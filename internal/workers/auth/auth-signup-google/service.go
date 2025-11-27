package authsignupgoogle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/zoho"
	"camunda-workers/internal/models"
)

type Service struct {
	config     *Config
	logger     logger.Logger
	keycloak   *auth.KeycloakClient
	zohoCRM    *zoho.CRMClient
	httpClient *http.Client
}

func NewService(deps ServiceDependencies, config *Config) *Service {
	return &Service{
		config:     config,
		logger:     deps.Logger,
		keycloak:   deps.Keycloak,
		zohoCRM:    deps.ZohoCRM,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {
	s.logger.Info("Executing Google signup flow", map[string]interface{}{
		"email":          input.Email,
		"hasRedirectURI": input.RedirectURI != "",
		"hasState":       input.State != "",
	})

	// Step 1: Exchange authorization code for tokens
	tokens, err := s.exchangeCodeForTokens(ctx, input.AuthCode, input.RedirectURI)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "GOOGLE_OAUTH_ERROR",
			Message:   "Failed to exchange authorization code for access token",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Step 2: Get user profile from Google
	profile, err := s.getUserProfile(ctx, tokens.AccessToken)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "GOOGLE_API_ERROR",
			Message:   "Failed to retrieve user profile from Google API",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Step 3: Validate user profile and input
	if err := s.validateSignup(profile, input); err != nil {
		return nil, &errors.StandardError{
			Code:      "VALIDATION_FAILED",
			Message:   "Signup validation failed",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Step 4: Check if user already exists
	existingUser, err := s.keycloak.GetUserByEmail(ctx, input.Email)
	if err == nil && existingUser != nil {
		return nil, &errors.StandardError{
			Code:      "USER_ALREADY_EXISTS",
			Message:   "User with this email already exists",
			Details:   fmt.Sprintf("Email: %s", input.Email),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Step 5: Create new user in Keycloak
	user, err := s.createUser(ctx, profile, input)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_ERROR",
			Message:   "Failed to create user in identity provider",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Step 6: Create CRM contact
	var crmContactID string
	if s.config.CreateCRMContact && s.zohoCRM != nil {
		contactID, err := s.createCRMContact(ctx, user, profile)
		if err != nil {
			s.logger.Warn("Failed to create CRM contact, continuing without it", map[string]interface{}{
				"userId": user.ID,
				"error":  err.Error(),
			})
		} else {
			crmContactID = contactID
		}
	}

	s.logger.Info("Google signup completed successfully", map[string]interface{}{
		"userId":            user.ID,
		"email":             user.Email,
		"crmContactCreated": crmContactID != "",
	})

	firstName, lastName := s.extractNames(input, profile)

	return &Output{
		Success:       true,
		UserID:        user.ID,
		Email:         user.Email,
		FirstName:     firstName,
		LastName:      lastName,
		Token:         tokens.AccessToken,
		EmailVerified: profile.VerifiedEmail,
		PasswordSet:   false, // OAuth signup doesn't set password
		CRMContactID:  crmContactID,
	}, nil
}

func (s *Service) exchangeCodeForTokens(ctx context.Context, authCode, redirectURI string) (*GoogleTokenResponse, error) {
	tokenURL := "https://oauth2.googleapis.com/token"

	data := url.Values{}
	data.Set("code", authCode)
	data.Set("client_id", s.config.ClientID)
	data.Set("client_secret", s.config.ClientSecret)
	data.Set("redirect_uri", redirectURI)
	data.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp GoogleTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access token in response")
	}

	return &tokenResp, nil
}

func (s *Service) getUserProfile(ctx context.Context, accessToken string) (*GoogleUserProfile, error) {
	profileURL := "https://www.googleapis.com/oauth2/v2/userinfo"

	req, err := http.NewRequestWithContext(ctx, "GET", profileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create profile request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute profile request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read profile response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("profile request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var profile GoogleUserProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, fmt.Errorf("failed to decode profile response: %w", err)
	}

	return &profile, nil
}

func (s *Service) validateSignup(profile *GoogleUserProfile, input *Input) error {
	// Validate email matches
	if profile.Email != input.Email {
		return fmt.Errorf("email mismatch: Google email (%s) does not match provided email (%s)",
			profile.Email, input.Email)
	}

	// Validate email is verified in Google
	if !profile.VerifiedEmail {
		return fmt.Errorf("email must be verified in Google account")
	}

	// Validate email format
	if !strings.Contains(input.Email, "@") {
		return fmt.Errorf("invalid email format")
	}

	return nil
}

func (s *Service) createUser(ctx context.Context, profile *GoogleUserProfile, input *Input) (*models.AuthUser, error) {
	firstName, lastName := s.extractNames(input, profile)

	newUser := &auth.User{
		Email:         input.Email,
		FirstName:     firstName,
		LastName:      lastName,
		Username:      input.Email,
		Enabled:       true,
		EmailVerified: profile.VerifiedEmail,
	}

	createdUser, err := s.keycloak.CreateUser(ctx, newUser)
	if err != nil {
		return nil, fmt.Errorf("failed to create user in Keycloak: %w", err)
	}

	s.logger.Info("Successfully created new user in Keycloak", map[string]interface{}{
		"userId": createdUser.ID,
		"email":  input.Email,
	})

	return &models.AuthUser{
		ID:            createdUser.ID,
		Email:         createdUser.Email,
		Name:          fmt.Sprintf("%s %s", firstName, lastName),
		Provider:      models.ProviderGoogle,
		ProviderID:    profile.ID,
		EmailVerified: createdUser.EmailVerified,
		Status:        "active",
		CreatedAt:     time.Now(),
	}, nil
}

func (s *Service) createCRMContact(ctx context.Context, user *models.AuthUser, profile *GoogleUserProfile) (string, error) {
	firstName := user.Name
	lastName := ""

	parts := strings.Fields(user.Name)
	if len(parts) > 1 {
		firstName = parts[0]
		lastName = strings.Join(parts[1:], " ")
	}

	contact := &zoho.Contact{
		Email:     user.Email,
		FirstName: firstName,
		LastName:  lastName,
		Source:    "Google Signup",
	}

	contactID, err := s.zohoCRM.CreateContact(ctx, contact)
	if err != nil {
		return "", &errors.StandardError{
			Code:      "ZOHO_CRM_ERROR",
			Message:   "Failed to create contact in CRM",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	s.logger.Info("Successfully created CRM contact", map[string]interface{}{
		"userId":    user.ID,
		"contactId": contactID,
	})

	return contactID, nil
}

func (s *Service) extractNames(input *Input, profile *GoogleUserProfile) (string, string) {
	// Prioritize input names if provided
	if input.FirstName != "" || input.LastName != "" {
		return input.FirstName, input.LastName
	}

	// Use Google profile names
	if profile.GivenName != "" || profile.FamilyName != "" {
		return profile.GivenName, profile.FamilyName
	}

	// Fallback to splitting full name
	if profile.Name != "" {
		parts := strings.Fields(profile.Name)
		if len(parts) == 0 {
			return "", ""
		}
		if len(parts) == 1 {
			return parts[0], ""
		}
		return parts[0], strings.Join(parts[1:], " ")
	}

	return "", ""
}
