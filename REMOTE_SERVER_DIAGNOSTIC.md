# REMOTE SERVER DIAGNOSTIC REPORT

## Date
2026-05-01

## Problem
Authentication callback redirecting to old CloudFront URL (`d595hydlunw5u.cloudfront.net`) instead of `dev.lemici.com`, breaking cookie persistence flow.

## Root Cause: CONFIRMED

The config file on the remote EC2 server is **NOT updated** with the latest changes.

## Evidence

### 1. Docker Mount Configuration
```
Mount Source: /home/ubuntu/Workflow-and-Workers/configs
Mount Destination: /app/configs
Mode: read-only
```

### 2. Remote Container Config Shows OLD Values
```bash
docker exec api-gateway cat /app/configs/config.yaml | head -50
```

**Output:**
```yaml
cors:
  allowOrigins:
    - "http://localhost:3000"
    - "http://localhost:5173"
    - "https://d3c34598mt7qdx.cloudfront.net"
    - "https://lemici.com"
    - "https://d595hydlunw5u.cloudfront.net"
    - "http://34.224.27.158:3005"
```

### 3. Search for dev.lemici.com in Container
```bash
docker exec api-gateway grep -r "dev.lemici.com" /app/
```
**Result:** No matches found - confirms old config is loaded.

## Container Status (from docker ps)
| Container | Image | Status | Uptime |
|-----------|-------|--------|--------|
| api-gateway | backend-gateway:latest | Up 10 min (healthy) | Recently restarted |
| camunda-workers | backend-worker:latest | Up 25 hours | OLD - needs restart |
| operate-ui | operate-ui:latest | Up 25 hours (unhealthy) | |
| keycloak | keycloak:23.0 | Up 25 hours (healthy) | |
| zeebe | zeebe:8.3.9 | Up 25 hours (healthy) | |
| redis | redis:7-alpine | Up 25 hours (healthy) | |
| postgres | postgres:15-alpine | Up 25 hours (healthy) | |
| elasticsearch | elasticsearch:8.9.0 | Up 25 hours (healthy) | |
| ollama | ollama:latest | Up 25 hours | |
| mailhog | mailhog:latest | Up 25 hours | |
| nginx | nginx:latest | Up 4 days | |

## Required Fixes

### Fix 1: Update Remote Config File
The file `/home/ubuntu/Workflow-and-Workers/configs/config.yaml` on the remote server needs to be updated with the subdomain-based configuration.

### Fix 2: Restart Containers
After config update:
```bash
docker restart api-gateway camunda-workers
```

### Fix 3: Verify
```bash
docker exec api-gateway grep "dev.lemici.com" /app/configs/config.yaml
docker exec api-gateway grep "us-dev-api.lemici.com" /app/configs/config.yaml
```

## Next Steps
1. Update config.yaml on remote server
2. Restart containers
3. Test authentication flow
4. Verify cookies are set with Domain=.lemici.com

## Files Changed Locally (Need to be Applied Remotely)
- `/home/kintesh/projects/go/workspace/Workflow-and-Workers/configs/config.yaml` - Subdomain-based auth config
- `/home/kintesh/projects/go/workspace/Workflow-and-Workers/configs/config.dev.yaml` - Dev environment config
- `/home/kintesh/projects/go/workspace/Workflow-and-Workers/internal/api/handlers/workflow_handler.go` - Cookie settings

## Notes
- Binary rebuild NOT required - config is mounted via volume
- Only need to update config file and restart containers
- nginx config may also need review for redirect interception
