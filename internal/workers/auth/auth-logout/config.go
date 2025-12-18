package authlogout

import (
	"fmt"
	"time"
)

// Config defines the configuration for the auth logout worker
type Config struct {
	Enabled       bool          `mapstructure:"enabled"`
	MaxJobsActive int           `mapstructure:"max_jobs_active"`
	Timeout       time.Duration `mapstructure:"timeout"`
	RedisHost     string        `mapstructure:"redis_host"`
	RedisPort     int           `mapstructure:"redis_port"`
	RedisPassword string        `mapstructure:"redis_password"`
	RedisDB       int           `mapstructure:"redis_db"`
}

// DefaultConfig returns default configuration values
func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		MaxJobsActive: 5,
		Timeout:       10 * time.Second,
		RedisHost:     "localhost",
		RedisPort:     6379,
		RedisPassword: "",
		RedisDB:       0,
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	if c.MaxJobsActive <= 0 {
		return fmt.Errorf("max_jobs_active must be positive")
	}

	// Redis host is required for session management
	if c.RedisHost == "" {
		return fmt.Errorf("redis_host is required")
	}

	// Redis port must be valid
	if c.RedisPort < 1 || c.RedisPort > 65535 {
		return fmt.Errorf("redis_port must be between 1 and 65535")
	}

	return nil
}

// IsRedisEnabled checks if Redis is configured
func (c *Config) IsRedisEnabled() bool {
	return c.RedisHost != ""
}
