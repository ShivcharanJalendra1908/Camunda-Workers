// internal/common/aws/ses.go
package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
)

type SESClient struct {
	client *ses.Client
}

func NewSESClient(ctx context.Context, region string) (*SESClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return &SESClient{client: ses.NewFromConfig(cfg)}, nil
}

func (s *SESClient) SendEmail(ctx context.Context, input *ses.SendEmailInput) (*ses.SendEmailOutput, error) {
	return s.client.SendEmail(ctx, input)
}
