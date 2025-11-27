// internal/workers/franchise/parse-search-filters/handler_test.go
package parsesearchfilters

import (
	"context"
	"encoding/json"
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

func createTestHandler(t *testing.T) *Handler {
	return NewHandler(createTestConfig(), newTestLogger(t))
}

func createInput(rawFilters map[string]interface{}) *Input {
	return &Input{
		RawFilters: rawFilters,
	}
}

// ==========================
// Core Functionality Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	tests := []struct {
		name           string
		input          *Input
		expectedOutput *Output
		validateOutput func(t *testing.T, output *Output)
	}{
		{
			name: "complete valid filters",
			input: createInput(map[string]interface{}{
				"categories": []string{"food", "retail"},
				"investmentRange": map[string]interface{}{
					"min": 50000,
					"max": 500000,
				},
				"locations": []string{"Texas", "California"},
				"keywords":  "fast food restaurant",
				"sortBy":    "investment_min",
				"pagination": map[string]interface{}{
					"page": 2,
					"size": 50,
				},
			}),
			expectedOutput: &Output{
				ParsedFilters: ParsedFilters{
					Categories: []string{"food", "retail"},
					InvestmentRange: InvestmentRange{
						Min: 50000,
						Max: 500000,
					},
					Locations: []string{"Texas", "California"},
					Keywords:  "fast food restaurant",
					SortBy:    "investment_min",
					Pagination: Pagination{
						Page: 2,
						Size: 50,
					},
				},
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, []string{"food", "retail"}, output.ParsedFilters.Categories)
				assert.Equal(t, 50000, output.ParsedFilters.InvestmentRange.Min)
				assert.Equal(t, 500000, output.ParsedFilters.InvestmentRange.Max)
				assert.Equal(t, []string{"Texas", "California"}, output.ParsedFilters.Locations)
				assert.Equal(t, "fast food restaurant", output.ParsedFilters.Keywords)
				assert.Equal(t, "investment_min", output.ParsedFilters.SortBy)
				assert.Equal(t, 2, output.ParsedFilters.Pagination.Page)
				assert.Equal(t, 50, output.ParsedFilters.Pagination.Size)
			},
		},
		{
			name: "minimal valid filters",
			input: createInput(map[string]interface{}{
				"categories": []string{"food"},
			}),
			expectedOutput: &Output{
				ParsedFilters: ParsedFilters{
					Categories: []string{"food"},
					InvestmentRange: InvestmentRange{
						Min: 0,
						Max: 10000000,
					},
					Locations: []string{},
					Keywords:  "",
					SortBy:    "relevance",
					Pagination: Pagination{
						Page: 1,
						Size: 20,
					},
				},
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, []string{"food"}, output.ParsedFilters.Categories)
				assert.Equal(t, 0, output.ParsedFilters.InvestmentRange.Min)
				assert.Equal(t, 10000000, output.ParsedFilters.InvestmentRange.Max)
				assert.Equal(t, "relevance", output.ParsedFilters.SortBy)
				assert.Equal(t, 1, output.ParsedFilters.Pagination.Page)
				assert.Equal(t, 20, output.ParsedFilters.Pagination.Size)
			},
		},
		{
			name:  "empty filters",
			input: createInput(map[string]interface{}{}),
			expectedOutput: &Output{
				ParsedFilters: ParsedFilters{
					Categories: []string{},
					InvestmentRange: InvestmentRange{
						Min: 0,
						Max: 10000000,
					},
					Locations: []string{},
					Keywords:  "",
					SortBy:    "relevance",
					Pagination: Pagination{
						Page: 1,
						Size: 20,
					},
				},
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Empty(t, output.ParsedFilters.Categories)
				assert.Empty(t, output.ParsedFilters.Locations)
				assert.Empty(t, output.ParsedFilters.Keywords)
				assert.Equal(t, "relevance", output.ParsedFilters.SortBy)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := createTestHandler(t)
			output, err := handler.Execute(context.Background(), tt.input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedOutput.ParsedFilters.Categories, output.ParsedFilters.Categories)
			assert.Equal(t, tt.expectedOutput.ParsedFilters.InvestmentRange, output.ParsedFilters.InvestmentRange)
			assert.Equal(t, tt.expectedOutput.ParsedFilters.Locations, output.ParsedFilters.Locations)
			assert.Equal(t, tt.expectedOutput.ParsedFilters.Keywords, output.ParsedFilters.Keywords)
			assert.Equal(t, tt.expectedOutput.ParsedFilters.SortBy, output.ParsedFilters.SortBy)
			assert.Equal(t, tt.expectedOutput.ParsedFilters.Pagination, output.ParsedFilters.Pagination)

			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}
		})
	}
}

func TestHandler_Execute_ValidationErrors(t *testing.T) {
	tests := []struct {
		name          string
		input         *Input
		expectedError string
	}{
		{
			name: "invalid category",
			input: createInput(map[string]interface{}{
				"categories": []string{"invalid_category"},
			}),
			expectedError: "INVALID_FILTER_FORMAT: invalid category 'invalid_category'",
		},
		{
			name: "invalid sortBy",
			input: createInput(map[string]interface{}{
				"sortBy": "invalid_sort",
			}),
			expectedError: "INVALID_FILTER_FORMAT: invalid sortBy 'invalid_sort'",
		},
		{
			name: "investment min greater than max",
			input: createInput(map[string]interface{}{
				"investmentRange": map[string]interface{}{
					"min": 500000,
					"max": 50000,
				},
			}),
			expectedError: "INVALID_FILTER_FORMAT: investment min (500000) > max (50000)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := createTestHandler(t)
			output, err := handler.Execute(context.Background(), tt.input)

			assert.Error(t, err)
			assert.Equal(t, tt.expectedError, err.Error())
			assert.Nil(t, output)
		})
	}
}

func TestHandler_Execute_NilRawFilters(t *testing.T) {
	handler := createTestHandler(t)
	input := &Input{} // RawFilters is nil

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	// Verify that empty parsed filters are returned
	assert.Empty(t, output.ParsedFilters.Categories)
	assert.Empty(t, output.ParsedFilters.Locations)
	assert.Empty(t, output.ParsedFilters.Keywords)
	assert.Equal(t, "relevance", output.ParsedFilters.SortBy)
	assert.Equal(t, 1, output.ParsedFilters.Pagination.Page)
	assert.Equal(t, 20, output.ParsedFilters.Pagination.Size)
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_ParseStringArray(t *testing.T) {
	handler := createTestHandler(t)

	tests := []struct {
		name     string
		input    interface{}
		expected []string
	}{
		{
			name:     "string with commas",
			input:    "food, retail, health",
			expected: []string{"food", "retail", "health"},
		},
		{
			name:     "string array interface",
			input:    []interface{}{"food", "retail", "health"},
			expected: []string{"food", "retail", "health"},
		},
		{
			name:     "string array",
			input:    []string{"food", "retail", "health"},
			expected: []string{"food", "retail", "health"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "nil input",
			input:    nil,
			expected: []string{},
		},
		{
			name:     "mixed types in array",
			input:    []interface{}{"food", 123, "retail"},
			expected: []string{"food", "retail"},
		},
		{
			name:     "with whitespace",
			input:    "  food ,  retail  , health  ",
			expected: []string{"food", "retail", "health"},
		},
		{
			name:     "empty strings filtered",
			input:    "food,,retail,",
			expected: []string{"food", "retail"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.parseStringArray(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_ParseInt(t *testing.T) {
	handler := createTestHandler(t)

	tests := []struct {
		name     string
		input    interface{}
		expected int
		wantErr  bool
	}{
		{
			name:     "float64",
			input:    float64(50000),
			expected: 50000,
			wantErr:  false,
		},
		{
			name:     "string number",
			input:    "50000",
			expected: 50000,
			wantErr:  false,
		},
		{
			name:     "string with commas",
			input:    "50,000",
			expected: 50000,
			wantErr:  false,
		},
		{
			name:     "string with dollar sign",
			input:    "$50,000",
			expected: 50000,
			wantErr:  false,
		},
		{
			name:     "string with mixed chars",
			input:    "USD 50,000.00",
			expected: 50000,
			wantErr:  false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "non-numeric string",
			input:    "not-a-number",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "nil input",
			input:    nil,
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "boolean input",
			input:    true,
			expected: 0,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := handler.parseInt(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	handler := createTestHandler(t)

	t.Run("investment range boundaries", func(t *testing.T) {
		input := createInput(map[string]interface{}{
			"investmentRange": map[string]interface{}{
				"min": -1000,    // Should be clamped to 0
				"max": 20000000, // Should be clamped to 10000000
			},
		})

		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.Equal(t, 0, output.ParsedFilters.InvestmentRange.Min)
		assert.Equal(t, 10000000, output.ParsedFilters.InvestmentRange.Max)
	})

	t.Run("pagination boundaries", func(t *testing.T) {
		input := createInput(map[string]interface{}{
			"pagination": map[string]interface{}{
				"page": 0,   // Should default to 1
				"size": 200, // Should be clamped to 100
			},
		})

		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.Equal(t, 1, output.ParsedFilters.Pagination.Page)
		assert.Equal(t, 100, output.ParsedFilters.Pagination.Size)
	})

	t.Run("mixed valid and invalid categories", func(t *testing.T) {
		input := createInput(map[string]interface{}{
			"categories": []string{"food", "invalid", "retail"},
		})

		output, err := handler.Execute(context.Background(), input)
		assert.Error(t, err)
		assert.Nil(t, output)
	})

	t.Run("duplicate categories", func(t *testing.T) {
		input := createInput(map[string]interface{}{
			"categories": []string{"food", "food", "retail"},
		})

		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.Equal(t, []string{"food", "retail"}, output.ParsedFilters.Categories)
	})

	t.Run("case sensitivity in categories", func(t *testing.T) {
		input := createInput(map[string]interface{}{
			"categories": []string{"Food", "RETAIL"}, // Should be lowercase
		})

		output, err := handler.Execute(context.Background(), input)
		assert.Error(t, err) // Categories are case-sensitive per validCategories map
		assert.Nil(t, output)
	})
}

func TestHandler_ValidCategoriesAndSortOptions(t *testing.T) {
	// Test that all predefined categories and sort options are valid
	assert.True(t, validCategories["food"])
	assert.True(t, validCategories["retail"])
	assert.True(t, validCategories["health"])
	assert.True(t, validCategories["education"])
	assert.True(t, validCategories["automotive"])
	assert.True(t, validCategories["fitness"])
	assert.True(t, validCategories["beauty"])
	assert.True(t, validCategories["home"])

	assert.True(t, validSortOptions["relevance"])
	assert.True(t, validSortOptions["investment_min"])
	assert.True(t, validSortOptions["name"])

	// Test some invalid ones
	assert.False(t, validCategories["invalid"])
	assert.False(t, validSortOptions["invalid"])
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	handler := createTestHandler(t)

	// Complex input with all filter types
	input := createInput(map[string]interface{}{
		"categories": []interface{}{"food", "retail"},
		"investmentRange": map[string]interface{}{
			"min": "$50,000",
			"max": "500,000 USD",
		},
		"locations": "Texas, California, New York",
		"keywords":  "  fast food franchise opportunity  ",
		"sortBy":    "name",
		"pagination": map[string]interface{}{
			"page": "2",
			"size": float64(25),
		},
	})

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)

	// Verify all filters were parsed correctly
	assert.Equal(t, []string{"food", "retail"}, output.ParsedFilters.Categories)
	assert.Equal(t, 50000, output.ParsedFilters.InvestmentRange.Min)
	assert.Equal(t, 500000, output.ParsedFilters.InvestmentRange.Max)
	assert.Equal(t, []string{"Texas", "California", "New York"}, output.ParsedFilters.Locations)
	assert.Equal(t, "fast food franchise opportunity", output.ParsedFilters.Keywords)
	assert.Equal(t, "name", output.ParsedFilters.SortBy)
	assert.Equal(t, 2, output.ParsedFilters.Pagination.Page)
	assert.Equal(t, 25, output.ParsedFilters.Pagination.Size)
}

// ==========================
// JSON Serialization Tests
// ==========================

func TestHandler_JSONSerialization(t *testing.T) {
	// Test that output can be properly serialized to JSON
	output := &Output{
		ParsedFilters: ParsedFilters{
			Categories: []string{"food", "retail"},
			InvestmentRange: InvestmentRange{
				Min: 50000,
				Max: 500000,
			},
			Locations: []string{"Texas", "California"},
			Keywords:  "test",
			SortBy:    "relevance",
			Pagination: Pagination{
				Page: 1,
				Size: 20,
			},
		},
	}

	jsonData, err := json.Marshal(output)
	assert.NoError(t, err)

	var decoded Output
	err = json.Unmarshal(jsonData, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, output.ParsedFilters, decoded.ParsedFilters)
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))

	input := createInput(map[string]interface{}{
		"categories": []string{"food", "retail", "health"},
		"investmentRange": map[string]interface{}{
			"min": 50000,
			"max": 500000,
		},
		"locations": []string{"Texas", "California"},
		"keywords":  "test keywords",
		"sortBy":    "relevance",
		"pagination": map[string]interface{}{
			"page": 1,
			"size": 20,
		},
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_ParseStringArray(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))

	inputs := []interface{}{
		"food,retail,health",
		[]interface{}{"food", "retail", "health"},
		[]string{"food", "retail", "health"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.parseStringArray(inputs[i%len(inputs)])
	}
}

func BenchmarkHandler_ParseInt(b *testing.B) {
	handler := NewHandler(createTestConfig(), newTestLogger(&testing.T{}))

	inputs := []interface{}{
		float64(50000),
		"50000",
		"$50,000",
		"USD 50,000.00",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.parseInt(inputs[i%len(inputs)])
	}
}

// // internal/workers/franchise/parse-search-filters/handler_test.go
// package parsesearchfilters

// import (
// 	"context"
// 	"encoding/json"
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

// func createTestHandler(t *testing.T) *Handler {
// 	return NewHandler(createTestConfig(), zaptest.NewLogger(t))
// }

// func createInput(rawFilters map[string]interface{}) *Input {
// 	return &Input{
// 		RawFilters: rawFilters,
// 	}
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name           string
// 		input          *Input
// 		expectedOutput *Output
// 		validateOutput func(t *testing.T, output *Output)
// 	}{
// 		{
// 			name: "complete valid filters",
// 			input: createInput(map[string]interface{}{
// 				"categories": []string{"food", "retail"},
// 				"investmentRange": map[string]interface{}{
// 					"min": 50000,
// 					"max": 500000,
// 				},
// 				"locations": []string{"Texas", "California"},
// 				"keywords":  "fast food restaurant",
// 				"sortBy":    "investment_min",
// 				"pagination": map[string]interface{}{
// 					"page": 2,
// 					"size": 50,
// 				},
// 			}),
// 			expectedOutput: &Output{
// 				ParsedFilters: ParsedFilters{
// 					Categories: []string{"food", "retail"},
// 					InvestmentRange: InvestmentRange{
// 						Min: 50000,
// 						Max: 500000,
// 					},
// 					Locations: []string{"Texas", "California"},
// 					Keywords:  "fast food restaurant",
// 					SortBy:    "investment_min",
// 					Pagination: Pagination{
// 						Page: 2,
// 						Size: 50,
// 					},
// 				},
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, []string{"food", "retail"}, output.ParsedFilters.Categories)
// 				assert.Equal(t, 50000, output.ParsedFilters.InvestmentRange.Min)
// 				assert.Equal(t, 500000, output.ParsedFilters.InvestmentRange.Max)
// 				assert.Equal(t, []string{"Texas", "California"}, output.ParsedFilters.Locations)
// 				assert.Equal(t, "fast food restaurant", output.ParsedFilters.Keywords)
// 				assert.Equal(t, "investment_min", output.ParsedFilters.SortBy)
// 				assert.Equal(t, 2, output.ParsedFilters.Pagination.Page)
// 				assert.Equal(t, 50, output.ParsedFilters.Pagination.Size)
// 			},
// 		},
// 		{
// 			name: "minimal valid filters",
// 			input: createInput(map[string]interface{}{
// 				"categories": []string{"food"},
// 			}),
// 			expectedOutput: &Output{
// 				ParsedFilters: ParsedFilters{
// 					Categories: []string{"food"},
// 					InvestmentRange: InvestmentRange{
// 						Min: 0,
// 						Max: 10000000,
// 					},
// 					Locations: []string{},
// 					Keywords:  "",
// 					SortBy:    "relevance",
// 					Pagination: Pagination{
// 						Page: 1,
// 						Size: 20,
// 					},
// 				},
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, []string{"food"}, output.ParsedFilters.Categories)
// 				assert.Equal(t, 0, output.ParsedFilters.InvestmentRange.Min)
// 				assert.Equal(t, 10000000, output.ParsedFilters.InvestmentRange.Max)
// 				assert.Equal(t, "relevance", output.ParsedFilters.SortBy)
// 				assert.Equal(t, 1, output.ParsedFilters.Pagination.Page)
// 				assert.Equal(t, 20, output.ParsedFilters.Pagination.Size)
// 			},
// 		},
// 		{
// 			name:  "empty filters",
// 			input: createInput(map[string]interface{}{}),
// 			expectedOutput: &Output{
// 				ParsedFilters: ParsedFilters{
// 					Categories: []string{},
// 					InvestmentRange: InvestmentRange{
// 						Min: 0,
// 						Max: 10000000,
// 					},
// 					Locations: []string{},
// 					Keywords:  "",
// 					SortBy:    "relevance",
// 					Pagination: Pagination{
// 						Page: 1,
// 						Size: 20,
// 					},
// 				},
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Empty(t, output.ParsedFilters.Categories)
// 				assert.Empty(t, output.ParsedFilters.Locations)
// 				assert.Empty(t, output.ParsedFilters.Keywords)
// 				assert.Equal(t, "relevance", output.ParsedFilters.SortBy)
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			handler := createTestHandler(t)
// 			output, err := handler.execute(context.Background(), tt.input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.Equal(t, tt.expectedOutput.ParsedFilters.Categories, output.ParsedFilters.Categories)
// 			assert.Equal(t, tt.expectedOutput.ParsedFilters.InvestmentRange, output.ParsedFilters.InvestmentRange)
// 			assert.Equal(t, tt.expectedOutput.ParsedFilters.Locations, output.ParsedFilters.Locations)
// 			assert.Equal(t, tt.expectedOutput.ParsedFilters.Keywords, output.ParsedFilters.Keywords)
// 			assert.Equal(t, tt.expectedOutput.ParsedFilters.SortBy, output.ParsedFilters.SortBy)
// 			assert.Equal(t, tt.expectedOutput.ParsedFilters.Pagination, output.ParsedFilters.Pagination)

// 			if tt.validateOutput != nil {
// 				tt.validateOutput(t, output)
// 			}
// 		})
// 	}
// }

// func TestHandler_Execute_ValidationErrors(t *testing.T) {
// 	tests := []struct {
// 		name          string
// 		input         *Input
// 		expectedError string
// 	}{
// 		{
// 			name: "invalid category",
// 			input: createInput(map[string]interface{}{
// 				"categories": []string{"invalid_category"},
// 			}),
// 			expectedError: "INVALID_FILTER_FORMAT: invalid category 'invalid_category'",
// 		},
// 		{
// 			name: "invalid sortBy",
// 			input: createInput(map[string]interface{}{
// 				"sortBy": "invalid_sort",
// 			}),
// 			expectedError: "INVALID_FILTER_FORMAT: invalid sortBy 'invalid_sort'",
// 		},
// 		{
// 			name: "investment min greater than max",
// 			input: createInput(map[string]interface{}{
// 				"investmentRange": map[string]interface{}{
// 					"min": 500000,
// 					"max": 50000,
// 				},
// 			}),
// 			expectedError: "INVALID_FILTER_FORMAT: investment min (500000) > max (50000)",
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			handler := createTestHandler(t)
// 			output, err := handler.execute(context.Background(), tt.input)

// 			assert.Error(t, err)
// 			assert.Equal(t, tt.expectedError, err.Error())
// 			assert.Nil(t, output)
// 		})
// 	}
// }

// func TestHandler_Execute_NilRawFilters(t *testing.T) {
// 	handler := createTestHandler(t)
// 	input := &Input{} // RawFilters is nil

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	// Verify that empty parsed filters are returned
// 	assert.Empty(t, output.ParsedFilters.Categories)
// 	assert.Empty(t, output.ParsedFilters.Locations)
// 	assert.Empty(t, output.ParsedFilters.Keywords)
// 	assert.Equal(t, "relevance", output.ParsedFilters.SortBy)
// 	assert.Equal(t, 1, output.ParsedFilters.Pagination.Page)
// 	assert.Equal(t, 20, output.ParsedFilters.Pagination.Size)
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_ParseStringArray(t *testing.T) {
// 	handler := createTestHandler(t)

// 	tests := []struct {
// 		name     string
// 		input    interface{}
// 		expected []string
// 	}{
// 		{
// 			name:     "string with commas",
// 			input:    "food, retail, health",
// 			expected: []string{"food", "retail", "health"},
// 		},
// 		{
// 			name:     "string array interface",
// 			input:    []interface{}{"food", "retail", "health"},
// 			expected: []string{"food", "retail", "health"},
// 		},
// 		{
// 			name:     "string array",
// 			input:    []string{"food", "retail", "health"},
// 			expected: []string{"food", "retail", "health"},
// 		},
// 		{
// 			name:     "empty string",
// 			input:    "",
// 			expected: []string{},
// 		},
// 		{
// 			name:     "nil input",
// 			input:    nil,
// 			expected: []string{},
// 		},
// 		{
// 			name:     "mixed types in array",
// 			input:    []interface{}{"food", 123, "retail"},
// 			expected: []string{"food", "retail"},
// 		},
// 		{
// 			name:     "with whitespace",
// 			input:    "  food ,  retail  , health  ",
// 			expected: []string{"food", "retail", "health"},
// 		},
// 		{
// 			name:     "empty strings filtered",
// 			input:    "food,,retail,",
// 			expected: []string{"food", "retail"},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result := handler.parseStringArray(tt.input)
// 			assert.Equal(t, tt.expected, result)
// 		})
// 	}
// }

// func TestHandler_ParseInt(t *testing.T) {
// 	handler := createTestHandler(t)

// 	tests := []struct {
// 		name     string
// 		input    interface{}
// 		expected int
// 		wantErr  bool
// 	}{
// 		{
// 			name:     "float64",
// 			input:    float64(50000),
// 			expected: 50000,
// 			wantErr:  false,
// 		},
// 		{
// 			name:     "string number",
// 			input:    "50000",
// 			expected: 50000,
// 			wantErr:  false,
// 		},
// 		{
// 			name:     "string with commas",
// 			input:    "50,000",
// 			expected: 50000,
// 			wantErr:  false,
// 		},
// 		{
// 			name:     "string with dollar sign",
// 			input:    "$50,000",
// 			expected: 50000,
// 			wantErr:  false,
// 		},
// 		{
// 			name:     "string with mixed chars",
// 			input:    "USD 50,000.00",
// 			expected: 50000,
// 			wantErr:  false,
// 		},
// 		{
// 			name:     "empty string",
// 			input:    "",
// 			expected: 0,
// 			wantErr:  true,
// 		},
// 		{
// 			name:     "non-numeric string",
// 			input:    "not-a-number",
// 			expected: 0,
// 			wantErr:  true,
// 		},
// 		{
// 			name:     "nil input",
// 			input:    nil,
// 			expected: 0,
// 			wantErr:  true,
// 		},
// 		{
// 			name:     "boolean input",
// 			input:    true,
// 			expected: 0,
// 			wantErr:  true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result, err := handler.parseInt(tt.input)
// 			if tt.wantErr {
// 				assert.Error(t, err)
// 			} else {
// 				assert.NoError(t, err)
// 				assert.Equal(t, tt.expected, result)
// 			}
// 		})
// 	}
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	handler := createTestHandler(t)

// 	t.Run("investment range boundaries", func(t *testing.T) {
// 		input := createInput(map[string]interface{}{
// 			"investmentRange": map[string]interface{}{
// 				"min": -1000,    // Should be clamped to 0
// 				"max": 20000000, // Should be clamped to 10000000
// 			},
// 		})

// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		assert.Equal(t, 0, output.ParsedFilters.InvestmentRange.Min)
// 		assert.Equal(t, 10000000, output.ParsedFilters.InvestmentRange.Max)
// 	})

// 	t.Run("pagination boundaries", func(t *testing.T) {
// 		input := createInput(map[string]interface{}{
// 			"pagination": map[string]interface{}{
// 				"page": 0,   // Should default to 1
// 				"size": 200, // Should be clamped to 100
// 			},
// 		})

// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		assert.Equal(t, 1, output.ParsedFilters.Pagination.Page)
// 		assert.Equal(t, 100, output.ParsedFilters.Pagination.Size)
// 	})

// 	t.Run("mixed valid and invalid categories", func(t *testing.T) {
// 		input := createInput(map[string]interface{}{
// 			"categories": []string{"food", "invalid", "retail"},
// 		})

// 		output, err := handler.execute(context.Background(), input)
// 		assert.Error(t, err)
// 		assert.Nil(t, output)
// 	})

// 	t.Run("duplicate categories", func(t *testing.T) {
// 		input := createInput(map[string]interface{}{
// 			"categories": []string{"food", "food", "retail"},
// 		})

// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		assert.Equal(t, []string{"food", "retail"}, output.ParsedFilters.Categories)
// 	})

// 	t.Run("case sensitivity in categories", func(t *testing.T) {
// 		input := createInput(map[string]interface{}{
// 			"categories": []string{"Food", "RETAIL"}, // Should be lowercase
// 		})

// 		output, err := handler.execute(context.Background(), input)
// 		assert.Error(t, err) // Categories are case-sensitive per validCategories map
// 		assert.Nil(t, output)
// 	})
// }

// func TestHandler_ValidCategoriesAndSortOptions(t *testing.T) {
// 	// Test that all predefined categories and sort options are valid
// 	assert.True(t, validCategories["food"])
// 	assert.True(t, validCategories["retail"])
// 	assert.True(t, validCategories["health"])
// 	assert.True(t, validCategories["education"])
// 	assert.True(t, validCategories["automotive"])
// 	assert.True(t, validCategories["fitness"])
// 	assert.True(t, validCategories["beauty"])
// 	assert.True(t, validCategories["home"])

// 	assert.True(t, validSortOptions["relevance"])
// 	assert.True(t, validSortOptions["investment_min"])
// 	assert.True(t, validSortOptions["name"])

// 	// Test some invalid ones
// 	assert.False(t, validCategories["invalid"])
// 	assert.False(t, validSortOptions["invalid"])
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	handler := createTestHandler(t)

// 	// Complex input with all filter types
// 	input := createInput(map[string]interface{}{
// 		"categories": []interface{}{"food", "retail"},
// 		"investmentRange": map[string]interface{}{
// 			"min": "$50,000",
// 			"max": "500,000 USD",
// 		},
// 		"locations": "Texas, California, New York",
// 		"keywords":  "  fast food franchise opportunity  ",
// 		"sortBy":    "name",
// 		"pagination": map[string]interface{}{
// 			"page": "2",
// 			"size": float64(25),
// 		},
// 	})

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)

// 	// Verify all filters were parsed correctly
// 	assert.Equal(t, []string{"food", "retail"}, output.ParsedFilters.Categories)
// 	assert.Equal(t, 50000, output.ParsedFilters.InvestmentRange.Min)
// 	assert.Equal(t, 500000, output.ParsedFilters.InvestmentRange.Max)
// 	assert.Equal(t, []string{"Texas", "California", "New York"}, output.ParsedFilters.Locations)
// 	assert.Equal(t, "fast food franchise opportunity", output.ParsedFilters.Keywords)
// 	assert.Equal(t, "name", output.ParsedFilters.SortBy)
// 	assert.Equal(t, 2, output.ParsedFilters.Pagination.Page)
// 	assert.Equal(t, 25, output.ParsedFilters.Pagination.Size)
// }

// // ==========================
// // JSON Serialization Tests
// // ==========================

// func TestHandler_JSONSerialization(t *testing.T) {
// 	// Test that output can be properly serialized to JSON
// 	output := &Output{
// 		ParsedFilters: ParsedFilters{
// 			Categories: []string{"food", "retail"},
// 			InvestmentRange: InvestmentRange{
// 				Min: 50000,
// 				Max: 500000,
// 			},
// 			Locations: []string{"Texas", "California"},
// 			Keywords:  "test",
// 			SortBy:    "relevance",
// 			Pagination: Pagination{
// 				Page: 1,
// 				Size: 20,
// 			},
// 		},
// 	}

// 	jsonData, err := json.Marshal(output)
// 	assert.NoError(t, err)

// 	var decoded Output
// 	err = json.Unmarshal(jsonData, &decoded)
// 	assert.NoError(t, err)
// 	assert.Equal(t, output.ParsedFilters, decoded.ParsedFilters)
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	input := createInput(map[string]interface{}{
// 		"categories": []string{"food", "retail", "health"},
// 		"investmentRange": map[string]interface{}{
// 			"min": 50000,
// 			"max": 500000,
// 		},
// 		"locations": []string{"Texas", "California"},
// 		"keywords":  "test keywords",
// 		"sortBy":    "relevance",
// 		"pagination": map[string]interface{}{
// 			"page": 1,
// 			"size": 20,
// 		},
// 	})

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), input)
// 	}
// }

// func BenchmarkHandler_ParseStringArray(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	inputs := []interface{}{
// 		"food,retail,health",
// 		[]interface{}{"food", "retail", "health"},
// 		[]string{"food", "retail", "health"},
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.parseStringArray(inputs[i%len(inputs)])
// 	}
// }

// func BenchmarkHandler_ParseInt(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	inputs := []interface{}{
// 		float64(50000),
// 		"50000",
// 		"$50,000",
// 		"USD 50,000.00",
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.parseInt(inputs[i%len(inputs)])
// 	}
// }
