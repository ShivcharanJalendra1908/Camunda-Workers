package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *redis.Client
	prefix string
}

// NewRedisStore creates a Redis-backed session store.
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{
		client: client,
		prefix: "session:",
	}
}

func (r *RedisStore) key(sessionID string) string {
	return r.prefix + sessionID
}

func (r *RedisStore) Create(ctx context.Context, s Session) error {
	if s.SessionID == "" || s.UserID == "" {
		return fmt.Errorf("session: missing session_id or user_id")
	}

	ttl := time.Until(s.ExpiresAt)
	if ttl <= 0 {
		return fmt.Errorf("session: expires_at must be in the future")
	}

	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("session: failed to marshal: %w", err)
	}

	return r.client.Set(ctx, r.key(s.SessionID), data, ttl).Err()
}

func (r *RedisStore) Get(ctx context.Context, sessionID string) (*Session, error) {
	val, err := r.client.Get(ctx, r.key(sessionID)).Result()
	if err == redis.Nil {
		return nil, nil // not found
	}
	if err != nil {
		return nil, err
	}

	var s Session
	if err := json.Unmarshal([]byte(val), &s); err != nil {
		return nil, fmt.Errorf("session: failed to unmarshal: %w", err)
	}

	return &s, nil
}

func (r *RedisStore) Delete(ctx context.Context, sessionID string) error {
	return r.client.Del(ctx, r.key(sessionID)).Err()
}

func (r *RedisStore) Update(ctx context.Context, s Session) error {
	if s.SessionID == "" {
		return fmt.Errorf("session: missing session_id")
	}

	ttl := time.Until(s.ExpiresAt)
	if ttl <= 0 {
		// If expired, delete session instead of extending
		return r.client.Del(ctx, r.key(s.SessionID)).Err()
	}

	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("session: failed to marshal: %w", err)
	}

	return r.client.Set(ctx, r.key(s.SessionID), data, ttl).Err()
}
