
---

## 📁 **15. `docs/workers/query-internal-data.md`**

```markdown
# Query Internal Data Worker

## Purpose
Retrieves internal franchise data based on extracted entities.

## Task Type
`query-internal-data`

## Input Schema
```json
{
  "entities": "array[object]",
  "dataSources": "array[string]"
}

## Output Schema
```json
{
  "internalData": "object (aggregated data)"
}