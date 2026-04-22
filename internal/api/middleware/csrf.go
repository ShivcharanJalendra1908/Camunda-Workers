package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func CSRFTokenIssuer(redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {

		method := c.Request.Method
		if method != "GET" && method != "HEAD" && method != "OPTIONS" {
			c.Next()
			return
		}

		// Get session_id from cookie
		sessionID, err := c.Cookie("session_id")
		if err != nil || sessionID == "" {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		key := "csrf:" + sessionID

		// Try to get existing CSRF token from Redis
		csrfToken, err := redisClient.Get(ctx, key).Result()
		if err != nil && err != redis.Nil {
			// Redis failure → don't break request
			c.Next()
			return
		}

		// If not found → generate and store
		if csrfToken == "" {
			csrfToken = uuid.New().String()
			// if err := redisClient.Set(ctx, key, csrfToken, time.Hour).Err(); err != nil {
			if err := redisClient.Set(ctx, key, csrfToken, 24*time.Hour).Err(); err != nil {
				c.Next()
				return
			}
		}

		// Attach token to response header
		c.Header("X-CSRF-Token", csrfToken)
		// c.SetSameSite(http.SameSiteStrictMode)
		// c.SetCookie(
		// 	"csrf_token",
		// 	csrfToken,
		// 	3600,
		// 	"/",
		// 	"",
		// 	false,
		// 	true,
		// )

		cookie := &http.Cookie{
			Name:     "csrf_token",
			Value:    csrfToken,
			Path:     "/",
			MaxAge:   86400,
			HttpOnly: false,
			Secure:   true,
			SameSite: http.SameSiteNoneMode,
		}
		c.Writer.Header().Add("Set-Cookie", cookie.String()+"; Partitioned")

		c.Next()
	}
}

func CSRFProtection(redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {

		method := c.Request.Method

		// Skip safe methods
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			c.Next()
			return
		}

		// 1. Get session_id
		sessionID, err := c.Cookie("session_id")
		if err != nil || sessionID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing session",
			})
			return
		}

		ctx := c.Request.Context()
		key := "csrf:" + sessionID

		// 2. Get expected token from Redis
		expectedToken, err := redisClient.Get(ctx, key).Result()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "csrf validation failed",
			})
			return
		}

		// 3. Get token from request header
		requestToken := c.GetHeader("X-CSRF-Token")
		if requestToken == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "missing csrf token",
			})
			return
		}

		// 4. Compare tokens
		if requestToken != expectedToken {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "invalid csrf token",
			})
			return
		}

		c.Next()
	}
}
