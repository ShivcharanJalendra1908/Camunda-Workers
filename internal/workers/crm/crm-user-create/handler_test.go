package crmusercreate

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/pb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ==========================
// Mock Service Implementation
// ==========================

type MockService struct {
	mock.Mock
}

func (m *MockService) Execute(ctx context.Context, input *Input) (*Output, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Output), args.Error(1)
}

func (m *MockService) TestConnection(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// ==========================
// Mock Job Helper
// ==========================

func createMockJob(key int64, variables map[string]interface{}) entities.Job {
	variablesJSON, _ := json.Marshal(variables)

	activatedJob := &pb.ActivatedJob{
		Key:                      key,
		Type:                     "crm.user.create",
		ProcessInstanceKey:       key * 10,
		BpmnProcessId:            "test-process",
		ProcessDefinitionVersion: 1,
		ProcessDefinitionKey:     1,
		ElementId:                "Activity_CRMUserCreate",
		ElementInstanceKey:       1,
		CustomHeaders:            "{}",
		Worker:                   "test-worker",
		Retries:                  3,
		Deadline:                 0,
		Variables:                string(variablesJSON),
	}

	return entities.Job{ActivatedJob: activatedJob}
}

// ==========================
// Test Helpers
// ==========================

func createValidInput() *Input {
	return &Input{
		Email:      "newuser@example.com",
		FirstName:  "Jane",
		LastName:   "Doe",
		Phone:      "+1234567890",
		Company:    "Acme Corp",
		JobTitle:   "Software Engineer",
		LeadSource: "Website",
		Tags:       []string{"prospect", "interested"},
		Metadata:   map[string]interface{}{"source": "web"},
	}
}

func createValidOutput() *Output {
	return &Output{
		Success:     true,
		Message:     "CRM user created successfully",
		ContactID:   "zoho-contact-12345",
		CRMProvider: "zoho",
		CreatedAt:   time.Now(),
	}
}

func createValidConfig() *Config {
	return &Config{
		Enabled:        true,
		MaxJobsActive:  5,
		Timeout:        30 * time.Second,
		ZohoAPIKey:     "test-api-key",
		ZohoOAuthToken: "test-oauth-token",
	}
}

// createHandlerWithMockService creates a Handler wired with a MockService via ServiceInterface.
func createHandlerWithMockService(svc ServiceInterface) *Handler {
	return &Handler{
		config:  createValidConfig(),
		logger:  logger.NewStructured("info", "json"),
		service: svc,
	}
}

// ==========================
// Handler Creation Tests
// ==========================

func TestHandler_NewHandler(t *testing.T) {
	tests := []struct {
		name    string
		opts    HandlerOptions
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid configuration",
			opts: HandlerOptions{
				CustomConfig: createValidConfig(),
				Logger:       logger.NewStructured("info", "json"),
			},
			wantErr: false,
		},
		{
			name: "missing Zoho API key",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:        true,
					MaxJobsActive:  5,
					Timeout:        30 * time.Second,
					ZohoOAuthToken: "test-token",
				},
			},
			wantErr: true,
			errMsg:  "zoho_api_key is required",
		},
		{
			name: "missing Zoho OAuth token",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       30 * time.Second,
					ZohoAPIKey:    "test-key",
				},
			},
			wantErr: true,
			errMsg:  "zoho_oauth_token is required",
		},
		{
			name: "invalid timeout",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:        true,
					MaxJobsActive:  5,
					Timeout:        -1 * time.Second,
					ZohoAPIKey:     "test-key",
					ZohoOAuthToken: "test-token",
				},
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "invalid max jobs active",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:        true,
					MaxJobsActive:  0,
					Timeout:        30 * time.Second,
					ZohoAPIKey:     "test-key",
					ZohoOAuthToken: "test-token",
				},
			},
			wantErr: true,
			errMsg:  "max_jobs_active must be positive",
		},
		{
			name: "default logger created when not provided",
			opts: HandlerOptions{
				CustomConfig: createValidConfig(),
				Logger:       nil,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := NewHandler(tt.opts)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, handler)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, handler)
				assert.NotNil(t, handler.config)
				assert.NotNil(t, handler.logger)
				assert.NotNil(t, handler.service)
			}
		})
	}
}

// ==========================
// Input Parsing Tests
// ==========================

func TestHandler_ParseInput(t *testing.T) {
	handler := &Handler{
		config: createValidConfig(),
		logger: logger.NewStructured("info", "json"),
	}

	tests := []struct {
		name      string
		variables map[string]interface{}
		wantErr   bool
		errCode   string
		validate  func(*testing.T, *Input)
	}{
		{
			name: "valid input with all fields",
			variables: map[string]interface{}{
				"email":      "test@example.com",
				"firstName":  "John",
				"lastName":   "Smith",
				"phone":      "+1234567890",
				"company":    "Acme Inc",
				"jobTitle":   "Developer",
				"leadSource": "Website",
				"tags":       []interface{}{"tag1", "tag2"},
				"customFields": map[string]interface{}{
					"field1": "value1",
				},
				"metadata": map[string]interface{}{
					"source": "campaign",
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "test@example.com", input.Email)
				assert.Equal(t, "John", input.FirstName)
				assert.Equal(t, "Smith", input.LastName)
				assert.Equal(t, "+1234567890", input.Phone)
				assert.Equal(t, "Acme Inc", input.Company)
				assert.Equal(t, "Developer", input.JobTitle)
				assert.Equal(t, "Website", input.LeadSource)
				assert.Len(t, input.Tags, 2)
				assert.Equal(t, "tag1", input.Tags[0])
				assert.NotNil(t, input.CustomFields)
				assert.NotNil(t, input.Metadata)
			},
		},
		{
			name: "valid input minimal fields",
			variables: map[string]interface{}{
				"email":     "minimal@example.com",
				"firstName": "Jane",
				"lastName":  "Doe",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "minimal@example.com", input.Email)
				assert.Equal(t, "Jane", input.FirstName)
				assert.Equal(t, "Doe", input.LastName)
				assert.Empty(t, input.Phone)
				assert.Empty(t, input.Company)
				assert.Empty(t, input.JobTitle)
				assert.Empty(t, input.LeadSource)
				assert.Nil(t, input.Tags)
				assert.Nil(t, input.CustomFields)
				assert.Nil(t, input.Metadata)
			},
		},
		{
			name: "missing email",
			variables: map[string]interface{}{
				"firstName": "John",
				"lastName":  "Doe",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "missing first name",
			variables: map[string]interface{}{
				"email":    "test@example.com",
				"lastName": "Doe",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "missing last name",
			variables: map[string]interface{}{
				"email":     "test@example.com",
				"firstName": "John",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "email too short",
			variables: map[string]interface{}{
				"email":     "a@b",
				"firstName": "John",
				"lastName":  "Doe",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "first name empty string",
			variables: map[string]interface{}{
				"email":     "test@example.com",
				"firstName": "",
				"lastName":  "Doe",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "last name empty string",
			variables: map[string]interface{}{
				"email":     "test@example.com",
				"firstName": "John",
				"lastName":  "",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "valid minimum length email",
			variables: map[string]interface{}{
				"email":     "a@b.c",
				"firstName": "J",
				"lastName":  "D",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "a@b.c", input.Email)
				assert.Equal(t, "J", input.FirstName)
				assert.Equal(t, "D", input.LastName)
			},
		},
		{
			name: "multiple tags",
			variables: map[string]interface{}{
				"email":     "test@example.com",
				"firstName": "John",
				"lastName":  "Doe",
				"tags":      []interface{}{"tag1", "tag2", "tag3"},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Len(t, input.Tags, 3)
				assert.Equal(t, "tag1", input.Tags[0])
				assert.Equal(t, "tag2", input.Tags[1])
				assert.Equal(t, "tag3", input.Tags[2])
			},
		},
		{
			name: "complex custom fields",
			variables: map[string]interface{}{
				"email":     "test@example.com",
				"firstName": "John",
				"lastName":  "Doe",
				"customFields": map[string]interface{}{
					"industry":    "Technology",
					"employees":   100,
					"isQualified": true,
					"budget":      50000.50,
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.NotNil(t, input.CustomFields)
				assert.Equal(t, "Technology", input.CustomFields["industry"])
				assert.Equal(t, float64(100), input.CustomFields["employees"])
				assert.Equal(t, true, input.CustomFields["isQualified"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := createMockJob(12345, tt.variables)

			input, err := handler.parseInput(job)

			if tt.wantErr {
				require.Error(t, err)
				stdErr, ok := err.(*errors.StandardError)
				require.True(t, ok, "error should be StandardError")
				assert.Equal(t, errors.ErrorCode(tt.errCode), stdErr.Code)
			} else {
				require.NoError(t, err)
				require.NotNil(t, input)
				if tt.validate != nil {
					tt.validate(t, input)
				}
			}
		})
	}
}

// ==========================
// Execute Tests (NEW — was missing entirely)
// ==========================

func TestHandler_Execute(t *testing.T) {
	t.Run("new contact created successfully", func(t *testing.T) {
		mockSvc := new(MockService)
		handler := createHandlerWithMockService(mockSvc)

		input := createValidInput()
		expected := createValidOutput()

		mockSvc.On("Execute", mock.Anything, mock.MatchedBy(func(i *Input) bool {
			return i.Email == input.Email
		})).Return(expected, nil)

		output, err := handler.Execute(context.Background(), input)

		require.NoError(t, err)
		require.NotNil(t, output)
		assert.True(t, output.Success)
		assert.Equal(t, "zoho-contact-12345", output.ContactID)
		assert.Equal(t, "zoho", output.CRMProvider)
		mockSvc.AssertExpectations(t)
	})

	t.Run("service returns CRM API error", func(t *testing.T) {
		mockSvc := new(MockService)
		handler := createHandlerWithMockService(mockSvc)

		input := createValidInput()

		mockSvc.On("Execute", mock.Anything, mock.Anything).Return(nil, &errors.StandardError{
			Code:      "CRM_API_ERROR",
			Message:   "Zoho API rate limit exceeded",
			Retryable: true,
			Timestamp: time.Now(),
		})

		output, err := handler.Execute(context.Background(), input)

		require.Error(t, err)
		assert.Nil(t, output)
		stdErr, ok := err.(*errors.StandardError)
		require.True(t, ok)
		assert.Equal(t, errors.ErrorCode("CRM_API_ERROR"), stdErr.Code)
		assert.True(t, stdErr.Retryable)
		mockSvc.AssertExpectations(t)
	})

	t.Run("service returns non-retryable error", func(t *testing.T) {
		mockSvc := new(MockService)
		handler := createHandlerWithMockService(mockSvc)

		input := createValidInput()

		mockSvc.On("Execute", mock.Anything, mock.Anything).Return(nil, &errors.StandardError{
			Code:      "CRM_NOT_CONFIGURED",
			Message:   "Missing CRM configuration",
			Retryable: false,
			Timestamp: time.Now(),
		})

		output, err := handler.Execute(context.Background(), input)

		require.Error(t, err)
		assert.Nil(t, output)
		stdErr, ok := err.(*errors.StandardError)
		require.True(t, ok)
		assert.False(t, stdErr.Retryable)
		mockSvc.AssertExpectations(t)
	})

	t.Run("invalid input fails validation before service call", func(t *testing.T) {
		mockSvc := new(MockService)
		handler := createHandlerWithMockService(mockSvc)

		input := &Input{
			Email:     "", // invalid
			FirstName: "Jane",
			LastName:  "Doe",
		}

		output, err := handler.Execute(context.Background(), input)

		require.Error(t, err)
		assert.Nil(t, output)
		// Service should NOT be called when validation fails
		mockSvc.AssertNotCalled(t, "Execute")
	})

	t.Run("context cancellation propagated to service", func(t *testing.T) {
		mockSvc := new(MockService)
		handler := createHandlerWithMockService(mockSvc)

		input := createValidInput()
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		mockSvc.On("Execute", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("context canceled"))

		output, err := handler.Execute(ctx, input)

		require.Error(t, err)
		assert.Nil(t, output)
		mockSvc.AssertExpectations(t)
	})
}

// ==========================
// Error Handling Tests
// ==========================

func TestHandler_ExtractErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name: "standard error - CRM API error",
			err: &errors.StandardError{
				Code:    "CRM_API_ERROR",
				Message: "Failed to create contact",
			},
			expected: "CRM_API_ERROR",
		},
		{
			name: "standard error - validation failed",
			err: &errors.StandardError{
				Code:    "VALIDATION_FAILED",
				Message: "Invalid input",
			},
			expected: "VALIDATION_FAILED",
		},
		{
			name: "standard error - CRM not configured",
			err: &errors.StandardError{
				Code:    "CRM_NOT_CONFIGURED",
				Message: "Missing configuration",
			},
			expected: "CRM_NOT_CONFIGURED",
		},
		{
			name:     "generic error",
			err:      fmt.Errorf("generic error"),
			expected: "UNKNOWN_ERROR",
		},
		{
			name:     "nil error",
			err:      nil,
			expected: "UNKNOWN_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := extractErrorCode(tt.err)
			assert.Equal(t, tt.expected, code)
		})
	}
}

func TestHandler_ConvertToStandardError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		validate func(*testing.T, *errors.StandardError)
	}{
		{
			name: "already standard error",
			err: &errors.StandardError{
				Code:      "TEST_ERROR",
				Message:   "Test message",
				Details:   "Test details",
				Retryable: false,
				Timestamp: time.Now(),
			},
			validate: func(t *testing.T, stdErr *errors.StandardError) {
				assert.Equal(t, errors.ErrorCode("TEST_ERROR"), stdErr.Code)
				assert.Equal(t, "Test message", stdErr.Message)
				assert.Equal(t, "Test details", stdErr.Details)
				assert.False(t, stdErr.Retryable)
			},
		},
		{
			name: "generic error converted",
			err:  fmt.Errorf("test error"),
			validate: func(t *testing.T, stdErr *errors.StandardError) {
				assert.Equal(t, errors.ErrorCode("CRM_USER_CREATE_ERROR"), stdErr.Code)
				assert.Equal(t, "Failed to create CRM user", stdErr.Message)
				assert.True(t, stdErr.Retryable)
				assert.Contains(t, stdErr.Details, "test error")
				assert.False(t, stdErr.Timestamp.IsZero())
			},
		},
		{
			name: "retryable error preserved",
			err: &errors.StandardError{
				Code:      "NETWORK_ERROR",
				Message:   "Network timeout",
				Retryable: true,
				Timestamp: time.Now(),
			},
			validate: func(t *testing.T, stdErr *errors.StandardError) {
				assert.True(t, stdErr.Retryable)
				assert.Equal(t, "NETWORK_ERROR", string(stdErr.Code))
			},
		},
		{
			name: "non-retryable error preserved",
			err: &errors.StandardError{
				Code:      "VALIDATION_FAILED",
				Message:   "Invalid data",
				Retryable: false,
				Timestamp: time.Now(),
			},
			validate: func(t *testing.T, stdErr *errors.StandardError) {
				assert.False(t, stdErr.Retryable)
				assert.Equal(t, "VALIDATION_FAILED", string(stdErr.Code))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdErr := convertToStandardError(tt.err)
			require.NotNil(t, stdErr)
			tt.validate(t, stdErr)
		})
	}
}

// ==========================
// Config Tests
// ==========================

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid config",
			config:  createValidConfig(),
			wantErr: false,
		},
		{
			name: "missing Zoho API key",
			config: &Config{
				ZohoOAuthToken: "token",
				Timeout:        30 * time.Second,
				MaxJobsActive:  5,
			},
			wantErr: true,
			errMsg:  "zoho_api_key is required",
		},
		{
			name: "missing Zoho OAuth token",
			config: &Config{
				ZohoAPIKey:    "key",
				Timeout:       30 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "zoho_oauth_token is required",
		},
		{
			name: "zero timeout",
			config: &Config{
				ZohoAPIKey:     "key",
				ZohoOAuthToken: "token",
				Timeout:        0,
				MaxJobsActive:  5,
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "negative timeout",
			config: &Config{
				ZohoAPIKey:     "key",
				ZohoOAuthToken: "token",
				Timeout:        -5 * time.Second,
				MaxJobsActive:  5,
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "zero max jobs active",
			config: &Config{
				ZohoAPIKey:     "key",
				ZohoOAuthToken: "token",
				Timeout:        30 * time.Second,
				MaxJobsActive:  0,
			},
			wantErr: true,
			errMsg:  "max_jobs_active must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConfig_DefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.True(t, config.Enabled)
	assert.Equal(t, 5, config.MaxJobsActive)
	assert.Equal(t, 30*time.Second, config.Timeout)
}

func TestCreateConfigFromAppConfig(t *testing.T) {
	tests := []struct {
		name         string
		appConfig    *config.Config
		customConfig *Config
		validate     func(*testing.T, *Config)
	}{
		{
			name:         "nil inputs returns default config",
			appConfig:    nil,
			customConfig: nil,
			validate: func(t *testing.T, cfg *Config) {
				assert.True(t, cfg.Enabled)
				assert.Equal(t, 5, cfg.MaxJobsActive)
				assert.Equal(t, 30*time.Second, cfg.Timeout)
			},
		},
		{
			name:      "custom config with timeout used directly",
			appConfig: nil,
			customConfig: &Config{
				Enabled:       false,
				MaxJobsActive: 2,
				Timeout:       5 * time.Second,
			},
			validate: func(t *testing.T, cfg *Config) {
				assert.False(t, cfg.Enabled)
				assert.Equal(t, 2, cfg.MaxJobsActive)
				assert.Equal(t, 5*time.Second, cfg.Timeout)
			},
		},
		{
			name: "worker settings loaded from app config",
			appConfig: &config.Config{
				Workers: map[string]config.WorkerConfig{
					"crm-user-create": {
						Enabled:       false,
						MaxJobsActive: 12,
						Timeout:       40000,
					},
				},
			},
			customConfig: nil,
			validate: func(t *testing.T, cfg *Config) {
				assert.False(t, cfg.Enabled)
				assert.Equal(t, 12, cfg.MaxJobsActive)
				assert.Equal(t, 40*time.Second, cfg.Timeout)
			},
		},
		{
			name: "Zoho config loaded from app config",
			appConfig: &config.Config{
				Integrations: config.IntegrationConfig{
					Zoho: struct {
						APIKey       string `mapstructure:"api_key"`
						AuthToken    string `mapstructure:"oauth_token"`
						BaseURL      string `mapstructure:"baseUrl"`
						Timeout      int    `mapstructure:"timeout"`
						RetryCount   int    `mapstructure:"retryCount"`
						RetryDelay   int    `mapstructure:"retryDelay"`
						ClientID     string `mapstructure:"clientId"`
						ClientSecret string `mapstructure:"clientSecret"`
						RefreshToken string `mapstructure:"refreshToken"`
						AccountURL   string `mapstructure:"accountUrl"`
						TokenURL     string `mapstructure:"tokenUrl"`
						Scopes       string `mapstructure:"scopes"`
					}{
						APIKey:    "app-api-key",
						AuthToken: "app-auth-token",
						BaseURL:   "https://app.zoho.com",
						Timeout:   30,
					},
				},
			},
			customConfig: nil,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "app-api-key", cfg.ZohoAPIKey)
				assert.Equal(t, "app-auth-token", cfg.ZohoOAuthToken)
			},
		},
		{
			name: "custom config overrides app config Zoho settings",
			appConfig: &config.Config{
				Integrations: config.IntegrationConfig{
					Zoho: struct {
						APIKey       string `mapstructure:"api_key"`
						AuthToken    string `mapstructure:"oauth_token"`
						BaseURL      string `mapstructure:"baseUrl"`
						Timeout      int    `mapstructure:"timeout"`
						RetryCount   int    `mapstructure:"retryCount"`
						RetryDelay   int    `mapstructure:"retryDelay"`
						ClientID     string `mapstructure:"clientId"`
						ClientSecret string `mapstructure:"clientSecret"`
						RefreshToken string `mapstructure:"refreshToken"`
						AccountURL   string `mapstructure:"accountUrl"`
						TokenURL     string `mapstructure:"tokenUrl"`
						Scopes       string `mapstructure:"scopes"`
					}{
						APIKey:    "app-key",
						AuthToken: "app-token",
					},
				},
			},
			customConfig: &Config{
				ZohoAPIKey:     "custom-key",
				ZohoOAuthToken: "custom-token",
			},
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "custom-key", cfg.ZohoAPIKey)
				assert.Equal(t, "custom-token", cfg.ZohoOAuthToken)
			},
		},
		{
			name: "complete config merge",
			appConfig: &config.Config{
				Workers: map[string]config.WorkerConfig{
					"crm-user-create": {
						Enabled:       true,
						MaxJobsActive: 15,
						Timeout:       60000,
					},
				},
				Integrations: config.IntegrationConfig{
					Zoho: struct {
						APIKey       string `mapstructure:"api_key"`
						AuthToken    string `mapstructure:"oauth_token"`
						BaseURL      string `mapstructure:"baseUrl"`
						Timeout      int    `mapstructure:"timeout"`
						RetryCount   int    `mapstructure:"retryCount"`
						RetryDelay   int    `mapstructure:"retryDelay"`
						ClientID     string `mapstructure:"clientId"`
						ClientSecret string `mapstructure:"clientSecret"`
						RefreshToken string `mapstructure:"refreshToken"`
						AccountURL   string `mapstructure:"accountUrl"`
						TokenURL     string `mapstructure:"tokenUrl"`
						Scopes       string `mapstructure:"scopes"`
					}{
						APIKey:    "merge-api-key",
						AuthToken: "merge-auth-token",
					},
				},
			},
			customConfig: &Config{
				Enabled: false,
			},
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 15, cfg.MaxJobsActive)
				assert.Equal(t, 60*time.Second, cfg.Timeout)
				assert.Equal(t, "merge-api-key", cfg.ZohoAPIKey)
				assert.Equal(t, "merge-auth-token", cfg.ZohoOAuthToken)
				// Enabled overridden by custom config
				assert.False(t, cfg.Enabled)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := createConfigFromAppConfig(tt.appConfig, tt.customConfig)
			require.NotNil(t, cfg)
			tt.validate(t, cfg)
		})
	}
}

// ==========================
// Handler Methods Tests
// ==========================

func TestHandler_GetTaskType(t *testing.T) {
	handler := &Handler{}
	assert.Equal(t, "crm.user.create", handler.GetTaskType())
	assert.Equal(t, TaskType, handler.GetTaskType())
}

func TestHandler_IsEnabled(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		enabled bool
	}{
		{
			name:    "enabled",
			config:  &Config{Enabled: true},
			enabled: true,
		},
		{
			name:    "disabled",
			config:  &Config{Enabled: false},
			enabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &Handler{config: tt.config}
			assert.Equal(t, tt.enabled, handler.IsEnabled())
		})
	}
}

func TestHandler_GetConfig(t *testing.T) {
	cfg := createValidConfig()
	handler := &Handler{config: cfg}

	assert.Equal(t, cfg, handler.GetConfig())
	assert.Equal(t, "test-api-key", handler.GetConfig().ZohoAPIKey)
	assert.Equal(t, "test-oauth-token", handler.GetConfig().ZohoOAuthToken)
}

// ==========================
// Schema Tests
// ==========================

func TestGetInputSchema(t *testing.T) {
	schema := GetInputSchema()

	assert.Equal(t, "object", schema.Type)
	assert.Contains(t, schema.Required, "email")
	assert.Contains(t, schema.Required, "firstName")
	assert.Contains(t, schema.Required, "lastName")
	assert.Len(t, schema.Required, 3)

	assert.Contains(t, schema.Properties, "email")
	assert.Contains(t, schema.Properties, "firstName")
	assert.Contains(t, schema.Properties, "lastName")
	assert.Contains(t, schema.Properties, "phone")
	assert.Contains(t, schema.Properties, "company")
	assert.Contains(t, schema.Properties, "jobTitle")
	assert.Contains(t, schema.Properties, "leadSource")
	assert.Contains(t, schema.Properties, "tags")
	assert.Contains(t, schema.Properties, "customFields")
	assert.Contains(t, schema.Properties, "metadata")

	assert.Equal(t, "string", schema.Properties["email"].Type)
	assert.Equal(t, "string", schema.Properties["firstName"].Type)
	assert.Equal(t, "string", schema.Properties["lastName"].Type)
	assert.Equal(t, "array", schema.Properties["tags"].Type)
	assert.Equal(t, "object", schema.Properties["customFields"].Type)

	assert.NotNil(t, schema.Properties["email"].MinLength)
	assert.Equal(t, 5, *schema.Properties["email"].MinLength)
	assert.NotNil(t, schema.Properties["firstName"].MinLength)
	assert.Equal(t, 1, *schema.Properties["firstName"].MinLength)

	assert.False(t, schema.AdditionalProperties)
}

func TestGetOutputSchema(t *testing.T) {
	schema := GetOutputSchema()

	assert.Equal(t, "object", schema.Type)

	assert.Contains(t, schema.Properties, "success")
	assert.Contains(t, schema.Properties, "message")
	assert.Contains(t, schema.Properties, "contactId")
	assert.Contains(t, schema.Properties, "accountId")
	assert.Contains(t, schema.Properties, "leadId")
	assert.Contains(t, schema.Properties, "crmProvider")
	assert.Contains(t, schema.Properties, "createdAt")

	assert.Equal(t, "boolean", schema.Properties["success"].Type)
	assert.Equal(t, "string", schema.Properties["message"].Type)
	assert.Equal(t, "string", schema.Properties["contactId"].Type)
	assert.Equal(t, "string", schema.Properties["crmProvider"].Type)

	assert.False(t, schema.AdditionalProperties)
}

// ==========================
// Input/Output Model Tests
// ==========================

func TestInput_JSONSerialization(t *testing.T) {
	input := createValidInput()

	data, err := json.Marshal(input)
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	var decoded Input
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, input.Email, decoded.Email)
	assert.Equal(t, input.FirstName, decoded.FirstName)
	assert.Equal(t, input.LastName, decoded.LastName)
	assert.Equal(t, input.Phone, decoded.Phone)
	assert.Equal(t, input.Company, decoded.Company)
	assert.Equal(t, input.JobTitle, decoded.JobTitle)
	assert.Equal(t, input.LeadSource, decoded.LeadSource)
	assert.Equal(t, input.Tags, decoded.Tags)
	assert.Equal(t, input.Metadata, decoded.Metadata)
}

func TestOutput_JSONSerialization(t *testing.T) {
	output := createValidOutput()

	data, err := json.Marshal(output)
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	var decoded Output
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, output.Success, decoded.Success)
	assert.Equal(t, output.Message, decoded.Message)
	assert.Equal(t, output.ContactID, decoded.ContactID)
	assert.Equal(t, output.CRMProvider, decoded.CRMProvider)
}

func TestOutput_WorkflowVariables(t *testing.T) {
	output := createValidOutput()

	vars := map[string]interface{}{
		"success":     output.Success,
		"message":     output.Message,
		"contactId":   output.ContactID,
		"crmProvider": output.CRMProvider,
		"createdAt":   output.CreatedAt.Format(time.RFC3339),
	}

	assert.True(t, vars["success"].(bool))
	assert.Equal(t, "CRM user created successfully", vars["message"])
	assert.Equal(t, "zoho-contact-12345", vars["contactId"])
	assert.Equal(t, "zoho", vars["crmProvider"])
	assert.NotEmpty(t, vars["createdAt"])
}

// ==========================
// Service Integration Tests (via MockService through ServiceInterface)
// ==========================

func TestService_Integration(t *testing.T) {
	t.Run("service executes with valid input", func(t *testing.T) {
		mockService := new(MockService)
		handler := createHandlerWithMockService(mockService)

		input := createValidInput()
		output := createValidOutput()

		mockService.On("Execute", mock.Anything, mock.MatchedBy(func(i *Input) bool {
			return i.Email == input.Email
		})).Return(output, nil)

		// Call through handler.Execute so mock is actually used
		result, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.True(t, result.Success)
		assert.Equal(t, "zoho-contact-12345", result.ContactID)
		assert.Equal(t, "zoho", result.CRMProvider)

		mockService.AssertExpectations(t)
	})

	t.Run("service handles CRM API error", func(t *testing.T) {
		mockService := new(MockService)
		handler := createHandlerWithMockService(mockService)

		input := createValidInput()

		mockService.On("Execute", mock.Anything, mock.Anything).Return(nil, &errors.StandardError{
			Code:    "CRM_API_ERROR",
			Message: "Zoho API rate limit exceeded",
			Details: "Too many requests",
		})

		result, err := handler.Execute(context.Background(), input)

		assert.Error(t, err)
		assert.Nil(t, result)
		stdErr, ok := err.(*errors.StandardError)
		assert.True(t, ok)
		assert.Equal(t, "CRM_API_ERROR", string(stdErr.Code))

		mockService.AssertExpectations(t)
	})
}
