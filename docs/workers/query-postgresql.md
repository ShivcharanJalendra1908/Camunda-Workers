
---

## 📁 **4. `docs/workers/query-postgresql.md`**

```markdown
# Query PostgreSQL Worker

## Purpose
Generic PostgreSQL query executor with parameterized query types.

## Task Type
`query-postgresql`

## Input Schema
```json
{
  "queryType": "string (required)",
  "franchiseId": "string (optional)",
  "franchiseIds": "array[string] (optional)",
  "userId": "string (optional)",
  "filters": "object (optional)"
}

## Output Schema
```json
{
  "data": "object|array (query result)",
  "rowCount": "integer",
  "queryExecutionTime": "integer (milliseconds)"
}