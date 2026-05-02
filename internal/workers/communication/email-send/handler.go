// internal/workers/communication/email-send/handler.go
package emailsend

import (
	"context"
	"encoding/json"
	"fmt"
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
	"camunda-workers/internal/common/validation"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const TaskType = "email-send"

type ServiceInterface interface {
	Execute(ctx context.Context, input *Input) (*Output, error)
	TestConnection(ctx context.Context) error
	GetCircuitBreakerMetrics() map[string]interface{}
}

type Handler struct {
	config             *Config
	logger             logger.Logger
	camunda            *camunda.Client
	service            ServiceInterface // *Service
	jobWorker          worker.JobWorker
	errorHandler       *errors.ErrorHandler
	validator          *validation.Validator
	sanitizer          *validation.Sanitizer
	cbManager          *circuitbreaker.Manager
	idempotencyChecker idempotency.Checker // ✅ Interface, not pointer
	keyGenerator       *idempotency.KeyGenerator
}

type HandlerOptions struct {
	AppConfig          *config.Config
	Camunda            *camunda.Client
	CustomConfig       *Config
	Logger             logger.Logger
	CBManager          *circuitbreaker.Manager
	IdempotencyChecker idempotency.Checker // ✅ NEW
}

func NewHandler(opts HandlerOptions) (*Handler, error) {
	workerConfig := createConfigFromAppConfig(opts.AppConfig, opts.CustomConfig)

	if err := workerConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration for email-send: %w", err)
	}

	var loggerInstance logger.Logger
	if opts.Logger != nil {
		loggerInstance = opts.Logger
	} else {
		loggerInstance = logger.NewStructured("info", "json")
	}

	var cbManager *circuitbreaker.Manager
	if opts.CBManager != nil {
		cbManager = opts.CBManager
	} else {
		cbManager = circuitbreaker.NewManager()
	}

	handler := &Handler{
		config:             workerConfig,
		logger:             loggerInstance,
		camunda:            opts.Camunda,
		errorHandler:       errors.NewErrorHandler(loggerInstance),
		validator:          validation.NewValidator(),
		sanitizer:          validation.NewSanitizer(),
		cbManager:          cbManager,
		idempotencyChecker: opts.IdempotencyChecker, // ✅ NEW
		keyGenerator:       idempotency.NewKeyGenerator(),
	}

	serviceDeps := ServiceDependencies{
		Logger:    loggerInstance,
		CBManager: cbManager,
	}

	handler.service = NewService(serviceDeps, handler.config)

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

	ctx, cancel := context.WithTimeout(ctx, h.config.Timeout) // ✅ Use traced context
	defer cancel()

	h.logger.Info("Processing email send request", map[string]interface{}{
		"jobKey":             job.GetKey(),
		"processInstanceKey": job.GetProcessInstanceKey(),
		"worker":             TaskType,
		"traceId":            traceID,
		"spanId":             span.SpanContext().SpanID().String(),
	})

	if !h.config.Enabled {
		span.SetAttributes(attribute.Bool("worker.disabled", true))
		h.logger.Info("Worker disabled by configuration", map[string]interface{}{
			"worker":  TaskType,
			"traceId": traceID,
		})
		h.completeJob(ctx, client, job, &Output{
			Success: false,
			Message: "Email sending disabled",
		})
		return
	}

	// ===== STEP 1: PARSE INPUT =====
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "email-send.parseInput")
	input, err := h.parseInput(job)
	spanParse.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("parse_error", true))
		errorCode := extractErrorCode(err)
		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
		h.failJob(ctx, client, job, err)
		return
	}

	// ===== STEP 2: SANITIZE INPUT =====
	_, spanSanitize := otel.Tracer("worker-manager").Start(ctx, "email-send.sanitizeInput")
	input.Sanitize()
	spanSanitize.End()

	// ===== STEP 3: VALIDATE INPUT =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "email-send.validateInput")
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
		h.failJob(ctx, client, job, err)
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
		// Generate idempotency key: notification_type + recipient + date
		_ = time.Now().Format("2006-01-02")
		idempotencyKey := h.keyGenerator.GenerateNotificationKeySimple("email", input.To)
		if rid, ok := jobVars["requestId"].(string); ok && rid != "" {
			idempotencyKey = fmt.Sprintf("%s_%s", idempotencyKey, rid)
		}

		h.logger.Debug("Checking email idempotency", map[string]interface{}{
			"idempotencyKey": idempotencyKey,
			"to":             input.To,
			"requestId":      jobVars["requestId"],
			"traceId":        traceID,
		})

		result, err := h.idempotencyChecker.Check(ctx, idempotencyKey)

		if err == idempotency.ErrDuplicateRequest {
			span.SetAttributes(attribute.Bool("idempotent_duplicate", true))
			h.logger.Info("Duplicate email detected, returning cached result", map[string]interface{}{
				"idempotencyKey": idempotencyKey,
				"to":             input.To,
				"traceId":        traceID,
			})

			output := &Output{
				Success:   true,
				Message:   "Email already sent (cached)",
				MessageID: fmt.Sprintf("cached_%s", idempotencyKey),
				Provider:  "cached",
				SentAt:    result.CreatedAt,
			}

			h.completeJob(ctx, client, job, output)
			metrics.WorkerJobsCompleted.WithLabelValues(TaskType).Inc()
			return
		}

		if err == idempotency.ErrProcessing {
			span.SetAttributes(attribute.Bool("idempotent_processing", true))
			h.logger.Warn("Email already being processed", map[string]interface{}{
				"idempotencyKey": idempotencyKey,
				"traceId":        traceID,
			})
			// Let Camunda retry later
			return
		}

		// Mark as processing
		if err := h.idempotencyChecker.MarkProcessing(ctx, idempotencyKey, TaskType, 24*time.Hour); err != nil {
			h.logger.Warn("Failed to mark processing, continuing", map[string]interface{}{
				"error":   err.Error(),
				"traceId": traceID,
			})
		}
	}

	// ===== STEP 5: EXECUTE BUSINESS LOGIC =====
	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "email-send.Execute")
	output, err := h.service.Execute(ctxExec, input)
	spanExec.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("execution_error", true))
		errorCode := extractErrorCode(err)
		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
		if h.idempotencyChecker != nil {
			failKey := h.keyGenerator.GenerateNotificationKeySimple("email", input.To)
			h.idempotencyChecker.MarkFailed(ctx, failKey)
		}
		h.failJob(ctx, client, job, err)
		return
	}
	// if err != nil {
	// 	span.RecordError(err)
	// 	span.SetAttributes(attribute.Bool("execution_error", true))
	// 	errorCode := extractErrorCode(err)
	// 	metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
	// 	h.failJob(ctx, client, job, err)
	// 	return
	// }

	// ===== ✅ STEP 6: STORE SUCCESS RESULT =====
	if h.idempotencyChecker != nil && output.Success {
		_ = time.Now().Format("2006-01-02")
		idempotencyKey := h.keyGenerator.GenerateNotificationKeySimple("email", input.To)

		response := map[string]interface{}{
			"success":   output.Success,
			"messageId": output.MessageID,
			"provider":  output.Provider,
			"sentAt":    output.SentAt,
		}

		if err := h.idempotencyChecker.MarkCompleted(ctx, idempotencyKey, response); err != nil {
			h.logger.Warn("Failed to mark completed", map[string]interface{}{
				"error":   err.Error(),
				"traceId": traceID,
			})
		}
	}

	// ===== STEP 7: COMPLETE JOB =====
	ctxComp, spanComp := otel.Tracer("worker-manager").Start(ctx, "email-send.completeJob")
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

	resolveEmailAliases(variables)

	// ✅ DEBUG: Log variables after alias resolution
	h.logger.Info("Email job variables debug", map[string]interface{}{
		"hasTo":        variables["to"] != nil,
		"hasSubject":   variables["subject"] != nil,
		"hasBody":      variables["body"] != nil,
		"toValue":      variables["to"],
		"subjectValue": variables["subject"],
		"bodyPreview": func() string {
			if body, ok := variables["body"].(string); ok && len(body) > 100 {
				return body[:100] + "..."
			}
			return fmt.Sprintf("%v", variables["body"])
		}(),
		"allKeys": fmt.Sprintf("%v", getMapKeys(variables)),
	})

	schema := GetInputSchema()
	validationResult := validation.ValidateInput(variables, schema)
	if !validationResult.Valid {
		h.logger.Warn("Schema validation failed", map[string]interface{}{
			"errors": validationResult.GetErrorMessages(),
			"keys":   fmt.Sprintf("%v", getMapKeys(variables)),
			"variables": map[string]interface{}{
				"to":      variables["to"],
				"subject": variables["subject"],
				"body":    variables["body"],
			},
		})
		return nil, &errors.StandardError{
			Code:      "VALIDATION_FAILED",
			Message:   "Input validation failed",
			Details:   fmt.Sprintf("Validation errors: %v", validationResult.GetErrorMessages()),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	input := &Input{
		To:      variables["to"].(string),
		Subject: variables["subject"].(string),
		Body:    variables["body"].(string),
	}

	if from, ok := variables["from"].(string); ok && from != "" {
		input.From = from
	} else {
		input.From = h.config.DefaultFrom
	}

	if cc, ok := variables["cc"].(string); ok {
		input.CC = cc
	}

	if bcc, ok := variables["bcc"].(string); ok {
		input.BCC = bcc
	}

	if replyTo, ok := variables["replyTo"].(string); ok {
		input.ReplyTo = replyTo
	}

	if isHTML, ok := variables["isHtml"].(bool); ok {
		input.IsHTML = isHTML
	}

	if priority, ok := variables["priority"].(string); ok {
		input.Priority = priority
	}

	if attachments, ok := variables["attachments"].([]interface{}); ok {
		input.Attachments = make([]Attachment, 0, len(attachments))
		for _, att := range attachments {
			if attMap, ok := att.(map[string]interface{}); ok {
				attachment := Attachment{}

				if filename, ok := attMap["filename"].(string); ok {
					attachment.Filename = filename
				}
				if contentType, ok := attMap["contentType"].(string); ok {
					attachment.ContentType = contentType
				}
				if content, ok := attMap["content"].(string); ok {
					attachment.Content = content
				}

				if attachment.Filename != "" && attachment.Content != "" {
					input.Attachments = append(input.Attachments, attachment)
				}
			}
		}
	}

	if metadata, ok := variables["metadata"].(map[string]interface{}); ok {
		input.Metadata = metadata
	}

	return input, nil
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	variables := map[string]interface{}{
		"emailSent":    output.Success,
		"emailMessage": output.Message,
	}

	if output.MessageID != "" {
		variables["messageId"] = output.MessageID
	}

	if output.Provider != "" {
		variables["emailProvider"] = output.Provider
	}

	if !output.SentAt.IsZero() {
		variables["sentAt"] = output.SentAt.Format(time.RFC3339)
	}

	if h.service != nil {
		metrics := h.service.GetCircuitBreakerMetrics()
		if metrics != nil {
			variables["circuitBreaker"] = map[string]interface{}{
				"state":         metrics["state"],
				"failureCount":  metrics["failureCount"],
				"successCount":  metrics["successCount"],
				"pendingEmails": metrics["pendingEmails"],
			}
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
		h.logger.Info("Successfully completed email send", map[string]interface{}{
			"jobKey":    job.GetKey(),
			"emailSent": output.Success,
			"messageId": output.MessageID,
			"worker":    TaskType,
			"traceId":   trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	return h.service.Execute(ctx, input)
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	if err := ValidateInput(input); err != nil {
		return nil, err
	}
	return h.service.Execute(ctx, input)
}

func (h *Handler) failJob(ctx context.Context, client worker.JobClient, job entities.Job, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	h.errorHandler.HandleJobError(ctx, client, job, err)
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

	h.logger.Info("Email send worker registered with Camunda", map[string]interface{}{
		"taskType":      TaskType,
		"maxJobsActive": h.config.MaxJobsActive,
		"timeout":       h.config.Timeout.String(),
		"enabled":       h.config.Enabled,
		"smtpHost":      h.config.SMTPHost,
		"smtpPort":      h.config.SMTPPort,
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
		return fmt.Errorf("smtp health check failed: %w", err)
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
	if h.service != nil {
		return h.service.GetCircuitBreakerMetrics()
	}
	return map[string]interface{}{
		"error": "Service not initialized",
	}
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
		if workerCfg, exists := appConfig.Workers["email-send"]; exists {
			cfg.Enabled = workerCfg.Enabled
			if workerCfg.MaxJobsActive > 0 {
				cfg.MaxJobsActive = workerCfg.MaxJobsActive
			}
			if workerCfg.Timeout > 0 {
				cfg.Timeout = time.Duration(workerCfg.Timeout) * time.Millisecond
			}
		}

		if appConfig.Integrations.SMTP.Host != "" {
			cfg.SMTPHost = appConfig.Integrations.SMTP.Host
			cfg.SMTPPort = appConfig.Integrations.SMTP.Port
			cfg.SMTPUsername = appConfig.Integrations.SMTP.Username
			cfg.SMTPPassword = appConfig.Integrations.SMTP.Password
			cfg.UseTLS = appConfig.Integrations.SMTP.UseTLS
			cfg.DefaultFrom = appConfig.Integrations.SMTP.DefaultFrom
		}
	}

	return cfg
}

func resolveEmailAliases(vars map[string]interface{}) {
	// Map "email" → "to" if "to" is absent or nil
	if val, ok := vars["to"]; !ok || val == nil || val == "" {
		if email, ok := vars["email"].(string); ok && email != "" {
			vars["to"] = email
		}
	} else if _, isString := val.(string); !isString {
		vars["to"] = fmt.Sprintf("%v", val)
	}

	// Build subject from applicant name if absent or nil
	if val, ok := vars["subject"]; !ok || val == nil || val == "" {
		name, _ := vars["fullName"].(string)
		if name == "" {
			name = "Applicant"
		}
		vars["subject"] = fmt.Sprintf("Thank you for your franchise enquiry, %s", name)
	} else if _, isString := val.(string); !isString {
		vars["subject"] = fmt.Sprintf("%v", val)
	}

	// Build body from enquiry fields if absent or nil
	if val, ok := vars["body"]; !ok || val == nil || val == "" {
		city, _ := vars["city"].(string)
		franchise, _ := vars["franchiseId"].(string)
		fullName, _ := vars["fullName"].(string)
		if fullName == "" {
			fullName = "Applicant"
		}

		vars["body"] = fmt.Sprintf(
			"Dear %s,\n\nWe have received your enquiry for franchise %s in %s. Our team will contact you shortly.\n\nTeam LeMiCi",
			fullName, franchise, city,
		)
	} else if _, isString := val.(string); !isString {
		vars["body"] = fmt.Sprintf("%v", val)
	}
}

func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
