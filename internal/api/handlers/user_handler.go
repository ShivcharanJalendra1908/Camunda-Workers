// internal/api/handlers/user_handler.go
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"camunda-workers/internal/common/auth/session"
	"camunda-workers/internal/common/constants"
	"camunda-workers/internal/common/logger"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type UserHandler struct {
	redisClient *redis.Client
	log         logger.Logger
}

func NewUserHandler(redisClient *redis.Client, log logger.Logger) *UserHandler {
	return &UserHandler{
		redisClient: redisClient,
		log:         log,
	}
}

type UserProfileResponse struct {
	Success   bool        `json:"success"`
	Data      UserProfile `json:"data"`
	RequestID string      `json:"requestId"`
	Timestamp string      `json:"timestamp"`
}

type UserProfile struct {
	UserID    string `json:"userId"`
	SessionID string `json:"sessionId"`
	IsActive  bool   `json:"isActive"`
}

func (h *UserHandler) GetProfile(c *gin.Context) {
	requestID := c.GetString("requestId")
	ctx := c.Request.Context()

	userID, exists := c.Get("userId")
	if !exists || userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":   false,
			"error":     "AUTH_REQUIRED",
			"message":   "Not authenticated",
			"requestId": requestID,
		})
		return
	}

	sessionID, _ := c.Get("sessionId")
	cookieSessionID, cookieErr := c.Cookie(constants.SessionCookieName)

	sess, err := h.getSessionFromRedis(ctx, fmt.Sprintf("%v", sessionID))
	if (err != nil || sess == nil) && cookieErr == nil && cookieSessionID != "" {
		sess, err = h.getSessionFromRedis(ctx, cookieSessionID)
	}

	if err != nil || sess == nil {
		h.log.Warn("Session not found in Redis, using context data", map[string]interface{}{
			"userId":    userID,
			"sessionId": sessionID,
			"requestId": requestID,
		})
		c.JSON(http.StatusOK, UserProfileResponse{
			Success: true,
			Data: UserProfile{
				UserID:    fmt.Sprintf("%v", userID),
				SessionID: fmt.Sprintf("%v", sessionID),
				IsActive:  true,
			},
			RequestID: requestID,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	h.log.Info("User profile fetched", map[string]interface{}{
		"userId":    sess.UserID,
		"sessionId": sess.SessionID,
		"requestId": requestID,
	})

	c.JSON(http.StatusOK, UserProfileResponse{
		Success: true,
		Data: UserProfile{
			UserID:    sess.UserID,
			SessionID: sess.SessionID,
			IsActive:  true,
		},
		RequestID: requestID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *UserHandler) getSessionFromRedis(ctx context.Context, sessionID string) (*session.Session, error) {
	if sessionID == "" {
		return nil, nil
	}
	store := session.NewRedisStore(h.redisClient)
	sess, err := store.Get(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("redis fetch failed: %w", err)
	}
	return sess, nil
}

func (h *UserHandler) ValidateSession(c *gin.Context) {
	requestID := c.GetString("requestId")

	userID, exists := c.Get("userId")
	if !exists || userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":   false,
			"valid":     false,
			"error":     "AUTH_REQUIRED",
			"requestId": requestID,
		})
		return
	}

	sessionID, _ := c.Get("sessionId")

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"valid":     true,
		"userId":    userID,
		"sessionId": sessionID,
		"requestId": requestID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// suppress unused import warning during review
var _ = json.Marshal