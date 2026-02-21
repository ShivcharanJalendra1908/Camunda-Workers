#!/bin/bash

set -e
set -u

echo "=========================================="
echo "🚀 POSTGRES MULTI-DB INITIALIZATION START"
echo "=========================================="

# --------------------------------------------------
# Helper: create database if it does not exist
# --------------------------------------------------
create_database() {
  local db="$1"

  echo "📦 Ensuring database exists: $db"

  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" <<-EOSQL
    SELECT 'CREATE DATABASE $db'
    WHERE NOT EXISTS (
      SELECT FROM pg_database WHERE datname = '$db'
    )\gexec

    GRANT ALL PRIVILEGES ON DATABASE $db TO $POSTGRES_USER;
EOSQL
}

# --------------------------------------------------
# Step 1: Create all databases
# --------------------------------------------------
if [ -n "${POSTGRES_MULTIPLE_DATABASES:-}" ]; then
  echo "Databases to create: $POSTGRES_MULTIPLE_DATABASES"
  echo ""

  for db in $(echo "$POSTGRES_MULTIPLE_DATABASES" | tr ',' ' '); do
    create_database "$db"
    echo "   ✅ Ready: $db"
  done
else
  echo "⚠️  POSTGRES_MULTIPLE_DATABASES not set"
fi

echo ""
echo "=========================================="
echo "📄 APPLYING DATABASE SCHEMAS"
echo "=========================================="

# --------------------------------------------------
# Step 2: Apply auth schema (Keycloak-related)
# --------------------------------------------------
if [ -f "/docker-entrypoint-initdb.d/auth-schema.sql" ]; then
  echo "Applying auth schema to 'keycloak' database..."
  psql -v ON_ERROR_STOP=1 \
       --username "$POSTGRES_USER" \
       --dbname "keycloak" \
       -f "/docker-entrypoint-initdb.d/auth-schema.sql"
  echo "   ✅ Auth schema applied"
else
  echo "⚠️  auth-schema.sql not found, skipping"
fi

# --------------------------------------------------
# Step 3: Apply franchises schema
# --------------------------------------------------
if [ -f "/docker-entrypoint-initdb.d/schema.sql" ]; then
  echo "Applying schema to 'franchises' database..."
  psql -v ON_ERROR_STOP=1 \
       --username "$POSTGRES_USER" \
       --dbname "franchises" \
       -f "/docker-entrypoint-initdb.d/schema.sql"
  echo "   ✅ Franchises schema applied"
else
  echo "⚠️  schema.sql not found, skipping"
fi

echo ""
echo "=========================================="
echo "🎉 DATABASE INITIALIZATION COMPLETE"
echo "=========================================="
echo "Databases initialized:"
echo " - $POSTGRES_MULTIPLE_DATABASES"
echo ""
