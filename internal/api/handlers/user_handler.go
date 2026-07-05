// internal/api/handlers/user_handler.go
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"camunda-workers/internal/common/auth/session"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/logger"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type UserHandler struct {
	redisClient *redis.Client
	db          *sql.DB
	log         logger.Logger
	cfg         *config.Config
}

func NewUserHandler(redisClient *redis.Client, db *sql.DB, log logger.Logger, cfg *config.Config) *UserHandler {
	return &UserHandler{
		redisClient: redisClient,
		db:          db,
		log:         log,
		cfg:         cfg,
	}
}

type UserProfileResponse struct {
	Success   bool        `json:"success"`
	Data      UserProfile `json:"data"`
	Timestamp string      `json:"timestamp"`
}

type UserProfile struct {
	Name             string `json:"name"`
	Email            string `json:"email"`
	Phone            string `json:"phone"`
	SubscriptionTier string `json:"subscriptionTier"`
	IsActive         bool   `json:"isActive"`
}

func (h *UserHandler) GetProfile(c *gin.Context) {
	ctx := c.Request.Context()

	userID, exists := c.Get("userId")
	if !exists || userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "AUTH_REQUIRED",
			"message": "Not authenticated",
		})
		return
	}

	// 1. Fetch Fresh Data from Database
	var name, phone, email sql.NullString
	var status string
	err := h.db.QueryRowContext(ctx, `
		SELECT name, email, phone, status 
		FROM users 
		WHERE id = $1`, userID).Scan(&name, &email, &phone, &status)

	if err != nil {
		h.log.Error("Failed to fetch user profile from DB", map[string]interface{}{
			"userId": userID,
			"error":  err.Error(),
		})
		// Fallback for unexpected DB errors
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "INTERNAL_ERROR",
			"message": "Failed to retrieve profile",
		})
		return
	}

	// 2. Fetch Subscription Tier
	var subscriptionTier string
	_ = h.db.QueryRowContext(ctx, `
		SELECT tier FROM user_subscriptions 
		WHERE user_id = $1 AND is_valid = true 
		ORDER BY created_at DESC LIMIT 1`, userID).Scan(&subscriptionTier)
	if subscriptionTier == "" {
		subscriptionTier = "free"
	}

	c.JSON(http.StatusOK, UserProfileResponse{
		Success: true,
		Data: UserProfile{
			Name:             name.String,
			Email:            email.String,
			Phone:            phone.String,
			SubscriptionTier: subscriptionTier,
			IsActive:         status == "active",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *UserHandler) getSessionFromRedis(ctx context.Context, sessionID string) (*session.Session, error) {
	if sessionID == "" {
		return nil, nil
	}
	store := session.NewRedisStore(h.redisClient)
	sess, err := store.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (h *UserHandler) ValidateSession(c *gin.Context) {
	userID, exists := c.Get("userId")
	if !exists || userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"valid":   false,
			"error":   "AUTH_REQUIRED",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"valid":     true,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *UserHandler) CheckEmailExists(c *gin.Context) {
	email := c.Query("email")
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "EMAIL_REQUIRED",
			"message": "Email is required",
		})
		return
	}

	var exists bool
	err := h.db.QueryRowContext(c.Request.Context(), `
		SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(email) = LOWER($1))`, email).Scan(&exists)

	if err != nil {
		h.log.Error("Failed to check if email exists", map[string]interface{}{
			"email": email,
			"error": err.Error(),
		})
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "DB_ERROR",
			"message": "Internal server error checking email",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"exists":  exists,
	})
}

// ============================================================================
// USER PREFERENCES
// ============================================================================

func (h *UserHandler) GetPreferences(c *gin.Context) {
	userID, exists := c.Get("userId")
	if !exists || userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "AUTH_REQUIRED",
			"message": "Not authenticated",
		})
		return
	}

	ctx := c.Request.Context()

	type UserPreferences struct {
		Theme    string `json:"theme"`
		Language string `json:"language"`
		Timezone string `json:"timezone"`
	}

	var prefs UserPreferences
	err := h.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(theme, ''),
			COALESCE(language, ''),
			COALESCE(timezone, '')
		FROM user_preferences
		WHERE user_id = $1`, userID).Scan(&prefs.Theme, &prefs.Language, &prefs.Timezone)

	if err == sql.ErrNoRows {
		// Return defaults
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": UserPreferences{
				Theme:    "light",
				Language: "en",
				Timezone: "UTC",
			},
		})
		return
	}
	if err != nil {
		h.log.Error("Failed to fetch user preferences", map[string]interface{}{
			"userId": userID,
			"error":  err.Error(),
		})
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "DB_ERROR",
			"message": "Failed to retrieve preferences",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    prefs,
	})
}

// ============================================================================
// USER AUDIT LOG
// ============================================================================

func (h *UserHandler) GetAuditLog(c *gin.Context) {
	userID, exists := c.Get("userId")
	if !exists || userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "AUTH_REQUIRED",
			"message": "Not authenticated",
		})
		return
	}

	ctx := c.Request.Context()

	// Parse pagination params
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, operation_type, operation_status, changes, error_message, created_at
		FROM profile_audit_log
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		h.log.Error("Failed to fetch audit log", map[string]interface{}{
			"userId": userID,
			"error":  err.Error(),
		})
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "DB_ERROR",
			"message": "Failed to retrieve audit log",
		})
		return
	}
	defer rows.Close()

	type AuditEntry struct {
		ID              string          `json:"id"`
		OperationType   string          `json:"operationType"`
		OperationStatus string          `json:"operationStatus"`
		Changes         json.RawMessage `json:"changes"`
		ErrorMessage    string          `json:"errorMessage,omitempty"`
		CreatedAt       time.Time       `json:"createdAt"`
	}

	var entries []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		if err := rows.Scan(&entry.ID, &entry.OperationType, &entry.OperationStatus, &entry.Changes, &entry.ErrorMessage, &entry.CreatedAt); err != nil {
			h.log.Error("Failed to scan audit log entry", map[string]interface{}{"error": err.Error()})
			continue
		}
		entries = append(entries, entry)
	}

	// Get total count
	var total int
	_ = h.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM profile_audit_log WHERE user_id = $1`, userID).Scan(&total)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"entries": entries,
			"total":   total,
			"page":    page,
			"limit":   limit,
		},
	})
}

// ============================================================================
// USER FEEDBACK
// ============================================================================

func (h *UserHandler) SubmitFeedback(c *gin.Context) {
	userID, exists := c.Get("userId")
	if !exists || userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "AUTH_REQUIRED",
			"message": "Not authenticated",
		})
		return
	}

	var input struct {
		Type    string `json:"type" binding:"required,oneof=bug feature improvement general"`
		Message string `json:"message" binding:"required,min=10,max=5000"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	var feedbackID string
	err := h.db.QueryRowContext(ctx, `
		INSERT INTO feedback (user_id, type, message, status, created_at)
		VALUES ($1, $2, $3, 'pending', NOW())
		RETURNING id`, userID, input.Type, input.Message).Scan(&feedbackID)

	if err != nil {
		h.log.Error("Failed to insert feedback", map[string]interface{}{
			"userId": userID,
			"error":  err.Error(),
		})
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "DB_ERROR",
			"message": "Failed to submit feedback",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"feedbackId": feedbackID,
		"message":    "Feedback submitted successfully",
	})
}

// ============================================================================
// USER DASHBOARD
// ============================================================================

func (h *UserHandler) GetDashboard(c *gin.Context) {
	userID, exists := c.Get("userId")
	if !exists || userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "AUTH_REQUIRED",
			"message": "Not authenticated",
		})
		return
	}

	ctx := c.Request.Context()

	type DashboardData struct {
		Name             string   `json:"name"`
		Email            string   `json:"email"`
		Phone            string   `json:"phone"`
		SubscriptionTier string   `json:"subscriptionTier"`
		PlanLabel        string   `json:"planLabel"`
		Entitlements     []string `json:"entitlements"`
		ExpiresAt        *string  `json:"expiresAt,omitempty"`
		IsActive         bool     `json:"isActive"`
		Occupation       string   `json:"occupation,omitempty"`
		Designation      string   `json:"designation,omitempty"`
		Industry         string   `json:"industry,omitempty"`
		IsFranchisee     bool     `json:"isFranchisee"`
		PaymentHistory   []map[string]string `json:"paymentHistory"`
	}

	var data DashboardData

	// Fetch user profile
	err := h.db.QueryRowContext(ctx, `
		SELECT name, email, phone, status
		FROM users WHERE id = $1`, userID).Scan(
		&data.Name, &data.Email, &data.Phone, &data.IsActive,
	)
	if err != nil {
		h.log.Error("Failed to fetch dashboard user", map[string]interface{}{
			"userId": userID,
			"error":  err.Error(),
		})
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "DB_ERROR",
			"message": "Failed to retrieve dashboard",
		})
		return
	}

	// Fetch subscription tier + expiry
	var expiresAt sql.NullTime
	_ = h.db.QueryRowContext(ctx, `
		SELECT tier, expires_at FROM user_subscriptions
		WHERE user_id = $1 AND is_valid = true
		ORDER BY created_at DESC LIMIT 1`, userID).Scan(&data.SubscriptionTier, &expiresAt)
	if data.SubscriptionTier == "" {
		data.SubscriptionTier = "free"
	}
	if expiresAt.Valid {
		t := expiresAt.Time.Format(time.RFC3339)
		data.ExpiresAt = &t
	}

	// Load plan entitlements from config
	if h.cfg != nil && h.cfg.Dropdowns != nil {
		data.PlanLabel = h.cfg.Dropdowns.GetPlanLabel(data.SubscriptionTier)
		if entitlements, ok := h.cfg.Dropdowns.GetPlanEntitlements(data.SubscriptionTier); ok {
			data.Entitlements = entitlements
		}
	}
	if data.PlanLabel == "" {
		data.PlanLabel = data.SubscriptionTier
	}

	// Fetch professional info (correct table: user_professional_details)
	_ = h.db.QueryRowContext(ctx, `
		SELECT occupation, designation, industry
		FROM user_professional_details
		WHERE user_id = $1`, userID).Scan(&data.Occupation, &data.Designation, &data.Industry)

	// Check franchisee status (using created_by since owner_id doesn't exist in schema)
	_ = h.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM franchises WHERE created_by = $1 AND status != 'deleted')`, userID).Scan(&data.IsFranchisee)

	// Payment history placeholder
	data.PaymentHistory = []map[string]string{
		{"message": "Coming soon — payment history will be available after payment gateway integration."},
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

