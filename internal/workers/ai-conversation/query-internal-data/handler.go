package queryinternaldata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/redis/go-redis/v9"
)

const (
	TaskType = "query-internal-data"
)

var (
	ErrInternalDataQueryFailed = errors.New("INTERNAL_DATA_QUERY_FAILED")
)

// Logger interface definition
type Logger interface {
	Info(msg string, fields map[string]interface{})
	Warn(msg string, fields map[string]interface{})
	Error(msg string, fields map[string]interface{})
	With(fields map[string]interface{}) Logger
}

type Handler struct {
	config      *Config
	db          *sql.DB
	esClient    *elasticsearch.Client
	redisClient *redis.Client
	logger      Logger
}

func NewHandler(config *Config, db *sql.DB, esClient *elasticsearch.Client, redisClient *redis.Client, log Logger) *Handler {
	return &Handler{
		config:      config,
		db:          db,
		esClient:    esClient,
		redisClient: redisClient,
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
		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
	defer cancel()

	output, err := h.execute(ctx, &input)
	if err != nil {
		errorCode := "INTERNAL_DATA_QUERY_FAILED"
		retries := int32(0)
		if strings.Contains(err.Error(), "postgres") || strings.Contains(err.Error(), "elasticsearch") {
			retries = 2
		}
		h.failJob(client, job, errorCode, err.Error(), retries)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	cacheKey := h.buildCacheKey(input.Entities)
	if val, err := h.redisClient.Get(ctx, cacheKey).Result(); err == nil {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(val), &data); err == nil {
			return &Output{InternalData: data}, nil
		}
	}

	filters := h.extractFilters(input.Entities)

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make(map[string]interface{})
	errChan := make(chan error, 2)

	if h.shouldQueryDB(input.DataSources) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := h.queryPostgreSQL(ctx, filters)
			if err != nil {
				errChan <- fmt.Errorf("postgres: %w", err)
				return
			}
			mu.Lock()
			for k, v := range data {
				results[k] = v
			}
			mu.Unlock()
		}()
	}

	if h.shouldQueryES(input.DataSources) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := h.queryElasticsearch(ctx, filters)
			if err != nil {
				errChan <- fmt.Errorf("elasticsearch: %w", err)
				return
			}
			mu.Lock()
			for k, v := range data {
				results[k] = v
			}
			mu.Unlock()
		}()
	}

	go func() {
		wg.Wait()
		close(errChan)
	}()

	for err := range errChan {
		return nil, fmt.Errorf("%w: %v", ErrInternalDataQueryFailed, err)
	}

	if len(results) > 0 {
		data, _ := json.Marshal(results)
		h.redisClient.Set(ctx, cacheKey, data, h.config.CacheTTL)
	}

	h.logger.Info("internal data queried successfully", map[string]interface{}{
		"entityCount": len(input.Entities),
		"resultCount": len(results),
	})

	return &Output{InternalData: results}, nil
}

func (h *Handler) buildCacheKey(entities []Entity) string {
	parts := make([]string, len(entities))
	for i, e := range entities {
		parts[i] = e.Type + ":" + e.Value
	}
	return "ai:internal:" + strings.Join(parts, "|")
}

func (h *Handler) extractFilters(entities []Entity) map[string]interface{} {
	filters := make(map[string]interface{})
	for _, entity := range entities {
		switch entity.Type {
		case "franchise_name":
			if names, ok := filters["franchise_names"].([]string); ok {
				filters["franchise_names"] = append(names, entity.Value)
			} else {
				filters["franchise_names"] = []string{entity.Value}
			}
		case "location":
			if locs, ok := filters["locations"].([]string); ok {
				filters["locations"] = append(locs, entity.Value)
			} else {
				filters["locations"] = []string{entity.Value}
			}
		case "category":
			if cats, ok := filters["categories"].([]string); ok {
				filters["categories"] = append(cats, entity.Value)
			} else {
				filters["categories"] = []string{entity.Value}
			}
		case "investment_amount":
			if amount, err := h.parseInt(entity.Value); err == nil {
				filters["investment_amount"] = amount
			}
		}
	}
	return filters
}

func (h *Handler) shouldQueryDB(dataSources []string) bool {
	for _, source := range dataSources {
		if source == "internal_db" {
			return true
		}
	}
	return false
}

func (h *Handler) shouldQueryES(dataSources []string) bool {
	for _, source := range dataSources {
		if source == "search_index" {
			return true
		}
	}
	return false
}

func (h *Handler) queryPostgreSQL(ctx context.Context, filters map[string]interface{}) (map[string]interface{}, error) {
	results := make(map[string]interface{})

	if names, ok := filters["franchise_names"].([]string); ok && len(names) > 0 {
		placeholders := make([]string, len(names))
		args := make([]interface{}, len(names))
		for i, name := range names {
			placeholders[i] = "$" + strconv.Itoa(i+1)
			args[i] = name
		}

		query := `SELECT id, name, description, investment_min, investment_max, category 
		          FROM franchises WHERE name ILIKE ANY(ARRAY[` + strings.Join(placeholders, ",") + `]) LIMIT $` + strconv.Itoa(len(names)+1)
		args = append(args, h.config.MaxResults)

		rows, err := h.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var franchises []map[string]interface{}
		for rows.Next() {
			var id, name, description, category string
			var investmentMin, investmentMax int
			err := rows.Scan(&id, &name, &description, &investmentMin, &investmentMax, &category)
			if err != nil {
				return nil, err
			}
			franchises = append(franchises, map[string]interface{}{
				"id":            id,
				"name":          name,
				"description":   description,
				"investmentMin": investmentMin,
				"investmentMax": investmentMax,
				"category":      category,
			})
		}
		results["franchises"] = franchises
	}

	if locations, ok := filters["locations"].([]string); ok && len(locations) > 0 {
		placeholders := make([]string, len(locations))
		args := make([]interface{}, len(locations))
		for i, loc := range locations {
			placeholders[i] = "$" + strconv.Itoa(i+1)
			args[i] = "%" + loc + "%"
		}

		query := `SELECT f.id, f.name, o.address, o.city, o.state 
		          FROM franchises f 
		          JOIN franchise_outlets o ON f.id = o.franchise_id 
		          WHERE o.city ILIKE ANY(ARRAY[` + strings.Join(placeholders, ",") + `]) LIMIT $` + strconv.Itoa(len(locations)+1)
		args = append(args, h.config.MaxResults)

		rows, err := h.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var outlets []map[string]interface{}
		for rows.Next() {
			var franchiseId, franchiseName, address, city, state string
			err := rows.Scan(&franchiseId, &franchiseName, &address, &city, &state)
			if err != nil {
				return nil, err
			}
			outlets = append(outlets, map[string]interface{}{
				"franchiseId":   franchiseId,
				"franchiseName": franchiseName,
				"address":       address,
				"city":          city,
				"state":         state,
			})
		}
		results["outlets"] = outlets
	}

	return results, nil
}

func (h *Handler) queryElasticsearch(ctx context.Context, filters map[string]interface{}) (map[string]interface{}, error) {
	var mustClauses []interface{}

	if names, ok := filters["franchise_names"].([]string); ok && len(names) > 0 {
		for _, name := range names {
			mustClauses = append(mustClauses, map[string]interface{}{
				"match": map[string]interface{}{"name": name},
			})
		}
	}

	if categories, ok := filters["categories"].([]string); ok && len(categories) > 0 {
		mustClauses = append(mustClauses, map[string]interface{}{
			"terms": map[string]interface{}{"category.keyword": categories},
		})
	}

	if locations, ok := filters["locations"].([]string); ok && len(locations) > 0 {
		for _, loc := range locations {
			mustClauses = append(mustClauses, map[string]interface{}{
				"match": map[string]interface{}{"locations": loc},
			})
		}
	}

	boolQuery := map[string]interface{}{"must": mustClauses}
	queryBody := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": boolQuery,
		},
		"size": h.config.MaxResults,
	}

	body, _ := json.Marshal(queryBody)
	req := esapi.SearchRequest{
		Index: []string{"franchises"},
		Body:  strings.NewReader(string(body)),
	}

	res, err := req.Do(ctx, h.esClient)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search failed: %s", res.String())
	}

	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, err
	}

	hits, ok := r["hits"].(map[string]interface{})["hits"].([]interface{})
	if !ok {
		return map[string]interface{}{"search_results": []interface{}{}}, nil
	}

	var results []map[string]interface{}
	for _, hit := range hits {
		if h, ok := hit.(map[string]interface{}); ok {
			if source, ok := h["_source"].(map[string]interface{}); ok {
				results = append(results, source)
			}
		}
	}

	return map[string]interface{}{"search_results": results}, nil
}

func (h *Handler) parseInt(s string) (int, error) {
	re := regexp.MustCompile(`[^\d]`)
	clean := re.ReplaceAllString(s, "")
	if clean == "" {
		return 0, errors.New("not a number")
	}
	return strconv.Atoi(clean)
}

func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}
	_, err = cmd.Send(context.Background())
	if err != nil {
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error": err.Error(),
		})
	}
}

func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, _ int32) {
	h.logger.Error("job failed", map[string]interface{}{
		"jobKey":       job.Key,
		"errorCode":    errorCode,
		"errorMessage": errorMessage,
	})

	_, err := client.NewThrowErrorCommand().
		JobKey(job.Key).
		ErrorCode(errorCode).
		ErrorMessage(errorMessage).
		Send(context.Background())
	if err != nil {
		h.logger.Error("failed to throw error", map[string]interface{}{
			"error": err.Error(),
		})
	}
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

// // internal/workers/ai-conversation/query-internal-data/handler.go
// package queryinternaldata

// import (
// 	"context"
// 	"database/sql"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"regexp"
// 	"strconv"
// 	"strings"
// 	"sync"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"github.com/elastic/go-elasticsearch/v8"
// 	"github.com/elastic/go-elasticsearch/v8/esapi"
// 	"github.com/redis/go-redis/v9"
// 	"go.uber.org/zap"
// )

// const (
// 	TaskType = "query-internal-data"
// )

// var (
// 	ErrInternalDataQueryFailed = errors.New("INTERNAL_DATA_QUERY_FAILED")
// )

// type Handler struct {
// 	config      *Config
// 	db          *sql.DB
// 	esClient    *elasticsearch.Client
// 	redisClient *redis.Client
// 	logger      *zap.Logger
// }

// func NewHandler(config *Config, db *sql.DB, esClient *elasticsearch.Client, redisClient *redis.Client, logger *zap.Logger) *Handler {
// 	return &Handler{
// 		config:      config,
// 		db:          db,
// 		esClient:    esClient,
// 		redisClient: redisClient,
// 		logger:      logger.With(zap.String("taskType", TaskType)),
// 	}
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	h.logger.Info("processing job",
// 		zap.Int64("jobKey", job.Key),
// 		zap.Int64("workflowKey", job.ProcessInstanceKey),
// 	)

// 	var input Input
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
// 		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
// 		return
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		errorCode := "INTERNAL_DATA_QUERY_FAILED"
// 		retries := int32(0)
// 		if strings.Contains(err.Error(), "postgres") || strings.Contains(err.Error(), "elasticsearch") {
// 			retries = 2
// 		}
// 		h.failJob(client, job, errorCode, err.Error(), retries)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	cacheKey := h.buildCacheKey(input.Entities)
// 	if val, err := h.redisClient.Get(ctx, cacheKey).Result(); err == nil {
// 		var data map[string]interface{}
// 		if err := json.Unmarshal([]byte(val), &data); err == nil {
// 			return &Output{InternalData: data}, nil
// 		}
// 	}

// 	filters := h.extractFilters(input.Entities)

// 	var wg sync.WaitGroup
// 	var mu sync.Mutex
// 	results := make(map[string]interface{})
// 	errChan := make(chan error, 2)

// 	if h.shouldQueryDB(input.DataSources) {
// 		wg.Add(1)
// 		go func() {
// 			defer wg.Done()
// 			data, err := h.queryPostgreSQL(ctx, filters)
// 			if err != nil {
// 				errChan <- fmt.Errorf("postgres: %w", err)
// 				return
// 			}
// 			mu.Lock()
// 			for k, v := range data {
// 				results[k] = v
// 			}
// 			mu.Unlock()
// 		}()
// 	}

// 	if h.shouldQueryES(input.DataSources) {
// 		wg.Add(1)
// 		go func() {
// 			defer wg.Done()
// 			data, err := h.queryElasticsearch(ctx, filters)
// 			if err != nil {
// 				errChan <- fmt.Errorf("elasticsearch: %w", err)
// 				return
// 			}
// 			mu.Lock()
// 			for k, v := range data {
// 				results[k] = v
// 			}
// 			mu.Unlock()
// 		}()
// 	}

// 	go func() {
// 		wg.Wait()
// 		close(errChan)
// 	}()

// 	for err := range errChan {
// 		return nil, fmt.Errorf("%w: %v", ErrInternalDataQueryFailed, err)
// 	}

// 	if len(results) > 0 {
// 		data, _ := json.Marshal(results)
// 		h.redisClient.Set(ctx, cacheKey, data, h.config.CacheTTL)
// 	}

// 	h.logger.Info("internal data queried successfully",
// 		zap.Int("entityCount", len(input.Entities)),
// 		zap.Int("resultCount", len(results)),
// 	)

// 	return &Output{InternalData: results}, nil
// }

// func (h *Handler) buildCacheKey(entities []Entity) string {
// 	parts := make([]string, len(entities))
// 	for i, e := range entities {
// 		parts[i] = e.Type + ":" + e.Value
// 	}
// 	return "ai:internal:" + strings.Join(parts, "|")
// }

// func (h *Handler) extractFilters(entities []Entity) map[string]interface{} {
// 	filters := make(map[string]interface{})
// 	for _, entity := range entities {
// 		switch entity.Type {
// 		case "franchise_name":
// 			if names, ok := filters["franchise_names"].([]string); ok {
// 				filters["franchise_names"] = append(names, entity.Value)
// 			} else {
// 				filters["franchise_names"] = []string{entity.Value}
// 			}
// 		case "location":
// 			if locs, ok := filters["locations"].([]string); ok {
// 				filters["locations"] = append(locs, entity.Value)
// 			} else {
// 				filters["locations"] = []string{entity.Value}
// 			}
// 		case "category":
// 			if cats, ok := filters["categories"].([]string); ok {
// 				filters["categories"] = append(cats, entity.Value)
// 			} else {
// 				filters["categories"] = []string{entity.Value}
// 			}
// 		case "investment_amount":
// 			if amount, err := h.parseInt(entity.Value); err == nil {
// 				filters["investment_amount"] = amount
// 			}
// 		}
// 	}
// 	return filters
// }

// func (h *Handler) shouldQueryDB(dataSources []string) bool {
// 	for _, source := range dataSources {
// 		if source == "internal_db" {
// 			return true
// 		}
// 	}
// 	return false
// }

// func (h *Handler) shouldQueryES(dataSources []string) bool {
// 	for _, source := range dataSources {
// 		if source == "search_index" {
// 			return true
// 		}
// 	}
// 	return false
// }

// func (h *Handler) queryPostgreSQL(ctx context.Context, filters map[string]interface{}) (map[string]interface{}, error) {
// 	results := make(map[string]interface{})

// 	if names, ok := filters["franchise_names"].([]string); ok && len(names) > 0 {
// 		placeholders := make([]string, len(names))
// 		args := make([]interface{}, len(names))
// 		for i, name := range names {
// 			placeholders[i] = "$" + strconv.Itoa(i+1)
// 			args[i] = name
// 		}

// 		query := `SELECT id, name, description, investment_min, investment_max, category
// 		          FROM franchises WHERE name ILIKE ANY(ARRAY[` + strings.Join(placeholders, ",") + `]) LIMIT $` + strconv.Itoa(len(names)+1)
// 		args = append(args, h.config.MaxResults)

// 		rows, err := h.db.QueryContext(ctx, query, args...)
// 		if err != nil {
// 			return nil, err
// 		}
// 		defer rows.Close()

// 		var franchises []map[string]interface{}
// 		for rows.Next() {
// 			var id, name, description, category string
// 			var investmentMin, investmentMax int
// 			err := rows.Scan(&id, &name, &description, &investmentMin, &investmentMax, &category)
// 			if err != nil {
// 				return nil, err
// 			}
// 			franchises = append(franchises, map[string]interface{}{
// 				"id":            id,
// 				"name":          name,
// 				"description":   description,
// 				"investmentMin": investmentMin,
// 				"investmentMax": investmentMax,
// 				"category":      category,
// 			})
// 		}
// 		results["franchises"] = franchises
// 	}

// 	if locations, ok := filters["locations"].([]string); ok && len(locations) > 0 {
// 		placeholders := make([]string, len(locations))
// 		args := make([]interface{}, len(locations))
// 		for i, loc := range locations {
// 			placeholders[i] = "$" + strconv.Itoa(i+1)
// 			args[i] = "%" + loc + "%"
// 		}

// 		query := `SELECT f.id, f.name, o.address, o.city, o.state
// 		          FROM franchises f
// 		          JOIN franchise_outlets o ON f.id = o.franchise_id
// 		          WHERE o.city ILIKE ANY(ARRAY[` + strings.Join(placeholders, ",") + `]) LIMIT $` + strconv.Itoa(len(locations)+1)
// 		args = append(args, h.config.MaxResults)

// 		rows, err := h.db.QueryContext(ctx, query, args...)
// 		if err != nil {
// 			return nil, err
// 		}
// 		defer rows.Close()

// 		var outlets []map[string]interface{}
// 		for rows.Next() {
// 			var franchiseId, franchiseName, address, city, state string
// 			err := rows.Scan(&franchiseId, &franchiseName, &address, &city, &state)
// 			if err != nil {
// 				return nil, err
// 			}
// 			outlets = append(outlets, map[string]interface{}{
// 				"franchiseId":   franchiseId,
// 				"franchiseName": franchiseName,
// 				"address":       address,
// 				"city":          city,
// 				"state":         state,
// 			})
// 		}
// 		results["outlets"] = outlets
// 	}

// 	return results, nil
// }

// func (h *Handler) queryElasticsearch(ctx context.Context, filters map[string]interface{}) (map[string]interface{}, error) {
// 	var mustClauses []interface{}

// 	if names, ok := filters["franchise_names"].([]string); ok && len(names) > 0 {
// 		for _, name := range names {
// 			mustClauses = append(mustClauses, map[string]interface{}{
// 				"match": map[string]interface{}{"name": name},
// 			})
// 		}
// 	}

// 	if categories, ok := filters["categories"].([]string); ok && len(categories) > 0 {
// 		mustClauses = append(mustClauses, map[string]interface{}{
// 			"terms": map[string]interface{}{"category.keyword": categories},
// 		})
// 	}

// 	if locations, ok := filters["locations"].([]string); ok && len(locations) > 0 {
// 		for _, loc := range locations {
// 			mustClauses = append(mustClauses, map[string]interface{}{
// 				"match": map[string]interface{}{"locations": loc},
// 			})
// 		}
// 	}

// 	boolQuery := map[string]interface{}{"must": mustClauses}
// 	queryBody := map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"bool": boolQuery,
// 		},
// 		"size": h.config.MaxResults,
// 	}

// 	body, _ := json.Marshal(queryBody)
// 	req := esapi.SearchRequest{
// 		Index: []string{"franchises"},
// 		Body:  strings.NewReader(string(body)),
// 	}

// 	res, err := req.Do(ctx, h.esClient)
// 	if err != nil {
// 		return nil, err
// 	}
// 	defer res.Body.Close()

// 	if res.IsError() {
// 		return nil, fmt.Errorf("search failed: %s", res.String())
// 	}

// 	var r map[string]interface{}
// 	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
// 		return nil, err
// 	}

// 	hits, ok := r["hits"].(map[string]interface{})["hits"].([]interface{})
// 	if !ok {
// 		return map[string]interface{}{"search_results": []interface{}{}}, nil
// 	}

// 	var results []map[string]interface{}
// 	for _, hit := range hits {
// 		if h, ok := hit.(map[string]interface{}); ok {
// 			if source, ok := h["_source"].(map[string]interface{}); ok {
// 				results = append(results, source)
// 			}
// 		}
// 	}

// 	return map[string]interface{}{"search_results": results}, nil
// }

// func (h *Handler) parseInt(s string) (int, error) {
// 	re := regexp.MustCompile(`[^\d]`)
// 	clean := re.ReplaceAllString(s, "")
// 	if clean == "" {
// 		return 0, errors.New("not a number")
// 	}
// 	return strconv.Atoi(clean)
// }

// func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
// 	cmd, err := client.NewCompleteJobCommand().
// 		JobKey(job.Key).
// 		VariablesFromObject(output)
// 	if err != nil {
// 		h.logger.Error("failed to create complete job command", zap.Error(err))
// 		return
// 	}
// 	_, err = cmd.Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to send complete job command", zap.Error(err))
// 	}
// }

// func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, _ int32) {
// 	h.logger.Error("job failed",
// 		zap.Int64("jobKey", job.Key),
// 		zap.String("errorCode", errorCode),
// 		zap.String("errorMessage", errorMessage),
// 	)

// 	_, err := client.NewThrowErrorCommand().
// 		JobKey(job.Key).
// 		ErrorCode(errorCode).
// 		ErrorMessage(errorMessage).
// 		Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to throw error", zap.Error(err))
// 	}
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }
