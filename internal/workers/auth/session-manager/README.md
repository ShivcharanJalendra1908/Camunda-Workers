# Session Manager Worker

Handles session lifecycle: create, retrieve, and delete sessions.

## Task Type
`auth.session-manager`

## Actions

### 1. Create Session
**Input:**
```json
{
  "action": "create",
  "userId": "user-uuid",
  "email": "user@example.com",
  "expiresIn": 86400
}
```

**Output:**
```json
{
  "success": true,
  "sessionId": "session-uuid",
  "userId": "user-uuid",
  "expiresAt": "2024-01-02T00:00:00Z",
  "cookieHeader": "Set-Cookie: session_id=...; HttpOnly; Secure; SameSite=Lax"
}
```

### 2. Get Session
**Input:**
```json
{
  "action": "get",
  "sessionId": "session-uuid"
}
```

**Output:**
```json
{
  "success": true,
  "sessionId": "session-uuid",
  "userId": "user-uuid",
  "expiresAt": "2024-01-02T00:00:00Z"
}
```

### 3. Delete Session
**Input:**
```json
{
  "action": "delete",
  "sessionId": "session-uuid"
}
```

**Output:**
```json
{
  "success": true,
  "sessionId": "session-uuid",
  "cookieHeader": "Set-Cookie: session_id=; Max-Age=-1"
}
```

## Dependencies
- Redis (session storage)
- auth-service/internal/session (RedisStore)

## Configuration
```yaml
workers:
  session-manager:
    enabled: true
    maxJobsActive: 10
    timeout: 10000
    defaultTtl: 86400000  # 24 hours in ms
    cookieName: "session_id"
    secure: true
    httpOnly: true
    sameSite: "Lax"
```