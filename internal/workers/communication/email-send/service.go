package emailsend

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"
	"time"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
)

type Service struct {
	config *Config
	logger logger.Logger
}

func NewService(deps ServiceDependencies, config *Config) *Service {
	return &Service{
		config: config,
		logger: deps.Logger,
	}
}

func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {
	s.logger.Info("Executing email send", map[string]interface{}{
		"to":      input.To,
		"subject": input.Subject,
		"from":    input.From,
		"isHtml":  input.IsHTML,
	})

	// Validate email addresses
	if err := s.validateEmailAddresses(input); err != nil {
		return nil, &errors.StandardError{
			Code:      "VALIDATION_FAILED",
			Message:   "Email validation failed",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Build email message
	message := s.buildEmailMessage(input)

	// Send email via SMTP
	if err := s.sendSMTP(ctx, input, message); err != nil {
		return nil, &errors.StandardError{
			Code:      "SMTP_ERROR",
			Message:   "Failed to send email via SMTP",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

	messageID := s.generateMessageID(input)

	s.logger.Info("Email sent successfully", map[string]interface{}{
		"to":        input.To,
		"messageId": messageID,
	})

	return &Output{
		Success:   true,
		Message:   "Email sent successfully",
		MessageID: messageID,
		Provider:  "SMTP",
		SentAt:    time.Now(),
	}, nil
}

func (s *Service) validateEmailAddresses(input *Input) error {
	// Validate To address
	if !s.isValidEmail(input.To) {
		return fmt.Errorf("invalid 'to' email address: %s", input.To)
	}

	// Validate From address
	if !s.isValidEmail(input.From) {
		return fmt.Errorf("invalid 'from' email address: %s", input.From)
	}

	// Validate CC addresses if present
	if input.CC != "" {
		ccAddresses := strings.Split(input.CC, ",")
		for _, addr := range ccAddresses {
			if !s.isValidEmail(strings.TrimSpace(addr)) {
				return fmt.Errorf("invalid 'cc' email address: %s", addr)
			}
		}
	}

	// Validate BCC addresses if present
	if input.BCC != "" {
		bccAddresses := strings.Split(input.BCC, ",")
		for _, addr := range bccAddresses {
			if !s.isValidEmail(strings.TrimSpace(addr)) {
				return fmt.Errorf("invalid 'bcc' email address: %s", addr)
			}
		}
	}

	// Validate ReplyTo if present
	if input.ReplyTo != "" && !s.isValidEmail(input.ReplyTo) {
		return fmt.Errorf("invalid 'replyTo' email address: %s", input.ReplyTo)
	}

	return nil
}

func (s *Service) isValidEmail(email string) bool {
	email = strings.TrimSpace(email)
	if email == "" {
		return false
	}
	// Basic email validation
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return false
	}
	if len(parts[0]) == 0 || len(parts[1]) == 0 {
		return false
	}
	if !strings.Contains(parts[1], ".") {
		return false
	}
	return true
}

func (s *Service) buildEmailMessage(input *Input) string {
	var builder strings.Builder

	// Headers
	builder.WriteString(fmt.Sprintf("From: %s\r\n", input.From))
	builder.WriteString(fmt.Sprintf("To: %s\r\n", input.To))

	if input.CC != "" {
		builder.WriteString(fmt.Sprintf("Cc: %s\r\n", input.CC))
	}

	if input.ReplyTo != "" {
		builder.WriteString(fmt.Sprintf("Reply-To: %s\r\n", input.ReplyTo))
	}

	builder.WriteString(fmt.Sprintf("Subject: %s\r\n", input.Subject))

	// Priority header
	if input.Priority != "" {
		switch strings.ToLower(input.Priority) {
		case "high":
			builder.WriteString("X-Priority: 1\r\n")
			builder.WriteString("Importance: high\r\n")
		case "low":
			builder.WriteString("X-Priority: 5\r\n")
			builder.WriteString("Importance: low\r\n")
		default:
			builder.WriteString("X-Priority: 3\r\n")
		}
	}

	// MIME headers
	builder.WriteString("MIME-Version: 1.0\r\n")

	if input.IsHTML {
		builder.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	} else {
		builder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	}

	builder.WriteString("\r\n")

	// Body
	builder.WriteString(input.Body)

	return builder.String()
}

func (s *Service) sendSMTP(ctx context.Context, input *Input, message string) error {

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before sending email: %w", err)
	}

	// Build recipient list
	recipients := []string{input.To}
	if input.CC != "" {
		ccAddresses := strings.Split(input.CC, ",")
		for _, addr := range ccAddresses {
			recipients = append(recipients, strings.TrimSpace(addr))
		}
	}
	if input.BCC != "" {
		bccAddresses := strings.Split(input.BCC, ",")
		for _, addr := range bccAddresses {
			recipients = append(recipients, strings.TrimSpace(addr))
		}
	}

	// SMTP server address
	addr := fmt.Sprintf("%s:%d", s.config.SMTPHost, s.config.SMTPPort)

	// Authentication
	var auth smtp.Auth
	if s.config.SMTPUsername != "" && s.config.SMTPPassword != "" {
		auth = smtp.PlainAuth("", s.config.SMTPUsername, s.config.SMTPPassword, s.config.SMTPHost)
	}

	// Send email
	if s.config.UseTLS {
		// TLS connection
		return s.sendWithTLS(addr, auth, input.From, recipients, []byte(message))
	}

	// Plain SMTP
	return smtp.SendMail(addr, auth, input.From, recipients, []byte(message))
}

func (s *Service) sendWithTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	// Connect to server
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %w", err)
	}
	defer client.Close()

	// Start TLS
	tlsConfig := &tls.Config{
		ServerName:         s.config.SMTPHost,
		InsecureSkipVerify: false,
	}

	if err = client.StartTLS(tlsConfig); err != nil {
		return fmt.Errorf("failed to start TLS: %w", err)
	}

	// Authenticate
	if auth != nil {
		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP authentication failed: %w", err)
		}
	}

	// Set sender
	if err = client.Mail(from); err != nil {
		return fmt.Errorf("failed to set sender: %w", err)
	}

	// Set recipients
	for _, addr := range to {
		if err = client.Rcpt(addr); err != nil {
			return fmt.Errorf("failed to set recipient %s: %w", addr, err)
		}
	}

	// Send message
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("failed to open data writer: %w", err)
	}

	_, err = w.Write(msg)
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	err = w.Close()
	if err != nil {
		return fmt.Errorf("failed to close data writer: %w", err)
	}

	return client.Quit()
}

func (s *Service) generateMessageID(input *Input) string {
	timestamp := time.Now().UnixNano()
	return fmt.Sprintf("<%d.%s@%s>", timestamp, sanitizeEmail(input.To), s.config.SMTPHost)
	//return fmt.Sprintf("<%d@%s>", timestamp, s.config.SMTPHost)
}

func sanitizeEmail(email string) string {
	// Extract local part before @ for message ID
	parts := strings.Split(email, "@")
	if len(parts) > 0 {
		// Remove any special characters and limit length
		local := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, parts[0])

		if len(local) > 10 {
			local = local[:10]
		}
		return local
	}
	return "user"
}

func (s *Service) TestConnection(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.config.SMTPHost, s.config.SMTPPort)

	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %w", err)
	}
	defer client.Close()

	if s.config.UseTLS {
		tlsConfig := &tls.Config{
			ServerName:         s.config.SMTPHost,
			InsecureSkipVerify: false,
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("failed to start TLS: %w", err)
		}
	}

	return client.Quit()
}
