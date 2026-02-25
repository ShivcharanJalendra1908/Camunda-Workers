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

type TestLogger struct {
	t      *testing.T
	fields map[string]interface{}
}

func NewTestLogger(t *testing.T) *TestLogger {
	return &TestLogger{t: t, fields: make(map[string]interface{})}
}

func (l *TestLogger) Info(msg string, fields map[string]interface{}) {
	l.t.Logf("INFO: %s %v", msg, l.mergeFields(fields))
}

func (l *TestLogger) Warn(msg string, fields map[string]interface{}) {
	l.t.Logf("WARN: %s %v", msg, l.mergeFields(fields))
}

func (l *TestLogger) Error(msg string, fields map[string]interface{}) {
	l.t.Logf("ERROR: %s %v", msg, l.mergeFields(fields))
}

func (l *TestLogger) With(fields map[string]interface{}) Logger {
	nl := &TestLogger{t: l.t, fields: make(map[string]interface{})}
	for k, v := range l.fields {
		nl.fields[k] = v
	}
	for k, v := range fields {
		nl.fields[k] = v
	}
	return nl
}

func (l *TestLogger) mergeFields(fields map[string]interface{}) map[string]interface{} {
	all := make(map[string]interface{})
	for k, v := range l.fields {
		all[k] = v
	}
	for k, v := range fields {
		all[k] = v
	}
	return all
}

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

func createHandlerWithConfig(t *testing.T, config *Config) *Handler {
	return NewHandler(HandlerOptions{
		Config: config,
		Logger: NewTestLogger(t),
	})
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
				// BUG FIX: "franchise_cost_inquiry" is NOT in handler's allowedIntents list.
				// Valid intents: search_franchise, compare_franchise, get_details,
				// calculate_investment, check_eligibility, general_inquiry.
				// Use "general_inquiry" for a general fee inquiry.
				Intent: Intent{
					PrimaryIntent: "general_inquiry",
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
				// "general_inquiry" is in the allowed list
				Intent: Intent{PrimaryIntent: "general_inquiry", Confidence: 0.5},
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
				// BUG FIX: "franchise_info" is NOT in the allowed list.
				// Use "get_details" which maps to the concept of getting franchise info.
				Intent: Intent{PrimaryIntent: "get_details", Confidence: 0.85},
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
				assert.Equal(t, "POST", r.Method)
				assert.Equal(t, "/api/ai/generate", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

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
			handler := createHandlerWithConfig(t, config)

			output, err := handler.Execute(context.Background(), tt.input)

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
		select {
		case <-r.Context().Done():
			return
		case <-time.After(10 * time.Second):
			return
		}
	}))
	defer server.Close()

	config := createTestConfig()
	config.GenAIBaseURL = server.URL
	config.Timeout = 50 * time.Millisecond
	handler := createHandlerWithConfig(t, config)

	input := &Input{
		Question:     "Test",
		InternalData: map[string]interface{}{},
		WebData:      WebData{},
		Intent:       Intent{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	output, err := handler.Execute(ctx, input)

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
			handler := createHandlerWithConfig(t, config)

			input := &Input{Question: "Test"}
			output, err := handler.Execute(context.Background(), input)

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
	handler := createHandlerWithConfig(t, config)

	input := &Input{Question: "Test"}
	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, "I don't have enough information to answer that question.", output.LLMResponse)
	assert.Equal(t, 0.1, output.Confidence)
}

func TestHandler_Execute_InvalidConfidence(t *testing.T) {
	tests := []struct {
		name               string
		confidence         float64
		expectedConfidence float64
	}{
		// BUG FIX: handler.execute() normalizes invalid confidence (< 0 or > 1) to 0.5.
		// Tests expecting the raw invalid value to pass through were wrong.
		{"negative confidence normalized to 0.5", -0.5, 0.5},
		{"confidence > 1 normalized to 0.5", 1.5, 0.5},
		{"valid confidence unchanged", 0.85, 0.85},
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
			handler := createHandlerWithConfig(t, config)

			output, err := handler.Execute(context.Background(), &Input{Question: "Test"})

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
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		response := createLLMAPIResponse("Success after retry", 0.8, []string{"test"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := createTestConfig()
	config.GenAIBaseURL = server.URL
	config.MaxRetries = 2
	handler := createHandlerWithConfig(t, config)

	output, err := handler.Execute(context.Background(), &Input{Question: "Test"})

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, "Success after retry", output.LLMResponse)
	assert.GreaterOrEqual(t, attempts, 2)
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_BuildPrompt(t *testing.T) {
	handler := createHandlerWithConfig(t, createTestConfig())

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
				// BUG FIX: "cost_inquiry" is not in allowed intents; omit PrimaryIntent
				// so validateIntent skips the allowedIntents check (only validates if non-empty)
				Intent: Intent{},
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
	handler := createHandlerWithConfig(t, createTestConfig())

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

	assert.Contains(t, prompt, "User Question:")
	assert.Contains(t, prompt, "Internal Franchise Data:")
	assert.Contains(t, prompt, "External Web Sources:")
	assert.Contains(t, prompt, "Instructions:")
	assert.Contains(t, prompt, "Answer:")
	assert.Contains(t, prompt, "Cite sources")
	assert.Contains(t, prompt, "confidence score")
	assert.Contains(t, prompt, "concise and professional")
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	handler := createHandlerWithConfig(t, createTestConfig())

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
			largeData[string(rune('a'+i%26))] = "test value"
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
	handler := createHandlerWithConfig(t, config)

	output, err := handler.Execute(context.Background(), &Input{Question: "Test"})

	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "LLM_SYNTHESIS_FAILED"))
	assert.Nil(t, output)
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		assert.NotEmpty(t, reqBody["prompt"])
		assert.NotNil(t, reqBody["context"])

		ctx := reqBody["context"].(map[string]interface{})
		assert.NotNil(t, ctx["internal"])
		assert.NotNil(t, ctx["external"])
		assert.NotNil(t, ctx["intent"])

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
	handler := createHandlerWithConfig(t, config)

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
		// BUG FIX: "franchise_inquiry" is NOT in the allowed list.
		// Use "search_franchise" which is the closest valid intent.
		Intent: Intent{
			PrimaryIntent: "search_franchise",
			Confidence:    0.88,
		},
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.NotEmpty(t, output.LLMResponse)
	assert.True(t, output.Confidence > 0.9)
	assert.Equal(t, 2, len(output.Sources))
}

// ==========================
// Validation Tests
// ==========================

func TestHandler_ValidateInput(t *testing.T) {
	handler := createHandlerWithConfig(t, createTestConfig())

	tests := []struct {
		name    string
		input   *Input
		wantErr bool
	}{
		{
			name:    "valid input",
			input:   &Input{Question: "Valid question?"},
			wantErr: false,
		},
		{
			name:    "empty question",
			input:   &Input{Question: ""},
			wantErr: true,
		},
		{
			name:    "question too long",
			input:   &Input{Question: string(make([]byte, 1001))},
			wantErr: true,
		},
		{
			name:    "SQL injection attempt",
			input:   &Input{Question: "test' OR '1'='1"},
			wantErr: true,
		},
		{
			name: "valid with all fields using allowed intent",
			// BUG FIX: Original used PrimaryIntent: "test" which is not in the allowed list.
			// The handler's validateIntent rejects any non-empty intent not in the allowed list.
			input: &Input{
				Question: "Test question",
				InternalData: map[string]interface{}{
					"key": "value",
				},
				WebData: WebData{
					Sources: []Source{{Title: "Test", URL: "http://test.com"}},
					Summary: "Summary",
				},
				Intent: Intent{PrimaryIntent: "general_inquiry", Confidence: 0.8},
			},
			wantErr: false,
		},
		{
			name: "invalid primary intent",
			input: &Input{
				Question: "Test question",
				Intent:   Intent{PrimaryIntent: "franchise_inquiry", Confidence: 0.8},
			},
			wantErr: true,
		},
		{
			name: "empty primary intent is allowed (optional field)",
			input: &Input{
				Question: "Test question",
				Intent:   Intent{PrimaryIntent: "", Confidence: 0.0},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateInput(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ==========================
// Circuit Breaker Tests
// ==========================

func TestHandler_CircuitBreaker(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		response := createLLMAPIResponse("Success", 0.8, []string{"test"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := createTestConfig()
	config.GenAIBaseURL = server.URL
	handler := createHandlerWithConfig(t, config)

	input := &Input{Question: "test"}
	output1, err1 := handler.Execute(context.Background(), input)
	assert.Error(t, err1)
	assert.Nil(t, output1)

	state := handler.GetCircuitBreakerState()
	assert.NotEmpty(t, state)
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
	handler := NewHandler(HandlerOptions{
		Config: config,
		Logger: &BenchmarkLogger{},
	})

	input := &Input{
		Question:     "Test question",
		InternalData: map[string]interface{}{"key": "value"},
		WebData:      WebData{Sources: []Source{}},
		Intent:       Intent{}, // empty intent to avoid validation failure
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_BuildPrompt(b *testing.B) {
	handler := NewHandler(HandlerOptions{
		Config: createTestConfig(),
		Logger: &BenchmarkLogger{},
	})
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
