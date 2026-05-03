// internal/api/handlers/user_handler.go
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"camunda-workers/internal/common/auth/session"
	"camunda-workers/internal/common/logger"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type UserHandler struct {
	redisClient *redis.Client
	db          *sql.DB
	log         logger.Logger
}

func NewUserHandler(redisClient *redis.Client, db *sql.DB, log logger.Logger) *UserHandler {
	return &UserHandler{
		redisClient: redisClient,
		db:          db,
		log:         log,
	}
}

type UserProfileResponse struct {
	Success   bool        `json:"success"`
	Data      UserProfile `json:"data"`
	Timestamp string      `json:"timestamp"`
}

type UserProfile struct {
	Name             string `json:"name"`
	Email            string `json:"email"`
	Phone            string `json:"phone"`
	SubscriptionTier string `json:"subscriptionTier"`
	IsActive         bool   `json:"isActive"`
}

func (h *UserHandler) GetProfile(c *gin.Context) {
	ctx := c.Request.Context()

	userID, exists := c.Get("userId")
	if !exists || userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "AUTH_REQUIRED",
			"message": "Not authenticated",
		})
		return
	}

	// 1. Fetch Fresh Data from Database
	var name, phone, email sql.NullString
	var status string
	err := h.db.QueryRowContext(ctx, `
		SELECT name, email, phone, status 
		FROM users 
		WHERE id = $1`, userID).Scan(&name, &email, &phone, &status)

	if err != nil {
		h.log.Error("Failed to fetch user profile from DB", map[string]interface{}{
			"userId": userID,
			"error":  err.Error(),
		})
		// Fallback for unexpected DB errors
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "INTERNAL_ERROR",
			"message": "Failed to retrieve profile",
		})
		return
	}

	// 2. Fetch Subscription Tier
	var subscriptionTier string
	_ = h.db.QueryRowContext(ctx, `
		SELECT tier FROM user_subscriptions 
		WHERE user_id = $1 AND is_valid = true 
		ORDER BY created_at DESC LIMIT 1`, userID).Scan(&subscriptionTier)
	if subscriptionTier == "" {
		subscriptionTier = "free"
	}

	c.JSON(http.StatusOK, UserProfileResponse{
		Success: true,
		Data: UserProfile{
			Name:             name.String,
			Email:            email.String,
			Phone:            phone.String,
			SubscriptionTier: subscriptionTier,
			IsActive:         status == "active",
		},
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
		return nil, err
	}
	return sess, nil
}

func (h *UserHandler) ValidateSession(c *gin.Context) {
	userID, exists := c.Get("userId")
	if !exists || userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"valid":   false,
			"error":   "AUTH_REQUIRED",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"valid":     true,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// suppress unused import warning during review
var _ = json.Marshal
