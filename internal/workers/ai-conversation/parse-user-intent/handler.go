package parseuserintent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"camunda-workers/internal/common/circuitbreaker"
	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/validation"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
)

const (
	TaskType = "parse-user-intent"
)

var (
	ErrIntentParsingFailed = errors.New("INTENT_PARSING_FAILED")
	ErrIntentAPITimeout    = errors.New("INTENT_API_TIMEOUT")
)

// Logger interface definition
type Logger interface {
	Info(msg string, fields map[string]interface{})
	Warn(msg string, fields map[string]interface{})
	Error(msg string, fields map[string]interface{})
	With(fields map[string]interface{}) Logger
}

type Handler struct {
	config       *Config
	client       *http.Client
	logger       Logger
	cb           *circuitbreaker.CircuitBreaker
	errorHandler *appErrs.ErrorHandler
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
}

type HandlerOptions struct {
	Config    *Config
	Logger    Logger
	CBManager *circuitbreaker.Manager
}

func NewHandler(opts HandlerOptions) *Handler {
	// Create circuit breaker if not provided via manager
	var cb *circuitbreaker.CircuitBreaker
	if opts.CBManager != nil {
		cb = opts.CBManager.GetOrCreate("genai-service", circuitbreaker.Config{
			Name:             "genai-service",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
		})
	} else {
		cb = circuitbreaker.New(circuitbreaker.Config{
			Name:             "genai-service",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
		})
	}

	return &Handler{
		config: opts.Config,
		client: &http.Client{
			Timeout: opts.Config.Timeout,
		},
		logger: opts.Logger.With(map[string]interface{}{
			"taskType": TaskType,
		}),
		cb:           cb,
		errorHandler: appErrs.NewErrorHandler(opts.Logger),
		validator:    validation.NewValidator(),
		sanitizer:    validation.NewSanitizer(),
	}
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
		if rid, ok := jobVars["requestId"].(string); ok && rid != "" {
			ctx = context.WithValue(ctx, "requestId", rid)
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

	// ===== STEP 1: PARSE INPUT =====
	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "parse-user-intent.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewValidationError("input", fmt.Sprintf("parse input: %v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== STEP 2: VALIDATE INPUT =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "parse-user-intent.validateInput")
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	// ===== STEP 3: EXECUTE BUSINESS LOGIC =====
	ctxExec, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()

	ctxExec2, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "parse-user-intent.Execute")
	res, err := h.cb.Execute(func() (interface{}, error) {
		return h.execute(ctxExec2, &input)
	})
	spanExec.End()

	if err != nil {
		if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("circuit_breaker.open", true))
			h.logger.Warn("Circuit breaker open for GenAI service, using fallback", map[string]interface{}{
				"circuitState": h.cb.State(),
				"question":     input.Question,
				"traceId":      traceID,
			})

			// Add circuit breaker metrics to fallback
			circuitMetrics := h.cb.Metrics()

			// Fallback: Return 'search' intent so user isn't blocked
			fallbackOutput := &Output{
				IntentAnalysis: IntentAnalysis{
					PrimaryIntent: "search",
					Confidence:    0.1,
				},
				DataSources: []string{"web_search", "internal_db"},
				Entities:    []Entity{},
				CircuitBreaker: map[string]interface{}{
					"state":            circuitMetrics["state"],
					"failureCount":     circuitMetrics["failureCount"],
					"successCount":     circuitMetrics["successCount"],
					"concurrentCalls":  circuitMetrics["concurrentCalls"],
					"requestLatencyMs": circuitMetrics["requestLatencyMs"],
					"fallbackUsed":     true,
				},
			}
			_, spanCompFb := otel.Tracer("worker-manager").Start(ctx, "parse-user-intent.completeJob.fallback")
			h.completeJob(ctx, client, job, fallbackOutput)
			spanCompFb.End()
			return
		}

		var stdErr *appErrs.StandardError
		if errors.Is(err, ErrIntentAPITimeout) {
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("timeout", true))
			stdErr = appErrs.NewIntentAPITimeoutError()
		} else if errors.Is(err, ErrIntentParsingFailed) {
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("error", true))
			stdErr = appErrs.NewIntentParsingFailedError(err)
		} else {
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("error", true))
			stdErr = appErrs.NewExternalServiceError("GenAI parse-intent", err)
		}
		h.errorHandler.HandleJobError(ctx, client, job, stdErr)
		return
	}

	// ===== STEP 4: COMPLETE JOB (with circuit breaker metrics) =====
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "parse-user-intent.completeJob")

	output := res.(*Output)
	// Add circuit breaker metrics to successful response
	circuitMetrics := h.cb.Metrics()
	output.CircuitBreaker = map[string]interface{}{
		"state":            circuitMetrics["state"],
		"failureCount":     circuitMetrics["failureCount"],
		"successCount":     circuitMetrics["successCount"],
		"concurrentCalls":  circuitMetrics["concurrentCalls"],
		"requestLatencyMs": circuitMetrics["requestLatencyMs"],
		"fallbackUsed":     false,
	}

	h.completeJob(ctx, client, job, output)
	spanComp.End()
}

func (h *Handler) validateInput(input *Input) error {
	// Validate Question
	if err := ozzo.Validate(input.Question,
		ozzo.Required.Error("question is required"),
		ozzo.Length(1, 1000).Error("question must be 1-1000 characters"),
		validation.SafeSQLString,
		validation.SafeNoSQLString,
	); err != nil {
		return appErrs.NewValidationError("question", err.Error())
	}

	// Validate Context (if provided)
	if input.Context != nil {
		if err := h.validateContext(input.Context); err != nil {
			return err
		}
	}

	// 🔒 ADDITIONAL SECURITY CHECKS
	// Prevent script injection in question
	if strings.Contains(strings.ToLower(input.Question), "javascript:") ||
		strings.Contains(strings.ToLower(input.Question), "script:") ||
		strings.Contains(strings.ToLower(input.Question), "data:") {
		return appErrs.NewValidationError("question", "contains potentially unsafe content")
	}

	// Prevent SQL injection patterns
	sqlPatterns := []string{
		"' OR '1'='1", "' OR '1'='1' --", "'; DROP TABLE", "UNION SELECT",
		"INSERT INTO", "UPDATE", "DELETE FROM",
	}

	lowerQuestion := strings.ToLower(input.Question)
	for _, pattern := range sqlPatterns {
		if strings.Contains(lowerQuestion, strings.ToLower(pattern)) {
			return appErrs.NewValidationError("question", "contains potentially unsafe SQL patterns")
		}
	}

	// Prevent file system access patterns
	fsPatterns := []string{
		"../", "..\\", "/etc/", "/bin/", "C:\\", "file://",
	}

	for _, pattern := range fsPatterns {
		if strings.Contains(lowerQuestion, pattern) {
			return appErrs.NewValidationError("question", "contains file system access patterns")
		}
	}

	return nil
}

func (h *Handler) validateContext(context map[string]interface{}) error {
	// Limit context size
	if len(context) > 20 {
		return appErrs.NewValidationError("context", "cannot exceed 20 items")
	}

	for key, value := range context {
		// Validate key
		if err := ozzo.Validate(key,
			ozzo.Length(1, 100).Error("key must be 1-100 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewValidationError(fmt.Sprintf("context.%s", key), err.Error())
		}

		// Validate value based on type
		switch v := value.(type) {
		case string:
			// Limit string length
			if len(v) > 500 {
				return appErrs.NewValidationError(fmt.Sprintf("context.%s", key),
					"string value cannot exceed 500 characters")
			}

			// Check for dangerous patterns
			lowerVal := strings.ToLower(v)
			if strings.Contains(lowerVal, "<script") ||
				strings.Contains(lowerVal, "javascript:") ||
				strings.Contains(lowerVal, "eval(") {
				return appErrs.NewValidationError(fmt.Sprintf("context.%s", key),
					"contains potentially unsafe content")
			}

		case []interface{}:
			// Limit array size
			if len(v) > 50 {
				return appErrs.NewValidationError(fmt.Sprintf("context.%s", key),
					"array cannot exceed 50 items")
			}

			// Validate array items
			for i, item := range v {
				if strItem, ok := item.(string); ok {
					if len(strItem) > 200 {
						return appErrs.NewValidationError(fmt.Sprintf("context.%s[%d]", key, i),
							"string item cannot exceed 200 characters")
					}
				}
			}

		case map[string]interface{}:
			// Prevent nested objects (too deep)
			return appErrs.NewValidationError(fmt.Sprintf("context.%s", key),
				"nested objects are not allowed")

		case float64, int, bool:
			// Simple types are fine
			continue

		default:
			return appErrs.NewValidationError(fmt.Sprintf("context.%s", key),
				fmt.Sprintf("unsupported type: %T", v))
		}
	}

	return nil
}

func (h *Handler) doRequest(req *http.Request) (*http.Response, error) {
	result, err := h.cb.Execute(func() (interface{}, error) {
		resp, err := h.client.Do(req)
		if err != nil {
			return nil, err
		}

		// Treat 5xx errors as circuit breaker failures
		if resp.StatusCode >= 500 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("genai server error %d: %s", resp.StatusCode, string(body))
		}

		return resp, nil
	})

	if err != nil {
		return nil, err
	}

	return result.(*http.Response), nil
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	// 🔒 Sanitize input before processing
	input.Question = h.sanitizer.SanitizeString(input.Question)

	// Sanitize context if provided
	if input.Context != nil {
		sanitizedContext := h.sanitizer.SanitizeInput(input.Context)
		input.Context = sanitizedContext
	}

	requestBody := map[string]interface{}{
		"query": input.Question,
	}

	// Only include context if it's not nil
	if input.Context != nil {
		requestBody["context"] = input.Context
	}

	body, _ := json.Marshal(requestBody)
	req, err := http.NewRequestWithContext(ctx, "POST", h.config.GenAIBaseURL+"/api/ai/parse-intent", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIntentParsingFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Propagate X-Request-ID for end-to-end tracing
	if reqID, ok := ctx.Value("requestId").(string); ok && reqID != "" {
		req.Header.Set("X-Request-ID", reqID)
	}

	var resp *http.Response
	var lastErr error

	for attempt := 0; attempt <= h.config.MaxRetries; attempt++ {

		if attempt > 0 {
			// Apply exponential backoff for retries
			backoff := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ErrIntentAPITimeout
			}
		}

		resp, lastErr = h.doRequest(req)

		// FIX: If context expired during the request, return timeout immediately.
		if ctx.Err() != nil ||
			errors.Is(lastErr, context.DeadlineExceeded) ||
			errors.Is(lastErr, context.Canceled) {

			return nil, ErrIntentAPITimeout
		}

		if lastErr == nil {
			// Check if response is successful
			if resp.StatusCode == http.StatusOK {
				break
			}
			// For non-OK status codes, treat as error and retry
			resp.Body.Close()
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
			resp = nil
		}
	}

	if lastErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrIntentAPITimeout
		}
		return nil, fmt.Errorf("%w: %v", ErrIntentParsingFailed, lastErr)
	}

	if resp == nil {
		return nil, fmt.Errorf("%w: no successful response after retries", ErrIntentParsingFailed)
	}
	defer resp.Body.Close()

	var apiResponse struct {
		Intent      string   `json:"intent"`
		Confidence  float64  `json:"confidence"`
		Entities    []Entity `json:"entities"`
		DataSources []string `json:"dataSources"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("%w: decode error: %v", ErrIntentParsingFailed, err)
	}

	// 🔒 Validate API response
	if err := h.validateAPIResponse(&apiResponse); err != nil {
		return nil, fmt.Errorf("%w: invalid response: %v", ErrIntentParsingFailed, err)
	}

	dataSources := apiResponse.DataSources
	if len(dataSources) == 0 {
		dataSources = h.determineDataSources(apiResponse.Intent, apiResponse.Entities)
	}

	output := &Output{
		IntentAnalysis: IntentAnalysis{
			PrimaryIntent: apiResponse.Intent,
			Confidence:    apiResponse.Confidence,
		},
		DataSources: dataSources,
		Entities:    apiResponse.Entities,
	}

	h.logger.Info("intent parsed successfully", map[string]interface{}{
		"intent":      apiResponse.Intent,
		"confidence":  apiResponse.Confidence,
		"entityCount": len(apiResponse.Entities),
		"dataSources": dataSources,
		"traceId":     trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
	})

	return output, nil
}

func (h *Handler) validateAPIResponse(response *struct {
	Intent      string   `json:"intent"`
	Confidence  float64  `json:"confidence"`
	Entities    []Entity `json:"entities"`
	DataSources []string `json:"dataSources"`
}) error {
	// Validate intent
	if response.Intent == "" {
		return fmt.Errorf("intent is empty")
	}

	// Validate confidence range
	if response.Confidence < 0 || response.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0 and 1")
	}

	// Validate entities array size
	if len(response.Entities) > 50 {
		return fmt.Errorf("entities array too large (max 50)")
	}

	// Validate each entity
	for i, entity := range response.Entities {
		// Validate entity type
		if entity.Type == "" {
			return fmt.Errorf("entity[%d].type is empty", i)
		}
		if len(entity.Type) > 50 {
			return fmt.Errorf("entity[%d].type too long (max 50)", i)
		}

		// Validate entity value
		if entity.Value == "" {
			return fmt.Errorf("entity[%d].value is empty", i)
		}
		if len(entity.Value) > 200 {
			return fmt.Errorf("entity[%d].value too long (max 200)", i)
		}

		// Validate entity type is allowed
		allowedTypes := []string{"franchise_name", "location", "category", "investment_amount",
			"business", "industry", "service", "product", "person", "organization"}

		validType := false
		for _, allowed := range allowedTypes {
			if entity.Type == allowed {
				validType = true
				break
			}
		}

		if !validType {
			return fmt.Errorf("entity[%d].type '%s' is not allowed", i, entity.Type)
		}
	}

	// Validate data sources array size
	if len(response.DataSources) > 10 {
		return fmt.Errorf("dataSources array too large (max 10)")
	}

	// Validate each data source
	for i, source := range response.DataSources {
		if source == "" {
			return fmt.Errorf("dataSources[%d] is empty", i)
		}
		if len(source) > 50 {
			return fmt.Errorf("dataSources[%d] too long (max 50)", i)
		}
	}

	return nil
}

func (h *Handler) determineDataSources(intent string, entities []Entity) []string {
	// Use a slice with fixed order to ensure deterministic results
	sources := []string{"internal_db"}
	hasSearch := false
	hasExternal := false

	for _, e := range entities {
		if e.Type == "franchise_name" || e.Type == "category" {
			hasSearch = true
		}
	}

	switch intent {
	case "general_info", "market_research", "competitor_analysis":
		hasExternal = true
	}

	if hasSearch {
		sources = append(sources, "search_index")
	}
	if hasExternal {
		sources = append(sources, "external_web")
	}

	return sources
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)

	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("Failed to create complete job command", map[string]interface{}{
			"jobKey":  job.Key,
			"error":   err.Error(),
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
		return
	}

	_, err = cmd.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("Failed to send complete job command", map[string]interface{}{
			"jobKey":  job.Key,
			"error":   err.Error(),
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// Validate before execution
	if err := h.validateInput(input); err != nil {
		return nil, err
	}
	return h.execute(ctx, input)
}

func (h *Handler) GetCircuitBreakerMetrics() map[string]interface{} {
	return h.cb.Metrics()
}

func (h *Handler) GetCircuitBreakerState() string {
	state := h.cb.State()
	switch state {
	case circuitbreaker.StateClosed:
		return "closed"
	case circuitbreaker.StateOpen:
		return "open"
	case circuitbreaker.StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}
