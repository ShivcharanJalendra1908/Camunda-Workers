// internal/workers/data-access/query-elasticsearch/queries/blog.go
package queries

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/elastic/go-elasticsearch/v8"
)

// BlogListing handles search and filtering of blogs using Elasticsearch
func BlogListing(ctx context.Context, esClient *elasticsearch.Client, params map[string]interface{}) (*QueryResult, error) {
	page := 1
	pageSize := 6 // Default: 6 cards per page

	if p, ok := params["page"].(float64); ok && p > 0 {
		page = int(p)
	}
	if ps, ok := params["pageSize"].(float64); ok && ps > 0 {
		pageSize = int(ps)
	}

	from := (page - 1) * pageSize

	// Build the Elasticsearch query
	query := map[string]interface{}{
		"bool": map[string]interface{}{
			"must": []map[string]interface{}{
				{"term": map[string]interface{}{"status": "live"}},
			},
		},
	}

	if search, ok := params["search"].(string); ok && search != "" {
		query["bool"].(map[string]interface{})["must"] = append(
			query["bool"].(map[string]interface{})["must"].([]map[string]interface{}),
			map[string]interface{}{
				"multi_match": map[string]interface{}{
					"query":  search,
					"fields": []string{"title^3", "short_description", "tags^2"},
					"type":   "best_fields",
				},
			},
		)
	}

	if catID, ok := params["categoryId"].(string); ok && catID != "" {
		query["bool"].(map[string]interface{})["filter"] = []map[string]interface{}{
			{"term": map[string]interface{}{"category_ids": catID}},
		}
	}

	body := map[string]interface{}{
		"from":  from,
		"size":  pageSize,
		"query": query,
		"sort": []map[string]interface{}{
			{"created_at": map[string]interface{}{"order": "desc"}},
		},
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, fmt.Errorf("failed to encode query: %w", err)
	}

	res, err := esClient.Search(
		esClient.Search.WithContext(ctx),
		esClient.Search.WithIndex("blog_listings"),
		esClient.Search.WithBody(&buf),
		esClient.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch search error: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch error: %s", res.String())
	}

	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	hits := r["hits"].(map[string]interface{})
	total := int64(hits["total"].(map[string]interface{})["value"].(float64))
	
	var maxScore float64
	if v, ok := hits["max_score"].(float64); ok {
		maxScore = v
	}

	var data []map[string]interface{}
	for _, hit := range hits["hits"].([]interface{}) {
		source := hit.(map[string]interface{})["_source"].(map[string]interface{})
		
		// Build a pruned map for the UI card
		pruned := map[string]interface{}{
			"id":                  hit.(map[string]interface{})["_id"],
			"title":               source["name"],
			"slug":                source["slug"],
			"short_description":   source["short_description"],
			"featured_image_url":  source["featured_image_url"],
			"reading_time_mins":   source["reading_time_mins"],
			"author_display_name": source["author_display_name"],
			"tags":                source["tags"],
			"categories":          source["categories"],
			"published_at":        source["created_at"],
		}
		
		// Optional fields (if they exist in ES)
		if vc, ok := source["view_count"]; ok {
			pruned["view_count"] = vc
		}

		data = append(data, pruned)
	}

	return &QueryResult{
		Data:      data,
		TotalHits: total,
		MaxScore:  maxScore,
		Took:      int64(r["took"].(float64)),
	}, nil
}
