# Kong API Gateway Integration Guide — LeMiCi Platform

> **Branch:** `kong-integration`  
> **Last Updated:** 2026-05-11

---

## Table of Contents

1. [Architecture Overview](#1-architecture-overview)
2. [Current Service Inventory](#2-current-service-inventory)
3. [Kong Declarative Config — Current State](#3-kong-declarative-config--current-state)
4. [Phase 1 — Core Routing & Service Mapping](#4-phase-1--core-routing--service-mapping)
5. [Phase 2 — Security & Rate Limiting Hardening](#5-phase-2--security--rate-limiting-hardening)
6. [Phase 3 — Keycloak Theme & Auth Flow via Kong](#6-phase-3--keycloak-theme--auth-flow-via-kong)
7. [Phase 4 — Observability & Tracing](#7-phase-4--observability--tracing)
8. [Phase 5 — Deployment & EC2 Rollout](#8-phase-5--deployment--ec2-rollout)
9. [Complete File Change Log](#9-complete-file-change-log)
10. [Verification Checklist](#10-verification-checklist)

---

## 1. Architecture Overview

```
                          Internet
                             |
                        [ALB / NLB]
                             |
                    ┌────────┴────────┐
                    │   KONG :8000   │
                    │  (declarative) │
                    └────────┬────────┘
                             |
              ┌──────────────┼──────────────────┐
              │              │                   │
     ┌────────┴──────┐  ┌───┴────────┐  ┌───────┴────────┐
     │  api-gatewa  │  │  Keycloak │  │  operate-ui   │
     │   (Go/Gin)   │  │  :8080    │  │  (nginx+SPA)  │
     │   :8080      │  │  (8180ext)│  │  :80          │
     └───────┬───────┘  └────────────┘  └────────────────┘
             │
    ┌────────┴──────────────────────┐
    │    camunda-workers          │
    │  (Zeebe 8.3.9 client)       │
    └───────────────────────────────┘
```

### Current Proxy Flow

```
Browser ──► Kong:8000 ──► api-gateway:8080 ──► Workers ──► Zeebe:26500
                                     ├──► PostgreSQL:5432
                                     ├──► Redis:6379
                                     ├──► Elasticsearch:9200
                                     └──► Keycloak:8180 (via worker)
Keycloak Auth ──► Kong:8000 ──► api-gateway:8080 ──► (workflow) ──► Workers ──► Keycloak
```

---

## 2. Current Service Inventory

| #   | Service             | Container                  | Internal Port | External Port | Exposed via Kong     |
| --- | ------------------- | -------------------------- | ------------- | ------------- | -------------------- |
| 1   | **Kong**            | `kong:3.6`                 | 8000          | 8000          | — (entry point)      |
| 2   | **api-gateway**     | `api-gateway`              | 8080          | —             | ✅ Yes               |
| 3   | **camunda-workers** | `camunda-workers`          | —             | —             | ❌ No (backend only) |
| 4   | **Keycloak**        | `keycloak:23.0`            | 8080          | —             | ✅ Yes               |
| 5   | **Zeebe**           | `camunda/zeebe:8.3.9`      | 26500         | 26500         | ❌ No (internal)     |
| 6   | **PostgreSQL**      | `postgres:15-alpine`       | 5432          | 5432          | ❌ No                |
| 7   | **Redis**           | `redis:7-alpine`           | 6379          | 6379          | ❌ No                |
| 8   | **Elasticsearch**   | `elasticsearch:8.9.0`      | 9200          | 9200          | ❌ No                |
| 9   | **Jaeger**          | `jaegertracing/all-in-one` | 4317/16686    | 16686         | ❌ No                |
| 10  | **Operate**         | `camunda/operate:8.3.9`    | 8080          | 8082          | ❌ No                |
| 11  | **Ollama**          | `ollama/ollama`            | 11434         | 11434         | ❌ No                |
| 12  | **MailHog**         | `mailhog/mailhog`          | 1025/8025     | 8025          | ❌ No                |

### Current Kong Route Table

| Route Name           | Path(s)                                 | Hosts                              | Plugin(s)                                          | Strip Path | Priority |
| -------------------- | --------------------------------------- | ---------------------------------- | -------------------------------------------------- | ---------- | -------- |
| `block-admin-public` | `/api/admin`, regex `api/v[0-9]+/admin` | us-dev-api.lemici.com | request-termination (404)                          | —          | 100      |
| `oauth-routes`       | regex `/api/v1/oauth(/.*)?$`            | all hosts                          | rate-limiting (300/m), request-size-limiting (1MB) | false      | 200      |
| `api-v1-routes`      | `/api/v1`                               | all hosts                          | —                                                  | false      | —        |
| `api-v2-routes`      | `/api/v2`                               | all hosts                          | —                                                  | false      | —        |
| `health-routes`      | `/health`, `/metrics`                   | all hosts                          | —                                                  | false      | —        |
| `admin-routes`       | `/internal`                             | (no host filter)                   | ip-restriction, rate-limiting (30/m)               | true       | —        |

### Global Plugins (applied at service level)

- `correlation-id` — X-Request-ID header
- `rate-limiting` — 100 req/min global
- `cors` — multi-origin (dev, mvp, prod)
- `request-size-limiting` — 5MB max
- `response-transformer` — security headers (X-Frame-Options, CSP, etc.)

---

## 3. Kong Declarative Config — Current State

**File:** `configs/kong/kong.yaml` (165 lines)

### What's Working

- Declarative DB-less mode (`KONG_DATABASE: "off"`)
- Service-to-route mapping for `api-gateway:8080`
- CORS fully configured for dev/mvp/prod origins
- Global rate limiting (100 req/min baseline)
- Security headers via response-transformer
- Correlation ID propagation (X-Request-ID)
- Admin/internal route protection via IP restriction
- OAuth routes with tighter rate limit (300/min) + payload limit (1MB)

### What's Missing / Needs Fixing

| Issue                                                        | Severity   | Details                                                                                 |
| ------------------------------------------------------------ | ---------- | --------------------------------------------------------------------------------------- |
| ❌ Keycloak not routed through Kong                          | **HIGH**   | Keycloak exposed on port 8180 directly — bypasses Kong auth, rate limiting, and logging |
| ❌ No Operate route                                          | **HIGH**   | `/operate/` path must proxy to api-gateway (for WebSocket)                              |
| ❌ No `/api/v1/auth/callback` route                          | **HIGH**   | Keycloak callback must not be blocked by Kong                                           |
| ❌ No `/api/v1/contact` route                                | **MEDIUM** | Public route without auth, needs rate limiting                                          |
| ❌ No `/api/v1/forms/:formType/submit` route                 | **MEDIUM** | Public form submission route                                                            |
| ❌ No `operate-ws` WebSocket route                           | **MEDIUM** | `/operate/ws` requires WebSocket upgrade headers                                        |
| ❌ Opentelemetry plugin commented out                        | **LOW**    | Should enable for Kong→Jaeger tracing                                                   |
| ❌ `/api/admin/workflows` block also blocks legitimate admin | **MEDIUM** | `block-admin-public` uses regex that may be over-broad                                  |
| ⚠️ No custom error templates                                 | **LOW**    | Kong returns raw JSON on 404/429 — should be consistent                                 |

---

## 4. Phase 1 — Core Routing & Service Mapping

### 4.1 Files Changed

| File                                    | Action                                                           |
| --------------------------------------- | ---------------------------------------------------------------- |
| `configs/kong/kong.yaml`                | **MODIFY** — add missing routes                                  |
| `deployments/docker/docker-compose.yml` | **MODIFY** — add keycloak route to Kong network, update Kong env |

### 4.2 Step-by-Step Changes

#### Step 1 — Add Keycloak Service to Kong

Add to `configs/kong/kong.yaml`:

```yaml
services:
  # ── Existing api-gateway service (keep as-is) ───────────────────────────
  - name: api-gateway
    url: http://api-gateway:8080
    # ... existing routes ...

  # ── NEW: Keycloak service ──────────────────────────────────────────────
  - name: keycloak
    url: http://keycloak:8080
    routes:
      - name: keycloak-auth
        paths:
          - /auth/realms
          - /auth/admin
          - /auth/resources
        strip_path: false
        preserve_host: true
        hosts:
          - us-dev-api.lemici.com
          - localhost
        plugins:
          - name: rate-limiting
            config:
              minute: 60
              policy: local
              limit_by: ip
          - name: cors
            config:
              origins:
                - "https://dev.lemici.com"
                - "http://localhost:3000"
                - "https://us-dev-api.lemici.com"
                - "https://lemici.com"
                - "https://www.lemici.com"
              methods:
                - GET
                - POST
                - OPTIONS
              headers:
                - Origin
                - Content-Type
                - Authorization
              credentials: true
      - name: keycloak-token
        paths:
          - /realms/camunda-platform/protocol/openid-connect/token
        strip_path: false
        preserve_host: true
        methods:
          - POST
        plugins:
          - name: rate-limiting
            config:
              minute: 120
              policy: local
              limit_by: ip
```

#### Step 2 — Add Operate WebSocket Route

Add to existing `api-gateway` service routes:

```yaml
- name: operate-routes
  paths:
    - /operate
  strip_path: false
  preserve_host: true
  hosts:
    - us-dev-api.lemici.com
    - localhost

- name: operate-ws
  paths:
    - /operate/ws
  strip_path: false
  preserve_host: true
  hosts:
    - us-dev-api.lemici.com
    - localhost
  protocols:
    - http
    - https
    - ws
    - wss
```

#### Step 3 — Add Public Form/Contact Routes

Add to existing `api-gateway` service routes:

```yaml
- name: public-forms
  paths:
    - /api/v1/contact
    - /api/v1/forms
  strip_path: false
  preserve_host: true
  methods:
    - POST
  plugins:
    - name: rate-limiting
      config:
        minute: 10
        policy: local
        limit_by: ip
```

#### Step 4 — Fix Admin Block Regex (Prevent Over-blocking)

Current regex `~/api/v[0-9]+/admin(/.*)?$` also matches legitimate `/api/v1/admin/...` routes. Change approach:

Replace the single `block-admin-public` route with a more specific approach:

```yaml
- name: block-admin-public
  hosts:
    - us-dev-api.lemici.com
  paths:
    - /api/v1/admin
  regex_priority: 100
  plugins:
    - name: request-termination
      config:
        status_code: 404
        message: "Not Found"
```

#### Step 5 — Update docker-compose.yml

Add Keycloak to the Kong network (it already is on `camunda-network` which Kong is also on — verify). Uncomment and fix ports:

```yaml
kong:
  environment:
    KONG_PLUGINS: "bundled,cors,rate-limiting,correlation-id,request-size-limiting,response-transformer,ip-restriction,proxy-cache"
  ports:
    - "8000:8000"
    - "8443:8443" # ← ADD: HTTPS port for future TLS termination
```

Also **remove direct Keycloak port exposure** once Kong is verified:

```yaml
keycloak:
  # ports:          ← COMMENT OUT / REMOVE
  #   - "8180:8080" ← after Kong route is verified
```

---

## 5. Phase 2 — Security & Rate Limiting Hardening

### 5.1 Files Changed

| File                     | Action                                            |
| ------------------------ | ------------------------------------------------- |
| `configs/kong/kong.yaml` | **MODIFY** — add security plugins, tighten limits |

### 5.2 Step-by-Step Changes

#### Step 1 — Add IP Whitelist for Admin Routes

Replace current `admin-routes` with stricter config:

```yaml
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
          - "172.31.0.0/16"
        status: 403
        message: "Access Denied: Internal API"
    - name: rate-limiting
      config:
        minute: 10 # ← TIGHTER: 30→10
        policy: local
        limit_by: ip
    - name: key-auth # ← NEW: require API key
      config:
        key_names:
          - X-Internal-API-Key
        key_in_body: false
```

#### Step 2 — Per-Route Rate Limit Tiers

| Tier         | Route Pattern                      | Limit    | Burst | Applied To                  |
| ------------ | ---------------------------------- | -------- | ----- | --------------------------- |
| **Strict**   | `/api/v1/contact`, `/api/v1/forms` | 10/min   | 5     | Public form submissions     |
| **Moderate** | `/api/v1/oauth`                    | 300/min  | 50    | OAuth token endpoints       |
| **Standard** | `/api/v1/*`                        | 100/min  | 20    | All authenticated API       |
| **Relaxed**  | `/health`, `/metrics`              | 1000/min | 100   | Health checks               |
| **Keycloak** | `/auth/realms/*`                   | 60/min   | 10    | Login page, realm resources |
| **Token**    | `/realms/*/token`                  | 120/min  | 20    | Token generation            |

#### Step 3 — Enable Request Body Validation

Add to global plugins:

```yaml
- name: request-validator
  config:
    allowed_content_types:
      - application/json
      - application/x-www-form-urlencoded
      - multipart/form-data
    max_body_size: 5242880 # 5MB
```

#### Step 4 — Add Bot Detection (Optional)

```yaml
- name: bot-detection
  config:
    allow:
      - "Mozilla/5.0"
      - "PostmanRuntime"
    deny:
      - "curl"
      - "wget"
      - "python-requests"
    deny_on_failure: false
```

---

## 6. Phase 3 — Keycloak Theme & Auth Flow via Kong

### 6.1 Current Auth Flow

```
Browser ──► us-dev-api.lemici.com ──► Kong:8000 ──► api-gateway:8080
                          (POST /api/v1/auth/login)
                               │
                     workflow.StartKeycloakLogin
                               │
                     ┌─────────▼─────────┐
                     │  Zeebe Workflow    │
                     │  (keycloak-signin) │
                     └─────────┬─────────┘
                               │
                     ┌─────────▼─────────┐
                     │  Worker            │
                     │  → Keycloak API    │
                     │    (back-channel)  │
                     │  → Redis session   │
                     └───────────────────┘
```

### 6.2 Target Auth Flow (via Kong)

```
Browser ──► us-dev-api.lemici.com ──► Kong:8000
                          │
            ┌─────────────┴─────────────┐
            │  POST /api/v1/auth/login  │
            │  → api-gateway:8080       │
            │    (same workflow path)   │
            ├───────────────────────────┤
            │  GET /auth/realms/*       │
            │  → keycloak:8080          │
            │    (Kong now proxies)     │
            ├───────────────────────────┤
            │  GET /api/v1/auth/callback│
            │  → api-gateway:8080       │
            │    (for OAuth redirect)   │
            └───────────────────────────┘
```

### 6.3 Keycloak Theme Files (Already Present)

| File                                                  | Status    | Notes                              |
| ----------------------------------------------------- | --------- | ---------------------------------- |
| `keycloak-theme/lemici/theme.properties`              | ✅ Exists | Parent: keycloak, imports common   |
| `keycloak-theme/lemici/login/template.ftl`            | ✅ Exists | Split-view: form left, image right |
| `keycloak-theme/lemici/login/login.ftl`               | ✅ Exists | Email/password + Google SSO        |
| `keycloak-theme/lemici/login/register.ftl`            | ✅ Exists | Registration form                  |
| `keycloak-theme/lemici/login/error.ftl`               | ✅ Exists | Error page                         |
| `keycloak-theme/lemici/login/resources/css/style.css` | ✅ Exists | Custom styles                      |

### 6.4 Changes Required for Keycloak Theme via Kong

#### Step 1 — Volume Mount Theme into Keycloak Container

In `docker-compose.yml`, add to `keycloak` service:

```yaml
keycloak:
  volumes:
    - ./keycloak/realm-export.json:/opt/keycloak/data/import/realm-export.json:ro
    - ../../keycloak-theme/lemici:/opt/keycloak/themes/lemici:ro # ← ADD
    - keycloak-data:/opt/keycloak/data
  environment:
    KC_SPI_THEME_STATIC_MAX_AGE: 2592000
    KC_SPI_THEME_CACHE_TTL: -1 # no cache during dev
    KC_LOGIN_THEME: lemici # already set in realm
```

#### Step 2 — Ensure Keycloak Redirect URIs Use Kong URL

In `realms-export.json`, update `lemici-frontend` client:

```json
{
  "clientId": "lemici-frontend",
  "redirectUris": [
    "https://d3r20osyd7sq76.cloudfront.net/*",
    "http://localhost:3000/*",
    "https://us-dev-api.lemici.com/api/v1/auth/callback",
    "https://us-dev-api.lemici.com/*"
  ],
  "webOrigins": [
    "https://dev.lemici.com",
    "https://us-dev-api.lemici.com",
    "http://localhost:3000"
  ]
}
```

**NOTE:** Keycloak must trust the Kong proxy. Set:

```yaml
KC_PROXY: edge # Already set ✅
KC_HOSTNAME_URL: https://us-dev-api.lemici.com
KC_HOSTNAME_ADMIN_URL: https://us-dev-api.lemici.com/auth/admin
```

#### Step 3 — Add `auth/callback` Route in Kong

```yaml
- name: auth-callback
  paths:
    - /api/v1/auth/callback
  strip_path: false
  preserve_host: true
  methods:
    - GET
    - POST
```

#### Step 4 — Add `auth/resources` Static Asset Route for Keycloak Theme

Keycloak serves its theme resources (CSS, JS, images) at:

```
GET /auth/resources/<version>/<theme-type>/<theme-name>/
```

Kong must route these to Keycloak:

```yaml
- name: keycloak-resources
  paths:
    - /auth/resources
  strip_path: false
  preserve_host: true
  plugins:
    - name: proxy-cache
      config:
        content_type:
          - text/css
          - application/javascript
          - image/png
          - image/jpeg
          - image/svg+xml
        cache_ttl: 3600 # 1 hour
        strategy: memory
```

---

## 7. Phase 4 — Observability & Tracing

### 7.1 Files Changed

| File                     | Action                                   |
| ------------------------ | ---------------------------------------- |
| `configs/kong/kong.yaml` | **MODIFY** — enable opentelemetry plugin |

### 7.2 Step-by-Step Changes

#### Step 1 — Enable OpenTelemetry Plugin

Uncomment and update the OTEL plugin in global plugins:

```yaml
- name: opentelemetry
  config:
    endpoint: http://jaeger:4318/v1/traces
    sampling_rate: 0.1 # 10% sampling in dev
    headers:
      - "Authorization: Bearer ${JAEGER_TOKEN}"
    resource_attributes:
      - "service.name=kong-api-gateway"
      - "deployment.environment=development"
```

#### Step 2 — Add Kong Plugin to docker-compose

Ensure Kong image includes OTEL plugin (bundled since Kong 3.x):

```yaml
kong:
  environment:
    KONG_PLUGINS: "bundled,cors,rate-limiting,correlation-id,request-size-limiting,response-transformer,ip-restriction,opentelemetry,proxy-cache"
```

#### Step 3 — Add Kong Logging to Elasticsearch (Optional)

```yaml
- name: file-log
  config:
    path: /var/log/kong/access.log
    reopen: true
```

---

## 8. Phase 5 — Deployment & EC2 Rollout

### 8.1 Current EC2 Services

Based on the `config.prod.yaml` and docker-compose, the current EC2 instance runs:

| Service         | Container                  | Notes                    |
| --------------- | -------------------------- | ------------------------ |
| Kong            | `kong:3.6`                 | Entry point on port 8000 |
| api-gateway     | Custom Go binary           | Gin backend              |
| camunda-workers | Custom Go binary           | Zeebe job workers        |
| Keycloak        | `quay.io/keycloak:23.0`    | Port 8180 (direct)       |
| PostgreSQL      | `postgres:15-alpine`       | Port 5432                |
| Redis           | `redis:7-alpine`           | Port 6379                |
| Elasticsearch   | `elasticsearch:8.9.0`      | Port 9200                |
| Zeebe           | `camunda/zeebe:8.3.9`      | Port 26500               |
| Operate         | `camunda/operate:8.3.9`    | Port 8082                |
| Jaeger          | `jaegertracing/all-in-one` | Port 16686               |
| Ollama          | `ollama/ollama`            | Port 11434               |
| MailHog         | `mailhog/mailhog`          | Port 8025                |

### 8.2 Rollout Sequence

#### Step 1 — Backup Current Kong Config

```bash
# Save current running config
ssh ec2-user@<EC2_IP> "curl -s http://localhost:8001/routes | jq ." > kong-routes-backup.json
```

#### Step 2 — Update Kong Config File

```bash
# Copy updated kong.yaml to EC2
scp configs/kong/kong.yaml ec2-user@<EC2_IP>:~/kong.yaml

# Reload Kong declarative config
ssh ec2-user@<EC2_IP> "curl -X POST http://localhost:8001/config \
  -H 'Content-Type: multipart/form-data' \
  -F 'config=@~/kong.yaml'"
```

Or restart the Kong container:

```bash
docker-compose restart kong
```

#### Step 3 — Add Keycloak Volume Mount & Restart

```bash
# Copy theme to EC2 (or use volume mount from repo)
scp -r keycloak-theme/lemici ec2-user@<EC2_IP>:~/lemici-theme

# SSH and restart Keycloak with volume
ssh ec2-user@<EC2_IP> "docker-compose -f deployments/docker/docker-compose.yml up -d keycloak"
```

#### Step 4 — Verify Each Route

```bash
# Test Core API
curl -s -o /dev/null -w "%{http_code}" http://<EC2_IP>:8000/api/v1/franchises/home

# Test Keycloak via Kong
curl -s -o /dev/null -w "%{http_code}" http://<EC2_IP>:8000/auth/realms/camunda-platform

# Test Token Endpoint
curl -s -X POST http://<EC2_IP>:8000/realms/camunda-platform/protocol/openid-connect/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=client_credentials&client_id=worker-client&client_secret=<SECRET>"

# Test Health
curl -s http://<EC2_IP>:8000/health | jq .

# Test Admin Block (should return 404)
curl -s -o /dev/null -w "%{http_code}" http://us-dev-api.lemici.com:8000/api/v1/admin

# Test Websocket Upgrade
curl -s -o /dev/null -w "%{http_code}" \
  -H "Connection: Upgrade" \
  -H "Upgrade: websocket" \
  http://<EC2_IP>:8000/operate/ws
```

#### Step 5 — Disable Direct Keycloak Port 8180

After verifying all Keycloak routes work via Kong, update security group to block port 8180 from external.

#### Step 6 — DNS Cutover

Update Route53 if needed:

| DNS Record              | Type     | Value      | Target              |
| ----------------------- | -------- | ---------- | ------------------- |
| `us-dev-api.lemici.com` | A        | `<EC2_IP>` | Kong:8000           |
| `us-dev-api.lemici.com`    | A        | `<EC2_IP>` | Kong:8000           |
| `us-dev-api.lemici.com` | A        | `<EC2_IP>` | Kong:8000 (already) |
| `keycloak.lemici.com`   | (Remove) | —          | No longer needed    |

---

## 9. Complete File Change Log

### 9.1 Files Modified

```
✓ configs/kong/kong.yaml                          # Main Kong declarative config — ADD routes, services, security
✓ deployments/docker/docker-compose.yml           # Keycloak theme volume, Kong plugins list
```

### 9.2 Files Created

```
✓ KONG-INTEGRATION-GUIDE.md                       # This document (project root)
```

### 9.3 Files Already Present (No Change Needed)

```
✓ keycloak-theme/lemici/theme.properties          # Theme definition
✓ keycloak-theme/lemici/login/template.ftl        # Login page template (split view)
✓ keycloak-theme/lemici/login/login.ftl            # Login form (email/password + Google)
✓ keycloak-theme/lemici/login/register.ftl         # Registration form
✓ keycloak-theme/lemici/login/error.ftl            # Error page
✓ keycloak-theme/lemici/login/resources/css/style.css  # Custom CSS
✓ deployments/docker/keycloak/realm-export.json    # Keycloak realm config (theme already set: "lemici")
✓ deployments/docker/Dockerfile.gateway            # Go API gateway build
✓ deployments/docker/Dockerfile.worker             # Camunda workers build
✓ deployments/docker/Dockerfile.operate            # Operate UI build
✓ deployments/docker/nginx.operate.conf            # Nginx config for operate-ui
```

### 9.4 Config File Dependency Map

```
configs/kong/kong.yaml
  └── mounted at: /kong/declarative/kong.yml (inside Kong container)
  └── referenced by: docker-compose.yml → kong.volumes

configs/config.yaml
  └── mounted at: /app/configs/config.yaml (inside api-gateway/workers)
  └── controls: routes, auth, DB connections, worker params

configs/config.dev.yaml
  └── overrides: keycloak URLs, session config, API keys for dev

configs/config.prod.yaml
  └── overrides: PG host, logging level, API keys for production

deployments/docker/docker-compose.yml
  └── top-level orchestrator — all services, networks, volumes
```

---

## 10. Verification Checklist

### Phase 1 — Core Routing

- [ ] Kong returns `200` for `GET /api/v1/franchises/home`
- [ ] Kong returns `200` for `GET /health`
- [ ] Kong returns `404` for `GET /api/v1/admin/workflows` (blocked)
- [ ] Kong returns `200` for `POST /api/v1/auth/login`
- [ ] Kong returns `200` for `GET /api/v1/auth/callback`
- [ ] Kong proxies WebSocket upgrade for `/operate/ws`

### Phase 2 — Keycloak via Kong

- [ ] Kong returns `200` for `GET /auth/realms/camunda-platform`
- [ ] Kong returns `200` for `POST /realms/.../token` (client_credentials)
- [ ] Keycloak login page loads with lemici theme (split view)
- [ ] Google SSO button visible on login page
- [ ] POST to `/auth/realms/.../token` respects 120/min rate limit

### Phase 3 — Security

- [ ] Security headers present: X-Frame-Options, CSP, X-Content-Type-Options
- [ ] Rate limit hits 429 for abuse on `/api/v1/contact`
- [ ] Internal routes blocked from external IPs
- [ ] Admin routes require key auth

### Phase 4 — Observability

- [ ] Jaeger shows spans from Kong → api-gateway → workers
- [ ] Correlation ID (X-Request-ID) propagates through all services
- [ ] Kong access logs visible in stdout

### Phase 5 — Deployment

- [ ] No direct access to Keycloak on port 8180 (security group)
- [ ] All DNS records point to Kong (port 8000)
- [ ] `docker-compose restart kong` succeeds without errors
- [ ] All existing frontend functionality works via Kong proxy

---

## Route Matrix — Final State

```
METHOD  PATH                                          SERVICE         AUTH    RATE LIMIT
──────  ────────────────────────────────────────────  ─────────────── ──────  ─────────
GET     /health                                       api-gateway     None    1000/min
GET     /metrics                                      api-gateway     None    1000/min
POST    /api/v1/auth/login                            api-gateway     None    60/min
POST    /api/v1/auth/logout                           api-gateway     None    60/min
GET     /api/v1/auth/callback                         api-gateway     None    60/min
POST    /api/v1/auth/password/reset                   api-gateway     None    10/min
POST    /api/v1/oauth/logout                          api-gateway     None    300/min
POST    /api/v1/oauth/logout-all                      api-gateway     None    300/min
GET     /api/v1/oauth/me                              api-gateway     JWT     100/min
GET     /api/v1/franchises/home                       api-gateway     None    100/min
GET     /api/v1/franchises/listing                    api-gateway     None    100/min
GET     /api/v1/franchises/detail/:slug               api-gateway     None    100/min
GET     /api/v1/franchises/search                     api-gateway     None    100/min
GET     /api/v1/franchises/:id                        api-gateway     None    100/min
POST    /api/v1/franchises/create                     api-gateway     JWT     100/min
PUT     /api/v1/franchises/:id                        api-gateway     JWT     100/min
DELETE  /api/v1/franchises/:id                        api-gateway     JWT     100/min
POST    /api/v1/contact                               api-gateway     None    10/min
POST    /api/v1/forms/:formType/submit                api-gateway     None    10/min
POST    /api/v1/ai/query                              api-gateway     JWT     100/min
POST    /api/v1/ai/discovery                          api-gateway     JWT     100/min
PUT     /api/v1/user/profile                          api-gateway     JWT     100/min
DELETE  /api/v1/user/account                          api-gateway     JWT     100/min
GET     /api/v1/user/profile                          api-gateway     JWT     100/min
GET     /api/v1/applications/*                        api-gateway     JWT     100/min
POST    /api/v1/applications/submit                   api-gateway     JWT     100/min
POST    /api/v1/crm/sync                              api-gateway     JWT     100/min
POST    /api/v1/email/campaign                        api-gateway     JWT     100/min
POST    /api/v1/email/welcome-series                  api-gateway     JWT     100/min
POST    /api/v1/social/auth                           api-gateway     JWT     100/min
POST    /api/v1/error/handle                          api-gateway     JWT     100/min
GET     /operate                                      api-gateway     JWT     100/min
WS      /operate/ws                                   api-gateway     JWT     N/A
GET     /auth/realms/*                                keycloak        None    60/min
POST    /realms/*/token                               keycloak        None    120/min
GET     /auth/resources/*                             keycloak        None    60/min
GET     /auth/admin/*                                 keycloak        Admin   30/min
GET     /internal/*                                   api-gateway     IP+Key  10/min
GET     /api/admin/workflows                          api-gateway     JWT+Adm 30/min
POST    /api/admin/workflows/:id/cancel               api-gateway     JWT+Adm 30/min
```

---

## Appendix A — Kong Version & Plugin Compatibility

| Feature                    | Kong 3.6           |
| -------------------------- | ------------------ |
| DB-less declarative config | ✅ Fully supported |
| OpenTelemetry tracing      | ✅ Bundled plugin  |
| WebSocket proxying         | ✅ Native support  |
| Rate limiting (local)      | ✅ Bundled         |
| CORS                       | ✅ Bundled         |
| IP restriction             | ✅ Bundled         |
| Request size limiting      | ✅ Bundled         |
| Response transformer       | ✅ Bundled         |
| Correlation ID             | ✅ Bundled         |
| Proxy cache                | ✅ Bundled         |
| Request validator          | ✅ Bundled         |
| Key auth                   | ✅ Bundled         |
| Bot detection              | ✅ Bundled         |

## Appendix B — Rollback Procedure

If Kong integration breaks production:

```bash
# 1. Restore previous Kong config
cp configs/kong/kong.yaml.bak configs/kong/kong.yaml

# 2. Re-expose Keycloak directly
# In docker-compose.yml, uncomment keycloak ports:
#   ports:
#     - "8180:8080"

# 3. Restart Kong
docker-compose restart kong

# 4. Update DNS if needed
# Point us-dev-api.lemici.com directly to ALB bypassing Kong
```

---

_End of Integration Guide_
