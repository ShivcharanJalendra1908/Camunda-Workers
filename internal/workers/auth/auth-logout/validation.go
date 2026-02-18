package authlogout

import (
	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/validation"
)

// ValidateInput performs comprehensive validation for auth-logout
func ValidateInput(input *Input) error {
	// ===== TEMPLATE SECTION 4: User ID (UUID validation) =====
	if err := ozzo.Validate(input.UserID,
		ozzo.Required.Error("userId is required"),
		ozzo.Length(36, 36).Error("userId must be exactly 36 characters"),
		validation.IsUUID,
		validation.SafeSQLString,
	); err != nil {
		return errors.NewInvalidUUIDError("userId", input.UserID)
	}

	// ===== TEMPLATE SECTION 3: Refresh Token (Session Token validation) =====
	if input.RefreshToken != "" {
		if err := ozzo.Validate(input.RefreshToken,
			ozzo.Length(10, 2000).Error("refreshToken must be between 10 and 2000 characters"),
			validation.SafeSQLString,
			validation.SafeNoSQLString,
		); err != nil {
			return errors.NewValidationError("refreshToken", err.Error())
		}
	}

	// ===== TEMPLATE SECTION 3: Access Token (Session Token validation) =====
	if input.AccessToken != "" {
		if err := ozzo.Validate(input.AccessToken,
			ozzo.Length(10, 2000).Error("accessToken must be between 10 and 2000 characters"),
			validation.SafeSQLString,
			validation.SafeNoSQLString,
		); err != nil {
			return errors.NewValidationError("accessToken", err.Error())
		}
	}

	// ===== TEMPLATE SECTION 3: Session ID (Session Token validation) =====
	if input.SessionID != "" {
		if err := ozzo.Validate(input.SessionID,
			ozzo.Length(1, 255).Error("sessionId must be between 1 and 255 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("sessionId", err.Error())
		}
	}

	// ===== ADDITIONAL: Device ID validation =====
	if input.DeviceID != "" {
		if err := ozzo.Validate(input.DeviceID,
			ozzo.Length(1, 255).Error("deviceId must be between 1 and 255 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("deviceId", err.Error())
		}
	}

	// ===== ADDITIONAL: Reason validation =====
	if input.Reason != "" {
		if err := ozzo.Validate(input.Reason,
			ozzo.Length(1, 500).Error("reason must be between 1 and 500 characters"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("reason", err.Error())
		}
	}

	// ===== TEMPLATE SECTION 9: Metadata validation =====
	if input.Metadata != nil {
		if err := validateMetadata(input.Metadata); err != nil {
			return err
		}
	}

	// ===== BUSINESS RULE: Either refreshToken or sessionID for single logout =====
	if !input.LogoutAll && input.RefreshToken == "" && input.SessionID == "" {
		return errors.NewValidationError("logout", "for single session logout, either refreshToken or sessionId is required")
	}

	return nil
}

// Sanitize sanitizes all input fields
func (input *Input) Sanitize() {
	sanitizer := validation.NewSanitizer()

	if input.UserID != "" {
		input.UserID = sanitizer.SanitizeString(input.UserID)
	}
	if input.RefreshToken != "" {
		input.RefreshToken = sanitizer.SanitizeString(input.RefreshToken)
	}
	if input.AccessToken != "" {
		input.AccessToken = sanitizer.SanitizeString(input.AccessToken)
	}
	if input.SessionID != "" {
		input.SessionID = sanitizer.SanitizeString(input.SessionID)
	}
	if input.DeviceID != "" {
		input.DeviceID = sanitizer.SanitizeString(input.DeviceID)
	}
	if input.Reason != "" {
		input.Reason = sanitizer.SanitizeString(input.Reason)
	}
	if input.Metadata != nil {
		input.Metadata = sanitizer.SanitizeInput(input.Metadata)
	}
}

// ===== HELPER: Metadata Validation =====
func validateMetadata(metadata map[string]interface{}) error {
	if len(metadata) > 20 {
		return errors.NewValidationError("metadata", "cannot exceed 20 items")
	}

	for key, value := range metadata {
		if err := ozzo.Validate(key,
			ozzo.Length(1, 100).Error("metadata key must be between 1 and 100 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("metadata."+key, "invalid key: "+err.Error())
		}

		switch v := value.(type) {
		case string:
			if len(v) > 500 {
				return errors.NewValidationError("metadata."+key, "string value cannot exceed 500 characters")
			}
		case []interface{}:
			if len(v) > 50 {
				return errors.NewValidationError("metadata."+key, "array cannot exceed 50 items")
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
		AdditionalProperties: true,
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
