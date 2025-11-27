// internal/workers/franchise/parse-search-filters/config.go
package parsesearchfilters

import "time"

// No per-worker config needed per spec, but struct provided for consistency
type Config struct {
	Timeout time.Duration
}

func LoadConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}
