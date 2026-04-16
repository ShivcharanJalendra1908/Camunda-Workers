package session

import (
	"context"
	"time"
)

type Session struct {
	SessionID         string    `json:"session_id"`
	UserID            string    `json:"user_id"`
	CreatedAt         time.Time `json:"created_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	AbsoluteExpiresAt time.Time `json:"absolute_expires_at"`
	Version           int       `json:"version"`
	CSRFToken         string    `json:"csrf_token"`
	KeycloakUserID    string    `json:"keycloak_user_id"`
	UserAgent         string    `json:"user_agent"`
	IP                string    `json:"ip"`
}

type Store interface {
	Create(ctx context.Context, s Session) error
	Get(ctx context.Context, sessionID string) (*Session, error)
	Update(ctx context.Context, s Session) error
	Delete(ctx context.Context, sessionID string) error
}
