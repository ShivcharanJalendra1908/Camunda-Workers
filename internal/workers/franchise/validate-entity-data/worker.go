package validateentitydata

import (
	"context"
	"fmt"
	"strings"

	"camunda-workers/internal/common/logger"
	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const TaskType = "validate-entity-data"

// Config represents the worker configuration
type Config struct {
	Timeout int `mapstructure:"timeout"`
}

// Handler implements the worker logic
type Handler struct {
	config *Config
	logger logger.Logger
}

// NewHandler creates a new Handler instance
func NewHandler(cfg *Config, log logger.Logger) *Handler {
	return &Handler{
		config: cfg,
		logger: log,
	}
}

// Handle executes the worker logic
func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	jobKey := job.GetKey()
	
	h.logger.Info("Validating entity data", map[string]interface{}{
		"jobKey": jobKey,
	})

	variables, err := job.GetVariablesAsMap()
	if err != nil {
		h.logger.Error("Failed to get variables", map[string]interface{}{
			"jobKey": jobKey,
			"error":  err.Error(),
		})
		client.NewFailJobCommand().JobKey(jobKey).Retries(0).ErrorMessage(err.Error()).Send(context.Background())
		return
	}

	entityType, ok := variables["entityType"].(string)
	if !ok || entityType == "" {
		h.logger.Error("Missing entityType", map[string]interface{}{"jobKey": jobKey})
		cmd, err := client.NewCompleteJobCommand().JobKey(jobKey).VariablesFromMap(map[string]interface{}{
			"isValid":          false,
			"validationErrors": []string{"entityType is required"},
		})
		if err == nil {
			cmd.Send(context.Background())
		}
		return
	}
	entityType = strings.ToLower(entityType)
	switch entityType {
	case "franchises":
		entityType = "franchise"
	case "associations":
		entityType = "association"
	case "master-franchise", "master_franchises", "master franchises", "masterfranchise":
		entityType = "master_franchise"
	default:
		entityType = strings.TrimSuffix(entityType, "s")
		if entityType == "master-franchise" || entityType == "master franchise" || entityType == "masterfranchise" {
			entityType = "master_franchise"
		}
	}

	formData, ok := variables["formData"].(map[string]interface{})
	if !ok || formData == nil {
		h.logger.Error("Missing formData", map[string]interface{}{"jobKey": jobKey})
		cmd, err := client.NewCompleteJobCommand().JobKey(jobKey).VariablesFromMap(map[string]interface{}{
			"isValid":          false,
			"validationErrors": []string{"formData is required"},
		})
		if err == nil {
			cmd.Send(context.Background())
		}
		return
	}

	// Basic validation based on entityType
	isValid := true
	var validationErrors []string

	switch entityType {
	case "franchise", "master_franchise":
		companyName, hasCompany := formData["companyName"].(string)
		brandName, hasBrand := formData["brandName"].(string)
		
		if (!hasCompany || companyName == "") && (!hasBrand || brandName == "") {
			isValid = false
			validationErrors = append(validationErrors, fmt.Sprintf("companyName or brandName is required for %s", entityType))
		}
	case "association":
		if name, ok := formData["associationName"].(string); !ok || name == "" {
			isValid = false
			validationErrors = append(validationErrors, "associationName is required for association")
		}
		if email, ok := formData["contactEmail"].(string); !ok || email == "" {
			isValid = false
			validationErrors = append(validationErrors, "contactEmail is required for association")
		}
	}

	// SVG and Logo rules
	if logoURL, ok := formData["logoUrl"].(string); ok && logoURL != "" {
		lowerURL := strings.ToLower(logoURL)
		if strings.HasPrefix(lowerURL, "data:image/svg+xml") && strings.Contains(lowerURL, "<script") {
			isValid = false
			validationErrors = append(validationErrors, "SVG logos cannot contain scripts")
		} else if strings.HasPrefix(lowerURL, "javascript:") {
			isValid = false
			validationErrors = append(validationErrors, "Invalid logo URL format")
		}
	}

	h.logger.Info("Validation completed", map[string]interface{}{
		"jobKey":     jobKey,
		"entityType": entityType,
		"isValid":    isValid,
	})

	variablesMap := map[string]interface{}{
		"isValid":    isValid,
		"entityType": entityType,
	}
	if !isValid {
		variablesMap["validationErrors"] = validationErrors
	}

	cmd, err := client.NewCompleteJobCommand().JobKey(jobKey).VariablesFromMap(variablesMap)
	if err != nil {
		h.logger.Error("Failed to set variables", map[string]interface{}{
			"jobKey": jobKey,
			"error":  err.Error(),
		})
		return
	}

	_, err = cmd.Send(context.Background())
	if err != nil {
		h.logger.Error("Failed to complete job", map[string]interface{}{
			"jobKey": jobKey,
			"error":  err.Error(),
		})
	}
}
