// FILE: internal/workers/data-access/query-elasticsearch/handler.go
package queryelasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/google/uuid"

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"
	"camunda-workers/internal/models"
	"camunda-workers/internal/workers/data-access/query-elasticsearch/queries"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "query-elasticsearch"
)

var (
	ErrElasticsearchConnectionFailed = errors.New("ELASTICSEARCH_CONNECTION_FAILED")
	ErrSearchQueryFailed             = errors.New("SEARCH_QUERY_FAILED")
	ErrSearchTimeout                 = errors.New("SEARCH_TIMEOUT")
	ErrIndexNotFound                 = errors.New("INDEX_NOT_FOUND")
	ErrInvalidInputFormat            = errors.New("INVALID_INPUT_FORMAT")
	ErrFranchiseNotFound             = errors.New("FRANCHISE_NOT_FOUND") // ✅ NEW
)

type Handler struct {
	config       *Config
	client       *elasticsearch.Client
	logger       logger.Logger
	errorHandler *appErrs.ErrorHandler
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
}

func NewHandler(config *Config, client *elasticsearch.Client, log logger.Logger) *Handler {
	return &Handler{
		config:       config,
		client:       client,
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: appErrs.NewErrorHandler(log),
		validator:    validation.NewValidator(),
		sanitizer:    validation.NewSanitizer(),
	}
}

// Handle implements the worker job handler interface
func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	ctx := context.Background()

	// Extract trace context
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

	// Create worker span
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

	// Parse input
	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "query-elasticsearch.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewBusinessRuleError("Parse input failed", fmt.Sprintf("%v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	// Sanitize input first
	h.sanitizeInput(&input)

	// Comprehensive validation
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// ✅ CRITICAL: Use timeout from config with proper context
	ctxExec, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()

	// Execute query
	startTime := time.Now()
	_, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "query-elasticsearch.Execute")
	output, err := h.execute(ctxExec, &input)
	spanExec.End()

	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))

		// ✅ IMPROVED: Better error handling with detailed logging
		h.logger.Error("query execution failed", map[string]interface{}{
			"error":     err.Error(),
			"queryType": input.QueryType,
			"slug":      h.extractSlugForLogging(&input),
			"traceId":   traceID,
		})

		var stdErr *appErrs.StandardError
		switch {
		case errors.Is(err, ErrSearchTimeout):
			span.SetAttributes(attribute.Bool("timeout", true))
			stdErr = appErrs.NewSearchTimeoutError(string(input.QueryType))
		case errors.Is(err, ErrFranchiseNotFound): // ✅ NEW: Handle not found gracefully
			span.SetAttributes(attribute.String("error.type", "franchise_not_found"))
			// Return empty result instead of failing
			output = &Output{
				Success:   true,
				Data:      []map[string]interface{}{},
				TotalHits: 0,
				MaxScore:  0,
				Took:      time.Since(startTime).Milliseconds(),
				QueryType: string(input.QueryType),
				Message:   "Franchise not found",
			}
			h.completeJob(ctx, client, job, output)
			return
		case errors.Is(err, ErrIndexNotFound):
			span.SetAttributes(attribute.String("error.type", "index_not_found"))
			stdErr = appErrs.NewIndexNotFoundError(input.IndexName)
		case errors.Is(err, ErrSearchQueryFailed):
			span.SetAttributes(attribute.String("error.type", "search_query_failed"))
			stdErr = appErrs.NewSearchQueryFailedError(string(input.QueryType), err)
		case errors.Is(err, ErrElasticsearchConnectionFailed):
			span.SetAttributes(attribute.String("error.type", "connection_failed"))
			stdErr = appErrs.NewElasticsearchConnectionFailedError(err)
		case errors.Is(err, ErrInvalidInputFormat):
			span.SetAttributes(attribute.String("error.type", "invalid_input"))
			stdErr = appErrs.NewValidationError("input", err.Error())
		default:
			span.SetAttributes(attribute.String("error.type", "external_service_error"))
			stdErr = appErrs.NewExternalServiceError("query-elasticsearch", err)
		}

		h.errorHandler.HandleJobError(ctx, client, job, stdErr)
		return
	}

	// Add execution time
	output.Took = time.Since(startTime).Milliseconds()
	output.Success = true
	if output.Message == "" {
		output.Message = "Query executed successfully"
	}

	h.completeJob(ctx, client, job, output)
}

// ✅ NEW: Helper to extract slug for logging
func (h *Handler) extractSlugForLogging(input *Input) string {
	if input.FranchiseID != "" {
		return input.FranchiseID
	}
	if input.Params != nil {
		if slug, ok := input.Params["slug"].(string); ok {
			return slug
		}
	}
	if input.Filters != nil {
		if slug, ok := input.Filters["slug"].(string); ok {
			return slug
		}
	}
	return ""
}

// ===== INPUT SANITIZATION =====
func (h *Handler) sanitizeInput(input *Input) {
	if input.QueryType != "" {
		input.QueryType = models.QueryType(strings.TrimSpace(string(input.QueryType)))
	}
	if input.IndexName != "" {
		input.IndexName = strings.TrimSpace(input.IndexName)
	}
	if input.FranchiseID != "" {
		input.FranchiseID = strings.TrimSpace(input.FranchiseID)
	}
	if input.Category != "" {
		input.Category = strings.TrimSpace(input.Category)
	}
	if input.CategorySlug != "" {
		input.CategorySlug = strings.TrimSpace(input.CategorySlug)
	}
	if input.SubCategorySlug != "" {
		input.SubCategorySlug = strings.TrimSpace(input.SubCategorySlug)
	}
	if input.Params != nil {
		if subCat, ok := input.Params["subCategorySlug"].(string); ok {
			input.Params["subCategorySlug"] = strings.TrimSpace(subCat)
		}
	}
	if input.Page > 0 && input.Pagination.Page == 0 {
		input.Pagination.Page = input.Page
	}
	if input.PageSize > 0 && input.Pagination.Size == 0 {
		input.Pagination.Size = input.PageSize
	}
	if input.Offset > 0 && input.Pagination.From == 0 {
		input.Pagination.From = input.Offset
	}
	if input.Pagination.From == 0 && input.Pagination.Page > 0 && input.Pagination.Size > 0 {
		input.Pagination.From = (input.Pagination.Page - 1) * input.Pagination.Size
	}
}

// ===== COMPREHENSIVE VALIDATION =====

func (h *Handler) validateInput(input *Input) error {
	// Validate that we have at least one valid query format
	if input.QueryType == "" && (input.IndexName == "" || input.Query == nil) {
		return appErrs.NewValidationError("",
			"Either queryType or (indexName + query) must be provided")
	}

	// Validate QueryType if provided (NEW format)
	if input.QueryType != "" {
		if err := h.validateQueryType(input.QueryType); err != nil {
			return err
		}

		// ✅ CRITICAL: Validate query-specific requirements
		if err := h.validateQueryTypeRequirements(input); err != nil {
			return err
		}
	}

	// Validate IndexName if provided (OLD format)
	if input.IndexName != "" {
		if err := ozzo.Validate(input.IndexName,
			ozzo.Required.Error("indexName is required when using raw query"),
			validation.ValidateStringLength(1, 100),
			validation.SafeNoSQLString,
			validation.SafeDatabaseIdentifier,
		); err != nil {
			return appErrs.NewValidationError("indexName", err.Error())
		}
	}

	// Validate FranchiseID (can be UUID or slug)
	if input.FranchiseID != "" {
		// ✅ CHANGED: Allow both UUID and slug format
		if _, err := uuid.Parse(input.FranchiseID); err != nil {
			// Not a UUID, validate as slug
			if err := ozzo.Validate(input.FranchiseID,
				validation.ValidateStringLength(1, 100),
				validation.SafeNoSQLString,
			); err != nil {
				return appErrs.NewValidationError("franchiseId", "must be a valid UUID or slug")
			}
		}
	}

	// Validate Category
	if input.Category != "" {
		if err := ozzo.Validate(input.Category,
			validation.ValidateStringLength(1, 100),
			validation.SafeNoSQLString,
		); err != nil {
			return appErrs.NewValidationError("category", err.Error())
		}
	}

	// Validate CategorySlug
	if input.CategorySlug != "" {
		if err := ozzo.Validate(input.CategorySlug,
			validation.ValidateStringLength(1, 100),
			validation.SafeNoSQLString,
		); err != nil {
			return appErrs.NewValidationError("categorySlug", err.Error())
		}
	}

	// Validate SubCategorySlug
	if input.SubCategorySlug != "" {
		if err := ozzo.Validate(input.SubCategorySlug,
			validation.ValidateStringLength(1, 100),
			validation.SafeNoSQLString,
		); err != nil {
			return appErrs.NewValidationError("subCategorySlug", err.Error())
		}
	}

	// Validate Filters
	if len(input.Filters) > 0 {
		if err := h.validateFilters(input.Filters); err != nil {
			return err
		}
	}

	// Validate Query (for raw queries)
	if input.Query != nil {
		if err := h.validateQuery(input.Query); err != nil {
			return err
		}
	}

	// Validate Pagination
	if err := h.validatePagination(&input.Pagination); err != nil {
		return err
	}

	// Validate Params (for registry queries)
	if input.Params != nil {
		if err := h.validateParams(input.Params); err != nil {
			return err
		}
	}

	return nil
}

// ✅ IMPROVED: Better query-specific validation
// internal/workers/data-access/query-elasticsearch/handler.go
// Lines 320-380: REPLACE ENTIRE validateQueryTypeRequirements function

func (h *Handler) validateQueryTypeRequirements(input *Input) error {
	switch input.QueryType {
	case models.ESQueryTypeFranchiseBySlug:
		// ✅ CRITICAL FIX: Extract slug AND set it (not just validate)
		slug := ""

		// Priority 1: franchiseId
		if input.FranchiseID != "" {
			slug = input.FranchiseID
		}

		// Priority 2: params.slug or params.franchiseId
		if slug == "" && input.Params != nil {
			if s, ok := input.Params["slug"].(string); ok && s != "" {
				slug = s
			} else if s, ok := input.Params["franchiseId"].(string); ok && s != "" {
				slug = s
			}
		}

		// Priority 3: filters.slug
		if slug == "" && input.Filters != nil {
			if s, ok := input.Filters["slug"].(string); ok && s != "" {
				slug = s
			}
		}

		if slug == "" {
			h.logger.Warn("FRANCHISE_BY_SLUG missing slug", map[string]interface{}{
				"franchiseId": input.FranchiseID,
				"params":      input.Params,
				"filters":     input.Filters,
			})
			return appErrs.NewRequiredFieldError("slug (in franchiseId, params.slug, or filters.slug)")
		}

		// ✅ CRITICAL: Set in params for query execution
		if input.Params == nil {
			input.Params = make(map[string]interface{})
		}
		input.Params["slug"] = slug

	case models.ESQueryTypeIndustryBySlug:
		// ✅ Similar fix for industry
		slug := ""

		if input.Params != nil {
			if s, ok := input.Params["industrySlug"].(string); ok && s != "" {
				slug = s
			} else if s, ok := input.Params["slug"].(string); ok && s != "" {
				slug = s
			}
		}

		if slug == "" && input.Filters != nil {
			if s, ok := input.Filters["industrySlug"].(string); ok && s != "" {
				slug = s
			} else if s, ok := input.Filters["category"].(string); ok && s != "" {
				slug = s
			}
		}

		if slug == "" {
			return appErrs.NewRequiredFieldError("industrySlug or slug")
		}

		if input.Params == nil {
			input.Params = make(map[string]interface{})
		}
		input.Params["industrySlug"] = slug
		input.Params["slug"] = slug

	case models.ESQueryTypeRecommendedByIndustry, models.ESQueryTypeMarketInsights:
		// Optional - set if available
		var slug string
		// if input.Params != nil {
		// 	if s, ok := input.Params["industrySlug"].(string); ok {
		// 		slug = s
		// 	}
		// }
		if input.IndustrySlug != "" {
                slug = input.IndustrySlug
        }
		
		if slug == "" && input.Filters != nil {
			if s, ok := input.Filters["category"].(string); ok {
				slug = s
			}
		}
		if slug != "" {
			if input.Params == nil {
				input.Params = make(map[string]interface{})
			}
			input.Params["industrySlug"] = slug
		}

	case models.ESQueryTypeSearchWithFilters, models.ESQueryTypeSearchWithAggregations:
		// if len(input.Filters) == 0 && input.Params == nil {
		// 	return appErrs.NewValidationError("filters", "search queries require filters or params")
		// }
	}

	return nil
}

func (h *Handler) validateQueryType(queryType models.QueryType) error {
	// Get all valid query types from registry
	validQueryTypes := make([]interface{}, 0, len(queries.Registry))
	for qt := range queries.Registry {
		validQueryTypes = append(validQueryTypes, string(qt))
	}

	if len(validQueryTypes) == 0 {
		h.logger.Warn("query registry is empty", map[string]interface{}{})
	}

	// ✅ Convert to string for validation
	if err := ozzo.Validate(string(queryType),
		ozzo.Required.Error("queryType is required when using registry"),
		validation.ValidateStringLength(3, 100),
		validation.SafeNoSQLString,
		ozzo.In(validQueryTypes...).Error("invalid query type"),
	); err != nil {
		return appErrs.NewValidationError("queryType", err.Error())
	}

	return nil
}

func (h *Handler) validateQuery(query map[string]interface{}) error {
	// Ensure no dangerous patterns in query
	queryJSON, _ := json.Marshal(query)
	queryStr := strings.ToLower(string(queryJSON))

	dangerousPatterns := []string{
		"\"script\":", "\"inline\":", "\"source\":",
		"\"function\":", "\"eval(\"", "\"exec(\"",
		"\"$where\":", "\"$ne\":", "\"$regex\":",
		"<script", "</script>", "javascript:",
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(queryStr, pattern) {
			h.logger.Error("dangerous pattern in query", map[string]interface{}{
				"pattern": pattern,
				"query":   string(queryJSON),
			})
			return appErrs.NewNoSQLInjectionError("query", pattern)
		}
	}

	return nil
}

func (h *Handler) validateFilters(filters map[string]interface{}) error {
	return h.validateFilterRecursive(filters, "", 0)
}

func (h *Handler) validateFilterRecursive(obj interface{}, path string, depth int) error {
	// Prevent deep nesting attacks
	if depth > 10 {
		return appErrs.NewValidationError(path, "object nesting too deep (max 10 levels)")
	}

	switch v := obj.(type) {
	case map[string]interface{}:
		// Check for dangerous keys
		for key := range v {
			lowerKey := strings.ToLower(key)

			// CRITICAL: Block script fields
			dangerousKeys := []string{"script", "inline", "function", "source", "eval", "exec"}
			for _, dk := range dangerousKeys {
				if strings.Contains(lowerKey, dk) {
					return appErrs.NewNoSQLInjectionError(path+"."+key, dk)
				}
			}

			// Validate key format
			if err := ozzo.Validate(key,
				validation.ValidateStringLength(1, 100),
				validation.SafeNoSQLString,
			); err != nil {
				return appErrs.NewValidationError(path+"."+key, err.Error())
			}

			// Recursively validate value
			nextPath := key
			if path != "" {
				nextPath = path + "." + key
			}
			if err := h.validateFilterRecursive(v[key], nextPath, depth+1); err != nil {
				return err
			}
		}

	case []interface{}:
		// Validate array size
		if len(v) > 1000 {
			return appErrs.NewArrayTooLargeError(path, 1000, len(v))
		}

		// Validate each item
		for i, item := range v {
			itemPath := fmt.Sprintf("%s[%d]", path, i)
			if err := h.validateFilterRecursive(item, itemPath, depth+1); err != nil {
				return err
			}
		}

	case string:
		// Validate string length
		if len(v) > 1000 {
			return appErrs.NewStringTooLongError(path, 1000, len(v))
		}

		// Check for dangerous patterns
		if err := ozzo.Validate(v,
			validation.SafeNoSQLString,
		); err != nil {
			return appErrs.NewValidationError(path, err.Error())
		}

	case float64:
		// Validate numeric range
		if v < -1e10 || v > 1e10 {
			return appErrs.NewValidationError(path, "number out of valid range (-1e10 to 1e10)")
		}

	case int, int64, bool:
		// These types are safe
		return nil

	case nil:
		// Ignore nil values (can happen when AI returns null for a field)
		return nil

	default:
		// Reject unexpected types
		return appErrs.NewValidationError(path, fmt.Sprintf("unsupported type: %T", v))
	}

	return nil
}

func (h *Handler) validatePagination(pagination *Pagination) error {
	// Apply defaults for missing values
	if pagination.Size <= 0 {
		pagination.Size = 10 // Default page size
	}
	if pagination.Size > 100 {
		pagination.Size = 100 // Cap at maximum
	}

	if pagination.From < 0 {
		pagination.From = 0
	}
	if pagination.From > 10000 {
		pagination.From = 10000
	}

	return nil
}

func (h *Handler) validateParams(params map[string]interface{}) error {
	return h.validateFilterRecursive(params, "params", 0)
}

// ===== EXECUTION LOGIC =====
func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	// ✅ Add timeout check at the start
	select {
	case <-ctx.Done():
		return nil, ErrSearchTimeout
	default:
	}

	// Determine execution mode
	if input.QueryType != "" {
		// NEW: Registry-based query
		return h.executeRegistryQuery(ctx, input)
	} else if input.IndexName != "" && input.Query != nil {
		// OLD: Raw query execution
		return h.executeRawQuery(ctx, input)
	}

	return nil, fmt.Errorf("%w: either queryType or (indexName + query) must be provided",
		ErrInvalidInputFormat)
}

func (h *Handler) executeRegistryQuery(ctx context.Context, input *Input) (*Output, error) {
	h.logger.Info("executing registry query", map[string]interface{}{
		"queryType": input.QueryType,
		"traceId":   trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
	})

	// Build params for registry query
	params := h.buildRegistryParams(input)

	// ✅ Log params for debugging
	h.logger.Debug("registry query params", map[string]interface{}{
		"params":    params,
		"queryType": input.QueryType,
	})

	// Validate generated parameters
	if err := h.validateGeneratedQuery(params); err != nil {
		return nil, err
	}

	// Execute registry query
	result, err := queries.Execute(ctx, h.client, input.QueryType, params)
	if err != nil {
		h.logger.Error("registry query execution failed", map[string]interface{}{
			"queryType": input.QueryType,
			"params":    params,
			"error":     err.Error(),
			"traceId":   trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})

		// ✅ Better error handling
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrSearchTimeout
		}

		// ✅ Handle "franchise not found" specially
		if strings.Contains(err.Error(), "franchise not found") {
			return nil, ErrFranchiseNotFound
		}

		if errors.Is(err, queries.ErrUnknownQueryType) {
			return nil, fmt.Errorf("%w: %v", ErrSearchQueryFailed, err)
		}
		if errors.Is(err, queries.ErrMissingIndex) {
			return nil, ErrIndexNotFound
		}
		return nil, fmt.Errorf("%w: %v", ErrSearchQueryFailed, err)
	}

	// ✅ Check if results are empty for specific queries
	if len(result.Data) == 0 && input.QueryType == models.ESQueryTypeFranchiseBySlug {
		slug := h.extractSlugForLogging(input)
		h.logger.Warn("franchise not found by slug", map[string]interface{}{
			"slug":      slug,
			"queryType": input.QueryType,
		})
		return nil, ErrFranchiseNotFound
	}

	return &Output{
		Success:   true,
		Data:      result.Data,
		TotalHits: result.TotalHits,
		MaxScore:  result.MaxScore,
		Took:      result.Took,
		QueryType: string(input.QueryType),
	}, nil
}

func (h *Handler) buildRegistryParams(input *Input) map[string]interface{} {
	params := make(map[string]interface{})

	// Copy input params if provided
	if input.Params != nil {
		for k, v := range input.Params {
			params[k] = v
		}
	}

	// ✅ ADD THIS - Extract from top-level Input fields
	if input.IndustrySlug != "" && params["industrySlug"] == nil {
		params["industrySlug"] = input.IndustrySlug
	}

	if input.CategorySlug != "" && params["categorySlug"] == nil {
		params["categorySlug"] = input.CategorySlug
	}

	if input.SubCategorySlug != "" && params["subCategorySlug"] == nil {
		params["subCategorySlug"] = input.SubCategorySlug
	}

	if input.EntityType != "" && params["entityType"] == nil {
		params["entityType"] = input.EntityType
	}

	if et, ok := params["entityType"].(string); ok && et != "" {
		et = strings.ToLower(et)
		switch et {
		case "franchises":
			et = "franchise"
		case "associations":
			et = "association"
		case "master-franchise", "master_franchises", "master franchises", "masterfranchise":
			et = "master_franchise"
		default:
			et = strings.TrimSuffix(et, "s")
			if et == "master-franchise" || et == "master franchise" || et == "masterfranchise" {
				et = "master_franchise"
			}
		}
		params["entityType"] = et
	}

	// ✅ CRITICAL: Extract slug from multiple possible sources
	slug := ""
	if input.FranchiseID != "" {
		slug = input.FranchiseID
	}
	if slug == "" && input.Params != nil {
		if s, ok := input.Params["slug"].(string); ok {
			slug = s
		}
	}
	if slug == "" && input.Category != "" {
		// For some queries, category might be the slug
		slug = input.Category
	}
	if slug == "" && input.Filters != nil {
		if filterSlug, ok := input.Filters["slug"].(string); ok {
			slug = filterSlug
		}
	}

	// ✅ Add slug if we found it
	if slug != "" && params["slug"] == nil {
		params["slug"] = slug
	}

	// Add common fields if not already in params
	if input.FranchiseID != "" && params["franchiseId"] == nil {
		params["franchiseId"] = input.FranchiseID
	}
	if input.Category != "" && params["category"] == nil {
		params["category"] = input.Category
	}
	if input.Category != "" && params["industrySlug"] == nil {
		params["industrySlug"] = input.Category
	}
	if len(input.Filters) > 0 && params["filters"] == nil {
		params["filters"] = input.Filters
	}

	// Add pagination if not already in params
	if input.Pagination.Size > 0 {
		if params["page"] == nil && params["from"] == nil {
			page := 1
			if input.Pagination.From > 0 {
				page = (input.Pagination.From / input.Pagination.Size) + 1
			}
			params["page"] = page
			params["limit"] = input.Pagination.Size
		}
	}

	if params["page"] == nil && input.Pagination.Page > 0 {
		params["page"] = input.Pagination.Page
	}
	if params["pageSize"] == nil && input.Pagination.Size > 0 {
		params["pageSize"] = input.Pagination.Size
	}
	if params["offset"] == nil && input.Pagination.From >= 0 {
		params["offset"] = input.Pagination.From
	}

	return params
}

func (h *Handler) executeRawQuery(ctx context.Context, input *Input) (*Output, error) {
	h.logger.Info("executing raw query", map[string]interface{}{
		"index":   input.IndexName,
		"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
	})

	start := time.Now()

	// Marshal query to JSON
	queryBody, err := json.Marshal(input.Query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	// ✅ Add timeout to ES request
	res, err := h.client.Search(
		h.client.Search.WithContext(ctx),
		h.client.Search.WithIndex(input.IndexName),
		h.client.Search.WithBody(bytes.NewReader(queryBody)),
		h.client.Search.WithTrackTotalHits(true),
		h.client.Search.WithTimeout(25*time.Second), // ✅ NEW: ES-level timeout
	)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrSearchTimeout
		}
		return nil, fmt.Errorf("%w: %v", ErrElasticsearchConnectionFailed, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("%w: %s", ErrSearchQueryFailed, res.String())
	}

	// Parse response
	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract hits
	hits, ok := r["hits"].(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid response format: hits missing")
	}

	// Get total
	total := int64(0)
	if totalObj, ok := hits["total"].(map[string]interface{}); ok {
		if value, ok := totalObj["value"].(float64); ok {
			total = int64(value)
		}
	}

	// Get max score
	maxScore := 0.0
	if ms, ok := hits["max_score"].(float64); ok {
		maxScore = ms
	}

	// Extract documents
	var data []map[string]interface{}
	if hitsList, ok := hits["hits"].([]interface{}); ok {
		for _, hit := range hitsList {
			if hitMap, ok := hit.(map[string]interface{}); ok {
				if source, ok := hitMap["_source"].(map[string]interface{}); ok {
					// Add ES metadata
					source["_id"] = hitMap["_id"]
					if score, ok := hitMap["_score"].(float64); ok {
						source["_score"] = score
					}
					data = append(data, source)
				}
			}
		}
	}

	return &Output{
		Success:   true,
		Data:      data,
		TotalHits: total,
		MaxScore:  maxScore,
		Took:      time.Since(start).Milliseconds(),
		IndexName: input.IndexName,
	}, nil
}

func (h *Handler) validateGeneratedQuery(params map[string]interface{}) error {
	// Ensure no script fields in final query
	paramsJSON, _ := json.Marshal(params)
	paramsStr := strings.ToLower(string(paramsJSON))

	dangerousPatterns := []string{
		"\"script\":", "\"inline\":", "\"source\":",
		"\"function\":", "\"eval(\"", "\"exec(\"",
		"\"$where\":", "\"$ne\":", "\"$regex\":",
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(paramsStr, pattern) {
			h.logger.Error("dangerous pattern in generated query", map[string]interface{}{
				"pattern": pattern,
				"params":  string(paramsJSON),
				"traceId": trace.SpanFromContext(context.Background()).SpanContext().TraceID().String(),
			})
			return appErrs.NewNoSQLInjectionError("generatedQuery", pattern)
		}
	}

	return nil
}

// ===== JOB COMPLETION =====
func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	span := trace.SpanFromContext(ctx)

	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		span.RecordError(err)
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error":   err,
			"jobKey":  job.Key,
			"traceId": span.SpanContext().TraceID().String(),
		})
		return
	}

	_, err = cmd.Send(ctx)
	if err != nil {
		span.RecordError(err)
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error":   err,
			"jobKey":  job.Key,
			"traceId": span.SpanContext().TraceID().String(),
		})
		return // ✅ Added return to prevent duplicate logging
	}

	h.logger.Info("job completed successfully", map[string]interface{}{
		"jobKey":        job.Key,
		"queryType":     output.QueryType,
		"indexName":     output.IndexName,
		"totalHits":     output.TotalHits,
		"executionTime": output.Took,
		"traceId":       span.SpanContext().TraceID().String(),
	})
}

// ===== PUBLIC API FOR DIRECT USAGE =====
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// Sanitize input
	h.sanitizeInput(input)

	// Validate input before execution
	if err := h.validateInput(input); err != nil {
		return nil, err
	}

	ctxExec, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()

	return h.execute(ctxExec, input)
}

// Helper function to map errors to error codes
func (h *Handler) mapErrorToCode(err error) string {
	switch {
	case errors.Is(err, ErrFranchiseNotFound):
		return "FRANCHISE_NOT_FOUND"
	case errors.Is(err, ErrIndexNotFound):
		return "INDEX_NOT_FOUND"
	case errors.Is(err, ErrSearchTimeout):
		return "SEARCH_TIMEOUT"
	case errors.Is(err, ErrSearchQueryFailed):
		return "SEARCH_QUERY_FAILED"
	case errors.Is(err, ErrElasticsearchConnectionFailed):
		return "ELASTICSEARCH_CONNECTION_FAILED"
	case errors.Is(err, ErrInvalidInputFormat):
		return "INVALID_INPUT_FORMAT"
	default:
		return "UNKNOWN_ERROR"
	}
}

// Helper function to determine retry count
func (h *Handler) getRetryCount(err error) int32 {
	switch {
	case errors.Is(err, ErrElasticsearchConnectionFailed):
		return 3
	case errors.Is(err, ErrSearchTimeout):
		return 2
	case errors.Is(err, ErrSearchQueryFailed):
		return 1
	case errors.Is(err, ErrFranchiseNotFound):
		return 0 // Don't retry not found errors
	default:
		return 0
	}
}
