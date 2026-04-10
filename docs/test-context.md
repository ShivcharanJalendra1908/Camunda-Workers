# Auth Flow Testing — Session & CSRF Validation
## Chat Context & Test State

### What We're Testing
Testing the following auth flow features on the **remote EC2 server** (`ubuntu@ip-172-31-26-176`) via port 8085:
1. **Session creation** — After OAuth2/OIDC login, verify session is created in Redis with all fields
2. **Session TTL / Idle expiry** — Sliding window (30 min) resets on each valid request
3. **Absolute expiry** — Hard limit (24h) that never extends
4. **CSRF token issuance** — `CSRFTokenIssuer` middleware generates token on GET requests
5. **CSRF enforcement** — `CSRFProtection` middleware blocks POST without valid token (403)
6. **Session fixation prevention** — Cryptographically secure 256-bit session IDs

---

### Code Changes Made (feature/backend-security branch)

**File: `internal/common/auth/session/store.go`**

Added JSON tags to the `Session` struct so all fields serialize correctly to Redis:

```go
type Session struct {
    SessionID         string    `json:"session_id"`
    UserID            string    `json:"user_id"`
    CreatedAt         time.Time `json:"created_at"`
    ExpiresAt         time.Time `json:"expires_at"`
    AbsoluteExpiresAt time.Time `json:"absolute_expires_at"`
    Version           int       `json:"version"`
    CSRFToken         string    `json:"csrf_token"`
    UserAgent         string    `json:"user_agent"`
    IP                string    `json:"ip"`
}
```

**Before fix (without JSON tags):** Redis stored `{"SessionID":"...","UserID":"...","CreatedAt":"0001-01-01T00:00:00Z","AbsoluteExpiresAt":"0001-01-01T00:00:00Z"}` with `CSRFToken` completely missing.

**After fix (with JSON tags):** All fields should serialize correctly, including timestamps and CSRF token.

---

### How We Set Up the Test Environment

**Goal:** Test feature branch code on the same remote server as MVP **without affecting** running MVP containers.

**Architecture:**
- **MVP:** Docker containers (`api-gateway` on port 8080, `camunda-workers` from ECR image)
- **Feature test:** Standalone `api-gateway-security` binary on port 8085 + `worker-test-feature` Docker container from locally built image
- **Shared:** Redis, PostgreSQL, Zeebe, Keycloak, Elasticsearch (same instances for both)

**Step-by-step setup:**

```bash
# 1. Fetch feature branch code on remote server
cd ~/Workflow-and-Workers && git fetch origin && git reset --hard origin/feature/backend-security

# 2. Build feature gateway binary
go build -o api-gateway-security ./cmd/api-gateway/

# 3. Build feature worker Docker image
docker build -t backend-worker:test -f deployments/docker/Dockerfile.worker .

# 4. Start feature gateway on port 8085
API_PORT=8085 ./api-gateway-security &

# 5. Start feature worker container
docker rm -f worker-test-feature 2>/dev/null
docker run -d --name worker-test-feature --network host --env-file .env \
  -e KEYCLOAK_URL=https://us-dev-api.lemici.com \
  -e KEYCLOAK_PUBLIC_URL=https://us-dev-api.lemici.com \
  -e TEMPLATE_REGISTRY_PATH=configs/templates \
  backend-worker:test
```

**Why 3 env var overrides:**
| Env Var | `.env` value | Why override |
|---------|-------------|-------------|
| `KEYCLOAK_URL` | `http://localhost:8180` | Remote Keycloak returns `https://us-dev-api.lemici.com` as issuer — OIDC mismatch |
| `KEYCLOAK_PUBLIC_URL` | (empty) | Must match Keycloak public URL for token validation |
| `TEMPLATE_REGISTRY_PATH` | `configs/templates.json` | Code expects a directory, `.env` points to a file |

---

### How We Run Tests

```bash
# 1. Initiate login
curl -s -X POST http://localhost:8085/api/v1/auth/login \
  -H "Content-Type: application/json" -d "{}"

# 2. Open returned OAuth2 URL in browser → log in with Keycloak → callback

# 3. Check sessions in Redis
docker exec -it d136b64f8446 redis-cli KEYS "session:*"

# 4. Inspect session data
docker exec -it d136b64f8446 redis-cli GET "session:<ID>" | python3 -m json.tool

# 5. Get CSRF token
curl -s -v -b 'session_id=<ID>' http://localhost:8085/api/v1/franchises/home 2>&1 | grep -i "x-csrf"

# 6. Test CSRF enforcement (POST without token → 403)
curl -s -X POST http://localhost:8085/api/v1/franchises/favorite/123 \
  -b 'session_id=<ID>' -H "Content-Type: application/json" -d "{}"
```

---

### Test Results So Far

| Test | Status | Result |
|------|--------|--------|
| Login initiation (OAuth2 URL generation) | ✅ PASS | Returns valid authorization URL with PKCE |
| CSRF token issuance (GET request) | ✅ PASS | `X-CSRF-Token` header + `Set-Cookie: csrf_token=...` returned |
| CSRF token stored in Redis | ✅ PASS | `csrf:<sessionID>` key exists in Redis |
| Session creation in Redis | ❌ FAIL | Session not created — token exchange failed |
| Session fields validation | ❌ BLOCKED | No session exists to inspect |
| CSRF enforcement (POST without token) | ❌ BLOCKED | No valid session to test with |
| Session idle/sliding expiry | ❌ BLOCKED | No session exists |
| Session absolute expiry | ❌ BLOCKED | No session exists |

---

### Blockers

**1. TOKEN_EXCHANGE_FAILED — Feature worker can't complete login**

The `keycloak-signin` worker in `worker-test-feature` picked up the callback task but failed:
```
{"errorCode":"TOKEN_EXCHANGE_FAILED","errorMessage":"Failed to exchange authorization code"}
```

**Root cause:** The `.env` has `KEYCLOAK_CLIENT_SECRET=worker-secret` which is the **admin client secret** for `worker-client`. The signin flow uses `lemici-frontend` client which likely has a **different client secret**. The feature worker doesn't have the correct client secret for the frontend OIDC client.

**2. Worker task contention — Both MVP and feature workers poll the same Zeebe broker**

Since both `camunda-workers` (MVP) and `worker-test-feature` (feature) poll the same Zeebe broker, either worker can pick up tasks. This means:
- The callback task was picked up by the **feature worker** (which has the bug)
- Even if it worked, sessions would be created by whichever worker grabs the task first
- We can't guarantee the feature worker handles the session creation

**3. Callback routing — Login initiated on 8085, callback goes through CloudFront → MVP gateway**

The OAuth2 redirect URL is hardcoded to `https://d595hydlunw5u.cloudfront.net/callback` which routes to the MVP gateway (port 8080), not our feature gateway (port 8085). The session-manager worker then creates the session, but it's using the **old Docker image** on MVP that doesn't have our `store.go` fix.

---

### What Needs to Happen Next

**Option A: Fix the client secret**
Get the correct `KEYCLOAK_CLIENT_SECRET` for `lemici-frontend` from Keycloak admin console and pass it to the feature worker.

**Option B: Test on MVP directly**
Push changes to MVP, rebuild Docker images via CI pipeline, and test in production-like environment. This is what the user decided to do.

**Option C: Isolate Zeebe tasks**
Run a separate Zeebe broker for feature testing (overkill for now).

---

### Current State

- **Feature branch:** `feature/backend-security` has two commits:
  1. `4c0718e` — Added JSON tags to `Session` struct
  2. `e0dc10e` — Cross-origin cookie fix (SameSite=None) + dynamic login redirects
- **Pending changes (not yet committed):** Fixed remaining `c.SetCookie()` calls in `redirectToLogin`, `redirectToLoginWithError`, and logout handler to use `http.SetCookie()` with `SameSite=None`. Also added `login_redirect_uri` config field.
- **Remote server:** `api-gateway-security` binary built, `worker-test-feature` container running
- **Feature worker:** Running but failing at token exchange
- **MVP containers:** Running normally, unaffected
- **Redis:** Clean (flushed during testing), no sessions currently
- **Status:** Testing paused — will resume on MVP branch with proper deployment pipeline
