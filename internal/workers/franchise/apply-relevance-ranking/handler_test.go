// internal/workers/franchise/apply-relevance-ranking/handler_test.go
package applyrelevanceranking

import (
	"context"
	"fmt"
	"testing"
	"time"

	"camunda-workers/internal/common/logger"

	"github.com/stretchr/testify/assert"
)

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{
		MaxItems: 100,
		Timeout:  3 * time.Second,
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

func createTestInput() *Input {
	return &Input{
		SearchResults: []SearchResult{
			{ID: "franchise-1", Score: 8.5},
			{ID: "franchise-2", Score: 7.2},
			{ID: "franchise-3", Score: 9.1},
		},
		DetailsData: []FranchiseDetail{
			{
				ID:               "franchise-1",
				Name:             "McDonald's",
				InvestmentMin:    1000000,
				InvestmentMax:    2200000,
				Category:         "Fast Food",
				Locations:        []string{"TX", "CA", "NY"},
				UpdatedAt:        time.Now().Add(-15 * 24 * time.Hour).Format(time.RFC3339), // 15 days ago
				ApplicationCount: 150,
				ViewCount:        500,
			},
			{
				ID:               "franchise-2",
				Name:             "Subway",
				InvestmentMin:    80000,
				InvestmentMax:    300000,
				Category:         "Sandwiches",
				Locations:        []string{"TX", "FL", "AZ"},
				UpdatedAt:        time.Now().Add(-60 * 24 * time.Hour).Format(time.RFC3339), // 60 days ago
				ApplicationCount: 80,
				ViewCount:        300,
			},
			{
				ID:               "franchise-3",
				Name:             "Starbucks",
				InvestmentMin:    300000,
				InvestmentMax:    700000,
				Category:         "Coffee",
				Locations:        []string{"CA", "WA", "NY"},
				UpdatedAt:        time.Now().Add(-200 * 24 * time.Hour).Format(time.RFC3339), // 200 days ago
				ApplicationCount: 200,
				ViewCount:        800,
			},
		},
		UserProfile: UserProfile{
			CapitalAvailable: 1500000,
			LocationPrefs:    []string{"TX", "CA"},
			Interests:        []string{"Fast Food", "Coffee"},
			ExperienceYears:  3,
		},
	}
}

func createMinimalInput() *Input {
	return &Input{
		SearchResults: []SearchResult{
			{ID: "franchise-1", Score: 5.0},
		},
		DetailsData: []FranchiseDetail{
			{
				ID:               "franchise-1",
				Name:             "Test Franchise",
				InvestmentMin:    50000,
				InvestmentMax:    200000,
				Category:         "Test Category",
				Locations:        []string{},
				UpdatedAt:        "",
				ApplicationCount: 0,
				ViewCount:        0,
			},
		},
		UserProfile: UserProfile{
			CapitalAvailable: 0,
			LocationPrefs:    []string{},
			Interests:        []string{},
			ExperienceYears:  0,
		},
	}
}

// ==========================
// Core Functionality Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	tests := []struct {
		name           string
		input          *Input
		validateOutput func(t *testing.T, output *Output)
	}{
		{
			name:  "complete matching data",
			input: createTestInput(),
			validateOutput: func(t *testing.T, output *Output) {
				assert.NotNil(t, output)
				assert.Equal(t, 3, len(output.RankedFranchises))
				assert.Greater(t, output.RankedFranchises[0].FinalScore, output.RankedFranchises[1].FinalScore)
				assert.Greater(t, output.RankedFranchises[1].FinalScore, output.RankedFranchises[2].FinalScore)

				// Verify all scores are within bounds
				for _, franchise := range output.RankedFranchises {
					assert.GreaterOrEqual(t, franchise.FinalScore, 0.0)
					assert.LessOrEqual(t, franchise.FinalScore, 100.0)
					assert.GreaterOrEqual(t, franchise.ESScore, 0.0)
					assert.LessOrEqual(t, franchise.ESScore, 100.0)
					assert.GreaterOrEqual(t, franchise.MatchScore, 0.0)
					assert.LessOrEqual(t, franchise.MatchScore, 100.0)
					assert.GreaterOrEqual(t, franchise.PopularityScore, 0.0)
					assert.LessOrEqual(t, franchise.PopularityScore, 100.0)
					assert.GreaterOrEqual(t, franchise.FreshnessScore, 0.0)
					assert.LessOrEqual(t, franchise.FreshnessScore, 100.0)
				}
			},
		},
		{
			name:  "minimal data",
			input: createMinimalInput(),
			validateOutput: func(t *testing.T, output *Output) {
				assert.NotNil(t, output)
				assert.Equal(t, 1, len(output.RankedFranchises))
				assert.Equal(t, "Test Franchise", output.RankedFranchises[0].Name)
				assert.Greater(t, output.RankedFranchises[0].FinalScore, 0.0)
			},
		},
		{
			name: "missing detail data",
			input: &Input{
				SearchResults: []SearchResult{
					{ID: "franchise-1", Score: 8.0},
					{ID: "franchise-2", Score: 7.0}, // No matching detail
				},
				DetailsData: []FranchiseDetail{
					{
						ID:               "franchise-1",
						Name:             "Available Franchise",
						InvestmentMin:    100000,
						InvestmentMax:    300000,
						Category:         "Test",
						Locations:        []string{"TX"},
						UpdatedAt:        time.Now().Format(time.RFC3339),
						ApplicationCount: 10,
						ViewCount:        50,
					},
				},
				UserProfile: UserProfile{CapitalAvailable: 200000},
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.NotNil(t, output)
				assert.Equal(t, 1, len(output.RankedFranchises)) // Only one with matching detail
				assert.Equal(t, "Available Franchise", output.RankedFranchises[0].Name)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(createTestConfig(), newTestLogger(t))
			output, err := handler.Execute(context.Background(), tt.input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			tt.validateOutput(t, output)
		})
	}
}

func TestHandler_Execute_Timeout(t *testing.T) {
	config := createTestConfig()
	config.Timeout = 1 * time.Millisecond
	handler := NewHandler(config, newTestLogger(t))

	// Create a large input to potentially cause timeout
	largeInput := &Input{
		SearchResults: make([]SearchResult, 1000),
		DetailsData:   make([]FranchiseDetail, 1000),
		UserProfile:   UserProfile{},
	}

	for i := 0; i < 1000; i++ {
		largeInput.SearchResults[i] = SearchResult{ID: fmt.Sprintf("f%d", i), Score: 5.0}
		largeInput.DetailsData[i] = FranchiseDetail{
			ID:               fmt.Sprintf("f%d", i),
			Name:             "Franchise",
			InvestmentMin:    100000,
			InvestmentMax:    200000,
			Category:         "Test",
			Locations:        []string{"TX"},
			UpdatedAt:        time.Now().Format(time.RFC3339),
			ApplicationCount: 10,
			ViewCount:        50,
		}
	}

	output, err := handler.Execute(context.Background(), largeInput)

	assert.NoError(t, err) // Should complete within timeout
	assert.NotNil(t, output)
	assert.Equal(t, 100, len(output.RankedFranchises), "Should be capped at MaxItems (100)")
}

func TestHandler_Execute_EmptyInput(t *testing.T) {
	tests := []struct {
		name  string
		input *Input
	}{
		{"empty search results", &Input{SearchResults: []SearchResult{}, DetailsData: []FranchiseDetail{}, UserProfile: UserProfile{}}},
		{"empty details data", &Input{SearchResults: []SearchResult{{ID: "test", Score: 5.0}}, DetailsData: []FranchiseDetail{}, UserProfile: UserProfile{}}},
		{"nil input", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(createTestConfig(), newTestLogger(t))
			output, err := handler.Execute(context.Background(), tt.input)

			if tt.input == nil {
				assert.Error(t, err)
				assert.Nil(t, output)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, output)
				assert.Equal(t, 0, len(output.RankedFranchises))
			}
		})
	}
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_CalculateMatchScore(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name     string
		detail   FranchiseDetail
		profile  UserProfile
		expected float64
	}{
		{
			name: "perfect match",
			detail: FranchiseDetail{
				InvestmentMin: 100000,
				InvestmentMax: 300000,
				Category:      "Fast Food",
				Locations:     []string{"TX", "CA"},
			},
			profile: UserProfile{
				CapitalAvailable: 200000,
				LocationPrefs:    []string{"TX"},
				Interests:        []string{"Fast Food"},
				ExperienceYears:  3,
			},
			expected: 100.0, // All components perfect
		},
		{
			name: "minimal profile",
			detail: FranchiseDetail{
				InvestmentMin: 100000,
				InvestmentMax: 300000,
				Category:      "Fast Food",
				Locations:     []string{"TX"},
			},
			profile: UserProfile{
				CapitalAvailable: 0,
				LocationPrefs:    []string{},
				Interests:        []string{},
				ExperienceYears:  0,
			},
			expected: 50.0, // Default score
		},
		{
			name: "financial above max",
			detail: FranchiseDetail{
				InvestmentMin: 100000,
				InvestmentMax: 300000,
				Category:      "Test",
				Locations:     []string{},
			},
			profile: UserProfile{
				CapitalAvailable: 500000,
				LocationPrefs:    []string{},
				Interests:        []string{},
				ExperienceYears:  0,
			},
			expected: 80.0 * 0.3, // Financial component only
		},
		{
			name: "location match",
			detail: FranchiseDetail{
				InvestmentMin: 100000,
				InvestmentMax: 300000,
				Category:      "Test",
				Locations:     []string{"TX", "CA"},
			},
			profile: UserProfile{
				CapitalAvailable: 0,
				LocationPrefs:    []string{"TX"},
				Interests:        []string{},
				ExperienceYears:  0,
			},
			expected: 100.0 * 0.2, // Location component only
		},
		{
			name: "interest match",
			detail: FranchiseDetail{
				InvestmentMin: 100000,
				InvestmentMax: 300000,
				Category:      "Coffee",
				Locations:     []string{},
			},
			profile: UserProfile{
				CapitalAvailable: 0,
				LocationPrefs:    []string{},
				Interests:        []string{"Coffee"},
				ExperienceYears:  0,
			},
			expected: 100.0 * 0.25, // Interest component only
		},
		{
			name: "experience match",
			detail: FranchiseDetail{
				InvestmentMin: 100000,
				InvestmentMax: 300000,
				Category:      "Test",
				Locations:     []string{},
			},
			profile: UserProfile{
				CapitalAvailable: 0,
				LocationPrefs:    []string{},
				Interests:        []string{},
				ExperienceYears:  3,
			},
			expected: 100.0 * 0.25, // Experience component only
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := handler.calculateMatchScore(&tt.detail, &tt.profile)
			assert.InDelta(t, tt.expected, score, 0.1)
			assert.GreaterOrEqual(t, score, 0.0)
			assert.LessOrEqual(t, score, 100.0)
		})
	}
}

func TestHandler_CalculateFreshnessScore(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	tests := []struct {
		name      string
		updatedAt string
		expected  float64
	}{
		{"recent (15 days)", time.Now().Add(-15 * 24 * time.Hour).Format(time.RFC3339), 100.0},
		{"recent (30 days)", time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339), 100.0},
		{"moderate (60 days)", time.Now().Add(-60 * 24 * time.Hour).Format(time.RFC3339), 80.0},
		{"moderate (90 days)", time.Now().Add(-90 * 24 * time.Hour).Format(time.RFC3339), 80.0},
		{"old (120 days)", time.Now().Add(-120 * 24 * time.Hour).Format(time.RFC3339), 60.0},
		{"old (180 days)", time.Now().Add(-180 * 24 * time.Hour).Format(time.RFC3339), 60.0},
		{"very old (200 days)", time.Now().Add(-200 * 24 * time.Hour).Format(time.RFC3339), 40.0},
		{"ancient (400 days)", time.Now().Add(-400 * 24 * time.Hour).Format(time.RFC3339), 20.0},
		{"invalid format", "invalid-date", 50.0},
		{"empty string", "", 50.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := handler.calculateFreshnessScore(tt.updatedAt)
			assert.Equal(t, tt.expected, score)
		})
	}
}

func TestHandler_MaxItemsRespected(t *testing.T) {
	config := createTestConfig()
	config.MaxItems = 2
	handler := NewHandler(config, newTestLogger(t))

	input := &Input{
		SearchResults: []SearchResult{
			{ID: "f1", Score: 9.0},
			{ID: "f2", Score: 8.0},
			{ID: "f3", Score: 7.0},
			{ID: "f4", Score: 6.0},
		},
		DetailsData: []FranchiseDetail{
			{ID: "f1", Name: "F1", InvestmentMin: 100000, InvestmentMax: 200000},
			{ID: "f2", Name: "F2", InvestmentMin: 100000, InvestmentMax: 200000},
			{ID: "f3", Name: "F3", InvestmentMin: 100000, InvestmentMax: 200000},
			{ID: "f4", Name: "F4", InvestmentMin: 100000, InvestmentMax: 200000},
		},
		UserProfile: UserProfile{CapitalAvailable: 150000},
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, 2, len(output.RankedFranchises))       // MaxItems respected
	assert.Equal(t, "F1", output.RankedFranchises[0].Name) // Highest score first
	assert.Equal(t, "F2", output.RankedFranchises[1].Name)
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	t.Run("negative elasticsearch score", func(t *testing.T) {
		input := &Input{
			SearchResults: []SearchResult{{ID: "f1", Score: -5.0}},
			DetailsData:   []FranchiseDetail{{ID: "f1", Name: "Test"}},
			UserProfile:   UserProfile{},
		}
		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.Equal(t, 0.0, output.RankedFranchises[0].ESScore) // Should be clamped to 0
	})

	t.Run("very high elasticsearch score", func(t *testing.T) {
		input := &Input{
			SearchResults: []SearchResult{{ID: "f1", Score: 50.0}}, // Very high score
			DetailsData:   []FranchiseDetail{{ID: "f1", Name: "Test"}},
			UserProfile:   UserProfile{},
		}
		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.Equal(t, 100.0, output.RankedFranchises[0].ESScore) // Should be clamped to 100
	})

	t.Run("duplicate franchise IDs", func(t *testing.T) {
		input := &Input{
			SearchResults: []SearchResult{
				{ID: "f1", Score: 8.0},
				{ID: "f1", Score: 9.0}, // Duplicate ID
			},
			DetailsData: []FranchiseDetail{
				{ID: "f1", Name: "Test", InvestmentMin: 100000, InvestmentMax: 200000},
			},
			UserProfile: UserProfile{},
		}
		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(output.RankedFranchises)) // Should deduplicate
	})

	t.Run("zero investment range", func(t *testing.T) {
		input := &Input{
			SearchResults: []SearchResult{{ID: "f1", Score: 5.0}},
			DetailsData: []FranchiseDetail{
				{ID: "f1", Name: "Test", InvestmentMin: 0, InvestmentMax: 0},
			},
			UserProfile: UserProfile{CapitalAvailable: 100000},
		}
		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.Greater(t, output.RankedFranchises[0].FinalScore, 0.0)
	})

	t.Run("negative popularity metrics", func(t *testing.T) {
		input := &Input{
			SearchResults: []SearchResult{{ID: "f1", Score: 5.0}},
			DetailsData: []FranchiseDetail{
				{ID: "f1", Name: "Test", ApplicationCount: -10, ViewCount: -5},
			},
			UserProfile: UserProfile{},
		}
		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.Equal(t, 0.0, output.RankedFranchises[0].PopularityScore) // Should handle negative
	})
}

func TestHandler_ScoreDistribution(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	input := createTestInput()
	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)

	// Verify score distribution makes sense
	for _, franchise := range output.RankedFranchises {
		// Final score should be weighted combination of components
		expectedFinal := (franchise.ESScore * 0.4) +
			(franchise.MatchScore * 0.3) +
			(franchise.PopularityScore * 0.2) +
			(franchise.FreshnessScore * 0.1)

		assert.InDelta(t, expectedFinal, franchise.FinalScore, 0.001)
	}
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	handler := NewHandler(createTestConfig(), newTestLogger(t))

	input := &Input{
		SearchResults: []SearchResult{
			{ID: "mcdonalds", Score: 9.2},
			{ID: "subway", Score: 7.8},
			{ID: "starbucks", Score: 8.5},
		},
		DetailsData: []FranchiseDetail{
			{
				ID:               "mcdonalds",
				Name:             "McDonald's",
				InvestmentMin:    1000000,
				InvestmentMax:    2200000,
				Category:         "Fast Food",
				Locations:        []string{"TX", "CA", "NY"},
				UpdatedAt:        time.Now().Add(-10 * 24 * time.Hour).Format(time.RFC3339),
				ApplicationCount: 200,
				ViewCount:        800,
			},
			{
				ID:               "subway",
				Name:             "Subway",
				InvestmentMin:    80000,
				InvestmentMax:    300000,
				Category:         "Sandwiches",
				Locations:        []string{"TX", "FL"},
				UpdatedAt:        time.Now().Add(-45 * 24 * time.Hour).Format(time.RFC3339),
				ApplicationCount: 120,
				ViewCount:        400,
			},
			{
				ID:               "starbucks",
				Name:             "Starbucks",
				InvestmentMin:    300000,
				InvestmentMax:    700000,
				Category:         "Coffee",
				Locations:        []string{"CA", "WA"},
				UpdatedAt:        time.Now().Add(-100 * 24 * time.Hour).Format(time.RFC3339),
				ApplicationCount: 180,
				ViewCount:        700,
			},
		},
		UserProfile: UserProfile{
			CapitalAvailable: 1500000,
			LocationPrefs:    []string{"TX", "CA"},
			Interests:        []string{"Fast Food", "Coffee"},
			ExperienceYears:  5,
		},
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, 3, len(output.RankedFranchises))

	// Verify ranking order makes sense
	assert.Greater(t, output.RankedFranchises[0].FinalScore, output.RankedFranchises[1].FinalScore)
	assert.Greater(t, output.RankedFranchises[1].FinalScore, output.RankedFranchises[2].FinalScore)

	// Verify all components are calculated
	for i, franchise := range output.RankedFranchises {
		assert.NotEmpty(t, franchise.ID)
		assert.NotEmpty(t, franchise.Name)
		assert.Greater(t, franchise.FinalScore, 0.0)
		assert.Greater(t, franchise.ESScore, 0.0)
		assert.Greater(t, franchise.MatchScore, 0.0)
		assert.Greater(t, franchise.PopularityScore, 0.0)
		assert.Greater(t, franchise.FreshnessScore, 0.0)
		assert.LessOrEqual(t, franchise.FinalScore, 100.0)

		t.Logf("Rank %d: %s - Score: %.2f (ES: %.2f, Match: %.2f, Pop: %.2f, Fresh: %.2f)",
			i+1, franchise.Name, franchise.FinalScore,
			franchise.ESScore, franchise.MatchScore,
			franchise.PopularityScore, franchise.FreshnessScore)
	}
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))

	// Create benchmark input
	input := &Input{
		SearchResults: make([]SearchResult, 100),
		DetailsData:   make([]FranchiseDetail, 100),
		UserProfile: UserProfile{
			CapitalAvailable: 500000,
			LocationPrefs:    []string{"TX", "CA"},
			Interests:        []string{"Fast Food", "Coffee"},
			ExperienceYears:  3,
		},
	}

	for i := 0; i < 100; i++ {
		input.SearchResults[i] = SearchResult{
			ID:    string(rune('a' + i)),
			Score: float64(i%10) + 1.0,
		}
		input.DetailsData[i] = FranchiseDetail{
			ID:               string(rune('a' + i)),
			Name:             "Franchise " + string(rune('A'+(i%26))),
			InvestmentMin:    50000 + (i * 10000),
			InvestmentMax:    200000 + (i * 50000),
			Category:         "Category " + string(rune('A'+(i%5))),
			Locations:        []string{"TX", "CA", "NY", "FL"},
			UpdatedAt:        time.Now().Add(-time.Duration(i%365) * 24 * time.Hour).Format(time.RFC3339),
			ApplicationCount: i * 10,
			ViewCount:        i * 50,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_CalculateMatchScore(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))

	detail := FranchiseDetail{
		InvestmentMin: 100000,
		InvestmentMax: 300000,
		Category:      "Fast Food",
		Locations:     []string{"TX", "CA", "NY", "FL"},
	}
	profile := UserProfile{
		CapitalAvailable: 200000,
		LocationPrefs:    []string{"TX", "CA"},
		Interests:        []string{"Fast Food", "Coffee"},
		ExperienceYears:  5,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.calculateMatchScore(&detail, &profile)
	}
}

func BenchmarkHandler_CalculateFreshnessScore(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))

	updatedAt := time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.calculateFreshnessScore(updatedAt)
	}
}

// // internal/workers/franchise/apply-relevance-ranking/handler_test.go
// package applyrelevanceranking

// import (
// 	"context"
// 	"fmt"
// 	"testing"
// 	"time"

// 	"github.com/stretchr/testify/assert"
// 	"go.uber.org/zap/zaptest"
// )

// // ==========================
// // Test Helper Functions
// // ==========================

// func createTestConfig() *Config {
// 	return &Config{
// 		MaxItems: 100,
// 		Timeout:  3 * time.Second,
// 	}
// }

// func createTestInput() *Input {
// 	return &Input{
// 		SearchResults: []SearchResult{
// 			{ID: "franchise-1", Score: 8.5},
// 			{ID: "franchise-2", Score: 7.2},
// 			{ID: "franchise-3", Score: 9.1},
// 		},
// 		DetailsData: []FranchiseDetail{
// 			{
// 				ID:               "franchise-1",
// 				Name:             "McDonald's",
// 				InvestmentMin:    1000000,
// 				InvestmentMax:    2200000,
// 				Category:         "Fast Food",
// 				Locations:        []string{"TX", "CA", "NY"},
// 				UpdatedAt:        time.Now().Add(-15 * 24 * time.Hour).Format(time.RFC3339), // 15 days ago
// 				ApplicationCount: 150,
// 				ViewCount:        500,
// 			},
// 			{
// 				ID:               "franchise-2",
// 				Name:             "Subway",
// 				InvestmentMin:    80000,
// 				InvestmentMax:    300000,
// 				Category:         "Sandwiches",
// 				Locations:        []string{"TX", "FL", "AZ"},
// 				UpdatedAt:        time.Now().Add(-60 * 24 * time.Hour).Format(time.RFC3339), // 60 days ago
// 				ApplicationCount: 80,
// 				ViewCount:        300,
// 			},
// 			{
// 				ID:               "franchise-3",
// 				Name:             "Starbucks",
// 				InvestmentMin:    300000,
// 				InvestmentMax:    700000,
// 				Category:         "Coffee",
// 				Locations:        []string{"CA", "WA", "NY"},
// 				UpdatedAt:        time.Now().Add(-200 * 24 * time.Hour).Format(time.RFC3339), // 200 days ago
// 				ApplicationCount: 200,
// 				ViewCount:        800,
// 			},
// 		},
// 		UserProfile: UserProfile{
// 			CapitalAvailable: 1500000,
// 			LocationPrefs:    []string{"TX", "CA"},
// 			Interests:        []string{"Fast Food", "Coffee"},
// 			ExperienceYears:  3,
// 		},
// 	}
// }

// func createMinimalInput() *Input {
// 	return &Input{
// 		SearchResults: []SearchResult{
// 			{ID: "franchise-1", Score: 5.0},
// 		},
// 		DetailsData: []FranchiseDetail{
// 			{
// 				ID:               "franchise-1",
// 				Name:             "Test Franchise",
// 				InvestmentMin:    50000,
// 				InvestmentMax:    200000,
// 				Category:         "Test Category",
// 				Locations:        []string{},
// 				UpdatedAt:        "",
// 				ApplicationCount: 0,
// 				ViewCount:        0,
// 			},
// 		},
// 		UserProfile: UserProfile{
// 			CapitalAvailable: 0,
// 			LocationPrefs:    []string{},
// 			Interests:        []string{},
// 			ExperienceYears:  0,
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
// 		validateOutput func(t *testing.T, output *Output)
// 	}{
// 		{
// 			name:  "complete matching data",
// 			input: createTestInput(),
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.NotNil(t, output)
// 				assert.Equal(t, 3, len(output.RankedFranchises))
// 				assert.Greater(t, output.RankedFranchises[0].FinalScore, output.RankedFranchises[1].FinalScore)
// 				assert.Greater(t, output.RankedFranchises[1].FinalScore, output.RankedFranchises[2].FinalScore)

// 				// Verify all scores are within bounds
// 				for _, franchise := range output.RankedFranchises {
// 					assert.GreaterOrEqual(t, franchise.FinalScore, 0.0)
// 					assert.LessOrEqual(t, franchise.FinalScore, 100.0)
// 					assert.GreaterOrEqual(t, franchise.ESScore, 0.0)
// 					assert.LessOrEqual(t, franchise.ESScore, 100.0)
// 					assert.GreaterOrEqual(t, franchise.MatchScore, 0.0)
// 					assert.LessOrEqual(t, franchise.MatchScore, 100.0)
// 					assert.GreaterOrEqual(t, franchise.PopularityScore, 0.0)
// 					assert.LessOrEqual(t, franchise.PopularityScore, 100.0)
// 					assert.GreaterOrEqual(t, franchise.FreshnessScore, 0.0)
// 					assert.LessOrEqual(t, franchise.FreshnessScore, 100.0)
// 				}
// 			},
// 		},
// 		{
// 			name:  "minimal data",
// 			input: createMinimalInput(),
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.NotNil(t, output)
// 				assert.Equal(t, 1, len(output.RankedFranchises))
// 				assert.Equal(t, "Test Franchise", output.RankedFranchises[0].Name)
// 				assert.Greater(t, output.RankedFranchises[0].FinalScore, 0.0)
// 			},
// 		},
// 		{
// 			name: "missing detail data",
// 			input: &Input{
// 				SearchResults: []SearchResult{
// 					{ID: "franchise-1", Score: 8.0},
// 					{ID: "franchise-2", Score: 7.0}, // No matching detail
// 				},
// 				DetailsData: []FranchiseDetail{
// 					{
// 						ID:               "franchise-1",
// 						Name:             "Available Franchise",
// 						InvestmentMin:    100000,
// 						InvestmentMax:    300000,
// 						Category:         "Test",
// 						Locations:        []string{"TX"},
// 						UpdatedAt:        time.Now().Format(time.RFC3339),
// 						ApplicationCount: 10,
// 						ViewCount:        50,
// 					},
// 				},
// 				UserProfile: UserProfile{CapitalAvailable: 200000},
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.NotNil(t, output)
// 				assert.Equal(t, 1, len(output.RankedFranchises)) // Only one with matching detail
// 				assert.Equal(t, "Available Franchise", output.RankedFranchises[0].Name)
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))
// 			output, err := handler.execute(context.Background(), tt.input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			tt.validateOutput(t, output)
// 		})
// 	}
// }

// func TestHandler_Execute_Timeout(t *testing.T) {
// 	config := createTestConfig()
// 	config.Timeout = 1 * time.Millisecond
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	// Create a large input to potentially cause timeout
// 	largeInput := &Input{
// 		SearchResults: make([]SearchResult, 1000),
// 		DetailsData:   make([]FranchiseDetail, 1000),
// 		UserProfile:   UserProfile{},
// 	}

// 	for i := 0; i < 1000; i++ {
// 		largeInput.SearchResults[i] = SearchResult{ID: fmt.Sprintf("f%d", i), Score: 5.0}
// 		largeInput.DetailsData[i] = FranchiseDetail{
// 			ID:               fmt.Sprintf("f%d", i),
// 			Name:             "Franchise",
// 			InvestmentMin:    100000,
// 			InvestmentMax:    200000,
// 			Category:         "Test",
// 			Locations:        []string{"TX"},
// 			UpdatedAt:        time.Now().Format(time.RFC3339),
// 			ApplicationCount: 10,
// 			ViewCount:        50,
// 		}
// 	}

// 	output, err := handler.execute(context.Background(), largeInput)

// 	assert.NoError(t, err) // Should complete within timeout
// 	assert.NotNil(t, output)
// 	assert.Equal(t, 100, len(output.RankedFranchises), "Should be capped at MaxItems (100)")
// }

// func TestHandler_Execute_EmptyInput(t *testing.T) {
// 	tests := []struct {
// 		name  string
// 		input *Input
// 	}{
// 		{"empty search results", &Input{SearchResults: []SearchResult{}, DetailsData: []FranchiseDetail{}, UserProfile: UserProfile{}}},
// 		{"empty details data", &Input{SearchResults: []SearchResult{{ID: "test", Score: 5.0}}, DetailsData: []FranchiseDetail{}, UserProfile: UserProfile{}}},
// 		{"nil input", nil},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))
// 			output, err := handler.execute(context.Background(), tt.input)

// 			if tt.input == nil {
// 				assert.Error(t, err)
// 				assert.Nil(t, output)
// 			} else {
// 				assert.NoError(t, err)
// 				assert.NotNil(t, output)
// 				assert.Equal(t, 0, len(output.RankedFranchises))
// 			}
// 		})
// 	}
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_CalculateMatchScore(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		detail   FranchiseDetail
// 		profile  UserProfile
// 		expected float64
// 	}{
// 		{
// 			name: "perfect match",
// 			detail: FranchiseDetail{
// 				InvestmentMin: 100000,
// 				InvestmentMax: 300000,
// 				Category:      "Fast Food",
// 				Locations:     []string{"TX", "CA"},
// 			},
// 			profile: UserProfile{
// 				CapitalAvailable: 200000,
// 				LocationPrefs:    []string{"TX"},
// 				Interests:        []string{"Fast Food"},
// 				ExperienceYears:  3,
// 			},
// 			expected: 100.0, // All components perfect
// 		},
// 		{
// 			name: "minimal profile",
// 			detail: FranchiseDetail{
// 				InvestmentMin: 100000,
// 				InvestmentMax: 300000,
// 				Category:      "Fast Food",
// 				Locations:     []string{"TX"},
// 			},
// 			profile: UserProfile{
// 				CapitalAvailable: 0,
// 				LocationPrefs:    []string{},
// 				Interests:        []string{},
// 				ExperienceYears:  0,
// 			},
// 			expected: 50.0, // Default score
// 		},
// 		{
// 			name: "financial above max",
// 			detail: FranchiseDetail{
// 				InvestmentMin: 100000,
// 				InvestmentMax: 300000,
// 				Category:      "Test",
// 				Locations:     []string{},
// 			},
// 			profile: UserProfile{
// 				CapitalAvailable: 500000,
// 				LocationPrefs:    []string{},
// 				Interests:        []string{},
// 				ExperienceYears:  0,
// 			},
// 			expected: 80.0 * 0.3, // Financial component only
// 		},
// 		{
// 			name: "location match",
// 			detail: FranchiseDetail{
// 				InvestmentMin: 100000,
// 				InvestmentMax: 300000,
// 				Category:      "Test",
// 				Locations:     []string{"TX", "CA"},
// 			},
// 			profile: UserProfile{
// 				CapitalAvailable: 0,
// 				LocationPrefs:    []string{"TX"},
// 				Interests:        []string{},
// 				ExperienceYears:  0,
// 			},
// 			expected: 100.0 * 0.2, // Location component only
// 		},
// 		{
// 			name: "interest match",
// 			detail: FranchiseDetail{
// 				InvestmentMin: 100000,
// 				InvestmentMax: 300000,
// 				Category:      "Coffee",
// 				Locations:     []string{},
// 			},
// 			profile: UserProfile{
// 				CapitalAvailable: 0,
// 				LocationPrefs:    []string{},
// 				Interests:        []string{"Coffee"},
// 				ExperienceYears:  0,
// 			},
// 			expected: 100.0 * 0.25, // Interest component only
// 		},
// 		{
// 			name: "experience match",
// 			detail: FranchiseDetail{
// 				InvestmentMin: 100000,
// 				InvestmentMax: 300000,
// 				Category:      "Test",
// 				Locations:     []string{},
// 			},
// 			profile: UserProfile{
// 				CapitalAvailable: 0,
// 				LocationPrefs:    []string{},
// 				Interests:        []string{},
// 				ExperienceYears:  3,
// 			},
// 			expected: 100.0 * 0.25, // Experience component only
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			score := handler.calculateMatchScore(&tt.detail, &tt.profile)
// 			assert.InDelta(t, tt.expected, score, 0.1)
// 			assert.GreaterOrEqual(t, score, 0.0)
// 			assert.LessOrEqual(t, score, 100.0)
// 		})
// 	}
// }

// func TestHandler_CalculateFreshnessScore(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name      string
// 		updatedAt string
// 		expected  float64
// 	}{
// 		{"recent (15 days)", time.Now().Add(-15 * 24 * time.Hour).Format(time.RFC3339), 100.0},
// 		{"recent (30 days)", time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339), 100.0},
// 		{"moderate (60 days)", time.Now().Add(-60 * 24 * time.Hour).Format(time.RFC3339), 80.0},
// 		{"moderate (90 days)", time.Now().Add(-90 * 24 * time.Hour).Format(time.RFC3339), 80.0},
// 		{"old (120 days)", time.Now().Add(-120 * 24 * time.Hour).Format(time.RFC3339), 60.0},
// 		{"old (180 days)", time.Now().Add(-180 * 24 * time.Hour).Format(time.RFC3339), 60.0},
// 		{"very old (200 days)", time.Now().Add(-200 * 24 * time.Hour).Format(time.RFC3339), 40.0},
// 		{"ancient (400 days)", time.Now().Add(-400 * 24 * time.Hour).Format(time.RFC3339), 20.0},
// 		{"invalid format", "invalid-date", 50.0},
// 		{"empty string", "", 50.0},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			score := handler.calculateFreshnessScore(tt.updatedAt)
// 			assert.Equal(t, tt.expected, score)
// 		})
// 	}
// }

// func TestHandler_MaxItemsRespected(t *testing.T) {
// 	config := createTestConfig()
// 	config.MaxItems = 2
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	input := &Input{
// 		SearchResults: []SearchResult{
// 			{ID: "f1", Score: 9.0},
// 			{ID: "f2", Score: 8.0},
// 			{ID: "f3", Score: 7.0},
// 			{ID: "f4", Score: 6.0},
// 		},
// 		DetailsData: []FranchiseDetail{
// 			{ID: "f1", Name: "F1", InvestmentMin: 100000, InvestmentMax: 200000},
// 			{ID: "f2", Name: "F2", InvestmentMin: 100000, InvestmentMax: 200000},
// 			{ID: "f3", Name: "F3", InvestmentMin: 100000, InvestmentMax: 200000},
// 			{ID: "f4", Name: "F4", InvestmentMin: 100000, InvestmentMax: 200000},
// 		},
// 		UserProfile: UserProfile{CapitalAvailable: 150000},
// 	}

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.Equal(t, 2, len(output.RankedFranchises))       // MaxItems respected
// 	assert.Equal(t, "F1", output.RankedFranchises[0].Name) // Highest score first
// 	assert.Equal(t, "F2", output.RankedFranchises[1].Name)
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	t.Run("negative elasticsearch score", func(t *testing.T) {
// 		input := &Input{
// 			SearchResults: []SearchResult{{ID: "f1", Score: -5.0}},
// 			DetailsData:   []FranchiseDetail{{ID: "f1", Name: "Test"}},
// 			UserProfile:   UserProfile{},
// 		}
// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		assert.Equal(t, 0.0, output.RankedFranchises[0].ESScore) // Should be clamped to 0
// 	})

// 	t.Run("very high elasticsearch score", func(t *testing.T) {
// 		input := &Input{
// 			SearchResults: []SearchResult{{ID: "f1", Score: 50.0}}, // Very high score
// 			DetailsData:   []FranchiseDetail{{ID: "f1", Name: "Test"}},
// 			UserProfile:   UserProfile{},
// 		}
// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		assert.Equal(t, 100.0, output.RankedFranchises[0].ESScore) // Should be clamped to 100
// 	})

// 	t.Run("duplicate franchise IDs", func(t *testing.T) {
// 		input := &Input{
// 			SearchResults: []SearchResult{
// 				{ID: "f1", Score: 8.0},
// 				{ID: "f1", Score: 9.0}, // Duplicate ID
// 			},
// 			DetailsData: []FranchiseDetail{
// 				{ID: "f1", Name: "Test", InvestmentMin: 100000, InvestmentMax: 200000},
// 			},
// 			UserProfile: UserProfile{},
// 		}
// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		assert.Equal(t, 1, len(output.RankedFranchises)) // Should deduplicate
// 	})

// 	t.Run("zero investment range", func(t *testing.T) {
// 		input := &Input{
// 			SearchResults: []SearchResult{{ID: "f1", Score: 5.0}},
// 			DetailsData: []FranchiseDetail{
// 				{ID: "f1", Name: "Test", InvestmentMin: 0, InvestmentMax: 0},
// 			},
// 			UserProfile: UserProfile{CapitalAvailable: 100000},
// 		}
// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		assert.Greater(t, output.RankedFranchises[0].FinalScore, 0.0)
// 	})

// 	t.Run("negative popularity metrics", func(t *testing.T) {
// 		input := &Input{
// 			SearchResults: []SearchResult{{ID: "f1", Score: 5.0}},
// 			DetailsData: []FranchiseDetail{
// 				{ID: "f1", Name: "Test", ApplicationCount: -10, ViewCount: -5},
// 			},
// 			UserProfile: UserProfile{},
// 		}
// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		assert.Equal(t, 0.0, output.RankedFranchises[0].PopularityScore) // Should handle negative
// 	})
// }

// func TestHandler_ScoreDistribution(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	input := createTestInput()
// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)

// 	// Verify score distribution makes sense
// 	for _, franchise := range output.RankedFranchises {
// 		// Final score should be weighted combination of components
// 		expectedFinal := (franchise.ESScore * 0.4) +
// 			(franchise.MatchScore * 0.3) +
// 			(franchise.PopularityScore * 0.2) +
// 			(franchise.FreshnessScore * 0.1)

// 		assert.InDelta(t, expectedFinal, franchise.FinalScore, 0.001)
// 	}
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	input := &Input{
// 		SearchResults: []SearchResult{
// 			{ID: "mcdonalds", Score: 9.2},
// 			{ID: "subway", Score: 7.8},
// 			{ID: "starbucks", Score: 8.5},
// 		},
// 		DetailsData: []FranchiseDetail{
// 			{
// 				ID:               "mcdonalds",
// 				Name:             "McDonald's",
// 				InvestmentMin:    1000000,
// 				InvestmentMax:    2200000,
// 				Category:         "Fast Food",
// 				Locations:        []string{"TX", "CA", "NY"},
// 				UpdatedAt:        time.Now().Add(-10 * 24 * time.Hour).Format(time.RFC3339),
// 				ApplicationCount: 200,
// 				ViewCount:        800,
// 			},
// 			{
// 				ID:               "subway",
// 				Name:             "Subway",
// 				InvestmentMin:    80000,
// 				InvestmentMax:    300000,
// 				Category:         "Sandwiches",
// 				Locations:        []string{"TX", "FL"},
// 				UpdatedAt:        time.Now().Add(-45 * 24 * time.Hour).Format(time.RFC3339),
// 				ApplicationCount: 120,
// 				ViewCount:        400,
// 			},
// 			{
// 				ID:               "starbucks",
// 				Name:             "Starbucks",
// 				InvestmentMin:    300000,
// 				InvestmentMax:    700000,
// 				Category:         "Coffee",
// 				Locations:        []string{"CA", "WA"},
// 				UpdatedAt:        time.Now().Add(-100 * 24 * time.Hour).Format(time.RFC3339),
// 				ApplicationCount: 180,
// 				ViewCount:        700,
// 			},
// 		},
// 		UserProfile: UserProfile{
// 			CapitalAvailable: 1500000,
// 			LocationPrefs:    []string{"TX", "CA"},
// 			Interests:        []string{"Fast Food", "Coffee"},
// 			ExperienceYears:  5,
// 		},
// 	}

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.Equal(t, 3, len(output.RankedFranchises))

// 	// Verify ranking order makes sense
// 	assert.Greater(t, output.RankedFranchises[0].FinalScore, output.RankedFranchises[1].FinalScore)
// 	assert.Greater(t, output.RankedFranchises[1].FinalScore, output.RankedFranchises[2].FinalScore)

// 	// Verify all components are calculated
// 	for i, franchise := range output.RankedFranchises {
// 		assert.NotEmpty(t, franchise.ID)
// 		assert.NotEmpty(t, franchise.Name)
// 		assert.Greater(t, franchise.FinalScore, 0.0)
// 		assert.Greater(t, franchise.ESScore, 0.0)
// 		assert.Greater(t, franchise.MatchScore, 0.0)
// 		assert.Greater(t, franchise.PopularityScore, 0.0)
// 		assert.Greater(t, franchise.FreshnessScore, 0.0)
// 		assert.LessOrEqual(t, franchise.FinalScore, 100.0)

// 		t.Logf("Rank %d: %s - Score: %.2f (ES: %.2f, Match: %.2f, Pop: %.2f, Fresh: %.2f)",
// 			i+1, franchise.Name, franchise.FinalScore,
// 			franchise.ESScore, franchise.MatchScore,
// 			franchise.PopularityScore, franchise.FreshnessScore)
// 	}
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	// Create benchmark input
// 	input := &Input{
// 		SearchResults: make([]SearchResult, 100),
// 		DetailsData:   make([]FranchiseDetail, 100),
// 		UserProfile: UserProfile{
// 			CapitalAvailable: 500000,
// 			LocationPrefs:    []string{"TX", "CA"},
// 			Interests:        []string{"Fast Food", "Coffee"},
// 			ExperienceYears:  3,
// 		},
// 	}

// 	for i := 0; i < 100; i++ {
// 		input.SearchResults[i] = SearchResult{
// 			ID:    string(rune('a' + i)),
// 			Score: float64(i%10) + 1.0,
// 		}
// 		input.DetailsData[i] = FranchiseDetail{
// 			ID:               string(rune('a' + i)),
// 			Name:             "Franchise " + string(rune('A'+(i%26))),
// 			InvestmentMin:    50000 + (i * 10000),
// 			InvestmentMax:    200000 + (i * 50000),
// 			Category:         "Category " + string(rune('A'+(i%5))),
// 			Locations:        []string{"TX", "CA", "NY", "FL"},
// 			UpdatedAt:        time.Now().Add(-time.Duration(i%365) * 24 * time.Hour).Format(time.RFC3339),
// 			ApplicationCount: i * 10,
// 			ViewCount:        i * 50,
// 		}
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), input)
// 	}
// }

// func BenchmarkHandler_CalculateMatchScore(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	detail := FranchiseDetail{
// 		InvestmentMin: 100000,
// 		InvestmentMax: 300000,
// 		Category:      "Fast Food",
// 		Locations:     []string{"TX", "CA", "NY", "FL"},
// 	}
// 	profile := UserProfile{
// 		CapitalAvailable: 200000,
// 		LocationPrefs:    []string{"TX", "CA"},
// 		Interests:        []string{"Fast Food", "Coffee"},
// 		ExperienceYears:  5,
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.calculateMatchScore(&detail, &profile)
// 	}
// }

// func BenchmarkHandler_CalculateFreshnessScore(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	updatedAt := time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339)

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.calculateFreshnessScore(updatedAt)
// 	}
// }

// // // internal/workers/franchise/apply-relevance-ranking/handler_test.go
// // package applyrelevanceranking

// // import (
// // 	"context"
// // 	"testing"
// // 	"time"

// // 	"github.com/stretchr/testify/assert"
// // 	"go.uber.org/zap"
// // )

// // func TestHandler_execute_Ranking(t *testing.T) {
// // 	handler := NewHandler(&Config{MaxItems: 10, Timeout: 10 * time.Second}, zap.NewNop())

// // 	now := time.Now().Format(time.RFC3339)

// // 	input := &Input{
// // 		SearchResults: []SearchResult{
// // 			{ID: "fran1", Score: 5.0},
// // 			{ID: "fran2", Score: 3.0},
// // 		},
// // 		DetailsData: []FranchiseDetail{
// // 			{
// // 				ID:               "fran1",
// // 				Name:             "McDonald's",
// // 				InvestmentMin:    500000,
// // 				InvestmentMax:    2000000,
// // 				Category:         "food",
// // 				Locations:        []string{"Texas"},
// // 				UpdatedAt:        now,
// // 				ApplicationCount: 100,
// // 				ViewCount:        1000,
// // 			},
// // 			{
// // 				ID:               "fran2",
// // 				Name:             "Starbucks",
// // 				InvestmentMin:    300000,
// // 				InvestmentMax:    1500000,
// // 				Category:         "food",
// // 				Locations:        []string{"California"},
// // 				UpdatedAt:        now,
// // 				ApplicationCount: 50,
// // 				ViewCount:        500,
// // 			},
// // 		},
// // 		UserProfile: UserProfile{
// // 			CapitalAvailable: 1000000,
// // 			LocationPrefs:    []string{"Texas"},
// // 			Interests:        []string{"food"},
// // 			ExperienceYears:  5,
// // 		},
// // 	}

// // 	output, err := handler.execute(context.Background(), input)
// // 	assert.NoError(t, err)
// // 	assert.Len(t, output.RankedFranchises, 2)
// // 	assert.Equal(t, "McDonald's", output.RankedFranchises[0].Name)
// // 	assert.Greater(t, output.RankedFranchises[0].FinalScore, output.RankedFranchises[1].FinalScore)
// // }

// // func TestHandler_execute_MissingDetail(t *testing.T) {
// // 	handler := NewHandler(&Config{MaxItems: 10, Timeout: 10 * time.Second}, zap.NewNop())

// // 	input := &Input{
// // 		SearchResults: []SearchResult{
// // 			{ID: "fran1", Score: 5.0},
// // 		},
// // 		DetailsData: []FranchiseDetail{},
// // 		UserProfile: UserProfile{},
// // 	}

// // 	output, err := handler.execute(context.Background(), input)
// // 	assert.NoError(t, err)
// // 	assert.Len(t, output.RankedFranchises, 0)
// // }
