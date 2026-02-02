#!/bin/bash

set -e
set -u

function create_user_and_database() {
    local database=$1
    echo "Creating database '$database'"
    psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" <<-EOSQL
        CREATE DATABASE $database;
        GRANT ALL PRIVILEGES ON DATABASE $database TO $POSTGRES_USER;
EOSQL
}

if [ -n "$POSTGRES_MULTIPLE_DATABASES" ]; then
    echo "=========================================="
    echo "🚀 CREATING MULTIPLE DATABASES"
    echo "Databases: $POSTGRES_MULTIPLE_DATABASES"
    echo "=========================================="
    
    # Pehle sab databases create karo
    for db in $(echo $POSTGRES_MULTIPLE_DATABASES | tr ',' ' '); do
        create_user_and_database $db
    done
    echo "✅ All databases created successfully"
    
    echo ""
    echo "=========================================="
    echo "📦 APPLYING SCHEMA TO FRANCHISES DATABASE"
    echo "=========================================="
    
    # Ab franchises database ke liye schema apply karo
    # Pehle check karo ki schema.sql file available hai
    if [ -f "/docker-entrypoint-initdb.d/schema.sql" ]; then
        echo "Found schema.sql, applying to franchises database..."
        if psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" -d franchises -f "/docker-entrypoint-initdb.d/schema.sql"; then
            echo "✅ Schema applied successfully to franchises database"
        else
            echo "❌ Failed to apply schema to franchises database"
            exit 1
        fi
    else
        echo "⚠️  schema.sql not found in /docker-entrypoint-initdb.d/"
        echo "Skipping schema initialization for franchises database"
    fi
    
    echo ""
    echo "=========================================="
    echo "✅ ALL DATABASE OPERATIONS COMPLETED"
    echo "=========================================="
fi