package models

import "time"

// AuthProvider represents OAuth provider types
type AuthProvider string

const (
	ProviderGoogle   AuthProvider = "google"
	ProviderLinkedIn AuthProvider = "linkedin"
	ProviderEmail    AuthProvider = "email"
)

// AuthUser represents authenticated user information
type AuthUser struct {
	ID            string                 `json:"id" db:"id"`
	Email         string                 `json:"email" db:"email"`
	Name          string                 `json:"name" db:"name"`
	Provider      AuthProvider           `json:"provider" db:"provider"`
	ProviderID    string                 `json:"providerId" db:"provider_id"`
	EmailVerified bool                   `json:"emailVerified" db:"email_verified"`
	ProfileImage  string                 `json:"profileImage,omitempty" db:"profile_image"`
	Status        string                 `json:"status" db:"status"`
	CreatedAt     time.Time              `json:"createdAt" db:"created_at"`
	UpdatedAt     time.Time              `json:"updatedAt" db:"updated_at"`
	LastLogin     *time.Time             `json:"lastLogin,omitempty" db:"last_login"`
	Metadata      map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
}

// OAuthToken represents OAuth token information
type OAuthToken struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	TokenType    string    `json:"tokenType"`
	ExpiresIn    int64     `json:"expiresIn"`
	ExpiresAt    time.Time `json:"expiresAt"`
	Scope        string    `json:"scope,omitempty"`
}

// CaptchaResult represents reCAPTCHA verification result
type CaptchaResult struct {
	Success     bool     `json:"success"`
	Score       float64  `json:"score,omitempty"`
	Action      string   `json:"action,omitempty"`
	ChallengeTS string   `json:"challenge_ts"`
	Hostname    string   `json:"hostname"`
	ErrorCodes  []string `json:"error-codes,omitempty"`
}

// CRMContact represents a contact in CRM system
type CRMContact struct {
	ID         string                 `json:"id"`
	Email      string                 `json:"email"`
	FirstName  string                 `json:"firstName"`
	LastName   string                 `json:"lastName"`
	Phone      string                 `json:"phone,omitempty"`
	Company    string                 `json:"company,omitempty"`
	Status     string                 `json:"status"`
	Source     string                 `json:"source"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	CreatedAt  time.Time              `json:"createdAt"`
	UpdatedAt  time.Time              `json:"updatedAt"`
}

// EmailMessage represents an email to be sent
type EmailMessage struct {
	To          []string               `json:"to"`
	Cc          []string               `json:"cc,omitempty"`
	Bcc         []string               `json:"bcc,omitempty"`
	Subject     string                 `json:"subject"`
	Body        string                 `json:"body"`
	HTMLBody    string                 `json:"htmlBody,omitempty"`
	From        string                 `json:"from"`
	FromName    string                 `json:"fromName,omitempty"`
	ReplyTo     string                 `json:"replyTo,omitempty"`
	TemplateID  string                 `json:"templateId,omitempty"`
	Variables   map[string]interface{} `json:"variables,omitempty"`
	Attachments []EmailAttachment      `json:"attachments,omitempty"`
}

// EmailAttachment represents an email attachment
type EmailAttachment struct {
	Filename    string `json:"filename"`
	Content     []byte `json:"content"`
	ContentType string `json:"contentType"`
}

// AuthRepository defines user authentication data access
type AuthRepository interface {
	CreateUser(user *AuthUser) error
	FindByEmail(email string) (*AuthUser, error)
	FindByProviderID(provider AuthProvider, providerID string) (*AuthUser, error)
	FindByID(userID string) (*AuthUser, error)
	Update(user *AuthUser) error
	UpdateLastLogin(userID string) error
}
