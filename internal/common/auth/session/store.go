package session

import (
	"context"
	"time"
)

// Session represents an authenticated user session.
// It intentionally stores only identity pointers, not auth state.
type Session struct {
	SessionID string    // unique session identifier
	UserID    string    // references users.id
	ExpiresAt time.Time // absolute expiry time
}

// Store defines how sessions are stored and retrieved.
// Implementations (e.g., Redis) must remain stateless and opaque.
type Store interface {
	Create(ctx context.Context, s Session) error
	Get(ctx context.Context, sessionID string) (*Session, error)
	Update(ctx context.Context, s Session) error
	Delete(ctx context.Context, sessionID string) error
}
