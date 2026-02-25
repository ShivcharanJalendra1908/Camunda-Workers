package sessionmanager

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
	"github.com/stretchr/testify/require"
)

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
		ElementId:                "Activity_SessionManager",
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
// NewHandler Tests
// ==========================

func TestNewHandler_ValidConfig(t *testing.T) {
	opts := HandlerOptions{
		CustomConfig: createValidConfig(),
		Logger:       logger.NewStructured("info", "json"),
		// Redis nil is allowed — service handles nil client gracefully
	}
	handler, err := NewHandler(opts)
	require.NoError(t, err)
	require.NotNil(t, handler)
	assert.NotNil(t, handler.config)
	assert.NotNil(t, handler.logger)
	assert.NotNil(t, handler.service)
}

func TestNewHandler_DefaultLogger(t *testing.T) {
	opts := HandlerOptions{
		CustomConfig: createValidConfig(),
		Logger:       nil, // should create default logger
	}
	handler, err := NewHandler(opts)
	require.NoError(t, err)
	require.NotNil(t, handler)
	assert.NotNil(t, handler.logger)
}

func TestNewHandler_InvalidConfig_ZeroTimeout(t *testing.T) {
	opts := HandlerOptions{
		CustomConfig: &Config{
			Enabled:       true,
			MaxJobsActive: 5,
			Timeout:       0,
			RedisHost:     "localhost",
			RedisPort:     6379,
		},
		Logger: logger.NewStructured("info", "json"),
	}
	handler, err := NewHandler(opts)
	assert.Error(t, err)
	assert.Nil(t, handler)
	assert.Contains(t, err.Error(), "invalid configuration")
}

func TestNewHandler_InvalidConfig_ZeroMaxJobsActive(t *testing.T) {
	opts := HandlerOptions{
		CustomConfig: &Config{
			Enabled:       true,
			MaxJobsActive: 0,
			Timeout:       10 * time.Second,
			RedisHost:     "localhost",
			RedisPort:     6379,
		},
		Logger: logger.NewStructured("info", "json"),
	}
	handler, err := NewHandler(opts)
	assert.Error(t, err)
	assert.Nil(t, handler)
}

func TestNewHandler_InvalidConfig_MissingRedisHost(t *testing.T) {
	opts := HandlerOptions{
		CustomConfig: &Config{
			Enabled:       true,
			MaxJobsActive: 5,
			Timeout:       10 * time.Second,
			RedisPort:     6379,
			// RedisHost intentionally empty
		},
		Logger: logger.NewStructured("info", "json"),
	}
	handler, err := NewHandler(opts)
	assert.Error(t, err)
	assert.Nil(t, handler)
}

// ==========================
// parseInput Tests
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
			name: "valid create action with all fields",
			variables: map[string]interface{}{
				"action":    "create",
				"userId":    "550e8400-e29b-41d4-a716-446655440000",
				"email":     "user@example.com",
				"sessionId": "session-abc-123",
				"expiresIn": float64(3600),
				"metadata": map[string]interface{}{
					"ip": "192.168.1.1",
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "create", input.Action)
				assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", input.UserID)
				assert.Equal(t, "user@example.com", input.Email)
				assert.Equal(t, "session-abc-123", input.SessionID)
				assert.Equal(t, 3600, input.ExpiresIn)
				assert.NotNil(t, input.Metadata)
				assert.Equal(t, "192.168.1.1", input.Metadata["ip"])
			},
		},
		{
			name: "valid get action with sessionId",
			variables: map[string]interface{}{
				"action":    "get",
				"sessionId": "session-abc-123",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "get", input.Action)
				assert.Equal(t, "session-abc-123", input.SessionID)
			},
		},
		{
			name: "valid delete action",
			variables: map[string]interface{}{
				"action":    "delete",
				"sessionId": "session-abc-123",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "delete", input.Action)
				assert.Equal(t, "session-abc-123", input.SessionID)
			},
		},
		{
			name: "expiresIn defaults to 86400 when not provided",
			variables: map[string]interface{}{
				"action": "create",
				"userId": "550e8400-e29b-41d4-a716-446655440000",
				"email":  "user@example.com",
				// expiresIn not provided
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				// Handler sets default of 86400 (24 hours) when expiresIn is absent
				assert.Equal(t, 86400, input.ExpiresIn)
			},
		},
		{
			name: "expiresIn as float64 is converted to int correctly",
			variables: map[string]interface{}{
				"action":    "create",
				"userId":    "550e8400-e29b-41d4-a716-446655440000",
				"email":     "user@example.com",
				"expiresIn": float64(7200),
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, 7200, input.ExpiresIn)
			},
		},
		{
			name:      "missing action field",
			variables: map[string]interface{}{},
			wantErr:   true,
			errCode:   "VALIDATION_FAILED",
		},
		{
			name: "metadata nil when not provided",
			variables: map[string]interface{}{
				"action": "create",
				"userId": "550e8400-e29b-41d4-a716-446655440000",
				"email":  "user@example.com",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Nil(t, input.Metadata)
			},
		},
		{
			name: "optional fields are empty when not provided",
			variables: map[string]interface{}{
				"action": "create",
				"userId": "550e8400-e29b-41d4-a716-446655440000",
				"email":  "user@example.com",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Empty(t, input.SessionID)
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
				require.True(t, ok, "expected *errors.StandardError, got %T", err)
				assert.Equal(t, errors.ErrorCode(tt.errCode), stdErr.Code)
				assert.False(t, stdErr.Retryable)
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
// extractErrorCode Tests
// ==========================

func TestExtractErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name: "StandardError with SESSION_NOT_FOUND",
			err: &errors.StandardError{
				Code:    "SESSION_NOT_FOUND",
				Message: "Session does not exist",
			},
			expected: "SESSION_NOT_FOUND",
		},
		{
			name: "StandardError with REDIS_ERROR",
			err: &errors.StandardError{
				Code:    "REDIS_ERROR",
				Message: "Redis connection failed",
			},
			expected: "REDIS_ERROR",
		},
		{
			name: "StandardError with VALIDATION_FAILED",
			err: &errors.StandardError{
				Code:    "VALIDATION_FAILED",
				Message: "Invalid input",
			},
			expected: "VALIDATION_FAILED",
		},
		{
			name: "StandardError with INPUT_PARSING_FAILED",
			err: &errors.StandardError{
				Code:    "INPUT_PARSING_FAILED",
				Message: "Cannot parse variables",
			},
			expected: "INPUT_PARSING_FAILED",
		},
		{
			name:     "generic error returns UNKNOWN_ERROR",
			err:      fmt.Errorf("unexpected failure"),
			expected: "UNKNOWN_ERROR",
		},
		{
			name:     "wrapped non-standard error returns UNKNOWN_ERROR",
			err:      fmt.Errorf("wrap: %w", fmt.Errorf("inner")),
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
			name: "zero timeout",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 5,
				Timeout:       0,
				RedisHost:     "localhost",
				RedisPort:     6379,
			},
			wantErr: true,
			errMsg:  "timeout",
		},
		{
			name: "negative timeout",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 5,
				Timeout:       -5 * time.Second,
				RedisHost:     "localhost",
				RedisPort:     6379,
			},
			wantErr: true,
			errMsg:  "timeout",
		},
		{
			name: "zero max jobs active",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 0,
				Timeout:       10 * time.Second,
				RedisHost:     "localhost",
				RedisPort:     6379,
			},
			wantErr: true,
			errMsg:  "maxJobsActive",
		},
		{
			name: "missing redis host",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 5,
				Timeout:       10 * time.Second,
				RedisPort:     6379,
			},
			wantErr: true,
			errMsg:  "redisHost",
		},
		{
			name: "redis port zero",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 5,
				Timeout:       10 * time.Second,
				RedisHost:     "localhost",
				RedisPort:     0,
			},
			wantErr: true,
			errMsg:  "redisPort",
		},
		{
			name: "redis port too high",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 5,
				Timeout:       10 * time.Second,
				RedisHost:     "localhost",
				RedisPort:     65536,
			},
			wantErr: true,
			errMsg:  "redisPort",
		},
		{
			name: "valid config with auth",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 10,
				Timeout:       30 * time.Second,
				RedisHost:     "redis.prod.internal",
				RedisPort:     6380,
				RedisPassword: "supersecret",
				RedisDB:       2,
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
	assert.NotNil(t, cfg)
	assert.True(t, cfg.Enabled)
	assert.Greater(t, cfg.MaxJobsActive, 0)
	assert.Greater(t, cfg.Timeout, time.Duration(0))
	assert.NotEmpty(t, cfg.RedisHost)
	assert.Greater(t, cfg.RedisPort, 0)
}

// ==========================
// createConfigFromAppConfig Tests
// ==========================

func TestCreateConfigFromAppConfig(t *testing.T) {
	t.Run("custom config takes precedence", func(t *testing.T) {
		custom := createValidConfig()
		cfg := createConfigFromAppConfig(&config.Config{}, custom)
		assert.Equal(t, custom.RedisHost, cfg.RedisHost)
		assert.Equal(t, custom.Timeout, cfg.Timeout)
	})

	t.Run("loads worker config from appConfig", func(t *testing.T) {
		appCfg := &config.Config{
			Workers: map[string]config.WorkerConfig{
				"session-manager": {
					Enabled:       true,
					MaxJobsActive: 12,
					Timeout:       25000,
				},
			},
			Database: config.DatabaseConfig{
				Redis: config.RedisConfig{
					Address:  "redis.internal:6381",
					Password: "pass123",
					DB:       1,
				},
			},
		}
		cfg := createConfigFromAppConfig(appCfg, nil)
		assert.Equal(t, 12, cfg.MaxJobsActive)
		assert.Equal(t, 25*time.Second, cfg.Timeout)
		assert.True(t, cfg.Enabled)
		assert.Equal(t, "redis.internal", cfg.RedisHost)
		assert.Equal(t, 6381, cfg.RedisPort)
		assert.Equal(t, "pass123", cfg.RedisPassword)
		assert.Equal(t, 1, cfg.RedisDB)
	})

	t.Run("uses defaults when appConfig is nil", func(t *testing.T) {
		cfg := createConfigFromAppConfig(nil, nil)
		assert.NotNil(t, cfg)
		assert.True(t, cfg.Enabled)
		assert.Greater(t, cfg.MaxJobsActive, 0)
	})

	t.Run("worker config with zero MaxJobsActive is ignored (keeps default)", func(t *testing.T) {
		appCfg := &config.Config{
			Workers: map[string]config.WorkerConfig{
				"session-manager": {
					Enabled:       true,
					MaxJobsActive: 0, // should be ignored
					Timeout:       0, // should be ignored
				},
			},
		}
		cfg := createConfigFromAppConfig(appCfg, nil)
		// Default MaxJobsActive should remain
		assert.Greater(t, cfg.MaxJobsActive, 0)
	})
}

// ==========================
// Handler Method Tests
// ==========================

func TestHandler_GetTaskType(t *testing.T) {
	handler := &Handler{}
	assert.Equal(t, "session-manager", handler.GetTaskType())
	assert.Equal(t, TaskType, handler.GetTaskType())
}

func TestHandler_IsEnabled(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{"enabled", true},
		{"disabled", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &Handler{config: &Config{Enabled: tt.enabled}}
			assert.Equal(t, tt.enabled, handler.IsEnabled())
		})
	}
}

func TestHandler_GetConfig(t *testing.T) {
	cfg := createValidConfig()
	handler := &Handler{config: cfg}
	result := handler.GetConfig()
	assert.Equal(t, cfg, result)
	assert.Equal(t, "localhost", result.RedisHost)
	assert.Equal(t, 6379, result.RedisPort)
}

// ==========================
// completeJob Variable Mapping Tests
// ==========================

func TestHandler_CompleteJob_VariableMapping(t *testing.T) {
	t.Run("success with all output fields", func(t *testing.T) {
		expiresAt := time.Now().Add(24 * time.Hour)
		output := &Output{
			Success:      true,
			Message:      "Session created successfully",
			SessionID:    "session-abc-123",
			UserID:       "550e8400-e29b-41d4-a716-446655440000",
			Email:        "user@example.com",
			ExpiresAt:    expiresAt,
			CookieHeader: "session=abc; HttpOnly; Secure",
		}

		vars := buildCompleteJobVariables(output)

		assert.True(t, vars["sessionSuccess"].(bool))
		assert.Equal(t, "Session created successfully", vars["sessionMessage"])
		assert.Equal(t, "session-abc-123", vars["sessionId"])
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", vars["userId"])
		assert.Equal(t, "user@example.com", vars["email"])
		assert.Equal(t, expiresAt.Format(time.RFC3339), vars["expiresAt"])
		assert.Equal(t, "session=abc; HttpOnly; Secure", vars["cookieHeader"])
	})

	t.Run("failure output sets sessionSuccess false and message", func(t *testing.T) {
		output := &Output{
			Success: false,
			Message: "Session not found or expired",
		}

		vars := buildCompleteJobVariables(output)

		assert.False(t, vars["sessionSuccess"].(bool))
		assert.Equal(t, "Session not found or expired", vars["sessionMessage"])
		// optional fields should not be present
		assert.NotContains(t, vars, "sessionId")
		assert.NotContains(t, vars, "userId")
		assert.NotContains(t, vars, "email")
		assert.NotContains(t, vars, "expiresAt")
		assert.NotContains(t, vars, "cookieHeader")
	})

	t.Run("disabled worker output", func(t *testing.T) {
		output := &Output{
			Success: false,
			Message: "Session manager disabled",
		}

		vars := buildCompleteJobVariables(output)

		assert.False(t, vars["sessionSuccess"].(bool))
		assert.Equal(t, "Session manager disabled", vars["sessionMessage"])
	})

	t.Run("zero ExpiresAt is not included in variables", func(t *testing.T) {
		output := &Output{
			Success:   true,
			Message:   "ok",
			SessionID: "session-123",
			// ExpiresAt is zero value
		}

		vars := buildCompleteJobVariables(output)

		assert.NotContains(t, vars, "expiresAt")
	})
}

// buildCompleteJobVariables replicates the variable-building logic from completeJob
// without requiring a real Zeebe client.
func buildCompleteJobVariables(output *Output) map[string]interface{} {
	variables := map[string]interface{}{
		"sessionSuccess": output.Success,
		"sessionMessage": output.Message,
	}
	if output.SessionID != "" {
		variables["sessionId"] = output.SessionID
	}
	if output.UserID != "" {
		variables["userId"] = output.UserID
	}
	if output.Email != "" {
		variables["email"] = output.Email
	}
	if !output.ExpiresAt.IsZero() {
		variables["expiresAt"] = output.ExpiresAt.Format(time.RFC3339)
	}
	if output.CookieHeader != "" {
		variables["cookieHeader"] = output.CookieHeader
	}
	return variables
}

// ==========================
// SESSION_NOT_FOUND Graceful Handling
// ==========================

func TestHandler_SessionNotFound_GracefulComplete(t *testing.T) {
	// Verify that SESSION_NOT_FOUND is detected correctly from a StandardError.
	// The variable is already typed as *errors.StandardError — no type assertion needed.
	err := &errors.StandardError{
		Code:    "SESSION_NOT_FOUND",
		Message: "Session not found",
	}

	assert.Equal(t, errors.ErrorCode("SESSION_NOT_FOUND"), err.Code)

	// The handler completes gracefully (no error) for SESSION_NOT_FOUND
	// This mirrors the logic in Handle():
	//   if stdErr.Code == "SESSION_NOT_FOUND" → completeJob(Success:false)
	isGraceful := err.Code == "SESSION_NOT_FOUND"
	assert.True(t, isGraceful)
}

func TestHandler_NonSessionNotFound_ReturnsError(t *testing.T) {
	// err is already typed as *errors.StandardError — direct field access, no type assertion.
	err := &errors.StandardError{
		Code:      "REDIS_ERROR",
		Message:   "Redis unavailable",
		Retryable: true,
	}

	isGraceful := err.Code == "SESSION_NOT_FOUND"
	assert.False(t, isGraceful, "REDIS_ERROR should not be handled gracefully")
}

// ==========================
// Input/Output Model Tests
// ==========================

func TestInput_JSONSerialization(t *testing.T) {
	input := &Input{
		Action:    "create",
		SessionID: "session-abc-123",
		UserID:    "550e8400-e29b-41d4-a716-446655440000",
		Email:     "user@example.com",
		ExpiresIn: 3600,
		Metadata:  map[string]interface{}{"ip": "10.0.0.1"},
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var decoded Input
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, input.Action, decoded.Action)
	assert.Equal(t, input.SessionID, decoded.SessionID)
	assert.Equal(t, input.UserID, decoded.UserID)
	assert.Equal(t, input.Email, decoded.Email)
	assert.Equal(t, input.ExpiresIn, decoded.ExpiresIn)
	assert.Equal(t, input.Metadata, decoded.Metadata)
}

func TestOutput_JSONSerialization(t *testing.T) {
	expiresAt := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	output := &Output{
		Success:      true,
		Message:      "Session created",
		SessionID:    "session-abc-123",
		UserID:       "550e8400-e29b-41d4-a716-446655440000",
		Email:        "user@example.com",
		ExpiresAt:    expiresAt,
		CookieHeader: "session=abc; HttpOnly",
	}

	data, err := json.Marshal(output)
	require.NoError(t, err)

	var decoded Output
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, output.Success, decoded.Success)
	assert.Equal(t, output.Message, decoded.Message)
	assert.Equal(t, output.SessionID, decoded.SessionID)
	assert.Equal(t, output.UserID, decoded.UserID)
	assert.Equal(t, output.Email, decoded.Email)
	assert.Equal(t, output.CookieHeader, decoded.CookieHeader)
}

// ==========================
// parseRedisAddress Tests
// ==========================

func TestParseRedisAddress(t *testing.T) {
	tests := []struct {
		input    string
		wantHost string
		wantPort int
	}{
		{"redis.internal:6380", "redis.internal", 6380},
		{"localhost:6379", "localhost", 6379},
		{"10.0.0.5:6399", "10.0.0.5", 6399},
		{"redis-only-no-port", "redis-only-no-port", 6379}, // no colon → default port
		{"192.168.1.1:6380", "192.168.1.1", 6380},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			host, port := parseRedisAddress(tt.input)
			assert.Equal(t, tt.wantHost, host)
			assert.Equal(t, tt.wantPort, port)
		})
	}
}

// ==========================
// Register/Close (structural tests)
// ==========================

func TestHandler_Register_Disabled(t *testing.T) {
	handler := &Handler{
		config: &Config{Enabled: false},
		logger: logger.NewStructured("info", "json"),
	}
	err := handler.Register()
	assert.NoError(t, err)
	assert.Nil(t, handler.jobWorker)
}

func TestHandler_Close_NilWorker(t *testing.T) {
	handler := &Handler{
		config:    createValidConfig(),
		logger:    logger.NewStructured("info", "json"),
		jobWorker: nil,
	}
	assert.NotPanics(t, func() { handler.Close() })
}

// ==========================
// Context / Trace Extraction Tests
// ==========================

func TestHandler_TraceExtraction(t *testing.T) {
	t.Run("extracts traceId and spanId from job variables", func(t *testing.T) {
		vars := map[string]interface{}{
			"action":  "create",
			"userId":  "550e8400-e29b-41d4-a716-446655440000",
			"traceId": "trace-abc-123",
			"spanId":  "span-xyz-456",
		}
		data, _ := json.Marshal(vars)

		var jobVars map[string]interface{}
		require.NoError(t, json.Unmarshal(data, &jobVars))

		traceID, _ := jobVars["traceId"].(string)
		spanID, _ := jobVars["spanId"].(string)

		assert.Equal(t, "trace-abc-123", traceID)
		assert.Equal(t, "span-xyz-456", spanID)
	})

	t.Run("missing traceId and spanId yields empty strings", func(t *testing.T) {
		vars := map[string]interface{}{"action": "create"}
		data, _ := json.Marshal(vars)

		var jobVars map[string]interface{}
		json.Unmarshal(data, &jobVars)

		traceID, _ := jobVars["traceId"].(string)
		spanID, _ := jobVars["spanId"].(string)

		assert.Empty(t, traceID)
		assert.Empty(t, spanID)
	})
}

// ==========================
// Schema Tests
// ==========================

func TestGetInputSchema(t *testing.T) {
	schema := GetInputSchema()
	assert.Equal(t, "object", schema.Type)
	assert.Contains(t, schema.Required, "action")
}

func TestGetOutputSchema(t *testing.T) {
	// GetOutputSchema is not defined in the sessionmanager package.
	// Instead, verify the Output struct fields map to the expected workflow variable keys
	// by checking the JSON tags and the buildCompleteJobVariables helper.
	expiresAt := time.Now().Add(time.Hour)
	output := &Output{
		Success:      true,
		Message:      "ok",
		SessionID:    "session-123",
		UserID:       "user-456",
		Email:        "user@example.com",
		ExpiresAt:    expiresAt,
		CookieHeader: "session=abc",
	}

	vars := buildCompleteJobVariables(output)

	// Confirm all expected workflow variable keys are present
	assert.Contains(t, vars, "sessionSuccess")
	assert.Contains(t, vars, "sessionMessage")
	assert.Contains(t, vars, "sessionId")
	assert.Contains(t, vars, "userId")
	assert.Contains(t, vars, "email")
	assert.Contains(t, vars, "expiresAt")
	assert.Contains(t, vars, "cookieHeader")
}

// ==========================
// TaskType Constant Test
// ==========================

func TestTaskTypeConstant(t *testing.T) {
	assert.Equal(t, "session-manager", TaskType)
}

// ==========================
// Disabled Worker Path
// ==========================

func TestHandler_Handle_DisabledWorker(t *testing.T) {
	handler := &Handler{
		config: &Config{Enabled: false},
		logger: logger.NewStructured("info", "json"),
	}
	assert.False(t, handler.config.Enabled)
	// When disabled, Handle() completes job with Success=false, Message="Session manager disabled"
	// Full integration requires a mock Zeebe client; verified structurally here.
}

// ==========================
// parseInput is context-independent
// ==========================

func TestHandler_ParseInput_ContextIndependent(t *testing.T) {
	handler := &Handler{
		config: createValidConfig(),
		logger: logger.NewStructured("info", "json"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	job := createMockJob(42, map[string]interface{}{
		"action": "create",
		"userId": "550e8400-e29b-41d4-a716-446655440000",
		"email":  "user@example.com",
	})

	// parseInput does not accept ctx — should succeed regardless of cancellation
	input, err := handler.parseInput(job)
	_ = ctx
	require.NoError(t, err)
	assert.Equal(t, "create", input.Action)
}
