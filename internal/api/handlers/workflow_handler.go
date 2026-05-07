// internal/api/handlers/workflow_handler.go
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	apierrors "camunda-workers/internal/api/errors"
	"camunda-workers/internal/api/middleware"
	"camunda-workers/internal/common/auth/session"
	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/constants"
	"camunda-workers/internal/common/idempotency"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

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
}

func NewWorkflowHandler(camunda *camunda.Client, logger logger.Logger, redisClient *redis.Client, cfg *config.Config) *WorkflowHandler {
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
		"requestId":        uuid.New().String(),
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
		"requestId":        uuid.New().String(),
	}

	if input.Filters != nil {
		variables["rawFilters"] = input.Filters
	}

	response := h.startWorkflow(c.Request.Context(), "discovery", variables)
	c.JSON(http.StatusOK, response)
}

// ============================================================================
// AUTHENTICATION WORKFLOWS
// ============================================================================

func (h *WorkflowHandler) StartGoogleSignup(c *gin.Context) {
	var input struct {
		AuthCode    string                 `json:"authCode" binding:"required"`
		Email       string                 `json:"email" binding:"required,email"`
		RedirectURI string                 `json:"redirectUri"`
		FirstName   string                 `json:"firstName"`
		LastName    string                 `json:"lastName"`
		Metadata    map[string]interface{} `json:"metadata"`
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

	// Validate names if provided
	if input.FirstName != "" {
		if err := h.validateString(input.FirstName, 1, 100); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid first name: " + err.Error()})
			return
		}
		input.FirstName = h.sanitizeInput(input.FirstName)
	}

	if input.LastName != "" {
		if err := h.validateString(input.LastName, 1, 100); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid last name: " + err.Error()})
			return
		}
		input.LastName = h.sanitizeInput(input.LastName)
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"authCode":     input.AuthCode,
		"email":        input.Email,
		"redirectUri":  input.RedirectURI,
		"firstName":    input.FirstName,
		"lastName":     input.LastName,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"provider":     "google",
		"requestId":    uuid.New().String(),
	}

	if input.Metadata != nil {
		variables["metadata"] = input.Metadata
	}

	response := h.startWorkflow(c.Request.Context(), "user-signup-process", variables)
	c.JSON(http.StatusOK, response)
}

func (h *WorkflowHandler) StartLinkedInSignup(c *gin.Context) {
	var input struct {
		AuthCode    string                 `json:"authCode" binding:"required"`
		Email       string                 `json:"email" binding:"required,email"`
		RedirectURI string                 `json:"redirectUri"`
		FirstName   string                 `json:"firstName"`
		LastName    string                 `json:"lastName"`
		Metadata    map[string]interface{} `json:"metadata"`
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

	// Validate names if provided
	if input.FirstName != "" {
		if err := h.validateString(input.FirstName, 1, 100); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid first name: " + err.Error()})
			return
		}
		input.FirstName = h.sanitizeInput(input.FirstName)
	}

	if input.LastName != "" {
		if err := h.validateString(input.LastName, 1, 100); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid last name: " + err.Error()})
			return
		}
		input.LastName = h.sanitizeInput(input.LastName)
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"authCode":     input.AuthCode,
		"email":        input.Email,
		"redirectUri":  input.RedirectURI,
		"firstName":    input.FirstName,
		"lastName":     input.LastName,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"provider":     "linkedin",
		"requestId":    uuid.New().String(),
	}

	if input.Metadata != nil {
		variables["metadata"] = input.Metadata
	}

	response := h.startWorkflow(c.Request.Context(), "user-signup-process", variables)
	c.JSON(http.StatusOK, response)
}

func (h *WorkflowHandler) StartGoogleSignin(c *gin.Context) {
	var input struct {
		AuthCode    string                 `json:"authCode" binding:"required"`
		RedirectURI string                 `json:"redirectUri"`
		State       string                 `json:"state"`
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

	variables := map[string]interface{}{
		"authCode":     input.AuthCode,
		"redirectUri":  input.RedirectURI,
		"state":        input.State,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"provider":     "google",
		"requestId":    uuid.New().String(),
	}

	if input.Metadata != nil {
		variables["metadata"] = input.Metadata
	}

	response := h.startWorkflow(c.Request.Context(), "signin-workflow", variables)
	c.JSON(http.StatusOK, response)
}

func (h *WorkflowHandler) StartLinkedInSignin(c *gin.Context) {
	var input struct {
		AuthCode    string                 `json:"authCode" binding:"required"`
		RedirectURI string                 `json:"redirectUri"`
		State       string                 `json:"state"`
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

	variables := map[string]interface{}{
		"authCode":     input.AuthCode,
		"redirectUri":  input.RedirectURI,
		"state":        input.State,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"provider":     "linkedin",
		"requestId":    uuid.New().String(),
	}

	if input.Metadata != nil {
		variables["metadata"] = input.Metadata
	}

	response := h.startWorkflow(c.Request.Context(), "signin-workflow", variables)
	c.JSON(http.StatusOK, response)
}

func (h *WorkflowHandler) StartUserSignin(c *gin.Context) {
	var input struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
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

	// Validate password
	if err := h.validateString(input.Password, 8, 100); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid password: " + err.Error()})
		return
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"email":        input.Email,
		"password":     input.Password,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    uuid.New().String(),
	}

	response := h.startWorkflow(c.Request.Context(), "user-login-workflow", variables)
	c.JSON(http.StatusOK, response)
}







// ============================================================================
// USER MANAGEMENT WORKFLOWS
// ============================================================================

func (h *WorkflowHandler) StartUserSignup(c *gin.Context) {
	var input struct {
		Email     string `json:"email" binding:"required,email"`
		Password  string `json:"password" binding:"required,min=8"`
		FirstName string `json:"firstName" binding:"required"`
		LastName  string `json:"lastName" binding:"required"`
		Phone     string `json:"phone"`
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

	// Validate password
	if err := h.validateString(input.Password, 8, 100); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid password: " + err.Error()})
		return
	}

	// Validate names
	if err := h.validateString(input.FirstName, 1, 100); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid first name: " + err.Error()})
		return
	}

	if err := h.validateString(input.LastName, 1, 100); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid last name: " + err.Error()})
		return
	}

	// Validate phone if provided
	if input.Phone != "" {
		if err := h.validatePhone(input.Phone); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid phone number: " + err.Error()})
			return
		}
	}

	// Sanitize inputs
	input.FirstName = h.sanitizeInput(input.FirstName)
	input.LastName = h.sanitizeInput(input.LastName)
	input.Phone = h.sanitizeInput(input.Phone)

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"email":        input.Email,
		"password":     input.Password,
		"firstName":    input.FirstName,
		"lastName":     input.LastName,
		"phone":        input.Phone,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    uuid.New().String(),
	}

	response := h.startWorkflow(c.Request.Context(), "user-signup-process", variables)
	c.JSON(http.StatusOK, response)
}

func (h *WorkflowHandler) StartProfileUpdate(c *gin.Context) {
	var input struct {
		UserID      string                 `json:"userId"`
		ProfileData map[string]interface{} `json:"profileData" binding:"required"`
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
		"profileData":  input.ProfileData,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    uuid.New().String(),
	}

	response := h.startWorkflow(c.Request.Context(), "user-profile-update", variables)
	c.JSON(http.StatusOK, response)
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
		"requestId":    uuid.New().String(),
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
		"requestId":    uuid.New().String(),
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
		"formType":       "contact_us",
		"formData":       formData,
		"requestId":      reqID,
		"correlationKey": reqID,
		"operationsEmail": h.config.Integrations.Internal.OperationsAlertEmail,
		"operationsName":  h.config.Integrations.Internal.OperationsAlertName,
		"marketingEmail":   h.config.Integrations.Internal.MarketingAlertEmail,
		"marketingName":    h.config.Integrations.Internal.MarketingAlertName,
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
		"formType":       formType,
		"formData":       payload,
		"requestId":      reqID,
		"correlationKey": reqID,
		"operationsEmail": h.config.Integrations.Internal.OperationsAlertEmail,
		"operationsName":  h.config.Integrations.Internal.OperationsAlertName,
		"marketingEmail":   h.config.Integrations.Internal.MarketingAlertEmail,
		"marketingName":    h.config.Integrations.Internal.MarketingAlertName,
	}

	response := h.startWorkflow(c.Request.Context(), "public-form-submission", variables)
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
		"requestId":       uuid.New().String(),
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
		"requestId":    uuid.New().String(),
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
		"userId":           getOrDefault(input.UserID, claims.UserID),
		"sessionId":        claims.SessionID,
		"sourceSystem":     claims.SourceSystem,
		"subscriptionTier": claims.SubscriptionTier,
		"requestId":        uuid.New().String(),
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
		"userId":       claims.UserID,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    uuid.New().String(),
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

// ============================================================================
// CRM WORKFLOWS
// ============================================================================

func (h *WorkflowHandler) StartCRMSync(c *gin.Context) {
	var input struct {
		UserID   string                 `json:"userId"`
		SyncType string                 `json:"syncType" binding:"required"`
		Data     map[string]interface{} `json:"data"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate sync type
	if err := h.validateString(input.SyncType, 1, 50); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid sync type: " + err.Error()})
		return
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"userId":       getOrDefault(input.UserID, claims.UserID),
		"syncType":     input.SyncType,
		"data":         input.Data,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    uuid.New().String(),
	}

	response := h.startWorkflow(c.Request.Context(), "crm-user-sync", variables)
	c.JSON(http.StatusOK, response)
}

// ============================================================================
// EMAIL WORKFLOWS
// ============================================================================

func (h *WorkflowHandler) StartEmailCampaign(c *gin.Context) {
	var input struct {
		CampaignID string   `json:"campaignId" binding:"required"`
		Recipients []string `json:"recipients" binding:"required"`
		TemplateID string   `json:"templateId" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate campaign ID
	if err := h.validateUUID(input.CampaignID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid campaign ID: " + err.Error()})
		return
	}

	// Validate template ID
	if err := h.validateUUID(input.TemplateID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid template ID: " + err.Error()})
		return
	}

	// Validate recipients
	if len(input.Recipients) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "At least one recipient is required"})
		return
	}

	for i, recipient := range input.Recipients {
		if err := h.validateEmail(recipient); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid recipient at position %d: %s", i, err.Error())})
			return
		}
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"campaignId":   input.CampaignID,
		"recipients":   input.Recipients,
		"templateId":   input.TemplateID,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    uuid.New().String(),
	}

	response := h.startWorkflow(c.Request.Context(), "email-campaign-workflow", variables)
	c.JSON(http.StatusOK, response)
}

func (h *WorkflowHandler) StartWelcomeSeries(c *gin.Context) {
	var input struct {
		UserID string `json:"userId"`
		Email  string `json:"email" binding:"required,email"`
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
		"userId":       getOrDefault(input.UserID, claims.UserID),
		"email":        input.Email,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    uuid.New().String(),
	}

	response := h.startWorkflow(c.Request.Context(), "welcome-email-series", variables)
	c.JSON(http.StatusOK, response)
}

// ============================================================================
// SOCIAL AUTH WORKFLOWS
// ============================================================================

func (h *WorkflowHandler) StartSocialAuthOrchestration(c *gin.Context) {
	var input struct {
		Provider    string                 `json:"provider" binding:"required"`
		AuthCode    string                 `json:"authCode" binding:"required"`
		RedirectURI string                 `json:"redirectUri"`
		Metadata    map[string]interface{} `json:"metadata"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate provider
	validProviders := map[string]bool{
		"google":   true,
		"linkedin": true,
		"facebook": true,
		"github":   true,
	}

	if !validProviders[input.Provider] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid provider. Must be one of: google, linkedin, facebook, github"})
		return
	}

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"provider":     input.Provider,
		"authCode":     input.AuthCode,
		"redirectUri":  input.RedirectURI,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    uuid.New().String(),
	}

	if input.Metadata != nil {
		variables["metadata"] = input.Metadata
	}

	response := h.startWorkflow(c.Request.Context(), "social-auth-orchestration", variables)
	c.JSON(http.StatusOK, response)
}

// ============================================================================
// ERROR HANDLING WORKFLOW
// ============================================================================

func (h *WorkflowHandler) StartErrorHandling(c *gin.Context) {
	var input struct {
		ErrorCode    string                 `json:"errorCode" binding:"required"`
		ErrorMessage string                 `json:"errorMessage" binding:"required"`
		Context      map[string]interface{} `json:"context"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Sanitize error message
	input.ErrorMessage = h.sanitizeInput(input.ErrorMessage)

	claims := middleware.ExtractClaims(c)
	if claims == nil {
		claims = &middleware.Claims{}
	}

	variables := map[string]interface{}{
		"errorCode":    input.ErrorCode,
		"errorMessage": input.ErrorMessage,
		"context":      input.Context,
		"sessionId":    claims.SessionID,
		"sourceSystem": claims.SourceSystem,
		"requestId":    uuid.New().String(),
	}

	response := h.startWorkflow(c.Request.Context(), "error-handling-workflow", variables)
	c.JSON(http.StatusOK, response)
}

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

	// FIX 1: dedicated 3s timeout, redirectToLogin nahi — 500 return
	confirmCtx, confirmCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer confirmCancel()
	if _, err := pubsub.ReceiveTimeout(confirmCtx, 3*time.Second); err != nil {
		c.Error(apierrors.ErrServiceUnavailable)
		c.Abort()
		return
	}

	variables := map[string]interface{}{
		"sessionId":            claims.SessionID,
		"sourceSystem":         claims.SourceSystem,
		"requestId":            uuid.New().String(),
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
		// Initiate flow — authorizationUrl return karo

		if sessionID, ok := response["sessionId"].(string); ok && sessionID != "" {
			h.completeLoginFlow(c, ctx, sessionID, userAgent, response)
			return
		}

		// Agar callback hai aur error aya (SESSION_EXPIRED etc.) toh
		// silently fresh login initiate karo - Amazon/Flipkart style
		isCallback := c.Query("code") != ""
		if isCallback {
			h.initiateFreshLogin(c)
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
					h.initiateFreshLogin(c)
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
	h.logger.Info("Setting session cookie", map[string]interface{}{
		"sessionId": sessionID,
		"name":      constants.SessionCookieName,
		"maxAge":    86400,
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

			if err := store.Update(ctx, *sess); err != nil {
				h.logger.Error("Failed to update session during login", map[string]interface{}{
					"sessionId": sessionID,
					"error":     err.Error(),
				})
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

func (h *WorkflowHandler) HandleKeycloakCallback(c *gin.Context) {

	code := c.Query("code")
	state := c.Query("state")

	h.logger.Info("OAuth callback received", map[string]interface{}{
		"requestId": c.GetString("requestId"),
		"traceId":   c.GetString("traceId"),
		"hasCode":   code != "",
		"hasState":  state != "",
	})

	// No code = Keycloak error (session expired, auth failed, user cancelled)
	// → silently start fresh login so user sees Keycloak login page again
	if code == "" || state == "" {
		h.logger.Warn("OAuth callback missing code/state — initiating fresh login", map[string]interface{}{
			"requestId": c.GetString("requestId"),
		})
		h.initiateFreshLogin(c)
		return
	}

	// Reuse existing flow by simulating JSON input
	c.Request.Header.Set("Content-Type", "application/json")

	body := fmt.Sprintf(`{"code":"%s","state":"%s","provider":"keycloak"}`, code, state)
	c.Request.Body = io.NopCloser(strings.NewReader(body))

	h.StartKeycloakLogin(c)
}

// initiateFreshLogin starts a new login workflow and redirects the user
// directly to the Keycloak login page — Amazon/Flipkart style seamless re-login.
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
		// Redis unavailable — fall back to home page
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
				// Add prompt=login so Keycloak shows fresh login page even if SSO session exists
				if strings.Contains(authURL, "?") {
					authURL += "&prompt=login"
				} else {
					authURL += "?prompt=login"
				}
				// Show bridge page with message before redirecting to fresh login
				h.renderRedirectPage(c, authURL, "Login Timeout", "Your session has expired for security. Redirecting you to the login page...")
				return
			}
		}
	case <-time.After(10 * time.Second):
		h.logger.Warn("initiateFreshLogin: timed out waiting for auth URL", map[string]interface{}{"correlationKey": correlationKey})
	}

	// Final fallback — home page (config driven, no hardcoding)
	c.Redirect(http.StatusFound, h.config.Auth.Keycloak.PostLoginRedirectURI)
}

// renderRedirectPage renders a premium, branded bridge page that informs the user
// of a session timeout before auto-redirecting to the fresh login page.
func (h *WorkflowHandler) renderRedirectPage(c *gin.Context, redirectURL string, title string, message string) {
	html := fmt.Sprintf(`
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Redirecting | LeMiCi</title>
    <meta http-equiv="refresh" content="3;url=%s">
    <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600&display=swap" rel="stylesheet">
    <style>
        :root { --bg: #050810; --card: #0c1220; --accent: #38bdf8; --text: #f8fafc; --muted: #94a3b8; }
        body { font-family: 'Outfit', sans-serif; background: var(--bg); color: var(--text); display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; overflow: hidden; }
        .container { position: relative; width: 100%%; max-width: 440px; padding: 2rem; }
        .card { background: var(--card); border: 1px solid rgba(56, 189, 248, 0.1); border-radius: 24px; padding: 3rem 2rem; text-align: center; backdrop-filter: blur(10px); box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5); position: relative; z-index: 10; }
        .glow { position: absolute; top: 50%%; left: 50%%; transform: translate(-50%%, -50%%); width: 300px; height: 300px; background: radial-gradient(circle, rgba(56, 189, 248, 0.15) 0%%, transparent 70%%); z-index: 1; pointer-events: none; }
        .loader { width: 48px; height: 48px; border: 3px solid rgba(56, 189, 248, 0.1); border-top-color: var(--accent); border-radius: 50%%; display: inline-block; animation: spin 1s cubic-bezier(0.55, 0.055, 0.675, 0.19) infinite; margin-bottom: 2rem; }
        @keyframes spin { to { transform: rotate(360deg); } }
        h1 { font-weight: 600; font-size: 1.5rem; margin: 0 0 0.75rem; color: var(--text); letter-spacing: -0.02em; }
        p { color: var(--muted); font-size: 0.95rem; line-height: 1.6; margin: 0 0 2rem; }
        .btn { display: inline-block; padding: 0.75rem 1.5rem; background: rgba(56, 189, 248, 0.1); color: var(--accent); text-decoration: none; border-radius: 12px; font-weight: 600; font-size: 0.875rem; border: 1px solid rgba(56, 189, 248, 0.2); transition: all 0.2s ease; }
        .btn:hover { background: var(--accent); color: var(--bg); transform: translateY(-2px); }
    </style>
</head>
<body>
    <div class="glow"></div>
    <div class="container">
        <div class="card">
            <div class="loader"></div>
            <h1>%s</h1>
            <p>%s</p>
            <a href="%s" class="btn">Login Again Now</a>
        </div>
    </div>
</body>
</html>`, redirectURL, title, message, redirectURL)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}
