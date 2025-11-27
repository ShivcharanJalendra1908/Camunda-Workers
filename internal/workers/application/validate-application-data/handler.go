// internal/workers/application/validate-application-data/handler.go
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

	"camunda-workers/internal/common/logger"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const (
	TaskType = "validate-application-data"
)

var (
	ErrApplicationValidationFailed = errors.New("APPLICATION_VALIDATION_FAILED")
)

type Handler struct {
	logger logger.Logger
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	return &Handler{
		logger: log.WithFields(map[string]interface{}{"taskType": TaskType}),
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	h.logger.Info("processing job", map[string]interface{}{
		"jobKey":      job.Key,
		"workflowKey": job.ProcessInstanceKey,
	})

	var input Input
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		h.failJob(client, job, "PARSE_ERROR", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := h.execute(ctx, &input)
	if err != nil {
		h.failJob(client, job, "APPLICATION_VALIDATION_FAILED", err.Error())
		return
	}

	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error": err,
		})
		return
	}
	_, err = cmd.Send(context.Background())
	if err != nil {
		h.logger.Error("failed to complete job", map[string]interface{}{
			"error": err,
		})
	}
}

func (h *Handler) execute(_ context.Context, input *Input) (*Output, error) {
	validated := make(map[string]interface{})
	var validationErrors []ValidationError

	// Validate personal info
	if personalRaw, ok := input.ApplicationData["personalInfo"]; ok {
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
	if financialRaw, ok := input.ApplicationData["financialInfo"]; ok {
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
	if experienceRaw, ok := input.ApplicationData["experience"]; ok {
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

func (h *Handler) validatePersonalInfo(data map[string]interface{}) (map[string]interface{}, []ValidationError) {
	validated := make(map[string]interface{})
	errors := []ValidationError{}

	// Name
	if nameRaw, ok := data["name"]; ok {
		if nameStr, ok := nameRaw.(string); ok {
			// Sanitize: trim and normalize multiple spaces to single space
			nameStr = strings.TrimSpace(nameStr)
			nameStr = regexp.MustCompile(`\s+`).ReplaceAllString(nameStr, " ")

			// Remove only invalid characters (keep letters, spaces, hyphens, apostrophes)
			// This regex keeps valid characters and removes everything else
			nameStr = regexp.MustCompile(`[^a-zA-Z\s\-']`).ReplaceAllString(nameStr, "")

			if nameRegex.MatchString(nameStr) {
				validated["name"] = nameStr
			} else {
				errors = append(errors, ValidationError{
					Field:   "personalInfo.name",
					Code:    "INVALID_FORMAT",
					Message: "Name must be 2-100 characters, letters, spaces, hyphens, or apostrophes",
				})
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

	// Email
	if emailRaw, ok := data["email"]; ok {
		if emailStr, ok := emailRaw.(string); ok {
			emailStr = strings.TrimSpace(emailStr)
			if emailRegex.MatchString(emailStr) {
				validated["email"] = emailStr
			} else {
				errors = append(errors, ValidationError{
					Field:   "personalInfo.email",
					Code:    "INVALID_FORMAT",
					Message: "Invalid email format",
				})
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

	// Phone
	if phoneRaw, ok := data["phone"]; ok {
		if phoneStr, ok := phoneRaw.(string); ok {
			phoneStr = strings.TrimSpace(phoneStr)
			// Remove all non-digit characters except leading +
			phoneStr = regexp.MustCompile(`[^\d\+]`).ReplaceAllString(phoneStr, "")

			// Validate AFTER sanitization - must have actual content
			if phoneStr == "" || !phoneRegex.MatchString(phoneStr) {
				errors = append(errors, ValidationError{
					Field:   "personalInfo.phone",
					Code:    "INVALID_FORMAT",
					Message: "Invalid phone format (E.164 recommended)",
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

	// Liquid capital
	if capitalRaw, ok := data["liquidCapital"]; ok {
		capital, err := h.parseInt(capitalRaw)
		if err != nil || capital < 0 {
			errors = append(errors, ValidationError{
				Field:   "financialInfo.liquidCapital",
				Code:    "INVALID_VALUE",
				Message: "Liquid capital must be a non-negative number",
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
	} else {
		errors = append(errors, ValidationError{
			Field:   "financialInfo.liquidCapital",
			Code:    "MISSING_REQUIRED",
			Message: "Liquid capital is required",
		})
	}

	// Net worth
	if netWorthRaw, ok := data["netWorth"]; ok {
		netWorth, err := h.parseInt(netWorthRaw)
		if err != nil || netWorth < 0 {
			errors = append(errors, ValidationError{
				Field:   "financialInfo.netWorth",
				Code:    "INVALID_VALUE",
				Message: "Net worth must be a non-negative number",
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
	} else {
		errors = append(errors, ValidationError{
			Field:   "financialInfo.netWorth",
			Code:    "MISSING_REQUIRED",
			Message: "Net worth is required",
		})
	}

	// Credit score (optional but required for some franchises)
	if creditRaw, ok := data["creditScore"]; ok {
		credit, err := h.parseInt(creditRaw)
		if err != nil || credit < 300 || credit > 850 {
			errors = append(errors, ValidationError{
				Field:   "financialInfo.creditScore",
				Code:    "INVALID_VALUE",
				Message: "Credit score must be between 300 and 850",
			})
		} else {
			validated["creditScore"] = credit
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

	// Years in industry
	if yearsRaw, ok := data["yearsInIndustry"]; ok {
		years, err := h.parseInt(yearsRaw)
		if err != nil || years < 0 {
			errors = append(errors, ValidationError{
				Field:   "experience.yearsInIndustry",
				Code:    "INVALID_VALUE",
				Message: "Years in industry must be a non-negative number",
			})
		} else {
			validated["yearsInIndustry"] = years
		}
	} else {
		errors = append(errors, ValidationError{
			Field:   "experience.yearsInIndustry",
			Code:    "MISSING_REQUIRED",
			Message: "Years in industry is required",
		})
	}

	// Management experience
	if mgmtRaw, ok := data["managementExperience"]; ok {
		if mgmtBool, ok := mgmtRaw.(bool); ok {
			validated["managementExperience"] = mgmtBool
		} else {
			errors = append(errors, ValidationError{
				Field:   "experience.managementExperience",
				Code:    "INVALID_TYPE",
				Message: "Management experience must be a boolean",
			})
		}
	} else {
		validated["managementExperience"] = false
	}

	return validated, errors
}

func (h *Handler) parseInt(raw interface{}) (int, error) {
	switch v := raw.(type) {
	case float64:
		return int(v), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(v))
	default:
		return 0, fmt.Errorf("not a number")
	}
}

func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string) {
	h.logger.Error("job failed", map[string]interface{}{
		"jobKey":       job.Key,
		"errorCode":    errorCode,
		"errorMessage": errorMessage,
	})

	_, _ = client.NewThrowErrorCommand().
		JobKey(job.Key).
		ErrorCode(errorCode).
		ErrorMessage(errorMessage).
		Send(context.Background())
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

// // internal/workers/application/validate-application-data/handler.go
// package validateapplicationdata

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"regexp"
// 	"strconv"
// 	"strings"
// 	"time"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"go.uber.org/zap"
// )

// const (
// 	TaskType = "validate-application-data"
// )

// var (
// 	ErrApplicationValidationFailed = errors.New("APPLICATION_VALIDATION_FAILED")
// )

// type Handler struct {
// 	logger *zap.Logger
// }

// func NewHandler(config *Config, logger *zap.Logger) *Handler {
// 	return &Handler{
// 		logger: logger.With(zap.String("taskType", TaskType)),
// 	}
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	h.logger.Info("processing job",
// 		zap.Int64("jobKey", job.Key),
// 		zap.Int64("workflowKey", job.ProcessInstanceKey),
// 	)

// 	var input Input
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
// 		h.failJob(client, job, "PARSE_ERROR", err.Error())
// 		return
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		h.failJob(client, job, "APPLICATION_VALIDATION_FAILED", err.Error())
// 		return
// 	}

// 	cmd, err := client.NewCompleteJobCommand().
// 		JobKey(job.Key).
// 		VariablesFromObject(output)
// 	if err != nil {
// 		h.logger.Error("failed to create complete job command", zap.Error(err))
// 		return
// 	}
// 	_, err = cmd.Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to complete job", zap.Error(err))
// 	}
// }

// func (h *Handler) execute(_ context.Context, input *Input) (*Output, error) {
// 	validated := make(map[string]interface{})
// 	var validationErrors []ValidationError

// 	// Validate personal info
// 	if personalRaw, ok := input.ApplicationData["personalInfo"]; ok {
// 		if personalMap, ok := personalRaw.(map[string]interface{}); ok {
// 			validatedPersonal, personalErrors := h.validatePersonalInfo(personalMap)
// 			validated["personalInfo"] = validatedPersonal
// 			validationErrors = append(validationErrors, personalErrors...)
// 		}
// 	} else {
// 		validationErrors = append(validationErrors, ValidationError{
// 			Field:   "personalInfo",
// 			Code:    "MISSING_REQUIRED",
// 			Message: "personalInfo is required",
// 		})
// 	}

// 	// Validate financial info
// 	if financialRaw, ok := input.ApplicationData["financialInfo"]; ok {
// 		if financialMap, ok := financialRaw.(map[string]interface{}); ok {
// 			validatedFinancial, financialErrors := h.validateFinancialInfo(financialMap, input.FranchiseID)
// 			validated["financialInfo"] = validatedFinancial
// 			validationErrors = append(validationErrors, financialErrors...)
// 		}
// 	} else {
// 		validationErrors = append(validationErrors, ValidationError{
// 			Field:   "financialInfo",
// 			Code:    "MISSING_REQUIRED",
// 			Message: "financialInfo is required",
// 		})
// 	}

// 	// Validate experience
// 	if experienceRaw, ok := input.ApplicationData["experience"]; ok {
// 		if experienceMap, ok := experienceRaw.(map[string]interface{}); ok {
// 			validatedExperience, experienceErrors := h.validateExperience(experienceMap)
// 			validated["experience"] = validatedExperience
// 			validationErrors = append(validationErrors, experienceErrors...)
// 		}
// 	} else {
// 		validationErrors = append(validationErrors, ValidationError{
// 			Field:   "experience",
// 			Code:    "MISSING_REQUIRED",
// 			Message: "experience is required",
// 		})
// 	}

// 	isValid := len(validationErrors) == 0
// 	h.logger.Info("validation completed",
// 		zap.Bool("isValid", isValid),
// 		zap.Int("errorCount", len(validationErrors)),
// 	)

// 	if !isValid {
// 		return nil, fmt.Errorf("%w: %d validation errors", ErrApplicationValidationFailed, len(validationErrors))
// 	}

// 	return &Output{
// 		IsValid:          true,
// 		ValidatedData:    validated,
// 		ValidationErrors: []ValidationError{},
// 	}, nil
// }

// func (h *Handler) validatePersonalInfo(data map[string]interface{}) (map[string]interface{}, []ValidationError) {
// 	validated := make(map[string]interface{})
// 	errors := []ValidationError{}

// 	// Name
// 	if nameRaw, ok := data["name"]; ok {
// 		if nameStr, ok := nameRaw.(string); ok {
// 			// Sanitize: trim and normalize multiple spaces to single space
// 			nameStr = strings.TrimSpace(nameStr)
// 			nameStr = regexp.MustCompile(`\s+`).ReplaceAllString(nameStr, " ")

// 			// Remove only invalid characters (keep letters, spaces, hyphens, apostrophes)
// 			// This regex keeps valid characters and removes everything else
// 			nameStr = regexp.MustCompile(`[^a-zA-Z\s\-']`).ReplaceAllString(nameStr, "")

// 			if nameRegex.MatchString(nameStr) {
// 				validated["name"] = nameStr
// 			} else {
// 				errors = append(errors, ValidationError{
// 					Field:   "personalInfo.name",
// 					Code:    "INVALID_FORMAT",
// 					Message: "Name must be 2-100 characters, letters, spaces, hyphens, or apostrophes",
// 				})
// 			}
// 		} else {
// 			errors = append(errors, ValidationError{
// 				Field:   "personalInfo.name",
// 				Code:    "INVALID_TYPE",
// 				Message: "Name must be a string",
// 			})
// 		}
// 	} else {
// 		errors = append(errors, ValidationError{
// 			Field:   "personalInfo.name",
// 			Code:    "MISSING_REQUIRED",
// 			Message: "Name is required",
// 		})
// 	}

// 	// Email
// 	if emailRaw, ok := data["email"]; ok {
// 		if emailStr, ok := emailRaw.(string); ok {
// 			emailStr = strings.TrimSpace(emailStr)
// 			if emailRegex.MatchString(emailStr) {
// 				validated["email"] = emailStr
// 			} else {
// 				errors = append(errors, ValidationError{
// 					Field:   "personalInfo.email",
// 					Code:    "INVALID_FORMAT",
// 					Message: "Invalid email format",
// 				})
// 			}
// 		} else {
// 			errors = append(errors, ValidationError{
// 				Field:   "personalInfo.email",
// 				Code:    "INVALID_TYPE",
// 				Message: "Email must be a string",
// 			})
// 		}
// 	} else {
// 		errors = append(errors, ValidationError{
// 			Field:   "personalInfo.email",
// 			Code:    "MISSING_REQUIRED",
// 			Message: "Email is required",
// 		})
// 	}

// 	// Phone
// 	if phoneRaw, ok := data["phone"]; ok {
// 		if phoneStr, ok := phoneRaw.(string); ok {
// 			phoneStr = strings.TrimSpace(phoneStr)
// 			// Remove all non-digit characters except leading +
// 			phoneStr = regexp.MustCompile(`[^\d\+]`).ReplaceAllString(phoneStr, "")

// 			// Validate AFTER sanitization - must have actual content
// 			if phoneStr == "" || !phoneRegex.MatchString(phoneStr) {
// 				errors = append(errors, ValidationError{
// 					Field:   "personalInfo.phone",
// 					Code:    "INVALID_FORMAT",
// 					Message: "Invalid phone format (E.164 recommended)",
// 				})
// 			} else {
// 				validated["phone"] = phoneStr
// 			}
// 		} else {
// 			errors = append(errors, ValidationError{
// 				Field:   "personalInfo.phone",
// 				Code:    "INVALID_TYPE",
// 				Message: "Phone must be a string",
// 			})
// 		}
// 	} else {
// 		errors = append(errors, ValidationError{
// 			Field:   "personalInfo.phone",
// 			Code:    "MISSING_REQUIRED",
// 			Message: "Phone is required",
// 		})
// 	}

// 	return validated, errors
// }

// func (h *Handler) validateFinancialInfo(data map[string]interface{}, franchiseID string) (map[string]interface{}, []ValidationError) {
// 	validated := make(map[string]interface{})
// 	errors := []ValidationError{}

// 	// Liquid capital
// 	if capitalRaw, ok := data["liquidCapital"]; ok {
// 		capital, err := h.parseInt(capitalRaw)
// 		if err != nil || capital < 0 {
// 			errors = append(errors, ValidationError{
// 				Field:   "financialInfo.liquidCapital",
// 				Code:    "INVALID_VALUE",
// 				Message: "Liquid capital must be a non-negative number",
// 			})
// 		} else {
// 			validated["liquidCapital"] = capital
// 			// Franchise-specific rule
// 			if rule, exists := franchiseRules[franchiseID]; exists {
// 				if capital < rule.MinLiquidCapital {
// 					errors = append(errors, ValidationError{
// 						Field:   "financialInfo.liquidCapital",
// 						Code:    "BELOW_MINIMUM",
// 						Message: fmt.Sprintf("Liquid capital must be at least $%d for this franchise", rule.MinLiquidCapital),
// 					})
// 				}
// 			}
// 		}
// 	} else {
// 		errors = append(errors, ValidationError{
// 			Field:   "financialInfo.liquidCapital",
// 			Code:    "MISSING_REQUIRED",
// 			Message: "Liquid capital is required",
// 		})
// 	}

// 	// Net worth
// 	if netWorthRaw, ok := data["netWorth"]; ok {
// 		netWorth, err := h.parseInt(netWorthRaw)
// 		if err != nil || netWorth < 0 {
// 			errors = append(errors, ValidationError{
// 				Field:   "financialInfo.netWorth",
// 				Code:    "INVALID_VALUE",
// 				Message: "Net worth must be a non-negative number",
// 			})
// 		} else {
// 			validated["netWorth"] = netWorth
// 			// Franchise-specific rule
// 			if rule, exists := franchiseRules[franchiseID]; exists {
// 				if netWorth < rule.MinNetWorth {
// 					errors = append(errors, ValidationError{
// 						Field:   "financialInfo.netWorth",
// 						Code:    "BELOW_MINIMUM",
// 						Message: fmt.Sprintf("Net worth must be at least $%d for this franchise", rule.MinNetWorth),
// 					})
// 				}
// 			}
// 		}
// 	} else {
// 		errors = append(errors, ValidationError{
// 			Field:   "financialInfo.netWorth",
// 			Code:    "MISSING_REQUIRED",
// 			Message: "Net worth is required",
// 		})
// 	}

// 	// Credit score (optional but required for some franchises)
// 	if creditRaw, ok := data["creditScore"]; ok {
// 		credit, err := h.parseInt(creditRaw)
// 		if err != nil || credit < 300 || credit > 850 {
// 			errors = append(errors, ValidationError{
// 				Field:   "financialInfo.creditScore",
// 				Code:    "INVALID_VALUE",
// 				Message: "Credit score must be between 300 and 850",
// 			})
// 		} else {
// 			validated["creditScore"] = credit
// 		}
// 	} else {
// 		// Check if required
// 		if rule, exists := franchiseRules[franchiseID]; exists && rule.RequiresCreditScore {
// 			errors = append(errors, ValidationError{
// 				Field:   "financialInfo.creditScore",
// 				Code:    "MISSING_REQUIRED",
// 				Message: "Credit score is required for this franchise",
// 			})
// 		}
// 	}

// 	return validated, errors
// }

// func (h *Handler) validateExperience(data map[string]interface{}) (map[string]interface{}, []ValidationError) {
// 	validated := make(map[string]interface{})
// 	errors := []ValidationError{}

// 	// Years in industry
// 	if yearsRaw, ok := data["yearsInIndustry"]; ok {
// 		years, err := h.parseInt(yearsRaw)
// 		if err != nil || years < 0 {
// 			errors = append(errors, ValidationError{
// 				Field:   "experience.yearsInIndustry",
// 				Code:    "INVALID_VALUE",
// 				Message: "Years in industry must be a non-negative number",
// 			})
// 		} else {
// 			validated["yearsInIndustry"] = years
// 		}
// 	} else {
// 		errors = append(errors, ValidationError{
// 			Field:   "experience.yearsInIndustry",
// 			Code:    "MISSING_REQUIRED",
// 			Message: "Years in industry is required",
// 		})
// 	}

// 	// Management experience
// 	if mgmtRaw, ok := data["managementExperience"]; ok {
// 		if mgmtBool, ok := mgmtRaw.(bool); ok {
// 			validated["managementExperience"] = mgmtBool
// 		} else {
// 			errors = append(errors, ValidationError{
// 				Field:   "experience.managementExperience",
// 				Code:    "INVALID_TYPE",
// 				Message: "Management experience must be a boolean",
// 			})
// 		}
// 	} else {
// 		validated["managementExperience"] = false
// 	}

// 	return validated, errors
// }

// func (h *Handler) parseInt(raw interface{}) (int, error) {
// 	switch v := raw.(type) {
// 	case float64:
// 		return int(v), nil
// 	case string:
// 		return strconv.Atoi(strings.TrimSpace(v))
// 	default:
// 		return 0, fmt.Errorf("not a number")
// 	}
// }

// func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string) {
// 	h.logger.Error("job failed",
// 		zap.Int64("jobKey", job.Key),
// 		zap.String("errorCode", errorCode),
// 		zap.String("errorMessage", errorMessage),
// 	)

// 	_, _ = client.NewThrowErrorCommand().
// 		JobKey(job.Key).
// 		ErrorCode(errorCode).
// 		ErrorMessage(errorMessage).
// 		Send(context.Background())
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }
