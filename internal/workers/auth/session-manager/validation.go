package sessionmanager

import (
	"fmt"
	"time"

	cerrors "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/validation"
)

func GetInputSchema() validation.JSONSchema {
	return validation.JSONSchema{
		Type: "object",
		Properties: map[string]validation.Property{
			"action": {
				Type: "string",
				Enum: []string{"create", "get", "delete"},
			},
			"sessionId": {
				Type: "string",
			},
			"userId": {
				Type: "string",
			},
			"keycloakUserId": {
				Type: "string",
			},
			"idToken": {
				Type: "string",
			},
			"email": {
				Type: "string",
			},
			"expiresIn": {
				Type:    "number",
				Default: float64(86400), // JSON numbers are float64
			},
		},
		Required:             []string{"action"},
		AdditionalProperties: true, // ✅ false → true
	}
}

func ValidateInput(input map[string]interface{}) error {
	// First, apply defaults
	schema := GetInputSchema()

	// Apply default values
	for fieldName, prop := range schema.Properties {
		if prop.Default != nil {
			if _, exists := input[fieldName]; !exists {
				input[fieldName] = prop.Default
			}
		}
	}

	// Validate against schema
	result := validation.ValidateInput(input, schema)
	if !result.Valid {
		// Convert the first error to StandardError
		if len(result.Errors) > 0 {
			err := result.Errors[0]
			return &cerrors.StandardError{
				Code:      cerrors.ErrorCode(err.Code),
				Message:   err.Message,
				Retryable: false,
				Timestamp: time.Now(),
			}
		}
	}

	// Extract values for custom validation
	action, _ := input["action"].(string)

	// Custom validation based on action
	switch action {
	case "create":
		// Check userId
		if userID, ok := input["userId"].(string); !ok || userID == "" {
			return &cerrors.StandardError{
				Code:      cerrors.ErrorCode("MISSING_USER_ID"),
				Message:   "UserID is required for create action",
				Retryable: false,
				Timestamp: time.Now(),
			}
		}

		// Check expiresIn
		if expiresIn, ok := input["expiresIn"].(float64); ok {
			if expiresIn <= 0 {
				return &cerrors.StandardError{
					Code:      cerrors.ErrorCode("INVALID_EXPIRES_IN"),
					Message:   "ExpiresIn must be positive",
					Retryable: false,
					Timestamp: time.Now(),
				}
			}
		}

	case "get", "delete":
		// Check sessionId
		if sessionID, ok := input["sessionId"].(string); !ok || sessionID == "" {
			return &cerrors.StandardError{
				Code:      cerrors.ErrorCode("MISSING_SESSION_ID"),
				Message:   fmt.Sprintf("SessionID is required for %s action", action),
				Retryable: false,
				Timestamp: time.Now(),
			}
		}
	}

	return nil
}
