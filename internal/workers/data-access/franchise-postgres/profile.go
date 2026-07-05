package franchisepostgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ============================================================================
// GET PROFILE
// ============================================================================

func (h *Handler) handleGetProfile(ctx context.Context, variables string) (*GetProfileOutput, error) {
	var input GetProfileInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserId)
	if err != nil {
		return nil, err
	}

	// Fetch core user record
	var name, phone, location, profileImage sql.NullString
	var email string
	var status string
	var createdAt time.Time

	err = h.db.QueryRowContext(ctx, `
		SELECT name, email, phone, location, profile_image, status, created_at
		FROM users WHERE id = $1`, userID,
	).Scan(&name, &email, &phone, &location, &profileImage, &status, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: user not found", ErrFranchiseNotFound)
		}
		return nil, fmt.Errorf("%w: select user: %v", ErrDatabaseError, err)
	}

	user := map[string]interface{}{
		"id":         userID.String(),
		"email":      email,
		"status":     status,
		"created_at": createdAt,
	}
	if name.Valid {
		user["name"] = name.String
	}
	if phone.Valid {
		user["phone"] = phone.String
	}
	if location.Valid {
		user["location"] = location.String
	}
	if profileImage.Valid {
		user["profile_image"] = profileImage.String
	}

	// Fetch professional details
	professional := h.fetchProfessionalDetails(ctx, userID)

	// Fetch company details
	company := h.fetchCompanyDetails(ctx, userID)

	// Fetch investment details
	investment := h.fetchInvestmentDetails(ctx, userID)

	// Fetch preferences
	preferences := h.fetchPreferences(ctx, userID)

	return &GetProfileOutput{
		Success:      true,
		Message:      "Profile retrieved successfully",
		User:         user,
		Professional: professional,
		Company:      company,
		Investment:   investment,
		Preferences:  preferences,
	}, nil
}

func (h *Handler) fetchProfessionalDetails(ctx context.Context, userID uuid.UUID) map[string]interface{} {
	var occupation, designation, experience, industry sql.NullString
	var priorExperience bool
	var industryID sql.NullString

	err := h.db.QueryRowContext(ctx, `
		SELECT occupation, designation, experience, prior_experience, industry_id, industry
		FROM user_professional_details WHERE user_id = $1`, userID,
	).Scan(&occupation, &designation, &experience, &priorExperience, &industryID, &industry)
	if err != nil {
		return nil
	}

	result := map[string]interface{}{
		"prior_experience": priorExperience,
	}
	if occupation.Valid {
		result["occupation"] = occupation.String
	}
	if designation.Valid {
		result["designation"] = designation.String
	}
	if experience.Valid {
		result["experience"] = experience.String
	}
	if industry.Valid {
		result["industry"] = industry.String
	}
	if industryID.Valid {
		result["industry_id"] = industryID.String
	}
	return result
}

func (h *Handler) fetchCompanyDetails(ctx context.Context, userID uuid.UUID) map[string]interface{} {
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

	result := make(map[string]interface{})
	if businessName.Valid {
		result["business_name"] = businessName.String
	}
	if businessType.Valid {
		result["business_type"] = businessType.String
	}
	if industrySector.Valid {
		result["industry_sector"] = industrySector.String
	}
	if yearEstablished.Valid {
		result["year_established"] = yearEstablished.Int64
	}
	if cinRegistration.Valid {
		result["cin_registration"] = cinRegistration.String
	}
	if gstNumber.Valid {
		result["gst_number"] = gstNumber.String
	}
	if annualTurnover.Valid {
		result["annual_turnover"] = annualTurnover.String
	}
	if companyWebsite.Valid {
		result["company_website"] = companyWebsite.String
	}
	if companyPhone.Valid {
		result["company_phone"] = companyPhone.String
	}
	if registeredAddress.Valid {
		result["registered_address"] = registeredAddress.String
	}
	if companyDescription.Valid {
		result["company_description"] = companyDescription.String
	}
	return result
}

func (h *Handler) fetchInvestmentDetails(ctx context.Context, userID uuid.UUID) map[string]interface{} {
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

	result := make(map[string]interface{})
	if minInvestment.Valid {
		result["min_investment"] = minInvestment.String
	}
	if maxInvestment.Valid {
		result["max_investment"] = maxInvestment.String
	}
	if liquidCapital.Valid {
		result["liquid_capital_available"] = liquidCapital.String
	}
	if fundingSource.Valid {
		result["funding_source"] = fundingSource.String
	}
	if roiTimeline.Valid {
		result["roi_timeline"] = roiTimeline.String
	}
	if expectedAnnualROI.Valid {
		result["expected_annual_roi"] = expectedAnnualROI.String
	}
	if len(preferredSectors) > 0 {
		result["preferred_sectors"] = preferredSectors
	}
	if len(preferredCategories) > 0 {
		result["preferred_categories"] = preferredCategories
	}
	return result
}

func (h *Handler) fetchPreferences(ctx context.Context, userID uuid.UUID) map[string]interface{} {
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

	result := map[string]interface{}{
		"email_notifications": emailNotifs,
		"push_notifications":  pushNotifs,
		"sms_notifications":   smsNotifs,
	}
	if theme.Valid {
		result["theme"] = theme.String
	}
	if language.Valid {
		result["language"] = language.String
	}
	if timezone.Valid {
		result["timezone"] = timezone.String
	}
	if notificationSettings.Valid && notificationSettings.String != "" {
		var settings map[string]interface{}
		if err := json.Unmarshal([]byte(notificationSettings.String), &settings); err == nil {
			result["notification_settings"] = settings
		}
	}
	return result
}

// ============================================================================
// UPDATE PERSONAL DETAILS
// ============================================================================

func (h *Handler) handleUpdatePersonalDetails(ctx context.Context, variables string) (*BaseOutput, error) {
	var input UpdatePersonalInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserId)
	if err != nil {
		return nil, err
	}

	pd := input.ProfileData
	if len(pd) == 0 {
		return nil, fmt.Errorf("%w: profileData cannot be empty", ErrValidationError)
	}

	// Build dynamic UPDATE for users table (name, phone, location)
	query := "UPDATE users SET updated_at = NOW()"
	args := []interface{}{}
	argPos := 2

	if v, ok := pd["name"].(string); ok {
		if err := h.validateString("name", v, 0, 500); err != nil {
			return nil, err
		}
		query += fmt.Sprintf(", name = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(v))
		argPos++
	}
	if v, ok := pd["phone"].(string); ok {
		if err := h.validateString("phone", v, 0, 100); err != nil {
			return nil, err
		}
		query += fmt.Sprintf(", phone = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(v))
		argPos++
	}
	if v, ok := pd["location"].(string); ok {
		if err := h.validateString("location", v, 0, 255); err != nil {
			return nil, err
		}
		query += fmt.Sprintf(", location = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(v))
		argPos++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argPos)
	args = append(args, userID)

	_, err = h.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: update personal details: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		Success: true,
		Message: "Personal details updated successfully",
	}, nil
}

// ============================================================================
// UPDATE PROFESSIONAL DETAILS
// ============================================================================

func (h *Handler) handleUpdateProfessionalDetails(ctx context.Context, variables string) (*BaseOutput, error) {
	var input UpdateProfessionalInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserId)
	if err != nil {
		return nil, err
	}

	pd := input.ProfileData
	if len(pd) == 0 {
		return nil, fmt.Errorf("%w: profileData cannot be empty", ErrValidationError)
	}

	// UPSERT: INSERT ON CONFLICT UPDATE
	query := `
		INSERT INTO user_professional_details (user_id, occupation, designation, experience, prior_experience, industry_id, industry)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id) DO UPDATE SET
			occupation = COALESCE(EXCLUDED.occupation, user_professional_details.occupation),
			designation = COALESCE(EXCLUDED.designation, user_professional_details.designation),
			experience = COALESCE(EXCLUDED.experience, user_professional_details.experience),
			prior_experience = EXCLUDED.prior_experience,
			industry_id = COALESCE(EXCLUDED.industry_id, user_professional_details.industry_id),
			industry = COALESCE(EXCLUDED.industry, user_professional_details.industry),
			updated_at = NOW()`

	var occupation, designation, experience, industry sql.NullString
	var priorExperience bool
	var industryID sql.NullString

	if v, ok := pd["occupation"].(string); ok {
		occupation = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["designation"].(string); ok {
		designation = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["experience"].(string); ok {
		experience = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["prior_experience"].(bool); ok {
		priorExperience = v
	}
	if v, ok := pd["industry"].(string); ok {
		industry = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["industry_id"].(string); ok && v != "" {
		if _, err := uuid.Parse(v); err == nil {
			industryID = sql.NullString{String: v, Valid: true}
		}
	}

	_, err = h.db.ExecContext(ctx, query, userID, occupation, designation, experience, priorExperience, industryID, industry)
	if err != nil {
		return nil, fmt.Errorf("%w: upsert professional details: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		Success: true,
		Message: "Professional details updated successfully",
	}, nil
}

// ============================================================================
// UPDATE COMPANY DETAILS
// ============================================================================

func (h *Handler) handleUpdateCompanyDetails(ctx context.Context, variables string) (*BaseOutput, error) {
	var input UpdateCompanyInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserId)
	if err != nil {
		return nil, err
	}

	pd := input.ProfileData
	if len(pd) == 0 {
		return nil, fmt.Errorf("%w: profileData cannot be empty", ErrValidationError)
	}

	query := `
		INSERT INTO user_company_details (
			user_id, business_name, business_type, industry_sector,
			year_established, cin_registration, gst_number, annual_turnover,
			company_website, company_phone, registered_address, company_description
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (user_id) DO UPDATE SET
			business_name = COALESCE(EXCLUDED.business_name, user_company_details.business_name),
			business_type = COALESCE(EXCLUDED.business_type, user_company_details.business_type),
			industry_sector = COALESCE(EXCLUDED.industry_sector, user_company_details.industry_sector),
			year_established = COALESCE(EXCLUDED.year_established, user_company_details.year_established),
			cin_registration = COALESCE(EXCLUDED.cin_registration, user_company_details.cin_registration),
			gst_number = COALESCE(EXCLUDED.gst_number, user_company_details.gst_number),
			annual_turnover = COALESCE(EXCLUDED.annual_turnover, user_company_details.annual_turnover),
			company_website = COALESCE(EXCLUDED.company_website, user_company_details.company_website),
			company_phone = COALESCE(EXCLUDED.company_phone, user_company_details.company_phone),
			registered_address = COALESCE(EXCLUDED.registered_address, user_company_details.registered_address),
			company_description = COALESCE(EXCLUDED.company_description, user_company_details.company_description),
			updated_at = NOW()`

	var businessName, businessType, industrySector sql.NullString
	var yearEstablished sql.NullInt64
	var cinRegistration, gstNumber, annualTurnover, companyWebsite, companyPhone sql.NullString
	var registeredAddress, companyDescription sql.NullString

	if v, ok := pd["business_name"].(string); ok {
		businessName = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["business_type"].(string); ok {
		businessType = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["industry_sector"].(string); ok {
		industrySector = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["year_established"].(float64); ok {
		yearEstablished = sql.NullInt64{Int64: int64(v), Valid: true}
	} else if v, ok := pd["year_established"].(int); ok {
		yearEstablished = sql.NullInt64{Int64: int64(v), Valid: true}
	}
	if v, ok := pd["cin_registration"].(string); ok {
		cinRegistration = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["gst_number"].(string); ok {
		gstNumber = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["annual_turnover"].(string); ok {
		annualTurnover = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["company_website"].(string); ok {
		if err := h.validateURL("companyWebsite", v); err != nil {
			return nil, err
		}
		companyWebsite = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["company_phone"].(string); ok {
		companyPhone = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["registered_address"].(string); ok {
		registeredAddress = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["company_description"].(string); ok {
		companyDescription = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}

	_, err = h.db.ExecContext(ctx, query, userID,
		businessName, businessType, industrySector, yearEstablished,
		cinRegistration, gstNumber, annualTurnover, companyWebsite,
		companyPhone, registeredAddress, companyDescription)
	if err != nil {
		return nil, fmt.Errorf("%w: upsert company details: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		Success: true,
		Message: "Company details updated successfully",
	}, nil
}

// ============================================================================
// UPDATE INVESTMENT DETAILS
// ============================================================================

func (h *Handler) handleUpdateInvestmentDetails(ctx context.Context, variables string) (*BaseOutput, error) {
	var input UpdateInvestmentInput2
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserId)
	if err != nil {
		return nil, err
	}

	pd := input.ProfileData
	if len(pd) == 0 {
		return nil, fmt.Errorf("%w: profileData cannot be empty", ErrValidationError)
	}

	query := `
		INSERT INTO user_investment_details (
			user_id, min_investment, max_investment, liquid_capital_available,
			funding_source, roi_timeline, expected_annual_roi,
			preferred_sectors, preferred_categories
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (user_id) DO UPDATE SET
			min_investment = COALESCE(EXCLUDED.min_investment, user_investment_details.min_investment),
			max_investment = COALESCE(EXCLUDED.max_investment, user_investment_details.max_investment),
			liquid_capital_available = COALESCE(EXCLUDED.liquid_capital_available, user_investment_details.liquid_capital_available),
			funding_source = COALESCE(EXCLUDED.funding_source, user_investment_details.funding_source),
			roi_timeline = COALESCE(EXCLUDED.roi_timeline, user_investment_details.roi_timeline),
			expected_annual_roi = COALESCE(EXCLUDED.expected_annual_roi, user_investment_details.expected_annual_roi),
			preferred_sectors = COALESCE(EXCLUDED.preferred_sectors, user_investment_details.preferred_sectors),
			preferred_categories = COALESCE(EXCLUDED.preferred_categories, user_investment_details.preferred_categories),
			updated_at = NOW()`

	var minInv, maxInv, liquidCap, fundingSource, roiTimeline, expectedROI sql.NullString
	var preferredSectors, preferredCategories []string

	if v, ok := pd["min_investment"].(string); ok {
		minInv = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["max_investment"].(string); ok {
		maxInv = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["liquid_capital_available"].(string); ok {
		liquidCap = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["funding_source"].(string); ok {
		fundingSource = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["roi_timeline"].(string); ok {
		roiTimeline = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["expected_annual_roi"].(string); ok {
		expectedROI = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["preferred_sectors"].([]interface{}); ok {
		for _, s := range v {
			if str, ok := s.(string); ok {
				preferredSectors = append(preferredSectors, h.sanitizer.SanitizeString(str))
			}
		}
	}
	if v, ok := pd["preferred_categories"].([]interface{}); ok {
		for _, c := range v {
			if str, ok := c.(string); ok {
				preferredCategories = append(preferredCategories, h.sanitizer.SanitizeString(str))
			}
		}
	}

	_, err = h.db.ExecContext(ctx, query, userID,
		minInv, maxInv, liquidCap, fundingSource,
		roiTimeline, expectedROI, preferredSectors, preferredCategories)
	if err != nil {
		return nil, fmt.Errorf("%w: upsert investment details: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		Success: true,
		Message: "Investment details updated successfully",
	}, nil
}

// ============================================================================
// UPDATE PREFERENCES
// ============================================================================

func (h *Handler) handleUpdatePreferences(ctx context.Context, variables string) (*BaseOutput, error) {
	var input UpdatePreferencesInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserId)
	if err != nil {
		return nil, err
	}

	pd := input.ProfileData
	if len(pd) == 0 {
		return nil, fmt.Errorf("%w: profileData cannot be empty", ErrValidationError)
	}

	query := `
		INSERT INTO user_preferences (user_id, theme, email_notifications, push_notifications, sms_notifications, language, timezone, notification_settings)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (user_id) DO UPDATE SET
			theme = COALESCE(EXCLUDED.theme, user_preferences.theme),
			email_notifications = EXCLUDED.email_notifications,
			push_notifications = EXCLUDED.push_notifications,
			sms_notifications = EXCLUDED.sms_notifications,
			language = COALESCE(EXCLUDED.language, user_preferences.language),
			timezone = COALESCE(EXCLUDED.timezone, user_preferences.timezone),
			notification_settings = COALESCE(EXCLUDED.notification_settings, user_preferences.notification_settings),
			updated_at = NOW()`

	var theme, language, timezone sql.NullString
	emailNotifs := true
	pushNotifs := true
	smsNotifs := false
	var notificationSettings sql.NullString

	if v, ok := pd["theme"].(string); ok {
		theme = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["email_notifications"].(bool); ok {
		emailNotifs = v
	}
	if v, ok := pd["push_notifications"].(bool); ok {
		pushNotifs = v
	}
	if v, ok := pd["sms_notifications"].(bool); ok {
		smsNotifs = v
	}
	if v, ok := pd["language"].(string); ok {
		language = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["timezone"].(string); ok {
		timezone = sql.NullString{String: h.sanitizer.SanitizeString(v), Valid: true}
	}
	if v, ok := pd["notification_settings"].(map[string]interface{}); ok {
		settingsJSON, _ := json.Marshal(v)
		notificationSettings = sql.NullString{String: string(settingsJSON), Valid: true}
	}

	_, err = h.db.ExecContext(ctx, query, userID,
		theme, emailNotifs, pushNotifs, smsNotifs,
		language, timezone, notificationSettings)
	if err != nil {
		return nil, fmt.Errorf("%w: upsert preferences: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		Success: true,
		Message: "Preferences updated successfully",
	}, nil
}

// ============================================================================
// INSERT PROFILE AUDIT
// ============================================================================

func (h *Handler) handleInsertProfileAudit(ctx context.Context, variables string) (*InsertProfileAuditOutput, error) {
	var input InsertProfileAuditInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserId)
	if err != nil {
		return nil, err
	}

	var changesJSON []byte
	if input.Changes != nil {
		changesJSON, _ = json.Marshal(input.Changes)
	}

	query := `
		INSERT INTO profile_audit_log (user_id, action, field, old_value, new_value, changes, source, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`

	var auditID uuid.UUID
	err = h.db.QueryRowContext(ctx, query,
		userID,
		h.sanitizer.SanitizeString(input.Action),
		h.sanitizer.SanitizeString(input.Field),
		h.sanitizer.SanitizeString(input.OldValue),
		h.sanitizer.SanitizeString(input.NewValue),
		changesJSON,
		h.sanitizer.SanitizeString(input.Source),
		h.sanitizer.SanitizeString(input.RequestId),
	).Scan(&auditID)
	if err != nil {
		return nil, fmt.Errorf("%w: insert profile audit: %v", ErrDatabaseError, err)
	}

	return &InsertProfileAuditOutput{
		Success: true,
		Message: "Audit log recorded",
		AuditId: auditID.String(),
	}, nil
}

// ============================================================================
// DELETE ACCOUNT (DPDPA anonymization)
// ============================================================================

func (h *Handler) handleDeleteAccount(ctx context.Context, variables string) (*BaseOutput, error) {
	var input DeleteAccountInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserId)
	if err != nil {
		return nil, err
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	archivedEmail := fmt.Sprintf("archived-%s@anonymized.local", userID.String())

	// 1. Anonymize users table
	_, err = tx.ExecContext(ctx, `
		UPDATE users SET
			email = $1,
			name = 'Deleted User',
			phone = NULL,
			location = NULL,
			profile_image = NULL,
			status = 'archived',
			archived_at = NOW(),
			updated_at = NOW()
		WHERE id = $2`, archivedEmail, userID)
	if err != nil {
		return nil, fmt.Errorf("%w: anonymize user: %v", ErrDatabaseError, err)
	}

	// 2. Anonymize professional details
	_, _ = tx.ExecContext(ctx, `
		DELETE FROM user_professional_details WHERE user_id = $1`, userID)

	// 3. Anonymize company details
	_, _ = tx.ExecContext(ctx, `
		DELETE FROM user_company_details WHERE user_id = $1`, userID)

	// 4. Anonymize investment details
	_, _ = tx.ExecContext(ctx, `
		DELETE FROM user_investment_details WHERE user_id = $1`, userID)

	// 5. Anonymize preferences
	_, _ = tx.ExecContext(ctx, `
		DELETE FROM user_preferences WHERE user_id = $1`, userID)

	// 6. Delete identities
	_, _ = tx.ExecContext(ctx, `
		DELETE FROM identities WHERE user_id = $1`, userID)

	// 7. Delete sessions (Redis-only, but log the intent)
	h.logger.Info("DPDPA: Account anonymized, Redis sessions should be cleared", map[string]interface{}{
		"userId": userID.String(),
	})

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit delete account: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		Success: true,
		Message: "Account anonymized successfully per DPDPA",
	}, nil
}

// ============================================================================
// DELETE KEYCLOAK USER
// ============================================================================

func (h *Handler) handleDeleteKeycloakUser(ctx context.Context, variables string) (*DeleteKeycloakUserOutput, error) {
	var input DeleteKeycloakUserInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserId)
	if err != nil {
		return nil, err
	}

	// Look up Keycloak UUID from identities table
	var providerUserID string
	err = h.db.QueryRowContext(ctx, `
		SELECT provider_user_id FROM identities
		WHERE user_id = $1 AND provider = 'keycloak'
		LIMIT 1`, userID,
	).Scan(&providerUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			h.logger.Warn("No Keycloak identity found for user, skipping", map[string]interface{}{
				"userId": userID.String(),
			})
			return &DeleteKeycloakUserOutput{
				Success: true,
				Message: "No Keycloak identity found, skipped",
			}, nil
		}
		return nil, fmt.Errorf("%w: lookup keycloak identity: %v", ErrDatabaseError, err)
	}

	// Get admin access token
	adminToken, err := h.getKeycloakAdminToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: get keycloak admin token: %v", ErrDatabaseError, err)
	}

	// DELETE to Keycloak Admin API
	url := fmt.Sprintf("%s/admin/realms/users/%s", h.config.KeycloakAdminURL, providerUserID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create keycloak delete request: %v", ErrDatabaseError, err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: keycloak delete request: %v", ErrDatabaseError, err)
	}
	defer resp.Body.Close()

	// 204 No Content or 404 Not Found (already deleted) are both success
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%w: keycloak delete returned %d: %s", ErrDatabaseError, resp.StatusCode, string(body))
	}

	h.logger.Info("Keycloak user deleted", map[string]interface{}{
		"userId":           userID.String(),
		"keycloakUserId":   providerUserID,
		"statusCode":       resp.StatusCode,
	})

	return &DeleteKeycloakUserOutput{
		Success: true,
		Message: "Keycloak user deleted successfully",
	}, nil
}

// getKeycloakAdminToken gets an admin access token from Keycloak
func (h *Handler) getKeycloakAdminToken(ctx context.Context) (string, error) {
	tokenURL := fmt.Sprintf("%s/realms/master/protocol/openid-connect/token", h.config.KeycloakAdminURL)

	data := fmt.Sprintf("client_id=%s&client_secret=%s&grant_type=client_credentials",
		h.config.KeycloakAdminClientID, h.config.KeycloakAdminSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewBufferString(data))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token request returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("empty access token received")
	}

	return tokenResp.AccessToken, nil
}

// stripBearer removes "Bearer " prefix if present
func stripBearer(token string) string {
	return strings.TrimPrefix(token, "Bearer ")
}
