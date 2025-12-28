// internal/workers/data-access/franchise-postgres/handler.go
package franchisepostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/google/uuid"

	"camunda-workers/internal/common/logger"
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
)

type Handler struct {
	db     *sql.DB
	logger logger.Logger
	config *Config
}

func NewHandler(db *sql.DB, logger logger.Logger, config *Config) *Handler {
	// Set default timeout if not provided
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 30 * time.Second
	}

	return &Handler{
		db:     db,
		logger: logger.WithFields(map[string]interface{}{"taskType": TaskType}),
		config: config,
	}
}

// Handle processes the franchise-postgres operations job
func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	h.logger.Info("Processing franchise-postgres job", map[string]interface{}{
		"jobKey":           job.GetKey(),
		"workflowInstance": job.ProcessInstanceKey,
		"workflowKey":      job.ProcessDefinitionKey,
	})

	// Parse input to determine operation type
	var baseInput BaseInput
	if err := json.Unmarshal([]byte(job.Variables), &baseInput); err != nil {
		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), h.config.RequestTimeout)
	defer cancel()

	// Route to appropriate operation handler
	var result interface{}
	var err error

	switch baseInput.OperationType {
	case "CREATE_FRANCHISE":
		result, err = h.handleCreateFranchise(ctx, job.Variables)
	case "UPDATE_FRANCHISE":
		result, err = h.handleUpdateFranchise(ctx, job.Variables, baseInput.UpdatedBy)
	case "GET_FRANCHISE":
		result, err = h.handleGetFranchise(ctx, job.Variables)
	case "DELETE_FRANCHISE":
		result, err = h.handleDeleteFranchise(ctx, job.Variables, baseInput.UpdatedBy)
	case "CREATE_BUSINESS_OVERVIEW":
		result, err = h.handleCreateBusinessOverview(ctx, job.Variables)
	case "UPDATE_BUSINESS_OVERVIEW":
		result, err = h.handleUpdateBusinessOverview(ctx, job.Variables, baseInput.UpdatedBy)
	case "CREATE_INVESTMENT":
		result, err = h.handleCreateInvestment(ctx, job.Variables)
	case "UPDATE_INVESTMENT":
		result, err = h.handleUpdateInvestment(ctx, job.Variables, baseInput.UpdatedBy)
	case "CREATE_OPERATIONS":
		result, err = h.handleCreateOperations(ctx, job.Variables)
	case "UPDATE_OPERATIONS":
		result, err = h.handleUpdateOperations(ctx, job.Variables, baseInput.UpdatedBy)
	case "GET_FULL_FRANCHISE":
		result, err = h.handleGetFullFranchise(ctx, job.Variables)
	// NEW OPERATIONS:
	case "CREATE_SOCIAL_LINKS":
		result, err = h.handleCreateSocialLinks(ctx, job.Variables)
	case "CREATE_FRANCHISE_STATS":
		result, err = h.handleCreateFranchiseStats(ctx, job.Variables)
	case "UPDATE_FRANCHISE_STATS":
		result, err = h.handleUpdateFranchiseStats(ctx, job.Variables)
	// Category Questions Operations (NEW)
	case "CREATE_CATEGORY_QUESTION":
		result, err = h.handleCreateCategoryQuestion(ctx, job.Variables)
	case "GET_CATEGORY_QUESTIONS":
		result, err = h.handleGetCategoryQuestions(ctx, job.Variables)
	case "UPDATE_CATEGORY_QUESTION":
		result, err = h.handleUpdateCategoryQuestion(ctx, job.Variables)
	case "DELETE_CATEGORY_QUESTION":
		result, err = h.handleDeleteCategoryQuestion(ctx, job.Variables)
	// Franchise Cities Operations (NEW)
	case "CREATE_FRANCHISE_CITY":
		result, err = h.handleCreateFranchiseCity(ctx, job.Variables)
	case "GET_FRANCHISE_CITIES":
		result, err = h.handleGetFranchiseCities(ctx, job.Variables)
	case "DELETE_FRANCHISE_CITY":
		result, err = h.handleDeleteFranchiseCity(ctx, job.Variables)
	// UPDATE_SOCIAL_LINKS (NEW)
	case "UPDATE_SOCIAL_LINKS":
		result, err = h.handleUpdateSocialLinks(ctx, job.Variables)
	default:
		h.failJob(client, job, "INVALID_OPERATION",
			fmt.Sprintf("Unknown operation type: %s", baseInput.OperationType), 0)
		return
	}

	if err != nil {
		h.logger.Error("Operation failed", map[string]interface{}{
			"operation": baseInput.OperationType,
			"error":     err,
			"jobKey":    job.GetKey(),
		})

		errorCode := "OPERATION_ERROR"
		retries := int32(0)

		if errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrValidationError) {
			errorCode = "VALIDATION_ERROR"
			retries = 0
		} else if errors.Is(err, ErrFranchiseNotFound) {
			errorCode = "NOT_FOUND"
			retries = 0
		} else if errors.Is(err, ErrDatabaseError) {
			errorCode = "DATABASE_ERROR"
			retries = 2
		}

		h.failJob(client, job, errorCode, err.Error(), retries)
		return
	}

	h.completeJob(client, job, result)
}

// ============================================================================
// VALIDATION FUNCTION
// ============================================================================

func (h *Handler) validateFranchiseInput(input CreateFranchiseInput) error {
    // Email format validation
    if input.ContactEmail != "" {
        if !strings.Contains(input.ContactEmail, "@") || !strings.Contains(input.ContactEmail, ".") {
            return fmt.Errorf("invalid email format")
        }
    }
    
    // URL validation
    if input.LogoURL != "" {
        if !strings.HasPrefix(input.LogoURL, "http://") && !strings.HasPrefix(input.LogoURL, "https://") {
            return fmt.Errorf("logo_url must start with http:// or https://")
        }
    }
    
    // Social URL validation
    if input.InstagramURL != "" && !isValidURL(input.InstagramURL) {
        return fmt.Errorf("invalid instagram URL format")
    }
    if input.FacebookURL != "" && !isValidURL(input.FacebookURL) {
        return fmt.Errorf("invalid facebook URL format")
    }
    if input.TwitterURL != "" && !isValidURL(input.TwitterURL) {
        return fmt.Errorf("invalid twitter URL format")
    }
    if input.LinkedinURL != "" && !isValidURL(input.LinkedinURL) {
        return fmt.Errorf("invalid linkedin URL format")
    }
    
    // Year validation - FIXED TYPE HANDLING
    currentYear := int16(time.Now().Year())
    if input.FoundedYear != nil && (*input.FoundedYear < 1800 || *input.FoundedYear > currentYear) {
        return fmt.Errorf("founded_year must be between 1800 and %d", currentYear)
    }
    
    // Established year validation - ADDED
    if input.EstablishedYear != nil && (*input.EstablishedYear < 1800 || *input.EstablishedYear > currentYear) {
        return fmt.Errorf("established_year must be between 1800 and %d", currentYear)
    }
    
    // Positive values
    if input.TotalOutlets < 0 {
        return fmt.Errorf("total_outlets must be >= 0")
    }
    
    // Units count validation - ADDED
    if input.UnitsCount < 0 {
        return fmt.Errorf("units_count must be >= 0")
    }
    
    return nil
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

	// UPDATED: Removed social URLs (moved to franchise_social_links table)
	query := `
		INSERT INTO franchises (
			name, slug, short_description, description, founded_year, 
			trusted_seller, verified, total_outlets, outlet_range,
			industry, parent_company, business_type, established_year,
			units_count, leader_name, leader_role, contact_email, 
			logo_url, created_by, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 
			$14, $15, $16, $17, $18, $19, $20, $21
		) RETURNING id, created_at, updated_at`

	var franchiseID uuid.UUID
	var createdAt, updatedAt time.Time
	now := time.Now()

	err = tx.QueryRowContext(ctx, query,
		input.Name, input.Slug, input.ShortDescription, input.Description,
		input.FoundedYear, input.TrustedSeller, input.Verified,
		input.TotalOutlets, input.OutletRange, input.Industry, input.ParentCompany,
		input.BusinessType, input.EstablishedYear, input.UnitsCount,
		input.LeaderName, input.LeaderRole, input.ContactEmail,
		input.LogoURL, createdBy, now, now,
	).Scan(&franchiseID, &createdAt, &updatedAt)

	if err != nil {
		return nil, fmt.Errorf("%w: insert franchise: %v", ErrDatabaseError, err)
	}

	// NEW: If social URLs provided, insert into franchise_social_links table
	if input.InstagramURL != "" || input.FacebookURL != "" || 
	   input.TwitterURL != "" || input.LinkedinURL != "" {
		socialQuery := `
			INSERT INTO franchise_social_links (
				franchise_id, instagram_url, facebook_url, 
				twitter_url, linkedin_url, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7)`
		
		_, err = tx.ExecContext(ctx, socialQuery,
			franchiseID, 
			nullString(input.InstagramURL),
			nullString(input.FacebookURL),
			nullString(input.TwitterURL),
			nullString(input.LinkedinURL),
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
		FranchiseID: franchiseID.String(),
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

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	// Build dynamic UPDATE query
	query, args := h.buildUpdateQuery(franchiseID, updatedBy, &input)

	var updatedAt time.Time
	err = tx.QueryRowContext(ctx, query, args...).Scan(&updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: franchise_id=%s", ErrFranchiseNotFound, input.FranchiseID)
		}
		return nil, fmt.Errorf("%w: update franchise: %v", ErrDatabaseError, err)
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
		args = append(args, *input.Name)
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
		args = append(args, *input.Industry)
		argPos++
	}
	if input.Description != nil {
		query += fmt.Sprintf(", description = $%d", argPos)
		args = append(args, *input.Description)
		argPos++
	}
	if input.ContactEmail != nil {
		query += fmt.Sprintf(", contact_email = $%d", argPos)
		args = append(args, *input.ContactEmail)
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

	// UPDATED: Removed social URLs and added new fields
	query := `
		SELECT 
			f.id, f.name, f.slug, f.short_description, f.description,
			f.founded_year, f.trusted_seller, f.verified, f.total_outlets, 
			f.outlet_range, f.industry, f.parent_company, f.business_type, 
			f.established_year, f.units_count, f.leader_name, f.leader_role, 
			f.contact_email, f.logo_url,
			f.created_by, f.updated_by, f.created_at, f.updated_at,
			fs.rating, fs.rating_count, fs.follow_count, fs.likes_count,
			fs.view_count, fs.save_count, fs.share_count, fs.enquiry_count,
			fs.news_count
		FROM franchises f
		LEFT JOIN franchise_stats fs ON f.id = fs.franchise_id
		WHERE `

	var args []interface{}
	if input.FranchiseID != "" {
		query += "f.id = $1"
		franchiseID, err := uuid.Parse(input.FranchiseID)
		if err != nil {
			return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
		}
		args = append(args, franchiseID)
	} else if input.Slug != "" {
		query += "f.slug = $1"
		args = append(args, input.Slug)
	} else {
		return nil, fmt.Errorf("%w: either franchise_id or slug is required", ErrValidationError)
	}

	var franchise Franchise
	var stats FranchiseStats
	
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
		productsJSON, _ := json.Marshal(input.Products)
		query += fmt.Sprintf(", products = $%d", argPos)
		args = append(args, productsJSON)
		argPos++
	}

	if input.Services != nil {
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

	// UPDATED: Added ROI fields and single_unit_cost fields
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
		input.InvestmentIncludes,
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
// UPDATE INVESTMENT
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
        query += fmt.Sprintf(", payback_min_months = $%d", argPos)
        args = append(args, *input.PaybackMinMonths)
        argPos++
    }
    if input.PaybackMaxMonths != nil {
        query += fmt.Sprintf(", payback_max_months = $%d", argPos)
        args = append(args, *input.PaybackMaxMonths)
        argPos++
    }
    if input.ROIMinPercentage != nil {
        query += fmt.Sprintf(", roi_min_percentage = $%d", argPos)
        args = append(args, *input.ROIMinPercentage)
        argPos++
    }
    if input.ROIMaxPercentage != nil {
        query += fmt.Sprintf(", roi_max_percentage = $%d", argPos)
        args = append(args, *input.ROIMaxPercentage)
        argPos++
    }
    if input.MonthlyTurnoverMin != nil {
        query += fmt.Sprintf(", monthly_turnover_min = $%d", argPos)
        args = append(args, *input.MonthlyTurnoverMin)
        argPos++
    }
    if input.MonthlyTurnoverMax != nil {
        query += fmt.Sprintf(", monthly_turnover_max = $%d", argPos)
        args = append(args, *input.MonthlyTurnoverMax)
        argPos++
    }
    if input.SingleUnitCostMin != nil {
        query += fmt.Sprintf(", single_unit_cost_min = $%d", argPos)
        args = append(args, *input.SingleUnitCostMin)
        argPos++
    }
    if input.SingleUnitCostMax != nil {
        query += fmt.Sprintf(", single_unit_cost_max = $%d", argPos)
        args = append(args, *input.SingleUnitCostMax)
        argPos++
    }

	if input.InitialInvestmentMin != nil {
		query += fmt.Sprintf(", initial_investment_min = $%d", argPos)
		args = append(args, *input.InitialInvestmentMin)
		argPos++
	}
	if input.InitialInvestmentMax != nil {
		query += fmt.Sprintf(", initial_investment_max = $%d", argPos)
		args = append(args, *input.InitialInvestmentMax)
		argPos++
	}
	if input.FranchiseFee != nil {
		query += fmt.Sprintf(", franchise_fee = $%d", argPos)
		args = append(args, *input.FranchiseFee)
		argPos++
	}
	if input.RoyaltyPercentage != nil {
		query += fmt.Sprintf(", royalty_percentage = $%d", argPos)
		args = append(args, *input.RoyaltyPercentage)
		argPos++
	}
	if input.MarketingFeePercentage != nil {
		query += fmt.Sprintf(", marketing_fee_percentage = $%d", argPos)
		args = append(args, *input.MarketingFeePercentage)
		argPos++
	}

	if input.InvestmentIncludes != nil {
		query += fmt.Sprintf(", investment_includes = $%d", argPos)
		args = append(args, *input.InvestmentIncludes)
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
// CREATE OPERATIONS
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

	// Convert staff breakdown to JSONB
	staffBreakdownJSON, _ := json.Marshal(input.StaffBreakdown)

	// UPDATED: Added all missing fields from new schema
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
		input.RequiredPropertyType, input.StaffRequiredMin, input.StaffRequiredMax,
		staffBreakdownJSON, input.OperatingHours, input.TrainingProvided,
		input.TrainingDetails, input.ComputerRequirements, input.MarketingSupport,
		input.PreferredLocations, input.QualificationRequired,
		input.SupplyChainSupport, input.QualityControl,
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
// UPDATE OPERATIONS
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
		query += fmt.Sprintf(", space_min_sqft = $%d", argPos)
		args = append(args, *input.SpaceMinSqft)
		argPos++
	}
	if input.SpaceMaxSqft != nil {
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
		query += fmt.Sprintf(", required_property_type = $%d", argPos)
		args = append(args, *input.RequiredPropertyType)
		argPos++
	}
	if input.StaffRequiredMin != nil {
		query += fmt.Sprintf(", staff_required_min = $%d", argPos)
		args = append(args, *input.StaffRequiredMin)
		argPos++
	}
	if input.StaffRequiredMax != nil {
		query += fmt.Sprintf(", staff_required_max = $%d", argPos)
		args = append(args, *input.StaffRequiredMax)
		argPos++
	}
	if input.StaffBreakdown != nil {
		staffJSON, _ := json.Marshal(input.StaffBreakdown)
		query += fmt.Sprintf(", staff_breakdown = $%d", argPos)
		args = append(args, staffJSON)
		argPos++
	}
	if input.OperatingHours != nil {
		query += fmt.Sprintf(", operating_hours = $%d", argPos)
		args = append(args, *input.OperatingHours)
		argPos++
	}
	if input.TrainingDetails != nil {
		query += fmt.Sprintf(", training_details = $%d", argPos)
		args = append(args, *input.TrainingDetails)
		argPos++
	}
	if input.ComputerRequirements != nil {
		query += fmt.Sprintf(", computer_requirements = $%d", argPos)
		args = append(args, *input.ComputerRequirements)
		argPos++
	}
	if input.MarketingSupport != nil {
		query += fmt.Sprintf(", marketing_support = $%d", argPos)
		args = append(args, *input.MarketingSupport)
		argPos++
	}
	if input.PreferredLocations != nil {
		query += fmt.Sprintf(", preferred_locations = $%d", argPos)
		args = append(args, *input.PreferredLocations)
		argPos++
	}
	if input.QualificationRequired != nil {
		query += fmt.Sprintf(", qualification_required = $%d", argPos)
		args = append(args, *input.QualificationRequired)
		argPos++
	}
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

	query := `
		INSERT INTO franchise_social_links (
			franchise_id, instagram_url, facebook_url, 
			twitter_url, linkedin_url, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`

	var id uuid.UUID
	now := time.Now()

	err = h.db.QueryRowContext(ctx, query,
		franchiseID, 
		nullString(input.InstagramURL),
		nullString(input.FacebookURL),
		nullString(input.TwitterURL),
		nullString(input.LinkedinURL),
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

	query := "UPDATE franchise_social_links SET updated_at = $1"
	args := []interface{}{time.Now()}
	argPos := 2

	if input.InstagramURL != nil {
		query += fmt.Sprintf(", instagram_url = $%d", argPos)
		args = append(args, nullString(*input.InstagramURL))
		argPos++
	}
	if input.FacebookURL != nil {
		query += fmt.Sprintf(", facebook_url = $%d", argPos)
		args = append(args, nullString(*input.FacebookURL))
		argPos++
	}
	if input.TwitterURL != nil {
		query += fmt.Sprintf(", twitter_url = $%d", argPos)
		args = append(args, nullString(*input.TwitterURL))
		argPos++
	}
	if input.LinkedinURL != nil {
		query += fmt.Sprintf(", linkedin_url = $%d", argPos)
		args = append(args, nullString(*input.LinkedinURL))
		argPos++
	}

	query += fmt.Sprintf(" WHERE franchise_id = $%d", argPos)
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
// CREATE FRANCHISE STATS
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

	query := `
		INSERT INTO franchise_stats (
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
// UPDATE FRANCHISE STATS
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

	query := "UPDATE franchise_stats SET updated_at = $1"
	args := []interface{}{time.Now()}
	argPos := 2

	if input.ViewCount != nil {
		query += fmt.Sprintf(", view_count = view_count + $%d", argPos)
		args = append(args, *input.ViewCount)
		argPos++
	}
	if input.LikesCount != nil {
		query += fmt.Sprintf(", likes_count = likes_count + $%d", argPos)
		args = append(args, *input.LikesCount)
		argPos++
	}
	if input.SaveCount != nil {
		query += fmt.Sprintf(", save_count = save_count + $%d", argPos)
		args = append(args, *input.SaveCount)
		argPos++
	}
	if input.ShareCount != nil {
		query += fmt.Sprintf(", share_count = share_count + $%d", argPos)
		args = append(args, *input.ShareCount)
		argPos++
	}
	if input.FollowCount != nil {
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

	query := `
		INSERT INTO category_questions (
			category_name, question, answer, display_order, created_at
		) VALUES ($1, $2, $3, $4, $5)
		RETURNING id`

	var id uuid.UUID
	now := time.Now()

	err := h.db.QueryRowContext(ctx, query,
		input.CategoryName, input.Question, input.Answer, 
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

	query := `
		SELECT id, category_name, question, answer, display_order, created_at
		FROM category_questions 
		WHERE category_name = $1
		ORDER BY display_order ASC, created_at ASC`

	rows, err := h.db.QueryContext(ctx, query, input.CategoryName)
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
		updates = append(updates, fmt.Sprintf("question = $%d", argPos))
		args = append(args, *input.Question)
		argPos++
	}
	if input.Answer != nil {
		updates = append(updates, fmt.Sprintf("answer = $%d", argPos))
		args = append(args, *input.Answer)
		argPos++
	}
	if input.DisplayOrder != nil {
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

	query := `
		INSERT INTO franchise_cities (
			franchise_id, city, state, country, created_at
		) VALUES ($1, $2, $3, $4, $5)
		RETURNING id`

	var id uuid.UUID
	now := time.Now()

	err = h.db.QueryRowContext(ctx, query,
		franchiseID, input.City, input.State, input.Country, now,
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
		FROM franchise_cities 
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

	query := `DELETE FROM franchise_cities WHERE id = $1`
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
		Stats:     franchise.Stats, // Already fetched in handleGetFranchise
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
	query = `
        SELECT 
            id, initial_investment_min, initial_investment_max, 
            franchise_fee, royalty_percentage, marketing_fee_percentage,
            payback_min_months, payback_max_months,
            roi_min_percentage, roi_max_percentage,
            monthly_turnover_min, monthly_turnover_max,
            single_unit_cost_min, single_unit_cost_max,
            investment_includes
        FROM franchise_investment_requirement 
        WHERE franchise_id = $1`
	
	err = h.db.QueryRowContext(ctx, query, franchiseUUID).Scan(
		&investment.ID, &investment.InitialInvestmentMin, &investment.InitialInvestmentMax,
		&investment.FranchiseFee, &investment.RoyaltyPercentage, &investment.MarketingFeePercentage,
		&investment.PaybackMinMonths, &investment.PaybackMaxMonths,
		&investment.ROIMinPercentage, &investment.ROIMaxPercentage,
		&investment.MonthlyTurnoverMin, &investment.MonthlyTurnoverMax,
		&investment.SingleUnitCostMin, &investment.SingleUnitCostMax,
		&investment.InvestmentIncludes,
	)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("%w: query investment: %v", ErrDatabaseError, err)
	}
	if err == nil {
		fullFranchise.Investment = &investment
	}

	// Get operations
	var operations Operations
	var staffBreakdownStr string
	query = `
        SELECT 
            id, space_min_sqft, space_max_sqft, required_property_type,
            staff_required_min, staff_required_max, staff_breakdown,
            operating_hours, training_provided, training_details,
            computer_requirements, marketing_support, preferred_locations,
            qualification_required, supply_chain_support, quality_control
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
	)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("%w: query operations: %v", ErrDatabaseError, err)
	}
	if err == nil {
		operations.StaffBreakdown = json.RawMessage(staffBreakdownStr)
		fullFranchise.Operations = &operations
	}

	// Get social links
	var socialLinks SocialLinks
	query = `
        SELECT id, instagram_url, facebook_url, twitter_url, linkedin_url
        FROM franchise_social_links 
        WHERE franchise_id = $1`
	
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
        FROM franchise_cities 
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

// // ============================================================================
// // DELETE FRANCHISE (SOFT DELETE) ----- ERROR
// // ============================================================================
// func (h *Handler) handleDeleteFranchise(ctx context.Context, variables string, updatedByStr string) (*BaseOutput, error) {
// 	var input DeleteFranchiseInput
// 	if err := json.Unmarshal([]byte(variables), &input); err != nil {
// 		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
// 	}

// 	franchiseID, err := uuid.Parse(input.FranchiseID)
// 	if err != nil {
// 		return nil, fmt.Errorf("%w: franchise_id: %v", ErrInvalidUUID, err)
// 	}

// 	updatedBy, err := uuid.Parse(updatedByStr)
// 	if err != nil {
// 		return nil, fmt.Errorf("%w: updated_by: %v", ErrInvalidUUID, err)
// 	}

// 	query := `DELETE FROM franchises WHERE id = $1`
// 	result, err := h.db.ExecContext(ctx, query, franchiseID)
// 	if err != nil {
// 		return nil, fmt.Errorf("%w: delete franchise: %v", ErrDatabaseError, err)
// 	}
	
// 	rowsAffected, _ := result.RowsAffected()
// 	if rowsAffected == 0 {
// 		return nil, fmt.Errorf("%w: franchise_id=%s", ErrFranchiseNotFound, input.FranchiseID)
// 	}

// 	return &BaseOutput{
// 		Success: true,
// 		Message: "Franchise deleted successfully",
// 	}, nil
// }

// ============================================================================
// DELETE FRANCHISE (HARD DELETE)
// ============================================================================
func (h *Handler) handleDeleteFranchise(ctx context.Context, variables string, updatedByStr string) (*BaseOutput, error) {
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

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================
func (h *Handler) completeJob(client worker.JobClient, job entities.Job, result interface{}) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.GetKey()).
		VariablesFromObject(result)
	if err != nil {
		h.logger.Error("Failed to create complete job command", map[string]interface{}{
			"error":  err,
			"jobKey": job.GetKey(),
		})
		return
	}

	_, err = cmd.Send(context.Background())
	if err != nil {
		h.logger.Error("Failed to send complete job command", map[string]interface{}{
			"error":  err,
			"jobKey": job.GetKey(),
		})
	}
}

func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, retries int32) {
	h.logger.Error("Job failed", map[string]interface{}{
		"jobKey":       job.GetKey(),
		"errorCode":    errorCode,
		"errorMessage": errorMessage,
		"retries":      retries,
	})

	_, err := client.NewThrowErrorCommand().
		JobKey(job.GetKey()).
		ErrorCode(errorCode).
		ErrorMessage(errorMessage).
		Send(context.Background())
	if err != nil {
		h.logger.Error("Failed to throw error", map[string]interface{}{
			"error":  err,
			"jobKey": job.GetKey(),
		})
	}
}

// ============================================================================
// HELPER: NULL STRING FUNCTION
// ============================================================================
func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}


