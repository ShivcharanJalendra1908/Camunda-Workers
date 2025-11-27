# Makefile for Camunda Workers

# --- Variables ---
PROJECT_NAME := camunda-workers
VERSION ?= $(shell git describe --tags --always --dirty="-dev")
COMMIT_HASH ?= $(shell git rev-parse HEAD)
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
BINARY_NAME := worker-manager
BUILD_DIR := bin
CMD_PATH := cmd/worker-manager
DOCKER_REGISTRY ?= your-registry-hub # e.g., docker.io/yourorg, ghcr.io/yourorg

# --- Default Target ---
.DEFAULT_GOAL := help

# --- Help ---
.PHONY: help
help:  ## Display this help message
	@echo "Makefile for $(PROJECT_NAME)"
	@echo "Usage: make <target>"
	@echo ""
	@grep -E '^[a-zA-Z_0-9%-]+:.*?## .*$$' $(word 1,$(MAKEFILE_LIST)) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

# --- Build ---
.PHONY: build
build:  ## Build the worker manager binary
	@echo "Building $(PROJECT_NAME) v$(VERSION) (commit: $(COMMIT_HASH))"
	go build -ldflags="-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH) -X main.date=$(BUILD_TIME)" -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_PATH)

.PHONY: build-all
build-all:  ## Build all binaries (currently only worker-manager)
	make build

# --- Test ---
.PHONY: test
test:  ## Run unit tests with coverage
	go test -v -race -coverprofile=coverage.out ./...

.PHONY: test-unit
test-unit:  ## Run only unit tests
	go test -v -tags=unit -cover ./internal/workers/...

.PHONY: test-integration
test-integration:  ## Run integration tests (requires running services)
	@echo "Running integration tests..."
	go test -v -tags=integration -count=1 -timeout 60s ./test/integration/...

.PHONY: coverage
coverage: test  ## Generate and open coverage report
	go tool cover -html=coverage.out

# --- Run ---
.PHONY: run
run: build  ## Build and run the worker manager locally
	@echo "Running $(BINARY_NAME)..."
	./$(BUILD_DIR)/$(BINARY_NAME)

.PHONY: run-dev
run-dev:  ## Run with development configuration
	APP_ENVIRONMENT=development ./$(BUILD_DIR)/$(BINARY_NAME)

# --- Docker ---
.PHONY: docker-build
docker-build:  ## Build the Docker image
	@echo "Building Docker image $(DOCKER_REGISTRY)/$(PROJECT_NAME):$(VERSION)"
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT_HASH=$(COMMIT_HASH) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(DOCKER_REGISTRY)/$(PROJECT_NAME):$(VERSION) \
		-t $(DOCKER_REGISTRY)/$(PROJECT_NAME):latest \
		-f deployments/docker/Dockerfile.worker .

.PHONY: docker-push
docker-push: docker-build  ## Push the Docker image to registry
	@echo "Pushing Docker image $(DOCKER_REGISTRY)/$(PROJECT_NAME):$(VERSION)"
	docker push $(DOCKER_REGISTRY)/$(PROJECT_NAME):$(VERSION)
	docker push $(DOCKER_REGISTRY)/$(PROJECT_NAME):latest

.PHONY: docker-run
docker-run:  ## Run the Docker image locally
	docker run --rm -it --network host \
		-e APP_ENVIRONMENT=development \
		-e ZEEBE_ADDRESS=localhost:26500 \
		$(DOCKER_REGISTRY)/$(PROJECT_NAME):$(VERSION)

# --- Registry & Worker Generation ---
.PHONY: registry-validate
registry-validate:  ## Validate the activity registry file
	@echo "Validating activity registry..."
	go run cmd/tools/registry-validator/main.go --registry configs/activity-registry.json

.PHONY: generate-worker
generate-worker:  ## Generate a new worker skeleton (usage: make generate-worker NAME=worker-name)
	@echo "Generating worker: $(NAME)"
	@if [ -z "$(NAME)" ]; then \
		echo "Error: NAME is required. Usage: make generate-worker NAME=my-new-worker"; \
		exit 1; \
	fi
	go run cmd/tools/worker-generator/main.go --activity $(NAME) --output internal/workers/

.PHONY: generate-all-workers
generate-all-workers:  ## Generate scaffolds for all 25 workers defined in registry
	@echo "Generating scaffolds for all workers..."
	@jq -r '.activities[].id' configs/activity-registry.json | while read id; do \
		echo "Generating for $$id"; \
		go run cmd/tools/worker-generator/main.go --activity "$$id" --output internal/workers/ || exit 1; \
	done

# --- Lint & Format ---
.PHONY: lint
lint:  ## Lint the Go code
	golangci-lint run

.PHONY: format
format:  ## Format the Go code
	gofmt -s -w .
	goimports -w .

# --- Clean ---
.PHONY: clean
clean:  ## Clean build artifacts
	rm -rf $(BUILD_DIR)/
	rm -f coverage.out

# --- Dependencies ---
.PHONY: tidy
tidy:  ## Tidy Go modules
	go mod tidy

.PHONY: vendor
vendor:  ## Vendor Go dependencies
	go mod vendor

# --- CI/CD Helpers ---
.PHONY: ci-test
ci-test: lint test  ## Run tests for CI pipeline
	@echo "CI tests passed."

.PHONY: release
release: build docker-build docker-push  ## Build and push release image (requires VERSION tag)
	@echo "Release $(VERSION) built and pushed."
	@echo "Don't forget to git tag v$(VERSION) and push the tag!"

# --- Local Dev Helpers ---
.PHONY: start-dependencies
start-dependencies:  ## Start local Camunda, DBs, etc. using Docker Compose
	docker-compose -f deployments/docker/docker-compose.yml up -d

.PHONY: stop-dependencies
stop-dependencies:  ## Stop local dependencies
	docker-compose -f deployments/docker/docker-compose.yml down

.PHONY: logs-dependencies
logs-dependencies:  ## View logs from local dependencies
	docker-compose -f deployments/docker/docker-compose.yml logs -f

include test/e2e/Makefile