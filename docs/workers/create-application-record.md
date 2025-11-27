
---

## 📁 **12. `docs/workers/create-application-record.md`**

```markdown
# Create Application Record Worker

## Purpose
Creates franchise application record in database.

## Task Type
`create-application-record`

## Input Schema
```json
{
  "seekerId": "string",
  "franchiseId": "string",
  "applicationData": "object",
  "readinessScore": "integer",
  "priority": "string"
}

## Output Schema
```json
{
  "applicationId": "string (UUID)",
  "applicationStatus": "string (submitted)",
  "createdAt": "string (ISO 8601)"
}