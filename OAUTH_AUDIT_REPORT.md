# OAuth2/OIDC Callback Implementation - Production Audit Report

**Date:** April 24, 2026
**Auditor:** Backend Engineering Team
**Status:** REVIEW COMPLETE - Implementation Ready with Recommendations

---

## 1. Executive Summary

This report documents the end-to-end OAuth2/OIDC authentication flow for the API gateway, including entry points, Camunda workers, and session management. Critical findings and discussion points for production readiness are included.

**Overall Status:** PASS WITH RECOMMENDATIONS

---

## 2. Entry Points (main.go)

| Route | Handler | Purpose |
|-------|---------|---------|
| `POST /api/v1/auth/login` | StartKeycloakLogin | Initiate login OR handle callback |
| `GET /api/v1/auth/callback` | HandleKeycloakCallback | Browser redirect from Keycloak |

---

## 3. Complete OAuth Request Flow

### Flow 1: Login Initiation (POST /auth/login with no code)

```
STEP 1: Browser → POST /api/v1/auth/login (no code/state)

STEP 2: API Gateway - StartKeycloakLogin (workflow_handler.go:1582)
  - Validate: code=="" AND state==""
  - Set action = "initiate"
  - Start Camunda workflow: keycloak-login-workflow
  - Wait on Redis pubsub (correlationKey)

STEP 3: Camunda Worker - keycloak-signin (initiate action)
  - generateState() → 32 bytes random
  - generatePKCE() → verifier + challenge
  - Redis SET oauth:state:{state} = {verifier} (TTL 5min)
  - Build Keycloak authorization URL with state + PKCE challenge

STEP 4: Return to Frontend
  - {authorizationUrl, state}
  - Browser redirects to Keycloak login page
  - User authenticates
  - Keycloak redirects to /callback?code=XXX&state=YYY
```

### Flow 2: OAuth Callback (GET /auth/callback OR POST /auth/login with code)

```
STEP 5: Browser → GET /api/v1/auth/callback?code=XXX&state=YYY

STEP 6: API Gateway - HandleKeycloakCallback (workflow_handler.go:2285)
  - Extract code and state from query params
  - Reuse StartKeycloakLogin with code+state
  - Start workflow: keycloak-login-workflow
  - Wait on Redis pubsub

STEP 7: Camunda Worker - keycloak-signin (callback action)
  - Validate state: Redis GET+DEL oauth:state:{state} atomically
    - Returns verifier if valid
    - Returns error INVALID_STATE if expired/missing
  - Exchange code for tokens: keycloak.ExchangeCode(code, verifier)
  - Resolve user: resolver.Resolve(identity)
    - Creates user if new
  - Output: {userId, email, keycloakUserId, idToken}

STEP 8: Camunda Worker - session-manager
  - Generate sessionID (UUID)
  - Generate CSRF token
  - Store session in Redis
  - Build cookie header
  - Output: {sessionId, cookieHeader}

STEP 9: API Gateway - completeLoginFlow
  - Clear old cookies (AUTH_SESSION_ID, session_id)
  - Set new cookie (AUTH_SESSION_ID)
    - HttpOnly: true
    - Secure: true
    - SameSite: None
  - Update session with UserAgent, IP
  - Log events
  - Redirect to frontend (configurable CallbackRedirectURI)
```

---

## 4. Worker Summary

| Worker | Task Type | Action | Responsibility |
|--------|----------|--------|---------------|
| keycloak-signin | keycloak-signin | initiate | Generate state + PKCE, store in Redis |
| keycloak-signin | keycloak-signin | callback | Validate state, exchange code, resolve user |
| session-manager | session-manager | create | Generate session, store in Redis |
| session-manager | session-manager | get | Retrieve session from Redis |
| session-manager | session-manager | delete | Delete session from Redis |

---

## 5. Security Review

### ✅ Implemented Correctly

| Security Feature | Implementation |
|-----------------|-----------------|
| CSRF State Validation | Redis atomic GET+DEL prevents replay attacks |
| PKCE Flow | Verifier + Challenge generated and validated |
| ID Token Validation | Handled by Keycloak provider |
| Secure Cookie | HttpOnly=true, Secure=true, SameSite=None |
| Session Fixation | New session ID created per login |

### Configuration (configs/config.yaml)

```yaml
auth:
  keycloak:
    url: "https://us-dev-api.lemici.com"
    realm: "camunda-platform"
    client_id: "lemici-frontend"
    callback_redirect_uri: "https://d595hydlunw5u.cloudfront.net/dashboard"
```

---

## 6. Breaking Points Analysis

| Component | Risk | Issue |
|----------|------|-------|
| Redis pubsub | HIGH | 3s timeout too aggressive under load |
| Workflow timeout | HIGH | 30s timeout may affect slow clients |
| State expiry | MEDIUM | 5min state TTL - user takes too long at Keycloak |
| Session race | MEDIUM | Cookie fails but session created → orphaned |
| Cookie blocked | HIGH | Safari ITP, incognito blocks cookies |
| CORS prefflight | LOW | GET /callback is simple request |

---

## 7. Under Load Behavior

| Scenario | Behavior |
|----------|----------|
| High traffic | Camunda workers parallel - horizontal scaling available |
| Redis connection | Circuit breaker fails fast, retry after 30s |
| Worker queue | Requests wait in Camunda queue |
| Concurrent logins | Each login creates NEW session - old sessions stay active |
| Memory leak | 24h TTL enforced on sessions |

---

## 8. Recommendations - Discussion Points

### Issue 1: Redis Pubsub Timeout (3 seconds)

**Current Code:**
```go
// workflow_handler.go:1611
confirmCtx, confirmCancel := context.WithTimeout(context.Background(), 3*time.Second)
defer confirmCancel()
if _, err := pubsub.ReceiveTimeout(confirmCtx, 3*time.Second); err != nil {
    h.redirectToLoginWithError(c, "service_unavailable")
    return
}
```

**Discussion:** Under load, 3s is too aggressive. User sees "service_unavailable".

| Option | Description | Tradeoff |
|-------|-------------|---------|
| A | Increase to 10s | Better resilience, slower feedback on failure |
| B | Increase to 5s | Balanced default |
| C | Add retry logic | More complex, handles temporary delays |
| D | Keep as-is | Fast fail, more 500 errors |

### Issue 2: Orphaned Sessions on New Login

**Current Behavior:** Each login creates NEW session → OLD session stays valid

**Discussion:** User logged in from 3 devices → all 3 sessions remain active.

| Option | Description | Tradeoff |
|-------|-------------|---------|
| A | Revoke old sessions on login | Security improved, unexpected session loss |
| B | Limit max sessions per user | Allow N sessions (e.g., 3 devices) |
| C | Keep as-is | Simpler, multiple concurrent sessions |

### Issue 3: Cookie Failure Handling

**Current Code:**
```go
// Silent failure - no error returned
cookie := &http.Cookie{...}
http.SetCookie(c.Writer, cookie)
```

**Discussion:** Session exists in Redis but user can't authenticate if cookie blocked.

| Option | Description | Tradeoff |
|-------|-------------|---------|
| A | Verify cookie set after SetCookie | Extra round-trip |
| B | Return cookie in JSON response | Allows JS fallback for blocked cookies |
| C | Add error on SetCookie failure | Clear logging |
| D | Keep as-is | Works for most cases |

---

## 9. Implementation Changes Made

### Commit 1: Structured Logging (da634e0)
- Replace fmt.Printf with h.logger in callback flow
- Added logging for:
  - OAuth callback received
  - Session cookie being set
  - Session fetch/update failures
  - Session initialized
  - Redirect to frontend

### Commit 2: Configurable Callback URL (238fb86)
- Add CallbackRedirectURI to Auth.Keycloak config
- Update completeLoginFlow to use config

---

## 10. Redis Implementation Architecture (CRARIFIED)

### Layer 1: Camunda/Worker Level - Redis Usage

| Component | Redis Key | Purpose | TTL |
|----------|----------|---------|-----|
| keycloak-signin | `oauth:state:{state}` | OAuth CSRF validation (one-time use) | 5min |
| session-manager | `session:{sessionId}` | Session data (userId, csrfToken, idToken, etc) | 24h |
| session-manager | `user_sessions:{userId}` | User's session set (index) | No TTL |

### Layer 2: API Gateway - completeLoginFlow

**Cookie set after successful login:**

| Setting | Value |
|--------|-------|
| Cookie Name | `AUTH_SESSION_ID` |
| HttpOnly | true |
| Secure | true |
| SameSite | None |
| MaxAge | 86400 (24h) |
| Path | / |

**Also clears old cookies:**
- `session_id` - cleared (legacy)
- `oauth_state` - cleared
- `pkce_verifier` - cleared

### Layer 3: Middleware - SessionOrJWTAuth

| Security Feature | Implementation |
|------------------|----------------|
| Session reading | Reads `AUTH_SESSION_ID` cookie |
| Session validation | Checks Redis: `session:{cookieValue}` |
| Absolute expiry | 24h from session creation |
| Idle expiry | 30min sliding window (renewed on access) |
| Session fixation | Risk score on UserAgent + IP mismatch |
| Session rotation | NEW session ID on each login (old NOT revoked) |

---

## 11. CSRF: Two Different Purposes

### CSRF 1: OAuth Flow CSRF (Camunda Worker)

**Location:** `internal/workers/auth/keycloak-signin/service.go`

**Purpose:** Validates the OAuth `state` parameter returned by Keycloak

**Implementation:**
```
Redis: GET+DEL oauth:state:{state} → returns verifier
- Atomic operation (get + delete in one call)
- One-time use - prevents replay attacks
- TTL: 5 minutes
```

**Why needed:** Prevents fake callbacks from attackers who capture a valid auth code

### CSRF 2: API Request CSRF (Middleware)

**Location:** `internal/api/middleware/csrf.go`

**Purpose:** Validates API requests after authentication

**Implementation:**
```
- CSRF token issued on each request
- Validated via X-CSRF-Token header
- Stored in Redis per session
```

**Why needed:** Prevents cross-site request forgery for authenticated API calls (POST/PUT/DELETE)

---

## 12. Cookie Names: Clarification

| File | Cookie Name Used |
|------|---------------|
| `constants.go` | `AUTH_SESSION_ID` ← CURRENT (active) |
| `middleware.go` (commented out) | `session_id` ← OLD (legacy, cleared on login) |
| `completeLoginFlow` clears | Both are cleared |

**Final cookie set after successful login:** `AUTH_SESSION_ID` only

---

## 14. Discussion Points

### Issue 1: Redis Pubsub Timeout (3 seconds)

**Current Code:**
```go
// workflow_handler.go:1611
confirmCtx, confirmCancel := context.WithTimeout(context.Background(), 3*time.Second)
defer confirmCancel()
if _, err := pubsub.ReceiveTimeout(confirmCtx, 3*time.Second); err != nil {
    h.redirectToLoginWithError(c, "service_unavailable")
    return
}
```

**Discussion:** Under load, 3s is too aggressive. User sees "service_unavailable".

| Option | Description | Tradeoff |
|-------|-------------|---------|
| A | Increase to 10s | Better resilience, slower feedback on failure |
| B | Increase to 5s | Balanced default |
| C | Add retry logic | More complex, handles temporary delays |
| D | Keep as-is | Fast fail, more 500 errors |

### Issue 2: Orphaned Sessions on New Login

**Current Behavior:** Each login creates NEW session → OLD session stays valid

**Discussion:** User logged in from 3 devices → all 3 sessions remain active.

| Option | Description | Tradeoff |
|-------|-------------|---------|
| A | Revoke old sessions on login | Security improved, unexpected session loss |
| B | Limit max sessions per user | Allow N sessions (e.g., 3 devices) |
| C | Keep as-is | Simpler, multiple concurrent sessions |

### Issue 3: Cookie Failure Handling

**Current Code:**
```go
// Silent failure - no error returned
cookie := &http.Cookie{...}
http.SetCookie(c.Writer, cookie)
```

**Discussion:** Session exists in Redis but user can't authenticate if cookie blocked.

| Option | Description | Tradeoff |
|-------|-------------|---------|
| A | Verify cookie set after SetCookie | Extra round-trip |
| B | Return cookie in JSON response | Allows JS fallback for blocked cookies |
| C | Add error on SetCookie failure | Clear logging |
| D | Keep as-is | Works for most cases |

---

## 15. Implementation Changes Made

### Commit 1: Structured Logging (da634e0)
- Replace fmt.Printf with h.logger in callback flow
- Added logging for:
  - OAuth callback received
  - Session cookie being set
  - Session fetch/update failures
  - Session initialized
  - Redirect to frontend

### Commit 2: Configurable Callback URL (238fb86)
- Add CallbackRedirectURI to Auth.Keycloak config
- Update completeLoginFlow to use config

---

## 16. Files Modified

| File | Changes |
|------|---------|
| `cmd/api-gateway/main.go` | Route registration |
| `internal/api/handlers/workflow_handler.go` | Login flow, callback handler |
| `internal/workers/auth/keycloak-signin/service.go` | State validation, token exchange |
| `internal/workers/auth/session-manager/service.go` | Session creation |
| `internal/common/config/config.go` | Added CallbackRedirectURI |
| `configs/config.yaml` | Added callback_redirect_uri |
| `bpmn/keycloak-login-workflow.bpmn` | Workflow definition |

---

## 17. Production Readiness Checklist

- [x] Route registration verified
- [x] State validation implemented (CSRF protected)
- [x] PKCE flow implemented
- [x] Cookie security (HttpOnly, Secure, SameSite)
- [x] Session storage (Redis with TTL)
- [x] Error handling (boundary events in BPMN)
- [ ] Pubsub timeout review needed
- [ ] Session limit review needed
- [ ] Cookie fallback review needed
- [ ] Load testing required

---

*End of Report*