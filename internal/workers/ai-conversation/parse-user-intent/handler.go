package parseuserintent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const (
	TaskType = "parse-user-intent"
)

var (
	ErrIntentParsingFailed = errors.New("INTENT_PARSING_FAILED")
	ErrIntentAPITimeout    = errors.New("INTENT_API_TIMEOUT")
)

// Logger interface definition
type Logger interface {
	Info(msg string, fields map[string]interface{})
	Warn(msg string, fields map[string]interface{})
	Error(msg string, fields map[string]interface{})
	With(fields map[string]interface{}) Logger
}

type Handler struct {
	config *Config
	client *http.Client
	logger Logger
}

func NewHandler(config *Config, log Logger) *Handler {
	return &Handler{
		config: config,
		client: &http.Client{
			Timeout: config.Timeout,
		},
		logger: log.With(map[string]interface{}{
			"taskType": TaskType,
		}),
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	h.logger.Info("processing job", map[string]interface{}{
		"jobKey":            job.Key,
		"workflowKey":       job.ProcessInstanceKey,
	})

	var input Input
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		h.failJob(client, job, fmt.Errorf("parse input: %w", err), 0)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
	defer cancel()

	output, err := h.execute(ctx, &input)
	if err != nil {
		retries := int32(0)
		if errors.Is(err, ErrIntentAPITimeout) {
			retries = 1
		} else if errors.Is(err, ErrIntentParsingFailed) {
			retries = 2
		}
		h.failJob(client, job, err, retries)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	requestBody := map[string]interface{}{
		"query": input.Question,
	}

	// Only include context if it's not nil
	if input.Context != nil {
		requestBody["context"] = input.Context
	}

	body, _ := json.Marshal(requestBody)
	req, err := http.NewRequestWithContext(ctx, "POST", h.config.GenAIBaseURL+"/api/ai/parse-intent", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIntentParsingFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")

	var resp *http.Response
	var lastErr error

	for attempt := 0; attempt <= h.config.MaxRetries; attempt++ {

		if attempt > 0 {
			// Apply exponential backoff for retries
			backoff := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ErrIntentAPITimeout
			}
		}

		resp, lastErr = h.client.Do(req)

		// FIX: If context expired during the request, return timeout immediately.
		if ctx.Err() != nil ||
			errors.Is(lastErr, context.DeadlineExceeded) ||
			errors.Is(lastErr, context.Canceled) {

			return nil, ErrIntentAPITimeout
		}

		if lastErr == nil {
			// Check if response is successful
			if resp.StatusCode == http.StatusOK {
				break
			}
			// For non-OK status codes, treat as error and retry
			resp.Body.Close()
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
			resp = nil
		}
	}

	if lastErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrIntentAPITimeout
		}
		return nil, fmt.Errorf("%w: %v", ErrIntentParsingFailed, lastErr)
	}

	if resp == nil {
		return nil, fmt.Errorf("%w: no successful response after retries", ErrIntentParsingFailed)
	}
	defer resp.Body.Close()

	var apiResponse struct {
		Intent      string   `json:"intent"`
		Confidence  float64  `json:"confidence"`
		Entities    []Entity `json:"entities"`
		DataSources []string `json:"dataSources"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("%w: decode error: %v", ErrIntentParsingFailed, err)
	}

	dataSources := apiResponse.DataSources
	if len(dataSources) == 0 {
		dataSources = h.determineDataSources(apiResponse.Intent, apiResponse.Entities)
	}

	output := &Output{
		IntentAnalysis: IntentAnalysis{
			PrimaryIntent: apiResponse.Intent,
			Confidence:    apiResponse.Confidence,
		},
		DataSources: dataSources,
		Entities:    apiResponse.Entities,
	}

	h.logger.Info("intent parsed successfully", map[string]interface{}{
		"intent":       apiResponse.Intent,
		"confidence":   apiResponse.Confidence,
		"entityCount":  len(apiResponse.Entities),
		"dataSources":  dataSources,
	})

	return output, nil
}

func (h *Handler) determineDataSources(intent string, entities []Entity) []string {
	// Use a slice with fixed order to ensure deterministic results
	sources := []string{"internal_db"}
	hasSearch := false
	hasExternal := false

	for _, e := range entities {
		if e.Type == "franchise_name" || e.Type == "category" {
			hasSearch = true
		}
	}

	switch intent {
	case "general_info", "market_research", "competitor_analysis":
		hasExternal = true
	}

	if hasSearch {
		sources = append(sources, "search_index")
	}
	if hasExternal {
		sources = append(sources, "external_web")
	}

	return sources
}

func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)

	if err != nil {
		h.logger.Error("Failed to create complete job command", map[string]interface{}{
			"jobKey": job.Key,
			"error":  err.Error(),
		})
		return
	}

	_, err = cmd.Send(context.Background())
	if err != nil {
		h.logger.Error("Failed to send complete job command", map[string]interface{}{
			"jobKey": job.Key,
			"error":  err.Error(),
		})
	}
}

func (h *Handler) failJob(client worker.JobClient, job entities.Job, err error, retries int32) {
	errorCode := "UNKNOWN_ERROR"
	if errors.Is(err, ErrIntentAPITimeout) {
		errorCode = "INTENT_API_TIMEOUT"
	} else if errors.Is(err, ErrIntentParsingFailed) {
		errorCode = "INTENT_PARSING_FAILED"
	}

	h.logger.Error("job failed", map[string]interface{}{
		"jobKey":    job.Key,
		"error":     err.Error(),
		"errorCode": errorCode,
	})

	_, _ = client.NewFailJobCommand().
		JobKey(job.Key).
		Retries(retries).
		ErrorMessage(errorCode + ": " + err.Error()).
		Send(context.Background())
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

// package parseuserintent

// import (
// 	"bytes"
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"net/http"
// 	"time"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"go.uber.org/zap"
// )

// const (
// 	TaskType = "parse-user-intent"
// )

// var (
// 	ErrIntentParsingFailed = errors.New("INTENT_PARSING_FAILED")
// 	ErrIntentAPITimeout    = errors.New("INTENT_API_TIMEOUT")
// )

// type Handler struct {
// 	config *Config
// 	client *http.Client
// 	logger *zap.Logger
// }

// func NewHandler(config *Config, logger *zap.Logger) *Handler {
// 	return &Handler{
// 		config: config,
// 		client: &http.Client{
// 			Timeout: config.Timeout,
// 		},
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
// 		h.failJob(client, job, fmt.Errorf("parse input: %w", err), 0)
// 		return
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		retries := int32(0)
// 		if errors.Is(err, ErrIntentAPITimeout) {
// 			retries = 1
// 		} else if errors.Is(err, ErrIntentParsingFailed) {
// 			retries = 2
// 		}
// 		h.failJob(client, job, err, retries)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	requestBody := map[string]interface{}{
// 		"query": input.Question,
// 	}

// 	// Only include context if it's not nil
// 	if input.Context != nil {
// 		requestBody["context"] = input.Context
// 	}

// 	body, _ := json.Marshal(requestBody)
// 	req, err := http.NewRequestWithContext(ctx, "POST", h.config.GenAIBaseURL+"/api/ai/parse-intent", bytes.NewBuffer(body))
// 	if err != nil {
// 		return nil, fmt.Errorf("%w: %v", ErrIntentParsingFailed, err)
// 	}
// 	req.Header.Set("Content-Type", "application/json")

// 	var resp *http.Response
// 	var lastErr error

// 	for attempt := 0; attempt <= h.config.MaxRetries; attempt++ {

// 		if attempt > 0 {
// 			// Apply exponential backoff for retries
// 			backoff := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
// 			select {
// 			case <-time.After(backoff):
// 			case <-ctx.Done():
// 				return nil, ErrIntentAPITimeout
// 			}
// 		}

// 		resp, lastErr = h.client.Do(req)

// 		// FIX: If context expired during the request, return timeout immediately.
// 		if ctx.Err() != nil ||
// 			errors.Is(lastErr, context.DeadlineExceeded) ||
// 			errors.Is(lastErr, context.Canceled) {

// 			return nil, ErrIntentAPITimeout
// 		}

// 		if lastErr == nil {
// 			// Check if response is successful
// 			if resp.StatusCode == http.StatusOK {
// 				break
// 			}
// 			// For non-OK status codes, treat as error and retry
// 			resp.Body.Close()
// 			lastErr = fmt.Errorf("status %d", resp.StatusCode)
// 			resp = nil
// 		}
// 	}

// 	if lastErr != nil {
// 		if ctx.Err() == context.DeadlineExceeded {
// 			return nil, ErrIntentAPITimeout
// 		}
// 		return nil, fmt.Errorf("%w: %v", ErrIntentParsingFailed, lastErr)
// 	}

// 	if resp == nil {
// 		return nil, fmt.Errorf("%w: no successful response after retries", ErrIntentParsingFailed)
// 	}
// 	defer resp.Body.Close()

// 	var apiResponse struct {
// 		Intent      string   `json:"intent"`
// 		Confidence  float64  `json:"confidence"`
// 		Entities    []Entity `json:"entities"`
// 		DataSources []string `json:"dataSources"`
// 	}

// 	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
// 		return nil, fmt.Errorf("%w: decode error: %v", ErrIntentParsingFailed, err)
// 	}

// 	dataSources := apiResponse.DataSources
// 	if len(dataSources) == 0 {
// 		dataSources = h.determineDataSources(apiResponse.Intent, apiResponse.Entities)
// 	}

// 	output := &Output{
// 		IntentAnalysis: IntentAnalysis{
// 			PrimaryIntent: apiResponse.Intent,
// 			Confidence:    apiResponse.Confidence,
// 		},
// 		DataSources: dataSources,
// 		Entities:    apiResponse.Entities,
// 	}

// 	h.logger.Info("intent parsed successfully",
// 		zap.String("intent", apiResponse.Intent),
// 		zap.Float64("confidence", apiResponse.Confidence),
// 		zap.Int("entityCount", len(apiResponse.Entities)),
// 		zap.Strings("dataSources", dataSources),
// 	)

// 	return output, nil
// }

// func (h *Handler) determineDataSources(intent string, entities []Entity) []string {
// 	// Use a slice with fixed order to ensure deterministic results
// 	sources := []string{"internal_db"}
// 	hasSearch := false
// 	hasExternal := false

// 	for _, e := range entities {
// 		if e.Type == "franchise_name" || e.Type == "category" {
// 			hasSearch = true
// 		}
// 	}

// 	switch intent {
// 	case "general_info", "market_research", "competitor_analysis":
// 		hasExternal = true
// 	}

// 	if hasSearch {
// 		sources = append(sources, "search_index")
// 	}
// 	if hasExternal {
// 		sources = append(sources, "external_web")
// 	}

// 	return sources
// }

// func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
// 	cmd, err := client.NewCompleteJobCommand().
// 		JobKey(job.Key).
// 		VariablesFromObject(output)

// 	if err != nil {
// 		h.logger.Error("Failed to create complete job command",
// 			zap.Int64("jobKey", job.Key),
// 			zap.Error(err))
// 		return
// 	}

// 	_, err = cmd.Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("Failed to send complete job command",
// 			zap.Int64("jobKey", job.Key),
// 			zap.Error(err))
// 	}
// }

// func (h *Handler) failJob(client worker.JobClient, job entities.Job, err error, retries int32) {
// 	errorCode := "UNKNOWN_ERROR"
// 	if errors.Is(err, ErrIntentAPITimeout) {
// 		errorCode = "INTENT_API_TIMEOUT"
// 	} else if errors.Is(err, ErrIntentParsingFailed) {
// 		errorCode = "INTENT_PARSING_FAILED"
// 	}

// 	h.logger.Error("job failed",
// 		zap.Int64("jobKey", job.Key),
// 		zap.Error(err),
// 		zap.String("errorCode", errorCode),
// 	)

// 	_, _ = client.NewFailJobCommand().
// 		JobKey(job.Key).
// 		Retries(retries).
// 		ErrorMessage(errorCode + ": " + err.Error()).
// 		Send(context.Background())
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }
