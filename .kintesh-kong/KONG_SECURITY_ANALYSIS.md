# Kong API Gateway - Expert Security Analysis Report

**Date:** May 4, 2026  
**Reviewer:** Kintesh
**Status:** Alpha - Pre Frontend Integration  
**System:** Workflow-and-Workers with Camunda 8

---

## Executive Summary

This Kong implementation demonstrates a solid foundational architecture with proper separation of concerns, FQDN-based routing, IP allowlisting for internal APIs, and central CORS management. However, there are **critical architectural conflicts** that must be resolved before frontend integration.

---

## Part 1: What's Implemented Correctly

### 1.1 Kong Declarative Configuration ✅

| Aspect                       | Implementation           | Status            |
| ---------------------------- | ------------------------ | ----------------- |
| Declarative config (DB-less) | `KONG_DATABASE: "off"`   | ✅ Correct        |
| Format version               | `_format_version: "3.0"` | ✅ Current        |
| Plugin architecture          | All core plugins enabled | ✅ Good           |
| Kong version                 | `kong:3.6`               | ✅ Stable release |

### 1.2 Route Architecture ✅

```yaml
# BLOCK ADMIN ON PUBLIC FQDN - Correctly Implemented
- name: block-admin-public
  hosts: [api.mvp.lemici.com, api.lemici.com]
  paths: [/api/admin]
  plugins: [request-termination] # Returns 404
```

**What works:**

- Public API routes with multiple hostnames
- Health checks on dedicated paths
- Internal admin routes with IP restriction
- FQDN-based routing strategy

### 1.3 IP Allowlisting for Internal Routes ✅

```yaml
plugins:
  - name: ip-restriction
    config:
      allow:
        - "127.0.0.1/32" # Localhost
        - "10.0.0.0/8" # AWS VPC Class A
        - "172.16.0.0/12" # Standard private
        - "172.31.0.0/16" # AWS default VPC
```

**Correctly covers:**

- Local development (127.0.0.1)
- AWS VPC ranges (10.x, 172.16-31.x)

### 1.4 Rate Limiting ✅

```yaml
# Service-level rate limiting (100/min)
- name: rate-limiting
  config:
    minute: 100
    policy: local
    limit_by: ip

# Internal route stricter limits (30/min)
- name: rate-limiting
  config:
    minute: 30
    policy: local
    limit_by: ip
```

### 1.5 Correlation ID ✅

```yaml
- name: correlation-id
  config:
    header_name: X-Request-ID
    generator: uuid
    echo_downstream: true
```

### 1.6 Security Headers (Response Transformer) ✅

```yaml
- name: response-transformer
  config:
    add:
      headers:
        - "X-Frame-Options: DENY"
        - "X-Content-Type-Options: nosniff"
        - "Referrer-Policy: strict-origin-when-cross-origin"
```

### 1.7 Request Size Limiting ✅

```yaml
- name: request-size-limiting
  config:
    allowed_payload_size: 5 # 5MB
```

---

## Part 2: Critical Issues - What Needs to Change

### Issue #1: DUPLICATE CORS - Architecture Conflict 🔴🔴🔴

**Location:**

- `configs/kong/kong.yaml:86-115` (Kong CORS)
- `internal/api/middleware/middleware.go:169-220` (Backend CORS)

**Problem:**
Both Kong AND the Go backend are both setting CORS headers. This causes:

```
Kong sets:
  Access-Control-Allow-Origin: https://dev.lemici.com

Backend then OVERWRITES with potentially different origin:
  Access-Control-Allow-Origin: https://other-origin.com
```

**Impact:**

- Whitelist mismatch between layers
- Headers conflict in preflight responses
- Impossible to centralize CORS policy
- Security risk: Origin validation bypass

**Evidence:**

```go
// Backend middleware.go:169-220
func CORS(corsConfig config.CORSConfig) gin.HandlerFunc {
    return func(c *gin.Context) {
        // Backend sets its own Origin validation
        if cleanAllowed == cleanOrigin || allowedOrigin == "*" {
            c.Header("Access-Control-Allow-Origin", origin)
        }
    }
}
```

```yaml
# Kong kong.yaml:86-115
- name: cors
  config:
    origins:
      - "https://dev.lemici.com"
      - "http://localhost:3000"
      # ... different list
```

**Solution:**
✅ REMOVE CORS middleware from backend - Kong should be the ONLY CORS point.

---

### Issue #2: DUPLICATE SECURITY HEADERS 🔴

**Problem:** Both Kong and backend set security headers.

| Header                    | Kong Sets | Backend Sets       |
| ------------------------- | --------- | ------------------ |
| X-Frame-Options           | DENY      | DENY               |
| X-Content-Type-Options    | nosniff   | nosniff            |
| X-XSS-Protection          | -         | 1; mode=block      |
| Strict-Transport-Security | -         | max-age=31536000   |
| Content-Security-Policy   | -         | default-src 'self' |

**Solution:**

- Use Kong's response-transformer for all security headers
- Remove security header middleware from backend

---

### Issue #3: DUPLICATE RATE LIMITING 🔴

**Problem:** Both layers have rate limiting.

| Layer   | Config           | Storage        |
| ------- | ---------------- | -------------- |
| Kong    | 100/min per IP   | In-memory      |
| Backend | Configurable RPS | In-memory (Go) |

**Issues:**

- Double counting requests
- Different limits cause confusion
- Backend rate limiter adds no value (Kong handles it)

**Solution:**

- Remove rate limiting from backend entirely
- Keep at Kong level only (already properly configured)

---

### Issue #4: DUPLICATE REQUEST ID / TRACING 🟡

**Problem:** Both generate request IDs.

| Layer   | Header       | Implementation        |
| ------- | ------------ | --------------------- |
| Kong    | X-Request-ID | correlation-id plugin |
| Backend | X-Request-ID | UUID generation       |
| Backend | X-Trace-ID   | OpenTelemetry         |
| Backend | X-Span-ID    | OpenTelemetry         |

**Issues:**

- Two different UUIDs generated (header conflict)
- Kong echoes upstream ID (echo_downstream: true)
- Backend creates new one if not present

**Solution:**

- Kong should generate and propagate
- Backend should use OpenTelemetry for tracing (keep this)
- Backend should read X-Request-ID from Kong, not generate new

---

### Issue #5: MISSING LOGGING CONFIGURATION 🟡

**Current State:**

- Kong logging enabled: access logs to stdout
- Backend logging: exists but not integrated

**What's Missing:**

```yaml
# Kong - Add structured logging
KONG_LOG_LEVEL: info
KONG_ANALYTICS: "on" # Not configured
```

**Backend Issues:**

- No logging plugin integration
- Access logs not centralized
- No audit logging for admin actions

**Recommended Kong Logging:**

```yaml
environment:
  KONG_LOG_LEVEL: info
  KONG_PROXY_ERROR_LOG: /dev/stderr
  # Add access log fields
  KONG_PLUGINS: "bundled,cors,rate-limiting,correlation-id,request-size-limiting,response-transformer,ip-restriction,jwt" # Add jwt when needed
```

---

## Part 3: What Should Be Added

### 3.1 Kong Admin API Protection 🟡

**Current:**

- Kong Admin exposed on port 8001 (docker-compose.yml:451)

**Risk:** Internal API not properly secured

**Recommendation:**

```yaml
# Restrict to internal network only
KONG_ADMIN_LISTEN: 127.0.0.1:8001

# OR if needed externally, add JWT
KONG_ADMIN_JWT_SIGNING_KEY: "your-secret-key"
```

### 3.2 API Version Routing 🟡

**Current:** Single `/api` path

**Recommended:**

```yaml
routes:
  - name: api-v1-routes
    paths:
      - /api/v1
    strip_path: true

  - name: api-v2-routes
    paths:
      - /api/v2
    strip_path: true
```

### 3.3 Response Caching for Health Checks 🟢

```yaml
- name: proxy-cache
  config:
    strategy: memory
    memory:
      dictionary_name: kong_cache
    request_method:
      - GET
    response_code:
      - 200
    cache_timeout: 60
```

### 3.4 Bot Detection (Alpha Enhancement) 🟢

```yaml
plugins:
  - name: bot-detection
    config:
      allow: []
      deny:
        - curl
        - wget
        - python-requests
```

### 3.5 Request Transformer (Backend Cleanup) 🟡

Remove sensitive headers before hitting backend:

```yaml
- name: request-transformer
  config:
    remove:
      headers:
        - X-Forwarded-Host
        - X-Real-IP # Let Kong handle this
```

---

## Part 4: Complete CORS Configuration at Kong

### Current Kong CORS (Needs Enhancement) 🟡

```yaml
- name: cors
  config:
    origins:
      - "https://dev.lemici.com"
      - "http://localhost:3000"
      - "http://localhost:5173"
      - "https://mvp.lemici.com"
      - "https://api.mvp.lemici.com"
      - "https://lemici.com"
      - "https://www.lemici.com"
    methods:
      - GET
      - POST
      - PUT
      - PATCH
      - DELETE
      - OPTIONS
    headers:
      - Origin
      - Content-Type
      - Accept
      - Authorization
      - X-Request-ID
      - X-Session-ID
    exposed_headers:
      - X-Request-ID
      - X-RateLimit-Limit
      - X-RateLimit-Remaining
    credentials: true
    max_age: 86400
```

**Issues:**

1. Missing wildcard origin handling for production
2. No dynamic origin validation
3. Pre-flight OPTIONS not logged

**Recommended Enhancement:**

```yaml
# For alpha - keep strict origins
# When moving to production, add:
# - condition for specific environments
# - log blocked origins for monitoring
```

---

## Part 5: Architecture Cleanup - Backend

### What to REMOVE from Backend Middleware

| Middleware           | Currently             | Action                   |
| -------------------- | --------------------- | ------------------------ |
| CORS                 | middleware.go:169-220 | **REMOVE**               |
| Security Headers     | middleware.go:310-318 | **REMOVE**               |
| Rate Limiter         | middleware.go:279-303 | **REMOVE**               |
| Request ID Generator | middleware.go:35-45   | **MODIFY** - use Kong's  |
| Tracing              | middleware.go:51-93   | **KEEP** - OpenTelemetry |
| Logger               | middleware.go:100-133 | **KEEP**                 |
| Recovery             | middleware.go:140-162 | **KEEP**                 |
| Timeout              | middleware.go:325-332 | **KEEP**                 |
| Input Validation     | middleware.go:339-383 | **KEEP**                 |
| Idempotency          | middleware.go:390-458 | **KEEP**                 |

### Backend Modified RequestID Middleware

```go
func RequestID() gin.HandlerFunc {
    return func(c *gin.Context) {
        // Use Kong's correlation ID instead of generating new one
        requestID := c.GetHeader("X-Request-ID")
        if requestID == "" {
            // Generate only if not provided by upstream
            requestID = uuid.New().String()
        }
        c.Set("requestId", requestID)
        c.Header("X-Request-ID", requestID)
        c.Next()
    }
}
```

---

## Part 6: Docker Compose Recommendations

### Add Kong Environment Variables

```yaml
kong:
  environment:
    # Current (keep)
    KONG_DATABASE: "off"
    KONG_DECLARATIVE_CONFIG: /kong/declarative/kong.yml
    KONG_PROXY_ACCESS_LOG: /dev/stdout
    KONG_ADMIN_ACCESS_LOG: /dev/stdout

    # Add for production
    KONG_LOG_LEVEL: info
    KONG_NGINX_PROXY_PROXY_BUFFER_SIZE: "160k"
    KONG_NGINX_PROXY_BUFFER_SIZE: "160k"

    # Recommended security
    KONG_SLASHING_MODE: "escaped"

    # Add for IP awareness (from docker-compose)
    KONG_TRUSTED_IPS: "0.0.0.0/0, ::/0"
    KONG_REAL_IP_HEADER: X-Forwarded-For
    KONG_REAL_IP_RECURSIVE: "on"
```

### Remove Backend Ports (Alpha - Already Correct)

```yaml
# api-gateway ports commented - CORRECT (go through Kong only)
# ports:
#   - "8080:8080"
```

---

## Part 7: Recommended New Kong Config File

```yaml
_format_version: "3.0"
_transform: true

services:
  - name: api-gateway
    url: http://api-gateway:8080

    routes:
      # =========================================
      # ADMIN API - BLOCKED ON PUBLIC
      # =========================================
      - name: block-admin-public
        hosts:
          - api.mvp.lemici.com
          - api.lemici.com
        paths:
          - /api/admin
        regex_priority: 100
        plugins:
          - name: request-termination
            config:
              status_code: 404
              message: "Not Found"

      # =========================================
      # PUBLIC API
      # =========================================
      - name: api-routes
        hosts:
          - api.mvp.lemici.com
          - api.lemici.com
          - localhost
          - 127.0.0.1
        paths:
          - /api
        strip_path: false
        preserve_host: true

      # =========================================
      # HEALTH CHECKS (No Auth)
      # =========================================
      - name: health-routes
        hosts:
          - api.mvp.lemici.com
          - api.lemici.com
          - localhost
          - 127.0.0.1
        paths:
          - /health
          - /metrics
        strip_path: false
        preserve_host: true

      # =========================================
      # INTERNAL ADMIN API (IP Restricted)
      # =========================================
      - name: admin-routes
        paths:
          - /internal
        strip_path: true
        preserve_host: true
        plugins:
          - name: ip-restriction
            config:
              allow:
                - "127.0.0.1/32"
                - "10.0.0.0/8"
                - "172.16.0.0/12"
              status: 403
              message: "Access Denied: Internal API Only"
          - name: rate-limiting
            config:
              minute: 30
              policy: local
              limit_by: ip

    # =========================================
    # GLOBAL SERVICE PLUGINS
    # =========================================
    plugins:
      # -----------------------------------------
      # Correlation ID (Add trace_id for Jaeger)
      # -----------------------------------------
      - name: correlation-id
        config:
          header_name: X-Request-ID
          generator: uuid
          echo_downstream: true

      # -----------------------------------------
      # Rate Limiting (Public API)
      # -----------------------------------------
      - name: rate-limiting
        config:
          minute: 100
          policy: local
          limit_by: ip
          fault_tolerant: true
          hide_client_headers: false # Show rate limit headers to client

      # -----------------------------------------
      # CORS - THE ONLY CORS ENTRY POINT
      # -----------------------------------------
      - name: cors
        config:
          origins:
            - "https://dev.lemici.com"
            - "http://localhost:3000"
            - "http://localhost:5173"
            - "https://mvp.lemici.com"
            - "https://api.mvp.lemici.com"
            - "https://lemici.com"
            - "https://www.lemici.com"
          methods:
            - GET
            - POST
            - PUT
            - PATCH
            - DELETE
            - OPTIONS
          headers:
            - Origin
            - Content-Type
            - Accept
            - Authorization
            - X-Request-ID
            - X-Session-ID
          exposed_headers:
            - X-Request-ID
            - X-RateLimit-Limit
            - X-RateLimit-Remaining
            - X-RateLimit-Reset
          credentials: true
          max_age: 86400

      # -----------------------------------------
      # Request Size Limit (5MB)
      # -----------------------------------------
      - name: request-size-limiting
        config:
          allowed_payload_size: 5
          size_unit: megabytes
          require_body_check: true

      # -----------------------------------------
      # Security Response Headers
      # -----------------------------------------
      - name: response-transformer
        config:
          add:
            headers:
              - "X-Frame-Options: DENY"
              - "X-Content-Type-Options: nosniff"
              - "Referrer-Policy: strict-origin-when-cross-origin"
              - "X-XSS-Protection: 1; mode=block"
```

---

## Recommended Backend Middleware Cleanup

### Modified Middleware Stack

```go
// internal/api/middleware/middleware.go

// KEEP - Tracing (OpenTelemetry integration)
func TracingMiddleware() gin.HandlerFunc

// MODIFIED - Use Kong's request ID, don't generate new
func RequestID() gin.HandlerFunc

// KEEP - Structured logging
func Logger(log logger.Logger) gin.HandlerFunc

// KEEP - Panic recovery
func Recovery(log logger.Logger) gin.HandlerFunc

// REMOVE - CORS handled by Kong
// func CORS(corsConfig config.CORSConfig)

// REMOVE - Rate limiting handled by Kong
// func RateLimiter(rateLimitConfig config.RateLimitConfig)

// REMOVE - Security headers handled by Kong
// func SecurityHeaders()

// KEEP - Timeout protection
func Timeout(timeout time.Duration) gin.HandlerFunc

// KEEP - Input validation
func InputValidation() gin.HandlerFunc

// KEEP - Idempotency
func IdempotencyMiddleware(redisClient *redis.Client)

// KEEP - Error handling
func ErrorHandler(log logger.Logger) gin.HandlerFunc
```

---

## Summary Action Items

### Immediate (Before Frontend Integration)

| Priority    | Action                                | Files                |
| ----------- | ------------------------------------- | -------------------- |
| 🔴 CRITICAL | **Remove CORS from backend**          | `middleware.go`      |
| 🔴 CRITICAL | **Remove duplicate rate limiter**     | `middleware.go`      |
| 🔴 CRITICAL | **Remove duplicate security headers** | `middleware.go`      |
| 🔴 CRITICAL | **Update RequestID middleware**       | `middleware.go`      |
| 🟡 HIGH     | Add Kong logging environment          | `docker-compose.yml` |
| 🟡 HIGH     | Protect Kong admin API                | `docker-compose.yml` |

### For Production (Post-Alpha)

| Priority  | Action                       |
| --------- | ---------------------------- |
| 🟢 MEDIUM | Add API versioning routes    |
| 🟢 MEDIUM | Add response caching         |
| 🟢 MEDIUM | Add bot detection plugin     |
| 🟢 LOW    | Move to database-backed Kong |

---

## Conclusion

This Kong implementation is **solid for alpha** with proper:

- ✅ FQDN-based routing
- ✅ IP allowlisting for internal APIs
- ✅ Rate limiting
- ✅ Correlation ID
- ✅ Security headers

**But it CANNOT go live with the current duplicate middleware architecture.**

The single most important fix: **Remove CORS from the Go backend and let Kong handle all CORS centrally.** This is the #1 cause of production CORS issues.

Once cleaned up, this architecture will properly isolate all API gateway concerns at Kong, allowing the backend to focus on business logic only.
