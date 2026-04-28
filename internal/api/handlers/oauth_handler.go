// internal/api/handlers/oauth_handler.go
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

type OAuthHandler struct {
	redisClient  *redis.Client
	log          logger.Logger
	sessionStore *session.RedisStore
}

func NewOAuthHandler(redisClient *redis.Client, log logger.Logger) *OAuthHandler {
	var sessionStore *session.RedisStore
	if redisClient != nil {
		sessionStore = session.NewRedisStore(redisClient)
	}

	return &OAuthHandler{
		redisClient:  redisClient,
		log:          log,
		sessionStore: sessionStore,
	}
}

// OAuthLogout handles pure OAuth/OIDC logout for cookie-based session auth
// Flow: Read AUTH_SESSION_ID cookie → Delete session from Redis → Clear cookie → Redirect to homepage
// ser is redirected to homepage, not Keycloak login
func (h *OAuthHandler) OAuthLogout(c *gin.Context) {
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

// LogoutAll handles logout from all devices/sessions for a user.
// It deletes all sessions associated with the user's ID from Redis.
func (h *OAuthHandler) LogoutAll(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetString("requestId")

	// Get current session ID from cookie
	sessionID, err := c.Cookie(constants.SessionCookieName)
	if err != nil || sessionID == "" {
		h.log.Info("LogoutAll: No session cookie found", map[string]interface{}{
			"requestId": requestID,
		})
		h.clearSessionCookie(c)
		c.JSON(http.StatusOK, gin.H{"status": "logged_out_all"})
		return
	}

	// Get session to extract userID
	sess, err := h.sessionStore.Get(ctx, sessionID)
	if err != nil {
		h.log.Error("LogoutAll: Failed to get session", map[string]interface{}{
			"sessionId": sessionID,
			"error":     err.Error(),
			"requestId": requestID,
		})
		// Continue anyway - we'll try to clear what we can
		sess = nil
	}

	if sess != nil && sess.UserID != "" {
		// Delete ALL sessions for this user
		if err := h.sessionStore.DeleteAllForUser(ctx, sess.UserID); err != nil {
			h.log.Error("LogoutAll: Failed to delete all sessions", map[string]interface{}{
				"userId":    sess.UserID,
				"error":     err.Error(),
				"requestId": requestID,
			})
			h.clearSessionCookie(c)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed_to_logout_all_sessions",
			})
			return
		}

		h.log.Info("LogoutAll: Successfully deleted all sessions", map[string]interface{}{
			"userId":    sess.UserID,
			"requestId": requestID,
		})
	}

	// Clear current session cookie
	h.clearSessionCookie(c)
	c.JSON(http.StatusOK, gin.H{"status": "logged_out_all"})
}

func (h *OAuthHandler) clearSessionCookie(c *gin.Context) {
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

func (h *OAuthHandler) HealthCheck(c *gin.Context) {
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
