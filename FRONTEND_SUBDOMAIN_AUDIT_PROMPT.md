# Frontend Subdomain-Based Fix Audit Prompt

## Objective
This prompt is designed for an LLM to audit the frontend codebase to identify and verify the implementation of subdomain-based fixes for cross-site cookie persistence issues between:
- Frontend: dev.lemici.com
- Backend API: us-dev-api.lemici.com

## Required Verifications

### 1. Asset Serving Domain Verification
Check that the frontend is configured to serve from `dev.lemici.com` rather than CloudFront URLs like `d595hydlunw5u.cloudfront.net` or `d3c34598mt7qdx.cloudfront.net`.

**Files to check:**
- All configuration files (.env, .env.*, vite.config.*, config.*)
- Build/deployment scripts
- Dockerfiles or container configurations
- CDN or static hosting configurations

**Specific checks:**
- No hardcoded references to `cloudfront.net` domains
- Base URL or asset hosting configured for `dev.lemici.com`
- Service worker configurations (if applicable) use correct scope

### 2. API Base URL Configuration
Verify that API requests are directed to `https://us-dev-api.lemici.com` rather than CloudFront or localhost references in production/build configurations.

**Files to check:**
- Environment variable files (.env, .env.*, .env.*)
- API service/configuration files (src/api/*, src/services/*)
- Vite/Webpack configuration files
- Constants or utility files containing API endpoints

**Specific checks:**
- `VITE_API_URL` or similar environment variable set to `https://us-dev-api.lemici.com`
- No hardcoded API URLs pointing to:
  - `localhost:8080` (in production builds)
  - `d595hydlunw5u.cloudfront.net`
  - `d3c34598mt7qdx.cloudfront.net`
  - `www.lemici.com` (for API endpoints)
- Proxy configurations (in vite.config.ts) properly forward `/api` or `/operate` paths to `https://us-dev-api.lemici.com`

### 3. Authentication Flow Verification
Check that authentication flows properly handle the subdomain redirect pattern:
1. Frontend (dev.lemici.com) → Backend API (us-dev-api.lemici.com/auth)
2. Backend API → Keycloak (us-dev-api.lemici.com)
3. Keycloak → Backend API callback (us-dev-api.lemici.com/api/v1/auth/callback)
4. Backend API → Frontend redirect (dev.lemici.com/dashboard or similar)

**Files to check:**
- Authentication service files
- Route guards or authentication wrappers
- Callback/redirect handling components
- Login/logout service implementations

**Specific checks:**
- Login redirects go to `https://us-dev-api.lemici.com/api/v1/auth/callback` (handled by backend)
- Post-login redirects from backend go to `https://dev.lemici.com/dashboard`
- Post-logout redirects from backend go to `https://dev.lemici.com/`
- No redirects to CloudFront domains
- No hardcoded localhost redirects in production code

### 4. Cookie-Related Configuration
While cookie settings are primarily backend-controlled, verify frontend doesn't override or conflict with intended subdomain cookie behavior.

**Files to check:**
- HTTP client/interceptors
- Authentication libraries configuration
- State management related to auth tokens

**Specific checks:**
- No manual cookie manipulation that would interfere with `.lemici.com` domain cookies
- Credentials mode set correctly for cross-subdomain requests (typically `include` for cookies)
- No domain overrides in fetch/XMLHttpRequest configurations

## Expected Output Format

The LLM should provide findings in this format:

```
## FRONTEND SUBDOMAIN FIX AUDIT RESULTS

### ✅ PASSING CHECKS
- [List of checks that are correctly implemented]

### ⚠️ WARNINGS
- [Minor issues or areas needing attention]

### ❌ FAILING CHECKS
- [Specific files and lines requiring fixes]
  - FILE:PATH:LINE_NUMBER: DESCRIPTION_OF_ISSUE
  - Example: src/api/authService.ts:42: Hardcoded API URL should use environment variable

### 📝 RECOMMENDATIONS
- [Actionable steps to fix failing checks]
```

## Context Information
- Backend API is correctly configured at: `https://us-dev-api.lemici.com`
- Frontend should be served from: `https://dev.lemici.com`
- Authentication redirects flow through: `https://us-dev-api.lemici.com/api/v1/auth/callback`
- Post-authentication redirects should go to: `https://dev.lemici.com/` (or `/dashboard`)
- Session cookies are configured with Domain: `.lemici.com` and SameSite: `Lax` in backend
- Keycloak client (lemici-frontend) already has correct redirect URIs configured

## Implementation Notes
Focus on finding:
1. Hardcoded URLs that should be environment variables
2. Environment variables with incorrect values
3. Build configurations that point to wrong domains
4. Service workers or caching layers with incorrect scopes
5. Authentication service implementations with wrong redirect handling