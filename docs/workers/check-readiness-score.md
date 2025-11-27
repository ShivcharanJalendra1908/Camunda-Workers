
---

## 📁 **10. `docs/workers/check-readiness-score.md`**

```markdown
# Check Readiness Score Worker

## Purpose
Calculates franchise seeker readiness score and qualification level.

## Task Type
`check-readiness-score`

## Input Schema
```json
{
  "userId": "string",
  "applicationData": "object"
}

## Output Schema
```json
{
  "readinessScore": "integer (0-100)",
  "qualificationLevel": "string (low|medium|high|excellent)",
  "scoreBreakdown": {
    "financial": "integer",
    "experience": "integer",
    "commitment": "integer",
    "compatibility": "integer"
  }
}