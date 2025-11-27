package captchaverify

import "camunda-workers/internal/common/validation"

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
		Type: "object",
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
