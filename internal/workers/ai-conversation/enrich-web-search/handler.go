// internal/workers/ai-conversation/enrich-web-search/handler.go
package enrichwebsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const (
	TaskType = "enrich-web-search"
)

var (
	ErrWebSearchTimeout = errors.New("WEB_SEARCH_TIMEOUT")
)

// Logger interface definition
type Logger interface {
	Info(msg string, fields map[string]interface{})
	Warn(msg string, fields map[string]interface{})
	Error(msg string, fields map[string]interface{})
	With(fields map[string]interface{}) Logger
}

type Handler struct {
	config *Config
	client *http.Client
	logger Logger
}

func NewHandler(config *Config, log Logger) *Handler {
	return &Handler{
		config: config,
		client: &http.Client{
			Timeout: config.Timeout,
		},
		logger: log.With(map[string]interface{}{
			"taskType": TaskType,
		}),
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	h.logger.Info("processing job", map[string]interface{}{
		"jobKey":      job.Key,
		"workflowKey": job.ProcessInstanceKey,
	})

	var input Input
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		h.failJob(client, job, fmt.Errorf("parse input: %w", err), 0)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
	defer cancel()

	output, err := h.execute(ctx, &input)
	if err != nil {
		if errors.Is(err, ErrWebSearchTimeout) {
			h.failJob(client, job, err, 0)
		} else {
			h.logger.Warn("web search failed, returning empty results", map[string]interface{}{
				"error": err.Error(),
			})
			output = &Output{WebData: WebData{Sources: []Source{}, Summary: ""}}
			h.completeJob(client, job, output)
		}
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	query := h.buildQuery(input.Question, input.Entities)
	searchURL := h.buildSearchURL(query)

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := h.client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded ||
			strings.Contains(err.Error(), "timeout") ||
			strings.Contains(err.Error(), "deadline") ||
			strings.Contains(err.Error(), "Client.Timeout") {
			return nil, ErrWebSearchTimeout
		}
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search API returned %d", resp.StatusCode)
	}

	var apiResponse struct {
		Items []struct {
			Link    string `json:"link"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
			Mime    string `json:"mime"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, err
	}

	// Convert apiResponse.Items to the expected type for processResults
	items := make([]struct {
		Link    string
		Title   string
		Snippet string
		Mime    string
	}, len(apiResponse.Items))

	for i, item := range apiResponse.Items {
		items[i] = struct {
			Link    string
			Title   string
			Snippet string
			Mime    string
		}{
			Link:    item.Link,
			Title:   item.Title,
			Snippet: item.Snippet,
			Mime:    item.Mime,
		}
	}

	sources := h.processResults(items)
	summary := h.generateSummary(sources)

	h.logger.Info("web search completed", map[string]interface{}{
		"query":       query,
		"resultCount": len(sources),
	})

	return &Output{
		WebData: WebData{
			Sources: sources,
			Summary: summary,
		},
	}, nil
}

func (h *Handler) buildQuery(question string, entities []Entity) string {
	query := question

	// Add entity values
	for _, entity := range entities {
		if entity.Type == "franchise_name" || entity.Type == "location" || entity.Type == "category" {
			query += " " + entity.Value
		}
	}

	// Clean and deduplicate
	query = regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(query), " ")
	return query
}

func (h *Handler) buildSearchURL(query string) string {
	baseURL, _ := url.Parse(h.config.SearchAPIBaseURL)
	params := url.Values{}
	params.Add("key", h.config.SearchAPIKey)
	params.Add("cx", h.config.SearchEngineID)
	params.Add("q", query)
	params.Add("num", fmt.Sprintf("%d", h.config.MaxResults))
	baseURL.RawQuery = params.Encode()
	return baseURL.String()
}

func (h *Handler) processResults(items []struct {
	Link    string
	Title   string
	Snippet string
	Mime    string
}) []Source {
	seen := make(map[string]bool)
	var sources []Source

	for _, item := range items {
		// Skip non-HTML
		if item.Mime != "" && !strings.Contains(item.Mime, "html") {
			continue
		}

		// Dedupe by URL
		if seen[item.Link] {
			continue
		}
		seen[item.Link] = true

		// Calculate relevance (simplified)
		relevance := 1.0
		if strings.Contains(item.Link, ".gov") || strings.Contains(item.Link, ".edu") {
			relevance += 0.2
		}
		if strings.Contains(strings.ToLower(item.Title), "official") {
			relevance += 0.1
		}

		if relevance >= h.config.MinRelevance {
			sources = append(sources, Source{
				URL:       item.Link,
				Title:     item.Title,
				Snippet:   item.Snippet,
				Relevance: relevance,
			})
		}
	}

	// Sort by relevance
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].Relevance > sources[j].Relevance
	})

	// Limit results
	if len(sources) > h.config.MaxResults {
		sources = sources[:h.config.MaxResults]
	}

	return sources
}

func (h *Handler) generateSummary(sources []Source) string {
	if len(sources) == 0 {
		return ""
	}
	// In real system, use LLM or summarization model
	// For now, return first snippet
	return sources[0].Snippet
}

func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)

	if err != nil {
		h.logger.Error("Failed to complete job", map[string]interface{}{
			"jobKey": job.Key,
			"error":  err.Error(),
		})
	}

	if _, sendErr := cmd.Send(context.Background()); sendErr != nil {
		h.logger.Error("Failed to send complete job", map[string]interface{}{
			"jobKey": job.Key,
			"error":  sendErr.Error(),
		})
	}
}

func (h *Handler) failJob(client worker.JobClient, job entities.Job, err error, retries int32) {
	errorCode := "UNKNOWN_ERROR"
	if errors.Is(err, ErrWebSearchTimeout) {
		errorCode = "WEB_SEARCH_TIMEOUT"
	}

	h.logger.Error("job failed", map[string]interface{}{
		"jobKey":    job.Key,
		"error":     err.Error(),
		"errorCode": errorCode,
	})

	_, _ = client.NewFailJobCommand().
		JobKey(job.Key).
		Retries(retries).
		ErrorMessage(err.Error()).
		Send(context.Background())
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

// // internal/workers/ai-conversation/enrich-web-search/handler.go
// package enrichwebsearch

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"net/http"
// 	"net/url"
// 	"regexp"
// 	"sort"
// 	"strings"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"go.uber.org/zap"
// )

// const (
// 	TaskType = "enrich-web-search"
// )

// var (
// 	ErrWebSearchTimeout = errors.New("WEB_SEARCH_TIMEOUT")
// )

// type Handler struct {
// 	config *Config
// 	client *http.Client
// 	logger *zap.Logger
// }

// func NewHandler(config *Config, logger *zap.Logger) *Handler {
// 	return &Handler{
// 		config: config,
// 		client: &http.Client{
// 			Timeout: config.Timeout,
// 		},
// 		logger: logger.With(zap.String("taskType", TaskType)),
// 	}
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	h.logger.Info("processing job",
// 		zap.Int64("jobKey", job.Key),
// 		zap.Int64("workflowKey", job.ProcessInstanceKey))

// 	var input Input
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
// 		h.failJob(client, job, fmt.Errorf("parse input: %w", err), 0)
// 		return
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		if errors.Is(err, ErrWebSearchTimeout) {
// 			h.failJob(client, job, err, 0)
// 		} else {
// 			h.logger.Warn("web search failed, returning empty results", zap.Error(err))
// 			output = &Output{WebData: WebData{Sources: []Source{}, Summary: ""}}
// 			h.completeJob(client, job, output)
// 		}
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	query := h.buildQuery(input.Question, input.Entities)
// 	searchURL := h.buildSearchURL(query)

// 	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
// 	if err != nil {
// 		return nil, err
// 	}

// 	resp, err := h.client.Do(req)
// 	if err != nil {
// 		// if ctx.Err() == context.DeadlineExceeded {
// 		// 	return nil, ErrWebSearchTimeout
// 		// }
// 		if ctx.Err() == context.DeadlineExceeded ||
// 			strings.Contains(err.Error(), "timeout") ||
// 			strings.Contains(err.Error(), "deadline") ||
// 			strings.Contains(err.Error(), "Client.Timeout") {
// 			return nil, ErrWebSearchTimeout
// 		}
// 		return nil, err
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK {
// 		return nil, fmt.Errorf("search API returned %d", resp.StatusCode)
// 	}

// 	var apiResponse struct {
// 		Items []struct {
// 			Link    string `json:"link"`
// 			Title   string `json:"title"`
// 			Snippet string `json:"snippet"`
// 			Mime    string `json:"mime"`
// 			//FormattedUrl string `json:"formattedUrl"`
// 		} `json:"items"`
// 	}

// 	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
// 		return nil, err
// 	}

// 	// Convert apiResponse.Items to the expected type for processResults
// 	items := make([]struct {
// 		Link    string
// 		Title   string
// 		Snippet string
// 		Mime    string
// 	}, len(apiResponse.Items))

// 	for i, item := range apiResponse.Items {
// 		items[i] = struct {
// 			Link    string
// 			Title   string
// 			Snippet string
// 			Mime    string
// 		}{
// 			Link:    item.Link,
// 			Title:   item.Title,
// 			Snippet: item.Snippet,
// 			Mime:    item.Mime,
// 		}
// 	}

// 	sources := h.processResults(items)
// 	summary := h.generateSummary(sources)

// 	h.logger.Info("web search completed",
// 		zap.String("query", query),
// 		zap.Int("resultCount", len(sources)))

// 	return &Output{
// 		WebData: WebData{
// 			Sources: sources,
// 			Summary: summary,
// 		},
// 	}, nil
// }

// func (h *Handler) buildQuery(question string, entities []Entity) string {
// 	query := question

// 	// Add entity values
// 	for _, entity := range entities {
// 		if entity.Type == "franchise_name" || entity.Type == "location" || entity.Type == "category" {
// 			query += " " + entity.Value
// 		}
// 	}

// 	// Clean and deduplicate
// 	query = regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(query), " ")
// 	return query
// }

// func (h *Handler) buildSearchURL(query string) string {
// 	baseURL, _ := url.Parse(h.config.SearchAPIBaseURL)
// 	params := url.Values{}
// 	params.Add("key", h.config.SearchAPIKey)
// 	params.Add("cx", h.config.SearchEngineID)
// 	params.Add("q", query)
// 	params.Add("num", fmt.Sprintf("%d", h.config.MaxResults))
// 	baseURL.RawQuery = params.Encode()
// 	return baseURL.String()
// }

// func (h *Handler) processResults(items []struct {
// 	Link    string
// 	Title   string
// 	Snippet string
// 	Mime    string
// 	//FormattedUrl string
// }) []Source {
// 	seen := make(map[string]bool)
// 	var sources []Source

// 	for _, item := range items {
// 		// Skip non-HTML
// 		if item.Mime != "" && !strings.Contains(item.Mime, "html") {
// 			continue
// 		}

// 		// Dedupe by URL
// 		if seen[item.Link] {
// 			continue
// 		}
// 		seen[item.Link] = true

// 		// Calculate relevance (simplified)
// 		relevance := 1.0
// 		if strings.Contains(item.Link, ".gov") || strings.Contains(item.Link, ".edu") {
// 			relevance += 0.2
// 		}
// 		if strings.Contains(strings.ToLower(item.Title), "official") {
// 			relevance += 0.1
// 		}

// 		if relevance >= h.config.MinRelevance {
// 			sources = append(sources, Source{
// 				URL:       item.Link,
// 				Title:     item.Title,
// 				Snippet:   item.Snippet,
// 				Relevance: relevance,
// 			})
// 		}
// 	}

// 	// Sort by relevance
// 	sort.Slice(sources, func(i, j int) bool {
// 		return sources[i].Relevance > sources[j].Relevance
// 	})

// 	// Limit results
// 	if len(sources) > h.config.MaxResults {
// 		sources = sources[:h.config.MaxResults]
// 	}

// 	return sources
// }

// func (h *Handler) generateSummary(sources []Source) string {
// 	if len(sources) == 0 {
// 		return ""
// 	}
// 	// In real system, use LLM or summarization model
// 	// For now, return first snippet
// 	return sources[0].Snippet
// }

// func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
// 	cmd, err := client.NewCompleteJobCommand().
// 		JobKey(job.Key).
// 		VariablesFromObject(output)

// 	if err != nil {
// 		h.logger.Error("Failed to complete job",
// 			zap.Int64("jobKey", job.Key),
// 			zap.Error(err))
// 	}

// 	//_, err = cmd.Send(context.Background())

// 	if _, sendErr := cmd.Send(context.Background()); sendErr != nil {
// 		h.logger.Error("Failed to send complete job",
// 			zap.Int64("jobKey", job.Key),
// 			zap.Error(sendErr))
// 	}
// }

// func (h *Handler) failJob(client worker.JobClient, job entities.Job, err error, retries int32) {
// 	errorCode := "UNKNOWN_ERROR"
// 	if errors.Is(err, ErrWebSearchTimeout) {
// 		errorCode = "WEB_SEARCH_TIMEOUT"
// 	}

// 	h.logger.Error("job failed",
// 		zap.Int64("jobKey", job.Key),
// 		zap.Error(err),
// 		zap.String("errorCode", errorCode))

// 	_, _ = client.NewFailJobCommand().
// 		JobKey(job.Key).
// 		Retries(retries).
// 		ErrorMessage(err.Error()).
// 		Send(context.Background())
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }
