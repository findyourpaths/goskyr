# basic-detail-pages-split-sections-com

## Purpose

This test case demonstrates the **multi-section sequential scraping issue** where related content is split across non-sibling HTML sections, causing the sequential scraper to fail to correctly group data into individual records.

## HTML Structure

The HTML contains 5 records, where each record's data is split across TWO separate `<div>` siblings:

```html
<div class="container">
  <!-- Record 1 - Info -->
  <div class="event-info">
    <span class="date">Feb 3, 2023</span>
    <span class="name">Alice</span>
    <a href="/alice-address">Address</a>
  </div>
  <!-- Record 1 - Description -->
  <div class="event-desc">
    <span class="description">Alice is a security researcher...</span>
  </div>

  <!-- Record 2 - Info -->
  <div class="event-info">...</div>
  <!-- Record 2 - Description -->
  <div class="event-desc">...</div>
  ...
</div>
```

## Expected Behavior

The scraper should produce **5 records**, each containing:
- `date`: Individual date (e.g., "Feb 3, 2023")
- `name`: Individual name (e.g., "Alice")
- `link`: Individual link (e.g., "/alice-address")
- `description`: Individual description (e.g., "Alice is a security researcher...")

Expected output (`basic-detail-pages-split-sections-com__s10a.json`):
```json
[
  {
    "date": "Feb 3, 2023",
    "name": "Alice",
    "link": "/alice-address",
    "description": "Alice is a security researcher specializing in cryptography."
  },
  {
    "date": "Feb 4, 2023",
    "name": "Bob",
    "link": "/bob-address",
    "description": "Bob works on distributed systems and consensus protocols."
  },
  ...
]
```

## Actual Behavior (FAILING)

The sequential scraper currently produces **1 record** with all values concatenated:

```json
[
  {
    "date": "Feb 3, 2023\nFeb 4, 2023\nFeb 5, 2023\nFeb 6, 2023\nFeb 7, 2023",
    "name": "Alice\nBob\nCarol\nDan\nEve",
    "link": "/alice-address",
    "description": "Alice is a security researcher...\nBob works on distributed systems...\nCarol focuses on...\nDan specializes in...\nEve researches..."
  }
]
```

## Root Cause

The sequential scraping algorithm:
1. Finds root selector: `body > div.container`
2. Selects all sibling `<div>` elements under the container (10 divs: 5 event-info + 5 event-desc)
3. Uses date-based chunking to group siblings into records
4. **Problem**: All dates are in `event-info` divs, so it creates ONE chunk containing all 10 divs
5. Extracts all values from the single chunk, concatenating everything together

The scraper cannot understand that:
- `event-info` divs belong together with their immediately following `event-desc` div
- Each pair (info + desc) should form one record
- The pattern alternates: info, desc, info, desc, ...

## Why This Matters

This pattern appears in real-world email HTML where:
- Event details (image, date, CTA) are in one section
- Event descriptions are in a separate section immediately after
- Email templating systems generate these as sibling sections, not nested

Example: Email ID `1844972871065198150` has this exact structure, resulting in:
- 9 generated records
- Only 2 with correct descriptions (22% success rate)
- desc field quality score: 17%
- Overall quality: 0.3/100 (Grade F)

## Solution Approach

To fix this, the sequential scraper needs to:

### Option 1: Section-Pair Recognition
Detect alternating section patterns and pair them:
- If siblings alternate between two types (A, B, A, B, A, B...)
- And type A contains dates but type B doesn't
- Group them as pairs: (A1+B1), (A2+B2), (A3+B3)...

### Option 2: Proximity-Based Chunking
After date-based chunking, associate nearby non-date sections:
- Chunk by dates: [info1, desc1], [info2, desc2], [info3, desc3]...
- If a desc section immediately follows an info section
- Merge them into the same record

### Option 3: Multi-Selector Strategy
Allow configs to define related selectors:
```yaml
primary_selector: div.event-info
associated_selectors:
  - selector: div.event-desc
    merge_strategy: next_sibling
```

## Testing

Run this test:
```bash
cd /Users/wag/Dropbox/Projects/goskyr
go test ./cmd/goskyr -v -run TestGenerate/regression/basic-detail-pages-split-sections
```

Check if the generated `basic-detail-pages-split-sections-com__s10a.json` matches the expected output.

## Related

- Issue documented in: `/Users/wag/Dropbox/Projects/paths/docs/sequential-scraping-multi-section-issue.md`
- Real-world case: Email `1844972871065198150` in paths system
- Similar test (working): `basic-detail-pages-flat-w-links-com` (all data in consecutive siblings)
