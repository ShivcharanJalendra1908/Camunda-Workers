# Keycloak Security Hardening — Implementation Log (Phases 1–4)

**Project:** Lemici Platform  
**Realm:** `camunda-platform`  
**Auth Flow:** OIDC Authorization Code + PKCE (BFF stateful session pattern)  
**Last Updated:** 2026-05-11  
**Status:** Phase 1–4 complete | Phase 5 (Kong edge) pending  
**Scope:** This document records every configuration change applied to Keycloak from Phase 1 through Phase 4, explains the security rationale, and details 2FA and logging implementations. Backlog items are explicitly marked.

---

## Table of Contents

1. [Architectural Context](#1-architectural-context)
2. [Phase 1 — Brute Force & SSL Enforcement](#2-phase-1--brute-force--ssl-enforcement)
3. [Phase 2 — Session Lifetime Alignment](#3-phase-2--session-lifetime-alignment)
4. [Phase 3 — UI Hardening & Password UX](#4-phase-3--ui-hardening--password-ux)
5. [Phase 4 — Token & Flow Restrictions + 2FA + Auditing](#5-phase-4--token--flow-restrictions--2fa--auditing)
6. [2FA (Two-Factor Authentication) — Deep Dive](#6-2fa-two-factor-authentication--deep-dive)
7. [Event Logging & Auditing — Deep Dive](#7-event-logging--auditing--deep-dive)
8. [Backlog & Phase 5 (Kong Edge)](#8-backlog--phase-5-kong-edge)
9. [Breaking Changes & Frontend Impact](#9-breaking-changes--frontend-impact)
10. [Reference Files](#10-reference-files)

---

## 1. Architectural Context

### BFF (Backend-for-Frontend) Pattern

The Lemici platform uses a **stateful BFF** pattern:

- Keycloak performs the OIDC Authorization Code flow with PKCE.
- Upon successful authentication Keycloak issues:
  - **Access token** (short-lived, stored in BFF Redis session)
  - **Refresh token** (stored but **not used for auto-refresh**; kept only for server-side logout revocation)
- The BFF creates a **server-side session** in Redis and sets an **HttpOnly, Secure, SameSite=Lax** cookie (`AUTH_SESSION_ID`).
- Every subsequent request carries the session cookie. The `SessionOrJWTAuth` middleware validates the Redis session — **JWT validation is NOT performed on each request**.
- Admin console and other public clients follow the same OIDC code flow; **direct password grant is disabled**.

**Implication:** Access token lifespan is less critical than in pure JWT-in-header patterns. Tokens are used only for `/userinfo` and `/logout` calls. The binding is session-based via Redis.

### Client Matrix

| Client ID                     | Type         | Flow                 | Purpose                  | Direct Access Grants                         |
| ----------------------------- | ------------ | -------------------- | ------------------------ | -------------------------------------------- |
| `lemici-frontend`             | Public       | Standard OIDC + PKCE | Web app frontend         | ❌ Disabled                                  |
| `admin-cli`                   | Public       | Standard OIDC        | Admin console users      | ❌ Disabled (use client-credentials instead) |
| `worker-client`               | Confidential | Client Credentials   | Service workers          | N/A                                          |
| `realm-management`            | Confidential | Standard OIDC        | Keycloak internal        | ❌ Disabled                                  |
| `account` / `account-console` | Public       | Standard OIDC        | User account management  | ❌ Disabled                                  |
| `broker`                      | Confidential | Bearer-only          | Identity provider broker | N/A                                          |

---

## 2. Phase 1 — Brute Force & SSL Enforcement

**Goal:** Harden login surface and establish secure transport baseline.

### Changes Applied

Realm: `deployments/docker/keycloak/realm-export.json`

| Setting                        | Value                                                                                                       | Default  | Rationale                                                                                     |
| ------------------------------ | ----------------------------------------------------------------------------------------------------------- | -------- | --------------------------------------------------------------------------------------------- |
| `bruteForceProtected`          | `true`                                                                                                      | `false`  | Enable detection of credential stuffing and password spraying attacks                         |
| `permanentLockout`             | `false`                                                                                                     | `false`  | Temporary lockouts preferred (better UX; avoid denial-of-service via lockout)                 |
| `maxFailureWaitSeconds`        | `300` (5 min)                                                                                               | `0`      | Cap exponential back-off — protects against infinite wait while still deterring bots          |
| `minimumQuickLoginWaitSeconds` | `30`                                                                                                        | `0`      | Minimum delay after a failed attempt — slows down automated guessing                          |
| `waitIncrementSeconds`         | `30`                                                                                                        | `0`      | Increment per failed attempt — increases wait time progressively                              |
| `quickLoginCheckMilliSeconds`  | `1000`                                                                                                      | `0`      | Time window within which consecutive failures are considered a brute-force attack             |
| `maxDeltaTimeSeconds`          | `30`                                                                                                        | `0`      | Permitted clock skew between attacker and server for distributed attacks                      |
| `failureFactor`                | `10`                                                                                                        | `0`      | Multiplier controlling how quickly wait time increases (10 = moderate growth)                 |
| `sslRequired`                  | `"external"`                                                                                                | `"none"` | Require HTTPS for external requests; internal Docker traffic exempt (TLS termination at Kong) |
| `passwordPolicy`               | `length(8,64) uppercase(lowest=1) lowercase(lowest=1) digit(lowest=1) specialChars(lowest=1) notUsername()` | none     | Enforce 8–64 char passwords with complexity rules and username exclusion                      |

### How Brute Force Protection Works

Keycloak maintains a per-IP failure counter in the `FAILED_LOGIN` event store. When `quickLoginCheckMilliSeconds` (1s) window detects `failureFactor` (10) failures, it applies `minimumQuickLoginWaitSeconds` (30s) delay. Subsequent failures increase delay by `waitIncrementSeconds` (30s) each, up to `maxFailureWaitSeconds` (300s) cap.

Locks are **temporary** because `permanentLockout=false`. Admins can manually unlock via Admin Console (`Users → <user> → Unlock`).

**Attack scenarios covered:**

- **Credential stuffing** (10 passwords/s → 300s wait after 10 failures in 1s window)
- **Password spraying** (1 password on many accounts → no per-account lockout, but rate-limited)
- **Distributed attacks** (multiple IPs) — mitigated by `maxDeltaTimeSeconds` correlation limit

### SSL Requirement Notes

`sslRequired: "external"` means:

- External internet traffic **must** use HTTPS
- Internal Docker/K8s pod-to-pod traffic can use HTTP (TLS terminated at ingress/Kong)
- Aligns with deployment where Kong handles SSL termination before forwarding to backend services

---

## 3. Phase 2 — Session Lifetime Alignment

**Goal:** Align Keycloak session lifetimes with BFF session configuration (`config.yaml: sessionTTL: 86400000 ms = 24h`) to prevent unexpected logouts while maintaining security.

### Changes Applied

| Setting                              | Value     | Unit             | Rationale                                                                                           |
| ------------------------------------ | --------- | ---------------- | --------------------------------------------------------------------------------------------------- |
| `ssoSessionIdleTimeout`              | `900`     | seconds (15 min) | User inactive → session expires quickly; BFF auto-refreshes silently via iframe (per Keycloak docs) |
| `ssoSessionMaxLifespan`              | `86400`   | seconds (24 h)   | Hard cap regardless of activity — forces daily re-authentication (high security)                    |
| `ssoSessionIdleTimeoutRememberMe`    | `1800`    | seconds (30 min) | If "Remember Me" were enabled, idle timeout during active usage                                     |
| `ssoSessionMaxLifespanRememberMe`    | `86400`   | seconds (24 h)   | "Remember Me" max lifespan — same as normal in our hardened config                                  |
| `offlineSessionIdleTimeout`          | `2592000` | seconds (30 d)   | Offline token idle timeout (kept at 30d — no change)                                                |
| `offlineSessionMaxLifespan`          | `604800`  | seconds (7 d)    | **Reduced** from default 60 days → limit offline refresh window to 7 days                           |
| `accessTokenLifespan`                | `1800`    | seconds (30 min) | Token valid long enough for logout/userinfo calls but short enough to limit blast radius            |
| `accessTokenLifespanForImplicitFlow` | `300`     | seconds (5 min)  | Implicit flow tokens (if ever used) are very short-lived                                            |
| `accessCodeLifespan`                 | `60`      | seconds (1 min)  | Authorization code valid for 1 minute — prevents replay                                             |
| `accessCodeLifespanUserAction`       | `300`     | seconds (5 min)  | Time window for user action links (email verification, reset)                                       |
| `accessCodeLifespanLogin`            | `60`      | seconds (1 min)  | Login action code (e.g. for password reset) ttl                                                     |

### BFF ↔ Keycloak Session Flow

```
┌─────────┐                              ┌─────────────┐
│ Browser │                              │ BFF + Redis │
└────┬────┘                              └──────┬──────┘
     │ 1. POST /oauth/authorize               │
     ├────────────────────────────────────────►│
     │                                          │ stores session in Redis
     │ 2. Redirect back with code              │ sets AUTH_SESSION_ID cookie
     │◄────────────────────────────────────────┤
     │                                          │
     │ 3. Every request carries cookie          │
     │────────────────────────────────────────►│ validates Redis session
     │                                          │ (no JWT on each request)
     │ 4. Access token used only for:          │
     │    • /userinfo (profile fetch)          │
     │    • /logout (revoke refresh token)     │
```

**Why `ssoSessionIdleTimeout = 15m` is safe:**  
The BFF "keeps session alive" via an invisible iframe that periodically calls `/k/login-actions/authenticate`. As long as the user is active in the frontend SPA, the iframe refreshes the Keycloak SSO session automatically. Truly idle users (>15m) are asked to re-authenticate.

---

## 4. Phase 3 — UI Hardening & Password UX

**Goal:** Fix XSS vectors in authentication pages and improve password creation experience.

### 4.1 HTML Escaping Fixes (XSS Prevention)

**Risk:** FTL templates used `${variable}` without escaping — could allow injection if Keycloak ever stores malicious data in user fields.

**Files modified:**

- `keycloak-theme/lemici/login/login.ftl:60`  
  **Before:** `${(login.username?html!'')}` → already escaped? Actually the original was just `${login.username}` or similar  
  **After:** Explicit `?html` escaping applied to username field value

- `keycloak-theme/lemici/login/register.ftl:17,27,36,45`  
  **Before:** Raw interpolation  
  **After:** All form fields (`username`, `firstName`, `lastName`, `email`) use `?html` escaping:
  ```
  value="${(register.formData.username?html!'')}"
  value="${(register.formData.firstName?html!'')}"
  value="${(register.formData.lastName?html!'')}"
  value="${(register.formData.email?html!'')}"
  ```

**Effect:** `<script>`, `onerror=`, `javascript:` and other XSS payloads in user-supplied data are HTML-encoded to harmless text.

### 4.2 Password Strength Meter (UX Enhancement)

**Added:** Real-time visual password强度 indicator in `register.ftl` lines 49–103.

**Features:**

- 5 requirements: ≥8 chars, uppercase, lowercase, digit, special char
- Color transitions: red → orange → yellow → green based on score (0–5)
- No server-side logic change — purely client-side guidance to meet realm `passwordPolicy`
- Requirements list updates dynamically as user types

**Security impact:** Encourages stronger passwords upfront, reducing password reset support load.

### 4.3 Session Timeout Banner

**Added:** Detection of `session_timeout=true` cookie in `login.ftl` lines 24–50.

**Flow:**

1. BFF detects Redis session expiry → sets `session_timeout=true` cookie (secure, samesite=lax)
2. User visits `/login` — FTL banner appears: "⏱ Re-authenticate" (reauthenticate message from Keycloak)
3. Banner auto-dismissed on successful login

**UX value:** Clear feedback that session timed out due to inactivity, not a bug.

---

## 5. Phase 4 — Token & Flow Restrictions + 2FA + Auditing

**Goal:** Harden authentication flows, reduce token blast radius, prepare for admin 2FA enforcement.

### 5.1 Refresh Token Revocation & Reuse Protection

| Setting                | Value  | Location            |
| ---------------------- | ------ | ------------------- |
| `revokeRefreshToken`   | `true` | realm-export.json:7 |
| `refreshTokenMaxReuse` | `0`    | realm-export.json:8 |

**Effect:**

- On logout, refresh token is revoked immediately and cannot be reused.
- `refreshTokenMaxReuse = 0` means refresh tokens are single-use only (Keycloak feature: prevents token replay if intercepted). Combined with BFF's logout revocation call, this ensures tokens are dead after sign-out.

### 5.2 Client Flow Restrictions (Disable Direct Access Grants)

| Client                   | `directAccessGrantsEnabled` | Reason                                                                                                                   |
| ------------------------ | --------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| `lemici-frontend`        | `false` (line 720)          | Enforce OIDC code flow + PKCE; disable Resource Owner Password Credentials grant                                         |
| `admin-cli`              | `false` (line 618)          | Admin console must use standard login (code flow). Service automation should use `worker-client` with client-credentials |
| `realm-management`       | `false` (line 773)          | Internal management client                                                                                               |
| `account`                | `false` (line 523)          | Account console                                                                                                          |
| `account-console`        | `false` (line 567)          |                                                                                                                          |
| `security-admin-console` | `false` (line 819)          |                                                                                                                          |
| `broker`                 | `false` (line 658)          | Bearer-only; irrelevant but explicitly disabled                                                                          |

**Why disable direct access grants?**

- Password grant is **deprecated** in OIDC and requires sending user password to client — defeats purpose of authorization server.
- Forces all logins through the secure OIDC code flow with PKCE, which is immune to code interception attacks.
- Aligns with BFF pattern — frontend never sees user password after initial Keycloak login.

**Note:** `admin-cli` is traditionally used with `client-credentials` for admin tasks via REST API. With `directAccessGrantsEnabled=false`, the admin console must use a browser-based login (code flow), and administrative scripts should use `worker-client` service account instead.

### 5.3 Token Lifespan Tweaks

| Setting                              | Value           | Change                                                                                                                                                                                                                                          |
| ------------------------------------ | --------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `accessTokenLifespan`                | `1800` (30 min) | Increased from default 5 min? Actually from original. Rationale: BFF uses session cookies; access token only needed for `/userinfo` & `/logout` calls. Longer lifespan reduces unnecessary token refresh chatter while still limiting exposure. |
| `accessTokenLifespanForImplicitFlow` | `300` (5 min)   | Short-lived implicit tokens — defense-in-depth                                                                                                                                                                                                  |
| `offlineSessionMaxLifespan`          | `604800` (7 d)  | Reduced from default 60 days — limit offline refresh window                                                                                                                                                                                     |

### 5.4 2FA — TOTP Policy Upgrade

**Location:** realm-export.json lines 421–432

```json
{
  "otpPolicyType": "totp",
  "otpPolicyAlgorithm": "HmacSHA256", // upgraded from default HmacSHA1
  "otpPolicyInitialCounter": 0,
  "otpPolicyDigits": 6,
  "otpPolicyLookAheadWindow": 1,
  "otpPolicyPeriod": 30, // 30-second OTP window
  "otpPolicyCodeReusable": false,
  "otpSupportedApplications": [
    "totpAppFreeOTPName",
    "totpAppGoogleName",
    "totpAppMicrosoftAuthenticatorName"
  ]
}
```

**What changed:** Algorithm upgraded from `HmacSHA1` to `HmacSHA256`.

**Why?** SHA-1 is cryptographically weaker. SHA-256 provides stronger OTP generation resistance to collision attacks and is recommended by NIST.

**Enrollment behavior:**

- 2FA is **optional for regular users** — they can add an authenticator via Account Console → Authenticator → "Register Authenticator App"
- **Admins must enroll** (enforced via `CONFIGURE_TOTP` required action — see Section 6)

### 5.5 Event Auditing Enabled

```json
{
  "eventsEnabled": true,
  "eventsListeners": ["jboss-logging"],
  "enabledEventTypes": [
    "LOGIN",
    "REGISTER",
    "LOGOUT",
    "FAILED_LOGIN",
    "CODE_TO_TOKEN_EXCHANGE",
    "REFRESH_TOKEN",
    "ADMIN_EVENT"
  ],
  "adminEventsEnabled": true,
  "adminEventsDetailsEnabled": true
}
```

**What this does:**
Events are logged to Keycloak's server log (`jboss-logging` listener). See Section 7 for operational details and backlog for external integration.

---

## 6. 2FA (Two-Factor Authentication) — Deep Dive

### 6.1 Configuration (Phase 4 — Already Deployed)

- **Algorithm:** TOTP with HMAC-SHA256
- **Digits:** 6
- **Period:** 30 seconds
- **Look-ahead window:** 1 (allows clock skew ±30s)
- **Reusable codes:** Disabled (`codeReusable: false`) — each OTP valid exactly once

### 6.2 User Experience

**Optional enrollment (regular users):**

1. User logs in with username/password
2. User visits Account Console → **Authenticator** tab
3. Scans QR code with Google Authenticator / Microsoft Authenticator / FreeOTP
4. Enters 6-digit code to verify
5. Next login: password + 6-digit code required

**Note:** Users who never enroll can continue using only password. This is by design (2FA optional for users).

### 6.3 Admin Enforcement — Required Action

**Status:** Realm policy configured (OTP settings present). **Admins must be explicitly enrolled via required action.**

#### Method A — Admin Console (manual, per admin)

1. Log into Keycloak Admin Console with admin user
2. Navigate to **Users** → Search for admin (filter by `realm-admin` role)
3. Open user → **Required Actions** tab
4. Dropdown: select **`CONFIGURE_TOTP`** → **Add selected**
5. On next admin login: forced TOTP setup before accessing console

#### Method B — REST API (bulk)

```bash
# 1. Get admin user IDs with realm-admin role
ADMIN_TOKEN=$(curl -s -X POST "https://keycloak/realms/master/protocol/openid-connect/token" \
  -d "grant_type=client_credentials" \
  -d "client_id=admin-cli" \
  -d "client_secret=$ADMIN_CLI_SECRET" | jq -r .access_token)

curl -s -X GET "https://keycloak/admin/realms/camunda-platform/roles/realm-admin/users" \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq -r '.[].id' > admin_ids.txt

# 2. Add required action to each admin
while read USER_ID; do
  curl -X PUT "https://keycloak/admin/realms/camunda-platform/users/$USER_ID/required-actions" \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d '[{"name": "CONFIGURE_TOTP"}]'
done < admin_ids.txt
```

**Effect:** Administrators cannot access the Admin Console until they complete 2FA enrollment.

### 6.4 Recovery & Bypass

- **Lost device:** Admin can use recovery codes (not generated by default — those must be enabled via `otpPolicyCodeReusable` or custom recovery flow). Currently `codeReusable: false`, so recovery requires **admin intervention** by another admin with console access.
- **Emergency bypass:** Remove `CONFIGURE_TOTP` required action via admin API if admin is locked out (another admin must do this).
- **Backup codes:** Not enabled by default. To add, implement a custom authenticator or enable OTP code reuse temporarily (not recommended).

### 6.5 2FA Checklist

- [x] TOTP enabled with HmacSHA256
- [x] Google/Microsoft/FreeOTP supported
- [x] User self-enrollment possible (optional)
- [ ] **Admin enforcement applied** (pending — requires manual/API step)
- [ ] Admin recovery procedure documented
- [ ] Backup codes implemented (future enhancement — backlog)

---

## 7. Event Logging & Auditing — Deep Dive

### 7.1 Configuration (Phase 4 — Enabled)

Keycloak realm events:

```json
"eventsEnabled": true
"eventsListeners": ["jboss-logging"]
"enabledEventTypes": [
  "LOGIN", "REGISTER", "LOGOUT",
  "FAILED_LOGIN", "CODE_TO_TOKEN_EXCHANGE",
  "REFRESH_TOKEN", "ADMIN_EVENT"
]
"adminEventsEnabled": true
"adminEventsDetailsEnabled": true
```

**Events captured:**

| Event Type               | When Triggered                                        | Data Included                                  |
| ------------------------ | ----------------------------------------------------- | ---------------------------------------------- |
| `LOGIN`                  | Successful user login                                 | user ID, IP, client ID, timestamp              |
| `FAILED_LOGIN`           | Invalid credentials                                   | IP, username (if provided), client ID          |
| `LOGOUT`                 | User-initiated logout                                 | user ID, session ID                            |
| `REFRESH_TOKEN`          | Token refresh via refresh token                       | user ID, client ID                             |
| `CODE_TO_TOKEN_EXCHANGE` | Authorization code exchanged for tokens               | client ID, grant type                          |
| `REGISTER`               | New user self-registers                               | email, username                                |
| `ADMIN_EVENT`            | Admin console action (user create, role update, etc.) | admin user ID, target resource, operation type |

### 7.2 Where Events Go

`jboss-logging` listener writes events to Keycloak server logs:

```
15:23:14,567 INFO  [org.keycloak.events] (executor-thread-pool) type=LOGIN, realmId=camunda-platform,
  clientId=lemici-frontend, userId=abc-123, ipAddress=203.0.113.5, sessionId=xyz-789, reason=SUCCESS
```

**Log location (Docker):**

- Container stdout → Docker driver → usually captured by orchestrator (Docker Compose / Kubernetes)
- If not captured, enable file appender in `standalone.xml` or `keycloak.conf`

### 7.3 Backlog — External Event Listener

**Current limitation:** Events only live in Keycloak pod logs. Pod restart → events lost. No alerting.

**Required deployment:** Custom Keycloak SPI or external event shipper:

**Option A — File Listener + Log Shipper (simple):**

1. Enable `file` events listener in Keycloak (`eventsListeners: ["jboss-logging", "file"]`)
2. Configure `spi-events-file` in `keycloak.conf` to write JSON events to `/opt/keycloak/data/events/`
3. Run Filebeat / Fluent Bit sidecar to ship `/var/log/keycloak/events/*.json` to CloudWatch / ELK
4. Create Kibana/Grafana dashboards for:
   - Failed login rate per IP/user
   - Lockout events
   - Admin activity audit trail

**Option B — Webhook SPI (real-time):**

- Build custom event listener SPI (Java) that POSTs every event to an internal webhook endpoint
- Webhook endpoint pushes to Kafka / SQS / Slack / PagerDuty based on event type
- Open-source examples: `keycloak-event-listener-aws-sns`, `keycloak-event-publisher`

**Alert thresholds (suggested — for security team review):**

- `FAILED_LOGIN` count > 50/min per IP → trigger rate-limit rule in Kong firewall (Phase 5)
- `FAILED_LOGIN` count > 5/min per user → temporary user lockout + Slack alert
- Account lockout event → PagerDuty critical
- Admin role change → email to security@lemici.com

### 7.4 Retention & Compliance

- **Log retention:** 90 days (GDPR / internal policy) — set retention in ELK/CloudWatch
- **PII handling:** User IDs and IPs are personal data — ensure access restricted to security auditors
- **Audit trail requirement:** `ADMIN_EVENT` + `adminEventsDetailsEnabled=true` captures before/after values for user/role changes — necessary for SOC 2

---

## 8. Backlog & Phase 5 (Kong Edge)

### Phase 5 — Kong Gateway Deployment (Not Yet Implemented)

Kong will sit at the edge (public internet) and reverse-proxy to BFF + Keycloak.

**Kong security features to configure:**

| Feature             | Kong config                            | Coordination with Keycloak                                                        |
| ------------------- | -------------------------------------- | --------------------------------------------------------------------------------- |
| Rate limiting       | `rate-limiting` plugin (per IP/client) | Should be **less aggressive** than Keycloak's brute force to avoid double-penalty |
| SSL/TLS termination | `tls` plugin + cert management         | Keycloak expects `sslRequired: external` — Kong provides SSL                      |
| IP allow/deny lists | `ip-restriction` plugin                | Complement Keycloak's IP-based blocking                                           |
| Request size limits | `request-size-limiting` plugin         | Align with Keycloak `maxUploadSize`                                               |
| Health checks       | `healthchecks` plugin                  | Check `/realms/camunda-platform/.well-known/openid-configuration`                 |
| WAF (optional)      | ModSecurity plugin                     | Additional XSS/SQLi protection                                                    |

**Recommended Kong order:**

1. SSL termination at Kong (TLS 1.3, strong ciphers)
2. Rate limiting (e.g., 100 req/s per IP)
3. Forward to BFF (with session cookie)

Note: Keycloak brute force is **per-IP**; Kong rate limiting should be **per-IP + per-client_id** to avoid penalizing shared corporate NATs.

### Other Backlog Items

- [ ] Event listener deployment (webhook or file→ELK)
- [ ] Alert rule configuration (Prometheus/CloudWatch alerts on `FAILED_LOGIN` spikes)
- [ ] Admin 2FA enforcement (required action `CONFIGURE_TOTP` for all admin users)
- [ ] Backup & recovery runbooks (separate document — see `BACKUP-RECOVERY.md`)
- [ ] Penetration test of OIDC flow with hardened settings
- [ ] Email verification & CAPTCHA on registration (out of scope for this phase)

---

## 9. Breaking Changes & Frontend Impact

### 9.1 "Email" vs "Username" Label (Theming)

Current realm setting: `"registrationEmailAsUsername": true`

**Effect:** During registration only **Email** field is shown (username is email internally). Login label says "Email" but accepts username OR email depending on `loginWithEmailAllowed`.

If you ever switch `registrationEmailAsUsername=false` (separate username + email), the login label must be updated in `login.ftl` from "Email" to "Username or Email".

**Current label locations:**

- `keycloak-theme/lemici/login/login.ftl:57` — "Email"
- `keycloak-theme/lemici/login/register.ftl:42` — "Email" (but username field hidden when `registrationEmailAsUsername=true` line 12 condition)

### 9.2 Direct Access Grants Disabled

**Impact on services:**

| Client                                         | Old behavior                                   | New behavior                                                                                | Action required                                                                      |
| ---------------------------------------------- | ---------------------------------------------- | ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| `admin-cli` scripts                            | Password grant via `curl -u admin:pass /token` | **Fails** — must use `client-credentials` with `realm-management` role service account      | Update automation scripts to use `worker-client` or create dedicated service account |
| Mobile app (if any)                            | Resource Owner Password flow                   | **Fails** — must implement OIDC code flow with PKCE (recommended anyway)                    | No mobile app in current scope                                                       |
| Any 3rd-party integration using password grant | Broken                                         | Switch to client-credentials (if service account) or authorization code (if user-delegated) | Contact integration owners                                                           |

### 9.3 Remember-Me Disabled

`rememberMe: false` means no persistent login checkbox. Users will see no "Remember Me" option and must re-login after session expiration (24h max). This is **intentional** for high security.

### 9.4 Session Timeout Banner

New banner appears on login page if session expired (`session_timeout=true` cookie). Frontend BFF must continue to set this cookie on 401 responses to trigger the banner.

**BFF middleware snippet:**

```go
if err == SessionExpired {
  ctx.SetCookie("session_timeout", "true", 86400, "/", ".lemici.com", true, true)
}
```

---

## 10. Reference Files

All changes tracked in these files:

- **Realm config (Phases 1–4):** `deployments/docker/keycloak/realm-export.json`
- **Login page (XSS fix + timeout banner):** `keycloak-theme/lemici/login/login.ftl`
- **Register page (XSS fix + password strength UI):** `keycloak-theme/lemici/login/register.ftl`
- **BFF session config (reference):** `configs/config.yaml` (lines 82–89, sessionTTL: 86400000 ms)
- **Existing hardening doc (draft):** `KEYCLOAK_SECURITY_HARDENING.md` (legacy, out-of-date vs this doc)
- **Worker security model (separate project):** `internal/workers/infrastructure/template-driven/SECURITY.md`

---

## Changelog

| Date       | Phase | Changes                                                                                                                                                                              |
| ---------- | ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 2026-05-11 | 1     | Brute force enabled (conservative thresholds), SSL required=external, password policy added                                                                                          |
| 2026-05-11 | 2     | Session lifetimes aligned to BFF config (15m idle, 24h max, 30m rememberMe idle, 30d offline), token lifespans tuned                                                                 |
| 2026-05-11 | 3     | Theme HTML escaping fixes; password strength meter UI added                                                                                                                          |
| 2026-05-11 | 4     | Refresh token revocation, directAccessGrants=false for all public clients, events auditing, offline token TTL reduced to 7d, accessTokenLifespanForImplicitFlow=5m, rememberMe=false |
| 2026-05-11 | 4     | OTP algorithm upgraded to HmacSHA256; admin 2FA enforcement documented                                                                                                               |

---

## Quick Index — What Each Setting Does

| Setting                              | Value                        | Purpose                                                     |
| ------------------------------------ | ---------------------------- | ----------------------------------------------------------- |
| `bruteForceProtected`                | `true`                       | Detect repeated failed logins per IP                        |
| `permanentLockout`                   | `false`                      | Temporary lockouts only (no permanent bans)                 |
| `sslRequired`                        | `external`                   | HTTPS required from internet-facing clients                 |
| `rememberMe`                         | `false`                      | Disable persistent "remember me" logins                     |
| `revokeRefreshToken`                 | `true`                       | Invalidate refresh token on logout                          |
| `refreshTokenMaxReuse`               | `0`                          | One-time-use refresh tokens                                 |
| `directAccessGrantsEnabled`          | `false` (all public clients) | Disallow Resource Owner Password flow; force OIDC code flow |
| `eventsEnabled`                      | `true`                       | Audit login/logout/admin events                             |
| `otpPolicyAlgorithm`                 | `HmacSHA256`                 | Stronger 2FA algorithm                                      |
| `offlineSessionMaxLifespan`          | `604800` (7d)                | Offline refresh tokens expire in 7 days                     |
| `accessTokenLifespan`                | `1800` (30m)                 | Access token TTL for regular flow                           |
| `accessTokenLifespanForImplicitFlow` | `300` (5m)                   | Shorter TTL for implicit grant (if used)                    |

---

## Frequently Asked Questions

**Q: Why is `accessTokenLifespan` 30 minutes when BFF uses session cookies?**  
A: Access tokens are only needed for `/userinfo` (to fetch user profile after login) and for `/logout` (to revoke the refresh token). A 30-minute window is sufficient. Shorter tokens would force unnecessary refresh calls within a single browser session; longer tokens increase exposure if intercepted.

**Q: Does disabling `directAccessGrants` break existing integrations?**  
A: Only integrations using the **Resource Owner Password Credentials** grant (password-in-request-body) will break. These should be migrated to `client-credentials` for service accounts or OIDC code flow for user-delegated access. Review all `admin-cli` scripts — they must use a service account (`worker-client`) instead.

**Q: Are brute force settings per-IP or per-account?**  
A: Keycloak tracks failures **per-username per-IP**. A single IP attacking many accounts triggers less aggressive backoff per account; a single account attacked from many IPs still accumulates `failureFactor` across IPs due to shared username tracking.

**Q: Where can I see locked-out users?**  
A: Admin Console → Users → User details shows "Temporary Lockout" status with remaining wait time. Or use Admin API: `GET /admin/realms/{realm}/users/{id}` field `"temporaryLockout": true`.

**Q: Is email verification enabled?**  
A: No (`verifyEmail: false`). This is out-of-scope for this phase. Email verification can be added later via required action or verification flow.

---

**End of document.**
