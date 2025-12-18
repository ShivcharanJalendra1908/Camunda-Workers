package authsignupgoogle

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

// ==========================
// Mock Job Helper
// ==========================

func createMockJob(key int64, variables map[string]interface{}) entities.Job {
	variablesJSON, _ := json.Marshal(variables)

	activatedJob := &pb.ActivatedJob{
		Key:                      key,
		Type:                     "auth.signup.google",
		ProcessInstanceKey:       key * 10,
		BpmnProcessId:            "test-process",
		ProcessDefinitionVersion: 1,
		ProcessDefinitionKey:     1,
		ElementId:                "Activity_GoogleSignup",
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
		AuthCode:    "valid_google_auth_code_12345",
		Email:       "newuser@example.com",
		RedirectURI: "https://example.com/callback",
		State:       "test-state",
		FirstName:   "Jane",
		LastName:    "Smith",
		Metadata:    map[string]interface{}{"source": "web"},
	}
}

func createValidOutput() *Output {
	return &Output{
		Success:       true,
		UserID:        "user-456",
		Email:         "newuser@example.com",
		FirstName:     "Jane",
		LastName:      "Smith",
		Token:         "access-token-456",
		AccessToken:   "access-token-456",
		RefreshToken:  "refresh-token-789",
		ExpiresIn:     3600,
		TokenType:     "Bearer",
		EmailVerified: true,
		PasswordSet:   false,
		CRMContactID:  "crm-contact-456",
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
				"email":       "newuser@example.com",
				"redirectUri": "https://custom.com/callback",
				"state":       "test-state",
				"firstName":   "Jane",
				"lastName":    "Smith",
				"metadata": map[string]interface{}{
					"source": "mobile-app",
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "test-auth-code-12345", input.AuthCode)
				assert.Equal(t, "newuser@example.com", input.Email)
				assert.Equal(t, "https://custom.com/callback", input.RedirectURI)
				assert.Equal(t, "test-state", input.State)
				assert.Equal(t, "Jane", input.FirstName)
				assert.Equal(t, "Smith", input.LastName)
				assert.NotNil(t, input.Metadata)
				assert.Equal(t, "mobile-app", input.Metadata["source"])
			},
		},
		{
			name: "valid input minimal fields",
			variables: map[string]interface{}{
				"authCode": "test-auth-code-12345",
				"email":    "newuser@example.com",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "test-auth-code-12345", input.AuthCode)
				assert.Equal(t, "newuser@example.com", input.Email)
				assert.Equal(t, "https://default.com/callback", input.RedirectURI)
				assert.Empty(t, input.State)
				assert.Empty(t, input.FirstName)
				assert.Empty(t, input.LastName)
				assert.Nil(t, input.Metadata)
			},
		},
		{
			name: "missing auth code",
			variables: map[string]interface{}{
				"email":       "newuser@example.com",
				"redirectUri": "https://custom.com/callback",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "missing email",
			variables: map[string]interface{}{
				"authCode": "test-auth-code-12345",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "auth code too short",
			variables: map[string]interface{}{
				"authCode": "short",
				"email":    "newuser@example.com",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "email too short",
			variables: map[string]interface{}{
				"authCode": "test-auth-code-12345",
				"email":    "a@b",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "valid email at minimum length",
			variables: map[string]interface{}{
				"authCode": "1234567890",
				"email":    "a@b.c",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "1234567890", input.AuthCode)
				assert.Equal(t, "a@b.c", input.Email)
			},
		},
		{
			name: "auth code exactly minimum length",
			variables: map[string]interface{}{
				"authCode": "1234567890",
				"email":    "user@example.com",
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
// Error Handling Tests
// ==========================

func TestHandler_ExtractErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name: "standard error - google signup",
			err: &errors.StandardError{
				Code:    "GOOGLE_SIGNUP_ERROR",
				Message: "Signup failed",
			},
			expected: "GOOGLE_SIGNUP_ERROR",
		},
		{
			name: "standard error - user exists",
			err: &errors.StandardError{
				Code:    "USER_ALREADY_EXISTS",
				Message: "User already exists",
			},
			expected: "USER_ALREADY_EXISTS",
		},
		{
			name: "standard error - validation",
			err: &errors.StandardError{
				Code:    "VALIDATION_FAILED",
				Message: "Input validation failed",
			},
			expected: "VALIDATION_FAILED",
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
				assert.Equal(t, errors.ErrorCode("GOOGLE_SIGNUP_ERROR"), stdErr.Code)
				assert.Equal(t, "Google signup failed", stdErr.Message)
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

func TestCreateConfigFromAppConfig(t *testing.T) {
	tests := []struct {
		name         string
		appConfig    *config.Config
		customConfig *Config
		validate     func(*testing.T, *Config)
	}{
		{
			name:         "custom config takes precedence",
			appConfig:    &config.Config{},
			customConfig: createValidConfig(),
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "test-client-id", cfg.ClientID)
				assert.Equal(t, "test-client-secret", cfg.ClientSecret)
				assert.Equal(t, "https://example.com/callback", cfg.RedirectURL)
			},
		},
		{
			name: "loads from app config",
			appConfig: &config.Config{
				Auth: config.AuthConfig{
					OAuthProviders: struct {
						Google struct {
							ClientID     string `mapstructure:"client_id"`
							ClientSecret string `mapstructure:"client_secret"`
							RedirectURL  string `mapstructure:"redirect_uri"`
							Scopes       string `mapstructure:"scopes"`
						} `mapstructure:"google"`
						LinkedIn struct {
							ClientID     string `mapstructure:"client_id"`
							ClientSecret string `mapstructure:"client_secret"`
							RedirectURL  string `mapstructure:"redirect_uri"`
							Scopes       string `mapstructure:"scopes"`
						} `mapstructure:"linkedin"`
						Microsoft struct {
							ClientID     string `mapstructure:"client_id"`
							ClientSecret string `mapstructure:"client_secret"`
							RedirectURL  string `mapstructure:"redirect_uri"`
							Scopes       string `mapstructure:"scopes"`
						} `mapstructure:"microsoft"`
					}{
						Google: struct {
							ClientID     string `mapstructure:"client_id"`
							ClientSecret string `mapstructure:"client_secret"`
							RedirectURL  string `mapstructure:"redirect_uri"`
							Scopes       string `mapstructure:"scopes"`
						}{
							ClientID:     "app-client-id",
							ClientSecret: "app-client-secret",
							RedirectURL:  "https://app.com/callback",
						},
					},
				},
				Workers: map[string]config.WorkerConfig{
					"auth-signup-google": {
						Enabled:       true,
						MaxJobsActive: 10,
						Timeout:       15000,
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
						APIKey: "zoho-key",
					},
				},
			},
			customConfig: nil,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "app-client-id", cfg.ClientID)
				assert.Equal(t, "app-client-secret", cfg.ClientSecret)
				assert.Equal(t, "https://app.com/callback", cfg.RedirectURL)
				assert.Equal(t, 10, cfg.MaxJobsActive)
				assert.Equal(t, 15*time.Second, cfg.Timeout)
				assert.True(t, cfg.Enabled)
				assert.True(t, cfg.CreateCRMContact)
			},
		},
		{
			name: "disable CRM when Zoho not configured",
			appConfig: &config.Config{
				Auth: config.AuthConfig{
					OAuthProviders: struct {
						Google struct {
							ClientID     string `mapstructure:"client_id"`
							ClientSecret string `mapstructure:"client_secret"`
							RedirectURL  string `mapstructure:"redirect_uri"`
							Scopes       string `mapstructure:"scopes"`
						} `mapstructure:"google"`
						LinkedIn struct {
							ClientID     string `mapstructure:"client_id"`
							ClientSecret string `mapstructure:"client_secret"`
							RedirectURL  string `mapstructure:"redirect_uri"`
							Scopes       string `mapstructure:"scopes"`
						} `mapstructure:"linkedin"`
						Microsoft struct {
							ClientID     string `mapstructure:"client_id"`
							ClientSecret string `mapstructure:"client_secret"`
							RedirectURL  string `mapstructure:"redirect_uri"`
							Scopes       string `mapstructure:"scopes"`
						} `mapstructure:"microsoft"`
					}{
						Google: struct {
							ClientID     string `mapstructure:"client_id"`
							ClientSecret string `mapstructure:"client_secret"`
							RedirectURL  string `mapstructure:"redirect_uri"`
							Scopes       string `mapstructure:"scopes"`
						}{
							ClientID:     "app-client-id",
							ClientSecret: "app-client-secret",
							RedirectURL:  "https://app.com/callback",
						},
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
						APIKey: "", // Empty API key should disable CRM
					},
				},
			},
			customConfig: nil,
			validate: func(t *testing.T, cfg *Config) {
				assert.False(t, cfg.CreateCRMContact)
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
	assert.Equal(t, "auth.signup.google", handler.GetTaskType())
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
	assert.Equal(t, input.Email, decoded.Email)
	assert.Equal(t, input.RedirectURI, decoded.RedirectURI)
	assert.Equal(t, input.State, decoded.State)
	assert.Equal(t, input.FirstName, decoded.FirstName)
	assert.Equal(t, input.LastName, decoded.LastName)
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
	assert.Equal(t, output.AccessToken, decoded.AccessToken)
	assert.Equal(t, output.RefreshToken, decoded.RefreshToken)
	assert.Equal(t, output.ExpiresIn, decoded.ExpiresIn)
	assert.Equal(t, output.TokenType, decoded.TokenType)
	assert.Equal(t, output.EmailVerified, decoded.EmailVerified)
	assert.Equal(t, output.PasswordSet, decoded.PasswordSet)
	assert.Equal(t, output.CRMContactID, decoded.CRMContactID)
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
			return i.AuthCode == input.AuthCode && i.Email == input.Email
		})).Return(output, nil)

		result, err := mockService.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.True(t, result.Success)
		assert.Equal(t, "user-456", result.UserID)
		assert.Equal(t, "newuser@example.com", result.Email)
		assert.Equal(t, "Jane", result.FirstName)
		assert.Equal(t, "Smith", result.LastName)
		assert.Equal(t, "access-token-456", result.AccessToken)
		assert.Equal(t, "refresh-token-789", result.RefreshToken)
		assert.Equal(t, 3600, result.ExpiresIn)
		assert.Equal(t, "Bearer", result.TokenType)
		assert.True(t, result.EmailVerified)
		assert.False(t, result.PasswordSet)

		mockService.AssertExpectations(t)
	})

	t.Run("service handles new user without CRM contact", func(t *testing.T) {
		mockService := new(MockService)
		input := createValidInput()
		output := createValidOutput()
		output.CRMContactID = "" // No CRM contact

		mockService.On("Execute", mock.Anything, mock.Anything).Return(output, nil)

		result, err := mockService.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.True(t, result.Success)
		assert.Empty(t, result.CRMContactID)

		mockService.AssertExpectations(t)
	})
}

// ==========================
// Schema Tests
// ==========================

func TestGetInputSchema(t *testing.T) {
	schema := GetInputSchema()

	assert.Equal(t, "object", schema.Type)
	assert.Contains(t, schema.Required, "authCode")
	assert.Contains(t, schema.Required, "email")
	assert.Len(t, schema.Required, 2)

	// Check key properties
	assert.NotNil(t, schema.Properties["authCode"])
	assert.NotNil(t, schema.Properties["email"])
	assert.NotNil(t, schema.Properties["redirectUri"])
	assert.NotNil(t, schema.Properties["state"])
	assert.NotNil(t, schema.Properties["firstName"])
	assert.NotNil(t, schema.Properties["lastName"])
	assert.NotNil(t, schema.Properties["metadata"])

	// Verify specific constraints
	assert.Equal(t, "string", schema.Properties["authCode"].Type)
	assert.Equal(t, 10, *schema.Properties["authCode"].MinLength)
	assert.Equal(t, 1000, *schema.Properties["authCode"].MaxLength)

	assert.Equal(t, "string", schema.Properties["email"].Type)
	assert.Equal(t, 5, *schema.Properties["email"].MinLength)
	assert.Equal(t, 255, *schema.Properties["email"].MaxLength)
}

func TestGetOutputSchema(t *testing.T) {
	schema := GetOutputSchema()

	assert.Equal(t, "object", schema.Type)

	// Verify all expected fields exist
	expectedFields := []string{
		"success", "userId", "email", "firstName", "lastName",
		"token", "accessToken", "refreshToken", "expiresIn",
		"tokenType", "emailVerified", "passwordSet", "crmContactId",
	}

	for _, field := range expectedFields {
		prop, exists := schema.Properties[field]
		assert.True(t, exists, "Field %s should exist", field)
		assert.NotEmpty(t, prop.Type, "Field %s should have a type", field)
	}

	// Verify specific types
	assert.Equal(t, "boolean", schema.Properties["success"].Type)
	assert.Equal(t, "string", schema.Properties["userId"].Type)
	assert.Equal(t, "string", schema.Properties["email"].Type)
	assert.Equal(t, "boolean", schema.Properties["emailVerified"].Type)
	assert.Equal(t, "boolean", schema.Properties["passwordSet"].Type)
}

// ==========================
// Task Type Tests
// ==========================

func TestTaskType(t *testing.T) {
	assert.Equal(t, "auth.signup.google", TaskType)
}

func TestTaskTypeNamingConvention(t *testing.T) {
	assert.Equal(t, "auth.signup.google", TaskType)

	// Verify it follows the naming convention
	parts := []string{"auth", "signup", "google"}
	assert.Equal(t, parts[0]+"."+parts[1]+"."+parts[2], TaskType)
}

// ==========================
// Workflow Variables Tests
// ==========================

func TestOutput_WorkflowVariables(t *testing.T) {
	output := createValidOutput()

	// Simulate how output would be converted to workflow variables
	// This mimics the completeJob method in handler.go
	vars := map[string]interface{}{
		"success":       output.Success,
		"userId":        output.UserID,
		"email":         output.Email,
		"firstName":     output.FirstName,
		"lastName":      output.LastName,
		"token":         output.Token, // For backward compatibility
		"accessToken":   output.AccessToken,
		"refreshToken":  output.RefreshToken,
		"expiresIn":     output.ExpiresIn,
		"tokenType":     output.TokenType,
		"emailVerified": output.EmailVerified,
		"passwordSet":   output.PasswordSet,
	}

	if output.CRMContactID != "" {
		vars["crmContactId"] = output.CRMContactID
	}

	// Verify all variables are present
	assert.Len(t, vars, 13) // 12 base fields + 1 conditional
	assert.True(t, vars["success"].(bool))
	assert.Equal(t, "user-456", vars["userId"])
	assert.Equal(t, "newuser@example.com", vars["email"])
	assert.Equal(t, "Jane", vars["firstName"])
	assert.Equal(t, "Smith", vars["lastName"])
	assert.Equal(t, "access-token-456", vars["token"])
	assert.Equal(t, "access-token-456", vars["accessToken"])
	assert.Equal(t, "refresh-token-789", vars["refreshToken"])
	assert.Equal(t, 3600, vars["expiresIn"])
	assert.Equal(t, "Bearer", vars["tokenType"])
	assert.True(t, vars["emailVerified"].(bool))
	assert.False(t, vars["passwordSet"].(bool))
	assert.Equal(t, "crm-contact-456", vars["crmContactId"])
}
