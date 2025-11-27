// internal/workers/application/send-notification/handler_test.go
package sendnotification

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"camunda-workers/internal/common/logger"

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

func createTestInput(notificationType string) *Input {
	return &Input{
		RecipientID:      "recipient-001",
		RecipientType:    RecipientTypeFranchisor,
		NotificationType: notificationType,
		ApplicationID:    "app-001",
		Priority:         "high",
		Metadata: map[string]interface{}{
			"franchiseName": "McDonald's",
			"seekerName":    "John Doe",
		},
	}
}

// Create a test logger that implements your logger.Logger interface
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
	return tl // Simple implementation for testing
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
			mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
				WithArgs("recipient-001").
				WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
					AddRow("franchisor@example.com", "+1234567890"))

			// Mock SES service
			mockSES := &MockSESService{
				SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
					assert.Equal(t, "franchisor@example.com", params.Destination.ToAddresses[0])
					assert.Equal(t, "noreply@franchise.com", *params.Source)
					return &ses.SendEmailOutput{}, nil
				},
			}

			// Mock SNS service
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

			handler := &Handler{
				config:      config,
				db:          db,
				logger:      newTestLogger(t),
				sesClient:   mockSES,
				snsClient:   mockSNS,
				templateMap: loadTestTemplates(),
			}

			tt.input.Priority = tt.priority
			output, err := handler.Execute(context.Background(), tt.input)

			assert.NoError(t, err)
			assert.NotNil(t, output)

			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestHandler_Execute_RecipientNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Mock recipient not found
	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
		WithArgs("recipient-001").
		WillReturnError(sql.ErrNoRows)

	config := createTestConfig()
	handler, err := NewHandler(config, db, newTestLogger(t))
	assert.NoError(t, err)

	// Replace with mock clients
	handler.sesClient = &MockSESService{}
	handler.snsClient = &MockSNSService{}

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

	// Mock recipient lookup
	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
		WithArgs("recipient-001").
		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
			AddRow("franchisor@example.com", "+1234567890"))

	// Mock SES service failure
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
	handler := &Handler{
		config:      config,
		db:          db,
		logger:      newTestLogger(t),
		sesClient:   mockSES,
		snsClient:   mockSNS,
		templateMap: loadTestTemplates(),
	}

	input := createTestInput(TypeNewApplication)
	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, StatusFailed, output.Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_SMSFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Mock recipient lookup
	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
		WithArgs("recipient-001").
		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
			AddRow("franchisor@example.com", "+1234567890"))

	// Mock SES service success
	mockSES := &MockSESService{
		SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
			return &ses.SendEmailOutput{}, nil
		},
	}

	// Mock SNS service failure
	mockSNS := &MockSNSService{
		PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
			return nil, errors.New("SNS service unavailable")
		},
	}

	config := createTestConfig()
	handler := &Handler{
		config:      config,
		db:          db,
		logger:      newTestLogger(t),
		sesClient:   mockSES,
		snsClient:   mockSNS,
		templateMap: loadTestTemplates(),
	}

	input := createTestInput(TypeNewApplication)
	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, StatusFailed, output.Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_TemplateNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Mock recipient lookup
	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
		WithArgs("recipient-001").
		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
			AddRow("franchisor@example.com", "+1234567890"))

	config := createTestConfig()
	handler, err := NewHandler(config, db, newTestLogger(t))
	assert.NoError(t, err)

	// Replace with mock clients
	handler.sesClient = &MockSESService{}
	handler.snsClient = &MockSNSService{}

	input := createTestInput("unknown_template_type")
	output, err := handler.Execute(context.Background(), input)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "template not found")
	assert.Nil(t, output)

	assert.NoError(t, mock.ExpectationsWereMet())
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
						WithArgs("recipient-001").
						WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
							AddRow(tt.expectedEmail, tt.expectedPhone))
				}
			} else {
				mock.ExpectQuery(tt.query).
					WithArgs("recipient-001").
					WillReturnError(sql.ErrNoRows)
			}

			email, phone, err := handler.getRecipientContact("recipient-001", tt.recipientType)

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

	// Verify template structure
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
	t.Run("empty recipient ID", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		assert.NoError(t, err)
		defer db.Close()

		// Mock recipient lookup with empty ID
		mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
			WithArgs("").
			WillReturnError(sql.ErrNoRows)

		config := createTestConfig()
		handler, err := NewHandler(config, db, newTestLogger(t))
		assert.NoError(t, err)

		handler.sesClient = &MockSESService{}
		handler.snsClient = &MockSNSService{}

		input := &Input{
			RecipientID:      "",
			RecipientType:    RecipientTypeFranchisor,
			NotificationType: TypeNewApplication,
		}

		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.Equal(t, StatusDisabled, output.Status)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty metadata", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		assert.NoError(t, err)
		defer db.Close()

		// Mock recipient lookup
		mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
			WithArgs("recipient-001").
			WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
				AddRow("franchisor@example.com", "+1234567890"))

		mockSES := &MockSESService{
			SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
				return &ses.SendEmailOutput{}, nil
			},
		}

		mockSNS := &MockSNSService{
			PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
				// This will be called only if priority is high
				return &sns.PublishOutput{}, nil
			},
		}

		config := createTestConfig()
		handler := &Handler{
			config:      config,
			db:          db,
			logger:      newTestLogger(t),
			sesClient:   mockSES,
			snsClient:   mockSNS,
			templateMap: loadTestTemplates(),
		}

		input := &Input{
			RecipientID:      "recipient-001",
			RecipientType:    RecipientTypeFranchisor,
			NotificationType: TypeNewApplication,
			ApplicationID:    "app-001",
			Priority:         "high",
			Metadata:         nil, // Empty metadata
		}

		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.Equal(t, StatusSent, output.Status)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("context timeout", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		assert.NoError(t, err)
		defer db.Close()

		// Mock recipient lookup that times out
		mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
			WithArgs("recipient-001").
			WillReturnError(context.DeadlineExceeded)

		config := createTestConfig()
		handler, err := NewHandler(config, db, newTestLogger(t))
		assert.NoError(t, err)

		handler.sesClient = &MockSESService{}
		handler.snsClient = &MockSNSService{}

		input := createTestInput(TypeNewApplication)
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()

		output, err := handler.Execute(ctx, input)

		assert.NoError(t, err) // Should handle gracefully
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

	// Mock recipient lookup
	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
		WithArgs("franchisor-001").
		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
			AddRow("owner@mcdonalds.com", "+15551234567"))

	// Mock SES service
	emailSent := false
	mockSES := &MockSESService{
		SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
			emailSent = true
			assert.Equal(t, "owner@mcdonalds.com", params.Destination.ToAddresses[0])
			assert.Equal(t, "noreply@franchise.com", *params.Source)
			assert.Contains(t, *params.Message.Subject.Data, "New Franchise Application Received")
			assert.Contains(t, *params.Message.Body.Text.Data, "APP-FULL-001")
			return &ses.SendEmailOutput{}, nil
		},
	}

	// Mock SNS service
	smsSent := false
	mockSNS := &MockSNSService{
		PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
			smsSent = true
			assert.Equal(t, "+15551234567", *params.PhoneNumber)
			assert.Contains(t, *params.Message, "APP-FULL-001")
			return &sns.PublishOutput{}, nil
		},
	}

	config := createTestConfig()
	handler := &Handler{
		config:      config,
		db:          db,
		logger:      newTestLogger(t),
		sesClient:   mockSES,
		snsClient:   mockSNS,
		templateMap: loadTestTemplates(),
	}

	input := &Input{
		RecipientID:      "franchisor-001",
		RecipientType:    RecipientTypeFranchisor,
		NotificationType: TypeNewApplication,
		ApplicationID:    "APP-FULL-001",
		Priority:         "high",
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

	// Verify both email and SMS were sent
	assert.True(t, emailSent)
	assert.True(t, smsSent)

	// Verify timestamp format
	_, err = time.Parse(time.RFC3339, output.SentAt)
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	db, mock, err := sqlmock.New()
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	// Setup mock expectations
	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
		WithArgs("benchmark-recipient").
		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
			AddRow("benchmark@example.com", "+1234567890"))

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
	handler := &Handler{
		config:      config,
		db:          db,
		logger:      newTestLogger(&testing.T{}),
		sesClient:   mockSES,
		snsClient:   mockSNS,
		templateMap: loadTestTemplates(),
	}

	input := createTestInput(TypeNewApplication)
	input.RecipientID = "benchmark-recipient"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.Execute(context.Background(), input)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		b.Errorf("there were unfulfilled expectations: %s", err)
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

// // internal/workers/application/send-notification/handler_test.go
// package sendnotification

// import (
// 	"context"
// 	"database/sql"
// 	"errors"
// 	"testing"
// 	"time"

// 	"github.com/DATA-DOG/go-sqlmock"
// 	"github.com/aws/aws-sdk-go-v2/service/ses"
// 	"github.com/aws/aws-sdk-go-v2/service/sns"
// 	"github.com/stretchr/testify/assert"
// 	"go.uber.org/zap/zaptest"
// )

// // ==========================
// // Mock Implementations
// // ==========================

// type MockSESService struct {
// 	SendEmailFunc func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error)
// }

// func (m *MockSESService) SendEmail(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
// 	return m.SendEmailFunc(ctx, params, optFns...)
// }

// type MockSNSService struct {
// 	PublishFunc func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error)
// }

// func (m *MockSNSService) Publish(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
// 	return m.PublishFunc(ctx, params, optFns...)
// }

// // ==========================
// // Test Helper Functions
// // ==========================

// func createTestConfig() *Config {
// 	return &Config{
// 		EmailEnabled:     true,
// 		SMSEnabled:       true,
// 		FromEmail:        "noreply@franchise.com",
// 		AWSRegion:        "us-east-1",
// 		TemplateRegistry: "test-registry",
// 		Timeout:          30 * time.Second,
// 	}
// }

// func createTestInput(notificationType string) *Input {
// 	return &Input{
// 		RecipientID:      "recipient-001",
// 		RecipientType:    RecipientTypeFranchisor,
// 		NotificationType: notificationType,
// 		ApplicationID:    "app-001",
// 		Priority:         "high",
// 		Metadata: map[string]interface{}{
// 			"franchiseName": "McDonald's",
// 			"seekerName":    "John Doe",
// 		},
// 	}
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name           string
// 		input          *Input
// 		emailEnabled   bool
// 		smsEnabled     bool
// 		priority       string
// 		validateOutput func(t *testing.T, output *Output)
// 	}{
// 		{
// 			name:         "email and SMS success",
// 			input:        createTestInput(TypeNewApplication),
// 			emailEnabled: true,
// 			smsEnabled:   true,
// 			priority:     "high",
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, StatusSent, output.Status)
// 				assert.NotEmpty(t, output.NotificationID)
// 				assert.NotEmpty(t, output.SentAt)
// 			},
// 		},
// 		{
// 			name:         "email only success",
// 			input:        createTestInput(TypeApplicationSubmitted),
// 			emailEnabled: true,
// 			smsEnabled:   false,
// 			priority:     "medium",
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, StatusSent, output.Status)
// 			},
// 		},
// 		{
// 			name:         "SMS only for high priority",
// 			input:        createTestInput(TypeNewApplication),
// 			emailEnabled: false,
// 			smsEnabled:   true,
// 			priority:     "high",
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, StatusSent, output.Status)
// 			},
// 		},
// 		{
// 			name:         "no SMS for medium priority",
// 			input:        createTestInput(TypeApplicationSubmitted),
// 			emailEnabled: false,
// 			smsEnabled:   true,
// 			priority:     "medium",
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, StatusDisabled, output.Status)
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			assert.NoError(t, err)
// 			defer db.Close()

// 			// Mock recipient lookup
// 			mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
// 				WithArgs("recipient-001").
// 				WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
// 					AddRow("franchisor@example.com", "+1234567890"))

// 			// Mock SES service
// 			mockSES := &MockSESService{
// 				SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
// 					assert.Equal(t, "franchisor@example.com", params.Destination.ToAddresses[0])
// 					assert.Equal(t, "noreply@franchise.com", *params.Source)
// 					return &ses.SendEmailOutput{}, nil
// 				},
// 			}

// 			// Mock SNS service
// 			mockSNS := &MockSNSService{
// 				PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
// 					if tt.priority == "high" && tt.smsEnabled {
// 						assert.Equal(t, "+1234567890", *params.PhoneNumber)
// 					}
// 					return &sns.PublishOutput{}, nil
// 				},
// 			}

// 			config := createTestConfig()
// 			config.EmailEnabled = tt.emailEnabled
// 			config.SMSEnabled = tt.smsEnabled

// 			handler := &Handler{
// 				config:      config,
// 				db:          db,
// 				logger:      zaptest.NewLogger(t),
// 				sesClient:   mockSES,
// 				snsClient:   mockSNS,
// 				templateMap: loadTestTemplates(),
// 			}

// 			tt.input.Priority = tt.priority
// 			output, err := handler.execute(context.Background(), tt.input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)

// 			if tt.validateOutput != nil {
// 				tt.validateOutput(t, output)
// 			}

// 			assert.NoError(t, mock.ExpectationsWereMet())
// 		})
// 	}
// }

// func TestHandler_Execute_RecipientNotFound(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock recipient not found
// 	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
// 		WithArgs("recipient-001").
// 		WillReturnError(sql.ErrNoRows)

// 	config := createTestConfig()
// 	handler, err := NewHandler(config, db, zaptest.NewLogger(t))
// 	assert.NoError(t, err)

// 	// Replace with mock clients
// 	handler.sesClient = &MockSESService{}
// 	handler.snsClient = &MockSNSService{}

// 	input := createTestInput(TypeNewApplication)
// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.Equal(t, StatusDisabled, output.Status)
// 	assert.NotEmpty(t, output.NotificationID)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_EmailFailure(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock recipient lookup
// 	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
// 		WithArgs("recipient-001").
// 		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
// 			AddRow("franchisor@example.com", "+1234567890"))

// 	// Mock SES service failure
// 	mockSES := &MockSESService{
// 		SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
// 			return nil, errors.New("SES service unavailable")
// 		},
// 	}

// 	mockSNS := &MockSNSService{
// 		PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
// 			return &sns.PublishOutput{}, nil
// 		},
// 	}

// 	config := createTestConfig()
// 	handler := &Handler{
// 		config:      config,
// 		db:          db,
// 		logger:      zaptest.NewLogger(t),
// 		sesClient:   mockSES,
// 		snsClient:   mockSNS,
// 		templateMap: loadTestTemplates(),
// 	}

// 	input := createTestInput(TypeNewApplication)
// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.Equal(t, StatusFailed, output.Status)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_SMSFailure(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock recipient lookup
// 	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
// 		WithArgs("recipient-001").
// 		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
// 			AddRow("franchisor@example.com", "+1234567890"))

// 	// Mock SES service success
// 	mockSES := &MockSESService{
// 		SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
// 			return &ses.SendEmailOutput{}, nil
// 		},
// 	}

// 	// Mock SNS service failure
// 	mockSNS := &MockSNSService{
// 		PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
// 			return nil, errors.New("SNS service unavailable")
// 		},
// 	}

// 	config := createTestConfig()
// 	handler := &Handler{
// 		config:      config,
// 		db:          db,
// 		logger:      zaptest.NewLogger(t),
// 		sesClient:   mockSES,
// 		snsClient:   mockSNS,
// 		templateMap: loadTestTemplates(),
// 	}

// 	input := createTestInput(TypeNewApplication)
// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.Equal(t, StatusFailed, output.Status)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_TemplateNotFound(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock recipient lookup
// 	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
// 		WithArgs("recipient-001").
// 		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
// 			AddRow("franchisor@example.com", "+1234567890"))

// 	config := createTestConfig()
// 	handler, err := NewHandler(config, db, zaptest.NewLogger(t))
// 	assert.NoError(t, err)

// 	// Replace with mock clients
// 	handler.sesClient = &MockSESService{}
// 	handler.snsClient = &MockSNSService{}

// 	input := createTestInput("unknown_template_type")
// 	output, err := handler.execute(context.Background(), input)

// 	assert.Error(t, err)
// 	assert.Contains(t, err.Error(), "template not found")
// 	assert.Nil(t, output)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_GetRecipientContact(t *testing.T) {
// 	tests := []struct {
// 		name          string
// 		recipientType string
// 		query         string
// 		expectedEmail string
// 		expectedPhone string
// 		expectError   bool
// 		errorContains string
// 	}{
// 		{
// 			name:          "franchisor recipient",
// 			recipientType: RecipientTypeFranchisor,
// 			query:         `SELECT email, phone FROM franchisors WHERE id = \$1`,
// 			expectedEmail: "franchisor@example.com",
// 			expectedPhone: "+1234567890",
// 		},
// 		{
// 			name:          "seeker recipient",
// 			recipientType: RecipientTypeSeeker,
// 			query:         `SELECT email, phone FROM users WHERE id = \$1`,
// 			expectedEmail: "seeker@example.com",
// 			expectedPhone: "+1987654321",
// 		},
// 		{
// 			name:          "invalid recipient type",
// 			recipientType: "invalid",
// 			expectError:   true,
// 			errorContains: "invalid recipient type",
// 		},
// 		{
// 			name:          "recipient not found",
// 			recipientType: RecipientTypeFranchisor,
// 			query:         `SELECT email, phone FROM franchisors WHERE id = \$1`,
// 			expectError:   true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			assert.NoError(t, err)
// 			defer db.Close()

// 			handler := &Handler{db: db, logger: zaptest.NewLogger(t)}

// 			if !tt.expectError || tt.recipientType == "invalid" {
// 				if tt.recipientType != "invalid" {
// 					mock.ExpectQuery(tt.query).
// 						WithArgs("recipient-001").
// 						WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
// 							AddRow(tt.expectedEmail, tt.expectedPhone))
// 				}
// 			} else {
// 				mock.ExpectQuery(tt.query).
// 					WithArgs("recipient-001").
// 					WillReturnError(sql.ErrNoRows)
// 			}

// 			email, phone, err := handler.getRecipientContact("recipient-001", tt.recipientType)

// 			if tt.expectError {
// 				assert.Error(t, err)
// 				if tt.errorContains != "" {
// 					assert.Contains(t, err.Error(), tt.errorContains)
// 				}
// 			} else {
// 				assert.NoError(t, err)
// 				assert.Equal(t, tt.expectedEmail, email)
// 				assert.Equal(t, tt.expectedPhone, phone)
// 			}

// 			assert.NoError(t, mock.ExpectationsWereMet())
// 		})
// 	}
// }

// func TestHandler_RenderTemplate(t *testing.T) {
// 	tests := []struct {
// 		name     string
// 		template string
// 		data     map[string]interface{}
// 		expected string
// 	}{
// 		{
// 			name:     "simple replacement",
// 			template: "Hello {{name}}, your application {{appId}} is ready.",
// 			data: map[string]interface{}{
// 				"name":  "John",
// 				"appId": "APP-123",
// 			},
// 			expected: "Hello John, your application APP-123 is ready.",
// 		},
// 		{
// 			name:     "multiple replacements",
// 			template: "Application {{applicationId}} for {{franchiseName}} has priority {{priority}}.",
// 			data: map[string]interface{}{
// 				"applicationId": "APP-001",
// 				"franchiseName": "McDonald's",
// 				"priority":      "high",
// 			},
// 			expected: "Application APP-001 for McDonald's has priority high.",
// 		},
// 		{
// 			name:     "integer value",
// 			template: "Your score is {{score}} points.",
// 			data: map[string]interface{}{
// 				"score": 85,
// 			},
// 			expected: "Your score is 85 points.",
// 		},
// 		{
// 			name:     "no replacements",
// 			template: "Static message without placeholders.",
// 			data:     map[string]interface{}{},
// 			expected: "Static message without placeholders.",
// 		},
// 		{
// 			name:     "missing placeholder",
// 			template: "Hello {{name}}, your {{missing}} is here.",
// 			data: map[string]interface{}{
// 				"name": "John",
// 			},
// 			expected: "Hello John, your  is here.",
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result := renderTemplate(tt.template, tt.data)
// 			assert.Equal(t, tt.expected, result)
// 		})
// 	}
// }

// func TestHandler_LoadTemplates(t *testing.T) {
// 	templates, err := loadTemplates("test-registry")

// 	assert.NoError(t, err)
// 	assert.NotNil(t, templates)

// 	// Verify template structure
// 	newAppTemplate, exists := templates[TypeNewApplication]
// 	assert.True(t, exists)
// 	assert.Equal(t, "New Franchise Application Received", newAppTemplate["subject"])
// 	assert.Contains(t, newAppTemplate["body"], "new application")

// 	submittedTemplate, exists := templates[TypeApplicationSubmitted]
// 	assert.True(t, exists)
// 	assert.Equal(t, "Application Submitted Successfully", submittedTemplate["subject"])
// 	assert.Contains(t, submittedTemplate["body"], "submitted")
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	t.Run("empty recipient ID", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		assert.NoError(t, err)
// 		defer db.Close()

// 		// Mock recipient lookup with empty ID
// 		mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
// 			WithArgs("").
// 			WillReturnError(sql.ErrNoRows)

// 		config := createTestConfig()
// 		handler, err := NewHandler(config, db, zaptest.NewLogger(t))
// 		assert.NoError(t, err)

// 		handler.sesClient = &MockSESService{}
// 		handler.snsClient = &MockSNSService{}

// 		input := &Input{
// 			RecipientID:      "",
// 			RecipientType:    RecipientTypeFranchisor,
// 			NotificationType: TypeNewApplication,
// 		}

// 		output, err := handler.execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		assert.Equal(t, StatusDisabled, output.Status)

// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})

// 	t.Run("empty metadata", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		assert.NoError(t, err)
// 		defer db.Close()

// 		// Mock recipient lookup
// 		mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
// 			WithArgs("recipient-001").
// 			WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
// 				AddRow("franchisor@example.com", "+1234567890"))

// 		mockSES := &MockSESService{
// 			SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
// 				return &ses.SendEmailOutput{}, nil
// 			},
// 		}

// 		mockSNS := &MockSNSService{
// 			PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
// 				// This will be called only if priority is high
// 				return &sns.PublishOutput{}, nil
// 			},
// 		}

// 		config := createTestConfig()
// 		handler := &Handler{
// 			config:      config,
// 			db:          db,
// 			logger:      zaptest.NewLogger(t),
// 			sesClient:   mockSES,
// 			snsClient:   mockSNS,
// 			templateMap: loadTestTemplates(),
// 		}

// 		input := &Input{
// 			RecipientID:      "recipient-001",
// 			RecipientType:    RecipientTypeFranchisor,
// 			NotificationType: TypeNewApplication,
// 			ApplicationID:    "app-001",
// 			Priority:         "high",
// 			Metadata:         nil, // Empty metadata
// 		}

// 		output, err := handler.execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		assert.Equal(t, StatusSent, output.Status)

// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})

// 	t.Run("context timeout", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		assert.NoError(t, err)
// 		defer db.Close()

// 		// Mock recipient lookup that times out
// 		mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
// 			WithArgs("recipient-001").
// 			WillReturnError(context.DeadlineExceeded)

// 		config := createTestConfig()
// 		handler, err := NewHandler(config, db, zaptest.NewLogger(t))
// 		assert.NoError(t, err)

// 		handler.sesClient = &MockSESService{}
// 		handler.snsClient = &MockSNSService{}

// 		input := createTestInput(TypeNewApplication)
// 		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
// 		defer cancel()

// 		output, err := handler.execute(ctx, input)

// 		assert.NoError(t, err) // Should handle gracefully
// 		assert.NotNil(t, output)
// 		assert.Equal(t, StatusDisabled, output.Status)

// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})

// 	t.Run("special characters in template data", func(t *testing.T) {
// 		template := "Message: {{content}}"
// 		data := map[string]interface{}{
// 			"content": "Special chars: <>&\"' and unicode: 🚀",
// 		}

// 		result := renderTemplate(template, data)
// 		expected := "Message: Special chars: <>&\"' and unicode: 🚀"
// 		assert.Equal(t, expected, result)
// 	})
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock recipient lookup
// 	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
// 		WithArgs("franchisor-001").
// 		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
// 			AddRow("owner@mcdonalds.com", "+15551234567"))

// 	// Mock SES service
// 	emailSent := false
// 	mockSES := &MockSESService{
// 		SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
// 			emailSent = true
// 			assert.Equal(t, "owner@mcdonalds.com", params.Destination.ToAddresses[0])
// 			assert.Equal(t, "noreply@franchise.com", *params.Source)
// 			assert.Contains(t, *params.Message.Subject.Data, "New Franchise Application Received")
// 			assert.Contains(t, *params.Message.Body.Text.Data, "APP-FULL-001")
// 			return &ses.SendEmailOutput{}, nil
// 		},
// 	}

// 	// Mock SNS service
// 	smsSent := false
// 	mockSNS := &MockSNSService{
// 		PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
// 			smsSent = true
// 			assert.Equal(t, "+15551234567", *params.PhoneNumber)
// 			assert.Contains(t, *params.Message, "APP-FULL-001")
// 			return &sns.PublishOutput{}, nil
// 		},
// 	}

// 	config := createTestConfig()
// 	handler := &Handler{
// 		config:      config,
// 		db:          db,
// 		logger:      zaptest.NewLogger(t),
// 		sesClient:   mockSES,
// 		snsClient:   mockSNS,
// 		templateMap: loadTestTemplates(),
// 	}

// 	input := &Input{
// 		RecipientID:      "franchisor-001",
// 		RecipientType:    RecipientTypeFranchisor,
// 		NotificationType: TypeNewApplication,
// 		ApplicationID:    "APP-FULL-001",
// 		Priority:         "high",
// 		Metadata: map[string]interface{}{
// 			"franchiseName": "McDonald's",
// 			"seekerName":    "Jane Smith",
// 			"investment":    500000,
// 		},
// 	}

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.Equal(t, StatusSent, output.Status)
// 	assert.NotEmpty(t, output.NotificationID)
// 	assert.NotEmpty(t, output.SentAt)

// 	// Verify both email and SMS were sent
// 	assert.True(t, emailSent)
// 	assert.True(t, smsSent)

// 	// Verify timestamp format
// 	_, err = time.Parse(time.RFC3339, output.SentAt)
// 	assert.NoError(t, err)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	db, mock, err := sqlmock.New()
// 	if err != nil {
// 		b.Fatal(err)
// 	}
// 	defer db.Close()

// 	// Setup mock expectations
// 	mock.ExpectQuery(`SELECT email, phone FROM franchisors WHERE id = \$1`).
// 		WithArgs("benchmark-recipient").
// 		WillReturnRows(sqlmock.NewRows([]string{"email", "phone"}).
// 			AddRow("benchmark@example.com", "+1234567890"))

// 	mockSES := &MockSESService{
// 		SendEmailFunc: func(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error) {
// 			return &ses.SendEmailOutput{}, nil
// 		},
// 	}

// 	mockSNS := &MockSNSService{
// 		PublishFunc: func(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error) {
// 			return &sns.PublishOutput{}, nil
// 		},
// 	}

// 	config := createTestConfig()
// 	handler := &Handler{
// 		config:      config,
// 		db:          db,
// 		logger:      zaptest.NewLogger(b),
// 		sesClient:   mockSES,
// 		snsClient:   mockSNS,
// 		templateMap: loadTestTemplates(),
// 	}

// 	input := createTestInput(TypeNewApplication)
// 	input.RecipientID = "benchmark-recipient"

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_, _ = handler.execute(context.Background(), input)
// 	}

// 	if err := mock.ExpectationsWereMet(); err != nil {
// 		b.Errorf("there were unfulfilled expectations: %s", err)
// 	}
// }

// func BenchmarkHandler_RenderTemplate(b *testing.B) {
// 	template := "Application {{applicationId}} for {{franchiseName}} by {{seekerName}} with priority {{priority}}."
// 	data := map[string]interface{}{
// 		"applicationId": "APP-001",
// 		"franchiseName": "McDonald's",
// 		"seekerName":    "John Doe",
// 		"priority":      "high",
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_ = renderTemplate(template, data)
// 	}
// }

// // Helper function for test templates
// func loadTestTemplates() map[string]map[string]interface{} {
// 	return map[string]map[string]interface{}{
// 		TypeNewApplication: {
// 			"subject": "New Franchise Application Received",
// 			"body":    "Hello, you have a new application for {{applicationId}}. Priority: {{priority}}.",
// 		},
// 		TypeApplicationSubmitted: {
// 			"subject": "Application Submitted Successfully",
// 			"body":    "Thank you! Your application {{applicationId}} has been submitted.",
// 		},
// 	}
// }
