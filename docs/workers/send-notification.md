
---

## 📁 **13. `docs/workers/send-notification.md`**

```markdown
# Send Notification Worker

## Purpose
Sends notifications via AWS SES (email) and SNS (SMS).

## Task Type
`send-notification`

## Input Schema
```json
{
  "recipientId": "string",
  "recipientType": "string (franchisor|seeker)",
  "notificationType": "string",
  "applicationId": "string (optional)",
  "priority": "string (optional)",
  "metadata": "object (optional)"
}

## Output Schema
```json
{
  "notificationId": "string",
  "status": "string (sent|failed|disabled)",
  "sentAt": "string (ISO 8601)"
}