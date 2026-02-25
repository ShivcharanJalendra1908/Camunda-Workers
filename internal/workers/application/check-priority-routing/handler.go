package checkpriorityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/redis/go-redis/v9"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "check-priority-routing"
)

type Handler struct {
	config       *Config
	db           *sql.DB
	redis        *redis.Client
	logger       logger.Logger
	errorHandler *errors.ErrorHandler
	validator    *validation.Validator // ✅ ADDED
	sanitizer    *validation.Sanitizer // ✅ ADDED
}

func NewHandler(config *Config, db *sql.DB, redis *redis.Client, log logger.Logger) *Handler {
	return &Handler{
		config:       config,
		db:           db,
		redis:        redis,
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: errors.NewErrorHandler(log),
		validator:    validation.NewValidator(), // ✅ ADDED
		sanitizer:    validation.NewSanitizer(), // ✅ ADDED
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
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "check-priority-routing.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			errors.NewValidationError("input", fmt.Sprintf("parse input: %v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== STEP 2: VALIDATE INPUT (GAP #1 FIX) =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "check-priority-routing.validateInput")
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	h.logger.Debug("Input validation passed", map[string]interface{}{
		"jobKey":     job.Key,
		"validation": "passed",
		"worker":     TaskType,
		"traceId":    traceID,
	})

	// ===== STEP 3: EXECUTE BUSINESS LOGIC =====
	ctxExec, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "check-priority-routing.Execute")
	output, err := h.execute(ctxExec, &input)
	spanExec.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctxExec, client, job,
			errors.NewValidationError("execution", err.Error()))
		return
	}

	// ===== STEP 4: COMPLETE JOB =====
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "check-priority-routing.completeJob")
	h.completeJob(ctx, client, job, output)
	spanComp.End()
}

// ===== CRITICAL VALIDATION FUNCTION (GAP #1 FIX) =====
func (h *Handler) validateInput(input *Input) error {
	// Validate FranchiseID (UUID format)
	if err := ozzo.Validate(input.FranchiseID,
		ozzo.Required.Error("franchiseId is required"),
		ozzo.Length(36, 36).Error("franchiseId must be exactly 36 characters"),
		validation.IsUUID,
		validation.SafeSQLString,
	); err != nil {
		return errors.NewInvalidUUIDError("franchiseId", input.FranchiseID)
	}

	// 🔒 ADDITIONAL SECURITY CHECKS
	// Prevent SQL injection patterns
	sqlPatterns := []string{
		"';", "--", "/*", "*/", "DROP", "UNION", "SELECT",
		"INSERT", "UPDATE", "DELETE", "EXEC", "xp_", "sp_",
	}

	lowerFranchiseID := strings.ToLower(input.FranchiseID)
	for _, pattern := range sqlPatterns {
		if strings.Contains(lowerFranchiseID, strings.ToLower(pattern)) {
			return errors.NewSQLInjectionError("franchiseId", input.FranchiseID)
		}
	}

	// Validate UUID format more strictly
	uuidRegex := regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
	if !uuidRegex.MatchString(strings.ToLower(input.FranchiseID)) {
		return errors.NewInvalidUUIDError("franchiseId", input.FranchiseID)
	}

	// Sanitize input
	input.FranchiseID = h.sanitizer.SanitizeString(input.FranchiseID)

	return nil
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	// 🔒 Sanitize franchise ID before using in queries
	sanitizedFranchiseID := h.sanitizer.SanitizeString(input.FranchiseID)

	accountType, err := h.getFranchisorAccountType(ctx, sanitizedFranchiseID)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("db.error", true))

		h.logger.Warn("failed to fetch franchisor account type, defaulting to standard", map[string]interface{}{
			"franchiseId": sanitizedFranchiseID,
			"error":       err,
			"traceId":     trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
		accountType = AccountTypeStandard
	}

	isPremium := accountType == AccountTypePremium
	priority := h.determinePriority(accountType)

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("franchise.account_type", accountType),
		attribute.Bool("franchise.is_premium", isPremium),
		attribute.String("routing.priority", priority),
	)

	h.logger.Info("priority routing determined", map[string]interface{}{
		"franchiseId": sanitizedFranchiseID,
		"accountType": accountType,
		"isPremium":   isPremium,
		"priority":    priority,
		"traceId":     trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
	})

	return &Output{
		IsPremiumFranchisor: isPremium,
		RoutingPriority:     priority,
	}, nil
}

func (h *Handler) getFranchisorAccountType(ctx context.Context, franchiseID string) (string, error) {
	// 🔒 Use parameterized query to prevent SQL injection
	cacheKey := "franchisor:account:" + franchiseID
	if val, err := h.redis.Get(ctx, cacheKey).Result(); err == nil {
		// Validate cache value
		if !isValidAccountType(val) {
			h.logger.Warn("invalid account type in cache, fetching from DB", map[string]interface{}{
				"franchiseId": franchiseID,
				"cachedValue": val,
				"traceId":     trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
			})
			// Delete invalid cache entry
			h.redis.Del(ctx, cacheKey)
		} else {
			return val, nil
		}
	}

	// 🔒 PARAMETERIZED QUERY - CRITICAL FOR SQL INJECTION PREVENTION
	_, spanQuery := otel.Tracer("worker-manager").Start(ctx, "check-priority-routing.dbQuery")
	row := h.db.QueryRowContext(ctx, `
		SELECT account_type 
		FROM franchisors 
		WHERE franchise_id = $1`, franchiseID)

	var accountType string
	err := row.Scan(&accountType)
	spanQuery.End()

	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("franchisor not found for franchise %s", franchiseID)
		}
		return "", fmt.Errorf("database error: %w", err)
	}

	// Validate account type from database
	if !isValidAccountType(accountType) {
		h.logger.Warn("invalid account type from database, defaulting to standard", map[string]interface{}{
			"franchiseId": franchiseID,
			"dbValue":     accountType,
			"traceId":     trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
		accountType = AccountTypeStandard
	}

	// 🔒 Validate before caching
	if isValidAccountType(accountType) {
		h.redis.Set(ctx, cacheKey, accountType, h.config.CacheTTL)
	}

	return accountType, nil
}

// Helper function to validate account type
func isValidAccountType(accountType string) bool {
	validTypes := []string{
		AccountTypePremium,
		AccountTypeVerified,
		AccountTypeStandard,
	}

	for _, validType := range validTypes {
		if accountType == validType {
			return true
		}
	}
	return false
}

func (h *Handler) determinePriority(accountType string) string {
	switch accountType {
	case AccountTypePremium:
		return PriorityHigh
	case AccountTypeVerified:
		return PriorityMedium
	default:
		return PriorityLow
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

// Public Execute method for direct API calls
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// Validate before execution
	if err := h.validateInput(input); err != nil {
		return nil, err
	}
	return h.execute(ctx, input)
}
