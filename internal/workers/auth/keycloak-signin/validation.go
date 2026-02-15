package keycloaksignin

import (
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
				Enum: []string{"initiate", "callback"},
			},
			"code": {
				Type: "string",
			},
			"state": {
				Type: "string",
			},
			"provider": {
				Type:    "string",
				Default: "keycloak",
			},
		},
		Required:             []string{"action"},
		AdditionalProperties: false,
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

	// Additional custom validation for callback action
	if action, ok := input["action"].(string); ok && action == "callback" {
		if code, ok := input["code"].(string); !ok || code == "" {
			return &cerrors.StandardError{
				Code:      "MISSING_CODE",
				Message:   "Authorization code is required for callback action",
				Retryable: false,
				Timestamp: time.Now(),
			}
		}
		if state, ok := input["state"].(string); !ok || state == "" {
			return &cerrors.StandardError{
				Code:      "MISSING_STATE",
				Message:   "State parameter is required for callback action",
				Retryable: false,
				Timestamp: time.Now(),
			}
		}
	}

	return nil
}
