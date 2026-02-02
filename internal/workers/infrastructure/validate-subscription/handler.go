package validatesubscription

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/redis/go-redis/v9"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "validate-subscription"
)

var (
	ErrSubscriptionInvalid     = errors.New("SUBSCRIPTION_INVALID")
	ErrSubscriptionExpired     = errors.New("SUBSCRIPTION_EXPIRED")
	ErrSubscriptionCheckFailed = errors.New("SUBSCRIPTION_CHECK_FAILED")
)

type Handler struct {
	config       *Config
	db           *sql.DB
	redis        *redis.Client
	logger       logger.Logger
	errorHandler *appErrs.ErrorHandler
	validator    *validation.Validator // ✅ ADDED
	sanitizer    *validation.Sanitizer // ✅ ADDED
}

func NewHandler(config *Config, db *sql.DB, redis *redis.Client, log logger.Logger) *Handler {
	return &Handler{
		config:       config,
		db:           db,
		redis:        redis,
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: appErrs.NewErrorHandler(log),
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
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "validate-subscription.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewBusinessRuleError("Parse input failed", fmt.Sprintf("%v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== STEP 2: VALIDATE INPUT (GAP #1 FIX) =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "validate-subscription.validateInput")
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("validation.error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	// ===== STEP 3: EXECUTE BUSINESS LOGIC =====
	ctxExec, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()

	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "validate-subscription.Execute")
	output, err := h.execute(ctxExec, &input)
	spanExec.End()

	if err != nil {
		span.RecordError(err)
		
		var stdErr *appErrs.StandardError
		if errors.Is(err, ErrSubscriptionInvalid) {
			span.SetAttributes(attribute.Bool("subscription.invalid", true))
			stdErr = appErrs.NewSubscriptionInvalidError("subscription not found or invalid")
		} else if errors.Is(err, ErrSubscriptionExpired) {
			span.SetAttributes(attribute.Bool("subscription.expired", true))
			stdErr = appErrs.NewSubscriptionExpiredError("subscription expired")
		} else if errors.Is(err, ErrSubscriptionCheckFailed) {
			span.SetAttributes(attribute.Bool("subscription.check.failed", true))
			stdErr = appErrs.NewSubscriptionCheckFailedError(err)
		} else {
			span.SetAttributes(attribute.Bool("external.service.error", true))
			stdErr = appErrs.NewExternalServiceError("subscription-check", err)
		}
		h.errorHandler.HandleJobError(ctx, client, job, stdErr)
		return
	}

	// ===== STEP 4: COMPLETE JOB =====
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "validate-subscription.completeJob")
	h.completeJob(ctx, client, job, output)
	spanComp.End()
}

// ===== CRITICAL VALIDATION FUNCTION (GAP #1 FIX) =====
func (h *Handler) validateInput(input *Input) error {
	// Validate UserID (UUID format)
	if err := ozzo.Validate(input.UserID,
		ozzo.Required.Error("userId is required"),
		ozzo.Length(36, 36).Error("userId must be exactly 36 characters"),
		validation.IsUUID,
		validation.SafeSQLString,
	); err != nil {
		return appErrs.NewInvalidUUIDError("userId", input.UserID)
	}

	// Validate SubscriptionTier
	if err := ozzo.Validate(input.SubscriptionTier,
		ozzo.Required.Error("subscriptionTier is required"),
		validation.ValidateEnum([]string{"free", "basic", "premium", "enterprise"}),
		ozzo.Length(1, 50).Error("subscriptionTier must be between 1 and 50 characters"),
		validation.SafeSQLString,
	); err != nil {
		return appErrs.NewValidationError("subscriptionTier", err.Error())
	}

	// 🔒 ADDITIONAL SECURITY CHECKS
	// Prevent SQL injection in user ID
	sqlPatterns := []string{
		"' OR '1'='1", "' OR '1'='1' --", "'; DROP TABLE", "UNION SELECT",
		"INSERT INTO", "UPDATE", "DELETE FROM", "EXEC", "EXECUTE",
		"SELECT", "FROM", "WHERE", "JOIN", "--", "/*", "*/", ";",
	}

	lowerUserID := strings.ToLower(input.UserID)
	for _, pattern := range sqlPatterns {
		if strings.Contains(lowerUserID, strings.ToLower(pattern)) {
			return appErrs.NewSQLInjectionError("userId", input.UserID)
		}
	}

	// Validate that the UUID is in correct format (8-4-4-4-12)
	uuidPattern := `^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`
	if !regexp.MustCompile(uuidPattern).MatchString(strings.ToLower(input.UserID)) {
		return appErrs.NewInvalidUUIDError("userId", input.UserID)
	}

	// Prevent NoSQL injection in subscription tier
	nosqlPatterns := []string{
		"$where", "$ne", "$gt", "$regex", "script:", "javascript:",
		"onload=", "onerror=", "eval(", "function(",
	}

	lowerTier := strings.ToLower(input.SubscriptionTier)
	for _, pattern := range nosqlPatterns {
		if strings.Contains(lowerTier, pattern) {
			return appErrs.NewNoSQLInjectionError("subscriptionTier", pattern)
		}
	}

	return nil
}

// ===== ORIGINAL EXECUTE METHOD (with sanitization added) =====
func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	// 🔒 Sanitize input before database query
	sanitizedUserID := h.sanitizer.SanitizeString(input.UserID)
	sanitizedTier := h.sanitizer.SanitizeString(input.SubscriptionTier)

	// Log sanitization if it changed the values
	if sanitizedUserID != input.UserID {
		h.logger.Warn("UserID was sanitized", map[string]interface{}{
			"original":  input.UserID,
			"sanitized": sanitizedUserID,
			"traceId":   trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}

	if sanitizedTier != input.SubscriptionTier {
		h.logger.Warn("SubscriptionTier was sanitized", map[string]interface{}{
			"original":  input.SubscriptionTier,
			"sanitized": sanitizedTier,
			"traceId":   trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}

	// 🔒 Validate that requested tier matches allowed values
	allowedTiers := map[string]bool{"free": true, "basic": true, "premium": true, "enterprise": true}
	if !allowedTiers[sanitizedTier] {
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(attribute.Bool("tier.invalid", true))
		return &Output{
			IsValid:     false,
			TierLevel:   "",
			Permissions: []string{},
		}, nil
	}

	cacheKey := "sub:" + sanitizedUserID + ":" + sanitizedTier
	
	// Cache check with tracing
	ctxCache, spanCache := otel.Tracer("worker-manager").Start(ctx, "validate-subscription.cacheCheck")
	if val, err := h.redis.Get(ctxCache, cacheKey).Result(); err == nil {
		var sub Subscription
		if err := json.Unmarshal([]byte(val), &sub); err == nil {
			spanCache.End()
			
			permissions := h.getPermissionsForTier(sub.Tier)
			span := trace.SpanFromContext(ctx)
			span.SetAttributes(
				attribute.String("subscription.tier", sub.Tier),
				attribute.Bool("subscription.valid", sub.IsValid),
				attribute.Bool("cache.hit", true),
			)
			
			return &Output{
				IsValid:     sub.IsValid,
				TierLevel:   sub.Tier,
				Permissions: permissions,
			}, nil
		}
		spanCache.SetAttributes(attribute.Bool("cache.deserialize.failed", true))
	}
	spanCache.End()

	// Database query with tracing
	ctxDB, spanDB := otel.Tracer("worker-manager").Start(ctx, "validate-subscription.databaseQuery")
	var sub Subscription
	query := `SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = $1 AND tier = $2`

	// 🔒 Use parameterized query with sanitized values
	err := h.db.QueryRowContext(ctxDB, query, sanitizedUserID, sanitizedTier).Scan(
		&sub.UserID, &sub.Tier, &sub.ExpiresAt, &sub.IsValid,
	)
	
	if err != nil {
		spanDB.RecordError(err)
		
		if errors.Is(err, sql.ErrNoRows) {
			spanDB.SetAttributes(attribute.Bool("no.rows", true))
			
			// Try without tier filter for backward compatibility
			queryFallback := `SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = $1`
			errFallback := h.db.QueryRowContext(ctxDB, queryFallback, sanitizedUserID).Scan(
				&sub.UserID, &sub.Tier, &sub.ExpiresAt, &sub.IsValid,
			)

			if errFallback != nil {
				spanDB.RecordError(errFallback)
				if errors.Is(errFallback, sql.ErrNoRows) {
					spanDB.SetAttributes(attribute.Bool("subscription.not.found", true))
					spanDB.End()
					return nil, ErrSubscriptionInvalid
				}
				spanDB.End()
				return nil, fmt.Errorf("%w: %v", ErrSubscriptionCheckFailed, errFallback)
			}
		} else {
			spanDB.End()
			return nil, fmt.Errorf("%w: %v", ErrSubscriptionCheckFailed, err)
		}
	}
	spanDB.End()

	if !sub.IsValid {
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(attribute.Bool("subscription.invalid", true))
		return nil, ErrSubscriptionInvalid
	}

	if sub.ExpiresAt != "" {
		exp, parseErr := time.Parse(time.RFC3339, sub.ExpiresAt)
		if parseErr != nil {
			h.logger.Debug("Failed to parse expiration date, skipping expiration check", map[string]interface{}{
				"userId":    sub.UserID,
				"expiresAt": sub.ExpiresAt,
				"error":     parseErr.Error(),
				"traceId":   trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
			})
		} else {
			if time.Now().After(exp) {
				span := trace.SpanFromContext(ctx)
				span.SetAttributes(attribute.Bool("subscription.expired", true))
				return nil, ErrSubscriptionExpired
			}
		}
	}

	// Check if user's tier matches or exceeds requested tier
	tierPriority := map[string]int{"free": 0, "basic": 1, "premium": 2, "enterprise": 3}
	if tierPriority[sub.Tier] < tierPriority[sanitizedTier] {
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(attribute.Bool("tier.insufficient", true))
		return &Output{
			IsValid:     false,
			TierLevel:   sub.Tier,
			Permissions: h.getPermissionsForTier(sub.Tier),
		}, nil
	}

	// Cache the result
	data, _ := json.Marshal(sub)
	h.redis.Set(ctx, cacheKey, data, 5*time.Minute)

	permissions := h.getPermissionsForTier(sub.Tier)

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("subscription.tier", sub.Tier),
		attribute.Bool("subscription.valid", true),
		attribute.Bool("cache.updated", true),
	)

	return &Output{
		IsValid:     true,
		TierLevel:   sub.Tier,
		Permissions: permissions,
	}, nil
}

// ===== HELPER: Get permissions for tier =====
func (h *Handler) getPermissionsForTier(tier string) []string {
	permissionsMap := map[string][]string{
		"free":    {"read_basic", "create_application"},
		"basic":   {"read_basic", "create_application", "view_reports", "export_data"},
		"premium": {"read_basic", "create_application", "view_reports", "export_data", "advanced_analytics", "api_access"},
		"enterprise": {"read_basic", "create_application", "view_reports", "export_data", "advanced_analytics",
			"api_access", "custom_integrations", "priority_support", "white_label"},
	}

	if perms, exists := permissionsMap[tier]; exists {
		return perms
	}
	return []string{}
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error":   err.Error(),
			"jobKey":  job.Key,
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
		return
	}
	_, err = cmd.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error":   err.Error(),
			"jobKey":  job.Key,
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



// // internal/workers/infrastructure/validate-subscription/handler.go
// package validatesubscription

// import (
// 	"context"
// 	"database/sql"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"time"

// 	"camunda-workers/internal/common/logger"
// 	appErrs "camunda-workers/internal/common/errors"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"github.com/redis/go-redis/v9"

// 	"go.opentelemetry.io/otel"
// )

// const (
// 	TaskType = "validate-subscription"
// )

// var (
// 	ErrSubscriptionInvalid     = errors.New("SUBSCRIPTION_INVALID")
// 	ErrSubscriptionExpired     = errors.New("SUBSCRIPTION_EXPIRED")
// 	ErrSubscriptionCheckFailed = errors.New("SUBSCRIPTION_CHECK_FAILED")
// )

// type Handler struct {
// 	config       *Config
// 	db           *sql.DB
// 	redis        *redis.Client
// 	logger       logger.Logger
// 	errorHandler *appErrs.ErrorHandler
// }

// func NewHandler(config *Config, db *sql.DB, redis *redis.Client, log logger.Logger) *Handler {
// 	return &Handler{
// 		config:       config,
// 		db:           db,
// 		redis:        redis,
// 		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
// 		errorHandler: appErrs.NewErrorHandler(log),
// 	}
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	h.logger.Info("processing job", map[string]interface{}{
// 		"jobKey":      job.Key,
// 		"workflowKey": job.ProcessInstanceKey,
// 	})

// 	var input Input
// 	_, spanParse := otel.Tracer("worker-manager").Start(context.Background(), "validate-subscription.parseInput")
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
// 		h.errorHandler.HandleJobError(context.Background(), client, job, appErrs.NewBusinessRuleError("Parse input failed", fmt.Sprintf("%v", err)))
// 		spanParse.End()
// 		return
// 	}
// 	spanParse.End()

// 	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
// 	defer cancel()

// 	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "validate-subscription.Execute")
// 	output, err := h.execute(ctxExec, &input)
// 	spanExec.End()
// 	if err != nil {
// 		var stdErr *appErrs.StandardError
// 		if errors.Is(err, ErrSubscriptionInvalid) {
// 			stdErr = appErrs.NewSubscriptionInvalidError("subscription not found or invalid")
// 		} else if errors.Is(err, ErrSubscriptionExpired) {
// 			stdErr = appErrs.NewSubscriptionExpiredError("subscription expired")
// 		} else if errors.Is(err, ErrSubscriptionCheckFailed) {
// 			stdErr = appErrs.NewSubscriptionCheckFailedError(err)
// 		} else {
// 			stdErr = appErrs.NewExternalServiceError("subscription-check", err)
// 		}
// 		h.errorHandler.HandleJobError(ctx, client, job, stdErr)
// 		return
// 	}

// 	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "validate-subscription.completeJob")
// 	h.completeJob(client, job, output)
// 	spanComp.End()
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	cacheKey := "sub:" + input.UserID
// 	if val, err := h.redis.Get(ctx, cacheKey).Result(); err == nil {
// 		var sub Subscription
// 		if err := json.Unmarshal([]byte(val), &sub); err == nil {
// 			return &Output{IsValid: sub.IsValid, TierLevel: sub.Tier}, nil
// 		}
// 	}

// 	var sub Subscription
// 	query := `SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = $1`
// 	err := h.db.QueryRowContext(ctx, query, input.UserID).Scan(
// 		&sub.UserID, &sub.Tier, &sub.ExpiresAt, &sub.IsValid,
// 	)
// 	if err != nil {
// 		if errors.Is(err, sql.ErrNoRows) {
// 			return nil, ErrSubscriptionInvalid
// 		}
// 		return nil, fmt.Errorf("%w: %v", ErrSubscriptionCheckFailed, err)
// 	}

// 	if !sub.IsValid {
// 		return nil, ErrSubscriptionInvalid
// 	}

// 	if sub.ExpiresAt != "" {
// 		exp, parseErr := time.Parse(time.RFC3339, sub.ExpiresAt)
// 		if parseErr != nil {
// 			// Use the structured logger interface
// 			h.logger.Debug("Failed to parse expiration date, skipping expiration check", map[string]interface{}{
// 				"userId":    sub.UserID,
// 				"expiresAt": sub.ExpiresAt,
// 				"error":     parseErr.Error(),
// 			})
// 		} else {
// 			if time.Now().After(exp) {
// 				return nil, ErrSubscriptionExpired
// 			}
// 		}
// 	}

// 	validTiers := map[string]bool{
// 		"free": true, "basic": true, "premium": true, "enterprise": true,
// 	}
// 	if !validTiers[sub.Tier] {
// 		return nil, ErrSubscriptionInvalid
// 	}

// 	data, _ := json.Marshal(sub)
// 	h.redis.Set(ctx, cacheKey, data, 5*time.Minute)

// 	return &Output{IsValid: true, TierLevel: sub.Tier}, nil
// }

// func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
// 	cmd, err := client.NewCompleteJobCommand().
// 		JobKey(job.Key).
// 		VariablesFromObject(output)
// 	if err != nil {
// 		h.logger.Error("failed to create complete job command", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 		return
// 	}
// 	_, err = cmd.Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to send complete job command", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 	}
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }
