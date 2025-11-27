// internal/workers/ai-conversation/enrich-web-search/config.go
package enrichwebsearch

import "time"

type Config struct {
	SearchAPIBaseURL string
	SearchAPIKey     string
	SearchEngineID   string
	Timeout          time.Duration
	MaxResults       int
	MinRelevance     float64
}

func LoadConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}
