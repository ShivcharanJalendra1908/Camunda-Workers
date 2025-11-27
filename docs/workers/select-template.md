
---

## 📁 **3. `docs/workers/select-template.md`**

```markdown
# Select Template Worker

## Purpose
Determines appropriate response template based on context (route, tier, confidence).

## Task Type
`select-template`

## Input Schema
```json
{
  "bibId": "string (optional)",
  "subscriptionTier": "string (required)",
  "routePath": "string (optional)",
  "templateType": "string (optional)",
  "confidence": "number (optional, 0.0-1.0)"
}

## Output Schema
'''json
{
  "selectedTemplateId": "string"
}