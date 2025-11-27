// internal/common/camunda/client.go
package camunda

import (
	"context"
	"fmt"
	"strings"
	"time"

	"camunda-workers/internal/common/errors"

	"github.com/camunda/zeebe/clients/go/v8/pkg/zbc"
)

// Client wraps the Zeebe gRPC client with enhanced error handling and retry logic.
type Client struct {
	client zbc.Client
	config *ClientConfig
}

// ClientConfig holds configuration for the Camunda/Zeebe client.
type ClientConfig struct {
	GatewayAddress         string
	UsePlaintextConnection bool
	ConnectionTimeout      time.Duration
	RequestTimeout         time.Duration
	RetryConfig            *RetryConfig
}

// RetryConfig defines retry behavior for transient failures.
type RetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// DefaultRetryConfig provides sensible defaults per REQ-INT-001.
var DefaultRetryConfig = &RetryConfig{
	MaxRetries: 3,
	BaseDelay:  1 * time.Second,
	MaxDelay:   10 * time.Second,
}

// NewClient creates a new Camunda client with default configuration.
// Suitable for simple setups (e.g., local dev).
func NewClient(address string) (*Client, error) {
	config := &ClientConfig{
		GatewayAddress:         address,
		UsePlaintextConnection: true, // Set to false and configure TLS in production
		ConnectionTimeout:      10 * time.Second,
		RequestTimeout:         30 * time.Second,
		RetryConfig:            DefaultRetryConfig,
	}
	return NewClientWithConfig(config)
}

// NewClientWithConfig creates a Camunda client using explicit configuration.
// Implements REQ-INT-001 (connection retry, timeout, TLS support).
func NewClientWithConfig(config *ClientConfig) (*Client, error) {
	if config.RetryConfig == nil {
		config.RetryConfig = DefaultRetryConfig
	}

	zeebeClient, err := zbc.NewClient(&zbc.ClientConfig{
		GatewayAddress:         config.GatewayAddress,
		UsePlaintextConnection: config.UsePlaintextConnection,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Zeebe client: %w", err)
	}

	// Test connection with timeout
	ctx, cancel := context.WithTimeout(context.Background(), config.ConnectionTimeout)
	defer cancel()

	if _, err := zeebeClient.NewTopologyCommand().Send(ctx); err != nil {
		zeebeClient.Close()
		return nil, fmt.Errorf("failed to connect to Zeebe broker at %s: %w", config.GatewayAddress, err)
	}

	return &Client{
		client: zeebeClient,
		config: config,
	}, nil
}

// GetClient returns the raw Zeebe client for advanced usage (e.g., job polling).
func (c *Client) GetClient() zbc.Client {
	return c.client
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error {
	return c.client.Close()
}

// ExecuteWithRetry executes a Zeebe command with exponential backoff retry logic.
// Only retryable errors (timeouts, connection issues) are retried.
// Implements REQ-COMMON-009.
func (c *Client) ExecuteWithRetry(
	ctx context.Context,
	commandFunc func(context.Context) (interface{}, error),
	operationName string,
) (interface{}, error) {
	var lastErr error

	for attempt := 0; attempt <= c.config.RetryConfig.MaxRetries; attempt++ {
		result, err := commandFunc(ctx)
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Stop retrying if not a transient error or max retries reached
		if !isRetryableZeebeError(err) || attempt == c.config.RetryConfig.MaxRetries {
			return nil, c.mapZeebeError(err, operationName, attempt)
		}

		// Exponential backoff with jitter-like safety via power-of-two
		delay := c.config.RetryConfig.BaseDelay * time.Duration(1<<attempt)
		if delay > c.config.RetryConfig.MaxDelay {
			delay = c.config.RetryConfig.MaxDelay
		}

		select {
		case <-time.After(delay):
			// Retry
		case <-ctx.Done():
			return nil, fmt.Errorf("operation %s cancelled after %d attempts: %w", operationName, attempt, ctx.Err())
		}
	}

	return nil, fmt.Errorf("operation %s failed after %d retries: %w", operationName, c.config.RetryConfig.MaxRetries, lastErr)
}

// isRetryableZeebeError checks if the error is transient and should be retried.
func isRetryableZeebeError(err error) bool {
	msg := strings.ToLower(err.Error())
	retryablePhrases := []string{
		"connection refused",
		"connection reset",
		"timeout",
		"deadline exceeded",
		"unavailable",
		"unreachable",
		"broken pipe",
	}
	for _, phrase := range retryablePhrases {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

// mapZeebeError converts Zeebe errors into standardized application errors.
// Implements REQ-COMMON-007 and REQ-COMMON-008.
func (c *Client) mapZeebeError(err error, operation string, attempt int) error {
	msg := err.Error()
	lowerMsg := strings.ToLower(msg)

	// Create enhanced error message with context
	enhancedMsg := fmt.Sprintf("Zeebe operation '%s' failed", operation)
	if attempt > 0 {
		enhancedMsg += fmt.Sprintf(" after %d attempts", attempt)
	}

	switch {
	case strings.Contains(lowerMsg, "connection refused") ||
		strings.Contains(lowerMsg, "connection reset") ||
		strings.Contains(lowerMsg, "unavailable") ||
		strings.Contains(lowerMsg, "unreachable"):
		return errors.NewExternalServiceError("zeebe", fmt.Errorf("%s: %s", enhancedMsg, msg))

	case strings.Contains(lowerMsg, "timeout") ||
		strings.Contains(lowerMsg, "deadline exceeded"):
		return errors.NewTimeoutError("zeebe", fmt.Errorf("%s: %s", enhancedMsg, msg))

	case strings.Contains(lowerMsg, "not found"):
		return errors.NewResourceNotFoundError("zeebe", fmt.Sprintf("%s: %s", enhancedMsg, msg))

	case strings.Contains(lowerMsg, "already exists"):
		return errors.NewBusinessRuleError(
			fmt.Sprintf("%s: %s", enhancedMsg, msg),
			"Resource already exists",
		)

	case strings.Contains(lowerMsg, "permission denied") ||
		strings.Contains(lowerMsg, "unauthorized"):
		return errors.NewAuthenticationError(fmt.Sprintf("%s: %s", enhancedMsg, msg))

	default:
		return errors.NewExternalServiceError("zeebe", fmt.Errorf("%s: %s", enhancedMsg, msg))
	}
}

// HealthCheck performs a basic health check against the Zeebe broker.
func (c *Client) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.config.ConnectionTimeout)
	defer cancel()

	_, err := c.client.NewTopologyCommand().Send(ctx)
	if err != nil {
		return fmt.Errorf("zeebe health check failed: %w", err)
	}
	return nil
}
