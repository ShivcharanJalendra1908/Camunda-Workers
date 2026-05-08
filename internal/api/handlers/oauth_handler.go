// internal/api/handlers/oauth_handler.go
package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	apierrors "camunda-workers/internal/api/errors"
	"camunda-workers/internal/common/auth/session"
	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/constants"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/models"

	"camunda-workers/internal/common/config"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type OAuthHandler struct {
	redisClient   *redis.Client
	log           logger.Logger
	sessionStore  *session.RedisStore
	db            *sql.DB
	camundaClient *camunda.Client
	cookieDomain  string
	config        *config.Config
}

func NewOAuthHandler(redisClient *redis.Client, log logger.Logger, db *sql.DB, camundaClient *camunda.Client, cookieDomain string, cfg *config.Config) *OAuthHandler {
	var sessionStore *session.RedisStore
	if redisClient != nil {
		sessionStore = session.NewRedisStore(redisClient)
	}

	if cookieDomain == "" {
		cookieDomain = ".lemici.com"
	}

	return &OAuthHandler{
		redisClient:   redisClient,
		log:           log,
		sessionStore:  sessionStore,
		db:            db,
		camundaClient: camundaClient,
		cookieDomain:  cookieDomain,
		config:        cfg,
	}
}

func (h *OAuthHandler) OAuthLogout(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetString("requestId")

	sessionID, err := c.Cookie(constants.SessionCookieName)
	if err != nil || sessionID == "" {
		h.log.Info("OAuthLogout: No session cookie found or empty", map[string]interface{}{
			"requestId": requestID,
		})
		h.clearSessionCookie(c)
		c.Redirect(http.StatusFound, "/")
		return
	}

	var userID, keycloakUserID, idToken, refreshToken string
	sess, err := h.sessionStore.Get(ctx, sessionID)
	if err == nil && sess != nil {
		userID = sess.UserID
		idToken = sess.IDToken
		refreshToken = sess.RefreshToken
		// Resolve Keycloak Internal ID from identities table
		if h.db != nil {
			_ = h.db.QueryRowContext(ctx,
				"SELECT provider_user_id FROM identities WHERE user_id = $1 AND provider = 'keycloak' LIMIT 1",
				userID).Scan(&keycloakUserID)
		}
	}

	// Step 1: Trigger Camunda process (LogoutWorkflow) for background cleanup
	if h.camundaClient != nil {

		// Direct Server-to-Server Logout using refresh_token (Bypasses Admin Credentials & CORS)
		if refreshToken != "" {
			go func(token string) {
				keycloakCfg := h.config.Auth.Keycloak
				logoutURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/logout",
					keycloakCfg.URL, keycloakCfg.Realm)

				data := url.Values{}
				data.Set("client_id", keycloakCfg.ClientID)
				data.Set("refresh_token", token)

				req, _ := http.NewRequest("POST", logoutURL, strings.NewReader(data.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				
				// Fire and forget server-side logout request
				_, _ = http.DefaultClient.Do(req)
			}(refreshToken)
		}

		variables := map[string]interface{}{
			"sessionId":      sessionID,
			"requestId":      requestID,
			"userId":         userID,
			"keycloakUserId": keycloakUserID,
			"idToken":        idToken,
		}

		// Use background context for fire-and-forget to ensure it's not cancelled by request completion
		go func(sessID, reqID string, vars map[string]interface{}) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// vars is already populated from the outer scope

			cmd, err := h.camundaClient.GetClient().NewCreateInstanceCommand().
				BPMNProcessId("LogoutWorkflow").
				LatestVersion().
				VariablesFromMap(vars)

			if err != nil {
				h.log.Error("OAuthLogout: Failed to prepare Camunda command", map[string]interface{}{
					"processId": "Logout",
					"sessionId": sessID,
					"requestId": reqID,
					"error":     err.Error(),
				})
				return
			}

			resp, err := cmd.Send(bgCtx)

			if err != nil {
				h.log.Error("OAuthLogout: Failed to start Camunda process", map[string]interface{}{
					"processId": "Logout",
					"sessionId": sessID,
					"requestId": reqID,
					"error":     err.Error(),
				})
			} else {
				h.log.Info("OAuthLogout: Camunda logout process started", map[string]interface{}{
					"instanceKey": resp.GetProcessInstanceKey(),
					"processId":   "Logout",
					"sessionId":   sessID,
					"requestId":   reqID,
				})
			}
		}(sessionID, requestID, variables)
	} else {
		h.log.Warn("OAuthLogout: Camunda client not initialized, performing direct Redis cleanup", map[string]interface{}{
			"requestId": requestID,
		})
		_ = h.sessionStore.Delete(ctx, sessionID)
	}

	// Clear cookie
	h.clearSessionCookie(c)

	// Professional Seamless Logout:
	// Since the backend now securely revokes the Keycloak session using the refresh_token,
	// we no longer need to force the browser to navigate to Keycloak.
	// We can safely return a 200 OK JSON response for the frontend's fetch call.
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Logged out successfully",
	})
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
			c.Error(&apierrors.AppError{
				Code:       "INTERNAL_ERROR",
				Message:    "Failed to log out from all sessions. Please try again.",
				StatusCode: http.StatusInternalServerError,
				LogMessage: "Failed to delete all sessions for user " + sess.UserID + ": " + err.Error(),
			})
			h.clearSessionCookie(c)
			c.Abort()
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
	// Domain read from config via constructor injection
	domain := h.cookieDomain

	// Clear the primary session cookie
	c.SetCookie(
		constants.SessionCookieName,
		"",
		-1,
		path,
		domain,
		h.config.Auth.Session.CookieSecure,
		h.config.Auth.Session.CookieHTTPOnly,
	)
	
	// Also clear the legacy session_id cookie just in case
	c.SetCookie(
		"session_id",
		"",
		-1,
		path,
		domain,
		h.config.Auth.Session.CookieSecure,
		h.config.Auth.Session.CookieHTTPOnly,
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
		SELECT id, email, name, profile_image, role
		FROM users
		WHERE id = $1
	`, userID).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
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

// getUserNameAndEmail fetches name and email fields for a user by ID
func (h *OAuthHandler) getUserNameAndEmail(ctx context.Context, userID string) (name, email string, err error) {
	if h.db == nil {
		return "", "", nil
	}

	var nameSQL, emailSQL sql.NullString
	err = h.db.QueryRowContext(ctx, `
		SELECT name, email 
		FROM users 
		WHERE id = $1`, userID).Scan(&nameSQL, &emailSQL)

	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", nil
		}
		return "", "", err
	}
	return nameSQL.String, emailSQL.String, nil
}

func (h *OAuthHandler) GetCurrentUser(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetString("requestId")

	sessionID, err := c.Cookie(constants.SessionCookieName)
	if err != nil || sessionID == "" {
		h.log.Warn("GetCurrentUser: No session cookie", map[string]interface{}{
			"requestId": requestID,
		})
		c.Error(apierrors.ErrUnauthenticated)
		c.Abort()
		return
	}

	sess, err := h.sessionStore.Get(ctx, sessionID)
	if err != nil {
		h.log.Error("GetCurrentUser: Error getting session", map[string]interface{}{
			"sessionId": sessionID,
			"error":     err.Error(),
			"requestId": requestID,
		})
		c.Error(apierrors.ErrUnauthenticated)
		c.Abort()
		return
	}

	if sess == nil || sess.UserID == "" {
		h.log.Warn("GetCurrentUser: Session not found", map[string]interface{}{
			"sessionId": sessionID,
			"requestId": requestID,
		})
		c.Error(apierrors.ErrUnauthenticated)
		c.Abort()
		return
	}

	userName, userEmail, err := h.getUserNameAndEmail(ctx, sess.UserID)
	if err != nil {
		h.log.Error("GetCurrentUser: Error fetching user from DB", map[string]interface{}{
			"userId":    sess.UserID,
			"error":     err.Error(),
			"requestId": requestID,
		})
		c.Error(&apierrors.AppError{
			Code:       "INTERNAL_ERROR",
			Message:    "An unexpected error occurred. Please try again.",
			StatusCode: http.StatusInternalServerError,
			LogMessage: "Failed to fetch user from DB: " + err.Error(),
		})
		c.Abort()
		return
	}

	if userName == "" {
		userName = "John Doe"
	}
	if userEmail == "" {
		userEmail = "johndoe@email.com"
	}

	// Debug logging for user data being sent to frontend
	h.log.Info("GetCurrentUser: Sending user data to frontend", map[string]interface{}{
		"userId":    sess.UserID,
		"name":      userName,
		"email":     userEmail,
		"requestId": requestID,
	})

	c.JSON(http.StatusOK, gin.H{
		"authenticated": true,
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
		"user": gin.H{
			"name":  userName,
			"email": userEmail,
		},
	})
}
