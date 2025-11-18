# Proposal: Add MinRecords Filter

**Status**: ✅ Implemented (tests and docs postponed)
**Created**: 2025-11-18
**Implemented**: 2025-11-18
**Author**: Human + Claude
**Priority**: Medium (quality improvement, non-breaking)

**Note**: Core implementation is complete and functional. Unit tests, integration tests, and documentation updates are postponed to a future iteration.

## Summary

Add a `MinRecords` parameter to filter out generated scrapers that don't produce at least a minimum number of records in the generated JSON output. This will improve scraper quality by eliminating configurations that extract too few records to be useful.

## Motivation

### Current Problem

The `goskyr generate` command produces many scraper configurations, some of which extract very few records (e.g., 1-2 records). These low-record configurations are often:

- **False positives** from pattern detection
- **Overly specific selectors** that only match a subset of records
- **Noise** from non-repeating page elements (headers, footers, navigation)
- **Failed scrapers** that partially matched patterns

### Example Output
```
Extracted records to: .../debhorton-com-events__n15aa_7.json       # 7 records
Extracted records to: .../debhorton-com-events__n30a_1.json        # 1 record
Extracted records to: .../debhorton-com-events__n15ab_30.json      # 30 records ✓
Extracted records to: .../debhorton-com-events__n20aaaa_7.json     # 7 records
```

Most users want configs that extract a substantial number of records (e.g., 20+), but must manually filter through dozens of low-quality configs.

### Why MinOccs Isn't Enough

The existing `MinOccs` parameter filters at the **HTML analysis phase** (requiring fields to appear N times in the DOM), but doesn't guarantee the final scraper will produce N records.

**MinOccs vs Actual Records** (from recent test run):
- `MinOccs=30` → extracted **1 record** (`n30a_1.json`)
- `MinOccs=30` → extracted **15 records** (`n30aa_15.json`)
- `MinOccs=30` → extracted **30 records** (`n30ab_30.json`)
- `MinOccs=15` → extracted **7 records** (`n15aa_7.json`)
- `MinOccs=15` → extracted **30 records** (`n15ab_30.json`)

**Why the discrepancy?** A scraper might pass `MinOccs=15` but still only extract 2 records due to:
- Selector specificity issues (overly precise CSS selectors)
- Root selector adjustment that filters out records
- Field validation failures (RequireString, RequireDates)
- Clustering strategy differences (nested vs sequential)

See `docs/analyzed/MINOCCS_VS_RECORD_COUNT_ANALYSIS.md` for detailed explanation.

### Current Workaround

`ConfigurationsForDetailPages` already uses a hardcoded `< 2` check (line 1134 in `generate.go`):

```go
if DoPruning && len(mergedC.Records) < 2 {
    slog.Info("candidate detail page configuration failed to produce more than one record, pruning")
    continue
}
```

This proposal generalizes this approach for all scrapers (list and detail pages).

## Proposed Changes

### 1. Add MinRecords to ConfigOptions

**File:** `generate/generate.go`

Add field to `ConfigOptions` struct (around line 217):

```go
type ConfigOptions struct {
    Batch bool
    ConfigOutputParentDir      string
    ConfigOutputDir            string
    DoDetailPages              bool
    MinOccs                    []int
    MinRecords                 int    // NEW: Minimum records required in output
    ModelName                  string
    Offline                    bool
    OnlyKnownDomainDetailPages bool
    OnlyVaryingFields          bool
    RenderJS                   bool
    RequireDates               bool
    RequireDetailURL           string
    RequireString              string
    URL                        string
    WordsDir                   string
    configID                   scrape.ConfigID
    configPrefix               string
}
```

**Default value:** `0` (no filtering) for backward compatibility

### 2. Add Command-Line Flag

**File:** `cmd/goskyr/main.go`

Add flag to `GenerateCmd` struct (around line 99):

```go
type GenerateCmd struct {
    URL string `arg:"" help:"Automatically generate a config file for the given input url."`

    Batch                      bool   `short:"b" long:"batch" default:true help:"Run batch (not interactively) to generate the config file."`
    DoDetailPages              bool   `default:true help:"Whether to generate configurations for detail page as well."`
    OnlyKnownDomainDetailPages bool   `default:true help:"Only go to detail pages on the same domain or a known ticketing domain."`
    OnlyVaryingFields          bool   `default:true help:"Only show fields that have varying values across the list of records."`
    MinOcc                     int    `short:"m" long:"min" help:"The minimum number of field occurrences on a page. Works in combination with the -g flag."`
    MinRecords                 int    `long:"min-records" default:"0" help:"Minimum number of records a scraper must produce (0 = no minimum)."`  // NEW
    CacheInputParentDir        string `default:"/tmp/goskyr/main/" help:"Parent directory for cached input html pages."`
    // ... rest of fields
}
```

Pass to `InitOpts` (around line 128):

```go
opts, err := generate.InitOpts(generate.ConfigOptions{
    Batch:                      cmd.Batch,
    ConfigOutputParentDir:      cmd.ConfigOutputParentDir,
    DoDetailPages:              cmd.DoDetailPages,
    URL:                        cmd.URL,
    MinOccs:                    minOccs,
    MinRecords:                 cmd.MinRecords,  // NEW
    ModelName:                  cmd.PretrainedModelPath,
    Offline:                    cmd.Offline,
    OnlyKnownDomainDetailPages: cmd.OnlyKnownDomainDetailPages,
    OnlyVaryingFields:          cmd.OnlyVaryingFields,
    // ... rest of fields
})
```

### 3. Apply Filter in List Page Generation

**File:** `generate/generate.go`

**Location:** In `expandAllPossibleConfigsWithDepth` after scraping nested config (around line 567)

**Current code:**
```go
slog.Info("in expandAllPossibleConfigs(), scraping nested")
recs, err = scrape.GQDocument(ctx, nestedConfig, &s, gqdoc)
if err != nil {
    slog.Info("candidate configuration got error scraping GQDocument, excluding", "opts.configID", nestedOpts.configID)
    status = "failed_to_scrape_gqdoc"
    return nil, err
}
slog.Info("in expandAllPossibleConfigs(), scraped nested", "len(recs)", len(recs))
nestedConfig.Records = recs
```

**Add after setting Records:**
```go
slog.Info("in expandAllPossibleConfigs(), scraped nested", "len(recs)", len(recs))
nestedConfig.Records = recs

// NEW: Check MinRecords threshold
if opts.MinRecords > 0 && len(recs) < opts.MinRecords {
    slog.Info("candidate configuration produced too few records, excluding",
        "opts.configID", nestedOpts.configID,
        "records_produced", len(recs),
        "min_required", opts.MinRecords)
    status = "failed_min_records"
    return rs, nil
}
```

**Do the same for sequential config** (around line 585):

**Current code:**
```go
// Add sequential config to results if it produces unique records
seqRecsStr := seqRecs.String()
if _, found := rs[seqRecsStr]; !found {
    rs[seqRecsStr] = seqConfig
    slog.Info("added sequential config", "id", seqConfig.ID.String())
} else {
    slog.Info("sequential config produces duplicate records, skipping", "id", seqConfig.ID.String())
}
```

**Replace with:**
```go
// Add sequential config to results if it produces unique records
seqRecsStr := seqRecs.String()

// NEW: Check MinRecords before adding
if opts.MinRecords > 0 && len(seqRecs) < opts.MinRecords {
    slog.Info("sequential config produced too few records, skipping",
        "id", seqConfig.ID.String(),
        "records_produced", len(seqRecs),
        "min_required", opts.MinRecords)
} else if _, found := rs[seqRecsStr]; !found {
    rs[seqRecsStr] = seqConfig
    slog.Info("added sequential config", "id", seqConfig.ID.String())
} else {
    slog.Info("sequential config produces duplicate records, skipping", "id", seqConfig.ID.String())
}
```

### 4. Apply Filter in Detail Page Generation

**File:** `generate/generate.go`

**Location:** In `ConfigurationsForDetailPages` (replace hardcoded check at line 1134)

**Current code:**
```go
if DoPruning && len(mergedC.Records) < 2 {
    slog.Info("candidate detail page configuration failed to produce more than one record, pruning", "opts.configID", opts.configID)
    continue
}
```

**Replace with:**
```go
// Use MinRecords if specified, otherwise default to 2 for detail pages
minRecords := 2
if opts.MinRecords > 0 {
    minRecords = opts.MinRecords
}
if DoPruning && len(mergedC.Records) < minRecords {
    slog.Info("candidate detail page configuration produced too few records, pruning",
        "opts.configID", opts.configID,
        "records_produced", len(mergedC.Records),
        "min_required", minRecords)
    continue
}
```

## Implementation Details

### Placement in Pipeline

The filter is applied **after scraping** but **before adding configs to results**. This placement is optimal because:

1. **Completeness:** All possible scrapers are still generated and tested
2. **Efficiency:** We avoid writing configs that don't meet the threshold
3. **Consistency:** Similar to existing filters (`RequireString`, `RequireDates`)
4. **Child exploration:** Doesn't prevent recursive exploration of config space

### Status Tracking

Add new observability status: `"failed_min_records"`

This allows tracking how many configs were filtered out:

```go
status = "failed_min_records"
```

### Interaction with Other Filters

MinRecords is applied **after** other validation filters:
1. `RequireString` validation
2. `RequireDates` validation
3. Record extraction
4. **MinRecords check** ← NEW
5. Add to results map

This ensures we only count records that passed all other validations.

## Use Cases

### Use Case 1: Event Listing Pages
```bash
# Only keep configs that extract at least 20 events
goskyr generate --min-records=20 https://example.com/events
```

**Expected:** Filters out noise configs (navigation elements, single featured events)

### Use Case 2: Product Catalogs
```bash
# Only keep configs that extract at least 50 products
goskyr generate --min-records=50 https://shop.example.com/category/books
```

**Expected:** Ensures substantial product lists, filters out sidebar recommendations

### Use Case 3: Article Archives
```bash
# Only keep configs that extract at least 10 articles
goskyr generate --min-records=10 https://blog.example.com/archive
```

**Expected:** Gets paginated article lists, filters out "recent posts" widgets

### Use Case 4: No Filter (Default)
```bash
# Generate all configs (backward compatible)
goskyr generate https://example.com/events
```

**Expected:** Same behavior as before (`MinRecords=0` means no filtering)

## Relationship to MinOccs

| Parameter | Purpose | Phase | Granularity | Example |
|-----------|---------|-------|-------------|---------|
| `MinOccs` | Filter field patterns | Analysis (pre-scrape) | DOM elements | "Find fields appearing 15+ times in HTML" |
| `MinRecords` | Filter output quality | Post-scrape | Final records | "Only keep scrapers producing 20+ records" |

### Complementary, Not Redundant

**MinOccs** asks: *"How many times does this field pattern appear in the HTML?"*
- Controls which fields are detected during analysis
- Indirect effect on record count

**MinRecords** asks: *"How many records did this scraper actually extract?"*
- Direct quality threshold on final output
- Ensures useful scraper configurations

### Example Workflow
```bash
goskyr generate --min=15 --min-records=20 https://example.com/events
```

1. **MinOccs=15**: Include fields appearing 15+ times in HTML
   - Generates scrapers with various field combinations
2. **Scrape**: Extract data using generated configs
   - Some configs produce 30 records, others produce 7
3. **MinRecords=20**: Only keep scrapers producing 20+ records
   - Filters out low-quality configs (7, 15 records)
   - Keeps high-quality configs (30 records)

## Testing Strategy

### 1. Unit Tests

**File:** `generate/generate_test.go`

```go
func TestMinRecordsFilter(t *testing.T) {
    tests := []struct {
        name           string
        minRecords     int
        actualRecords  int
        shouldBeKept   bool
    }{
        {"no filter", 0, 5, true},
        {"meets threshold", 10, 15, true},
        {"exact threshold", 10, 10, true},
        {"below threshold", 10, 5, false},
        {"zero records", 10, 0, false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

### 2. Integration Tests

Test with real HTML fixtures:

```bash
# Test with debhorton.com cached page
go test -v ./cmd/goskyr -run TestGenerateWithMinRecords
```

Verify:
- Configs with 30 records are kept (when `MinRecords=20`)
- Configs with 7 records are filtered (when `MinRecords=20`)
- Observability metrics show `failed_min_records` count

### 3. Regression Tests

Ensure backward compatibility:

```bash
# Default (MinRecords=0) should behave exactly as before
goskyr generate <url>

# Compare output file count and content
diff <(ls old_output/*.json | wc -l) <(ls new_output/*.json | wc -l)
```

## Benefits

### 1. Reduced Noise
Users get fewer, higher-quality configs to choose from.

**Before:**
```
Generated 60 page configurations
  - 15 configs with 1-5 records (noise)
  - 20 configs with 6-15 records (marginal)
  - 25 configs with 20+ records (useful)
```

**After (MinRecords=20):**
```
Generated 25 page configurations
  - 25 configs with 20+ records (useful)
```

### 2. Faster Manual Review
Users spend less time inspecting and discarding low-quality configs.

### 3. Better Defaults for Automation
When integrating goskyr into automated pipelines, MinRecords provides a simple quality gate.

### 4. Clearer Semantics
Users can directly specify: *"I want scrapers that extract at least N records"*

## Edge Cases

### Child Scrapers (Recursive Exploration)

Child scrapers are still generated because filtering happens **after** the parent config is scraped but **before** the recursive call returns. The recursion still executes:

```go
rs, err = expandAllPossibleConfigsWithDepth(ctx, cache, exsCache, gqdoc, nextOpts,
                                            clusterID, nextLPs, nextRootSel, pagProps, rs, depth+1)
```

This ensures we don't prematurely stop exploring the configuration space.

### Detail Pages

Detail pages currently use a hardcoded threshold of 2 records. With this proposal:
- If `MinRecords=0` (default): Use 2 (current behavior)
- If `MinRecords>0`: Use user's value (configurable)

### Sequential vs Nested Strategies

Both strategies are filtered independently, ensuring we don't keep low-quality configs just because they use a different strategy.

### Empty Result Set

If MinRecords is set too high, no configs may pass the filter:

```bash
goskyr generate --min-records=100 <url-with-30-records>
# Result: Generated 0 page configurations
```

**Mitigation:** Add warning if no configs pass the filter:
```
Warning: No configurations produced at least 100 records.
Try lowering --min-records or check if the page has fewer records than expected.
```

## Success Criteria

### Functional
- ✅ Configs producing < MinRecords are filtered out
- ✅ Configs producing >= MinRecords are kept
- ✅ MinRecords=0 behaves exactly as current code (backward compatible)
- ✅ Works for both list pages and detail pages
- ✅ Works with both nested and sequential strategies

### Performance
- ✅ No measurable performance impact (filter is O(1) check per config)
- ✅ Reduces output file I/O (fewer configs written)

### Observability
- ✅ Logs show which configs were filtered: `status = "failed_min_records"`
- ✅ Final count shows: `Generated N page configurations` (after filtering)

### User Experience
- ✅ Clear CLI flag documentation
- ✅ Helpful error message when no configs pass filter
- ✅ Observability traces show filtering decisions

## Alternatives Considered

### Alternative 1: Post-Process Filtering
Filter configs after generation completes (in `ConfigurationsForPage`).

**Rejected because:**
- Configs would still be written to disk and observability logs
- Wasted computation on processing low-quality configs
- Less efficient than filtering during generation

### Alternative 2: MinOccs Tuning
Rely on MinOccs to indirectly control record count.

**Rejected because:**
- MinOccs doesn't directly control record count (see analysis doc)
- Requires trial-and-error to find right MinOccs value
- Different pages need different MinOccs values for same record count

### Alternative 3: Hardcoded Threshold
Keep using hardcoded `< 2` check for all scrapers.

**Rejected because:**
- Not configurable for different use cases
- 2 records might be too low for production scrapers
- User has no control over quality threshold

## Documentation Updates

### 1. CLI Help Text
Already included in flag definition:
```
--min-records=0  Minimum number of records a scraper must produce (0 = no minimum)
```

### 2. generate/DESIGN.md
Update "Configuration Output" section:

```markdown
## Configuration Filtering

Generated configurations are filtered based on several criteria:

- **MinOccs**: Fields must appear at least this many times in the HTML (analysis phase)
- **MinRecords**: Final scraper must produce at least this many records (post-scrape, default: 0)
- **RequireString**: Records must contain the specified text
- **RequireDates**: Records must include date fields
- **Pruning**: Duplicate record outputs are eliminated
```

### 3. README.md
Add example to usage section:

```markdown
### Generate with Quality Threshold

Only keep scrapers that extract at least 20 records:

\`\`\`bash
goskyr generate --min-records=20 https://example.com/events
\`\`\`

This filters out noise (navigation, headers) and low-quality configs.
```

## Migration Path

### Backward Compatibility
Setting `MinRecords=0` (the default) maintains current behavior exactly. **No breaking changes.**

### Recommended Usage

**For exploration/debugging:**
```bash
goskyr generate <url>  # MinRecords=0, see all configs
```

**For production scraping:**
```bash
goskyr generate --min-records=10 <url>  # Quality threshold
```

**For high-volume sites:**
```bash
goskyr generate --min-records=50 <url>  # Expect substantial lists
```

## Implementation Checklist

- [x] Add `MinRecords` field to `ConfigOptions` struct (generate/generate.go:218)
- [x] Add `--min-records` CLI flag to `GenerateCmd` (cmd/goskyr/main.go:100)
- [x] Pass `MinRecords` to `InitOpts` (cmd/goskyr/main.go:137)
- [x] Add filter in `expandAllPossibleConfigsWithDepth` (nested) (generate/generate.go:643-650)
- [x] Add filter in `expandAllPossibleConfigsWithDepth` (sequential) (generate/generate.go:588-604)
- [x] Update `ConfigurationsForDetailPages` to use `MinRecords` or default to 2 (generate/generate.go:1161-1172)
- [x] Add `"failed_min_records"` status to observability (generate/generate.go:650)
- [x] Add warning when no configs pass filter (generate/generate.go:348-353)
- [x] Add record count to JSON output filenames (scrape/scrape.go:146)
- [~] Write unit tests for filter logic (postponed)
- [~] Write integration test with real HTML fixture (postponed)
- [~] Update `generate/DESIGN.md` documentation (postponed)
- [~] Update `README.md` with examples (postponed)
- [~] Test backward compatibility (`MinRecords=0`) (postponed)

## Summary of Code Changes

| File | Change | Lines Affected |
|------|--------|----------------|
| `generate/generate.go` | Add `MinRecords` field to `ConfigOptions` | ~217 |
| `generate/generate.go` | Add filter in nested scraping | ~568-575 |
| `generate/generate.go` | Add filter in sequential scraping | ~585-592 |
| `generate/generate.go` | Update detail page threshold | ~1134-1142 |
| `cmd/goskyr/main.go` | Add `MinRecords` CLI flag | ~99 |
| `cmd/goskyr/main.go` | Pass `MinRecords` to `InitOpts` | ~135 |
| `generate/DESIGN.md` | Document `MinRecords` parameter | Various |
| `README.md` | Add usage examples | Various |

**Total:** ~8 locations, ~30-40 lines of code

## Related Documentation

- **Analysis**: `docs/analyzed/MINOCCS_VS_RECORD_COUNT_ANALYSIS.md`
  - Explains why MinOccs ≠ record count
  - Shows need for direct record count filtering
- **Existing Proposal**: `docs/proposed/PROPOSAL_MIN_LIST_RECORDS.md`
  - Similar proposal with name `MinListRecords` instead of `MinRecords`
  - This proposal uses shorter, more parallel naming with `MinOccs`
