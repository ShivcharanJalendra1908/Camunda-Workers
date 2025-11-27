// internal/workers/application/validate-application-data/handler_test.go
package validateapplicationdata

import (
	"context"
	"testing"

	"camunda-workers/internal/common/logger"

	"github.com/stretchr/testify/assert"
)

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{}
}

func createValidApplicationData() map[string]interface{} {
	return map[string]interface{}{
		"personalInfo": map[string]interface{}{
			"name":  "John Doe",
			"email": "john.doe@example.com",
			"phone": "+1234567890",
		},
		"financialInfo": map[string]interface{}{
			"liquidCapital": 600000.0, // Use float64 to match JSON unmarshaling behavior
			"netWorth":      1200000.0,
			"creditScore":   750.0,
		},
		"experience": map[string]interface{}{
			"yearsInIndustry":      5.0, // Use float64
			"managementExperience": true,
		},
	}
}

func createInvalidApplicationData() map[string]interface{} {
	return map[string]interface{}{
		"personalInfo": map[string]interface{}{
			"name":  "J", // Too short
			"email": "invalid-email",
			"phone": "not-a-phone",
		},
		"financialInfo": map[string]interface{}{
			"liquidCapital": -1000.0, // Negative
			"netWorth":      "abc",   // Not a number
			// Missing creditScore for McDonald's
		},
		"experience": map[string]interface{}{
			"yearsInIndustry":      -2.0,  // Negative
			"managementExperience": "yes", // Wrong type
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
		name        string
		franchiseID string
		inputData   map[string]interface{}
		validate    func(t *testing.T, output *Output)
	}{
		{
			name:        "valid mcdonalds application",
			franchiseID: "mcdonalds",
			inputData:   createValidApplicationData(),
			validate: func(t *testing.T, output *Output) {
				assert.True(t, output.IsValid)
				assert.Empty(t, output.ValidationErrors)
				assert.NotNil(t, output.ValidatedData)

				personalInfo := output.ValidatedData["personalInfo"].(map[string]interface{})
				assert.Equal(t, "John Doe", personalInfo["name"])
				assert.Equal(t, "john.doe@example.com", personalInfo["email"])
				assert.Equal(t, "+1234567890", personalInfo["phone"])

				financialInfo := output.ValidatedData["financialInfo"].(map[string]interface{})
				assert.Equal(t, 600000, financialInfo["liquidCapital"])
				assert.Equal(t, 1200000, financialInfo["netWorth"])
				assert.Equal(t, 750, financialInfo["creditScore"])
			},
		},
		{
			name:        "valid starbucks application without credit score",
			franchiseID: "starbucks",
			inputData: func() map[string]interface{} {
				data := createValidApplicationData()
				financial := data["financialInfo"].(map[string]interface{})
				delete(financial, "creditScore")
				financial["liquidCapital"] = 400000.0
				financial["netWorth"] = 800000.0
				return data
			}(),
			validate: func(t *testing.T, output *Output) {
				assert.True(t, output.IsValid)
				assert.Empty(t, output.ValidationErrors)
			},
		},
		{
			name:        "application with minimum financials",
			franchiseID: "starbucks",
			inputData: func() map[string]interface{} {
				data := createValidApplicationData()
				financial := data["financialInfo"].(map[string]interface{})
				financial["liquidCapital"] = 300000.0
				financial["netWorth"] = 600000.0
				delete(financial, "creditScore")
				return data
			}(),
			validate: func(t *testing.T, output *Output) {
				assert.True(t, output.IsValid)
				assert.Empty(t, output.ValidationErrors)
			},
		},
		{
			name:        "application with exact minimum for mcdonalds",
			franchiseID: "mcdonalds",
			inputData: func() map[string]interface{} {
				data := createValidApplicationData()
				financial := data["financialInfo"].(map[string]interface{})
				financial["liquidCapital"] = 500000.0
				financial["netWorth"] = 1000000.0
				financial["creditScore"] = 700.0
				return data
			}(),
			validate: func(t *testing.T, output *Output) {
				assert.True(t, output.IsValid)
				assert.Empty(t, output.ValidationErrors)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(createTestConfig(), newTestLogger(t))
			input := &Input{
				ApplicationData: tt.inputData,
				FranchiseID:     tt.franchiseID,
			}

			output, err := handler.Execute(context.Background(), input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.True(t, output.IsValid)
			assert.Empty(t, output.ValidationErrors)

			if tt.validate != nil {
				tt.validate(t, output)
			}
		})
	}
}

func TestHandler_Execute_ValidationFailed(t *testing.T) {
	tests := []struct {
		name        string
		franchiseID string
		inputData   map[string]interface{}
		expectedErr string
		minErrCount int // Minimum expected errors
	}{
		{
			name:        "completely invalid data",
			franchiseID: "mcdonalds",
			inputData:   createInvalidApplicationData(),
			expectedErr: "APPLICATION_VALIDATION_FAILED",
			minErrCount: 7, // Multiple validation errors
		},
		{
			name:        "missing required sections",
			franchiseID: "mcdonalds",
			inputData:   map[string]interface{}{}, // Empty data
			expectedErr: "APPLICATION_VALIDATION_FAILED",
			minErrCount: 3, // Missing personalInfo, financialInfo, experience
		},
		{
			name:        "insufficient financials for franchise",
			franchiseID: "mcdonalds",
			inputData: func() map[string]interface{} {
				data := createValidApplicationData()
				financial := data["financialInfo"].(map[string]interface{})
				financial["liquidCapital"] = 100000.0 // Below McDonald's minimum
				financial["netWorth"] = 200000.0      // Below McDonald's minimum
				delete(financial, "creditScore")      // Missing for McDonald's
				return data
			}(),
			expectedErr: "APPLICATION_VALIDATION_FAILED",
			minErrCount: 3, // Liquid capital, net worth, credit score
		},
		{
			name:        "missing credit score for mcdonalds",
			franchiseID: "mcdonalds",
			inputData: func() map[string]interface{} {
				data := createValidApplicationData()
				financial := data["financialInfo"].(map[string]interface{})
				delete(financial, "creditScore")
				return data
			}(),
			expectedErr: "APPLICATION_VALIDATION_FAILED",
			minErrCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(createTestConfig(), newTestLogger(t))
			input := &Input{
				ApplicationData: tt.inputData,
				FranchiseID:     tt.franchiseID,
			}

			output, err := handler.Execute(context.Background(), input)

			assert.Error(t, err)
			assert.Nil(t, output)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestHandler_Execute_Timeout(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	input := &Input{
		ApplicationData: createValidApplicationData(),
		FranchiseID:     "mcdonalds",
	}

	output, err := handler.Execute(ctx, input)

	// Should still work since validation is synchronous
	assert.NoError(t, err)
	assert.NotNil(t, output)
}

// ==========================
// Unit Tests - Personal Info Validation
// ==========================

func TestHandler_ValidatePersonalInfo(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name     string
		data     map[string]interface{}
		wantErr  bool
		errCount int
		validate func(t *testing.T, result map[string]interface{})
	}{
		{
			name: "valid personal info",
			data: map[string]interface{}{
				"name":  "John O'Conner-Smith",
				"email": "test@example.com",
				"phone": "+123456789012345",
			},
			wantErr:  false,
			errCount: 0,
			validate: func(t *testing.T, result map[string]interface{}) {
				assert.Equal(t, "John O'Conner-Smith", result["name"]) // Apostrophe is kept
				assert.Equal(t, "test@example.com", result["email"])
				assert.Equal(t, "+123456789012345", result["phone"])
			},
		},
		{
			name: "invalid name - too short",
			data: map[string]interface{}{
				"name":  "J",
				"email": "test@example.com",
				"phone": "+1234567890",
			},
			wantErr:  true,
			errCount: 1,
		},
		{
			name: "invalid email format",
			data: map[string]interface{}{
				"name":  "John Doe",
				"email": "not-an-email",
				"phone": "+1234567890",
			},
			wantErr:  true,
			errCount: 1,
		},
		{
			name: "invalid phone format - letters with numbers",
			data: map[string]interface{}{
				"name":  "John Doe",
				"email": "test@example.com",
				"phone": "abc123", // Sanitizes to "123", which is too short (< 7 digits)
			},
			wantErr:  true,
			errCount: 1,
		},
		{
			name: "invalid phone format - too short",
			data: map[string]interface{}{
				"name":  "John Doe",
				"email": "test@example.com",
				"phone": "123456", // 6 digits, need minimum 7
			},
			wantErr:  true,
			errCount: 1,
		},
		{
			name:     "missing required fields",
			data:     map[string]interface{}{},
			wantErr:  true,
			errCount: 3, // name, email, phone
		},
		{
			name: "wrong data types",
			data: map[string]interface{}{
				"name":  123,  // Should be string
				"email": 456,  // Should be string
				"phone": true, // Should be string
			},
			wantErr:  true,
			errCount: 3,
		},
		{
			name: "name and phone sanitization",
			data: map[string]interface{}{
				"name":  "  John   Doe  ", // Extra spaces
				"email": "test@example.com",
				"phone": "+1 (234) 567-8900", // Formatting characters
			},
			wantErr:  false,
			errCount: 0,
			validate: func(t *testing.T, result map[string]interface{}) {
				assert.Equal(t, "John Doe", result["name"])
				assert.Equal(t, "+12345678900", result["phone"]) // Sanitized
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, errors := handler.validatePersonalInfo(tt.data)

			if tt.wantErr {
				assert.GreaterOrEqual(t, len(errors), tt.errCount)
			} else {
				assert.Empty(t, errors)
			}

			if tt.validate != nil {
				tt.validate(t, result)
			}
		})
	}
}

// ==========================
// Unit Tests - Financial Info Validation
// ==========================

func TestHandler_ValidateFinancialInfo(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name        string
		franchiseID string
		data        map[string]interface{}
		wantErr     bool
		errCount    int
	}{
		{
			name:        "valid financials for mcdonalds",
			franchiseID: "mcdonalds",
			data: map[string]interface{}{
				"liquidCapital": 600000.0,
				"netWorth":      1200000.0,
				"creditScore":   750.0,
			},
			wantErr:  false,
			errCount: 0,
		},
		{
			name:        "valid financials for starbucks without credit",
			franchiseID: "starbucks",
			data: map[string]interface{}{
				"liquidCapital": 400000.0,
				"netWorth":      800000.0,
			},
			wantErr:  false,
			errCount: 0,
		},
		{
			name:        "insufficient for mcdonalds",
			franchiseID: "mcdonalds",
			data: map[string]interface{}{
				"liquidCapital": 400000.0, // Below minimum
				"netWorth":      800000.0, // Below minimum
				"creditScore":   750.0,
			},
			wantErr:  true,
			errCount: 2, // Liquid capital and net worth below minimum
		},
		{
			name:        "missing credit score for mcdonalds",
			franchiseID: "mcdonalds",
			data: map[string]interface{}{
				"liquidCapital": 600000.0,
				"netWorth":      1200000.0,
			},
			wantErr:  true,
			errCount: 1, // Missing credit score
		},
		{
			name:        "invalid credit score range - too low",
			franchiseID: "mcdonalds",
			data: map[string]interface{}{
				"liquidCapital": 600000.0,
				"netWorth":      1200000.0,
				"creditScore":   200.0, // Too low
			},
			wantErr:  true,
			errCount: 1,
		},
		{
			name:        "invalid credit score range - too high",
			franchiseID: "mcdonalds",
			data: map[string]interface{}{
				"liquidCapital": 600000.0,
				"netWorth":      1200000.0,
				"creditScore":   900.0, // Too high
			},
			wantErr:  true,
			errCount: 1,
		},
		{
			name:        "negative values",
			franchiseID: "starbucks",
			data: map[string]interface{}{
				"liquidCapital": -1000.0,
				"netWorth":      -5000.0,
			},
			wantErr:  true,
			errCount: 2,
		},
		{
			name:        "string numbers - valid",
			franchiseID: "starbucks",
			data: map[string]interface{}{
				"liquidCapital": "300000",
				"netWorth":      "600000",
			},
			wantErr:  false,
			errCount: 0,
		},
		{
			name:        "missing required fields",
			franchiseID: "starbucks",
			data:        map[string]interface{}{},
			wantErr:     true,
			errCount:    2, // liquidCapital and netWorth
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, errors := handler.validateFinancialInfo(tt.data, tt.franchiseID)

			if tt.wantErr {
				assert.GreaterOrEqual(t, len(errors), tt.errCount)
			} else {
				assert.Empty(t, errors)
				assert.NotNil(t, result)
			}
		})
	}
}

// ==========================
// Unit Tests - Experience Validation
// ==========================

func TestHandler_ValidateExperience(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name     string
		data     map[string]interface{}
		wantErr  bool
		errCount int
		validate func(t *testing.T, result map[string]interface{})
	}{
		{
			name: "valid experience",
			data: map[string]interface{}{
				"yearsInIndustry":      5.0,
				"managementExperience": true,
			},
			wantErr:  false,
			errCount: 0,
			validate: func(t *testing.T, result map[string]interface{}) {
				assert.Equal(t, 5, result["yearsInIndustry"])
				assert.True(t, result["managementExperience"].(bool))
			},
		},
		{
			name: "zero years experience",
			data: map[string]interface{}{
				"yearsInIndustry":      0.0,
				"managementExperience": false,
			},
			wantErr:  false,
			errCount: 0,
		},
		{
			name: "negative years",
			data: map[string]interface{}{
				"yearsInIndustry":      -1.0,
				"managementExperience": true,
			},
			wantErr:  true,
			errCount: 1,
		},
		{
			name: "missing management experience defaults to false",
			data: map[string]interface{}{
				"yearsInIndustry": 5.0,
			},
			wantErr:  false,
			errCount: 0,
			validate: func(t *testing.T, result map[string]interface{}) {
				assert.False(t, result["managementExperience"].(bool)) // Defaults to false
			},
		},
		{
			name: "string number for years - valid",
			data: map[string]interface{}{
				"yearsInIndustry":      "3",
				"managementExperience": true,
			},
			wantErr:  false,
			errCount: 0,
		},
		{
			name: "wrong type for management experience",
			data: map[string]interface{}{
				"yearsInIndustry":      5.0,
				"managementExperience": "yes", // Should be boolean
			},
			wantErr:  true,
			errCount: 1,
		},
		{
			name: "missing years in industry",
			data: map[string]interface{}{
				"managementExperience": true,
			},
			wantErr:  true,
			errCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, errors := handler.validateExperience(tt.data)

			if tt.wantErr {
				assert.GreaterOrEqual(t, len(errors), tt.errCount)
			} else {
				assert.Empty(t, errors)
			}

			if tt.validate != nil {
				tt.validate(t, result)
			}
		})
	}
}

// ==========================
// Unit Tests - ParseInt Helper
// ==========================

func TestHandler_ParseInt(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name    string
		input   interface{}
		want    int
		wantErr bool
	}{
		{"float64", 123.0, 123, false},
		{"float64 decimal", 123.45, 123, false},
		{"string number", "456", 456, false},
		{"string with spaces", " 789 ", 789, false},
		{"invalid string", "abc", 0, true},
		{"bool", true, 0, true},
		{"nil", nil, 0, true},
		{"negative float", -50.0, -50, false},
		{"negative string", "-100", -100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := handler.parseInt(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	t.Run("empty application data", func(t *testing.T) {
		input := &Input{
			ApplicationData: map[string]interface{}{},
			FranchiseID:     "mcdonalds",
		}

		output, err := handler.Execute(context.Background(), input)

		assert.Error(t, err)
		assert.Nil(t, output)
		assert.Contains(t, err.Error(), "APPLICATION_VALIDATION_FAILED")
	})

	t.Run("nil application data", func(t *testing.T) {
		input := &Input{
			ApplicationData: nil,
			FranchiseID:     "mcdonalds",
		}

		output, err := handler.Execute(context.Background(), input)

		assert.Error(t, err)
		assert.Nil(t, output)
	})

	t.Run("unknown franchise ID", func(t *testing.T) {
		input := &Input{
			ApplicationData: createValidApplicationData(),
			FranchiseID:     "unknown-franchise",
		}

		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err) // Should still validate basic rules
		assert.True(t, output.IsValid)
	})

	t.Run("malformed nested data", func(t *testing.T) {
		input := &Input{
			ApplicationData: map[string]interface{}{
				"personalInfo":  "not-a-map", // Should be map
				"financialInfo": map[string]interface{}{},
				"experience":    nil,
			},
			FranchiseID: "mcdonalds",
		}

		output, err := handler.Execute(context.Background(), input)

		assert.Error(t, err)
		assert.Nil(t, output)
	})

	t.Run("very large numbers", func(t *testing.T) {
		input := &Input{
			ApplicationData: map[string]interface{}{
				"personalInfo": map[string]interface{}{
					"name":  "Test Name",
					"email": "test@example.com",
					"phone": "+1234567890",
				},
				"financialInfo": map[string]interface{}{
					"liquidCapital": 999999999.0,
					"netWorth":      9999999999.0,
					"creditScore":   850.0,
				},
				"experience": map[string]interface{}{
					"yearsInIndustry":      50.0,
					"managementExperience": true,
				},
			},
			FranchiseID: "mcdonalds",
		}

		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.True(t, output.IsValid)
	})

	t.Run("special characters in name", func(t *testing.T) {
		input := &Input{
			ApplicationData: map[string]interface{}{
				"personalInfo": map[string]interface{}{
					"name":  "John@Doe#Test$",
					"email": "test@example.com",
					"phone": "+1234567890",
				},
				"financialInfo": map[string]interface{}{
					"liquidCapital": 600000.0,
					"netWorth":      1200000.0,
					"creditScore":   750.0,
				},
				"experience": map[string]interface{}{
					"yearsInIndustry":      5.0,
					"managementExperience": true,
				},
			},
			FranchiseID: "mcdonalds",
		}

		_, err := handler.Execute(context.Background(), input)

		// Should sanitize and pass if result is valid after sanitization
		if err != nil {
			assert.Contains(t, err.Error(), "APPLICATION_VALIDATION_FAILED")
		}
	})
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	// Test complete valid workflow
	validInput := &Input{
		ApplicationData: createValidApplicationData(),
		FranchiseID:     "mcdonalds",
	}

	output, err := handler.Execute(context.Background(), validInput)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.True(t, output.IsValid)
	assert.Empty(t, output.ValidationErrors)

	// Verify all data is properly validated and returned
	assert.NotEmpty(t, output.ValidatedData["personalInfo"])
	assert.NotEmpty(t, output.ValidatedData["financialInfo"])
	assert.NotEmpty(t, output.ValidatedData["experience"])

	personal := output.ValidatedData["personalInfo"].(map[string]interface{})
	financial := output.ValidatedData["financialInfo"].(map[string]interface{})
	experience := output.ValidatedData["experience"].(map[string]interface{})

	assert.Equal(t, "John Doe", personal["name"])
	assert.Equal(t, "john.doe@example.com", personal["email"])
	assert.Equal(t, "+1234567890", personal["phone"])
	assert.Equal(t, 600000, financial["liquidCapital"])
	assert.Equal(t, 1200000, financial["netWorth"])
	assert.Equal(t, 750, financial["creditScore"])
	assert.Equal(t, 5, experience["yearsInIndustry"])
	assert.True(t, experience["managementExperience"].(bool))
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))

	validInput := &Input{
		ApplicationData: createValidApplicationData(),
		FranchiseID:     "mcdonalds",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.Execute(context.Background(), validInput)
	}
}

func BenchmarkHandler_ValidatePersonalInfo(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))

	personalData := map[string]interface{}{
		"name":  "John Doe",
		"email": "john.doe@example.com",
		"phone": "+1234567890",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.validatePersonalInfo(personalData)
	}
}

func BenchmarkHandler_ValidateFinancialInfo(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))

	financialData := map[string]interface{}{
		"liquidCapital": 600000.0,
		"netWorth":      1200000.0,
		"creditScore":   750.0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.validateFinancialInfo(financialData, "mcdonalds")
	}
}

func BenchmarkHandler_ValidateExperience(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))

	experienceData := map[string]interface{}{
		"yearsInIndustry":      5.0,
		"managementExperience": true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.validateExperience(experienceData)
	}
}

// // internal/workers/application/validate-application-data/handler_test.go
// package validateapplicationdata

// import (
// 	"context"
// 	"testing"

// 	"github.com/stretchr/testify/assert"
// 	"go.uber.org/zap/zaptest"
// )

// // ==========================
// // Test Helper Functions
// // ==========================

// func createTestConfig() *Config {
// 	return &Config{}
// }

// func createValidApplicationData() map[string]interface{} {
// 	return map[string]interface{}{
// 		"personalInfo": map[string]interface{}{
// 			"name":  "John Doe",
// 			"email": "john.doe@example.com",
// 			"phone": "+1234567890",
// 		},
// 		"financialInfo": map[string]interface{}{
// 			"liquidCapital": 600000.0, // Use float64 to match JSON unmarshaling behavior
// 			"netWorth":      1200000.0,
// 			"creditScore":   750.0,
// 		},
// 		"experience": map[string]interface{}{
// 			"yearsInIndustry":      5.0, // Use float64
// 			"managementExperience": true,
// 		},
// 	}
// }

// func createInvalidApplicationData() map[string]interface{} {
// 	return map[string]interface{}{
// 		"personalInfo": map[string]interface{}{
// 			"name":  "J", // Too short
// 			"email": "invalid-email",
// 			"phone": "not-a-phone",
// 		},
// 		"financialInfo": map[string]interface{}{
// 			"liquidCapital": -1000.0, // Negative
// 			"netWorth":      "abc",   // Not a number
// 			// Missing creditScore for McDonald's
// 		},
// 		"experience": map[string]interface{}{
// 			"yearsInIndustry":      -2.0,  // Negative
// 			"managementExperience": "yes", // Wrong type
// 		},
// 	}
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name        string
// 		franchiseID string
// 		inputData   map[string]interface{}
// 		validate    func(t *testing.T, output *Output)
// 	}{
// 		{
// 			name:        "valid mcdonalds application",
// 			franchiseID: "mcdonalds",
// 			inputData:   createValidApplicationData(),
// 			validate: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Empty(t, output.ValidationErrors)
// 				assert.NotNil(t, output.ValidatedData)

// 				personalInfo := output.ValidatedData["personalInfo"].(map[string]interface{})
// 				assert.Equal(t, "John Doe", personalInfo["name"])
// 				assert.Equal(t, "john.doe@example.com", personalInfo["email"])
// 				assert.Equal(t, "+1234567890", personalInfo["phone"])

// 				financialInfo := output.ValidatedData["financialInfo"].(map[string]interface{})
// 				assert.Equal(t, 600000, financialInfo["liquidCapital"])
// 				assert.Equal(t, 1200000, financialInfo["netWorth"])
// 				assert.Equal(t, 750, financialInfo["creditScore"])
// 			},
// 		},
// 		{
// 			name:        "valid starbucks application without credit score",
// 			franchiseID: "starbucks",
// 			inputData: func() map[string]interface{} {
// 				data := createValidApplicationData()
// 				financial := data["financialInfo"].(map[string]interface{})
// 				delete(financial, "creditScore")
// 				financial["liquidCapital"] = 400000.0
// 				financial["netWorth"] = 800000.0
// 				return data
// 			}(),
// 			validate: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Empty(t, output.ValidationErrors)
// 			},
// 		},
// 		{
// 			name:        "application with minimum financials",
// 			franchiseID: "starbucks",
// 			inputData: func() map[string]interface{} {
// 				data := createValidApplicationData()
// 				financial := data["financialInfo"].(map[string]interface{})
// 				financial["liquidCapital"] = 300000.0
// 				financial["netWorth"] = 600000.0
// 				delete(financial, "creditScore")
// 				return data
// 			}(),
// 			validate: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Empty(t, output.ValidationErrors)
// 			},
// 		},
// 		{
// 			name:        "application with exact minimum for mcdonalds",
// 			franchiseID: "mcdonalds",
// 			inputData: func() map[string]interface{} {
// 				data := createValidApplicationData()
// 				financial := data["financialInfo"].(map[string]interface{})
// 				financial["liquidCapital"] = 500000.0
// 				financial["netWorth"] = 1000000.0
// 				financial["creditScore"] = 700.0
// 				return data
// 			}(),
// 			validate: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Empty(t, output.ValidationErrors)
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))
// 			input := &Input{
// 				ApplicationData: tt.inputData,
// 				FranchiseID:     tt.franchiseID,
// 			}

// 			output, err := handler.execute(context.Background(), input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.True(t, output.IsValid)
// 			assert.Empty(t, output.ValidationErrors)

// 			if tt.validate != nil {
// 				tt.validate(t, output)
// 			}
// 		})
// 	}
// }

// func TestHandler_Execute_ValidationFailed(t *testing.T) {
// 	tests := []struct {
// 		name        string
// 		franchiseID string
// 		inputData   map[string]interface{}
// 		expectedErr string
// 		minErrCount int // Minimum expected errors
// 	}{
// 		{
// 			name:        "completely invalid data",
// 			franchiseID: "mcdonalds",
// 			inputData:   createInvalidApplicationData(),
// 			expectedErr: "APPLICATION_VALIDATION_FAILED",
// 			minErrCount: 7, // Multiple validation errors
// 		},
// 		{
// 			name:        "missing required sections",
// 			franchiseID: "mcdonalds",
// 			inputData:   map[string]interface{}{}, // Empty data
// 			expectedErr: "APPLICATION_VALIDATION_FAILED",
// 			minErrCount: 3, // Missing personalInfo, financialInfo, experience
// 		},
// 		{
// 			name:        "insufficient financials for franchise",
// 			franchiseID: "mcdonalds",
// 			inputData: func() map[string]interface{} {
// 				data := createValidApplicationData()
// 				financial := data["financialInfo"].(map[string]interface{})
// 				financial["liquidCapital"] = 100000.0 // Below McDonald's minimum
// 				financial["netWorth"] = 200000.0      // Below McDonald's minimum
// 				delete(financial, "creditScore")      // Missing for McDonald's
// 				return data
// 			}(),
// 			expectedErr: "APPLICATION_VALIDATION_FAILED",
// 			minErrCount: 3, // Liquid capital, net worth, credit score
// 		},
// 		{
// 			name:        "missing credit score for mcdonalds",
// 			franchiseID: "mcdonalds",
// 			inputData: func() map[string]interface{} {
// 				data := createValidApplicationData()
// 				financial := data["financialInfo"].(map[string]interface{})
// 				delete(financial, "creditScore")
// 				return data
// 			}(),
// 			expectedErr: "APPLICATION_VALIDATION_FAILED",
// 			minErrCount: 1,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))
// 			input := &Input{
// 				ApplicationData: tt.inputData,
// 				FranchiseID:     tt.franchiseID,
// 			}

// 			output, err := handler.execute(context.Background(), input)

// 			assert.Error(t, err)
// 			assert.Nil(t, output)
// 			assert.Contains(t, err.Error(), tt.expectedErr)
// 		})
// 	}
// }

// func TestHandler_Execute_Timeout(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	// Create a context that's already cancelled
// 	ctx, cancel := context.WithCancel(context.Background())
// 	cancel()

// 	input := &Input{
// 		ApplicationData: createValidApplicationData(),
// 		FranchiseID:     "mcdonalds",
// 	}

// 	output, err := handler.execute(ctx, input)

// 	// Should still work since validation is synchronous
// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// }

// // ==========================
// // Unit Tests - Personal Info Validation
// // ==========================

// func TestHandler_ValidatePersonalInfo(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		data     map[string]interface{}
// 		wantErr  bool
// 		errCount int
// 		validate func(t *testing.T, result map[string]interface{})
// 	}{
// 		{
// 			name: "valid personal info",
// 			data: map[string]interface{}{
// 				"name":  "John O'Conner-Smith",
// 				"email": "test@example.com",
// 				"phone": "+123456789012345",
// 			},
// 			wantErr:  false,
// 			errCount: 0,
// 			validate: func(t *testing.T, result map[string]interface{}) {
// 				assert.Equal(t, "John O'Conner-Smith", result["name"]) // Apostrophe is kept
// 				assert.Equal(t, "test@example.com", result["email"])
// 				assert.Equal(t, "+123456789012345", result["phone"])
// 			},
// 		},
// 		{
// 			name: "invalid name - too short",
// 			data: map[string]interface{}{
// 				"name":  "J",
// 				"email": "test@example.com",
// 				"phone": "+1234567890",
// 			},
// 			wantErr:  true,
// 			errCount: 1,
// 		},
// 		{
// 			name: "invalid email format",
// 			data: map[string]interface{}{
// 				"name":  "John Doe",
// 				"email": "not-an-email",
// 				"phone": "+1234567890",
// 			},
// 			wantErr:  true,
// 			errCount: 1,
// 		},
// 		{
// 			name: "invalid phone format - letters with numbers",
// 			data: map[string]interface{}{
// 				"name":  "John Doe",
// 				"email": "test@example.com",
// 				"phone": "abc123", // Sanitizes to "123", which is too short (< 7 digits)
// 			},
// 			wantErr:  true,
// 			errCount: 1,
// 		},
// 		{
// 			name: "invalid phone format - too short",
// 			data: map[string]interface{}{
// 				"name":  "John Doe",
// 				"email": "test@example.com",
// 				"phone": "123456", // 6 digits, need minimum 7
// 			},
// 			wantErr:  true,
// 			errCount: 1,
// 		},
// 		{
// 			name:     "missing required fields",
// 			data:     map[string]interface{}{},
// 			wantErr:  true,
// 			errCount: 3, // name, email, phone
// 		},
// 		{
// 			name: "wrong data types",
// 			data: map[string]interface{}{
// 				"name":  123,  // Should be string
// 				"email": 456,  // Should be string
// 				"phone": true, // Should be string
// 			},
// 			wantErr:  true,
// 			errCount: 3,
// 		},
// 		{
// 			name: "name and phone sanitization",
// 			data: map[string]interface{}{
// 				"name":  "  John   Doe  ", // Extra spaces
// 				"email": "test@example.com",
// 				"phone": "+1 (234) 567-8900", // Formatting characters
// 			},
// 			wantErr:  false,
// 			errCount: 0,
// 			validate: func(t *testing.T, result map[string]interface{}) {
// 				assert.Equal(t, "John Doe", result["name"])
// 				assert.Equal(t, "+12345678900", result["phone"]) // Sanitized
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result, errors := handler.validatePersonalInfo(tt.data)

// 			if tt.wantErr {
// 				assert.GreaterOrEqual(t, len(errors), tt.errCount)
// 			} else {
// 				assert.Empty(t, errors)
// 			}

// 			if tt.validate != nil {
// 				tt.validate(t, result)
// 			}
// 		})
// 	}
// }

// // ==========================
// // Unit Tests - Financial Info Validation
// // ==========================

// func TestHandler_ValidateFinancialInfo(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name        string
// 		franchiseID string
// 		data        map[string]interface{}
// 		wantErr     bool
// 		errCount    int
// 	}{
// 		{
// 			name:        "valid financials for mcdonalds",
// 			franchiseID: "mcdonalds",
// 			data: map[string]interface{}{
// 				"liquidCapital": 600000.0,
// 				"netWorth":      1200000.0,
// 				"creditScore":   750.0,
// 			},
// 			wantErr:  false,
// 			errCount: 0,
// 		},
// 		{
// 			name:        "valid financials for starbucks without credit",
// 			franchiseID: "starbucks",
// 			data: map[string]interface{}{
// 				"liquidCapital": 400000.0,
// 				"netWorth":      800000.0,
// 			},
// 			wantErr:  false,
// 			errCount: 0,
// 		},
// 		{
// 			name:        "insufficient for mcdonalds",
// 			franchiseID: "mcdonalds",
// 			data: map[string]interface{}{
// 				"liquidCapital": 400000.0, // Below minimum
// 				"netWorth":      800000.0, // Below minimum
// 				"creditScore":   750.0,
// 			},
// 			wantErr:  true,
// 			errCount: 2, // Liquid capital and net worth below minimum
// 		},
// 		{
// 			name:        "missing credit score for mcdonalds",
// 			franchiseID: "mcdonalds",
// 			data: map[string]interface{}{
// 				"liquidCapital": 600000.0,
// 				"netWorth":      1200000.0,
// 			},
// 			wantErr:  true,
// 			errCount: 1, // Missing credit score
// 		},
// 		{
// 			name:        "invalid credit score range - too low",
// 			franchiseID: "mcdonalds",
// 			data: map[string]interface{}{
// 				"liquidCapital": 600000.0,
// 				"netWorth":      1200000.0,
// 				"creditScore":   200.0, // Too low
// 			},
// 			wantErr:  true,
// 			errCount: 1,
// 		},
// 		{
// 			name:        "invalid credit score range - too high",
// 			franchiseID: "mcdonalds",
// 			data: map[string]interface{}{
// 				"liquidCapital": 600000.0,
// 				"netWorth":      1200000.0,
// 				"creditScore":   900.0, // Too high
// 			},
// 			wantErr:  true,
// 			errCount: 1,
// 		},
// 		{
// 			name:        "negative values",
// 			franchiseID: "starbucks",
// 			data: map[string]interface{}{
// 				"liquidCapital": -1000.0,
// 				"netWorth":      -5000.0,
// 			},
// 			wantErr:  true,
// 			errCount: 2,
// 		},
// 		{
// 			name:        "string numbers - valid",
// 			franchiseID: "starbucks",
// 			data: map[string]interface{}{
// 				"liquidCapital": "300000",
// 				"netWorth":      "600000",
// 			},
// 			wantErr:  false,
// 			errCount: 0,
// 		},
// 		{
// 			name:        "missing required fields",
// 			franchiseID: "starbucks",
// 			data:        map[string]interface{}{},
// 			wantErr:     true,
// 			errCount:    2, // liquidCapital and netWorth
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result, errors := handler.validateFinancialInfo(tt.data, tt.franchiseID)

// 			if tt.wantErr {
// 				assert.GreaterOrEqual(t, len(errors), tt.errCount)
// 			} else {
// 				assert.Empty(t, errors)
// 				assert.NotNil(t, result)
// 			}
// 		})
// 	}
// }

// // ==========================
// // Unit Tests - Experience Validation
// // ==========================

// func TestHandler_ValidateExperience(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		data     map[string]interface{}
// 		wantErr  bool
// 		errCount int
// 		validate func(t *testing.T, result map[string]interface{})
// 	}{
// 		{
// 			name: "valid experience",
// 			data: map[string]interface{}{
// 				"yearsInIndustry":      5.0,
// 				"managementExperience": true,
// 			},
// 			wantErr:  false,
// 			errCount: 0,
// 			validate: func(t *testing.T, result map[string]interface{}) {
// 				assert.Equal(t, 5, result["yearsInIndustry"])
// 				assert.True(t, result["managementExperience"].(bool))
// 			},
// 		},
// 		{
// 			name: "zero years experience",
// 			data: map[string]interface{}{
// 				"yearsInIndustry":      0.0,
// 				"managementExperience": false,
// 			},
// 			wantErr:  false,
// 			errCount: 0,
// 		},
// 		{
// 			name: "negative years",
// 			data: map[string]interface{}{
// 				"yearsInIndustry":      -1.0,
// 				"managementExperience": true,
// 			},
// 			wantErr:  true,
// 			errCount: 1,
// 		},
// 		{
// 			name: "missing management experience defaults to false",
// 			data: map[string]interface{}{
// 				"yearsInIndustry": 5.0,
// 			},
// 			wantErr:  false,
// 			errCount: 0,
// 			validate: func(t *testing.T, result map[string]interface{}) {
// 				assert.False(t, result["managementExperience"].(bool)) // Defaults to false
// 			},
// 		},
// 		{
// 			name: "string number for years - valid",
// 			data: map[string]interface{}{
// 				"yearsInIndustry":      "3",
// 				"managementExperience": true,
// 			},
// 			wantErr:  false,
// 			errCount: 0,
// 		},
// 		{
// 			name: "wrong type for management experience",
// 			data: map[string]interface{}{
// 				"yearsInIndustry":      5.0,
// 				"managementExperience": "yes", // Should be boolean
// 			},
// 			wantErr:  true,
// 			errCount: 1,
// 		},
// 		{
// 			name: "missing years in industry",
// 			data: map[string]interface{}{
// 				"managementExperience": true,
// 			},
// 			wantErr:  true,
// 			errCount: 1,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result, errors := handler.validateExperience(tt.data)

// 			if tt.wantErr {
// 				assert.GreaterOrEqual(t, len(errors), tt.errCount)
// 			} else {
// 				assert.Empty(t, errors)
// 			}

// 			if tt.validate != nil {
// 				tt.validate(t, result)
// 			}
// 		})
// 	}
// }

// // ==========================
// // Unit Tests - ParseInt Helper
// // ==========================

// func TestHandler_ParseInt(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name    string
// 		input   interface{}
// 		want    int
// 		wantErr bool
// 	}{
// 		{"float64", 123.0, 123, false},
// 		{"float64 decimal", 123.45, 123, false},
// 		{"string number", "456", 456, false},
// 		{"string with spaces", " 789 ", 789, false},
// 		{"invalid string", "abc", 0, true},
// 		{"bool", true, 0, true},
// 		{"nil", nil, 0, true},
// 		{"negative float", -50.0, -50, false},
// 		{"negative string", "-100", -100, false},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result, err := handler.parseInt(tt.input)

// 			if tt.wantErr {
// 				assert.Error(t, err)
// 			} else {
// 				assert.NoError(t, err)
// 				assert.Equal(t, tt.want, result)
// 			}
// 		})
// 	}
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	t.Run("empty application data", func(t *testing.T) {
// 		input := &Input{
// 			ApplicationData: map[string]interface{}{},
// 			FranchiseID:     "mcdonalds",
// 		}

// 		output, err := handler.execute(context.Background(), input)

// 		assert.Error(t, err)
// 		assert.Nil(t, output)
// 		assert.Contains(t, err.Error(), "APPLICATION_VALIDATION_FAILED")
// 	})

// 	t.Run("nil application data", func(t *testing.T) {
// 		input := &Input{
// 			ApplicationData: nil,
// 			FranchiseID:     "mcdonalds",
// 		}

// 		output, err := handler.execute(context.Background(), input)

// 		assert.Error(t, err)
// 		assert.Nil(t, output)
// 	})

// 	t.Run("unknown franchise ID", func(t *testing.T) {
// 		input := &Input{
// 			ApplicationData: createValidApplicationData(),
// 			FranchiseID:     "unknown-franchise",
// 		}

// 		output, err := handler.execute(context.Background(), input)

// 		assert.NoError(t, err) // Should still validate basic rules
// 		assert.True(t, output.IsValid)
// 	})

// 	t.Run("malformed nested data", func(t *testing.T) {
// 		input := &Input{
// 			ApplicationData: map[string]interface{}{
// 				"personalInfo":  "not-a-map", // Should be map
// 				"financialInfo": map[string]interface{}{},
// 				"experience":    nil,
// 			},
// 			FranchiseID: "mcdonalds",
// 		}

// 		output, err := handler.execute(context.Background(), input)

// 		assert.Error(t, err)
// 		assert.Nil(t, output)
// 	})

// 	t.Run("very large numbers", func(t *testing.T) {
// 		input := &Input{
// 			ApplicationData: map[string]interface{}{
// 				"personalInfo": map[string]interface{}{
// 					"name":  "Test Name",
// 					"email": "test@example.com",
// 					"phone": "+1234567890",
// 				},
// 				"financialInfo": map[string]interface{}{
// 					"liquidCapital": 999999999.0,
// 					"netWorth":      9999999999.0,
// 					"creditScore":   850.0,
// 				},
// 				"experience": map[string]interface{}{
// 					"yearsInIndustry":      50.0,
// 					"managementExperience": true,
// 				},
// 			},
// 			FranchiseID: "mcdonalds",
// 		}

// 		output, err := handler.execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.True(t, output.IsValid)
// 	})

// 	t.Run("special characters in name", func(t *testing.T) {
// 		input := &Input{
// 			ApplicationData: map[string]interface{}{
// 				"personalInfo": map[string]interface{}{
// 					"name":  "John@Doe#Test$",
// 					"email": "test@example.com",
// 					"phone": "+1234567890",
// 				},
// 				"financialInfo": map[string]interface{}{
// 					"liquidCapital": 600000.0,
// 					"netWorth":      1200000.0,
// 					"creditScore":   750.0,
// 				},
// 				"experience": map[string]interface{}{
// 					"yearsInIndustry":      5.0,
// 					"managementExperience": true,
// 				},
// 			},
// 			FranchiseID: "mcdonalds",
// 		}

// 		_, err := handler.execute(context.Background(), input)

// 		// Should sanitize and pass if result is valid after sanitization
// 		if err != nil {
// 			assert.Contains(t, err.Error(), "APPLICATION_VALIDATION_FAILED")
// 		}
// 	})
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	// Test complete valid workflow
// 	validInput := &Input{
// 		ApplicationData: createValidApplicationData(),
// 		FranchiseID:     "mcdonalds",
// 	}

// 	output, err := handler.execute(context.Background(), validInput)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.True(t, output.IsValid)
// 	assert.Empty(t, output.ValidationErrors)

// 	// Verify all data is properly validated and returned
// 	assert.NotEmpty(t, output.ValidatedData["personalInfo"])
// 	assert.NotEmpty(t, output.ValidatedData["financialInfo"])
// 	assert.NotEmpty(t, output.ValidatedData["experience"])

// 	personal := output.ValidatedData["personalInfo"].(map[string]interface{})
// 	financial := output.ValidatedData["financialInfo"].(map[string]interface{})
// 	experience := output.ValidatedData["experience"].(map[string]interface{})

// 	assert.Equal(t, "John Doe", personal["name"])
// 	assert.Equal(t, "john.doe@example.com", personal["email"])
// 	assert.Equal(t, "+1234567890", personal["phone"])
// 	assert.Equal(t, 600000, financial["liquidCapital"])
// 	assert.Equal(t, 1200000, financial["netWorth"])
// 	assert.Equal(t, 750, financial["creditScore"])
// 	assert.Equal(t, 5, experience["yearsInIndustry"])
// 	assert.True(t, experience["managementExperience"].(bool))
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	validInput := &Input{
// 		ApplicationData: createValidApplicationData(),
// 		FranchiseID:     "mcdonalds",
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_, _ = handler.execute(context.Background(), validInput)
// 	}
// }

// func BenchmarkHandler_ValidatePersonalInfo(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	personalData := map[string]interface{}{
// 		"name":  "John Doe",
// 		"email": "john.doe@example.com",
// 		"phone": "+1234567890",
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_, _ = handler.validatePersonalInfo(personalData)
// 	}
// }

// func BenchmarkHandler_ValidateFinancialInfo(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	financialData := map[string]interface{}{
// 		"liquidCapital": 600000.0,
// 		"netWorth":      1200000.0,
// 		"creditScore":   750.0,
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_, _ = handler.validateFinancialInfo(financialData, "mcdonalds")
// 	}
// }

// func BenchmarkHandler_ValidateExperience(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	experienceData := map[string]interface{}{
// 		"yearsInIndustry":      5.0,
// 		"managementExperience": true,
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_, _ = handler.validateExperience(experienceData)
// 	}
// }
