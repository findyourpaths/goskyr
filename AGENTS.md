# Goskyr Project Context

**Last Updated**: 2025-11-17
**Project Type**: Go library (CLI tool + importable package)
**Original**: Forked from github.com/jakopako/goskyr

---

## Project Overview

**Goskyr** is a web scraper designed to extract **list-like structured data** from web pages.

**Use cases**:
- Extract lists of events from teacher websites (primary use: paths project)
- Extract books from online book stores
- Extract articles from news sites
- Extract plays from theater listings
- Any structured list data on web pages

**Key features**:
- YAML-based scraper configuration
- CSS selector-based field extraction
- JavaScript rendering support (optional)
- Pagination handling
- Detail page scraping (two-level pattern)
- Semi-automatic configuration generation (experimental)

---

## Architecture

### CLI Tool

**Binary**: `goskyr`

**Commands**:
```bash
goskyr                                # Run scraper with config.yml
goskyr -g <url>                       # Generate config from URL
goskyr -s <scraper-name>             # Run specific scraper
goskyr -debug                         # Enable debug logging
```

### Importable Package

**Package**: `github.com/findyourpaths/goskyr/scrape`

**Usage in paths**:
```go
import "github.com/findyourpaths/goskyr/scrape"

config := scrape.Config{/* ... */}
scraper := scrape.NewScraper(config)
results := scraper.Scrape(ctx, url)
```

---

## Configuration Format

### Basic Config (List Scraping)

```yaml
scrapers:
  - name: "EventList"
    url: "https://example.com/events"
    item: ".event"            # CSS selector for list items
    fields:
      - name: "title"
        selector: ".title"
      - name: "date"
        selector: ".date"
        type: "date"
```

### Two-Level Pattern (List + Detail Pages)

```yaml
scrapers:
  - name: "Events"
    url: "https://example.com/events"
    item: ".event"
    fields:
      - name: "profile_url"
        selector: "a.more"
        attribute: "href"
        detail_page: true      # Marks as detail page URL
      - name: "list_title"     # From list page
        selector: ".title"

    # Detail page config
    detail_fields:
      - name: "full_description"
        selector: ".description"
      - name: "contact_email"
        selector: ".contact"
```

**How it works**:
1. Scrape list page (extract list-level fields + profile URLs)
2. For each item, visit profile URL
3. Extract detail-level fields
4. Merge list fields + detail fields
5. Return combined results

---

## Integration with Paths

The paths project uses goskyr to scrape teacher/event listings from websites.

**Workflow**:
```
1. paths generates goskyr config (via LLM or rules)
   └─ internal/extraction/goskyr_generation.go

2. paths runs goskyr scraper
   └─ internal/extraction/goskyr.go

3. goskyr returns structured JSON

4. paths converts JSON to PersonRecords/EventRecords
   └─ internal/extraction/goskyr_mapping.go
```

**Key files**:
- `internal/extraction/goskyr.go` - Main scraping logic
- `internal/extraction/goskyr_generation.go` - Config generation
- `internal/extraction/goskyr_mapping.go` - JSON → Entity conversion

---

## Technology Stack

**Language**: Go 1.22+
**Web scraping**: github.com/gocolly/colly
**JavaScript rendering**: chromedp (optional)
**Config format**: YAML
**Output format**: JSON

---

## Development Environment

**Build**:
```bash
go build -o goskyr main.go
```

**Run**:
```bash
go run main.go -g <url>
```

**Test**:
```bash
go test ./...
```

---

## Documentation

**Design docs**: [design_overview.md](docs/design/design_overview.md)
**Examples**: [testdata/](testdata/)

---

## Key Differences from Original

**Forked from**: github.com/jakopako/goskyr

**Changes in findyourpaths fork**:
- Required detail URL support (added `require_detail_url` flag)
- Sequential scraping for rate-limiting
- Integration with paths repository pattern
- Custom field mapping for PersonRecords

See: [docs/](docs/) for detailed design docs

---

## Common Commands

**Generate config from URL**:
```bash
goskyr -g https://example.com/events -f
```

**Run scraper**:
```bash
goskyr                           # Use config.yml
goskyr -c path/to/config.yml    # Use specific config
```

**Debug mode**:
```bash
goskyr -debug                    # Verbose logging + HTML dumps
```

**JavaScript rendering**:
```bash
goskyr -r                        # Enable JS rendering
```

---

## Testing

**Test data**: `scrape/testdata/`
**Golden files**: `scrape/testdata/*.html`, `scrape/testdata/*.yaml`

**Run tests**:
```bash
go test ./scrape -v
```

**Update golden files**:
```bash
go test ./scrape -update
```

---

## Related Projects

**paths**: Primary consumer of goskyr (event extraction)
**croncert**: Concert scraper (original use case)

---

## Quick Start for Development

```bash
# Clone or navigate to goskyr
cd /Users/wag/Dropbox/Projects/goskyr

# Build
go build -o goskyr main.go

# Generate config for a site
./goskyr -g https://example.com/events -f

# Review and edit config.yml

# Run scraper
./goskyr

# View results (JSON output to stdout)
```

---

## Related Documentation

**Shared standards** (parent level):
- [go_style.md](../docs/go/go_style.md) - Go coding conventions
- [llm_workflow.md](../docs/llm/llm_workflow.md) - LLM collaboration patterns
- [architecture_fcis.md](../docs/architecture/architecture_fcis.md) - Architecture patterns

**Project-specific**:
- [docs/](docs/) - Design documents
