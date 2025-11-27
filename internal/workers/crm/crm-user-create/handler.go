package crmusercreate

import (
	"context"
	"fmt"
	"time"

	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/metrics"
	"camunda-workers/internal/common/validation"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/pb"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const TaskType = "crm.user.create"

type Handler struct {
	config    *Config
	logger    logger.Logger
	camunda   *camunda.Client
	service   *Service
	jobWorker worker.JobWorker
}

type HandlerOptions struct {
	AppConfig    *config.Config
	Camunda      *camunda.Client
	CustomConfig *Config
	Logger       logger.Logger
}

func NewHandler(opts HandlerOptions) (*Handler, error) {
	workerConfig := createConfigFromAppConfig(opts.AppConfig, opts.CustomConfig)

	if err := workerConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration for crm-user-create: %w", err)
	}

	var loggerInstance logger.Logger
	if opts.Logger != nil {
		loggerInstance = opts.Logger
	} else {
		loggerInstance = logger.NewStructured("info", "json")
	}

	handler := &Handler{
		config:  workerConfig,
		logger:  loggerInstance,
		camunda: opts.Camunda,
	}

	handler.service = NewService(ServiceDependencies{
		Logger: loggerInstance,
	}, handler.config)

	return handler, nil
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	startTime := time.Now()
	metrics.WorkerJobsActive.WithLabelValues(TaskType).Inc()
	defer metrics.WorkerJobsActive.WithLabelValues(TaskType).Dec()

	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
	defer cancel()

	h.logger.Info("Processing CRM user create request", map[string]interface{}{
		"jobKey":             job.GetKey(),
		"processInstanceKey": job.GetProcessInstanceKey(),
		"worker":             TaskType,
	})

	if !h.config.Enabled {
		h.logger.Info("Worker disabled by configuration", map[string]interface{}{
			"worker": TaskType,
		})
		h.completeJob(ctx, client, job, &Output{
			Success: false,
			Message: "CRM user creation disabled",
		})
		return
	}

	input, err := h.parseInput(job)
	if err != nil {
		errorCode := extractErrorCode(err)
		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
		h.failJob(ctx, client, job, err)
		return
	}

	output, err := h.Execute(ctx, input) // Use Execute method directly
	if err != nil {
		errorCode := extractErrorCode(err)
		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
		h.failJob(ctx, client, job, err)
		return
	}

	h.completeJob(ctx, client, job, output)
	metrics.WorkerJobsCompleted.WithLabelValues(TaskType).Inc()
	metrics.WorkerJobDuration.WithLabelValues(TaskType).Observe(time.Since(startTime).Seconds())
}

func (h *Handler) parseInput(job entities.Job) (*Input, error) {
	variables, err := job.GetVariablesAsMap()
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "INPUT_PARSING_FAILED",
			Message:   "Failed to parse job variables",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	schema := GetInputSchema()
	validationResult := validation.ValidateInput(variables, schema)
	if !validationResult.Valid {
		return nil, &errors.StandardError{
			Code:      "VALIDATION_FAILED",
			Message:   "Input validation failed",
			Details:   fmt.Sprintf("Validation errors: %v", validationResult.GetErrorMessages()),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	input := &Input{
		Email:     variables["email"].(string),
		FirstName: variables["firstName"].(string),
		LastName:  variables["lastName"].(string),
	}

	if phone, ok := variables["phone"].(string); ok {
		input.Phone = phone
	}

	if company, ok := variables["company"].(string); ok {
		input.Company = company
	}

	if jobTitle, ok := variables["jobTitle"].(string); ok {
		input.JobTitle = jobTitle
	}

	if leadSource, ok := variables["leadSource"].(string); ok {
		input.LeadSource = leadSource
	}

	if tags, ok := variables["tags"].([]interface{}); ok {
		input.Tags = make([]string, len(tags))
		for i, tag := range tags {
			if tagStr, ok := tag.(string); ok {
				input.Tags[i] = tagStr
			}
		}
	}

	if customFields, ok := variables["customFields"].(map[string]interface{}); ok {
		input.CustomFields = customFields
	}

	if metadata, ok := variables["metadata"].(map[string]interface{}); ok {
		input.Metadata = metadata
	}

	return input, nil
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	variables := map[string]interface{}{
		"crmUserCreated": output.Success,
		"crmMessage":     output.Message,
	}

	if output.ContactID != "" {
		variables["crmContactId"] = output.ContactID
	}

	if output.AccountID != "" {
		variables["crmAccountId"] = output.AccountID
	}

	if output.LeadID != "" {
		variables["crmLeadId"] = output.LeadID
	}

	if output.CRMProvider != "" {
		variables["crmProvider"] = output.CRMProvider
	}

	request, err := client.NewCompleteJobCommand().JobKey(job.GetKey()).VariablesFromMap(variables)
	if err != nil {
		h.logger.Error("Failed to create complete job command", map[string]interface{}{
			"jobKey": job.GetKey(),
			"error":  err.Error(),
			"worker": TaskType,
		})
		return
	}

	_, err = request.Send(ctx)
	if err != nil {
		h.logger.Error("Failed to complete job", map[string]interface{}{
			"jobKey": job.GetKey(),
			"error":  err.Error(),
			"worker": TaskType,
		})
	} else {
		h.logger.Info("Successfully completed CRM user create", map[string]interface{}{
			"jobKey":    job.GetKey(),
			"success":   output.Success,
			"contactId": output.ContactID,
			"worker":    TaskType,
		})
	}
}

func (h *Handler) failJob(ctx context.Context, client worker.JobClient, job entities.Job, err error) {
	stdErr := convertToStandardError(err)
	bpmnErr := errors.ConvertToBPMNError(stdErr)

	h.logger.Error("CRM user create job failed", map[string]interface{}{
		"jobKey":       job.GetKey(),
		"errorCode":    bpmnErr.Code,
		"errorMessage": bpmnErr.Message,
		"retryable":    bpmnErr.Retryable,
		"retries":      bpmnErr.Retries,
		"worker":       TaskType,
	})

	failCmd := client.NewFailJobCommand().
		JobKey(job.GetKey()).
		Retries(int32(bpmnErr.Retries)).
		ErrorMessage(fmt.Sprintf("[%s] %s", bpmnErr.Code, bpmnErr.Message))

	var finalCmd interface {
		Send(context.Context) (*pb.FailJobResponse, error)
	}
	if len(bpmnErr.ErrorVariables) > 0 {
		varCmd, varErr := failCmd.VariablesFromMap(bpmnErr.ToErrorVariables())
		if varErr != nil {
			h.logger.Error("Failed to set error variables, sending without them", map[string]interface{}{
				"jobKey": job.GetKey(),
				"error":  varErr.Error(),
				"worker": TaskType,
			})
			finalCmd = failCmd
		} else {
			finalCmd = varCmd
		}
	} else {
		finalCmd = failCmd
	}

	_, failErr := finalCmd.Send(ctx)
	if failErr != nil {
		h.logger.Error("Failed to send BPMN error to Camunda", map[string]interface{}{
			"jobKey": job.GetKey(),
			"error":  failErr.Error(),
			"worker": TaskType,
		})
	}
}

func (h *Handler) Register() error {
	if !h.config.Enabled {
		h.logger.Info("Worker is disabled, skipping registration", map[string]interface{}{
			"worker": TaskType,
		})
		return nil
	}

	zeebeClient := h.camunda.GetClient()

	jobWorker := zeebeClient.NewJobWorker().
		JobType(TaskType).
		Handler(h.Handle).
		MaxJobsActive(h.config.MaxJobsActive).
		Timeout(h.config.Timeout).
		Name(fmt.Sprintf("%s-worker", TaskType)).
		Open()

	h.jobWorker = jobWorker

	h.logger.Info("CRM user create worker registered with Camunda", map[string]interface{}{
		"taskType":      TaskType,
		"maxJobsActive": h.config.MaxJobsActive,
		"timeout":       h.config.Timeout.String(),
		"enabled":       h.config.Enabled,
	})

	return nil
}

func (h *Handler) Close() {
	if h.jobWorker != nil {
		h.logger.Info("Shutting down worker gracefully", map[string]interface{}{
			"worker": TaskType,
		})
		h.jobWorker.Close()
		h.jobWorker = nil
	}
}

func (h *Handler) HealthCheck(ctx context.Context) error {
	if err := h.camunda.HealthCheck(ctx); err != nil {
		return fmt.Errorf("camunda health check failed: %w", err)
	}

	// Test CRM connection
	if err := h.service.TestConnection(ctx); err != nil {
		return fmt.Errorf("crm health check failed: %w", err)
	}

	h.logger.Info("Health check passed", map[string]interface{}{
		"worker": TaskType,
	})

	return nil
}

func (h *Handler) GetTaskType() string {
	return TaskType
}

func (h *Handler) IsEnabled() bool {
	return h.config.Enabled
}

func (h *Handler) GetConfig() *Config {
	return h.config
}

func extractErrorCode(err error) string {
	if stdErr, ok := err.(*errors.StandardError); ok {
		return string(stdErr.Code)
	}
	return "UNKNOWN_ERROR"
}

func convertToStandardError(err error) *errors.StandardError {
	if stdErr, ok := err.(*errors.StandardError); ok {
		return stdErr
	}
	return &errors.StandardError{
		Code:      "CRM_USER_CREATE_ERROR",
		Message:   "Failed to create CRM user",
		Details:   err.Error(),
		Retryable: true,
		Timestamp: time.Now(),
	}
}

func createConfigFromAppConfig(appConfig *config.Config, customConfig *Config) *Config {
	if customConfig != nil {
		return customConfig
	}

	cfg := DefaultConfig()

	if appConfig != nil {
		if workerCfg, exists := appConfig.Workers["crm-user-create"]; exists {
			cfg.Enabled = workerCfg.Enabled
			if workerCfg.MaxJobsActive > 0 {
				cfg.MaxJobsActive = workerCfg.MaxJobsActive
			}
			if workerCfg.Timeout > 0 {
				cfg.Timeout = time.Duration(workerCfg.Timeout) * time.Millisecond
			}
		}

		// Load Zoho CRM configuration if available
		if appConfig.Integrations.Zoho.APIKey != "" {
			cfg.ZohoAPIKey = appConfig.Integrations.Zoho.APIKey
			cfg.ZohoOAuthToken = appConfig.Integrations.Zoho.AuthToken
		}
	}

	return cfg
}

// Execute implements the standard worker interface for direct execution
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// Delegate to the service layer for business logic
	return h.service.Execute(ctx, input)
}

// package crmusercreate

// import (
// 	"context"
// 	"fmt"
// 	"time"

// 	"camunda-workers/internal/common/camunda"
// 	"camunda-workers/internal/common/config"
// 	"camunda-workers/internal/common/errors"
// 	"camunda-workers/internal/common/logger"
// 	"camunda-workers/internal/common/metrics"
// 	"camunda-workers/internal/common/validation"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/pb"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// )

// const TaskType = "crm.user.create"

// type Handler struct {
// 	config    *Config
// 	logger    logger.Logger
// 	camunda   *camunda.Client
// 	service   *Service
// 	jobWorker worker.JobWorker
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	panic("unimplemented")
// }

// // func (h *Handler) execute(context context.Context, input *Input) {
// // 	panic("unimplemented")
// // }

// type HandlerOptions struct {
// 	AppConfig    *config.Config
// 	Camunda      *camunda.Client
// 	CustomConfig *Config
// 	Logger       logger.Logger
// }

// func NewHandler(opts HandlerOptions) (*Handler, error) {
// 	workerConfig := createConfigFromAppConfig(opts.AppConfig, opts.CustomConfig)

// 	if err := workerConfig.Validate(); err != nil {
// 		return nil, fmt.Errorf("invalid configuration for crm-user-create: %w", err)
// 	}

// 	var loggerInstance logger.Logger
// 	if opts.Logger != nil {
// 		loggerInstance = opts.Logger
// 	} else {
// 		loggerInstance = logger.NewStructured("info", "json")
// 	}

// 	handler := &Handler{
// 		config:  workerConfig,
// 		logger:  loggerInstance,
// 		camunda: opts.Camunda,
// 	}

// 	handler.service = NewService(ServiceDependencies{
// 		Logger: loggerInstance,
// 	}, handler.config)

// 	return handler, nil
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	startTime := time.Now()
// 	metrics.WorkerJobsActive.WithLabelValues(TaskType).Inc()
// 	defer metrics.WorkerJobsActive.WithLabelValues(TaskType).Dec()

// 	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
// 	defer cancel()

// 	h.logger.Info("Processing CRM user create request", map[string]interface{}{
// 		"jobKey":             job.GetKey(),
// 		"processInstanceKey": job.GetProcessInstanceKey(),
// 		"worker":             TaskType,
// 	})

// 	if !h.config.Enabled {
// 		h.logger.Info("Worker disabled by configuration", map[string]interface{}{
// 			"worker": TaskType,
// 		})
// 		h.completeJob(ctx, client, job, &Output{
// 			Success: false,
// 			Message: "CRM user creation disabled",
// 		})
// 		return
// 	}

// 	input, err := h.parseInput(job)
// 	if err != nil {
// 		errorCode := extractErrorCode(err)
// 		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
// 		h.failJob(ctx, client, job, err)
// 		return
// 	}

// 	output, err := h.service.Execute(ctx, input)
// 	if err != nil {
// 		errorCode := extractErrorCode(err)
// 		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
// 		h.failJob(ctx, client, job, err)
// 		return
// 	}

// 	h.completeJob(ctx, client, job, output)
// 	metrics.WorkerJobsCompleted.WithLabelValues(TaskType).Inc()
// 	metrics.WorkerJobDuration.WithLabelValues(TaskType).Observe(time.Since(startTime).Seconds())
// }

// func (h *Handler) parseInput(job entities.Job) (*Input, error) {
// 	variables, err := job.GetVariablesAsMap()
// 	if err != nil {
// 		return nil, &errors.StandardError{
// 			Code:      "INPUT_PARSING_FAILED",
// 			Message:   "Failed to parse job variables",
// 			Details:   err.Error(),
// 			Retryable: false,
// 			Timestamp: time.Now(),
// 		}
// 	}

// 	schema := GetInputSchema()
// 	validationResult := validation.ValidateInput(variables, schema)
// 	if !validationResult.Valid {
// 		return nil, &errors.StandardError{
// 			Code:      "VALIDATION_FAILED",
// 			Message:   "Input validation failed",
// 			Details:   fmt.Sprintf("Validation errors: %v", validationResult.GetErrorMessages()),
// 			Retryable: false,
// 			Timestamp: time.Now(),
// 		}
// 	}

// 	input := &Input{
// 		Email:     variables["email"].(string),
// 		FirstName: variables["firstName"].(string),
// 		LastName:  variables["lastName"].(string),
// 	}

// 	if phone, ok := variables["phone"].(string); ok {
// 		input.Phone = phone
// 	}

// 	if company, ok := variables["company"].(string); ok {
// 		input.Company = company
// 	}

// 	if jobTitle, ok := variables["jobTitle"].(string); ok {
// 		input.JobTitle = jobTitle
// 	}

// 	if leadSource, ok := variables["leadSource"].(string); ok {
// 		input.LeadSource = leadSource
// 	}

// 	if tags, ok := variables["tags"].([]interface{}); ok {
// 		input.Tags = make([]string, len(tags))
// 		for i, tag := range tags {
// 			if tagStr, ok := tag.(string); ok {
// 				input.Tags[i] = tagStr
// 			}
// 		}
// 	}

// 	if customFields, ok := variables["customFields"].(map[string]interface{}); ok {
// 		input.CustomFields = customFields
// 	}

// 	if metadata, ok := variables["metadata"].(map[string]interface{}); ok {
// 		input.Metadata = metadata
// 	}

// 	return input, nil
// }

// func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
// 	variables := map[string]interface{}{
// 		"crmUserCreated": output.Success,
// 		"crmMessage":     output.Message,
// 	}

// 	if output.ContactID != "" {
// 		variables["crmContactId"] = output.ContactID
// 	}

// 	if output.AccountID != "" {
// 		variables["crmAccountId"] = output.AccountID
// 	}

// 	if output.LeadID != "" {
// 		variables["crmLeadId"] = output.LeadID
// 	}

// 	if output.CRMProvider != "" {
// 		variables["crmProvider"] = output.CRMProvider
// 	}

// 	request, err := client.NewCompleteJobCommand().JobKey(job.GetKey()).VariablesFromMap(variables)
// 	if err != nil {
// 		h.logger.Error("Failed to create complete job command", map[string]interface{}{
// 			"jobKey": job.GetKey(),
// 			"error":  err.Error(),
// 			"worker": TaskType,
// 		})
// 		return
// 	}

// 	_, err = request.Send(ctx)
// 	if err != nil {
// 		h.logger.Error("Failed to complete job", map[string]interface{}{
// 			"jobKey": job.GetKey(),
// 			"error":  err.Error(),
// 			"worker": TaskType,
// 		})
// 	} else {
// 		h.logger.Info("Successfully completed CRM user create", map[string]interface{}{
// 			"jobKey":    job.GetKey(),
// 			"success":   output.Success,
// 			"contactId": output.ContactID,
// 			"worker":    TaskType,
// 		})
// 	}
// }

// func (h *Handler) failJob(ctx context.Context, client worker.JobClient, job entities.Job, err error) {
// 	stdErr := convertToStandardError(err)
// 	bpmnErr := errors.ConvertToBPMNError(stdErr)

// 	h.logger.Error("CRM user create job failed", map[string]interface{}{
// 		"jobKey":       job.GetKey(),
// 		"errorCode":    bpmnErr.Code,
// 		"errorMessage": bpmnErr.Message,
// 		"retryable":    bpmnErr.Retryable,
// 		"retries":      bpmnErr.Retries,
// 		"worker":       TaskType,
// 	})

// 	failCmd := client.NewFailJobCommand().
// 		JobKey(job.GetKey()).
// 		Retries(int32(bpmnErr.Retries)).
// 		ErrorMessage(fmt.Sprintf("[%s] %s", bpmnErr.Code, bpmnErr.Message))

// 	var finalCmd interface {
// 		Send(context.Context) (*pb.FailJobResponse, error)
// 	}
// 	if len(bpmnErr.ErrorVariables) > 0 {
// 		varCmd, varErr := failCmd.VariablesFromMap(bpmnErr.ToErrorVariables())
// 		if varErr != nil {
// 			h.logger.Error("Failed to set error variables, sending without them", map[string]interface{}{
// 				"jobKey": job.GetKey(),
// 				"error":  varErr.Error(),
// 				"worker": TaskType,
// 			})
// 			finalCmd = failCmd
// 		} else {
// 			finalCmd = varCmd
// 		}
// 	} else {
// 		finalCmd = failCmd
// 	}

// 	_, failErr := finalCmd.Send(ctx)
// 	if failErr != nil {
// 		h.logger.Error("Failed to send BPMN error to Camunda", map[string]interface{}{
// 			"jobKey": job.GetKey(),
// 			"error":  failErr.Error(),
// 			"worker": TaskType,
// 		})
// 	}
// }

// func (h *Handler) Register() error {
// 	if !h.config.Enabled {
// 		h.logger.Info("Worker is disabled, skipping registration", map[string]interface{}{
// 			"worker": TaskType,
// 		})
// 		return nil
// 	}

// 	zeebeClient := h.camunda.GetClient()

// 	jobWorker := zeebeClient.NewJobWorker().
// 		JobType(TaskType).
// 		Handler(h.Handle).
// 		MaxJobsActive(h.config.MaxJobsActive).
// 		Timeout(h.config.Timeout).
// 		Name(fmt.Sprintf("%s-worker", TaskType)).
// 		Open()

// 	h.jobWorker = jobWorker

// 	h.logger.Info("CRM user create worker registered with Camunda", map[string]interface{}{
// 		"taskType":      TaskType,
// 		"maxJobsActive": h.config.MaxJobsActive,
// 		"timeout":       h.config.Timeout.String(),
// 		"enabled":       h.config.Enabled,
// 	})

// 	return nil
// }

// func (h *Handler) Close() {
// 	if h.jobWorker != nil {
// 		h.logger.Info("Shutting down worker gracefully", map[string]interface{}{
// 			"worker": TaskType,
// 		})
// 		h.jobWorker.Close()
// 		h.jobWorker = nil
// 	}
// }

// func (h *Handler) HealthCheck(ctx context.Context) error {
// 	if err := h.camunda.HealthCheck(ctx); err != nil {
// 		return fmt.Errorf("camunda health check failed: %w", err)
// 	}

// 	// Test CRM connection
// 	if err := h.service.TestConnection(ctx); err != nil {
// 		return fmt.Errorf("crm health check failed: %w", err)
// 	}

// 	h.logger.Info("Health check passed", map[string]interface{}{
// 		"worker": TaskType,
// 	})

// 	return nil
// }

// func (h *Handler) GetTaskType() string {
// 	return TaskType
// }

// func (h *Handler) IsEnabled() bool {
// 	return h.config.Enabled
// }

// func (h *Handler) GetConfig() *Config {
// 	return h.config
// }

// func extractErrorCode(err error) string {
// 	if stdErr, ok := err.(*errors.StandardError); ok {
// 		return string(stdErr.Code)
// 	}
// 	return "UNKNOWN_ERROR"
// }

// func convertToStandardError(err error) *errors.StandardError {
// 	if stdErr, ok := err.(*errors.StandardError); ok {
// 		return stdErr
// 	}
// 	return &errors.StandardError{
// 		Code:      "CRM_USER_CREATE_ERROR",
// 		Message:   "Failed to create CRM user",
// 		Details:   err.Error(),
// 		Retryable: true,
// 		Timestamp: time.Now(),
// 	}
// }

// func createConfigFromAppConfig(appConfig *config.Config, customConfig *Config) *Config {
// 	if customConfig != nil {
// 		return customConfig
// 	}

// 	cfg := DefaultConfig()

// 	if appConfig != nil {
// 		if workerCfg, exists := appConfig.Workers["crm-user-create"]; exists {
// 			cfg.Enabled = workerCfg.Enabled
// 			if workerCfg.MaxJobsActive > 0 {
// 				cfg.MaxJobsActive = workerCfg.MaxJobsActive
// 			}
// 			if workerCfg.Timeout > 0 {
// 				cfg.Timeout = time.Duration(workerCfg.Timeout) * time.Millisecond
// 			}
// 		}

// 		// Load Zoho CRM configuration if available
// 		if appConfig.Integrations.Zoho.APIKey != "" {
// 			cfg.ZohoAPIKey = appConfig.Integrations.Zoho.APIKey
// 			cfg.ZohoOAuthToken = appConfig.Integrations.Zoho.AuthToken
// 		}
// 	}

// 	return cfg
// }
