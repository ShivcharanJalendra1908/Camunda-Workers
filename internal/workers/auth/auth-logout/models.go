package authlogout

import (
	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/logger"
	"database/sql"
	"time"

	"github.com/redis/go-redis/v9"
)

// Input represents the input variables for the logout worker
type Input struct {
	UserID         string                 `json:"userId"` // Keycloak user ID (required for global logout)
	KeycloakUserID string                 `json:"keycloakUserId,omitempty"`
	IDToken        string                 `json:"idToken,omitempty"`           // ID token for Keycloak id_token_hint (Keycloak 17+ logout)
	RefreshToken   string                 `json:"refreshToken"`               // Keycloak refresh token (required for single session logout)
	AccessToken    string                 `json:"accessToken"`                // Keycloak access token (optional, for revocation list)
	SessionID      string                 `json:"sessionId,omitempty"`        // Local session ID (optional)
	DeviceID       string                 `json:"deviceId,omitempty"`         // Device identifier (optional)
	LogoutAll      bool                   `json:"logoutAll,omitempty"`        // Global logout flag
	Reason         string                 `json:"reason,omitempty"`           // Logout reason for audit
	Metadata       map[string]interface{} `json:"metadata,omitempty"`         // Additional metadata
}

// Output represents the output variables after logout
type Output struct {
	Success             bool      `json:"success"`                       // Whether logout was successful
	Message             string    `json:"message"`                       // Result message
	SessionsInvalidated int       `json:"sessionsInvalidated,omitempty"` // Number of sessions invalidated
	TokenRevoked        bool      `json:"tokenRevoked,omitempty"`        // Whether Keycloak tokens were revoked
	LogoutAt            time.Time `json:"logoutAt"`                      // Timestamp of logout
	LogoutURL           string    `json:"logoutUrl"`                     // Keycloak browser logout URL (frontend must redirect here to clear SSO cookie)
}

// ServiceDependencies contains all external dependencies for the service
type ServiceDependencies struct {
	Keycloak    *auth.KeycloakClient // Keycloak client for token revocation
	Logger      logger.Logger
	RedisClient *redis.Client
	DB          *sql.DB
}
