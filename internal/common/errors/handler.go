// internal/common/errors/handler.go
package errors

import (
	"context"
	"encoding/json"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

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
		// Backfill missing fields
		if stdErr.ID == "" {
			stdErr.ID = uuid.New().String()
		}
		if stdErr.Timestamp.IsZero() {
			stdErr.Timestamp = time.Now().UTC()
		}
		if stdErr.StackTrace == "" {
			stdErr.StackTrace = string(debug.Stack())
		}
		return stdErr
	}
	return &StandardError{
		ID:         uuid.New().String(),
		Code:       "INTERNAL_ERROR",
		Message:    "Unexpected error",
		Details:    err.Error(),
		Retryable:  false,
		Timestamp:  time.Now().UTC(),
		StackTrace: string(debug.Stack()),
	}
}

func (h *ErrorHandler) failJobWithRetries(ctx context.Context, client worker.JobClient, job entities.Job, bpmnErr *BPMNError, maxRetries int) {
	// Determine retries to use
	retriesToUse := maxRetries
	if job.Retries > 0 && int(job.Retries) < maxRetries {
		retriesToUse = int(job.Retries)
	}
	// Compute attempt index (1-based)
	attempt := maxRetries - retriesToUse + 1
	if attempt < 1 {
		attempt = 1
	}
	backoff := computeBackoff(attempt)

	vars := bpmnErr.ToErrorVariables()
	vars["retryBackoffMs"] = backoff.Milliseconds()
	varsJSON, _ := json.Marshal(vars)

	// Optional local backoff to space retries
	time.Sleep(backoff)

	cmd := client.NewFailJobCommand().
		JobKey(job.Key).
		Retries(int32(retriesToUse)).
		ErrorMessage(bpmnErr.Message)

	if len(vars) > 0 {
		if varsJSONStr := string(varsJSON); varsJSONStr != "null" {
			cmdWithVars, err := cmd.VariablesFromString(varsJSONStr)
			if err == nil {
				_, _ = cmdWithVars.Send(ctx)
				return
			}
		}
	}

	_, _ = cmd.Send(ctx)
}

func computeBackoff(attempt int) time.Duration {
	base := time.Second
	max := 30 * time.Second
	// Exponential: 1s, 2s, 4s, 8s, capped
	backoff := base * time.Duration(1<<uint(attempt-1))
	if backoff > max {
		backoff = max
	}
	return backoff
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
	h.logErrorWithContext(job, stdErr, bpmnErr)
}

// getSeverity determines log severity based on error category and context
func getSeverity(code ErrorCode) string {
	cat := GetErrorCategory(code)
	switch cat {
	case CategoryValidation, CategoryClient:
		return "WARN"
	case CategoryTransient:
		return "ERROR"
	case CategoryDependency:
		return "HIGH"
	case CategoryPermanent:
		return "CRITICAL"
	default:
		return "ERROR"
	}
}

// Enhanced logging with full error context
func (h *ErrorHandler) logErrorWithContext(job entities.Job, stdErr *StandardError, _ *BPMNError) {
	// Create comprehensive job context
	jobContext := map[string]interface{}{
		"workflowInstanceKey":  job.ProcessInstanceKey,
		"jobKey":               job.Key,
		"jobType":              job.Type,
		"processDefinitionKey": job.ProcessDefinitionKey,
		"bpmnProcessId":        job.BpmnProcessId,
		"elementId":            job.ElementId,
		"retries":              job.Retries,
		"deadline":             job.Deadline,
		"worker":               job.Worker,
	}

	// Create full error context
	errorCtx := NewErrorContext(stdErr, jobContext)

	// Log based on severity
	severity := getSeverity(stdErr.Code)
	logFields := map[string]interface{}{
		"errorContext": errorCtx,
		"stackTrace":   stdErr.StackTrace,
	}

	switch severity {
	case "CRITICAL":
		h.logger.Error("CRITICAL: Job failed - immediate attention required", logFields)
	case "HIGH":
		h.logger.Error("HIGH: Job failed - dependency issue", logFields)
	case "ERROR":
		h.logger.Error("ERROR: Job failed - retryable error", logFields)
	case "WARN":
		h.logger.Error("WARN: Job failed - validation/client error", logFields)
	default:
		h.logger.Error("Job failed", logFields)
	}
}
