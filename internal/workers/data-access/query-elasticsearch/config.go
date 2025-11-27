// internal/workers/data-access/query-elasticsearch/config.go
package queryelasticsearch

import "time"

type Config struct {
	Timeout time.Duration
}

func LoadConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}
