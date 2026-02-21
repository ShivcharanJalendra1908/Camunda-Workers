#!/bin/bash
set -e
set -u

echo "=========================================="
echo "🚀 POSTGRES MULTI-DB INITIALIZATION START"
echo "=========================================="

create_database() {
  local db="$1"
  echo "📦 Ensuring database exists: $db"

  psql -v ON_ERROR_STOP=1 \
    --username "$POSTGRES_USER" \
    --dbname "postgres" <<-EOSQL
    SELECT 'CREATE DATABASE $db'
    WHERE NOT EXISTS (
      SELECT FROM pg_database WHERE datname = '$db'
    )\gexec;

    GRANT ALL PRIVILEGES ON DATABASE $db TO $POSTGRES_USER;
EOSQL
}

# --------------------------------------------------
# Step 1: Create databases
# --------------------------------------------------
if [ -n "${POSTGRES_MULTIPLE_DATABASES:-}" ]; then
  echo "Databases to create: $POSTGRES_MULTIPLE_DATABASES"
  for db in $(echo "$POSTGRES_MULTIPLE_DATABASES" | tr ',' ' '); do
    create_database "$db"
    echo "   ✅ Ready: $db"
  done
else
  echo "❌ POSTGRES_MULTIPLE_DATABASES not set"
  exit 1
fi

# --------------------------------------------------
# Step 2: Keycloak schema
# --------------------------------------------------
if [ -f "/docker-entrypoint-initdb.d/20-auth-schema.sql" ]; then
  echo "📄 Applying auth schema to keycloak DB..."
  psql -v ON_ERROR_STOP=1 \
    --username "$POSTGRES_USER" \
    --dbname "keycloak" \
    -f "/docker-entrypoint-initdb.d/20-auth-schema.sql"
else
  echo "❌ 20-auth-schema.sql not found"
  exit 1
fi

# --------------------------------------------------
# Step 3: Franchises schema
# --------------------------------------------------
if [ -f "/docker-entrypoint-initdb.d/30-schema.sql" ]; then
  echo "📄 Applying franchises schema..."
  psql -v ON_ERROR_STOP=1 \
    --username "$POSTGRES_USER" \
    --dbname "franchises" \
    -f "/docker-entrypoint-initdb.d/30-schema.sql"
else
  echo "❌ 30-schema.sql not found"
  exit 1
fi

echo "=========================================="
echo "🎉 DATABASE INITIALIZATION COMPLETE"
echo "=========================================="
