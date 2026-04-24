# Middleware Session Implementation - Production Readiness Analysis

**Date:** April 24, 2026
**Focus:** Current middleware session handling and production readiness

---

## 1. Current Implementation Analysis

### 1.1 SessionOrJWTAuth (middleware/jwt.go:692)

**What It Does:**

```go
func SessionOrJWTAuth(jwtConfig config.JWTConfig, redisClient *redis.Client) gin.HandlerFunc
```

**Current Flow:**
1. Read `AUTH_SESSION_ID` cookie
2. Get session from Redis: `session:{sessionId}`
3. Validate absolute expiry (24h from creation)
4. Validate idle expiry (30min sliding window)
5. Risk-based validation (UserAgent + IP mismatch)
6. Update session with new expiry
7. Attach to context

### 1.2 What's Working ✅

| Feature | Implementation | Location |
|---------|---------------|----------|
| Session cookie reading | `c.Cookie(constants.SessionCookieName)` | Line 699 |
| Session retrieval | `redisClient.Get("session:"+cookie)` | Line 702 |
| Absolute expiry check | `now.After(sess.AbsoluteExpiresAt)` | Line 723 |
| Idle expiry check | `now.After(sess.ExpiresAt)` | Line 743 |
| UserAgent validation | `normalizeUserAgent()` | Line 776-787 |
| IP validation | `sameIPSubnet()` | Line 790-795 |
| Risk scoring | Risk ≥ 3 blocks session | Line 815 |
| Session refresh | Updates TTL on access | Line 837-875 |
| Context attachment | `c.Set("userId", sess.UserID)` | Line 879 |
| Cookie clearing on expiry | Uses `http.SameSiteNoneMode` | Line 727 |

### 1.3 What's Missing ❌

| Feature | Priority | Impact |
|---------|----------|--------|
| User status check (DB) | 🔴 CRITICAL | Suspended users can still use session |
| CSRF validation | 🟡 MEDIUM | API requests not protected |
| Login attempt audit | 🟡 MEDIUM | No audit trail |
| Max session limit | 🟡 MEDIUM | Unlimited sessions per user |
| Specific error codes | 🟡 MEDIUM | Generic errors |
| Server-side session listing | 🟡 MEDIUM | Can't list user's sessions |

### 1.4 Security Gap: No User Status Check

**Current Code:** Line 707-879
```go
if err == nil {
    var sess session.Session
    if err := json.Unmarshal([]byte(val), &sess); err != nil {
        // ... error handling
    } else {
        // ❌ MISSING: No user status check here!
        now := time.Now()
        // ... rest of validation
        c.Set("userId", sess.UserID)
        c.Set("sessionId", sess.SessionID)
    }
}
```

**What Should Happen:**
```go
// After session validation, BEFORE context attachment:
// 1. Fetch user from DB
user, err := userRepo.FindByID(sess.UserID)
if err != nil {
    return err // USER_NOT_FOUND
}

// 2. Check user status
switch user.Status {
case "suspended":
    return ErrUserSuspended
case "inactive":
    return ErrUserInactive
case "pending":
    return ErrUserPending
case "active":
    // Continue
}
```

---

## 2. Session Data Flow

### 2.1 Session Creation (session-manager/service.go)

**Location:** `internal/workers/auth/session-manager/service.go:59-130`

```go
func (s *Service) handleCreate(ctx context.Context, input *Input) (*Output, error) {
    // 1. Generate session ID
    sessionID, err := session.GenerateID()  // UUID
    
    // 2. Generate CSRF token
    csrfToken, err := generateCSRFToken()   // 32 bytes random
    
    // 3. Create session struct
    sess := session.Session{
        SessionID:         sessionID,
        UserID:             input.UserID,
        KeycloakUserID:     input.KeycloakUserID,
        IDToken:            input.IDToken,
        CreatedAt:          now,
        AbsoluteExpiresAt:  now.Add(24*time.Hour),
        ExpiresAt:          now.Add(30*time.Minute),
        Version:            1,
        CSRFToken:          csrfToken,
    }
    
    // 4. Store in Redis (via RedisStore)
    if err := s.sessionStore.Create(ctx, sess); err != nil {
        return nil, err
    }
    
    // 5. Build cookie header
    cookieHeader := s.buildSetCookieHeader(sessionID, expiresAt)
}
```

### 2.2 Redis Session Storage (redis_store.go)

**Location:** `internal/common/auth/session/redis_store.go:30-58`

```go
func (r *RedisStore) Create(ctx context.Context, s Session) error {
    // 1. Validate inputs
    if s.SessionID == "" || s.UserID == "" {
        return fmt.Errorf("session: missing session_id or user_id")
    }
    
    // 2. Calculate TTL
    ttl := time.Until(s.ExpiresAt)
    if ttl <= 0 {
        return fmt.Errorf("session: expires_at must be in the future")
    }
    
    // 3. Marshal session data
    data, err := json.Marshal(s)
    if err != nil {
        return fmt.Errorf("session: failed to marshal: %w", err)
    }
    
    // 4. Pipeline: SET session + SADD to user index
    pipe := r.client.TxPipeline()
    pipe.Set(ctx, r.key(s.SessionID), data, ttl)
    pipe.SAdd(ctx, r.userKey(s.UserID), s.SessionID)
    
    log.Printf("[SESSION_CREATE] sid=%s user_id=%s expires_at=%s",
        s.SessionID, s.UserID, s.ExpiresAt.UTC())
    
    // 5. Execute
    _, err = pipe.Exec(ctx)
    return err
}
```

### 2.3 Session Deletion (redis_store.go:77-99)

```go
func (r *RedisStore) Delete(ctx context.Context, sessionID string) error {
    // 1. Fetch session to get user_id
    s, err := r.Get(ctx, sessionID)
    if err != nil {
        return err
    }
    if s == nil {
        return nil // idempotent
    }
    
    // 2. Pipeline: DEL session + SREM from user index
    pipe := r.client.TxPipeline()
    pipe.Del(ctx, r.key(sessionID))
    pipe.SRem(ctx, r.userKey(s.UserID), sessionID)
    
    _, err = pipe.Exec(ctx)
    return err
}
```

---

## 3. Session Model

### 3.1 Session Struct (session/store.go)

```go
type Session struct {
    SessionID          string    `json:"session_id"`
    UserID            string    `json:"user_id"`
    CreatedAt         time.Time `json:"created_at"`
    ExpiresAt         time.Time `json:"expires_at"`         // Idle expiry (30min)
    AbsoluteExpiresAt time.Time `json:"absolute_expires_at"` // Hard limit (24h)
    Version           int       `json:"version"`            // For session rotation
    CSRFToken         string    `json:"csrf_token"`        // For API protection
    KeycloakUserID   string    `json:"keycloak_user_id"`
    IDToken          string    `json:"id_token"`          // OIDC token
    UserAgent        string    `json:"user_agent"`
    IP               string    `json:"ip"`
}
```

---

## 4. Middleware Risk Validation

### 4.1 UserAgent Validation (middleware/jwt.go:776-788)

```go
// --- UserAgent check ---
if sess.UserAgent != "" && currentUA != "" {
    storedUA = normalizeUserAgent(sess.UserAgent)
    currentUAParsed = normalizeUserAgent(currentUA)
    
    if storedUA != "unknown" && currentUAParsed != "unknown" && storedUA != currentUAParsed {
        uaMismatch = true
        riskScore += 2
    }
    
    if isUnknownUA(currentUAParsed) {
        riskScore += 1
    }
}
```

### 4.2 IP Validation (middleware/jwt.go:790-796)

```go
// --- IP check ---
if sess.IP != "" && currentIP != "" {
    if !sameIPSubnet(sess.IP, currentIP) {
        ipMismatch = true
        riskScore += 1
    }
}
```

### 4.3 Risk Decision (middleware/jwt.go:815)

```go
// --- Decision ---
if riskScore >= 3 {
    // Delete session, return 401
    respondWithError(c, http.StatusUnauthorized, "AUTH_SESSION_INVALID", "session risk detected", nil)
    c.Abort()
    return
}
```

---

## 5. Redis Keys Structure

### Current Keys:

| Key | Type | TTL | Purpose |
|-----|------|-----|---------|
| `session:{sessionId}` | String (JSON) | 24h | Session data |
| `user_sessions:{userId}` | SET | None | User's session IDs |

### Required New Keys:

| Key | Type | TTL | Purpose |
|-----|------|-----|---------|
| `session_meta:{sessionId}` | String (JSON) | 24h | Session metadata |
| `login_audit:{userId}` | LIST | 30 days | Login attempts |

---

## 6. Cookie Configuration

### Current Cookie Settings (constants.go)

```go
const (
    SessionCookieName     = "AUTH_SESSION_ID"
    SessionCookiePath     = "/"
    SessionCookieSecure   = true
    SessionCookieHTTPOnly = true
)
```

### Cookie Set in completeLoginFlow:

```go
cookie := &http.Cookie{
    Name:     constants.SessionCookieName,
    Value:    sessionID,
    Path:     "/",
    Domain:   "",
    MaxAge:   86400,  // 24 hours
    HttpOnly: true,
    Secure:   true,
    SameSite: http.SameSiteNoneMode,  // Required for cross-origin
}
```

---

## 7. Production Readiness Checklist

### ✅ Working / Tested

| Feature | Status | Notes |
|---------|--------|-------|
| Session creation | ✅ | Generated with UUID + CSRF |
| Session retrieval | ✅ | JSON unmarshal |
| Absolute expiry | ✅ | 24h hard limit |
| Idle expiry | ✅ | 30min sliding window |
| Risk validation | ✅ | UserAgent + IP mismatch |
| Session refresh | ✅ | TTL updated on access |
| Cookie security | ✅ | HttpOnly, Secure, SameSite=None |
| Redis TTL | ✅ | Auto-expiring |

### ❌ Missing / Needs Work

| Feature | Priority | Status |
|---------|----------|--------|
| User status check | 🔴 HIGH | ❌ NOT IMPLEMENTED |
| CSRF token validation | 🟡 MEDIUM | ❌ NOT VALIDATED IN MIDDLEWARE |
| Login audit | 🟡 MEDIUM | ❌ NOT TRACKED |
| Max session limit | 🟡 MEDIUM | ❌ NOT ENFORCED |
| Session listing API | 🟡 MEDIUM | ❌ NOT AVAILABLE |

---

## 8. Error Handling Gaps

### Current Errors:

| Error Code | When Triggered |
|------------|---------------|
| AUTH_SESSION_EXPIRED | Absolute/Idle timeout |
| AUTH_SESSION_INVALID | Risk score ≥ 3 |
| AUTH_REQUIRED | No cookie and no JWT |
| AUTH_INVALID_TOKEN | JWT validation fails |

### Missing Errors:

| Error Code | When Should Trigger |
|------------|---------------------|
| USER_SUSPENDED | User status = suspended |
| USER_INACTIVE | User status = inactive |
| USER_PENDING | User status = pending |
| USER_NOT_FOUND | User ID not in DB |
| SESSION_NOT_FOUND | Cookie but no session in Redis |

---

## 9. Implementation Priority

### Phase 1 (Immediate - Production Risk)

| Task | Priority | Effort |
|------|----------|--------|
| Add user status check in middleware | 🔴 4 hours |
| Add error codes for user status | 🟡 2 hours |
| Add tests for user status blocking | 🟡 3 hours |

### Phase 2 (Short Term)

| Task | Priority | Effort |
|------|----------|--------|
| Add CSRF validation in middleware | 🟡 4 hours |
| Add login audit trail | 🟡 6 hours |
| Add max session enforcement | 🟡 4 hours |

### Phase 3 (Medium Term)

| Task | Priority | Effort |
|------|----------|--------|
| Add session listing API | 🟡 4 hours |
| Admin session Kill | 🟡 4 hours |
| Load testing | 🟡 8 hours |

---

## 10. Summary

| Metric | Current | Required | Gap |
|--------|---------|----------|-----|
| Session validation | ✅ Complete | ✅ Complete | - |
| User status validation | ❌ Missing | ✅ Required | 🔴 HIGH |
| Risk-based validation | ✅ Complete | ✅ Complete | - |
| Session rotation | ✅ Complete | ✅ Complete | - |
| Cookie security | ✅ Complete | ✅ Complete | - |
| Audit logging | ❌ Missing | ✅ Required | 🟡 MEDIUM |
| Max sessions | ❌ Missing | ✅ Required | 🟡 MEDIUM |

---

*End of Middleware Analysis*

**Last Updated:** April 24, 2026
**Status:** READY FOR IMPLEMENTATION

---

## Section 1: Executive Summary

This document provides a detailed implementation roadmap for:
1. Enhanced session management (multi-session per user)
2. User status integration with session validation
3. Login attempt auditing
4. Admin user management capabilities
5. Complete logout/scns scenarios

**Key Design Decisions:**
- Max 5 sessions per user (configurable 4-6 range)
- On suspension/deactivation: ALL sessions immediately revoked
- Login attempts tracked in Redis (separate from Keycloak brute force protection)
- Admin can only perform "logout all" (not specific session)

---

## Section 2: Current Implementation Analysis

### 2.1 Existing Redis Structure

| Key | Structure | TTL | Purpose |
|-----|-----------|-----|---------|
| `session:{sessionId}` | JSON Session | 24h | Session data |
| `user_sessions:{userId}` | SET of sessionIds | None | User's sessions index |

### 2.2 Current Session Flow (middleware/jwt.go)

```
SessionOrJWTAuth
  1. Read AUTH_SESSION_ID cookie
  2. GET session:{cookie} from Redis
  3. Validate absolute expiry (24h)
  4. Validate idle expiry (30min sliding)
  5. Risk check (UserAgent + IP mismatch)
  6. ❌ MISSING: User status check
  7. Attach to context
```

### 2.3 Current User Status (models/user.go)

```go
type User struct {
    Status string `json:"status" db:"status"` 
    // Values: active, inactive, suspended, pending
}
```

**Validation:** `ozzo.In("active", "inactive", "suspended", "pending")` (line 101)

**Status Meanings:**

| Status | Can Login? | Description |
|--------|------------|-------------|
| active | ✅ YES | Normal user |
| pending | ❌ NO | Email not verified |
| suspended | ❌ NO | Temporarily disabled |
| inactive | ❌ NO | Account deactivated |

---

## Section 3: Redis Store Enhancements

### 3.1 Updated RedisStore (redis_store.go)

**New Methods:**

```go
// GetAllSessionsForUser returns all session IDs for a user
func (r *RedisStore) GetAllSessionsForUser(ctx context.Context, userID string) ([]string, error)

// DeleteAllUserSessions deletes every session for a user
func (r *RedisStore) DeleteAllUserSessions(ctx context.Context, userID string) error

// GetSessionCount returns number of active sessions
func (r *RedisStore) GetSessionCount(ctx context.Context, userID string) (int, error)

// EnforceMaxSessions removes oldest sessions over limit
func (r *RedisStore) EnforceMaxSessions(ctx context.Context, userID string, maxSessions int) error

// RecordLoginAttempt stores login attempt for audit
func (r *RedisStore) RecordLoginAttempt(ctx context.Context, userID string, attempt LoginAttempt) error

// GetLoginAttempts returns recent login attempts
func (r *RedisStore) GetLoginAttempts(ctx context.Context, userID string, limit int) ([]LoginAttempt, error)
```

**Login Attempt Model:**

```go
type LoginAttempt struct {
    Timestamp    time.Time `json:"timestamp"`
    IP           string    `json:"ip"`
    UserAgent   string    `json:"userAgent"`
    Success     bool      `json:"success"`
    FailureReason string   `json:"failureReason,omitempty"`
}
```

### 3.2 New Redis Keys

| Key | Structure | TTL | Purpose |
|-----|-----------|-----|---------|
| `session:{sessionId}` | JSON Session | 24h | Session data |
| `user_sessions:{userId}` | SET | None | User sessions index |
| `session_meta:{sessionId}` | JSON Metadata | 24h | Device, IP, location |
| `login_audit:{userId}` | LIST | 30 days | Login attempts audit |

---

## Section 4: Middleware Enhancements

### 4.1 Updated SessionOrJWTAuth Flow

```
SessionOrJWTAuth
  1. Read AUTH_SESSION_ID cookie
  2. GET session:{cookie} from Redis
  3. Validate absolute expiry (24h)
     → If expired: Clear cookie, 401 AUTH_SESSION_EXPIRED
  4. Validate idle expiry (30min sliding)
     → If expired: Clear cookie, 401 AUTH_SESSION_EXPIRED
  5. Risk-based validation
     - UserAgent mismatch: +2 risk points
     - IP mismatch: +1 risk point
     - Risk ≥ 3: Delete session, 401 AUTH_SESSION_INVALID
  6. ❌ NEW: Fetch user from DB by user ID
  7. ❌ NEW: Validate user status
     - if "suspended": 403 USER_SUSPENDED
     - if "inactive": 403 USER_INACTIVE  
     - if "pending": 403 USER_PENDING_EMAIL
  8. ❌ NEW: Validate CSRF token for state-changing requests
  9. Attach to context with all user data
```

### 4.2 Error Response Codes

| HTTP | Code | Message |
|------|------|---------|
| 401 | AUTH_SESSION_EXPIRED | Session expired |
| 401 | AUTH_SESSION_INVALID | Suspicious activity detected |
| 403 | USER_SUSPENDED | Account suspended. Contact support |
| 403 | USER_INACTIVE | Account deactivated |
| 403 | USER_PENDING_EMAIL | Verify email to continue |

---

## Section 5: Events & Actions Matrix

### 5.1 Session Lifecycle Events

| Event | Session Action | User Status Action |
|-------|----------------|-------------------|
| User login | Create new session | None |
| User logout | Delete current | None |
| User logout-all | Delete ALL user sessions | None |
| Session idle timeout | Delete | None |
| Session absolute timeout | Delete | None |
| Password reset | Delete ALL sessions | None |
| User changed password | Delete ALL sessions | None |

### 5.2 Admin Actions

| Admin Action | Session Action | User Status Action |
|--------------|----------------|-------------------|
| Suspend user | Delete ALL sessions | Set status = suspended |
| Deactivate user | Delete ALL sessions | Set status = inactive |
| Reactivate user | None | Set status = active |
| Terminate all sessions | Delete ALL sessions | None |

---

## Section 6: Implementation Phases

### Phase 1: Database & User Status (2 hours)

| Task | File | Change |
|------|------|--------|
| Ensure migration adds status column | `migrations/*.sql` | Add IF NOT EXISTS |
| Set default status = 'active' on user create | Workers | Default value |

**Migration (if not exists):**
```sql
ALTER TABLE users 
ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'active' 
CHECK (status IN ('active', 'inactive', 'suspended', 'pending'));
```

### Phase 2: Redis Store Enhancements (8 hours)

| Task | File | Change |
|------|------|--------|
| GetAllSessionsForUser | `redis_store.go` | New method |
| DeleteAllUserSessions | `redis_store.go` | New method |
| GetSessionCount | `redis_store.go` | New method |
| EnforceMaxSessions | `redis_store.go` | New method |
| RecordLoginAttempt | `redis_store.go` | New method |
| GetLoginAttempts | `redis_store.go` | New method |
| Store interface update | `store.go` | Add method signatures |

**Login Audit Key:** `login_audit:{userId}` - LIST, TTL 30 days
**Max retention:** 100 attempts per user

### Phase 3: Middleware Enhancement (8 hours)

| Task | File | Change |
|------|------|--------|
| Add user status check | `middleware/jwt.go` | After session validation |
| Add proper error codes | `middleware/jwt.go` | New error constants |
| Add CSRF validation | `middleware/csrf.go` | Already exists, verify integration |
| Update response messages | `middleware/jwt.go` | User-friendly messages |

### Phase 4: Admin Endpoints (8 hours)

| Endpoint | Handler | Purpose |
|----------|---------|---------|
| `PUT /api/v1/admin/user/:id/status` | `handlers/user_handler.go` | Change user status |
| `GET /api/v1/admin/user/:id/sessions` | `handlers/user_handler.go` | List all user sessions |
| `DELETE /api/v1/admin/user/:id/sessions` | `handlers/user_handler.go` | Kill all sessions |
| Register routes | `main.go` | Add routes |

**Status Change Logic (admin):**
```
PUT /admin/user/:id/status
  Body: {status: "suspended"|"inactive"|"active"}
  
  If status == "suspended" AND current != "suspended":
    - Update user status to "suspended"
    - Delete ALL user sessions (via RedisStore.DeleteAllUserSessions)
    - Log admin action
    
  If status == "inactive" AND current != "inactive":
    - Update user status to "inactive"
    - Delete ALL user sessions
    - Log admin action
    
  If status == "active" AND (current == "suspended" OR current == "inactive"):
    - Update user status to "active"
    - (User must re-login to create new session)
    - Log admin action
```

### Phase 5: Logout Workflows (10 hours)

| Workflow | Worker | Changes |
|----------|--------|---------|
| Logout current | session-manager | Already exists |
| Logout-all | session-manager | Add action="delete_all" |
| Password reset triggers logout | session-manager | Listen for password change event |

**Delete All Sessions (session-manager/service.go):**
```go
func (s *Service) handleDeleteAll(ctx context.Context, input *Input) (*Output, error) {
    sessions, err := s.sessionStore.GetAllSessionsForUser(ctx, input.UserID)
    if err != nil {
        return nil, err
    }
    
    deleted := 0
    for _, sessionID := range sessions {
        if err := s.sessionStore.Delete(ctx, sessionID); err == nil {
            deleted++
        }
    }
    
    return &Output{
        Success:    true,
        Message:   fmt.Sprintf("Deleted %d sessions", deleted),
    }, nil
}
```

### Phase 6: Testing (12 hours)

| Test Type | Coverage |
|-----------|-----------|
| Unit tests | RedisStore new methods |
| Integration | Middleware flow |
| Admin endpoints | Status changes |
| Logout flows | All scenarios |
| Login audit | Recording & retrieval |
| Load test | Concurrent sessions |

---

## Section 7: Configuration (configs/config.yaml)

```yaml
auth:
  session:
    max_sessions_per_user: 5    # Range 4-6
    allow_concurrent_logins: true
    idle_timeout_minutes: 30
    absolute_timeout_hours: 24
    
  session_status:
    check_on_login: true
    check_on_api_request: true
    
  login_audit:
    enabled: true
    max_attempts_retention: 100
    retention_days: 30
    
permissions:
  admin_can_suspend: true
  admin_can_deactivate: true
  admin_can_terminate_sessions: true
  admin_can_reactivate: true
```

---

## Section 8: API Reference

### 8.1 Session Response (enhanced)

```json
{
  "success": true,
  "sessionId": "uuid",
  "userId": "uuid",
  "email": "user@example.com",
  "status": "active",
  "activeSessions": 3,
  "maxSessions": 5,
  "expiresAt": "2026-04-25T18:00:00Z"
}
```

### 8.2 Admin User Sessions Response

```json
{
  "userId": "uuid",
  "email": "user@example.com",
  "status": "active",
  "sessions": [
    {
      "sessionId": "uuid",
      "createdAt": "2026-04-24T10:00:00Z",
      "expiresAt": "2026-04-25T10:00:00Z",
      "ip": "192.168.1.1",
      "userAgent": "Chrome/120.0",
      "lastActivity": "2026-04-24T18:00:00Z"
    }
  ],
  "totalSessions": 1
}
```

### 8.3 Login Audit Response

```json
{
  "userId": "uuid",
  "attempts": [
    {
      "timestamp": "2026-04-24T18:00:00Z",
      "ip": "192.168.1.1",
      "userAgent": "Chrome/120.0",
      "success": false,
      "failureReason": "invalid_password"
    },
    {
      "timestamp": "2026-04-24T18:01:00Z", 
      "ip": "192.168.1.1",
      "userAgent": "Chrome/120.0",
      "success": true
    }
  ]
}
```

---

## Section 9: Implementation Checklist

- [ ] Phase 1: Database setup
- [ ] Phase 2: Redis store enhancements  
- [ ] Phase 3: Middleware user status check
- [ ] Phase 4: Admin endpoints
- [ ] Phase 5: Logout-all workflow
- [ ] Phase 6: Testing

---

## Section 10: Discussion Points Resolution

| Question | Resolution |
|----------|------------|
| Max sessions | 5 (default, range 4-6 configurable) |
| On suspension | Immediately revoke ALL sessions |
| Login attempts | Track in Redis audit list |
| Admin force logout | All sessions only (not specific) |

---

*End of Implementation Roadmap*