// internal/common/camunda/worker.go
package camunda

import (
	"context"

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
