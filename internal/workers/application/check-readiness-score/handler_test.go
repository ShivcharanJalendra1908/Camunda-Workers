// internal/workers/application/check-readiness-score/handler_test.go
package checkreadinessscore

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

func createTestInput(userID string, applicationData map[string]interface{}) *Input {
	return &Input{
		UserID:          userID,
		ApplicationData: applicationData,
	}
}

func createHighScoreApplicationData() map[string]interface{} {
	return map[string]interface{}{
		"liquidCapital":        1000000,
		"netWorth":             2000000,
		"creditScore":          750,
		"yearsInIndustry":      10,
		"managementExperience": true,
		"businessOwnership":    true,
		"timeAvailability":     40,
		"relocationWilling":    true,
		"categoryMatch":        true,
		"skillAlignment":       true,
		"locationMatch":        true,
	}
}

func createMediumScoreApplicationData() map[string]interface{} {
	return map[string]interface{}{
		"liquidCapital":        500000,
		"netWorth":             750000,
		"creditScore":          650,
		"yearsInIndustry":      3,
		"managementExperience": true,
		"businessOwnership":    false,
		"timeAvailability":     25,
		"relocationWilling":    false,
		"categoryMatch":        true,
		"skillAlignment":       false,
		"locationMatch":        true,
	}
}

func createLowScoreApplicationData() map[string]interface{} {
	return map[string]interface{}{
		"liquidCapital":        50000,
		"netWorth":             100000,
		"creditScore":          550,
		"yearsInIndustry":      0,
		"managementExperience": false,
		"businessOwnership":    false,
		"timeAvailability":     5,
		"relocationWilling":    false,
		"categoryMatch":        false,
		"skillAlignment":       false,
		"locationMatch":        false,
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
		name              string
		input             *Input
		expectedScore     int
		expectedLevel     string
		expectedBreakdown ScoreBreakdown
		validateOutput    func(t *testing.T, output *Output)
	}{
		{
			name:          "excellent qualification level",
			input:         createTestInput("user-001", createHighScoreApplicationData()),
			expectedScore: 100, // 40+30+30 + 40+30+30 + 50+50 + 40+30+30 = 100
			expectedLevel: "excellent",
			expectedBreakdown: ScoreBreakdown{
				Financial:     100,
				Experience:    100,
				Commitment:    100,
				Compatibility: 100,
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "excellent", output.QualificationLevel)
				assert.True(t, output.ReadinessScore >= 81)
				assert.Equal(t, 100, output.ScoreBreakdown.Financial)
				assert.Equal(t, 100, output.ScoreBreakdown.Experience)
				assert.Equal(t, 100, output.ScoreBreakdown.Commitment)
				assert.Equal(t, 100, output.ScoreBreakdown.Compatibility)
			},
		},
		{
			name: "high qualification level",
			input: createTestInput("user-002", map[string]interface{}{
				"liquidCapital":        750000,
				"netWorth":             1500000,
				"creditScore":          720,
				"yearsInIndustry":      7,
				"managementExperience": true,
				"businessOwnership":    false,
				"timeAvailability":     35,
				"relocationWilling":    true,
				"categoryMatch":        true,
				"skillAlignment":       true,
				"locationMatch":        false,
			}),
			expectedScore: 75, // 30+20+30 + 30+30+0 + 30+50 + 40+30+0 = ~75
			expectedLevel: "high",
			expectedBreakdown: ScoreBreakdown{
				Financial:     80,
				Experience:    60,
				Commitment:    80,
				Compatibility: 70,
			},
		},
		{
			name:          "medium qualification level",
			input:         createTestInput("user-003", createMediumScoreApplicationData()),
			expectedScore: 55, // 30+10+20 + 20+30+0 + 30+0 + 40+0+30 = ~55
			expectedLevel: "medium",
			expectedBreakdown: ScoreBreakdown{
				Financial:     60,
				Experience:    50,
				Commitment:    30,
				Compatibility: 70,
			},
		},
		{
			name:          "low qualification level",
			input:         createTestInput("user-004", createLowScoreApplicationData()),
			expectedScore: 15, // 0+0+10 + 0+0+0 + 0+0 + 0+0+0 = ~15
			expectedLevel: "low",
			expectedBreakdown: ScoreBreakdown{
				Financial:     10,
				Experience:    0,
				Commitment:    0,
				Compatibility: 0,
			},
		},
		{
			name: "minimal application data",
			input: createTestInput("user-005", map[string]interface{}{
				"liquidCapital": 100000,
				"creditScore":   600,
			}),
			expectedScore: 20, // 10+0+20 + 0+0+0 + 0+0 + 0+0+0 = ~20
			expectedLevel: "low",
			expectedBreakdown: ScoreBreakdown{
				Financial:     30,
				Experience:    0,
				Commitment:    0,
				Compatibility: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := createTestConfig()
			handler := NewHandler(config, newTestLogger(t))

			output, err := handler.Execute(context.Background(), tt.input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedLevel, output.QualificationLevel)
			assert.Equal(t, tt.expectedBreakdown, output.ScoreBreakdown)

			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}
		})
	}
}

func TestHandler_Execute_EmptyApplicationData(t *testing.T) {
	config := createTestConfig()
	handler := NewHandler(config, newTestLogger(t))

	input := createTestInput("user-empty", map[string]interface{}{})
	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, "low", output.QualificationLevel)
	assert.Equal(t, 0, output.ReadinessScore)
	assert.Equal(t, ScoreBreakdown{0, 0, 0, 0}, output.ScoreBreakdown)
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_CalculateFinancialReadiness(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name     string
		data     map[string]interface{}
		expected int
	}{
		{
			name: "excellent financials",
			data: map[string]interface{}{
				"liquidCapital": 1500000,
				"netWorth":      2500000,
				"creditScore":   800,
			},
			expected: 100, // 40 + 30 + 30
		},
		{
			name: "good financials",
			data: map[string]interface{}{
				"liquidCapital": 750000,
				"netWorth":      1500000,
				"creditScore":   720,
			},
			expected: 80, // 30 + 20 + 30
		},
		{
			name: "average financials",
			data: map[string]interface{}{
				"liquidCapital": 300000,
				"netWorth":      750000,
				"creditScore":   650,
			},
			expected: 50, // 20 + 10 + 20
		},
		{
			name: "poor_financials",
			data: map[string]interface{}{
				"liquidCapital": 50000,  // 0 points (below 100k threshold)
				"netWorth":      100000, // 0 points (below 500k threshold)
				"creditScore":   550,    // 10 points (500-599 range)
			},
			expected: 10, // 0 + 0 + 10 = 10 ✓
		},
		{
			name: "missing financial data",
			data: map[string]interface{}{
				"creditScore": 700,
			},
			expected: 30, // 0 + 0 + 30
		},
		{
			name:     "no financial data",
			data:     map[string]interface{}{},
			expected: 0,
		},
		{
			name: "string number values",
			data: map[string]interface{}{
				"liquidCapital": "1000000",
				"netWorth":      "2000000",
				"creditScore":   "750",
			},
			expected: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.calculateFinancialReadiness(tt.data)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_CalculateExperience(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name     string
		data     map[string]interface{}
		expected int
	}{
		{
			name: "extensive experience",
			data: map[string]interface{}{
				"yearsInIndustry":      15,
				"managementExperience": true,
				"businessOwnership":    true,
			},
			expected: 100, // 40 + 30 + 30
		},
		{
			name: "good experience",
			data: map[string]interface{}{
				"yearsInIndustry":      7,
				"managementExperience": true,
				"businessOwnership":    false,
			},
			expected: 60, // 30 + 30 + 0
		},
		{
			name: "some experience",
			data: map[string]interface{}{
				"yearsInIndustry":      3,
				"managementExperience": false,
				"businessOwnership":    true,
			},
			expected: 50, // 20 + 0 + 30
		},
		{
			name: "minimal experience",
			data: map[string]interface{}{
				"yearsInIndustry":      1,
				"managementExperience": false,
				"businessOwnership":    false,
			},
			expected: 10, // 10 + 0 + 0
		},
		{
			name: "no experience",
			data: map[string]interface{}{
				"yearsInIndustry": 0,
			},
			expected: 0,
		},
		{
			name:     "missing experience data",
			data:     map[string]interface{}{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.calculateExperience(tt.data)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_CalculateCommitment(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name     string
		data     map[string]interface{}
		expected int
	}{
		{
			name: "full commitment",
			data: map[string]interface{}{
				"timeAvailability":  40,
				"relocationWilling": true,
			},
			expected: 100, // 50 + 50
		},
		{
			name: "good commitment",
			data: map[string]interface{}{
				"timeAvailability":  35,
				"relocationWilling": true,
			},
			expected: 80, // 30 + 50
		},
		{
			name: "moderate commitment",
			data: map[string]interface{}{
				"timeAvailability":  25,
				"relocationWilling": false,
			},
			expected: 30, // 30 + 0
		},
		{
			name: "low commitment",
			data: map[string]interface{}{
				"timeAvailability":  5,
				"relocationWilling": false,
			},
			expected: 0, // 0 + 0
		},
		{
			name: "relocation only",
			data: map[string]interface{}{
				"timeAvailability":  0,
				"relocationWilling": true,
			},
			expected: 50, // 0 + 50
		},
		{
			name:     "missing commitment data",
			data:     map[string]interface{}{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.calculateCommitment(tt.data)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_CalculateCompatibility(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name     string
		data     map[string]interface{}
		expected int
	}{
		{
			name: "perfect compatibility",
			data: map[string]interface{}{
				"categoryMatch":  true,
				"skillAlignment": true,
				"locationMatch":  true,
			},
			expected: 100, // 40 + 30 + 30
		},
		{
			name: "good compatibility",
			data: map[string]interface{}{
				"categoryMatch":  true,
				"skillAlignment": true,
				"locationMatch":  false,
			},
			expected: 70, // 40 + 30 + 0
		},
		{
			name: "moderate compatibility",
			data: map[string]interface{}{
				"categoryMatch":  true,
				"skillAlignment": false,
				"locationMatch":  false,
			},
			expected: 40, // 40 + 0 + 0
		},
		{
			name: "location compatibility only",
			data: map[string]interface{}{
				"categoryMatch":  false,
				"skillAlignment": false,
				"locationMatch":  true,
			},
			expected: 30, // 0 + 0 + 30
		},
		{
			name: "no compatibility",
			data: map[string]interface{}{
				"categoryMatch":  false,
				"skillAlignment": false,
				"locationMatch":  false,
			},
			expected: 0,
		},
		{
			name:     "missing compatibility data",
			data:     map[string]interface{}{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.calculateCompatibility(tt.data)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_ClassifyQualificationLevel(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name     string
		score    int
		expected string
	}{
		{"excellent 100", 100, "excellent"},
		{"excellent 90", 90, "excellent"},
		{"excellent 81", 81, "excellent"},
		{"high 80", 80, "high"},
		{"high 70", 70, "high"},
		{"high 61", 61, "high"},
		{"medium 60", 60, "medium"},
		{"medium 50", 50, "medium"},
		{"medium 41", 41, "medium"},
		{"low 40", 40, "low"},
		{"low 30", 30, "low"},
		{"low 0", 0, "low"},
		{"low negative", -10, "low"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.classifyQualificationLevel(tt.score)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_ParseInt(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name     string
		input    interface{}
		expected int
		hasError bool
	}{
		{"float64", 1000.0, 1000, false},
		{"float64 decimal", 1500.5, 1500, false},
		{"string number", "2000", 2000, false},
		{"string with commas", "1,000,000", 1000000, false},
		{"bool true", true, 0, true},
		{"bool false", false, 0, true},
		{"nil", nil, 0, true},
		{"map", map[string]interface{}{"value": 100}, 0, true},
		{"slice", []int{100}, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := handler.parseInt(tt.input)
			if tt.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestHandler_Clamp(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name     string
		value    int
		min      int
		max      int
		expected int
	}{
		{"within range", 50, 0, 100, 50},
		{"at min", 0, 0, 100, 0},
		{"at max", 100, 0, 100, 100},
		{"below min", -10, 0, 100, 0},
		{"above max", 150, 0, 100, 100},
		{"negative range", -50, -100, 0, -50},
		{"all same", 5, 5, 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.clamp(tt.value, tt.min, tt.max)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	t.Run("nil application data", func(t *testing.T) {
		input := &Input{
			UserID:          "user-nil",
			ApplicationData: nil,
		}

		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.Equal(t, "low", output.QualificationLevel)
		assert.Equal(t, 0, output.ReadinessScore)
	})

	t.Run("very large numbers", func(t *testing.T) {
		input := createTestInput("user-large", map[string]interface{}{
			"liquidCapital":    1000000000,
			"netWorth":         5000000000,
			"creditScore":      1000, // Will be clamped to 850
			"yearsInIndustry":  50,
			"timeAvailability": 168, // hours in week
		})

		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		// Expected: Financial=100, Experience=40 (no mgmt/ownership), Commitment=50 (no relocation), Compatibility=0
		// Score: (100*0.3) + (40*0.25) + (50*0.2) + (0*0.25) = 30 + 10 + 10 + 0 = 50
		assert.Equal(t, "medium", output.QualificationLevel) // Changed from "excellent" to "medium"
		assert.Equal(t, 50, output.ReadinessScore)
	})

	t.Run("negative numbers", func(t *testing.T) {
		input := createTestInput("user-negative", map[string]interface{}{
			"liquidCapital":    -50000,
			"netWorth":         -100000,
			"creditScore":      -100,
			"yearsInIndustry":  -5,
			"timeAvailability": -10,
		})

		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.Equal(t, "low", output.QualificationLevel)
	})

	t.Run("mixed data types", func(t *testing.T) {
		input := createTestInput("user-mixed", map[string]interface{}{
			"liquidCapital":        "750000",   // string
			"netWorth":             1500000.0,  // float64
			"creditScore":          700,        // int
			"yearsInIndustry":      "5",        // string
			"managementExperience": "true",     // string (should be bool)
			"timeAvailability":     "25 hours", // invalid string
		})

		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		// Should handle gracefully and calculate based on valid data
	})

	t.Run("empty user ID", func(t *testing.T) {
		input := createTestInput("", createMediumScoreApplicationData())

		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.Equal(t, "medium", output.QualificationLevel)
	})
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	config := createTestConfig()
	handler := NewHandler(config, newTestLogger(t))

	// Test complete scoring workflow
	input := createTestInput("user-complete", map[string]interface{}{
		"liquidCapital":        800000,
		"netWorth":             1800000,
		"creditScore":          720,
		"yearsInIndustry":      8,
		"managementExperience": true,
		"businessOwnership":    false,
		"timeAvailability":     35,
		"relocationWilling":    true,
		"categoryMatch":        true,
		"skillAlignment":       true,
		"locationMatch":        false,
	})

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)

	// Verify all components are calculated
	assert.Greater(t, output.ReadinessScore, 0)
	assert.Contains(t, []string{"excellent", "high", "medium", "low"}, output.QualificationLevel)
	assert.GreaterOrEqual(t, output.ScoreBreakdown.Financial, 0)
	assert.GreaterOrEqual(t, output.ScoreBreakdown.Experience, 0)
	assert.GreaterOrEqual(t, output.ScoreBreakdown.Commitment, 0)
	assert.GreaterOrEqual(t, output.ScoreBreakdown.Compatibility, 0)
	assert.LessOrEqual(t, output.ScoreBreakdown.Financial, 100)
	assert.LessOrEqual(t, output.ScoreBreakdown.Experience, 100)
	assert.LessOrEqual(t, output.ScoreBreakdown.Commitment, 100)
	assert.LessOrEqual(t, output.ScoreBreakdown.Compatibility, 100)

	// Verify weighted average calculation
	expectedWeighted := int(
		float64(output.ScoreBreakdown.Financial)*0.30 +
			float64(output.ScoreBreakdown.Experience)*0.25 +
			float64(output.ScoreBreakdown.Commitment)*0.20 +
			float64(output.ScoreBreakdown.Compatibility)*0.25)

	assert.Equal(t, expectedWeighted, output.ReadinessScore)
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	config := createTestConfig()
	handler := NewHandler(config, newTestLogger(&testing.T{}))

	input := createTestInput("benchmark-user", createHighScoreApplicationData())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_CalculateFinancialReadiness(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))
	data := createHighScoreApplicationData()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.calculateFinancialReadiness(data)
	}
}

func BenchmarkHandler_CalculateExperience(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))
	data := createHighScoreApplicationData()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.calculateExperience(data)
	}
}

func BenchmarkHandler_ClassifyQualificationLevel(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.classifyQualificationLevel(75)
	}
}

// // internal/workers/application/check-readiness-score/handler_test.go
// package checkreadinessscore

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

// func createTestInput(userID string, applicationData map[string]interface{}) *Input {
// 	return &Input{
// 		UserID:          userID,
// 		ApplicationData: applicationData,
// 	}
// }

// func createHighScoreApplicationData() map[string]interface{} {
// 	return map[string]interface{}{
// 		"liquidCapital":        1000000,
// 		"netWorth":             2000000,
// 		"creditScore":          750,
// 		"yearsInIndustry":      10,
// 		"managementExperience": true,
// 		"businessOwnership":    true,
// 		"timeAvailability":     40,
// 		"relocationWilling":    true,
// 		"categoryMatch":        true,
// 		"skillAlignment":       true,
// 		"locationMatch":        true,
// 	}
// }

// func createMediumScoreApplicationData() map[string]interface{} {
// 	return map[string]interface{}{
// 		"liquidCapital":        500000,
// 		"netWorth":             750000,
// 		"creditScore":          650,
// 		"yearsInIndustry":      3,
// 		"managementExperience": true,
// 		"businessOwnership":    false,
// 		"timeAvailability":     25,
// 		"relocationWilling":    false,
// 		"categoryMatch":        true,
// 		"skillAlignment":       false,
// 		"locationMatch":        true,
// 	}
// }

// func createLowScoreApplicationData() map[string]interface{} {
// 	return map[string]interface{}{
// 		"liquidCapital":        50000,
// 		"netWorth":             100000,
// 		"creditScore":          550,
// 		"yearsInIndustry":      0,
// 		"managementExperience": false,
// 		"businessOwnership":    false,
// 		"timeAvailability":     5,
// 		"relocationWilling":    false,
// 		"categoryMatch":        false,
// 		"skillAlignment":       false,
// 		"locationMatch":        false,
// 	}
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name              string
// 		input             *Input
// 		expectedScore     int
// 		expectedLevel     string
// 		expectedBreakdown ScoreBreakdown
// 		validateOutput    func(t *testing.T, output *Output)
// 	}{
// 		{
// 			name:          "excellent qualification level",
// 			input:         createTestInput("user-001", createHighScoreApplicationData()),
// 			expectedScore: 100, // 40+30+30 + 40+30+30 + 50+50 + 40+30+30 = 100
// 			expectedLevel: "excellent",
// 			expectedBreakdown: ScoreBreakdown{
// 				Financial:     100,
// 				Experience:    100,
// 				Commitment:    100,
// 				Compatibility: 100,
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, "excellent", output.QualificationLevel)
// 				assert.True(t, output.ReadinessScore >= 81)
// 				assert.Equal(t, 100, output.ScoreBreakdown.Financial)
// 				assert.Equal(t, 100, output.ScoreBreakdown.Experience)
// 				assert.Equal(t, 100, output.ScoreBreakdown.Commitment)
// 				assert.Equal(t, 100, output.ScoreBreakdown.Compatibility)
// 			},
// 		},
// 		{
// 			name: "high qualification level",
// 			input: createTestInput("user-002", map[string]interface{}{
// 				"liquidCapital":        750000,
// 				"netWorth":             1500000,
// 				"creditScore":          720,
// 				"yearsInIndustry":      7,
// 				"managementExperience": true,
// 				"businessOwnership":    false,
// 				"timeAvailability":     35,
// 				"relocationWilling":    true,
// 				"categoryMatch":        true,
// 				"skillAlignment":       true,
// 				"locationMatch":        false,
// 			}),
// 			expectedScore: 75, // 30+20+30 + 30+30+0 + 30+50 + 40+30+0 = ~75
// 			expectedLevel: "high",
// 			expectedBreakdown: ScoreBreakdown{
// 				Financial:     80,
// 				Experience:    60,
// 				Commitment:    80,
// 				Compatibility: 70,
// 			},
// 		},
// 		{
// 			name:          "medium qualification level",
// 			input:         createTestInput("user-003", createMediumScoreApplicationData()),
// 			expectedScore: 55, // 30+10+20 + 20+30+0 + 30+0 + 40+0+30 = ~55
// 			expectedLevel: "medium",
// 			expectedBreakdown: ScoreBreakdown{
// 				Financial:     60,
// 				Experience:    50,
// 				Commitment:    30,
// 				Compatibility: 70,
// 			},
// 		},
// 		{
// 			name:          "low qualification level",
// 			input:         createTestInput("user-004", createLowScoreApplicationData()),
// 			expectedScore: 15, // 0+0+10 + 0+0+0 + 0+0 + 0+0+0 = ~15
// 			expectedLevel: "low",
// 			expectedBreakdown: ScoreBreakdown{
// 				Financial:     10,
// 				Experience:    0,
// 				Commitment:    0,
// 				Compatibility: 0,
// 			},
// 		},
// 		{
// 			name: "minimal application data",
// 			input: createTestInput("user-005", map[string]interface{}{
// 				"liquidCapital": 100000,
// 				"creditScore":   600,
// 			}),
// 			expectedScore: 20, // 10+0+20 + 0+0+0 + 0+0 + 0+0+0 = ~20
// 			expectedLevel: "low",
// 			expectedBreakdown: ScoreBreakdown{
// 				Financial:     30,
// 				Experience:    0,
// 				Commitment:    0,
// 				Compatibility: 0,
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			config := createTestConfig()
// 			handler := NewHandler(config, zaptest.NewLogger(t))

// 			output, err := handler.execute(context.Background(), tt.input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.Equal(t, tt.expectedLevel, output.QualificationLevel)
// 			assert.Equal(t, tt.expectedBreakdown, output.ScoreBreakdown)

// 			if tt.validateOutput != nil {
// 				tt.validateOutput(t, output)
// 			}
// 		})
// 	}
// }

// func TestHandler_Execute_EmptyApplicationData(t *testing.T) {
// 	config := createTestConfig()
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	input := createTestInput("user-empty", map[string]interface{}{})
// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.Equal(t, "low", output.QualificationLevel)
// 	assert.Equal(t, 0, output.ReadinessScore)
// 	assert.Equal(t, ScoreBreakdown{0, 0, 0, 0}, output.ScoreBreakdown)
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_CalculateFinancialReadiness(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		data     map[string]interface{}
// 		expected int
// 	}{
// 		{
// 			name: "excellent financials",
// 			data: map[string]interface{}{
// 				"liquidCapital": 1500000,
// 				"netWorth":      2500000,
// 				"creditScore":   800,
// 			},
// 			expected: 100, // 40 + 30 + 30
// 		},
// 		{
// 			name: "good financials",
// 			data: map[string]interface{}{
// 				"liquidCapital": 750000,
// 				"netWorth":      1500000,
// 				"creditScore":   720,
// 			},
// 			expected: 80, // 30 + 20 + 30
// 		},
// 		{
// 			name: "average financials",
// 			data: map[string]interface{}{
// 				"liquidCapital": 300000,
// 				"netWorth":      750000,
// 				"creditScore":   650,
// 			},
// 			expected: 50, // 20 + 10 + 20
// 		},
// 		{
// 			name: "poor_financials",
// 			data: map[string]interface{}{
// 				"liquidCapital": 50000,  // 0 points (below 100k threshold)
// 				"netWorth":      100000, // 0 points (below 500k threshold)
// 				"creditScore":   550,    // 10 points (500-599 range)
// 			},
// 			expected: 10, // 0 + 0 + 10 = 10 ✓
// 		},
// 		{
// 			name: "missing financial data",
// 			data: map[string]interface{}{
// 				"creditScore": 700,
// 			},
// 			expected: 30, // 0 + 0 + 30
// 		},
// 		{
// 			name:     "no financial data",
// 			data:     map[string]interface{}{},
// 			expected: 0,
// 		},
// 		{
// 			name: "string number values",
// 			data: map[string]interface{}{
// 				"liquidCapital": "1000000",
// 				"netWorth":      "2000000",
// 				"creditScore":   "750",
// 			},
// 			expected: 100,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result := handler.calculateFinancialReadiness(tt.data)
// 			assert.Equal(t, tt.expected, result)
// 		})
// 	}
// }

// func TestHandler_CalculateExperience(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		data     map[string]interface{}
// 		expected int
// 	}{
// 		{
// 			name: "extensive experience",
// 			data: map[string]interface{}{
// 				"yearsInIndustry":      15,
// 				"managementExperience": true,
// 				"businessOwnership":    true,
// 			},
// 			expected: 100, // 40 + 30 + 30
// 		},
// 		{
// 			name: "good experience",
// 			data: map[string]interface{}{
// 				"yearsInIndustry":      7,
// 				"managementExperience": true,
// 				"businessOwnership":    false,
// 			},
// 			expected: 60, // 30 + 30 + 0
// 		},
// 		{
// 			name: "some experience",
// 			data: map[string]interface{}{
// 				"yearsInIndustry":      3,
// 				"managementExperience": false,
// 				"businessOwnership":    true,
// 			},
// 			expected: 50, // 20 + 0 + 30
// 		},
// 		{
// 			name: "minimal experience",
// 			data: map[string]interface{}{
// 				"yearsInIndustry":      1,
// 				"managementExperience": false,
// 				"businessOwnership":    false,
// 			},
// 			expected: 10, // 10 + 0 + 0
// 		},
// 		{
// 			name: "no experience",
// 			data: map[string]interface{}{
// 				"yearsInIndustry": 0,
// 			},
// 			expected: 0,
// 		},
// 		{
// 			name:     "missing experience data",
// 			data:     map[string]interface{}{},
// 			expected: 0,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result := handler.calculateExperience(tt.data)
// 			assert.Equal(t, tt.expected, result)
// 		})
// 	}
// }

// func TestHandler_CalculateCommitment(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		data     map[string]interface{}
// 		expected int
// 	}{
// 		{
// 			name: "full commitment",
// 			data: map[string]interface{}{
// 				"timeAvailability":  40,
// 				"relocationWilling": true,
// 			},
// 			expected: 100, // 50 + 50
// 		},
// 		{
// 			name: "good commitment",
// 			data: map[string]interface{}{
// 				"timeAvailability":  35,
// 				"relocationWilling": true,
// 			},
// 			expected: 80, // 30 + 50
// 		},
// 		{
// 			name: "moderate commitment",
// 			data: map[string]interface{}{
// 				"timeAvailability":  25,
// 				"relocationWilling": false,
// 			},
// 			expected: 30, // 30 + 0
// 		},
// 		{
// 			name: "low commitment",
// 			data: map[string]interface{}{
// 				"timeAvailability":  5,
// 				"relocationWilling": false,
// 			},
// 			expected: 0, // 0 + 0
// 		},
// 		{
// 			name: "relocation only",
// 			data: map[string]interface{}{
// 				"timeAvailability":  0,
// 				"relocationWilling": true,
// 			},
// 			expected: 50, // 0 + 50
// 		},
// 		{
// 			name:     "missing commitment data",
// 			data:     map[string]interface{}{},
// 			expected: 0,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result := handler.calculateCommitment(tt.data)
// 			assert.Equal(t, tt.expected, result)
// 		})
// 	}
// }

// func TestHandler_CalculateCompatibility(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		data     map[string]interface{}
// 		expected int
// 	}{
// 		{
// 			name: "perfect compatibility",
// 			data: map[string]interface{}{
// 				"categoryMatch":  true,
// 				"skillAlignment": true,
// 				"locationMatch":  true,
// 			},
// 			expected: 100, // 40 + 30 + 30
// 		},
// 		{
// 			name: "good compatibility",
// 			data: map[string]interface{}{
// 				"categoryMatch":  true,
// 				"skillAlignment": true,
// 				"locationMatch":  false,
// 			},
// 			expected: 70, // 40 + 30 + 0
// 		},
// 		{
// 			name: "moderate compatibility",
// 			data: map[string]interface{}{
// 				"categoryMatch":  true,
// 				"skillAlignment": false,
// 				"locationMatch":  false,
// 			},
// 			expected: 40, // 40 + 0 + 0
// 		},
// 		{
// 			name: "location compatibility only",
// 			data: map[string]interface{}{
// 				"categoryMatch":  false,
// 				"skillAlignment": false,
// 				"locationMatch":  true,
// 			},
// 			expected: 30, // 0 + 0 + 30
// 		},
// 		{
// 			name: "no compatibility",
// 			data: map[string]interface{}{
// 				"categoryMatch":  false,
// 				"skillAlignment": false,
// 				"locationMatch":  false,
// 			},
// 			expected: 0,
// 		},
// 		{
// 			name:     "missing compatibility data",
// 			data:     map[string]interface{}{},
// 			expected: 0,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result := handler.calculateCompatibility(tt.data)
// 			assert.Equal(t, tt.expected, result)
// 		})
// 	}
// }

// func TestHandler_ClassifyQualificationLevel(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		score    int
// 		expected string
// 	}{
// 		{"excellent 100", 100, "excellent"},
// 		{"excellent 90", 90, "excellent"},
// 		{"excellent 81", 81, "excellent"},
// 		{"high 80", 80, "high"},
// 		{"high 70", 70, "high"},
// 		{"high 61", 61, "high"},
// 		{"medium 60", 60, "medium"},
// 		{"medium 50", 50, "medium"},
// 		{"medium 41", 41, "medium"},
// 		{"low 40", 40, "low"},
// 		{"low 30", 30, "low"},
// 		{"low 0", 0, "low"},
// 		{"low negative", -10, "low"},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result := handler.classifyQualificationLevel(tt.score)
// 			assert.Equal(t, tt.expected, result)
// 		})
// 	}
// }

// func TestHandler_ParseInt(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		input    interface{}
// 		expected int
// 		hasError bool
// 	}{
// 		{"float64", 1000.0, 1000, false},
// 		{"float64 decimal", 1500.5, 1500, false},
// 		{"string number", "2000", 2000, false},
// 		{"string with commas", "1,000,000", 1000000, false},
// 		{"bool true", true, 0, true},
// 		{"bool false", false, 0, true},
// 		{"nil", nil, 0, true},
// 		{"map", map[string]interface{}{"value": 100}, 0, true},
// 		{"slice", []int{100}, 0, true},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result, err := handler.parseInt(tt.input)
// 			if tt.hasError {
// 				assert.Error(t, err)
// 			} else {
// 				assert.NoError(t, err)
// 				assert.Equal(t, tt.expected, result)
// 			}
// 		})
// 	}
// }

// func TestHandler_Clamp(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		value    int
// 		min      int
// 		max      int
// 		expected int
// 	}{
// 		{"within range", 50, 0, 100, 50},
// 		{"at min", 0, 0, 100, 0},
// 		{"at max", 100, 0, 100, 100},
// 		{"below min", -10, 0, 100, 0},
// 		{"above max", 150, 0, 100, 100},
// 		{"negative range", -50, -100, 0, -50},
// 		{"all same", 5, 5, 5, 5},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result := handler.clamp(tt.value, tt.min, tt.max)
// 			assert.Equal(t, tt.expected, result)
// 		})
// 	}
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	t.Run("nil application data", func(t *testing.T) {
// 		input := &Input{
// 			UserID:          "user-nil",
// 			ApplicationData: nil,
// 		}

// 		output, err := handler.execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		assert.Equal(t, "low", output.QualificationLevel)
// 		assert.Equal(t, 0, output.ReadinessScore)
// 	})

// 	t.Run("very large numbers", func(t *testing.T) {
// 		input := createTestInput("user-large", map[string]interface{}{
// 			"liquidCapital":    1000000000,
// 			"netWorth":         5000000000,
// 			"creditScore":      1000, // Will be clamped to 850
// 			"yearsInIndustry":  50,
// 			"timeAvailability": 168, // hours in week
// 		})

// 		output, err := handler.execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		// Expected: Financial=100, Experience=40 (no mgmt/ownership), Commitment=50 (no relocation), Compatibility=0
// 		// Score: (100*0.3) + (40*0.25) + (50*0.2) + (0*0.25) = 30 + 10 + 10 + 0 = 50
// 		assert.Equal(t, "medium", output.QualificationLevel) // Changed from "excellent" to "medium"
// 		assert.Equal(t, 50, output.ReadinessScore)
// 	})

// 	t.Run("negative numbers", func(t *testing.T) {
// 		input := createTestInput("user-negative", map[string]interface{}{
// 			"liquidCapital":    -50000,
// 			"netWorth":         -100000,
// 			"creditScore":      -100,
// 			"yearsInIndustry":  -5,
// 			"timeAvailability": -10,
// 		})

// 		output, err := handler.execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		assert.Equal(t, "low", output.QualificationLevel)
// 	})

// 	t.Run("mixed data types", func(t *testing.T) {
// 		input := createTestInput("user-mixed", map[string]interface{}{
// 			"liquidCapital":        "750000",   // string
// 			"netWorth":             1500000.0,  // float64
// 			"creditScore":          700,        // int
// 			"yearsInIndustry":      "5",        // string
// 			"managementExperience": "true",     // string (should be bool)
// 			"timeAvailability":     "25 hours", // invalid string
// 		})

// 		output, err := handler.execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		// Should handle gracefully and calculate based on valid data
// 	})

// 	t.Run("empty user ID", func(t *testing.T) {
// 		input := createTestInput("", createMediumScoreApplicationData())

// 		output, err := handler.execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		assert.Equal(t, "medium", output.QualificationLevel)
// 	})
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	config := createTestConfig()
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	// Test complete scoring workflow
// 	input := createTestInput("user-complete", map[string]interface{}{
// 		"liquidCapital":        800000,
// 		"netWorth":             1800000,
// 		"creditScore":          720,
// 		"yearsInIndustry":      8,
// 		"managementExperience": true,
// 		"businessOwnership":    false,
// 		"timeAvailability":     35,
// 		"relocationWilling":    true,
// 		"categoryMatch":        true,
// 		"skillAlignment":       true,
// 		"locationMatch":        false,
// 	})

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)

// 	// Verify all components are calculated
// 	assert.Greater(t, output.ReadinessScore, 0)
// 	assert.Contains(t, []string{"excellent", "high", "medium", "low"}, output.QualificationLevel)
// 	assert.GreaterOrEqual(t, output.ScoreBreakdown.Financial, 0)
// 	assert.GreaterOrEqual(t, output.ScoreBreakdown.Experience, 0)
// 	assert.GreaterOrEqual(t, output.ScoreBreakdown.Commitment, 0)
// 	assert.GreaterOrEqual(t, output.ScoreBreakdown.Compatibility, 0)
// 	assert.LessOrEqual(t, output.ScoreBreakdown.Financial, 100)
// 	assert.LessOrEqual(t, output.ScoreBreakdown.Experience, 100)
// 	assert.LessOrEqual(t, output.ScoreBreakdown.Commitment, 100)
// 	assert.LessOrEqual(t, output.ScoreBreakdown.Compatibility, 100)

// 	// Verify weighted average calculation
// 	expectedWeighted := int(
// 		float64(output.ScoreBreakdown.Financial)*0.30 +
// 			float64(output.ScoreBreakdown.Experience)*0.25 +
// 			float64(output.ScoreBreakdown.Commitment)*0.20 +
// 			float64(output.ScoreBreakdown.Compatibility)*0.25)

// 	assert.Equal(t, expectedWeighted, output.ReadinessScore)
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	config := createTestConfig()
// 	handler := NewHandler(config, zaptest.NewLogger(b))

// 	input := createTestInput("benchmark-user", createHighScoreApplicationData())

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), input)
// 	}
// }

// func BenchmarkHandler_CalculateFinancialReadiness(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))
// 	data := createHighScoreApplicationData()

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.calculateFinancialReadiness(data)
// 	}
// }

// func BenchmarkHandler_CalculateExperience(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))
// 	data := createHighScoreApplicationData()

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.calculateExperience(data)
// 	}
// }

// func BenchmarkHandler_ClassifyQualificationLevel(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.classifyQualificationLevel(75)
// 	}
// }
