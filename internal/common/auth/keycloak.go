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
	"time"

	"camunda-workers/internal/common/errors"
)

// KeycloakClient provides methods to interact with Keycloak for user management and authentication.
type KeycloakClient struct {
	baseURL      string
	realm        string
	clientID     string
	clientSecret string
	httpClient   *http.Client
	accessToken  string
	tokenExpiry  time.Time
}

// User represents a user in Keycloak.
type User struct {
	ID            string `json:"id,omitempty"`
	Email         string `json:"email"`
	FirstName     string `json:"firstName"`
	LastName      string `json:"lastName"`
	Username      string `json:"username"`
	Enabled       bool   `json:"enabled"`
	EmailVerified bool   `json:"emailVerified"`
}

// TokenResponse holds the response from Keycloak's token endpoint.
type TokenResponse struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
	TokenType        string `json:"token_type"`
	RefreshToken     string `json:"refresh_token"`
	Scope            string `json:"scope"`
}

// NewKeycloakClient creates a new instance of KeycloakClient.
func NewKeycloakClient(baseURL, realm, clientID, clientSecret string) *KeycloakClient {
	return &KeycloakClient{
		baseURL:      strings.TrimSuffix(baseURL, "/"),
		realm:        realm,
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

// getAccessToken fetches a new access token using the client credentials flow.
// It caches the token until expiry.
func (k *KeycloakClient) getAccessToken(ctx context.Context) error {
	if k.tokenExpiry.After(time.Now()) && k.accessToken != "" {
		// Token is still valid, no need to fetch a new one
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

	resp, err := k.httpClient.Do(req)
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

// CreateUser creates a new user in Keycloak.
func (k *KeycloakClient) CreateUser(ctx context.Context, user *User) (*User, error) {
	if err := k.getAccessToken(ctx); err != nil {
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true, // Auth errors might be transient
		}
	}

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
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", userURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create HTTP request",
			Details:   err.Error(),
			Retryable: true,
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+k.accessToken)

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "NETWORK_ERROR",
			Message:   "Failed to send request to Keycloak",
			Details:   err.Error(),
			Retryable: true,
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "IO_ERROR",
			Message:   "Failed to read Keycloak response",
			Details:   err.Error(),
			Retryable: true,
		}
	}

	if resp.StatusCode != http.StatusCreated {
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Keycloak API error during user creation",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
		}
	}

	// On success (201 Created), Keycloak usually returns an empty body.
	// The user ID is in the Location header.
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
		}
	}

	// Use Keycloak's search API by email
	searchURL := fmt.Sprintf("%s/admin/realms/%s/users?email=%s&exact=true", k.baseURL, k.realm, url.QueryEscape(email))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create search request",
			Details:   err.Error(),
			Retryable: false,
		}
	}

	req.Header.Set("Authorization", "Bearer "+k.accessToken)

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "NETWORK_ERROR",
			Message:   "Failed to send search request",
			Details:   err.Error(),
			Retryable: true,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Keycloak API error during user search",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
		}
	}

	var users []User
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return nil, &errors.StandardError{
			Code:      "DESERIALIZATION_ERROR",
			Message:   "Failed to decode user search results",
			Details:   err.Error(),
			Retryable: false,
		}
	}

	if len(users) == 0 {
		return nil, &errors.StandardError{
			Code:      "USER_NOT_FOUND",
			Message:   "User not found",
			Details:   fmt.Sprintf("No user found with email: %s", email),
			Retryable: false,
		}
	}

	// Assuming the search is exact and returns only one user
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
		}
	}

	userURL := fmt.Sprintf("%s/admin/realms/%s/users/%s", k.baseURL, k.realm, userID)

	req, err := http.NewRequestWithContext(ctx, "GET", userURL, nil)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create get user request",
			Details:   err.Error(),
			Retryable: false,
		}
	}

	req.Header.Set("Authorization", "Bearer "+k.accessToken)

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "NETWORK_ERROR",
			Message:   "Failed to send get user request",
			Details:   err.Error(),
			Retryable: true,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Keycloak API error during user retrieval",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
		}
	}

	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, &errors.StandardError{
			Code:      "DESERIALIZATION_ERROR",
			Message:   "Failed to decode user details",
			Details:   err.Error(),
			Retryable: false,
		}
	}

	return &user, nil
}

// DeleteUser deletes a user by their unique ID.
func (k *KeycloakClient) DeleteUser(ctx context.Context, userID string) error {
	if err := k.getAccessToken(ctx); err != nil {
		return &errors.StandardError{
			Code:      "KEYCLOAK_AUTH_ERROR",
			Message:   "Failed to authenticate with Keycloak",
			Details:   err.Error(),
			Retryable: true,
		}
	}

	userURL := fmt.Sprintf("%s/admin/realms/%s/users/%s", k.baseURL, k.realm, userID)

	req, err := http.NewRequestWithContext(ctx, "DELETE", userURL, nil)
	if err != nil {
		return &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create delete user request",
			Details:   err.Error(),
			Retryable: false,
		}
	}

	req.Header.Set("Authorization", "Bearer "+k.accessToken)

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return &errors.StandardError{
			Code:      "NETWORK_ERROR",
			Message:   "Failed to send delete user request",
			Details:   err.Error(),
			Retryable: true,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent { // 204 No Content is expected on success
		body, _ := io.ReadAll(resp.Body)
		return &errors.StandardError{
			Code:      "KEYCLOAK_API_ERROR",
			Message:   "Keycloak API error during user deletion",
			Details:   string(body),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
		}
	}

	return nil
}

// Logout revokes a user's refresh token. This is a standard OAuth2/OpenID Connect logout mechanism.
func (k *KeycloakClient) Logout(ctx context.Context, refreshToken string) error {
	logoutURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/logout", k.baseURL, k.realm)

	data := url.Values{}
	data.Set("client_id", k.clientID)
	data.Set("client_secret", k.clientSecret) // Required for confidential clients
	data.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, "POST", logoutURL, strings.NewReader(data.Encode()))
	if err != nil {
		return &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create logout request",
			Details:   err.Error(),
			Retryable: false,
		}
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return &errors.StandardError{
			Code:      "NETWORK_ERROR",
			Message:   "Failed to execute logout request",
			Details:   err.Error(),
			Retryable: true,
		}
	}
	defer resp.Body.Close()

	// Keycloak returns 204 No Content on successful logout
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return &errors.StandardError{
			Code:      "KEYCLOAK_LOGOUT_FAILED",
			Message:   "Keycloak logout failed",
			Details:   fmt.Sprintf("Status: %d, Body: %s", resp.StatusCode, string(body)),
			Retryable: k.isTransientHTTPError(resp.StatusCode),
		}
	}

	return nil
}

// ValidateToken checks if an access token is valid and active.
func (k *KeycloakClient) ValidateToken(ctx context.Context, token string) (*TokenInfo, error) {
	introspectURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token/introspect", k.baseURL, k.realm)

	data := url.Values{}
	data.Set("token", token)
	data.Set("token_type_hint", "access_token") // Hint to introspect an access token
	data.Set("client_id", k.clientID)
	data.Set("client_secret", k.clientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", introspectURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "HTTP_REQUEST_ERROR",
			Message:   "Failed to create introspection request",
			Details:   err.Error(),
			Retryable: false,
		}
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "NETWORK_ERROR",
			Message:   "Failed to send introspection request",
			Details:   err.Error(),
			Retryable: true,
		}
	}
	defer resp.Body.Close()

	var tokenInfo TokenInfo
	if err := json.NewDecoder(resp.Body).Decode(&tokenInfo); err != nil {
		return nil, &errors.StandardError{
			Code:      "DESERIALIZATION_ERROR",
			Message:   "Failed to decode token introspection response",
			Details:   err.Error(),
			Retryable: false,
		}
	}

	if !tokenInfo.Active {
		return nil, &errors.StandardError{
			Code:      "TOKEN_INVALID",
			Message:   "Token is not active",
			Details:   "The provided access token is expired, revoked, malformed, or invalid for other reasons.",
			Retryable: false,
		}
	}

	return &tokenInfo, nil
}

// isTransientHTTPError returns true if the HTTP status code indicates a potentially transient error.
func (k *KeycloakClient) isTransientHTTPError(statusCode int) bool {
	switch statusCode {
	case http.StatusInternalServerError, // 500
		http.StatusBadGateway,         // 502
		http.StatusServiceUnavailable, // 503
		http.StatusGatewayTimeout:     // 504
		return true
	default:
		return false
	}
}

// TokenInfo holds the information returned by the token introspection endpoint.
type TokenInfo struct {
	Active    bool     `json:"active"`
	Scope     string   `json:"scope,omitempty"`
	ClientID  string   `json:"client_id,omitempty"`
	Username  string   `json:"username,omitempty"`
	TokenType string   `json:"token_type,omitempty"`
	Exp       int64    `json:"exp,omitempty"` // Expiration timestamp (seconds since epoch)
	Iat       int64    `json:"iat,omitempty"` // Issued at timestamp (seconds since epoch)
	Nbf       int64    `json:"nbf,omitempty"` // Not before timestamp (seconds since epoch)
	Sub       string   `json:"sub,omitempty"` // Subject (user ID)
	Aud       []string `json:"aud,omitempty"` // Audience
	Iss       string   `json:"iss,omitempty"` // Issuer
	Jti       string   `json:"jti,omitempty"` // JWT ID
	// Add other fields as needed based on your Keycloak setup (e.g., realm_access, resource_access for roles)
}
