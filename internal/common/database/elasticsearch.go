// internal/common/database/elasticsearch.go
package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"camunda-workers/internal/common/config"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// ElasticsearchClient wraps the Elasticsearch client
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

// Search is an alias for SearchDocuments for backward compatibility
//
//	func (c *ElasticsearchClient) Search(ctx context.Context, index string, query map[string]interface{}) (map[string]interface{}, error) {
//		return c.SearchDocuments(ctx, index, query)
//	}
//
// Search performs a search query on one or more indices
func (c *ElasticsearchClient) Search(ctx context.Context, indices interface{}, query map[string]interface{}) (map[string]interface{}, error) {
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
	// Convert query to JSON
	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	// Perform search
	res, err := c.Client.Search(
		c.Client.Search.WithContext(ctx),
		c.Client.Search.WithIndex(index),
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

// SearchMultipleIndices performs a search query on multiple indices
func (c *ElasticsearchClient) SearchMultipleIndices(ctx context.Context, indices []string, query map[string]interface{}) (map[string]interface{}, error) {
	// Convert query to JSON
	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	// Join indices with comma for Elasticsearch API
	indexStr := strings.Join(indices, ",")

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

// Get retrieves a document by ID
func (c *ElasticsearchClient) Get(ctx context.Context, index, id string) (map[string]interface{}, error) {
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

// Bulk performs bulk operations
func (c *ElasticsearchClient) Bulk(ctx context.Context, operations []map[string]interface{}) error {
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
