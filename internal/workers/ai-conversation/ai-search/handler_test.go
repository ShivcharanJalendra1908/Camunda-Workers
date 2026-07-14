// FILE: internal/workers/ai-conversation/ai-search/handler_test.go
package ai_search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
				"Return ONLY valid JSON", "ice cream franchise in kolkata",
			},
		},
		{
			name:  "Prompt includes all parameter fields",
			query: "test",
			wantContains: []string{
				`"Industry":`, `"Category":`, `"Subcategory":`,
				`"Location":`, `"Minimum_Investment":`, `"Maximum_Investment":`,
				`"Area_Requirement":`, `"ROI":`, `"Rating":`, `"Staff":`, 
				`"Outlets":`, `"Verified":`, `"Trusted_Seller":`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := pe.BuildPrompt(tt.query, "franchise")
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
		{"food franchises", "bool"},
	}
	for _, tt := range tests {
		q := handler.buildBasicQuery(tt.query, "")
		assert.Equal(t, handler.config.DefaultPageSize, q["size"])
		queryMap := q["query"].(map[string]interface{})
		assert.Contains(t, queryMap, tt.wantType)
	}
}

func TestBuildElasticsearchQuery(t *testing.T) {
	handler := &Handler{config: createTestConfig(), logger: &MockLogger{}}
	tests := []struct {
		name          string
		params        *ExtractedParameters
		wantQueryType string
	}{
		{
			name:          "Category only → bool query",
			params:        &ExtractedParameters{Category: "Food & Beverage"},
			wantQueryType: "bool",
		},
		{
			name:          "With ROI → bool query",
			params:        &ExtractedParameters{Category: "Education", ROI: &RangeFilter{Min: 15, Max: 25}},
			wantQueryType: "bool",
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
		EntityType: "association",
	}
	results := &SearchResults{Total: 5, MaxScore: 1.5, TookMs: 20, Hits: []map[string]interface{}{}}

	resp := handler.buildResponse(input, params, results)
	assert.Equal(t, true, resp["success"])
	ep := resp["extractedParams"].(map[string]interface{})
	assert.Equal(t, "Food", ep["category"])
	assert.Equal(t, "Delhi", ep["location"])
	assert.Equal(t, float64(2000000), ep["minInvestment"])
	assert.Equal(t, "association", ep["entityType"])
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
	t.Run("Wildcard returns empty with EntityType fallback", func(t *testing.T) {
		p := handler.extractParametersWithFallback(context.Background(), &SearchInput{Query: "*", EntityType: "association"})
		assert.NotNil(t, p)
		assert.Equal(t, "association", p.EntityType)
	})
	t.Run("Empty returns empty with EntityType fallback", func(t *testing.T) {
		p := handler.extractParametersWithFallback(context.Background(), &SearchInput{Query: "   ", EntityType: "master-franchise"})
		assert.NotNil(t, p)
		assert.Equal(t, "master_franchise", p.EntityType)
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
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Pre-cancel to guarantee deterministic timeout/cancellation error
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

func TestParameterExtractor_StressTest(t *testing.T) {
	pe := NewParameterExtractor(createTestConfig())

	type testCase struct {
		name     string
		query    string
		llmResp  string
		expected map[string]interface{}
	}

	scenarios := []testCase{
		{
			name:    "Dual Loc: English - Delhi to Pune",
			query:   "I am currently in Delhi but want to start a business in Pune",
			llmResp: `{"Location": "Delhi"}`,
			expected: map[string]interface{}{"City": "Pune"},
		},
		{
			name:    "Investment: Crores",
			query:   "Searching for franchises under 2 Cr in Mumbai",
			llmResp: `{"Maximum_Investment": "2 Cr", "Location": "Mumbai"}`,
			expected: map[string]interface{}{"MaxInv": 20000000.0, "City": "Mumbai"},
		},
		{
			name:    "ROI: Percentage Range",
			query:   "ROI between 20% and 40% in Hyderabad",
			llmResp: `{"ROI": "20% to 40%", "Location": "Hyderabad"}`,
			expected: map[string]interface{}{"ROIMin": 20.0, "City": "Hyderabad"},
		},
		{
			name:    "Space: Square Feet",
			query:   "1000 square feet shop needed in Kolkata",
			llmResp: `{"Area_Requirement": "1000", "Location": "Kolkata"}`,
			expected: map[string]interface{}{"SpaceMin": 800.0, "City": "Kolkata"},
		},
		{
			name:    "HQ Ambiguity",
			query:   "This brand is headquartered in Chennai, but I want to open it in Gurgaon",
			llmResp: `{"Location": "Chennai"}`,
			expected: map[string]interface{}{"City": "Gurugram"}, // Normalized name
		},
		{
			name:    "Main Road Bug Fix",
			query:   "Main road location available for food franchise in Bangalore",
			llmResp: `{"Location": "null"}`,
			expected: map[string]interface{}{"City": "Bengaluru"},
		},
		{
			name:    "Zone Priority: Northeast",
			query:   "Northeast India expansion models under 5L",
			llmResp: `{"Location": "null", "Maximum_Investment": "5L"}`,
			expected: map[string]interface{}{"City": "Northeast India"},
		},
		{
			name:    "Hinglish: Mumbai to Chennai",
			query:   "Main Mumbai mein hun, Chennai mein lena hai",
			llmResp: `{"Location": "Mumbai"}`,
			expected: map[string]interface{}{"City": "Chennai"},
		},
		{
			name:    "Investment: Lakhs",
			query:   "Business around 25 Lakhs in Delhi NCR",
			llmResp: `{"Maximum_Investment": "25L", "Location": "Delhi"}`,
			expected: map[string]interface{}{"MaxInv": 2500000.0, "City": "Delhi NCR"},
		},
		{
			name:    "State Match: West Bengal",
			query:   "Education franchise for West Bengal",
			llmResp: `{"Location": "West Bengal"}`,
			expected: map[string]interface{}{"City": "West Bengal", "State": "West Bengal"},
		},
		{
			name:    "Combination Query",
			query:   "I need a retail franchise in Punjab with 30% ROI and 500 sq ft space",
			llmResp: `{"Location": "Punjab", "ROI": "30%", "Area_Requirement": "500", "Industry": "Retail"}`,
			expected: map[string]interface{}{"City": "Punjab", "ROIMin": 30.0, "SpaceMin": 400.0},
		},
		{
			name:    "Negative: No location",
			query:   "Best low investment food brands",
			llmResp: `{"Industry": "Food", "Minimum_Investment": "1L"}`,
			expected: map[string]interface{}{"City": ""},
		},
		{
			name:    "Current Based In",
			query:   "Currently based in Noida, seeking opportunities in Jaipur",
			llmResp: `{"Location": "Noida"}`,
			expected: map[string]interface{}{"City": "Jaipur"},
		},
		{
			name:    "Hinglish: Patna se Kanpur",
			query:   "Main Patna se hun, Kanpur mein business karna hai",
			llmResp: `{"Location": "Patna"}`,
			expected: map[string]interface{}{"City": "Kanpur"},
		},
		{
			name:    "Pan India Target",
			query:   "Nationwide franchise options above 50L",
			llmResp: `{"Minimum_Investment": "50L", "Location": "Pan India"}`,
			expected: map[string]interface{}{"City": "Pan India"},
		},
		{
			name:    "Central India Search",
			query:   "Central India dealership offers",
			llmResp: `{"Location": "null"}`,
			expected: map[string]interface{}{"City": "Central India"},
		},
		{
			name:    "Substring Prevention: Puneet",
			query:   "Puneet's garments franchise in Delhi",
			llmResp: `{"Location": "Delhi"}`,
			expected: map[string]interface{}{"City": "Delhi NCR"}, // Should NOT pick Pune, but Delhi normalizes to Delhi NCR
		},
		{
			name:    "Investment Min/Max Range",
			query:   "Franchise above 10L but below 30L in Surat",
			llmResp: `{"Minimum_Investment": "10L", "Maximum_Investment": "30L", "Location": "Surat"}`,
			expected: map[string]interface{}{"MinInv": 1000000.0, "MaxInv": 3000000.0, "City": "Surat"},
		},
		{
			name:    "Simple English Query",
			query:   "What are the food franchises in Ahmedabad?",
			llmResp: `{"Industry": "Food", "Location": "Ahmedabad"}`,
			expected: map[string]interface{}{"City": "Ahmedabad"},
		},
		{
			name:    "Complex ROI Phrasing",
			query:   "High profit business with ROI more than 25 percent in Indore",
			llmResp: `{"ROI": "25", "Location": "Indore"}`,
			expected: map[string]interface{}{"ROIMin": 25.0, "City": "Indore"},
		},
		{
			name:    "Under 1 Lakh Fallback",
			query:   "franchise under 1 lakh",
			llmResp: `{"Industry": null, "Category": null, "Minimum_Investment": null, "Maximum_Investment": null}`,
			expected: map[string]interface{}{"MaxInv": 100000.0, "MinInv": 10000.0},
		},
		{
			name:    "Industry Fallback: Food",
			query:   "food franchise under 5 lakh",
			llmResp: `{"Industry": null, "Category": null, "Maximum_Investment": null}`,
			expected: map[string]interface{}{"Industry": "Food & Beverage", "MaxInv": 500000.0},
		},
		{
			name:    "Range Investment Fallback",
			query:   "budget between 5 to 10 lakh",
			llmResp: `{"Industry": null, "Maximum_Investment": null}`,
			expected: map[string]interface{}{"MinInv": 500000.0, "MaxInv": 1000000.0},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			params := pe.ParseWithContext(sc.llmResp, sc.query)

			if expectedCity, ok := sc.expected["City"].(string); ok {
				if expectedCity == "" {
					assert.Nil(t, params.Location, "Location should be nil for: %s", sc.query)
				} else {
					if assert.NotNil(t, params.Location, "Location missing for: %s", sc.query) {
						assert.Equal(t, expectedCity, params.Location.City, "City mismatch for: %s", sc.query)
					}
				}
			}

			if expectedState, ok := sc.expected["State"].(string); ok {
				assert.Equal(t, expectedState, params.Location.State)
			}

			if expectedMax, ok := sc.expected["MaxInv"].(float64); ok {
				if assert.NotNil(t, params.Investment) {
					assert.Equal(t, expectedMax, params.Investment.Max)
				}
			}

			if expectedMin, ok := sc.expected["MinInv"].(float64); ok {
				if assert.NotNil(t, params.Investment) {
					assert.Equal(t, expectedMin, params.Investment.Min)
				}
			}

			if expectedROIMin, ok := sc.expected["ROIMin"].(float64); ok {
				if assert.NotNil(t, params.ROI) {
					assert.Equal(t, expectedROIMin, params.ROI.Min)
				}
			}

			if expectedSpaceMin, ok := sc.expected["SpaceMin"].(float64); ok {
				if assert.NotNil(t, params.Space) {
					assert.Equal(t, expectedSpaceMin, params.Space.Min)
				}
			}

			if expectedIndustry, ok := sc.expected["Industry"].(string); ok {
				assert.Equal(t, expectedIndustry, params.Industry)
			}
		})
	}
}
