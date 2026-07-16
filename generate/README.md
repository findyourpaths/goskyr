# Generate Package

Analyzes fetched documents and produces executable scraper configurations for repeating record structures.

## Key Types/Functions

- `ConfigOptions` — generation inputs, strategy controls, and acceptance bounds
- `ConfigurationsForPage(ctx, cache, opts)` — generates configurations from a fetched page
- `ConfigurationsForGQDocument(ctx, cache, opts, doc)` — generates configurations from an analyzed document
- `ConfigurationsForAllDetailPages(ctx, cache, opts, configs)` — extends list configurations with detail-page extraction
- `JoinGQDocuments(docs)` — combines detail documents for shared structural analysis

## Conventions

- Generated configurations must be executed before return; candidate fields describe evidence reproduced by their generated records.
- Keep constant fields with an authored `Value`; keep dynamic fields only when at least one generated record contains a non-empty aligned value.
- Recompute validation that references generated fields after pruning those fields.
- Nested and sequential strategies share record-alignment and minimum-record acceptance rules.
- Preserve deterministic config IDs and selector-derived field names across equivalent input documents.

## Design Docs

- [design_generate.md](../docs/design/design_generate.md) — generation phases, strategy selection, and pruning
- [design_overview.md](../docs/design/design_overview.md) — repository architecture and record-aligned generation contract
- [design_sequential-scraping.md](../docs/design/design_sequential-scraping.md) — sequential extraction strategy
