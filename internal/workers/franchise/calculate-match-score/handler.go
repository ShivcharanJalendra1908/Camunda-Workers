package calculatematchscore

import (
	"context"
	"database/sql"
	"encoding/json"
	stdErrors "errors" // ✅ RENAMED: Go's built-in errors
	"fmt"
	"time"

	"camunda-workers/internal/common/errors" // ✅ Your custom errors (as cerrors)
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/redis/go-redis/v9"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "calculate-match-score"
)

var (
	ErrMatchScoreFailed = stdErrors.New("MATCH_SCORE_FAILED") // ✅ Use stdErrors
)

type Handler struct {
	config       *Config
	db           *sql.DB
	redis        *redis.Client
	logger       logger.Logger
	errorHandler *errors.ErrorHandler // ✅ Your custom errors
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
}

func NewHandler(config *Config, db *sql.DB, redis *redis.Client, log logger.Logger) *Handler {
	return &Handler{
		config:       config,
		db:           db,
		redis:        redis,
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: errors.NewErrorHandler(log), // ✅ Your custom errors
		validator:    validation.NewValidator(),
		sanitizer:    validation.NewSanitizer(),
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
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "calculate-match-score.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			errors.NewValidationError("input", fmt.Sprintf("parse input: %v", err))) // ✅ Your custom errors
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== STEP 2: VALIDATE INPUT (GAP #1 FIX) =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "calculate-match-score.validateInput")
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	// ===== STEP 3: EXECUTE BUSINESS LOGIC =====
	ctxExec, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "calculate-match-score.Execute")
	output, err := h.execute(ctxExec, &input)
	spanExec.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, &errors.StandardError{ // ✅ Your custom errors
			Code:      "MATCH_SCORE_FAILED",
			Message:   err.Error(),
			Retryable: true,
		})
		return
	}

	// ===== STEP 4: COMPLETE JOB =====
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "calculate-match-score.completeJob")
	h.completeJob(ctx, client, job, output)
	spanComp.End()
}

// ===== CRITICAL VALIDATION FUNCTION (GAP #1 FIX) =====
func (h *Handler) validateInput(input *Input) error {
	// Validate FranchiseID (UUID)
	if input.FranchiseData.ID != "" {
		if err := ozzo.Validate(input.FranchiseData.ID,
			ozzo.Required.Error("franchiseId is required"),
			ozzo.Length(36, 36).Error("franchiseId must be exactly 36 characters"),
			validation.IsUUID,
			validation.SafeSQLString,
		); err != nil {
			return errors.NewInvalidUUIDError("franchiseData.id", input.FranchiseData.ID) // ✅ Your custom errors
		}
	}

	// Validate UserID (UUID) if provided
	if input.UserID != "" {
		if err := ozzo.Validate(input.UserID,
			ozzo.Required.Error("userId is required"),
			ozzo.Length(36, 36).Error("userId must be exactly 36 characters"),
			validation.IsUUID,
			validation.SafeSQLString,
		); err != nil {
			return errors.NewInvalidUUIDError("userId", input.UserID) // ✅ Your custom errors
		}
	}

	// Validate Investment Range
	if input.FranchiseData.InvestmentMin < 0 {
		return errors.NewValidationError("franchiseData.investmentMin", "cannot be negative") // ✅ Your custom errors
	}
	if input.FranchiseData.InvestmentMax < 0 {
		return errors.NewValidationError("franchiseData.investmentMax", "cannot be negative") // ✅ Your custom errors
	}
	if input.FranchiseData.InvestmentMax > 0 && input.FranchiseData.InvestmentMin > input.FranchiseData.InvestmentMax {
		return errors.NewValidationError("franchiseData.investmentRange", "min cannot be greater than max") // ✅ Your custom errors
	}

	// Validate InvestmentMax limit (prevent integer overflow)
	if input.FranchiseData.InvestmentMax > 1000000000 { // 1 billion
		return errors.NewValidationError("franchiseData.investmentMax", "cannot exceed 1,000,000,000") // ✅ Your custom errors
	}

	// Validate Franchise Category
	if input.FranchiseData.Category != "" {
		if err := ozzo.Validate(input.FranchiseData.Category,
			ozzo.Length(1, 100).Error("category must be between 1 and 100 characters"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("franchiseData.category", err.Error()) // ✅ Your custom errors
		}
	}

	// Validate Franchise Locations
	if input.FranchiseData.Locations != nil {
		if len(input.FranchiseData.Locations) > 50 {
			return errors.NewArrayTooLargeError("franchiseData.locations", 50, len(input.FranchiseData.Locations)) // ✅ Your custom errors
		}

		for i, location := range input.FranchiseData.Locations {
			if err := ozzo.Validate(location,
				ozzo.Length(1, 100).Error("location must be between 1 and 100 characters"),
				validation.SafeSQLString,
			); err != nil {
				return errors.NewValidationError(fmt.Sprintf("franchiseData.locations[%d]", i), err.Error()) // ✅ Your custom errors
			}
		}
	}

	// Validate UserProfile if provided
	if input.UserProfile != nil {
		if err := h.validateUserProfile(input.UserProfile); err != nil {
			return err
		}
	}

	return nil
}

// ===== HELPER: UserProfile Validation =====
func (h *Handler) validateUserProfile(profile *UserProfile) error {
	// Validate CapitalAvailable (cannot be negative)
	if profile.CapitalAvailable < 0 {
		return errors.NewValidationError("userProfile.capitalAvailable", "cannot be negative") // ✅ Your custom errors
	}
	if profile.CapitalAvailable > 1000000000 { // 1 billion
		return errors.NewValidationError("userProfile.capitalAvailable", "cannot exceed 1,000,000,000") // ✅ Your custom errors
	}

	// Validate ExperienceYears (cannot be negative, reasonable limit)
	if profile.ExperienceYears < 0 {
		return errors.NewValidationError("userProfile.experienceYears", "cannot be negative") // ✅ Your custom errors
	}
	if profile.ExperienceYears > 50 { // Max 50 years experience
		return errors.NewValidationError("userProfile.experienceYears", "cannot exceed 50 years") // ✅ Your custom errors
	}

	// Validate LocationPrefs array size
	if profile.LocationPrefs != nil {
		if len(profile.LocationPrefs) > 50 {
			return errors.NewArrayTooLargeError("userProfile.locationPrefs", 50, len(profile.LocationPrefs)) // ✅ Your custom errors
		}

		for i, location := range profile.LocationPrefs {
			if err := ozzo.Validate(location,
				ozzo.Length(1, 100).Error("location preference must be between 1 and 100 characters"),
				validation.SafeSQLString,
			); err != nil {
				return errors.NewValidationError(fmt.Sprintf("userProfile.locationPrefs[%d]", i), err.Error()) // ✅ Your custom errors
			}
		}
	}

	// Validate Interests array size
	if profile.Interests != nil {
		if len(profile.Interests) > 50 {
			return errors.NewArrayTooLargeError("userProfile.interests", 50, len(profile.Interests)) // ✅ Your custom errors
		}

		for i, interest := range profile.Interests {
			if err := ozzo.Validate(interest,
				ozzo.Length(1, 100).Error("interest must be between 1 and 100 characters"),
				validation.SafeSQLString,
			); err != nil {
				return errors.NewValidationError(fmt.Sprintf("userProfile.interests[%d]", i), err.Error()) // ✅ Your custom errors
			}
		}
	}

	return nil
}

// ===== ORIGINAL EXECUTE METHOD (with sanitization added) =====
func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	// 🔒 Sanitize input before processing
	h.sanitizeInput(input)

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
		"traceId":     trace.SpanFromContext(ctx).SpanContext().TraceID().String(), // ✅ ADD traceId
	})

	return &Output{
		MatchScore:   finalScore,
		MatchFactors: factors,
	}, nil
}

// 🔒 SANITIZATION METHOD
func (h *Handler) sanitizeInput(input *Input) {
	// Sanitize string fields
	if input.UserID != "" {
		input.UserID = h.sanitizer.SanitizeString(input.UserID)
	}
	if input.FranchiseData.ID != "" {
		input.FranchiseData.ID = h.sanitizer.SanitizeString(input.FranchiseData.ID)
	}
	if input.FranchiseData.Category != "" {
		input.FranchiseData.Category = h.sanitizer.SanitizeString(input.FranchiseData.Category)
	}

	// Sanitize locations array
	if input.FranchiseData.Locations != nil {
		sanitizedLocations := make([]string, len(input.FranchiseData.Locations))
		for i, location := range input.FranchiseData.Locations {
			sanitizedLocations[i] = h.sanitizer.SanitizeString(location)
		}
		input.FranchiseData.Locations = sanitizedLocations
	}

	// Sanitize user profile if provided
	if input.UserProfile != nil {
		if input.UserProfile.LocationPrefs != nil {
			sanitizedPrefs := make([]string, len(input.UserProfile.LocationPrefs))
			for i, pref := range input.UserProfile.LocationPrefs {
				sanitizedPrefs[i] = h.sanitizer.SanitizeString(pref)
			}
			input.UserProfile.LocationPrefs = sanitizedPrefs
		}

		if input.UserProfile.Interests != nil {
			sanitizedInterests := make([]string, len(input.UserProfile.Interests))
			for i, interest := range input.UserProfile.Interests {
				sanitizedInterests[i] = h.sanitizer.SanitizeString(interest)
			}
			input.UserProfile.Interests = sanitizedInterests
		}
	}
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

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error":   err,
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
		return
	}
	_, err = cmd.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error":   err,
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
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

// // internal/workers/franchise/calculate-match-score/handler.go
// package calculatematchscore

// import (
// 	"context"
// 	"database/sql"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"time"

// 	"camunda-workers/internal/common/logger"
// 	appErrs "camunda-workers/internal/common/errors"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"github.com/redis/go-redis/v9"

// 	"go.opentelemetry.io/otel"
// )

// const (
// 	TaskType = "calculate-match-score"
// )

// var (
// 	ErrMatchScoreFailed = errors.New("MATCH_SCORE_FAILED")
// )

// type Handler struct {
// 	config       *Config
// 	db           *sql.DB
// 	redis        *redis.Client
// 	logger       logger.Logger
// 	errorHandler *appErrs.ErrorHandler
// }

// func NewHandler(config *Config, db *sql.DB, redis *redis.Client, log logger.Logger) *Handler {
// 	return &Handler{
// 		config:       config,
// 		db:           db,
// 		redis:        redis,
// 		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
// 		errorHandler: appErrs.NewErrorHandler(log),
// 	}
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	h.logger.Info("processing job", map[string]interface{}{
// 		"jobKey":      job.Key,
// 		"workflowKey": job.ProcessInstanceKey,
// 	})

// 	var input Input
// 	_, spanParse := otel.Tracer("worker-manager").Start(context.Background(), "calculate-match-score.parseInput")
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
// 		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
// 		spanParse.End()
// 		return
// 	}
// 	spanParse.End()

// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctx, "calculate-match-score.Execute")
// 	output, err := h.execute(ctxExec, &input)
// 	spanExec.End()
// 	if err != nil {
// 		h.failJob(client, job, "MATCH_SCORE_FAILED", err.Error(), 0)
// 		return
// 	}

// 	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "calculate-match-score.completeJob")
// 	h.completeJob(client, job, output)
// 	spanComp.End()
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	var profile *UserProfile
// 	if input.UserProfile != nil {
// 		profile = input.UserProfile
// 	} else if input.UserID != "" {
// 		var err error
// 		profile, err = h.getUserProfile(ctx, input.UserID)
// 		if err != nil {
// 			h.logger.Warn("failed to fetch user profile", map[string]interface{}{
// 				"userId": input.UserID,
// 				"error":  err,
// 			})
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

// 	h.logger.Info("match score calculated", map[string]interface{}{
// 		"userId":      input.UserID,
// 		"franchiseId": input.FranchiseData.ID,
// 		"score":       finalScore,
// 		"factors":     factors,
// 	})

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
