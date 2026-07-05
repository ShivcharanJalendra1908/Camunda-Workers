// internal/api/handlers/workflow_handler.go
package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	apierrors "camunda-workers/internal/api/errors"
	"camunda-workers/internal/api/middleware"
	"camunda-workers/internal/common/auth/session"
	awsutil "camunda-workers/internal/common/aws"
	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/constants"
	"camunda-workers/internal/common/encryption"
	// "camunda-workers/internal/common/flagsmith"
	"camunda-workers/internal/common/idempotency"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"
	"camunda-workers/internal/common/workflow"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

type WorkflowHandler struct {
	camunda      *camunda.Client
	logger       logger.Logger
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
	redisStore   *idempotency.RedisStore
	keyGenerator *idempotency.KeyGenerator
	redisClient  *redis.Client
	config       *config.Config
	fle          *encryption.FLEService
	s3Client     *awsutil.S3Client
}

func NewWorkflowHandler(camunda *camunda.Client, logger logger.Logger, redisClient *redis.Client, cfg *config.Config, fle *encryption.FLEService, s3Client *awsutil.S3Client) *WorkflowHandler {
	var redisStore *idempotency.RedisStore
	if redisClient != nil {
		redisStore = idempotency.NewRedisStore(redisClient)
	}

	return &WorkflowHandler{
		camunda:      camunda,
		logger:       logger,
		validator:    validation.NewValidator(),
		sanitizer:    validation.NewSanitizer(),
		redisStore:   redisStore,
		keyGenerator: idempotency.NewKeyGenerator(),
		redisClient:  redisClient,
		config:       cfg,
		fle:          fle,
		s3Client:     s3Client,
	}
}

// Generic workflow start response
type WorkflowResponse struct {
	WorkflowInstanceKey int64                  `json:"workflowInstanceKey"`
	ProcessID           string                 `json:"processId"`
	RequestID           string                 `json:"requestId"`
	Status              string                 `json:"status"`
	Message             string                 `json:"message"`
	Variables           map[string]interface{} `json:"variables,omitempty"`
}

func (h *WorkflowHandler) getRequestID(c *gin.Context) string {
	if requestID := c.GetString("requestId"); requestID != "" {
		return requestID
	}
	return uuid.New().String()
}

// ===== VALIDATION & SANITIZATION HELPERS =====
func (h *WorkflowHandler) validateEmail(email string) error {
	return ozzo.Validate(email,
		ozzo.Required.Error("email is required"),
		is.Email.Error("invalid email format"),
		ozzo.Length(5, 255).Error("email must be between 5 and 255 characters"),
	)
}

func (h *WorkflowHandler) validateUUID(id string) error {
	return ozzo.Validate(id,
		ozzo.Required.Error("id is required"),
		ozzo.Length(36, 36).Error("id must be 36 characters"),
		ozzo.Match(regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)).Error("invalid UUID format"),
	)
}

func (h *WorkflowHandler) validateString(s string, minLen, maxLen int) error {
	return ozzo.Validate(s,
		ozzo.Required.Error("field is required"),
		ozzo.Length(minLen, maxLen).Error(fmt.Sprintf("must be between %d and %d characters", minLen, maxLen)),
	)
}

func (h *WorkflowHandler) validateURL(url string) error {
	return ozzo.Validate(url,
		is.URL.Error("invalid URL format"),
		ozzo.Length(0, 500).Error("URL must be less than 500 characters"),
	)
}

func (h *WorkflowHandler) validateSlug(slug string) error {
	return ozzo.Validate(slug,
		ozzo.Required.Error("slug is required"),
		ozzo.Length(3, 100).Error("slug must be between 3 and 100 characters"),
		ozzo.Match(regexp.MustCompile(`^[a-z0-9-]+$`)).Error("slug can only contain lowercase letters, numbers, and hyphens"),
	)
}

func (h *WorkflowHandler) validatePhone(phone string) error {
	return ozzo.Validate(phone,
		ozzo.Length(0, 20).Error("phone must be less than 20 characters"),
		ozzo.Match(regexp.MustCompile(`^[\d\s\+\-\(\)]*$`)).Error("invalid phone number format"),
	)
}

func (h *WorkflowHandler) sanitizeInput(input string) string {
	if h.sanitizer != nil {
		return strings.TrimSpace(input)
	}

	input = strings.TrimSpace(input)

	dangerousPatterns := []string{
		"'", "\"", ";", "--", "/*", "*/", "@@", "@",
		"char(", "nchar(", "varchar(", "nvarchar(",
		"alter ", "begin", "cast(", "create ", "cursor ",
		"declare ", "delete ", "drop ", "end", "exec ",
		"execute ", "fetch ", "insert ", "kill", "select ",
		"sys", "sysobjects", "syscolumns", "table", "update",
		"<script>", "</script>", "javascript:", "onload=",
		"onerror=", "onclick=",
	}

	lower := strings.ToLower(input)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(lower, pattern) {
			input = strings.ReplaceAll(input, pattern, "")
			input = strings.ReplaceAll(input, strings.ToUpper(pattern), "")
		}
	}

	input = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(input, "")

	return input
}

// ============================================================================
// AI CONVERSATION WORKFLOWS
// ============================================================================

func (h *WorkflowHandler) StartAIQuery(c *gin.Context) {
	var input struct {
		Question string                 `json:"question" binding:"required"`
		Context  map[string]interface{} `json:"context"`
		UserID   string                 `json:"userId"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate question
	if err := h.validateString(input.Question, 1, 1000); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid question: " + err.Error()})
		return
	}

	// Sanitize question
	input.Question = h.sanitizeInput(input.Question)

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{} // Default empty claims
	}

	variables := map[string]interface{}{
		"question":         input.Question,
		"userId":           getOrDefault(input.UserID, claims.UserID),
		"sessionId":        claims.SessionID,
		"sourceSystem":     claims.SourceSystem,
		"subscriptionTier": claims.SubscriptionTier,
		"requestId":        h.getRequestID(c),
	}

	if input.Context != nil {
		variables["context"] = input.Context
	}

	response := h.startWorkflow(c.Request.Context(), "ai_query", variables)
	c.JSON(http.StatusOK, response)
}

func (h *WorkflowHandler) StartDiscovery(c *gin.Context) {
	var input struct {
		Query   string                 `json:"query" binding:"required"`
		Filters map[string]interface{} `json:"filters"`
		UserID  string                 `json:"userId"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate query
	if err := h.validateString(input.Query, 1, 500); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid query: " + err.Error()})
		return
	}

	// Sanitize query
	input.Query = h.sanitizeInput(input.Query)

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"searchQuery":      input.Query,
		"userId":           getOrDefault(input.UserID, claims.UserID),
		"sessionId":        claims.SessionID,
		"sourceSystem":     claims.SourceSystem,
		"subscriptionTier": claims.SubscriptionTier,
		"requestId":        h.getRequestID(c),
	}

	if input.Filters != nil {
		variables["rawFilters"] = input.Filters
	}

	response := h.startWorkflow(c.Request.Context(), "discovery", variables)
	c.JSON(http.StatusOK, response)
}

// ============================================================================
// USER MANAGEMENT WORKFLOWS
// ============================================================================

func (h *WorkflowHandler) StartProfileUpdate(c *gin.Context) {
	claims := middleware.ExtractClaims(c)
	if claims == nil || claims.UserID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "AUTH_REQUIRED", "message": "Not authenticated"})
		return
	}

	userID := claims.UserID
	contentType := c.GetHeader("Content-Type")

	var action string
	var profileData map[string]interface{}

	if strings.Contains(contentType, "multipart/form-data") {
		// Multipart form — used when photo upload is included
		if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse multipart form: " + err.Error()})
			return
		}

		action = c.PostForm("action")
		if action == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "action field is required"})
			return
		}

		// Parse profileData JSON from form field
		if pdStr := c.PostForm("profileData"); pdStr != "" {
			if err := json.Unmarshal([]byte(pdStr), &profileData); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid profileData JSON: " + err.Error()})
				return
			}
		}

		// Handle photo upload if present (only for update_personal action)
		if action == "update_personal" {
			file, _, err := c.Request.FormFile("photo")
			if err == nil {
				defer file.Close()

				// Read photo data
				photoBytes, err := io.ReadAll(io.LimitReader(file, 4*1024*1024))
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "PHOTO_READ_FAILED", "message": "Failed to read uploaded photo"})
					return
				}

				// Get photo config from S3 config
				photoConfig := middleware.PhotoSecurityConfig{
					MaxFileSize:  3145728, // 3MB default
					AllowedTypes: []string{"image/jpeg", "image/png", "image/webp"},
					MaxWidth:     200,
					MaxHeight:    200,
				}
				if h.config != nil && h.config.Integrations.AWS.S3.Enabled {
					if h.config.Integrations.AWS.S3.MaxFileSize > 0 {
						photoConfig.MaxFileSize = h.config.Integrations.AWS.S3.MaxFileSize
					}
					if len(h.config.Integrations.AWS.S3.AllowedTypes) > 0 {
						photoConfig.AllowedTypes = h.config.Integrations.AWS.S3.AllowedTypes
					}
					if h.config.Integrations.AWS.S3.MaxWidth > 0 {
						photoConfig.MaxWidth = h.config.Integrations.AWS.S3.MaxWidth
					}
					if h.config.Integrations.AWS.S3.MaxHeight > 0 {
						photoConfig.MaxHeight = h.config.Integrations.AWS.S3.MaxHeight
					}
				}

				// Validate photo security
				result := middleware.ValidatePhoto(photoBytes, photoConfig)
				if !result.Valid {
					c.JSON(http.StatusBadRequest, gin.H{
						"success": false,
						"error":   result.ErrorCode,
						"message": result.Error,
					})
					return
				}

				// Check if S3 is available
				if h.s3Client == nil {
					c.JSON(http.StatusServiceUnavailable, gin.H{
						"error":   "PHOTO_STORAGE_UNAVAILABLE",
						"message": "Photo upload is not available. S3 is not configured.",
					})
					return
				}

				// Upload to S3
				ext := extensionFromContentType(result.ContentType)
				key := awsutil.MakePhotoKey(userID, ext)

				uploadResult, err := h.s3Client.UploadPhoto(c.Request.Context(), key, result.ContentType, bytes.NewReader(photoBytes), int64(len(photoBytes)))
				if err != nil {
					h.logger.Error("S3 photo upload failed", map[string]interface{}{
						"error":  err.Error(),
						"userId": userID,
					})
					c.JSON(http.StatusInternalServerError, gin.H{"error": "PHOTO_UPLOAD_FAILED", "message": "Failed to upload profile photo"})
					return
				}

				// Inject photo URL into profileData
				if profileData == nil {
					profileData = make(map[string]interface{})
				}
				profileData["profile_image"] = uploadResult.URL

				h.logger.Info("Profile photo uploaded", map[string]interface{}{
					"userId": userID,
					"key":    uploadResult.Key,
					"url":    uploadResult.URL,
				})
			}
		}
	} else {
		// JSON body — standard workflow
		var input struct {
			Action      string                 `json:"action" binding:"required,oneof=retrieve update_personal update_professional update_company update_investment update_preferences delete_account"`
			ProfileData map[string]interface{} `json:"profileData"`
		}

		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		action = input.Action
		profileData = input.ProfileData
	}

	// delete_account requires no profileData
	if action == "delete_account" {
		profileData = nil
	}

	// Non-retrieve actions require profileData
	if action != "retrieve" && action != "delete_account" {
		if len(profileData) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "profileData is required for action: " + action})
			return
		}
	}

	// FLE: encrypt PII fields before sending to Camunda
	if h.fle != nil && h.fle.Enabled() && profileData != nil {
		if err := h.fle.EncryptProfileData(profileData); err != nil {
			h.logger.Error("FLE encryption failed", map[string]interface{}{
				"error":  err.Error(),
				"userId": userID,
			})
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to process profile data"})
			return
		}
	}

	correlationKey := uuid.New().String()
	ctx := c.Request.Context()

	// Subscribe BEFORE starting workflow (race condition prevention)
	channel := fmt.Sprintf("workflow:response:%s", correlationKey)
	pubsub := h.redisClient.Subscribe(ctx, channel)
	defer pubsub.Close()

	confirmCtx, confirmCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer confirmCancel()
	if _, err := pubsub.ReceiveTimeout(confirmCtx, 3*time.Second); err != nil {
		h.logger.Warn("Subscription confirm timeout, proceeding anyway", map[string]interface{}{
			"channel": channel,
			"error":   err.Error(),
		})
	}

	variables := map[string]interface{}{
		"userId":        userID,
		"action":        action,
		"profileData":   profileData,
		"correlationKey": correlationKey,
		"sessionId":     claims.SessionID,
		"sourceSystem":  claims.SourceSystem,
		"requestId":     h.getRequestID(c),
	}

	// Start workflow in background (non-blocking)
	go h.startWorkflow(ctx, "user-profile-update", variables)

	select {
	case msg := <-pubsub.Channel():
		var envelope workflow.WorkflowResponse
		if err := json.Unmarshal([]byte(msg.Payload), &envelope); err != nil {
			h.logger.Error("Failed to parse workflow response", map[string]interface{}{
				"error": err.Error(),
			})
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to parse response"})
			return
		}

		if envelope.Response == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Workflow returned nil response"})
			return
		}

		// FLE: decrypt PII fields in response
		if h.fle != nil && h.fle.Enabled() {
			if err := h.fle.DecryptProfileData(envelope.Response); err != nil {
				h.logger.Warn("FLE decryption failed on response", map[string]interface{}{
					"error": err.Error(),
				})
			}
		}

		c.JSON(http.StatusOK, envelope.Response)

	case <-time.After(30 * time.Second):
		h.logger.Warn("Profile update workflow timeout", map[string]interface{}{
			"correlationKey": correlationKey,
			"userId":         userID,
		})
		c.JSON(http.StatusGatewayTimeout, gin.H{
			"error":          "TIMEOUT",
			"message":        "Profile update timed out. Please try again.",
			"correlationKey": correlationKey,
		})

	case <-ctx.Done():
		c.JSON(http.StatusGatewayTimeout, gin.H{"error": "TIMEOUT", "message": "Request context cancelled"})
	}
}

// extensionFromContentType returns a file extension for the given MIME type.
func extensionFromContentType(ct string) string {
	switch ct {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	default:
		return "jpg"
	}
}

// GetWorkflowCompletionStatus checks if a workflow response is cached in Redis.
// GET /user/workflow/:workflowId
func (h *WorkflowHandler) GetWorkflowCompletionStatus(c *gin.Context) {
	workflowID := c.Param("workflowId")
	if workflowID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflowId is required"})
		return
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil || claims.UserID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "AUTH_REQUIRED", "message": "Not authenticated"})
		return
	}

	// Check Redis cache key (send-api-response worker stores backup with 2min TTL)
	cacheKey := fmt.Sprintf("workflow:response:cache:%s", workflowID)
	ctx := c.Request.Context()
	cached, err := h.redisClient.Get(ctx, cacheKey).Result()
	if err != nil {
		// Cache miss — workflow is still pending or expired
		c.JSON(http.StatusNotFound, gin.H{
			"success":   false,
			"status":    "pending",
			"message":   "Workflow result not available yet or expired",
			"workflowId": workflowID,
		})
		return
	}

	var envelope struct {
		Response     map[string]interface{} `json:"response"`
		CookieHeader string                 `json:"cookieHeader,omitempty"`
		RequestID    string                 `json:"requestId,omitempty"`
	}
	if err := json.Unmarshal([]byte(cached), &envelope); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to parse cached response"})
		return
	}

	// FLE: decrypt PII fields in cached response
	if h.fle != nil && h.fle.Enabled() && envelope.Response != nil {
		if err := h.fle.DecryptProfileData(envelope.Response); err != nil {
			h.logger.Warn("FLE decryption failed on cached response", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"status":    "completed",
		"data":      envelope.Response,
		"workflowId": workflowID,
	})
}

// GetPreferenceOptions returns the available options for user preferences from dropdowns config.
// GET /user/preferences/options
func (h *WorkflowHandler) GetPreferenceOptions(c *gin.Context) {
	if h.config.Dropdowns == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": map[string]interface{}{}})
		return
	}

	type Option struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}

	type PreferenceOptions struct {
		Theme           []Option `json:"theme"`
		Language        []Option `json:"language"`
		Timezone        []Option `json:"timezone"`
		Industry        []Option `json:"industry"`
		InvestmentRange []Option `json:"investmentRange"`
	}

	options := PreferenceOptions{}

	if dd, ok := h.config.Dropdowns.FindDropdownByName("theme"); ok {
		for _, opt := range dd {
			options.Theme = append(options.Theme, Option{Value: opt.Value, Label: opt.Label})
		}
	}
	if dd, ok := h.config.Dropdowns.FindDropdownByName("language"); ok {
		for _, opt := range dd {
			options.Language = append(options.Language, Option{Value: opt.Value, Label: opt.Label})
		}
	}
	if dd, ok := h.config.Dropdowns.FindDropdownByName("timezone"); ok {
		for _, opt := range dd {
			options.Timezone = append(options.Timezone, Option{Value: opt.Value, Label: opt.Label})
		}
	}
	if dd, ok := h.config.Dropdowns.FindDropdownByName("industry"); ok {
		for _, opt := range dd {
			options.Industry = append(options.Industry, Option{Value: opt.Value, Label: opt.Label})
		}
	}
	if dd, ok := h.config.Dropdowns.FindDropdownByName("investment_range"); ok {
		for _, opt := range dd {
			options.InvestmentRange = append(options.InvestmentRange, Option{Value: opt.Value, Label: opt.Label})
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": options})
}

func (h *WorkflowHandler) StartPasswordReset(c *gin.Context) {
	var input struct {
		Email string `json:"email" binding:"required,email"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate email
	if err := h.validateEmail(input.Email); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid email: " + err.Error()})
		return
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"email":        input.Email,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    h.getRequestID(c),
	}

	response := h.startWorkflow(c.Request.Context(), "password-reset-workflow", variables)
	c.JSON(http.StatusOK, response)
}

func (h *WorkflowHandler) StartAccountDeletion(c *gin.Context) {
	var input struct {
		UserID string `json:"userId"`
		Reason string `json:"reason"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"userId":       getOrDefault(input.UserID, claims.UserID),
		"reason":       input.Reason,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    h.getRequestID(c),
	}

	response := h.startWorkflow(c.Request.Context(), "account-deletion-workflow", variables)
	c.JSON(http.StatusOK, response)
}

// ============================================================================
// CONTACT US WORKFLOW
// ============================================================================

func (h *WorkflowHandler) StartContactUs(c *gin.Context) {
	var input struct {
		FirstName string `json:"firstName" binding:"required"`
		LastName  string `json:"lastName"`
		Email     string `json:"email" binding:"required,email"`
		Message   string `json:"message" binding:"required"`
		Company   string `json:"company"`
		Phone     string `json:"phone"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate inputs
	if err := h.validateString(input.FirstName, 1, 50); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid first name: " + err.Error()})
		return
	}
	if err := h.validateEmail(input.Email); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid email: " + err.Error()})
		return
	}
	if err := h.validateString(input.Message, 10, 5000); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid message: " + err.Error()})
		return
	}

	// Sanitize inputs
	input.FirstName = h.sanitizeInput(input.FirstName)
	input.LastName = h.sanitizeInput(input.LastName)
	input.Email = h.sanitizeInput(input.Email)
	input.Message = h.sanitizeInput(input.Message)
	input.Company = h.sanitizeInput(input.Company)
	input.Phone = h.sanitizeInput(input.Phone)

	reqID := uuid.New().String()

	// Format payload exactly as public forms expects it
	formData := map[string]interface{}{
		"firstName": input.FirstName,
		"lastName":  input.LastName,
		"email":     input.Email,
		"message":   input.Message,
		"company":   input.Company,
		"phone":     input.Phone,
		"ip":        c.ClientIP(),
	}

	variables := map[string]interface{}{
		"formType":        "contact_us",
		"formData":        formData,
		"requestId":       reqID,
		"correlationKey":  reqID,
		"operationsEmail": h.config.Integrations.Internal.OperationsAlertEmail,
		"operationsName":  h.config.Integrations.Internal.OperationsAlertName,
		"marketingEmail":  h.config.Integrations.Internal.MarketingAlertEmail,
		"marketingName":   h.config.Integrations.Internal.MarketingAlertName,
	}

	response := h.startWorkflow(c.Request.Context(), "public-form-submission", variables)
	c.JSON(http.StatusOK, response)
}

// ============================================================================
// PUBLIC FORM SUBMISSION WORKFLOW
// ============================================================================

func (h *WorkflowHandler) StartFormSubmission(c *gin.Context) {
	formType := c.Param("formType")
	if formType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Form type is required"})
		return
	}

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload: " + err.Error()})
		return
	}

	reqID := uuid.New().String()
	variables := map[string]interface{}{
		"formType":        formType,
		"formData":        payload,
		"requestId":       reqID,
		"correlationKey":  reqID,
		"operationsEmail": h.config.Integrations.Internal.OperationsAlertEmail,
		"operationsName":  h.config.Integrations.Internal.OperationsAlertName,
		"marketingEmail":  h.config.Integrations.Internal.MarketingAlertEmail,
		"marketingName":   h.config.Integrations.Internal.MarketingAlertName,
	}

	workflowID := "public-form-submission"
	if formType == "buyer-registration" {
		workflowID = "franchise-enquiry-submission"
		
		// Extract userId from context (set by session/cookie middleware)
		userID := c.GetString("userId")
		if userID != "" {
			variables["userId"] = userID
		}

		// Extract franchiseId from payload (required by enquiry workflow)
		if fid, ok := payload["franchiseId"].(string); ok {
			variables["franchiseId"] = fid
		}

		// Map formData to enquiryFormData for compatibility
		variables["enquiryFormData"] = payload
		variables["operation"] = "franchise_enquiry"
	}

	response := h.startWorkflow(c.Request.Context(), workflowID, variables)
	c.JSON(http.StatusOK, response)
}

// ============================================================================
// UNIFIED ONBOARDING WORKFLOW (V2)
// ============================================================================

func (h *WorkflowHandler) StartUnifiedOnboarding(c *gin.Context) {
	entityType := c.Param("entityType")
	et := strings.ToLower(strings.TrimSpace(entityType))
	switch et {
	case "franchise", "franchises":
		et = "franchise"
	case "association", "associations":
		et = "association"
	case "master-franchise", "master_franchises", "master franchises", "master franchise", "masterfranchise", "master_franchise":
		et = "master_franchise"
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid entity type. Must be 'franchise', 'association', or 'master_franchise'"})
		return
	}

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload: " + err.Error()})
		return
	}

	reqID := uuid.New().String()
	
	// Extract user ID if authenticated
	claims := middleware.ExtractClaims(c)
	userID := ""
	if claims != nil {
		userID = claims.UserID
	}

	// Check feature flag - forced to false as we don't have the admin panel to approve applications yet.
	useV2 := false
	
	var workflowID string
	var variables map[string]interface{}

	if useV2 {
		workflowID = "supplier-onboarding"
		variables = map[string]interface{}{
			"entityType":      et,
			"formData":        payload,
			"requestId":       reqID,
			"correlationKey":  reqID,
			"userId":          userID,
			"operationsEmail": h.config.Integrations.Internal.OperationsAlertEmail,
			"operationsName":  h.config.Integrations.Internal.OperationsAlertName,
			"marketingEmail":  h.config.Integrations.Internal.MarketingAlertEmail,
			"marketingName":   h.config.Integrations.Internal.MarketingAlertName,
		}
	} else {
		// Fallback to legacy
		workflowID = "public-form-submission"
		variables = map[string]interface{}{
			"formType":        et + "_registration",
			"formData":        payload,
			"requestId":       reqID,
			"correlationKey":  reqID,
			"userId":          userID,
			"operationsEmail": h.config.Integrations.Internal.OperationsAlertEmail,
			"operationsName":  h.config.Integrations.Internal.OperationsAlertName,
			"marketingEmail":  h.config.Integrations.Internal.MarketingAlertEmail,
			"marketingName":   h.config.Integrations.Internal.MarketingAlertName,
		}
	}

	response := h.startWorkflow(c.Request.Context(), workflowID, variables)
	c.JSON(http.StatusOK, response)
}

// ============================================================================
// APPLICATION WORKFLOWS
// ============================================================================

func (h *WorkflowHandler) StartApplicationProcessing(c *gin.Context) {
	var input struct {
		FranchiseID     string                 `json:"franchiseId" binding:"required"`
		SeekerID        string                 `json:"seekerId"`
		ApplicationData map[string]interface{} `json:"applicationData" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate franchise ID
	if err := h.validateUUID(input.FranchiseID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid franchise ID: " + err.Error()})
		return
	}

	// Validate seeker ID if provided
	if input.SeekerID != "" {
		if err := h.validateUUID(input.SeekerID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid seeker ID: " + err.Error()})
			return
		}
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"franchiseId":     input.FranchiseID,
		"seekerId":        getOrDefault(input.SeekerID, claims.UserID),
		"applicationData": input.ApplicationData,
		"sessionId":       claims.SessionID,
		"sourceSystem":    claims.SourceSystem,
		"requestId":       h.getRequestID(c),
	}

	response := h.startWorkflow(c.Request.Context(), "application_processing", variables)
	c.JSON(http.StatusOK, response)
}

func (h *WorkflowHandler) StartActivityApproval(c *gin.Context) {
	var input struct {
		ActivityID   string                 `json:"activityId" binding:"required"`
		ActivityType string                 `json:"activityType" binding:"required"`
		ApproverID   string                 `json:"approverId"`
		Data         map[string]interface{} `json:"data"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate activity ID
	if err := h.validateUUID(input.ActivityID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid activity ID: " + err.Error()})
		return
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"activityId":   input.ActivityID,
		"activityType": input.ActivityType,
		"approverId":   getOrDefault(input.ApproverID, claims.UserID),
		"data":         input.Data,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    h.getRequestID(c),
	}

	response := h.startWorkflow(c.Request.Context(), "activity-approval-workflow", variables)
	c.JSON(http.StatusOK, response)
}

// ============================================================================
// FRANCHISE WORKFLOWS
// ============================================================================

func (h *WorkflowHandler) StartFranchiseSearch(c *gin.Context) {
	var input struct {
		Query   string                 `json:"query"`
		Filters map[string]interface{} `json:"filters"`
		UserID  string                 `json:"userId"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Sanitize query if provided
	if input.Query != "" {
		input.Query = h.sanitizeInput(input.Query)
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"searchQuery":      input.Query,
		"rawFilters":       input.Filters,
		"entityType":       c.Param("entityType"),
		"userId":           getOrDefault(input.UserID, claims.UserID),
		"sessionId":        claims.SessionID,
		"sourceSystem":     claims.SourceSystem,
		"subscriptionTier": claims.SubscriptionTier,
		"requestId":        h.getRequestID(c),
	}

	response := h.startWorkflow(c.Request.Context(), "franchise_detail", variables)
	c.JSON(http.StatusOK, response)
}

func (h *WorkflowHandler) GetFranchiseDetails(c *gin.Context) {
	franchiseID := c.Param("id")
	if franchiseID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "franchise ID required"})
		return
	}

	// Validate franchise ID
	if err := h.validateUUID(franchiseID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid franchise ID: " + err.Error()})
		return
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"franchiseId":  franchiseID,
		"entityType":   c.Param("entityType"),
		"userId":       claims.UserID,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    h.getRequestID(c),
	}

	response := h.startWorkflow(c.Request.Context(), "franchise_detail", variables)
	c.JSON(http.StatusOK, response)
}

// ============================================================================
// FRANCHISE CRUD WORKFLOWS
// ============================================================================

// CreateFranchise starts a workflow to create a new franchise
func (h *WorkflowHandler) CreateFranchise(c *gin.Context) {
	var input struct {
		Name          string `json:"name" binding:"required"`
		Slug          string `json:"slug" binding:"required"`
		FoundedYear   *int16 `json:"founded_year,omitempty"`
		TrustedSeller bool   `json:"trusted_seller"`
		TotalOutlets  int    `json:"total_outlets"`
		OutletRange   string `json:"outlet_range,omitempty"`
		Industry      string `json:"industry,omitempty"`
		ParentCompany string `json:"parent_company,omitempty"`
		BusinessType  string `json:"business_type,omitempty"`
		LeaderName    string `json:"leader_name,omitempty"`
		LeaderRole    string `json:"leader_role,omitempty"`
		ContactEmail  string `json:"contact_email,omitempty"`
		Description   string `json:"description,omitempty"`
		InstagramURL  string `json:"instagram_url,omitempty"`
		FacebookURL   string `json:"facebook_url,omitempty"`
		TwitterURL    string `json:"twitter_url,omitempty"`
		LinkedinURL   string `json:"linkedin_url,omitempty"`
	}

	// Get claims from JWT
	claims := middleware.ExtractClaims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// ===== VALIDATION =====
	// Validate Name
	if err := h.validateString(input.Name, 2, 100); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid name: " + err.Error()})
		return
	}

	// Validate Slug
	if err := h.validateSlug(input.Slug); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid slug: " + err.Error()})
		return
	}

	// Validate Email if provided
	if input.ContactEmail != "" {
		if err := h.validateEmail(input.ContactEmail); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contact email: " + err.Error()})
			return
		}
	}

	// Validate URLs if provided
	if input.InstagramURL != "" {
		if err := h.validateURL(input.InstagramURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Instagram URL: " + err.Error()})
			return
		}
	}

	if input.FacebookURL != "" {
		if err := h.validateURL(input.FacebookURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Facebook URL: " + err.Error()})
			return
		}
	}

	if input.TwitterURL != "" {
		if err := h.validateURL(input.TwitterURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Twitter URL: " + err.Error()})
			return
		}
	}

	if input.LinkedinURL != "" {
		if err := h.validateURL(input.LinkedinURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid LinkedIn URL: " + err.Error()})
			return
		}
	}

	// Validate founded year if provided
	if input.FoundedYear != nil {
		if *input.FoundedYear < 1900 || *input.FoundedYear > int16(time.Now().Year()) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid founded year"})
			return
		}
	}

	// Validate total outlets
	if input.TotalOutlets < 0 || input.TotalOutlets > 100000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Total outlets must be between 0 and 100,000"})
		return
	}

	// Sanitize inputs
	input.Name = h.sanitizeInput(input.Name)
	input.Industry = h.sanitizeInput(input.Industry)
	input.ParentCompany = h.sanitizeInput(input.ParentCompany)
	input.BusinessType = h.sanitizeInput(input.BusinessType)
	input.LeaderName = h.sanitizeInput(input.LeaderName)
	input.LeaderRole = h.sanitizeInput(input.LeaderRole)
	input.Description = h.sanitizeInput(input.Description)
	input.OutletRange = h.sanitizeInput(input.OutletRange)

	// Prepare variables for Camunda workflow
	variables := map[string]interface{}{
		"operation_type": "CREATE_FRANCHISE",
		"name":           input.Name,
		"slug":           input.Slug,
		"founded_year":   input.FoundedYear,
		"trusted_seller": input.TrustedSeller,
		"total_outlets":  input.TotalOutlets,
		"outlet_range":   input.OutletRange,
		"industry":       input.Industry,
		"parent_company": input.ParentCompany,
		"business_type":  input.BusinessType,
		"leader_name":    input.LeaderName,
		"leader_role":    input.LeaderRole,
		"contact_email":  input.ContactEmail,
		"description":    input.Description,
		"instagram_url":  input.InstagramURL,
		"facebook_url":   input.FacebookURL,
		"twitter_url":    input.TwitterURL,
		"linkedin_url":   input.LinkedinURL,
		"created_by":     claims.UserID,
		"request_id":     uuid.New().String(),
	}

	// Start the Camunda workflow
	response := h.startWorkflow(c.Request.Context(), "franchise-postgres-process", variables)

	if response.Status == "failed" {
		h.logger.Error("Failed to start franchise creation workflow", map[string]interface{}{
			"error": response.Message,
		})
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to start franchise creation workflow",
			"message": response.Message,
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":               "Franchise creation initiated",
		"workflow_instance_key": response.WorkflowInstanceKey,
		"request_id":            response.RequestID,
		"process_id":            response.ProcessID,
		"slug":                  input.Slug,
	})
}

// UpdateFranchise starts a workflow to update an existing franchise (admin only)
func (h *WorkflowHandler) UpdateFranchise(c *gin.Context) {
	franchiseID := c.Param("id")
	if franchiseID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Franchise ID is required"})
		return
	}

	// Validate franchise ID
	if err := h.validateUUID(franchiseID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid franchise ID: " + err.Error()})
		return
	}

	// Check admin role from JWT
	claims := middleware.ExtractClaims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	// Verify admin role - check if "SYSTEM_ADMIN" or "ADMIN" is in the Roles array
	isAdmin := false
	for _, role := range claims.Roles {
		if role == "SYSTEM_ADMIN" || role == "ADMIN" {
			isAdmin = true
			break
		}
	}

	if !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin privileges required"})
		return
	}

	var input struct {
		Name          *string `json:"name,omitempty"`
		FoundedYear   *int16  `json:"founded_year,omitempty"`
		TrustedSeller *bool   `json:"trusted_seller,omitempty"`
		TotalOutlets  *int    `json:"total_outlets,omitempty"`
		Industry      *string `json:"industry,omitempty"`
		ContactEmail  *string `json:"contact_email,omitempty"`
		Description   *string `json:"description,omitempty"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate email if provided
	if input.ContactEmail != nil && *input.ContactEmail != "" {
		if err := h.validateEmail(*input.ContactEmail); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid email format: " + err.Error()})
			return
		}
	}

	// Validate name if provided
	if input.Name != nil && *input.Name != "" {
		if err := h.validateString(*input.Name, 2, 100); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid name: " + err.Error()})
			return
		}
	}

	// Validate founded year if provided
	if input.FoundedYear != nil {
		if *input.FoundedYear < 1900 || *input.FoundedYear > int16(time.Now().Year()) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid founded year"})
			return
		}
	}

	// Validate total outlets if provided
	if input.TotalOutlets != nil {
		if *input.TotalOutlets < 0 || *input.TotalOutlets > 100000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Total outlets must be between 0 and 100,000"})
			return
		}
	}

	// Sanitize inputs if provided
	if input.Name != nil && *input.Name != "" {
		sanitizedName := h.sanitizeInput(*input.Name)
		input.Name = &sanitizedName
	}

	if input.Industry != nil && *input.Industry != "" {
		sanitizedIndustry := h.sanitizeInput(*input.Industry)
		input.Industry = &sanitizedIndustry
	}

	if input.Description != nil && *input.Description != "" {
		sanitizedDescription := h.sanitizeInput(*input.Description)
		input.Description = &sanitizedDescription
	}

	// Prepare variables for Camunda workflow
	variables := map[string]interface{}{
		"operation_type": "UPDATE_FRANCHISE",
		"franchise_id":   franchiseID,
		"updated_by":     claims.UserID,
		"request_id":     uuid.New().String(),
	}

	// Add optional fields if provided
	if input.Name != nil {
		variables["name"] = *input.Name
	}
	if input.FoundedYear != nil {
		variables["founded_year"] = *input.FoundedYear
	}
	if input.TrustedSeller != nil {
		variables["trusted_seller"] = *input.TrustedSeller
	}
	if input.TotalOutlets != nil {
		variables["total_outlets"] = *input.TotalOutlets
	}
	if input.Industry != nil {
		variables["industry"] = *input.Industry
	}
	if input.ContactEmail != nil {
		variables["contact_email"] = *input.ContactEmail
	}
	if input.Description != nil {
		variables["description"] = *input.Description
	}

	// Start the Camunda workflow
	response := h.startWorkflow(c.Request.Context(), "franchise-postgres-process", variables)

	if response.Status == "failed" {
		h.logger.Error("Failed to start franchise update workflow", map[string]interface{}{
			"error": response.Message,
		})
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to start franchise update workflow",
			"message": response.Message,
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":               "Franchise update initiated",
		"workflow_instance_key": response.WorkflowInstanceKey,
		"request_id":            response.RequestID,
		"process_id":            response.ProcessID,
		"franchise_id":          franchiseID,
	})
}

// DeleteFranchise starts a workflow to delete a franchise (admin only)
func (h *WorkflowHandler) DeleteFranchise(c *gin.Context) {
	franchiseID := c.Param("id")
	if franchiseID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Franchise ID is required"})
		return
	}

	// Validate franchise ID
	if err := h.validateUUID(franchiseID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid franchise ID: " + err.Error()})
		return
	}

	// Check admin role from JWT
	claims := middleware.ExtractClaims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	// Verify admin role - check if "SYSTEM_ADMIN" or "ADMIN" is in the Roles array
	isAdmin := false
	for _, role := range claims.Roles {
		if role == "SYSTEM_ADMIN" || role == "ADMIN" {
			isAdmin = true
			break
		}
	}

	if !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin privileges required"})
		return
	}

	// Prepare variables for Camunda workflow
	variables := map[string]interface{}{
		"operation_type": "DELETE_FRANCHISE",
		"franchise_id":   franchiseID,
		"updated_by":     claims.UserID,
		"request_id":     uuid.New().String(),
	}

	// Start the Camunda workflow
	response := h.startWorkflow(c.Request.Context(), "franchise-postgres-process", variables)

	if response.Status == "failed" {
		h.logger.Error("Failed to start franchise deletion workflow", map[string]interface{}{
			"error": response.Message,
		})
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to start franchise deletion workflow",
			"message": response.Message,
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":               "Franchise deletion initiated",
		"workflow_instance_key": response.WorkflowInstanceKey,
		"request_id":            response.RequestID,
		"process_id":            response.ProcessID,
		"franchise_id":          franchiseID,
	})
}

// GetFullFranchise starts a workflow to get a franchise with all related data
func (h *WorkflowHandler) GetFullFranchise(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Franchise slug is required"})
		return
	}

	// Validate slug
	if err := h.validateSlug(slug); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid franchise slug: " + err.Error()})
		return
	}

	// Get claims from JWT (optional - for personalized results)
	claims := middleware.ExtractClaims(c)
	userID := ""
	if claims != nil {
		userID = claims.UserID
	}

	// Prepare variables for Camunda workflow
	variables := map[string]interface{}{
		"operation_type": "GET_FULL_FRANCHISE",
		"slug":           slug,
		"user_id":        userID,
		"request_id":     uuid.New().String(),
	}

	// Start the Camunda workflow
	response := h.startWorkflow(c.Request.Context(), "franchise-postgres-process", variables)

	if response.Status == "failed" {
		h.logger.Error("Failed to start full franchise query workflow", map[string]interface{}{
			"error": response.Message,
		})
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to start franchise query",
			"message": response.Message,
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":               "Franchise query initiated",
		"workflow_instance_key": response.WorkflowInstanceKey,
		"request_id":            response.RequestID,
		"process_id":            response.ProcessID,
		"slug":                  slug,
	})
}

// AUTHENTICATION WORKFLOWS
// ============================================================================

func (h *WorkflowHandler) StartKeycloakLogin(c *gin.Context) {
	var input struct {
		Code        string                 `json:"code"`
		State       string                 `json:"state"`
		Provider    string                 `json:"provider"`
		RedirectURL string                 `json:"redirectUrl"`
		Metadata    map[string]interface{} `json:"metadata"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	userAgent := c.GetHeader("User-Agent")

	correlationKey := uuid.New().String()
	ctx := c.Request.Context()

	channel := fmt.Sprintf("workflow:response:%s", correlationKey)
	pubsub := h.redisClient.Subscribe(ctx, channel)
	defer pubsub.Close()

	// FIX 1: dedicated 3s timeout, non-fatal to allow workflow start
	confirmCtx, confirmCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer confirmCancel()
	if _, err := pubsub.ReceiveTimeout(confirmCtx, 3*time.Second); err != nil {
		h.logger.Warn("Subscription confirm timeout, proceeding anyway", map[string]interface{}{
			"channel": channel,
			"error":   err.Error(),
		})
	}

	variables := map[string]interface{}{
		"sessionId":            claims.SessionID,
		"sourceSystem":         claims.SourceSystem,
		"requestId":            h.getRequestID(c),
		"correlationKey":       correlationKey,
		"postLoginRedirectUri": h.config.Auth.Keycloak.PostLoginRedirectURI,
	}

	if input.Code != "" && input.State != "" {
		if err := h.validateString(input.Code, 1, 500); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid authorization code"})
			return
		}
		if err := h.validateString(input.State, 1, 500); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid state"})
			return
		}
		variables["action"] = "callback"
		variables["code"] = input.Code
		variables["state"] = input.State
		variables["provider"] = getOrDefault(input.Provider, "keycloak")
	} else {
		variables["action"] = "initiate"
		variables["provider"] = getOrDefault(input.Provider, "keycloak")
		variables["redirectUrl"] = input.RedirectURL
	}

	if input.Metadata != nil {
		variables["metadata"] = input.Metadata
	}

	// FIX 2: workflow BAAD mein start hoga subscribe confirm ke
	h.startWorkflow(ctx, "keycloak-login-workflow", variables)

	select {
	case msg := <-pubsub.Channel():
		// Parse new payload format: {"response": {...}, "cookieHeader": "..."}
		var envelope struct {
			Response     map[string]interface{} `json:"response"`
			CookieHeader string                 `json:"cookieHeader"`
		}
		if err := json.Unmarshal([]byte(msg.Payload), &envelope); err != nil {
			c.Error(apierrors.ErrInternalError)
			c.Abort()
			return
		}
		response := envelope.Response
		if response == nil {
			c.Error(apierrors.ErrInternalError)
			c.Abort()
			return
		}

		if authURL, ok := response["authorizationUrl"].(string); ok && authURL != "" {
			if strings.Contains(authURL, "?") {
				authURL += "&max_age=0"
			} else {
				authURL += "?max_age=0"
			}
			response["authorizationUrl"] = authURL
		}
		// Initiate flow - authorizationUrl return karo

		if sessionID, ok := response["sessionId"].(string); ok && sessionID != "" {
			h.completeLoginFlow(c, ctx, sessionID, userAgent, response)
			return
		}

		// Agar callback hai aur error aya (SESSION_EXPIRED etc.) toh
		// silently fresh login initiate karo - Amazon/Flipkart style
		isCallback := c.Query("code") != ""
		if isCallback {
			h.initiateFreshLogin(c) // Silent redirect on callback error
			return
		}
		c.JSON(http.StatusOK, response)

	case <-time.After(30 * time.Second):
		cacheKey := fmt.Sprintf("workflow:response:cache:%s", correlationKey)
		cached, err := h.redisClient.Get(ctx, cacheKey).Result()
		if err == nil {
			var envelope struct {
				Response     map[string]interface{} `json:"response"`
				CookieHeader string                 `json:"cookieHeader"`
			}
			if json.Unmarshal([]byte(cached), &envelope) == nil && envelope.Response != nil {
				// cookie fix: redundant with completeLoginFlow
				// if envelope.CookieHeader != "" {
				// 	c.Writer.Header().Add("Set-Cookie", envelope.CookieHeader)
				// }
				if sessionID, ok := envelope.Response["sessionId"].(string); ok && sessionID != "" {
					h.completeLoginFlow(c, ctx, sessionID, userAgent, envelope.Response)
					return
				}
				// Callback error - initiate fresh login
				isCallbackCache := c.Query("code") != ""
				if isCallbackCache {
					h.initiateFreshLogin(c) // Silent redirect on callback error
					return
				}
				if authURL, ok := envelope.Response["authorizationUrl"].(string); ok && authURL != "" {
					if strings.Contains(authURL, "?") {
						authURL += "&max_age=0"
					} else {
						authURL += "?max_age=0"
					}
					envelope.Response["authorizationUrl"] = authURL
				}
				c.JSON(http.StatusOK, envelope.Response)
				return
			}
		}
		h.logger.Warn("Keycloak login timeout", map[string]interface{}{"correlationKey": correlationKey})
		c.Error(apierrors.ErrTimeout)
		c.Abort()

	case <-ctx.Done():
		c.Error(apierrors.ErrInternalError)
		c.Abort()
	}
}

// ============================================================================
// ADMIN ENDPOINTS
// ============================================================================

func (h *WorkflowHandler) ListWorkflows(c *gin.Context) {
	// Implementation for listing active workflows
	c.JSON(http.StatusOK, gin.H{
		"message": "List workflows endpoint",
		"status":  "not_implemented",
	})
}

func (h *WorkflowHandler) GetWorkflowStatus(c *gin.Context) {
	workflowID := c.Param("id")
	// Implementation for getting workflow status
	c.JSON(http.StatusOK, gin.H{
		"workflowId": workflowID,
		"message":    "Get workflow status endpoint",
		"status":     "not_implemented",
	})
}

func (h *WorkflowHandler) CancelWorkflow(c *gin.Context) {
	workflowID := c.Param("id")
	// Implementation for canceling workflow
	c.JSON(http.StatusOK, gin.H{
		"workflowId": workflowID,
		"message":    "Cancel workflow endpoint",
		"status":     "not_implemented",
	})
}

// ============================================================================
// HELPER METHODS
// ============================================================================

func (h *WorkflowHandler) startWorkflow(ctx context.Context, processID string, variables map[string]interface{}) WorkflowResponse {
	timeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	requestID, _ := variables["requestId"].(string)

	if requestID == "" {
		requestID = uuid.New().String()
		variables["requestId"] = requestID
	}

	// SAFE: gin context extraction
	var ginCtx *gin.Context
	if g, ok := ctx.(*gin.Context); ok {
		ginCtx = g
	}

	if ginCtx != nil {
		traceID := ginCtx.GetString("traceId")
		spanID := ginCtx.GetString("spanId")

		if traceID != "" {
			variables["traceId"] = traceID
			variables["spanId"] = spanID
			variables["traceContext"] = map[string]string{
				"traceId": traceID,
				"spanId":  spanID,
			}
		}
	}

	// ===== IDEMPOTENCY =====
	if h.redisStore != nil {
		var idempotencyKey string

		if ginCtx != nil {
			if key := ginCtx.GetHeader("X-Idempotency-Key"); key != "" && h.keyGenerator.ValidateAPIKey(key) {
				idempotencyKey = key
			}
		}

		if idempotencyKey == "" {
			idempotencyKey = h.keyGenerator.GenerateWorkerKeyWithTimestamp(processID, variables, 1*time.Hour)
		}

		h.logger.Debug("Checking workflow idempotency", map[string]interface{}{
			"idempotencyKey": idempotencyKey,
			"processId":      processID,
		})

		cachedResponse, found, err := h.redisStore.CheckAPIRequest(timeout, idempotencyKey)
		if err != nil {
			h.logger.Warn("Idempotency check failed", map[string]interface{}{
				"error": err.Error(),
			})
		} else if found {
			return WorkflowResponse{
				WorkflowInstanceKey: int64(cachedResponse["workflowInstanceKey"].(float64)),
				ProcessID:           cachedResponse["processId"].(string),
				RequestID:           cachedResponse["requestId"].(string),
				Status:              cachedResponse["status"].(string),
				Message:             "Workflow already started (cached)",
			}
		}
	}

	h.logger.Info("Starting workflow", map[string]interface{}{
		"processId": processID,
		"requestId": requestID,
	})

	cmd := h.camunda.GetClient().NewCreateInstanceCommand().
		BPMNProcessId(processID).
		LatestVersion()

	cmdStep, err := cmd.VariablesFromMap(variables)
	if err != nil {
		return WorkflowResponse{
			ProcessID: processID,
			RequestID: requestID,
			Status:    "failed",
			Message:   fmt.Sprintf("Failed to set workflow variables: %v", err),
		}
	}

	result, err := cmdStep.Send(timeout)
	if err != nil {
		return WorkflowResponse{
			ProcessID: processID,
			RequestID: requestID,
			Status:    "failed",
			Message:   fmt.Sprintf("Failed to start workflow: %v", err),
		}
	}

	// ===== STORE RESULT =====
	if h.redisStore != nil {
		response := map[string]interface{}{
			"workflowInstanceKey": result.GetProcessInstanceKey(),
			"processId":           processID,
			"requestId":           requestID,
			"status":              "started",
		}

		var idempotencyKey string
		if ginCtx != nil {
			if key := ginCtx.GetHeader("X-Idempotency-Key"); key != "" && h.keyGenerator.ValidateAPIKey(key) {
				idempotencyKey = key
			}
		}

		if idempotencyKey == "" {
			idempotencyKey = h.keyGenerator.GenerateWorkerKeyWithTimestamp(processID, variables, 1*time.Hour)
		}

		_ = h.redisStore.StoreAPIRequest(timeout, idempotencyKey, response, 24*time.Hour)
	}

	return WorkflowResponse{
		WorkflowInstanceKey: result.GetProcessInstanceKey(),
		ProcessID:           processID,
		RequestID:           requestID,
		Status:              "started",
		Message:             "Workflow started successfully",
		Variables:           variables,
	}
}

func getOrDefault(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}

// Used for keycloak signin (with commented part)
func waitForRedisResponse(ctx context.Context, client *redis.Client, correlationKey string, timeout time.Duration) (map[string]interface{}, error) {
	channel := fmt.Sprintf("workflow:response:%s", correlationKey)

	pubsub := client.Subscribe(ctx, channel)
	defer pubsub.Close()

	confirmCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if _, err := pubsub.ReceiveTimeout(confirmCtx, 3*time.Second); err != nil {
		return nil, err
	}

	select {
	case msg := <-pubsub.Channel():
		var response map[string]interface{}
		if err := json.Unmarshal([]byte(msg.Payload), &response); err != nil {
			return nil, err
		}
		return response, nil

	case <-time.After(timeout):
		cacheKey := fmt.Sprintf("workflow:response:cache:%s", correlationKey)
		cached, err := client.Get(ctx, cacheKey).Result()
		if err == nil {
			var response map[string]interface{}
			if json.Unmarshal([]byte(cached), &response) == nil {
				return response, nil
			}
		}
		return nil, fmt.Errorf("timeout")

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *WorkflowHandler) completeLoginFlow(
	c *gin.Context,
	ctx context.Context,
	sessionID string,
	userAgent string,
	responsePayload map[string]interface{},
) {

	now := time.Now()

	// Step 1: clear old cookies
	domain := h.config.Auth.Session.CookieDomain
	if domain == "" {
		domain = ".lemici.com"
	}

	for _, name := range []string{"AUTH_SESSION_ID", "session_id"} {
		cookie := &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			Domain:   h.config.Auth.Session.CookieDomain,
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		}
		http.SetCookie(c.Writer, cookie)
	}

	// Step 2: set new cookie
	maxAge := h.config.Auth.Session.SessionTTL / 1000 // Convert ms to seconds
	if maxAge <= 0 {
		maxAge = 86400 // Default 24h
	}

	h.logger.Info("Setting session cookie", map[string]interface{}{
		"sessionId": sessionID,
		"name":      constants.SessionCookieName,
		"maxAge":    maxAge,
		"domain":    domain,
	})

	cookie := &http.Cookie{
		Name:     constants.SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		Domain:   h.config.Auth.Session.CookieDomain,
		MaxAge:   h.config.Auth.Session.SessionTTL / 1000,
		HttpOnly: h.config.Auth.Session.CookieHTTPOnly,
		Secure:   h.config.Auth.Session.CookieSecure,
		SameSite: http.SameSiteLaxMode, // Keep Lax for subdomain compatibility
	}
	http.SetCookie(c.Writer, cookie)

	// Headers
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")

	if h.redisClient != nil {
		store := session.NewRedisStore(h.redisClient)

		sess, err := store.Get(ctx, sessionID)
		if err != nil && err != redis.Nil {
			h.logger.Error("Failed to fetch session during login", map[string]interface{}{
				"sessionId": sessionID,
				"error":     err.Error(),
			})
		}

		if sess != nil {

			// Only set absolute expiry if not already set
			if sess.AbsoluteExpiresAt.IsZero() {
				sess.AbsoluteExpiresAt = now.Add(24 * time.Hour)
			}

			sess.CreatedAt = now
			sess.ExpiresAt = now.Add(30 * time.Minute)

			sess.UserAgent = userAgent
			sess.IP = c.ClientIP()

			// Assign tokens from payload to session for persistence
			if idToken, ok := responsePayload["idToken"].(string); ok && idToken != "" {
				sess.IDToken = idToken
			}
			if accessToken, ok := responsePayload["accessToken"].(string); ok && accessToken != "" {
				sess.AccessToken = accessToken
			}
			if refreshToken, ok := responsePayload["refreshToken"].(string); ok && refreshToken != "" {
				sess.RefreshToken = refreshToken
			}
			if kcUserID, ok := responsePayload["keycloakUserId"].(string); ok && kcUserID != "" {
				sess.KeycloakUserID = kcUserID
			}

			if err := store.Update(ctx, *sess); err != nil {
				h.logger.Error("Failed to update session during login", map[string]interface{}{
					"sessionId": sessionID,
					"error":     err.Error(),
				})
			}

			// Robust Keycloak identification (User ID + Session ID)
			keycloakUserID := ""
			keycloakSessionID := ""

			if sess.IDToken != "" {
				keycloakUserID, keycloakSessionID = extractClaimsFromJWT(sess.IDToken)
			}

			// Fallback to AccessToken if IDToken failed
			if keycloakUserID == "" && sess.AccessToken != "" {
				keycloakUserID, keycloakSessionID = extractClaimsFromJWT(sess.AccessToken)
			}

			if keycloakUserID != "" {
				h.logger.Info("Triggering targeted session cleanup", map[string]interface{}{
					"keycloakUserId":     keycloakUserID,
					"currentKeycloakSid": keycloakSessionID,
				})
				go h.revokeUserPreviousKeycloakSessions(context.Background(), keycloakUserID, keycloakSessionID)
			}
		} else {
			h.logger.Warn("Session not found during login flow", map[string]interface{}{
				"sessionId": sessionID,
			})
		}
	}

	h.logger.Info("Session initialized for user", map[string]interface{}{
		"sessionId": sessionID,
		"userAgent": userAgent,
		"ip":        c.ClientIP(),
	})

	// Compile final response map
	result := gin.H{
		"success":   true,
		"sessionId": sessionID,
		"message":   "Login complete",
	}

	for k, v := range responsePayload {
		if _, exists := result[k]; !exists {
			result[k] = v
		}
	}

	// ============================================================================
	// Callback Handling - 302 Redirect to Frontend
	// ============================================================================

	isCallback := c.Query("code") != ""

	if isCallback {
		redirectURL := h.config.Auth.Keycloak.PostLoginRedirectURI

		// Prefer runtime redirectUrl from gin.Context (set by HandleKeycloakCallback)
		if runtimeURL, exists := c.Get("redirectUrl"); exists {
			if rURL, ok := runtimeURL.(string); ok && rURL != "" {
				if isAllowedRedirectDomain(rURL, h.config.Auth.Keycloak.AllowedRedirectDomains) {
					redirectURL = rURL
				}
			}
		}

		if redirectURL == "" {
			redirectURL = "https://lemici.com/"
		}
		h.logger.Info("Redirecting to frontend after successful login", map[string]interface{}{
			"sessionId":   sessionID,
			"redirectUrl": redirectURL,
		})
		c.Redirect(http.StatusFound, redirectURL)
		return
	}

	c.JSON(http.StatusOK, result)

}

func isAllowedRedirectDomain(rawURL string, allowed []string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}

	hostname := parsed.Hostname()

	// Only https in real environments; localhost permitted for dev
	if parsed.Scheme != "https" && hostname != "localhost" {
		return false
	}

	for _, domain := range allowed {
		// "."+domain prevents evil-lemici.com from matching lemici.com
		if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
			return true
		}
	}
	return false
}

func (h *WorkflowHandler) HandleKeycloakCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	errParam := c.Query("error") // Keycloak error capture karo

	h.logger.Info("OAuth callback received", map[string]interface{}{
		"requestId": c.GetString("requestId"),
		"traceId":   c.GetString("traceId"),
		"hasCode":   code != "",
		"hasState":  state != "",
		"error":     errParam,
	})

	// No code = Keycloak error (session expired, auth failed, user cancelled)
	// â†’ Redirect directly to fresh login initiation
	// Skip bridge page here for login-page timeouts to avoid double-login frustration
	if code == "" || state == "" {
		h.logger.Warn("OAuth callback missing code/state â€” redirecting to fresh login", map[string]interface{}{
			"requestId": c.GetString("requestId"),
			"error":     errParam,
		})

		h.initiateFreshLogin(c) // Silent redirect
		return
	}

	// Read redirectUrl from the Redis session set during initiate
	// This is a non-destructive read â€” the Zeebe worker atomically GETDELs it for PKCE
	stateKey := fmt.Sprintf("oauth:state:%s", state)
	sessionRaw, err := h.redisClient.Get(c.Request.Context(), stateKey).Result()
	if err == nil {
		var session struct {
			RedirectURL string `json:"r"`
		}
		if json.Unmarshal([]byte(sessionRaw), &session) == nil && session.RedirectURL != "" {
			c.Set("redirectUrl", session.RedirectURL)
		}
	}

	// Reuse existing flow by simulating JSON input
	c.Request.Header.Set("Content-Type", "application/json")

	bodyBytes, _ := json.Marshal(map[string]string{
		"code":     code,
		"state":    state,
		"provider": "keycloak",
	})
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	h.StartKeycloakLogin(c)
}

// initiateFreshLogin starts a new login workflow and redirects the user
// directly to the Keycloak login page â€” Amazon/Flipkart style seamless re-login.
func (h *WorkflowHandler) initiateFreshLogin(c *gin.Context) {
	ctx := c.Request.Context()
	correlationKey := uuid.New().String()
	channel := fmt.Sprintf("workflow:response:%s", correlationKey)

	pubsub := h.redisClient.Subscribe(ctx, channel)
	defer pubsub.Close()

	// Confirm subscription using same pattern as StartKeycloakLogin
	confirmCtx, confirmCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer confirmCancel()
	if _, err := pubsub.ReceiveTimeout(confirmCtx, 3*time.Second); err != nil {
		// Redis unavailable â€” fall back to home page
		h.logger.Warn("initiateFreshLogin: Redis subscription confirm failed", map[string]interface{}{"err": err.Error()})
		c.Redirect(http.StatusFound, h.config.Auth.Keycloak.PostLoginRedirectURI)
		return
	}

	// Start fresh initiate workflow (same as /auth/login)
	variables := map[string]interface{}{
		"action":               "initiate",
		"provider":             "keycloak",
		"correlationKey":       correlationKey,
		"postLoginRedirectUri": h.config.Auth.Keycloak.PostLoginRedirectURI,
		"redirectUrl":          h.config.Auth.Keycloak.RedirectURL,
	}

	if _, err := h.camunda.StartProcessInstance(ctx, "keycloak-login-workflow", variables); err != nil {
		h.logger.Error("initiateFreshLogin: workflow start failed", map[string]interface{}{"err": err.Error()})
		c.Redirect(http.StatusFound, h.config.Auth.Keycloak.PostLoginRedirectURI)
		return
	}

	// Wait for auth URL from worker (max 10s)
	select {
	case msg := <-pubsub.Channel():
		var envelope struct {
			Response map[string]interface{} `json:"response"`
		}
		if err := json.Unmarshal([]byte(msg.Payload), &envelope); err == nil && envelope.Response != nil {
			if authURL, ok := envelope.Response["authorizationUrl"].(string); ok && authURL != "" {
				// NUCLEAR COOKIE DELETION (Internal Logout Bypass)
				// We forcefully clear Keycloak cookies from the browser to prevent "Account Collision".
				kcURL, err := url.Parse(h.config.Auth.Keycloak.URL)
				kcDomain := ""
				if err == nil {
					kcDomain = kcURL.Hostname()
				}

				kcPath := fmt.Sprintf("/realms/%s/", h.config.Auth.Keycloak.Realm)
				kcPathNoSlash := fmt.Sprintf("/realms/%s", h.config.Auth.Keycloak.Realm)

				cookieNames := []string{
					"KEYCLOAK_SESSION", "KEYCLOAK_IDENTITY",
					"KEYCLOAK_SESSION_LEGACY", "KEYCLOAK_IDENTITY_LEGACY",
					"KEYCLOAK_REMEMBER_ME", "KC_RESTART",
					"AUTH_SESSION_ID", "AUTH_SESSION_ID_LEGACY",
				}

				for _, name := range cookieNames {
					clearCookie := func(domain, path string) {
						http.SetCookie(c.Writer, &http.Cookie{
							Name:     name,
							Value:    "",
							Path:     path,
							Domain:   domain,
							MaxAge:   -1,
							Secure:   true,
							HttpOnly: true,
							SameSite: http.SameSiteNoneMode,
						})
					}
					clearCookie(kcDomain, kcPath)
					clearCookie(kcDomain, kcPathNoSlash)
					clearCookie(kcDomain, "/")
					clearCookie("", kcPath)
					clearCookie("", kcPathNoSlash)
					clearCookie("", "/")
				}

				// Force login screen by adding prompt=login and max_age=0
				if strings.Contains(authURL, "?") {
					authURL += "&prompt=login&max_age=0"
				} else {
					authURL += "?prompt=login&max_age=0"
				}

				// âœ… Timeout cookie set karo
				http.SetCookie(c.Writer, &http.Cookie{
					Name:     "session_timeout",
					Value:    "true",
					Path:     "/",
					MaxAge:   300,
					HttpOnly: false,
					Secure:   true,
					SameSite: http.SameSiteLaxMode,
				})

				// Silent redirect
				c.Redirect(http.StatusFound, authURL)
				return
			}
		}
	case <-time.After(10 * time.Second):
		h.logger.Warn("initiateFreshLogin: timed out waiting for auth URL", map[string]interface{}{"correlationKey": correlationKey})
	}

	// âœ… Fallback cookie
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "session_timeout",
		Value:    "true",
		Path:     "/",
		MaxAge:   300,
		HttpOnly: false,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	// Final fallback — home page (config driven, no hardcoding)
	c.Redirect(http.StatusFound, h.config.Auth.Keycloak.PostLoginRedirectURI)
}




func (h *WorkflowHandler) renderRedirectPage(c *gin.Context, redirectURL string, title string, message string) {
	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Timeout | LeMiCi</title>
    <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg: #030712; /* Very dark blue */
            --card-bg: rgba(15, 23, 42, 0.7); /* Slate 900 with transparency */
            --border: rgba(56, 189, 248, 0.2);
            --accent: #38bdf8;
            --text: #f8fafc;
            --muted: #94a3b8;
        }
        body {
            font-family: 'Outfit', sans-serif;
            background: radial-gradient(circle at center, #0f172a 0%%, var(--bg) 100%%);
            color: var(--text);
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
            margin: 0;
            overflow: hidden;
        }
        .container {
            width: 100%%;
            max-width: 460px;
            padding: 2rem;
            box-sizing: border-box;
        }
        .card {
            background: var(--card-bg);
            border: 1px solid var(--border);
            border-radius: 24px;
            padding: 3rem 2.5rem;
            text-align: center;
            backdrop-filter: blur(16px);
            -webkit-backdrop-filter: blur(16px);
            box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5), inset 0 1px 0 rgba(255, 255, 255, 0.05);
            position: relative;
            z-index: 10;
        }
        .icon-wrapper {
            width: 64px;
            height: 64px;
            margin: 0 auto 1.5rem;
            background: rgba(56, 189, 248, 0.1);
            border-radius: 50%%;
            display: flex;
            align-items: center;
            justify-content: center;
            border: 1px solid rgba(56, 189, 248, 0.2);
        }
        .icon-wrapper svg {
            width: 32px;
            height: 32px;
            color: var(--accent);
        }
        h1 {
            font-weight: 600;
            font-size: 1.75rem;
            margin: 0 0 1rem;
            color: var(--text);
            letter-spacing: -0.02em;
        }
        p {
            color: var(--muted);
            font-size: 1.05rem;
            line-height: 1.6;
            margin: 0 0 2rem;
        }
        .timer-text {
            color: var(--accent);
            font-weight: 600;
        }
        .btn {
            display: inline-block;
            width: 100%%;
            padding: 0.875rem 1.5rem;
            background: var(--accent);
            color: #030712;
            text-decoration: none;
            border-radius: 12px;
            font-weight: 600;
            font-size: 1rem;
            transition: all 0.2s ease;
            box-sizing: border-box;
        }
        .btn:hover {
            transform: translateY(-2px);
            box-shadow: 0 10px 15px -3px rgba(56, 189, 248, 0.3);
            background: #7dd3fc;
        }
        /* Background decorative elements */
        .glow {
            position: absolute;
            top: 50%%;
            left: 50%%;
            transform: translate(-50%%, -50%%);
            width: 400px;
            height: 400px;
            background: radial-gradient(circle, rgba(56, 189, 248, 0.1) 0%%, transparent 60%%);
            z-index: 1;
            pointer-events: none;
            border-radius: 50%%;
        }
    </style>
</head>
<body>
    <div class="glow"></div>
    <div class="container">
        <div class="card">
            <div class="icon-wrapper">
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
            </div>
            <h1>%s</h1>
            <p>%s<br><br>Redirecting in <span class="timer-text" id="countdown">4</span> seconds...</p>
            <a href="%s" class="btn">Login Again Now</a>
        </div>
    </div>
    <script>
        let timeLeft = 4;
        const countdownEl = document.getElementById('countdown');
        const redirectUrl = "%s";
        
        const timer = setInterval(() => {
            timeLeft -= 1;
            countdownEl.textContent = timeLeft;
            if (timeLeft <= 0) {
                clearInterval(timer);
                window.location.replace(redirectUrl);
            }
        }, 1000);
    </script>
</body>
</html>`, title, message, redirectURL, redirectURL)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

// extractClaimsFromJWT â€” JWT se sub (User UUID) aur sid (Session ID) nikalta hai
func extractClaimsFromJWT(token string) (string, string) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims struct {
		Sub string `json:"sub"`
		Sid string `json:"sid"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ""
	}
	return claims.Sub, claims.Sid
}

// revokeUserPreviousKeycloakSessions â€” successful login ke baad user ke purane sessions hatao
func (h *WorkflowHandler) revokeUserPreviousKeycloakSessions(ctx context.Context, keycloakUserID string, currentKeycloakSid string) {
	cfg := h.config.Auth.Keycloak

	adminToken := h.getKeycloakAdminToken()
	if adminToken == "" {
		h.logger.Error("revokeUserPreviousKeycloakSessions: failed to get admin token", nil)
		return
	}

	// User ke saare sessions lo
	sessionsURL := fmt.Sprintf("%s/admin/realms/%s/users/%s/sessions",
		cfg.URL, cfg.Realm, keycloakUserID)

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, sessionsURL, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		h.logger.Error("revokeUserPreviousKeycloakSessions: failed to fetch sessions from Keycloak", map[string]interface{}{
			"status": resp.StatusCode,
			"err":    err,
		})
		return
	}
	defer resp.Body.Close()

	var sessions []struct {
		ID    string `json:"id"`
		Start int64  `json:"start"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return
	}

	h.logger.Info("revokeUserPreviousKeycloakSessions: scanning sessions", map[string]interface{}{
		"keycloakUserId": keycloakUserID,
		"foundCount":     len(sessions),
		"currentSid":     currentKeycloakSid,
	})

	if len(sessions) == 0 {
		return
	}

	// Case A: sid available hai â€” delete everything EXCEPT sid
	if currentKeycloakSid != "" {
		for _, s := range sessions {
			if s.ID == currentKeycloakSid {
				continue // Current session ko mat chhuo
			}
			h.revokeSingleKeycloakSession(ctx, adminToken, s.ID)
		}
		return
	}

	// Case B: sid missing hai (fallback) â€” keep latest start time
	if len(sessions) <= 1 {
		return
	}

	latestIdx := 0
	for i, s := range sessions {
		if s.Start > sessions[latestIdx].Start {
			latestIdx = i
		}
	}

	for i, s := range sessions {
		if i == latestIdx {
			continue
		}
		h.revokeSingleKeycloakSession(ctx, adminToken, s.ID)
	}
}

// revokeSingleKeycloakSession â€” individual session deletion helper
func (h *WorkflowHandler) revokeSingleKeycloakSession(ctx context.Context, adminToken string, sessionID string) {
	cfg := h.config.Auth.Keycloak
	delURL := fmt.Sprintf("%s/admin/realms/%s/sessions/%s", cfg.URL, cfg.Realm, sessionID)

	delReq, _ := http.NewRequestWithContext(ctx, http.MethodDelete, delURL, nil)
	delReq.Header.Set("Authorization", "Bearer "+adminToken)

	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		h.logger.Warn("Failed to delete Keycloak session", map[string]interface{}{"sid": sessionID, "err": err})
		return
	}
	defer delResp.Body.Close()

	if delResp.StatusCode == http.StatusNoContent || delResp.StatusCode == http.StatusOK {
		h.logger.Info("Keycloak orphaned session revoked", map[string]interface{}{"sid": sessionID})
	} else {
		h.logger.Warn("Keycloak session revocation returned unexpected status", map[string]interface{}{"sid": sessionID, "status": delResp.StatusCode})
	}
}

// getKeycloakAdminToken â€” shared helper, admin credentials se token lao
func (h *WorkflowHandler) getKeycloakAdminToken() string {
	cfg := h.config.Auth.Keycloak

	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token",
		cfg.URL, cfg.Realm)

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", cfg.AdminClientID)
	data.Set("client_secret", cfg.AdminClientSecret)

	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		h.logger.Error("getKeycloakAdminToken: network error", map[string]interface{}{"error": err.Error()})
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		h.logger.Warn("getKeycloakAdminToken: auth rejected by Keycloak", map[string]interface{}{
			"status":   resp.StatusCode,
			"response": string(body),
			"clientId": cfg.AdminClientID,
		})
		return ""
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		h.logger.Error("getKeycloakAdminToken: decode failed", map[string]interface{}{"error": err.Error()})
		return ""
	}
	return result.AccessToken
}
