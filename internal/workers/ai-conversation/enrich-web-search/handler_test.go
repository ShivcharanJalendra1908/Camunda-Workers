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
	handler := NewHandler(config, NewTestLogger(t))

	input := &Input{
		Question: "McDonald's franchise",
		Entities: []Entity{{Type: "franchise_name", Value: "McDonald's"}},
	}

	output, err := handler.execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, 1, len(output.WebData.Sources)) // PDF filtered out
	assert.NotEmpty(t, output.WebData.Summary)
	assert.Contains(t, output.WebData.Sources[0].URL, "mcdonalds")
}

func TestHandler_Execute_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ✅ CORRECT: Block forever using context
		<-r.Context().Done()
		// Don't write any response - let context timeout handle it
	}))
	defer server.Close()

	config := createTestConfig()
	config.SearchAPIBaseURL = server.URL
	config.Timeout = 50 * time.Millisecond
	handler := NewHandler(config, NewTestLogger(t))

	input := &Input{Question: "test", Entities: []Entity{}}
	output, err := handler.execute(context.Background(), input)

	// ✅ CORRECT: Must get exact WEB_SEARCH_TIMEOUT error
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
	handler := NewHandler(config, NewTestLogger(t))

	output, err := handler.execute(context.Background(), &Input{Question: "test"})

	assert.Error(t, err)
	assert.Nil(t, output)
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_BuildQuery(t *testing.T) {
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

	handler := NewHandler(createTestConfig(), NewTestLogger(t))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handler.buildQuery(tt.question, tt.entities)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHandler_BuildSearchURL(t *testing.T) {
	handler := NewHandler(createTestConfig(), NewTestLogger(t))
	url := handler.buildSearchURL("test query")

	assert.Contains(t, url, "key=test-api-key")
	assert.Contains(t, url, "cx=test-engine-id")
	assert.Contains(t, url, "q=test+query")
	assert.Contains(t, url, "num=5")
}

func TestHandler_ProcessResults(t *testing.T) {
	handler := NewHandler(createTestConfig(), NewTestLogger(t))

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
	handler := NewHandler(createTestConfig(), NewTestLogger(t))

	t.Run("empty sources", func(t *testing.T) {
		assert.Empty(t, handler.generateSummary([]Source{}))
	})

	t.Run("with sources", func(t *testing.T) {
		sources := []Source{{Snippet: "First snippet"}, {Snippet: "Second"}}
		assert.Equal(t, "First snippet", handler.generateSummary(sources))
	})
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	handler := NewHandler(createTestConfig(), NewTestLogger(t))

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
	handler := NewHandler(config, NewTestLogger(t))

	input := &Input{Question: "Test query", Entities: []Entity{}}
	output, err := handler.execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.GreaterOrEqual(t, len(output.WebData.Sources), 1)
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
	handler := NewHandler(config, benchLogger)
	input := &Input{Question: "Test", Entities: []Entity{}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.execute(context.Background(), input)
	}
}

// BenchmarkLogger is a minimal logger for benchmarks
type BenchmarkLogger struct{}

func (b *BenchmarkLogger) Info(msg string, fields map[string]interface{})  {}
func (b *BenchmarkLogger) Warn(msg string, fields map[string]interface{})  {}
func (b *BenchmarkLogger) Error(msg string, fields map[string]interface{}) {}
func (b *BenchmarkLogger) With(fields map[string]interface{}) Logger       { return b }

// // internal/workers/ai-conversation/enrich-web-search/handler_test.go
// package enrichwebsearch

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"net/http"
// 	"net/http/httptest"
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
// 		SearchAPIBaseURL: "http://localhost:8080/search",
// 		SearchAPIKey:     "test-api-key",
// 		SearchEngineID:   "test-engine-id",
// 		Timeout:          3 * time.Second,
// 		MaxResults:       5,
// 		MinRelevance:     0.5,
// 	}
// }

// func createSearchAPIResponse(items []map[string]interface{}) string {
// 	response := map[string]interface{}{"items": items}
// 	data, _ := json.Marshal(response)
// 	return string(data)
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		assert.Equal(t, "test-api-key", r.URL.Query().Get("key"))
// 		response := createSearchAPIResponse([]map[string]interface{}{
// 			{
// 				"link":    "https://mcdonalds.com/franchise",
// 				"title":   "McDonald's Official",
// 				"snippet": "Franchise opportunities",
// 				"mime":    "text/html",
// 			},
// 			{
// 				"link":    "https://example.com/pdf",
// 				"title":   "PDF Document",
// 				"snippet": "PDF content",
// 				"mime":    "application/pdf",
// 			},
// 		})
// 		w.Header().Set("Content-Type", "application/json")
// 		w.WriteHeader(http.StatusOK)
// 		w.Write([]byte(response))
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.SearchAPIBaseURL = server.URL
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	input := &Input{
// 		Question: "McDonald's franchise",
// 		Entities: []Entity{{Type: "franchise_name", Value: "McDonald's"}},
// 	}

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.Equal(t, 1, len(output.WebData.Sources)) // PDF filtered out
// 	assert.NotEmpty(t, output.WebData.Summary)
// 	assert.Contains(t, output.WebData.Sources[0].URL, "mcdonalds")
// }

// func TestHandler_Execute_Timeout(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		// ✅ CORRECT: Block forever using context
// 		<-r.Context().Done()
// 		// Don't write any response - let context timeout handle it
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.SearchAPIBaseURL = server.URL
// 	config.Timeout = 50 * time.Millisecond
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	input := &Input{Question: "test", Entities: []Entity{}}
// 	output, err := handler.execute(context.Background(), input)

// 	// ✅ CORRECT: Must get exact WEB_SEARCH_TIMEOUT error
// 	assert.Error(t, err)
// 	assert.True(t, errors.Is(err, ErrWebSearchTimeout),
// 		"Expected WEB_SEARCH_TIMEOUT, got: %v", err)
// 	assert.Nil(t, output)
// }

// func TestHandler_Execute_APIError(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		w.WriteHeader(http.StatusInternalServerError)
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.SearchAPIBaseURL = server.URL
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	output, err := handler.execute(context.Background(), &Input{Question: "test"})

// 	assert.Error(t, err)
// 	assert.Nil(t, output)
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_BuildQuery(t *testing.T) {
// 	tests := []struct {
// 		name     string
// 		question string
// 		entities []Entity
// 		want     string
// 	}{
// 		{
// 			name:     "simple query",
// 			question: "What is franchising?",
// 			entities: []Entity{},
// 			want:     "What is franchising?",
// 		},
// 		{
// 			name:     "with franchise entity",
// 			question: "Tell me about",
// 			entities: []Entity{{Type: "franchise_name", Value: "Subway"}},
// 			want:     "Tell me about Subway",
// 		},
// 		{
// 			name:     "multiple entities",
// 			question: "Find opportunities",
// 			entities: []Entity{
// 				{Type: "franchise_name", Value: "McDonald's"},
// 				{Type: "location", Value: "Texas"},
// 				{Type: "investment_amount", Value: "100000"}, // should be ignored
// 			},
// 			want: "Find opportunities McDonald's Texas",
// 		},
// 		{
// 			name:     "whitespace cleanup",
// 			question: "  Multiple   spaces   ",
// 			entities: []Entity{},
// 			want:     "Multiple spaces",
// 		},
// 	}

// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got := handler.buildQuery(tt.question, tt.entities)
// 			assert.Equal(t, tt.want, got)
// 		})
// 	}
// }

// func TestHandler_BuildSearchURL(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))
// 	url := handler.buildSearchURL("test query")

// 	assert.Contains(t, url, "key=test-api-key")
// 	assert.Contains(t, url, "cx=test-engine-id")
// 	assert.Contains(t, url, "q=test+query")
// 	assert.Contains(t, url, "num=5")
// }

// func TestHandler_ProcessResults(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	items := []struct {
// 		Link    string
// 		Title   string
// 		Snippet string
// 		Mime    string
// 	}{
// 		{Link: "https://example.com/page", Title: "HTML", Snippet: "Content", Mime: "text/html"},
// 		{Link: "https://example.com/doc.pdf", Title: "PDF", Snippet: "PDF", Mime: "application/pdf"},
// 		{Link: "https://example.com/page", Title: "Duplicate", Snippet: "Dup", Mime: "text/html"}, // duplicate
// 		{Link: "https://ftc.gov/franchise", Title: "Official Gov", Snippet: "Gov content", Mime: "text/html"},
// 	}

// 	sources := handler.processResults(items)

// 	assert.Equal(t, 2, len(sources))           // PDF and duplicate filtered
// 	assert.Contains(t, sources[0].URL, ".gov") // gov prioritized
// 	assert.True(t, sources[0].Relevance > 1.0)
// }

// func TestHandler_GenerateSummary(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	t.Run("empty sources", func(t *testing.T) {
// 		assert.Empty(t, handler.generateSummary([]Source{}))
// 	})

// 	t.Run("with sources", func(t *testing.T) {
// 		sources := []Source{{Snippet: "First snippet"}, {Snippet: "Second"}}
// 		assert.Equal(t, "First snippet", handler.generateSummary(sources))
// 	})
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(t))

// 	t.Run("empty question", func(t *testing.T) {
// 		query := handler.buildQuery("", []Entity{})
// 		assert.NotPanics(t, func() { handler.buildSearchURL(query) })
// 	})

// 	t.Run("special characters in query", func(t *testing.T) {
// 		query := handler.buildQuery("What about \"McDonald's\"?", []Entity{})
// 		url := handler.buildSearchURL(query)
// 		assert.NotEmpty(t, url)
// 	})

// 	t.Run("max results respected", func(t *testing.T) {
// 		items := make([]struct {
// 			Link    string
// 			Title   string
// 			Snippet string
// 			Mime    string
// 		}, 10)
// 		for i := 0; i < 10; i++ {
// 			items[i].Link = "https://example.com/" + string(rune('a'+i))
// 			items[i].Title = "Page"
// 			items[i].Snippet = "Content"
// 			items[i].Mime = "text/html"
// 		}
// 		sources := handler.processResults(items)
// 		assert.Equal(t, 5, len(sources)) // MaxResults = 5
// 	})
// }

// // ==========================
// // Integration Test (Simplified)
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		response := createSearchAPIResponse([]map[string]interface{}{
// 			{"link": "https://example.com", "title": "Test", "snippet": "Test snippet", "mime": "text/html"},
// 		})
// 		w.Header().Set("Content-Type", "application/json")
// 		w.WriteHeader(http.StatusOK)
// 		w.Write([]byte(response))
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.SearchAPIBaseURL = server.URL
// 	handler := NewHandler(config, zaptest.NewLogger(t))

// 	input := &Input{Question: "Test query", Entities: []Entity{}}
// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.GreaterOrEqual(t, len(output.WebData.Sources), 1)
// }

// // ==========================
// // Benchmark
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		response := createSearchAPIResponse([]map[string]interface{}{
// 			{"link": "https://example.com", "title": "Test", "snippet": "Snippet", "mime": "text/html"},
// 		})
// 		w.Header().Set("Content-Type", "application/json")
// 		w.WriteHeader(http.StatusOK)
// 		w.Write([]byte(response))
// 	}))
// 	defer server.Close()

// 	config := createTestConfig()
// 	config.SearchAPIBaseURL = server.URL
// 	handler := NewHandler(config, zaptest.NewLogger(b))
// 	input := &Input{Question: "Test", Entities: []Entity{}}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), input)
// 	}
// }
