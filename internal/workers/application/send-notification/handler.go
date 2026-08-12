package sendnotification

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/idempotency"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
	"github.com/aws/aws-sdk-go-v2/service/sns" // ✅ CORRECT IMPORT
	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/google/uuid"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "send-notification"
)

var (
	ErrNotificationSendFailed = errors.New("NOTIFICATION_SEND_FAILED")
)

// Define interfaces for mocking
type SESService interface {
	SendEmail(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error)
}

type SNSService interface {
	Publish(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error)
}

type Handler struct {
	config             *Config
	db                 *sql.DB
	logger             logger.Logger
	sesClient          SESService
	snsClient          SNSService
	templateMap        map[string]map[string]interface{}
	errorHandler       *appErrs.ErrorHandler
	validator          *validation.Validator
	sanitizer          *validation.Sanitizer
	keyGenerator       *idempotency.KeyGenerator
	idempotencyChecker *idempotency.DBChecker
}

func NewHandler(config *Config, db *sql.DB, log logger.Logger) (*Handler, error) {
	templateData, err := loadTemplates(config.TemplateRegistry)
	if err != nil {
		return nil, fmt.Errorf("load templates: %w", err)
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(config.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	return &Handler{
		config:             config,
		db:                 db,
		logger:             log.WithFields(map[string]interface{}{"taskType": TaskType}),
		sesClient:          ses.NewFromConfig(awsCfg),
		snsClient:          sns.NewFromConfig(awsCfg),
		templateMap:        templateData,
		errorHandler:       appErrs.NewErrorHandler(log),
		validator:          validation.NewValidator(),
		sanitizer:          validation.NewSanitizer(),
		keyGenerator:       idempotency.NewKeyGenerator(),
		idempotencyChecker: idempotency.NewDBChecker(db),
	}, nil
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	// ✅ EXTRACT TRACE CONTEXT
	ctx := context.Background()

	var traceID, parentSpanID string
	var jobVars map[string]interface{}

	if err := json.Unmarshal([]byte(job.Variables), &jobVars); err == nil {
		if tid, ok := jobVars["traceId"].(string); ok {
			traceID = tid
		}
		if psid, ok := jobVars["spanId"].(string); ok {
			parentSpanID = psid
		}
	}

	// ✅ CREATE WORKER SPAN
	tracer := otel.Tracer("worker-manager")
	ctx, span := tracer.Start(ctx, "worker:"+TaskType,
		trace.WithAttributes(
			attribute.String("worker.name", TaskType),
			attribute.Int64("job.key", job.GetKey()),
			attribute.Int64("workflow.instance_key", job.GetProcessInstanceKey()),
			attribute.String("workflow.process_id", job.GetBpmnProcessId()),
			attribute.String("workflow.element_id", job.GetElementId()),
			attribute.String("trace.parent_id", parentSpanID),
		),
	)
	defer span.End()

	h.logger.Info("processing job", map[string]interface{}{
		"jobKey":      job.Key,
		"workflowKey": job.ProcessInstanceKey,
		"traceId":     traceID,
		"spanId":      span.SpanContext().SpanID().String(),
	})

	// ===== STEP 1: PARSE INPUT =====
	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "send-notification.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewValidationError("input", fmt.Sprintf("parse input: %v", err)))
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== STEP 2: VALIDATE INPUT (GAP #1 FIX) =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "send-notification.validateInput")
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	// ===== STEP 3: SANITIZE INPUT =====
	_, spanSanitize := otel.Tracer("worker-manager").Start(ctx, "send-notification.sanitizeInput")
	h.sanitizeInput(&input)
	spanSanitize.End()

	// ===== STEP 4: EXECUTE BUSINESS LOGIC =====
	ctxExec, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()

	_, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "send-notification.Execute")
	output, err := h.execute(ctxExec, &input)
	spanExec.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		errorCode := "NOTIFICATION_SEND_FAILED"
		retries := int32(0)
		if errors.Is(err, ErrNotificationSendFailed) {
			retries = 3
			span.SetAttributes(attribute.Bool("retryable", true))
		}
		h.failJob(ctx, client, job, errorCode, err.Error(), retries)
		return
	}

	// ===== STEP 5: COMPLETE JOB =====
	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "send-notification.completeJob")
	h.completeJob(ctx, client, job, output)
	spanComp.End()
}

// ===== CRITICAL VALIDATION FUNCTION (GAP #1 FIX) =====
func (h *Handler) validateInput(input *Input) error {
	// Validate NotificationType
	if err := ozzo.Validate(input.NotificationType,
		ozzo.Required.Error("notificationType is required"),
		ozzo.Length(1, 50).Error("notificationType must be 1-50 characters"),
		validation.ValidateEnum([]string{
			TypeNewApplication,
			TypeApplicationSubmitted,
			// Add other types if they exist in your system
		}),
		validation.SafeSQLString,
	); err != nil {
		return appErrs.NewValidationError("notificationType", err.Error())
	}

	// Validate RecipientID (UUID)
	if err := ozzo.Validate(input.RecipientID,
		ozzo.Required.Error("recipientId is required"),
		ozzo.Length(36, 36).Error("recipientId must be exactly 36 characters"),
		validation.IsUUID,
		validation.SafeSQLString,
	); err != nil {
		return appErrs.NewInvalidUUIDError("recipientId", input.RecipientID)
	}

	// Validate RecipientType
	if err := ozzo.Validate(input.RecipientType,
		ozzo.Required.Error("recipientType is required"),
		ozzo.In(RecipientTypeFranchisor, RecipientTypeSeeker).Error("recipientType must be either 'franchisor' or 'seeker'"),
		validation.SafeSQLString,
	); err != nil {
		return appErrs.NewValidationError("recipientType", err.Error())
	}

	// Validate ApplicationID (UUID, optional)
	if input.ApplicationID != "" {
		if err := ozzo.Validate(input.ApplicationID,
			ozzo.Length(36, 36).Error("applicationId must be exactly 36 characters"),
			validation.IsUUID,
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewInvalidUUIDError("applicationId", input.ApplicationID)
		}
	}

	// Validate Priority
	if input.Priority != "" {
		if err := ozzo.Validate(input.Priority,
			ozzo.In("low", "medium", "high", "urgent").Error("priority must be one of: low, medium, high, urgent"),
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewValidationError("priority", err.Error())
		}
	}

	// Validate Metadata (if present)
	if input.Metadata != nil {
		if err := h.validateMetadata(input.Metadata); err != nil {
			return err
		}
	}

	// 🔒 SECURITY CHECKS
	// Prevent template injection in notification type
	if strings.Contains(strings.ToLower(input.NotificationType), "script:") ||
		strings.Contains(strings.ToLower(input.NotificationType), "javascript:") ||
		strings.Contains(strings.ToLower(input.NotificationType), "../") {
		return appErrs.NewValidationError("notificationType", "contains potentially unsafe content")
	}

	// Check for SQL injection in recipient fields
	sqlPatterns := []string{
		"';", "--", "/*", "*/", "UNION SELECT", "DROP TABLE", "INSERT INTO",
	}

	for _, pattern := range sqlPatterns {
		if strings.Contains(strings.ToLower(input.RecipientID), strings.ToLower(pattern)) ||
			strings.Contains(strings.ToLower(input.RecipientType), strings.ToLower(pattern)) {
			return appErrs.NewValidationError("recipient", "contains potentially unsafe SQL patterns")
		}
	}

	return nil
}

// ===== HELPER: Metadata Validation =====
func (h *Handler) validateMetadata(metadata map[string]interface{}) error {
	// Limit metadata size
	if len(metadata) > 20 {
		return appErrs.NewValidationError("metadata", "cannot exceed 20 items")
	}

	for key, value := range metadata {
		// Validate key
		if err := ozzo.Validate(key,
			ozzo.Length(1, 100).Error("metadata key must be 1-100 characters"),
			validation.IDString,
			validation.SafeSQLString,
		); err != nil {
			return appErrs.NewValidationError(fmt.Sprintf("metadata.%s", key), "invalid key: "+err.Error())
		}

		switch v := value.(type) {
		case string:
			// Validate string value length and content
			if len(v) > 1000 {
				return appErrs.NewValidationError(fmt.Sprintf("metadata.%s", key),
					"string value cannot exceed 1000 characters")
			}

			// Prevent script injection in metadata values
			if strings.Contains(strings.ToLower(v), "<script") ||
				strings.Contains(strings.ToLower(v), "javascript:") ||
				strings.Contains(strings.ToLower(v), "onload=") {
				return appErrs.NewValidationError(fmt.Sprintf("metadata.%s", key),
					"value contains potentially unsafe content")
			}

		case []interface{}:
			// Validate array size
			if len(v) > 50 {
				return appErrs.NewValidationError(fmt.Sprintf("metadata.%s", key),
					"array cannot exceed 50 items")
			}

			// Validate array items
			for i, item := range v {
				if str, ok := item.(string); ok && len(str) > 500 {
					return appErrs.NewValidationError(fmt.Sprintf("metadata.%s[%d]", key, i),
						"string item cannot exceed 500 characters")
				}
			}

		case map[string]interface{}:
			// Prevent nested objects (they could be too deep)
			return appErrs.NewValidationError(fmt.Sprintf("metadata.%s", key),
				"nested objects are not allowed")

		default:
			// Allow simple types
			continue
		}
	}

	return nil
}

// ===== HELPER: Input Sanitization =====
func (h *Handler) sanitizeInput(input *Input) {
	// Sanitize all string fields
	if input.NotificationType != "" {
		input.NotificationType = h.sanitizer.SanitizeString(input.NotificationType)
	}
	if input.RecipientID != "" {
		input.RecipientID = h.sanitizer.SanitizeString(input.RecipientID)
	}
	if input.RecipientType != "" {
		input.RecipientType = h.sanitizer.SanitizeString(input.RecipientType)
	}
	if input.ApplicationID != "" {
		input.ApplicationID = h.sanitizer.SanitizeString(input.ApplicationID)
	}
	if input.Priority != "" {
		input.Priority = h.sanitizer.SanitizeString(input.Priority)
	}

	// Sanitize metadata
	if input.Metadata != nil {
		input.Metadata = h.sanitizer.SanitizeInput(input.Metadata)
	}
}

// ===== ORIGINAL EXECUTE METHOD =====
func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	// ===== STEP 1: GENERATE IDEMPOTENCY KEY =====
	var idempotencyKey string
	if input.ApplicationID != "" {
		idempotencyKey = h.keyGenerator.GenerateNotificationKey(input.NotificationType, input.RecipientID, input.ApplicationID)
	} else {
		idempotencyKey = h.keyGenerator.GenerateNotificationKeySimple(input.NotificationType, input.RecipientID)
	}

	h.logger.Info("Generated idempotency key", map[string]interface{}{
		"idempotencyKey":   idempotencyKey,
		"notificationType": input.NotificationType,
		"traceId":          trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
	})

	// ===== STEP 2: CHECK IF NOTIFICATION ALREADY SENT =====
	_, spanCheck := otel.Tracer("worker-manager").Start(ctx, "send-notification.checkIdempotency")
	sent, existingNotifID, err := h.idempotencyChecker.CheckNotificationSent(ctx, input.NotificationType, input.RecipientID, input.ApplicationID)
	spanCheck.End()

	if err != nil {
		h.logger.Error("Failed to check notification", map[string]interface{}{
			"error":   err.Error(),
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}

	if sent {
		h.logger.Warn("Duplicate notification detected", map[string]interface{}{
			"notificationId": existingNotifID,
			"traceId":        trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})

		return &Output{
			NotificationID: existingNotifID,
			Status:         StatusSent,
			SentAt:         time.Now().UTC().Format(time.RFC3339),
		}, nil
	}

	// ===== STEP 3: MARK AS PROCESSING =====
	_, spanMark := otel.Tracer("worker-manager").Start(ctx, "send-notification.markProcessing")
	err = h.idempotencyChecker.MarkProcessing(ctx, idempotencyKey, "send-notification", 1*time.Hour)
	spanMark.End()

	if err != nil {
		h.logger.Error("Failed to mark as processing", map[string]interface{}{
			"error":   err.Error(),
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}

	_, spanContact := otel.Tracer("worker-manager").Start(ctx, "send-notification.getRecipientContact")
	email, phone, err := h.getRecipientContact(ctx, input.RecipientID, input.RecipientType)
	spanContact.End()

	if err != nil {
		h.logger.Warn("recipient not found", map[string]interface{}{
			"recipientId": input.RecipientID,
			"type":        input.RecipientType,
			"traceId":     trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
		return &Output{
			NotificationID: uuid.New().String(),
			Status:         StatusDisabled,
			SentAt:         time.Now().UTC().Format(time.RFC3339),
		}, nil
	}

	template, exists := h.templateMap[input.NotificationType]
	if !exists {
		return nil, fmt.Errorf("template not found for type: %s", input.NotificationType)
	}

	// Build data map for template rendering
	data := map[string]interface{}{
		"recipientId":      input.RecipientID,
		"notificationType": input.NotificationType,
		"applicationId":    input.ApplicationID,
		"priority":         input.Priority,
	}

	// Merge metadata if present
	if input.Metadata != nil {
		for k, v := range input.Metadata {
			data[k] = v
		}
	}

	subject := renderTemplate(template["subject"].(string), data)
	body := renderTemplate(template["body"].(string), data)

	// 🔒 Sanitize rendered content before sending
	subject = h.sanitizer.SanitizeString(subject)
	body = h.sanitizer.SanitizeString(body)

	// 🔒 Validate rendered content doesn't contain dangerous patterns
	if err := h.validateContent(subject, body); err != nil {
		h.logger.Warn("content validation failed", map[string]interface{}{
			"error":            err.Error(),
			"notificationType": input.NotificationType,
			"traceId":          trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
		// Use safe fallback content
		subject = "Notification"
		body = "You have a new notification."
	}

	sentAt := time.Now().UTC().Format(time.RFC3339)
	notificationID := uuid.New().String()

	// ===== INSERT WITH IDEMPOTENCY =====
	_, spanInsert := otel.Tracer("worker-manager").Start(ctx, "send-notification.insertNotification")
	insertQuery := `
        INSERT INTO notifications (
            id, notification_type, recipient_id, application_id,
            subject, message, channel, sent_at, idempotency_key
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
        ON CONFLICT (notification_type, recipient_id, application_id, DATE(sent_at))
        DO NOTHING
        RETURNING id`

	var returnedID string
	err = h.db.QueryRowContext(ctx, insertQuery,
		notificationID,
		input.NotificationType,
		input.RecipientID,
		input.ApplicationID,
		subject,
		body,
		"email",
		time.Now(),
		idempotencyKey,
	).Scan(&returnedID)

	spanInsert.End()

	if err == sql.ErrNoRows {
		h.logger.Warn("Concurrent notification detected", map[string]interface{}{
			"notificationType": input.NotificationType,
			"traceId":          trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})

		return &Output{
			NotificationID: notificationID,
			Status:         StatusSent,
			SentAt:         sentAt,
		}, nil
	}

	if err != nil {
		h.idempotencyChecker.MarkFailed(ctx, idempotencyKey)
		return &Output{NotificationID: notificationID, Status: StatusFailed, SentAt: sentAt}, nil
	}

	// ===== MARK AS COMPLETED =====
	responseData := map[string]interface{}{
		"notificationId": notificationID,
		"sent":           true,
		"sentAt":         sentAt,
	}

	err = h.idempotencyChecker.MarkCompleted(ctx, idempotencyKey, responseData)
	if err != nil {
		h.logger.Error("Failed to mark as completed", map[string]interface{}{
			"error":   err.Error(),
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}

	// Track what was sent
	emailSent := false
	smsSent := false

	// Send email if enabled and email exists
	if h.config.EmailEnabled && email != "" {
		_, spanEmail := otel.Tracer("worker-manager").Start(ctx, "send-notification.sendEmail")
		if err := h.sendEmail(ctx, email, subject, body); err != nil {
			h.logger.Error("email send failed", map[string]interface{}{
				"error":   err,
				"email":   email,
				"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
			})
			spanEmail.End()
			return &Output{NotificationID: notificationID, Status: StatusFailed, SentAt: sentAt}, nil
		}
		spanEmail.End()
		emailSent = true
	}

	// Send SMS only if: enabled AND phone exists AND priority is high
	if h.config.SMSEnabled && phone != "" && input.Priority == "high" {
		_, spanSMS := otel.Tracer("worker-manager").Start(ctx, "send-notification.sendSMS")
		if err := h.sendSMS(ctx, phone, body); err != nil {
			h.logger.Error("SMS send failed", map[string]interface{}{
				"error":   err,
				"phone":   phone,
				"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
			})
			spanSMS.End()
			return &Output{NotificationID: notificationID, Status: StatusFailed, SentAt: sentAt}, nil
		}
		spanSMS.End()
		smsSent = true
	}

	// Determine status based on what was sent
	status := StatusDisabled
	if emailSent || smsSent {
		status = StatusSent
	}

	return &Output{
		NotificationID: notificationID,
		Status:         status,
		SentAt:         sentAt,
	}, nil
}

// ===== HELPER: Content Validation =====
func (h *Handler) validateContent(subject, body string) error {
	// Check for dangerous patterns in notification content
	dangerousPatterns := []string{
		"<script", "</script>", "javascript:", "data:text/html",
		"onload=", "onerror=", "onclick=", "eval(", "alert(",
		"document.cookie", "window.location", "window.open",
	}

	combined := strings.ToLower(subject + " " + body)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(combined, pattern) {
			return fmt.Errorf("content contains potentially unsafe pattern: %s", pattern)
		}
	}

	// Check for URL redirection attempts
	urlPatterns := []string{
		"http://localhost", "http://127.0.0.1", "http://192.168.",
		"http://10.", "http://172.16.", "file://", "//evil.com",
	}

	for _, pattern := range urlPatterns {
		if strings.Contains(combined, pattern) {
			return fmt.Errorf("content contains potentially unsafe URL: %s", pattern)
		}
	}

	// Validate content length limits
	if len(subject) > 200 {
		return fmt.Errorf("subject too long (max 200 characters)")
	}
	if len(body) > 5000 {
		return fmt.Errorf("body too long (max 5000 characters)")
	}

	return nil
}

func (h *Handler) getRecipientContact(ctx context.Context, recipientID, recipientType string) (string, string, error) {
	var email, phone string
	var query string

	switch recipientType {
	case RecipientTypeFranchisor:
		query = `SELECT email, phone FROM franchisors WHERE id = $1`
	case RecipientTypeSeeker:
		query = `SELECT email, phone FROM users WHERE id = $1`
	default:
		return "", "", fmt.Errorf("invalid recipient type: %s", recipientType)
	}

	err := h.db.QueryRowContext(ctx, query, recipientID).Scan(&email, &phone)
	return email, phone, err
}

func (h *Handler) sendEmail(ctx context.Context, to, subject, body string) error {
	// Validate email address before sending
	if err := is.Email.Validate(to); err != nil {
		return fmt.Errorf("invalid email address: %s", to)
	}

	_, err := h.sesClient.SendEmail(ctx, &ses.SendEmailInput{
		Destination: &types.Destination{
			ToAddresses: []string{to},
		},
		Message: &types.Message{
			Subject: &types.Content{Data: aws.String(subject)},
			Body: &types.Body{
				Text: &types.Content{Data: aws.String(body)},
				Html: &types.Content{Data: aws.String(body)},
			},
		},
		Source: aws.String(h.config.FromEmail),
	})
	return err
}

func (h *Handler) sendSMS(ctx context.Context, to, message string) error {
	// Create regexp for phone validation
	phoneRegex := regexp.MustCompile(`^\+?[1-9]\d{1,14}$`)

	// Validate phone number using ozzo
	if err := ozzo.Validate(to,
		ozzo.Match(phoneRegex).Error("invalid phone number format"),
	); err != nil {
		return fmt.Errorf("invalid phone number: %s", to)
	}

	_, err := h.snsClient.Publish(ctx, &sns.PublishInput{
		PhoneNumber: aws.String(to),
		Message:     aws.String(message),
	})
	return err
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error":   err,
			"traceId": span.SpanContext().TraceID().String(),
		})
		return
	}
	_, err = cmd.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error":   err,
			"traceId": span.SpanContext().TraceID().String(),
		})
	}
}

func (h *Handler) failJob(ctx context.Context, client worker.JobClient, job entities.Job, errorCode, errorMessage string, retries int32) {
	stdErr := &appErrs.StandardError{
		Code:      appErrs.ErrorCode(errorCode),
		Message:   errorMessage,
		Retryable: retries > 0,
	}
	h.errorHandler.HandleJobError(ctx, client, job, stdErr)
}

// Simplified template rendering with placeholder removal for missing values
func renderTemplate(tmpl string, data map[string]interface{}) string {
	result := tmpl

	// First, replace all known placeholders
	for k, v := range data {
		placeholder := "{{" + k + "}}"
		value := ""
		if s, ok := v.(string); ok {
			value = s
		} else if i, ok := v.(int); ok {
			value = fmt.Sprintf("%d", i)
		} else if v != nil {
			value = fmt.Sprintf("%v", v)
		}
		result = strings.ReplaceAll(result, placeholder, value)
	}

	// Remove any remaining placeholders (missing values)
	// This handles {{missing}} -> empty string
	for {
		start := strings.Index(result, "{{")
		if start == -1 {
			break
		}
		end := strings.Index(result[start:], "}}")
		if end == -1 {
			break
		}
		end += start + 2
		result = result[:start] + result[end:]
	}

	return result
}

func loadTemplates(_ string) (map[string]map[string]interface{}, error) {
	return map[string]map[string]interface{}{
		TypeNewApplication: {
			"subject": "New Franchise Application Received",
			"body":    "Hello, you have a new application for {{applicationId}}. Priority: {{priority}}.",
		},
		TypeApplicationSubmitted: {
			"subject": "Application Submitted Successfully",
			"body":    "Thank you! Your application {{applicationId}} has been submitted.",
		},
		TypeBlogSubmitted: {
			"subject": "Your blog has been submitted for review — LeMiCi",
			"body":    "Hi {{authorName}},\n\nThank you for submitting your blog post \"{{blogTitle}}\" to LeMiCi!\n\nOur editorial team will review your content within 2–3 business days. You will receive an email notification once your blog goes live.\n\nBlog ID: {{applicationId}}\n\nIf you have any questions, feel free to reach out to our support team.\n\nBest regards,\nTeam LeMiCi",
		},
	}, nil
}

// Public Execute method for direct API calls
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	// Validate before execution
	if err := h.validateInput(input); err != nil {
		return nil, err
	}
	return h.execute(ctx, input)
}
