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
	"camunda-workers/internal/common/encryption"
	"camunda-workers/internal/common/logger"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type UserHandler struct {
	redisClient *redis.Client
	db          *sql.DB
	log         logger.Logger
	cfg         *config.Config
	fle         *encryption.FLEService
}

func NewUserHandler(redisClient *redis.Client, db *sql.DB, log logger.Logger, cfg *config.Config, fle *encryption.FLEService) *UserHandler {
	return &UserHandler{
		redisClient: redisClient,
		db:          db,
		log:         log,
		cfg:         cfg,
		fle:         fle,
	}
}

type UserProfileResponse struct {
	Success   bool        `json:"success"`
	Data      UserProfile `json:"data"`
	Timestamp string      `json:"timestamp"`
}

type UserProfile struct {
	ID                string                    `json:"id"`
	Email             string                    `json:"email"`
	EmailVerified     bool                      `json:"emailVerified"`
	Name              string                    `json:"name"`
	Phone             string                    `json:"phone"`
	Status            string                    `json:"status"`
	ProfileImage      string                    `json:"profileImage,omitempty"`
	Location          string                    `json:"location,omitempty"`
	Professional      *ProfessionalProfile      `json:"professional,omitempty"`
	Company           *CompanyProfile           `json:"company,omitempty"`
	Investment        *InvestmentProfile        `json:"investment,omitempty"`
	Preferences       *PreferencesProfile       `json:"preferences,omitempty"`
	Subscription      *SubscriptionProfile      `json:"subscription,omitempty"`
	CreatedAt         time.Time                 `json:"createdAt"`
	UpdatedAt         time.Time                 `json:"updatedAt"`
}

type ProfessionalProfile struct {
	Occupation      string `json:"occupation,omitempty"`
	Designation     string `json:"designation,omitempty"`
	Experience      string `json:"experience,omitempty"`
	PriorExperience bool   `json:"priorExperience"`
	IndustryID      string `json:"industryId,omitempty"`
	Industry        string `json:"industry,omitempty"`
}

type CompanyProfile struct {
	BusinessName       string `json:"businessName,omitempty"`
	BusinessType       string `json:"businessType,omitempty"`
	IndustrySector     string `json:"industrySector,omitempty"`
	YearEstablished    int64  `json:"yearEstablished,omitempty"`
	CINRegistration    string `json:"cinRegistration,omitempty"`
	GSTNumber          string `json:"gstNumber,omitempty"`
	AnnualTurnover     string `json:"annualTurnover,omitempty"`
	CompanyWebsite     string `json:"companyWebsite,omitempty"`
	CompanyPhone       string `json:"companyPhone,omitempty"`
	RegisteredAddress  string `json:"registeredAddress,omitempty"`
	CompanyDescription string `json:"companyDescription,omitempty"`
}

type InvestmentProfile struct {
	MinInvestment          string   `json:"minInvestment,omitempty"`
	MaxInvestment          string   `json:"maxInvestment,omitempty"`
	LiquidCapitalAvailable string   `json:"liquidCapitalAvailable,omitempty"`
	FundingSource          string   `json:"fundingSource,omitempty"`
	ROITimeline            string   `json:"roiTimeline,omitempty"`
	ExpectedAnnualROI      string   `json:"expectedAnnualRoi,omitempty"`
	PreferredSectors       []string `json:"preferredSectors,omitempty"`
	PreferredCategories    []string `json:"preferredCategories,omitempty"`
}

type PreferencesProfile struct {
	Theme                string                 `json:"theme,omitempty"`
	Language             string                 `json:"language,omitempty"`
	Timezone             string                 `json:"timezone,omitempty"`
	EmailNotifications   bool                   `json:"emailNotifications"`
	PushNotifications    bool                   `json:"pushNotifications"`
	SMSNotifications     bool                   `json:"smsNotifications"`
	NotificationSettings map[string]interface{} `json:"notificationSettings,omitempty"`
}

type SubscriptionProfile struct {
	Tier      string     `json:"tier"`
	IsValid   bool       `json:"isValid"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
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

	// 1. Fetch core user record
	var name, phone, email, location, profileImage sql.NullString
	var status string
	var createdAt, updatedAt time.Time
	err := h.db.QueryRowContext(ctx, `
		SELECT name, email, phone, location, profile_image, status, created_at, updated_at
		FROM users WHERE id = $1`, userID).Scan(
		&name, &email, &phone, &location, &profileImage, &status, &createdAt, &updatedAt)
	if err != nil {
		h.log.Error("Failed to fetch user profile from DB", map[string]interface{}{
			"userId": userID,
			"error":  err.Error(),
		})
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "INTERNAL_ERROR",
			"message": "Failed to retrieve profile",
		})
		return
	}

	profile := UserProfile{
		ID:            userID.(string),
		Email:         email.String,
		Name:          name.String,
		Phone:         phone.String,
		Status:        status,
		Location:      location.String,
		ProfileImage:  profileImage.String,
		EmailVerified: false,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}

	// FLE decryption for PII fields
	if h.fle != nil {
		profile.Email, _ = h.fle.DecryptField("email", profile.Email)
		profile.Name, _ = h.fle.DecryptField("name", profile.Name)
		profile.Phone, _ = h.fle.DecryptField("phone", profile.Phone)
		profile.Location, _ = h.fle.DecryptField("location", profile.Location)
	}

	// 2. Fetch professional details
	profile.Professional = h.fetchProfessionalProfile(ctx, userID)

	// 3. Fetch company details
	profile.Company = h.fetchCompanyProfile(ctx, userID)

	// 4. Fetch investment details
	profile.Investment = h.fetchInvestmentProfile(ctx, userID)

	// 5. Fetch preferences
	profile.Preferences = h.fetchPreferencesProfile(ctx, userID)

	// 6. Fetch subscription
	profile.Subscription = h.fetchSubscriptionProfile(ctx, userID)

	c.JSON(http.StatusOK, UserProfileResponse{
		Success:   true,
		Data:      profile,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *UserHandler) fetchProfessionalProfile(ctx context.Context, userID interface{}) *ProfessionalProfile {
	var occupation, designation, experience, industry, industryID sql.NullString
	var priorExperience bool

	err := h.db.QueryRowContext(ctx, `
		SELECT occupation, designation, experience, prior_experience, industry_id, industry
		FROM user_professional_details WHERE user_id = $1`, userID,
	).Scan(&occupation, &designation, &experience, &priorExperience, &industryID, &industry)
	if err != nil {
		return nil
	}

	p := &ProfessionalProfile{
		Occupation:      occupation.String,
		Designation:     designation.String,
		Experience:      experience.String,
		PriorExperience: priorExperience,
		IndustryID:      industryID.String,
		Industry:        industry.String,
	}

	if h.fle != nil {
		p.Occupation, _ = h.fle.DecryptField("jobTitle", p.Occupation)
	}

	return p
}

func (h *UserHandler) fetchCompanyProfile(ctx context.Context, userID interface{}) *CompanyProfile {
	var businessName, businessType, industrySector sql.NullString
	var yearEstablished sql.NullInt64
	var cinRegistration, gstNumber, annualTurnover, companyWebsite, companyPhone sql.NullString
	var registeredAddress, companyDescription sql.NullString

	err := h.db.QueryRowContext(ctx, `
		SELECT business_name, business_type, industry_sector, year_established,
		       cin_registration, gst_number, annual_turnover, company_website,
		       company_phone, registered_address, company_description
		FROM user_company_details WHERE user_id = $1`, userID,
	).Scan(&businessName, &businessType, &industrySector, &yearEstablished,
		&cinRegistration, &gstNumber, &annualTurnover, &companyWebsite,
		&companyPhone, &registeredAddress, &companyDescription)
	if err != nil {
		return nil
	}

	c := &CompanyProfile{
		BusinessName:       businessName.String,
		BusinessType:       businessType.String,
		IndustrySector:     industrySector.String,
		YearEstablished:    yearEstablished.Int64,
		CINRegistration:    cinRegistration.String,
		GSTNumber:          gstNumber.String,
		AnnualTurnover:     annualTurnover.String,
		CompanyWebsite:     companyWebsite.String,
		CompanyPhone:       companyPhone.String,
		RegisteredAddress:  registeredAddress.String,
		CompanyDescription: companyDescription.String,
	}

	if h.fle != nil {
		c.BusinessName, _ = h.fle.DecryptField("businessName", c.BusinessName)
		c.CINRegistration, _ = h.fle.DecryptField("cinNumber", c.CINRegistration)
		c.GSTNumber, _ = h.fle.DecryptField("gstNumber", c.GSTNumber)
	}

	return c
}

func (h *UserHandler) fetchInvestmentProfile(ctx context.Context, userID interface{}) *InvestmentProfile {
	var minInvestment, maxInvestment, liquidCapital, fundingSource sql.NullString
	var roiTimeline, expectedAnnualROI sql.NullString
	var preferredSectors, preferredCategories []string

	err := h.db.QueryRowContext(ctx, `
		SELECT min_investment, max_investment, liquid_capital_available, funding_source,
		       roi_timeline, expected_annual_roi, preferred_sectors, preferred_categories
		FROM user_investment_details WHERE user_id = $1`, userID,
	).Scan(&minInvestment, &maxInvestment, &liquidCapital, &fundingSource,
		&roiTimeline, &expectedAnnualROI, &preferredSectors, &preferredCategories)
	if err != nil {
		return nil
	}

	return &InvestmentProfile{
		MinInvestment:          minInvestment.String,
		MaxInvestment:          maxInvestment.String,
		LiquidCapitalAvailable: liquidCapital.String,
		FundingSource:          fundingSource.String,
		ROITimeline:            roiTimeline.String,
		ExpectedAnnualROI:      expectedAnnualROI.String,
		PreferredSectors:       preferredSectors,
		PreferredCategories:    preferredCategories,
	}
}

func (h *UserHandler) fetchPreferencesProfile(ctx context.Context, userID interface{}) *PreferencesProfile {
	var theme, language, timezone sql.NullString
	var emailNotifs, pushNotifs, smsNotifs bool
	var notificationSettings sql.NullString

	err := h.db.QueryRowContext(ctx, `
		SELECT theme, email_notifications, push_notifications, sms_notifications,
		       language, timezone, notification_settings
		FROM user_preferences WHERE user_id = $1`, userID,
	).Scan(&theme, &emailNotifs, &pushNotifs, &smsNotifs,
		&language, &timezone, &notificationSettings)
	if err != nil {
		return nil
	}

	p := &PreferencesProfile{
		Theme:              theme.String,
		Language:           language.String,
		Timezone:           timezone.String,
		EmailNotifications: emailNotifs,
		PushNotifications:  pushNotifs,
		SMSNotifications:   smsNotifs,
	}

	if notificationSettings.Valid && notificationSettings.String != "" {
		var settings map[string]interface{}
		if err := json.Unmarshal([]byte(notificationSettings.String), &settings); err == nil {
			p.NotificationSettings = settings
		}
	}

	return p
}

func (h *UserHandler) fetchSubscriptionProfile(ctx context.Context, userID interface{}) *SubscriptionProfile {
	var tier string
	var isValid bool
	var expiresAt sql.NullTime

	_ = h.db.QueryRowContext(ctx, `
		SELECT tier, is_valid, expires_at FROM user_subscriptions
		WHERE user_id = $1 AND is_valid = true
		ORDER BY created_at DESC LIMIT 1`, userID,
	).Scan(&tier, &isValid, &expiresAt)
	if tier == "" {
		tier = "free"
		isValid = true
	}

	s := &SubscriptionProfile{
		Tier:    tier,
		IsValid: isValid,
	}
	if expiresAt.Valid {
		s.ExpiresAt = &expiresAt.Time
	}
	return s
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
		Theme                string `json:"theme"`
		Language             string `json:"language"`
		Timezone             string `json:"timezone"`
		EmailNotifications   bool   `json:"emailNotifications"`
		PushNotifications    bool   `json:"pushNotifications"`
		SMSNotifications     bool   `json:"smsNotifications"`
		NotificationSettings map[string]interface{} `json:"notificationSettings,omitempty"`
	}

	var prefs UserPreferences
	var notificationSettings sql.NullString
	err := h.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(theme, ''),
			COALESCE(language, ''),
			COALESCE(timezone, ''),
			email_notifications,
			push_notifications,
			sms_notifications,
			notification_settings
		FROM user_preferences
		WHERE user_id = $1`, userID).Scan(
		&prefs.Theme, &prefs.Language, &prefs.Timezone,
		&prefs.EmailNotifications, &prefs.PushNotifications, &prefs.SMSNotifications,
		&notificationSettings)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": UserPreferences{
				Theme:              "light",
				Language:           "en",
				Timezone:           "UTC",
				EmailNotifications: true,
				PushNotifications:  true,
				SMSNotifications:   false,
			},
			"timestamp": time.Now().UTC().Format(time.RFC3339),
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

	if notificationSettings.Valid && notificationSettings.String != "" {
		var settings map[string]interface{}
		if err := json.Unmarshal([]byte(notificationSettings.String), &settings); err == nil {
			prefs.NotificationSettings = settings
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      prefs,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
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
		SELECT id, action, field, old_value, new_value, changes, source, request_id, created_at
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
		ID        string          `json:"id"`
		Action    string          `json:"action"`
		Field     string          `json:"field,omitempty"`
		OldValue  string          `json:"oldValue,omitempty"`
		NewValue  string          `json:"newValue,omitempty"`
		Changes   json.RawMessage `json:"changes,omitempty"`
		Source    string          `json:"source,omitempty"`
		RequestID string          `json:"requestId,omitempty"`
		CreatedAt time.Time       `json:"createdAt"`
	}

	var entries []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		if err := rows.Scan(&entry.ID, &entry.Action, &entry.Field, &entry.OldValue, &entry.NewValue, &entry.Changes, &entry.Source, &entry.RequestID, &entry.CreatedAt); err != nil {
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
			"items":      entries,
			"totalCount": total,
			"page":       page,
			"limit":      limit,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
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
		"status":     "open",
		"message":    "Feedback submitted successfully",
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
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
		"success":   true,
		"data":      data,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

