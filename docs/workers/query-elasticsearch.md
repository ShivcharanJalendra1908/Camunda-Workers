
---

## 📁 **5. `docs/workers/query-elasticsearch.md`**

```markdown
# Query Elasticsearch Worker

## Purpose
Generic Elasticsearch query executor for full-text search.

## Task Type
`query-elasticsearch`

## Input Schema
```json
{
  "indexName": "string (required)",
  "queryType": "string (required)",
  "filters": "object (required)",
  "franchiseId": "string (optional)",
  "category": "string (optional)",
  "pagination": {
    "from": "integer",
    "size": "integer"
  }
}

## Output Schema
```json
{
  "data": "array (search results)",
  "totalHits": "integer",
  "maxScore": "float",
  "took": "integer (milliseconds)"
}