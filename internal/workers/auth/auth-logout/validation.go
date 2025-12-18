package authlogout

import "camunda-workers/internal/common/validation"

func GetInputSchema() validation.JSONSchema {
	return validation.JSONSchema{
		Type:     "object",
		Required: []string{"userId"},
		Properties: map[string]validation.Property{
			"userId": {
				Type:        "string",
				Description: "Keycloak user identifier (required for all logout operations)",
				MinLength:   intPtr(3),
				MaxLength:   intPtr(255),
			},
			"refreshToken": {
				Type:        "string",
				Description: "Keycloak refresh token to revoke (required for single session logout)",
				MinLength:   intPtr(10),
				MaxLength:   intPtr(2000),
			},
			"accessToken": {
				Type:        "string",
				Description: "Keycloak access token to add to revocation list (optional)",
				MinLength:   intPtr(10),
				MaxLength:   intPtr(2000),
			},
			"sessionId": {
				Type:        "string",
				Description: "Local session identifier to invalidate (optional)",
				MaxLength:   intPtr(255),
			},
			"deviceId": {
				Type:        "string",
				Description: "Device identifier for audit logging (optional)",
				MaxLength:   intPtr(255),
			},
			"logoutAll": {
				Type:        "boolean",
				Description: "Whether to logout from all sessions (global logout)",
			},
			"reason": {
				Type:        "string",
				Description: "Reason for logout (for audit trail)",
				MaxLength:   intPtr(500),
			},
			"metadata": {
				Type:        "object",
				Description: "Additional metadata for audit logging",
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
				Description: "Result message describing what happened",
			},
			"sessionsInvalidated": {
				Type:        "integer",
				Description: "Number of sessions that were invalidated",
			},
			"tokenRevoked": {
				Type:        "boolean",
				Description: "Whether Keycloak tokens were successfully revoked",
			},
			"logoutAt": {
				Type:        "string",
				Description: "ISO 8601 timestamp of when logout occurred",
			},
		},
		AdditionalProperties: false,
	}
}

func intPtr(i int) *int {
	return &i
}
