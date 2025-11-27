// internal/workers/infrastructure/select-template/config.go
package selecttemplate

import "time"

// Config matches REQ-INFRA-013
type Config struct {
	TemplateRules map[string]map[string]string `mapstructure:"template_rules"`
	Timeout       time.Duration
}

func LoadConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}
