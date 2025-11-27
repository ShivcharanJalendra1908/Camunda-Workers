package buildresponse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"camunda-workers/internal/common/logger"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/xeipuuv/gojsonschema"
)

const TaskType = "build-response"

var (
	ErrTemplateNotFound         = errors.New("TEMPLATE_NOT_FOUND")
	ErrTemplateValidationFailed = errors.New("TEMPLATE_VALIDATION_FAILED")
)

type templateCacheEntry struct {
	template *TemplateDefinition
	loadedAt time.Time
}

type Handler struct {
	config *Config
	logger logger.Logger // Changed from *zap.Logger
	cache  map[string]*templateCacheEntry
	mu     sync.RWMutex
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	return &Handler{
		config: config,
		logger: log.WithFields(map[string]interface{}{"taskType": TaskType}), // Fixed WithFields call
		cache:  make(map[string]*templateCacheEntry),
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	h.logger.Info("processing job",
		map[string]interface{}{
			"jobKey":      job.Key,
			"workflowKey": job.ProcessInstanceKey,
		})

	var input Input
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := h.Execute(ctx, &input) // Changed to Execute
	if err != nil {
		errorCode := "RESPONSE_BUILD_ERROR"
		if errors.Is(err, ErrTemplateNotFound) {
			errorCode = "TEMPLATE_NOT_FOUND"
		} else if errors.Is(err, ErrTemplateValidationFailed) {
			errorCode = "TEMPLATE_VALIDATION_FAILED"
		}
		h.failJob(client, job, errorCode, err.Error(), 0)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	template, err := h.loadTemplate(input.TemplateId)
	if err != nil {
		return nil, err
	}

	if err := h.validateData(template.Schema, input.Data); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTemplateValidationFailed, err)
	}

	responseData := h.substituteTemplate(template.Template, input.Data)

	if responseData == nil {
		h.logger.Error("Template substitution resulted in nil root object",
			map[string]interface{}{
				"templateId": input.TemplateId,
				"requestId":  input.RequestId,
			})
		return nil, fmt.Errorf("template substitution resulted in nil root object for template ID: %s", input.TemplateId)
	}

	responseDataMap, ok := responseData.(map[string]interface{})
	if !ok {
		h.logger.Error("Template substitution did not return a map for root object",
			map[string]interface{}{
				"templateId": input.TemplateId,
				"requestId":  input.RequestId,
				"resultType": fmt.Sprintf("%T", responseData),
			})
		return nil, fmt.Errorf("expected template root to be an object after substitution, got %T (value: %v) for template ID: %s", responseData, responseData, input.TemplateId)
	}

	metadata := ResponseMetadata{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Version:   h.config.AppVersion,
	}

	payload := ResponsePayload{
		RequestId: input.RequestId,
		Status:    "success",
		Data:      responseDataMap,
		Metadata:  metadata,
	}

	return &Output{Response: payload}, nil
}

func (h *Handler) substituteTemplate(templateData interface{}, inputData map[string]interface{}) interface{} {
	if templateData == nil {
		return nil
	}

	switch v := templateData.(type) {
	case string:
		// Check if this is a template placeholder like {{key}}
		if len(v) > 4 && v[0] == '{' && v[1] == '{' && v[len(v)-2] == '}' && v[len(v)-1] == '}' {
			key := strings.TrimSpace(v[2 : len(v)-2]) // Remove whitespace from key
			value := h.lookupNestedValue(inputData, key)
			if value != nil {
				// Convert integer types to float64 for JSON compatibility
				switch numVal := value.(type) {
				case int:
					return float64(numVal)
				case int64:
					return float64(numVal)
				case int32:
					return float64(numVal)
				case int16:
					return float64(numVal)
				case int8:
					return float64(numVal)
				default:
					return value
				}
			}
			// Return nil for missing values - let the test handle this appropriately
			return nil
		}
		return v
	case map[string]interface{}:
		result := make(map[string]interface{})
		for k, v2 := range v {
			substituted := h.substituteTemplate(v2, inputData)
			// Include the key even if value is nil to maintain structure
			result[k] = substituted
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = h.substituteTemplate(item, inputData)
		}
		return result
	default:
		return v
	}
}

func (h *Handler) lookupNestedValue(data map[string]interface{}, key string) interface{} {
	parts := strings.Split(key, ".")
	current := interface{}(data)

	for _, part := range parts {
		currentMap, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}

		val, exists := currentMap[part]
		if !exists {
			return nil
		}

		current = val
	}

	return current
}

func (h *Handler) loadTemplate(id string) (*TemplateDefinition, error) {
	h.mu.RLock()
	if entry, ok := h.cache[id]; ok && time.Since(entry.loadedAt) < h.config.CacheTTL {
		h.mu.RUnlock()
		return entry.template, nil
	}
	h.mu.RUnlock()

	registryBytes, err := os.ReadFile(h.config.TemplateRegistry)
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}

	var registry struct {
		Templates []TemplateDefinition `json:"templates"`
	}
	if err := json.Unmarshal(registryBytes, &registry); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}

	for _, t := range registry.Templates {
		if t.ID == id {
			h.mu.Lock()
			h.cache[id] = &templateCacheEntry{
				template: &t,
				loadedAt: time.Now(),
			}
			h.mu.Unlock()
			return &t, nil
		}
	}

	return nil, ErrTemplateNotFound
}

func (h *Handler) validateData(schemaMap, data map[string]interface{}) error {
	if len(schemaMap) == 0 {
		return nil
	}

	schemaLoader := gojsonschema.NewGoLoader(schemaMap)
	documentLoader := gojsonschema.NewGoLoader(data)

	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		return fmt.Errorf("validation error: %w", err)
	}

	if !result.Valid() {
		errs := make([]string, len(result.Errors()))
		for i, desc := range result.Errors() {
			errs[i] = desc.String()
		}
		return fmt.Errorf("data validation failed: %v", errs)
	}

	return nil
}

func (h *Handler) deepMerge(dst, src map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range dst {
		result[k] = v
	}
	for k, v := range src {
		result[k] = v
	}
	return result
}

func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		h.logger.Error("failed to create complete job command", map[string]interface{}{"error": err})
		return
	}
	_, err = cmd.Send(context.Background())
	if err != nil {
		h.logger.Error("failed to send complete job command", map[string]interface{}{"error": err})
	}
}

func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, _ int32) {
	h.logger.Error("job failed",
		map[string]interface{}{
			"jobKey":       job.Key,
			"errorCode":    errorCode,
			"errorMessage": errorMessage,
		})

	_, err := client.NewThrowErrorCommand().
		JobKey(job.Key).
		ErrorCode(errorCode).
		ErrorMessage(errorMessage).
		Send(context.Background())
	if err != nil {
		h.logger.Error("failed to throw error", map[string]interface{}{"error": err})
	}
}

// // internal/workers/infrastructure/build-response/handler.go
// package buildresponse

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"os"
// 	"strings"
// 	"sync"
// 	"time"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"github.com/xeipuuv/gojsonschema"
// 	"go.uber.org/zap"
// )

// const TaskType = "build-response"

// var (
// 	ErrTemplateNotFound         = errors.New("TEMPLATE_NOT_FOUND")
// 	ErrTemplateValidationFailed = errors.New("TEMPLATE_VALIDATION_FAILED")
// )

// type templateCacheEntry struct {
// 	template *TemplateDefinition
// 	loadedAt time.Time
// }

// type Handler struct {
// 	config *Config
// 	logger *zap.Logger
// 	cache  map[string]*templateCacheEntry
// 	mu     sync.RWMutex
// }

// func NewHandler(config *Config, logger *zap.Logger) *Handler {
// 	return &Handler{
// 		config: config,
// 		logger: logger.With(zap.String("taskType", TaskType)),
// 		cache:  make(map[string]*templateCacheEntry),
// 	}
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	h.logger.Info("processing job",
// 		zap.Int64("jobKey", job.Key),
// 		zap.Int64("workflowKey", job.ProcessInstanceKey),
// 	)

// 	var input Input
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
// 		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
// 		return
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		errorCode := "RESPONSE_BUILD_ERROR"
// 		if errors.Is(err, ErrTemplateNotFound) {
// 			errorCode = "TEMPLATE_NOT_FOUND"
// 		} else if errors.Is(err, ErrTemplateValidationFailed) {
// 			errorCode = "TEMPLATE_VALIDATION_FAILED"
// 		}
// 		h.failJob(client, job, errorCode, err.Error(), 0)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(_ context.Context, input *Input) (*Output, error) {
// 	template, err := h.loadTemplate(input.TemplateId)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if err := h.validateData(template.Schema, input.Data); err != nil {
// 		return nil, fmt.Errorf("%w: %v", ErrTemplateValidationFailed, err)
// 	}

// 	responseData := h.substituteTemplate(template.Template, input.Data)

// 	if responseData == nil {
// 		h.logger.Error("Template substitution resulted in nil root object",
// 			zap.String("templateId", input.TemplateId),
// 			zap.String("requestId", input.RequestId))
// 		return nil, fmt.Errorf("template substitution resulted in nil root object for template ID: %s", input.TemplateId)
// 	}

// 	responseDataMap, ok := responseData.(map[string]interface{})
// 	if !ok {
// 		h.logger.Error("Template substitution did not return a map for root object",
// 			zap.String("templateId", input.TemplateId),
// 			zap.String("requestId", input.RequestId),
// 			zap.Any("resultType", fmt.Sprintf("%T", responseData)))
// 		return nil, fmt.Errorf("expected template root to be an object after substitution, got %T (value: %v) for template ID: %s", responseData, responseData, input.TemplateId)
// 	}

// 	metadata := ResponseMetadata{
// 		Timestamp: time.Now().UTC().Format(time.RFC3339),
// 		Version:   h.config.AppVersion,
// 	}

// 	payload := ResponsePayload{
// 		RequestId: input.RequestId,
// 		Status:    "success",
// 		Data:      responseDataMap,
// 		Metadata:  metadata, // Fixed: was Meta
// 	}

// 	return &Output{Response: payload}, nil
// }

// func (h *Handler) substituteTemplate(templateData interface{}, inputData map[string]interface{}) interface{} {
// 	if templateData == nil {
// 		return nil
// 	}

// 	switch v := templateData.(type) {
// 	case string:
// 		// Check if this is a template placeholder like {{key}}
// 		if len(v) > 4 && v[0] == '{' && v[1] == '{' && v[len(v)-2] == '}' && v[len(v)-1] == '}' {
// 			key := strings.TrimSpace(v[2 : len(v)-2]) // Remove whitespace from key
// 			value := h.lookupNestedValue(inputData, key)
// 			if value != nil {
// 				// Convert integer types to float64 for JSON compatibility
// 				switch numVal := value.(type) {
// 				case int:
// 					return float64(numVal)
// 				case int64:
// 					return float64(numVal)
// 				case int32:
// 					return float64(numVal)
// 				case int16:
// 					return float64(numVal)
// 				case int8:
// 					return float64(numVal)
// 				default:
// 					return value
// 				}
// 			}
// 			// Return nil for missing values - let the test handle this appropriately
// 			return nil
// 		}
// 		return v
// 	case map[string]interface{}:
// 		result := make(map[string]interface{})
// 		for k, v2 := range v {
// 			substituted := h.substituteTemplate(v2, inputData)
// 			// Include the key even if value is nil to maintain structure
// 			result[k] = substituted
// 		}
// 		return result
// 	case []interface{}:
// 		result := make([]interface{}, len(v))
// 		for i, item := range v {
// 			result[i] = h.substituteTemplate(item, inputData)
// 		}
// 		return result
// 	default:
// 		return v
// 	}
// }

// func (h *Handler) lookupNestedValue(data map[string]interface{}, key string) interface{} {
// 	parts := strings.Split(key, ".")
// 	current := interface{}(data)

// 	for _, part := range parts {
// 		currentMap, ok := current.(map[string]interface{})
// 		if !ok {
// 			return nil
// 		}

// 		val, exists := currentMap[part]
// 		if !exists {
// 			return nil
// 		}

// 		current = val
// 	}

// 	return current
// }

// func (h *Handler) loadTemplate(id string) (*TemplateDefinition, error) {
// 	h.mu.RLock()
// 	if entry, ok := h.cache[id]; ok && time.Since(entry.loadedAt) < h.config.CacheTTL {
// 		h.mu.RUnlock()
// 		return entry.template, nil
// 	}
// 	h.mu.RUnlock()

// 	registryBytes, err := os.ReadFile(h.config.TemplateRegistry)
// 	if err != nil {
// 		return nil, fmt.Errorf("read registry: %w", err)
// 	}

// 	var registry struct {
// 		Templates []TemplateDefinition `json:"templates"`
// 	}
// 	if err := json.Unmarshal(registryBytes, &registry); err != nil {
// 		return nil, fmt.Errorf("parse registry: %w", err)
// 	}

// 	for _, t := range registry.Templates {
// 		if t.ID == id {
// 			h.mu.Lock()
// 			h.cache[id] = &templateCacheEntry{
// 				template: &t,
// 				loadedAt: time.Now(),
// 			}
// 			h.mu.Unlock()
// 			return &t, nil
// 		}
// 	}

// 	return nil, ErrTemplateNotFound
// }

// func (h *Handler) validateData(schemaMap, data map[string]interface{}) error {
// 	if len(schemaMap) == 0 {
// 		return nil
// 	}

// 	schemaLoader := gojsonschema.NewGoLoader(schemaMap)
// 	documentLoader := gojsonschema.NewGoLoader(data)

// 	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
// 	if err != nil {
// 		return fmt.Errorf("validation error: %w", err)
// 	}

// 	if !result.Valid() {
// 		errs := make([]string, len(result.Errors()))
// 		for i, desc := range result.Errors() {
// 			errs[i] = desc.String()
// 		}
// 		return fmt.Errorf("data validation failed: %v", errs)
// 	}

// 	return nil
// }

// func (h *Handler) deepMerge(dst, src map[string]interface{}) map[string]interface{} {
// 	result := make(map[string]interface{})
// 	for k, v := range dst {
// 		result[k] = v
// 	}
// 	for k, v := range src {
// 		result[k] = v
// 	}
// 	return result
// }

// func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
// 	cmd, err := client.NewCompleteJobCommand().
// 		JobKey(job.Key).
// 		VariablesFromObject(output)
// 	if err != nil {
// 		h.logger.Error("failed to create complete job command", zap.Error(err))
// 		return
// 	}
// 	_, err = cmd.Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to send complete job command", zap.Error(err))
// 	}
// }

// func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, _ int32) {
// 	h.logger.Error("job failed",
// 		zap.Int64("jobKey", job.Key),
// 		zap.String("errorCode", errorCode),
// 		zap.String("errorMessage", errorMessage),
// 	)

// 	_, err := client.NewThrowErrorCommand().
// 		JobKey(job.Key).
// 		ErrorCode(errorCode).
// 		ErrorMessage(errorMessage).
// 		Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to throw error", zap.Error(err))
// 	}
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
//     return h.execute(ctx, input)
// }
