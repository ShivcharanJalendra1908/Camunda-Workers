package checkreadinessscore

import (
	"context"
	"database/sql"
	"testing"

	"camunda-workers/internal/common/logger"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{}
}

// Valid UUID v4 strings for testing
const (
	testUserUUID001   = "b2c3d4e5-f6a7-4890-9abc-000000000001"
	testUserUUID002   = "b2c3d4e5-f6a7-4890-9abc-000000000002"
	testUserUUID003   = "b2c3d4e5-f6a7-4890-9abc-000000000003"
	testUserUUID004   = "b2c3d4e5-f6a7-4890-9abc-000000000004"
	testUserUUID005   = "b2c3d4e5-f6a7-4890-9abc-000000000005"
	testUserUUIDEmpty = "b2c3d4e5-f6a7-4890-9abc-00000000000e"
	testUserUUIDLarge = "b2c3d4e5-f6a7-4890-9abc-000000000010"
	testUserUUIDNeg   = "b2c3d4e5-f6a7-4890-9abc-000000000011"
	testUserUUIDMix   = "b2c3d4e5-f6a7-4890-9abc-000000000012"
	testUserUUIDComp  = "b2c3d4e5-f6a7-4890-9abc-000000000013"
	testUserUUIDBench = "b2c3d4e5-f6a7-4890-9abc-000000000014"
)

// Mock database for tests that need DB access
type mockDB struct {
	sqlmock.Sqlmock
	*sql.DB
}

func setupMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	return db, mock
}

func createTestInput(userID string, applicationData map[string]interface{}) *Input {
	return &Input{
		UserID:          userID,
		FranchiseID:     "franchise-123",
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
		"preferredState":       "Maharashtra",
		"industryBackground":   "retail",
		"involvementLevel":     "full-time-owner",
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
		"preferredState":       "Karnataka",
		"industryBackground":   "technology",
		"involvementLevel":     "part-time-with-manager",
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
		"preferredState":       "",
		"industryBackground":   "",
		"involvementLevel":     "investor-only",
	}
}

// Create a test logger that implements logger.Logger interface
type testLogger struct {
	t *testing.T
}

func (tl *testLogger) Debug(msg string, fields map[string]interface{}) {
	if tl.t != nil {
		tl.t.Logf("DEBUG: %s %v", msg, fields)
	}
}

func (tl *testLogger) Info(msg string, fields map[string]interface{}) {
	if tl.t != nil {
		tl.t.Logf("INFO: %s %v", msg, fields)
	}
}

func (tl *testLogger) Warn(msg string, fields map[string]interface{}) {
	if tl.t != nil {
		tl.t.Logf("WARN: %s %v", msg, fields)
	}
}

func (tl *testLogger) Error(msg string, fields map[string]interface{}) {
	if tl.t != nil {
		tl.t.Logf("ERROR: %s %v", msg, fields)
	}
}

func (tl *testLogger) WithFields(fields map[string]interface{}) logger.Logger {
	return tl
}

func (tl *testLogger) WithError(err error) logger.Logger {
	return tl.WithFields(map[string]interface{}{"error": err})
}

func (tl *testLogger) With(fields map[string]interface{}) logger.Logger {
	return tl
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
		setupMock      func(sqlmock.Sqlmock)
		expectedLevel  string
		validateOutput func(t *testing.T, output *Output)
	}{
		{
			name:  "excellent qualification level",
			input: createTestInput(testUserUUID001, createHighScoreApplicationData()),
			setupMock: func(mock sqlmock.Sqlmock) {
				// Mock location match query
				mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM franchise_cities").
					WithArgs("franchise-123", "maharashtra").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

				// Mock category match query
				mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM franchise_categories").
					WithArgs("franchise-123", "retail").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

				// Mock skill alignment query
				mock.ExpectQuery("SELECT qualification_required FROM franchise_operations").
					WithArgs("franchise-123").
					WillReturnRows(sqlmock.NewRows([]string{"qualification_required"}).AddRow("full-time-owner"))
			},
			expectedLevel: "excellent",
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "excellent", output.QualificationLevel)
				assert.GreaterOrEqual(t, output.ReadinessScore, 81)
				assert.GreaterOrEqual(t, output.ScoreBreakdown.Financial, 80)
				assert.GreaterOrEqual(t, output.ScoreBreakdown.Experience, 80)
				assert.GreaterOrEqual(t, output.ScoreBreakdown.Commitment, 80)
				assert.GreaterOrEqual(t, output.ScoreBreakdown.Compatibility, 80)
			},
		},
		{
			name: "high qualification level",
			input: createTestInput(testUserUUID002, map[string]interface{}{
				"liquidCapital":        750000,
				"netWorth":             1500000,
				"creditScore":          720,
				"yearsInIndustry":      7,
				"managementExperience": true,
				"businessOwnership":    false,
				"timeAvailability":     35,
				"relocationWilling":    true,
				"preferredState":       "Maharashtra",
				"industryBackground":   "retail",
				"involvementLevel":     "full-time-owner",
			}),
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM franchise_cities").
					WithArgs("franchise-123", "maharashtra").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

				mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM franchise_categories").
					WithArgs("franchise-123", "retail").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

				mock.ExpectQuery("SELECT qualification_required FROM franchise_operations").
					WithArgs("franchise-123").
					WillReturnRows(sqlmock.NewRows([]string{"qualification_required"}).AddRow("full-time-owner"))
			},
			expectedLevel: "high",
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "high", output.QualificationLevel)
				assert.GreaterOrEqual(t, output.ReadinessScore, 61)
				assert.LessOrEqual(t, output.ReadinessScore, 80)
			},
		},
		{
			name:  "medium qualification level",
			input: createTestInput(testUserUUID003, createMediumScoreApplicationData()),
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM franchise_cities").
					WithArgs("franchise-123", "karnataka").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

				mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM franchise_categories").
					WithArgs("franchise-123", "technology").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

				mock.ExpectQuery("SELECT qualification_required FROM franchise_operations").
					WithArgs("franchise-123").
					WillReturnRows(sqlmock.NewRows([]string{"qualification_required"}).AddRow("part-time-with-manager"))
			},
			expectedLevel: "medium",
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "medium", output.QualificationLevel)
				assert.GreaterOrEqual(t, output.ReadinessScore, 41)
				assert.LessOrEqual(t, output.ReadinessScore, 60)
			},
		},
		{
			name:  "low qualification level",
			input: createTestInput(testUserUUID004, createLowScoreApplicationData()),
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT qualification_required FROM franchise_operations").
					WithArgs("franchise-123").
					WillReturnRows(sqlmock.NewRows([]string{"qualification_required"}).AddRow("investor-only"))
			},
			expectedLevel: "low",
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "low", output.QualificationLevel)
				assert.LessOrEqual(t, output.ReadinessScore, 40)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupMockDB(t)
			defer db.Close()

			tt.setupMock(mock)

			config := createTestConfig()
			handler := NewHandler(config, db, newTestLogger(t))

			output, err := handler.Execute(context.Background(), tt.input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedLevel, output.QualificationLevel)

			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}

			// Verify all expectations were met
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestHandler_Execute_EmptyApplicationData(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	// Mock empty results for all queries
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM franchise_cities").
		WithArgs("franchise-123", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM franchise_categories").
		WithArgs("franchise-123", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	mock.ExpectQuery("SELECT qualification_required FROM franchise_operations").
		WithArgs("franchise-123").
		WillReturnRows(sqlmock.NewRows([]string{"qualification_required"}).AddRow(nil))

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(t))

	input := createTestInput(testUserUUIDEmpty, map[string]interface{}{})
	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, "low", output.QualificationLevel)
	assert.Equal(t, 0, output.ReadinessScore)
	assert.Equal(t, ScoreBreakdown{0, 0, 0, 0}, output.ScoreBreakdown)
}

// ==========================
// Unit Tests (No DB Required)
// ==========================

func TestHandler_CalculateFinancialReadiness(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()

	handler := NewHandler(createTestConfig(), db, newTestLogger(t))

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
			expected: 100,
		},
		{
			name: "good financials",
			data: map[string]interface{}{
				"liquidCapital": 750000,
				"netWorth":      1500000,
				"creditScore":   720,
			},
			expected: 80,
		},
		{
			name: "average financials",
			data: map[string]interface{}{
				"liquidCapital": 300000,
				"netWorth":      750000,
				"creditScore":   650,
			},
			expected: 50,
		},
		{
			name: "poor financials",
			data: map[string]interface{}{
				"liquidCapital": 50000,
				"netWorth":      100000,
				"creditScore":   550,
			},
			expected: 10,
		},
		{
			name: "missing financial data",
			data: map[string]interface{}{
				"creditScore": 700,
			},
			expected: 30, // credit score only
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
	db, _ := setupMockDB(t)
	defer db.Close()

	handler := NewHandler(createTestConfig(), db, newTestLogger(t))

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
			expected: 100,
		},
		{
			name: "good experience",
			data: map[string]interface{}{
				"yearsInIndustry":      7,
				"managementExperience": true,
				"businessOwnership":    false,
			},
			expected: 60, // 30 (years: 5-9 range) + 30 (management)
		},
		{
			name: "some experience",
			data: map[string]interface{}{
				"yearsInIndustry":      3,
				"managementExperience": false,
				"businessOwnership":    true,
			},
			expected: 50, // 20 (years: 2-4 range) + 30 (business ownership)
		},
		{
			name: "minimal experience",
			data: map[string]interface{}{
				"yearsInIndustry":      1,
				"managementExperience": false,
				"businessOwnership":    false,
			},
			expected: 10,
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
	db, _ := setupMockDB(t)
	defer db.Close()

	handler := NewHandler(createTestConfig(), db, newTestLogger(t))

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
			expected: 100,
		},
		{
			name: "good commitment",
			data: map[string]interface{}{
				"timeAvailability":  35,
				"relocationWilling": true,
			},
			expected: 80, // 30 (time: 20-39 range) + 50 (relocation)
		},
		{
			name: "moderate commitment",
			data: map[string]interface{}{
				"timeAvailability":  25,
				"relocationWilling": false,
			},
			expected: 30,
		},
		{
			name: "low commitment",
			data: map[string]interface{}{
				"timeAvailability":  5,
				"relocationWilling": false,
			},
			expected: 0,
		},
		{
			name: "relocation only",
			data: map[string]interface{}{
				"timeAvailability":  0,
				"relocationWilling": true,
			},
			expected: 50,
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

func TestHandler_ClassifyQualificationLevel(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()

	handler := NewHandler(createTestConfig(), db, newTestLogger(t))

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
	db, _ := setupMockDB(t)
	defer db.Close()

	handler := NewHandler(createTestConfig(), db, newTestLogger(t))

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
	db, _ := setupMockDB(t)
	defer db.Close()

	handler := NewHandler(createTestConfig(), db, newTestLogger(t))

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

func TestHandler_BudgetToCapital(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()

	handler := NewHandler(createTestConfig(), db, newTestLogger(t))

	tests := []struct {
		name     string
		budget   string
		expected float64
	}{
		{"Under 10 Lakhs", "Under 10 Lakhs", 500000},
		{"10-25 Lakhs", "10-25 Lakhs", 1000000},
		{"25-50 Lakhs", "25-50 Lakhs", 2500000},
		{"50 Lakhs - 1 Crore", "50 Lakhs - 1 Crore", 5000000},
		{"1-2 Crores", "1-2 Crores", 10000000},
		{"Above 2 Crores", "Above 2 Crores", 20000000},
		{"Unknown", "Unknown", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.budgetToCapital(tt.budget)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_ParseYearsFromBackground(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()

	handler := NewHandler(createTestConfig(), db, newTestLogger(t))

	tests := []struct {
		name       string
		background string
		expected   int
	}{
		{"5 years experience", "I have 5 years of retail experience.", 5},
		{"10 years experience", "10 years in the industry", 10},
		{"No years mentioned", "Experienced professional", 0},
		{"Multiple years", "After 15 years, I'm ready", 15},
		{"Year not a number", "Several years of experience", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.parseYearsFromBackground(tt.background)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_InvolvementToHours(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()

	handler := NewHandler(createTestConfig(), db, newTestLogger(t))

	tests := []struct {
		name        string
		involvement string
		expected    float64
	}{
		{"full-time-owner", "full-time-owner", 40},
		{"part-time-with-manager", "part-time-with-manager", 20},
		{"investor-only", "investor-only", 5},
		{"Unknown", "unknown", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.involvementToHours(tt.involvement)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	t.Run("nil application data", func(t *testing.T) {
		db, mock := setupMockDB(t)
		defer db.Close()

		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM franchise_cities").
			WithArgs("franchise-123", sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM franchise_categories").
			WithArgs("franchise-123", sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		mock.ExpectQuery("SELECT qualification_required FROM franchise_operations").
			WithArgs("franchise-123").
			WillReturnRows(sqlmock.NewRows([]string{"qualification_required"}).AddRow(nil))

		config := createTestConfig()
		handler := NewHandler(config, db, newTestLogger(t))

		input := &Input{
			UserID:          testUserUUIDEmpty,
			FranchiseID:     "franchise-123",
			ApplicationData: nil,
		}

		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.Equal(t, "low", output.QualificationLevel)
		assert.Equal(t, 0, output.ReadinessScore)
	})

	t.Run("very large numbers", func(t *testing.T) {
		db, _ := setupMockDB(t)
		defer db.Close()

		config := createTestConfig()
		handler := NewHandler(config, db, newTestLogger(t))

		input := createTestInput(testUserUUIDLarge, map[string]interface{}{
			"liquidCapital":    1000000000,
			"netWorth":         5000000000,
			"creditScore":      1000,
			"yearsInIndustry":  50,
			"timeAvailability": 168,
		})

		output, err := handler.Execute(context.Background(), input)

		assert.Error(t, err)
		assert.Nil(t, output)
	})

	t.Run("negative numbers", func(t *testing.T) {
		db, _ := setupMockDB(t)
		defer db.Close()

		config := createTestConfig()
		handler := NewHandler(config, db, newTestLogger(t))

		input := createTestInput(testUserUUIDNeg, map[string]interface{}{
			"liquidCapital":    -50000,
			"netWorth":         -100000,
			"creditScore":      -100,
			"yearsInIndustry":  -5,
			"timeAvailability": -10,
		})

		output, err := handler.Execute(context.Background(), input)

		assert.Error(t, err)
		assert.Nil(t, output)
	})

	t.Run("empty user ID fails validation", func(t *testing.T) {
		db, _ := setupMockDB(t)
		defer db.Close()

		config := createTestConfig()
		handler := NewHandler(config, db, newTestLogger(t))

		input := createTestInput("", createMediumScoreApplicationData())

		output, err := handler.Execute(context.Background(), input)

		assert.Error(t, err)
		assert.Nil(t, output)
	})
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	db, mock := setupMockDB(&testing.T{})
	defer db.Close()

	// Setup mock expectations for benchmark
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM franchise_cities").
		WithArgs("franchise-123", "maharashtra").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM franchise_categories").
		WithArgs("franchise-123", "retail").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	mock.ExpectQuery("SELECT qualification_required FROM franchise_operations").
		WithArgs("franchise-123").
		WillReturnRows(sqlmock.NewRows([]string{"qualification_required"}).AddRow("full-time-owner"))

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(&testing.T{}))

	input := createTestInput(testUserUUIDBench, createHighScoreApplicationData())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}
