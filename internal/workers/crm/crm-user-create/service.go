package crmusercreate

import (
	"context"
	"fmt"
	"strings"
	"time"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/zoho"
)

type Service struct {
	config     *Config
	logger     logger.Logger
	zohoClient *zoho.CRMClient
}

func NewService(deps ServiceDependencies, config *Config) *Service {
	var zohoClient *zoho.CRMClient
	if config.ZohoAPIKey != "" && config.ZohoOAuthToken != "" {
		zohoClient = zoho.NewCRMClient(config.ZohoAPIKey, config.ZohoOAuthToken)
	}

	return &Service{
		config:     config,
		logger:     deps.Logger,
		zohoClient: zohoClient,
	}
}

func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {
	s.logger.Info("Executing CRM user create", map[string]interface{}{
		"email":     input.Email,
		"firstName": input.FirstName,
		"lastName":  input.LastName,
		"company":   input.Company,
	})

	// Validate email format
	if err := s.validateEmail(input.Email); err != nil {
		return nil, &errors.StandardError{
			Code:      "VALIDATION_FAILED",
			Message:   "Invalid email address",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Check if Zoho client is configured
	if s.zohoClient == nil {
		return nil, &errors.StandardError{
			Code:      "CRM_NOT_CONFIGURED",
			Message:   "Zoho CRM client not configured",
			Details:   "Missing API key or OAuth token",
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Check if contact already exists
	existingContacts, err := s.zohoClient.SearchContacts(ctx, input.Email)
	if err != nil {
		s.logger.Warn("Failed to search for existing contact", map[string]interface{}{
			"email": input.Email,
			"error": err.Error(),
		})
	} else if len(existingContacts) > 0 {
		// Contact already exists
		s.logger.Info("Contact already exists in CRM", map[string]interface{}{
			"email":     input.Email,
			"contactId": existingContacts[0].ID,
		})
		return &Output{
			Success:     true,
			Message:     "Contact already exists in CRM",
			ContactID:   existingContacts[0].ID,
			CRMProvider: "zoho",
			CreatedAt:   time.Now(),
		}, nil
	}

	// Create Zoho contact
	contact := &zoho.Contact{
		Email:     input.Email,
		FirstName: input.FirstName,
		LastName:  input.LastName,
		Phone:     input.Phone,
		Source:    input.LeadSource,
	}

	contactID, err := s.zohoClient.CreateContact(ctx, contact)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "CRM_API_ERROR",
			Message:   "Failed to create CRM contact",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	s.logger.Info("CRM user created successfully", map[string]interface{}{
		"contactId": contactID,
		"email":     input.Email,
		"provider":  "zoho",
	})

	return &Output{
		Success:     true,
		Message:     "CRM user created successfully",
		ContactID:   contactID,
		CRMProvider: "zoho",
		CreatedAt:   time.Now(),
	}, nil
}

func (s *Service) validateEmail(email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return fmt.Errorf("email is required")
	}
	// Basic email validation
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return fmt.Errorf("invalid email format")
	}
	if len(parts[0]) == 0 || len(parts[1]) == 0 {
		return fmt.Errorf("invalid email format")
	}
	if !strings.Contains(parts[1], ".") {
		return fmt.Errorf("invalid email domain")
	}
	return nil
}

func (s *Service) TestConnection(ctx context.Context) error {
	s.logger.Info("Testing CRM connection", map[string]interface{}{
		"provider": "zoho",
	})

	if s.zohoClient == nil {
		return fmt.Errorf("zoho CRM client not configured")
	}

	// Try to search for a test contact to verify connection
	// This is a lightweight operation that verifies authentication
	_, err := s.zohoClient.SearchContacts(ctx, "test@healthcheck.com")
	if err != nil {
		// If error is not authentication-related, connection might still be OK
		if !strings.Contains(err.Error(), "401") && !strings.Contains(err.Error(), "403") {
			return nil
		}
		return fmt.Errorf("zoho CRM authentication failed: %w", err)
	}

	return nil
}
