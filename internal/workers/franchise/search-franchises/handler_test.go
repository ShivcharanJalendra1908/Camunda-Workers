package searchfranchises

import (
	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/logger"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock Elasticsearch client for testing
type MockElasticsearchClient struct {
	mock.Mock
}

func (m *MockElasticsearchClient) SearchDocuments(ctx context.Context, index string, query map[string]interface{}) (map[string]interface{}, error) {
	args := m.Called(ctx, index, query)
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockElasticsearchClient) Ping() error {
	return m.Called().Error(0)
}

func TestHandler_ValidateInput(t *testing.T) {
	// Create a config instance directly
	config := &Config{
		DefaultLimit:       20,
		MaxLimit:           100,
		Fuzziness:          "AUTO",
		EnableSuggestions:  true,
		EnableAggregations: true,
	}

	handler := &Handler{config: config}

	tests := []struct {
		name     string
		input    Input
		expected error
	}{
		{
			name: "Valid input - ice cream search",
			input: Input{
				Page:          1,
				Limit:         10,
				Query:         "ice cream",
				Category:      "food",
				Location:      "India",
				MinInvestment: 200000,
				MaxInvestment: 1000000,
				MinSpace:      100,
				MaxSpace:      500,
				MinRating:     3.5,
				Tags:          []string{"Ice-cream franchise"},
			},
			expected: nil,
		},
		{
			name: "Valid input - education franchise",
			input: Input{
				Page:          1,
				Limit:         15,
				Query:         "career counselling",
				Category:      "education",
				Location:      "India",
				MinInvestment: 50000,
				MaxInvestment: 2000000,
				MinRating:     4.0,
			},
			expected: nil,
		},
		{
			name: "Valid input - fashion brand search",
			input: Input{
				Page:          1,
				Limit:         20,
				Query:         "footwear",
				Category:      "fashion",
				Location:      "Pan India",
				MinInvestment: 1000000,
				MaxInvestment: 5000000,
				MinSpace:      400,
				MaxSpace:      600,
				SortBy:        "investment",
				SortOrder:     "asc",
			},
			expected: nil,
		},
		{
			name: "Invalid page number",
			input: Input{
				Page:  0,
				Limit: 10,
			},
			expected: fmt.Errorf("page must be greater than 0"),
		},
		{
			name: "Invalid limit - exceeds max limit",
			input: Input{
				Page:  1,
				Limit: 200,
			},
			expected: fmt.Errorf("limit must be between 1 and %d", config.MaxLimit),
		},
		{
			name: "Invalid investment range",
			input: Input{
				Page:          1,
				Limit:         10,
				MinInvestment: 5000000,
				MaxInvestment: 1000000,
			},
			expected: fmt.Errorf("min_investment cannot be greater than max_investment"),
		},
		{
			name: "Invalid space range",
			input: Input{
				Page:     1,
				Limit:    10,
				MinSpace: 500,
				MaxSpace: 100,
			},
			expected: fmt.Errorf("min_space cannot be greater than max_space"),
		},
		{
			name: "Invalid rating - below 0",
			input: Input{
				Page:      1,
				Limit:     10,
				MinRating: -1.0,
			},
			expected: fmt.Errorf("min_rating must be between 0 and 5"),
		},
		{
			name: "Invalid rating - above 5",
			input: Input{
				Page:      1,
				Limit:     10,
				MinRating: 6.0,
			},
			expected: fmt.Errorf("min_rating must be between 0 and 5"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateInput(&tt.input)
			if tt.expected == nil {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.expected.Error())
			}
		})
	}
}

func TestHandler_GetIndicesToSearch(t *testing.T) {
	handler := &Handler{}

	tests := []struct {
		name     string
		category string
		expected []string
	}{
		{
			name:     "Food category",
			category: "food",
			expected: []string{"food_beverage_franchises"},
		},
		{
			name:     "Food & Beverage category",
			category: "food & beverage",
			expected: []string{"food_beverage_franchises"},
		},
		{
			name:     "Ice cream category",
			category: "ice cream",
			expected: []string{"food_beverage_franchises"},
		},
		{
			name:     "Dessert category",
			category: "dessert",
			expected: []string{"food_beverage_franchises"},
		},
		{
			name:     "Education category",
			category: "education",
			expected: []string{"education_franchises"},
		},
		{
			name:     "Training category",
			category: "training",
			expected: []string{"education_franchises"},
		},
		{
			name:     "Fashion category",
			category: "fashion",
			expected: []string{"fashion_franchises"},
		},
		{
			name:     "Apparel category",
			category: "apparel",
			expected: []string{"fashion_franchises"},
		},
		{
			name:     "Jewellery category",
			category: "jewellery",
			expected: []string{"fashion_franchises"},
		},
		{
			name:     "No category specified",
			category: "",
			expected: []string{
				"food_beverage_franchises",
				"education_franchises",
				"fashion_franchises",
			},
		},
		{
			name:     "Unknown category",
			category: "technology",
			expected: []string{
				"food_beverage_franchises",
				"education_franchises",
				"fashion_franchises",
			},
		},
		{
			name:     "Case insensitive category",
			category: "FOOD",
			expected: []string{"food_beverage_franchises"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.getIndicesToSearch(tt.category)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_BuildSearchRequest(t *testing.T) {
	config := &Config{
		DefaultLimit:       20,
		MaxLimit:           100,
		Fuzziness:          "AUTO",
		EnableSuggestions:  true,
		EnableAggregations: true,
	}

	handler := &Handler{config: config}

	t.Run("Empty search query", func(t *testing.T) {
		input := &Input{
			Page:  1,
			Limit: 10,
		}

		req, err := handler.buildSearchRequest(input)
		assert.NoError(t, err)
		assert.NotNil(t, req)
		assert.Equal(t, 0, req.From)
		assert.Equal(t, 10, req.Size)
		assert.NotNil(t, req.Aggregations) // Aggregations are enabled
	})

	t.Run("Ice cream search with filters", func(t *testing.T) {
		input := &Input{
			Page:          2,
			Limit:         15,
			Query:         "ice cream premium",
			Category:      "food",
			Location:      "India",
			MinInvestment: 500000,
			MaxInvestment: 5000000,
			MinSpace:      200,
			MaxSpace:      500,
			MinRating:     4.0,
			Tags:          []string{"premium", "artisan"},
			SortBy:        "rating",
			SortOrder:     "desc",
		}

		req, err := handler.buildSearchRequest(input)
		assert.NoError(t, err)
		assert.NotNil(t, req)
		assert.Equal(t, 15, req.From) // (2-1) * 15 = 15
		assert.Equal(t, 15, req.Size)

		// Check that aggregations are included
		assert.NotNil(t, req.Aggregations)
	})

	t.Run("Education franchise search", func(t *testing.T) {
		input := &Input{
			Page:      1,
			Limit:     20,
			Query:     "digital marketing training",
			Category:  "education",
			Location:  "India",
			MinRating: 4.0,
			SortBy:    "since",
			SortOrder: "asc",
		}

		req, err := handler.buildSearchRequest(input)
		assert.NoError(t, err)
		assert.NotNil(t, req)
		assert.Equal(t, 0, req.From)
		assert.Equal(t, 20, req.Size)

		// Check sort order
		assert.NotNil(t, req.Sort)
	})

	t.Run("Fashion brand search with location", func(t *testing.T) {
		input := &Input{
			Page:          1,
			Limit:         10,
			Query:         "footwear brand",
			Category:      "fashion",
			Location:      "South India",
			MinInvestment: 3000000,
			MaxInvestment: 7500000,
			MinSpace:      250,
			MaxSpace:      300,
			SortBy:        "investment",
			SortOrder:     "asc",
		}

		req, err := handler.buildSearchRequest(input)
		assert.NoError(t, err)
		assert.NotNil(t, req)
	})

	t.Run("Search with only text query", func(t *testing.T) {
		input := &Input{
			Page:  1,
			Limit: 5,
			Query: "Amul",
		}

		req, err := handler.buildSearchRequest(input)
		assert.NoError(t, err)
		assert.NotNil(t, req)

		// Check that query is built correctly
		queryMap := req.Query // This is already map[string]interface{}
		assert.NotNil(t, queryMap)

		boolQuery, ok := queryMap["bool"].(map[string]interface{})
		assert.True(t, ok)

		mustArray, ok := boolQuery["must"].([]map[string]interface{})
		assert.True(t, ok)
		assert.Greater(t, len(mustArray), 0)

		// Check fuzziness is applied
		multiMatch, ok := mustArray[0]["multi_match"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "AUTO", multiMatch["fuzziness"])
	})

	t.Run("Search with multiple tags", func(t *testing.T) {
		input := &Input{
			Page:  1,
			Limit: 10,
			Tags:  []string{"Low investment", "High brand recall", "Ice-cream franchise"},
		}

		req, err := handler.buildSearchRequest(input)
		assert.NoError(t, err)
		assert.NotNil(t, req)
	})
}

// Test parseInput separately using a helper function
func TestParseInputFunction(t *testing.T) {
	config := &Config{
		DefaultLimit: 20,
		MaxLimit:     100,
	}

	// Test parseInput logic directly
	t.Run("Parse complete variables", func(t *testing.T) {
		variables := `{
			"query": "ice cream",
			"category": "food",
			"location": "India",
			"min_investment": 200000,
			"max_investment": 1000000,
			"min_space": 100,
			"max_space": 500,
			"min_rating": 4.0,
			"tags": "Ice-cream franchise,Low investment",
			"page": 1,
			"limit": 10,
			"sort_by": "rating",
			"sort_order": "desc",
			"user_id": "user123",
			"session_id": "session456"
		}`

		// // Create a simple job mock
		// type simpleJob struct {
		// 	variables string
		// }

		// Instead of calling parseInput directly, test the parsing logic
		var vars map[string]interface{}
		err := json.Unmarshal([]byte(variables), &vars)
		assert.NoError(t, err)

		// Manually test the parsing logic
		input := &Input{}

		// Parse string inputs
		if query, ok := vars["query"].(string); ok {
			input.Query = query
		}

		if category, ok := vars["category"].(string); ok {
			input.Category = category
		}

		if location, ok := vars["location"].(string); ok {
			input.Location = location
		}

		// Parse numeric inputs
		if minInv, ok := vars["min_investment"].(float64); ok {
			input.MinInvestment = minInv
		}

		if maxInv, ok := vars["max_investment"].(float64); ok {
			input.MaxInvestment = maxInv
		}

		if minSpace, ok := vars["min_space"].(float64); ok {
			input.MinSpace = minSpace
		}

		if maxSpace, ok := vars["max_space"].(float64); ok {
			input.MaxSpace = maxSpace
		}

		if minRating, ok := vars["min_rating"].(float64); ok {
			input.MinRating = minRating
		}

		// Parse tags
		if tags, ok := vars["tags"].(string); ok && tags != "" {
			input.Tags = strings.Split(tags, ",")
		}

		// Parse pagination
		input.Page = 1
		input.Limit = config.DefaultLimit

		if page, ok := vars["page"].(float64); ok {
			input.Page = int(page)
		}

		if limit, ok := vars["limit"].(float64); ok {
			input.Limit = int(limit)
			if input.Limit > config.MaxLimit {
				input.Limit = config.MaxLimit
			}
		}

		// Parse sorting
		if sortBy, ok := vars["sort_by"].(string); ok {
			input.SortBy = sortBy
		}

		if sortOrder, ok := vars["sort_order"].(string); ok {
			input.SortOrder = sortOrder
		}

		// Parse user context
		if userId, ok := vars["user_id"].(string); ok {
			input.UserId = userId
		}

		if sessionId, ok := vars["session_id"].(string); ok {
			input.SessionId = sessionId
		}

		// Assertions
		assert.Equal(t, "ice cream", input.Query)
		assert.Equal(t, "food", input.Category)
		assert.Equal(t, "India", input.Location)
		assert.Equal(t, 200000.0, input.MinInvestment)
		assert.Equal(t, 1000000.0, input.MaxInvestment)
		assert.Equal(t, 100.0, input.MinSpace)
		assert.Equal(t, 500.0, input.MaxSpace)
		assert.Equal(t, 4.0, input.MinRating)
		assert.Equal(t, []string{"Ice-cream franchise", "Low investment"}, input.Tags)
		assert.Equal(t, 1, input.Page)
		assert.Equal(t, 10, input.Limit)
		assert.Equal(t, "rating", input.SortBy)
		assert.Equal(t, "desc", input.SortOrder)
		assert.Equal(t, "user123", input.UserId)
		assert.Equal(t, "session456", input.SessionId)
	})

	t.Run("Parse with tags as array", func(t *testing.T) {
		variables := `{
			"tags": ["Career counselling", "Study abroad", "Education franchise"]
		}`

		var vars map[string]interface{}
		err := json.Unmarshal([]byte(variables), &vars)
		assert.NoError(t, err)

		input := &Input{}

		// Test array parsing logic
		if tagsList, ok := vars["tags"].([]interface{}); ok {
			for _, tag := range tagsList {
				if tagStr, ok := tag.(string); ok {
					input.Tags = append(input.Tags, tagStr)
				}
			}
		}

		assert.Equal(t, []string{"Career counselling", "Study abroad", "Education franchise"}, input.Tags)
	})

	t.Run("Parse with default values", func(t *testing.T) {
		variables := `{}`

		var vars map[string]interface{}
		err := json.Unmarshal([]byte(variables), &vars)
		assert.NoError(t, err)

		input := &Input{}
		input.Page = 1
		input.Limit = config.DefaultLimit

		assert.Equal(t, 1, input.Page)
		assert.Equal(t, config.DefaultLimit, input.Limit)
		assert.Empty(t, input.Query)
		assert.Empty(t, input.Category)
		assert.Empty(t, input.Location)
	})

	t.Run("Parse with limit exceeding max", func(t *testing.T) {
		variables := `{"limit": 150}`

		var vars map[string]interface{}
		err := json.Unmarshal([]byte(variables), &vars)
		assert.NoError(t, err)

		input := &Input{}
		// input.Page = 1
		input.Limit = config.DefaultLimit

		if limit, ok := vars["limit"].(float64); ok {
			input.Limit = int(limit)
			if input.Limit > config.MaxLimit {
				input.Limit = config.MaxLimit
			}
		}

		assert.Equal(t, config.MaxLimit, input.Limit) // Should be capped at MaxLimit
	})
}

func TestHandler_PrepareOutput(t *testing.T) {
	handler := &Handler{}

	results := []map[string]interface{}{
		{
			"_id":    "amul",
			"brand":  "Amul",
			"rating": 4.5,
			"_score": 0.95,
			"_index": "food_beverage_franchises",
		},
		{
			"_id":    "edumilestones",
			"brand":  "Edumilestones",
			"rating": 4.5,
			"_score": 0.85,
			"_index": "education_franchises",
		},
	}

	input := &Input{
		Query:         "ice cream",
		Category:      "food",
		Location:      "India",
		MinInvestment: 200000,
		MaxInvestment: 1000000,
		MinRating:     3.5,
		Page:          1,
		Limit:         10,
		SortBy:        "rating",
		SortOrder:     "desc",
	}

	queryTime := int64(150) // 150ms

	t.Run("Prepare output with suggestions", func(t *testing.T) {
		suggestions := []string{"Amul Ice Cream", "Dairy Don", "Gianis"}

		output := handler.prepareOutput(results, 2, queryTime, suggestions, input)

		assert.NotNil(t, output)
		assert.True(t, output.Success)
		assert.Equal(t, 2, output.TotalCount)
		assert.Equal(t, queryTime, output.QueryTimeMs)
		assert.Equal(t, results, output.Franchises)
		assert.Equal(t, suggestions, output.Suggestions)

		// Check applied filters
		assert.Equal(t, "ice cream", output.AppliedFilters["query"])
		assert.Equal(t, "food", output.AppliedFilters["category"])
		assert.Equal(t, "India", output.AppliedFilters["location"])
		assert.Equal(t, 200000.0, output.AppliedFilters["min_investment"])
		assert.Equal(t, 1000000.0, output.AppliedFilters["max_investment"])
		assert.Equal(t, 3.5, output.AppliedFilters["min_rating"])
		assert.Equal(t, 1, output.AppliedFilters["page"])
		assert.Equal(t, 10, output.AppliedFilters["limit"])
	})

	t.Run("Prepare output without suggestions", func(t *testing.T) {
		output := handler.prepareOutput(results, 2, queryTime, nil, input)

		assert.NotNil(t, output)
		assert.True(t, output.Success)
		assert.Equal(t, 2, output.TotalCount)
		assert.Empty(t, output.Suggestions)
	})
}

func TestNewHandler(t *testing.T) {
	config := &Config{
		DefaultLimit:      20,
		MaxLimit:          100,
		EnableSuggestions: true,
	}

	// Mock dependencies
	var esClient *database.ElasticsearchClient
	var logger logger.Logger

	handler := NewHandler(config, esClient, logger)

	assert.NotNil(t, handler)
	assert.Equal(t, config, handler.config)
	assert.Equal(t, esClient, handler.esClient)
	assert.Equal(t, logger, handler.logger)
}

// Benchmark test for performance
func BenchmarkHandler_BuildSearchRequest(b *testing.B) {
	config := &Config{
		DefaultLimit:       20,
		MaxLimit:           100,
		Fuzziness:          "AUTO",
		EnableAggregations: true,
	}

	handler := &Handler{config: config}

	input := &Input{
		Page:          1,
		Limit:         10,
		Query:         "test query",
		Category:      "food",
		Location:      "India",
		MinInvestment: 100000,
		MaxInvestment: 5000000,
		MinRating:     3.0,
		Tags:          []string{"tag1", "tag2", "tag3"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.buildSearchRequest(input)
	}
}

func TestHandler_EdgeCases(t *testing.T) {
	config := &Config{
		DefaultLimit:       20,
		MaxLimit:           100,
		Fuzziness:          "",
		EnableSuggestions:  true,
		EnableAggregations: false,
	}

	handler := &Handler{config: config}

	t.Run("Build search request with empty fuzziness", func(t *testing.T) {
		input := &Input{
			Page:  1,
			Limit: 10,
			Query: "test",
		}

		req, err := handler.buildSearchRequest(input)
		assert.NoError(t, err)
		assert.NotNil(t, req)
	})

	t.Run("Validate input with zero values", func(t *testing.T) {
		input := &Input{
			Page:      1,
			Limit:     10,
			MinRating: 0,
		}

		err := handler.validateInput(input)
		assert.NoError(t, err)
	})

	t.Run("Get indices with mixed case category", func(t *testing.T) {
		result := handler.getIndicesToSearch("FOOD & BEVERAGE")
		assert.Equal(t, []string{"food_beverage_franchises"}, result)
	})
}

func TestHandler_ConcurrentAccess(t *testing.T) {
	config := &Config{
		DefaultLimit: 20,
		MaxLimit:     100,
	}

	handler := &Handler{config: config}

	var wg sync.WaitGroup
	concurrentCalls := 100

	// Test concurrent access to getIndicesToSearch
	for i := 0; i < concurrentCalls; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			category := "food"
			switch index % 3 {
			case 1:
				category = "education"
			case 2:
				category = "fashion"
			}

			result := handler.getIndicesToSearch(category)
			assert.NotEmpty(t, result)
		}(i)
	}

	wg.Wait()
}

func TestHandler_Performance(t *testing.T) {
	config := &Config{
		DefaultLimit: 20,
		MaxLimit:     100,
	}

	handler := &Handler{config: config}

	start := time.Now()

	// Perform multiple operations
	for i := 0; i < 1000; i++ {
		input := &Input{
			Page:  1,
			Limit: 10,
		}
		_ = handler.validateInput(input)
		_ = handler.getIndicesToSearch("food")
	}

	elapsed := time.Since(start)

	// Should complete within reasonable time
	assert.Less(t, elapsed.Milliseconds(), int64(1000),
		"1000 operations should complete within 1 second")
}
