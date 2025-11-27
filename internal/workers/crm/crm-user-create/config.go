package crmusercreate

import (
	"fmt"
	"time"
)

type Config struct {
	Enabled        bool          `mapstructure:"enabled"`
	MaxJobsActive  int           `mapstructure:"max_jobs_active"`
	Timeout        time.Duration `mapstructure:"timeout"`
	ZohoAPIKey     string        `mapstructure:"zoho_api_key"`
	ZohoOAuthToken string        `mapstructure:"zoho_oauth_token"`
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		MaxJobsActive: 5,
		Timeout:       30 * time.Second,
	}
}

func (c *Config) Validate() error {
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if c.MaxJobsActive <= 0 {
		return fmt.Errorf("max_jobs_active must be positive")
	}
	if c.ZohoAPIKey == "" {
		return fmt.Errorf("zoho_api_key is required")
	}
	if c.ZohoOAuthToken == "" {
		return fmt.Errorf("zoho_oauth_token is required")
	}
	return nil
}
