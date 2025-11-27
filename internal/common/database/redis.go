// internal/common/database/redis.go
package database

import (
	"context"
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
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
		MinIdleConns: 5,
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

// // internal/common/database/redis.go
// package database

// import (
// 	"context"
// 	"fmt"
// 	"time"

// 	"camunda-workers/internal/common/config"

// 	"github.com/redis/go-redis/v9"
// )

// // RedisClient wraps the Redis client
// type RedisClient struct {
// 	Client *redis.Client
// }

// // NewRedis creates a new Redis client
// func NewRedis(cfg config.RedisConfig) (*RedisClient, error) {
// 	rdb := redis.NewClient(&redis.Options{
// 		Addr:         cfg.Address,
// 		Password:     cfg.Password,
// 		DB:           cfg.DB,
// 		DialTimeout:  5 * time.Second,
// 		ReadTimeout:  3 * time.Second,
// 		WriteTimeout: 3 * time.Second,
// 		PoolSize:     10,
// 		MinIdleConns: 5,
// 	})

// 	return &RedisClient{Client: rdb}, nil
// }

// // Ping tests the Redis connection
// func (c *RedisClient) Ping(ctx context.Context) error {
// 	if err := c.Client.Ping(ctx).Err(); err != nil {
// 		return fmt.Errorf("redis ping failed: %w", err)
// 	}
// 	return nil
// }

// // Close closes the Redis connection
// func (c *RedisClient) Close() error {
// 	if c.Client != nil {
// 		return c.Client.Close()
// 	}
// 	return nil
// }

// // Get retrieves a value by key
// func (c *RedisClient) Get(ctx context.Context, key string) (string, error) {
// 	return c.Client.Get(ctx, key).Result()
// }

// // Set sets a value with optional expiration
// func (c *RedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
// 	return c.Client.Set(ctx, key, value, expiration).Err()
// }

// // Del deletes one or more keys
// func (c *RedisClient) Del(ctx context.Context, keys ...string) error {
// 	return c.Client.Del(ctx, keys...).Err()
// }

// // GetClient returns the underlying *redis.Client for compatibility
// func (c *RedisClient) GetClient() *redis.Client {
// 	return c.Client
// }
