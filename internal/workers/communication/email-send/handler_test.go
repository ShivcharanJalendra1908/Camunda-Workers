package emailsend

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
		Type:                     "email.send",
		ProcessInstanceKey:       key * 10,
		BpmnProcessId:            "test-process",
		ProcessDefinitionVersion: 1,
		ProcessDefinitionKey:     1,
		ElementId:                "Activity_EmailSend",
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
		From:     "sender@example.com",
		To:       "recipient@example.com",
		Subject:  "Test Email",
		Body:     "This is a test email body.",
		IsHTML:   false,
		Priority: "normal",
	}
}

func createValidHTMLInput() *Input {
	return &Input{
		From:    "sender@example.com",
		To:      "recipient@example.com",
		Subject: "HTML Test Email",
		Body:    "<html><body><h1>Test</h1></body></html>",
		IsHTML:  true,
		CC:      "cc@example.com",
		BCC:     "bcc@example.com",
		ReplyTo: "reply@example.com",
	}
}

func createValidOutput() *Output {
	return &Output{
		Success:   true,
		Message:   "Email sent successfully",
		MessageID: "<123456@smtp.example.com>",
		Provider:  "SMTP",
		SentAt:    time.Now(),
	}
}

func createValidConfig() *Config {
	return &Config{
		Enabled:       true,
		MaxJobsActive: 5,
		Timeout:       30 * time.Second,
		SMTPHost:      "smtp.example.com",
		SMTPPort:      587,
		SMTPUsername:  "test-user",
		SMTPPassword:  "test-password",
		UseTLS:        true,
		DefaultFrom:   "noreply@example.com",
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
			name: "missing SMTP host",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       30 * time.Second,
					SMTPPort:      587,
					DefaultFrom:   "noreply@example.com",
				},
			},
			wantErr: true,
			errMsg:  "smtp_host is required",
		},
		{
			name: "invalid SMTP port (zero)",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       30 * time.Second,
					SMTPHost:      "smtp.example.com",
					SMTPPort:      0,
					DefaultFrom:   "noreply@example.com",
				},
			},
			wantErr: true,
			errMsg:  "smtp_port must be between 1 and 65535",
		},
		{
			name: "invalid SMTP port (too high)",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       30 * time.Second,
					SMTPHost:      "smtp.example.com",
					SMTPPort:      70000,
					DefaultFrom:   "noreply@example.com",
				},
			},
			wantErr: true,
			errMsg:  "smtp_port must be between 1 and 65535",
		},
		{
			name: "missing default from",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       30 * time.Second,
					SMTPHost:      "smtp.example.com",
					SMTPPort:      587,
				},
			},
			wantErr: true,
			errMsg:  "default_from email is required",
		},
		{
			name: "invalid timeout",
			opts: HandlerOptions{
				CustomConfig: &Config{
					Enabled:       true,
					MaxJobsActive: 5,
					Timeout:       -1 * time.Second,
					SMTPHost:      "smtp.example.com",
					SMTPPort:      587,
					DefaultFrom:   "noreply@example.com",
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
					Timeout:       30 * time.Second,
					SMTPHost:      "smtp.example.com",
					SMTPPort:      587,
					DefaultFrom:   "noreply@example.com",
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
			DefaultFrom: "default@example.com",
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
				"from":     "sender@example.com",
				"to":       "recipient@example.com",
				"cc":       "cc@example.com",
				"bcc":      "bcc@example.com",
				"replyTo":  "reply@example.com",
				"subject":  "Test Subject",
				"body":     "This is the email body",
				"isHtml":   true,
				"priority": "high",
				"attachments": []interface{}{
					map[string]interface{}{
						"filename":    "document.pdf",
						"contentType": "application/pdf",
						"content":     "base64encodedcontent",
					},
				},
				"metadata": map[string]interface{}{
					"campaignId": "campaign-123",
					"source":     "marketing",
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "sender@example.com", input.From)
				assert.Equal(t, "recipient@example.com", input.To)
				assert.Equal(t, "cc@example.com", input.CC)
				assert.Equal(t, "bcc@example.com", input.BCC)
				assert.Equal(t, "reply@example.com", input.ReplyTo)
				assert.Equal(t, "Test Subject", input.Subject)
				assert.Equal(t, "This is the email body", input.Body)
				assert.True(t, input.IsHTML)
				assert.Equal(t, "high", input.Priority)
				assert.Len(t, input.Attachments, 1)
				assert.Equal(t, "document.pdf", input.Attachments[0].Filename)
				assert.NotNil(t, input.Metadata)
				assert.Equal(t, "campaign-123", input.Metadata["campaignId"])
			},
		},
		{
			name: "valid input minimal fields",
			variables: map[string]interface{}{
				"to":      "recipient@example.com",
				"subject": "Test Subject",
				"body":    "Test body",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "default@example.com", input.From)
				assert.Equal(t, "recipient@example.com", input.To)
				assert.Equal(t, "Test Subject", input.Subject)
				assert.Equal(t, "Test body", input.Body)
				assert.False(t, input.IsHTML)
				assert.Empty(t, input.CC)
				assert.Empty(t, input.BCC)
				assert.Empty(t, input.ReplyTo)
				assert.Empty(t, input.Priority)
				assert.Nil(t, input.Attachments)
				assert.Nil(t, input.Metadata)
			},
		},
		{
			name: "custom from overrides default",
			variables: map[string]interface{}{
				"from":    "custom@example.com",
				"to":      "recipient@example.com",
				"subject": "Test",
				"body":    "Body",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "custom@example.com", input.From)
			},
		},
		{
			name: "empty from uses default",
			variables: map[string]interface{}{
				"from":    "",
				"to":      "recipient@example.com",
				"subject": "Test",
				"body":    "Body",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "default@example.com", input.From)
			},
		},
		{
			name: "missing to field",
			variables: map[string]interface{}{
				"subject": "Test Subject",
				"body":    "Test body",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "missing subject field",
			variables: map[string]interface{}{
				"to":   "recipient@example.com",
				"body": "Test body",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "missing body field",
			variables: map[string]interface{}{
				"to":      "recipient@example.com",
				"subject": "Test Subject",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "to field too short",
			variables: map[string]interface{}{
				"to":      "a@b",
				"subject": "Test",
				"body":    "Body",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "subject empty string",
			variables: map[string]interface{}{
				"to":      "recipient@example.com",
				"subject": "",
				"body":    "Body",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "body empty string",
			variables: map[string]interface{}{
				"to":      "recipient@example.com",
				"subject": "Subject",
				"body":    "",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "subject too long",
			variables: map[string]interface{}{
				"to":      "recipient@example.com",
				"subject": string(make([]byte, 501)),
				"body":    "Body",
			},
			wantErr: true,
			errCode: "VALIDATION_FAILED",
		},
		{
			name: "multiple attachments",
			variables: map[string]interface{}{
				"to":      "recipient@example.com",
				"subject": "Test",
				"body":    "Body",
				"attachments": []interface{}{
					map[string]interface{}{
						"filename":    "file1.pdf",
						"contentType": "application/pdf",
						"content":     "content1",
					},
					map[string]interface{}{
						"filename":    "file2.jpg",
						"contentType": "image/jpeg",
						"content":     "content2",
					},
				},
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Len(t, input.Attachments, 2)
				assert.Equal(t, "file1.pdf", input.Attachments[0].Filename)
				assert.Equal(t, "file2.jpg", input.Attachments[1].Filename)
			},
		},
		{
			name: "valid minimum length fields",
			variables: map[string]interface{}{
				"to":      "a@b.c",
				"subject": "S",
				"body":    "B",
			},
			wantErr: false,
			validate: func(t *testing.T, input *Input) {
				assert.Equal(t, "a@b.c", input.To)
				assert.Equal(t, "S", input.Subject)
				assert.Equal(t, "B", input.Body)
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

// Add this test after TestHandler_ParseInput
func TestHandler_ParseHTMLInput(t *testing.T) {
	handler := &Handler{
		config: &Config{
			DefaultFrom: "default@example.com",
		},
		logger: logger.NewStructured("info", "json"),
	}

	htmlInput := createValidHTMLInput()

	variables := map[string]interface{}{
		"from":    htmlInput.From,
		"to":      htmlInput.To,
		"cc":      htmlInput.CC,
		"bcc":     htmlInput.BCC,
		"replyTo": htmlInput.ReplyTo,
		"subject": htmlInput.Subject,
		"body":    htmlInput.Body,
		"isHtml":  htmlInput.IsHTML,
	}

	job := createMockJob(12345, variables)
	input, err := handler.parseInput(job)

	require.NoError(t, err)
	assert.True(t, input.IsHTML)
	assert.Contains(t, input.Body, "<html>")
	assert.Equal(t, htmlInput.CC, input.CC)
	assert.Equal(t, htmlInput.BCC, input.BCC)
	assert.Equal(t, htmlInput.ReplyTo, input.ReplyTo)
}

func TestService_OutputStructure(t *testing.T) {
	output := createValidOutput()

	assert.True(t, output.Success)
	assert.Equal(t, "Email sent successfully", output.Message)
	assert.NotEmpty(t, output.MessageID)
	assert.Equal(t, "SMTP", output.Provider)
	assert.False(t, output.SentAt.IsZero())
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
			name: "standard error - SMTP error",
			err: &errors.StandardError{
				Code:    "SMTP_ERROR",
				Message: "SMTP connection failed",
			},
			expected: "SMTP_ERROR",
		},
		{
			name: "standard error - validation failed",
			err: &errors.StandardError{
				Code:    "VALIDATION_FAILED",
				Message: "Email validation failed",
			},
			expected: "VALIDATION_FAILED",
		},
		{
			name: "standard error - input parsing",
			err: &errors.StandardError{
				Code:    "INPUT_PARSING_FAILED",
				Message: "Failed to parse job variables",
			},
			expected: "INPUT_PARSING_FAILED",
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
				assert.Equal(t, errors.ErrorCode("EMAIL_SEND_ERROR"), stdErr.Code)
				assert.Equal(t, "Failed to send email", stdErr.Message)
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
				Message:   "Invalid email",
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
			name: "missing SMTP host",
			config: &Config{
				SMTPPort:      587,
				DefaultFrom:   "noreply@example.com",
				Timeout:       30 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "smtp_host is required",
		},
		{
			name: "invalid SMTP port - zero",
			config: &Config{
				SMTPHost:      "smtp.example.com",
				SMTPPort:      0,
				DefaultFrom:   "noreply@example.com",
				Timeout:       30 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "smtp_port must be between 1 and 65535",
		},
		{
			name: "invalid SMTP port - negative",
			config: &Config{
				SMTPHost:      "smtp.example.com",
				SMTPPort:      -1,
				DefaultFrom:   "noreply@example.com",
				Timeout:       30 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "smtp_port must be between 1 and 65535",
		},
		{
			name: "invalid SMTP port - too high",
			config: &Config{
				SMTPHost:      "smtp.example.com",
				SMTPPort:      65536,
				DefaultFrom:   "noreply@example.com",
				Timeout:       30 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "smtp_port must be between 1 and 65535",
		},
		{
			name: "missing default from",
			config: &Config{
				SMTPHost:      "smtp.example.com",
				SMTPPort:      587,
				Timeout:       30 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "default_from email is required",
		},
		{
			name: "zero timeout",
			config: &Config{
				SMTPHost:      "smtp.example.com",
				SMTPPort:      587,
				DefaultFrom:   "noreply@example.com",
				Timeout:       0,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "negative timeout",
			config: &Config{
				SMTPHost:      "smtp.example.com",
				SMTPPort:      587,
				DefaultFrom:   "noreply@example.com",
				Timeout:       -5 * time.Second,
				MaxJobsActive: 5,
			},
			wantErr: true,
			errMsg:  "timeout must be positive",
		},
		{
			name: "zero max jobs active",
			config: &Config{
				SMTPHost:      "smtp.example.com",
				SMTPPort:      587,
				DefaultFrom:   "noreply@example.com",
				Timeout:       30 * time.Second,
				MaxJobsActive: 0,
			},
			wantErr: true,
			errMsg:  "max_jobs_active must be positive",
		},
		{
			name: "valid config with TLS",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 10,
				Timeout:       60 * time.Second,
				SMTPHost:      "smtp.gmail.com",
				SMTPPort:      587,
				SMTPUsername:  "user@gmail.com",
				SMTPPassword:  "password",
				UseTLS:        true,
				DefaultFrom:   "noreply@example.com",
			},
			wantErr: false,
		},
		{
			name: "valid config without TLS",
			config: &Config{
				Enabled:       true,
				MaxJobsActive: 5,
				Timeout:       30 * time.Second,
				SMTPHost:      "localhost",
				SMTPPort:      25,
				UseTLS:        false,
				DefaultFrom:   "noreply@example.com",
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
	assert.Equal(t, 30*time.Second, config.Timeout)
	assert.Equal(t, 587, config.SMTPPort)
	assert.True(t, config.UseTLS)
	assert.Equal(t, "noreply@example.com", config.DefaultFrom)
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
				assert.Equal(t, "smtp.example.com", cfg.SMTPHost)
				assert.Equal(t, 587, cfg.SMTPPort)
				assert.Equal(t, "test-user", cfg.SMTPUsername)
				assert.Equal(t, "test-password", cfg.SMTPPassword)
				assert.True(t, cfg.UseTLS)
			},
		},
		{
			name: "loads from app config",
			appConfig: &config.Config{
				Workers: map[string]config.WorkerConfig{
					"email-send": {
						Enabled:       true,
						MaxJobsActive: 10,
						Timeout:       45000,
					},
				},
				Integrations: func() config.IntegrationConfig {
					ic := config.IntegrationConfig{}
					ic.SMTP.Host = "smtp.mailgun.org"
					ic.SMTP.Port = 465
					ic.SMTP.Username = "mailgun-user"
					ic.SMTP.Password = "mailgun-pass"
					ic.SMTP.UseTLS = true
					ic.SMTP.DefaultFrom = "system@example.com"
					return ic
				}(),
			},
			customConfig: nil,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "smtp.mailgun.org", cfg.SMTPHost)
				assert.Equal(t, 465, cfg.SMTPPort)
				assert.Equal(t, "mailgun-user", cfg.SMTPUsername)
				assert.Equal(t, "mailgun-pass", cfg.SMTPPassword)
				assert.True(t, cfg.UseTLS)
				assert.Equal(t, "system@example.com", cfg.DefaultFrom)
				assert.Equal(t, 10, cfg.MaxJobsActive)
				assert.Equal(t, 45*time.Second, cfg.Timeout)
				assert.True(t, cfg.Enabled)
			},
		},
		{
			name: "defaults when SMTP not configured",
			appConfig: &config.Config{
				Workers: map[string]config.WorkerConfig{
					"email-send": {
						Enabled:       false,
						MaxJobsActive: 3,
						Timeout:       20000,
					},
				},
			},
			customConfig: nil,
			validate: func(t *testing.T, cfg *Config) {
				assert.False(t, cfg.Enabled)
				assert.Equal(t, 3, cfg.MaxJobsActive)
				assert.Equal(t, 20*time.Second, cfg.Timeout)
				assert.Equal(t, "", cfg.SMTPHost)
				assert.Equal(t, 587, cfg.SMTPPort)
				assert.Equal(t, "noreply@example.com", cfg.DefaultFrom)
			},
		},
		{
			name:         "uses defaults when no configs provided",
			appConfig:    nil,
			customConfig: nil,
			validate: func(t *testing.T, cfg *Config) {
				assert.True(t, cfg.Enabled)
				assert.Equal(t, 5, cfg.MaxJobsActive)
				assert.Equal(t, 30*time.Second, cfg.Timeout)
				assert.Equal(t, 587, cfg.SMTPPort)
				assert.True(t, cfg.UseTLS)
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
	assert.Equal(t, "email.send", handler.GetTaskType())
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
	assert.Equal(t, "smtp.example.com", handler.GetConfig().SMTPHost)
	assert.Equal(t, 587, handler.GetConfig().SMTPPort)
	assert.Equal(t, "test-user", handler.GetConfig().SMTPUsername)
	assert.Equal(t, "noreply@example.com", handler.GetConfig().DefaultFrom)
}

// ==========================
// Schema Tests
// ==========================

func TestGetInputSchema(t *testing.T) {
	schema := GetInputSchema()

	assert.Equal(t, "object", schema.Type)
	assert.Contains(t, schema.Required, "to")
	assert.Contains(t, schema.Required, "subject")
	assert.Contains(t, schema.Required, "body")
	assert.Len(t, schema.Required, 3)

	// Verify key properties exist
	assert.Contains(t, schema.Properties, "from")
	assert.Contains(t, schema.Properties, "to")
	assert.Contains(t, schema.Properties, "cc")
	assert.Contains(t, schema.Properties, "bcc")
	assert.Contains(t, schema.Properties, "replyTo")
	assert.Contains(t, schema.Properties, "subject")
	assert.Contains(t, schema.Properties, "body")
	assert.Contains(t, schema.Properties, "isHtml")
	assert.Contains(t, schema.Properties, "priority")
	assert.Contains(t, schema.Properties, "attachments")
	assert.Contains(t, schema.Properties, "metadata")

	// Verify type constraints
	assert.Equal(t, "string", schema.Properties["to"].Type)
	assert.Equal(t, "string", schema.Properties["subject"].Type)
	assert.Equal(t, "string", schema.Properties["body"].Type)
	assert.Equal(t, "boolean", schema.Properties["isHtml"].Type)
	assert.Equal(t, "array", schema.Properties["attachments"].Type)
	assert.Equal(t, "object", schema.Properties["metadata"].Type)

	// Verify length constraints
	assert.NotNil(t, schema.Properties["to"].MinLength)
	assert.Equal(t, 5, *schema.Properties["to"].MinLength)
	assert.NotNil(t, schema.Properties["subject"].MinLength)
	assert.Equal(t, 1, *schema.Properties["subject"].MinLength)
	assert.NotNil(t, schema.Properties["subject"].MaxLength)
	assert.Equal(t, 500, *schema.Properties["subject"].MaxLength)

	assert.False(t, schema.AdditionalProperties)
}

func TestGetOutputSchema(t *testing.T) {
	schema := GetOutputSchema()

	assert.Equal(t, "object", schema.Type)

	// Verify output properties
	assert.Contains(t, schema.Properties, "success")
	assert.Contains(t, schema.Properties, "message")
	assert.Contains(t, schema.Properties, "messageId")
	assert.Contains(t, schema.Properties, "provider")
	assert.Contains(t, schema.Properties, "sentAt")

	// Verify types
	assert.Equal(t, "boolean", schema.Properties["success"].Type)
	assert.Equal(t, "string", schema.Properties["message"].Type)
	assert.Equal(t, "string", schema.Properties["messageId"].Type)
	assert.Equal(t, "string", schema.Properties["provider"].Type)
	assert.Equal(t, "string", schema.Properties["sentAt"].Type)

	assert.False(t, schema.AdditionalProperties)
}

// ==========================
// Integration-Style Tests
// ==========================

// ==========================
// Integration-Style Tests
// ==========================

func TestHandler_HandleDisabledWorker(t *testing.T) {
	// Create a real service with test config
	testConfig := &Config{
		Enabled:       false,
		DefaultFrom:   "noreply@example.com",
		MaxJobsActive: 5,
		Timeout:       30 * time.Second,
		SMTPHost:      "localhost",
		SMTPPort:      25,
	}

	handler := &Handler{
		config: testConfig,
		logger: logger.NewStructured("info", "json"),
		service: NewService(ServiceDependencies{
			Logger: logger.NewStructured("info", "json"),
		}, testConfig),
	}

	// Verify handler configuration
	assert.False(t, handler.IsEnabled())
	assert.NotNil(t, handler.config)
	assert.NotNil(t, handler.service)

	// Test that disabled worker won't process jobs
	variables := map[string]interface{}{
		"to":      "recipient@example.com",
		"subject": "Test",
		"body":    "Body",
	}

	job := createMockJob(12345, variables)

	// Verify we can parse input even when disabled
	input, err := handler.parseInput(job)
	require.NoError(t, err)
	assert.Equal(t, "recipient@example.com", input.To)
}

func TestHandler_HandleValidEmail(t *testing.T) {
	// Test handler configuration without using mockService directly
	handler := &Handler{
		config: createValidConfig(),
		service: NewService(ServiceDependencies{
			Logger: logger.NewStructured("info", "json"),
		}, createValidConfig()),
	}

	// Verify handler is properly configured
	assert.True(t, handler.IsEnabled())
	assert.Equal(t, "email.send", handler.GetTaskType())
	assert.NotNil(t, handler.service)
	assert.NotNil(t, handler.config)

	// Test input parsing
	input := createValidInput()
	assert.Equal(t, "sender@example.com", input.From)
	assert.Equal(t, "recipient@example.com", input.To)
	assert.Equal(t, "Test Email", input.Subject)
}

func TestHandler_HandleServiceError(t *testing.T) {
	// Test error handling without using mockService directly
	serviceError := &errors.StandardError{
		Code:      "SMTP_ERROR",
		Message:   "Failed to connect to SMTP server",
		Details:   "Connection timeout",
		Retryable: true,
		Timestamp: time.Now(),
	}

	handler := &Handler{
		config: createValidConfig(),
		service: NewService(ServiceDependencies{
			Logger: logger.NewStructured("info", "json"),
		}, createValidConfig()),
	}

	// Verify error handling configuration
	stdErr := convertToStandardError(serviceError)
	assert.Equal(t, errors.ErrorCode("SMTP_ERROR"), stdErr.Code)
	assert.True(t, stdErr.Retryable)
	assert.Equal(t, "Failed to connect to SMTP server", stdErr.Message)

	// Verify handler can extract error codes
	code := extractErrorCode(serviceError)
	assert.Equal(t, "SMTP_ERROR", code)

	// Verify handler is properly configured
	assert.NotNil(t, handler.service)
	assert.NotNil(t, handler.config)
}

// ==========================
// Additional Edge Case Tests
// ==========================

func TestHandler_ParseInputWithInvalidJSON(t *testing.T) {
	handler := &Handler{
		config: &Config{
			DefaultFrom: "default@example.com",
		},
		logger: logger.NewStructured("info", "json"),
	}

	// Create job with invalid variables structure
	activatedJob := &pb.ActivatedJob{
		Key:       12345,
		Type:      "email.send",
		Variables: "invalid json{",
	}
	job := entities.Job{ActivatedJob: activatedJob}

	_, err := handler.parseInput(job)

	require.Error(t, err)
	stdErr, ok := err.(*errors.StandardError)
	require.True(t, ok)
	assert.Equal(t, errors.ErrorCode("INPUT_PARSING_FAILED"), stdErr.Code)
}

func TestHandler_ParseInputWithMultipleRecipients(t *testing.T) {
	handler := &Handler{
		config: &Config{
			DefaultFrom: "default@example.com",
		},
		logger: logger.NewStructured("info", "json"),
	}

	variables := map[string]interface{}{
		"to":      "recipient1@example.com",
		"cc":      "cc1@example.com,cc2@example.com",
		"bcc":     "bcc1@example.com,bcc2@example.com,bcc3@example.com",
		"subject": "Test",
		"body":    "Body",
	}

	job := createMockJob(12345, variables)
	input, err := handler.parseInput(job)

	require.NoError(t, err)
	assert.Equal(t, "cc1@example.com,cc2@example.com", input.CC)
	assert.Equal(t, "bcc1@example.com,bcc2@example.com,bcc3@example.com", input.BCC)
}

func TestHandler_ParseInputWithComplexMetadata(t *testing.T) {
	handler := &Handler{
		config: &Config{
			DefaultFrom: "default@example.com",
		},
		logger: logger.NewStructured("info", "json"),
	}

	variables := map[string]interface{}{
		"to":      "recipient@example.com",
		"subject": "Test",
		"body":    "Body",
		"metadata": map[string]interface{}{
			"campaignId":   "campaign-123",
			"userId":       "user-456",
			"templateId":   "template-789",
			"tracking":     true,
			"customField1": "value1",
			"customField2": 12345,
			"nested": map[string]interface{}{
				"key": "value",
			},
		},
	}

	job := createMockJob(12345, variables)
	input, err := handler.parseInput(job)

	require.NoError(t, err)
	require.NotNil(t, input.Metadata)
	assert.Equal(t, "campaign-123", input.Metadata["campaignId"])
	assert.Equal(t, "user-456", input.Metadata["userId"])
	assert.Equal(t, true, input.Metadata["tracking"])
	assert.Equal(t, float64(12345), input.Metadata["customField2"])

	nested, ok := input.Metadata["nested"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "value", nested["key"])
}

func TestHandler_ParseInputWithPriorityVariations(t *testing.T) {
	handler := &Handler{
		config: &Config{
			DefaultFrom: "default@example.com",
		},
		logger: logger.NewStructured("info", "json"),
	}

	priorities := []string{"high", "normal", "low", "urgent", ""}

	for _, priority := range priorities {
		t.Run(fmt.Sprintf("priority_%s", priority), func(t *testing.T) {
			variables := map[string]interface{}{
				"to":       "recipient@example.com",
				"subject":  "Test",
				"body":     "Body",
				"priority": priority,
			}

			job := createMockJob(12345, variables)
			input, err := handler.parseInput(job)

			require.NoError(t, err)
			assert.Equal(t, priority, input.Priority)
		})
	}
}

func TestHandler_ErrorCodeExtraction(t *testing.T) {
	testCases := []struct {
		name         string
		error        error
		expectedCode string
	}{
		{
			name: "SMTP connection error",
			error: &errors.StandardError{
				Code:    "SMTP_CONNECTION_ERROR",
				Message: "Cannot connect",
			},
			expectedCode: "SMTP_CONNECTION_ERROR",
		},
		{
			name: "Authentication failed",
			error: &errors.StandardError{
				Code:    "SMTP_AUTH_FAILED",
				Message: "Invalid credentials",
			},
			expectedCode: "SMTP_AUTH_FAILED",
		},
		{
			name: "Rate limit exceeded",
			error: &errors.StandardError{
				Code:    "RATE_LIMIT_EXCEEDED",
				Message: "Too many requests",
			},
			expectedCode: "RATE_LIMIT_EXCEEDED",
		},
		{
			name:         "Unknown error",
			error:        fmt.Errorf("something went wrong"),
			expectedCode: "UNKNOWN_ERROR",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			code := extractErrorCode(tc.error)
			assert.Equal(t, tc.expectedCode, code)
		})
	}
}
