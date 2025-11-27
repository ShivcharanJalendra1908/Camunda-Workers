
---

## 📁 **8. `docs/workers/calculate-match-score.md`**

```markdown
# Calculate Match Score Worker

## Purpose
Calculates seeker-franchise compatibility score.

## Task Type
`calculate-match-score`

## Input Schema
```json
{
  "userId": "string",
  "franchiseData": "object",
  "userProfile": "object"
}

## Output Schema
```json
{
  "matchScore": "integer (0-100)",
  "matchFactors": {
    "financialFit": "integer",
    "experienceFit": "integer",
    "locationFit": "integer",
    "interestFit": "integer"
  }
}