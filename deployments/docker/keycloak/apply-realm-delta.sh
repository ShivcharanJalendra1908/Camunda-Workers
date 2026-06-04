#!/bin/bash
set -euo pipefail

REALM="camunda-platform"
COMPOSE_DIR="$HOME/Workflow-and-Workers/deployments/docker"
KCADM="docker compose exec -T keycloak /opt/keycloak/bin/kcadm.sh"

if [ ! -d "$COMPOSE_DIR" ]; then
  echo "ERROR: Directory not found: $COMPOSE_DIR"
  exit 1
fi

if ! docker compose version &>/dev/null; then
  echo "ERROR: 'docker compose' not found. Install Docker Compose v2."
  exit 1
fi

cd "$COMPOSE_DIR"

log()  { echo "==> $*"; }
info() { echo "    $*"; }
ok()   { echo "    OK"; }

get_group_id() {
  $KCADM get groups -r "$REALM" -q "name=$1" --fields id --format csv 2>/dev/null | tail -1 | tr -d '"'
}

get_user_id() {
  $KCADM get users -r "$REALM" -q "username=$1" --fields id --format csv 2>/dev/null | tail -1 | tr -d '"'
}

user_exists() {
  local uid
  uid=$(get_user_id "$1")
  [ -n "$uid" ] && [ "$uid" != "[]" ]
}

wait_for_user_id() {
  local USERNAME="$1"
  local USER_ID=""
  for i in 1 2 3 4 5; do
    USER_ID=$(get_user_id "$USERNAME")
    if [ -n "$USER_ID" ] && [ "$USER_ID" != "[]" ]; then
      echo "$USER_ID"
      return 0
    fi
    info "  Waiting for $USERNAME to be indexed (attempt $i/5)..."
    sleep 2
  done
  echo ""
  return 1
}

# ============================================================================
# Step 0: Authenticate
# ============================================================================
log "Authenticating to Keycloak Admin CLI"
$KCADM config credentials \
  --server http://localhost:8080 \
  --realm master \
  --user admin \
  --password admin
ok

# ============================================================================
# Step 1: Create lemici-admin realm role (composite)
# ============================================================================
log "Creating realm role: lemici-admin"

if $KCADM get roles/lemici-admin -r "$REALM" &>/dev/null; then
  info "lemici-admin role already exists, skipping creation"
else
  $KCADM create roles -r "$REALM" \
    -s name=lemici-admin \
    -s description="LeMiCi platform administrator" \
    -s composite=true
  info "Created lemici-admin role"
fi

info "Adding composite client roles from realm-management..."
for ROLE in create-client view-identity-providers query-users manage-users \
  manage-identity-providers query-clients query-groups query-realms \
  view-clients view-users manage-authorization manage-realm manage-clients \
  view-events impersonation view-authorization view-realm manage-events; do
  $KCADM add-roles -r "$REALM" \
    --rname lemici-admin \
    --cclientid realm-management \
    --rid "$ROLE" 2>/dev/null || true
done
ok

# ============================================================================
# Step 2: Create engineering-team group
# ============================================================================
log "Creating group: engineering-team"

GROUP_ID=$(get_group_id engineering-team)
if [ -z "$GROUP_ID" ] || [ "$GROUP_ID" = "[]" ]; then
  $KCADM create groups -r "$REALM" \
    -s name=engineering-team \
    -s 'attributes.displayName=["Engineering Team"]'
  GROUP_ID=$(get_group_id engineering-team)
  info "Created engineering-team group"
else
  info "engineering-team group already exists (id: $GROUP_ID)"
fi

# ============================================================================
# Step 3: Create users + set password + assign role + join group
# ============================================================================
log "Creating users"

for USER in kintesh.admin shivcharan.admin syed.admin arslaan.admin; do
  echo ""
  info "Processing user: $USER"

  if user_exists "$USER"; then
    info "  User $USER already exists, skipping creation"
    USER_ID=$(get_user_id "$USER")
  else
    $KCADM create users -r "$REALM" \
      -s username="$USER" \
      -s enabled=true \
      -s 'requiredActions=["UPDATE_PASSWORD"]'
    info "  User created, waiting for index..."

    USER_ID=$(wait_for_user_id "$USER")
    if [ -z "$USER_ID" ]; then
      echo "ERROR: Could not retrieve ID for $USER after 5 attempts. Aborting."
      exit 1
    fi
    info "  Created user $USER (id: $USER_ID)"

    $KCADM set-password -r "$REALM" \
      --username "$USER" \
      --password "Test@1122" \
      --temporary
    info "  Temporary password set"
  fi

  $KCADM add-roles -r "$REALM" \
    --uusername "$USER" \
    --rolename lemici-admin 2>/dev/null || info "  lemici-admin role already assigned"

  if [ -n "$GROUP_ID" ] && [ "$GROUP_ID" != "[]" ]; then
    $KCADM update users/"$USER_ID"/groups/"$GROUP_ID" -r "$REALM" \
      -b "{\"groupId\": \"$GROUP_ID\", \"realm\": \"$REALM\", \"userId\": \"$USER_ID\"}" \
      2>/dev/null || info "  Already in engineering-team group"
  fi

  info "  Done: $USER"
done

# ============================================================================
# Step 4: Create backstage client
# ============================================================================
echo ""
log "Creating client: backstage"

BACKSTAGE_CLIENT_ID=$($KCADM get clients -r "$REALM" -q clientId=backstage --fields id --format csv 2>/dev/null | tail -1 | tr -d '"')

if [ -z "$BACKSTAGE_CLIENT_ID" ] || [ "$BACKSTAGE_CLIENT_ID" = "[]" ]; then
  $KCADM create clients -r "$REALM" \
    -s clientId=backstage \
    -s name=Backstage \
    -s description="OIDC client for Backstage developer portal" \
    -s enabled=true \
    -s publicClient=false \
    -s standardFlowEnabled=true \
    -s serviceAccountsEnabled=true \
    -s 'redirectUris=["http://localhost:7007/api/auth/oidc/handler/frame","https://backstage.lemici.com/api/auth/oidc/handler/frame"]' \
    -s 'webOrigins=["http://localhost:7007","https://backstage.lemici.com"]'
  info "Created backstage client"
  BACKSTAGE_CLIENT_ID=$($KCADM get clients -r "$REALM" -q clientId=backstage --fields id --format csv | tail -1 | tr -d '"')
else
  info "backstage client already exists"
fi

# ============================================================================
# Step 5: Assign service account roles for backstage
# ============================================================================
echo ""
log "Assigning service account roles for backstage"

SA_USER_ID=$(get_user_id service-account-backstage)
if [ -z "$SA_USER_ID" ] || [ "$SA_USER_ID" = "[]" ]; then
  info "service-account-backstage not yet visible, waiting 3s..."
  sleep 3
  SA_USER_ID=$(get_user_id service-account-backstage)
fi

if [ -n "$SA_USER_ID" ] && [ "$SA_USER_ID" != "[]" ]; then
  for ROLE in view-users query-users view-groups query-groups; do
    $KCADM add-roles -r "$REALM" \
      --uusername service-account-backstage \
      --cclientid realm-management \
      --rid "$ROLE" 2>/dev/null || info "  Role $ROLE already assigned"
  done
  ok
else
  info "WARNING: Could not find service-account-backstage user"
  info "Assign manually via Admin Console:"
  info "  Clients → backstage → Service Account Roles → realm-management"
  info "  Roles: view-users, query-users, view-groups, query-groups"
fi

# ============================================================================
# Done
# ============================================================================
echo ""
log "=== Apply complete ==="
echo ""
info "Next steps:"
info "  1. Regenerate backstage client secret:"
info "     Admin Console → Clients → backstage → Credentials → Regenerate Secret"
info "  2. Update .env BACKSTAGE_CLIENT_SECRET on EC2"
info "  3. Verify events: docker logs keycloak | grep -i 'event'"
