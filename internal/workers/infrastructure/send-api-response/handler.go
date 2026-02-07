// ============================================================
// FILE: internal/workers/infrastructure/send-api-response/handler.go
// NEW WORKER: Sends workflow result back to API handler
// ============================================================

package send_api_response

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/pkg/registry"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const TaskType = "send-api-response"

type Handler struct {
	logger       logger.Logger
	errorHandler *errors.ErrorHandler
	deps         *registry.Dependencies
}

// ResponseHandler interface - injected from API gateway
type ResponseHandler interface {
	ReceiveWorkflowResponse(correlationKey string, response map[string]interface{}) error
}

func init() {
	registry.RegisterWorker(TaskType, func(deps *registry.Dependencies) (registry.WorkerHandler, error) {
		config := &Config{
			Timeout: 10 * time.Second,
		}
		return NewHandler(config, deps.Logger, deps), nil // ✅ CHANGED
	})
}

func NewHandler(config *Config, log logger.Logger, deps *registry.Dependencies) *Handler {
	if config == nil {
		config = &Config{
			Timeout: 30 * time.Second,
		}
	}

	return &Handler{
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: errors.NewErrorHandler(log),
		deps:         deps, // ✅ CHANGED
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	ctx := context.Background()

	// Extract trace context
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

	// Create worker span
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

	// Step 1: Parse input
	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "send-api-response.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			errors.NewValidationError("input", fmt.Sprintf("parse input: %v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	// Step 2: Validate input
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "send-api-response.validateInput")
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	// Step 3: Execute business logic
	ctxExec, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "send-api-response.ExecuteWorker")
	output, err := h.ExecuteWorker(ctxExec, &input)
	spanExec.End()

	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			errors.NewExternalServiceError("send-api-response", err))
		return
	}

	// Step 4: Complete job
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "send-api-response.completeJob")
	h.completeJob(ctx, client, job, output)
	spanComp.End()

	h.logger.Info("response sent to API successfully",
		map[string]interface{}{
			"correlationKey": input.CorrelationKey,
			"sent":           output.SentToAPI,
			"workflowKey":    job.ProcessInstanceKey,
		})
}

func (h *Handler) validateInput(input *Input) error {
	// Validate correlation key
	if err := ozzo.Validate(input.CorrelationKey,
		ozzo.Required.Error("correlationKey is required"),
		ozzo.Length(1, 100).Error("correlationKey must be 1-100 characters"),
	); err != nil {
		return errors.NewValidationError("correlationKey", err.Error())
	}

	// Validate response
	if input.Response == nil {
		return errors.NewValidationError("response", "response object is required")
	}

	// Validate response size
	jsonBytes, err := json.Marshal(input.Response)
	if err != nil {
		return errors.NewValidationError("response", fmt.Sprintf("cannot serialize response: %v", err))
	}

	sizeKB := len(jsonBytes) / 1024
	if sizeKB > 500 { // 500KB max
		return errors.NewValidationError("response",
			fmt.Sprintf("response too large (%d KB), maximum is 500 KB", sizeKB))
	}

	return nil
}

func (h *Handler) ExecuteWorker(ctx context.Context, input *Input) (*Output, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	output := &Output{
		Success:   true,
		SentToAPI: false,
	}

	// ✅ Check if Redis available
	if h.deps == nil || h.deps.RedisClient == nil {
		h.logger.Warn("Redis not available", map[string]interface{}{
			"correlationKey": input.CorrelationKey,
		})
		output.Success = false
		output.Error = "Redis not available"
		return output, nil
	}

	// ✅ Publish to Redis
	channel := fmt.Sprintf("workflow:response:%s", input.CorrelationKey)
	payload, err := json.Marshal(input.Response)
	if err != nil {
		h.logger.Error("Failed to marshal response", map[string]interface{}{
			"error":          err.Error(),
			"correlationKey": input.CorrelationKey,
		})
		return output, fmt.Errorf("failed to marshal response: %w", err)
	}

	redisClient := h.deps.RedisClient.GetClient()

	// Publish to channel
	if err := redisClient.Publish(ctx, channel, payload).Err(); err != nil {
		h.logger.Error("Failed to publish to Redis", map[string]interface{}{
			"error":          err.Error(),
			"correlationKey": input.CorrelationKey,
			"channel":        channel,
		})
		output.Success = false
		output.ApiError = err.Error()
		return output, nil
	}

	h.logger.Info("Response published to Redis successfully", map[string]interface{}{
		"correlationKey": input.CorrelationKey,
		"channel":        channel,
		"payloadSize":    len(payload),
	})

	// Cache as backup
	cacheKey := fmt.Sprintf("workflow:response:cache:%s", input.CorrelationKey)
	if err := redisClient.Set(ctx, cacheKey, payload, 2*time.Minute).Err(); err != nil {
		h.logger.Warn("Failed to cache response", map[string]interface{}{
			"error": err.Error(),
		})
	}

	output.SentToAPI = true
	return output, nil
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
				"error":   err,
				"traceId": span.SpanContext().TraceID().String(),
			})
		return
	}

	_, err = cmd.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to send complete job command",
			map[string]interface{}{
				"error":   err,
				"traceId": span.SpanContext().TraceID().String(),
			})
	}
}

// ✅ THIS IS THE registry.WorkerHandler INTERFACE METHOD
func (h *Handler) Execute(ctx context.Context, task *registry.Task) (map[string]interface{}, error) {
	var input Input

	// Parse input from task variables
	if task.Variables != nil {
		if corrKey, ok := task.Variables["correlationKey"].(string); ok {
			input.CorrelationKey = corrKey
		}
		if response, ok := task.Variables["response"].(map[string]interface{}); ok {
			input.Response = response
		}
		if metadata, ok := task.Variables["metadata"].(map[string]interface{}); ok {
			input.Metadata = metadata
		}
	}

	// Validate input
	if err := h.validateInput(&input); err != nil {
		return nil, err
	}

	// Execute the handler (using renamed method)
	output, err := h.ExecuteWorker(ctx, &input)
	if err != nil {
		return nil, err
	}

	// Convert to map
	result := map[string]interface{}{
		"success":   output.Success,
		"sentToApi": output.SentToAPI,
	}
	if output.Error != "" {
		result["error"] = output.Error
	}
	if output.ApiError != "" {
		result["apiError"] = output.ApiError
	}

	return result, nil
}

// // ============================================================
// // FILE: internal/workers/infrastructure/send-api-response/handler.go
// // NEW WORKER: Sends workflow result back to API handler
// // ============================================================

// package send_api_response

// import (
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"time"

// 	"camunda-workers/internal/common/errors"
// 	"camunda-workers/internal/common/logger"
// 	"camunda-workers/pkg/registry"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	ozzo "github.com/go-ozzo/ozzo-validation/v4"

// 	"go.opentelemetry.io/otel"
// 	"go.opentelemetry.io/otel/attribute"
// 	"go.opentelemetry.io/otel/trace"
// )

// const TaskType = "send-api-response"

// type Handler struct {
// 	logger          logger.Logger
// 	errorHandler    *errors.ErrorHandler
// 	responseHandler ResponseHandler
// }

// // ResponseHandler interface - injected from API gateway
// type ResponseHandler interface {
// 	ReceiveWorkflowResponse(correlationKey string, response map[string]interface{}) error
// }

// func init() {
// 	registry.RegisterWorker(TaskType, func(deps *registry.Dependencies) (registry.WorkerHandler, error) {
// 		config := &Config{
// 			Timeout: 10 * time.Second, // Default
// 		}
// 		return NewHandler(config, deps.Logger, deps.ResponseHandler), nil
// 	})
// }

// func NewHandler(config *Config, log logger.Logger, responseHandler ResponseHandler) *Handler {
// 	if config == nil {
// 		config = &Config{
// 			Timeout: 30 * time.Second,
// 		}
// 	}

// 	return &Handler{
// 		logger:          log.WithFields(map[string]interface{}{"taskType": TaskType}),
// 		errorHandler:    errors.NewErrorHandler(log),
// 		responseHandler: responseHandler,
// 	}
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	ctx := context.Background()

// 	// Extract trace context
// 	var traceID, parentSpanID string
// 	var jobVars map[string]interface{}

// 	if err := json.Unmarshal([]byte(job.Variables), &jobVars); err == nil {
// 		if tid, ok := jobVars["traceId"].(string); ok {
// 			traceID = tid
// 		}
// 		if psid, ok := jobVars["spanId"].(string); ok {
// 			parentSpanID = psid
// 		}
// 	}

// 	// Create worker span
// 	tracer := otel.Tracer("worker-manager")
// 	ctx, span := tracer.Start(ctx, "worker:"+TaskType,
// 		trace.WithAttributes(
// 			attribute.String("worker.name", TaskType),
// 			attribute.Int64("job.key", job.GetKey()),
// 			attribute.Int64("workflow.instance_key", job.GetProcessInstanceKey()),
// 			attribute.String("workflow.process_id", job.GetBpmnProcessId()),
// 			attribute.String("workflow.element_id", job.GetElementId()),
// 			attribute.String("trace.parent_id", parentSpanID),
// 		),
// 	)
// 	defer span.End()

// 	h.logger.Info("processing job",
// 		map[string]interface{}{
// 			"jobKey":      job.Key,
// 			"workflowKey": job.ProcessInstanceKey,
// 			"traceId":     traceID,
// 			"spanId":      span.SpanContext().SpanID().String(),
// 		})

// 	// Step 1: Parse input
// 	var input Input
// 	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "send-api-response.parseInput")
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
// 		span.RecordError(err)
// 		span.SetAttributes(attribute.Bool("error", true))
// 		h.errorHandler.HandleJobError(ctx, client, job,
// 			errors.NewValidationError("input", fmt.Sprintf("parse input: %v", err)))
// 		spanParse.End()
// 		return
// 	}
// 	spanParse.End()

// 	// Step 2: Validate input
// 	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "send-api-response.validateInput")
// 	if err := h.validateInput(&input); err != nil {
// 		span.RecordError(err)
// 		span.SetAttributes(attribute.Bool("error", true))
// 		h.errorHandler.HandleJobError(ctx, client, job, err)
// 		spanValidate.End()
// 		return
// 	}
// 	spanValidate.End()

// 	// Step 3: Execute business logic
// 	ctxExec, cancel := context.WithTimeout(ctx, 30*time.Second)
// 	defer cancel()

// 	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "send-api-response.ExecuteWorker")
// 	output, err := h.ExecuteWorker(ctxExec, &input)
// 	spanExec.End()

// 	if err != nil {
// 		span.RecordError(err)
// 		span.SetAttributes(attribute.Bool("error", true))
// 		h.errorHandler.HandleJobError(ctx, client, job,
// 			errors.NewExternalServiceError("send-api-response", err))
// 		return
// 	}

// 	// Step 4: Complete job
// 	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "send-api-response.completeJob")
// 	h.completeJob(ctx, client, job, output)
// 	spanComp.End()

// 	h.logger.Info("response sent to API successfully",
// 		map[string]interface{}{
// 			"correlationKey": input.CorrelationKey,
// 			"sent":           output.SentToAPI,
// 			"workflowKey":    job.ProcessInstanceKey,
// 		})
// }

// func (h *Handler) validateInput(input *Input) error {
// 	// Validate correlation key
// 	if err := ozzo.Validate(input.CorrelationKey,
// 		ozzo.Required.Error("correlationKey is required"),
// 		ozzo.Length(1, 100).Error("correlationKey must be 1-100 characters"),
// 	); err != nil {
// 		return errors.NewValidationError("correlationKey", err.Error())
// 	}

// 	// Validate response
// 	if input.Response == nil {
// 		return errors.NewValidationError("response", "response object is required")
// 	}

// 	// Validate response size
// 	jsonBytes, err := json.Marshal(input.Response)
// 	if err != nil {
// 		return errors.NewValidationError("response", fmt.Sprintf("cannot serialize response: %v", err))
// 	}

// 	sizeKB := len(jsonBytes) / 1024
// 	if sizeKB > 500 { // 500KB max
// 		return errors.NewValidationError("response",
// 			fmt.Sprintf("response too large (%d KB), maximum is 500 KB", sizeKB))
// 	}

// 	return nil
// }

// func (h *Handler) ExecuteWorker(ctx context.Context, input *Input) (*Output, error) {
// 	// ✅ Add timeout to prevent infinite waiting
// 	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
// 	defer cancel()

// 	output := &Output{
// 		Success:   true,
// 		SentToAPI: false,
// 	}

// 	// ✅ Check if response handler exists
// 	if h.responseHandler == nil {
// 		h.logger.Warn("No response handler registered", map[string]interface{}{
// 			"correlationKey": input.CorrelationKey,
// 		})
// 		output.Success = false
// 		output.Error = "No response handler available"
// 		return output, nil // ✅ Don't fail the job
// 	}

// 	// ✅ Send response with error handling
// 	err := h.responseHandler.ReceiveWorkflowResponse(input.CorrelationKey, input.Response)

// 	if err != nil {
// 		h.logger.Warn("Failed to deliver response", map[string]interface{}{
// 			"correlationKey": input.CorrelationKey,
// 			"error":          err.Error(),
// 		})

// 		output.Success = true
// 		output.ApiError = err.Error()

// 		// ✅ CRITICAL: Don't fail the job, just mark as unsuccessful
// 		return output, nil
// 	}

// 	output.SentToAPI = true
// 	h.logger.Info("Response sent successfully", map[string]interface{}{
// 		"correlationKey": input.CorrelationKey,
// 	})

// 	return output, nil
// }

// // // ✅ RENAMED: Changed from Execute to ExecuteWorker
// // func (h *Handler) ExecuteWorker(ctx context.Context, input *Input) (*Output, error) {
// // 	output := &Output{
// // 		Success:   true,
// // 		SentToAPI: false,
// // 	}

// // 	// Send response to API gateway
// // 	if h.responseHandler != nil {
// // 		err := h.responseHandler.ReceiveWorkflowResponse(input.CorrelationKey, input.Response)
// // 		if err != nil {
// // 			output.Success = false
// // 			output.ApiError = err.Error()
// // 			h.logger.Error("failed to send response to API",
// // 				map[string]interface{}{
// // 					"correlationKey": input.CorrelationKey,
// // 					"error":          err.Error(),
// // 					"traceId":        trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
// // 				})
// // 			return output, fmt.Errorf("api gateway error: %w", err)
// // 		}
// // 		output.SentToAPI = true
// // 	} else {
// // 		h.logger.Warn("no response handler registered, skipping API response",
// // 			map[string]interface{}{
// // 				"correlationKey": input.CorrelationKey,
// // 				"traceId":        trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
// // 			})
// // 	}

// // 	return output, nil
// // }

// func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
// 	cmd, err := client.NewCompleteJobCommand().
// 		JobKey(job.Key).
// 		VariablesFromObject(output)

// 	if err != nil {
// 		span := trace.SpanFromContext(ctx)
// 		span.RecordError(err)
// 		h.logger.Error("failed to create complete job command",
// 			map[string]interface{}{
// 				"error":   err,
// 				"traceId": span.SpanContext().TraceID().String(),
// 			})
// 		return
// 	}

// 	_, err = cmd.Send(ctx)
// 	if err != nil {
// 		span := trace.SpanFromContext(ctx)
// 		span.RecordError(err)
// 		h.logger.Error("failed to send complete job command",
// 			map[string]interface{}{
// 				"error":   err,
// 				"traceId": span.SpanContext().TraceID().String(),
// 			})
// 	}
// }

// // ✅ THIS IS THE registry.WorkerHandler INTERFACE METHOD
// func (h *Handler) Execute(ctx context.Context, task *registry.Task) (map[string]interface{}, error) {
// 	var input Input

// 	// Parse input from task variables
// 	if task.Variables != nil {
// 		if corrKey, ok := task.Variables["correlationKey"].(string); ok {
// 			input.CorrelationKey = corrKey
// 		}
// 		if response, ok := task.Variables["response"].(map[string]interface{}); ok {
// 			input.Response = response
// 		}
// 		if metadata, ok := task.Variables["metadata"].(map[string]interface{}); ok {
// 			input.Metadata = metadata
// 		}
// 	}

// 	// Validate input
// 	if err := h.validateInput(&input); err != nil {
// 		return nil, err
// 	}

// 	// Execute the handler (using renamed method)
// 	output, err := h.ExecuteWorker(ctx, &input)
// 	if err != nil {
// 		return nil, err
// 	}

// 	// Convert to map
// 	result := map[string]interface{}{
// 		"success":   output.Success,
// 		"sentToApi": output.SentToAPI,
// 	}
// 	if output.Error != "" {
// 		result["error"] = output.Error
// 	}
// 	if output.ApiError != "" {
// 		result["apiError"] = output.ApiError
// 	}

// 	return result, nil
// }
