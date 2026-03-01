package ai_search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/metrics"
)

// ============================================================
// HANDLER STRUCT
// ============================================================

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
// OLLAMA LLM SERVICE - OPTIMIZED
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
			// ✅ OPTIMIZED: 20s timeout (reduced from 35s)
			Timeout: 40 * time.Second,
			Transport: &http.Transport{
				// ✅ Connection pooling for better performance
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     30 * time.Second,
				DisableKeepAlives:   false,
				// ✅ Connection timeouts
				DialContext: (&net.Dialer{
					Timeout:   3 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout: 3 * time.Second,
			},
		},
		logger: log,
	}
}

func (s *OllamaService) Extract(ctx context.Context, prompt string) (string, error) {
	startTime := time.Now()

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
			"temperature":    0.0,  // ✅ CHANGE - 0.0 se fast hoga
			"num_predict":    150,  // ✅ CHANGE - 100 se 80 (fast)
			"num_ctx":        2048, // ✅ CRITICAL - 512 se 2048 (recompilation avoid)
			"repeat_penalty": 1.0,  // ✅ CHANGE - penalty hataya
			"top_k":          5,    // ✅ CHANGE - 10 se 5
			"top_p":          0.8,  // ✅ CHANGE - 0.9 se 0.8
			"num_thread":     0,    // ✅ NAYA - Auto-detect threads
			"num_batch":      512,  // ✅ NAYA - Batch size
			//"low_vram":       true, // ✅ NAYA - Memory optimization
		},
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
		duration := time.Since(startTime)
		s.logger.Warn("LLM request failed", map[string]interface{}{
			"error":       err.Error(),
			"duration_ms": duration.Milliseconds(),
		})
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
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

	duration := time.Since(startTime)
	s.logger.Info("LLM response received", map[string]interface{}{
		"response_len": len(ollamaResp.Response),
		"done":         ollamaResp.Done,
		"duration_ms":  duration.Milliseconds(),
	})

	return ollamaResp.Response, nil
}

// ============================================================
// MAIN HANDLER LOGIC - PARALLEL EXECUTION
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

	// ✅ PARALLEL EXECUTION: LLM + Basic ES query run simultaneously
	paramsChan := make(chan *ExtractedParameters, 1)
	resultsChan := make(chan *SearchResults, 1)
	errChan := make(chan error, 1)

	// ✅ Start LLM extraction in background (non-blocking)
	go func() {
		llmCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		defer cancel()

		params := h.extractParametersWithFallback(llmCtx, input)
		paramsChan <- params
	}()

	// ✅ Start basic ES query immediately (don't wait for LLM)
	go func() {
		basicQuery := h.buildBasicQuery(input.Query) // ✅ yahan define hoga
		results, err := h.executeSearch(ctx, basicQuery)
		if err != nil {
			errChan <- err
			return
		}
		resultsChan <- results
		errChan <- nil
	}()

	// ✅ Wait for basic ES query (should be fast, max 5s)
	var basicResults *SearchResults
	select {
	case err := <-errChan:
		if err != nil {
			h.handleError(client, job, err, "BASIC_SEARCH_ERROR")
			return
		}
		basicResults = <-resultsChan
	case <-time.After(5 * time.Second):
		h.handleError(client, job, fmt.Errorf("basic search timeout"), "SEARCH_TIMEOUT")
		return
	}

	// ✅ Wait for LLM with timeout (max 30s)
	var params *ExtractedParameters
	select {
	case params = <-paramsChan:
		h.logger.Info("LLM parameters extracted", map[string]interface{}{
			"has_industry":    params.Industry != "",
			"has_category":    params.Category != "",
			"has_subcategory": params.Subcategory != "",
			"has_location":    params.Location != nil,
		})
	case <-time.After(30 * time.Second):
		h.logger.Warn("LLM extraction timeout, using empty params", nil)
		params = &ExtractedParameters{}
	}

	// ✅ If LLM gave us useful params, refine the search
	var finalResults *SearchResults
	if params.Industry != "" || params.Category != "" || params.Subcategory != "" || params.Location != nil {
		refinedQuery, err := h.buildElasticsearchQuery(params)
		if err != nil {
			h.logger.Warn("Refined query build failed, using basic results", map[string]interface{}{
				"error": err.Error(),
			})
			finalResults = basicResults
		} else {
			refinedResults, err := h.executeSearch(ctx, refinedQuery)
			if err != nil {
				h.logger.Warn("Refined search failed, using basic results", map[string]interface{}{
					"error": err.Error(),
				})
				finalResults = basicResults
			} else {
				finalResults = refinedResults
			}
		}
	} else {
		// No LLM params or timeout - use basic results
		finalResults = basicResults
	}

	// Build response
	response := h.buildResponse(input, params, finalResults)

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
		"job_key":  job.Key,
		"results":  finalResults.Total,
		"took_ms":  duration.Milliseconds(),
		"used_llm": params.Industry != "" || params.Category != "" || params.Subcategory != "" || params.Location != nil,
	})
}

// ============================================================
// QUERY BUILDERS
// ============================================================

// ✅ NEW: Build basic query without waiting for LLM
func (h *Handler) buildBasicQuery(query string) map[string]interface{} {
	if query == "*" || strings.TrimSpace(query) == "" {
		return map[string]interface{}{
			"size": h.config.DefaultPageSize,
			"query": map[string]interface{}{
				"match_all": map[string]interface{}{},
			},
			"sort": []interface{}{
				map[string]interface{}{"rating": "desc"},
				map[string]interface{}{"total_outlets": "desc"},
			},
		}
	}

	cleanQuery := stripLocationFromQuery(query)
	if strings.TrimSpace(cleanQuery) == "" {
		cleanQuery = query
	}

	// return map[string]interface{}{
	// 	"size": h.config.DefaultPageSize,
	// 	"query": map[string]interface{}{
	// 		"multi_match": map[string]interface{}{
	// 			"query":     cleanQuery,
	// 			"fields":    []string{"name^3", "industry.name^2", "tags"},
	// 			"type":      "best_fields",
	// 			"fuzziness": "AUTO",
	// 		},
	// 	},
	// 	"sort": []interface{}{
	// 		map[string]interface{}{"_score": "desc"},
	// 		map[string]interface{}{"rating": "desc"},
	// 	},
	// }
	// buildBasicQuery mein ye change karo:
	return map[string]interface{}{
		"size": h.config.DefaultPageSize,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					// Text fields pe fuzzy search
					map[string]interface{}{
						"multi_match": map[string]interface{}{
							"query":     cleanQuery,
							"fields":    []string{"name^3", "tags^2", "description"},
							"type":      "best_fields",
							"fuzziness": "AUTO",
						},
					},
					// Industry name exact match
					map[string]interface{}{
						"term": map[string]interface{}{
							"industry.name": cleanQuery,
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
		"sort": []interface{}{
			map[string]interface{}{"_score": "desc"},
			map[string]interface{}{"rating": "desc"},
		},
	}
}

// ✅ FIXED: Flat query with ALL parameters using post_filter (max depth 3)
func (h *Handler) buildElasticsearchQuery(params *ExtractedParameters) (map[string]interface{}, error) {

	// Build main query string for text search
	var queryParts []string

	// Add industry to query
	if params.Industry != "" {
		queryParts = append(queryParts, params.Industry)
	}

	// Add category to query
	if params.Category != "" {
		queryParts = append(queryParts, params.Category)
	}

	// Add subcategory to query
	if params.Subcategory != "" {
		queryParts = append(queryParts, params.Subcategory)
	}

	// // Add location to query
	// if params.Location != nil && params.Location.City != "" {
	// 	queryParts = append(queryParts, params.Location.City)
	// }

	// Base query structure
	esQuery := map[string]interface{}{
		"size": h.config.DefaultPageSize,
		"from": 0,
		"sort": []interface{}{
			map[string]interface{}{"_score": "desc"},
			map[string]interface{}{"rating": "desc"},
			map[string]interface{}{"total_outlets": "desc"},
		},
	}

	// Main text query
	// if len(queryParts) > 0 {
	// 	queryString := strings.ReplaceAll(strings.Join(queryParts, " "), "&", "and")
	// 	esQuery["query"] = map[string]interface{}{
	// 		"simple_query_string": map[string]interface{}{
	// 			"query":            queryString,
	// 			"fields":           []string{"industry.name^3", "industry.slug^2", "name^2", "tags"},
	// 			"default_operator": "OR", // AND → OR
	// 		},
	// 	}
	// }
	if len(queryParts) > 0 {
		esQuery["query"] = map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					// Industry exact match (keyword field)
					map[string]interface{}{
						"term": map[string]interface{}{
							"industry.name": params.Industry,
						},
					},
					// Industry slug match
					map[string]interface{}{
						"term": map[string]interface{}{
							"industry.slug": strings.ToLower(strings.ReplaceAll(params.Industry, " ", "-")),
						},
					},
					// Name/tags text search
					map[string]interface{}{
						"multi_match": map[string]interface{}{
							"query":  strings.Join(queryParts, " "),
							"fields": []string{"name^2", "tags"},
						},
					},
				},
				"minimum_should_match": 1,
			},
		}
	} else {
		esQuery["query"] = map[string]interface{}{
			"match_all": map[string]interface{}{},
		}
	}

	// ✅ POST_FILTER: All filters applied AFTER query (keeps depth flat!)
	postFilters := []interface{}{}

	if params.Location != nil && params.Location.City != "" {
		city := strings.ToLower(params.Location.City)
		cityTitle := strings.ToUpper(city[:1]) + city[1:] // strings.Title ka replacement

		postFilters = append(postFilters, map[string]interface{}{
			"terms": map[string]interface{}{
				"location": []string{
					cityTitle,
					city,
					strings.ToUpper(city),
					"Pan India",
					"Pan-India",
					"All major Indian cities",
					"North Indian Cities",
					"South Indian Cities",
					"East Indian Cities",
					"West Indian Cities",
				},
			},
		})
	}

	// Investment range
	if params.Investment != nil {
		if params.Investment.Max > 0 {
			maxLakhs := params.Investment.Max / 100000 // rupees → lakhs
			postFilters = append(postFilters, map[string]interface{}{
				"range": map[string]interface{}{
					"investment.min_investment": map[string]interface{}{"lte": maxLakhs},
				},
			})
		}
		if params.Investment.Min > 0 {
			minLakhs := params.Investment.Min / 100000
			postFilters = append(postFilters, map[string]interface{}{
				"range": map[string]interface{}{
					"investment.max_investment": map[string]interface{}{"gte": minLakhs},
				},
			})
		}
	}
	// if params.Investment != nil {
	// 	if params.Investment.Max > 0 {
	// 		postFilters = append(postFilters, map[string]interface{}{
	// 			"range": map[string]interface{}{
	// 				"investment.min_investment": map[string]interface{}{"lte": params.Investment.Max},
	// 			},
	// 		})
	// 	}
	// 	if params.Investment.Min > 0 {
	// 		postFilters = append(postFilters, map[string]interface{}{
	// 			"range": map[string]interface{}{
	// 				"investment.max_investment": map[string]interface{}{"gte": params.Investment.Min},
	// 			},
	// 		})
	// 	}
	// }

	// Rating filter
	if params.Rating != nil && *params.Rating > 0 {
		postFilters = append(postFilters, map[string]interface{}{
			"range": map[string]interface{}{
				"rating": map[string]interface{}{"gte": *params.Rating},
			},
		})
	}

	// Space requirements
	if params.Space != nil {
		if params.Space.Max > 0 {
			postFilters = append(postFilters, map[string]interface{}{
				"range": map[string]interface{}{
					"space.minSpace": map[string]interface{}{"lte": params.Space.Max},
				},
			})
		}
		if params.Space.Min > 0 {
			postFilters = append(postFilters, map[string]interface{}{
				"range": map[string]interface{}{
					"space.maxSpace": map[string]interface{}{"gte": params.Space.Min},
				},
			})
		}
	}

	// ROI range
	if params.ROI != nil {
		roiRange := map[string]interface{}{}
		if params.ROI.Min > 0 {
			roiRange["gte"] = params.ROI.Min
		}
		if params.ROI.Max > 0 {
			roiRange["lte"] = params.ROI.Max
		}
		if len(roiRange) > 0 {
			postFilters = append(postFilters, map[string]interface{}{
				"range": map[string]interface{}{"roi": roiRange},
			})
		}
	}

	// Staff requirements
	if params.Staff != nil {
		staffRange := map[string]interface{}{}
		if params.Staff.Min > 0 {
			staffRange["gte"] = params.Staff.Min
		}
		if params.Staff.Max > 0 {
			staffRange["lte"] = params.Staff.Max
		}
		if len(staffRange) > 0 {
			postFilters = append(postFilters, map[string]interface{}{
				"range": map[string]interface{}{"staff": staffRange},
			})
		}
	}

	// Outlets filter
	if params.Outlets != nil && *params.Outlets > 0 {
		postFilters = append(postFilters, map[string]interface{}{
			"range": map[string]interface{}{
				"total_outlets": map[string]interface{}{"gte": *params.Outlets},
			},
		})
	}

	// Verified flag
	if params.Verified != nil && *params.Verified {
		postFilters = append(postFilters, map[string]interface{}{
			"term": map[string]interface{}{"verified": true},
		})
	}

	// Trusted seller flag
	if params.TrustedSeller != nil && *params.TrustedSeller {
		postFilters = append(postFilters, map[string]interface{}{
			"term": map[string]interface{}{"trusted_seller": true},
		})
	}

	// Apply post_filter if we have any filters
	if len(postFilters) > 0 {
		if len(postFilters) == 1 {
			// Single filter - no bool needed
			esQuery["post_filter"] = postFilters[0]
		} else {
			// Multiple filters - wrap in bool.must (still depth 3 max!)
			esQuery["post_filter"] = map[string]interface{}{
				"bool": map[string]interface{}{
					"must": postFilters,
				},
			}
		}
	}

	return esQuery, nil
}

// ============================================================
// HELPER METHODS
// ============================================================

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

	// Allow empty queries but set to wildcard
	if strings.TrimSpace(input.Query) == "" {
		h.logger.Warn("Empty search query received, using wildcard", map[string]interface{}{
			"original_query": input.Query,
		})
		input.Query = "*"
	}

	if len(input.Query) > h.config.MaxQueryLength {
		return fmt.Errorf("query too long: %d chars (max %d)", len(input.Query), h.config.MaxQueryLength)
	}
	return nil
}

func (h *Handler) extractParametersWithFallback(ctx context.Context, input *SearchInput) *ExtractedParameters {
	h.logger.Debug("Extracting parameters", map[string]interface{}{
		"query": input.Query,
	})

	// Wildcard query - return empty params
	if input.Query == "*" || strings.TrimSpace(input.Query) == "" {
		h.logger.Info("Wildcard query - returning empty parameters", nil)
		return &ExtractedParameters{}
	}

	// Build prompt (now much shorter)
	prompt := h.paramExtractor.BuildPrompt(input.Query)

	// Call LLM
	response, err := h.llmService.Extract(ctx, prompt)
	if err != nil {
		h.logger.Warn("LLM extraction failed, using fallback", map[string]interface{}{
			"error": err.Error(),
			"query": input.Query,
		})
		return &ExtractedParameters{}
	}

	// Parse response with fallback
	params := h.paramExtractor.ParseWithFallback(response)

	h.logger.Info("Parameters extracted", map[string]interface{}{
		"industry":       params.Industry,
		"category":       params.Category,
		"subcategory":    params.Subcategory,
		"has_location":   params.Location != nil,
		"has_investment": params.Investment != nil,
		"has_rating":     params.Rating != nil,
	})

	return params
}

func (h *Handler) executeSearch(ctx context.Context, query map[string]interface{}) (*SearchResults, error) {
	// ✅ OPTIMIZED: 3s timeout (reduced from 5s)
	searchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
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

func (h *Handler) buildResponse(input *SearchInput, params *ExtractedParameters, results *SearchResults) map[string]interface{} {
	extractedParams := map[string]interface{}{
		"query":         input.Query,
		"industry":      "",
		"category":      "",
		"subcategory":   "",
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
	if params.Industry != "" {
		extractedParams["industry"] = params.Industry
	}

	if params.Category != "" {
		extractedParams["category"] = params.Category
	}

	if params.Subcategory != "" {
		extractedParams["subcategory"] = params.Subcategory
	}

	// Build tags from industry, category, and subcategory
	tags := []string{}
	if params.Industry != "" {
		tags = append(tags, params.Industry)
	}
	if params.Category != "" {
		tags = append(tags, params.Category)
	}
	if params.Subcategory != "" {
		tags = append(tags, params.Subcategory)
	}
	if len(tags) > 0 {
		extractedParams["tags"] = tags
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

func stripLocationFromQuery(query string) string {
	prepositions := []string{" in ", " at ", " near ", " from ", " around "}
	result := " " + strings.ToLower(strings.TrimSpace(query)) + " "
	for _, prep := range prepositions {
		if idx := strings.Index(result, prep); idx != -1 {
			result = result[:idx]
		}
	}
	return strings.TrimSpace(result)
}
