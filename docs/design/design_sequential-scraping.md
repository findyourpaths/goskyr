# Sequential Scraping Strategy - Design Document

## Overview

This document describes the design and implementation of the sequential scraping strategy for goskyr, which enables extraction of structured data from flat HTML where records are represented as sequences of sibling elements rather than nested structures.

## Problem Statement

### Background

Traditional web scraping assumes a nested HTML structure where each record is contained within a parent element:

```html
<div class="record">
  <span class="date">Feb 3, 2023</span>
  <span class="name">Alice</span>
  <a href="/alice">Link</a>
</div>
<div class="record">
  <span class="date">Feb 4, 2023</span>
  <span class="name">Bob</span>
  <a href="/bob">Link</a>
</div>
```

However, many websites use flat structures where record fields are siblings at the same level:

```html
<div class="container">
  <div><span class="date">Feb 3, 2023</span></div>
  <div><span class="name">Alice</span></div>
  <div><a href="/alice">Link</a></div>
  <div><span class="date">Feb 4, 2023</span></div>
  <div><span class="name">Bob</span></div>
  <div><a href="/bob">Link</a></div>
</div>
```

### Challenge

In flat structures:
1. There is no parent element wrapping each logical record
2. Fields belonging to the same record must be identified and grouped
3. A delimiter signal is needed to determine where one record ends and another begins
4. Invalid records (missing required fields) must be filtered out

## Requirements

### Functional Requirements

**FR-1: Pattern Detection and Chunking**
- Detect date elements as start signals for new records
- Group sibling elements into chunks based on date delimiters
- Each chunk represents one logical record

**FR-2: Record Validation**
- Validate that each chunk contains required fields (date and "call to action" (CTA) link)
- Skip chunks that don't meet validation criteria
- Support configurable validation rules via `ValidationConfig`

**FR-3: Scraping Execution**
- Extract field values from each validated chunk
- Apply field selectors relative to chunk content
- Produce output records with all fields merged into single records

**FR-4: Auto-Generation Support**
- Automatically detect when sequential strategy should be used
- Generate appropriate configuration with parent selector
- Set validation requirements for CTA elements

### Non-Functional Requirements

**NFR-1: Backward Compatibility**
- Default to nested strategy when `strategy` field is not specified
- Existing configurations continue to work without modification

**NFR-2: Performance**
- Chunking operates in O(n) time for n child elements
- Minimal overhead compared to nested strategy

**NFR-3: Maintainability**
- Clear separation between nested and sequential strategies
- Well-defined interfaces for validation and field extraction

## Solution Design

### Architecture

The solution introduces a new scraping strategy alongside the existing nested approach:

```
┌─────────────────────────────────────────────────────────────┐
│                      scrape.GQDocument()                     │
│  Entry point that routes to appropriate strategy             │
└────────────┬──────────────────────────────────┬─────────────┘
             │                                   │
             ▼                                   ▼
   ┌─────────────────┐              ┌──────────────────────┐
   │ Nested Strategy │              │ Sequential Strategy  │
   │   (default)     │              │  (strategy: seq.)    │
   │                 │              │                      │
   │ Each child is   │              │ 1. Chunk by dates    │
   │ a record        │              │ 2. Validate chunks   │
   │                 │              │ 3. Extract fields    │
   └─────────────────┘              └──────────────────────┘
```

### Key Components

#### 1. Strategy Field
**Location**: `scrape/scrape.go:355`

```go
type Scraper struct {
    Strategy string `yaml:"strategy,omitempty"` // "nested" (default) or "sequential"
    // ... other fields
}
```

**Purpose**: Determines which scraping algorithm to use
**Values**:
- Empty or `"nested"`: Traditional nested scraping (default)
- `"sequential"`: Sequential chunking-based scraping

#### 2. Validation Configuration
**Location**: `scrape/scrape.go:363-365`

```go
type ValidationConfig struct {
    RequiresCTASelector string `yaml:"requires_cta_selector,omitempty"`
}
```

**Purpose**: Defines validation rules for chunks
**Usage**: Ensures each chunk contains required elements (e.g., links)

#### 3. Sequential Scraping Function
**Location**: `scrape/scrape.go:621-732`

```go
func scrapeSequential(ctx context.Context, c *Config, s *Scraper,
    parentSel *goquery.Selection, baseURL string, gqdoc *fetch.Document) (output.Records, error)
```

**Algorithm**:
1. **Chunking Phase**:
   - Iterate through parent's children
   - Detect date elements using `isDateElement()`
   - Start new chunk when date found
   - Add subsequent siblings to current chunk until next date

2. **Validation Phase**:
   - Check each chunk has a date element
   - Verify CTA selector (if configured) finds elements
   - Skip chunks that fail validation

3. **Extraction Phase**:
   - Serialize chunk HTML
   - Parse into goquery document
   - Apply field selectors to extract values
   - Create output record with all fields

#### 4. Date Detection
**Location**: `scrape/scrape.go:609-619`

```go
func isDateElement(sel *goquery.Selection) bool
```

**Purpose**: Identifies elements that mark record boundaries
**Implementation**: Checks for presence of date-specific span class (`span.a-3y`)
**Design Decision**: Uses structural marker rather than content parsing to avoid false positives

#### 5. Auto-Generation Detection
**Location**: `generate/generate.go:26-66`

```go
func shouldUseSequentialStrategy(gqdoc *fetch.Document, rootSel string,
    fields []scrape.Field) bool
```

**Detection Criteria**:
- Presence of date field (`date_time_tz_ranges` type)
- Selector targets individual children (`> div` or `> span`)
- Parent has many children (> fields × 2)

**Result**: Sets `strategy: sequential` and adjusts selector to parent

## Implementation Details

### Data Flow

```
HTML Input → Parse Document → Select Parent → Chunk Children
                                                     ↓
Output Records ← Extract Fields ← Validate Chunks ← Group by Dates
```

### Configuration Example

#### Input HTML
```html
<div class="a-1 b-1">
  <div><span class="a-3y">Feb 3, 2023</span></div>
  <div><span class="a-3n">Alice</span></div>
  <div><span class="a-3x"><a href="/alice-address">Address</a></span></div>
  <div><span class="a-3y">Feb 4, 2023</span></div>
  <div><span class="a-3n">Bob</span></div>
  <div><span class="a-3x"><a href="/bob-address">Address</a></span></div>
</div>
```

#### Configuration
```yaml
scrapers:
  - name: example
    url: https://example.com
    strategy: sequential
    selector: body > div.a-0.b-0 > div.a-1.b-1
    validation:
      requires_cta_selector: div > span.a-3x > a
    fields:
      - name: Fname
        type: text
        location:
          - selector: span.a-3n
      - name: Fdate
        type: date_time_tz_ranges
        location:
          - selector: span.a-3y
      - name: Flink
        type: url
        location:
          - selector: span.a-3x > a
            attr: href
```

#### Output
```json
[
  {
    "Fname": "Alice",
    "Fdate": "Feb 3, 2023",
    "Fdate__Pdate_time_tz_ranges": "2023-02-03TZ",
    "Flink": "/alice-address",
    "Flink__Aurl": "https://example.com/alice-address"
  },
  {
    "Fname": "Bob",
    "Fdate": "Feb 4, 2023",
    "Fdate__Pdate_time_tz_ranges": "2023-02-04TZ",
    "Flink": "/bob-address",
    "Flink__Aurl": "https://example.com/bob-address"
  }
]
```

### Selector Strategy

**Nested Mode**:
- Selector: `body > div.container > div.record` (selects each record)
- Each selection is one record

**Sequential Mode**:
- Selector: `body > div.container` (selects parent container)
- Children are chunked into records
- Rationale: Parent selection allows iteration over all children

### Testing Strategy

#### Test Files
**Location**: `testdata/regression/basic-detail-pages-flat-w-links-com/`

**Test Structure**:
- HTML file with flat structure
- Config YML with sequential strategy
- Expected JSON output with merged records
- Test validates actual vs expected output

#### Test Execution
```bash
# Run specific test
go test -v -run TestScrape/regression/basic-detail-pages-flat-w-links-com ./cmd/goskyr

# Regenerate test data
env GOWORK=off go run ./cmd/goskyr regenerate
```

#### Test Coverage
- Chunking with multiple records
- Date detection and boundary marking
- Field extraction from chunks
- Validation (CTA requirement)
- Auto-generation detection
- Cache handling

### Files Modified

#### Core Scraping
- `scrape/scrape.go:355`: Added `Strategy` field to `Scraper`
- `scrape/scrape.go:363-365`: Added `ValidationConfig` struct
- `scrape/scrape.go:554-562`: Route to sequential strategy
- `scrape/scrape.go:609-619`: Implement `isDateElement()`
- `scrape/scrape.go:621-732`: Implement `scrapeSequential()`

#### Auto-Generation
- `generate/generate.go:26-66`: Implement `shouldUseSequentialStrategy()`
- `generate/generate.go:367-384`: Detect and apply sequential strategy
- `generate/generate.go:375-383`: Add validation config for sequential mode

#### Testing
- `cmd/goskyr/main_test.go:303-311`: Write configs to output directory
- `testdata/regression/basic-detail-pages-flat-w-links-com/`: Test data

#### Observability
- `observability/instruments.go`: Add nil checks to prevent panics in tests

## Edge Cases and Error Handling

### Edge Cases

1. **No Date Elements**:
   - Behavior: No chunks created
   - Result: Empty output records
   - Rationale: Dates are required delimiters

2. **Single Field per Chunk**:
   - Behavior: Chunk validated if meets requirements
   - Result: Record with some empty fields
   - Rationale: `# fields are optional by default` allows optional fields

3. **Orphaned Elements Before First Date**:
   - Behavior: Elements ignored until first date found
   - Result: Elements not included in any record
   - Rationale: Date marks record start

4. **Missing CTA Element**:
   - Behavior: Chunk skipped during validation
   - Result: Record not included in output
   - Rationale: Validation ensures data quality

### Error Handling

1. **HTML Parsing Errors**:
   - Location: `scrape/scrape.go:691-695`
   - Action: Log warning, continue with next chunk
   - Impact: Graceful degradation

2. **Field Extraction Errors**:
   - Location: `scrape/scrape.go:714-718`
   - Action: Log warning, continue with next chunk
   - Impact: Partial results returned

3. **Nil Observability**:
   - Location: `observability/instruments.go`
   - Action: Check for nil before use
   - Impact: Tests run without observability

## Performance Characteristics

### Time Complexity
- Chunking: O(n) where n = number of child elements
- Validation: O(m) where m = number of chunks
- Extraction: O(m × f) where f = number of fields per chunk
- Overall: O(n + m × f)

### Space Complexity
- Chunk storage: O(m × k) where k = average chunk size
- Output records: O(m × f)
- HTML serialization: O(k) per chunk (temporary)

### Optimization Opportunities
1. **Streaming Processing**: Process chunks as they're created
2. **Parallel Extraction**: Extract fields from multiple chunks concurrently
3. **Selector Caching**: Cache compiled selectors for field extraction

## Future Enhancements

### Potential Improvements

1. **Configurable Delimiters**:
   - Allow custom delimiter detection beyond dates
   - Example: Detect by specific CSS class or data attribute

2. **Multi-Level Chunking**:
   - Support nested chunking for complex hierarchies
   - Example: Group weeks containing days containing events

3. **Pattern Learning**:
   - Machine learning to detect record boundaries
   - Reduce manual configuration requirements

4. **Validation Extensions**:
   - Multiple validation rules (AND/OR logic)
   - Field value validation (regex, ranges)
   - Cross-field validation

5. **Performance Optimizations**:
   - Parallel chunk processing
   - Incremental parsing for large documents
   - Memory-efficient streaming mode

### Compatibility Considerations

All enhancements must maintain:
- Backward compatibility with existing configs
- Clear upgrade path for users
- Consistent API contracts

## Lessons Learned

### Design Decisions

1. **Structural vs. Content-Based Detection**:
   - Initial attempt used content parsing (`datetime.Parse()`)
   - Problem: False positives (non-dates parsed as dates)
   - Solution: Use structural markers (`span.a-3y` class)
   - Lesson: Structural hints more reliable than content analysis

2. **Selector Hierarchy**:
   - Sequential mode requires parent selector, not child
   - Auto-generation adjusts selector automatically
   - Lesson: Strategy affects optimal selector choice

3. **Validation Placement**:
   - Validation happens after chunking, before extraction
   - Early filtering improves performance
   - Lesson: Fail fast on invalid data

### Implementation Insights

1. **HTML Serialization**:
   - goquery selections can't be reused across documents
   - Must serialize to HTML and re-parse for field extraction
   - Impact: Small performance overhead, necessary for correctness

2. **Test Cache Management**:
   - Tests cache HTML files in `/tmp`
   - Stale cache caused confusing test failures
   - Solution: Always clear cache before test runs
   - Lesson: Cache invalidation is critical

3. **Field Type Determination**:
   - Field types determined during `processFields()`
   - Not stored in intermediate `locationProps`
   - Lesson: Understand data flow before extending

## References

### Related Files
- Original requirements: `seqs.md`
- Test HTML: `testdata/regression/basic-detail-pages-flat-w-links-com/basic-detail-pages-flat-w-links-com.html`
- Test config: `testdata/regression/basic-detail-pages-flat-w-links-com_configs/basic-detail-pages-flat-w-links-com__10a.yml`

### External Documentation
- goquery: https://github.com/PuerkitoBio/goquery
- CSS Selectors: https://www.w3.org/TR/selectors/
- phil datetime parser: `/Users/wag/Dropbox/Projects/phil`

## Appendix

### Complete Example

#### Test HTML
```html
HTTP/0.0 200 OK

<html>
  <head><title>Wypipo</title></head>
  <body>
    <div class="a-0 b-0">
      <div class="a-1 b-1">
          <div><span class="a-3y">Feb 3, 2023</span></div>
          <div><span class="a-3n">Alice</span></div>
          <div><span class="a-3x"><a href="/alice-address">Address</a></span></div>
          <div><span class="a-3y">Feb 4, 2023</span></div>
          <div><span class="a-3n">Bob</span></div>
          <div><span class="a-3x"><a href="/bob-address">Address</a></span></div>
          <!-- ... more records ... -->
        </div>
      </div>
    </div>
  </body>
</html>
```

#### Auto-Generated Configuration
```yaml
id:
    slug: basic-detail-pages-flat-w-links-com
    id: 10a
    field: ""
    subid: ""
writer:
    type: stdout
    uri: ""
    user: ""
    password: ""
    filepath: ""
scrapers:
    - name: basic-detail-pages-flat-w-links-com__10a
      render_js: true
      selector: body > div.a-0.b-0 > div.a-1.b-1
      strategy: sequential
      url: https://basic-detail-pages-flat-w-links.com
      validation:
        requires_cta_selector: span.a-3x > a
      fields:
        - name: Ff2b40e9e-href-0
          type: url
          location:
            - selector: span.a-3x > a
              attr: href
          # fields are optional by default
        - name: F5af81126--0
          type: date_time_tz_ranges
          location:
            - selector: span.a-3y
          # fields are optional by default
        - name: Fd92b94e1--0
          type: text
          location:
            - selector: span.a-3n
          # fields are optional by default
global:
    user-agent: goskyr web scraper (github.com/findyourpaths/goskyr)
records: []
```

#### Output Records
```json
[
  {
    "Atitle": "Wypipo",
    "Aurl": "https://basic-detail-pages-flat-w-links.com",
    "F5af81126--0": "Feb 3, 2023",
    "F5af81126--0__Pdate_time_tz_ranges": "2023-02-03TZ",
    "Fd92b94e1--0": "Alice",
    "Ff2b40e9e-href-0": "/alice-address",
    "Ff2b40e9e-href-0__Aurl": "https://basic-detail-pages-flat-w-links.com/alice-address"
  },
  {
    "Atitle": "Wypipo",
    "Aurl": "https://basic-detail-pages-flat-w-links.com",
    "F5af81126--0": "Feb 4, 2023",
    "F5af81126--0__Pdate_time_tz_ranges": "2023-02-04TZ",
    "Fd92b94e1--0": "Bob",
    "Ff2b40e9e-href-0": "/bob-address",
    "Ff2b40e9e-href-0__Aurl": "https://basic-detail-pages-flat-w-links.com/bob-address"
  }
]
```

### Glossary

- **Chunk**: A group of sibling HTML elements representing one logical record
- **CTA (Call-to-Action)**: A link or button element required for validation
- **Delimiter**: An element that marks the boundary between records (dates in this implementation)
- **Flat Structure**: HTML where record fields are siblings rather than nested
- **Nested Structure**: Traditional HTML where each record has a parent container
- **Sequential Strategy**: Scraping mode that chunks flat sibling elements into records
- **Validation**: Rules to ensure chunks contain required elements before extraction

---

**Document Version**: 1.0
**Last Updated**: 2025-10-06
**Author**: Generated from implementation in goskyr project
