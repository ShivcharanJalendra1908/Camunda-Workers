package validation

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

// Validator provides comprehensive input validation
type Validator struct {
	maxStringLength int
	maxArraySize    int
	maxObjectDepth  int
	maxBodySize     int64
}

// NewValidator creates a new validator with default limits
func NewValidator() *Validator {
	return &Validator{
		maxStringLength: 1000,
		maxArraySize:    100,
		maxObjectDepth:  10,
		maxBodySize:     10 * 1024 * 1024, // 10MB
	}
}

// Common validation rules
var (
	// UUID validation
	IsUUID = ozzo.NewStringRule(func(s string) bool {
		if s == "" {
			return true
		}
		match, _ := regexp.MatchString(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`, strings.ToLower(s))
		return match
	}, "must be a valid UUID v4")

	// SQL Injection prevention
	SafeSQLString = ozzo.NewStringRule(func(s string) bool {
		dangerous := []string{";", "--", "/*", "*/", "xp_", "sp_", "exec", "execute", "DROP", "UNION", "SELECT", "INSERT", "UPDATE", "DELETE"}
		lower := strings.ToLower(s)
		for _, pattern := range dangerous {
			if strings.Contains(lower, strings.ToLower(pattern)) {
				return false
			}
		}
		return true
	}, "contains potentially unsafe SQL characters")

	// NoSQL Injection prevention (MongoDB, Elasticsearch)
	SafeNoSQLString = ozzo.NewStringRule(func(s string) bool {
		// dangerous := []string{"$where", "$ne", "$gt", "$regex", "script", "eval(", "function("}
		dangerous := []string{"$where", "$ne", "$gt", "$regex", "<script", "eval(", "function("}
		lower := strings.ToLower(s)
		for _, pattern := range dangerous {
			if strings.Contains(lower, pattern) {
				return false
			}
		}
		return true
	}, "contains potentially unsafe NoSQL operators")

	// Alphanumeric with dashes/underscores only
	IDString = ozzo.Match(regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)).Error("must contain only alphanumeric characters, dashes, or underscores")

	// No special characters (prevents injection)
	AlphanumericOnly = ozzo.Match(regexp.MustCompile(`^[a-zA-Z0-9]+$`)).Error("must contain only alphanumeric characters")

	// Valid table/column names (PostgreSQL safe)
	SafeDatabaseIdentifier = ozzo.Match(regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)).Error("must be a valid database identifier")
)

// SanitizeString removes dangerous characters
func SanitizeString(s string) string {
	// Remove null bytes
	s = strings.ReplaceAll(s, "\x00", "")

	// Remove control characters
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return -1
		}
		return r
	}, s)

	// Trim whitespace
	s = strings.TrimSpace(s)

	return s
}

// ValidateStringLength validates string length with sanitization
func ValidateStringLength(min, max int) ozzo.Rule {
	return ozzo.By(func(value interface{}) error {
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be a string")
		}

		s = SanitizeString(s)
		length := len(s)

		if min > 0 && length < min {
			return fmt.Errorf("must be at least %d characters", min)
		}
		if max > 0 && length > max {
			return fmt.Errorf("must be at most %d characters", max)
		}
		return nil
	})
}

// ValidateArraySize validates array size
func ValidateArraySize(min, max int) ozzo.Rule {
	return ozzo.By(func(value interface{}) error {
		switch v := value.(type) {
		case []interface{}:
			if min > 0 && len(v) < min {
				return fmt.Errorf("array must have at least %d items", min)
			}
			if max > 0 && len(v) > max {
				return fmt.Errorf("array must have at most %d items", max)
			}
		case []string:
			if min > 0 && len(v) < min {
				return fmt.Errorf("array must have at least %d items", min)
			}
			if max > 0 && len(v) > max {
				return fmt.Errorf("array must have at most %d items", max)
			}
		default:
			return fmt.Errorf("must be an array")
		}
		return nil
	})
}

// ValidateEnum validates that value is in allowed list
func ValidateEnum(allowed []string) ozzo.Rule {
	return ozzo.In(stringsToInterfaces(allowed)...).Error(fmt.Sprintf("must be one of: %s", strings.Join(allowed, ", ")))
}

// ValidateIntRange validates integer range
func ValidateIntRange(min, max int64) ozzo.Rule {
	return ozzo.By(func(value interface{}) error {
		var num int64
		switch v := value.(type) {
		case int:
			num = int64(v)
		case int32:
			num = int64(v)
		case int64:
			num = v
		case float64:
			num = int64(v)
		default:
			return fmt.Errorf("must be a number")
		}

		if num < min {
			return fmt.Errorf("must be >= %d", min)
		}
		if num > max {
			return fmt.Errorf("must be <= %d", max)
		}
		return nil
	})
}

// ValidateEmail validates email format
func ValidateEmail() ozzo.Rule {
	return is.Email.Error("must be a valid email address")
}

// ValidatePhone validates phone number format
func ValidatePhone() ozzo.Rule {
	return ozzo.Match(regexp.MustCompile(`^\+?[1-9]\d{1,14}$`)).Error("must be a valid phone number (E.164 format)")
}

// ValidateURL validates URL format
func ValidateURL() ozzo.Rule {
	return is.URL.Error("must be a valid URL")
}

// Helper function
func stringsToInterfaces(strings []string) []interface{} {
	result := make([]interface{}, len(strings))
	for i, s := range strings {
		result[i] = s
	}
	return result
}

// package validation

// import (
// 	"regexp"
// 	"strings"

// 	ozzo "github.com/go-ozzo/ozzo-validation/v4"
// 	"github.com/go-ozzo/ozzo-validation/v4/is"
// )

// // Common validation rules
// var (
// 	// IsUUID validates that the string is a valid UUID
// 	IsUUID = ozzo.NewStringRule(isUUID, "must be a valid UUID")

// 	// SafeSQLString validates that the string does not contain common SQL injection patterns
// 	SafeSQLString = ozzo.NewStringRule(isSafeSQLString, "contains unsafe SQL characters")

// 	// IDString validates that the string contains only alphanumeric characters and dashes
// 	IDString = ozzo.Match(regexp.MustCompile("^[a-zA-Z0-9-]+$")).Error("must contain only alphanumeric characters and dashes")
// )

// // isUUID checks if the string is a valid UUID
// func isUUID(s string) bool {
// 	if s == "" {
// 		return true // Allow empty, use Required rule to enforce presence
// 	}
// 	return is.UUIDv4.Validate(s) == nil
// }

// // isSafeSQLString checks for common SQL injection characters
// func isSafeSQLString(s string) bool {
// 	unsafePatterns := []string{";", "--", "/*", "*/", "'", "\""}
// 	for _, pattern := range unsafePatterns {
// 		if strings.Contains(s, pattern) {
// 			return false
// 		}
// 	}
// 	return true
// }

// // SanitizeString removes leading/trailing whitespace and control characters
// func SanitizeString(s string) string {
// 	// Remove control characters
// 	re := regexp.MustCompile(`[\x00-\x1F\x7F]`)
// 	s = re.ReplaceAllString(s, "")
// 	// Trim whitespace
// 	return strings.TrimSpace(s)
// }

// // ValidateStruct is a helper to validate a struct using ozzo-validation
// func ValidateStruct(s interface{}, fields ...*ozzo.FieldRules) error {
// 	return ozzo.ValidateStruct(s, fields...)
// }
