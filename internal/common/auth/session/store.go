package session

import (
	"context"
	"time"
)

type Session struct {
	SessionID         string
	UserID            string
	CreatedAt         time.Time
	ExpiresAt         time.Time
	AbsoluteExpiresAt time.Time
	Version           int
	CSRFToken         string
	UserAgent         string `json:"user_agent"`
	IP                string `json:"ip"`
}

type Store interface {
	Create(ctx context.Context, s Session) error
	Get(ctx context.Context, sessionID string) (*Session, error)
	Update(ctx context.Context, s Session) error
	Delete(ctx context.Context, sessionID string) error
}
