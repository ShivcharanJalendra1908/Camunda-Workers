// internal/workers/ai-conversation/query-internal-data/models.go
package queryinternaldata

type Input struct {
	Entities    []Entity `json:"entities"`
	DataSources []string `json:"dataSources"`
}

type Output struct {
	InternalData map[string]interface{} `json:"internalData"`
}

type Entity struct {
	Type  string `json:"type"` // "franchise_name", "location", "category", "investment_amount"
	Value string `json:"value"`
}
