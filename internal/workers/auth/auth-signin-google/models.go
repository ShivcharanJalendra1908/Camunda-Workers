package authsigningoogle

import (
	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/zoho"
)

// Input represents the input variables for the worker
type Input struct {
	AuthCode    string                 `json:"authCode"`
	RedirectURI string                 `json:"redirectUri,omitempty"`
	State       string                 `json:"state,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// Output represents the output variables from the worker
type Output struct {
	Success      bool   `json:"success"`
	UserID       string `json:"userId"`
	Email        string `json:"email"`
	FirstName    string `json:"firstName"`
	LastName     string `json:"lastName"`
	Token        string `json:"token"`
	IsNewUser    bool   `json:"isNewUser,omitempty"`
	CRMContactID string `json:"crmContactId,omitempty"`
}

// GoogleTokenResponse represents the response from Google OAuth token endpoint
type GoogleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token,omitempty"`
}

// GoogleUserProfile represents the user profile from Google API
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

// ServiceDependencies contains all external dependencies for the service
type ServiceDependencies struct {
	Keycloak *auth.KeycloakClient
	ZohoCRM  *zoho.CRMClient
	Logger   logger.Logger
}
