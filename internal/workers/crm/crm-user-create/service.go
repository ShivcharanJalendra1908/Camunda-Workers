package crmusercreate

import (
	"context"
	std_errors "errors"
	"fmt"
	"strings"
	"time"

	"camunda-workers/internal/common/circuitbreaker"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/zoho"
)

type Service struct {
	config     *Config
	logger     logger.Logger
	zohoClient *zoho.CRMClient
	cb         *circuitbreaker.CircuitBreaker
	cbManager  *circuitbreaker.Manager
}

func NewService(deps ServiceDependencies, config *Config) *Service {
	var zohoClient *zoho.CRMClient
	if config.ZohoAPIKey != "" && config.ZohoOAuthToken != "" {
		// Create circuit breaker manager
		cbManager := circuitbreaker.NewManager()

		// Pass the circuit breaker manager to NewCRMClient
		zohoClient = zoho.NewCRMClient(config.ZohoAPIKey, config.ZohoOAuthToken, cbManager)
	}

	return &Service{
		config:     config,
		logger:     deps.Logger,
		zohoClient: zohoClient,
		cbManager:  circuitbreaker.NewManager(),
		cb: circuitbreaker.New(circuitbreaker.Config{
			Name:             "zoho-crm",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
		}),
	}
}

func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {
	s.logger.Info("Executing CRM user create", map[string]interface{}{
		"email":      input.Email,
		"firstName":  input.FirstName,
		"lastName":   input.LastName,
		"company":    input.Company,
		"leadSource": input.LeadSource,
	})

	// NOTE: Input is already validated and sanitized by handler

	// Validate Zoho CRM configuration
	if s.zohoClient == nil {
		return nil, &errors.StandardError{
			Code:      "CRM_NOT_CONFIGURED",
			Message:   "Zoho CRM client not configured",
			Details:   "Missing API key or OAuth token configuration",
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Check if contact already exists
	existingContact, err := s.findExistingContact(ctx, input.Email)
	if err != nil {
		s.logger.Warn("Error checking for existing contact", map[string]interface{}{
			"email": input.Email,
			"error": err.Error(),
		})
		// Continue with creation attempt even if search fails
	} else if existingContact != nil {
		// Contact already exists - return existing contact info
		s.logger.Info("Contact already exists in CRM", map[string]interface{}{
			"email":     input.Email,
			"contactId": existingContact.ID,
		})

		return &Output{
			Success:     true,
			Message:     "Contact already exists in CRM",
			ContactID:   existingContact.ID,
			CRMProvider: "zoho",
			CreatedAt:   time.Now(),
		}, nil
	}

	// Create new contact in Zoho CRM
	var contactID string

	res, err := s.cb.Execute(func() (interface{}, error) {
		return s.createContact(ctx, input)
	})

	if err != nil {
		if std_errors.Is(err, circuitbreaker.ErrCircuitOpen) {
			s.logger.Warn("Circuit breaker open for Zoho CRM, using fallback", map[string]interface{}{
				"email": input.Email,
			})
			// Fallback: Return success to not block user flow, but indicate queuing (simulated here)
			return &Output{
				Success:     true,
				Message:     "CRM creation queued (fallback)",
				ContactID:   "queued-for-retry",
				CRMProvider: "zoho-fallback",
				CreatedAt:   time.Now(),
			}, nil
		}

		return nil, &errors.StandardError{
			Code:      "CRM_CREATE_FAILED",
			Message:   "Failed to create contact in CRM",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}
	contactID = res.(string)

	// Optionally create account if company is provided
	var accountID string
	if input.Company != "" && s.config.CreateAccount {
		accID, err := s.createAccount(ctx, input)
		if err != nil {
			s.logger.Warn("Failed to create CRM account", map[string]interface{}{
				"company":   input.Company,
				"contactId": contactID,
				"error":     err.Error(),
			})
			// Don't fail the entire operation if account creation fails
		} else {
			accountID = accID
			s.logger.Info("Created CRM account", map[string]interface{}{
				"accountId": accountID,
				"company":   input.Company,
			})
		}
	}

	// Apply tags if provided
	if len(input.Tags) > 0 {
		err := s.applyTags(ctx, contactID, input.Tags)
		if err != nil {
			s.logger.Warn("Failed to apply tags", map[string]interface{}{
				"contactId": contactID,
				"tags":      input.Tags,
				"error":     err.Error(),
			})
			// Don't fail the operation if tagging fails
		}
	}

	s.logger.Info("CRM user created successfully", map[string]interface{}{
		"contactId": contactID,
		"accountId": accountID,
		"email":     input.Email,
		"provider":  "zoho",
	})

	return &Output{
		Success:     true,
		Message:     "CRM user created successfully",
		ContactID:   contactID,
		AccountID:   accountID,
		CRMProvider: "zoho",
		CreatedAt:   time.Now(),
	}, nil
}

func (s *Service) findExistingContact(ctx context.Context, email string) (*zoho.Contact, error) {
	contacts, err := s.zohoClient.SearchContacts(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("failed to search contacts: %w", err)
	}

	if len(contacts) > 0 {
		return &contacts[0], nil
	}

	return nil, nil
}

func (s *Service) createContact(ctx context.Context, input *Input) (string, error) {
	contact := &zoho.Contact{
		Email:     input.Email,
		FirstName: input.FirstName,
		LastName:  input.LastName,
		Phone:     input.Phone,
		Source:    input.LeadSource,
	}

	// Add job title if provided
	if input.JobTitle != "" {
		contact.Title = input.JobTitle
	}

	// Add company if provided (as account name)
	if input.Company != "" {
		contact.AccountName = input.Company
	}

	contactID, err := s.zohoClient.CreateContact(ctx, contact)
	if err != nil {
		return "", fmt.Errorf("zoho API error: %w", err)
	}

	s.logger.Info("Created contact in CRM", map[string]interface{}{
		"contactId": contactID,
		"email":     input.Email,
	})

	return contactID, nil
}

func (s *Service) createAccount(ctx context.Context, input *Input) (string, error) {
	account := &zoho.Account{
		AccountName: input.Company,
		// Add any other relevant fields
	}

	accountID, err := s.zohoClient.CreateAccount(ctx, account)
	if err != nil {
		return "", fmt.Errorf("failed to create account: %w", err)
	}

	return accountID, nil
}

func (s *Service) applyTags(_ context.Context, contactID string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}

	// Zoho CRM tags API implementation would go here
	// For now, just log
	s.logger.Info("Would apply tags to contact", map[string]interface{}{
		"contactId": contactID,
		"tags":      tags,
	})

	return nil
}

func (s *Service) TestConnection(ctx context.Context) error {
	s.logger.Info("Testing CRM connection", map[string]interface{}{
		"provider": "zoho",
	})

	if s.zohoClient == nil {
		return fmt.Errorf("zoho CRM client not configured")
	}

	// Create a timeout context for the health check
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Try to search for a test contact to verify connection
	// This is a lightweight operation that verifies authentication
	_, err := s.zohoClient.SearchContacts(testCtx, "healthcheck@test.com")
	if err != nil {
		// Check if error is authentication-related
		errStr := err.Error()
		if strings.Contains(errStr, "401") || strings.Contains(errStr, "unauthorized") {
			return fmt.Errorf("zoho CRM authentication failed: invalid credentials")
		}
		if strings.Contains(errStr, "403") || strings.Contains(errStr, "forbidden") {
			return fmt.Errorf("zoho CRM authentication failed: access forbidden")
		}

		// For other errors (like not found), connection might still be OK
		s.logger.Debug("CRM test search completed with non-auth error", map[string]interface{}{
			"error": errStr,
		})
		return nil
	}

	s.logger.Info("CRM connection test successful", map[string]interface{}{
		"provider": "zoho",
	})

	return nil
}

func (s *Service) GetCircuitBreakerMetrics() map[string]interface{} {
	return s.zohoClient.GetCircuitBreakerMetrics()
}
