# LeMiCi Keycloak Theme — Development Context Document

> **Theme Name:** `lemici`  
> **Parent (login):** `keycloak`  
> **Parent (email):** `base`  
> **Brand:** LeMiCi IQ — AI-powered marketing automation platform (WhatsApp Business API, web push, social media tools)  
> **Primary Color:** `#6D3E93` (purple)  
> **BFF Frontend:** Go-based API Gateway serving `dev.lemici.com` / `www.lemici.com` / `localhost:3000`

---

## 1. Architecture & Flow Overview

### Theme Structure

The `lemici` theme is a custom Keycloak theme split into two independent sub-themes:

- **`login/`** — Handles all login-screen UI: authentication forms, registration, password reset, email verification status, error pages. Inherits from `keycloak` parent via `theme.properties`.
- **`email/`** — Handles outbound transactional emails (email verification). Inherits from `base` parent via `theme.properties`.

### Lifecycle Flow

```
┌─────────────────────────────────────────────────────────────────────┐
│                        USER REGISTRATION FLOW                       │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  1. register.ftl                                                   │
│     ├─ Form: username (if !emailAsUsername), firstName, lastName,   │
│     │         email, password, password-confirm                     │
│     ├─ Client-side: password strength meter, match indicator,       │
│     │   terms checkbox gate, client-side validation                 │
│     ├─ Social: Google OAuth button (via social.providers loop)     │
│     └─ On submit → Keycloak creates user + triggers email           │
│                                                                     │
│  2. email/html/email-verification.ftl  (HTML email sent)           │
│     email/text/email-verification.ftl  (plain text fallback)       │
│     ├─ Contains ${link} (Keycloak action token URL)                │
│     ├─ Contains ${linkExpirationInMinutes}                         │
│     └─ Subject: "Welcome to Lemici! Verify your email"            │
│                                                                     │
│  3. login-verify-email.ftl  (displayed after registration submit)  │
│     ├─ Shows "email sent" confirmation with user.email             │
│     ├─ "Sign In" button → handleReturnToLogin()                    │
│     ├─ "Resend Verification Email" link → ${url.loginAction}       │
│     └─ JS: Polls localStorage for 'email_verified' flag            │
│        (set by info.ftl when user clicks verification link)        │
│                                                                     │
│  4. User clicks email link → Keycloak processes action token       │
│     ├─ SUCCESS → info.ftl (message.type=success, summary contains  │
│     │            "verified") → shows "Email Verified Successfully" │
│     │            with "Sign In to LeMiCi" button                   │
│     ├─ EXPIRED/USED → error.ftl (action token failure)             │
│     │            → shows "link no longer valid" with Sign In btn   │
│     └─ CROSS-BROWSER PANIC → error.ftl (AUTH_SESSION_ID missing)  │
│              → currently shows generic error (NEEDS FIX)           │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│                         LOGIN FLOW                                  │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  login.ftl                                                         │
│  ├─ Session timeout banner (reads `session_timeout` cookie)        │
│  ├─ Form: username + password with eye toggle                      │
│  ├─ Submit button disabled until both fields non-empty             │
│  ├─ "Forgot password?" → login-reset-password.ftl                 │
│  ├─ Social: Google OAuth button                                    │
│  └─ "Create an account" → register.ftl                            │
│                                                                     │
│  On successful auth → Keycloak redirects to BFF callback           │
│  On failure → page reloads with message.type="error"              │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│                      PASSWORD RESET FLOW                            │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  1. login-reset-password.ftl                                       │
│     ├─ Form: email/username input                                  │
│     ├─ Pre-submit validation: fetches /api/v1/auth/check-email    │
│     │   to verify email exists before submitting to Keycloak       │
│     ├─ Rate limiting: 3 failed attempts → auto-redirect to signup │
│     │   (tracked in sessionStorage)                                │
│     └─ On submit → Keycloak sends reset email                      │
│                                                                     │
│  2. User clicks reset link → login-update-password.ftl            │
│     ├─ Hidden username/password fields (browser autofill compat)  │
│     ├─ Form: password-new + password-confirm with eye toggles     │
│     ├─ "Logout from other devices" checkbox (default checked)     │
│     ├─ Client-side: validates match + min 8 chars                 │
│     ├─ If isAppInitiatedAction → shows Cancel + Reset buttons     │
│     └─ On submit → Keycloak updates password                       │
│                                                                     │
│  3. After password change → info.ftl                               │
│     ├─ Sets localStorage flag 'password_reset_success'            │
│     ├─ 5-second countdown auto-redirect to login                   │
│     └─ Cross-tab: other tabs listen for storage event and redirect │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│                       ERROR / INFO STATES                           │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  error.ftl                                                         │
│  ├─ Used for: expired/used action tokens, cross-device panics     │
│  ├─ Shows: warning icon + "link no longer valid" message           │
│  ├─ "Sign In" button → handleReturnToLogin()                      │
│  └─ NO detection of AUTH_SESSION_ID panic vs genuine expiry        │
│                                                                     │
│  info.ftl                                                          │
│  ├─ Dynamic icon based on message.type (success/warning/error/info)│
│  ├─ If summary contains "verified" → email verified success page  │
│  │   Sets localStorage 'email_verified' flag for cross-tab sync   │
│  ├─ If summary contains "receive/instruction/sent" → email sent   │
│  │   page with storage event listener for password_reset_success  │
│  └─ Default → auto-redirect countdown to login with storage sync  │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Cross-Tab Communication Protocol

The theme uses `localStorage` as a cross-tab signaling mechanism:

| Key                      | Set By                               | Consumed By                    | Purpose                                                |
| ------------------------ | ------------------------------------ | ------------------------------ | ------------------------------------------------------ |
| `email_verified`         | `info.ftl` (on verification success) | `login-verify-email.ftl`       | Notifies waiting tab that email was verified           |
| `password_reset_success` | `info.ftl` (after password change)   | `info.ftl` (email-sent branch) | Notifies password-reset-email tab that reset completed |

Both consumers use dual detection: `storage` event listener + 1-second polling interval as fallback for browsers that block storage events.

---

## 2. The De-Duplication & Refactoring Map (Critical)

### 2.1 `handleReturnToLogin()` — Duplicated 3 Times

The exact same environment-routing JavaScript function is copy-pasted across three files:

| File                     | Lines | Context                                     |
| ------------------------ | ----- | ------------------------------------------- |
| `error.ftl`              | 34–45 | Inline `<script>` inside error message div  |
| `info.ftl`               | 11–22 | Inline `<script>` at top of form section    |
| `login-verify-email.ftl` | 44–55 | Inline `<script>` at bottom of form section |

**Identical logic (all three):**
```javascript
function handleReturnToLogin() {
    var host = window.location.hostname;
    if (host.indexOf('dev') !== -1 && host.indexOf('lemici.com') !== -1) {
        window.location.href = 'https://dev.lemici.com/login';
    } else if (host.indexOf('lemici.com') !== -1) {
        window.location.href = 'https://www.lemici.com/login';
    } else if (host === 'localhost' || host === '127.0.0.1' || host.indexOf('192.168.') === 0) {
        window.location.href = 'http://localhost:3000/login';
    } else {
        window.location.href = '${url.loginUrl}';
    }
}
```

**`info.ftl` also has a second duplicated function `handleReturnToApp()`** (lines 23–34) using the same host-detection pattern but routing to `/` instead of `/login`.

**Refactor plan:** Move both `handleReturnToLogin()` and `handleReturnToApp()` into `template.ftl` as a global `<script>` block so all child templates inherit them.

### 2.2 Password Visibility Toggle — Duplicated 5 Times

The eye-toggle button HTML wrapper (including the eye-off and eye-on SVG icons) is copy-pasted in every password field:

| File                        | Field               | Lines |
| --------------------------- | ------------------- | ----- |
| `login.ftl`                 | `#password`         | 70–73 |
| `register.ftl`              | `#password`         | 56–59 |
| `register.ftl`              | `#password-confirm` | 72–75 |
| `login-update-password.ftl` | `#password-new`     | 23–26 |
| `login-update-password.ftl` | `#password-confirm` | 35–38 |

**Identical HTML block (all five):**
```html
<button type="button" class="eye-toggle" onclick="togglePassword('FIELD_ID', this)" tabindex="-1" aria-label="Toggle password visibility">
    <svg class="eye-icon eye-off" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/>
        <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/>
        <line x1="1" y1="1" x2="23" y2="23"/>
    </svg>
    <svg class="eye-icon eye-on" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="display:none">
        <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/>
        <circle cx="12" cy="12" r="3"/>
    </svg>
</button>
```

The `togglePassword()` function itself is already defined once in `template.ftl` (lines 306–319), so only the HTML wrapper needs extraction.

**Refactor plan:** Create a FreeMarker macro `#macro passwordToggle(inputId)` in `template.ftl`.

### 2.3 Social Login Google Button — Duplicated 2 Times

The Google OAuth social login block (divider + button with full SVG logo) is copy-pasted in:

| File           | Lines   |
| -------------- | ------- |
| `login.ftl`    | 89–104  |
| `register.ftl` | 194–210 |

**Identical block (both):**
```ftl
<#if social.providers?? && social.providers?has_content>
<#list social.providers as p><#if p.alias == "google">
<div class="divider">
    <hr><span>OR</span><hr>
</div>
<div id="kc-social-providers">
    <ul>
        <li>
            <a href="${p.loginUrl}" class="social-btn google" title="Continue with Google">
                <svg viewBox="0 0 24 24" width="20" height="20"><!-- full Google logo paths --></svg>
                <span>Continue with Google</span>
            </a>
        </li>
    </ul>
</div>
</#if></#list>
</#if>
```

**Refactor plan:** Create a FreeMarker macro `#macro socialLoginButtons()` in `template.ftl`.

### 2.4 Password Field Wrapper with Lock SVG — Duplicated 5 Times

The entire password `<div class="form-group">` block including the lock icon SVG, input, eye toggle, and password strength/match indicators is repeated. The lock SVG (`M18 8h-1V6c0-2.76...`) appears in:

| File                        | Field               | Lines |
| --------------------------- | ------------------- | ----- |
| `login.ftl`                 | `#password`         | 66–77 |
| `register.ftl`              | `#password`         | 51–65 |
| `register.ftl`              | `#password-confirm` | 67–78 |
| `login-update-password.ftl` | `#password-new`     | 19–28 |
| `login-update-password.ftl` | `#password-confirm` | 30–40 |

**Refactor plan:** Create `#macro passwordField(inputId, label, placeholder, showStrength=false, showMatch=false)` in `template.ftl`.

### 2.5 Input Wrapper with Person SVG — Duplicated 4 Times

The person/user icon SVG (`M12 12c2.21 0 4-1.79...`) with `<div class="input-wrapper">` is repeated for every name/username field:

| File           | Field        | Lines |
| -------------- | ------------ | ----- |
| `login.ftl`    | `#username`  | 59–62 |
| `register.ftl` | `#username`  | 15–18 |
| `register.ftl` | `#firstName` | 25–28 |
| `register.ftl` | `#lastName`  | 34–37 |

**Refactor plan:** Create `#macro inputField(inputId, label, type, placeholder, iconSvg, value)` in `template.ftl`.

### 2.6 Button Loading Spinner SVG — Duplicated 5 Times

The loading spinner `<span class="btn-spinner">` with the animated circle SVG is repeated in every submit button:

| File                        | Lines  |
| --------------------------- | ------ |
| `login.ftl`                 | 82     |
| `register.ftl`              | 91     |
| `login-reset-password.ftl`  | 25     |
| `login-update-password.ftl` | 54, 63 |

**Refactor plan:** Create `#macro submitButton(text, id='kc-login')` in `template.ftl`.

### 2.7 Structural Refactoring Game Plan

**Target:** Move all shared components into `template.ftl` as reusable FreeMarker macros.

```ftl
<#-- === REUSABLE MACROS TO ADD TO template.ftl === -->

<#-- 1. Global navigation functions (replaces 3x handleReturnToLogin duplication) -->
<#macro globalNavScripts>
<script>
    function handleReturnToLogin() { /* ... */ }
    function handleReturnToApp() { /* ... */ }
</script>
</#macro>

<#-- 2. Password visibility toggle button -->
<#macro passwordToggle inputId>
<button type="button" class="eye-toggle" onclick="togglePassword('${inputId}', this)" tabindex="-1" aria-label="Toggle password visibility">
    <svg class="eye-icon eye-off" ...>...</svg>
    <svg class="eye-icon eye-on" ... style="display:none">...</svg>
</button>
</#macro>

<#-- 3. Complete password field with optional strength/match -->
<#macro passwordField inputId label placeholder showStrength=false showMatch=false>
<div class="form-group">
    <label for="${inputId}">${label}</label>
    <div class="input-wrapper password-wrapper">
        <svg fill="currentColor" viewBox="0 0 24 24"><path d="M18 8h-1V6c0-2.76..."/></svg>
        <input type="password" id="${inputId}" class="pf-c-form-control" name="${inputId}" placeholder="${placeholder}" autocomplete="new-password" />
        <@passwordToggle inputId=inputId />
    </div>
    <#if showStrength>
    <div class="password-strength" id="password-strength">
        <div class="strength-bar"><div class="strength-fill" id="strength-fill"></div></div>
        <span class="strength-label" id="strength-label"></span>
    </div>
    </#if>
    <#if showMatch>
    <div class="password-match" id="password-match"></div>
    </#if>
</div>
</#macro>

<#-- 4. Generic input field with left icon -->
<#macro inputField inputId label type placeholder iconSvg value="" autocomplete="">
<div class="form-group">
    <label for="${inputId}">${label}</label>
    <div class="input-wrapper">
        ${iconSvg?raw}
        <input type="${type}" id="${inputId}" class="pf-c-form-control" name="${inputId}" value="${value}" placeholder="${placeholder}" <#if autocomplete?has_content>autocomplete="${autocomplete}"</#if> />
    </div>
</div>
</#macro>

<#-- 5. Social login buttons (Google) -->
<#macro socialLoginButtons>
<#if social.providers?? && social.providers?has_content>
<#list social.providers as p><#if p.alias == "google">
<div class="divider"><hr><span>OR</span><hr></div>
<div id="kc-social-providers">
    <ul><li>
        <a href="${p.loginUrl}" class="social-btn google" title="Continue with Google">
            <svg viewBox="0 0 24 24" width="20" height="20">...</svg>
            <span>Continue with Google</span>
        </a>
    </li></ul>
</div>
</#if></#list>
</#if>
</#macro>

<#-- 6. Submit button with loading spinner -->
<#macro submitButton text buttonId="kc-login" disabled=true>
<button class="pf-c-button pf-m-primary" type="submit" id="${buttonId}" <#if disabled>disabled</#if>>
    <span class="btn-text">${text}</span>
    <span class="btn-spinner"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10" stroke-dasharray="31.42" stroke-dashoffset="10"><animateTransform attributeName="transform" type="rotate" from="0 12 12" to="360 12 12" dur="0.8s" repeatCount="indefinite"/></circle></svg></span>
</button>
</#macro>
```

**Impact after refactoring:**

| File                        | Current Lines | Estimated After | Reduction          |
| --------------------------- | ------------- | --------------- | ------------------ |
| `login.ftl`                 | 130           | ~55             | ~58%               |
| `register.ftl`              | 217           | ~70             | ~68%               |
| `login-update-password.ftl` | 89            | ~35             | ~61%               |
| `login-reset-password.ftl`  | 149           | ~95             | ~36%               |
| `error.ftl`                 | 49            | ~25             | ~49%               |
| `info.ftl`                  | 146           | ~100            | ~32%               |
| `login-verify-email.ftl`    | 80            | ~40             | ~50%               |
| `template.ftl`              | 322           | ~420            | +98 (macros added) |

**Net code reduction: ~250 lines across the theme.**

---

## 3. BFF Cross-Browser State Fix Plan

### Problem Statement

When a user clicks a verification/link action token URL in a **different browser or device** than the one that initiated the flow, Keycloak fails because the `AUTH_SESSION_ID` cookie (which is browser/device-scoped) is missing or mismatched. This causes Keycloak to render `error.ftl` with a generic error message.

**Current behavior:** `error.ftl` shows a static "link no longer valid" warning — even when the verification actually succeeded server-side but the cross-device session is invalid. The user sees an error instead of confirmation.

### 3.1 Updated `error.ftl` Layout Plan

**Goal:** Detect action token panics (invalid/missing `AUTH_SESSION_ID`) and convert them into a friendly "Email Verified Successfully" dashboard that routes back to the Go BFF `get-started` flow.

```ftl
<#import "template.ftl" as layout>
<@layout.registrationLayout displayMessage=true; section>
    <#if section = "header">
        <div class="login-header">
            <h1>Email Verification</h1>
            <p>This link may have expired or already been used.</p>
        </div>
    <#elseif section = "form">
        <div id="kc-error-message" style="text-align: center; padding: 20px 0;">

            <#-- ============================================================ -->
            <#-- PHASE 1: Detect if this is a cross-device AUTH_SESSION_ID     -->
            <#-- panic vs a genuine expired/used token error.                 -->
            <#--                                                             -->
            <#-- Keycloak sets: message.summary, message.type,                -->
            <#-- error?has_content, and actionMsg (if present).              -->
            <#--                                                             -->
            <#-- Cross-device panics typically produce messages containing:   -->
            <#-- "invalid token", "missing code", "expired", "session",     -->
            <#-- or the page is rendered with action token params in URL.    -->
            <#-- ============================================================ -->

            <#assign isCrossDevicePanic = false />
            <#assign isExpiredLink = false />

            <#-- Detect cross-device panic patterns -->
            <#if message?has_content>
                <#assign summaryLower = message.summary?lower_case />
                <#if summaryLower?contains("invalid") && summaryLower?contains("token")>
                    <#assign isCrossDevicePanic = true />
                <#elseif summaryLower?contains("missing") && summaryLower?contains("code")>
                    <#assign isCrossDevicePanic = true />
                <#elseif summaryLower?contains("session") && summaryLower?contains("not")>
                    <#assign isCrossDevicePanic = true />
                <#elseif summaryLower?contains("could not") && summaryLower?contains("process")>
                    <#assign isCrossDevicePanic = true />
                </#if>

                <#-- Detect genuine expired/used link -->
                <#if summaryLower?contains("expired") || summaryLower?contains("already been used") || summaryLower?contains("no longer valid")>
                    <#assign isExpiredLink = true />
                </#if>
            </#if>

            <#-- Also check URL params for action token evidence -->
            <#if request.getParameter("error")?? || request.getParameter("code")??>
                <#assign isCrossDevicePanic = true />
            </#if>

            <#-- ============================================================ -->
            <#-- PHASE 2: Branch rendering based on detection                -->
            <#-- ============================================================ -->

            <#if isCrossDevicePanic>
                <#-- *** CROSS-DEVICE PANIC → Friendly Success Dashboard *** -->

                <#-- Green success icon -->
                <div style="width: 64px; height: 64px; background: rgba(22, 163, 74, 0.1); color: #16a34a; border-radius: 50%; display: flex; align-items: center; justify-content: center; margin: 0 auto 24px auto;">
                    <svg fill="none" viewBox="0 0 24 24" stroke-width="2.5" stroke="currentColor" style="width: 32px; height: 32px;">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                    </svg>
                </div>

                <p style="font-size: 18px; font-weight: 700; color: #16a34a; margin-bottom: 8px;">
                    Email Verified Successfully
                </p>
                <p style="font-size: 14px; color: #6b7280; margin-bottom: 32px; line-height: 1.6;">
                    Your email has been verified. You can now sign in to access your LeMiCi account.
                </p>

                <#-- Route back to Go BFF get-started flow -->
                <div style="margin-bottom: 16px;">
                    <a href="#" onclick="event.preventDefault(); handleReturnToBffGetStarted();"
                       class="pf-c-button pf-m-primary"
                       style="text-decoration: none; display: inline-block; padding: 12px 36px; background: #6D3E93; color: white; border-radius: 8px; font-weight: 600; font-size: 16px; box-shadow: 0 4px 10px rgba(109, 62, 147, 0.25);">
                        Get Started
                    </a>
                </div>

                <p style="font-size: 13px; color: #9ca3af; line-height: 1.5; margin-top: 16px;">
                    Redirecting to sign in automatically in <span id="countdown-sec">5</span> seconds...
                </p>

                <script>
                    // Set localStorage flag so info.ftl/login-verify-email.ftl can detect
                    try { localStorage.setItem('email_verified', 'true'); } catch(e) {}

                    function handleReturnToBffGetStarted() {
                        var host = window.location.hostname;
                        if (host.indexOf('dev') !== -1 && host.indexOf('lemici.com') !== -1) {
                            window.location.href = 'https://dev.lemici.com/get-started';
                        } else if (host.indexOf('lemici.com') !== -1) {
                            window.location.href = 'https://www.lemici.com/get-started';
                        } else if (host === 'localhost' || host === '127.0.0.1' || host.indexOf('192.168.') === 0) {
                            window.location.href = 'http://localhost:3000/get-started';
                        } else {
                            window.location.href = '/';
                        }
                    }

                    // Auto-redirect countdown
                    (function() {
                        var sec = 5;
                        var timer = setInterval(function() {
                            sec--;
                            var el = document.getElementById('countdown-sec');
                            if (el) el.innerText = sec;
                            if (sec <= 0) {
                                clearInterval(timer);
                                handleReturnToBffGetStarted();
                            }
                        }, 1000);
                    })();
                </script>

            <#else>
                <#-- *** GENUINE EXPIRED/USED LINK → Original Error UI *** -->

                <div style="width: 64px; height: 64px; background: rgba(217, 119, 6, 0.1); color: #d97706; border-radius: 50%; display: flex; align-items: center; justify-content: center; margin: 0 auto 24px auto;">
                    <svg fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" style="width: 32px; height: 32px;">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                    </svg>
                </div>

                <p style="font-size: 15px; color: #374151; margin-bottom: 8px; line-height: 1.6;">
                    The verification link you clicked is no longer valid.
                </p>
                <p style="font-size: 14px; color: #6b7280; margin-bottom: 32px; line-height: 1.6;">
                    This can happen if the link expired, was already used, or was opened on a different device or browser.
                </p>

                <div style="margin-bottom: 16px;">
                    <a href="#" onclick="event.preventDefault(); handleReturnToLogin();"
                       class="pf-c-button pf-m-primary"
                       style="text-decoration: none; display: inline-block; padding: 12px 36px; background: #6D3E93; color: white; border-radius: 8px; font-weight: 600; font-size: 16px; box-shadow: 0 4px 10px rgba(109, 62, 147, 0.25);">
                        Sign In
                    </a>
                </div>

                <p style="font-size: 13px; color: #9ca3af; line-height: 1.5; margin-top: 16px;">
                    Sign in with your email and password — a new verification email will be sent if needed.
                </p>
            </#if>

        </div>
    </#if>
</@layout.registrationLayout>
```

### 3.2 Auto-Reload Tracking Script in `login-verify-email.ftl`

**Where it belongs:** At the bottom of the `login-verify-email.ftl` form section, after the existing `handleReturnToLogin()` function definition.

**What it should do:** In addition to the current `localStorage` polling for `email_verified`, it should also:
1. Set a page-level tracking flag (`lemici_verify_page_active`) in `localStorage` so other tabs know this page is waiting
2. Listen for a new key `email_verified_by_action_token` (set by the updated `error.ftl` cross-device panic handler)
3. On detection, clear tracking and redirect to BFF `get-started`

```ftl
<#-- Add this script block at the bottom of login-verify-email.ftl's form section -->
<script>
    (function() {
        // Mark this verification page as active for cross-tab coordination
        try {
            localStorage.setItem('lemici_verify_page_active', 'true');
        } catch(e) {}

        // Clear stale flags
        localStorage.removeItem('email_verified');

        function handleEmailVerified() {
            try {
                localStorage.removeItem('email_verified');
                localStorage.removeItem('lemici_verify_page_active');
            } catch(e) {}
            // Route to BFF get-started on successful verification
            var host = window.location.hostname;
            if (host.indexOf('dev') !== -1 && host.indexOf('lemici.com') !== -1) {
                window.location.href = 'https://dev.lemici.com/get-started';
            } else if (host.indexOf('lemici.com') !== -1) {
                window.location.href = 'https://www.lemici.com/get-started';
            } else if (host === 'localhost' || host === '127.0.0.1' || host.indexOf('192.168.') === 0) {
                window.location.href = 'http://localhost:3000/get-started';
            } else {
                window.location.href = '/';
            }
        }

        // Cross-tab listener: info.ftl sets 'email_verified' on success
        window.addEventListener('storage', function(e) {
            if (e.key === 'email_verified' && e.newValue === 'true') {
                handleEmailVerified();
            }
            // Cross-device panic handler: error.ftl sets this on cross-device verification
            if (e.key === 'email_verified_by_action_token' && e.newValue === 'true') {
                handleEmailVerified();
            }
        });

        // Fallback polling (covers browsers that block storage events)
        var checkTimer = setInterval(function() {
            if (localStorage.getItem('email_verified') === 'true' ||
                localStorage.getItem('email_verified_by_action_token') === 'true') {
                clearInterval(checkTimer);
                handleEmailVerified();
            }
        }, 1000);

        // Cleanup on page unload
        window.addEventListener('beforeunload', function() {
            try {
                localStorage.removeItem('lemici_verify_page_active');
            } catch(e) {}
        });
    })();
</script>
```

### 3.3 Cross-Device Verification Flow Diagram

```
User A (Desktop Chrome)          User B (Mobile Safari)
        │                                │
        ├─ register.ftl                  │
        │   └─ Email sent                │
        │                                │
        │   login-verify-email.ftl       │
        │   (showing "check your email") │
        │   Polling localStorage...      │
        │                                ├─ Opens email link
        │                                │   on mobile Safari
        │                                │
        │                                ├─ Keycloak: action token
        │                                │   processed ✓
        │                                │
        │                                ├─ AUTH_SESSION_ID missing
        │                                │   on mobile (cross-device)
        │                                │
        │                                ├─ error.ftl renders
        │                                │   isCrossDevicePanic = true
        │                                │
        │                                ├─ Sets localStorage:
        │                                │   'email_verified_by_action_token' = 'true'
        │                                │
        │   detectStorageEvent() ◄───────┤
        │                                │
        ├─ handleEmailVerified()         │
        │   └─ Redirect to               │
        │      /get-started              │
        │                                │
        │   Shows: "Email Verified       │
        │    Successfully" dashboard     │
        │                                │
```

---

## 4. Complete File Manifest

### Login Theme (`keycloak-theme/lemici/login/`)

| File                        | Path                                    | Operational Role                                                                                                                                                                                                                                                                                                                 |
| --------------------------- | --------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `theme.properties`          | `login/theme.properties`                | Declares `parent=keycloak` inheritance and imports `common/keycloak` resources, establishing the base Keycloak login theme as the foundation for all custom overrides.                                                                                                                                                           |
| `template.ftl`              | `login/template.ftl`                    | Master layout macro (`registrationLayout`) that defines the split-screen HTML shell (form side + image side with testimonial), all CSS styles, alert rendering, the `togglePassword()` JS function, and nested slot points (`header`, `form`, `info`) consumed by all child templates.                                           |
| `login.ftl`                 | `login/login.ftl`                       | Primary authentication page with username/password form, session-timeout banner (reads `session_timeout` cookie), Google social login, client-side form validation (disables submit until both fields populated), and "Create an account" link to registration.                                                                  |
| `register.ftl`              | `login/register.ftl`                    | User registration form with conditional username field (respects `realm.registrationEmailAsUsername`), first/last name, email, password with strength meter + match indicator, Terms of Service/Privacy Policy checkbox gate, Google social login, and client-side validation.                                                   |
| `error.ftl`                 | `login/error.ftl`                       | Error page displayed when action token processing fails (expired/used verification links, cross-device `AUTH_SESSION_ID` panics). Shows warning icon with contextual message and "Sign In" button routing via `handleReturnToLogin()`.                                                                                           |
| `info.ftl`                  | `login/info.ftl`                        | Multi-purpose info page that dynamically renders success/warning/error/info icons based on `message.type`. Handles three branches: email verified success (sets `email_verified` localStorage), email sent confirmation (listens for `password_reset_success`), and default auto-redirect countdown with cross-tab storage sync. |
| `login-reset-password.ftl`  | `login/login-reset-password.ftl`        | Forgot-password form that pre-validates email existence via `/api/v1/auth/check-email` fetch before submitting to Keycloak, with client-side rate limiting (3 failed attempts → redirect to registration, tracked in `sessionStorage`).                                                                                          |
| `login-update-password.ftl` | `login/login-update-password.ftl`       | Password reset form with hidden username/password fields for browser autofill compatibility, new/confirm password inputs with eye toggles, "logout from other devices" checkbox, and `isAppInitiatedAction` conditional for Cancel button display.                                                                               |
| `login-verify-email.ftl`    | `login/login-verify-email.ftl`          | Post-registration verification page showing the email address the verification was sent to, "Sign In" and "Resend Verification Email" buttons, and cross-tab `localStorage` polling that detects when `info.ftl` sets the `email_verified` flag from another tab.                                                                |
| `messages_en.properties`    | `login/messages/messages_en.properties` | English i18n message overrides for all Keycloak error/status messages, sanitizing user-facing text for invalid credentials, account status, password validation, social auth failures, and session timeout states.                                                                                                               |
| `style.css`                 | `login/resources/css/style.css`         | Standalone CSS file (currently **not loaded** by `template.ftl` which uses inline `<style>`) containing equivalent styles with CSS custom properties (`--primary-color` etc.), Font Awesome imports, and slightly different spacing/typography values — appears to be an earlier or alternate version of the inline styles.      |
| `cube.png`                  | `login/resources/img/cube.png`          | Favicon and brand icon loaded via `<link>` tags in `template.ftl` with cache-busting `?v=2` parameter.                                                                                                                                                                                                                           |
| `signin-bg.jpg`             | `login/resources/img/signin-bg.jpg`     | Background image for the right-side testimonial panel in the split-screen layout, loaded via CSS `background` property in `template.ftl`.                                                                                                                                                                                        |
| `privacy.html`              | `login/resources/pages/privacy.html`    | Standalone Privacy Policy page (last updated May 16, 2026) linked from the registration form's terms checkbox, covering data collection, usage, security, third-party services, and user data rights for LeMiCi.                                                                                                                 |
| `terms.html`                | `login/resources/pages/terms.html`      | Standalone Terms of Service page (last updated May 16, 2026) linked from the registration form's terms checkbox, covering acceptable use, intellectual property, liability limitations, and account responsibilities for LeMiCi.                                                                                                 |

### Email Theme (`keycloak-theme/lemici/email/`)

| File                            | Path                                    | Operational Role                                                                                                                                                                                                                                                                                                                                 |
| ------------------------------- | --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `theme.properties`              | `email/theme.properties`                | Declares `parent=base` inheritance, establishing the minimal Keycloak base email theme as foundation for the custom HTML/text email templates.                                                                                                                                                                                                   |
| `email-verification.ftl` (HTML) | `email/html/email-verification.ftl`     | Rich HTML email template for new-user verification emails, rendering a branded card with LeMiCi logo, personalized greeting (`${user.firstName}`), CTA button linking to `${link}`, expiration notice (`${linkExpirationInMinutes}`), and fallback plain-text URL — styled with Inter font, purple `#6D3E93` CTA, and responsive email-safe CSS. |
| `email-verification.ftl` (text) | `email/text/email-verification.ftl`     | Plain-text fallback for email clients that don't render HTML, containing the same verification link, expiration notice, and LeMiCi branding in minimal text format.                                                                                                                                                                              |
| `messages_en.properties`        | `email/messages/messages_en.properties` | English i18n override for the email subject line: "Welcome to Lemici! Verify your email".                                                                                                                                                                                                                                                        |

### Notable Observations

1. **`style.css` is orphaned** — The file at `login/resources/css/style.css` defines equivalent styles to the inline CSS in `template.ftl` but is never referenced via `<link>` in any template. It appears to be a legacy/development artifact that should either be integrated (replacing inline styles) or removed.

2. **`handleReturnToApp()` exists only in `info.ftl`** — This function routes to the app root (`/`) rather than `/login`, and is not duplicated elsewhere. It should also be moved to `template.ftl` for consistency.

3. **Hardcoded environment URLs** — The `handleReturnToLogin()` function hardcodes `dev.lemici.com`, `www.lemici.com`, and `localhost:3000`. These should ideally be extracted to FreeMarker variables or a configuration block in `template.ftl` for easier environment management.

4. **Google-only social login** — The social provider block filters specifically for `p.alias == "google"`. If additional providers (LinkedIn, GitHub) are added later, the macro refactoring should accept a provider list parameter.

5. **Email template uses hardcoded logo URL** — `email/html/email-verification.ftl:136` references `https://dev.lemici.com/abhinay/cube.png` which is environment-specific and should use a configurable base URL variable.
