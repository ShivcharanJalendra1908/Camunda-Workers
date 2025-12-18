// internal/workers/franchise/search-franchises/handler.go
package searchfranchises

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/logger"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/camunda/zeebe/clients/go/v8/pkg/zbc"
)

type Handler struct {
	config   *Config
	logger   logger.Logger
	esClient *database.ElasticsearchClient
}

func NewHandler(config *Config, esClient *database.ElasticsearchClient, log logger.Logger) *Handler {
	return &Handler{
		config:   config,
		logger:   log,
		esClient: esClient,
	}
}

// Execute provides a direct API for searching franchises - ADDED THIS METHOD
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	h.logger.Info("Executing franchise search via direct API", map[string]interface{}{
		"query":    input.Query,
		"category": input.Category,
		"page":     input.Page,
		"limit":    input.Limit,
	})

	startTime := time.Now()

	// Validate input
	if err := h.validateInput(input); err != nil {
		return nil, fmt.Errorf("input validation failed: %w", err)
	}

	// Build Elasticsearch query
	searchRequest, err := h.buildSearchRequest(input)
	if err != nil {
		return nil, fmt.Errorf("failed to build search request: %w", err)
	}

	// Determine which indices to search
	indices := h.getIndicesToSearch(input.Category)

	// Execute search across indices
	results, totalCount, err := h.executeSearch(indices, searchRequest)
	if err != nil {
		return nil, fmt.Errorf("search execution failed: %w", err)
	}

	// Get suggestions if enabled
	var suggestions []string
	if h.config.EnableSuggestions && input.Query != "" {
		suggestions = h.getSuggestions(input.Query, indices)
	}

	// Calculate query time
	queryTime := time.Since(startTime).Milliseconds()

	// Prepare output
	output := h.prepareOutput(results, totalCount, queryTime, suggestions, input)

	h.logger.Info("Franchise search via direct API completed", map[string]interface{}{
		"results_count": len(results),
		"query_time_ms": queryTime,
		"total_count":   totalCount,
	})

	return output, nil
}

// Register registers the worker with Zeebe
func (h *Handler) Register(client zbc.Client, jobType string) {
	// ✅ FIXED: Use correct Zeebe v8 API
	jobWorker := client.NewJobWorker().
		JobType(jobType).
		Handler(h.HandleJob).
		MaxJobsActive(5).
		Concurrency(3).
		PollInterval(100 * time.Millisecond).
		Timeout(60 * time.Second).
		Open()

	// Store worker reference if needed
	_ = jobWorker

	h.logger.Info("Franchise search worker registered", map[string]interface{}{
		"job_type": jobType,
	})
}

func (h *Handler) HandleJob(client worker.JobClient, job entities.Job) {
	h.logger.Info("Processing franchise search job", map[string]interface{}{
		"job_id":   job.GetKey(),
		"job_type": job.Type,
	})

	startTime := time.Now()

	// Parse input variables
	input, err := h.parseInput(job)
	if err != nil {
		h.logger.Error("Failed to parse input variables", map[string]interface{}{
			"error": err.Error(),
		})
		h.failJob(client, job, "Invalid input parameters")
		return
	}

	// Validate input
	if err := h.validateInput(input); err != nil {
		h.logger.Error("Input validation failed", map[string]interface{}{
			"error": err.Error(),
		})
		h.failJob(client, job, err.Error())
		return
	}

	// Build Elasticsearch query
	searchRequest, err := h.buildSearchRequest(input)
	if err != nil {
		h.logger.Error("Failed to build search request", map[string]interface{}{
			"error": err.Error(),
		})
		h.failJob(client, job, "Failed to build search query")
		return
	}

	// Determine which indices to search
	indices := h.getIndicesToSearch(input.Category)

	// Execute search across indices
	results, totalCount, err := h.executeSearch(indices, searchRequest)
	if err != nil {
		h.logger.Error("Search execution failed", map[string]interface{}{
			"error": err.Error(),
		})
		h.failJob(client, job, "Search execution failed")
		return
	}

	// Get suggestions if enabled
	var suggestions []string
	if h.config.EnableSuggestions && input.Query != "" {
		suggestions = h.getSuggestions(input.Query, indices)
	}

	// Calculate query time
	queryTime := time.Since(startTime).Milliseconds()

	// Prepare output
	output := h.prepareOutput(results, totalCount, queryTime, suggestions, input)

	// Complete job
	h.completeJob(client, job, output)

	h.logger.Info("Franchise search job completed", map[string]interface{}{
		"job_id":        job.GetKey(),
		"results_count": len(results),
		"query_time_ms": queryTime,
	})
}

func (h *Handler) parseInput(job entities.Job) (*Input, error) {
	var input Input

	// ✅ FIXED: job.Variables is a STRING in Zeebe v8, not a map
	// We need to unmarshal it first
	var vars map[string]interface{}
	if err := json.Unmarshal([]byte(job.Variables), &vars); err != nil {
		return nil, fmt.Errorf("failed to unmarshal variables: %w", err)
	}

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

	// Parse array inputs
	if tags, ok := vars["tags"].(string); ok {
		if tags != "" {
			input.Tags = strings.Split(tags, ",")
		}
	} else if tagsList, ok := vars["tags"].([]interface{}); ok {
		for _, tag := range tagsList {
			if tagStr, ok := tag.(string); ok {
				input.Tags = append(input.Tags, tagStr)
			}
		}
	}

	// Parse pagination
	input.Page = 1
	input.Limit = h.config.DefaultLimit

	if page, ok := vars["page"].(float64); ok {
		input.Page = int(page)
	}

	if limit, ok := vars["limit"].(float64); ok {
		input.Limit = int(limit)
		if input.Limit > h.config.MaxLimit {
			input.Limit = h.config.MaxLimit
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

	return &input, nil
}

func (h *Handler) validateInput(input *Input) error {
	// Validate pagination
	if input.Page < 1 {
		return fmt.Errorf("page must be greater than 0")
	}

	if input.Limit < 1 || input.Limit > h.config.MaxLimit {
		return fmt.Errorf("limit must be between 1 and %d", h.config.MaxLimit)
	}

	// Validate numeric ranges
	if input.MinInvestment > 0 && input.MaxInvestment > 0 && input.MinInvestment > input.MaxInvestment {
		return fmt.Errorf("min_investment cannot be greater than max_investment")
	}

	if input.MinSpace > 0 && input.MaxSpace > 0 && input.MinSpace > input.MaxSpace {
		return fmt.Errorf("min_space cannot be greater than max_space")
	}

	if input.MinRating < 0 || input.MinRating > 5 {
		return fmt.Errorf("min_rating must be between 0 and 5")
	}

	return nil
}

func (h *Handler) getIndicesToSearch(category string) []string {
	switch strings.ToLower(category) {
	case "food", "food & beverage", "ice cream", "dessert":
		return []string{"food_beverage_franchises"}
	case "education", "training", "coaching":
		return []string{"education_franchises"}
	case "fashion", "apparel", "footwear", "jewellery":
		return []string{"fashion_franchises"}
	default:
		return []string{
			"food_beverage_franchises",
			"education_franchises",
			"fashion_franchises",
		}
	}
}

func (h *Handler) buildSearchRequest(input *Input) (*SearchRequest, error) {
	query := map[string]interface{}{
		"bool": map[string]interface{}{
			"must": []map[string]interface{}{},
		},
	}

	// Text search
	if input.Query != "" {
		multiMatch := map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  input.Query,
				"fields": []string{"brand^3", "description^2", "highlights", "tags"},
				"type":   "best_fields",
			},
		}

		if h.config.Fuzziness != "" {
			multiMatch["multi_match"].(map[string]interface{})["fuzziness"] = h.config.Fuzziness
		}

		query["bool"].(map[string]interface{})["must"] = append(
			query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
			multiMatch,
		)
	}

	// Location filter
	if input.Location != "" {
		locationFilter := map[string]interface{}{
			"term": map[string]interface{}{
				"location.keyword": input.Location,
			},
		}
		query["bool"].(map[string]interface{})["must"] = append(
			query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
			locationFilter,
		)
	}

	// Investment range filter
	if input.MinInvestment > 0 || input.MaxInvestment > 0 {
		rangeFilter := map[string]interface{}{}
		if input.MinInvestment > 0 {
			rangeFilter["gte"] = input.MinInvestment
		}
		if input.MaxInvestment > 0 {
			rangeFilter["lte"] = input.MaxInvestment
		}

		investmentFilter := map[string]interface{}{
			"range": map[string]interface{}{
				"investment_min": rangeFilter,
			},
		}
		query["bool"].(map[string]interface{})["must"] = append(
			query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
			investmentFilter,
		)
	}

	// Space range filter
	if input.MinSpace > 0 || input.MaxSpace > 0 {
		rangeFilter := map[string]interface{}{}
		if input.MinSpace > 0 {
			rangeFilter["gte"] = input.MinSpace
		}
		if input.MaxSpace > 0 {
			rangeFilter["lte"] = input.MaxSpace
		}

		spaceFilter := map[string]interface{}{
			"range": map[string]interface{}{
				"space_min": rangeFilter,
			},
		}
		query["bool"].(map[string]interface{})["must"] = append(
			query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
			spaceFilter,
		)
	}

	// Rating filter
	if input.MinRating > 0 {
		ratingFilter := map[string]interface{}{
			"range": map[string]interface{}{
				"rating": map[string]interface{}{
					"gte": input.MinRating,
				},
			},
		}
		query["bool"].(map[string]interface{})["must"] = append(
			query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
			ratingFilter,
		)
	}

	// Tags filter
	if len(input.Tags) > 0 {
		for _, tag := range input.Tags {
			tagFilter := map[string]interface{}{
				"term": map[string]interface{}{
					"tags.keyword": tag,
				},
			}
			query["bool"].(map[string]interface{})["must"] = append(
				query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
				tagFilter,
			)
		}
	}

	// Build sorting
	var sort []map[string]interface{}

	if input.SortBy != "" {
		order := "desc"
		if input.SortOrder != "" {
			order = input.SortOrder
		}

		switch input.SortBy {
		case "rating":
			sort = append(sort, map[string]interface{}{
				"rating": map[string]interface{}{"order": order, "missing": "_last"},
			})
		case "investment":
			sort = append(sort, map[string]interface{}{
				"investment_min": map[string]interface{}{"order": order},
			})
		case "since":
			sort = append(sort, map[string]interface{}{
				"since": map[string]interface{}{"order": order},
			})
		default:
			sort = append(sort, map[string]interface{}{
				"_score": map[string]interface{}{"order": "desc"},
			})
		}
	} else {
		sort = append(sort, map[string]interface{}{
			"_score": map[string]interface{}{"order": "desc"},
		})
	}

	sort = append(sort, map[string]interface{}{
		"rating": map[string]interface{}{"order": "desc", "missing": "_last"},
	})

	// Build aggregations
	var aggs map[string]interface{}
	if h.config.EnableAggregations {
		aggs = map[string]interface{}{
			"by_category": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "category.keyword",
					"size":  10,
				},
			},
			"by_location": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "location.keyword",
					"size":  10,
				},
			},
			"investment_ranges": map[string]interface{}{
				"range": map[string]interface{}{
					"field": "investment_min",
					"ranges": []map[string]interface{}{
						{"to": 1000000},
						{"from": 1000000, "to": 5000000},
						{"from": 5000000, "to": 20000000},
						{"from": 20000000},
					},
				},
			},
		}
	}

	return &SearchRequest{
		Query:        query,
		From:         (input.Page - 1) * input.Limit,
		Size:         input.Limit,
		Sort:         sort,
		Aggregations: aggs,
	}, nil
}

func (h *Handler) executeSearch(indices []string, request *SearchRequest) ([]map[string]interface{}, int, error) {
	var allResults []map[string]interface{}
	totalCount := 0

	for _, index := range indices {
		fullQuery := map[string]interface{}{
			"query": request.Query,
			"from":  request.From,
			"size":  request.Size,
			"sort":  request.Sort,
		}

		if request.Aggregations != nil {
			fullQuery["aggs"] = request.Aggregations
		}

		result, err := h.esClient.SearchDocuments(context.Background(), index, fullQuery)
		if err != nil {
			h.logger.Warn("Search failed for index", map[string]interface{}{
				"index": index,
				"error": err.Error(),
			})
			continue
		}

		if hits, ok := result["hits"].(map[string]interface{}); ok {
			if totalVal, ok := hits["total"].(map[string]interface{}); ok {
				if val, ok := totalVal["value"].(float64); ok {
					totalCount += int(val)
				}
			}

			if hitsList, ok := hits["hits"].([]interface{}); ok {
				for _, hit := range hitsList {
					if hitMap, ok := hit.(map[string]interface{}); ok {
						if source, ok := hitMap["_source"].(map[string]interface{}); ok {
							source["_id"] = hitMap["_id"]
							source["_score"] = hitMap["_score"]
							source["_index"] = hitMap["_index"]

							allResults = append(allResults, source)
						}
					}
				}
			}
		}
	}

	return allResults, totalCount, nil
}

func (h *Handler) getSuggestions(query string, indices []string) []string {
	var suggestions []string

	suggestQuery := map[string]interface{}{
		"suggest": map[string]interface{}{
			"brand_suggest": map[string]interface{}{
				"prefix": query,
				"completion": map[string]interface{}{
					"field":           "brand",
					"skip_duplicates": true,
					"size":            5,
				},
			},
		},
	}

	for _, index := range indices {
		result, err := h.esClient.SearchDocuments(context.Background(), index, suggestQuery)
		if err != nil {
			continue
		}

		if suggests, ok := result["suggest"].(map[string]interface{}); ok {
			if brandSuggests, ok := suggests["brand_suggest"].([]interface{}); ok {
				for _, suggestion := range brandSuggests {
					if sMap, ok := suggestion.(map[string]interface{}); ok {
						if options, ok := sMap["options"].([]interface{}); ok {
							for _, option := range options {
								if optMap, ok := option.(map[string]interface{}); ok {
									if text, ok := optMap["text"].(string); ok {
										suggestions = append(suggestions, text)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return suggestions
}

func (h *Handler) prepareOutput(results []map[string]interface{}, totalCount int, queryTime int64, suggestions []string, input *Input) *Output {
	appliedFilters := map[string]interface{}{
		"query":          input.Query,
		"category":       input.Category,
		"location":       input.Location,
		"min_investment": input.MinInvestment,
		"max_investment": input.MaxInvestment,
		"min_space":      input.MinSpace,
		"max_space":      input.MaxSpace,
		"min_rating":     input.MinRating,
		"tags":           input.Tags,
		"page":           input.Page,
		"limit":          input.Limit,
		"sort_by":        input.SortBy,
		"sort_order":     input.SortOrder,
	}

	return &Output{
		Franchises:     results,
		TotalCount:     totalCount,
		QueryTimeMs:    queryTime,
		Suggestions:    suggestions,
		AppliedFilters: appliedFilters,
		Success:        true,
	}
}

func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
	variables := make(map[string]interface{})

	outputJSON, _ := json.Marshal(output)
	var outputMap map[string]interface{}
	json.Unmarshal(outputJSON, &outputMap)

	variables["search_results"] = outputMap
	variables["success"] = output.Success
	variables["total_count"] = output.TotalCount
	variables["query_time_ms"] = output.QueryTimeMs

	if len(output.Suggestions) > 0 {
		variables["suggestions"] = strings.Join(output.Suggestions, ",")
	}

	request, err := client.NewCompleteJobCommand().
		JobKey(job.GetKey()).
		VariablesFromMap(variables)

	if err != nil {
		h.logger.Error("Failed to create complete job command", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	ctx := context.Background()
	_, err = request.Send(ctx)
	if err != nil {
		h.logger.Error("Failed to complete job", map[string]interface{}{
			"error": err.Error(),
		})
	}
}

func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorMessage string) {
	variables := map[string]interface{}{
		"error_message": errorMessage,
		"success":       false,
	}

	request, err := client.NewFailJobCommand().
		JobKey(job.GetKey()).
		Retries(job.Retries - 1).
		ErrorMessage(errorMessage).
		VariablesFromMap(variables)

	if err != nil {
		h.logger.Error("Failed to create fail job command", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	ctx := context.Background()
	_, err = request.Send(ctx)
	if err != nil {
		h.logger.Error("Failed to fail job", map[string]interface{}{
			"error": err.Error(),
		})
	}
}

// // internal/workers/franchise/search-franchises/handler.go
// package searchfranchises

// import (
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"strings"
// 	"time"

// 	"camunda-workers/internal/common/database"
// 	"camunda-workers/internal/common/logger"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/zbc"
// )

// type Handler struct {
// 	config   *Config
// 	logger   logger.Logger
// 	esClient *database.ElasticsearchClient
// }

// func NewHandler(config *Config, esClient *database.ElasticsearchClient, log logger.Logger) *Handler {
// 	return &Handler{
// 		config:   config,
// 		logger:   log,
// 		esClient: esClient,
// 	}
// }

// // Register registers the worker with Zeebe
// func (h *Handler) Register(client zbc.Client, jobType string) {
// 	// ✅ FIXED: Use correct Zeebe v8 API
// 	jobWorker := client.NewJobWorker().
// 		JobType(jobType).
// 		Handler(h.HandleJob).
// 		MaxJobsActive(5).
// 		Concurrency(3).
// 		PollInterval(100 * time.Millisecond).
// 		Timeout(60 * time.Second).
// 		Open()

// 	// Store worker reference if needed
// 	_ = jobWorker

// 	h.logger.Info("Franchise search worker registered", map[string]interface{}{
// 		"job_type": jobType,
// 	})
// }

// func (h *Handler) HandleJob(client worker.JobClient, job entities.Job) {
// 	h.logger.Info("Processing franchise search job", map[string]interface{}{
// 		"job_id":   job.GetKey(),
// 		"job_type": job.Type,
// 	})

// 	startTime := time.Now()

// 	// Parse input variables
// 	input, err := h.parseInput(job)
// 	if err != nil {
// 		h.logger.Error("Failed to parse input variables", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 		h.failJob(client, job, "Invalid input parameters")
// 		return
// 	}

// 	// Validate input
// 	if err := h.validateInput(input); err != nil {
// 		h.logger.Error("Input validation failed", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 		h.failJob(client, job, err.Error())
// 		return
// 	}

// 	// Build Elasticsearch query
// 	searchRequest, err := h.buildSearchRequest(input)
// 	if err != nil {
// 		h.logger.Error("Failed to build search request", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 		h.failJob(client, job, "Failed to build search query")
// 		return
// 	}

// 	// Determine which indices to search
// 	indices := h.getIndicesToSearch(input.Category)

// 	// Execute search across indices
// 	results, totalCount, err := h.executeSearch(indices, searchRequest)
// 	if err != nil {
// 		h.logger.Error("Search execution failed", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 		h.failJob(client, job, "Search execution failed")
// 		return
// 	}

// 	// Get suggestions if enabled
// 	var suggestions []string
// 	if h.config.EnableSuggestions && input.Query != "" {
// 		suggestions = h.getSuggestions(input.Query, indices)
// 	}

// 	// Calculate query time
// 	queryTime := time.Since(startTime).Milliseconds()

// 	// Prepare output
// 	output := h.prepareOutput(results, totalCount, queryTime, suggestions, input)

// 	// Complete job
// 	h.completeJob(client, job, output)

// 	h.logger.Info("Franchise search job completed", map[string]interface{}{
// 		"job_id":        job.GetKey(),
// 		"results_count": len(results),
// 		"query_time_ms": queryTime,
// 	})
// }

// func (h *Handler) parseInput(job entities.Job) (*Input, error) {
// 	var input Input

// 	// ✅ FIXED: job.Variables is a STRING in Zeebe v8, not a map
// 	// We need to unmarshal it first
// 	var vars map[string]interface{}
// 	if err := json.Unmarshal([]byte(job.Variables), &vars); err != nil {
// 		return nil, fmt.Errorf("failed to unmarshal variables: %w", err)
// 	}

// 	// Parse string inputs
// 	if query, ok := vars["query"].(string); ok {
// 		input.Query = query
// 	}

// 	if category, ok := vars["category"].(string); ok {
// 		input.Category = category
// 	}

// 	if location, ok := vars["location"].(string); ok {
// 		input.Location = location
// 	}

// 	// Parse numeric inputs
// 	if minInv, ok := vars["min_investment"].(float64); ok {
// 		input.MinInvestment = minInv
// 	}

// 	if maxInv, ok := vars["max_investment"].(float64); ok {
// 		input.MaxInvestment = maxInv
// 	}

// 	if minSpace, ok := vars["min_space"].(float64); ok {
// 		input.MinSpace = minSpace
// 	}

// 	if maxSpace, ok := vars["max_space"].(float64); ok {
// 		input.MaxSpace = maxSpace
// 	}

// 	if minRating, ok := vars["min_rating"].(float64); ok {
// 		input.MinRating = minRating
// 	}

// 	// Parse array inputs
// 	if tags, ok := vars["tags"].(string); ok {
// 		if tags != "" {
// 			input.Tags = strings.Split(tags, ",")
// 		}
// 	} else if tagsList, ok := vars["tags"].([]interface{}); ok {
// 		for _, tag := range tagsList {
// 			if tagStr, ok := tag.(string); ok {
// 				input.Tags = append(input.Tags, tagStr)
// 			}
// 		}
// 	}

// 	// Parse pagination
// 	input.Page = 1
// 	input.Limit = h.config.DefaultLimit

// 	if page, ok := vars["page"].(float64); ok {
// 		input.Page = int(page)
// 	}

// 	if limit, ok := vars["limit"].(float64); ok {
// 		input.Limit = int(limit)
// 		if input.Limit > h.config.MaxLimit {
// 			input.Limit = h.config.MaxLimit
// 		}
// 	}

// 	// Parse sorting
// 	if sortBy, ok := vars["sort_by"].(string); ok {
// 		input.SortBy = sortBy
// 	}

// 	if sortOrder, ok := vars["sort_order"].(string); ok {
// 		input.SortOrder = sortOrder
// 	}

// 	// Parse user context
// 	if userId, ok := vars["user_id"].(string); ok {
// 		input.UserId = userId
// 	}

// 	if sessionId, ok := vars["session_id"].(string); ok {
// 		input.SessionId = sessionId
// 	}

// 	return &input, nil
// }

// func (h *Handler) validateInput(input *Input) error {
// 	// Validate pagination
// 	if input.Page < 1 {
// 		return fmt.Errorf("page must be greater than 0")
// 	}

// 	if input.Limit < 1 || input.Limit > h.config.MaxLimit {
// 		return fmt.Errorf("limit must be between 1 and %d", h.config.MaxLimit)
// 	}

// 	// Validate numeric ranges
// 	if input.MinInvestment > 0 && input.MaxInvestment > 0 && input.MinInvestment > input.MaxInvestment {
// 		return fmt.Errorf("min_investment cannot be greater than max_investment")
// 	}

// 	if input.MinSpace > 0 && input.MaxSpace > 0 && input.MinSpace > input.MaxSpace {
// 		return fmt.Errorf("min_space cannot be greater than max_space")
// 	}

// 	if input.MinRating < 0 || input.MinRating > 5 {
// 		return fmt.Errorf("min_rating must be between 0 and 5")
// 	}

// 	return nil
// }

// func (h *Handler) getIndicesToSearch(category string) []string {
// 	switch strings.ToLower(category) {
// 	case "food", "food & beverage", "ice cream", "dessert":
// 		return []string{"food_beverage_franchises"}
// 	case "education", "training", "coaching":
// 		return []string{"education_franchises"}
// 	case "fashion", "apparel", "footwear", "jewellery":
// 		return []string{"fashion_franchises"}
// 	default:
// 		return []string{
// 			"food_beverage_franchises",
// 			"education_franchises",
// 			"fashion_franchises",
// 		}
// 	}
// }

// func (h *Handler) buildSearchRequest(input *Input) (*SearchRequest, error) {
// 	query := map[string]interface{}{
// 		"bool": map[string]interface{}{
// 			"must": []map[string]interface{}{},
// 		},
// 	}

// 	// Text search
// 	if input.Query != "" {
// 		multiMatch := map[string]interface{}{
// 			"multi_match": map[string]interface{}{
// 				"query":  input.Query,
// 				"fields": []string{"brand^3", "description^2", "highlights", "tags"},
// 				"type":   "best_fields",
// 			},
// 		}

// 		if h.config.Fuzziness != "" {
// 			multiMatch["multi_match"].(map[string]interface{})["fuzziness"] = h.config.Fuzziness
// 		}

// 		query["bool"].(map[string]interface{})["must"] = append(
// 			query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
// 			multiMatch,
// 		)
// 	}

// 	// Location filter
// 	if input.Location != "" {
// 		locationFilter := map[string]interface{}{
// 			"term": map[string]interface{}{
// 				"location.keyword": input.Location,
// 			},
// 		}
// 		query["bool"].(map[string]interface{})["must"] = append(
// 			query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
// 			locationFilter,
// 		)
// 	}

// 	// Investment range filter
// 	if input.MinInvestment > 0 || input.MaxInvestment > 0 {
// 		rangeFilter := map[string]interface{}{}
// 		if input.MinInvestment > 0 {
// 			rangeFilter["gte"] = input.MinInvestment
// 		}
// 		if input.MaxInvestment > 0 {
// 			rangeFilter["lte"] = input.MaxInvestment
// 		}

// 		investmentFilter := map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"investment_min": rangeFilter,
// 			},
// 		}
// 		query["bool"].(map[string]interface{})["must"] = append(
// 			query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
// 			investmentFilter,
// 		)
// 	}

// 	// Space range filter
// 	if input.MinSpace > 0 || input.MaxSpace > 0 {
// 		rangeFilter := map[string]interface{}{}
// 		if input.MinSpace > 0 {
// 			rangeFilter["gte"] = input.MinSpace
// 		}
// 		if input.MaxSpace > 0 {
// 			rangeFilter["lte"] = input.MaxSpace
// 		}

// 		spaceFilter := map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"space_min": rangeFilter,
// 			},
// 		}
// 		query["bool"].(map[string]interface{})["must"] = append(
// 			query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
// 			spaceFilter,
// 		)
// 	}

// 	// Rating filter
// 	if input.MinRating > 0 {
// 		ratingFilter := map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"rating": map[string]interface{}{
// 					"gte": input.MinRating,
// 				},
// 			},
// 		}
// 		query["bool"].(map[string]interface{})["must"] = append(
// 			query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
// 			ratingFilter,
// 		)
// 	}

// 	// Tags filter
// 	if len(input.Tags) > 0 {
// 		for _, tag := range input.Tags {
// 			tagFilter := map[string]interface{}{
// 				"term": map[string]interface{}{
// 					"tags.keyword": tag,
// 				},
// 			}
// 			query["bool"].(map[string]interface{})["must"] = append(
// 				query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
// 				tagFilter,
// 			)
// 		}
// 	}

// 	// Build sorting
// 	var sort []map[string]interface{}

// 	if input.SortBy != "" {
// 		order := "desc"
// 		if input.SortOrder != "" {
// 			order = input.SortOrder
// 		}

// 		switch input.SortBy {
// 		case "rating":
// 			sort = append(sort, map[string]interface{}{
// 				"rating": map[string]interface{}{"order": order, "missing": "_last"},
// 			})
// 		case "investment":
// 			sort = append(sort, map[string]interface{}{
// 				"investment_min": map[string]interface{}{"order": order},
// 			})
// 		case "since":
// 			sort = append(sort, map[string]interface{}{
// 				"since": map[string]interface{}{"order": order},
// 			})
// 		default:
// 			sort = append(sort, map[string]interface{}{
// 				"_score": map[string]interface{}{"order": "desc"},
// 			})
// 		}
// 	} else {
// 		sort = append(sort, map[string]interface{}{
// 			"_score": map[string]interface{}{"order": "desc"},
// 		})
// 	}

// 	sort = append(sort, map[string]interface{}{
// 		"rating": map[string]interface{}{"order": "desc", "missing": "_last"},
// 	})

// 	// Build aggregations
// 	var aggs map[string]interface{}
// 	if h.config.EnableAggregations {
// 		aggs = map[string]interface{}{
// 			"by_category": map[string]interface{}{
// 				"terms": map[string]interface{}{
// 					"field": "category.keyword",
// 					"size":  10,
// 				},
// 			},
// 			"by_location": map[string]interface{}{
// 				"terms": map[string]interface{}{
// 					"field": "location.keyword",
// 					"size":  10,
// 				},
// 			},
// 			"investment_ranges": map[string]interface{}{
// 				"range": map[string]interface{}{
// 					"field": "investment_min",
// 					"ranges": []map[string]interface{}{
// 						{"to": 1000000},
// 						{"from": 1000000, "to": 5000000},
// 						{"from": 5000000, "to": 20000000},
// 						{"from": 20000000},
// 					},
// 				},
// 			},
// 		}
// 	}

// 	return &SearchRequest{
// 		Query:        query,
// 		From:         (input.Page - 1) * input.Limit,
// 		Size:         input.Limit,
// 		Sort:         sort,
// 		Aggregations: aggs,
// 	}, nil
// }

// func (h *Handler) executeSearch(indices []string, request *SearchRequest) ([]map[string]interface{}, int, error) {
// 	var allResults []map[string]interface{}
// 	totalCount := 0

// 	for _, index := range indices {
// 		fullQuery := map[string]interface{}{
// 			"query": request.Query,
// 			"from":  request.From,
// 			"size":  request.Size,
// 			"sort":  request.Sort,
// 		}

// 		if request.Aggregations != nil {
// 			fullQuery["aggs"] = request.Aggregations
// 		}

// 		result, err := h.esClient.SearchDocuments(context.Background(), index, fullQuery)
// 		if err != nil {
// 			h.logger.Warn("Search failed for index", map[string]interface{}{
// 				"index": index,
// 				"error": err.Error(),
// 			})
// 			continue
// 		}

// 		if hits, ok := result["hits"].(map[string]interface{}); ok {
// 			if totalVal, ok := hits["total"].(map[string]interface{}); ok {
// 				if val, ok := totalVal["value"].(float64); ok {
// 					totalCount += int(val)
// 				}
// 			}

// 			if hitsList, ok := hits["hits"].([]interface{}); ok {
// 				for _, hit := range hitsList {
// 					if hitMap, ok := hit.(map[string]interface{}); ok {
// 						if source, ok := hitMap["_source"].(map[string]interface{}); ok {
// 							source["_id"] = hitMap["_id"]
// 							source["_score"] = hitMap["_score"]
// 							source["_index"] = hitMap["_index"]

// 							allResults = append(allResults, source)
// 						}
// 					}
// 				}
// 			}
// 		}
// 	}

// 	return allResults, totalCount, nil
// }

// func (h *Handler) getSuggestions(query string, indices []string) []string {
// 	var suggestions []string

// 	suggestQuery := map[string]interface{}{
// 		"suggest": map[string]interface{}{
// 			"brand_suggest": map[string]interface{}{
// 				"prefix": query,
// 				"completion": map[string]interface{}{
// 					"field":           "brand",
// 					"skip_duplicates": true,
// 					"size":            5,
// 				},
// 			},
// 		},
// 	}

// 	for _, index := range indices {
// 		result, err := h.esClient.SearchDocuments(context.Background(), index, suggestQuery)
// 		if err != nil {
// 			continue
// 		}

// 		if suggests, ok := result["suggest"].(map[string]interface{}); ok {
// 			if brandSuggests, ok := suggests["brand_suggest"].([]interface{}); ok {
// 				for _, suggestion := range brandSuggests {
// 					if sMap, ok := suggestion.(map[string]interface{}); ok {
// 						if options, ok := sMap["options"].([]interface{}); ok {
// 							for _, option := range options {
// 								if optMap, ok := option.(map[string]interface{}); ok {
// 									if text, ok := optMap["text"].(string); ok {
// 										suggestions = append(suggestions, text)
// 									}
// 								}
// 							}
// 						}
// 					}
// 				}
// 			}
// 		}
// 	}

// 	return suggestions
// }

// func (h *Handler) prepareOutput(results []map[string]interface{}, totalCount int, queryTime int64, suggestions []string, input *Input) *Output {
// 	appliedFilters := map[string]interface{}{
// 		"query":          input.Query,
// 		"category":       input.Category,
// 		"location":       input.Location,
// 		"min_investment": input.MinInvestment,
// 		"max_investment": input.MaxInvestment,
// 		"min_space":      input.MinSpace,
// 		"max_space":      input.MaxSpace,
// 		"min_rating":     input.MinRating,
// 		"tags":           input.Tags,
// 		"page":           input.Page,
// 		"limit":          input.Limit,
// 		"sort_by":        input.SortBy,
// 		"sort_order":     input.SortOrder,
// 	}

// 	return &Output{
// 		Franchises:     results,
// 		TotalCount:     totalCount,
// 		QueryTimeMs:    queryTime,
// 		Suggestions:    suggestions,
// 		AppliedFilters: appliedFilters,
// 		Success:        true,
// 	}
// }

// func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
// 	variables := make(map[string]interface{})

// 	outputJSON, _ := json.Marshal(output)
// 	var outputMap map[string]interface{}
// 	json.Unmarshal(outputJSON, &outputMap)

// 	variables["search_results"] = outputMap
// 	variables["success"] = output.Success
// 	variables["total_count"] = output.TotalCount
// 	variables["query_time_ms"] = output.QueryTimeMs

// 	if len(output.Suggestions) > 0 {
// 		variables["suggestions"] = strings.Join(output.Suggestions, ",")
// 	}

// 	request, err := client.NewCompleteJobCommand().
// 		JobKey(job.GetKey()).
// 		VariablesFromMap(variables)

// 	if err != nil {
// 		h.logger.Error("Failed to create complete job command", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 		return
// 	}

// 	ctx := context.Background()
// 	_, err = request.Send(ctx)
// 	if err != nil {
// 		h.logger.Error("Failed to complete job", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 	}
// }

// func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorMessage string) {
// 	variables := map[string]interface{}{
// 		"error_message": errorMessage,
// 		"success":       false,
// 	}

// 	request, err := client.NewFailJobCommand().
// 		JobKey(job.GetKey()).
// 		Retries(job.Retries - 1).
// 		ErrorMessage(errorMessage).
// 		VariablesFromMap(variables)

// 	if err != nil {
// 		h.logger.Error("Failed to create fail job command", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 		return
// 	}

// 	ctx := context.Background()
// 	_, err = request.Send(ctx)
// 	if err != nil {
// 		h.logger.Error("Failed to fail job", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 	}
// }
