package apierrors

import "net/http"

// AppError represents a typed API error for auth and future API-wide handling.
// Isolated from internal/common/errors (used for Camunda workers).
type AppError struct {
	Code       string // Machine-readable error code (e.g., OAUTH_STATE_MISMATCH)
	Message    string // User-safe message (never expose internal details)
	StatusCode int    // HTTP status code for JSON responses
	RedirectTo string // If non-empty, redirect to this URL instead of returning JSON
	LogMessage string // Internal-only log message (never sent to users)
}

// Auth-flow specific sentinel errors
var (
	ErrOAuthStateMismatch = &AppError{
		Code:       "OAUTH_STATE_MISMATCH",
		Message:    "Your session has expired. Please try logging in again.",
		StatusCode: http.StatusBadRequest,
		RedirectTo: "/login?error=session_expired",
		LogMessage: "OAuth state parameter mismatch or expired",
	}

	ErrCodeExchangeFailed = &AppError{
		Code:       "CODE_EXCHANGE_FAILED",
		Message:    "Authentication failed. Please try again.",
		StatusCode: http.StatusUnauthorized,
		RedirectTo: "/login?error=auth_failed",
		LogMessage: "Failed to exchange OAuth code for token",
	}

	ErrSessionCreateFailed = &AppError{
		Code:       "SESSION_CREATE_FAILED",
		Message:    "Could not establish your session. Please try again.",
		StatusCode: http.StatusInternalServerError,
		RedirectTo: "/login?error=server_error",
		LogMessage: "Failed to create user session after successful authentication",
	}

	ErrAccountDisabled = &AppError{
		Code:       "ACCOUNT_DISABLED",
		Message:    "Your account has been disabled. Contact support for assistance.",
		StatusCode: http.StatusForbidden,
		RedirectTo: "/login?error=account_disabled",
		LogMessage: "User account is disabled in Keycloak",
	}

	ErrOAuthCodeMissing = &AppError{
		Code:       "OAUTH_CODE_MISSING",
		Message:    "Authentication code is missing. Please try logging in again.",
		StatusCode: http.StatusBadRequest,
		RedirectTo: "/login?error=missing_code",
		LogMessage: "OAuth code or state parameter missing from callback",
	}

	ErrServiceUnavailable = &AppError{
		Code:       "SERVICE_UNAVAILABLE",
		Message:    "Authentication service is temporarily unavailable. Please try again later.",
		StatusCode: http.StatusServiceUnavailable,
		RedirectTo: "/login?error=service_unavailable",
		LogMessage: "Redis or workflow service unavailable",
	}

	ErrTimeout = &AppError{
		Code:       "AUTH_TIMEOUT",
		Message:    "Authentication timed out. Please try again.",
		StatusCode: http.StatusGatewayTimeout,
		RedirectTo: "/login?error=timeout",
		LogMessage: "Workflow response timed out",
	}

	ErrInternalError = &AppError{
		Code:       "INTERNAL_ERROR",
		Message:    "An unexpected error occurred. Please try again.",
		StatusCode: http.StatusInternalServerError,
		RedirectTo: "/login?error=internal_error",
		LogMessage: "Unexpected internal error in auth flow",
	}

	ErrUnauthenticated = &AppError{
		Code:       "UNAUTHENTICATED",
		Message:    "Authentication required. Please log in.",
		StatusCode: http.StatusUnauthorized,
		LogMessage: "Missing or invalid session cookie",
	}
)

// Error implements the error interface for AppError.
func (e *AppError) Error() string {
	return e.Code + ": " + e.Message
}
