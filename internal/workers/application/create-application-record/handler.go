// internal/workers/application/create-application-record/handler.go
package createapplicationrecord

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
	"github.com/google/uuid"
)

const (
	TaskType = "create-application-record"
)

var (
	ErrDatabaseInsertFailed = errors.New("DATABASE_INSERT_FAILED")
	ErrDuplicateApplication = errors.New("DUPLICATE_APPLICATION")
)

type Handler struct {
	db     *sql.DB
	logger logger.Logger
}

func NewHandler(config *Config, db *sql.DB, log logger.Logger) *Handler {
	return &Handler{
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := h.execute(ctx, &input)
	if err != nil {
		errorCode := "UNKNOWN_ERROR"
		retries := int32(0)
		if errors.Is(err, ErrDatabaseInsertFailed) {
			errorCode = "DATABASE_INSERT_FAILED"
			retries = 3
		} else if errors.Is(err, ErrDuplicateApplication) {
			errorCode = "DUPLICATE_APPLICATION"
			retries = 0
		}
		h.failJob(client, job, errorCode, err.Error(), retries)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	// Check for duplicate application
	var exists bool
	err := h.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM applications 
			WHERE seeker_id = $1 AND franchise_id = $2
		)`, input.SeekerID, input.FranchiseID).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("%w: duplicate check failed: %v", ErrDatabaseInsertFailed, err)
	}
	if exists {
		return nil, fmt.Errorf("%w: application already exists for seeker %s and franchise %s",
			ErrDuplicateApplication, input.SeekerID, input.FranchiseID)
	}

	// Generate unique application ID and timestamp
	appID := uuid.New().String()
	createdAt := time.Now().UTC().Format(time.RFC3339)

	// Serialize application_data to JSON for JSONB column
	applicationDataJSON, err := json.Marshal(input.ApplicationData)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to marshal application data: %v", ErrDatabaseInsertFailed, err)
	}

	// Insert application record
	_, err = h.db.ExecContext(ctx, `
		INSERT INTO applications (
			id, seeker_id, franchise_id, application_data, 
			readiness_score, priority, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
		appID,
		input.SeekerID,
		input.FranchiseID,
		applicationDataJSON, // Use JSON bytes
		input.ReadinessScore,
		input.Priority,
		"submitted",
		createdAt,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: insert failed: %v", ErrDatabaseInsertFailed, err)
	}

	// Create audit log entry (non-critical, log error but don't fail)
	auditDetailsJSON, err := json.Marshal(map[string]interface{}{
		"seekerId":       input.SeekerID,
		"franchiseId":    input.FranchiseID,
		"readinessScore": input.ReadinessScore,
		"priority":       input.Priority,
	})
	if err != nil {
		h.logger.Warn("failed to marshal audit log details", map[string]interface{}{
			"error": err,
		})
		auditDetailsJSON = []byte("{}")
	}

	_, err = h.db.ExecContext(ctx, `
		INSERT INTO audit_log (event_type, resource_type, resource_id, details, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		"application_created",
		"application",
		appID,
		auditDetailsJSON, // Use JSON bytes
		createdAt,
	)
	if err != nil {
		h.logger.Warn("audit log insert failed", map[string]interface{}{
			"error":         err,
			"applicationId": appID,
		})
	}

	h.logger.Info("application record created", map[string]interface{}{
		"applicationId":  appID,
		"seekerId":       input.SeekerID,
		"franchiseId":    input.FranchiseID,
		"readinessScore": input.ReadinessScore,
		"priority":       input.Priority,
	})

	return &Output{
		ApplicationID:     appID,
		ApplicationStatus: "submitted",
		CreatedAt:         createdAt,
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
	} else {
		h.logger.Info("job completed successfully", map[string]interface{}{
			"jobKey": job.Key,
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

// // internal/workers/application/create-application-record/handler.go
// package createapplicationrecord

// import (
// 	"context"
// 	"database/sql"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"time"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"github.com/google/uuid"
// 	"go.uber.org/zap"
// )

// const (
// 	TaskType = "create-application-record"
// )

// var (
// 	ErrDatabaseInsertFailed = errors.New("DATABASE_INSERT_FAILED")
// 	ErrDuplicateApplication = errors.New("DUPLICATE_APPLICATION")
// )

// type Handler struct {
// 	db     *sql.DB
// 	logger *zap.Logger
// }

// func NewHandler(config *Config, db *sql.DB, logger *zap.Logger) *Handler {
// 	return &Handler{
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

// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		errorCode := "UNKNOWN_ERROR"
// 		retries := int32(0)
// 		if errors.Is(err, ErrDatabaseInsertFailed) {
// 			errorCode = "DATABASE_INSERT_FAILED"
// 			retries = 3
// 		} else if errors.Is(err, ErrDuplicateApplication) {
// 			errorCode = "DUPLICATE_APPLICATION"
// 			retries = 0
// 		}
// 		h.failJob(client, job, errorCode, err.Error(), retries)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	// Check for duplicate application
// 	var exists bool
// 	err := h.db.QueryRowContext(ctx, `
// 		SELECT EXISTS(
// 			SELECT 1 FROM applications
// 			WHERE seeker_id = $1 AND franchise_id = $2
// 		)`, input.SeekerID, input.FranchiseID).Scan(&exists)
// 	if err != nil {
// 		return nil, fmt.Errorf("%w: duplicate check failed: %v", ErrDatabaseInsertFailed, err)
// 	}
// 	if exists {
// 		return nil, fmt.Errorf("%w: application already exists for seeker %s and franchise %s",
// 			ErrDuplicateApplication, input.SeekerID, input.FranchiseID)
// 	}

// 	// Generate unique application ID and timestamp
// 	appID := uuid.New().String()
// 	createdAt := time.Now().UTC().Format(time.RFC3339)

// 	// Serialize application_data to JSON for JSONB column
// 	applicationDataJSON, err := json.Marshal(input.ApplicationData)
// 	if err != nil {
// 		return nil, fmt.Errorf("%w: failed to marshal application data: %v", ErrDatabaseInsertFailed, err)
// 	}

// 	// Insert application record
// 	_, err = h.db.ExecContext(ctx, `
// 		INSERT INTO applications (
// 			id, seeker_id, franchise_id, application_data,
// 			readiness_score, priority, status, created_at, updated_at
// 		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
// 		appID,
// 		input.SeekerID,
// 		input.FranchiseID,
// 		applicationDataJSON, // Use JSON bytes
// 		input.ReadinessScore,
// 		input.Priority,
// 		"submitted",
// 		createdAt,
// 	)
// 	if err != nil {
// 		return nil, fmt.Errorf("%w: insert failed: %v", ErrDatabaseInsertFailed, err)
// 	}

// 	// Create audit log entry (non-critical, log error but don't fail)
// 	auditDetailsJSON, err := json.Marshal(map[string]interface{}{
// 		"seekerId":       input.SeekerID,
// 		"franchiseId":    input.FranchiseID,
// 		"readinessScore": input.ReadinessScore,
// 		"priority":       input.Priority,
// 	})
// 	if err != nil {
// 		h.logger.Warn("failed to marshal audit log details", zap.Error(err))
// 		auditDetailsJSON = []byte("{}")
// 	}

// 	_, err = h.db.ExecContext(ctx, `
// 		INSERT INTO audit_log (event_type, resource_type, resource_id, details, created_at)
// 		VALUES ($1, $2, $3, $4, $5)`,
// 		"application_created",
// 		"application",
// 		appID,
// 		auditDetailsJSON, // Use JSON bytes
// 		createdAt,
// 	)
// 	if err != nil {
// 		h.logger.Warn("audit log insert failed",
// 			zap.Error(err),
// 			zap.String("applicationId", appID),
// 		)
// 	}

// 	h.logger.Info("application record created",
// 		zap.String("applicationId", appID),
// 		zap.String("seekerId", input.SeekerID),
// 		zap.String("franchiseId", input.FranchiseID),
// 		zap.Int("readinessScore", input.ReadinessScore),
// 		zap.String("priority", input.Priority),
// 	)

// 	return &Output{
// 		ApplicationID:     appID,
// 		ApplicationStatus: "submitted",
// 		CreatedAt:         createdAt,
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
// 	} else {
// 		h.logger.Info("job completed successfully", zap.Int64("jobKey", job.Key))
// 	}
// }

// func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, retries int32) {
// 	h.logger.Error("job failed",
// 		zap.Int64("jobKey", job.Key),
// 		zap.String("errorCode", errorCode),
// 		zap.String("errorMessage", errorMessage),
// 		zap.Int32("retries", retries),
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
