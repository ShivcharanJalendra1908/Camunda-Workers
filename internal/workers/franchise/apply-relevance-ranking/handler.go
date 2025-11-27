// internal/workers/franchise/apply-relevance-ranking/handler.go
package applyrelevanceranking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"camunda-workers/internal/common/logger"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const (
	TaskType = "apply-relevance-ranking"
)

var (
	ErrNilInput = errors.New("input cannot be nil")
)

type Handler struct {
	config *Config
	logger logger.Logger
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	return &Handler{
		config: config,
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
		h.failJob(client, job, "RANKING_FAILED", err.Error(), 0)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(_ context.Context, input *Input) (*Output, error) {
	// Validate input as per REQ-BIZ-007
	if input == nil {
		return nil, ErrNilInput
	}

	start := time.Now()

	// Build map of details for O(1) lookup
	detailsMap := make(map[string]FranchiseDetail)
	for _, d := range input.DetailsData {
		detailsMap[d.ID] = d
	}

	// Track processed IDs to avoid duplicates (REQ-BIZ-008)
	processedIDs := make(map[string]bool)
	var ranked []RankedFranchise

	// Process each search result and calculate ranking scores
	for _, sr := range input.SearchResults {
		// Skip if already processed (deduplication)
		if processedIDs[sr.ID] {
			continue
		}

		detail, exists := detailsMap[sr.ID]
		if !exists {
			// Skip franchises without matching detail data
			continue
		}

		// Mark as processed
		processedIDs[sr.ID] = true

		// Calculate component scores as per REQ-BIZ-006
		// ES Score: Elasticsearch relevance score (normalized 0-100)
		esScore := math.Min(math.Max(sr.Score*10.0, 0.0), 100.0)

		// Match Score: User-franchise compatibility score (0-100)
		matchScore := h.calculateMatchScore(&detail, &input.UserProfile)

		// Popularity: Based on application count and views (normalized 0-100)
		// Clamp negative values to 0 (REQ-BIZ-008)
		totalPopularity := math.Max(float64(detail.ViewCount+detail.ApplicationCount), 0.0)
		popularityScore := math.Min(totalPopularity/10.0, 100.0)

		// Freshness: Based on last update timestamp (normalized 0-100)
		freshnessScore := h.calculateFreshnessScore(detail.UpdatedAt)

		// Final Score = (ES_Score * 0.4) + (Match_Score * 0.3) + (Popularity * 0.2) + (Freshness * 0.1)
		// As per REQ-BIZ-006
		finalScore := (esScore*0.4 +
			matchScore*0.3 +
			popularityScore*0.2 +
			freshnessScore*0.1)

		ranked = append(ranked, RankedFranchise{
			ID:              detail.ID,
			Name:            detail.Name,
			FinalScore:      finalScore,
			ESScore:         esScore,
			MatchScore:      matchScore,
			PopularityScore: popularityScore,
			FreshnessScore:  freshnessScore,
		})
	}

	// Sort by final score in descending order as per REQ-BIZ-005
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].FinalScore > ranked[j].FinalScore
	})

	// Return top N results based on pagination/MaxItems as per REQ-BIZ-005
	if len(ranked) > h.config.MaxItems {
		ranked = ranked[:h.config.MaxItems]
	}

	duration := time.Since(start).Milliseconds()
	h.logger.Info("ranking completed", map[string]interface{}{
		"inputCount":  len(input.SearchResults),
		"outputCount": len(ranked),
		"durationMs":  duration,
	})

	// Log warning if ranking exceeds 500ms as per REQ-BIZ-007
	if duration > 500 {
		h.logger.Warn("ranking exceeded 500ms", map[string]interface{}{
			"durationMs": duration,
		})
	}

	return &Output{RankedFranchises: ranked}, nil
}

// calculateMatchScore implements the matching algorithm as per REQ-BIZ-010
// Financial fit (30%): Investment range vs user capital
// Experience fit (25%): Required vs actual industry experience
// Location fit (20%): Franchise locations vs user preferences
// Interest fit (25%): Category alignment with user interests
func (h *Handler) calculateMatchScore(detail *FranchiseDetail, profile *UserProfile) float64 {
	// Handle anonymous users or users with no profile data as per REQ-BIZ-011
	// ONLY return 50.0 if ALL profile fields are empty/zero
	if profile.CapitalAvailable == 0 && len(profile.LocationPrefs) == 0 &&
		len(profile.Interests) == 0 && profile.ExperienceYears == 0 {
		return 50.0
	}

	score := 0.0

	// Financial fit (30%): Compare investment range with user capital
	financial := 0.0
	if profile.CapitalAvailable > 0 {
		if profile.CapitalAvailable >= detail.InvestmentMin && profile.CapitalAvailable <= detail.InvestmentMax {
			financial = 100.0 // Perfect fit within range
		} else if profile.CapitalAvailable > detail.InvestmentMax {
			financial = 80.0 // Can afford more
		} else if float64(profile.CapitalAvailable) > float64(detail.InvestmentMin)*0.8 {
			financial = 60.0 // Close to minimum
		}
	}
	score += financial * 0.3

	// Location fit (20%): Match franchise locations with user preferences
	locationFit := 0.0
	if len(profile.LocationPrefs) > 0 && len(detail.Locations) > 0 {
		for _, up := range profile.LocationPrefs {
			for _, loc := range detail.Locations {
				if up == loc {
					locationFit = 100.0
					break
				}
			}
			if locationFit == 100.0 {
				break
			}
		}
	}
	score += locationFit * 0.2

	// Interest fit (25%): Align franchise category with user interests
	interestFit := 0.0
	if len(profile.Interests) > 0 {
		for _, interest := range profile.Interests {
			if interest == detail.Category {
				interestFit = 100.0
				break
			}
		}
	}
	score += interestFit * 0.25

	// Experience fit (25%): Compare required vs actual experience
	experienceFit := 0.0
	if profile.ExperienceYears >= 2 {
		experienceFit = 100.0
	} else if profile.ExperienceYears > 0 {
		experienceFit = 70.0
	}
	score += experienceFit * 0.25

	return math.Min(score, 100.0)
}

// calculateFreshnessScore calculates freshness based on last update timestamp
// As per REQ-BIZ-006: Freshness based on last update timestamp (normalized 0-100)
func (h *Handler) calculateFreshnessScore(updatedAt string) float64 {
	if updatedAt == "" {
		return 50.0 // Default score for missing data
	}

	t, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return 50.0 // Default score for invalid format
	}

	// Round to nearest day to handle floating point precision issues
	daysOld := math.Round(time.Since(t).Hours() / 24.0)

	switch {
	case daysOld <= 30:
		return 100.0 // Very recent (0-30 days inclusive)
	case daysOld <= 90:
		return 80.0 // Recent (31-90 days)
	case daysOld <= 180:
		return 60.0 // Moderate (91-180 days)
	case daysOld <= 365:
		return 40.0 // Old (181-365 days)
	default:
		return 20.0 // Very old (366+ days)
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

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

// // internal/workers/franchise/apply-relevance-ranking/handler.go
// package applyrelevanceranking

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"math"
// 	"sort"
// 	"time"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"go.uber.org/zap"
// )

// const (
// 	TaskType = "apply-relevance-ranking"
// )

// var (
// 	ErrNilInput = errors.New("input cannot be nil")
// )

// type Handler struct {
// 	config *Config
// 	logger *zap.Logger
// }

// func NewHandler(config *Config, logger *zap.Logger) *Handler {
// 	return &Handler{
// 		config: config,
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
// 		h.failJob(client, job, "RANKING_FAILED", err.Error(), 0)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(_ context.Context, input *Input) (*Output, error) {
// 	// Validate input as per REQ-BIZ-007
// 	if input == nil {
// 		return nil, ErrNilInput
// 	}

// 	start := time.Now()

// 	// Build map of details for O(1) lookup
// 	detailsMap := make(map[string]FranchiseDetail)
// 	for _, d := range input.DetailsData {
// 		detailsMap[d.ID] = d
// 	}

// 	// Track processed IDs to avoid duplicates (REQ-BIZ-008)
// 	processedIDs := make(map[string]bool)
// 	var ranked []RankedFranchise

// 	// Process each search result and calculate ranking scores
// 	for _, sr := range input.SearchResults {
// 		// Skip if already processed (deduplication)
// 		if processedIDs[sr.ID] {
// 			continue
// 		}

// 		detail, exists := detailsMap[sr.ID]
// 		if !exists {
// 			// Skip franchises without matching detail data
// 			continue
// 		}

// 		// Mark as processed
// 		processedIDs[sr.ID] = true

// 		// Calculate component scores as per REQ-BIZ-006
// 		// ES Score: Elasticsearch relevance score (normalized 0-100)
// 		esScore := math.Min(math.Max(sr.Score*10.0, 0.0), 100.0)

// 		// Match Score: User-franchise compatibility score (0-100)
// 		matchScore := h.calculateMatchScore(&detail, &input.UserProfile)

// 		// Popularity: Based on application count and views (normalized 0-100)
// 		// Clamp negative values to 0 (REQ-BIZ-008)
// 		totalPopularity := math.Max(float64(detail.ViewCount+detail.ApplicationCount), 0.0)
// 		popularityScore := math.Min(totalPopularity/10.0, 100.0)

// 		// Freshness: Based on last update timestamp (normalized 0-100)
// 		freshnessScore := h.calculateFreshnessScore(detail.UpdatedAt)

// 		// Final Score = (ES_Score * 0.4) + (Match_Score * 0.3) + (Popularity * 0.2) + (Freshness * 0.1)
// 		// As per REQ-BIZ-006
// 		finalScore := (esScore*0.4 +
// 			matchScore*0.3 +
// 			popularityScore*0.2 +
// 			freshnessScore*0.1)

// 		ranked = append(ranked, RankedFranchise{
// 			ID:              detail.ID,
// 			Name:            detail.Name,
// 			FinalScore:      finalScore,
// 			ESScore:         esScore,
// 			MatchScore:      matchScore,
// 			PopularityScore: popularityScore,
// 			FreshnessScore:  freshnessScore,
// 		})
// 	}

// 	// Sort by final score in descending order as per REQ-BIZ-005
// 	sort.Slice(ranked, func(i, j int) bool {
// 		return ranked[i].FinalScore > ranked[j].FinalScore
// 	})

// 	// Return top N results based on pagination/MaxItems as per REQ-BIZ-005
// 	if len(ranked) > h.config.MaxItems {
// 		ranked = ranked[:h.config.MaxItems]
// 	}

// 	duration := time.Since(start).Milliseconds()
// 	h.logger.Info("ranking completed",
// 		zap.Int("inputCount", len(input.SearchResults)),
// 		zap.Int("outputCount", len(ranked)),
// 		zap.Int64("durationMs", duration),
// 	)

// 	// Log warning if ranking exceeds 500ms as per REQ-BIZ-007
// 	if duration > 500 {
// 		h.logger.Warn("ranking exceeded 500ms",
// 			zap.Int64("durationMs", duration),
// 		)
// 	}

// 	return &Output{RankedFranchises: ranked}, nil
// }

// // calculateMatchScore implements the matching algorithm as per REQ-BIZ-010
// // Financial fit (30%): Investment range vs user capital
// // Experience fit (25%): Required vs actual industry experience
// // Location fit (20%): Franchise locations vs user preferences
// // Interest fit (25%): Category alignment with user interests
// func (h *Handler) calculateMatchScore(detail *FranchiseDetail, profile *UserProfile) float64 {
// 	// Handle anonymous users or users with no profile data as per REQ-BIZ-011
// 	// ONLY return 50.0 if ALL profile fields are empty/zero
// 	if profile.CapitalAvailable == 0 && len(profile.LocationPrefs) == 0 &&
// 		len(profile.Interests) == 0 && profile.ExperienceYears == 0 {
// 		return 50.0
// 	}

// 	score := 0.0

// 	// Financial fit (30%): Compare investment range with user capital
// 	financial := 0.0
// 	if profile.CapitalAvailable > 0 {
// 		if profile.CapitalAvailable >= detail.InvestmentMin && profile.CapitalAvailable <= detail.InvestmentMax {
// 			financial = 100.0 // Perfect fit within range
// 		} else if profile.CapitalAvailable > detail.InvestmentMax {
// 			financial = 80.0 // Can afford more
// 		} else if float64(profile.CapitalAvailable) > float64(detail.InvestmentMin)*0.8 {
// 			financial = 60.0 // Close to minimum
// 		}
// 	}
// 	score += financial * 0.3

// 	// Location fit (20%): Match franchise locations with user preferences
// 	locationFit := 0.0
// 	if len(profile.LocationPrefs) > 0 && len(detail.Locations) > 0 {
// 		for _, up := range profile.LocationPrefs {
// 			for _, loc := range detail.Locations {
// 				if up == loc {
// 					locationFit = 100.0
// 					break
// 				}
// 			}
// 			if locationFit == 100.0 {
// 				break
// 			}
// 		}
// 	}
// 	score += locationFit * 0.2

// 	// Interest fit (25%): Align franchise category with user interests
// 	interestFit := 0.0
// 	if len(profile.Interests) > 0 {
// 		for _, interest := range profile.Interests {
// 			if interest == detail.Category {
// 				interestFit = 100.0
// 				break
// 			}
// 		}
// 	}
// 	score += interestFit * 0.25

// 	// Experience fit (25%): Compare required vs actual experience
// 	experienceFit := 0.0
// 	if profile.ExperienceYears >= 2 {
// 		experienceFit = 100.0
// 	} else if profile.ExperienceYears > 0 {
// 		experienceFit = 70.0
// 	}
// 	score += experienceFit * 0.25

// 	return math.Min(score, 100.0)
// }

// // calculateFreshnessScore calculates freshness based on last update timestamp
// // As per REQ-BIZ-006: Freshness based on last update timestamp (normalized 0-100)
// func (h *Handler) calculateFreshnessScore(updatedAt string) float64 {
// 	if updatedAt == "" {
// 		return 50.0 // Default score for missing data
// 	}

// 	t, err := time.Parse(time.RFC3339, updatedAt)
// 	if err != nil {
// 		return 50.0 // Default score for invalid format
// 	}

// 	// Round to nearest day to handle floating point precision issues
// 	daysOld := math.Round(time.Since(t).Hours() / 24.0)

// 	switch {
// 	case daysOld <= 30:
// 		return 100.0 // Very recent (0-30 days inclusive)
// 	case daysOld <= 90:
// 		return 80.0 // Recent (31-90 days)
// 	case daysOld <= 180:
// 		return 60.0 // Moderate (91-180 days)
// 	case daysOld <= 365:
// 		return 40.0 // Old (181-365 days)
// 	default:
// 		return 20.0 // Very old (366+ days)
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
// 	return h.execute(ctx, input)
// }
