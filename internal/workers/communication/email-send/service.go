// internal/workers/communication/email-send/service.go
package emailsend

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net/smtp"
	"path/filepath"
	"strings"
	"time"

	"camunda-workers/internal/common/circuitbreaker"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
)

type Service struct {
	config *Config
	logger logger.Logger
	cb     *circuitbreaker.CircuitBreaker

	// Fallback queue for when circuit is open
	pendingEmails chan *EmailJob
}

type EmailJob struct {
	Input     *Input
	Message   string
	MessageID string
	Attempts  int
}

func NewService(deps ServiceDependencies, config *Config) *Service {
	var cb *circuitbreaker.CircuitBreaker

	// Create circuit breaker from manager or standalone
	if deps.CBManager != nil {
		cb = deps.CBManager.GetOrCreate("email-send-smtp", circuitbreaker.Config{
			Name:             "email-send-smtp",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          60 * time.Second,
			MaxConcurrent:    10,
		})
	} else {
		cb = circuitbreaker.New(circuitbreaker.Config{
			Name:             "email-send-smtp",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          60 * time.Second,
		})
	}

	service := &Service{
		config:        config,
		logger:        deps.Logger,
		cb:            cb,
		pendingEmails: make(chan *EmailJob, 1000),
	}

	// Start background processor for pending emails
	go service.processPendingEmails(context.Background())

	return service
}

func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {
	s.logger.Info("Executing email send", map[string]interface{}{
		"to":          input.To,
		"subject":     input.Subject,
		"from":        input.From,
		"isHtml":      input.IsHTML,
		"attachments": len(input.Attachments),
	})

	// Validate SMTP configuration
	if err := s.validateSMTPConfig(); err != nil {
		return nil, &errors.StandardError{
			Code:      "SMTP_NOT_CONFIGURED",
			Message:   "SMTP service not properly configured",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

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
	message, err := s.buildEmailMessage(input)
	if err != nil {
		return nil, &errors.StandardError{
			Code:      "MESSAGE_BUILD_ERROR",
			Message:   "Failed to build email message",
			Details:   err.Error(),
			Retryable: false,
			Timestamp: time.Now(),
		}
	}

	// Generate message ID
	messageID := s.generateMessageID(input)

	// Send email via SMTP with circuit breaker protection
	err = s.sendSMTP(ctx, input, message)
	if err != nil {
		// Check if circuit breaker is open
		if err == circuitbreaker.ErrCircuitOpen {
			// Queue email for later processing
			emailJob := &EmailJob{
				Input:     input,
				Message:   message,
				MessageID: messageID,
				Attempts:  0,
			}

			select {
			case s.pendingEmails <- emailJob:
				s.logger.Warn("SMTP circuit breaker open - email queued", map[string]interface{}{
					"to":        input.To,
					"subject":   input.Subject,
					"messageId": messageID,
				})
				return &Output{
					Success:   false,
					Message:   "Email queued for delivery (SMTP unavailable)",
					MessageID: messageID,
					Provider:  "SMTP-queued",
					SentAt:    time.Now(),
				}, nil
			default:
				return nil, &errors.StandardError{
					Code:      "SMTP_UNAVAILABLE",
					Message:   "SMTP service unavailable and queue full",
					Details:   "Failed to send email and queue is at capacity",
					Retryable: true,
					Timestamp: time.Now(),
				}
			}
		}

		return nil, &errors.StandardError{
			Code:      "SMTP_SEND_ERROR",
			Message:   "Failed to send email via SMTP",
			Details:   err.Error(),
			Retryable: true,
			Timestamp: time.Now(),
		}
	}

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

func (s *Service) sendSMTP(ctx context.Context, input *Input, message string) error {
	_, err := s.cb.Execute(func() (interface{}, error) {
		return nil, s.sendSMTPInternal(ctx, input, message)
	})
	return err
}

func (s *Service) sendSMTPInternal(ctx context.Context, input *Input, message string) error {
	// Check context before sending
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
		return s.sendWithTLS(ctx, addr, auth, input.From, recipients, []byte(message))
	}

	// Plain SMTP
	return smtp.SendMail(addr, auth, input.From, recipients, []byte(message))
}

func (s *Service) sendWithTLS(ctx context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	// Check context
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled: %w", err)
	}

	// Connect to server
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server %s: %w", addr, err)
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
		return fmt.Errorf("failed to set sender %s: %w", from, err)
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

// processPendingEmails - Background worker for fallback queue
func (s *Service) processPendingEmails(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // Process every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Only process if circuit is not open
			if s.cb.State() == circuitbreaker.StateOpen {
				s.logger.Debug("Circuit breaker open, skipping pending emails", nil)
				continue
			}

			// Process up to 5 emails per batch
			for i := 0; i < 5; i++ {
				select {
				case emailJob := <-s.pendingEmails:
					emailJob.Attempts++

					// Try to send the email
					err := s.sendSMTP(ctx, emailJob.Input, emailJob.Message)
					if err != nil {
						s.logger.Warn("Failed to send queued email", map[string]interface{}{
							"messageId": emailJob.MessageID,
							"attempts":  emailJob.Attempts,
							"error":     err.Error(),
						})

						// If circuit breaker opens again during retry, put back in queue
						if err == circuitbreaker.ErrCircuitOpen {
							select {
							case s.pendingEmails <- emailJob:
								s.logger.Debug("Requeued email due to circuit breaker", map[string]interface{}{
									"messageId": emailJob.MessageID,
								})
							default:
								s.logger.Error("Failed to requeue email, queue full", map[string]interface{}{
									"messageId": emailJob.MessageID,
								})
							}
						} else if emailJob.Attempts < 3 {
							// Retry up to 3 times for other errors
							select {
							case s.pendingEmails <- emailJob:
							default:
								s.logger.Error("Failed to requeue email for retry", map[string]interface{}{
									"messageId": emailJob.MessageID,
								})
							}
						} else {
							s.logger.Error("Giving up on queued email after max attempts", map[string]interface{}{
								"messageId": emailJob.MessageID,
								"attempts":  emailJob.Attempts,
							})
						}
					} else {
						s.logger.Info("Successfully sent queued email", map[string]interface{}{
							"messageId": emailJob.MessageID,
							"attempts":  emailJob.Attempts,
						})
					}
				default:
					// No more pending emails
					goto nextTick
				}
			}
		nextTick:
		}
	}
}

// Helper methods from original service (keep as is)
func (s *Service) validateSMTPConfig() error {
	if s.config.SMTPHost == "" {
		return fmt.Errorf("SMTP host is not configured")
	}
	if s.config.SMTPPort <= 0 {
		return fmt.Errorf("SMTP port is not configured")
	}
	if s.config.DefaultFrom == "" {
		return fmt.Errorf("default from address is not configured")
	}
	return nil
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

func (s *Service) buildEmailMessage(input *Input) (string, error) {
	var builder strings.Builder

	// Generate message ID
	messageID := s.generateMessageID(input)

	// Headers
	builder.WriteString(fmt.Sprintf("Message-ID: %s\r\n", messageID))
	builder.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	builder.WriteString(fmt.Sprintf("From: %s\r\n", input.From))
	builder.WriteString(fmt.Sprintf("To: %s\r\n", input.To))

	if input.CC != "" {
		builder.WriteString(fmt.Sprintf("Cc: %s\r\n", input.CC))
	}

	if input.ReplyTo != "" {
		builder.WriteString(fmt.Sprintf("Reply-To: %s\r\n", input.ReplyTo))
	}

	// Encode subject for non-ASCII characters
	encodedSubject := mime.QEncoding.Encode("UTF-8", input.Subject)
	builder.WriteString(fmt.Sprintf("Subject: %s\r\n", encodedSubject))

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

	// Handle attachments
	if len(input.Attachments) > 0 {
		boundary := fmt.Sprintf("boundary_%d", time.Now().UnixNano())
		builder.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary))
		builder.WriteString("\r\n")

		// Body part
		builder.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		if input.IsHTML {
			builder.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
		} else {
			builder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		}
		builder.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		builder.WriteString("\r\n")
		builder.WriteString(input.Body)
		builder.WriteString("\r\n\r\n")

		// Attachment parts
		for _, att := range input.Attachments {
			if err := s.addAttachment(&builder, boundary, att); err != nil {
				return "", fmt.Errorf("failed to add attachment %s: %w", att.Filename, err)
			}
		}

		builder.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	} else {
	// Simple email without attachments
		if input.IsHTML {
			builder.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
		} else {
			builder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		}
		builder.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		builder.WriteString("\r\n")
		builder.WriteString(s.encodeQuotedPrintable(input.Body))
		builder.WriteString("\r\n")
	}

	return builder.String(), nil
}

func (s *Service) encodeQuotedPrintable(str string) string {
	var builder strings.Builder
	for _, r := range str {
		if r > 126 || r == '=' {
			builder.WriteString(fmt.Sprintf("=%02X", r))
		} else {
			builder.WriteRune(r)
		}
	}
	// Wrap lines at 76 characters
	content := builder.String()
	var wrapped strings.Builder
	for len(content) > 75 {
		wrapped.WriteString(content[:75] + "=\r\n")
		content = content[75:]
	}
	wrapped.WriteString(content)
	return wrapped.String()
}

func (s *Service) addAttachment(builder *strings.Builder, boundary string, att Attachment) error {
	builder.WriteString(fmt.Sprintf("--%s\r\n", boundary))

	// Determine content type
	contentType := att.ContentType
	if contentType == "" {
		// Try to guess from filename extension
		ext := filepath.Ext(att.Filename)
		contentType = mime.TypeByExtension(ext)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
	}

	builder.WriteString(fmt.Sprintf("Content-Type: application/octet-stream; name=\"%s\"\r\n", att.Filename))
	builder.WriteString("Content-Transfer-Encoding: base64\r\n")
	builder.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", att.Filename))
	builder.WriteString("\r\n")

	// Content should already be base64 encoded, but verify
	if !s.isBase64(att.Content) {
		// If not base64, encode it
		att.Content = base64.StdEncoding.EncodeToString([]byte(att.Content))
	}

	// Write base64 content in 76-character lines (RFC 2045)
	content := att.Content
	for len(content) > 76 {
		builder.WriteString(content[:76])
		builder.WriteString("\r\n")
		content = content[76:]
	}
	if len(content) > 0 {
		builder.WriteString(content)
		builder.WriteString("\r\n")
	}

	builder.WriteString("\r\n")
	return nil
}

func (s *Service) isBase64(str string) bool {
	_, err := base64.StdEncoding.DecodeString(str)
	return err == nil
}

func (s *Service) generateMessageID(input *Input) string {
	timestamp := time.Now().UnixNano()
	sanitized := sanitizeEmail(input.To)
	return fmt.Sprintf("<%d.%s@%s>", timestamp, sanitized, s.config.SMTPHost)
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
		if local != "" {
			return local
		}
	}
	return "user"
}

func (s *Service) TestConnection(ctx context.Context) error {
	if s.config.SMTPHost == "" {
		return fmt.Errorf("SMTP host not configured")
	}

	addr := fmt.Sprintf("%s:%d", s.config.SMTPHost, s.config.SMTPPort)

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Create a channel to signal completion
	done := make(chan error, 1)

	go func() {
		client, err := smtp.Dial(addr)
		if err != nil {
			done <- fmt.Errorf("failed to connect to SMTP server %s: %w", addr, err)
			return
		}
		defer client.Close()

		if s.config.UseTLS {
			tlsConfig := &tls.Config{
				ServerName:         s.config.SMTPHost,
				InsecureSkipVerify: false,
			}
			if err = client.StartTLS(tlsConfig); err != nil {
				done <- fmt.Errorf("failed to start TLS: %w", err)
				return
			}
		}

		done <- client.Quit()
	}()

	select {
	case err := <-done:
		return err
	case <-testCtx.Done():
		return fmt.Errorf("SMTP connection timeout")
	}
}

func (s *Service) GetCircuitBreakerMetrics() map[string]interface{} {
	metrics := s.cb.Metrics()
	metrics["pendingEmails"] = len(s.pendingEmails)
	return metrics
}

func (s *Service) GetPendingEmailCount() int {
	return len(s.pendingEmails)
}
