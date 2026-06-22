// internal/workers/application/send-notification/handler_test.go
package sendnotification

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"camunda-workers/internal/common/idempotency"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/stretchr/testify/assert"
)

// ==========================
// Mock Implementations
// ==========================

type MockSESService struct {
	SendEmailFunc func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error)
}

func (m *MockSESService) SendEmail(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
	return m.SendEmailFunc(ctx, params, optFns...)
}

type MockSNSService struct {
	PublishFunc func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error)
}

func (m *MockSNSService) Publish(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
	return m.PublishFunc(ctx, params, optFns...)
}

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{
		EmailEnabled:     true,
		SMSEnabled:       true,
		FromEmail:        "noreply@franchise.com",
		AWSRegion:        "us-east-1",
		TemplateRegistry: "test-registry",
		Timeout:          30 * time.Second,
	}
}

func createTestHandler(config *Config, db *sql.DB, log logger.Logger, ses SESService, sns SNSService) *Handler {
	return &Handler{
		config:             config,
		db:                 db,
		logger:             log,
		sesClient:          ses,
		snsClient:          sns,
		templateMap:        loadTestTemplates(),
		validator:          validation.NewValidator(),
		sanitizer:          validation.NewSanitizer(),
		keyGenerator:       idempotency.NewKeyGenerator(),
		idempotencyChecker: idempotency.NewDBChecker(db),
	}
}

// BUG FIX: validateInput uses ozzo.Required + UUID v4 regex on RecipientID.
// "recipient-001" is NOT a valid UUID v4 → all tests fail at validation.
// All recipient/application IDs must be valid UUID v4.

const (
	testRecipientUUID001  = "d4e5f6a7-b8c9-4012-bcde-000000000001"
	testRecipientUUID002  = "d4e5f6a7-b8c9-4012-bcde-000000000002"
	testFranchisorUUID001 = "d4e5f6a7-b8c9-4012-bcde-000000000003"
	testAppUUID001        = "d4e5f6a7-b8c9-4012-bcde-000000000004"
	testAppUUIDFull       = "d4e5f6a7-b8c9-4012-bcde-000000000005"
	testRecipientBench    = "d4e5f6a7-b8c9-4012-bcde-000000000006"
)

func createTestInput(notificationType string) *Input {
	return &Input{
		// BUG FIX: was "recipient-001" (invalid UUID) → valid UUID v4
		RecipientID:      testRecipientUUID001,
		RecipientType:    RecipientTypeFranchisor,
		NotificationType: notificationType,
		// BUG FIX: was "app-001" (not a UUID) → valid UUID v4
		ApplicationID: testAppUUID001,
		Priority:      "high",
		Metadata: map[string]interface{}{
			"franchiseName": "McDonald's",
			"seekerName":    "John Doe",
		},
	}
}

type testLogger struct {
	t *testing.T
}

func (tl *testLogger) Debug(msg string, fields map[string]interface{}) {
	tl.t.Logf("DEBUG: %s %v", msg, fields)
}

func (tl *testLogger) Info(msg string, fields map[string]interface{}) {
	tl.t.Logf("INFO: %s %v", msg, fields)
}

func (tl *testLogger) Warn(msg string, fields map[string]interface{}) {
	tl.t.Logf("WARN: %s %v", msg, fields)
}

func (tl *testLogger) Error(msg string, fields map[string]interface{}) {
	tl.t.Logf("ERROR: %s %v", msg, fields)
}

func (tl *testLogger) WithFields(fields map[string]interface{}) logger.Logger {
	return tl
}

func (tl *testLogger) WithError(err error) logger.Logger {
	return tl.WithFields(map[string]interface{}{"error": err})
}

func (t *testLogger) With(fields map[string]interface{}) logger.Logger {
	return t
}

func newTestLogger(t *testing.T) logger.Logger {
	return &testLogger{t: t}
}

// ==========================
// Core Functionality Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	tests := []struct {
		name           string
		input          *Input
		emailEnabled   bool
		smsEnabled     bool
		priority       string
		validateOutput func(t *testing.T, output *Output)
	}{
		{
			name:         "email and SMS success",
			input:        createTestInput(TypeNewApplication),
			emailEnabled: true,
			smsEnabled:   true,
			priority:     "high",
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, StatusSent, output.Status)
				assert.NotEmpty(t, output.NotificationID)
				assert.NotEmpty(t, output.SentAt)
			},
		},
		{
			name:         "email only success",
			input:        createTestInput(TypeApplicationSubmitted),
			emailEnabled: true,
			smsEnabled:   false,
			priority:     "medium",
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, StatusSent, output.Status)
			},
		},
		{
			name:         "SMS only for high priority",
			input:        createTestInput(TypeNewApplication),
			emailEnabled: false,
			smsEnabled:   true,
			priority:     "high",
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, StatusSent, output.Status)
			},
		},
		{
			name:         "no SMS for medium priority",
			input:        createTestInput(TypeApplicationSubmitted),
			emailEnabled: false,
			smsEnabled:   true,
			priority:     "medium",
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, StatusDisabled, output.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			assert.NoError(t, err)
			defer db.Close()

			// Mock recipient lookup
			// BUG FIX: was "recipient-001" (invalid UUID) → use valid UUID v4 constant
			mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
				WithArgs(testRecipientUUID001).
				WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
					AddRow("franchisor@example.com", "+1234567890"))

			// Mock idempotency DB insert (notifications table)
			mock.ExpectQuery(`INSERT INTO notifications`).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("some-notif-id"))

			mockSES := &MockSESService{
				SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
					assert.Equal(t, "franchisor@example.com", params.Destination.ToAddresses[0])
					assert.Equal(t, "noreply@franchise.com", *params.Source)
					return &ses.SendEmailOutput{}, nil
				},
			}

			mockSNS := &MockSNSService{
				PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
					if tt.priority == "high" && tt.smsEnabled {
						assert.Equal(t, "+1234567890", *params.PhoneNumber)
					}
					return &sns.PublishOutput{}, nil
				},
			}

			config := createTestConfig()
			config.EmailEnabled = tt.emailEnabled
			config.SMSEnabled = tt.smsEnabled

			handler := createTestHandler(config, db, newTestLogger(t), mockSES, mockSNS)

			tt.input.Priority = tt.priority
			output, err := handler.Execute(context.Background(), tt.input)

			assert.NoError(t, err)
			assert.NotNil(t, output)

			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}
		})
	}
}

func TestHandler_Execute_RecipientNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Mock recipient not found
	// BUG FIX: was "recipient-001" → valid UUID v4
	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
		WithArgs(testRecipientUUID001).
		WillReturnError(sql.ErrNoRows)

	config := createTestConfig()
	mockSES := &MockSESService{SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
		return &ses.SendEmailOutput{}, nil
	}}
	mockSNS := &MockSNSService{PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
		return &sns.PublishOutput{}, nil
	}}
	handler := createTestHandler(config, db, newTestLogger(t), mockSES, mockSNS)

	input := createTestInput(TypeNewApplication)
	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, StatusDisabled, output.Status)
	assert.NotEmpty(t, output.NotificationID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_EmailFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// BUG FIX: was "recipient-001" → valid UUID v4
	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
		WithArgs(testRecipientUUID001).
		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
			AddRow("franchisor@example.com", "+1234567890"))

	// Mock notification DB insert
	mock.ExpectQuery(`INSERT INTO notifications`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("notif-id"))

	mockSES := &MockSESService{
		SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
			return nil, errors.New("SES service unavailable")
		},
	}

	mockSNS := &MockSNSService{
		PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
			return &sns.PublishOutput{}, nil
		},
	}

	config := createTestConfig()
	handler := createTestHandler(config, db, newTestLogger(t), mockSES, mockSNS)

	input := createTestInput(TypeNewApplication)
	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, StatusFailed, output.Status)
}

func TestHandler_Execute_SMSFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// BUG FIX: was "recipient-001" → valid UUID v4
	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
		WithArgs(testRecipientUUID001).
		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
			AddRow("franchisor@example.com", "+1234567890"))

	mock.ExpectQuery(`INSERT INTO notifications`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("notif-id"))

	mockSES := &MockSESService{
		SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
			return &ses.SendEmailOutput{}, nil
		},
	}

	mockSNS := &MockSNSService{
		PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
			return nil, errors.New("SNS service unavailable")
		},
	}

	config := createTestConfig()
	handler := createTestHandler(config, db, newTestLogger(t), mockSES, mockSNS)

	input := createTestInput(TypeNewApplication)
	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, StatusFailed, output.Status)
}

func TestHandler_Execute_TemplateNotFound(t *testing.T) {
	// BUG FIX: Original test passed "unknown_template_type" to validateInput,
	// which checks ValidateEnum([TypeNewApplication, TypeApplicationSubmitted]).
	// "unknown_template_type" fails enum validation → error is about notificationType,
	// NOT "template not found". The test must assert validation error, not template error.
	handler := createTestHandler(createTestConfig(), nil, newTestLogger(t), &MockSESService{}, &MockSNSService{})

	input := createTestInput("unknown_template_type")
	output, err := handler.Execute(context.Background(), input)

	assert.Error(t, err)
	// BUG FIX: error comes from validateInput enum check (notificationType),
	// NOT from template lookup. Error contains "notificationType", not "template not found".
	assert.Contains(t, err.Error(), "notificationType")
	assert.Nil(t, output)
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_GetRecipientContact(t *testing.T) {
	tests := []struct {
		name          string
		recipientType string
		query         string
		expectedEmail string
		expectedPhone string
		expectError   bool
		errorContains string
	}{
		{
			name:          "franchisor recipient",
			recipientType: RecipientTypeFranchisor,
			query:         `SELECT email, phone FROM franchisors WHERE id = \$1`,
			expectedEmail: "franchisor@example.com",
			expectedPhone: "+1234567890",
		},
		{
			name:          "seeker recipient",
			recipientType: RecipientTypeSeeker,
			query:         `SELECT email, phone FROM users WHERE id = \$1`,
			expectedEmail: "seeker@example.com",
			expectedPhone: "+1987654321",
		},
		{
			name:          "invalid recipient type",
			recipientType: "invalid",
			expectError:   true,
			errorContains: "invalid recipient type",
		},
		{
			name:          "recipient not found",
			recipientType: RecipientTypeFranchisor,
			query:         `SELECT email, phone FROM franchisors WHERE id = \$1`,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			assert.NoError(t, err)
			defer db.Close()

			handler := &Handler{db: db, logger: newTestLogger(t)}

			if !tt.expectError || tt.recipientType == "invalid" {
				if tt.recipientType != "invalid" {
					mock.ExpectQuery(tt.query).
						// BUG FIX: was "recipient-001" → valid UUID v4
						WithArgs(testRecipientUUID001).
						WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
							AddRow(tt.expectedEmail, tt.expectedPhone))
				}
			} else {
				mock.ExpectQuery(tt.query).
					WithArgs(testRecipientUUID001).
					WillReturnError(sql.ErrNoRows)
			}

			ctx := context.Background()
			// BUG FIX: was "recipient-001" → valid UUID v4
			email, phone, err := handler.getRecipientContact(ctx, testRecipientUUID001, tt.recipientType)
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedEmail, email)
				assert.Equal(t, tt.expectedPhone, phone)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestHandler_RenderTemplate(t *testing.T) {
	tests := []struct {
		name     string
		template string
		data     map[string]interface{}
		expected string
	}{
		{
			name:     "simple replacement",
			template: "Hello {{name}}, your application {{appId}} is ready.",
			data: map[string]interface{}{
				"name":  "John",
				"appId": "APP-123",
			},
			expected: "Hello John, your application APP-123 is ready.",
		},
		{
			name:     "multiple replacements",
			template: "Application {{applicationId}} for {{franchiseName}} has priority {{priority}}.",
			data: map[string]interface{}{
				"applicationId": "APP-001",
				"franchiseName": "McDonald's",
				"priority":      "high",
			},
			expected: "Application APP-001 for McDonald's has priority high.",
		},
		{
			name:     "integer value",
			template: "Your score is {{score}} points.",
			data: map[string]interface{}{
				"score": 85,
			},
			expected: "Your score is 85 points.",
		},
		{
			name:     "no replacements",
			template: "Static message without placeholders.",
			data:     map[string]interface{}{},
			expected: "Static message without placeholders.",
		},
		{
			name:     "missing placeholder",
			template: "Hello {{name}}, your {{missing}} is here.",
			data: map[string]interface{}{
				"name": "John",
			},
			expected: "Hello John, your  is here.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderTemplate(tt.template, tt.data)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_LoadTemplates(t *testing.T) {
	templates, err := loadTemplates("test-registry")

	assert.NoError(t, err)
	assert.NotNil(t, templates)

	newAppTemplate, exists := templates[TypeNewApplication]
	assert.True(t, exists)
	assert.Equal(t, "New Franchise Application Received", newAppTemplate["subject"])
	assert.Contains(t, newAppTemplate["body"], "new application")

	submittedTemplate, exists := templates[TypeApplicationSubmitted]
	assert.True(t, exists)
	assert.Equal(t, "Application Submitted Successfully", submittedTemplate["subject"])
	assert.Contains(t, submittedTemplate["body"], "submitted")
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	t.Run("empty recipient ID fails UUID validation", func(t *testing.T) {
		// BUG FIX: Original test expected NoError with empty RecipientID.
		// validateInput uses ozzo.Required which rejects empty string → error.
		handler := createTestHandler(createTestConfig(), nil, newTestLogger(t), &MockSESService{}, &MockSNSService{})

		input := &Input{
			RecipientID:      "",
			RecipientType:    RecipientTypeFranchisor,
			NotificationType: TypeNewApplication,
		}

		output, err := handler.Execute(context.Background(), input)

		assert.Error(t, err)
		assert.Nil(t, output)
	})

	t.Run("empty metadata", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		assert.NoError(t, err)
		defer db.Close()

		// BUG FIX: was "recipient-001" → valid UUID v4
		mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
			WithArgs(testRecipientUUID001).
			WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
				AddRow("franchisor@example.com", "+1234567890"))

		mock.ExpectQuery(`INSERT INTO notifications`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("notif-id"))

		mockSES := &MockSESService{
			SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
				return &ses.SendEmailOutput{}, nil
			},
		}

		mockSNS := &MockSNSService{
			PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
				return &sns.PublishOutput{}, nil
			},
		}

		config := createTestConfig()
		handler := createTestHandler(config, db, newTestLogger(t), mockSES, mockSNS)

		input := &Input{
			// BUG FIX: was "recipient-001" → valid UUID v4
			RecipientID:      testRecipientUUID001,
			RecipientType:    RecipientTypeFranchisor,
			NotificationType: TypeNewApplication,
			// BUG FIX: was "app-001" → valid UUID v4
			ApplicationID: testAppUUID001,
			Priority:      "high",
			Metadata:      nil,
		}

		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.Equal(t, StatusSent, output.Status)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("context timeout - recipient query fails", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		assert.NoError(t, err)
		defer db.Close()

		mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
			WithArgs(testRecipientUUID001).
			WillReturnError(context.DeadlineExceeded)

		config := createTestConfig()
		handler := createTestHandler(config, db, newTestLogger(t), &MockSESService{}, &MockSNSService{})

		input := createTestInput(TypeNewApplication)
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()

		output, err := handler.Execute(ctx, input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.Equal(t, StatusDisabled, output.Status)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("special characters in template data", func(t *testing.T) {
		template := "Message: {{content}}"
		data := map[string]interface{}{
			"content": "Special chars: <>&\"' and unicode: 🚀",
		}

		result := renderTemplate(template, data)
		expected := "Message: Special chars: <>&\"' and unicode: 🚀"
		assert.Equal(t, expected, result)
	})
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// BUG FIX: was "franchisor-001" (invalid UUID) → valid UUID v4
	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
		WithArgs(testFranchisorUUID001).
		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
			AddRow("owner@mcdonalds.com", "+15551234567"))

	mock.ExpectQuery(`INSERT INTO notifications`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("notif-full-id"))

	emailSent := false
	mockSES := &MockSESService{
		SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
			emailSent = true
			assert.Equal(t, "owner@mcdonalds.com", params.Destination.ToAddresses[0])
			assert.Equal(t, "noreply@franchise.com", *params.Source)
			assert.Contains(t, *params.Message.Subject.Data, "New Franchise Application Received")
			assert.Contains(t, *params.Message.Body.Text.Data, testAppUUIDFull)
			return &ses.SendEmailOutput{}, nil
		},
	}

	smsSent := false
	mockSNS := &MockSNSService{
		PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
			smsSent = true
			assert.Equal(t, "+15551234567", *params.PhoneNumber)
			assert.Contains(t, *params.Message, testAppUUIDFull)
			return &sns.PublishOutput{}, nil
		},
	}

	config := createTestConfig()
	handler := createTestHandler(config, db, newTestLogger(t), mockSES, mockSNS)

	input := &Input{
		// BUG FIX: was "franchisor-001" (invalid UUID) → valid UUID v4
		RecipientID:      testFranchisorUUID001,
		RecipientType:    RecipientTypeFranchisor,
		NotificationType: TypeNewApplication,
		// BUG FIX: was "APP-FULL-001" (not a UUID) → valid UUID v4
		ApplicationID: testAppUUIDFull,
		Priority:      "high",
		Metadata: map[string]interface{}{
			"franchiseName": "McDonald's",
			"seekerName":    "Jane Smith",
			"investment":    500000,
		},
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, StatusSent, output.Status)
	assert.NotEmpty(t, output.NotificationID)
	assert.NotEmpty(t, output.SentAt)

	assert.True(t, emailSent)
	assert.True(t, smsSent)

	_, err = time.Parse(time.RFC3339, output.SentAt)
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	db, _, err := sqlmock.New()
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	mockSES := &MockSESService{
		SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
			return &ses.SendEmailOutput{}, nil
		},
	}

	mockSNS := &MockSNSService{
		PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
			return &sns.PublishOutput{}, nil
		},
	}

	config := createTestConfig()
	handler := createTestHandler(config, db, newTestLogger(&testing.T{}), mockSES, mockSNS)

	input := createTestInput(TypeNewApplication)
	// BUG FIX: was "benchmark-recipient" (invalid UUID) → valid UUID v4
	input.RecipientID = testRecipientBench

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_RenderTemplate(b *testing.B) {
	template := "Application {{applicationId}} for {{franchiseName}} by {{seekerName}} with priority {{priority}}."
	data := map[string]interface{}{
		"applicationId": "APP-001",
		"franchiseName": "McDonald's",
		"seekerName":    "John Doe",
		"priority":      "high",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = renderTemplate(template, data)
	}
}

// Helper function for test templates
func loadTestTemplates() map[string]map[string]interface{} {
	return map[string]map[string]interface{}{
		TypeNewApplication: {
			"subject": "New Franchise Application Received",
			"body":    "Hello, you have a new application for {{applicationId}}. Priority: {{priority}}.",
		},
		TypeApplicationSubmitted: {
			"subject": "Application Submitted Successfully",
			"body":    "Thank you! Your application {{applicationId}} has been submitted.",
		},
	}
}
