package captchaverify

import (
	"net"
	"strings"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/validation"
)

// ValidateInput performs comprehensive validation for captcha verification
func ValidateInput(input *Input) error {
	// ===== TEMPLATE SECTION 8: Captcha Token validation =====
	if err := ozzo.Validate(input.CaptchaID,
		ozzo.Required.Error("captchaId is required"),
		ozzo.Length(5, 100).Error("captchaId must be between 5 and 100 characters"),
		validation.IDString,
		validation.SafeSQLString,
		validateCaptchaIDFormat(), // Additional captcha ID format validation
	); err != nil {
		return errors.NewValidationError("captchaId", err.Error())
	}

	// ===== TEMPLATE SECTION 8: Captcha Value validation =====
	if err := ozzo.Validate(input.CaptchaValue,
		ozzo.Required.Error("captchaValue is required"),
		ozzo.Length(4, 8).Error("captchaValue must be between 4 and 8 characters"),
		validation.AlphanumericOnly, // Captcha should be alphanumeric only
		validation.SafeSQLString,
		validation.SafeNoSQLString,
	); err != nil {
		return errors.NewValidationError("captchaValue", err.Error())
	}

	// ===== CLIENT IP VALIDATION =====
	if err := ozzo.Validate(input.ClientIP,
		ozzo.Required.Error("clientIp is required"),
		ozzo.Length(7, 45).Error("clientIp must be between 7 and 45 characters"),
		validation.SafeSQLString,
		validateIPAddress(), // Validate IP address format
	); err != nil {
		return errors.NewValidationError("clientIp", err.Error())
	}

	// ===== USER AGENT VALIDATION =====
	if err := ozzo.Validate(input.UserAgent,
		ozzo.Required.Error("userAgent is required"),
		ozzo.Length(10, 500).Error("userAgent must be between 10 and 500 characters"),
		validation.SafeSQLString,
		validateSafeUserAgent(), // Prevent malicious user agents
	); err != nil {
		return errors.NewValidationError("userAgent", err.Error())
	}

	// ===== SESSION ID VALIDATION =====
	if input.SessionID != "" {
		if err := ozzo.Validate(input.SessionID,
			ozzo.Length(1, 100).Error("sessionId must be between 1 and 100 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("sessionId", err.Error())
		}
	}

	// ===== TEMPLATE SECTION 9: Metadata validation =====
	if input.Metadata != nil {
		if err := validateMetadata(input.Metadata); err != nil {
			return err
		}
	}

	return nil
}

// Sanitize sanitizes all input fields
func (input *Input) Sanitize() {
	sanitizer := validation.NewSanitizer()

	if input.CaptchaID != "" {
		input.CaptchaID = sanitizer.SanitizeString(input.CaptchaID)
	}
	if input.CaptchaValue != "" {
		// Uppercase for case-insensitive comparison (standard for CAPTCHAs)
		input.CaptchaValue = strings.ToUpper(sanitizer.SanitizeString(input.CaptchaValue))
	}
	if input.ClientIP != "" {
		input.ClientIP = sanitizer.SanitizeString(input.ClientIP)
	}
	if input.UserAgent != "" {
		input.UserAgent = sanitizer.SanitizeString(input.UserAgent)
	}
	if input.SessionID != "" {
		input.SessionID = sanitizer.SanitizeString(input.SessionID)
	}
	if input.Metadata != nil {
		input.Metadata = sanitizer.SanitizeInput(input.Metadata)
	}
}

// ===== HELPER VALIDATION FUNCTIONS =====

// validateCaptchaIDFormat validates captcha ID format
func validateCaptchaIDFormat() ozzo.Rule {
	return ozzo.NewStringRule(func(s string) bool {
		// Common captcha ID format: cap_ followed by alphanumeric
		if strings.HasPrefix(s, "cap_") {
			// After prefix, should be alphanumeric
			rest := s[4:]
			for _, char := range rest {
				if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') &&
					!(char >= '0' && char <= '9') && char != '-' && char != '_' {
					return false
				}
			}
			return true
		}
		// Or could be UUID format - FIXED: Properly call Validate
		err := validation.IsUUID.Validate(s)
		return err == nil
	}, "must start with 'cap_' followed by alphanumeric characters or be a valid UUID")
}

// validateIPAddress validates IP address format
func validateIPAddress() ozzo.Rule {
	return ozzo.NewStringRule(func(s string) bool {
		// Check for IPv4 or IPv6
		if ip := net.ParseIP(s); ip != nil {
			return true
		}
		// Check for CIDR notation (optional)
		if _, _, err := net.ParseCIDR(s); err == nil {
			return true
		}
		return false
	}, "must be a valid IPv4 or IPv6 address")
}

// validateSafeUserAgent prevents malicious user agent strings
func validateSafeUserAgent() ozzo.Rule {
	return ozzo.NewStringRule(func(s string) bool {
		dangerousPatterns := []string{
			"<script", "</script>", "javascript:", "onload=", "onerror=",
			"onclick=", "eval(", "alert(", "document.cookie",
			"../", "..\\", "/etc/passwd", "/etc/shadow", "UNION SELECT",
			"INSERT INTO", "DROP TABLE", "DELETE FROM",
		}

		lower := strings.ToLower(s)
		for _, pattern := range dangerousPatterns {
			if strings.Contains(lower, pattern) {
				return false
			}
		}

		// Limit length of individual components
		components := strings.Split(s, " ")
		for _, comp := range components {
			if len(comp) > 100 {
				return false
			}
		}

		return true
	}, "contains potentially unsafe characters")
}

// validateMetadata validates metadata object
func validateMetadata(metadata map[string]interface{}) error {
	if len(metadata) > 10 {
		return errors.NewValidationError("metadata", "cannot exceed 10 items")
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

// GetInputSchema remains the same
func GetInputSchema() validation.JSONSchema {
	return validation.JSONSchema{
		Type:     "object",
		Required: []string{"captchaId", "captchaValue", "clientIp", "userAgent"},
		Properties: map[string]validation.Property{
			"captchaId": {
				Type:        "string",
				Description: "Unique captcha identifier",
				MinLength:   intPtr(5),
				MaxLength:   intPtr(100),
			},
			"captchaValue": {
				Type:        "string",
				Description: "User-entered captcha value",
				MinLength:   intPtr(4),
				MaxLength:   intPtr(8),
			},
			"clientIp": {
				Type:        "string",
				Description: "Client IP address for verification",
				MinLength:   intPtr(7),
				MaxLength:   intPtr(45),
			},
			"userAgent": {
				Type:        "string",
				Description: "Client user agent string",
				MinLength:   intPtr(10),
				MaxLength:   intPtr(500),
			},
			"sessionId": {
				Type:        "string",
				Description: "Session identifier (optional)",
				MaxLength:   intPtr(100),
			},
			"metadata": {
				Type:        "object",
				Description: "Additional metadata for the verification request",
			},
		},
		AdditionalProperties: false,
	}
}

func GetOutputSchema() validation.JSONSchema {
	return validation.JSONSchema{
		Type:     "object",
		Required: []string{"valid", "message"},
		Properties: map[string]validation.Property{
			"valid": {
				Type:        "boolean",
				Description: "Whether the captcha verification was successful",
			},
			"message": {
				Type:        "string",
				Description: "Human-readable verification result message",
			},
			"reason": {
				Type:        "string",
				Description: "Reason code for verification result",
			},
			"attemptsRemaining": {
				Type:        "integer",
				Description: "Number of verification attempts remaining",
			},
		},
		AdditionalProperties: false,
	}
}

func intPtr(i int) *int {
	return &i
}

// package captchaverify

// import "camunda-workers/internal/common/validation"

// func GetInputSchema() validation.JSONSchema {
// 	return validation.JSONSchema{
// 		Type:     "object",
// 		Required: []string{"captchaId", "captchaValue", "clientIp", "userAgent"},
// 		Properties: map[string]validation.Property{
// 			"captchaId": {
// 				Type:        "string",
// 				Description: "Unique captcha identifier",
// 				MinLength:   intPtr(5),
// 				MaxLength:   intPtr(100),
// 			},
// 			"captchaValue": {
// 				Type:        "string",
// 				Description: "User-entered captcha value",
// 				MinLength:   intPtr(4),
// 				MaxLength:   intPtr(8),
// 			},
// 			"clientIp": {
// 				Type:        "string",
// 				Description: "Client IP address for verification",
// 				MinLength:   intPtr(7),
// 				MaxLength:   intPtr(45),
// 			},
// 			"userAgent": {
// 				Type:        "string",
// 				Description: "Client user agent string",
// 				MinLength:   intPtr(10),
// 				MaxLength:   intPtr(500),
// 			},
// 			"sessionId": {
// 				Type:        "string",
// 				Description: "Session identifier (optional)",
// 				MaxLength:   intPtr(100),
// 			},
// 			"metadata": {
// 				Type:        "object",
// 				Description: "Additional metadata for the verification request",
// 			},
// 		},
// 		AdditionalProperties: false,
// 	}
// }

// func GetOutputSchema() validation.JSONSchema {
// 	return validation.JSONSchema{
// 		Type: "object",
// 		Required: []string{"valid", "message"},  // Added
// 		Properties: map[string]validation.Property{
// 			"valid": {
// 				Type:        "boolean",
// 				Description: "Whether the captcha verification was successful",
// 			},
// 			"message": {
// 				Type:        "string",
// 				Description: "Human-readable verification result message",
// 			},
// 			"reason": {
// 				Type:        "string",
// 				Description: "Reason code for verification result",
// 			},
// 			"attemptsRemaining": {
// 				Type:        "integer",
// 				Description: "Number of verification attempts remaining",
// 			},
// 		},
// 		AdditionalProperties: false,
// 	}
// }

// func intPtr(i int) *int {
// 	return &i
// }
