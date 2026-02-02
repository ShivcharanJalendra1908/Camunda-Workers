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

// package searchfranchises

// import (
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"strings"
// 	"time"

// 	"camunda-workers/internal/common/database"
// 	"camunda-workers/internal/common/errors"
// 	"camunda-workers/internal/common/logger"
// 	"camunda-workers/internal/common/validation"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/zbc"

// 	ozzo "github.com/go-ozzo/ozzo-validation/v4"

// 	"go.opentelemetry.io/otel"
// 	"go.opentelemetry.io/otel/attribute"
// 	"go.opentelemetry.io/otel/trace"
// )

// const (
// 	TaskType = "search-franchises"
// )

// type Handler struct {
// 	config       *Config
// 	logger       logger.Logger
// 	esClient     *database.ElasticsearchClient
// 	errorHandler *errors.ErrorHandler
// 	validator    *validation.Validator
// 	sanitizer    *validation.Sanitizer
// }

// func NewHandler(config *Config, esClient *database.ElasticsearchClient, log logger.Logger) *Handler {
// 	return &Handler{
// 		config:       config,
// 		logger:       log,
// 		esClient:     esClient,
// 		errorHandler: errors.NewErrorHandler(log),
// 		validator:    validation.NewValidator(),
// 		sanitizer:    validation.NewSanitizer(),
// 	}
// }

// // ===== CRITICAL VALIDATION FUNCTION (GAP #1 FIX) =====
// func (h *Handler) validateInput(input *Input) error {
// 	// Validate Query (if provided)
// 	if input.Query != "" {
// 		if err := ozzo.Validate(input.Query,
// 			ozzo.Length(0, 500).Error("query must be max 500 characters"),
// 			validation.SafeSQLString,
// 			validation.SafeNoSQLString,
// 		); err != nil {
// 			return errors.NewValidationError("query", err.Error())
// 		}
// 	}

// 	// Validate Category (if provided)
// 	if input.Category != "" {
// 		if err := ozzo.Validate(input.Category,
// 			ozzo.Length(0, 100).Error("category must be max 100 characters"),
// 			validation.SafeSQLString,
// 			validation.ValidateEnum([]string{"food", "education", "fashion", "food & beverage", "ice cream", "dessert", "training", "coaching", "apparel", "footwear", "jewellery"}),
// 		); err != nil {
// 			return errors.NewValidationError("category", err.Error())
// 		}
// 	}

// 	// Validate Location (if provided)
// 	if input.Location != "" {
// 		if err := ozzo.Validate(input.Location,
// 			ozzo.Length(0, 200).Error("location must be max 200 characters"),
// 			validation.SafeSQLString,
// 		); err != nil {
// 			return errors.NewValidationError("location", err.Error())
// 		}
// 	}

// 	// Validate pagination
// 	if input.Page < 1 {
// 		return errors.NewValidationError("page", "must be greater than 0")
// 	}

// 	if input.Limit < 1 || input.Limit > h.config.MaxLimit {
// 		return errors.NewValidationError("limit", fmt.Sprintf("must be between 1 and %d", h.config.MaxLimit))
// 	}

// 	// Validate numeric ranges
// 	if input.MinInvestment > 0 && input.MaxInvestment > 0 && input.MinInvestment > input.MaxInvestment {
// 		return errors.NewValidationError("min_investment", "cannot be greater than max_investment")
// 	}

// 	if input.MinSpace > 0 && input.MaxSpace > 0 && input.MinSpace > input.MaxSpace {
// 		return errors.NewValidationError("min_space", "cannot be greater than max_space")
// 	}

// 	if input.MinRating < 0 || input.MinRating > 5 {
// 		return errors.NewValidationError("min_rating", "must be between 0 and 5")
// 	}

// 	// Validate MinInvestment (if provided)
// 	if input.MinInvestment > 0 {
// 		if input.MinInvestment > 1000000000 { // 1 billion
// 			return errors.NewValidationError("min_investment", "cannot exceed 1,000,000,000")
// 		}
// 	}

// 	// Validate MaxInvestment (if provided)
// 	if input.MaxInvestment > 0 {
// 		if input.MaxInvestment > 1000000000 { // 1 billion
// 			return errors.NewValidationError("max_investment", "cannot exceed 1,000,000,000")
// 		}
// 	}

// 	// Validate MinSpace (if provided)
// 	if input.MinSpace > 0 {
// 		if input.MinSpace > 100000 { // 100,000 sq ft
// 			return errors.NewValidationError("min_space", "cannot exceed 100,000")
// 		}
// 	}

// 	// Validate MaxSpace (if provided)
// 	if input.MaxSpace > 0 {
// 		if input.MaxSpace > 100000 { // 100,000 sq ft
// 			return errors.NewValidationError("max_space", "cannot exceed 100,000")
// 		}
// 	}

// 	// Validate Tags array (if provided)
// 	if len(input.Tags) > 0 {
// 		if len(input.Tags) > 50 {
// 			return errors.NewArrayTooLargeError("tags", 50, len(input.Tags))
// 		}

// 		for i, tag := range input.Tags {
// 			if err := ozzo.Validate(tag,
// 				ozzo.Length(1, 50).Error("tag must be 1-50 characters"),
// 				validation.IDString,
// 				validation.SafeSQLString,
// 			); err != nil {
// 				return errors.NewValidationError(fmt.Sprintf("tags[%d]", i), err.Error())
// 			}
// 		}
// 	}

// 	// Validate SortBy (if provided)
// 	if input.SortBy != "" {
// 		if err := ozzo.Validate(input.SortBy,
// 			ozzo.Length(1, 50).Error("sort_by must be 1-50 characters"),
// 			validation.ValidateEnum([]string{"rating", "investment", "since", "_score"}),
// 			validation.SafeSQLString,
// 		); err != nil {
// 			return errors.NewValidationError("sort_by", err.Error())
// 		}
// 	}

// 	// Validate SortOrder (if provided)
// 	if input.SortOrder != "" {
// 		if err := ozzo.Validate(input.SortOrder,
// 			ozzo.Length(1, 10).Error("sort_order must be 1-10 characters"),
// 			validation.ValidateEnum([]string{"asc", "desc"}),
// 		); err != nil {
// 			return errors.NewValidationError("sort_order", err.Error())
// 		}
// 	}

// 	// Validate UserId (if provided)
// 	if input.UserId != "" {
// 		if err := ozzo.Validate(input.UserId,
// 			ozzo.Length(1, 100).Error("user_id must be 1-100 characters"),
// 			validation.SafeSQLString,
// 			validation.SafeNoSQLString,
// 		); err != nil {
// 			return errors.NewValidationError("user_id", err.Error())
// 		}
// 	}

// 	// Validate SessionId (if provided)
// 	if input.SessionId != "" {
// 		if err := ozzo.Validate(input.SessionId,
// 			ozzo.Length(1, 100).Error("session_id must be 1-100 characters"),
// 			validation.SafeSQLString,
// 			validation.SafeNoSQLString,
// 		); err != nil {
// 			return errors.NewValidationError("session_id", err.Error())
// 		}
// 	}

// 	// 🔒 ADDITIONAL SECURITY CHECKS
// 	// Prevent NoSQL injection in search fields
// 	lowerQuery := strings.ToLower(input.Query)
// 	if strings.Contains(lowerQuery, "$where") ||
// 		strings.Contains(lowerQuery, "$ne") ||
// 		strings.Contains(lowerQuery, "$gt") ||
// 		strings.Contains(lowerQuery, "script:") {
// 		return errors.NewValidationError("query", "contains potentially unsafe patterns")
// 	}

// 	// Prevent malicious patterns in location
// 	lowerLocation := strings.ToLower(input.Location)
// 	if strings.Contains(lowerLocation, "javascript:") ||
// 		strings.Contains(lowerLocation, "data:") ||
// 		strings.Contains(lowerLocation, "file://") {
// 		return errors.NewValidationError("location", "contains potentially unsafe patterns")
// 	}

// 	return nil
// }

// // ===== SANITIZE INPUT FUNCTION =====
// func (h *Handler) sanitizeInput(input *Input) {
// 	// Sanitize all string fields
// 	if input.Query != "" {
// 		input.Query = h.sanitizer.SanitizeString(input.Query)
// 	}
// 	if input.Category != "" {
// 		input.Category = h.sanitizer.SanitizeString(input.Category)
// 	}
// 	if input.Location != "" {
// 		input.Location = h.sanitizer.SanitizeString(input.Location)
// 	}
// 	if input.SortBy != "" {
// 		input.SortBy = h.sanitizer.SanitizeString(input.SortBy)
// 	}
// 	if input.SortOrder != "" {
// 		input.SortOrder = h.sanitizer.SanitizeString(input.SortOrder)
// 	}
// 	if input.UserId != "" {
// 		input.UserId = h.sanitizer.SanitizeString(input.UserId)
// 	}
// 	if input.SessionId != "" {
// 		input.SessionId = h.sanitizer.SanitizeString(input.SessionId)
// 	}

// 	// Sanitize tags
// 	if len(input.Tags) > 0 {
// 		sanitizedTags := make([]string, len(input.Tags))
// 		for i, tag := range input.Tags {
// 			sanitizedTags[i] = h.sanitizer.SanitizeString(tag)
// 		}
// 		input.Tags = sanitizedTags
// 	}
// }

// // Execute provides a direct API for searching franchises
// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	span := trace.SpanFromContext(ctx)
// 	span.AddEvent("start franchise search")

// 	h.logger.Info("Executing franchise search via direct API", map[string]interface{}{
// 		"query":    input.Query,
// 		"category": input.Category,
// 		"page":     input.Page,
// 		"limit":    input.Limit,
// 		"traceId":  trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
// 		"spanId":   trace.SpanFromContext(ctx).SpanContext().SpanID().String(),
// 	})

// 	startTime := time.Now()

// 	// ===== VALIDATE INPUT =====
// 	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "search-franchises.validateInput")
// 	if err := h.validateInput(input); err != nil {
// 		span.RecordError(err)
// 		span.SetAttributes(attribute.Bool("error", true))
// 		spanValidate.End()
// 		return nil, fmt.Errorf("input validation failed: %w", err)
// 	}
// 	spanValidate.End()

// 	// ===== SANITIZE INPUT =====
// 	_, spanSanitize := otel.Tracer("worker-manager").Start(ctx, "search-franchises.sanitizeInput")
// 	h.sanitizeInput(input)
// 	spanSanitize.End()

// 	// Build Elasticsearch query
// 	_, spanBuildQuery := otel.Tracer("worker-manager").Start(ctx, "search-franchises.buildQuery")
// 	searchRequest, err := h.buildSearchRequest(input)
// 	if err != nil {
// 		span.RecordError(err)
// 		span.SetAttributes(attribute.Bool("error", true))
// 		spanBuildQuery.End()
// 		return nil, fmt.Errorf("failed to build search request: %w", err)
// 	}
// 	spanBuildQuery.End()

// 	// Determine which indices to search
// 	indices := h.getIndicesToSearch(input.Category)
// 	span.SetAttributes(attribute.StringSlice("elasticsearch.indices", indices))

// 	// Execute search across indices
// 	_, spanSearch := otel.Tracer("worker-manager").Start(ctx, "search-franchises.executeSearch")
// 	results, totalCount, err := h.executeSearch(ctx, indices, searchRequest)
// 	if err != nil {
// 		span.RecordError(err)
// 		span.SetAttributes(attribute.Bool("error", true))
// 		spanSearch.End()
// 		return nil, fmt.Errorf("search execution failed: %w", err)
// 	}
// 	spanSearch.End()

// 	// Get suggestions if enabled
// 	var suggestions []string
// 	if h.config.EnableSuggestions && input.Query != "" {
// 		_, spanSuggest := otel.Tracer("worker-manager").Start(ctx, "search-franchises.getSuggestions")
// 		suggestions = h.getSuggestions(ctx, input.Query, indices)
// 		spanSuggest.End()
// 	}

// 	// Calculate query time
// 	queryTime := time.Since(startTime).Milliseconds()

// 	// Prepare output
// 	output := h.prepareOutput(results, totalCount, queryTime, suggestions, input)

// 	h.logger.Info("Franchise search via direct API completed", map[string]interface{}{
// 		"results_count": len(results),
// 		"query_time_ms": queryTime,
// 		"total_count":   totalCount,
// 		"traceId":       trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
// 	})

// 	return output, nil
// }

// // Register registers the worker with Zeebe
// func (h *Handler) Register(client zbc.Client, jobType string) {
// 	jobWorker := client.NewJobWorker().
// 		JobType(jobType).
// 		Handler(h.HandleJob).
// 		MaxJobsActive(5).
// 		Concurrency(3).
// 		PollInterval(100 * time.Millisecond).
// 		Timeout(60 * time.Second).
// 		Open()

// 	_ = jobWorker

// 	h.logger.Info("Franchise search worker registered", map[string]interface{}{
// 		"job_type": jobType,
// 	})
// }

// func (h *Handler) HandleJob(client worker.JobClient, job entities.Job) {
// 	// ✅ EXTRACT TRACE CONTEXT
// 	ctx := context.Background()

// 	var traceID, parentSpanID string
// 	var jobVars map[string]interface{}

// 	if err := json.Unmarshal([]byte(job.Variables), &jobVars); err == nil {
// 		if tid, ok := jobVars["traceId"].(string); ok {
// 			traceID = tid
// 		}
// 		if psid, ok := jobVars["spanId"].(string); ok {
// 			parentSpanID = psid
// 		}
// 	}

// 	// ✅ CREATE WORKER SPAN
// 	tracer := otel.Tracer("worker-manager")
// 	ctx, span := tracer.Start(ctx, "worker:"+TaskType,
// 		trace.WithAttributes(
// 			attribute.String("worker.name", TaskType),
// 			attribute.Int64("job.key", job.GetKey()),
// 			attribute.Int64("workflow.instance_key", job.GetProcessInstanceKey()),
// 			attribute.String("workflow.process_id", job.GetBpmnProcessId()),
// 			attribute.String("workflow.element_id", job.GetElementId()),
// 			attribute.String("trace.parent_id", parentSpanID),
// 		),
// 	)
// 	defer span.End()

// 	h.logger.Info("Processing franchise search job", map[string]interface{}{
// 		"job_id":   job.GetKey(),
// 		"job_type": job.Type,
// 		"traceId":  traceID,
// 		"spanId":   span.SpanContext().SpanID().String(),
// 	})

// 	startTime := time.Now()

// 	// ===== STEP 1: PARSE INPUT =====
// 	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "search-franchises.parseInput")
// 	input, err := h.parseInput(job)
// 	if err != nil {
// 		span.RecordError(err)
// 		span.SetAttributes(attribute.Bool("error", true))
// 		h.logger.Error("Failed to parse input variables", map[string]interface{}{
// 			"error":   err.Error(),
// 			"traceId": traceID,
// 		})
// 		h.errorHandler.HandleJobError(ctx, client, job, errors.NewInvalidFilterFormatError("Invalid input parameters"))
// 		spanParse.End()
// 		return
// 	}
// 	spanParse.End()

// 	// ===== STEP 2: VALIDATE INPUT (GAP #1 FIX) =====
// 	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "search-franchises.validateInput")
// 	if err := h.validateInput(input); err != nil {
// 		span.RecordError(err)
// 		span.SetAttributes(attribute.Bool("error", true))
// 		h.logger.Error("Input validation failed", map[string]interface{}{
// 			"error":   err.Error(),
// 			"traceId": traceID,
// 		})
// 		h.errorHandler.HandleJobError(ctx, client, job, errors.NewInvalidFilterFormatError(err.Error()))
// 		spanValidate.End()
// 		return
// 	}
// 	spanValidate.End()

// 	// ===== STEP 3: SANITIZE INPUT =====
// 	_, spanSanitize := otel.Tracer("worker-manager").Start(ctx, "search-franchises.sanitizeInput")
// 	h.sanitizeInput(input)
// 	spanSanitize.End()

// 	// Build Elasticsearch query
// 	_, spanBuildQuery := otel.Tracer("worker-manager").Start(ctx, "search-franchises.buildQuery")
// 	searchRequest, err := h.buildSearchRequest(input)
// 	if err != nil {
// 		span.RecordError(err)
// 		span.SetAttributes(attribute.Bool("error", true))
// 		h.logger.Error("Failed to build search request", map[string]interface{}{
// 			"error":   err.Error(),
// 			"traceId": traceID,
// 		})
// 		h.errorHandler.HandleJobError(ctx, client, job, errors.NewInvalidFilterFormatError("Failed to build search query"))
// 		spanBuildQuery.End()
// 		return
// 	}
// 	spanBuildQuery.End()

// 	// Determine which indices to search
// 	indices := h.getIndicesToSearch(input.Category)
// 	span.SetAttributes(attribute.StringSlice("elasticsearch.indices", indices))

// 	// Execute search across indices
// 	_, spanSearch := otel.Tracer("worker-manager").Start(ctx, "search-franchises.executeSearch")
// 	results, totalCount, err := h.executeSearch(ctx, indices, searchRequest)
// 	if err != nil {
// 		span.RecordError(err)
// 		span.SetAttributes(attribute.Bool("error", true))
// 		h.logger.Error("Search execution failed", map[string]interface{}{
// 			"error":   err.Error(),
// 			"traceId": traceID,
// 		})
// 		h.errorHandler.HandleJobError(ctx, client, job, errors.NewSearchQueryFailedError("franchise_search", err))
// 		spanSearch.End()
// 		return
// 	}
// 	spanSearch.End()

// 	// Get suggestions if enabled
// 	var suggestions []string
// 	if h.config.EnableSuggestions && input.Query != "" {
// 		_, spanSuggest := otel.Tracer("worker-manager").Start(ctx, "search-franchises.getSuggestions")
// 		suggestions = h.getSuggestions(ctx, input.Query, indices)
// 		spanSuggest.End()
// 	}

// 	// Calculate query time
// 	queryTime := time.Since(startTime).Milliseconds()

// 	// Prepare output
// 	output := h.prepareOutput(results, totalCount, queryTime, suggestions, input)

// 	// Complete job
// 	h.completeJob(ctx, client, job, output)

// 	h.logger.Info("Franchise search job completed", map[string]interface{}{
// 		"job_id":        job.GetKey(),
// 		"results_count": len(results),
// 		"query_time_ms": queryTime,
// 		"traceId":       traceID,
// 	})
// }

// func (h *Handler) parseInput(job entities.Job) (*Input, error) {
// 	var input Input

// 	// job.Variables is a STRING in Zeebe v8, not a map
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

// func (h *Handler) getIndicesToSearch(category string) []string {
// 	switch strings.ToLower(category) {
// 	case "food", "food & beverage", "ice cream", "dessert", "yogurt", "beverage":
// 		return []string{"food_franchises"}
// 	case "education", "training", "coaching":
// 		return []string{"education_franchises"}
// 	case "fashion", "apparel", "footwear", "jewellery":
// 		return []string{"fashion_franchises"}
// 	default:
// 		return []string{
// 			"food_franchises",
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

// 	// ✅ FIXED: Category filter with wildcard matching (MUST BE FIRST!)
// 	if input.Category != "" {
// 		categoryLower := strings.ToLower(input.Category)

// 		// Build flexible category matching
// 		shouldClauses := []map[string]interface{}{}

// 		// Try exact match
// 		shouldClauses = append(shouldClauses, map[string]interface{}{
// 			"term": map[string]interface{}{
// 				"category.keyword": input.Category,
// 			},
// 		})

// 		// Add wildcard for partial matching
// 		shouldClauses = append(shouldClauses, map[string]interface{}{
// 			"wildcard": map[string]interface{}{
// 				"category": map[string]interface{}{
// 					"value":            "*" + categoryLower + "*",
// 					"case_insensitive": true,
// 				},
// 			},
// 		})

// 		categoryFilter := map[string]interface{}{
// 			"bool": map[string]interface{}{
// 				"should":               shouldClauses,
// 				"minimum_should_match": 1,
// 			},
// 		}

// 		query["bool"].(map[string]interface{})["must"] = append(
// 			query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
// 			categoryFilter,
// 		)
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

// func (h *Handler) executeSearch(ctx context.Context, indices []string, request *SearchRequest) ([]map[string]interface{}, int, error) {
// 	span := trace.SpanFromContext(ctx)
// 	var allResults []map[string]interface{}
// 	totalCount := 0

// 	for _, index := range indices {
// 		span.AddEvent("searching index", trace.WithAttributes(
// 			attribute.String("elasticsearch.index", index),
// 		))

// 		fullQuery := map[string]interface{}{
// 			"query": request.Query,
// 			"from":  request.From,
// 			"size":  request.Size,
// 			"sort":  request.Sort,
// 		}

// 		if request.Aggregations != nil {
// 			fullQuery["aggs"] = request.Aggregations
// 		}

// 		result, err := h.esClient.SearchDocuments(ctx, index, fullQuery)
// 		if err != nil {
// 			span.RecordError(err)
// 			h.logger.Warn("Search failed for index", map[string]interface{}{
// 				"index":   index,
// 				"error":   err.Error(),
// 				"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
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

// 	span.SetAttributes(
// 		attribute.Int("elasticsearch.results_count", len(allResults)),
// 		attribute.Int("elasticsearch.total_count", totalCount),
// 	)

// 	return allResults, totalCount, nil
// }

// func (h *Handler) getSuggestions(ctx context.Context, query string, indices []string) []string {
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
// 		result, err := h.esClient.SearchDocuments(ctx, index, suggestQuery)
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

// func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
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
// 		span := trace.SpanFromContext(ctx)
// 		span.RecordError(err)
// 		h.logger.Error("Failed to create complete job command", map[string]interface{}{
// 			"error":   err.Error(),
// 			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
// 		})
// 		return
// 	}

// 	_, err = request.Send(ctx)
// 	if err != nil {
// 		span := trace.SpanFromContext(ctx)
// 		span.RecordError(err)
// 		h.logger.Error("Failed to complete job", map[string]interface{}{
// 			"error":   err.Error(),
// 			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
// 		})
// 	}
// }

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
// 	"camunda-workers/internal/common/errors"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/zbc"
// )

// type Handler struct {
// 	config       *Config
// 	logger       logger.Logger
// 	esClient     *database.ElasticsearchClient
// 	errorHandler *errors.ErrorHandler
// }

// func NewHandler(config *Config, esClient *database.ElasticsearchClient, log logger.Logger) *Handler {
// 	return &Handler{
// 		config:       config,
// 		logger:       log,
// 		esClient:     esClient,
// 		errorHandler: errors.NewErrorHandler(log),
// 	}
// }

// // Execute provides a direct API for searching franchises - ADDED THIS METHOD
// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	h.logger.Info("Executing franchise search via direct API", map[string]interface{}{
// 		"query":    input.Query,
// 		"category": input.Category,
// 		"page":     input.Page,
// 		"limit":    input.Limit,
// 	})

// 	startTime := time.Now()

// 	// Validate input
// 	if err := h.validateInput(input); err != nil {
// 		return nil, fmt.Errorf("input validation failed: %w", err)
// 	}

// 	// Build Elasticsearch query
// 	searchRequest, err := h.buildSearchRequest(input)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to build search request: %w", err)
// 	}

// 	// Determine which indices to search
// 	indices := h.getIndicesToSearch(input.Category)

// 	// Execute search across indices
// 	results, totalCount, err := h.executeSearch(indices, searchRequest)
// 	if err != nil {
// 		return nil, fmt.Errorf("search execution failed: %w", err)
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

// 	h.logger.Info("Franchise search via direct API completed", map[string]interface{}{
// 		"results_count": len(results),
// 		"query_time_ms": queryTime,
// 		"total_count":   totalCount,
// 	})

// 	return output, nil
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
// 	ctx := context.Background()

// 	// Parse input variables
// 	input, err := h.parseInput(job)
// 	if err != nil {
// 		h.logger.Error("Failed to parse input variables", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 		h.errorHandler.HandleJobError(ctx, client, job, errors.NewInvalidFilterFormatError("Invalid input parameters"))
// 		return
// 	}

// 	// Validate input
// 	if err := h.validateInput(input); err != nil {
// 		h.logger.Error("Input validation failed", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 		h.errorHandler.HandleJobError(ctx, client, job, errors.NewInvalidFilterFormatError(err.Error()))
// 		return
// 	}

// 	// Build Elasticsearch query
// 	searchRequest, err := h.buildSearchRequest(input)
// 	if err != nil {
// 		h.logger.Error("Failed to build search request", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 		h.errorHandler.HandleJobError(ctx, client, job, errors.NewInvalidFilterFormatError("Failed to build search query"))
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
// 		h.errorHandler.HandleJobError(ctx, client, job, errors.NewSearchQueryFailedError("franchise_search", err))
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
