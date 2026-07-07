package middleware

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"

	"camunda-workers/internal/common/config"
	"camunda-workers/internal/models"

	"github.com/gin-gonic/gin"
)

const (
	HeaderDeviceToken      = "X-Device-Token"
	HeaderDeviceSalt       = "X-Device-Salt"
	HeaderGuestSessionID   = "X-Guest-Session-ID"
)

// GuestSignalMiddleware extracts device identity headers and builds a compositeKey.
// The compositeKey is stored in gin.Context as models.CompositeKeyContext.
//
// compositeKey = SHA256(deviceToken + deviceSalt) if token present
//
//	fallback = SHA256(ip/24 + normalizedUA + deviceSalt)
//
// If neither token nor salt is provided, the request is blocked.
func GuestSignalMiddleware(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		deviceToken := strings.TrimSpace(c.GetHeader(HeaderDeviceToken))
		deviceSalt := strings.TrimSpace(c.GetHeader(HeaderDeviceSalt))

		// Salt is required
		if deviceSalt == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "MISSING_DEVICE_SALT",
				"message": "X-Device-Salt header is required.",
			})
			return
		}

		identity := &models.GuestIdentity{
			HasToken: deviceToken != "",
			HasSalt:  true,
		}

		var compositeKey string

		if deviceToken != "" {
			// Validate token: minimum 32-char hex
			if !isValidHexToken(deviceToken) {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
					"success": false,
					"error":   "INVALID_DEVICE_TOKEN",
					"message": "X-Device-Token must be a lowercase hex string (min 32 chars).",
				})
				return
			}
			compositeKey = buildCompositeKey(deviceToken, deviceSalt)
		} else {
			// Fallback: IP/24 + normalized UA + salt
			ip := c.ClientIP()
			ua := c.GetHeader("User-Agent")
			compositeKey = buildFallbackCompositeKey(ip, ua, deviceSalt)
			identity.FallbackIP = ipSlash24(ip)

			// S4: Token absence â€” log warning for anomaly tracking
			fmt.Printf("[GUEST_S4] token_absent ip=%s\n", ipSlash24(ip))
		}

		identity.CompositeKey = compositeKey

		c.Set(models.CompositeKeyContext, identity)
		c.Next()
	}
}

// buildCompositeKey creates SHA256(token + salt), truncated to 8 bytes (16 hex chars).
func buildCompositeKey(token, salt string) string {
	h := sha256.Sum256([]byte(token + ":" + salt))
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars
}

// buildFallbackCompositeKey creates SHA256(ip/24 + normalizedUA + salt), truncated to 8 bytes (16 hex chars).
func buildFallbackCompositeKey(ip, ua, salt string) string {
	ipPrefix := ipSlash24(ip)
	normalizedUA := normalizeUserAgent(ua)
	raw := ipPrefix + ":" + normalizedUA + ":" + salt
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars
}

// ipSlash24 returns the first three octets of an IPv4 address (/24 subnet).
// For IPv6 it returns the first three colon-separated groups.
func ipSlash24(ip string) string {
	ip = strings.TrimSpace(ip)

	// IPv4: "a.b.c.d" â†’ "a.b.c"
	if strings.Count(ip, ".") >= 2 {
		parts := strings.SplitN(ip, ".", 4)
		if len(parts) >= 3 {
			return parts[0] + "." + parts[1] + "." + parts[2]
		}
	}

	// IPv6: "2001:db8:85a3::1" â†’ "2001:db8:85a3"
	if strings.Count(ip, ":") >= 2 {
		parts := strings.SplitN(ip, ":", 4)
		if len(parts) >= 3 {
			return parts[0] + ":" + parts[1] + ":" + parts[2]
		}
	}

	return ip
}

// isValidHexToken checks if a string is a valid lowercase hex token (min 32 chars).
func isValidHexToken(token string) bool {
	if len(token) < 32 {
		return false
	}
	for _, c := range token {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
