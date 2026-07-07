package middleware

import (
	"net/http"

	"camunda-workers/internal/common/flagsmith"

	"github.com/gin-gonic/gin"
)

// RequireFlag checks if a feature flag is enabled globally (for anonymous routes)
// or for the logged-in user (if authenticated). If the flag is disabled,
// it responds with a 403 Forbidden status code.
func RequireFlag(flagName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := GetUserID(c)
		var isEnabled bool

		if userID != "" {
			// Retrieve user traits (e.g. email, roles, subscription Tier if they exist in Gin context)
			traits := make(map[string]interface{})
			if email, exists := c.Get("email"); exists {
				traits["email"] = email
			}
			if tier, exists := c.Get("subscriptionTier"); exists {
				traits["subscription_tier"] = tier
			}
			if roles, exists := c.Get("roles"); exists {
				traits["roles"] = roles
			}

			// Evaluate flag specifically for this user identity
			isEnabled = flagsmith.IsFeatureEnabledForUser(c.Request.Context(), userID, flagName, traits)
		} else {
			// Evaluate global environment-level flag
			isEnabled = flagsmith.IsFeatureEnabled(c.Request.Context(), flagName)
		}

		if !isEnabled {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "forbidden",
				"message": "Feature '" + flagName + "' is not enabled for your account or environment",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
