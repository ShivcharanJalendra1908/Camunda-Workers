#!/bin/bash
# scripts/deploy-operate-ui.sh
# Builds operate frontend Docker image and pushes to ECR
# Same pattern as your backend-gateway and backend-worker
#
# Usage:
#   ./scripts/deploy-operate-ui.sh                    # builds from project root
#   ./scripts/deploy-operate-ui.sh production         # production build
#   VITE_API_URL=https://myapi.com ./scripts/deploy-operate-ui.sh

set -e

# ── Config — match your existing ECR setup ───────────────────────────────────
AWS_ACCOUNT_ID="177925987307"
AWS_REGION="us-east-1"
ECR_REPO="operate-ui"
IMAGE_TAG="${IMAGE_TAG:-latest}"
VITE_API_URL="${VITE_API_URL:-https://dev-api.lemici.com}"

ECR_REGISTRY="$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com"
FULL_IMAGE="$ECR_REGISTRY/$ECR_REPO:$IMAGE_TAG"

# Project root (script lives in scripts/, go up one level)
PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Operate UI → ECR Deploy"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Registry : $ECR_REGISTRY"
echo "  Image    : $FULL_IMAGE"
echo "  API URL  : $VITE_API_URL"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ── 1. ECR login ──────────────────────────────────────────────────────────────
echo ""
echo "→ Logging into ECR..."
aws ecr get-login-password --region "$AWS_REGION" | \
  docker login --username AWS --password-stdin "$ECR_REGISTRY"

# ── 2. Create ECR repo if it doesn't exist ───────────────────────────────────
echo "→ Ensuring ECR repo exists..."
aws ecr describe-repositories \
  --repository-names "$ECR_REPO" \
  --region "$AWS_REGION" > /dev/null 2>&1 || \
aws ecr create-repository \
  --repository-name "$ECR_REPO" \
  --region "$AWS_REGION" \
  --image-scanning-configuration scanOnPush=true \
  --image-tag-mutability MUTABLE

# ── 3. Build Docker image ─────────────────────────────────────────────────────
echo "→ Building Docker image..."
cd "$PROJECT_ROOT"
docker build \
  --file deployments/docker/Dockerfile.operate \
  --build-arg VITE_API_URL="$VITE_API_URL" \
  --tag "$FULL_IMAGE" \
  --tag "$ECR_REGISTRY/$ECR_REPO:$(git rev-parse --short HEAD 2>/dev/null || echo 'no-git')" \
  .

# ── 4. Push to ECR ───────────────────────────────────────────────────────────
echo "→ Pushing to ECR..."
docker push "$FULL_IMAGE"
docker push "$ECR_REGISTRY/$ECR_REPO:$(git rev-parse --short HEAD 2>/dev/null || echo 'no-git')"

echo ""
echo "✅ Done!"
echo "   Image: $FULL_IMAGE"
echo ""
echo "Next steps:"
echo "  • ECS: Update your task definition to use $FULL_IMAGE"
echo "  • EC2: docker pull $FULL_IMAGE && docker-compose up -d operate-ui"