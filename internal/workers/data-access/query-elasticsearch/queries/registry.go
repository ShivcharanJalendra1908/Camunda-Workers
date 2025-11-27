// internal/workers/data-access/query-elasticsearch/queries/registry.go
package queries

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
)

type QueryResult struct {
	Data      []map[string]interface{}
	TotalHits int64
	MaxScore  float64
	Took      int64
}

func Execute(ctx context.Context, esClient *elasticsearch.Client, input map[string]interface{}) (*QueryResult, error) {
	eq := ElasticsearchQuery{
		Index:      input["indexName"].(string),
		QueryType:  input["queryType"].(string),
		Filters:    input["filters"].(map[string]interface{}),
		Pagination: struct{ From, Size int }{0, 20},
	}

	if franchiseID, ok := input["franchiseId"].(string); ok {
		eq.FranchiseID = franchiseID
	}
	if category, ok := input["category"].(string); ok {
		eq.Category = category
	}
	if pagination, ok := input["pagination"].(map[string]interface{}); ok {
		if from, exists := pagination["from"].(float64); exists {
			eq.Pagination.From = int(from)
		}
		if size, exists := pagination["size"].(float64); exists {
			eq.Pagination.Size = int(size)
			if eq.Pagination.Size > 100 {
				eq.Pagination.Size = 100
			}
			if eq.Pagination.Size < 1 {
				eq.Pagination.Size = 20
			}
		}
	}

	req, err := BuildQuery(esClient, eq)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	res, err := req.Do(ctx, esClient)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search query failed: %s", res.String())
	}

	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, err
	}

	hits := r["hits"].(map[string]interface{})
	total := hits["total"].(map[string]interface{})["value"].(float64)
	maxScore := 0.0
	if ms, ok := hits["max_score"].(float64); ok {
		maxScore = ms
	}

	var data []map[string]interface{}
	for _, hit := range hits["hits"].([]interface{}) {
		source := hit.(map[string]interface{})["_source"].(map[string]interface{})
		data = append(data, source)
	}

	return &QueryResult{
		Data:      data,
		TotalHits: int64(total),
		MaxScore:  maxScore,
		Took:      time.Since(start).Milliseconds(),
	}, nil
}
