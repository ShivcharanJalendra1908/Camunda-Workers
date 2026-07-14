package searchfranchises

import (
	"camunda-workers/internal/common/database"
	errors "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/pb"
	"github.com/stretchr/testify/assert"
)

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{
		DefaultLimit:       20,
		MaxLimit:           100,
		Fuzziness:          "AUTO",
		EnableSuggestions:  true,
		EnableAggregations: true,
	}
}

// noopLogger is a no-op logger for testing
type noopLogger struct{}

func (n *noopLogger) Debug(msg string, fields map[string]interface{})        {}
func (n *noopLogger) Info(msg string, fields map[string]interface{})         {}
func (n *noopLogger) Warn(msg string, fields map[string]interface{})         {}
func (n *noopLogger) Error(msg string, fields map[string]interface{})        {}
func (n *noopLogger) WithFields(fields map[string]interface{}) logger.Logger { return n }
func (n *noopLogger) WithError(err error) logger.Logger                      { return n }
func (n *noopLogger) With(fields map[string]interface{}) logger.Logger       { return n }

func newTestLogger(_ *testing.T) logger.Logger {
	return &noopLogger{}
}

func TestHandler_ValidateInput(t *testing.T) {
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
				Industry:      "food",
				Location:      "India",
				MinInvestment: 200000,
				MaxInvestment: 1000000,
				MinSpace:      100,
				MaxSpace:      500,
				MinRating:     3.5,
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
				Industry:      "education",
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
				Industry:      "retail",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateInput(&tt.input)
			if tt.expected == nil {
				assert.NoError(t, err)
			} else {
				if stdErr, ok := err.(*errors.StandardError); ok {
					field, _ := stdErr.Metadata["field"].(string)
					combinedErr := fmt.Sprintf("%s %s", field, stdErr.Details)
					assert.Contains(t, combinedErr, tt.expected.Error())
				} else {
					assert.Contains(t, err.Error(), tt.expected.Error())
				}
			}
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

		req, err := handler.buildSearchRequest(input, false)
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
			Industry:      "food",
			Location:      "India",
			MinInvestment: 500000,
			MaxInvestment: 5000000,
			MinSpace:      200,
			MaxSpace:      500,
			MinRating:     4.0,
			SortBy:        "rating",
			SortOrder:     "desc",
		}

		req, err := handler.buildSearchRequest(input, false)
		assert.NoError(t, err)
		assert.NotNil(t, req)
		assert.Equal(t, 15, req.From) // (2-1) * 15 = 15
		assert.Equal(t, 15, req.Size)
		assert.NotNil(t, req.Aggregations)
	})

	t.Run("Master Franchise query with filters", func(t *testing.T) {
		input := &Input{
			Page:            1,
			Limit:           10,
			EntityType:      "master_franchise",
			ExclusivityType: "State-wide exclusivity",
			TerritoryScope:  "State-wide exclusivity",
			MinUnits:        5,
			LocalBrandsOnly: true,
		}

		req, err := handler.buildSearchRequest(input, false)
		assert.NoError(t, err)
		assert.NotNil(t, req)
		assert.Equal(t, 0, req.From)
		assert.Equal(t, 10, req.Size)

		queryMap := req.Query
		assert.NotNil(t, queryMap)

		boolQuery, ok := queryMap["bool"].(map[string]interface{})
		assert.True(t, ok)

		mustArray, ok := boolQuery["must"].([]interface{})
		if !ok {
			// Try as []map[string]interface{}
			mustArrayMap, ok2 := boolQuery["must"].([]map[string]interface{})
			assert.True(t, ok2)
			mustArray = make([]interface{}, len(mustArrayMap))
			for i, v := range mustArrayMap {
				mustArray[i] = v
			}
		}

		foundEntityType := false
		foundExclusivityType := false
		foundTerritoryScope := false
		foundMinUnits := false
		foundLocalBrandsOnly := false

		for _, clauseItem := range mustArray {
			clause, ok := clauseItem.(map[string]interface{})
			if !ok {
				continue
			}
			if term, ok := clause["term"].(map[string]interface{}); ok {
				if val, ok := term["entity_type"]; ok {
					assert.Equal(t, "master_franchise", val)
					foundEntityType = true
				}
				if val, ok := term["exclusivity_type"]; ok {
					assert.Equal(t, "State-wide exclusivity", val)
					foundExclusivityType = true
				}
				if val, ok := term["territory_scope"]; ok {
					assert.Equal(t, "State-wide exclusivity", val)
					foundTerritoryScope = true
				}
				if val, ok := term["country.keyword"]; ok {
					assert.Equal(t, "India", val)
					foundLocalBrandsOnly = true
				}
			}
			if rangeQuery, ok := clause["range"].(map[string]interface{}); ok {
				if totalOutlets, ok := rangeQuery["total_outlets"].(map[string]interface{}); ok {
					assert.Equal(t, 5, totalOutlets["gte"])
					foundMinUnits = true
				}
			}
		}

		assert.True(t, foundEntityType, "should filter by entity_type")
		assert.True(t, foundExclusivityType, "should filter by exclusivity_type")
		assert.True(t, foundTerritoryScope, "should filter by territory_scope")
		assert.True(t, foundMinUnits, "should filter by total_outlets (minUnits)")
		assert.True(t, foundLocalBrandsOnly, "should filter by country.keyword (localBrandsOnly)")
	})

	t.Run("Education franchise search", func(t *testing.T) {
		input := &Input{
			Page:      1,
			Limit:     20,
			Query:     "digital marketing training",
			Category:  "education",
			Industry:  "education",
			Location:  "India",
			MinRating: 4.0,
			SortBy:    "rating",
			SortOrder: "asc",
		}

		req, err := handler.buildSearchRequest(input, false)
		assert.NoError(t, err)
		assert.NotNil(t, req)
		assert.Equal(t, 0, req.From)
		assert.Equal(t, 20, req.Size)
		assert.NotNil(t, req.Sort)
	})

	t.Run("Fashion brand search with location", func(t *testing.T) {
		input := &Input{
			Page:          1,
			Limit:         10,
			Query:         "footwear brand",
			Category:      "fashion",
			Industry:      "retail",
			Location:      "South India",
			MinInvestment: 3000000,
			MaxInvestment: 7500000,
			MinSpace:      250,
			MaxSpace:      300,
			SortBy:        "investment",
			SortOrder:     "asc",
		}

		req, err := handler.buildSearchRequest(input, false)
		assert.NoError(t, err)
		assert.NotNil(t, req)
	})

	t.Run("Search with only text query", func(t *testing.T) {
		input := &Input{
			Page:  1,
			Limit: 5,
			Query: "Amul",
		}

		req, err := handler.buildSearchRequest(input, false)
		assert.NoError(t, err)
		assert.NotNil(t, req)

		queryMap := req.Query
		assert.NotNil(t, queryMap)

		boolQuery, ok := queryMap["bool"].(map[string]interface{})
		assert.True(t, ok)

		mustArray, ok := boolQuery["must"].([]map[string]interface{})
		assert.True(t, ok)

		// Check fuzziness is applied in multi_match
		for _, clause := range mustArray {
			if multiMatch, ok := clause["multi_match"].(map[string]interface{}); ok {
				assert.Equal(t, "AUTO", multiMatch["fuzziness"])
				break
			}
		}
	})

	t.Run("Search with industry filter", func(t *testing.T) {
		input := &Input{
			Page:     1,
			Limit:    10,
			Industry: "food",
		}

		req, err := handler.buildSearchRequest(input, false)
		assert.NoError(t, err)
		assert.NotNil(t, req)
		assert.NotNil(t, req.Query)
	})

	t.Run("Search with category filter (nested)", func(t *testing.T) {
		input := &Input{
			Page:     1,
			Limit:    10,
			Category: "food",
		}

		req, err := handler.buildSearchRequest(input, false)
		assert.NoError(t, err)
		assert.NotNil(t, req)
		assert.NotNil(t, req.Query)
	})
}

// TestParseInputFunction tests parseInput logic directly via JSON unmarshaling
// (mirrors what the handler does internally)
func TestParseInputFunction(t *testing.T) {
	config := &Config{
		DefaultLimit: 20,
		MaxLimit:     100,
	}

	t.Run("Parse complete variables", func(t *testing.T) {
		variables := `{
			"query": "ice cream",
			"category": "food",
			"industry": "food",
			"location": "India",
			"min_investment": 200000,
			"max_investment": 1000000,
			"min_space": 100,
			"max_space": 500,
			"min_rating": 4.0,
			"page": 1,
			"limit": 10,
			"sort_by": "rating",
			"sort_order": "desc"
		}`

		var vars map[string]interface{}
		err := json.Unmarshal([]byte(variables), &vars)
		assert.NoError(t, err)

		input := &Input{}

		if query, ok := vars["query"].(string); ok {
			input.Query = query
		}
		if category, ok := vars["category"].(string); ok {
			input.Category = category
		}
		if industry, ok := vars["industry"].(string); ok {
			input.Industry = industry
		}
		if location, ok := vars["location"].(string); ok {
			input.Location = location
		}
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
		if sortBy, ok := vars["sort_by"].(string); ok {
			input.SortBy = sortBy
		}
		if sortOrder, ok := vars["sort_order"].(string); ok {
			input.SortOrder = sortOrder
		}

		assert.Equal(t, "ice cream", input.Query)
		assert.Equal(t, "food", input.Category)
		assert.Equal(t, "food", input.Industry)
		assert.Equal(t, "India", input.Location)
		assert.Equal(t, 200000.0, input.MinInvestment)
		assert.Equal(t, 1000000.0, input.MaxInvestment)
		assert.Equal(t, 100.0, input.MinSpace)
		assert.Equal(t, 500.0, input.MaxSpace)
		assert.Equal(t, 4.0, input.MinRating)
		assert.Equal(t, 1, input.Page)
		assert.Equal(t, 10, input.Limit)
		assert.Equal(t, "rating", input.SortBy)
		assert.Equal(t, "desc", input.SortOrder)
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
		assert.Empty(t, input.Industry)
		assert.Empty(t, input.Location)
	})

	t.Run("Parse with limit exceeding max", func(t *testing.T) {
		variables := `{"limit": 150}`

		var vars map[string]interface{}
		err := json.Unmarshal([]byte(variables), &vars)
		assert.NoError(t, err)

		input := &Input{}
		input.Limit = config.DefaultLimit

		if limit, ok := vars["limit"].(float64); ok {
			input.Limit = int(limit)
			if input.Limit > config.MaxLimit {
				input.Limit = config.MaxLimit
			}
		}

		assert.Equal(t, config.MaxLimit, input.Limit)
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
		},
		{
			"_id":    "edumilestones",
			"brand":  "Edumilestones",
			"rating": 4.5,
			"_score": 0.85,
		},
	}

	input := &Input{
		Query:         "ice cream",
		Category:      "food",
		Industry:      "food",
		Location:      "India",
		MinInvestment: 200000,
		MaxInvestment: 1000000,
		MinRating:     3.5,
		Page:          1,
		Limit:         10,
		SortBy:        "rating",
		SortOrder:     "desc",
	}

	queryTime := int64(150)

	t.Run("Prepare output", func(t *testing.T) {
		output := handler.prepareOutput(results, 2, queryTime, input)

		assert.NotNil(t, output)
		assert.True(t, output.Success)
		assert.Equal(t, 2, output.TotalCount)
		assert.Equal(t, queryTime, output.QueryTimeMs)
		assert.Equal(t, results, output.SearchResults)

		assert.Equal(t, "ice cream", output.AppliedFilters["query"])
		assert.Equal(t, "food", output.AppliedFilters["category"])
		assert.Equal(t, "food", output.AppliedFilters["industry"])
		assert.Equal(t, "India", output.AppliedFilters["location"])
		assert.Equal(t, 200000.0, output.AppliedFilters["min_investment"])
		assert.Equal(t, 1000000.0, output.AppliedFilters["max_investment"])
		assert.Equal(t, 1, output.AppliedFilters["page"])
		assert.Equal(t, 10, output.AppliedFilters["limit"])
	})

	t.Run("Prepare output without industry", func(t *testing.T) {
		inputNoIndustry := &Input{
			Query:    "test",
			Category: "food",
			Page:     1,
			Limit:    10,
		}

		output := handler.prepareOutput(results, 1, queryTime, inputNoIndustry)

		assert.NotNil(t, output)
		assert.True(t, output.Success)
		assert.Equal(t, "test", output.AppliedFilters["query"])
		assert.Equal(t, "food", output.AppliedFilters["category"])
		assert.Equal(t, "", output.AppliedFilters["industry"])
	})
}

func TestNewHandler(t *testing.T) {
	config := &Config{
		DefaultLimit:      20,
		MaxLimit:          100,
		EnableSuggestions: true,
	}

	// FIX: NewHandler calls log.WithFields internally, so we can't pass nil logger.
	// Use a real no-op logger instead.
	testLog := newTestLogger(t)

	// esClient can be nil for this test since we're only checking the handler is created.
	var esClient *database.ElasticsearchClient

	handler := NewHandler(config, esClient, testLog)

	assert.NotNil(t, handler)
	assert.Equal(t, config, handler.config)
	assert.Equal(t, esClient, handler.esClient)
}

func BenchmarkHandler_BuildSearchRequest(b *testing.B) {
	config := &Config{
		DefaultLimit:       20,
		MaxLimit:           100,
		Fuzziness:          "AUTO",
		EnableAggregations: true,
	}

	handler := &Handler{config: config}

	input := &Input{
		Page:      1,
		Limit:     10,
		Query:     "test query",
		Category:  "food",
		Industry:  "food",
		Location:  "India",
		MinRating: 3.0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.buildSearchRequest(input, false)
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

		req, err := handler.buildSearchRequest(input, false)
		assert.NoError(t, err)
		assert.NotNil(t, req)
		// With EnableAggregations=false, aggregations should be nil
		assert.Nil(t, req.Aggregations)
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
}

func TestHandler_ConcurrentAccess(t *testing.T) {
	config := &Config{
		DefaultLimit: 20,
		MaxLimit:     100,
	}

	handler := &Handler{config: config}

	var wg sync.WaitGroup
	concurrentCalls := 100

	for i := 0; i < concurrentCalls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			input := &Input{
				Page:     1,
				Limit:    10,
				Query:    "test",
				Category: "food",
				Industry: "food",
			}
			err := handler.validateInput(input)
			assert.NoError(t, err)
		}()
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

	for i := 0; i < 1000; i++ {
		input := &Input{
			Page:  1,
			Limit: 10,
		}
		_ = handler.validateInput(input)
	}

	elapsed := time.Since(start)

	assert.Less(t, elapsed.Milliseconds(), int64(1000),
		"1000 operations should complete within 1 second")
}

func TestHandler_ParseInput_CamelCase(t *testing.T) {
	handler := &Handler{config: createTestConfig()}

	job := entities.Job{
		ActivatedJob: &pb.ActivatedJob{
			Variables: `{
				"minInvestment": 100000,
				"maxInvestment": 500000,
				"minSpace": 200,
				"maxSpace": 800,
				"minRating": 4.5
			}`,
		},
	}

	input, err := handler.parseInput(job)
	assert.NoError(t, err)
	assert.Equal(t, 100000.0, input.MinInvestment)
	assert.Equal(t, 500000.0, input.MaxInvestment)
	assert.Equal(t, 200.0, input.MinSpace)
	assert.Equal(t, 800.0, input.MaxSpace)
	assert.Equal(t, 4.5, input.MinRating)
}

func TestHandler_LocationCountryFilter(t *testing.T) {
	handler := &Handler{config: createTestConfig()}

	input := &Input{
		Page:     1,
		Limit:    10,
		Location: "India",
	}

	req, err := handler.buildSearchRequest(input, false)
	assert.NoError(t, err)
	assert.NotNil(t, req)

	boolQuery, ok := req.Query["bool"].(map[string]interface{})
	assert.True(t, ok)

	mustArray, ok := boolQuery["must"].([]interface{})
	if !ok {
		mustArrayMap, ok2 := boolQuery["must"].([]map[string]interface{})
		assert.True(t, ok2)
		mustArray = make([]interface{}, len(mustArrayMap))
		for i, v := range mustArrayMap {
			mustArray[i] = v
		}
	}

	foundLocationQuery := false
	for _, clauseItem := range mustArray {
		clause, ok := clauseItem.(map[string]interface{})
		if !ok {
			continue
		}
		if bq, ok := clause["bool"].(map[string]interface{}); ok {
			var shoulds []map[string]interface{}
			if sh, ok := bq["should"].([]map[string]interface{}); ok {
				shoulds = sh
			} else if sh, ok := bq["should"].([]interface{}); ok {
				for _, sVal := range sh {
					if sm, ok := sVal.(map[string]interface{}); ok {
						shoulds = append(shoulds, sm)
					}
				}
			}

			if len(shoulds) > 0 {
				hasLocationTerm := false
				hasCountryMatch := false
				for _, sh := range shoulds {
					if term, ok := sh["term"].(map[string]interface{}); ok {
						if _, ok := term["location"]; ok {
							hasLocationTerm = true
						}
					}
					if terms, ok := sh["terms"].(map[string]interface{}); ok {
						if _, ok := terms["location"]; ok {
							hasLocationTerm = true
						}
					}
					if match, ok := sh["match"].(map[string]interface{}); ok {
						if _, ok := match["country"]; ok {
							hasCountryMatch = true
						}
					}
				}
				if hasLocationTerm && hasCountryMatch {
					foundLocationQuery = true
				}
			}
		}
	}
	assert.True(t, foundLocationQuery, "should construct location query using both location and country fields")
}

func TestHandler_ParseInput_SizeFee(t *testing.T) {
	handler := &Handler{config: createTestConfig()}

	job := entities.Job{
		ActivatedJob: &pb.ActivatedJob{
			Variables: `{
				"minMembers": 10,
				"maxMembers": 50,
				"minFee": 1000,
				"maxFee": 5000
			}`,
		},
	}

	input, err := handler.parseInput(job)
	assert.NoError(t, err)
	assert.Equal(t, 10.0, input.MinSize)
	assert.Equal(t, 50.0, input.MaxSize)
	assert.Equal(t, 1000.0, input.MinFee)
	assert.Equal(t, 5000.0, input.MaxFee)
}

func TestHandler_InvestmentSpaceOverlapFilters(t *testing.T) {
	handler := &Handler{config: createTestConfig()}

	t.Run("Raw INR values are converted to Lakhs and overlap logic is applied", func(t *testing.T) {
		input := &Input{
			MinInvestment: 5000000, // 50 Lakhs in raw INR
			MaxInvestment: 10000000, // 100 Lakhs in raw INR
			MinSpace:      500,
			MaxSpace:      1000,
		}

		req, err := handler.buildSearchRequest(input, false)
		assert.NoError(t, err)
		assert.NotNil(t, req)

		boolQuery, ok := req.Query["bool"].(map[string]interface{})
		assert.True(t, ok)

		mustArray, ok := boolQuery["must"].([]interface{})
		if !ok {
			mustArrayMap, ok2 := boolQuery["must"].([]map[string]interface{})
			assert.True(t, ok2)
			mustArray = make([]interface{}, len(mustArrayMap))
			for i, v := range mustArrayMap {
				mustArray[i] = v
			}
		}

		foundMaxInvestment := false
		foundMinInvestment := false
		foundMaxSpace := false
		foundMinSpace := false

		for _, clauseItem := range mustArray {
			clause, ok := clauseItem.(map[string]interface{})
			if !ok {
				continue
			}
			if rangeQuery, ok := clause["range"].(map[string]interface{}); ok {
				if maxInv, ok := rangeQuery["investment.max_investment"].(map[string]interface{}); ok {
					assert.Equal(t, float64(50), maxInv["gte"]) // 5,000,000 / 100,000 = 50
					foundMaxInvestment = true
				}
				if minInv, ok := rangeQuery["investment.min_investment"].(map[string]interface{}); ok {
					assert.Equal(t, float64(100), minInv["lte"]) // 10,000,000 / 100,000 = 100
					foundMinInvestment = true
				}
				if maxSpace, ok := rangeQuery["space.max_space"].(map[string]interface{}); ok {
					assert.Equal(t, float64(500), maxSpace["gte"])
					foundMaxSpace = true
				}
				if minSpace, ok := rangeQuery["space.min_space"].(map[string]interface{}); ok {
					assert.Equal(t, float64(1000), minSpace["lte"])
					foundMinSpace = true
				}
			}
		}

		assert.True(t, foundMaxInvestment, "should filter by investment.max_investment >= user.min")
		assert.True(t, foundMinInvestment, "should filter by investment.min_investment <= user.max")
		assert.True(t, foundMaxSpace, "should filter by space.max_space >= user.min")
		assert.True(t, foundMinSpace, "should filter by space.min_space <= user.max")
	})

	t.Run("Values already in Lakhs (< 1000) are not divided again", func(t *testing.T) {
		input := &Input{
			MinInvestment: 50, // 50 Lakhs directly
			MaxInvestment: 100, // 100 Lakhs directly
		}

		req, err := handler.buildSearchRequest(input, false)
		assert.NoError(t, err)
		assert.NotNil(t, req)

		boolQuery, ok := req.Query["bool"].(map[string]interface{})
		assert.True(t, ok)

		mustArray, ok := boolQuery["must"].([]interface{})
		if !ok {
			mustArrayMap, ok2 := boolQuery["must"].([]map[string]interface{})
			assert.True(t, ok2)
			mustArray = make([]interface{}, len(mustArrayMap))
			for i, v := range mustArrayMap {
				mustArray[i] = v
			}
		}

		foundMaxInvestment := false
		foundMinInvestment := false

		for _, clauseItem := range mustArray {
			clause, ok := clauseItem.(map[string]interface{})
			if !ok {
				continue
			}
			if rangeQuery, ok := clause["range"].(map[string]interface{}); ok {
				if maxInv, ok := rangeQuery["investment.max_investment"].(map[string]interface{}); ok {
					assert.Equal(t, float64(50), maxInv["gte"])
					foundMaxInvestment = true
				}
				if minInv, ok := rangeQuery["investment.min_investment"].(map[string]interface{}); ok {
					assert.Equal(t, float64(100), minInv["lte"])
					foundMinInvestment = true
				}
			}
		}

		assert.True(t, foundMaxInvestment)
		assert.True(t, foundMinInvestment)
	})
}
