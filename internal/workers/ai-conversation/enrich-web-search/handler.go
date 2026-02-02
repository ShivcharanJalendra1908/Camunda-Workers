package enrichwebsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	"camunda-workers/internal/common/circuitbreaker"
	cerrors "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/validation"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "enrich-web-search"
)

var (
	ErrWebSearchTimeout = errors.New("WEB_SEARCH_TIMEOUT")
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
	errorHandler *cerrors.ErrorHandler
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
		cb = opts.CBManager.GetOrCreate("web-search", circuitbreaker.Config{
			Name:             "web-search",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
		})
	} else {
		cb = circuitbreaker.New(circuitbreaker.Config{
			Name:             "web-search",
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
		errorHandler: cerrors.NewErrorHandler(opts.Logger),
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

	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "enrich-web-search.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, cerrors.NewInvalidFilterFormatError(err.Error()))
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== VALIDATION =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "enrich-web-search.validateInput")
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

	ctxExec2, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "enrich-web-search.Execute")

	// Execute with circuit breaker
	res, err := h.cb.Execute(func() (interface{}, error) {
		return h.execute(ctxExec2, &input)
	})
	spanExec.End()

	if err != nil {
		if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("circuit_breaker.open", true))
			h.logger.Warn("Circuit breaker open for web search, using fallback", map[string]interface{}{
				"circuitState": h.cb.State(),
				"question":     input.Question,
				"traceId":      traceID,
			})

			// Add circuit breaker metrics to fallback
			circuitMetrics := h.cb.Metrics()

			// Fallback: Return empty results with circuit breaker info
			fallbackOutput := &Output{
				WebData: WebData{
					Sources: []Source{},
					Summary: "",
				},
				CircuitBreaker: map[string]interface{}{
					"state":            circuitMetrics["state"],
					"failureCount":     circuitMetrics["failureCount"],
					"successCount":     circuitMetrics["successCount"],
					"concurrentCalls":  circuitMetrics["concurrentCalls"],
					"requestLatencyMs": circuitMetrics["requestLatencyMs"],
					"fallbackUsed":     true,
				},
			}
			_, spanCompFb := otel.Tracer("worker-manager").Start(ctx, "enrich-web-search.completeJob.fallback")
			h.completeJob(ctx, client, job, fallbackOutput)
			spanCompFb.End()
			return
		}

		if errors.Is(err, ErrWebSearchTimeout) {
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("timeout", true))
			h.errorHandler.HandleJobError(ctx, client, job, cerrors.NewWebSearchTimeoutError())
		} else {
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("error", true))
			h.logger.Warn("web search failed, returning empty results", map[string]interface{}{
				"error":   err.Error(),
				"traceId": traceID,
			})

			// Get circuit breaker metrics for error case
			circuitMetrics := h.cb.Metrics()

			output := &Output{
				WebData: WebData{
					Sources: []Source{},
					Summary: "",
				},
				CircuitBreaker: map[string]interface{}{
					"state":            circuitMetrics["state"],
					"failureCount":     circuitMetrics["failureCount"],
					"successCount":     circuitMetrics["successCount"],
					"concurrentCalls":  circuitMetrics["concurrentCalls"],
					"requestLatencyMs": circuitMetrics["requestLatencyMs"],
					"fallbackUsed":     false,
					"error":            err.Error(),
				},
			}
			_, spanCompFb := otel.Tracer("worker-manager").Start(ctx, "enrich-web-search.completeJob.fallback")
			h.completeJob(ctx, client, job, output)
			spanCompFb.End()
		}
		return
	}

	// ===== COMPLETE JOB (with circuit breaker metrics) =====
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "enrich-web-search.completeJob")

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
	if input.Question == "" {
		return cerrors.NewValidationError("question", "question is required")
	}

	if len(input.Question) < 3 || len(input.Question) > 500 {
		return cerrors.NewValidationError("question", "question must be 3-500 characters")
	}

	// Check for SQL injection patterns
	sqlPatterns := []string{
		"' OR '1'='1", "' OR '1'='1' --", "'; DROP TABLE", "UNION SELECT",
		"INSERT INTO", "UPDATE", "DELETE FROM", "EXEC", "EXECUTE",
	}

	lowerQuestion := strings.ToLower(input.Question)
	for _, pattern := range sqlPatterns {
		if strings.Contains(lowerQuestion, strings.ToLower(pattern)) {
			return cerrors.NewValidationError("question", "contains potentially unsafe SQL patterns")
		}
	}

	// Check for NoSQL injection patterns
	nosqlPatterns := []string{
		"$where", "$ne", "$gt", "$regex", "script:", "javascript:",
		"onload=", "onerror=", "eval(", "function(",
	}

	for _, pattern := range nosqlPatterns {
		if strings.Contains(lowerQuestion, pattern) {
			return cerrors.NewValidationError("question", "contains potentially unsafe NoSQL patterns")
		}
	}

	// Check for script injection
	if strings.Contains(lowerQuestion, "javascript:") ||
		strings.Contains(lowerQuestion, "script:") ||
		strings.Contains(lowerQuestion, "data:") {
		return cerrors.NewValidationError("question", "contains potentially unsafe content")
	}

	// Validate Entities array size
	if len(input.Entities) > 50 {
		return cerrors.NewArrayTooLargeError("entities", 50, len(input.Entities))
	}

	// Validate each entity
	for i, entity := range input.Entities {
		// Validate entity type
		if entity.Type == "" {
			return cerrors.NewValidationError(fmt.Sprintf("entities[%d].type", i), "entity type is required")
		}

		if len(entity.Type) > 50 {
			return cerrors.NewValidationError(fmt.Sprintf("entities[%d].type", i),
				"entity type must be max 50 characters")
		}

		// Validate entity value
		if entity.Value == "" {
			return cerrors.NewValidationError(fmt.Sprintf("entities[%d].value", i), "entity value is required")
		}

		if len(entity.Value) > 200 {
			return cerrors.NewValidationError(fmt.Sprintf("entities[%d].value", i),
				"entity value must be max 200 characters")
		}

		// Check for SQL injection in entity value
		for _, pattern := range sqlPatterns {
			if strings.Contains(strings.ToLower(entity.Value), strings.ToLower(pattern)) {
				return cerrors.NewValidationError(fmt.Sprintf("entities[%d].value", i),
					"contains potentially unsafe SQL patterns")
			}
		}

		// Validate entity type is from allowed list
		allowedTypes := []string{"franchise_name", "location", "category", "industry"}
		found := false
		for _, allowed := range allowedTypes {
			if entity.Type == allowed {
				found = true
				break
			}
		}
		if !found {
			return cerrors.NewValidationError(fmt.Sprintf("entities[%d].type", i),
				fmt.Sprintf("must be one of: %v", allowedTypes))
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
			return nil, fmt.Errorf("search API server error %d: %s", resp.StatusCode, string(body))
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
	for i := range input.Entities {
		input.Entities[i].Type = h.sanitizer.SanitizeString(input.Entities[i].Type)
		input.Entities[i].Value = h.sanitizer.SanitizeString(input.Entities[i].Value)
	}

	query := h.buildQuery(input.Question, input.Entities)

	// 🔒 Validate final query length
	if len(query) > 2000 {
		h.logger.Warn("Search query too long, truncating", map[string]interface{}{
			"originalLength": len(query),
			"truncatedTo":    2000,
		})
		query = query[:2000]
	}

	searchURL := h.buildSearchURL(query)

	// 🔒 Validate URL doesn't point to internal IPs
	if err := h.validateURL(searchURL); err != nil {
		h.logger.Warn("Invalid URL detected", map[string]interface{}{
			"url":   searchURL,
			"error": err.Error(),
		})
		// Continue with search but log warning
	}

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	// Execute via circuit breaker doRequest
	resp, err := h.doRequest(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded ||
			strings.Contains(err.Error(), "timeout") ||
			strings.Contains(err.Error(), "deadline") ||
			strings.Contains(err.Error(), "Client.Timeout") {
			return nil, ErrWebSearchTimeout
		}
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search API returned %d", resp.StatusCode)
	}

	var apiResponse struct {
		Items []struct {
			Link    string `json:"link"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
			Mime    string `json:"mime"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, err
	}

	// Convert apiResponse.Items to the expected type for processResults
	items := make([]struct {
		Link    string
		Title   string
		Snippet string
		Mime    string
	}, len(apiResponse.Items))

	for i, item := range apiResponse.Items {
		items[i] = struct {
			Link    string
			Title   string
			Snippet string
			Mime    string
		}{
			Link:    item.Link,
			Title:   item.Title,
			Snippet: item.Snippet,
			Mime:    item.Mime,
		}
	}

	sources := h.processResults(items)
	summary := h.generateSummary(sources)

	h.logger.Info("web search completed", map[string]interface{}{
		"query":       query,
		"resultCount": len(sources),
	})

	return &Output{
		WebData: WebData{
			Sources: sources,
			Summary: summary,
		},
	}, nil
}

func (h *Handler) buildQuery(question string, entities []Entity) string {
	query := question

	// Add entity values
	for _, entity := range entities {
		if entity.Type == "franchise_name" || entity.Type == "location" || entity.Type == "category" {
			query += " " + entity.Value
		}
	}

	// Clean and deduplicate
	query = regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(query), " ")
	return query
}

func (h *Handler) buildSearchURL(query string) string {
	baseURL, _ := url.Parse(h.config.SearchAPIBaseURL)
	params := url.Values{}
	params.Add("key", h.config.SearchAPIKey)
	params.Add("cx", h.config.SearchEngineID)
	params.Add("q", query)
	params.Add("num", fmt.Sprintf("%d", h.config.MaxResults))
	baseURL.RawQuery = params.Encode()
	return baseURL.String()
}

func (h *Handler) processResults(items []struct {
	Link    string
	Title   string
	Snippet string
	Mime    string
}) []Source {
	seen := make(map[string]bool)
	var sources []Source

	for _, item := range items {
		// Skip non-HTML
		if item.Mime != "" && !strings.Contains(item.Mime, "html") {
			continue
		}

		// Dedupe by URL
		if seen[item.Link] {
			continue
		}
		seen[item.Link] = true

		// Calculate relevance (simplified)
		relevance := 1.0
		if strings.Contains(item.Link, ".gov") || strings.Contains(item.Link, ".edu") {
			relevance += 0.2
		}
		if strings.Contains(strings.ToLower(item.Title), "official") {
			relevance += 0.1
		}

		if relevance >= h.config.MinRelevance {
			sources = append(sources, Source{
				URL:       item.Link,
				Title:     item.Title,
				Snippet:   item.Snippet,
				Relevance: relevance,
			})
		}
	}

	// Sort by relevance
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].Relevance > sources[j].Relevance
	})

	// Limit results
	if len(sources) > h.config.MaxResults {
		sources = sources[:h.config.MaxResults]
	}

	return sources
}

func (h *Handler) generateSummary(sources []Source) string {
	if len(sources) == 0 {
		return ""
	}
	// In real system, use LLM or summarization model
	// For now, return first snippet
	return sources[0].Snippet
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)

	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("Failed to complete job", map[string]interface{}{
			"jobKey":  job.Key,
			"error":   err.Error(),
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}

	if _, sendErr := cmd.Send(ctx); sendErr != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(sendErr)
		h.logger.Error("Failed to send complete job", map[string]interface{}{
			"jobKey":  job.Key,
			"error":   sendErr.Error(),
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

// 🔒 ADDITIONAL SECURITY: URL Validation
func (h *Handler) validateURL(urlStr string) error {
	// Parse URL to validate structure
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Ensure it's a proper HTTP/HTTPS URL
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL must use http or https protocol")
	}

	// Check for potentially malicious hosts (SSRF prevention)
	maliciousHosts := []string{
		"localhost", "127.0.0.1", "0.0.0.0", "::1",
		"169.254.", "10.", "172.16.", "192.168.",
	}

	for _, host := range maliciousHosts {
		if strings.HasPrefix(parsedURL.Host, host) {
			return fmt.Errorf("URL points to internal/reserved IP range")
		}
	}

	return nil
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

// // internal/workers/ai-conversation/enrich-web-search/handler.go
// package enrichwebsearch

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"net/http"
// 	"net/url"
// 	"regexp"
// 	"sort"
// 	"strings"
// 	"time"

// 	"camunda-workers/internal/common/circuitbreaker"
// 	cerrors "camunda-workers/internal/common/errors"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

// 	"go.opentelemetry.io/otel"
// )

// const (
// 	TaskType = "enrich-web-search"
// )

// var (
// 	ErrWebSearchTimeout = errors.New("WEB_SEARCH_TIMEOUT")
// )

// // Logger interface definition
// type Logger interface {
// 	Info(msg string, fields map[string]interface{})
// 	Warn(msg string, fields map[string]interface{})
// 	Error(msg string, fields map[string]interface{})
// 	With(fields map[string]interface{}) Logger
// }

// type Handler struct {
// 	config       *Config
// 	client       *http.Client
// 	logger       Logger
// 	cb           *circuitbreaker.CircuitBreaker
// 	errorHandler *cerrors.ErrorHandler
// }

// func NewHandler(config *Config, log Logger) *Handler {
// 	return &Handler{
// 		config: config,
// 		client: &http.Client{
// 			Timeout: config.Timeout,
// 		},
// 		logger: log.With(map[string]interface{}{
// 			"taskType": TaskType,
// 		}),
// 		cb: circuitbreaker.New(circuitbreaker.Config{
// 			Name:             "web-search",
// 			FailureThreshold: 5,
// 			SuccessThreshold: 2,
// 			Timeout:          30 * time.Second,
// 		}),
// 		errorHandler: cerrors.NewErrorHandler(log),
// 	}
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	h.logger.Info("processing job", map[string]interface{}{
// 		"jobKey":      job.Key,
// 		"workflowKey": job.ProcessInstanceKey,
// 	})

// 	var input Input
// 	_, spanParse := otel.Tracer("worker-manager").Start(context.Background(), "enrich-web-search.parseInput")
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
// 		h.errorHandler.HandleJobError(context.Background(), client, job, cerrors.NewInvalidFilterFormatError(err.Error()))
// 		spanParse.End()
// 		return
// 	}
// 	spanParse.End()

// 	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
// 	defer cancel()

// 	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "enrich-web-search.Execute")
// 	output, err := h.execute(ctxExec, &input)
// 	spanExec.End()
// 	if err != nil {
// 		if errors.Is(err, ErrWebSearchTimeout) {
// 			h.errorHandler.HandleJobError(ctx, client, job, cerrors.NewWebSearchTimeoutError())
// 		} else {
// 			h.logger.Warn("web search failed, returning empty results", map[string]interface{}{
// 				"error": err.Error(),
// 			})
// 			output = &Output{WebData: WebData{Sources: []Source{}, Summary: ""}}
// 			_, spanCompFb := otel.Tracer("worker-manager").Start(ctx, "enrich-web-search.completeJob.fallback")
// 			h.completeJob(client, job, output)
// 			spanCompFb.End()
// 		}
// 		return
// 	}

// 	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "enrich-web-search.completeJob")
// 	h.completeJob(client, job, output)
// 	spanComp.End()
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	query := h.buildQuery(input.Question, input.Entities)
// 	searchURL := h.buildSearchURL(query)

// 	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
// 	if err != nil {
// 		return nil, err
// 	}

// 	// Execute via Circuit Breaker
// 	result, err := h.cb.Execute(func() (interface{}, error) {
// 		return h.client.Do(req)
// 	})
// 	if err != nil {
// 		if ctx.Err() == context.DeadlineExceeded ||
// 			strings.Contains(err.Error(), "timeout") ||
// 			strings.Contains(err.Error(), "deadline") ||
// 			strings.Contains(err.Error(), "Client.Timeout") {
// 			return nil, ErrWebSearchTimeout
// 		}
// 		return nil, err
// 	}

// 	resp := result.(*http.Response)
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK {
// 		return nil, fmt.Errorf("search API returned %d", resp.StatusCode)
// 	}

// 	var apiResponse struct {
// 		Items []struct {
// 			Link    string `json:"link"`
// 			Title   string `json:"title"`
// 			Snippet string `json:"snippet"`
// 			Mime    string `json:"mime"`
// 		} `json:"items"`
// 	}

// 	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
// 		return nil, err
// 	}

// 	// Convert apiResponse.Items to the expected type for processResults
// 	items := make([]struct {
// 		Link    string
// 		Title   string
// 		Snippet string
// 		Mime    string
// 	}, len(apiResponse.Items))

// 	for i, item := range apiResponse.Items {
// 		items[i] = struct {
// 			Link    string
// 			Title   string
// 			Snippet string
// 			Mime    string
// 		}{
// 			Link:    item.Link,
// 			Title:   item.Title,
// 			Snippet: item.Snippet,
// 			Mime:    item.Mime,
// 		}
// 	}

// 	sources := h.processResults(items)
// 	summary := h.generateSummary(sources)

// 	h.logger.Info("web search completed", map[string]interface{}{
// 		"query":       query,
// 		"resultCount": len(sources),
// 	})

// 	return &Output{
// 		WebData: WebData{
// 			Sources: sources,
// 			Summary: summary,
// 		},
// 	}, nil
// }

// func (h *Handler) buildQuery(question string, entities []Entity) string {
// 	query := question

// 	// Add entity values
// 	for _, entity := range entities {
// 		if entity.Type == "franchise_name" || entity.Type == "location" || entity.Type == "category" {
// 			query += " " + entity.Value
// 		}
// 	}

// 	// Clean and deduplicate
// 	query = regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(query), " ")
// 	return query
// }

// func (h *Handler) buildSearchURL(query string) string {
// 	baseURL, _ := url.Parse(h.config.SearchAPIBaseURL)
// 	params := url.Values{}
// 	params.Add("key", h.config.SearchAPIKey)
// 	params.Add("cx", h.config.SearchEngineID)
// 	params.Add("q", query)
// 	params.Add("num", fmt.Sprintf("%d", h.config.MaxResults))
// 	baseURL.RawQuery = params.Encode()
// 	return baseURL.String()
// }

// func (h *Handler) processResults(items []struct {
// 	Link    string
// 	Title   string
// 	Snippet string
// 	Mime    string
// }) []Source {
// 	seen := make(map[string]bool)
// 	var sources []Source

// 	for _, item := range items {
// 		// Skip non-HTML
// 		if item.Mime != "" && !strings.Contains(item.Mime, "html") {
// 			continue
// 		}

// 		// Dedupe by URL
// 		if seen[item.Link] {
// 			continue
// 		}
// 		seen[item.Link] = true

// 		// Calculate relevance (simplified)
// 		relevance := 1.0
// 		if strings.Contains(item.Link, ".gov") || strings.Contains(item.Link, ".edu") {
// 			relevance += 0.2
// 		}
// 		if strings.Contains(strings.ToLower(item.Title), "official") {
// 			relevance += 0.1
// 		}

// 		if relevance >= h.config.MinRelevance {
// 			sources = append(sources, Source{
// 				URL:       item.Link,
// 				Title:     item.Title,
// 				Snippet:   item.Snippet,
// 				Relevance: relevance,
// 			})
// 		}
// 	}

// 	// Sort by relevance
// 	sort.Slice(sources, func(i, j int) bool {
// 		return sources[i].Relevance > sources[j].Relevance
// 	})

// 	// Limit results
// 	if len(sources) > h.config.MaxResults {
// 		sources = sources[:h.config.MaxResults]
// 	}

// 	return sources
// }

// func (h *Handler) generateSummary(sources []Source) string {
// 	if len(sources) == 0 {
// 		return ""
// 	}
// 	// In real system, use LLM or summarization model
// 	// For now, return first snippet
// 	return sources[0].Snippet
// }

// func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
// 	cmd, err := client.NewCompleteJobCommand().
// 		JobKey(job.Key).
// 		VariablesFromObject(output)

// 	if err != nil {
// 		h.logger.Error("Failed to complete job", map[string]interface{}{
// 			"jobKey": job.Key,
// 			"error":  err.Error(),
// 		})
// 	}

// 	if _, sendErr := cmd.Send(context.Background()); sendErr != nil {
// 		h.logger.Error("Failed to send complete job", map[string]interface{}{
// 			"jobKey": job.Key,
// 			"error":  sendErr.Error(),
// 		})
// 	}
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }
