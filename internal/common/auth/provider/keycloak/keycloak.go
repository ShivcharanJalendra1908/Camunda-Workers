package keycloak

import (
	"context"
	"errors"
	"fmt"
	"strings"

	auth "camunda-workers/internal/common/auth/types"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const providerName = "keycloak"

// Provider implements OAuth + OIDC authentication against Keycloak.
type Provider struct {
	oauthConfig *oauth2.Config
	verifier    *oidc.IDTokenVerifier
}

// New initializes a Keycloak OIDC provider using discovery.
func New(
	ctx context.Context,
	issuer string,
	clientID string,
	redirectURL string,
	publicBaseURL string,
) (*Provider, error) {

	if issuer == "" || clientID == "" || redirectURL == "" || publicBaseURL == "" {
		return nil, errors.New("keycloak oauth config missing required fields")
	}

	oidcProvider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to init keycloak oidc provider: %w", err)
	}

	verifier := oidcProvider.Verifier(&oidc.Config{
		ClientID: clientID,
	})

	ep := oidcProvider.Endpoint()
	//ep.AuthURL = publicBaseURL + "/realms/auth-service/protocol/openid-connect/auth"
	// ✅ NAYA - SAHI
	ep.AuthURL = publicBaseURL + "/realms/" + issuer[strings.LastIndex(issuer, "/realms/")+8:] + "/protocol/openid-connect/auth"

	oauthCfg := &oauth2.Config{
		ClientID:    clientID,
		RedirectURL: redirectURL,
		Endpoint:    ep,
		Scopes: []string{
			oidc.ScopeOpenID,
			"email",
			"profile",
		},
	}

	return &Provider{
		oauthConfig: oauthCfg,
		verifier:    verifier,
	}, nil
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return providerName
}

// AuthCodeURL builds the OAuth authorization URL with PKCE parameters.
func (p *Provider) AuthCodeURL(state string, codeChallenge string) string {
	return p.oauthConfig.AuthCodeURL(
		state,
		oauth2.AccessTypeOnline,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

// ExchangeCode exchanges the authorization code and returns a normalized identity.
func (p *Provider) ExchangeCode(
	ctx context.Context,
	code string,
	codeVerifier string,
) (*auth.Identity, error) {

	token, err := p.oauthConfig.Exchange(
		ctx,
		code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return nil, fmt.Errorf("keycloak token exchange failed: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, errors.New("keycloak did not return id_token")
	}

	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("keycloak id_token verification failed: %w", err)
	}

	var claims struct {
		Subject           string `json:"sub"`
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		PreferredUsername string `json:"preferred_username"`
	}

	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("keycloak id_token claims parse failed: %w", err)
	}

	if claims.Subject == "" || claims.Email == "" {
		return nil, errors.New("keycloak id_token missing required claims")
	}

	return &auth.Identity{
		Provider:       providerName,
		ProviderUserID: claims.Subject,
		Email:          claims.Email,
		EmailVerified:  claims.EmailVerified,
	}, nil
}
