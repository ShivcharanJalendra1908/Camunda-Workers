// internal/workers/ai-conversation/query-internal-data/config.go
package queryinternaldata

import "time"

type Config struct {
	Timeout    time.Duration
	CacheTTL   time.Duration
	MaxResults int
}

func LoadConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}
