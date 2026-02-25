// internal/workers/infrastructure/build-response/config.go
package buildresponse

import "time"

type Config struct {
	AppVersion string
	Timeout    time.Duration
}

func LoadConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}
