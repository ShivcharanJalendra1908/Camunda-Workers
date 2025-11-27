
---

## 📁 **7. `docs/workers/apply-relevance-ranking.md`**

```markdown
# Apply Relevance Ranking Worker

## Purpose
Applies custom ranking algorithm to merge and sort search results.

## Task Type
`apply-relevance-ranking`

## Input Schema
```json
{
  "searchResults": "array (from Elasticsearch)",
  "detailsData": "array (from PostgreSQL)",
  "userProfile": "object"
}

## Output Schema
```json
{
  "rankedFranchises": "array (sorted by relevance)"
}