package keycloaksignin

import (
	"camunda-workers/internal/common/validation"
	"time"
)

type Input struct {
	Action   string                 `json:"action"`   // "initiate" or "callback"
	Code     string                 `json:"code"`     // Authorization code (for callback)
	State    string                 `json:"state"`    // State parameter (for callback)
	Provider string                 `json:"provider"` // Always "keycloak"
	Metadata map[string]interface{} `json:"metadata"`
}

func (i *Input) Sanitize() {
	if i.Action != "" {
		i.Action = validation.SanitizeString(i.Action)
	}
	if i.Code != "" {
		i.Code = validation.SanitizeString(i.Code)
	}
	if i.State != "" {
		i.State = validation.SanitizeString(i.State)
	}
}

type Output struct {
	// For "initiate" action
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
	State            string `json:"state,omitempty"`

	// For "callback" action
	Success         bool      `json:"success"`
	UserID          string    `json:"userId,omitempty"`
	Email           string    `json:"email,omitempty"`
	EmailVerified   bool      `json:"emailVerified,omitempty"`
	AccessToken     string    `json:"accessToken,omitempty"`
	RefreshToken    string    `json:"refreshToken,omitempty"`
	ExpiresIn       int       `json:"expiresIn,omitempty"`
	IsNewUser       bool      `json:"isNewUser,omitempty"`
	KeucloakUserID  string    `json:"keycloakUserId,omitempty"`
	AuthenticatedAt time.Time `json:"authenticatedAt,omitempty"`
}

type PKCEData struct {
	Verifier  string
	Challenge string
}
