package authsignuplinkedin

import (
	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/zoho"
)

type Input struct {
	AuthCode    string                 `json:"authCode"`
	Email       string                 `json:"email"`
	RedirectURI string                 `json:"redirectUri,omitempty"`
	State       string                 `json:"state,omitempty"`
	FirstName   string                 `json:"firstName,omitempty"`
	LastName    string                 `json:"lastName,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type Output struct {
	Success       bool   `json:"success"`
	UserID        string `json:"userId"`
	Email         string `json:"email"`
	FirstName     string `json:"firstName"`
	LastName      string `json:"lastName"`
	Token         string `json:"token"`
	EmailVerified bool   `json:"emailVerified"`
	PasswordSet   bool   `json:"passwordSet"`
	CRMContactID  string `json:"crmContactId,omitempty"`
}

type LinkedInTokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

type LinkedInUserProfile struct {
	ID        string `json:"id"`
	FirstName string `json:"localizedFirstName"`
	LastName  string `json:"localizedLastName"`
	Email     string `json:"email"`
}

type LinkedInEmailResponse struct {
	Elements []struct {
		Handle struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"handle~"`
	} `json:"elements"`
}

type ServiceDependencies struct {
	Keycloak *auth.KeycloakClient
	ZohoCRM  *zoho.CRMClient
	Logger   logger.Logger
}
