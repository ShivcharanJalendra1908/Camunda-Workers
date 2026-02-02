// config.go update karo:
package authsignupgoogle

import (
	"fmt"
	"time"
)

type Config struct {
	Enabled          bool          `mapstructure:"enabled"`
	MaxJobsActive    int           `mapstructure:"max_jobs_active"`
	Timeout          time.Duration `mapstructure:"timeout"`
	ClientID         string        `mapstructure:"client_id"`
	ClientSecret     string        `mapstructure:"client_secret"`
	RedirectURL      string        `mapstructure:"redirect_uri"`
	CreateCRMContact bool          `mapstructure:"create_crm_contact"`
	ZohoAPIKey       string        `mapstructure:"zoho_api_key"`
	ZohoOAuthToken   string        `mapstructure:"zoho_oauth_token"`
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:          true,
		MaxJobsActive:    5,
		Timeout:          10 * time.Second,
		CreateCRMContact: true,
	}
}

func (c *Config) Validate() error {
	if c.ClientID == "" {
		return fmt.Errorf("client_id is required")
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
	// Zoho credentials validation only if CRM contact creation is enabled
	if c.CreateCRMContact && c.ZohoAPIKey == "" {
		return fmt.Errorf("zoho_api_key is required when create_crm_contact is true")
	}
	if c.CreateCRMContact && c.ZohoOAuthToken == "" {
		return fmt.Errorf("zoho_oauth_token is required when create_crm_contact is true")
	}
	return nil
}

func (c *Config) IsCRMEnabled() bool {
	return c.CreateCRMContact &&
		c.ZohoAPIKey != "" &&
		c.ZohoOAuthToken != ""
}
