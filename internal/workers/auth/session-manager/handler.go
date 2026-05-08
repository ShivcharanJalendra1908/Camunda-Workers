package sessionmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/config"
	cerrors "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/metrics"
	"camunda-workers/internal/common/validation"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const TaskType = "session-manager"

type Handler struct {
	config       *Config
	logger       logger.Logger
	camunda      *camunda.Client
	service      *Service
	jobWorker    worker.JobWorker
	errorHandler *cerrors.ErrorHandler
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
}

type HandlerOptions struct {
	AppConfig    *config.Config
	Camunda      *camunda.Client
	Redis        *redis.Client
	CustomConfig *Config
	Logger       logger.Logger
}

func NewHandler(opts HandlerOptions) (*Handler, error) {
	workerConfig := createConfigFromAppConfig(opts.AppConfig, opts.CustomConfig)

	if err := workerConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration for session-manager: %w", err)
	}

	var loggerInstance logger.Logger
	if opts.Logger != nil {
		loggerInstance = opts.Logger
	} else {
		loggerInstance = logger.NewStructured("info", "json")
	}

	handler := &Handler{
		config:       workerConfig,
		logger:       loggerInstance.WithFields(map[string]interface{}{"taskType": TaskType}),
		camunda:      opts.Camunda,
		errorHandler: cerrors.NewErrorHandler(loggerInstance),
		validator:    validation.NewValidator(),
		sanitizer:    validation.NewSanitizer(),
	}

	handler.service = NewService(ServiceDependencies{
		Redis:  opts.Redis,
		Logger: loggerInstance,
	}, workerConfig)

	return handler, nil
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
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

	h.logger.Info("Processing session manager request", map[string]interface{}{
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
			Message: "Session manager disabled",
		})
		return
	}

	// Parse input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "session-manager.parseInput")
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

	// Sanitize input
	_, spanSanitize := otel.Tracer("worker-manager").Start(ctx, "session-manager.sanitizeInput")
	input.Sanitize()
	spanSanitize.End()

	// Validate input
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "session-manager.validateInput")

	inputMap := map[string]interface{}{
		"action":    input.Action,
		"sessionId": input.SessionID,
		"userId":    input.UserID,
		"email":     input.Email,
		"expiresIn": input.ExpiresIn,
	}

	if err := ValidateInput(inputMap); err != nil {
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

	// Execute business logic
	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "session-manager.Execute")
	output, err := h.service.Execute(ctxExec, input)
	spanExec.End()
	if err != nil {
		// ✅ SESSION_NOT_FOUND — error nahi, gracefully complete karo
		if stdErr, ok := err.(*cerrors.StandardError); ok && stdErr.Code == "SESSION_NOT_FOUND" {
			h.completeJob(ctx, client, job, &Output{
				Success: false,
				Message: "Session not found or expired",
			})
			return
		}
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		errorCode := extractErrorCode(err)
		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// Complete job
	ctxComp, spanComp := otel.Tracer("worker-manager").Start(ctx, "session-manager.completeJob")
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
		Action: variables["action"].(string),
	}

	if sessionID, ok := variables["sessionId"].(string); ok {
		input.SessionID = sessionID
	}

	if userID, ok := variables["userId"].(string); ok {
		input.UserID = userID
	}

	if keycloakUserID, ok := variables["keycloakUserId"].(string); ok {
		input.KeycloakUserID = keycloakUserID
	}

	if idToken, ok := variables["idToken"].(string); ok {
		input.IDToken = idToken
	}

	if refreshToken, ok := variables["refreshToken"].(string); ok {
		input.RefreshToken = refreshToken
	}

	if email, ok := variables["email"].(string); ok {
		input.Email = email
	}

	if expiresIn, ok := variables["expiresIn"].(float64); ok {
		input.ExpiresIn = int(expiresIn)
	} else {
		input.ExpiresIn = 86400 // Default 24 hours
	}

	if metadata, ok := variables["metadata"].(map[string]interface{}); ok {
		input.Metadata = metadata
	}

	return input, nil
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	variables := map[string]interface{}{
		"sessionSuccess": output.Success,
		"sessionMessage": output.Message,
	}

	if output.SessionID != "" {
		variables["sessionId"] = output.SessionID
	}

	if output.UserID != "" {
		variables["userId"] = output.UserID
	}

	if output.KeycloakUserID != "" {
		variables["keycloakUserId"] = output.KeycloakUserID
	}

	if output.Email != "" {
		variables["email"] = output.Email
	}

	if output.IDToken != "" {
		variables["idToken"] = output.IDToken
	}

	if output.RefreshToken != "" {
		variables["refreshToken"] = output.RefreshToken
	}

	if !output.ExpiresAt.IsZero() {
		variables["expiresAt"] = output.ExpiresAt.Format(time.RFC3339)
	}

	if output.CookieHeader != "" {
		variables["cookieHeader"] = output.CookieHeader
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
		h.logger.Info("Successfully completed session manager operation", map[string]interface{}{
			"jobKey":  job.GetKey(),
			"action":  variables["action"],
			"success": output.Success,
			"worker":  TaskType,
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}
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

	h.logger.Info("Session manager worker registered with Camunda", map[string]interface{}{
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
		return fmt.Errorf("redis health check failed: %w", err)
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
		// Worker config
		if workerCfg, exists := appConfig.Workers["session-manager"]; exists {
			cfg.Enabled = workerCfg.Enabled
			if workerCfg.MaxJobsActive > 0 {
				cfg.MaxJobsActive = workerCfg.MaxJobsActive
			}
			if workerCfg.Timeout > 0 {
				cfg.Timeout = time.Duration(workerCfg.Timeout) * time.Millisecond
			}
		}

		// Redis config
		if appConfig.Database.Redis.Address != "" {
			host, port := parseRedisAddress(appConfig.Database.Redis.Address)
			cfg.RedisHost = host
			cfg.RedisPort = port
			cfg.RedisPassword = appConfig.Database.Redis.Password
			cfg.RedisDB = appConfig.Database.Redis.DB
		}
	}

	return cfg
}

func parseRedisAddress(address string) (string, int) {
	host := address
	port := 6379

	for i := len(address) - 1; i >= 0; i-- {
		if address[i] == ':' {
			host = address[:i]
			fmt.Sscanf(address[i+1:], "%d", &port)
			break
		}
	}

	return host, port
}
