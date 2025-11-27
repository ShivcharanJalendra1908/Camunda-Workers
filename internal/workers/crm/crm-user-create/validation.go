package crmusercreate

import "camunda-workers/internal/common/validation"

func GetInputSchema() validation.JSONSchema {
	return validation.JSONSchema{
		Type:     "object",
		Required: []string{"email", "firstName", "lastName"},
		Properties: map[string]validation.Property{
			"email": {
				Type:        "string",
				Description: "Email address of the user",
				MinLength:   intPtr(5),
				MaxLength:   intPtr(255),
			},
			"firstName": {
				Type:        "string",
				Description: "First name of the user",
				MinLength:   intPtr(1),
				MaxLength:   intPtr(100),
			},
			"lastName": {
				Type:        "string",
				Description: "Last name of the user",
				MinLength:   intPtr(1),
				MaxLength:   intPtr(100),
			},
			"phone": {
				Type:        "string",
				Description: "Phone number",
				MaxLength:   intPtr(50),
			},
			"company": {
				Type:        "string",
				Description: "Company name",
				MaxLength:   intPtr(200),
			},
			"jobTitle": {
				Type:        "string",
				Description: "Job title",
				MaxLength:   intPtr(100),
			},
			"leadSource": {
				Type:        "string",
				Description: "Source of the lead",
				MaxLength:   intPtr(100),
			},
			"tags": {
				Type:        "array",
				Description: "Tags associated with the user",
			},
			"customFields": {
				Type:        "object",
				Description: "Custom fields for the CRM",
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
				Description: "Whether the user was created successfully",
			},
			"message": {
				Type:        "string",
				Description: "Result message",
			},
			"contactId": {
				Type:        "string",
				Description: "CRM contact identifier",
			},
			"accountId": {
				Type:        "string",
				Description: "CRM account identifier",
			},
			"leadId": {
				Type:        "string",
				Description: "CRM lead identifier",
			},
			"crmProvider": {
				Type:        "string",
				Description: "CRM provider used",
			},
			"createdAt": {
				Type:        "string",
				Description: "Timestamp when user was created",
			},
		},
		AdditionalProperties: false,
	}
}

func intPtr(i int) *int {
	return &i
}
