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

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/validation"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
	config       *Config
	db           *sql.DB
	esClient     *elasticsearch.Client
	redisClient  *redis.Client
	logger       Logger
	errorHandler *appErrs.ErrorHandler
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
}

func NewHandler(config *Config, db *sql.DB, esClient *elasticsearch.Client, redisClient *redis.Client, log Logger) *Handler {
	return &Handler{
		config:       config,
		db:           db,
		esClient:     esClient,
		redisClient:  redisClient,
		logger:       log.With(map[string]interface{}{"taskType": TaskType}),
		errorHandler: appErrs.NewErrorHandler(log),
		validator:    validation.NewValidator(),
		sanitizer:    validation.NewSanitizer(),
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	// ✅ EXTRACT TRACE CONTEXT
	ctx := context.Background()

	var traceID, parentSpanID string
	var jobVars map[string]interface{}

	if err := json.Unmarshal([]byte(job.Variables), &jobVars); err == nil {
		if tid, ok := jobVars["traceId"].(string); ok {
			traceID = tid
		}
		if psid, ok := jobVars["spanId"].(string); ok {
			parentSpanID = psid
		}
	}

	// ✅ CREATE WORKER SPAN
	tracer := otel.Tracer("worker-manager")
	ctx, span := tracer.Start(ctx, "worker:"+TaskType,
		trace.WithAttributes(
			attribute.String("worker.name", TaskType),
			attribute.Int64("job.key", job.GetKey()),
			attribute.Int64("workflow.instance_key", job.GetProcessInstanceKey()),
			attribute.String("workflow.process_id", job.GetBpmnProcessId()),
			attribute.String("workflow.element_id", job.GetElementId()),
			attribute.String("trace.parent_id", parentSpanID),
		),
	)
	defer span.End()

	h.logger.Info("processing job", map[string]interface{}{
		"jobKey":      job.Key,
		"workflowKey": job.ProcessInstanceKey,
		"traceId":     traceID,
		"spanId":      span.SpanContext().SpanID().String(),
	})

	// ===== STEP 1: PARSE INPUT =====
	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "query-internal-data.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewBusinessRuleError("Parse input failed", fmt.Sprintf("%v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== STEP 2: VALIDATE INPUT =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "query-internal-data.validateInput")
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	// ===== STEP 3: EXECUTE BUSINESS LOGIC =====
	ctxExec, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()

	_, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "query-internal-data.Execute")
	output, err := h.execute(ctxExec, &input)
	spanExec.End()

	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))

		var stdErr *appErrs.StandardError
		if strings.Contains(err.Error(), "postgres") {
			stdErr = appErrs.NewQueryExecutionFailedError("postgres", err)
		} else if strings.Contains(err.Error(), "elasticsearch") {
			stdErr = appErrs.NewSearchQueryFailedError("internal-data", err)
		} else {
			stdErr = appErrs.NewExternalServiceError("query-internal-data", err)
		}
		h.errorHandler.HandleJobError(ctx, client, job, stdErr)
		return
	}

	// ===== STEP 4: COMPLETE JOB =====
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "query-internal-data.completeJob")
	h.completeJob(ctx, client, job, output)
	spanComp.End()
}

// ===== CRITICAL VALIDATION FUNCTION =====
func (h *Handler) validateInput(input *Input) error {
	// Validate Entities array
	if len(input.Entities) == 0 {
		return appErrs.NewValidationError("entities", "at least one entity is required")
	}

	if len(input.Entities) > 50 {
		return appErrs.NewArrayTooLargeError("entities", 50, len(input.Entities))
	}

	// Validate each entity
	for i, entity := range input.Entities {
		// Validate entity type (using string validation directly)
		if entity.Type == "" {
			return appErrs.NewValidationError(
				fmt.Sprintf("entities[%d].type", i),
				"entity type is required",
			)
		}

		if len(entity.Type) > 50 {
			return appErrs.NewValidationError(
				fmt.Sprintf("entities[%d].type", i),
				"entity type must be 50 characters or less",
			)
		}

		// Validate against allowed entity types
		allowedTypes := []string{
			"franchise_name", "location", "category",
			"investment_amount", "industry", "business_type",
			"franchise_id", "user_id", "application_id",
		}

		typeValid := false
		for _, allowed := range allowedTypes {
			if entity.Type == allowed {
				typeValid = true
				break
			}
		}

		if !typeValid {
			return appErrs.NewValidationError(
				fmt.Sprintf("entities[%d].type", i),
				fmt.Sprintf("must be one of: %v", allowedTypes),
			)
		}

		// Validate entity value
		if entity.Value == "" {
			return appErrs.NewValidationError(
				fmt.Sprintf("entities[%d].value", i),
				"entity value is required",
			)
		}

		if len(entity.Value) > 200 {
			return appErrs.NewValidationError(
				fmt.Sprintf("entities[%d].value", i),
				"entity value must be 200 characters or less",
			)
		}

		// Check for SQL injection in entity value
		if h.containsSQLInjection(entity.Value) {
			return appErrs.NewValidationError(
				fmt.Sprintf("entities[%d].value", i),
				"entity value contains potentially unsafe SQL patterns",
			)
		}

		// Check for NoSQL injection in entity value
		if h.containsNoSQLInjection(entity.Value) {
			return appErrs.NewValidationError(
				fmt.Sprintf("entities[%d].value", i),
				"entity value contains potentially unsafe NoSQL patterns",
			)
		}

		// 🔒 Validate entity type-value combination for security
		if err := h.validateEntityTypeValue(entity.Type, entity.Value); err != nil {
			return appErrs.NewValidationError(
				fmt.Sprintf("entities[%d]", i),
				err.Error(),
			)
		}
	}

	// Validate DataSources array
	if len(input.DataSources) == 0 {
		return appErrs.NewValidationError("dataSources", "at least one data source is required")
	}

	if len(input.DataSources) > 5 {
		return appErrs.NewArrayTooLargeError("dataSources", 5, len(input.DataSources))
	}

	// Validate each data source
	allowedSources := []string{
		"internal_db", "search_index", "cache",
		"postgresql", "elasticsearch", "redis",
	}

	for i, source := range input.DataSources {
		if source == "" {
			return appErrs.NewValidationError(
				fmt.Sprintf("dataSources[%d]", i),
				"data source cannot be empty",
			)
		}

		if len(source) > 50 {
			return appErrs.NewValidationError(
				fmt.Sprintf("dataSources[%d]", i),
				"data source must be 50 characters or less",
			)
		}

		sourceValid := false
		for _, allowed := range allowedSources {
			if source == allowed {
				sourceValid = true
				break
			}
		}

		if !sourceValid {
			return appErrs.NewValidationError(
				fmt.Sprintf("dataSources[%d]", i),
				fmt.Sprintf("must be one of: %v", allowedSources),
			)
		}

		// Check data source for unsafe characters
		if !regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`).MatchString(source) {
			return appErrs.NewValidationError(
				fmt.Sprintf("dataSources[%d]", i),
				"data source contains invalid characters",
			)
		}
	}

	return nil
}

// ===== HELPER: Validate Entity Type-Value Combination =====
func (h *Handler) validateEntityTypeValue(entityType, value string) error {
	// Prevent script injection in values
	dangerousPatterns := []string{
		"<script", "</script>", "javascript:", "onload=", "onerror=",
		"eval(", "alert(", "document.cookie", "data:text/html",
	}

	lowerValue := strings.ToLower(value)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(lowerValue, pattern) {
			return fmt.Errorf("entity value contains potentially unsafe content")
		}
	}

	// Additional validations based on entity type
	switch entityType {
	case "franchise_name", "business_type", "industry":
		// Business names can have some special characters
		if !regexp.MustCompile(`^[a-zA-Z0-9\s\&\.\-\'\,\"\(\)]+$`).MatchString(value) {
			return fmt.Errorf("%s contains invalid characters", entityType)
		}

	case "location":
		// Location should not contain special characters except spaces, commas, hyphens
		if !regexp.MustCompile(`^[a-zA-Z\s\-,]+$`).MatchString(value) {
			return fmt.Errorf("location must contain only letters, spaces, commas, and hyphens")
		}

	case "category":
		// Categories should be alphanumeric with spaces
		if !regexp.MustCompile(`^[a-zA-Z0-9\s\-]+$`).MatchString(value) {
			return fmt.Errorf("category contains invalid characters")
		}

	case "investment_amount":
		// Should be numeric or contain numbers
		if !regexp.MustCompile(`^\$?\d+(?:,\d{3})*(?:\.\d{2})?$`).MatchString(value) &&
			!regexp.MustCompile(`^\d+\s*(?:k|K|thousand|million|billion)?$`).MatchString(value) {
			return fmt.Errorf("investment_amount must be a valid number or currency")
		}

	case "franchise_id", "user_id", "application_id":
		// Should be UUID format
		if !regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`).MatchString(strings.ToLower(value)) {
			return fmt.Errorf("%s must be a valid UUID v4", entityType)
		}
	}

	return nil
}

// ===== HELPER: Detect SQL Injection =====
func (h *Handler) containsSQLInjection(value string) bool {
	sqlPatterns := []string{
		"' OR '1'='1", "' OR '1'='1' --", "'; DROP TABLE", "UNION SELECT",
		"INSERT INTO", "UPDATE", "DELETE FROM", "EXEC", "EXECUTE",
		"SELECT * FROM", "CREATE TABLE", "ALTER TABLE", "TRUNCATE TABLE",
		"xp_", "sp_", "--", "/*", "*/", ";",
	}

	lowerValue := strings.ToLower(value)
	for _, pattern := range sqlPatterns {
		if strings.Contains(lowerValue, strings.ToLower(pattern)) {
			return true
		}
	}

	return false
}

// ===== HELPER: Detect NoSQL Injection =====
func (h *Handler) containsNoSQLInjection(value string) bool {
	nosqlPatterns := []string{
		"$where", "$ne", "$gt", "$regex", "$exists", "$type",
		"$expr", "$jsonSchema", "script:", "function(", "eval(",
	}

	lowerValue := strings.ToLower(value)
	for _, pattern := range nosqlPatterns {
		if strings.Contains(lowerValue, pattern) {
			return true
		}
	}

	return false
}

// ===== ORIGINAL EXECUTE METHOD (with sanitization added) =====
func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	// 🔒 Sanitize input before processing
	for i := range input.Entities {
		input.Entities[i].Type = h.sanitizer.SanitizeString(input.Entities[i].Type)
		input.Entities[i].Value = h.sanitizer.SanitizeString(input.Entities[i].Value)
	}
	for i := range input.DataSources {
		input.DataSources[i] = h.sanitizer.SanitizeString(input.DataSources[i])
	}

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
		"traceId":     trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
	})

	return &Output{InternalData: results}, nil
}

// ===== ORIGINAL HELPER METHODS =====

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
		if source == "internal_db" || source == "postgresql" {
			return true
		}
	}
	return false
}

func (h *Handler) shouldQueryES(dataSources []string) bool {
	for _, source := range dataSources {
		if source == "search_index" || source == "elasticsearch" {
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

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error":   err.Error(),
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
		return
	}
	_, err = cmd.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error":   err.Error(),
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}
}

// Public Execute method for direct API calls
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// Validate before execution
	if err := h.validateInput(input); err != nil {
		return nil, err
	}
	return h.execute(ctx, input)
}