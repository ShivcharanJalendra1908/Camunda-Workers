
---

## 📁 **2. `docs/workers/build-response.md`**

```markdown
# Build Response Worker

## Purpose
Constructs standardized response payload for BFF callback with metadata.

## Task Type
`build-response`

## Input Schema
```json
{
  "templateId": "string (required)",
  "requestId": "string (required)",
  "data": "object (dynamic based on template)",
  "metadata": "object (optional)"
}

## Output Schema
'''json
{
  "response": {
    "requestId": "string",
    "status": "string (success|error)",
    "data": "object",
    "metadata": {
      "timestamp": "string (ISO 8601)",
      "version": "string"
    }
  }
}