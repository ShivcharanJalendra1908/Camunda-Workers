package franchisepostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"camunda-workers/internal/common/idempotency"
	"camunda-workers/internal/crypto"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/google/uuid"

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "franchise-postgres"
)

var (
	ErrInvalidInput      = errors.New("INVALID_INPUT")
	ErrDatabaseError     = errors.New("DATABASE_ERROR")
	ErrFranchiseNotFound = errors.New("FRANCHISE_NOT_FOUND")
	ErrInvalidOperation  = errors.New("INVALID_OPERATION")
	ErrInvalidUUID       = errors.New("INVALID_UUID")
	ErrValidationError   = errors.New("VALIDATION_ERROR")

	sqlInjectionPattern = regexp.MustCompile(`(?i)(\b(select|insert|update|delete|drop|union|exec|execute|truncate|alter)\b|(--|\/\*|\*\/|;))`)
	emailPattern        = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
)

type Handler struct {
	db                 *sql.DB
	logger             logger.Logger
	config             *Config
	errorHandler       *appErrs.ErrorHandler
	validator          *validation.Validator
	sanitizer          *validation.Sanitizer
	keyGenerator       *idempotency.KeyGenerator
	idempotencyChecker *idempotency.DBChecker
	encryptor          *crypto.Encryptor
}

func NewHandler(db *sql.DB, logger logger.Logger, config *Config, workerName string) *Handler {
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 30 * time.Second
	}

	keyGen := idempotency.NewKeyGenerator()
	checker := idempotency.NewDBChecker(db)

	var encryptor *crypto.Encryptor
	if config.EncryptionKey != "" {
		enc, err := crypto.NewEncryptor(config.EncryptionKey)
		if err == nil {
			encryptor = enc
		} else {
			logger.Warn("Failed to initialize encryptor", map[string]interface{}{"error": err.Error()})
		}
	} else {
		logger.Warn("EncryptionKey not provided in config", nil)
	}

	return &Handler{
		db:                 db,
		logger:             logger.WithFields(map[string]interface{}{"taskType": TaskType}),
		config:             config,
		errorHandler:       appErrs.NewErrorHandler(logger),
		validator:          validation.NewValidator(),
		sanitizer:          validation.NewSanitizer(),
		keyGenerator:       keyGen,
		idempotencyChecker: checker,
		encryptor:          encryptor,
	}
}

// ============================================================================
// CRITICAL: MAIN VALIDATION FUNCTIONS
// ============================================================================

func (h *Handler) validateBaseInput(input *BaseInput) error {
	// Validate OperationType
	if err := ozzo.Validate(input.OperationType,
		ozzo.Required.Error("operationType is required"),
		validation.ValidateStringLength(3, 50),
		validation.ValidateEnum([]string{
			"CREATE_FRANCHISE",
			"UPDATE_FRANCHISE",
			"GET_FRANCHISE",
			"DELETE_FRANCHISE",
			"CREATE_BUSINESS_OVERVIEW",
			"UPDATE_BUSINESS_OVERVIEW",
			"CREATE_INVESTMENT",
			"UPDATE_INVESTMENT",
			"CREATE_OPERATIONS",
			"UPDATE_OPERATIONS",
			"GET_FULL_FRANCHISE",
			"CREATE_SOCIAL_LINKS",
			"CREATE_LISTING_STATS",
			"UPDATE_LISTING_STATS",
			"CREATE_CATEGORY_QUESTION",
			"GET_CATEGORY_QUESTIONS",
			"UPDATE_CATEGORY_QUESTION",
			"DELETE_CATEGORY_QUESTION",
			"CREATE_FRANCHISE_CITY",
			"GET_LISTING_CITIES",
			"DELETE_FRANCHISE_CITY",
			"UPDATE_SOCIAL_LINKS",
			"ADD_BOOKMARK",
			"REMOVE_BOOKMARK",
			"GET_USER_BOOKMARKS",
			"CHECK_BOOKMARK",
			"SUBMIT_USER_RATING",
			"UPDATE_USER_RATING",
			"GET_USER_RATING",
			"GET_FRANCHISE_RATINGS",
			"DELETE_USER_RATING",
			"SHARE_FRANCHISE",
			"GET_USER_SHARES",
			"SAVE_CONTACT_MESSAGE",
			"CREATE_PENDING_ENTITY",
			"UPDATE_STATUS",
			"CREATE_ENQUIRY",
			"UPDATE_ENQUIRY_STATUS",
			"GET_ENQUIRIES",
			"GET_PROFILE",
			"UPDATE_PERSONAL",
			"UPDATE_PROFESSIONAL",
			"UPDATE_COMPANY",
			"UPDATE_USER_INVESTMENT",
			"UPDATE_PREFERENCES",
			"INSERT_PROFILE_AUDIT",
			"DELETE_ACCOUNT",
			"DELETE_KEYCLOAK_USER",
		}),
	); err != nil {
		return appErrs.NewValidationError("operationType", err.Error())
	}

	// Validate UpdatedBy if present
	if input.UpdatedBy != "" {
		if _, err := h.validateUUID("updatedBy", input.UpdatedBy); err != nil {
			return err
		}
	}

	return nil
}

func (h *Handler) validateUUID(fieldName, uuidStr string) (uuid.UUID, error) {
	if uuidStr == "" {
		return uuid.Nil, fmt.Errorf("%w: %s is required", ErrValidationError, fieldName)
	}

	parsedUUID, err := uuid.Parse(uuidStr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %s: %v", ErrInvalidUUID, fieldName, err)
	}

	if parsedUUID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: %s cannot be nil UUID", ErrValidationError, fieldName)
	}

	return parsedUUID, nil
}

func (h *Handler) validateString(fieldName, value string, minLen, maxLen int) error {
	if value == "" {
		return nil
	}

	if err := ozzo.Validate(value,
		validation.ValidateStringLength(minLen, maxLen),
		validation.SafeSQLString,
	); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrValidationError, fieldName, err)
	}

	if sqlInjectionPattern.MatchString(value) {
		return fmt.Errorf("%w: %s contains suspicious patterns", ErrValidationError, fieldName)
	}

	return nil
}

func (h *Handler) validateEmail(fieldName, email string) error {
	if email == "" {
		return nil
	}

	if !emailPattern.MatchString(email) {
		return fmt.Errorf("%w: %s has invalid format", ErrValidationError, fieldName)
	}

	if err := h.validateString(fieldName, email, 3, 255); err != nil {
		return err
	}

	return nil
}

func (h *Handler) validateURL(fieldName, urlStr string) error {
	if urlStr == "" {
		return nil
	}

	if err := h.validateString(fieldName, urlStr, 5, 500); err != nil {
		return err
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("%w: %s has invalid URL format: %v", ErrValidationError, fieldName, err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("%w: %s must use http:// or https://", ErrValidationError, fieldName)
	}

	return nil
}

func (h *Handler) validateNumericRange(fieldName string, value, min, max float64, allowNil bool) error {
	if allowNil && value == 0 {
		return nil
	}

	if value < min || value > max {
		return fmt.Errorf("%w: %s must be between %.2f and %.2f", ErrValidationError, fieldName, min, max)
	}

	return nil
}

func (h *Handler) validateYear(fieldName string, year *int16) error {
	if year == nil {
		return nil
	}

	currentYear := int16(time.Now().Year())
	if *year < 1800 || *year > currentYear {
		return fmt.Errorf("%w: %s must be between 1800 and %d", ErrValidationError, fieldName, currentYear)
	}

	return nil
}

func (h *Handler) validateJSONField(fieldName string, jsonData interface{}) error {
	if jsonData == nil {
		return nil
	}

	jsonBytes, err := json.Marshal(jsonData)
	if err != nil {
		return fmt.Errorf("%w: %s has invalid JSON format: %v", ErrValidationError, fieldName, err)
	}

	jsonStr := string(jsonBytes)

	dangerousPatterns := []string{
		"\"script\":", "\"inline\":", "\"source\":", "\"function\":",
		"\"eval(\"", "\"exec(\"", "\"$where\":", "\"$ne\":", "\"$regex\":",
	}

	lowerJSON := strings.ToLower(jsonStr)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(lowerJSON, pattern) {
			return fmt.Errorf("%w: %s contains dangerous pattern: %s", ErrValidationError, fieldName, pattern)
		}
	}

	if len(jsonStr) > 10000 {
		return fmt.Errorf("%w: %s exceeds maximum size of 10000 characters", ErrValidationError, fieldName)
	}

	return nil
}

func (h *Handler) validateFranchiseInput(input CreateFranchiseInput) error {
	// 1. Validate Name
	if err := h.validateString("name", input.Name, 2, 100); err != nil {
		return err
	}

	// 2. Validate Slug
	if err := h.validateString("slug", input.Slug, 2, 100); err != nil {
		return err
	}

	// 3. Validate Short Description
	if err := h.validateString("shortDescription", input.ShortDescription, 10, 200); err != nil {
		return err
	}

	// 4. Validate Description
	if err := h.validateString("description", input.Description, 10, 5000); err != nil {
		return err
	}

	// 5. Validate Email
	if err := h.validateEmail("contactEmail", input.ContactEmail); err != nil {
		return err
	}

	// 6. Validate Logo URL
	if err := h.validateURL("logoURL", input.LogoURL); err != nil {
		return err
	}

	// Validate Website URL
	if input.WebsiteURL != "" {
		if err := h.validateURL("websiteURL", input.WebsiteURL); err != nil {
			return err
		}
	}

	// 7. Validate Years
	if err := h.validateYear("foundedYear", input.FoundedYear); err != nil {
		return err
	}
	if err := h.validateYear("establishedYear", input.EstablishedYear); err != nil {
		return err
	}

	// 8. Validate Counts
	if input.TotalOutlets < 0 {
		return fmt.Errorf("%w: totalOutlets must be >= 0", ErrValidationError)
	}
	if input.UnitsCount < 0 {
		return fmt.Errorf("%w: unitsCount must be >= 0", ErrValidationError)
	}

	// 9. Validate Leader Information
	if input.LeaderName != "" {
		if err := h.validateString("leaderName", input.LeaderName, 2, 100); err != nil {
			return err
		}
	}
	if input.LeaderRole != "" {
		if err := h.validateString("leaderRole", input.LeaderRole, 2, 100); err != nil {
			return err
		}
	}

	// 10. Validate CreatedBy
	if _, err := h.validateUUID("createdBy", input.CreatedBy); err != nil {
		return err
	}

	// 11. Validate Social URLs
	if input.InstagramURL != "" {
		if err := h.validateURL("instagramURL", input.InstagramURL); err != nil {
			return err
		}
	}
	if input.FacebookURL != "" {
		if err := h.validateURL("facebookURL", input.FacebookURL); err != nil {
			return err
		}
	}
	if input.TwitterURL != "" {
		if err := h.validateURL("twitterURL", input.TwitterURL); err != nil {
			return err
		}
	}
	if input.LinkedinURL != "" {
		if err := h.validateURL("linkedinURL", input.LinkedinURL); err != nil {
			return err
		}
	}

	// 12. Validate Industry/Business Type
	if input.Industry != "" {
		if err := h.validateString("industry", input.Industry, 2, 100); err != nil {
			return err
		}
	}
	if input.BusinessType != "" {
		if err := h.validateString("businessType", input.BusinessType, 2, 50); err != nil {
			return err
		}
	}

	return nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

func nullString(s string) string {
	if s == "" {
		return ""
	}
	return s
}

func nullFloat64(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

func nullInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}

func nullBool(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

// Handle processes the franchise-postgres operations job
func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	// ✅ EXTRACT TRACE CONTEXT
	ctx := context.Background()

	var traceID, parentSpanID string
	var jobVars map[string]interface{}

	if err := json.Unmarshal([]byte(job.Variables), &jobVars); err == nil {
		if tid, ok := jobVars["traceId"].(string); ok {
			traceID = tid
		}
		if psid, ok := jobVars["spanId"].(string); ok {
			parentSpanID = psid
		}
	}

	// ✅ CREATE WORKER SPAN
	tracer := otel.Tracer("worker-manager")
	ctx, span := tracer.Start(ctx, "worker:"+TaskType,
		trace.WithAttributes(
			attribute.String("worker.name", TaskType),
			attribute.Int64("job.key", job.GetKey()),
			attribute.Int64("workflow.instance_key", job.GetProcessInstanceKey()),
			attribute.String("workflow.process_id", job.GetBpmnProcessId()),
			attribute.String("workflow.element_id", job.GetElementId()),
			attribute.String("trace.parent_id", parentSpanID),
		),
	)
	defer span.End()

	h.logger.Info("Processing franchise-postgres job", map[string]interface{}{
		"jobKey":           job.GetKey(),
		"workflowInstance": job.ProcessInstanceKey,
		"workflowKey":      job.ProcessDefinitionKey,
		"traceId":          traceID,
		"spanId":           span.SpanContext().SpanID().String(),
	})

	// Parse input to determine operation type
	var baseInput BaseInput
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "franchise-postgres.parseInput")
	if err := json.Unmarshal([]byte(job.Variables), &baseInput); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.failJob(ctx, client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
		spanParse.End()
		return
	}
	spanParse.End()

	// ===== CRITICAL: VALIDATE BASE INPUT =====
	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "franchise-postgres.validateInput")
	if err := h.validateBaseInput(&baseInput); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.failJob(ctx, client, job, "VALIDATION_ERROR", err.Error(), 0)
		spanValidate.End()
		return
	}
	spanValidate.End()

	ctxExec, cancel := context.WithTimeout(ctx, h.config.RequestTimeout)
	defer cancel()

	_, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "franchise-postgres.Execute")
	// Route to appropriate operation handler
	var result interface{}
	var err error

	// ===== CRITICAL: Add input sanitization before processing =====
	sanitizedVariables := h.sanitizer.SanitizeString(job.Variables)

	switch baseInput.OperationType {
	case "CREATE_FRANCHISE":
		result, err = h.handleCreateFranchise(ctxExec, sanitizedVariables)
	case "UPDATE_FRANCHISE":
		result, err = h.handleUpdateFranchise(ctxExec, sanitizedVariables, baseInput.UpdatedBy)
	case "GET_FRANCHISE":
		result, err = h.handleGetFranchise(ctxExec, sanitizedVariables)
	case "DELETE_FRANCHISE":
		result, err = h.handleDeleteFranchise(ctxExec, sanitizedVariables, baseInput.UpdatedBy)
	case "CREATE_BUSINESS_OVERVIEW":
		result, err = h.handleCreateBusinessOverview(ctxExec, sanitizedVariables)
	case "UPDATE_BUSINESS_OVERVIEW":
		result, err = h.handleUpdateBusinessOverview(ctxExec, sanitizedVariables, baseInput.UpdatedBy)
	case "CREATE_INVESTMENT":
		result, err = h.handleCreateInvestment(ctxExec, sanitizedVariables)
	case "UPDATE_INVESTMENT":
		result, err = h.handleUpdateInvestment(ctxExec, sanitizedVariables, baseInput.UpdatedBy)
	case "CREATE_OPERATIONS":
		result, err = h.handleCreateOperations(ctxExec, sanitizedVariables)
	case "UPDATE_OPERATIONS":
		result, err = h.handleUpdateOperations(ctxExec, sanitizedVariables, baseInput.UpdatedBy)
	case "GET_FULL_FRANCHISE":
		result, err = h.handleGetFullFranchise(ctxExec, sanitizedVariables)
	// NEW OPERATIONS:
	case "CREATE_SOCIAL_LINKS":
		result, err = h.handleCreateSocialLinks(ctxExec, sanitizedVariables)
	case "CREATE_LISTING_STATS":
		result, err = h.handleCreateFranchiseStats(ctxExec, sanitizedVariables)
	case "UPDATE_LISTING_STATS":
		result, err = h.handleUpdateFranchiseStats(ctxExec, sanitizedVariables)
	// Category Questions Operations (NEW)
	case "CREATE_CATEGORY_QUESTION":
		result, err = h.handleCreateCategoryQuestion(ctxExec, sanitizedVariables)
	case "GET_CATEGORY_QUESTIONS":
		result, err = h.handleGetCategoryQuestions(ctxExec, sanitizedVariables)
	case "UPDATE_CATEGORY_QUESTION":
		result, err = h.handleUpdateCategoryQuestion(ctxExec, sanitizedVariables)
	case "DELETE_CATEGORY_QUESTION":
		result, err = h.handleDeleteCategoryQuestion(ctxExec, sanitizedVariables)
	// Franchise Cities Operations (NEW)
	case "CREATE_FRANCHISE_CITY":
		result, err = h.handleCreateFranchiseCity(ctxExec, sanitizedVariables)
	case "GET_LISTING_CITIES":
		result, err = h.handleGetFranchiseCities(ctxExec, sanitizedVariables)
	case "DELETE_FRANCHISE_CITY":
		result, err = h.handleDeleteFranchiseCity(ctxExec, sanitizedVariables)
	// UPDATE_SOCIAL_LINKS (NEW)
	case "UPDATE_SOCIAL_LINKS":
		result, err = h.handleUpdateSocialLinks(ctxExec, sanitizedVariables)

	case "ADD_BOOKMARK":
		result, err = h.handleAddBookmark(ctxExec, sanitizedVariables)
	case "REMOVE_BOOKMARK":
		result, err = h.handleRemoveBookmark(ctxExec, sanitizedVariables)
	case "GET_USER_BOOKMARKS":
		result, err = h.handleGetUserBookmarks(ctxExec, sanitizedVariables)
	case "CHECK_BOOKMARK":
		result, err = h.handleCheckBookmark(ctxExec, sanitizedVariables)
	case "SUBMIT_USER_RATING":
		result, err = h.handleSubmitUserRating(ctxExec, sanitizedVariables)
	case "UPDATE_USER_RATING":
		result, err = h.handleUpdateUserRating(ctxExec, sanitizedVariables)
	case "GET_USER_RATING":
		result, err = h.handleGetUserRating(ctxExec, sanitizedVariables)
	case "GET_FRANCHISE_RATINGS":
		result, err = h.handleGetFranchiseRatings(ctxExec, sanitizedVariables)
	case "DELETE_USER_RATING":
		result, err = h.handleDeleteUserRating(ctxExec, sanitizedVariables)
	case "SHARE_FRANCHISE":
		result, err = h.handleShareFranchise(ctxExec, sanitizedVariables)
	case "GET_USER_SHARES":
		result, err = h.handleGetUserShares(ctxExec, sanitizedVariables)
	case "SAVE_CONTACT_MESSAGE":
		result, err = h.saveContactMessage(ctxExec, sanitizedVariables)
	case "CREATE_PENDING_ENTITY":
		result, err = h.handleCreatePendingEntity(ctxExec, sanitizedVariables)
	case "UPDATE_STATUS":
		result, err = h.handleUpdateStatus(ctxExec, sanitizedVariables)
	case "CREATE_ENQUIRY":
		result, err = h.handleCreateEnquiry(ctxExec, sanitizedVariables)
	case "UPDATE_ENQUIRY_STATUS":
		result, err = h.handleUpdateEnquiryStatus(ctxExec, sanitizedVariables)
	case "GET_ENQUIRIES":
		result, err = h.handleGetEnquiries(ctxExec, sanitizedVariables)

	// USER PROFILE OPERATIONS
	case "GET_PROFILE":
		result, err = h.handleGetProfile(ctxExec, sanitizedVariables)
	case "UPDATE_PERSONAL":
		result, err = h.handleUpdatePersonalDetails(ctxExec, sanitizedVariables)
	case "UPDATE_PROFESSIONAL":
		result, err = h.handleUpdateProfessionalDetails(ctxExec, sanitizedVariables)
	case "UPDATE_COMPANY":
		result, err = h.handleUpdateCompanyDetails(ctxExec, sanitizedVariables)
	case "UPDATE_USER_INVESTMENT":
		result, err = h.handleUpdateInvestmentDetails(ctxExec, sanitizedVariables)
	case "UPDATE_PREFERENCES":
		result, err = h.handleUpdatePreferences(ctxExec, sanitizedVariables)
	case "INSERT_PROFILE_AUDIT":
		result, err = h.handleInsertProfileAudit(ctxExec, sanitizedVariables)
	case "DELETE_ACCOUNT":
		result, err = h.handleDeleteAccount(ctxExec, sanitizedVariables)
	case "DELETE_KEYCLOAK_USER":
		result, err = h.handleDeleteKeycloakUser(ctxExec, sanitizedVariables)

	default:
		span.SetAttributes(attribute.String("error.operation_type", baseInput.OperationType))
		h.failJob(ctx, client, job, "INVALID_OPERATION",
			fmt.Sprintf("Unknown operation type: %s", baseInput.OperationType), 0)
		return
	}

	spanExec.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.logger.Error("Operation failed", map[string]interface{}{
			"operation": baseInput.OperationType,
			"error":     err,
			"jobKey":    job.GetKey(),
			"traceId":   traceID,
		})

		errorCode := "OPERATION_ERROR"
		retries := int32(0)

		if errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrValidationError) {
			errorCode = "VALIDATION_ERROR"
			retries = 0
			span.SetAttributes(attribute.String("error.type", "validation"))
		} else if errors.Is(err, ErrFranchiseNotFound) {
			errorCode = "NOT_FOUND"
			retries = 0
			span.SetAttributes(attribute.String("error.type", "not_found"))
		} else if errors.Is(err, ErrDatabaseError) {
			errorCode = "DATABASE_ERROR"
			retries = 2
			span.SetAttributes(attribute.String("error.type", "database"))
		}

		h.failJob(ctx, client, job, errorCode, err.Error(), retries)
		return
	}

	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "franchise-postgres.completeJob")
	h.completeJob(ctx, client, job, result)
	spanComp.End()
}

// ============================================================================
// CREATE FRANCHISE
// ============================================================================
func (h *Handler) handleCreateFranchise(ctx context.Context, variables string) (*FranchiseOutput, error) {
	var input CreateFranchiseInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidationError, err)
	}

	if err := h.validateFranchiseInput(input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidationError, err)
	}

	createdBy, err := uuid.Parse(input.CreatedBy)
	if err != nil {
		return nil, fmt.Errorf("%w: created_by: %v", ErrInvalidUUID, err)
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	var encryptedEmail string
	if input.ContactEmail != "" {
		if h.encryptor == nil {
			return nil, fmt.Errorf("encryptor not initialized")
		}
		enc, err := h.encryptor.Encrypt([]byte(input.ContactEmail))
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt email: %v", err)
		}
		encryptedEmail = enc
	}

	listingQuery := `
		INSERT INTO listings (
			name, slug, short_description, description, founded_year, 
			trusted_seller, verified, contact_email, 
			logo_url_circle, website_url, is_sponsored, created_by, created_at, updated_at, entity_type, status
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, 'franchise', 'pending'
		) RETURNING id, created_at, updated_at`

	var listingID uuid.UUID
	var createdAt, updatedAt time.Time
	now := time.Now()

	// ===== CRITICAL: All parameters are sanitized =====
	err = tx.QueryRowContext(ctx, listingQuery,
		h.sanitizer.SanitizeString(input.Name),
		h.sanitizer.SanitizeString(input.Slug),
		h.sanitizer.SanitizeString(input.ShortDescription),
		h.sanitizer.SanitizeString(input.Description),
		input.FoundedYear, input.TrustedSeller, input.Verified,
		encryptedEmail,
		h.sanitizer.SanitizeString(input.LogoURL),
		h.sanitizer.SanitizeString(input.WebsiteURL),
		input.IsSponsored,
		createdBy, now, now,
	).Scan(&listingID, &createdAt, &updatedAt)

	if err != nil {
		return nil, fmt.Errorf("%w: insert listing: %v", ErrDatabaseError, err)
	}

	franchiseQuery := `
		INSERT INTO franchises (
			id, total_outlets, outlet_range, parent_company, business_type, 
			established_year, units_count, leader_name, leader_role
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9
		)`

	_, err = tx.ExecContext(ctx, franchiseQuery,
		listingID,
		input.TotalOutlets, h.sanitizer.SanitizeString(input.OutletRange),
		h.sanitizer.SanitizeString(input.ParentCompany),
		h.sanitizer.SanitizeString(input.BusinessType),
		input.EstablishedYear, input.UnitsCount,
		h.sanitizer.SanitizeString(input.LeaderName),
		h.sanitizer.SanitizeString(input.LeaderRole),
	)

	if err != nil {
		return nil, fmt.Errorf("%w: insert franchise: %v", ErrDatabaseError, err)
	}

	// NEW: If social URLs provided, insert into listing_social_links table
	if input.InstagramURL != "" || input.FacebookURL != "" ||
		input.TwitterURL != "" || input.LinkedinURL != "" {

		// ===== CRITICAL: Validate URLs before insertion =====
		if input.InstagramURL != "" {
			if err := h.validateURL("instagramURL", input.InstagramURL); err != nil {
				return nil, err
			}
		}
		if input.FacebookURL != "" {
			if err := h.validateURL("facebookURL", input.FacebookURL); err != nil {
				return nil, err
			}
		}
		if input.TwitterURL != "" {
			if err := h.validateURL("twitterURL", input.TwitterURL); err != nil {
				return nil, err
			}
		}
		if input.LinkedinURL != "" {
			if err := h.validateURL("linkedinURL", input.LinkedinURL); err != nil {
				return nil, err
			}
		}

		socialQuery := `
			INSERT INTO listing_social_links (
				listing_id, instagram_url, facebook_url, 
				twitter_url, linkedin_url, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7)`

		_, err = tx.ExecContext(ctx, socialQuery,
			listingID,
			h.sanitizer.SanitizeString(input.InstagramURL),
			h.sanitizer.SanitizeString(input.FacebookURL),
			h.sanitizer.SanitizeString(input.TwitterURL),
			h.sanitizer.SanitizeString(input.LinkedinURL),
			now, now,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: insert social links: %v", ErrDatabaseError, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit transaction: %v", ErrDatabaseError, err)
	}

	return &FranchiseOutput{
		FranchiseID: listingID.String(),
		Slug:        input.Slug,
		Success:     true,
		Message:     "Franchise created successfully",
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}, nil
}

// ============================================================================
// UPDATE FRANCHISE
// ============================================================================
func (h *Handler) handleUpdateFranchise(ctx context.Context, variables string, updatedByStr string) (*FranchiseOutput, error) {
	var input UpdateFranchiseInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	updatedBy, err := uuid.Parse(updatedByStr)
	if err != nil {
		return nil, fmt.Errorf("%w: updated_by: %v", ErrInvalidUUID, err)
	}

	// ===== CRITICAL: Validate individual fields =====
	if input.Name != nil {
		if err := h.validateString("name", *input.Name, 2, 100); err != nil {
			return nil, err
		}
	}
	if input.Description != nil {
		if err := h.validateString("description", *input.Description, 10, 5000); err != nil {
			return nil, err
		}
	}
	if input.ContactEmail != nil {
		if err := h.validateEmail("contactEmail", *input.ContactEmail); err != nil {
			return nil, err
		}
	}
	if input.FoundedYear != nil {
		if err := h.validateYear("foundedYear", input.FoundedYear); err != nil {
			return nil, err
		}
	}
	if input.TotalOutlets != nil && *input.TotalOutlets < 0 {
		return nil, fmt.Errorf("%w: totalOutlets must be >= 0", ErrValidationError)
	}
	if input.Industry != nil {
		if err := h.validateString("industry", *input.Industry, 2, 100); err != nil {
			return nil, err
		}
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	// 1. Fetch current curation values to track audit changes
	var oldIsFeatured, oldIsSponsored bool
	var oldFeaturedOrder int
	var oldFeaturedStartAt, oldFeaturedExpiresAt sql.NullTime

	selectQuery := `
		SELECT is_featured, is_sponsored, featured_order, featured_start_at, featured_expires_at 
		FROM franchises WHERE id = $1 FOR UPDATE`
	err = tx.QueryRowContext(ctx, selectQuery, franchiseID).Scan(
		&oldIsFeatured, &oldIsSponsored, &oldFeaturedOrder, &oldFeaturedStartAt, &oldFeaturedExpiresAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: franchise_id=%s", ErrFranchiseNotFound, input.FranchiseID)
		}
		return nil, fmt.Errorf("%w: fetch old franchise values: %v", ErrDatabaseError, err)
	}

	// 2. Build dynamic UPDATE query
	query, args := h.buildUpdateQuery(franchiseID, updatedBy, &input)

	var updatedAt time.Time
	err = tx.QueryRowContext(ctx, query, args...).Scan(&updatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: update franchise: %v", ErrDatabaseError, err)
	}

	// 3. Compare and insert into featured_association_audit_log if changes exist
	oldValues := make(map[string]interface{})
	newValues := make(map[string]interface{})
	hasCurationChanges := false

	if input.IsFeatured != nil && *input.IsFeatured != oldIsFeatured {
		oldValues["is_featured"] = oldIsFeatured
		newValues["is_featured"] = *input.IsFeatured
		hasCurationChanges = true
	}
	if input.IsSponsored != nil && *input.IsSponsored != oldIsSponsored {
		oldValues["is_sponsored"] = oldIsSponsored
		newValues["is_sponsored"] = *input.IsSponsored
		hasCurationChanges = true
	}
	if input.FeaturedOrder != nil && *input.FeaturedOrder != oldFeaturedOrder {
		oldValues["featured_order"] = oldFeaturedOrder
		newValues["featured_order"] = *input.FeaturedOrder
		hasCurationChanges = true
	}
	if input.FeaturedStartAt != nil {
		var oldVal interface{}
		if oldFeaturedStartAt.Valid {
			oldVal = oldFeaturedStartAt.Time
		}
		if !oldFeaturedStartAt.Valid || !input.FeaturedStartAt.Equal(oldFeaturedStartAt.Time) {
			oldValues["featured_start_at"] = oldVal
			newValues["featured_start_at"] = *input.FeaturedStartAt
			hasCurationChanges = true
		}
	}
	if input.FeaturedExpiresAt != nil {
		var oldVal interface{}
		if oldFeaturedExpiresAt.Valid {
			oldVal = oldFeaturedExpiresAt.Time
		}
		if !oldFeaturedExpiresAt.Valid || !input.FeaturedExpiresAt.Equal(oldFeaturedExpiresAt.Time) {
			oldValues["featured_expires_at"] = oldVal
			newValues["featured_expires_at"] = *input.FeaturedExpiresAt
			hasCurationChanges = true
		}
	}

	if hasCurationChanges {
		oldBytes, _ := json.Marshal(oldValues)
		newBytes, _ := json.Marshal(newValues)

		auditQuery := `
			INSERT INTO featured_association_audit_log (
				franchise_id, action, actor_id, old_values, new_values, notes, created_at
			) VALUES (
				$1, 'UPDATE_CURATION_SETTINGS', $2, $3, $4, 'Admin updated curation settings', CURRENT_TIMESTAMP)`
		
		var actorIDVal interface{} = updatedBy
		if updatedBy == uuid.Nil {
			actorIDVal = nil
		}

		_, err = tx.ExecContext(ctx, auditQuery, franchiseID, actorIDVal, oldBytes, newBytes)
		if err != nil {
			return nil, fmt.Errorf("%w: insert curation audit log: %v", ErrDatabaseError, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit transaction: %v", ErrDatabaseError, err)
	}

	return &FranchiseOutput{
		FranchiseID: franchiseID.String(),
		Success:     true,
		Message:     "Franchise updated successfully",
		UpdatedAt:   updatedAt,
	}, nil
}

func (h *Handler) buildUpdateQuery(franchiseID, updatedBy uuid.UUID, input *UpdateFranchiseInput) (string, []interface{}) {
	query := "UPDATE franchises SET updated_by = $1, updated_at = $2"
	args := []interface{}{updatedBy, time.Now()}
	argPos := 3

	if input.Name != nil {
		query += fmt.Sprintf(", name = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.Name))
		argPos++
	}
	if input.FoundedYear != nil {
		query += fmt.Sprintf(", founded_year = $%d", argPos)
		args = append(args, *input.FoundedYear)
		argPos++
	}
	if input.TrustedSeller != nil {
		query += fmt.Sprintf(", trusted_seller = $%d", argPos)
		args = append(args, *input.TrustedSeller)
		argPos++
	}
	if input.TotalOutlets != nil {
		query += fmt.Sprintf(", total_outlets = $%d", argPos)
		args = append(args, *input.TotalOutlets)
		argPos++
	}
	if input.Industry != nil {
		query += fmt.Sprintf(", industry = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.Industry))
		argPos++
	}
	if input.Description != nil {
		query += fmt.Sprintf(", description = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.Description))
		argPos++
	}
	if input.ContactEmail != nil {
		query += fmt.Sprintf(", contact_email = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.ContactEmail))
		argPos++
	}
	if len(input.AssociationMetadata) > 0 {
		query += fmt.Sprintf(", association_metadata = $%d", argPos)
		// association_metadata is raw JSON, ensure we don't sanitize it blindly, but we should cast it
		args = append(args, input.AssociationMetadata)
		argPos++
	}
	if input.MemberCount != nil {
		query += fmt.Sprintf(", member_count = $%d", argPos)
		args = append(args, *input.MemberCount)
		argPos++
	}
	if input.MembershipFeeMin != nil {
		query += fmt.Sprintf(", membership_fee_min = $%d", argPos)
		args = append(args, *input.MembershipFeeMin)
		argPos++
	}
	if input.MembershipFeeMax != nil {
		query += fmt.Sprintf(", membership_fee_max = $%d", argPos)
		args = append(args, *input.MembershipFeeMax)
		argPos++
	}
	if input.ApprovedAt != nil {
		query += fmt.Sprintf(", approved_at = $%d", argPos)
		args = append(args, *input.ApprovedAt)
		argPos++
	}
	if input.WebsiteURL != nil {
		query += fmt.Sprintf(", website_url = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.WebsiteURL))
		argPos++
	}
	if input.IsFeatured != nil {
		query += fmt.Sprintf(", is_featured = $%d", argPos)
		args = append(args, *input.IsFeatured)
		argPos++
	}
	if input.FeaturedStartAt != nil {
		query += fmt.Sprintf(", featured_start_at = $%d", argPos)
		args = append(args, *input.FeaturedStartAt)
		argPos++
	}
	if input.FeaturedExpiresAt != nil {
		query += fmt.Sprintf(", featured_expires_at = $%d", argPos)
		args = append(args, *input.FeaturedExpiresAt)
		argPos++
	}
	if input.FeaturedOrder != nil {
		query += fmt.Sprintf(", featured_order = $%d", argPos)
		args = append(args, *input.FeaturedOrder)
		argPos++
	}
	if input.IsSponsored != nil {
		query += fmt.Sprintf(", is_sponsored = $%d", argPos)
		args = append(args, *input.IsSponsored)
		argPos++
	}

	query += fmt.Sprintf(" WHERE id = $%d RETURNING updated_at", argPos)
	args = append(args, franchiseID)

	return query, args
}

// ============================================================================
// GET FRANCHISE
// ============================================================================
func (h *Handler) handleGetFranchise(ctx context.Context, variables string) (*Franchise, error) {
	var input GetFranchiseInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	// ===== CRITICAL: Validate input =====
	if input.FranchiseID == "" && input.Slug == "" {
		return nil, fmt.Errorf("%w: either franchise_id or slug is required", ErrValidationError)
	}

	if input.FranchiseID != "" {
		if _, err := h.validateUUID("franchiseID", input.FranchiseID); err != nil {
			return nil, err
		}
	}

	if input.Slug != "" {
		if err := h.validateString("slug", input.Slug, 2, 100); err != nil {
			return nil, err
		}
	}

	query := `
		SELECT 
			f.id, f.name, f.slug, f.short_description, f.description,
			f.founded_year, f.trusted_seller, f.verified, f.total_outlets, 
			f.outlet_range, f.industry, f.parent_company, f.business_type, 
			f.established_year, f.units_count, f.leader_name, f.leader_role, 
			f.contact_email, f.logo_url,
			f.created_by, f.updated_by, f.created_at, f.updated_at,
			f.entity_type, f.association_metadata,
			f.member_count, f.membership_fee_min, f.membership_fee_max, f.approved_at,
			f.website_url, f.is_featured, f.featured_start_at, f.featured_expires_at, f.featured_order, f.is_sponsored,
			fs.rating, fs.rating_count, fs.follow_count, fs.likes_count,
			fs.view_count, fs.save_count, fs.share_count, fs.enquiry_count,
			fs.news_count
		FROM franchises f
		LEFT JOIN listing_stats fs ON f.id = fs.listing_id
		WHERE `

	var args []interface{}
	if input.FranchiseID != "" {
		query += "l.id = $1"
		franchiseID, _ := uuid.Parse(input.FranchiseID)
		args = append(args, franchiseID)
	} else if input.Slug != "" {
		query += "l.slug = $1"
		args = append(args, h.sanitizer.SanitizeString(input.Slug))
	} else {
		return nil, fmt.Errorf("%w: either franchise_id or slug is required", ErrValidationError)
	}

	var franchise Franchise
	var stats FranchiseStats
	var assocMetaStr sql.NullString
	var websiteUrlStr sql.NullString
	var featuredStartAt, featuredExpiresAt sql.NullTime

	err := h.db.QueryRowContext(ctx, query, args...).Scan(
		&franchise.ID, &franchise.Name, &franchise.Slug,
		&franchise.ShortDescription, &franchise.Description,
		&franchise.FoundedYear, &franchise.TrustedSeller, &franchise.Verified,
		&franchise.TotalOutlets, &franchise.OutletRange, &franchise.Industry,
		&franchise.ParentCompany, &franchise.BusinessType, &franchise.EstablishedYear,
		&franchise.UnitsCount, &franchise.LeaderName, &franchise.LeaderRole,
		&franchise.ContactEmail, &franchise.LogoURL,
		&franchise.CreatedBy, &franchise.UpdatedBy, &franchise.CreatedAt,
		&franchise.UpdatedAt,
		&franchise.EntityType, &assocMetaStr,
		&franchise.MemberCount, &franchise.MembershipFeeMin, &franchise.MembershipFeeMax, &franchise.ApprovedAt,
		&websiteUrlStr, &franchise.IsFeatured, &featuredStartAt, &featuredExpiresAt, &franchise.FeaturedOrder, &franchise.IsSponsored,
		&stats.Rating, &stats.RatingCount, &stats.FollowCount, &stats.LikesCount,
		&stats.ViewCount, &stats.SaveCount, &stats.ShareCount, &stats.EnquiryCount,
		&stats.NewsCount,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w", ErrFranchiseNotFound)
		}
		return nil, fmt.Errorf("%w: query franchise: %v", ErrDatabaseError, err)
	}

	if assocMetaStr.Valid && assocMetaStr.String != "" && assocMetaStr.String != "null" {
		franchise.AssociationMetadata = json.RawMessage(assocMetaStr.String)
	}
	if websiteUrlStr.Valid {
		franchise.WebsiteURL = &websiteUrlStr.String
	}
	if featuredStartAt.Valid {
		franchise.FeaturedStartAt = &featuredStartAt.Time
	}
	if featuredExpiresAt.Valid {
		franchise.FeaturedExpiresAt = &featuredExpiresAt.Time
	}

	franchise.Stats = &stats

	return &franchise, nil
}

// ============================================================================
// CREATE BUSINESS OVERVIEW
// ============================================================================
func (h *Handler) handleCreateBusinessOverview(ctx context.Context, variables string) (*BaseOutput, error) {
	var input CreateBusinessOverviewInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	createdBy, err := uuid.Parse(input.CreatedBy)
	if err != nil {
		return nil, fmt.Errorf("%w: created_by: %v", ErrInvalidUUID, err)
	}

	// ===== CRITICAL: Validate JSON fields for NoSQL injection =====
	if err := h.validateJSONField("products", input.Products); err != nil {
		return nil, err
	}
	if err := h.validateJSONField("services", input.Services); err != nil {
		return nil, err
	}

	productsJSON, _ := json.Marshal(input.Products)
	servicesJSON, _ := json.Marshal(input.Services)

	query := `
		INSERT INTO franchise_business_overview 
			(franchise_id, products, services, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`

	var id uuid.UUID
	now := time.Now()

	err = h.db.QueryRowContext(ctx, query, franchiseID, productsJSON, servicesJSON, createdBy, now, now).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("%w: insert business overview: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		ID:      id.String(),
		Success: true,
		Message: "Business overview created successfully",
	}, nil
}

// ============================================================================
// UPDATE BUSINESS OVERVIEW
// ============================================================================
func (h *Handler) handleUpdateBusinessOverview(ctx context.Context, variables string, updatedByStr string) (*BaseOutput, error) {
	var input UpdateBusinessOverviewInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	updatedBy, err := uuid.Parse(updatedByStr)
	if err != nil {
		return nil, fmt.Errorf("%w: updated_by: %v", ErrInvalidUUID, err)
	}

	query := "UPDATE franchise_business_overview SET updated_by = $1, updated_at = $2"
	args := []interface{}{updatedBy, time.Now()}
	argPos := 3

	if input.Products != nil {
		// ===== CRITICAL: Validate JSON before marshaling =====
		if err := h.validateJSONField("products", input.Products); err != nil {
			return nil, err
		}
		productsJSON, _ := json.Marshal(input.Products)
		query += fmt.Sprintf(", products = $%d", argPos)
		args = append(args, productsJSON)
		argPos++
	}

	if input.Services != nil {
		// ===== CRITICAL: Validate JSON before marshaling =====
		if err := h.validateJSONField("services", input.Services); err != nil {
			return nil, err
		}
		servicesJSON, _ := json.Marshal(input.Services)
		query += fmt.Sprintf(", services = $%d", argPos)
		args = append(args, servicesJSON)
		argPos++
	}

	query += fmt.Sprintf(" WHERE franchise_id = $%d", argPos)
	args = append(args, franchiseID)

	_, err = h.db.ExecContext(ctx, query, args...)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: franchise_id=%s", ErrFranchiseNotFound, input.FranchiseID)
		}
		return nil, fmt.Errorf("%w: update business overview: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		Success: true,
		Message: "Business overview updated successfully",
	}, nil
}

// ============================================================================
// CREATE INVESTMENT REQUIREMENT
// ============================================================================
func (h *Handler) handleCreateInvestment(ctx context.Context, variables string) (*BaseOutput, error) {
	var input CreateInvestmentInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	createdBy, err := uuid.Parse(input.CreatedBy)
	if err != nil {
		return nil, fmt.Errorf("%w: created_by: %v", ErrInvalidUUID, err)
	}

	// ===== CRITICAL: Validate numeric ranges =====
	if *input.InitialInvestmentMin < 0 || *input.InitialInvestmentMin > 10000000 {
		return nil, fmt.Errorf("%w: initialInvestmentMin must be between 0 and 10000000", ErrValidationError)
	}
	if *input.InitialInvestmentMax < *input.InitialInvestmentMin || *input.InitialInvestmentMax > 10000000 {
		return nil, fmt.Errorf("%w: initialInvestmentMax must be >= initialInvestmentMin and <= 10000000", ErrValidationError)
	}
	if *input.FranchiseFee < 0 || *input.FranchiseFee > 1000000 {
		return nil, fmt.Errorf("%w: franchiseFee must be between 0 and 1000000", ErrValidationError)
	}
	if *input.RoyaltyPercentage < 0 || *input.RoyaltyPercentage > 50 {
		return nil, fmt.Errorf("%w: royaltyPercentage must be between 0 and 50", ErrValidationError)
	}
	if *input.MarketingFeePercentage < 0 || *input.MarketingFeePercentage > 20 {
		return nil, fmt.Errorf("%w: marketingFeePercentage must be between 0 and 20", ErrValidationError)
	}
	if *input.ROIMinPercentage < 0 || *input.ROIMinPercentage > 1000 {
		return nil, fmt.Errorf("%w: roiMinPercentage must be between 0 and 1000", ErrValidationError)
	}
	if *input.ROIMaxPercentage < *input.ROIMinPercentage || *input.ROIMaxPercentage > 1000 {
		return nil, fmt.Errorf("%w: roiMaxPercentage must be >= roiMinPercentage and <= 1000", ErrValidationError)
	}
	if *input.MonthlyTurnoverMin < 0 || *input.MonthlyTurnoverMin > 10000000 {
		return nil, fmt.Errorf("%w: monthlyTurnoverMin must be between 0 and 10000000", ErrValidationError)
	}
	if *input.MonthlyTurnoverMax < *input.MonthlyTurnoverMin || *input.MonthlyTurnoverMax > 10000000 {
		return nil, fmt.Errorf("%w: monthlyTurnoverMax must be >= monthlyTurnoverMin and <= 10000000", ErrValidationError)
	}

	query := `
		INSERT INTO franchise_investment_requirement (
			franchise_id, initial_investment_min, initial_investment_max,
			franchise_fee, royalty_percentage, marketing_fee_percentage,
			payback_min_months, payback_max_months, 
			roi_min_percentage, roi_max_percentage,
			monthly_turnover_min, monthly_turnover_max,
			single_unit_cost_min, single_unit_cost_max,
			investment_includes,
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING id`

	var id uuid.UUID
	now := time.Now()

	err = h.db.QueryRowContext(ctx, query,
		franchiseID, input.InitialInvestmentMin, input.InitialInvestmentMax,
		input.FranchiseFee, input.RoyaltyPercentage, input.MarketingFeePercentage,
		input.PaybackMinMonths, input.PaybackMaxMonths,
		input.ROIMinPercentage, input.ROIMaxPercentage,
		input.MonthlyTurnoverMin, input.MonthlyTurnoverMax,
		input.SingleUnitCostMin, input.SingleUnitCostMax,
		h.sanitizer.SanitizeString(*input.InvestmentIncludes),
		createdBy, now, now,
	).Scan(&id)

	if err != nil {
		return nil, fmt.Errorf("%w: insert investment requirement: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		ID:      id.String(),
		Success: true,
		Message: "Investment requirement created successfully",
	}, nil
}

// ============================================================================
// UPDATE INVESTMENT (FIXED pointer issues)
// ============================================================================
func (h *Handler) handleUpdateInvestment(ctx context.Context, variables string, updatedByStr string) (*BaseOutput, error) {
	var input UpdateInvestmentInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	updatedBy, err := uuid.Parse(updatedByStr)
	if err != nil {
		return nil, fmt.Errorf("%w: updated_by: %v", ErrInvalidUUID, err)
	}

	query := "UPDATE franchise_investment_requirement SET updated_by = $1, updated_at = $2"
	args := []interface{}{updatedBy, time.Now()}
	argPos := 3

	if input.PaybackMinMonths != nil {
		if *input.PaybackMinMonths < 0 || *input.PaybackMinMonths > 240 {
			return nil, fmt.Errorf("%w: paybackMinMonths must be between 0 and 240", ErrValidationError)
		}
		query += fmt.Sprintf(", payback_min_months = $%d", argPos)
		args = append(args, *input.PaybackMinMonths)
		argPos++
	}
	if input.PaybackMaxMonths != nil {
		if *input.PaybackMaxMonths < 0 || *input.PaybackMaxMonths > 240 {
			return nil, fmt.Errorf("%w: paybackMaxMonths must be between 0 and 240", ErrValidationError)
		}
		query += fmt.Sprintf(", payback_max_months = $%d", argPos)
		args = append(args, *input.PaybackMaxMonths)
		argPos++
	}
	if input.ROIMinPercentage != nil {
		if *input.ROIMinPercentage < 0 || *input.ROIMinPercentage > 1000 {
			return nil, fmt.Errorf("%w: roiMinPercentage must be between 0 and 1000", ErrValidationError)
		}
		query += fmt.Sprintf(", roi_min_percentage = $%d", argPos)
		args = append(args, *input.ROIMinPercentage)
		argPos++
	}
	if input.ROIMaxPercentage != nil {
		if *input.ROIMaxPercentage < 0 || *input.ROIMaxPercentage > 1000 {
			return nil, fmt.Errorf("%w: roiMaxPercentage must be between 0 and 1000", ErrValidationError)
		}
		query += fmt.Sprintf(", roi_max_percentage = $%d", argPos)
		args = append(args, *input.ROIMaxPercentage)
		argPos++
	}
	if input.MonthlyTurnoverMin != nil {
		if *input.MonthlyTurnoverMin < 0 || *input.MonthlyTurnoverMin > 10000000 {
			return nil, fmt.Errorf("%w: monthlyTurnoverMin must be between 0 and 10000000", ErrValidationError)
		}
		query += fmt.Sprintf(", monthly_turnover_min = $%d", argPos)
		args = append(args, *input.MonthlyTurnoverMin)
		argPos++
	}
	if input.MonthlyTurnoverMax != nil {
		if *input.MonthlyTurnoverMax < 0 || *input.MonthlyTurnoverMax > 10000000 {
			return nil, fmt.Errorf("%w: monthlyTurnoverMax must be between 0 and 10000000", ErrValidationError)
		}
		query += fmt.Sprintf(", monthly_turnover_max = $%d", argPos)
		args = append(args, *input.MonthlyTurnoverMax)
		argPos++
	}
	if input.SingleUnitCostMin != nil {
		if *input.SingleUnitCostMin < 0 || *input.SingleUnitCostMin > 1000000 {
			return nil, fmt.Errorf("%w: singleUnitCostMin must be between 0 and 1000000", ErrValidationError)
		}
		query += fmt.Sprintf(", single_unit_cost_min = $%d", argPos)
		args = append(args, *input.SingleUnitCostMin)
		argPos++
	}
	if input.SingleUnitCostMax != nil {
		if *input.SingleUnitCostMax < 0 || *input.SingleUnitCostMax > 1000000 {
			return nil, fmt.Errorf("%w: singleUnitCostMax must be between 0 and 1000000", ErrValidationError)
		}
		query += fmt.Sprintf(", single_unit_cost_max = $%d", argPos)
		args = append(args, *input.SingleUnitCostMax)
		argPos++
	}

	if input.InitialInvestmentMin != nil {
		if *input.InitialInvestmentMin < 0 || *input.InitialInvestmentMin > 10000000 {
			return nil, fmt.Errorf("%w: initialInvestmentMin must be between 0 and 10000000", ErrValidationError)
		}
		query += fmt.Sprintf(", initial_investment_min = $%d", argPos)
		args = append(args, *input.InitialInvestmentMin)
		argPos++
	}
	if input.InitialInvestmentMax != nil {
		if *input.InitialInvestmentMax < 0 || *input.InitialInvestmentMax > 10000000 {
			return nil, fmt.Errorf("%w: initialInvestmentMax must be between 0 and 10000000", ErrValidationError)
		}
		query += fmt.Sprintf(", initial_investment_max = $%d", argPos)
		args = append(args, *input.InitialInvestmentMax)
		argPos++
	}
	if input.FranchiseFee != nil {
		if *input.FranchiseFee < 0 || *input.FranchiseFee > 1000000 {
			return nil, fmt.Errorf("%w: franchiseFee must be between 0 and 1000000", ErrValidationError)
		}
		query += fmt.Sprintf(", franchise_fee = $%d", argPos)
		args = append(args, *input.FranchiseFee)
		argPos++
	}
	if input.RoyaltyPercentage != nil {
		if *input.RoyaltyPercentage < 0 || *input.RoyaltyPercentage > 50 {
			return nil, fmt.Errorf("%w: royaltyPercentage must be between 0 and 50", ErrValidationError)
		}
		query += fmt.Sprintf(", royalty_percentage = $%d", argPos)
		args = append(args, *input.RoyaltyPercentage)
		argPos++
	}
	if input.MarketingFeePercentage != nil {
		if *input.MarketingFeePercentage < 0 || *input.MarketingFeePercentage > 20 {
			return nil, fmt.Errorf("%w: marketingFeePercentage must be between 0 and 20", ErrValidationError)
		}
		query += fmt.Sprintf(", marketing_fee_percentage = $%d", argPos)
		args = append(args, *input.MarketingFeePercentage)
		argPos++
	}

	if input.InvestmentIncludes != nil {
		if err := h.validateString("investmentIncludes", *input.InvestmentIncludes, 0, 2000); err != nil {
			return nil, err
		}
		query += fmt.Sprintf(", investment_includes = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.InvestmentIncludes))
		argPos++
	}

	query += fmt.Sprintf(" WHERE franchise_id = $%d", argPos)
	args = append(args, franchiseID)

	_, err = h.db.ExecContext(ctx, query, args...)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: franchise_id=%s", ErrFranchiseNotFound, input.FranchiseID)
		}
		return nil, fmt.Errorf("%w: update investment: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		Success: true,
		Message: "Investment updated successfully",
	}, nil
}

// ============================================================================
// CREATE OPERATIONS (FIXED pointer issues)
// ============================================================================
func (h *Handler) handleCreateOperations(ctx context.Context, variables string) (*BaseOutput, error) {
	var input CreateOperationsInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	createdBy, err := uuid.Parse(input.CreatedBy)
	if err != nil {
		return nil, fmt.Errorf("%w: created_by: %v", ErrInvalidUUID, err)
	}

	// ===== CRITICAL: Validate JSON fields =====
	if err := h.validateJSONField("staffBreakdown", input.StaffBreakdown); err != nil {
		return nil, err
	}

	// ===== CRITICAL: Validate numeric ranges =====
	if *input.SpaceMinSqft < 0 || *input.SpaceMinSqft > 100000 {
		return nil, fmt.Errorf("%w: spaceMinSqft must be between 0 and 100000", ErrValidationError)
	}
	if *input.SpaceMaxSqft < 0 || *input.SpaceMaxSqft > 100000 {
		return nil, fmt.Errorf("%w: spaceMaxSqft must be between 0 and 100000", ErrValidationError)
	}
	if *input.SpaceMaxSqft < *input.SpaceMinSqft {
		return nil, fmt.Errorf("%w: spaceMaxSqft must be >= spaceMinSqft", ErrValidationError)
	}

	// Convert staff breakdown to JSONB
	staffBreakdownJSON, _ := json.Marshal(input.StaffBreakdown)

	query := `
		INSERT INTO franchise_operations (
			franchise_id, space_min_sqft, space_max_sqft, 
			required_property_type, staff_required_min, staff_required_max,
			staff_breakdown, operating_hours, training_provided, 
			training_details, computer_requirements, marketing_support,
			preferred_locations, qualification_required,
			supply_chain_support, quality_control, 
			created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		RETURNING id`

	var id uuid.UUID
	now := time.Now()

	err = h.db.QueryRowContext(ctx, query,
		franchiseID, input.SpaceMinSqft, input.SpaceMaxSqft,
		h.sanitizer.SanitizeString(*input.RequiredPropertyType),
		input.StaffRequiredMin, input.StaffRequiredMax,
		staffBreakdownJSON,
		h.sanitizer.SanitizeString(*input.OperatingHours),
		input.TrainingProvided,
		h.sanitizer.SanitizeString(*input.TrainingDetails),
		h.sanitizer.SanitizeString(*input.ComputerRequirements),
		h.sanitizer.SanitizeString(*input.MarketingSupport),
		h.sanitizer.SanitizeString(*input.PreferredLocations),
		h.sanitizer.SanitizeString(*input.QualificationRequired),
		input.SupplyChainSupport,
		input.QualityControl,
		createdBy, now, now,
	).Scan(&id)

	if err != nil {
		return nil, fmt.Errorf("%w: insert operations: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		ID:      id.String(),
		Success: true,
		Message: "Operations created successfully",
	}, nil
}

// ============================================================================
// UPDATE OPERATIONS (FIXED pointer issues)
// ============================================================================
func (h *Handler) handleUpdateOperations(ctx context.Context, variables string, updatedByStr string) (*BaseOutput, error) {
	var input UpdateOperationsInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	updatedBy, err := uuid.Parse(updatedByStr)
	if err != nil {
		return nil, fmt.Errorf("%w: updated_by: %v", ErrInvalidUUID, err)
	}

	query := "UPDATE franchise_operations SET updated_by = $1, updated_at = $2"
	args := []interface{}{updatedBy, time.Now()}
	argPos := 3

	if input.SpaceMinSqft != nil {
		if *input.SpaceMinSqft < 0 || *input.SpaceMinSqft > 100000 {
			return nil, fmt.Errorf("%w: spaceMinSqft must be between 0 and 100000", ErrValidationError)
		}
		query += fmt.Sprintf(", space_min_sqft = $%d", argPos)
		args = append(args, *input.SpaceMinSqft)
		argPos++
	}
	if input.SpaceMaxSqft != nil {
		if *input.SpaceMaxSqft < 0 || *input.SpaceMaxSqft > 100000 {
			return nil, fmt.Errorf("%w: spaceMaxSqft must be between 0 and 100000", ErrValidationError)
		}
		query += fmt.Sprintf(", space_max_sqft = $%d", argPos)
		args = append(args, *input.SpaceMaxSqft)
		argPos++
	}
	if input.TrainingProvided != nil {
		query += fmt.Sprintf(", training_provided = $%d", argPos)
		args = append(args, *input.TrainingProvided)
		argPos++
	}
	if input.RequiredPropertyType != nil {
		if err := h.validateString("requiredPropertyType", *input.RequiredPropertyType, 0, 100); err != nil {
			return nil, err
		}
		query += fmt.Sprintf(", required_property_type = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.RequiredPropertyType))
		argPos++
	}
	if input.StaffRequiredMin != nil {
		if *input.StaffRequiredMin < 0 || *input.StaffRequiredMin > 1000 {
			return nil, fmt.Errorf("%w: staffRequiredMin must be between 0 and 1000", ErrValidationError)
		}
		query += fmt.Sprintf(", staff_required_min = $%d", argPos)
		args = append(args, *input.StaffRequiredMin)
		argPos++
	}
	if input.StaffRequiredMax != nil {
		if *input.StaffRequiredMax < 0 || *input.StaffRequiredMax > 1000 {
			return nil, fmt.Errorf("%w: staffRequiredMax must be between 0 and 1000", ErrValidationError)
		}
		query += fmt.Sprintf(", staff_required_max = $%d", argPos)
		args = append(args, *input.StaffRequiredMax)
		argPos++
	}
	if input.StaffBreakdown != nil {
		if err := h.validateJSONField("staffBreakdown", input.StaffBreakdown); err != nil {
			return nil, err
		}
		staffJSON, _ := json.Marshal(input.StaffBreakdown)
		query += fmt.Sprintf(", staff_breakdown = $%d", argPos)
		args = append(args, staffJSON)
		argPos++
	}
	if input.OperatingHours != nil {
		if err := h.validateString("operatingHours", *input.OperatingHours, 0, 200); err != nil {
			return nil, err
		}
		query += fmt.Sprintf(", operating_hours = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.OperatingHours))
		argPos++
	}
	if input.TrainingDetails != nil {
		if err := h.validateString("trainingDetails", *input.TrainingDetails, 0, 1000); err != nil {
			return nil, err
		}
		query += fmt.Sprintf(", training_details = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.TrainingDetails))
		argPos++
	}
	if input.ComputerRequirements != nil {
		if err := h.validateString("computerRequirements", *input.ComputerRequirements, 0, 500); err != nil {
			return nil, err
		}
		query += fmt.Sprintf(", computer_requirements = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.ComputerRequirements))
		argPos++
	}
	if input.MarketingSupport != nil {
		if err := h.validateString("marketingSupport", *input.MarketingSupport, 0, 1000); err != nil {
			return nil, err
		}
		query += fmt.Sprintf(", marketing_support = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.MarketingSupport))
		argPos++
	}
	if input.PreferredLocations != nil {
		if err := h.validateString("preferredLocations", *input.PreferredLocations, 0, 500); err != nil {
			return nil, err
		}
		query += fmt.Sprintf(", preferred_locations = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.PreferredLocations))
		argPos++
	}
	if input.QualificationRequired != nil {
		if err := h.validateString("qualificationRequired", *input.QualificationRequired, 0, 500); err != nil {
			return nil, err
		}
		query += fmt.Sprintf(", qualification_required = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.QualificationRequired))
		argPos++
	}
	// FIXED: SupplyChainSupport and QualityControl are *bool not *string
	if input.SupplyChainSupport != nil {
		query += fmt.Sprintf(", supply_chain_support = $%d", argPos)
		args = append(args, *input.SupplyChainSupport)
		argPos++
	}
	if input.QualityControl != nil {
		query += fmt.Sprintf(", quality_control = $%d", argPos)
		args = append(args, *input.QualityControl)
		argPos++
	}

	query += fmt.Sprintf(" WHERE franchise_id = $%d", argPos)
	args = append(args, franchiseID)

	_, err = h.db.ExecContext(ctx, query, args...)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: franchise_id=%s", ErrFranchiseNotFound, input.FranchiseID)
		}
		return nil, fmt.Errorf("%w: update operations: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		Success: true,
		Message: "Operations updated successfully",
	}, nil
}

// ============================================================================
// CREATE SOCIAL LINKS
// ============================================================================
func (h *Handler) handleCreateSocialLinks(ctx context.Context, variables string) (*BaseOutput, error) {
	var input CreateSocialLinksInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	// ===== CRITICAL: Validate URLs =====
	if input.InstagramURL != "" {
		if err := h.validateURL("instagramURL", input.InstagramURL); err != nil {
			return nil, err
		}
	}
	if input.FacebookURL != "" {
		if err := h.validateURL("facebookURL", input.FacebookURL); err != nil {
			return nil, err
		}
	}
	if input.TwitterURL != "" {
		if err := h.validateURL("twitterURL", input.TwitterURL); err != nil {
			return nil, err
		}
	}
	if input.LinkedinURL != "" {
		if err := h.validateURL("linkedinURL", input.LinkedinURL); err != nil {
			return nil, err
		}
	}

	query := `
		INSERT INTO listing_social_links (
			listing_id, instagram_url, facebook_url, 
			twitter_url, linkedin_url, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`

	var id uuid.UUID
	now := time.Now()

	err = h.db.QueryRowContext(ctx, query,
		franchiseID,
		h.sanitizer.SanitizeString(input.InstagramURL),
		h.sanitizer.SanitizeString(input.FacebookURL),
		h.sanitizer.SanitizeString(input.TwitterURL),
		h.sanitizer.SanitizeString(input.LinkedinURL),
		now, now,
	).Scan(&id)

	if err != nil {
		return nil, fmt.Errorf("%w: insert social links: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		ID:      id.String(),
		Success: true,
		Message: "Social links created successfully",
	}, nil
}

// ============================================================================
// UPDATE SOCIAL LINKS
// ============================================================================
func (h *Handler) handleUpdateSocialLinks(ctx context.Context, variables string) (*BaseOutput, error) {
	var input UpdateSocialLinksInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	query := "UPDATE listing_social_links SET updated_at = $1"
	args := []interface{}{time.Now()}
	argPos := 2

	if input.InstagramURL != nil {
		if *input.InstagramURL != "" {
			if err := h.validateURL("instagramURL", *input.InstagramURL); err != nil {
				return nil, err
			}
		}
		query += fmt.Sprintf(", instagram_url = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.InstagramURL))
		argPos++
	}
	if input.FacebookURL != nil {
		if *input.FacebookURL != "" {
			if err := h.validateURL("facebookURL", *input.FacebookURL); err != nil {
				return nil, err
			}
		}
		query += fmt.Sprintf(", facebook_url = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.FacebookURL))
		argPos++
	}
	if input.TwitterURL != nil {
		if *input.TwitterURL != "" {
			if err := h.validateURL("twitterURL", *input.TwitterURL); err != nil {
				return nil, err
			}
		}
		query += fmt.Sprintf(", twitter_url = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.TwitterURL))
		argPos++
	}
	if input.LinkedinURL != nil {
		if *input.LinkedinURL != "" {
			if err := h.validateURL("linkedinURL", *input.LinkedinURL); err != nil {
				return nil, err
			}
		}
		query += fmt.Sprintf(", linkedin_url = $%d", argPos)
		args = append(args, h.sanitizer.SanitizeString(*input.LinkedinURL))
		argPos++
	}

	query += fmt.Sprintf(" WHERE listing_id = $%d", argPos)
	args = append(args, franchiseID)

	_, err = h.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: update social links: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		Success: true,
		Message: "Social links updated successfully",
	}, nil
}

// ============================================================================
// CREATE FRANCHISE STATS (FIXED pointer issues)
// ============================================================================
func (h *Handler) handleCreateFranchiseStats(ctx context.Context, variables string) (*BaseOutput, error) {
	var input CreateFranchiseStatsInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	// ===== CRITICAL: Validate numeric ranges =====
	if *input.Rating < 0 || *input.Rating > 5 {
		return nil, fmt.Errorf("%w: rating must be between 0 and 5", ErrValidationError)
	}
	if input.RatingCount < 0 || input.RatingCount > 1000000 {
		return nil, fmt.Errorf("%w: ratingCount must be between 0 and 1000000", ErrValidationError)
	}
	if input.FollowCount < 0 || input.FollowCount > 10000000 {
		return nil, fmt.Errorf("%w: followCount must be between 0 and 10000000", ErrValidationError)
	}
	if input.LikesCount < 0 || input.LikesCount > 10000000 {
		return nil, fmt.Errorf("%w: likesCount must be between 0 and 10000000", ErrValidationError)
	}
	if input.ViewCount < 0 || input.ViewCount > 100000000 {
		return nil, fmt.Errorf("%w: viewCount must be between 0 and 100000000", ErrValidationError)
	}
	if input.SaveCount < 0 || input.SaveCount > 1000000 {
		return nil, fmt.Errorf("%w: saveCount must be between 0 and 1000000", ErrValidationError)
	}
	if input.ShareCount < 0 || input.ShareCount > 1000000 {
		return nil, fmt.Errorf("%w: shareCount must be between 0 and 1000000", ErrValidationError)
	}
	if input.EnquiryCount < 0 || input.EnquiryCount > 100000 {
		return nil, fmt.Errorf("%w: enquiryCount must be between 0 and 100000", ErrValidationError)
	}
	if input.NewsCount < 0 || input.NewsCount > 10000 {
		return nil, fmt.Errorf("%w: newsCount must be between 0 and 10000", ErrValidationError)
	}

	query := `
		INSERT INTO listing_stats (
			franchise_id, rating, rating_count, follow_count, 
			likes_count, view_count, save_count, share_count,
			enquiry_count, news_count, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id`

	var id uuid.UUID
	now := time.Now()

	err = h.db.QueryRowContext(ctx, query,
		franchiseID, input.Rating, input.RatingCount, input.FollowCount,
		input.LikesCount, input.ViewCount, input.SaveCount, input.ShareCount,
		input.EnquiryCount, input.NewsCount, now, now,
	).Scan(&id)

	if err != nil {
		return nil, fmt.Errorf("%w: insert franchise stats: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		ID:      id.String(),
		Success: true,
		Message: "Franchise stats created successfully",
	}, nil
}

// ============================================================================
// UPDATE FRANCHISE STATS (FIXED pointer issues)
// ============================================================================
func (h *Handler) handleUpdateFranchiseStats(ctx context.Context, variables string) (*BaseOutput, error) {
	var input UpdateFranchiseStatsInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	query := "UPDATE listing_stats SET updated_at = $1"
	args := []interface{}{time.Now()}
	argPos := 2

	if input.ViewCount != nil {
		if *input.ViewCount < 0 || *input.ViewCount > 100000 {
			return nil, fmt.Errorf("%w: viewCount must be between 0 and 100000", ErrValidationError)
		}
		query += fmt.Sprintf(", view_count = view_count + $%d", argPos)
		args = append(args, *input.ViewCount)
		argPos++
	}
	if input.LikesCount != nil {
		if *input.LikesCount < 0 || *input.LikesCount > 100000 {
			return nil, fmt.Errorf("%w: likesCount must be between 0 and 100000", ErrValidationError)
		}
		query += fmt.Sprintf(", likes_count = likes_count + $%d", argPos)
		args = append(args, *input.LikesCount)
		argPos++
	}
	if input.SaveCount != nil {
		if *input.SaveCount < 0 || *input.SaveCount > 100000 {
			return nil, fmt.Errorf("%w: saveCount must be between 0 and 100000", ErrValidationError)
		}
		query += fmt.Sprintf(", save_count = save_count + $%d", argPos)
		args = append(args, *input.SaveCount)
		argPos++
	}
	if input.ShareCount != nil {
		if *input.ShareCount < 0 || *input.ShareCount > 100000 {
			return nil, fmt.Errorf("%w: shareCount must be between 0 and 100000", ErrValidationError)
		}
		query += fmt.Sprintf(", share_count = share_count + $%d", argPos)
		args = append(args, *input.ShareCount)
		argPos++
	}
	if input.FollowCount != nil {
		if *input.FollowCount < 0 || *input.FollowCount > 100000 {
			return nil, fmt.Errorf("%w: followCount must be between 0 and 100000", ErrValidationError)
		}
		query += fmt.Sprintf(", follow_count = follow_count + $%d", argPos)
		args = append(args, *input.FollowCount)
		argPos++
	}

	query += fmt.Sprintf(" WHERE franchise_id = $%d", argPos)
	args = append(args, franchiseID)

	_, err = h.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: update franchise stats: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		Success: true,
		Message: "Franchise stats updated successfully",
	}, nil
}

// ============================================================================
// CATEGORY QUESTIONS OPERATIONS
// ============================================================================

func (h *Handler) handleCreateCategoryQuestion(ctx context.Context, variables string) (*BaseOutput, error) {
	var input CreateCategoryQuestionInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidationError, err)
	}

	// ===== CRITICAL: Validate strings =====
	if err := h.validateString("categoryName", input.CategoryName, 2, 100); err != nil {
		return nil, err
	}
	if err := h.validateString("question", input.Question, 10, 500); err != nil {
		return nil, err
	}
	if err := h.validateString("answer", input.Answer, 10, 2000); err != nil {
		return nil, err
	}
	if input.DisplayOrder < 0 || input.DisplayOrder > 1000 {
		return nil, fmt.Errorf("%w: displayOrder must be between 0 and 1000", ErrValidationError)
	}

	query := `
		INSERT INTO category_questions (
			category_name, question, answer, display_order, created_at
		) VALUES ($1, $2, $3, $4, $5)
		RETURNING id`

	var id uuid.UUID
	now := time.Now()

	err := h.db.QueryRowContext(ctx, query,
		h.sanitizer.SanitizeString(input.CategoryName),
		h.sanitizer.SanitizeString(input.Question),
		h.sanitizer.SanitizeString(input.Answer),
		input.DisplayOrder, now,
	).Scan(&id)

	if err != nil {
		return nil, fmt.Errorf("%w: insert category question: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		ID:      id.String(),
		Success: true,
		Message: "Category question created successfully",
	}, nil
}

func (h *Handler) handleGetCategoryQuestions(ctx context.Context, variables string) (*CategoryQuestionsOutput, error) {
	var input GetCategoryQuestionsInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	// ===== CRITICAL: Validate category name =====
	if err := h.validateString("categoryName", input.CategoryName, 2, 100); err != nil {
		return nil, err
	}

	query := `
		SELECT id, category_name, question, answer, display_order, created_at
		FROM category_questions 
		WHERE category_name = $1
		ORDER BY display_order ASC, created_at ASC`

	rows, err := h.db.QueryContext(ctx, query, h.sanitizer.SanitizeString(input.CategoryName))
	if err != nil {
		return nil, fmt.Errorf("%w: query category questions: %v", ErrDatabaseError, err)
	}
	defer rows.Close()

	var questions []CategoryQuestion
	for rows.Next() {
		var q CategoryQuestion
		err := rows.Scan(&q.ID, &q.CategoryName, &q.Question, &q.Answer,
			&q.DisplayOrder, &q.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: scan category question: %v", ErrDatabaseError, err)
		}
		questions = append(questions, q)
	}

	return &CategoryQuestionsOutput{
		Questions: questions,
		Success:   true,
		Message:   fmt.Sprintf("Found %d questions", len(questions)),
	}, nil
}

func (h *Handler) handleUpdateCategoryQuestion(ctx context.Context, variables string) (*BaseOutput, error) {
	var input UpdateCategoryQuestionInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	questionID, err := uuid.Parse(input.QuestionID)
	if err != nil {
		return nil, fmt.Errorf("%w: question_id: %v", ErrInvalidUUID, err)
	}

	query := "UPDATE category_questions SET "
	args := []interface{}{}
	argPos := 1
	updates := []string{}

	if input.Question != nil {
		if err := h.validateString("question", *input.Question, 10, 500); err != nil {
			return nil, err
		}
		updates = append(updates, fmt.Sprintf("question = $%d", argPos))
		args = append(args, h.sanitizer.SanitizeString(*input.Question))
		argPos++
	}
	if input.Answer != nil {
		if err := h.validateString("answer", *input.Answer, 10, 2000); err != nil {
			return nil, err
		}
		updates = append(updates, fmt.Sprintf("answer = $%d", argPos))
		args = append(args, h.sanitizer.SanitizeString(*input.Answer))
		argPos++
	}
	if input.DisplayOrder != nil {
		if *input.DisplayOrder < 0 || *input.DisplayOrder > 1000 {
			return nil, fmt.Errorf("%w: displayOrder must be between 0 and 1000", ErrValidationError)
		}
		updates = append(updates, fmt.Sprintf("display_order = $%d", argPos))
		args = append(args, *input.DisplayOrder)
		argPos++
	}

	if len(updates) == 0 {
		return nil, fmt.Errorf("%w: no fields to update", ErrValidationError)
	}

	query += strings.Join(updates, ", ")
	query += fmt.Sprintf(" WHERE id = $%d", argPos)
	args = append(args, questionID)

	_, err = h.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: update category question: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		Success: true,
		Message: "Category question updated successfully",
	}, nil
}

func (h *Handler) handleDeleteCategoryQuestion(ctx context.Context, variables string) (*BaseOutput, error) {
	var input DeleteCategoryQuestionInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	questionID, err := uuid.Parse(input.QuestionID)
	if err != nil {
		return nil, fmt.Errorf("%w: question_id: %v", ErrInvalidUUID, err)
	}

	query := `DELETE FROM category_questions WHERE id = $1`
	result, err := h.db.ExecContext(ctx, query, questionID)
	if err != nil {
		return nil, fmt.Errorf("%w: delete category question: %v", ErrDatabaseError, err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return nil, fmt.Errorf("%w: question not found", ErrFranchiseNotFound)
	}

	return &BaseOutput{
		Success: true,
		Message: "Category question deleted successfully",
	}, nil
}

// ============================================================================
// FRANCHISE CITIES OPERATIONS
// ============================================================================

func (h *Handler) handleCreateFranchiseCity(ctx context.Context, variables string) (*BaseOutput, error) {
	var input CreateFranchiseCityInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	// ===== CRITICAL: Validate location strings =====
	if err := h.validateString("city", input.City, 2, 100); err != nil {
		return nil, err
	}
	if err := h.validateString("state", input.State, 2, 100); err != nil {
		return nil, err
	}
	if err := h.validateString("country", input.Country, 2, 100); err != nil {
		return nil, err
	}

	query := `
		INSERT INTO listing_cities (
			franchise_id, city, state, country, created_at
		) VALUES ($1, $2, $3, $4, $5)
		RETURNING id`

	var id uuid.UUID
	now := time.Now()

	err = h.db.QueryRowContext(ctx, query,
		franchiseID,
		h.sanitizer.SanitizeString(input.City),
		h.sanitizer.SanitizeString(input.State),
		h.sanitizer.SanitizeString(input.Country),
		now,
	).Scan(&id)

	if err != nil {
		return nil, fmt.Errorf("%w: insert franchise city: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		ID:      id.String(),
		Success: true,
		Message: "Franchise city created successfully",
	}, nil
}

func (h *Handler) handleGetFranchiseCities(ctx context.Context, variables string) (*FranchiseCitiesOutput, error) {
	var input GetFranchiseCitiesInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	query := `
		SELECT id, franchise_id, city, state, country, created_at
		FROM listing_cities 
		WHERE franchise_id = $1
		ORDER BY city ASC`

	rows, err := h.db.QueryContext(ctx, query, franchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: query franchise cities: %v", ErrDatabaseError, err)
	}
	defer rows.Close()

	var cities []FranchiseCity
	for rows.Next() {
		var c FranchiseCity
		err := rows.Scan(&c.ID, &c.FranchiseID, &c.City, &c.State, &c.Country, &c.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: scan franchise city: %v", ErrDatabaseError, err)
		}
		cities = append(cities, c)
	}

	return &FranchiseCitiesOutput{
		Cities:  cities,
		Success: true,
		Message: fmt.Sprintf("Found %d cities", len(cities)),
	}, nil
}

func (h *Handler) handleDeleteFranchiseCity(ctx context.Context, variables string) (*BaseOutput, error) {
	var input DeleteFranchiseCityInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	cityID, err := uuid.Parse(input.CityID)
	if err != nil {
		return nil, fmt.Errorf("%w: city_id: %v", ErrInvalidUUID, err)
	}

	query := `DELETE FROM listing_cities WHERE id = $1`
	result, err := h.db.ExecContext(ctx, query, cityID)
	if err != nil {
		return nil, fmt.Errorf("%w: delete franchise city: %v", ErrDatabaseError, err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return nil, fmt.Errorf("%w: city not found", ErrFranchiseNotFound)
	}

	return &BaseOutput{
		Success: true,
		Message: "Franchise city deleted successfully",
	}, nil
}

// ============================================================================
// GET FULL FRANCHISE (WITH ALL RELATED DATA)
// ============================================================================
func (h *Handler) handleGetFullFranchise(ctx context.Context, variables string) (*FullFranchise, error) {
	var input GetFranchiseInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	// Get main franchise data (with stats already joined)
	franchise, err := h.handleGetFranchise(ctx, variables)
	if err != nil {
		return nil, err
	}

	franchiseUUID, _ := uuid.Parse(franchise.ID)

	fullFranchise := &FullFranchise{
		Franchise: *franchise,
		Stats:     franchise.Stats,
	}

	// Get business overview
	var overview BusinessOverview
	var productsStr, servicesStr string
	query := `SELECT id, products, services FROM franchise_business_overview WHERE franchise_id = $1`
	err = h.db.QueryRowContext(ctx, query, franchiseUUID).Scan(
		&overview.ID, &productsStr, &servicesStr)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("%w: query business overview: %v", ErrDatabaseError, err)
	}
	if err == nil {
		overview.Products = json.RawMessage(productsStr)
		overview.Services = json.RawMessage(servicesStr)
		fullFranchise.BusinessOverview = &overview
	}

	// Get investment requirement
	var investment Investment
	var revenueModelStr sql.NullString
	query = `
        SELECT 
            id, initial_investment_min, initial_investment_max, 
            franchise_fee, royalty_percentage, marketing_fee_percentage,
            payback_min_months, payback_max_months,
            roi_min_percentage, roi_max_percentage,
            monthly_turnover_min, monthly_turnover_max,
            single_unit_cost_min, single_unit_cost_max,
            investment_includes, revenue_model
        FROM franchise_investment_requirement 
        WHERE franchise_id = $1`

	err = h.db.QueryRowContext(ctx, query, franchiseUUID).Scan(
		&investment.ID, &investment.InitialInvestmentMin, &investment.InitialInvestmentMax,
		&investment.FranchiseFee, &investment.RoyaltyPercentage, &investment.MarketingFeePercentage,
		&investment.PaybackMinMonths, &investment.PaybackMaxMonths,
		&investment.ROIMinPercentage, &investment.ROIMaxPercentage,
		&investment.MonthlyTurnoverMin, &investment.MonthlyTurnoverMax,
		&investment.SingleUnitCostMin, &investment.SingleUnitCostMax,
		&investment.InvestmentIncludes, &revenueModelStr,
	)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("%w: query investment: %v", ErrDatabaseError, err)
	}
	if err == nil {
		if revenueModelStr.Valid && revenueModelStr.String != "" && revenueModelStr.String != "null" {
			investment.RevenueModel = json.RawMessage(revenueModelStr.String)
		}
		fullFranchise.Investment = &investment
	}

	// Get operations
	var operations Operations
	var staffBreakdownStr string
	var territoryStr, devScheduleStr, supportStr, legalStr sql.NullString
	query = `
        SELECT 
            id, space_min_sqft, space_max_sqft, required_property_type,
            staff_required_min, staff_required_max, staff_breakdown,
            operating_hours, training_provided, training_details,
            computer_requirements, marketing_support, preferred_locations,
            qualification_required, supply_chain_support, quality_control,
            territory_details, development_schedule, support_training, legal_compliance
        FROM franchise_operations 
        WHERE franchise_id = $1`

	err = h.db.QueryRowContext(ctx, query, franchiseUUID).Scan(
		&operations.ID, &operations.SpaceMinSqft, &operations.SpaceMaxSqft,
		&operations.RequiredPropertyType, &operations.StaffRequiredMin,
		&operations.StaffRequiredMax, &staffBreakdownStr,
		&operations.OperatingHours, &operations.TrainingProvided,
		&operations.TrainingDetails, &operations.ComputerRequirements,
		&operations.MarketingSupport, &operations.PreferredLocations,
		&operations.QualificationRequired, &operations.SupplyChainSupport,
		&operations.QualityControl,
		&territoryStr, &devScheduleStr, &supportStr, &legalStr,
	)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("%w: query operations: %v", ErrDatabaseError, err)
	}
	if err == nil {
		operations.StaffBreakdown = json.RawMessage(staffBreakdownStr)
		if territoryStr.Valid && territoryStr.String != "" && territoryStr.String != "null" {
			operations.TerritoryDetails = json.RawMessage(territoryStr.String)
		}
		if devScheduleStr.Valid && devScheduleStr.String != "" && devScheduleStr.String != "null" {
			operations.DevelopmentSchedule = json.RawMessage(devScheduleStr.String)
		}
		if supportStr.Valid && supportStr.String != "" && supportStr.String != "null" {
			operations.SupportTraining = json.RawMessage(supportStr.String)
		}
		if legalStr.Valid && legalStr.String != "" && legalStr.String != "null" {
			operations.LegalCompliance = json.RawMessage(legalStr.String)
		}
		fullFranchise.Operations = &operations
	}

	// Get social links
	var socialLinks SocialLinks
	query = `
        SELECT id, instagram_url, facebook_url, twitter_url, linkedin_url
        FROM listing_social_links 
        WHERE listing_id = $1`

	err = h.db.QueryRowContext(ctx, query, franchiseUUID).Scan(
		&socialLinks.ID, &socialLinks.InstagramURL, &socialLinks.FacebookURL,
		&socialLinks.TwitterURL, &socialLinks.LinkedinURL,
	)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("%w: query social links: %v", ErrDatabaseError, err)
	}
	if err == nil {
		fullFranchise.SocialLinks = &socialLinks
	}

	// Get franchise cities
	query = `
        SELECT id, franchise_id, city, state, country, created_at
        FROM listing_cities 
        WHERE franchise_id = $1
        ORDER BY city ASC`

	rows, err := h.db.QueryContext(ctx, query, franchiseUUID)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("%w: query cities: %v", ErrDatabaseError, err)
	}
	if err == nil {
		defer rows.Close()
		var cities []FranchiseCity
		for rows.Next() {
			var city FranchiseCity
			err := rows.Scan(&city.ID, &city.FranchiseID, &city.City,
				&city.State, &city.Country, &city.CreatedAt)
			if err != nil {
				return nil, fmt.Errorf("%w: scan city: %v", ErrDatabaseError, err)
			}
			cities = append(cities, city)
		}
		if len(cities) > 0 {
			fullFranchise.Cities = cities
		}
	}

	return fullFranchise, nil
}

// ============================================================================
// DELETE FRANCHISE (HARD DELETE)
// ============================================================================
func (h *Handler) handleDeleteFranchise(ctx context.Context, variables string, _ string) (*BaseOutput, error) {
	var input DeleteFranchiseInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
	}

	// Hard delete - no need for updatedBy
	query := `DELETE FROM franchises WHERE id = $1`
	result, err := h.db.ExecContext(ctx, query, franchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: delete franchise: %v", ErrDatabaseError, err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return nil, fmt.Errorf("%w: franchise_id=%s", ErrFranchiseNotFound, input.FranchiseID)
	}

	return &BaseOutput{
		Success: true,
		Message: "Franchise deleted successfully",
	}, nil
}

// HandleAddToFavorites adds franchise to user favorites with idempotency
func (h *Handler) HandleAddToFavorites(ctx context.Context, userID, franchiseID string) (*BaseOutput, error) {
	// ===== STEP 1: VALIDATE INPUTS =====
	if _, err := h.validateUUID("userId", userID); err != nil {
		return nil, err
	}
	if _, err := h.validateUUID("franchiseId", franchiseID); err != nil {
		return nil, err
	}

	// ===== STEP 2: GENERATE IDEMPOTENCY KEY =====
	idempotencyKey := h.keyGenerator.GenerateFavoriteKey(userID, franchiseID)

	h.logger.Info("Generated idempotency key", map[string]interface{}{
		"idempotencyKey": idempotencyKey,
		"userId":         userID,
		"franchiseId":    franchiseID,
	})

	// ===== STEP 3: CHECK IF FAVORITE ALREADY EXISTS =====
	exists, existingFavID, err := h.idempotencyChecker.CheckFavoriteExists(ctx, userID, franchiseID)
	if err != nil {
		h.logger.Error("Failed to check existing favorite", map[string]interface{}{
			"error": err.Error(),
		})
	}

	if exists {
		h.logger.Warn("Favorite already exists", map[string]interface{}{
			"favoriteId":  existingFavID,
			"userId":      userID,
			"franchiseId": franchiseID,
		})

		return &BaseOutput{
			ID:      existingFavID,
			Success: true,
			Message: "Favorite already exists",
		}, nil
	}

	// ===== STEP 4: MARK AS PROCESSING =====
	err = h.idempotencyChecker.MarkProcessing(ctx, idempotencyKey, "add-to-favorites", 1*time.Hour)
	if err != nil {
		h.logger.Error("Failed to mark as processing", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// ===== STEP 5: BEGIN TRANSACTION =====
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	favoriteID := uuid.New().String()

	// ===== STEP 6: INSERT WITH ON CONFLICT =====
	insertQuery := `
        INSERT INTO user_favorites (id, user_id, franchise_id, created_at)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (user_id, franchise_id) DO NOTHING
        RETURNING id`

	var returnedID string
	err = tx.QueryRowContext(ctx, insertQuery,
		favoriteID,
		userID,
		franchiseID,
		time.Now(),
	).Scan(&returnedID)

	if err == sql.ErrNoRows {
		h.logger.Warn("Concurrent favorite creation detected", map[string]interface{}{
			"userId":      userID,
			"franchiseId": franchiseID,
		})

		var existingID string
		queryErr := h.db.QueryRowContext(ctx, `
            SELECT id FROM user_favorites
            WHERE user_id = $1 AND franchise_id = $2
        `, userID, franchiseID).Scan(&existingID)

		if queryErr == nil {
			return &BaseOutput{
				ID:      existingID,
				Success: true,
				Message: "Favorite created by concurrent request",
			}, nil
		}

		return nil, fmt.Errorf("%w: concurrent conflict", ErrDatabaseError)
	}

	if err != nil {
		h.idempotencyChecker.MarkFailed(ctx, idempotencyKey)
		return nil, fmt.Errorf("%w: insert favorite: %v", ErrDatabaseError, err)
	}

	// ===== STEP 7: UPDATE FRANCHISE STATS =====
	_, err = tx.ExecContext(ctx, `
        UPDATE listing_stats
        SET save_count = save_count + 1, updated_at = $1
        WHERE franchise_id = $2
    `, time.Now(), franchiseID)

	if err != nil {
		h.logger.Error("Failed to update franchise stats", map[string]interface{}{
			"error":       err.Error(),
			"franchiseId": franchiseID,
		})
	}

	// ===== STEP 8: COMMIT TRANSACTION =====
	if err := tx.Commit(); err != nil {
		h.idempotencyChecker.MarkFailed(ctx, idempotencyKey)
		return nil, fmt.Errorf("%w: commit transaction: %v", ErrDatabaseError, err)
	}

	// ===== STEP 9: MARK AS COMPLETED =====
	responseData := map[string]interface{}{
		"favoriteId": favoriteID,
		"createdAt":  time.Now().Format(time.RFC3339),
	}

	err = h.idempotencyChecker.MarkCompleted(ctx, idempotencyKey, responseData)
	if err != nil {
		h.logger.Error("Failed to mark as completed", map[string]interface{}{
			"error": err.Error(),
		})
	}

	h.logger.Info("Successfully added to favorites", map[string]interface{}{
		"favoriteId":  favoriteID,
		"userId":      userID,
		"franchiseId": franchiseID,
	})

	return &BaseOutput{
		ID:      favoriteID,
		Success: true,
		Message: "Added to favorites successfully",
	}, nil
}

// ============================================================
// BOOKMARK HANDLERS
// ============================================================

// handleAddBookmark — adds entity to user bookmarks (user_favorites table)
func (h *Handler) handleAddBookmark(ctx context.Context, variables string) (*BookmarkOutput, error) {
	var input BookmarkInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserID)
	if err != nil {
		return nil, err
	}
	
	targetID := input.EntityID
	if targetID == "" {
		targetID = input.FranchiseID
	}
	entityID, err := h.validateUUID("entityId", targetID)
	if err != nil {
		return nil, err
	}
	
	entityType := normalizeEntityType(input.EntityType, "franchise")

	// Idempotency key generate karo
	idempotencyKey := h.keyGenerator.GenerateFavoriteKey(input.UserID, targetID)

	// Check if already bookmarked
	var existingID string
	queryErr := h.db.QueryRowContext(ctx, `
		SELECT id FROM user_favorites WHERE user_id = $1 AND entity_id = $2 AND entity_type = $3`,
		userID, entityID, entityType,
	).Scan(&existingID)

	if queryErr == nil {
		return &BookmarkOutput{
			ID:           existingID,
			UserID:       input.UserID,
			FranchiseID:  input.FranchiseID, // backward compatibility
			EntityID:     targetID,
			EntityType:   entityType,
			IsBookmarked: true,
			Success:      true,
			Message:      "Already bookmarked",
		}, nil
	}

	// Mark as processing
	_ = h.idempotencyChecker.MarkProcessing(ctx, idempotencyKey, "add-bookmark", 1*time.Hour)

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	bookmarkID := uuid.New().String()

	// INSERT with ON CONFLICT DO NOTHING — idempotent
	var returnedID string
	insertErr := tx.QueryRowContext(ctx, `
		INSERT INTO user_favorites (id, user_id, entity_id, entity_type, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, entity_id, entity_type) DO NOTHING
		RETURNING id`,
		bookmarkID, userID, entityID, entityType, time.Now(),
	).Scan(&returnedID)

	if insertErr == sql.ErrNoRows {
		// Concurrent insert — fetch existing
		var existID string
		_ = h.db.QueryRowContext(ctx, `
			SELECT id FROM user_favorites WHERE user_id=$1 AND entity_id=$2 AND entity_type=$3`,
			userID, entityID, entityType,
		).Scan(&existID)
		return &BookmarkOutput{
			ID: existID, UserID: input.UserID, FranchiseID: input.FranchiseID,
			EntityID: targetID, EntityType: entityType,
			IsBookmarked: true, Success: true, Message: "Already bookmarked",
		}, nil
	}
	if insertErr != nil {
		_ = h.idempotencyChecker.MarkFailed(ctx, idempotencyKey)
		return nil, fmt.Errorf("%w: insert bookmark: %v", ErrDatabaseError, insertErr)
	}

	// Update entity_stats.save_count +1
	_, _ = tx.ExecContext(ctx, `
		UPDATE listing_stats SET save_count = save_count + 1, updated_at = $1
		WHERE franchise_id = $2`,
		time.Now(), entityID,
	)

	if err := tx.Commit(); err != nil {
		_ = h.idempotencyChecker.MarkFailed(ctx, idempotencyKey)
		return nil, fmt.Errorf("%w: commit: %v", ErrDatabaseError, err)
	}

	_ = h.idempotencyChecker.MarkCompleted(ctx, idempotencyKey, map[string]interface{}{
		"bookmarkId": bookmarkID,
	})

	return &BookmarkOutput{
		ID:           bookmarkID,
		UserID:       input.UserID,
		FranchiseID:  input.FranchiseID,
		EntityID:     targetID,
		EntityType:   entityType,
		IsBookmarked: true,
		Success:      true,
		Message:      "Bookmark added successfully",
	}, nil
}

// handleRemoveBookmark — removes entity from user bookmarks
func (h *Handler) handleRemoveBookmark(ctx context.Context, variables string) (*BookmarkOutput, error) {
	var input BookmarkInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserID)
	if err != nil {
		return nil, err
	}
	targetID := input.EntityID
	if targetID == "" {
		targetID = input.FranchiseID
	}
	entityID, err := h.validateUUID("entityId", targetID)
	if err != nil {
		return nil, err
	}
	entityType := normalizeEntityType(input.EntityType, "franchise")

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		DELETE FROM user_favorites WHERE user_id = $1 AND entity_id = $2 AND entity_type = $3`,
		userID, entityID, entityType,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: delete bookmark: %v", ErrDatabaseError, err)
	}

	rowsAffected, _ := result.RowsAffected()

	// Decrement save_count only if actually deleted
	if rowsAffected > 0 {
		_, _ = tx.ExecContext(ctx, `
			UPDATE listing_stats
			SET save_count = GREATEST(save_count - 1, 0), updated_at = $1
			WHERE franchise_id = $2`,
			time.Now(), entityID,
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit: %v", ErrDatabaseError, err)
	}

	return &BookmarkOutput{
		UserID:       input.UserID,
		FranchiseID:  input.FranchiseID,
		EntityID:     targetID,
		EntityType:   entityType,
		IsBookmarked: false,
		Success:      true,
		Message:      "Bookmark removed successfully",
	}, nil
}

// handleGetUserBookmarks — get all bookmarked entities for a user
func (h *Handler) handleGetUserBookmarks(ctx context.Context, variables string) (*GetBookmarksOutput, error) {
	var input GetBookmarksInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserID)
	if err != nil {
		return nil, err
	}

	entityType := normalizeEntityType(input.EntityType, "all")

	// Defaults
	if input.Page <= 0 {
		input.Page = 1
	}
	if input.Limit <= 0 || input.Limit > 50 {
		input.Limit = 20
	}
	offset := (input.Page - 1) * input.Limit

	// Total count
	var totalCount int
	_ = h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_favorites WHERE user_id = $1 AND ($2 = 'all' OR entity_type = $2)`, userID, entityType,
	).Scan(&totalCount)

	// Fetch bookmarks with entity info
	rows, err := h.db.QueryContext(ctx, `
		SELECT
			uf.id as bookmark_id,
			uf.entity_id,
			uf.entity_type,
			COALESCE(f.name, '') as name,
			COALESCE(f.slug, '') as slug,
			COALESCE(f.logo_url_circle, f.logo_url_square, f.logo_url, '') as logo_url,
			COALESCE(
				(SELECT i.name FROM listing_categories fc JOIN categories c ON fc.category_id = c.id JOIN industries i ON c.industry_id = i.id WHERE fc.listing_id = f.id AND fc.is_primary = true LIMIT 1), 
				f.industry,
				''
			) as industry,
			uf.created_at as bookmarked_at
		FROM user_favorites uf
		LEFT JOIN franchises f ON uf.entity_id = f.id
		WHERE uf.user_id = $1 AND ($2 = 'all' OR uf.entity_type = $2)
		ORDER BY uf.created_at DESC
		LIMIT $3 OFFSET $4`,
		userID, entityType, input.Limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: query bookmarks: %v", ErrDatabaseError, err)
	}
	defer rows.Close()

	var bookmarks []BookmarkedFranchise
	for rows.Next() {
		var b BookmarkedFranchise
		var dbEntityType string
		if err := rows.Scan(
			&b.BookmarkID, &b.EntityID, &dbEntityType, &b.Name,
			&b.Slug, &b.LogoURL, &b.Industry, &b.BookmarkedAt,
		); err != nil {
			return nil, fmt.Errorf("%w: scan bookmark: %v", ErrDatabaseError, err)
		}
		b.FranchiseID = b.EntityID // backward compatibility
		b.EntityType = dbEntityType
		bookmarks = append(bookmarks, b)
	}

	return &GetBookmarksOutput{
		Bookmarks:  bookmarks,
		TotalCount: totalCount,
		Page:       input.Page,
		Limit:      input.Limit,
		Success:    true,
		Message:    fmt.Sprintf("Found %d bookmarks", totalCount),
	}, nil
}

// handleCheckBookmark — check if user has bookmarked a franchise
func (h *Handler) handleCheckBookmark(ctx context.Context, variables string) (*CheckBookmarkOutput, error) {
	var input CheckBookmarkInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserID)
	if err != nil {
		return nil, err
	}
	targetID := input.EntityID
	if targetID == "" {
		targetID = input.FranchiseID
	}
	entityID, err := h.validateUUID("entityId", targetID)
	if err != nil {
		return nil, err
	}
	entityType := normalizeEntityType(input.EntityType, "franchise")

	var bookmarkID string
	queryErr := h.db.QueryRowContext(ctx, `
		SELECT id FROM user_favorites WHERE user_id = $1 AND entity_id = $2 AND entity_type = $3`,
		userID, entityID, entityType,
	).Scan(&bookmarkID)

	if queryErr == sql.ErrNoRows {
		return &CheckBookmarkOutput{IsBookmarked: false, Success: true}, nil
	}
	if queryErr != nil {
		return nil, fmt.Errorf("%w: check bookmark: %v", ErrDatabaseError, queryErr)
	}

	return &CheckBookmarkOutput{
		IsBookmarked: true,
		BookmarkID:   bookmarkID,
		Success:      true,
	}, nil
}

// ============================================================
// RATING HANDLERS
// ============================================================

// handleSubmitUserRating — submit or upsert a user rating
func (h *Handler) handleSubmitUserRating(ctx context.Context, variables string) (*RatingOutput, error) {
	var input SubmitRatingInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	// Validate input
	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidationError, err)
	}

	userID, err := h.validateUUID("userId", input.UserID)
	if err != nil {
		return nil, err
	}
	targetID := input.EntityID
	if targetID == "" {
		targetID = input.FranchiseID
	}
	entityID, err := h.validateUUID("entityId", targetID)
	if err != nil {
		return nil, err
	}
	entityType := normalizeEntityType(input.EntityType, "franchise")

	// Sanitize review text
	sanitizedReview := h.sanitizer.SanitizeString(input.Review)
	if err := h.validateString("review", sanitizedReview, 0, 2000); err != nil {
		return nil, err
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	ratingID := uuid.New().String()
	now := time.Now()
	isNew := false

	// UPSERT — ON CONFLICT update existing rating
	var returnedID string
	var createdAt time.Time
	err = tx.QueryRowContext(ctx, `
		INSERT INTO user_ratings (id, user_id, entity_id, entity_type, rating, review, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id, entity_id, entity_type)
		DO UPDATE SET
			rating     = EXCLUDED.rating,
			review     = EXCLUDED.review,
			updated_at = EXCLUDED.updated_at
		RETURNING id, created_at`,
		ratingID, userID, entityID, entityType, input.Rating, sanitizedReview, now, now,
	).Scan(&returnedID, &createdAt)

	if err != nil {
		return nil, fmt.Errorf("%w: upsert rating: %v", ErrDatabaseError, err)
	}

	// returnedID == ratingID means newly inserted
	isNew = (returnedID == ratingID)

	// Recalculate avg rating in stats
	_, err = tx.ExecContext(ctx, `
		UPDATE listing_stats 
		SET
			rating       = (SELECT AVG(rating) FROM user_ratings WHERE entity_id = $1),
			rating_count = (SELECT COUNT(*) FROM user_ratings WHERE entity_id = $1),
			updated_at   = $2
		WHERE franchise_id = $1`,
		entityID, now,
	)
	
	if err != nil {
		h.logger.Error("Failed to update entity rating stats", map[string]interface{}{
			"entityId": targetID,
			"error":    err.Error(),
		})
		// Non-fatal — continue
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit: %v", ErrDatabaseError, err)
	}

	msg := "Rating updated successfully"
	if isNew {
		msg = "Rating submitted successfully"
	}

	return &RatingOutput{
		ID:          returnedID,
		UserID:      input.UserID,
		FranchiseID: input.FranchiseID, // backwards compatibility
		EntityID:    targetID,
		EntityType:  entityType,
		Rating:      input.Rating,
		Review:      sanitizedReview,
		IsNew:       isNew,
		Success:     true,
		Message:     msg,
		CreatedAt:   createdAt,
	}, nil
}

// handleUpdateUserRating — partial update of existing rating
func (h *Handler) handleUpdateUserRating(ctx context.Context, variables string) (*RatingOutput, error) {
	var input UpdateRatingInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserID)
	if err != nil {
		return nil, err
	}
	targetID := input.EntityID
	if targetID == "" {
		targetID = input.FranchiseID
	}
	entityID, err := h.validateUUID("entityId", targetID)
	if err != nil {
		return nil, err
	}
	entityType := normalizeEntityType(input.EntityType, "franchise")

	query := "UPDATE user_ratings SET updated_at = $1"
	args := []interface{}{time.Now()}
	argPos := 2

	if input.Rating != nil {
		if *input.Rating < 1.0 || *input.Rating > 5.0 {
			return nil, fmt.Errorf("%w: rating must be between 1.0 and 5.0", ErrValidationError)
		}
		query += fmt.Sprintf(", rating = $%d", argPos)
		args = append(args, *input.Rating)
		argPos++
	}

	if input.Review != nil {
		sanitized := h.sanitizer.SanitizeString(*input.Review)
		if len(sanitized) > 2000 {
			return nil, fmt.Errorf("%w: review must not exceed 2000 characters", ErrValidationError)
		}
		query += fmt.Sprintf(", review = $%d", argPos)
		args = append(args, sanitized)
		argPos++
	}

	query += fmt.Sprintf(" WHERE user_id = $%d AND entity_id = $%d AND entity_type = $%d RETURNING id, rating, review",
		argPos, argPos+1, argPos+2)
	args = append(args, userID, entityID, entityType)

	var retID string
	var retRating float64
	var retReview sql.NullString
	err = h.db.QueryRowContext(ctx, query, args...).Scan(&retID, &retRating, &retReview)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: rating not found for this user-entity pair", ErrFranchiseNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: update rating: %v", ErrDatabaseError, err)
	}

	// Recalculate stats
	_, _ = h.db.ExecContext(ctx, `
		UPDATE listing_stats
		SET rating = (SELECT AVG(rating) FROM user_ratings WHERE entity_id = $1),
			updated_at = $2
		WHERE franchise_id = $1`,
		entityID, time.Now(),
	)

	review := ""
	if retReview.Valid {
		review = retReview.String
	}

	return &RatingOutput{
		ID: retID, UserID: input.UserID, FranchiseID: input.FranchiseID,
		EntityID: targetID, EntityType: entityType,
		Rating: retRating, Review: review, IsNew: false,
		Success: true, Message: "Rating updated successfully",
	}, nil
}

// handleGetUserRating — get a specific user's rating for an entity
func (h *Handler) handleGetUserRating(ctx context.Context, variables string) (*GetUserRatingOutput, error) {
	var input GetUserRatingInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserID)
	if err != nil {
		return nil, err
	}
	targetID := input.EntityID
	if targetID == "" {
		targetID = input.FranchiseID
	}
	entityID, err := h.validateUUID("entityId", targetID)
	if err != nil {
		return nil, err
	}
	entityType := normalizeEntityType(input.EntityType, "franchise")

	var ratingID string
	var rating float64
	var review sql.NullString
	var updatedAt time.Time

	err = h.db.QueryRowContext(ctx, `
		SELECT id, rating, review, updated_at
		FROM user_ratings
		WHERE user_id = $1 AND entity_id = $2 AND entity_type = $3`,
		userID, entityID, entityType,
	).Scan(&ratingID, &rating, &review, &updatedAt)

	if err == sql.ErrNoRows {
		return &GetUserRatingOutput{HasRated: false, Success: true, Message: "No rating found"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: get user rating: %v", ErrDatabaseError, err)
	}

	reviewStr := ""
	if review.Valid {
		reviewStr = review.String
	}

	return &GetUserRatingOutput{
		HasRated:  true,
		Rating:    rating,
		Review:    reviewStr,
		RatingID:  ratingID,
		UpdatedAt: updatedAt,
		Success:   true,
		Message:   "Rating found",
	}, nil
}

// handleGetFranchiseRatings — get all ratings for an entity (paginated)
func (h *Handler) handleGetFranchiseRatings(ctx context.Context, variables string) (*GetFranchiseRatingsOutput, error) {
	var input GetFranchiseRatingsInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	targetID := input.EntityID
	if targetID == "" {
		targetID = input.FranchiseID
	}
	entityID, err := h.validateUUID("entityId", targetID)
	if err != nil {
		return nil, err
	}
	entityType := normalizeEntityType(input.EntityType, "franchise")

	if input.Page <= 0 {
		input.Page = 1
	}
	if input.Limit <= 0 || input.Limit > 50 {
		input.Limit = 10
	}
	offset := (input.Page - 1) * input.Limit

	// Count + avg
	var totalCount int
	var avgRating sql.NullFloat64
	_ = h.db.QueryRowContext(ctx, `
		SELECT COUNT(*), AVG(rating) FROM user_ratings WHERE entity_id = $1 AND entity_type = $2`,
		entityID, entityType,
	).Scan(&totalCount, &avgRating)

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, user_id, rating, COALESCE(review, ''), created_at
		FROM user_ratings
		WHERE entity_id = $1 AND entity_type = $2
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4`,
		entityID, entityType, input.Limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: get entity ratings: %v", ErrDatabaseError, err)
	}
	defer rows.Close()

	var ratings []RatingItem
	for rows.Next() {
		var r RatingItem
		if err := rows.Scan(&r.ID, &r.UserID, &r.Rating, &r.Review, &r.CreatedAt); err != nil {
			continue
		}
		ratings = append(ratings, r)
	}

	avg := 0.0
	if avgRating.Valid {
		avg = avgRating.Float64
	}

	return &GetFranchiseRatingsOutput{
		Ratings:    ratings,
		TotalCount: totalCount,
		AvgRating:  avg,
		Page:       input.Page,
		Limit:      input.Limit,
		Success:    true,
		Message:    fmt.Sprintf("Found %d ratings", totalCount),
	}, nil
}

// handleDeleteUserRating — user apni rating delete kar sakta hai
func (h *Handler) handleDeleteUserRating(ctx context.Context, variables string) (*BaseOutput, error) {
	var input GetUserRatingInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserID)
	if err != nil {
		return nil, err
	}
	targetID := input.EntityID
	if targetID == "" {
		targetID = input.FranchiseID
	}
	entityID, err := h.validateUUID("entityId", targetID)
	if err != nil {
		return nil, err
	}
	entityType := normalizeEntityType(input.EntityType, "franchise")

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin tx: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		DELETE FROM user_ratings WHERE user_id = $1 AND entity_id = $2 AND entity_type = $3`,
		userID, entityID, entityType,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: delete rating: %v", ErrDatabaseError, err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return nil, fmt.Errorf("%w: rating not found", ErrFranchiseNotFound)
	}

	// Recalculate stats
	_, _ = tx.ExecContext(ctx, `
		UPDATE listing_stats
		SET rating       = (SELECT AVG(rating) FROM user_ratings WHERE entity_id = $1),
			rating_count = (SELECT COUNT(*) FROM user_ratings WHERE entity_id = $1),
			updated_at   = $2
		WHERE franchise_id = $1`,
		entityID, time.Now(),
	)

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{Success: true, Message: "Rating deleted successfully"}, nil
}

// ============================================================
// SHARE HANDLERS
// ============================================================

// handleShareFranchise — records a share event + increments share_count
func (h *Handler) handleShareFranchise(ctx context.Context, variables string) (*ShareOutput, error) {
	var input ShareFranchiseInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidationError, err)
	}

	targetID := input.EntityID
	if targetID == "" {
		targetID = input.FranchiseID
	}
	entityID, err := h.validateUUID("entityId", targetID)
	if err != nil {
		return nil, err
	}
	entityType := normalizeEntityType(input.EntityType, "franchise")

	// userID optional — nil if anonymous
	var userIDPtr interface{}
	if input.UserID != "" {
		uid, err := h.validateUUID("userId", input.UserID)
		if err != nil {
			return nil, err
		}
		userIDPtr = uid
	}

	// Sanitize platform
	platform := h.sanitizer.SanitizeString(input.SharePlatform)
	if platform == "" {
		platform = "copy_link"
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin tx: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	shareID := uuid.New().String()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO franchise_shares (id, user_id, entity_id, entity_type, share_platform, ip_address, shared_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		shareID, userIDPtr, entityID, entityType, platform,
		h.sanitizer.SanitizeString(input.IPAddress),
		time.Now(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: insert share: %v", ErrDatabaseError, err)
	}

	// Increment share_count in stats
	_, _ = tx.ExecContext(ctx, `
		UPDATE listing_stats
		SET share_count = share_count + 1, updated_at = $1
		WHERE franchise_id = $2`,
		time.Now(), entityID,
	)

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit: %v", ErrDatabaseError, err)
	}

	return &ShareOutput{
		ShareID:       shareID,
		FranchiseID:   input.FranchiseID, // backwards compatibility
		EntityID:      targetID,
		EntityType:    entityType,
		SharePlatform: platform,
		Success:       true,
		Message:       "Share recorded successfully",
	}, nil
}

// handleGetUserShares — get user's share history
func (h *Handler) handleGetUserShares(ctx context.Context, variables string) (*GetUserSharesOutput, error) {
	var input GetUserSharesInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	userID, err := h.validateUUID("userId", input.UserID)
	if err != nil {
		return nil, err
	}

	if input.Page <= 0 {
		input.Page = 1
	}
	if input.Limit <= 0 || input.Limit > 50 {
		input.Limit = 20
	}
	offset := (input.Page - 1) * input.Limit

	var totalCount int
	_ = h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM franchise_shares WHERE user_id = $1`, userID,
	).Scan(&totalCount)

	rows, err := h.db.QueryContext(ctx, `
		SELECT
			fs.id,
			fs.entity_id,
			fs.entity_type,
			COALESCE(f.name, '') as entity_name,
			fs.share_platform,
			fs.shared_at
		FROM franchise_shares fs
		LEFT JOIN franchises f ON fs.entity_id = f.id
		WHERE fs.user_id = $1
		ORDER BY fs.shared_at DESC
		LIMIT $2 OFFSET $3`,
		userID, input.Limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: get user shares: %v", ErrDatabaseError, err)
	}
	defer rows.Close()

	var shares []ShareItem
	for rows.Next() {
		var s ShareItem
		var dbEntityType string
		if err := rows.Scan(&s.ShareID, &s.EntityID, &dbEntityType, &s.EntityName, &s.SharePlatform, &s.SharedAt); err != nil {
			continue
		}
		s.FranchiseID = s.EntityID // backward compatibility
		s.FranchiseName = s.EntityName
		s.EntityType = dbEntityType
		shares = append(shares, s)
	}

	return &GetUserSharesOutput{
		Shares:     shares,
		TotalCount: totalCount,
		Success:    true,
		Message:    fmt.Sprintf("Found %d shares", totalCount),
	}, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================
func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, result interface{}) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.GetKey()).
		VariablesFromObject(result)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("Failed to create complete job command", map[string]interface{}{
			"error":   err,
			"jobKey":  job.GetKey(),
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
		return
	}

	_, err = cmd.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("Failed to send complete job command", map[string]interface{}{
			"error":   err,
			"jobKey":  job.GetKey(),
			"traceId": trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		})
	}
}

func (h *Handler) failJob(ctx context.Context, client worker.JobClient, job entities.Job, errorCode, errorMessage string, retries int32) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(fmt.Errorf("%s: %s", errorCode, errorMessage))
	span.SetAttributes(attribute.String("error.code", errorCode))

	stdErr := &appErrs.StandardError{
		Code:      appErrs.ErrorCode(errorCode),
		Message:   errorMessage,
		Retryable: retries > 0,
	}
	h.errorHandler.HandleJobError(ctx, client, job, stdErr)
}

func (h *Handler) saveContactMessage(ctx context.Context, variables string) (map[string]interface{}, error) {
	var input struct {
		ContactName    string `json:"contactName"`
		ContactEmail   string `json:"contactEmail"`
		ContactMessage string `json:"contactMessage"`
		ContactCompany string `json:"contactCompany"`
		ContactPhone   string `json:"contactPhone"`
		IpAddress      string `json:"ipAddress"`
	}

	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	if input.ContactName == "" {
		return nil, fmt.Errorf("contactName is required")
	}
	if input.ContactEmail == "" {
		return nil, fmt.Errorf("contactEmail is required")
	}
	if input.ContactMessage == "" {
		return nil, fmt.Errorf("contactMessage is required")
	}

	var id uuid.UUID
	query := `
		INSERT INTO contact_messages (name, email, company, phone, message, ip_address, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`

	err := h.db.QueryRowContext(ctx, query,
		input.ContactName,
		input.ContactEmail,
		nullableString(input.ContactCompany),
		nullableString(input.ContactPhone),
		input.ContactMessage,
		nullableString(input.IpAddress),
		time.Now().UTC(),
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("%w: saveContactMessage insert failed: %v", ErrDatabaseError, err)
	}

	return map[string]interface{}{
		"id":      id.String(),
		"success": true,
		"message": "Message received. We'll get back to you within 1-2 business days.",
	}, nil
}

// nullableString returns nil for empty strings so Postgres stores NULL
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func normalizeEntityType(et string, defaultVal string) string {
	et = strings.ToLower(strings.TrimSpace(et))
	if et == "" {
		return defaultVal
	}
	switch et {
	case "franchises":
		return "franchise"
	case "associations":
		return "association"
	case "master-franchise", "master_franchises", "master franchises", "masterfranchise":
		return "master_franchise"
	default:
		et = strings.TrimSuffix(et, "s")
		if et == "master-franchise" || et == "master franchise" || et == "masterfranchise" {
			return "master_franchise"
		}
		return et
	}
}
