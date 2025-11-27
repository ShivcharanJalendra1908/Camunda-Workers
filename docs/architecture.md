
### 📄 `docs/architecture.md`
```markdown
# Architecture

## System Context
- **BFF**: Receives HTTP requests, starts Camunda workflows
- **Camunda 8**: Orchestrates workflows, manages job lifecycle
- **Workers**: Execute business logic, integrate with services
- **Databases**: PostgreSQL (OLTP), Elasticsearch (search), Redis (cache)
- **External Services**: GenAI API, AWS SES/SNS, Web Search API

## Data Flow
1. BFF → Camunda (Start Process)
2. Camunda → Worker (Job Assignment)
3. Worker → Services (DB, APIs)
4. Worker → Camunda (Complete Job)
5. Camunda → BFF (HTTP Callback)

## Component Diagram
[Diagram would be here in real doc]