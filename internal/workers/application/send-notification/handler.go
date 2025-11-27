// internal/workers/application/send-notification/handler.go
package sendnotification

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"camunda-workers/internal/common/logger"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/google/uuid"
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
	config      *Config
	db          *sql.DB
	logger      logger.Logger
	sesClient   SESService
	snsClient   SNSService
	templateMap map[string]map[string]interface{}
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
		config:      config,
		db:          db,
		logger:      log.WithFields(map[string]interface{}{"taskType": TaskType}),
		sesClient:   ses.NewFromConfig(awsCfg),
		snsClient:   sns.NewFromConfig(awsCfg),
		templateMap: templateData,
	}, nil
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	h.logger.Info("processing job", map[string]interface{}{
		"jobKey":      job.Key,
		"workflowKey": job.ProcessInstanceKey,
	})

	var input Input
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
	defer cancel()

	output, err := h.execute(ctx, &input)
	if err != nil {
		errorCode := "NOTIFICATION_SEND_FAILED"
		retries := int32(0)
		if errors.Is(err, ErrNotificationSendFailed) {
			retries = 3
		}
		h.failJob(client, job, errorCode, err.Error(), retries)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
	email, phone, err := h.getRecipientContact(input.RecipientID, input.RecipientType)
	if err != nil {
		h.logger.Warn("recipient not found", map[string]interface{}{
			"recipientId": input.RecipientID,
			"type":        input.RecipientType,
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

	sentAt := time.Now().UTC().Format(time.RFC3339)
	notificationID := uuid.New().String()

	// Track what was sent
	emailSent := false
	smsSent := false

	// Send email if enabled and email exists
	if h.config.EmailEnabled && email != "" {
		if err := h.sendEmail(ctx, email, subject, body); err != nil {
			h.logger.Error("email send failed", map[string]interface{}{
				"error": err,
				"email": email,
			})
			return &Output{NotificationID: notificationID, Status: StatusFailed, SentAt: sentAt}, nil
		}
		emailSent = true
	}

	// Send SMS only if: enabled AND phone exists AND priority is high
	if h.config.SMSEnabled && phone != "" && input.Priority == "high" {
		if err := h.sendSMS(ctx, phone, body); err != nil {
			h.logger.Error("SMS send failed", map[string]interface{}{
				"error": err,
				"phone": phone,
			})
			return &Output{NotificationID: notificationID, Status: StatusFailed, SentAt: sentAt}, nil
		}
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

func (h *Handler) getRecipientContact(recipientID, recipientType string) (string, string, error) {
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

	err := h.db.QueryRowContext(context.Background(), query, recipientID).Scan(&email, &phone)
	return email, phone, err
}

func (h *Handler) sendEmail(ctx context.Context, to, subject, body string) error {
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
	_, err := h.snsClient.Publish(ctx, &sns.PublishInput{
		PhoneNumber: aws.String(to),
		Message:     aws.String(message),
	})
	return err
}

func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error": err,
		})
		return
	}
	_, err = cmd.Send(context.Background())
	if err != nil {
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error": err,
		})
	}
}

func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, _ int32) {
	h.logger.Error("job failed", map[string]interface{}{
		"jobKey":       job.Key,
		"errorCode":    errorCode,
		"errorMessage": errorMessage,
	})

	_, err := client.NewThrowErrorCommand().
		JobKey(job.Key).
		ErrorCode(errorCode).
		ErrorMessage(errorMessage).
		Send(context.Background())
	if err != nil {
		h.logger.Error("failed to throw error", map[string]interface{}{
			"error": err,
		})
	}
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
	}, nil
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

// // internal/workers/application/send-notification/handler.go
// package sendnotification

// import (
// 	"context"
// 	"database/sql"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"strings"
// 	"time"

// 	"github.com/aws/aws-sdk-go-v2/aws"
// 	awsconfig "github.com/aws/aws-sdk-go-v2/config"
// 	"github.com/aws/aws-sdk-go-v2/service/ses"
// 	"github.com/aws/aws-sdk-go-v2/service/ses/types"
// 	"github.com/aws/aws-sdk-go-v2/service/sns"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"github.com/google/uuid"
// 	"go.uber.org/zap"
// )

// const (
// 	TaskType = "send-notification"
// )

// var (
// 	ErrNotificationSendFailed = errors.New("NOTIFICATION_SEND_FAILED")
// )

// // Define interfaces for mocking
// type SESService interface {
// 	SendEmail(ctx context.Context, params *ses.SendEmailInput, optFns ...func(*ses.Options)) (*ses.SendEmailOutput, error)
// }

// type SNSService interface {
// 	Publish(ctx context.Context, params *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error)
// }

// type Handler struct {
// 	config      *Config
// 	db          *sql.DB
// 	logger      *zap.Logger
// 	sesClient   SESService
// 	snsClient   SNSService
// 	templateMap map[string]map[string]interface{}
// }

// func NewHandler(config *Config, db *sql.DB, logger *zap.Logger) (*Handler, error) {
// 	templateData, err := loadTemplates(config.TemplateRegistry)
// 	if err != nil {
// 		return nil, fmt.Errorf("load templates: %w", err)
// 	}

// 	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(config.AWSRegion))
// 	if err != nil {
// 		return nil, fmt.Errorf("load AWS config: %w", err)
// 	}

// 	return &Handler{
// 		config:      config,
// 		db:          db,
// 		logger:      logger.With(zap.String("taskType", TaskType)),
// 		sesClient:   ses.NewFromConfig(awsCfg),
// 		snsClient:   sns.NewFromConfig(awsCfg),
// 		templateMap: templateData,
// 	}, nil
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	h.logger.Info("processing job",
// 		zap.Int64("jobKey", job.Key),
// 		zap.Int64("workflowKey", job.ProcessInstanceKey),
// 	)

// 	var input Input
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
// 		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
// 		return
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		errorCode := "NOTIFICATION_SEND_FAILED"
// 		retries := int32(0)
// 		if errors.Is(err, ErrNotificationSendFailed) {
// 			retries = 3
// 		}
// 		h.failJob(client, job, errorCode, err.Error(), retries)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(ctx context.Context, input *Input) (*Output, error) {
// 	email, phone, err := h.getRecipientContact(input.RecipientID, input.RecipientType)
// 	if err != nil {
// 		h.logger.Warn("recipient not found",
// 			zap.String("recipientId", input.RecipientID),
// 			zap.String("type", input.RecipientType),
// 		)
// 		return &Output{
// 			NotificationID: uuid.New().String(),
// 			Status:         StatusDisabled,
// 			SentAt:         time.Now().UTC().Format(time.RFC3339),
// 		}, nil
// 	}

// 	template, exists := h.templateMap[input.NotificationType]
// 	if !exists {
// 		return nil, fmt.Errorf("template not found for type: %s", input.NotificationType)
// 	}

// 	// Build data map for template rendering
// 	data := map[string]interface{}{
// 		"recipientId":      input.RecipientID,
// 		"notificationType": input.NotificationType,
// 		"applicationId":    input.ApplicationID,
// 		"priority":         input.Priority,
// 	}

// 	// Merge metadata if present
// 	if input.Metadata != nil {
// 		for k, v := range input.Metadata {
// 			data[k] = v
// 		}
// 	}

// 	subject := renderTemplate(template["subject"].(string), data)
// 	body := renderTemplate(template["body"].(string), data)

// 	sentAt := time.Now().UTC().Format(time.RFC3339)
// 	notificationID := uuid.New().String()

// 	// Track what was sent
// 	emailSent := false
// 	smsSent := false

// 	// Send email if enabled and email exists
// 	if h.config.EmailEnabled && email != "" {
// 		if err := h.sendEmail(ctx, email, subject, body); err != nil {
// 			h.logger.Error("email send failed", zap.Error(err), zap.String("email", email))
// 			return &Output{NotificationID: notificationID, Status: StatusFailed, SentAt: sentAt}, nil
// 		}
// 		emailSent = true
// 	}

// 	// Send SMS only if: enabled AND phone exists AND priority is high
// 	if h.config.SMSEnabled && phone != "" && input.Priority == "high" {
// 		if err := h.sendSMS(ctx, phone, body); err != nil {
// 			h.logger.Error("SMS send failed", zap.Error(err), zap.String("phone", phone))
// 			return &Output{NotificationID: notificationID, Status: StatusFailed, SentAt: sentAt}, nil
// 		}
// 		smsSent = true
// 	}

// 	// Determine status based on what was sent
// 	status := StatusDisabled
// 	if emailSent || smsSent {
// 		status = StatusSent
// 	}

// 	return &Output{
// 		NotificationID: notificationID,
// 		Status:         status,
// 		SentAt:         sentAt,
// 	}, nil
// }

// func (h *Handler) getRecipientContact(recipientID, recipientType string) (string, string, error) {
// 	var email, phone string
// 	var query string

// 	switch recipientType {
// 	case RecipientTypeFranchisor:
// 		query = `SELECT email, phone FROM franchisors WHERE id = $1`
// 	case RecipientTypeSeeker:
// 		query = `SELECT email, phone FROM users WHERE id = $1`
// 	default:
// 		return "", "", fmt.Errorf("invalid recipient type: %s", recipientType)
// 	}

// 	err := h.db.QueryRowContext(context.Background(), query, recipientID).Scan(&email, &phone)
// 	return email, phone, err
// }

// func (h *Handler) sendEmail(ctx context.Context, to, subject, body string) error {
// 	_, err := h.sesClient.SendEmail(ctx, &ses.SendEmailInput{
// 		Destination: &types.Destination{
// 			ToAddresses: []string{to},
// 		},
// 		Message: &types.Message{
// 			Subject: &types.Content{Data: aws.String(subject)},
// 			Body: &types.Body{
// 				Text: &types.Content{Data: aws.String(body)},
// 				Html: &types.Content{Data: aws.String(body)},
// 			},
// 		},
// 		Source: aws.String(h.config.FromEmail),
// 	})
// 	return err
// }

// func (h *Handler) sendSMS(ctx context.Context, to, message string) error {
// 	_, err := h.snsClient.Publish(ctx, &sns.PublishInput{
// 		PhoneNumber: aws.String(to),
// 		Message:     aws.String(message),
// 	})
// 	return err
// }

// func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
// 	cmd, err := client.NewCompleteJobCommand().
// 		JobKey(job.Key).
// 		VariablesFromObject(output)
// 	if err != nil {
// 		h.logger.Error("failed to create complete job command", zap.Error(err))
// 		return
// 	}
// 	_, err = cmd.Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to send complete job command", zap.Error(err))
// 	}
// }

// func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, _ int32) {
// 	h.logger.Error("job failed",
// 		zap.Int64("jobKey", job.Key),
// 		zap.String("errorCode", errorCode),
// 		zap.String("errorMessage", errorMessage),
// 	)

// 	_, err := client.NewThrowErrorCommand().
// 		JobKey(job.Key).
// 		ErrorCode(errorCode).
// 		ErrorMessage(errorMessage).
// 		Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to throw error", zap.Error(err))
// 	}
// }

// // Simplified template rendering with placeholder removal for missing values
// func renderTemplate(tmpl string, data map[string]interface{}) string {
// 	result := tmpl

// 	// First, replace all known placeholders
// 	for k, v := range data {
// 		placeholder := "{{" + k + "}}"
// 		value := ""
// 		if s, ok := v.(string); ok {
// 			value = s
// 		} else if i, ok := v.(int); ok {
// 			value = fmt.Sprintf("%d", i)
// 		} else if v != nil {
// 			value = fmt.Sprintf("%v", v)
// 		}
// 		result = strings.ReplaceAll(result, placeholder, value)
// 	}

// 	// Remove any remaining placeholders (missing values)
// 	// This handles {{missing}} -> empty string
// 	for {
// 		start := strings.Index(result, "{{")
// 		if start == -1 {
// 			break
// 		}
// 		end := strings.Index(result[start:], "}}")
// 		if end == -1 {
// 			break
// 		}
// 		end += start + 2
// 		result = result[:start] + result[end:]
// 	}

// 	return result
// }

// func loadTemplates(_ string) (map[string]map[string]interface{}, error) {
// 	return map[string]map[string]interface{}{
// 		TypeNewApplication: {
// 			"subject": "New Franchise Application Received",
// 			"body":    "Hello, you have a new application for {{applicationId}}. Priority: {{priority}}.",
// 		},
// 		TypeApplicationSubmitted: {
// 			"subject": "Application Submitted Successfully",
// 			"body":    "Thank you! Your application {{applicationId}} has been submitted.",
// 		},
// 	}, nil
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }
