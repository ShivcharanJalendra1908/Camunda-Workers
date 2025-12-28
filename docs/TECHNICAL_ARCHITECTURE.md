# Technical Architecture Documentation

This document provides detailed technical implementation documentation for the Camunda Workers system. It focuses on **how the code is implemented** rather than functional requirements.

## Table of Contents

1. [System Architecture Overview](#system-architecture-overview)
2. [Worker Manager (`cmd/worker-manager/main.go`)](#worker-manager)
3. [API Gateway (`cmd/api-gateway/main.go`)](#api-gateway)
4. [Worker Implementation Patterns](#worker-implementation-patterns)
5. [Database Layer Architecture](#database-layer-architecture)
6. [Error Handling Architecture](#error-handling-architecture)
7. [Configuration Management](#configuration-management)
8. [Logging and Observability](#logging-and-observability)
9. [API Handler Architecture](#api-handler-architecture)
10. [Individual Worker Technical Details](#individual-worker-technical-details)

---

## System Architecture Overview

### Core Components

The system consists of two main executables:

1. **Worker Manager** (`cmd/worker-manager/main.go`): Registers and manages all 27 Camunda workers
2. **API Gateway** (`cmd/api-gateway/main.go`): HTTP API server that triggers Camunda workflows

### Technology Stack

- **Language**: Go 1.21+
- **BPMN Engine**: Camunda Zeebe v8 (gRPC protocol)
- **Database**: PostgreSQL (via `database/sql`), Elasticsearch (via official client), Redis (go-redis/v9)
- **HTTP Framework**: Gin
- **Logging**: Zap (uber-go/zap)
- **Configuration**: Viper (spf13/viper) with YAML files
- **Observability**: Prometheus metrics, OpenTelemetry tracing

### Connection Management

All database connections use connection pooling:

- **PostgreSQL**: `sql.DB` with `SetMaxOpenConns()`, `SetMaxIdleConns()`, `SetConnMaxLifetime()`
- **Redis**: `redis.Client` with `PoolSize`, `MinIdleConns` configuration
- **Elasticsearch**: Official client with retry and timeout configuration

---

## Worker Manager

**File**: `cmd/worker-manager/main.go`

### Initialization Sequence

```go
1. Logger initialization (zap)
2. Config loading (config.Load())
3. Observability setup (observability.New())
4. Zeebe client connection (with retry logic)
5. PostgreSQL connection (with retry logic)
6. Elasticsearch connection (with retry logic)
7. Redis connection (with retry logic)
8. External service clients (Keycloak, Zoho)
9. Worker registration (all 27 workers)
10. Health/metrics server (HTTP on :8080)
11. Graceful shutdown handler
```

### Retry Mechanism

Uses exponential backoff for connection initialization:

```go
func retryWithBackoff(operation func() error, maxRetries int, initialDelay time.Duration, log *zap.Logger, operationName string) error {
    delay := initialDelay
    for i := 0; i < maxRetries; i++ {
        err = operation()
        if err == nil { return nil }
        if i < maxRetries-1 {
            time.Sleep(delay)
            delay *= 2 // Exponential backoff
        }
    }
    return fmt.Errorf("%s failed after %d attempts: %w", operationName, maxRetries, err)
}
```

**Retry Configuration**:
- Zeebe: 10 retries, 2s initial delay
- PostgreSQL: 15 retries, 2s initial delay
- Elasticsearch: 15 retries, 2s initial delay
- Redis: 10 retries, 2s initial delay

### Worker Registration Pattern

Each worker follows this pattern:

```go
if cfg.Workers[taskType].Enabled {
    handler := workerPackage.NewHandler(
        &workerPackage.Config{...},
        dependencies..., // DB, Redis, ES, Logger
    )
    startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
}
```

### `startWorker` Function

```go
func startWorker(client zbc.Client, taskType string, wcfg config.WorkerConfig, handlerFunc func(worker.JobClient, entities.Job), log *zap.Logger) {
    client.NewJobWorker().
        JobType(taskType).
        Handler(handlerFunc).
        MaxJobsActive(wcfg.MaxJobsActive).
        Timeout(time.Duration(wcfg.Timeout) * time.Millisecond).
        Open()
}
```

**Key Parameters**:
- `MaxJobsActive`: Maximum concurrent jobs (from config)
- `Timeout`: Job timeout in milliseconds (from config)
- `JobType`: Task type string (e.g., "validate-subscription")

### Logger Adapters

AI workers use custom logger interfaces. Adapters bridge between `logger.Logger` and worker-specific interfaces:

```go
type parseUserIntentLoggerAdapter struct {
    logger.Logger
}

func (a *parseUserIntentLoggerAdapter) With(fields map[string]interface{}) pui.Logger {
    return &parseUserIntentLoggerAdapter{a.Logger.With(fields)}
}
```

### Health and Metrics Server

Runs on port 8080:

- `/health`: Returns `{"status": "healthy", "time": "..."}`
- `/ready`: Returns `{"status": "ready", "time": "..."}`
- `/metrics`: Prometheus metrics endpoint

### Graceful Shutdown

```go
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
<-sigCh

shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

zeebeClient.Close()
```

---

## API Gateway

**File**: `cmd/api-gateway/main.go`

### Initialization Sequence

```go
1. Config loading
2. Logger initialization (structured logger)
3. Keycloak client initialization
4. Camunda client initialization (camunda.NewClientFromEnv())
5. PostgreSQL connection
6. Gin router setup
7. Middleware registration (CORS, auth, rate limiting)
8. Route registration
9. HTTP server startup
10. Graceful shutdown
```

### Camunda Client Initialization

Uses environment-based configuration:

```go
camundaClient, err := camunda.NewClientFromEnv()
```

The `camunda.Client` wraps Zeebe gRPC client and provides workflow management methods.

### Route Structure

Routes are organized by domain:
- `/api/v1/ai/*` - AI conversation workflows
- `/api/v1/auth/*` - Authentication workflows
- `/api/v1/franchise/*` - Franchise management workflows
- `/api/v1/application/*` - Application workflows

### Middleware Chain

1. CORS middleware
2. Request ID middleware
3. Authentication middleware (JWT validation via Keycloak)
4. Rate limiting middleware
5. Logging middleware

---

## Worker Implementation Patterns

### Standard Handler Structure

All workers follow this pattern:

```go
type Handler struct {
    config *Config
    db     *sql.DB          // If needed
    redis  *redis.Client    // If needed
    esClient *database.ElasticsearchClient // If needed
    logger logger.Logger
}

func NewHandler(config *Config, dependencies..., log logger.Logger) *Handler {
    return &Handler{
        config: config,
        dependencies...,
        logger: log.WithFields(map[string]interface{}{"taskType": TaskType}),
    }
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
    // 1. Parse input
    var input Input
    json.Unmarshal([]byte(job.Variables), &input)
    
    // 2. Create context with timeout
    ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
    defer cancel()
    
    // 3. Execute business logic
    output, err := h.execute(ctx, &input)
    
    // 4. Handle result
    if err != nil {
        h.failJob(client, job, errorCode, err.Error(), retries)
        return
    }
    
    h.completeJob(client, job, output)
}
```

### Job Variable Parsing

In Zeebe v8, `job.Variables` is a **JSON string**, not a map:

```go
var input Input
if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
    h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
    return
}
```

### Context Management

All database operations use context with timeout:

```go
ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
defer cancel()

// Use ctx in all DB operations
err := h.db.QueryRowContext(ctx, query, args...).Scan(...)
```

### Job Completion

```go
func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
    cmd, err := client.NewCompleteJobCommand().
        JobKey(job.Key).
        VariablesFromObject(output)
    if err != nil {
        h.logger.Error("failed to create complete job command", ...)
        return
    }
    _, err = cmd.Send(context.Background())
    if err != nil {
        h.logger.Error("failed to send complete job command", ...)
    }
}
```

### Job Failure

Two patterns are used:

**Pattern 1: Throw BPMN Error** (for non-retryable errors)
```go
func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, retries int32) {
    _, err := client.NewThrowErrorCommand().
        JobKey(job.Key).
        ErrorCode(errorCode).
        ErrorMessage(errorMessage).
        Send(context.Background())
}
```

**Pattern 2: Fail Job with Retries** (for retryable errors)
```go
func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, retries int32) {
    _, err := client.NewFailJobCommand().
        JobKey(job.Key).
        Retries(retries).
        ErrorMessage(errorMessage).
        Send(context.Background())
}
```

### Error Classification

Workers classify errors to determine retry behavior:

```go
if errors.Is(err, ErrSubscriptionInvalid) || errors.Is(err, ErrSubscriptionExpired) {
    errorCode = err.Error()
    retries = 0  // No retries for business logic errors
} else if errors.Is(err, ErrSubscriptionCheckFailed) {
    errorCode = "SUBSCRIPTION_CHECK_FAILED"
    retries = 3  // Retry for transient errors
}
```

---

## Database Layer Architecture

### PostgreSQL Client

**File**: `internal/common/database/postgres.go`

**Connection Pool Configuration**:
```go
db.SetMaxOpenConns(cfg.MaxConnections)
db.SetMaxIdleConns(cfg.MaxIdle)
db.SetConnMaxLifetime(5 * time.Minute)
db.SetConnMaxIdleTime(5 * time.Minute)
```

**DSN Construction**:
```go
func (p PostgresConfig) GetDSN() string {
    if p.ConnectionString != "" {
        return p.ConnectionString  // Prefer connection string
    }
    return fmt.Sprintf(
        "host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
        p.Host, p.Port, user, p.Password, p.Database, p.SSLMode,
    )
}
```

### Query Registry Pattern

**File**: `internal/workers/data-access/query-postgresql/queries/registry.go`

Uses a registry pattern for query execution:

```go
type QueryFunc func(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error)

var Registry = map[models.QueryType]QueryFunc{
    models.QueryTypeFranchiseFullDetails:  FranchiseFullDetails,
    models.QueryTypeFranchiseOutlets:      FranchiseOutlets,
    // ...
}

func Execute(ctx context.Context, db *sql.DB, queryType models.QueryType, params map[string]interface{}) (interface{}, int, int64, error) {
    fn, exists := Registry[queryType]
    if !exists {
        return nil, 0, 0, fmt.Errorf("%w: %s", ErrUnknownQueryType, queryType)
    }
    return fn(ctx, db, params)
}
```

**Query Execution Flow**:
1. Handler receives `QueryType` from input
2. Looks up query function in registry
3. Executes query with context and parameters
4. Returns data, row count, execution time, and error

### Transaction Management

Transactions are used for multi-step operations:

```go
tx, err := h.db.BeginTx(ctx, nil)
if err != nil {
    return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
}
defer tx.Rollback()  // Always rollback on error

// Execute operations
err = tx.QueryRowContext(ctx, query, args...).Scan(...)
if err != nil {
    return nil, err  // Rollback happens via defer
}

if err := tx.Commit(); err != nil {
    return nil, fmt.Errorf("%w: commit transaction: %v", ErrDatabaseError, err)
}
```

### Dynamic Query Building

Some workers build queries dynamically based on input:

```go
func (h *Handler) buildUpdateQuery(franchiseID, updatedBy uuid.UUID, input *UpdateFranchiseInput) (string, []interface{}) {
    query := "UPDATE franchises SET updated_by = $1, updated_at = $2"
    args := []interface{}{updatedBy, time.Now()}
    argPos := 3
    
    if input.Name != nil {
        query += fmt.Sprintf(", name = $%d", argPos)
        args = append(args, *input.Name)
        argPos++
    }
    // ... more fields
    
    query += fmt.Sprintf(" WHERE id = $%d RETURNING updated_at", argPos)
    args = append(args, franchiseID)
    
    return query, args
}
```

### Redis Caching Pattern

**File**: `internal/common/database/redis.go`

**Connection Configuration**:
```go
rdb := redis.NewClient(&redis.Options{
    Addr:         cfg.Address,
    Password:     cfg.Password,
    DB:           cfg.DB,
    DialTimeout:  time.Duration(cfg.DialTimeout) * time.Second,
    ReadTimeout:  time.Duration(cfg.ReadTimeout) * time.Second,
    WriteTimeout: time.Duration(cfg.WriteTimeout) * time.Second,
    PoolSize:     cfg.PoolSize,
    MinIdleConns: 5,
    MaxRetries:   cfg.MaxRetries,
})
```

**Cache-Aside Pattern**:
```go
cacheKey := "sub:" + input.UserID
if val, err := h.redis.Get(ctx, cacheKey).Result(); err == nil {
    var sub Subscription
    if err := json.Unmarshal([]byte(val), &sub); err == nil {
        return &Output{IsValid: sub.IsValid, TierLevel: sub.Tier}, nil
    }
}

// Cache miss - query database
var sub Subscription
err := h.db.QueryRowContext(ctx, query, input.UserID).Scan(...)

// Store in cache
data, _ := json.Marshal(sub)
h.redis.Set(ctx, cacheKey, data, 5*time.Minute)
```

### Elasticsearch Client

**File**: `internal/common/database/elasticsearch.go`

Uses official Elasticsearch Go client with configuration:

```go
cfg := elasticsearch.Config{
    Addresses: cfg.Addresses,
    Username:  cfg.Username,
    Password:  cfg.Password,
    // ...
}
client, err := elasticsearch.NewClient(cfg)
```

**Search Execution**:
```go
func (c *ElasticsearchClient) SearchDocuments(ctx context.Context, index string, query map[string]interface{}) (map[string]interface{}, error) {
    var buf bytes.Buffer
    if err := json.NewEncoder(&buf).Encode(query); err != nil {
        return nil, err
    }
    
    res, err := c.Client.Search(
        c.Client.Search.WithContext(ctx),
        c.Client.Search.WithIndex(index),
        c.Client.Search.WithBody(&buf),
    )
    // ... parse response
}
```

---

## Error Handling Architecture

### Error Types

Workers define custom error types:

```go
var (
    ErrSubscriptionInvalid     = errors.New("SUBSCRIPTION_INVALID")
    ErrSubscriptionExpired     = errors.New("SUBSCRIPTION_EXPIRED")
    ErrSubscriptionCheckFailed = errors.New("SUBSCRIPTION_CHECK_FAILED")
)
```

### Error Wrapping

Uses `fmt.Errorf` with `%w` verb for error wrapping:

```go
return nil, fmt.Errorf("%w: %v", ErrDatabaseError, err)
```

### Error Checking

Uses `errors.Is()` for error comparison:

```go
if errors.Is(err, ErrSubscriptionInvalid) {
    // Handle specific error
}
```

### Context Timeout Handling

```go
if ctx.Err() == context.DeadlineExceeded {
    return nil, ErrQueryTimeout
}
```

### Database Error Handling

```go
if err != nil {
    if err == sql.ErrNoRows {
        return nil, ErrFranchiseNotFound
    }
    return nil, fmt.Errorf("%w: query franchise: %v", ErrDatabaseError, err)
}
```

---

## Configuration Management

### Config Structure

**File**: `internal/common/config/config.go`

Uses Viper with YAML files. Config is loaded via `config.Load()` which:
1. Reads `configs/config.yaml` as base
2. Overrides with environment-specific files (e.g., `config.dev.yaml`)
3. Applies environment variable overrides

### Worker Configuration

Each worker has configuration in `config.yaml`:

```yaml
workers:
  validate-subscription:
    enabled: true
    max_jobs_active: 10
    timeout: 5000
    max_retries: 3
```

Accessed in code:
```go
cfg.Workers[vs.TaskType].Enabled
cfg.Workers[vs.TaskType].MaxJobsActive
cfg.Workers[vs.TaskType].Timeout
```

### Database Configuration

```yaml
database:
  postgres:
    host: localhost
    port: 5432
    database: camunda_workers
    user: postgres
    password: password
    max_connections: 25
    max_idle: 5
    sslmode: disable
```

### API Configuration

```yaml
api:
  port: 8080
  readTimeout: 30
  writeTimeout: 30
  cors:
    allowOrigins: ["*"]
    allowMethods: ["GET", "POST", "PUT", "DELETE"]
```

---

## Logging and Observability

### Logger Interface

**File**: `internal/common/logger/logger.go`

Defines a minimal logging interface:

```go
type Logger interface {
    Debug(msg string, fields map[string]interface{})
    Info(msg string, fields map[string]interface{})
    Warn(msg string, fields map[string]interface{})
    Error(msg string, fields map[string]interface{})
    WithFields(fields map[string]interface{}) Logger
    With(fields map[string]interface{}) Logger
}
```

### Zap Adapter

Wraps `zap.Logger` to implement the interface:

```go
type zapWrapper struct {
    l *zap.Logger
}

func (z *zapWrapper) Info(msg string, fields map[string]interface{}) {
    z.l.Info(msg, mapToZapFields(fields)...)
}

func mapToZapFields(fields map[string]interface{}) []zap.Field {
    out := make([]zap.Field, 0, len(fields))
    for k, v := range fields {
        out = append(out, zap.Any(k, v))
    }
    return out
}
```

### Structured Logging

All logs use structured fields:

```go
h.logger.Info("processing job", map[string]interface{}{
    "jobKey":      job.Key,
    "workflowKey": job.ProcessInstanceKey,
})
```

### Logger Context

Workers create logger with task type context:

```go
logger: log.WithFields(map[string]interface{}{"taskType": TaskType})
```

---

## API Handler Architecture

### Handler Structure

**File**: `internal/api/handlers/workflow_handler.go`

```go
type WorkflowHandler struct {
    camunda *camunda.Client
    logger  logger.Logger
}

func NewWorkflowHandler(camunda *camunda.Client, logger logger.Logger) *WorkflowHandler {
    return &WorkflowHandler{
        camunda: camunda,
        logger:  logger,
    }
}
```

### Request Processing Flow

1. **Bind JSON input**:
```go
var input struct {
    Question string                 `json:"question" binding:"required"`
    Context  map[string]interface{} `json:"context"`
    UserID   string                 `json:"userId"`
}

if err := c.ShouldBindJSON(&input); err != nil {
    c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
    return
}
```

2. **Extract JWT claims**:
```go
claims := middleware.ExtractClaims(c)
if claims == nil {
    claims = &middleware.Claims{} // Default empty claims
}
```

3. **Build workflow variables**:
```go
variables := map[string]interface{}{
    "question":         input.Question,
    "userId":           getOrDefault(input.UserID, claims.UserID),
    "sessionId":        claims.SessionID,
    "sourceSystem":     claims.SourceSystem,
    "subscriptionTier": claims.SubscriptionTier,
    "requestId":        uuid.New().String(),
}
```

4. **Start workflow**:
```go
response := h.startWorkflow(c.Request.Context(), "ai_query", variables)
c.JSON(http.StatusOK, response)
```

### Workflow Start Implementation

```go
func (h *WorkflowHandler) startWorkflow(ctx context.Context, processID string, variables map[string]interface{}) *WorkflowResponse {
    instance, err := h.camunda.StartProcessInstance(ctx, processID, variables)
    if err != nil {
        return &WorkflowResponse{
            Status:  "error",
            Message: err.Error(),
        }
    }
    
    return &WorkflowResponse{
        WorkflowInstanceKey: instance.ProcessInstanceKey,
        ProcessID:           processID,
        RequestID:           getRequestID(variables),
        Status:              "started",
        Message:             "Workflow started successfully",
        Variables:           variables,
    }
}
```

### Helper Functions

```go
func getOrDefault(value, defaultValue string) string {
    if value != "" {
        return value
    }
    return defaultValue
}
```

---

## Individual Worker Technical Details

### 1. validate-subscription

**File**: `internal/workers/infrastructure/validate-subscription/handler.go`

**Dependencies**: PostgreSQL, Redis

**Implementation Details**:
- Uses Redis cache with 5-minute TTL
- Cache key format: `"sub:" + input.UserID`
- Validates subscription tier against whitelist: `["free", "basic", "premium", "enterprise"]`
- Handles expiration date parsing with error tolerance (logs warning, continues)
- SQL query: `SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = $1`

**Error Handling**:
- `SUBSCRIPTION_INVALID`: No retries
- `SUBSCRIPTION_EXPIRED`: No retries
- `SUBSCRIPTION_CHECK_FAILED`: 3 retries

### 2. query-postgresql

**File**: `internal/workers/data-access/query-postgresql/handler.go`

**Dependencies**: PostgreSQL

**Implementation Details**:
- Uses query registry pattern (`queries.Registry`)
- Supports multiple query types via `QueryType` enum
- Returns: data, row count, execution time (ms)
- Parameter mapping: `franchiseId`, `franchiseIds`, `userId`, `filters`

**Query Execution**:
```go
data, rowCount, execTime, err := queries.Execute(ctx, h.db, queryType, params)
```

**Error Handling**:
- `QUERY_TIMEOUT`: 2 retries
- `INVALID_QUERY_TYPE`: No retries
- `QUERY_EXECUTION_FAILED`: No retries

### 3. franchise-postgres

**File**: `internal/workers/data-access/franchise-postgres/handler.go`

**Dependencies**: PostgreSQL

**Implementation Details**:
- **Operation-based routing**: Uses `operation_type` field to route to 22 different operations
- **Transaction management**: All CREATE/UPDATE operations use transactions
- **Dynamic query building**: UPDATE operations build queries dynamically based on provided fields
- **UUID validation**: Validates all UUID inputs before database operations
- **JSONB handling**: Serializes complex types (products, services, staff_breakdown) to JSONB

**Operations Supported**:
- Franchise: CREATE, UPDATE, GET, DELETE, GET_FULL
- Business Overview: CREATE, UPDATE
- Investment: CREATE, UPDATE
- Operations: CREATE, UPDATE
- Social Links: CREATE, UPDATE
- Stats: CREATE, UPDATE
- Category Questions: CREATE, GET, UPDATE, DELETE
- Franchise Cities: CREATE, GET, DELETE

**Validation**:
- Email format validation (contains "@" and ".")
- URL validation (must start with "http://" or "https://")
- Year validation (1800 to current year)
- Positive number validation for counts

**Transaction Pattern**:
```go
tx, err := h.db.BeginTx(ctx, nil)
defer tx.Rollback()
// ... operations
if err := tx.Commit(); err != nil {
    return nil, fmt.Errorf("%w: commit transaction: %v", ErrDatabaseError, err)
}
```

### 4. query-elasticsearch

**File**: `internal/workers/data-access/query-elasticsearch/handler.go`

**Dependencies**: Elasticsearch

**Implementation Details**:
- Accepts raw Elasticsearch query as JSON
- Executes search via `esClient.SearchDocuments()`
- Returns raw Elasticsearch response
- Context timeout handling

**Query Format**:
```json
{
  "query": {...},
  "from": 0,
  "size": 20,
  "sort": [...]
}
```

### 5. parse-user-intent

**File**: `internal/workers/ai-conversation/parse-user-intent/handler.go`

**Dependencies**: HTTP client (GenAI API)

**Implementation Details**:
- **Custom logger interface**: Uses `Logger` interface instead of `logger.Logger`
- **HTTP client with timeout**: `&http.Client{Timeout: config.Timeout}`
- **Retry logic with exponential backoff**:
```go
for attempt := 0; attempt <= h.config.MaxRetries; attempt++ {
    if attempt > 0 {
        backoff := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
        time.Sleep(backoff)
    }
    resp, lastErr = h.client.Do(req)
    if resp.StatusCode == http.StatusOK {
        break
    }
}
```
- **Context timeout checking**: Checks `ctx.Err()` after each attempt
- **Data source determination**: Falls back to `determineDataSources()` if API doesn't return sources

**API Request**:
```go
POST {GenAIBaseURL}/api/ai/parse-intent
Body: {"query": "...", "context": {...}}
```

**Error Handling**:
- `INTENT_API_TIMEOUT`: 1 retry
- `INTENT_PARSING_FAILED`: 2 retries

### 6. search-franchises

**File**: `internal/workers/franchise/search-franchises/handler.go`

**Dependencies**: Elasticsearch

**Implementation Details**:
- **Multi-index search**: Searches across multiple indices based on category
- **Query building**: Builds Elasticsearch bool query with must clauses
- **Field boosting**: `"brand^3", "description^2"` in multi_match
- **Fuzziness**: Configurable fuzziness (default "AUTO")
- **Aggregations**: Optional aggregations for facets
- **Suggestions**: Uses Elasticsearch completion suggester

**Index Selection**:
```go
func (h *Handler) getIndicesToSearch(category string) []string {
    switch strings.ToLower(category) {
    case "food", "food & beverage":
        return []string{"food_beverage_franchises"}
    case "education", "training":
        return []string{"education_franchises"}
    default:
        return []string{"food_beverage_franchises", "education_franchises", "fashion_franchises"}
    }
}
```

**Query Building**:
- Multi-match for text search
- Term filters for exact matches (location, tags)
- Range filters for numeric ranges (investment, space, rating)
- Sorting: `_score` (relevance) + `rating` (descending)

**Variable Parsing**:
Handles both string and array formats for tags:
```go
if tags, ok := vars["tags"].(string); ok {
    input.Tags = strings.Split(tags, ",")
} else if tagsList, ok := vars["tags"].([]interface{}); ok {
    for _, tag := range tagsList {
        if tagStr, ok := tag.(string); ok {
            input.Tags = append(input.Tags, tagStr)
        }
    }
}
```

### 7. calculate-match-score

**File**: `internal/workers/franchise/calculate-match-score/handler.go`

**Dependencies**: PostgreSQL, Redis

**Implementation Details**:
- **Caching**: Caches match scores in Redis with 10-minute TTL
- **Cache key**: `"match:" + userID + ":" + franchiseID`
- **Score calculation**: Complex algorithm considering multiple factors
- **Database queries**: Joins multiple tables for user preferences and franchise data

### 8. apply-relevance-ranking

**File**: `internal/workers/franchise/apply-relevance-ranking/handler.go`

**Implementation Details**:
- **In-memory sorting**: Sorts results by relevance score
- **Max items limit**: Configurable max items (default 100)
- **Score normalization**: Normalizes scores to 0-1 range

### 9. parse-search-filters

**File**: `internal/workers/franchise/parse-search-filters/handler.go`

**Implementation Details**:
- **Filter parsing**: Parses raw filter input into structured format
- **Validation**: Validates filter ranges and values
- **Normalization**: Normalizes filter values (e.g., category names)

### 10. validate-application-data

**File**: `internal/workers/application/validate-application-data/handler.go`

**Implementation Details**:
- **Field validation**: Validates required fields
- **Format validation**: Email, phone, date formats
- **Business rule validation**: Custom business rules

### 11. check-readiness-score

**File**: `internal/workers/application/check-readiness-score/handler.go`

**Implementation Details**:
- **Score calculation**: Calculates readiness score based on multiple factors
- **Threshold checking**: Compares score against thresholds
- **Output**: Returns score and recommendations

### 12. check-priority-routing

**File**: `internal/workers/application/check-priority-routing/handler.go`

**Dependencies**: PostgreSQL, Redis

**Implementation Details**:
- **Caching**: 30-minute TTL for routing decisions
- **Priority calculation**: Based on application characteristics
- **Route selection**: Selects appropriate workflow route

### 13. create-application-record

**File**: `internal/workers/application/create-application-record/handler.go`

**Dependencies**: PostgreSQL

**Implementation Details**:
- **Transaction**: Single transaction for application creation
- **UUID generation**: Uses `google/uuid` for IDs
- **Duplicate checking**: Checks for existing applications
- **Timestamp handling**: Sets `created_at` and `updated_at`

### 14. send-notification

**File**: `internal/workers/application/send-notification/handler.go`

**Dependencies**: PostgreSQL

**Implementation Details**:
- **Template loading**: Loads notification templates
- **Variable substitution**: Replaces template variables
- **Multi-channel**: Supports email, SMS, push
- **Delivery tracking**: Records delivery status in database

### 15-19. Authentication Workers

**Files**: `internal/workers/auth/*/handler.go`

**Common Pattern**:
- **OAuth flow**: Handles OAuth2 authorization code flow
- **Token exchange**: Exchanges authorization code for tokens
- **User lookup**: Checks for existing users
- **JWT generation**: Creates JWT tokens via Keycloak
- **Session management**: Creates/updates sessions

**Providers Supported**:
- Google OAuth2
- LinkedIn OAuth2

**Operations**:
- Sign in (existing user)
- Sign up (new user)
- Logout (session invalidation)

### 20. captcha-verify

**File**: `internal/workers/auth/captcha-verify/handler.go`

**Implementation Details**:
- **reCAPTCHA verification**: Calls Google reCAPTCHA API
- **Score validation**: Validates score against threshold
- **HTTP request**: POST to reCAPTCHA verify endpoint

### 21. email-send

**File**: `internal/workers/communication/email-send/handler.go`

**Implementation Details**:
- **Template rendering**: Renders email templates
- **SMTP/SendGrid**: Supports multiple email providers
- **Attachment handling**: Handles email attachments
- **Delivery tracking**: Tracks email delivery status

### 22. crm-user-create

**File**: `internal/workers/crm/crm-user-create/handler.go`

**Dependencies**: Zoho CRM API

**Implementation Details**:
- **Zoho API integration**: Creates user in Zoho CRM
- **Data mapping**: Maps internal user data to Zoho format
- **Error handling**: Handles Zoho API errors

### 23-27. AI Conversation Workers

**Files**: `internal/workers/ai-conversation/*/handler.go`

**Common Patterns**:
- **HTTP client with retries**: All use HTTP clients with retry logic
- **Context timeout**: All use context with timeout
- **Custom logger interfaces**: Use worker-specific logger interfaces
- **API integration**: Integrate with GenAI and WebSearch APIs

**query-internal-data**:
- Searches PostgreSQL and Elasticsearch
- Caches results in Redis
- Combines results from multiple sources

**enrich-web-search**:
- Calls web search API
- Filters results by relevance score
- Returns top N results

**llm-synthesis**:
- Calls GenAI API for text synthesis
- Handles token limits
- Returns synthesized text

---

## Tools

### Worker Generator

**File**: `cmd/tools/worker-generator/main.go`

**Purpose**: Generates boilerplate code for new workers

**Templates**:
- Handler template
- Service template
- Models template
- Config template

**Usage**:
```bash
go run cmd/tools/worker-generator/main.go --name "my-worker" --task-type "my-task"
```

### Registry Updater

**File**: `cmd/tools/registry-updater/main.go`

**Purpose**: Updates activity registry JSON file

**Functionality**:
- Reads activity registry
- Adds/updates worker entries
- Validates JSON structure
- Writes updated registry

---

## Development Patterns

### Adding a New Worker

1. Create worker directory: `internal/workers/{category}/{worker-name}/`
2. Create files:
   - `handler.go` - Main handler implementation
   - `models.go` - Input/output structs
   - `config.go` - Configuration struct
   - `README.md` - Documentation
3. Register in `cmd/worker-manager/main.go`
4. Add config in `configs/config.yaml`
5. Add entry to `configs/activity-registry.json`

### Testing Patterns

Workers expose `Execute()` method for direct testing:

```go
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
    return h.execute(ctx, input)
}
```

This allows testing business logic without Zeebe:

```go
handler := NewHandler(config, db, log)
output, err := handler.Execute(ctx, &Input{...})
```

---

## Performance Considerations

### Connection Pooling

- PostgreSQL: Configured via `MaxOpenConns`, `MaxIdleConns`
- Redis: Configured via `PoolSize`, `MinIdleConns`
- Elasticsearch: Managed by official client

### Caching Strategy

- **Cache-aside pattern**: Check cache, query DB on miss, update cache
- **TTL-based expiration**: Configurable TTL per cache entry
- **Cache key format**: `"{prefix}:{identifier}"`

### Query Optimization

- **Prepared statements**: All queries use parameterized statements
- **Index usage**: Queries designed to use database indexes
- **Connection reuse**: Connection pooling reduces connection overhead

### Timeout Management

- **Context timeouts**: All operations use context with timeout
- **Configurable timeouts**: Per-worker timeout configuration
- **Graceful degradation**: Timeouts prevent resource exhaustion

---

## Security Considerations

### Input Validation

- **JSON schema validation**: All inputs validated against struct tags
- **Type checking**: Type assertions for dynamic values
- **Range validation**: Numeric ranges validated
- **Format validation**: Email, URL, UUID formats validated

### SQL Injection Prevention

- **Parameterized queries**: All queries use `$1, $2, ...` placeholders
- **No string concatenation**: Queries built with parameter arrays

### Authentication

- **JWT validation**: All API requests validated via Keycloak
- **OAuth2 flows**: Secure OAuth2 implementation
- **Session management**: Secure session handling

---

## Monitoring and Observability

### Metrics

- Prometheus metrics endpoint: `/metrics`
- Custom metrics: Job processing time, error rates
- Database metrics: Connection pool stats, query times

### Logging

- Structured logging: All logs use structured fields
- Log levels: Debug, Info, Warn, Error
- Context propagation: Job keys, workflow keys in logs

### Health Checks

- `/health`: Basic health check
- `/ready`: Readiness check (checks dependencies)
- Dependency checks: Database, Redis, Elasticsearch connectivity

---

## Deployment Considerations

### Configuration

- Environment-based configs: `config.dev.yaml`, `config.prod.yaml`
- Environment variable overrides
- Secret management: Passwords, API keys from environment

### Scaling

- **Horizontal scaling**: Multiple worker manager instances
- **Worker concurrency**: `MaxJobsActive` controls concurrency
- **Database connections**: Connection pool limits per instance

### Graceful Shutdown

- Signal handling: SIGTERM, SIGINT
- Context timeout: 30-second shutdown timeout
- Resource cleanup: Close connections, stop workers

---

## Conclusion

This technical architecture document provides implementation-level details for the Camunda Workers system. For functional requirements and business logic, refer to the functional documentation.

Key architectural patterns:
1. **Registry pattern** for query execution
2. **Adapter pattern** for logger interfaces
3. **Factory pattern** for handler creation
4. **Cache-aside pattern** for caching
5. **Transaction pattern** for multi-step operations
6. **Retry pattern** with exponential backoff
7. **Context pattern** for timeout and cancellation

