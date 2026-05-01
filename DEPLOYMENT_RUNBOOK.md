# DEPLOYMENT RUNBOOK

## Problem Summary

Code changes have been pushed to git but are NOT reflecting on the EC2 instance because:
- Containers use **pre-built Docker images** from ECR
- ECR images were **NOT rebuilt** with the latest code
- **Only pushing to `prod` branch triggers the CI/CD pipeline** that rebuilds and deploys images

## Architecture Overview

```
Local Code → Git Push → GitHub Actions (CI/CD) → ECR → EC2 Docker Compose
                                    ↑
                          Only triggers on: push to `prod` branch
```

### Current Image Sources on EC2

| Service | ECR Image |
|---------|-----------|
| api-gateway | `177925987307.dkr.ecr.us-east-1.amazonaws.com/backend-gateway-dev:latest` |
| camunda-workers | `177925987307.dkr.ecr.us-east-1.amazonaws.com/backend-worker-dev:latest` |
| operate-ui | `177925987307.dkr.ecr.us-east-1.amazonaws.com/operate-ui:latest` |

---

## Option 1: Push to `prod` Branch (Recommended - Automated)

This triggers the full CI/CD pipeline that:
1. Builds Go binaries
2. Builds Docker images
3. Pushes to ECR
4. SSHs to EC2 and deploys

### Steps:

```bash
# From local machine
cd /home/kintesh/projects/go/workspace/Workflow-and-Workers

# Ensure all changes are committed
git add -A
git commit -m "fix: subdomain-based auth redirect to dev.lemici.com"

# Push to prod branch
git push origin main:prod
```

Wait ~5 minutes for the pipeline to complete. The EC2 deployment is automated.

---

## Option 2: Manual Local Build + ECR Push + Deploy

Use this if you want to control the process manually.

### Prerequisites

1. **Docker installed locally**
2. **AWS CLI configured** with credentials that have ECR push permissions
3. **AWS CLI configured** with EC2 SSH access

### Step 1: Build Docker Images Locally

```bash
cd /home/kintesh/projects/go/workspace/Workflow-and-Workers

# Build gateway image
docker build -t backend-gateway-dev:latest \
  -f deployments/docker/Dockerfile.gateway .

# Build worker image
docker build -t backend-worker-dev:latest \
  -f deployments/docker/Dockerfile.worker .
```

### Step 2: Authenticate with ECR

```bash
# Login to ECR
aws ecr get-login-password --region us-east-1 \
  | docker login --username AWS \
  --password-stdin 177925987307.dkr.ecr.us-east-1.amazonaws.com
```

### Step 3: Tag and Push to ECR

```bash
# Tag gateway image
docker tag backend-gateway-dev:latest \
  177925987307.dkr.ecr.us-east-1.amazonaws.com/backend-gateway-dev:latest

# Tag worker image
docker tag backend-worker-dev:latest \
  177925987307.dkr.ecr.us-east-1.amazonaws.com/backend-worker-dev:latest

# Push gateway
docker push 177925987307.dkr.ecr.us-east-1.amazonaws.com/backend-gateway-dev:latest

# Push worker
docker push 177925987307.dkr.ecr.us-east-1.amazonaws.com/backend-worker-dev:latest
```

### Step 4: Deploy to EC2

```bash
# SSH to EC2
ssh -i camunda-dev.pem ubuntu@34.224.27.158

# Navigate to compose directory
cd ~/Workflow-and-Workers/deployments/docker

# Login to ECR on EC2
aws ecr get-login-password --region us-east-1 \
  | docker login --username AWS \
  --password-stdin 177925987307.dkr.ecr.us-east-1.amazonaws.com

# Pull new images and restart
docker compose down
docker compose pull
docker compose up -d --remove-orphans

# Verify
docker ps
```

---

## Option 3: Quick Fix - Edit Config on EC2 (No Rebuild Needed)

If the hardcoded redirect is in the **Go binary** (not config), this won't work. But if it's in config, you can fix it without rebuild.

### SSH to EC2

```bash
ssh -i camunda-dev.pem ubuntu@34.224.27.158
```

### Edit Config

```bash
nano /home/ubuntu/Workflow-and-Workers/configs/config.yaml
```

Find and change this section (around line 79):

**FROM:**
```
    post_logout_redirect_uri: "https://d595hydlunw5u.cloudfront.net/"
```

**TO:**
```
    post_logout_redirect_uri: "https://dev.lemici.com/"
```

Also check and update:
```
    post_login_redirect_uri: "https://dev.lemici.com/dashboard"
    login_redirect_uri: "https://dev.lemici.com/login"
```

### Restart Containers

```bash
docker restart api-gateway camunda-workers
```

---

## Option 4: Hardcoded Redirect Fix (Requires Code Change + Rebuild)

The grep output showed this line in the Go code:
```
workflow_handler.go: redirectURL = "https://d595hydlunw5u.cloudfront.net/"
```

### Fix the Code

1. **Find the exact line:**
   ```bash
   grep -n "d595hydlunw5u.cloudfront.net" /home/kintesh/projects/go/workspace/Workflow-and-Workers/internal/api/handlers/workflow_handler.go
   ```

2. **Edit the file** - Replace hardcoded URL with config value or `dev.lemici.com`

3. **Commit and push to `prod`** (triggers CI/CD):
   ```bash
   git add internal/api/handlers/workflow_handler.go
   git commit -m "fix: replace hardcoded cloudfront redirect with dev.lemici.com"
   git push origin main:prod
   ```

---

## Verification After Deploy

### Check Container is Running

```bash
ssh -i camunda-dev.pem ubuntu@34.224.27.158
docker ps | grep -E "api-gateway|camunda-workers"
```

### Check Config Inside Container

```bash
docker exec api-gateway grep "dev.lemici.com" /app/configs/config.yaml
```

### Check Logs for Errors

```bash
docker logs api-gateway --tail 50
docker logs camunda-workers --tail 50
```

### Test Auth Flow

1. Clear browser cookies
2. Go to `https://dev.lemici.com`
3. Login
4. Check Network tab - `Location` header should redirect to `https://dev.lemici.com/`

---

## Troubleshooting

### Container Fails to Start

```bash
# Check logs
docker logs api-gateway --tail 100

# Common errors:
# - YAML parse error: Check config.yaml syntax
# - Port already in use: docker ps to find conflicts
# - OOM killed: docker inspect <container> | grep -i oom
```

### Images Not Pulled from ECR

```bash
# Force pull specific image
docker pull 177925987307.dkr.ecr.us-east-1.amazonaws.com/backend-gateway-dev:latest

# Verify image age
docker images | grep backend
```

### EC2 Can't Login to ECR

```bash
# Check AWS credentials
aws sts get-caller-identity

# Check IAM role has ecr:GetAuthorizationToken permission
aws ecr get-login-password --region us-east-1
```

---

## Quick Reference Commands

| Action | Command |
|--------|---------|
| Build images locally | `docker build -t backend-gateway-dev:latest -f deployments/docker/Dockerfile.gateway .` |
| Login to ECR | `aws ecr get-login-password --region us-east-1 \| docker login --username AWS --password-stdin 177925987307.dkr.ecr.us-east-1.amazonaws.com` |
| Push to ECR | `docker push 177925987307.dkr.ecr.us-east-1.amazonaws.com/backend-gateway-dev:latest` |
| SSH to EC2 | `ssh -i camunda-dev.pem ubuntu@34.224.27.158` |
| Deploy on EC2 | `cd ~/Workflow-and-Workers/deployments/docker && docker compose pull && docker compose up -d` |
| Trigger CI/CD | `git push origin main:prod` |
| Check container logs | `docker logs api-gateway --tail 50` |

---

## Files Modified That Require Rebuild

| File | Requires Rebuild? | Why |
|------|-------------------|-----|
| `configs/config.yaml` | ❌ No | Volume-mounted, read at runtime |
| `internal/api/handlers/workflow_handler.go` | ✅ Yes | Compiled into binary |
| `internal/workers/auth/keycloak-signin/config.go` | ✅ Yes | Compiled into binary |
| `deployments/docker/docker-compose.yml` | ❌ No | Only if changing image/service config |

---

## Recommended Workflow

1. **For config changes:** Edit config.yaml on EC2 → restart containers
2. **For code changes:** Push to `prod` branch → CI/CD handles everything
3. **For quick testing:** Build locally → push to ECR → manual deploy on EC2
