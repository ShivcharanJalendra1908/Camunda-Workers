package authlogout

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"

	"github.com/redis/go-redis/v9"
)

type Service struct {
	config      *Config
	logger      logger.Logger
	keycloak    *auth.KeycloakClient
	redisClient *redis.Client
	db          *sql.DB
}

func NewService(deps ServiceDependencies, config *Config) *Service {
	// var redisClient *redis.Client
	// if config.RedisHost != "" {
	// 	redisClient = redis.NewClient(&redis.Options{
	// 		Addr:     fmt.Sprintf("%s:%d", config.RedisHost, config.RedisPort),
	// 		Password: config.RedisPassword,
	// 		DB:       config.RedisDB,
	// 	})
	// }

	return &Service{
		config:      config,
		logger:      deps.Logger,
		keycloak:    deps.Keycloak,
		redisClient: deps.RedisClient,
		db:          deps.DB,
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

	var sessionsInvalidated int
	var tokenRevoked bool

	// Step 1: Keycloak server-side session revoke
	if s.keycloak != nil {
		var err error
		if input.LogoutAll && input.KeycloakUserID != "" {
			err = s.keycloak.RevokeAllUserSessions(ctx, input.KeycloakUserID)
			s.logger.Info("Keycloak global logout initiated", map[string]interface{}{"keycloakUserId": input.KeycloakUserID})
		} else if input.SessionID != "" {
			err = s.keycloak.DeleteSession(ctx, input.SessionID)
			s.logger.Info("Keycloak targeted session deletion initiated", map[string]interface{}{"sessionId": input.SessionID})
		}

		if err != nil {
			s.logger.Warn("Keycloak session revoke failed", map[string]interface{}{
				"error":     err.Error(),
				"sessionId": input.SessionID,
				"userId":    input.KeycloakUserID,
			})
		} else if (input.LogoutAll && input.KeycloakUserID != "") || input.SessionID != "" {
			tokenRevoked = true
			sessionsInvalidated = 1
			s.logger.Info("Keycloak session(s) revoked successfully", nil)
		}
	}


	// Step 2: Redis local session cleanup
	if s.redisClient != nil {
		if err := s.cleanupLocalSessions(ctx, input); err != nil {
			s.logger.Warn("Failed to cleanup local sessions", map[string]interface{}{
				"userId": input.UserID,
				"error":  err.Error(),
			})
		}
	}

	// Step 3: Audit log
	if s.redisClient != nil {
		s.logLogoutEvent(ctx, input, sessionsInvalidated, tokenRevoked)
	}

	// Step 4: Build Keycloak browser logout URL (frontend MUST redirect the browser here to clear SSO cookies)
	params := url.Values{}
	params.Add("post_logout_redirect_uri", s.config.PostLogoutRedirectURI)
	params.Add("client_id", s.config.ClientID)
	
	// Keycloak 17+ requires id_token_hint for redirect to work correctly
	if input.IDToken != "" {
		params.Add("id_token_hint", input.IDToken)
	}

	logoutURL := fmt.Sprintf("%s/protocol/openid-connect/logout?%s", s.config.Issuer, params.Encode())

	s.logger.Info("Auth logout completed successfully", map[string]interface{}{
		"userId":              input.UserID,
		"sessionsInvalidated": sessionsInvalidated,
		"tokenRevoked":        tokenRevoked,
	})

	return &Output{
		Success:             true,
		Message:             "Logged out successfully",
		SessionsInvalidated: sessionsInvalidated,
		TokenRevoked:        tokenRevoked,
		LogoutURL:           logoutURL,
		LogoutAt:            time.Now(),
	}, nil
}

// func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	s.logger.Info("Executing auth logout", map[string]interface{}{
// 		"userId":    input.UserID,
// 		"sessionId": input.SessionID,
// 		"logoutAll": input.LogoutAll,
// 		"deviceId":  input.DeviceID,
// 		"reason":    input.Reason,
// 	})

// 	// NOTE: Input is already validated and sanitized by the handler

// 	// 🔒 Safety: Keycloak client must not be nil
// 	if s.keycloak == nil {
// 		s.logger.Warn("Keycloak client not configured, skipping Keycloak logout", map[string]interface{}{
// 			"userId": input.UserID,
// 		})
// 	}

// 	var sessionsInvalidated int
// 	var tokenRevoked bool

// 	// Step 1: Revoke refresh token in Keycloak (single session logout)
// 	if input.RefreshToken != "" && !input.LogoutAll && s.keycloak != nil {
// 		err := s.keycloak.Logout(ctx, input.RefreshToken)
// 		if err != nil {
// 			s.logger.Warn("Failed to logout from Keycloak", map[string]interface{}{
// 				"userId": input.UserID,
// 				"error":  err.Error(),
// 			})
// 		} else {
// 			tokenRevoked = true
// 			sessionsInvalidated = 1
// 		}
// 	}

//     // Step 2: Global logout - revoke ALL sessions in Keycloak
// 	if input.LogoutAll && s.keycloak != nil {
// 		kcUserID := input.KeycloakUserID

// 		if kcUserID == "" && s.db != nil {
// 			var providerUserID string
// 			err := s.db.QueryRowContext(ctx,
// 				`SELECT provider_user_id FROM identities WHERE user_id = $1 AND provider = 'keycloak' LIMIT 1`,
// 				input.UserID,
// 			).Scan(&providerUserID)
// 			if err == nil {
// 				kcUserID = providerUserID
// 				s.logger.Info("Resolved keycloakUserId from DB", map[string]interface{}{
// 					"userId":         input.UserID,
// 					"keycloakUserId": kcUserID,
// 				})
// 			} else {
// 				s.logger.Warn("Could not resolve keycloakUserId from DB, skipping Keycloak revoke", map[string]interface{}{
// 					"userId": input.UserID,
// 					"error":  err.Error(),
// 				})
// 			}
// 		}

// 		if kcUserID != "" {
// 			s.logger.Info("Attempting Keycloak revoke all sessions", map[string]interface{}{
// 				"keycloakUserId": kcUserID,
// 			})
// 			err := s.keycloak.RevokeAllUserSessions(ctx, kcUserID)
// 			if err != nil {
// 				s.logger.Error("Keycloak revoke error", map[string]interface{}{
// 					"error":          err.Error(),
// 					"keycloakUserId": kcUserID,
// 				})
// 				if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "User not found") {
// 					s.logger.Warn("User not found in Keycloak, treating as already logged out", nil)
// 				} else {
// 					return nil, &errors.StandardError{
// 						Code:      "KEYCLOAK_LOGOUT_FAILED",
// 						Message:   "Failed to revoke all user sessions in Keycloak",
// 						Details:   err.Error(),
// 						Retryable: true,
// 						Timestamp: time.Now(),
// 					}
// 				}
// 			} else {
// 				s.logger.Info("Keycloak revoke all sessions successful", map[string]interface{}{
// 					"keycloakUserId": kcUserID,
// 				})
// 				tokenRevoked = true
// 			}
// 		}
// 	}

// 	// Step 3: Clean up local Redis sessions
// 	if s.redisClient != nil {
// 		err := s.cleanupLocalSessions(ctx, input)
// 		if err != nil {
// 			s.logger.Warn("Failed to cleanup local sessions", map[string]interface{}{
// 				"userId": input.UserID,
// 				"error":  err.Error(),
// 			})
// 			// Don't fail the entire logout if Redis cleanup fails
// 		}
// 	}

// 	// Step 4: Add access token to revocation list (if provided)
// 	if input.AccessToken != "" && s.redisClient != nil {
// 		err := s.revokeAccessToken(ctx, input.AccessToken)
// 		if err != nil {
// 			s.logger.Warn("Failed to add access token to revocation list", map[string]interface{}{
// 				"error": err.Error(),
// 			})
// 		}
// 	}

// 	// Step 5: Log logout event for audit trail
// 	if s.redisClient != nil {
// 		s.logLogoutEvent(ctx, input, sessionsInvalidated, tokenRevoked)
// 	}

// 	s.logger.Info("Auth logout completed successfully", map[string]interface{}{
// 		"userId":              input.UserID,
// 		"sessionsInvalidated": sessionsInvalidated,
// 		"tokenRevoked":        tokenRevoked,
// 		"logoutAll":           input.LogoutAll,
// 	})

// 	return &Output{
// 		Success:             true,
// 		Message:             s.buildSuccessMessage(input.LogoutAll, sessionsInvalidated),
// 		SessionsInvalidated: sessionsInvalidated,
// 		TokenRevoked:        tokenRevoked,
// 		LogoutAt:            time.Now(),
// 	}, nil
// }

func (s *Service) validateInput(input *Input) error {
	if input.UserID == "" {
		return fmt.Errorf("user ID is required")
	}

	if len(input.UserID) < 3 {
		return fmt.Errorf("user ID too short (minimum 3 characters)")
	}

	// For single session logout, require either refreshToken or sessionID
	if !input.LogoutAll && input.RefreshToken == "" && input.SessionID == "" {
		return fmt.Errorf("refresh token or session ID required for single session logout")
	}

	return nil
}

func (s *Service) countUserSessions(ctx context.Context, userID string) (int, error) {
	count, err := s.redisClient.SCard(ctx, "user_sessions:"+userID).Result()
	return int(count), err
}

func (s *Service) cleanupLocalSessions(ctx context.Context, input *Input) error {
	if input.LogoutAll {
		// Delete all sessions for user
		return s.invalidateAllLocalSessions(ctx, input.UserID)
	}

	if input.SessionID != "" {
		// Delete specific session
		return s.invalidateLocalSession(ctx, input.UserID, input.SessionID)
	}

	return nil
}

func (s *Service) invalidateLocalSession(ctx context.Context, userID, sessionID string) error {
	sessionKey := fmt.Sprintf("session:%s:%s", userID, sessionID)
	err := s.redisClient.Del(ctx, sessionKey).Err()
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	// Also delete any refresh token mapping
	refreshTokenKey := fmt.Sprintf("refresh_token:%s:%s", userID, sessionID)
	s.redisClient.Del(ctx, refreshTokenKey)

	s.logger.Info("Local session invalidated", map[string]interface{}{
		"userId":    userID,
		"sessionId": sessionID,
	})

	return nil
}

func (s *Service) invalidateAllLocalSessions(ctx context.Context, userID string) error {
	userKey := "user_sessions:" + userID

	sids, err := s.redisClient.SMembers(ctx, userKey).Result()
	if err != nil {
		return fmt.Errorf("failed to get user sessions: %w", err)
	}

	pipe := s.redisClient.TxPipeline()
	for _, sid := range sids {
		pipe.Del(ctx, "session:"+sid)
	}
	pipe.Del(ctx, userKey)
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete sessions: %w", err)
	}

	s.logger.Info("All local sessions invalidated", map[string]interface{}{
		"userId": userID,
		"count":  len(sids),
	})

	return nil
}

func (s *Service) revokeAccessToken(ctx context.Context, accessToken string) error {
	// Add access token to revocation list
	// Use a hash of the token to save space
	tokenKey := fmt.Sprintf("token:revoked:%s", s.hashToken(accessToken))

	// Store with TTL matching typical JWT expiration (1 hour default)
	ttl := 2 * time.Hour // Store for 2 hours to be safe
	err := s.redisClient.Set(ctx, tokenKey, time.Now().Unix(), ttl).Err()
	if err != nil {
		return fmt.Errorf("failed to revoke access token: %w", err)
	}

	s.logger.Debug("Access token added to revocation list", map[string]interface{}{
		"tokenPrefix": accessToken[:min(10, len(accessToken))] + "...",
	})

	return nil
}

func (s *Service) logLogoutEvent(ctx context.Context, input *Input, sessionsInvalidated int, tokenRevoked bool) {
	eventKey := fmt.Sprintf("logout:event:%s:%d", input.UserID, time.Now().Unix())
	eventData := map[string]interface{}{
		"userId":              input.UserID,
		"sessionId":           input.SessionID,
		"deviceId":            input.DeviceID,
		"logoutAll":           input.LogoutAll,
		"reason":              input.Reason,
		"sessionsInvalidated": sessionsInvalidated,
		"tokenRevoked":        tokenRevoked,
		"timestamp":           time.Now().Unix(),
		"metadata":            input.Metadata,
	}

	data, err := json.Marshal(eventData)
	if err != nil {
		s.logger.Warn("Failed to marshal logout event", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	// Store logout event for audit trail (keep for 90 days)
	err = s.redisClient.Set(ctx, eventKey, string(data), 90*24*time.Hour).Err()
	if err != nil {
		s.logger.Warn("Failed to log logout event", map[string]interface{}{
			"error": err.Error(),
		})
	}
}

func (s *Service) buildSuccessMessage(logoutAll bool, sessionsInvalidated int) string {
	if logoutAll {
		return fmt.Sprintf("Logged out from all sessions (%d sessions invalidated)", sessionsInvalidated)
	}
	return "Logged out successfully"
}

func (s *Service) hashToken(token string) string {
	// Simple hash for token storage - in production use crypto/sha256
	if len(token) > 32 {
		return token[:32]
	}
	return token
}

func (s *Service) TestConnection(ctx context.Context) error {
	// Test Keycloak connection
	if s.keycloak != nil {
		// Try to get a user to test connectivity
		testCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		_, err := s.keycloak.GetUserByEmail(testCtx, "healthcheck@test.com")
		if err != nil {
			if stdErr, ok := err.(*errors.StandardError); ok {
				// USER_NOT_FOUND is expected and means Keycloak is reachable
				if stdErr.Code != "USER_NOT_FOUND" {
					return fmt.Errorf("keycloak connection failed: %w", err)
				}
			}
		}
	}

	// Test Redis connection
	if s.redisClient != nil {
		testCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		_, err := s.redisClient.Ping(testCtx).Result()
		if err != nil {
			return fmt.Errorf("redis connection failed: %w", err)
		}
	}

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
