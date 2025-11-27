# Auth SignIn LinkedIn Worker

## Activity Type
`auth.signin.linkedin`

## Input Schema
```json
{
  "authCode": "string (required)",
  "email": "string (optional)"
}
```

## Output Schema
```json
{
  "userId": "string",
  "email": "string",
  "firstName": "string",
  "lastName": "string",
  "token": "string",
  "success": "boolean"
}
```