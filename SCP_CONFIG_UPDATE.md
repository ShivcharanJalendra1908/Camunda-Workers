# SCP Config Update Guide

## Overview
Copy the updated config file from your LOCAL machine to the REMOTE EC2 server using SCP, with backup of old config.

**EC2 Details:**
- Public IP: `34.224.27.158`
- SSH Key: `camunda-dev.pem`
- User: `ubuntu`

---

## Step 1: Backup Old Config on EC2

SSH to your EC2 instance and rename the current config file:

```bash
ssh -i camunda-dev.pem ubuntu@34.224.27.158
```

Then rename the old config:

```bash
cp /home/ubuntu/Workflow-and-Workers/configs/config.yaml /home/ubuntu/Workflow-and-Workers/configs/config-old.yaml
```

Verify the backup was created:

```bash
ls -la /home/ubuntu/Workflow-and-Workers/configs/
```

Expected output should show both files:
```
-rw-rw-r-- 1 ubuntu ubuntu  XXXX May  1 12:00 config.yaml
-rw-rw-r-- 1 ubuntu ubuntu  XXXX May  1 12:00 config-old.yaml
```

Exit SSH:

```bash
exit
```

---

## Step 2: Copy New Config via SCP

From YOUR LOCAL machine (NOT SSH), run:

```bash
scp -i camunda-dev.pem /home/kintesh/projects/go/workspace/Workflow-and-Workers/configs/config.yaml ubuntu@34.224.27.158:/home/ubuntu/Workflow-and-Workers/configs/config.yaml
```

You'll see a progress indicator:
```
config.yaml  100%  XXXX   XX.XKB/s   00:00
```

---

## Step 3: Verify Config on EC2

SSH back to EC2:

```bash
ssh -i camunda-dev.pem ubuntu@34.224.27.158
```

Verify the new config is in place:

```bash
grep -n "dev.lemici.com" /home/ubuntu/Workflow-and-Workers/configs/config.yaml
grep -n "us-dev-api.lemici.com" /home/ubuntu/Workflow-and-Workers/configs/config.yaml
```

Expected output:
```
31:        - "https://dev.lemici.com"
32:        - "https://d595hydlunw5u.cloudfront.net"
34:        - "https://us-dev-api.lemici.com"
69:      url: "https://us-dev-api.lemici.com"
75:      publicBaseUrl: "https://us-dev-api.lemici.com"
76:      redirectUrl: "https://us-dev-api.lemici.com/api/v1/auth/callback"
77:      post_login_redirect_uri: "https://dev.lemici.com/dashboard"
78:      login_redirect_uri: "https://dev.lemici.com/login"
79:      post_logout_redirect_uri: "https://dev.lemici.com/"
```

---

## Step 4: Restart Containers

Still on EC2, restart both containers to pick up new config:

```bash
docker restart api-gateway camunda-workers
```

Wait 10 seconds for containers to start:

```bash
sleep 10
```

Verify they're running:

```bash
docker ps | grep -E "api-gateway|camunda-workers"
```

Expected output:
```
63084ccaaf6e   backend-gateway:latest    "/app/api-gateway"    X seconds ago   Up X seconds (healthy)   0.0.0.0:8080->8080/tcp   api-gateway
4bff3e144214   backend-worker:latest     "./worker-manager"     X seconds ago   Up X seconds (healthy)   camunda-workers
```

---

## Step 5: Verify Container Config

Check that the container now has the updated config:

```bash
docker exec api-gateway grep "dev.lemici.com" /app/configs/config.yaml
```

Expected output:
```
- "https://dev.lemici.com"
post_login_redirect_uri: "https://dev.lemici.com/dashboard"
login_redirect_uri: "https://dev.lemici.com/login"
post_logout_redirect_uri: "https://dev.lemici.com/"
```

Also check session config:

```bash
docker exec api-gateway grep -A3 "session:" /app/configs/config.yaml
```

Expected output:
```
  session:
    ttl: 86400000
    cookieName: "session_id"
    domain: ".lemici.com"
    secure: true
    httpOnly: true
    sameSite: "Lax"
```

---

## Step 6: Test Authentication Flow

1. Clear browser cookies for all related domains
2. Go to `https://dev.lemici.com`
3. Attempt login
4. Check Network tab in browser dev tools:
   - After callback, `Location` header should redirect to `https://dev.lemici.com/` (NOT cloudfront)
   - Cookie `session_id` should have `Domain: .lemici.com` and `SameSite: Lax`

---

## Troubleshooting

### If SCP fails with "Permission denied":
```bash
# Ensure key has correct permissions
chmod 400 camunda-dev.pem

# Try with explicit key path
scp -i camunda-dev.pem /home/kintesh/projects/go/workspace/Workflow-and-Workers/configs/config.yaml ubuntu@34.224.27.158:/home/ubuntu/Workflow-and-Workers/configs/config.yaml
```

### If container doesn't restart:
```bash
# Force stop and start
docker stop api-gateway camunda-workers
docker start api-gateway camunda-workers
```

### If still showing old config in container:
```bash
# Check the mount point
docker inspect api-gateway | grep -A5 Mounts

# Check file permissions
ls -la /home/ubuntu/Workflow-and-Workers/configs/
```

---

## Quick Reference: All Commands

```bash
# Step 1: SSH and backup old config
ssh -i camunda-dev.pem ubuntu@34.224.27.158
cp /home/ubuntu/Workflow-and-Workers/configs/config.yaml /home/ubuntu/Workflow-and-Workers/configs/config-old.yaml
exit

# Step 2: SCP from local to remote
scp -i camunda-dev.pem /home/kintesh/projects/go/workspace/Workflow-and-Workers/configs/config.yaml ubuntu@34.224.27.158:/home/ubuntu/Workflow-and-Workers/configs/config.yaml

# Step 3-5: SSH back and restart
ssh -i camunda-dev.pem ubuntu@34.224.27.158
grep "dev.lemici.com" /home/ubuntu/Workflow-and-Workers/configs/config.yaml
docker restart api-gateway camunda-workers
sleep 10
docker exec api-gateway grep "dev.lemici.com" /app/configs/config.yaml
exit
```

---

## Files Involved

| File | Location | Purpose |
|------|----------|---------|
| `config.yaml` | Local: `/home/kintesh/projects/go/workspace/Workflow-and-Workers/configs/` | Updated config with subdomain URLs |
| `config-old.yaml` | Remote: `/home/ubuntu/Workflow-and-Workers/configs/` | Backup of old config |
| `config.yaml` | Remote: `/home/ubuntu/Workflow-and-Workers/configs/` | Target - will be replaced by SCP |
| `camunda-dev.pem` | Local: Current directory | SSH key for EC2 access |

---

## Notes
- No binary rebuild required
- Config changes take effect after container restart
- Old config is backed up as `config-old.yaml`
- Both `api-gateway` and `camunda-workers` must be restarted
- Always use `-i camunda-dev.pem` for SSH/SCP commands
