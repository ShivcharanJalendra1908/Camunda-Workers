// Package authlogout handles user logout requests.
package authlogout

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"

	"github.com/go-redis/redis/v8"
)

type Service struct {
	config      *Config
	logger      logger.Logger
	redisClient *redis.Client
}

func NewService(deps ServiceDependencies, config *Config) *Service {
	var redisClient *redis.Client
	if config.RedisHost != "" {
		redisClient = redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", config.RedisHost, config.RedisPort),
			Password: config.RedisPassword,
			DB:       config.RedisDB,
		})
	}

	return &Service{
		config:      config,
		logger:      deps.Logger,
		redisClient: redisClient,
	}
}

func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {
	s.logger.Info("Executing auth logout", map[string]interface{}{
		"userId":    input.UserID,
		"sessionId": input.SessionID,
		"logoutAll": input.LogoutAll,
		"deviceId":  input.DeviceID,
		"reason":    input.Reason,
	})

	if s.redisClient == nil {
		return nil, &errors.StandardError{
			Code:      "REDIS_NOT_CONFIGURED",
			Message:   "Redis client not configured",
			Details:   "Session management requires Redis",
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Validate user ID
	if err := s.validateUserID(input.UserID); err != nil {
		return nil, &errors.StandardError{
			Code:      "VALIDATION_FAILED",
			Message:   "Invalid user ID",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	var sessionsInvalidated int
	var tokenRevoked bool

	if input.LogoutAll {
		// Logout from all sessions
		count, err := s.invalidateAllSessions(ctx, input.UserID)
		if err != nil {
			return nil, &errors.StandardError{
				Code:      "SESSION_INVALIDATION_ERROR",
				Message:   "Failed to invalidate all sessions",
				Details:   err.Error(),
				Retryable: true,
				Timestamp: time.Now(),
			}
		}
		sessionsInvalidated = count
	} else if input.SessionID != "" {
		// Logout from specific session
		err := s.invalidateSession(ctx, input.UserID, input.SessionID)
		if err != nil {
			return nil, &errors.StandardError{
				Code:      "SESSION_INVALIDATION_ERROR",
				Message:   "Failed to invalidate session",
				Details:   err.Error(),
				Retryable: true,
				Timestamp: time.Now(),
			}
		}
		sessionsInvalidated = 1
	}

	// Revoke token if provided
	if input.Token != "" {
		err := s.revokeToken(ctx, input.Token)
		if err != nil {
			s.logger.Warn("Failed to revoke token", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			tokenRevoked = true
		}
	}

	// Log logout event
	s.logLogoutEvent(ctx, input)

	s.logger.Info("Auth logout completed successfully", map[string]interface{}{
		"userId":              input.UserID,
		"sessionsInvalidated": sessionsInvalidated,
		"tokenRevoked":        tokenRevoked,
	})

	return &Output{
		Success:             true,
		Message:             "Logout successful",
		SessionsInvalidated: sessionsInvalidated,
		TokenRevoked:        tokenRevoked,
		LogoutAt:            time.Now(),
	}, nil
}

func (s *Service) validateUserID(userID string) error {
	if userID == "" {
		return fmt.Errorf("user ID is required")
	}
	if len(userID) < 3 {
		return fmt.Errorf("user ID too short")
	}
	return nil
}

func (s *Service) invalidateSession(ctx context.Context, userID, sessionID string) error {
	// Delete session from Redis
	sessionKey := fmt.Sprintf("session:%s:%s", userID, sessionID)
	err := s.redisClient.Del(ctx, sessionKey).Err()
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	s.logger.Info("Session invalidated", map[string]interface{}{
		"userId":    userID,
		"sessionId": sessionID,
	})

	return nil
}

func (s *Service) invalidateAllSessions(ctx context.Context, userID string) (int, error) {
	// Find all sessions for user
	pattern := fmt.Sprintf("session:%s:*", userID)
	keys, err := s.redisClient.Keys(ctx, pattern).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to find sessions: %w", err)
	}

	if len(keys) == 0 {
		return 0, nil
	}

	// Delete all sessions
	err = s.redisClient.Del(ctx, keys...).Err()
	if err != nil {
		return 0, fmt.Errorf("failed to delete sessions: %w", err)
	}

	s.logger.Info("All sessions invalidated", map[string]interface{}{
		"userId": userID,
		"count":  len(keys),
	})

	return len(keys), nil
}

func (s *Service) revokeToken(ctx context.Context, token string) error {
	// Add token to revocation list in Redis
	revokedKey := fmt.Sprintf("token:revoked:%s", token)

	// Store for 24 hours (typical JWT expiration)
	err := s.redisClient.Set(ctx, revokedKey, "1", 24*time.Hour).Err()
	if err != nil {
		return fmt.Errorf("failed to revoke token: %w", err)
	}

	s.logger.Info("Token revoked", map[string]interface{}{
		"token": token[:10] + "...", // Log only first 10 chars for security
	})

	return nil
}

func (s *Service) logLogoutEvent(ctx context.Context, input *Input) {
	eventKey := fmt.Sprintf("logout:event:%s:%d", input.UserID, time.Now().Unix())
	eventData := map[string]interface{}{
		"userId":    input.UserID,
		"sessionId": input.SessionID,
		"deviceId":  input.DeviceID,
		"logoutAll": input.LogoutAll,
		"reason":    input.Reason,
		"timestamp": time.Now().Unix(),
	}

	// Store logout event for audit trail
	data, _ := json.Marshal(eventData)
	s.redisClient.Set(ctx, eventKey, string(data), 30*24*time.Hour) // Keep for 30 days
}

func (s *Service) TestConnection(ctx context.Context) error {
	if s.redisClient == nil {
		return fmt.Errorf("redis client not configured")
	}

	// Test Redis connection
	_, err := s.redisClient.Ping(ctx).Result()
	if err != nil {
		return fmt.Errorf("redis connection failed: %w", err)
	}

	return nil
}
