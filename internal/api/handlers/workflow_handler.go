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

	// Validate slug format
	if !isValidSlug(input.Slug) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid slug format. Use only lowercase letters, numbers, and hyphens"})
		return
	}

	// Validate email if provided
	if input.ContactEmail != "" && !isValidEmail(input.ContactEmail) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid email format"})
		return
	}

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
	if input.ContactEmail != nil && !isValidEmail(*input.ContactEmail) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid email format"})
		return
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

// ============================================================================
// VALIDATION HELPERS
// ============================================================================

func isValidSlug(slug string) bool {
	// Only lowercase letters, numbers, and hyphens
	for i := 0; i < len(slug); i++ {
		c := slug[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

func isValidEmail(email string) bool {
	// Simple email validation
	hasAt := false
	hasDot := false
	for i := 0; i < len(email); i++ {
		if email[i] == '@' {
			if hasAt {
				return false // Multiple @ symbols
			}
			hasAt = true
		} else if email[i] == '.' && hasAt {
			hasDot = true
		}
	}
	return hasAt && hasDot && len(email) > 3
}
