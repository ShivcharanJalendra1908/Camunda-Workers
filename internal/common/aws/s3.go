// internal/common/aws/s3.go
package aws

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Client wraps the AWS S3 client for profile photo operations.
type S3Client struct {
	client *s3.Client
	bucket string
}

// NewS3Client creates a new S3Client using the default credential chain
// (env vars, IAM role, etc.) — same pattern as SES/SNS.
func NewS3Client(ctx context.Context, region, bucket string) (*S3Client, error) {
	if region == "" {
		region = "ap-south-1"
	}
	if bucket == "" {
		return nil, fmt.Errorf("S3 bucket name is required")
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config for S3: %w", err)
	}

	return &S3Client{
		client: s3.NewFromConfig(cfg),
		bucket: bucket,
	}, nil
}

// UploadResult contains the result of a successful S3 upload.
type UploadResult struct {
	Key  string
	Size int64
}

// UploadPhoto uploads a file to S3 and returns the object key.
func (s *S3Client) UploadPhoto(ctx context.Context, key string, contentType string, body io.Reader, size int64) (*UploadResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("S3 client not initialized")
	}

	contentType = sanitizeContentType(contentType)

	input := &s3.PutObjectInput{
		Bucket:       aws.String(s.bucket),
		Key:          aws.String(key),
		Body:         body,
		ContentType:  aws.String(contentType),
		CacheControl: aws.String("max-age=31536000, immutable"),
		Metadata: map[string]string{
			"uploaded-at": time.Now().UTC().Format(time.RFC3339),
		},
	}

	_, err := s.client.PutObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("S3 upload failed for key %q: %w", key, err)
	}

	return &UploadResult{
		Key:  key,
		Size: size,
	}, nil
}

// DeletePhoto removes a photo from S3 by its URL.
func (s *S3Client) DeletePhoto(ctx context.Context, photoURL string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("S3 client not initialized")
	}

	key := s.keyFromURL(photoURL)
	if key == "" {
		return fmt.Errorf("could not extract S3 key from URL: %s", photoURL)
	}

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("S3 delete failed for key %q: %w", key, err)
	}
	return nil
}

// DeletePhotoByKey removes a photo from S3 by its object key.
// Used for async cleanup of old photos after replacement or account deletion.
func (s *S3Client) DeletePhotoByKey(ctx context.Context, key string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("S3 client not initialized")
	}
	if key == "" {
		return nil // No key to delete
	}

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("S3 delete failed for key %q: %w", key, err)
	}
	return nil
}

// GeneratePresignedURL generates a temporary pre-signed URL.
func (s *S3Client) GeneratePresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if s == nil || s.client == nil {
		return "", fmt.Errorf("S3 client not initialized")
	}

	presignClient := s3.NewPresignClient(s.client)
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}

	result, err := presignClient.PresignPutObject(ctx, input, func(o *s3.PresignOptions) {
		o.Expires = ttl
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}
	return result.URL, nil
}

// keyFromURL extracts the S3 object key from a full S3 URL.
func (s *S3Client) keyFromURL(photoURL string) string {
	prefixes := []string{
		fmt.Sprintf("https://%s.s3.amazonaws.com/", s.bucket),
		fmt.Sprintf("https://%s.s3.", s.bucket),
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(photoURL, prefix) {
			key := strings.TrimPrefix(photoURL, prefix)
			if idx := strings.Index(key, "?"); idx != -1 {
				key = key[:idx]
			}
			return key
		}
	}
	return ""
}

// BuildPhotoURL constructs the full CDN URL from a stored S3 key.
// Called at read time only — never stored in DB.
// Returns empty string if key is empty (no photo uploaded).
func BuildPhotoURL(cdnBaseURL, key string) string {
	if key == "" || cdnBaseURL == "" {
		return ""
	}
	return strings.TrimRight(cdnBaseURL, "/") + "/" + strings.TrimLeft(key, "/")
}

// MakePhotoKey generates the S3 object key for a profile photo.
// Format: "profile-photos/{userID}/{uuid}.{ext}"
func MakePhotoKey(userID string, ext string) string {
	if ext == "" {
		ext = "jpg"
	}
	return path.Join("profile-photos", userID, fmt.Sprintf("%s.%s", generateUUID(), ext))
}

// sanitizeContentType maps file extensions to MIME types.
func sanitizeContentType(ct string) string {
	switch strings.ToLower(ct) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	default:
		if strings.HasPrefix(ct, "image/") {
			return ct
		}
		return "image/jpeg"
	}
}

// generateUUID returns a simple UUID v4 string.
func generateUUID() string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		time.Now().UnixNano(),
		time.Now().UnixNano()%0xffff,
		time.Now().UnixNano()%0xffff,
		time.Now().UnixNano()%0xffff,
		time.Now().UnixNano()%0xffffffffffff,
	)
}
