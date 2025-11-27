package emailsend

import (
	"camunda-workers/internal/common/logger"
	"time"
)

type Input struct {
	From        string                 `json:"from"`
	To          string                 `json:"to"`
	CC          string                 `json:"cc,omitempty"`
	BCC         string                 `json:"bcc,omitempty"`
	ReplyTo     string                 `json:"replyTo,omitempty"`
	Subject     string                 `json:"subject"`
	Body        string                 `json:"body"`
	IsHTML      bool                   `json:"isHtml"`
	Priority    string                 `json:"priority,omitempty"`
	Attachments []Attachment           `json:"attachments,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Content     string `json:"content"` // Base64 encoded
}

type Output struct {
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	MessageID string    `json:"messageId,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	SentAt    time.Time `json:"sentAt,omitempty"`
}

type ServiceDependencies struct {
	Logger logger.Logger
}
