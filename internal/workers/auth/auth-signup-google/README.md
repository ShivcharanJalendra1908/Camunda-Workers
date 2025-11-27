# Auth Signup Google Worker

## Description
Handles new user registration using Google OAuth 2.0 authentication flow.

## Activity Type
`auth.signup.google`

## Owner
auth-team

## Input Schema
```json
{
  "type": "object",
  "required": ["authCode"],
  "properties": {
    "authCode": {
      "type": "string",
      "description": "Google OAuth authorization code",
      "minLength": 10
    },
    "email": {
      "type": "string",
      "format": "email",
      "description": "Optional - User email for validation"
    }
  }
}
```

## Output Schema
```json
{
  "type": "object",
  "properties": {
    "userId": {
      "type": "string",
      "description": "Keycloak user ID"
    },
    "email": {
      "type": "string",
      "description": "User email address"
    },
    "firstName": {
      "type": "string",
      "description": "User first name"
    },
    "lastName": {
      "type": "string",
      "description": "User last name"
    },
    "success": {
      "type": "boolean",
      "description": "Signup success status"
    }
  }
}
```
