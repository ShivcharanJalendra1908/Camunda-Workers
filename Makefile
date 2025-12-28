# ============================================================================
# Makefile for Camunda Workers & API Gateway
# ============================================================================

# --- Variables ---
PROJECT_NAME := camunda-workers
VERSION ?= $(shell git describe --tags --always --dirty="-dev" 2>/dev/null || echo "v1.0.0-dev")
COMMIT_HASH ?= $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Binary names
WORKER_BINARY := worker-manager
GATEWAY_BINARY := api-gateway

# Build directories
BUILD_DIR := bin
WORKER_CMD_PATH := cmd/worker-manager
GATEWAY_CMD_PATH := cmd/api-gateway

# Docker configuration
DOCKER_REGISTRY ?= docker.io/yourorg
COMPOSE_FILE := deployments/docker/docker-compose.yml

# Colors for output
COLOR_RESET := \033[0m
COLOR_BOLD := \033[1m
COLOR_GREEN := \033[32m
COLOR_YELLOW := \033[33m
COLOR_BLUE := \033[36m

# --- Default Target ---
.DEFAULT_GOAL := help

# --- Help ---
.PHONY: help
help:  ## Display this help message
	@echo "$(COLOR_BOLD)Makefile for $(PROJECT_NAME)$(COLOR_RESET)"
	@echo "$(COLOR_BLUE)Version: $(VERSION)$(COLOR_RESET)"
	@echo ""
	@echo "$(COLOR_BOLD)Usage:$(COLOR_RESET) make $(COLOR_GREEN)<target>$(COLOR_RESET)"
	@echo ""
	@grep -E '^[a-zA-Z_0-9%-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  $(COLOR_GREEN)%-30s$(COLOR_RESET) %s\n", $$1, $$2}'

# --- Quick Run Commands ---
.PHONY: run-api
run-api: ## Run API gateway directly with go run
	@echo "$(COLOR_BLUE)Running API gateway...$(COLOR_RESET)"
	@go run cmd/api-gateway/main.go

.PHONY: run-workers
run-workers: ## Run worker manager directly with go run
	@echo "$(COLOR_BLUE)Running worker manager...$(COLOR_RESET)"
	@go run cmd/worker-manager/main.go

.PHONY: run-all
run-all: docker-compose-up ## Start all services with Docker Compose (alias)

# --- Test Commands ---
.PHONY: test
test: ## Run all tests
	@echo "$(COLOR_BLUE)Running all tests...$(COLOR_RESET)"
	@go test ./...
	@echo "$(COLOR_GREEN)✓ Tests completed$(COLOR_RESET)"

# --- Build Commands ---
.PHONY: build
build: ## Build using the build script
	@echo "$(COLOR_BLUE)Building with scripts/build.sh...$(COLOR_RESET)"
	@./scripts/build.sh
	@echo "$(COLOR_GREEN)✓ Build completed$(COLOR_RESET)"

.PHONY: go-build
go-build: build-worker build-gateway  ## Build all binaries with Go

.PHONY: build-worker
build-worker:  ## Build the worker manager binary
	@echo "$(COLOR_BLUE)Building worker-manager v$(VERSION)...$(COLOR_RESET)"
	@go build -ldflags="-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH) -X main.date=$(BUILD_TIME)" \
		-o $(BUILD_DIR)/$(WORKER_BINARY) $(WORKER_CMD_PATH)
	@echo "$(COLOR_GREEN)✓ Worker binary built: $(BUILD_DIR)/$(WORKER_BINARY)$(COLOR_RESET)"

.PHONY: build-gateway
build-gateway:  ## Build the API gateway binary
	@echo "$(COLOR_BLUE)Building api-gateway v$(VERSION)...$(COLOR_RESET)"
	@go build -ldflags="-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH) -X main.date=$(BUILD_TIME)" \
		-o $(BUILD_DIR)/$(GATEWAY_BINARY) $(GATEWAY_CMD_PATH)
	@echo "$(COLOR_GREEN)✓ Gateway binary built: $(BUILD_DIR)/$(GATEWAY_BINARY)$(COLOR_RESET)"

# --- Deploy Commands ---
.PHONY: deploy
deploy: ## Deploy using the deploy script
	@echo "$(COLOR_BLUE)Deploying with scripts/deploy.sh...$(COLOR_RESET)"
	@./scripts/deploy.sh
	@echo "$(COLOR_GREEN)✓ Deployment completed$(COLOR_RESET)"

# --- Run Commands ---
.PHONY: run-worker
run-worker: build-worker  ## Build and run worker manager
	@echo "$(COLOR_BLUE)Starting worker manager...$(COLOR_RESET)"
	@./$(BUILD_DIR)/$(WORKER_BINARY)

.PHONY: run-gateway
run-gateway: build-gateway  ## Build and run API gateway
	@echo "$(COLOR_BLUE)Starting API gateway...$(COLOR_RESET)"
	@./$(BUILD_DIR)/$(GATEWAY_BINARY)

.PHONY: run-dev
run-dev:  ## Run both services with hot reload (requires air)
	@echo "$(COLOR_BLUE)Starting development mode...$(COLOR_RESET)"
	@which air > /dev/null || (echo "$(COLOR_YELLOW)Installing air...$(COLOR_RESET)" && go install github.com/cosmtrek/air@latest)
	@air

# --- Extended Test Commands ---
.PHONY: test-unit
test-unit:  ## Run only unit tests
	@echo "$(COLOR_BLUE)Running unit tests...$(COLOR_RESET)"
	@go test -v -short -cover ./internal/...

.PHONY: test-integration
test-integration:  ## Run integration tests (requires services)
	@echo "$(COLOR_BLUE)Running integration tests...$(COLOR_RESET)"
	@go test -v -tags=integration -count=1 -timeout 60s ./test/integration/...

.PHONY: test-coverage
test-coverage:  ## Generate and open coverage report
	@echo "$(COLOR_BLUE)Generating coverage report...$(COLOR_RESET)"
	@go test -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out
	@echo "$(COLOR_GREEN)✓ Coverage report opened in browser$(COLOR_RESET)"

# --- JWT Token Generation ---
.PHONY: generate-jwt
generate-jwt:  ## Generate a test JWT token
	@echo "$(COLOR_BLUE)Generating JWT token...$(COLOR_RESET)"
	@go run scripts/generate-jwt.go -verbose

.PHONY: generate-jwt-admin
generate-jwt-admin:  ## Generate an admin JWT token
	@echo "$(COLOR_BLUE)Generating admin JWT token...$(COLOR_RESET)"
	@go run scripts/generate-jwt.go -roles "user,admin" -subscriptionTier "premium" -verbose

# --- Docker Commands ---
.PHONY: docker-build
docker-build: docker-build-worker docker-build-gateway  ## Build all Docker images

.PHONY: docker-build-worker
docker-build-worker:  ## Build worker Docker image
	@echo "$(COLOR_BLUE)Building worker Docker image...$(COLOR_RESET)"
	@docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT_HASH=$(COMMIT_HASH) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(DOCKER_REGISTRY)/$(PROJECT_NAME)-worker:$(VERSION) \
		-t $(DOCKER_REGISTRY)/$(PROJECT_NAME)-worker:latest \
		-f deployments/docker/Dockerfile.worker .
	@echo "$(COLOR_GREEN)✓ Worker image built$(COLOR_RESET)"

.PHONY: docker-build-gateway
docker-build-gateway:  ## Build API gateway Docker image
	@echo "$(COLOR_BLUE)Building gateway Docker image...$(COLOR_RESET)"
	@docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT_HASH=$(COMMIT_HASH) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(DOCKER_REGISTRY)/$(PROJECT_NAME)-gateway:$(VERSION) \
		-t $(DOCKER_REGISTRY)/$(PROJECT_NAME)-gateway:latest \
		-f deployments/docker/Dockerfile.gateway .
	@echo "$(COLOR_GREEN)✓ Gateway image built$(COLOR_RESET)"

.PHONY: docker-push
docker-push: docker-build  ## Push Docker images to registry
	@echo "$(COLOR_BLUE)Pushing Docker images...$(COLOR_RESET)"
	@docker push $(DOCKER_REGISTRY)/$(PROJECT_NAME)-worker:$(VERSION)
	@docker push $(DOCKER_REGISTRY)/$(PROJECT_NAME)-worker:latest
	@docker push $(DOCKER_REGISTRY)/$(PROJECT_NAME)-gateway:$(VERSION)
	@docker push $(DOCKER_REGISTRY)/$(PROJECT_NAME)-gateway:latest
	@echo "$(COLOR_GREEN)✓ Images pushed$(COLOR_RESET)"

# --- Docker Compose Commands ---
.PHONY: docker-compose-up
docker-compose-up:  ## Start all services with Docker Compose
	@echo "$(COLOR_BLUE)Starting all services...$(COLOR_RESET)"
	@docker-compose -f $(COMPOSE_FILE) up -d
	@echo "$(COLOR_GREEN)✓ Services started$(COLOR_RESET)"
	@echo "$(COLOR_YELLOW)Waiting for services to be ready...$(COLOR_RESET)"
	@sleep 10
	@make docker-compose-status

.PHONY: docker-compose-down
docker-compose-down:  ## Stop all services
	@echo "$(COLOR_BLUE)Stopping all services...$(COLOR_RESET)"
	@docker-compose -f $(COMPOSE_FILE) down
	@echo "$(COLOR_GREEN)✓ Services stopped$(COLOR_RESET)"

.PHONY: docker-compose-restart
docker-compose-restart:  ## Restart all services
	@echo "$(COLOR_BLUE)Restarting services...$(COLOR_RESET)"
	@docker-compose -f $(COMPOSE_FILE) restart
	@echo "$(COLOR_GREEN)✓ Services restarted$(COLOR_RESET)"

.PHONY: docker-compose-logs
docker-compose-logs:  ## View logs from all services
	@docker-compose -f $(COMPOSE_FILE) logs -f

.PHONY: docker-compose-logs-worker
docker-compose-logs-worker:  ## View worker logs only
	@docker-compose -f $(COMPOSE_FILE) logs -f camunda-workers

.PHONY: docker-compose-logs-gateway
docker-compose-logs-gateway:  ## View gateway logs only
	@docker-compose -f $(COMPOSE_FILE) logs -f api-gateway

.PHONY: docker-compose-status
docker-compose-status:  ## Show status of all services
	@echo "$(COLOR_BOLD)Service Status:$(COLOR_RESET)"
	@docker-compose -f $(COMPOSE_FILE) ps

.PHONY: docker-compose-clean
docker-compose-clean:  ## Stop and remove all containers, networks, volumes
	@echo "$(COLOR_YELLOW)⚠️  This will remove all data. Continue? [y/N]$(COLOR_RESET) " && read ans && [ $${ans:-N} = y ]
	@docker-compose -f $(COMPOSE_FILE) down -v
	@echo "$(COLOR_GREEN)✓ Cleanup completed$(COLOR_RESET)"

# --- Service Health Checks ---
.PHONY: health
health:  ## Check health of all services
	@echo "$(COLOR_BOLD)Health Check:$(COLOR_RESET)"
	@echo -n "API Gateway: " && curl -sf http://localhost:8080/health > /dev/null && echo "$(COLOR_GREEN)✓$(COLOR_RESET)" || echo "$(COLOR_YELLOW)✗$(COLOR_RESET)"
	@echo -n "Zeebe: " && docker exec zeebe timeout 5s bash -c ':> /dev/tcp/127.0.0.1/26500' 2>/dev/null && echo "$(COLOR_GREEN)✓$(COLOR_RESET)" || echo "$(COLOR_YELLOW)✗$(COLOR_RESET)"
	@echo -n "PostgreSQL: " && docker exec postgres pg_isready -U postgres > /dev/null 2>&1 && echo "$(COLOR_GREEN)✓$(COLOR_RESET)" || echo "$(COLOR_YELLOW)✗$(COLOR_RESET)"
	@echo -n "Redis: " && docker exec redis redis-cli ping > /dev/null 2>&1 && echo "$(COLOR_GREEN)✓$(COLOR_RESET)" || echo "$(COLOR_YELLOW)✗$(COLOR_RESET)"
	@echo -n "Elasticsearch: " && curl -sf http://localhost:9200/_cluster/health > /dev/null && echo "$(COLOR_GREEN)✓$(COLOR_RESET)" || echo "$(COLOR_YELLOW)✗$(COLOR_RESET)"

# --- Code Quality ---
.PHONY: lint
lint:  ## Run linter
	@echo "$(COLOR_BLUE)Running linter...$(COLOR_RESET)"
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	@golangci-lint run
	@echo "$(COLOR_GREEN)✓ Linting completed$(COLOR_RESET)"

.PHONY: format
format:  ## Format code
	@echo "$(COLOR_BLUE)Formatting code...$(COLOR_RESET)"
	@gofmt -s -w .
	@which goimports > /dev/null && goimports -w . || echo "$(COLOR_YELLOW)Install goimports: go install golang.org/x/tools/cmd/goimports@latest$(COLOR_RESET)"
	@echo "$(COLOR_GREEN)✓ Code formatted$(COLOR_RESET)"

.PHONY: vet
vet:  ## Run go vet
	@echo "$(COLOR_BLUE)Running go vet...$(COLOR_RESET)"
	@go vet ./...
	@echo "$(COLOR_GREEN)✓ Vet completed$(COLOR_RESET)"

# --- Dependencies ---
.PHONY: deps
deps:  ## Download dependencies
	@echo "$(COLOR_BLUE)Downloading dependencies...$(COLOR_RESET)"
	@go mod download
	@echo "$(COLOR_GREEN)✓ Dependencies downloaded$(COLOR_RESET)"

.PHONY: tidy
tidy:  ## Tidy Go modules
	@echo "$(COLOR_BLUE)Tidying modules...$(COLOR_RESET)"
	@go mod tidy
	@echo "$(COLOR_GREEN)✓ Modules tidied$(COLOR_RESET)"

.PHONY: vendor
vendor:  ## Vendor dependencies
	@echo "$(COLOR_BLUE)Vendoring dependencies...$(COLOR_RESET)"
	@go mod vendor
	@echo "$(COLOR_GREEN)✓ Dependencies vendored$(COLOR_RESET)"

# --- Clean ---
.PHONY: clean
clean:  ## Clean build artifacts
	@echo "$(COLOR_BLUE)Cleaning build artifacts...$(COLOR_RESET)"
	@rm -rf $(BUILD_DIR)/
	@rm -f coverage.out
	@echo "$(COLOR_GREEN)✓ Cleanup completed$(COLOR_RESET)"

# --- Release ---
.PHONY: release
release:  ## Build release binaries for multiple platforms
	@echo "$(COLOR_BLUE)Building release binaries...$(COLOR_RESET)"
	@mkdir -p $(BUILD_DIR)/release
	# Linux AMD64
	@GOOS=linux GOARCH=amd64 go build -ldflags="-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH)" \
		-o $(BUILD_DIR)/release/$(WORKER_BINARY)-linux-amd64 $(WORKER_CMD_PATH)
	@GOOS=linux GOARCH=amd64 go build -ldflags="-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH)" \
		-o $(BUILD_DIR)/release/$(GATEWAY_BINARY)-linux-amd64 $(GATEWAY_CMD_PATH)
	# macOS AMD64
	@GOOS=darwin GOARCH=amd64 go build -ldflags="-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH)" \
		-o $(BUILD_DIR)/release/$(WORKER_BINARY)-darwin-amd64 $(WORKER_CMD_PATH)
	@GOOS=darwin GOARCH=amd64 go build -ldflags="-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH)" \
		-o $(BUILD_DIR)/release/$(GATEWAY_BINARY)-darwin-amd64 $(GATEWAY_CMD_PATH)
	# Windows AMD64
	@GOOS=windows GOARCH=amd64 go build -ldflags="-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH)" \
		-o $(BUILD_DIR)/release/$(WORKER_BINARY)-windows-amd64.exe $(WORKER_CMD_PATH)
	@GOOS=windows GOARCH=amd64 go build -ldflags="-X main.version=$(VERSION) -X main.commit=$(COMMIT_HASH)" \
		-o $(BUILD_DIR)/release/$(GATEWAY_BINARY)-windows-amd64.exe $(GATEWAY_CMD_PATH)
	@echo "$(COLOR_GREEN)✓ Release binaries built in $(BUILD_DIR)/release/$(COLOR_RESET)"

# --- Quick Test Commands ---
.PHONY: test-api
test-api:  ## Quick API test (requires running services)
	@echo "$(COLOR_BLUE)Testing API endpoints...$(COLOR_RESET)"
	@echo "Health check:"
	@curl -s http://localhost:8080/health | jq || echo "$(COLOR_YELLOW)API not responding$(COLOR_RESET)"

.PHONY: test-api-auth
test-api-auth:  ## Test API with authentication
	@echo "$(COLOR_BLUE)Testing authenticated endpoint...$(COLOR_RESET)"
	@export TOKEN=$$(go run scripts/generate-jwt.go) && \
		curl -s -X POST http://localhost:8080/api/v1/ai/query \
		-H "Authorization: Bearer $$TOKEN" \
		-H "Content-Type: application/json" \
		-d '{"question":"test"}' | jq || echo "$(COLOR_YELLOW)Test failed$(COLOR_RESET)"

# --- Database Commands ---
.PHONY: db-migrate-up
db-migrate-up:  ## Run database migrations up
	@echo "$(COLOR_BLUE)Running database migrations...$(COLOR_RESET)"
	@go run cmd/tools/migrate/main.go up

.PHONY: db-migrate-down
db-migrate-down:  ## Run database migrations down
	@echo "$(COLOR_BLUE)Rolling back database migrations...$(COLOR_RESET)"
	@go run cmd/tools/migrate/main.go down

.PHONY: db-reset
db-reset:  ## Reset database (down + up)
	@make db-migrate-down
	@make db-migrate-up

# --- CI/CD ---
.PHONY: ci
ci: deps lint test  ## Run CI pipeline
	@echo "$(COLOR_GREEN)✓ CI pipeline completed$(COLOR_RESET)"

.PHONY: ci-test
ci-test: test  ## Run tests for CI
	@echo "$(COLOR_GREEN)✓ CI tests passed$(COLOR_RESET)"

# --- Info ---
.PHONY: version
version:  ## Show version information
	@echo "$(COLOR_BOLD)Version:$(COLOR_RESET) $(VERSION)"
	@echo "$(COLOR_BOLD)Commit:$(COLOR_RESET) $(COMMIT_HASH)"
	@echo "$(COLOR_BOLD)Build Time:$(COLOR_RESET) $(BUILD_TIME)"

.PHONY: info
info: version  ## Show project information
	@echo "$(COLOR_BOLD)Project:$(COLOR_RESET) $(PROJECT_NAME)"
	@echo "$(COLOR_BOLD)Go Version:$(COLOR_RESET) $$(go version)"
	@echo "$(COLOR_BOLD)Docker Registry:$(COLOR_RESET) $(DOCKER_REGISTRY)"

# ============================================================================
# FRANCHISE POSTGRES WORKER
# ============================================================================

.PHONY: test-franchise-worker
test-franchise-worker:  ## Test franchise-postgres worker
	@echo "$(COLOR_BLUE)Testing franchise-postgres worker...$(COLOR_RESET)"
	@go test ./internal/workers/data-access/franchise-postgres/... -v
	@echo "$(COLOR_GREEN)✓ Tests completed$(COLOR_RESET)"

.PHONY: deploy-franchise-bpmn
deploy-franchise-bpmn:  ## Deploy franchise BPMN workflows
	@echo "$(COLOR_BLUE)Deploying franchise BPMN workflows...$(COLOR_RESET)"
	@zbctl deploy bpmn/franchise-complete-registration.bpmn
	@zbctl deploy bpmn/franchise-admin-update.bpmn
	@echo "$(COLOR_GREEN)✓ Workflows deployed$(COLOR_RESET)"