package captchaverify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
		Type:                     "captcha.verify",
		ProcessInstanceKey:       key * 10,
		BpmnProcessId:            "test-process",
		ProcessDefinitionVersion: 1,
		ProcessDefinitionKey:     1,
		ElementId:                "Activity_CaptchaVerify",
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
		CaptchaID:    "cap_test123",
		CaptchaValue: "ABCD",
		ClientIP:     "192.168.1.1",
		UserAgent:    "Mozilla/5.0 Test Browser",
		SessionID:    "sess_12345",
		Metadata:     map[string]interface{}{"source": "web"},
	}
}

func createValidOutput() *Output {
	return &Output{
		Valid:   true,
		Message: "Captcha verified successfully",
		Reason:  "SUCCESS",
	}
}

func createValidConfig() *Config {
	return &Config{
		Enabled:        true,
		MaxJobsActive:  10,
		Timeout:        5 * time.Second,
		MaxAttempts:    3,
		VerifyClientIP: false,
		ExpiryMinutes:  5,
		RedisHost:      "localhost",
		RedisPort:      6379,
		RedisPassword:  "",
		RedisDB:        0,
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
			name: "invalid timeout",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 10,
					Timeout:       -1 * time.Second,
					MaxAttempts:   3,
					ExpiryMinutes: 5,
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
					Timeout:       5 * time.Second,
					MaxAttempts:   3,
					ExpiryMinutes: 5,
				},
			},
			wantErr: true,
			errMsg:  "max_jobs_active must be positive",
		},
		{
			name: "invalid max attempts",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 10,
					Timeout:       5 * time.Second,
					MaxAttempts:   0,
					ExpiryMinutes: 5,
				},
			},
			wantErr: true,
			errMsg:  "max_attempts must be positive",
		},
		{
			name: "invalid expiry minutes",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 10,
					Timeout:       5 * time.Second,
					MaxAttempts:   3,
					ExpiryMinutes: 0,
				},
			},
			wantErr: true,
			errMsg:  "expiry_minutes must be positive",
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
				"captchaId":    "cap_test123",
				"captchaValue": "ABCD",
				"clientIp":     "192.168.1.1",
				"userAgent":    "Mozilla/5.0 Test Browser",
				"sessionId":    "sess_12345",
				"metadata": map[string]interface{}{
					"source": "mobile-app",
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "cap_test123", input.CaptchaID)
				assert.Equal(t, "ABCD", input.CaptchaValue)
				assert.Equal(t, "192.168.1.1", input.ClientIP)
				assert.Equal(t, "Mozilla/5.0 Test Browser", input.UserAgent)
				assert.Equal(t, "sess_12345", input.SessionID)
				assert.NotNil(t, input.Metadata)
				assert.Equal(t, "mobile-app", input.Metadata["source"])
			},
		},
		{
			name: "valid input minimal fields",
			variables: map[string]interface{}{
				"captchaId":    "cap_test123",
				"captchaValue": "ABCD",
				"clientIp":     "192.168.1.1",
				"userAgent":    "Mozilla/5.0 Test Browser",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "cap_test123", input.CaptchaID)
				assert.Equal(t, "ABCD", input.CaptchaValue)
				assert.Empty(t, input.SessionID)
				assert.Nil(t, input.Metadata)
			},
		},
		{
			name: "missing captcha ID",
			variables: map[string]interface{}{
				"captchaValue": "ABCD",
				"clientIp":     "192.168.1.1",
				"userAgent":    "Mozilla/5.0 Test Browser",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "missing captcha value",
			variables: map[string]interface{}{
				"captchaId": "cap_test123",
				"clientIp":  "192.168.1.1",
				"userAgent": "Mozilla/5.0 Test Browser",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "missing client IP",
			variables: map[string]interface{}{
				"captchaId":    "cap_test123",
				"captchaValue": "ABCD",
				"userAgent":    "Mozilla/5.0 Test Browser",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "missing user agent",
			variables: map[string]interface{}{
				"captchaId":    "cap_test123",
				"captchaValue": "ABCD",
				"clientIp":     "192.168.1.1",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "captcha ID too short",
			variables: map[string]interface{}{
				"captchaId":    "cap",
				"captchaValue": "ABCD",
				"clientIp":     "192.168.1.1",
				"userAgent":    "Mozilla/5.0 Test Browser",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "captcha value too short",
			variables: map[string]interface{}{
				"captchaId":    "cap_test123",
				"captchaValue": "ABC",
				"clientIp":     "192.168.1.1",
				"userAgent":    "Mozilla/5.0 Test Browser",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "client IP too short",
			variables: map[string]interface{}{
				"captchaId":    "cap_test123",
				"captchaValue": "ABCD",
				"clientIp":     "1.1.1",
				"userAgent":    "Mozilla/5.0 Test Browser",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "user agent too short",
			variables: map[string]interface{}{
				"captchaId":    "cap_test123",
				"captchaValue": "ABCD",
				"clientIp":     "192.168.1.1",
				"userAgent":    "short",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
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
// Mock Service Tests
// ==========================

func TestMockService_Execute(t *testing.T) {
	// Test case: successful verification
	t.Run("successful verification", func(t *testing.T) {
		mockService := new(MockService) // Create new mock for each test
		validOutput := createValidOutput()
		mockService.On("Execute", mock.Anything, mock.AnythingOfType("*captchaverify.Input")).
			Return(validOutput, nil)

		output, err := mockService.Execute(context.Background(), createValidInput())
		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.True(t, output.Valid)
		assert.Equal(t, "SUCCESS", output.Reason)

		mockService.AssertExpectations(t)
	})

	// Test case: failed verification
	t.Run("failed verification", func(t *testing.T) {
		mockService := new(MockService) // Create new mock for each test
		failedOutput := &Output{
			Valid:   false,
			Message: "Incorrect captcha value",
			Reason:  "INCORRECT_VALUE",
		}
		mockService.On("Execute", mock.Anything, mock.AnythingOfType("*captchaverify.Input")).
			Return(failedOutput, nil)

		output, err := mockService.Execute(context.Background(), createValidInput())
		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.False(t, output.Valid)
		assert.Equal(t, "INCORRECT_VALUE", output.Reason)

		mockService.AssertExpectations(t)
	})

	// Test case: service error
	t.Run("service error", func(t *testing.T) {
		mockService := new(MockService) // Create new mock for each test
		mockService.On("Execute", mock.Anything, mock.AnythingOfType("*captchaverify.Input")).
			Return(nil, &errors.StandardError{
				Code:      "SERVICE_ERROR",
				Message:   "Service unavailable",
				Retryable: true,
			})

		output, err := mockService.Execute(context.Background(), createValidInput())
		assert.Error(t, err)
		assert.Nil(t, output)
		stdErr, ok := err.(*errors.StandardError)
		require.True(t, ok)
		assert.Equal(t, "SERVICE_ERROR", string(stdErr.Code))

		mockService.AssertExpectations(t)
	})
}

// ==========================
// Service Tests (Unit tests without Redis)
// ==========================

func TestService_Execute_Unit(t *testing.T) {
	config := createValidConfig()

	tests := []struct {
		name     string
		setup    func(*testing.T, *Service)
		input    *Input
		validate func(*testing.T, *Output, error)
	}{
		{
			name: "redis not configured",
			setup: func(t *testing.T, s *Service) {
				// Create service without Redis
				s.redisClient = nil
			},
			input: createValidInput(),
			validate: func(t *testing.T, output *Output, err error) {
				assert.Error(t, err)
				assert.Nil(t, output)
				stdErr, ok := err.(*errors.StandardError)
				require.True(t, ok)
				assert.Equal(t, "REDIS_NOT_CONFIGURED", string(stdErr.Code))
			},
		},
		{
			name:  "invalid captcha ID format",
			setup: nil,
			input: &Input{
				CaptchaID:    "invalid_id",
				CaptchaValue: "ABCD",
				ClientIP:     "192.168.1.1",
				UserAgent:    "Mozilla/5.0 Test",
			},
			validate: func(t *testing.T, output *Output, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, output)
				assert.False(t, output.Valid)
				assert.Equal(t, "INVALID_FORMAT", output.Reason)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testService := NewService(ServiceDependencies{
				Logger: logger.NewStructured("info", "json"),
			}, config)

			if tt.setup != nil {
				tt.setup(t, testService)
			}

			output, err := testService.Execute(context.Background(), tt.input)
			tt.validate(t, output, err)
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
				MaxJobsActive: 10,
				Timeout:       0,
				MaxAttempts:   3,
				ExpiryMinutes: 5,
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "negative timeout",
			config: &Config{
				MaxJobsActive: 10,
				Timeout:       -5 * time.Second,
				MaxAttempts:   3,
				ExpiryMinutes: 5,
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "zero max jobs active",
			config: &Config{
				MaxJobsActive: 0,
				Timeout:       5 * time.Second,
				MaxAttempts:   3,
				ExpiryMinutes: 5,
			},
			wantErr: true,
			errMsg:  "max_jobs_active must be positive",
		},
		{
			name: "zero max attempts",
			config: &Config{
				MaxJobsActive: 10,
				Timeout:       5 * time.Second,
				MaxAttempts:   0,
				ExpiryMinutes: 5,
			},
			wantErr: true,
			errMsg:  "max_attempts must be positive",
		},
		{
			name: "zero expiry minutes",
			config: &Config{
				MaxJobsActive: 10,
				Timeout:       5 * time.Second,
				MaxAttempts:   3,
				ExpiryMinutes: 0,
			},
			wantErr: true,
			errMsg:  "expiry_minutes must be positive",
		},
		{
			name: "invalid redis port",
			config: &Config{
				MaxJobsActive: 10,
				Timeout:       5 * time.Second,
				MaxAttempts:   3,
				ExpiryMinutes: 5,
				RedisHost:     "localhost",
				RedisPort:     0, // Invalid port
			},
			wantErr: true,
			errMsg:  "redis_port must be between 1 and 65535",
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
	assert.Equal(t, 10, config.MaxJobsActive)
	assert.Equal(t, 5*time.Second, config.Timeout)
	assert.Equal(t, 3, config.MaxAttempts)
	assert.False(t, config.VerifyClientIP)
	assert.Equal(t, 5, config.ExpiryMinutes)
	assert.Equal(t, "localhost", config.RedisHost)
	assert.Equal(t, 6379, config.RedisPort)
	assert.Equal(t, "", config.RedisPassword)
	assert.Equal(t, 0, config.RedisDB)
}

// ==========================
// Handler Methods Tests
// ==========================

func TestHandler_GetTaskType(t *testing.T) {
	handler := &Handler{}
	assert.Equal(t, "captcha.verify", handler.GetTaskType())
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

// ==========================
// Task Type Naming Convention Tests
// ==========================

func TestTaskTypeNamingConvention(t *testing.T) {
	taskType := TaskType

	assert.Equal(t, "captcha.verify", taskType)

	parts := strings.Split(taskType, ".")
	assert.Len(t, parts, 2, "Task type must have exactly 2 parts")
	assert.Equal(t, "captcha", parts[0], "Domain should be 'captcha'")
	assert.Equal(t, "verify", parts[1], "Action should be 'verify'")

	assert.Equal(t, strings.ToLower(taskType), taskType, "Task type should be lowercase")
}

// ==========================
// Validation Schema Tests
// ==========================

func TestGetInputSchema(t *testing.T) {
	schema := GetInputSchema()

	assert.Equal(t, "object", schema.Type)
	assert.Contains(t, schema.Required, "captchaId")
	assert.Contains(t, schema.Required, "captchaValue")
	assert.Contains(t, schema.Required, "clientIp")
	assert.Contains(t, schema.Required, "userAgent")
	assert.NotNil(t, schema.Properties)

	captchaIdProp, exists := schema.Properties["captchaId"]
	assert.True(t, exists)
	assert.Equal(t, "string", captchaIdProp.Type)

	captchaValueProp, exists := schema.Properties["captchaValue"]
	assert.True(t, exists)
	assert.Equal(t, "string", captchaValueProp.Type)

	assert.False(t, schema.AdditionalProperties)
}

func TestGetOutputSchema(t *testing.T) {
	schema := GetOutputSchema()

	assert.Equal(t, "object", schema.Type)
	assert.NotNil(t, schema.Properties)

	expectedFields := []string{"valid", "message", "reason", "attemptsRemaining"}

	for _, field := range expectedFields {
		prop, exists := schema.Properties[field]
		assert.True(t, exists, "Field %s should exist", field)
		assert.NotEmpty(t, prop.Type, "Field %s should have a type", field)
	}

	assert.Equal(t, "boolean", schema.Properties["valid"].Type)
	assert.Equal(t, "string", schema.Properties["message"].Type)
}

// ==========================
// Helper Function Tests
// ==========================

func TestCreateValidOutput(t *testing.T) {
	output := createValidOutput()

	assert.True(t, output.Valid)
	assert.Equal(t, "Captcha verified successfully", output.Message)
	assert.Equal(t, "SUCCESS", output.Reason)
}
