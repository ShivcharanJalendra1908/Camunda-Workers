package authlogout

import (
	"fmt"
	"time"
)

type Config struct {
	Enabled       bool          `mapstructure:"enabled"`
	MaxJobsActive int           `mapstructure:"max_jobs_active"`
	Timeout       time.Duration `mapstructure:"timeout"`
	RedisHost     string        `mapstructure:"redis_host"`
	RedisPort     int           `mapstructure:"redis_port"`
	RedisPassword string        `mapstructure:"redis_password"`
	RedisDB       int           `mapstructure:"redis_db"`
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		MaxJobsActive: 5,
		Timeout:       10 * time.Second,
		RedisPort:     6379,
		RedisDB:       0,
	}
}

func (c *Config) Validate() error {
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if c.MaxJobsActive <= 0 {
		return fmt.Errorf("max_jobs_active must be positive")
	}
	if c.RedisHost == "" {
		return fmt.Errorf("redis_host is required")
	}
	if c.RedisPort <= 0 || c.RedisPort > 65535 {
		return fmt.Errorf("redis_port must be between 1 and 65535")
	}
	return nil
}
