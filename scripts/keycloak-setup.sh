#!/bin/bash
# Fixed Keycloak Setup for Camunda OAuth Integration
# This script sets up Keycloak realm, client, and service account

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_header() { echo -e "\n${BLUE}========================================${NC}"; echo -e "${BLUE}$1${NC}"; echo -e "${BLUE}========================================${NC}"; }
print_status() { echo -e "${GREEN}[✓]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[!]${NC} $1"; }
print_error() { echo -e "${RED}[✗]${NC} $1"; }

print_header "Keycloak OAuth Setup for Camunda Platform"

# Configuration
KEYCLOAK_URL="http://localhost:8080"
ADMIN_USER="admin"
ADMIN_PASSWORD="admin"
REALM_NAME="camunda-platform"
CLIENT_ID="worker-client"
CLIENT_SECRET="worker-secret"

# Check dependencies
if ! command -v jq &> /dev/null; then
    print_error "jq is required but not installed"
    echo "Install: sudo apt-get install jq (Linux) or brew install jq (Mac)"
    exit 1
fi

if ! command -v curl &> /dev/null; then
    print_error "curl is required but not installed"
    exit 1
fi

# Wait for Keycloak
print_header "Step 1: Waiting for Keycloak"
MAX_WAIT=120
WAIT_TIME=0
while ! curl -s "$KEYCLOAK_URL/health/ready" > /dev/null 2>&1; do
    if [ $WAIT_TIME -ge $MAX_WAIT ]; then
        print_error "Keycloak not ready after ${MAX_WAIT}s"
        exit 1
    fi
    print_warning "Waiting for Keycloak... (${WAIT_TIME}s/${MAX_WAIT}s)"
    sleep 5
    WAIT_TIME=$((WAIT_TIME + 5))
done
print_status "Keycloak is ready"

# Get admin token
print_header "Step 2: Obtaining Admin Token"
TOKEN_RESPONSE=$(curl -s -X POST \
  "$KEYCLOAK_URL/realms/master/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "username=$ADMIN_USER" \
  -d "password=$ADMIN_PASSWORD" \
  -d "grant_type=password" \
  -d "client_id=admin-cli")

ADMIN_TOKEN=$(echo "$TOKEN_RESPONSE" | jq -r '.access_token')

if [ "$ADMIN_TOKEN" = "null" ] || [ -z "$ADMIN_TOKEN" ]; then
    print_error "Failed to get admin token"
    echo "Response: $TOKEN_RESPONSE"
    exit 1
fi
print_status "Admin token obtained"

# Create realm
print_header "Step 3: Creating Realm '$REALM_NAME'"
REALM_PAYLOAD='{
  "realm": "'$REALM_NAME'",
  "enabled": true,
  "displayName": "Camunda Platform",
  "accessTokenLifespan": 3600,
  "ssoSessionIdleTimeout": 1800,
  "ssoSessionMaxLifespan": 36000
}'

REALM_RESPONSE=$(curl -s -w "\n%{http_code}" -X POST \
  "$KEYCLOAK_URL/admin/realms" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$REALM_PAYLOAD")

HTTP_CODE=$(echo "$REALM_RESPONSE" | tail -n1)
BODY=$(echo "$REALM_RESPONSE" | head -n -1)

if [ "$HTTP_CODE" -eq 201 ]; then
    print_status "Realm created successfully"
elif [ "$HTTP_CODE" -eq 409 ]; then
    print_warning "Realm already exists (skipping)"
else
    print_error "Failed to create realm (HTTP $HTTP_CODE)"
    echo "Response: $BODY"
    exit 1
fi

# Create client
print_header "Step 4: Creating Client '$CLIENT_ID'"
CLIENT_PAYLOAD='{
  "clientId": "'$CLIENT_ID'",
  "name": "Camunda Worker Client",
  "description": "Service account for Camunda workers",
  "enabled": true,
  "protocol": "openid-connect",
  "publicClient": false,
  "serviceAccountsEnabled": true,
  "standardFlowEnabled": false,
  "implicitFlowEnabled": false,
  "directAccessGrantsEnabled": false,
  "clientAuthenticatorType": "client-secret",
  "secret": "'$CLIENT_SECRET'",
  "attributes": {
    "access.token.lifespan": "3600"
  }
}'

CLIENT_RESPONSE=$(curl -s -w "\n%{http_code}" -X POST \
  "$KEYCLOAK_URL/admin/realms/$REALM_NAME/clients" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$CLIENT_PAYLOAD")

HTTP_CODE=$(echo "$CLIENT_RESPONSE" | tail -n1)
BODY=$(echo "$CLIENT_RESPONSE" | head -n -1)

if [ "$HTTP_CODE" -eq 201 ]; then
    print_status "Client created successfully"
elif [ "$HTTP_CODE" -eq 409 ]; then
    print_warning "Client already exists (skipping)"
else
    print_error "Failed to create client (HTTP $HTTP_CODE)"
    echo "Response: $BODY"
    exit 1
fi

# Get client UUID
print_header "Step 5: Retrieving Client Configuration"
CLIENTS_LIST=$(curl -s -X GET \
  "$KEYCLOAK_URL/admin/realms/$REALM_NAME/clients?clientId=$CLIENT_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN")

CLIENT_UUID=$(echo "$CLIENTS_LIST" | jq -r '.[0].id')

if [ "$CLIENT_UUID" = "null" ] || [ -z "$CLIENT_UUID" ]; then
    print_error "Failed to find client UUID"
    exit 1
fi
print_status "Client UUID: $CLIENT_UUID"

# Get service account user
SERVICE_ACCOUNT=$(curl -s -X GET \
  "$KEYCLOAK_URL/admin/realms/$REALM_NAME/clients/$CLIENT_UUID/service-account-user" \
  -H "Authorization: Bearer $ADMIN_TOKEN")

SERVICE_ACCOUNT_ID=$(echo "$SERVICE_ACCOUNT" | jq -r '.id')
print_status "Service account user: $SERVICE_ACCOUNT_ID"

# Verification
print_header "Step 6: Verification"

# Test OAuth endpoint
print_status "Testing OAuth discovery endpoint..."
DISCOVERY=$(curl -s "$KEYCLOAK_URL/realms/$REALM_NAME/.well-known/openid-configuration")
TOKEN_ENDPOINT=$(echo "$DISCOVERY" | jq -r '.token_endpoint')
ISSUER=$(echo "$DISCOVERY" | jq -r '.issuer')

echo "  Token Endpoint: $TOKEN_ENDPOINT"
echo "  Issuer: $ISSUER"

# Test token acquisition
print_status "Testing token acquisition with client credentials..."
TEST_TOKEN_RESPONSE=$(curl -s -X POST \
  "$KEYCLOAK_URL/realms/$REALM_NAME/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "client_id=$CLIENT_ID" \
  -d "client_secret=$CLIENT_SECRET" \
  -d "grant_type=client_credentials")

TEST_TOKEN=$(echo "$TEST_TOKEN_RESPONSE" | jq -r '.access_token')

if [ "$TEST_TOKEN" != "null" ] && [ -n "$TEST_TOKEN" ]; then
    print_status "✅ Successfully obtained access token"
    echo "  Token (first 50 chars): ${TEST_TOKEN:0:50}..."
    
    # Decode token to show claims
    TOKEN_PAYLOAD=$(echo "$TEST_TOKEN" | cut -d'.' -f2)
    # Add padding if needed
    case $((${#TOKEN_PAYLOAD} % 4)) in
        2) TOKEN_PAYLOAD="${TOKEN_PAYLOAD}==" ;;
        3) TOKEN_PAYLOAD="${TOKEN_PAYLOAD}=" ;;
    esac
    
    DECODED=$(echo "$TOKEN_PAYLOAD" | base64 -d 2>/dev/null | jq '.')
    echo ""
    echo "  Token Claims:"
    echo "$DECODED" | jq '{
        azp: .azp,
        iss: .iss,
        exp: .exp,
        iat: .iat,
        clientId: .clientId
    }'
else
    print_error "Failed to obtain access token"
    echo "Response: $TEST_TOKEN_RESPONSE"
    exit 1
fi

# Summary
print_header "Setup Complete! 🎉"
echo ""
echo "Keycloak Configuration:"
echo "  URL:            $KEYCLOAK_URL"
echo "  Realm:          $REALM_NAME"
echo "  Client ID:      $CLIENT_ID"
echo "  Client Secret:  $CLIENT_SECRET"
echo ""
echo "OAuth Endpoints:"
echo "  Token URL:      $TOKEN_ENDPOINT"
echo "  Issuer:         $ISSUER"
echo ""
echo "Next Steps:"
echo "  1. Restart your Camunda workers:"
echo "     docker-compose restart camunda-workers"
echo ""
echo "  2. Monitor worker logs:"
echo "     docker logs -f camunda-workers"
echo ""
echo "  3. Workers should now authenticate successfully!"