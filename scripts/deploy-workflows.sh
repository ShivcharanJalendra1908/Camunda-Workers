#!/bin/bash

echo "🚀 Deploying BPMN workflows to Zeebe..."

# Wait for Zeebe to be ready
echo "⏳ Waiting for Zeebe..."
until curl -s http://localhost:9600/ready > /dev/null 2>&1; do
  echo "Zeebe not ready yet, waiting 5s..."
  sleep 5
done

echo "✅ Zeebe is ready!"

# Install zbctl if not present
if ! command -v zbctl &> /dev/null; then
  echo "📥 Installing zbctl..."
  curl -sL https://github.com/camunda/zeebe/releases/download/8.3.9/zbctl.linux.amd64.tar.gz | tar xz -C /tmp
  sudo mv /tmp/zbctl /usr/local/bin/
  chmod +x /usr/local/bin/zbctl
fi

# Deploy all BPMN files
echo "📦 Deploying BPMN files..."

# Home Page
zbctl deploy bpmn/franchise-home-page.bpmn \
  --address localhost:26500 \
  --insecure

# Listing Page  
zbctl deploy bpmn/franchise-listing-page.bpmn \
  --address localhost:26500 \
  --insecure

# Detail Page
zbctl deploy bpmn/franchise-detail-page.bpmn \
  --address localhost:26500 \
  --insecure

# Search
zbctl deploy bpmn/franchise-listing-ai-search.bpmn \
  --address localhost:26500 \
  --insecure

echo "✅ All workflows deployed successfully!"