package querypostgresql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/models"
	"camunda-workers/internal/workers/data-access/query-postgresql/queries"
)

const (
	TaskType = "query-postgresql"
)

var (
	ErrDatabaseConnectionFailed = errors.New("DATABASE_CONNECTION_FAILED")
	ErrQueryExecutionFailed     = errors.New("QUERY_EXECUTION_FAILED")
	ErrQueryTimeout             = errors.New("QUERY_TIMEOUT")
	ErrInvalidQueryType         = errors.New("INVALID_QUERY_TYPE")
)

type Handler struct {
	config *Config
	db     *sql.DB
	logger logger.Logger
}

func NewHandler(config *Config, db *sql.DB, log logger.Logger) *Handler {
	return &Handler{
		config: config,
		db:     db,
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
		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
	defer cancel()

	output, err := h.execute(ctx, &input)
	if err != nil {
		errorCode := "QUERY_EXECUTION_FAILED"
		retries := int32(0)
		if errors.Is(err, ErrQueryTimeout) {
			errorCode = "QUERY_TIMEOUT"
			retries = 2
		} else if errors.Is(err, ErrInvalidQueryType) {
			errorCode = "INVALID_QUERY_TYPE"
			retries = 0
		}
		h.failJob(client, job, errorCode, err.Error(), retries)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	if input == nil {
		return nil, fmt.Errorf("input cannot be nil")
	}

	queryType := models.QueryType(input.QueryType)
	if _, exists := queries.Registry[queryType]; !exists {
		return nil, fmt.Errorf("%w: %s", ErrInvalidQueryType, input.QueryType)
	}

	params := make(map[string]interface{})
	if input.FranchiseID != "" {
		params["franchiseId"] = input.FranchiseID
	}
	if len(input.FranchiseIDs) > 0 {
		params["franchiseIds"] = input.FranchiseIDs
	}
	if input.UserID != "" {
		params["userId"] = input.UserID
	}
	if input.Filters != nil {
		params["filters"] = input.Filters
	}

	data, rowCount, execTime, err := queries.Execute(ctx, h.db, queryType, params)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrQueryTimeout
		}
		return nil, fmt.Errorf("%w: %v", ErrQueryExecutionFailed, err)
	}

	return &Output{
		Data:               data,
		RowCount:           rowCount,
		QueryExecutionTime: execTime,
	}, nil
}

func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
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
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error": err,
		})
	}
}

func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, retries int32) {
	h.logger.Error("job failed", map[string]interface{}{
		"jobKey":       job.Key,
		"errorCode":    errorCode,
		"errorMessage": errorMessage,
		"retries":      retries,
	})

	_, err := client.NewThrowErrorCommand().
		JobKey(job.Key).
		ErrorCode(errorCode).
		ErrorMessage(errorMessage).
		Send(context.Background())
	if err != nil {
		h.logger.Error("failed to throw error", map[string]interface{}{
			"error": err,
		})
	}
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

// package querypostgresql

// import (
// 	"context"
// 	"database/sql"
// 	"encoding/json"
// 	"errors"
// 	"fmt"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"go.uber.org/zap"

// 	"camunda-workers/internal/models"
// 	"camunda-workers/internal/workers/data-access/query-postgresql/queries"
// )

// const (
// 	TaskType = "query-postgresql"
// )

// var (
// 	ErrDatabaseConnectionFailed = errors.New("DATABASE_CONNECTION_FAILED")
// 	ErrQueryExecutionFailed     = errors.New("QUERY_EXECUTION_FAILED")
// 	ErrQueryTimeout             = errors.New("QUERY_TIMEOUT")
// 	ErrInvalidQueryType         = errors.New("INVALID_QUERY_TYPE")
// )

// type Handler struct {
// 	config *Config
// 	db     *sql.DB
// 	logger *zap.Logger
// }

// func NewHandler(config *Config, db *sql.DB, logger *zap.Logger) *Handler {
// 	return &Handler{
// 		config: config,
// 		db:     db,
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
// 		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
// 		return
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		errorCode := "QUERY_EXECUTION_FAILED"
// 		retries := int32(0)
// 		if errors.Is(err, ErrQueryTimeout) {
// 			errorCode = "QUERY_TIMEOUT"
// 			retries = 2
// 		} else if errors.Is(err, ErrInvalidQueryType) {
// 			errorCode = "INVALID_QUERY_TYPE"
// 			retries = 0
// 		}
// 		h.failJob(client, job, errorCode, err.Error(), retries)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	if input == nil {
// 		return nil, fmt.Errorf("input cannot be nil")
// 	}

// 	queryType := models.QueryType(input.QueryType)
// 	if _, exists := queries.Registry[queryType]; !exists {
// 		return nil, fmt.Errorf("%w: %s", ErrInvalidQueryType, input.QueryType)
// 	}

// 	params := make(map[string]interface{})
// 	if input.FranchiseID != "" {
// 		params["franchiseId"] = input.FranchiseID
// 	}
// 	if len(input.FranchiseIDs) > 0 {
// 		params["franchiseIds"] = input.FranchiseIDs
// 	}
// 	if input.UserID != "" {
// 		params["userId"] = input.UserID
// 	}
// 	if input.Filters != nil {
// 		params["filters"] = input.Filters
// 	}

// 	data, rowCount, execTime, err := queries.Execute(ctx, h.db, queryType, params)
// 	if err != nil {
// 		if ctx.Err() == context.DeadlineExceeded {
// 			return nil, ErrQueryTimeout
// 		}
// 		return nil, fmt.Errorf("%w: %v", ErrQueryExecutionFailed, err)
// 	}

// 	return &Output{
// 		Data:               data,
// 		RowCount:           rowCount,
// 		QueryExecutionTime: execTime,
// 	}, nil
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
// 		//Retries(retries).
// 		Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to throw error", zap.Error(err))
// 	}
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }
