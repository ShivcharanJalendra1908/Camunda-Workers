// internal/workers/franchise/apply-relevance-ranking/config.go
package applyrelevanceranking

import "time"

type Config struct {
	MaxItems int
	Timeout  time.Duration
}

func LoadConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}
