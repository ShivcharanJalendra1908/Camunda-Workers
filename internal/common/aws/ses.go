// internal/common/aws/ses.go
package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"

	"camunda-workers/internal/common/circuitbreaker"
)

type SESClient struct {
	client    *ses.Client
	fromEmail string
	cb        *circuitbreaker.CircuitBreaker

	// Fallback queue for failed emails
	emailQueue chan *EmailMessage
}

type EmailMessage struct {
	To      []string
	Subject string
	Body    string
	IsHTML  bool
}

func NewSESClient(ctx context.Context, region, fromEmail string, cbManager *circuitbreaker.Manager) (*SESClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	cb := cbManager.GetOrCreate("aws-ses", circuitbreaker.Config{
		Name:             "aws-ses",
		FailureThreshold: 5,
		SuccessThreshold: 2,
		Timeout:          30 * time.Second,
		MaxConcurrent:    15,
	})

	client := &SESClient{
		client:     ses.NewFromConfig(cfg),
		fromEmail:  fromEmail,
		cb:         cb,
		emailQueue: make(chan *EmailMessage, 1000),
	}

	// Start background worker for queued emails
	go client.processEmailQueue(ctx)

	return client, nil
}

func (s *SESClient) SendEmail(ctx context.Context, msg *EmailMessage) error {
	_, err := s.cb.Execute(func() (interface{}, error) {
		return nil, s.sendEmailInternal(ctx, msg)
	})

	if err != nil {
		if err == circuitbreaker.ErrCircuitOpen {
			// Queue email for retry
			select {
			case s.emailQueue <- msg:
				return fmt.Errorf("email queued for retry (SES unavailable)")
			default:
				return fmt.Errorf("email queue full, message dropped")
			}
		}
		return err
	}

	return nil
}

func (s *SESClient) sendEmailInternal(ctx context.Context, msg *EmailMessage) error {
	input := &ses.SendEmailInput{
		Source: aws.String(s.fromEmail),
		Destination: &types.Destination{
			ToAddresses: msg.To,
		},
		Message: &types.Message{
			Subject: &types.Content{
				Data: aws.String(msg.Subject),
			},
			Body: &types.Body{},
		},
	}

	if msg.IsHTML {
		input.Message.Body.Html = &types.Content{
			Data: aws.String(msg.Body),
		}
	} else {
		input.Message.Body.Text = &types.Content{
			Data: aws.String(msg.Body),
		}
	}

	_, err := s.client.SendEmail(ctx, input)
	return err
}

func (s *SESClient) processEmailQueue(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Only process if circuit is not open
			if s.cb.State() == circuitbreaker.StateOpen {
				continue
			}

			// Process emails
			for i := 0; i < 10; i++ {
				select {
				case msg := <-s.emailQueue:
					err := s.SendEmail(ctx, msg)
					if err != nil {
						// Put back if still failing
						select {
						case s.emailQueue <- msg:
						default:
							// Queue full
						}
					}
				default:
					goto nextTick
				}
			}
		nextTick:
		}
	}
}

func (s *SESClient) GetCircuitBreakerMetrics() map[string]interface{} {
	metrics := s.cb.Metrics()
	metrics["queued_emails"] = len(s.emailQueue)
	return metrics
}

func (s *SESClient) GetQueuedEmailCount() int {
	return len(s.emailQueue)
}
