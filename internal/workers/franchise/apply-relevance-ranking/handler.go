package applyrelevanceranking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	appErrs "camunda-workers/internal/common/errors" // ✅ Keep original alias
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "apply-relevance-ranking"
)

var (
	ErrNilInput = errors.New("input cannot be nil")
)

type Handler struct {
	config       *Config
	logger       logger.Logger
	errorHandler *appErrs.ErrorHandler // ✅ Use appErrs
	validator    *validation.Validator // ✅ ADDED
	sanitizer    *validation.Sanitizer // ✅ ADDED
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	return &Handler{
		config:       config,
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: appErrs.NewErrorHandler(log), // ✅ Use appErrs
		validator:    validation.NewValidator(),    // ✅ ADDED
		sanitizer:    validation.NewSanitizer(),    // ✅ ADDED
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	// ✅ EXTRACT TRACE CONTEXT
	ctx := context.Background()
	
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
	
	// ✅ CREATE WORKER SPAN
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
	
	h.logger.Info("processing job", map[string]interface{}{
		"jobKey":      job.Key,
		"workflowKey": job.ProcessInstanceKey,
		"traceId":     traceID,
		"spanId":      span.SpanContext().SpanID().String(),
	})

	// ===== STEP 1: PARSE INPUT =====
	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "apply-relevance-ranking.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewValidationError("input", fmt.Sprintf("parse input: %v", err))) // ✅ Use appErrs
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== STEP 2: VALIDATE INPUT (GAP #1 FIX) =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "apply-relevance-ranking.validateInput")
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	// ===== STEP 3: EXECUTE BUSINESS LOGIC =====
	ctxExec, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()

	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "apply-relevance-ranking.Execute")
	output, err := h.execute(ctxExec, &input)
	spanExec.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctxExec, client, job,
			appErrs.NewValidationError("execution", err.Error())) // ✅ Use appErrs
		return
	}

	// ===== STEP 4: COMPLETE JOB =====
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "apply-relevance-ranking.completeJob")
	h.completeJob(ctx, client, job, output)
	spanComp.End()
}

// ===== CRITICAL VALIDATION FUNCTION (GAP #1 FIX) =====
func (h *Handler) validateInput(input *Input) error {
	if input == nil {
		return appErrs.NewValidationError("input", "input cannot be nil") // ✅ Use appErrs
	}

	// Validate SearchResults array
	if len(input.SearchResults) == 0 {
		return appErrs.NewValidationError("searchResults", "cannot be empty") // ✅ Use appErrs
	}

	if len(input.SearchResults) > 1000 {
		return appErrs.NewArrayTooLargeError("searchResults", 1000, len(input.SearchResults)) // ✅ Use appErrs
	}

	// Validate each search result
	for i, result := range input.SearchResults {
		// Validate ID (UUID format)
		if err := ozzo.Validate(result.ID,
			ozzo.Required.Error("id is required"),
			ozzo.Length(36, 36).Error("id must be exactly 36 characters"),
			validation.IsUUID,
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewInvalidUUIDError(fmt.Sprintf("searchResults[%d].id", i), result.ID) // ✅ Use appErrs
		}

		// Validate Score (0-100 range)
		if result.Score < 0 || result.Score > 100 {
			return appErrs.NewValidationError(fmt.Sprintf("searchResults[%d].score", i),
				fmt.Sprintf("must be between 0 and 100, got %.2f", result.Score)) // ✅ Use appErrs
		}
	}

	// Validate DetailsData array
	if len(input.DetailsData) > 1000 {
		return appErrs.NewArrayTooLargeError("detailsData", 1000, len(input.DetailsData)) // ✅ Use appErrs
	}

	// Validate each franchise detail
	for i, detail := range input.DetailsData {
		// Validate ID
		if err := ozzo.Validate(detail.ID,
			ozzo.Required.Error("id is required"),
			ozzo.Length(36, 36).Error("id must be exactly 36 characters"),
			validation.IsUUID,
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewInvalidUUIDError(fmt.Sprintf("detailsData[%d].id", i), detail.ID) // ✅ Use appErrs
		}

		// Validate Name
		if err := ozzo.Validate(detail.Name,
			ozzo.Required.Error("name is required"),
			ozzo.Length(1, 200).Error("name must be between 1 and 200 characters"),
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewValidationError(fmt.Sprintf("detailsData[%d].name", i), err.Error()) // ✅ Use appErrs
		}

		// Validate Investment Range
		if detail.InvestmentMin < 0 {
			return appErrs.NewValidationError(fmt.Sprintf("detailsData[%d].investmentMin", i),
				"cannot be negative") // ✅ Use appErrs
		}
		if detail.InvestmentMax < 0 {
			return appErrs.NewValidationError(fmt.Sprintf("detailsData[%d].investmentMax", i),
				"cannot be negative") // ✅ Use appErrs
		}
		if detail.InvestmentMax > 0 && detail.InvestmentMin > detail.InvestmentMax {
			return appErrs.NewValidationError(fmt.Sprintf("detailsData[%d].investmentRange", i),
				"min cannot be greater than max") // ✅ Use appErrs
		}

		// Validate Counts (non-negative)
		if detail.ViewCount < 0 {
			return appErrs.NewValidationError(fmt.Sprintf("detailsData[%d].viewCount", i),
				"cannot be negative") // ✅ Use appErrs
		}
		if detail.ApplicationCount < 0 {
			return appErrs.NewValidationError(fmt.Sprintf("detailsData[%d].applicationCount", i),
				"cannot be negative") // ✅ Use appErrs
		}

		// Validate Category
		if detail.Category != "" {
			if err := ozzo.Validate(detail.Category,
				ozzo.Length(1, 100).Error("category must be between 1 and 100 characters"),
				validation.SafeSQLString,
			); err != nil {
				return appErrs.NewValidationError(fmt.Sprintf("detailsData[%d].category", i), err.Error()) // ✅ Use appErrs
			}
		}

		// Validate UpdatedAt format
		if detail.UpdatedAt != "" {
			if _, err := time.Parse(time.RFC3339, detail.UpdatedAt); err != nil {
				return appErrs.NewValidationError(fmt.Sprintf("detailsData[%d].updatedAt", i),
					fmt.Sprintf("must be RFC3339 format: %v", err)) // ✅ Use appErrs
			}
		}

		// Validate Locations array size
		if len(detail.Locations) > 50 {
			return appErrs.NewArrayTooLargeError(fmt.Sprintf("detailsData[%d].locations", i),
				50, len(detail.Locations)) // ✅ Use appErrs
		}

		// Validate each location
		for j, location := range detail.Locations {
			if err := ozzo.Validate(location,
				ozzo.Length(1, 100).Error("location must be between 1 and 100 characters"),
				validation.SafeSQLString,
			); err != nil {
				return appErrs.NewValidationError(
					fmt.Sprintf("detailsData[%d].locations[%d]", i, j), err.Error()) // ✅ Use appErrs
			}
		}
	}

	// Validate UserProfile
	// Validate CapitalAvailable (non-negative)
	if input.UserProfile.CapitalAvailable < 0 {
		return appErrs.NewValidationError("userProfile.capitalAvailable", "cannot be negative") // ✅ Use appErrs
	}

	// Validate ExperienceYears (non-negative)
	if input.UserProfile.ExperienceYears < 0 {
		return appErrs.NewValidationError("userProfile.experienceYears", "cannot be negative") // ✅ Use appErrs
	}

	// Validate LocationPrefs array size
	if len(input.UserProfile.LocationPrefs) > 50 {
		return appErrs.NewArrayTooLargeError("userProfile.locationPrefs",
			50, len(input.UserProfile.LocationPrefs)) // ✅ Use appErrs
	}

	// Validate each location preference
	for i, location := range input.UserProfile.LocationPrefs {
		if err := ozzo.Validate(location,
			ozzo.Length(1, 100).Error("location must be between 1 and 100 characters"),
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewValidationError(
				fmt.Sprintf("userProfile.locationPrefs[%d]", i), err.Error()) // ✅ Use appErrs
		}
	}

	// Validate Interests array size
	if len(input.UserProfile.Interests) > 50 {
		return appErrs.NewArrayTooLargeError("userProfile.interests",
			50, len(input.UserProfile.Interests)) // ✅ Use appErrs
	}

	// Validate each interest
	for i, interest := range input.UserProfile.Interests {
		if err := ozzo.Validate(interest,
			ozzo.Length(1, 100).Error("interest must be between 1 and 100 characters"),
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewValidationError(
				fmt.Sprintf("userProfile.interests[%d]", i), err.Error()) // ✅ Use appErrs
		}
	}

	return nil
}

// ===== ORIGINAL EXECUTE METHOD (with sanitization added) =====
func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	// Validate input as per REQ-BIZ-007
	if input == nil {
		return nil, ErrNilInput
	}

	start := time.Now()

	// 🔒 Sanitize input data
	input = h.sanitizeInput(input)

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
	
	// Get span from context to add attributes
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.Int("input.search_results.count", len(input.SearchResults)),
		attribute.Int("input.details_data.count", len(input.DetailsData)),
		attribute.Int("output.ranked_franchises.count", len(ranked)),
		attribute.Int64("processing.duration_ms", duration),
	)
	
	h.logger.Info("ranking completed", map[string]interface{}{
		"inputCount":  len(input.SearchResults),
		"outputCount": len(ranked),
		"durationMs":  duration,
		"traceId":     span.SpanContext().TraceID().String(),
	})

	// Log warning if ranking exceeds 500ms as per REQ-BIZ-007
	if duration > 500 {
		span.SetAttributes(attribute.Bool("processing.slow", true))
		h.logger.Warn("ranking exceeded 500ms", map[string]interface{}{
			"durationMs": duration,
			"traceId":    span.SpanContext().TraceID().String(),
		})
	}

	return &Output{RankedFranchises: ranked}, nil
}

// ===== HELPER: Input Sanitization =====
func (h *Handler) sanitizeInput(input *Input) *Input {
	sanitized := &Input{
		SearchResults: make([]SearchResult, len(input.SearchResults)),
		DetailsData:   make([]FranchiseDetail, len(input.DetailsData)),
		UserProfile:   input.UserProfile,
	}

	// Sanitize SearchResults
	for i, sr := range input.SearchResults {
		sanitized.SearchResults[i] = SearchResult{
			ID:    sr.ID, // UUID doesn't need sanitization
			Score: sr.Score,
		}
	}

	// Sanitize FranchiseDetails
	for i, detail := range input.DetailsData {
		sanitized.DetailsData[i] = FranchiseDetail{
			ID:               detail.ID, // UUID doesn't need sanitization
			Name:             h.sanitizer.SanitizeString(detail.Name),
			Category:         h.sanitizer.SanitizeString(detail.Category),
			InvestmentMin:    detail.InvestmentMin,
			InvestmentMax:    detail.InvestmentMax,
			ViewCount:        detail.ViewCount,
			ApplicationCount: detail.ApplicationCount,
			UpdatedAt:        detail.UpdatedAt, // RFC3339 timestamp
			Locations:        make([]string, len(detail.Locations)),
		}

		// Sanitize locations
		for j, location := range detail.Locations {
			sanitized.DetailsData[i].Locations[j] = h.sanitizer.SanitizeString(location)
		}
	}

	// Sanitize UserProfile
	sanitized.UserProfile.CapitalAvailable = input.UserProfile.CapitalAvailable
	sanitized.UserProfile.ExperienceYears = input.UserProfile.ExperienceYears
	sanitized.UserProfile.LocationPrefs = make([]string, len(input.UserProfile.LocationPrefs))
	sanitized.UserProfile.Interests = make([]string, len(input.UserProfile.Interests))

	for i, location := range input.UserProfile.LocationPrefs {
		sanitized.UserProfile.LocationPrefs[i] = h.sanitizer.SanitizeString(location)
	}

	for i, interest := range input.UserProfile.Interests {
		sanitized.UserProfile.Interests[i] = h.sanitizer.SanitizeString(interest)
	}

	return sanitized
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

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error":   err,
			"traceId": span.SpanContext().TraceID().String(),
		})
		return
	}
	_, err = cmd.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error":   err,
			"traceId": span.SpanContext().TraceID().String(),
		})
	}
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// Validate before execution
	if err := h.validateInput(input); err != nil {
		return nil, err
	}
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

// 	"camunda-workers/internal/common/logger"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	appErrs "camunda-workers/internal/common/errors"

// 	"go.opentelemetry.io/otel"
// )

// const (
// 	TaskType = "apply-relevance-ranking"
// )

// var (
// 	ErrNilInput = errors.New("input cannot be nil")
// )

// type Handler struct {
// 	config       *Config
// 	logger       logger.Logger
// 	errorHandler *appErrs.ErrorHandler
// }

// func NewHandler(config *Config, log logger.Logger) *Handler {
// 	return &Handler{
// 		config: config,
// 		logger: log.WithFields(map[string]interface{}{"taskType": TaskType}),
// 	}
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	h.logger.Info("processing job", map[string]interface{}{
// 		"jobKey":      job.Key,
// 		"workflowKey": job.ProcessInstanceKey,
// 	})

// 	var input Input
// 	_, spanParse := otel.Tracer("worker-manager").Start(context.Background(), "apply-relevance-ranking.parseInput")
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
// 		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
// 		spanParse.End()
// 		return
// 	}
// 	spanParse.End()

// 	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
// 	defer cancel()

// 	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "apply-relevance-ranking.Execute")
// 	output, err := h.execute(ctxExec, &input)
// 	spanExec.End()
// 	if err != nil {
// 		h.failJob(client, job, "RANKING_FAILED", err.Error(), 0)
// 		return
// 	}

// 	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "apply-relevance-ranking.completeJob")
// 	h.completeJob(client, job, output)
// 	spanComp.End()
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
// 	h.logger.Info("ranking completed", map[string]interface{}{
// 		"inputCount":  len(input.SearchResults),
// 		"outputCount": len(ranked),
// 		"durationMs":  duration,
// 	})

// 	// Log warning if ranking exceeds 500ms as per REQ-BIZ-007
// 	if duration > 500 {
// 		h.logger.Warn("ranking exceeded 500ms", map[string]interface{}{
// 			"durationMs": duration,
// 		})
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
// 		h.logger.Error("failed to create complete job command", map[string]interface{}{
// 			"error": err,
// 		})
// 		return
// 	}
// 	_, err = cmd.Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to send complete job command", map[string]interface{}{
// 			"error": err,
// 		})
// 	}
// }

// func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, _ int32) {
// 	h.logger.Error("job failed", map[string]interface{}{
// 		"jobKey":       job.Key,
// 		"errorCode":    errorCode,
// 		"errorMessage": errorMessage,
// 	})
// 	stdErr := &appErrs.StandardError{
// 		Code:      appErrs.ErrorCode(errorCode),
// 		Message:   errorMessage,
// 		Retryable: false,
// 	}
// 	h.errorHandler.HandleJobError(context.Background(), client, job, stdErr)
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }
