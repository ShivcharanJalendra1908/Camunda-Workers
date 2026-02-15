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
		Required: []string{"action"},
	}
}

func ValidateInput(inputMap map[string]interface{}) error {
	action, ok := inputMap["action"].(string)
	if !ok || action == "" {
		return &cerrors.StandardError{
			Code:      "INVALID_ACTION",
			Message:   "Action is required and must be a string",
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	if action != "initiate" && action != "callback" {
		return &cerrors.StandardError{
			Code:      "INVALID_ACTION",
			Message:   "Action must be 'initiate' or 'callback'",
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	if action == "callback" {
		code, codeOk := inputMap["code"].(string)
		state, stateOk := inputMap["state"].(string)

		if !codeOk || code == "" {
			return &cerrors.StandardError{
				Code:      "MISSING_CODE",
				Message:   "Authorization code is required for callback action",
				Retryable: false,
				Timestamp: time.Now(),
			}
		}

		if !stateOk || state == "" {
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
