// internal/workers/ai-conversation/llm-synthesis/handler_test.go
// internal/workers/ai-conversation/llm-synthesis/handler_test.go
package llmsynthesis

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
		Timeout:      5 * time.Second,
		MaxRetries:   1,
		MaxTokens:    500,
		Temperature:  0.7,
	}
}

func createLLMAPIResponse(text string, confidence float64, sources []string) string {
	response := map[string]interface{}{
		"text":       text,
		"confidence": confidence,
		"sources":    sources,
	}
	data, _ := json.Marshal(response)
	return string(data)
}

// ==========================
// Core Functionality Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	tests := []struct {
		name           string
		input          *Input
		apiResponse    string
		expectedText   string
		expectedConf   float64
		expectedSrcs   int
		validateOutput func(t *testing.T, output *Output)
	}{
		{
			name: "complete response with internal data",
			input: &Input{
				Question: "What are McDonald's franchise fees?",
				InternalData: map[string]interface{}{
					"franchise_name":   "McDonald's",
					"initial_fee":      45000,
					"total_investment": "1M-2M",
				},
				WebData: WebData{
					Sources: []Source{
						{URL: "https://mcdonalds.com", Title: "Official Site"},
					},
					Summary: "McDonald's franchise information",
				},
				Intent: Intent{
					PrimaryIntent: "franchise_cost_inquiry",
					Confidence:    0.9,
				},
			},
			apiResponse:  createLLMAPIResponse("McDonald's franchise fee is $45,000 with total investment of $1M-2M.", 0.95, []string{"Internal DB", "Official Site"}),
			expectedText: "McDonald's franchise fee is $45,000 with total investment of $1M-2M.",
			expectedConf: 0.95,
			expectedSrcs: 2,
			validateOutput: func(t *testing.T, output *Output) {
				assert.Contains(t, output.LLMResponse, "45,000")
				assert.True(t, output.Confidence > 0.9)
				assert.Equal(t, 2, len(output.Sources))
			},
		},
		{
			name: "minimal data",
			input: &Input{
				Question:     "Tell me about franchising",
				InternalData: map[string]interface{}{},
				WebData:      WebData{Sources: []Source{}, Summary: ""},
				Intent:       Intent{PrimaryIntent: "general_inquiry", Confidence: 0.5},
			},
			apiResponse:  createLLMAPIResponse("Franchising is a business model.", 0.7, []string{"General knowledge"}),
			expectedText: "Franchising is a business model.",
			expectedConf: 0.7,
			expectedSrcs: 1,
		},
		{
			name: "high confidence response",
			input: &Input{
				Question: "Where is Subway headquartered?",
				InternalData: map[string]interface{}{
					"headquarters": "Milford, Connecticut",
				},
				WebData: WebData{Sources: []Source{}},
				Intent:  Intent{PrimaryIntent: "franchise_info", Confidence: 0.85},
			},
			apiResponse:  createLLMAPIResponse("Subway is headquartered in Milford, Connecticut.", 0.98, []string{"Internal DB"}),
			expectedText: "Subway is headquartered in Milford, Connecticut.",
			expectedConf: 0.98,
			expectedSrcs: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request
				assert.Equal(t, "POST", r.Method)
				assert.Equal(t, "/api/ai/generate", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				// Verify request body structure
				var reqBody map[string]interface{}
				json.NewDecoder(r.Body).Decode(&reqBody)
				assert.NotEmpty(t, reqBody["prompt"])
				assert.NotNil(t, reqBody["context"])
				assert.Equal(t, float64(500), reqBody["max_tokens"])
				assert.Equal(t, 0.7, reqBody["temperature"])

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
			assert.Equal(t, tt.expectedText, output.LLMResponse)
			assert.Equal(t, tt.expectedConf, output.Confidence)
			assert.Equal(t, tt.expectedSrcs, len(output.Sources))

			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}
		})
	}
}

func TestHandler_Execute_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use a select with both context and a longer timeout to prevent hanging
		select {
		case <-r.Context().Done():
			// Context was cancelled - this is what we want
			return
		case <-time.After(10 * time.Second):
			// Safety net: if context doesn't cancel, we timeout after 10s
			t.Log("Test server safety timeout reached")
			return
		}
	}))
	defer server.Close()

	config := createTestConfig()
	config.GenAIBaseURL = server.URL
	config.Timeout = 50 * time.Millisecond // Very short timeout for test
	handler := NewHandler(config, NewTestLogger(t))

	input := &Input{
		Question:     "Test",
		InternalData: map[string]interface{}{},
		WebData:      WebData{},
		Intent:       Intent{},
	}

	// Add test timeout to prevent hanging
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	output, err := handler.execute(ctx, input)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrLLMTimeout),
		"Expected LLM_TIMEOUT, got: %v", err)
	assert.Nil(t, output)
}

func TestHandler_Execute_APIError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"Internal Server Error", http.StatusInternalServerError},
		{"Bad Gateway", http.StatusBadGateway},
		{"Service Unavailable", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			config := createTestConfig()
			config.GenAIBaseURL = server.URL
			handler := NewHandler(config, NewTestLogger(t))

			input := &Input{Question: "Test"}
			output, err := handler.execute(context.Background(), input)

			assert.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), "LLM_SYNTHESIS_FAILED"))
			assert.Nil(t, output)
		})
	}
}

func TestHandler_Execute_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := createLLMAPIResponse("", 0.5, []string{})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := createTestConfig()
	config.GenAIBaseURL = server.URL
	handler := NewHandler(config, NewTestLogger(t))

	input := &Input{Question: "Test"}
	output, err := handler.execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	// Should provide fallback message
	assert.Equal(t, "I don't have enough information to answer that question.", output.LLMResponse)
	assert.Equal(t, 0.1, output.Confidence)
}

func TestHandler_Execute_InvalidConfidence(t *testing.T) {
	tests := []struct {
		name               string
		confidence         float64
		expectedConfidence float64
	}{
		{"negative confidence", -0.5, 0.5},
		{"confidence > 1", 1.5, 0.5},
		{"valid confidence", 0.85, 0.85},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := createLLMAPIResponse("Valid response", tt.confidence, []string{"test"})
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
			assert.Equal(t, tt.expectedConfidence, output.Confidence)
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
		response := createLLMAPIResponse("Success after retry", 0.8, []string{"test"})
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
	assert.Equal(t, "Success after retry", output.LLMResponse)
	assert.GreaterOrEqual(t, attempts, 2)
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_BuildPrompt(t *testing.T) {
	handler := NewHandler(createTestConfig(), NewTestLogger(t))

	tests := []struct {
		name     string
		input    *Input
		contains []string
	}{
		{
			name: "complete prompt",
			input: &Input{
				Question: "What is the franchise fee?",
				InternalData: map[string]interface{}{
					"fee": 50000,
				},
				WebData: WebData{
					Sources: []Source{{Title: "Official", URL: "https://test.com"}},
					Summary: "Test summary",
				},
				Intent: Intent{PrimaryIntent: "cost_inquiry"},
			},
			contains: []string{
				"What is the franchise fee?",
				"Internal Franchise Data",
				"External Web Sources",
				"Official",
				"Test summary",
				"Cite sources",
			},
		},
		{
			name: "minimal prompt",
			input: &Input{
				Question:     "Simple question",
				InternalData: map[string]interface{}{},
				WebData:      WebData{Sources: []Source{}},
			},
			contains: []string{
				"Simple question",
				"helpful franchise advisor",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := handler.buildPrompt(tt.input)
			for _, substr := range tt.contains {
				assert.Contains(t, prompt, substr)
			}
		})
	}
}

func TestHandler_BuildPrompt_Structure(t *testing.T) {
	handler := NewHandler(createTestConfig(), NewTestLogger(t))

	input := &Input{
		Question: "Test question",
		InternalData: map[string]interface{}{
			"key": "value",
		},
		WebData: WebData{
			Sources: []Source{{Title: "Source", URL: "http://test.com"}},
			Summary: "Summary text",
		},
	}

	prompt := handler.buildPrompt(input)

	// Verify structure
	assert.Contains(t, prompt, "User Question:")
	assert.Contains(t, prompt, "Internal Franchise Data:")
	assert.Contains(t, prompt, "External Web Sources:")
	assert.Contains(t, prompt, "Instructions:")
	assert.Contains(t, prompt, "Answer:")

	// Verify instructions are present
	assert.Contains(t, prompt, "Cite sources")
	assert.Contains(t, prompt, "confidence score")
	assert.Contains(t, prompt, "concise and professional")
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	handler := NewHandler(createTestConfig(), NewTestLogger(t))

	t.Run("empty question", func(t *testing.T) {
		prompt := handler.buildPrompt(&Input{Question: ""})
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "User Question:")
	})

	t.Run("nil internal data", func(t *testing.T) {
		prompt := handler.buildPrompt(&Input{
			Question:     "Test",
			InternalData: nil,
		})
		assert.NotEmpty(t, prompt)
	})

	t.Run("empty web sources", func(t *testing.T) {
		prompt := handler.buildPrompt(&Input{
			Question: "Test",
			WebData:  WebData{Sources: []Source{}},
		})
		assert.NotEmpty(t, prompt)
		assert.NotContains(t, prompt, "External Web Sources:")
	})

	t.Run("special characters in question", func(t *testing.T) {
		input := &Input{
			Question: "What about \"McDonald's\" <script>alert('xss')</script>",
		}
		prompt := handler.buildPrompt(input)
		assert.Contains(t, prompt, input.Question)
	})

	t.Run("large internal data", func(t *testing.T) {
		largeData := make(map[string]interface{})
		for i := 0; i < 100; i++ {
			largeData[string(rune('a'+i))] = "test value"
		}
		prompt := handler.buildPrompt(&Input{
			Question:     "Test",
			InternalData: largeData,
		})
		assert.NotEmpty(t, prompt)
		assert.Contains(t, prompt, "Internal Franchise Data:")
	})
}

func TestHandler_MalformedAPIResponse(t *testing.T) {
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
	assert.True(t, strings.Contains(err.Error(), "LLM_SYNTHESIS_FAILED"))
	assert.Nil(t, output)
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify complete request structure
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		assert.NotEmpty(t, reqBody["prompt"])
		assert.NotNil(t, reqBody["context"])

		context := reqBody["context"].(map[string]interface{})
		assert.NotNil(t, context["internal"])
		assert.NotNil(t, context["external"])
		assert.NotNil(t, context["intent"])

		response := createLLMAPIResponse(
			"Complete response with all data integrated",
			0.92,
			[]string{"Internal DB", "External Source"},
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
		Question: "Comprehensive test question",
		InternalData: map[string]interface{}{
			"franchise": "Test Franchise",
			"fee":       50000,
		},
		WebData: WebData{
			Sources: []Source{
				{Title: "Official Site", URL: "https://example.com"},
			},
			Summary: "Test summary from web",
		},
		Intent: Intent{
			PrimaryIntent: "franchise_inquiry",
			Confidence:    0.88,
		},
	}

	output, err := handler.execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.NotEmpty(t, output.LLMResponse)
	assert.True(t, output.Confidence > 0.9)
	assert.Equal(t, 2, len(output.Sources))
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := createLLMAPIResponse("Test response", 0.8, []string{"test"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := createTestConfig()
	config.GenAIBaseURL = server.URL
	handler := NewHandler(config, &BenchmarkLogger{})

	input := &Input{
		Question:     "Test question",
		InternalData: map[string]interface{}{"key": "value"},
		WebData:      WebData{Sources: []Source{}},
		Intent:       Intent{PrimaryIntent: "test"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.execute(context.Background(), input)
	}
}

func BenchmarkHandler_BuildPrompt(b *testing.B) {
	handler := NewHandler(createTestConfig(), &BenchmarkLogger{})
	input := &Input{
		Question: "Test question",
		InternalData: map[string]interface{}{
			"key1": "value1",
			"key2": "value2",
		},
		WebData: WebData{
			Sources: []Source{
				{Title: "Source 1", URL: "http://test1.com"},
				{Title: "Source 2", URL: "http://test2.com"},
			},
			Summary: "Test summary",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.buildPrompt(input)
	}
}

// // internal/workers/ai-conversation/llm-synthesis/handler_test.go
// package llmsynthesis

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
// 		Timeout:      5 * time.Second,
// 		MaxRetries:   1,
// 		MaxTokens:    500,
// 		Temperature:  0.7,
// 	}
// }

// func createLLMAPIResponse(text string, confidence float64, sources []string) string {
// 	response := map[string]interface{}{
// 		"text":       text,
// 		"confidence": confidence,
// 		"sources":    sources,
// 	}
// 	data, _ := json.Marshal(response)
// 	return string(data)
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name           string
// 		input          *Input
// 		apiResponse    string
// 		expectedText   string
// 		expectedConf   float64
// 		expectedSrcs   int
// 		validateOutput func(t *testing.T, output *Output)
// 	}{
// 		{
// 			name: "complete response with internal data",
// 			input: &Input{
// 				Question: "What are McDonald's franchise fees?",
// 				InternalData: map[string]interface{}{
// 					"franchise_name":   "McDonald's",
// 					"initial_fee":      45000,
// 					"total_investment": "1M-2M",
// 				},
// 				WebData: WebData{
// 					Sources: []Source{
// 						{URL: "https://mcdonalds.com", Title: "Official Site"},
// 					},
// 					Summary: "McDonald's franchise information",
// 				},
// 				Intent: Intent{
// 					PrimaryIntent: "franchise_cost_inquiry",
// 					Confidence:    0.9,
// 				},
// 			},
// 			apiResponse:  createLLMAPIResponse("McDonald's franchise fee is $45,000 with total investment of $1M-2M.", 0.95, []string{"Internal DB", "Official Site"}),
// 			expectedText: "McDonald's franchise fee is $45,000 with total investment of $1M-2M.",
// 			expectedConf: 0.95,
// 			expectedSrcs: 2,
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Contains(t, output.LLMResponse, "45,000")
// 				assert.True(t, output.Confidence > 0.9)
// 				assert.Equal(t, 2, len(output.Sources))
// 			},
// 		},
// 		{
// 			name: "minimal data",
// 			input: &Input{
// 				Question:     "Tell me about franchising",
// 				InternalData: map[string]interface{}{},
// 				WebData:      WebData{Sources: []Source{}, Summary: ""},
// 				Intent:       Intent{PrimaryIntent: "general_inquiry", Confidence: 0.5},
// 			},
// 			apiResponse:  createLLMAPIResponse("Franchising is a business model.", 0.7, []string{"General knowledge"}),
// 			expectedText: "Franchising is a business model.",
// 			expectedConf: 0.7,
// 			expectedSrcs: 1,
// 		},
// 		{
// 			name: "high confidence response",
// 			input: &Input{
// 				Question: "Where is Subway headquartered?",
// 				InternalData: map[string]interface{}{
// 					"headquarters": "Milford, Connecticut",
// 				},
// 				WebData: WebData{Sources: []Source{}},
// 				Intent:  Intent{PrimaryIntent: "franchise_info", Confidence: 0.85},
// 			},
// 			apiResponse:  createLLMAPIResponse("Subway is headquartered in Milford, Connecticut.", 0.98, []string{"Internal DB"}),
// 			expectedText: "Subway is headquartered in Milford, Connecticut.",
// 			expectedConf: 0.98,
// 			expectedSrcs: 1,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				// Verify request
// 				assert.Equal(t, "POST", r.Method)
// 				assert.Equal(t, "/api/ai/generate", r.URL.Path)
// 				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

// 				// Verify request body structure
// 				var reqBody map[string]interface{}
// 				json.NewDecoder(r.Body).Decode(&reqBody)
// 				assert.NotEmpty(t, reqBody["prompt"])
// 				assert.NotNil(t, reqBody["context"])
// 				assert.Equal(t, float64(500), reqBody["max_tokens"])
// 				assert.Equal(t, 0.7, reqBody["temperature"])

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
// 			assert.Equal(t, tt.expectedText, output.LLMResponse)
// 			assert.Equal(t, tt.expectedConf, output.Confidence)
// 			assert.Equal(t, tt.expectedSrcs, len(output.Sources))

// 			if tt.validateOutput != nil {
// 				tt.validateOutput(t, output)
// 			}
// 		})
// 	}
// }

// func TestHandler_Execute_Timeout(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		// Use a select with both context and a longer timeout to prevent hanging
// 		select {
// 		case <-r.Context().Done():
// 			// Context was cancelled - this is what we want
// 			return
// 		case <-time.After(10 * time.Second):
// 			// Safety net: if context doesn't cancel, we timeout after 10s
// 			t.Log("Test server safety timeout reached")
// 			return
// 		}
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.GenAIBaseURL = server.URL
// 	config.Timeout = 50 * time.Millisecond // Very short timeout for test
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	input := &Input{
// 		Question:     "Test",
// 		InternalData: map[string]interface{}{},
// 		WebData:      WebData{},
// 		Intent:       Intent{},
// 	}

// 	// Add test timeout to prevent hanging
// 	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
// 	defer cancel()

// 	output, err := handler.execute(ctx, input)

// 	assert.Error(t, err)
// 	assert.True(t, errors.Is(err, ErrLLMTimeout),
// 		"Expected LLM_TIMEOUT, got: %v", err)
// 	assert.Nil(t, output)
// }

// func TestHandler_Execute_APIError(t *testing.T) {
// 	tests := []struct {
// 		name       string
// 		statusCode int
// 	}{
// 		{"Internal Server Error", http.StatusInternalServerError},
// 		{"Bad Gateway", http.StatusBadGateway},
// 		{"Service Unavailable", http.StatusServiceUnavailable},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				w.WriteHeader(tt.statusCode)
// 			}))
// 			defer server.Close()

// 			config := createTestConfig()
// 			config.GenAIBaseURL = server.URL
// 			handler := NewHandler(config, zaptest.NewLogger(t))

// 			input := &Input{Question: "Test"}
// 			output, err := handler.execute(context.Background(), input)

// 			assert.Error(t, err)
// 			assert.True(t, strings.Contains(err.Error(), "LLM_SYNTHESIS_FAILED"))
// 			assert.Nil(t, output)
// 		})
// 	}
// }

// func TestHandler_Execute_EmptyResponse(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		response := createLLMAPIResponse("", 0.5, []string{})
// 		w.Header().Set("Content-Type", "application/json")
// 		w.WriteHeader(http.StatusOK)
// 		w.Write([]byte(response))
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.GenAIBaseURL = server.URL
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	input := &Input{Question: "Test"}
// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	// Should provide fallback message
// 	assert.Equal(t, "I don't have enough information to answer that question.", output.LLMResponse)
// 	assert.Equal(t, 0.1, output.Confidence)
// }

// func TestHandler_Execute_InvalidConfidence(t *testing.T) {
// 	tests := []struct {
// 		name               string
// 		confidence         float64
// 		expectedConfidence float64
// 	}{
// 		{"negative confidence", -0.5, 0.5},
// 		{"confidence > 1", 1.5, 0.5},
// 		{"valid confidence", 0.85, 0.85},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				response := createLLMAPIResponse("Valid response", tt.confidence, []string{"test"})
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
// 			assert.Equal(t, tt.expectedConfidence, output.Confidence)
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
// 		response := createLLMAPIResponse("Success after retry", 0.8, []string{"test"})
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
// 	assert.Equal(t, "Success after retry", output.LLMResponse)
// 	assert.GreaterOrEqual(t, attempts, 2)
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_BuildPrompt(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		input    *Input
// 		contains []string
// 	}{
// 		{
// 			name: "complete prompt",
// 			input: &Input{
// 				Question: "What is the franchise fee?",
// 				InternalData: map[string]interface{}{
// 					"fee": 50000,
// 				},
// 				WebData: WebData{
// 					Sources: []Source{{Title: "Official", URL: "https://test.com"}},
// 					Summary: "Test summary",
// 				},
// 				Intent: Intent{PrimaryIntent: "cost_inquiry"},
// 			},
// 			contains: []string{
// 				"What is the franchise fee?",
// 				"Internal Franchise Data",
// 				"External Web Sources",
// 				"Official",
// 				"Test summary",
// 				"Cite sources",
// 			},
// 		},
// 		{
// 			name: "minimal prompt",
// 			input: &Input{
// 				Question:     "Simple question",
// 				InternalData: map[string]interface{}{},
// 				WebData:      WebData{Sources: []Source{}},
// 			},
// 			contains: []string{
// 				"Simple question",
// 				"helpful franchise advisor",
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			prompt := handler.buildPrompt(tt.input)
// 			for _, substr := range tt.contains {
// 				assert.Contains(t, prompt, substr)
// 			}
// 		})
// 	}
// }

// func TestHandler_BuildPrompt_Structure(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	input := &Input{
// 		Question: "Test question",
// 		InternalData: map[string]interface{}{
// 			"key": "value",
// 		},
// 		WebData: WebData{
// 			Sources: []Source{{Title: "Source", URL: "http://test.com"}},
// 			Summary: "Summary text",
// 		},
// 	}

// 	prompt := handler.buildPrompt(input)

// 	// Verify structure
// 	assert.Contains(t, prompt, "User Question:")
// 	assert.Contains(t, prompt, "Internal Franchise Data:")
// 	assert.Contains(t, prompt, "External Web Sources:")
// 	assert.Contains(t, prompt, "Instructions:")
// 	assert.Contains(t, prompt, "Answer:")

// 	// Verify instructions are present
// 	assert.Contains(t, prompt, "Cite sources")
// 	assert.Contains(t, prompt, "confidence score")
// 	assert.Contains(t, prompt, "concise and professional")
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	t.Run("empty question", func(t *testing.T) {
// 		prompt := handler.buildPrompt(&Input{Question: ""})
// 		assert.NotEmpty(t, prompt)
// 		assert.Contains(t, prompt, "User Question:")
// 	})

// 	t.Run("nil internal data", func(t *testing.T) {
// 		prompt := handler.buildPrompt(&Input{
// 			Question:     "Test",
// 			InternalData: nil,
// 		})
// 		assert.NotEmpty(t, prompt)
// 	})

// 	t.Run("empty web sources", func(t *testing.T) {
// 		prompt := handler.buildPrompt(&Input{
// 			Question: "Test",
// 			WebData:  WebData{Sources: []Source{}},
// 		})
// 		assert.NotEmpty(t, prompt)
// 		assert.NotContains(t, prompt, "External Web Sources:")
// 	})

// 	t.Run("special characters in question", func(t *testing.T) {
// 		input := &Input{
// 			Question: "What about \"McDonald's\" <script>alert('xss')</script>",
// 		}
// 		prompt := handler.buildPrompt(input)
// 		assert.Contains(t, prompt, input.Question)
// 	})

// 	t.Run("large internal data", func(t *testing.T) {
// 		largeData := make(map[string]interface{})
// 		for i := 0; i < 100; i++ {
// 			largeData[string(rune('a'+i))] = "test value"
// 		}
// 		prompt := handler.buildPrompt(&Input{
// 			Question:     "Test",
// 			InternalData: largeData,
// 		})
// 		assert.NotEmpty(t, prompt)
// 		assert.Contains(t, prompt, "Internal Franchise Data:")
// 	})
// }

// func TestHandler_MalformedAPIResponse(t *testing.T) {
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
// 	assert.True(t, strings.Contains(err.Error(), "LLM_SYNTHESIS_FAILED"))
// 	assert.Nil(t, output)
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		// Verify complete request structure
// 		var reqBody map[string]interface{}
// 		json.NewDecoder(r.Body).Decode(&reqBody)

// 		assert.NotEmpty(t, reqBody["prompt"])
// 		assert.NotNil(t, reqBody["context"])

// 		context := reqBody["context"].(map[string]interface{})
// 		assert.NotNil(t, context["internal"])
// 		assert.NotNil(t, context["external"])
// 		assert.NotNil(t, context["intent"])

// 		response := createLLMAPIResponse(
// 			"Complete response with all data integrated",
// 			0.92,
// 			[]string{"Internal DB", "External Source"},
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
// 		Question: "Comprehensive test question",
// 		InternalData: map[string]interface{}{
// 			"franchise": "Test Franchise",
// 			"fee":       50000,
// 		},
// 		WebData: WebData{
// 			Sources: []Source{
// 				{Title: "Official Site", URL: "https://example.com"},
// 			},
// 			Summary: "Test summary from web",
// 		},
// 		Intent: Intent{
// 			PrimaryIntent: "franchise_inquiry",
// 			Confidence:    0.88,
// 		},
// 	}

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.NotEmpty(t, output.LLMResponse)
// 	assert.True(t, output.Confidence > 0.9)
// 	assert.Equal(t, 2, len(output.Sources))
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		response := createLLMAPIResponse("Test response", 0.8, []string{"test"})
// 		w.Header().Set("Content-Type", "application/json")
// 		w.WriteHeader(http.StatusOK)
// 		w.Write([]byte(response))
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.GenAIBaseURL = server.URL
// 	handler := NewHandler(config, zaptest.NewLogger(b))

// 	input := &Input{
// 		Question:     "Test question",
// 		InternalData: map[string]interface{}{"key": "value"},
// 		WebData:      WebData{Sources: []Source{}},
// 		Intent:       Intent{PrimaryIntent: "test"},
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), input)
// 	}
// }

// func BenchmarkHandler_BuildPrompt(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))
// 	input := &Input{
// 		Question: "Test question",
// 		InternalData: map[string]interface{}{
// 			"key1": "value1",
// 			"key2": "value2",
// 		},
// 		WebData: WebData{
// 			Sources: []Source{
// 				{Title: "Source 1", URL: "http://test1.com"},
// 				{Title: "Source 2", URL: "http://test2.com"},
// 			},
// 			Summary: "Test summary",
// 		},
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.buildPrompt(input)
// 	}
// }

// // internal/workers/ai-conversation/llm-synthesis/handler_test.go
// package llmsynthesis

// import (
// 	"context"
// 	"encoding/json"
// 	"net/http"
// 	"net/http/httptest"
// 	"testing"
// 	"time"

// 	"github.com/stretchr/testify/assert"
// 	"go.uber.org/zap"
// )

// func TestHandler_execute_Success(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		w.Header().Set("Content-Type", "application/json")
// 		json.NewEncoder(w).Encode(map[string]interface{}{
// 			"text":       "McDonald's is a great franchise opportunity.",
// 			"confidence": 0.92,
// 			"sources":    []string{"https://www.mcdonalds.com/franchise"},
// 		})
// 	}))
// 	defer server.Close()

// 	handler := NewHandler(&Config{
// 		GenAIBaseURL: server.URL,
// 		Timeout:      10 * time.Second,
// 		MaxRetries:   1,
// 		MaxTokens:    500,
// 		Temperature:  0.7,
// 	}, zap.NewNop())

// 	input := &Input{
// 		Question: "Is McDonald's a good franchise?",
// 		InternalData: map[string]interface{}{
// 			"franchises": []map[string]interface{}{{"name": "McDonald's"}},
// 		},
// 		WebData: WebData{
// 			Sources: []Source{},
// 			Summary: "",
// 		},
// 		Intent: Intent{
// 			PrimaryIntent: "franchise_info",
// 			Confidence:    0.95,
// 		},
// 	}

// 	output, err := handler.execute(context.Background(), input)
// 	assert.NoError(t, err)
// 	assert.Equal(t, "McDonald's is a great franchise opportunity.", output.LLMResponse)
// 	assert.Equal(t, 0.92, output.Confidence)
// 	assert.Len(t, output.Sources, 1)
// }

// func TestHandler_execute_Timeout(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		time.Sleep(10 * time.Second)
// 	}))
// 	defer server.Close()

// 	handler := NewHandler(&Config{
// 		GenAIBaseURL: server.URL,
// 		Timeout:      1 * time.Second,
// 		MaxRetries:   0,
// 		MaxTokens:    500,
// 		Temperature:  0.7,
// 	}, zap.NewNop())

// 	input := &Input{
// 		Question:     "test",
// 		InternalData: map[string]interface{}{},
// 		WebData: WebData{
// 			Sources: []Source{},
// 			Summary: "",
// 		},
// 		Intent: Intent{
// 			PrimaryIntent: "test",
// 			Confidence:    0.5,
// 		},
// 	}

// 	_, err := handler.execute(context.Background(), input)
// 	assert.Error(t, err)
// 	assert.Contains(t, err.Error(), "LLM_TIMEOUT")
// }

// func TestHandler_execute_EmptyResponse(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		w.Header().Set("Content-Type", "application/json")
// 		json.NewEncoder(w).Encode(map[string]interface{}{
// 			"text":       "",
// 			"confidence": 0.0,
// 			"sources":    []string{},
// 		})
// 	}))
// 	defer server.Close()

// 	handler := NewHandler(&Config{
// 		GenAIBaseURL: server.URL,
// 		Timeout:      10 * time.Second,
// 		MaxRetries:   0,
// 		MaxTokens:    500,
// 		Temperature:  0.7,
// 	}, zap.NewNop())

// 	input := &Input{
// 		Question: "test",
// 	}

// 	output, err := handler.execute(context.Background(), input)
// 	assert.NoError(t, err)
// 	assert.Equal(t, "I don't have enough information to answer that question.", output.LLMResponse)
// 	assert.Equal(t, 0.1, output.Confidence)
// }
