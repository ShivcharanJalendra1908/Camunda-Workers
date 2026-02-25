package validateapplicationdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "validate-application-data"
)

var (
	ErrApplicationValidationFailed = errors.New("APPLICATION_VALIDATION_FAILED")

	// Regex patterns
	nameRegex  = regexp.MustCompile(`^[a-zA-Z\s\-']{2,100}$`)
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	phoneRegex = regexp.MustCompile(`^\+?[1-9]\d{1,14}$`)
)

type Handler struct {
	logger       logger.Logger
	errorHandler *appErrs.ErrorHandler
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	return &Handler{
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: appErrs.NewErrorHandler(log),
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

	// ===== STEP 1: PARSE INPUT =====
	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "validate-application-data.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewApplicationValidationFailedError(fmt.Sprintf("parse input: %v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== STEP 2: VALIDATE INPUT (GAP #1 FIX) =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "validate-application-data.validateInput")
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

	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "validate-application-data.Execute")
	output, err := h.execute(ctxExec, &input)
	spanExec.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, appErrs.NewApplicationValidationFailedError(err.Error()))
		return
	}

	// ===== STEP 4: COMPLETE JOB =====
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "validate-application-data.completeJob")
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		span.RecordError(err)
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error":   err,
			"traceId": traceID,
		})
		spanComp.End()
		return
	}
	_, err = cmd.Send(ctx)
	if err != nil {
		span.RecordError(err)
		h.logger.Error("failed to complete job", map[string]interface{}{
			"error":   err,
			"traceId": traceID,
		})
	}
	spanComp.End()
}

// ===== CRITICAL VALIDATION FUNCTION (GAP #1 FIX) =====
func (h *Handler) validateInput(input *Input) error {
	// Validate FranchiseID (UUID format)
	if input.FranchiseID != "" {
		if err := ozzo.Validate(input.FranchiseID,
			validation.IsUUID,
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewInvalidUUIDError("franchiseId", input.FranchiseID)
		}
	} else {
		return appErrs.NewRequiredFieldError("franchiseId")
	}

	// Validate ApplicationData is not empty
	// ✅ Simplified - covers both nil and empty cases
	if len(input.ApplicationData) == 0 {
		return appErrs.NewValidationError("applicationData", "cannot be empty")
	}

	// 🔒 ADDITIONAL SECURITY: Validate ApplicationData structure
	if err := h.validateApplicationDataStructure(input.ApplicationData); err != nil {
		return err
	}

	// 🔒 SECURITY: Check for SQL injection patterns in string values
	if err := h.checkForInjectionPatterns(input.ApplicationData); err != nil {
		return err
	}

	return nil
}

// ===== ORIGINAL EXECUTE METHOD (with ozzo enhanced validation) =====
func (h *Handler) execute(_ context.Context, input *Input) (*Output, error) {
	validated := make(map[string]interface{})
	var validationErrors []ValidationError

	// 🔒 Sanitize input before processing
	sanitizedData := h.sanitizer.SanitizeInput(input.ApplicationData)

	// Validate personal info
	if personalRaw, ok := sanitizedData["personalInfo"]; ok {
		if personalMap, ok := personalRaw.(map[string]interface{}); ok {
			validatedPersonal, personalErrors := h.validatePersonalInfo(personalMap)
			validated["personalInfo"] = validatedPersonal
			validationErrors = append(validationErrors, personalErrors...)
		}
	} else {
		validationErrors = append(validationErrors, ValidationError{
			Field:   "personalInfo",
			Code:    "MISSING_REQUIRED",
			Message: "personalInfo is required",
		})
	}

	// Validate financial info
	if financialRaw, ok := sanitizedData["financialInfo"]; ok {
		if financialMap, ok := financialRaw.(map[string]interface{}); ok {
			validatedFinancial, financialErrors := h.validateFinancialInfo(financialMap, input.FranchiseID)
			validated["financialInfo"] = validatedFinancial
			validationErrors = append(validationErrors, financialErrors...)
		}
	} else {
		validationErrors = append(validationErrors, ValidationError{
			Field:   "financialInfo",
			Code:    "MISSING_REQUIRED",
			Message: "financialInfo is required",
		})
	}

	// Validate experience
	if experienceRaw, ok := sanitizedData["experience"]; ok {
		if experienceMap, ok := experienceRaw.(map[string]interface{}); ok {
			validatedExperience, experienceErrors := h.validateExperience(experienceMap)
			validated["experience"] = validatedExperience
			validationErrors = append(validationErrors, experienceErrors...)
		}
	} else {
		validationErrors = append(validationErrors, ValidationError{
			Field:   "experience",
			Code:    "MISSING_REQUIRED",
			Message: "experience is required",
		})
	}

	isValid := len(validationErrors) == 0
	h.logger.Info("validation completed", map[string]interface{}{
		"isValid":    isValid,
		"errorCount": len(validationErrors),
	})

	if !isValid {
		return nil, fmt.Errorf("%w: %d validation errors", ErrApplicationValidationFailed, len(validationErrors))
	}

	return &Output{
		IsValid:          true,
		ValidatedData:    validated,
		ValidationErrors: []ValidationError{},
	}, nil
}

// ===== ENHANCED VALIDATION METHODS WITH OZZO =====

func (h *Handler) validatePersonalInfo(data map[string]interface{}) (map[string]interface{}, []ValidationError) {
	validated := make(map[string]interface{})
	errors := []ValidationError{}

	// Name validation with ozzo
	if nameRaw, ok := data["name"]; ok {
		if nameStr, ok := nameRaw.(string); ok {
			// 🔒 Sanitize
			nameStr = h.sanitizer.SanitizeString(nameStr)

			// 🔒 Validate with ozzo
			if err := ozzo.Validate(nameStr,
				ozzo.Length(2, 100).Error("name must be 2-100 characters"),
				validation.SafeSQLString,
				ozzo.Match(nameRegex).Error("name can only contain letters, spaces, hyphens, or apostrophes"),
			); err != nil {
				errors = append(errors, ValidationError{
					Field:   "personalInfo.name",
					Code:    "INVALID_FORMAT",
					Message: err.Error(),
				})
			} else {
				validated["name"] = nameStr
			}
		} else {
			errors = append(errors, ValidationError{
				Field:   "personalInfo.name",
				Code:    "INVALID_TYPE",
				Message: "Name must be a string",
			})
		}
	} else {
		errors = append(errors, ValidationError{
			Field:   "personalInfo.name",
			Code:    "MISSING_REQUIRED",
			Message: "Name is required",
		})
	}

	// Email validation with ozzo
	if emailRaw, ok := data["email"]; ok {
		if emailStr, ok := emailRaw.(string); ok {
			// 🔒 Sanitize
			emailStr = h.sanitizer.SanitizeString(emailStr)

			// 🔒 Validate with ozzo
			if err := ozzo.Validate(emailStr,
				ozzo.Length(5, 255).Error("email must be 5-255 characters"),
				ozzo.Match(emailRegex).Error("invalid email format"),
				validation.SafeSQLString,
			); err != nil {
				errors = append(errors, ValidationError{
					Field:   "personalInfo.email",
					Code:    "INVALID_FORMAT",
					Message: err.Error(),
				})
			} else {
				validated["email"] = emailStr
			}
		} else {
			errors = append(errors, ValidationError{
				Field:   "personalInfo.email",
				Code:    "INVALID_TYPE",
				Message: "Email must be a string",
			})
		}
	} else {
		errors = append(errors, ValidationError{
			Field:   "personalInfo.email",
			Code:    "MISSING_REQUIRED",
			Message: "Email is required",
		})
	}

	// Phone validation with ozzo
	if phoneRaw, ok := data["phone"]; ok {
		if phoneStr, ok := phoneRaw.(string); ok {
			// 🔒 Sanitize - remove all non-digit characters except leading +
			phoneStr = h.sanitizer.SanitizeString(phoneStr)

			// 🔒 Validate with ozzo
			if err := ozzo.Validate(phoneStr,
				ozzo.Length(10, 15).Error("phone must be 10-15 characters"),
				ozzo.Match(phoneRegex).Error("invalid phone format (E.164 recommended: +1234567890)"),
				validation.SafeSQLString,
			); err != nil {
				errors = append(errors, ValidationError{
					Field:   "personalInfo.phone",
					Code:    "INVALID_FORMAT",
					Message: err.Error(),
				})
			} else {
				validated["phone"] = phoneStr
			}
		} else {
			errors = append(errors, ValidationError{
				Field:   "personalInfo.phone",
				Code:    "INVALID_TYPE",
				Message: "Phone must be a string",
			})
		}
	} else {
		errors = append(errors, ValidationError{
			Field:   "personalInfo.phone",
			Code:    "MISSING_REQUIRED",
			Message: "Phone is required",
		})
	}

	return validated, errors
}

func (h *Handler) validateFinancialInfo(data map[string]interface{}, franchiseID string) (map[string]interface{}, []ValidationError) {
	validated := make(map[string]interface{})
	errors := []ValidationError{}

	// Liquid capital validation
	if capitalRaw, ok := data["liquidCapital"]; ok {
		capital, err := h.parseInt(capitalRaw)
		if err != nil {
			errors = append(errors, ValidationError{
				Field:   "financialInfo.liquidCapital",
				Code:    "INVALID_TYPE",
				Message: "Liquid capital must be a number",
			})
		} else {
			// 🔒 Validate with ozzo (numeric validation)
			if err := ozzo.Validate(capital,
				ozzo.Min(0).Error("liquid capital cannot be negative"),
				ozzo.Max(1000000000).Error("liquid capital cannot exceed 1,000,000,000"),
			); err != nil {
				errors = append(errors, ValidationError{
					Field:   "financialInfo.liquidCapital",
					Code:    "INVALID_VALUE",
					Message: err.Error(),
				})
			} else {
				validated["liquidCapital"] = capital

				// Franchise-specific rule
				if rule, exists := franchiseRules[franchiseID]; exists {
					if capital < rule.MinLiquidCapital {
						errors = append(errors, ValidationError{
							Field:   "financialInfo.liquidCapital",
							Code:    "BELOW_MINIMUM",
							Message: fmt.Sprintf("Liquid capital must be at least $%d for this franchise", rule.MinLiquidCapital),
						})
					}
				}
			}
		}
	} else {
		errors = append(errors, ValidationError{
			Field:   "financialInfo.liquidCapital",
			Code:    "MISSING_REQUIRED",
			Message: "Liquid capital is required",
		})
	}

	// Net worth validation
	if netWorthRaw, ok := data["netWorth"]; ok {
		netWorth, err := h.parseInt(netWorthRaw)
		if err != nil {
			errors = append(errors, ValidationError{
				Field:   "financialInfo.netWorth",
				Code:    "INVALID_TYPE",
				Message: "Net worth must be a number",
			})
		} else {
			// 🔒 Validate with ozzo
			if err := ozzo.Validate(netWorth,
				ozzo.Min(0).Error("net worth cannot be negative"),
				ozzo.Max(1000000000).Error("net worth cannot exceed 1,000,000,000"),
			); err != nil {
				errors = append(errors, ValidationError{
					Field:   "financialInfo.netWorth",
					Code:    "INVALID_VALUE",
					Message: err.Error(),
				})
			} else {
				validated["netWorth"] = netWorth

				// Franchise-specific rule
				if rule, exists := franchiseRules[franchiseID]; exists {
					if netWorth < rule.MinNetWorth {
						errors = append(errors, ValidationError{
							Field:   "financialInfo.netWorth",
							Code:    "BELOW_MINIMUM",
							Message: fmt.Sprintf("Net worth must be at least $%d for this franchise", rule.MinNetWorth),
						})
					}
				}
			}
		}
	} else {
		errors = append(errors, ValidationError{
			Field:   "financialInfo.netWorth",
			Code:    "MISSING_REQUIRED",
			Message: "Net worth is required",
		})
	}

	// Credit score validation
	if creditRaw, ok := data["creditScore"]; ok {
		credit, err := h.parseInt(creditRaw)
		if err != nil {
			errors = append(errors, ValidationError{
				Field:   "financialInfo.creditScore",
				Code:    "INVALID_TYPE",
				Message: "Credit score must be a number",
			})
		} else {
			// 🔒 Validate with ozzo
			if err := ozzo.Validate(credit,
				ozzo.Min(300).Error("credit score must be at least 300"),
				ozzo.Max(850).Error("credit score cannot exceed 850"),
			); err != nil {
				errors = append(errors, ValidationError{
					Field:   "financialInfo.creditScore",
					Code:    "INVALID_VALUE",
					Message: err.Error(),
				})
			} else {
				validated["creditScore"] = credit
			}
		}
	} else {
		// Check if required
		if rule, exists := franchiseRules[franchiseID]; exists && rule.RequiresCreditScore {
			errors = append(errors, ValidationError{
				Field:   "financialInfo.creditScore",
				Code:    "MISSING_REQUIRED",
				Message: "Credit score is required for this franchise",
			})
		}
	}

	return validated, errors
}

func (h *Handler) validateExperience(data map[string]interface{}) (map[string]interface{}, []ValidationError) {
	validated := make(map[string]interface{})
	errors := []ValidationError{}

	// Years in industry validation
	if yearsRaw, ok := data["yearsInIndustry"]; ok {
		years, err := h.parseInt(yearsRaw)
		if err != nil {
			errors = append(errors, ValidationError{
				Field:   "experience.yearsInIndustry",
				Code:    "INVALID_TYPE",
				Message: "Years in industry must be a number",
			})
		} else {
			// 🔒 Validate with ozzo
			if err := ozzo.Validate(years,
				ozzo.Min(0).Error("years cannot be negative"),
				ozzo.Max(50).Error("years cannot exceed 50"),
			); err != nil {
				errors = append(errors, ValidationError{
					Field:   "experience.yearsInIndustry",
					Code:    "INVALID_VALUE",
					Message: err.Error(),
				})
			} else {
				validated["yearsInIndustry"] = years
			}
		}
	} else {
		errors = append(errors, ValidationError{
			Field:   "experience.yearsInIndustry",
			Code:    "MISSING_REQUIRED",
			Message: "Years in industry is required",
		})
	}

	// Management experience validation
	if mgmtRaw, ok := data["managementExperience"]; ok {
		if mgmtBool, ok := mgmtRaw.(bool); ok {
			validated["managementExperience"] = mgmtBool
		} else {
			errors = append(errors, ValidationError{
				Field:   "experience.managementExperience",
				Code:    "INVALID_TYPE",
				Message: "Management experience must be a boolean (true/false)",
			})
		}
	} else {
		validated["managementExperience"] = false
	}

	return validated, errors
}

// ===== HELPER METHODS =====

func (h *Handler) parseInt(raw interface{}) (int, error) {
	switch v := raw.(type) {
	case float64:
		return int(v), nil
	case string:
		// 🔒 Sanitize before parsing
		sanitized := h.sanitizer.SanitizeString(v)
		return strconv.Atoi(strings.TrimSpace(sanitized))
	default:
		return 0, fmt.Errorf("not a number")
	}
}

// ===== SECURITY VALIDATION METHODS =====

func (h *Handler) validateApplicationDataStructure(data map[string]interface{}) error {
	// Check data size
	if len(data) > 50 {
		return appErrs.NewValidationError("applicationData", "cannot exceed 50 fields")
	}

	// Check nesting depth
	if err := h.checkNestingDepth(data, 0); err != nil {
		return err
	}

	// Check field names for SQL injection patterns
	for key := range data {
		if err := ozzo.Validate(key,
			ozzo.Length(1, 100).Error("field name must be 1-100 characters"),
			validation.SafeSQLString,
			validation.SafeNoSQLString,
		); err != nil {
			return appErrs.NewValidationError(fmt.Sprintf("applicationData.%s", key), "invalid field name: "+err.Error())
		}
	}

	return nil
}

func (h *Handler) checkNestingDepth(data map[string]interface{}, depth int) error {
	if depth > 5 {
		return appErrs.NewValidationError("applicationData", "nesting too deep (max 5 levels)")
	}

	for _, value := range data {
		if nestedMap, ok := value.(map[string]interface{}); ok {
			if err := h.checkNestingDepth(nestedMap, depth+1); err != nil {
				return err
			}
		}
	}

	return nil
}

func (h *Handler) checkForInjectionPatterns(data map[string]interface{}) error {
	for key, value := range data {
		// Recursively check nested objects
		if nestedMap, ok := value.(map[string]interface{}); ok {
			if err := h.checkForInjectionPatterns(nestedMap); err != nil {
				return err
			}
			continue
		}

		// Check string values for SQL injection patterns
		if strVal, ok := value.(string); ok {
			if err := ozzo.Validate(strVal,
				validation.SafeSQLString,
				validation.SafeNoSQLString,
			); err != nil {
				return appErrs.NewValidationError(
					fmt.Sprintf("applicationData.%s", key),
					"contains potentially unsafe content",
				)
			}
		}
	}

	return nil
}

// Public Execute method for direct API calls
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// Validate before execution
	if err := h.validateInput(input); err != nil {
		return nil, err
	}
	return h.execute(ctx, input)
}
