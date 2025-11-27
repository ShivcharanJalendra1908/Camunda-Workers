// internal/workers/infrastructure/validate-subscription/handler.go
package validatesubscription

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"camunda-workers/internal/common/logger"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/redis/go-redis/v9"
)

const (
	TaskType = "validate-subscription"
)

var (
	ErrSubscriptionInvalid     = errors.New("SUBSCRIPTION_INVALID")
	ErrSubscriptionExpired     = errors.New("SUBSCRIPTION_EXPIRED")
	ErrSubscriptionCheckFailed = errors.New("SUBSCRIPTION_CHECK_FAILED")
)

type Handler struct {
	config *Config
	db     *sql.DB
	redis  *redis.Client
	logger logger.Logger // Changed from *zap.Logger
}

func NewHandler(config *Config, db *sql.DB, redis *redis.Client, log logger.Logger) *Handler {
	return &Handler{
		config: config,
		db:     db,
		redis:  redis,
		logger: log.WithFields(map[string]interface{}{"taskType": TaskType}), // Use WithFields instead of zap.With
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
		errorCode := "UNKNOWN_ERROR"
		retries := int32(0)
		if errors.Is(err, ErrSubscriptionInvalid) || errors.Is(err, ErrSubscriptionExpired) {
			errorCode = err.Error()
			retries = 0
		} else if errors.Is(err, ErrSubscriptionCheckFailed) {
			errorCode = "SUBSCRIPTION_CHECK_FAILED"
			retries = 3
		}
		h.failJob(client, job, errorCode, err.Error(), retries)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	cacheKey := "sub:" + input.UserID
	if val, err := h.redis.Get(ctx, cacheKey).Result(); err == nil {
		var sub Subscription
		if err := json.Unmarshal([]byte(val), &sub); err == nil {
			return &Output{IsValid: sub.IsValid, TierLevel: sub.Tier}, nil
		}
	}

	var sub Subscription
	query := `SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = $1`
	err := h.db.QueryRowContext(ctx, query, input.UserID).Scan(
		&sub.UserID, &sub.Tier, &sub.ExpiresAt, &sub.IsValid,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSubscriptionInvalid
		}
		return nil, fmt.Errorf("%w: %v", ErrSubscriptionCheckFailed, err)
	}

	if !sub.IsValid {
		return nil, ErrSubscriptionInvalid
	}

	if sub.ExpiresAt != "" {
		exp, parseErr := time.Parse(time.RFC3339, sub.ExpiresAt)
		if parseErr != nil {
			// Use the structured logger interface
			h.logger.Debug("Failed to parse expiration date, skipping expiration check", map[string]interface{}{
				"userId":    sub.UserID,
				"expiresAt": sub.ExpiresAt,
				"error":     parseErr.Error(),
			})
		} else {
			if time.Now().After(exp) {
				return nil, ErrSubscriptionExpired
			}
		}
	}

	validTiers := map[string]bool{
		"free": true, "basic": true, "premium": true, "enterprise": true,
	}
	if !validTiers[sub.Tier] {
		return nil, ErrSubscriptionInvalid
	}

	data, _ := json.Marshal(sub)
	h.redis.Set(ctx, cacheKey, data, 5*time.Minute)

	return &Output{IsValid: true, TierLevel: sub.Tier}, nil
}

func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}
	_, err = cmd.Send(context.Background())
	if err != nil {
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error": err.Error(),
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
			"error": err.Error(),
		})
	}
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

// // internal/workers/infrastructure/validate-subscription/handler.go
// package validatesubscription

// import (
// 	"context"
// 	"database/sql"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"time"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"github.com/redis/go-redis/v9"
// 	"go.uber.org/zap"
// )

// const (
// 	TaskType = "validate-subscription"
// )

// var (
// 	ErrSubscriptionInvalid     = errors.New("SUBSCRIPTION_INVALID")
// 	ErrSubscriptionExpired     = errors.New("SUBSCRIPTION_EXPIRED")
// 	ErrSubscriptionCheckFailed = errors.New("SUBSCRIPTION_CHECK_FAILED")
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
// 		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
// 		return
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		errorCode := "UNKNOWN_ERROR"
// 		retries := int32(0)
// 		if errors.Is(err, ErrSubscriptionInvalid) || errors.Is(err, ErrSubscriptionExpired) {
// 			errorCode = err.Error()
// 			retries = 0
// 		} else if errors.Is(err, ErrSubscriptionCheckFailed) {
// 			errorCode = "SUBSCRIPTION_CHECK_FAILED"
// 			retries = 3
// 		}
// 		h.failJob(client, job, errorCode, err.Error(), retries)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	cacheKey := "sub:" + input.UserID
// 	if val, err := h.redis.Get(ctx, cacheKey).Result(); err == nil {
// 		var sub Subscription
// 		if err := json.Unmarshal([]byte(val), &sub); err == nil {
// 			return &Output{IsValid: sub.IsValid, TierLevel: sub.Tier}, nil
// 		}
// 	}

// 	var sub Subscription
// 	query := `SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = $1`
// 	err := h.db.QueryRowContext(ctx, query, input.UserID).Scan(
// 		&sub.UserID, &sub.Tier, &sub.ExpiresAt, &sub.IsValid,
// 	)
// 	if err != nil {
// 		if errors.Is(err, sql.ErrNoRows) {
// 			return nil, ErrSubscriptionInvalid
// 		}
// 		return nil, fmt.Errorf("%w: %v", ErrSubscriptionCheckFailed, err)
// 	}

// 	if !sub.IsValid {
// 		return nil, ErrSubscriptionInvalid
// 	}

// 	// if sub.ExpiresAt != "" {
// 	// 	exp, _ := time.Parse(time.RFC3339, sub.ExpiresAt)
// 	// 	if time.Now().After(exp) {
// 	// 		return nil, ErrSubscriptionExpired
// 	// 	}
// 	// }

// 	if sub.ExpiresAt != "" {
// 		exp, parseErr := time.Parse(time.RFC3339, sub.ExpiresAt)
// 		if parseErr != nil {
// 			// Log the parsing error for debugging purposes (optional).
// 			h.logger.Debug("Failed to parse expiration date, skipping expiration check",
// 				zap.String("userId", sub.UserID),
// 				zap.String("expiresAt", sub.ExpiresAt),
// 				zap.Error(parseErr))
// 			// Treat an invalid date as if there's no valid expiration to check against now.
// 		} else {
// 			if time.Now().After(exp) {
// 				return nil, ErrSubscriptionExpired
// 			}
// 		}
// 	}

// 	validTiers := map[string]bool{
// 		"free": true, "basic": true, "premium": true, "enterprise": true,
// 	}
// 	if !validTiers[sub.Tier] {
// 		return nil, ErrSubscriptionInvalid
// 	}

// 	data, _ := json.Marshal(sub)
// 	h.redis.Set(ctx, cacheKey, data, 5*time.Minute)

// 	return &Output{IsValid: true, TierLevel: sub.Tier}, nil
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
