# User Profile API — Frontend Integration Guide

> **Version:** 1.0
> **Last Updated:** 2026-07-14
> **Base URL:** `/api/v1`
> **Auth:** HttpOnly session cookie (NOT Bearer token)

---

## Authentication

All endpoints require a valid session cookie. The backend derives `userId` server-side from the session — **never send `userId` in the request body**.

```http
Cookie: session_token=<token>
```

If the session is invalid or missing, all endpoints return `401`.

---

## Endpoints

### 1. GET `/user/profile` — Get Full Profile

Returns the complete user profile including professional, company, investment, preferences, and subscription data.

**Request:**
```http
GET /api/v1/user/profile
Cookie: session_token=<token>
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "id": "uuid",
    "email": "user@example.com",
    "emailVerified": false,
    "name": "John Doe",
    "phone": "+91-9876543210",
    "status": "active",
    "profileImage": "https://d3xxx.cloudfront.net/profile-photos/uuid/avatar.jpg",
    "location": "Mumbai",
    "createdAt": "2026-01-15T10:30:00Z",
    "updatedAt": "2026-07-14T08:15:00Z",
    "professional": {
      "occupation": "entrepreneur",
      "designation": "c_level",
      "experience": "10-15",
      "priorExperience": true,
      "industryId": "uuid",
      "industry": "food_beverage"
    },
    "company": {
      "businessName": "Acme Foods Pvt Ltd",
      "businessType": "private_limited",
      "industrySector": "food_beverage",
      "yearEstablished": 2020,
      "cinRegistration": "U12345MH2020PTC123456",
      "gstNumber": "27AABCU9603R1ZM",
      "annualTurnover": "50_lakhs_1_crore",
      "companyWebsite": "https://acmefoods.com",
      "companyPhone": "+91-9876543210",
      "registeredAddress": "Mumbai, Maharashtra",
      "companyDescription": "Leading food franchise brand"
    },
    "investment": {
      "minInvestment": "50_lakhs_1_crore",
      "maxInvestment": "1_5_crores",
      "liquidCapitalAvailable": "50_lakhs_1_crore",
      "fundingSource": "self_funded",
      "roiTimeline": "2_years",
      "expectedAnnualRoi": "20-30%",
      "preferredSectors": ["food_beverage", "retail"],
      "preferredCategories": ["QSR", "Fast Casual"]
    },
    "preferences": {
      "theme": "light",
      "language": "en",
      "timezone": "Asia/Kolkata",
      "emailNotifications": true,
      "pushNotifications": true,
      "smsNotifications": false,
      "notificationSettings": {}
    },
    "subscription": {
      "tier": "premium",
      "isValid": true,
      "expiresAt": "2027-01-15T00:00:00Z"
    }
  },
  "timestamp": "2026-07-14T08:15:00Z"
}
```

**Notes:**
- `profileImage` is a CDN URL (constructed from S3 key at read time). Empty string if no photo uploaded.
- All sub-profiles (`professional`, `company`, `investment`, `preferences`) are `null` if not yet created.
- `subscription` may be `null` for free-tier users.

---

### 2. PUT `/user/profile` — Update Profile (Workflow)

Triggers the `user-profile-update` BPMN workflow. Supports both JSON and multipart form data (for photo uploads).

#### 2a. JSON Body — Standard Updates

**Request:**
```http
PUT /api/v1/user/profile
Content-Type: application/json
Cookie: session_token=<token>
```

```json
{
  "action": "update_personal",
  "profileData": {
    "name": "John Doe",
    "phone": "+91-9876543210",
    "location": "Mumbai"
  }
}
```

#### 2b. Multipart Form — Photo Upload

**Request:**
```http
PUT /api/v1/user/profile
Content-Type: multipart/form-data
Cookie: session_token=<token>
```

| Field | Type | Required | Description |
|---|---|---|---|
| `action` | string | Yes | Must be `update_personal` for photo uploads |
| `profileData` | string (JSON) | No | JSON string of fields to update alongside photo |
| `photo` | file | No | Image file (JPEG, PNG, WebP) |

**Photo Constraints:**
- Max file size: **3MB** (configurable, check `MAX_FILE_SIZE` env)
- Allowed types: `image/jpeg`, `image/png`, `image/webp`
- Max dimensions: **200x200px** (configurable)
- EXIF/GPS data is stripped before upload

**Response (200) — Success:**
```json
{
  "success": true,
  "message": "Personal details updated successfully",
  "action": "update_personal",
  "data": {
    "profile": {
      "name": "John Doe",
      "phone": "+91-9876543210",
      "location": "Mumbai",
      "profileImage": "https://d3xxx.cloudfront.net/profile-photos/uuid/avatar.jpg"
    },
    "updatedFields": ["name", "phone", "location", "profile_image"],
    "auditId": "uuid"
  }
}
```

**Response (200) — Error from workflow:**
```json
{
  "success": false,
  "error": "VALIDATION_ERROR",
  "message": "name is required",
  "action": "update_personal"
}
```

#### Available Actions

| Action | `profileData` Fields | Description |
|---|---|---|
| `retrieve` | (none) | Get full profile (alternative to GET endpoint) |
| `update_personal` | `name`, `phone`, `location`, `profileImage` | Update personal details + photo |
| `update_professional` | `occupation`, `designation`, `experience`, `industryId`, `priorExperience` | Update professional details |
| `update_company` | `businessName`, `businessType`, `industrySector`, `yearEstablished`, `cinRegistration`, `gstNumber`, `annualTurnover`, `companyWebsite`, `companyPhone`, `registeredAddress`, `companyDescription` | Update company details |
| `update_investment` | `minInvestment`, `maxInvestment`, `liquidCapitalAvailable`, `fundingSource`, `roiTimeline`, `expectedAnnualRoi`, `preferredSectors`, `preferredCategories` | Update investment preferences |
| `update_preferences` | `theme`, `language`, `timezone`, `emailNotifications`, `pushNotifications`, `smsNotifications`, `notificationSettings` | Update UI preferences |

---

### 3. DELETE `/user/account` — Delete Account

Triggers the `account-deletion-workflow` BPMN. Anonymizes user data, soft-deletes the account, and removes the Keycloak user.

**Request:**
```http
DELETE /api/v1/user/account
Content-Type: application/json
Cookie: session_token=<token>
```

```json
{
  "reason": "No longer interested"
}
```

`reason` is optional.

**Response (200):**
```json
{
  "success": true,
  "message": "Account deleted successfully"
}
```

---

### 4. GET `/user/preferences` — Get Preferences

**Request:**
```http
GET /api/v1/user/preferences
Cookie: session_token=<token>
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "theme": "light",
    "language": "en",
    "timezone": "Asia/Kolkata",
    "emailNotifications": true,
    "pushNotifications": true,
    "smsNotifications": false,
    "notificationSettings": {}
  },
  "timestamp": "2026-07-14T08:15:00Z"
}
```

**Default values** (if no preferences row exists):
- `theme`: `"light"`
- `language`: `"en"`
- `timezone`: `"UTC"`
- `emailNotifications`: `true`
- `pushNotifications`: `true`
- `smsNotifications`: `false`

---

### 5. GET `/user/preferences/options` — Get Dropdown Options

Returns available options for dropdown fields (theme, language, timezone, industry, investment range).

**Request:**
```http
GET /api/v1/user/preferences/options
Cookie: session_token=<token>
```

**Response (200):**
```json
{
  "success": true,
  "data": {
    "theme": [
      {"value": "light", "label": "Light"},
      {"value": "dark", "label": "Dark"},
      {"value": "auto", "label": "Auto"}
    ],
    "language": [
      {"value": "en", "label": "English"},
      {"value": "hi", "label": "Hindi"},
      {"value": "bn", "label": "Bengali"},
      {"value": "te", "label": "Telugu"},
      {"value": "mr", "label": "Marathi"},
      {"value": "ta", "label": "Tamil"},
      {"value": "gu", "label": "Gujarati"},
      {"value": "kn", "label": "Kannada"},
      {"value": "ml", "label": "Malayalam"},
      {"value": "pa", "label": "Punjabi"}
    ],
    "timezone": [
      {"value": "Asia/Kolkata", "label": "Asia/Kolkata (IST)"},
      {"value": "America/New_York", "label": "America/New_York (EST)"},
      {"value": "America/Chicago", "label": "America/Chicago (CST)"},
      {"value": "America/Denver", "label": "America/Denver (MST)"},
      {"value": "America/Los_Angeles", "label": "America/Los_Angeles (PST)"},
      {"value": "Europe/London", "label": "Europe/London (GMT)"},
      {"value": "Europe/Berlin", "label": "Europe/Berlin (CET)"},
      {"value": "Asia/Dubai", "label": "Asia/Dubai (GST)"},
      {"value": "Asia/Singapore", "label": "Asia/Singapore (SGT)"},
      {"value": "Asia/Tokyo", "label": "Asia/Tokyo (JST)"},
      {"value": "Australia/Sydney", "label": "Australia/Sydney (AEST)"},
      {"value": "UTC", "label": "UTC"}
    ],
    "industry": [
      {"value": "food_beverage", "label": "Food & Beverage"},
      {"value": "education", "label": "Education"},
      {"value": "healthcare", "label": "Healthcare"},
      {"value": "retail", "label": "Retail"},
      {"value": "technology", "label": "Technology"},
      {"value": "real_estate", "label": "Real Estate"},
      {"value": "automotive", "label": "Automotive"},
      {"value": "fitness_wellness", "label": "Fitness & Wellness"},
      {"value": "beauty_salon", "label": "Beauty & Salon"},
      {"value": "travel_tourism", "label": "Travel & Tourism"},
      {"value": "logistics_delivery", "label": "Logistics & Delivery"},
      {"value": "manufacturing", "label": "Manufacturing"},
      {"value": "agriculture", "label": "Agriculture"},
      {"value": "financial_services", "label": "Financial Services"},
      {"value": "other", "label": "Other"}
    ],
    "investmentRange": [
      {"value": "below_5_lakhs", "label": "Below 5 Lakhs"},
      {"value": "5_10_lakhs", "label": "5-10 Lakhs"},
      {"value": "10_25_lakhs", "label": "10-25 Lakhs"},
      {"value": "25_50_lakhs", "label": "25-50 Lakhs"},
      {"value": "50_lakhs_1_crore", "label": "50 Lakhs - 1 Crore"},
      {"value": "1_5_crores", "label": "1-5 Crores"},
      {"value": "above_5_crores", "label": "Above 5 Crores"}
    ]
  }
}
```

---

### 6. GET `/user/audit-log` — Get Audit Log

Returns paginated audit log entries for the user.

**Request:**
```http
GET /api/v1/user/audit-log?page=1&limit=20
Cookie: session_token=<token>
```

| Param | Type | Default | Description |
|---|---|---|---|
| `page` | int | 1 | Page number (1-indexed) |
| `limit` | int | 20 | Items per page (max 100) |

**Response (200):**
```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": "uuid",
        "action": "update_personal",
        "field": "update_personal",
        "oldValue": "",
        "newValue": "",
        "changes": {
          "name": "John Doe",
          "phone": "+91-9876543210",
          "profile_image": "profile-photos/uuid/avatar.jpg"
        },
        "source": "web",
        "requestId": "uuid",
        "createdAt": "2026-07-14T08:15:00Z"
      }
    ],
    "totalCount": 15,
    "page": 1,
    "limit": 20
  },
  "timestamp": "2026-07-14T08:15:00Z"
}
```

---

### 7. GET `/user/session/validate` — Validate Session

Checks if the current session is valid.

**Request:**
```http
GET /api/v1/user/session/validate
Cookie: session_token=<token>
```

**Response (200):**
```json
{
  "success": true,
  "userId": "uuid",
  "valid": true
}
```

---

### 8. GET `/user/dashboard` — Get Dashboard

Returns dashboard data for the user.

**Request:**
```http
GET /api/v1/user/dashboard
Cookie: session_token=<token>
```

**Response (200):** Returns dashboard-specific data (franchise matches, application status, etc.).

---

## Error Handling

### Standard Error Response

All errors follow this format:

```json
{
  "success": false,
  "error": "ERROR_CODE",
  "message": "Human-readable error message"
}
```

### HTTP Status Codes

| Status | Meaning |
|---|---|
| `200` | Success |
| `400` | Bad request / validation error |
| `401` | Not authenticated (missing/invalid session) |
| `404` | Resource not found |
| `408` | Workflow timeout (30s) |
| `500` | Internal server error |
| `503` | Service unavailable (S3 not configured) |

### Error Codes

| Code | HTTP Status | Description |
|---|---|---|
| `AUTH_REQUIRED` | 401 | No valid session cookie |
| `VALIDATION_ERROR` | 400 | Input validation failed |
| `INTERNAL_ERROR` | 500 | Unexpected server error |
| `WORKFLOW_TIMEOUT` | 408 | BPMN workflow took >30s |
| `DB_ERROR` | 500 | Database query failed |
| `PHOTO_TOO_LARGE` | 400 | File exceeds max size (3MB) |
| `PHOTO_TOO_LARGE_DIMENSIONS` | 400 | Image exceeds max dimensions |
| `PHOTO_INVALID` | 400 | File is not a valid image |
| `PHOTO_UNSUPPORTED_FORMAT` | 400 | File type not allowed |
| `PHOTO_SECURITY_REJECTED` | 400 | Image failed security checks |
| `PHOTO_READ_FAILED` | 400 | Failed to read uploaded file |
| `PHOTO_UPLOAD_FAILED` | 500 | S3 upload failed |
| `PHOTO_STORAGE_UNAVAILABLE` | 503 | S3 not configured |

### Photo Upload Error Examples

**File too large:**
```json
{
  "success": false,
  "error": "PHOTO_TOO_LARGE",
  "message": "Photo exceeds maximum size of 3MB"
}
```

**Invalid format:**
```json
{
  "success": false,
  "error": "PHOTO_UNSUPPORTED_FORMAT",
  "message": "Only JPEG, PNG, and WebP formats are allowed"
}
```

**Security rejected:**
```json
{
  "success": false,
  "error": "PHOTO_SECURITY_REJECTED",
  "message": "Photo failed security validation"
}
```

---

## Important Implementation Notes

### 1. Profile Image Handling

- **Storage:** S3 key only (e.g., `profile-photos/uuid/avatar.jpg`)
- **Response:** CDN URL constructed at read time (e.g., `https://d3xxx.cloudfront.net/profile-photos/uuid/avatar.jpg`)
- **Never store CDN URLs in your state** — always use the key and let the backend construct URLs
- **Empty string** means no photo uploaded (`profileImage: ""`)

### 2. Photo Upload Flow

```
Frontend                    Backend
   │                           │
   ├─ PUT /user/profile ──────►│
   │  (multipart/form-data)    │
   │  action: "update_personal"│
   │  photo: <file>            │
   │  profileData: {...}       │
   │                           ├─ Validate photo security
   │                           ├─ Strip EXIF/GPS data
   │                           ├─ Upload to S3
   │                           ├─ Start BPMN workflow
   │                           ├─ Write to DB + audit
   │                           ├─ Construct CDN URL
   │◄── 200 { success, data }──┤
```

### 3. Workflow Response Wrapping

The `PUT /user/profile` endpoint returns responses wrapped in a workflow envelope. The actual profile data is in `data.profile`:

```json
{
  "success": true,
  "message": "...",
  "action": "update_personal",
  "data": {
    "profile": { ... },
    "updatedFields": [...],
    "auditId": "uuid"
  }
}
```

### 4. FLE (Field-Level Encryption)

PII fields (name, phone, location, email) are encrypted server-side before reaching Camunda. The backend decrypts them before returning responses. **Frontend does not need to handle encryption/decryption.**

### 5. Dropdown Values

Use `GET /user/preferences/options` to populate dropdowns. Values sent in `profileData` must match the `value` field from options (e.g., `"food_beverage"`, not `"Food & Beverage"`).

#### Professional Details

| Dropdown | Values |
|---|---|
| `occupation` | `software_engineer`, `business_analyst`, `product_manager`, `data_scientist`, `marketing_manager`, `sales_executive`, `financial_advisor`, `consultant`, `entrepreneur`, `teacher`, `doctor`, `lawyer`, `engineer`, `architect`, `designer`, `accountant`, `hr_professional`, `operations_manager`, `other` |
| `designation` | `junior`, `mid_level`, `senior`, `lead`, `manager`, `director`, `vp`, `c_level`, `founder`, `co_founder`, `intern`, `fresher` |
| `experience_level` | `0-2`, `2-5`, `5-10`, `10-15`, `15-20`, `20+` |

#### Company Details

| Dropdown | Values |
|---|---|
| `business_type` | `sole_proprietorship`, `partnership`, `llp`, `private_limited`, `public_limited`, `opc`, `huf`, `trust`, `society`, `other` |

#### Investment Details

| Dropdown | Values |
|---|---|
| `funding_source` | `self_funded`, `family_friends`, `bank_loan`, `nbfc`, `angel_investor`, `venture_capital`, `government_scheme`, `crowdfunding`, `other` |
| `roi_timeline` | `6_months`, `1_year`, `2_years`, `3_years`, `5_plus_years` |
| `investment_range` | `below_5_lakhs`, `5_10_lakhs`, `10_25_lakhs`, `25_50_lakhs`, `50_lakhs_1_crore`, `1_5_crores`, `above_5_crores` |

#### Preferences

| Dropdown | Values |
|---|---|
| `theme` | `light`, `dark`, `auto` |
| `language` | `en`, `hi`, `bn`, `te`, `mr`, `ta`, `gu`, `kn`, `ml`, `pa` |
| `timezone` | `Asia/Kolkata`, `America/New_York`, `America/Chicago`, `America/Denver`, `America/Los_Angeles`, `Europe/London`, `Europe/Berlin`, `Asia/Dubai`, `Asia/Singapore`, `Asia/Tokyo`, `Australia/Sydney`, `UTC` |

#### Industry

| Dropdown | Values |
|---|---|
| `industry` | `food_beverage`, `education`, `healthcare`, `retail`, `technology`, `real_estate`, `automotive`, `fitness_wellness`, `beauty_salon`, `travel_tourism`, `logistics_delivery`, `manufacturing`, `agriculture`, `financial_services`, `other` |

### 6. Multipart Form — profileData as String

When uploading a photo, `profileData` must be a **JSON string** in a form field, not a nested object:

```javascript
const formData = new FormData();
formData.append('action', 'update_personal');
formData.append('profileData', JSON.stringify({
  name: 'John Doe',
  phone: '+91-9876543210'
}));
formData.append('photo', fileInput.files[0]);

fetch('/api/v1/user/profile', {
  method: 'PUT',
  body: formData,
  credentials: 'include'  // important: send cookies
});
```

### 7. Timeout Handling

Workflow requests may take up to **30 seconds**. Show a loading spinner and handle `408` (WORKFLOW_TIMEOUT) gracefully by retrying.

---

## Quick Reference — All Endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/user/profile` | Yes | Get full profile |
| `PUT` | `/user/profile` | Yes | Update profile (workflow) |
| `DELETE` | `/user/account` | Yes | Delete account (workflow) |
| `GET` | `/user/preferences` | Yes | Get preferences |
| `GET` | `/user/preferences/options` | Yes | Get dropdown options |
| `GET` | `/user/audit-log` | Yes | Get audit log (paginated) |
| `GET` | `/user/session/validate` | Yes | Validate session |
| `GET` | `/user/dashboard` | Yes | Get dashboard data |
| `GET` | `/user/workflow/:workflowId` | Yes | Check workflow status |
