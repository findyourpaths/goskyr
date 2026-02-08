# Sequential Scraping Field Extraction Issue

## Overview

Sequential scraping has a fundamental issue with field extraction: **CSS selectors that work in nested mode fail in sequential mode** because the HTML structure is reconstructed during chunk assembly.

## The Problem

### How Sequential Scraping Works

1. **Identify chunks**: Iterate through sibling elements and group them into chunks based on date boundaries
2. **Extract chunk HTML**: For each chunk, extract the outer HTML of each element
3. **Reassemble HTML**: Concatenate the chunk HTML strings and parse into a new document
4. **Extract fields**: Apply field selectors to the reassembled document

### Why Field Extraction Fails

The field selectors in the config are designed for the **original document structure**, but sequential scraping applies them to **reassembled chunk documents** with different structure.

## Detailed Example

### Original HTML Structure

```html
<body>
  <table>
    <tbody>
      <tr>
        <td>
          <div>
            <div>
              <table class="wr">
                <tbody>
                  <tr>
                    <td>
                      <table>
                        <tbody>
                          <!-- CHUNK 1 STARTS HERE -->
                          <tr>
                            <td class="cn">
                              <table class="cn">
                                <tbody>
                                  <tr>
                                    <td class="tb tm">
                                      <p>
                                        <span>
                                          <span>
                                            <span>October 7th</span>  <!-- DATE ELEMENT -->
                                          </span>
                                        </span>
                                      </p>
                                    </td>
                                  </tr>
                                </tbody>
                              </table>
                            </td>
                          </tr>
                          <!-- CHUNK 1 ENDS HERE -->

                          <!-- CHUNK 2 STARTS HERE -->
                          <tr>
                            <td class="cn">
                              <table class="cn">
                                <tbody>
                                  <tr>
                                    <td class="tb tm">
                                      <p>
                                        <span>
                                          <span>
                                            <span>October 25th</span>  <!-- DATE ELEMENT -->
                                          </span>
                                        </span>
                                      </p>
                                    </td>
                                  </tr>
                                </tbody>
                              </table>
                            </td>
                          </tr>
                          <!-- CHUNK 2 ENDS HERE -->
                        </tbody>
                      </table>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </td>
      </tr>
    </tbody>
  </table>
</body>
```

### Nested Mode (Working)

**Parent Selector:** `body > table > tbody > tr > td > div > div > table.wr > tbody > tr > td > table > tbody > tr`

This matches each `<tr>` element (the chunks). For each matched element:

**Date Field Selector:** `tbody > tr > td.cn > table.cn > tbody > tr > td.tb.tm > p > span > span > span`

Starting from the `<tr>` element, this selector navigates DOWN to find the date. It works because:
- The selector starts at the `<tr>` (chunk root)
- Navigates through: `<tr>` → `<td class="cn">` → `<table class="cn">` → `<tbody>` → `<tr>` → `<td class="tb tm">` → `<p>` → `<span>` → `<span>` → `<span>`
- Successfully finds: `"October 7th"`

### Sequential Mode (Broken)

**Step 1: Identify Chunks**

The sequential scraper identifies chunks by finding date elements. It uses the parent selector but removes the last element:

**Parent Selector:** `body > table > tbody > tr > td > div > div > table.wr > tbody > tr > td > table > tbody`

This matches the `<tbody>` containing all the `<tr>` chunks. The scraper iterates through its children (the `<tr>` elements) and groups them into chunks based on date boundaries.

**Step 2: Extract Chunk HTML**

For chunk 1, it extracts the outer HTML of the `<tr>` element:

```html
<tr>
  <td class="cn">
    <table class="cn">
      <tbody>
        <tr>
          <td class="tb tm">
            <p>
              <span>
                <span>
                  <span>October 7th</span>
                </span>
              </span>
            </p>
          </td>
        </tr>
      </tbody>
    </table>
  </td>
</tr>
```

**Step 3: Reassemble into New Document**

The chunk HTML is parsed into a new document:

```html
<html>
  <head></head>
  <body>
    <tr>
      <td class="cn">
        <table class="cn">
          <tbody>
            <tr>
              <td class="tb tm">
                <p>
                  <span>
                    <span>
                      <span>October 7th</span>
                    </span>
                  </span>
                </p>
              </td>
            </tr>
          </tbody>
        </table>
      </td>
    </tr>
  </body>
</html>
```

**Step 4: Apply Field Selectors**

The scraper gets the `<body>` element from the reassembled document and tries to extract fields.

**Date Field Selector:** `tbody > tr > td.cn > table.cn > tbody > tr > td.tb.tm > p > span > span > span`

Starting from `<body>`, this selector tries to find:
- `<body>` → `<tbody>` ❌ **FAILS** - `<body>` has a `<tr>` child, not a `<tbody>` child

The selector fails because it expects the path to start with `<tbody>`, but in the reassembled document, the root is `<body>` and its immediate child is `<tr>`, not `<tbody>`.

### Why the Selector Doesn't Match

In the original document:
```
<tr> (parent selector matches here)
  └─ tbody > tr > td.cn > ... (field selector works from here)
```

In the reassembled chunk document:
```
<body> (this is the root we search from)
  └─ <tr> (the chunk HTML starts here)
      └─ tbody > tr > td.cn > ...
```

The field selector expects to find `tbody` as a direct child of the starting point, but in the reassembled document, `tbody` is nested deeper (inside `<table class="cn">`).

## Why Some Fields Work and Others Don't

Looking at the config, we see fields are extracted but dates are empty:

```yaml
fields:
  - name: Fd1f7685c-href-0          # ✅ Works
    type: url
    location:
      - selector: tbody > tr > td.cn > table.cn > tbody > tr > td > a
        attr: href

  - name: Fbad2a48c--1              # ❌ Fails (empty)
    type: date_time_tz_ranges
    location:
      - selector: tbody > tr > td.cn > table.cn > tbody > tr > td.tb.tm > p > span > span > span
        child_index: 1
```

Both selectors start with `tbody > tr > td.cn > table.cn > tbody`, which should fail in the reassembled document. However, the URL field might be working because:

1. **Can be empty**: Both fields have `# fields are optional by default`, so extraction failures don't cause errors
2. **Alternative matching**: The selector might accidentally match different elements in the reassembled structure
3. **Partial matching**: Some CSS selector implementations are more lenient

But in reality, looking at the sequential JSON output, the URL field `Fd1f7685c-href-0` actually **does have values**, while the date field `Fbad2a48c--1` is empty. This suggests the issue is more nuanced.

## The Actual Root Cause

After deeper investigation, the real issue is that the reassembled HTML is not well-formed when it starts with `<tr>`:

```html
<body>
  <tr>...</tr>  <!-- Invalid: <tr> must be inside <tbody> -->
</body>
```

Modern HTML parsers auto-correct this by wrapping the `<tr>` in a `<tbody>`:

```html
<body>
  <tbody>  <!-- Auto-inserted by parser -->
    <tr>...</tr>
  </tbody>
</body>
```

So the actual structure becomes:
```
<body>
  └─ <tbody> (auto-inserted)
      └─ <tr> (chunk root)
          └─ <td class="cn">
              └─ <table class="cn">
                  └─ <tbody>
                      └─ <tr>
                          └─ <td class="tb tm">
                              └─ <p>
                                  └─ <span> → <span> → <span>October 7th</span>
```

Now the field selector `tbody > tr > td.cn > table.cn > tbody > tr > td.tb.tm > p > span > span > span` starting from `<body>`:
- `<body>` → `tbody` ✅ (the auto-inserted one)
- `tbody` → `tr` ✅ (the chunk root)
- `tr` → `td.cn` ✅
- `td.cn` → `table.cn` ✅
- `table.cn` → `tbody` ✅
- `tbody` → `tr` ✅
- `tr` → `td.tb.tm` ✅
- `td.tb.tm` → `p` ✅
- `p` → `span` ✅
- `span` → `span` ✅
- `span` → `span` ✅ **SHOULD MATCH!**

But it still returns empty. The issue must be with `child_index: 1`.

## The Child Index Problem

The date field has `child_index: 1`, which means "get the 2nd child node (0-indexed)" of the matched element.

In the original document, the `<span>` element that matches the selector might have multiple children, and the date is at index 1.

In the reassembled document, the HTML structure might be slightly different due to:
1. **Whitespace handling**: The HTML parser may add or remove text nodes (whitespace)
2. **Node reconstruction**: Serializing and re-parsing HTML can change the DOM tree
3. **Element nesting**: The auto-correction might affect child node counts

This is why the field extraction works in nested mode but fails in sequential mode - the `child_index` no longer points to the correct element in the reassembled structure.

## Solution Approaches

### 1. Fix Field Selectors for Sequential Mode
Adjust selectors to work on reassembled chunk structure (complex, fragile)

### 2. Extract Fields Before Chunking
Extract field values from original document, then associate with chunks based on element position (requires significant refactoring)

### 3. Use Simpler Selectors
Avoid `child_index` and deep nesting; use more robust selectors that work in both contexts

### 4. Preserve Original DOM References
Instead of serializing/deserializing HTML, keep references to original DOM nodes and extract fields from them (best approach but requires major refactoring)

## Current Status

Sequential scraping works for:
- ✅ Identifying chunks based on date boundaries
- ✅ Creating records (9 records generated)
- ✅ Extracting some fields (URLs, text)

Sequential scraping fails for:
- ❌ Extracting date fields with `child_index`
- ❌ Extracting fields with complex nested selectors that depend on exact DOM structure

## Solution: HTML Normalization

The implemented solution normalizes HTML at the point where documents are first loaded (in `fetch.NewDocumentFromString()`). This ensures that:

1. **Pattern generation** analyzes normalized HTML structure
2. **Nested scraping** extracts from normalized HTML
3. **Sequential scraping** reassembles chunks with the same normalized structure

The normalization process:
1. Parses HTML string into a DOM tree using `html.Parse()`
2. Serializes the DOM back to HTML using `html.Render()`
3. This applies all browser-style auto-corrections (like wrapping `<tr>` in `<tbody>`) consistently

Because the same normalization is applied during both config generation and scraping, the CSS selectors generated during analysis work correctly during extraction, even after chunk reassembly in sequential mode.

### Implementation

See `/Users/wag/Dropbox/Projects/goskyr/fetch/cache.go:80-95` for the `normalizeHTML()` function and its usage in `NewDocumentFromString()`.

This approach is superior to other alternatives because:
- It's applied once at document load time (minimal performance impact)
- It ensures consistency across all scraping modes (nested and sequential)
- It preserves all the benefits of the existing selector-based field extraction
- It requires no changes to config generation or field extraction logic
