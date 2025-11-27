package models

import "time"

// Session represents a user session
type Session struct {
	ID           string                 `json:"id" db:"id"`
	UserID       string                 `json:"userId" db:"user_id"`
	Token        string                 `json:"token" db:"token"`
	DeviceInfo   string                 `json:"deviceInfo,omitempty" db:"device_info"`
	IPAddress    string                 `json:"ipAddress,omitempty" db:"ip_address"`
	CreatedAt    time.Time              `json:"createdAt" db:"created_at"`
	ExpiresAt    time.Time              `json:"expiresAt" db:"expires_at"`
	LastActivity time.Time              `json:"lastActivity" db:"last_activity"`
	IsActive     bool                   `json:"isActive" db:"is_active"`
	Metadata     map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
}

// IsExpired checks if session has expired
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// UpdateActivity updates the last activity timestamp
func (s *Session) UpdateActivity() {
	s.LastActivity = time.Now()
}

// SessionRepository defines session data access interface
type SessionRepository interface {
	Create(session *Session) error
	FindByToken(token string) (*Session, error)
	FindByUserID(userID string) ([]*Session, error)
	Update(session *Session) error
	Delete(sessionID string) error
	DeleteByUserID(userID string) error
	InvalidateExpired() error
}
