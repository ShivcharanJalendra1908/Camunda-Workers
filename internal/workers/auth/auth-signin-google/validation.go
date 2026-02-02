package authsigningoogle

import (
	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/validation"
)

// ValidateInput performs comprehensive validation for auth-signin-google
func ValidateInput(input *Input) error {
	// ===== TEMPLATE SECTION 1: OAuth Code (for signin workers) =====
	if err := ozzo.Validate(input.AuthCode,
		ozzo.Required.Error("authCode is required"),
		ozzo.Length(10, 1000).Error("authCode must be between 10 and 1000 characters"),
		validation.SafeSQLString,
		validation.SafeNoSQLString,
	); err != nil {
		return errors.NewValidationError("authCode", err.Error())
	}

	// ===== TEMPLATE SECTION 2: State/CSRF Token (for signin workers) =====
	if input.State != "" {
		if err := ozzo.Validate(input.State,
			ozzo.Length(10, 500).Error("state must be between 10 and 500 characters"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("state", err.Error())
		}
	}

	// ===== TEMPLATE SECTION 7: Redirect URI =====
	if input.RedirectURI != "" {
		if err := ozzo.Validate(input.RedirectURI,
			ozzo.Length(10, 500).Error("redirectUri must be between 10 and 500 characters"),
		); err != nil {
			return errors.NewValidationError("redirectUri", err.Error())
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

	if input.AuthCode != "" {
		input.AuthCode = sanitizer.SanitizeString(input.AuthCode)
	}
	if input.RedirectURI != "" {
		input.RedirectURI = sanitizer.SanitizeString(input.RedirectURI)
	}
	if input.State != "" {
		input.State = sanitizer.SanitizeString(input.State)
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
		Required: []string{"authCode"},
		Properties: map[string]validation.Property{
			"authCode": {
				Type:        "string",
				Description: "Google OAuth authorization code obtained from the frontend",
				MinLength:   intPtr(10),
				MaxLength:   intPtr(1000),
			},
			"redirectUri": {
				Type:        "string",
				Description: "Redirect URI used in the OAuth flow",
				MaxLength:   intPtr(500),
			},
			"state": {
				Type:        "string",
				Description: "OAuth state parameter for CSRF protection",
				MaxLength:   intPtr(500),
			},
			"metadata": {
				Type:        "object",
				Description: "Additional metadata for the authentication request",
			},
		},
		AdditionalProperties: false,
	}
}

// GetOutputSchema returns the JSON schema for output validation
func GetOutputSchema() validation.JSONSchema {
	return validation.JSONSchema{
		Type: "object",
		Properties: map[string]validation.Property{
			"success": {
				Type:        "boolean",
				Description: "Whether the Google signin was successful",
			},
			"userId": {
				Type:        "string",
				Description: "Unique identifier for the user in our system",
			},
			"email": {
				Type:        "string",
				Description: "User's email address",
			},
			"firstName": {
				Type:        "string",
				Description: "User's first name",
				MaxLength:   intPtr(100),
			},
			"lastName": {
				Type:        "string",
				Description: "User's last name",
				MaxLength:   intPtr(100),
			},
			"token": {
				Type:        "string",
				Description: "Authentication token for the user session",
			},
			"accessToken": {
				Type:        "string",
				Description: "Access token for the user session",
			},
			"refreshToken": {
				Type:        "string",
				Description: "Refresh token for the user session",
			},
			"expiresIn": {
				Type:        "integer",
				Description: "Token expiration in seconds",
			},
			"tokenType": {
				Type:        "string",
				Description: "Type of token (e.g., 'Bearer')",
			},
			"isNewUser": {
				Type:        "boolean",
				Description: "Whether this is a newly created user",
			},
			"emailVerified": {
				Type:        "boolean",
				Description: "Whether the user's email is verified",
			},
			"crmContactId": {
				Type:        "string",
				Description: "CRM contact ID if created",
			},
		},
		AdditionalProperties: false,
	}
}

func intPtr(i int) *int {
	return &i
}

// package authsigningoogle

// import "camunda-workers/internal/common/validation"

// // GetInputSchema returns the JSON schema for input validation
// func GetInputSchema() validation.JSONSchema {
// 	return validation.JSONSchema{
// 		Type:     "object",
// 		Required: []string{"authCode"},
// 		Properties: map[string]validation.Property{
// 			"authCode": {
// 				Type:        "string",
// 				Description: "Google OAuth authorization code obtained from the frontend",
// 				MinLength:   intPtr(10),
// 				MaxLength:   intPtr(1000),
// 			},
// 			"redirectUri": {
// 				Type:        "string",
// 				Description: "Redirect URI used in the OAuth flow",
// 				MaxLength:   intPtr(500),
// 			},
// 			"state": {
// 				Type:        "string",
// 				Description: "OAuth state parameter for CSRF protection",
// 				MaxLength:   intPtr(500),
// 			},
// 			"metadata": {
// 				Type:        "object",
// 				Description: "Additional metadata for the authentication request",
// 			},
// 		},
// 		AdditionalProperties: false,
// 	}
// }

// // GetOutputSchema returns the JSON schema for output validation
// func GetOutputSchema() validation.JSONSchema {
// 	return validation.JSONSchema{
// 		Type: "object",
// 		Properties: map[string]validation.Property{
// 			"success": {
// 				Type:        "boolean",
// 				Description: "Whether the Google signin was successful",
// 			},
// 			"userId": {
// 				Type:        "string",
// 				Description: "Unique identifier for the user in our system",
// 			},
// 			"email": {
// 				Type:        "string",
// 				Description: "User's email address",
// 			},
// 			"firstName": {
// 				Type:        "string",
// 				Description: "User's first name",
// 				MaxLength:   intPtr(100),
// 			},
// 			"lastName": {
// 				Type:        "string",
// 				Description: "User's last name",
// 				MaxLength:   intPtr(100),
// 			},
// 			"token": {
// 				Type:        "string",
// 				Description: "Authentication token for the user session",
// 			},
// 			"accessToken": {
// 				Type:        "string",
// 				Description: "Access token for the user session",
// 			},
// 			"refreshToken": {
// 				Type:        "string",
// 				Description: "Refresh token for the user session",
// 			},
// 			"expiresIn": {
// 				Type:        "integer",
// 				Description: "Token expiration in seconds",
// 			},
// 			"tokenType": {
// 				Type:        "string",
// 				Description: "Type of token (e.g., 'Bearer')",
// 			},
// 			"isNewUser": {
// 				Type:        "boolean",
// 				Description: "Whether this is a newly created user",
// 			},
// 			"emailVerified": {
// 				Type:        "boolean",
// 				Description: "Whether the user's email is verified",
// 			},
// 			"crmContactId": {
// 				Type:        "string",
// 				Description: "CRM contact ID if created",
// 			},
// 		},
// 		AdditionalProperties: false,
// 	}
// }

// func intPtr(i int) *int {
// 	return &i
// }
