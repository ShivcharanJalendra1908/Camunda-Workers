// internal/common/camunda/worker.go
package camunda

import (
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/observability"
	"context"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/camunda/zeebe/clients/go/v8/pkg/zbc"
	"go.uber.org/zap"
)

// JobHandler must return an error (required by Zeebe client)
type JobHandler interface {
	Handle(client worker.JobClient, job entities.Job) error
}

type CamundaWorker struct {
	client   zbc.Client
	worker   worker.JobWorker
	logger   *zap.Logger
	taskType string
}

func NewWorker(
	client zbc.Client,
	taskType string,
	maxJobsActive int,
	handler JobHandler,
	logger *zap.Logger,
) *CamundaWorker {
	// Wrap handler to match Zeebe's expected signature
	jobWorker := client.NewJobWorker().
		JobType(taskType).
		Handler(func(client worker.JobClient, job entities.Job) {
			if err := handler.Handle(client, job); err != nil {
				logger.Error("Handler returned error", zap.Error(err), zap.Int64("jobKey", job.Key))
				// Optionally fail the job here if needed
			}
		}).
		MaxJobsActive(maxJobsActive).
		Open()

	return &CamundaWorker{
		client:   client,
		worker:   jobWorker,
		logger:   logger,
		taskType: taskType,
	}
}

func (w *CamundaWorker) Start() {
	w.logger.Info("worker started", zap.String("taskType", w.taskType))
}

func (w *CamundaWorker) Stop(ctx context.Context) {
	w.logger.Info("stopping worker", zap.String("taskType", w.taskType))
	w.worker.Close()
	w.client.Close()
}

// ============================================================================
// ERROR HANDLING INTEGRATION (GAP #4)
// ============================================================================

// JobHandlerWithErrorHandling wraps a job handler with standardized error handling
type JobHandlerWithErrorHandling struct {
	handler      JobHandler
	errorHandler *errors.ErrorHandler
	metrics      *observability.Observability
	workerName   string
}

// NewJobHandlerWithErrorHandling creates a wrapped handler with error handling
func NewJobHandlerWithErrorHandling(
	handler JobHandler,
	errorHandler *errors.ErrorHandler,
	metrics *observability.Observability,
	workerName string,
) *JobHandlerWithErrorHandling {
	return &JobHandlerWithErrorHandling{
		handler:      handler,
		errorHandler: errorHandler,
		metrics:      metrics,
		workerName:   workerName,
	}
}

// Handle implements JobHandler interface with error handling
func (h *JobHandlerWithErrorHandling) Handle(client worker.JobClient, job entities.Job) error {
	ctx := context.Background()
	startTime := time.Now()

	// Execute the actual handler
	err := h.handler.Handle(client, job)

	duration := time.Since(startTime)

	if err != nil {
		// Record error metrics
		if stdErr, ok := err.(*errors.StandardError); ok {
			if h.metrics != nil {
				h.metrics.RecordError(ctx, stdErr.Code, h.workerName)

				// Record retry if applicable
				if stdErr.Retryable && job.Retries > 0 {
					maxRetries := errors.GetRetryCount(stdErr.Code)
					currentRetry := maxRetries - int(job.Retries)
					h.metrics.RecordRetry(ctx, stdErr.Code, currentRetry, h.workerName)
				}
			}
		}

		// Use centralized error handler
		h.errorHandler.HandleJobError(ctx, client, job, err)

		// Record failed job
		if h.metrics != nil {
			h.metrics.RecordJobProcessed(ctx, "failed")
			h.metrics.RecordJobDuration(ctx, duration, "failed")
		}

		return err
	}

	// Record successful job
	if h.metrics != nil {
		h.metrics.RecordJobProcessed(ctx, "success")
		h.metrics.RecordJobDuration(ctx, duration, "success")
	}

	return nil
}
