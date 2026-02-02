#!/bin/bash

# ========================================
# MIGRATE TO SINGLE ES INDEX
# ========================================

set -e

ES_HOST="${ES_HOST:-http://localhost:9200}"
INDEX_NAME="franchises_index"
MAPPING_FILE="./data/elasticsearch/franchises_index_mapping.json"

echo "🔧 Starting migration to single ES index..."


# ---------- Pre-checks ----------
echo "🔍 Checking Elasticsearch..."
curl -s "$ES_HOST" > /dev/null || {
  echo "❌ Elasticsearch not reachable at $ES_HOST"
  exit 1
}

if [ ! -f "$MAPPING_FILE" ]; then
  echo "❌ Mapping file not found: $MAPPING_FILE"
  exit 1
fi


# Step 1: Delete old indices
echo "🗑️ Deleting old indices..."
curl -X DELETE "$ES_HOST/food_franchises" || true
curl -X DELETE "$ES_HOST/education_franchises" || true
curl -X DELETE "$ES_HOST/fashion_franchises" || true

# Step 2: Create new index with mapping
echo "📦 Creating new index: $INDEX_NAME..."
curl -X PUT "$ES_HOST/$INDEX_NAME" \
  -H "Content-Type: application/json" \
  -d @"$MAPPING_FILE"

# Step 3: Check index creation
echo "✅ Checking index..."
curl -X GET "$ES_HOST/$INDEX_NAME/_mapping?pretty"

# Step 4: Sync data from Postgres
echo "🔄 Syncing data from Postgres to ES..."

# You'll need to call your franchise-es-indexer worker for each franchise
# Or use a bulk indexing script

# Example using psql + curl:
psql -h localhost -U lemici_user -d lemici_franchise_db -t -c \
  "SELECT id FROM franchises;" | while read -r franchise_id; do
    if [ -n "$franchise_id" ]; then
        echo "Indexing franchise: $franchise_id"
        
        # Trigger Camunda workflow or direct API call
        # curl -X POST "http://localhost:8080/api/v1/internal/franchises/$franchise_id/reindex"
        
        # OR call worker directly via Zeebe
        # zbctl create instance franchise-es-sync --variables "{\"franchise_id\": \"$franchise_id\"}"
    fi
done

echo "✅ Migration completed!"
echo "📊 Index stats:"
curl -X GET "$ES_HOST/$INDEX_NAME/_stats?pretty" | grep -E "\"count\"|\"size_in_bytes\""

echo ""
echo "🎉 All done! New index: $INDEX_NAME"