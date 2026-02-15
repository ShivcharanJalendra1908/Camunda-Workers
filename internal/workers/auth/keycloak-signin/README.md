# Keycloak Signin Worker

Handles OAuth 2.0 + OIDC authentication flow with Keycloak.

## Task Type
`auth.keycloak-signin`

## Actions

### 1. Initiate Login
**Input:**
```json
{
  "action": "initiate"
}
```

**Output:**
```json
{
  "authorizationUrl": "http://keycloak/realms/auth-service/protocol/openid-connect/auth?...",
  "state": "random-state-value"
}
```

### 2. Handle Callback
**Input:**
```json
{
  "action": "callback",
  "code": "authorization-code",
  "state": "state-from-initiate"
}
```

**Output:**
```json
{
  "success": true,
  "userId": "uuid",
  "email": "user@example.com",
  "emailVerified": true,
  "isNewUser": false,
  "keycloakUserId": "keycloak-sub",
  "authenticatedAt": "2024-01-01T00:00:00Z"
}
```

## Dependencies
- Keycloak Provider (from auth-service)
- DBResolver (from auth-service)
- Redis (for state storage)
- PostgreSQL (for user resolution)

## Configuration
```yaml
workers:
  keycloak-signin:
    enabled: true
    maxJobsActive: 10
    timeout: 30000
    issuer: "http://localhost:8081/realms/auth-service"
    clientId: "frontend-app"
    redirectUrl: "http://localhost:3000/callback"
    publicBaseUrl: "http://localhost:8081"
```