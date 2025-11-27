# Auth Logout Worker

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
- auth-signin-linkedin
