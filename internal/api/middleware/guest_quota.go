package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"

	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/constants"
	"camunda-workers/internal/common/database"
	"camunda-workers/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	HeaderSessionID         = "X-Session-ID"
	HeaderQueriesUsed       = "X-Session-Queries-Used"
	HeaderQueriesRemaining  = "X-Session-Queries-Remaining"
	HeaderQueriesLimit      = "X-Session-Queries-Limit"
	HeaderCreditsUsed       = "X-Session-Credits-Used"
	HeaderCreditsRemaining  = "X-Session-Credits-Remaining"
	HeaderCreditsLimit      = "X-Session-Credits-Limit"
	HeaderIsNewSession      = "X-Session-Is-New"
	HeaderQuotaStatus       = "X-Quota-Status"

	sessionPrefix = "gsess_"
)

// GuestQuotaMiddleware enforces per-route-group credit rolling-window quota
// and queries-per-session limit.
//
// Flow:
//  1. Check if request has valid auth session cookie â†’ skip ALL guest quota (c.Next())
//  2. Extract guest identity from context (set by GuestSignalMiddleware)
//  3. Check existing session:
//     - If client sends X-Guest-Session-ID and it matches active session â†’ resume, incr queries
//     - If no session or mismatch â†’ new session, consume credit
//  4. Check credits: if creditsUsed > limit â†’ 429 SIGNUP_REQUIRED
//  5. Set response headers with quota info
func GuestQuotaMiddleware(redisClient *redis.Client, cfg *config.Config, routeGroup string) gin.HandlerFunc {
	groupCfg, exists := cfg.Guest.RouteGroups[routeGroup]
	if !exists {
		// Unknown route group â€” fail open (should not happen with valid config)
		return func(c *gin.Context) {
			fmt.Printf("[GUEST_QUOTA] unknown_route_group group=%s\n", routeGroup)
			c.Next()
		}
	}

	scripts := database.NewQuotaScripts()

	return func(c *gin.Context) {
		// â”€â”€ Step 1: Skip if user has valid auth session â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		if isAuthSessionValid(c.Request.Context(), c, redisClient) {
			c.Next()
			return
		}

		// â”€â”€ Step 2: Get guest identity â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		identity := getGuestIdentity(c)
		if identity == nil {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		key := identity.CompositeKey

		// â”€â”€ Step 3: Check for existing session â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		clientSessionID := c.GetHeader(HeaderGuestSessionID)

		if clientSessionID != "" {
			// Try to resume existing session
			sessionKey := fmt.Sprintf("guest:active_session:%s:%s", routeGroup, key)
			queryKey := fmt.Sprintf("session:%s:queries", clientSessionID)

			resumeResult, err := scripts.SessionResume(ctx, *redisClient, sessionKey, queryKey,
				clientSessionID, groupCfg.QueriesPerSession)

			if err == nil && resumeResult.Allowed {
				// Session resumed successfully
				creditsUsed, _ := getCreditCount(ctx, redisClient, routeGroup, key)
				setGuestResponseHeaders(c, clientSessionID, resumeResult.QueriesUsed,
					int64(groupCfg.QueriesPerSession), creditsUsed, int64(groupCfg.CreditsPerWindow), false)
				c.Next()
				return
			}

			if err == nil && !resumeResult.Allowed && resumeResult.QueriesUsed >= int64(groupCfg.QueriesPerSession) {
				// Queries exhausted â€” delete old session, fall through to new session + consume credit
				redisClient.Del(ctx, sessionKey)
			}

			// Session didn't match or queries exhausted â€” fall through to new session
		}

		// â”€â”€ Step 4: New session â€” consume a credit â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		quotaKey := fmt.Sprintf("quota:%s:%s", routeGroup, key)
		ttlSeconds := int64(groupCfg.CreditWindowDays) * 24 * 60 * 60
		quotaResult, err := scripts.QuotaIncr(ctx, *redisClient, quotaKey,
			groupCfg.CreditsPerWindow, ttlSeconds)

		if err != nil {
			// Redis failure â€” fail open
			fmt.Printf("[GUEST_QUOTA] redis_error group=%s key=%s error=%v\n", routeGroup, key, err)
			c.Next()
			return
		}

		if !quotaResult.Allowed {
			// Credits exhausted â€” read remaining TTL for accurate reset_in_seconds
			ttl, ttlErr := redisClient.TTL(ctx, quotaKey).Result()
			resetInSec := groupCfg.CreditWindowDays * 24 * 60 * 60
			if ttlErr == nil && ttl > 0 {
				resetInSec = int(ttl.Seconds())
			}
			errModel := models.NewQuotaExhaustedError(int(quotaResult.CreditsUsed), groupCfg.CreditsPerWindow, resetInSec)
			c.AbortWithStatusJSON(http.StatusTooManyRequests, errModel)
			return
		}

		creditsUsed := quotaResult.CreditsUsed

		// â”€â”€ Step 5: Create new session â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		newSessionID := generateSessionID()
		sessionKey := fmt.Sprintf("guest:active_session:%s:%s", routeGroup, key)
		queryKey := fmt.Sprintf("session:%s:queries", newSessionID)

		created, err := scripts.SessionCreate(ctx, *redisClient, sessionKey, queryKey, newSessionID,
			groupCfg.ActiveSessionTTL, groupCfg.SessionQueriesTTL)
		if err != nil {
			// Redis failure â€” fail open
			fmt.Printf("[GUEST_QUOTA] session_create_error group=%s key=%s error=%v\n", routeGroup, key, err)
			c.Next()
			return
		}

		if !created {
			// Lost race â€” another request created session first. Read and use that session.
			existingID, _ := redisClient.Get(ctx, sessionKey).Result()
			if existingID != "" {
				// Resume the winning session's query counter
				qVal, _ := redisClient.Incr(ctx, fmt.Sprintf("session:%s:queries", existingID)).Result()
				setGuestResponseHeaders(c, existingID, qVal, int64(groupCfg.QueriesPerSession),
					creditsUsed, int64(groupCfg.CreditsPerWindow), false)
				c.Next()
				return
			}
		}

		// â”€â”€ Step 6: Set response headers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		setGuestResponseHeaders(c, newSessionID, 1, int64(groupCfg.QueriesPerSession),
			creditsUsed, int64(groupCfg.CreditsPerWindow), true)

		c.Next()
	}
}

// â”€â”€ Helpers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// isAuthSessionValid checks if the request has a valid auth session (fast Redis GET).
func isAuthSessionValid(ctx context.Context, c *gin.Context, redisClient *redis.Client) bool {
	sessionID, err := c.Cookie(constants.SessionCookieName)
	if err != nil || sessionID == "" {
		return false
	}

	val, err := redisClient.Get(ctx, "session:"+sessionID).Result()
	if err != nil || val == "" {
		return false
	}

	return true
}

// getCreditCount returns the number of credits used in the current window for a route group.
func getCreditCount(ctx context.Context, client *redis.Client, routeGroup, compositeKey string) (int64, error) {
	key := fmt.Sprintf("quota:%s:%s", routeGroup, compositeKey)
	val, err := client.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return val, err
}

// setGuestResponseHeaders sets all X-Session-* headers on the response.
func setGuestResponseHeaders(c *gin.Context, sessionID string, queriesUsed, queriesLimit, creditsUsed, creditsLimit int64, isNew bool) {
	c.Header(HeaderSessionID, sessionID)
	c.Header(HeaderQueriesUsed, strconv.FormatInt(queriesUsed, 10))
	c.Header(HeaderQueriesRemaining, strconv.FormatInt(queriesLimit-queriesUsed, 10))
	c.Header(HeaderQueriesLimit, strconv.FormatInt(queriesLimit, 10))
	c.Header(HeaderCreditsUsed, strconv.FormatInt(creditsUsed, 10))
	c.Header(HeaderCreditsRemaining, strconv.FormatInt(creditsLimit-creditsUsed, 10))
	c.Header(HeaderCreditsLimit, strconv.FormatInt(creditsLimit, 10))

	if isNew {
		c.Header(HeaderIsNewSession, "true")
	} else {
		c.Header(HeaderIsNewSession, "false")
	}

	// Quota status for frontend
	remaining := creditsLimit - creditsUsed
	if remaining <= 0 {
		c.Header(HeaderQuotaStatus, "exhausted")
	} else if remaining == 1 {
		c.Header(HeaderQuotaStatus, "last_credit")
	} else {
		c.Header(HeaderQuotaStatus, "ok")
	}
}

// generateSessionID creates a session ID with gsess_ prefix + 12 random bytes (24 hex chars) = 30 chars total.
func generateSessionID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return fmt.Sprintf("%s%s", sessionPrefix, hex.EncodeToString(b))
}
