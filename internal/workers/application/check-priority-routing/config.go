// internal/workers/application/check-priority-routing/config.go
package checkpriorityrouting

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
