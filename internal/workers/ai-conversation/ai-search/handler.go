// ============================================================
// FILE: internal/workers/ai-conversation/ai-search/handler.go
// COMPLETE OPTIMIZED VERSION with Parallel Execution + Fallback
// ============================================================

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
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				// ✅ Connection pooling for better performance
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     30 * time.Second,
				DisableKeepAlives:   false,
				// ✅ Connection timeouts
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout: 5 * time.Second,
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

	// ✅ OPTIMIZED REQUEST: Smaller context, fewer tokens
	reqBody := OllamaRequest{
		Model:  s.model,
		Prompt: prompt,
		Stream: false,
		Format: "json",
		Options: map[string]interface{}{
			"temperature":    s.temperature,
			"num_predict":    100, // ✅ Reduced from 200 (2x faster)
			"num_ctx":        512, // ✅ Small context (64x smaller than default)
			"repeat_penalty": 1.1,
			"top_k":          10,
			"top_p":          0.9,
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
	errChan := make(chan error, 1)

	// ✅ Start LLM extraction in background (non-blocking)
	go func() {
		llmCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		params := h.extractParametersWithFallback(llmCtx, input)
		paramsChan <- params
	}()

	// ✅ Start basic ES query immediately (don't wait for LLM)
	var basicResults *SearchResults
	go func() {
		basicQuery := h.buildBasicQuery(input.Query)
		results, err := h.executeSearch(ctx, basicQuery)
		if err != nil {
			errChan <- err
			return
		}
		basicResults = results
		errChan <- nil
	}()

	// ✅ Wait for LLM with timeout (max 18s)
	var params *ExtractedParameters
	select {
	case params = <-paramsChan:
		h.logger.Info("LLM parameters extracted", map[string]interface{}{
			"has_category": params.Category != "",
			"has_location": params.Location != nil,
		})
	case <-time.After(18 * time.Second):
		h.logger.Warn("LLM extraction timeout, using empty params", nil)
		params = &ExtractedParameters{}
	}

	// ✅ Wait for basic ES query (should be fast, max 5s)
	select {
	case err := <-errChan:
		if err != nil {
			h.handleError(client, job, err, "BASIC_SEARCH_ERROR")
			return
		}
	case <-time.After(5 * time.Second):
		h.handleError(client, job, fmt.Errorf("basic search timeout"), "SEARCH_TIMEOUT")
		return
	}

	// ✅ If LLM gave us useful params, refine the search
	var finalResults *SearchResults
	if params.Category != "" || params.Location != nil {
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
		"used_llm": params.Category != "" || params.Location != nil,
	})
}

// ============================================================
// QUERY BUILDERS
// ============================================================

// ✅ NEW: Build basic query without waiting for LLM
func (h *Handler) buildBasicQuery(query string) map[string]interface{} {
	// Handle wildcard or empty query
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

	// Simple multi-field search
	return map[string]interface{}{
		"size": h.config.DefaultPageSize,
		"query": map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  query,
				"fields": []string{"name^3", "industry.name^2", "tags", "location"},
				"type":   "best_fields",
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
	// ✅ STRATEGY:
	// 1. Main query: simple_query_string for category/location (depth 2)
	// 2. Post-filter: All range/term filters (depth 2 each, but separate from query)
	// This keeps total depth at 3 maximum!

	// Build main query string for text search
	var queryParts []string

	if params.Category != "" {
		queryParts = append(queryParts, params.Category)
	}

	if params.Location != nil && params.Location.City != "" {
		queryParts = append(queryParts, params.Location.City)
	}

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
	if len(queryParts) > 0 {
		queryString := strings.Join(queryParts, " ")
		esQuery["query"] = map[string]interface{}{
			"simple_query_string": map[string]interface{}{
				"query":            queryString,
				"fields":           []string{"industry.name^3", "industry.slug^2", "name^2", "tags", "location"},
				"default_operator": "AND",
			},
		}
	} else {
		esQuery["query"] = map[string]interface{}{
			"match_all": map[string]interface{}{},
		}
	}

	// ✅ POST_FILTER: All filters applied AFTER query (keeps depth flat!)
	postFilters := []interface{}{}

	// Investment range
	if params.Investment != nil {
		if params.Investment.Max > 0 {
			postFilters = append(postFilters, map[string]interface{}{
				"range": map[string]interface{}{
					"investment.min_investment": map[string]interface{}{"lte": params.Investment.Max},
				},
			})
		}
		if params.Investment.Min > 0 {
			postFilters = append(postFilters, map[string]interface{}{
				"range": map[string]interface{}{
					"investment.max_investment": map[string]interface{}{"gte": params.Investment.Min},
				},
			})
		}
	}

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
		"category":       params.Category,
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
// // FIXED VERSION: Ultra-flat query structure (max depth 3)
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

// // Handler processes natural language search queries
// type Handler struct {
// 	config         *Config
// 	llmService     *OllamaService
// 	esClient       *database.ElasticsearchClient
// 	logger         logger.Logger
// 	paramExtractor *ParameterExtractor
// }

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
// // OLLAMA LLM SERVICE
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
// 		"endpoint":   s.endpoint,
// 		"prompt_len": len(prompt),
// 		"timeout":    s.httpClient.Timeout.Seconds(),
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

// 	s.logger.Debug("LLM response received", map[string]interface{}{
// 		"response_len": len(ollamaResp.Response),
// 		"done":         ollamaResp.Done,
// 	})

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
// 		"index":      h.config.IndexName,
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

// 	// Extract parameters using LLM (with fallback)
// 	params := h.extractParametersWithFallback(ctx, input)

// 	// Build ES query
// 	esQuery, err := h.buildElasticsearchQuery(params)
// 	if err != nil {
// 		h.handleError(client, job, err, "QUERY_BUILD_ERROR")
// 		return
// 	}

// 	// Execute search
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

// 	// Allow empty queries but set a default
// 	if strings.TrimSpace(input.Query) == "" {
// 		h.logger.Warn("Empty search query received, using default", map[string]interface{}{
// 			"original_query": input.Query,
// 		})
// 		input.Query = "*"
// 	}

// 	if len(input.Query) > h.config.MaxQueryLength {
// 		return fmt.Errorf("query too long: %d chars (max %d)", len(input.Query), h.config.MaxQueryLength)
// 	}
// 	return nil
// }

// func (h *Handler) extractParametersWithFallback(ctx context.Context, input *SearchInput) *ExtractedParameters {
// 	h.logger.Debug("Extracting parameters", map[string]interface{}{
// 		"query": input.Query,
// 	})

// 	// If query is wildcard, return empty params
// 	if input.Query == "*" || strings.TrimSpace(input.Query) == "" {
// 		h.logger.Info("Wildcard or empty query - returning match-all parameters", nil)
// 		return &ExtractedParameters{}
// 	}

// 	// Build prompt
// 	prompt := h.paramExtractor.BuildPrompt(input.Query)

// 	// Call LLM with timeout
// 	llmCtx, cancel := context.WithTimeout(ctx, h.config.LLMTimeout)
// 	defer cancel()

// 	response, err := h.llmService.Extract(llmCtx, prompt)
// 	if err != nil {
// 		h.logger.Warn("LLM extraction failed, using fallback", map[string]interface{}{
// 			"error": err.Error(),
// 			"query": input.Query,
// 		})
// 		// ✅ FALLBACK: Return empty params instead of failing
// 		return &ExtractedParameters{}
// 	}

// 	// Parse response with fallback
// 	params := h.paramExtractor.ParseWithFallback(response)

// 	h.logger.Info("Parameters extracted", map[string]interface{}{
// 		"category":       params.Category,
// 		"has_location":   params.Location != nil,
// 		"has_investment": params.Investment != nil,
// 		"has_rating":     params.Rating != nil,
// 		"has_space":      params.Space != nil,
// 		"has_staff":      params.Staff != nil,
// 		"has_outlets":    params.Outlets != nil,
// 		"has_roi":        params.ROI != nil,
// 	})

// 	return params
// }

// // ✅ FIXED: Ultra-flat query structure (max depth 3) to stay well under ES limit of 5
// func (h *Handler) buildElasticsearchQuery(params *ExtractedParameters) (map[string]interface{}, error) {
// 	must := []interface{}{}
// 	filter := []interface{}{}

// 	// ✅ CATEGORY: Use multi_match at top level (depth 2)
// 	if params.Category != "" {
// 		must = append(must, map[string]interface{}{
// 			"multi_match": map[string]interface{}{
// 				"query":  params.Category,
// 				"fields": []string{"industry.name^3", "industry.slug^2.5", "name^2", "tags"},
// 				"type":   "best_fields",
// 			},
// 		})
// 	}

// 	// ✅ LOCATION (depth 2)
// 	if params.Location != nil && params.Location.City != "" {
// 		filter = append(filter, map[string]interface{}{
// 			"match": map[string]interface{}{
// 				"location": params.Location.City,
// 			},
// 		})
// 	}

// 	// ✅ INVESTMENT (depth 2)
// 	if params.Investment != nil && params.Investment.Max > 0 {
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"investment.min_investment": map[string]interface{}{"lte": params.Investment.Max},
// 			},
// 		})
// 	}
// 	if params.Investment != nil && params.Investment.Min > 0 {
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"investment.max_investment": map[string]interface{}{"gte": params.Investment.Min},
// 			},
// 		})
// 	}

// 	// ✅ RATING (depth 2)
// 	if params.Rating != nil && *params.Rating > 0 {
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"rating": map[string]interface{}{"gte": *params.Rating},
// 			},
// 		})
// 	}

// 	// ✅ SPACE (depth 2)
// 	if params.Space != nil && params.Space.Max > 0 {
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"space.minSpace": map[string]interface{}{"lte": params.Space.Max},
// 			},
// 		})
// 	}
// 	if params.Space != nil && params.Space.Min > 0 {
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"space.maxSpace": map[string]interface{}{"gte": params.Space.Min},
// 			},
// 		})
// 	}

// 	// ✅ ROI (depth 2)
// 	if params.ROI != nil && (params.ROI.Min > 0 || params.ROI.Max > 0) {
// 		roiRange := map[string]interface{}{}
// 		if params.ROI.Min > 0 {
// 			roiRange["gte"] = params.ROI.Min
// 		}
// 		if params.ROI.Max > 0 {
// 			roiRange["lte"] = params.ROI.Max
// 		}
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{"roi": roiRange},
// 		})
// 	}

// 	// ✅ STAFF (depth 2)
// 	if params.Staff != nil && (params.Staff.Min > 0 || params.Staff.Max > 0) {
// 		staffRange := map[string]interface{}{}
// 		if params.Staff.Min > 0 {
// 			staffRange["gte"] = params.Staff.Min
// 		}
// 		if params.Staff.Max > 0 {
// 			staffRange["lte"] = params.Staff.Max
// 		}
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{"staff": staffRange},
// 		})
// 	}

// 	// ✅ OUTLETS (depth 2)
// 	if params.Outlets != nil && *params.Outlets > 0 {
// 		filter = append(filter, map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"total_outlets": map[string]interface{}{"gte": *params.Outlets},
// 			},
// 		})
// 	}

// 	// ✅ VERIFIED (depth 2)
// 	if params.Verified != nil && *params.Verified {
// 		filter = append(filter, map[string]interface{}{
// 			"term": map[string]interface{}{"verified": true},
// 		})
// 	}

// 	// ✅ TRUSTED SELLER (depth 2)
// 	if params.TrustedSeller != nil && *params.TrustedSeller {
// 		filter = append(filter, map[string]interface{}{
// 			"term": map[string]interface{}{"trusted_seller": true},
// 		})
// 	}

// 	// ✅ Build final query - Total max depth = 3
// 	query := map[string]interface{}{
// 		"size": h.config.DefaultPageSize,
// 		"from": 0,
// 		"sort": []interface{}{
// 			map[string]interface{}{"_score": "desc"},
// 			map[string]interface{}{"rating": "desc"},
// 			map[string]interface{}{"total_outlets": "desc"},
// 		},
// 	}

// 	// Build bool query only if needed
// 	if len(must) > 0 || len(filter) > 0 {
// 		boolQuery := map[string]interface{}{}

// 		if len(must) > 0 {
// 			boolQuery["must"] = must
// 		} else {
// 			// If no must clauses, add match_all
// 			boolQuery["must"] = []interface{}{
// 				map[string]interface{}{"match_all": map[string]interface{}{}},
// 			}
// 		}

// 		if len(filter) > 0 {
// 			boolQuery["filter"] = filter
// 		}

// 		query["query"] = map[string]interface{}{"bool": boolQuery}
// 	} else {
// 		// No filters at all - simple match_all
// 		query["query"] = map[string]interface{}{"match_all": map[string]interface{}{}}
// 	}

// 	return query, nil
// }

// func (h *Handler) executeSearch(ctx context.Context, query map[string]interface{}) (*SearchResults, error) {
// 	searchCtx, cancel := context.WithTimeout(ctx, h.config.SearchTimeout)
// 	defer cancel()

// 	// Log query for debugging
// 	queryJSON, _ := json.MarshalIndent(query, "", "  ")
// 	h.logger.Debug("Executing ES query", map[string]interface{}{
// 		"index": h.config.IndexName,
// 		"query": string(queryJSON),
// 	})

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
// 	extractedParams := map[string]interface{}{
// 		"query":         input.Query,
// 		"category":      "",
// 		"location":      "",
// 		"minInvestment": 0,
// 		"maxInvestment": 0,
// 		"minSpace":      0,
// 		"maxSpace":      0,
// 		"minStaff":      0,
// 		"maxStaff":      0,
// 		"minOutlets":    0,
// 		"minRoi":        0.0,
// 		"maxRoi":        0.0,
// 		"minRating":     0.0,
// 		"verified":      false,
// 		"trustedSeller": false,
// 		"tags":          []string{},
// 	}

// 	// Populate from extracted parameters
// 	if params.Category != "" {
// 		extractedParams["category"] = params.Category
// 		extractedParams["tags"] = []string{params.Category}
// 	}

// 	if params.Location != nil {
// 		extractedParams["location"] = params.Location.City
// 	}

// 	if params.Investment != nil {
// 		extractedParams["minInvestment"] = params.Investment.Min
// 		extractedParams["maxInvestment"] = params.Investment.Max
// 	}

// 	if params.Space != nil {
// 		extractedParams["minSpace"] = params.Space.Min
// 		extractedParams["maxSpace"] = params.Space.Max
// 	}

// 	if params.Staff != nil {
// 		extractedParams["minStaff"] = params.Staff.Min
// 		extractedParams["maxStaff"] = params.Staff.Max
// 	}

// 	if params.Outlets != nil {
// 		extractedParams["minOutlets"] = *params.Outlets
// 	}

// 	if params.ROI != nil {
// 		extractedParams["minRoi"] = params.ROI.Min
// 		extractedParams["maxRoi"] = params.ROI.Max
// 	}

// 	if params.Rating != nil {
// 		extractedParams["minRating"] = *params.Rating
// 	}

// 	if params.Verified != nil {
// 		extractedParams["verified"] = *params.Verified
// 	}

// 	if params.TrustedSeller != nil {
// 		extractedParams["trustedSeller"] = *params.TrustedSeller
// 	}

// 	return map[string]interface{}{
// 		"success":         true,
// 		"extractedParams": extractedParams,
// 		"metadata": map[string]interface{}{
// 			"processed_at": time.Now().UTC().Format(time.RFC3339),
// 			"took_ms":      results.TookMs,
// 			"llm_model":    h.config.LLMModel,
// 			"total_found":  results.Total,
// 		},
// 	}
// }

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
