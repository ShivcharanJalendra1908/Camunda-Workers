package crmusercreate

import (
	"fmt"
	"time"
)

// Config defines the configuration for the CRM user create worker
type Config struct {
	Enabled        bool          `mapstructure:"enabled"`
	MaxJobsActive  int           `mapstructure:"max_jobs_active"`
	Timeout        time.Duration `mapstructure:"timeout"`
	ZohoAPIKey     string        `mapstructure:"zoho_api_key"`
	ZohoOAuthToken string        `mapstructure:"zoho_oauth_token"`
	CreateAccount  bool          `mapstructure:"create_account"` // Create account for company
	ApplyTags      bool          `mapstructure:"apply_tags"`     // Apply tags to contacts
}

// DefaultConfig returns default configuration values
func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		MaxJobsActive: 5,
		Timeout:       30 * time.Second,
		CreateAccount: false, // Don't create accounts by default
		ApplyTags:     true,  // Apply tags if provided
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

	// Zoho credentials are required
	if c.ZohoAPIKey == "" {
		return fmt.Errorf("zoho_api_key is required")
	}
	if c.ZohoOAuthToken == "" {
		return fmt.Errorf("zoho_oauth_token is required")
	}

	return nil
}

// IsConfigured checks if Zoho CRM is properly configured
func (c *Config) IsConfigured() bool {
	return c.ZohoAPIKey != "" && c.ZohoOAuthToken != ""
}
