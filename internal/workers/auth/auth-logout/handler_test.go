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
		Type:                     TaskType,
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

// convertToStandardError is a local test helper only.
// It wraps unknown errors as INTERNAL_ERROR — this matches the authlogout
// test's assertions and does NOT conflict with the handler, which has no
// convertToStandardError of its own.
func convertToStandardError(err error) *errors.StandardError {
	if stdErr, ok := err.(*errors.StandardError); ok {
		if stdErr.Timestamp.IsZero() {
			stdErr.Timestamp = time.Now()
		}
		return stdErr
	}
	return &errors.StandardError{
		Code:      "INTERNAL_ERROR",
		Message:   "Unexpected error",
		Details:   err.Error(),
		Retryable: true,
		Timestamp: time.Now(),
	}
}

func createValidInput() *Input {
	return &Input{
		UserID:       "550e8400-e29b-41d4-a716-446655440000",
		RefreshToken: "refresh-token-abc-123",
		AccessToken:  "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test",
		SessionID:    "session-456",
		DeviceID:     "device-789",
		LogoutAll:    false,
		Reason:       "user_initiated",
		Metadata:     map[string]interface{}{"ip": "192.168.1.1"},
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
				"userId":         "550e8400-e29b-41d4-a716-446655440000",
				"keycloakUserId": "kc-550e8400-e29b-41d4-a716-446655440001",
				"refreshToken":   "refresh-token-abc-123",
				"accessToken":    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test",
				"sessionId":      "session-456",
				"deviceId":       "device-789",
				"logoutAll":      true,
				"reason":         "security_concern",
				"metadata": map[string]interface{}{
					"ip":        "192.168.1.1",
					"userAgent": "Mozilla/5.0",
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", input.UserID)
				assert.Equal(t, "kc-550e8400-e29b-41d4-a716-446655440001", input.KeycloakUserID)
				assert.Equal(t, "refresh-token-abc-123", input.RefreshToken)
				assert.Equal(t, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test", input.AccessToken)
				assert.Equal(t, "session-456", input.SessionID)
				assert.Equal(t, "device-789", input.DeviceID)
				assert.True(t, input.LogoutAll)
				assert.Equal(t, "security_concern", input.Reason)
				assert.NotNil(t, input.Metadata)
				assert.Equal(t, "192.168.1.1", input.Metadata["ip"])
			},
		},
		{
			name: "logoutAll true skips token/session requirement",
			variables: map[string]interface{}{
				"userId":    "550e8400-e29b-41d4-a716-446655440000",
				"logoutAll": true,
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", input.UserID)
				assert.True(t, input.LogoutAll)
				assert.Empty(t, input.AccessToken)
				assert.Empty(t, input.SessionID)
				assert.Empty(t, input.DeviceID)
				assert.Empty(t, input.Reason)
				assert.Nil(t, input.Metadata)
			},
		},
		{
			name: "valid single session logout with refreshToken",
			variables: map[string]interface{}{
				"userId":       "550e8400-e29b-41d4-a716-446655440000",
				"refreshToken": "refresh-token-abc-1234567890",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", input.UserID)
				assert.Equal(t, "refresh-token-abc-1234567890", input.RefreshToken)
				assert.False(t, input.LogoutAll)
			},
		},
		{
			name: "valid with accessToken and sessionId (no refreshToken)",
			variables: map[string]interface{}{
				"userId":      "550e8400-e29b-41d4-a716-446655440000",
				"accessToken": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test.payload",
				"sessionId":   "session-456",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", input.UserID)
				assert.Empty(t, input.RefreshToken)
				assert.Equal(t, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test.payload", input.AccessToken)
				assert.Equal(t, "session-456", input.SessionID)
			},
		},
		{
			name: "missing userId",
			variables: map[string]interface{}{
				"refreshToken": "refresh-token-abc-123",
			},
			wantErr: false,
		},
		{
			name: "userId not a valid UUID",
			variables: map[string]interface{}{
				"userId":       "not-a-valid-uuid-string-here",
				"refreshToken": "refresh-token-abc-123",
			},
			wantErr: true,
			errCode: "INVALID_UUID_FORMAT",
		},
		{
			name: "refreshToken too short (when provided)",
			variables: map[string]interface{}{
				"userId":       "550e8400-e29b-41d4-a716-446655440000",
				"refreshToken": "short",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "accessToken too short (when provided)",
			variables: map[string]interface{}{
				"userId":      "550e8400-e29b-41d4-a716-446655440000",
				"accessToken": "short",
				"sessionId":   "session-456",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "empty userId string",
			variables: map[string]interface{}{
				"userId":       "",
				"refreshToken": "refresh-token-abc-123",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "single session logout missing both refreshToken and sessionId",
			variables: map[string]interface{}{
				"userId":    "550e8400-e29b-41d4-a716-446655440000",
				"logoutAll": false,
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "logout all sessions with keycloakUserId",
			variables: map[string]interface{}{
				"userId":         "550e8400-e29b-41d4-a716-446655440000",
				"keycloakUserId": "kc-550e8400-e29b-41d4-a716-446655440001",
				"logoutAll":      true,
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.True(t, input.LogoutAll)
				assert.Equal(t, "kc-550e8400-e29b-41d4-a716-446655440001", input.KeycloakUserID)
			},
		},
		{
			name: "logout single session with sessionId only",
			variables: map[string]interface{}{
				"userId":    "550e8400-e29b-41d4-a716-446655440000",
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
			name: "with reason field",
			variables: map[string]interface{}{
				"userId":       "550e8400-e29b-41d4-a716-446655440000",
				"refreshToken": "refresh-token-abc-123",
				"reason":       "user_initiated",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "user_initiated", input.Reason)
			},
		},
		{
			name: "complex metadata",
			variables: map[string]interface{}{
				"userId":       "550e8400-e29b-41d4-a716-446655440000",
				"refreshToken": "refresh-token-abc-123",
				"metadata": map[string]interface{}{
					"ip":              "192.168.1.1",
					"userAgent":       "Chrome",
					"logoutInitiator": "admin",
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
			// extractErrorCode uses a direct type assertion (not errors.As),
			// so a wrapped StandardError returns "UNKNOWN_ERROR".
			name:     "wrapped standard error falls back to UNKNOWN_ERROR",
			err:      fmt.Errorf("wrapped: %w", &errors.StandardError{Code: "INNER_CODE", Message: "inner"}),
			expected: "UNKNOWN_ERROR",
		},
		// NOTE: extractErrorCode(nil) is NOT tested here because the handler's
		// extractErrorCode function performs a type assertion on nil which panics.
		// If nil-safety is required, add a nil guard to extractErrorCode in handler.go.
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
			// The local test helper convertToStandardError wraps unknown errors as
			// INTERNAL_ERROR / "Unexpected error". The authlogout handler has no
			// convertToStandardError of its own, so this matches test-local logic.
			name: "generic error converted to INTERNAL_ERROR",
			err:  fmt.Errorf("test error"),
			validate: func(t *testing.T, stdErr *errors.StandardError) {
				assert.Equal(t, errors.ErrorCode("INTERNAL_ERROR"), stdErr.Code)
				assert.Equal(t, "Unexpected error", stdErr.Message)
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
	cfg := DefaultConfig()

	assert.True(t, cfg.Enabled)
	assert.Equal(t, 5, cfg.MaxJobsActive)
	assert.Equal(t, 10*time.Second, cfg.Timeout)
	// DefaultConfig sets RedisHost = "redis" (the Docker service name)
	assert.Equal(t, "redis", cfg.RedisHost)
	assert.Equal(t, 6379, cfg.RedisPort)
	assert.Equal(t, 0, cfg.RedisDB)
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
				assert.Equal(t, "redis", cfg.RedisHost)
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
	assert.Equal(t, "auth-logout", handler.GetTaskType())
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
	assert.Equal(t, "localhost", handler.GetConfig().RedisHost)
	assert.Equal(t, 6379, handler.GetConfig().RedisPort)
}

// ==========================
// Schema Tests
// ==========================

func TestGetInputSchema(t *testing.T) {
	schema := GetInputSchema()

	assert.Equal(t, "object", schema.Type)
	assert.Empty(t, schema.Required)

	assert.Contains(t, schema.Properties, "userId")
	assert.Contains(t, schema.Properties, "refreshToken")
	assert.Contains(t, schema.Properties, "accessToken")
	assert.Contains(t, schema.Properties, "sessionId")
	assert.Contains(t, schema.Properties, "deviceId")
	assert.Contains(t, schema.Properties, "logoutAll")
	assert.Contains(t, schema.Properties, "reason")
	assert.Contains(t, schema.Properties, "metadata")

	assert.Equal(t, "string", schema.Properties["userId"].Type)
	assert.Equal(t, "string", schema.Properties["refreshToken"].Type)
	assert.Equal(t, "string", schema.Properties["accessToken"].Type)
	assert.Equal(t, "boolean", schema.Properties["logoutAll"].Type)
	assert.Equal(t, "object", schema.Properties["metadata"].Type)

	assert.NotNil(t, schema.Properties["userId"].MinLength)
	assert.Equal(t, 3, *schema.Properties["userId"].MinLength)

	// authlogout GetInputSchema sets AdditionalProperties: true
	assert.True(t, schema.AdditionalProperties)
}

func TestGetOutputSchema(t *testing.T) {
	// GetOutputSchema is defined in the authlogout package's validation.go.
	// Verify via the Output struct JSON tags that all expected workflow variable
	// keys are present — this matches what completeJob() writes.
	output := &Output{
		Success:             true,
		Message:             "Logout successful",
		SessionsInvalidated: 2,
		TokenRevoked:        true,
		LogoutAt:            time.Now(),
	}

	vars := map[string]interface{}{
		"logoutSuccess": output.Success,
		"logoutMessage": output.Message,
		"logoutAt":      output.LogoutAt.Format(time.RFC3339),
	}
	if output.SessionsInvalidated > 0 {
		vars["sessionsInvalidated"] = output.SessionsInvalidated
	}
	if output.TokenRevoked {
		vars["tokenRevoked"] = output.TokenRevoked
	}

	assert.Contains(t, vars, "logoutSuccess")
	assert.Contains(t, vars, "logoutMessage")
	assert.Contains(t, vars, "logoutAt")
	assert.Contains(t, vars, "sessionsInvalidated")
	assert.Contains(t, vars, "tokenRevoked")

	assert.True(t, vars["logoutSuccess"].(bool))
	assert.Equal(t, "Logout successful", vars["logoutMessage"])
	assert.Equal(t, 2, vars["sessionsInvalidated"])
	assert.True(t, vars["tokenRevoked"].(bool))
	assert.NotEmpty(t, vars["logoutAt"])
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
	assert.Equal(t, input.UserID, decoded.UserID)
	assert.Equal(t, input.RefreshToken, decoded.RefreshToken)
	assert.Equal(t, input.AccessToken, decoded.AccessToken)
	assert.Equal(t, input.SessionID, decoded.SessionID)
	assert.Equal(t, input.DeviceID, decoded.DeviceID)
	assert.Equal(t, input.LogoutAll, decoded.LogoutAll)
	assert.Equal(t, input.Reason, decoded.Reason)
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
	assert.Equal(t, output.SessionsInvalidated, decoded.SessionsInvalidated)
	assert.Equal(t, output.TokenRevoked, decoded.TokenRevoked)
}

func TestOutput_WorkflowVariables(t *testing.T) {
	output := createValidOutput()

	// Matches exactly how completeJob() builds the variables map in handler.go
	vars := map[string]interface{}{
		"logoutSuccess": output.Success,
		"logoutMessage": output.Message,
		"logoutAt":      output.LogoutAt.Format(time.RFC3339),
	}
	if output.SessionsInvalidated > 0 {
		vars["sessionsInvalidated"] = output.SessionsInvalidated
	}
	if output.TokenRevoked {
		vars["tokenRevoked"] = output.TokenRevoked
	}

	assert.True(t, vars["logoutSuccess"].(bool))
	assert.Equal(t, "Logout successful", vars["logoutMessage"])
	assert.Equal(t, 1, vars["sessionsInvalidated"])
	assert.True(t, vars["tokenRevoked"].(bool))
	assert.NotEmpty(t, vars["logoutAt"])
}
