package crmusercreate

import (
	"fmt"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/validation"
)

// ValidateInput performs comprehensive validation for crm-user-create
func ValidateInput(input *Input) error {
	// ===== TEMPLATE SECTION 5: Email Validation =====
	if err := ozzo.Validate(input.Email,
		ozzo.Required.Error("email is required"),
		ozzo.Length(5, 255).Error("email must be between 5 and 255 characters"),
		validation.ValidateEmail(),
		validation.SafeSQLString,
	); err != nil {
		return errors.NewValidationError("email", err.Error())
	}

	// ===== TEMPLATE SECTION: First Name Validation =====
	if err := ozzo.Validate(input.FirstName,
		ozzo.Required.Error("firstName is required"),
		ozzo.Length(1, 100).Error("firstName must be between 1 and 100 characters"),
		validation.SafeSQLString,
	); err != nil {
		return errors.NewValidationError("firstName", err.Error())
	}

	// ===== TEMPLATE SECTION: Last Name Validation =====
	if err := ozzo.Validate(input.LastName,
		ozzo.Required.Error("lastName is required"),
		ozzo.Length(1, 100).Error("lastName must be between 1 and 100 characters"),
		validation.SafeSQLString,
	); err != nil {
		return errors.NewValidationError("lastName", err.Error())
	}

	// ===== ADDITIONAL: Phone Validation =====
	if input.Phone != "" {
		if err := ozzo.Validate(input.Phone,
			ozzo.Length(1, 50).Error("phone must be between 1 and 50 characters"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("phone", err.Error())
		}
	}

	// ===== ADDITIONAL: Company Validation =====
	if input.Company != "" {
		if err := ozzo.Validate(input.Company,
			ozzo.Length(1, 200).Error("company must be between 1 and 200 characters"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("company", err.Error())
		}
	}

	// ===== ADDITIONAL: Job Title Validation =====
	if input.JobTitle != "" {
		if err := ozzo.Validate(input.JobTitle,
			ozzo.Length(1, 100).Error("jobTitle must be between 1 and 100 characters"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("jobTitle", err.Error())
		}
	}

	// ===== ADDITIONAL: Lead Source Validation =====
	if input.LeadSource != "" {
		if err := ozzo.Validate(input.LeadSource,
			ozzo.Length(1, 100).Error("leadSource must be between 1 and 100 characters"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("leadSource", err.Error())
		}
	}

	// ===== TEMPLATE SECTION 9: Tags Validation =====
	if input.Tags != nil {
		if err := validateTags(input.Tags); err != nil {
			return err
		}
	}

	// ===== TEMPLATE SECTION 9: Custom Fields Validation =====
	if input.CustomFields != nil {
		if err := validateCustomFields(input.CustomFields); err != nil {
			return err
		}
	}

	// ===== TEMPLATE SECTION 9: Metadata Validation =====
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

	if input.Email != "" {
		input.Email = sanitizer.SanitizeString(input.Email)
	}
	if input.FirstName != "" {
		input.FirstName = sanitizer.SanitizeString(input.FirstName)
	}
	if input.LastName != "" {
		input.LastName = sanitizer.SanitizeString(input.LastName)
	}
	if input.Phone != "" {
		input.Phone = sanitizer.SanitizeString(input.Phone)
	}
	if input.Company != "" {
		input.Company = sanitizer.SanitizeString(input.Company)
	}
	if input.JobTitle != "" {
		input.JobTitle = sanitizer.SanitizeString(input.JobTitle)
	}
	if input.LeadSource != "" {
		input.LeadSource = sanitizer.SanitizeString(input.LeadSource)
	}
	if input.Tags != nil {
		sanitizedTags := make([]string, len(input.Tags))
		for i, tag := range input.Tags {
			sanitizedTags[i] = sanitizer.SanitizeString(tag)
		}
		input.Tags = sanitizedTags
	}
	if input.CustomFields != nil {
		input.CustomFields = sanitizer.SanitizeInput(input.CustomFields)
	}
	if input.Metadata != nil {
		input.Metadata = sanitizer.SanitizeInput(input.Metadata)
	}
}

// ===== HELPER: Tags Validation =====
func validateTags(tags []string) error {
	if len(tags) > 20 {
		return errors.NewValidationError("tags", "cannot exceed 20 tags")
	}

	for i, tag := range tags {
		if err := ozzo.Validate(tag,
			ozzo.Length(1, 50).Error("tag must be between 1 and 50 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError(fmt.Sprintf("tags[%d]", i), err.Error())
		}
	}

	return nil
}

// ===== HELPER: Custom Fields Validation =====
func validateCustomFields(fields map[string]interface{}) error {
	if len(fields) > 10 {
		return errors.NewValidationError("customFields", "cannot exceed 10 fields")
	}

	for key, value := range fields {
		if err := ozzo.Validate(key,
			ozzo.Length(1, 50).Error("custom field key must be between 1 and 50 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("customFields."+key, "invalid key: "+err.Error())
		}

		switch v := value.(type) {
		case string:
			if len(v) > 500 {
				return errors.NewValidationError("customFields."+key, "string value cannot exceed 500 characters")
			}
		case []interface{}:
			if len(v) > 50 {
				return errors.NewValidationError("customFields."+key, "array cannot exceed 50 items")
			}
		case map[string]interface{}:
			return errors.NewValidationError("customFields."+key, "nested objects are not allowed")
		}
	}

	return nil
}

// ===== HELPER: Metadata Validation =====
func validateMetadata(metadata map[string]interface{}) error {
	if len(metadata) > 10 {
		return errors.NewValidationError("metadata", "cannot exceed 10 items")
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

// GetOutputSchema returns the JSON schema for output validation
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

// package crmusercreate

// import "camunda-workers/internal/common/validation"

// func GetInputSchema() validation.JSONSchema {
// 	return validation.JSONSchema{
// 		Type:     "object",
// 		Required: []string{"email", "firstName", "lastName"},
// 		Properties: map[string]validation.Property{
// 			"email": {
// 				Type:        "string",
// 				Description: "Email address of the user",
// 				MinLength:   intPtr(5),
// 				MaxLength:   intPtr(255),
// 			},
// 			"firstName": {
// 				Type:        "string",
// 				Description: "First name of the user",
// 				MinLength:   intPtr(1),
// 				MaxLength:   intPtr(100),
// 			},
// 			"lastName": {
// 				Type:        "string",
// 				Description: "Last name of the user",
// 				MinLength:   intPtr(1),
// 				MaxLength:   intPtr(100),
// 			},
// 			"phone": {
// 				Type:        "string",
// 				Description: "Phone number",
// 				MaxLength:   intPtr(50),
// 			},
// 			"company": {
// 				Type:        "string",
// 				Description: "Company name",
// 				MaxLength:   intPtr(200),
// 			},
// 			"jobTitle": {
// 				Type:        "string",
// 				Description: "Job title",
// 				MaxLength:   intPtr(100),
// 			},
// 			"leadSource": {
// 				Type:        "string",
// 				Description: "Source of the lead",
// 				MaxLength:   intPtr(100),
// 			},
// 			"tags": {
// 				Type:        "array",
// 				Description: "Tags associated with the user",
// 			},
// 			"customFields": {
// 				Type:        "object",
// 				Description: "Custom fields for the CRM",
// 			},
// 			"metadata": {
// 				Type:        "object",
// 				Description: "Additional metadata",
// 			},
// 		},
// 		AdditionalProperties: false,
// 	}
// }

// func GetOutputSchema() validation.JSONSchema {
// 	return validation.JSONSchema{
// 		Type: "object",
// 		Properties: map[string]validation.Property{
// 			"success": {
// 				Type:        "boolean",
// 				Description: "Whether the user was created successfully",
// 			},
// 			"message": {
// 				Type:        "string",
// 				Description: "Result message",
// 			},
// 			"contactId": {
// 				Type:        "string",
// 				Description: "CRM contact identifier",
// 			},
// 			"accountId": {
// 				Type:        "string",
// 				Description: "CRM account identifier",
// 			},
// 			"leadId": {
// 				Type:        "string",
// 				Description: "CRM lead identifier",
// 			},
// 			"crmProvider": {
// 				Type:        "string",
// 				Description: "CRM provider used",
// 			},
// 			"createdAt": {
// 				Type:        "string",
// 				Description: "Timestamp when user was created",
// 			},
// 		},
// 		AdditionalProperties: false,
// 	}
// }

// func intPtr(i int) *int {
// 	return &i
// }
