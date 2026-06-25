# Guest AI Request Flow — Technical Reference

## Overview

This document traces every step of a guest (non-logged-in) user's request through
the `/api/v1/ai/query` endpoint, including edge cases, failure modes, Redis key
lifecycle, and which file/function handles each step.

---

## Request Pipeline

```
Client (browser)
  │
  │  POST /api/v1/ai/query
  │  Headers:
  │    X-Device-Token: a1b2c3...      (optional — browser-generated fingerprint)
  │    X-Device-Salt: x9y8z7...       (required — random secret per device)
  │    X-Guest-Session-ID: gsess_...  (optional — existing session to resume)
  │
  ▼
┌──────────────────────────────────────────────────────────────────┐
│  GIN ROUTER                                                      │
│  Route: POST /api/v1/ai/query                                    │
│  Middleware chain (in order):                                    │
│    1. GuestSignalMiddleware    → builds device identity          │
│    2. GuestAnomalyMiddleware   → detects abuse patterns          │
│    3. GuestQuotaMiddleware     → enforces credits + sessions     │
│  Handler: WorkflowHandler.StartAIQuery                           │
└──────────────────────────────────────────────────────────────────┘
```

---

## Layer 1: Device Identity

**File:** `internal/api/middleware/guest_signal.go`

### What it does

Builds a unique, anonymised identifier for the guest device. This identifier
is used as the Redis key suffix for all subsequent lookups (credits, sessions,
anomaly tracking).

### Functions

| Function                    | Signature                              | Returns      | Purpose                                                  |
| --------------------------- | -------------------------------------- | ------------ | -------------------------------------------------------- |
| `GuestSignalMiddleware`     | `(cfg *config.Config) gin.HandlerFunc` | middleware   | Main middleware — extracts headers, builds composite key |
| `buildCompositeKey`         | `(token, salt string) string`          | 16 hex chars | SHA256(token:salt) truncated to 8 bytes                  |
| `buildFallbackCompositeKey` | `(ip, ua, salt string) string`         | 16 hex chars | SHA256(ip/24:normalizedUA:salt) truncated to 8 bytes     |
| `ipSlash24`                 | `(ip string) string`                   | `"a.b.c"`    | Extracts /24 subnet from IPv4                            |
| `isValidHexToken`           | `(token string) bool`                  | true/false   | Validates token is lowercase hex, min 32 chars           |

### Flow

```
Request arrives
  │
  ├─ Is X-Device-Salt present?
  │   NO  → 400 MISSING_DEVICE_SALT (abort)
  │   YES → continue
  │
  ├─ Is X-Device-Token present?
  │   YES → validate hex format
  │         INVALID → 400 INVALID_DEVICE_TOKEN (abort)
  │         VALID   → compositeKey = buildCompositeKey(token, salt)
  │                   identity.HasToken = true
  │
  │   NO  → compositeKey = buildFallbackCompositeKey(ip, ua, salt)
  │         identity.FallbackIP = ip/24
  │         Log: [GUEST_S4] token_absent ip=a.b.c
  │
  └─ Store identity in gin.Context as "guestIdentity"
     Call c.Next() → next middleware
```

### Edge cases

| Edge case                                  | Handling                                           |
| ------------------------------------------ | -------------------------------------------------- |
| Salt is empty string                       | 400 `MISSING_DEVICE_SALT` — request blocked        |
| Token is present but too short (<32 chars) | 400 `INVALID_DEVICE_TOKEN` — request blocked       |
| Token contains uppercase hex               | 400 `INVALID_DEVICE_TOKEN` — must be lowercase     |
| Token absent entirely                      | Falls back to IP+UA composite key, logs S4 warning |
| IPv6 address                               | `ipSlash24` extracts first 3 colon-groups          |

### Redis keys touched

None. This layer is pure computation.

---

## Layer 2: Anomaly Detection

**File:** `internal/api/middleware/guest_anomaly.go`

### What it does

Runs 5 abuse-detection signals (S1–S5) before quota enforcement. All checks
are **fail-open** — Redis errors don't block users.

### Signals

| Signal | Name          | What it catches          | Detection method            | Block threshold | Redis key                |
| ------ | ------------- | ------------------------ | --------------------------- | --------------- | ------------------------ |
| **S5** | Blocklist     | Known abusers            | `EXISTS`                    | Already blocked | `block:guest:{key}`      |
| **S3** | Burst         | Robot-like speed         | `INCR` + `EXPIRE`           | 3+ in 60s       | `anom:token:{key}:burst` |
| **S1** | Tokens-per-IP | Shared proxy/VPN         | HyperLogLog (PFADD/PFCOUNT) | 20+ in 1hr      | `anom:ip:{ip/24}:tokens` |
| **S2** | IPs-per-token | Stolen token             | HyperLogLog (PFADD/PFCOUNT) | 3+ in 1hr       | `anom:token:{key}:ips`   |
| **S4** | Token absence | Cookie clear/first visit | Log warning (not blocking)  | N/A — info only | N/A                      |

### Functions

| Function                 | Signature                                                       | Returns      | Purpose                                               |
| ------------------------ | --------------------------------------------------------------- | ------------ | ----------------------------------------------------- |
| `GuestAnomalyMiddleware` | `(redisClient, cfg, auditRepo) gin.HandlerFunc`                 | middleware   | Main middleware — runs S5→S3→S1→S2 in order           |
| `isBlocked`              | `(ctx, client, compositeKey, blockTTL) bool`                    | true/false   | S5 — checks blocklist via Redis EXISTS                |
| `blockGuest`             | `(ctx, client, compositeKey, blockTTL)`                         | void         | Adds key to blocklist with TTL                        |
| `checkBurst`             | `(ctx, client, compositeKey, threshold, window) (int64, error)` | count, err   | S3 — increments burst counter, sets TTL on first      |
| `countTokensPerIP`       | `(ctx, client, ipSlash24, compositeKey, ttl) (int64, error)`    | count, err   | S1 — PFADD to IP's HLL, PFCOUNT returns unique tokens |
| `countIPsPerToken`       | `(ctx, client, compositeKey, ipSlash24, ttl) (int64, error)`    | count, err   | S2 — PFADD to token's HLL, PFCOUNT returns unique IPs |
| `getGuestIdentity`       | `(c *gin.Context) *GuestIdentity`                               | identity/nil | Reads identity from gin.Context (set by Signal layer) |
| `logAuditEvent`          | `(auditRepo, event)`                                            | void         | Writes to PG audit log (or stdout if repo nil)        |

### Flow (in execution order)

```
Request arrives at anomaly middleware
  │
  ├─ Is anomaly detection enabled? (cfg.Guest.Anomaly.Enabled)
  │   NO  → skip all checks, c.Next()
  │
  ├─ Is guest identity present? (from Signal layer)
  │   NO  → skip (not a guest request), c.Next()
  │
  ├─── S5: Blocklist check ───────────────────────────────
  │    Redis: EXISTS block:guest:{compositeKey}
  │    EXISTS → 403 ACCESS_BLOCKED, log audit event, abort
  │    NOT EXISTS → continue
  │
  ├─── S3: Burst detection ───────────────────────────────
  │    Redis: INCR anom:token:{key}:burst
  │    count == 1 → EXPIRE 60s (set window)
  │    count >= 3 → blockGuest(), 429 RATE_LIMITED, abort
  │
  ├─── S1: Tokens-per-IP (only if fallback IP available) ─
  │    Redis: PFADD anom:ip:{ip/24}:tokens {compositeKey}
  │    Redis: EXPIRE anom:ip:{ip/24}:tokens 3600
  │    Redis: PFCOUNT anom:ip:{ip/24}:tokens
  │    count >= 20 → blockGuest(), 429 TOO_MANY_DEVICES, abort
  │
  ├─── S2: IPs-per-token (only if token was provided) ────
  │    Redis: PFADD anom:token:{key}:ips {ip/24}
  │    Redis: EXPIRE anom:token:{key}:ips 3600
  │    Redis: PFCOUNT anom:token:{key}:ips
  │    count >= 3 → blockGuest(), 429 SHARED_DEVICE, abort
  │
  └─ All checks passed → c.Next() → quota layer
```

### Edge cases

| Edge case                   | Handling                                                                  |
| --------------------------- | ------------------------------------------------------------------------- |
| Redis down on S5            | `isBlocked` returns false (fail open)                                     |
| Redis down on S3            | `checkBurst` returns error, check skipped                                 |
| Redis down on S1/S2         | `PFCount` returns error, check skipped                                    |
| S1 triggers without token   | S1 only runs if `identity.FallbackIP != ""`                               |
| S2 triggers without token   | S2 only runs if `identity.HasToken`                                       |
| Burst counter overflow      | Counter increments forever within window, but block triggers at threshold |
| HyperLogLog false positives | HLL has ~0.81% error rate — acceptable for abuse detection                |

### Redis keys created

| Key                      | TTL                       | Purpose                    |
| ------------------------ | ------------------------- | -------------------------- |
| `block:guest:{key}`      | `block_ttl_seconds` (24h) | Blocks known abusers       |
| `anom:token:{key}:burst` | 60s                       | Burst request counter      |
| `anom:ip:{ip/24}:tokens` | 1hr                       | Unique tokens from this IP |
| `anom:token:{key}:ips`   | 1hr                       | Unique IPs for this token  |

---

## Layer 3: Credit & Session Enforcement

**File:** `internal/api/middleware/guest_quota.go`

### What it does

Enforces the 3-credit rolling-window limit and 6-queries-per-session limit.
Skips entirely for authenticated users (valid session cookie).

### Functions

| Function                  | Signature                                                                     | Returns             | Purpose                                          |
| ------------------------- | ----------------------------------------------------------------------------- | ------------------- | ------------------------------------------------ |
| `GuestQuotaMiddleware`    | `(redisClient, cfg, routeGroup) gin.HandlerFunc`                              | middleware          | Main middleware — credit + session enforcement   |
| `isAuthSessionValid`      | `(ctx, c, redisClient) bool`                                                  | true/false          | Checks AUTH_SESSION_ID cookie in Redis           |
| `getCreditCount`          | `(ctx, client, routeGroup, compositeKey) (int64, error)`                      | count, err          | Reads current credit count for the window        |
| `setGuestResponseHeaders` | `(c, sessionID, queriesUsed, queriesLimit, creditsUsed, creditsLimit, isNew)` | void                | Sets all X-Session-\* and X-Quota-Status headers |
| `generateSessionID`       | `() string`                                                                   | `"gsess_" + 24 hex` | Creates 30-char session ID                       |

### Flow

```
Request arrives at quota middleware
  │
  ├─── Auth bypass ───────────────────────────────────────
  │    Check: AUTH_SESSION_ID cookie → Redis GET session:{sid}
  │    EXISTS → user is logged in, skip ALL guest quota, c.Next()
  │    NOT EXISTS → continue as guest
  │
  ├─── Extract identity ──────────────────────────────────
  │    Read "guestIdentity" from gin.Context
  │    nil → skip (shouldn't happen), c.Next()
  │
  ├─── Check for existing session ────────────────────────
  │    clientSessionID = c.GetHeader("X-Guest-Session-ID")
  │
  │    IF clientSessionID is present:
  │      Redis: sessionResumeScript Lua
  │        GET guest:active_session:{routeGroup}:{key}
  │        IF stored session matches client session:
  │          INCR session:{clientSessionID}:queries
  │          IF queries <= 6 → ALLOWED, set headers, c.Next()
  │          IF queries > 6 → QUERIES EXHAUSTED
  │            DELETE guest:active_session:{routeGroup}:{key}
  │            Fall through to new session
  │        IF stored session doesn't match:
  │          Fall through to new session
  │
  ├─── New session — consume a credit ────────────────────
  │    Redis: quotaIncrScript Lua
  │      GET quota:{routeGroup}:{key}
  │      IF current >= 3 → BLOCKED (credits exhausted)
  │        Read TTL for reset_in_seconds
  │        Return 429 SIGNUP_REQUIRED
  │      INCR quota:{routeGroup}:{key}
  │      IF was first INCR → EXPIRE 2592000 (30 days)
  │      RETURN {creditsUsed, allowed}
  │
  │    IF not allowed → 429 SIGNUP_REQUIRED, abort
  │
  ├─── Create new session ────────────────────────────────
  │    newSessionID = generateSessionID()  // "gsess_" + 24 hex
  │    Redis: sessionCreateScript Lua
  │      SET guest:active_session:{routeGroup}:{key} = sessionID NX EX 86400
  │      IF NX failed (race condition):
  │        Read existing session ID
  │        Resume that session instead
  │      IF NX succeeded:
  │        SET session:{newSessionID}:queries = 1 EX 86400
  │
  ├─── Set response headers ──────────────────────────────
  │    X-Session-ID: gsess_...
  │    X-Session-Queries-Used: 1
  │    X-Session-Queries-Remaining: 5
  │    X-Session-Queries-Limit: 6
  │    X-Session-Credits-Used: 1
  │    X-Session-Credits-Remaining: 2
  │    X-Session-Credits-Limit: 3
  │    X-Session-Is-New: true
  │    X-Quota-Status: ok | last_credit | exhausted
  │
  └─ c.Next() → reaches WorkflowHandler.StartAIQuery
```

### Edge cases

| Edge case                             | Handling                                                                                                 |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| Redis down on auth check              | `isAuthSessionValid` returns false — treats as guest                                                     |
| Redis down on quota INCR              | Fail open — request proceeds without consuming credit                                                    |
| Redis down on session create          | Fail open — request proceeds                                                                             |
| Race: 2 concurrent first requests     | Lua is atomic — first INCR gets credit 1 (sets TTL), second gets credit 2 (TTL untouched)                |
| Race: 2 concurrent session creates    | SET NX — only one succeeds, loser reads winner's session                                                 |
| Session ID sent but doesn't exist     | Falls through to new session, consumes credit                                                            |
| Session ID sent but queries exhausted | Deletes old session, falls through to new session                                                        |
| Credit key expires mid-session        | Existing session keys are independent (own TTLs), session continues; next new session starts fresh count |
| `routeGroup` not in config            | Fail open — middleware no-ops with warning log                                                           |

### Redis keys used

| Key                                       | Purpose                      | TTL                         |
| ----------------------------------------- | ---------------------------- | --------------------------- |
| `quota:{routeGroup}:{key}`                | Credit counter               | 30 days (set on first INCR) |
| `guest:active_session:{routeGroup}:{key}` | Points to current session ID | `active_session_ttl` (24h)  |
| `session:{sessionID}:queries`             | Query count for this session | `session_queries_ttl` (24h) |
| `session:{sessionID}`                     | Auth session (read-only)     | managed by auth system      |

---

## Lua Scripts

**File:** `internal/common/database/quota_script.go`

### quotaIncrScript

Runs atomically. Checks + increments credit counter with rolling-window TTL.

```lua
-- Input: KEYS[1] = quota key, ARGV[1] = limit, ARGV[2] = ttlSeconds
-- Logic:
--   current = GET key (or 0)
--   if current >= limit → return {current, 0} (blocked)
--   new_val = INCR key
--   if current == 0 → EXPIRE key ttlSeconds  (first request sets window)
--   if new_val > limit → return {new_val, 0}  (over limit after increment)
--   return {new_val, 1}                       (allowed)
```

### sessionCreateScript

Runs atomically. Creates session with SET NX (no overwrite).

```lua
-- Input: KEYS[1] = session pointer, KEYS[2] = query counter
--        ARGV[1] = sessionID, ARGV[2] = activeTTL, ARGV[3] = queryTTL
-- Logic:
--   wasSet = SET sessionKey sessionID EX activeTTL NX
--   if not wasSet → return 0  (race: another request won)
--   SET queryKey 1 EX queryTTL
--   return 1  (created)
```

### sessionResumeScript

Runs atomically. Validates session ownership + increments query count.

```lua
-- Input: KEYS[1] = session pointer, KEYS[2] = query counter
--        ARGV[1] = clientSessionID, ARGV[2] = queriesLimit
-- Logic:
--   stored = GET sessionKey
--   if not stored or stored != clientSessionID → return {0, 0} (mismatch)
--   current = GET queryKey (or 0)
--   if current >= limit → return {current, 0}  (queries exhausted)
--   new_val = INCR queryKey
--   if new_val > limit → return {new_val, 0}
--   return {new_val, 1}  (allowed)
```

---

## Handler Layer

**File:** `internal/api/handlers/workflow_handler.go`

### What happens after middleware

The handler `WorkflowHandler.StartAIQuery` (line 161) receives the request
after all 3 middleware layers have passed. At this point:

- Guest identity is in `gin.Context` as `"guestIdentity"`
- All response headers are already set by quota middleware
- The handler processes the actual AI query (forwards to GenAI API, etc.)

The handler does NOT interact with the guest quota system — all quota logic
is handled by middleware before the handler runs.

---

## End-to-End Example: First-Time AI Query

```
1. Browser sends:
   POST /api/v1/ai/query
   X-Device-Token: a1b2c3d4e5f6...  (32 hex chars)
   X-Device-Salt: x9y8z7w6v5u4...
   Body: {"query": "Tell me about food franchises"}

2. Signal middleware:
   - Validates salt ✓
   - Validates token format ✓
   - compositeKey = SHA256("a1b2c3...:x9y8z7...")[:8] = "f8a1b2c3d4e5f6a1"
   - Stores identity in context

3. Anomaly middleware:
   - S5: EXISTS block:guest:f8a1b2... → NO (not blocked)
   - S3: INCR anom:token:f8a1b2...:burst → 1 (first request, sets 60s TTL)
   - S1: PFADD anom:ip:192.168.1:tokens f8a1b2... → PFCOUNT = 1 (OK, threshold 20)
   - S2: PFADD anom:token:f8a1b2...:ips 192.168.1 → PFCOUNT = 1 (OK, threshold 3)
   - All passed → continue

4. Quota middleware:
   - Auth check: no AUTH_SESSION_ID cookie → continue as guest
   - X-Guest-Session-ID header: not present → new session
   - Credit check:
     Lua: GET quota:ai:f8a1b2... → nil (first time)
          INCR → 1
          EXPIRE 2592000 (30 days)
          Return {1, 1} (1 credit used, allowed)
   - Create session:
     newSessionID = "gsess_a1b2c3d4e5f6a1b2c3d4e5f6"
     Lua: SET guest:active_session:ai:f8a1b2... = gsess_... NX EX 86400 → OK
          SET session:gsess_...:queries = 1 EX 86400
   - Set headers:
     X-Session-ID: gsess_a1b2c3d4e5f6a1b2c3d4e5f6
     X-Session-Queries-Used: 1
     X-Session-Queries-Remaining: 5
     X-Session-Credits-Used: 1
     X-Session-Credits-Remaining: 2
     X-Session-Is-New: true
     X-Quota-Status: ok

5. Handler (StartAIQuery):
   - Reads query from body
   - Forwards to GenAI API
   - Returns AI response to client
```

---

## End-to-Example: Session Resumption (3rd Query)

```
1. Browser sends:
   POST /api/v1/ai/query
   X-Device-Token: a1b2c3d4e5f6...
   X-Device-Salt: x9y8z7w6v5u4...
   X-Guest-Session-ID: gsess_a1b2c3d4e5f6a1b2c3d4e5f6
   Body: {"query": "What about fashion franchises?"}

2. Signal middleware:
   - compositeKey = "f8a1b2c3d4e5f6a1" (same as before)

3. Anomaly middleware:
   - S3: INCR burst → 2 (still under threshold of 3)
   - S1/S2: HLL counts still low → pass

4. Quota middleware:
   - Auth check: no session cookie → continue as guest
   - X-Guest-Session-ID present → try resume
   - Lua: GET guest:active_session:ai:f8a1b2... → "gsess_..." (matches!)
         GET session:gsess_...:queries → 2 (already used 2 queries)
         INCR → 3
         Return {3, 1} (3 queries used, allowed)
   - Set headers:
     X-Session-Queries-Used: 3
     X-Session-Queries-Remaining: 3
     X-Session-Is-New: false
     X-Quota-Status: ok

5. Handler processes query normally
```

---

## End-to-Example: Credits Exhausted (4th Session)

```
1. Browser sends POST /api/v1/ai/query (after using 3 credits in past 30 days)

4. Quota middleware:
   - No existing session → new session path
   - Credit check:
     Lua: GET quota:ai:f8a1b2... → 3 (all used)
          3 >= 3 → return {3, 0} (blocked)
   - Read TTL: redisClient.TTL(quota:ai:f8a1b2...) → 2500000s (~29 days)
   - Return 429:
     {
       "code": "SIGNUP_REQUIRED",
       "reason": "monthly_credits_exhausted",
       "creditsUsed": 3,
       "creditsLimit": 3,
       "resetIn": 2500000,
       "message": "You've used all 3 free sessions. Sign up for unlimited access.",
       "signupUrl": "/register"
     }
   - Request aborted — never reaches handler
```

---

## Redis Key Lifecycle

```
First request from device:
  quota:ai:{key}              created, TTL = 30 days (EXPIRE on first INCR)
  guest:active_session:ai:{key}  created, TTL = 24h
  session:gsess_...:queries   created, TTL = 24h, value = 1

Subsequent requests (same session):
  quota:ai:{key}              incremented (TTL untouched)
  session:gsess_...:queries   incremented (TTL extended on resume)

Session expires (24h) or queries exhausted:
  guest:active_session:ai:{key}  expires or deleted
  session:gsess_...:queries   expires

New session starts:
  guest:active_session:ai:{key}  new value, fresh 24h TTL
  session:gsess_{new}:queries   new key, TTL = 24h

Credit window expires (30 days):
  quota:ai:{key}              expires naturally → next request starts fresh
```

---

## File Index

| File                                           | Purpose                              | Key exports                                                                              |
| ---------------------------------------------- | ------------------------------------ | ---------------------------------------------------------------------------------------- |
| `internal/api/middleware/guest_signal.go`      | Layer 1 — Device identity            | `GuestSignalMiddleware`, `HeaderDeviceToken`, `HeaderDeviceSalt`, `HeaderGuestSessionID` |
| `internal/api/middleware/guest_anomaly.go`     | Layer 2 — Abuse detection            | `GuestAnomalyMiddleware`, `getGuestIdentity`, `logAuditEvent`                            |
| `internal/api/middleware/guest_quota.go`       | Layer 3 — Credit/session enforcement | `GuestQuotaMiddleware`, `setGuestResponseHeaders`, `generateSessionID`                   |
| `internal/common/database/quota_script.go`     | Lua scripts for atomic Redis ops     | `QuotaScripts`, `QuotaIncr`, `SessionCreate`, `SessionResume`                            |
| `internal/common/database/guest_audit_repo.go` | Async PG audit writer                | `GuestAuditRepo`, `LogEvent`, `PruneOldEvents`                                           |
| `internal/models/guest.go`                     | Data structures                      | `GuestIdentity`, `SessionResult`, `QuotaExhaustedError`, `GuestAuditEvent`               |
| `internal/common/config/config.go`             | Configuration structs                | `GuestQuotaConfig`, `GuestRouteGroupConfig`, `GuestAnomalyConfig`                        |
| `cmd/api-gateway/main.go`                      | Route wiring                         | Middleware chain registration per route group                                            |
| `deployments/docker/postgres/30-schema.sql`    | Database schema                      | `guest_audit_log` table, `cleanup_expired_guest_audit_events()`                          |

---

## Configuration Reference

**File:** `configs/config.yaml` (under `guest:`)

```yaml
guest:
  enabled: true # master switch — false disables all guest middleware
  route_groups:
    ai: # route group name (used in Redis keys + audit)
      credits_per_window: 3 # max credits per rolling window
      credit_window_days: 30 # window duration in days
      queries_per_session: 6 # max queries before session ends
      session_ttl_seconds: 86400 # session pointer TTL (cleanup)
      credit_key_ttl: "8760h" # credit key max TTL (safety net)
      session_queries_ttl: "24h" # query counter TTL
      active_session_ttl: "24h" # active session pointer TTL
  anomaly:
    enabled: true # master switch for S1-S5
    s1_tokens_per_ip_threshold: 20 # S1 block threshold
    s2_ips_per_token_threshold: 3 # S2 block threshold
    s3_burst_threshold: 3 # S3 block threshold
    s3_burst_window_seconds: 60 # S3 time window
    block_ttl_seconds: 86400 # how long blocked users stay blocked
  audit:
    enabled: true # write audit events to PostgreSQL
    retain_days: 30 # how long to keep audit logs
    buffer_size: 500 # async write queue size
  encryption:
    key_env: "GUEST_AUDIT_ENCRYPTION_KEY" # env var with base64 key
```
