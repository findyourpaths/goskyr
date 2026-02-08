# Generate Package Design

## Overview

The `generate` package implements automatic scraper configuration generation by analyzing HTML pages. It discovers repeating patterns in web pages (like event listings, product catalogs, article lists) and automatically generates scraper configurations that can extract structured data from those patterns.

## Core Concept

The fundamental insight is that many web pages are generated from database templates, where the same HTML structure is repeated for each record (event, product, article, etc.). By analyzing multiple instances of these records on a page, we can:

1. **Identify repeating patterns** - Find DOM structures that appear multiple times
2. **Discover field locations** - Determine where data fields (titles, dates, prices, etc.) appear within each record
3. **Generate selectors** - Create CSS selectors that can extract these fields from any page using the same template
4. **Support detail pages** - Follow links from list pages and generate scrapers for detail pages

## Architecture

### File Structure

- **generate.go** - Main orchestration: configuration generation pipeline, detail page handling
- **analyze.go** - HTML analysis: pattern detection, field discovery, clustering
- **parse.go** - HTML parsing: tokenization, DOM path construction
- **locationprops.go** - Data structures: field metadata, naming, visual representation

### Key Data Structures

#### `path` (slice of `node`)
Represents a DOM path from root to a specific element. Used to uniquely identify locations in the HTML tree.

```go
type node struct {
    Tag        string
    Attributes map[string]string
    Classes    []string
    IDs        []string
    Index      int // nth-child position
}
type path []node
```

#### `locationProps`
Metadata about a field location discovered in the HTML:

```go
type locationProps struct {
    path      path              // DOM path to this element
    count     int               // How many times this pattern appears
    name      string            // Generated field name (hash-based or semantic)
    attr      string            // Attribute to extract (e.g., "href", "" for text)
    examples  []string          // Sample values found
    isText    bool              // Whether extracting text content vs attribute
    textIndex int               // Index when multiple text nodes exist
    color     tcell.Color       // Visualization color
    distance  float64           // Distance in DOM tree (for clustering)
}
```

#### `ConfigOptions`
Configuration for the generation process:

```go
type ConfigOptions struct {
    URL                        string
    ConfigOutputDir            string
    MinOccs                    []int      // Minimum occurrence thresholds to try
    MinRecords                 int        // Minimum records required in output (0 = no filter)
    OnlyVaryingFields          bool       // Filter out static fields
    DoDetailPages              bool       // Generate detail page scrapers
    OnlyKnownDomainDetailPages bool       // Only scrape known domains
    RenderJS                   bool       // Whether to render JavaScript
    RequireDetailURL           string     // Only generate detail configs including this URL
    RequireString              string     // Required text for validation
    configID                   ConfigID   // Identifies this configuration
}
```

## Generation Pipeline

### Phase 1: HTML Analysis (`analyzePage`)

**Input:** HTML string, minimum occurrence threshold
**Output:** List of field locations (`locationProps`) and pagination locations

**Steps:**

1. **Parse HTML** - Tokenize HTML into DOM paths
   - Walk through HTML tokens (start tag, end tag, text)
   - Build path from root to each element
   - Record text content and attributes

2. **Detect Patterns** - Find repeating structures
   - Group elements by their DOM path
   - Count occurrences of each path
   - Keep paths that appear >= minimum occurrence threshold

3. **Squash Similar Paths** - Merge variations
   - Two paths are similar if they differ only in nth-child indices
   - Example: `div.event:nth-child(1) > h2` and `div.event:nth-child(2) > h2` → `div.event > h2`
   - Increases count and combines examples

4. **Filter Fields** - Remove unwanted locations
   - Remove fields below minimum count
   - Remove static fields (if `OnlyVaryingFields=true`) - fields where all examples are identical
   - Remove navigation elements

5. **Generate Field Names** - Create unique identifiers
   - Hash the DOM path using CRC32
   - Format: `F{hash}-{attr}-{index}` (e.g., `Fd1f7685c-href-0`)
   - Hash collision detection

### Phase 2: Root Selector Discovery (`findSharedRootSelector`)

**Input:** List of field locations
**Output:** Common parent selector that contains all fields for one record

**Purpose:** Find the repeating container element (e.g., `div.event-card`) that wraps each record.

**Algorithm:**

1. **Find Shared Prefix** - Determine common ancestor path
   - Take shortest path length among all fields
   - Walk up from each field, checking for common prefix
   - Result: path that all fields share

2. **Pull Back Root** - Adjust to actual repeating element
   - Count how many times the root selector matches
   - If matches > field count, pull back (go up) one level
   - Prevents selecting too granular elements

3. **Shorten Root** - Remove unnecessary specificity
   - Strip overly specific classes/IDs that might change
   - Keep structure-defining attributes

### Phase 3: Field Processing (`processFields`)

**Input:** Field locations, root selector
**Output:** Structured field definitions for scraper

**For each field location:**

1. **Determine Field Type** - Infer from attribute and content
   - `attr="href"` → url
   - `attr="src"` → image
   - Contains date patterns → date
   - Default → text

2. **Extract Relative Selector** - Path from root to field
   - Remove root selector prefix from field path
   - Keep only the portion specific to this field

3. **Handle Dynamic Content** - Deal with variations
   - nth-child variations
   - Class name variations
   - Fallback selectors

### Phase 4: Strategy Selection (`expandAllPossibleConfigs`)

**Purpose:** Determine whether to use nested or sequential extraction strategy.

#### Nested Strategy
Best for: Hierarchical data where all fields are children of the record container

```html
<div class="event">
  <h2>Event Title</h2>
  <span class="date">2025-01-15</span>
  <a href="/register">Register</a>
</div>
```

Scraper: Select `.event`, then within each, select `h2`, `span.date`, `a`

#### Sequential Strategy
Best for: Split data where fields come from sibling elements

```html
<div class="event-info">
  <h2>Event Title</h2>
</div>
<div class="event-desc">
  <span class="date">2025-01-15</span>
  <a href="/register">Register</a>
</div>
```

Scraper: Select parent, iterate children sequentially, extract from each

**Selection Heuristics:**

```go
func shouldUseSequentialStrategy(gqdoc, rootSel, fields) bool {
    // Count how many times root selector matches
    rootCount := gqdoc.Find(rootSel).Length()

    // Check if fields span multiple sibling containers
    childPaths := getUniqueChildPaths(fields, rootSel)

    // Use sequential if:
    // 1. Exactly 2 child paths (likely info/description split)
    // 2. Root selector matches more times than expected (too granular)
    return len(childPaths) == 2 || rootCount > expectedRecords
}
```

**Configuration Creation:**

1. **Nested Config** - Uses root selector, fields extracted via child selectors
2. **Sequential Config** - Uses parent selector, fields found in order
3. **Both Configs** - Generate and test both, keep the one that produces better results

### Phase 5: Validation and Pruning

**Input:** Generated configurations
**Output:** Filtered set of working configurations

**Steps:**

1. **Scrape Test** - Run each configuration on the page
   - Extract records using the generated selectors
   - Check that records are produced

2. **Pruning Logic** (if `DoPruning=true`):
   - **< 2 records**: Discard (not enough data)
   - **Duplicate records**: Discard (same as another config)
   - **All identical**: Discard (no variation, likely extracted wrong elements)

3. **Record Quality Checks**:
   - Required fields present
   - Field values make sense (dates parseable, URLs valid)
   - Minimum field count met

## Detail Page Handling

### Overview

Many sites have **list pages** (showing summaries) linking to **detail pages** (showing full information). The generate package can automatically:

1. Detect URL fields in list page configs
2. Fetch the linked detail pages
3. Generate scrapers for detail page content
4. Merge detail page data back into list page records

### Pipeline

#### `ConfigurationsForAllDetailPages`

**For each URL field in list page configs:**

1. **Collect URLs** - Gather all values from URL fields
   - Example field: `Fd1f7685c-href-0` (event registration links)
   - Example URLs: `theembodylab.com/schedule/event-1`, `.../event-2`, etc.

2. **Resolve Redirects** - Follow tracking links to final destinations
   - `email.kjbm.theembodylab.com/c/xyz123` → `theembodylab.com/schedule/event-1`
   - Uses `cache.GetResolvedURL()` to get final URL

3. **Filter Duplicates** - Same URLs from different fields
   - Avoid regenerating scrapers for same detail page type

4. **Generate Detail Scrapers** - Call `ConfigurationsForDetailPages`

#### `ConfigurationsForDetailPages`

**For each URL field:**

1. **Fetch Pages** - Retrieve all detail pages
   - May be multiple pages (e.g., 10 event detail pages)
   - Pages are expected to use the same template

2. **Concatenate HTML** - Join pages into single document
   - Goskyr works by finding patterns across multiple examples
   - Concatenating makes them analyzable as "records"

3. **Generate Detail Config** - Run standard analysis pipeline
   - Same as list page generation
   - Produces selectors for detail page fields

4. **Merge Configurations** - Combine list + detail
   ```yaml
   scrapers:
     - name: list-page
       selector: div.event-summary
       fields:
         - name: title
         - name: date
         - name: url # Link to detail page
     - name: detail-page
       selector: body  # Single page, no repeating container
       fields:
         - name: description
         - name: instructor
         - name: price
   ```

5. **Scrape and Join** - Execute combined scraper
   - Extract list page records
   - For each record, follow URL to detail page
   - Scrape detail page
   - Add detail fields to list record

### Detail Page Strategy

Detail pages have a different pattern than list pages:

- **List page**: Multiple records on one page, need to find repeating container
- **Detail page**: Single record per page, no repeating container needed
- **Selector**: Often just `body` or a simple container, fields are direct children

### Detail Page Filtering (`RequireDetailURL`)

List pages often contain links to multiple page types (event details, profiles, external ticketing). The `--require-detail-url` flag restricts detail page config generation to URL fields that contain the specified URL.

**Pipeline placement:** Applied after URL collection but before fetching/analyzing detail pages.

1. All detail page URLs are extracted from the list page (normal behavior)
2. For each URL field, check if `RequireDetailURL` is present in the URL list
3. Fields whose URLs don't contain the required URL are skipped
4. Warning logged when no fields match the filter

**Match strategy:** Substring matching (consistent with `--require-string`).

**Interaction with other filters:** AND logic — when both `--require-detail-url` and `--require-string` are specified, both must be satisfied.

```bash
# Only generate detail configs for fields linking to this event page
goskyr generate \
  --require-detail-url "https://example.com/event/annual-conference" \
  https://example.com/events
```

## Minimum Occurrence Threshold

The `MinOccs` parameter (e.g., `[3, 4, 5, 7, 10]`) controls pattern sensitivity:

### High Threshold (10)
- **Finds:** Only very common patterns (fields appearing 10+ times)
- **Use case:** Large listings with many records
- **Risk:** Might miss valid patterns with fewer examples

### Low Threshold (3)
- **Finds:** Patterns appearing 3+ times
- **Use case:** Pages with few records
- **Risk:** More false positives (random elements that happen to repeat)

### Multi-Threshold Strategy

The pipeline tries multiple thresholds in descending order:

```go
for minOcc in [10, 7, 5, 4, 3]:
    configs = ConfigurationsForGQDocumentWithMinOccurrence(doc, minOcc)
    results.merge(configs)  // Add new configs, avoid duplicates
```

This finds both very common fields and less common but still valid fields.

### MinOccs vs Record Count

**MinOccs is a field occurrence threshold, not a record count.** A MinOccs value of 15 can produce a scraper that extracts 30 records. In practice, lower MinOccs values often produce *better* scrapers because they capture optional fields that don't appear in every record.

| MinOccs | Effect | Typical Result |
|---------|--------|----------------|
| 30 | Only fields appearing 30+ times | Minimal scraper (core fields only) |
| 15 | Fields appearing 15+ times | Richer scraper (core + optional fields) |

Web pages commonly have required fields (present in every record) and optional fields (present in most records — dates, images, links). A lower MinOccs threshold captures both, producing more comprehensive data per record.

**Example:** On a page with 30 events, MinOccs=30 produced 3 fields per record while MinOccs=15 produced 28 fields per record — both extracted all 30 records.

## Minimum Records Filter (`MinRecords`)

The `--min-records` flag (default: 0) is a post-scrape quality filter that eliminates configurations producing too few records. Unlike MinOccs (which filters during HTML analysis), MinRecords checks the actual scraper output.

| Parameter | Purpose | Phase | Granularity |
|-----------|---------|-------|-------------|
| `MinOccs` | Filter field patterns | Analysis (pre-scrape) | DOM elements |
| `MinRecords` | Filter output quality | Post-scrape | Final records |

**Applied in three locations:**
1. Nested strategy configs — after scraping, before adding to results
2. Sequential strategy configs — before adding to results
3. Detail page configs — replaces the hardcoded `< 2` threshold (defaults to 2 when MinRecords is 0)

**Observability:** Filtered configs receive status `"failed_min_records"` for tracing.

```bash
# Only keep scrapers producing 20+ records
goskyr generate --min-records=20 https://example.com/events
```

## Field Name Hashing

Field names are generated by hashing the DOM path:

```go
func setFieldNames(locations) {
    for location in locations:
        pathStr = location.path.string()  // e.g., "body > div.events > div > h2"
        hash = crc32(pathStr)              // e.g., 0xd1f7685c
        location.name = fmt.Sprintf("F%x-%s-%d", hash, location.attr, location.textIndex)
        // Result: "Fd1f7685c-href-0"
}
```

**Benefits:**
- **Stable**: Same DOM structure always produces same field name
- **Unique**: Different paths produce different names
- **Compact**: Shorter than full CSS selector
- **Collision detection**: Panic if two different paths hash to same value

## Configuration Output

Generated configurations are written as YAML files:

```yaml
scrapers:
  - name: gmail-1844972871065198150__n03a_Fd1f7685c-href-0_s01a
    url: gmail://1844972871065198150
    strategy: sequential
    selector: body > div[data-section-id]
    fields:
      - name: title
        type: text
        element_locations:
          - selector: h1.eventitem-title
      - name: date
        type: date
        element_locations:
          - selector: time.event-date
      - name: url
        type: url
        element_locations:
          - selector: a.register-link
            attribute: href
```

## Key Algorithms

### Squashing Algorithm

Merges similar paths that differ only in nth-child indices:

```go
func squashLocationManager(locations, minOcc) {
    grouped = groupByPathStructure(locations)  // Group paths ignoring nth-child

    for group in grouped:
        if len(group) >= minOcc:
            merged = mergeLocations(group)
            merged.count = len(group)
            merged.examples = flatten(group.examples)
            result.append(merged)

    return result
}
```

### Path Distance

Used for clustering related fields:

```go
func (p path) distance(p2 path) float64 {
    // Count how many nodes differ between paths
    commonLen = min(len(p), len(p2))
    diffCount = 0

    for i in 0..commonLen:
        if !p[i].equals(p2[i]):
            diffCount++

    diffCount += abs(len(p) - len(p2))  // Length difference
    return float64(diffCount)
}
```

Fields with small distances are likely from the same record structure.

## Integration with Scrape Package

The generate package produces `scrape.Config` objects that the scrape package executes:

1. **Generate** creates config → writes YAML file
2. **Scrape** reads YAML file → executes selectors → produces data

The generated selectors are designed to work with goquery (jQuery-like CSS selectors).

## Error Handling

- **No patterns found**: Returns empty config map, no error
- **Parse errors**: Returns error immediately
- **Scraping failures**: Logs warning, continues with other configs
- **Hash collisions**: Panics (should never happen with good hash function)

## Performance Considerations

- **HTML parsing**: O(n) where n = HTML size
- **Pattern detection**: O(p × m) where p = number of paths, m = MinOcc iterations
- **Root finding**: O(p × d) where d = max DOM depth
- **Detail pages**: O(u × a) where u = number of URLs, a = analysis time per page

For large pages (100+ KB HTML with 1000+ fields), analysis can take 10-60 seconds.

## Testing

The package is primarily tested through integration tests:

1. Provide HTML fixture files
2. Run configuration generation
3. Verify generated selectors produce expected records
4. Check field types and values are correct

See `scrape/scrape_test.go` for example test cases.
