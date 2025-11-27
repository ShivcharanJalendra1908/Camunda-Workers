#!/bin/bash
# scripts/deploy.sh
set -e

echo "Deploying to Kubernetes..."
kubectl apply -f deployments/kubernetes/
echo "Deployment completed."