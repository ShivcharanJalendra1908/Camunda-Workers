package send_api_response

import (
	"fmt"
	"time"

	"camunda-workers/pkg/registry"
)

type Config struct {
	Timeout time.Duration `json:"timeout"` // Timeout for sending to API
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		Timeout: 30 * time.Second,
	}
}

// LoadConfigFromMap loads config from map
func LoadConfigFromMap(configMap map[string]interface{}) *Config {
	cfg := DefaultConfig()

	if timeout, ok := configMap["timeout"].(float64); ok {
		cfg.Timeout = time.Duration(timeout) * time.Second
	}

	return cfg
}

// ✅ FIXED INIT FUNCTION
func init() {
	registry.RegisterWorker(TaskType, func(deps *registry.Dependencies) (registry.WorkerHandler, error) {
		if deps == nil {
			return nil, fmt.Errorf("dependencies is nil")
		}

		config := DefaultConfig()

		// Get response handler from dependencies
		if deps.ResponseHandler == nil {
			return nil, fmt.Errorf("response handler not available in dependencies")
		}

		return NewHandler(config, deps.Logger, deps.ResponseHandler), nil
	})
}
