// internal/common/database/elasticsearch.go
package database

import (
	"context"
	"fmt"
	"time"

	"camunda-workers/internal/common/config"

	"github.com/elastic/go-elasticsearch/v8"
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

