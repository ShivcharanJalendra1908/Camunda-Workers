package emailsend

import (
	"fmt"
	"time"
)

type Config struct {
	Enabled       bool          `mapstructure:"enabled"`
	MaxJobsActive int           `mapstructure:"max_jobs_active"`
	Timeout       time.Duration `mapstructure:"timeout"`
	SMTPHost      string        `mapstructure:"smtp_host"`
	SMTPPort      int           `mapstructure:"smtp_port"`
	SMTPUsername  string        `mapstructure:"smtp_username"`
	SMTPPassword  string        `mapstructure:"smtp_password"`
	UseTLS        bool          `mapstructure:"use_tls"`
	DefaultFrom   string        `mapstructure:"default_from"`
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		MaxJobsActive: 5,
		Timeout:       30 * time.Second,
		SMTPPort:      587,
		UseTLS:        true,
		DefaultFrom:   "noreply@example.com",
	}
}

func (c *Config) Validate() error {
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if c.MaxJobsActive <= 0 {
		return fmt.Errorf("max_jobs_active must be positive")
	}
	if c.SMTPHost == "" {
		return fmt.Errorf("smtp_host is required")
	}
	if c.SMTPPort <= 0 || c.SMTPPort > 65535 {
		return fmt.Errorf("smtp_port must be between 1 and 65535")
	}
	if c.DefaultFrom == "" {
		return fmt.Errorf("default_from email is required")
	}
	return nil
}
