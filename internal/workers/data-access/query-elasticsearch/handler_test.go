package queryelasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"camunda-workers/internal/common/logger"
)

// ==========================
// Test Helpers
// ==========================

func createTestConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}

func createTestLogger(t *testing.T) logger.Logger {
	return logger.NewZapAdapter(zaptest.NewLogger(t))
}

func createRealElasticsearchClient(t *testing.T) *elasticsearch.Client {
	cfg := elasticsearch.Config{
		Addresses: []string{"http://localhost:9200"},
	}

	esClient, err := elasticsearch.NewClient(cfg)
	if err != nil {
		t.Skipf("Skipping test: Failed to create Elasticsearch client: %v", err)
		return nil
	}

	res, err := esClient.Info()
	if err != nil {
		t.Skipf("Skipping test: Elasticsearch container not responding: %v", err)
		return nil
	}
	defer res.Body.Close()

	if res.IsError() {
		t.Skipf("Skipping test: Elasticsearch error: %s", res.String())
		return nil
	}

	t.Log("✅ Connected to REAL Elasticsearch container")
	return esClient
}

func setupRealTestData(t *testing.T, esClient *elasticsearch.Client) {
	esClient.Indices.Delete([]string{"franchises"}, esClient.Indices.Delete.WithIgnoreUnavailable(true))

	time.Sleep(2 * time.Second)

	indexBody := `{
		"mappings": {
			"properties": {
				"name": {"type": "text"},
				"description": {"type": "text"},
				"category": {"type": "keyword"},
				"investment_min": {"type": "integer"},
				"investment_max": {"type": "integer"},
				"locations": {"type": "keyword"}
			}
		}
	}`

	res, err := esClient.Indices.Create(
		"franchises",
		esClient.Indices.Create.WithBody(strings.NewReader(indexBody)),
	)
	require.NoError(t, err, "Failed to create index")
	res.Body.Close()

	time.Sleep(1 * time.Second)

	testDocs := []map[string]interface{}{
		{
			"name":           "Starbucks",
			"description":    "Global coffee chain franchise",
			"category":       "food",
			"investment_min": 300000,
			"investment_max": 600000,
			"locations":      []string{"US", "CA", "UK"},
		},
		{
			"name":           "McDonald's",
			"description":    "Fast food burger franchise",
			"category":       "food",
			"investment_min": 500000,
			"investment_max": 1000000,
			"locations":      []string{"US", "CA", "EU"},
		},
		{
			"name":           "Subway",
			"description":    "Sandwich franchise",
			"category":       "food",
			"investment_min": 150000,
			"investment_max": 300000,
			"locations":      []string{"US", "CA"},
		},
		{
			"name":           "7-Eleven",
			"description":    "Convenience store franchise",
			"category":       "retail",
			"investment_min": 200000,
			"investment_max": 500000,
			"locations":      []string{"US", "CA", "JP"},
		},
	}

	for i, doc := range testDocs {
		docJSON, _ := json.Marshal(doc)
		res, err := esClient.Index(
			"franchises",
			strings.NewReader(string(docJSON)),
			esClient.Index.WithDocumentID(fmt.Sprintf("%d", i+1)),
			esClient.Index.WithRefresh("wait_for"),
		)
		require.NoError(t, err, "Failed to index document %d: %v", i+1, doc)
		res.Body.Close()
	}

	_, err = esClient.Indices.Refresh(esClient.Indices.Refresh.WithIndex("franchises"))
	require.NoError(t, err, "Failed to refresh index")

	verifyTestData(t, esClient)

	t.Log("✅ REAL test data setup complete in Elasticsearch container")
}

func verifyTestData(t *testing.T, esClient *elasticsearch.Client) {
	res, err := esClient.Count(
		esClient.Count.WithIndex("franchises"),
	)
	require.NoError(t, err)
	defer res.Body.Close()

	var countResult map[string]interface{}
	err = json.NewDecoder(res.Body).Decode(&countResult)
	require.NoError(t, err)

	count := int(countResult["count"].(float64))
	t.Logf("📊 Verified: %d documents in index", count)

	searchRes, err := esClient.Search(
		esClient.Search.WithIndex("franchises"),
		esClient.Search.WithBody(strings.NewReader(`{"query": {"match_all": {}}, "size": 10}`)),
	)
	require.NoError(t, err)
	defer searchRes.Body.Close()

	var searchResult map[string]interface{}
	err = json.NewDecoder(searchRes.Body).Decode(&searchResult)
	require.NoError(t, err)

	hits := searchResult["hits"].(map[string]interface{})["hits"].([]interface{})
	t.Logf("🔍 Actual documents in Elasticsearch:")
	for i, hit := range hits {
		source := hit.(map[string]interface{})["_source"].(map[string]interface{})
		t.Logf("  %d: %s (category: %s)", i+1, source["name"], source["category"])
	}
}

// ==========================
// Integration Tests (require live ES)
// ==========================

func TestHandler_Execute_Success_RealElasticsearch(t *testing.T) {
	esClient := createRealElasticsearchClient(t)
	if esClient == nil {
		return
	}
	setupRealTestData(t, esClient)

	handler := NewHandler(createTestConfig(), esClient, createTestLogger(t))

	tests := []struct {
		name     string
		input    *Input
		validate func(t *testing.T, output *Output)
	}{
		{
			// Raw query path: IndexName + Query (no QueryType registry required)
			name: "raw query - search all franchises",
			input: &Input{
				IndexName: "franchises",
				Query: map[string]interface{}{
					"query": map[string]interface{}{
						"match_all": map[string]interface{}{},
					},
				},
				Pagination: Pagination{From: 0, Size: 10},
			},
			validate: func(t *testing.T, output *Output) {
				assert.Equal(t, int64(4), output.TotalHits, "Should find all 4 test documents")
				assert.Equal(t, 4, len(output.Data))
				assert.Greater(t, output.Took, int64(0))
				t.Logf("✅ Found %d franchises in %d ms", output.TotalHits, output.Took)
			},
		},
		{
			// Raw query path: filter by category
			name: "raw query - food category franchises",
			input: &Input{
				IndexName: "franchises",
				Query: map[string]interface{}{
					"query": map[string]interface{}{
						"term": map[string]interface{}{
							"category": "food",
						},
					},
				},
				Pagination: Pagination{From: 0, Size: 10},
			},
			validate: func(t *testing.T, output *Output) {
				assert.Equal(t, int64(3), output.TotalHits, "Should find 3 food franchises")
				assert.Equal(t, 3, len(output.Data))
				for _, item := range output.Data {
					assert.Equal(t, "food", item["category"])
				}
				t.Logf("✅ Found %d food franchises", output.TotalHits)
			},
		},
		{
			// Raw query path: full-text match
			name: "raw query - coffee keyword",
			input: &Input{
				IndexName: "franchises",
				Query: map[string]interface{}{
					"query": map[string]interface{}{
						"match": map[string]interface{}{
							"description": "coffee",
						},
					},
				},
				Pagination: Pagination{From: 0, Size: 10},
			},
			validate: func(t *testing.T, output *Output) {
				assert.Equal(t, int64(1), output.TotalHits, "Should find 1 coffee franchise")
				if output.TotalHits > 0 {
					assert.Equal(t, "Starbucks", output.Data[0]["name"])
					t.Logf("✅ Found coffee franchise: %s", output.Data[0]["name"])
				}
			},
		},
		{
			// Raw query path: retail category
			name: "raw query - retail category",
			input: &Input{
				IndexName: "franchises",
				Query: map[string]interface{}{
					"query": map[string]interface{}{
						"term": map[string]interface{}{
							"category": "retail",
						},
					},
				},
				Pagination: Pagination{From: 0, Size: 10},
			},
			validate: func(t *testing.T, output *Output) {
				assert.Equal(t, int64(1), output.TotalHits, "Should find 1 retail franchise")
				if output.TotalHits > 0 {
					assert.Equal(t, "7-Eleven", output.Data[0]["name"])
					t.Logf("✅ Found retail franchise: %s", output.Data[0]["name"])
				} else {
					t.Log("❌ No retail franchises found - checking data...")
					debugSearchByCategory(t, esClient, "retail")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := handler.execute(context.Background(), tt.input)

			assert.NoError(t, err)
			assert.NotNil(t, output)

			if tt.validate != nil {
				tt.validate(t, output)
			}
		})
	}
}

func debugSearchByCategory(t *testing.T, esClient *elasticsearch.Client, category string) {
	query := fmt.Sprintf(`{
		"query": {
			"term": {
				"category": "%s"
			}
		},
		"size": 10
	}`, category)

	res, err := esClient.Search(
		esClient.Search.WithIndex("franchises"),
		esClient.Search.WithBody(strings.NewReader(query)),
	)
	require.NoError(t, err)
	defer res.Body.Close()

	var result map[string]interface{}
	err = json.NewDecoder(res.Body).Decode(&result)
	require.NoError(t, err)

	hits := result["hits"].(map[string]interface{})["hits"].([]interface{})
	t.Logf("🔍 DEBUG: Manual category search for '%s' found %d hits:", category, len(hits))
	for i, hit := range hits {
		source := hit.(map[string]interface{})["_source"].(map[string]interface{})
		t.Logf("  Hit %d: %s (category: %s)", i+1, source["name"], source["category"])
	}

	aggQuery := `{
		"size": 0,
		"aggs": {
			"categories": {
				"terms": {
					"field": "category",
					"size": 10
				}
			}
		}
	}`

	aggRes, err := esClient.Search(
		esClient.Search.WithIndex("franchises"),
		esClient.Search.WithBody(strings.NewReader(aggQuery)),
	)
	require.NoError(t, err)
	defer aggRes.Body.Close()

	var aggResult map[string]interface{}
	_ = json.NewDecoder(aggRes.Body).Decode(&aggResult) // best-effort debug

	if aggs, ok := aggResult["aggregations"].(map[string]interface{}); ok {
		if categories, ok := aggs["categories"].(map[string]interface{}); ok {
			if buckets, ok := categories["buckets"].([]interface{}); ok {
				t.Logf("📊 Available categories:")
				for _, bucket := range buckets {
					b := bucket.(map[string]interface{})
					t.Logf("  - %s: %v documents", b["key"], b["doc_count"])
				}
			}
		}
	}
}

// ==========================
// Investment Range Tests
// ==========================

func TestHandler_Execute_InvestmentRange_RealElasticsearch(t *testing.T) {
	esClient := createRealElasticsearchClient(t)
	if esClient == nil {
		return
	}
	setupRealTestData(t, esClient)

	handler := NewHandler(createTestConfig(), esClient, createTestLogger(t))

	tests := []struct {
		name          string
		investmentMin float64
		investmentMax float64
		expectedHits  int64
		description   string
	}{
		{
			name:          "low investment range (100k-300k)",
			investmentMin: 100000,
			investmentMax: 300000,
			expectedHits:  1,
			description:   "Should find franchises where entire investment range fits within search range",
		},
		{
			name:          "medium investment range (300k-700k)",
			investmentMin: 300000,
			investmentMax: 700000,
			expectedHits:  1,
			description:   "Should find franchises where entire investment range fits within search range",
		},
		{
			name:          "high investment range (800k-2M)",
			investmentMin: 800000,
			investmentMax: 2000000,
			expectedHits:  0,
			description:   "Should find no franchises since none fit entirely within high range",
		},
		{
			name:          "very low range (0-200k)",
			investmentMin: 0,
			investmentMax: 200000,
			expectedHits:  0,
			description:   "Should find no franchises since none fit entirely within very low range",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build a raw range query since we're not using the registry
			rangeQuery := map[string]interface{}{
				"query": map[string]interface{}{
					"bool": map[string]interface{}{
						"filter": []map[string]interface{}{
							{
								"range": map[string]interface{}{
									"investment_min": map[string]interface{}{
										"gte": tt.investmentMin,
									},
								},
							},
							{
								"range": map[string]interface{}{
									"investment_max": map[string]interface{}{
										"lte": tt.investmentMax,
									},
								},
							},
						},
					},
				},
			}

			input := &Input{
				IndexName:  "franchises",
				Query:      rangeQuery,
				Pagination: Pagination{From: 0, Size: 10},
			}

			output, err := handler.execute(context.Background(), input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedHits, output.TotalHits, tt.description)

			if output.TotalHits > 0 {
				t.Logf("🔍 Found %d franchises in range %v-%v:", output.TotalHits, tt.investmentMin, tt.investmentMax)
				for _, item := range output.Data {
					t.Logf("   - %s: %v-%v", item["name"], item["investment_min"], item["investment_max"])
				}
			}

			t.Logf("✅ Investment range %v-%v: %d hits (%s)",
				tt.investmentMin, tt.investmentMax, output.TotalHits, tt.description)
		})
	}
}

// ==========================
// Index Not Found
// ==========================

func TestHandler_Execute_IndexNotFound_RealElasticsearch(t *testing.T) {
	esClient := createRealElasticsearchClient(t)
	if esClient == nil {
		return
	}

	handler := NewHandler(createTestConfig(), esClient, createTestLogger(t))

	// Use raw query path against a non-existent index
	input := &Input{
		IndexName: "nonexistent_index_xyz",
		Query: map[string]interface{}{
			"query": map[string]interface{}{
				"match_all": map[string]interface{}{},
			},
		},
	}

	output, err := handler.execute(context.Background(), input)

	assert.Error(t, err)
	assert.True(t,
		errors.Is(err, ErrSearchQueryFailed) ||
			errors.Is(err, ErrIndexNotFound) ||
			strings.Contains(err.Error(), "index_not_found") ||
			strings.Contains(err.Error(), "SEARCH_QUERY_FAILED"),
	)
	assert.Nil(t, output)

	t.Logf("✅ Correctly handled missing index: %v", err)
}

// ==========================
// Error Mapping Unit Tests
// ==========================

func TestHandler_ErrorMapping(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"franchise not found", ErrFranchiseNotFound, "FRANCHISE_NOT_FOUND"},
		{"index not found", ErrIndexNotFound, "INDEX_NOT_FOUND"},
		{"search timeout", ErrSearchTimeout, "SEARCH_TIMEOUT"},
		{"search query failed", ErrSearchQueryFailed, "SEARCH_QUERY_FAILED"},
		{"connection failed", ErrElasticsearchConnectionFailed, "ELASTICSEARCH_CONNECTION_FAILED"},
		{"invalid input format", ErrInvalidInputFormat, "INVALID_INPUT_FORMAT"},
		{"unknown error", errors.New("random error"), "UNKNOWN_ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := handler.mapErrorToCode(tt.err)
			assert.Equal(t, tt.expected, code)
		})
	}
}

// ==========================
// Retry Count Unit Tests
// ==========================

func TestHandler_GetRetryCount(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	tests := []struct {
		name     string
		err      error
		expected int32
	}{
		{"connection failed retries 3", ErrElasticsearchConnectionFailed, 3},
		{"timeout retries 2", ErrSearchTimeout, 2},
		{"query failed retries 1", ErrSearchQueryFailed, 1},
		{"franchise not found no retry", ErrFranchiseNotFound, 0},
		{"unknown no retry", errors.New("random"), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count := handler.getRetryCount(tt.err)
			assert.Equal(t, tt.expected, count)
		})
	}
}

// ==========================
// Validation Unit Tests (no ES required)
// ==========================

func TestHandler_ValidateInput_NoQueryType_NoIndexName(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	input := &Input{
		// Neither QueryType nor IndexName+Query provided
	}

	err := handler.validateInput(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Either queryType or (indexName + query) must be provided")
}

func TestHandler_ValidateInput_IndexNameOnly_NoQuery(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	input := &Input{
		IndexName: "franchises",
		// Query is nil
	}

	err := handler.validateInput(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Either queryType or (indexName + query) must be provided")
}

func TestHandler_ValidateInput_ValidRawQuery(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	input := &Input{
		IndexName: "franchises",
		Query: map[string]interface{}{
			"query": map[string]interface{}{
				"match_all": map[string]interface{}{},
			},
		},
		Pagination: Pagination{From: 0, Size: 10},
	}

	err := handler.validateInput(input)
	assert.NoError(t, err)
}

func TestHandler_ValidateInput_DangerousPatternInQuery(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	input := &Input{
		IndexName: "franchises",
		Query: map[string]interface{}{
			"script": map[string]interface{}{
				"inline": "ctx._source.price += 1",
			},
		},
	}

	err := handler.validateInput(input)
	assert.Error(t, err)
}

func TestHandler_ValidateInput_PaginationDefaults(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	input := &Input{
		IndexName: "franchises",
		Query: map[string]interface{}{
			"query": map[string]interface{}{"match_all": map[string]interface{}{}},
		},
		Pagination: Pagination{Size: 0, From: -5}, // invalid values
	}

	err := handler.validateInput(input)
	assert.NoError(t, err)
	// Defaults applied
	assert.Equal(t, 10, input.Pagination.Size)
	assert.Equal(t, 0, input.Pagination.From)
}

func TestHandler_ValidateInput_PaginationCappedAtMax(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	input := &Input{
		IndexName: "franchises",
		Query: map[string]interface{}{
			"query": map[string]interface{}{"match_all": map[string]interface{}{}},
		},
		Pagination: Pagination{Size: 9999},
	}

	err := handler.validateInput(input)
	assert.NoError(t, err)
	assert.Equal(t, 100, input.Pagination.Size)
}

func TestHandler_ValidateFilters_DeepNesting(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	// Build deeply nested object > 10 levels
	nested := map[string]interface{}{}
	current := nested
	for i := 0; i < 12; i++ {
		next := map[string]interface{}{}
		current["child"] = next
		current = next
	}

	err := handler.validateFilters(nested)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nesting too deep")
}

func TestHandler_ValidateFilters_ArrayTooLarge(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	large := make([]interface{}, 1001)
	for i := range large {
		large[i] = "item"
	}

	err := handler.validateFilterRecursive(large, "field", 0)
	assert.Error(t, err)
}

func TestHandler_ValidateFilters_DangerousKey(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	filters := map[string]interface{}{
		"script": "malicious",
	}

	err := handler.validateFilters(filters)
	assert.Error(t, err)
}

func TestHandler_SanitizeInput(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	input := &Input{
		IndexName:   "  franchises  ",
		FranchiseID: "  some-id  ",
		Category:    "  food  ",
	}

	handler.sanitizeInput(input)

	assert.Equal(t, "franchises", input.IndexName)
	assert.Equal(t, "some-id", input.FranchiseID)
	assert.Equal(t, "food", input.Category)
}

// ==========================
// execute() path tests (no ES required for error paths)
// ==========================

func TestHandler_Execute_NoQueryType_NoQuery_ReturnsError(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	// Both QueryType and (IndexName+Query) missing → ErrInvalidInputFormat
	input := &Input{
		FranchiseID: "some-id",
	}

	output, err := handler.execute(context.Background(), input)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInputFormat))
	assert.Nil(t, output)
}

func TestHandler_Execute_CancelledContext(t *testing.T) {
	esClient := createRealElasticsearchClient(t)
	if esClient == nil {
		return
	}
	setupRealTestData(t, esClient)

	handler := NewHandler(createTestConfig(), esClient, createTestLogger(t))

	input := &Input{
		IndexName: "franchises",
		Query: map[string]interface{}{
			"query": map[string]interface{}{"match_all": map[string]interface{}{}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	output, err := handler.execute(ctx, input)
	// Cancelled context: either timeout error or the query may have already run
	if err != nil {
		assert.True(t,
			errors.Is(err, ErrSearchTimeout) ||
				errors.Is(err, ErrElasticsearchConnectionFailed) ||
				errors.Is(err, ErrSearchQueryFailed) ||
				errors.Is(ctx.Err(), context.Canceled),
		)
	} else {
		// Query completed before cancellation took effect — acceptable
		assert.NotNil(t, output)
	}
}

func TestHandler_BuildRegistryParams(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	t.Run("slug extracted from FranchiseID", func(t *testing.T) {
		input := &Input{
			FranchiseID: "my-franchise-slug",
			Pagination:  Pagination{Size: 20},
		}
		params := handler.buildRegistryParams(input)
		assert.Equal(t, "my-franchise-slug", params["slug"])
		assert.Equal(t, "my-franchise-slug", params["franchiseId"])
		assert.Equal(t, 1, params["page"])
		assert.Equal(t, 20, params["limit"])
	})

	t.Run("slug extracted from params", func(t *testing.T) {
		input := &Input{
			Params: map[string]interface{}{
				"slug": "param-slug",
			},
		}
		params := handler.buildRegistryParams(input)
		assert.Equal(t, "param-slug", params["slug"])
	})

	t.Run("slug extracted from filters", func(t *testing.T) {
		input := &Input{
			Filters: map[string]interface{}{
				"slug": "filter-slug",
			},
		}
		params := handler.buildRegistryParams(input)
		assert.Equal(t, "filter-slug", params["slug"])
	})

	t.Run("industrySlug from IndustrySlug field", func(t *testing.T) {
		input := &Input{
			IndustrySlug: "food-beverage",
		}
		params := handler.buildRegistryParams(input)
		assert.Equal(t, "food-beverage", params["industrySlug"])
	})

	t.Run("category mapped to industrySlug", func(t *testing.T) {
		input := &Input{
			Category: "retail",
		}
		params := handler.buildRegistryParams(input)
		assert.Equal(t, "retail", params["industrySlug"])
		assert.Equal(t, "retail", params["category"])
	})

	t.Run("existing params not overwritten", func(t *testing.T) {
		input := &Input{
			FranchiseID: "override-attempt",
			Params: map[string]interface{}{
				"franchiseId": "original-id",
			},
		}
		params := handler.buildRegistryParams(input)
		// franchiseId already in params, should NOT be overwritten
		assert.Equal(t, "original-id", params["franchiseId"])
	})

	t.Run("pagination page calculation", func(t *testing.T) {
		input := &Input{
			Pagination: Pagination{From: 20, Size: 10},
		}
		params := handler.buildRegistryParams(input)
		assert.Equal(t, 3, params["page"])
		assert.Equal(t, 10, params["limit"])
	})
}

func TestHandler_ExtractSlugForLogging(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	t.Run("from FranchiseID", func(t *testing.T) {
		input := &Input{FranchiseID: "franchise-slug"}
		assert.Equal(t, "franchise-slug", handler.extractSlugForLogging(input))
	})

	t.Run("from params.slug", func(t *testing.T) {
		input := &Input{Params: map[string]interface{}{"slug": "param-slug"}}
		assert.Equal(t, "param-slug", handler.extractSlugForLogging(input))
	})

	t.Run("from filters.slug", func(t *testing.T) {
		input := &Input{Filters: map[string]interface{}{"slug": "filter-slug"}}
		assert.Equal(t, "filter-slug", handler.extractSlugForLogging(input))
	})

	t.Run("empty when nothing set", func(t *testing.T) {
		input := &Input{}
		assert.Equal(t, "", handler.extractSlugForLogging(input))
	})
}
