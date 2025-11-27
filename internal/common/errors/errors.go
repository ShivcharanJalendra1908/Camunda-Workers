// Package errors provides standardized error handling for BPMN workflow integration.
package errors

import (
	"fmt"
	"strings"
	"time"
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
	Code      ErrorCode              `json:"code"`
	Message   string                 `json:"message"`
	Details   string                 `json:"details,omitempty"`
	Retryable bool                   `json:"retryable"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

func (e *StandardError) Error() string {
	return fmt.Sprintf("StandardError[%s]: %s", e.Code, e.Message)
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
		Code:      ErrCodeSubscriptionInvalid,
		Message:   "Invalid or not found subscription",
		Details:   details,
		Retryable: false,
		Timestamp: time.Now().UTC(),
	}
}

// NewSubscriptionExpiredError creates a non-retryable subscription error.
func NewSubscriptionExpiredError(details string) *StandardError {
	return &StandardError{
		Code:      ErrCodeSubscriptionExpired,
		Message:   "Subscription has expired",
		Details:   details,
		Retryable: false,
		Timestamp: time.Now().UTC(),
	}
}

// NewSubscriptionCheckFailedError creates a retryable database error.
func NewSubscriptionCheckFailedError(err error) *StandardError {
	return &StandardError{
		Code:      ErrCodeSubscriptionCheckFailed,
		Message:   "Database error during subscription check",
		Details:   err.Error(),
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

// NewTemplateNotFoundError creates a non-retryable template error.
func NewTemplateNotFoundError(templateID string) *StandardError {
	return &StandardError{
		Code:      ErrCodeTemplateNotFound,
		Message:   "Template not found in registry",
		Details:   fmt.Sprintf("templateId: %s", templateID),
		Retryable: false,
		Timestamp: time.Now().UTC(),
	}
}

// NewTemplateValidationFailedError creates a non-retryable template validation error.
func NewTemplateValidationFailedError(details string) *StandardError {
	return &StandardError{
		Code:      ErrCodeTemplateValidationFailed,
		Message:   "Data validation failed for template",
		Details:   details,
		Retryable: false,
		Timestamp: time.Now().UTC(),
	}
}

// NewDatabaseConnectionFailedError creates a retryable database connection error.
func NewDatabaseConnectionFailedError(err error) *StandardError {
	return &StandardError{
		Code:      ErrCodeDatabaseConnectionFailed,
		Message:   "Database connection error",
		Details:   err.Error(),
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

// NewQueryExecutionFailedError creates a retryable query execution error.
func NewQueryExecutionFailedError(queryType string, err error) *StandardError {
	return &StandardError{
		Code:      ErrCodeQueryExecutionFailed,
		Message:   "Database query execution error",
		Details:   fmt.Sprintf("queryType: %s, error: %s", queryType, err.Error()),
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

// NewQueryTimeoutError creates a retryable query timeout error.
func NewQueryTimeoutError(queryType string) *StandardError {
	return &StandardError{
		Code:      ErrCodeQueryTimeout,
		Message:   "Database query timeout",
		Details:   fmt.Sprintf("queryType: %s", queryType),
		Retryable: true,
		Timestamp: time.Now().UTC(),
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
		Code:      ErrCodeElasticsearchConnectionFailed,
		Message:   "Elasticsearch connection error",
		Details:   err.Error(),
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

// NewSearchQueryFailedError creates a retryable search query error.
func NewSearchQueryFailedError(queryType string, err error) *StandardError {
	return &StandardError{
		Code:      ErrCodeSearchQueryFailed,
		Message:   "Elasticsearch query error",
		Details:   fmt.Sprintf("queryType: %s, error: %s", queryType, err.Error()),
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

// NewSearchTimeoutError creates a retryable search timeout error.
func NewSearchTimeoutError(queryType string) *StandardError {
	return &StandardError{
		Code:      ErrCodeSearchTimeout,
		Message:   "Elasticsearch query timeout",
		Details:   fmt.Sprintf("queryType: %s", queryType),
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

// NewIndexNotFoundError creates a non-retryable index not found error.
func NewIndexNotFoundError(indexName string) *StandardError {
	return &StandardError{
		Code:      ErrCodeIndexNotFound,
		Message:   "Elasticsearch index not found",
		Details:   fmt.Sprintf("indexName: %s", indexName),
		Retryable: false,
		Timestamp: time.Now().UTC(),
	}
}

// NewInvalidFilterFormatError creates a non-retryable filter format error.
func NewInvalidFilterFormatError(details string) *StandardError {
	return &StandardError{
		Code:      ErrCodeInvalidFilterFormat,
		Message:   "Invalid filter format",
		Details:   details,
		Retryable: false,
		Timestamp: time.Now().UTC(),
	}
}

// NewApplicationValidationFailedError creates a non-retryable application validation error.
func NewApplicationValidationFailedError(details string) *StandardError {
	return &StandardError{
		Code:      ErrCodeApplicationValidationFailed,
		Message:   "Application data validation failed",
		Details:   details,
		Retryable: false,
		Timestamp: time.Now().UTC(),
	}
}

// NewDatabaseInsertFailedError creates a retryable database insert error.
func NewDatabaseInsertFailedError(err error) *StandardError {
	return &StandardError{
		Code:      ErrCodeDatabaseInsertFailed,
		Message:   "Database insert operation failed",
		Details:   err.Error(),
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

// NewDuplicateApplicationError creates a non-retryable duplicate application error.
func NewDuplicateApplicationError(applicationID string) *StandardError {
	return &StandardError{
		Code:      ErrCodeDuplicateApplication,
		Message:   "Application already exists",
		Details:   fmt.Sprintf("applicationId: %s", applicationID),
		Retryable: false,
		Timestamp: time.Now().UTC(),
	}
}

// NewNotificationSendFailedError creates a retryable notification send error.
func NewNotificationSendFailedError(notificationType string, err error) *StandardError {
	return &StandardError{
		Code:      ErrCodeNotificationSendFailed,
		Message:   "Notification delivery failed",
		Details:   fmt.Sprintf("type: %s, error: %s", notificationType, err.Error()),
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

// NewIntentParsingFailedError creates a retryable intent parsing error.
func NewIntentParsingFailedError(err error) *StandardError {
	return &StandardError{
		Code:      ErrCodeIntentParsingFailed,
		Message:   "Intent parsing API error",
		Details:   err.Error(),
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

// NewIntentAPITimeoutError creates a retryable intent API timeout error.
func NewIntentAPITimeoutError() *StandardError {
	return &StandardError{
		Code:      ErrCodeIntentAPITimeout,
		Message:   "Intent parsing API timeout",
		Details:   "API call exceeded timeout threshold",
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

// NewWebSearchTimeoutError creates a non-retryable (returns empty) web search timeout error.
func NewWebSearchTimeoutError() *StandardError {
	return &StandardError{
		Code:      ErrCodeWebSearchTimeout,
		Message:   "Web search API timeout",
		Details:   "Search call exceeded 3 second timeout",
		Retryable: false, // Per doc: return empty, don't retry
		Timestamp: time.Now().UTC(),
	}
}

// NewLLMTimeoutError creates a retryable LLM timeout error.
func NewLLMTimeoutError() *StandardError {
	return &StandardError{
		Code:      ErrCodeLLMTimeout,
		Message:   "LLM synthesis timeout",
		Details:   "LLM call exceeded 5 second timeout",
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

// NewLLMSynthesisFailedError creates a retryable LLM synthesis error.
func NewLLMSynthesisFailedError(err error) *StandardError {
	return &StandardError{
		Code:      ErrCodeLLMSynthesisFailed,
		Message:   "LLM synthesis API error",
		Details:   err.Error(),
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

// Generic constructors

func NewBusinessRuleError(message, details string) *StandardError {
	return &StandardError{
		Code:      "BUSINESS_RULE_VIOLATION",
		Message:   message,
		Details:   details,
		Retryable: false,
		Timestamp: time.Now().UTC(),
	}
}

func NewExternalServiceError(service string, err error) *StandardError {
	return &StandardError{
		Code:      "EXTERNAL_SERVICE_ERROR",
		Message:   fmt.Sprintf("External service '%s' error", service),
		Details:   err.Error(),
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

func NewTimeoutError(service string, err error) *StandardError {
	return &StandardError{
		Code:      "TIMEOUT_ERROR",
		Message:   fmt.Sprintf("Service '%s' timeout", service),
		Details:   err.Error(),
		Retryable: true,
		Timestamp: time.Now().UTC(),
	}
}

func NewResourceNotFoundError(service, details string) *StandardError {
	return &StandardError{
		Code:      "RESOURCE_NOT_FOUND",
		Message:   fmt.Sprintf("Resource not found in %s", service),
		Details:   details,
		Retryable: false,
		Timestamp: time.Now().UTC(),
	}
}

func NewAuthenticationError(details string) *StandardError {
	return &StandardError{
		Code:      "AUTHENTICATION_ERROR",
		Message:   "Authentication failed",
		Details:   details,
		Retryable: false,
		Timestamp: time.Now().UTC(),
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

// GetRetryCount returns the recommended retry count based on Appendix B.
func GetRetryCount(code ErrorCode) int {
	switch code {
	case ErrCodeSubscriptionCheckFailed,
		ErrCodeDatabaseConnectionFailed,
		ErrCodeQueryExecutionFailed,
		ErrCodeElasticsearchConnectionFailed,
		ErrCodeSearchQueryFailed,
		ErrCodeDatabaseInsertFailed,
		ErrCodeNotificationSendFailed,
		ErrCodeIntentParsingFailed,
		ErrCodeLLMSynthesisFailed:
		return 3 // Retryable technical errors

	case ErrCodeQueryTimeout,
		ErrCodeSearchTimeout,
		ErrCodeIntentAPITimeout:
		return 2 // Partial retry for timeouts

	case ErrCodeLLMTimeout:
		return 1 // As per BPMN boundary event

	default:
		return 0 // Business errors: no retry
	}
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

// GetErrorCategory returns the category of the error code.
func GetErrorCategory(code ErrorCode) string {
	codeStr := string(code)
	switch {
	case strings.Contains(codeStr, "SUBSCRIPTION"):
		return "AUTH/SUBSCRIPTION"
	case strings.Contains(codeStr, "TEMPLATE"):
		return "TEMPLATE"
	case strings.Contains(codeStr, "DATABASE") || strings.Contains(codeStr, "QUERY"):
		return "DATABASE"
	case strings.Contains(codeStr, "ELASTICSEARCH") || strings.Contains(codeStr, "SEARCH"):
		return "SEARCH"
	case strings.Contains(codeStr, "NOTIFICATION"):
		return "NOTIFICATION"
	case strings.Contains(codeStr, "INTENT") || strings.Contains(codeStr, "LLM") || strings.Contains(codeStr, "WEB"):
		return "AI"
	case strings.Contains(codeStr, "INVALID") || strings.Contains(codeStr, "VALIDATION"):
		return "VALIDATION"
	default:
		return "OTHER"
	}
}

// // Package errors provides standardized error handling for BPMN workflow integration.
// package errors

// import (
// 	"fmt"
// 	"strings"
// 	"time"
// )

// // ==========================
// // 1. Standard Error Types
// // ==========================

// // ErrorCode represents standardized internal error codes.
// type ErrorCode string

// // Business Rule / Subscription Errors (from requirements Appendix B)
// const (
// 	ErrCodeSubscriptionInvalid     ErrorCode = "SUBSCRIPTION_INVALID"
// 	ErrCodeSubscriptionExpired     ErrorCode = "SUBSCRIPTION_EXPIRED"
// 	ErrCodeSubscriptionCheckFailed ErrorCode = "SUBSCRIPTION_CHECK_FAILED"

// 	ErrCodeTemplateNotFound         ErrorCode = "TEMPLATE_NOT_FOUND"
// 	ErrCodeTemplateValidationFailed ErrorCode = "TEMPLATE_VALIDATION_FAILED"

// 	ErrCodeDatabaseConnectionFailed ErrorCode = "DATABASE_CONNECTION_FAILED"
// 	ErrCodeQueryExecutionFailed     ErrorCode = "QUERY_EXECUTION_FAILED"
// 	ErrCodeQueryTimeout             ErrorCode = "QUERY_TIMEOUT"
// 	ErrCodeInvalidQueryType         ErrorCode = "INVALID_QUERY_TYPE"

// 	ErrCodeElasticsearchConnectionFailed ErrorCode = "ELASTICSEARCH_CONNECTION_FAILED"
// 	ErrCodeSearchQueryFailed             ErrorCode = "SEARCH_QUERY_FAILED"
// 	ErrCodeSearchTimeout                 ErrorCode = "SEARCH_TIMEOUT"
// 	ErrCodeIndexNotFound                 ErrorCode = "INDEX_NOT_FOUND"

// 	ErrCodeInvalidFilterFormat         ErrorCode = "INVALID_FILTER_FORMAT"
// 	ErrCodeApplicationValidationFailed ErrorCode = "APPLICATION_VALIDATION_FAILED"

// 	ErrCodeDatabaseInsertFailed ErrorCode = "DATABASE_INSERT_FAILED"
// 	ErrCodeDuplicateApplication ErrorCode = "DUPLICATE_APPLICATION"

// 	ErrCodeNotificationSendFailed ErrorCode = "NOTIFICATION_SEND_FAILED"

// 	ErrCodeIntentParsingFailed ErrorCode = "INTENT_PARSING_FAILED"
// 	ErrCodeIntentAPITimeout    ErrorCode = "INTENT_API_TIMEOUT"
// 	ErrCodeWebSearchTimeout    ErrorCode = "WEB_SEARCH_TIMEOUT"
// 	ErrCodeLLMTimeout          ErrorCode = "LLM_TIMEOUT"
// 	ErrCodeLLMSynthesisFailed  ErrorCode = "LLM_SYNTHESIS_FAILED"
// )

// // StandardError represents a structured application error.
// type StandardError struct {
// 	Code      ErrorCode              `json:"code"`
// 	Message   string                 `json:"message"`
// 	Details   string                 `json:"details,omitempty"`
// 	Retryable bool                   `json:"retryable"`
// 	Metadata  map[string]interface{} `json:"metadata,omitempty"`
// 	Timestamp time.Time              `json:"timestamp"`
// }

// func (e *StandardError) Error() string {
// 	return fmt.Sprintf("StandardError[%s]: %s", e.Code, e.Message)
// }

// // ==========================
// // 2. BPMN Error Integration
// // ==========================

// // BPMNError represents an error that can be thrown to the Camunda workflow engine.
// type BPMNError struct {
// 	Code           string                 `json:"code"`
// 	Message        string                 `json:"message"`
// 	Details        string                 `json:"details,omitempty"`
// 	Retryable      bool                   `json:"retryable"`
// 	Retries        int                    `json:"retries"`
// 	ErrorVariables map[string]interface{} `json:"errorVariables,omitempty"`
// }

// func (e *BPMNError) Error() string {
// 	return fmt.Sprintf("BPMNError[%s]: %s", e.Code, e.Message)
// }

// // ToErrorVariables returns a map suitable for setting Camunda job fail variables.
// func (e *BPMNError) ToErrorVariables() map[string]interface{} {
// 	vars := map[string]interface{}{
// 		"errorCode":    e.Code,
// 		"errorMessage": e.Message,
// 		"errorDetails": e.Details,
// 		"retryable":    e.Retryable,
// 	}

// 	if e.ErrorVariables != nil {
// 		for k, v := range e.ErrorVariables {
// 			vars[k] = v
// 		}
// 	}

// 	return vars
// }

// // ==========================
// // 3. Error Constructors
// // ==========================

// // NewSubscriptionInvalidError creates a non-retryable subscription error.
// func NewSubscriptionInvalidError(details string) *StandardError {
// 	return &StandardError{
// 		Code:      ErrCodeSubscriptionInvalid,
// 		Message:   "Invalid or not found subscription",
// 		Details:   details,
// 		Retryable: false,
// 		Timestamp: time.Now().UTC(),
// 	}
// }

// // NewSubscriptionExpiredError creates a non-retryable subscription error.
// func NewSubscriptionExpiredError(details string) *StandardError {
// 	return &StandardError{
// 		Code:      ErrCodeSubscriptionExpired,
// 		Message:   "Subscription has expired",
// 		Details:   details,
// 		Retryable: false,
// 		Timestamp: time.Now().UTC(),
// 	}
// }

// // NewSubscriptionCheckFailedError creates a retryable database error.
// func NewSubscriptionCheckFailedError(err error) *StandardError {
// 	return &StandardError{
// 		Code:      ErrCodeSubscriptionCheckFailed,
// 		Message:   "Database error during subscription check",
// 		Details:   err.Error(),
// 		Retryable: true,
// 		Timestamp: time.Now().UTC(),
// 	}
// }

// // Generic constructors (add more as needed)

// func NewBusinessRuleError(message, details string) *StandardError {
// 	return &StandardError{
// 		Code:      "BUSINESS_RULE_VIOLATION",
// 		Message:   message,
// 		Details:   details,
// 		Retryable: false,
// 		Timestamp: time.Now().UTC(),
// 	}
// }

// func NewExternalServiceError(service string, err error) *StandardError {
// 	return &StandardError{
// 		Code:      "EXTERNAL_SERVICE_ERROR",
// 		Message:   fmt.Sprintf("External service '%s' error", service),
// 		Details:   err.Error(),
// 		Retryable: true,
// 		Timestamp: time.Now().UTC(),
// 	}
// }

// func NewTimeoutError(service string, err error) *StandardError {
// 	return &StandardError{
// 		Code:      "TIMEOUT_ERROR",
// 		Message:   fmt.Sprintf("Service '%s' timeout", service),
// 		Details:   err.Error(),
// 		Retryable: true,
// 		Timestamp: time.Now().UTC(),
// 	}
// }

// func NewResourceNotFoundError(service, details string) *StandardError {
// 	return &StandardError{
// 		Code:      "RESOURCE_NOT_FOUND",
// 		Message:   fmt.Sprintf("Resource not found in %s", service),
// 		Details:   details,
// 		Retryable: false,
// 		Timestamp: time.Now().UTC(),
// 	}
// }

// func NewAuthenticationError(details string) *StandardError {
// 	return &StandardError{
// 		Code:      "AUTHENTICATION_ERROR",
// 		Message:   "Authentication failed",
// 		Details:   details,
// 		Retryable: false,
// 		Timestamp: time.Now().UTC(),
// 	}
// }

// // ==========================
// // 4. Error Conversion to BPMN
// // ==========================

// // BPMNErrorMapping maps internal error codes to BPMN error codes (same as internal).
// // Per Appendix B, these are identical.
// var BPMNErrorMapping = map[ErrorCode]string{
// 	ErrCodeSubscriptionInvalid:           "SUBSCRIPTION_INVALID",
// 	ErrCodeSubscriptionExpired:           "SUBSCRIPTION_EXPIRED",
// 	ErrCodeSubscriptionCheckFailed:       "SUBSCRIPTION_CHECK_FAILED",
// 	ErrCodeTemplateNotFound:              "TEMPLATE_NOT_FOUND",
// 	ErrCodeTemplateValidationFailed:      "TEMPLATE_VALIDATION_FAILED",
// 	ErrCodeDatabaseConnectionFailed:      "DATABASE_CONNECTION_FAILED",
// 	ErrCodeQueryExecutionFailed:          "QUERY_EXECUTION_FAILED",
// 	ErrCodeQueryTimeout:                  "QUERY_TIMEOUT",
// 	ErrCodeInvalidQueryType:              "INVALID_QUERY_TYPE",
// 	ErrCodeElasticsearchConnectionFailed: "ELASTICSEARCH_CONNECTION_FAILED",
// 	ErrCodeSearchQueryFailed:             "SEARCH_QUERY_FAILED",
// 	ErrCodeSearchTimeout:                 "SEARCH_TIMEOUT",
// 	ErrCodeIndexNotFound:                 "INDEX_NOT_FOUND",
// 	ErrCodeInvalidFilterFormat:           "INVALID_FILTER_FORMAT",
// 	ErrCodeApplicationValidationFailed:   "APPLICATION_VALIDATION_FAILED",
// 	ErrCodeDatabaseInsertFailed:          "DATABASE_INSERT_FAILED",
// 	ErrCodeDuplicateApplication:          "DUPLICATE_APPLICATION",
// 	ErrCodeNotificationSendFailed:        "NOTIFICATION_SEND_FAILED",
// 	ErrCodeIntentParsingFailed:           "INTENT_PARSING_FAILED",
// 	ErrCodeIntentAPITimeout:              "INTENT_API_TIMEOUT",
// 	ErrCodeWebSearchTimeout:              "WEB_SEARCH_TIMEOUT",
// 	ErrCodeLLMTimeout:                    "LLM_TIMEOUT",
// 	ErrCodeLLMSynthesisFailed:            "LLM_SYNTHESIS_FAILED",
// }

// // GetRetryCount returns the recommended retry count based on Appendix B.
// func GetRetryCount(code ErrorCode) int {
// 	switch code {
// 	case ErrCodeSubscriptionCheckFailed,
// 		ErrCodeDatabaseConnectionFailed,
// 		ErrCodeQueryExecutionFailed,
// 		ErrCodeElasticsearchConnectionFailed,
// 		ErrCodeSearchQueryFailed,
// 		ErrCodeDatabaseInsertFailed,
// 		ErrCodeNotificationSendFailed,
// 		ErrCodeIntentParsingFailed,
// 		ErrCodeLLMSynthesisFailed:
// 		return 3 // Retryable technical errors

// 	case ErrCodeQueryTimeout,
// 		ErrCodeSearchTimeout,
// 		ErrCodeIntentAPITimeout:
// 		return 2 // Partial retry for timeouts

// 	case ErrCodeLLMTimeout:
// 		return 1 // As per BPMN boundary event

// 	default:
// 		return 0 // Business errors: no retry
// 	}
// }

// // ConvertToBPMNError converts a StandardError to a BPMNError for Camunda.
// func ConvertToBPMNError(stdErr *StandardError) *BPMNError {
// 	bpmnCode, exists := BPMNErrorMapping[stdErr.Code]
// 	if !exists {
// 		bpmnCode = string(stdErr.Code) // Fallback
// 	}

// 	retries := GetRetryCount(stdErr.Code)
// 	if !stdErr.Retryable {
// 		retries = 0
// 	}

// 	return &BPMNError{
// 		Code:      bpmnCode,
// 		Message:   stdErr.Message,
// 		Details:   stdErr.Details,
// 		Retryable: stdErr.Retryable,
// 		Retries:   retries,
// 		ErrorVariables: map[string]interface{}{
// 			"originalErrorCode": string(stdErr.Code),
// 			"timestamp":         stdErr.Timestamp.Format(time.RFC3339),
// 		},
// 	}
// }

// // ==========================
// // 5. Utility Functions
// // ==========================

// // IsRetryableErrorCode checks if an error code is retryable.
// func IsRetryableErrorCode(code ErrorCode) bool {
// 	return GetRetryCount(code) > 0
// }

// // GetErrorCategory returns the category of the error code.
// func GetErrorCategory(code ErrorCode) string {
// 	codeStr := string(code)
// 	switch {
// 	case strings.Contains(codeStr, "SUBSCRIPTION"):
// 		return "AUTH/SUBSCRIPTION"
// 	case strings.Contains(codeStr, "TEMPLATE"):
// 		return "TEMPLATE"
// 	case strings.Contains(codeStr, "DATABASE") || strings.Contains(codeStr, "QUERY"):
// 		return "DATABASE"
// 	case strings.Contains(codeStr, "ELASTICSEARCH") || strings.Contains(codeStr, "SEARCH"):
// 		return "SEARCH"
// 	case strings.Contains(codeStr, "NOTIFICATION"):
// 		return "NOTIFICATION"
// 	case strings.Contains(codeStr, "INTENT") || strings.Contains(codeStr, "LLM") || strings.Contains(codeStr, "WEB"):
// 		return "AI"
// 	case strings.Contains(codeStr, "INVALID") || strings.Contains(codeStr, "VALIDATION"):
// 		return "VALIDATION"
// 	default:
// 		return "OTHER"
// 	}
// }
