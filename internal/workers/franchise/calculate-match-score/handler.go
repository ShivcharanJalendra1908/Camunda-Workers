// internal/workers/franchise/calculate-match-score/handler.go
package calculatematchscore

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
	TaskType = "calculate-match-score"
)

var (
	ErrMatchScoreFailed = errors.New("MATCH_SCORE_FAILED")
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
		"jobKey":      job.Key,
		"workflowKey": job.ProcessInstanceKey,
	})

	var input Input
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := h.execute(ctx, &input)
	if err != nil {
		h.failJob(client, job, "MATCH_SCORE_FAILED", err.Error(), 0)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	var profile *UserProfile
	if input.UserProfile != nil {
		profile = input.UserProfile
	} else if input.UserID != "" {
		var err error
		profile, err = h.getUserProfile(ctx, input.UserID)
		if err != nil {
			h.logger.Warn("failed to fetch user profile", map[string]interface{}{
				"userId": input.UserID,
				"error":  err,
			})
		}
	}

	if profile == nil {
		return &Output{
			MatchScore: 50,
			MatchFactors: MatchFactors{
				FinancialFit:  50,
				ExperienceFit: 50,
				LocationFit:   50,
				InterestFit:   50,
			},
		}, nil
	}

	financial := h.calculateFinancialFit(profile.CapitalAvailable, input.FranchiseData.InvestmentMin, input.FranchiseData.InvestmentMax)
	experience := h.calculateExperienceFit(profile.ExperienceYears)
	location := h.calculateLocationFit(profile.LocationPrefs, input.FranchiseData.Locations)
	interest := h.calculateInterestFit(profile.Interests, input.FranchiseData.Category)

	finalScore := int(
		float64(financial)*0.30 +
			float64(experience)*0.25 +
			float64(location)*0.20 +
			float64(interest)*0.25)

	factors := MatchFactors{
		FinancialFit:  financial,
		ExperienceFit: experience,
		LocationFit:   location,
		InterestFit:   interest,
	}

	h.logger.Info("match score calculated", map[string]interface{}{
		"userId":      input.UserID,
		"franchiseId": input.FranchiseData.ID,
		"score":       finalScore,
		"factors":     factors,
	})

	return &Output{
		MatchScore:   finalScore,
		MatchFactors: factors,
	}, nil
}

func (h *Handler) getUserProfile(ctx context.Context, userID string) (*UserProfile, error) {
	cacheKey := "user:profile:" + userID
	if val, err := h.redis.Get(ctx, cacheKey).Result(); err == nil {
		var profile UserProfile
		if err := json.Unmarshal([]byte(val), &profile); err == nil {
			return &profile, nil
		}
	}

	row := h.db.QueryRowContext(ctx, `
		SELECT capital_available, location_preferences, interests, industry_experience
		FROM users WHERE id = $1`, userID)

	var profile UserProfile
	var locPrefs, interests []byte
	err := row.Scan(&profile.CapitalAvailable, &locPrefs, &interests, &profile.ExperienceYears)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(locPrefs, &profile.LocationPrefs); err != nil {
		profile.LocationPrefs = []string{}
	}
	if err := json.Unmarshal(interests, &profile.Interests); err != nil {
		profile.Interests = []string{}
	}

	data, _ := json.Marshal(profile)
	h.redis.Set(ctx, cacheKey, data, h.config.CacheTTL)

	return &profile, nil
}

func (h *Handler) calculateFinancialFit(capital, minInvest, maxInvest int) int {
	if capital == 0 {
		return 50
	}
	if capital >= minInvest && capital <= maxInvest {
		return 100
	} else if capital > maxInvest {
		return 80
	} else if float64(capital) >= float64(minInvest)*0.8 {
		return 60
	} else if float64(capital) >= float64(minInvest)*0.5 {
		return 40
	}
	return 20
}

func (h *Handler) calculateExperienceFit(years int) int {
	if years >= 5 {
		return 100
	} else if years >= 3 {
		return 80
	} else if years >= 1 {
		return 60
	}
	return 30
}

func (h *Handler) calculateLocationFit(userLocs, franchiseLocs []string) int {
	if len(userLocs) == 0 || len(franchiseLocs) == 0 {
		return 50
	}
	for _, ul := range userLocs {
		for _, fl := range franchiseLocs {
			if ul == fl {
				return 100
			}
		}
	}
	return 30
}

func (h *Handler) calculateInterestFit(userInterests []string, category string) int {
	if len(userInterests) == 0 {
		return 50
	}
	for _, interest := range userInterests {
		if interest == category {
			return 100
		}
	}
	return 40
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

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

// // internal/workers/franchise/calculate-match-score/handler.go
// package calculatematchscore

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
// 	TaskType = "calculate-match-score"
// )

// var (
// 	ErrMatchScoreFailed = errors.New("MATCH_SCORE_FAILED")
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

// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		h.failJob(client, job, "MATCH_SCORE_FAILED", err.Error(), 0)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	var profile *UserProfile
// 	if input.UserProfile != nil {
// 		profile = input.UserProfile
// 	} else if input.UserID != "" {
// 		var err error
// 		profile, err = h.getUserProfile(ctx, input.UserID)
// 		if err != nil {
// 			h.logger.Warn("failed to fetch user profile",
// 				zap.String("userId", input.UserID),
// 				zap.Error(err),
// 			)
// 		}
// 	}

// 	if profile == nil {
// 		return &Output{
// 			MatchScore: 50,
// 			MatchFactors: MatchFactors{
// 				FinancialFit:  50,
// 				ExperienceFit: 50,
// 				LocationFit:   50,
// 				InterestFit:   50,
// 			},
// 		}, nil
// 	}

// 	financial := h.calculateFinancialFit(profile.CapitalAvailable, input.FranchiseData.InvestmentMin, input.FranchiseData.InvestmentMax)
// 	experience := h.calculateExperienceFit(profile.ExperienceYears)
// 	location := h.calculateLocationFit(profile.LocationPrefs, input.FranchiseData.Locations)
// 	interest := h.calculateInterestFit(profile.Interests, input.FranchiseData.Category)

// 	finalScore := int(
// 		float64(financial)*0.30 +
// 			float64(experience)*0.25 +
// 			float64(location)*0.20 +
// 			float64(interest)*0.25)

// 	factors := MatchFactors{
// 		FinancialFit:  financial,
// 		ExperienceFit: experience,
// 		LocationFit:   location,
// 		InterestFit:   interest,
// 	}

// 	h.logger.Info("match score calculated",
// 		zap.String("userId", input.UserID),
// 		zap.String("franchiseId", input.FranchiseData.ID),
// 		zap.Int("score", finalScore),
// 		zap.Any("factors", factors),
// 	)

// 	return &Output{
// 		MatchScore:   finalScore,
// 		MatchFactors: factors,
// 	}, nil
// }

// func (h *Handler) getUserProfile(ctx context.Context, userID string) (*UserProfile, error) {
// 	cacheKey := "user:profile:" + userID
// 	if val, err := h.redis.Get(ctx, cacheKey).Result(); err == nil {
// 		var profile UserProfile
// 		if err := json.Unmarshal([]byte(val), &profile); err == nil {
// 			return &profile, nil
// 		}
// 	}

// 	row := h.db.QueryRowContext(ctx, `
// 		SELECT capital_available, location_preferences, interests, industry_experience
// 		FROM users WHERE id = $1`, userID)

// 	var profile UserProfile
// 	var locPrefs, interests []byte
// 	err := row.Scan(&profile.CapitalAvailable, &locPrefs, &interests, &profile.ExperienceYears)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if err := json.Unmarshal(locPrefs, &profile.LocationPrefs); err != nil {
// 		profile.LocationPrefs = []string{}
// 	}
// 	if err := json.Unmarshal(interests, &profile.Interests); err != nil {
// 		profile.Interests = []string{}
// 	}

// 	data, _ := json.Marshal(profile)
// 	h.redis.Set(ctx, cacheKey, data, h.config.CacheTTL)

// 	return &profile, nil
// }

// func (h *Handler) calculateFinancialFit(capital, minInvest, maxInvest int) int {
// 	if capital == 0 {
// 		return 50
// 	}
// 	if capital >= minInvest && capital <= maxInvest {
// 		return 100
// 	} else if capital > maxInvest {
// 		return 80
// 	} else if float64(capital) >= float64(minInvest)*0.8 {
// 		return 60
// 	} else if float64(capital) >= float64(minInvest)*0.5 {
// 		return 40
// 	}
// 	return 20
// }

// func (h *Handler) calculateExperienceFit(years int) int {
// 	if years >= 5 {
// 		return 100
// 	} else if years >= 3 {
// 		return 80
// 	} else if years >= 1 {
// 		return 60
// 	}
// 	return 30
// }

// func (h *Handler) calculateLocationFit(userLocs, franchiseLocs []string) int {
// 	if len(userLocs) == 0 || len(franchiseLocs) == 0 {
// 		return 50
// 	}
// 	for _, ul := range userLocs {
// 		for _, fl := range franchiseLocs {
// 			if ul == fl {
// 				return 100
// 			}
// 		}
// 	}
// 	return 30
// }

// func (h *Handler) calculateInterestFit(userInterests []string, category string) int {
// 	if len(userInterests) == 0 {
// 		return 50
// 	}
// 	for _, interest := range userInterests {
// 		if interest == category {
// 			return 100
// 		}
// 	}
// 	return 40
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
