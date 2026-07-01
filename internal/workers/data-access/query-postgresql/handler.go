package querypostgresql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"
	"camunda-workers/internal/crypto"
	"camunda-workers/internal/models"
	"camunda-workers/internal/workers/data-access/query-postgresql/queries"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "query-postgresql"
)

var (
	ErrDatabaseConnectionFailed = errors.New("DATABASE_CONNECTION_FAILED")
	ErrQueryExecutionFailed     = errors.New("QUERY_EXECUTION_FAILED")
	ErrQueryTimeout             = errors.New("QUERY_TIMEOUT")
	ErrInvalidQueryType         = errors.New("INVALID_QUERY_TYPE")
)

type Handler struct {
	config       *Config
	db           *sql.DB
	logger       logger.Logger
	errorHandler *appErrs.ErrorHandler
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
	encryptor    *crypto.Encryptor
}

func NewHandler(config *Config, db *sql.DB, log logger.Logger) *Handler {
	var encryptor *crypto.Encryptor
	if config.EncryptionKey != "" {
		enc, err := crypto.NewEncryptor(config.EncryptionKey)
		if err == nil {
			encryptor = enc
		} else {
			log.Warn("Failed to initialize encryptor", map[string]interface{}{"error": err.Error()})
		}
	} else {
		log.Warn("EncryptionKey not provided in config", nil)
	}

	return &Handler{
		config:       config,
		db:           db,
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: appErrs.NewErrorHandler(log),
		validator:    validation.NewValidator(),
		sanitizer:    validation.NewSanitizer(),
		encryptor:    encryptor,
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
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "query-postgresql.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewValidationError("input", fmt.Sprintf("parse input: %v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== CRITICAL: INPUT VALIDATION (GAP #1) =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "query-postgresql.validateInput")

	// Sanitize input first
	input.Sanitize()

	// Comprehensive validation
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("validation.error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	// Execute with timeout
	ctxExec, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()

	_, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "query-postgresql.Execute")
	output, err := h.execute(ctxExec, &input)
	spanExec.End()

	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("execution.error", true))
		h.errorHandler.HandleJobError(ctxExec, client, job, err)
		return
	}

	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "query-postgresql.completeJob")
	h.completeJob(ctx, client, job, output)
	spanComp.End()
}

func (h *Handler) validateInput(input *Input) error {
	// Use the Input's Validate method which includes all fields
	if err := input.Validate(); err != nil {
		return appErrs.NewValidationError("input", err.Error())
	}

	// Additional validation based on query type
	switch input.QueryType {
	case string(models.QueryTypeFranchiseFullDetails):
		return h.validateFranchiseById(input)

	case string(models.QueryTypeFranchiseDetails):
		return h.validateFranchiseByIds(input)

	case string(models.QueryTypeUserProfile):
		return h.validateUserById(input)

	case string(models.QueryTypeIndustryBySlug):
		return h.validateIndustryBySlug(input)

	case string(models.QueryTypeCategoryQuestionsByIndustry):
		return h.validateCategoryQuestions(input)

	case string(models.QueryTypeIndustriesTop9),
		string(models.QueryTypeCategoriesTop30),
		string(models.QueryTypeCategoriesFeatured8),
		string(models.QueryTypeFranchiseOverview),
		string(models.QueryTypeFranchiseBusiness),
		string(models.QueryTypeFranchiseInvestment),
		string(models.QueryTypeFranchiseOperations),
		string(models.QueryTypeFranchiseSocial),
		string(models.QueryTypeIndustryBySlugWithQuestions),
		string(models.QueryTypeFeaturedCategoriesByIndustry):
		return nil // These don't need additional validation

	case string(models.QueryTypeFranchiseContactInfo):
		return h.validateFranchiseById(input)

	default:
		return appErrs.NewInvalidQueryTypeError(input.QueryType)
	}
}

// ===== ADD THIS HELPER FUNCTION =====
func (h *Handler) sanitizeSlug(slug string) string {
	if slug == "" {
		return ""
	}

	// Remove whitespace
	slug = strings.TrimSpace(slug)

	// Remove leading/trailing quotes (both single and double)
	slug = strings.Trim(slug, `"'`)

	// Remove escaped quotes
	slug = strings.ReplaceAll(slug, `\"`, "")
	slug = strings.ReplaceAll(slug, `\'`, "")

	// Remove any backslashes
	slug = strings.ReplaceAll(slug, `\`, "")

	return slug
}

// ✅ FIXED: Extract slug from multiple possible locations
func (h *Handler) validateIndustryBySlug(input *Input) error {
	// Try to get slug from multiple sources
	slug := input.Slug
	if slug == "" {
		slug = input.IndustrySlug
	}
	if slug == "" && input.Filters != nil {
		if s, ok := input.Filters["slug"].(string); ok {
			slug = s
		}
	}
	if slug == "" && input.Filters != nil {
		if s, ok := input.Filters["industrySlug"].(string); ok {
			slug = s
		}
	}

	if slug == "" {
		return nil
	}

	// Clean malformed quotes from slug
	slug = h.sanitizeSlug(slug)

	// Update the input with cleaned slug
	input.Slug = slug
	if input.IndustrySlug != "" {
		input.IndustrySlug = slug
	}

	// Validate slug format
	if err := ozzo.Validate(slug,
		ozzo.Required.Error("slug is required"),
		ozzo.Length(1, 100).Error("slug must be between 1 and 100 characters"),
		validation.SafeSQLString,
	); err != nil {
		return appErrs.NewValidationError("slug", err.Error())
	}

	return nil
}

// ✅ FIXED: Accept either industrySlug or industryId
// func (h *Handler) validateCategoryQuestions(input *Input) error {
// 	hasSlug := input.IndustrySlug != "" ||
// 		(input.Filters != nil && input.Filters["industrySlug"] != nil)
// 	hasId := input.IndustryID != "" ||
// 		(input.Filters != nil && input.Filters["industryId"] != nil)

// 	if !hasSlug && !hasId {
// 		return appErrs.NewRequiredFieldError("industrySlug or industryId")
// 	}

//		return nil
//	}
func (h *Handler) validateCategoryQuestions(_ *Input) error {
	return nil
}

func (h *Handler) validateFranchiseById(input *Input) error {
	// CRITICAL: UUID validation for franchiseId
	if err := ozzo.Validate(input.FranchiseID,
		ozzo.Required.Error("franchiseId is required"),
		ozzo.Length(36, 36).Error("franchiseId must be exactly 36 characters"),
		validation.IsUUID,
		validation.SafeSQLString,
	); err != nil {
		return appErrs.NewInvalidUUIDError("franchiseId", input.FranchiseID)
	}
	return nil
}

func (h *Handler) validateFranchiseByIds(input *Input) error {
	// CRITICAL: Validate array of franchise IDs
	if err := validation.ValidateArraySize(1, 100).Validate(input.FranchiseIDs); err != nil {
		return appErrs.NewArrayTooLargeError("franchiseIds", 100, len(input.FranchiseIDs))
	}

	// Validate each UUID
	for i, id := range input.FranchiseIDs {
		if err := ozzo.Validate(id,
			ozzo.Required.Error(fmt.Sprintf("franchiseIds[%d] is required", i)),
			ozzo.Length(36, 36).Error(fmt.Sprintf("franchiseIds[%d] must be exactly 36 characters", i)),
			validation.IsUUID,
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewInvalidUUIDError(fmt.Sprintf("franchiseIds[%d]", i), id)
		}
	}
	return nil
}

func (h *Handler) validateFranchiseSearch(input *Input) error {
	if input.Filters == nil {
		return nil // Filters are optional for search
	}

	filters := input.Filters

	// Validate category
	if category, ok := filters["category"].(string); ok {
		if err := ozzo.Validate(category,
			ozzo.Length(0, 100).Error("category must be at most 100 characters"),
			validation.SafeSQLString,
			validation.AlphanumericOnly,
		); err != nil {
			return appErrs.NewValidationError("filters.category", err.Error())
		}
	}

	// Validate location
	if location, ok := filters["location"].(string); ok {
		if err := ozzo.Validate(location,
			ozzo.Length(0, 200).Error("location must be at most 200 characters"),
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewValidationError("filters.location", err.Error())
		}
	}

	// Validate investment range
	if investmentMin, ok := filters["investmentMin"]; ok {
		if err := h.validateInvestmentValue(investmentMin, "filters.investmentMin"); err != nil {
			return err
		}
	}

	if investmentMax, ok := filters["investmentMax"]; ok {
		if err := h.validateInvestmentValue(investmentMax, "filters.investmentMax"); err != nil {
			return err
		}
	}

	// Validate investment range makes sense
	if min, maxExists := filters["investmentMin"]; maxExists {
		if max, minExists := filters["investmentMax"]; minExists {
			minVal := h.toFloat64(min)
			maxVal := h.toFloat64(max)
			if minVal > maxVal {
				return appErrs.NewValidationError("filters.investmentRange",
					"investmentMin cannot be greater than investmentMax")
			}
		}
	}

	// Validate pagination
	if page, ok := filters["page"]; ok {
		if err := h.validatePaginationValue(page, "filters.page", 1, 1000); err != nil {
			return err
		}
	}

	if limit, ok := filters["limit"]; ok {
		if err := h.validatePaginationValue(limit, "filters.limit", 1, 100); err != nil {
			return err
		}
	}

	// Validate sort field
	if sortField, ok := filters["sortField"].(string); ok {
		allowedFields := []string{"name", "category", "investment", "created_at", "updated_at"}
		if err := validation.ValidateEnum(allowedFields).Validate(sortField); err != nil {
			return appErrs.NewValidationError("filters.sortField",
				fmt.Sprintf("must be one of: %s", strings.Join(allowedFields, ", ")))
		}
	}

	// Validate sort direction
	if sortDir, ok := filters["sortDirection"].(string); ok {
		if err := validation.ValidateEnum([]string{"ASC", "DESC", "asc", "desc"}).Validate(sortDir); err != nil {
			return appErrs.NewValidationError("filters.sortDirection", "must be ASC or DESC")
		}
	}

	return nil
}

func (h *Handler) validateUserById(input *Input) error {
	// CRITICAL: UUID validation for userId
	if err := ozzo.Validate(input.UserID,
		ozzo.Required.Error("userId is required"),
		ozzo.Length(36, 36).Error("userId must be exactly 36 characters"),
		validation.IsUUID,
		validation.SafeSQLString,
	); err != nil {
		return appErrs.NewInvalidUUIDError("userId", input.UserID)
	}
	return nil
}

func (h *Handler) validateUserByEmail(input *Input) error {
	if input.Filters == nil {
		return appErrs.NewRequiredFieldError("filters.email")
	}

	email, ok := input.Filters["email"].(string)
	if !ok {
		return appErrs.NewValidationError("filters.email", "must be a string")
	}

	// CRITICAL: Email validation
	if err := ozzo.Validate(email,
		ozzo.Required.Error("email is required"),
		ozzo.Length(5, 255).Error("email must be between 5 and 255 characters"),
		validation.ValidateEmail(),
		validation.SafeSQLString,
	); err != nil {
		return appErrs.NewValidationError("filters.email", err.Error())
	}

	return nil
}

func (h *Handler) validateUserSearch(input *Input) error {
	if input.Filters == nil {
		return nil // Filters are optional
	}

	filters := input.Filters

	// Validate search query
	if query, ok := filters["query"].(string); ok {
		if err := ozzo.Validate(query,
			ozzo.Length(0, 500).Error("query must be at most 500 characters"),
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewValidationError("filters.query", err.Error())
		}
	}

	// Validate role filter
	if role, ok := filters["role"].(string); ok {
		allowedRoles := []string{"admin", "user", "franchisee", "investor"}
		if err := validation.ValidateEnum(allowedRoles).Validate(role); err != nil {
			return appErrs.NewValidationError("filters.role",
				fmt.Sprintf("must be one of: %s", strings.Join(allowedRoles, ", ")))
		}
	}

	// Validate pagination
	if page, ok := filters["page"]; ok {
		if err := h.validatePaginationValue(page, "filters.page", 1, 1000); err != nil {
			return err
		}
	}

	if limit, ok := filters["limit"]; ok {
		if err := h.validatePaginationValue(limit, "filters.limit", 1, 100); err != nil {
			return err
		}
	}

	return nil
}

// Helper validation functions

func (h *Handler) validateInvestmentValue(value interface{}, field string) error {
	numValue := h.toFloat64(value)
	if numValue < 0 {
		return appErrs.NewValidationError(field, "must be >= 0")
	}
	if numValue > 1000000000 { // 1 billion max
		return appErrs.NewValidationError(field, "must be <= 1,000,000,000")
	}
	return nil
}

func (h *Handler) validatePaginationValue(value interface{}, field string, min, max int64) error {
	numValue := h.toInt64(value)
	if numValue < min {
		return appErrs.NewValidationError(field, fmt.Sprintf("must be >= %d", min))
	}
	if numValue > max {
		return appErrs.NewValidationError(field, fmt.Sprintf("must be <= %d", max))
	}
	return nil
}

func (h *Handler) toFloat64(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func (h *Handler) toInt64(value interface{}) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}

// ===== QUERY EXECUTION WITH SAFETY CHECKS =====
func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	if input == nil {
		return nil, appErrs.NewValidationError("input", "input cannot be nil")
	}

	queryType := models.QueryType(input.QueryType)
	if _, exists := queries.Registry[queryType]; !exists {
		return nil, appErrs.NewInvalidQueryTypeError(input.QueryType)
	}

	// ✅ Build parameters with ALL possible field sources
	params := make(map[string]interface{})

	if input.Params != nil {
		if basicInfo, ok := input.Params["basicInfo"].(map[string]interface{}); ok {
			if id, ok := basicInfo["id"].(string); ok && id != "" {
				params["franchiseId"] = id
				h.logger.Info("Extracted franchiseId from basicInfo", map[string]interface{}{
					"franchiseId": id,
				})
			}
		}
	}

	// Add specific known fields
	if input.FranchiseID != "" {
		params["franchiseId"] = input.FranchiseID
	}
	
	// ✅ CRITICAL FIX: If franchiseId is not a UUID (e.g. it's a slug passed as fallback), resolve it
	if idStr, ok := params["franchiseId"].(string); ok && idStr != "" {
		// A simple UUID check (length 36, 4 dashes)
		isUUID := len(idStr) == 36 && strings.Count(idStr, "-") == 4
		if !isUUID {
			var realID string
			dbErr := h.db.QueryRowContext(ctx, "SELECT id FROM franchises WHERE slug = $1", idStr).Scan(&realID)
			if dbErr == nil {
				params["franchiseId"] = realID
				input.FranchiseID = realID
				h.logger.Info("Resolved franchise slug to UUID", map[string]interface{}{
					"slug": idStr,
					"uuid": realID,
				})
			}
		}
	}
	if len(input.FranchiseIDs) > 0 {
		params["franchiseIds"] = input.FranchiseIDs
	}
	if input.UserID != "" {
		params["userId"] = input.UserID
	}
	if input.EntityType != "" {
		et := strings.ToLower(input.EntityType)
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

	// ✅ CRITICAL: Sanitize all slug fields before using them
	if input.Slug != "" {
		params["slug"] = h.sanitizeSlug(input.Slug)
	}
	if input.IndustrySlug != "" {
		cleanSlug := h.sanitizeSlug(input.IndustrySlug)
		params["industrySlug"] = cleanSlug
		// Also set as "slug" if slug wasn't provided
		if input.Slug == "" {
			params["slug"] = cleanSlug
		}
	}
	if input.IndustryID != "" {
		params["industryId"] = input.IndustryID
	}

	// ✅ Extract from filters with sanitization
	if input.Filters != nil {
		params["filters"] = input.Filters

		// Extract common filter fields as top-level params
		if slug, ok := input.Filters["slug"].(string); ok && input.Slug == "" {
			params["slug"] = h.sanitizeSlug(slug)
		}
		if industrySlug, ok := input.Filters["industrySlug"].(string); ok && input.IndustrySlug == "" {
			cleanSlug := h.sanitizeSlug(industrySlug)
			params["industrySlug"] = cleanSlug
			if input.Slug == "" {
				params["slug"] = cleanSlug
			}
		}
		if industryId, ok := input.Filters["industryId"].(string); ok && input.IndustryID == "" {
			params["industryId"] = industryId
		}

		// ✅ Extract industrySlug from category field
		if category, ok := input.Filters["category"].(string); ok && category != "" {
			cleanSlug := h.sanitizeSlug(category)
			if input.IndustrySlug == "" {
				params["industrySlug"] = cleanSlug
			}
			if input.Slug == "" {
				params["slug"] = cleanSlug
			}
		}
	}

	if input.Params != nil {
		for k, v := range input.Params {
			if _, exists := params[k]; !exists {
				params[k] = v
			}
		}
	}

	// Add query type as span attribute
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("db.query_type", string(queryType)),
		attribute.Int("input.franchise_ids_count", len(input.FranchiseIDs)),
	)

	// Execute query
	data, rowCount, execTime, err := queries.Execute(ctx, h.db, queryType, params, h.encryptor)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, appErrs.NewQueryTimeoutError(string(queryType))
		}
		return nil, appErrs.NewQueryExecutionFailedError(string(queryType), err)
	}

	// Validate result size
	if rowCount > 10000 {
		h.logger.Warn("large result set returned", map[string]interface{}{
			"queryType": queryType,
			"rowCount":  rowCount,
			"traceId":   span.SpanContext().TraceID().String(),
		})
	}

	// Add execution metrics to span
	span.SetAttributes(
		attribute.Int64("db.row_count", int64(rowCount)),
		attribute.Float64("db.execution_time_ms", float64(execTime)),
	)

	return &Output{
		Data:               data,
		RowCount:           rowCount,
		QueryExecutionTime: execTime,
	}, nil
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
			"jobKey":  job.Key,
			"traceId": span.SpanContext().TraceID().String(),
		})
		return
	}

	_, err = cmd.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error":   err,
			"jobKey":  job.Key,
			"traceId": span.SpanContext().TraceID().String(),
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
