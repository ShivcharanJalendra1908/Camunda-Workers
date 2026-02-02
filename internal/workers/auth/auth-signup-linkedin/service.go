package authsignuplinkedin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/zoho"
)

type Service struct {
	config   *Config
	logger   logger.Logger
	keycloak *auth.KeycloakClient
	zohoCRM  *zoho.CRMClient
}

func NewService(deps ServiceDependencies, config *Config) *Service {
	return &Service{
		config:   config,
		logger:   deps.Logger,
		keycloak: deps.Keycloak,
		zohoCRM:  deps.ZohoCRM,
	}
}

func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {

	// 🔴 REQUIRED SAFETY CHECKS (SAME AS SIGNIN WORKERS)
	if s.keycloak == nil {
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_NOT_CONFIGURED",
			Message:   "Keycloak client is not initialized",
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	if s.config == nil {
		return nil, &errors.StandardError{
			Code:      "CONFIG_NOT_INITIALIZED",
			Message:   "Auth signup google config is missing",
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	s.logger.Info("Executing LinkedIn signup flow", map[string]interface{}{
		"email":          input.Email,
		"hasRedirectURI": input.RedirectURI != "",
		"hasState":       input.State != "",
	})

	// CRITICAL FIX: Exchange code with Keycloak (not LinkedIn directly)
	// Keycloak handles the LinkedIn OAuth flow internally via Identity Provider
	keycloakTokens, err := s.keycloak.ExchangeCodeForToken(ctx, input.AuthCode, input.RedirectURI)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_TOKEN_EXCHANGE_FAILED",
			Message:   "Failed to exchange authorization code with Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Get user info from Keycloak (which includes LinkedIn profile data)
	userInfo, err := s.keycloak.GetUserInfo(ctx, keycloakTokens.AccessToken)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "KEYCLOAK_USERINFO_ERROR",
			Message:   "Failed to retrieve user info from Keycloak",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Validate email matches input
	if input.Email != "" && userInfo.Email != input.Email {
		return nil, &errors.StandardError{
			Code:      "EMAIL_MISMATCH",
			Message:   "Email mismatch between LinkedIn account and provided email",
			Details:   fmt.Sprintf("Expected: %s, Got: %s", input.Email, userInfo.Email),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Check if this is a new user or existing user
	existingUser, err := s.keycloak.GetUserByEmail(ctx, userInfo.Email)
	if err != nil {
		// If user not found error, this is a new signup - continue
		if stdErr, ok := err.(*errors.StandardError); ok {
			if stdErr.Code != "USER_NOT_FOUND" {
				return nil, &errors.StandardError{
					Code:      "KEYCLOAK_ERROR",
					Message:   "Failed to check existing user",
					Details:   err.Error(),
					Retryable: true,
					Timestamp: time.Now(),
				}
			}
		}
	}

	// If user exists but trying to signup, return error
	if existingUser != nil {
		return nil, &errors.StandardError{
			Code:      "USER_ALREADY_EXISTS",
			Message:   "User with this email already exists",
			Details:   fmt.Sprintf("Email: %s", userInfo.Email),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// For new users created via LinkedIn OAuth, Keycloak automatically creates them
	// So we just need to fetch the newly created user details
	user, err := s.keycloak.GetUserByEmail(ctx, userInfo.Email)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "USER_CREATION_FAILED",
			Message:   "Failed to retrieve newly created user",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	// Extract names from user info
	firstName, lastName := s.extractNamesFromUserInfo(userInfo, input)

	// Update user with additional info if provided via input
	if input.FirstName != "" || input.LastName != "" || len(input.Metadata) > 0 {
		err := s.updateUserDetails(ctx, user.ID, input, firstName, lastName)
		if err != nil {
			// Log but don't fail - user is already created
			s.logger.Warn("Failed to update user details", map[string]interface{}{
				"userId": user.ID,
				"error":  err.Error(),
			})
		}
	}

	// Create CRM contact if enabled
	var crmContactID string
	if s.config.CreateCRMContact && s.zohoCRM != nil {
		contactID, err := s.createCRMContact(ctx, user, firstName, lastName)
		if err != nil {
			s.logger.Warn("Failed to create CRM contact, continuing without it", map[string]interface{}{
				"userId": user.ID,
				"error":  err.Error(),
			})
		} else {
			crmContactID = contactID
		}
	}

	s.logger.Info("LinkedIn signup completed successfully", map[string]interface{}{
		"userId":            user.ID,
		"email":             user.Email,
		"emailVerified":     userInfo.EmailVerified,
		"crmContactCreated": crmContactID != "",
	})

	return &Output{
		Success:       true,
		UserID:        user.ID,
		Email:         user.Email,
		FirstName:     firstName,
		LastName:      lastName,
		Token:         keycloakTokens.AccessToken,
		AccessToken:   keycloakTokens.AccessToken,
		RefreshToken:  keycloakTokens.RefreshToken,
		ExpiresIn:     keycloakTokens.ExpiresIn,
		TokenType:     keycloakTokens.TokenType,
		EmailVerified: userInfo.EmailVerified,
		PasswordSet:   false, // OAuth signup doesn't set password
		CRMContactID:  crmContactID,
	}, nil
}

func (s *Service) extractNamesFromUserInfo(userInfo *auth.TokenInfo, input *Input) (string, string) {
	// Priority 1: Use input names if explicitly provided
	if input.FirstName != "" || input.LastName != "" {
		return input.FirstName, input.LastName
	}

	// Priority 2: Use Keycloak's given_name and family_name
	if userInfo.GivenName != "" || userInfo.FamilyName != "" {
		return userInfo.GivenName, userInfo.FamilyName
	}

	// Priority 3: Split the full name from Keycloak
	if userInfo.Name != "" {
		parts := strings.Fields(userInfo.Name)
		if len(parts) == 0 {
			return "", ""
		}
		if len(parts) == 1 {
			return parts[0], ""
		}
		return parts[0], strings.Join(parts[1:], " ")
	}

	// Priority 4: Use preferred_username as fallback
	if userInfo.PreferredUsername != "" {
		return userInfo.PreferredUsername, ""
	}

	return "", ""
}

func (s *Service) updateUserDetails(ctx context.Context, userID string, input *Input, _, _ string) error {
	user, err := s.keycloak.GetUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user for update: %w", err)
	}

	updated := false

	// Update names if provided in input
	if input.FirstName != "" && user.FirstName != input.FirstName {
		user.FirstName = input.FirstName
		updated = true
	}
	if input.LastName != "" && user.LastName != input.LastName {
		user.LastName = input.LastName
		updated = true
	}

	// Add metadata as attributes
	if len(input.Metadata) > 0 {
		if user.Attributes == nil {
			user.Attributes = make(map[string][]string)
		}
		for key, value := range input.Metadata {
			if strVal, ok := value.(string); ok {
				user.Attributes[key] = []string{strVal}
				updated = true
			}
		}
	}

	if updated {
		err := s.keycloak.UpdateUser(ctx, userID, user)
		if err != nil {
			return fmt.Errorf("failed to update user: %w", err)
		}
		s.logger.Info("Updated user details", map[string]interface{}{
			"userId": userID,
		})
	}

	return nil
}

func (s *Service) createCRMContact(ctx context.Context, user *auth.User, firstName, lastName string) (string, error) {
	// Use provided names or user's names
	if firstName == "" {
		firstName = user.FirstName
	}
	if lastName == "" {
		lastName = user.LastName
	}

	contact := &zoho.Contact{
		Email:     user.Email,
		FirstName: firstName,
		LastName:  lastName,
		Source:    "LinkedIn Signup",
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
