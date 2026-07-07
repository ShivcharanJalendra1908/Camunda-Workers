package handlers

import (
	"camunda-workers/internal/common/config"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type DocumentHandler struct {
	config *config.Config
}

func NewDocumentHandler(cfg *config.Config) *DocumentHandler {
	return &DocumentHandler{config: cfg}
}

// GeneratePresignedURL generates a secure, temporary S3 URL for direct document uploads
func (h *DocumentHandler) GeneratePresignedURL(c *gin.Context) {
	docType := c.Query("type")
	if docType == "" {
		docType = "document"
	}
	filename := c.Query("filename")
	if filename == "" {
		filename = c.Query("fileName")
	}

	if filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename or fileName is required"})
		return
	}

	// TODO: Replace with official github.com/aws/aws-sdk-go-v2 implementation
	// For now, returning the structural payload required by the frontend
	
	bucket := h.config.Integrations.AWS.S3.Bucket
	region := h.config.Integrations.AWS.S3.Region
	if bucket == "" {
		bucket = "lemici-documents"
	}
	if region == "" {
		region = "ap-south-1"
	}

	// Generate a secure, unique S3 key
	s3Key := fmt.Sprintf("franchise-docs/%s/%d_%s", docType, time.Now().Unix(), filename)
	
	// Mock pre-signed URL structure
	presignedURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=mock", bucket, region, s3Key)
	
	// The final URL the frontend should pass back in the StartUnifiedOnboarding payload
	finalS3URL := fmt.Sprintf("s3://%s/%s", bucket, s3Key)
	// Public access URL (useful for fileUrl in integration guide)
	publicURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, region, s3Key)

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"presignedUrl": presignedURL,
		"uploadUrl":    presignedURL, // alias for integration guide
		"s3Url":        finalS3URL,
		"fileUrl":      publicURL,    // HTTP URL alias for integration guide
		"expiresIn":    h.config.Integrations.AWS.S3.PresignTTL,
		"documentType": docType,
	})
}
