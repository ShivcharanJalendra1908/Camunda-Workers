package crmusercreate

import (
	"camunda-workers/internal/common/logger"
	"time"
)

type Input struct {
	Email        string                 `json:"email"`
	FirstName    string                 `json:"firstName"`
	LastName     string                 `json:"lastName"`
	Phone        string                 `json:"phone,omitempty"`
	Company      string                 `json:"company,omitempty"`
	JobTitle     string                 `json:"jobTitle,omitempty"`
	LeadSource   string                 `json:"leadSource,omitempty"`
	Tags         []string               `json:"tags,omitempty"`
	CustomFields map[string]interface{} `json:"customFields,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

type Output struct {
	Success     bool      `json:"success"`
	Message     string    `json:"message"`
	ContactID   string    `json:"contactId,omitempty"`
	AccountID   string    `json:"accountId,omitempty"`
	LeadID      string    `json:"leadId,omitempty"`
	CRMProvider string    `json:"crmProvider,omitempty"`
	CreatedAt   time.Time `json:"createdAt,omitempty"`
}

type ServiceDependencies struct {
	Logger logger.Logger
}
