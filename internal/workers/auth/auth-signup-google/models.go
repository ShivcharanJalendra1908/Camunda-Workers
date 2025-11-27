package authsignupgoogle

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

type GoogleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token,omitempty"`
}

type GoogleUserProfile struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	Name          string `json:"name"`
	GivenName     string `json:"given_name"`
	FamilyName    string `json:"family_name"`
	Picture       string `json:"picture"`
	Locale        string `json:"locale"`
	HD            string `json:"hd,omitempty"`
}

type ServiceDependencies struct {
	Keycloak *auth.KeycloakClient
	ZohoCRM  *zoho.CRMClient
	Logger   logger.Logger
}
