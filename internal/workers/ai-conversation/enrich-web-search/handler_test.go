// internal/workers/ai-conversation/enrich-web-search/handler_test.go
package enrichwebsearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{
		SearchAPIBaseURL: "http://localhost:8080/search",
		SearchAPIKey:     "test-api-key",
		SearchEngineID:   "test-engine-id",
		Timeout:          3 * time.Second,
		MaxResults:       5,
		MinRelevance:     0.5,
	}
}

func createHandlerWithConfig(t *testing.T, config *Config) *Handler {
	return NewHandler(HandlerOptions{
		Config: config,
		Logger: NewTestLogger(t),
	})
}

func createSearchAPIResponse(items []map[string]interface{}) string {
	response := map[string]interface{}{"items": items}
	data, _ := json.Marshal(response)
	return string(data)
}

// ==========================
// Core Functionality Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-api-key", r.URL.Query().Get("key"))
		response := createSearchAPIResponse([]map[string]interface{}{
			{
				"link":    "https://mcdonalds.com/franchise",
				"title":   "McDonald's Official",
				"snippet": "Franchise opportunities",
				"mime":    "text/html",
			},
			{
				"link":    "https://example.com/pdf",
				"title":   "PDF Document",
				"snippet": "PDF content",
				"mime":    "application/pdf",
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := createTestConfig()
	config.SearchAPIBaseURL = server.URL
	handler := createHandlerWithConfig(t, config)

	input := &Input{
		Question: "McDonald's franchise",
		Entities: []Entity{{Type: "franchise_name", Value: "McDonald's"}},
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, 1, len(output.WebData.Sources)) // PDF filtered out
	assert.NotEmpty(t, output.WebData.Summary)
	assert.Contains(t, output.WebData.Sources[0].URL, "mcdonalds")
}

func TestHandler_Execute_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block forever using context
		<-r.Context().Done()
	}))
	defer server.Close()

	config := createTestConfig()
	config.SearchAPIBaseURL = server.URL
	config.Timeout = 50 * time.Millisecond
	handler := createHandlerWithConfig(t, config)

	input := &Input{Question: "test", Entities: []Entity{}}
	output, err := handler.Execute(context.Background(), input)

	// Must get exact WEB_SEARCH_TIMEOUT error
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrWebSearchTimeout),
		"Expected WEB_SEARCH_TIMEOUT, got: %v", err)
	assert.Nil(t, output)
}

func TestHandler_Execute_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	config := createTestConfig()
	config.SearchAPIBaseURL = server.URL
	handler := createHandlerWithConfig(t, config)

	output, err := handler.Execute(context.Background(), &Input{Question: "test"})

	assert.Error(t, err)
	assert.Nil(t, output)
}

// ==========================
// Unit Tests for Helper Methods
// ==========================

func TestHandler_BuildQuery(t *testing.T) {
	handler := createHandlerWithConfig(t, createTestConfig())

	tests := []struct {
		name     string
		question string
		entities []Entity
		want     string
	}{
		{
			name:     "simple query",
			question: "What is franchising?",
			entities: []Entity{},
			want:     "What is franchising?",
		},
		{
			name:     "with franchise entity",
			question: "Tell me about",
			entities: []Entity{{Type: "franchise_name", Value: "Subway"}},
			want:     "Tell me about Subway",
		},
		{
			name:     "multiple entities",
			question: "Find opportunities",
			entities: []Entity{
				{Type: "franchise_name", Value: "McDonald's"},
				{Type: "location", Value: "Texas"},
				{Type: "investment_amount", Value: "100000"}, // should be ignored
			},
			want: "Find opportunities McDonald's Texas",
		},
		{
			name:     "whitespace cleanup",
			question: "  Multiple   spaces   ",
			entities: []Entity{},
			want:     "Multiple spaces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handler.buildQuery(tt.question, tt.entities)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHandler_BuildSearchURL(t *testing.T) {
	handler := createHandlerWithConfig(t, createTestConfig())
	url := handler.buildSearchURL("test query")

	assert.Contains(t, url, "key=test-api-key")
	assert.Contains(t, url, "cx=test-engine-id")
	assert.Contains(t, url, "q=test+query")
	assert.Contains(t, url, "num=5")
}

func TestHandler_ProcessResults(t *testing.T) {
	handler := createHandlerWithConfig(t, createTestConfig())

	items := []struct {
		Link    string
		Title   string
		Snippet string
		Mime    string
	}{
		{Link: "https://example.com/page", Title: "HTML", Snippet: "Content", Mime: "text/html"},
		{Link: "https://example.com/doc.pdf", Title: "PDF", Snippet: "PDF", Mime: "application/pdf"},
		{Link: "https://example.com/page", Title: "Duplicate", Snippet: "Dup", Mime: "text/html"}, // duplicate
		{Link: "https://ftc.gov/franchise", Title: "Official Gov", Snippet: "Gov content", Mime: "text/html"},
	}

	sources := handler.processResults(items)

	assert.Equal(t, 2, len(sources))           // PDF and duplicate filtered
	assert.Contains(t, sources[0].URL, ".gov") // gov prioritized
	assert.True(t, sources[0].Relevance > 1.0)
}

func TestHandler_GenerateSummary(t *testing.T) {
	handler := createHandlerWithConfig(t, createTestConfig())

	t.Run("empty sources", func(t *testing.T) {
		assert.Empty(t, handler.generateSummary([]Source{}))
	})

	t.Run("with sources", func(t *testing.T) {
		sources := []Source{{Snippet: "First snippet"}, {Snippet: "Second"}}
		assert.Equal(t, "First snippet", handler.generateSummary(sources))
	})
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
			input:   &Input{Question: "Valid question?", Entities: []Entity{}},
			wantErr: false,
		},
		{
			name:    "empty question",
			input:   &Input{Question: ""},
			wantErr: true,
		},
		{
			name:    "question too short",
			input:   &Input{Question: "ab"},
			wantErr: true,
		},
		{
			name:    "question too long",
			input:   &Input{Question: string(make([]byte, 501))},
			wantErr: true,
		},
		{
			name:    "SQL injection attempt",
			input:   &Input{Question: "test' OR '1'='1"},
			wantErr: true,
		},
		{
			name: "too many entities",
			input: &Input{
				Question: "test",
				Entities: make([]Entity, 51),
			},
			wantErr: true,
		},
		{
			name: "invalid entity type",
			input: &Input{
				Question: "test",
				Entities: []Entity{{Type: "invalid_type", Value: "test"}},
			},
			wantErr: true,
		},
		{
			name: "empty entity value",
			input: &Input{
				Question: "test",
				Entities: []Entity{{Type: "franchise_name", Value: ""}},
			},
			wantErr: true,
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
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	handler := createHandlerWithConfig(t, createTestConfig())

	t.Run("empty question", func(t *testing.T) {
		query := handler.buildQuery("", []Entity{})
		assert.NotPanics(t, func() { handler.buildSearchURL(query) })
	})

	t.Run("special characters in query", func(t *testing.T) {
		query := handler.buildQuery("What about \"McDonald's\"?", []Entity{})
		url := handler.buildSearchURL(query)
		assert.NotEmpty(t, url)
	})

	t.Run("max results respected", func(t *testing.T) {
		items := make([]struct {
			Link    string
			Title   string
			Snippet string
			Mime    string
		}, 10)
		for i := 0; i < 10; i++ {
			items[i].Link = "https://example.com/" + string(rune('a'+i))
			items[i].Title = "Page"
			items[i].Snippet = "Content"
			items[i].Mime = "text/html"
		}
		sources := handler.processResults(items)
		assert.Equal(t, 5, len(sources)) // MaxResults = 5
	})
}

// ==========================
// Integration Test (Simplified)
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := createSearchAPIResponse([]map[string]interface{}{
			{"link": "https://example.com", "title": "Test", "snippet": "Test snippet", "mime": "text/html"},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := createTestConfig()
	config.SearchAPIBaseURL = server.URL
	handler := createHandlerWithConfig(t, config)

	input := &Input{Question: "Test query", Entities: []Entity{}}
	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.GreaterOrEqual(t, len(output.WebData.Sources), 1)
}

// ==========================
// Circuit Breaker Tests
// ==========================

func TestHandler_CircuitBreaker(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 3 {
			// First 3 calls fail
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// After that, succeed
		response := createSearchAPIResponse([]map[string]interface{}{
			{"link": "https://example.com", "title": "Test", "snippet": "Test", "mime": "text/html"},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := createTestConfig()
	config.SearchAPIBaseURL = server.URL
	handler := createHandlerWithConfig(t, config)

	// First call should fail but not open circuit yet
	input := &Input{Question: "test", Entities: []Entity{}}
	output1, err1 := handler.Execute(context.Background(), input)
	assert.Error(t, err1)
	assert.Nil(t, output1)

	// Get circuit breaker state
	state := handler.GetCircuitBreakerState()
	assert.NotEmpty(t, state)

	// Get metrics
	metrics := handler.GetCircuitBreakerMetrics()
	assert.NotNil(t, metrics)
}

// ==========================
// URL Validation Tests
// ==========================

func TestHandler_URLValidation(t *testing.T) {
	handler := createHandlerWithConfig(t, createTestConfig())

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "valid https URL",
			url:     "https://example.com/search?q=test",
			wantErr: false,
		},
		{
			name:    "valid http URL",
			url:     "http://example.com/search?q=test",
			wantErr: false,
		},
		{
			name:    "invalid protocol",
			url:     "ftp://example.com",
			wantErr: true,
		},
		{
			name:    "localhost URL",
			url:     "http://localhost:8080/search",
			wantErr: true,
		},
		{
			name:    "127.0.0.1 URL",
			url:     "http://127.0.0.1/search",
			wantErr: true,
		},
		{
			name:    "private IP range",
			url:     "http://192.168.1.1/search",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateURL(tt.url)
			if tt.wantErr {
				assert.Error(t, err, "Expected error for URL: %s", tt.url)
			} else {
				assert.NoError(t, err, "Expected no error for URL: %s", tt.url)
			}
		})
	}
}

// ==========================
// Benchmark
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := createSearchAPIResponse([]map[string]interface{}{
			{"link": "https://example.com", "title": "Test", "snippet": "Snippet", "mime": "text/html"},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := createTestConfig()
	config.SearchAPIBaseURL = server.URL

	// Create a simple benchmark logger
	benchLogger := &BenchmarkLogger{}
	handler := NewHandler(HandlerOptions{
		Config: config,
		Logger: benchLogger,
	})
	input := &Input{Question: "Test", Entities: []Entity{}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

// BenchmarkLogger is a minimal logger for benchmarks
type BenchmarkLogger struct{}

func (b *BenchmarkLogger) Info(msg string, fields map[string]interface{})  {}
func (b *BenchmarkLogger) Warn(msg string, fields map[string]interface{})  {}
func (b *BenchmarkLogger) Error(msg string, fields map[string]interface{}) {}
func (b *BenchmarkLogger) With(fields map[string]interface{}) Logger       { return b }
