package emailsend

import "camunda-workers/internal/common/validation"

func GetInputSchema() validation.JSONSchema {
	return validation.JSONSchema{
		Type:     "object",
		Required: []string{"to", "subject", "body"},
		Properties: map[string]validation.Property{
			"from": {
				Type:        "string",
				Description: "Sender email address (optional, uses default if not provided)",
				MaxLength:   intPtr(255),
			},
			"to": {
				Type:        "string",
				Description: "Recipient email address",
				MinLength:   intPtr(5),
				MaxLength:   intPtr(255),
			},
			"cc": {
				Type:        "string",
				Description: "CC recipients (comma-separated)",
				MaxLength:   intPtr(1000),
			},
			"bcc": {
				Type:        "string",
				Description: "BCC recipients (comma-separated)",
				MaxLength:   intPtr(1000),
			},
			"replyTo": {
				Type:        "string",
				Description: "Reply-to email address",
				MaxLength:   intPtr(255),
			},
			"subject": {
				Type:        "string",
				Description: "Email subject line",
				MinLength:   intPtr(1),
				MaxLength:   intPtr(500),
			},
			"body": {
				Type:        "string",
				Description: "Email body content",
				MinLength:   intPtr(1),
				MaxLength:   intPtr(100000),
			},
			"isHtml": {
				Type:        "boolean",
				Description: "Whether the email body is HTML",
			},
			"priority": {
				Type:        "string",
				Description: "Email priority (high, normal, low)",
			},
			"attachments": {
				Type:        "array",
				Description: "Email attachments",
			},
			"metadata": {
				Type:        "object",
				Description: "Additional metadata for the email",
			},
		},
		AdditionalProperties: false,
	}
}

func GetOutputSchema() validation.JSONSchema {
	return validation.JSONSchema{
		Type: "object",
		Properties: map[string]validation.Property{
			"success": {
				Type:        "boolean",
				Description: "Whether the email was sent successfully",
			},
			"message": {
				Type:        "string",
				Description: "Result message",
			},
			"messageId": {
				Type:        "string",
				Description: "Unique message identifier",
			},
			"provider": {
				Type:        "string",
				Description: "Email service provider used",
			},
			"sentAt": {
				Type:        "string",
				Description: "Timestamp when email was sent",
			},
		},
		AdditionalProperties: false,
	}
}

func intPtr(i int) *int {
	return &i
}
