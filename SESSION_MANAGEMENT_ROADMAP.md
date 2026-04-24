# Session Management & User Status - Implementation Roadmap

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