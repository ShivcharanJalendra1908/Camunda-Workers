package checkreadinessscore

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "check-readiness-score"
)

type Handler struct {
	logger       logger.Logger
	errorHandler *errors.ErrorHandler
	validator    *validation.Validator // ✅ ADDED
	sanitizer    *validation.Sanitizer // ✅ ADDED
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	return &Handler{
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
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "check-readiness-score.parseInput")
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
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "check-readiness-score.validateInput")
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

	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "check-readiness-score.Execute")
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
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "check-readiness-score.completeJob")
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
		return errors.NewInvalidUUIDError("userId", input.UserID)
	}

	// Validate ApplicationData (if provided)
	if input.ApplicationData != nil {
		if err := h.validateApplicationData(input.ApplicationData); err != nil {
			return err
		}
	}

	// 🔒 ADDITIONAL SECURITY CHECKS
	// Prevent script injection in string fields
	if input.ApplicationData != nil {
		if err := h.validateNoScriptInjection(input.ApplicationData); err != nil {
			return err
		}
	}

	// Sanitize input
	input.UserID = h.sanitizer.SanitizeString(input.UserID)

	return nil
}

// ===== HELPER: Application Data Validation =====
func (h *Handler) validateApplicationData(data map[string]interface{}) error {
	// Limit data size
	if len(data) > 100 {
		return errors.NewValidationError("applicationData", "cannot exceed 100 fields")
	}

	// Validate each field
	for key, value := range data {
		// Validate key
		if err := ozzo.Validate(key,
			ozzo.Length(1, 100).Error("field name must be 1-100 characters"),
			validation.IDString,
			validation.SafeSQLString,
			validation.SafeNoSQLString,
		); err != nil {
			return errors.NewValidationError(fmt.Sprintf("applicationData.%s", key), "invalid field name: "+err.Error())
		}

		// Validate value based on type
		switch v := value.(type) {
		case string:
			if len(v) > 1000 {
				return errors.NewValidationError(fmt.Sprintf("applicationData.%s", key),
					"string value cannot exceed 1000 characters")
			}

			// Check for script injection in strings
			if strings.Contains(strings.ToLower(v), "javascript:") ||
				strings.Contains(strings.ToLower(v), "script:") ||
				strings.Contains(strings.ToLower(v), "data:") ||
				strings.Contains(strings.ToLower(v), "<script") {
				return errors.NewValidationError(fmt.Sprintf("applicationData.%s", key),
					"value contains potentially unsafe content")
			}

		case float64:
			// Validate numeric ranges for known financial fields
			if strings.Contains(strings.ToLower(key), "capital") ||
				strings.Contains(strings.ToLower(key), "worth") ||
				strings.Contains(strings.ToLower(key), "score") ||
				strings.Contains(strings.ToLower(key), "amount") {

				// Prevent negative values for financial data
				if v < 0 {
					return errors.NewValidationError(fmt.Sprintf("applicationData.%s", key),
						"cannot be negative")
				}

				// Prevent unreasonably large values (billion limit)
				if v > 1000000000 { // 1 billion
					return errors.NewValidationError(fmt.Sprintf("applicationData.%s", key),
						"value too large (max 1,000,000,000)")
				}
			}

		case bool:
			// Boolean values are fine
			continue

		case []interface{}:
			if len(v) > 100 {
				return errors.NewValidationError(fmt.Sprintf("applicationData.%s", key),
					"array cannot exceed 100 items")
			}

		case map[string]interface{}:
			// Prevent deep nesting (max 3 levels)
			if err := h.validateNestedObjectDepth(v, 1, fmt.Sprintf("applicationData.%s", key)); err != nil {
				return err
			}

		default:
			// Reject unknown types
			return errors.NewValidationError(fmt.Sprintf("applicationData.%s", key),
				fmt.Sprintf("unsupported type: %T", v))
		}
	}

	return nil
}

// ===== HELPER: Nested Object Depth Validation =====
func (h *Handler) validateNestedObjectDepth(data map[string]interface{}, currentDepth int, fieldPath string) error {
	if currentDepth > 3 {
		return errors.NewValidationError(fieldPath, "object nesting too deep (max 3 levels)")
	}

	for key, value := range data {
		if err := ozzo.Validate(key,
			ozzo.Length(1, 100).Error("field name must be 1-100 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError(fmt.Sprintf("%s.%s", fieldPath, key),
				"invalid field name: "+err.Error())
		}

		if nestedMap, ok := value.(map[string]interface{}); ok {
			if err := h.validateNestedObjectDepth(nestedMap, currentDepth+1,
				fmt.Sprintf("%s.%s", fieldPath, key)); err != nil {
				return err
			}
		}
	}

	return nil
}

// ===== HELPER: Script Injection Prevention =====
func (h *Handler) validateNoScriptInjection(data map[string]interface{}) error {
	for key, value := range data {
		switch v := value.(type) {
		case string:
			// Check for script injection patterns
			dangerousPatterns := []string{
				"javascript:", "script:", "data:text/html", "data:text/javascript",
				"onload=", "onerror=", "onclick=", "onmouseover=",
				"eval(", "alert(", "document.cookie", "window.location",
				"<script", "</script>", "<iframe", "</iframe>", "<object",
			}

			lowerValue := strings.ToLower(v)
			for _, pattern := range dangerousPatterns {
				if strings.Contains(lowerValue, pattern) {
					return errors.NewValidationError(fmt.Sprintf("applicationData.%s", key),
						"contains potentially unsafe script content")
				}
			}

		case map[string]interface{}:
			// Recursively check nested objects
			if err := h.validateNoScriptInjection(v); err != nil {
				return err
			}

		case []interface{}:
			// Check arrays
			for i, item := range v {
				if str, ok := item.(string); ok {
					lowerStr := strings.ToLower(str)
					if strings.Contains(lowerStr, "javascript:") ||
						strings.Contains(lowerStr, "<script") {
						return errors.NewValidationError(fmt.Sprintf("applicationData.%s[%d]", key, i),
							"contains potentially unsafe content")
					}
				}
			}
		}
	}

	return nil
}

// ===== ORIGINAL EXECUTE METHOD (with sanitization added) =====
func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	// 🔒 Sanitize all string fields in application data
	sanitizedData := h.sanitizeApplicationData(input.ApplicationData)

	data := sanitizedData
	if data == nil {
		data = make(map[string]interface{})
	}

	financial := h.calculateFinancialReadiness(data)
	experience := h.calculateExperience(data)
	commitment := h.calculateCommitment(data)
	compatibility := h.calculateCompatibility(data)

	// Calculate weighted average: Financial(30%) + Experience(25%) + Commitment(20%) + Compatibility(25%)
	finalScore := int(
		float64(financial)*0.30 +
			float64(experience)*0.25 +
			float64(commitment)*0.20 +
			float64(compatibility)*0.25)

	level := h.classifyQualificationLevel(finalScore)

	breakdown := ScoreBreakdown{
		Financial:     financial,
		Experience:    experience,
		Commitment:    commitment,
		Compatibility: compatibility,
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.Int("readiness_score.financial", financial),
		attribute.Int("readiness_score.experience", experience),
		attribute.Int("readiness_score.commitment", commitment),
		attribute.Int("readiness_score.compatibility", compatibility),
		attribute.Int("readiness_score.final", finalScore),
		attribute.String("readiness_score.level", level),
	)

	h.logger.Info("readiness score calculated", map[string]interface{}{
		"userId":    input.UserID,
		"score":     finalScore,
		"level":     level,
		"breakdown": breakdown,
		"traceId":   span.SpanContext().TraceID().String(),
	})

	return &Output{
		ReadinessScore:     finalScore,
		QualificationLevel: level,
		ScoreBreakdown:     breakdown,
	}, nil
}

// ===== HELPER: Sanitize Application Data =====
func (h *Handler) sanitizeApplicationData(data map[string]interface{}) map[string]interface{} {
	if data == nil {
		return nil
	}

	sanitized := make(map[string]interface{})

	for key, value := range data {
		sanitizedKey := h.sanitizer.SanitizeString(key)

		switch v := value.(type) {
		case string:
			sanitized[sanitizedKey] = h.sanitizer.SanitizeString(v)

		case map[string]interface{}:
			// Recursively sanitize nested objects
			sanitized[sanitizedKey] = h.sanitizeApplicationData(v)

		case []interface{}:
			sanitizedArray := make([]interface{}, len(v))
			for i, item := range v {
				if str, ok := item.(string); ok {
					sanitizedArray[i] = h.sanitizer.SanitizeString(str)
				} else {
					sanitizedArray[i] = item
				}
			}
			sanitized[sanitizedKey] = sanitizedArray

		default:
			sanitized[sanitizedKey] = value
		}
	}

	return sanitized
}

// ===== ORIGINAL HELPER METHODS (remain the same) =====
func (h *Handler) calculateFinancialReadiness(data map[string]interface{}) int {
	var financialData map[string]interface{}
	if fi, ok := data["financialInfo"]; ok {
		if fiMap, ok := fi.(map[string]interface{}); ok {
			financialData = fiMap
		} else {
			financialData = data
		}
	} else {
		financialData = data
	}

	capital := 0
	if capRaw, ok := financialData["liquidCapital"]; ok {
		if capInt, err := h.parseInt(capRaw); err == nil && capInt >= 0 {
			capital = capInt
		}
	}

	netWorth := 0
	if nwRaw, ok := financialData["netWorth"]; ok {
		if nwInt, err := h.parseInt(nwRaw); err == nil && nwInt >= 0 {
			netWorth = nwInt
		}
	}

	creditScore := 0
	if csRaw, ok := financialData["creditScore"]; ok {
		if csInt, err := h.parseInt(csRaw); err == nil {
			// Clamp credit score to valid range (300-850 per FICO standard)
			creditScore = h.clamp(csInt, 300, 850)
		}
	}

	score := 0

	// Liquid capital scoring (max 40 points) - NO points below 100k
	if capital >= 1000000 {
		score += 40
	} else if capital >= 500000 {
		score += 30
	} else if capital >= 250000 {
		score += 20
	} else if capital >= 100000 {
		score += 10
	}

	// Net worth scoring (max 30 points) - NO points below 500k
	if netWorth >= 2000000 {
		score += 30
	} else if netWorth >= 1000000 {
		score += 20
	} else if netWorth >= 500000 {
		score += 10
	}

	// Credit score scoring (max 30 points)
	if creditScore >= 700 {
		score += 30
	} else if creditScore >= 600 {
		score += 20
	} else if creditScore >= 500 {
		score += 10
	}

	return h.clamp(score, 0, 100)
}

func (h *Handler) calculateExperience(data map[string]interface{}) int {
	var experienceData map[string]interface{}
	if exp, ok := data["experience"]; ok {
		if expMap, ok := exp.(map[string]interface{}); ok {
			experienceData = expMap
		} else {
			experienceData = data
		}
	} else {
		experienceData = data
	}

	years := 0
	if yRaw, ok := experienceData["yearsInIndustry"]; ok {
		if yInt, err := h.parseInt(yRaw); err == nil && yInt >= 0 {
			years = yInt
		}
	}

	mgmt := false
	if mRaw, ok := experienceData["managementExperience"]; ok {
		mgmt, _ = mRaw.(bool)
	}

	bizOwner := false
	if bRaw, ok := experienceData["businessOwnership"]; ok {
		bizOwner, _ = bRaw.(bool)
	}

	score := 0

	// Years of experience (max 40 points)
	if years >= 10 {
		score += 40
	} else if years >= 5 {
		score += 30
	} else if years >= 2 {
		score += 20
	} else if years >= 1 {
		score += 10
	}

	// Management experience (30 points)
	if mgmt {
		score += 30
	}

	// Business ownership (30 points)
	if bizOwner {
		score += 30
	}

	return h.clamp(score, 0, 100)
}

func (h *Handler) calculateCommitment(data map[string]interface{}) int {
	timeAvail := 0
	if tRaw, ok := data["timeAvailability"]; ok {
		if tInt, err := h.parseInt(tRaw); err == nil && tInt >= 0 {
			timeAvail = tInt
		}
	}

	relocation := false
	if rRaw, ok := data["relocationWilling"]; ok {
		relocation, _ = rRaw.(bool)
	}

	score := 0

	// Time availability (max 50 points)
	if timeAvail >= 40 {
		score += 50
	} else if timeAvail >= 20 {
		score += 30
	} else if timeAvail >= 10 {
		score += 10
	}

	// Relocation willingness (50 points)
	if relocation {
		score += 50
	}

	return h.clamp(score, 0, 100)
}

func (h *Handler) calculateCompatibility(data map[string]interface{}) int {
	categoryMatch := false
	if cRaw, ok := data["categoryMatch"]; ok {
		categoryMatch, _ = cRaw.(bool)
	}

	skillMatch := false
	if sRaw, ok := data["skillAlignment"]; ok {
		skillMatch, _ = sRaw.(bool)
	}

	locationMatch := false
	if lRaw, ok := data["locationMatch"]; ok {
		locationMatch, _ = lRaw.(bool)
	}

	score := 0

	// Category match (40 points)
	if categoryMatch {
		score += 40
	}

	// Skill alignment (30 points)
	if skillMatch {
		score += 30
	}

	// Location match (30 points)
	if locationMatch {
		score += 30
	}

	return h.clamp(score, 0, 100)
}

func (h *Handler) classifyQualificationLevel(score int) string {
	switch {
	case score >= 81:
		return "excellent"
	case score >= 61:
		return "high"
	case score >= 41:
		return "medium"
	default:
		return "low"
	}
}

func (h *Handler) parseInt(raw interface{}) (int, error) {
	switch v := raw.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case string:
		cleaned := strings.ReplaceAll(v, ",", "")
		cleaned = strings.TrimSpace(cleaned)
		return strconv.Atoi(cleaned)
	default:
		return 0, fmt.Errorf("not a number: %T", raw)
	}
}

func (h *Handler) clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
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
			"traceId": span.SpanContext().TraceID().String(),
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
