package validateenquirydata

import "time"

type Config struct {
	Timeout time.Duration
}

func DefaultConfig() *Config {
	return &Config{
		Timeout: 10 * time.Second,
	}
}
