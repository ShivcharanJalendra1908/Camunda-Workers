// internal/workers/ai-conversation/llm-synthesis/config.go
package llmsynthesis

import "time"

type Config struct {
	GenAIBaseURL string
	Timeout      time.Duration
	MaxRetries   int
	MaxTokens    int
	Temperature  float64
}

func LoadConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}
