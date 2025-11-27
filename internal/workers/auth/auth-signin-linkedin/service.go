package authsigninlinkedin

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
	s.logger.Info("Executing LinkedIn OAuth authentication flow", map[string]interface{}{
		"hasRedirectURI": input.RedirectURI != "",
		"hasState":       input.State != "",
	})

	// Step 1: Exchange authorization code for tokens
	tokens, err := s.exchangeCodeForTokens(ctx, input.AuthCode, input.RedirectURI)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "LINKEDIN_OAUTH_ERROR",
			Message:   "Failed to exchange authorization code for access token",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Step 2: Get user profile from LinkedIn
	profile, err := s.getUserProfile(ctx, tokens.AccessToken)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "LINKEDIN_API_ERROR",
			Message:   "Failed to retrieve user profile from LinkedIn API",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Step 3: Get user email from LinkedIn
	email, err := s.getUserEmail(ctx, tokens.AccessToken)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "LINKEDIN_API_ERROR",
			Message:   "Failed to retrieve user email from LinkedIn API",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}
	profile.Email = email

	// Step 4: Validate user profile
	if err := s.validateProfile(profile); err != nil {
		return nil, &errors.StandardError{
			Code:      "INVALID_PROFILE",
			Message:   "User profile validation failed",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Step 5: Find or create user in Keycloak
	user, isNew, err := s.findOrCreateUser(ctx, profile)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_ERROR",
			Message:   "Failed to create or find user in identity provider",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Step 6: Create CRM contact for new users
	var crmContactID string
	if isNew && s.config.CreateCRMContact && s.zohoCRM != nil {
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

	s.logger.Info("LinkedIn OAuth authentication completed successfully", map[string]interface{}{
		"userId":            user.ID,
		"email":             profile.Email,
		"isNewUser":         isNew,
		"crmContactCreated": crmContactID != "",
	})

	firstName, lastName := extractNames(user)

	return &Output{
		Success:      true,
		UserID:       user.ID,
		Email:        user.Email,
		FirstName:    firstName,
		LastName:     lastName,
		Token:        tokens.AccessToken,
		IsNewUser:    isNew,
		CRMContactID: crmContactID,
	}, nil
}

func (s *Service) exchangeCodeForTokens(ctx context.Context, authCode, redirectURI string) (*LinkedInTokenResponse, error) {
	tokenURL := "https://www.linkedin.com/oauth/v2/accessToken"

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", authCode)
	data.Set("client_id", s.config.ClientID)
	data.Set("client_secret", s.config.ClientSecret)
	data.Set("redirect_uri", redirectURI)

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

	var tokenResp LinkedInTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access token in response")
	}

	return &tokenResp, nil
}

func (s *Service) getUserProfile(ctx context.Context, accessToken string) (*LinkedInUserProfile, error) {
	profileURL := "https://api.linkedin.com/v2/me"

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

	var profile LinkedInUserProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, fmt.Errorf("failed to decode profile response: %w", err)
	}

	return &profile, nil
}

func (s *Service) getUserEmail(ctx context.Context, accessToken string) (string, error) {
	emailURL := "https://api.linkedin.com/v2/emailAddress?q=members&projection=(elements*(handle~))"

	req, err := http.NewRequestWithContext(ctx, "GET", emailURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create email request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute email request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read email response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("email request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var emailResp LinkedInEmailResponse
	if err := json.Unmarshal(body, &emailResp); err != nil {
		return "", fmt.Errorf("failed to decode email response: %w", err)
	}

	if len(emailResp.Elements) == 0 {
		return "", fmt.Errorf("no email found in response")
	}

	return emailResp.Elements[0].Handle.EmailAddress, nil
}

func (s *Service) validateProfile(profile *LinkedInUserProfile) error {
	if profile.Email == "" {
		return fmt.Errorf("email is required in user profile")
	}
	return nil
}

func (s *Service) findOrCreateUser(ctx context.Context, profile *LinkedInUserProfile) (*models.AuthUser, bool, error) {
	existingUser, err := s.keycloak.GetUserByEmail(ctx, profile.Email)
	if err == nil {
		s.logger.Debug("Found existing user in Keycloak", map[string]interface{}{
			"userId": existingUser.ID,
			"email":  profile.Email,
		})
		return &models.AuthUser{
			ID:            existingUser.ID,
			Email:         existingUser.Email,
			Name:          fmt.Sprintf("%s %s", existingUser.FirstName, existingUser.LastName),
			Provider:      models.ProviderLinkedIn,
			ProviderID:    profile.ID,
			EmailVerified: existingUser.EmailVerified,
			Status:        "active",
		}, false, nil
	}

	s.logger.Info("Creating new user in Keycloak", map[string]interface{}{
		"email": profile.Email,
	})

	newUser := &auth.User{
		Email:         profile.Email,
		FirstName:     profile.FirstName,
		LastName:      profile.LastName,
		Username:      profile.Email,
		Enabled:       true,
		EmailVerified: true,
	}

	createdUser, err := s.keycloak.CreateUser(ctx, newUser)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create user in Keycloak: %w", err)
	}

	s.logger.Info("Successfully created new user in Keycloak", map[string]interface{}{
		"userId": createdUser.ID,
		"email":  profile.Email,
	})

	return &models.AuthUser{
		ID:            createdUser.ID,
		Email:         createdUser.Email,
		Name:          fmt.Sprintf("%s %s", createdUser.FirstName, createdUser.LastName),
		Provider:      models.ProviderLinkedIn,
		ProviderID:    profile.ID,
		EmailVerified: createdUser.EmailVerified,
		Status:        "active",
		CreatedAt:     time.Now(),
	}, true, nil
}

func (s *Service) createCRMContact(ctx context.Context, user *models.AuthUser, _ *LinkedInUserProfile) (string, error) {
	firstName, lastName := extractNames(user)

	contact := &zoho.Contact{
		Email:     user.Email,
		FirstName: firstName,
		LastName:  lastName,
		Source:    "LinkedIn Signin",
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

func extractNames(user *models.AuthUser) (string, string) {
	if user.Name == "" {
		return "", ""
	}

	parts := strings.Fields(user.Name)
	if len(parts) == 0 {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}

	return parts[0], strings.Join(parts[1:], " ")
}
