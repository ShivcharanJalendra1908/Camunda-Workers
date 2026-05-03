# Kong API Gateway - Complete Local Testing Guide

**Test Environment:** Docker Compose (local)  
**Kong Proxy:** <http://localhost:8000>  
**Kong Admin:** <http://localhost:8001>

---

## Prerequisites

### 1. Directory Structure

```
/home/kintesh/projects/go/workspace/Workflow-and-Workers/
├── deployments/
│   └── docker/
│       ├── docker-compose.yml
│       ├── Dockerfile.gateway
│       └── Dockerfile.worker
├── configs/
│   └── kong/
│       └── kong.yaml
├── internal/
│   └── api/
├── bpmn/
└── docker-compose.yml (root)
```

---

## Step 1: Start All Services

### 1.1 Full Startup (All Services)

From project root:

```bash
cd /home/kintesh/projects/go/workspace/Workflow-and-Workers

# Start all services
docker compose -f deployments/docker/docker-compose.yml up -d
```

**This starts:**

| Service       | Container       | Port      | Purpose         |
| ------------- | --------------- | --------- | --------------- |
| Kong          | kong            | 8000      | API Gateway     |
| Zeebe         | zeebe           | 26500     | Workflow Engine |
| PostgreSQL    | postgres        | 5432      | Database        |
| Redis         | redis           | 6379      | Cache           |
| Elasticsearch | elasticsearch   | 9200      | Search/Logs     |
| Jaeger        | jaeger          | 16686     | Tracing         |
| Operate       | operate         | 8082      | Camunda UI      |
| Keycloak      | keycloak        | 8180      | Auth            |
| Ollama        | ollama          | 11434     | LLM             |
| API Gateway   | api-gateway     | -         | Go Backend      |
| Workers       | camunda-workers | -         | Background Jobs |
| Mailhog       | mailhog         | 1025/8025 | Email Testing   |

### 1.2 Startup Order (Dependencies First)

If you want to start services in stages:

```bash
# 1. Start infrastructure first
docker compose -f deployments/docker/docker-compose.yml up -d \
  postgres \
  redis \
  elasticsearch

# Wait for healthy
docker compose -f deployments/docker/docker-compose.yml ps

# 2. Start observability
docker compose -f deployments/docker/docker-compose.yml up -d \
  jaeger \
  keycloak

# 3. Start Camunda
docker compose -f deployments/docker/docker-compose.yml up -d \
  zeebe \
  operate

# 4. Start AI
docker compose -f deployments/docker/docker-compose.yml up -d \
  ollama \
  ollama-setup

# 5. Start API Gateway and Workers
docker compose -f deployments/docker/docker-compose.yml up -d \
  api-gateway \
  camunda-workers

# 6. Start Kong (LAST - routes to backend)
docker compose -f deployments/docker/docker-compose.yml up -d kong
```

### 1.3 Quick Kong Only (For Config Testing)

```bash
# Only Kong + api-gateway (for config testing)
cd /home/kintesh/projects/go/workspace/Workflow-and-Workers/deployments/docker

docker compose up -d kong api-gateway
```

Wait for Kong to be healthy:

```bash
# Check Kong status
curl -s http://localhost:8001/status | jq

# Check Kong health
curl -s http://localhost:8000/health
```

---

## Step 2: Verify All Services Running

### 2.1 Check Container Status

```bash
docker compose -f deployments/docker/docker-compose.yml ps
```

**Expected output:**

```
NAME                IMAGE                      STATUS
kong               kong:3.6                 Up (healthy)
api-gateway         app                       Up (healthy)
zeebe               camunda/zeebe:8.3.9       Up (healthy)
postgres            postgres:15-alpine        Up (healthy)
redis               redis:7-alpine            Up (healthy)
elasticsearch      elasticsearch:8.9.0       Up (healthy)
jaeger              jaegertracing/all-in-...   Up (healthy)
keycloak            quay.io/keycloak/...     Up (healthy)
ollama              ollama/ollama:latest      Up (healthy)
camunda-workers     app                       Up (healthy)
```

### 2.2 Health Checks (All Services)

```bash
# Kong
curl -s http://localhost:8001/status | jq

# API Gateway
curl -s http://localhost:8000/health

# Zeebe
curl -s http://localhost:9600/ready

# Elasticsearch
curl -s http://localhost:9200/_cluster/health

# PostgreSQL
docker exec postgres pg_isready -U postgres

# Redis
docker exec redis redis-cli ping

# Keycloak
curl -s http://localhost:8180/health/ready

# Jaeger
curl -s http://localhost:16686/api/health

# Ollama
curl -s http://localhost:11434/api/tags
```

---

## Step 3: Test Kong Configuration

### 3.1 Kong Health

```bash
curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  -H "Host: localhost" \
  http://localhost:8000/health
```

**Expected:** HTTP 200

### 3.2 Kong Metrics

```bash
curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  -H "Host: localhost" \
  http://localhost:8000/metrics
```

### 3.3 Verify Kong Config

```bash
# Check routes
curl -s http://localhost:8001/routes | jq '.data[] | {name, paths}'

# Check services
curl -s http://localhost:8001/services | jq

# Check plugins
curl -s http://localhost:8001/plugins | jq '.data[] | {name, enabled}'
```

---

## Step 4: CORS Tests

### 4.1 Preflight (Allowed Origin)

```bash
curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  -X OPTIONS http://localhost:8000/api \
  -H "Origin: http://localhost:3000" \
  -H "Access-Control-Request-Method: GET" \
  -H "Access-Control-Request-Headers: Content-Type,Authorization" \
  -i
```

**Expected:**

```
HTTP/1.1 200 OK
Access-Control-Allow-Origin: http://localhost:3000
Access-Control-Allow-Methods: GET, POST, PUT, PATCH, DELETE, OPTIONS
Access-Control-Allow-Headers: Origin, Content-Type, Accept, Authorization, X-Request-ID, X-Session-ID
Access-Control-Allow-Credentials: true
Access-Control-Max-Age: 86400
```

### 4.2 Preflight (Disallowed Origin)

```bash
curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  -X OPTIONS http://localhost:8000/api \
  -H "Origin: https://evil-site.com" \
  -H "Access-Control-Request-Method: GET" \
  -i
```

**Expected:** No CORS headers in response

### 4.3 Preflight (Production Domains)

```bash
# Test with mvp.lemici.com
curl -s -i -X OPTIONS http://localhost:8000/api \
  -H "Origin: https://mvp.lemici.com" \
  -H "Access-Control-Request-Method: POST" \
  http://localhost:8000/api >/dev/null

# Test with api.mvp.lemici.com
curl -s -i -X OPTIONS http://localhost:8000/api \
  -H "Origin: https://api.mvp.lemici.com" \
  -H "Access-Control-Request-Method: POST" \
  http://localhost:8000/api >/dev/null
```

---

## Step 5: Rate Limit Tests

### 5.1 Check Rate Limit Headers

```bash
curl -s -I -H "Host: localhost" \
  http://localhost:8000/health | grep -i ratelimit
```

**Expected:**

```
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 99
X-RateLimit-Reset: <unix-timestamp>
```

### 5.2 Exhaust Rate Limit

```bash
# Make 100 requests rapidly
for i in {1..100}; do
  curl -s -o /dev/null -H "Host: localhost" http://localhost:8000/health
done

# 101st request should be limited
curl -s -w "\nHTTP_CODE:%{http_code}\n" -H "Host: localhost" http://localhost:8000/health
```

**Expected:** HTTP 429

### 5.3 Rate Limit Response Headers

```bash
curl -s -i -H "Host: localhost" http://localhost:8000/health
```

**Expected:**

```
HTTP/1.1 429 Too Many Requests
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 0
Retry-After: <seconds>
```

---

## Step 6: Correlation ID Tests

### 6.1 Client Provided ID

```bash
curl -s -i -H "X-Request-ID: my-custom-id-12345" -H "Host: localhost" \
  http://localhost:8000/health 2>&1 | grep -i "x-request-id"
```

**Expected:** `X-Request-ID: my-custom-id-12345`

### 6.2 Kong Generates ID

```bash
curl -s -i -H "Host: localhost" \
  http://localhost:8000/health 2>&1 | grep -i "x-request-id"
```

**Expected:** `X-Request-ID: <uuid>`

---

## Step 7: Security Headers Tests

### 7.1 Check All Security Headers

```bash
curl -s -I -H "Host: localhost" http://localhost:8000/health
```

**Expected:**

```
X-Frame-Options: DENY
X-Content-Type-Options: nosniff
Referrer-Policy: strict-origin-when-cross-origin
```

---

## Step 8: Route Blocking Tests

### 8.1 Admin Route Blocked (Public FQDN)

```bash
curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  -H "Host: api.lemici.com" \
  http://localhost:8000/api/admin/users
```

**Expected:** HTTP 404

**Response:**

```json
{ "message": "Not Found" }
```

### 8.2 Admin Route Not Blocked (Internal Access)

```bash
curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  -H "Host: localhost" \
  http://localhost:8000/api/admin/health
```

**Expected:** HTTP 200 (when internal routing works)

---

## Step 9: Internal API Protection (IP Allowlist)

### 9.1 /internal from Localhost (Allowed)

```bash
curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  -H "Host: localhost" \
  http://localhost:8000/internal/health
```

**Expected:** HTTP 200 (127.0.0.1 in allowlist)

### 9.2 /internal from External IP (Blocked)

```bash
curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  -H "Host: api.lemici.com" \
  -H "X-Forwarded-For: 203.0.113.50" \
  http://localhost:8000/internal/health
```

**Expected:** HTTP 403

---

## Step 10: Request Size Limit Tests

### 10.1 Small Request (Success)

```bash
echo '{"name": "test", "value": "data"}' > /tmp/small.json

curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  -X POST -H "Host: localhost" \
  -H "Content-Type: application/json" \
  -d @/tmp/small.json \
  http://localhost:8000/api/health
```

### 10.2 Large Request (>5MB - Blocked)

```bash
# Create 6MB file
dd if=/dev/zero of=/tmp/large.json bs=1M count=6 2>/dev/null

curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  -X POST -H "Host: localhost" \
  -H "Content-Type: application/json" \
  --data-binary @/tmp/large.json \
  http://localhost:8000/api/health
```

**Expected:** HTTP 413

---

## Step 11: Backend Proxy Tests

### 11.1 Full Request to Backend via Kong

```bash
curl -s -w "\nHTTP_CODE:%{http_code}\n" \
  -H "Host: localhost" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer test-token" \
  -X GET \
  http://localhost:8000/api/health
```

**Expected:**

- HTTP 200
- Kong adds: X-Request-ID, security headers, rate limit headers
- Backend receives: original headers + X-Request-ID

### 11.2 Direct Backend (Bypassing Kong)

```bash
# Connect directly to api-gateway container
docker exec api-gateway wget -qO- http://localhost:8080/health
```

**Expected:** HTTP 200 (direct connection)

---

## Step 12: Full Integration Tests

### 12.1 Complete API Flow

```bash
# 1. Health check
curl -s http://localhost:8000/health

# 2. Auth via Keycloak (if configured)
curl -s -X POST http://localhost:8180/realms/camunda-platform/protocol/openid-connect/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=password" \
  -d "client_id=worker-client" \
  -d "client_secret=yNHfb6FR4ZGIsUyi27pSpfsAbxEfquhS" \
  -d "username=admin" \
  -d "password=admin"

# 3. API call with token
# TOKEN=$(curl -s ... | jq -r '.access_token')
# curl -H "Authorization: Bearer $TOKEN" http://localhost:8000/api/...
```

### 12.2 Camunda Workflow

```bash
# Check Zeebe status
curl -s http://localhost:9600/ready

# Check Operate
curl -s http://localhost:8082/actuator/health
```

### 12.3 Tracing

```bash
# Check Jaeger UI
curl -s http://localhost:16686/api/health
```

---

## Comprehensive Test Script

Save as `test-all.sh`:

```bash
#!/bin/bash
set -e

echo "========================================"
echo "Full Kong + Backend Test Suite"
echo "========================================"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "${GREEN}PASS${NC}: $1"; }
fail() { echo -e "${RED}FAIL${NC}: $1"; }
info() { echo -e "${YELLOW}INFO${NC}: $1"; }

KONG_URL="http://localhost:8000"

# ========================================
# SECTION 1: Infrastructure
# ========================================
echo ""
echo ">>> SECTION 1: Infrastructure"
echo "========================================"

info "Checking Kong..."
if curl -s -o /dev/null -w "%{http_code}" http://localhost:8001/status | grep -q "200"; then
    pass "Kong is running"
else
    fail "Kong is not running"
fi

info "Checking API Gateway..."
if curl -s -o /dev/null -w "%{http_code}" http://localhost:8000/health | grep -q "200"; then
    pass "API Gateway reachable"
else
    fail "API Gateway not reachable"
fi

info "Checking PostgreSQL..."
if docker exec postgres pg_isready -U postgres >/dev/null 2>&1; then
    pass "PostgreSQL is running"
else
    fail "PostgreSQL not running"
fi

info "Checking Redis..."
if docker exec redis redis-cli ping | grep -q "PONG"; then
    pass "Redis is running"
else
    fail "Redis not running"
fi

info "Checking Elasticsearch..."
if curl -s -o /dev/null -w "%{http_code}" http://localhost:9200/_cluster/health | grep -q "200"; then
    pass "Elasticsearch is running"
else
    fail "Elasticsearch not running"
fi

info "Checking Keycloak..."
if curl -s -o /dev/null -w "%{http_code}" http://localhost:8180/health/ready | grep -q "200"; then
    pass "Keycloak is running"
else
    fail "Keycloak not running"
fi

info "Checking Camunda Zeebe..."
if curl -s -o /dev/null -w "%{http_code}" http://localhost:9600/ready | grep -q "200"; then
    pass "Zeebe is running"
else
    fail "Zeebe not running"
fi

# ========================================
# SECTION 2: Kong Configuration
# ========================================
echo ""
echo ">>> SECTION 2: Kong Configuration"
echo "========================================"

info "Testing Kong Health..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: localhost" $KONG_URL/health)
if [ "$HTTP_CODE" = "200" ]; then
    pass "Health endpoint returns 200"
else
    fail "Health endpoint returns $HTTP_CODE"
fi

info "Testing CORS Preflight..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X OPTIONS $KONG_URL/api \
  -H "Origin: http://localhost:3000" \
  -H "Access-Control-Request-Method: GET")
if [ "$HTTP_CODE" = "200" ]; then
    pass "CORS preflight returns 200"
else
    fail "CORS preflight returns $HTTP_CODE"
fi

info "Testing Rate Limit Headers..."
if curl -s -I -H "Host: localhost" $KONG_URL/health | grep -qi "X-RateLimit-Limit"; then
    pass "Rate limit headers present"
else
    fail "Rate limit headers missing"
fi

info "Testing Correlation ID..."
if curl -s -i -H "Host: localhost" $KONG_URL/health | grep -qi "X-Request-ID"; then
    pass "Correlation ID present"
else
    fail "Correlation ID missing"
fi

info "Testing Security Headers..."
if curl -s -I -H "Host: localhost" $KONG_URL/health | grep -qi "X-Frame-Options"; then
    pass "Security headers present"
else
    fail "Security headers missing"
fi

info "Testing Admin Route Blocked..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: api.lemici.com" $KONG_URL/api/admin/users)
if [ "$HTTP_CODE" = "404" ]; then
    pass "Admin route blocked (404)"
else
    fail "Admin route returns $HTTP_CODE"
fi

info "Testing Internal Route (localhost)..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: localhost" $KONG_URL/internal/health)
if [ "$HTTP_CODE" = "200" ]; then
    pass "Internal route accessible from localhost"
else
    fail "Internal route returns $HTTP_CODE"
fi

# ========================================
# SECTION 3: Backend Tests
# ========================================
echo ""
echo ">>> SECTION 3: Backend Tests"
echo "========================================"

info "Testing Backend Direct..."
if docker exec api-gateway wget -qO- http://localhost:8080/health >/dev/null 2>&1; then
    pass "Backend directly accessible"
else
    fail "Backend not accessible"
fi

info "Testing Backend via Kong..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: localhost" $KONG_URL/api/health)
if [ "$HTTP_CODE" = "200" ]; then
    pass "Backend reachable via Kong"
else
    fail "Backend returns $HTTP_CODE via Kong"
fi

# ========================================
# SUMMARY
# ========================================
echo ""
echo "========================================"
echo "Test Suite Complete"
echo "========================================"
```

Run with:

```bash
chmod +x test-all.sh
./test-all.sh
```

---

## Service Startup Commands (Quick Reference)

### Minimal (Kong + API Gateway Only)

```bash
cd /home/kintesh/projects/go/workspace/Workflow-and-Workers/deployments/docker
docker compose up -d kong api-gateway
```

### With Database + Cache

```bash
docker compose up -d postgres redis
docker compose up -d kong api-gateway
```

### Full Stack

```bash
cd /home/kintesh/projects/go/workspace/Workflow-and-Workers/deployments/docker
docker compose up -d
```

### Individual Service Commands

```bash
# Start specific service
docker compose up -d kong
docker compose up -d api-gateway
docker compose up -d zeebe
docker compose up -d postgres
docker compose up -d redis
docker compose up -d elasticsearch
docker compose up -d keycloak
docker compose up -d jaeger
docker compose up -d camunda-workers
docker compose up -d ollama

# View logs
docker compose logs -f kong
docker compose logs -f api-gateway
docker compose logs -f zeebe

# Stop all
docker compose down

# Restart Kong (reload config)
docker compose restart kong
```

---

## Network Architecture

```
                                    INTERNET
                                        |
                                        v
                                  +-----------+
                                  |  :8000    |  Kong Proxy
                                  |  Kong     |
                                  +-----------+
                                        |
                    +-------------------+-------------------+
                    |                   |                   |
               /health              /api               /internal
               (public)           (public)           (internal)
                    |                   |                   |
                    v                   v                   v
              +---------+         +---------+         +---------+
              | 8080   | <----- | 8080   |         | 8080   |
              | api-   |         | api-   |         | api-   |
              | gateway |         | gateway |         | gateway |
              +---------+         +---------+         +---------+
                    |                   |                   |
                    +-------------------+-------------------+
                                        |
                                        v
                                  +---------+
                                  | Zeebe   |
                                  | :26500  |
                                  +---------+
```

---

## Manual Verification Checklist

### Infrastructure

- [ ] Kong running on port 8000
- [ ] Kong Admin on port 8001
- [ ] API Gateway container healthy
- [ ] PostgreSQL running
- [ ] Redis running
- [ ] Elasticsearch running
- [ ] Keycloak running on 8180
- [ ] Zeebe running on 26500

### Kong Configuration

- [ ] Health endpoint returns 200
- [ ] CORS preflight works
- [ ] Rate limit headers present
- [ ] Correlation ID present
- [ ] Security headers present
- [ ] Admin routes blocked (404)
- [ ] Internal routes IP-protected
- [ ] Request size limit works

### Backend Integration

- [ ] Backend reachable via Kong
- [ ] Headers pass through correctly
- [ ] CORS handled by Kong only

---

## Troubleshooting

### Check All Logs

```bash
docker compose logs -f
```

### Check Specific Service

```bash
docker compose logs kong --tail 100
docker compose logs api-gateway --tail 100
docker compose logs zeebe --tail 100
```

### Restart Services

```bash
# Restart Kong (reloads config)
docker compose restart kong

# Restart API Gateway
docker compose restart api-gateway

# Restart all
docker compose restart
```

### Verify Kong Admin API

```bash
# Get Kong config
curl -s http://localhost:8001/config | jq

# Get routes
curl -s http://localhost:8001/routes | jq

# Get plugins
curl -s http://localhost:8001/plugins | jq

# Get services
curl -s http://localhost:8001/services | jq
```

### Network Issues

```bash
# Check container network
docker inspect kong | grep -A 20 "Networks"

# Test connectivity between containers
docker exec kong ping -c 1 api-gateway
docker exec api-gateway ping -c 1 postgres
```

---

## Expected Results Summary

| Test                | Expected Status | Key Headers                                      |
| ------------------- | --------------- | ------------------------------------------------ |
| Kong health         | 200             | -                                                |
| /health via Kong    | 200             | X-Request-ID, X-Frame-Options, X-RateLimit-Limit |
| OPTIONS (preflight) | 200             | CORS headers                                     |
| /api/admin          | 404             | -                                                |
| Internal /internal  | 200/403         | IP dependent                                     |
| Rate limit exceeded | 429             | X-RateLimit-Remaining: 0                         |
| Request >5MB        | 413             | -                                                |

---

## Cleanup

```bash
# Stop all services
cd /home/kintesh/projects/go/workspace/Workflow-and-Workers/deployments/docker
docker compose down

# Remove volumes (WARNING: deletes data)
docker compose down -v

# Remove images
docker compose down --rmi local
```
