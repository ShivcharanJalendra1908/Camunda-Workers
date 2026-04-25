#!/bin/bash
# Run once to create Zeebe operate indices in Elasticsearch.
# Usage: ./setup-operate-indices.sh [ES_HOST]
# Default ES_HOST: http://localhost:9200
#./setup-operate-indices.sh http://localhost:9200

set -e
ES="${1:-http://localhost:9200}"
MAPPING_DIR="$(dirname "$0")/../data/elasticsearch/operate"
 
declare -A INDICES=(
  ["zeebe-process-instances"]="process_instances_mapping.json"
  ["zeebe-jobs"]="jobs_mapping.json"
  ["zeebe-incidents"]="incidents_mapping.json"
  ["zeebe-variables"]="variables_mapping.json"
  ["zeebe-deployments"]="deployments_mapping.json"
)
 
echo "Connecting to Elasticsearch: $ES"
echo ""
 
for INDEX in "${!INDICES[@]}"; do
  FILE="$MAPPING_DIR/${INDICES[$INDEX]}"
  printf "→ %-35s" "$INDEX"
 
  STATUS=$(curl -s -o /tmp/es_response.json -w "%{http_code}" \
    -X PUT "$ES/$INDEX" \
    -H "Content-Type: application/json" \
    -d @"$FILE")
 
  case "$STATUS" in
    200|201) echo "✓ created" ;;
    400)
      REASON=$(cat /tmp/es_response.json | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('error',{}).get('type',''))" 2>/dev/null)
      if [ "$REASON" = "resource_already_exists_exception" ]; then
        echo "⚠ already exists (skipped)"
      else
        echo "✗ failed: $(cat /tmp/es_response.json)"
        exit 1
      fi
      ;;
    *) echo "✗ HTTP $STATUS: $(cat /tmp/es_response.json)"; exit 1 ;;
  esac
done
 
echo ""
echo "All indices ready. Verify:"
echo "  curl $ES/_cat/indices/zeebe-* ?v"
 