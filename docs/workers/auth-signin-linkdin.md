# LinkedIn Sign-In Worker

**Activity ID:** `auth.signin.linkedin`  
**Version:** 1.0.0  
**Category:** Authentication  
**Owner:** Auth Team

## Overview

Authenticates users via LinkedIn OAuth 2.0 protocol, retrieves user profile information, and creates or updates the user account in Keycloak identity management system.

## Task Type
```
auth.signin.linkedin
```

## Input Parameters

| Parameter | Type | Required | Constraints | Description |
|-----------|------|----------|-------------|-------------|
| `authCode` | string | Yes | 10-1000 chars | LinkedIn OAuth 2.0 authorization code obtained from OAuth flow |
| `email` | string | No | Valid email format | Optional email hint for user identification |

### Input Example
```json
{
  "authCode": "AQT5xK3vN...",
  "email": "user@example.com"
}
```

## Output Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `success` | boolean | Authentication result (always true on success) |
| `userId` | string | Keycloak user UUID |
| `email` | string | User email address from LinkedIn |
| `firstName` | string | User first name from LinkedIn profile |
| `lastName` | string | User last name from LinkedIn profile |
| `token` | string | LinkedIn OAuth access token |
| `timestamp` | string | ISO 8601 timestamp of completion |

### Output Example
```json
{
  "success": true,
  "userId": "a3f8e7b2-4c9d-4e5a-b8c1-3f4a5e6b7c8d",
  "email": "john.doe@example.com",
  "firstName": "John",
  "lastName": "Doe",
  "token": "AQVxK3v...",
  "timestamp": "2024-11-14T10:30:00Z"
}
```

## Error Codes

| Code | Description | Retryable | HTTP Analog |
|------|-------------|-----------|-------------|
| `VALIDATION_FAILED` | Input validation failed | No | 400 |
| `MISSING_PARAMETER` | Required parameter missing | No | 400 |
| `LINKEDIN_OAUTH_ERROR` | OAuth token exchange failed | Yes | 502 |
| `LINKEDIN_API_ERROR` | LinkedIn API request failed | Yes | 502 |
| `KEYCLOAK_ERROR` | Keycloak operation failed | No | 500 |
| `INTERNAL_ERROR` | Internal server error | No | 500 |

## BPMN Integration

### Service Task Configuration
```xml
<bpmn:serviceTask id="LinkedInSignIn" name="LinkedIn Sign-In">
  <bpmn:extensionElements>
    <zeebe:taskDefinition type="auth.signin.linkedin" />
    <zeebe:ioMapping>
      <zeebe:input source="=authCode" target="authCode" />
      <zeebe:input source="=userEmail" target="email" />
      <zeebe:output source="=userId" target="userId" />
      <zeebe:output source="=token" target="accessToken" />
    </zeebe:ioMapping>
  </bpmn:extensionElements>
</bpmn:serviceTask>
```

### Error Handling Example
```xml
<bpmn:boundaryEvent id="LinkedInError" attachedToRef="LinkedInSignIn">
  <bpmn:errorEventDefinition errorRef="LINKEDIN_OAUTH_ERROR" />
</bpmn:boundaryEvent>
```

## Implementation Details

### External Dependencies
- LinkedIn OAuth API v2
- Keycloak Admin API
- Go OAuth2 library

### Retry Strategy
- LinkedIn API errors: 3 retries with exponential backoff
- Network timeouts: Auto-retry by Camunda
- Validation errors: No retry

### Performance
- Expected latency: 200-500ms
- Timeout: 30 seconds
- Concurrent executions: Unlimited

## Testing
```bash
# Run unit tests
go test -v ./internal/workers/auth/auth-signin-linkedin/

# Run with coverage
go test -v -cover ./internal/workers/auth/auth-signin-linkedin/
```

## Configuration

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LINKEDIN_CLIENT_ID` | Yes | - | LinkedIn OAuth client ID |
| `LINKEDIN_CLIENT_SECRET` | Yes | - | LinkedIn OAuth client secret |
| `LINKEDIN_REDIRECT_URL` | Yes | - | OAuth redirect URL |
| `KEYCLOAK_URL` | Yes | - | Keycloak server URL |
| `KEYCLOAK_REALM` | Yes | - | Keycloak realm name |
| `KEYCLOAK_CLIENT_ID` | Yes | - | Keycloak client ID |
| `KEYCLOAK_CLIENT_SECRET` | Yes | - | Keycloak client secret |

## Change Log

### v1.0.0 (2024-11-14)
- Initial implementation
- LinkedIn OAuth 2.0 integration
- Keycloak user management
- Comprehensive error handling
- Schema validation support

## Related Workers

- `auth.signin.google` - Google OAuth sign-in
- `auth.signup.linkedin` - LinkedIn sign-up
- `auth.logout` - User logout

## Support

For issues or questions, contact the Auth Team or refer to the [Architecture Documentation](../architecture.md).