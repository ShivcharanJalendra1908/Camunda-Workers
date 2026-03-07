package emailsend

import (
	"fmt"
	"net/mail"
	"strings"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/validation"
)

// ValidateInput performs comprehensive validation for email sending
func ValidateInput(input *Input) error {
	// ===== TEMPLATE SECTION 5: Email Validation =====
	// Validate 'to' email address
	if err := ozzo.Validate(input.To,
		ozzo.Required.Error("to email address is required"),
		ozzo.Length(5, 255).Error("to email must be between 5 and 255 characters"),
		validateEmailFormat(),
		validation.SafeSQLString,
	); err != nil {
		return errors.NewValidationError("to", err.Error())
	}

	// ===== FROM EMAIL VALIDATION =====
	if input.From != "" {
		if err := ozzo.Validate(input.From,
			ozzo.Length(5, 255).Error("from email must be between 5 and 255 characters"),
			validateEmailFormat(),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("from", err.Error())
		}
	}

	// ===== CC EMAIL VALIDATION =====
	if input.CC != "" {
		if err := validateEmailList(input.CC, "cc"); err != nil {
			return err
		}
	}

	// ===== BCC EMAIL VALIDATION =====
	if input.BCC != "" {
		if err := validateEmailList(input.BCC, "bcc"); err != nil {
			return err
		}
	}

	// ===== REPLY-TO EMAIL VALIDATION =====
	if input.ReplyTo != "" {
		if err := ozzo.Validate(input.ReplyTo,
			ozzo.Length(5, 255).Error("replyTo email must be between 5 and 255 characters"),
			validateEmailFormat(),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("replyTo", err.Error())
		}
	}

	// ===== SUBJECT VALIDATION =====
	if err := ozzo.Validate(input.Subject,
		ozzo.Required.Error("subject is required"),
		ozzo.Length(1, 500).Error("subject must be between 1 and 500 characters"),
		validation.SafeSQLString,
		validateSafeText(), // Prevent injection in subject
	); err != nil {
		return errors.NewValidationError("subject", err.Error())
	}

	// ===== BODY VALIDATION =====
	if err := ozzo.Validate(input.Body,
		ozzo.Required.Error("body is required"),
		ozzo.Length(1, 100000).Error("body must be between 1 and 100,000 characters"),
		// Note: Can't use SafeSQLString here since email body may contain HTML/formatting
		// But we should validate for script injection
		validateEmailBody(input.IsHTML),
	); err != nil {
		return errors.NewValidationError("body", err.Error())
	}

	// ===== PRIORITY VALIDATION =====
	if input.Priority != "" {
		if err := ozzo.Validate(input.Priority,
			validation.ValidateEnum([]string{"high", "normal", "low", "urgent"}),
		); err != nil {
			return errors.NewValidationError("priority", err.Error())
		}
	}

	// ===== ATTACHMENTS VALIDATION =====
	if len(input.Attachments) > 0 {
		if err := validateAttachments(input.Attachments); err != nil {
			return err
		}
	}

	// ===== TEMPLATE SECTION 9: Metadata validation =====
	if input.Metadata != nil {
		if err := validateMetadata(input.Metadata); err != nil {
			return err
		}
	}

	// ===== BUSINESS RULE: Total recipients limit =====
	totalRecipients := countRecipients(input)
	if totalRecipients > 100 {
		return errors.NewValidationError("recipients",
			fmt.Sprintf("total recipients cannot exceed 100, got %d", totalRecipients))
	}

	return nil
}

// Sanitize sanitizes all input fields
func (input *Input) Sanitize() {
	sanitizer := validation.NewSanitizer()

	if input.From != "" {
		input.From = sanitizer.SanitizeString(input.From)
	}
	if input.To != "" {
		input.To = sanitizer.SanitizeString(input.To)
	}
	if input.CC != "" {
		input.CC = sanitizer.SanitizeString(input.CC)
	}
	if input.BCC != "" {
		input.BCC = sanitizer.SanitizeString(input.BCC)
	}
	if input.ReplyTo != "" {
		input.ReplyTo = sanitizer.SanitizeString(input.ReplyTo)
	}
	if input.Subject != "" {
		input.Subject = sanitizer.SanitizeString(input.Subject)
	}
	if input.Body != "" {
		// Don't fully sanitize body as it may contain HTML
		// Just remove null bytes and control chars
		input.Body = strings.ReplaceAll(input.Body, "\x00", "")
	}
	if input.Priority != "" {
		input.Priority = sanitizer.SanitizeString(input.Priority)
	}
	// Attachments are base64 encoded, sanitization happens during validation
	if input.Metadata != nil {
		input.Metadata = sanitizer.SanitizeInput(input.Metadata)
	}
}

// ===== HELPER VALIDATION FUNCTIONS =====

// validateEmailFormat validates email address format
func validateEmailFormat() ozzo.Rule {
	return ozzo.NewStringRule(func(s string) bool {
		// Use Go's standard library for email validation
		_, err := mail.ParseAddress(s)
		return err == nil
	}, "must be a valid email address")
}

// validateEmailList validates comma-separated email list
func validateEmailList(emailList, fieldName string) error {
	emails := strings.Split(emailList, ",")
	for i, email := range emails {
		email = strings.TrimSpace(email)
		if email == "" {
			continue
		}
		if err := ozzo.Validate(email,
			ozzo.Length(5, 255).Error("email must be between 5 and 255 characters"),
			validateEmailFormat(),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError(fmt.Sprintf("%s[%d]", fieldName, i), err.Error())
		}
	}
	return nil
}

// validateSafeText prevents script injection in text fields
func validateSafeText() ozzo.Rule {
	return ozzo.NewStringRule(func(s string) bool {
		dangerousPatterns := []string{
			"<script", "</script>", "javascript:", "onload=", "onerror=",
			"onclick=", "eval(", "alert(", "document.cookie",
		}

		lower := strings.ToLower(s)
		for _, pattern := range dangerousPatterns {
			if strings.Contains(lower, pattern) {
				return false
			}
		}
		return true
	}, "contains potentially unsafe content")
}

// validateEmailBody validates email body content
func validateEmailBody(isHTML bool) ozzo.Rule {
	return ozzo.NewStringRule(func(s string) bool {
		// Remove null bytes
		s = strings.ReplaceAll(s, "\x00", "")

		if isHTML {
			// For HTML emails, check for dangerous tags/attributes
			dangerousTags := []string{
				"<script", "<iframe", "<object", "<embed", "<applet",
				"onload=", "onerror=", "onclick=", "onmouseover=",
				"javascript:", "vbscript:", "data:",
			}

			lower := strings.ToLower(s)
			for _, pattern := range dangerousTags {
				if strings.Contains(lower, pattern) {
					return false
				}
			}
		} else {
			// For plain text, just check for basic injection
			if strings.Contains(s, "<script") || strings.Contains(strings.ToLower(s), "javascript:") {
				return false
			}
		}

		// Check for potential email header injection
		if strings.Contains(s, "\r\n") || strings.Contains(s, "\n") {
			lines := strings.Split(s, "\n")
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "To:") || strings.HasPrefix(trimmed, "From:") ||
					strings.HasPrefix(trimmed, "Subject:") || strings.HasPrefix(trimmed, "CC:") ||
					strings.HasPrefix(trimmed, "BCC:") {
					return false
				}
			}
		}

		return true
	}, "contains potentially unsafe content")
}

// validateAttachments validates email attachments
func validateAttachments(attachments []Attachment) error {
	// Limit number of attachments
	if len(attachments) > 10 {
		return errors.NewValidationError("attachments", "cannot exceed 10 attachments")
	}

	totalSize := 0
	for i, att := range attachments {
		// Validate filename
		if err := ozzo.Validate(att.Filename,
			ozzo.Required.Error("filename is required"),
			ozzo.Length(1, 255).Error("filename must be between 1 and 255 characters"),
			validation.SafeSQLString,
			validateSafeFilename(),
		); err != nil {
			return errors.NewValidationError(fmt.Sprintf("attachments[%d].filename", i), err.Error())
		}

		// Validate content type (optional)
		if att.ContentType != "" {
			if err := ozzo.Validate(att.ContentType,
				ozzo.Length(1, 100).Error("contentType must be between 1 and 100 characters"),
				validation.SafeSQLString,
			); err != nil {
				return errors.NewValidationError(fmt.Sprintf("attachments[%d].contentType", i), err.Error())
			}
		}

		// Validate content (base64 encoded)
		if err := ozzo.Validate(att.Content,
			ozzo.Required.Error("content is required"),
			validateBase64(),
		); err != nil {
			return errors.NewValidationError(fmt.Sprintf("attachments[%d].content", i), err.Error())
		}

		// Check attachment size (approximate)
		contentSize := len(att.Content) * 3 / 4 // Approx base64 decoded size
		if contentSize > 10*1024*1024 {         // 10MB per attachment
			return errors.NewValidationError(fmt.Sprintf("attachments[%d]", i),
				"attachment size cannot exceed 10MB")
		}

		totalSize += contentSize
		if totalSize > 25*1024*1024 { // 25MB total
			return errors.NewValidationError("attachments",
				"total attachments size cannot exceed 25MB")
		}
	}

	return nil
}

// validateSafeFilename prevents dangerous filenames
func validateSafeFilename() ozzo.Rule {
	return ozzo.NewStringRule(func(s string) bool {
		// Block dangerous file extensions
		dangerousExts := []string{
			".exe", ".bat", ".cmd", ".sh", ".bash", ".ps1",
			".vbs", ".js", ".jar", ".class", ".php", ".py",
			".pl", ".rb", ".asp", ".aspx", ".jsp",
		}

		lower := strings.ToLower(s)
		for _, ext := range dangerousExts {
			if strings.HasSuffix(lower, ext) {
				return false
			}
		}

		// Block path traversal attempts
		if strings.Contains(s, "..") || strings.Contains(s, "/") ||
			strings.Contains(s, "\\") || strings.Contains(s, ":") {
			return false
		}

		// Block control characters
		for _, char := range s {
			if char < 32 || char == 127 { // Control chars
				return false
			}
		}

		return true
	}, "filename contains potentially unsafe characters or extension")
}

// validateBase64 validates base64 encoded content
func validateBase64() ozzo.Rule {
	return ozzo.NewStringRule(func(s string) bool {
		// Check length is multiple of 4
		if len(s)%4 != 0 {
			return false
		}

		// Check for valid base64 characters
		validChars := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="
		for _, char := range s {
			if !strings.ContainsRune(validChars, char) {
				return false
			}
		}

		return true
	}, "must be valid base64 encoded content")
}

// validateMetadata validates metadata object
func validateMetadata(metadata map[string]interface{}) error {
	if len(metadata) > 20 {
		return errors.NewValidationError("metadata", "cannot exceed 20 items")
	}

	for key, value := range metadata {
		if err := ozzo.Validate(key,
			ozzo.Length(1, 50).Error("metadata key must be between 1 and 50 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("metadata."+key, "invalid key: "+err.Error())
		}

		switch v := value.(type) {
		case string:
			if len(v) > 200 {
				return errors.NewValidationError("metadata."+key, "string value cannot exceed 200 characters")
			}
		case []interface{}:
			if len(v) > 20 {
				return errors.NewValidationError("metadata."+key, "array cannot exceed 20 items")
			}
		case map[string]interface{}:
			return errors.NewValidationError("metadata."+key, "nested objects are not allowed")
		}
	}

	return nil
}

// Helper function to count total recipients
func countRecipients(input *Input) int {
	count := 1 // 'to' field

	if input.CC != "" {
		count += len(strings.Split(input.CC, ","))
	}

	if input.BCC != "" {
		count += len(strings.Split(input.BCC, ","))
	}

	return count
}

// GetInputSchema remains the same
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