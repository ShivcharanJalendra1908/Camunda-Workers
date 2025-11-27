// internal/workers/ai-conversation/llm-synthesis/handler.go
package llmsynthesis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const (
	TaskType = "llm-synthesis"
)

var (
	ErrLLMTimeout         = errors.New("LLM_TIMEOUT")
	ErrLLMSynthesisFailed = errors.New("LLM_SYNTHESIS_FAILED")
)

// Logger interface definition
type Logger interface {
	Info(msg string, fields map[string]interface{})
	Error(msg string, fields map[string]interface{})
	With(fields map[string]interface{}) Logger
}

type Handler struct {
	config *Config
	client *http.Client
	logger Logger
}

// Updated constructor with Logger interface
func NewHandler(config *Config, log Logger) *Handler {
	return &Handler{
		config: config,
		client: &http.Client{
			// Remove HTTP client timeout completely - rely only on context
		},
		logger: log.With(map[string]interface{}{
			"taskType": TaskType,
		}),
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	h.logger.Info("processing job", map[string]interface{}{
		"jobKey":      job.Key,
		"workflowKey": job.ProcessInstanceKey,
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
		if errors.Is(err, ErrLLMTimeout) {
			retries = 1
		} else if errors.Is(err, ErrLLMSynthesisFailed) {
			retries = 1
		}
		h.failJob(client, job, err, retries)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	prompt := h.buildPrompt(input)
	requestBody := map[string]interface{}{
		"prompt": prompt,
		"context": map[string]interface{}{
			"internal": input.InternalData,
			"external": input.WebData,
			"intent":   input.Intent,
		},
		"max_tokens":  h.config.MaxTokens,
		"temperature": h.config.Temperature,
	}

	body, _ := json.Marshal(requestBody)
	req, err := http.NewRequestWithContext(ctx, "POST", h.config.GenAIBaseURL+"/api/ai/generate", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLLMSynthesisFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")

	var resp *http.Response
	var lastErr error

	for attempt := 0; attempt <= h.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Apply exponential backoff
			backoff := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
			select {
			case <-time.After(backoff):
				// Continue with retry
			case <-ctx.Done():
				return nil, ErrLLMTimeout
			}
		}

		resp, lastErr = h.client.Do(req)
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

		// Check if error is due to context cancellation/timeout
		if ctx.Err() != nil {
			return nil, ErrLLMTimeout
		}
	}

	if lastErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrLLMTimeout
		}
		return nil, fmt.Errorf("%w: %v", ErrLLMSynthesisFailed, lastErr)
	}

	if resp == nil {
		return nil, fmt.Errorf("%w: no successful response after retries", ErrLLMSynthesisFailed)
	}
	defer resp.Body.Close()

	var apiResponse struct {
		Text       string   `json:"text"`
		Confidence float64  `json:"confidence"`
		Sources    []string `json:"sources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("%w: decode error: %v", ErrLLMSynthesisFailed, err)
	}

	// Validate response
	if strings.TrimSpace(apiResponse.Text) == "" {
		apiResponse.Text = "I don't have enough information to answer that question."
		apiResponse.Confidence = 0.1
	}

	if apiResponse.Confidence < 0.0 || apiResponse.Confidence > 1.0 {
		apiResponse.Confidence = 0.5
	}

	h.logger.Info("LLM synthesis completed", map[string]interface{}{
		"confidence":  apiResponse.Confidence,
		"sourceCount": len(apiResponse.Sources),
	})

	return &Output{
		LLMResponse: apiResponse.Text,
		Confidence:  apiResponse.Confidence,
		Sources:     apiResponse.Sources,
	}, nil
}

func (h *Handler) buildPrompt(input *Input) string {
	var parts []string

	parts = append(parts, "You are a helpful franchise advisor. Answer the user's question based ONLY on the provided data.")
	parts = append(parts, fmt.Sprintf("\nUser Question: %s", input.Question))

	// Internal data
	if len(input.InternalData) > 0 {
		internalJSON, _ := json.MarshalIndent(input.InternalData, "", "  ")
		parts = append(parts, "\nInternal Franchise Data:")
		parts = append(parts, string(internalJSON))
	}

	// Web data
	if len(input.WebData.Sources) > 0 {
		parts = append(parts, "\nExternal Web Sources:")
		for _, src := range input.WebData.Sources {
			parts = append(parts, fmt.Sprintf("- %s: %s", src.Title, src.URL))
		}
		if input.WebData.Summary != "" {
			parts = append(parts, fmt.Sprintf("Summary: %s", input.WebData.Summary))
		}
	}

	parts = append(parts, "\nInstructions:")
	parts = append(parts, "- Cite sources when using external information")
	parts = append(parts, "- If data is insufficient, say so clearly")
	parts = append(parts, "- Keep response concise and professional")
	parts = append(parts, "- Return confidence score between 0.0 and 1.0")

	parts = append(parts, "\nAnswer:")

	return strings.Join(parts, "\n")
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
	if errors.Is(err, ErrLLMTimeout) {
		errorCode = "LLM_TIMEOUT"
	} else if errors.Is(err, ErrLLMSynthesisFailed) {
		errorCode = "LLM_SYNTHESIS_FAILED"
	}

	h.logger.Error("job failed", map[string]interface{}{
		"jobKey":    job.Key,
		"error":     err.Error(),
		"errorCode": errorCode,
		"retries":   retries,
	})

	_, _ = client.NewFailJobCommand().
		JobKey(job.Key).
		Retries(retries).
		ErrorMessage(err.Error()).
		Send(context.Background())
}

// Execute method for direct usage
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

// // internal/workers/ai-conversation/llm-synthesis/handler.go
// package llmsynthesis

// import (
// 	"bytes"
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"net/http"
// 	"strings"
// 	"time"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"go.uber.org/zap"
// )

// const (
// 	TaskType = "llm-synthesis"
// )

// var (
// 	ErrLLMTimeout         = errors.New("LLM_TIMEOUT")
// 	ErrLLMSynthesisFailed = errors.New("LLM_SYNTHESIS_FAILED")
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
// 			// Remove HTTP client timeout completely - rely only on context
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
// 		if errors.Is(err, ErrLLMTimeout) {
// 			retries = 1
// 		} else if errors.Is(err, ErrLLMSynthesisFailed) {
// 			retries = 1
// 		}
// 		h.failJob(client, job, err, retries)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	prompt := h.buildPrompt(input)
// 	requestBody := map[string]interface{}{
// 		"prompt": prompt,
// 		"context": map[string]interface{}{
// 			"internal": input.InternalData,
// 			"external": input.WebData,
// 			"intent":   input.Intent,
// 		},
// 		"max_tokens":  h.config.MaxTokens,
// 		"temperature": h.config.Temperature,
// 	}

// 	body, _ := json.Marshal(requestBody)
// 	req, err := http.NewRequestWithContext(ctx, "POST", h.config.GenAIBaseURL+"/api/ai/generate", bytes.NewBuffer(body))
// 	if err != nil {
// 		return nil, fmt.Errorf("%w: %v", ErrLLMSynthesisFailed, err)
// 	}
// 	req.Header.Set("Content-Type", "application/json")

// 	var resp *http.Response
// 	var lastErr error

// 	for attempt := 0; attempt <= h.config.MaxRetries; attempt++ {
// 		if attempt > 0 {
// 			// Apply exponential backoff
// 			backoff := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
// 			select {
// 			case <-time.After(backoff):
// 				// Continue with retry
// 			case <-ctx.Done():
// 				return nil, ErrLLMTimeout
// 			}
// 		}

// 		resp, lastErr = h.client.Do(req)
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

// 		// Check if error is due to context cancellation/timeout
// 		if ctx.Err() != nil {
// 			return nil, ErrLLMTimeout
// 		}
// 	}

// 	if lastErr != nil {
// 		if ctx.Err() == context.DeadlineExceeded {
// 			return nil, ErrLLMTimeout
// 		}
// 		return nil, fmt.Errorf("%w: %v", ErrLLMSynthesisFailed, lastErr)
// 	}

// 	if resp == nil {
// 		return nil, fmt.Errorf("%w: no successful response after retries", ErrLLMSynthesisFailed)
// 	}
// 	defer resp.Body.Close()

// 	var apiResponse struct {
// 		Text       string   `json:"text"`
// 		Confidence float64  `json:"confidence"`
// 		Sources    []string `json:"sources"`
// 	}
// 	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
// 		return nil, fmt.Errorf("%w: decode error: %v", ErrLLMSynthesisFailed, err)
// 	}

// 	// Validate response
// 	if strings.TrimSpace(apiResponse.Text) == "" {
// 		apiResponse.Text = "I don't have enough information to answer that question."
// 		apiResponse.Confidence = 0.1
// 	}

// 	if apiResponse.Confidence < 0.0 || apiResponse.Confidence > 1.0 {
// 		apiResponse.Confidence = 0.5
// 	}

// 	h.logger.Info("LLM synthesis completed",
// 		zap.Float64("confidence", apiResponse.Confidence),
// 		zap.Int("sourceCount", len(apiResponse.Sources)),
// 	)

// 	return &Output{
// 		LLMResponse: apiResponse.Text,
// 		Confidence:  apiResponse.Confidence,
// 		Sources:     apiResponse.Sources,
// 	}, nil
// }

// func (h *Handler) buildPrompt(input *Input) string {
// 	var parts []string

// 	parts = append(parts, "You are a helpful franchise advisor. Answer the user's question based ONLY on the provided data.")
// 	parts = append(parts, fmt.Sprintf("\nUser Question: %s", input.Question))

// 	// Internal data
// 	if len(input.InternalData) > 0 {
// 		internalJSON, _ := json.MarshalIndent(input.InternalData, "", "  ")
// 		parts = append(parts, "\nInternal Franchise Data:")
// 		parts = append(parts, string(internalJSON))
// 	}

// 	// Web data
// 	if len(input.WebData.Sources) > 0 {
// 		parts = append(parts, "\nExternal Web Sources:")
// 		for _, src := range input.WebData.Sources {
// 			parts = append(parts, fmt.Sprintf("- %s: %s", src.Title, src.URL))
// 		}
// 		if input.WebData.Summary != "" {
// 			parts = append(parts, fmt.Sprintf("Summary: %s", input.WebData.Summary))
// 		}
// 	}

// 	parts = append(parts, "\nInstructions:")
// 	parts = append(parts, "- Cite sources when using external information")
// 	parts = append(parts, "- If data is insufficient, say so clearly")
// 	parts = append(parts, "- Keep response concise and professional")
// 	parts = append(parts, "- Return confidence score between 0.0 and 1.0")

// 	parts = append(parts, "\nAnswer:")

// 	return strings.Join(parts, "\n")
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
// 	if errors.Is(err, ErrLLMTimeout) {
// 		errorCode = "LLM_TIMEOUT"
// 	} else if errors.Is(err, ErrLLMSynthesisFailed) {
// 		errorCode = "LLM_SYNTHESIS_FAILED"
// 	}

// 	h.logger.Error("job failed",
// 		zap.Int64("jobKey", job.Key),
// 		zap.Error(err),
// 		zap.String("errorCode", errorCode),
// 	)

// 	_, _ = client.NewFailJobCommand().
// 		JobKey(job.Key).
// 		Retries(retries).
// 		ErrorMessage(err.Error()).
// 		Send(context.Background())
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }
