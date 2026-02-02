// internal/common/idempotency/redis_store.go
package idempotency

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore implements Redis-based idempotency store
// Used for API Gateway level idempotency (fast checks)
type RedisStore struct {
	client *redis.Client
}

// NewRedisStore creates a new Redis store
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

// ============================================================================
// API REQUEST IDEMPOTENCY (Fast Cache Layer)
// ============================================================================

// StoreAPIRequest stores API request idempotency key with response
func (rs *RedisStore) StoreAPIRequest(ctx context.Context, key string, response map[string]interface{}, ttl time.Duration) error {
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	err = rs.client.Set(ctx, fmt.Sprintf("api:idempotency:%s", key), responseJSON, ttl).Err()
	if err != nil {
		return fmt.Errorf("failed to store idempotency key: %w", err)
	}

	return nil
}

// CheckAPIRequest checks if API request was already processed
func (rs *RedisStore) CheckAPIRequest(ctx context.Context, key string) (map[string]interface{}, bool, error) {
	val, err := rs.client.Get(ctx, fmt.Sprintf("api:idempotency:%s", key)).Result()

	if err == redis.Nil {
		// Not found - first time request
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("failed to check idempotency: %w", err)
	}

	// Found - duplicate request
	var response map[string]interface{}
	if err := json.Unmarshal([]byte(val), &response); err != nil {
		return nil, true, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return response, true, nil
}

// ============================================================================
// WORKER IDEMPOTENCY (In-Progress Tracking)
// ============================================================================

// MarkWorkerProcessing marks worker as processing with timeout
func (rs *RedisStore) MarkWorkerProcessing(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	// Use SETNX to atomically set only if not exists
	success, err := rs.client.SetNX(ctx, fmt.Sprintf("worker:processing:%s", key), time.Now().Unix(), ttl).Result()
	if err != nil {
		return false, fmt.Errorf("failed to mark processing: %w", err)
	}

	return success, nil
}

// UnmarkWorkerProcessing removes processing marker
func (rs *RedisStore) UnmarkWorkerProcessing(ctx context.Context, key string) error {
	err := rs.client.Del(ctx, fmt.Sprintf("worker:processing:%s", key)).Err()
	if err != nil {
		return fmt.Errorf("failed to unmark processing: %w", err)
	}

	return nil
}

// IsWorkerProcessing checks if worker is currently processing
func (rs *RedisStore) IsWorkerProcessing(ctx context.Context, key string) (bool, error) {
	exists, err := rs.client.Exists(ctx, fmt.Sprintf("worker:processing:%s", key)).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check processing: %w", err)
	}

	return exists > 0, nil
}

// ============================================================================
// DISTRIBUTED LOCK (For Critical Operations)
// ============================================================================

// AcquireLock acquires distributed lock
func (rs *RedisStore) AcquireLock(ctx context.Context, resource string, ttl time.Duration) (string, bool, error) {
	lockKey := fmt.Sprintf("lock:%s", resource)
	lockValue := fmt.Sprintf("%d", time.Now().UnixNano())

	success, err := rs.client.SetNX(ctx, lockKey, lockValue, ttl).Result()
	if err != nil {
		return "", false, fmt.Errorf("failed to acquire lock: %w", err)
	}

	return lockValue, success, nil
}

// ReleaseLock releases distributed lock
func (rs *RedisStore) ReleaseLock(ctx context.Context, resource, lockValue string) error {
	lockKey := fmt.Sprintf("lock:%s", resource)

	// Lua script for atomic check-and-delete
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`

	result, err := rs.client.Eval(ctx, script, []string{lockKey}, lockValue).Result()
	if err != nil {
		return fmt.Errorf("failed to release lock: %w", err)
	}

	if result.(int64) == 0 {
		return fmt.Errorf("lock already released or expired")
	}

	return nil
}

// ============================================================================
// CACHE HELPER FUNCTIONS
// ============================================================================

// CacheResult caches worker result for fast retrieval
func (rs *RedisStore) CacheResult(ctx context.Context, key string, result map[string]interface{}, ttl time.Duration) error {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	err = rs.client.Set(ctx, fmt.Sprintf("cache:result:%s", key), resultJSON, ttl).Err()
	if err != nil {
		return fmt.Errorf("failed to cache result: %w", err)
	}

	return nil
}

// GetCachedResult retrieves cached result
func (rs *RedisStore) GetCachedResult(ctx context.Context, key string) (map[string]interface{}, bool, error) {
	val, err := rs.client.Get(ctx, fmt.Sprintf("cache:result:%s", key)).Result()

	if err == redis.Nil {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("failed to get cached result: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(val), &result); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal result: %w", err)
	}

	return result, true, nil
}

// ============================================================================
// STATISTICS & MONITORING
// ============================================================================

// IncrementCounter increments a counter metric
func (rs *RedisStore) IncrementCounter(ctx context.Context, counterName string) error {
	key := fmt.Sprintf("metrics:counter:%s", counterName)
	err := rs.client.Incr(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("failed to increment counter: %w", err)
	}
	return nil
}

// GetCounter gets counter value
func (rs *RedisStore) GetCounter(ctx context.Context, counterName string) (int64, error) {
	key := fmt.Sprintf("metrics:counter:%s", counterName)
	val, err := rs.client.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get counter: %w", err)
	}
	return val, nil
}

// RecordLatency records operation latency
func (rs *RedisStore) RecordLatency(ctx context.Context, operation string, latencyMs int64) error {
	key := fmt.Sprintf("metrics:latency:%s", operation)

	// Store last 100 latencies
	pipe := rs.client.Pipeline()
	pipe.LPush(ctx, key, latencyMs)
	pipe.LTrim(ctx, key, 0, 99)
	pipe.Expire(ctx, key, 24*time.Hour)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to record latency: %w", err)
	}

	return nil
}

// GetAverageLatency gets average latency for operation
func (rs *RedisStore) GetAverageLatency(ctx context.Context, operation string) (float64, error) {
	key := fmt.Sprintf("metrics:latency:%s", operation)

	vals, err := rs.client.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get latencies: %w", err)
	}

	if len(vals) == 0 {
		return 0, nil
	}

	var sum int64
	for _, v := range vals {
		var latency int64
		fmt.Sscanf(v, "%d", &latency)
		sum += latency
	}

	return float64(sum) / float64(len(vals)), nil
}

// ============================================================================
// CLEANUP
// ============================================================================

// Ping checks Redis connection
func (rs *RedisStore) Ping(ctx context.Context) error {
	return rs.client.Ping(ctx).Err()
}

// FlushIdempotencyKeys removes all idempotency keys (use with caution!)
func (rs *RedisStore) FlushIdempotencyKeys(ctx context.Context) error {
	iter := rs.client.Scan(ctx, 0, "api:idempotency:*", 100).Iterator()
	for iter.Next(ctx) {
		rs.client.Del(ctx, iter.Val())
	}
	return iter.Err()
}
