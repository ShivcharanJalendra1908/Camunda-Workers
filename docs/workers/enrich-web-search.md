
---

## 📁 **16. `docs/workers/enrich-web-search.md`**

```markdown
# Enrich Web Search Worker

## Purpose
Enriches response with external web data.

## Task Type
`enrich-web-search`

## Input Schema
```json
{
  "question": "string",
  "entities": "array[object]"
}

## Output Schema
```json
{
  "webData": {
    "sources": "array[object]",
    "summary": "string"
  }
}