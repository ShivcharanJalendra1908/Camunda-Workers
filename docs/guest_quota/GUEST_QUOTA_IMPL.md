## Final Guest Quota Implementation Plan

### 1. Config (`configs/config.yaml` + `config.go`)

**Add to `config.yaml`:**
```yaml
# ============================================================================
# GUEST QUOTA CONFIGURATION
# ============================================================================
guest_quota:
  enabled: true
  session_credits_limit: 3
  queries_per_session: 6
  # Redis key TTLs (cleanup only, not session lifetime)
  credit_key_ttl: "8760h"         # 1 year — credits reset monthly via EXPIREAT
  session_queries_ttl: "24h"      # cleanup orphan query counters
  active_session_ttl: "24h"       # cleanup stale session pointers
  audit_encryption_key: ""        # 32-byte base64 AES key
  anomaly:
    s1_tokens_per_ip_threshold: 20
    s2_ips_per_token_threshold: 3
    s3_burst_threshold: 3
    s3_burst_window_seconds: 60
    block_ttl_seconds: 86400
```

**Add to `config.go` Config struct:**
```go
GuestQuota GuestQuotaConfig `mapstructure:"guest_quota"`
```

**New struct:**
```go
type GuestQuotaConfig struct {
    Enabled                 bool          `mapstructure:"enabled"`
    SessionCreditsLimit     int           `mapstructure:"session_credits_limit"`
    QueriesPerSession       int           `mapstructure:"queries_per_session"`
    CreditKeyTTL            time.Duration `mapstructure:"credit_key_ttl"`
    SessionQueriesTTL       time.Duration `mapstructure:"session_queries_ttl"`
    ActiveSessionTTL        time.Duration `mapstructure:"active_session_ttl"`
    AuditEncryptionKey      string        `mapstructure:"audit_encryption_key"`
    Anomaly                 GuestAnomalyConfig `mapstructure:"anomaly"`
}

type GuestAnomalyConfig struct {
    S1TokensPerIPThreshold int `mapstructure:"s1_tokens_per_ip_threshold"`
    S2IPsPerTokenThreshold int `mapstructure:"s2_ips_per_token_threshold"`
    S3BurstThreshold       int `mapstructure:"s3_burst_threshold"`
    S3BurstWindowSeconds   int `mapstructure:"s3_burst_window_seconds"`
    BlockTTLSeconds        int `mapstructure:"block_ttl_seconds"`
}
```

**Docker-compose** — only add `GUEST_AUDIT_ENCRYPTION_KEY` as an env override (secret, shouldn't be in yaml). All other guest config stays in config.yaml.

---

### 2. Session Model (no fixed TTL)

The session is **ephemeral, frontend-driven**:

```
Frontend stores session ID in sessionStorage.
Sends X-AI-Session-ID header with each request.

Backend logic:
  1. Check Redis: guest:active_session:{compositeKey}
  2. If active session exists AND matches client's X-AI-Session-ID → resume
  3. If no active session OR mismatch → new session, consume credit

Session "dies" when:
  - Frontend loses sessionStorage (close tab, clear storage)
  - Frontend stops sending X-AI-Session-ID
  - Monthly credit resets (EXPIREAT on quota key)

Redis TTLs are ONLY for cleanup of orphaned keys:
  - guest:active_session:{key} → 24hr (stale pointer cleanup)
  - session:{id}:queries → 24hr (orphaned counter cleanup)
  - quota:sessions:{key}:{month} → EXPIREAT 1st of next month
```

---

### 3. Files to DELETE (1)

| File | Reason |
|------|--------|
| `internal/api/middleware/anon_limit.go` | Old per-endpoint quota. Remove `AnonymousInquiryLimiter` calls from `/contact` and `/forms` routes too. |

---

### 4. Files to CREATE (8)

| # | File | Purpose |
|---|------|---------|
| 1 | `internal/crypto/encrypt.go` | AES-256-GCM + HMAC-SHA256 |
| 2 | `internal/crypto/encrypt_test.go` | Tests |
| 3 | `internal/models/guest.go` | `GuestIdentity`, `SessionResult`, `QuotaExhaustedError` |
| 4 | `internal/common/database/quota_script.go` | Lua scripts (quotaIncrScript) |
| 5 | `internal/api/middleware/guest_signal.go` | compositeKey builder from headers |
| 6 | `internal/api/middleware/guest_anomaly.go` | S1–S5 anomaly detection |
| 7 | `internal/api/middleware/guest_ai_session.go` | 3-credit / 6-query enforcement |
| 8 | `internal/common/database/guest_audit_repo.go` | Async PG audit writes |

---

### 5. Files to MODIFY (5)

| # | File | Changes |
|---|------|---------|
| 1 | `internal/common/config/config.go` | Add `GuestQuotaConfig` struct + field in `Config` |
| 2 | `configs/config.yaml` | Add `guest_quota:` section |
| 3 | `internal/common/database/redis.go` | Add 7 new methods (GetActiveSession, SetActiveSession, etc.) |
| 4 | `configs/kong/kong.yaml` | Remove ai-routes rate limit, add CORS headers, add pre-function |
| 5 | `cmd/api-gateway/main.go` | Move `/ai` outside protected block, apply guest middleware, remove `AnonymousInquiryLimiter` from contact/forms |

---

### 6. `main.go` Route Change

```go
// BEFORE: ai routes inside protected block
protectedAPI := router.Group("/api/v1")
protectedAPI.Use(middleware.SessionOrJWTAuth(...))
{
    aiGroup := protectedAPI.Group("/ai")
    // ...
}

// AFTER: ai routes with guest quota middleware
aiGroup := router.Group("/api/v1/ai")
aiGroup.Use(
    middleware.GuestSignalMiddleware(cfg),
    middleware.GuestAnomalyMiddleware(redisClient.GetClient(), cfg),
    middleware.GuestAISessionMiddleware(redisClient.GetClient(), cfg),
)
aiGroup.POST("/query", workflowHandler.StartAIQuery)
aiGroup.POST("/discovery", workflowHandler.StartDiscovery)

// Protected routes no longer include /ai
protectedAPI := router.Group("/api/v1")
protectedAPI.Use(middleware.SessionOrJWTAuth(...))
{
    // user, franchise, application routes only
}
```

Remove from contact/forms:
```go
// BEFORE:
publicAPI.POST("/contact",
    middleware.AnonymousInquiryLimiter(redisClient.GetClient(), 50),
    workflowHandler.StartContactUs)

// AFTER:
publicAPI.POST("/contact", workflowHandler.StartContactUs)
```

---

### 7. Kong Changes

**ai-routes section:**
- Remove `rate-limiting` plugin (minute: 3)
- Add `pre-function` for X-Device-Token / X-AI-Session-ID validation

**Global cors plugin:**
- Add to `headers`: `X-Device-Token`, `X-Device-Salt`, `X-AI-Session-ID`, `X-Guest-CSRF-Token`
- Add to `exposed_headers`: all `X-Session-*` headers + `X-Quota-Status`

---

### 8. Redis Keys

| Key | Type | TTL | Purpose |
|-----|------|-----|---------|
| `guest:active_session:{compositeKey}` | STRING | 24hr (cleanup) | Current session pointer |
| `session:{sessionId}:queries` | INCR | 24hr (cleanup) | Query count |
| `quota:sessions:{compositeKey}:{YYYY-MM}` | INCR | EXPIREAT 1st of month | Monthly credits |
| `anom:ip:{ip24}:tokens` | HLL | 1hr | S1 |
| `anom:token:{key}:ips` | HLL | 1hr | S2 |
| `anom:token:{key}:burst` | INCR | 60s | S3 |
| `block:guest:{compositeKey}` | STRING | 24hr | S5 block |

---

### 9. Middleware Flow

```
GuestSignalMiddleware:
  Read X-Device-Token, X-Device-Salt, X-JA3-Fingerprint, X-Real-IP, User-Agent
  Build compositeKey (SHA256 of token+salt or IP/24+UA+salt fallback)
  Set "guestIdentity" in context

GuestAnomalyMiddleware:
  S5: EXISTS block:guest:{key} → reject if blocked
  S3: INCR anom:token:{key}:burst → reject if >= 3 in 60s
  S1: PFADD+PFCOUNT anom:ip:{ip24}:tokens → block if >= 20
  S2: PFADD+PFCOUNT anom:token:{key}:ips → block if >= 3
  S4: token absent → log warning, use fallback (don't block)

GuestAISessionMiddleware:
  Check session cookie → if valid auth session, skip quota, c.Next()
  If guest:
    GET guest:active_session:{key}
    If matches client X-AI-Session-ID → resume, INCR queries
    If no match / no session → INCR credits via Lua script
    If credits > 3 → 429 SIGNUP_REQUIRED
    SET guest:active_session + session:queries via pipeline
    Set X-Session-* response headers
```
