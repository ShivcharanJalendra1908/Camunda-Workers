package llmsynthesis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/google/uuid"

	"camunda-workers/internal/common/circuitbreaker"
	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/validation"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "llm-synthesis"

	// ✅ NEW: Error body read limit
	MaxErrorBodySize = 1024 // 1KB

	// ✅ NEW: Prompt limits
	MaxPromptLength                      = 10000
	MinPromptLengthForSentenceTruncation = 5000
)

var (
	ErrLLMTimeout         = errors.New("LLM_TIMEOUT")
	ErrLLMSynthesisFailed = errors.New("LLM_SYNTHESIS_FAILED")
	ErrLLMClientError     = errors.New("LLM_CLIENT_ERROR") // ✅ NEW: For 4xx errors
)

type Logger interface {
	Info(msg string, fields map[string]interface{})
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
	var cb *circuitbreaker.CircuitBreaker
	if opts.CBManager != nil {
		cb = opts.CBManager.GetOrCreate("llm-synthesis", circuitbreaker.Config{
			Name:             "llm-synthesis",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
		})
	} else {
		cb = circuitbreaker.New(circuitbreaker.Config{
			Name:             "llm-synthesis",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
		})
	}

	return &Handler{
		config: opts.Config,
		client: &http.Client{
			// ✅ FIXED: Add timeout as safety net
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
				DisableCompression:  false,
			},
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

	h.logger.Info("processing job", map[string]interface{}{
		"jobKey":      job.Key,
		"workflowKey": job.ProcessInstanceKey,
		"traceId":     traceID,
		"spanId":      span.SpanContext().SpanID().String(),
	})

	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "llm-synthesis.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewBusinessRuleError("Input parsing failed", err.Error()))
		spanParse.End()
		return
	}
	spanParse.End()

	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "llm-synthesis.validateInput")
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	ctxExec, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()

	ctxExec2, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "llm-synthesis.Execute")

	// ✅ FIXED: Single circuit breaker wrapping
	res, err := h.cb.Execute(func() (interface{}, error) {
		return h.execute(ctxExec2, &input)
	})
	spanExec.End()

	if err != nil {
		if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("circuit_breaker.open", true))
			h.logger.Info("Circuit breaker open for LLM service, using fallback", map[string]interface{}{
				"circuitState": h.cb.State(),
				"question":     input.Question,
				"traceId":      traceID,
			})

			circuitMetrics := h.cb.Metrics()
			fallbackOutput := &Output{
				LLMResponse: "I'm having trouble accessing my knowledge base right now. Please try again in a moment or rephrase your question.",
				Confidence:  0.1,
				Sources:     []string{"internal_knowledge_base"},
				CircuitBreaker: map[string]interface{}{
					"state":            circuitMetrics["state"],
					"failureCount":     circuitMetrics["failureCount"],
					"successCount":     circuitMetrics["successCount"],
					"concurrentCalls":  circuitMetrics["concurrentCalls"],
					"requestLatencyMs": circuitMetrics["requestLatencyMs"],
					"fallbackUsed":     true,
				},
			}
			_, spanCompFb := otel.Tracer("worker-manager").Start(ctx, "llm-synthesis.completeJob.fallback")
			h.completeJob(ctx, client, job, fallbackOutput)
			spanCompFb.End()
			return
		}

		var stdErr *appErrs.StandardError
		if errors.Is(err, ErrLLMTimeout) {
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("timeout", true))
			stdErr = appErrs.NewLLMTimeoutError()
		} else if errors.Is(err, ErrLLMClientError) {
			// ✅ NEW: Handle client errors (4xx) without retry
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("client_error", true))
			stdErr = appErrs.NewBusinessRuleError("Invalid request", err.Error())
		} else if errors.Is(err, ErrLLMSynthesisFailed) {
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("error", true))
			stdErr = appErrs.NewLLMSynthesisFailedError(err)
		} else {
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("error", true))
			stdErr = appErrs.NewExternalServiceError("llm-synthesis", err)
		}
		h.errorHandler.HandleJobError(ctx, client, job, stdErr)
		return
	}

	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "llm-synthesis.completeJob")
	output := res.(*Output)
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

// ✅ FIXED: Remove circuit breaker wrapping from doRequest
func (h *Handler) doRequest(req *http.Request) (*http.Response, error) {
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}

	// ✅ FIXED: Limit error body reading
	if resp.StatusCode >= 500 {
		limitedReader := io.LimitReader(resp.Body, MaxErrorBodySize)
		body, _ := io.ReadAll(limitedReader)
		resp.Body.Close()
		return nil, fmt.Errorf("llm server error %d: %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

func (h *Handler) validateInput(input *Input) error {
	if input.Question == "" {
		return appErrs.NewValidationError("question", "question is required")
	}

	if len(input.Question) < 3 || len(input.Question) > 1000 {
		return appErrs.NewValidationError("question", "question must be 3-1000 characters")
	}

	sqlPatterns := []string{
		"' OR '1'='1", "' OR '1'='1' --", "'; DROP TABLE", "UNION SELECT",
		"INSERT INTO", "UPDATE", "DELETE FROM", "EXEC", "EXECUTE",
	}

	lowerQuestion := strings.ToLower(input.Question)
	for _, pattern := range sqlPatterns {
		if strings.Contains(lowerQuestion, strings.ToLower(pattern)) {
			return appErrs.NewValidationError("question", "contains potentially unsafe SQL patterns")
		}
	}

	nosqlPatterns := []string{
		"$where", "$ne", "$gt", "$regex", "script:", "javascript:",
		"onload=", "onerror=", "eval(", "function(",
	}

	for _, pattern := range nosqlPatterns {
		if strings.Contains(lowerQuestion, pattern) {
			return appErrs.NewValidationError("question", "contains potentially unsafe NoSQL patterns")
		}
	}

	if strings.Contains(lowerQuestion, "javascript:") ||
		strings.Contains(lowerQuestion, "script:") ||
		strings.Contains(lowerQuestion, "data:") {
		return appErrs.NewValidationError("question", "contains potentially unsafe content")
	}

	fsPatterns := []string{
		"../", "..\\", "/etc/", "/bin/", "/usr/",
		"C:\\", "D:\\", "E:\\", "file://",
	}

	for _, pattern := range fsPatterns {
		if strings.Contains(lowerQuestion, pattern) {
			return appErrs.NewValidationError("question", "contains file system access patterns")
		}
	}

	if input.InternalData != nil {
		if err := h.validateDataDepth("internalData", input.InternalData, 0); err != nil {
			return err
		}
		if len(input.InternalData) > 100 {
			return appErrs.NewArrayTooLargeError("internalData", 100, len(input.InternalData))
		}
	}

	if err := h.validateWebData(&input.WebData); err != nil {
		return err
	}

	if err := h.validateIntent(&input.Intent); err != nil {
		return err
	}

	return nil
}

func (h *Handler) validateDataDepth(fieldName string, data map[string]interface{}, depth int) error {
	if depth > 10 {
		return appErrs.NewValidationError(fieldName, "object nesting too deep (max 10 levels)")
	}

	for key, value := range data {
		if len(key) > 100 {
			return appErrs.NewValidationError(fieldName+"."+key, "key too long (max 100 chars)")
		}

		lowerKey := strings.ToLower(key)
		sqlPatterns := []string{"' OR '1'='1", "'; DROP TABLE", "UNION SELECT"}
		for _, pattern := range sqlPatterns {
			if strings.Contains(lowerKey, strings.ToLower(pattern)) {
				return appErrs.NewValidationError(fieldName+"."+key, "key contains unsafe SQL patterns")
			}
		}

		switch v := value.(type) {
		case string:
			if len(v) > 5000 {
				return appErrs.NewValidationError(fieldName+"."+key,
					"string value too long (max 5000 chars)")
			}

			lowerValue := strings.ToLower(v)
			if strings.Contains(lowerValue, "<script") ||
				strings.Contains(lowerValue, "javascript:") ||
				strings.Contains(lowerValue, "data:text/html") {
				return appErrs.NewValidationError(fieldName+"."+key,
					"value contains potentially unsafe content")
			}

		case []interface{}:
			if len(v) > 100 {
				return appErrs.NewArrayTooLargeError(fieldName+"."+key, 100, len(v))
			}

		case map[string]interface{}:
			if err := h.validateDataDepth(fieldName+"."+key, v, depth+1); err != nil {
				return err
			}

		case float64, int, bool:
			continue

		default:
			return appErrs.NewValidationError(fieldName+"."+key,
				fmt.Sprintf("unsupported type: %T", v))
		}
	}

	return nil
}

func (h *Handler) validateWebData(webData *WebData) error {
	if len(webData.Sources) > 50 {
		return appErrs.NewArrayTooLargeError("webData.sources", 50, len(webData.Sources))
	}

	for i, source := range webData.Sources {
		if source.URL != "" && len(source.URL) > 500 {
			return appErrs.NewValidationError(fmt.Sprintf("webData.sources[%d].url", i),
				"URL too long (max 500 chars)")
		}

		if source.Title != "" && len(source.Title) > 200 {
			return appErrs.NewValidationError(fmt.Sprintf("webData.sources[%d].title", i),
				"title too long (max 200 chars)")
		}

		if source.URL != "" {
			lowerURL := strings.ToLower(source.URL)
			dangerousPatterns := []string{
				"javascript:", "data:", "file://",
				"localhost", "127.0.0.1", "192.168.", "10.", "172.16.",
			}

			for _, pattern := range dangerousPatterns {
				if strings.Contains(lowerURL, pattern) {
					return appErrs.NewValidationError(fmt.Sprintf("webData.sources[%d].url", i),
						"URL contains potentially unsafe content")
				}
			}
		}
	}

	if webData.Summary != "" && len(webData.Summary) > 5000 {
		return appErrs.NewValidationError("webData.summary",
			"summary too long (max 5000 chars)")
	}

	return nil
}

func (h *Handler) validateIntent(intent *Intent) error {
	if intent.PrimaryIntent != "" {
		if len(intent.PrimaryIntent) > 100 {
			return appErrs.NewValidationError("intent.primaryIntent",
				"primary intent too long (max 100 chars)")
		}

		allowedIntents := []string{
			"search_franchise", "compare_franchise", "get_details",
			"calculate_investment", "check_eligibility", "general_inquiry",
		}

		found := false
		for _, allowed := range allowedIntents {
			if intent.PrimaryIntent == allowed {
				found = true
				break
			}
		}
		if !found {
			return appErrs.NewValidationError("intent.primaryIntent",
				fmt.Sprintf("must be one of: %v", allowedIntents))
		}
	}

	if intent.Confidence < 0.0 || intent.Confidence > 1.0 {
		return appErrs.NewValidationError("intent.confidence",
			"must be between 0.0 and 1.0")
	}

	return nil
}

// ✅ IMPROVED: Better retry logic with jitter and 4xx handling
func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	// Sanitize input
	input.Question = h.sanitizer.SanitizeString(input.Question)

	if input.Intent.PrimaryIntent != "" {
		input.Intent.PrimaryIntent = h.sanitizer.SanitizeString(input.Intent.PrimaryIntent)
	}

	for i := range input.WebData.Sources {
		if input.WebData.Sources[i].URL != "" {
			input.WebData.Sources[i].URL = h.sanitizer.SanitizeString(input.WebData.Sources[i].URL)
		}
		if input.WebData.Sources[i].Title != "" {
			input.WebData.Sources[i].Title = h.sanitizer.SanitizeString(input.WebData.Sources[i].Title)
		}
	}

	if input.WebData.Summary != "" {
		input.WebData.Summary = h.sanitizer.SanitizeString(input.WebData.Summary)
	}

	if input.InternalData != nil {
		input.InternalData = h.sanitizer.SanitizeInput(input.InternalData)
	}

	// ✅ IMPROVED: Build prompt with better truncation
	prompt := h.buildPrompt(input)

	// ✅ NEW: Add request ID
	requestID := uuid.New().String()
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("request.id", requestID))

	requestBody := map[string]interface{}{
		"prompt": prompt,
		"context": map[string]interface{}{
			"internal": input.InternalData,
			"external": input.WebData,
			"intent":   input.Intent,
		},
		"max_tokens":  h.config.MaxTokens,
		"temperature": h.config.Temperature,
	}

	body, _ := json.Marshal(requestBody)
	req, err := http.NewRequestWithContext(ctx, "POST", h.config.GenAIBaseURL+"/api/ai/generate", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLLMSynthesisFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", requestID) // ✅ NEW

	var resp *http.Response
	var lastErr error

	for attempt := 0; attempt <= h.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// ✅ IMPROVED: Add jitter to prevent thundering herd
			backoff := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
			jitter := time.Duration(rand.Intn(100)) * time.Millisecond

			select {
			case <-time.After(backoff + jitter):
			case <-ctx.Done():
				return nil, ErrLLMTimeout
			}
		}

		resp, lastErr = h.doRequest(req)

		if lastErr == nil && resp != nil {
			if resp.StatusCode == http.StatusOK {
				break // Success!
			}

			// ✅ IMPROVED: Don't retry 4xx client errors
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				resp.Body.Close()
				return nil, fmt.Errorf("%w: client error %d", ErrLLMClientError, resp.StatusCode)
			}

			// Retry 5xx server errors
			resp.Body.Close()
			lastErr = fmt.Errorf("server error %d", resp.StatusCode)
			resp = nil
			continue
		}

		// Check for timeout
		if ctx.Err() != nil {
			return nil, ErrLLMTimeout
		}
	}

	if lastErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrLLMTimeout
		}
		return nil, fmt.Errorf("%w: %v", ErrLLMSynthesisFailed, lastErr)
	}

	if resp == nil {
		return nil, fmt.Errorf("%w: no successful response after retries", ErrLLMSynthesisFailed)
	}
	defer resp.Body.Close()

	var apiResponse struct {
		Text       string   `json:"text"`
		Confidence float64  `json:"confidence"`
		Sources    []string `json:"sources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("%w: decode error: %v", ErrLLMSynthesisFailed, err)
	}

	if strings.TrimSpace(apiResponse.Text) == "" {
		apiResponse.Text = "I don't have enough information to answer that question."
		apiResponse.Confidence = 0.1
	}

	if apiResponse.Confidence < 0.0 || apiResponse.Confidence > 1.0 {
		apiResponse.Confidence = 0.5
	}

	apiResponse.Text = h.sanitizer.SanitizeString(apiResponse.Text)
	for i := range apiResponse.Sources {
		apiResponse.Sources[i] = h.sanitizer.SanitizeString(apiResponse.Sources[i])
	}

	h.logger.Info("LLM synthesis completed", map[string]interface{}{
		"confidence":   apiResponse.Confidence,
		"sourceCount":  len(apiResponse.Sources),
		"circuitState": h.cb.State(),
		"requestId":    requestID,
		"traceId":      span.SpanContext().TraceID().String(),
	})

	return &Output{
		LLMResponse: apiResponse.Text,
		Confidence:  apiResponse.Confidence,
		Sources:     apiResponse.Sources,
	}, nil
}

// ✅ IMPROVED: Better prompt building with sentence-aware truncation
func (h *Handler) buildPrompt(input *Input) string {
	var parts []string

	parts = append(parts, "You are a helpful franchise advisor. Answer the user's question based ONLY on the provided data.")
	parts = append(parts, fmt.Sprintf("\nUser Question: %s", input.Question))

	if len(input.InternalData) > 0 {
		internalJSON, _ := json.MarshalIndent(input.InternalData, "", "  ")
		parts = append(parts, "\nInternal Franchise Data:")
		parts = append(parts, string(internalJSON))
	}

	if len(input.WebData.Sources) > 0 {
		parts = append(parts, "\nExternal Web Sources:")
		for _, src := range input.WebData.Sources {
			parts = append(parts, fmt.Sprintf("- %s: %s", src.Title, src.URL))
		}
		if input.WebData.Summary != "" {
			parts = append(parts, fmt.Sprintf("Summary: %s", input.WebData.Summary))
		}
	}

	parts = append(parts, "\nInstructions:")
	parts = append(parts, "- Cite sources when using external information")
	parts = append(parts, "- If data is insufficient, say so clearly")
	parts = append(parts, "- Keep response concise and professional")
	parts = append(parts, "- Return confidence score between 0.0 and 1.0")
	parts = append(parts, "\nAnswer:")

	prompt := strings.Join(parts, "\n")

	// ✅ IMPROVED: Truncate at sentence boundary
	if len(prompt) > MaxPromptLength {
		h.logger.Info("Prompt too long, truncating", map[string]interface{}{
			"originalLength": len(prompt),
		})

		truncated := prompt[:MaxPromptLength]

		// Find last sentence boundary
		lastPeriod := strings.LastIndexAny(truncated, ".!?")
		if lastPeriod > MinPromptLengthForSentenceTruncation {
			truncated = truncated[:lastPeriod+1]
		}

		prompt = truncated + "\n[Content truncated due to length]"
	}

	return prompt
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
			"traceId": span.SpanContext().TraceID().String(),
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
			"traceId": span.SpanContext().TraceID().String(),
		})
	}
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
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

