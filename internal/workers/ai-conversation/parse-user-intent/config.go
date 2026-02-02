package parseuserintent

import "time"

type Config struct {
	GenAIBaseURL   string
	Timeout        time.Duration
	MaxRetries     int
	CircuitBreaker CircuitBreakerConfig
}

type CircuitBreakerConfig struct {
	FailureThreshold int
	SuccessThreshold int
	OpenTimeout      time.Duration
	MaxConcurrent    int
}

func LoadConfig() *Config {
	return &Config{
		Timeout:    30 * time.Second,
		MaxRetries: 3,
		CircuitBreaker: CircuitBreakerConfig{
			FailureThreshold: 5,
			SuccessThreshold: 2,
			OpenTimeout:      30 * time.Second,
			MaxConcurrent:    20,
		},
	}
}
