package authsigningoogle

import "camunda-workers/internal/common/validation"

// GetInputSchema returns the JSON schema for input validation
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
				// Removed: Format field (not supported in Property struct)
			},
			"state": {
				Type:        "string",
				Description: "OAuth state parameter for CSRF protection",
				MaxLength:   intPtr(500),
			},
			"metadata": {
				Type:        "object",
				Description: "Additional metadata for the authentication request",
				// Removed: AdditionalProperties field (not supported in Property struct)
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
				// Removed: Format field (not supported in Property struct)
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
			"isNewUser": {
				Type:        "boolean",
				Description: "Whether this is a newly created user",
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
