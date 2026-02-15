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

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "lemici_dev" <<-EOSQL
    \i /docker-entrypoint-initdb.d/auth-schema.sql
EOSQL

echo "Auth schema created successfully"

if [ -n "$POSTGRES_MULTIPLE_DATABASES" ]; then
    echo "=========================================="
    echo "🚀 INITIALIZING MULTIPLE DATABASES"
    echo "=========================================="
    echo "Database list: $POSTGRES_MULTIPLE_DATABASES"
    echo ""
    
    # Step 1: Create all databases
    echo "📦 Creating databases..."
    for db in $(echo $POSTGRES_MULTIPLE_DATABASES | tr ',' ' '); do
        create_user_and_database $db
        echo "   ✅ Created: $db"
    done
    echo "✅ All databases created successfully"
    
    echo ""
    echo "=========================================="
    echo "📦 APPLYING FRANCHISES DATABASE SCHEMA"
    echo "=========================================="
    
    # Step 2: Apply schema to franchises database
    # First wait a bit to ensure databases are ready
    sleep 2
    
    if [ -f "/docker-entrypoint-initdb.d/schema.sql" ]; then
        echo "Found schema.sql file, applying to 'franchises' database..."
        
        # Try to apply schema, retry a few times if needed
        for i in {1..5}; do
            echo "Attempt $i to apply schema..."
            if psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" -d franchises -f "/docker-entrypoint-initdb.d/schema.sql"; then
                echo "✅ Schema successfully applied to 'franchises' database"
                break
            else
                if [ $i -eq 5 ]; then
                    echo "❌ Failed to apply schema after 5 attempts"
                    exit 1
                fi
                echo "Schema application failed, retrying in 2 seconds..."
                sleep 2
            fi
        done
    else
        echo "⚠️  Warning: schema.sql not found at /docker-entrypoint-initdb.d/schema.sql"
        echo "Skipping schema initialization for franchises database"
    fi
    
    echo ""
    echo "=========================================="
    echo "🎉 DATABASE INITIALIZATION COMPLETE"
    echo "=========================================="
    echo "Databases ready: $POSTGRES_MULTIPLE_DATABASES"
    echo "Franchises schema: ✅ Applied"
    echo ""
fi