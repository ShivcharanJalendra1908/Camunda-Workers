package database

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	t.Cleanup(func() {
		client.Close()
		mr.Close()
	})

	return client, mr
}

// ============================================================================
// QUOTA INCR SCRIPT TESTS
// ============================================================================

func TestQuotaIncr_FirstIncrement(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	key := "quota:ai:test-composite-key"

	result, err := scripts.QuotaIncr(ctx, *client, key, 3, 2592000)
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.CreditsUsed)
	assert.True(t, result.Allowed, "first increment should be allowed")
}

func TestQuotaIncr_WithinLimit(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	key := "quota:ai:test-composite-key"

	for i := 1; i <= 3; i++ {
		result, err := scripts.QuotaIncr(ctx, *client, key, 3, 2592000)
		require.NoError(t, err)
		assert.Equal(t, int64(i), result.CreditsUsed)
		assert.True(t, result.Allowed, "increment %d should be allowed", i)
	}
}

func TestQuotaIncr_AtLimit(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	key := "quota:ai:test-composite-key"

	// Use up all 3 credits
	for i := 0; i < 3; i++ {
		result, err := scripts.QuotaIncr(ctx, *client, key, 3, 2592000)
		require.NoError(t, err)
		assert.True(t, result.Allowed)
	}

	// 4th request: current(3) >= limit(3), returns {3, 0} without incrementing
	result, err := scripts.QuotaIncr(ctx, *client, key, 3, 2592000)
	require.NoError(t, err)
	assert.Equal(t, int64(3), result.CreditsUsed, "returns current value without incrementing when at limit")
	assert.False(t, result.Allowed, "should deny at limit")
}

func TestQuotaIncr_OverLimit(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	key := "quota:ai:test-composite-key"

	// Use up all 3 credits
	for i := 0; i < 3; i++ {
		_, err := scripts.QuotaIncr(ctx, *client, key, 3, 2592000)
		require.NoError(t, err)
	}

	// Multiple over-limit requests
	for i := 0; i < 5; i++ {
		result, err := scripts.QuotaIncr(ctx, *client, key, 3, 2592000)
		require.NoError(t, err)
		assert.False(t, result.Allowed)
	}
}

func TestQuotaIncr_TTLSetOnFirstIncrement(t *testing.T) {
	client, mr := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	key := "quota:ai:test-composite-key"

	// First increment sets TTL
	_, err := scripts.QuotaIncr(ctx, *client, key, 3, 2592000)
	require.NoError(t, err)

	// Check TTL exists
	ttl := mr.TTL(key)
	assert.Greater(t, ttl, time.Duration(0), "TTL should be set on first increment")
	assert.LessOrEqual(t, ttl, 2592000*time.Second)
}

func TestQuotaIncr_TTLNotResetOnSubsequentIncrements(t *testing.T) {
	client, mr := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	key := "quota:ai:test-composite-key"

	// First increment sets TTL
	_, err := scripts.QuotaIncr(ctx, *client, key, 3, 2592000)
	require.NoError(t, err)
	ttl1 := mr.TTL(key)

	// Second increment
	_, err = scripts.QuotaIncr(ctx, *client, key, 3, 2592000)
	require.NoError(t, err)
	ttl2 := mr.TTL(key)

	// TTL should be same or slightly less (due to time passing), not reset
	assert.LessOrEqual(t, ttl2, ttl1+time.Second, "TTL should not be reset on subsequent increments")
}

func TestQuotaIncr_DifferentKeysAreIndependent(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	key1 := "quota:ai:user1"
	key2 := "quota:ai:user2"

	// Exhaust key1
	for i := 0; i < 3; i++ {
		_, err := scripts.QuotaIncr(ctx, *client, key1, 3, 2592000)
		require.NoError(t, err)
	}

	// key1 exhausted
	result1, err := scripts.QuotaIncr(ctx, *client, key1, 3, 2592000)
	require.NoError(t, err)
	assert.False(t, result1.Allowed)

	// key2 should still be allowed
	result2, err := scripts.QuotaIncr(ctx, *client, key2, 3, 2592000)
	require.NoError(t, err)
	assert.True(t, result2.Allowed)
	assert.Equal(t, int64(1), result2.CreditsUsed)
}

func TestQuotaIncr_RouteGroupIsolation(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	// Same composite key, different route groups
	aiKey := "quota:ai:user1"
	contactKey := "quota:contact:user1"

	// Exhaust AI quota
	for i := 0; i < 3; i++ {
		_, err := scripts.QuotaIncr(ctx, *client, aiKey, 3, 2592000)
		require.NoError(t, err)
	}

	// AI exhausted
	aiResult, err := scripts.QuotaIncr(ctx, *client, aiKey, 3, 2592000)
	require.NoError(t, err)
	assert.False(t, aiResult.Allowed)

	// Contact quota still available
	contactResult, err := scripts.QuotaIncr(ctx, *client, contactKey, 3, 2592000)
	require.NoError(t, err)
	assert.True(t, contactResult.Allowed)
}

func TestQuotaIncr_Limit1(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	key := "quota:ai:single-credit"

	// First increment allowed
	result, err := scripts.QuotaIncr(ctx, *client, key, 1, 2592000)
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.Equal(t, int64(1), result.CreditsUsed)

	// Second increment denied
	result, err = scripts.QuotaIncr(ctx, *client, key, 1, 2592000)
	require.NoError(t, err)
	assert.False(t, result.Allowed)
}

// ============================================================================
// SESSION CREATE SCRIPT TESTS
// ============================================================================

func TestSessionCreate_FirstCreation(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	sessionKey := "guest:active_session:ai:user1"
	queryKey := "session:gsess_new123:queries"

	created, err := scripts.SessionCreate(ctx, *client, sessionKey, queryKey,
		"gsess_new123", 24*time.Hour, 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, created, "first creation should succeed")
}

func TestSessionCreate_ConcurrentCreation(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	sessionKey := "guest:active_session:ai:user1"
	queryKey1 := "session:gsess_first:queries"
	queryKey2 := "session:gsess_second:queries"

	// First creation succeeds
	created1, err := scripts.SessionCreate(ctx, *client, sessionKey, queryKey1,
		"gsess_first", 24*time.Hour, 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, created1)

	// Second creation should fail (SET NX returns nil)
	created2, err := scripts.SessionCreate(ctx, *client, sessionKey, queryKey2,
		"gsess_second", 24*time.Hour, 24*time.Hour)
	require.NoError(t, err)
	assert.False(t, created2, "second creation should fail (race condition)")
}

func TestSessionCreate_QueryCounterInitialized(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	sessionKey := "guest:active_session:ai:user1"
	queryKey := "session:gsess_test:queries"

	created, err := scripts.SessionCreate(ctx, *client, sessionKey, queryKey,
		"gsess_test", 24*time.Hour, 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, created)

	// Query counter should be 1 (initialized)
	val, err := client.Get(ctx, queryKey).Int()
	require.NoError(t, err)
	assert.Equal(t, 1, val, "query counter should be initialized to 1")
}

func TestSessionCreate_TTLsSet(t *testing.T) {
	client, mr := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	sessionKey := "guest:active_session:ai:user1"
	queryKey := "session:gsess_ttl:queries"

	activeTTL := 12 * time.Hour
	queryTTL := 6 * time.Hour

	_, err := scripts.SessionCreate(ctx, *client, sessionKey, queryKey,
		"gsess_ttl", activeTTL, queryTTL)
	require.NoError(t, err)

	// Check TTLs
	sessionTTL := mr.TTL(sessionKey)
	queryTTLActual := mr.TTL(queryKey)

	assert.LessOrEqual(t, sessionTTL, activeTTL)
	assert.LessOrEqual(t, queryTTLActual, queryTTL)
}

// ============================================================================
// SESSION RESUME SCRIPT TESTS
// ============================================================================

func TestSessionResume_MatchingSession(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	sessionKey := "guest:active_session:ai:user1"
	queryKey := "session:gsess_resume:queries"
	sessionID := "gsess_resume"

	// Create session
	created, err := scripts.SessionCreate(ctx, *client, sessionKey, queryKey,
		sessionID, 24*time.Hour, 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, created)

	// Resume with matching session ID
	result, err := scripts.SessionResume(ctx, *client, sessionKey, queryKey,
		sessionID, 6)
	require.NoError(t, err)
	assert.True(t, result.Allowed, "should allow resume with matching session")
	assert.Equal(t, int64(2), result.QueriesUsed, "should increment from 1 to 2")
}

func TestSessionResume_MismatchedSession(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	sessionKey := "guest:active_session:ai:user1"
	queryKey := "session:gsess_original:queries"

	// Create session with gsess_original
	created, err := scripts.SessionCreate(ctx, *client, sessionKey, queryKey,
		"gsess_original", 24*time.Hour, 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, created)

	// Try to resume with different session ID
	result, err := scripts.SessionResume(ctx, *client, sessionKey, queryKey,
		"gsess_different", 6)
	require.NoError(t, err)
	assert.False(t, result.Allowed, "should deny resume with mismatched session")
	assert.Equal(t, int64(0), result.QueriesUsed)
}

func TestSessionResume_QueriesExhausted(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	sessionKey := "guest:active_session:ai:user1"
	queryKey := "session:gsess_exhaust:queries"
	sessionID := "gsess_exhaust"

	// Create session (query counter = 1, first query consumed)
	created, err := scripts.SessionCreate(ctx, *client, sessionKey, queryKey,
		sessionID, 24*time.Hour, 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, created)

	// With limit=2, one more resume is allowed (total = 2)
	result, err := scripts.SessionResume(ctx, *client, sessionKey, queryKey,
		sessionID, 2)
	require.NoError(t, err)
	assert.True(t, result.Allowed, "query 2 should be allowed (within limit)")

	// Next resume: current(2) >= limit(2), denied
	result2, err := scripts.SessionResume(ctx, *client, sessionKey, queryKey,
		sessionID, 2)
	require.NoError(t, err)
	assert.False(t, result2.Allowed, "should deny when queries exhausted (current >= limit)")
}

func TestSessionResume_NoSessionExists(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()

	sessionKey := "guest:active_session:ai:nonexistent"
	queryKey := "session:gsess_ghost:queries"

	result, err := scripts.SessionResume(ctx, *client, sessionKey, queryKey,
		"gsess_ghost", 6)
	require.NoError(t, err)
	assert.False(t, result.Allowed, "should deny when no session exists")
	assert.Equal(t, int64(0), result.QueriesUsed)
}

// ============================================================================
// INTEGRATION TEST: FULL QUOTA FLOW
// ============================================================================

func TestFullQuotaFlow_ThreeCredits(t *testing.T) {
	client, _ := setupTestRedis(t)
	scripts := NewQuotaScripts()
	ctx := context.Background()
	routeGroup := "ai"
	compositeKey := "integration-test-key"

	creditsLimit := 3
	queriesPerSession := 6
	ttlSeconds := int64(2592000)

	// ── Credit 1: New session ──
	quotaKey1 := "quota:" + routeGroup + ":" + compositeKey
	qResult, err := scripts.QuotaIncr(ctx, *client, quotaKey1, creditsLimit, ttlSeconds)
	require.NoError(t, err)
	assert.True(t, qResult.Allowed)
	assert.Equal(t, int64(1), qResult.CreditsUsed)

	sessionKey1 := "guest:active_session:" + routeGroup + ":" + compositeKey
	queryKey1 := "session:gsess_c1:queries"
	created, err := scripts.SessionCreate(ctx, *client, sessionKey1, queryKey1,
		"gsess_c1", 24*time.Hour, 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, created)

	// ── Credit 1: Resume session, use queries ──
	for i := 0; i < 5; i++ {
		result, err := scripts.SessionResume(ctx, *client, sessionKey1, queryKey1,
			"gsess_c1", queriesPerSession)
		require.NoError(t, err)
		assert.True(t, result.Allowed, "query %d of credit 1 should be allowed", i+2)
	}

	// ── Credit 2: New session (old session exhausted) ──
	quotaKey2 := "quota:" + routeGroup + ":" + compositeKey
	qResult2, err := scripts.QuotaIncr(ctx, *client, quotaKey2, creditsLimit, ttlSeconds)
	require.NoError(t, err)
	assert.True(t, qResult2.Allowed)
	assert.Equal(t, int64(2), qResult2.CreditsUsed)

	// ── Credit 3: New session ──
	qResult3, err := scripts.QuotaIncr(ctx, *client, quotaKey2, creditsLimit, ttlSeconds)
	require.NoError(t, err)
	assert.True(t, qResult3.Allowed)
	assert.Equal(t, int64(3), qResult3.CreditsUsed)

	// ── Credit 4: Should be denied ──
	qResult4, err := scripts.QuotaIncr(ctx, *client, quotaKey2, creditsLimit, ttlSeconds)
	require.NoError(t, err)
	assert.False(t, qResult4.Allowed, "should deny after 3 credits")
}

// ============================================================================
// REDIS FAILURE TESTS
// ============================================================================

func TestRedisFailure_FailOpen(t *testing.T) {
	// Use a client pointing to a non-existent Redis
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:1", // Wrong port
	})
	defer client.Close()

	scripts := NewQuotaScripts()
	ctx := context.Background()

	// QuotaIncr should return error, caller handles fail-open
	_, err := scripts.QuotaIncr(ctx, *client, "quota:ai:test", 3, 2592000)
	assert.Error(t, err, "should return error on Redis failure")

	// SessionCreate should return error
	_, err = scripts.SessionCreate(ctx, *client, "session:1", "session:2",
		"gsess_test", 24*time.Hour, 24*time.Hour)
	assert.Error(t, err)

	// SessionResume should return error
	_, err = scripts.SessionResume(ctx, *client, "session:1", "session:2",
		"gsess_test", 6)
	assert.Error(t, err)
}
