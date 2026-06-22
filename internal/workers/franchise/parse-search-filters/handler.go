package parsesearchfilters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	ozzo "github.com/go-ozzo/ozzo-validation/v4" // ✅ CHANGE THIS

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const TaskType = "parse-search-filters"

var (
	ErrInvalidFilterFormat = errors.New("INVALID_FILTER_FORMAT")
)

var validCategories = map[string]bool{
	"food": true, "retail": true, "health": true, "education": true,
	"automotive": true, "fitness": true, "beauty": true, "home": true,
}

var validSortOptions = map[string]bool{
	"relevance": true, "investment_min": true, "name": true,
}

type Handler struct {
	config       *Config
	logger       logger.Logger
	errorHandler *appErrs.ErrorHandler
	validator    *validation.Validator // ✅ KEEP
	sanitizer    *validation.Sanitizer // ✅ KEEP
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	return &Handler{
		config:       config,
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: appErrs.NewErrorHandler(log),
		validator:    validation.NewValidator(), // ✅ KEEP
		sanitizer:    validation.NewSanitizer(), // ✅ KEEP
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

	// ===== STEP 1: PARSE INPUT =====
	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "parse-search-filters.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewInvalidFilterFormatError(fmt.Sprintf("parse input: %v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== STEP 2: VALIDATE INPUT (GAP #1 FIX) =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "parse-search-filters.validateInput")
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	// ===== STEP 3: EXECUTE BUSINESS LOGIC =====
	ctxExec, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "parse-search-filters.Execute")
	output, err := h.execute(ctxExec, &input)
	spanExec.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, appErrs.NewInvalidFilterFormatError(err.Error()))
		return
	}

	// ===== STEP 4: COMPLETE JOB =====
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "parse-search-filters.completeJob")
	h.completeJob(ctx, client, job, output)
	spanComp.End()
}

// ===== CRITICAL VALIDATION FUNCTION (GAP #1 FIX) =====
func (h *Handler) validateInput(input *Input) error {
	// Validate RawFilters map size
	if len(input.RawFilters) > 50 {
		return appErrs.NewArrayTooLargeError("rawFilters", 50, len(input.RawFilters))
	}

	// Validate each key-value pair in RawFilters
	for key, value := range input.RawFilters {
		// Validate key using ozzo directly
		if err := ozzo.Validate(key,
			ozzo.Length(1, 50).Error("key must be between 1 and 50 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewValidationError(fmt.Sprintf("rawFilters.%s", key), err.Error())
		}

		// Validate value based on type
		switch v := value.(type) {
		case string:
			// String validation using ozzo
			if err := ozzo.Validate(v,
				ozzo.Length(0, 1000).Error("value must not exceed 1000 characters"),
				validation.SafeSQLString,
			); err != nil {
				return appErrs.NewValidationError(fmt.Sprintf("rawFilters.%s", key), err.Error())
			}

		case []interface{}:
			// Array validation
			if len(v) > 100 {
				return appErrs.NewArrayTooLargeError(fmt.Sprintf("rawFilters.%s", key), 100, len(v))
			}
			for i, item := range v {
				if str, ok := item.(string); ok {
					if err := ozzo.Validate(str,
						ozzo.Length(0, 200).Error("array item must not exceed 200 characters"),
						validation.SafeSQLString,
					); err != nil {
						return appErrs.NewValidationError(fmt.Sprintf("rawFilters.%s[%d]", key, i), err.Error())
					}
				}
			}

		case []string:
			// Array validation for string slices
			if len(v) > 100 {
				return appErrs.NewArrayTooLargeError(fmt.Sprintf("rawFilters.%s", key), 100, len(v))
			}
			for i, item := range v {
				if err := ozzo.Validate(item,
					ozzo.Length(0, 200).Error("array item must not exceed 200 characters"),
					validation.SafeSQLString,
				); err != nil {
					return appErrs.NewValidationError(fmt.Sprintf("rawFilters.%s[%d]", key, i), err.Error())
				}
			}

		case map[string]interface{}:
			// Nested object validation
			if len(v) > 20 {
				return appErrs.NewValidationError(fmt.Sprintf("rawFilters.%s", key),
					"nested object cannot exceed 20 items")
			}

			// Prevent deep nesting
			for nestedKey, nestedValue := range v {
				if _, isMap := nestedValue.(map[string]interface{}); isMap {
					return appErrs.NewValidationError(fmt.Sprintf("rawFilters.%s.%s", key, nestedKey),
						"objects cannot be nested more than 2 levels deep")
				}
			}

		case float64, int, bool:
			// Simple types are fine
			continue

		default:
			return appErrs.NewValidationError(fmt.Sprintf("rawFilters.%s", key),
				fmt.Sprintf("unsupported type: %T", v))
		}
	}

	// 🔒 ADDITIONAL SECURITY CHECKS
	// Check for NoSQL injection patterns in all string values
	if input.RawFilters != nil {
		for key, value := range input.RawFilters {
			if str, ok := value.(string); ok {
				if strings.Contains(strings.ToLower(str), "$where") ||
					strings.Contains(strings.ToLower(str), "$ne") ||
					strings.Contains(strings.ToLower(str), "$gt") ||
					strings.Contains(strings.ToLower(str), "$regex") {
					return appErrs.NewValidationError(fmt.Sprintf("rawFilters.%s", key),
						"contains potentially unsafe NoSQL patterns")
				}
			}
		}
	}

	return nil
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	// 🔒 Sanitize RawFilters before processing
	if input.RawFilters != nil {
		input.RawFilters = h.sanitizer.SanitizeInput(input.RawFilters)
	} else {
		input.RawFilters = make(map[string]interface{})
	}

	// Initialize with defaults as per REQ-BIZ-002
	parsed := ParsedFilters{
		Categories:      []string{},
		Locations:       []string{},
		Keywords:        "",
		SortBy:          "relevance",
		Pagination:      Pagination{Page: 1, Size: 20},
		InvestmentRange: InvestmentRange{Min: 0, Max: 10000000},
	}

	// Parse categories
	if categoriesRaw, ok := input.RawFilters["categories"]; ok {
		parsed.Categories = h.parseStringArray(categoriesRaw)
		// Validate categories as per REQ-BIZ-003
		for _, cat := range parsed.Categories {
			if !validCategories[cat] {
				return nil, fmt.Errorf("%w: invalid category '%s'", ErrInvalidFilterFormat, cat)
			}
		}
	}

	// Parse investment range as per REQ-BIZ-002
	if invRangeRaw, ok := input.RawFilters["investmentRange"]; ok {
		if invMap, ok := invRangeRaw.(map[string]interface{}); ok {
			// Parse min
			if minRaw, exists := invMap["min"]; exists {
				if min, err := h.parseInt(minRaw); err == nil {
					// Validate min >= 0 as per REQ-BIZ-003
					if min >= 0 {
						parsed.InvestmentRange.Min = min
					}
				}
			}

			// Parse max
			if maxRaw, exists := invMap["max"]; exists {
				if max, err := h.parseInt(maxRaw); err == nil {
					// Validate max <= 10000000 as per REQ-BIZ-003
					if max > 0 && max <= 10000000 {
						parsed.InvestmentRange.Max = max
					}
				}
			}

			// Validate min <= max as per REQ-BIZ-003
			if parsed.InvestmentRange.Min > parsed.InvestmentRange.Max {
				return nil, fmt.Errorf("%w: investment min (%d) > max (%d)",
					ErrInvalidFilterFormat, parsed.InvestmentRange.Min, parsed.InvestmentRange.Max)
			}
		}
	}

	// Parse locations
	if locationsRaw, ok := input.RawFilters["locations"]; ok {
		parsed.Locations = h.parseStringArray(locationsRaw)
	}

	// Parse keywords
	if keywordsRaw, ok := input.RawFilters["keywords"]; ok {
		if s, ok := keywordsRaw.(string); ok {
			parsed.Keywords = strings.TrimSpace(s)
		}
	}

	// Parse sortBy with validation as per REQ-BIZ-003
	if sortByRaw, ok := input.RawFilters["sortBy"]; ok {
		if s, ok := sortByRaw.(string); ok {
			s = strings.TrimSpace(s)
			if validSortOptions[s] {
				parsed.SortBy = s
			} else {
				return nil, fmt.Errorf("%w: invalid sortBy '%s'", ErrInvalidFilterFormat, s)
			}
		}
	}

	// Parse pagination as per REQ-BIZ-002
	if paginationRaw, ok := input.RawFilters["pagination"]; ok {
		if pgMap, ok := paginationRaw.(map[string]interface{}); ok {
			// Parse page
			if pageRaw, exists := pgMap["page"]; exists {
				if page, err := h.parseInt(pageRaw); err == nil {
					// Validate page >= 1 as per REQ-BIZ-003
					if page >= 1 {
						parsed.Pagination.Page = page
					}
				}
			}

			// Parse size
			if sizeRaw, exists := pgMap["size"]; exists {
				if size, err := h.parseInt(sizeRaw); err == nil {
					// Validate size between 1 and 100 as per REQ-BIZ-003
					// Values > 100 are capped at 100
					if size >= 1 {
						if size <= 100 {
							parsed.Pagination.Size = size
						} else {
							parsed.Pagination.Size = 100
						}
					}
				}
			}
		}
	}

	// Parse exclusivityType
	if val, ok := input.RawFilters["exclusivityType"]; ok {
		if s, ok := val.(string); ok {
			parsed.ExclusivityType = strings.TrimSpace(s)
		}
	}

	// Parse territoryScope
	if val, ok := input.RawFilters["territoryScope"]; ok {
		if s, ok := val.(string); ok {
			parsed.TerritoryScope = strings.TrimSpace(s)
		}
	}

	// Parse minUnits
	if val, ok := input.RawFilters["minUnits"]; ok {
		if i, err := h.parseInt(val); err == nil {
			parsed.MinUnits = i
		}
	}

	// Parse localBrandsOnly
	if val, ok := input.RawFilters["localBrandsOnly"]; ok {
		if b, ok := val.(bool); ok {
			parsed.LocalBrandsOnly = b
		} else if s, ok := val.(string); ok {
			parsed.LocalBrandsOnly = (strings.ToLower(strings.TrimSpace(s)) == "true")
		}
	}

	h.logger.Info("filters parsed successfully", map[string]interface{}{
		"categories":      parsed.Categories,
		"investmentRange": parsed.InvestmentRange,
		"locations":       parsed.Locations,
		"keywords":        parsed.Keywords,
		"sortBy":          parsed.SortBy,
		"pagination":      parsed.Pagination,
		"traceId":         trace.SpanFromContext(ctx).SpanContext().TraceID().String(), // ✅ ADD traceId
	})

	return &Output{ParsedFilters: parsed}, nil
}

func (h *Handler) parseStringArray(raw interface{}) []string {
	// Always return non-nil slice
	result := []string{}

	if raw == nil {
		return result
	}

	seen := make(map[string]bool) // For deduplication

	switch v := raw.(type) {
	case string:
		if v != "" {
			parts := strings.Split(v, ",")
			for _, s := range parts {
				trimmed := strings.TrimSpace(s)
				if trimmed != "" && !seen[trimmed] {
					result = append(result, trimmed)
					seen[trimmed] = true
				}
			}
		}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				trimmed := strings.TrimSpace(s)
				if trimmed != "" && !seen[trimmed] {
					result = append(result, trimmed)
					seen[trimmed] = true
				}
			}
		}
	case []string:
		for _, s := range v {
			trimmed := strings.TrimSpace(s)
			if trimmed != "" && !seen[trimmed] {
				result = append(result, trimmed)
				seen[trimmed] = true
			}
		}
	}

	return result
}

func (h *Handler) parseInt(raw interface{}) (int, error) {
	if raw == nil {
		return 0, errors.New("cannot parse nil as integer")
	}

	switch v := raw.(type) {
	case float64:
		// Check if it's a valid positive integer
		if v < 0 || v != float64(int(v)) {
			return 0, errors.New("not a valid positive integer")
		}
		return int(v), nil

	case int:
		if v < 0 {
			return 0, errors.New("negative integer not allowed")
		}
		return v, nil

	case int64:
		if v < 0 {
			return 0, errors.New("negative integer not allowed")
		}
		return int(v), nil

	case string:
		// 🔒 SANITIZE the string first
		sanitized := h.sanitizer.SanitizeString(v)

		// Handle special case: extract numbers and check for decimal point
		// "USD 50,000.00" should become "50000" not "5000000"

		// First remove currency symbols and spaces
		cleaned := strings.ReplaceAll(sanitized, " ", "")
		cleaned = strings.ReplaceAll(cleaned, "$", "")
		cleaned = strings.ReplaceAll(cleaned, "USD", "")
		cleaned = strings.ReplaceAll(cleaned, ",", "")

		// If there's a decimal point, truncate at it (for monetary values)
		if strings.Contains(cleaned, ".") {
			parts := strings.Split(cleaned, ".")
			cleaned = parts[0]
		}

		// Now remove any remaining non-digit characters
		re := regexp.MustCompile(`[^\d]+`)
		cleaned = re.ReplaceAllString(cleaned, "")

		if cleaned == "" {
			return 0, errors.New("not a number")
		}

		num, err := strconv.Atoi(cleaned)
		if err != nil {
			return 0, fmt.Errorf("strconv.Atoi failed: %w", err)
		}
		if num < 0 {
			return 0, errors.New("negative integer not allowed")
		}
		return num, nil

	default:
		return 0, errors.New("not a number")
	}
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to create complete job command", map[string]interface{}{
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
	if err := h.validateInput(input); err != nil {
		return nil, err
	}
	return h.execute(ctx, input)
}

