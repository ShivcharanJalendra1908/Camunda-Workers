// FILE: internal/workers/ai-conversation/ai-search/handler_test.go
package ai_search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/pb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"camunda-workers/internal/common/logger"
)

// MockLogger
type MockLogger struct{ mock.Mock }

func (m *MockLogger) Debug(msg string, fields map[string]interface{})        {}
func (m *MockLogger) Info(msg string, fields map[string]interface{})         {}
func (m *MockLogger) Warn(msg string, fields map[string]interface{})         {}
func (m *MockLogger) Error(msg string, fields map[string]interface{})        {}
func (m *MockLogger) With(fields map[string]interface{}) logger.Logger       { return m }
func (m *MockLogger) WithError(err error) logger.Logger                      { return m }
func (m *MockLogger) WithFields(fields map[string]interface{}) logger.Logger { return m }

// newJobWithVariables constructs a real entities.Job value with Variables set.
func newJobWithVariables(variables string) entities.Job {
	return entities.Job{
		ActivatedJob: &pb.ActivatedJob{
			Variables: variables,
		},
	}
}

func createTestConfig() *Config {
	return &Config{
		LLMEndpoint:     "http://localhost:11434",
		LLMModel:        "llama2",
		LLMMaxTokens:    1000,
		LLMTemperature:  0.1,
		DefaultPageSize: 20,
		MaxQueryLength:  500,
		IndexName:       "franchises",
	}
}

// ============================================================
// PARAMETER EXTRACTOR TESTS
// ============================================================

func TestParameterExtractor_BuildPrompt(t *testing.T) {
	config := createTestConfig()
	pe := NewParameterExtractor(config)

	tests := []struct {
		name         string
		query        string
		wantContains []string
	}{
		{
			name:  "Prompt includes taxonomy structure",
			query: "ice cream franchise in kolkata",
			wantContains: []string{
				"TAXONOMY:", "Industry → Category → Subcategory",
				"Return ONLY valid JSON:", "ice cream franchise in kolkata",
			},
		},
		{
			name:  "Prompt includes all parameter fields",
			query: "test",
			wantContains: []string{
				`"industry":`, `"category":`, `"subcategory":`,
				`"location":`, `"investment":`, `"rating":`,
				`"space":`, `"staff":`, `"outlets":`, `"roi":`,
				`"verified":`, `"trusted_seller":`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := pe.BuildPrompt(tt.query)
			for _, want := range tt.wantContains {
				assert.Contains(t, prompt, want)
			}
		})
	}
}

func TestParameterExtractor_Parse(t *testing.T) {
	config := createTestConfig()
	pe := NewParameterExtractor(config)

	tests := []struct {
		name        string
		llmResponse string
		wantErr     bool
		errContains string
		assertions  func(*testing.T, *ExtractedParameters)
	}{
		{
			name:        "Valid JSON response",
			llmResponse: `{"Industry":"Food & Beverage","Category":"Dessert & Frozen Treats","Location":"Kolkata"}`,
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, "Food & Beverage", p.Industry)
				assert.Equal(t, "Dessert & Frozen Treats", p.Category)
				assert.NotNil(t, p.Location)
				assert.Equal(t, "Kolkata", p.Location.City)
			},
		},
		{
			name:        "JSON with markdown code blocks",
			llmResponse: "```json\n{\"Industry\":\"Education\",\"Category\":\"Tutoring\"}\n```",
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, "Education", p.Industry)
				assert.Equal(t, "Tutoring", p.Category)
			},
		},
		{
			name:        "JSON with extra text",
			llmResponse: `Sure! {"Category":"Fashion"} Hope this helps!`,
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, "Fashion", p.Category)
			},
		},
		{
			name:        "Invalid JSON - should error",
			llmResponse: `{"Industry": "Food", invalid}`,
			wantErr:     true,
			errContains: "JSON parse failed",
		},
		{
			name:        "No JSON object found",
			llmResponse: "I couldn't extract parameters",
			wantErr:     true,
			errContains: "no valid JSON found",
		},
		{
			name:        "Location normalization",
			llmResponse: `{"Location":"mumbai"}`,
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, "Mumbai", p.Location.City)
				assert.Equal(t, "India", p.Location.Country)
			},
		},
		{
			name:        "Investment normalization",
			llmResponse: `{"Minimum_Investment":"10L","Maximum_Investment":"50L"}`,
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, float64(1000000), p.Investment.Min)
				assert.Equal(t, float64(5000000), p.Investment.Max)
			},
		},
		{
			name:        "ROI clamping",
			llmResponse: `{"ROI":150}`,
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, 100.0, p.ROI.Min)
			},
		},
		{
			name:        "Rating clamped to 0-5",
			llmResponse: `{"Rating":7.5}`,
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, 5.0, *p.Rating)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, err := pe.Parse(tt.llmResponse)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				assert.Nil(t, params)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, params)
				if tt.assertions != nil {
					tt.assertions(t, params)
				}
			}
		})
	}
}

func TestParameterExtractor_ParseWithFallback(t *testing.T) {
	pe := NewParameterExtractor(createTestConfig())

	t.Run("Valid response", func(t *testing.T) {
		params := pe.ParseWithFallback(`{"Industry":"Food & Beverage","Category":"Quick Service"}`)
		assert.NotNil(t, params)
		assert.Equal(t, "Food & Beverage", params.Industry)
	})
	t.Run("Invalid JSON returns empty", func(t *testing.T) {
		params := pe.ParseWithFallback(`{"Invalid": JSON}`)
		assert.NotNil(t, params)
		assert.Equal(t, "", params.Industry)
		assert.Nil(t, params.Location)
	})
	t.Run("Empty response returns empty", func(t *testing.T) {
		params := pe.ParseWithFallback("")
		assert.NotNil(t, params)
		assert.Equal(t, "", params.Category)
	})
}

func TestParameterExtractor_normalizeParameters(t *testing.T) {
	pe := NewParameterExtractor(createTestConfig())

	tests := []struct {
		name       string
		input      *ExtractedParameters
		wantErr    bool
		assertions func(*testing.T, *ExtractedParameters)
	}{
		{
			name:  "String fields trimmed",
			input: &ExtractedParameters{Industry: "  Food & Beverage  ", Category: "\tRetail\n", Subcategory: "  "},
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, "Food & Beverage", p.Industry)
				assert.Equal(t, "Retail", p.Category)
				assert.Equal(t, "", p.Subcategory)
			},
		},
		{
			name:  "Location title-cased, country defaulted",
			input: &ExtractedParameters{Location: &LocationFilter{City: "mumbai", State: "maharashtra"}},
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, "Mumbai", p.Location.City)
				assert.Equal(t, "Maharashtra", p.Location.State)
				assert.Equal(t, "India", p.Location.Country)
			},
		},
		{
			name:  "Investment swapped when reversed",
			input: &ExtractedParameters{Investment: &InvestmentFilter{Min: 5000000, Max: 1000000}},
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, float64(1000000), p.Investment.Min)
				assert.Equal(t, float64(5000000), p.Investment.Max)
			},
		},
		{
			name:  "ROI clamped",
			input: &ExtractedParameters{ROI: &RangeFilter{Min: -5, Max: 150}},
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, 0.0, p.ROI.Min)
				assert.Equal(t, 100.0, p.ROI.Max)
			},
		},
		{
			name:  "Rating clamped to 5.0",
			input: &ExtractedParameters{Rating: func() *float64 { v := 7.5; return &v }()},
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, 5.0, *p.Rating)
			},
		},
		{
			name:  "Space auto-fills max (5x)",
			input: &ExtractedParameters{Space: &RangeFilter{Min: 500, Max: 0}},
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, 500.0, p.Space.Min)
				assert.Equal(t, 2500.0, p.Space.Max)
			},
		},
		{
			name:  "Staff auto-fills max (3x)",
			input: &ExtractedParameters{Staff: &RangeFilter{Min: 2, Max: 0}},
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, 2.0, p.Staff.Min)
				assert.Equal(t, 6.0, p.Staff.Max)
			},
		},
		{
			name:  "Negative outlets clamped to 0",
			input: &ExtractedParameters{Outlets: func() *int { v := -5; return &v }()},
			assertions: func(t *testing.T, p *ExtractedParameters) {
				assert.Equal(t, 0, *p.Outlets)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := pe.normalizeParameters(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.assertions != nil {
					tt.assertions(t, tt.input)
				}
			}
		})
	}
}

// ============================================================
// HANDLER UNIT TESTS
// ============================================================

func TestValidateInput(t *testing.T) {
	handler := &Handler{config: createTestConfig(), logger: &MockLogger{}}

	tests := []struct {
		name      string
		input     *SearchInput
		wantErr   bool
		wantQuery string
	}{
		{name: "Nil input", input: nil, wantErr: true},
		{name: "Empty → wildcard", input: &SearchInput{Query: ""}, wantQuery: "*"},
		{name: "Whitespace → wildcard", input: &SearchInput{Query: "   "}, wantQuery: "*"},
		{name: "Valid query", input: &SearchInput{Query: "food franchises"}, wantQuery: "food franchises"},
		{name: "Too long", input: &SearchInput{Query: strings.Repeat("a", 501)}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateInput(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.wantQuery != "" {
					assert.Equal(t, tt.wantQuery, tt.input.Query)
				}
			}
		})
	}
}

func TestBuildBasicQuery(t *testing.T) {
	handler := &Handler{config: createTestConfig(), logger: &MockLogger{}}
	tests := []struct {
		query, wantType string
	}{
		{"*", "match_all"},
		{"   ", "match_all"},
		{"food franchises", "multi_match"},
	}
	for _, tt := range tests {
		q := handler.buildBasicQuery(tt.query)
		assert.Equal(t, handler.config.DefaultPageSize, q["size"])
		queryMap := q["query"].(map[string]interface{})
		assert.Contains(t, queryMap, tt.wantType)
		if tt.wantType == "multi_match" {
			assert.Equal(t, tt.query, queryMap["multi_match"].(map[string]interface{})["query"])
		}
	}
}

func TestBuildElasticsearchQuery(t *testing.T) {
	handler := &Handler{config: createTestConfig(), logger: &MockLogger{}}
	tests := []struct {
		name           string
		params         *ExtractedParameters
		wantQueryType  string
		wantPostFilter bool
	}{
		{
			name:          "Category only → simple_query_string",
			params:        &ExtractedParameters{Category: "Food & Beverage"},
			wantQueryType: "simple_query_string",
		},
		{
			name:           "With ROI → post_filter",
			params:         &ExtractedParameters{Category: "Education", ROI: &RangeFilter{Min: 15, Max: 25}},
			wantQueryType:  "simple_query_string",
			wantPostFilter: true,
		},
		{
			name:          "Nil params → match_all",
			params:        nil,
			wantQueryType: "match_all",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := handler.buildElasticsearchQuery(tt.params)
			assert.NoError(t, err)
			assert.Contains(t, q["query"].(map[string]interface{}), tt.wantQueryType)
			if tt.wantPostFilter {
				assert.Contains(t, q, "post_filter")
			} else {
				assert.NotContains(t, q, "post_filter")
			}
		})
	}
}

func TestBuildResponse(t *testing.T) {
	handler := &Handler{config: createTestConfig(), logger: &MockLogger{}}
	input := &SearchInput{Query: "food in Delhi"}
	params := &ExtractedParameters{
		Category:   "Food",
		Location:   &LocationFilter{City: "Delhi"},
		Investment: &InvestmentFilter{Min: 2000000, Max: 3000000},
	}
	results := &SearchResults{Total: 5, MaxScore: 1.5, TookMs: 20, Hits: []map[string]interface{}{}}

	resp := handler.buildResponse(input, params, results)
	assert.Equal(t, true, resp["success"])
	ep := resp["extractedParams"].(map[string]interface{})
	assert.Equal(t, "Food", ep["category"])
	assert.Equal(t, "Delhi", ep["location"])
	assert.Equal(t, float64(2000000), ep["minInvestment"])
	assert.Contains(t, ep["tags"].([]string), "Food")

	meta := resp["metadata"].(map[string]interface{})
	assert.Contains(t, meta, "processed_at")
	assert.Equal(t, int64(5), meta["total_found"])
}

// ============================================================
// TestParseInput — real entities.Job, no MockJob
// ============================================================

func TestParseInput(t *testing.T) {
	handler := &Handler{config: createTestConfig(), logger: &MockLogger{}}

	tests := []struct {
		name      string
		variables string
		wantQuery string
		wantErr   bool
	}{
		{name: "query field", variables: `{"query":"food"}`, wantQuery: "food"},
		{name: "searchQuery fallback", variables: `{"searchQuery":"retail"}`, wantQuery: "retail"},
		{name: "search_query fallback", variables: `{"search_query":"edu"}`, wantQuery: "edu"},
		{name: "text fallback", variables: `{"text":"fashion"}`, wantQuery: "fashion"},
		{name: "invalid JSON", variables: `{"bad": json}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := newJobWithVariables(tt.variables)
			input, err := handler.parseInput(job)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantQuery, input.Query)
			}
		})
	}
}

func TestExtractParametersWithFallback(t *testing.T) {
	config := createTestConfig()
	handler := &Handler{
		config:         config,
		logger:         &MockLogger{},
		paramExtractor: NewParameterExtractor(config),
	}

	t.Run("Wildcard returns empty", func(t *testing.T) {
		p := handler.extractParametersWithFallback(context.Background(), &SearchInput{Query: "*"})
		assert.NotNil(t, p)
		assert.Equal(t, "", p.Category)
	})
	t.Run("Empty returns empty", func(t *testing.T) {
		p := handler.extractParametersWithFallback(context.Background(), &SearchInput{Query: "   "})
		assert.NotNil(t, p)
		assert.Equal(t, "", p.Industry)
	})
}

// ============================================================
// OLLAMA SERVICE TESTS
// ============================================================

func TestOllamaService_Extract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/generate", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"llama2","response":"{\"category\":\"Food\"}","done":true}`))
	}))
	defer server.Close()

	svc := NewOllamaService(&Config{LLMEndpoint: server.URL, LLMModel: "llama2", LLMMaxTokens: 1000}, &MockLogger{})

	t.Run("Success", func(t *testing.T) {
		resp, err := svc.Extract(context.Background(), "test")
		assert.NoError(t, err)
		assert.Contains(t, resp, "Food")
	})
	t.Run("Timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()
		_, err := svc.Extract(ctx, "test")
		assert.Error(t, err)
	})
}

// ============================================================
// EDGE CASES
// ============================================================

func TestEdgeCases(t *testing.T) {
	h := &Handler{config: createTestConfig(), logger: &MockLogger{}}

	t.Run("Zero range values", func(t *testing.T) {
		q, err := h.buildElasticsearchQuery(&ExtractedParameters{
			ROI: &RangeFilter{Min: 0, Max: 0}, Investment: &InvestmentFilter{Min: 0, Max: 0},
		})
		assert.NoError(t, err)
		assert.NotNil(t, q)
	})
	t.Run("Empty text fields → match_all", func(t *testing.T) {
		q, err := h.buildElasticsearchQuery(&ExtractedParameters{Location: &LocationFilter{City: ""}})
		assert.NoError(t, err)
		assert.Contains(t, q["query"].(map[string]interface{}), "match_all")
	})
	t.Run("Empty struct → no post_filter", func(t *testing.T) {
		q, err := h.buildElasticsearchQuery(&ExtractedParameters{})
		assert.NoError(t, err)
		assert.NotContains(t, q, "post_filter")
	})
}

// ============================================================
// BENCHMARKS
// ============================================================

func BenchmarkBuildElasticsearchQuery(b *testing.B) {
	h := &Handler{config: createTestConfig(), logger: &MockLogger{}}
	p := &ExtractedParameters{
		Category: "Retail", Location: &LocationFilter{City: "Mumbai"},
		ROI: &RangeFilter{Min: 15, Max: 25}, Investment: &InvestmentFilter{Min: 1000000, Max: 5000000},
		Rating:   func() *float64 { v := 4.0; return &v }(),
		Verified: func() *bool { v := true; return &v }(),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = h.buildElasticsearchQuery(p)
	}
}

func BenchmarkParameterExtractor_Parse(b *testing.B) {
	pe := NewParameterExtractor(createTestConfig())
	resp := `{"industry":"Food & Beverage","category":"Quick Service","location":{"city":"Mumbai","country":"India"},"investment":{"min":1000000,"max":5000000}}`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pe.Parse(resp)
	}
}
