// internal/api/handlers/workflow_handler.go
package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"camunda-workers/internal/api/middleware"
	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type WorkflowHandler struct {
	camunda *camunda.Client
	logger  logger.Logger
}

func NewWorkflowHandler(camunda *camunda.Client, logger logger.Logger) *WorkflowHandler {
	return &WorkflowHandler{
		camunda: camunda,
		logger:  logger,
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

func (h *WorkflowHandler) StartUserLogout(c *gin.Context) {
	var input struct {
		UserID    string                 `json:"userId"`
		Token     string                 `json:"token"`
		LogoutAll bool                   `json:"logoutAll"`
		DeviceID  string                 `json:"deviceId"`
		Reason    string                 `json:"reason"`
		Metadata  map[string]interface{} `json:"metadata"`
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
		"token":        input.Token,
		"sessionId":    claims.SessionID,
		"logoutAll":    input.LogoutAll,
		"deviceId":     input.DeviceID,
		"reason":       input.Reason,
		"sourceSystem": claims.SourceSystem,
		"requestId":    uuid.New().String(),
	}

	if input.Metadata != nil {
		variables["metadata"] = input.Metadata
	}

	response := h.startWorkflow(c.Request.Context(), "user-logout-workflow", variables)
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

	requestID := variables["requestId"].(string)

	h.logger.Info("Starting workflow", map[string]interface{}{
		"processId": processID,
		"requestId": requestID,
	})

	// Create the command
	cmd := h.camunda.GetClient().NewCreateInstanceCommand().
		BPMNProcessId(processID).
		LatestVersion()

	// Add variables and check for error
	cmdStep, err := cmd.VariablesFromMap(variables)
	if err != nil {
		h.logger.Error("Failed to set workflow variables", map[string]interface{}{
			"processId": processID,
			"requestId": requestID,
			"error":     err.Error(),
		})

		return WorkflowResponse{
			ProcessID: processID,
			RequestID: requestID,
			Status:    "failed",
			Message:   fmt.Sprintf("Failed to set workflow variables: %v", err),
		}
	}

	// Send the command
	result, err := cmdStep.Send(timeout)
	if err != nil {
		h.logger.Error("Failed to start workflow", map[string]interface{}{
			"processId": processID,
			"requestId": requestID,
			"error":     err.Error(),
		})

		return WorkflowResponse{
			ProcessID: processID,
			RequestID: requestID,
			Status:    "failed",
			Message:   fmt.Sprintf("Failed to start workflow: %v", err),
		}
	}

	h.logger.Info("Workflow started successfully", map[string]interface{}{
		"processId":           processID,
		"requestId":           requestID,
		"workflowInstanceKey": result.GetProcessInstanceKey(),
	})

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
