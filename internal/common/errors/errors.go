// Package errors provides standardized error handling for BPMN workflow integration.
package errors

import (
	"fmt"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
)

// ==========================
// 1. Standard Error Types
// ==========================

// ErrorCode represents standardized internal error codes.
type ErrorCode string

// Business Rule / Subscription Errors (from requirements Appendix B)
const (
	ErrCodeSubscriptionInvalid     ErrorCode = "SUBSCRIPTION_INVALID"
	ErrCodeSubscriptionExpired     ErrorCode = "SUBSCRIPTION_EXPIRED"
	ErrCodeSubscriptionCheckFailed ErrorCode = "SUBSCRIPTION_CHECK_FAILED"

	ErrCodeTemplateNotFound         ErrorCode = "TEMPLATE_NOT_FOUND"
	ErrCodeTemplateValidationFailed ErrorCode = "TEMPLATE_VALIDATION_FAILED"

	ErrCodeDatabaseConnectionFailed ErrorCode = "DATABASE_CONNECTION_FAILED"
	ErrCodeQueryExecutionFailed     ErrorCode = "QUERY_EXECUTION_FAILED"
	ErrCodeQueryTimeout             ErrorCode = "QUERY_TIMEOUT"
	ErrCodeInvalidQueryType         ErrorCode = "INVALID_QUERY_TYPE"

	ErrCodeElasticsearchConnectionFailed ErrorCode = "ELASTICSEARCH_CONNECTION_FAILED"
	ErrCodeSearchQueryFailed             ErrorCode = "SEARCH_QUERY_FAILED"
	ErrCodeSearchTimeout                 ErrorCode = "SEARCH_TIMEOUT"
	ErrCodeIndexNotFound                 ErrorCode = "INDEX_NOT_FOUND"

	ErrCodeInvalidFilterFormat         ErrorCode = "INVALID_FILTER_FORMAT"
	ErrCodeApplicationValidationFailed ErrorCode = "APPLICATION_VALIDATION_FAILED"

	ErrCodeDatabaseInsertFailed ErrorCode = "DATABASE_INSERT_FAILED"
	ErrCodeDuplicateApplication ErrorCode = "DUPLICATE_APPLICATION"

	ErrCodeNotificationSendFailed ErrorCode = "NOTIFICATION_SEND_FAILED"

	ErrCodeIntentParsingFailed ErrorCode = "INTENT_PARSING_FAILED"
	ErrCodeIntentAPITimeout    ErrorCode = "INTENT_API_TIMEOUT"
	ErrCodeWebSearchTimeout    ErrorCode = "WEB_SEARCH_TIMEOUT"
	ErrCodeLLMTimeout          ErrorCode = "LLM_TIMEOUT"
	ErrCodeLLMSynthesisFailed  ErrorCode = "LLM_SYNTHESIS_FAILED"
)

// StandardError represents a structured application error.
type StandardError struct {
	ID         string                 `json:"id"`
	Code       ErrorCode              `json:"code"`
	Message    string                 `json:"message"`
	Details    string                 `json:"details,omitempty"`
	Retryable  bool                   `json:"retryable"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	Timestamp  time.Time              `json:"timestamp"`
	StackTrace string                 `json:"stackTrace,omitempty"`
}

func (e *StandardError) Error() string {
	return fmt.Sprintf("StandardError[%s] %s: %s", e.Code, e.ID, e.Message)
}

// ==========================
// 2. BPMN Error Integration
// ==========================

// BPMNError represents an error that can be thrown to the Camunda workflow engine.
type BPMNError struct {
	Code           string                 `json:"code"`
	Message        string                 `json:"message"`
	Details        string                 `json:"details,omitempty"`
	Retryable      bool                   `json:"retryable"`
	Retries        int                    `json:"retries"`
	ErrorVariables map[string]interface{} `json:"errorVariables,omitempty"`
}

func (e *BPMNError) Error() string {
	return fmt.Sprintf("BPMNError[%s]: %s", e.Code, e.Message)
}

// ToErrorVariables returns a map suitable for setting Camunda job fail variables.
func (e *BPMNError) ToErrorVariables() map[string]interface{} {
	vars := map[string]interface{}{
		"errorCode":    e.Code,
		"errorMessage": e.Message,
		"errorDetails": e.Details,
		"retryable":    e.Retryable,
	}

	if e.ErrorVariables != nil {
		for k, v := range e.ErrorVariables {
			vars[k] = v
		}
	}

	return vars
}

// ==========================
// 3. Error Constructors
// ==========================

// NewSubscriptionInvalidError creates a non-retryable subscription error.
func NewSubscriptionInvalidError(details string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeSubscriptionInvalid,
		Message:    "Invalid or not found subscription",
		Details:    details,
		Retryable:  false,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewSubscriptionExpiredError creates a non-retryable subscription error.
func NewSubscriptionExpiredError(details string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeSubscriptionExpired,
		Message:    "Subscription has expired",
		Details:    details,
		Retryable:  false,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewSubscriptionCheckFailedError creates a retryable database error.
func NewSubscriptionCheckFailedError(err error) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeSubscriptionCheckFailed,
		Message:    "Database error during subscription check",
		Details:    err.Error(),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewTemplateNotFoundError creates a non-retryable template error.
func NewTemplateNotFoundError(templateID string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeTemplateNotFound,
		Message:    "Template not found in registry",
		Details:    fmt.Sprintf("templateId: %s", templateID),
		Retryable:  false,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewTemplateValidationFailedError creates a non-retryable template validation error.
func NewTemplateValidationFailedError(details string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeTemplateValidationFailed,
		Message:    "Data validation failed for template",
		Details:    details,
		Retryable:  false,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewDatabaseConnectionFailedError creates a retryable database connection error.
func NewDatabaseConnectionFailedError(err error) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeDatabaseConnectionFailed,
		Message:    "Database connection error",
		Details:    err.Error(),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewQueryExecutionFailedError creates a retryable query execution error.
func NewQueryExecutionFailedError(queryType string, err error) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeQueryExecutionFailed,
		Message:    "Database query execution error",
		Details:    fmt.Sprintf("queryType: %s, error: %s", queryType, err.Error()),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewQueryTimeoutError creates a retryable query timeout error.
func NewQueryTimeoutError(queryType string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeQueryTimeout,
		Message:    "Database query timeout",
		Details:    fmt.Sprintf("queryType: %s", queryType),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewInvalidQueryTypeError creates a non-retryable invalid query type error.
func NewInvalidQueryTypeError(queryType string) *StandardError {
	return &StandardError{
		Code:      ErrCodeInvalidQueryType,
		Message:   "Unsupported query type",
		Details:   fmt.Sprintf("queryType: %s", queryType),
		Retryable: false,
		Timestamp: time.Now().UTC(),
	}
}

// NewElasticsearchConnectionFailedError creates a retryable Elasticsearch connection error.
func NewElasticsearchConnectionFailedError(err error) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeElasticsearchConnectionFailed,
		Message:    "Elasticsearch connection error",
		Details:    err.Error(),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewSearchQueryFailedError creates a retryable search query error.
func NewSearchQueryFailedError(queryType string, err error) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeSearchQueryFailed,
		Message:    "Elasticsearch query error",
		Details:    fmt.Sprintf("queryType: %s, error: %s", queryType, err.Error()),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewSearchTimeoutError creates a retryable search timeout error.
func NewSearchTimeoutError(queryType string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeSearchTimeout,
		Message:    "Elasticsearch query timeout",
		Details:    fmt.Sprintf("queryType: %s", queryType),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewIndexNotFoundError creates a non-retryable index not found error.
func NewIndexNotFoundError(indexName string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeIndexNotFound,
		Message:    "Elasticsearch index not found",
		Details:    fmt.Sprintf("indexName: %s", indexName),
		Retryable:  false,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewInvalidFilterFormatError creates a non-retryable filter format error.
func NewInvalidFilterFormatError(details string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeInvalidFilterFormat,
		Message:    "Invalid filter format",
		Details:    details,
		Retryable:  false,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewApplicationValidationFailedError creates a non-retryable application validation error.
func NewApplicationValidationFailedError(details string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeApplicationValidationFailed,
		Message:    "Application data validation failed",
		Details:    details,
		Retryable:  false,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewDatabaseInsertFailedError creates a retryable database insert error.
func NewDatabaseInsertFailedError(err error) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeDatabaseInsertFailed,
		Message:    "Database insert operation failed",
		Details:    err.Error(),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewDuplicateApplicationError creates a non-retryable duplicate application error.
func NewDuplicateApplicationError(applicationID string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeDuplicateApplication,
		Message:    "Application already exists",
		Details:    fmt.Sprintf("applicationId: %s", applicationID),
		Retryable:  false,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewNotificationSendFailedError creates a retryable notification send error.
func NewNotificationSendFailedError(notificationType string, err error) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeNotificationSendFailed,
		Message:    "Notification delivery failed",
		Details:    fmt.Sprintf("type: %s, error: %s", notificationType, err.Error()),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewIntentParsingFailedError creates a retryable intent parsing error.
func NewIntentParsingFailedError(err error) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeIntentParsingFailed,
		Message:    "Intent parsing API error",
		Details:    err.Error(),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewIntentAPITimeoutError creates a retryable intent API timeout error.
func NewIntentAPITimeoutError() *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeIntentAPITimeout,
		Message:    "Intent parsing API timeout",
		Details:    "API call exceeded timeout threshold",
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewWebSearchTimeoutError creates a non-retryable (returns empty) web search timeout error.
func NewWebSearchTimeoutError() *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeWebSearchTimeout,
		Message:    "Web search API timeout",
		Details:    "Search call exceeded 3 second timeout",
		Retryable:  false, // Per doc: return empty, don't retry
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewLLMTimeoutError creates a retryable LLM timeout error.
func NewLLMTimeoutError() *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeLLMTimeout,
		Message:    "LLM synthesis timeout",
		Details:    "LLM call exceeded 5 second timeout",
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewLLMSynthesisFailedError creates a retryable LLM synthesis error.
func NewLLMSynthesisFailedError(err error) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrCodeLLMSynthesisFailed,
		Message:    "LLM synthesis API error",
		Details:    err.Error(),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// Generic constructors

func NewBusinessRuleError(message, details string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       "BUSINESS_RULE_VIOLATION",
		Message:    message,
		Details:    details,
		Retryable:  false,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

func NewExternalServiceError(service string, err error) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       "EXTERNAL_SERVICE_ERROR",
		Message:    fmt.Sprintf("External service '%s' error", service),
		Details:    err.Error(),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

func NewTimeoutError(service string, err error) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       "TIMEOUT_ERROR",
		Message:    fmt.Sprintf("Service '%s' timeout", service),
		Details:    err.Error(),
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

func NewResourceNotFoundError(service, details string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       "RESOURCE_NOT_FOUND",
		Message:    fmt.Sprintf("Resource not found in %s", service),
		Details:    details,
		Retryable:  false,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

func NewAuthenticationError(details string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       "AUTHENTICATION_ERROR",
		Message:    "Authentication failed",
		Details:    details,
		Retryable:  false,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

func NewCircuitBreakerOpenError(serviceName string, timeout time.Duration) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrorCode("CIRCUIT_BREAKER_OPEN"),
		Message:    fmt.Sprintf("Circuit breaker for %s is OPEN. Timeout: %v", serviceName, timeout),
		Details:    "",
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

func NewCircuitBreakerHalfOpenError(serviceName string, concurrentRequests int) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrorCode("CIRCUIT_BREAKER_HALF_OPEN"),
		Message:    fmt.Sprintf("Circuit breaker for %s is HALF_OPEN. Concurrent: %d", serviceName, concurrentRequests),
		Details:    "",
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

func NewRateLimitExceededError(serviceName string, limitPerMinute int) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrorCode("RATE_LIMIT_EXCEEDED"),
		Message:    fmt.Sprintf("Rate limit exceeded for %s: %d req/min", serviceName, limitPerMinute),
		Details:    "",
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

func NewContextCancelledError(reason string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrorCode("CONTEXT_CANCELLED"),
		Message:    fmt.Sprintf("Context cancelled: %s", reason),
		Details:    "",
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

func NewDependencyError(serviceName, details string) *StandardError {
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       ErrorCode("DEPENDENCY_FAILURE"),
		Message:    fmt.Sprintf("Dependency %s failed: %s", serviceName, details),
		Details:    details,
		Retryable:  true,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// ==========================
// 4. Error Conversion to BPMN
// ==========================

// BPMNErrorMapping maps internal error codes to BPMN error codes (same as internal).
// Per Appendix B, these are identical.
var BPMNErrorMapping = map[ErrorCode]string{
	ErrCodeSubscriptionInvalid:           "SUBSCRIPTION_INVALID",
	ErrCodeSubscriptionExpired:           "SUBSCRIPTION_EXPIRED",
	ErrCodeSubscriptionCheckFailed:       "SUBSCRIPTION_CHECK_FAILED",
	ErrCodeTemplateNotFound:              "TEMPLATE_NOT_FOUND",
	ErrCodeTemplateValidationFailed:      "TEMPLATE_VALIDATION_FAILED",
	ErrCodeDatabaseConnectionFailed:      "DATABASE_CONNECTION_FAILED",
	ErrCodeQueryExecutionFailed:          "QUERY_EXECUTION_FAILED",
	ErrCodeQueryTimeout:                  "QUERY_TIMEOUT",
	ErrCodeInvalidQueryType:              "INVALID_QUERY_TYPE",
	ErrCodeElasticsearchConnectionFailed: "ELASTICSEARCH_CONNECTION_FAILED",
	ErrCodeSearchQueryFailed:             "SEARCH_QUERY_FAILED",
	ErrCodeSearchTimeout:                 "SEARCH_TIMEOUT",
	ErrCodeIndexNotFound:                 "INDEX_NOT_FOUND",
	ErrCodeInvalidFilterFormat:           "INVALID_FILTER_FORMAT",
	ErrCodeApplicationValidationFailed:   "APPLICATION_VALIDATION_FAILED",
	ErrCodeDatabaseInsertFailed:          "DATABASE_INSERT_FAILED",
	ErrCodeDuplicateApplication:          "DUPLICATE_APPLICATION",
	ErrCodeNotificationSendFailed:        "NOTIFICATION_SEND_FAILED",
	ErrCodeIntentParsingFailed:           "INTENT_PARSING_FAILED",
	ErrCodeIntentAPITimeout:              "INTENT_API_TIMEOUT",
	ErrCodeWebSearchTimeout:              "WEB_SEARCH_TIMEOUT",
	ErrCodeLLMTimeout:                    "LLM_TIMEOUT",
	ErrCodeLLMSynthesisFailed:            "LLM_SYNTHESIS_FAILED",
}



// ConvertToBPMNError converts a StandardError to a BPMNError for Camunda.
func ConvertToBPMNError(stdErr *StandardError) *BPMNError {
	bpmnCode, exists := BPMNErrorMapping[stdErr.Code]
	if !exists {
		bpmnCode = string(stdErr.Code) // Fallback
	}

	retries := GetRetryCount(stdErr.Code)
	if !stdErr.Retryable {
		retries = 0
	}

	return &BPMNError{
		Code:      bpmnCode,
		Message:   stdErr.Message,
		Details:   stdErr.Details,
		Retryable: stdErr.Retryable,
		Retries:   retries,
		ErrorVariables: map[string]interface{}{
			"originalErrorCode": string(stdErr.Code),
			"timestamp":         stdErr.Timestamp.Format(time.RFC3339),
		},
	}
}

// ==========================
// 5. Utility Functions
// ==========================

// IsRetryableErrorCode checks if an error code is retryable.
func IsRetryableErrorCode(code ErrorCode) bool {
	return GetRetryCount(code) > 0
}

// ==========================
// 6. Validation Error Constructors (GAP #1)
// ==========================

// NewValidationError creates a non-retryable validation error
func NewValidationError(field, message string) *StandardError {
	return &StandardError{
		ID:        uuid.New().String(),
		Code:      ErrorCode("VALIDATION_FAILED"),
		Message:   fmt.Sprintf("Validation failed for field '%s'", field),
		Details:   message,
		Retryable: false,
		Metadata: map[string]interface{}{
			"field": field,
		},
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewSQLInjectionError creates a non-retryable SQL injection detection error
func NewSQLInjectionError(field, value string) *StandardError {
	return &StandardError{
		ID:        uuid.New().String(),
		Code:      ErrorCode("SQL_INJECTION_DETECTED"),
		Message:   "Potential SQL injection detected",
		Details:   fmt.Sprintf("Field '%s' contains unsafe SQL characters", field),
		Retryable: false,
		Metadata: map[string]interface{}{
			"field":         field,
			"sanitizedValue": "[REDACTED]", // Never log actual malicious value
		},
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewNoSQLInjectionError creates a non-retryable NoSQL injection detection error
func NewNoSQLInjectionError(field, operator string) *StandardError {
	return &StandardError{
		ID:        uuid.New().String(),
		Code:      ErrorCode("NOSQL_INJECTION_DETECTED"),
		Message:   "Potential NoSQL injection detected",
		Details:   fmt.Sprintf("Field '%s' contains unsafe operator: %s", field, operator),
		Retryable: false,
		Metadata: map[string]interface{}{
			"field":    field,
			"operator": operator,
		},
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewInvalidUUIDError creates a non-retryable UUID format error
func NewInvalidUUIDError(field, value string) *StandardError {
	return &StandardError{
		ID:        uuid.New().String(),
		Code:      ErrorCode("INVALID_UUID_FORMAT"),
		Message:   fmt.Sprintf("Invalid UUID format for field '%s'", field),
		Details:   "Expected format: 8-4-4-4-12 hexadecimal characters",
		Retryable: false,
		Metadata: map[string]interface{}{
			"field":        field,
			"providedValue": value,
		},
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewRequiredFieldError creates a non-retryable required field error
func NewRequiredFieldError(field string) *StandardError {
	return &StandardError{
		ID:        uuid.New().String(),
		Code:      ErrorCode("REQUIRED_FIELD_MISSING"),
		Message:   fmt.Sprintf("Required field '%s' is missing", field),
		Details:   "This field must be provided",
		Retryable: false,
		Metadata: map[string]interface{}{
			"field": field,
		},
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewStringTooLongError creates a non-retryable string length error
func NewStringTooLongError(field string, maxLength, actualLength int) *StandardError {
	return &StandardError{
		ID:        uuid.New().String(),
		Code:      ErrorCode("STRING_TOO_LONG"),
		Message:   fmt.Sprintf("Field '%s' exceeds maximum length", field),
		Details:   fmt.Sprintf("Maximum length: %d, provided: %d", maxLength, actualLength),
		Retryable: false,
		Metadata: map[string]interface{}{
			"field":        field,
			"maxLength":    maxLength,
			"actualLength": actualLength,
		},
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

// NewArrayTooLargeError creates a non-retryable array size error
func NewArrayTooLargeError(field string, maxSize, actualSize int) *StandardError {
	return &StandardError{
		ID:        uuid.New().String(),
		Code:      ErrorCode("ARRAY_TOO_LARGE"),
		Message:   fmt.Sprintf("Array '%s' exceeds maximum size", field),
		Details:   fmt.Sprintf("Maximum size: %d, provided: %d", maxSize, actualSize),
		Retryable: false,
		Metadata: map[string]interface{}{
			"field":      field,
			"maxSize":    maxSize,
			"actualSize": actualSize,
		},
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}