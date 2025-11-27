# Google Signin Worker

Handles user authentication via Google OAuth 2.0 flow.

## Overview

This worker processes Google OAuth authorization codes, exchanges them for access tokens, retrieves user profiles, creates/updates users in Keycloak, and optionally creates CRM contacts in Zoho.

## Task Type

`auth.signin.google`

## Input Variables

| Variable | Type | Required | Description |
|----------|------|----------|-------------|
| `authCode` | string | Yes | Google OAuth authorization code |
| `redirectUri` | string | No | Redirect URI for OAuth callback (defaults to configured value) |
| `state` | string | No | OAuth state parameter for CSRF protection |
| `metadata` | object | No | Additional metadata for the request |

## Output Variables

| Variable | Type | Description |
|----------|------|-------------|
| `success` | boolean | Whether authentication was successful |
| `userId` | string | User ID in the system |
| `email` | string | User's email address |
| `firstName` | string | User's first name |
| `lastName` | string | User's last name |
| `token` | string | OAuth access token |
| `isNewUser` | boolean | Whether this is a newly created user |
| `crmContactId` | string | CRM contact ID if created |

## Error Codes

- `GOOGLE_OAUTH_ERROR` - Failed to exchange authorization code
- `GOOGLE_API_ERROR` - Failed to retrieve user profile
- `KEYCLOAK_ERROR` - Failed to create/find user in Keycloak
- `ZOHO_CRM_ERROR` - Failed to create CRM contact
- `VALIDATION_FAILED` - Input validation failed
- `INPUT_PARSING_FAILED` - Failed to parse job variables

## Configuration

```yaml
workers:
  auth-signin-google:
    enabled: true
    max_jobs_active: 5
    timeout: 10000

auth:
  oauth_providers:
    google:
      client_id: "your-google-client-id"
      client_secret: "your-google-client-secret"
      redirect_uri: "https://yourapp.com/auth/google/callback"

integrations:
  zoho:
    api_key: "your-zoho-api-key"
    oauth_token: "your-zoho-oauth-token"