# Auth Logout Worker

## Description
Handles user logout operations with support for single-session and global (all devices) logout.

## Activity Type
`auth.logout`

## Features
- ✅ Single session logout (revoke specific refresh token)
- ✅ Global logout (revoke all user sessions)
- ✅ Keycloak integration for token revocation
- ✅ Redis session cleanup
- ✅ Access token revocation list
- ✅ Audit trail logging (90-day retention)

## Dependencies
- **Keycloak**: For OAuth token revocation
- **Redis**: For session management and audit logging (optional)
- **Camunda**: For workflow orchestration

## Input Schema

### Required Fields
- `userId` (string): Keycloak user ID

### Optional Fields
- `refreshToken` (string): Refresh token to revoke (required for single session logout)
- `accessToken` (string): Access token to add to revocation list
- `sessionId` (string): Local session ID to invalidate
- `deviceId` (string): Device identifier for audit logging
- `logoutAll` (boolean): If true, logout from all sessions
- `reason` (string): Logout reason (e.g., "user_initiated", "password_changed")
- `metadata` (object): Additional audit metadata

## Output Schema
```json
{
  "success": true,
  "message": "Logged out successfully",
  "sessionsInvalidated": 1,
  "tokenRevoked": true,
  "logoutAt": "2025-12-11T10:30:00Z"
}
```

## Usage Examples

### Single Session Logout
```json
{
  "userId": "user-123",
  "refreshToken": "rt_abc123...",
  "accessToken": "at_xyz789...",
  "sessionId": "sess-456",
  "deviceId": "device-iphone-12",
  "reason": "user_initiated"
}
```

### Global Logout (All Devices)
```json
{
  "userId": "user-123",
  "logoutAll": true,
  "reason": "password_changed"
}
```

## Architecture

### Logout Flow

<!-- # Auth Logout Worker

## Description
Handles user logout and session invalidation in Keycloak.

## Activity Type
`auth.logout`

## Owner
auth-team

## Input Schema
```json
{
  "type": "object",
  "required": ["userId"],
  "properties": {
    "userId": {
      "type": "string",
      "description": "User ID to logout"
    },
    "token": {
      "type": "string",
      "description": "Optional - User's current token"
    }
  }
}
```

## Output Schema
```json
{
  "type": "object",
  "properties": {
    "success": {
      "type": "boolean",
      "description": "Logout success status"
    },
    "message": {
      "type": "string",
      "description": "Status message"
    }
  }
}
```

## Usage Example

### Start Workflow with Logout
```bash
zbctl create instance user-logout-process \
  --variables '{
    "userId": "user-12345",
    "token": "optional-token-string"
  }' \
  --insecure
```

### Expected Flow
1. Receive logout job from Zeebe
2. Validate userId
3. Invalidate session in Keycloak
4. Return success status

## Testing

### Unit Tests
```bash
cd workers/auth-logout
go test ./tests/... -v
```

### Integration Test
```bash
# Start services
docker-compose -f docker-compose-test.yml up -d

# Run test
go test ./tests/... -v -tags=integration

# Cleanup
docker-compose -f docker-compose-test.yml down
```

## Monitoring

### Metrics
- `jobs_processed_total{worker="auth-logout",status="success|failed"}`
- `jobs_duration_seconds{worker="auth-logout"}`

### Logs
```bash
# View worker logs
docker logs -f worker-auth-logout

# Filter for errors
docker logs worker-auth-logout 2>&1 | grep ERROR
```

## Troubleshooting

### Worker not processing jobs
- Check Zeebe connectivity: `nc -zv zeebe 26500`
- Verify job type matches: `auth.logout`
- Check worker logs for errors

### Keycloak errors
- Verify KEYCLOAK_URL is accessible
- Check client credentials are correct
- Ensure realm exists

## Related Workers
- auth-signup-google
- auth-signin-google
- auth-signup-linkedin
- auth-signin-linkedin -->
