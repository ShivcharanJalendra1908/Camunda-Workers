package models

import (
	"time"
)

// Session represents a user session with access and refresh tokens
type Session struct {
	ID               string                 `json:"id" db:"id"`
	UserID           string                 `json:"userId" db:"user_id"`
	AccessToken      string                 `json:"accessToken" db:"access_token"`
	RefreshToken     string                 `json:"refreshToken" db:"refresh_token"`
	DeviceInfo       string                 `json:"deviceInfo,omitempty" db:"device_info"`
	IPAddress        string                 `json:"ipAddress,omitempty" db:"ip_address"`
	CreatedAt        time.Time              `json:"createdAt" db:"created_at"`
	ExpiresAt        time.Time              `json:"expiresAt" db:"expires_at"`
	RefreshExpiresAt time.Time              `json:"refreshExpiresAt" db:"refresh_expires_at"`
	LastActivity     time.Time              `json:"lastActivity" db:"last_activity"`
	IsActive         bool                   `json:"isActive" db:"is_active"`
	Metadata         map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
}

// IsExpired checks if access token has expired
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// IsRefreshTokenExpired checks if refresh token has expired
func (s *Session) IsRefreshTokenExpired() bool {
	return time.Now().After(s.RefreshExpiresAt)
}

// UpdateActivity updates the last activity timestamp
func (s *Session) UpdateActivity() {
	s.LastActivity = time.Now()
}

// IsValid checks if session is still valid (not expired and active)
func (s *Session) IsValid() bool {
	return s.IsActive && !s.IsExpired()
}

// CanRefresh checks if refresh token is still valid
func (s *Session) CanRefresh() bool {
	return s.IsActive && !s.IsRefreshTokenExpired()
}

// SessionRepository defines session data access interface
type SessionRepository interface {
	Create(session *Session) error
	FindByID(sessionID string) (*Session, error)
	FindByToken(token string) (*Session, error) // Now searches for access token
	FindByRefreshToken(refreshToken string) (*Session, error)
	FindByUserID(userID string) ([]*Session, error)
	Update(session *Session) error
	Delete(sessionID string) error
	DeleteByUserID(userID string) error
	InvalidateExpired() error
	InvalidateSession(sessionID string) error
}
