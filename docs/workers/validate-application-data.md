
---

## 📁 **9. `docs/workers/validate-application-data.md`**

```markdown
# Validate Application Data Worker

## Purpose
Validates franchise application data against business rules.

## Task Type
`validate-application-data`

## Input Schema
```json
{
  "applicationData": {
    "personalInfo": "object",
    "financialInfo": "object",
    "experience": "object"
  },
  "franchiseId": "string"
}

## Output Schema
```json
{
  "isValid": "boolean",
  "validatedData": "object (cleaned data)",
  "validationErrors": "array[object]"
}