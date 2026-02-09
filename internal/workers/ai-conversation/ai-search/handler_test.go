// ============================================================
// FILE: internal/workers/ai-conversation/ai-search/handler_test.go
// ============================================================

package ai_search

import (
	"camunda-workers/internal/common/logger"
	"strings"
	"testing"
	"time"
)

// ============================================================
// BASIC TYPE DEFINITIONS FOR TESTING
// ============================================================

type TestLogger struct{}

func (t *TestLogger) Debug(msg string, fields map[string]interface{})        {}
func (t *TestLogger) Info(msg string, fields map[string]interface{})         {}
func (t *TestLogger) Warn(msg string, fields map[string]interface{})         {}
func (t *TestLogger) Error(msg string, fields map[string]interface{})        {}
func (t *TestLogger) With(fields map[string]interface{}) logger.Logger       { return t }
func (t *TestLogger) WithError(err error) logger.Logger                      { return t }
func (t *TestLogger) WithFields(fields map[string]interface{}) logger.Logger { return t }

// Test types for parameters
type RangeFilter struct {
	Min float64
	Max float64
}

type InvestmentFilter struct {
	Min int
	Max int
}

type LocationFilter struct {
	City string
}

type ExtractedParameters struct {
	Category       string
	Location       *LocationFilter
	Investment     *InvestmentFilter
	Rating         *float64
	Space          *RangeFilter
	Staff          *RangeFilter
	Outlets        *int
	ROI            *RangeFilter
	Verified       *bool
	TrustedSeller  *bool
}

type SearchInput struct {
	Query string
}

type SearchResults struct {
	Total    int64
	MaxScore float64
	TookMs   int64
	Hits     []map[string]interface{}
}

// ============================================================
// TEST UTILITIES
// ============================================================

func createTestConfig() *Config {
	return &Config{
		LLMEndpoint:     "http://localhost:11434",
		LLMModel:        "llama2",
		LLMMaxTokens:    1000,
		LLMTemperature:  0.1,
		LLMTimeout:      30 * time.Second,
		SearchTimeout:   10 * time.Second,
		DefaultPageSize: 20,
		MaxQueryLength:  500,
		IndexName:       "franchises",
	}
}

// Helper function to validate parameters (moved from test to helper)
func validateParameters(params *ExtractedParameters) error {
	if params == nil {
		return nil // Handler allows nil params
	}

	// Validate ROI
	if params.ROI != nil {
		if params.ROI.Min > params.ROI.Max {
			return fmt.Errorf("ROI min cannot be greater than max")
		}
		if params.ROI.Min < 0 || params.ROI.Max > 100 {
			return fmt.Errorf("ROI must be between 0 and 100")
		}
	}

	// Validate investment
	if params.Investment != nil {
		if params.Investment.Min < 0 {
			return fmt.Errorf("investment cannot be negative")
		}
		if params.Investment.Min > params.Investment.Max {
			return fmt.Errorf("investment min cannot be greater than max")
		}
	}

	// Validate rating
	if params.Rating != nil {
		if *params.Rating < 0 || *params.Rating > 5 {
			return fmt.Errorf("rating must be between 0 and 5")
		}
	}

	// Validate space
	if params.Space != nil {
		if params.Space.Min < 0 {
			return fmt.Errorf("space cannot be negative")
		}
		if params.Space.Min > params.Space.Max {
			return fmt.Errorf("space min cannot be greater than max")
		}
	}

	// Validate staff
	if params.Staff != nil {
		if params.Staff.Min < 0 {
			return fmt.Errorf("staff cannot be negative")
		}
		if params.Staff.Min > params.Staff.Max {
			return fmt.Errorf("staff min cannot be greater than max")
		}
	}

	return nil
}

// ============================================================
// SIMPLE UNIT TESTS
// ============================================================

func TestValidateInput(t *testing.T) {
	handler := &Handler{
		config: createTestConfig(),
		logger: &TestLogger{},
	}

	tests := []struct {
		name    string
		input   *SearchInput
		wantErr bool
	}{
		{
			name:    "Nil input - should error",
			input:   nil,
			wantErr: true,
		},
		{
			name: "Empty query - should pass (handler sets default)",
			input: &SearchInput{
				Query: "",
			},
			wantErr: false, // Handler will set it to "*" in validateInput
		},
		{
			name: "Valid query - should pass",
			input: &SearchInput{
				Query: "food franchises in Mumbai",
			},
			wantErr: false,
		},
		{
			name: "Query too long - should error",
			input: &SearchInput{
				Query: strings.Repeat("a", 501),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateInput(tt.input)

			if tt.wantErr && err == nil {
				t.Errorf("Expected error for test case: %s", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Unexpected error for test case %s: %v", tt.name, err)
			}
		})
	}
}

func TestValidateParameters(t *testing.T) {
	tests := []struct {
		name    string
		params  *ExtractedParameters
		wantErr bool
	}{
		{
			name:    "Nil parameters - should pass (handler allows nil)",
			params:  nil,
			wantErr: false,
		},
		{
			name: "Valid ROI - should pass",
			params: &ExtractedParameters{
				ROI: &RangeFilter{Min: 10, Max: 20},
			},
			wantErr: false,
		},
		{
			name: "ROI min > max - should error",
			params: &ExtractedParameters{
				ROI: &RangeFilter{Min: 30, Max: 20},
			},
			wantErr: true,
		},
		{
			name: "ROI out of range - should error",
			params: &ExtractedParameters{
				ROI: &RangeFilter{Min: -10, Max: 150},
			},
			wantErr: true,
		},
		{
			name: "Valid investment - should pass",
			params: &ExtractedParameters{
				Investment: &InvestmentFilter{Min: 1000000, Max: 5000000},
			},
			wantErr: false,
		},
		{
			name: "Negative investment - should error",
			params: &ExtractedParameters{
				Investment: &InvestmentFilter{Min: -1000000, Max: 5000000},
			},
			wantErr: true,
		},
		{
			name: "Valid rating - should pass",
			params: &ExtractedParameters{
				Rating: func() *float64 { r := 4.5; return &r }(),
			},
			wantErr: false,
		},
		{
			name: "Rating too high - should error",
			params: &ExtractedParameters{
				Rating: func() *float64 { r := 6.0; return &r }(),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateParameters(tt.params)

			if tt.wantErr && err == nil {
				t.Errorf("Expected error for test case: %s", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Unexpected error for test case %s: %v", tt.name, err)
			}
		})
	}
}

func TestBuildResponse(t *testing.T) {
	handler := &Handler{
		config: createTestConfig(),
		logger: &TestLogger{},
	}

	input := &SearchInput{
		Query: "food franchises in Delhi",
	}

	params := &ExtractedParameters{
		Category: "Food",
		Location: &LocationFilter{City: "Delhi"},
	}

	results := &SearchResults{
		Total:    5,
		MaxScore: 1.5,
		TookMs:   20,
		Hits: []map[string]interface{}{
			{"_source": map[string]interface{}{"name": "Franchise 1"}},
			{"_source": map[string]interface{}{"name": "Franchise 2"}},
		},
	}

	response := handler.buildResponse(input, params, results)

	// Basic response structure validation
	if response == nil {
		t.Fatal("Response should not be nil")
	}

	// Check success flag
	if success, ok := response["success"].(bool); !ok || !success {
		t.Error("Response should have success=true")
	}

	// Check extractedParams
	if extractedParams, ok := response["extractedParams"].(map[string]interface{}); !ok {
		t.Error("Response should have extractedParams field")
	} else {
		if query, ok := extractedParams["query"].(string); !ok || query != input.Query {
			t.Error("extractedParams should contain original query")
		}
	}

	// Check metadata
	if metadata, ok := response["metadata"].(map[string]interface{}); !ok {
		t.Error("Response should have metadata")
	} else {
		if _, ok := metadata["processed_at"].(string); !ok {
			t.Error("Metadata should have processed_at timestamp")
		}
		if total, ok := metadata["total_found"].(int64); !ok || total != results.Total {
			t.Error("Metadata should contain total_found")
		}
	}
}

// ============================================================
// BUILD ES QUERY TESTS
// ============================================================

func TestBuildElasticsearchQuery(t *testing.T) {
	handler := &Handler{
		config: createTestConfig(),
		logger: &TestLogger{},
	}

	tests := []struct {
		name          string
		params        *ExtractedParameters
		wantQueryKeys []string
	}{
		{
			name: "Category only query",
			params: &ExtractedParameters{
				Category: "Food & Beverage",
			},
			wantQueryKeys: []string{"query", "size"},
		},
		{
			name: "Query with location",
			params: &ExtractedParameters{
				Category: "Retail",
				Location: &LocationFilter{City: "Mumbai"},
			},
			wantQueryKeys: []string{"query", "size"},
		},
		{
			name: "Query with ROI filter",
			params: &ExtractedParameters{
				Category: "Education",
				ROI:      &RangeFilter{Min: 15, Max: 25},
			},
			wantQueryKeys: []string{"query", "size"},
		},
		{
			name:   "Nil parameters - match_all query",
			params: nil,
			wantQueryKeys: []string{"query", "size"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, err := handler.buildElasticsearchQuery(tt.params)

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if query == nil {
				t.Error("Query should not be nil")
				return
			}

			// Check required fields exist
			for _, key := range tt.wantQueryKeys {
				if _, ok := query[key]; !ok {
					t.Errorf("Query should have '%s' field", key)
				}
			}

			// Check size matches config
			if size, ok := query["size"].(int); !ok || size != handler.config.DefaultPageSize {
				t.Errorf("Expected size %d, got %v", handler.config.DefaultPageSize, query["size"])
			}
		})
	}
}

// ============================================================
// EDGE CASE TESTS
// ============================================================

func TestEdgeCases(t *testing.T) {
	handler := &Handler{
		config: createTestConfig(),
		logger: &TestLogger{},
	}

	t.Run("Zero values in ranges", func(t *testing.T) {
		params := &ExtractedParameters{
			ROI:        &RangeFilter{Min: 0, Max: 0},
			Investment: &InvestmentFilter{Min: 0, Max: 0},
		}

		err := validateParameters(params)
		if err != nil {
			t.Errorf("Zero values should be valid: %v", err)
		}
	})

	t.Run("Empty category with other filters", func(t *testing.T) {
		params := &ExtractedParameters{
			Category: "",
			Location: &LocationFilter{City: "Delhi"},
			Verified: func() *bool { v := true; return &v }(),
		}

		query, err := handler.buildElasticsearchQuery(params)
		if err != nil {
			t.Errorf("Should build query even with empty category: %v", err)
		}
		if query == nil {
			t.Error("Query should not be nil")
		}
	})
}

// ============================================================
// INTEGRATION TESTS (Full Flow Simulation)
// ============================================================

func TestCompleteSearchFlow(t *testing.T) {
	// Test the entire flow from input to response
	handler := &Handler{
		config: createTestConfig(),
		logger: &TestLogger{},
	}

	t.Run("Full search flow simulation", func(t *testing.T) {
		// Step 1: Create input
		input := &SearchInput{
			Query: "Find food franchises in Delhi with 20-30 lakhs investment",
		}

		// Step 2: Validate input
		err := handler.validateInput(input)
		if err != nil {
			t.Fatalf("Input validation failed: %v", err)
		}

		// Step 3: Simulate extracted parameters (normally done by LLM)
		params := &ExtractedParameters{
			Category:   "Food",
			Location:   &LocationFilter{City: "Delhi"},
			Investment: &InvestmentFilter{Min: 2000000, Max: 3000000},
			Rating:     func() *float64 { r := 4.0; return &r }(),
		}

		// Step 4: Validate parameters using helper
		err = validateParameters(params)
		if err != nil {
			t.Fatalf("Parameters validation failed: %v", err)
		}

		// Step 5: Build ES query
		esQuery, err := handler.buildElasticsearchQuery(params)
		if err != nil {
			t.Fatalf("Query building failed: %v", err)
		}

		// Step 6: Verify query structure
		if esQuery == nil {
			t.Fatal("ES query should not be nil")
		}

		// Check query has proper structure
		if _, ok := esQuery["query"]; !ok {
			t.Error("ES query should have 'query' field")
		}
		if _, ok := esQuery["size"]; !ok {
			t.Error("ES query should have 'size' field")
		}

		// Step 7: Simulate search results
		results := &SearchResults{
			Total:    3,
			MaxScore: 2.1,
			TookMs:   45,
			Hits: []map[string]interface{}{
				{
					"_source": map[string]interface{}{
						"name":     "Burger King",
						"industry": "Food & Beverage",
						"investment": map[string]interface{}{
							"min_investment": 2500000,
							"max_investment": 4000000,
						},
					},
				},
				{
					"_source": map[string]interface{}{
						"name":     "Domino's Pizza",
						"industry": "Food & Beverage",
						"investment": map[string]interface{}{
							"min_investment": 1500000,
							"max_investment": 3000000,
						},
					},
				},
			},
		}

		// Step 8: Build final response
		response := handler.buildResponse(input, params, results)

		// Step 9: Verify response
		if response == nil {
			t.Fatal("Response should not be nil")
		}

		// Check all required fields
		requiredFields := []string{"success", "extractedParams", "metadata"}
		for _, field := range requiredFields {
			if _, ok := response[field]; !ok {
				t.Errorf("Response missing required field: %s", field)
			}
		}

		// Verify success flag
		if success, ok := response["success"].(bool); !ok || !success {
			t.Error("Response should indicate success")
		}

		// Verify extractedParams has query
		if extractedParams, ok := response["extractedParams"].(map[string]interface{}); ok {
			if query, ok := extractedParams["query"].(string); !ok || query != input.Query {
				t.Error("extractedParams should contain original query")
			}
		}

		// Verify metadata has total_found
		if metadata, ok := response["metadata"].(map[string]interface{}); ok {
			if total, ok := metadata["total_found"].(int64); !ok || total != results.Total {
				t.Errorf("Expected %d results in metadata, got %v", results.Total, total)
			}
		}
	})

	t.Run("Error flow simulation", func(t *testing.T) {
		// Test error scenarios
		testCases := []struct {
			name      string
			input     *SearchInput
			params    *ExtractedParameters
			expectErr bool
		}{
			{
				name:      "Invalid input - query too long",
				input:     &SearchInput{Query: strings.Repeat("a", 501)},
				params:    nil,
				expectErr: true,
			},
			{
				name:      "Invalid parameters - ROI out of range",
				input:     &SearchInput{Query: "test query"},
				params:    &ExtractedParameters{ROI: &RangeFilter{Min: -10, Max: 200}},
				expectErr: true,
			},
			{
				name:      "Valid flow",
				input:     &SearchInput{Query: "valid query"},
				params:    &ExtractedParameters{Category: "Food"},
				expectErr: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				// Validate input
				inputErr := handler.validateInput(tc.input)

				// Validate parameters if provided
				var paramsErr error
				if tc.params != nil {
					paramsErr = validateParameters(tc.params)
				}

				// Check if we got expected errors
				hasErr := inputErr != nil || paramsErr != nil
				if hasErr != tc.expectErr {
					t.Errorf("Expected error: %v, got inputErr: %v, paramsErr: %v",
						tc.expectErr, inputErr, paramsErr)
				}

				// If no errors expected, try to build query
				if !tc.expectErr && tc.params != nil {
					query, err := handler.buildElasticsearchQuery(tc.params)
					if err != nil {
						t.Errorf("Should build query without error, got: %v", err)
					}
					if query == nil {
						t.Error("Query should not be nil")
					}
				}
			})
		}
	})
}

// ============================================================
// PERFORMANCE TESTS
// ============================================================

func BenchmarkBuildElasticsearchQuery(b *testing.B) {
	handler := &Handler{
		config: createTestConfig(),
		logger: &TestLogger{},
	}

	params := &ExtractedParameters{
		Category:   "Retail",
		Location:   &LocationFilter{City: "Mumbai"},
		ROI:        &RangeFilter{Min: 15, Max: 25},
		Investment: &InvestmentFilter{Min: 1000000, Max: 5000000},
		Rating:     func() *float64 { r := 4.0; return &r }(),
		Verified:   func() *bool { v := true; return &v }(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.buildElasticsearchQuery(params)
	}
}

func BenchmarkValidateParameters(b *testing.B) {
	params := &ExtractedParameters{
		Category:   "Food",
		Location:   &LocationFilter{City: "Delhi"},
		ROI:        &RangeFilter{Min: 10, Max: 20},
		Investment: &InvestmentFilter{Min: 2000000, Max: 5000000},
		Rating:     func() *float64 { r := 4.5; return &r }(),
		Verified:   func() *bool { v := true; return &v }(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validateParameters(params)
	}
}

func BenchmarkCompleteSearchFlow(b *testing.B) {
	handler := &Handler{
		config: createTestConfig(),
		logger: &TestLogger{},
	}

	input := &SearchInput{
		Query: "Find retail franchises in Mumbai with good ROI",
	}

	params := &ExtractedParameters{
		Category: "Retail",
		Location: &LocationFilter{City: "Mumbai"},
		ROI:      &RangeFilter{Min: 15, Max: 30},
		Rating:   func() *float64 { r := 4.0; return &r }(),
	}

	results := &SearchResults{
		Total:    10,
		MaxScore: 3.2,
		TookMs:   30,
		Hits:     []map[string]interface{}{},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate full flow
		_ = handler.validateInput(input)
		_ = validateParameters(params)
		_, _ = handler.buildElasticsearchQuery(params)
		_ = handler.buildResponse(input, params, results)
	}
}