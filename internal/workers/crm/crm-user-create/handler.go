package crmusercreate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

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

const TaskType = "crm.user.create"

type Handler struct {
	config             *Config
	logger             logger.Logger
	camunda            *camunda.Client
	service            *Service
	jobWorker          worker.JobWorker
	rateLimiter        *ratelimit.Limiter
	errorHandler       *errors.ErrorHandler
	validator          *validation.Validator
	sanitizer          *validation.Sanitizer
	cbManager          *circuitbreaker.Manager
	zohoClient         *zoho.CRMClient
	keyGenerator       *idempotency.KeyGenerator
	idempotencyChecker *idempotency.DBChecker
	db                 *sql.DB
}

type HandlerOptions struct {
	AppConfig    *config.Config
	Camunda      *camunda.Client
	CustomConfig *Config
	Logger       logger.Logger
	CBManager    *circuitbreaker.Manager
	ZohoClient   *zoho.CRMClient
	DB           *sql.DB
}

func NewHandler(opts HandlerOptions) (*Handler, error) {
	workerConfig := createConfigFromAppConfig(opts.AppConfig, opts.CustomConfig)

	if err := workerConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration for crm-user-create: %w", err)
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

	// Create Zoho client if not provided
	var zohoClient *zoho.CRMClient
	if opts.ZohoClient != nil {
		zohoClient = opts.ZohoClient
	} else if workerConfig.ZohoAPIKey != "" && workerConfig.ZohoOAuthToken != "" {
		zohoClient = zoho.NewCRMClient(workerConfig.ZohoAPIKey, workerConfig.ZohoOAuthToken, cbManager)
	}

	// Create service dependencies
	serviceDeps := ServiceDependencies{
		Logger: loggerInstance,
	}

	// Create service
	service := NewService(serviceDeps, workerConfig)

	// Set Zoho client in service if available
	if zohoClient != nil {
		service.zohoClient = zohoClient
	}

	handler := &Handler{
		config:             workerConfig,
		logger:             loggerInstance,
		camunda:            opts.Camunda,
		service:            service,
		rateLimiter:        ratelimit.New(200.0/60.0, 20),
		errorHandler:       errors.NewErrorHandler(loggerInstance),
		validator:          validation.NewValidator(),
		sanitizer:          validation.NewSanitizer(),
		cbManager:          cbManager,
		zohoClient:         zohoClient,
		keyGenerator:       idempotency.NewKeyGenerator(),
		idempotencyChecker: idempotency.NewDBChecker(opts.DB),
		db:                 opts.DB,
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

	h.logger.Info("processing job", map[string]interface{}{
		"jobKey":      job.Key,
		"workflowKey": job.ProcessInstanceKey,
		"traceId":     traceID,
		"spanId":      span.SpanContext().SpanID().String(),
	})

	// START: ORIGINAL CODE WITH METRICS AND TRACING
	startTime := time.Now()
	metrics.WorkerJobsActive.WithLabelValues(TaskType).Inc()
	defer metrics.WorkerJobsActive.WithLabelValues(TaskType).Dec()

	ctx, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()

	h.logger.Info("Processing CRM user create request", map[string]interface{}{
		"jobKey":             job.GetKey(),
		"processInstanceKey": job.GetProcessInstanceKey(),
		"worker":             TaskType,
		"traceId":            traceID,
	})

	// Check rate limit
	if !h.rateLimiter.Allow() {
		h.logger.Warn("Rate limit exceeded for CRM user create", map[string]interface{}{
			"worker":  TaskType,
			"traceId": traceID,
		})
		span.RecordError(errors.NewRateLimitExceededError("zoho_crm", 200))
		span.SetAttributes(attribute.Bool("rate_limit_exceeded", true))
		h.errorHandler.HandleJobError(ctx, client, job, errors.NewRateLimitExceededError("zoho_crm", 200))
		return
	}

	if !h.config.Enabled {
		h.logger.Info("Worker disabled by configuration", map[string]interface{}{
			"worker":  TaskType,
			"traceId": traceID,
		})
		h.completeJob(ctx, client, job, &Output{
			Success: false,
			Message: "CRM user creation disabled",
		})
		return
	}
	// END: ORIGINAL CODE

	// ===== STEP 1: PARSE INPUT (with original JSON schema validation) =====
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "crm-user-create.parseInput")
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
	_, spanSanitize := otel.Tracer("worker-manager").Start(ctx, "crm-user-create.sanitizeInput")
	input.Sanitize()
	spanSanitize.End()

	// ===== STEP 3: VALIDATE INPUT =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "crm-user-create.validateInput")
	if err := ValidateInput(input); err != nil {
		h.logger.Warn("Input validation failed", map[string]interface{}{
			"jobKey":     job.GetKey(),
			"error":      err.Error(),
			"validation": "failed",
			"worker":     TaskType,
			"traceId":    traceID,
		})

		span.RecordError(err)
		span.SetAttributes(attribute.Bool("validation_failed", true))
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
	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "crm-user-create.Execute")
	output, err := h.Execute(ctxExec, input)
	spanExec.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		errorCode := extractErrorCode(err)
		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// ===== STEP 5: COMPLETE JOB =====
	ctxComp, spanComp := otel.Tracer("worker-manager").Start(ctx, "crm-user-create.completeJob")
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
		Email:     variables["email"].(string),
		FirstName: variables["firstName"].(string),
		LastName:  variables["lastName"].(string),
	}

	if phone, ok := variables["phone"].(string); ok {
		input.Phone = phone
	}

	if company, ok := variables["company"].(string); ok {
		input.Company = company
	}

	if jobTitle, ok := variables["jobTitle"].(string); ok {
		input.JobTitle = jobTitle
	}

	if leadSource, ok := variables["leadSource"].(string); ok {
		input.LeadSource = leadSource
	}

	if tags, ok := variables["tags"].([]interface{}); ok {
		input.Tags = make([]string, len(tags))
		for i, tag := range tags {
			if tagStr, ok := tag.(string); ok {
				input.Tags[i] = tagStr
			}
		}
	}

	if customFields, ok := variables["customFields"].(map[string]interface{}); ok {
		input.CustomFields = customFields
	}

	if metadata, ok := variables["metadata"].(map[string]interface{}); ok {
		input.Metadata = metadata
	}

	return input, nil
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	variables := map[string]interface{}{
		"crmUserCreated": output.Success,
		"crmMessage":     output.Message,
	}

	if output.ContactID != "" {
		variables["crmContactId"] = output.ContactID
	}

	if output.AccountID != "" {
		variables["crmAccountId"] = output.AccountID
	}

	if output.LeadID != "" {
		variables["crmLeadId"] = output.LeadID
	}

	if output.CRMProvider != "" {
		variables["crmProvider"] = output.CRMProvider
	}

	// Add circuit breaker metrics if available
	if h.zohoClient != nil {
		metrics := h.zohoClient.GetCircuitBreakerMetrics()
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
		h.logger.Info("Successfully completed CRM user create", map[string]interface{}{
			"jobKey":    job.GetKey(),
			"success":   output.Success,
			"contactId": output.ContactID,
			"worker":    TaskType,
			"traceId":   trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	span := trace.SpanFromContext(ctx)

	if err := ValidateInput(input); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// ===== STEP 1: NORMALIZE EMAIL & GENERATE IDEMPOTENCY KEY =====
	email := strings.ToLower(strings.TrimSpace(input.Email))
	source := "Unknown"
	if input.LeadSource != "" {
		source = input.LeadSource
	}

	idempotencyKey := h.keyGenerator.GenerateCRMUserKey(email, source)

	h.logger.Info("Generated idempotency key", map[string]interface{}{
		"idempotencyKey": idempotencyKey,
		"email":          email,
		"source":         source,
		"traceId":        span.SpanContext().TraceID().String(),
	})

	// ===== STEP 2: CHECK IF CONTACT EXISTS IN ZOHO =====
	if h.zohoClient != nil {
		_, spanCheckContact := otel.Tracer("worker-manager").Start(ctx, "crm-user-create.checkExistingContact")
		existingContact, errResult := h.zohoClient.FindContactByEmail(ctx, email)
		spanCheckContact.End()

		if errResult != nil {
			// Convert errResult to string for logging
			errorStr := fmt.Sprintf("%v", errResult)

			// Convert to proper error type for span.RecordError
			var err error
			if e, ok := errResult.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", errResult)
			}

			h.logger.Error("Failed to check existing contact", map[string]interface{}{
				"error":   errorStr,
				"email":   email,
				"traceId": span.SpanContext().TraceID().String(),
			})
			span.RecordError(err)
			// Continue anyway - might be API error
		} else if existingContact != nil {
			// Type assert to map or string
			var contactID string

			// Try to extract ID from response
			if contactMap, ok := existingContact.(map[string]interface{}); ok {
				if id, exists := contactMap["id"]; exists {
					contactID = fmt.Sprintf("%v", id)
				}
			} else if idStr, ok := existingContact.(string); ok {
				contactID = idStr
			} else {
				// If it's some other type, convert to string
				contactID = fmt.Sprintf("%v", existingContact)
			}

			if contactID != "" {
				h.logger.Warn("CRM contact already exists", map[string]interface{}{
					"contactId": contactID,
					"email":     email,
					"traceId":   span.SpanContext().TraceID().String(),
				})

				span.SetAttributes(attribute.Bool("contact_already_exists", true))
				return &Output{
					Success:     true,
					ContactID:   contactID,
					CRMProvider: "Zoho",
					Message:     "Contact already exists in CRM",
				}, nil
			}
		}
	}

	// ===== STEP 3: MARK AS PROCESSING =====
	if h.idempotencyChecker != nil {
		_, spanMarkProcessing := otel.Tracer("worker-manager").Start(ctx, "crm-user-create.markProcessing")
		err := h.idempotencyChecker.MarkProcessing(ctx, idempotencyKey, "crm-user-create", 1*time.Hour)
		spanMarkProcessing.End()

		if err != nil {
			h.logger.Error("Failed to mark as processing", map[string]interface{}{
				"error":   err.Error(),
				"traceId": span.SpanContext().TraceID().String(),
			})
			span.RecordError(err)
		}
	}

	// ===== STEP 4: EXECUTE SERVICE =====
	ctxExec, spanServiceExec := otel.Tracer("worker-manager").Start(ctx, "crm-user-create.serviceExecute")
	output, err := h.service.Execute(ctxExec, input)
	spanServiceExec.End()

	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("service_error", true))

		if h.idempotencyChecker != nil {
			h.idempotencyChecker.MarkFailed(ctx, idempotencyKey)
		}
		return nil, err
	}

	// ===== STEP 5: MARK AS COMPLETED =====
	if h.idempotencyChecker != nil {
		responseData := map[string]interface{}{
			"contactId":   output.ContactID,
			"crmProvider": output.CRMProvider,
			"createdAt":   time.Now().Format(time.RFC3339),
		}

		_, spanMarkComplete := otel.Tracer("worker-manager").Start(ctx, "crm-user-create.markCompleted")
		err = h.idempotencyChecker.MarkCompleted(ctx, idempotencyKey, responseData)
		spanMarkComplete.End()

		if err != nil {
			h.logger.Error("Failed to mark as completed", map[string]interface{}{
				"error":   err.Error(),
				"traceId": span.SpanContext().TraceID().String(),
			})
			span.RecordError(err)
		}
	}

	span.SetAttributes(
		attribute.Bool("success", true),
		attribute.String("contact_id", output.ContactID),
		attribute.String("crm_provider", output.CRMProvider),
	)

	return output, nil
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

	h.logger.Info("CRM user create worker registered with Camunda", map[string]interface{}{
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

	// Test CRM connection
	if err := h.service.TestConnection(ctx); err != nil {
		return fmt.Errorf("crm health check failed: %w", err)
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
	if h.zohoClient != nil {
		return h.zohoClient.GetCircuitBreakerMetrics()
	}
	return map[string]interface{}{
		"error": "Zoho client not initialized",
	}
}

func extractErrorCode(err error) string {
	if stdErr, ok := err.(*errors.StandardError); ok {
		return string(stdErr.Code)
	}
	return "UNKNOWN_ERROR"
}

func convertToStandardError(err error) *errors.StandardError {
	if stdErr, ok := err.(*errors.StandardError); ok {
		return stdErr
	}
	return &errors.StandardError{
		Code:      "CRM_USER_CREATE_ERROR",
		Message:   "Failed to create CRM user",
		Details:   err.Error(),
		Retryable: true,
		Timestamp: time.Now(),
	}
}

func createConfigFromAppConfig(appConfig *config.Config, customConfig *Config) *Config {
	// If customConfig is provided and has credentials or timeout set, use it directly
	// This ensures validation catches all invalid values in test scenarios
	if customConfig != nil {
		// Check if this looks like a test config (has API credentials or explicit timeout/maxjobs)
		hasCredentials := customConfig.ZohoAPIKey != "" || customConfig.ZohoOAuthToken != ""
		hasTimeoutOrMaxJobs := customConfig.Timeout != 0 || customConfig.MaxJobsActive != 0

		// If it has credentials OR (timeout/maxjobs set), it's a complete config for validation
		if hasCredentials || hasTimeoutOrMaxJobs {
			return customConfig
		}

		// Otherwise, it's a partial config like {Enabled: false} - merge it
	}

	cfg := DefaultConfig()

	// Apply app config values if available
	if appConfig != nil {
		// Check if worker config exists
		if workerCfg, exists := appConfig.Workers["crm-user-create"]; exists {
			cfg.Enabled = workerCfg.Enabled
			if workerCfg.MaxJobsActive > 0 {
				cfg.MaxJobsActive = workerCfg.MaxJobsActive
			}
			if workerCfg.Timeout > 0 {
				cfg.Timeout = time.Duration(workerCfg.Timeout) * time.Millisecond
			}
		}

		// Check if Zoho config has API key
		if appConfig.Integrations.Zoho.APIKey != "" {
			cfg.ZohoAPIKey = appConfig.Integrations.Zoho.APIKey
			cfg.ZohoOAuthToken = appConfig.Integrations.Zoho.AuthToken
		}
	}

	// Override with custom config values if provided (for partial configs only)
	if customConfig != nil {
		// Always override boolean (both true/false are intentional)
		cfg.Enabled = customConfig.Enabled

		// Override CreateAccount and ApplyTags if set
		if customConfig.CreateAccount {
			cfg.CreateAccount = customConfig.CreateAccount
		}

		// Override strings if not empty
		if customConfig.ZohoAPIKey != "" {
			cfg.ZohoAPIKey = customConfig.ZohoAPIKey
		}
		if customConfig.ZohoOAuthToken != "" {
			cfg.ZohoOAuthToken = customConfig.ZohoOAuthToken
		}
	}

	return cfg
}

// package crmusercreate

// import (
// 	"context"
// 	"fmt"
// 	"time"

// 	"camunda-workers/internal/common/camunda"
// 	"camunda-workers/internal/common/config"
// 	"camunda-workers/internal/common/errors"
// 	"camunda-workers/internal/common/logger"
// 	"camunda-workers/internal/common/metrics"
// 	"camunda-workers/internal/common/ratelimit"
// 	"camunda-workers/internal/common/validation"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

// 	"go.opentelemetry.io/otel"
// )

// const TaskType = "crm.user.create"

// type Handler struct {
// 	config       *Config
// 	logger       logger.Logger
// 	camunda      *camunda.Client
// 	service      *Service
// 	jobWorker    worker.JobWorker
// 	rateLimiter  *ratelimit.Limiter
// 	errorHandler *errors.ErrorHandler
// }

// type HandlerOptions struct {
// 	AppConfig    *config.Config
// 	Camunda      *camunda.Client
// 	CustomConfig *Config
// 	Logger       logger.Logger
// }

// func NewHandler(opts HandlerOptions) (*Handler, error) {
// 	workerConfig := createConfigFromAppConfig(opts.AppConfig, opts.CustomConfig)

// 	if err := workerConfig.Validate(); err != nil {
// 		return nil, fmt.Errorf("invalid configuration for crm-user-create: %w", err)
// 	}

// 	var loggerInstance logger.Logger
// 	if opts.Logger != nil {
// 		loggerInstance = opts.Logger
// 	} else {
// 		loggerInstance = logger.NewStructured("info", "json")
// 	}

// 	handler := &Handler{
// 		config:       workerConfig,
// 		logger:       loggerInstance,
// 		camunda:      opts.Camunda,
// 		// Rate limit: 200 calls per minute (approx 3.33 calls/sec), burst 20
// 		rateLimiter:  ratelimit.New(200.0/60.0, 20),
// 		errorHandler: errors.NewErrorHandler(loggerInstance),
// 	}

// 	handler.service = NewService(ServiceDependencies{
// 		Logger: loggerInstance,
// 	}, handler.config)

// 	return handler, nil
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	startTime := time.Now()
// 	metrics.WorkerJobsActive.WithLabelValues(TaskType).Inc()
// 	defer metrics.WorkerJobsActive.WithLabelValues(TaskType).Dec()

// 	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
// 	defer cancel()

// 	h.logger.Info("Processing CRM user create request", map[string]interface{}{
// 		"jobKey":             job.GetKey(),
// 		"processInstanceKey": job.GetProcessInstanceKey(),
// 		"worker":             TaskType,
// 	})

// 	// Check rate limit
// 	if !h.rateLimiter.Allow() {
// 		h.logger.Warn("Rate limit exceeded for CRM user create", map[string]interface{}{
// 			"worker": TaskType,
// 		})
// 		h.errorHandler.HandleJobError(ctx, client, job, errors.NewRateLimitExceededError("zoho_crm", 200))
// 		return
// 	}

// 	if !h.config.Enabled {
// 		h.logger.Info("Worker disabled by configuration", map[string]interface{}{
// 			"worker": TaskType,
// 		})
// 		h.completeJob(ctx, client, job, &Output{
// 			Success: false,
// 			Message: "CRM user creation disabled",
// 		})
// 		return
// 	}

// 	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "crm-user-create.parseInput")
// 	input, err := h.parseInput(job)
// 	spanParse.End()
// 	if err != nil {
// 		errorCode := extractErrorCode(err)
// 		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
// 		h.errorHandler.HandleJobError(ctx, client, job, err)
// 		return
// 	}

// 	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "crm-user-create.Execute")
// 	output, err := h.Execute(ctxExec, input)
// 	spanExec.End()
// 	if err != nil {
// 		errorCode := extractErrorCode(err)
// 		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
// 		h.errorHandler.HandleJobError(ctx, client, job, err)
// 		return
// 	}

// 	ctxComp, spanComp := otel.Tracer("worker-manager").Start(ctx, "crm-user-create.completeJob")
// 	h.completeJob(ctxComp, client, job, output)
// 	spanComp.End()
// 	metrics.WorkerJobsCompleted.WithLabelValues(TaskType).Inc()
// 	metrics.WorkerJobDuration.WithLabelValues(TaskType).Observe(time.Since(startTime).Seconds())
// }

// func (h *Handler) parseInput(job entities.Job) (*Input, error) {
// 	variables, err := job.GetVariablesAsMap()
// 	if err != nil {
// 		return nil, &errors.StandardError{
// 			Code:      "INPUT_PARSING_FAILED",
// 			Message:   "Failed to parse job variables",
// 			Details:   err.Error(),
// 			Retryable: false,
// 			Timestamp: time.Now(),
// 		}
// 	}

// 	schema := GetInputSchema()
// 	validationResult := validation.ValidateInput(variables, schema)
// 	if !validationResult.Valid {
// 		return nil, &errors.StandardError{
// 			Code:      "VALIDATION_FAILED",
// 			Message:   "Input validation failed",
// 			Details:   fmt.Sprintf("Validation errors: %v", validationResult.GetErrorMessages()),
// 			Retryable: false,
// 			Timestamp: time.Now(),
// 		}
// 	}

// 	input := &Input{
// 		Email:     variables["email"].(string),
// 		FirstName: variables["firstName"].(string),
// 		LastName:  variables["lastName"].(string),
// 	}

// 	if phone, ok := variables["phone"].(string); ok {
// 		input.Phone = phone
// 	}

// 	if company, ok := variables["company"].(string); ok {
// 		input.Company = company
// 	}

// 	if jobTitle, ok := variables["jobTitle"].(string); ok {
// 		input.JobTitle = jobTitle
// 	}

// 	if leadSource, ok := variables["leadSource"].(string); ok {
// 		input.LeadSource = leadSource
// 	}

// 	if tags, ok := variables["tags"].([]interface{}); ok {
// 		input.Tags = make([]string, len(tags))
// 		for i, tag := range tags {
// 			if tagStr, ok := tag.(string); ok {
// 				input.Tags[i] = tagStr
// 			}
// 		}
// 	}

// 	if customFields, ok := variables["customFields"].(map[string]interface{}); ok {
// 		input.CustomFields = customFields
// 	}

// 	if metadata, ok := variables["metadata"].(map[string]interface{}); ok {
// 		input.Metadata = metadata
// 	}

// 	return input, nil
// }

// func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
// 	variables := map[string]interface{}{
// 		"crmUserCreated": output.Success,
// 		"crmMessage":     output.Message,
// 	}

// 	if output.ContactID != "" {
// 		variables["crmContactId"] = output.ContactID
// 	}

// 	if output.AccountID != "" {
// 		variables["crmAccountId"] = output.AccountID
// 	}

// 	if output.LeadID != "" {
// 		variables["crmLeadId"] = output.LeadID
// 	}

// 	if output.CRMProvider != "" {
// 		variables["crmProvider"] = output.CRMProvider
// 	}

// 	request, err := client.NewCompleteJobCommand().JobKey(job.GetKey()).VariablesFromMap(variables)
// 	if err != nil {
// 		h.logger.Error("Failed to create complete job command", map[string]interface{}{
// 			"jobKey": job.GetKey(),
// 			"error":  err.Error(),
// 			"worker": TaskType,
// 		})
// 		return
// 	}

// 	_, err = request.Send(ctx)
// 	if err != nil {
// 		h.logger.Error("Failed to complete job", map[string]interface{}{
// 			"jobKey": job.GetKey(),
// 			"error":  err.Error(),
// 			"worker": TaskType,
// 		})
// 	} else {
// 		h.logger.Info("Successfully completed CRM user create", map[string]interface{}{
// 			"jobKey":    job.GetKey(),
// 			"success":   output.Success,
// 			"contactId": output.ContactID,
// 			"worker":    TaskType,
// 		})
// 	}
// }

// func (h *Handler) failJob(ctx context.Context, client worker.JobClient, job entities.Job, err error) {
// 	// Delegate to centralized handler
// 	h.errorHandler.HandleJobError(ctx, client, job, err)
// }

// func (h *Handler) Register() error {
// 	if !h.config.Enabled {
// 		h.logger.Info("Worker is disabled, skipping registration", map[string]interface{}{
// 			"worker": TaskType,
// 		})
// 		return nil
// 	}

// 	zeebeClient := h.camunda.GetClient()

// 	jobWorker := zeebeClient.NewJobWorker().
// 		JobType(TaskType).
// 		Handler(h.Handle).
// 		MaxJobsActive(h.config.MaxJobsActive).
// 		Timeout(h.config.Timeout).
// 		Name(fmt.Sprintf("%s-worker", TaskType)).
// 		Open()

// 	h.jobWorker = jobWorker

// 	h.logger.Info("CRM user create worker registered with Camunda", map[string]interface{}{
// 		"taskType":      TaskType,
// 		"maxJobsActive": h.config.MaxJobsActive,
// 		"timeout":       h.config.Timeout.String(),
// 		"enabled":       h.config.Enabled,
// 	})

// 	return nil
// }

// func (h *Handler) Close() {
// 	if h.jobWorker != nil {
// 		h.logger.Info("Shutting down worker gracefully", map[string]interface{}{
// 			"worker": TaskType,
// 		})
// 		h.jobWorker.Close()
// 		h.jobWorker = nil
// 	}
// }

// func (h *Handler) HealthCheck(ctx context.Context) error {
// 	if err := h.camunda.HealthCheck(ctx); err != nil {
// 		return fmt.Errorf("camunda health check failed: %w", err)
// 	}

// 	// Test CRM connection
// 	if err := h.service.TestConnection(ctx); err != nil {
// 		return fmt.Errorf("crm health check failed: %w", err)
// 	}

// 	h.logger.Info("Health check passed", map[string]interface{}{
// 		"worker": TaskType,
// 	})

// 	return nil
// }

// func (h *Handler) GetTaskType() string {
// 	return TaskType
// }

// func (h *Handler) IsEnabled() bool {
// 	return h.config.Enabled
// }

// func (h *Handler) GetConfig() *Config {
// 	return h.config
// }

// func extractErrorCode(err error) string {
// 	if stdErr, ok := err.(*errors.StandardError); ok {
// 		return string(stdErr.Code)
// 	}
// 	return "UNKNOWN_ERROR"
// }

// func convertToStandardError(err error) *errors.StandardError {
// 	if stdErr, ok := err.(*errors.StandardError); ok {
// 		return stdErr
// 	}
// 	return &errors.StandardError{
// 		Code:      "CRM_USER_CREATE_ERROR",
// 		Message:   "Failed to create CRM user",
// 		Details:   err.Error(),
// 		Retryable: true,
// 		Timestamp: time.Now(),
// 	}
// }

// func createConfigFromAppConfig(appConfig *config.Config, customConfig *Config) *Config {
// 	// If customConfig is provided and has credentials or timeout set, use it directly
// 	// This ensures validation catches all invalid values in test scenarios
// 	if customConfig != nil {
// 		// Check if this looks like a test config (has API credentials or explicit timeout/maxjobs)
// 		hasCredentials := customConfig.ZohoAPIKey != "" || customConfig.ZohoOAuthToken != ""
// 		hasTimeoutOrMaxJobs := customConfig.Timeout != 0 || customConfig.MaxJobsActive != 0

// 		// If it has credentials OR (timeout/maxjobs set), it's a complete config for validation
// 		if hasCredentials || hasTimeoutOrMaxJobs {
// 			return customConfig
// 		}

// 		// Otherwise, it's a partial config like {Enabled: false} - merge it
// 	}

// 	cfg := DefaultConfig()

// 	// Apply app config values if available
// 	if appConfig != nil {
// 		// Check if worker config exists
// 		if workerCfg, exists := appConfig.Workers["crm-user-create"]; exists {
// 			cfg.Enabled = workerCfg.Enabled
// 			if workerCfg.MaxJobsActive > 0 {
// 				cfg.MaxJobsActive = workerCfg.MaxJobsActive
// 			}
// 			if workerCfg.Timeout > 0 {
// 				cfg.Timeout = time.Duration(workerCfg.Timeout) * time.Millisecond
// 			}
// 		}

// 		// Check if Zoho config has API key
// 		if appConfig.Integrations.Zoho.APIKey != "" {
// 			cfg.ZohoAPIKey = appConfig.Integrations.Zoho.APIKey
// 			cfg.ZohoOAuthToken = appConfig.Integrations.Zoho.AuthToken
// 		}
// 	}

// 	// Override with custom config values if provided (for partial configs only)
// 	if customConfig != nil {
// 		// Always override boolean (both true/false are intentional)
// 		cfg.Enabled = customConfig.Enabled

// 		// Override CreateAccount and ApplyTags if set
// 		if customConfig.CreateAccount {
// 			cfg.CreateAccount = customConfig.CreateAccount
// 		}

// 		// Override strings if not empty
// 		if customConfig.ZohoAPIKey != "" {
// 			cfg.ZohoAPIKey = customConfig.ZohoAPIKey
// 		}
// 		if customConfig.ZohoOAuthToken != "" {
// 			cfg.ZohoOAuthToken = customConfig.ZohoOAuthToken
// 		}
// 	}

// 	return cfg
// }

// // Execute implements the standard worker interface for direct execution
// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	// Delegate to the service layer for business logic
// 	return h.service.Execute(ctx, input)
// }
