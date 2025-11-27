package authsigninlinkedin

import (
	"context"
	"encoding/json"
	"strings"
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

// ==========================
// Mock Job Helper
// ==========================

func createMockJob(key int64, variables map[string]interface{}) entities.Job {
	variablesJSON, _ := json.Marshal(variables)

	activatedJob := &pb.ActivatedJob{
		Key:                      key,
		Type:                     "auth.signin.linkedin",
		ProcessInstanceKey:       key * 10,
		BpmnProcessId:            "test-process",
		ProcessDefinitionVersion: 1,
		ProcessDefinitionKey:     1,
		ElementId:                "Activity_LinkedInSignin",
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
		AuthCode:    "valid_linkedin_auth_code_12345",
		RedirectURI: "https://example.com/callback",
		State:       "test-state",
		Metadata:    map[string]interface{}{"source": "web"},
	}
}

func createValidOutput() *Output {
	return &Output{
		Success:      true,
		UserID:       "user-123",
		Email:        "test@example.com",
		FirstName:    "John",
		LastName:     "Doe",
		Token:        "access-token-123",
		IsNewUser:    false,
		CRMContactID: "",
	}
}

func createValidConfig() *Config {
	return &Config{
		Enabled:          true,
		MaxJobsActive:    5,
		Timeout:          10 * time.Second,
		ClientID:         "test-client-id",
		ClientSecret:     "test-client-secret",
		RedirectURL:      "https://example.com/callback",
		CreateCRMContact: true,
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
			name: "missing client ID",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       10 * time.Second,
					ClientSecret:  "test-secret",
					RedirectURL:   "https://test.com/callback",
				},
			},
			wantErr: true,
			errMsg:  "client_id is required",
		},
		{
			name: "missing client secret",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       10 * time.Second,
					ClientID:      "test-client-id",
					RedirectURL:   "https://test.com/callback",
				},
			},
			wantErr: true,
			errMsg:  "client_secret is required",
		},
		{
			name: "missing redirect URL",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       10 * time.Second,
					ClientID:      "test-client-id",
					ClientSecret:  "test-secret",
				},
			},
			wantErr: true,
			errMsg:  "redirect_uri is required",
		},
		{
			name: "invalid timeout",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       -1 * time.Second,
					ClientID:      "test-client-id",
					ClientSecret:  "test-secret",
					RedirectURL:   "https://test.com/callback",
				},
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "invalid max jobs active",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 0,
					Timeout:       10 * time.Second,
					ClientID:      "test-client-id",
					ClientSecret:  "test-secret",
					RedirectURL:   "https://test.com/callback",
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
		config: &Config{
			RedirectURL: "https://default.com/callback",
		},
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
				"authCode":    "test-auth-code-12345",
				"redirectUri": "https://custom.com/callback",
				"state":       "test-state",
				"metadata": map[string]interface{}{
					"source": "mobile-app",
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "test-auth-code-12345", input.AuthCode)
				assert.Equal(t, "https://custom.com/callback", input.RedirectURI)
				assert.Equal(t, "test-state", input.State)
				assert.NotNil(t, input.Metadata)
				assert.Equal(t, "mobile-app", input.Metadata["source"])
			},
		},
		{
			name: "valid input minimal fields",
			variables: map[string]interface{}{
				"authCode": "test-auth-code-12345",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "test-auth-code-12345", input.AuthCode)
				assert.Equal(t, "https://default.com/callback", input.RedirectURI)
				assert.Empty(t, input.State)
				assert.Nil(t, input.Metadata)
			},
		},
		{
			name: "missing auth code",
			variables: map[string]interface{}{
				"redirectUri": "https://custom.com/callback",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "auth code too short",
			variables: map[string]interface{}{
				"authCode": "short",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "auth code exactly minimum length",
			variables: map[string]interface{}{
				"authCode": "1234567890",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "1234567890", input.AuthCode)
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
			name: "missing client ID",
			config: &Config{
				ClientSecret:  "secret",
				RedirectURL:   "https://test.com",
				Timeout:       10 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "client_id is required",
		},
		{
			name: "missing client secret",
			config: &Config{
				ClientID:      "client-id",
				RedirectURL:   "https://test.com",
				Timeout:       10 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "client_secret is required",
		},
		{
			name: "missing redirect URL",
			config: &Config{
				ClientID:      "client-id",
				ClientSecret:  "secret",
				Timeout:       10 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "redirect_uri is required",
		},
		{
			name: "zero timeout",
			config: &Config{
				ClientID:      "client-id",
				ClientSecret:  "secret",
				RedirectURL:   "https://test.com",
				Timeout:       0,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "negative timeout",
			config: &Config{
				ClientID:      "client-id",
				ClientSecret:  "secret",
				RedirectURL:   "https://test.com",
				Timeout:       -5 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "zero max jobs active",
			config: &Config{
				ClientID:      "client-id",
				ClientSecret:  "secret",
				RedirectURL:   "https://test.com",
				Timeout:       10 * time.Second,
				MaxJobsActive: 0,
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
	assert.Equal(t, 10*time.Second, config.Timeout)
	assert.True(t, config.CreateCRMContact)
}

func TestConfig_IsCRMEnabled(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected bool
	}{
		{
			name: "CRM enabled with credentials",
			config: &Config{
				CreateCRMContact: true,
				ClientID:         "test-id",
				ClientSecret:     "test-secret",
			},
			expected: true,
		},
		{
			name: "CRM disabled",
			config: &Config{
				CreateCRMContact: false,
				ClientID:         "test-id",
				ClientSecret:     "test-secret",
			},
			expected: false,
		},
		{
			name: "CRM enabled but missing client ID",
			config: &Config{
				CreateCRMContact: true,
				ClientSecret:     "test-secret",
			},
			expected: false,
		},
		{
			name: "CRM enabled but missing client secret",
			config: &Config{
				CreateCRMContact: true,
				ClientID:         "test-id",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.IsCRMEnabled()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ==========================
// Handler Methods Tests
// ==========================

func TestHandler_GetTaskType(t *testing.T) {
	handler := &Handler{}
	assert.Equal(t, "auth.signin.linkedin", handler.GetTaskType())
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
	config := createValidConfig()
	handler := &Handler{config: config}

	assert.Equal(t, config, handler.GetConfig())
	assert.Equal(t, "test-client-id", handler.GetConfig().ClientID)
	assert.Equal(t, "test-client-secret", handler.GetConfig().ClientSecret)
	assert.Equal(t, "https://example.com/callback", handler.GetConfig().RedirectURL)
}

// ==========================
// Input/Output Model Tests
// ==========================

func TestInput_JSONSerialization(t *testing.T) {
	input := createValidInput()

	// Test JSON marshaling
	data, err := json.Marshal(input)
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	// Test JSON unmarshaling
	var decoded Input
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, input.AuthCode, decoded.AuthCode)
	assert.Equal(t, input.RedirectURI, decoded.RedirectURI)
	assert.Equal(t, input.State, decoded.State)
	assert.Equal(t, input.Metadata, decoded.Metadata)
}

func TestOutput_JSONSerialization(t *testing.T) {
	output := createValidOutput()

	// Test JSON marshaling
	data, err := json.Marshal(output)
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	// Test JSON unmarshaling
	var decoded Output
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, output.Success, decoded.Success)
	assert.Equal(t, output.UserID, decoded.UserID)
	assert.Equal(t, output.Email, decoded.Email)
	assert.Equal(t, output.FirstName, decoded.FirstName)
	assert.Equal(t, output.LastName, decoded.LastName)
	assert.Equal(t, output.Token, decoded.Token)
	assert.Equal(t, output.IsNewUser, decoded.IsNewUser)
	assert.Equal(t, output.CRMContactID, decoded.CRMContactID)
}

func TestOutput_WorkflowVariables(t *testing.T) {
	output := createValidOutput()

	// Simulate how output would be converted to workflow variables
	vars := map[string]interface{}{
		"success":   output.Success,
		"userId":    output.UserID,
		"email":     output.Email,
		"firstName": output.FirstName,
		"lastName":  output.LastName,
		"token":     output.Token,
		"isNewUser": output.IsNewUser,
	}

	if output.CRMContactID != "" {
		vars["crmContactId"] = output.CRMContactID
	}

	assert.True(t, vars["success"].(bool))
	assert.Equal(t, "user-123", vars["userId"])
	assert.Equal(t, "test@example.com", vars["email"])
	assert.Equal(t, "John", vars["firstName"])
	assert.Equal(t, "Doe", vars["lastName"])
	assert.Equal(t, "access-token-123", vars["token"])
	assert.False(t, vars["isNewUser"].(bool))
}

// ==========================
// Service Integration Tests
// ==========================

func TestService_Integration(t *testing.T) {
	t.Run("service executes with valid input", func(t *testing.T) {
		mockService := new(MockService)
		input := createValidInput()
		output := createValidOutput()

		mockService.On("Execute", mock.Anything, mock.MatchedBy(func(i *Input) bool {
			return i.AuthCode == input.AuthCode
		})).Return(output, nil)

		result, err := mockService.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.True(t, result.Success)
		assert.Equal(t, "user-123", result.UserID)
		assert.Equal(t, "test@example.com", result.Email)

		mockService.AssertExpectations(t)
	})

	t.Run("service handles new user with CRM contact", func(t *testing.T) {
		mockService := new(MockService)
		input := createValidInput()
		output := createValidOutput()
		output.IsNewUser = true
		output.CRMContactID = "crm-contact-123"

		mockService.On("Execute", mock.Anything, mock.Anything).Return(output, nil)

		result, err := mockService.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.True(t, result.Success)
		assert.True(t, result.IsNewUser)
		assert.Equal(t, "crm-contact-123", result.CRMContactID)

		mockService.AssertExpectations(t)
	})
}

// ==========================
// Task Type Naming Convention Tests
// ==========================

func TestTaskTypeNamingConvention(t *testing.T) {
	taskType := TaskType

	assert.Equal(t, "auth.signin.linkedin", taskType)

	parts := strings.Split(taskType, ".")
	assert.Len(t, parts, 3, "Task type must have exactly 3 parts")
	assert.Equal(t, "auth", parts[0], "Domain should be 'auth'")
	assert.Equal(t, "signin", parts[1], "Subdomain should be 'signin'")
	assert.Equal(t, "linkedin", parts[2], "Action should be 'linkedin'")

	assert.Equal(t, strings.ToLower(taskType), taskType, "Task type should be lowercase")
}

// ==========================
// Validation Schema Tests
// ==========================

func TestGetInputSchema(t *testing.T) {
	schema := GetInputSchema()

	assert.Equal(t, "object", schema.Type)
	assert.Contains(t, schema.Required, "authCode")
	assert.NotNil(t, schema.Properties)

	authCodeProp, exists := schema.Properties["authCode"]
	assert.True(t, exists)
	assert.Equal(t, "string", authCodeProp.Type)
	assert.NotNil(t, authCodeProp.MinLength)
	assert.Equal(t, 10, *authCodeProp.MinLength)
	assert.NotNil(t, authCodeProp.MaxLength)
	assert.Equal(t, 1000, *authCodeProp.MaxLength)

	redirectProp, exists := schema.Properties["redirectUri"]
	assert.True(t, exists)
	assert.Equal(t, "string", redirectProp.Type)
	assert.NotNil(t, redirectProp.MaxLength)

	stateProp, exists := schema.Properties["state"]
	assert.True(t, exists)
	assert.Equal(t, "string", stateProp.Type)

	metadataProp, exists := schema.Properties["metadata"]
	assert.True(t, exists)
	assert.Equal(t, "object", metadataProp.Type)

	assert.False(t, schema.AdditionalProperties)
}

func TestGetOutputSchema(t *testing.T) {
	schema := GetOutputSchema()

	assert.Equal(t, "object", schema.Type)
	assert.NotNil(t, schema.Properties)

	expectedFields := []string{
		"success", "userId", "email", "firstName",
		"lastName", "token", "isNewUser", "crmContactId",
	}

	for _, field := range expectedFields {
		prop, exists := schema.Properties[field]
		assert.True(t, exists, "Field %s should exist", field)
		assert.NotEmpty(t, prop.Type, "Field %s should have a type", field)
	}

	assert.Equal(t, "boolean", schema.Properties["success"].Type)
	assert.Equal(t, "string", schema.Properties["userId"].Type)
	assert.Equal(t, "string", schema.Properties["email"].Type)
	assert.Equal(t, "boolean", schema.Properties["isNewUser"].Type)
}

// ==========================
// Integration Tests
// ==========================

func TestIntegration_ConfigValidation(t *testing.T) {
	appConfig := &config.Config{
		Auth: config.AuthConfig{
			OAuthProviders: struct {
				Google struct {
					ClientID     string `mapstructure:"client_id"`
					ClientSecret string `mapstructure:"client_secret"`
					RedirectURL  string `mapstructure:"redirect_uri"`
				} `mapstructure:"google"`
				LinkedIn struct {
					ClientID     string `mapstructure:"client_id"`
					ClientSecret string `mapstructure:"client_secret"`
					RedirectURL  string `mapstructure:"redirect_uri"`
				} `mapstructure:"linkedin"`
			}{
				LinkedIn: struct {
					ClientID     string `mapstructure:"client_id"`
					ClientSecret string `mapstructure:"client_secret"`
					RedirectURL  string `mapstructure:"redirect_uri"`
				}{
					ClientID:     "app-config-client-id",
					ClientSecret: "app-config-client-secret",
					RedirectURL:  "https://app.com/callback",
				},
			},
		},
		Workers: map[string]config.WorkerConfig{
			"auth-signin-linkedin": {
				Enabled:       true,
				MaxJobsActive: 10,
				Timeout:       15000,
			},
		},
	}

	cfg := createConfigFromAppConfig(appConfig, nil)

	assert.Equal(t, "app-config-client-id", cfg.ClientID)
	assert.Equal(t, "app-config-client-secret", cfg.ClientSecret)
	assert.Equal(t, "https://app.com/callback", cfg.RedirectURL)
	assert.Equal(t, 10, cfg.MaxJobsActive)
	assert.Equal(t, 15*time.Second, cfg.Timeout)
}
