package validation

import (
	"html"
	"regexp"
	"strings"
	"unicode"
)

// Sanitizer provides input sanitization
type Sanitizer struct{}

// NewSanitizer creates a new sanitizer
func NewSanitizer() *Sanitizer {
	return &Sanitizer{}
}

// SanitizeInput sanitizes all string fields in a map
func (s *Sanitizer) SanitizeInput(input map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	for key, value := range input {
		result[key] = s.sanitizeValue(value)
	}

	return result
}

func (s *Sanitizer) sanitizeValue(value interface{}) interface{} {
	switch v := value.(type) {
	case string:
		return s.SanitizeString(v)
	case map[string]interface{}:
		return s.SanitizeInput(v)
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = s.sanitizeValue(item)
		}
		return result
	default:
		return value
	}
}

// SanitizeString performs comprehensive string sanitization
func (s *Sanitizer) SanitizeString(input string) string {
	// 1. Trim whitespace
	input = strings.TrimSpace(input)

	// 2. Remove null bytes
	input = strings.ReplaceAll(input, "\x00", "")

	// 3. Remove control characters (except newline, tab, carriage return)
	input = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return -1
		}
		return r
	}, input)

	// 4. Normalize Unicode to NFC form
	// Note: Would need golang.org/x/text/unicode/norm for full implementation

	// 5. HTML escape (for fields that will be displayed in UI)
	// Only if needed - comment out if not required
	// input = html.EscapeString(input)

	return input
}

// SanitizeHTML removes potentially dangerous HTML
func (s *Sanitizer) SanitizeHTML(input string) string {
	// Remove script tags
	scriptRegex := regexp.MustCompile(`(?i)<script[^>]*>.*?</script>`)
	input = scriptRegex.ReplaceAllString(input, "")

	// Remove event handlers
	eventRegex := regexp.MustCompile(`(?i)on\w+\s*=\s*["'][^"']*["']`)
	input = eventRegex.ReplaceAllString(input, "")

	// Escape HTML
	input = html.EscapeString(input)

	return input
}

// SanitizeSQL escapes SQL special characters (use parameterized queries instead!)
func (s *Sanitizer) SanitizeSQL(input string) string {
	// This should NEVER be used for actual SQL queries
	// Always use parameterized queries
	// This is only for logging/display purposes

	replacer := strings.NewReplacer(
		"'", "''",
		"\\", "\\\\",
		"\n", "\\n",
		"\r", "\\r",
		"\x00", "\\0",
		"\x1a", "\\Z",
	)

	return replacer.Replace(input)
}

// RemoveSQLKeywords removes common SQL keywords (for logging)
func (s *Sanitizer) RemoveSQLKeywords(input string) string {
	keywords := []string{
		"SELECT", "INSERT", "UPDATE", "DELETE", "DROP", "CREATE", "ALTER",
		"EXEC", "EXECUTE", "UNION", "JOIN", "WHERE", "FROM", "TABLE",
		"DATABASE", "SCHEMA", "INDEX", "VIEW", "PROCEDURE", "FUNCTION",
		"--", "/*", "*/", ";", "xp_", "sp_",
	}

	lower := strings.ToLower(input)
	for _, keyword := range keywords {
		lower = strings.ReplaceAll(lower, strings.ToLower(keyword), "")
	}

	return lower
}

// RemoveNoSQLOperators removes MongoDB/Elasticsearch operators
func (s *Sanitizer) RemoveNoSQLOperators(input string) string {
	operators := []string{
		"$where", "$ne", "$gt", "$gte", "$lt", "$lte", "$in", "$nin",
		"$regex", "$exists", "$type", "$expr", "$jsonSchema",
		"script", "inline", "function", "eval",
	}

	for _, op := range operators {
		input = strings.ReplaceAll(input, op, "")
	}

	return input
}
