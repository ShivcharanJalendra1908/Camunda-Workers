#!/bin/bash
# scripts/test.sh
set -e

echo "Running tests..."
go test -v -cover ./...
echo "Tests completed."