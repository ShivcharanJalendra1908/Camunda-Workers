Camunda 8 Go Workers – Franchise Workflow System
This repository contains a complete, production-ready implementation of 17 Go-based workers for the Camunda 8 workflow engine, supporting four core franchise business workflows:

AI Conversation: Natural language Q&A with franchise insights
Franchise Discovery: Search and browse franchises with intelligent ranking
Franchise Detail Page: View detailed franchise information with match scoring
Franchise Application: Submit and process franchise applications
Built for scalability, observability, and maintainability, this system integrates with PostgreSQL, Elasticsearch, Redis, AWS (SES/SNS), and an internal GenAI service.

🧩 Architecture Overview
The system follows a worker-per-activity pattern where each Camunda BPMN task is handled by a dedicated, stateless Go worker. Workers are organized into logical domains:

Infrastructure: Authentication, templating, response building
Data Access: Unified query interfaces for PostgreSQL and Elasticsearch
Business Logic: Workflow-specific validation, scoring, and persistence
AI/ML: Intent parsing, data enrichment, and LLM synthesis
All workers share common utilities for logging, configuration, error handling, and Camunda integration.

📂 Project Structure

camunda-workers/
├── cmd/
│ ├── worker-manager/ # Main entry point
│ └── tools/ # CLI utilities (registry, scaffolding)
├── internal/
│ ├── workers/ # All 17 workers (grouped by domain)
│ ├── common/ # Shared utilities (logging, config, DB clients)
│ └── models/ # Shared data models
├── pkg/
│ └── registry/ # Activity registry loader
├── configs/ # YAML configs + activity registry
├── deployments/ # Docker Compose + Kubernetes manifests
├── scripts/ # Build, test, deploy helpers
└── docs/ # Architecture, development, and worker guides

Each worker follows a standardized structure:

worker-name/
├── handler.go # Camunda job handler
├── handler_test.go # Unit tests (≥80% coverage)
├── config.go # Worker-specific config
├── models.go # Input/output structs
└── README.md # Worker-specific documentation

🚀 Quick Start (Local Development)
Prerequisites
Go 1.21+
Docker + Docker Compose

1. Start Dependencies
   bash
   docker-compose -f deployments/docker/docker-compose.yml up -d

Services launched:
Zeebe (Camunda engine) on :26500
Operate (workflow UI) on :8081
PostgreSQL, Elasticsearch, Redis

2. Build & Run Workers
   bash
   go run cmd/worker-manager/main.go

Workers will:
Connect to Zeebe
Register all 17 task handlers
Expose health endpoints on :8080 (/health, /ready, /metrics)

3. Deploy & Test Workflows
   Use Operate UI (http://localhost:8081) to:

Deploy BPMN workflows
Start process instances with sample variables
Monitor job execution and errors

☁️ Production Deployment

Kubernetes
bash
kubectl apply -f deployments/kubernetes/

Includes:
Deployment (3 replicas, resource limits)
ConfigMap (non-sensitive config)
Secrets (database passwords, API keys)
Service (health/metrics endpoints)
Liveness/Readiness Probes
Configuration
All settings are managed via configs/config.yaml with environment overrides:

yaml

workers:
validate-subscription:
enabled: true
max_jobs_active: 5
timeout: 10s

database:
postgres:
host: ${DB_HOST}
password: ${DB_PASSWORD} # ← from env or secret

🔒 Security & Compliance
Secrets: Never stored in code — injected via environment or Kubernetes Secrets
TLS: Enforced for all external communication (DB, Elasticsearch, APIs)
Input Validation: All user inputs sanitized and validated to prevent injection
PII Handling: Sensitive data (emails, phone numbers) encrypted at rest
Least Privilege: Database users and AWS roles follow minimal permissions

📊 Observability

Metrics
Prometheus metrics exposed on :9090:

worker_jobs_completed_total{task_type}
worker_jobs_failed_total{task_type, error_code}
worker_job_duration_seconds{task_type}
Database, API, and cache performance metrics

Logging
Structured JSON logs with context:

json

{
"level": "info",
"msg": "processing job",
"taskType": "validate-subscription",
"jobKey": 12345,
"workflowKey": 67890
}

Health Checks
GET /health → Liveness (Camunda + DB connectivity)
GET /ready → Readiness (safe to receive traffic)

🛠️ Developer Tools
CLI Utilities
bash

# Update activity registry

go run cmd/tools/registry-updater/main.go --id validate-subscription --status completed

# Scaffold new worker

go run cmd/tools/worker-generator/main.go --activity my-new-worker

Testing
Unit Tests: go test ./... (mocked dependencies, ≥80% coverage)

Integration Tests: go test -tags=integration ./... (real dependencies via Testcontainers)

📚 Documentation
Architecture: docs/architecture.md
Development Guide: docs/development-guide.md
Deployment Guide: docs/deployment-guide.md
Worker Specs: docs/workers/ (per-worker READMEs)

🤝 Support
For issues or enhancements, please open a GitHub issue with:

Camunda workflow ID
Job variables (sanitized)
Worker logs (with jobKey)
Expected vs actual behavior

Ready to power your franchise platform with event-driven workflows. 🚀

# Camunda Workflow API Gateway

Complete API Gateway for triggering all 25 Camunda workflows with JWT authentication, context storage, and comprehensive monitoring.

## 🚀 Features

- **25 Workflow Endpoints**: RESTful APIs for all Camunda workflows
- **JWT Authentication**: Secure token-based authentication
- **Context Storage**: Redis-based workflow context persistence
- **Rate Limiting**: IP-based request throttling
- **CORS Support**: Configurable cross-origin requests
- **Monitoring**: Prometheus metrics + Grafana dashboards
- **Health Checks**: Kubernetes-ready liveness/readiness probes
- **Graceful Shutdown**: Zero-downtime deployments
- **Docker Support**: Complete containerized setup

## 📋 Prerequisites

- Go 1.21+
- Docker & Docker Compose
- PostgreSQL 15+
- Redis 7+
- Elasticsearch 8+
- Camunda 8 (Zeebe)

## 🛠️ Quick Start

### 1. Clone Repository

```bash
git clone https://github.com/your-org/camunda-workers.git
cd camunda-workers
```

### 2. Configure Environment

```bash
cp .env.example .env
# Edit .env with your configuration
vim .env
```

**Required Environment Variables:**

```env
JWT_SECRET=your-super-secret-jwt-key-min-32-chars
POSTGRES_PASSWORD=your-postgres-password
REDIS_PASSWORD=your-redis-password
GOOGLE_CLIENT_ID=your-google-oauth-client-id
GOOGLE_CLIENT_SECRET=your-google-oauth-secret
LINKEDIN_CLIENT_ID=your-linkedin-oauth-client-id
LINKEDIN_CLIENT_SECRET=your-linkedin-oauth-secret
```

### 3. Start All Services (Docker Compose)

```bash
# Start all services
make docker-compose-up

# View logs
make docker-compose-logs

# Stop services
make docker-compose-down
```

**Services Started:**

- API Gateway: `http://localhost:8080`
- Worker Manager: (background service)
- PostgreSQL: `localhost:5432`
- Redis: `localhost:6379`
- Elasticsearch: `http://localhost:9200`
- Zeebe: `localhost:26500`
- Operate UI: `http://localhost:8081`
- Tasklist UI: `http://localhost:8082`
- Keycloak: `http://localhost:8180`
- Prometheus: `http://localhost:9091`
- Grafana: `http://localhost:3000`

### 4. Verify Installation

```bash
# Health check
curl http://localhost:8080/health

# Expected response:
# {
#   "status": "healthy",
#   "timestamp": "2024-01-01T12:00:00Z"
# }
```

## 🔐 Authentication

### Generate Test JWT Token

```bash
# Using make command
export JWT_SECRET="your-jwt-secret"
export TOKEN=$(go run scripts/generate-jwt.go \
  --userId="test-user-123" \
  --email="test@example.com" \
  --sessionId="sess-abc123" \
  --sourceSystem="web-app" \
  --roles="user,admin" \
  --subscriptionTier="premium")

echo $TOKEN
```

### Use Token in Requests

```bash
curl -X POST http://localhost:8080/api/v1/ai/query \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "question": "What are the best franchise opportunities?",
    "userId": "test-user-123"
  }'
```

## 📚 API Endpoints

### AI & Conversational

| Endpoint               | Method | Description                        |
| ---------------------- | ------ | ---------------------------------- |
| `/api/v1/ai/query`     | POST   | Start AI query workflow            |
| `/api/v1/ai/discovery` | POST   | Start franchise discovery workflow |

### Authentication

| Endpoint                       | Method | Description           |
| ------------------------------ | ------ | --------------------- |
| `/api/v1/auth/signup/google`   | POST   | Google OAuth signup   |
| `/api/v1/auth/signup/linkedin` | POST   | LinkedIn OAuth signup |
| `/api/v1/auth/signin/google`   | POST   | Google OAuth signin   |
| `/api/v1/auth/signin/linkedin` | POST   | LinkedIn OAuth signin |
| `/api/v1/auth/signin`          | POST   | Email/password signin |
| `/api/v1/auth/logout`          | POST   | User logout           |

### User Management

| Endpoint                      | Method | Description         |
| ----------------------------- | ------ | ------------------- |
| `/api/v1/user/signup`         | POST   | Traditional signup  |
| `/api/v1/user/profile/update` | POST   | Update user profile |
| `/api/v1/user/password/reset` | POST   | Password reset      |
| `/api/v1/user/account`        | DELETE | Delete account      |

### Franchise

| Endpoint                        | Method | Description           |
| ------------------------------- | ------ | --------------------- |
| `/api/v1/franchise/search`      | POST   | Search franchises     |
| `/api/v1/franchise/details/:id` | GET    | Get franchise details |

### Applications

| Endpoint                      | Method | Description                  |
| ----------------------------- | ------ | ---------------------------- |
| `/api/v1/application/submit`  | POST   | Submit franchise application |
| `/api/v1/application/approve` | POST   | Approve activity             |

### CRM

| Endpoint           | Method | Description        |
| ------------------ | ------ | ------------------ |
| `/api/v1/crm/sync` | POST   | Sync CRM user data |

### Email Campaigns

| Endpoint                       | Method | Description                |
| ------------------------------ | ------ | -------------------------- |
| `/api/v1/email/campaign`       | POST   | Start email campaign       |
| `/api/v1/email/welcome-series` | POST   | Start welcome email series |

### Social Auth

| Endpoint              | Method | Description               |
| --------------------- | ------ | ------------------------- |
| `/api/v1/social/auth` | POST   | Social auth orchestration |

### Error Handling

| Endpoint               | Method | Description                     |
| ---------------------- | ------ | ------------------------------- |
| `/api/v1/error/handle` | POST   | Trigger error handling workflow |

### Admin (Requires admin role)

| Endpoint                          | Method | Description           |
| --------------------------------- | ------ | --------------------- |
| `/api/admin/workflows`            | GET    | List active workflows |
| `/api/admin/workflows/:id/status` | GET    | Get workflow status   |
| `/api/admin/workflows/:id/cancel` | POST   | Cancel workflow       |

Full API documentation: [API_DOCUMENTATION.md](./docs/API_DOCUMENTATION.md)

## 🏗️ Project Structure

```
camunda-workers/
├── cmd/
│   ├── api-gateway/           # API Gateway main
│   ├── tools/                 # Utility tools
│   └── worker-manager/        # Worker manager main
├── internal/
│   ├── api/
│   │   ├── handlers/          # HTTP handlers
│   │   └── middleware/        # Middleware (JWT, CORS, etc.)
│   ├── common/                # Shared utilities
│   │   ├── auth/              # Authentication clients
│   │   ├── camunda/           # Camunda client
│   │   ├── config/            # Configuration
│   │   ├── database/          # Database clients
│   │   └── logger/            # Logging
│   ├── models/                # Domain models
│   └── workers/               # 25 Worker implementations
├── configs/
│   ├── api-config.yaml        # API configuration
│   ├── config.yaml            # Worker configuration
│   └── templates.json         # Response templates
├── deployments/
│   ├── docker/                # Docker files
│   └── kubernetes/            # K8s manifests
├── docs/                      # Documentation
├── scripts/                   # Utility scripts
├── test/                      # Tests
├── .env.example               # Environment template
├── docker-compose.yml         # Docker Compose setup
├── Dockerfile                 # API Gateway Dockerfile
├── Dockerfile.worker          # Worker Dockerfile
└── Makefile                   # Build commands
```

## 🔧 Development

### Local Development

```bash
# Install dependencies
make deps

# Run with hot reload (requires air)
make run-dev

# Run tests
make test

# Run with coverage
make test-coverage

# Lint code
make lint

# Format code
make format
```

### Build Binary

```bash
# Build for current platform
make build

# Build for all platforms
make release
```

### Database Migrations

```bash
# Run migrations up
make db-migrate-up

# Run migrations down
make db-migrate-down

# Create new migration
make db-migrate-create NAME=create_users_table
```

## 🧪 Testing

### Unit Tests

```bash
make test
```

### Integration Tests

```bash
make test-integration
```

### Load Testing (using hey)

```bash
# Install hey
go install github.com/rakyll/hey@latest

# Test AI query endpoint
export TOKEN="your-jwt-token"
hey -n 1000 -c 50 -m POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"question":"test"}' \
  http://localhost:8080/api/v1/ai/query
```

## 📊 Monitoring

### Prometheus Metrics

Access: `http://localhost:9091`

**Available Metrics:**

- `api_requests_total` - Total API requests
- `api_request_duration_seconds` - Request latency
- `workflow_starts_total` - Workflow initiations
- `workflow_active_instances` - Active workflows

### Grafana Dashboards

Access: `http://localhost:3000` (admin/admin)

**Pre-configured Dashboards:**

- API Performance
- Workflow Metrics
- Error Rates
- System Resources

### Health Checks

```bash
# API Gateway health
curl http://localhost:8080/health

# Prometheus health
curl http://localhost:9091/-/healthy

# Zeebe health
docker exec camunda-zeebe /usr/local/zeebe/bin/zbctl status --insecure
```

## 🔒 Security

### JWT Token Security

- **Secret**: Minimum 32 characters
- **Algorithm**: HS256 (HMAC-SHA256)
- **Expiry**: 24 hours (configurable)
- **Refresh**: 7 days (configurable)

### TLS/HTTPS

Enable in production:

```yaml
# configs/api-config.yaml
security:
  tls:
    enabled: true
    certFile: "/path/to/cert.pem"
    keyFile: "/path/to/key.pem"
```

### Rate Limiting

Current configuration:

- 100 requests/second per IP
- Burst capacity: 200 requests

Customize in `configs/api-config.yaml`:

```yaml
api:
  rateLimit:
    enabled: true
    requestsPerSecond: 100
    burst: 200
```

### CORS Configuration

```yaml
api:
  cors:
    allowOrigins:
      - "https://your-frontend.com"
    allowMethods:
      - "GET"
      - "POST"
    allowCredentials: true
```

## 🚢 Deployment

### Docker Deployment

```bash
# Build image
make docker-build

# Run container
make docker-run

# Push to registry
docker tag camunda-api-gateway:latest your-registry/camunda-api-gateway:latest
docker push your-registry/camunda-api-gateway:latest
```

### Kubernetes Deployment

```bash
# Apply configurations
kubectl apply -f deployments/kubernetes/

# Check pods
kubectl get pods -n camunda

# View logs
kubectl logs -f deployment/api-gateway -n camunda
```

### Production Checklist

- [ ] Set strong JWT secret
- [ ] Enable TLS/HTTPS
- [ ] Configure proper CORS origins
- [ ] Set up database backups
- [ ] Configure log aggregation
- [ ] Set up monitoring alerts
- [ ] Enable IP whitelisting (if needed)
- [ ] Review rate limits
- [ ] Set up CI/CD pipeline
- [ ] Document runbooks

## 🐛 Troubleshooting

### API Gateway won't start

```bash
# Check logs
docker logs camunda-api-gateway

# Common issues:
# 1. Port already in use
lsof -i :8080

# 2. Camunda not reachable
nc -zv localhost 26500

# 3. Database connection failed
psql -h localhost -U postgres -d lemici_db
```

### Workers not processing jobs

```bash
# Check worker logs
docker logs camunda-worker-manager

# Verify Zeebe connection
docker exec camunda-zeebe /usr/local/zeebe/bin/zbctl status --insecure

# Check workflow deployment
curl http://localhost:8081/api/processes
```

### JWT Authentication failing

```bash
# Verify token
export TOKEN="your-token"
echo $TOKEN | cut -d'.' -f2 | base64 -d | jq

# Check JWT secret
echo $JWT_SECRET

# Test with curl
curl -v http://localhost:8080/api/v1/ai/query \
  -H "Authorization: Bearer $TOKEN"
```

## 📖 Additional Resources

- [Complete API Documentation](./docs/API_DOCUMENTATION.md)
- [Worker Implementation Guide](./docs/WORKERS.md)
- [Architecture Overview](./docs/ARCHITECTURE.md)
- [Deployment Guide](./docs/DEPLOYMENT.md)
- [Contributing Guidelines](./CONTRIBUTING.md)

## 🤝 Contributing

1. Fork the repository
2. Create feature branch (`git checkout -b feature/amazing-feature`)
3. Commit changes (`git commit -m 'Add amazing feature'`)
4. Push to branch (`git push origin feature/amazing-feature`)
5. Open Pull Request

## 📝 License

This project is licensed under the MIT License - see [LICENSE](./LICENSE) file.

## 📧 Support

- Email: support@lemici.com
- Issues: https://github.com/your-org/camunda-workers/issues
- Slack: [Join our Slack](https://slack.lemici.com)

---

**Built with ❤️ by the LeMiCi Team**
