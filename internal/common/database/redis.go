// internal/common/database/redis.go
package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"camunda-workers/internal/common/config"

	"github.com/redis/go-redis/v9"
)

// RedisClient wraps the Redis client
type RedisClient struct {
	Client *redis.Client
}

// NewRedis creates a new Redis client
func NewRedis(cfg config.RedisConfig) (*RedisClient, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Address,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  time.Duration(cfg.DialTimeout) * time.Second,
		ReadTimeout:  time.Duration(cfg.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.WriteTimeout) * time.Second,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: 5,
		MaxRetries:   cfg.MaxRetries,
	})

	return &RedisClient{Client: rdb}, nil
}

// Ping tests the Redis connection
func (c *RedisClient) Ping(ctx context.Context) error {
	if err := c.Client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}
	return nil
}

// Close closes the Redis connection
func (c *RedisClient) Close() error {
	if c.Client != nil {
		return c.Client.Close()
	}
	return nil
}

// Get retrieves a value by key
func (c *RedisClient) Get(ctx context.Context, key string) (string, error) {
	return c.Client.Get(ctx, key).Result()
}

// Set sets a value with optional expiration
func (c *RedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return c.Client.Set(ctx, key, value, expiration).Err()
}

// Del deletes one or more keys
func (c *RedisClient) Del(ctx context.Context, keys ...string) error {
	return c.Client.Del(ctx, keys...).Err()
}

// GetClient returns the underlying *redis.Client for compatibility
func (c *RedisClient) GetClient() *redis.Client {
	return c.Client
}

// ============================================================================
// SESSION MANAGEMENT METHODS
// ============================================================================

// SaveSession saves a session to Redis with TTL
func (c *RedisClient) SaveSession(ctx context.Context, sessionID string, data interface{}, ttl time.Duration) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal session data: %w", err)
	}

	key := fmt.Sprintf("session:%s", sessionID)
	return c.Set(ctx, key, jsonData, ttl)
}

// GetSession retrieves a session from Redis
func (c *RedisClient) GetSession(ctx context.Context, sessionID string) (string, error) {
	key := fmt.Sprintf("session:%s", sessionID)
	return c.Get(ctx, key)
}

// GetSessionAs retrieves a session and unmarshals it into a struct
func (c *RedisClient) GetSessionAs(ctx context.Context, sessionID string, v interface{}) error {
	data, err := c.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(data), v)
}

// DeleteSession deletes a session from Redis
func (c *RedisClient) DeleteSession(ctx context.Context, sessionID string) error {
	key := fmt.Sprintf("session:%s", sessionID)
	return c.Del(ctx, key)
}

// SessionExists checks if a session exists
func (c *RedisClient) SessionExists(ctx context.Context, sessionID string) (bool, error) {
	key := fmt.Sprintf("session:%s", sessionID)
	exists, err := c.Client.Exists(ctx, key).Result()
	return exists > 0, err
}

// ExtendSession extends the TTL of a session
func (c *RedisClient) ExtendSession(ctx context.Context, sessionID string, ttl time.Duration) error {
	key := fmt.Sprintf("session:%s", sessionID)
	return c.Client.Expire(ctx, key, ttl).Err()
}

// ============================================================================
// TOKEN MANAGEMENT METHODS
// ============================================================================

// SaveRefreshToken saves a refresh token for a user
func (c *RedisClient) SaveRefreshToken(ctx context.Context, userID string, token string, ttl time.Duration) error {
	key := fmt.Sprintf("refresh_token:%s:%s", userID, token)
	return c.Set(ctx, key, "active", ttl)
}

// GetRefreshToken checks if a refresh token exists and is valid
func (c *RedisClient) GetRefreshToken(ctx context.Context, userID, token string) (bool, error) {
	key := fmt.Sprintf("refresh_token:%s:%s", userID, token)
	exists, err := c.Client.Exists(ctx, key).Result()
	return exists > 0, err
}

// DeleteRefreshToken removes a specific refresh token
func (c *RedisClient) DeleteRefreshToken(ctx context.Context, userID, token string) error {
	key := fmt.Sprintf("refresh_token:%s:%s", userID, token)
	return c.Del(ctx, key)
}

// DeleteAllRefreshTokens removes all refresh tokens for a user
func (c *RedisClient) DeleteAllRefreshTokens(ctx context.Context, userID string) error {
	pattern := fmt.Sprintf("refresh_token:%s:*", userID)

	iter := c.Client.Scan(ctx, 0, pattern, 0).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}

	if err := iter.Err(); err != nil {
		return fmt.Errorf("failed to scan refresh tokens: %w", err)
	}

	if len(keys) > 0 {
		return c.Del(ctx, keys...)
	}
	return nil
}

// ============================================================================
// ACCESS TOKEN BLACKLIST METHODS
// ============================================================================

// BlacklistToken adds a token to the blacklist until expiry
func (c *RedisClient) BlacklistToken(ctx context.Context, token string, expiry time.Duration) error {
	key := fmt.Sprintf("blacklist:token:%s", token)
	return c.Set(ctx, key, "blacklisted", expiry)
}

// IsTokenBlacklisted checks if a token is blacklisted
func (c *RedisClient) IsTokenBlacklisted(ctx context.Context, token string) (bool, error) {
	key := fmt.Sprintf("blacklist:token:%s", token)
	exists, err := c.Client.Exists(ctx, key).Result()
	return exists > 0, err
}

// ============================================================================
// RATE LIMITING METHODS
// ============================================================================
// IncrementRateLimit increments a rate limit counter
func (c *RedisClient) IncrementRateLimit(ctx context.Context, key string, window time.Duration) (int64, error) {
	current := time.Now().Unix()
	windowStart := current - int64(window.Seconds())

	// Remove old entries
	_, err := c.Client.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart)).Result()
	if err != nil {
		return 0, err
	}

	// Add current request
	_, err = c.Client.ZAdd(ctx, key, redis.Z{
		Score:  float64(current),
		Member: current,
	}).Result()
	if err != nil {
		return 0, err
	}

	// Set expiration
	_, err = c.Client.Expire(ctx, key, window+time.Second).Result()
	if err != nil {
		return 0, err
	}

	// Count requests in window
	return c.Client.ZCount(ctx, key, fmt.Sprintf("%d", windowStart), fmt.Sprintf("%d", current)).Result()
}

// ============================================================================
// UTILITY METHODS
// ============================================================================

// SetWithExpiry is an alias for Set with explicit TTL
func (c *RedisClient) SetWithExpiry(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return c.Set(ctx, key, value, ttl)
}

// GetWithTTL retrieves a value with its remaining TTL
func (c *RedisClient) GetWithTTL(ctx context.Context, key string) (string, time.Duration, error) {
	val, err := c.Get(ctx, key)
	if err != nil {
		return "", 0, err
	}

	ttl, err := c.Client.TTL(ctx, key).Result()
	return val, ttl, err
}

// HashSet sets a field in a hash
func (c *RedisClient) HashSet(ctx context.Context, key, field string, value interface{}) error {
	return c.Client.HSet(ctx, key, field, value).Err()
}

// HashGet gets a field from a hash
func (c *RedisClient) HashGet(ctx context.Context, key, field string) (string, error) {
	return c.Client.HGet(ctx, key, field).Result()
}

// HashGetAll gets all fields from a hash
func (c *RedisClient) HashGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.Client.HGetAll(ctx, key).Result()
}

// HashDelete deletes a field from a hash
func (c *RedisClient) HashDelete(ctx context.Context, key string, fields ...string) error {
	return c.Client.HDel(ctx, key, fields...).Err()
}

// Increment increments a counter
func (c *RedisClient) Increment(ctx context.Context, key string) (int64, error) {
	return c.Client.Incr(ctx, key).Result()
}

// IncrementBy increments a counter by specified value
func (c *RedisClient) IncrementBy(ctx context.Context, key string, value int64) (int64, error) {
	return c.Client.IncrBy(ctx, key, value).Result()
}

// Decrement decrements a counter
func (c *RedisClient) Decrement(ctx context.Context, key string) (int64, error) {
	return c.Client.Decr(ctx, key).Result()
}

// Keys finds keys by pattern
func (c *RedisClient) Keys(ctx context.Context, pattern string) ([]string, error) {
	return c.Client.Keys(ctx, pattern).Result()
}

// Scan finds keys by pattern with pagination
func (c *RedisClient) Scan(ctx context.Context, cursor uint64, pattern string, count int64) ([]string, uint64, error) {
	return c.Client.Scan(ctx, cursor, pattern, count).Result()
}

// ============================================================================
// PUB/SUB METHODS FOR WORKFLOW RESPONSES
// ============================================================================

// PublishWorkflowResponse publishes workflow response to Redis channel
func (c *RedisClient) PublishWorkflowResponse(ctx context.Context, correlationKey string, response map[string]interface{}) error {
	channel := fmt.Sprintf("workflow:response:%s", correlationKey)

	responseJSON, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	// Publish to channel
	err = c.Client.Publish(ctx, channel, responseJSON).Err()
	if err != nil {
		return fmt.Errorf("failed to publish to Redis: %w", err)
	}

	// Also cache with TTL (backup for retries)
	cacheKey := fmt.Sprintf("workflow:response:cache:%s", correlationKey)
	_ = c.Set(ctx, cacheKey, responseJSON, 60*time.Second)

	return nil
}

// WaitForWorkflowResponse subscribes and waits for workflow response
func (c *RedisClient) WaitForWorkflowResponse(
	ctx context.Context,
	correlationKey string,
	timeout time.Duration,
) (map[string]interface{}, error) {

	channel := fmt.Sprintf("workflow:response:%s", correlationKey)

	// Subscribe to channel
	pubsub := c.Client.Subscribe(ctx, channel)
	defer pubsub.Close()

	// Wait for subscription confirmation
	_, err := pubsub.Receive(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to confirm subscription: %w", err)
	}

	// Get message channel
	ch := pubsub.Channel()

	// Wait with timeout
	select {
	case msg := <-ch:
		var response map[string]interface{}
		if err := json.Unmarshal([]byte(msg.Payload), &response); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
		return response, nil

	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for workflow response after %v", timeout)

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetCachedWorkflowResponse retrieves cached workflow response (for retries)
func (c *RedisClient) GetCachedWorkflowResponse(ctx context.Context, correlationKey string) (map[string]interface{}, bool, error) {
	cacheKey := fmt.Sprintf("workflow:response:cache:%s", correlationKey)

	val, err := c.Client.Get(ctx, cacheKey).Result()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	var response map[string]interface{}
	if err := json.Unmarshal([]byte(val), &response); err != nil {
		return nil, false, err
	}

	return response, true, nil
}
