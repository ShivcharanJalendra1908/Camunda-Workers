// internal/workers/ai-conversation/parse-user-intent/config.go
package parseuserintent

import "time"

type Config struct {
	GenAIBaseURL string
	Timeout      time.Duration
	MaxRetries   int
}

func LoadConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}
