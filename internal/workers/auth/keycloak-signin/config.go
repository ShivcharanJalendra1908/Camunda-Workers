package keycloaksignin

import (
	"fmt"
	"time"
)

type Config struct {
	Enabled       bool          `yaml:"enabled"`
	MaxJobsActive int           `yaml:"maxJobsActive"`
	Timeout       time.Duration `yaml:"timeout"`

	// Keycloak Configuration
	Issuer        string `yaml:"issuer"`
	ClientID      string `yaml:"clientId"`
	RedirectURL   string `yaml:"redirectUrl"`
	PublicBaseURL string `yaml:"publicBaseUrl"`

	// Redis for state storage
	RedisHost     string `yaml:"redisHost"`
	RedisPort     int    `yaml:"redisPort"`
	RedisPassword string `yaml:"redisPassword"`
	RedisDB       int    `yaml:"redisDb"`

	// State/PKCE TTL
	StateTTL time.Duration `yaml:"stateTtl"`
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		MaxJobsActive: 10,
		Timeout:       30 * time.Second,
		// These will always be overridden by config.yaml - do NOT hardcode URLs here
		Issuer:        "",
		ClientID:      "lemici-frontend",
		RedirectURL:   "",
		PublicBaseURL: "",
		RedisHost:     "redis",
		RedisPort:     6379,
		RedisDB:       0,
		StateTTL:      5 * time.Minute,
	}
}

func (c *Config) Validate() error {
	if c.Issuer == "" {
		return fmt.Errorf("keycloak issuer is required")
	}
	if c.ClientID == "" {
		return fmt.Errorf("keycloak clientId is required")
	}
	if c.RedirectURL == "" {
		return fmt.Errorf("redirect URL is required")
	}
	if c.RedisHost == "" {
		return fmt.Errorf("redis host is required")
	}
	return nil
}
