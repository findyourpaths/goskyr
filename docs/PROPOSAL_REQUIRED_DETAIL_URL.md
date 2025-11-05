# Proposal: Required Detail URL Filter for Configuration Generation

**Status**: Proposed
**Created**: 2025-11-05
**Author**: Human + Claude

---

## Table of Contents

1. [Problem Statement](#problem-statement)
2. [Motivation](#motivation)
3. [Proposed Solution](#proposed-solution)
4. [Design Questions](#design-questions)
5. [Implementation Plan](#implementation-plan)
6. [Testing Strategy](#testing-strategy)
7. [Alternatives Considered](#alternatives-considered)
8. [Success Criteria](#success-criteria)

---

## Problem Statement

When generating goskyr configurations with the `generate` command, the tool may create configurations for detail pages that do not contain a specific required URL. This results in configurations that:

1. **Waste time** - Generate scrapers for irrelevant detail pages
2. **Pollute output** - Create multiple configuration files, many of which are not useful
3. **Complicate selection** - Make it harder to identify the correct configuration to use
4. **Increase iteration time** - Require manual filtering of generated configurations

**Current behavior**:
```bash
goskyr generate --list-url https://example.com/events
# Generates configs for ALL detail page URLs found in list page
# Result: 10+ configuration files, only 1-2 are relevant
```

**Desired behavior**:
```bash
goskyr generate --list-url https://example.com/events \
  --require-detail-url https://example.com/events/important-event
# Generates configs ONLY for detail pages that include this specific URL
# Result: 1-2 relevant configuration files
```

---

## Motivation

### Real-World Use Case

When generating scrapers for spiritual event listings, list pages often contain links to:
- **Event detail pages** (target): `https://ifs-institute.com/event/123`
- **Profile pages**: `https://ifs-institute.com/practitioners/jane-doe`
- **General info pages**: `https://ifs-institute.com/about`
- **External ticketing**: `https://eventbrite.com/e/12345`

**Problem**: Goskyr generates configurations for ALL of these link types, creating 10+ config files when we only want the event detail scraper.

**Solution**: Provide a filter to specify one known detail page URL that MUST be included in the generated configuration. This ensures we only generate scrapers that will extract the desired page type.

### Benefits

1. **Faster iteration** - Generate only relevant configurations
2. **Clearer intent** - Explicitly specify what type of detail page is needed
3. **Fewer false positives** - Avoid generating scrapers for wrong page types
4. **Better automation** - Enable scripts to generate targeted configurations
5. **Consistent with existing patterns** - Similar to `--require-string` flag for field content

---

## Proposed Solution

### Command-Line Interface

Add a new flag to the `generate` command:

```bash
goskyr generate --list-url <URL> --require-detail-url <URL>
```

**Flag specification**:
- **Name**: `--require-detail-url`
- **Type**: `string`
- **Default**: `""` (no filtering)
- **Description**: "Only generate configurations if the detail pages include this specific URL. The URL must be extracted by the list page scraper."

### Behavior

1. **List page scraping** proceeds as normal
2. **Detail URL collection** proceeds as normal (all URL fields extracted)
3. **Filtering step** (NEW):
   - For each field containing URLs (e.g., `href`, `reg_url`, `link`)
   - Check if `--require-detail-url` value is present in the list of URLs
   - If NOT present, skip generating detail page config for this field
   - If present, continue with detail page config generation
4. **Configuration generation** only for filtered fields

### Example Usage

```bash
# Scenario 1: Filter for specific event detail page
goskyr generate \
  --list-url "https://ifs-institute.com/practitioners?page=0" \
  --require-detail-url "https://ifs-institute.com/event/annual-conference-2025"

# Result: Only generates detail page config if list page contains this event URL

# Scenario 2: No filter (existing behavior)
goskyr generate \
  --list-url "https://ifs-institute.com/practitioners?page=0"

# Result: Generates detail page configs for ALL URL fields (existing behavior)
```

---

## Design Questions

### Q1: Where should the filtering logic be implemented?

**Options**:
- **A**: In `ConfigurationsForAllDetailPages()` before collecting URLs
- **B**: In `ConfigurationsForAllDetailPages()` after collecting URLs, before generating configs
- **C**: In `ConfigurationsForDetailPages()` as early return

**Recommendation**: **Option B** - yes

**Rationale**:
- Need to collect all URLs first to check if required URL is present
- Filter at the field level (each field's URL list checked independently)
- Early enough to avoid unnecessary work (fetching/analyzing pages)
- Late enough to have complete URL data for filtering

**Location**: `generate/generate.go:ConfigurationsForAllDetailPages()` at line ~980, right before iterating over `fnames`.

### Q2: Should the match be exact or partial?

**Options**:
- **A**: Exact match - `require-detail-url` must exactly match a URL in the list
- **B**: Substring match - `require-detail-url` can be a substring of any URL
- **C**: Regex match - `require-detail-url` is a regex pattern

**Recommendation**: **Option B (Substring match)** - no, exact

**Rationale**:
- **Flexible**: Allows matching `example.com/event/123` even if actual URL has query params
- **User-friendly**: Don't need to know exact URL format with all parameters
- **Consistent**: Matches behavior of existing `--require-string` flag (uses substring matching)
- **Simple**: Easy to understand and document

**Trade-off**: May match more than intended if substring is too generic (e.g., `/event` matches `/events-calendar`)

**Mitigation**: Document best practices - use specific enough substrings (e.g., `/event/123` not `/event`)

### Q3: Should we log/report which URLs were filtered out?

**Options**:
- **A**: Silent filtering (no output)
- **B**: Info-level logging showing filtered fields
- **C**: Verbose mode with full URL lists

**Recommendation**: **Option B (Info-level logging)** - yes

**Rationale**:
- **Transparency**: User knows why certain configs weren't generated
- **Debugging**: Can verify filtering worked as expected
- **Not overwhelming**: Single log line per filtered field

**Implementation**:
```go
if !urlListContains(fieldURLs, opts.RequireDetailURL) {
    slog.Info("Skipping field due to --require-detail-url filter",
        "field_name", fname,
        "required_url", opts.RequireDetailURL,
        "url_count", len(fieldURLs))
    continue
}
```

### Q4: What happens if no configurations match the filter?

**Options**:
- **A**: Return error
- **B**: Return empty result with warning log
- **C**: Fall back to generating all configs (ignore filter)

**Recommendation**: **Option B (Empty result with warning)** - yes

**Rationale**:
- **Clear intent**: User specified a filter, respect it even if nothing matches
- **Obvious feedback**: Warning log makes it clear why no configs generated
- **Correct behavior**: Better to generate nothing than wrong configs
- **Matches expectations**: If filter doesn't match, user likely gave wrong URL

**Implementation**:
```go
if len(rs) == 0 && opts.RequireDetailURL != "" {
    slog.Warn("No detail page configurations generated - required URL not found",
        "required_url", opts.RequireDetailURL,
        "list_url", opts.URL)
}
```

### Q5: Should this filter apply to list page configs too?

**Options**:
- **A**: Only filter detail pages (proposed)
- **B**: Also filter list page configs based on extracted URLs
- **C**: Separate flags for list vs detail filtering

**Recommendation**: **Option A (Only detail pages)** - yes

**Rationale**:
- **Original intent**: Problem is with detail page explosion, not list pages
- **Simpler**: One filter, one purpose, easy to understand
- **List pages are input**: User already specifies list page URL explicitly
- **Future flexibility**: Can add `--require-list-url` later if needed

### Q6: How should the flag interact with `--require-string`?

**Options**:
- **A**: Independent - both filters applied separately
- **B**: Combined AND - must satisfy both filters
- **C**: Combined OR - must satisfy at least one filter

**Recommendation**: **Option B (Combined AND)** - yes

**Rationale**:
- **Logical composition**: Multiple filters = more restrictive = AND
- **Intuitive**: "Require X AND require Y" = both must be true
- **Flexible**: User can omit either flag if not needed
- **Consistent**: Standard filter composition pattern

**Behavior**:
```bash
# Both filters active
goskyr generate \
  --require-string "Conference 2025" \
  --require-detail-url "https://example.com/event/123"
# Generates configs only if:
#   1. List page extracts string "Conference 2025" (existing filter)
#   2. Detail pages include URL "https://example.com/event/123" (new filter)

# Only URL filter active
goskyr generate \
  --require-detail-url "https://example.com/event/123"
# Generates configs only if detail pages include this URL
```

---

## Implementation Plan

### Phase 0: Design (Current Phase)

**Status**: ✅ Complete

**Deliverables**:
- [x] Proposal document (this file)
- [x] Design questions answered
- [x] Implementation approach defined

**Git commit**: None (proposal only)

---

### Phase 1: Add CLI Flag and Configuration Plumbing

**Status**: ✅ Complete

**Goal**: Add `--require-detail-url` flag and thread it through the codebase without implementing filtering logic yet.

**Changes**:

1. **cmd/goskyr/main.go** (Line ~92):
   ```go
   type GenerateCmd struct {
       // ... existing fields ...
       RequireString       string `help:"Require a candidate configuration to extract the given text in order for it to be generated."`
       RequireDetailURL    string `help:"Only generate detail page configurations if they include this URL."` // NEW
       WordsDir            string `short:"w" default:"word-lists" description:"The directory that contains a number of files containing words of different languages. This is needed for the ML part (use with -e or -b)."`
   }
   ```

2. **cmd/goskyr/main.go** `GenerateCmd.Run()` (Line ~126):
   ```go
   opts, err := generate.InitOpts(generate.ConfigOptions{
       // ... existing options ...
       RequireString:              cmd.RequireString,
       RequireDetailURL:           cmd.RequireDetailURL, // NEW
       WordsDir:                   cmd.WordsDir,
   })
   ```

3. **generate/generate.go** `ConfigOptions` struct (Line ~25):
   ```go
   type ConfigOptions struct {
       // ... existing fields ...
       RequireString              string
       RequireDetailURL           string // NEW
       WordsDir                   string
   }
   ```

**Testing**:
- ✅ Run `goskyr generate --help` and verify new flag appears
- ✅ Run with `--require-detail-url <URL>` and verify no errors (even though not functional yet)
- ✅ Build succeeds with no compilation errors

**Git commit** (ready for human to run):
```
Add --require-detail-url flag to generate command

Add CLI flag and thread through configuration without implementing
filtering logic. This prepares for Phase 2 implementation.

Changes:
- cmd/goskyr/main.go: Add RequireDetailURL field to GenerateCmd
- generate/generate.go: Add RequireDetailURL to ConfigOptions

Tests: Manual CLI testing
Status: Flag present but not functional yet
```

---

### Phase 2: Implement Filtering Logic

**Goal**: Add filtering logic to skip detail page config generation when required URL not present.

**Changes**:

1. **generate/generate.go** `ConfigurationsForAllDetailPages()` (After line ~975):
   ```go
   for _, fURLs := range fieldURLsByFieldName {
       sort.Strings(fURLs)
   }

   // NEW: Filter out fields that don't contain required detail URL
   if opts.RequireDetailURL != "" {
       slog.Info("Filtering detail page fields by required URL",
           "required_url", opts.RequireDetailURL,
           "total_fields", len(pageJoinsByFieldName))

       filteredCount := 0
       for fname := range pageJoinsByFieldName {
           fieldURLs := fieldURLsByFieldName[fname]
           if !urlListContains(fieldURLs, opts.RequireDetailURL) {
               slog.Info("Skipping field - required URL not found",
                   "field_name", fname,
                   "required_url", opts.RequireDetailURL,
                   "url_count", len(fieldURLs))
               delete(pageJoinsByFieldName, fname)
               delete(fieldURLsByFieldName, fname)
               filteredCount++
           }
       }

       slog.Info("Filtered detail page fields",
           "filtered_out", filteredCount,
           "remaining", len(pageJoinsByFieldName))

       if len(pageJoinsByFieldName) == 0 {
           slog.Warn("No detail page configurations match filter",
               "required_url", opts.RequireDetailURL,
               "list_url", opts.URL)
       }
   }

   for fname := range pageJoinsByFieldName {
       fnames = append(fnames, fname)
   }
   // ... rest of function continues ...
   ```

2. **generate/generate.go** - Add helper function (near top of file):
   ```go
   // urlListContains checks if any URL in the list contains the required substring.
   // Uses substring matching for flexibility (e.g., matches with or without query params).
   func urlListContains(urls []string, required string) bool {
       if required == "" {
           return true // No filter = match all
       }
       for _, u := range urls {
           if strings.Contains(u, required) {
               return true
           }
       }
       return false
   }
   ```

**Testing**:
- Run with `--require-detail-url` and verify filtering works
- Check logs to see filtered vs remaining fields
- Verify empty result when no match

**Git commit**:
```
Implement detail URL filtering in configuration generation

Add filtering logic to skip detail page configurations when the
required URL is not present in the field's extracted URLs.

Changes:
- generate/generate.go: Add filtering in ConfigurationsForAllDetailPages()
- generate/generate.go: Add urlListContains() helper function
- Uses substring matching for flexibility
- Logs filtered fields for transparency

Tests: Manual testing with various URLs
Status: Feature complete
```

---

### Phase 3: Add Tests

**Goal**: Add test cases to verify filtering behavior.

**Changes**:

1. **cmd/goskyr/main_test.go** - Add test cases:
   ```go
   func TestRequireDetailURLFiltering(t *testing.T) {
       tests := []struct {
           name              string
           listURL           string
           requireDetailURL  string
           expectedConfigCount int
       }{
           {
               name:              "No filter - all configs generated",
               listURL:           "https://example.com/events",
               requireDetailURL:  "",
               expectedConfigCount: 3, // Example: href, reg_url, link
           },
           {
               name:              "Match found - config generated",
               listURL:           "https://example.com/events",
               requireDetailURL:  "https://example.com/event/123",
               expectedConfigCount: 1, // Only matching field
           },
           {
               name:              "No match - no configs",
               listURL:           "https://example.com/events",
               requireDetailURL:  "https://different.com/event/999",
               expectedConfigCount: 0,
           },
           {
               name:              "Substring match - config generated",
               listURL:           "https://example.com/events",
               requireDetailURL:  "/event/123", // Matches even without domain
               expectedConfigCount: 1,
           },
       }

       for _, tt := range tests {
           t.Run(tt.name, func(t *testing.T) {
               // Test implementation here
               // Use testdata with known URL structure
           })
       }
   }
   ```

2. **testdata/filtering/** - Add test fixtures:
   - Create test HTML pages with known URL patterns
   - Document expected filtering behavior

**Testing**:
- Run `go test ./cmd/goskyr -v`
- Verify all test cases pass

**Git commit**:
```
Add tests for detail URL filtering

Add test cases to verify --require-detail-url filtering behavior
including no filter, match found, no match, and substring matching.

Changes:
- cmd/goskyr/main_test.go: Add TestRequireDetailURLFiltering()
- testdata/filtering/: Add test fixtures

Tests: go test ./cmd/goskyr -v (all passing)
Status: Tests complete
```

---

### Phase 4: Documentation

**Goal**: Document the new feature in README and DESIGN docs.

**Changes**:

1. **README.md** - Add to "Semi-Automatic Configuration" section (After line ~78):
   ```markdown
   - `--require-detail-url`: Only generate detail page configurations if they
     include this specific URL. Useful for filtering out irrelevant detail page
     types (e.g., profile pages, external ticketing pages) when you only want
     configurations for a specific detail page type (e.g., event pages).
     Uses substring matching, so you can specify `/event/123` to match
     `https://example.com/event/123?param=value`.
   ```

2. **README.md** - Add usage example (After line ~56):
   ```markdown
   ### Filtering Detail Pages

   When generating configurations, you may want to focus only on specific
   types of detail pages. Use `--require-detail-url` to filter:

   ```bash
   # Generate configs only for event detail pages, skip profile/external pages
   goskyr generate \
     --list-url "https://ifs-institute.com/practitioners?page=0" \
     --require-detail-url "https://ifs-institute.com/event/conference-2025"
   ```

   The filter uses substring matching, so you can use partial URLs:

   ```bash
   # Match any URL containing "/event/"
   goskyr generate \
     --list-url "https://example.com/events" \
     --require-detail-url "/event/"
   ```
   ```

3. **docs/DESIGN.md** - Document in "Generate Package" section:
   ```markdown
   ### Detail Page Filtering

   The `--require-detail-url` flag filters detail page configurations by URL:

   1. **Collection**: All detail page URLs extracted from list page
   2. **Filtering**: For each URL field, check if required URL is present
   3. **Skipping**: If required URL not found, skip config generation for that field
   4. **Logging**: Info-level logs show which fields were filtered

   **Match Strategy**: Substring matching for flexibility
   **Combination**: Works with `--require-string` (AND logic)
   **Edge Cases**: Empty result with warning if no matches
   ```

**Testing**:
- Review documentation for accuracy
- Verify examples work as documented

**Git commit**:
```
Document --require-detail-url feature

Add documentation for detail URL filtering feature in README and
DESIGN docs with usage examples and implementation details.

Changes:
- README.md: Add flag description and usage examples
- docs/DESIGN.md: Document filtering algorithm

Status: Documentation complete
```

---

### Phase Final: Proposal Archival

**Goal**: Archive this proposal and update documentation index.

**Changes**:

1. Move proposal:
   ```bash
   git mv docs/PROPOSAL_REQUIRED_DETAIL_URL.md docs/completed/
   ```

2. Update this proposal's status section:
   ```markdown
   **Status**: ✅ Complete (2025-11-05)
   ```

3. Update docs/SUMMARY.md (if it exists) or README to note feature is complete.

**Git commit**:
```
Complete detail URL filtering feature

Archive proposal and update documentation to reflect completed feature.

Changes:
- docs/PROPOSAL_REQUIRED_DETAIL_URL.md → docs/completed/
- Update status to Complete

All phases complete:
✅ Phase 1: CLI flag and plumbing
✅ Phase 2: Filtering logic
✅ Phase 3: Tests
✅ Phase 4: Documentation
✅ Phase Final: Proposal archival
```

---

## Testing Strategy

### Manual Testing

1. **No filter** (baseline):
   ```bash
   goskyr generate --list-url "https://example.com/events"
   # Verify: All detail page configs generated (existing behavior)
   ```

2. **Filter with match**:
   ```bash
   goskyr generate \
     --list-url "https://example.com/events" \
     --require-detail-url "https://example.com/event/123"
   # Verify: Only configs for fields containing this URL
   ```

3. **Filter with no match**:
   ```bash
   goskyr generate \
     --list-url "https://example.com/events" \
     --require-detail-url "https://nonexistent.com/fake"
   # Verify: No detail configs generated, warning logged
   ```

4. **Substring matching**:
   ```bash
   goskyr generate \
     --list-url "https://example.com/events" \
     --require-detail-url "/event/123"
   # Verify: Matches URLs with this substring
   ```

5. **Combined filters**:
   ```bash
   goskyr generate \
     --list-url "https://example.com/events" \
     --require-string "Conference" \
     --require-detail-url "/event/123"
   # Verify: Both filters applied (AND logic)
   ```

### Automated Testing

**Test cases** (see Phase 3):
- No filter (baseline)
- Filter with exact match
- Filter with substring match
- Filter with no match
- Filter with query parameters
- Combined with `--require-string`

### Edge Cases

1. **Empty list page** - No URLs extracted
   - Expected: No detail configs, no error

2. **Multiple fields with same URLs**
   - Expected: Both filtered the same way (existing behavior)

3. **URL with redirects**
   - Expected: Filter checks resolved URL (existing resolution logic)

4. **Case sensitivity**
   - Expected: Case-sensitive matching (standard Go `strings.Contains`)

---

## Alternatives Considered

### Alternative 1: Regex Pattern Filter

**Approach**: Use `--require-detail-url-pattern <regex>` with regex matching.

**Pros**:
- More powerful (can match complex patterns)
- More precise (can anchor to start/end)

**Cons**:
- More complex for users (regex syntax)
- Overkill for common use case (just need substring)
- Harder to document and explain

**Decision**: **Rejected** - Substring matching is sufficient for 90% of use cases.

### Alternative 2: Allowlist/Blocklist

**Approach**: Use `--allow-detail-domains` and `--block-detail-domains` to filter by domain.

**Pros**:
- Intuitive for domain-level filtering
- Matches existing `OnlyKnownDomainDetailPages` pattern

**Cons**:
- Less precise (can't filter by path)
- Requires separate flags for allow/block
- Doesn't solve specific URL filtering need

**Decision**: **Rejected** - Domain filtering already exists via `OnlyKnownDomainDetailPages`.

### Alternative 3: Multiple Required URLs (OR logic)

**Approach**: Accept multiple URLs: `--require-detail-url URL1,URL2,URL3`

**Pros**:
- More flexible (match any of several URLs)
- Useful for multi-variant pages

**Cons**:
- More complex interface
- Rare use case (usually want one specific URL)
- Can be simulated with less specific substring

**Decision**: **Deferred** - Start simple with single URL, add multiple later if needed.

### Alternative 4: Post-Generation Filtering

**Approach**: Generate all configs, then filter files based on records.

**Pros**:
- Separation of concerns (generation vs filtering)
- Could filter on more criteria (field values, record count, etc.)

**Cons**:
- Wasteful (generates configs just to delete them)
- Slower (fetches/analyzes unnecessary pages)
- Less clear intent (filtering after the fact)

**Decision**: **Rejected** - Better to filter during generation (avoid wasted work).

---

## Success Criteria

### Functional Requirements

- [x] `--require-detail-url` flag added to CLI
- [ ] Flag filters detail page configurations by URL substring
- [ ] No filtering when flag is empty (backward compatible)
- [ ] Info-level logging shows filtered fields
- [ ] Warning logged when no configurations match filter
- [ ] Works independently and combined with `--require-string`

### Non-Functional Requirements

- [ ] No performance regression (filtering is O(n) over URL lists)
- [ ] Backward compatible (no breaking changes)
- [ ] Well-documented (README, DESIGN, help text)
- [ ] Tested (manual + automated test cases)

### User Experience

- [ ] Reduces generated configuration count when used
- [ ] Clear feedback about what was filtered and why
- [ ] Intuitive flag name and behavior
- [ ] Easy to use for common cases (substring matching)

---

## Open Questions

1. **Should we add `--require-detail-url-regex` for advanced users?**
   - Decision: Defer to future if demand arises

2. **Should we support multiple required URLs (OR logic)?**
   - Decision: Defer to future, single URL is sufficient for now

3. **Should filtering also apply to list page configs?**
   - Decision: No, list pages are explicitly specified by user

4. **Case-sensitive or case-insensitive matching?**
   - Decision: Case-sensitive (standard Go behavior)

---

## References

- **Existing similar flag**: `--require-string` (cmd/goskyr/main.go:107)
- **Detail page logic**: `generate/generate.go:ConfigurationsForAllDetailPages()`
- **URL collection**: `generate/generate.go:820-1017`
- **Field filtering pattern**: `generate/generate.go:982-993` (duplicate URL field check)

---

## Revision History

| Date | Version | Changes |
|------|---------|---------|
| 2025-11-05 | 1.0 | Initial proposal |

---

## Approval

**Design Questions**: ✅ Answered (6 questions)
**Implementation Plan**: ✅ Defined (4 phases)
**Testing Strategy**: ✅ Documented
**Alternatives**: ✅ Considered (4 alternatives)

**Ready for Phase 1**: ⏸️ Awaiting human approval
