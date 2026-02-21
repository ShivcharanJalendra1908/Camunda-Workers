#!/bin/bash
set -e
set -u

echo "=========================================="
echo "🚀 POSTGRES MULTI-DB INITIALIZATION START"
echo "=========================================="

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
# Step 1: Create required databases FIRST
# --------------------------------------------------
if [ -n "${POSTGRES_MULTIPLE_DATABASES:-}" ]; then
  echo "Databases to create: $POSTGRES_MULTIPLE_DATABASES"
  for db in $(echo "$POSTGRES_MULTIPLE_DATABASES" | tr ',' ' '); do
    create_database "$db"
    echo "   ✅ Ready: $db"
  done
else
  echo "⚠️  POSTGRES_MULTIPLE_DATABASES not set"
fi

# --------------------------------------------------
# Step 2: Apply auth schema (Keycloak DB)
# --------------------------------------------------
if [ -f "/docker-entrypoint-initdb.d/auth-schema.sql" ]; then
  echo "📄 Applying auth schema to 'keycloak' database..."
  psql -v ON_ERROR_STOP=1 \
    --username "$POSTGRES_USER" \
    --dbname "keycloak" \
    -f "/docker-entrypoint-initdb.d/20-auth-schema.sql"
  echo "   ✅ Auth schema applied"
else
  echo "⚠️  auth-schema.sql not found"
fi

# --------------------------------------------------
# Step 3: Apply franchises schema
# --------------------------------------------------
if [ -f "/docker-entrypoint-initdb.d/schema.sql" ]; then
  echo "📄 Applying schema to 'franchises' database..."
  psql -v ON_ERROR_STOP=1 \
    --username "$POSTGRES_USER" \
    --dbname "franchises" \
    -f "/docker-entrypoint-initdb.d/30-schema.sql"
  echo "   ✅ Franchises schema applied"
else
  echo "⚠️  schema.sql not found"
fi

echo "=========================================="
echo "🎉 DATABASE INITIALIZATION COMPLETE"
echo "=========================================="
