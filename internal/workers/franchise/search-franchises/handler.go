package searchfranchises

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType        = "search-franchises"
	ListingsIndex   = "franchise_listings"   // ✅ Main search index
	HomeIndex       = "franchise_home"       // ✅ Homepage data
	IndustriesIndex = "franchise_industries" // ✅ Industry pages
)

type Handler struct {
	config       *Config
	logger       logger.Logger
	esClient     *database.ElasticsearchClient
	errorHandler *errors.ErrorHandler
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
}

func NewHandler(config *Config, esClient *database.ElasticsearchClient, log logger.Logger) *Handler {
	return &Handler{
		config:       config,
		logger:       log,
		esClient:     esClient,
		errorHandler: errors.NewErrorHandler(log),
		validator:    validation.NewValidator(),
		sanitizer:    validation.NewSanitizer(),
	}
}

func (h *Handler) HandleJob(client worker.JobClient, job entities.Job) {
	ctx := context.Background()
	tracer := otel.Tracer("worker-manager")
	ctx, span := tracer.Start(ctx, "worker:"+TaskType,
		trace.WithAttributes(
			attribute.String("worker.name", TaskType),
			attribute.Int64("job.key", job.GetKey()),
		),
	)
	defer span.End()

	h.logger.Info("Processing franchise search job", map[string]interface{}{
		"job_id": job.GetKey(),
	})

	startTime := time.Now()

	// Parse input
	input, err := h.parseInput(job)
	if err != nil {
		span.RecordError(err)
		h.errorHandler.HandleJobError(ctx, client, job, errors.NewInvalidFilterFormatError("Invalid input"))
		return
	}

	// Validate input
	if err := h.validateInput(input); err != nil {
		span.RecordError(err)
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// Sanitize input
	h.sanitizeInput(input)

	// Build query
	searchRequest, err := h.buildSearchRequest(input)
	if err != nil {
		span.RecordError(err)
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// Execute search on franchise_listings index
	results, totalCount, err := h.executeSearch(ctx, searchRequest)
	if err != nil {
		span.RecordError(err)
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	queryTime := time.Since(startTime).Milliseconds()

	// Prepare output
	output := h.prepareOutput(results, totalCount, queryTime, input)

	// Complete job
	h.completeJob(ctx, client, job, output)

	h.logger.Info("Search job completed", map[string]interface{}{
		"job_id":      job.GetKey(),
		"results":     len(results),
		"query_time":  queryTime,
		"total_count": totalCount,
	})
}

func (h *Handler) validateInput(input *Input) error {
	// Query validation
	if input.Query != "" {
		if err := ozzo.Validate(input.Query,
			ozzo.Length(0, 500).Error("query must be max 500 characters"),
			validation.SafeSQLString,
			validation.SafeNoSQLString,
		); err != nil {
			return errors.NewValidationError("query", err.Error())
		}
	}

	// Category validation
	if input.Category != "" {
		if err := ozzo.Validate(input.Category,
			ozzo.Length(0, 100).Error("category must be max 100 characters"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("category", err.Error())
		}
	}

	// Industry validation
	if input.Industry != "" {
		if err := ozzo.Validate(input.Industry,
			ozzo.Length(0, 100).Error("industry must be max 100 characters"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("industry", err.Error())
		}
	}

	// Location validation
	if input.Location != "" {
		if err := ozzo.Validate(input.Location,
			ozzo.Length(0, 200).Error("location must be max 200 characters"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("location", err.Error())
		}
	}

	// Pagination
	if input.Page < 1 {
		return errors.NewValidationError("page", "must be greater than 0")
	}
	if input.Limit < 1 || input.Limit > h.config.MaxLimit {
		return errors.NewValidationError("limit", fmt.Sprintf("must be between 1 and %d", h.config.MaxLimit))
	}

	// Investment range
	if input.MinInvestment > 0 && input.MaxInvestment > 0 && input.MinInvestment > input.MaxInvestment {
		return errors.NewValidationError("min_investment", "cannot be greater than max_investment")
	}

	return nil
}

func (h *Handler) sanitizeInput(input *Input) {
	if input.Query != "" {
		input.Query = h.sanitizer.SanitizeString(input.Query)
	}
	if input.Category != "" {
		input.Category = h.sanitizer.SanitizeString(input.Category)
	}
	if input.Industry != "" {
		input.Industry = h.sanitizer.SanitizeString(input.Industry)
	}
	if input.Location != "" {
		input.Location = h.sanitizer.SanitizeString(input.Location)
	}
}

func (h *Handler) buildSearchRequest(input *Input) (*SearchRequest, error) {
	query := map[string]interface{}{
		"bool": map[string]interface{}{
			"must": []map[string]interface{}{},
		},
	}

	mustClauses := query["bool"].(map[string]interface{})["must"].([]map[string]interface{})

	// ✅ INDUSTRY FILTER (TOP LEVEL OBJECT)
	if input.Industry != "" {
		industryFilter := map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"industry.slug": strings.ToLower(input.Industry),
						},
					},
					{
						"match": map[string]interface{}{
							"industry.name": input.Industry,
						},
					},
				},
				"minimum_should_match": 1,
			},
		}
		mustClauses = append(mustClauses, industryFilter)
	}

	// ✅ CATEGORY FILTER (NESTED)
	if input.Category != "" {
		categoryFilter := map[string]interface{}{
			"nested": map[string]interface{}{
				"path": "categories",
				"query": map[string]interface{}{
					"bool": map[string]interface{}{
						"should": []map[string]interface{}{
							{
								"term": map[string]interface{}{
									"categories.slug": strings.ToLower(input.Category),
								},
							},
							{
								"match": map[string]interface{}{
									"categories.name": input.Category,
								},
							},
						},
						"minimum_should_match": 1,
					},
				},
			},
		}
		mustClauses = append(mustClauses, categoryFilter)
	}

	// ✅ TEXT SEARCH (MULTI-MATCH)
	if input.Query != "" {
		multiMatch := map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":     input.Query,
				"fields":    []string{"name^3", "description^2", "tags"},
				"type":      "best_fields",
				"fuzziness": h.config.Fuzziness,
			},
		}
		mustClauses = append(mustClauses, multiMatch)
	}

	// ✅ LOCATION FILTER (keyword field)
	if input.Location != "" {
		locationFilter := map[string]interface{}{
			"term": map[string]interface{}{
				"location": strings.ToLower(input.Location),
			},
		}
		mustClauses = append(mustClauses, locationFilter)
	}

	// ✅ INVESTMENT RANGE
	if input.MinInvestment > 0 || input.MaxInvestment > 0 {
		rangeFilter := map[string]interface{}{}
		if input.MinInvestment > 0 {
			rangeFilter["gte"] = input.MinInvestment
		}
		if input.MaxInvestment > 0 {
			rangeFilter["lte"] = input.MaxInvestment
		}
		mustClauses = append(mustClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"investment.min_investment": rangeFilter,
			},
		})
	}

	// ✅ SPACE RANGE
	if input.MinSpace > 0 || input.MaxSpace > 0 {
		rangeFilter := map[string]interface{}{}
		if input.MinSpace > 0 {
			rangeFilter["gte"] = input.MinSpace
		}
		if input.MaxSpace > 0 {
			rangeFilter["lte"] = input.MaxSpace
		}
		mustClauses = append(mustClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"space.min_space": rangeFilter,
			},
		})
	}

	// ✅ RATING FILTER
	if input.MinRating > 0 {
		mustClauses = append(mustClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"rating": map[string]interface{}{
					"gte": input.MinRating,
				},
			},
		})
	}

	query["bool"].(map[string]interface{})["must"] = mustClauses

	// ✅ SORTING
	var sort []map[string]interface{}
	sortOrder := "desc"
	if input.SortOrder != "" {
		sortOrder = input.SortOrder
	}

	switch input.SortBy {
	case "rating":
		sort = append(sort, map[string]interface{}{
			"rating": map[string]interface{}{"order": sortOrder, "missing": "_last"},
		})
	case "investment":
		sort = append(sort, map[string]interface{}{
			"investment.min_investment": map[string]interface{}{"order": sortOrder},
		})
	case "year":
		sort = append(sort, map[string]interface{}{
			"year_of_establishment": map[string]interface{}{"order": sortOrder},
		})
	default:
		sort = append(sort, map[string]interface{}{
			"_score": map[string]interface{}{"order": "desc"},
		})
	}

	// ✅ AGGREGATIONS (matching new structure)
	var aggs map[string]interface{}
	if h.config.EnableAggregations {
		aggs = map[string]interface{}{
			"by_industry": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "industry.slug",
					"size":  10,
				},
			},
			"by_category": map[string]interface{}{
				"nested": map[string]interface{}{
					"path": "categories",
				},
				"aggs": map[string]interface{}{
					"category_slugs": map[string]interface{}{
						"terms": map[string]interface{}{
							"field": "categories.slug",
							"size":  20,
						},
					},
				},
			},
			"by_location": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "location",
					"size":  20,
				},
			},
			"by_rating": map[string]interface{}{
				"histogram": map[string]interface{}{
					"field":         "rating",
					"interval":      1,
					"min_doc_count": 1,
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

func (h *Handler) executeSearch(ctx context.Context, request *SearchRequest) ([]map[string]interface{}, int, error) {
	span := trace.SpanFromContext(ctx)

	fullQuery := map[string]interface{}{
		"query": request.Query,
		"from":  request.From,
		"size":  request.Size,
		"sort":  request.Sort,
	}

	if request.Aggregations != nil {
		fullQuery["aggs"] = request.Aggregations
	}

	// ✅ SEARCH franchise_listings INDEX
	result, err := h.esClient.SearchDocuments(ctx, ListingsIndex, fullQuery)
	if err != nil {
		span.RecordError(err)
		return nil, 0, fmt.Errorf("search failed: %w", err)
	}

	var allResults []map[string]interface{}
	totalCount := 0

	if hits, ok := result["hits"].(map[string]interface{}); ok {
		if totalVal, ok := hits["total"].(map[string]interface{}); ok {
			if val, ok := totalVal["value"].(float64); ok {
				totalCount = int(val)
			}
		}

		if hitsList, ok := hits["hits"].([]interface{}); ok {
			for _, hit := range hitsList {
				if hitMap, ok := hit.(map[string]interface{}); ok {
					if source, ok := hitMap["_source"].(map[string]interface{}); ok {
						source["_id"] = hitMap["_id"]
						source["_score"] = hitMap["_score"]
						allResults = append(allResults, source)
					}
				}
			}
		}
	}

	span.SetAttributes(
		attribute.Int("results_count", len(allResults)),
		attribute.Int("total_count", totalCount),
	)

	return allResults, totalCount, nil
}

func (h *Handler) parseInput(job entities.Job) (*Input, error) {
	var vars map[string]interface{}
	if err := json.Unmarshal([]byte(job.Variables), &vars); err != nil {
		return nil, fmt.Errorf("failed to unmarshal variables: %w", err)
	}

	input := &Input{
		Page:  1,
		Limit: h.config.DefaultLimit,
	}

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
	if page, ok := vars["page"].(float64); ok {
		input.Page = int(page)
	}
	if limit, ok := vars["limit"].(float64); ok {
		input.Limit = int(limit)
		if input.Limit > h.config.MaxLimit {
			input.Limit = h.config.MaxLimit
		}
	}
	if sortBy, ok := vars["sort_by"].(string); ok {
		input.SortBy = sortBy
	}
	if sortOrder, ok := vars["sort_order"].(string); ok {
		input.SortOrder = sortOrder
	}

	return input, nil
}

func (h *Handler) prepareOutput(results []map[string]interface{}, totalCount int, queryTime int64, input *Input) *Output {
	return &Output{
		Franchises:  results,
		TotalCount:  totalCount,
		QueryTimeMs: queryTime,
		AppliedFilters: map[string]interface{}{
			"query":          input.Query,
			"category":       input.Category,
			"industry":       input.Industry,
			"location":       input.Location,
			"min_investment": input.MinInvestment,
			"max_investment": input.MaxInvestment,
			"page":           input.Page,
			"limit":          input.Limit,
		},
		Success: true,
	}
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	variables := map[string]interface{}{
		"search_results": output.Franchises,
		"total_count":    output.TotalCount,
		"query_time_ms":  output.QueryTimeMs,
		"success":        output.Success,
	}

	request, err := client.NewCompleteJobCommand().JobKey(job.GetKey()).VariablesFromMap(variables)
	if err != nil {
		h.logger.Error("Failed to create complete job command", map[string]interface{}{"error": err.Error()})
		return
	}

	_, err = request.Send(ctx)
	if err != nil {
		h.logger.Error("Failed to complete job", map[string]interface{}{"error": err.Error()})
	}
}
