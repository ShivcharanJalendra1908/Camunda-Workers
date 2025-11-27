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
│   ├── worker-manager/     # Main entry point
│   └── tools/              # CLI utilities (registry, scaffolding)
├── internal/
│   ├── workers/            # All 17 workers (grouped by domain)
│   ├── common/             # Shared utilities (logging, config, DB clients)
│   └── models/             # Shared data models
├── pkg/
│   └── registry/           # Activity registry loader
├── configs/                # YAML configs + activity registry
├── deployments/            # Docker Compose + Kubernetes manifests
├── scripts/                # Build, test, deploy helpers
└── docs/                   # Architecture, development, and worker guides

Each worker follows a standardized structure:

worker-name/
├── handler.go       # Camunda job handler
├── handler_test.go  # Unit tests (≥80% coverage)
├── config.go        # Worker-specific config
├── models.go        # Input/output structs
└── README.md        # Worker-specific documentation 

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
    password: ${DB_PASSWORD}  # ← from env or secret


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