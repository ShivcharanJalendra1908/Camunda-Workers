// internal/workers/application/send-notification/models.go
package sendnotification

type Input struct {
	RecipientID      string                 `json:"recipientId"`
	RecipientType    string                 `json:"recipientType"` // "franchisor" or "seeker"
	NotificationType string                 `json:"notificationType"`
	ApplicationID    string                 `json:"applicationId,omitempty"`
	Priority         string                 `json:"priority,omitempty"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

type Output struct {
	NotificationID string `json:"notificationId"`
	Status         string `json:"status"` // "sent", "failed", "disabled"
	SentAt         string `json:"sentAt"` // ISO 8601
}

// Notification types
const (
	TypeNewApplication       = "new_application"
	TypeApplicationSubmitted = "application_submitted"
)

// Statuses
const (
	StatusSent     = "sent"
	StatusFailed   = "failed"
	StatusDisabled = "disabled"
)

// Recipient types
const (
	RecipientTypeFranchisor = "franchisor"
	RecipientTypeSeeker     = "seeker"
)
