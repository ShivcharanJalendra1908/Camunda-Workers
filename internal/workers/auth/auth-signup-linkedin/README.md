# Auth Signup LinkedIn Worker

## Description
Handles user signup using LinkedIn OAuth authentication.

## Activity Type
`auth.signup.linkedin`

## Input Schema
```json
{
  "authCode": "string (required) - LinkedIn OAuth authorization code",
  "email": "string (optional) - User email for validation"
}
```

## Output Schema
```json
{
  "userId": "string - Keycloak user ID",
  "email": "string - User email",
  "firstName": "string - User first name",
  "lastName": "string - User last name",
  "success": "boolean - Operation status"
}
```

## Environment Variables
- ZEEBE_ADDRESS
- LINKEDIN_CLIENT_ID
- LINKEDIN_CLIENT_SECRET
- LINKEDIN_REDIRECT_URL
- KEYCLOAK_URL
- KEYCLOAK_CLIENT_SECRET
- ZOHO_CRM_API_KEY
- ZOHO_CRM_OAUTH_TOKEN

## Testing
```bash
go test ./tests/...
```