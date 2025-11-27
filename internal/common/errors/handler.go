// internal/common/errors/handler.go
package errors

import (
	"context"
	"encoding/json"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

// ErrorHandler handles job errors with standardized error handling
type ErrorHandler struct {
	logger Logger
}

type Logger interface {
	Error(msg string, fields map[string]interface{})
}

func NewErrorHandler(logger Logger) *ErrorHandler {
	return &ErrorHandler{logger: logger}
}

// HandleJobError handles any error in a worker job
func (h *ErrorHandler) HandleJobError(ctx context.Context, client worker.JobClient, job entities.Job, err error) {
	// Normalize to StandardError
	stdErr := h.normalizeError(err)

	// Convert to BPMN error
	bpmnErr := ConvertToBPMNError(stdErr)

	// Log
	h.logError(job, stdErr, bpmnErr)

	// Decide: retry or throw
	retries := GetRetryCount(stdErr.Code)
	if retries > 0 && job.Retries > 0 {
		h.failJobWithRetries(ctx, client, job, bpmnErr, retries)
	} else {
		h.throwBPMNError(ctx, client, job, bpmnErr)
	}
}

// normalizeError ensures we always have a StandardError
func (h *ErrorHandler) normalizeError(err error) *StandardError {
	if stdErr, ok := err.(*StandardError); ok {
		return stdErr
	}
	return &StandardError{
		Code:      "INTERNAL_ERROR",
		Message:   "Unexpected error",
		Details:   err.Error(),
		Retryable: false,
		Timestamp: time.Now().UTC(), // FIXED: Use time.Now().UTC() instead of undefined Now()
	}
}

func (h *ErrorHandler) failJobWithRetries(ctx context.Context, client worker.JobClient, job entities.Job, bpmnErr *BPMNError, maxRetries int) {
	// Camunda uses job.Retries as total remaining — so set it to maxRetries
	// But to avoid overriding, use min(job.Retries, maxRetries)
	retriesToUse := maxRetries
	if job.Retries > 0 && int(job.Retries) < maxRetries {
		retriesToUse = int(job.Retries)
	}

	vars := bpmnErr.ToErrorVariables()
	varsJSON, _ := json.Marshal(vars)

	// FIXED: Chain commands properly without type assignment issues
	cmd := client.NewFailJobCommand().
		JobKey(job.Key).
		Retries(int32(retriesToUse)).
		ErrorMessage(bpmnErr.Message)

	// Add error variables if available
	if len(vars) > 0 {
		if varsJSONStr := string(varsJSON); varsJSONStr != "null" {
			// Use the variables command directly without type assignment
			cmdWithVars, err := cmd.VariablesFromString(varsJSONStr)
			if err == nil {
				_, _ = cmdWithVars.Send(ctx)
				return
			}
		}
	}

	// Fallback: send without variables if there was an issue
	_, _ = cmd.Send(ctx)
}

func (h *ErrorHandler) throwBPMNError(ctx context.Context, client worker.JobClient, job entities.Job, bpmnErr *BPMNError) {
	vars := bpmnErr.ToErrorVariables()
	varsJSON, _ := json.Marshal(vars)

	cmd := client.NewThrowErrorCommand().
		JobKey(job.Key).
		ErrorCode(bpmnErr.Code).
		ErrorMessage(bpmnErr.Message)

	// Add error variables if available
	if len(vars) > 0 {
		if varsJSONStr := string(varsJSON); varsJSONStr != "null" {
			cmdWithVars, err := cmd.VariablesFromString(varsJSONStr)
			if err == nil {
				_, _ = cmdWithVars.Send(ctx)
				return
			}
		}
	}

	// Fallback: send without variables if there was an issue
	_, _ = cmd.Send(ctx)
}

func (h *ErrorHandler) logError(job entities.Job, stdErr *StandardError, bpmnErr *BPMNError) {
	h.logger.Error("Job failed", map[string]interface{}{
		"jobKey":           job.Key,
		"jobType":          job.Type,
		"errorCode":        string(stdErr.Code),
		"bpmnErrorCode":    bpmnErr.Code,
		"message":          bpmnErr.Message,
		"details":          stdErr.Details,
		"retryable":        stdErr.Retryable,
		"retries":          GetRetryCount(stdErr.Code),
		"errorCategory":    GetErrorCategory(stdErr.Code),
		"workflowInstance": job.ProcessInstanceKey,
	})
}
