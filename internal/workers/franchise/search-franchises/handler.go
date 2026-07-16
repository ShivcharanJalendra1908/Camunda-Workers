package searchfranchises

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/location"
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

	// Phase 1: Build exact match query (no fuzziness)
	searchRequest, err := h.buildSearchRequest(input, false)
	if err != nil {
		span.RecordError(err)
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// Execute exact match search
	results, totalCount, err := h.executeSearch(ctx, searchRequest)
	if err != nil {
		span.RecordError(err)
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// Phase 2: If exact match returned 0 results and query exists, retry with fuzzy
	if totalCount == 0 && input.Query != "" {
		h.logger.Info("Exact match returned 0 results, retrying with fuzzy", map[string]interface{}{
			"query": input.Query,
		})
		fuzzyRequest, err := h.buildSearchRequest(input, true)
		if err == nil {
			fuzzyResults, fuzzyCount, fuzzyErr := h.executeSearch(ctx, fuzzyRequest)
			if fuzzyErr == nil {
				results = fuzzyResults
				totalCount = fuzzyCount
			}
		}
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

	// Subcategory validation
	if input.Subcategory != "" {
		if err := ozzo.Validate(input.Subcategory,
			ozzo.Length(0, 100).Error("subcategory must be max 100 characters"),
			validation.SafeSQLString,
		); err != nil {
			return errors.NewValidationError("subcategory", err.Error())
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
	if input.Subcategory != "" {
		input.Subcategory = h.sanitizer.SanitizeString(input.Subcategory)
	}
	if input.Industry != "" {
		input.Industry = h.sanitizer.SanitizeString(input.Industry)
	}
	if input.Location != "" {
		input.Location = h.sanitizer.SanitizeString(input.Location)
	}
}

func (h *Handler) buildSearchRequest(input *Input, useFuzzy bool) (*SearchRequest, error) {
	// ✅ NORMALIZE ENTITY TYPE (frontend may send plural forms)
	switch strings.ToLower(input.EntityType) {
	case "franchises":
		input.EntityType = "franchise"
	case "associations":
		input.EntityType = "association"
	case "master_franchises", "master-franchises":
		input.EntityType = "master_franchise"
	}

	// ✅ INFER LOCATION AND ENTITY TYPE FROM QUERY
	if input.Query != "" {
		if input.Location == "" {
			detectedCity := location.DetectCityFromQuery(input.Query)
			if detectedCity != "" {
				input.Location = detectedCity
			}
		}

		lowerQuery := strings.ToLower(input.Query)
		if input.EntityType == "" || input.EntityType == "all" {
			assocKeywords := []string{"association", "chamber", "federation", "society", "council", "forum", "consortium", "trust"}
			isAssoc := false
			for _, kw := range assocKeywords {
				if strings.Contains(lowerQuery, kw) {
					isAssoc = true
					break
				}
			}

			if isAssoc {
				input.EntityType = "association"
			} else if strings.Contains(lowerQuery, "franchise") {
				input.EntityType = "franchise"
			}
		}
	}

	query := map[string]interface{}{
		"bool": map[string]interface{}{
			"must":   []map[string]interface{}{},
			"should": []map[string]interface{}{},
		},
	}

	mustClauses := query["bool"].(map[string]interface{})["must"].([]map[string]interface{})
	shouldClauses := query["bool"].(map[string]interface{})["should"].([]map[string]interface{})

	// ✅ INDUSTRY FILTER (TOP LEVEL OBJECT)
	if input.IndustrySlug != "" {
		slugs := strings.Split(input.IndustrySlug, ",")
		var shouldTerms []map[string]interface{}
		for _, slg := range slugs {
			slugTrimmed := strings.TrimSpace(slg)
			if slugTrimmed == "" {
				continue
			}
			shouldTerms = append(shouldTerms, map[string]interface{}{
				"term": map[string]interface{}{
					"industry.slug": slugTrimmed,
				},
			})
		}
		if len(shouldTerms) > 0 {
			// Extract safe explicit words to bypass the industry filter
			stopWords := map[string]bool{"and": true, "for": true, "the": true, "with": true, "near": true, "from": true, "in": true, "at": true, "of": true, "franchise": true, "franchises": true, "association": true, "associations": true}
			
			// Also add industry words to stopWords to prevent them from bypassing the filter
			if input.Industry != "" {
				inds := strings.Split(input.Industry, ",")
				for _, ind := range inds {
					indWords := strings.Fields(strings.ToLower(strings.TrimSpace(ind)))
					for _, w := range indWords {
						if len(w) > 2 {
							stopWords[w] = true
						}
					}
				}
			}

			words := strings.Fields(strings.ToLower(input.Query))
			var safeWords []string
			for _, w := range words {
				if len(w) > 2 && !stopWords[w] {
					// Also check if it starts with any industry word (e.g., 'healthcare' starts with 'health')
					isIndustryPrefix := false
					if input.Industry != "" {
						inds := strings.Split(input.Industry, ",")
						for _, ind := range inds {
							indWords := strings.Fields(strings.ToLower(strings.TrimSpace(ind)))
							for _, indW := range indWords {
								if len(indW) > 2 && strings.HasPrefix(w, indW) {
									isIndustryPrefix = true
									break
								}
							}
						}
					}
					if !isIndustryPrefix {
						safeWords = append(safeWords, w)
					}
				}
			}

			industryFilter := map[string]interface{}{
				"bool": map[string]interface{}{
					"should": []interface{}{
						map[string]interface{}{
							"bool": map[string]interface{}{
								"should":               shouldTerms,
								"minimum_should_match": 1,
							},
						},
					},
					"minimum_should_match": 1,
				},
			}

			if len(safeWords) > 0 {
				industryFilter["bool"].(map[string]interface{})["should"] = append(
					industryFilter["bool"].(map[string]interface{})["should"].([]interface{}),
					map[string]interface{}{
						"terms": map[string]interface{}{
							"slug": safeWords,
						},
					},
				)
			}

			mustClauses = append(mustClauses, industryFilter)
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
		mustClauses = append(mustClauses, categoryFilter)
	}

	// ✅ SUBCATEGORY FILTER (NESTED)
	if input.Subcategory != "" {
		subcategoryFilter := map[string]interface{}{
			"nested": map[string]interface{}{
				"path": "sub_categories",
				"query": map[string]interface{}{
					"bool": map[string]interface{}{
						"should": []map[string]interface{}{
							{
								"term": map[string]interface{}{
									"sub_categories.slug": strings.ToLower(input.Subcategory),
								},
							},
							{
								"match": map[string]interface{}{
									"sub_categories.name": input.Subcategory,
								},
							},
						},
						"minimum_should_match": 1,
					},
				},
			},
		}
		mustClauses = append(mustClauses, subcategoryFilter)
	}

	// ✅ TEXT SEARCH (MULTI-MATCH)
	if input.Query != "" {
		cleanQuery := input.Query
		detectedCity := location.DetectCityFromQuery(input.Query)
		if detectedCity != "" {
			stripped := location.StripLocationFromQuery(input.Query)
			if strings.TrimSpace(stripped) == "" {
				cleanQuery = input.Query
			} else {
				cleanQuery = stripped
			}
			// If AI extraction was skipped and input.Location is empty, populate it!
			if input.Location == "" {
				input.Location = detectedCity
			}
		}

		// Strip entity keywords
		cleanQuery = strings.ToLower(cleanQuery)
		entityPatterns := []string{`master\s+franchises?`, `franchises?`, `associations?`, `businesses?`, `brands?`}
		for _, pattern := range entityPatterns {
			re := regexp.MustCompile(`(?i)\b` + pattern + `\b`)
			cleanQuery = re.ReplaceAllString(cleanQuery, "")
		}

		// Strip common prepositions and conjunctions
		prepositions := []string{
			"in", "at", "for", "near", "from", "within", "across", "around", "of", "the", "a", "an", "to", "with", "by", "on",
			"and", "or", "is", "are", "than", "more", "less", "under", "above", "between", "up", "down", "which", "who", "what", "where", "why", "how",
		}
		words := strings.Fields(cleanQuery)
		var filteredWords []string
		for _, w := range words {
			isPreposition := false
			for _, p := range prepositions {
				if w == p {
					isPreposition = true
					break
				}
			}
			if !isPreposition {
				filteredWords = append(filteredWords, w)
			}
		}
		cleanQuery = strings.TrimSpace(strings.Join(filteredWords, " "))

		// Strip mapped industry/category names from text search to avoid forcing them in text match
		if input.Industry != "" {
			inds := strings.Split(input.Industry, ",")
			for _, ind := range inds {
				indTrimmed := strings.TrimSpace(ind)
				if indTrimmed != "" {
					indWords := strings.Fields(strings.ToLower(indTrimmed))
					for _, w := range indWords {
						if len(w) > 2 {
							// Use [a-z]* to match variations like 'health' -> 'healthcare'
							re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(w) + `[a-z]*\b`)
							cleanQuery = re.ReplaceAllString(cleanQuery, "")
						}
					}
				}
			}
		}
		if input.Category != "" {
			cats := strings.Split(input.Category, ",")
			for _, cat := range cats {
				catTrimmed := strings.TrimSpace(cat)
				if catTrimmed != "" {
					catWords := strings.Fields(strings.ToLower(catTrimmed))
					for _, w := range catWords {
						if len(w) > 2 {
							re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(w) + `\b`)
							cleanQuery = re.ReplaceAllString(cleanQuery, "")
						}
					}
				}
			}
		}
		
		cleanQuery = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(cleanQuery, " "))



		if cleanQuery != "" {
			shouldQueries := []map[string]interface{}{
				// 1. Exact match on slug (for acronyms like ima, ficci)
				{
					"term": map[string]interface{}{
						"slug": map[string]interface{}{
							"value": strings.ToLower(cleanQuery),
							"boost": 50,
						},
					},
				},
				// 2. Exact phrase match on name
				{
					"match_phrase": map[string]interface{}{
						"name": map[string]interface{}{
							"query": cleanQuery,
							"boost": 20,
						},
					},
				},
				// 3. Exact match on name, tags, industry (no fuzziness)
				{
					"multi_match": map[string]interface{}{
						"query":                cleanQuery,
						"fields":               []string{"name^3", "description^2", "tags^3", "industry.name^2"},
						"type":                 "best_fields",
						"minimum_should_match": "2<70%",
					},
				},
				// 4. Categories nested match (no fuzziness)
				{
					"nested": map[string]interface{}{
						"path": "categories",
						"query": map[string]interface{}{
							"match": map[string]interface{}{
								"categories.name": cleanQuery,
							},
						},
					},
				},
				// 5. Subcategories nested match (no fuzziness)
				{
					"nested": map[string]interface{}{
						"path": "sub_categories",
						"query": map[string]interface{}{
							"match": map[string]interface{}{
								"sub_categories.name": cleanQuery,
							},
						},
					},
				},
			}

			// Phase 2 only: add fuzzy match queries
			if useFuzzy {
				shouldQueries = append(shouldQueries,
					map[string]interface{}{
						"multi_match": map[string]interface{}{
							"query":                cleanQuery,
							"fields":               []string{"name^3", "description", "tags^2", "industry.name^2"},
							"type":                 "best_fields",
							"fuzziness":            "AUTO",
							"minimum_should_match": "2<70%",
						},
					},
					map[string]interface{}{
						"nested": map[string]interface{}{
							"path": "categories",
							"query": map[string]interface{}{
								"match": map[string]interface{}{
									"categories.name": map[string]interface{}{
										"query":     cleanQuery,
										"fuzziness": "AUTO",
									},
								},
							},
						},
					},
					map[string]interface{}{
						"nested": map[string]interface{}{
							"path": "sub_categories",
							"query": map[string]interface{}{
								"match": map[string]interface{}{
									"sub_categories.name": map[string]interface{}{
										"query":     cleanQuery,
										"fuzziness": "AUTO",
									},
								},
							},
						},
					},
				)
			}

			textMatch := map[string]interface{}{
				"bool": map[string]interface{}{
					"should":               shouldQueries,
					"minimum_should_match": 1,
				},
			}
			mustClauses = append(mustClauses, textMatch)
		}
	}

	// ✅ LOCATION FILTER (strict must filter with smart regions)
	if input.Location != "" {
		normalizedLocation := input.Location
		if extracted := location.DetectCityFromQuery(input.Location); extracted != "" {
			normalizedLocation = extracted
		}

		locationTerms := location.BuildLocationTerms(normalizedLocation)
		var exactTerms []string
		for _, t := range locationTerms {
			exactTerms = append(exactTerms, t)
			exactTerms = append(exactTerms, strings.ToLower(t))
			exactTerms = append(exactTerms, strings.ToUpper(t))
			exactTerms = append(exactTerms, strings.Title(strings.ToLower(t)))
		}

		locationFilter := map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []map[string]interface{}{
					{
						"terms": map[string]interface{}{
							"location.keyword": exactTerms,
						},
					},
					{
						"terms": map[string]interface{}{
							"locations": exactTerms,
						},
					},
					{
						"terms": map[string]interface{}{
							"country.keyword": exactTerms,
						},
					},
					{
						"match": map[string]interface{}{
							"location": normalizedLocation,
						},
					},
					{
						"match": map[string]interface{}{
							"locations": normalizedLocation,
						},
					},
					{
						"match": map[string]interface{}{
							"country": normalizedLocation,
						},
					},
				},
				"minimum_should_match": 1,
			},
		}
		shouldClauses = append(shouldClauses, map[string]interface{}{
			"constant_score": map[string]interface{}{
				"filter": locationFilter,
				"boost": 10,
			},
		})

		// Also add description/name match as soft boost for city relevance
		shouldClauses = append(shouldClauses, map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  normalizedLocation,
				"fields": []string{"description", "name"},
				"boost":  5,
			},
		})
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
			shouldClauses = append(shouldClauses, map[string]interface{}{
				"constant_score": map[string]interface{}{
					"filter": map[string]interface{}{
						"range": map[string]interface{}{
							"investment.max_investment": map[string]interface{}{
								"gte": minLakhs,
							},
						},
					},
					"boost": 20,
				},
			})
		}
		if maxLakhs > 0 {
			shouldClauses = append(shouldClauses, map[string]interface{}{
				"constant_score": map[string]interface{}{
					"filter": map[string]interface{}{
						"range": map[string]interface{}{
							"investment.min_investment": map[string]interface{}{
								"lte": maxLakhs,
							},
						},
					},
					"boost": 20,
				},
			})
		}
	}

	// ✅ SPACE RANGE (Overlap Logic) - SOFT BOOST
	if input.MinSpace > 0 || input.MaxSpace > 0 {
		if input.MinSpace > 0 {
			shouldClauses = append(shouldClauses, map[string]interface{}{
				"constant_score": map[string]interface{}{
					"filter": map[string]interface{}{
						"range": map[string]interface{}{
							"space.max_space": map[string]interface{}{
								"gte": input.MinSpace,
							},
						},
					},
					"boost": 15,
				},
			})
		}
		if input.MaxSpace > 0 {
			shouldClauses = append(shouldClauses, map[string]interface{}{
				"constant_score": map[string]interface{}{
					"filter": map[string]interface{}{
						"range": map[string]interface{}{
							"space.min_space": map[string]interface{}{
								"lte": input.MaxSpace,
							},
						},
					},
					"boost": 15,
				},
			})
		}
	}

	// ✅ SIZE RANGE - SOFT BOOST
	if input.MinSize > 0 || input.MaxSize > 0 {
		rangeFilter := map[string]interface{}{}
		if input.MinSize > 0 {
			rangeFilter["gte"] = input.MinSize
		}
		if input.MaxSize > 0 {
			rangeFilter["lte"] = input.MaxSize
		}
		shouldClauses = append(shouldClauses, map[string]interface{}{
			"constant_score": map[string]interface{}{
				"filter": map[string]interface{}{
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
				},
				"boost": 10,
			},
		})
	}

	// ✅ FEE RANGE (Overlap Logic for membership_fee_min & max) - SOFT BOOST
	if input.MinFee > 0 || input.MaxFee > 0 {
		if input.MinFee > 0 {
			shouldClauses = append(shouldClauses, map[string]interface{}{
				"constant_score": map[string]interface{}{
					"filter": map[string]interface{}{
						"range": map[string]interface{}{
							"membership_fee_max": map[string]interface{}{
								"gte": input.MinFee,
							},
						},
					},
					"boost": 15,
				},
			})
		}
		if input.MaxFee > 0 {
			shouldClauses = append(shouldClauses, map[string]interface{}{
				"constant_score": map[string]interface{}{
					"filter": map[string]interface{}{
						"range": map[string]interface{}{
							"membership_fee_min": map[string]interface{}{
								"lte": input.MaxFee,
							},
						},
					},
					"boost": 15,
				},
			})
		}
	}

	// ✅ RATING FILTER - SOFT BOOST
	if input.MinRating > 0 {
		shouldClauses = append(shouldClauses, map[string]interface{}{
			"constant_score": map[string]interface{}{
				"filter": map[string]interface{}{
					"range": map[string]interface{}{
						"rating": map[string]interface{}{
							"gte": input.MinRating,
						},
					},
				},
				"boost": 10,
			},
		})
	}

	// ✅ ENTITY TYPE FILTER
	if input.EntityType != "" && input.EntityType != "all" {
		if input.EntityType == "franchise" {
			mustClauses = append(mustClauses, map[string]interface{}{
				"bool": map[string]interface{}{
					"should": []map[string]interface{}{
						{
							"term": map[string]interface{}{
								"entity_type": "franchise",
							},
						},
						{
							"bool": map[string]interface{}{
								"must_not": map[string]interface{}{
									"exists": map[string]interface{}{
										"field": "entity_type",
									},
								},
							},
						},
					},
					"minimum_should_match": 1,
				},
			})
		} else {
			mustClauses = append(mustClauses, map[string]interface{}{
				"term": map[string]interface{}{
					"entity_type": input.EntityType,
				},
			})
		}
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
	if industrySlug, ok := vars["industrySlug"].(string); ok {
		input.IndustrySlug = industrySlug
	}
	if searchParams, ok := vars["searchParams"].(map[string]interface{}); ok {
		if indSlug, ok := searchParams["industrySlugAll"].(string); ok && indSlug != "" {
			input.IndustrySlug = indSlug
		} else if indSlug, ok := searchParams["industrySlug"].(string); ok && indSlug != "" {
			input.IndustrySlug = indSlug
		}
		if ind, ok := searchParams["industry"].(string); ok && ind != "" && input.Industry == "" {
			input.Industry = ind
		}
	}

	// If IndustrySlug is somehow empty (e.g. not mapped in BPMN) but Industry is provided, dynamically generate it
	if input.IndustrySlug == "" && input.Industry != "" {
		parts := strings.Split(input.Industry, ",")
		var slugs []string
		for _, part := range parts {
			partTrimmed := strings.TrimSpace(part)
			if partTrimmed == "" {
				continue
			}
			slug := strings.ToLower(partTrimmed)
			slug = strings.ReplaceAll(slug, " & ", "-")
			slug = strings.ReplaceAll(slug, " / ", "-")
			slug = strings.ReplaceAll(slug, "&", "")
			slug = strings.ReplaceAll(slug, "/", "")
			slug = strings.ReplaceAll(slug, ",", "")
			slug = strings.ReplaceAll(slug, " ", "-")
			for strings.Contains(slug, "--") {
				slug = strings.ReplaceAll(slug, "--", "-")
			}
			slugs = append(slugs, slug)
		}
		input.IndustrySlug = strings.Join(slugs, ",")
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

	// Also check parsedFilters.investmentRange from parse-search-filters
	if parsedFilters, ok := vars["parsedFilters"].(map[string]interface{}); ok {
		if invRange, ok := parsedFilters["investmentRange"].(map[string]interface{}); ok {
			if min, ok := getNumber(invRange, "min"); ok && min > 0 && input.MinInvestment == 0 {
				input.MinInvestment = min
			}
			if max, ok := getNumber(invRange, "max"); ok && max > 0 && input.MaxInvestment == 0 {
				input.MaxInvestment = max
			}
		}
	}

	// Also check filters map from SearchFranchises
	if filters, ok := vars["filters"].(map[string]interface{}); ok {
		if min, ok := getNumber(filters, "minInvestment", "min_investment"); ok && min > 0 && input.MinInvestment == 0 {
			input.MinInvestment = min
		}
		if max, ok := getNumber(filters, "maxInvestment", "max_investment"); ok && max > 0 && input.MaxInvestment == 0 {
			input.MaxInvestment = max
		}
	}
	// ✅ READ AI-EXTRACTED PARAMETERS (from ai-search worker output)
	// The ai-search worker extracts location, investment, industry, etc. from the
	// natural language query and stores them in extractedParams. We use these as
	// fallback when the explicit filter variables are not set.
	if ep, ok := vars["extractedParams"].(map[string]interface{}); ok {
		if input.Location == "" {
			if loc, ok := ep["location"].(string); ok && loc != "" {
				input.Location = loc
			}
		}
		if input.Industry == "" {
			if ind, ok := ep["industry"].(string); ok && ind != "" {
				input.Industry = ind
			}
		}
		if input.IndustrySlug == "" {
			if slug, ok := ep["industrySlug"].(string); ok && slug != "" {
				input.IndustrySlug = slug
			}
			if slugAll, ok := ep["industrySlugAll"].(string); ok && slugAll != "" {
				input.IndustrySlug = slugAll
			}
		}
		if input.Category == "" {
			if cat, ok := ep["category"].(string); ok && cat != "" {
				input.Category = cat
			}
		}
		if input.MinInvestment == 0 {
			if min, ok := getNumber(ep, "minInvestment"); ok && min > 0 {
				input.MinInvestment = min
			}
		}
		if input.MaxInvestment == 0 {
			if max, ok := getNumber(ep, "maxInvestment"); ok && max > 0 {
				input.MaxInvestment = max
			}
		}
		if input.MinSpace == 0 {
			if min, ok := getNumber(ep, "minSpace"); ok && min > 0 {
				input.MinSpace = min
			}
		}
		if input.MaxSpace == 0 {
			if max, ok := getNumber(ep, "maxSpace"); ok && max > 0 {
				input.MaxSpace = max
			}
		}
		if input.MinRating == 0 {
			if r, ok := getNumber(ep, "minRating"); ok && r > 0 {
				input.MinRating = r
			}
		}
		if input.MinSize == 0 {
			if min, ok := getNumber(ep, "minMembers", "minSize"); ok && min > 0 {
				input.MinSize = min
			}
		}
		if input.MaxSize == 0 {
			if max, ok := getNumber(ep, "maxMembers", "maxSize"); ok && max > 0 {
				input.MaxSize = max
			}
		}
		if input.MinFee == 0 {
			if min, ok := getNumber(ep, "minFee", "membership_fee_min"); ok && min > 0 {
				input.MinFee = min
			}
		}
		if input.MaxFee == 0 {
			if max, ok := getNumber(ep, "maxFee", "membership_fee_max"); ok && max > 0 {
				input.MaxFee = max
			}
		}
		if input.EntityType == "" || input.EntityType == "all" {
			if et, ok := ep["entityType"].(string); ok && et != "" && et != "all" {
				input.EntityType = et
			}
		}
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
