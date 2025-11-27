package authsigninlinkedin

import (
	"context"
	"fmt"
	"time"

	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/metrics"
	"camunda-workers/internal/common/validation"
	"camunda-workers/internal/common/zoho"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/pb"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const TaskType = "auth.signin.linkedin"

type Handler struct {
	config    *Config
	logger    logger.Logger
	camunda   *camunda.Client
	keycloak  *auth.KeycloakClient
	zohoCRM   *zoho.CRMClient
	service   *Service
	jobWorker worker.JobWorker
}

type HandlerOptions struct {
	AppConfig    *config.Config
	Camunda      *camunda.Client
	Keycloak     *auth.KeycloakClient
	ZohoCRM      *zoho.CRMClient
	CustomConfig *Config
	Logger       logger.Logger
}

func NewHandler(opts HandlerOptions) (*Handler, error) {
	workerConfig := createConfigFromAppConfig(opts.AppConfig, opts.CustomConfig)

	if err := workerConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration for auth-signin-linkedin: %w", err)
	}

	var loggerInstance logger.Logger
	if opts.Logger != nil {
		loggerInstance = opts.Logger
	} else {
		loggerInstance = logger.NewStructured("info", "json")
	}

	handler := &Handler{
		config:   workerConfig,
		logger:   loggerInstance,
		camunda:  opts.Camunda,
		keycloak: opts.Keycloak,
		zohoCRM:  opts.ZohoCRM,
	}

	handler.service = NewService(ServiceDependencies{
		Keycloak: handler.keycloak,
		ZohoCRM:  handler.zohoCRM,
		Logger:   loggerInstance,
	}, handler.config)

	return handler, nil
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	startTime := time.Now()
	metrics.WorkerJobsActive.WithLabelValues(TaskType).Inc()
	defer metrics.WorkerJobsActive.WithLabelValues(TaskType).Dec()

	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
	defer cancel()

	h.logger.Info("Processing LinkedIn signin request", map[string]interface{}{
		"jobKey":             job.GetKey(),
		"processInstanceKey": job.GetProcessInstanceKey(),
		"worker":             TaskType,
	})

	if !h.config.Enabled {
		h.logger.Info("Worker disabled by configuration", map[string]interface{}{
			"worker": TaskType,
		})
		h.completeJob(ctx, client, job, &Output{Success: false})
		return
	}

	input, err := h.parseInput(job)
	if err != nil {
		errorCode := extractErrorCode(err)
		metrics.WorkerJobsFailed.WithLabelValues(TaskType, errorCode).Inc()
		h.failJob(ctx, client, job, err)
		return
	}

	output, err := h.Execute(ctx, input)
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
		AuthCode: variables["authCode"].(string),
	}

	if redirectURI, ok := variables["redirectUri"].(string); ok && redirectURI != "" {
		input.RedirectURI = redirectURI
	} else {
		input.RedirectURI = h.config.RedirectURL
	}

	if state, ok := variables["state"].(string); ok {
		input.State = state
	}

	if metadata, ok := variables["metadata"].(map[string]interface{}); ok {
		input.Metadata = metadata
	}

	return input, nil
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	variables := map[string]interface{}{
		"success":   output.Success,
		"userId":    output.UserID,
		"email":     output.Email,
		"firstName": output.FirstName,
		"lastName":  output.LastName,
		"token":     output.Token,
		"isNewUser": output.IsNewUser,
	}

	if output.CRMContactID != "" {
		variables["crmContactId"] = output.CRMContactID
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
		h.logger.Info("Successfully completed LinkedIn signin", map[string]interface{}{
			"jobKey":    job.GetKey(),
			"userId":    output.UserID,
			"isNewUser": output.IsNewUser,
			"worker":    TaskType,
		})
	}
}

func (h *Handler) failJob(ctx context.Context, client worker.JobClient, job entities.Job, err error) {
	stdErr := convertToStandardError(err)
	bpmnErr := errors.ConvertToBPMNError(stdErr)

	h.logger.Error("LinkedIn signin job failed", map[string]interface{}{
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

	h.logger.Info("LinkedIn signin worker registered with Camunda", map[string]interface{}{
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

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	testUser, err := h.keycloak.GetUserByEmail(testCtx, "healthcheck@test.com")
	if err != nil {
		if stdErr, ok := err.(*errors.StandardError); ok {
			if stdErr.Code != "USER_NOT_FOUND" {
				return fmt.Errorf("keycloak health check failed: %w", err)
			}
		}
	}
	_ = testUser

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
		Code:      "LINKEDIN_OAUTH_ERROR",
		Message:   "LinkedIn OAuth authentication failed",
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
		linkedInConfig := appConfig.Auth.OAuthProviders.LinkedIn
		if linkedInConfig.ClientID != "" {
			cfg.ClientID = linkedInConfig.ClientID
			cfg.ClientSecret = linkedInConfig.ClientSecret
			cfg.RedirectURL = linkedInConfig.RedirectURL
		}

		if workerCfg, exists := appConfig.Workers["auth-signin-linkedin"]; exists {
			cfg.Enabled = workerCfg.Enabled
			if workerCfg.MaxJobsActive > 0 {
				cfg.MaxJobsActive = workerCfg.MaxJobsActive
			}
			if workerCfg.Timeout > 0 {
				cfg.Timeout = time.Duration(workerCfg.Timeout) * time.Millisecond
			}
		}

		if appConfig.Integrations.Zoho.APIKey == "" {
			cfg.CreateCRMContact = false
		}
	}

	return cfg
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// Delegate to the service layer for business logic
	return h.service.Execute(ctx, input)
}
