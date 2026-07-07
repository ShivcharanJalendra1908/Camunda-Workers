// internal/common/database/elasticsearch.go
package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/errors"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// ElasticsearchClient wraps the Elasticsearch client with validation
type ElasticsearchClient struct {
	Client *elasticsearch.Client
}

// NewElasticsearch creates a new Elasticsearch client
func NewElasticsearch(cfg config.ElasticsearchConfig) (*ElasticsearchClient, error) {
	esCfg := elasticsearch.Config{
		Addresses: cfg.Addresses,
	}

	if cfg.Username != "" {
		esCfg.Username = cfg.Username
		esCfg.Password = cfg.Password
	}

	es, err := elasticsearch.NewClient(esCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create elasticsearch client: %w", err)
	}

	return &ElasticsearchClient{Client: es}, nil
}

// Ping tests the Elasticsearch connection
func (c *ElasticsearchClient) Ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := c.Client.Ping(
		c.Client.Ping.WithContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("elasticsearch ping failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("elasticsearch ping error: %s", res.Status())
	}

	return nil
}

// Info returns cluster information
func (c *ElasticsearchClient) Info(ctx context.Context) error {
	res, err := c.Client.Info(
		c.Client.Info.WithContext(ctx),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("elasticsearch info error: %s", res.Status())
	}

	return nil
}

// Search performs a search query on one or more indices with validation
func (c *ElasticsearchClient) Search(ctx context.Context, indices interface{}, query map[string]interface{}) (map[string]interface{}, error) {
	// Validate query before execution
	if err := validateElasticsearchQuery(query); err != nil {
		return nil, err
	}

	// Convert query to JSON
	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	// Convert indices to string
	var indexStr string
	switch v := indices.(type) {
	case string:
		indexStr = v
	case []string:
		indexStr = strings.Join(v, ",")
	default:
		return nil, fmt.Errorf("invalid indices type: %T", indices)
	}

	// Validate indices
	if indexStr == "" {
		return nil, fmt.Errorf("no indices specified")
	}

	// Perform search
	res, err := c.Client.Search(
		c.Client.Search.WithContext(ctx),
		c.Client.Search.WithIndex(indexStr),
		c.Client.Search.WithBody(strings.NewReader(string(queryJSON))),
		c.Client.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search error: %s", res.String())
	}

	// Parse response
	var result map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// SearchDocuments performs a search query on the specified index
func (c *ElasticsearchClient) SearchDocuments(ctx context.Context, index string, query map[string]interface{}) (map[string]interface{}, error) {
	return c.Search(ctx, index, query)
}

// SearchMultipleIndices performs a search query on multiple indices
func (c *ElasticsearchClient) SearchMultipleIndices(ctx context.Context, indices []string, query map[string]interface{}) (map[string]interface{}, error) {
	return c.Search(ctx, indices, query)
}

// Get retrieves a document by ID
func (c *ElasticsearchClient) Get(ctx context.Context, index, id string) (map[string]interface{}, error) {
	// Validate index and ID
	if index == "" || id == "" {
		return nil, fmt.Errorf("index and id are required")
	}

	// Validate ID doesn't contain dangerous patterns
	if strings.ContainsAny(id, "\\/*?\"<>|") {
		return nil, errors.NewNoSQLInjectionError("id", "contains dangerous characters")
	}

	res, err := c.Client.Get(
		index,
		id,
		c.Client.Get.WithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("get request failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		if res.StatusCode == 404 {
			return nil, nil // Document not found
		}
		return nil, fmt.Errorf("get error: %s", res.String())
	}

	// Parse response
	var result map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract the _source field
	if source, ok := result["_source"].(map[string]interface{}); ok {
		// Add metadata
		source["_id"] = result["_id"]
		source["_index"] = result["_index"]
		return source, nil
	}

	return nil, fmt.Errorf("no _source field in response")
}

// Count counts documents matching a query
func (c *ElasticsearchClient) Count(ctx context.Context, index string, query map[string]interface{}) (int64, error) {
	// Validate query
	if err := validateElasticsearchQuery(query); err != nil {
		return 0, err
	}

	// Convert query to JSON
	queryJSON, err := json.Marshal(query)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal query: %w", err)
	}

	// Perform count
	res, err := c.Client.Count(
		c.Client.Count.WithContext(ctx),
		c.Client.Count.WithIndex(index),
		c.Client.Count.WithBody(strings.NewReader(string(queryJSON))),
	)
	if err != nil {
		return 0, fmt.Errorf("count request failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return 0, fmt.Errorf("count error: %s", res.String())
	}

	// Parse response
	var result map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract count
	if count, ok := result["count"].(float64); ok {
		return int64(count), nil
	}

	return 0, fmt.Errorf("no count field in response")
}

// Index creates or updates a document
func (c *ElasticsearchClient) Index(ctx context.Context, index, id string, document map[string]interface{}) error {
	// Validate index and document
	if index == "" {
		return fmt.Errorf("index is required")
	}

	if document == nil {
		return fmt.Errorf("document is required")
	}

	// Validate ID if provided
	if id != "" && strings.ContainsAny(id, "\\/*?\"<>|") {
		return errors.NewNoSQLInjectionError("id", "contains dangerous characters")
	}

	// Convert document to JSON
	docJSON, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("failed to marshal document: %w", err)
	}

	var res *esapi.Response
	if id == "" {
		// Auto-generate ID
		res, err = c.Client.Index(
			index,
			strings.NewReader(string(docJSON)),
			c.Client.Index.WithContext(ctx),
		)
	} else {
		// Use specified ID
		res, err = c.Client.Index(
			index,
			strings.NewReader(string(docJSON)),
			c.Client.Index.WithContext(ctx),
			c.Client.Index.WithDocumentID(id),
		)
	}

	if err != nil {
		return fmt.Errorf("index request failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("index error: %s", res.String())
	}

	return nil
}

// Update updates a document
func (c *ElasticsearchClient) Update(ctx context.Context, index, id string, update map[string]interface{}) error {
	// Validate index, ID, and update
	if index == "" || id == "" {
		return fmt.Errorf("index and id are required")
	}

	if update == nil {
		return fmt.Errorf("update is required")
	}

	// Validate ID
	if strings.ContainsAny(id, "\\/*?\"<>|") {
		return errors.NewNoSQLInjectionError("id", "contains dangerous characters")
	}

	// Convert update to JSON
	updateJSON, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("failed to marshal update: %w", err)
	}

	res, err := c.Client.Update(
		index,
		id,
		strings.NewReader(string(updateJSON)),
		c.Client.Update.WithContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("update request failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("update error: %s", res.String())
	}

	return nil
}

// Delete deletes a document
func (c *ElasticsearchClient) Delete(ctx context.Context, index, id string) error {
	// Validate index and ID
	if index == "" || id == "" {
		return fmt.Errorf("index and id are required")
	}

	// Validate ID
	if strings.ContainsAny(id, "\\/*?\"<>|") {
		return errors.NewNoSQLInjectionError("id", "contains dangerous characters")
	}

	res, err := c.Client.Delete(
		index,
		id,
		c.Client.Delete.WithContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("delete request failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		if res.StatusCode == 404 {
			return nil // Document already deleted
		}
		return fmt.Errorf("delete error: %s", res.String())
	}

	return nil
}

// Bulk performs bulk operations with validation
func (c *ElasticsearchClient) Bulk(ctx context.Context, operations []map[string]interface{}) error {
	// Validate operations
	if len(operations) == 0 {
		return fmt.Errorf("no operations provided")
	}

	// Validate each operation
	for i, op := range operations {
		// Check for script operations
		if script, ok := op["script"].(map[string]interface{}); ok {
			if source, ok := script["source"].(string); ok {
				if strings.Contains(strings.ToLower(source), "runtime") ||
					strings.Contains(strings.ToLower(source), "exec") ||
					strings.Contains(strings.ToLower(source), "system") {
					return errors.NewNoSQLInjectionError("bulk operation", fmt.Sprintf("dangerous script at index %d", i))
				}
			}
		}
	}

	var body strings.Builder
	for _, op := range operations {
		opJSON, err := json.Marshal(op)
		if err != nil {
			return fmt.Errorf("failed to marshal operation: %w", err)
		}
		body.WriteString(string(opJSON))
		body.WriteString("\n")
	}

	res, err := c.Client.Bulk(
		strings.NewReader(body.String()),
		c.Client.Bulk.WithContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("bulk request failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("bulk error: %s", res.String())
	}

	return nil
}

// CreateIndex creates a new index with optional mapping
func (c *ElasticsearchClient) CreateIndex(ctx context.Context, index string, mapping map[string]interface{}) error {
	// Validate index name
	if index == "" {
		return fmt.Errorf("index name is required")
	}

	if strings.ContainsAny(index, "\\/*?\"<>|,:") {
		return fmt.Errorf("invalid index name")
	}

	var body string
	if mapping != nil {
		mappingJSON, err := json.Marshal(mapping)
		if err != nil {
			return fmt.Errorf("failed to marshal mapping: %w", err)
		}
		body = string(mappingJSON)
	}

	res, err := c.Client.Indices.Create(
		index,
		c.Client.Indices.Create.WithContext(ctx),
		c.Client.Indices.Create.WithBody(strings.NewReader(body)),
	)
	if err != nil {
		return fmt.Errorf("create index request failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("create index error: %s", res.String())
	}

	return nil
}

// DeleteIndex deletes an index
func (c *ElasticsearchClient) DeleteIndex(ctx context.Context, index string) error {
	// Validate index name
	if index == "" {
		return fmt.Errorf("index name is required")
	}

	// Prevent deletion of system indices
	if strings.HasPrefix(index, ".") {
		return fmt.Errorf("cannot delete system indices")
	}

	res, err := c.Client.Indices.Delete(
		[]string{index},
		c.Client.Indices.Delete.WithContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("delete index request failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("delete index error: %s", res.String())
	}

	return nil
}

// IndexExists checks if an index exists
func (c *ElasticsearchClient) IndexExists(ctx context.Context, index string) (bool, error) {
	// Validate index name
	if index == "" {
		return false, fmt.Errorf("index name is required")
	}

	res, err := c.Client.Indices.Exists(
		[]string{index},
		c.Client.Indices.Exists.WithContext(ctx),
	)
	if err != nil {
		return false, fmt.Errorf("index exists request failed: %w", err)
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case 200:
		return true, nil
	case 404:
		return false, nil
	}

	return false, fmt.Errorf("index exists error: %s", res.String())
}

// validateElasticsearchQuery checks for dangerous patterns in Elasticsearch queries
func validateElasticsearchQuery(query map[string]interface{}) error {
	if query == nil {
		return fmt.Errorf("query cannot be nil")
	}

	// Marshal query to string for pattern matching
	queryJSON, err := json.Marshal(query)
	if err != nil {
		return fmt.Errorf("failed to marshal query for validation: %w", err)
	}

	queryStr := strings.ToLower(string(queryJSON))

	// Check for dangerous patterns
	dangerousPatterns := []string{
		`"script"`,
		`"inline"`,
		`"source"`,
		`"lang":"painless"`,
		`"function_score"`,
		`"script_score"`,
		`"runtime_mappings"`,
		`"params"`,
		`"stored"`,
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(queryStr, pattern) {
			return errors.NewNoSQLInjectionError("query", fmt.Sprintf("contains dangerous pattern: %s", pattern))
		}
	}

	// Check query depth (prevent deeply nested queries)
	if err := checkQueryDepth(query, 0, 15); err != nil {
		return err
	}

	// Check query size (prevent overly large queries)
	if len(queryJSON) > 1000000 { // 1MB limit
		return fmt.Errorf("query too large: %d bytes", len(queryJSON))
	}

	return nil
}

// checkQueryDepth prevents deeply nested queries
func checkQueryDepth(query interface{}, currentDepth, maxDepth int) error {
	if currentDepth > maxDepth {
		return fmt.Errorf("query too deeply nested: depth %d exceeds maximum %d", currentDepth, maxDepth)
	}

	switch v := query.(type) {
	case map[string]interface{}:
		for _, value := range v {
			if err := checkQueryDepth(value, currentDepth+1, maxDepth); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, item := range v {
			if err := checkQueryDepth(item, currentDepth+1, maxDepth); err != nil {
				return err
			}
		}
	}
	return nil
}
