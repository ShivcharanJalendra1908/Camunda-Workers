package captchaverify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/metrics"
	"camunda-workers/internal/common/validation"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const TaskType = "captcha.verify"

type Handler struct {
	config       *Config
	logger       logger.Logger
	camunda      *camunda.Client
	service      *Service
	jobWorker    worker.JobWorker
	errorHandler *errors.ErrorHandler
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
}

type HandlerOptions struct {
	AppConfig    *config.Config
	Camunda      *camunda.Client
	CustomConfig *Config
	Logger       logger.Logger
}

func NewHandler(opts HandlerOptions) (*Handler, error) {
	workerConfig := createConfigFromAppConfig(opts.AppConfig, opts.CustomConfig)

	if err := workerConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration for captcha-verify: %w", err)
	}

	var loggerInstance logger.Logger
	if opts.Logger != nil {
		loggerInstance = opts.Logger
	} else {
		loggerInstance = logger.NewStructured("info", "json")
	}

	handler := &Handler{
		config:       workerConfig,
		logger:       loggerInstance,
		camunda:      opts.Camunda,
		errorHandler: errors.NewErrorHandler(loggerInstance),
		validator:    validation.NewValidator(),
		sanitizer:    validation.NewSanitizer(),
	}

	handler.service = NewService(ServiceDependencies{
		Logger: loggerInstance,
		Config: workerConfig,
	}, workerConfig)

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

	// 🔥 START: ORIGINAL CODE WITH METRICS AND TRACING 🔥
	startTime := time.Now()
	metrics.WorkerJobsActive.WithLabelValues(TaskType).Inc()
	defer metrics.WorkerJobsActive.WithLabelValues(TaskType).Dec()

	ctx, cancel := context.WithTimeout(ctx, h.config.Timeout) // ✅ Use traced context
	defer cancel()

	h.logger.Info("Processing captcha verification request", map[string]interface{}{
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
			Valid:   false,
			Message: "Captcha verification disabled",
		})
		return
	}
	// 🔥 END: ORIGINAL CODE 🔥

	// ===== STEP 1: PARSE INPUT (with original JSON schema validation) =====
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "captcha-verify.parseInput")
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

	// ===== STEP 2: SANITIZE INPUT (GAP #1 FIX) =====
	_, spanSanitize := otel.Tracer("worker-manager").Start(ctx, "captcha-verify.sanitizeInput")
	input.Sanitize()
	spanSanitize.End()

	// ===== STEP 3: VALIDATE INPUT (GAP #1 FIX - Business logic validation) =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "captcha-verify.validateInput")
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

	// ===== STEP 4: EXECUTE BUSINESS LOGIC =====
	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "captcha-verify.Execute")
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

	// ===== STEP 5: COMPLETE JOB =====
	ctxComp, spanComp := otel.Tracer("worker-manager").Start(ctx, "captcha-verify.completeJob")
	h.completeJob(ctxComp, client, job, output)
	spanComp.End()
	metrics.WorkerJobsCompleted.WithLabelValues(TaskType).Inc()
	metrics.WorkerJobDuration.WithLabelValues(TaskType).Observe(time.Since(startTime).Seconds())
}

// Template compatible execute method
func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	return h.service.Execute(ctx, input)
}

// Template compatible completeJobSimple method
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

// Public Execute method for direct API calls
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// Validate before execution
	if err := ValidateInput(input); err != nil {
		return nil, err
	}
	return h.service.Execute(ctx, input)
}

// 🔥 ORIGINAL parseInput FUNCTION (with JSON schema validation)
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
		CaptchaID:    variables["captchaId"].(string),
		CaptchaValue: variables["captchaValue"].(string),
		ClientIP:     variables["clientIp"].(string),
		UserAgent:    variables["userAgent"].(string),
	}

	if sessionID, ok := variables["sessionId"].(string); ok {
		input.SessionID = sessionID
	}

	if metadata, ok := variables["metadata"].(map[string]interface{}); ok {
		input.Metadata = metadata
	}

	return input, nil
}

// 🔥 ORIGINAL completeJob FUNCTION
func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	variables := map[string]interface{}{
		"captchaValid":   output.Valid,
		"captchaMessage": output.Message,
	}

	if output.Reason != "" {
		variables["captchaReason"] = output.Reason
	}

	if output.AttemptsRemaining > 0 {
		variables["attemptsRemaining"] = output.AttemptsRemaining
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
		h.logger.Info("Successfully completed captcha verification", map[string]interface{}{
			"jobKey":       job.GetKey(),
			"captchaValid": output.Valid,
			"reason":       output.Reason,
			"worker":       TaskType,
			"traceId":      trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}
}

// Original failJob method
func (h *Handler) failJob(ctx context.Context, client worker.JobClient, job entities.Job, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetAttributes(attribute.Bool("error", true))
	h.errorHandler.HandleJobError(ctx, client, job, err)
}

// 🔥 ORIGINAL REGISTRATION & MANAGEMENT FUNCTIONS 🔥

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

	h.logger.Info("Captcha verify worker registered with Camunda", map[string]interface{}{
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

	// Test Redis connection
	if err := h.service.TestConnection(ctx); err != nil {
		return fmt.Errorf("captcha service health check failed: %w", err)
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

// 🔥 ORIGINAL HELPER FUNCTIONS 🔥

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
		Code:      "CAPTCHA_VERIFICATION_ERROR",
		Message:   "Captcha verification failed",
		Details:   err.Error(),
		Retryable: false,
		Timestamp: time.Now(),
	}
}

func createConfigFromAppConfig(appConfig *config.Config, customConfig *Config) *Config {
	cfg := DefaultConfig()

	// ✅ merge customConfig (if provided)
	if customConfig != nil {
		if customConfig.Enabled {
			cfg.Enabled = customConfig.Enabled
		}
		if customConfig.MaxJobsActive > 0 {
			cfg.MaxJobsActive = customConfig.MaxJobsActive
		}
		if customConfig.Timeout > 0 {
			cfg.Timeout = customConfig.Timeout
		}
		if customConfig.MaxAttempts > 0 {
			cfg.MaxAttempts = customConfig.MaxAttempts
		}
		if customConfig.ExpiryMinutes > 0 {
			cfg.ExpiryMinutes = customConfig.ExpiryMinutes
		}
		if customConfig.RedisHost != "" {
			cfg.RedisHost = customConfig.RedisHost
		}
		if customConfig.RedisPort > 0 {
			cfg.RedisPort = customConfig.RedisPort
		}
		cfg.RedisPassword = customConfig.RedisPassword
		cfg.RedisDB = customConfig.RedisDB
	}

	if appConfig == nil {
		return cfg
	}

	// ✅ Read REDIS_ADDRESS correctly
	if appConfig.Database.Redis.Address != "" {
		host, port := parseRedisAddress(appConfig.Database.Redis.Address)
		if host != "" {
			cfg.RedisHost = host
		}
		if port > 0 {
			cfg.RedisPort = port
		}
		cfg.RedisPassword = appConfig.Database.Redis.Password
		cfg.RedisDB = appConfig.Database.Redis.DB
	}

	// worker override
	if workerCfg, ok := appConfig.Workers["captcha-verify"]; ok {
		cfg.Enabled = workerCfg.Enabled
		if workerCfg.MaxJobsActive > 0 {
			cfg.MaxJobsActive = workerCfg.MaxJobsActive
		}
		if workerCfg.Timeout > 0 {
			cfg.Timeout = time.Duration(workerCfg.Timeout) * time.Millisecond
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

// package captchaverify

// import (
// 	"context"
// 	"fmt"
// 	"time"

// 	"camunda-workers/internal/common/camunda"
// 	"camunda-workers/internal/common/config"
// 	"camunda-workers/internal/common/errors"
// 	"camunda-workers/internal/common/logger"
// 	"camunda-workers/internal/common/metrics"
// 	"camunda-workers/internal/common/validation"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

// 	"go.opentelemetry.io/otel"
// )

// const TaskType = "captcha.verify"

// type Handler struct {
// 	config       *Config
// 	logger       logger.Logger
// 	camunda      *camunda.Client
// 	service      *Service
// 	jobWorker    worker.JobWorker
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
// 		return nil, fmt.Errorf("invalid configuration for captcha-verify: %w", err)
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

// 	h.logger.Info("Processing captcha verification request", map[string]interface{}{
// 		"jobKey":             job.GetKey(),
// 		"processInstanceKey": job.GetProcessInstanceKey(),
// 		"worker":             TaskType,
// 	})

// 	if !h.config.Enabled {
// 		h.logger.Info("Worker disabled by configuration", map[string]interface{}{
// 			"worker": TaskType,
// 		})
// 		h.completeJob(ctx, client, job, &Output{
// 			Valid:   false,
// 			Message: "Captcha verification disabled",
// 		})
// 		return
// 	}

// 	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "captcha-verify.parseInput")
// 	input, err := h.parseInput(job)
// 	spanParse.End()
// 	if err != nil {
// 		errorCode := extractErrorCode(err)
// 		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
// 		h.errorHandler.HandleJobError(ctx, client, job, err)
// 		return
// 	}

// 	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "captcha-verify.Execute")
// 	output, err := h.service.Execute(ctxExec, input)
// 	spanExec.End()
// 	if err != nil {
// 		errorCode := extractErrorCode(err)
// 		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
// 		h.errorHandler.HandleJobError(ctx, client, job, err)
// 		return
// 	}

// 	ctxComp, spanComp := otel.Tracer("worker-manager").Start(ctx, "captcha-verify.completeJob")
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
// 		CaptchaID:    variables["captchaId"].(string),
// 		CaptchaValue: variables["captchaValue"].(string),
// 		ClientIP:     variables["clientIp"].(string),
// 		UserAgent:    variables["userAgent"].(string),
// 	}

// 	if sessionID, ok := variables["sessionId"].(string); ok {
// 		input.SessionID = sessionID
// 	}

// 	if metadata, ok := variables["metadata"].(map[string]interface{}); ok {
// 		input.Metadata = metadata
// 	}

// 	return input, nil
// }

// func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
// 	variables := map[string]interface{}{
// 		"captchaValid":   output.Valid,
// 		"captchaMessage": output.Message,
// 	}

// 	if output.Reason != "" {
// 		variables["captchaReason"] = output.Reason
// 	}

// 	if output.AttemptsRemaining > 0 {
// 		variables["attemptsRemaining"] = output.AttemptsRemaining
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
// 		h.logger.Info("Successfully completed captcha verification", map[string]interface{}{
// 			"jobKey":       job.GetKey(),
// 			"captchaValid": output.Valid,
// 			"reason":       output.Reason,
// 			"worker":       TaskType,
// 		})
// 	}
// }

// func (h *Handler) failJob(ctx context.Context, client worker.JobClient, job entities.Job, err error) {
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

// 	h.logger.Info("Captcha verify worker registered with Camunda", map[string]interface{}{
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

// 	// Test Redis connection
// 	if err := h.service.TestConnection(ctx); err != nil {
// 		return fmt.Errorf("captcha service health check failed: %w", err)
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

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.service.Execute(ctx, input)
// }

// // Helper functions

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
// 		Code:      "CAPTCHA_VERIFICATION_ERROR",
// 		Message:   "Captcha verification failed",
// 		Details:   err.Error(),
// 		Retryable: false,
// 		Timestamp: time.Now(),
// 	}
// }

// // func createConfigFromAppConfig(appConfig *config.Config, customConfig *Config) *Config {
// // 	if customConfig != nil {
// // 		return customConfig
// // 	}

// // 	cfg := DefaultConfig()

// // 	if appConfig != nil {
// // 		if workerCfg, exists := appConfig.Workers["captcha-verify"]; exists {
// // 			cfg.Enabled = workerCfg.Enabled
// // 			if workerCfg.MaxJobsActive > 0 {
// // 				cfg.MaxJobsActive = workerCfg.MaxJobsActive
// // 			}
// // 			if workerCfg.Timeout > 0 {
// // 				cfg.Timeout = time.Duration(workerCfg.Timeout) * time.Millisecond
// // 			}
// // 		}

// // 		// Load Redis configuration
// // 		if appConfig.Database.Redis.Address != "" {
// // 			host, port := parseRedisAddress(appConfig.Database.Redis.Address)
// // 			cfg.RedisHost = host
// // 			cfg.RedisPort = port
// // 			cfg.RedisPassword = appConfig.Database.Redis.Password
// // 			cfg.RedisDB = appConfig.Database.Redis.DB
// // 		}
// // 	}

// // 	return cfg
// // }
// func createConfigFromAppConfig(appConfig *config.Config, customConfig *Config) *Config {
// 	cfg := DefaultConfig() // 🔥 ALWAYS start from defaults

// 	// ✅ merge customConfig (if provided)
// 	if customConfig != nil {
// 		if customConfig.Enabled {
// 			cfg.Enabled = customConfig.Enabled
// 		}
// 		if customConfig.MaxJobsActive > 0 {
// 			cfg.MaxJobsActive = customConfig.MaxJobsActive
// 		}
// 		if customConfig.Timeout > 0 {
// 			cfg.Timeout = customConfig.Timeout
// 		}
// 		if customConfig.MaxAttempts > 0 {
// 			cfg.MaxAttempts = customConfig.MaxAttempts
// 		}
// 		if customConfig.ExpiryMinutes > 0 {
// 			cfg.ExpiryMinutes = customConfig.ExpiryMinutes
// 		}
// 		if customConfig.RedisHost != "" {
// 			cfg.RedisHost = customConfig.RedisHost
// 		}
// 		if customConfig.RedisPort > 0 {
// 			cfg.RedisPort = customConfig.RedisPort
// 		}
// 		cfg.RedisPassword = customConfig.RedisPassword
// 		cfg.RedisDB = customConfig.RedisDB
// 	}

// 	if appConfig == nil {
// 		return cfg
// 	}

// 	// ✅ Read REDIS_ADDRESS correctly
// 	if appConfig.Database.Redis.Address != "" {
// 		host, port := parseRedisAddress(appConfig.Database.Redis.Address)
// 		if host != "" {
// 			cfg.RedisHost = host
// 		}
// 		if port > 0 {
// 			cfg.RedisPort = port
// 		}
// 		cfg.RedisPassword = appConfig.Database.Redis.Password
// 		cfg.RedisDB = appConfig.Database.Redis.DB
// 	}

// 	// worker override
// 	if workerCfg, ok := appConfig.Workers["captcha-verify"]; ok {
// 		cfg.Enabled = workerCfg.Enabled
// 		if workerCfg.MaxJobsActive > 0 {
// 			cfg.MaxJobsActive = workerCfg.MaxJobsActive
// 		}
// 		if workerCfg.Timeout > 0 {
// 			cfg.Timeout = time.Duration(workerCfg.Timeout) * time.Millisecond
// 		}
// 	}

// 	return cfg
// }

// func parseRedisAddress(address string) (string, int) {
// 	host := address
// 	port := 6379 // default

// 	// Find the last colon to split host:port
// 	for i := len(address) - 1; i >= 0; i-- {
// 		if address[i] == ':' {
// 			host = address[:i]
// 			fmt.Sscanf(address[i+1:], "%d", &port)
// 			break
// 		}
// 	}

// 	return host, port
// }
