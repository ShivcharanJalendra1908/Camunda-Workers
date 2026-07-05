package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"camunda-workers/internal/common/logger"
	"github.com/redis/go-redis/v9"
)

// CompletionWaiter provides a reusable Redis pub/sub completion-wait pattern
// for Camunda workflow handlers. It subscribes to a Redis channel and waits
// for the send-api-response worker to publish the workflow result.
type CompletionWaiter struct {
	redisClient *redis.Client
	logger      logger.Logger
}

// NewCompletionWaiter creates a new CompletionWaiter.
func NewCompletionWaiter(redisClient *redis.Client, log logger.Logger) *CompletionWaiter {
	return &CompletionWaiter{
		redisClient: redisClient,
		logger:      log,
	}
}

// WorkflowResponse represents the envelope published by send-api-response worker.
type WorkflowResponse struct {
	Response     map[string]interface{} `json:"response"`
	CookieHeader string                 `json:"cookieHeader,omitempty"`
	RequestID    string                 `json:"requestId,omitempty"`
}

// WaitForCompletion subscribes to the Redis channel for the given correlationKey
// and waits for the workflow result or timeout.
//
// The pattern:
//  1. Subscribe to Redis channel workflow:response:{correlationKey}
//  2. Wait up to 3s for SUBSCRIBE confirmation (non-fatal)
//  3. Block on select: pubsub message, timeout with cache fallback, or context done
//
// Returns the parsed workflow response or an error on timeout/cancellation.
func (w *CompletionWaiter) WaitForCompletion(
	ctx context.Context,
	correlationKey string,
	timeout time.Duration,
) (*WorkflowResponse, error) {
	if w.redisClient == nil {
		return nil, fmt.Errorf("Redis client not available")
	}

	channel := fmt.Sprintf("workflow:response:%s", correlationKey)
	pubsub := w.redisClient.Subscribe(ctx, channel)
	defer pubsub.Close()

	// Wait for subscription confirmation (non-fatal, 3s max)
	confirmCtx, confirmCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer confirmCancel()
	if _, err := pubsub.ReceiveTimeout(confirmCtx, 3*time.Second); err != nil {
		w.logger.Warn("Subscription confirm timeout, proceeding anyway", map[string]interface{}{
			"channel": channel,
			"error":   err.Error(),
		})
	}

	select {
	case msg := <-pubsub.Channel():
		var envelope WorkflowResponse
		if err := json.Unmarshal([]byte(msg.Payload), &envelope); err != nil {
			return nil, fmt.Errorf("failed to parse workflow response: %w", err)
		}
		if envelope.Response == nil {
			return nil, fmt.Errorf("workflow returned nil response")
		}
		return &envelope, nil

	case <-time.After(timeout):
		// Fallback: check Redis cache key (worker stores backup with 2min TTL)
		return w.tryCacheFallback(ctx, correlationKey)

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// WaitForCompletionWithFallback waits for completion with a custom cache fallback handler.
// This allows the caller to implement custom fallback logic (e.g., checking workflow status API).
func (w *CompletionWaiter) WaitForCompletionWithFallback(
	ctx context.Context,
	correlationKey string,
	timeout time.Duration,
	fallback func(ctx context.Context, correlationKey string) (*WorkflowResponse, error),
) (*WorkflowResponse, error) {
	result, err := w.WaitForCompletion(ctx, correlationKey, timeout)
	if err == nil {
		return result, nil
	}

	if fallback != nil {
		return fallback(ctx, correlationKey)
	}
	return nil, err
}

// tryCacheFallback attempts to retrieve the workflow response from Redis cache.
// The send-api-response worker stores a backup copy with a 2-minute TTL.
func (w *CompletionWaiter) tryCacheFallback(ctx context.Context, correlationKey string) (*WorkflowResponse, error) {
	cacheKey := fmt.Sprintf("workflow:response:cache:%s", correlationKey)
	cached, err := w.redisClient.Get(ctx, cacheKey).Result()
	if err != nil {
		return nil, fmt.Errorf("workflow timeout and cache miss: %w", err)
	}

	var envelope WorkflowResponse
	if err := json.Unmarshal([]byte(cached), &envelope); err != nil {
		return nil, fmt.Errorf("failed to parse cached response: %w", err)
	}
	if envelope.Response == nil {
		return nil, fmt.Errorf("cached response is nil")
	}
	return &envelope, nil
}

// ChannelName returns the Redis channel name for a given correlationKey.
func ChannelName(correlationKey string) string {
	return fmt.Sprintf("workflow:response:%s", correlationKey)
}

// CacheKeyName returns the Redis cache key name for a given correlationKey.
func CacheKeyName(correlationKey string) string {
	return fmt.Sprintf("workflow:response:cache:%s", correlationKey)
}

// IsAvailable returns whether the Redis client is connected.
func (w *CompletionWaiter) IsAvailable(ctx context.Context) bool {
	if w.redisClient == nil {
		return false
	}
	return w.redisClient.Ping(ctx).Err() == nil
}
