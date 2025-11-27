# Email Send Worker

## Description
Sends email notifications via SMTP server for workflow-driven communications.

## Activity Type
`email.send`

## Owner
notification-team

## Input Schema
```json
{
  "type": "object",
  "required": ["to", "subject", "body"],
  "properties": {
    "to": {
      "type": "string",
      "format": "email",
      "description": "Recipient email address"
    },
    "subject": {
      "type": "string",
      "description": "Email subject line",
      "maxLength": 200
    },
    "body": {
      "type": "string",
      "description": "Email body content (plain text or HTML)"
    },
    "cc": {
      "type": "string",
      "description": "CC recipients (comma-separated)"
    },
    "bcc": {
      "type": "string",
      "description": "BCC recipients (comma-separated)"
    }
  }
}
```

## Output Schema
```json
{
  "type": "object",
  "properties": {
    "sent": {
      "type": "boolean",
      "description": "Email sent successfully"
    },
    "messageId": {
      "type": "string",
      "description": "Unique message identifier"
    }
  }
}
```
## SMTP Configuration

### Supported Providers

#### Gmail
```env
SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_USERNAME=your-email@gmail.com
SMTP_PASSWORD=your-app-password
```

#### SendGrid
```env
SMTP_HOST=smtp.sendgrid.net
SMTP_PORT=587
SMTP_USERNAME=apikey
SMTP_PASSWORD=your-sendgrid-api-key
```

#### AWS SES
```env
SMTP_HOST=email-smtp.us-east-1.amazonaws.com
SMTP_PORT=587
SMTP_USERNAME=your-ses-username
SMTP_PASSWORD=your-ses-password
```

## Process Flow

```
1. Receive email parameters from workflow
2. Validate recipient email format
3. Validate required fields (to, subject, body)
4. Connect to SMTP server
5. Authenticate with credentials
6. Send email
7. Generate message ID
8. Return success status and message ID
```