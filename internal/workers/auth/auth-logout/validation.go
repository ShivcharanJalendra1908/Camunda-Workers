package authlogout

import "camunda-workers/internal/common/validation"

func GetInputSchema() validation.JSONSchema {
	return validation.JSONSchema{
		Type:     "object",
		Required: []string{"userId", "token"},
		Properties: map[string]validation.Property{
			"userId": {
				Type:        "string",
				Description: "User identifier",
				MinLength:   intPtr(3),
				MaxLength:   intPtr(255),
			},
			"token": {
				Type:        "string",
				Description: "Authentication token to revoke",
				MinLength:   intPtr(10),
				MaxLength:   intPtr(2000),
			},
			"sessionId": {
				Type:        "string",
				Description: "Session identifier to invalidate",
				MaxLength:   intPtr(255),
			},
			"deviceId": {
				Type:        "string",
				Description: "Device identifier",
				MaxLength:   intPtr(255),
			},
			"logoutAll": {
				Type:        "boolean",
				Description: "Whether to logout from all sessions",
			},
			"reason": {
				Type:        "string",
				Description: "Reason for logout",
				MaxLength:   intPtr(500),
			},
			"metadata": {
				Type:        "object",
				Description: "Additional metadata",
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
				Description: "Whether logout was successful",
			},
			"message": {
				Type:        "string",
				Description: "Result message",
			},
			"sessionsInvalidated": {
				Type:        "integer",
				Description: "Number of sessions invalidated",
			},
			"tokenRevoked": {
				Type:        "boolean",
				Description: "Whether token was revoked",
			},
			"logoutAt": {
				Type:        "string",
				Description: "Timestamp of logout",
			},
		},
		AdditionalProperties: false,
	}
}

func intPtr(i int) *int {
	return &i
}
