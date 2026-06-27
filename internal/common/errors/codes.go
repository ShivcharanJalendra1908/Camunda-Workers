// internal/common/errors/codes.go
package errors

import (
	"net/http"
	"time"
)

// Infrastructure
const (
	SUBSCRIPTION_INVALID       = "SUBSCRIPTION_INVALID"
	SUBSCRIPTION_EXPIRED       = "SUBSCRIPTION_EXPIRED"
	SUBSCRIPTION_CHECK_FAILED  = "SUBSCRIPTION_CHECK_FAILED"
	TEMPLATE_NOT_FOUND         = "TEMPLATE_NOT_FOUND"
	TEMPLATE_VALIDATION_FAILED = "TEMPLATE_VALIDATION_FAILED"
)

// Data Access
const (
	DATABASE_CONNECTION_FAILED      = "DATABASE_CONNECTION_FAILED"
	QUERY_EXECUTION_FAILED          = "QUERY_EXECUTION_FAILED"
	QUERY_TIMEOUT                   = "QUERY_TIMEOUT"
	INVALID_QUERY_TYPE              = "INVALID_QUERY_TYPE"
	ELASTICSEARCH_CONNECTION_FAILED = "ELASTICSEARCH_CONNECTION_FAILED"
	SEARCH_QUERY_FAILED             = "SEARCH_QUERY_FAILED"
	SEARCH_TIMEOUT                  = "SEARCH_TIMEOUT"
	INDEX_NOT_FOUND                 = "INDEX_NOT_FOUND"
)

// Business Logic
const (
	INVALID_FILTER_FORMAT         = "INVALID_FILTER_FORMAT"
	APPLICATION_VALIDATION_FAILED = "APPLICATION_VALIDATION_FAILED"
	DATABASE_INSERT_FAILED        = "DATABASE_INSERT_FAILED"
	DUPLICATE_APPLICATION         = "DUPLICATE_APPLICATION"
	NOTIFICATION_SEND_FAILED      = "NOTIFICATION_SEND_FAILED"
)

// Validation Errors (GAP #1)
const (
	// Input Validation
	VALIDATION_FAILED      = "VALIDATION_FAILED"
	INVALID_INPUT_TYPE     = "INVALID_INPUT_TYPE"
	REQUIRED_FIELD_MISSING = "REQUIRED_FIELD_MISSING"
	INVALID_FORMAT         = "INVALID_FORMAT"
	VALUE_OUT_OF_RANGE     = "VALUE_OUT_OF_RANGE"
	STRING_TOO_LONG        = "STRING_TOO_LONG"
	STRING_TOO_SHORT       = "STRING_TOO_SHORT"
	ARRAY_TOO_LARGE        = "ARRAY_TOO_LARGE"
	OBJECT_TOO_DEEP        = "OBJECT_TOO_DEEP"
	INVALID_ENUM_VALUE     = "INVALID_ENUM_VALUE"
	PATTERN_MISMATCH       = "PATTERN_MISMATCH"

	// SQL Injection Prevention
	SQL_INJECTION_DETECTED      = "SQL_INJECTION_DETECTED"
	UNSAFE_SQL_CHARACTERS       = "UNSAFE_SQL_CHARACTERS"
	INVALID_DATABASE_IDENTIFIER = "INVALID_DATABASE_IDENTIFIER"

	// NoSQL Injection Prevention
	NOSQL_INJECTION_DETECTED  = "NOSQL_INJECTION_DETECTED"
	UNSAFE_QUERY_OPERATOR     = "UNSAFE_QUERY_OPERATOR"
	SCRIPT_INJECTION_DETECTED = "SCRIPT_INJECTION_DETECTED"

	// Data Type Validation
	INVALID_UUID_FORMAT  = "INVALID_UUID_FORMAT"
	INVALID_EMAIL_FORMAT = "INVALID_EMAIL_FORMAT"
	INVALID_PHONE_FORMAT = "INVALID_PHONE_FORMAT"
	INVALID_URL_FORMAT   = "INVALID_URL_FORMAT"
	INVALID_DATE_FORMAT  = "INVALID_DATE_FORMAT"

	// Request Size Limits
	REQUEST_BODY_TOO_LARGE = "REQUEST_BODY_TOO_LARGE"
	PAYLOAD_TOO_COMPLEX    = "PAYLOAD_TOO_COMPLEX"
)

// AI/ML
const (
	INTENT_PARSING_FAILED = "INTENT_PARSING_FAILED"
	INTENT_API_TIMEOUT    = "INTENT_API_TIMEOUT"
	WEB_SEARCH_TIMEOUT    = "WEB_SEARCH_TIMEOUT"
	LLM_TIMEOUT           = "LLM_TIMEOUT"
	LLM_SYNTHESIS_FAILED  = "LLM_SYNTHESIS_FAILED"
)

// Circuit Breaker & Rate Limiting
const (
	CIRCUIT_BREAKER_OPEN      = "CIRCUIT_BREAKER_OPEN"
	CIRCUIT_BREAKER_HALF_OPEN = "CIRCUIT_BREAKER_HALF_OPEN"
	RATE_LIMIT_EXCEEDED       = "RATE_LIMIT_EXCEEDED"
	DEPENDENCY_FAILURE        = "DEPENDENCY_FAILURE"
	CONTEXT_CANCELLED         = "CONTEXT_CANCELLED"
	SERVICE_UNAVAILABLE       = "SERVICE_UNAVAILABLE"
)

// Error Categories (GAP #4)
type ErrorCategory string

const (
	CategoryValidation ErrorCategory = "validation" // 4xx: Never retry
	CategoryClient     ErrorCategory = "client"     // 4xx: User action needed
	CategoryTransient  ErrorCategory = "transient"  // 5xx: Retry with backoff
	CategoryPermanent  ErrorCategory = "permanent"  // 5xx: Alert ops
	CategoryDependency ErrorCategory = "dependency" // 5xx: Circuit breaker
)

// GetErrorCategory maps error codes to categories
func GetErrorCategory(code ErrorCode) ErrorCategory {
	switch code {
	case ErrCodeSubscriptionInvalid, ErrCodeSubscriptionExpired, ErrCodeTemplateNotFound,
		ErrCodeTemplateValidationFailed, ErrCodeInvalidQueryType, ErrCodeIndexNotFound,
		ErrCodeInvalidFilterFormat, ErrCodeApplicationValidationFailed, ErrCodeDuplicateApplication:
		return CategoryValidation

	case ErrCodeDatabaseConnectionFailed, ErrCodeQueryTimeout, ErrCodeElasticsearchConnectionFailed,
		ErrCodeSearchTimeout, ErrCodeIntentAPITimeout, ErrCodeWebSearchTimeout, ErrCodeLLMTimeout,
		ErrorCode("RATE_LIMIT_EXCEEDED"), ErrorCode("CONTEXT_CANCELLED"), ErrorCode("SERVICE_UNAVAILABLE"):
		return CategoryTransient

	case ErrCodeSubscriptionCheckFailed, ErrCodeQueryExecutionFailed, ErrCodeSearchQueryFailed,
		ErrCodeDatabaseInsertFailed, ErrCodeNotificationSendFailed, ErrCodeIntentParsingFailed,
		ErrCodeLLMSynthesisFailed, ErrorCode("CIRCUIT_BREAKER_OPEN"), ErrorCode("CIRCUIT_BREAKER_HALF_OPEN"),
		ErrorCode("DEPENDENCY_FAILURE"):
		return CategoryDependency

	default:
		return CategoryPermanent
	}
}

// GetRetryCount returns max retries based on error code
func GetRetryCount(code ErrorCode) int {
	switch code {
	case ErrorCode("CIRCUIT_BREAKER_OPEN"), ErrorCode("RATE_LIMIT_EXCEEDED"), ErrorCode("DEPENDENCY_FAILURE"):
		return 3
	case ErrorCode("CONTEXT_CANCELLED"):
		return 2
	}
	// Fallback by category
	switch GetErrorCategory(code) {
	case CategoryTransient:
		return 3
	case CategoryDependency:
		return 2
	default:
		return 0
	}
}

// GetUserFriendlyMessage returns safe messages for users
func GetUserFriendlyMessage(code ErrorCode) string {
	switch code {
	case ErrorCode("CIRCUIT_BREAKER_OPEN"), ErrorCode("CIRCUIT_BREAKER_HALF_OPEN"):
		return "Service temporarily unavailable. Please try again in a few moments."
	case ErrorCode("RATE_LIMIT_EXCEEDED"):
		return "Too many requests. Please wait before trying again."
	case ErrorCode("DEPENDENCY_FAILURE"):
		return "A required service is currently unavailable. Please try again later."
	case ErrorCode("SERVICE_UNAVAILABLE"):
		return "The service is currently undergoing maintenance. Please try again soon."
	case ErrorCode("CONTEXT_CANCELLED"):
		return "Request timed out. Please try again."
	case ErrCodeApplicationValidationFailed:
		return "Please check your application details and try again."
	case ErrCodeSubscriptionInvalid:
		return "Your subscription is invalid or expired."
	case ErrCodeDuplicateApplication:
		return "You have already applied for this franchise."
	case ErrCodeDatabaseConnectionFailed, ErrCodeElasticsearchConnectionFailed:
		return "Service temporarily unavailable. Please try again later."
	case ErrCodeQueryTimeout, ErrCodeSearchTimeout:
		return "Request timed out. Please try again."
	default:
		return "An unexpected error occurred. Please contact support."
	}
}

// GetHTTPStatus maps error codes to HTTP status codes
func GetHTTPStatus(code ErrorCode) int {
	switch code {
	case ErrorCode("CIRCUIT_BREAKER_OPEN"), ErrorCode("SERVICE_UNAVAILABLE"), ErrorCode("DEPENDENCY_FAILURE"):
		return http.StatusServiceUnavailable
	case ErrorCode("RATE_LIMIT_EXCEEDED"):
		return http.StatusTooManyRequests
	case ErrorCode("CONTEXT_CANCELLED"):
		return http.StatusRequestTimeout
	}
	// Fallback by category
	switch GetErrorCategory(code) {
	case CategoryValidation, CategoryClient:
		return http.StatusBadRequest
	case CategoryTransient:
		return http.StatusServiceUnavailable
	case CategoryDependency:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// ============================================================================
// ERROR CONTEXT STRUCTURE (GAP #4)
// ============================================================================

// ErrorContext provides detailed context for error tracking and debugging
type ErrorContext struct {
	ErrorID          string                 `json:"errorId"`
	ErrorCode        ErrorCode              `json:"errorCode"`
	ErrorMessage     string                 `json:"errorMessage"`
	Details          string                 `json:"details,omitempty"`
	Category         ErrorCategory          `json:"category"`
	Severity         string                 `json:"severity"`
	Timestamp        string                 `json:"timestamp"`
	Context          map[string]interface{} `json:"context"`
	Resolution       ResolutionInfo         `json:"resolution"`
	UserMessage      string                 `json:"userMessage,omitempty"`
	DocumentationURL string                 `json:"documentationUrl,omitempty"`
}

// ResolutionInfo provides error resolution details
type ResolutionInfo struct {
	Retryable       bool   `json:"retryable"`
	RetryAfter      string `json:"retryAfter,omitempty"`
	SuggestedAction string `json:"suggestedAction"`
	UserAction      string `json:"userAction"`
	MaxRetries      int    `json:"maxRetries"`
	CurrentRetry    int    `json:"currentRetry"`
}

// NewErrorContext creates a complete error context from StandardError
func NewErrorContext(stdErr *StandardError, jobContext map[string]interface{}) *ErrorContext {
	category := GetErrorCategory(stdErr.Code)
	severity := getSeverityFromCategory(category)
	retryCount := GetRetryCount(stdErr.Code)

	return &ErrorContext{
		ErrorID:          stdErr.ID,
		ErrorCode:        stdErr.Code,
		ErrorMessage:     stdErr.Message,
		Details:          stdErr.Details,
		Category:         category,
		Severity:         severity,
		Timestamp:        stdErr.Timestamp.Format(time.RFC3339),
		Context:          jobContext,
		UserMessage:      GetUserFriendlyMessage(stdErr.Code),
		DocumentationURL: getDocumentationURL(stdErr.Code),
		Resolution: ResolutionInfo{
			Retryable:       stdErr.Retryable,
			RetryAfter:      getRetryAfter(category, retryCount),
			SuggestedAction: getSuggestedAction(category),
			UserAction:      GetUserFriendlyMessage(stdErr.Code),
			MaxRetries:      retryCount,
		},
	}
}

// Helper functions for ErrorContext
func getSeverityFromCategory(cat ErrorCategory) string {
	switch cat {
	case CategoryValidation, CategoryClient:
		return "WARN"
	case CategoryTransient:
		return "ERROR"
	case CategoryDependency:
		return "HIGH"
	case CategoryPermanent:
		return "CRITICAL"
	default:
		return "ERROR"
	}
}

func getRetryAfter(cat ErrorCategory, retryCount int) string {
	if retryCount == 0 {
		return ""
	}

	switch cat {
	case CategoryTransient:
		return "Exponential backoff (1s, 2s, 4s...)"
	case CategoryDependency:
		return "Fixed 5s delay"
	default:
		return ""
	}
}

func getSuggestedAction(cat ErrorCategory) string {
	switch cat {
	case CategoryValidation:
		return "Fix input data and retry"
	case CategoryClient:
		return "User action required"
	case CategoryTransient:
		return "Retry automatically with backoff"
	case CategoryDependency:
		return "Check dependency health, use circuit breaker"
	case CategoryPermanent:
		return "Alert operations team"
	default:
		return "Contact support"
	}
}

func getDocumentationURL(code ErrorCode) string {
	// Base URL for error documentation
	baseURL := "https://docs.lemici.com/errors/"
	return baseURL + string(code)
}
