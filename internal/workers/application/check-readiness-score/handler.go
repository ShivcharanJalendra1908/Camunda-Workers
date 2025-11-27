// internal/workers/application/check-readiness-score/handler.go
package checkreadinessscore

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"camunda-workers/internal/common/logger"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const (
	TaskType = "check-readiness-score"
)

type Handler struct {
	logger logger.Logger
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	return &Handler{
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
		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := h.execute(ctx, &input)
	if err != nil {
		h.failJob(client, job, "READINESS_SCORE_FAILED", err.Error())
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(_ context.Context, input *Input) (*Output, error) {
	data := input.ApplicationData
	if data == nil {
		data = make(map[string]interface{})
	}

	financial := h.calculateFinancialReadiness(data)
	experience := h.calculateExperience(data)
	commitment := h.calculateCommitment(data)
	compatibility := h.calculateCompatibility(data)

	// Calculate weighted average: Financial(30%) + Experience(25%) + Commitment(20%) + Compatibility(25%)
	finalScore := int(
		float64(financial)*0.30 +
			float64(experience)*0.25 +
			float64(commitment)*0.20 +
			float64(compatibility)*0.25)

	level := h.classifyQualificationLevel(finalScore)

	breakdown := ScoreBreakdown{
		Financial:     financial,
		Experience:    experience,
		Commitment:    commitment,
		Compatibility: compatibility,
	}

	h.logger.Info("readiness score calculated", map[string]interface{}{
		"userId":    input.UserID,
		"score":     finalScore,
		"level":     level,
		"breakdown": breakdown,
	})

	return &Output{
		ReadinessScore:     finalScore,
		QualificationLevel: level,
		ScoreBreakdown:     breakdown,
	}, nil
}

func (h *Handler) calculateFinancialReadiness(data map[string]interface{}) int {
	var financialData map[string]interface{}
	if fi, ok := data["financialInfo"]; ok {
		if fiMap, ok := fi.(map[string]interface{}); ok {
			financialData = fiMap
		} else {
			financialData = data
		}
	} else {
		financialData = data
	}

	capital := 0
	if capRaw, ok := financialData["liquidCapital"]; ok {
		if capInt, err := h.parseInt(capRaw); err == nil && capInt >= 0 {
			capital = capInt
		}
	}

	netWorth := 0
	if nwRaw, ok := financialData["netWorth"]; ok {
		if nwInt, err := h.parseInt(nwRaw); err == nil && nwInt >= 0 {
			netWorth = nwInt
		}
	}

	creditScore := 0
	if csRaw, ok := financialData["creditScore"]; ok {
		if csInt, err := h.parseInt(csRaw); err == nil {
			// Clamp credit score to valid range (300-850 per FICO standard)
			creditScore = h.clamp(csInt, 300, 850)
		}
	}

	score := 0

	// Liquid capital scoring (max 40 points) - NO points below 100k
	if capital >= 1000000 {
		score += 40
	} else if capital >= 500000 {
		score += 30
	} else if capital >= 250000 {
		score += 20
	} else if capital >= 100000 {
		score += 10
	}

	// Net worth scoring (max 30 points) - NO points below 500k
	if netWorth >= 2000000 {
		score += 30
	} else if netWorth >= 1000000 {
		score += 20
	} else if netWorth >= 500000 {
		score += 10
	}

	// Credit score scoring (max 30 points)
	if creditScore >= 700 {
		score += 30
	} else if creditScore >= 600 {
		score += 20
	} else if creditScore >= 500 {
		score += 10
	}

	return h.clamp(score, 0, 100)
}

func (h *Handler) calculateExperience(data map[string]interface{}) int {
	var experienceData map[string]interface{}
	if exp, ok := data["experience"]; ok {
		if expMap, ok := exp.(map[string]interface{}); ok {
			experienceData = expMap
		} else {
			experienceData = data
		}
	} else {
		experienceData = data
	}

	years := 0
	if yRaw, ok := experienceData["yearsInIndustry"]; ok {
		if yInt, err := h.parseInt(yRaw); err == nil && yInt >= 0 {
			years = yInt
		}
	}

	mgmt := false
	if mRaw, ok := experienceData["managementExperience"]; ok {
		mgmt, _ = mRaw.(bool)
	}

	bizOwner := false
	if bRaw, ok := experienceData["businessOwnership"]; ok {
		bizOwner, _ = bRaw.(bool)
	}

	score := 0

	// Years of experience (max 40 points)
	if years >= 10 {
		score += 40
	} else if years >= 5 {
		score += 30
	} else if years >= 2 {
		score += 20
	} else if years >= 1 {
		score += 10
	}

	// Management experience (30 points)
	if mgmt {
		score += 30
	}

	// Business ownership (30 points)
	if bizOwner {
		score += 30
	}

	return h.clamp(score, 0, 100)
}

func (h *Handler) calculateCommitment(data map[string]interface{}) int {
	timeAvail := 0
	if tRaw, ok := data["timeAvailability"]; ok {
		if tInt, err := h.parseInt(tRaw); err == nil && tInt >= 0 {
			timeAvail = tInt
		}
	}

	relocation := false
	if rRaw, ok := data["relocationWilling"]; ok {
		relocation, _ = rRaw.(bool)
	}

	score := 0

	// Time availability (max 50 points)
	if timeAvail >= 40 {
		score += 50
	} else if timeAvail >= 20 {
		score += 30
	} else if timeAvail >= 10 {
		score += 10
	}

	// Relocation willingness (50 points)
	if relocation {
		score += 50
	}

	return h.clamp(score, 0, 100)
}

func (h *Handler) calculateCompatibility(data map[string]interface{}) int {
	categoryMatch := false
	if cRaw, ok := data["categoryMatch"]; ok {
		categoryMatch, _ = cRaw.(bool)
	}

	skillMatch := false
	if sRaw, ok := data["skillAlignment"]; ok {
		skillMatch, _ = sRaw.(bool)
	}

	locationMatch := false
	if lRaw, ok := data["locationMatch"]; ok {
		locationMatch, _ = lRaw.(bool)
	}

	score := 0

	// Category match (40 points)
	if categoryMatch {
		score += 40
	}

	// Skill alignment (30 points)
	if skillMatch {
		score += 30
	}

	// Location match (30 points)
	if locationMatch {
		score += 30
	}

	return h.clamp(score, 0, 100)
}

func (h *Handler) classifyQualificationLevel(score int) string {
	switch {
	case score >= 81:
		return "excellent"
	case score >= 61:
		return "high"
	case score >= 41:
		return "medium"
	default:
		return "low"
	}
}

func (h *Handler) parseInt(raw interface{}) (int, error) {
	switch v := raw.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case string:
		cleaned := strings.ReplaceAll(v, ",", "")
		cleaned = strings.TrimSpace(cleaned)
		return strconv.Atoi(cleaned)
	default:
		return 0, fmt.Errorf("not a number: %T", raw)
	}
}

func (h *Handler) clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
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

func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string) {
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

// // internal/workers/application/check-readiness-score/handler.go
// package checkreadinessscore

// import (
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"strconv"
// 	"strings"
// 	"time"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"go.uber.org/zap"
// )

// const (
// 	TaskType = "check-readiness-score"
// )

// type Handler struct {
// 	logger *zap.Logger
// }

// func NewHandler(config *Config, logger *zap.Logger) *Handler {
// 	return &Handler{
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
// 		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err))
// 		return
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		h.failJob(client, job, "READINESS_SCORE_FAILED", err.Error())
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(_ context.Context, input *Input) (*Output, error) {
// 	data := input.ApplicationData
// 	if data == nil {
// 		data = make(map[string]interface{})
// 	}

// 	financial := h.calculateFinancialReadiness(data)
// 	experience := h.calculateExperience(data)
// 	commitment := h.calculateCommitment(data)
// 	compatibility := h.calculateCompatibility(data)

// 	// Calculate weighted average: Financial(30%) + Experience(25%) + Commitment(20%) + Compatibility(25%)
// 	finalScore := int(
// 		float64(financial)*0.30 +
// 			float64(experience)*0.25 +
// 			float64(commitment)*0.20 +
// 			float64(compatibility)*0.25)

// 	level := h.classifyQualificationLevel(finalScore)

// 	breakdown := ScoreBreakdown{
// 		Financial:     financial,
// 		Experience:    experience,
// 		Commitment:    commitment,
// 		Compatibility: compatibility,
// 	}

// 	h.logger.Info("readiness score calculated",
// 		zap.String("userId", input.UserID),
// 		zap.Int("score", finalScore),
// 		zap.String("level", level),
// 		zap.Any("breakdown", breakdown),
// 	)

// 	return &Output{
// 		ReadinessScore:     finalScore,
// 		QualificationLevel: level,
// 		ScoreBreakdown:     breakdown,
// 	}, nil
// }

// func (h *Handler) calculateFinancialReadiness(data map[string]interface{}) int {
// 	var financialData map[string]interface{}
// 	if fi, ok := data["financialInfo"]; ok {
// 		if fiMap, ok := fi.(map[string]interface{}); ok {
// 			financialData = fiMap
// 		} else {
// 			financialData = data
// 		}
// 	} else {
// 		financialData = data
// 	}

// 	capital := 0
// 	if capRaw, ok := financialData["liquidCapital"]; ok {
// 		if capInt, err := h.parseInt(capRaw); err == nil && capInt >= 0 {
// 			capital = capInt
// 		}
// 	}

// 	netWorth := 0
// 	if nwRaw, ok := financialData["netWorth"]; ok {
// 		if nwInt, err := h.parseInt(nwRaw); err == nil && nwInt >= 0 {
// 			netWorth = nwInt
// 		}
// 	}

// 	creditScore := 0
// 	if csRaw, ok := financialData["creditScore"]; ok {
// 		if csInt, err := h.parseInt(csRaw); err == nil {
// 			// Clamp credit score to valid range (300-850 per FICO standard)
// 			creditScore = h.clamp(csInt, 300, 850)
// 		}
// 	}

// 	score := 0

// 	// Liquid capital scoring (max 40 points) - NO points below 100k
// 	if capital >= 1000000 {
// 		score += 40
// 	} else if capital >= 500000 {
// 		score += 30
// 	} else if capital >= 250000 {
// 		score += 20
// 	} else if capital >= 100000 {
// 		score += 10
// 	}

// 	// Net worth scoring (max 30 points) - NO points below 500k
// 	if netWorth >= 2000000 {
// 		score += 30
// 	} else if netWorth >= 1000000 {
// 		score += 20
// 	} else if netWorth >= 500000 {
// 		score += 10
// 	}

// 	// Credit score scoring (max 30 points)
// 	if creditScore >= 700 {
// 		score += 30
// 	} else if creditScore >= 600 {
// 		score += 20
// 	} else if creditScore >= 500 {
// 		score += 10
// 	}

// 	return h.clamp(score, 0, 100)
// }

// func (h *Handler) calculateExperience(data map[string]interface{}) int {
// 	var experienceData map[string]interface{}
// 	if exp, ok := data["experience"]; ok {
// 		if expMap, ok := exp.(map[string]interface{}); ok {
// 			experienceData = expMap
// 		} else {
// 			experienceData = data
// 		}
// 	} else {
// 		experienceData = data
// 	}

// 	years := 0
// 	if yRaw, ok := experienceData["yearsInIndustry"]; ok {
// 		if yInt, err := h.parseInt(yRaw); err == nil && yInt >= 0 {
// 			years = yInt
// 		}
// 	}

// 	mgmt := false
// 	if mRaw, ok := experienceData["managementExperience"]; ok {
// 		mgmt, _ = mRaw.(bool)
// 	}

// 	bizOwner := false
// 	if bRaw, ok := experienceData["businessOwnership"]; ok {
// 		bizOwner, _ = bRaw.(bool)
// 	}

// 	score := 0

// 	// Years of experience (max 40 points)
// 	if years >= 10 {
// 		score += 40
// 	} else if years >= 5 {
// 		score += 30
// 	} else if years >= 2 {
// 		score += 20
// 	} else if years >= 1 {
// 		score += 10
// 	}

// 	// Management experience (30 points)
// 	if mgmt {
// 		score += 30
// 	}

// 	// Business ownership (30 points)
// 	if bizOwner {
// 		score += 30
// 	}

// 	return h.clamp(score, 0, 100)
// }

// func (h *Handler) calculateCommitment(data map[string]interface{}) int {
// 	timeAvail := 0
// 	if tRaw, ok := data["timeAvailability"]; ok {
// 		if tInt, err := h.parseInt(tRaw); err == nil && tInt >= 0 {
// 			timeAvail = tInt
// 		}
// 	}

// 	relocation := false
// 	if rRaw, ok := data["relocationWilling"]; ok {
// 		relocation, _ = rRaw.(bool)
// 	}

// 	score := 0

// 	// Time availability (max 50 points)
// 	if timeAvail >= 40 {
// 		score += 50
// 	} else if timeAvail >= 20 {
// 		score += 30
// 	} else if timeAvail >= 10 {
// 		score += 10
// 	}

// 	// Relocation willingness (50 points)
// 	if relocation {
// 		score += 50
// 	}

// 	return h.clamp(score, 0, 100)
// }

// func (h *Handler) calculateCompatibility(data map[string]interface{}) int {
// 	categoryMatch := false
// 	if cRaw, ok := data["categoryMatch"]; ok {
// 		categoryMatch, _ = cRaw.(bool)
// 	}

// 	skillMatch := false
// 	if sRaw, ok := data["skillAlignment"]; ok {
// 		skillMatch, _ = sRaw.(bool)
// 	}

// 	locationMatch := false
// 	if lRaw, ok := data["locationMatch"]; ok {
// 		locationMatch, _ = lRaw.(bool)
// 	}

// 	score := 0

// 	// Category match (40 points)
// 	if categoryMatch {
// 		score += 40
// 	}

// 	// Skill alignment (30 points)
// 	if skillMatch {
// 		score += 30
// 	}

// 	// Location match (30 points)
// 	if locationMatch {
// 		score += 30
// 	}

// 	return h.clamp(score, 0, 100)
// }

// func (h *Handler) classifyQualificationLevel(score int) string {
// 	switch {
// 	case score >= 81:
// 		return "excellent"
// 	case score >= 61:
// 		return "high"
// 	case score >= 41:
// 		return "medium"
// 	default:
// 		return "low"
// 	}
// }

// func (h *Handler) parseInt(raw interface{}) (int, error) {
// 	switch v := raw.(type) {
// 	case float64:
// 		return int(v), nil
// 	case int:
// 		return v, nil
// 	case string:
// 		cleaned := strings.ReplaceAll(v, ",", "")
// 		cleaned = strings.TrimSpace(cleaned)
// 		return strconv.Atoi(cleaned)
// 	default:
// 		return 0, fmt.Errorf("not a number: %T", raw)
// 	}
// }

// func (h *Handler) clamp(value, min, max int) int {
// 	if value < min {
// 		return min
// 	}
// 	if value > max {
// 		return max
// 	}
// 	return value
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

// func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string) {
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
