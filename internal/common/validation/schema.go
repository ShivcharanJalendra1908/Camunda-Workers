package validation

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// JSONSchema defines the structure for input/output schemas
type JSONSchema struct {
	Type                 string              `json:"type"`
	Properties           map[string]Property `json:"properties"`
	Required             []string            `json:"required,omitempty"`
	AdditionalProperties bool                `json:"additionalProperties,omitempty"`
	PatternProperties    map[string]Property `json:"patternProperties,omitempty"`
}

type Property struct {
	Type        string              `json:"type"`
	Description string              `json:"description,omitempty"`
	Default     interface{}         `json:"default,omitempty"`
	Minimum     *float64            `json:"minimum,omitempty"`
	Maximum     *float64            `json:"maximum,omitempty"`
	Enum        []string            `json:"enum,omitempty"`
	Pattern     *string             `json:"pattern,omitempty"`
	MinLength   *int                `json:"minLength,omitempty"`
	MaxLength   *int                `json:"maxLength,omitempty"`
	Items       *Property           `json:"items,omitempty"`      // For array validation
	Properties  map[string]Property `json:"properties,omitempty"` // For nested objects
	Required    []string            `json:"required,omitempty"`   // For nested objects
}

type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// ValidateInput validates input against JSON schema with detailed errors
func ValidateInput(input map[string]interface{}, schema JSONSchema) *ValidationResult {
	errors := []ValidationError{}

	// Check required fields
	for _, requiredField := range schema.Required {
		if _, exists := input[requiredField]; !exists {
			errors = append(errors, ValidationError{
				Field:   requiredField,
				Message: "required field missing",
				Code:    "REQUIRED_FIELD_MISSING",
			})
		}
	}

	// Validate field types and constraints
	for fieldName, value := range input {
		prop, exists := schema.Properties[fieldName]
		if !exists {
			if !schema.AdditionalProperties {
				errors = append(errors, ValidationError{
					Field:   fieldName,
					Message: "field not allowed in schema",
					Code:    "EXTRA_FIELD",
				})
			}
			continue
		}

		if fieldErrors := validateField(fieldName, value, prop); len(fieldErrors) > 0 {
			errors = append(errors, fieldErrors...)
		}
	}

	return &ValidationResult{
		Valid:  len(errors) == 0,
		Errors: errors,
	}
}

func validateField(fieldName string, value interface{}, prop Property) []ValidationError {
	errors := []ValidationError{}

	// Type validation
	if typeErr := validateType(value, prop.Type); typeErr != nil {
		errors = append(errors, ValidationError{
			Field:   fieldName,
			Message: typeErr.Error(),
			Code:    "INVALID_TYPE",
		})
		return errors // Return early if type is wrong
	}

	// String validations
	if strVal, ok := value.(string); ok {
		// Length validation
		if prop.MinLength != nil && len(strVal) < *prop.MinLength {
			errors = append(errors, ValidationError{
				Field:   fieldName,
				Message: fmt.Sprintf("value must be at least %d characters", *prop.MinLength),
				Code:    "MIN_LENGTH_VIOLATION",
			})
		}
		if prop.MaxLength != nil && len(strVal) > *prop.MaxLength {
			errors = append(errors, ValidationError{
				Field:   fieldName,
				Message: fmt.Sprintf("value must be at most %d characters", *prop.MaxLength),
				Code:    "MAX_LENGTH_VIOLATION",
			})
		}

		// Pattern validation
		if prop.Pattern != nil {
			matched, err := regexp.MatchString(*prop.Pattern, strVal)
			if err != nil || !matched {
				errors = append(errors, ValidationError{
					Field:   fieldName,
					Message: fmt.Sprintf("value must match pattern %s", *prop.Pattern),
					Code:    "PATTERN_MISMATCH",
				})
			}
		}

		// Enum validation
		if prop.Enum != nil && len(prop.Enum) > 0 {
			found := false
			for _, enumVal := range prop.Enum {
				if strVal == enumVal {
					found = true
					break
				}
			}
			if !found {
				errors = append(errors, ValidationError{
					Field:   fieldName,
					Message: fmt.Sprintf("value must be one of %v", prop.Enum),
					Code:    "INVALID_ENUM_VALUE",
				})
			}
		}
	}

	// Number range validation
	if numVal, ok := value.(float64); ok {
		if prop.Minimum != nil && numVal < *prop.Minimum {
			errors = append(errors, ValidationError{
				Field:   fieldName,
				Message: fmt.Sprintf("value must be >= %f", *prop.Minimum),
				Code:    "MINIMUM_VIOLATION",
			})
		}
		if prop.Maximum != nil && numVal > *prop.Maximum {
			errors = append(errors, ValidationError{
				Field:   fieldName,
				Message: fmt.Sprintf("value must be <= %f", *prop.Maximum),
				Code:    "MAXIMUM_VIOLATION",
			})
		}
	}

	// Array validation
	if arrVal, ok := value.([]interface{}); ok && prop.Items != nil {
		for i, item := range arrVal {
			itemErrors := validateField(fmt.Sprintf("%s[%d]", fieldName, i), item, *prop.Items)
			errors = append(errors, itemErrors...)
		}
	}

	// Nested object validation
	if objVal, ok := value.(map[string]interface{}); ok && prop.Properties != nil {
		nestedSchema := JSONSchema{
			Type:                 "object",
			Properties:           prop.Properties,
			Required:             prop.Required,
			AdditionalProperties: true, // Default to allow additional properties in nested objects
		}
		nestedResult := ValidateInput(objVal, nestedSchema)
		for _, nestedErr := range nestedResult.Errors {
			errors = append(errors, ValidationError{
				Field:   fmt.Sprintf("%s.%s", fieldName, nestedErr.Field),
				Message: nestedErr.Message,
				Code:    nestedErr.Code,
			})
		}
	}

	return errors
}

func validateType(value interface{}, expectedType string) error {
	switch expectedType {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			// Try to convert from int
			if _, ok := value.(int); !ok {
				if _, ok := value.(int32); !ok {
					if _, ok := value.(int64); !ok {
						return fmt.Errorf("expected number, got %T", value)
					}
				}
			}
		}
	case "integer":
		if _, ok := value.(int); !ok {
			if _, ok := value.(int32); !ok {
				if _, ok := value.(int64); !ok {
					return fmt.Errorf("expected integer, got %T", value)
				}
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", value)
		}
	case "object":
		if _, ok := value.(map[string]interface{}); !ok {
			return fmt.Errorf("expected object, got %T", value)
		}
	case "array":
		if _, ok := value.([]interface{}); !ok {
			return fmt.Errorf("expected array, got %T", value)
		}
	case "null":
		if value != nil {
			return fmt.Errorf("expected null, got %T", value)
		}
	}
	return nil
}

// ValidateActivityNaming validates activity ID follows naming convention
func ValidateActivityNaming(activityId string) error {
	namingPattern := regexp.MustCompile(`^[a-z]+\.[a-z]+\.[a-z]+$`)
	if !namingPattern.MatchString(activityId) {
		return fmt.Errorf("activity ID must follow format: domain.subdomain.action (e.g., user.account.create)")
	}
	return nil
}

// GetSchemaFromJSON parses JSON schema from string
func GetSchemaFromJSON(schemaJSON string) (JSONSchema, error) {
	var schema JSONSchema
	err := json.Unmarshal([]byte(schemaJSON), &schema)
	return schema, err
}

// GetErrorMessages returns a simple list of error messages
func (vr *ValidationResult) GetErrorMessages() []string {
	messages := make([]string, len(vr.Errors))
	for i, err := range vr.Errors {
		messages[i] = fmt.Sprintf("%s: %s", err.Field, err.Message)
	}
	return messages
}

// HasErrors checks if validation has errors for specific field
func (vr *ValidationResult) HasErrors(field string) bool {
	for _, err := range vr.Errors {
		if err.Field == field {
			return true
		}
	}
	return false
}

// GetErrorsForField returns errors for a specific field
func (vr *ValidationResult) GetErrorsForField(field string) []ValidationError {
	var fieldErrors []ValidationError
	for _, err := range vr.Errors {
		if err.Field == field || strings.HasPrefix(err.Field, field+".") || strings.HasPrefix(err.Field, field+"[") {
			fieldErrors = append(fieldErrors, err)
		}
	}
	return fieldErrors
}

// ValidateEmail validates email format
func ValidateEmail(email string) bool {
	emailPattern := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	return emailPattern.MatchString(email)
}

// ValidatePhone validates basic phone number format
func ValidatePhone(phone string) bool {
	phonePattern := regexp.MustCompile(`^\+?[\d\s\-\(\)]{10,}$`)
	return phonePattern.MatchString(phone)
}

// ValidateURL validates URL format
func ValidateURL(url string) bool {
	urlPattern := regexp.MustCompile(`^(https?|ftp)://[^\s/$.?#].[^\s]*$`)
	return urlPattern.MatchString(url)
}
