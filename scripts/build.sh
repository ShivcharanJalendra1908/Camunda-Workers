#!/bin/bash
# scripts/build.sh
set -e

echo "Building camunda-workers..."
go build -o bin/worker-manager ./cmd/worker-manager
echo "Build completed: bin/worker-manager"