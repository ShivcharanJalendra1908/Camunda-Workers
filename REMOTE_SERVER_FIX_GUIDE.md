# REMOTE SERVER FIX GUIDE

## Problem
Remote EC2 server has OLD config.yaml with CloudFront URLs, causing authentication redirects to `d595hydlunw5u.cloudfront.net` instead of `dev.lemici.com`.

## Solution: Update Config on Remote Server

### Option 1: Direct SSH Edit (Recommended)

```bash
# SSH to your EC2 instance
ssh ubuntu@YOUR_EC2_IP

# Backup current config
cp /home/ubuntu/Workflow-and-Workers/configs/config.yaml /home/ubuntu/Workflow-and-Workers/configs/config.yaml.bak

# Open config file for editing
nano /home/ubuntu/Workflow-and-Workers/configs/config.yaml
```

**Then apply these changes:**

#### Change 1: Add dev.lemici.com and us-dev-api.lemici.com to CORS
Find this section:
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

Add these two lines to allowOrigins:
```yaml
      - "https://dev.lemici.com"
      - "https://us-dev-api.lemici.com"
```

#### Change 2: Update Keycloak URLs
Find this section:
```yaml
  keycloak:
    url: "https://d595hydlunw5u.cloudfront.net"
    realm: "camunda-platform"
    client_id: "lemici-frontend"
    client_secret: "client-secret"
    admin_client_id: "worker-client"                     
    admin_client_secret: "yNHfb6FR4ZGIsUyi27pSpfsAbxEfquhS"
    publicBaseUrl: "https://d595hydlunw5u.cloudfront.net"
    redirectUrl: "https://d595hydlunw5u.cloudfront.net/api/v1/auth/callback"
```

Change to:
```yaml
  keycloak:
    url: "https://us-dev-api.lemici.com"
    realm: "camunda-platform"
    client_id: "lemici-frontend"
    client_secret: "client-secret"
    admin_client_id: "worker-client"                     
    admin_client_secret: "yNHfb6FR4ZGIsUyi27pSpfsAbxEfquhS"
    publicBaseUrl: "https://us-dev-api.lemici.com"
    redirectUrl: "https://us-dev-api.lemici.com/api/v1/auth/callback"
    post_login_redirect_uri: "https://dev.lemici.com/dashboard"
    login_redirect_uri: "https://dev.lemici.com/login"
    post_logout_redirect_uri: "https://dev.lemici.com/"
```

#### Change 3: Update Session Configuration
Find this section:
```yaml
  session:
    ttl: 86400000
    cookieName: "session_id"
    secure: true
    httpOnly: true
    sameSite: "None"
```

Change to:
```yaml
  session:
    ttl: 86400000
    cookieName: "session_id"
    domain: ".lemici.com"
    secure: true
    httpOnly: true
    sameSite: "Lax"
```

#### Save and Exit
- Press `Ctrl + X`
- Press `Y` to save
- Press `Enter` to confirm

### Option 2: Copy from Local Machine

```bash
# From your LOCAL machine (not SSH):
scp /home/kintesh/projects/go/workspace/Workflow-and-Workers/configs/config.yaml ubuntu@YOUR_EC2_IP:/home/ubuntu/Workflow-and-Workers/configs/config.yaml
```

---

## Step 2: Restart Containers

```bash
# SSH to EC2
ssh ubuntu@YOUR_EC2_IP

# Restart both containers to pick up new config
docker restart api-gateway camunda-workers

# Wait 10 seconds
sleep 10

# Check they're running
docker ps | grep -E "api-gateway|camunda-workers"
```

---

## Step 3: Verify Config Update

```bash
# Check if dev.lemici.com is now in the config
docker exec api-gateway grep "dev.lemici.com" /app/configs/config.yaml

# Check if us-dev-api.lemici.com is now in the config
docker exec api-gateway grep "us-dev-api.lemici.com" /app/configs/config.yaml

# Check session domain is set
docker exec api-gateway grep "domain:" /app/configs/config.yaml

# Check sameSite is Lax
docker exec api-gateway grep "sameSite" /app/configs/config.yaml
```

Expected output:
```
- "https://dev.lemici.com"
post_login_redirect_uri: "https://dev.lemici.com/dashboard"
login_redirect_uri: "https://dev.lemici.com/login"
post_logout_redirect_uri: "https://dev.lemici.com/"
url: "https://us-dev-api.lemici.com"
publicBaseUrl: "https://us-dev-api.lemici.com"
redirectUrl: "https://us-dev-api.lemici.com/api/v1/auth/callback"
domain: ".lemici.com"
sameSite: "Lax"
```

---

## Step 4: Test Authentication

1. Clear browser cookies for all domains:
   - dev.lemici.com
   - us-dev-api.lemici.com
   - d595hydlunw5u.cloudfront.net

2. Open browser and go to: `https://dev.lemici.com`

3. Try to login

4. Check browser dev tools Network tab:
   - After login, should redirect to `https://dev.lemici.com/` (NOT cloudfront)
   - Check response headers for `Location:` header

5. Check cookies:
   - Should see `session_id` cookie with Domain: `.lemici.com`
   - Should see `SameSite: Lax`

---

## Troubleshooting

### If still redirecting to cloudfront:
```bash
# Check if config was actually updated
cat /home/ubuntu/Workflow-and-Workers/configs/config.yaml | grep "dev.lemici.com"

# If not found, the file wasn't updated - repeat Option 1 or 2 above
```

### If containers don't restart:
```bash
# Force stop and start
docker stop api-gateway camunda-workers
docker start api-gateway camunda-workers
```

### Check container logs for errors:
```bash
docker logs api-gateway --tail 50
docker logs camunda-workers --tail 50
```

---

## Notes
- Config is mounted as read-only volume, so editing the host file will take effect after container restart
- No binary rebuild needed - only config changes required
- Both api-gateway AND camunda-workers need restart to pick up new config
