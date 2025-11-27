// internal/workers/franchise/calculate-match-score/config.go
package calculatematchscore

import "time"

type Config struct {
	CacheTTL time.Duration
	Timeout  time.Duration
}

func LoadConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}
