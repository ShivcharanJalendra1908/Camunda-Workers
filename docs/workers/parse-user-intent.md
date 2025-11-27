
---

## 📁 **14. `docs/workers/parse-user-intent.md`**

```markdown
# Parse User Intent Worker

## Purpose
Analyzes user query intent using NLP/AI.

## Task Type
`parse-user-intent`

## Input Schema
```json
{
  "question": "string",
  "context": "object (conversation history)"
}

## Output Schema
```json
{
  "intentAnalysis": {
    "primaryIntent": "string",
    "confidence": "float"
  },
  "dataSources": "array[string]",
  "entities": "array[object]"
}