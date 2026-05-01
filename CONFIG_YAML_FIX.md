# CONFIG YAML FIX GUIDE

## Problem
Container crashes with YAML parse error after SCP config update:
```
panic: Failed to load config: error reading base config: While parsing config: yaml: line 12: did not find expected key
```

## Root Cause
SCP transfer likely corrupted YAML syntax (tabs vs spaces, encoding issues).

---

## Step 1: Restore Backup Config

SSH to EC2:
```bash
ssh -i camunda-dev.pem ubuntu@34.224.27.158
```

Restore old working config:
```bash
cp /home/ubuntu/Workflow-and-Workers/configs/config-old.yaml /home/ubuntu/Workflow-and-Workers/configs/config.yaml
```

---

## Step 2: Start Containers with Old Config

```bash
docker start api-gateway camunda-workers
```

Wait 10 seconds:
```bash
sleep 10
```

Verify running:
```bash
docker ps | grep -E "api-gateway|camunda-workers"
```

---

## Step 3: Edit Config Manually (Safe Method)

Open config in nano:
```bash
nano /home/ubuntu/Workflow-and-Workers/configs/config.yaml
```

### Make These 3 Changes Only:

#### Change 1: CORS Origins (Around line 24-34)

FIND:
```
    allowOrigins:
      - "http://localhost:3000"
      - "http://localhost:5173"
      - "https://d3c34598mt7qdx.cloudfront.net"
      - "https://lemici.com"
      - "https://d595hydlunw5u.cloudfront.net"
      - "http://34.224.27.158:3005"
```

CHANGE TO:
```
    allowOrigins:
      - "http://localhost:3000"
      - "http://localhost:5173"
      - "https://d3c34598mt7qdx.cloudfront.net"
      - "https://www.lemici.com"
      - "https://lemici.com"
      - "https://www.lemici.com"
      - "https://dev.lemici.com"
      - "https://d595hydlunw5u.cloudfront.net"
      - "http://34.224.27.158:3005"
      - "https://us-dev-api.lemici.com"
```

#### Change 2: Keycloak URLs (Around line 68-79)

FIND:
```
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

CHANGE TO:
```
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

#### Change 3: Session Config (Around line 82-88)

FIND:
```
    session:
      ttl: 86400000
      cookieName: "session_id"
      secure: true
      httpOnly: true
      sameSite: "None"
```

CHANGE TO:
```
    session:
      ttl: 86400000
      cookieName: "session_id"
      domain: ".lemici.com"
      secure: true
      httpOnly: true
      sameSite: "Lax"
```

### Save and Exit Nano:
- Press `Ctrl + X`
- Press `Y` to confirm save
- Press `Enter`

---

## Step 4: Validate YAML Before Restart

Check YAML syntax:
```bash
python3 -c "import yaml; yaml.safe_load(open('/home/ubuntu/Workflow-and-Workers/configs/config.yaml')); print('YAML OK')"
```

Expected output:
```
YAML OK
```

---

## Step 5: Restart Containers

```bash
docker restart api-gateway camunda-workers
```

Wait 10 seconds:
```bash
sleep 10
```

Verify running:
```bash
docker ps | grep -E "api-gateway|camunda-workers"
```

---

## Step 6: Verify New Config Loaded

Check dev.lemici.com in container:
```bash
docker exec api-gateway grep "dev.lemici.com" /app/configs/config.yaml
```

Check session domain:
```bash
docker exec api-gateway grep "domain:" /app/configs/config.yaml
```

---

## Troubleshooting

### If containers crash again:

Check error logs:
```bash
docker logs api-gateway --tail 20
```

If still YAML error:
```bash
# Check for tab characters
cat -A /home/ubuntu/Workflow-and-Workers/configs/config.yaml | head -20
```

Look for `^I` which indicates tab characters. YAML requires spaces only.

### Fix tab characters if found:
```bash
# Replace tabs with 2 spaces
sed -i 's/\t/  /g' /home/ubuntu/Workflow-and-Workers/configs/config.yaml
```

Then restart:
```bash
docker start api-gateway camunda-workers
```

---

## Quick Reference

| Action | Command |
|--------|---------|
| Restore backup | `cp config-old.yaml config.yaml` |
| Start containers | `docker start api-gateway camunda-workers` |
| Restart containers | `docker restart api-gateway camunda-workers` |
| Check YAML | `python3 -c "import yaml; yaml.safe_load(open('/home/ubuntu/Workflow-and-Workers/configs/config.yaml')); print('OK')"` |
| View logs | `docker logs api-gateway --tail 20` |
| Check container config | `docker exec api-gateway grep "dev.lemici.com" /app/configs/config.yaml` |
