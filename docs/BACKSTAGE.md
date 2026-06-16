# Backstage + Keycloak OIDC Configuration Guide

## Overview

This document covers the configuration required to integrate Backstage with Keycloak for OIDC authentication and user/group catalog synchronization.

**Environment:** Dev (US)
**Base URL:** `https://us-dev-api.lemici.com/backstage`
**Keycloak Realm:** `camunda-platform`

---

## 1. Keycloak Client Setup

### 1.1 Create/Update the `backstage` client

In the Keycloak Admin Console (`/admin/camunda-platform/console/`), navigate to **Clients** and create or update the `backstage` client with the following settings:

| Setting                      | Value               |
| ---------------------------- | ------------------- |
| Client ID                    | `backstage`         |
| Client Protocol              | `openid-connect`    |
| Client Authentication        | `On` (confidential) |
| Standard Flow Enabled        | `On`                |
| Service Accounts Enabled     | `On`                |
| Direct Access Grants Enabled | `Off`               |
| Authorization                | `Off`               |
| Root URL                     | (leave empty)       |

### 1.2 Valid Redirect URIs

```
http://localhost:7007/api/auth/oidc/handler/frame
https://us-dev-api.lemici.com/backstage/api/auth/oidc/handler/frame
```

### 1.3 Web Origins

```
http://localhost:7007
https://us-dev-api.lemici.com
```

### 1.4 Client Scopes

Ensure the following default client scopes are assigned:

- `web-origins`
- `acr`
- `profile`
- `roles`
- `email`

### 1.5 Client Attributes

| Attribute                             | Value  |
| ------------------------------------- | ------ |
| `pkce.code.challenge.method`          | `S256` |
| `post.logout.redirect.uris`           | `+`    |
| `backchannel.logout.session.required` | `true` |
| `access.token.lifespan`               | `1800` |

---

## 2. Service Account Permissions

The `backstage` client has a service account (`service-account-backstage`) used by `KeycloakOrgEntityProvider` to sync users and groups from Keycloak.

### 2.1 Required Client Roles (realm-management)

The service account **must** have these client roles from `realm-management`:

- `view-users`
- `query-users`
- `view-groups`
- `query-groups`
- **`view-realm`** (critical - without this, catalog sync returns 403)

### 2.2 How to assign via Keycloak Admin

1. Go to **Users** -> search for `service-account-backstage`
2. Go to **Role Mappings** tab
3. Under **Client Role**, select `realm-management`
4. Assign the 5 roles listed above

---

## 3. Backstage App Config (`app-config.yaml`)

Add or update the following in your Backstage `app-config.yaml`:

```yaml
app:
  baseUrl: https://us-dev-api.lemici.com/backstage

backend:
  baseUrl: https://us-dev-api.lemici.com/backstage
  listen:
    port: 7007

auth:
  providers:
    oidc:
      production:
        clientId: backstage
        clientSecret: ${AUTH_OIDC_CLIENT_SECRET}
        metadataUrl: https://<keycloak-host>/realms/camunda-platform/.well-known/openid-configuration
        authorizationUrl: https://<keycloak-host>/realms/camunda-platform/protocol/openid-connect/auth
        tokenUrl: https://<keycloak-host>/realms/camunda-platform/protocol/openid-connect/token
        userInfoUrl: https://<keycloak-host>/realms/camunda-platform/protocol/openid-connect/userinfo
        callbackUrl: https://us-dev-api.lemici.com/backstage/api/auth/oidc/handler/frame

catalog:
  providers:
    keycloakOrg:
      production:
        baseUrl: https://<keycloak-host>
        realm: camunda-platform
        clientId: backstage
        clientSecret: ${AUTH_OIDC_CLIENT_SECRET}
        userQueryFilter: "true"
        groupQueryFilter: "true"
```

Replace `<keycloak-host>` with your actual Keycloak domain (e.g., `auth.lemici.com`).

### 3.1 Environment Variables

Set these in your deployment environment (Docker, EC2, etc.):

```bash
export AUTH_OIDC_CLIENT_SECRET=<your-backstage-client-secret>
```

To retrieve the client secret from Keycloak:

1. Go to **Clients** -> `backstage`
2. Go to **Credentials** tab
3. Copy the **Client Secret**

---

## 4. Kong Routing

Ensure Kong is configured to proxy `/backstage` to the Backstage container:

| Setting     | Value                   |
| ----------- | ----------------------- |
| Service URL | `http://localhost:7007` |
| Paths       | `/backstage`            |
| Strip Path  | `false`                 |

Example Kong service configuration:

```json
{
  "name": "backstage",
  "url": "http://localhost:7007",
  "paths": ["/backstage"],
  "strip_path": false
}
```

---

## 5. Deployment Steps

### 5.1 Update Keycloak Realm

After modifying `realm-export.json`:

```bash
# Option A: Re-import via Keycloak Admin Console
# Go to Realm Settings -> Import -> Select the updated file

# Option B: Via Keycloak REST API
curl -X POST "https://<keycloak-host>/admin/realms" \
  -H "Authorization: Bearer <admin-token>" \
  -H "Content-Type: application/json" \
  -d @deployments/docker/keycloak/realm-export.json
```

### 5.2 Restart Backstage

```bash
# If using Docker
docker compose restart backstage

# If running directly
pm2 restart backstage
# or
systemctl restart backstage
```

### 5.3 Verify

1. **OIDC Login:**

   ```
   curl -I https://us-dev-api.lemici.com/backstage/api/auth/oidc/start?env=production
   ```

   Expected: `302` redirect to Keycloak login (not 500)

2. **Catalog Sync:**
   Check Backstage backend logs for successful Keycloak sync:

   ```bash
   docker logs backstage --tail 50 | grep -i keycloak
   ```

   Expected: No `403` errors, successful user/group import

3. **Full Auth Flow:**
   - Navigate to `https://us-dev-api.lemici.com/backstage`
   - Click "Sign In"
   - You should be redirected to Keycloak login
   - After authentication, you should be redirected back to Backstage

---

## 6. Troubleshooting

### 6.1 OIDC `/start` returns 500

**Cause:** Backstage OIDC provider misconfigured or Keycloak client settings mismatch.

**Fix:**

- Verify `metadataUrl` in `app-config.yaml` is reachable from the Backstage server
- Ensure `callbackUrl` matches exactly: `https://us-dev-api.lemici.com/backstage/api/auth/oidc/handler/frame`
- Check Backstage logs: `docker logs backstage --tail 100`

### 6.2 `KeycloakOrgEntityProvider` 403 error

**Cause:** Service account missing `view-realm` role from `realm-management`.

**Fix:**

- Go to Keycloak -> Users -> `service-account-backstage` -> Role Mappings
- Add `view-realm` to the `realm-management` client role mappings
- This role was added in the updated `realm-export.json`

### 6.3 Users not syncing

**Cause:** `userQueryFilter` or `groupQueryFilter` too restrictive, or service account permissions insufficient.

**Fix:**

- Verify service account has all 5 required roles (see Section 2.1)
- Set `userQueryFilter: "true"` to import all users
- Check Keycloak admin events for denied API calls

### 6.4 CORS errors in browser

**Cause:** Missing web origin in Keycloak client configuration.

**Fix:**

- Add `https://us-dev-api.lemici.com` to the backstage client's **Web Origins**
- Ensure `Access-Control-Allow-Origin` header is present in Keycloak responses

---

## 7. Reference: Keycloak Realm Export Locations

| File                                              | Description                                                |
| ------------------------------------------------- | ---------------------------------------------------------- |
| `deployments/docker/keycloak/realm-export.json`   | Current production realm export (includes Backstage fixes) |
| `deployments/docker/keycloak/realm-export 1.json` | Offline reference with Backstage changes (superseded)      |

---

## 8. Security Notes

- **Never commit client secrets** to version control
- Use environment variables or a secrets manager for `AUTH_OIDC_CLIENT_SECRET`
- The `backstage` client is confidential (not public) - ensure `clientAuthenticatorType: client-secret`
- PKCE is enabled (`S256`) - do not disable this
- `frontchannelLogout` is enabled for proper session cleanup
