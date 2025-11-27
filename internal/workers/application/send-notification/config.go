// internal/workers/application/send-notification/config.go
package sendnotification

import "time"

type Config struct {
	EmailEnabled     bool
	SMSEnabled       bool
	FromEmail        string
	AWSRegion        string
	TemplateRegistry string
	Timeout          time.Duration
}

func LoadConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}
