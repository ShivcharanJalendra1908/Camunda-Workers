// internal/common/circuitbreaker/circuitbreaker.go
package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// State represents the circuit breaker state
type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

var (
	ErrCircuitOpen     = errors.New("circuit breaker is open")
	ErrTooManyRequests = errors.New("too many concurrent requests")
)

// Config holds circuit breaker configuration
type Config struct {
	Name             string
	FailureThreshold int           // Number of failures to open circuit
	SuccessThreshold int           // Number of successes to close from half-open
	Timeout          time.Duration // Time to wait before attempting half-open
	MaxConcurrent    int           // Max concurrent requests (0 = unlimited)
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name             string
	failureThreshold int
	successThreshold int
	timeout          time.Duration
	maxConcurrent    int

	mutex           sync.RWMutex
	state           State
	failures        int
	successes       int
	lastFailure     time.Time
	lastStateChange time.Time

	consecutiveSuccesses int
	consecutiveFailures  int

	// Concurrency control
	semaphore chan struct{}

	// Metrics
	totalRequests   int64
	totalSuccesses  int64
	totalFailures   int64
	totalRejections int64
}

// New creates a new CircuitBreaker with the given configuration
func New(cfg Config) *CircuitBreaker {
	cb := &CircuitBreaker{
		name:             cfg.Name,
		failureThreshold: cfg.FailureThreshold,
		successThreshold: cfg.SuccessThreshold,
		timeout:          cfg.Timeout,
		maxConcurrent:    cfg.MaxConcurrent,
		state:            StateClosed,
		lastStateChange:  time.Now(),
	}

	// Initialize semaphore if max concurrent is set
	if cfg.MaxConcurrent > 0 {
		cb.semaphore = make(chan struct{}, cfg.MaxConcurrent)
	}

	return cb
}

// Execute runs the given function within circuit breaker protection
func (cb *CircuitBreaker) Execute(fn func() (interface{}, error)) (interface{}, error) {
	// Check if request is allowed
	if !cb.AllowRequest() {
		cb.recordRejection()
		return nil, ErrCircuitOpen
	}

	// Acquire semaphore for concurrency control
	if cb.semaphore != nil {
		select {
		case cb.semaphore <- struct{}{}:
			defer func() { <-cb.semaphore }()
		default:
			cb.recordRejection()
			return nil, ErrTooManyRequests
		}
	}

	cb.recordRequest()

	// Execute the function
	result, err := fn()

	if err != nil {
		cb.RecordFailure()
		return nil, err
	}

	cb.RecordSuccess()
	return result, nil
}

// AllowRequest checks if a request should be allowed
func (cb *CircuitBreaker) AllowRequest() bool {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()

	switch cb.state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if timeout has elapsed
		if now.Sub(cb.lastFailure) > cb.timeout {
			cb.setState(StateHalfOpen)
			cb.consecutiveSuccesses = 0
			cb.consecutiveFailures = 0
			return true
		}
		return false

	case StateHalfOpen:
		// Allow limited requests in half-open state
		return true
	}

	return false
}

// RecordSuccess records a successful request
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.totalSuccesses++
	cb.consecutiveSuccesses++
	cb.consecutiveFailures = 0

	switch cb.state {
	case StateHalfOpen:
		if cb.consecutiveSuccesses >= cb.successThreshold {
			cb.setState(StateClosed)
			cb.failures = 0
		}

	case StateClosed:
		cb.failures = 0
	}
}

// RecordFailure records a failed request
func (cb *CircuitBreaker) RecordFailure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.totalFailures++
	cb.failures++
	cb.consecutiveFailures++
	cb.consecutiveSuccesses = 0
	cb.lastFailure = time.Now()

	switch cb.state {
	case StateClosed:
		if cb.failures >= cb.failureThreshold {
			cb.setState(StateOpen)
		}

	case StateHalfOpen:
		// Immediately open on any failure in half-open
		cb.setState(StateOpen)
	}
}

// setState changes the circuit breaker state
func (cb *CircuitBreaker) setState(newState State) {
	if cb.state != newState {
		cb.state = newState
		cb.lastStateChange = time.Now()
	}
}

// recordRequest increments total request counter
func (cb *CircuitBreaker) recordRequest() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.totalRequests++
}

// recordRejection increments rejection counter
func (cb *CircuitBreaker) recordRejection() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.totalRejections++
}

// State returns the current state
func (cb *CircuitBreaker) State() State {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state
}

// Name returns the circuit breaker name
func (cb *CircuitBreaker) Name() string {
	return cb.name
}

// Metrics returns current metrics
func (cb *CircuitBreaker) Metrics() map[string]interface{} {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	return map[string]interface{}{
		"name":                  cb.name,
		"state":                 cb.stateString(),
		"total_requests":        cb.totalRequests,
		"total_successes":       cb.totalSuccesses,
		"total_failures":        cb.totalFailures,
		"total_rejections":      cb.totalRejections,
		"consecutive_successes": cb.consecutiveSuccesses,
		"consecutive_failures":  cb.consecutiveFailures,
		"last_state_change":     cb.lastStateChange.Format(time.RFC3339),
		"last_failure":          cb.lastFailure.Format(time.RFC3339),
	}
}

// stateString returns state as string
func (cb *CircuitBreaker) stateString() string {
	switch cb.state {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.state = StateClosed
	cb.failures = 0
	cb.successes = 0
	cb.consecutiveSuccesses = 0
	cb.consecutiveFailures = 0
	cb.lastStateChange = time.Now()
}
