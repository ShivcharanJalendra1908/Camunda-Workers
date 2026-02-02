#!/bin/bash

# Save as fix-databases.sh
echo "Fixing PostgreSQL databases..."

# Create missing databases
docker exec postgres psql -U postgres -c "CREATE DATABASE IF NOT EXISTS keycloak;"
docker exec postgres psql -U postgres -c "CREATE DATABASE IF NOT EXISTS camunda;"
docker exec postgres psql -U postgres -c "CREATE DATABASE IF NOT EXISTS franchises;"

# Apply schema to franchises
echo "Applying schema to franchises database..."
docker exec postgres psql -U postgres -d franchises -f /docker-entrypoint-initdb.d/schema.sql

# Restart keycloak
echo "Restarting Keycloak..."
docker-compose -f deployments/docker/docker-compose.yml restart keycloak

# Check status
echo "Checking databases..."
docker exec postgres psql -U postgres -c "\l"

echo "Done!"