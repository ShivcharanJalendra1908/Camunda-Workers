package selecttemplate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "select-template"
)

type Handler struct {
	config       *Config
	logger       logger.Logger
	errorHandler *errors.ErrorHandler
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	return &Handler{
		config:       config,
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: errors.NewErrorHandler(log),
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

	h.logger.Info("processing job",
		map[string]interface{}{
			"jobKey":      job.Key,
			"workflowKey": job.ProcessInstanceKey,
			"traceId":     traceID,
			"spanId":      span.SpanContext().SpanID().String(),
		})

	// ===== STEP 1: PARSE INPUT =====
	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "select-template.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			errors.NewBusinessRuleError("Parse input failed", fmt.Sprintf("%v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== STEP 2: VALIDATE INPUT =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "select-template.validateInput")
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

	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "select-template.Execute")
	output, err := h.Execute(ctxExec, &input)
	spanExec.End()

	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		var stdErr *errors.StandardError
		// Map config/template errors to standardized constructors
		if err.Error() == "missing route template rules in config" {
			stdErr = errors.NewTemplateNotFoundError("route-rules")
		} else {
			stdErr = errors.NewTemplateValidationFailedError(err.Error())
		}
		h.errorHandler.HandleJobError(ctx, client, job, stdErr)
		return
	}

	// ===== STEP 4: COMPLETE JOB =====
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "select-template.completeJob")
	h.completeJob(ctx, client, job, output)
	spanComp.End()
}

// ===== CRITICAL VALIDATION FUNCTION =====
func (h *Handler) validateInput(input *Input) error {
	// Validate SubscriptionTier (if provided)
	if input.SubscriptionTier != "" {
		if err := ozzo.Validate(input.SubscriptionTier,
			ozzo.In("free", "premium", "enterprise").Error("must be one of: free, premium, enterprise"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("subscriptionTier", err.Error())
		}
	}

	// Validate RoutePath (if provided)
	if input.RoutePath != "" {
		if err := ozzo.Validate(input.RoutePath,
			ozzo.Length(1, 200).Error("must be 1-200 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("routePath", err.Error())
		}
	}

	// Validate TemplateType (if provided)
	if input.TemplateType != "" {
		if err := ozzo.Validate(input.TemplateType,
			ozzo.In("ai-response", "email", "notification", "document").Error("must be one of: ai-response, email, notification, document"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("templateType", err.Error())
		}
	}

	// Validate Confidence (if provided and TemplateType is ai-response)
	if input.TemplateType == "ai-response" {
		if input.Confidence < 0 || input.Confidence > 1 {
			return errors.NewValidationError("confidence", "must be between 0 and 1")
		}
	}

	// 🔒 ADDITIONAL SECURITY CHECKS

	// Prevent script injection in RoutePath
	if containsDangerousPatterns(input.RoutePath) {
		return errors.NewValidationError("routePath", "contains potentially unsafe content")
	}

	// Prevent script injection in TemplateType
	if containsDangerousPatterns(input.TemplateType) {
		return errors.NewValidationError("templateType", "contains potentially unsafe content")
	}

	// Prevent SQL injection in SubscriptionTier
	if containsSQLPatterns(input.SubscriptionTier) {
		return errors.NewValidationError("subscriptionTier", "contains potentially unsafe SQL patterns")
	}

	return nil
}

// Helper function to check for dangerous patterns
func containsDangerousPatterns(s string) bool {
	if s == "" {
		return false
	}

	dangerousPatterns := []string{
		"<script", "</script>", "javascript:", "data:text/html",
		"onload=", "onerror=", "onclick=", "eval(", "alert(",
	}

	lower := s
	for _, pattern := range dangerousPatterns {
		if containsIgnoreCase(lower, pattern) {
			return true
		}
	}
	return false
}

// Helper function to check for SQL patterns
func containsSQLPatterns(s string) bool {
	if s == "" {
		return false
	}

	sqlPatterns := []string{
		"' OR '1'='1", "' OR '1'='1' --", "'; DROP TABLE", "UNION SELECT",
		"INSERT INTO", "UPDATE", "DELETE FROM", "EXEC", "EXECUTE",
	}

	lower := strings.ToLower(s)
	for _, pattern := range sqlPatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// Helper function for case-insensitive contains
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// Execute with sanitization added
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// 🔒 Sanitize input before processing
	input = h.sanitizeInput(input)

	var selected string

	// Route-based selection
	if input.RoutePath != "" {
		routeRules, exists := h.config.TemplateRules["route"]
		if !exists {
			return nil, fmt.Errorf("missing route template rules in config")
		}

		routeKey := input.RoutePath
		switch input.SubscriptionTier {
		case "free":
			routeKey += ":free"
		case "premium":
			routeKey += ":premium"
		default:
			// Unknown tier, try fallback
			routeKey += ":fallback"
		}

		if template, ok := routeRules[routeKey]; ok {
			selected = template
			h.logger.Info("selected route template",
				map[string]interface{}{
					"route":    input.RoutePath,
					"tier":     input.SubscriptionTier,
					"template": selected,
					"traceId":  trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
				})
			return &Output{SelectedTemplateId: selected}, nil
		}
	}

	// Fallback: try generic route
	if input.RoutePath != "" {
		fallbackKey := input.RoutePath + ":fallback"
		if template, ok := h.config.TemplateRules["route"][fallbackKey]; ok {
			selected = template
			h.logger.Warn("used fallback template",
				map[string]interface{}{
					"route":    input.RoutePath,
					"template": selected,
					"traceId":  trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
				})
			return &Output{SelectedTemplateId: selected}, nil
		}
	}

	// Confidence-based selection (AI responses)
	if input.TemplateType == "ai-response" {
		// Clamp negative confidence to 0
		confidence := input.Confidence
		if confidence < 0 {
			confidence = 0
		}

		if confidence >= 0.8 {
			selected = "ai-detailed"
		} else {
			selected = "ai-tentative"
		}
		h.logger.Info("selected AI template",
			map[string]interface{}{
				"confidence": input.Confidence,
				"template":   selected,
				"traceId":    trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
			})
		return &Output{SelectedTemplateId: selected}, nil
	}

	// Final fallback
	selected = "default-template"
	h.logger.Warn("used default fallback template",
		map[string]interface{}{
			"input":   input,
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	return &Output{SelectedTemplateId: selected}, nil
}

// Sanitize all input fields
func (h *Handler) sanitizeInput(input *Input) *Input {
	// Create a sanitized copy
	sanitized := &Input{
		SubscriptionTier: h.sanitizer.SanitizeString(input.SubscriptionTier),
		RoutePath:        h.sanitizer.SanitizeString(input.RoutePath),
		TemplateType:     h.sanitizer.SanitizeString(input.TemplateType),
		Confidence:       input.Confidence,
	}

	return sanitized
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to create complete job command",
			map[string]interface{}{
				"error":   err.Error(),
				"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
			})
		return
	}

	_, err = cmd.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to send complete job command",
			map[string]interface{}{
				"error":   err.Error(),
				"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
			})
	}
}
