package authsignuplinkedin

import "camunda-workers/internal/common/validation"

func GetInputSchema() validation.JSONSchema {
	return validation.JSONSchema{
		Type:     "object",
		Required: []string{"authCode", "email"},
		Properties: map[string]validation.Property{
			"authCode": {
				Type:        "string",
				Description: "LinkedIn OAuth authorization code obtained from the frontend",
				MinLength:   intPtr(10),
				MaxLength:   intPtr(1000),
			},
			"email": {
				Type:        "string",
				Description: "User's email address for signup",
				MinLength:   intPtr(5),
				MaxLength:   intPtr(255),
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
			"firstName": {
				Type:        "string",
				Description: "User's first name (optional, will use LinkedIn profile if not provided)",
				MaxLength:   intPtr(100),
			},
			"lastName": {
				Type:        "string",
				Description: "User's last name (optional, will use LinkedIn profile if not provided)",
				MaxLength:   intPtr(100),
			},
			"metadata": {
				Type:        "object",
				Description: "Additional metadata for the signup request",
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
				Description: "Whether the LinkedIn signup was successful",
			},
			"userId": {
				Type:        "string",
				Description: "Unique identifier for the newly created user",
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
			"emailVerified": {
				Type:        "boolean",
				Description: "Whether the email is verified",
			},
			"passwordSet": {
				Type:        "boolean",
				Description: "Whether a password has been set (always false for OAuth signup)",
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
