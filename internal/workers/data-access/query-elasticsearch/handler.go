package queryelasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/elastic/go-elasticsearch/v8"

	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/workers/data-access/query-elasticsearch/queries"
)

const (
	TaskType = "query-elasticsearch"
)

var (
	ErrElasticsearchConnectionFailed = errors.New("ELASTICSEARCH_CONNECTION_FAILED")
	ErrSearchQueryFailed             = errors.New("SEARCH_QUERY_FAILED")
	ErrSearchTimeout                 = errors.New("SEARCH_TIMEOUT")
	ErrIndexNotFound                 = errors.New("INDEX_NOT_FOUND")
)

type Handler struct {
	config *Config
	client *elasticsearch.Client
	logger logger.Logger
}

func NewHandler(config *Config, client *elasticsearch.Client, log logger.Logger) *Handler {
	return &Handler{
		config: config,
		client: client,
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
		errorCode := h.mapErrorToCode(err)
		retries := h.getRetryCount(err)
		h.failJob(client, job, errorCode, err.Error(), retries)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	if input == nil {
		return nil, errors.New("input cannot be nil")
	}

	params := map[string]interface{}{
		"indexName":  input.IndexName,
		"queryType":  input.QueryType,
		"filters":    input.Filters,
		"pagination": input.Pagination,
	}
	if input.FranchiseID != "" {
		params["franchiseId"] = input.FranchiseID
	}
	if input.Category != "" {
		params["category"] = input.Category
	}

	result, err := queries.Execute(ctx, h.client, params)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrSearchTimeout
		}
		if errors.Is(err, queries.ErrUnknownQueryType) {
			return nil, fmt.Errorf("%w: %v", ErrSearchQueryFailed, err)
		}
		if errors.Is(err, queries.ErrMissingIndex) {
			return nil, ErrIndexNotFound
		}
		return nil, fmt.Errorf("%w: %v", ErrSearchQueryFailed, err)
	}

	return &Output{
		Data:      result.Data,
		TotalHits: result.TotalHits,
		MaxScore:  result.MaxScore,
		Took:      result.Took,
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

func (h *Handler) mapErrorToCode(err error) string {
	if errors.Is(err, ErrIndexNotFound) {
		return "INDEX_NOT_FOUND"
	} else if errors.Is(err, ErrSearchTimeout) {
		return "SEARCH_TIMEOUT"
	} else if errors.Is(err, ErrSearchQueryFailed) {
		return "SEARCH_QUERY_FAILED"
	} else if errors.Is(err, ErrElasticsearchConnectionFailed) {
		return "ELASTICSEARCH_CONNECTION_FAILED"
	}
	return "UNKNOWN_ERROR"
}

func (h *Handler) getRetryCount(err error) int32 {
	if errors.Is(err, ErrElasticsearchConnectionFailed) || errors.Is(err, ErrSearchQueryFailed) {
		return 3
	} else if errors.Is(err, ErrSearchTimeout) {
		return 2
	}
	return 0
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

// // internal/workers/data-access/query-elasticsearch/handler.go
// package queryelasticsearch

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"github.com/elastic/go-elasticsearch/v8"
// 	"go.uber.org/zap"

// 	"camunda-workers/internal/workers/data-access/query-elasticsearch/queries"
// )

// const (
// 	TaskType = "query-elasticsearch"
// )

// var (
// 	ErrElasticsearchConnectionFailed = errors.New("ELASTICSEARCH_CONNECTION_FAILED")
// 	ErrSearchQueryFailed             = errors.New("SEARCH_QUERY_FAILED")
// 	ErrSearchTimeout                 = errors.New("SEARCH_TIMEOUT")
// 	ErrIndexNotFound                 = errors.New("INDEX_NOT_FOUND")
// )

// type Handler struct {
// 	config *Config
// 	client *elasticsearch.Client
// 	logger *zap.Logger
// }

// func NewHandler(config *Config, client *elasticsearch.Client, logger *zap.Logger) *Handler {
// 	return &Handler{
// 		config: config,
// 		client: client,
// 		logger: logger.With(zap.String("taskType", TaskType)), // ✅ FIXED - This was the only issue.
// 	}
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	h.logger.Info("processing job",
// 		zap.Int64("jobKey", job.Key),
// 		zap.Int64("workflowKey", job.ProcessInstanceKey),
// 	)

// 	var input Input
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil { // ✅ job.Variables is []byte
// 		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
// 		return
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		errorCode := h.mapErrorToCode(err)
// 		retries := h.getRetryCount(err)
// 		h.failJob(client, job, errorCode, err.Error(), retries)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	if input == nil {
// 		return nil, errors.New("input cannot be nil")
// 	}

// 	params := map[string]interface{}{
// 		"indexName":  input.IndexName,
// 		"queryType":  input.QueryType,
// 		"filters":    input.Filters,
// 		"pagination": input.Pagination,
// 	}
// 	if input.FranchiseID != "" {
// 		params["franchiseId"] = input.FranchiseID
// 	}
// 	if input.Category != "" {
// 		params["category"] = input.Category
// 	}

// 	result, err := queries.Execute(ctx, h.client, params)
// 	if err != nil {
// 		if ctx.Err() == context.DeadlineExceeded {
// 			return nil, ErrSearchTimeout
// 		}
// 		if errors.Is(err, queries.ErrUnknownQueryType) {
// 			return nil, fmt.Errorf("%w: %v", ErrSearchQueryFailed, err)
// 		}
// 		if errors.Is(err, queries.ErrMissingIndex) {
// 			return nil, ErrIndexNotFound
// 		}
// 		return nil, fmt.Errorf("%w: %v", ErrSearchQueryFailed, err)
// 	}

// 	return &Output{
// 		Data:      result.Data,
// 		TotalHits: result.TotalHits,
// 		MaxScore:  result.MaxScore,
// 		Took:      result.Took,
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
// 		Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to throw error", zap.Error(err))
// 	}
// }

// func (h *Handler) mapErrorToCode(err error) string {
// 	if errors.Is(err, ErrIndexNotFound) {
// 		return "INDEX_NOT_FOUND"
// 	} else if errors.Is(err, ErrSearchTimeout) {
// 		return "SEARCH_TIMEOUT"
// 	} else if errors.Is(err, ErrSearchQueryFailed) {
// 		return "SEARCH_QUERY_FAILED"
// 	} else if errors.Is(err, ErrElasticsearchConnectionFailed) {
// 		return "ELASTICSEARCH_CONNECTION_FAILED"
// 	}
// 	return "UNKNOWN_ERROR"
// }

// func (h *Handler) getRetryCount(err error) int32 {
// 	if errors.Is(err, ErrElasticsearchConnectionFailed) || errors.Is(err, ErrSearchQueryFailed) {
// 		return 3
// 	} else if errors.Is(err, ErrSearchTimeout) {
// 		return 2
// 	}
// 	return 0
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }
