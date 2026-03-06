// ============================================================
// FILE: internal/workers/data-access/query-elasticsearch/queries/registry.go
// ============================================================

package queries

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"camunda-workers/internal/models"

	"github.com/elastic/go-elasticsearch/v8"
)

var (
	ErrUnknownQueryType = errors.New("unknown query type")
	ErrMissingIndex     = errors.New("index name is required")
	ErrQueryExecution   = errors.New("query execution failed")
)

// QueryResult represents the result of an Elasticsearch query
type QueryResult struct {
	Data      []map[string]interface{} `json:"data"`
	TotalHits int64                    `json:"total"`
	MaxScore  float64                  `json:"max_score"`
	Took      int64                    `json:"took_ms"`
}

// QueryFunc defines the signature for ES query functions
type QueryFunc func(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error)

// Registry maps query types to their implementations
var Registry = map[models.QueryType]QueryFunc{
	// ===== HOME PAGE QUERIES =====
	models.ESQueryTypeHeroBrands:      HeroBrands,
	models.ESQueryTypePopularListings: PopularListings,

	// ===== LISTING PAGE QUERIES =====
	models.ESQueryTypeFranchiseListing:      FranchiseListing,
	models.ESQueryTypeRecommendedByIndustry: RecommendedByIndustry,

	// ===== DETAIL PAGE QUERIES =====
	models.ESQueryTypeFranchiseBySlug: FranchiseBySlug,
	models.ESQueryTypeIndustryBySlug:  IndustryBySlug,
	models.ESQueryTypeRecommended:     Recommended,
	models.ESQueryTypeMarketInsights:  MarketInsights,

	// ===== SEARCH QUERIES =====
	models.ESQueryTypeSearchWithFilters: SearchWithFilters,

	// ✅ ADD THESE 5 NEW QUERIES
	models.ESQueryTypeSearchWithAggregations: SearchWithAggregations,
	models.ESQueryTypeGetStats:               GetStatsAggregations,
	models.ESQueryTypeGetSuggestions:         GetSuggestions,
	models.ESQueryTypeGetByID:                GetByID,
	models.ESQueryTypeCountByFilter:          CountByFilter,
}

// Execute executes an Elasticsearch query by type
func Execute(ctx context.Context, esClient *elasticsearch.Client, queryType models.QueryType, params map[string]interface{}) (*QueryResult, error) {
	fn, exists := Registry[queryType]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownQueryType, queryType)
	}

	return fn(ctx, esClient, params)
}

// ============================================================
// EXISTING QUERY IMPLEMENTATIONS (Keep all existing ones)
// ============================================================

func HeroBrands(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"match_all": map[string]interface{}{},
		},
		"size": 9,
		"sort": []map[string]interface{}{
			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
			{"_score": map[string]interface{}{"order": "desc"}},
		},
		"_source": []string{
			"franchise_id",
			"name",
			"slug",
			"logo", //"logo_url",
		},
	}

	// Execute query
	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	start := time.Now()
	res, err := esClient.Search(
		esClient.Search.WithContext(ctx),
		esClient.Search.WithIndex("franchise_listings"),
		esClient.Search.WithBody(bytes.NewReader(queryJSON)),
		esClient.Search.WithTrackTotalHits(true),
	)

	if err != nil {
		return nil, fmt.Errorf("elasticsearch query failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch error: %s", res.String())
	}

	// Parse response
	var esResponse map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&esResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	hits, ok := esResponse["hits"].(map[string]interface{})
	if !ok {
		return &QueryResult{
			Data:      []map[string]interface{}{},
			TotalHits: 0,
			Took:      time.Since(start).Milliseconds(),
		}, nil
	}

	hitsList, ok := hits["hits"].([]interface{})
	if !ok {
		return &QueryResult{
			Data:      []map[string]interface{}{},
			TotalHits: 0,
			Took:      time.Since(start).Milliseconds(),
		}, nil
	}

	// Transform results to match API spec
	transformedData := make([]map[string]interface{}, 0, len(hitsList))

	for _, hit := range hitsList {
		hitMap, ok := hit.(map[string]interface{})
		if !ok {
			continue
		}

		source, ok := hitMap["_source"].(map[string]interface{})
		if !ok {
			continue
		}

		// Build brand object as per PDF spec
		brand := map[string]interface{}{
			"brandId": source["franchise_id"], // Use brandId NOT franchise_id
			"name":    getStringField(source, "name", ""),
			"slug":    getStringField(source, "slug", ""),
			"logo": map[string]interface{}{ // Create logo object
				//"url": getStringField(source, "logo_url", ""),
				"url": func() string {
					if logo, ok := source["logo"].(map[string]interface{}); ok {
						if url, ok := logo["url"].(string); ok {
							return url
						}
					}
					return ""
				}(),
				"alt": getStringField(source, "name", ""),
			},
		}

		transformedData = append(transformedData, brand)
	}

	// Get total hits
	total := int64(0)
	if totalObj, ok := hits["total"].(map[string]interface{}); ok {
		if value, ok := totalObj["value"].(float64); ok {
			total = int64(value)
		}
	}

	return &QueryResult{
		Data:      transformedData,
		TotalHits: total,
		Took:      time.Since(start).Milliseconds(),
	}, nil
}

// Helper function to safely extract string fields
func getStringField(data map[string]interface{}, key string, defaultVal string) string {
	if val, ok := data[key].(string); ok && val != "" {
		return val
	}
	return defaultVal
}

// PopularListings - Get 12 popular franchise listings
func PopularListings(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"match_all": map[string]interface{}{},
		},
		"size": 12,
		"sort": []map[string]interface{}{
			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
			{"_score": map[string]interface{}{"order": "desc"}},
		},
	}

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

// RecommendedByIndustry - Get recommended franchises by industry
func RecommendedByIndustry(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	industrySlug, ok := params["industrySlug"].(string)

	var query map[string]interface{}

	if ok && industrySlug != "" {
		// Filter by industry
		// query = map[string]interface{}{
		// 	"query": map[string]interface{}{
		// 		"match": map[string]interface{}{
		// 			"industry.slug": map[string]interface{}{
		// 				"query":    industrySlug,
		// 				"operator": "and",
		// 			},
		// 		},
		// 	},
		// ✅ NAYA — industry.name se match karo (text field hai, sahi rahega)
		// industrySlug yahan actually "Food & Beverage" jaisa string aata hai BPMN se
		query = map[string]interface{}{
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"should": []interface{}{
						map[string]interface{}{
							"term": map[string]interface{}{
								"industry.name.keyword": industrySlug,
							},
						},
						map[string]interface{}{
							"match": map[string]interface{}{
								"industry.name": industrySlug,
							},
						},
						map[string]interface{}{
							"term": map[string]interface{}{
								"industry.slug": strings.ToLower(
									strings.ReplaceAll(
										strings.ReplaceAll(industrySlug, " & ", "-"),
										" ", "-")),
							},
						},
					},
					"minimum_should_match": 1,
				},
			},
			"size": 4,
			"sort": []map[string]interface{}{
				{"rating": map[string]interface{}{"order": "desc"}},
			},
			"_source": []string{"franchise_id", "name", "slug", "industry", "logo"},
		}
	} else {
		// Get any top franchises
		query = map[string]interface{}{
			"query": map[string]interface{}{
				"match_all": map[string]interface{}{},
			},
			"size": 4,
			"sort": []map[string]interface{}{
				{"rating": map[string]interface{}{"order": "desc"}},
			},
			"_source": []string{"franchise_id", "name", "slug", "industry", "logo"},
		}
	}

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

func FranchiseBySlug(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	slug, ok := params["slug"].(string)
	if !ok || slug == "" {
		return nil, errors.New("slug parameter is required")
	}

	// ✅ FIXED: Use both keyword and text fields for maximum compatibility
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"slug.keyword": slug, // Exact match on keyword
						},
					},
					{
						"match": map[string]interface{}{
							"slug": map[string]interface{}{
								"query":    slug,
								"operator": "and",
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
		"size":    1,
		"timeout": "5s",
	}

	result, err := executeQuery(ctx, esClient, "franchise_listings", query)

	if err != nil {
		return nil, fmt.Errorf("elasticsearch query failed: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("franchise not found with slug: %s", slug)
	}

	return result, nil
}

// Add this new query function after FranchiseBySlug:
func IndustryBySlug(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	slug, ok := params["slug"].(string)
	if !ok || slug == "" {
		return nil, errors.New("slug parameter is required")
	}

	// Query for industry by slug
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"slug.keyword": slug,
						},
					},
					{
						"match": map[string]interface{}{
							"slug": map[string]interface{}{
								"query":    slug,
								"operator": "and",
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
		"size":    1,
		"timeout": "5s",
	}

	// Assuming industries are in a different index
	// You might need to adjust the index name based on your setup
	indexName := "industries" // or whatever your industry index is called

	result, err := executeQuery(ctx, esClient, indexName, query)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch query failed: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("industry not found with slug: %s", slug)
	}

	return result, nil
}

// RecommendedFranchises - Get recommended franchises (similar)
func Recommended(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	industrySlug, _ := params["industrySlug"].(string)
	currentFranchiseID, _ := params["franchiseId"].(string)

	var query map[string]interface{}

	if industrySlug != "" {
		// Same industry, exclude current
		boolQuery := map[string]interface{}{
			"must": []map[string]interface{}{
				{"match": map[string]interface{}{
					"industry.slug": map[string]interface{}{
						"query":    industrySlug,
						"operator": "and",
					},
				}},
			},
		}

		if currentFranchiseID != "" {
			boolQuery["must_not"] = []map[string]interface{}{
				{"term": map[string]interface{}{"franchise_id": currentFranchiseID}},
			}
		}

		query = map[string]interface{}{
			"query": map[string]interface{}{
				"bool": boolQuery,
			},
			"size": 4,
			"sort": []map[string]interface{}{
				{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
			},
			"_source": []string{"franchise_id", "name", "slug", "industry", "logo"}, // "logo_url"
		}
	} else {
		query = map[string]interface{}{
			"query": map[string]interface{}{
				"match_all": map[string]interface{}{},
			},
			"size": 4,
			"sort": []map[string]interface{}{
				{"rating": map[string]interface{}{"order": "desc"}},
			},
			"_source": []string{"franchise_id", "name", "slug", "industry", "logo"}, // "logo_url"
		}
	}

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

// MarketInsights - Get market insights for industry

func MarketInsights(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	// Extract industry identifier from params
	industrySlug, hasSlug := params["industrySlug"].(string)
	industryId, hasId := params["industryId"].(string)

	var query map[string]interface{}

	if hasId && industryId != "" {
		query = map[string]interface{}{
			"query": map[string]interface{}{
				"match": map[string]interface{}{
					"industry_id": industryId,
				},
			},
			"size": 1,
		}
	} else if hasSlug && industrySlug != "" {
		query = map[string]interface{}{
			"query": map[string]interface{}{
				"match": map[string]interface{}{
					"industry_slug": map[string]interface{}{
						"query":    industrySlug,
						"operator": "and",
					},
				},
			},
			"size": 1,
		}
	} else {
		// No valid industry identifier - return empty result
		return &QueryResult{
			Data:      []map[string]interface{}{},
			TotalHits: 0,
			Took:      0,
		}, nil
	}

	// Execute search against industry_insights index
	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	start := time.Now()
	res, err := esClient.Search(
		esClient.Search.WithContext(ctx),
		esClient.Search.WithIndex("industry_insights"),
		esClient.Search.WithBody(bytes.NewReader(queryJSON)),
		esClient.Search.WithTrackTotalHits(true),
	)

	if err != nil {
		return nil, fmt.Errorf("elasticsearch query failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		// ✅ IMPROVED: Log actual error instead of swallowing it
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("elasticsearch error %d: %s", res.StatusCode, string(body))
	}

	// Parse response
	var esResponse map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&esResponse); err != nil {
		return nil, fmt.Errorf("failed to decode ES response: %w", err)
	}

	// Extract hits
	hits, ok := esResponse["hits"].(map[string]interface{})
	if !ok {
		return &QueryResult{
			Data:      []map[string]interface{}{},
			TotalHits: 0,
			Took:      time.Since(start).Milliseconds(),
		}, nil
	}

	hitsList, ok := hits["hits"].([]interface{})
	if !ok || len(hitsList) == 0 {
		// No insights found - return empty (graceful degradation)
		return &QueryResult{
			Data:      []map[string]interface{}{},
			TotalHits: 0,
			Took:      time.Since(start).Milliseconds(),
		}, nil
	}

	// Extract first hit
	firstHit, ok := hitsList[0].(map[string]interface{})
	if !ok {
		return &QueryResult{
			Data:      []map[string]interface{}{},
			TotalHits: 0,
			Took:      time.Since(start).Milliseconds(),
		}, nil
	}

	source, ok := firstHit["_source"].(map[string]interface{})
	if !ok {
		return &QueryResult{
			Data:      []map[string]interface{}{},
			TotalHits: 0,
			Took:      time.Since(start).Milliseconds(),
		}, nil
	}

	// Transform to API response structure
	insights := map[string]interface{}{
		"growth_rate": map[string]interface{}{
			"title":       getStringOrDefault(source, "growth_rate_title", "Growth Rate"),
			"description": getStringOrDefault(source, "growth_rate_description", ""),
		},
		"market_trend": map[string]interface{}{
			"title":       getStringOrDefault(source, "market_trend_title", "Market Trend"),
			"description": getStringOrDefault(source, "market_trend_description", ""),
		},
	}

	return &QueryResult{
		Data:      []map[string]interface{}{insights},
		TotalHits: 1,
		Took:      time.Since(start).Milliseconds(),
	}, nil
}

// Helper function to safely extract string values with defaults
func getStringOrDefault(data map[string]interface{}, key string, defaultVal string) string {
	if val, ok := data[key].(string); ok && val != "" {
		return val
	}
	return defaultVal
}

// SearchWithFilters - Advanced search with filters
func SearchWithFilters(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	filters, _ := params["filters"].(map[string]interface{})
	page, _ := params["page"].(int)
	limit, _ := params["limit"].(int)

	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 12
	}
	if limit > 100 {
		limit = 100
	}

	from := (page - 1) * limit

	// Build query
	query := buildSearchQuery(filters)
	query["from"] = from
	query["size"] = limit
	query["sort"] = []map[string]interface{}{
		{"_score": map[string]interface{}{"order": "desc"}},
		{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
	}

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

// FranchiseListing - Paginated franchise listing (Listing Page MAIN query)
func FranchiseListing(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {

	industrySlug, _ := params["industrySlug"].(string)
	page, _ := params["page"].(int)
	limit, _ := params["limit"].(int)

	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 12
	}
	if limit > 50 {
		limit = 50
	}

	// ✅ ADD THIS: Round to nearest multiple of 3
	if limit != 9 && limit != 12 && limit != 15 && limit != 18 && limit != 21 {
		// Round to nearest multiple of 3
		limit = ((limit + 2) / 3) * 3
		if limit > 12 {
			limit = 12 // Cap at 12 for listing page
		}
		if limit < 9 {
			limit = 9 // Minimum 9
		}
	}

	from := (page - 1) * limit

	query := map[string]interface{}{
		"from": from,
		"size": limit,
		"sort": []map[string]interface{}{
			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
			{"_score": map[string]interface{}{"order": "desc"}},
		},
	}

	// if industrySlug != "" {
	// 	query["query"] = map[string]interface{}{
	// 		"term": map[string]interface{}{
	// 			"industry.slug": industrySlug,
	// 		},
	// 	}
	// }
	if industrySlug != "" {
		query["query"] = map[string]interface{}{
			"match": map[string]interface{}{
				"industry.slug": map[string]interface{}{
					"query":    industrySlug,
					"operator": "and",
				},
			},
		}
	} else {
		query["query"] = map[string]interface{}{
			"match_all": map[string]interface{}{},
		}
	}

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

// ============================================================
// NEW QUERY IMPLEMENTATIONS (Added for handler completeness)
// ============================================================

// 1. SearchWithAggregations - Search with all aggregations
func SearchWithAggregations(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	filters, _ := params["filters"].(map[string]interface{})
	page, _ := params["page"].(int)
	limit, _ := params["limit"].(int)

	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 12
	}
	if limit > 100 {
		limit = 100
	}

	from := (page - 1) * limit

	// Build query with aggregations
	query := buildSearchQuery(filters)
	query["from"] = from
	query["size"] = limit
	query["sort"] = []map[string]interface{}{
		{"_score": map[string]interface{}{"order": "desc"}},
		{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
	}

	// ✅ ADD ALL AGGREGATIONS (handler ke hisaab)
	query["aggs"] = map[string]interface{}{
		"by_industry": map[string]interface{}{
			"terms": map[string]interface{}{
				"field": "industry.slug",
				"size":  50,
			},
		},
		"by_location": map[string]interface{}{
			"terms": map[string]interface{}{
				"field": "location.keyword",
				"size":  20,
			},
		},
		"investment_stats": map[string]interface{}{
			"stats": map[string]interface{}{
				"field": "investment.min_investment",
			},
		},
		"space_stats": map[string]interface{}{
			"stats": map[string]interface{}{
				"field": "space.min_space",
			},
		},
	}

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

// 2. GetStatsAggregations - For stats page
func GetStatsAggregations(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	query := map[string]interface{}{
		"size": 0,
		"aggs": map[string]interface{}{
			"by_industry": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "industry.slug",
					"size":  20,
				},
			},
			"investment_stats": map[string]interface{}{
				"stats": map[string]interface{}{
					"field": "investment.min_investment",
				},
			},
			"space_stats": map[string]interface{}{
				"stats": map[string]interface{}{
					"field": "space.min_space",
				},
			},
		},
	}

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

// 3. GetSuggestions - Autocomplete
func GetSuggestions(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	prefix, ok := params["prefix"].(string)
	if !ok || prefix == "" {
		return &QueryResult{
			Data:      []map[string]interface{}{},
			TotalHits: 0,
			Took:      0,
		}, nil
	}

	query := map[string]interface{}{
		"suggest": map[string]interface{}{
			"brand_suggest": map[string]interface{}{
				"prefix": prefix,
				"completion": map[string]interface{}{
					"field":           "name.suggest",
					"skip_duplicates": true,
					"size":            10,
				},
			},
		},
	}

	// ✅ Special execution for suggest queries
	return executeSuggestQuery(ctx, esClient, "franchise_listings", query)
}

// 4. GetByID - Simple get by ID
func GetByID(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	franchiseID, ok := params["franchiseId"].(string)
	if !ok || franchiseID == "" {
		return nil, errors.New("franchiseId parameter is required")
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"_id": franchiseID,
			},
		},
		"size": 1,
	}

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

// 5. CountByFilter - Count documents
func CountByFilter(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	filters, _ := params["filters"].(map[string]interface{})

	query := buildSearchQuery(filters)
	query["size"] = 0

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

// ============================================================
// HELPER FUNCTIONS
// ============================================================

// buildSearchQuery builds a search query with filters
func buildSearchQuery(filters map[string]interface{}) map[string]interface{} {
	mustClauses := []map[string]interface{}{}
	filterClauses := []map[string]interface{}{}

	// Text search
	if searchQuery, ok := filters["query"].(string); ok && searchQuery != "" {
		// ADD THIS:
		cleanQuery := searchQuery
		for _, prep := range []string{" in ", " at ", " near ", " from ", " around "} {
			if idx := strings.Index(strings.ToLower(cleanQuery), prep); idx != -1 {
				cleanQuery = cleanQuery[:idx]
			}
		}
		cleanQuery = strings.TrimSpace(cleanQuery)
		if cleanQuery == "" {
			cleanQuery = searchQuery
		}
		// USE cleanQuery instead of searchQuery:
		mustClauses = append(mustClauses, map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":     cleanQuery, // "food franchise" not "food franchise in mumbai"
				"fields":    []string{"name^3", "description^2", "tags", "industry.name"},
				"type":      "best_fields",
				"fuzziness": "AUTO",
			},
		})
	}

	// Category/Industry filter
	category := ""
	if c, ok := filters["category"].(string); ok && c != "" {
		category = c
	} else if i, ok := filters["industry"].(string); ok && i != "" {
		category = i
	}
	if category != "" {
		filterClauses = append(filterClauses, map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{
						"term": map[string]interface{}{
							"industry.name.keyword": category, // "Food & Beverage" exact match
						},
					},
					map[string]interface{}{
						"match": map[string]interface{}{
							"industry.name": category, // fuzzy fallback
						},
					},
					map[string]interface{}{
						"term": map[string]interface{}{
							"industry.slug": strings.ToLower(
								strings.ReplaceAll(
									strings.ReplaceAll(category, " & ", "-"),
									" ", "-")), // "food-beverage" slug fallback
						},
					},
				},
				"minimum_should_match": 1,
			},
		})
	}
	// if category != "" {
	// 	filterClauses = append(filterClauses, map[string]interface{}{
	// 		"bool": map[string]interface{}{
	// 			"should": []map[string]interface{}{
	// 				{"term": map[string]interface{}{"industry.slug": category}},
	// 				{"match": map[string]interface{}{"industry.name": category}},
	// 			},
	// 			"minimum_should_match": 1,
	// 		},
	// 	})
	// }

	// Location filter
	if location, ok := filters["location"].(string); ok && location != "" {
		city := strings.TrimSpace(location)
		// Title case banao: "delhi" → "Delhi"
		if len(city) > 0 {
			city = strings.ToUpper(city[:1]) + strings.ToLower(city[1:])
		}
		filterClauses = append(filterClauses, map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{
						"terms": map[string]interface{}{
							"location": []string{
								city,
								strings.ToUpper(city),
								strings.ToLower(city),
								"Pan India", "Pan-India",
								"All major Indian cities",
								"North Indian Cities", "South Indian Cities",
								"East Indian Cities", "West Indian Cities",
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		})
	}

	// Investment range filter
	if minInv, minOk := filters["minInvestment"].(float64); minOk && minInv > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"investment.max_investment": map[string]interface{}{"gte": minInv / 100000},
			},
		})
	}

	if maxInv, maxOk := filters["maxInvestment"].(float64); maxOk && maxInv > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"investment.min_investment": map[string]interface{}{"lte": maxInv / 100000},
			},
		})
	}

	// Space range filter
	if minSpace, ok := filters["minSpace"].(float64); ok && minSpace > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"space.maxSpace": map[string]interface{}{"gte": minSpace},
			},
		})
	}

	if maxSpace, ok := filters["maxSpace"].(float64); ok && maxSpace > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"space.minSpace": map[string]interface{}{"lte": maxSpace},
			},
		})
	}

	// Tags filter
	// if tags, ok := filters["tags"].([]interface{}); ok && len(tags) > 0 {
	// 	tagShoulds := []interface{}{}
	// 	for _, tag := range tags {
	// 		if tagStr, ok := tag.(string); ok {
	// 			tagShoulds = append(tagShoulds, map[string]interface{}{
	// 				"match": map[string]interface{}{"industry.name": tagStr},
	// 			})
	// 			tagShoulds = append(tagShoulds, map[string]interface{}{
	// 				"term": map[string]interface{}{"tags": strings.ToLower(tagStr)},
	// 			})
	// 		}
	// 	}
	// 	if len(tagShoulds) > 0 {
	// 		filterClauses = append(filterClauses, map[string]interface{}{
	// 			"bool": map[string]interface{}{
	// 				"should":               tagShoulds,
	// 				"minimum_should_match": 1,
	// 			},
	// 		})
	// 	}
	// 	// if len(tagShoulds) > 0 {
	// 	// 	mustClauses = append(mustClauses, map[string]interface{}{
	// 	// 		"bool": map[string]interface{}{
	// 	// 			"should": tagShoulds,
	// 	// 			// minimum_should_match NAHI — optional boost
	// 	// 		},
	// 	// 	})
	// 	// }
	// }

	// Tags filter
	// ✅ NAYA — multi-word tags ko split karke match karo
	if tags, ok := filters["tags"].([]interface{}); ok && len(tags) > 0 {
		tagShoulds := []interface{}{}
		for _, tag := range tags {
			if tagStr, ok := tag.(string); ok {
				// "ice cream" → ["ice", "cream"] — har word alag term query
				words := strings.Fields(strings.ToLower(tagStr))
				for _, word := range words {
					if len(word) > 2 {
						tagShoulds = append(tagShoulds, map[string]interface{}{
							"term": map[string]interface{}{
								"tags": strings.ToUpper(word[:1]) + word[1:], // "Ice"
							},
						})
					}
				}
				// Industry name se bhi match karo
				tagShoulds = append(tagShoulds, map[string]interface{}{
					"match": map[string]interface{}{"industry.name": tagStr},
				})
				// Brand name mein bhi search karo
				tagShoulds = append(tagShoulds, map[string]interface{}{
					"match": map[string]interface{}{
						"name": map[string]interface{}{
							"query":     tagStr,
							"fuzziness": "AUTO",
						},
					},
				})
			}
		}
		if len(tagShoulds) > 0 {
			filterClauses = append(filterClauses, map[string]interface{}{
				"bool": map[string]interface{}{
					"should":               tagShoulds,
					"minimum_should_match": 1,
				},
			})
		}
	}

	// Min rating filter
	if minRating, ok := filters["minRating"].(float64); ok && minRating > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"rating": map[string]interface{}{"gte": minRating},
			},
		})
	}

	// Default match_all if no text search
	if len(mustClauses) == 0 {
		mustClauses = append(mustClauses, map[string]interface{}{
			"match_all": map[string]interface{}{},
		})
	}

	boolQuery := map[string]interface{}{
		"must": mustClauses,
	}

	if len(filterClauses) > 0 {
		boolQuery["filter"] = filterClauses
	}

	return map[string]interface{}{
		"query": map[string]interface{}{
			"bool": boolQuery,
		},
	}
}

// executeQuery executes an Elasticsearch query
func executeQuery(ctx context.Context, esClient *elasticsearch.Client, index string, query map[string]interface{}) (*QueryResult, error) {
	start := time.Now()

	// Marshal query to JSON
	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	// Execute search
	res, err := esClient.Search(
		esClient.Search.WithContext(ctx),
		esClient.Search.WithIndex(index),
		esClient.Search.WithBody(bytes.NewReader(body)),
		esClient.Search.WithTrackTotalHits(true),
	)

	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrQueryExecution, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("%w: %s", ErrQueryExecution, res.String())
	}

	// Parse response
	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract hits
	hits, ok := r["hits"].(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid response format: hits missing")
	}

	// Get total
	total := int64(0)
	if totalObj, ok := hits["total"].(map[string]interface{}); ok {
		if value, ok := totalObj["value"].(float64); ok {
			total = int64(value)
		}
	}

	// Get max score
	maxScore := 0.0
	if ms, ok := hits["max_score"].(float64); ok {
		maxScore = ms
	}

	// Extract documents
	var data []map[string]interface{}
	if hitsList, ok := hits["hits"].([]interface{}); ok {
		for _, hit := range hitsList {
			if hitMap, ok := hit.(map[string]interface{}); ok {
				if source, ok := hitMap["_source"].(map[string]interface{}); ok {
					// ✅ APPLY TRANSFORMATION
					transformed := transformFranchiseFields(source)
					data = append(data, transformed)
				}
			}
		}
	}

	return &QueryResult{
		Data:      data,
		TotalHits: total,
		MaxScore:  maxScore,
		Took:      time.Since(start).Milliseconds(),
	}, nil
}

// executeSuggestQuery - Special function for suggest queries
func executeSuggestQuery(ctx context.Context, esClient *elasticsearch.Client, index string, query map[string]interface{}) (*QueryResult, error) {
	start := time.Now()

	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal suggest query: %w", err)
	}

	res, err := esClient.Search(
		esClient.Search.WithContext(ctx),
		esClient.Search.WithIndex(index),
		esClient.Search.WithBody(bytes.NewReader(body)),
	)

	if err != nil {
		return nil, fmt.Errorf("suggest query failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("suggest query error: %s", res.String())
	}

	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("failed to decode suggest response: %w", err)
	}

	// ✅ Process suggest response differently
	var suggestions []map[string]interface{}

	if suggests, ok := r["suggest"].(map[string]interface{}); ok {
		if brandSuggests, ok := suggests["brand_suggest"].([]interface{}); ok {
			for _, suggestion := range brandSuggests {
				if sMap, ok := suggestion.(map[string]interface{}); ok {
					if options, ok := sMap["options"].([]interface{}); ok {
						for _, option := range options {
							if optMap, ok := option.(map[string]interface{}); ok {
								if text, ok := optMap["text"].(string); ok && text != "" {
									suggestions = append(suggestions, map[string]interface{}{
										"text":  text,
										"score": optMap["_score"],
									})
								}
							}
						}
					}
				}
			}
		}
	}

	return &QueryResult{
		Data:      suggestions,
		TotalHits: int64(len(suggestions)),
		Took:      time.Since(start).Milliseconds(),
	}, nil
}

// ============================================================
// FIELD TRANSFORMATION FUNCTIONS
// ============================================================

// transformFranchiseFields converts ES fields to API spec format
func transformFranchiseFields(source map[string]interface{}) map[string]interface{} {
	// Remove ES metadata
	delete(source, "_id")
	delete(source, "_score")
	delete(source, "updated_at")

	// ✅ FIX 1: Map franchise_id → id
	if franchiseID, ok := source["franchise_id"].(string); ok {
		source["id"] = franchiseID
		delete(source, "franchise_id")
	}

	// ✅ FIX 2: Map name → brand
	if name, ok := source["name"].(string); ok {
		source["brand"] = name
		delete(source, "name")
	}

	// ✅ FIX 3: Map total_outlets → no_of_outlets
	if outlets, ok := source["total_outlets"]; ok {
		source["no_of_outlets"] = outlets
		delete(source, "total_outlets")
	}

	// ✅ FIX 4: Extract industry.name → category (top-level)
	if industry, ok := source["industry"].(map[string]interface{}); ok {
		if categoryName, ok := industry["name"].(string); ok {
			source["category"] = categoryName
		}

		// Keep color at top level
		if color, ok := industry["color"].(string); ok {
			source["color"] = color
		}
	}

	// ✅ FIX 5: Transform logo_url → logo object
	if logoURL, ok := source["logo_url"].(string); ok {
		brandName := ""
		if name, ok := source["brand"].(string); ok {
			brandName = name
		}

		source["logo"] = map[string]interface{}{
			"url": logoURL,
			"alt": brandName,
		}
		delete(source, "logo_url")
	}

	// ✅ FIX 6: Ensure space has spaceUnit
	if space, ok := source["space"].(map[string]interface{}); ok {
		if _, hasUnit := space["spaceUnit"]; !hasUnit {
			space["spaceUnit"] = "sq ft"
		}
	}

	// ✅ FIX 7: Ensure investmentRange has unit
	if invRange, ok := source["investmentRange"].(map[string]interface{}); ok {
		if _, hasUnit := invRange["investmentUnit"]; !hasUnit {
			invRange["investmentUnit"] = "Lakhs"
		}
	}

	return source
}
