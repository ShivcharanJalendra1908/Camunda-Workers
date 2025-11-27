package authsigningoogle

import (
	"fmt"
	"time"
)

// Config defines the configuration for Google OAuth worker
type Config struct {
	Enabled          bool          `mapstructure:"enabled"`
	MaxJobsActive    int           `mapstructure:"max_jobs_active"`
	Timeout          time.Duration `mapstructure:"timeout"`
	ClientID         string        `mapstructure:"client_id"`
	ClientSecret     string        `mapstructure:"client_secret"`
	RedirectURL      string        `mapstructure:"redirect_uri"`
	CreateCRMContact bool          `mapstructure:"create_crm_contact"`
}

// DefaultConfig returns default configuration values
func DefaultConfig() *Config {
	return &Config{
		Enabled:          true,
		MaxJobsActive:    5,
		Timeout:          10 * time.Second,
		CreateCRMContact: true,
	}
}

// Validate checks if configuration is valid
func (c *Config) Validate() error {
	if c.ClientID == "" {
		return fmt.Errorf("client_id is required")
		//	return fmt.Errorf("clientId is required") // Change to camelCase
	}
	if c.ClientSecret == "" {
		return fmt.Errorf("client_secret is required")
	}
	if c.RedirectURL == "" {
		return fmt.Errorf("redirect_uri is required")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if c.MaxJobsActive <= 0 {
		return fmt.Errorf("max_jobs_active must be positive")
	}
	return nil
}

// IsCRMEnabled checks if CRM integration should be used
func (c *Config) IsCRMEnabled() bool {
	return c.CreateCRMContact && c.ClientID != "" && c.ClientSecret != ""
}
