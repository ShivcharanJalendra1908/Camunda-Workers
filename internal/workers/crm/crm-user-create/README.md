# CRM User Create Worker

## Description
Creates user contact records in Zoho CRM for customer relationship management.

## Activity Type
`crm.user.create`

## Owner
crm-team

## Input Schema
```json
{
  "type": "object",
  "required": ["email", "firstName", "lastName"],
  "properties": {
    "email": {
      "type": "string",
      "format": "email",
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
    "phone": {
      "type": "string",
      "description": "Phone number (optional)"
    },
    "source": {
      "type": "string",
      "description": "Lead source (e.g., 'Google OAuth Signup')"
    }
  }
}
```

## Output Schema
```json
{
  "type": "object",
  "properties": {
    "contactId": {
      "type": "string",
      "description": "Zoho CRM contact ID"
    },
    "success": {
      "type": "boolean",
      "description": "Creation success status"
    }
  }
}
```
## Zoho CRM Integration

### API Endpoint
- **Base URL**: https://www.zohoapis.com/crm/v3
- **Endpoint**: /Contacts
- **Method**: POST
- **Authentication**: OAuth 2.0

### Contact Fields
- Email (required)
- First Name (required)
- Last Name (required)
- Phone (optional)
- Lead Source (optional)
- Created Time (auto)
- Modified Time (auto)

## Process Flow

```
1. Receive user data from workflow
2. Validate required fields (email, firstName, lastName)
3. Format contact data for Zoho API
4. Call Zoho CRM API to create contact
5. Extract contact ID from response
6. Return contact ID and success status
```
