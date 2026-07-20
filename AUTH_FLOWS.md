# Authentication Flows — End-to-End Documentation

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Registration Flow](#1-registration-flow)
3. [Email Verification Flow (Magic Link)](#2-email-verification-flow-magic-link)
4. [Login Flow (OAuth2 + PKCE)](#3-login-flow-oauth2--pkce)
5. [Password Reset Flow](#4-password-reset-flow)
6. [Session Management](#5-session-management)
7. [Failure Modes & Where Things Break](#6-failure-modes--where-things-break)
8. [File Reference Index](#7-file-reference-index)

---

## Architecture Overview

```
┌──────────────┐      ┌──────────────┐      ┌──────────────┐      ┌──────────────┐
│   Frontend   │──────│  API Gateway │──────│   Camunda    │──────│   Keycloak   │
│  (React SPA) │      │   (Gin)      │      │   (Zeebe)    │      │   (IdP)      │
└──────────────┘      └──────┬───────┘      └──────┬───────┘      └──────────────┘
                             │                     │
                      ┌──────┴───────┐      ┌──────┴───────┐
                      │    Redis     │      │  PostgreSQL  │
                      │  (Sessions,  │      │  (Users,     │
                      │   OAuth      │      │   Identities)│
                      │   State)     │      │              │
                      └──────────────┘      └──────────────┘
```

**Key architectural decisions:**
- Registration is handled **entirely by Keycloak** (no Go backend involvement)
- Login uses **OAuth2/PKCE** orchestrated through a **Camunda BPMN workflow**
- Sessions are stored in **Redis** with HTTP-only secure cookies
- User records are created in **PostgreSQL** on first successful login (not on registration)
- Email verification uses Keycloak's built-in **action token** mechanism
- Password reset uses Keycloak's built-in **execute-actions-email** API

---

## 1. Registration Flow

### Overview
Registration is a **Keycloak-only** flow. The Go backend is not involved. The user fills out a form rendered by Keycloak, and Keycloak creates the user in its own database, then sends a verification email.

### Step-by-Step

#### Step 1: User Clicks "Create an Account"
- **File:** `keycloak-theme/lemici/login/login.ftl:126-128`
- The login page has a link: "or create an account if you don't have one yet"
- Links to `${url.registrationUrl}` — a Keycloak-internal URL

#### Step 2: Registration Form
- **File:** `keycloak-theme/lemici/login/register.ftl:1-217`
- Form action: `${url.registrationAction}` — posts directly to Keycloak
- Collects: `firstName`, `lastName`, `email`, `password`, `password-confirm`
- Username field only shown if `realm.registrationEmailAsUsername` is OFF (line 12)
- Requires Terms of Service checkbox (line 81-86)
- Submit button is disabled until all fields are valid (line 89)
- Client-side password strength meter and validation (lines 95-191)
- Social login (Google) available as alternative (lines 194-210)

#### Step 3: Keycloak Creates the User
- **No Go code involved** — this is native Keycloak behavior
- Keycloak creates the user in its internal database
- User is created with `email_verified=false` (or whatever the realm config says)
- Keycloak automatically sends a verification email using the custom email theme

#### Step 4: Keycloak Sends Verification Email
- **File:** `keycloak-theme/lemici/email/html/email-verification.ftl:1-169`
- Subject line from `email/messages/messages_en.properties:1`: `emailVerificationSubject=Welcome to Lemici! Verify your email`
- The email contains a `${link}` — this is a **Keycloak action token URL**
- Link format:
  ```
  https://{keycloak-base-url}/realms/{realm}/login-actions/execute-actions?...
  ```
- Link expires in `${linkExpirationInMinutes}` minutes (configurable in Keycloak realm settings)

#### Step 5: User Sees "Check Your Email" Page
- **File:** `keycloak-theme/lemici/login/login-verify-email.ftl:1-58`
- Shows the user's email address (line 19)
- "Sign In" button that redirects to the frontend login page via `handleReturnToLogin()` (line 28)
- "Resend Verification Email" button that posts to `${url.loginAction}` (line 37)
- Instructions: "Once you click the link in the email to verify, please click 'Sign In' below" (line 24)

### Where Registration Can Break

| Failure Point                    | What Happens                                  | How to Fix                                                                              |
| -------------------------------- | --------------------------------------------- | --------------------------------------------------------------------------------------- |
| Keycloak realm misconfiguration  | Registration form doesn't appear              | Check Keycloak admin console → Realm Settings → Login tab → "User registration" enabled |
| SMTP not configured              | Verification email never sent                 | Check Keycloak → Realm Settings → Email tab → SMTP settings                             |
| `registrationEmailAsUsername` ON | Username field hidden, email used as username | Intentional — no fix needed                                                             |
| Form validation fails            | Client-side JS prevents submission            | Check password length ≥ 8, passwords match, terms checked                               |
| Keycloak DB connection down      | Registration POST fails                       | Check Keycloak logs, PostgreSQL connectivity                                            |

---

## 2. Email Verification Flow (Magic Link)

### Overview
Keycloak generates a **signed action token** embedded in a URL. When the user clicks this link, Keycloak validates the token, marks the email as verified, and shows a success page. This is entirely handled by Keycloak — no Go backend involvement.

### Step-by-Step

#### Step 1: Email Sent
- **File:** `keycloak-theme/lemici/email/html/email-verification.ftl:147-148`
- CTA button: `<a href="${link}" class="cta-button">Verify Email Address</a>`
- Plain text fallback at lines 154-157

#### Step 2: User Clicks the Link
The `${link}` is a Keycloak action token URL. Example format:
```
https://us-dev-api.lemici.com/realms/camunda-platform/login-actions/execute-actions?key=<token>&execution=<id>&client_id=lemici-frontend&tab_id=<id>
```

#### Step 3: Keycloak Validates the Action Token
- **Keycloak internal** — no Go code
- Validates: token signature, expiry, session, execution ID
- If valid: marks `email_verified=true` on the Keycloak user
- If invalid: shows error page

#### Step 4: Redirect to `info.ftl`
- **File:** `keycloak-theme/lemici/login/info.ftl:1-140`
- Keycloak redirects to this page with `message.summary` containing "verified" or "activation"

#### Step 5: Email Verified Success Page
- **File:** `keycloak-theme/lemici/login/info.ftl:60-74`
- Shows success icon (green checkmark) at lines 38-40
- Message: "Your email address has been verified successfully. Your account is ready — please sign in to continue." (lines 62-64)
- "Sign In to LeMiCi" button calls `handleReturnToLogin()` (line 67)
- `handleReturnToLogin()` (lines 11-22) detects environment from hostname:
  - `dev.lemici.com` → `https://dev.lemici.com/login`
  - `lemici.com` → `https://www.lemici.com/login`
  - `localhost` → `http://localhost:3000/login`
  - Fallback → `${url.loginUrl}`

### Same-Browser vs Cross-Device

**Same-browser flow:**
1. User registers in Tab A → sees "Check Your Email" page (`login-verify-email.ftl`)
2. User clicks verification link in the same browser → Keycloak processes it
3. Auth flow completes → user lands on app homepage
4. The waiting tab (Tab A) is orphaned — nobody looks at it

**Cross-device flow:**
1. User registers on Device A → sees "Check Your Email" page
2. User clicks verification link on Device B → Keycloak marks email verified
3. `info.ftl` shows success → user clicks "Sign In" → redirected to frontend login page
4. User logs in through the normal login flow

### Where Email Verification Can Break

| Failure Point                                    | What Happens                         | How to Fix                                                    |
| ------------------------------------------------ | ------------------------------------ | ------------------------------------------------------------- |
| Link expired                                     | Keycloak shows error page            | User needs to click "Resend Verification Email"               |
| Link already used                                | Keycloak shows error                 | Normal — link is one-time use                                 |
| SMTP not configured                              | Email never arrives                  | Check Keycloak SMTP settings                                  |
| User clicks link on different device             | Success but needs to log in manually | This is expected behavior — `info.ftl` shows "Sign In" button |
| Keycloak action token validation fails           | Error page shown                     | Check Keycloak logs, realm signing keys                       |
| `handleReturnToLogin()` hostname detection fails | Wrong redirect                       | Check hostname pattern matching in `info.ftl:11-22`           |

---

## 3. Login Flow (OAuth2 + PKCE)

### Overview
The login flow is the most complex part. It uses **OAuth2 Authorization Code flow with PKCE** (Proof Key for Code Exchange), orchestrated through a **Camunda BPMN workflow** with two Zeebe workers.

### Complete Flow Diagram

```
Frontend                    API Gateway                 Camunda Workers              Keycloak
  │                             │                             │                         │
  │ POST /api/v1/auth/login     │                             │                         │
  │ (action: "initiate")        │                             │                         │
  │────────────────────────────>│                             │                         │
  │                             │ Start workflow              │                         │
  │                             │────────────────────────────>│                         │
  │                             │                             │ keycloak-signin worker  │
  │                             │                             │────────────────────────>│
  │                             │                             │ (generates state + PKCE │
  │                             │                             │  builds auth URL)        │
  │                             │                             │<────────────────────────│
  │                             │ Response: authorizationUrl  │                         │
  │                             │<────────────────────────────│                         │
  │ { authorizationUrl }        │                             │                         │
  │<────────────────────────────│                             │                         │
  │                             │                             │                         │
  │ User redirected to Keycloak │                             │                         │
  │────────────────────────────────────────────────────────────────────────────────────>│
  │                             │                             │                         │
  │ User authenticates at Keycloak                            │                         │
  │────────────────────────────────────────────────────────────────────────────────────>│
  │                             │                             │                         │
  │ Keycloak redirects to       │                             │                         │
  │ /api/v1/auth/callback       │                             │                         │
  │ (code + state)              │                             │                         │
  │────────────────────────────>│                             │                         │
  │                             │ POST /api/v1/auth/login     │                         │
  │                             │ (action: "callback")        │                         │
  │                             │────────────────────────────>│                         │
  │                             │                             │ keycloak-signin worker  │
  │                             │                             │ (exchanges code + PKCE) │
  │                             │                             │────────────────────────>│
  │                             │                             │<────────────────────────│
  │                             │                             │ (tokens + identity)     │
  │                             │                             │                         │
  │                             │                             │ session-manager worker  │
  │                             │                             │ (creates Redis session) │
  │                             │                             │                         │
  │                             │ Response: sessionId + cookie│                         │
  │                             │<────────────────────────────│                         │
  │ Set-Cookie: AUTH_SESSION_ID │                             │                         │
  │ { success, userId, email }  │                             │                         │
  │<────────────────────────────│                             │                         │
```

### Step-by-Step

#### Step 1: Frontend Initiates Login
- **File:** `cmd/api-gateway/main.go:326`
  ```go
  authGroup.POST("/login", workflowHandler.StartKeycloakLogin)
  ```
- Frontend sends `POST /api/v1/auth/login` with empty body (or `{}`)
- This triggers the "initiate" path

#### Step 2: API Gateway Starts Camunda Workflow
- **File:** `internal/api/handlers/workflow_handler.go:1124-1282`
- `StartKeycloakLogin()`:
  1. Generates a `correlationKey` (UUID) for Redis pub/sub (line 1145)
  2. Subscribes to Redis channel `workflow:response:<correlationKey>` (line 1148)
  3. Since no `code`/`state` in request, sets `action = "initiate"` (line 1174)
  4. Starts Camunda workflow `keycloak-login-workflow` with variables (line 1194)
  5. Waits up to 30 seconds for worker response via Redis pub/sub

#### Step 3: Camunda Routes to Initiate Path
- **File:** `bpmn/keycloak-login-workflow.bpmn:1-237`
- Process: `keycloak-login-workflow`
- Start → `Gateway_RouteAction` (line 8) → routes to `Task_InitiateOAuth` (line 13)
- Gateway condition: default path is "initiate" (line 95)

#### Step 4: Keycloak-Signin Worker Generates Auth URL
- **File:** `internal/workers/auth/keycloak-signin/service.go:64-151`
- `handleInitiate()`:
  1. Generates random `state` (32 bytes, base64url) — line 66
  2. Generates PKCE `verifier` and `challenge` (S256) — line 78
  3. Validates `redirectUrl` against allowed domains — line 90
  4. Stores in Redis: key `oauth:state:<state>`, value `{"v":"<verifier>","r":"<redirectUrl>"}` — line 118
  5. Builds authorization URL: `s.keycloak.AuthCodeURL(state, pkce.Challenge)` — line 139

#### Step 5: Keycloak Provider Builds Auth URL
- **File:** `internal/common/auth/provider/keycloak/keycloak.go:80-88`
- `AuthCodeURL()` builds:
  ```
  https://{publicBaseURL}/realms/{realm}/protocol/openid-connect/auth?
    access_type=online&
    client_id={clientID}&
    code_challenge={challenge}&
    code_challenge_method=S256&
    redirect_uri={redirectURL}&
    response_mode=query&
    response_type=code&
    scope=openid+email+profile&
    state={state}&
    max_age=0
  ```

#### Step 6: Response Returned to Frontend
- **File:** `internal/api/handlers/workflow_handler.go:1215-1222`
- Workflow sends `{"success": true, "authorizationUrl": "...", "state": "..."}`
- API Gateway returns this to the frontend
- Frontend redirects user's browser to the `authorizationUrl`

#### Step 7: User Authenticates at Keycloak
- **File:** `keycloak-theme/lemici/login/login.ftl:1-130`
- User enters email/password or clicks "Continue with Google"
- Keycloak validates credentials
- If email not verified: Keycloak shows `login-verify-email.ftl` instead

#### Step 8: Keycloak Redirects to Callback
- After successful authentication, Keycloak redirects to:
  ```
  https://{backend}/api/v1/auth/callback?code={authCode}&state={state}
  ```

#### Step 9: API Gateway Handles Callback
- **File:** `internal/api/handlers/workflow_handler.go:1699-1749`
- `HandleKeycloakCallback()`:
  1. Extracts `code` and `state` from query params (lines 1700-1701)
  2. If missing code/state: calls `initiateFreshLogin()` for silent re-login (line 1721)
  3. Reads `redirectUrl` from Redis (non-destructive read) (line 1727)
  4. Constructs synthetic JSON POST body with `code`, `state`, `provider` (lines 1741-1746)
  5. Calls `StartKeycloakLogin()` — reuses the same flow (line 1748)

#### Step 10: Camunda Routes to Callback Path
- **File:** `bpmn/keycloak-login-workflow.bpmn:98-100`
- Gateway condition: `action = "callback"` → routes to `Task_HandleCallback` (line 40)

#### Step 11: Keycloak-Signin Worker Exchanges Code
- **File:** `internal/workers/auth/keycloak-signin/service.go:153-248`
- `handleCallback()`:
  1. **Atomic GETDEL** from Redis: retrieves and deletes `oauth:state:<state>` (lines 167-176)
     - Uses Lua script for atomicity — prevents replay attacks
  2. Parses PKCE verifier from stored session (lines 187-195)
  3. Exchanges authorization code for tokens: `s.keycloak.ExchangeCode(ctx, input.Code, verifier)` (line 198)

#### Step 12: Token Exchange
- **File:** `internal/common/auth/provider/keycloak/keycloak.go:91-156`
- `ExchangeCode()`:
  1. Calls Keycloak token endpoint with `code` and `code_verifier` (line 97-101)
  2. Receives `id_token`, `access_token`, `refresh_token`
  3. Verifies `id_token` signature using OIDC discovery (line 111)
  4. Extracts claims: `sub`, `email`, `email_verified`, `given_name`, `family_name` (lines 116-124)
  5. Returns normalized `Identity` struct

#### Step 13: User Resolution (PostgreSQL)
- **File:** `internal/common/auth/resolver/db_resolver.go:29-143`
- `Resolve()`:
  1. **Identity lookup**: checks `identities` table for `provider + provider_user_id` (lines 49-52)
     - If found: returns existing `user_id`
  2. **Email-based linking**: checks `users` table for matching email (lines 74-77)
     - If found: links identity, updates name if missing
  3. **New user creation** (lines 106-142):
     - `INSERT INTO users (email, email_verified, name)` with `ON CONFLICT` handling
     - `INSERT INTO identities (user_id, provider, provider_user_id)`
     - `INSERT INTO user_subscriptions (user_id, tier='free')`

#### Step 14: Session Creation
- **File:** `internal/workers/auth/session-manager/service.go:59-133`
- `handleCreate()`:
  1. Generates session ID: 32 random bytes, base64url (43 chars) — line 62
  2. Generates CSRF token: 32 random bytes, base64url — line 76
  3. Creates `Session` struct with all fields — lines 87-99
  4. Stores in Redis: `session:<sessionID>` with TTL — line 102
  5. Builds `Set-Cookie` header — line 113

#### Step 15: Cookie Set and Response Returned
- **File:** `internal/api/handlers/workflow_handler.go:1493-1675`
- `completeLoginFlow()`:
  1. Clears old cookies — lines 1503-1521
  2. Sets `AUTH_SESSION_ID` cookie — lines 1523-1546
  3. Returns response with `userId`, `email`, `sessionId`, tokens

### Where Login Can Break

| Failure Point                                            | What Happens                                   | How to Fix                                            |
| -------------------------------------------------------- | ---------------------------------------------- | ----------------------------------------------------- |
| Redis down                                               | State storage fails, pub/sub fails             | Check Redis connectivity, `oauth:state:*` keys        |
| Camunda/Zeebe down                                       | Workflow doesn't start                         | Check Zeebe broker health, worker registration        |
| Keycloak OIDC discovery fails                            | Provider initialization fails                  | Check `issuer` URL, network connectivity              |
| PKCE state expired (5 min TTL)                           | Token exchange fails with `INVALID_STATE`      | User must restart login flow                          |
| State replayed (double callback)                         | Atomic GETDEL prevents replay                  | Normal — second request gets `INVALID_STATE`          |
| `allowedRedirectDomains` misconfigured                   | Redirect URL rejected                          | Check config `auth.keycloak.allowed_redirect_domains` |
| Cookie domain mismatch                                   | Session cookie not sent on subsequent requests | Check `CookieDomain` in session config                |
| `AUTH_SESSION_ID` cookie conflicts with `__Host-session` | Session not found                              | Reconcile `constants.go` vs `cookie.go` naming        |
| PostgreSQL down                                          | User resolution fails                          | Check DB connectivity                                 |
| Keycloak token endpoint fails                            | Token exchange returns error                   | Check Keycloak logs, client secret                    |

---

## 4. Password Reset Flow

### Overview
Password reset has two entry points:
1. **Keycloak-native flow**: User clicks "Forgot password?" on login page → Keycloak handles everything
2. **API-triggered flow**: `POST /api/v1/auth/password/reset` → Camunda workflow → email sent

Both paths converge at Keycloak's `execute-actions-email` API, which sends the reset email.

### Flow Diagram

```
User                    Frontend               Keycloak              API Gateway
  │                        │                      │                      │
  │ Click "Forgot password?"│                     │                      │
  │───────────────────────>│                      │                      │
  │                        │ Redirect to          │                      │
  │                        │ login-reset-password │                      │
  │                        │─────────────────────>│                      │
  │                        │                      │                      │
  │ Enter email            │                      │                      │
  │ Submit form            │                      │                      │
  │───────────────────────>│ POST to Keycloak     │                      │
  │                        │─────────────────────>│                      │
  │                        │                      │ Check email exists   │
  │                        │                      │ (calls /check-email) │
  │                        │                      │─────────────────────>│
  │                        │                      │<─────────────────────│
  │                        │                      │                      │
  │                        │                      │ Send reset email     │
  │                        │                      │ (execute-actions-email)│
  │                        │                      │                      │
  │<─────── Email with reset link ───────────────────────────────────────│
  │                        │                      │                      │
  │ Click reset link       │                      │                      │
  │──────────────────────────────────────────────────────────────────────>│
  │                        │                      │                      │
  │ login-update-password  │                      │                      │
  │ (enter new password)   │                      │                      │
  │──────────────────────────────────────────────────────────────────────>│
  │                        │                      │                      │
  │ Success → info.ftl     │                      │                      │
  │ (auto-redirect to login)│                     │                      │
```

### Step-by-Step

#### Step 1: User Clicks "Forgot Password?"
- **File:** `keycloak-theme/lemici/login/login.ftl:76`
- Link: `<a href="${url.loginResetCredentialsUrl}">Forgot password?</a>`
- Redirects to Keycloak's built-in reset credentials page

#### Step 2: Reset Password Form
- **File:** `keycloak-theme/lemici/login/login-reset-password.ftl:1-149`
- Form action: `${url.loginAction}` — posts to Keycloak (line 13)
- JavaScript pre-validation (lines 31-142):
  1. Before submitting to Keycloak, calls `GET /api/v1/auth/check-email?email=...` (line 94)
  2. If email exists (`data.exists === true`): submits the Keycloak form (line 105)
  3. If not found: increments `failedAttempts` in `sessionStorage` (lines 107-108)
  4. After 3 failed attempts: disables form, shows error, redirects to registration (lines 110-121)

#### Step 3: Email Validation Endpoint
- **File:** `cmd/api-gateway/main.go:335`
  ```go
  authGroup.GET("/check-email", userHandler.CheckEmailExists)
  ```
- **File:** `internal/api/handlers/user_handler.go:134-166`
- Queries PostgreSQL: `SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(email) = LOWER($1))` (line 147)
- Returns: `{"success": true, "exists": true/false}`

#### Step 4: Keycloak Sends Reset Email
- Keycloak receives the form POST and calls its internal `execute-actions-email` API
- The API call is:
  ```
  PUT /admin/realms/{realm}/users/{userId}/execute-actions-email
  Body: ["UPDATE_PASSWORD"]
  ```
- Keycloak generates a signed action token URL (similar to email verification)
- The reset link format:
  ```
  https://{keycloak-base-url}/realms/{realm}/login-actions/execute-actions?...
  ```

#### Step 5: User Clicks Reset Link
- Keycloak validates the action token
- Renders the **password update form**

#### Step 6: Password Update Form
- **File:** `keycloak-theme/lemici/login/login-update-password.ftl:1-89`
- Form action: `${url.loginAction}` — posts to Keycloak (line 12)
- Fields: `password-new`, `password-confirm` (lines 22, 34)
- Hidden fields for browser autofill compatibility (lines 15-16)
- "Sign out from other devices" checkbox — default ON (line 43)
- Client-side validation: both passwords must match and be ≥ 8 chars (lines 70-87)
- Buttons:
  - If `isAppInitiatedAction`: "Reset Password" + "Cancel" (lines 50-59)
  - Otherwise: only "Reset Password" (lines 60-65)

#### Step 7: Keycloak Processes Password Change
- Keycloak updates the password in its database
- If "Sign out from other devices" was checked: invalidates all existing sessions for the user
- Keycloak redirects to `info.ftl` with a success message

#### Step 8: Success Page with Cross-Tab Communication
- **File:** `keycloak-theme/lemici/login/info.ftl:112-136`
- The default success page (when message doesn't contain "verified" or "sent"):
  1. Sets `localStorage.setItem('password_reset_success', 'true')` — line 123
  2. Shows 5-second countdown: "Redirecting to login in 5 seconds..." — line 115
  3. Auto-redirects to login page after countdown — lines 125-134
  4. Manual "Go to Login Page" button available — line 117

#### Step 9: Cross-Tab Communication
- **File:** `keycloak-theme/lemici/login/info.ftl:75-111`
- The "Email Sent" page (when message contains "receive", "instruction", or "sent"):
  - Listens for `password_reset_success` in localStorage via `storage` event (lines 97-100)
  - Polls localStorage every 1 second as fallback (lines 104-109)
  - When detected: auto-redirects to login page

### Password Reset Cross-Tab Pattern

```
Tab A (login-reset-password.ftl)     Tab B (login-update-password.ftl)     Tab C (info.ftl)
        │                                      │                                    │
        │ User enters email                    │                                    │
        │ Keycloak sends reset email           │                                    │
        │                                      │                                    │
        │ (User clicks link in email)          │                                    │
        │                                      │                                    │
        │                              User enters new password                     │
        │                              Submits form to Keycloak                     │
        │                                      │                                    │
        │                                      │ Sets localStorage                  │
        │                                      │ ['password_reset_success'] = true  │
        │                                      │                                    │
        │ Listens for storage event  <--- cross-tab storage event ---  Detects flag │
        │ Auto-redirects to login              │                                    │
```

### Where Password Reset Can Break

| Failure Point                               | What Happens                             | How to Fix                                             |
| ------------------------------------------- | ---------------------------------------- | ------------------------------------------------------ |
| Email not in PostgreSQL                     | `/check-email` returns `exists: false`   | User sees "email not registered" — must register first |
| After 3 failed attempts                     | Form disabled, redirects to registration | Normal — anti-enumeration protection                   |
| SMTP not configured                         | Reset email never sent                   | Check Keycloak SMTP settings                           |
| Reset link expired                          | Keycloak shows error                     | User must request new reset link                       |
| Keycloak `execute-actions-email` fails      | No email sent                            | Check Keycloak logs, user existence in Keycloak DB     |
| "Sign out from other devices" checked       | All sessions invalidated                 | User must log in again on all devices                  |
| Cross-tab localStorage blocked              | Auto-redirect doesn't work               | User can manually click "Go to Login Page"             |
| `login-update-password.ftl` session expired | Error page shown                         | User must restart reset flow                           |

---

## 5. Session Management

### Session Structure
- **File:** `internal/common/auth/session/store.go:8-22`
  ```go
  type Session struct {
      SessionID         string
      UserID            string
      CreatedAt         time.Time
      ExpiresAt         time.Time
      AbsoluteExpiresAt time.Time
      Version           int
      CSRFToken         string
      KeycloakUserID    string
      IDToken           string
      AccessToken       string
      RefreshToken      string
      UserAgent         string
      IP                string
  }
  ```

### Session Storage (Redis)
- **File:** `internal/common/auth/session/redis_store.go:19-155`
- Key format: `session:<sessionID>`
- TTL: configurable (default 24 hours)
- Operations:
  - `Create()`: `SET session:<id> <json> <ttl>` + `SADD user_sessions:<userId> <id>`
  - `Get()`: `GET session:<id>`
  - `Delete()`: `DEL session:<id>` + `SREM user_sessions:<userId> <id>`
  - `Update()`: re-set with new TTL
  - `DeleteAllForUser()`: `SMEMBERS` + bulk delete

### Session Cookie
- **File:** `internal/common/constants/constants.go:5-8`
  ```go
  SessionCookieName   = "AUTH_SESSION_ID"
  SessionCookiePath   = "/"
  SessionCookieSecure = true
  SessionCookieHTTPOnly = true
  ```
- **File:** `internal/workers/auth/session-manager/config.go:37-41`
  - Default domain: `.lemici.com`
  - Default SameSite: `Lax`

### Session ID Generation
- **File:** `internal/common/auth/session/id.go:9-20`
- 32 bytes from `crypto/rand`, base64 URL-encoded (43 chars)

### CSRF Token
- Generated alongside session ID: 32 bytes, base64url
- Sent in response body (not cookie) — frontend must include in request headers

---

## 6. Failure Modes & Where Things Break

### Critical: Cookie Name Inconsistency

There's a known inconsistency between:
- `internal/common/constants/constants.go:5`: `SessionCookieName = "AUTH_SESSION_ID"`
- `internal/common/auth/session/cookie.go`: `CookieName = "__Host-session"`

These must be reconciled. If the session-manager worker uses `__Host-session` but the middleware checks `AUTH_SESSION_ID`, sessions won't be found.

### Critical: ErrorHandler Middleware

- **File:** `internal/api/middleware/middleware.go:374`
- The `isAuthFlow` check matches both `/api/v1/auth` and `/oauth`
- API calls to `/api/v1/auth/*` get HTML redirects instead of JSON errors
- This is a known issue that should be fixed

### Common Failure Scenarios

| Scenario                                          | Symptoms                                        | Root Cause                                | Fix                                         |
| ------------------------------------------------- | ----------------------------------------------- | ----------------------------------------- | ------------------------------------------- |
| User registers but never gets email               | "Check Your Email" page shown forever           | SMTP not configured                       | Configure Keycloak SMTP                     |
| User clicks verification link, gets error         | Keycloak error page                             | Link expired or already used              | Click "Resend Verification Email"           |
| Login redirect loop                               | User keeps getting redirected to login          | Session cookie not set, CSRF mismatch     | Check cookie domain, SameSite settings      |
| "Session Expired" on callback                     | `INVALID_STATE` error                           | PKCE state expired (5 min TTL)            | User must restart login flow                |
| Password reset email not received                 | Nothing happens after "Send Reset Link"         | SMTP not configured                       | Configure Keycloak SMTP                     |
| Cross-device password reset doesn't auto-redirect | User stuck on "Email Sent" page                 | localStorage cross-tab blocked            | User clicks "Go to Login Page" manually     |
| User created in Keycloak but not PostgreSQL       | Login succeeds but app shows "user not found"   | DB resolver failed mid-transaction        | Check PostgreSQL connectivity, retry login  |
| Multiple sessions for same user                   | Old sessions still active after password change | "Sign out from other devices" not checked | Default is checked — verify Keycloak config |

### Redis Key Reference

| Key Pattern                          | TTL                     | Purpose                                           |
| ------------------------------------ | ----------------------- | ------------------------------------------------- |
| `oauth:state:<state>`                | 5 minutes               | Stores PKCE verifier + redirectUrl for OAuth flow |
| `session:<sessionID>`                | 24 hours (configurable) | User session data                                 |
| `user_sessions:<userID>`             | —                       | Set of active session IDs per user                |
| `workflow:response:<correlationKey>` | —                       | Pub/sub channel for Camunda workflow responses    |

---

## 7. File Reference Index

### Keycloak Theme Files

| File                                                          | Purpose                                                    |
| ------------------------------------------------------------- | ---------------------------------------------------------- |
| `keycloak-theme/lemici/login/template.ftl`                    | Shared layout for all login pages                          |
| `keycloak-theme/lemici/login/login.ftl`                       | Login form (email/password + Google)                       |
| `keycloak-theme/lemici/login/register.ftl`                    | Registration form                                          |
| `keycloak-theme/lemici/login/login-verify-email.ftl`          | Post-registration verify prompt                            |
| `keycloak-theme/lemici/login/login-reset-password.ftl`        | Forgot password form (email entry)                         |
| `keycloak-theme/lemici/login/login-update-password.ftl`       | New password entry form                                    |
| `keycloak-theme/lemici/login/info.ftl`                        | Success/status page (email verified, password reset, etc.) |
| `keycloak-theme/lemici/login/error.ftl`                       | Error page                                                 |
| `keycloak-theme/lemici/login/theme.properties`                | Theme config (extends `keycloak`)                          |
| `keycloak-theme/lemici/email/html/email-verification.ftl`     | Verification email HTML                                    |
| `keycloak-theme/lemici/email/text/email-verification.ftl`     | Verification email plain text                              |
| `keycloak-theme/lemici/email/messages/messages_en.properties` | Email subject lines                                        |
| `keycloak-theme/lemici/email/theme.properties`                | Email theme config (extends `base`)                        |

### Go Backend Files

| File                                                        | Key Lines                | Purpose                                       |
| ----------------------------------------------------------- | ------------------------ | --------------------------------------------- |
| `cmd/api-gateway/main.go:326`                               | Route registration       | `POST /login` → `StartKeycloakLogin`          |
| `cmd/api-gateway/main.go:329`                               | Route registration       | `GET /callback` → `HandleKeycloakCallback`    |
| `cmd/api-gateway/main.go:332`                               | Route registration       | `POST /password/reset` → `StartPasswordReset` |
| `cmd/api-gateway/main.go:335`                               | Route registration       | `GET /check-email` → `CheckEmailExists`       |
| `internal/api/handlers/workflow_handler.go:1124-1282`       | `StartKeycloakLogin`     | Main login handler                            |
| `internal/api/handlers/workflow_handler.go:1699-1749`       | `HandleKeycloakCallback` | OAuth callback handler                        |
| `internal/api/handlers/workflow_handler.go:1493-1675`       | `completeLoginFlow`      | Sets session cookie                           |
| `internal/api/handlers/workflow_handler.go:281-311`         | `StartPasswordReset`     | Password reset handler                        |
| `internal/api/handlers/user_handler.go:134-166`             | `CheckEmailExists`       | Email validation endpoint                     |
| `internal/api/middleware/middleware.go:374`                 | ErrorHandler             | Auth flow redirect detection                  |
| `internal/workers/auth/keycloak-signin/service.go:64-151`   | `handleInitiate`         | PKCE + state generation                       |
| `internal/workers/auth/keycloak-signin/service.go:153-248`  | `handleCallback`         | Token exchange + user resolution              |
| `internal/workers/auth/session-manager/service.go:59-133`   | `handleCreate`           | Redis session creation                        |
| `internal/common/auth/provider/keycloak/keycloak.go:80-88`  | `AuthCodeURL`            | Builds Keycloak auth URL                      |
| `internal/common/auth/provider/keycloak/keycloak.go:91-156` | `ExchangeCode`           | Token exchange + verification                 |
| `internal/common/auth/resolver/db_resolver.go:29-143`       | `Resolve`                | PostgreSQL user lookup/creation               |
| `internal/common/auth/session/redis_store.go:30-58`         | `Create`                 | Redis session storage                         |
| `internal/common/auth/session/store.go:8-22`                | `Session` struct         | Session data model                            |
| `internal/common/constants/constants.go:5-8`                | Constants                | Cookie name, path, security flags             |

### BPMN Workflows

| File                                 | Process ID                | Purpose                     |
| ------------------------------------ | ------------------------- | --------------------------- |
| `bpmn/keycloak-login-workflow.bpmn`  | `keycloak-login-workflow` | Login (initiate + callback) |
| `bpmn/keycloak-logout-workflow.bpmn` | —                         | Logout                      |
| `bpmn/public-form-submission.bpmn`   | —                         | Form submissions            |
| `bpmn/supplier-onboarding.bpmn`      | —                         | Supplier onboarding         |
| `bpmn/franchise-*.bpmn`              | —                         | Franchise workflows         |

### Configuration

| File                                                    | Key Lines                | Purpose                                              |
| ------------------------------------------------------- | ------------------------ | ---------------------------------------------------- |
| `configs/config.dev.yaml:14-31`                         | Keycloak + redirect URIs | `login_redirect_uri: "https://dev.lemici.com/login"` |
| `internal/workers/auth/keycloak-signin/config.go`       | Worker config            | `AllowedRedirectDomains`, `StateTTL`                 |
| `internal/workers/auth/session-manager/config.go:37-41` | Session config           | Cookie name, domain, TTL                             |
| `internal/common/config/config.go:269-274`              | `SessionConfig` struct   | Session configuration model                          |

---

## Appendix: Keycloak Theme Variable Reference

### Available Variables in Login Templates

| Variable                               | Source                | Description                                               |
| -------------------------------------- | --------------------- | --------------------------------------------------------- |
| `${url.loginUrl}`                      | Keycloak              | URL to the login page                                     |
| `${url.loginAction}`                   | Keycloak              | POST endpoint for login form                              |
| `${url.registrationUrl}`               | Keycloak              | URL to the registration page                              |
| `${url.registrationAction}`            | Keycloak              | POST endpoint for registration form                       |
| `${url.loginResetCredentialsUrl}`      | Keycloak              | URL to the forgot password page                           |
| `${url.loginAction}`                   | Keycloak              | POST endpoint for password reset form                     |
| `${url.resourcesPath}`                 | Keycloak              | Path to theme resources                                   |
| `${message.type}`                      | Keycloak              | Message type: `success`, `warning`, `error`, `info`       |
| `${message.summary}`                   | Keycloak              | Message text                                              |
| `${user.email}`                        | Keycloak              | Current user's email                                      |
| `${user.firstName}`                    | Keycloak              | Current user's first name                                 |
| `${realm.registrationEmailAsUsername}` | Keycloak              | Whether email is used as username                         |
| `${realm.loginWithEmailAllowed}`       | Keycloak              | Whether login with email is allowed                       |
| `${realm.password}`                    | Keycloak              | Whether password auth is enabled                          |
| `${social.displayInfo}`                | Keycloak              | Whether to show social login section                      |
| `${social.providers}`                  | Keycloak              | List of configured social providers                       |
| `${auth.attemptedUsername}`            | Keycloak              | Username that was attempted (for login form)              |
| `${isAppInitiatedAction}`              | Keycloak              | Whether action was initiated by app (shows Cancel button) |
| `${pageRedirectUri}`                   | Keycloak              | Redirect URI for "Back to Application" button             |
| `${username}`                          | Keycloak              | Username for password update form                         |
| `${link}`                              | Keycloak (email)      | Action token URL (verification/reset link)                |
| `${linkExpirationInMinutes}`           | Keycloak (email)      | Link expiration time                                      |
| `${kcSanitize(msg("key"))?no_esc}`     | Keycloak              | Sanitized i18n message                                    |
| `${properties.frontendLoginUrl!''}`    | Custom theme property | Frontend login URL (not currently used)                   |

### Keycloak Internal URLs

| URL                                                          | Purpose                                       |
| ------------------------------------------------------------ | --------------------------------------------- |
| `${url.loginAction}`                                         | Handles all login-related form POSTs          |
| `${url.registrationAction}`                                  | Handles registration form POST                |
| `${url.loginUrl}`                                            | Renders the login page                        |
| `${url.registrationUrl}`                                     | Renders the registration page                 |
| `${url.loginResetCredentialsUrl}`                            | Renders the forgot password page              |
| `/realms/{realm}/protocol/openid-connect/auth`               | OAuth authorization endpoint                  |
| `/realms/{realm}/protocol/openid-connect/token`              | OAuth token endpoint                          |
| `/admin/realms/{realm}/users/{userId}/execute-actions-email` | Triggers action email (password reset, etc.)  |
| `/login-actions/execute-actions`                             | Handles action token execution                |
| `/login-actions/execute`                                     | Handles action token execution (verification) |
