package keycloaksignin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	"camunda-workers/internal/common/auth/provider/keycloak"
	"camunda-workers/internal/common/auth/resolver"
	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/database"
	cerrors "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/metrics"
	"camunda-workers/internal/common/validation"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const TaskType = "keycloak-signin"

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
		return nil, fmt.Errorf("invalid configuration for keycloak-signin: %w", err)
	}

	var loggerInstance logger.Logger
	if opts.Logger != nil {
		loggerInstance = opts.Logger
	} else {
		loggerInstance = logger.NewStructured("info", "json")
	}

	postgresClient, err := database.NewPostgres(opts.AppConfig.Database.Postgres)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize postgres: %w", err)
	}

	ctx := context.Background()

	// ✅ RETRY LOGIC: Wait for Keycloak to be ready
	var keycloakProvider *keycloak.Provider
	maxRetries := 10
	retryDelay := 3 * time.Second

	loggerInstance.Info("Initializing Keycloak provider", map[string]interface{}{
		"issuer":     workerConfig.Issuer,
		"clientId":   workerConfig.ClientID,
		"maxRetries": maxRetries,
		"retryDelay": retryDelay.String(),
	})

	for attempt := 1; attempt <= maxRetries; attempt++ {
		keycloakProvider, err = keycloak.New(
			ctx,
			workerConfig.Issuer,
			workerConfig.ClientID,
			workerConfig.RedirectURL,
			workerConfig.PublicBaseURL,
		)

		if err == nil {
			loggerInstance.Info("Keycloak provider initialized successfully", map[string]interface{}{
				"attempt": attempt,
			})
			break
		}

		if attempt < maxRetries {
			loggerInstance.Warn("Keycloak not ready, retrying...", map[string]interface{}{
				"attempt":     attempt,
				"maxRetries":  maxRetries,
				"error":       err.Error(),
				"nextRetryIn": retryDelay.String(),
			})
			time.Sleep(retryDelay)
		} else {
			return nil, fmt.Errorf("failed to initialize keycloak provider after %d attempts: %w", maxRetries, err)
		}
	}


	dbResolver := resolver.NewDBResolver(postgresClient)

	handler := &Handler{
		config:       workerConfig,
		logger:       loggerInstance.WithFields(map[string]interface{}{"taskType": TaskType}),
		camunda:      opts.Camunda,
		errorHandler: cerrors.NewErrorHandler(loggerInstance),
		validator:    validation.NewValidator(),
		sanitizer:    validation.NewSanitizer(),
	}

	handler.service = NewService(ServiceDependencies{
		KeycloakProvider: keycloakProvider,
		Resolver:         dbResolver,
		Redis:            opts.Redis,
		Logger:           loggerInstance,
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

	h.logger.Info("Processing Keycloak signin request", map[string]interface{}{
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
		h.completeJob(ctx, client, job, &Output{Success: false})
		return
	}

	// Parse input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "keycloak-signin.parseInput")
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
	_, spanSanitize := otel.Tracer("worker-manager").Start(ctx, "keycloak-signin.sanitizeInput")
	input.Sanitize()
	spanSanitize.End()

	// Validate input
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "keycloak-signin.validateInput")

	inputMap := map[string]interface{}{
		"action":   input.Action,
		"code":     input.Code,
		"state":    input.State,
		"provider": input.Provider,
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
	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "keycloak-signin.Execute")
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

	// Complete job
	ctxComp, spanComp := otel.Tracer("worker-manager").Start(ctx, "keycloak-signin.completeJob")
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

	actionVal, ok := variables["action"]
	if !ok {
		return nil, &cerrors.StandardError{
			Code:      "VALIDATION_FAILED",
			Message:   "Input validation failed",
			Details:   "missing required field: action",
			Retryable: false,
			Timestamp: time.Now(),
		}
	}
	action, ok := actionVal.(string)
	if !ok || (action != "initiate" && action != "callback") {
		return nil, &cerrors.StandardError{
			Code:      "VALIDATION_FAILED",
			Message:   "Input validation failed",
			Details:   fmt.Sprintf("action must be 'initiate' or 'callback', got: %v", actionVal),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	if action == "callback" {
		if _, hasCode := variables["code"]; !hasCode {
			return nil, &cerrors.StandardError{
				Code:      "VALIDATION_FAILED",
				Message:   "Input validation failed",
				Details:   "missing required field: code (required for callback action)",
				Retryable: false,
				Timestamp: time.Now(),
			}
		}
		if _, hasState := variables["state"]; !hasState {
			return nil, &cerrors.StandardError{
				Code:      "VALIDATION_FAILED",
				Message:   "Input validation failed",
				Details:   "missing required field: state (required for callback action)",
				Retryable: false,
				Timestamp: time.Now(),
			}
		}
	}

	input := &Input{
		Action:   action,
		Provider: "keycloak",
	}

	if code, ok := variables["code"].(string); ok {
		input.Code = code
	}
	if state, ok := variables["state"].(string); ok {
		input.State = state
	}
	if metadata, ok := variables["metadata"].(map[string]interface{}); ok {
		input.Metadata = metadata
	}

	return input, nil
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	variables := map[string]interface{}{
		"success": output.Success,
	}

	// For initiate action
	if output.AuthorizationURL != "" {
		variables["authorizationUrl"] = output.AuthorizationURL
		variables["state"] = output.State
	}

	// For callback action
	if output.UserID != "" {
		variables["userId"] = output.UserID
		variables["email"] = output.Email
		variables["emailVerified"] = output.EmailVerified
		variables["isNewUser"] = output.IsNewUser
		variables["keycloakUserId"] = output.KeycloakUserID
		variables["idToken"] = output.IDToken
		variables["authenticatedAt"] = output.AuthenticatedAt.Format(time.RFC3339)
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
		h.logger.Info("Successfully completed Keycloak signin", map[string]interface{}{
			"jobKey": job.GetKey(),
			"action": func() string {
				if output.AuthorizationURL != "" {
					return "initiate"
				}
				return "callback"
			}(),
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

	h.logger.Info("Keycloak signin worker registered with Camunda", map[string]interface{}{
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
		// Keycloak config
		if appConfig.Auth.Keycloak.URL != "" {
			// Build issuer from URL and realm
			cfg.Issuer = fmt.Sprintf("%s/realms/%s",
				strings.TrimSuffix(appConfig.Auth.Keycloak.URL, "/"),
				appConfig.Auth.Keycloak.Realm)
			cfg.ClientID = appConfig.Auth.Keycloak.ClientID
			//cfg.PublicBaseURL = appConfig.Auth.Keycloak.URL
			cfg.PublicBaseURL = appConfig.Auth.Keycloak.PublicBaseURL

			// RedirectURL from config if exists, else use default
			if appConfig.Auth.Keycloak.RedirectURL != "" {
				cfg.RedirectURL = appConfig.Auth.Keycloak.RedirectURL
			}
		}

		// Worker config
		if workerCfg, exists := appConfig.Workers["keycloak-signin"]; exists {
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
