package database

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ============================================================================
// LUA SCRIPTS FOR GUEST QUOTA
// ============================================================================

// quotaIncrScript atomically checks credits, increments if allowed, and returns the result.
// Uses EXPIRE on first increment to set a rolling 30-day window from the guest's first request.
// Keys:
//   [1] quota:{routeGroup}:{compositeKey}  â€” credit counter (rolling window)
//
// Args:
//   [1] creditsLimit  â€” max credits per window (3)
//   [2] ttlSeconds    â€” TTL in seconds (e.g. 2592000 for 30 days)
//
// Returns: (creditsUsed int64, allowed bool)
//   allowed = 1 if increment succeeded (creditsUsed <= creditsLimit)
//   allowed = 0 if credits exhausted (creditsUsed > creditsLimit)
var quotaIncrScript = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local ttlSeconds = tonumber(ARGV[2])

-- Get current value
local current = tonumber(redis.call('GET', key) or '0')

-- Check if already at or over limit
if current >= limit then
    return {current, 0}
end

-- Increment
local new_val = redis.call('INCR', key)

-- Set rolling window TTL on first increment (when current was 0)
if current == 0 then
    redis.call('EXPIRE', key, ttlSeconds)
end

-- Check if still within limit after increment
if new_val > limit then
    return {new_val, 0}
end

return {new_val, 1}
`)

// sessionCreateScript atomically sets session pointer with SET NX (prevents concurrent overwrite)
// and initializes query counter.
// Keys:
//   [1] guest:active_session:{compositeKey}  â€” current session pointer
//   [2] session:{sessionID}:queries          â€” query count for this session
//
// Args:
//   [1] sessionID     â€” new session ID (gsess_xxx)
//   [2] activeTTL     â€” TTL for active session pointer (seconds)
//   [3] queryTTL      â€” TTL for query counter (seconds)
//
// Returns: 1 on success (SET NX succeeded), 0 if key already existed (race condition)
var sessionCreateScript = redis.NewScript(`
local sessionKey = KEYS[1]
local queryKey = KEYS[2]
local sessionID = ARGV[1]
local activeTTL = tonumber(ARGV[2])
local queryTTL = tonumber(ARGV[3])

-- SET NX: only set if no active session exists (prevents concurrent overwrite)
local wasSet = redis.call('SET', sessionKey, sessionID, 'EX', activeTTL, 'NX')

if not wasSet then
    -- Another request already created a session â€” return 0 (caller reads existing)
    return 0
end

-- Initialize query counter to 1 (this is the first query)
redis.call('SET', queryKey, 1, 'EX', queryTTL)

return 1
`)

// sessionResumeScript atomically validates session ownership and increments query count.
// Keys:
//   [1] guest:active_session:{compositeKey}  â€” current session pointer
//   [2] session:{sessionID}:queries          â€” query count for this session
//
// Args:
//   [1] clientSessionID â€” session ID from client's X-Guest-Session-ID header
//   [2] queriesLimit    â€” max queries per session (6)
//
// Returns: (queriesUsed int64, allowed bool)
//   allowed = 1 if session matches and queries within limit
//   allowed = 0 if session mismatch or queries exhausted
var sessionResumeScript = redis.NewScript(`
local sessionKey = KEYS[1]
local queryKey = KEYS[2]
local clientSessionID = ARGV[1]
local limit = tonumber(ARGV[2])

-- Check if active session exists and matches
local storedSessionID = redis.call('GET', sessionKey)
if not storedSessionID or storedSessionID ~= clientSessionID then
    return {0, 0}
end

-- Get current query count
local current = tonumber(redis.call('GET', queryKey) or '0')

-- Check if at query limit
if current >= limit then
    return {current, 0}
end

-- Increment query count
local new_val = redis.call('INCR', queryKey)

if new_val > limit then
    return {new_val, 0}
end

return {new_val, 1}
`)

// QuotaScripts holds compiled Lua scripts for guest quota operations.
type QuotaScripts struct {
	quotaIncr   *redis.Script
	sessionCreate *redis.Script
	sessionResume *redis.Script
}

// NewQuotaScripts returns the compiled quota scripts.
func NewQuotaScripts() *QuotaScripts {
	return &QuotaScripts{
		quotaIncr:     quotaIncrScript,
		sessionCreate: sessionCreateScript,
		sessionResume: sessionResumeScript,
	}
}

// QuotaIncrResult is the return value from QuotaIncr.
type QuotaIncrResult struct {
	CreditsUsed int64
	Allowed     bool
}

// SessionResult is the return value from SessionCreate/SessionResume.
type SessionScriptResult struct {
	QueriesUsed int64
	Allowed     bool
}

// QuotaIncr atomically increments the rolling-window credit counter if within limit.
// Uses EXPIRE on first increment to set a rolling TTL from the guest's first request.
func (s *QuotaScripts) QuotaIncr(ctx context.Context, client redis.Client, key string, creditsLimit int, ttlSeconds int64) (*QuotaIncrResult, error) {
	result, err := s.quotaIncr.Run(ctx, &client, []string{key}, creditsLimit, ttlSeconds).Int64Slice()
	if err != nil {
		return nil, fmt.Errorf("quotaIncr script: %w", err)
	}

	return &QuotaIncrResult{
		CreditsUsed: result[0],
		Allowed:     result[1] == 1,
	}, nil
}

// SessionCreate atomically creates a new session with query counter initialized to 1.
// Uses SET NX to prevent concurrent session overwrite. Returns whether this call created
// the session (true) or lost the race (false â€” caller should read existing session).
func (s *QuotaScripts) SessionCreate(ctx context.Context, client redis.Client, sessionKey, queryKey, sessionID string, activeTTL, queryTTL time.Duration) (created bool, err error) {
	result, err := s.sessionCreate.Run(ctx, &client, []string{sessionKey, queryKey},
		sessionID, int(activeTTL.Seconds()), int(queryTTL.Seconds())).Int()
	if err != nil {
		return false, fmt.Errorf("sessionCreate script: %w", err)
	}
	return result == 1, nil
}

// SessionResume atomically validates session ownership and increments query count.
func (s *QuotaScripts) SessionResume(ctx context.Context, client redis.Client, sessionKey, queryKey, clientSessionID string, queriesLimit int) (*SessionScriptResult, error) {
	result, err := s.sessionResume.Run(ctx, &client, []string{sessionKey, queryKey},
		clientSessionID, queriesLimit).Int64Slice()
	if err != nil {
		return nil, fmt.Errorf("sessionResume script: %w", err)
	}

	return &SessionScriptResult{
		QueriesUsed: result[0],
		Allowed:     result[1] == 1,
	}, nil
}
