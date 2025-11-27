package authlogout

import (
	"camunda-workers/internal/common/logger"
	"time"
)

type Input struct {
	UserID    string                 `json:"userId"`
	Token     string                 `json:"token"`
	SessionID string                 `json:"sessionId,omitempty"`
	DeviceID  string                 `json:"deviceId,omitempty"`
	LogoutAll bool                   `json:"logoutAll,omitempty"`
	Reason    string                 `json:"reason,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

type Output struct {
	Success             bool      `json:"success"`
	Message             string    `json:"message"`
	SessionsInvalidated int       `json:"sessionsInvalidated,omitempty"`
	TokenRevoked        bool      `json:"tokenRevoked,omitempty"`
	LogoutAt            time.Time `json:"logoutAt,omitempty"`
}

type ServiceDependencies struct {
	Logger logger.Logger
}
