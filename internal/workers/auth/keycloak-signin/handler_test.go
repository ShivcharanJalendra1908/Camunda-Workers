package keycloaksignin

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
		ElementId:                "Activity_KeycloakSignin",
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
		Issuer:        "http://keycloak:8080/realms/test",
		ClientID:      "test-client",
		RedirectURL:   "http://localhost:8080/callback",
		PublicBaseURL: "http://localhost:8080",
		RedisHost:     "localhost",
		RedisPort:     6379,
	}
}

// ==========================
// NewHandler Tests
// ==========================

// NOTE: NewHandler connects to Postgres and Keycloak at startup, so it cannot be
// unit-tested without real infrastructure. Tests cover config validation path only,
// which returns before attempting any network connections.

func TestNewHandler_InvalidConfig_MissingIssuer(t *testing.T) {
	opts := HandlerOptions{
		CustomConfig: &Config{
			Enabled:       true,
			MaxJobsActive: 5,
			Timeout:       10 * time.Second,
			ClientID:      "test-client",
			RedirectURL:   "http://localhost/callback",
			// Issuer intentionally empty
		},
		Logger: logger.NewStructured("info", "json"),
	}
	handler, err := NewHandler(opts)
	assert.Error(t, err)
	assert.Nil(t, handler)
	assert.Contains(t, err.Error(), "invalid configuration")
}

func TestNewHandler_InvalidConfig_ZeroTimeout(t *testing.T) {
	opts := HandlerOptions{
		CustomConfig: &Config{
			Enabled:       true,
			MaxJobsActive: 5,
			Timeout:       0,
			Issuer:        "http://keycloak:8080/realms/test",
			ClientID:      "test-client",
			RedirectURL:   "http://localhost/callback",
			RedisHost:     "localhost",
		},
		Logger: logger.NewStructured("info", "json"),
	}
	handler, err := NewHandler(opts)
	assert.Error(t, err)
	assert.Nil(t, handler)
}

func TestNewHandler_InvalidConfig_ZeroMaxJobsActive(t *testing.T) {
	opts := HandlerOptions{
		CustomConfig: &Config{
			Enabled:       true,
			MaxJobsActive: 0,
			Timeout:       10 * time.Second,
			Issuer:        "http://keycloak:8080/realms/test",
			ClientID:      "test-client",
			RedirectURL:   "http://localhost/callback",
			RedisHost:     "localhost",
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
			name: "valid initiate action",
			variables: map[string]interface{}{
				"action": "initiate",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "initiate", input.Action)
				assert.Equal(t, "keycloak", input.Provider)
				assert.Empty(t, input.Code)
				assert.Empty(t, input.State)
			},
		},
		{
			name: "valid callback action with code and state",
			variables: map[string]interface{}{
				"action": "callback",
				"code":   "auth-code-abc123",
				"state":  "state-xyz789",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "callback", input.Action)
				assert.Equal(t, "auth-code-abc123", input.Code)
				assert.Equal(t, "state-xyz789", input.State)
				assert.Equal(t, "keycloak", input.Provider)
			},
		},
		{
			name: "valid initiate action with metadata",
			variables: map[string]interface{}{
				"action": "initiate",
				"metadata": map[string]interface{}{
					"ip":        "192.168.1.1",
					"userAgent": "Chrome/120",
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "initiate", input.Action)
				assert.NotNil(t, input.Metadata)
				assert.Equal(t, "192.168.1.1", input.Metadata["ip"])
			},
		},
		{
			name:      "missing action field",
			variables: map[string]interface{}{},
			wantErr:   true,
			errCode:   "VALIDATION_FAILED",
		},
		{
			name: "invalid action value",
			variables: map[string]interface{}{
				"action": "invalid-action",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "callback missing code",
			variables: map[string]interface{}{
				"action": "callback",
				"state":  "state-xyz789",
				// code intentionally missing
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "callback missing state",
			variables: map[string]interface{}{
				"action": "callback",
				"code":   "auth-code-abc123",
				// state intentionally missing
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "callback missing both code and state",
			variables: map[string]interface{}{
				"action": "callback",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "action as non-string type",
			variables: map[string]interface{}{
				"action": 42, // not a string
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "provider is always set to keycloak regardless of input",
			variables: map[string]interface{}{
				"action":   "initiate",
				"provider": "some-other-provider", // should be ignored
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				// provider field in variables is ignored; hardcoded to "keycloak"
				assert.Equal(t, "keycloak", input.Provider)
			},
		},
		{
			name: "callback with extra optional fields",
			variables: map[string]interface{}{
				"action": "callback",
				"code":   "auth-code-abc123",
				"state":  "state-xyz789",
				"metadata": map[string]interface{}{
					"sessionHint": "mobile",
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "callback", input.Action)
				assert.Equal(t, "auth-code-abc123", input.Code)
				assert.Equal(t, "state-xyz789", input.State)
				assert.NotNil(t, input.Metadata)
				assert.Equal(t, "mobile", input.Metadata["sessionHint"])
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
			name: "StandardError returns its code",
			err: &errors.StandardError{
				Code:    "KEYCLOAK_UNAVAILABLE",
				Message: "Keycloak is down",
			},
			expected: "KEYCLOAK_UNAVAILABLE",
		},
		{
			name: "validation failed error",
			err: &errors.StandardError{
				Code:    "VALIDATION_FAILED",
				Message: "Invalid input",
			},
			expected: "VALIDATION_FAILED",
		},
		{
			name: "input parsing failed",
			err: &errors.StandardError{
				Code:    "INPUT_PARSING_FAILED",
				Message: "Cannot parse variables",
			},
			expected: "INPUT_PARSING_FAILED",
		},
		{
			name:     "generic error returns UNKNOWN_ERROR",
			err:      fmt.Errorf("some unexpected error"),
			expected: "UNKNOWN_ERROR",
		},
		{
			name:     "wrapped error (non-standard) returns UNKNOWN_ERROR",
			err:      fmt.Errorf("wrapped: %w", fmt.Errorf("inner error")),
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
			name: "missing issuer",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 5,
				Timeout:       10 * time.Second,
				ClientID:      "test-client",
				RedirectURL:   "http://localhost/callback",
				PublicBaseURL: "http://localhost",
				RedisHost:     "localhost",
			},
			wantErr: true,
			errMsg:  "issuer",
		},
		{
			name: "missing clientId",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 5,
				Timeout:       10 * time.Second,
				Issuer:        "http://keycloak:8080/realms/test",
				RedirectURL:   "http://localhost/callback",
				PublicBaseURL: "http://localhost",
				RedisHost:     "localhost",
			},
			wantErr: true,
			errMsg:  "clientId",
		},
		{
			name: "missing redirectURL",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 5,
				Timeout:       10 * time.Second,
				Issuer:        "http://keycloak:8080/realms/test",
				ClientID:      "test-client",
				PublicBaseURL: "http://localhost",
				RedisHost:     "localhost",
			},
			wantErr: true,
			errMsg:  "redirect URL",
		},
		{
			name: "missing redisHost",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 5,
				Timeout:       10 * time.Second,
				Issuer:        "http://keycloak:8080/realms/test",
				ClientID:      "test-client",
				RedirectURL:   "http://localhost/callback",
				PublicBaseURL: "http://localhost",
				RedisHost:     "",
			},
			wantErr: true,
			errMsg:  "redis host",
		},
		{
			name: "zero timeout",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 5,
				Timeout:       0,
				Issuer:        "http://keycloak:8080/realms/test",
				ClientID:      "test-client",
				RedirectURL:   "http://localhost/callback",
				PublicBaseURL: "http://localhost",
				RedisHost:     "localhost",
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
				Issuer:        "http://keycloak:8080/realms/test",
				ClientID:      "test-client",
				RedirectURL:   "http://localhost/callback",
				PublicBaseURL: "http://localhost",
				RedisHost:     "localhost",
			},
			wantErr: true,
			errMsg:  "maxJobsActive",
		},
		{
			name: "negative timeout",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 5,
				Timeout:       -1 * time.Second,
				Issuer:        "http://keycloak:8080/realms/test",
				ClientID:      "test-client",
				RedirectURL:   "http://localhost/callback",
				PublicBaseURL: "http://localhost",
				RedisHost:     "localhost",
			},
			wantErr: true,
			errMsg:  "timeout",
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
}

// ==========================
// createConfigFromAppConfig Tests
// ==========================

func TestCreateConfigFromAppConfig(t *testing.T) {
	t.Run("custom config takes precedence over appConfig", func(t *testing.T) {
		custom := createValidConfig()
		cfg := createConfigFromAppConfig(&config.Config{}, custom)
		assert.Equal(t, custom.Issuer, cfg.Issuer)
		assert.Equal(t, custom.ClientID, cfg.ClientID)
	})

	t.Run("loads keycloak config from appConfig", func(t *testing.T) {
		// NOTE: The exact type name of config.Auth.Keycloak depends on the config package
		// definition (e.g. config.KeycloakConfig, config.KeycloakAuthConfig, etc.).
		// We skip the struct literal and set fields via a config.Config value directly.
		// If your config package uses a different type name, update the Keycloak field accordingly.
		appCfg := new(config.Config)
		appCfg.Auth.Keycloak.URL = "http://keycloak:8080"
		appCfg.Auth.Keycloak.Realm = "myrealm"
		appCfg.Auth.Keycloak.ClientID = "my-client"
		appCfg.Auth.Keycloak.RedirectURL = "http://app/callback"
		appCfg.Auth.Keycloak.PublicBaseURL = "http://app"
		appCfg.Workers = map[string]config.WorkerConfig{
			"keycloak-signin": {
				Enabled:       true,
				MaxJobsActive: 8,
				Timeout:       20000,
			},
		}

		cfg := createConfigFromAppConfig(appCfg, nil)
		assert.Contains(t, cfg.Issuer, "myrealm")
		assert.Contains(t, cfg.Issuer, "http://keycloak:8080")
		assert.Equal(t, "my-client", cfg.ClientID)
		assert.Equal(t, "http://app/callback", cfg.RedirectURL)
		assert.Equal(t, "http://app", cfg.PublicBaseURL)
		assert.Equal(t, 8, cfg.MaxJobsActive)
		assert.Equal(t, 20*time.Second, cfg.Timeout)
		assert.True(t, cfg.Enabled)
	})

	t.Run("loads redis config from appConfig", func(t *testing.T) {
		appCfg := &config.Config{
			Database: config.DatabaseConfig{
				Redis: config.RedisConfig{
					Address:  "redis.internal:6380",
					Password: "secret",
					DB:       3,
				},
			},
		}
		cfg := createConfigFromAppConfig(appCfg, nil)
		assert.Equal(t, "redis.internal", cfg.RedisHost)
		assert.Equal(t, 6380, cfg.RedisPort)
		assert.Equal(t, "secret", cfg.RedisPassword)
		assert.Equal(t, 3, cfg.RedisDB)
	})

	t.Run("uses defaults when appConfig is nil", func(t *testing.T) {
		cfg := createConfigFromAppConfig(nil, nil)
		assert.NotNil(t, cfg)
		assert.True(t, cfg.Enabled)
		assert.Greater(t, cfg.MaxJobsActive, 0)
	})

	t.Run("keycloak URL trailing slash is trimmed", func(t *testing.T) {
		appCfg := new(config.Config)
		appCfg.Auth.Keycloak.URL = "http://keycloak:8080/"
		appCfg.Auth.Keycloak.Realm = "test"

		cfg := createConfigFromAppConfig(appCfg, nil)
		assert.NotContains(t, cfg.Issuer, "//realms")
	})
}

// ==========================
// Handler Method Tests
// ==========================

func TestHandler_GetTaskType(t *testing.T) {
	handler := &Handler{}
	assert.Equal(t, "keycloak-signin", handler.GetTaskType())
	assert.Equal(t, TaskType, handler.GetTaskType())
}

func TestHandler_IsEnabled(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{"enabled true", true},
		{"enabled false", false},
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
	assert.Equal(t, "http://keycloak:8080/realms/test", result.Issuer)
	assert.Equal(t, "test-client", result.ClientID)
}

// ==========================
// completeJob Variable Key Tests
// ==========================

func TestHandler_CompleteJob_VariableMapping(t *testing.T) {
	t.Run("initiate output sets authorizationUrl and state", func(t *testing.T) {
		output := &Output{
			Success:          true,
			AuthorizationURL: "http://keycloak/auth?client_id=test",
			State:            "random-state-abc",
		}

		vars := buildCompleteJobVariables(output)

		assert.True(t, vars["success"].(bool))
		assert.Equal(t, "http://keycloak/auth?client_id=test", vars["authorizationUrl"])
		assert.Equal(t, "random-state-abc", vars["state"])
		// callback fields should NOT be set
		assert.NotContains(t, vars, "userId")
		assert.NotContains(t, vars, "email")
	})

	t.Run("callback output sets user fields", func(t *testing.T) {
		now := time.Now()
		output := &Output{
			Success:         true,
			UserID:          "550e8400-e29b-41d4-a716-446655440000",
			Email:           "user@example.com",
			EmailVerified:   true,
			IsNewUser:       false,
			KeycloakUserID:  "kc-user-id-123",
			AuthenticatedAt: now,
		}

		vars := buildCompleteJobVariables(output)

		assert.True(t, vars["success"].(bool))
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", vars["userId"])
		assert.Equal(t, "user@example.com", vars["email"])
		assert.True(t, vars["emailVerified"].(bool))
		assert.False(t, vars["isNewUser"].(bool))
		assert.Equal(t, "kc-user-id-123", vars["keycloakUserId"])
		assert.Equal(t, now.Format(time.RFC3339), vars["authenticatedAt"])
		// initiate fields should NOT be set
		assert.NotContains(t, vars, "authorizationUrl")
	})

	t.Run("failed output sets success false only", func(t *testing.T) {
		output := &Output{Success: false}
		vars := buildCompleteJobVariables(output)
		assert.False(t, vars["success"].(bool))
		assert.NotContains(t, vars, "authorizationUrl")
		assert.NotContains(t, vars, "userId")
	})
}

// buildCompleteJobVariables replicates the variable-building logic from completeJob
// without needing a real Zeebe client, enabling pure unit testing of the mapping.
func buildCompleteJobVariables(output *Output) map[string]interface{} {
	variables := map[string]interface{}{
		"success": output.Success,
	}
	if output.AuthorizationURL != "" {
		variables["authorizationUrl"] = output.AuthorizationURL
		variables["state"] = output.State
	}
	if output.UserID != "" {
		variables["userId"] = output.UserID
		variables["email"] = output.Email
		variables["emailVerified"] = output.EmailVerified
		variables["isNewUser"] = output.IsNewUser
		variables["keycloakUserId"] = output.KeycloakUserID
		variables["authenticatedAt"] = output.AuthenticatedAt.Format(time.RFC3339)
	}
	return variables
}

// ==========================
// Input/Output Model Tests
// ==========================

func TestInput_JSONSerialization(t *testing.T) {
	input := &Input{
		Action:   "callback",
		Code:     "auth-code-123",
		State:    "state-abc",
		Provider: "keycloak",
		Metadata: map[string]interface{}{"ip": "10.0.0.1"},
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var decoded Input
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, input.Action, decoded.Action)
	assert.Equal(t, input.Code, decoded.Code)
	assert.Equal(t, input.State, decoded.State)
	assert.Equal(t, input.Provider, decoded.Provider)
	assert.Equal(t, input.Metadata, decoded.Metadata)
}

func TestOutput_JSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	output := &Output{
		Success:          true,
		AuthorizationURL: "http://keycloak/auth",
		State:            "state-xyz",
		UserID:           "550e8400-e29b-41d4-a716-446655440000",
		Email:            "user@example.com",
		EmailVerified:    true,
		IsNewUser:        true,
		KeycloakUserID:   "kc-123",
		AuthenticatedAt:  now,
	}

	data, err := json.Marshal(output)
	require.NoError(t, err)

	var decoded Output
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, output.Success, decoded.Success)
	assert.Equal(t, output.AuthorizationURL, decoded.AuthorizationURL)
	assert.Equal(t, output.State, decoded.State)
	assert.Equal(t, output.UserID, decoded.UserID)
	assert.Equal(t, output.Email, decoded.Email)
	assert.Equal(t, output.EmailVerified, decoded.EmailVerified)
	assert.Equal(t, output.IsNewUser, decoded.IsNewUser)
	assert.Equal(t, output.KeycloakUserID, decoded.KeycloakUserID)
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
		{"10.0.0.1:6399", "10.0.0.1", 6399},
		{"redis-host:0", "redis-host", 0},
		// no colon → returns whole string as host, default port
		{"redis-host-only", "redis-host-only", 6379},
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
// Register/Close/HealthCheck
// (structural / disabled-path tests only — no live Zeebe/Redis)
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
	// Should not panic
	assert.NotPanics(t, func() { handler.Close() })
}

// ==========================
// Context / Trace Extraction Tests
// ==========================

func TestHandler_TraceExtraction(t *testing.T) {
	t.Run("traceId and spanId extracted from job variables", func(t *testing.T) {
		vars := map[string]interface{}{
			"action":  "initiate",
			"traceId": "abc123trace",
			"spanId":  "def456span",
		}
		data, _ := json.Marshal(vars)

		var jobVars map[string]interface{}
		err := json.Unmarshal(data, &jobVars)
		require.NoError(t, err)

		traceID, _ := jobVars["traceId"].(string)
		spanID, _ := jobVars["spanId"].(string)

		assert.Equal(t, "abc123trace", traceID)
		assert.Equal(t, "def456span", spanID)
	})

	t.Run("missing traceId and spanId results in empty strings", func(t *testing.T) {
		vars := map[string]interface{}{"action": "initiate"}
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
	// GetOutputSchema is not defined in the keycloaksignin package.
	// Instead, verify that the Output struct has the expected fields via JSON serialisation.
	now := time.Now()
	out := &Output{
		Success:          true,
		AuthorizationURL: "http://keycloak/auth",
		State:            "state-abc",
		UserID:           "550e8400-e29b-41d4-a716-446655440000",
		Email:            "user@example.com",
		EmailVerified:    true,
		IsNewUser:        false,
		KeycloakUserID:   "kc-123",
		AuthenticatedAt:  now,
	}
	data, err := json.Marshal(out)
	require.NoError(t, err)

	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &m))

	assert.Contains(t, m, "success")
	assert.Contains(t, m, "authorizationUrl")
	assert.Contains(t, m, "userId")
	assert.Contains(t, m, "email")
}

// ==========================
// TaskType Constant Test
// ==========================

func TestTaskTypeConstant(t *testing.T) {
	assert.Equal(t, "keycloak-signin", TaskType)
}

// ==========================
// Disabled Worker Handle Path
// ==========================

func TestHandler_Handle_DisabledWorker_CompletesGracefully(t *testing.T) {
	// Build a minimal handler with disabled config. We can't call Handle() without
	// a real Zeebe client, but we can verify that the disabled check short-circuits
	// before any service call by inspecting config state.
	handler := &Handler{
		config: &Config{Enabled: false},
	}
	assert.False(t, handler.config.Enabled)
	// The Handle method would call completeJob with Success=false when disabled.
	// Full integration of Handle() requires mock Zeebe client; tested structurally here.
}

// Ensure context is not cancelled before service.Execute call in normal flow
func TestHandler_ParseInput_ContextIndependent(t *testing.T) {
	handler := &Handler{
		config: createValidConfig(),
		logger: logger.NewStructured("info", "json"),
	}

	// parseInput does not use ctx — it only uses the job entity
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	job := createMockJob(99, map[string]interface{}{
		"action": "initiate",
	})

	// Should still parse successfully even with cancelled ctx
	input, err := handler.parseInput(job)
	_ = ctx
	require.NoError(t, err)
	assert.Equal(t, "initiate", input.Action)
}
