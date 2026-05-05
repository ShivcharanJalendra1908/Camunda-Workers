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
	UserID         string                 `json:"userId,omitempty"`
	KeycloakUserID string                 `json:"keycloakUserId,omitempty"`
	IDToken        string                 `json:"idToken,omitempty"`
	RefreshToken   string                 `json:"refreshToken,omitempty"`
	AccessToken    string                 `json:"accessToken,omitempty"`
	SessionID      string                 `json:"sessionId,omitempty"`
	RequestID      string                 `json:"requestId,omitempty"`
	DeviceID       string                 `json:"deviceId,omitempty"`
	LogoutAll      bool                   `json:"logoutAll,omitempty"`
	Reason         string                 `json:"reason,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
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
