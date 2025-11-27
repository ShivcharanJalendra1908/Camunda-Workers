// internal/workers/ai-conversation/parse-user-intent/handler_test.go
package parseuserintent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ==========================
// Test Logger Implementation
// ==========================

// TestLogger implements the Logger interface for testing
type TestLogger struct {
	t      *testing.T
	fields map[string]interface{}
}

func NewTestLogger(t *testing.T) *TestLogger {
	return &TestLogger{
		t:      t,
		fields: make(map[string]interface{}),
	}
}

func (l *TestLogger) Info(msg string, fields map[string]interface{}) {
	allFields := l.mergeFields(fields)
	l.t.Logf("INFO: %s %v", msg, allFields)
}

func (l *TestLogger) Warn(msg string, fields map[string]interface{}) {
	allFields := l.mergeFields(fields)
	l.t.Logf("WARN: %s %v", msg, allFields)
}

func (l *TestLogger) Error(msg string, fields map[string]interface{}) {
	allFields := l.mergeFields(fields)
	l.t.Logf("ERROR: %s %v", msg, allFields)
}

func (l *TestLogger) With(fields map[string]interface{}) Logger {
	newLogger := &TestLogger{
		t:      l.t,
		fields: make(map[string]interface{}),
	}

	// Copy existing fields
	for k, v := range l.fields {
		newLogger.fields[k] = v
	}

	// Add new fields
	for k, v := range fields {
		newLogger.fields[k] = v
	}

	return newLogger
}

func (l *TestLogger) mergeFields(fields map[string]interface{}) map[string]interface{} {
	allFields := make(map[string]interface{})

	// Add base fields
	for k, v := range l.fields {
		allFields[k] = v
	}

	// Add method-specific fields
	for k, v := range fields {
		allFields[k] = v
	}

	return allFields
}

// BenchmarkLogger is a minimal logger for benchmarks
type BenchmarkLogger struct{}

func (b *BenchmarkLogger) Info(msg string, fields map[string]interface{})  {}
func (b *BenchmarkLogger) Warn(msg string, fields map[string]interface{})  {}
func (b *BenchmarkLogger) Error(msg string, fields map[string]interface{}) {}
func (b *BenchmarkLogger) With(fields map[string]interface{}) Logger       { return b }

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{
		GenAIBaseURL: "http://localhost:8080",
		Timeout:      30 * time.Second,
		MaxRetries:   2,
	}
}

func createIntentAPIResponse(intent string, confidence float64, entities []Entity, dataSources []string) string {
	response := map[string]interface{}{
		"intent":      intent,
		"confidence":  confidence,
		"entities":    entities,
		"dataSources": dataSources,
	}
	data, _ := json.Marshal(response)
	return string(data)
}

// ==========================
// Core Functionality Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	tests := []struct {
		name            string
		input           *Input
		apiResponse     string
		expectedIntent  string
		expectedConf    float64
		expectedEnts    int
		expectedSrcs    []string
		validateOutput  func(t *testing.T, output *Output)
		validateRequest func(t *testing.T, reqBody map[string]interface{})
	}{
		{
			name: "complete response with explicit data sources",
			input: &Input{
				Question: "What are McDonald's franchise opportunities in Texas?",
				Context: map[string]interface{}{
					"userType": "prospective_franchisee",
				},
			},
			apiResponse: createIntentAPIResponse(
				"franchise_opportunity_inquiry",
				0.92,
				[]Entity{
					{Type: "franchise_name", Value: "McDonald's"},
					{Type: "location", Value: "Texas"},
				},
				[]string{"internal_db", "search_index", "external_web"},
			),
			expectedIntent: "franchise_opportunity_inquiry",
			expectedConf:   0.92,
			expectedEnts:   2,
			expectedSrcs:   []string{"internal_db", "search_index", "external_web"},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "franchise_opportunity_inquiry", output.IntentAnalysis.PrimaryIntent)
				assert.True(t, output.IntentAnalysis.Confidence > 0.9)
				assert.Equal(t, 2, len(output.Entities))
				assert.Equal(t, 3, len(output.DataSources))
			},
		},
		{
			name: "response with auto-determined data sources",
			input: &Input{
				Question: "Tell me about Subway",
				Context:  map[string]interface{}{},
			},
			apiResponse: createIntentAPIResponse(
				"general_info",
				0.85,
				[]Entity{
					{Type: "franchise_name", Value: "Subway"},
				},
				[]string{}, // Empty to trigger auto-determination
			),
			expectedIntent: "general_info",
			expectedConf:   0.85,
			expectedEnts:   1,
			expectedSrcs:   []string{"internal_db", "search_index", "external_web"},
		},
		{
			name: "market research intent",
			input: &Input{
				Question: "Compare McDonald's and Burger King franchise costs",
				Context: map[string]interface{}{
					"industry": "fast_food",
				},
			},
			apiResponse: createIntentAPIResponse(
				"market_research",
				0.88,
				[]Entity{
					{Type: "franchise_name", Value: "McDonald's"},
					{Type: "franchise_name", Value: "Burger King"},
					{Type: "category", Value: "franchise_costs"},
				},
				[]string{}, // Empty to trigger auto-determination
			),
			expectedIntent: "market_research",
			expectedConf:   0.88,
			expectedEnts:   3,
			expectedSrcs:   []string{"internal_db", "search_index", "external_web"},
		},
		{
			name: "minimal response",
			input: &Input{
				Question: "Hello",
				Context:  nil,
			},
			apiResponse: createIntentAPIResponse(
				"greeting",
				0.95,
				[]Entity{},
				[]string{"internal_db"},
			),
			expectedIntent: "greeting",
			expectedConf:   0.95,
			expectedEnts:   0,
			expectedSrcs:   []string{"internal_db"},
			validateRequest: func(t *testing.T, reqBody map[string]interface{}) {
				// When Context is nil, it should not be present in the request body
				_, hasContext := reqBody["context"]
				assert.False(t, hasContext, "context should not be in request when nil")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request
				assert.Equal(t, "POST", r.Method)
				assert.Equal(t, "/api/ai/parse-intent", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				// Verify request body structure
				var reqBody map[string]interface{}
				err := json.NewDecoder(r.Body).Decode(&reqBody)
				assert.NoError(t, err)
				assert.Equal(t, tt.input.Question, reqBody["query"])

				if tt.validateRequest != nil {
					tt.validateRequest(t, reqBody)
				} else if tt.input.Context != nil {
					assert.Equal(t, tt.input.Context, reqBody["context"])
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(tt.apiResponse))
			}))
			defer server.Close()

			config := createTestConfig()
			config.GenAIBaseURL = server.URL
			handler := NewHandler(config, NewTestLogger(t))

			output, err := handler.execute(context.Background(), tt.input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedIntent, output.IntentAnalysis.PrimaryIntent)
			assert.Equal(t, tt.expectedConf, output.IntentAnalysis.Confidence)
			assert.Equal(t, tt.expectedEnts, len(output.Entities))
			assert.Equal(t, tt.expectedSrcs, output.DataSources)

			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}
		})
	}
}

func TestHandler_Execute_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond) // slow API
	}))
	defer server.Close()

	config := createTestConfig()
	config.GenAIBaseURL = server.URL
	config.Timeout = 50 * time.Millisecond
	handler := NewHandler(config, NewTestLogger(t))

	input := &Input{
		Question: "Test question",
		Context:  map[string]interface{}{},
	}

	start := time.Now()
	output, err := handler.execute(context.Background(), input)
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrIntentAPITimeout))
	assert.Nil(t, output)

	// NEW CORRECT EXPECTATION:
	// timeout happens *immediately* (no retries)
	assert.Less(t, elapsed, 150*time.Millisecond)
}

func TestHandler_Execute_APIError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"Internal Server Error", http.StatusInternalServerError},
		{"Bad Gateway", http.StatusBadGateway},
		{"Service Unavailable", http.StatusServiceUnavailable},
		{"Bad Request", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			config := createTestConfig()
			config.GenAIBaseURL = server.URL
			config.MaxRetries = 0 // Disable retries for this test
			handler := NewHandler(config, NewTestLogger(t))

			input := &Input{Question: "Test question"}
			output, err := handler.execute(context.Background(), input)

			assert.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), "INTENT_PARSING_FAILED"))
			assert.Nil(t, output)
		})
	}
}

func TestHandler_Execute_RetryLogic(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			// Fail first attempt
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// Succeed on retry
		response := createIntentAPIResponse("success", 0.9, []Entity{}, []string{"internal_db"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := createTestConfig()
	config.GenAIBaseURL = server.URL
	config.MaxRetries = 2
	handler := NewHandler(config, NewTestLogger(t))

	output, err := handler.execute(context.Background(), &Input{Question: "Test"})

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, "success", output.IntentAnalysis.PrimaryIntent)
	assert.GreaterOrEqual(t, attempts, 2)
}

func TestHandler_Execute_MalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid json {{{"))
	}))
	defer server.Close()

	config := createTestConfig()
	config.GenAIBaseURL = server.URL
	handler := NewHandler(config, NewTestLogger(t))

	output, err := handler.execute(context.Background(), &Input{Question: "Test"})

	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "INTENT_PARSING_FAILED"))
	assert.Nil(t, output)
}

func TestHandler_Execute_InvalidConfidence(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
	}{
		{"negative confidence", -0.5},
		{"confidence > 1", 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := createIntentAPIResponse("test_intent", tt.confidence, []Entity{}, []string{"internal_db"})
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(response))
			}))
			defer server.Close()

			config := createTestConfig()
			config.GenAIBaseURL = server.URL
			handler := NewHandler(config, NewTestLogger(t))

			output, err := handler.execute(context.Background(), &Input{Question: "Test"})

			assert.NoError(t, err)
			assert.Equal(t, tt.confidence, output.IntentAnalysis.Confidence)
		})
	}
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_DetermineDataSources(t *testing.T) {
	handler := NewHandler(createTestConfig(), NewTestLogger(t))

	tests := []struct {
		name     string
		intent   string
		entities []Entity
		expected []string
	}{
		{
			name:   "franchise name entity only",
			intent: "franchise_inquiry",
			entities: []Entity{
				{Type: "franchise_name", Value: "McDonald's"},
			},
			expected: []string{"internal_db", "search_index"},
		},
		{
			name:   "category entity",
			intent: "category_search",
			entities: []Entity{
				{Type: "category", Value: "fast_food"},
			},
			expected: []string{"internal_db", "search_index"},
		},
		{
			name:   "general info intent",
			intent: "general_info",
			entities: []Entity{
				{Type: "franchise_name", Value: "Subway"},
			},
			expected: []string{"internal_db", "search_index", "external_web"},
		},
		{
			name:   "market research intent",
			intent: "market_research",
			entities: []Entity{
				{Type: "franchise_name", Value: "Burger King"},
			},
			expected: []string{"internal_db", "search_index", "external_web"},
		},
		{
			name:   "competitor analysis intent",
			intent: "competitor_analysis",
			entities: []Entity{
				{Type: "franchise_name", Value: "KFC"},
			},
			expected: []string{"internal_db", "search_index", "external_web"},
		},
		{
			name:     "no entities, basic intent",
			intent:   "greeting",
			entities: []Entity{},
			expected: []string{"internal_db"},
		},
		{
			name:   "location entity only (should not trigger search)",
			intent: "location_inquiry",
			entities: []Entity{
				{Type: "location", Value: "Texas"},
			},
			expected: []string{"internal_db"},
		},
		{
			name:   "investment amount only (should not trigger search)",
			intent: "cost_inquiry",
			entities: []Entity{
				{Type: "investment_amount", Value: "100000"},
			},
			expected: []string{"internal_db"},
		},
		{
			name:   "multiple relevant entities",
			intent: "general_info",
			entities: []Entity{
				{Type: "franchise_name", Value: "McDonald's"},
				{Type: "category", Value: "fast_food"},
				{Type: "location", Value: "Texas"},
			},
			expected: []string{"internal_db", "search_index", "external_web"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.determineDataSources(tt.intent, tt.entities)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	t.Run("empty question", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var reqBody map[string]interface{}
			json.NewDecoder(r.Body).Decode(&reqBody)
			assert.Equal(t, "", reqBody["query"])

			response := createIntentAPIResponse("unknown", 0.1, []Entity{}, []string{"internal_db"})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))
		}))
		defer server.Close()

		config := createTestConfig()
		config.GenAIBaseURL = server.URL
		handler := NewHandler(config, NewTestLogger(t))

		output, err := handler.execute(context.Background(), &Input{Question: ""})
		assert.NoError(t, err)
		assert.NotNil(t, output)
	})

	t.Run("nil context", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var reqBody map[string]interface{}
			json.NewDecoder(r.Body).Decode(&reqBody)
			_, hasContext := reqBody["context"]
			assert.False(t, hasContext, "context should not be in request body when nil")

			response := createIntentAPIResponse("test", 0.8, []Entity{}, []string{"internal_db"})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))
		}))
		defer server.Close()

		config := createTestConfig()
		config.GenAIBaseURL = server.URL
		handler := NewHandler(config, NewTestLogger(t))

		output, err := handler.execute(context.Background(), &Input{Question: "Test", Context: nil})
		assert.NoError(t, err)
		assert.NotNil(t, output)
	})

	t.Run("special characters in question", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var reqBody map[string]interface{}
			json.NewDecoder(r.Body).Decode(&reqBody)
			assert.Contains(t, reqBody["query"], "McDonald's")

			response := createIntentAPIResponse("test", 0.8, []Entity{}, []string{"internal_db"})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))
		}))
		defer server.Close()

		config := createTestConfig()
		config.GenAIBaseURL = server.URL
		handler := NewHandler(config, NewTestLogger(t))

		input := &Input{
			Question: "What about \"McDonald's\" franchise?",
			Context:  map[string]interface{}{},
		}
		output, err := handler.execute(context.Background(), input)
		assert.NoError(t, err)
		assert.NotNil(t, output)
	})

	t.Run("large context data", func(t *testing.T) {
		largeContext := make(map[string]interface{})
		for i := 0; i < 100; i++ {
			key := "key_" + string(rune('a'+(i%26)))
			largeContext[key] = "test value"
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var reqBody map[string]interface{}
			json.NewDecoder(r.Body).Decode(&reqBody)
			assert.NotNil(t, reqBody["context"])

			response := createIntentAPIResponse("test", 0.8, []Entity{}, []string{"internal_db"})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))
		}))
		defer server.Close()

		config := createTestConfig()
		config.GenAIBaseURL = server.URL
		handler := NewHandler(config, NewTestLogger(t))

		input := &Input{
			Question: "Test question",
			Context:  largeContext,
		}
		output, err := handler.execute(context.Background(), input)
		assert.NoError(t, err)
		assert.NotNil(t, output)
	})
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify complete request structure
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		assert.Equal(t, "Comprehensive franchise inquiry", reqBody["query"])
		assert.NotNil(t, reqBody["context"])

		context := reqBody["context"].(map[string]interface{})
		assert.Equal(t, "prospective_buyer", context["userType"])

		response := createIntentAPIResponse(
			"franchise_opportunity_inquiry",
			0.94,
			[]Entity{
				{Type: "franchise_name", Value: "McDonald's"},
				{Type: "location", Value: "Texas"},
				{Type: "investment_amount", Value: "500000"},
			},
			[]string{"internal_db", "search_index", "external_web"},
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := createTestConfig()
	config.GenAIBaseURL = server.URL
	handler := NewHandler(config, NewTestLogger(t))

	input := &Input{
		Question: "Comprehensive franchise inquiry",
		Context: map[string]interface{}{
			"userType": "prospective_buyer",
			"industry": "fast_food",
		},
	}

	output, err := handler.execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, "franchise_opportunity_inquiry", output.IntentAnalysis.PrimaryIntent)
	assert.True(t, output.IntentAnalysis.Confidence > 0.9)
	assert.Equal(t, 3, len(output.Entities))
	assert.Equal(t, 3, len(output.DataSources))

	// Verify entities
	entityTypes := make(map[string]bool)
	for _, entity := range output.Entities {
		entityTypes[entity.Type] = true
	}
	assert.True(t, entityTypes["franchise_name"])
	assert.True(t, entityTypes["location"])
	assert.True(t, entityTypes["investment_amount"])
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := createIntentAPIResponse("test_intent", 0.8, []Entity{}, []string{"internal_db"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := createTestConfig()
	config.GenAIBaseURL = server.URL
	handler := NewHandler(config, &BenchmarkLogger{})

	input := &Input{
		Question: "Test question",
		Context:  map[string]interface{}{"test": "value"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.execute(context.Background(), input)
	}
}

func BenchmarkHandler_DetermineDataSources(b *testing.B) {
	handler := NewHandler(createTestConfig(), &BenchmarkLogger{})

	entities := []Entity{
		{Type: "franchise_name", Value: "McDonald's"},
		{Type: "location", Value: "Texas"},
		{Type: "category", Value: "fast_food"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.determineDataSources("franchise_opportunity", entities)
	}
}

// // internal/workers/ai-conversation/parse-user-intent/handler_test.go
// package parseuserintent

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"net/http"
// 	"net/http/httptest"
// 	"strings"
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
// 		GenAIBaseURL: "http://localhost:8080",
// 		Timeout:      30 * time.Second,
// 		MaxRetries:   2,
// 	}
// }

// func createIntentAPIResponse(intent string, confidence float64, entities []Entity, dataSources []string) string {
// 	response := map[string]interface{}{
// 		"intent":      intent,
// 		"confidence":  confidence,
// 		"entities":    entities,
// 		"dataSources": dataSources,
// 	}
// 	data, _ := json.Marshal(response)
// 	return string(data)
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name            string
// 		input           *Input
// 		apiResponse     string
// 		expectedIntent  string
// 		expectedConf    float64
// 		expectedEnts    int
// 		expectedSrcs    []string
// 		validateOutput  func(t *testing.T, output *Output)
// 		validateRequest func(t *testing.T, reqBody map[string]interface{})
// 	}{
// 		{
// 			name: "complete response with explicit data sources",
// 			input: &Input{
// 				Question: "What are McDonald's franchise opportunities in Texas?",
// 				Context: map[string]interface{}{
// 					"userType": "prospective_franchisee",
// 				},
// 			},
// 			apiResponse: createIntentAPIResponse(
// 				"franchise_opportunity_inquiry",
// 				0.92,
// 				[]Entity{
// 					{Type: "franchise_name", Value: "McDonald's"},
// 					{Type: "location", Value: "Texas"},
// 				},
// 				[]string{"internal_db", "search_index", "external_web"},
// 			),
// 			expectedIntent: "franchise_opportunity_inquiry",
// 			expectedConf:   0.92,
// 			expectedEnts:   2,
// 			expectedSrcs:   []string{"internal_db", "search_index", "external_web"},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, "franchise_opportunity_inquiry", output.IntentAnalysis.PrimaryIntent)
// 				assert.True(t, output.IntentAnalysis.Confidence > 0.9)
// 				assert.Equal(t, 2, len(output.Entities))
// 				assert.Equal(t, 3, len(output.DataSources))
// 			},
// 		},
// 		{
// 			name: "response with auto-determined data sources",
// 			input: &Input{
// 				Question: "Tell me about Subway",
// 				Context:  map[string]interface{}{},
// 			},
// 			apiResponse: createIntentAPIResponse(
// 				"general_info",
// 				0.85,
// 				[]Entity{
// 					{Type: "franchise_name", Value: "Subway"},
// 				},
// 				[]string{}, // Empty to trigger auto-determination
// 			),
// 			expectedIntent: "general_info",
// 			expectedConf:   0.85,
// 			expectedEnts:   1,
// 			expectedSrcs:   []string{"internal_db", "search_index", "external_web"},
// 		},
// 		{
// 			name: "market research intent",
// 			input: &Input{
// 				Question: "Compare McDonald's and Burger King franchise costs",
// 				Context: map[string]interface{}{
// 					"industry": "fast_food",
// 				},
// 			},
// 			apiResponse: createIntentAPIResponse(
// 				"market_research",
// 				0.88,
// 				[]Entity{
// 					{Type: "franchise_name", Value: "McDonald's"},
// 					{Type: "franchise_name", Value: "Burger King"},
// 					{Type: "category", Value: "franchise_costs"},
// 				},
// 				[]string{}, // Empty to trigger auto-determination
// 			),
// 			expectedIntent: "market_research",
// 			expectedConf:   0.88,
// 			expectedEnts:   3,
// 			expectedSrcs:   []string{"internal_db", "search_index", "external_web"},
// 		},
// 		{
// 			name: "minimal response",
// 			input: &Input{
// 				Question: "Hello",
// 				Context:  nil,
// 			},
// 			apiResponse: createIntentAPIResponse(
// 				"greeting",
// 				0.95,
// 				[]Entity{},
// 				[]string{"internal_db"},
// 			),
// 			expectedIntent: "greeting",
// 			expectedConf:   0.95,
// 			expectedEnts:   0,
// 			expectedSrcs:   []string{"internal_db"},
// 			validateRequest: func(t *testing.T, reqBody map[string]interface{}) {
// 				// When Context is nil, it should not be present in the request body
// 				_, hasContext := reqBody["context"]
// 				assert.False(t, hasContext, "context should not be in request when nil")
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				// Verify request
// 				assert.Equal(t, "POST", r.Method)
// 				assert.Equal(t, "/api/ai/parse-intent", r.URL.Path)
// 				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

// 				// Verify request body structure
// 				var reqBody map[string]interface{}
// 				err := json.NewDecoder(r.Body).Decode(&reqBody)
// 				assert.NoError(t, err)
// 				assert.Equal(t, tt.input.Question, reqBody["query"])

// 				if tt.validateRequest != nil {
// 					tt.validateRequest(t, reqBody)
// 				} else if tt.input.Context != nil {
// 					assert.Equal(t, tt.input.Context, reqBody["context"])
// 				}

// 				w.Header().Set("Content-Type", "application/json")
// 				w.WriteHeader(http.StatusOK)
// 				w.Write([]byte(tt.apiResponse))
// 			}))
// 			defer server.Close()

// 			config := createTestConfig()
// 			config.GenAIBaseURL = server.URL
// 			handler := NewHandler(config, zaptest.NewLogger(t))

// 			output, err := handler.execute(context.Background(), tt.input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.Equal(t, tt.expectedIntent, output.IntentAnalysis.PrimaryIntent)
// 			assert.Equal(t, tt.expectedConf, output.IntentAnalysis.Confidence)
// 			assert.Equal(t, tt.expectedEnts, len(output.Entities))
// 			assert.Equal(t, tt.expectedSrcs, output.DataSources)

// 			if tt.validateOutput != nil {
// 				tt.validateOutput(t, output)
// 			}
// 		})
// 	}
// }

// func TestHandler_Execute_Timeout(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		time.Sleep(200 * time.Millisecond) // slow API
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.GenAIBaseURL = server.URL
// 	config.Timeout = 50 * time.Millisecond
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	input := &Input{
// 		Question: "Test question",
// 		Context:  map[string]interface{}{},
// 	}

// 	start := time.Now()
// 	output, err := handler.execute(context.Background(), input)
// 	elapsed := time.Since(start)

// 	assert.Error(t, err)
// 	assert.True(t, errors.Is(err, ErrIntentAPITimeout))
// 	assert.Nil(t, output)

// 	// NEW CORRECT EXPECTATION:
// 	// timeout happens *immediately* (no retries)
// 	assert.Less(t, elapsed, 150*time.Millisecond)
// }

// func TestHandler_Execute_APIError(t *testing.T) {
// 	tests := []struct {
// 		name       string
// 		statusCode int
// 	}{
// 		{"Internal Server Error", http.StatusInternalServerError},
// 		{"Bad Gateway", http.StatusBadGateway},
// 		{"Service Unavailable", http.StatusServiceUnavailable},
// 		{"Bad Request", http.StatusBadRequest},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				w.WriteHeader(tt.statusCode)
// 			}))
// 			defer server.Close()

// 			config := createTestConfig()
// 			config.GenAIBaseURL = server.URL
// 			config.MaxRetries = 0 // Disable retries for this test
// 			handler := NewHandler(config, zaptest.NewLogger(t))

// 			input := &Input{Question: "Test question"}
// 			output, err := handler.execute(context.Background(), input)

// 			assert.Error(t, err)
// 			assert.True(t, strings.Contains(err.Error(), "INTENT_PARSING_FAILED"))
// 			assert.Nil(t, output)
// 		})
// 	}
// }

// func TestHandler_Execute_RetryLogic(t *testing.T) {
// 	attempts := 0
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		attempts++
// 		if attempts < 2 {
// 			// Fail first attempt
// 			w.WriteHeader(http.StatusServiceUnavailable)
// 			return
// 		}
// 		// Succeed on retry
// 		response := createIntentAPIResponse("success", 0.9, []Entity{}, []string{"internal_db"})
// 		w.Header().Set("Content-Type", "application/json")
// 		w.WriteHeader(http.StatusOK)
// 		w.Write([]byte(response))
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.GenAIBaseURL = server.URL
// 	config.MaxRetries = 2
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	output, err := handler.execute(context.Background(), &Input{Question: "Test"})

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.Equal(t, "success", output.IntentAnalysis.PrimaryIntent)
// 	assert.GreaterOrEqual(t, attempts, 2)
// }

// func TestHandler_Execute_MalformedResponse(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		w.Header().Set("Content-Type", "application/json")
// 		w.WriteHeader(http.StatusOK)
// 		w.Write([]byte("invalid json {{{"))
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.GenAIBaseURL = server.URL
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	output, err := handler.execute(context.Background(), &Input{Question: "Test"})

// 	assert.Error(t, err)
// 	assert.True(t, strings.Contains(err.Error(), "INTENT_PARSING_FAILED"))
// 	assert.Nil(t, output)
// }

// func TestHandler_Execute_InvalidConfidence(t *testing.T) {
// 	tests := []struct {
// 		name       string
// 		confidence float64
// 	}{
// 		{"negative confidence", -0.5},
// 		{"confidence > 1", 1.5},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				response := createIntentAPIResponse("test_intent", tt.confidence, []Entity{}, []string{"internal_db"})
// 				w.Header().Set("Content-Type", "application/json")
// 				w.WriteHeader(http.StatusOK)
// 				w.Write([]byte(response))
// 			}))
// 			defer server.Close()

// 			config := createTestConfig()
// 			config.GenAIBaseURL = server.URL
// 			handler := NewHandler(config, zaptest.NewLogger(t))

// 			output, err := handler.execute(context.Background(), &Input{Question: "Test"})

// 			assert.NoError(t, err)
// 			assert.Equal(t, tt.confidence, output.IntentAnalysis.Confidence)
// 		})
// 	}
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_DetermineDataSources(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		intent   string
// 		entities []Entity
// 		expected []string
// 	}{
// 		{
// 			name:   "franchise name entity only",
// 			intent: "franchise_inquiry",
// 			entities: []Entity{
// 				{Type: "franchise_name", Value: "McDonald's"},
// 			},
// 			expected: []string{"internal_db", "search_index"},
// 		},
// 		{
// 			name:   "category entity",
// 			intent: "category_search",
// 			entities: []Entity{
// 				{Type: "category", Value: "fast_food"},
// 			},
// 			expected: []string{"internal_db", "search_index"},
// 		},
// 		{
// 			name:   "general info intent",
// 			intent: "general_info",
// 			entities: []Entity{
// 				{Type: "franchise_name", Value: "Subway"},
// 			},
// 			expected: []string{"internal_db", "search_index", "external_web"},
// 		},
// 		{
// 			name:   "market research intent",
// 			intent: "market_research",
// 			entities: []Entity{
// 				{Type: "franchise_name", Value: "Burger King"},
// 			},
// 			expected: []string{"internal_db", "search_index", "external_web"},
// 		},
// 		{
// 			name:   "competitor analysis intent",
// 			intent: "competitor_analysis",
// 			entities: []Entity{
// 				{Type: "franchise_name", Value: "KFC"},
// 			},
// 			expected: []string{"internal_db", "search_index", "external_web"},
// 		},
// 		{
// 			name:     "no entities, basic intent",
// 			intent:   "greeting",
// 			entities: []Entity{},
// 			expected: []string{"internal_db"},
// 		},
// 		{
// 			name:   "location entity only (should not trigger search)",
// 			intent: "location_inquiry",
// 			entities: []Entity{
// 				{Type: "location", Value: "Texas"},
// 			},
// 			expected: []string{"internal_db"},
// 		},
// 		{
// 			name:   "investment amount only (should not trigger search)",
// 			intent: "cost_inquiry",
// 			entities: []Entity{
// 				{Type: "investment_amount", Value: "100000"},
// 			},
// 			expected: []string{"internal_db"},
// 		},
// 		{
// 			name:   "multiple relevant entities",
// 			intent: "general_info",
// 			entities: []Entity{
// 				{Type: "franchise_name", Value: "McDonald's"},
// 				{Type: "category", Value: "fast_food"},
// 				{Type: "location", Value: "Texas"},
// 			},
// 			expected: []string{"internal_db", "search_index", "external_web"},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result := handler.determineDataSources(tt.intent, tt.entities)
// 			assert.Equal(t, tt.expected, result)
// 		})
// 	}
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	t.Run("empty question", func(t *testing.T) {
// 		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 			var reqBody map[string]interface{}
// 			json.NewDecoder(r.Body).Decode(&reqBody)
// 			assert.Equal(t, "", reqBody["query"])

// 			response := createIntentAPIResponse("unknown", 0.1, []Entity{}, []string{"internal_db"})
// 			w.Header().Set("Content-Type", "application/json")
// 			w.WriteHeader(http.StatusOK)
// 			w.Write([]byte(response))
// 		}))
// 		defer server.Close()

// 		config := createTestConfig()
// 		config.GenAIBaseURL = server.URL
// 		handler := NewHandler(config, zaptest.NewLogger(t))

// 		output, err := handler.execute(context.Background(), &Input{Question: ""})
// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 	})

// 	t.Run("nil context", func(t *testing.T) {
// 		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 			var reqBody map[string]interface{}
// 			json.NewDecoder(r.Body).Decode(&reqBody)
// 			_, hasContext := reqBody["context"]
// 			assert.False(t, hasContext, "context should not be in request body when nil")

// 			response := createIntentAPIResponse("test", 0.8, []Entity{}, []string{"internal_db"})
// 			w.Header().Set("Content-Type", "application/json")
// 			w.WriteHeader(http.StatusOK)
// 			w.Write([]byte(response))
// 		}))
// 		defer server.Close()

// 		config := createTestConfig()
// 		config.GenAIBaseURL = server.URL
// 		handler := NewHandler(config, zaptest.NewLogger(t))

// 		output, err := handler.execute(context.Background(), &Input{Question: "Test", Context: nil})
// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 	})

// 	t.Run("special characters in question", func(t *testing.T) {
// 		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 			var reqBody map[string]interface{}
// 			json.NewDecoder(r.Body).Decode(&reqBody)
// 			assert.Contains(t, reqBody["query"], "McDonald's")

// 			response := createIntentAPIResponse("test", 0.8, []Entity{}, []string{"internal_db"})
// 			w.Header().Set("Content-Type", "application/json")
// 			w.WriteHeader(http.StatusOK)
// 			w.Write([]byte(response))
// 		}))
// 		defer server.Close()

// 		config := createTestConfig()
// 		config.GenAIBaseURL = server.URL
// 		handler := NewHandler(config, zaptest.NewLogger(t))

// 		input := &Input{
// 			Question: "What about \"McDonald's\" franchise?",
// 			Context:  map[string]interface{}{},
// 		}
// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 	})

// 	t.Run("large context data", func(t *testing.T) {
// 		largeContext := make(map[string]interface{})
// 		for i := 0; i < 100; i++ {
// 			key := "key_" + string(rune('a'+(i%26)))
// 			largeContext[key] = "test value"
// 		}

// 		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 			var reqBody map[string]interface{}
// 			json.NewDecoder(r.Body).Decode(&reqBody)
// 			assert.NotNil(t, reqBody["context"])

// 			response := createIntentAPIResponse("test", 0.8, []Entity{}, []string{"internal_db"})
// 			w.Header().Set("Content-Type", "application/json")
// 			w.WriteHeader(http.StatusOK)
// 			w.Write([]byte(response))
// 		}))
// 		defer server.Close()

// 		config := createTestConfig()
// 		config.GenAIBaseURL = server.URL
// 		handler := NewHandler(config, zaptest.NewLogger(t))

// 		input := &Input{
// 			Question: "Test question",
// 			Context:  largeContext,
// 		}
// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 	})
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		// Verify complete request structure
// 		var reqBody map[string]interface{}
// 		json.NewDecoder(r.Body).Decode(&reqBody)

// 		assert.Equal(t, "Comprehensive franchise inquiry", reqBody["query"])
// 		assert.NotNil(t, reqBody["context"])

// 		context := reqBody["context"].(map[string]interface{})
// 		assert.Equal(t, "prospective_buyer", context["userType"])

// 		response := createIntentAPIResponse(
// 			"franchise_opportunity_inquiry",
// 			0.94,
// 			[]Entity{
// 				{Type: "franchise_name", Value: "McDonald's"},
// 				{Type: "location", Value: "Texas"},
// 				{Type: "investment_amount", Value: "500000"},
// 			},
// 			[]string{"internal_db", "search_index", "external_web"},
// 		)
// 		w.Header().Set("Content-Type", "application/json")
// 		w.WriteHeader(http.StatusOK)
// 		w.Write([]byte(response))
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.GenAIBaseURL = server.URL
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	input := &Input{
// 		Question: "Comprehensive franchise inquiry",
// 		Context: map[string]interface{}{
// 			"userType": "prospective_buyer",
// 			"industry": "fast_food",
// 		},
// 	}

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.Equal(t, "franchise_opportunity_inquiry", output.IntentAnalysis.PrimaryIntent)
// 	assert.True(t, output.IntentAnalysis.Confidence > 0.9)
// 	assert.Equal(t, 3, len(output.Entities))
// 	assert.Equal(t, 3, len(output.DataSources))

// 	// Verify entities
// 	entityTypes := make(map[string]bool)
// 	for _, entity := range output.Entities {
// 		entityTypes[entity.Type] = true
// 	}
// 	assert.True(t, entityTypes["franchise_name"])
// 	assert.True(t, entityTypes["location"])
// 	assert.True(t, entityTypes["investment_amount"])
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		response := createIntentAPIResponse("test_intent", 0.8, []Entity{}, []string{"internal_db"})
// 		w.Header().Set("Content-Type", "application/json")
// 		w.WriteHeader(http.StatusOK)
// 		w.Write([]byte(response))
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.GenAIBaseURL = server.URL
// 	handler := NewHandler(config, zaptest.NewLogger(b))

// 	input := &Input{
// 		Question: "Test question",
// 		Context:  map[string]interface{}{"test": "value"},
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), input)
// 	}
// }

// func BenchmarkHandler_DetermineDataSources(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	entities := []Entity{
// 		{Type: "franchise_name", Value: "McDonald's"},
// 		{Type: "location", Value: "Texas"},
// 		{Type: "category", Value: "fast_food"},
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.determineDataSources("franchise_opportunity", entities)
// 	}
// }
