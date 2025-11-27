// internal/workers/ai-conversation/llm-synthesis/models.go
package llmsynthesis

type Input struct {
	Question     string                 `json:"question"`
	InternalData map[string]interface{} `json:"internalData"`
	WebData      WebData                `json:"webData"`
	Intent       Intent                 `json:"intent"`
}

type Output struct {
	LLMResponse string   `json:"llmResponse"`
	Confidence  float64  `json:"confidence"`
	Sources     []string `json:"sources"`
}

type WebData struct {
	Sources []Source `json:"sources"`
	Summary string   `json:"summary"`
}

type Source struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

type Intent struct {
	PrimaryIntent string  `json:"primaryIntent"`
	Confidence    float64 `json:"confidence"`
}
