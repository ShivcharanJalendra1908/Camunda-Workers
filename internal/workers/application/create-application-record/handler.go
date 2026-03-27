package createapplicationrecord

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/google/uuid"

	"camunda-workers/internal/common/idempotency"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "create-application-record"
)

var (
	ErrDatabaseInsertFailed = errors.New("DATABASE_INSERT_FAILED")
	ErrDuplicateApplication = errors.New("DUPLICATE_APPLICATION")
)

type Handler struct {
	db                 *sql.DB
	logger             logger.Logger
	errorHandler       *appErrs.ErrorHandler
	validator          *validation.Validator
	sanitizer          *validation.Sanitizer
	keyGenerator       *idempotency.KeyGenerator
	idempotencyChecker *idempotency.DBChecker
}

func NewHandler(config *Config, db *sql.DB, log logger.Logger) *Handler {
	return &Handler{
		db:                 db,
		logger:             log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler:       appErrs.NewErrorHandler(log),
		validator:          validation.NewValidator(),
		sanitizer:          validation.NewSanitizer(),
		keyGenerator:       idempotency.NewKeyGenerator(),
		idempotencyChecker: idempotency.NewDBChecker(db),
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
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "create-application-record.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewValidationError("input", fmt.Sprintf("parse input: %v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== STEP 2: VALIDATE INPUT (GAP #1 FIX) =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "create-application-record.validateInput")
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

	// ===== STEP 3: GENERATE IDEMPOTENCY KEY =====
	// Generate idempotency key from input
	idempotencyKey := h.generateIdempotencyKey(&input)

	h.logger.Info("Generated idempotency key", map[string]interface{}{
		"idempotencyKey": idempotencyKey,
		"seekerId":       input.SeekerID,
		"franchiseId":    input.FranchiseID,
		"traceId":        traceID,
	})

	// ===== STEP 4: CHECK IDEMPOTENCY =====
	_, spanIdempotency := otel.Tracer("worker-manager").Start(ctx, "create-application-record.checkIdempotency")

	// First check if operation already processed
	result, err := h.idempotencyChecker.Check(ctx, idempotencyKey)
	if err != nil && err != idempotency.ErrKeyExpired {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.logger.Error("Idempotency check failed", map[string]interface{}{
			"error":   err.Error(),
			"traceId": traceID,
		})
		h.failJob(ctx, client, job, "IDEMPOTENCY_CHECK_FAILED", err.Error(), 3)
		spanIdempotency.End()
		return
	}

	if result != nil && result.IsDuplicate && result.Response != nil {
		h.logger.Info("Duplicate request detected, returning cached response", map[string]interface{}{
			"idempotencyKey": idempotencyKey,
			"createdAt":      result.CreatedAt,
			"traceId":        traceID,
		})

		// Convert cached response to Output
		cachedOutput := &Output{
			ApplicationID:     getStringFromMap(result.Response, "applicationId"),
			ApplicationStatus: getStringFromMap(result.Response, "applicationStatus"),
			CreatedAt:         getStringFromMap(result.Response, "createdAt"),
		}

		h.completeJob(ctx, client, job, cachedOutput)
		spanIdempotency.End()
		return
	}

	// Mark as processing (lock the key)
	err = h.idempotencyChecker.MarkProcessing(ctx, idempotencyKey, "create-application-record", 1*time.Hour)
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.logger.Error("Failed to mark as processing", map[string]interface{}{
			"error":   err.Error(),
			"traceId": traceID,
		})
		h.failJob(ctx, client, job, "PROCESSING_LOCK_FAILED", err.Error(), 3)
		spanIdempotency.End()
		return
	}
	spanIdempotency.End()

	// ===== STEP 5: EXECUTE BUSINESS LOGIC =====
	ctxExec, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "create-application-record.Execute")
	output, err := h.execute(ctxExec, &input, idempotencyKey)
	spanExec.End()

	if err != nil {
		errorCode := "UNKNOWN_ERROR"
		retries := int32(0)
		if errors.Is(err, ErrDatabaseInsertFailed) {
			errorCode = "DATABASE_INSERT_FAILED"
			retries = 3
		} else if errors.Is(err, ErrDuplicateApplication) {
			errorCode = "DUPLICATE_APPLICATION"
			retries = 0
		}

		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))

		// Mark idempotency as failed
		h.idempotencyChecker.MarkFailed(ctx, idempotencyKey)

		h.failJob(ctx, client, job, errorCode, err.Error(), retries)
		return
	}

	// ===== STEP 6: STORE SUCCESS RESPONSE =====
	outputMap := map[string]interface{}{
		"applicationId":     output.ApplicationID,
		"applicationStatus": output.ApplicationStatus,
		"createdAt":         output.CreatedAt,
	}
	h.idempotencyChecker.MarkCompleted(ctx, idempotencyKey, outputMap)

	// ===== STEP 7: COMPLETE JOB =====
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "create-application-record.completeJob")
	h.completeJob(ctx, client, job, output)
	spanComp.End()
}

// ===== HELPER: GET STRING FROM MAP =====
func getStringFromMap(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// ===== IDEMPOTENCY KEY GENERATION =====
func (h *Handler) generateIdempotencyKey(input *Input) string {
	// Use combination of seekerId + franchiseId + current hour
	// This prevents duplicate applications within same hour
	currentHour := time.Now().UTC().Format("2006-01-02T15")
	return fmt.Sprintf("app:%s:%s:%s", input.SeekerID, input.FranchiseID, currentHour)
}

// ===== CRITICAL VALIDATION FUNCTION (GAP #1 FIX) =====
func (h *Handler) validateInput(input *Input) error {
	// Validate SeekerID (UUID)
	if err := ozzo.Validate(input.SeekerID,
		ozzo.Required.Error("seekerId is required"),
		ozzo.Length(36, 36).Error("seekerId must be exactly 36 characters"),
		validation.IsUUID,
		validation.SafeSQLString,
	); err != nil {
		return appErrs.NewInvalidUUIDError("seekerId", input.SeekerID)
	}

	// Validate FranchiseID (UUID)
	if err := ozzo.Validate(input.FranchiseID,
		ozzo.Required.Error("franchiseId is required"),
		ozzo.Length(36, 36).Error("franchiseId must be exactly 36 characters"),
		validation.IsUUID,
		validation.SafeSQLString,
	); err != nil {
		return appErrs.NewInvalidUUIDError("franchiseId", input.FranchiseID)
	}

	// Validate ReadinessScore
	if err := ozzo.Validate(input.ReadinessScore,
		// ozzo.Required.Error("readinessScore is required"),
		ozzo.Min(0.0).Error("readinessScore cannot be negative"),
		ozzo.Max(100.0).Error("readinessScore cannot exceed 100"),
	); err != nil {
		return appErrs.NewValidationError("readinessScore", err.Error())
	}

	// Validate Priority
	if err := ozzo.Validate(input.Priority,
		ozzo.Required.Error("priority is required"),
		validation.ValidateEnum([]string{"low", "medium", "high", "urgent"}),
	); err != nil {
		return appErrs.NewValidationError("priority", err.Error())
	}

	// Validate ApplicationData (JSON object)
	if input.ApplicationData == nil {
		return appErrs.NewRequiredFieldError("applicationData")
	}

	// Validate ApplicationData size and structure
	if err := h.validateApplicationData(input.ApplicationData); err != nil {
		return err
	}

	return nil
}

// ===== HELPER: ApplicationData Validation =====
func (h *Handler) validateApplicationData(data map[string]interface{}) error {
	// Prevent too large application data
	if len(data) > 50 {
		return appErrs.NewValidationError("applicationData", "cannot exceed 50 fields")
	}

	// Prevent deep nesting
	if err := h.validateDataDepth(data, 0); err != nil {
		return err
	}

	// Validate common fields if present
	for key, value := range data {
		// Validate key
		if err := ozzo.Validate(key,
			ozzo.Length(1, 100).Error("field name must be between 1-100 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewValidationError(fmt.Sprintf("applicationData.%s", key), "invalid field name: "+err.Error())
		}

		// Validate value based on type
		switch v := value.(type) {
		case string:
			if len(v) > 1000 {
				return appErrs.NewValidationError(fmt.Sprintf("applicationData.%s", key),
					"string value cannot exceed 1000 characters")
			}
			// Check for script injection in string values
			if strings.Contains(strings.ToLower(v), "<script") ||
				strings.Contains(strings.ToLower(v), "javascript:") ||
				strings.Contains(strings.ToLower(v), "data:text/html") {
				return appErrs.NewValidationError(fmt.Sprintf("applicationData.%s", key),
					"value contains potentially unsafe content")
			}
		case float64:
			// Numeric values are fine
		case bool:
			// Boolean values are fine
		case []interface{}:
			// Validate array size
			if len(v) > 100 {
				return appErrs.NewValidationError(fmt.Sprintf("applicationData.%s", key),
					"array cannot exceed 100 items")
			}
		case map[string]interface{}:
			// Already handled by depth validation
		case nil:
			// Null values are allowed
		default:
			return appErrs.NewValidationError(fmt.Sprintf("applicationData.%s", key),
				fmt.Sprintf("unsupported data type: %T", v))
		}
	}

	return nil
}

// ===== HELPER: Data Depth Validation =====
func (h *Handler) validateDataDepth(data map[string]interface{}, depth int) error {
	if depth > 5 {
		return appErrs.NewValidationError("applicationData", "object nesting too deep (max 5 levels)")
	}

	for _, value := range data {
		if nestedMap, ok := value.(map[string]interface{}); ok {
			if err := h.validateDataDepth(nestedMap, depth+1); err != nil {
				return err
			}
		}
	}

	return nil
}

// ===== EXECUTE WITH IDEMPOTENCY =====
func (h *Handler) execute(ctx context.Context, input *Input, idempotencyKey string) (*Output, error) {
	// ===== STEP 1: SANITIZE INPUT =====
	input.SeekerID = h.sanitizer.SanitizeString(input.SeekerID)
	input.FranchiseID = h.sanitizer.SanitizeString(input.FranchiseID)
	input.Priority = h.sanitizer.SanitizeString(input.Priority)

	if input.ApplicationData != nil {
		sanitizedData := make(map[string]interface{})
		for key, value := range input.ApplicationData {
			sanitizedKey := h.sanitizer.SanitizeString(key)
			switch v := value.(type) {
			case string:
				sanitizedData[sanitizedKey] = h.sanitizer.SanitizeString(v)
			case map[string]interface{}:
				sanitizedData[sanitizedKey] = h.sanitizer.SanitizeInput(v)
			case []interface{}:
				sanitizedArray := make([]interface{}, len(v))
				for i, item := range v {
					if str, ok := item.(string); ok {
						sanitizedArray[i] = h.sanitizer.SanitizeString(str)
					} else {
						sanitizedArray[i] = item
					}
				}
				sanitizedData[sanitizedKey] = sanitizedArray
			default:
				sanitizedData[sanitizedKey] = v
			}
		}
		input.ApplicationData = sanitizedData
	}

	// ===== STEP 2: CHECK FOR DUPLICATE APPLICATION =====
	exists, existingAppID, err := h.idempotencyChecker.CheckApplicationExists(ctx, input.SeekerID, input.FranchiseID)
	if err != nil {
		h.logger.Error("Failed to check existing application", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, err
	}

	if exists {
		h.logger.Warn("Duplicate application detected", map[string]interface{}{
			"existingApplicationId": existingAppID,
			"seekerId":              input.SeekerID,
			"franchiseId":           input.FranchiseID,
		})

		// Return existing application
		var createdAt time.Time
		err = h.db.QueryRowContext(ctx, `SELECT created_at FROM franchise_applications WHERE id = $1`, existingAppID).Scan(&createdAt)
		if err != nil {
			return nil, err
		}

		return &Output{
			ApplicationID:     existingAppID,
			ApplicationStatus: "submitted",
			CreatedAt:         createdAt.Format(time.RFC3339),
		}, nil
	}

	// ===== STEP 3: BEGIN TRANSACTION =====
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseInsertFailed, err)
	}
	defer tx.Rollback()

	// Generate unique application ID and timestamp
	appID := uuid.New().String()
	createdAt := time.Now().UTC().Format(time.RFC3339)

	// Serialize application_data to JSON
	applicationDataJSON, err := json.Marshal(input.ApplicationData)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to marshal application data: %v", ErrDatabaseInsertFailed, err)
	}

	// ===== STEP 4: INSERT WITH ON CONFLICT =====
	insertQuery := `
        INSERT INTO franchise_applications (
            id, seeker_id, franchise_id, application_data, 
            status, idempotency_key, submitted_at, created_at, updated_at
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
        ON CONFLICT (seeker_id, franchise_id) 
        WHERE status IN ('submitted', 'under_review')
        DO NOTHING
        RETURNING id`

	var returnedID string
	err = tx.QueryRowContext(ctx, insertQuery,
		appID,
		input.SeekerID,
		input.FranchiseID,
		applicationDataJSON,
		"submitted",
		idempotencyKey,
		time.Now(),
		time.Now(),
		time.Now(),
	).Scan(&returnedID)

	if err == sql.ErrNoRows {
		// Concurrent creation detected
		h.logger.Warn("Concurrent application creation detected", map[string]interface{}{
			"seekerId":    input.SeekerID,
			"franchiseId": input.FranchiseID,
		})

		// Query existing application
		var existingID string
		queryErr := h.db.QueryRowContext(ctx, `
            SELECT id FROM franchise_applications
            WHERE seeker_id = $1 AND franchise_id = $2
            AND status IN ('submitted', 'under_review')
            LIMIT 1
        `, input.SeekerID, input.FranchiseID).Scan(&existingID)

		if queryErr == nil {
			return &Output{
				ApplicationID:     existingID,
				ApplicationStatus: "submitted",
				CreatedAt:         createdAt,
			}, nil
		}

		return nil, fmt.Errorf("%w: concurrent conflict", ErrDuplicateApplication)
	}

	if err != nil {
		return nil, fmt.Errorf("%w: insert failed: %v", ErrDatabaseInsertFailed, err)
	}

	// ===== STEP 5: UPDATE FRANCHISE STATS =====
	_, err = tx.ExecContext(ctx, `
        UPDATE franchise_stats
        SET enquiry_count = enquiry_count + 1, updated_at = $1
        WHERE franchise_id = $2
    `, time.Now(), input.FranchiseID)

	if err != nil {
		h.logger.Warn("Failed to update franchise stats", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// ===== STEP 6: INSERT AUDIT LOG =====
	_, err = tx.ExecContext(ctx, `
        INSERT INTO application_history (id, application_id, old_status, new_status, notes, created_at)
        VALUES ($1, $2, $3, $4, $5, $6)`,
		uuid.New().String(),
		appID,
		nil,
		"submitted",
		fmt.Sprintf("Application created - Priority: %s", input.Priority),
		time.Now(),
	)

	if err != nil {
		h.logger.Warn("Audit log insert failed", map[string]interface{}{"error": err})
	}

	// ===== STEP 7: COMMIT TRANSACTION =====
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit transaction: %v", ErrDatabaseInsertFailed, err)
	}

	h.logger.Info("application record created", map[string]interface{}{
		"applicationId": appID,
		"seekerId":      input.SeekerID,
		"franchiseId":   input.FranchiseID,
	})

	return &Output{
		ApplicationID:     appID,
		ApplicationStatus: "submitted",
		CreatedAt:         createdAt,
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
	} else {
		h.logger.Info("job completed successfully", map[string]interface{}{
			"jobKey":  job.Key,
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}
}

func (h *Handler) failJob(ctx context.Context, client worker.JobClient, job entities.Job, errorCode, errorMessage string, retries int32) {
	stdErr := &appErrs.StandardError{
		Code:      appErrs.ErrorCode(errorCode),
		Message:   errorMessage,
		Retryable: retries > 0,
	}
	h.errorHandler.HandleJobError(ctx, client, job, stdErr)
}

// Public Execute method for direct API calls
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// Validate before execution
	if err := h.validateInput(input); err != nil {
		return nil, err
	}

	// Generate idempotency key
	idempotencyKey := h.generateIdempotencyKey(input)
	return h.execute(ctx, input, idempotencyKey)
}
