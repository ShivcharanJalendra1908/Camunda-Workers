// internal/workers/ai-conversation/parse-user-intent/models.go
package parseuserintent

type Input struct {
	Question string                 `json:"question"`
	Context  map[string]interface{} `json:"context"`
}

type Output struct {
	IntentAnalysis IntentAnalysis         `json:"intentAnalysis"`
	DataSources    []string               `json:"dataSources"`
	Entities       []Entity               `json:"entities"`
	CircuitBreaker map[string]interface{} `json:"circuitBreaker,omitempty"` // NEW: Circuit breaker metrics
}

type IntentAnalysis struct {
	PrimaryIntent string  `json:"primaryIntent"`
	Confidence    float64 `json:"confidence"`
}

type Entity struct {
	Type  string `json:"type"` // "franchise_name", "location", "category", "investment_amount"
	Value string `json:"value"`
}
