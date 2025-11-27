// internal/common/aws/sns.go
package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

type SNSClient struct {
	client *sns.Client
}

func NewSNSClient(ctx context.Context, region string) (*SNSClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return &SNSClient{client: sns.NewFromConfig(cfg)}, nil
}

func (s *SNSClient) Publish(ctx context.Context, input *sns.PublishInput) (*sns.PublishOutput, error) {
	return s.client.Publish(ctx, input)
}
