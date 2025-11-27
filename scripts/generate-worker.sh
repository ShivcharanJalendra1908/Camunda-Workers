#!/bin/bash
# scripts/generate-worker.sh
set -e

if [ $# -eq 0 ]; then
  echo "Usage: generate-worker.sh <activity-id>"
  exit 1
fi

go run cmd/tools/worker-generator/main.go --activity "$1" --output internal/workers/infrastructure/
echo "Worker scaffold generated for $1"