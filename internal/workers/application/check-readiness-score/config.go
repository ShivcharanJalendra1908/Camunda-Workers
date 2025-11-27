// internal/workers/application/check-readiness-score/config.go
package checkreadinessscore

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
