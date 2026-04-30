package authlogout

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/redis/go-redis/v9"

	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/circuitbreaker"
	"camunda-workers/internal/common/config"
	cerrors "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/metrics"
	"camunda-workers/internal/common/validation"
	"database/sql"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "auth-logout"
)

type Handler struct {
	config       *Config
	logger       logger.Logger
	camunda      *camunda.Client
	keycloak     *auth.KeycloakClient
	service      *Service
	jobWorker    worker.JobWorker
	errorHandler *cerrors.ErrorHandler
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
	cbManager    *circuitbreaker.Manager
}

type HandlerOptions struct {
	AppConfig    *config.Config
	Camunda      *camunda.Client
	Keycloak     *auth.KeycloakClient
	CustomConfig *Config
	Logger       logger.Logger
	CBManager    *circuitbreaker.Manager
	RedisClient  *redis.Client
	DB           *sql.DB
}

func NewHandler(opts HandlerOptions) (*Handler, error) {
	workerConfig := createConfigFromAppConfig(opts.AppConfig, opts.CustomConfig)

	if err := workerConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration for auth-logout: %w", err)
	}

	var loggerInstance logger.Logger
	if opts.Logger != nil {
		loggerInstance = opts.Logger
	} else {
		loggerInstance = logger.NewStructured("info", "json")
	}

	// Create circuit breaker manager if not provided
	var cbManager *circuitbreaker.Manager
	if opts.CBManager != nil {
		cbManager = opts.CBManager
	} else {
		cbManager = circuitbreaker.NewManager()
	}

	handler := &Handler{
		config:       workerConfig,
		logger:       loggerInstance.WithFields(map[string]interface{}{"taskType": TaskType}),
		camunda:      opts.Camunda,
		keycloak:     opts.Keycloak,
		errorHandler: cerrors.NewErrorHandler(loggerInstance),
		validator:    validation.NewValidator(),
		sanitizer:    validation.NewSanitizer(),
		cbManager:    cbManager,
	}

	handler.service = NewService(ServiceDependencies{
		Keycloak:    handler.keycloak,
		Logger:      loggerInstance,
		RedisClient: opts.RedisClient,
		DB:          opts.DB,
	}, handler.config)

	return handler, nil
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	// ✅ EXTRACT TRACE CONTEXT
	ctx := context.Background()

	var traceID, parentSpanID string
	var jobVars map[string]interface{}

	if err := json.Unmarshal([]byte(job.Variables), &jobVars); err == nil {
		if tid, ok := jobVars["traceId"].(string); ok {
			traceID = tid
		}
		if psid, ok := jobVars["spanId"].(string); ok {
			parentSpanID = psid
		}
	}

	// ✅ CREATE WORKER SPAN
	tracer := otel.Tracer("worker-manager")
	ctx, span := tracer.Start(ctx, "worker:"+TaskType,
		trace.WithAttributes(
			attribute.String("worker.name", TaskType),
			attribute.Int64("job.key", job.GetKey()),
			attribute.Int64("workflow.instance_key", job.GetProcessInstanceKey()),
			attribute.String("workflow.process_id", job.GetBpmnProcessId()),
			attribute.String("workflow.element_id", job.GetElementId()),
			attribute.String("trace.parent_id", parentSpanID),
		),
	)
	defer span.End()

	startTime := time.Now()
	metrics.WorkerJobsActive.WithLabelValues(TaskType).Inc()
	defer metrics.WorkerJobsActive.WithLabelValues(TaskType).Dec()

	h.logger.Info("Processing auth logout request", map[string]interface{}{
		"jobKey":             job.GetKey(),
		"processInstanceKey": job.GetProcessInstanceKey(),
		"worker":             TaskType,
		"traceId":            traceID,
		"spanId":             span.SpanContext().SpanID().String(),
	})

	if !h.config.Enabled {
		h.logger.Info("Worker disabled by configuration", map[string]interface{}{
			"worker":  TaskType,
			"traceId": traceID,
		})
		h.completeJob(ctx, client, job, &Output{
			Success: false,
			Message: "Auth logout worker is disabled",
		})
		return
	}

	// ===== STEP 1: PARSE INPUT =====
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "auth-logout.parseInput")
	input, err := h.parseInput(job)
	spanParse.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		errorCode := extractErrorCode(err)
		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// ===== STEP 2: SANITIZE INPUT =====
	_, spanSanitize := otel.Tracer("worker-manager").Start(ctx, "auth-logout.sanitizeInput")
	input.Sanitize()
	spanSanitize.End()

	// ===== STEP 3: VALIDATE INPUT =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "auth-logout.validateInput")
	if err := ValidateInput(input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.logger.Warn("Input validation failed", map[string]interface{}{
			"jobKey":     job.GetKey(),
			"error":      err.Error(),
			"validation": "failed",
			"worker":     TaskType,
			"traceId":    traceID,
		})

		metrics.WorkerJobsFailed.WithLabelValues(TaskType, "VALIDATION_FAILED").Inc()
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	h.logger.Debug("Input validation passed", map[string]interface{}{
		"jobKey":     job.GetKey(),
		"validation": "passed",
		"worker":     TaskType,
		"traceId":    traceID,
	})

	// ===== STEP 4: EXECUTE BUSINESS LOGIC =====
	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "auth-logout.Execute")
	output, err := h.service.Execute(ctxExec, input)
	spanExec.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		errorCode := extractErrorCode(err)
		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// ===== STEP 5: COMPLETE JOB (with circuit breaker metrics) =====
	ctxComp, spanComp := otel.Tracer("worker-manager").Start(ctx, "auth-logout.completeJob")
	h.completeJob(ctxComp, client, job, output)
	spanComp.End()
	metrics.WorkerJobsCompleted.WithLabelValues(TaskType).Inc()
	metrics.WorkerJobDuration.WithLabelValues(TaskType).Observe(time.Since(startTime).Seconds())
}

func (h *Handler) parseInput(job entities.Job) (*Input, error) {
	variables, err := job.GetVariablesAsMap()
	if err != nil {
		return nil, &cerrors.StandardError{
			Code:      "INPUT_PARSING_FAILED",
			Message:   "Failed to parse job variables",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// First do schema validation
	schema := GetInputSchema()
	validationResult := validation.ValidateInput(variables, schema)
	if !validationResult.Valid {
		return nil, &cerrors.StandardError{
			Code:      "VALIDATION_FAILED",
			Message:   "Input validation failed",
			Details:   fmt.Sprintf("Validation errors: %v", validationResult.GetErrorMessages()),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	input := &Input{
		UserID: variables["userId"].(string),
	}

	if keycloakUserID, ok := variables["keycloakUserId"].(string); ok {
		input.KeycloakUserID = keycloakUserID
	}

	// IDToken is used for id_token_hint in Keycloak 17+ logout URL
	if idToken, ok := variables["idToken"].(string); ok && idToken != "" {
		input.IDToken = idToken
	}

	// RefreshToken is optional but recommended for single session logout
	if refreshToken, ok := variables["refreshToken"].(string); ok && refreshToken != "" {
		input.RefreshToken = refreshToken
	}

	// AccessToken is optional - used for adding to revocation list
	if accessToken, ok := variables["accessToken"].(string); ok && accessToken != "" {
		input.AccessToken = accessToken
	}

	if sessionID, ok := variables["sessionId"].(string); ok {
		input.SessionID = sessionID
	}

	if deviceID, ok := variables["deviceId"].(string); ok {
		input.DeviceID = deviceID
	}

	if logoutAll, ok := variables["logoutAll"].(bool); ok {
		input.LogoutAll = logoutAll
	}

	if reason, ok := variables["reason"].(string); ok {
		input.Reason = reason
	}

	if metadata, ok := variables["metadata"].(map[string]interface{}); ok {
		input.Metadata = metadata
	}

	if err := ValidateInput(input); err != nil {
		return nil, err
	}

	return input, nil
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	variables := map[string]interface{}{
		"logoutSuccess": output.Success,
		"logoutMessage": output.Message,
		"logoutAt":      output.LogoutAt.Format(time.RFC3339),
		"logoutUrl":     output.LogoutURL,
	}

	if output.SessionsInvalidated > 0 {
		variables["sessionsInvalidated"] = output.SessionsInvalidated
	}

	if output.TokenRevoked {
		variables["tokenRevoked"] = output.TokenRevoked
	}

	// Add Keycloak circuit breaker metrics if available
	if h.keycloak != nil {
		keycloakMetrics := h.keycloak.GetCircuitBreakerMetrics()
		variables["keycloakCircuitBreaker"] = map[string]interface{}{
			"state":            keycloakMetrics["state"],
			"failureCount":     keycloakMetrics["failureCount"],
			"successCount":     keycloakMetrics["successCount"],
			"concurrentCalls":  keycloakMetrics["concurrentCalls"],
			"requestLatencyMs": keycloakMetrics["requestLatencyMs"],
		}
	}

	request, err := client.NewCompleteJobCommand().JobKey(job.GetKey()).VariablesFromMap(variables)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("Failed to create complete job command", map[string]interface{}{
			"jobKey":  job.GetKey(),
			"error":   err.Error(),
			"worker":  TaskType,
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
		return
	}

	_, err = request.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("Failed to complete job", map[string]interface{}{
			"jobKey":  job.GetKey(),
			"error":   err.Error(),
			"worker":  TaskType,
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	} else {
		h.logger.Info("Successfully completed auth logout", map[string]interface{}{
			"jobKey":              job.GetKey(),
			"success":             output.Success,
			"sessionsInvalidated": output.SessionsInvalidated,
			"tokenRevoked":        output.TokenRevoked,
			"worker":              TaskType,
			"traceId":             trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	return h.service.Execute(ctx, input)
}

func (h *Handler) completeJobSimple(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		h.logger.Error("failed to complete job command", map[string]interface{}{
			"error": err,
		})
		return
	}
	_, err = cmd.Send(ctx)
	if err != nil {
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error": err,
		})
	}
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// Validate before execution
	if err := ValidateInput(input); err != nil {
		return nil, err
	}
	return h.service.Execute(ctx, input)
}

func (h *Handler) Register() error {
	if !h.config.Enabled {
		h.logger.Info("Worker is disabled, skipping registration", map[string]interface{}{
			"worker": TaskType,
		})
		return nil
	}

	zeebeClient := h.camunda.GetClient()

	jobWorker := zeebeClient.NewJobWorker().
		JobType(TaskType).
		Handler(h.Handle).
		MaxJobsActive(h.config.MaxJobsActive).
		Timeout(h.config.Timeout).
		Name(fmt.Sprintf("%s-worker", TaskType)).
		Open()

	h.jobWorker = jobWorker

	h.logger.Info("Auth logout worker registered with Camunda", map[string]interface{}{
		"taskType":      TaskType,
		"maxJobsActive": h.config.MaxJobsActive,
		"timeout":       h.config.Timeout.String(),
		"enabled":       h.config.Enabled,
	})

	return nil
}

func (h *Handler) Close() {
	if h.jobWorker != nil {
		h.logger.Info("Shutting down worker gracefully", map[string]interface{}{
			"worker": TaskType,
		})
		h.jobWorker.Close()
		h.jobWorker = nil
	}
}

func (h *Handler) HealthCheck(ctx context.Context) error {
	if err := h.camunda.HealthCheck(ctx); err != nil {
		return fmt.Errorf("camunda health check failed: %w", err)
	}

	if err := h.service.TestConnection(ctx); err != nil {
		return fmt.Errorf("auth service health check failed: %w", err)
	}

	h.logger.Info("Health check passed", map[string]interface{}{
		"worker": TaskType,
	})

	return nil
}

func (h *Handler) GetTaskType() string {
	return TaskType
}

func (h *Handler) IsEnabled() bool {
	return h.config.Enabled
}

func (h *Handler) GetConfig() *Config {
	return h.config
}

func (h *Handler) GetMetrics() map[string]interface{} {
	metrics := map[string]interface{}{
		"taskType": TaskType,
	}

	// Add Keycloak metrics if available
	if h.keycloak != nil {
		metrics["keycloak"] = h.keycloak.GetCircuitBreakerMetrics()
	}

	return metrics
}

func extractErrorCode(err error) string {
	if stdErr, ok := err.(*cerrors.StandardError); ok {
		return string(stdErr.Code)
	}
	return "UNKNOWN_ERROR"
}

func createConfigFromAppConfig(appConfig *config.Config, customConfig *Config) *Config {
	if customConfig != nil {
		return customConfig
	}

	cfg := DefaultConfig()

	if appConfig != nil {
		if workerCfg, exists := appConfig.Workers["auth-logout"]; exists {
			cfg.Enabled = workerCfg.Enabled
			if workerCfg.MaxJobsActive > 0 {
				cfg.MaxJobsActive = workerCfg.MaxJobsActive
			}
			if workerCfg.Timeout > 0 {
				cfg.Timeout = time.Duration(workerCfg.Timeout) * time.Millisecond
			}
		}

		// Load Redis configuration for session management
		if appConfig.Database.Redis.Address != "" {
			host, port := parseRedisAddress(appConfig.Database.Redis.Address)
			cfg.RedisHost = host
			cfg.RedisPort = port
			cfg.RedisPassword = appConfig.Database.Redis.Password
			cfg.RedisDB = appConfig.Database.Redis.DB
		}

		// ✅ Keycloak config — Issuer, ClientID, PostLogoutRedirectURI
		if appConfig.Auth.Keycloak.URL != "" {
			cfg.Issuer = fmt.Sprintf("%s/realms/%s",
				strings.TrimSuffix(appConfig.Auth.Keycloak.URL, "/"),
				appConfig.Auth.Keycloak.Realm,
			)
			cfg.ClientID = appConfig.Auth.Keycloak.ClientID
			cfg.PostLogoutRedirectURI = appConfig.Auth.Keycloak.PostLogoutRedirectURI
		}
	}

	return cfg
}

func parseRedisAddress(address string) (string, int) {
	host := address
	port := 6379 // default

	// Find the last colon to split host:port
	for i := len(address) - 1; i >= 0; i-- {
		if address[i] == ':' {
			host = address[:i]
			fmt.Sscanf(address[i+1:], "%d", &port)
			break
		}
	}

	return host, port
}
