# Goskyr Documentation

**Fork of**: [jakopako/goskyr](https://github.com/jakopako/goskyr)

Web scraper for extracting structured list data from web pages.

---

## Design Documents

| Document | Description |
|----------|-------------|
| [design_overview.md](design/design_overview.md) | Fork architecture, extraction strategies, configuration format |
| [design_scrape.md](design/design_scrape.md) | Scrape package: strategies, field extraction, pagination, detail pages |
| [design_generate.md](design/design_generate.md) | Generate package: pattern detection, config generation pipeline |
| [design_sequential-scraping.md](design/design_sequential-scraping.md) | Sequential extraction strategy for flat HTML structures |
| [design_sequential-field-extraction.md](design/design_sequential-field-extraction.md) | Field extraction challenges in sequential mode |

---

## Related

- [AGENTS.md](../AGENTS.md) — Project context for LLM collaboration
- [../docs/go/go_style.md](../../docs/go/go_style.md) — Go coding conventions
- [../docs/llm/README.md](../../docs/llm/README.md) — LLM collaboration rules
