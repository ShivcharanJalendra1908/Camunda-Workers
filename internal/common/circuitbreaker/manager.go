// internal/common/circuitbreaker/manager.go
package circuitbreaker

import (
	"fmt"
	"sync"
	"time"
)

// Manager manages multiple circuit breakers for different services
type Manager struct {
	breakers map[string]*CircuitBreaker
	mu       sync.RWMutex
}

// NewManager creates a new circuit breaker manager
func NewManager() *Manager {
	return &Manager{
		breakers: make(map[string]*CircuitBreaker),
	}
}

// Register registers a new circuit breaker with the given configuration
func (m *Manager) Register(cfg Config) *CircuitBreaker {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If already exists, return existing
	if cb, exists := m.breakers[cfg.Name]; exists {
		return cb
	}

	cb := New(cfg)
	m.breakers[cfg.Name] = cb
	return cb
}

// Get retrieves a circuit breaker by name
func (m *Manager) Get(name string) (*CircuitBreaker, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cb, exists := m.breakers[name]
	if !exists {
		return nil, fmt.Errorf("circuit breaker '%s' not found", name)
	}

	return cb, nil
}

// GetOrCreate gets existing circuit breaker or creates with default config
func (m *Manager) GetOrCreate(name string, defaultCfg Config) *CircuitBreaker {
	cb, err := m.Get(name)
	if err != nil {
		defaultCfg.Name = name
		return m.Register(defaultCfg)
	}
	return cb
}

// GetAll returns all registered circuit breakers
func (m *Manager) GetAll() map[string]*CircuitBreaker {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*CircuitBreaker, len(m.breakers))
	for name, cb := range m.breakers {
		result[name] = cb
	}

	return result
}

// GetMetrics returns metrics for all circuit breakers
func (m *Manager) GetMetrics() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metrics := make(map[string]interface{})
	for name, cb := range m.breakers {
		metrics[name] = cb.Metrics()
	}

	return metrics
}

// Reset resets all circuit breakers to closed state
func (m *Manager) ResetAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, cb := range m.breakers {
		cb.Reset()
	}
}

// InitializeDefaults initializes circuit breakers with default configurations
func (m *Manager) InitializeDefaults() {
	configs := []Config{
		{
			Name:             "keycloak",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
			MaxConcurrent:    50,
		},
		{
			Name:             "zoho-crm",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          45 * time.Second,
			MaxConcurrent:    10,
		},
		{
			Name:             "aws-ses",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
			MaxConcurrent:    15,
		},
		{
			Name:             "genai-service",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
			MaxConcurrent:    10,
		},
		{
			Name:             "linkedin-oauth",
			FailureThreshold: 3,
			SuccessThreshold: 2,
			Timeout:          60 * time.Second,
			MaxConcurrent:    20,
		},
		{
			Name:             "google-oauth",
			FailureThreshold: 3,
			SuccessThreshold: 2,
			Timeout:          60 * time.Second,
			MaxConcurrent:    20,
		},
		{
			Name:             "web-search",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
			MaxConcurrent:    10,
		},
	}

	for _, cfg := range configs {
		m.Register(cfg)
	}
}
