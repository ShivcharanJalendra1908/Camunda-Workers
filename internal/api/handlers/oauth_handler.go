// internal/api/handlers/oauth_handler.go
package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"camunda-workers/internal/common/auth/session"
	"camunda-workers/internal/common/constants"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type OAuthHandler struct {
	redisClient  *redis.Client
	log          logger.Logger
	sessionStore *session.RedisStore
	db           *sql.DB
}

func NewOAuthHandler(redisClient *redis.Client, log logger.Logger, db *sql.DB) *OAuthHandler {
	var sessionStore *session.RedisStore
	if redisClient != nil {
		sessionStore = session.NewRedisStore(redisClient)
	}

	return &OAuthHandler{
		redisClient:  redisClient,
		log:          log,
		sessionStore: sessionStore,
		db:           db,
	}
}

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

func (h *OAuthHandler) getUserByID(ctx context.Context, userID string) (*models.User, error) {
	if h.db == nil {
		return nil, nil
	}

	var user models.User
	err := h.db.QueryRowContext(ctx, `
		SELECT id, email, name, first_name, last_name, profile_image, role
		FROM users
		WHERE id = $1
	`, userID).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.FirstName,
		&user.LastName,
		&user.ProfileImage,
		&user.Role,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// getUserFirstAndLastNameAndEmail fetches first_name, last_name, and email fields for a user by ID
func (h *OAuthHandler) getUserFirstAndLastNameAndEmail(ctx context.Context, userID string) (firstName, lastName, email string, err error) {
	if h.db == nil {
		return "", "", "", nil
	}

	var firstNameSQL, lastNameSQL, emailSQL sql.NullString
	err = h.db.QueryRowContext(ctx, `
		SELECT first_name, last_name, email 
		FROM users 
		WHERE id = $1`, userID).Scan(&firstNameSQL, &lastNameSQL, &emailSQL)

	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", "", nil
		}
		return "", "", "", err
	}
	return firstNameSQL.String, lastNameSQL.String, emailSQL.String, nil
}

func (h *OAuthHandler) GetCurrentUser(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetString("requestId")

	sessionID, err := c.Cookie(constants.SessionCookieName)
	if err != nil || sessionID == "" {
		h.log.Warn("GetCurrentUser: No session cookie", map[string]interface{}{
			"requestId": requestID,
		})
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthenticated",
			"code":  "AUTH_REQUIRED",
		})
		return
	}

	sess, err := h.sessionStore.Get(ctx, sessionID)
	if err != nil {
		h.log.Error("GetCurrentUser: Error getting session", map[string]interface{}{
			"sessionId": sessionID,
			"error":     err.Error(),
			"requestId": requestID,
		})
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthenticated",
			"code":  "AUTH_REQUIRED",
		})
		return
	}

	if sess == nil || sess.UserID == "" {
		h.log.Warn("GetCurrentUser: Session not found", map[string]interface{}{
			"sessionId": sessionID,
			"requestId": requestID,
		})
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthenticated",
			"code":  "AUTH_REQUIRED",
		})
		return
	}

	firstName, lastName, userEmail, err := h.getUserFirstAndLastNameAndEmail(ctx, sess.UserID)
	if err != nil {
		h.log.Error("GetCurrentUser: Error fetching user from DB", map[string]interface{}{
			"userId":    sess.UserID,
			"error":     err.Error(),
			"requestId": requestID,
		})
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error",
		})
		return
	}

	// Debug logging for first name, last name and email being sent to frontend
	h.log.Info("GetCurrentUser: Sending user data to frontend", map[string]interface{}{
		"userId":    sess.UserID,
		"firstName": firstName,
		"lastName":  lastName,
		"email":     userEmail,
		"requestId": requestID,
	})

	// Return ONLY first name, last name and email to frontend (no fallbacks)
	c.JSON(http.StatusOK, gin.H{
		"authenticated": true,
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
		"user": gin.H{
			"firstName": firstName, // Will be empty string if not set in DB
			"lastName":  lastName,  // Will be empty string if not set in DB
			"email":     userEmail, // Will be empty string if not set in DB
		},
	})
}
