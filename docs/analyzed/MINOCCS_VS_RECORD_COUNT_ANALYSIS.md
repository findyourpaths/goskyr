# MinOccs vs Record Count Analysis

**Date**: 2025-11-18
**Test Case**: debhorton.com/events scraper generation
**Question**: Why does the best scraper have MinOccs=15 when it produces 30 records?

## Executive Summary

**MinOccs is NOT the number of records extracted.** MinOccs is a threshold for filtering DOM patterns during HTML analysis. A MinOccs value of 15 can produce a scraper that extracts 30 records, and in fact, the MinOccs=15 scraper is often *better* than MinOccs=30 because it captures more fields per record.

## Test Run Details

### Command
```bash
env GOWORK=off time go run ./cmd/goskyr --log-level=info generate \
  --do-detail-pages=false \
  --require-detail-url 'https://www.debhorton.com/events/2023/8/30/blue-moon-type-four-group' \
  'https://www.debhorton.com/events'
```

### MinOccs Values Tested
Default values: `[15, 20, 25, 30]`
Tested in reverse order: `[30, 25, 20, 15]` (see `cmd/goskyr/main.go:121-122`, `generate/generate.go:333`)

### Best Result
**File**: `/tmp/goskyr/main/debhorton-com_configs/debhorton-com-events__n15ab_30.json`
- **MinOccs**: 15
- **Records extracted**: 30
- **Fields per record**: 28
- **File size**: 95KB

## What MinOccs Actually Controls

### Definition
**MinOccs** = Minimum occurrence threshold for DOM pattern filtering

From `generate/DESIGN.md`:
> **Detect Patterns** - Find repeating structures
> - Group elements by their DOM path
> - Count occurrences of each path
> - Keep paths that appear >= minimum occurrence threshold

### Analysis Phase (Before Scraping)
During HTML analysis, MinOccs filters out DOM paths based on their occurrence count:

- **MinOccs=30**: Only include fields that appear 30+ times in the HTML
- **MinOccs=15**: Include fields that appear 15+ times in the HTML

### Result: More Inclusive = More Fields
A lower MinOccs value allows the scraper to capture fields that appear in some, but not all, records.

## Comparison: n15ab vs n30ab

Both configs extracted from the same page with the same 30 records, but with different field counts:

| Config | MinOccs | Records | Fields/Record | File Size | Notes |
|--------|---------|---------|---------------|-----------|-------|
| `n15ab_30.json` | 15 | 30 | 28 | 95KB | **Best** - most complete |
| `n30ab_30.json` | 30 | 30 | 3 | 28KB | Minimal - only core fields |

### Field Count Verification
```bash
jq '.[0] | keys | length' /tmp/goskyr/.../n15ab_30.json  # Output: 28
jq '.[0] | keys | length' /tmp/goskyr/.../n30ab_30.json  # Output: 3
```

### Example: n30ab (MinOccs=30) - Only 3 Fields
```json
{
  "Atitle": "Events — Debbi Horton",
  "Aurl": "https://www.debhorton.com/events",
  "F112f04f7--0": "I am honored to serve as a Guide in EPP's Path to Freedom..."
}
```

### Example: n15ab (MinOccs=15) - 28 Fields
```json
{
  "Atitle": "Events — Debbi Horton",
  "Aurl": "https://www.debhorton.com/events",
  "F112f04f7--0": "I am honored to serve...",
  "F3e1f0305--0": "",
  "F542f8cb9-href-0": "/events/2022/3/29/...",
  "F5e924055-href-0": "/events/2022/3/29/...",
  "F5eb8cdc1--0": "Tue, Feb 6, 2024\nSun, May 19, 2024",
  "F5eb8cdc1--0__Pdate_time_tz_ranges": "2024-02-06TZ, 2024-05-19TZ",
  "F67623c64--0": "Feb",
  ... (20 more fields)
}
```

## Why MinOccs=15 Produced a Better Scraper

### Field Distribution in HTML
On the debhorton.com/events page:
- **Core fields** (title, URL, description): Appear 30 times (once per record)
- **Optional fields** (dates, images, links): Appear 15-29 times (not in every record)

### Filtering Effect

#### With MinOccs=30
- ✅ Keeps: Fields appearing 30+ times (core fields only)
- ❌ Filters out: Fields appearing 15-29 times (optional fields)
- **Result**: Minimal scraper with only 3 fields

#### With MinOccs=15
- ✅ Keeps: Fields appearing 15+ times (core + optional fields)
- ✅ Keeps: Fields that appear in at least half the records
- **Result**: Comprehensive scraper with 28 fields

### Real-World Data Variance
Web pages often have:
- **Required fields**: Present in every record (30/30)
- **Optional fields**: Present in most records (15-29/30)
  - Event dates (some events are past/undated)
  - Images (some events lack images)
  - Links (some events lack detail pages)

A scraper that captures optional fields is more valuable than one that only captures required fields.

## Why Not Just Use MinOccs=Record Count?

### Misconception
"If the page has 30 records, I should use MinOccs=30 to get a scraper that produces 30 records."

### Reality
Setting MinOccs=30 doesn't guarantee:
1. **30 records** (you might get fewer due to selector issues)
2. **Complete records** (you'll miss optional fields)

### Better Approach
Use a **lower MinOccs** to:
1. Capture more fields (including optional ones)
2. Create more robust scrapers that handle variance
3. Extract richer data per record

## MinOccs Selection Strategy

### Current Default (cmd/goskyr/main.go)
```go
minOccs := []int{15, 20, 25, 30}
```

### Testing Order
The system tests MinOccs in **descending order**: `[30, 25, 20, 15]`

This is optimal because:
1. Start with high threshold (30) → minimal fields
2. Work down to lower thresholds (25, 20, 15) → more fields
3. Each iteration adds configs with progressively more fields
4. Later configs (lower MinOccs) are richer but not necessarily "better"

### Selection Criteria
The "best" config is typically:
- **Most fields** per record (comprehensiveness)
- **Correct record count** (all records extracted)
- **Largest file size** (more data captured)

In this case: `n15ab_30.json` (28 fields, 30 records, 95KB)

## Relationship: MinOccs vs MinListRecords

From `PROPOSAL_MIN_LIST_RECORDS.md`:

| Parameter | Purpose | Phase | Granularity |
|-----------|---------|-------|-------------|
| `MinOccs` | Filter field patterns | Analysis (pre-scrape) | DOM elements |
| `MinListRecords` | Filter output quality | Post-scrape | Final records |

### Example Workflow
1. **MinOccs=15**: "Include fields appearing 15+ times in HTML"
   → Generates scraper with 28 fields
2. **Scrape**: Extract data using generated config
   → Produces 30 records
3. **MinListRecords=20**: "Only keep scrapers producing 20+ records"
   → Keeps this config (30 > 20)

## Key Insights

### 1. MinOccs ≠ Record Count
MinOccs is a field occurrence threshold, not a record count requirement.

### 2. Lower MinOccs = Richer Data
Lower MinOccs values capture optional fields, creating more comprehensive scrapers.

### 3. Record Count is Independent
A scraper with MinOccs=15 can produce:
- 30 records (if selectors are good)
- 15 records (if selectors are too specific)
- 45 records (if selectors are too broad)

### 4. Multiple Configs, Same Record Count
Many configs extract 30 records but with different field counts:
```bash
ls -lhS /tmp/goskyr/.../debhorton-com-events__*_30.json | head -5

-rw-r--r-- 95K  n15ab_30.json       # 28 fields (BEST)
-rw-r--r-- 69K  n15abc_30.json      # 23 fields
-rw-r--r-- 29K  n30abaaaaaaaa_30.json  # 10 fields
-rw-r--r-- 29K  n15abcd_30.json     # 10 fields
-rw-r--r-- 28K  n30ab_30.json       # 3 fields
```

### 5. Suffix Matters Too
The suffix (`ab`, `abc`, etc.) represents the clustering/field selection strategy.
- Different suffixes = different field combinations
- Both MinOccs AND suffix affect field count

## Recommendations

### For Users
1. **Don't assume MinOccs = record count** - they're unrelated
2. **Use lower MinOccs values** (15-20) for richer scrapers
3. **Sort results by file size** to find the most comprehensive config
4. **Inspect field counts** to verify data completeness

### For Development
1. **Add field count to output** - help users compare configs
2. **Implement MinListRecords** - filter configs by actual record count
3. **Document MinOccs clearly** - emphasize it's a field threshold, not record count
4. **Consider auto-selection** - pick config with most fields at target record count

## Conclusion

The `debhorton-com-events__n15ab_30.json` config has MinOccs=15 (not 30) because:

1. **MinOccs filters DOM patterns**, not records
2. **MinOccs=15 allows optional fields** that appear 15-29 times
3. **MinOccs=30 would exclude those fields**, producing a minimal scraper
4. **The n15 scraper is richer** (28 fields vs 3 fields)
5. **Both extract 30 records**, but n15 captures more data per record

**Bottom line**: MinOccs=15 produced the best scraper not despite extracting 30 records, but because it captured the most fields while still extracting all 30 records.

## References

- **Code**: `generate/generate.go:333` (MinOccs reverse sort)
- **Code**: `cmd/goskyr/main.go:121-122` (default MinOccs values)
- **Docs**: `generate/DESIGN.md` (HTML analysis phase)
- **Proposal**: `docs/proposed/PROPOSAL_MIN_LIST_RECORDS.md` (MinOccs vs MinListRecords)
- **Output**: `out.txt:203` (extraction confirmation)
- **Data**: `/tmp/goskyr/main/debhorton-com_configs/debhorton-com-events__n15ab_30.json`
