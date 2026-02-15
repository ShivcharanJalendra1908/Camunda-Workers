package sessionmanager

import (
	"context"
	"fmt"
	"net/http"
	"time"

	cerrors "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"

	"camunda-workers/internal/common/auth/session"

	"github.com/redis/go-redis/v9"
)

type ServiceDependencies struct {
	Redis  *redis.Client
	Logger logger.Logger
}

type Service struct {
	config       *Config
	sessionStore *session.RedisStore
	redis        *redis.Client
	logger       logger.Logger
}

func NewService(deps ServiceDependencies, config *Config) *Service {
	// ✅ FIX: Use session.NewRedisStore directly
	return &Service{
		config:       config,
		sessionStore: session.NewRedisStore(deps.Redis),
		redis:        deps.Redis,
		logger:       deps.Logger,
	}
}

func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {
	switch input.Action {
	case "create":
		return s.handleCreate(ctx, input)
	case "get":
		return s.handleGet(ctx, input)
	case "delete":
		return s.handleDelete(ctx, input)
	default:
		return nil, &cerrors.StandardError{
			Code:      "INVALID_ACTION",
			Message:   fmt.Sprintf("Unknown action: %s", input.Action),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}
}

func (s *Service) handleCreate(ctx context.Context, input *Input) (*Output, error) {
	// ✅ FIX: Generate session ID first
	sessionID, err := session.GenerateID()
	if err != nil {
		return nil, &cerrors.StandardError{
			Code:      "SESSION_ID_GENERATION_FAILED",
			Message:   "Failed to generate session ID",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Calculate expiry
	expiresAt := time.Now().Add(time.Duration(input.ExpiresIn) * time.Second)

	// Create session object
	sess := session.Session{
		SessionID: sessionID,
		UserID:    input.UserID,
		ExpiresAt: expiresAt,
	}

	// ✅ FIX: Store in Redis (pass session struct)
	if err := s.sessionStore.Create(ctx, sess); err != nil {
		return nil, &cerrors.StandardError{
			Code:      "SESSION_CREATION_FAILED",
			Message:   "Failed to create session in Redis",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Build cookie header
	cookieHeader := s.buildSetCookieHeader(sessionID, expiresAt)

	s.logger.Info("Session created successfully", map[string]interface{}{
		"sessionId": sessionID,
		"userId":    input.UserID,
		"expiresAt": expiresAt,
	})

	return &Output{
		Success:      true,
		SessionID:    sessionID,
		UserID:       input.UserID,
		Email:        input.Email,
		ExpiresAt:    expiresAt,
		CookieHeader: cookieHeader,
		Message:      "Session created successfully",
	}, nil
}

func (s *Service) handleGet(ctx context.Context, input *Input) (*Output, error) {
	// Retrieve session from Redis
	sess, err := s.sessionStore.Get(ctx, input.SessionID)
	if err != nil {
		return nil, &cerrors.StandardError{
			Code:      "SESSION_NOT_FOUND",
			Message:   "Session not found or expired",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// ✅ FIX: Check for nil session
	if sess == nil {
		return nil, &cerrors.StandardError{
			Code:      "SESSION_NOT_FOUND",
			Message:   "Session not found or expired",
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	s.logger.Info("Session retrieved successfully", map[string]interface{}{
		"sessionId": sess.SessionID,
		"userId":    sess.UserID,
	})

	return &Output{
		Success:   true,
		SessionID: sess.SessionID,
		UserID:    sess.UserID,
		ExpiresAt: sess.ExpiresAt,
		Message:   "Session retrieved successfully",
	}, nil
}

func (s *Service) handleDelete(ctx context.Context, input *Input) (*Output, error) {
	// Delete session from Redis
	if err := s.sessionStore.Delete(ctx, input.SessionID); err != nil {
		return nil, &cerrors.StandardError{
			Code:      "SESSION_DELETION_FAILED",
			Message:   "Failed to delete session",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Build clear cookie header
	cookieHeader := s.buildClearCookieHeader()

	s.logger.Info("Session deleted successfully", map[string]interface{}{
		"sessionId": input.SessionID,
	})

	return &Output{
		Success:      true,
		SessionID:    input.SessionID,
		CookieHeader: cookieHeader,
		Message:      "Session deleted successfully",
	}, nil
}

func (s *Service) buildSetCookieHeader(sessionID string, expiresAt time.Time) string {
	maxAge := int(time.Until(expiresAt).Seconds())

	cookie := &http.Cookie{
		Name:     s.config.CookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: s.config.HttpOnly,
		Secure:   s.config.Secure,
		MaxAge:   maxAge,
	}

	switch s.config.SameSite {
	case "Strict":
		cookie.SameSite = http.SameSiteStrictMode
	case "None":
		cookie.SameSite = http.SameSiteNoneMode
	default:
		cookie.SameSite = http.SameSiteLaxMode
	}

	return cookie.String()
}

func (s *Service) buildClearCookieHeader() string {
	cookie := &http.Cookie{
		Name:     s.config.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: s.config.HttpOnly,
		Secure:   s.config.Secure,
		MaxAge:   -1, // Immediate expiry
	}

	switch s.config.SameSite {
	case "Strict":
		cookie.SameSite = http.SameSiteStrictMode
	case "None":
		cookie.SameSite = http.SameSiteNoneMode
	default:
		cookie.SameSite = http.SameSiteLaxMode
	}

	return cookie.String()
}

func (s *Service) TestConnection(ctx context.Context) error {
	return s.redis.Ping(ctx).Err()
}
