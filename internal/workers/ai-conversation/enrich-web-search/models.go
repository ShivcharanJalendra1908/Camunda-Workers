// internal/workers/ai-conversation/enrich-web-search/models.go
package enrichwebsearch

type Input struct {
	Question string   `json:"question"`
	Entities []Entity `json:"entities"`
}

type Output struct {
	WebData WebData `json:"webData"`
}

type Entity struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type WebData struct {
	Sources []Source `json:"sources"`
	Summary string   `json:"summary"`
}

type Source struct {
	URL       string  `json:"url"`
	Title     string  `json:"title"`
	Snippet   string  `json:"snippet"`
	Relevance float64 `json:"relevance"`
}
