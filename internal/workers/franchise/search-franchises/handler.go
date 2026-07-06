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
			"must":   []map[string]interface{}{},
			"should": []map[string]interface{}{},
		},
	}

	mustClauses := query["bool"].(map[string]interface{})["must"].([]map[string]interface{})
	shouldClauses := query["bool"].(map[string]interface{})["should"].([]map[string]interface{})

	// ✅ INDUSTRY FILTER (TOP LEVEL OBJECT)
	if input.Industry != "" {
		industries := strings.Split(input.Industry, ",")
		var shouldTerms []map[string]interface{}
		for _, ind := range industries {
			indTrimmed := strings.TrimSpace(ind)
			if indTrimmed == "" {
				continue
			}
			shouldTerms = append(shouldTerms, 
				map[string]interface{}{
					"term": map[string]interface{}{
						"industry.slug": strings.ToLower(indTrimmed),
					},
				},
				map[string]interface{}{
					"match": map[string]interface{}{
						"industry.name": indTrimmed,
					},
				},
			)
		}
		if len(shouldTerms) > 0 {
			industryFilter := map[string]interface{}{
				"bool": map[string]interface{}{
					"should":               shouldTerms,
					"minimum_should_match": 1,
				},
			}
			if input.Query != "" {
				shouldClauses = append(shouldClauses, industryFilter)
			} else {
				mustClauses = append(mustClauses, industryFilter)
			}
		}
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
		if input.Query != "" {
			shouldClauses = append(shouldClauses, categoryFilter)
		} else {
			mustClauses = append(mustClauses, categoryFilter)
		}
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

	// ✅ LOCATION FILTER (keyword field or country field)
	if input.Location != "" {
		locationFilter := map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"location": strings.ToLower(input.Location),
						},
					},
					{
						"term": map[string]interface{}{
							"location": map[string]interface{}{
								"value":            input.Location,
								"case_insensitive": true,
							},
						},
					},
					{
						"match": map[string]interface{}{
							"location": input.Location,
						},
					},
					{
						"match": map[string]interface{}{
							"country": input.Location,
						},
					},
					{
						"term": map[string]interface{}{
							"country.keyword": map[string]interface{}{
								"value":            input.Location,
								"case_insensitive": true,
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		}
		mustClauses = append(mustClauses, locationFilter)
	}

	// ✅ INVESTMENT RANGE (Overlap Logic with Lakhs Conversion)
	if input.MinInvestment > 0 || input.MaxInvestment > 0 {
		minLakhs := input.MinInvestment
		if minLakhs >= 1000 {
			minLakhs = minLakhs / 100000
		}
		maxLakhs := input.MaxInvestment
		if maxLakhs >= 1000 {
			maxLakhs = maxLakhs / 100000
		}

		if minLakhs > 0 {
			mustClauses = append(mustClauses, map[string]interface{}{
				"range": map[string]interface{}{
					"investment.max_investment": map[string]interface{}{
						"gte": minLakhs,
					},
				},
			})
		}
		if maxLakhs > 0 {
			mustClauses = append(mustClauses, map[string]interface{}{
				"range": map[string]interface{}{
					"investment.min_investment": map[string]interface{}{
						"lte": maxLakhs,
					},
				},
			})
		}
	}

	// ✅ SPACE RANGE (Overlap Logic)
	if input.MinSpace > 0 || input.MaxSpace > 0 {
		if input.MinSpace > 0 {
			mustClauses = append(mustClauses, map[string]interface{}{
				"range": map[string]interface{}{
					"space.max_space": map[string]interface{}{
						"gte": input.MinSpace,
					},
				},
			})
		}
		if input.MaxSpace > 0 {
			mustClauses = append(mustClauses, map[string]interface{}{
				"range": map[string]interface{}{
					"space.min_space": map[string]interface{}{
						"lte": input.MaxSpace,
					},
				},
			})
		}
	}

	// ✅ SIZE RANGE
	if input.MinSize > 0 || input.MaxSize > 0 {
		rangeFilter := map[string]interface{}{}
		if input.MinSize > 0 {
			rangeFilter["gte"] = input.MinSize
		}
		if input.MaxSize > 0 {
			rangeFilter["lte"] = input.MaxSize
		}
		mustClauses = append(mustClauses, map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []map[string]interface{}{
					{
						"range": map[string]interface{}{
							"member_count": rangeFilter,
						},
					},
					{
						"range": map[string]interface{}{
							"total_outlets": rangeFilter,
						},
					},
				},
				"minimum_should_match": 1,
			},
		})
	}

	// ✅ FEE RANGE
	if input.MinFee > 0 || input.MaxFee > 0 {
		rangeFilter := map[string]interface{}{}
		if input.MinFee > 0 {
			rangeFilter["gte"] = input.MinFee
		}
		if input.MaxFee > 0 {
			rangeFilter["lte"] = input.MaxFee
		}
		mustClauses = append(mustClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"membership_fee_min": rangeFilter,
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

	// ✅ ENTITY TYPE FILTER
	if input.EntityType != "" && input.EntityType != "all" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]interface{}{
				"entity_type": input.EntityType,
			},
		})
	}

	// ✅ EXCLUSIVITY TYPE FILTER
	if input.ExclusivityType != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]interface{}{
				"exclusivity_type": input.ExclusivityType,
			},
		})
	}

	// ✅ TERRITORY SCOPE FILTER
	if input.TerritoryScope != "" {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]interface{}{
				"territory_scope": input.TerritoryScope,
			},
		})
	}

	// ✅ MIN UNITS FILTER
	if input.MinUnits > 0 {
		mustClauses = append(mustClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"total_outlets": map[string]interface{}{
					"gte": input.MinUnits,
				},
			},
		})
	}

	// ✅ LOCAL BRANDS ONLY FILTER
	if input.LocalBrandsOnly {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]interface{}{
				"country.keyword": "India",
			},
		})
	}

	// ✅ FEATURED FILTER
	if input.IsFeaturedOnly {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]interface{}{
				"is_featured": true,
			},
		})
	}

	// ✅ SPONSORED FILTER
	if input.IsSponsoredOnly {
		mustClauses = append(mustClauses, map[string]interface{}{
			"term": map[string]interface{}{
				"is_sponsored": true,
			},
		})
	}

	query["bool"].(map[string]interface{})["must"] = mustClauses
	if len(shouldClauses) > 0 {
		query["bool"].(map[string]interface{})["should"] = shouldClauses
	}

	// ✅ SORTING
	var sort []map[string]interface{}
	sortOrder := "desc"
	if input.SortOrder != "" {
		sortOrder = input.SortOrder
	}

	switch input.SortBy {
	case "rating":
		sort = append(sort, map[string]interface{}{
			"rating": map[string]interface{}{"order": sortOrder, "missing": "_last", "unmapped_type": "double"},
		})
	case "investment":
		sort = append(sort, map[string]interface{}{
			"investment.min_investment": map[string]interface{}{"order": sortOrder, "unmapped_type": "double"},
		})
	case "year":
		sort = append(sort, map[string]interface{}{
			"year_of_establishment": map[string]interface{}{"order": sortOrder, "unmapped_type": "integer"},
		})
	case "featured":
		sort = append(sort, map[string]interface{}{
			"is_featured": map[string]interface{}{"order": "desc", "unmapped_type": "boolean"},
		})
		sort = append(sort, map[string]interface{}{
			"featured_order": map[string]interface{}{"order": "asc", "unmapped_type": "integer"},
		})
		sort = append(sort, map[string]interface{}{
			"featured_start_at": map[string]interface{}{"order": "asc", "missing": "_last", "unmapped_type": "date"},
		})
		sort = append(sort, map[string]interface{}{
			"_score": map[string]interface{}{"order": "desc"},
		})
	default:
		// Default curation order: Sponsored first, then Featured, then Featured Order, then Score
		sort = append(sort, map[string]interface{}{
			"is_sponsored": map[string]interface{}{"order": "desc", "unmapped_type": "boolean"},
		})
		sort = append(sort, map[string]interface{}{
			"is_featured": map[string]interface{}{"order": "desc", "unmapped_type": "boolean"},
		})
		sort = append(sort, map[string]interface{}{
			"featured_order": map[string]interface{}{"order": "asc", "unmapped_type": "integer"},
		})
		sort = append(sort, map[string]interface{}{
			"featured_start_at": map[string]interface{}{"order": "asc", "missing": "_last", "unmapped_type": "date"},
		})
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
	if minInv, ok := getNumber(vars, "min_investment", "minInvestment"); ok {
		input.MinInvestment = minInv
	}
	if maxInv, ok := getNumber(vars, "max_investment", "maxInvestment"); ok {
		input.MaxInvestment = maxInv
	}
	if minSpace, ok := getNumber(vars, "min_space", "minSpace"); ok {
		input.MinSpace = minSpace
	}
	if maxSpace, ok := getNumber(vars, "max_space", "maxSpace"); ok {
		input.MaxSpace = maxSpace
	}
	if minSize, ok := getNumber(vars, "min_size", "minSize", "minMembers"); ok {
		input.MinSize = minSize
	}
	if maxSize, ok := getNumber(vars, "max_size", "maxSize", "maxMembers"); ok {
		input.MaxSize = maxSize
	}
	if minFee, ok := getNumber(vars, "min_fee", "minFee", "membership_fee_min"); ok {
		input.MinFee = minFee
	}
	if maxFee, ok := getNumber(vars, "max_fee", "maxFee", "membership_fee_max"); ok {
		input.MaxFee = maxFee
	}
	if minRating, ok := getNumber(vars, "min_rating", "minRating"); ok {
		input.MinRating = minRating
	}
	if page, ok := getNumber(vars, "page"); ok {
		input.Page = int(page)
	}
	if limit, ok := getNumber(vars, "limit"); ok {
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
	if entityType, ok := vars["entityType"].(string); ok {
		et := strings.ToLower(entityType)
		switch et {
		case "franchises":
			et = "franchise"
		case "associations":
			et = "association"
		case "master-franchise", "master_franchises", "master franchises", "masterfranchise":
			et = "master_franchise"
		case "all":
			et = "all"
		default:
			et = strings.TrimSuffix(et, "s")
			if et == "master-franchise" || et == "master franchise" || et == "masterfranchise" {
				et = "master_franchise"
			}
		}
		input.EntityType = et
	}

	if exclusivityType, ok := vars["exclusivityType"].(string); ok {
		input.ExclusivityType = exclusivityType
	}
	if territoryScope, ok := vars["territoryScope"].(string); ok {
		input.TerritoryScope = territoryScope
	}
	if minUnits, ok := vars["minUnits"].(float64); ok {
		input.MinUnits = int(minUnits)
	} else if minUnits, ok := vars["minUnits"].(int); ok {
		input.MinUnits = minUnits
	}
	if localBrandsOnly, ok := vars["localBrandsOnly"].(bool); ok {
		input.LocalBrandsOnly = localBrandsOnly
	} else if localBrandsOnlyStr, ok := vars["localBrandsOnly"].(string); ok {
		input.LocalBrandsOnly = (strings.ToLower(strings.TrimSpace(localBrandsOnlyStr)) == "true")
	}

	if isFeaturedOnly, ok := vars["isFeaturedOnly"].(bool); ok {
		input.IsFeaturedOnly = isFeaturedOnly
	} else if isFeaturedOnlyStr, ok := vars["isFeaturedOnly"].(string); ok {
		input.IsFeaturedOnly = (strings.ToLower(strings.TrimSpace(isFeaturedOnlyStr)) == "true")
	}

	if isSponsoredOnly, ok := vars["isSponsoredOnly"].(bool); ok {
		input.IsSponsoredOnly = isSponsoredOnly
	} else if isSponsoredOnlyStr, ok := vars["isSponsoredOnly"].(string); ok {
		input.IsSponsoredOnly = (strings.ToLower(strings.TrimSpace(isSponsoredOnlyStr)) == "true")
	}

	return input, nil
}

func (h *Handler) prepareOutput(results []map[string]interface{}, totalCount int, queryTime int64, input *Input) *Output {
	return &Output{
		SearchResults: results,
		TotalCount:  totalCount,
		QueryTimeMs: queryTime,
		AppliedFilters: map[string]interface{}{
			"query":           input.Query,
			"category":        input.Category,
			"industry":        input.Industry,
			"location":        input.Location,
			"min_investment":  input.MinInvestment,
			"max_investment":  input.MaxInvestment,
			"page":            input.Page,
			"limit":           input.Limit,
			"min_size":        input.MinSize,
			"max_size":        input.MaxSize,
			"min_fee":         input.MinFee,
			"max_fee":         input.MaxFee,
			"exclusivityType": input.ExclusivityType,
			"territoryScope":  input.TerritoryScope,
			"minUnits":        input.MinUnits,
			"localBrandsOnly": input.LocalBrandsOnly,
		},
		Success: true,
	}
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	variables := map[string]interface{}{
		"search_results": output.SearchResults,
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

func getNumber(vars map[string]interface{}, keys ...string) (float64, bool) {
	for _, key := range keys {
		if val, ok := vars[key]; ok {
			switch v := val.(type) {
			case float64:
				return v, true
			case float32:
				return float64(v), true
			case int:
				return float64(v), true
			case int64:
				return float64(v), true
			case int32:
				return float64(v), true
			}
		}
	}
	return 0, false
}
