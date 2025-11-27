package captchaverify

import (
	"fmt"
	"time"
)

type Config struct {
	Enabled        bool          `mapstructure:"enabled"`
	MaxJobsActive  int           `mapstructure:"max_jobs_active"`
	Timeout        time.Duration `mapstructure:"timeout"`
	MaxAttempts    int           `mapstructure:"max_attempts"`
	VerifyClientIP bool          `mapstructure:"verify_client_ip"`
	ExpiryMinutes  int           `mapstructure:"expiry_minutes"`
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:        true,
		MaxJobsActive:  10,
		Timeout:        5 * time.Second,
		MaxAttempts:    3,
		VerifyClientIP: false,
		ExpiryMinutes:  5,
	}
}

func (c *Config) Validate() error {
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if c.MaxJobsActive <= 0 {
		return fmt.Errorf("max_jobs_active must be positive")
	}
	if c.MaxAttempts <= 0 {
		return fmt.Errorf("max_attempts must be positive")
	}
	if c.ExpiryMinutes <= 0 {
		return fmt.Errorf("expiry_minutes must be positive")
	}
	return nil
}
