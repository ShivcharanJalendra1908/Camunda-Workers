// internal/common/auth/keycloak.go
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"camunda-workers/internal/common/circuitbreaker"
	"camunda-workers/internal/common/errors"
)

// ============================================================================
// KEYCLOAK CLIENT
// ============================================================================

// KeycloakClient provides thread-safe methods to interact with Keycloak for
// user management and authentication.
type KeycloakClient struct {
	baseURL           string
	realm             string
	clientID          string
	clientSecret      string
	adminClientID     string // ← ADD
	adminClientSecret string // ← ADD
	httpClient        *http.Client
	cb                *circuitbreaker.CircuitBreaker
	mu                sync.RWMutex
	accessToken       string
	tokenExpiry       time.Time
}

// ============================================================================
// DATA MODELS
// ============================================================================

// User represents a user in Keycloak with all relevant attributes.
type User struct {
	ID               string              `json:"id,omitempty"`
	Email            string              `json:"email"`
	FirstName        string              `json:"firstName,omitempty"`
	LastName         string              `json:"lastName,omitempty"`
	Username         string              `json:"username"`
	Enabled          bool                `json:"enabled"`
	EmailVerified    bool                `json:"emailVerified"`
	Attributes       map[string][]string `json:"attributes,omitempty"`
	CreatedTimestamp int64               `json:"createdTimestamp,omitempty"`
	RequiredActions  []string            `json:"requiredActions,omitempty"`
	Groups           []string            `json:"groups,omitempty"`
	RealmRoles       []string            `json:"realmRoles,omitempty"`
}

// TokenResponse holds the response from Keycloak's token endpoint.
type TokenResponse struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
	TokenType        string `json:"token_type"`
	RefreshToken     string `json:"refresh_token"`
	Scope            string `json:"scope"`
	SessionState     string `json:"session_state,omitempty"`
	NotBeforePolicy  int    `json:"not-before-policy,omitempty"`
}

// TokenInfo holds the information returned by the token introspection endpoint.
type TokenInfo struct {
	Active            bool     `json:"active"`
	Scope             string   `json:"scope,omitempty"`
	ClientID          string   `json:"client_id,omitempty"`
	Username          string   `json:"username,omitempty"`
	TokenType         string   `json:"token_type,omitempty"`
	Exp               int64    `json:"exp,omitempty"`
	Iat               int64    `json:"iat,omitempty"`
	Nbf               int64    `json:"nbf,omitempty"`
	Sub               string   `json:"sub,omitempty"`
	Aud               []string `json:"aud,omitempty"`
	Iss               string   `json:"iss,omitempty"`
	Jti               string   `json:"jti,omitempty"`
	Email             string   `json:"email,omitempty"`
	EmailVerified     bool     `json:"email_verified,omitempty"`
	Name              string   `json:"name,omitempty"`
	GivenName         string   `json:"given_name,omitempty"`
	FamilyName        string   `json:"family_name,omitempty"`
	PreferredUsername string   `json:"preferred_username,omitempty"`
}

// Credential represents a Keycloak user credential (e.g., password).
type Credential struct {
	Type      string `json:"type"`
	Value     string `json:"value"`
	Temporary bool   `json:"temporary"`
}

// UserSession represents an active user session.
type UserSession struct {
	ID         string            `json:"id"`
	Username   string            `json:"username"`
	UserID     string            `json:"userId"`
	IPAddress  string            `json:"ipAddress"`
	Start      int64             `json:"start"`
	LastAccess int64             `json:"lastAccess"`
	Clients    map[string]string `json:"clients,omitempty"`
}

// ============================================================================
// CONSTRUCTOR
// ============================================================================

// NewKeycloakClient creates a new instance of KeycloakClient with sensible defaults.
func NewKeycloakClient(baseURL, realm, clientID, clientSecret, adminClientID, adminClientSecret string) *KeycloakClient {
	return &KeycloakClient{
		baseURL:           strings.TrimSuffix(baseURL, "/"),
		realm:             realm,
		clientID:          clientID,
		clientSecret:      clientSecret,
		adminClientID:     adminClientID,     // ← ADD
		adminClientSecret: adminClientSecret, // ← ADD
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		cb: circuitbreaker.New(circuitbreaker.Config{
			Name:             "keycloak-auth",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
		}),
	}
}

func (k *KeycloakClient) doRequest(req *http.Request) (*http.Response, error) {
	result, err := k.cb.Execute(func() (interface{}, error) {
		resp, err := k.httpClient.Do(req)
		if err != nil {
			return nil, err
		}

		// Treat 5xx errors as circuit breaker failures
		if resp.StatusCode >= 500 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("keycloak server error %d: %s", resp.StatusCode, string(body))
		}

		return resp, nil
	})

	if err != nil {
		if err == circuitbreaker.ErrCircuitOpen {
			return nil, &errors.StandardError{
				Code:      "KEYCLOAK_UNAVAILABLE",
				Message:   "Keycloak service is temporarily unavailable",
				Details:   err.Error(),
				Retryable: true,
				Timestamp: time.Now(),
			}
		}
		return nil, err
	}

	return result.(*http.Response), nil
}

// ============================================================================
// AUTHENTICATION METHODS
// ============================================================================

// getAccessToken fetches a new access token using the client credentials flow.
// It caches the token until expiry with a 60-second buffer for safety.
// This method is thread-safe using double-checked locking pattern.
func (k *KeycloakClient) getAccessToken(ctx context.Context) error {
	// Quick check without lock (read lock)
	k.mu.RLock()
	if time.Now().Add(60*time.Second).Before(k.tokenExpiry) && k.accessToken != "" {
		k.mu.RUnlock()
		return nil
	}
	k.mu.RUnlock()

	// Acquire write lock for token refresh
	k.mu.Lock()
	defer k.mu.Unlock()

	// Double-check after acquiring lock (another goroutine might have refreshed)
	if time.Now().Add(60*time.Second).Before(k.tokenExpiry) && k.accessToken != "" {
		return nil
	}

	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", k.baseURL, k.realm)

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", k.clientID)
	data.Set("client_secret", k.clientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.doRequest(req)
	if err != nil {
		return fmt.Errorf("failed to execute token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("keycloak token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("failed to decode token response: %w", err)
	}

	k.accessToken = tokenResp.AccessToken
	k.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return nil
}

// ExchangeCodeForToken exchanges an authorization code for tokens (OAuth2 Authorization Code Flow).
func (k *KeycloakClient) ExchangeCodeForToken(ctx context.Context, code, redirectURI string) (*TokenResponse, error) {
	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", k.baseURL, k.realm)

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("client_id", k.clientID)
	data.Set("client_secret", k.clientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create token exchange request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, &errors.StandardError{
			Code:      "OAUTH_TOKEN_EXCHANGE_FAILED",
			Message:   "Failed to exchange authorization code for tokens",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, &errors.StandardError{
			Code:      "DESERIALIZATION_ERROR",
			Message:   "Failed to decode token response",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	return &tokenResp, nil
}

// RefreshAccessToken refreshes an access token using a refresh token.
func (k *KeycloakClient) RefreshAccessToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", k.baseURL, k.realm)

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", k.clientID)
	data.Set("client_secret", k.clientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create token refresh request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, &errors.StandardError{
			Code:      "TOKEN_REFRESH_FAILED",
			Message:   "Failed to refresh access token",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, &errors.StandardError{
			Code:      "DESERIALIZATION_ERROR",
			Message:   "Failed to decode token response",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	return &tokenResp, nil
}

// ValidateToken checks if an access token is valid and active using Keycloak's introspection endpoint.
func (k *KeycloakClient) ValidateToken(ctx context.Context, token string) (*TokenInfo, error) {
	introspectURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token/introspect", k.baseURL, k.realm)

	data := url.Values{}
	data.Set("token", token)
	data.Set("token_type_hint", "access_token")
	data.Set("client_id", k.clientID)
	data.Set("client_secret", k.clientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", introspectURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create introspection request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tokenInfo TokenInfo
	if err := json.NewDecoder(resp.Body).Decode(&tokenInfo); err != nil {
		return nil, &errors.StandardError{
			Code:      "DESERIALIZATION_ERROR",
			Message:   "Failed to decode token introspection response",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	if !tokenInfo.Active {
		return nil, &errors.StandardError{
			Code:      "TOKEN_INVALID",
			Message:   "Token is not active",
			Details:   "The provided access token is expired, revoked, or invalid",
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	return &tokenInfo, nil
}

// GetUserInfo retrieves user information from an access token using the userinfo endpoint.
// This is useful for extracting user details after authentication.
func (k *KeycloakClient) GetUserInfo(ctx context.Context, accessToken string) (*TokenInfo, error) {
	userInfoURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/userinfo", k.baseURL, k.realm)

	req, err := http.NewRequestWithContext(ctx, "GET", userInfoURL, nil)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create userinfo request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := k.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Failed to retrieve user info",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	var userInfo TokenInfo
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, &errors.StandardError{
			Code:      "DESERIALIZATION_ERROR",
			Message:   "Failed to decode userinfo response",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	return &userInfo, nil
}

// Logout revokes a user's refresh token (single session logout).
func (k *KeycloakClient) Logout(ctx context.Context, refreshToken string) error {
	logoutURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/logout", k.baseURL, k.realm)

	data := url.Values{}
	data.Set("client_id", k.clientID)
	data.Set("client_secret", k.clientSecret)
	data.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, "POST", logoutURL, strings.NewReader(data.Encode()))
	if err != nil {
		return &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create logout request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return &errors.StandardError{
			Code:      "KEYCLOAK_LOGOUT_FAILED",
			Message:   "Keycloak logout failed",
			Details:   fmt.Sprintf("Status: %d, Body: %s", resp.StatusCode, string(body)),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	return nil
}

// RevokeAllUserSessions terminates all active sessions for a user (global logout).
// This requires admin privileges and uses the Admin API.
func (k *KeycloakClient) RevokeAllUserSessions(ctx context.Context, userID string) error {
	if err := k.getAccessToken(ctx); err != nil {
		return &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	k.mu.RLock()
	token := k.accessToken
	k.mu.RUnlock()

	logoutURL := fmt.Sprintf("%s/admin/realms/%s/users/%s/logout", k.baseURL, k.realm, userID)

	req, err := http.NewRequestWithContext(ctx, "POST", logoutURL, nil)
	if err != nil {
		return &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create user logout request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := k.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return &errors.StandardError{
			Code:      "KEYCLOAK_LOGOUT_FAILED",
			Message:   "Failed to revoke user sessions",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	return nil
}

// GetUserSessions retrieves all active sessions for a user.
func (k *KeycloakClient) GetUserSessions(ctx context.Context, userID string) ([]UserSession, error) {
	if err := k.getAccessToken(ctx); err != nil {
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	k.mu.RLock()
	token := k.accessToken
	k.mu.RUnlock()

	sessionsURL := fmt.Sprintf("%s/admin/realms/%s/users/%s/sessions", k.baseURL, k.realm, userID)

	req, err := http.NewRequestWithContext(ctx, "GET", sessionsURL, nil)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create get sessions request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := k.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Failed to retrieve user sessions",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	var sessions []UserSession
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, &errors.StandardError{
			Code:      "DESERIALIZATION_ERROR",
			Message:   "Failed to decode sessions",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	return sessions, nil
}

// ============================================================================
// OAUTH2 URL GENERATION
// ============================================================================

// GetAuthorizationURL generates the OAuth2 authorization URL for standard login.
func (k *KeycloakClient) GetAuthorizationURL(redirectURI, state string, scopes []string) string {
	baseAuthURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/auth", k.baseURL, k.realm)

	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}

	params := url.Values{}
	params.Set("client_id", k.clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", strings.Join(scopes, " "))
	params.Set("state", state)

	return baseAuthURL + "?" + params.Encode()
}

// GetSocialLoginURL generates the OAuth2 authorization URL with a social provider hint.
// Supported providers: "google", "linkedin", "facebook", "github", etc.
func (k *KeycloakClient) GetSocialLoginURL(provider, redirectURI, state string) string {
	baseAuthURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/auth", k.baseURL, k.realm)

	params := url.Values{}
	params.Set("client_id", k.clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", "openid email profile")
	params.Set("state", state)
	params.Set("kc_idp_hint", provider)

	return baseAuthURL + "?" + params.Encode()
}

// GetLogoutURL generates the logout URL that redirects to a post-logout page.
func (k *KeycloakClient) GetLogoutURL(redirectURI string) string {
	logoutURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/logout", k.baseURL, k.realm)

	params := url.Values{}
	params.Set("client_id", k.clientID)
	if redirectURI != "" {
		params.Set("post_logout_redirect_uri", redirectURI)
	}

	return logoutURL + "?" + params.Encode()
}

// ============================================================================
// USER MANAGEMENT METHODS
// ============================================================================

// CreateUser creates a new user in Keycloak.
func (k *KeycloakClient) CreateUser(ctx context.Context, user *User) (*User, error) {
	if err := k.getAccessToken(ctx); err != nil {
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	k.mu.RLock()
	token := k.accessToken
	k.mu.RUnlock()

	// Ensure username is set (often to email)
	if user.Username == "" {
		user.Username = user.Email
	}

	userURL := fmt.Sprintf("%s/admin/realms/%s/users", k.baseURL, k.realm)

	jsonData, err := json.Marshal(user)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "SERIALIZATION_ERROR",
			Message:   "Failed to serialize user data",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", userURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create HTTP request",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := k.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusConflict {
		return nil, &errors.StandardError{
			Code:      "USER_ALREADY_EXISTS",
			Message:   "User with this email already exists",
			Details:   string(body),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	if resp.StatusCode != http.StatusCreated {
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Keycloak API error during user creation",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	// Extract user ID from Location header
	location := resp.Header.Get("Location")
	if location != "" {
		parts := strings.Split(location, "/")
		user.ID = parts[len(parts)-1]
	}

	return user, nil
}

// GetUserByEmail retrieves a user by their email address.
func (k *KeycloakClient) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	if err := k.getAccessToken(ctx); err != nil {
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	k.mu.RLock()
	token := k.accessToken
	k.mu.RUnlock()

	searchURL := fmt.Sprintf("%s/admin/realms/%s/users?email=%s&exact=true",
		k.baseURL, k.realm, url.QueryEscape(email))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create search request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := k.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Keycloak API error during user search",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	var users []User
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return nil, &errors.StandardError{
			Code:      "DESERIALIZATION_ERROR",
			Message:   "Failed to decode user search results",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	if len(users) == 0 {
		return nil, &errors.StandardError{
			Code:      "USER_NOT_FOUND",
			Message:   "User not found",
			Details:   fmt.Sprintf("No user found with email: %s", email),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	return &users[0], nil
}

// GetUser retrieves a user by their unique ID.
func (k *KeycloakClient) GetUser(ctx context.Context, userID string) (*User, error) {
	if err := k.getAccessToken(ctx); err != nil {
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	k.mu.RLock()
	token := k.accessToken
	k.mu.RUnlock()

	userURL := fmt.Sprintf("%s/admin/realms/%s/users/%s", k.baseURL, k.realm, userID)

	req, err := http.NewRequestWithContext(ctx, "GET", userURL, nil)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create get user request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := k.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &errors.StandardError{
			Code:      "USER_NOT_FOUND",
			Message:   "User not found",
			Details:   fmt.Sprintf("No user found with ID: %s", userID),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Keycloak API error during user retrieval",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, &errors.StandardError{
			Code:      "DESERIALIZATION_ERROR",
			Message:   "Failed to decode user details",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	return &user, nil
}

// UpdateUser updates an existing user in Keycloak.
func (k *KeycloakClient) UpdateUser(ctx context.Context, userID string, user *User) error {
	if err := k.getAccessToken(ctx); err != nil {
		return &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	k.mu.RLock()
	token := k.accessToken
	k.mu.RUnlock()

	userURL := fmt.Sprintf("%s/admin/realms/%s/users/%s", k.baseURL, k.realm, userID)

	jsonData, err := json.Marshal(user)
	if err != nil {
		return &errors.StandardError{
			Code:      "SERIALIZATION_ERROR",
			Message:   "Failed to serialize user data",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", userURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create update request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := k.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Failed to update user",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	return nil
}

// DeleteUser deletes a user by their unique ID.
func (k *KeycloakClient) DeleteUser(ctx context.Context, userID string) error {
	if err := k.getAccessToken(ctx); err != nil {
		return &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	k.mu.RLock()
	token := k.accessToken
	k.mu.RUnlock()

	userURL := fmt.Sprintf("%s/admin/realms/%s/users/%s", k.baseURL, k.realm, userID)

	req, err := http.NewRequestWithContext(ctx, "DELETE", userURL, nil)
	if err != nil {
		return &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create delete user request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := k.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Keycloak API error during user deletion",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	return nil
}

// ============================================================================
// PASSWORD & EMAIL VERIFICATION
// ============================================================================

// SetUserPassword sets or resets a user's password (admin operation).
func (k *KeycloakClient) SetUserPassword(ctx context.Context, userID, newPassword string, temporary bool) error {
	if err := k.getAccessToken(ctx); err != nil {
		return &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	k.mu.RLock()
	token := k.accessToken
	k.mu.RUnlock()

	resetURL := fmt.Sprintf("%s/admin/realms/%s/users/%s/reset-password",
		k.baseURL, k.realm, userID)

	credential := Credential{
		Type:      "password",
		Value:     newPassword,
		Temporary: temporary,
	}

	jsonData, err := json.Marshal(credential)
	if err != nil {
		return &errors.StandardError{
			Code:      "SERIALIZATION_ERROR",
			Message:   "Failed to serialize credential",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", resetURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create password reset request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := k.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Password reset failed",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	return nil
}

// SendPasswordResetEmail sends a password reset email to the user.
func (k *KeycloakClient) SendPasswordResetEmail(ctx context.Context, userID string, redirectURI string) error {
	if err := k.getAccessToken(ctx); err != nil {
		return &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	k.mu.RLock()
	token := k.accessToken
	k.mu.RUnlock()

	resetURL := fmt.Sprintf("%s/admin/realms/%s/users/%s/execute-actions-email",
		k.baseURL, k.realm, userID)

	if redirectURI != "" {
		resetURL += "?redirect_uri=" + url.QueryEscape(redirectURI)
	}

	actions := []string{"UPDATE_PASSWORD"}
	jsonData, _ := json.Marshal(actions)

	req, err := http.NewRequestWithContext(ctx, "PUT", resetURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create reset email request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := k.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Failed to send password reset email",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	return nil
}

// SendVerificationEmail sends an email verification link to the user.
func (k *KeycloakClient) SendVerificationEmail(ctx context.Context, userID string, redirectURI string) error {
	if err := k.getAccessToken(ctx); err != nil {
		return &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	k.mu.RLock()
	token := k.accessToken
	k.mu.RUnlock()

	emailURL := fmt.Sprintf("%s/admin/realms/%s/users/%s/send-verify-email",
		k.baseURL, k.realm, userID)

	if redirectURI != "" {
		emailURL += "?redirect_uri=" + url.QueryEscape(redirectURI)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", emailURL, nil)
	if err != nil {
		return &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create verification email request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := k.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Failed to trigger verification email",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	return nil
}

// ExecuteActionsEmail sends an email to the user to execute specific actions.
// Common actions: "VERIFY_EMAIL", "UPDATE_PASSWORD", "UPDATE_PROFILE", "CONFIGURE_TOTP"
func (k *KeycloakClient) ExecuteActionsEmail(ctx context.Context, userID string, actions []string, redirectURI string, lifespan int) error {
	if err := k.getAccessToken(ctx); err != nil {
		return &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	k.mu.RLock()
	token := k.accessToken
	k.mu.RUnlock()

	actionsURL := fmt.Sprintf("%s/admin/realms/%s/users/%s/execute-actions-email",
		k.baseURL, k.realm, userID)

	params := url.Values{}
	if redirectURI != "" {
		params.Set("redirect_uri", redirectURI)
	}
	if lifespan > 0 {
		params.Set("lifespan", fmt.Sprintf("%d", lifespan))
	}
	if len(params) > 0 {
		actionsURL += "?" + params.Encode()
	}

	jsonData, _ := json.Marshal(actions)

	req, err := http.NewRequestWithContext(ctx, "PUT", actionsURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create actions email request",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := k.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Failed to send actions email",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
			Timestamp: time.Now(),
		}
	}

	return nil
}

// ============================================================================
// TOKEN RESPONSE HELPER METHODS
// ============================================================================

// IsExpired checks if a token response has expired (with 60-second buffer).
func (tr *TokenResponse) IsExpired() bool {
	expiryTime := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return time.Now().Add(60 * time.Second).After(expiryTime)
}

// GetExpiry returns the expiration time of the access token.
func (tr *TokenResponse) GetExpiry() time.Time {
	return time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
}

// GetRefreshExpiry returns the expiration time of the refresh token.
func (tr *TokenResponse) GetRefreshExpiry() time.Time {
	return time.Now().Add(time.Duration(tr.RefreshExpiresIn) * time.Second)
}

// ============================================================================
// HELPER METHODS
// ============================================================================

// isTransientHTTPError returns true if the HTTP status code indicates a potentially transient error.
func (k *KeycloakClient) isTransientHTTPError(statusCode int) bool {
	switch statusCode {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// GetCircuitBreakerMetrics returns circuit breaker metrics.
func (k *KeycloakClient) GetCircuitBreakerMetrics() map[string]interface{} {
	return k.cb.Metrics()
}
