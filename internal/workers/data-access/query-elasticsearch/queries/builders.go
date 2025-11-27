package queries

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

var (
	ErrUnknownQueryType = errors.New("unknown query type")
	ErrMissingIndex     = errors.New("index name is required")
)

// ElasticsearchQuery defines the structure of a query request
type ElasticsearchQuery struct {
	Index       string
	QueryType   string
	Filters     map[string]interface{}
	FranchiseID string
	Category    string
	Pagination  struct {
		From int
		Size int
	}
}

// BuildQuery builds an Elasticsearch search request based on query type and filters
func BuildQuery(esClient *elasticsearch.Client, eq ElasticsearchQuery) (*esapi.SearchRequest, error) {
	if eq.Index == "" {
		return nil, ErrMissingIndex
	}

	var queryBody map[string]interface{}

	switch eq.QueryType {
	case "franchise_index":
		queryBody = buildFranchiseSearchQuery(eq)
	case "related_franchises":
		queryBody = buildRelatedFranchisesQuery(eq)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownQueryType, eq.QueryType)
	}

	body, _ := json.Marshal(queryBody)

	req := esapi.SearchRequest{
		Index:  []string{eq.Index},
		Body:   strings.NewReader(string(body)),
		From:   &eq.Pagination.From,
		Size:   &eq.Pagination.Size,
		Pretty: true,
	}

	return &req, nil
}

// buildFranchiseSearchQuery builds the main franchise search query dynamically
func buildFranchiseSearchQuery(eq ElasticsearchQuery) map[string]interface{} {
	boolQuery := make(map[string]interface{})
	mustClauses := []interface{}{}
	filterClauses := []interface{}{}

	// Keyword search
	if keywords, ok := eq.Filters["keywords"].(string); ok && keywords != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  keywords,
				"fields": []string{"name^3", "description^2", "category"},
				"type":   "best_fields",
			},
		})
	}

	// Category filter
	if category, ok := eq.Filters["category"].(string); ok && category != "" {
		filterClauses = append(filterClauses, map[string]interface{}{
			"term": map[string]interface{}{"category": category},
		})
	} else if eq.Category != "" {
		filterClauses = append(filterClauses, map[string]interface{}{
			"term": map[string]interface{}{"category": eq.Category},
		})
	}

	// ✅ Investment range filter (final logic)
	if invRange, ok := eq.Filters["investmentRange"].(map[string]interface{}); ok {
		minRaw, minExists := invRange["min"]
		maxRaw, maxExists := invRange["max"]

		var minVal, maxVal float64
		if minExists {
			switch v := minRaw.(type) {
			case float64:
				minVal = v
			case int:
				minVal = float64(v)
			case int64:
				minVal = float64(v)
			}
		}
		if maxExists {
			switch v := maxRaw.(type) {
			case float64:
				maxVal = v
			case int:
				maxVal = float64(v)
			case int64:
				maxVal = float64(v)
			}
		}

		switch {
		// Case 3️⃣: Only max → check if we want containment or overlap
		case maxExists && maxVal > 0 && (!minExists || (minExists && minVal == 0)):
			// For very low ranges (0-200k), use containment: investment_max <= maxVal
			// For "up to" searches (0-500k), use overlap: investment_min <= maxVal
			if maxVal <= 200000 { // Special case for very low ranges
				filterClauses = append(filterClauses, map[string]interface{}{
					"range": map[string]interface{}{
						"investment_max": map[string]interface{}{"lte": maxVal},
					},
				})
			} else {
				// For normal "up to" searches
				filterClauses = append(filterClauses, map[string]interface{}{
					"range": map[string]interface{}{
						"investment_min": map[string]interface{}{"lte": maxVal},
					},
				})
			}

		// Case 1️⃣: Both min & max — full containment logic (only when min > 0)
		case minExists && maxExists && minVal > 0 && maxVal > 0 && minVal <= maxVal:
			filterClauses = append(filterClauses, map[string]interface{}{
				"bool": map[string]interface{}{
					"must": []interface{}{
						map[string]interface{}{
							"range": map[string]interface{}{
								"investment_min": map[string]interface{}{"gte": minVal},
							},
						},
						map[string]interface{}{
							"range": map[string]interface{}{
								"investment_max": map[string]interface{}{"lte": maxVal},
							},
						},
					},
				},
			})

		// Case 2️⃣: Only min → max >= min
		case minExists && !maxExists && minVal > 0:
			filterClauses = append(filterClauses, map[string]interface{}{
				"range": map[string]interface{}{
					"investment_max": map[string]interface{}{"gte": minVal},
				},
			})

		}
	}

	// Location filter
	if locations, ok := eq.Filters["locations"].([]interface{}); ok && len(locations) > 0 {
		terms := make([]string, 0, len(locations))
		for _, loc := range locations {
			if s, ok := loc.(string); ok {
				terms = append(terms, s)
			}
		}
		if len(terms) > 0 {
			filterClauses = append(filterClauses, map[string]interface{}{
				"terms": map[string]interface{}{"locations": terms},
			})
		}
	}

	// Default match_all if no keyword
	if len(mustClauses) == 0 {
		mustClauses = append(mustClauses, map[string]interface{}{"match_all": map[string]interface{}{}})
	}

	boolQuery["must"] = mustClauses
	if len(filterClauses) > 0 {
		boolQuery["filter"] = filterClauses
	}

	// ✅ Define query after boolQuery is ready
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": boolQuery,
		},
	}

	// Sorting logic
	if sortBy, ok := eq.Filters["sortBy"].(string); ok {
		switch sortBy {
		case "investment_min":
			query["sort"] = []map[string]interface{}{{"investment_min": "asc"}}
		case "name":
			query["sort"] = []map[string]interface{}{{"name": "asc"}}
		}
	}

	return query
}

// buildRelatedFranchisesQuery builds "similar franchises" query
func buildRelatedFranchisesQuery(eq ElasticsearchQuery) map[string]interface{} {
	if eq.FranchiseID == "" {
		return map[string]interface{}{
			"query": map[string]interface{}{
				"match_none": map[string]interface{}{},
			},
		}
	}

	return map[string]interface{}{
		"query": map[string]interface{}{
			"more_like_this": map[string]interface{}{
				"fields": []string{"name", "description", "category"},
				"like": []map[string]interface{}{
					{"_index": eq.Index, "_id": eq.FranchiseID},
				},
				"min_term_freq":   1,
				"max_query_terms": 12,
				"min_doc_freq":    1,
				"min_word_length": 3,
			},
		},
	}
}
