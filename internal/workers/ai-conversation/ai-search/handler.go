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
	"sync"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"

	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/location"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/metrics"
)

var (
	llmCacheMu sync.RWMutex
	llmCache   = make(map[string]*ExtractedParameters)
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
	h := &Handler{
		config:         config,
		llmService:     NewOllamaService(config, log),
		esClient:       esClient,
		logger:         log,
		paramExtractor: NewParameterExtractor(config),
	}
	// Preload model in background to avoid cold-start delay
	go h.llmService.Preload()
	return h
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
	KeepAlive interface{}            `json:"keep_alive,omitempty"`
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
			Timeout: 40 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     30 * time.Second,
				DisableKeepAlives:   false,
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

func (s *OllamaService) Preload() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	s.logger.Info("Pre-loading LLM model into memory...", map[string]interface{}{
		"model": s.model,
	})

	reqBody := OllamaRequest{
		Model:     s.model,
		Prompt:    "",
		Stream:    false,
		KeepAlive: -1,
	}

	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", s.endpoint+"/api/generate", bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	// Propagate X-Request-ID for end-to-end tracing
	if reqID, ok := ctx.Value("requestId").(string); ok && reqID != "" {
		req.Header.Set("X-Request-ID", reqID)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logger.Warn("Pre-load failed", map[string]interface{}{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	s.logger.Info("LLM model pre-loaded successfully", map[string]interface{}{"status": resp.Status})
}

func (s *OllamaService) Extract(ctx context.Context, prompt string) (string, error) {
	startTime := time.Now()

	s.logger.Debug("Calling Ollama API", map[string]interface{}{
		"model":      s.model,
		"endpoint":   s.endpoint,
		"prompt_len": len(prompt),
	})

	reqBody := OllamaRequest{
		Model:     s.model,
		Prompt:    prompt,
		Stream:    false,
		KeepAlive: -1, // Indefinite load for maximum performance
		Options: map[string]interface{}{
			"temperature": 0.0,
			"num_predict": 128,
			"num_ctx":     1024,
			"num_thread":  2,
			"num_batch":   512,
			"stop":        []string{"<|im_end|>", "<|im_start|>"},
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
	if reqID, ok := ctx.Value("requestId").(string); ok && reqID != "" {
		req.Header.Set("X-Request-ID", reqID)
	}

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

	var jobVars map[string]interface{}
	if err := json.Unmarshal([]byte(job.Variables), &jobVars); err == nil {
		if rid, ok := jobVars["requestId"].(string); ok && rid != "" {
			ctx = context.WithValue(ctx, "requestId", rid)
		}
	}

	h.logger.Info("AI search job started", map[string]interface{}{
		"job_key":    job.Key,
		"process_id": job.ProcessInstanceKey,
	})

	input, err := h.parseInput(job)
	if err != nil {
		h.handleError(client, job, err, "INPUT_PARSE_ERROR")
		return
	}

	if err := h.validateInput(input); err != nil {
		h.handleError(client, job, err, "VALIDATION_ERROR")
		return
	}

	paramsChan := make(chan *ExtractedParameters, 1)
	resultsChan := make(chan *SearchResults, 1)
	errChan := make(chan error, 1)

	go func() {
		llmCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		defer cancel()
		params := h.extractParametersWithFallback(llmCtx, input)
		paramsChan <- params
	}()

	go func() {
		basicQuery := h.buildBasicQuery(input.Query, input.EntityType)
		results, err := h.executeSearch(ctx, basicQuery)
		if err != nil {
			errChan <- err
			return
		}
		resultsChan <- results
		errChan <- nil
	}()

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

	var params *ExtractedParameters
	select {
	case params = <-paramsChan:
		h.logger.Info("LLM parameters extracted", map[string]interface{}{
			"industry":       params.Industry,
			"category":       params.Category,
			"has_location":   params.Location != nil,
			"has_investment": params.Investment != nil,
		})
	case <-time.After(30 * time.Second):
		h.logger.Warn("LLM timeout, using basic results", nil)
		params = &ExtractedParameters{}
	}

	hasExtractedParams := params.Industry != "" || params.Category != "" || params.Subcategory != "" ||
		params.Location != nil || params.Investment != nil || params.ROI != nil ||
		params.Space != nil || params.Staff != nil || params.Outlets != nil ||
		params.Rating != nil || params.Verified != nil || params.TrustedSeller != nil ||
		params.EntityType != "" || params.MemberCount != nil || params.MembershipFee != nil ||
		params.MinUnits != nil || params.ExclusivityType != "" || params.TerritoryScope != ""

	var finalResults *SearchResults
	if hasExtractedParams {
		refinedQuery, err := h.buildElasticsearchQuery(params)
		if err != nil {
			h.logger.Warn("Refined query failed, using basic", map[string]interface{}{"error": err.Error()})
			finalResults = basicResults
		} else {
			refinedResults, err := h.executeSearch(ctx, refinedQuery)
			if err != nil || refinedResults == nil || refinedResults.Total == 0 {
				refinedTotal := int64(0)
				if refinedResults != nil {
					refinedTotal = refinedResults.Total
				}
				h.logger.Warn("Refined search empty/failed, falling back to basic", map[string]interface{}{
					"refined_total": refinedTotal,
					"error":         fmt.Sprintf("%v", err),
				})
				finalResults = basicResults
			} else {
				finalResults = refinedResults
			}
		}
	} else {
		finalResults = basicResults
	}

	response := h.buildResponse(input, params, finalResults)

	if err := h.completeJob(client, job, response); err != nil {
		h.logger.Error("Failed to complete job", map[string]interface{}{
			"job_key": job.Key,
			"error":   err.Error(),
		})
		return
	}

	duration := time.Since(startTime)
	metrics.WorkerJobsCompleted.WithLabelValues("ai-search-franchise").Inc()
	metrics.WorkerJobDuration.WithLabelValues("ai-search-franchise").Observe(duration.Seconds())

	h.logger.Info("AI search completed", map[string]interface{}{
		"job_key":       job.Key,
		"total_results": finalResults.Total,
		"took_ms":       duration.Milliseconds(),
		"llm_used":      hasExtractedParams,
	})
}

// ============================================================
// QUERY BUILDERS
// ============================================================

func (h *Handler) buildBasicQuery(query string, entityType string) map[string]interface{} {
	if query == "*" || strings.TrimSpace(query) == "" {
		boolQuery := map[string]interface{}{"match_all": map[string]interface{}{}}
		
		var filterClauses []interface{}
		if entityType != "" && entityType != "all" {
			filterClauses = append(filterClauses, map[string]interface{}{
				"term": map[string]interface{}{"entity_type": entityType},
			})
		}
		
		queryMap := map[string]interface{}{}
		if len(filterClauses) > 0 {
			queryMap["bool"] = map[string]interface{}{
				"must": []interface{}{boolQuery},
				"filter": filterClauses,
			}
		} else {
			queryMap = boolQuery
		}

		return map[string]interface{}{
			"size":  h.config.DefaultPageSize,
			"query": queryMap,
			"sort": []interface{}{
				map[string]interface{}{"rating": "desc"},
				map[string]interface{}{"total_outlets": "desc"},
			},
		}
	}

	detectedCity := location.DetectCityFromQuery(query)

	cleanQuery := location.StripLocationFromQuery(query)

	isPureLocation := strings.TrimSpace(cleanQuery) == "" ||
		strings.EqualFold(strings.TrimSpace(cleanQuery), strings.TrimSpace(detectedCity))

	if isPureLocation && detectedCity != "" {
		filterClauses := []interface{}{
			map[string]interface{}{
				"terms": map[string]interface{}{
					"location": location.BuildLocationTerms(detectedCity),
				},
			},
		}
		if entityType != "" && entityType != "all" {
			filterClauses = append(filterClauses, map[string]interface{}{
				"term": map[string]interface{}{"entity_type": entityType},
			})
		}

		return map[string]interface{}{
			"size": h.config.DefaultPageSize,
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"must": []interface{}{
						map[string]interface{}{"match_all": map[string]interface{}{}},
					},
					"filter": filterClauses,
				},
			},
			"sort": []interface{}{
				map[string]interface{}{"rating": "desc"},
				map[string]interface{}{"total_outlets": "desc"},
			},
		}
	}

	// boolQuery := map[string]interface{}{
	// 	"should": []interface{}{
	// 		map[string]interface{}{"match": map[string]interface{}{"industry.name": map[string]interface{}{"query": cleanQuery, "boost": 3}}},
	// 		map[string]interface{}{"match": map[string]interface{}{"tags": map[string]interface{}{"query": cleanQuery, "boost": 2}}},
	// 		map[string]interface{}{"match": map[string]interface{}{"name": map[string]interface{}{"query": cleanQuery, "fuzziness": "AUTO"}}},
	// 		map[string]interface{}{"match": map[string]interface{}{"description": cleanQuery}},
	// 	},
	// 	"minimum_should_match": 1,
	// }
	boolQuery := map[string]interface{}{
		"should": []interface{}{
			map[string]interface{}{
				"multi_match": map[string]interface{}{
					"query":     cleanQuery,
					"fields":    []string{"name^5", "tags^3", "description^2", "industry.name^2"},
					"fuzziness": "AUTO",
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
		},
		"minimum_should_match": 1,
	}

	var filterClauses []interface{}
	if detectedCity != "" {
		filterClauses = append(filterClauses, map[string]interface{}{
			"terms": map[string]interface{}{"location": location.BuildLocationTerms(detectedCity)},
		})
	}
	if entityType != "" && entityType != "all" {
		filterClauses = append(filterClauses, map[string]interface{}{
			"term": map[string]interface{}{"entity_type": entityType},
		})
	}

	if len(filterClauses) > 0 {
		boolQuery["filter"] = filterClauses
	}

	return map[string]interface{}{
		"size":  h.config.DefaultPageSize,
		"query": map[string]interface{}{"bool": boolQuery},
		"sort": []interface{}{
			map[string]interface{}{"_score": "desc"},
			map[string]interface{}{"rating": "desc"},
		},
	}
}

func (h *Handler) buildElasticsearchQuery(params *ExtractedParameters) (map[string]interface{}, error) {
	if params == nil {
		params = &ExtractedParameters{}
	}

	esQuery := map[string]interface{}{
		"size": h.config.DefaultPageSize,
		"from": 0,
		"sort": []interface{}{
			map[string]interface{}{"_score": "desc"},
			map[string]interface{}{"rating": "desc"},
			map[string]interface{}{"total_outlets": "desc"},
		},
	}

	mustClauses := []interface{}{}
	shouldClauses := []interface{}{}

	// Always enforce a text match on the user's raw query (minus location)
	// This acts as a safety net if the LLM categorizes "pizza" as F&B but drops "pizza" from Category
	if params.OriginalQuery != "" {
		cleanQuery := location.StripLocationFromQuery(params.OriginalQuery)
		// Strip common filler words that might ruin strict matches
		cleanQuery = strings.ReplaceAll(cleanQuery, "franchise", "")
		cleanQuery = strings.ReplaceAll(cleanQuery, "business", "")
		cleanQuery = strings.TrimSpace(cleanQuery)

		if cleanQuery != "" {
			textMatch := map[string]interface{}{
				"bool": map[string]interface{}{
					"should": []map[string]interface{}{
						// 1. Root fields match
						{
							"multi_match": map[string]interface{}{
								"query":     cleanQuery,
								"fields":    []string{"name^5", "tags^3", "description", "industry.name^2"},
								"fuzziness": "AUTO",
							},
						},
						// 2. Categories nested match
						{
							"nested": map[string]interface{}{
								"path":            "categories",
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
						// 3. Subcategories nested match
						{
							"nested": map[string]interface{}{
								"path":            "sub_categories",
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
					},
					"minimum_should_match": 1,
				},
			}
			mustClauses = append(mustClauses, textMatch)
		}
	}

	// Industry match (must instead of should to enforce strict filtering)
	if params.Industry != "" {
		industrySlug := GetIndustrySlug(params.Industry)

		mustClauses = append(mustClauses, map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{
						"term": map[string]interface{}{
							"industry.name.keyword": params.Industry,
						},
					},
					map[string]interface{}{
						"match": map[string]interface{}{
							"industry.name": params.Industry,
						},
					},
					map[string]interface{}{
						"term": map[string]interface{}{
							"industry.slug": industrySlug,
						},
					},
				},
				"minimum_should_match": 1,
			},
		})
	}

	// Category match — ES mein category.name field nahi hai
	// industry.name, tags aur name pe match karo
	// should use karo taaki industry already match ho toh ye boost kare
	if params.Category != "" {
		shouldClauses = append(shouldClauses, map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{
						"match": map[string]interface{}{
							"industry.name": map[string]interface{}{
								"query": params.Category,
								"boost": 2,
							},
						},
					},
					map[string]interface{}{
						"match": map[string]interface{}{
							"tags": map[string]interface{}{
								"query":     params.Category,
								"fuzziness": "AUTO",
							},
						},
					},
					map[string]interface{}{
						"match": map[string]interface{}{
							"name": map[string]interface{}{
								"query":     params.Category,
								"fuzziness": "AUTO",
							},
						},
					},
					map[string]interface{}{
						"nested": map[string]interface{}{
							"path":            "categories",
							"query": map[string]interface{}{
								"match": map[string]interface{}{
									"categories.name": map[string]interface{}{
										"query":     params.Category,
										"fuzziness": "AUTO",
									},
								},
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		})
	}

	// Subcategory match
	if params.Subcategory != "" {
		shouldClauses = append(shouldClauses, map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{
						"match": map[string]interface{}{
							"tags": map[string]interface{}{
								"query":     params.Subcategory,
								"fuzziness": "AUTO",
							},
						},
					},
					map[string]interface{}{
						"match": map[string]interface{}{
							"name": map[string]interface{}{
								"query":     params.Subcategory,
								"fuzziness": "AUTO",
							},
						},
					},
					map[string]interface{}{
						"nested": map[string]interface{}{
							"path":            "sub_categories",
							"query": map[string]interface{}{
								"match": map[string]interface{}{
									"sub_categories.name": map[string]interface{}{
										"query":     params.Subcategory,
										"fuzziness": "AUTO",
									},
								},
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		})
	}

	boolQuery := map[string]interface{}{}
	if len(mustClauses) > 0 {
		boolQuery["must"] = mustClauses
	}
	if len(shouldClauses) > 0 {
		boolQuery["should"] = shouldClauses
	}

	if len(boolQuery) > 0 {
		esQuery["query"] = map[string]interface{}{
			"bool": boolQuery,
		}
	} else {
		esQuery["query"] = map[string]interface{}{
			"match_all": map[string]interface{}{},
		}
	}

	filterClauses := []interface{}{}
	// softBoosts: Investment & ROI are soft boosts (should), not hard filters.
	// This ensures results are not cut to near-zero when combining multiple criteria.
	softBoosts := []interface{}{}

	// Entity Type filter
	if params.EntityType != "" && params.EntityType != "all" {
		filterClauses = append(filterClauses, map[string]interface{}{
			"term": map[string]interface{}{"entity_type": params.EntityType},
		})
	}

	// Member Count (Associations)
	if params.MemberCount != nil {
		memRange := map[string]interface{}{}
		if params.MemberCount.Min > 0 {
			memRange["gte"] = params.MemberCount.Min
		}
		if params.MemberCount.Max > 0 {
			memRange["lte"] = params.MemberCount.Max
		}
		if len(memRange) > 0 {
			filterClauses = append(filterClauses, map[string]interface{}{
				"range": map[string]interface{}{"member_count": memRange},
			})
		}
	}

	// Membership Fee (Associations) - soft boost
	if params.MembershipFee != nil {
		if params.MembershipFee.Max > 0 {
			softBoosts = append(softBoosts, map[string]interface{}{
				"range": map[string]interface{}{
					"membership_fee_min": map[string]interface{}{"lte": params.MembershipFee.Max, "boost": 2},
				},
			})
		}
		if params.MembershipFee.Min > 0 {
			softBoosts = append(softBoosts, map[string]interface{}{
				"range": map[string]interface{}{
					"membership_fee_max": map[string]interface{}{"gte": params.MembershipFee.Min, "boost": 2},
				},
			})
		}
	}

	// Min Units / Master Franchise
	if params.MinUnits != nil && *params.MinUnits > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"total_outlets": map[string]interface{}{"gte": *params.MinUnits},
			},
		})
	}

	if params.ExclusivityType != "" {
		filterClauses = append(filterClauses, map[string]interface{}{
			"term": map[string]interface{}{"exclusivity_type": params.ExclusivityType},
		})
	}

	if params.TerritoryScope != "" {
		filterClauses = append(filterClauses, map[string]interface{}{
			"term": map[string]interface{}{"territory_scope": params.TerritoryScope},
		})
	}

	// Location filter — HARD filter (user explicitly wants these cities)
	if params.Location != nil && params.Location.City != "" {
		cities := strings.Split(params.Location.City, ",")
		var allTerms []string
		for _, city := range cities {
			allTerms = append(allTerms, location.BuildLocationTerms(strings.TrimSpace(city))...)
		}
		filterClauses = append(filterClauses, map[string]interface{}{
			"terms": map[string]interface{}{
				"location": dedupLocationTerms(allTerms),
			},
		})
	}

	// Investment — SOFT boost (prefer matching, not exclude)
	if params.Investment != nil {
		if params.Investment.Max > 0 {
			maxLakhs := params.Investment.Max / 100000
			softBoosts = append(softBoosts, map[string]interface{}{
				"range": map[string]interface{}{
					"investment.min_investment": map[string]interface{}{"lte": maxLakhs, "boost": 2},
				},
			})
		}
		if params.Investment.Min > 0 {
			minLakhs := params.Investment.Min / 100000
			softBoosts = append(softBoosts, map[string]interface{}{
				"range": map[string]interface{}{
					"investment.max_investment": map[string]interface{}{"gte": minLakhs, "boost": 2},
				},
			})
		}
	}

	if params.Space != nil {
		if params.Space.Max > 0 {
			filterClauses = append(filterClauses, map[string]interface{}{
				"range": map[string]interface{}{
					"space.minSpace": map[string]interface{}{"lte": params.Space.Max},
				},
			})
		}
		if params.Space.Min > 0 {
			filterClauses = append(filterClauses, map[string]interface{}{
				"range": map[string]interface{}{
					"space.maxSpace": map[string]interface{}{"gte": params.Space.Min},
				},
			})
		}
	}

	// ROI — SOFT boost (prefer matching, not exclude)
	if params.ROI != nil {
		if params.ROI.Min > 0 {
			softBoosts = append(softBoosts, map[string]interface{}{
				"range": map[string]interface{}{
					"roi.max": map[string]interface{}{"gte": params.ROI.Min, "boost": 3},
				},
			})
		}
		if params.ROI.Max > 0 {
			softBoosts = append(softBoosts, map[string]interface{}{
				"range": map[string]interface{}{
					"roi.min": map[string]interface{}{"lte": params.ROI.Max, "boost": 3},
				},
			})
		}
	}

	if params.Rating != nil && *params.Rating > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"rating": map[string]interface{}{"gte": *params.Rating},
			},
		})
	}

	if params.Staff != nil {
		staffRange := map[string]interface{}{}
		if params.Staff.Min > 0 {
			staffRange["gte"] = params.Staff.Min
		}
		if params.Staff.Max > 0 {
			staffRange["lte"] = params.Staff.Max
		}
		if len(staffRange) > 0 {
			filterClauses = append(filterClauses, map[string]interface{}{
				"range": map[string]interface{}{"staff": staffRange},
			})
		}
	}

	if params.Outlets != nil && *params.Outlets > 0 {
		filterClauses = append(filterClauses, map[string]interface{}{
			"range": map[string]interface{}{
				"total_outlets": map[string]interface{}{"gte": *params.Outlets},
			},
		})
	}

	// if params.Verified != nil && *params.Verified {
	// 	filterClauses = append(filterClauses, map[string]interface{}{
	// 		"term": map[string]interface{}{"verified": true},
	// 	})
	// }

	if params.TrustedSeller != nil && *params.TrustedSeller {
		softBoosts = append(softBoosts, map[string]interface{}{
			"term": map[string]interface{}{
				"trusted_seller": map[string]interface{}{
					"value": true,
					"boost": 5.0,
				},
			},
		})
	}

	// Apply hard filters and soft boosts
	currentQuery := esQuery["query"].(map[string]interface{})
	if boolQuery, hasBool := currentQuery["bool"].(map[string]interface{}); hasBool {
		if len(filterClauses) > 0 {
			boolQuery["filter"] = filterClauses
		}
		if len(softBoosts) > 0 {
			// Merge with existing should or create new
			if existingShould, ok := boolQuery["should"].([]interface{}); ok {
				boolQuery["should"] = append(existingShould, softBoosts...)
			} else {
				boolQuery["should"] = softBoosts
			}
		}
	} else if len(filterClauses) > 0 || len(softBoosts) > 0 {
		newBool := map[string]interface{}{
			"must": []interface{}{currentQuery},
		}
		if len(filterClauses) > 0 {
			newBool["filter"] = filterClauses
		}
		if len(softBoosts) > 0 {
			newBool["should"] = softBoosts
		}
		esQuery["query"] = map[string]interface{}{"bool": newBool}
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

	var varMap map[string]interface{}
	json.Unmarshal([]byte(job.Variables), &varMap)

	if input.Query == "" {
		for _, field := range []string{"query", "searchQuery", "search_query", "text"} {
			if val, ok := varMap[field].(string); ok && val != "" {
				input.Query = val
				break
			}
		}
	}

	if input.EntityType == "" {
		for _, field := range []string{"entityType", "entity_type", "entity-type"} {
			if val, ok := varMap[field].(string); ok && val != "" {
				input.EntityType = val
				break
			}
		}
	}

	if input.EntityType != "" {
		et := strings.ToLower(input.EntityType)
		switch et {
		case "franchises":
			et = "franchise"
		case "associations":
			et = "association"
		case "master-franchise", "master_franchises", "master franchises", "master franchise", "masterfranchise":
			et = "master_franchise"
		default:
			et = strings.TrimSuffix(et, "s")
			if et == "master-franchise" || et == "master franchise" || et == "masterfranchise" {
				et = "master_franchise"
			}
		}
		input.EntityType = et
	}

	return &input, nil
}

func (h *Handler) validateInput(input *SearchInput) error {
	if input == nil {
		return fmt.Errorf("input is nil")
	}
	if strings.TrimSpace(input.Query) == "" {
		h.logger.Warn("Empty query, using wildcard", nil)
		input.Query = "*"
	}
	if len(input.Query) > h.config.MaxQueryLength {
		return fmt.Errorf("query too long: %d chars (max %d)", len(input.Query), h.config.MaxQueryLength)
	}
	return nil
}

func (h *Handler) extractParametersWithFallback(ctx context.Context, input *SearchInput) *ExtractedParameters {
	if input.EntityType != "" {
		et := strings.ToLower(input.EntityType)
		if et == "master-franchise" || et == "master_franchises" || et == "master franchises" || et == "master franchise" || et == "masterfranchise" {
			et = "master_franchise"
		}
		input.EntityType = et
	}

	if input.Query == "*" || strings.TrimSpace(input.Query) == "" {
		params := &ExtractedParameters{}
		if input.EntityType != "" {
			params.EntityType = input.EntityType
		}
		return params
	}

	cacheKey := strings.ToLower(strings.TrimSpace(input.Query))
	if input.EntityType != "" {
		cacheKey = input.EntityType + ":" + cacheKey
	}

	llmCacheMu.RLock()
	cached, exists := llmCache[cacheKey]
	llmCacheMu.RUnlock()
	if exists {
		h.logger.Info("LLM parameter extraction cache hit", map[string]interface{}{
			"query":    input.Query,
			"industry": cached.Industry,
		})
		return cached
	}

	prompt := h.paramExtractor.BuildPrompt(input.Query, input.EntityType)

	response, err := h.llmService.Extract(ctx, prompt)
	if err != nil {
		h.logger.Warn("LLM failed, using fallback", map[string]interface{}{
			"error": err.Error(),
			"query": input.Query,
		})
		params := &ExtractedParameters{}
		if input.EntityType != "" {
			params.EntityType = input.EntityType
		}
		return params
	}

	// CHANGED: ParseWithFallback → ParseWithContext (original query pass karo)
	params := h.paramExtractor.ParseWithContext(response, input.Query)

	// Force sanitization of LLM hallucinated EntityTypes (e.g. "Food & Beverage")
	if params.EntityType != "" {
		et := strings.ToLower(params.EntityType)
		if strings.Contains(et, "association") {
			params.EntityType = "association"
		} else if strings.Contains(et, "master") {
			params.EntityType = "master_franchise"
		} else if strings.Contains(et, "franchise") {
			params.EntityType = "franchise"
		} else {
			params.EntityType = "" // Invalid, clear it
		}
	}

	// Fallback to payload EntityType if LLM did not extract it (or if it was cleared)
	if params.EntityType == "" {
		if input.EntityType != "" {
			et := strings.ToLower(input.EntityType)
			if et == "master-franchise" || et == "master_franchises" || et == "master franchises" || et == "master franchise" || et == "masterfranchise" {
				et = "master_franchise"
			}
			params.EntityType = et
		} else {
			if input.Query == "" || input.Query == "*" {
				params.EntityType = "all"
			} else {
				// Try to infer from query
				lowerQ := strings.ToLower(input.Query)
				if strings.Contains(lowerQ, "association") {
					params.EntityType = "association"
				} else if strings.Contains(lowerQ, "master") && strings.Contains(lowerQ, "franchise") {
					params.EntityType = "master_franchise"
				} else if strings.Contains(lowerQ, "franchise") {
					params.EntityType = "franchise"
				} else {
					params.EntityType = "all"
				}
			}
		}
	} else if input.EntityType != "" && input.EntityType != "all" {
		// If LLM extracted something but input explicitly provides an entity type, input wins
		et := strings.ToLower(input.EntityType)
		if et == "master-franchise" || et == "master_franchises" || et == "master franchises" || et == "master franchise" || et == "masterfranchise" {
			et = "master_franchise"
		}
		params.EntityType = et
	}

	h.logger.Info("Parameters extracted", map[string]interface{}{
		"industry":    params.Industry,
		"category":    params.Category,
		"entity_type": params.EntityType,
		"location": func() string {
			if params.Location != nil {
				return params.Location.City
			}
			return ""
		}(),
		"state": func() string {
			if params.Location != nil {
				return params.Location.State
			}
			return ""
		}(),
		"has_investment": params.Investment != nil,
		"has_roi":        params.ROI != nil,
	})

	llmCacheMu.Lock()
	if len(llmCache) > 1000 {
		llmCache = make(map[string]*ExtractedParameters)
	}
	llmCache[cacheKey] = params
	llmCacheMu.Unlock()

	return params
}

// func (h *Handler) extractParametersWithFallback(ctx context.Context, input *SearchInput) *ExtractedParameters {
// 	if input.Query == "*" || strings.TrimSpace(input.Query) == "" {
// 		return &ExtractedParameters{}
// 	}

// 	prompt := h.paramExtractor.BuildPrompt(input.Query)

// 	response, err := h.llmService.Extract(ctx, prompt)
// 	if err != nil {
// 		h.logger.Warn("LLM failed, using fallback", map[string]interface{}{
// 			"error": err.Error(),
// 			"query": input.Query,
// 		})
// 		return &ExtractedParameters{}
// 	}

// 	params := h.paramExtractor.ParseWithFallback(response)

// 	h.logger.Info("Parameters extracted", map[string]interface{}{
// 		"industry": params.Industry,
// 		"category": params.Category,
// 		"location_city": func() string {
// 			if params.Location != nil {
// 				return params.Location.City
// 			}
// 			return ""
// 		}(),
// 		"has_investment": params.Investment != nil,
// 		"has_space":      params.Space != nil,
// 		"has_roi":        params.ROI != nil,
// 	})

// 	return params
// }

func (h *Handler) executeSearch(ctx context.Context, query map[string]interface{}) (*SearchResults, error) {
	searchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	queryJSON, _ := json.MarshalIndent(query, "", "  ")
	h.logger.Debug("ES query", map[string]interface{}{
		"index": h.config.IndexName,
		"query": string(queryJSON),
	})

	result, err := h.esClient.Search(searchCtx, h.config.IndexName, query)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

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
		"roi":           0.0,
		"maxRoi":        0.0,
		"minRating":     0.0,
		"verified":      false,
		"trustedSeller": false,
		"tags":          []string{},
		"isAiSearch":    true,
	}

	// if params.Industry != "" {
	// 	extractedParams["industry"] = params.Industry
	// 	// industrySlug bhi set karo — BPMN Task_GetRecommended ko yahi chahiye
	// 	extractedParams["industrySlug"] = strings.ToLower(
	// 		strings.ReplaceAll(
	// 			strings.ReplaceAll(params.Industry, " & ", "-"),
	// 			" ", "-"))
	// }
	if params.Industry != "" {
		extractedParams["industry"] = params.Industry
		fullSlug := GetIndustrySlug(params.Industry) // may be "food-beverage, fashion"
		// Support multi-industry featured categories, queries, recommended
		extractedParams["industrySlug"] = fullSlug
		// Store full comma-separated slugs for multi-industry ES queries (RECOMMENDED_BY_INDUSTRY etc.)
		extractedParams["industrySlugAll"] = fullSlug
	}

	if params.Category != "" {
		extractedParams["category"] = params.Category
	}
	if params.Subcategory != "" {
		extractedParams["subcategory"] = params.Subcategory
	}

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

	if params.Location != nil && params.Location.City != "" {
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

	if params.ROI != nil {
		extractedParams["roi"] = params.ROI.Min
		extractedParams["maxRoi"] = params.ROI.Max
	}

	if params.Staff != nil {
		extractedParams["minStaff"] = params.Staff.Min
		extractedParams["maxStaff"] = params.Staff.Max
	}

	if params.Outlets != nil {
		extractedParams["minOutlets"] = *params.Outlets
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
	if params.EntityType != "" {
		extractedParams["entityType"] = params.EntityType
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

func dedupLocationTerms(terms []string) []string {
	seen := make(map[string]bool)
	var unique []string
	for _, t := range terms {
		tLower := strings.ToLower(strings.TrimSpace(t))
		if tLower != "" && !seen[tLower] {
			seen[tLower] = true
			unique = append(unique, t)
		}
	}
	return unique
}
