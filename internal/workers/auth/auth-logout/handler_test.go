package authlogout

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
		Type:                     "auth.logout",
		ProcessInstanceKey:       key * 10,
		BpmnProcessId:            "test-process",
		ProcessDefinitionVersion: 1,
		ProcessDefinitionKey:     1,
		ElementId:                "Activity_AuthLogout",
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
		UserID:    "user-123",
		Token:     "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test",
		SessionID: "session-456",
		DeviceID:  "device-789",
		LogoutAll: false,
		Reason:    "user_initiated",
		Metadata:  map[string]interface{}{"ip": "192.168.1.1"},
	}
}

func createValidOutput() *Output {
	return &Output{
		Success:             true,
		Message:             "Logout successful",
		SessionsInvalidated: 1,
		TokenRevoked:        true,
		LogoutAt:            time.Now(),
	}
}

func createValidConfig() *Config {
	return &Config{
		Enabled:       true,
		MaxJobsActive: 5,
		Timeout:       10 * time.Second,
		RedisHost:     "localhost",
		RedisPort:     6379,
		RedisPassword: "",
		RedisDB:       0,
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
			name: "missing Redis host",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       10 * time.Second,
					RedisPort:     6379,
				},
			},
			wantErr: true,
			errMsg:  "redis_host is required",
		},
		{
			name: "invalid Redis port (zero)",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       10 * time.Second,
					RedisHost:     "localhost",
					RedisPort:     0,
				},
			},
			wantErr: true,
			errMsg:  "redis_port must be between 1 and 65535",
		},
		{
			name: "invalid Redis port (too high)",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       10 * time.Second,
					RedisHost:     "localhost",
					RedisPort:     70000,
				},
			},
			wantErr: true,
			errMsg:  "redis_port must be between 1 and 65535",
		},
		{
			name: "invalid timeout",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       -1 * time.Second,
					RedisHost:     "localhost",
					RedisPort:     6379,
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
					RedisHost:     "localhost",
					RedisPort:     6379,
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
				"userId":    "user-123",
				"token":     "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test",
				"sessionId": "session-456",
				"deviceId":  "device-789",
				"logoutAll": true,
				"reason":    "security_concern",
				"metadata": map[string]interface{}{
					"ip":        "192.168.1.1",
					"userAgent": "Mozilla/5.0",
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "user-123", input.UserID)
				assert.Equal(t, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test", input.Token)
				assert.Equal(t, "session-456", input.SessionID)
				assert.Equal(t, "device-789", input.DeviceID)
				assert.True(t, input.LogoutAll)
				assert.Equal(t, "security_concern", input.Reason)
				assert.NotNil(t, input.Metadata)
				assert.Equal(t, "192.168.1.1", input.Metadata["ip"])
			},
		},
		{
			name: "valid input minimal fields",
			variables: map[string]interface{}{
				"userId": "user-456",
				"token":  "token-abc-123",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "user-456", input.UserID)
				assert.Equal(t, "token-abc-123", input.Token)
				assert.Empty(t, input.SessionID)
				assert.Empty(t, input.DeviceID)
				assert.False(t, input.LogoutAll)
				assert.Empty(t, input.Reason)
				assert.Nil(t, input.Metadata)
			},
		},
		{
			name: "missing userId",
			variables: map[string]interface{}{
				"token": "token-abc-123",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "missing token",
			variables: map[string]interface{}{
				"userId": "user-123",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "userId too short",
			variables: map[string]interface{}{
				"userId": "ab",
				"token":  "token-abc-123",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "token too short",
			variables: map[string]interface{}{
				"userId": "user-123",
				"token":  "short",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "empty userId string",
			variables: map[string]interface{}{
				"userId": "",
				"token":  "token-abc-123",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "empty token string",
			variables: map[string]interface{}{
				"userId": "user-123",
				"token":  "",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "valid minimum length fields",
			variables: map[string]interface{}{
				"userId": "abc",
				"token":  "1234567890",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "abc", input.UserID)
				assert.Equal(t, "1234567890", input.Token)
			},
		},
		{
			name: "logout all sessions",
			variables: map[string]interface{}{
				"userId":    "user-123",
				"token":     "token-abc-123",
				"logoutAll": true,
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.True(t, input.LogoutAll)
			},
		},
		{
			name: "logout single session",
			variables: map[string]interface{}{
				"userId":    "user-123",
				"token":     "token-abc-123",
				"sessionId": "session-456",
				"logoutAll": false,
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.False(t, input.LogoutAll)
				assert.Equal(t, "session-456", input.SessionID)
			},
		},
		{
			name: "various logout reasons",
			variables: map[string]interface{}{
				"userId": "user-123",
				"token":  "token-abc-123",
				"reason": "user_initiated",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "user_initiated", input.Reason)
			},
		},
		{
			name: "complex metadata",
			variables: map[string]interface{}{
				"userId": "user-123",
				"token":  "token-abc-123",
				"metadata": map[string]interface{}{
					"ip":              "192.168.1.1",
					"userAgent":       "Chrome",
					"logoutInitiator": "admin",
					"timestamp":       1234567890,
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.NotNil(t, input.Metadata)
				assert.Equal(t, "192.168.1.1", input.Metadata["ip"])
				assert.Equal(t, "Chrome", input.Metadata["userAgent"])
				assert.Equal(t, "admin", input.Metadata["logoutInitiator"])
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
			name: "standard error - session invalidation",
			err: &errors.StandardError{
				Code:    "SESSION_INVALIDATION_ERROR",
				Message: "Failed to invalidate session",
			},
			expected: "SESSION_INVALIDATION_ERROR",
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
			name: "standard error - Redis not configured",
			err: &errors.StandardError{
				Code:    "REDIS_NOT_CONFIGURED",
				Message: "Redis client missing",
			},
			expected: "REDIS_NOT_CONFIGURED",
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
				assert.Equal(t, errors.ErrorCode("AUTH_LOGOUT_ERROR"), stdErr.Code)
				assert.Equal(t, "Failed to logout user", stdErr.Message)
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
			name: "missing Redis host",
			config: &Config{
				RedisPort:     6379,
				Timeout:       10 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "redis_host is required",
		},
		{
			name: "invalid Redis port - zero",
			config: &Config{
				RedisHost:     "localhost",
				RedisPort:     0,
				Timeout:       10 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "redis_port must be between 1 and 65535",
		},
		{
			name: "invalid Redis port - negative",
			config: &Config{
				RedisHost:     "localhost",
				RedisPort:     -1,
				Timeout:       10 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "redis_port must be between 1 and 65535",
		},
		{
			name: "invalid Redis port - too high",
			config: &Config{
				RedisHost:     "localhost",
				RedisPort:     65536,
				Timeout:       10 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "redis_port must be between 1 and 65535",
		},
		{
			name: "zero timeout",
			config: &Config{
				RedisHost:     "localhost",
				RedisPort:     6379,
				Timeout:       0,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "negative timeout",
			config: &Config{
				RedisHost:     "localhost",
				RedisPort:     6379,
				Timeout:       -5 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "zero max jobs active",
			config: &Config{
				RedisHost:     "localhost",
				RedisPort:     6379,
				Timeout:       10 * time.Second,
				MaxJobsActive: 0,
			},
			wantErr: true,
			errMsg:  "max_jobs_active must be positive",
		},
		{
			name: "valid config with password",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 10,
				Timeout:       30 * time.Second,
				RedisHost:     "redis.example.com",
				RedisPort:     6380,
				RedisPassword: "secret-password",
				RedisDB:       1,
			},
			wantErr: false,
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
	assert.Equal(t, 6379, config.RedisPort)
	assert.Equal(t, 0, config.RedisDB)
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
				assert.Equal(t, "localhost", cfg.RedisHost)
				assert.Equal(t, 6379, cfg.RedisPort)
			},
		},
		{
			name: "loads from app config",
			appConfig: &config.Config{
				Workers: map[string]config.WorkerConfig{
					"auth-logout": {
						Enabled:       true,
						MaxJobsActive: 10,
						Timeout:       15000,
					},
				},
				Database: config.DatabaseConfig{
					Redis: config.RedisConfig{
						Address:  "redis.example.com:6380",
						Password: "redis-secret",
						DB:       2,
					},
				},
			},
			customConfig: nil,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "redis.example.com", cfg.RedisHost)
				assert.Equal(t, 6380, cfg.RedisPort)
				assert.Equal(t, "redis-secret", cfg.RedisPassword)
				assert.Equal(t, 2, cfg.RedisDB)
				assert.Equal(t, 10, cfg.MaxJobsActive)
				assert.Equal(t, 15*time.Second, cfg.Timeout)
				assert.True(t, cfg.Enabled)
			},
		},
		{
			name:         "uses defaults when no configs provided",
			appConfig:    nil,
			customConfig: nil,
			validate: func(t *testing.T, cfg *Config) {
				assert.True(t, cfg.Enabled)
				assert.Equal(t, 5, cfg.MaxJobsActive)
				assert.Equal(t, 10*time.Second, cfg.Timeout)
				assert.Equal(t, 6379, cfg.RedisPort)
				assert.Equal(t, 0, cfg.RedisDB)
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
	assert.Equal(t, "auth.logout", handler.GetTaskType())
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
	assert.Equal(t, "localhost", handler.GetConfig().RedisHost)
	assert.Equal(t, 6379, handler.GetConfig().RedisPort)
}

// ==========================
// Schema Tests
// ==========================

func TestGetInputSchema(t *testing.T) {
	schema := GetInputSchema()

	assert.Equal(t, "object", schema.Type)
	assert.Contains(t, schema.Required, "userId")
	assert.Contains(t, schema.Required, "token")
	assert.Len(t, schema.Required, 2)

	// Verify key properties exist
	assert.Contains(t, schema.Properties, "userId")
	assert.Contains(t, schema.Properties, "token")
	assert.Contains(t, schema.Properties, "sessionId")
	assert.Contains(t, schema.Properties, "deviceId")
	assert.Contains(t, schema.Properties, "logoutAll")
	assert.Contains(t, schema.Properties, "reason")
	assert.Contains(t, schema.Properties, "metadata")

	// Verify type constraints
	assert.Equal(t, "string", schema.Properties["userId"].Type)
	assert.Equal(t, "string", schema.Properties["token"].Type)
	assert.Equal(t, "boolean", schema.Properties["logoutAll"].Type)
	assert.Equal(t, "object", schema.Properties["metadata"].Type)

	// Verify length constraints
	assert.NotNil(t, schema.Properties["userId"].MinLength)
	assert.Equal(t, 3, *schema.Properties["userId"].MinLength)
	assert.NotNil(t, schema.Properties["token"].MinLength)
	assert.Equal(t, 10, *schema.Properties["token"].MinLength)

	assert.False(t, schema.AdditionalProperties)
}

func TestGetOutputSchema(t *testing.T) {
	schema := GetOutputSchema()

	assert.Equal(t, "object", schema.Type)

	// Verify output properties
	assert.Contains(t, schema.Properties, "success")
	assert.Contains(t, schema.Properties, "message")
	assert.Contains(t, schema.Properties, "sessionsInvalidated")
	assert.Contains(t, schema.Properties, "tokenRevoked")
	assert.Contains(t, schema.Properties, "logoutAt")

	// Verify types
	assert.Equal(t, "boolean", schema.Properties["success"].Type)
	assert.Equal(t, "string", schema.Properties["message"].Type)
	assert.Equal(t, "integer", schema.Properties["sessionsInvalidated"].Type)
	assert.Equal(t, "boolean", schema.Properties["tokenRevoked"].Type)
	assert.Equal(t, "string", schema.Properties["logoutAt"].Type)

	assert.False(t, schema.AdditionalProperties)
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
	assert.Equal(t, input.UserID, decoded.UserID)
	assert.Equal(t, input.Token, decoded.Token)
	assert.Equal(t, input.SessionID, decoded.SessionID)
	assert.Equal(t, input.DeviceID, decoded.DeviceID)
	assert.Equal(t, input.LogoutAll, decoded.LogoutAll)
	assert.Equal(t, input.Reason, decoded.Reason)
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
	assert.Equal(t, output.Message, decoded.Message)
	assert.Equal(t, output.SessionsInvalidated, decoded.SessionsInvalidated)
	assert.Equal(t, output.TokenRevoked, decoded.TokenRevoked)
	// Note: logoutAt might not match exactly due to time serialization
}

func TestOutput_WorkflowVariables(t *testing.T) {
	output := createValidOutput()

	// Simulate how output would be converted to workflow variables
	vars := map[string]interface{}{
		"success":             output.Success,
		"message":             output.Message,
		"sessionsInvalidated": output.SessionsInvalidated,
		"tokenRevoked":        output.TokenRevoked,
		"logoutAt":            output.LogoutAt.Format(time.RFC3339),
	}

	assert.True(t, vars["success"].(bool))
	assert.Equal(t, "Logout successful", vars["message"])
	assert.Equal(t, 1, vars["sessionsInvalidated"])
	assert.True(t, vars["tokenRevoked"].(bool))
	assert.NotEmpty(t, vars["logoutAt"])
}
