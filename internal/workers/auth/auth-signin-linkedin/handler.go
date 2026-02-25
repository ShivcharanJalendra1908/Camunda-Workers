package authsigninlinkedin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/circuitbreaker"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/idempotency"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/metrics"
	"camunda-workers/internal/common/ratelimit"
	"camunda-workers/internal/common/validation"
	"camunda-workers/internal/common/zoho"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const TaskType = "auth.signin.linkedin"

type Handler struct {
	config             *Config
	logger             logger.Logger
	camunda            *camunda.Client
	keycloak           *auth.KeycloakClient
	zohoCRM            *zoho.CRMClient
	service            *Service
	jobWorker          worker.JobWorker
	rateLimiter        *ratelimit.Limiter
	errorHandler       *errors.ErrorHandler
	validator          *validation.Validator
	sanitizer          *validation.Sanitizer
	cbManager          *circuitbreaker.Manager
	idempotencyChecker idempotency.Checker // ✅ Interface
	keyGenerator       *idempotency.KeyGenerator
}

type HandlerOptions struct {
	AppConfig          *config.Config
	Camunda            *camunda.Client
	Keycloak           *auth.KeycloakClient
	ZohoCRM            *zoho.CRMClient
	CustomConfig       *Config
	Logger             logger.Logger
	CBManager          *circuitbreaker.Manager
	IdempotencyChecker idempotency.Checker // ✅ NEW
}

func NewHandler(opts HandlerOptions) (*Handler, error) {
	workerConfig := createConfigFromAppConfig(opts.AppConfig, opts.CustomConfig)

	if err := workerConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration for auth-signin-linkedin: %w", err)
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

	// Create Zoho client if not provided but config has credentials
	var zohoClient *zoho.CRMClient
	if opts.ZohoCRM != nil {
		zohoClient = opts.ZohoCRM
	} else if workerConfig.IsCRMEnabled() {
		zohoClient = zoho.NewCRMClient(
			workerConfig.ZohoAPIKey,
			workerConfig.ZohoOAuthToken,
			cbManager,
		)
	}
	// Create service dependencies
	serviceDeps := ServiceDependencies{
		Keycloak: opts.Keycloak,
		ZohoCRM:  zohoClient,
		Logger:   loggerInstance,
	}

	// Create service
	service := NewService(serviceDeps, workerConfig)

	handler := &Handler{
		config:             workerConfig,
		logger:             loggerInstance,
		camunda:            opts.Camunda,
		keycloak:           opts.Keycloak,
		zohoCRM:            zohoClient,
		service:            service,
		errorHandler:       errors.NewErrorHandler(loggerInstance),
		rateLimiter:        ratelimit.New(100.0/60.0, 10),
		validator:          validation.NewValidator(),
		sanitizer:          validation.NewSanitizer(),
		cbManager:          cbManager,
		idempotencyChecker: opts.IdempotencyChecker, // ✅ NEW
		keyGenerator:       idempotency.NewKeyGenerator(),
	}

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

	// START: ORIGINAL CODE WITH METRICS AND TRACING
	startTime := time.Now()
	metrics.WorkerJobsActive.WithLabelValues(TaskType).Inc()
	defer metrics.WorkerJobsActive.WithLabelValues(TaskType).Dec()

	ctx, cancel := context.WithTimeout(ctx, h.config.Timeout) // ✅ Use traced context
	defer cancel()

	h.logger.Info("Processing LinkedIn signin request", map[string]interface{}{
		"jobKey":             job.GetKey(),
		"processInstanceKey": job.GetProcessInstanceKey(),
		"worker":             TaskType,
		"traceId":            traceID,
		"spanId":             span.SpanContext().SpanID().String(),
	})

	// Check rate limit
	if !h.rateLimiter.Allow() {
		span.RecordError(errors.NewRateLimitExceededError("LinkedIn API", 100))
		span.SetAttributes(attribute.Bool("rate_limit_exceeded", true))
		h.logger.Warn("Rate limit exceeded for LinkedIn worker", map[string]interface{}{
			"worker":  TaskType,
			"traceId": traceID,
		})
		h.errorHandler.HandleJobError(ctx, client, job, errors.NewRateLimitExceededError("LinkedIn API", 100))
		return
	}

	if !h.config.Enabled {
		span.SetAttributes(attribute.Bool("worker.disabled", true))
		h.logger.Info("Worker disabled by configuration", map[string]interface{}{
			"worker":  TaskType,
			"traceId": traceID,
		})
		h.completeJob(ctx, client, job, &Output{Success: false})
		return
	}
	// END: ORIGINAL CODE

	// ===== STEP 1: PARSE INPUT (with original JSON schema validation) =====
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "auth-signin-linkedin.parseInput")
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
	_, spanSanitize := otel.Tracer("worker-manager").Start(ctx, "auth-signin-linkedin.sanitizeInput")
	input.Sanitize()
	spanSanitize.End()

	// ===== STEP 3: VALIDATE INPUT =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "auth-signin-linkedin.validateInput")
	if err := ValidateInput(input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("validation_error", true))
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

	// ===== ✅ STEP 4: IDEMPOTENCY CHECK =====
	if h.idempotencyChecker != nil {
		// For signin, we can use auth code as idempotency key (one-time use)
		idempotencyKey := h.keyGenerator.GenerateSigninKey(input.AuthCode, "linkedin")

		h.logger.Debug("Checking signin idempotency", map[string]interface{}{
			"idempotencyKey": idempotencyKey,
			"traceId":        traceID,
		})

		result, err := h.idempotencyChecker.Check(ctx, idempotencyKey)

		if err == idempotency.ErrDuplicateRequest {
			span.SetAttributes(attribute.Bool("idempotent_duplicate", true))
			h.logger.Info("Duplicate signin detected, returning cached result", map[string]interface{}{
				"idempotencyKey": idempotencyKey,
				"traceId":        traceID,
			})

			if result.Response != nil {
				output := &Output{
					Success:      true,
					UserID:       result.Response["userId"].(string),
					Email:        result.Response["email"].(string),
					AccessToken:  result.Response["accessToken"].(string),
					RefreshToken: result.Response["refreshToken"].(string),
				}

				h.completeJob(ctx, client, job, output)
				metrics.WorkerJobsCompleted.WithLabelValues(TaskType).Inc()
				return
			}
		}

		if err == idempotency.ErrProcessing {
			span.SetAttributes(attribute.Bool("idempotent_processing", true))
			h.logger.Warn("Signin already being processed", map[string]interface{}{
				"idempotencyKey": idempotencyKey,
				"traceId":        traceID,
			})
			return
		}

		// Mark as processing
		if err := h.idempotencyChecker.MarkProcessing(ctx, idempotencyKey, TaskType, 1*time.Hour); err != nil {
			h.logger.Warn("Failed to mark processing", map[string]interface{}{
				"error":   err.Error(),
				"traceId": traceID,
			})
		}
	}

	// ===== STEP 5: EXECUTE BUSINESS LOGIC =====
	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "auth-signin-linkedin.Execute")
	output, err := h.service.Execute(ctxExec, input)
	spanExec.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("execution_error", true))
		errorCode := extractErrorCode(err)
		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// ===== ✅ STEP 6: STORE SUCCESS RESULT =====
	if h.idempotencyChecker != nil && output.Success {
		idempotencyKey := h.keyGenerator.GenerateSigninKey(input.AuthCode, "linkedin")

		response := map[string]interface{}{
			"userId":       output.UserID,
			"email":        output.Email,
			"accessToken":  output.AccessToken,
			"refreshToken": output.RefreshToken,
			"success":      true,
		}

		if err := h.idempotencyChecker.MarkCompleted(ctx, idempotencyKey, response); err != nil {
			h.logger.Warn("Failed to mark completed", map[string]interface{}{
				"error":   err.Error(),
				"traceId": traceID,
			})
		}
	}

	// ===== STEP 7: COMPLETE JOB (with circuit breaker metrics) =====
	ctxComp, spanComp := otel.Tracer("worker-manager").Start(ctx, "auth-signin-linkedin.completeJob")
	h.completeJob(ctxComp, client, job, output)
	spanComp.End()
	metrics.WorkerJobsCompleted.WithLabelValues(TaskType).Inc()
	metrics.WorkerJobDuration.WithLabelValues(TaskType).Observe(time.Since(startTime).Seconds())
}

func (h *Handler) parseInput(job entities.Job) (*Input, error) {
	variables, err := job.GetVariablesAsMap()
	if err != nil {
		return nil, &errors.StandardError{
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
		return nil, &errors.StandardError{
			Code:      "VALIDATION_FAILED",
			Message:   "Input validation failed",
			Details:   fmt.Sprintf("Validation errors: %v", validationResult.GetErrorMessages()),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	input := &Input{
		AuthCode: variables["authCode"].(string),
	}

	if redirectURI, ok := variables["redirectUri"].(string); ok && redirectURI != "" {
		input.RedirectURI = redirectURI
	} else {
		input.RedirectURI = h.config.RedirectURL
	}

	if state, ok := variables["state"].(string); ok {
		input.State = state
	}

	if metadata, ok := variables["metadata"].(map[string]interface{}); ok {
		input.Metadata = metadata
	}

	return input, nil
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	return h.service.Execute(ctx, input)
}

func (h *Handler) completeJobSimple(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to complete job command", map[string]interface{}{
			"error":   err,
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
		return
	}
	_, err = cmd.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error":   err,
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
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

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	variables := map[string]interface{}{
		"success":       output.Success,
		"userId":        output.UserID,
		"email":         output.Email,
		"firstName":     output.FirstName,
		"lastName":      output.LastName,
		"accessToken":   output.AccessToken,
		"refreshToken":  output.RefreshToken,
		"expiresIn":     output.ExpiresIn,
		"tokenType":     output.TokenType,
		"isNewUser":     output.IsNewUser,
		"emailVerified": output.EmailVerified,
	}

	if output.CRMContactID != "" {
		variables["crmContactId"] = output.CRMContactID
	}

	// Also include the single token field for backward compatibility
	variables["token"] = output.Token

	// Add circuit breaker metrics if available
	if h.zohoCRM != nil {
		metrics := h.zohoCRM.GetCircuitBreakerMetrics()
		variables["crmCircuitBreaker"] = map[string]interface{}{
			"state":            metrics["state"],
			"failureCount":     metrics["failureCount"],
			"successCount":     metrics["successCount"],
			"pendingContacts":  metrics["pendingContacts"],
			"pendingAccounts":  metrics["pendingAccounts"],
			"concurrentCalls":  metrics["concurrentCalls"],
			"requestLatencyMs": metrics["requestLatencyMs"],
		}
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
		h.logger.Info("Successfully completed LinkedIn signin", map[string]interface{}{
			"jobKey":        job.GetKey(),
			"userId":        output.UserID,
			"isNewUser":     output.IsNewUser,
			"crmContactId":  output.CRMContactID,
			"emailVerified": output.EmailVerified,
			"worker":        TaskType,
			"traceId":       trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
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

	h.logger.Info("LinkedIn signin worker registered with Camunda", map[string]interface{}{
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
	if h.camunda == nil {
		return fmt.Errorf("camunda client is nil")
	}

	if err := h.camunda.HealthCheck(ctx); err != nil {
		return fmt.Errorf("camunda health check failed: %w", err)
	}

	// CRITICAL FIX
	if h.keycloak == nil {
		h.logger.Warn("Keycloak not configured, skipping keycloak health check", map[string]interface{}{
			"worker": TaskType,
		})
		return nil
	}

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := h.keycloak.GetUserByEmail(testCtx, "healthcheck@test.com")
	if err != nil {
		if stdErr, ok := err.(*errors.StandardError); ok {
			if stdErr.Code != "USER_NOT_FOUND" {
				return fmt.Errorf("keycloak health check failed: %w", err)
			}
		}
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

	// Add Zoho CRM metrics if available
	if h.zohoCRM != nil {
		metrics["zohoCRM"] = h.zohoCRM.GetCircuitBreakerMetrics()
	}

	// Add Keycloak metrics if available
	if h.keycloak != nil {
		metrics["keycloak"] = h.keycloak.GetCircuitBreakerMetrics()
	}

	return metrics
}

func extractErrorCode(err error) string {
	if stdErr, ok := err.(*errors.StandardError); ok {
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
		linkedInConfig := appConfig.Auth.OAuthProviders.LinkedIn
		if linkedInConfig.ClientID != "" {
			cfg.ClientID = linkedInConfig.ClientID
			cfg.ClientSecret = linkedInConfig.ClientSecret
			cfg.RedirectURL = linkedInConfig.RedirectURL
		}

		if workerCfg, exists := appConfig.Workers["auth-signin-linkedin"]; exists {
			cfg.Enabled = workerCfg.Enabled
			if workerCfg.MaxJobsActive > 0 {
				cfg.MaxJobsActive = workerCfg.MaxJobsActive
			}
			if workerCfg.Timeout > 0 {
				cfg.Timeout = time.Duration(workerCfg.Timeout) * time.Millisecond
			}
		}

		if appConfig.Integrations.Zoho.APIKey != "" {
			cfg.CreateCRMContact = true
			cfg.ZohoAPIKey = appConfig.Integrations.Zoho.APIKey
			cfg.ZohoOAuthToken = appConfig.Integrations.Zoho.AuthToken
		}
	}

	return cfg
}

