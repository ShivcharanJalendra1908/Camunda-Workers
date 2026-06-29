package models

import (
	"fmt"
	"time"
)

// ============================================================================
// GUEST IDENTITY
// ============================================================================

// GuestIdentity represents the computed identity of a guest user.
type GuestIdentity struct {
	CompositeKey string `json:"compositeKey"` // SHA256 hash used as Redis key suffix
	FallbackIP   string `json:"fallbackIP"`   // ip/24 used when no device token
	HasToken     bool   `json:"hasToken"`     // whether device token was provided
	HasSalt      bool   `json:"hasSalt"`      // whether device salt was provided
}

// ============================================================================
// SESSION RESULT
// ============================================================================

// SessionResult represents the outcome of a guest AI session check.
type SessionResult struct {
	SessionID       string `json:"sessionID"`       // gsess_ prefix + 24 hex chars
	QueriesUsed     int    `json:"queriesUsed"`     // queries used in current session
	QueriesLimit    int    `json:"queriesLimit"`    // max queries per session (6)
	CreditsUsed     int    `json:"creditsUsed"`     // monthly credits used
	CreditsLimit    int    `json:"creditsLimit"`    // max credits per month (3)
	CreditsRemaining int   `json:"creditsRemaining"` // credits left
	IsNewSession    bool   `json:"isNewSession"`    // whether this was a new session
}

// ============================================================================
// QUOTA EXHAUSTED ERROR
// ============================================================================

// QuotaExhaustedError is returned when a guest has used all credits in their window.
type QuotaExhaustedError struct {
	Code           string    `json:"code"`            // SIGNUP_REQUIRED
	Reason         string    `json:"reason"`          // monthly_credits_exhausted
	CreditsUsed    int       `json:"credits_used"`
	CreditsLimit   int       `json:"credits_limit"`
	ResetAt        time.Time `json:"reset_at"`
	ResetInSeconds int       `json:"reset_in_seconds"`
	Message        string    `json:"message"`
	SignupURL      string    `json:"signup_url"`
}

func (e *QuotaExhaustedError) Error() string {
	return fmt.Sprintf("credits exhausted: %d/%d used", e.CreditsUsed, e.CreditsLimit)
}

// ============================================================================
// AUDIT EVENT
// ============================================================================

// GuestAuditEvent represents an audit log entry for guest activity.
type GuestAuditEvent struct {
	ID            int64     `json:"id" db:"id"`
	SessionID     string    `json:"sessionID" db:"session_id"`
	CompositeKey  string    `json:"compositeKey" db:"composite_key"`
	Action        string    `json:"action" db:"action"`           // "query", "new_session", "credit_consumed", "blocked"
	RouteGroup    string    `json:"routeGroup" db:"route_group"`   // "ai", "contact", "forms", etc.
	QueriesUsed   int       `json:"queriesUsed" db:"queries_used"`
	CreditsUsed   int       `json:"creditsUsed" db:"credits_used"`
	AnomalyFlags  []string  `json:"anomalyFlags" db:"anomaly_flags"`
	Blocked       bool      `json:"blocked" db:"blocked"`
	BlockReason   string    `json:"blockReason,omitempty" db:"block_reason"`
	CreatedAt     time.Time `json:"createdAt" db:"created_at"`
}

// ============================================================================
// CONSTANTS
// ============================================================================

const (
	SessionPrefix       = "gsess_"
	SessionIDLength     = 24 // hex chars after prefix
	CompositeKeyContext  = "guestIdentity"
	SessionResultContext = "sessionResult"
)

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// NewQuotaExhaustedError creates a new QuotaExhaustedError with proper fields.
// resetInSeconds is the actual remaining TTL from Redis (rolling window), not a calendar computation.
func NewQuotaExhaustedError(creditsUsed, creditsLimit, resetInSeconds int) *QuotaExhaustedError {
	resetAt := time.Now().UTC().Add(time.Duration(resetInSeconds) * time.Second)

	return &QuotaExhaustedError{
		Code:           "SIGNUP_REQUIRED",
		Reason:         "monthly_credits_exhausted",
		CreditsUsed:    creditsUsed,
		CreditsLimit:   creditsLimit,
		ResetAt:        resetAt,
		ResetInSeconds: resetInSeconds,
		Message:        fmt.Sprintf("You've used all %d free sessions. Sign up for unlimited access.", creditsLimit),
		SignupURL:      "/register",
	}
}
