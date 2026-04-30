# Frontend Guide: Logout and Logout-All APIs

This document explains how to use the logout and logout-all APIs from the frontend (e.g., a SPA or mobile web app).

## Overview

The API provides two endpoints for ending user sessions:

1. **Logout** (`POST /api/v1/oauth/logout`): Ends the current session (the one associated with the cookie in the request).
2. **Logout-All** (`POST /api/v1/oauth/logout-all`): Ends all sessions for the authenticated user (across all devices/browsers).

Both endpoints are **public** (no authentication token or session required in the request body or headers beyond the cookie). They rely on the presence of the `AUTH_SESSION_ID` cookie to identify the user.

## Important Notes

- **Cookie-Based**: The frontend must ensure that the `AUTH_SESSION_ID` cookie (HttpOnly, Secure, SameSite=Lax) is included in the request. This is handled automatically by the browser for same-origin requests or when the cookie domain matches.
- **Idempotent**: Calling these endpoints when no active session exists is safe and returns a success response.
- **Redirects Not Required**: These endpoints return JSON responses. The frontend should handle navigation (e.g., redirect to login page) based on the response or application state.
- **CSRF**: These endpoints are protected by CSRF middleware. Ensure your frontend includes the CSRF token in the request header (typically `X-CSRF-Token`) as required by your application's CSRF policy.

---

## 1. Logout (Single Session)

### Endpoint
```
POST /api/v1/oauth/logout
```

### Description
Invalidates the session associated with the `AUTH_SESSION_ID` cookie in the request (if any) and clears the cookie.

### Request
- **Method**: POST
- **Headers**:
  - `Content-Type`: application/json (though no body is required, some middleware may expect it)
  - `Cookie`: `AUTH_SESSION_ID=<session_id>` (automatically included by the browser if the cookie is set for the domain)
  - `X-CSRF-Token`: <csrf_token> (required if CSRF protection is enabled)
- **Body**: None (empty body is acceptable)

### Response
- **Success (200 OK)**:
  ```json
  {
    "status": "logged_out"
  }
  ```
- **Note**: Even if no session is found (cookie missing, expired, or invalid), the endpoint returns 200 OK with the same response. This makes it safe to call unconditionally.

### Example (using Fetch API)
```javascript
async function logout() {
  try {
    const response = await fetch('/api/v1/oauth/logout', {
      method: 'POST',
      credentials: 'include', // Important: sends cookies with the request
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': getCSRFToken() // Implement this function to retrieve your CSRF token
      }
    });

    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }

    const data = await response.json();
    console.log('Logout successful:', data);

    // Clear any local user state (e.g., Redux, React context, localStorage)
    clearUserState();

    // Redirect to login or homepage
    window.location.href = '/login';
  } catch (error) {
    console.error('Logout failed:', error);
    // Optionally, still redirect to login on error to prevent stale UI state
    window.location.href = '/login';
  }
}
```

---

## 2. Logout-All (All Sessions)

### Endpoint
```
POST /api/v1/oauth/logout-all
```

### Description
Invalidates **all** sessions associated with the user (identified by the `AUTH_SESSION_ID` cookie) and clears the cookie in the response.

### Request
- **Method**: POST
- **Headers**:
  - `Content-Type`: application/json
  - `Cookie`: `AUTH_SESSION_ID=<session_id>` (automatically included by the browser)
  - `X-CSRF-Token`: <csrf_token> (required if CSRF protection is enabled)
- **Body**: None

### Response
- **Success (200 OK)**:
  ```json
  {
    "status": "logged_out_all"
  }
  ```
- **Error (500 Internal Server Error)**:
  ```json
  {
    "error": "failed_to_logout_all_sessions"
  }
  ```
- **Note**: If no session cookie is present, the endpoint returns 200 OK with `{"status": "logged_out_all"}` (nothing to do).

### Example (using Fetch API)
```javascript
async function logoutAll() {
  try {
    const response = await fetch('/api/v1/oauth/logout-all', {
      method: 'POST',
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': getCSRFToken()
      }
    });

    if (!response.ok) {
      // Handle non-2xx responses
      const errorData = await response.json();
      throw new Error(errorData.error || `HTTP error! status: ${response.status}`);
    }

    const data = await response.json();
    console.log('Logout-all successful:', data);

    // Clear any local user state
    clearUserState();

    // Redirect to login or homepage
    window.location.href = '/login';
  } catch (error) {
    console.error('Logout-all failed:', error);
    // Optionally redirect to login on error
    window.location.href = '/login';
  }
}
```

---

## Cookie Requirements

For these endpoints to work, ensure your frontend application:

1. **Sets the cookie with correct attributes** (handled by the backend on login):
   - Name: `AUTH_SESSION_ID`
   - Path: `/`
   - Domain: (empty string for host-only, or explicitly set to your domain)
   - Secure: `true` (only sent over HTTPS)
   - HttpOnly: `true` (not accessible via JavaScript)
   - SameSite: `Lax` (or `Strict`; adjust based on your cross-site needs)

2. **Sends the cookie with requests**:
   - Use `credentials: 'include'` in `fetch` or `withCredentials: true` in `XMLHttpRequest`.
   - For same-origin requests (frontend and backend on the same domain/subdomain), this is typically sufficient.
   - If your frontend and backend are on different subdomains (e.g., `app.example.com` and `api.example.com`), ensure the cookie domain is set to `.example.com` (note the leading dot) and that your frontend respects this.

---

## Backend Behavior (For Reference)

- **Logout**: Deletes the single session matching the cookie (if valid) and clears the cookie.
- **Logout-All**: 
  1. Reads the session ID from the cookie.
  2. Looks up the user ID associated with that session.
  3. Deletes **all** session keys and the user-to-sessions mapping (`user_sessions:<userID>`) for that user.
  4. Clears the cookie.
- Both endpoints clear the cookie in the response (by setting `Max-Age=-1`).

---

## Testing

1. **Normal Logout**:
   - Log in to your application.
   - Call `/api/v1/oauth/logout`.
   - Verify the user is logged out (e.g., redirected to login, subsequent API calls return 401).
   - Check that the `AUTH_SESSION_ID` cookie is removed.

2. **Logout-All**:
   - Log in from two different browsers/devices (or simulate by having two sessions).
   - Call `/api/v1/oauth/logout-all` from one of them.
   - Verify that **both** sessions are invalidated (both devices/browsers are logged out).

3. **Edge Cases**:
   - Call logout when no session exists (should still return 200).
   - Call logout-all when no session exists (should still return 200).
   - Ensure CSRF protection is working (missing/invalid token returns 403).

---

## Integration with Session Check Endpoint

Consider implementing a `/api/v1/oauth/profile` or `/api/v1/oauth/session` endpoint (GET) that returns the current user's session data if authenticated, or 401 if not. This can be used on app load to initialize the user state without relying on logout endpoints.

Example:
```javascript
async function checkAuth() {
  try {
    const res = await fetch('/api/v1/oauth/profile', { credentials: 'include' });
    if (res.ok) {
      const userData = await res.json();
      setCurrentUser(userData.user);
    } else {
      setCurrentUser(null);
    }
  } catch (err) {
    setCurrentUser(null);
  }
}
```
