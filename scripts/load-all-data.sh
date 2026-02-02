#!/bin/bash
# scripts/load-all-data-complete-fixed.sh

set -e

echo "🚀 Loading ALL Franchise Data (Elasticsearch + PostgreSQL)..."

# ============================================================================
# 0. PRE-CHECKS AND SETUP
# ============================================================================
echo ""
echo "🔍 Pre-checks..."

# Check Elasticsearch
ES_HOST="http://localhost:9200"
if ! curl -s "$ES_HOST" > /dev/null; then
    echo "❌ Elasticsearch is not running at $ES_HOST"
    exit 1
fi
echo "✅ Elasticsearch is running"

# Check PostgreSQL
if ! docker ps | grep -q postgres; then
    echo "❌ PostgreSQL container is not running"
    exit 1
fi
echo "✅ PostgreSQL container is running"

# Ensure system user exists
echo ""
echo "👤 Ensuring system user exists..."
SYSTEM_USER_EXISTS=$(docker exec postgres psql -U postgres -d franchises -t -c "SELECT COUNT(*) FROM users WHERE email = 'system@lemici.local';" | tr -d '[:space:]')
if [ "$SYSTEM_USER_EXISTS" = "0" ]; then
    docker exec postgres psql -U postgres -d franchises <<EOF
INSERT INTO users (id, email, name) VALUES 
('00000000-0000-0000-0000-000000000001', 'system@lemici.local', 'System Seeder');
EOF
    echo "✅ System user created"
else
    echo "✅ System user already exists"
fi
SYSTEM_USER_ID="00000000-0000-0000-0000-000000000001"

# ============================================================================
# 1. LOAD ELASTICSEARCH DATA
# ============================================================================
echo ""
echo "📊 Step 1: Loading Elasticsearch Indices..."

# Clean up existing indices
echo "Cleaning up existing indices..."
for index in food-franchises education-franchises fashion-franchises food_franchises education_franchises fashion_franchises; do
    curl -s -X DELETE "$ES_HOST/$index" 2>/dev/null || true
done

# Create indices with underscores
echo ""
echo "Creating Elasticsearch indices..."
for index_name in food_franchises education_franchises fashion_franchises; do
    echo "Creating index: $index_name"
    curl -s -X PUT "$ES_HOST/$index_name" -H 'Content-Type: application/json' -d '{
      "settings": {
        "number_of_shards": 1,
        "number_of_replicas": 0
      },
      "mappings": {
        "properties": {
          "brand": { "type": "text", "fields": { "keyword": { "type": "keyword" } } },
          "category": { "type": "text", "fields": { "keyword": { "type": "keyword" } } },
          "description": { "type": "text" },
          "highlights": { "type": "text" },
          "tags": { "type": "keyword" },
          "location": { "type": "text", "fields": { "keyword": { "type": "keyword" } } },
          "investment_min": { "type": "long" },
          "investment_max": { "type": "long" },
          "space_min": { "type": "integer" },
          "space_max": { "type": "integer" },
          "rating": { "type": "float" },
          "since": { "type": "integer" },
          "outlets": { "type": "integer" },
          "verified": { "type": "boolean" }
        }
      }
    }' | jq -r '"  ✅ " + (.index // .acknowledged)'
done
echo "✅ Elasticsearch indices created"

# Load NDJSON data
echo ""
echo "📥 Loading Elasticsearch NDJSON data..."
for category in food education fashion; do
    ndjson_file="data/elasticsearch/${category}-franchises.ndjson"
    if [ -f "$ndjson_file" ]; then
        echo "Loading $category data..."
        response=$(curl -s -X POST "$ES_HOST/${category}_franchises/_bulk" \
             -H 'Content-Type: application/x-ndjson' \
             --data-binary @"$ndjson_file")
        if echo "$response" | jq -e '.errors == false' > /dev/null 2>&1; then
            item_count=$(echo "$response" | jq -r '.items | length')
            echo "  ✅ Loaded $item_count documents"
        else
            echo "  ❌ Error loading data"
        fi
    else
        echo "  ⚠️  File not found: $ndjson_file"
    fi
done

# Verify ES data
echo ""
echo "📊 Elasticsearch verification:"
for index in food_franchises education_franchises fashion_franchises; do
    count=$(curl -s "$ES_HOST/$index/_count" | jq -r '.count // 0')
    echo "  $index: $count documents"
done

# ============================================================================
# 2. LOAD POSTGRESQL DATA
# ============================================================================
echo ""
echo "📊 Step 2: Loading PostgreSQL Data..."

# Check CSV files
echo "📂 Checking CSV files..."
if [ ! -d "data/postgres" ]; then
    echo "❌ data/postgres directory not found!"
    exit 1
fi

echo "Available CSV files:"
for csv_file in data/postgres/*.csv; do
    if [ -f "$csv_file" ]; then
        file_name=$(basename "$csv_file")
        lines=$(wc -l < "$csv_file" 2>/dev/null || echo "?")
        echo "  - $file_name ($lines lines)"
    fi
done

echo ""
read -p "Load PostgreSQL data? (y/n): " -r response
if [[ ! "$response" =~ ^([yY][eE][sS]|[yY])$ ]]; then
    echo "Skipping PostgreSQL data load."
else
    # ============================================================================
    # 2.1 TRUNCATE TABLES
    # ============================================================================
    echo ""
    echo "🧹 Truncating tables..."
    docker exec postgres psql -U postgres -d franchises <<EOF
-- Truncate in reverse dependency order
TRUNCATE TABLE notifications CASCADE;
TRUNCATE TABLE application_history CASCADE;
TRUNCATE TABLE franchise_applications CASCADE;
TRUNCATE TABLE idempotency_keys CASCADE;
TRUNCATE TABLE franchise_operations CASCADE;
TRUNCATE TABLE franchise_investment_requirement CASCADE;
TRUNCATE TABLE franchise_business_overview CASCADE;
TRUNCATE TABLE franchise_social_links CASCADE;
TRUNCATE TABLE franchise_stats CASCADE;
TRUNCATE TABLE franchise_cities CASCADE;
TRUNCATE TABLE category_questions CASCADE;
TRUNCATE TABLE saved_searches CASCADE;
TRUNCATE TABLE user_favorites CASCADE;
TRUNCATE TABLE franchise_categories CASCADE;
TRUNCATE TABLE sub_categories CASCADE;
TRUNCATE TABLE categories CASCADE;
TRUNCATE TABLE franchises CASCADE;
TRUNCATE TABLE industries CASCADE;
EOF
    echo "✅ Tables truncated"

    # ============================================================================
    # 2.2 LOAD EACH TABLE WITH CORRECT SETTINGS
    # ============================================================================
    
    # Load Industries (has quotes)
    echo ""
    echo "📥 Loading industries..."
    if [ -f "data/postgres/industries.csv" ]; then
        docker cp data/postgres/industries.csv postgres:/tmp/industries.csv
        docker exec postgres psql -U postgres -d franchises <<EOF
COPY industries FROM '/tmp/industries.csv' WITH (FORMAT CSV, HEADER true, QUOTE '"');
EOF
        echo "✅ Industries loaded"
    else
        echo "❌ industries.csv not found!"
    fi

    # Load Franchises (NO quotes)
    echo ""
    echo "📥 Loading franchises..."
    if [ -f "data/postgres/franchises.csv" ]; then
        docker cp data/postgres/franchises.csv postgres:/tmp/franchises.csv
        docker exec postgres psql -U postgres -d franchises <<EOF
-- First check if created_by, updated_by exist in CSV
COPY franchises FROM '/tmp/franchises.csv' WITH (FORMAT CSV, HEADER true);
EOF
        # Update created_by, updated_by to system user if NULL
        docker exec postgres psql -U postgres -d franchises <<EOF
UPDATE franchises 
SET created_by = '$SYSTEM_USER_ID',
    updated_by = '$SYSTEM_USER_ID'
WHERE created_by IS NULL OR updated_by IS NULL;
EOF
        echo "✅ Franchises loaded"
    else
        echo "❌ franchises.csv not found!"
    fi

    # Load Franchise Stats (has quotes)
    echo ""
    echo "📥 Loading franchise_stats..."
    if [ -f "data/postgres/franchise_stats.csv" ]; then
        docker cp data/postgres/franchise_stats.csv postgres:/tmp/franchise_stats.csv
        docker exec postgres psql -U postgres -d franchises <<EOF
COPY franchise_stats FROM '/tmp/franchise_stats.csv' WITH (FORMAT CSV, HEADER true, QUOTE '"');
EOF
        echo "✅ Franchise stats loaded"
    else
        echo "⚠️  franchise_stats.csv not found"
    fi

    # Load Franchise Cities (has quotes)
    echo ""
    echo "📥 Loading franchise_cities..."
    if [ -f "data/postgres/franchise_cities.csv" ]; then
        docker cp data/postgres/franchise_cities.csv postgres:/tmp/franchise_cities.csv
        docker exec postgres psql -U postgres -d franchises <<EOF
COPY franchise_cities FROM '/tmp/franchise_cities.csv' WITH (FORMAT CSV, HEADER true, QUOTE '"');
EOF
        echo "✅ Franchise cities loaded"
    else
        echo "⚠️  franchise_cities.csv not found"
    fi

    # Load Franchise Business Overview (NO quotes)
    echo ""
    echo "📥 Loading franchise_business_overview..."
    if [ -f "data/postgres/franchise_business_overview.csv" ]; then
        docker cp data/postgres/franchise_business_overview.csv postgres:/tmp/franchise_business_overview.csv
        docker exec postgres psql -U postgres -d franchises <<EOF
COPY franchise_business_overview FROM '/tmp/franchise_business_overview.csv' WITH (FORMAT CSV, HEADER true);
EOF
        # Update created_by, updated_by to system user if NULL
        docker exec postgres psql -U postgres -d franchises <<EOF
UPDATE franchise_business_overview 
SET created_by = '$SYSTEM_USER_ID',
    updated_by = '$SYSTEM_USER_ID'
WHERE created_by IS NULL OR updated_by IS NULL;
EOF
        echo "✅ Franchise business overview loaded"
    else
        echo "⚠️  franchise_business_overview.csv not found"
    fi

    # Load Franchise Social Links (has quotes)
    echo ""
    echo "📥 Loading franchise_social_links..."
    if [ -f "data/postgres/franchise_social_links.csv" ]; then
        docker cp data/postgres/franchise_social_links.csv postgres:/tmp/franchise_social_links.csv
        docker exec postgres psql -U postgres -d franchises <<EOF
COPY franchise_social_links FROM '/tmp/franchise_social_links.csv' WITH (FORMAT CSV, HEADER true, QUOTE '"');
EOF
        echo "✅ Franchise social links loaded"
    else
        echo "⚠️  franchise_social_links.csv not found"
    fi

    # Load Category Questions (has quotes)
    echo ""
    echo "📥 Loading category_questions..."
    if [ -f "data/postgres/category_questions.csv" ]; then
        docker cp data/postgres/category_questions.csv postgres:/tmp/category_questions.csv
        docker exec postgres psql -U postgres -d franchises <<EOF
COPY category_questions FROM '/tmp/category_questions.csv' WITH (FORMAT CSV, HEADER true, QUOTE '"');
EOF
        echo "✅ Category questions loaded"
    else
        echo "⚠️  category_questions.csv not found"
    fi

    # Clean up temp files
    docker exec postgres bash -c "rm -f /tmp/*.csv" 2>/dev/null || true

    # ============================================================================
    # 2.3 VERIFICATION
    # ============================================================================
    echo ""
    echo "📊 PostgreSQL verification:"
    docker exec postgres psql -U postgres -d franchises <<EOF
SELECT 
    'Industries' as table_name, 
    COUNT(*) as rows,
    CASE WHEN COUNT(*) > 0 THEN '✅' ELSE '❌' END as status
FROM industries
UNION ALL
SELECT 'Franchises', COUNT(*), CASE WHEN COUNT(*) > 0 THEN '✅' ELSE '❌' END
FROM franchises
UNION ALL
SELECT 'Franchise Stats', COUNT(*), CASE WHEN COUNT(*) > 0 THEN '✅' ELSE '❌' END
FROM franchise_stats
UNION ALL
SELECT 'Franchise Cities', COUNT(*), CASE WHEN COUNT(*) > 0 THEN '✅' ELSE '❌' END
FROM franchise_cities
UNION ALL
SELECT 'Business Overview', COUNT(*), CASE WHEN COUNT(*) > 0 THEN '✅' ELSE '❌' END
FROM franchise_business_overview
UNION ALL
SELECT 'Social Links', COUNT(*), CASE WHEN COUNT(*) > 0 THEN '✅' ELSE '❌' END
FROM franchise_social_links
UNION ALL
SELECT 'Category Questions', COUNT(*), CASE WHEN COUNT(*) > 0 THEN '✅' ELSE '❌' END
FROM category_questions
ORDER BY table_name;
EOF
fi

# ============================================================================
# 3. FINAL SUMMARY AND TESTS
# ============================================================================
echo ""
echo "✨✨✨ FINAL SUMMARY ✨✨✨"

echo ""
echo "📊 Elasticsearch Status:"
for index in food_franchises education_franchises fashion_franchises; do
    count=$(curl -s "$ES_HOST/$index/_count" | jq -r '.count // 0')
    status="✅"
    if [ "$count" -eq 0 ]; then
        status="❌"
    fi
    echo "  $status $index: $count documents"
done

echo ""
echo "📊 PostgreSQL Status:"
if [[ "$response" =~ ^([yY][eE][sS]|[yY])$ ]]; then
    docker exec postgres psql -U postgres -d franchises <<EOF
SELECT 'Franchises' as table, COUNT(*) as count FROM franchises;
SELECT 'Sample franchise:' as info, name, slug FROM franchises LIMIT 1;
EOF
fi

echo ""
echo "✅✅✅ DATA LOADING COMPLETE ✅✅✅"
echo ""
echo "🧪 Quick Tests:"
echo ""
echo "1. Test Elasticsearch indices:"
echo "   curl -s \"http://localhost:9200/_cat/indices?v\" | grep franchise"
echo ""
echo "2. Test API Search (food):"
echo "   curl \"http://localhost:8080/api/v1/public/franchises/search?query=pizza&category=food\""
echo ""
echo "3. Check PostgreSQL data:"
echo "   docker exec postgres psql -U postgres -d franchises -c \"SELECT COUNT(*) FROM franchises;\""
echo ""
echo "4. Check specific franchise:"
echo "   docker exec postgres psql -U postgres -d franchises -c \"SELECT id, name, slug FROM franchises LIMIT 3;\""