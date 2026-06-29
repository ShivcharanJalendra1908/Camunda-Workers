package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuotaExhaustedError_JSON_SnakeCase(t *testing.T) {
	resetInSec := 2592000
	err := NewQuotaExhaustedError(3, 3, resetInSec)

	data, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	// All field names MUST be snake_case
	_, hasCreditsUsed := raw["credits_used"]
	_, hasCreditsUsedCamel := raw["creditsUsed"]
	assert.True(t, hasCreditsUsed, "must have credits_used (snake_case)")
	assert.False(t, hasCreditsUsedCamel, "must NOT have creditsUsed (camelCase)")

	_, hasCreditsLimit := raw["credits_limit"]
	_, hasCreditsLimitCamel := raw["creditsLimit"]
	assert.True(t, hasCreditsLimit, "must have credits_limit")
	assert.False(t, hasCreditsLimitCamel, "must NOT have creditsLimit")

	_, hasResetAt := raw["reset_at"]
	_, hasResetAtCamel := raw["resetAt"]
	assert.True(t, hasResetAt, "must have reset_at")
	assert.False(t, hasResetAtCamel, "must NOT have resetAt")

	_, hasSignupURL := raw["signup_url"]
	_, hasSignupURLCamel := raw["signupUrl"]
	assert.True(t, hasSignupURL, "must have signup_url")
	assert.False(t, hasSignupURLCamel, "must NOT have signupUrl")

	// These are always snake_case (no ambiguity)
	_, hasResetInSec := raw["reset_in_seconds"]
	assert.True(t, hasResetInSec, "must have reset_in_seconds")
	_, hasCode := raw["code"]
	assert.True(t, hasCode, "must have code")
	_, hasReason := raw["reason"]
	assert.True(t, hasReason, "must have reason")
	_, hasMessage := raw["message"]
	assert.True(t, hasMessage, "must have message")
}

func TestQuotaExhaustedError_FieldValues(t *testing.T) {
	err := NewQuotaExhaustedError(5, 3, 1000)

	assert.Equal(t, "SIGNUP_REQUIRED", err.Code)
	assert.Equal(t, "monthly_credits_exhausted", err.Reason)
	assert.Equal(t, 5, err.CreditsUsed)
	assert.Equal(t, 3, err.CreditsLimit)
	assert.Equal(t, 1000, err.ResetInSeconds)
	assert.Equal(t, "/register", err.SignupURL)
	assert.Contains(t, err.Message, "all 3 free sessions")
}

func TestQuotaExhaustedError_ResetAtIsFuture(t *testing.T) {
	err := NewQuotaExhaustedError(3, 3, 2592000)
	now := time.Now().UTC()
	assert.True(t, err.ResetAt.After(now), "reset_at should be in the future")
}

func TestQuotaExhaustedError_ErrorString(t *testing.T) {
	err := NewQuotaExhaustedError(3, 3, 1000)
	assert.Contains(t, err.Error(), "3/3 used")
}

func TestGuestIdentity_Fields(t *testing.T) {
	identity := &GuestIdentity{
		CompositeKey: "abc123def4567890",
		FallbackIP:   "192.168.1",
		HasToken:     true,
		HasSalt:      true,
	}

	data, err := json.Marshal(identity)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Equal(t, "abc123def4567890", raw["compositeKey"])
	assert.Equal(t, "192.168.1", raw["fallbackIP"])
	assert.Equal(t, true, raw["hasToken"])
	assert.Equal(t, true, raw["hasSalt"])
}

func TestSessionResult_Fields(t *testing.T) {
	result := &SessionResult{
		SessionID:       "gsess_abcdef1234567890abcdef12",
		QueriesUsed:     2,
		QueriesLimit:    6,
		CreditsUsed:     1,
		CreditsLimit:    3,
		CreditsRemaining: 2,
		IsNewSession:    true,
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Equal(t, "gsess_abcdef1234567890abcdef12", raw["sessionID"])
	assert.Equal(t, float64(2), raw["queriesUsed"])
	assert.Equal(t, float64(6), raw["queriesLimit"])
	assert.Equal(t, float64(1), raw["creditsUsed"])
	assert.Equal(t, float64(3), raw["creditsLimit"])
	assert.Equal(t, float64(2), raw["creditsRemaining"])
	assert.Equal(t, true, raw["isNewSession"])
}

func TestGuestAuditEvent_RouteGroup(t *testing.T) {
	event := &GuestAuditEvent{
		SessionID:    "gsess_test123",
		CompositeKey: "key123",
		Action:       "query",
		RouteGroup:   "ai",
		QueriesUsed:  2,
		CreditsUsed:  1,
		Blocked:      false,
		CreatedAt:    time.Now(),
	}

	data, err := json.Marshal(event)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Equal(t, "ai", raw["routeGroup"])
}
