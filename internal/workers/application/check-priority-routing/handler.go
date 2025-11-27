// internal/workers/application/check-priority-routing/handler.go
package checkpriorityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/redis/go-redis/v9"
	"camunda-workers/internal/common/logger"
)

const (
	TaskType = "check-priority-routing"
)

type Handler struct {
	config *Config
	db     *sql.DB
	redis  *redis.Client
	logger logger.Logger
}

func NewHandler(config *Config, db *sql.DB, redis *redis.Client, log logger.Logger) *Handler {
	return &Handler{
		config: config,
		db:     db,
		redis:  redis,
		logger: log.WithFields(map[string]interface{}{"taskType": TaskType}),
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	h.logger.Info("processing job", map[string]interface{}{
		"jobKey":       job.Key,
		"workflowKey":  job.ProcessInstanceKey,
	})

	var input Input
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		h.failJob(client, job, "PRIORITY_ROUTING_FAILED", fmt.Sprintf("parse input: %v", err), 0)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := h.execute(ctx, &input)
	if err != nil {
		h.failJob(client, job, "PRIORITY_ROUTING_FAILED", err.Error(), 0)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	accountType, err := h.getFranchisorAccountType(ctx, input.FranchiseID)
	if err != nil {
		h.logger.Warn("failed to fetch franchisor account type, defaulting to standard", map[string]interface{}{
			"franchiseId": input.FranchiseID,
			"error":       err,
		})
		accountType = AccountTypeStandard
	}

	isPremium := accountType == AccountTypePremium
	priority := h.determinePriority(accountType)

	h.logger.Info("priority routing determined", map[string]interface{}{
		"franchiseId": input.FranchiseID,
		"accountType": accountType,
		"isPremium":   isPremium,
		"priority":    priority,
	})

	return &Output{
		IsPremiumFranchisor: isPremium,
		RoutingPriority:     priority,
	}, nil
}

func (h *Handler) getFranchisorAccountType(ctx context.Context, franchiseID string) (string, error) {
	cacheKey := "franchisor:account:" + franchiseID
	if val, err := h.redis.Get(ctx, cacheKey).Result(); err == nil {
		return val, nil
	}

	row := h.db.QueryRowContext(ctx, `
		SELECT account_type 
		FROM franchisors 
		WHERE franchise_id = $1`, franchiseID)

	var accountType string
	err := row.Scan(&accountType)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("franchisor not found for franchise %s", franchiseID)
		}
		return "", fmt.Errorf("database error: %w", err)
	}

	switch accountType {
	case AccountTypePremium, AccountTypeVerified, AccountTypeStandard:
		// valid
	default:
		accountType = AccountTypeStandard
	}

	h.redis.Set(ctx, cacheKey, accountType, h.config.CacheTTL)
	return accountType, nil
}

func (h *Handler) determinePriority(accountType string) string {
	switch accountType {
	case AccountTypePremium:
		return PriorityHigh
	case AccountTypeVerified:
		return PriorityMedium
	default:
		return PriorityLow
	}
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

func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, _ int32) {
	h.logger.Error("job failed", map[string]interface{}{
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
		h.logger.Error("failed to throw error", map[string]interface{}{
			"error": err,
		})
	}
}

// // internal/workers/application/check-priority-routing/handler.go
// package checkpriorityrouting

// import (
// 	"context"
// 	"database/sql"
// 	"encoding/json"
// 	"fmt"
// 	"time"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"github.com/redis/go-redis/v9"
// 	"go.uber.org/zap"
// )

// const (
// 	TaskType = "check-priority-routing"
// )

// type Handler struct {
// 	config *Config
// 	db     *sql.DB
// 	redis  *redis.Client
// 	logger *zap.Logger
// }

// func NewHandler(config *Config, db *sql.DB, redis *redis.Client, logger *zap.Logger) *Handler {
// 	return &Handler{
// 		config: config,
// 		db:     db,
// 		redis:  redis,
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
// 		h.failJob(client, job, "PRIORITY_ROUTING_FAILED", fmt.Sprintf("parse input: %v", err), 0)
// 		return
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		h.failJob(client, job, "PRIORITY_ROUTING_FAILED", err.Error(), 0)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	accountType, err := h.getFranchisorAccountType(ctx, input.FranchiseID)
// 	if err != nil {
// 		h.logger.Warn("failed to fetch franchisor account type, defaulting to standard",
// 			zap.String("franchiseId", input.FranchiseID),
// 			zap.Error(err),
// 		)
// 		accountType = AccountTypeStandard
// 	}

// 	isPremium := accountType == AccountTypePremium
// 	priority := h.determinePriority(accountType)

// 	h.logger.Info("priority routing determined",
// 		zap.String("franchiseId", input.FranchiseID),
// 		zap.String("accountType", accountType),
// 		zap.Bool("isPremium", isPremium),
// 		zap.String("priority", priority),
// 	)

// 	return &Output{
// 		IsPremiumFranchisor: isPremium,
// 		RoutingPriority:     priority,
// 	}, nil
// }

// func (h *Handler) getFranchisorAccountType(ctx context.Context, franchiseID string) (string, error) {
// 	cacheKey := "franchisor:account:" + franchiseID
// 	if val, err := h.redis.Get(ctx, cacheKey).Result(); err == nil {
// 		return val, nil
// 	}

// 	row := h.db.QueryRowContext(ctx, `
// 		SELECT account_type 
// 		FROM franchisors 
// 		WHERE franchise_id = $1`, franchiseID)

// 	var accountType string
// 	err := row.Scan(&accountType)
// 	if err != nil {
// 		if err == sql.ErrNoRows {
// 			return "", fmt.Errorf("franchisor not found for franchise %s", franchiseID)
// 		}
// 		return "", fmt.Errorf("database error: %w", err)
// 	}

// 	switch accountType {
// 	case AccountTypePremium, AccountTypeVerified, AccountTypeStandard:
// 		// valid
// 	default:
// 		accountType = AccountTypeStandard
// 	}

// 	h.redis.Set(ctx, cacheKey, accountType, h.config.CacheTTL)
// 	return accountType, nil
// }

// func (h *Handler) determinePriority(accountType string) string {
// 	switch accountType {
// 	case AccountTypePremium:
// 		return PriorityHigh
// 	case AccountTypeVerified:
// 		return PriorityMedium
// 	default:
// 		return PriorityLow
// 	}
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