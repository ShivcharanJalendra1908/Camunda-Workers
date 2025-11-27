// internal/workers/data-access/query-elasticsearch/models.go
package queryelasticsearch

type Input struct {
	IndexName   string                 `json:"indexName"`
	QueryType   string                 `json:"queryType"`
	Filters     map[string]interface{} `json:"filters"`
	FranchiseID string                 `json:"franchiseId,omitempty"`
	Category    string                 `json:"category,omitempty"`
	Pagination  Pagination             `json:"pagination"`
}

type Pagination struct {
	From int `json:"from"`
	Size int `json:"size"`
}

type Output struct {
	Data      []map[string]interface{} `json:"data"`
	TotalHits int64                    `json:"totalHits"`
	MaxScore  float64                  `json:"maxScore"`
	Took      int64                    `json:"took"` // milliseconds
}
