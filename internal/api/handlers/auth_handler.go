// internal/api/handlers/auth_handler.go
package handlers

import (
	"context"
	"net/http"
	"time"

	"camunda-workers/internal/common/auth/session"
	"camunda-workers/internal/common/constants"
	"camunda-workers/internal/common/logger"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type AuthHandler struct {
	redisClient  *redis.Client
	log          logger.Logger
	sessionStore *session.RedisStore
}

func NewAuthHandler(redisClient *redis.Client, log logger.Logger) *AuthHandler {
	var sessionStore *session.RedisStore
	if redisClient != nil {
		sessionStore = session.NewRedisStore(redisClient)
	}

	return &AuthHandler{
		redisClient:  redisClient,
		log:          log,
		sessionStore: sessionStore,
	}
}

// OAuthLogout handles pure OAuth/OIDC logout for cookie-based session auth
// Flow: Read AUTH_SESSION_ID cookie → Delete session from Redis → Clear cookie → Redirect to homepage
// ser is redirected to homepage, not Keycloak login
func (h *AuthHandler) OAuthLogout(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetString("requestId")

	sessionID, err := c.Cookie(constants.SessionCookieName)
	if err != nil {
		h.log.Info("OAuthLogout: No session cookie found", map[string]interface{}{
			"requestId": requestID,
		})
		h.clearSessionCookie(c)
		c.Redirect(http.StatusFound, "/")
		return
	}

	if sessionID == "" {
		h.log.Info("OAuthLogout: Empty session cookie", map[string]interface{}{
			"requestId": requestID,
		})
		h.clearSessionCookie(c)
		c.Redirect(http.StatusFound, "/")
		return
	}

	sess, err := h.sessionStore.Get(ctx, sessionID)
	if err != nil {
		h.log.Error("OAuthLogout: Error checking session in Redis", map[string]interface{}{
			"sessionId": sessionID,
			"error":     err.Error(),
			"requestId": requestID,
		})
		h.clearSessionCookie(c)
		c.Redirect(http.StatusFound, "/")
		return
	}

	if sess == nil {
		h.log.Info("OAuthLogout: Session not found in Redis", map[string]interface{}{
			"sessionId": sessionID,
			"requestId": requestID,
		})
		h.clearSessionCookie(c)
		c.Redirect(http.StatusFound, "/")
		return
	}

	if err := h.sessionStore.Delete(ctx, sessionID); err != nil {
		h.log.Error("OAuthLogout: Failed to delete session", map[string]interface{}{
			"sessionId": sessionID,
			"userId":    sess.UserID,
			"error":     err.Error(),
			"requestId": requestID,
		})
		h.clearSessionCookie(c)
		c.Redirect(http.StatusFound, "/")
		return
	}

	h.log.Info("OAuthLogout: Session deleted successfully", map[string]interface{}{
		"sessionId": sessionID,
		"userId":    sess.UserID,
		"requestId": requestID,
	})

	h.clearSessionCookie(c)
	// Redirect to homepage after logout, not Keycloak login
	c.Redirect(http.StatusFound, "/")
}

func (h *AuthHandler) clearSessionCookie(c *gin.Context) {
	c.SetCookie(
		constants.SessionCookieName,
		"",
		-1,
		constants.SessionCookiePath,
		"",
		false,
		true,
	)
}

func (h *AuthHandler) HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := h.redisClient.Ping(ctx).Err(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unhealthy",
			"redis":  "disconnected",
			"error":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"redis":  "connected",
	})
}
