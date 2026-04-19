package sessionmanager

import (
	"fmt"
	"time"
)

type Config struct {
	Enabled       bool          `yaml:"enabled"`
	MaxJobsActive int           `yaml:"maxJobsActive"`
	Timeout       time.Duration `yaml:"timeout"`

	// Redis Configuration
	RedisHost     string `yaml:"redisHost"`
	RedisPort     int    `yaml:"redisPort"`
	RedisPassword string `yaml:"redisPassword"`
	RedisDB       int    `yaml:"redisDb"`

	// Session Configuration
	DefaultTTL time.Duration `yaml:"defaultTtl"`
	CookieName string        `yaml:"cookieName"`
	Secure     bool          `yaml:"secure"`
	HttpOnly   bool          `yaml:"httpOnly"`
	SameSite   string        `yaml:"sameSite"` // "Lax", "Strict", "None"
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		MaxJobsActive: 10,
		Timeout:       10 * time.Second,
		RedisHost:     "localhost",
		RedisPort:     6379,
		RedisDB:       0,
		DefaultTTL:    24 * time.Hour,
		CookieName:    "session_id",
		Secure:        true,
		HttpOnly:      true,
		SameSite:      "None",
	}
}

func (c *Config) Validate() error {
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	if c.MaxJobsActive <= 0 {
		return fmt.Errorf("maxJobsActive must be positive")
	}

	if c.RedisHost == "" {
		return fmt.Errorf("redisHost is required")
	}

	if c.RedisPort < 1 || c.RedisPort > 65535 {
		return fmt.Errorf("redisPort must be between 1 and 65535")
	}

	if c.CookieName == "" {
		return fmt.Errorf("cookieName is required")
	}

	return nil
}
