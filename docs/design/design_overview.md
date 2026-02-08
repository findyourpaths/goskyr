# Goskyr Fork - Comprehensive Design Documentation

## Table of Contents

1. [Overview](#overview)
2. [Fork Differences from Upstream](#fork-differences-from-upstream)
3. [Architecture](#architecture)
4. [Generate Package](#generate-package)
5. [Scrape Package](#scrape-package)
6. [Fetch Package](#fetch-package)
7. [Library Usage](#library-usage)
8. [Configuration Format](#configuration-format)
9. [Extraction Strategies](#extraction-strategies)
10. [Detail Page Handling](#detail-page-handling)
11. [Testing](#testing)

---

## Overview

This is a fork of [jakopako/goskyr](https://github.com/jakopako/goskyr), a Go-based web scraper that automatically discovers and extracts structured data from web pages. The tool analyzes HTML to find repeating patterns and generates configurations that can extract this data.

**Primary Use Case**: Extracting event information from various websites, where:
- List pages show multiple events (title, date, summary)
- Detail pages contain full information (description, price, venue details, etc.)

**Core Insight**: Web pages are often generated from database templates, creating repeating HTML structures. By analyzing these patterns across multiple instances, goskyr can automatically generate selectors to extract structured data.

---

## Fork Differences from Upstream

### Original goskyr

The upstream goskyr focuses on **list pages** - pages that display multiple records with repeating HTML structure:

```html
<!-- List page: multiple events -->
<div class="event">
  <h2>Event 1</h2>
  <span>Date 1</span>
</div>
<div class="event">
  <h2>Event 2</h2>
  <span>Date 2</span>
</div>
```

### This Fork's Innovation

This fork extends goskyr to also handle **detail pages** - individual pages with single records that contain additional information:

```html
<!-- Detail page: single event with more fields -->
<body>
  <h1>Event 1</h1>
  <p class="description">Full description...</p>
  <span class="price">$50</span>
  <div class="venue">Venue details...</div>
</body>
```

**Key Enhancement**: The fork discovers repetition **across multiple detail pages** by:

1. **Collecting URLs** from list page records (e.g., event registration links)
2. **Fetching multiple detail pages** (e.g., 10 different event detail pages)
3. **Concatenating them** into a single HTML document wrapped in `<htmls>` tags
4. **Analyzing the concatenated document** to find repeating patterns across detail pages
5. **Generating detail page scrapers** that extract fields common to all detail pages
6. **Merging detail data** back into list page records

This means goskyr can now extract both:
- **List page data**: Event titles, dates, links (appearing multiple times on one page)
- **Detail page data**: Full descriptions, prices, venue info (appearing once per page, but repeated across multiple detail pages)

---

## Architecture

### Package Structure

```
goskyr/
├── generate/          # Automatic scraper configuration generation
│   ├── generate.go    # Orchestration, detail page handling
│   ├── analyze.go     # Pattern detection, field discovery
│   ├── parse.go       # HTML tokenization, DOM path construction
│   └── locationprops.go # Field metadata, naming, visualization
├── scrape/            # Configuration execution
│   └── scrape.go      # Field extraction, strategies, detail pages
├── fetch/             # HTTP fetching and caching
├── output/            # Output formatting (JSON, CSV)
├── cmd/goskyr/        # CLI interface
└── testdata/          # Test fixtures
```

### Data Flow

```
HTML Page → Parse → Analyze Patterns → Generate Config → Scrape → Records
                                           ↓
                              Detail Pages (fetch, analyze, merge)
```

### Core Abstractions

#### `path` (DOM Path)

A sequence of nodes from root to a specific element:

```go
type node struct {
    Tag        string              // e.g., "div"
    Attributes map[string]string   // e.g., {"class": "event"}
    Classes    []string            // Parsed from class attribute
    IDs        []string            // Parsed from id attribute
    Index      int                 // nth-child position
}
type path []node
```

Example: `body > div.events > div.event:nth-child(2) > h2.title`

#### `locationProps` (Field Metadata)

Discovered field location with metadata:

```go
type locationProps struct {
    path      path          // DOM path to this element
    count     int           // Number of occurrences
    name      string        // Generated field name (e.g., "Fd1f7685c-href-0")
    attr      string        // Attribute to extract ("href", "", etc.)
    examples  []string      // Sample values
    isText    bool          // Text content vs attribute
    textIndex int           // When multiple text nodes exist
    color     tcell.Color   // For terminal visualization
    distance  float64       // Distance in DOM tree (clustering)
}
```

#### `Config` and `Scraper`

```go
type Config struct {
    ID       ConfigID         // Hierarchical identifier
    Scrapers []Scraper        // List and detail page scrapers
    Records  output.Records   // Extracted data
}

type Scraper struct {
    Name       string         // Descriptive name
    URL        string         // Target URL pattern
    Strategy   string         // "nested" or "sequential"
    Selector   string         // CSS selector for record container
    Fields     []Field        // Fields to extract
    Validation *ValidationConfig
}
```

#### `ConfigID` (Hierarchical Naming)

Identifies configurations with a hierarchy:

```go
type ConfigID struct {
    Slug  string  // Domain/source (e.g., "gmail-1844972871065198150")
    ID    string  // Config variant (e.g., "n03a", "s05a")
    Field string  // For detail pages (e.g., "Fd1f7685c-href-0")
    SubID string  // Sub-config (e.g., "n01a")
}
```

**Format**: `{Slug}__{ID}_{Field}_{SubID}`

**Examples**:
- `basic-detail-pages-flat-w-links-com__n10a` - List page, nested strategy, min occurrence 10
- `basic-detail-pages-flat-w-links-com__n10a_Fd1f7685c-href-0_s05a` - Detail page for field `Fd1f7685c-href-0`, sequential strategy, min occurrence 5

**Prefixes**:
- `n` = nested strategy
- `s` = sequential strategy

**Numbers**:
- `03a`, `05a`, `10a` = minimum occurrence threshold (3, 5, 10, etc.)

---

## Generate Package

### Purpose

Automatically analyzes HTML to discover repeating patterns and generate scraper configurations.

### Main Entry Points

#### `ConfigurationsForPage()`

```go
func ConfigurationsForPage(ctx context.Context, cache fetch.Cache, opts ConfigOptions) (map[string]*scrape.Config, error)
```

**Purpose**: Generate configurations for a web page by fetching and analyzing it.

**Process**:
1. Fetch page from cache
2. Call `ConfigurationsForGQDocument()`
3. Return generated configs keyed by record signature

#### `ConfigurationsForGQDocument()`

```go
func ConfigurationsForGQDocument(ctx context.Context, cache fetch.Cache, opts ConfigOptions, gqdoc *fetch.Document) (map[string]*scrape.Config, error)
```

**Purpose**: Generate configs using multiple minimum occurrence thresholds.

**Process**:
1. Try thresholds in descending order (e.g., [10, 7, 5, 4, 3])
2. For each threshold, call `ConfigurationsForGQDocumentWithMinOccurrence()`
3. Merge results, avoiding duplicates

**Rationale**: Higher thresholds find very common patterns (many records), lower thresholds find less common but valid patterns (few records).

#### `ConfigurationsForGQDocumentWithMinOccurrence()`

```go
func ConfigurationsForGQDocumentWithMinOccurrence(ctx context.Context, cache fetch.Cache, opts ConfigOptions, gqdoc *fetch.Document, minOcc int, rs map[string]*scrape.Config) (map[string]*scrape.Config, error)
```

**Purpose**: Core generation logic for a specific minimum occurrence threshold.

**Process**:
1. **Analyze**: `analyzePage()` - Find repeating patterns
2. **Find Root**: `findSharedRootSelector()` - Determine container element
3. **Expand**: `expandAllPossibleConfigs()` - Generate and test configurations

### Analysis Pipeline

#### Phase 1: HTML Analysis (`analyzePage`)

**Input**: HTML string, minimum occurrence threshold
**Output**: List of field locations (`locationProps`) and pagination locations

**Steps**:

1. **Parse HTML**
   - Tokenize HTML using `html.Tokenizer`
   - Track current path through DOM tree
   - Record text content and attributes at each node
   - Build `locationProps` for each potential field

2. **Detect Patterns**
   - Group elements by their DOM path
   - Count occurrences of each path
   - Keep paths appearing ≥ minimum occurrence threshold

3. **Squash Similar Paths**
   - Merge paths differing only in nth-child indices
   - Example: `div.event:nth-child(1) > h2` + `div.event:nth-child(2) > h2` → `div.event > h2`
   - Combine examples from all instances
   - Increase count

4. **Filter Fields**
   - Remove fields below minimum count
   - Remove static fields (if `OnlyVaryingFields=true`) - all examples identical
   - Remove navigation elements (next/previous links)

5. **Generate Field Names**
   - Hash DOM path using CRC32
   - Format: `F{hash}-{attr}-{textIndex}`
   - Example: `Fd1f7685c-href-0`
   - Detect hash collisions (should be rare)

**Example**:

```html
<div class="event">
  <h2>Event A</h2>
  <span>2025-01-15</span>
</div>
<div class="event">
  <h2>Event B</h2>
  <span>2025-01-20</span>
</div>
```

**Discovered patterns** (minOcc=2):
- Path: `body > div.events > div.event > h2`, Count: 2, Examples: ["Event A", "Event B"]
- Path: `body > div.events > div.event > span`, Count: 2, Examples: ["2025-01-15", "2025-01-20"]

#### Phase 2: Root Selector Discovery (`findSharedRootSelector`)

**Purpose**: Find the repeating container element that wraps each record.

**Algorithm**:

1. **Find Shared Prefix**
   - Walk down from root, comparing paths
   - Stop at first divergence
   - Result: common ancestor path

2. **Pull Back Root** (`pullBackRootSelector`)
   - Count how many times root selector matches
   - If matches > expected count, move up one level
   - Prevents selecting too granular elements
   - Special handling for email HTML with section divs

3. **Shorten Root**
   - Strip overly specific classes/IDs
   - Keep structure-defining attributes

**Example**:

```
Field paths:
- body > div.events > div.event:nth-child(1) > h2
- body > div.events > div.event:nth-child(2) > h2
- body > div.events > div.event:nth-child(1) > span
- body > div.events > div.event:nth-child(2) > span

Shared prefix: body > div.events > div.event
Root selector: div.event  (or body > div.events > div.event)
```

#### Phase 3: Field Processing (`processFields`)

**Purpose**: Convert field locations to scraper field definitions.

**For each field**:

1. **Determine Field Type**
   - `attr="href"` → `url`
   - `attr="src"` → `image`
   - Contains date patterns → `date`
   - Default → `text`

2. **Extract Relative Selector**
   - Remove root selector prefix
   - Keep only field-specific path

3. **Handle Dynamic Content**
   - nth-child variations
   - Class name variations
   - Fallback selectors

**Example**:

```
Root: div.event
Field path: body > div.events > div.event > h2.title
Relative selector: h2.title
Type: text
```

#### Phase 4: Strategy Selection (`expandAllPossibleConfigs`)

**Purpose**: Determine whether to use nested or sequential extraction.

##### Nested Strategy

**Best for**: Hierarchical data where all fields are children of the record container.

```html
<div class="event">
  <h2>Event Title</h2>
  <span class="date">2025-01-15</span>
  <a href="/register">Register</a>
</div>
```

**Config**:
```yaml
strategy: nested
selector: div.event
fields:
  - name: title
    element_locations:
      - selector: h2
```

**Scraping**: Select `.event`, then within each, select `h2`, `span.date`, `a`.

##### Sequential Strategy

**Best for**: Split data where fields come from sibling elements.

```html
<div class="container">
  <div class="event-info">
    <h2>Event Title</h2>
  </div>
  <div class="event-desc">
    <span class="date">2025-01-15</span>
  </div>
</div>
```

**Config**:
```yaml
strategy: sequential
selector: div.container
fields:
  - name: title
    element_locations:
      - selector: div.event-info > h2
```

**Scraping**: Select parent, extract Nth occurrence of each field, group into records.

##### Selection Heuristics (`shouldUseSequentialStrategy`)

```go
func shouldUseSequentialStrategy(gqdoc *fetch.Document, rootSel string, fields []Field) bool
```

**Criteria**:
- Has date fields (key indicator of sequential records)
- Selector ends with container element (`div`, `span`, `tr`, `td`, `table`)
- If both true, generate sequential config

**Both Strategies Generated**:
- Generate nested config (always)
- Generate sequential config (if criteria met)
- Both are tested and deduplicated
- Keeps whichever produces better/different results

#### Phase 5: Validation and Pruning

**Steps**:

1. **Scrape Test**
   - Run each config on the page
   - Verify records are produced

2. **Pruning** (if `DoPruning=true`)
   - `< 2 records`: Discard
   - Duplicate records: Discard (same as another config)
   - All identical: Discard (no variation)

3. **Quality Checks**
   - Required fields present
   - Field values parseable (dates, URLs valid)
   - Minimum field count met

### Clustering

**Purpose**: Group related fields to create variant configurations.

```go
func findClusters(ctx context.Context, lps []*locationProps, rootSelector path) map[string][]*locationProps
```

**Algorithm**:
1. Calculate distance between field paths
2. Group fields with small distances
3. Create sub-configurations for each cluster
4. Recursive exploration of field combinations

**Use Case**: When multiple related fields exist at different depths, generate configs trying different field combinations.

---

## Scrape Package

### Purpose

Execute scraper configurations to extract structured data from HTML.

### Main Entry Points

#### `Page()`

```go
func Page(ctx context.Context, cache fetch.Cache, c *Config, s *Scraper, globalConfig *GlobalConfig, rawDyn bool, path string) (output.Records, error)
```

**Purpose**: Scrape from URL.

**Process**:
1. Fetch URL using cache
2. Optionally render JavaScript
3. Convert to GQDocument
4. Call `GQDocument()`

#### `GQDocument()`

```go
func GQDocument(ctx context.Context, c *Config, s *Scraper, gqdoc *fetch.Document) (output.Records, error)
```

**Purpose**: Scrape from parsed HTML document.

**Process**:
1. Initialize filters
2. Determine strategy (nested vs sequential)
3. Find all record containers using `s.Selector`
4. For each container:
   - Call `GQSelection()` (nested) or `scrapeSequential()` (sequential)
   - Extract fields
   - Validate record
   - Apply filters
5. Handle pagination
6. Handle detail pages
7. Return records

### Extraction Strategies

#### Nested Extraction (`GQSelection`)

**Process**:
1. Find all elements matching root selector → N selections
2. For each selection:
   - Find field elements within this container
   - Extract text/attributes
   - Build record
3. Return N records

**Code**:
```go
func GQSelection(ctx context.Context, c *Config, s *Scraper, sel *fetch.Selection, baseURL string, baseYear int) (output.Records, error)
```

#### Sequential Extraction (`scrapeSequential`)

**Challenge**: Match fields across different containers when data is split.

**Process**:
1. Find parent element
2. Get all descendants matching field selectors
3. **Use date elements as anchors**:
   - Date fields appear once per record
   - Clone HTML tree, remove non-date elements
   - Count date containers = number of records
4. Build records by extracting Nth occurrence of each field
5. Return N records

**Code**:
```go
func scrapeSequential(ctx context.Context, c *Config, s *Scraper, gqdoc *fetch.Document, baseURL string, baseYear int) (output.Records, error)
```

**Example**:

```html
<div class="container">
  <h2>Title 1</h2>
  <span>2025-01-15</span>  <!-- Date anchor 1 -->
  <a href="/1">Link 1</a>
  <h2>Title 2</h2>
  <span>2025-01-20</span>  <!-- Date anchor 2 -->
  <a href="/2">Link 2</a>
</div>
```

**Algorithm**:
- Count date spans: 2
- Extract 1st h2, 1st span, 1st a → Record 1
- Extract 2nd h2, 2nd span, 2nd a → Record 2

### Field Extraction

#### Text Extraction

```go
func getTextString(e *ElementLocation, sel *fetch.Selection) (string, error)
```

**Process**:
1. Find elements matching selector
2. Extract text content
3. Trim whitespace
4. Join multiple elements with `\n`

#### URL Extraction

**Features**:
- Resolve relative URLs using base URL
- Handle base tags in HTML
- Return absolute URLs

#### Date Extraction

**Features**:
- Uses `phil/datetime` package for flexible parsing
- Handles various formats (ISO, US, European)
- Year guessing for dates without years
- Multiple format attempts

**Process**:
```go
// Try parsing as date
dts := datetime.Parse(value)
if len(dts) > 0 {
    rec[f.Name] = dts[0].Format("2006-01-02")
    return nil
}

// Year guessing if parse fails
if baseYear > 0 {
    dts = datetime.ParseWithYear(value, baseYear)
    // ...
}
```

### Advanced Features

#### Fallback Locations

```yaml
- name: date
  element_locations:
    - selector: time[datetime]
      attribute: datetime
      fallbacks:
        - selector: time
        - selector: span.date
```

#### Regex Extraction

```yaml
- name: price
  regex:
    exp: \$(\d+\.\d{2})
    index: 1
  element_locations:
    - selector: span.price
```

**Example**: `"Price: $49.99"` → `"49.99"`

#### Value Transformation

```yaml
- name: title
  transform:
    - type: regex-replace
      regex: "CANCELLED.*"
      replace: ""
```

#### Filters

```yaml
filters:
  - field: status
    exp: "cancelled"
    match: false  # Exclude if matches
  - field: date
    exp: "> now"
    match: true   # Include if matches
```

#### Validation

```yaml
validation:
  requires_cta_selector: a.register-link
```

Records without required elements are discarded.

---

## Detail Page Handling

### Overview

The **key innovation of this fork**: automatically scraping detail pages.

**Problem**: Many sites have list pages (summaries) linking to detail pages (full info).

**Solution**:
1. Detect URL fields in list page configs
2. Fetch linked detail pages
3. Generate scrapers for detail page content (by finding repetition across multiple detail pages)
4. Merge detail data into list records

**⚠️ Troubleshooting**: Detail page generation can fail silently at several stages. See **`docs/troubleshooting/GOSKYR_DETAIL_PAGE_GENERATION.md`** for comprehensive debugging guide including:
- Why `ConfigurationsForAllDetailPages()` returns 0 configs
- How to identify list vs detail page configs correctly
- Expected file patterns and debugging checklist
- Real-world debugging examples

### Pipeline

#### 1. `ConfigurationsForAllDetailPages`

```go
func ConfigurationsForAllDetailPages(ctx context.Context, cache fetch.Cache, opts ConfigOptions, pageConfigs map[string]*scrape.Config) (map[string]*scrape.Config, error)
```

**Purpose**: Generate configs for all detail pages linked from list configs.

**Process**:

**For each URL field in list page configs**:

1. **Collect URLs**
   - Gather all values from URL fields
   - Example: Event registration links from list page

2. **Resolve Redirects**
   - Follow tracking links to final destinations
   - `email.kjbm.theembodylab.com/c/xyz` → `theembodylab.com/schedule/event-1`
   - Uses `cache.GetResolvedURL()`

3. **Filter**
   - Deduplicate URLs
   - Skip blocked domains (e.g., Wikipedia)
   - Optionally filter by domain (only known domains)
   - Skip email sources domain checking

4. **Generate Detail Scrapers**
   - Call `ConfigurationsForDetailPages()`

#### 2. `ConfigurationsForDetailPages`

```go
func ConfigurationsForDetailPages(ctx context.Context, cache fetch.Cache, opts ConfigOptions, pjs []*pageJoin, rs map[string]*scrape.Config) (map[string]*scrape.Config, error)
```

**Purpose**: Generate scrapers for a set of detail page URLs.

**Process**:

1. **Fetch Pages**
   - Retrieve all detail pages (e.g., 10 event detail pages)
   - Pages expected to use the same template

2. **Concatenate HTML** (`joinPageJoinsGQDocuments`)
   - Join pages into single document: `<htmls>\n<html>...</html>\n<html>...</html>\n</htmls>`
   - **Rationale**: Goskyr finds patterns by counting repetitions. Single detail pages have no repetition, but 10 detail pages concatenated create repetition!

**Example**:

```html
<htmls>
  <html><body><h1>Event 1</h1><p>Description 1</p><span>$50</span></body></html>
  <html><body><h1>Event 2</h1><p>Description 2</p><span>$60</span></body></html>
  <html><body><h1>Event 3</h1><p>Description 3</p><span>$70</span></body></html>
  ...
</htmls>
```

**Now** goskyr can find:
- `h1` appears 10 times → title field
- `p` appears 10 times → description field
- `span` appears 10 times → price field

3. **Generate Detail Config**
   - Run standard analysis pipeline (`ConfigurationsForGQDocument`)
   - Same as list page generation
   - Produces selectors for detail page fields

4. **Merge Configurations**
   ```yaml
   scrapers:
     - name: list-page
       selector: div.event-summary
       fields:
         - name: title
         - name: date
         - name: url  # Link to detail page
     - name: detail-page
       selector: body > htmls  # Container for concatenated pages
       fields:
         - name: description
         - name: price
   ```

5. **Test Scraping** (`scrape.DetailPages`)
   - For each list record, follow URL to detail page
   - Scrape detail page with detail-page scraper
   - Merge detail fields into list record

**Result**:

```json
[
  {
    "title": "Event 1",
    "date": "2025-01-15",
    "url": "https://example.com/event/1",
    "description": "Full description from detail page",
    "price": "$50"
  }
]
```

### Detail Page Strategy

**Difference from list pages**:

- **List page**: Multiple records on one page → find repeating container (`div.event`)
- **Detail page**: Single record per page → no repeating container needed
- **Concatenated detail pages**: Multiple pages → body > htmls > html becomes the "container"

**Selector**:
- After analysis: `body > htmls > html` or similar
- Trimmed to: `body` or simple container
- Fields are relative to this

**Why concatenation works**:
1. Individual detail page: `<h1>` appears once → Not detected as pattern
2. Ten detail pages concatenated: `<h1>` appears 10 times → Detected as pattern!
3. Goskyr generates selectors that work on single detail pages
4. Scraper strips `htmls` prefix when creating final config

#### 3. `DetailPages` (in scrape package)

```go
func DetailPages(ctx context.Context, cache fetch.Cache, c *Config, s *Scraper, recs output.Records, domain string) error
```

**Purpose**: Execute detail page scraping and merge results.

**Process**:

```go
// For each record from list page
for _, rec := range recs {
    // Get URL field value
    detailURL := rec[urlFieldName]

    // Fetch detail page
    detailDoc := cache.Get(detailURL)

    // Scrape detail page with detail-page scraper
    detailRecs := GQDocument(c, detailScraper, detailDoc)

    // Merge detail fields into list record
    for field, value := range detailRecs[0] {
        rec[field] = value
    }
}
```

---

## Fetch Package

### Purpose

Abstract HTTP fetching and caching for testability and offline operation.

### Cache Interface

```go
type Cache interface {
    Get(url string) (*Document, bool, error)
    Set(url string, html string) error
    GetResolvedURL(url string) (string, error)
}
```

### Implementations

#### `FileCache`

Caches pages as HTML files on disk.

```
/tmp/goskyr/main/
  basic-detail-pages-flat-w-links-com.html
  basic-detail-pages-flat-w-links-com_configs/
    basic-detail-pages-flat-w-links-com__n10a.yml
```

#### `MemoryCache`

In-memory cache with LRU eviction, wraps another cache for fallback.

#### `URLFileCache`

Specialized cache that:
- Reads from one directory (input cache)
- Writes to another directory (output cache)
- URL slug → filename mapping

### URL Resolution

```go
func (c *Cache) GetResolvedURL(url string) (string, error)
```

Follows redirects to get final URL, used for tracking links in emails.

---

## Library Usage

### Intended Use

This fork is designed to be **used as a library** in a larger system, not just a CLI tool.

### Integration Pattern

```go
package main

import (
    "context"
    "github.com/findyourpaths/goskyr/generate"
    "github.com/findyourpaths/goskyr/scrape"
    "github.com/findyourpaths/goskyr/fetch"
)

func main() {
    ctx := context.Background()

    // Create cache (can be custom implementation)
    cache := fetch.NewURLFileCache(nil, "/path/to/cache", false)

    // Generate scraper configs
    opts := generate.ConfigOptions{
        URL:               "https://example.com/events",
        Batch:             true,
        DoDetailPages:     true,
        MinOccs:           []int{5, 10},
        OnlyVaryingFields: true,
    }
    configs, err := generate.ConfigurationsForPage(ctx, cache, opts)

    // Use configs to scrape data
    for _, config := range configs {
        records, err := scrape.GQDocument(ctx, config, &config.Scrapers[0], doc)
        // Process records...
    }
}
```

### Custom Cache Implementation

```go
type MyCache struct {
    db Database
}

func (c *MyCache) Get(url string) (*fetch.Document, bool, error) {
    html, found := c.db.Query("SELECT html FROM cache WHERE url = ?", url)
    if !found {
        return nil, false, nil
    }
    doc, err := fetch.NewDocumentFromString(html)
    return doc, true, err
}

func (c *MyCache) Set(url string, html string) error {
    return c.db.Exec("INSERT INTO cache (url, html) VALUES (?, ?)", url, html)
}

func (c *MyCache) GetResolvedURL(url string) (string, error) {
    // Implement redirect resolution
}
```

### Key Extension Points

1. **Cache**: Custom storage backends (database, S3, etc.)
2. **Filters**: Custom record filtering logic
3. **Validation**: Custom validation rules
4. **Output**: Custom output formats (not just JSON/CSV)

---

## Configuration Format

### YAML Structure

```yaml
scrapers:
  - name: "list-page-scraper"
    url: "https://example.com/events"
    strategy: nested  # or sequential
    selector: div.event
    render_js: true

    fields:
      - name: title
        type: text
        element_locations:
          - selector: h2.event-title

      - name: event_date
        type: date
        element_locations:
          - selector: time[datetime]
            attribute: datetime
          - selector: time  # Fallback

      - name: detail_url
        type: url
        element_locations:
          - selector: a.event-link
            attribute: href

    filters:
      - field: event_date
        exp: "> now"
        match: true

    validation:
      requires_cta_selector: a.event-link

  - name: "detail-page-scraper"
    strategy: nested
    selector: body
    fields:
      - name: description
        type: text
        element_locations:
          - selector: div.event-description

      - name: price
        type: text
        element_locations:
          - selector: span.price
        regex:
          exp: \$(\d+\.\d{2})
          index: 1
```

### Field Types

#### `text`

Extract text content:

```yaml
- name: title
  type: text
  element_locations:
    - selector: h2.title
```

#### `url`

Extract and normalize URLs:

```yaml
- name: registration_url
  type: url
  element_locations:
    - selector: a.register-link
      attribute: href  # Implied for url type
```

#### `date`

Extract and parse dates:

```yaml
- name: event_date
  type: date
  element_locations:
    - selector: time
      attribute: datetime
  date_location: "America/New_York"
```

#### `date_time_tz_ranges` (Custom)

This fork adds support for datetime ranges with timezones (used internally).

### Element Location Options

```yaml
element_locations:
  - selector: div.content            # CSS selector
    attribute: data-value            # Extract from attribute
    regex_extract:                   # Extract substring
      exp: "Event: (.*)"
      index: 1
    child_index: 2                   # Select nth child
    entire_subtree: true             # Get all text in subtree
    all_nodes: true                  # Join all matching nodes
    separator: ", "                  # Join separator
    max_length: 100                  # Truncate
```

---

## Extraction Strategies

### When to Use Each

#### Nested Strategy

**Use when**:
- All fields are descendants of a repeating container
- Data is hierarchically structured
- Each record is self-contained

**HTML pattern**:
```html
<div class="record">
  <field1>Value</field1>
  <field2>Value</field2>
</div>
```

#### Sequential Strategy

**Use when**:
- Data spans sibling elements
- Fields are split across containers
- Date elements serve as anchors

**HTML pattern**:
```html
<container>
  <div><field1>Value 1</field1></div>
  <div><field2>Value 1</field2></div>
  <div><field1>Value 2</field1></div>
  <div><field2>Value 2</field2></div>
</container>
```

### Strategy Selection Algorithm

```go
func shouldUseSequentialStrategy(gqdoc *fetch.Document, rootSel string, fields []Field) bool {
    // Must have date fields
    hasDateField := false
    for _, f := range fields {
        if f.Type == "date_time_tz_ranges" {
            hasDateField = true
            break
        }
    }
    if !hasDateField {
        return false
    }

    // Must end with container element
    endsWithContainer := false
    for _, suffix := range []string{" > div", " > span", " > tr", " > td", " > table"} {
        if strings.HasSuffix(rootSel, suffix) {
            endsWithContainer = true
            break
        }
    }
    if !endsWithContainer {
        return false
    }

    return true  // Use sequential strategy
}
```

---

## Testing

### Test Structure

```
testdata/
  regression/
    basic-detail-pages-flat-w-links-com/
      basic-detail-pages-flat-w-links-com.html       # List page HTML
      detail-page-1.html                              # Detail page HTML
      detail-page-2.html
    basic-detail-pages-flat-w-links-com_configs/
      basic-detail-pages-flat-w-links-com__n10a.yml  # Expected config
      basic-detail-pages-flat-w-links-com__n10a.json # Expected records
```

### Test Flow

```go
func TestGenerate(t *testing.T) {
    // 1. Generate configs from HTML
    configs := generate.ConfigurationsForPage(ctx, cache, opts)

    // 2. Compare generated configs to expected
    assert.YAMLEqual(t, expectedConfig, generatedConfig)

    // 3. Scrape using generated config
    records := scrape.GQDocument(ctx, config, scraper, doc)

    // 4. Compare scraped records to expected
    assert.JSONEqual(t, expectedRecords, scrapedRecords)
}
```

### Running Tests

```bash
# Clean output
rm -r /tmp/goskyr

# Run all tests
time go test -v ./...

# Run specific tests
go test -v ./cmd/goskyr -run TestGenerate/regression/basic-detail-pages-flat-w-links-com
go test -v ./cmd/goskyr -run TestScrape/regression/basic-detail-pages-split-sections-com
```

### Test Categories

- **regression**: Main test suite with real-world examples
- **unit**: Low-level function tests (if any)

---

## Key Algorithms

### Squashing Algorithm

Merges similar paths differing only in nth-child indices:

```go
func squashLocationManager(locations []*locationProps, minOcc int) []*locationProps {
    grouped := groupByPathStructure(locations)  // Ignore nth-child

    result := []*locationProps{}
    for _, group := range grouped {
        if len(group) >= minOcc {
            merged := mergeLocations(group)
            merged.count = len(group)
            merged.examples = flatten(group.examples)
            result = append(result, merged)
        }
    }
    return result
}
```

### Path Distance

Used for clustering related fields:

```go
func (p path) distance(p2 path) float64 {
    commonLen := min(len(p), len(p2))
    diffCount := 0

    for i := 0; i < commonLen; i++ {
        if !p[i].equals(p2[i]) {
            diffCount++
        }
    }

    diffCount += abs(len(p) - len(p2))  // Length difference
    return float64(diffCount)
}
```

### Date Anchor Algorithm (Sequential Scraping)

```go
func scrapeSequential(...) output.Records {
    // 1. Clone HTML tree
    clone := cloneDocument(gqdoc)

    // 2. Remove all non-date elements
    removedNonDateElements(clone)

    // 3. Count remaining date containers
    numRecords := countDateContainers(clone)

    // 4. Extract Nth occurrence of each field
    records := []Record{}
    for i := 0; i < numRecords; i++ {
        record := Record{}
        for _, field := range fields {
            value := extractNthOccurrence(field, i)
            record[field.Name] = value
        }
        records = append(records, record)
    }

    return records
}
```

---

## Performance Considerations

### HTML Parsing

- O(n) where n = HTML size
- Uses Go's `html.Tokenizer` (streaming parser)

### Pattern Detection

- O(p × m) where p = number of paths, m = minOcc iterations
- Most expensive phase

### Root Finding

- O(p × d) where p = number of paths, d = max DOM depth
- Usually fast (shallow trees)

### Detail Pages

- O(u × a) where u = number of URLs, a = analysis time per page
- Can be slow for many detail pages
- Parallel fetching not currently implemented

### Caching

Critical for performance:
- HTTP responses cached
- Rendered JavaScript pages cached
- Resolved redirects cached
- Parsed documents cached in memory

### Typical Timings

- Small page (10 KB, 10 fields): 1-2 seconds
- Medium page (100 KB, 100 fields): 10-20 seconds
- Large page (1 MB, 1000 fields): 60-120 seconds
- Detail pages (10 pages): Add 10-30 seconds

---

## Error Handling

### Generate Package

- **No patterns found**: Returns empty config map, no error
- **Parse errors**: Returns error immediately
- **Scraping failures**: Logs warning, continues with other configs
- **Hash collisions**: Panics (should never happen)

### Scrape Package

- **Element not found**: Try fallback locations, log warning, continue
- **Parse errors**: Log warning, store raw value
- **Validation failures**: Discard record, log info
- **Filter mismatches**: Discard record silently
- **Fatal errors**: Network failures, invalid config → return error

### Logging

Uses structured logging with `log/slog`:

```go
slog.Info("generating configurations",
    "url", opts.URL,
    "minOcc", minOcc,
    "fieldsFound", len(fields))
```

Separate log files per operation when `output.WriteSeparateLogFiles = true`.

---

## Observability

### Tracing

OpenTelemetry tracing for performance analysis:

```go
ctx, span := otel.Tracer("goskyr/generate").Start(ctx, "analyzePage")
defer span.End()
```

### Metrics

Counters and histograms:

```go
observability.Add(ctx, observability.Instruments.Generate, 1,
    attribute.Int("fields_found", len(fields)),
    attribute.Int("records_extracted", len(records)))
```

### Debug Output

- HTML dumps: `/tmp/goskyr/main/*.html`
- Config outputs: `/tmp/goskyr/main/*_configs/*.yml`
- Record outputs: `/tmp/goskyr/main/*_configs/*.json`
- Separate log files per operation

---

## Summary

This fork of goskyr extends the original's list page scraping capabilities with **automatic detail page scraping**. The key innovation is **finding repetition across multiple detail pages** by concatenating them and analyzing the result. This allows a single tool to:

1. **Discover** repeating patterns in list pages (events, products, articles)
2. **Follow** links to detail pages
3. **Discover** repeating patterns across multiple detail pages
4. **Extract** comprehensive records merging list and detail data
5. **Generate** configurations automatically requiring minimal manual adjustment

The fork is designed as a **library** for integration into larger systems, providing:
- Clean cache abstraction for custom storage
- Structured Config and Records types
- Comprehensive error handling
- Observable with tracing and metrics

All with **minimal configuration** through automatic pattern discovery.
