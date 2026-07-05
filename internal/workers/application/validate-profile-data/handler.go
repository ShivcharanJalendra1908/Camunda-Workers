package validateprofiledata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "validate-profile-data"
)

var (
	validTimezones = map[string]bool{
		"UTC": true, "Asia/Kolkata": true, "Asia/Dubai": true, "America/New_York": true,
		"America/Los_Angeles": true, "Europe/London": true, "Europe/Berlin": true,
		"Asia/Tokyo": true, "Australia/Sydney": true, "Pacific/Auckland": true,
	}

	validThemes = map[string]bool{
		"light": true, "dark": true, "system": true,
	}

	validLanguages = map[string]bool{
		"en": true, "hi": true, "ar": true, "fr": true, "es": true, "de": true,
	}
)

type Handler struct {
	logger       logger.Logger
	errorHandler *appErrs.ErrorHandler
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	return &Handler{
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: appErrs.NewErrorHandler(log),
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
	})

	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "validate-profile-data.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewValidationError("input", fmt.Sprintf("parse input: %v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "validate-profile-data.validateInput")
	output := h.validate(&input)
	spanValidate.End()

	if !output.IsValid {
		span.SetAttributes(attribute.Bool("validation.failed", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewValidationError(output.ErrorCode, output.ErrorMessage))
		return
	}

	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "validate-profile-data.completeJob")
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		span.RecordError(err)
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error": err,
		})
		spanComp.End()
		return
	}
	_, err = cmd.Send(ctx)
	if err != nil {
		span.RecordError(err)
		h.logger.Error("failed to complete job", map[string]interface{}{
			"error": err,
		})
	}
	spanComp.End()
}

func (h *Handler) validate(input *Input) *Output {
	// Validate action is required
	if input.Action == "" {
		return &Output{
			IsValid:      false,
			ErrorCode:    "VALIDATION_ERROR",
			ErrorMessage: "action is required",
		}
	}

	// Validate userId is required
	if input.UserId == "" {
		return &Output{
			IsValid:      false,
			ErrorCode:    "VALIDATION_ERROR",
			ErrorMessage: "userId is required",
		}
	}

	// Validate UUID format
	uuidRegex := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	if !uuidRegex.MatchString(strings.ToLower(input.UserId)) {
		return &Output{
			IsValid:      false,
			ErrorCode:    "VALIDATION_ERROR",
			ErrorMessage: "userId must be a valid UUID",
		}
	}

	// Route to action-specific validation
	switch input.Action {
	case "retrieve":
		return h.validateRetrieve(input)
	case "update_personal":
		return h.validateUpdatePersonal(input)
	case "update_professional":
		return h.validateUpdateProfessional(input)
	case "update_company":
		return h.validateUpdateCompany(input)
	case "update_investment":
		return h.validateUpdateInvestment(input)
	case "update_preferences":
		return h.validateUpdatePreferences(input)
	case "delete_account":
		return h.validateDeleteAccount(input)
	default:
		return &Output{
			IsValid:      false,
			ErrorCode:    "VALIDATION_ERROR",
			ErrorMessage: fmt.Sprintf("unsupported action: %s", input.Action),
		}
	}
}

func (h *Handler) validateRetrieve(input *Input) *Output {
	// profileData can be nil for retrieve
	return &Output{
		IsValid:       true,
		ValidatedData: input.ProfileData,
	}
}

func (h *Handler) validateUpdatePersonal(input *Input) *Output {
	pd := input.ProfileData
	if len(pd) == 0 {
		return &Output{
			IsValid:      false,
			ErrorCode:    "VALIDATION_ERROR",
			ErrorMessage: "at least one field is required for update_personal",
		}
	}

	if v, ok := pd["name"].(string); ok {
		if len(v) > 500 {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "name must be max 500 characters"}
		}
	}
	if v, ok := pd["phone"].(string); ok {
		if len(v) > 100 {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "phone must be max 100 characters"}
		}
		// E.164 basic check
		if !regexp.MustCompile(`^\+?[1-9]\d{1,14}$`).MatchString(v) {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "phone must be valid E.164 format"}
		}
	}
	if v, ok := pd["location"].(string); ok {
		if len(v) > 255 {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "location must be max 255 characters"}
		}
	}

	return &Output{IsValid: true, ValidatedData: pd}
}

func (h *Handler) validateUpdateProfessional(input *Input) *Output {
	pd := input.ProfileData
	if len(pd) == 0 {
		return &Output{
			IsValid:      false,
			ErrorCode:    "VALIDATION_ERROR",
			ErrorMessage: "at least one field is required for update_professional",
		}
	}

	if v, ok := pd["occupation"].(string); ok {
		if len(v) > 255 {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "occupation must be max 255 characters"}
		}
	}
	if v, ok := pd["designation"].(string); ok {
		if len(v) > 255 {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "designation must be max 255 characters"}
		}
	}
	if v, ok := pd["experience"].(string); ok {
		if len(v) > 50 {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "experience must be max 50 characters"}
		}
	}

	return &Output{IsValid: true, ValidatedData: pd}
}

func (h *Handler) validateUpdateCompany(input *Input) *Output {
	pd := input.ProfileData
	if len(pd) == 0 {
		return &Output{
			IsValid:      false,
			ErrorCode:    "VALIDATION_ERROR",
			ErrorMessage: "at least one field is required for update_company",
		}
	}

	if v, ok := pd["business_name"].(string); ok {
		if len(v) > 500 {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "business_name must be max 500 characters"}
		}
	}
	if v, ok := pd["company_website"].(string); ok && v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "company_website must be a valid URL"}
		}
	}
	if v, ok := pd["year_established"].(float64); ok {
		if v < 1800 || v > float64(time.Now().Year()) {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "year_established must be between 1800 and current year"}
		}
	}

	return &Output{IsValid: true, ValidatedData: pd}
}

func (h *Handler) validateUpdateInvestment(input *Input) *Output {
	pd := input.ProfileData
	if len(pd) == 0 {
		return &Output{
			IsValid:      false,
			ErrorCode:    "VALIDATION_ERROR",
			ErrorMessage: "at least one field is required for update_investment",
		}
	}

	return &Output{IsValid: true, ValidatedData: pd}
}

func (h *Handler) validateUpdatePreferences(input *Input) *Output {
	pd := input.ProfileData
	if len(pd) == 0 {
		return &Output{
			IsValid:      false,
			ErrorCode:    "VALIDATION_ERROR",
			ErrorMessage: "at least one field is required for update_preferences",
		}
	}

	if v, ok := pd["theme"].(string); ok {
		if !validThemes[v] {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "theme must be light, dark, or system"}
		}
	}
	if v, ok := pd["language"].(string); ok {
		if !validLanguages[v] {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "language must be a supported language code"}
		}
	}
	if v, ok := pd["timezone"].(string); ok {
		if !validTimezones[v] {
			return &Output{IsValid: false, ErrorCode: "VALIDATION_ERROR", ErrorMessage: "timezone must be a valid IANA timezone"}
		}
	}

	return &Output{IsValid: true, ValidatedData: pd}
}

func (h *Handler) validateDeleteAccount(input *Input) *Output {
	// profileData is optional for delete_account (reason)
	return &Output{IsValid: true, ValidatedData: input.ProfileData}
}
