package sessionmanager

import (
	"camunda-workers/internal/common/validation"
	"time"
)

type Input struct {
	Action    string                 `json:"action"` // "create", "get", "delete"
	SessionID string                 `json:"sessionId,omitempty"`
	UserID         string                 `json:"userId,omitempty"`
	KeycloakUserID string                 `json:"keycloakUserId,omitempty"`
	IDToken        string                 `json:"idToken,omitempty"`
	Email          string                 `json:"email,omitempty"`
	ExpiresIn int                    `json:"expiresIn,omitempty"` // seconds
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

func (i *Input) Sanitize() {
	if i.Action != "" {
		i.Action = validation.SanitizeString(i.Action)
	}
	if i.SessionID != "" {
		i.SessionID = validation.SanitizeString(i.SessionID)
	}
	if i.UserID != "" {
		i.UserID = validation.SanitizeString(i.UserID)
	}
	if i.KeycloakUserID != "" {
		i.KeycloakUserID = validation.SanitizeString(i.KeycloakUserID)
	}
	if i.Email != "" {
		i.Email = validation.SanitizeString(i.Email)
	}
}

type Output struct {
	Success      bool      `json:"success"`
	SessionID      string    `json:"sessionId,omitempty"`
	UserID         string    `json:"userId,omitempty"`
	KeycloakUserID string    `json:"keycloakUserId,omitempty"`
	Email          string    `json:"email,omitempty"`
	IDToken        string    `json:"idToken,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	CookieHeader string    `json:"cookieHeader,omitempty"`
	Message      string    `json:"message,omitempty"`
}
