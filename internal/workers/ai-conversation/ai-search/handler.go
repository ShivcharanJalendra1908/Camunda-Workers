// ============================================================
// FILE: internal/workers/ai-conversation/ai-search/handler.go
// FINAL VERSION: All parameters + robust error handling
// ============================================================

package ai_search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/metrics"
)

// Handler processes natural language search queries
type Handler struct {
	config         *Config
	llmService     *OllamaService
	esClient       *database.ElasticsearchClient
	logger         logger.Logger
	paramExtractor *ParameterExtractor
}

func NewHandler(
	config *Config,
	esClient *database.ElasticsearchClient,
	log logger.Logger,
) *Handler {
	return &Handler{
		config:         config,
		llmService:     NewOllamaService(config, log),
		esClient:       esClient,
		logger:         log,
		paramExtractor: NewParameterExtractor(config),
	}
}

// ============================================================
// OLLAMA LLM SERVICE
// ============================================================

type OllamaService struct {
	endpoint    string
	model       string
	maxTokens   int
	temperature float64
	httpClient  *http.Client
	logger      logger.Logger
}

type OllamaRequest struct {
	Model     string                 `json:"model"`
	Prompt    string                 `json:"prompt"`
	Stream    bool                   `json:"stream"`
	Options   map[string]interface{} `json:"options,omitempty"`
	Format    string                 `json:"format,omitempty"`
	KeepAlive string                 `json:"keep_alive,omitempty"`
}

type OllamaResponse struct {
	Model    string `json:"model"`
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

func NewOllamaService(config *Config, log logger.Logger) *OllamaService {
	return &OllamaService{
		endpoint:    config.LLMEndpoint,
		model:       config.LLMModel,
		maxTokens:   config.LLMMaxTokens,
		temperature: config.LLMTemperature,
		httpClient: &http.Client{
			Timeout: config.LLMTimeout,
		},
		logger: log,
	}
}

func (s *OllamaService) Extract(ctx context.Context, prompt string) (string, error) {
	s.logger.Debug("Calling Ollama API", map[string]interface{}{
		"model":      s.model,
		"endpoint":   s.endpoint,
		"prompt_len": len(prompt),
		"timeout":    s.httpClient.Timeout.Seconds(),
	})

	reqBody := OllamaRequest{
		Model:  s.model,
		Prompt: prompt,
		Stream: false,
		Format: "json",
		Options: map[string]interface{}{
			"temperature": s.temperature,
			"num_predict": s.maxTokens,
		},
		KeepAlive: "5m",
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.endpoint+"/api/generate", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("request creation failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read failed: %w", err)
	}

	var ollamaResp OllamaResponse
	if err := json.Unmarshal(body, &ollamaResp); err != nil {
		return "", fmt.Errorf("parse failed: %w", err)
	}

	s.logger.Debug("LLM response received", map[string]interface{}{
		"response_len": len(ollamaResp.Response),
		"done":         ollamaResp.Done,
	})

	return ollamaResp.Response, nil
}

// ============================================================
// MAIN HANDLER LOGIC
// ============================================================

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	ctx := context.Background()
	startTime := time.Now()

	h.logger.Info("AI search job started", map[string]interface{}{
		"job_key":    job.Key,
		"process_id": job.ProcessInstanceKey,
		"index":      h.config.IndexName,
	})

	// Parse input
	input, err := h.parseInput(job)
	if err != nil {
		h.handleError(client, job, err, "INPUT_PARSE_ERROR")
		return
	}

	// Validate
	if err := h.validateInput(input); err != nil {
		h.handleError(client, job, err, "VALIDATION_ERROR")
		return
	}

	// Extract parameters using LLM (with fallback)
	params := h.extractParametersWithFallback(ctx, input)

	// Build ES query
	esQuery, err := h.buildElasticsearchQuery(params)
	if err != nil {
		h.handleError(client, job, err, "QUERY_BUILD_ERROR")
		return
	}

	// Execute search
	results, err := h.executeSearch(ctx, esQuery)
	if err != nil {
		h.handleError(client, job, err, "SEARCH_EXECUTION_ERROR")
		return
	}

	// Build response
	response := h.buildResponse(input, params, results)

	// Complete job
	if err := h.completeJob(client, job, response); err != nil {
		h.logger.Error("Failed to complete job", map[string]interface{}{
			"job_key": job.Key,
			"error":   err.Error(),
		})
		return
	}

	// Record metrics
	duration := time.Since(startTime)
	metrics.WorkerJobsCompleted.WithLabelValues("ai-search-franchise").Inc()
	metrics.WorkerJobDuration.WithLabelValues("ai-search-franchise").Observe(duration.Seconds())

	h.logger.Info("AI search completed", map[string]interface{}{
		"job_key": job.Key,
		"results": results.Total,
		"took_ms": duration.Milliseconds(),
	})
}

func (h *Handler) parseInput(job entities.Job) (*SearchInput, error) {
	var input SearchInput
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		return nil, fmt.Errorf("parse failed: %w", err)
	}

	// Try alternative field names
	if input.Query == "" {
		var varMap map[string]interface{}
		json.Unmarshal([]byte(job.Variables), &varMap)
		for _, field := range []string{"query", "searchQuery", "search_query", "text"} {
			if val, ok := varMap[field].(string); ok && val != "" {
				input.Query = val
				break
			}
		}
	}

	return &input, nil
}

func (h *Handler) validateInput(input *SearchInput) error {
	if input == nil {
		return fmt.Errorf("input is nil")
	}

	// Allow empty queries but set a default
	if strings.TrimSpace(input.Query) == "" {
		h.logger.Warn("Empty search query received, using default", map[string]interface{}{
			"original_query": input.Query,
		})
		input.Query = "*"
	}

	if len(input.Query) > h.config.MaxQueryLength {
		return fmt.Errorf("query too long: %d chars (max %d)", len(input.Query), h.config.MaxQueryLength)
	}
	return nil
}

// ✅ NEW: Extract parameters with fallback instead of failing
func (h *Handler) extractParametersWithFallback(ctx context.Context, input *SearchInput) *ExtractedParameters {
	h.logger.Debug("Extracting parameters", map[string]interface{}{
		"query": input.Query,
	})

	// If query is wildcard, return empty params
	if input.Query == "*" || strings.TrimSpace(input.Query) == "" {
		h.logger.Info("Wildcard or empty query - returning match-all parameters", nil)
		return &ExtractedParameters{}
	}

	// Build prompt
	prompt := h.paramExtractor.BuildPrompt(input.Query)

	// Call LLM with timeout
	llmCtx, cancel := context.WithTimeout(ctx, h.config.LLMTimeout)
	defer cancel()

	response, err := h.llmService.Extract(llmCtx, prompt)
	if err != nil {
		h.logger.Warn("LLM extraction failed, using fallback", map[string]interface{}{
			"error": err.Error(),
			"query": input.Query,
		})
		// ✅ FALLBACK: Return empty params instead of failing
		return &ExtractedParameters{}
	}

	// Parse response with fallback
	params := h.paramExtractor.ParseWithFallback(response)

	h.logger.Info("Parameters extracted", map[string]interface{}{
		"category":       params.Category,
		"has_location":   params.Location != nil,
		"has_investment": params.Investment != nil,
		"has_rating":     params.Rating != nil,
		"has_space":      params.Space != nil,
		"has_staff":      params.Staff != nil,
		"has_outlets":    params.Outlets != nil,
		"has_roi":        params.ROI != nil,
	})

	return params
}

// // ✅ UPDATED: Build query with ALL parameters
// func (h *Handler) buildElasticsearchQuery(params *ExtractedParameters) (map[string]interface{}, error) {
// 	must := []interface{}{}
// 	filter := []interface{}{}
// 	hasFilters := false

// 	// ✅ CATEGORY: Search in industry.name, industry.slug, name, tags
// 	if params.Category != "" {
// 		hasFilters = true
// 		must = append(must, map[string]interface{}{
// 			"bool": map[string]interface{}{
// 				"should": []interface{}{
// 					map[string]interface{}{
// 						"match": map[string]interface{}{
// 							"industry.name": map[string]interface{}{
// 								"query": params.Category,
// 								"boost": 3.0,
// 							},
// 						},
// 					},
// 					map[string]interface{}{
// 						"match": map[string]interface{}{
// 							"industry.slug": map[string]interface{}{
// 								"query": params.Category,
// 								"boost": 2.5,
// 							},
// 						},
// 					},
// 					map[string]interface{}{
// 						"match": map[string]interface{}{
// 							"name": map[string]interface{}{
// 								"query": params.Category,
// 								"boost": 2.0,
// 							},
// 						},
// 					},
// 					map[string]interface{}{
// 						"term": map[string]interface{}{
// 							"tags": strings.ToLower(params.Category),
// 						},
// 					},
// 				},
// 				"minimum_should_match": 1,
// 			},
// 		})
// 	}

// 	// ✅ LOCATION: Match location field (string)
// 	if params.Location != nil && params.Location.City != "" {
// 		hasFilters = true
// 		filter = append(filter, map[string]interface{}{
// 			"match": map[string]interface{}{
// 				"location": params.Location.City,
// 			},
// 		})
// 	}

// 	// ✅ INVESTMENT: Range query on investment.min_investment and investment.max_investment
// 	if params.Investment != nil && (params.Investment.Min > 0 || params.Investment.Max > 0) {
// 		hasFilters = true
// 		if params.Investment.Max > 0 {
// 			filter = append(filter, map[string]interface{}{
// 				"range": map[string]interface{}{
// 					"investment.min_investment": map[string]interface{}{
// 						"lte": params.Investment.Max,
// 					},
// 				},
// 			})
// 		}
// 		if params.Investment.Min > 0 {
// 			filter = append(filter, map[string]interface{}{
// 				"range": map[string]interface{}{
// 					"investment.max_investment": map[string]interface{}{
// 						"gte": params.Investment.Min,
// 					},
// 				},
// 			})
// 		}
// 	}

// 	// ✅ RATING: Minimum rating filter
// 	if params.Rating != nil && *params.Rating > 0 {
// 		hasFilters = true
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"rating": map[string]interface{}{
// 					"gte": *params.Rating,
// 				},
// 			},
// 		})
// 	}

// 	// ✅ SPACE: Range query on space.minSpace and space.maxSpace
// 	if params.Space != nil && (params.Space.Min > 0 || params.Space.Max > 0) {
// 		hasFilters = true
// 		if params.Space.Max > 0 {
// 			filter = append(filter, map[string]interface{}{
// 				"range": map[string]interface{}{
// 					"space.minSpace": map[string]interface{}{
// 						"lte": params.Space.Max,
// 					},
// 				},
// 			})
// 		}
// 		if params.Space.Min > 0 {
// 			filter = append(filter, map[string]interface{}{
// 				"range": map[string]interface{}{
// 					"space.maxSpace": map[string]interface{}{
// 						"gte": params.Space.Min,
// 					},
// 				},
// 			})
// 		}
// 	}

// 	// ✅ ROI: Range query (if your ES schema supports it)
// 	if params.ROI != nil && (params.ROI.Min > 0 || params.ROI.Max > 0) {
// 		hasFilters = true
// 		roiFilter := map[string]interface{}{}
// 		if params.ROI.Min > 0 {
// 			roiFilter["gte"] = params.ROI.Min
// 		}
// 		if params.ROI.Max > 0 {
// 			roiFilter["lte"] = params.ROI.Max
// 		}
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"roi": roiFilter, // Assuming your schema has "roi" field
// 			},
// 		})
// 	}

// 	// ✅ OUTLETS: Minimum outlets filter
// 	if params.Outlets != nil && *params.Outlets > 0 {
// 		hasFilters = true
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"total_outlets": map[string]interface{}{
// 					"gte": *params.Outlets,
// 				},
// 			},
// 		})
// 	}

// 	// ✅ VERIFIED: Boolean filter
// 	if params.Verified != nil && *params.Verified {
// 		hasFilters = true
// 		filter = append(filter, map[string]interface{}{
// 			"term": map[string]interface{}{
// 				"verified": true,
// 			},
// 		})
// 	}

// 	// ✅ TRUSTED SELLER: Boolean filter
// 	if params.TrustedSeller != nil && *params.TrustedSeller {
// 		hasFilters = true
// 		filter = append(filter, map[string]interface{}{
// 			"term": map[string]interface{}{
// 				"trusted_seller": true,
// 			},
// 		})
// 	}

// 	// If no filters, use match_all
// 	if !hasFilters {
// 		must = append(must, map[string]interface{}{
// 			"match_all": map[string]interface{}{},
// 		})
// 	}

// 	// Build final query
// 	query := map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"bool": map[string]interface{}{
// 				"must":   must,
// 				"filter": filter,
// 			},
// 		},
// 		"size": h.config.DefaultPageSize,
// 		"from": 0,
// 		"sort": []interface{}{
// 			map[string]interface{}{"_score": map[string]interface{}{"order": "desc"}},
// 			map[string]interface{}{"rating": map[string]interface{}{"order": "desc"}},
// 			map[string]interface{}{"total_outlets": map[string]interface{}{"order": "desc"}},
// 		},
// 	}

//		return query, nil
//	}
//
// ✅ FIXED: Flattened query (max depth 4)
func (h *Handler) buildElasticsearchQuery(params *ExtractedParameters) (map[string]interface{}, error) {
	should := []interface{}{} // For category matching
	filter := []interface{}{} // For all other filters

	// ✅ CATEGORY: Flatten should clauses (no nested bool)
	if params.Category != "" {
		should = append(should,
			map[string]interface{}{
				"match": map[string]interface{}{
					"industry.name": map[string]interface{}{
						"query": params.Category,
						"boost": 3.0,
					},
				},
			},
			map[string]interface{}{
				"match": map[string]interface{}{
					"industry.slug": map[string]interface{}{
						"query": params.Category,
						"boost": 2.5,
					},
				},
			},
			map[string]interface{}{
				"match": map[string]interface{}{
					"name": map[string]interface{}{
						"query": params.Category,
						"boost": 2.0,
					},
				},
			},
			map[string]interface{}{
				"term": map[string]interface{}{
					"tags": strings.ToLower(params.Category),
				},
			},
		)
	}

	// ✅ LOCATION
	if params.Location != nil && params.Location.City != "" {
		filter = append(filter, map[string]interface{}{
			"match": map[string]interface{}{
				"location": params.Location.City,
			},
		})
	}

	// ✅ INVESTMENT
	if params.Investment != nil && params.Investment.Max > 0 {
		filter = append(filter, map[string]interface{}{
			"range": map[string]interface{}{
				"investment.min_investment": map[string]interface{}{
					"lte": params.Investment.Max,
				},
			},
		})
	}
	if params.Investment != nil && params.Investment.Min > 0 {
		filter = append(filter, map[string]interface{}{
			"range": map[string]interface{}{
				"investment.max_investment": map[string]interface{}{
					"gte": params.Investment.Min,
				},
			},
		})
	}

	// ✅ RATING
	if params.Rating != nil && *params.Rating > 0 {
		filter = append(filter, map[string]interface{}{
			"range": map[string]interface{}{
				"rating": map[string]interface{}{
					"gte": *params.Rating,
				},
			},
		})
	}

	// ✅ SPACE
	if params.Space != nil && params.Space.Max > 0 {
		filter = append(filter, map[string]interface{}{
			"range": map[string]interface{}{
				"space.minSpace": map[string]interface{}{
					"lte": params.Space.Max,
				},
			},
		})
	}
	if params.Space != nil && params.Space.Min > 0 {
		filter = append(filter, map[string]interface{}{
			"range": map[string]interface{}{
				"space.maxSpace": map[string]interface{}{
					"gte": params.Space.Min,
				},
			},
		})
	}

	// ✅ ROI
	if params.ROI != nil && (params.ROI.Min > 0 || params.ROI.Max > 0) {
		roiRange := map[string]interface{}{}
		if params.ROI.Min > 0 {
			roiRange["gte"] = params.ROI.Min
		}
		if params.ROI.Max > 0 {
			roiRange["lte"] = params.ROI.Max
		}
		filter = append(filter, map[string]interface{}{
			"range": map[string]interface{}{
				"roi": roiRange,
			},
		})
	}

	// ✅ STAFF
	if params.Staff != nil && (params.Staff.Min > 0 || params.Staff.Max > 0) {
		staffRange := map[string]interface{}{}
		if params.Staff.Min > 0 {
			staffRange["gte"] = params.Staff.Min
		}
		if params.Staff.Max > 0 {
			staffRange["lte"] = params.Staff.Max
		}
		filter = append(filter, map[string]interface{}{
			"range": map[string]interface{}{
				"staff": staffRange,
			},
		})
	}

	// ✅ OUTLETS
	if params.Outlets != nil && *params.Outlets > 0 {
		filter = append(filter, map[string]interface{}{
			"range": map[string]interface{}{
				"total_outlets": map[string]interface{}{
					"gte": *params.Outlets,
				},
			},
		})
	}

	// ✅ VERIFIED
	if params.Verified != nil && *params.Verified {
		filter = append(filter, map[string]interface{}{
			"term": map[string]interface{}{
				"verified": true,
			},
		})
	}

	// ✅ TRUSTED SELLER
	if params.TrustedSeller != nil && *params.TrustedSeller {
		filter = append(filter, map[string]interface{}{
			"term": map[string]interface{}{
				"trusted_seller": true,
			},
		})
	}

	// ✅ Build final query (FLAT structure)
	query := map[string]interface{}{
		"size": h.config.DefaultPageSize,
		"from": 0,
		"sort": []interface{}{
			map[string]interface{}{"_score": map[string]interface{}{"order": "desc"}},
			map[string]interface{}{"rating": map[string]interface{}{"order": "desc"}},
			map[string]interface{}{"total_outlets": map[string]interface{}{"order": "desc"}},
		},
	}

	// ✅ Case 1: Category search with filters
	if len(should) > 0 && len(filter) > 0 {
		query["query"] = map[string]interface{}{
			"bool": map[string]interface{}{
				"should":               should,
				"filter":               filter,
				"minimum_should_match": 1,
			},
		}
	} else if len(should) > 0 {
		// ✅ Case 2: Only category search
		query["query"] = map[string]interface{}{
			"bool": map[string]interface{}{
				"should":               should,
				"minimum_should_match": 1,
			},
		}
	} else if len(filter) > 0 {
		// ✅ Case 3: Only filters
		query["query"] = map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []interface{}{
					map[string]interface{}{"match_all": map[string]interface{}{}},
				},
				"filter": filter,
			},
		}
	} else {
		// ✅ Case 4: No filters at all
		query["query"] = map[string]interface{}{
			"match_all": map[string]interface{}{},
		}
	}

	return query, nil
}

func (h *Handler) executeSearch(ctx context.Context, query map[string]interface{}) (*SearchResults, error) {
	searchCtx, cancel := context.WithTimeout(ctx, h.config.SearchTimeout)
	defer cancel()

	// Log query for debugging
	queryJSON, _ := json.MarshalIndent(query, "", "  ")
	h.logger.Debug("Executing ES query", map[string]interface{}{
		"index": h.config.IndexName,
		"query": string(queryJSON),
	})

	result, err := h.esClient.Search(searchCtx, h.config.IndexName, query)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Parse response
	hits, ok := result["hits"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid hits structure")
	}

	totalInfo, ok := hits["total"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid total structure")
	}

	total := int64(0)
	if val, ok := totalInfo["value"].(float64); ok {
		total = int64(val)
	}

	maxScore := 0.0
	if val, ok := hits["max_score"].(float64); ok {
		maxScore = val
	}

	hitsList := []map[string]interface{}{}
	if hitsArray, ok := hits["hits"].([]interface{}); ok {
		for _, hit := range hitsArray {
			if hitMap, ok := hit.(map[string]interface{}); ok {
				hitsList = append(hitsList, hitMap)
			}
		}
	}

	tookMs := int64(0)
	if val, ok := result["took"].(float64); ok {
		tookMs = int64(val)
	}

	return &SearchResults{
		Total:    total,
		MaxScore: maxScore,
		Hits:     hitsList,
		TookMs:   tookMs,
	}, nil
}

// ✅ UPDATED: Build response with ALL parameters
func (h *Handler) buildResponse(input *SearchInput, params *ExtractedParameters, results *SearchResults) map[string]interface{} {
	extractedParams := map[string]interface{}{
		"query":         input.Query,
		"category":      "",
		"location":      "",
		"minInvestment": 0,
		"maxInvestment": 0,
		"minSpace":      0,
		"maxSpace":      0,
		"minStaff":      0,
		"maxStaff":      0,
		"minOutlets":    0,
		"minRoi":        0.0,
		"maxRoi":        0.0,
		"minRating":     0.0,
		"verified":      false,
		"trustedSeller": false,
		"tags":          []string{},
	}

	// Populate from extracted parameters
	if params.Category != "" {
		extractedParams["category"] = params.Category
		extractedParams["tags"] = []string{params.Category}
	}

	if params.Location != nil {
		extractedParams["location"] = params.Location.City
	}

	if params.Investment != nil {
		extractedParams["minInvestment"] = params.Investment.Min
		extractedParams["maxInvestment"] = params.Investment.Max
	}

	if params.Space != nil {
		extractedParams["minSpace"] = params.Space.Min
		extractedParams["maxSpace"] = params.Space.Max
	}

	if params.Staff != nil {
		extractedParams["minStaff"] = params.Staff.Min
		extractedParams["maxStaff"] = params.Staff.Max
	}

	if params.Outlets != nil {
		extractedParams["minOutlets"] = *params.Outlets
	}

	if params.ROI != nil {
		extractedParams["minRoi"] = params.ROI.Min
		extractedParams["maxRoi"] = params.ROI.Max
	}

	if params.Rating != nil {
		extractedParams["minRating"] = *params.Rating
	}

	if params.Verified != nil {
		extractedParams["verified"] = *params.Verified
	}

	if params.TrustedSeller != nil {
		extractedParams["trustedSeller"] = *params.TrustedSeller
	}

	return map[string]interface{}{
		"success":         true,
		"extractedParams": extractedParams,
		"metadata": map[string]interface{}{
			"processed_at": time.Now().UTC().Format(time.RFC3339),
			"took_ms":      results.TookMs,
			"llm_model":    h.config.LLMModel,
			"total_found":  results.Total,
		},
	}
}

func (h *Handler) completeJob(client worker.JobClient, job entities.Job, response map[string]interface{}) error {
	request, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromMap(response)
	if err != nil {
		return fmt.Errorf("create command failed: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := request.Send(ctx); err != nil {
		return fmt.Errorf("send failed: %w", err)
	}

	return nil
}

func (h *Handler) handleError(client worker.JobClient, job entities.Job, err error, errorCode string) {
	h.logger.Error("Job failed", map[string]interface{}{
		"job_key":    job.Key,
		"error_code": errorCode,
		"error":      err.Error(),
	})

	metrics.WorkerJobsFailed.WithLabelValues("ai-search-franchise", errorCode).Inc()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client.NewFailJobCommand().
		JobKey(job.Key).
		Retries(job.Retries - 1).
		ErrorMessage(fmt.Sprintf("%s: %v", errorCode, err)).
		Send(ctx)
}

// // ============================================================
// // FILE: internal/workers/ai-conversation/ai-search/handler.go
// // ============================================================

// package ai_search

// import (
// 	"bytes"
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"io"
// 	"net/http"
// 	"strings"
// 	"time"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

// 	"camunda-workers/internal/common/database"
// 	"camunda-workers/internal/common/logger"
// 	"camunda-workers/internal/common/metrics"
// )

// // ============================================================
// // HANDLER
// // ============================================================

// // Handler processes natural language search queries
// type Handler struct {
// 	config         *Config
// 	llmService     *OllamaService
// 	esClient       *database.ElasticsearchClient
// 	logger         logger.Logger
// 	paramExtractor *ParameterExtractor
// }

// // NewHandler creates a new AI search handler
// func NewHandler(
// 	config *Config,
// 	esClient *database.ElasticsearchClient,
// 	log logger.Logger,
// ) *Handler {
// 	return &Handler{
// 		config:         config,
// 		llmService:     NewOllamaService(config, log),
// 		esClient:       esClient,
// 		logger:         log,
// 		paramExtractor: NewParameterExtractor(config),
// 	}
// }

// // ============================================================
// // OLLAMA LLM SERVICE (Internal)
// // ============================================================

// type OllamaService struct {
// 	endpoint    string
// 	model       string
// 	maxTokens   int
// 	temperature float64
// 	httpClient  *http.Client
// 	logger      logger.Logger
// }

// type OllamaRequest struct {
// 	Model     string                 `json:"model"`
// 	Prompt    string                 `json:"prompt"`
// 	Stream    bool                   `json:"stream"`
// 	Options   map[string]interface{} `json:"options,omitempty"`
// 	Format    string                 `json:"format,omitempty"`
// 	KeepAlive string                 `json:"keep_alive,omitempty"`
// }

// type OllamaResponse struct {
// 	Model    string `json:"model"`
// 	Response string `json:"response"`
// 	Done     bool   `json:"done"`
// }

// func NewOllamaService(config *Config, log logger.Logger) *OllamaService {
// 	return &OllamaService{
// 		endpoint:    config.LLMEndpoint,
// 		model:       config.LLMModel,
// 		maxTokens:   config.LLMMaxTokens,
// 		temperature: config.LLMTemperature,
// 		httpClient: &http.Client{
// 			Timeout: config.LLMTimeout,
// 		},
// 		logger: log,
// 	}
// }

// func (s *OllamaService) Extract(ctx context.Context, prompt string) (string, error) {
// 	s.logger.Debug("Calling Ollama API", map[string]interface{}{
// 		"model":      s.model,
// 		"prompt_len": len(prompt),
// 	})

// 	reqBody := OllamaRequest{
// 		Model:  s.model,
// 		Prompt: prompt,
// 		Stream: false,
// 		Format: "json",
// 		Options: map[string]interface{}{
// 			"temperature": s.temperature,
// 			"num_predict": s.maxTokens,
// 		},
// 		KeepAlive: "5m",
// 	}

// 	jsonData, err := json.Marshal(reqBody)
// 	if err != nil {
// 		return "", fmt.Errorf("marshal failed: %w", err)
// 	}

// 	req, err := http.NewRequestWithContext(ctx, "POST", s.endpoint+"/api/generate", bytes.NewBuffer(jsonData))
// 	if err != nil {
// 		return "", fmt.Errorf("request creation failed: %w", err)
// 	}
// 	req.Header.Set("Content-Type", "application/json")

// 	resp, err := s.httpClient.Do(req)
// 	if err != nil {
// 		return "", fmt.Errorf("request failed: %w", err)
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK {
// 		body, _ := io.ReadAll(resp.Body)
// 		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
// 	}

// 	body, err := io.ReadAll(resp.Body)
// 	if err != nil {
// 		return "", fmt.Errorf("read failed: %w", err)
// 	}

// 	var ollamaResp OllamaResponse
// 	if err := json.Unmarshal(body, &ollamaResp); err != nil {
// 		return "", fmt.Errorf("parse failed: %w", err)
// 	}

// 	return ollamaResp.Response, nil
// }

// // ============================================================
// // MAIN HANDLER LOGIC
// // ============================================================

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	ctx := context.Background()
// 	startTime := time.Now()

// 	h.logger.Info("AI search job started", map[string]interface{}{
// 		"job_key":    job.Key,
// 		"process_id": job.ProcessInstanceKey,
// 	})

// 	// Parse input
// 	input, err := h.parseInput(job)
// 	if err != nil {
// 		h.handleError(client, job, err, "INPUT_PARSE_ERROR")
// 		return
// 	}

// 	// Validate
// 	if err := h.validateInput(input); err != nil {
// 		h.handleError(client, job, err, "VALIDATION_ERROR")
// 		return
// 	}

// 	// Extract parameters using LLM
// 	params, err := h.extractParameters(ctx, input)
// 	if err != nil {
// 		h.handleError(client, job, err, "PARAMETER_EXTRACTION_ERROR")
// 		return
// 	}

// 	// Build ES query
// 	esQuery, err := h.buildElasticsearchQuery(params)
// 	if err != nil {
// 		h.handleError(client, job, err, "QUERY_BUILD_ERROR")
// 		return
// 	}

// 	// Execute search using common ES client
// 	results, err := h.executeSearch(ctx, esQuery)
// 	if err != nil {
// 		h.handleError(client, job, err, "SEARCH_EXECUTION_ERROR")
// 		return
// 	}

// 	// Build response
// 	response := h.buildResponse(input, params, results)

// 	// Complete job
// 	if err := h.completeJob(client, job, response); err != nil {
// 		h.logger.Error("Failed to complete job", map[string]interface{}{
// 			"job_key": job.Key,
// 			"error":   err.Error(),
// 		})
// 		return
// 	}

// 	// Record metrics
// 	duration := time.Since(startTime)
// 	metrics.WorkerJobsCompleted.WithLabelValues("ai-search-franchise").Inc()
// 	metrics.WorkerJobDuration.WithLabelValues("ai-search-franchise").Observe(duration.Seconds())

// 	h.logger.Info("AI search completed", map[string]interface{}{
// 		"job_key": job.Key,
// 		"results": results.Total,
// 		"took_ms": duration.Milliseconds(),
// 	})
// }

// func (h *Handler) parseInput(job entities.Job) (*SearchInput, error) {
// 	var input SearchInput
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
// 		return nil, fmt.Errorf("parse failed: %w", err)
// 	}

// 	// Try alternative field names
// 	if input.Query == "" {
// 		var varMap map[string]interface{}
// 		json.Unmarshal([]byte(job.Variables), &varMap)
// 		for _, field := range []string{"query", "searchQuery", "search_query", "text"} {
// 			if val, ok := varMap[field].(string); ok && val != "" {
// 				input.Query = val
// 				break
// 			}
// 		}
// 	}

// 	return &input, nil
// }

// func (h *Handler) validateInput(input *SearchInput) error {
// 	if input == nil {
// 		return fmt.Errorf("input is nil")
// 	}

// 	// ✅ CHANGED: Allow empty queries but set a default
// 	if strings.TrimSpace(input.Query) == "" {
// 		h.logger.Warn("Empty search query received, using default", map[string]interface{}{
// 			"original_query": input.Query,
// 		})
// 		// Set a default query that will match all
// 		input.Query = "*"
// 	}

// 	if len(input.Query) > h.config.MaxQueryLength {
// 		return fmt.Errorf("query too long")
// 	}
// 	return nil
// }

// // func (h *Handler) validateInput(input *SearchInput) error {
// // 	if input == nil {
// // 		return fmt.Errorf("input is nil")
// // 	}
// // 	if strings.TrimSpace(input.Query) == "" {
// // 		return fmt.Errorf("query is empty")
// // 	}
// // 	if len(input.Query) > h.config.MaxQueryLength {
// // 		return fmt.Errorf("query too long")
// // 	}
// // 	return nil
// // }

// func (h *Handler) extractParameters(ctx context.Context, input *SearchInput) (*ExtractedParameters, error) {
// 	h.logger.Debug("Extracting parameters", map[string]interface{}{
// 		"query": input.Query,
// 	})

// 	// ✅ ADDED: If query is wildcard, return empty params
// 	if input.Query == "*" || strings.TrimSpace(input.Query) == "" {
// 		h.logger.Info("Wildcard or empty query - returning match-all parameters", nil)
// 		return &ExtractedParameters{
// 			Category:   "",
// 			Location:   nil,
// 			Investment: nil,
// 			ROI:        nil,
// 			Rating:     nil,
// 			Verified:   nil,
// 			TrustedSeller: nil,
// 		}, nil
// 	}

// 	prompt := h.paramExtractor.BuildPrompt(input.Query)

// 	llmCtx, cancel := context.WithTimeout(ctx, h.config.LLMTimeout)
// 	defer cancel()

// 	response, err := h.llmService.Extract(llmCtx, prompt)
// 	if err != nil {
// 		return nil, fmt.Errorf("LLM extraction failed: %w", err)
// 	}

// 	params, err := h.paramExtractor.Parse(response)
// 	if err != nil {
// 		return nil, fmt.Errorf("parse failed: %w", err)
// 	}

// 	if err := h.validateParameters(params); err != nil {
// 		return nil, fmt.Errorf("validation failed: %w", err)
// 	}

// 	return params, nil
// }

// // func (h *Handler) extractParameters(ctx context.Context, input *SearchInput) (*ExtractedParameters, error) {
// // 	h.logger.Debug("Extracting parameters", map[string]interface{}{
// // 		"query": input.Query,
// // 	})

// // 	prompt := h.paramExtractor.BuildPrompt(input.Query)

// // 	llmCtx, cancel := context.WithTimeout(ctx, h.config.LLMTimeout)
// // 	defer cancel()

// // 	response, err := h.llmService.Extract(llmCtx, prompt)
// // 	if err != nil {
// // 		return nil, fmt.Errorf("LLM extraction failed: %w", err)
// // 	}

// // 	params, err := h.paramExtractor.Parse(response)
// // 	if err != nil {
// // 		return nil, fmt.Errorf("parse failed: %w", err)
// // 	}

// // 	if err := h.validateParameters(params); err != nil {
// // 		return nil, fmt.Errorf("validation failed: %w", err)
// // 	}

// // 	return params, nil
// // }

// func (h *Handler) validateParameters(params *ExtractedParameters) error {
// 	if params == nil {
// 		return fmt.Errorf("params nil")
// 	}

// 	if params.ROI != nil {
// 		if params.ROI.Min < 0 || params.ROI.Max > 100 {
// 			return fmt.Errorf("ROI must be 0-100")
// 		}
// 		if params.ROI.Min > params.ROI.Max {
// 			return fmt.Errorf("ROI min > max")
// 		}
// 	}

// 	if params.Investment != nil {
// 		if params.Investment.Min < 0 || params.Investment.Max < 0 {
// 			return fmt.Errorf("investment must be positive")
// 		}
// 	}

// 	if params.Rating != nil && (*params.Rating < 0 || *params.Rating > 5) {
// 		return fmt.Errorf("rating must be 0-5")
// 	}

// 	return nil
// }

// func (h *Handler) buildElasticsearchQuery(params *ExtractedParameters) (map[string]interface{}, error) {
// 	must := []interface{}{}
// 	filter := []interface{}{}

// 	// ✅ ADDED: If no params, use match_all
// 	hasFilters := false

// 	// Category
// 	if params.Category != "" {
// 		hasFilters = true
// 		must = append(must, map[string]interface{}{
// 			"match": map[string]interface{}{
// 				"industry": map[string]interface{}{
// 					"query": params.Category,
// 					"boost": 2.0,
// 				},
// 			},
// 		})
// 	}

// 	// Location
// 	if params.Location != nil && params.Location.City != "" {
// 		hasFilters = true
// 		filter = append(filter, map[string]interface{}{
// 			"nested": map[string]interface{}{
// 				"path": "cities",
// 				"query": map[string]interface{}{
// 					"bool": map[string]interface{}{
// 						"must": []interface{}{
// 							map[string]interface{}{
// 								"match": map[string]interface{}{
// 									"cities.city": params.Location.City,
// 								},
// 							},
// 						},
// 					},
// 				},
// 			},
// 		})
// 	}

// 	// ROI Range
// 	if params.ROI != nil {
// 		hasFilters = true
// 		filter = append(filter, map[string]interface{}{
// 			"bool": map[string]interface{}{
// 				"must": []interface{}{
// 					map[string]interface{}{
// 						"range": map[string]interface{}{
// 							"investment.roi_min_percentage": map[string]interface{}{
// 								"lte": params.ROI.Max,
// 							},
// 						},
// 					},
// 					map[string]interface{}{
// 						"range": map[string]interface{}{
// 							"investment.roi_max_percentage": map[string]interface{}{
// 								"gte": params.ROI.Min,
// 							},
// 						},
// 					},
// 				},
// 			},
// 		})
// 	}

// 	// Investment
// 	if params.Investment != nil && params.Investment.Min > 0 {
// 		hasFilters = true
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"investment.initial_investment_min": map[string]interface{}{
// 					"gte": params.Investment.Min,
// 					"lte": params.Investment.Max,
// 				},
// 			},
// 		})
// 	}

// 	// Rating
// 	if params.Rating != nil {
// 		hasFilters = true
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"stats.rating": map[string]interface{}{
// 					"gte": *params.Rating,
// 				},
// 			},
// 		})
// 	}

// 	// Verified
// 	if params.Verified != nil && *params.Verified {
// 		hasFilters = true
// 		filter = append(filter, map[string]interface{}{
// 			"term": map[string]interface{}{"verified": true},
// 		})
// 	}

// 	// Trusted seller
// 	if params.TrustedSeller != nil && *params.TrustedSeller {
// 		hasFilters = true
// 		filter = append(filter, map[string]interface{}{
// 			"term": map[string]interface{}{"trusted_seller": true},
// 		})
// 	}

// 	// ✅ ADDED: If no filters, use match_all
// 	if !hasFilters {
// 		must = append(must, map[string]interface{}{
// 			"match_all": map[string]interface{}{},
// 		})
// 	}

// 	// Build final query
// 	query := map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"bool": map[string]interface{}{
// 				"must":   must,
// 				"filter": filter,
// 			},
// 		},
// 		"size": h.config.DefaultPageSize,
// 		"from": 0,
// 		"sort": []interface{}{
// 			map[string]interface{}{"_score": map[string]interface{}{"order": "desc"}},
// 			map[string]interface{}{"stats.rating": map[string]interface{}{"order": "desc"}},
// 			map[string]interface{}{"stats.view_count": map[string]interface{}{"order": "desc"}},
// 		},
// 	}

// 	return query, nil
// }

// // func (h *Handler) buildElasticsearchQuery(params *ExtractedParameters) (map[string]interface{}, error) {
// // 	must := []interface{}{}
// // 	filter := []interface{}{}

// // 	// Category
// // 	if params.Category != "" {
// // 		must = append(must, map[string]interface{}{
// // 			"match": map[string]interface{}{
// // 				"industry": map[string]interface{}{
// // 					"query": params.Category,
// // 					"boost": 2.0,
// // 				},
// // 			},
// // 		})
// // 	}

// // 	// Location
// // 	if params.Location != nil && params.Location.City != "" {
// // 		filter = append(filter, map[string]interface{}{
// // 			"nested": map[string]interface{}{
// // 				"path": "cities",
// // 				"query": map[string]interface{}{
// // 					"bool": map[string]interface{}{
// // 						"must": []interface{}{
// // 							map[string]interface{}{
// // 								"match": map[string]interface{}{
// // 									"cities.city": params.Location.City,
// // 								},
// // 							},
// // 						},
// // 					},
// // 				},
// // 			},
// // 		})
// // 	}

// // 	// ROI Range - Check overlap
// // 	if params.ROI != nil {
// // 		filter = append(filter, map[string]interface{}{
// // 			"bool": map[string]interface{}{
// // 				"must": []interface{}{
// // 					map[string]interface{}{
// // 						"range": map[string]interface{}{
// // 							"investment.roi_min_percentage": map[string]interface{}{
// // 								"lte": params.ROI.Max,
// // 							},
// // 						},
// // 					},
// // 					map[string]interface{}{
// // 						"range": map[string]interface{}{
// // 							"investment.roi_max_percentage": map[string]interface{}{
// // 								"gte": params.ROI.Min,
// // 							},
// // 						},
// // 					},
// // 				},
// // 			},
// // 		})
// // 	}

// // 	// Investment
// // 	if params.Investment != nil && params.Investment.Min > 0 {
// // 		filter = append(filter, map[string]interface{}{
// // 			"range": map[string]interface{}{
// // 				"investment.initial_investment_min": map[string]interface{}{
// // 					"gte": params.Investment.Min,
// // 					"lte": params.Investment.Max,
// // 				},
// // 			},
// // 		})
// // 	}

// // 	// Rating
// // 	if params.Rating != nil {
// // 		filter = append(filter, map[string]interface{}{
// // 			"range": map[string]interface{}{
// // 				"stats.rating": map[string]interface{}{
// // 					"gte": *params.Rating,
// // 				},
// // 			},
// // 		})
// // 	}

// // 	// Verified
// // 	if params.Verified != nil && *params.Verified {
// // 		filter = append(filter, map[string]interface{}{
// // 			"term": map[string]interface{}{"verified": true},
// // 		})
// // 	}

// // 	// Trusted seller
// // 	if params.TrustedSeller != nil && *params.TrustedSeller {
// // 		filter = append(filter, map[string]interface{}{
// // 			"term": map[string]interface{}{"trusted_seller": true},
// // 		})
// // 	}

// // 	// Build final query
// // 	query := map[string]interface{}{
// // 		"query": map[string]interface{}{
// // 			"bool": map[string]interface{}{
// // 				"must":   must,
// // 				"filter": filter,
// // 			},
// // 		},
// // 		"size": h.config.DefaultPageSize,
// // 		"from": 0,
// // 		"sort": []interface{}{
// // 			map[string]interface{}{"_score": map[string]interface{}{"order": "desc"}},
// // 			map[string]interface{}{"stats.rating": map[string]interface{}{"order": "desc"}},
// // 			map[string]interface{}{"stats.view_count": map[string]interface{}{"order": "desc"}},
// // 		},
// // 	}

// // 	return query, nil
// // }

// func (h *Handler) executeSearch(ctx context.Context, query map[string]interface{}) (*SearchResults, error) {
// 	searchCtx, cancel := context.WithTimeout(ctx, h.config.SearchTimeout)
// 	defer cancel()

// 	// Use common ES client's Search method
// 	result, err := h.esClient.Search(searchCtx, h.config.IndexName, query)
// 	if err != nil {
// 		return nil, fmt.Errorf("search failed: %w", err)
// 	}

// 	// Parse response
// 	hits, ok := result["hits"].(map[string]interface{})
// 	if !ok {
// 		return nil, fmt.Errorf("invalid hits structure")
// 	}

// 	totalInfo, ok := hits["total"].(map[string]interface{})
// 	if !ok {
// 		return nil, fmt.Errorf("invalid total structure")
// 	}

// 	total := int64(0)
// 	if val, ok := totalInfo["value"].(float64); ok {
// 		total = int64(val)
// 	}

// 	maxScore := 0.0
// 	if val, ok := hits["max_score"].(float64); ok {
// 		maxScore = val
// 	}

// 	hitsList := []map[string]interface{}{}
// 	if hitsArray, ok := hits["hits"].([]interface{}); ok {
// 		for _, hit := range hitsArray {
// 			if hitMap, ok := hit.(map[string]interface{}); ok {
// 				hitsList = append(hitsList, hitMap)
// 			}
// 		}
// 	}

// 	tookMs := int64(0)
// 	if val, ok := result["took"].(float64); ok {
// 		tookMs = int64(val)
// 	}

// 	return &SearchResults{
// 		Total:    total,
// 		MaxScore: maxScore,
// 		Hits:     hitsList,
// 		TookMs:   tookMs,
// 	}, nil
// }

// func (h *Handler) buildResponse(input *SearchInput, params *ExtractedParameters, results *SearchResults) map[string]interface{} {
// 	// ✅ Build extractedParams that matches what BPMN expects
// 	extractedParams := map[string]interface{}{
// 		"query":         input.Query,
// 		"category":      "",
// 		"location":      "",
// 		"minInvestment": 0,
// 		"maxInvestment": 0,
// 		"minSpace":      0,
// 		"maxSpace":      0,
// 		"minRating":     0.0,
// 		"tags":          []string{},
// 	}

// 	// Populate from extracted parameters
// 	if params.Category != "" {
// 		extractedParams["category"] = params.Category
// 	}

// 	if params.Location != nil {
// 		extractedParams["location"] = params.Location.City
// 	}

// 	if params.Investment != nil {
// 		extractedParams["minInvestment"] = params.Investment.Min
// 		extractedParams["maxInvestment"] = params.Investment.Max
// 	}

// 	if params.ROI != nil {
// 		// You might want to convert ROI to rating or keep separate
// 		extractedParams["minRating"] = float64(params.ROI.Min) / 20.0 // Example: 8% ROI = 0.4 rating
// 	}

// 	return map[string]interface{}{
// 		"success": true,

// 		// ✅ CRITICAL: This maps to "filters" in BPMN
// 		"extractedParams": extractedParams,

// 		// Also include metadata
// 		"metadata": map[string]interface{}{
// 			"processed_at": time.Now().UTC().Format(time.RFC3339),
// 			"took_ms":      results.TookMs,
// 			"llm_model":    h.config.LLMModel,
// 		},
// 	}
// }

// // func (h *Handler) buildResponse(input *SearchInput, params *ExtractedParameters, results *SearchResults) map[string]interface{} {
// //     return map[string]interface{}{
// //         "success": true,
// //         "query":   input.Query,

// //         // ✅ CRITICAL: Use "extractedParams" to match BPMN output mapping
// //         "extractedParams": map[string]interface{}{
// //             "query":         input.Query,
// //             "category":      params.Category,
// //             "location":      params.Location,
// //             "minInvestment": 0,
// //             "maxInvestment": 0,
// //             "minSpace":      0,
// //             "maxSpace":      0,
// //             "minRating":     0,
// //             "tags":          nil,
// //         },

// //         // Also include search results
// //         "results": map[string]interface{}{
// //             "total":     results.Total,
// //             "hits":      results.Hits,
// //             "max_score": results.MaxScore,
// //         },
// //         "metadata": map[string]interface{}{
// //             "processed_at": time.Now().UTC().Format(time.RFC3339),
// //             "took_ms":      results.TookMs,
// //         },
// //     }
// // }

// func (h *Handler) completeJob(client worker.JobClient, job entities.Job, response map[string]interface{}) error {
// 	request, err := client.NewCompleteJobCommand().
// 		JobKey(job.Key).
// 		VariablesFromMap(response)
// 	if err != nil {
// 		return fmt.Errorf("create command failed: %w", err)
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	if _, err := request.Send(ctx); err != nil {
// 		return fmt.Errorf("send failed: %w", err)
// 	}

// 	return nil
// }

// func (h *Handler) handleError(client worker.JobClient, job entities.Job, err error, errorCode string) {
// 	h.logger.Error("Job failed", map[string]interface{}{
// 		"job_key":    job.Key,
// 		"error_code": errorCode,
// 		"error":      err.Error(),
// 	})

// 	metrics.WorkerJobsFailed.WithLabelValues("ai-search-franchise", errorCode).Inc()

// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	client.NewFailJobCommand().
// 		JobKey(job.Key).
// 		Retries(job.Retries - 1).
// 		ErrorMessage(fmt.Sprintf("%s: %v", errorCode, err)).
// 		Send(ctx)
// }
