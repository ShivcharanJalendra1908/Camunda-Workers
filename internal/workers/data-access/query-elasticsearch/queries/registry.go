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

	"camunda-workers/internal/common/location"
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

	// ===== ALL INDUSTRIES =====
	models.ESQueryTypeGetAllIndustries: GetAllIndustries,
	models.ESQueryTypeSearchIndustries: SearchIndustries,
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
	entityType, _ := params["entityType"].(string)
	if entityType == "" {
		entityType = "franchise"
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{"entity_type": entityType}},
					map[string]interface{}{"match_all": map[string]interface{}{}},
				},
			},
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
			"logo": func() map[string]interface{} {
				circle := ""
				square := ""
				if logo, ok := source["logo"].(map[string]interface{}); ok {
					if c, ok := logo["circle"].(string); ok {
						circle = c
					}
					if s, ok := logo["square"].(string); ok {
						square = s
					}
				}
				return map[string]interface{}{
					"circle": circle,
					"square": square,
					"alt":    getStringField(source, "name", ""),
				}
			}(),
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
	entityType, _ := params["entityType"].(string)
	if entityType == "" {
		entityType = "franchise"
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{"entity_type": entityType}},
					map[string]interface{}{"match_all": map[string]interface{}{}},
				},
			},
		},
		"size": 12,
		"sort": []map[string]interface{}{
			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
			{"_score": map[string]interface{}{"order": "desc"}},
		},
	}

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

// // RecommendedByIndustry - Get recommended franchises by industry
// func RecommendedByIndustry(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
// 	industrySlug, ok := params["industrySlug"].(string)

// 	var query map[string]interface{}

// 	if ok && industrySlug != "" {
// 		// Convert name→slug if needed: "Food & Beverage" → "food-beverage"
// 		slugValue := strings.ToLower(
// 			strings.ReplaceAll(
// 				strings.ReplaceAll(industrySlug, " & ", "-"),
// 				" ", "-"))

// 		query = map[string]interface{}{
// 			"query": map[string]interface{}{
// 				"bool": map[string]interface{}{
// 					"should": []interface{}{
// 						map[string]interface{}{
// 							"term": map[string]interface{}{
// 								"industry.slug": slugValue, // "food-beverage"
// 							},
// 						},
// 						map[string]interface{}{
// 							"term": map[string]interface{}{
// 								"industry.name.keyword": industrySlug, // "Food & Beverage"
// 							},
// 						},
// 					},
// 					"minimum_should_match": 1,
// 				},
// 			},
// 			"size": 4,
// 			"sort": []map[string]interface{}{
// 				{"rating": map[string]interface{}{"order": "desc"}},
// 			},
// 			"_source": []string{"franchise_id", "name", "slug", "industry", "logo"},
// 		}
// 	} else {
// 		query = map[string]interface{}{
// 			"query": map[string]interface{}{
// 				"match_all": map[string]interface{}{},
// 			},
// 			"size": 4,
// 			"sort": []map[string]interface{}{
// 				{"rating": map[string]interface{}{"order": "desc"}},
// 			},
// 			"_source": []string{"franchise_id", "name", "slug", "industry", "logo"},
// 		}
// 	}

// 	return executeQuery(ctx, esClient, "franchise_listings", query)
// }

// RecommendedByIndustry - Get recommended franchises by industry
func RecommendedByIndustry(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	industrySlug, _ := params["industrySlug"].(string)
	extracted, hasExtracted := params["extractedParams"].(map[string]interface{})

	entityType, _ := params["entityType"].(string)
	if entityType == "" {
		entityType = "franchise"
	}

	// Effective slug — prefer industrySlugAll (multi-slug) from extractedParams if available
	effectiveSlug := industrySlug
	if hasExtracted {
		// First try the full multi-slug (e.g. "food-beverage, fashion")
		if s, ok := extracted["industrySlugAll"].(string); ok && s != "" {
			effectiveSlug = s
		} else if s, ok := extracted["industrySlug"].(string); ok && s != "" {
			effectiveSlug = s
		}
	}

	// ============================================================
	// CASE 1: Industry context hai — same industry ke top rated
	// ============================================================
	if effectiveSlug != "" {
		industryName := ""
		if hasExtracted {
			industryName, _ = extracted["industry"].(string)
		}

		var shouldClauses []interface{}

		// Support multiple comma-separated slugs/names
		slugs := strings.Split(effectiveSlug, ",")
		for _, slug := range slugs {
			slugTrimmed := strings.TrimSpace(slug)
			if slugTrimmed == "" {
				continue
			}
			shouldClauses = append(shouldClauses,
				map[string]interface{}{"term": map[string]interface{}{"industry.slug": slugTrimmed}},
				map[string]interface{}{"prefix": map[string]interface{}{"industry.slug": slugTrimmed}},
				map[string]interface{}{"wildcard": map[string]interface{}{"industry.slug": "*" + slugTrimmed + "*"}},
			)
		}

		if industryName != "" {
			names := strings.Split(industryName, ",")
			for _, name := range names {
				nameTrimmed := strings.TrimSpace(name)
				if nameTrimmed == "" {
					continue
				}
				shouldClauses = append(shouldClauses,
					map[string]interface{}{"term": map[string]interface{}{"industry.name.keyword": nameTrimmed}},
				)
			}
		}

		query := map[string]interface{}{
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"must": []interface{}{
						map[string]interface{}{"term": map[string]interface{}{"entity_type": entityType}},
						map[string]interface{}{
							"bool": map[string]interface{}{
								"should":               shouldClauses,
								"minimum_should_match": 1,
							},
						},
					},
				},
			},
			"size": 4,
			"sort": []map[string]interface{}{
				{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
				{"total_outlets": map[string]interface{}{"order": "desc", "missing": "_last"}},
			},
			"_source": []string{"franchise_id", "name", "slug", "industry", "logo"},
		}

		return executeQuery(ctx, esClient, "franchise_listings", query)
	}

	// ============================================================
	// CASE 2: No industry — global top rated (match_all + rating)
	// Real world: "You might also like" — popular franchises
	// ============================================================
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{"entity_type": entityType}},
					map[string]interface{}{"match_all": map[string]interface{}{}},
				},
			},
		},
		"size": 4,
		// CORRECT
		"sort": []map[string]interface{}{
			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
			{"total_outlets": map[string]interface{}{"order": "desc", "missing": "_last"}},
		},
		"_source": []string{"franchise_id", "name", "slug", "industry", "logo"},
	}

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

func FranchiseBySlug(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	slug, ok := params["slug"].(string)
	if !ok || slug == "" {
		return nil, errors.New("slug parameter is required")
	}

	entityType, _ := params["entityType"].(string)
	if entityType == "" {
		entityType = "franchise"
	}

	// ✅ FIXED: Use both keyword and text fields for maximum compatibility, wrapped in entity_type
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{"entity_type": entityType}},
					map[string]interface{}{
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
				},
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

// // RecommendedFranchises - Get recommended franchises (similar)
func Recommended(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	currentFranchiseID, _ := params["franchiseId"].(string)

	entityType, _ := params["entityType"].(string)
	if entityType == "" {
		entityType = "franchise"
	}

	mustNot := []map[string]interface{}{}
	if currentFranchiseID != "" {
		mustNot = append(mustNot, map[string]interface{}{
			"term": map[string]interface{}{"franchise_id": currentFranchiseID},
		})
	}

	// Zyada results fetch karo — phir manually 1 per industry filter karenge
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{"entity_type": entityType}},
				},
				"must_not": mustNot,
			},
		},
		"size": 20, // zyada lo taaki 4 unique industries milein
		"sort": []map[string]interface{}{
			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
			{"total_outlets": map[string]interface{}{"order": "desc", "missing": "_last"}},
		},
		"_source": []string{"franchise_id", "name", "slug", "industry", "logo"},
	}

	result, err := executeQuery(ctx, esClient, "franchise_listings", query)
	if err != nil {
		return nil, err
	}

	if result == nil || len(result.Data) == 0 {
		return result, nil
	}

	// ✅ 1 per industry filter — top 4 unique industries
	seen := map[string]bool{}
	filtered := []map[string]interface{}{}

	for _, item := range result.Data {
		industryObj, ok := item["industry"].(map[string]interface{})
		if !ok {
			continue
		}
		slug, _ := industryObj["slug"].(string)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		filtered = append(filtered, item)

		if len(filtered) == 4 {
			break
		}
	}

	result.Data = filtered
	return result, nil
}

// func Recommended(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
//     industrySlug, _ := params["industrySlug"].(string)
//     currentFranchiseID, _ := params["franchiseId"].(string)

//     // Exclude current franchise — har case mein
//     mustNot := []map[string]interface{}{}
//     if currentFranchiseID != "" {
//         mustNot = append(mustNot, map[string]interface{}{
//             "term": map[string]interface{}{"franchise_id": currentFranchiseID},
//         })
//     }

//     // CASE 1: Same industry ke franchises dhundo
//     if industrySlug != "" {
//         query := map[string]interface{}{
//             "query": map[string]interface{}{
//                 "bool": map[string]interface{}{
//                     "must": []map[string]interface{}{
//                         {"term": map[string]interface{}{"industry.slug": industrySlug}}, // ← term, match nahi
//                     },
//                     "must_not": mustNot,
//                 },
//             },
//             "size": 4,
//             "sort": []map[string]interface{}{
//                 {"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
//                 {"total_outlets": map[string]interface{}{"order": "desc", "missing": "_last"}},
//             },
//             "_source": []string{"franchise_id", "name", "slug", "industry", "logo"},
//         }

//         result, err := executeQuery(ctx, esClient, "franchise_listings", query)
//         if err != nil {
//             return nil, err
//         }

//         // Same industry mein results mile — return karo
//         if result != nil && len(result.Data) > 0 {
//             return result, nil
//         }

//         // ← FALLBACK: Same industry mein koi nahi, global top-rated lo
//     }

//     // CASE 2: Global fallback — top outlets, current exclude
//     fallbackQuery := map[string]interface{}{
//         "query": map[string]interface{}{
//             "bool": map[string]interface{}{
//                 "must_not": mustNot,
//             },
//         },
//         "size": 4,
//         "sort": []map[string]interface{}{
//             {"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
//             {"total_outlets": map[string]interface{}{"order": "desc", "missing": "_last"}},
//         },
//         "_source": []string{"franchise_id", "name", "slug", "industry", "logo"},
//     }

//     return executeQuery(ctx, esClient, "franchise_listings", fallbackQuery)
// }

// MarketInsights - Get market insights for industry

func MarketInsights(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	// Extract industry identifier from params
	industrySlug, hasSlug := params["industrySlug"].(string)
	industryId, hasId := params["industryId"].(string)

	entityType, _ := params["entityType"].(string)
	if entityType == "" {
		entityType = "franchise"
	}

	var query map[string]interface{}

	if hasId && industryId != "" {
		query = map[string]interface{}{
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"must": []interface{}{
						map[string]interface{}{"term": map[string]interface{}{"entity_type": entityType}},
						map[string]interface{}{"match": map[string]interface{}{"industry_id": industryId}},
					},
				},
			},
			"size": 1,
		}
	} else if hasSlug && industrySlug != "" && !strings.Contains(industrySlug, ",") {
		var shouldClauses []interface{}
		parts := strings.Split(industrySlug, ",")
		for _, part := range parts {
			partTrimmed := strings.TrimSpace(part)
			if partTrimmed == "" {
				continue
			}
			slugPrefix := strings.Split(partTrimmed, "-")[0]
			shouldClauses = append(shouldClauses,
				map[string]interface{}{
					"term": map[string]interface{}{
						"industry_slug": partTrimmed,
					},
				},
				map[string]interface{}{
					"prefix": map[string]interface{}{
						"industry_slug": slugPrefix,
					},
				},
			)
		}

		query = map[string]interface{}{
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"must": []interface{}{
						map[string]interface{}{"term": map[string]interface{}{"entity_type": entityType}},
						map[string]interface{}{
							"bool": map[string]interface{}{
								"should":               shouldClauses,
								"minimum_should_match": 1,
							},
						},
					},
				},
			},
			"size": 1,
		}
	} else {
		// No valid industry identifier - generic fallback with intent detection
		searchQuery, _ := params["searchQuery"].(string)
		intentTag := detectESIntentTag(searchQuery)
		// query = map[string]interface{}{
		// 	"query": map[string]interface{}{
		// 		"bool": map[string]interface{}{
		// 			"must": []interface{}{
		// 				map[string]interface{}{
		// 					"term": map[string]interface{}{
		// 						"industry_id": "00000000-0000-0000-0000-000000000000",
		// 					},
		// 				},
		// 				map[string]interface{}{
		// 					"term": map[string]interface{}{
		// 						"intent_tag": intentTag,
		// 					},
		// 				},
		// 			},
		// 		},
		// 	},
		// 	"size": 1,
		// }
		query = map[string]interface{}{
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"must": []interface{}{
						map[string]interface{}{"term": map[string]interface{}{"entity_type": entityType}},
						map[string]interface{}{"term": map[string]interface{}{"industry_id": "00000000-0000-0000-0000-000000000000"}},
					},
					"should": []interface{}{
						map[string]interface{}{"term": map[string]interface{}{"intent_tag": intentTag}}, // preferred (boosted)
						map[string]interface{}{"term": map[string]interface{}{"intent_tag": "general"}}, // guaranteed fallback
					},
					"minimum_should_match": 1,
				},
			},
			"sort": []interface{}{
				map[string]interface{}{"_score": map[string]interface{}{"order": "desc"}},
			},
			"size": 1,
		}
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

// detectESIntentTag - detects intent from search query for generic fallback
func detectESIntentTag(searchQuery string) string {
	q := strings.ToLower(searchQuery)

	lowInvestmentKeywords := []string{"cheap", "low investment", "budget", "affordable", "under 5", "under 10", "5 lakh", "10 lakh", "less investment", "minimum investment", "small investment"}
	for _, kw := range lowInvestmentKeywords {
		if strings.Contains(q, kw) {
			return "low-investment"
		}
	}

	locationKeywords := []string{"delhi", "mumbai", "bangalore", "bengaluru", "hyderabad", "chennai", "pune", "kolkata", "jaipur", "lucknow", "indore", "city", "location", "near me", "tier 2", "tier 3", "local"}
	for _, kw := range locationKeywords {
		if strings.Contains(q, kw) {
			return "location-based"
		}
	}

	roiKeywords := []string{"roi", "profit", "return", "earning", "income", "revenue", "margin", "payback", "profitable"}
	for _, kw := range roiKeywords {
		if strings.Contains(q, kw) {
			return "roi-focused"
		}
	}

	return "general"
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

	page := 1
	if v, ok := params["page"].(float64); ok && v > 0 {
		page = int(v)
	} else if v, ok := params["page"].(int); ok && v > 0 {
		page = v
	}

	limit := 0
	if v, ok := params["limit"].(float64); ok && v > 0 {
		limit = int(v)
	} else if v, ok := params["limit"].(int); ok && v > 0 {
		limit = v
	}

	from := 0
	if v, ok := params["offset"].(float64); ok && v >= 0 {
		from = int(v)
	} else if v, ok := params["offset"].(int); ok && v >= 0 {
		from = v
	} else if limit > 0 {
		from = (page - 1) * limit
	}

	entityType, _ := params["entityType"].(string)
	if entityType == "" {
		entityType = "franchise"
	}

	// Build query
	query := buildSearchQuery(filters)

	// Wrap query with entity_type filter
	originalQuery := query["query"]
	query["query"] = map[string]interface{}{
		"bool": map[string]interface{}{
			"must": []interface{}{
				map[string]interface{}{"term": map[string]interface{}{"entity_type": entityType}},
				originalQuery,
			},
		},
	}

	query["from"] = from
	query["size"] = limit

	sortBy, _ := params["sortBy"].(string)
	if sortBy == "" {
		sortBy, _ = params["sort_by"].(string)
	}
	if sortBy == "" && filters != nil {
		sortBy, _ = filters["sortBy"].(string)
		if sortBy == "" {
			sortBy, _ = filters["sort_by"].(string)
		}
	}

	if sortBy != "" {
		switch sortBy {
		case "alphabetical":
			query["sort"] = []map[string]interface{}{
				{"name.keyword": map[string]interface{}{"order": "asc"}},
			}
		case "newest":
			query["sort"] = []map[string]interface{}{
				{"approved_at": map[string]interface{}{"order": "desc", "missing": "_last"}},
			}
		case "popularity":
			query["sort"] = []map[string]interface{}{
				{"member_count": map[string]interface{}{"order": "desc", "missing": "_last"}},
			}
		default:
			query["sort"] = []map[string]interface{}{
				{"_score": map[string]interface{}{"order": "desc"}},
				{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
			}
		}
	} else {
		query["sort"] = []map[string]interface{}{
			{"_score": map[string]interface{}{"order": "desc"}},
			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
		}
	}

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

// FranchiseListing - Paginated franchise listing (Listing Page MAIN query)
// func FranchiseListing(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {

// 	industrySlug, _ := params["industrySlug"].(string)
// 	page := 1
// 	if v, ok := params["page"].(float64); ok && v > 0 {
// 		page = int(v)
// 	} else if v, ok := params["page"].(int); ok && v > 0 {
// 		page = v
// 	}

// 	pageSize := 0
// 	if v, ok := params["pageSize"].(float64); ok && v > 0 {
// 		pageSize = int(v)
// 	} else if v, ok := params["pageSize"].(int); ok && v > 0 {
// 		pageSize = v
// 	} else if v, ok := params["limit"].(float64); ok && v > 0 {
// 		pageSize = int(v)
// 	} else if v, ok := params["limit"].(int); ok && v > 0 {
// 		pageSize = v
// 	}

// 	from := 0
// 	if v, ok := params["offset"].(float64); ok && v >= 0 {
// 		from = int(v)
// 	} else if v, ok := params["offset"].(int); ok && v >= 0 {
// 		from = v
// 	} else if pageSize > 0 {
// 		from = (page - 1) * pageSize
// 	}

// 	query := map[string]interface{}{
// 		"from":             from,
// 		"size":             pageSize, // ← CHANGE 1: limit → pageSize
// 		"track_total_hits": true,     // ← CHANGE 2: naya add karo
// 		"sort": []map[string]interface{}{
// 			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
// 			{"_score": map[string]interface{}{"order": "desc"}},
// 		},
// 	}

// 	if industrySlug != "" {
// 		query["query"] = map[string]interface{}{
// 			"bool": map[string]interface{}{
// 				"should": []interface{}{
// 					map[string]interface{}{
// 						"term": map[string]interface{}{
// 							"industry.slug": industrySlug,
// 						},
// 					},
// 					map[string]interface{}{
// 						"prefix": map[string]interface{}{
// 							"industry.slug": industrySlug,
// 						},
// 					},
// 					map[string]interface{}{
// 						"wildcard": map[string]interface{}{
// 							"industry.slug": map[string]interface{}{
// 								"value": "*" + industrySlug + "*",
// 							},
// 						},
// 					},
// 					map[string]interface{}{
// 						"match": map[string]interface{}{
// 							"industry.name": map[string]interface{}{
// 								"query":     industrySlug,
// 								"fuzziness": "AUTO",
// 							},
// 						},
// 					},
// 				},
// 				"minimum_should_match": 1,
// 			},
// 		}
// 	} else {
// 		query["query"] = map[string]interface{}{
// 			"match_all": map[string]interface{}{},
// 		}
// 	}

//		return executeQuery(ctx, esClient, "franchise_listings", query)
//	}
func FranchiseListing(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {

	industrySlug, _ := params["industrySlug"].(string)
	categorySlug, _ := params["categorySlug"].(string) // ← NEW
	subCategorySlug, _ := params["subCategorySlug"].(string)

	page := 1
	if v, ok := params["page"].(float64); ok && v > 0 {
		page = int(v)
	} else if v, ok := params["page"].(int); ok && v > 0 {
		page = v
	}

	pageSize := 0
	if v, ok := params["pageSize"].(float64); ok && v > 0 {
		pageSize = int(v)
	} else if v, ok := params["pageSize"].(int); ok && v > 0 {
		pageSize = v
	} else if v, ok := params["limit"].(float64); ok && v > 0 {
		pageSize = int(v)
	} else if v, ok := params["limit"].(int); ok && v > 0 {
		pageSize = v
	}

	from := 0
	if v, ok := params["offset"].(float64); ok && v >= 0 {
		from = int(v)
	} else if v, ok := params["offset"].(int); ok && v >= 0 {
		from = v
	} else if pageSize > 0 {
		from = (page - 1) * pageSize
	}

	query := map[string]interface{}{
		"from":             from,
		"size":             pageSize,
		"track_total_hits": true,
		"sort": []map[string]interface{}{
			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
			{"_score": map[string]interface{}{"order": "desc"}},
		},
	}

	entityType, _ := params["entityType"].(string)
	if entityType == "" {
		entityType = "franchise"
	}

	var filterClause interface{}

	if subCategorySlug != "" {
		// ✅ CASE 1: SubCategory filter — nested query
		filterClause = map[string]interface{}{
			"nested": map[string]interface{}{
				"path": "sub_categories",
				"query": map[string]interface{}{
					"term": map[string]interface{}{
						"sub_categories.slug": subCategorySlug,
					},
				},
			},
		}
	} else if categorySlug != "" {
		// ✅ CASE 2: Category filter — nested query
		filterClause = map[string]interface{}{
			"nested": map[string]interface{}{
				"path": "categories",
				"query": map[string]interface{}{
					"term": map[string]interface{}{
						"categories.slug": categorySlug,
					},
				},
			},
		}
	} else if industrySlug != "" {
		// ✅ CASE 3: Industry filter — support comma-separated multiple industries
		var shouldClauses []interface{}
		parts := strings.Split(industrySlug, ",")
		for _, part := range parts {
			partTrimmed := strings.TrimSpace(part)
			if partTrimmed == "" {
				continue
			}
			shouldClauses = append(shouldClauses,
				map[string]interface{}{"term": map[string]interface{}{"industry.slug": partTrimmed}},
				map[string]interface{}{"prefix": map[string]interface{}{"industry.slug": partTrimmed}},
				map[string]interface{}{"wildcard": map[string]interface{}{"industry.slug": map[string]interface{}{"value": "*" + partTrimmed + "*"}}},
				map[string]interface{}{"match": map[string]interface{}{"industry.name": map[string]interface{}{"query": partTrimmed, "fuzziness": "AUTO"}}},
			)
		}
		filterClause = map[string]interface{}{
			"bool": map[string]interface{}{
				"should":               shouldClauses,
				"minimum_should_match": 1,
			},
		}
	} else {
		// ✅ CASE 4: No filter
		filterClause = map[string]interface{}{"match_all": map[string]interface{}{}}
	}

	query["query"] = map[string]interface{}{
		"bool": map[string]interface{}{
			"must": []interface{}{
				map[string]interface{}{
					"term": map[string]interface{}{
						"entity_type": entityType,
					},
				},
				filterClause,
			},
		},
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

	entityType, _ := params["entityType"].(string)
	if entityType == "" {
		entityType = "franchise"
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{"entity_type": entityType}},
					map[string]interface{}{"term": map[string]interface{}{"_id": franchiseID}},
				},
			},
		},
		"size": 1,
	}

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

// 5. CountByFilter - Count documents
func CountByFilter(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	filters, _ := params["filters"].(map[string]interface{})

	entityType, _ := params["entityType"].(string)
	if entityType == "" {
		entityType = "franchise"
	}

	query := buildSearchQuery(filters)
	
	// Wrap with entity_type
	originalQuery := query["query"]
	query["query"] = map[string]interface{}{
		"bool": map[string]interface{}{
			"must": []interface{}{
				map[string]interface{}{"term": map[string]interface{}{"entity_type": entityType}},
				originalQuery,
			},
		},
	}
	
	query["size"] = 0

	return executeQuery(ctx, esClient, "franchise_listings", query)
}

// ============================================================
// GET ALL INDUSTRIES (with search also)
// ============================================================

func GetAllIndustries(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"match_all": map[string]interface{}{},
		},
		"size": 50,
		"sort": []map[string]interface{}{
			{"display_order": map[string]interface{}{"order": "asc"}},
		},
	}
	return executeQuery(ctx, esClient, "franchise_browse", query)
}

func SearchIndustries(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	searchTerm, _ := params["search"].(string)

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					// Industry name search
					map[string]interface{}{
						"match": map[string]interface{}{
							"industry_name": map[string]interface{}{
								"query":     searchTerm,
								"fuzziness": "AUTO",
							},
						},
					},
					// Category name search (nested)
					map[string]interface{}{
						"nested": map[string]interface{}{
							"path": "categories",
							"query": map[string]interface{}{
								"match": map[string]interface{}{
									"categories.category_name": map[string]interface{}{
										"query":     searchTerm,
										"fuzziness": "AUTO",
									},
								},
							},
						},
					},
					// Sub-category name search (nested inside nested)
					map[string]interface{}{
						"nested": map[string]interface{}{
							"path": "categories",
							"query": map[string]interface{}{
								"nested": map[string]interface{}{
									"path": "categories.sub_categories",
									"query": map[string]interface{}{
										"match": map[string]interface{}{
											"categories.sub_categories.sub_category_name": map[string]interface{}{
												"query":     searchTerm,
												"fuzziness": "AUTO",
											},
										},
									},
								},
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
		"size": 50,
		"sort": []map[string]interface{}{
			{"_score": map[string]interface{}{"order": "desc"}},
			{"display_order": map[string]interface{}{"order": "asc"}},
		},
	}
	return executeQuery(ctx, esClient, "franchise_browse", query)
}

// ============================================================
// HELPER FUNCTIONS
// ============================================================

// buildSearchQuery builds a search query with filters
func buildSearchQuery(filters map[string]interface{}) map[string]interface{} {
	mustClauses := []map[string]interface{}{}
	filterClauses := []map[string]interface{}{}
	shouldClauses := []map[string]interface{}{}

	isAiSearch, _ := filters["isAiSearch"].(bool)

	// Entity Type filter
	if entityType, ok := filters["entityType"].(string); ok && entityType != "" {
		filterClauses = append(filterClauses, map[string]interface{}{
			"term": map[string]interface{}{
				"entity_type": entityType,
			},
		})
	} else if entityType, ok := filters["entity_type"].(string); ok && entityType != "" {
		filterClauses = append(filterClauses, map[string]interface{}{
			"term": map[string]interface{}{
				"entity_type": entityType,
			},
		})
	}

	// Text search
	if searchQuery, ok := filters["query"].(string); ok && searchQuery != "" {
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

		locationVal, hasLocation := filters["location"].(string)
		isQueryJustLocation := hasLocation &&
			strings.EqualFold(strings.TrimSpace(cleanQuery), strings.TrimSpace(locationVal))

		// Skip generic noise words that won't match any franchise name
		noiseWords := []string{"franchise", "franchises", "business", "businesses"}
		isNoise := false
		cleanLower := strings.ToLower(cleanQuery)
		for _, nw := range noiseWords {
			if strings.TrimSpace(cleanLower) == nw {
				isNoise = true
				break
			}
		}

		if !isQueryJustLocation && cleanQuery != "" && !isNoise {
			textClause := map[string]interface{}{
				"multi_match": map[string]interface{}{
					"query":     cleanQuery,
					"fields":    []string{"name^3", "description^2", "tags", "industry.name"},
					"type":      "best_fields",
					"fuzziness": "AUTO",
				},
			}

			if isAiSearch {
				// For AI search: text query is an optional relevance boost, not a hard filter.
				// The AI has already extracted structured filters (industry, location, investment, etc.)
				// so forcing a text match on "food and fashion franchise" would eliminate valid results.
				shouldClauses = append(shouldClauses, textClause)
			} else {
				mustClauses = append(mustClauses, textClause)
			}
		}
	}

	// 1. Industry filter
	var industryVal string
	if ind, ok := filters["industry"].(string); ok && ind != "" {
		industryVal = ind
	} else if ind, ok := filters["industrySlug"].(string); ok && ind != "" {
		industryVal = ind
	} else if ind, ok := filters["industrySlugAll"].(string); ok && ind != "" {
		industryVal = ind
	}

	if industryVal != "" {
		var shouldClausesInner []interface{}
		parts := strings.Split(industryVal, ",")
		for _, part := range parts {
			partTrimmed := strings.TrimSpace(part)
			if partTrimmed == "" {
				continue
			}
			shouldClausesInner = append(shouldClausesInner,
				map[string]interface{}{
					"term": map[string]interface{}{
						"industry.name.keyword": partTrimmed,
					},
				},
				map[string]interface{}{
					"match": map[string]interface{}{
						"industry.name": partTrimmed,
					},
				},
				map[string]interface{}{
					"term": map[string]interface{}{
						"industry.slug": buildIndustrySlug(partTrimmed),
					},
				},
			)
		}
		if len(shouldClausesInner) > 0 {
			clause := map[string]interface{}{
				"bool": map[string]interface{}{
					"should":               shouldClausesInner,
					"minimum_should_match": 1,
				},
			}
			filterClauses = append(filterClauses, clause)
		}
	}

	// 2. Category filter
	var categoryVal string
	if cat, ok := filters["category"].(string); ok && cat != "" {
		categoryVal = cat
	} else if cat, ok := filters["categorySlug"].(string); ok && cat != "" {
		categoryVal = cat
	}

	if categoryVal != "" {
		var shouldClausesInner []interface{}
		parts := strings.Split(categoryVal, ",")
		for _, part := range parts {
			partTrimmed := strings.TrimSpace(part)
			if partTrimmed == "" {
				continue
			}
			shouldClausesInner = append(shouldClausesInner,
				map[string]interface{}{
					"term": map[string]interface{}{
						"categories.slug": strings.ToLower(partTrimmed),
					},
				},
				map[string]interface{}{
					"match": map[string]interface{}{
						"categories.name": partTrimmed,
					},
				},
				map[string]interface{}{
					"term": map[string]interface{}{
						"categories.slug": buildIndustrySlug(partTrimmed),
					},
				},
			)
		}
		if len(shouldClausesInner) > 0 {
			clause := map[string]interface{}{
				"nested": map[string]interface{}{
					"path": "categories",
					"query": map[string]interface{}{
						"bool": map[string]interface{}{
							"should":               shouldClausesInner,
							"minimum_should_match": 1,
						},
					},
				},
			}
			filterClauses = append(filterClauses, clause)
		}
	}

	// 3. Subcategory filter
	var subCategoryVal string
	if subCat, ok := filters["subcategory"].(string); ok && subCat != "" {
		subCategoryVal = subCat
	} else if subCat, ok := filters["subCategory"].(string); ok && subCat != "" {
		subCategoryVal = subCat
	} else if subCat, ok := filters["subCategorySlug"].(string); ok && subCat != "" {
		subCategoryVal = subCat
	}

	if subCategoryVal != "" {
		var shouldClausesInner []interface{}
		parts := strings.Split(subCategoryVal, ",")
		for _, part := range parts {
			partTrimmed := strings.TrimSpace(part)
			if partTrimmed == "" {
				continue
			}
			shouldClausesInner = append(shouldClausesInner,
				map[string]interface{}{
					"term": map[string]interface{}{
						"sub_categories.slug": strings.ToLower(partTrimmed),
					},
				},
				map[string]interface{}{
					"match": map[string]interface{}{
						"sub_categories.name": partTrimmed,
					},
				},
				map[string]interface{}{
					"term": map[string]interface{}{
						"sub_categories.slug": buildIndustrySlug(partTrimmed),
					},
				},
			)
		}
		if len(shouldClausesInner) > 0 {
			clause := map[string]interface{}{
				"nested": map[string]interface{}{
					"path": "sub_categories",
					"query": map[string]interface{}{
						"bool": map[string]interface{}{
							"should":               shouldClausesInner,
							"minimum_should_match": 1,
						},
					},
				},
			}
			filterClauses = append(filterClauses, clause)
		}
	}

	// Location filter (supports comma-separated multiple locations)
	if loc, ok := filters["location"].(string); ok && loc != "" {
		cities := strings.Split(loc, ",")
		seen := make(map[string]bool)
		var allTerms []string
		
		for _, city := range cities {
			cityStr := strings.TrimSpace(city)
			if cityStr == "" {
				continue
			}
			terms := location.BuildLocationTerms(cityStr)
			for _, t := range terms {
				if !seen[t] {
					seen[t] = true
					allTerms = append(allTerms, t)
				}
			}
		}

		if len(allTerms) > 0 {
			filterClauses = append(filterClauses, map[string]interface{}{
				"bool": map[string]interface{}{
					"should": []interface{}{
						map[string]interface{}{
							"terms": map[string]interface{}{
								"location": allTerms,
							},
						},
					},
					"minimum_should_match": 1,
				},
			})
		}
	}

	// Helper to extract float64 from various numeric types
	toFloat64 := func(v interface{}) float64 {
		if v == nil {
			return 0
		}
		switch val := v.(type) {
		case float64:
			return val
		case int:
			return float64(val)
		case int64:
			return float64(val)
		}
		return 0
	}

	// Investment range filter
	minInv := toFloat64(filters["minInvestment"])
	if minInv > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"investment.max_investment": map[string]interface{}{"gte": minInv / 100000},
			},
		})
	}

	maxInv := toFloat64(filters["maxInvestment"])
	if maxInv > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"investment.min_investment": map[string]interface{}{"lte": maxInv / 100000},
			},
		})
	}

	// Space range filter
	minSpace := toFloat64(filters["minSpace"])
	if minSpace > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"space.maxSpace": map[string]interface{}{"gte": minSpace},
			},
		})
	}

	maxSpace := toFloat64(filters["maxSpace"])
	if maxSpace > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"space.minSpace": map[string]interface{}{"lte": maxSpace},
			},
		})
	}

	// ROI range filter
	minRoi := toFloat64(filters["roi"])
	if minRoi == 0 {
		minRoi = toFloat64(filters["minRoi"])
	}
	maxRoi := toFloat64(filters["maxRoi"])

	if minRoi > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"roi.max": map[string]interface{}{"gte": minRoi},
			},
		})
	}
	if maxRoi > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"roi.min": map[string]interface{}{"lte": maxRoi},
			},
		})
	}

	// Tags filter
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
			clause := map[string]interface{}{
				"bool": map[string]interface{}{
					"should":               tagShoulds,
					"minimum_should_match": 1,
				},
			}
			if isAiSearch {
				shouldClauses = append(shouldClauses, clause)
			} else {
				filterClauses = append(filterClauses, clause)
			}
		}
	}

	// Min rating filter
	minRating := toFloat64(filters["minRating"])
	if minRating > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"rating": map[string]interface{}{"gte": minRating},
			},
		})
	}

	// Member count range filter
	minMembers := toFloat64(filters["minMembers"])
	if minMembers == 0 {
		minMembers = toFloat64(filters["min_members"])
	}
	if minMembers > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"member_count": map[string]interface{}{"gte": minMembers},
			},
		})
	}

	maxMembers := toFloat64(filters["maxMembers"])
	if maxMembers == 0 {
		maxMembers = toFloat64(filters["max_members"])
	}
	if maxMembers > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"member_count": map[string]interface{}{"lte": maxMembers},
			},
		})
	}

	// Membership fee range filter
	minFee := toFloat64(filters["minFee"])
	if minFee == 0 {
		minFee = toFloat64(filters["min_fee"])
	}
	if minFee > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"membership_fee_max": map[string]interface{}{"gte": minFee},
			},
		})
	}

	maxFee := toFloat64(filters["maxFee"])
	if maxFee == 0 {
		maxFee = toFloat64(filters["max_fee"])
	}
	if maxFee > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"membership_fee_min": map[string]interface{}{"lte": maxFee},
			},
		})
	}

	// Master Franchise filters
	if exclusivityType, ok := filters["exclusivityType"].(string); ok && exclusivityType != "" {
		filterClauses = append(filterClauses, map[string]interface{}{
			"term": map[string]interface{}{
				"exclusivity_type": exclusivityType,
			},
		})
	}

	if territoryScope, ok := filters["territoryScope"].(string); ok && territoryScope != "" {
		filterClauses = append(filterClauses, map[string]interface{}{
			"term": map[string]interface{}{
				"territory_scope": territoryScope,
			},
		})
	}

	minUnits := toFloat64(filters["minUnits"])
	if minUnits > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"total_outlets": map[string]interface{}{"gte": minUnits},
			},
		})
	}

	var isLocal bool
	if lbo, ok := filters["localBrandsOnly"].(bool); ok {
		isLocal = lbo
	} else if lboStr, ok := filters["localBrandsOnly"].(string); ok {
		isLocal = strings.ToLower(strings.TrimSpace(lboStr)) == "true"
	}
	if isLocal {
		filterClauses = append(filterClauses, map[string]interface{}{
			"term": map[string]interface{}{
				"country.keyword": "India",
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

	if len(shouldClauses) > 0 {
		boolQuery["should"] = shouldClauses
	}

	return map[string]interface{}{
		"query": map[string]interface{}{
			"bool": boolQuery,
		},
	}
}

// buildIndustrySlug converts industry name to ES slug format
// "Sports & Fitness" → "sports-fitness"
// "Finance / Banking" → "finance-banking"
// "Hotel, Travel & Tourism" → "hotel-travel-tourism"
func buildIndustrySlug(name string) string {
	slug := strings.ToLower(name)
	slug = strings.ReplaceAll(slug, " & ", "-")
	slug = strings.ReplaceAll(slug, " / ", "-")
	slug = strings.ReplaceAll(slug, "&", "")
	slug = strings.ReplaceAll(slug, "/", "")
	slug = strings.ReplaceAll(slug, ",", "")
	slug = strings.ReplaceAll(slug, " ", "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	return slug
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
					// Add metadata back
					source["_id"] = hitMap["_id"]
					if score, ok := hitMap["_score"].(float64); ok {
						source["_score"] = score
					}
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
			"circle": logoURL,
			"square": logoURL,
			"alt":    brandName,
		}
		delete(source, "logo_url")
		// source["logo"] = map[string]interface{}{
		// 	"url": logoURL,
		// 	"alt": brandName,
		// }
		// delete(source, "logo_url")
	}

	// ✅ FIX 6: Ensure space has spaceUnit
	if space, ok := source["space"].(map[string]interface{}); ok {
		if _, hasUnit := space["spaceUnit"]; !hasUnit {
			space["spaceUnit"] = "sq ft"
		}
	}

	// ✅ FIX 7: Map investment range for listing cards
	if invRange, ok := source["investmentRange"].(map[string]interface{}); ok {
		// Ensure unit exists
		if _, hasUnit := invRange["investmentUnit"]; !hasUnit {
			invRange["investmentUnit"] = "Lakhs"
		}
	}

	return source
}
