package middleware

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	// anonInquiryLimit is the maximum number of inquiries allowed for non-logged-in users.
	anonInquiryLimit = 3

	// anonInquiryWindowTTL is the sliding window duration for the inquiry counter.
	// Counter resets after this period from the first inquiry.
	anonInquiryWindowTTL = 24 * time.Hour

	// anonInquiryKeyPrefix is the Redis key prefix for anonymous inquiry counters.
	anonInquiryKeyPrefix = "anon:inquiry:"

	// anonInquiryCountHeader is set on the response so the frontend can show
	// "X out of 3 free inquiries used".
	anonInquiryCountHeader = "X-Inquiry-Count"

	// anonInquiryLimitHeader tells the frontend the hard limit.
	anonInquiryLimitHeader = "X-Inquiry-Limit"
)

// AnonymousInquiryLimiter limits non-logged-in users to `limit` inquiries per
// 24-hour window. Fingerprint is based on IP subnet (/16) + normalized
// User-Agent so cookie deletion does not bypass the limit.
//
// Logged-in users (valid session_id cookie in Redis) are always allowed through.
//
// Apply this middleware only on routes that constitute an "inquiry", e.g.:
//
//	franchiseGroup.Use(middleware.AnonymousInquiryLimiter(redisClient, 3))
func AnonymousInquiryLimiter(redisClient *redis.Client, limit int) gin.HandlerFunc {
	if limit <= 0 {
		limit = anonInquiryLimit
	}

	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// ── Step 1: Skip if user is logged in ────────────────────────────────
		if isSessionValid(ctx, c, redisClient) {
			c.Next()
			return
		}

		// ── Step 2: Build fingerprint ─────────────────────────────────────────
		fp := buildFingerprint(c.ClientIP(), c.GetHeader("User-Agent"))
		key := anonInquiryKeyPrefix + fp

		// ── Step 3: Atomic increment via Lua (avoids TOCTOU race) ────────────
		// Returns the count AFTER the increment.
		script := redis.NewScript(`
			local count = redis.call('INCR', KEYS[1])
			if count == 1 then
				-- First inquiry: set TTL for the window
				redis.call('EXPIRE', KEYS[1], ARGV[1])
			end
			return count
		`)

		windowSecs := int(anonInquiryWindowTTL.Seconds())
		countAfterRaw, err := script.Run(ctx, redisClient, []string{key}, windowSecs).Int64()
		if err != nil {
			// Redis failure — fail open (don't block user on infra error)
			fmt.Printf("[ANON_LIMIT] redis_error fingerprint=%s error=%v\n", fp, err)
			c.Next()
			return
		}

		// ── Step 4: Get remaining TTL to surface to frontend ─────────────────
		ttlDur, _ := redisClient.TTL(ctx, key).Result()

		// ── Step 5: Always set informational headers ──────────────────────────
		c.Header(anonInquiryCountHeader, fmt.Sprintf("%d", countAfterRaw))
		c.Header(anonInquiryLimitHeader, fmt.Sprintf("%d", limit))

		// ── Step 6: Block if over limit ───────────────────────────────────────
		if countAfterRaw > int64(limit) {
			fmt.Printf("[ANON_LIMIT] blocked fingerprint=%s count=%d ttl=%s\n",
				fp, countAfterRaw, ttlDur.Round(time.Minute))

			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error":   "INQUIRY_LIMIT_REACHED",
				"message": "You have used all 3 free enquiries. Please sign in to continue.",
				"details": gin.H{
					"inquiriesUsed":   countAfterRaw,
					"inquiryLimit":    limit,
					"resetsInMinutes": int(ttlDur.Minutes()),
				},
			})
			return
		}

		fmt.Printf("[ANON_LIMIT] allowed fingerprint=%s count=%d/%d\n",
			fp, countAfterRaw, limit)

		c.Next()
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// isSessionValid checks if the request carries a valid, non-expired session
// by doing a fast Redis existence check (no TTL update, no risk scoring).
// If Redis is down it returns false (deny session bypass, not block user).
func isSessionValid(ctx context.Context, c *gin.Context, redisClient *redis.Client) bool {
	sessionID, err := c.Cookie("session_id")
	if err != nil || sessionID == "" {
		return false
	}

	// Quick existence check — do NOT do full session parsing here.
	// SessionOrJWTAuth (on protected routes) does the full validation.
	exists, err := redisClient.Exists(ctx, "session:"+sessionID).Result()
	if err != nil {
		return false
	}
	return exists > 0
}

// buildFingerprint creates a stable, anonymised identifier for a user that
// persists across cookie deletions. Uses /16 IP subnet + normalized UA.
//
// Examples:
//
//	192.168.10.5  + "Mozilla/5.0 Chrome/120"  → sha256("192.168:chrome")[:16]
//	192.168.99.1  + "Mozilla/5.0 Chrome/119"  → sha256("192.168:chrome")[:16]  ← same bucket
//	10.0.0.1      + "Mozilla/5.0 Firefox/120" → sha256("10.0:firefox")[:16]
func buildFingerprint(ip, userAgent string) string {
	ipPrefix := ipSlash16(ip)
	ua := normalizeUserAgent(userAgent) // reuses existing function from jwt.go
	raw := ipPrefix + ":" + ua
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum[:8]) // 16 hex chars, 64-bit entropy
}

// ipSlash16 returns the first two octets of an IPv4 address ("/16 subnet").
// For IPv6 it returns the first two colon-separated groups.
// Falls back to the full IP for single-component inputs.
func ipSlash16(ip string) string {
	ip = strings.TrimSpace(ip)

	// IPv4: "a.b.c.d" → "a.b"
	if strings.Count(ip, ".") >= 1 {
		parts := strings.SplitN(ip, ".", 3)
		if len(parts) >= 2 {
			return parts[0] + "." + parts[1]
		}
	}

	// IPv6: "2001:db8::1" → "2001:db8"
	if strings.Count(ip, ":") >= 1 {
		parts := strings.SplitN(ip, ":", 3)
		if len(parts) >= 2 {
			return parts[0] + ":" + parts[1]
		}
	}

	return ip
}
