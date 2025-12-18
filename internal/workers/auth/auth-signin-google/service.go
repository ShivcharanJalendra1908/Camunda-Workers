package authsigningoogle

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
			Message:   "Auth signin google config is missing",
			Retryable: false,
			Timestamp: time.Now(),
		}
	}	
	
	s.logger.Info("Executing Google signin flow", map[string]interface{}{
		"hasRedirectURI": input.RedirectURI != "",
		"hasState":       input.State != "",
	})

	// CRITICAL FIX: Exchange code with Keycloak (not Google directly)
	// Keycloak handles the Google OAuth flow internally via Identity Provider
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

	// Get user info from Keycloak (which includes Google profile data)
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

	// Validate email is present and verified
	if err := s.validateUserInfo(userInfo); err != nil {
		return nil, &errors.StandardError{
			Code:      "INVALID_USER_INFO",
			Message:   "User info validation failed",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Get full user details from Keycloak
	user, err := s.keycloak.GetUserByEmail(ctx, userInfo.Email)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "USER_NOT_FOUND",
			Message:   "User does not exist in the system",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Check if this is a newly created user (created in this session)
	isNewUser := s.isRecentlyCreated(user)

	// Extract names from user info
	firstName, lastName := s.extractNamesFromUserInfo(userInfo, user)

	// Update user with additional metadata if provided
	if len(input.Metadata) > 0 {
		err := s.updateUserMetadata(ctx, user.ID, input.Metadata)
		if err != nil {
			// Log but don't fail - user can still sign in
			s.logger.Warn("Failed to update user metadata", map[string]interface{}{
				"userId": user.ID,
				"error":  err.Error(),
			})
		}
	}

	// Create CRM contact if this is a new user and CRM is enabled
	var crmContactID string
	if isNewUser && s.config.CreateCRMContact && s.zohoCRM != nil {
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

	s.logger.Info("Google signin completed successfully", map[string]interface{}{
		"userId":            user.ID,
		"email":             user.Email,
		"isNewUser":         isNewUser,
		"emailVerified":     userInfo.EmailVerified,
		"crmContactCreated": crmContactID != "",
	})

	return &Output{
		Success:       true,
		UserID:        user.ID,
		Email:         user.Email,
		FirstName:     firstName,
		LastName:      lastName,
		Token:         keycloakTokens.AccessToken, // Set token for backward compatibility
		AccessToken:   keycloakTokens.AccessToken,
		RefreshToken:  keycloakTokens.RefreshToken,
		ExpiresIn:     keycloakTokens.ExpiresIn,
		TokenType:     keycloakTokens.TokenType,
		IsNewUser:     isNewUser,
		EmailVerified: userInfo.EmailVerified,
		CRMContactID:  crmContactID,
	}, nil
}

func (s *Service) validateUserInfo(userInfo *auth.TokenInfo) error {
	if userInfo.Email == "" {
		return fmt.Errorf("email is required in user info")
	}

	if !userInfo.EmailVerified {
		return fmt.Errorf("email must be verified")
	}

	return nil
}

func (s *Service) extractNamesFromUserInfo(userInfo *auth.TokenInfo, user *auth.User) (string, string) {
	// Priority 1: Use Keycloak user's stored names
	if user.FirstName != "" || user.LastName != "" {
		return user.FirstName, user.LastName
	}

	// Priority 2: Use Keycloak's given_name and family_name from token
	if userInfo.GivenName != "" || userInfo.FamilyName != "" {
		return userInfo.GivenName, userInfo.FamilyName
	}

	// Priority 3: Split the full name from token
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

func (s *Service) isRecentlyCreated(user *auth.User) bool {
	// Consider user as "new" if created within last 5 minutes
	if user.CreatedTimestamp == 0 {
		return false
	}

	createdTime := time.Unix(user.CreatedTimestamp/1000, 0) // Keycloak uses milliseconds
	return time.Since(createdTime) < 5*time.Minute
}

func (s *Service) updateUserMetadata(ctx context.Context, userID string, metadata map[string]interface{}) error {
	user, err := s.keycloak.GetUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user for update: %w", err)
	}

	if user.Attributes == nil {
		user.Attributes = make(map[string][]string)
	}

	updated := false
	for key, value := range metadata {
		if strVal, ok := value.(string); ok {
			user.Attributes[key] = []string{strVal}
			updated = true
		}
	}

	if updated {
		err := s.keycloak.UpdateUser(ctx, userID, user)
		if err != nil {
			return fmt.Errorf("failed to update user metadata: %w", err)
		}
		s.logger.Info("Updated user metadata", map[string]interface{}{
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
		Source:    "Google Signin",
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
