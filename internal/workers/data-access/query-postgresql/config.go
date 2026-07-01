// internal/workers/data-access/query-postgresql/config.go
package querypostgresql

import "time"

type Config struct {
	Timeout       time.Duration
	EncryptionKey string
}

func LoadConfig() *Config {
	return &Config{
		Timeout:       30 * time.Second,
		EncryptionKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==", // fallback dev key
	}
}
