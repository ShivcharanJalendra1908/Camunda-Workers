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
			name: "search all franchises",
			input: &Input{
				IndexName:  "franchises",
				QueryType:  "franchise_index",
				Filters:    map[string]interface{}{},
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
			name: "search food category franchises",
			input: &Input{
				IndexName: "franchises",
				QueryType: "franchise_index",
				Filters: map[string]interface{}{
					"category": "food",
				},
				Pagination: Pagination{From: 0, Size: 10},
			},
			validate: func(t *testing.T, output *Output) {
				assert.Equal(t, int64(3), output.TotalHits, "Should find 3 food franchises")
				assert.Equal(t, 3, len(output.Data))

				categories := make(map[string]bool)
				for _, item := range output.Data {
					if cat, ok := item["category"].(string); ok {
						categories[cat] = true
					}
				}
				t.Logf("📋 Found categories: %v", categories)

				for _, item := range output.Data {
					assert.Equal(t, "food", item["category"])
				}
				t.Logf("✅ Found %d food franchises", output.TotalHits)
			},
		},
		{
			name: "search with coffee keyword",
			input: &Input{
				IndexName: "franchises",
				QueryType: "franchise_index",
				Filters: map[string]interface{}{
					"keywords": "coffee",
				},
				Pagination: Pagination{From: 0, Size: 10},
			},
			validate: func(t *testing.T, output *Output) {
				assert.Equal(t, int64(1), output.TotalHits, "Should find 1 coffee franchise")
				if output.TotalHits > 0 {
					assert.Equal(t, 1, len(output.Data))
					assert.Equal(t, "Starbucks", output.Data[0]["name"])
					t.Logf("✅ Found coffee franchise: %s", output.Data[0]["name"])
				}
			},
		},
		{
			name: "search retail category",
			input: &Input{
				IndexName: "franchises",
				QueryType: "franchise_index",
				Filters: map[string]interface{}{
					"category": "retail",
				},
				Pagination: Pagination{From: 0, Size: 10},
			},
			validate: func(t *testing.T, output *Output) {
				assert.Equal(t, int64(1), output.TotalHits, "Should find 1 retail franchise")
				if output.TotalHits > 0 {
					assert.Equal(t, 1, len(output.Data))
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
	err = json.NewDecoder(aggRes.Body).Decode(&aggResult)
	require.NoError(t, err)

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
		{
			name:          "minimum investment only (300k+)",
			investmentMin: 300000,
			investmentMax: 0,
			expectedHits:  4,
			description:   "Should find franchises where maximum investment >= 300k",
		},
		{
			name:          "maximum investment only (up to 500k)",
			investmentMin: 0,
			investmentMax: 500000,
			expectedHits:  4,
			description:   "Should find franchises where minimum investment <= 500k",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &Input{
				IndexName: "franchises",
				QueryType: "franchise_index",
				Filters: map[string]interface{}{
					"investmentRange": map[string]interface{}{
						"min": tt.investmentMin,
						"max": tt.investmentMax,
					},
				},
				Pagination: Pagination{From: 0, Size: 10},
			}

			output, err := handler.execute(context.Background(), input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedHits, output.TotalHits, tt.description)

			if output.TotalHits > 0 {
				t.Logf("🔍 Found %d franchises in range %v-%v:", output.TotalHits, tt.investmentMin, tt.investmentMax)
				for _, item := range output.Data {
					t.Logf("   - %s: %v-%v",
						item["name"],
						item["investment_min"],
						item["investment_max"])
				}
			} else {
				t.Logf("🔍 No franchises found in range %v-%v", tt.investmentMin, tt.investmentMax)
			}

			t.Logf("✅ Investment range %v-%v: %d hits (%s)",
				tt.investmentMin, tt.investmentMax, output.TotalHits, tt.description)
		})
	}
}

func TestHandler_Execute_IndexNotFound_RealElasticsearch(t *testing.T) {
	esClient := createRealElasticsearchClient(t)
	if esClient == nil {
		return
	}

	handler := NewHandler(createTestConfig(), esClient, createTestLogger(t))

	input := &Input{
		IndexName: "nonexistent_index",
		QueryType: "franchise_index",
		Filters:   map[string]interface{}{},
	}

	output, err := handler.execute(context.Background(), input)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrIndexNotFound) || strings.Contains(err.Error(), "index_not_found"))
	assert.Nil(t, output)

	t.Logf("✅ Correctly handled missing index: %v", err)
}

func TestHandler_ErrorMapping(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"index not found", ErrIndexNotFound, "INDEX_NOT_FOUND"},
		{"search timeout", ErrSearchTimeout, "SEARCH_TIMEOUT"},
		{"search query failed", ErrSearchQueryFailed, "SEARCH_QUERY_FAILED"},
		{"connection failed", ErrElasticsearchConnectionFailed, "ELASTICSEARCH_CONNECTION_FAILED"},
		{"unknown error", errors.New("random error"), "UNKNOWN_ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := handler.mapErrorToCode(tt.err)
			assert.Equal(t, tt.expected, code)
		})
	}
}

func TestHandler_EdgeCases(t *testing.T) {
	esClient := createRealElasticsearchClient(t)
	if esClient == nil {
		return
	}

	handler := NewHandler(createTestConfig(), esClient, createTestLogger(t))

	t.Run("nil input", func(t *testing.T) {
		output, err := handler.execute(context.Background(), nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be nil")
		assert.Nil(t, output)
	})

	t.Run("empty index name", func(t *testing.T) {
		input := &Input{
			IndexName: "",
			QueryType: "franchise_index",
			Filters:   map[string]interface{}{},
		}
		output, err := handler.execute(context.Background(), input)
		assert.Error(t, err)
		assert.Nil(t, output)
	})

	t.Run("invalid query type", func(t *testing.T) {
		input := &Input{
			IndexName: "franchises",
			QueryType: "invalid_query_type",
			Filters:   map[string]interface{}{},
		}
		output, err := handler.execute(context.Background(), input)
		assert.Error(t, err)
		assert.Nil(t, output)
	})
}

// // internal/workers/data-access/query-elasticsearch/handler_test.go
// package queryelasticsearch

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"strings"
// 	"testing"
// 	"time"

// 	"github.com/elastic/go-elasticsearch/v8"
// 	"github.com/stretchr/testify/assert"
// 	"github.com/stretchr/testify/require"
// 	"go.uber.org/zap/zaptest"
// )

// func createTestConfig() *Config {
// 	return &Config{
// 		Timeout: 30 * time.Second,
// 	}
// }

// func createRealElasticsearchClient(t *testing.T) *elasticsearch.Client {
// 	cfg := elasticsearch.Config{
// 		Addresses: []string{"http://localhost:9200"},
// 	}

// 	esClient, err := elasticsearch.NewClient(cfg)
// 	if err != nil {
// 		t.Skipf("Skipping test: Failed to create Elasticsearch client: %v", err)
// 		return nil
// 	}

// 	res, err := esClient.Info()
// 	if err != nil {
// 		t.Skipf("Skipping test: Elasticsearch container not responding: %v", err)
// 		return nil
// 	}
// 	defer res.Body.Close()

// 	if res.IsError() {
// 		t.Skipf("Skipping test: Elasticsearch error: %s", res.String())
// 		return nil
// 	}

// 	t.Log("✅ Connected to REAL Elasticsearch container")
// 	return esClient
// }

// func setupRealTestData(t *testing.T, esClient *elasticsearch.Client) {
// 	esClient.Indices.Delete([]string{"franchises"}, esClient.Indices.Delete.WithIgnoreUnavailable(true))

// 	time.Sleep(2 * time.Second)

// 	indexBody := `{
// 		"mappings": {
// 			"properties": {
// 				"name": {"type": "text"},
// 				"description": {"type": "text"},
// 				"category": {"type": "keyword"},
// 				"investment_min": {"type": "integer"},
// 				"investment_max": {"type": "integer"},
// 				"locations": {"type": "keyword"}
// 			}
// 		}
// 	}`

// 	res, err := esClient.Indices.Create(
// 		"franchises",
// 		esClient.Indices.Create.WithBody(strings.NewReader(indexBody)),
// 	)
// 	require.NoError(t, err, "Failed to create index")
// 	res.Body.Close()

// 	time.Sleep(1 * time.Second)

// 	testDocs := []map[string]interface{}{
// 		{
// 			"name":           "Starbucks",
// 			"description":    "Global coffee chain franchise",
// 			"category":       "food",
// 			"investment_min": 300000,
// 			"investment_max": 600000,
// 			"locations":      []string{"US", "CA", "UK"},
// 		},
// 		{
// 			"name":           "McDonald's",
// 			"description":    "Fast food burger franchise",
// 			"category":       "food",
// 			"investment_min": 500000,
// 			"investment_max": 1000000,
// 			"locations":      []string{"US", "CA", "EU"},
// 		},
// 		{
// 			"name":           "Subway",
// 			"description":    "Sandwich franchise",
// 			"category":       "food",
// 			"investment_min": 150000,
// 			"investment_max": 300000,
// 			"locations":      []string{"US", "CA"},
// 		},
// 		{
// 			"name":           "7-Eleven",
// 			"description":    "Convenience store franchise",
// 			"category":       "retail",
// 			"investment_min": 200000,
// 			"investment_max": 500000,
// 			"locations":      []string{"US", "CA", "JP"},
// 		},
// 	}

// 	for i, doc := range testDocs {
// 		docJSON, _ := json.Marshal(doc)
// 		res, err := esClient.Index(
// 			"franchises",
// 			strings.NewReader(string(docJSON)),
// 			esClient.Index.WithDocumentID(fmt.Sprintf("%d", i+1)),
// 			esClient.Index.WithRefresh("wait_for"),
// 		)
// 		require.NoError(t, err, "Failed to index document %d: %v", i+1, doc)
// 		res.Body.Close()
// 	}

// 	_, err = esClient.Indices.Refresh(esClient.Indices.Refresh.WithIndex("franchises"))
// 	require.NoError(t, err, "Failed to refresh index")

// 	verifyTestData(t, esClient)

// 	t.Log("✅ REAL test data setup complete in Elasticsearch container")
// }

// func verifyTestData(t *testing.T, esClient *elasticsearch.Client) {
// 	res, err := esClient.Count(
// 		esClient.Count.WithIndex("franchises"),
// 	)
// 	require.NoError(t, err)
// 	defer res.Body.Close()

// 	var countResult map[string]interface{}
// 	err = json.NewDecoder(res.Body).Decode(&countResult)
// 	require.NoError(t, err)

// 	count := int(countResult["count"].(float64))
// 	t.Logf("📊 Verified: %d documents in index", count)

// 	searchRes, err := esClient.Search(
// 		esClient.Search.WithIndex("franchises"),
// 		esClient.Search.WithBody(strings.NewReader(`{"query": {"match_all": {}}, "size": 10}`)),
// 	)
// 	require.NoError(t, err)
// 	defer searchRes.Body.Close()

// 	var searchResult map[string]interface{}
// 	err = json.NewDecoder(searchRes.Body).Decode(&searchResult)
// 	require.NoError(t, err)

// 	hits := searchResult["hits"].(map[string]interface{})["hits"].([]interface{})
// 	t.Logf("🔍 Actual documents in Elasticsearch:")
// 	for i, hit := range hits {
// 		source := hit.(map[string]interface{})["_source"].(map[string]interface{})
// 		t.Logf("  %d: %s (category: %s)", i+1, source["name"], source["category"])
// 	}
// }

// func TestHandler_Execute_Success_RealElasticsearch(t *testing.T) {
// 	esClient := createRealElasticsearchClient(t)
// 	if esClient == nil {
// 		return
// 	}
// 	setupRealTestData(t, esClient)

// 	handler := NewHandler(createTestConfig(), esClient, zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		input    *Input
// 		validate func(t *testing.T, output *Output)
// 	}{
// 		{
// 			name: "search all franchises",
// 			input: &Input{
// 				IndexName:  "franchises",
// 				QueryType:  "franchise_index",
// 				Filters:    map[string]interface{}{},
// 				Pagination: Pagination{From: 0, Size: 10},
// 			},
// 			validate: func(t *testing.T, output *Output) {
// 				assert.Equal(t, int64(4), output.TotalHits, "Should find all 4 test documents")
// 				assert.Equal(t, 4, len(output.Data))
// 				assert.Greater(t, output.Took, int64(0))
// 				t.Logf("✅ Found %d franchises in %d ms", output.TotalHits, output.Took)
// 			},
// 		},
// 		{
// 			name: "search food category franchises - FIXED",
// 			input: &Input{
// 				IndexName: "franchises",
// 				QueryType: "franchise_index",
// 				Filters: map[string]interface{}{
// 					"category": "food",
// 				},
// 				Pagination: Pagination{From: 0, Size: 10},
// 			},
// 			validate: func(t *testing.T, output *Output) {
// 				assert.Equal(t, int64(3), output.TotalHits, "Should find 3 food franchises")
// 				assert.Equal(t, 3, len(output.Data))

// 				categories := make(map[string]bool)
// 				for _, item := range output.Data {
// 					if cat, ok := item["category"].(string); ok {
// 						categories[cat] = true
// 					}
// 				}
// 				t.Logf("📋 Found categories: %v", categories)

// 				for _, item := range output.Data {
// 					assert.Equal(t, "food", item["category"])
// 				}
// 				t.Logf("✅ Found %d food franchises", output.TotalHits)
// 			},
// 		},
// 		{
// 			name: "search with coffee keyword",
// 			input: &Input{
// 				IndexName: "franchises",
// 				QueryType: "franchise_index",
// 				Filters: map[string]interface{}{
// 					"keywords": "coffee",
// 				},
// 				Pagination: Pagination{From: 0, Size: 10},
// 			},
// 			validate: func(t *testing.T, output *Output) {
// 				assert.Equal(t, int64(1), output.TotalHits, "Should find 1 coffee franchise")
// 				if output.TotalHits > 0 {
// 					assert.Equal(t, 1, len(output.Data))
// 					assert.Equal(t, "Starbucks", output.Data[0]["name"])
// 					t.Logf("✅ Found coffee franchise: %s", output.Data[0]["name"])
// 				}
// 			},
// 		},
// 		{
// 			name: "search retail category - FIXED",
// 			input: &Input{
// 				IndexName: "franchises",
// 				QueryType: "franchise_index",
// 				Filters: map[string]interface{}{
// 					"category": "retail",
// 				},
// 				Pagination: Pagination{From: 0, Size: 10},
// 			},
// 			validate: func(t *testing.T, output *Output) {
// 				assert.Equal(t, int64(1), output.TotalHits, "Should find 1 retail franchise")
// 				if output.TotalHits > 0 {
// 					assert.Equal(t, 1, len(output.Data))
// 					assert.Equal(t, "7-Eleven", output.Data[0]["name"])
// 					t.Logf("✅ Found retail franchise: %s", output.Data[0]["name"])
// 				} else {
// 					t.Log("❌ No retail franchises found - checking data...")
// 					debugSearchByCategory(t, esClient, "retail")
// 				}
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			output, err := handler.execute(context.Background(), tt.input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)

// 			if tt.validate != nil {
// 				tt.validate(t, output)
// 			}
// 		})
// 	}
// }

// func debugSearchByCategory(t *testing.T, esClient *elasticsearch.Client, category string) {
// 	query := fmt.Sprintf(`{
// 		"query": {
// 			"term": {
// 				"category": "%s"
// 			}
// 		},
// 		"size": 10
// 	}`, category)

// 	res, err := esClient.Search(
// 		esClient.Search.WithIndex("franchises"),
// 		esClient.Search.WithBody(strings.NewReader(query)),
// 	)
// 	require.NoError(t, err)
// 	defer res.Body.Close()

// 	var result map[string]interface{}
// 	err = json.NewDecoder(res.Body).Decode(&result)
// 	require.NoError(t, err)

// 	hits := result["hits"].(map[string]interface{})["hits"].([]interface{})
// 	t.Logf("🔍 DEBUG: Manual category search for '%s' found %d hits:", category, len(hits))

// 	for i, hit := range hits {
// 		source := hit.(map[string]interface{})["_source"].(map[string]interface{})
// 		t.Logf("  Hit %d: %s (category: %s)", i+1, source["name"], source["category"])
// 	}

// 	aggQuery := `{
// 		"size": 0,
// 		"aggs": {
// 			"categories": {
// 				"terms": {
// 					"field": "category",
// 					"size": 10
// 				}
// 			}
// 		}
// 	}`

// 	aggRes, err := esClient.Search(
// 		esClient.Search.WithIndex("franchises"),
// 		esClient.Search.WithBody(strings.NewReader(aggQuery)),
// 	)
// 	require.NoError(t, err)
// 	defer aggRes.Body.Close()

// 	var aggResult map[string]interface{}
// 	err = json.NewDecoder(aggRes.Body).Decode(&aggResult)
// 	require.NoError(t, err)

// 	if aggs, ok := aggResult["aggregations"].(map[string]interface{}); ok {
// 		if categories, ok := aggs["categories"].(map[string]interface{}); ok {
// 			if buckets, ok := categories["buckets"].([]interface{}); ok {
// 				t.Logf("📊 Available categories:")
// 				for _, bucket := range buckets {
// 					b := bucket.(map[string]interface{})
// 					t.Logf("  - %s: %v documents", b["key"], b["doc_count"])
// 				}
// 			}
// 		}
// 	}
// }

// func TestHandler_Execute_InvestmentRange_RealElasticsearch(t *testing.T) {
// 	esClient := createRealElasticsearchClient(t)
// 	if esClient == nil {
// 		return
// 	}
// 	setupRealTestData(t, esClient)

// 	handler := NewHandler(createTestConfig(), esClient, zaptest.NewLogger(t))

// 	tests := []struct {
// 		name          string
// 		investmentMin float64
// 		investmentMax float64
// 		expectedHits  int64
// 		description   string
// 	}{
// 		{
// 			name:          "low investment range (100k-300k)",
// 			investmentMin: 100000,
// 			investmentMax: 300000,
// 			expectedHits:  1,
// 			description:   "Should find franchises where entire investment range fits within search range",
// 		},
// 		{
// 			name:          "medium investment range (300k-700k)",
// 			investmentMin: 300000,
// 			investmentMax: 700000,
// 			expectedHits:  1,
// 			description:   "Should find franchises where entire investment range fits within search range",
// 		},
// 		{
// 			name:          "high investment range (800k-2M)",
// 			investmentMin: 800000,
// 			investmentMax: 2000000,
// 			expectedHits:  0,
// 			description:   "Should find no franchises since none fit entirely within high range",
// 		},
// 		{
// 			name:          "very low range (0-200k)",
// 			investmentMin: 0,
// 			investmentMax: 200000,
// 			expectedHits:  0,
// 			description:   "Should find no franchises since none fit entirely within very low range",
// 		},
// 		{
// 			name:          "minimum investment only (300k+)",
// 			investmentMin: 300000,
// 			investmentMax: 0,
// 			expectedHits:  4,
// 			description:   "Should find franchises where maximum investment >= 300k",
// 		},
// 		{
// 			name:          "maximum investment only (up to 500k)",
// 			investmentMin: 0,
// 			investmentMax: 500000,
// 			expectedHits:  4,
// 			description:   "Should find franchises where minimum investment <= 500k",
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			input := &Input{
// 				IndexName: "franchises",
// 				QueryType: "franchise_index",
// 				Filters: map[string]interface{}{
// 					"investmentRange": map[string]interface{}{
// 						"min": tt.investmentMin,
// 						"max": tt.investmentMax,
// 					},
// 				},
// 				Pagination: Pagination{From: 0, Size: 10},
// 			}

// 			output, err := handler.execute(context.Background(), input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.Equal(t, tt.expectedHits, output.TotalHits, tt.description)

// 			if output.TotalHits > 0 {
// 				t.Logf("🔍 Found %d franchises in range %v-%v:", output.TotalHits, tt.investmentMin, tt.investmentMax)
// 				for _, item := range output.Data {
// 					t.Logf("   - %s: %v-%v",
// 						item["name"],
// 						item["investment_min"],
// 						item["investment_max"])
// 				}
// 			} else {
// 				t.Logf("🔍 No franchises found in range %v-%v", tt.investmentMin, tt.investmentMax)
// 			}

// 			t.Logf("✅ Investment range %v-%v: %d hits (%s)",
// 				tt.investmentMin, tt.investmentMax, output.TotalHits, tt.description)
// 		})
// 	}
// }

// func TestHandler_Execute_IndexNotFound_RealElasticsearch(t *testing.T) {
// 	esClient := createRealElasticsearchClient(t)
// 	if esClient == nil {
// 		return
// 	}

// 	handler := NewHandler(createTestConfig(), esClient, zaptest.NewLogger(t))

// 	input := &Input{
// 		IndexName: "nonexistent_index",
// 		QueryType: "franchise_index",
// 		Filters:   map[string]interface{}{},
// 	}

// 	output, err := handler.execute(context.Background(), input)

// 	assert.Error(t, err)
// 	assert.True(t, errors.Is(err, ErrIndexNotFound) || strings.Contains(err.Error(), "index_not_found"))
// 	assert.Nil(t, output)

// 	t.Logf("✅ Correctly handled missing index: %v", err)
// }

// func TestHandler_ErrorMapping(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), nil, zaptest.NewLogger(t))

// 	tests := []struct {
// 		name     string
// 		err      error
// 		expected string
// 	}{
// 		{"index not found", ErrIndexNotFound, "INDEX_NOT_FOUND"},
// 		{"search timeout", ErrSearchTimeout, "SEARCH_TIMEOUT"},
// 		{"search query failed", ErrSearchQueryFailed, "SEARCH_QUERY_FAILED"},
// 		{"connection failed", ErrElasticsearchConnectionFailed, "ELASTICSEARCH_CONNECTION_FAILED"},
// 		{"unknown error", errors.New("random error"), "UNKNOWN_ERROR"},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			code := handler.mapErrorToCode(tt.err)
// 			assert.Equal(t, tt.expected, code)
// 		})
// 	}
// }

// func TestHandler_EdgeCases(t *testing.T) {
// 	esClient := createRealElasticsearchClient(t)
// 	if esClient == nil {
// 		return
// 	}

// 	handler := NewHandler(createTestConfig(), esClient, zaptest.NewLogger(t))

// 	t.Run("nil input", func(t *testing.T) {
// 		output, err := handler.execute(context.Background(), nil)
// 		assert.Error(t, err)
// 		assert.Contains(t, err.Error(), "cannot be nil")
// 		assert.Nil(t, output)
// 	})

// 	t.Run("empty index name", func(t *testing.T) {
// 		input := &Input{
// 			IndexName: "",
// 			QueryType: "franchise_index",
// 			Filters:   map[string]interface{}{},
// 		}
// 		output, err := handler.execute(context.Background(), input)
// 		assert.Error(t, err)
// 		assert.Nil(t, output)
// 	})

// 	t.Run("invalid query type", func(t *testing.T) {
// 		input := &Input{
// 			IndexName: "franchises",
// 			QueryType: "invalid_query_type",
// 			Filters:   map[string]interface{}{},
// 		}
// 		output, err := handler.execute(context.Background(), input)
// 		assert.Error(t, err)
// 		assert.Nil(t, output)
// 	})
// }
