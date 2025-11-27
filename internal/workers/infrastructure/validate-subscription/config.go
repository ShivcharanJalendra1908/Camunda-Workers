// internal/workers/infrastructure/validate-subscription/config.go
package validatesubscription

import "time"

type Config struct {
	Timeout  time.Duration
	CacheTTL time.Duration
}

func LoadConfig() *Config {
	return &Config{
		Timeout:  30 * time.Second,
		CacheTTL: 5 * time.Minute,
	}
}
