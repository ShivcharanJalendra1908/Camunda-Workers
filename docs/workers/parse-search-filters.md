
---

## 📁 **6. `docs/workers/parse-search-filters.md`**

```markdown
# Parse Search Filters Worker

## Purpose
Parses and normalizes search query parameters from user input.

## Task Type
`parse-search-filters`

## Input Schema
```json
{
  "rawFilters": "object (query parameters)"
}

## Output Schema
```json
{
  "parsedFilters": {
    "categories": "array[string]",
    "investmentRange": {
      "min": "integer",
      "max": "integer"
    },
    "locations": "array[string]",
    "keywords": "string",
    "sortBy": "string",
    "pagination": {
      "page": "integer",
      "size": "integer"
    }
  }
}