// internal/models/notification.go
package models

type Notification struct {
	ID            string                 `json:"id"`
	RecipientID   string                 `json:"recipientId"`
	RecipientType string                 `json:"recipientType"` // "franchisor" or "seeker"
	Type          string                 `json:"type"`          // "new_application", "application_submitted"
	Channel       string                 `json:"channel"`       // "email", "sms"
	Status        string                 `json:"status"`        // "sent", "failed", "disabled"
	Payload       map[string]interface{} `json:"payload"`
	SentAt        string                 `json:"sentAt"`
	CreatedAt     string                 `json:"createdAt"`
}

type NotificationTemplate struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	HTMLBody string `json:"htmlBody,omitempty"`
	Version  string `json:"version"`
}
