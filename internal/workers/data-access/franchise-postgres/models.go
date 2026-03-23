// internal/workers/data-access/franchise-postgres/models.go
package franchisepostgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ============================================================================
// BASE MODELS
// ============================================================================

type BaseInput struct {
	OperationType string `json:"operation_type"`       // CREATE_FRANCHISE, UPDATE_FRANCHISE, etc.
	UpdatedBy     string `json:"updated_by,omitempty"` // System Admin UUID (for updates)
}

type BaseOutput struct {
	ID      string `json:"id,omitempty"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ============================================================================
// FRANCHISE MODELS (UPDATED)
// ============================================================================

type Franchise struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Slug             string          `json:"slug"`
	ShortDescription *string         `json:"short_description,omitempty"` // NEW
	Description      *string         `json:"description,omitempty"`
	FoundedYear      *int16          `json:"founded_year,omitempty"`
	TrustedSeller    bool            `json:"trusted_seller"`
	Verified         bool            `json:"verified"` // NEW
	TotalOutlets     int             `json:"total_outlets"`
	OutletRange      *string         `json:"outlet_range,omitempty"`
	Industry         *string         `json:"industry,omitempty"`
	ParentCompany    *string         `json:"parent_company,omitempty"`
	BusinessType     *string         `json:"business_type,omitempty"`
	EstablishedYear  *int16          `json:"established_year,omitempty"` // NEW
	UnitsCount       int             `json:"units_count"`                // NEW
	LeaderName       *string         `json:"leader_name,omitempty"`
	LeaderRole       *string         `json:"leader_role,omitempty"`
	ContactEmail     *string         `json:"contact_email,omitempty"`
	LogoURL          *string         `json:"logo_url,omitempty"` // NEW
	CreatedBy        string          `json:"created_by"`
	UpdatedBy        *string         `json:"updated_by,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	Stats            *FranchiseStats `json:"stats,omitempty"` // NEW: Stats joined
}

type CreateFranchiseInput struct {
	OperationType string `json:"operation_type"` // Must be "CREATE_FRANCHISE"

	// Required fields
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	CreatedBy string `json:"created_by"` // User UUID

	// Optional fields
	ShortDescription string `json:"short_description,omitempty"` // NEW
	Description      string `json:"description,omitempty"`
	FoundedYear      *int16 `json:"founded_year,omitempty"`
	TrustedSeller    bool   `json:"trusted_seller"`
	Verified         bool   `json:"verified"` // NEW
	TotalOutlets     int    `json:"total_outlets"`
	OutletRange      string `json:"outlet_range,omitempty"`
	Industry         string `json:"industry,omitempty"`
	ParentCompany    string `json:"parent_company,omitempty"`
	BusinessType     string `json:"business_type,omitempty"`
	EstablishedYear  *int16 `json:"established_year,omitempty"` // NEW
	UnitsCount       int    `json:"units_count"`                // NEW
	LeaderName       string `json:"leader_name,omitempty"`
	LeaderRole       string `json:"leader_role,omitempty"`
	ContactEmail     string `json:"contact_email,omitempty"`
	LogoURL          string `json:"logo_url,omitempty"` // NEW

	// Social URLs (will be inserted into franchise_social_links table)
	InstagramURL string `json:"instagram_url,omitempty"`
	FacebookURL  string `json:"facebook_url,omitempty"`
	TwitterURL   string `json:"twitter_url,omitempty"`
	LinkedinURL  string `json:"linkedin_url,omitempty"`
}

func (i *CreateFranchiseInput) Validate() error {
	if i.Name == "" {
		return errors.New("name is required")
	}
	if len(i.Name) > 150 {
		return errors.New("name must be max 150 characters")
	}
	if i.Slug == "" {
		return errors.New("slug is required")
	}
	if !isValidSlug(i.Slug) {
		return errors.New("slug must contain only lowercase letters, numbers, and hyphens")
	}
	if i.CreatedBy == "" {
		return errors.New("created_by (user UUID) is required")
	}
	if i.ContactEmail != "" && !isValidEmail(i.ContactEmail) {
		return errors.New("invalid email format")
	}
	if i.LogoURL != "" && !isValidURL(i.LogoURL) {
		return errors.New("invalid logo URL format")
	}
	currentYear := time.Now().Year()
	if i.FoundedYear != nil && (*i.FoundedYear < 1800 || *i.FoundedYear > int16(currentYear)) {
		return fmt.Errorf("founded_year must be between 1800 and %d", currentYear)
	}
	if i.TotalOutlets < 0 {
		return errors.New("total_outlets must be >= 0")
	}
	return nil
}

type UpdateFranchiseInput struct {
	OperationType string `json:"operation_type"` // Must be "UPDATE_FRANCHISE"
	FranchiseID   string `json:"franchise_id"`
	UpdatedBy     string `json:"updated_by"` // System Admin UUID

	// All fields optional for updates
	Name             *string `json:"name,omitempty"`
	ShortDescription *string `json:"short_description,omitempty"` // NEW
	Description      *string `json:"description,omitempty"`
	FoundedYear      *int16  `json:"founded_year,omitempty"`
	TrustedSeller    *bool   `json:"trusted_seller,omitempty"`
	Verified         *bool   `json:"verified,omitempty"` // NEW
	TotalOutlets     *int    `json:"total_outlets,omitempty"`
	OutletRange      *string `json:"outlet_range,omitempty"`
	Industry         *string `json:"industry,omitempty"`
	ParentCompany    *string `json:"parent_company,omitempty"`
	BusinessType     *string `json:"business_type,omitempty"`
	EstablishedYear  *int16  `json:"established_year,omitempty"` // NEW
	UnitsCount       *int    `json:"units_count,omitempty"`      // NEW
	LeaderName       *string `json:"leader_name,omitempty"`
	LeaderRole       *string `json:"leader_role,omitempty"`
	ContactEmail     *string `json:"contact_email,omitempty"`
	LogoURL          *string `json:"logo_url,omitempty"` // NEW
}

type GetFranchiseInput struct {
	OperationType string `json:"operation_type"` // GET_FRANCHISE or GET_FULL_FRANCHISE
	FranchiseID   string `json:"franchise_id,omitempty"`
	Slug          string `json:"slug,omitempty"`
}

type FranchiseOutput struct {
	FranchiseID string    `json:"franchise_id"`
	Slug        string    `json:"slug,omitempty"`
	Success     bool      `json:"success"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type DeleteFranchiseInput struct {
	OperationType string `json:"operation_type"` // DELETE_FRANCHISE
	FranchiseID   string `json:"franchise_id"`
	UpdatedBy     string `json:"updated_by"` // System Admin UUID
}

// ============================================================================
// FRANCHISE STATS MODELS (NEW)
// ============================================================================

type FranchiseStats struct {
	ID           string   `json:"id,omitempty"`
	FranchiseID  string   `json:"franchise_id"`
	Rating       *float64 `json:"rating,omitempty"`
	RatingCount  int      `json:"rating_count"`
	FollowCount  int      `json:"follow_count"`
	LikesCount   int      `json:"likes_count"`
	ViewCount    int      `json:"view_count"`
	SaveCount    int      `json:"save_count"`
	ShareCount   int      `json:"share_count"`
	EnquiryCount int      `json:"enquiry_count"`
	NewsCount    int      `json:"news_count"`
}

type CreateFranchiseStatsInput struct {
	OperationType string   `json:"operation_type"` // CREATE_FRANCHISE_STATS
	FranchiseID   string   `json:"franchise_id"`
	Rating        *float64 `json:"rating,omitempty"`
	RatingCount   int      `json:"rating_count"`
	FollowCount   int      `json:"follow_count"`
	LikesCount    int      `json:"likes_count"`
	ViewCount     int      `json:"view_count"`
	SaveCount     int      `json:"save_count"`
	ShareCount    int      `json:"share_count"`
	EnquiryCount  int      `json:"enquiry_count"`
	NewsCount     int      `json:"news_count"`
}

type UpdateFranchiseStatsInput struct {
	OperationType string `json:"operation_type"` // UPDATE_FRANCHISE_STATS
	FranchiseID   string `json:"franchise_id"`
	ViewCount     *int   `json:"view_count,omitempty"`    // Increment by this value
	LikesCount    *int   `json:"likes_count,omitempty"`   // Increment by this value
	SaveCount     *int   `json:"save_count,omitempty"`    // Increment by this value
	ShareCount    *int   `json:"share_count,omitempty"`   // Increment by this value
	FollowCount   *int   `json:"follow_count,omitempty"`  // Increment by this value
	EnquiryCount  *int   `json:"enquiry_count,omitempty"` // Increment by this value
}

// ============================================================================
// SOCIAL LINKS MODELS (NEW)
// ============================================================================

type SocialLinks struct {
	ID           string  `json:"id"`
	FranchiseID  string  `json:"franchise_id"`
	InstagramURL *string `json:"instagram_url,omitempty"`
	FacebookURL  *string `json:"facebook_url,omitempty"`
	TwitterURL   *string `json:"twitter_url,omitempty"`
	LinkedinURL  *string `json:"linkedin_url,omitempty"`
}

type CreateSocialLinksInput struct {
	OperationType string `json:"operation_type"` // CREATE_SOCIAL_LINKS
	FranchiseID   string `json:"franchise_id"`
	InstagramURL  string `json:"instagram_url,omitempty"`
	FacebookURL   string `json:"facebook_url,omitempty"`
	TwitterURL    string `json:"twitter_url,omitempty"`
	LinkedinURL   string `json:"linkedin_url,omitempty"`
}

type UpdateSocialLinksInput struct {
	OperationType string  `json:"operation_type"` // UPDATE_SOCIAL_LINKS
	FranchiseID   string  `json:"franchise_id"`
	InstagramURL  *string `json:"instagram_url,omitempty"`
	FacebookURL   *string `json:"facebook_url,omitempty"`
	TwitterURL    *string `json:"twitter_url,omitempty"`
	LinkedinURL   *string `json:"linkedin_url,omitempty"`
}

// ============================================================================
// BUSINESS OVERVIEW MODELS (UNCHANGED)
// ============================================================================

type BusinessOverview struct {
	ID       string          `json:"id"`
	Products json.RawMessage `json:"products"`
	Services json.RawMessage `json:"services"`
}

type CreateBusinessOverviewInput struct {
	OperationType string        `json:"operation_type"` // CREATE_BUSINESS_OVERVIEW
	FranchiseID   string        `json:"franchise_id"`
	CreatedBy     string        `json:"created_by"`
	Products      []ProductItem `json:"products"`
	Services      []ServiceItem `json:"services"`
}

type UpdateBusinessOverviewInput struct {
	OperationType string         `json:"operation_type"` // UPDATE_BUSINESS_OVERVIEW
	FranchiseID   string         `json:"franchise_id"`
	UpdatedBy     string         `json:"updated_by"` // System Admin UUID
	Products      *[]ProductItem `json:"products,omitempty"`
	Services      *[]ServiceItem `json:"services,omitempty"`
}

type ProductItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
}

type ServiceItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
}

// ============================================================================
// INVESTMENT REQUIREMENT MODELS (UPDATED)
// ============================================================================

type Investment struct {
	ID                     string   `json:"id"`
	InitialInvestmentMin   *float64 `json:"initial_investment_min,omitempty"`
	InitialInvestmentMax   *float64 `json:"initial_investment_max,omitempty"`
	FranchiseFee           *float64 `json:"franchise_fee,omitempty"`
	RoyaltyPercentage      *float64 `json:"royalty_percentage,omitempty"`
	MarketingFeePercentage *float64 `json:"marketing_fee_percentage,omitempty"`
	PaybackMinMonths       *int     `json:"payback_min_months,omitempty"`
	PaybackMaxMonths       *int     `json:"payback_max_months,omitempty"`
	ROIMinPercentage       *float64 `json:"roi_min_percentage,omitempty"` // NEW
	ROIMaxPercentage       *float64 `json:"roi_max_percentage,omitempty"` // NEW
	MonthlyTurnoverMin     *float64 `json:"monthly_turnover_min,omitempty"`
	MonthlyTurnoverMax     *float64 `json:"monthly_turnover_max,omitempty"`
	SingleUnitCostMin      *float64 `json:"single_unit_cost_min,omitempty"` // NEW
	SingleUnitCostMax      *float64 `json:"single_unit_cost_max,omitempty"` // NEW
	InvestmentIncludes     *string  `json:"investment_includes,omitempty"`  // NEW
}

type CreateInvestmentInput struct {
	OperationType          string   `json:"operation_type"` // CREATE_INVESTMENT
	FranchiseID            string   `json:"franchise_id"`
	CreatedBy              string   `json:"created_by"`
	InitialInvestmentMin   *float64 `json:"initial_investment_min,omitempty"`
	InitialInvestmentMax   *float64 `json:"initial_investment_max,omitempty"`
	FranchiseFee           *float64 `json:"franchise_fee,omitempty"`
	RoyaltyPercentage      *float64 `json:"royalty_percentage,omitempty"`
	MarketingFeePercentage *float64 `json:"marketing_fee_percentage,omitempty"`
	PaybackMinMonths       *int     `json:"payback_min_months,omitempty"`
	PaybackMaxMonths       *int     `json:"payback_max_months,omitempty"`
	ROIMinPercentage       *float64 `json:"roi_min_percentage,omitempty"` // NEW
	ROIMaxPercentage       *float64 `json:"roi_max_percentage,omitempty"` // NEW
	MonthlyTurnoverMin     *float64 `json:"monthly_turnover_min,omitempty"`
	MonthlyTurnoverMax     *float64 `json:"monthly_turnover_max,omitempty"`
	SingleUnitCostMin      *float64 `json:"single_unit_cost_min,omitempty"` // NEW
	SingleUnitCostMax      *float64 `json:"single_unit_cost_max,omitempty"` // NEW
	InvestmentIncludes     *string  `json:"investment_includes,omitempty"`  // NEW
}

type UpdateInvestmentInput struct {
	OperationType          string   `json:"operation_type"` // UPDATE_INVESTMENT
	FranchiseID            string   `json:"franchise_id"`
	UpdatedBy              string   `json:"updated_by"` // System Admin UUID
	InitialInvestmentMin   *float64 `json:"initial_investment_min,omitempty"`
	InitialInvestmentMax   *float64 `json:"initial_investment_max,omitempty"`
	FranchiseFee           *float64 `json:"franchise_fee,omitempty"`
	RoyaltyPercentage      *float64 `json:"royalty_percentage,omitempty"`
	MarketingFeePercentage *float64 `json:"marketing_fee_percentage,omitempty"`
	PaybackMinMonths       *int     `json:"payback_min_months,omitempty"` // ✅ ADD
	PaybackMaxMonths       *int     `json:"payback_max_months,omitempty"` // ✅ ADD
	ROIMinPercentage       *float64 `json:"roi_min_percentage,omitempty"`
	ROIMaxPercentage       *float64 `json:"roi_max_percentage,omitempty"`
	MonthlyTurnoverMin     *float64 `json:"monthly_turnover_min,omitempty"` // ✅ ADD
	MonthlyTurnoverMax     *float64 `json:"monthly_turnover_max,omitempty"` // ✅ ADD
	SingleUnitCostMin      *float64 `json:"single_unit_cost_min,omitempty"`
	SingleUnitCostMax      *float64 `json:"single_unit_cost_max,omitempty"`
	InvestmentIncludes     *string  `json:"investment_includes,omitempty"` // ✅ ADD
}

// ============================================================================
// OPERATIONS MODELS (UPDATED)
// ============================================================================

type Operations struct {
	ID                    string          `json:"id"`
	SpaceMinSqft          *int            `json:"space_min_sqft,omitempty"`
	SpaceMaxSqft          *int            `json:"space_max_sqft,omitempty"`
	RequiredPropertyType  *string         `json:"required_property_type,omitempty"` // NEW
	StaffRequiredMin      *int            `json:"staff_required_min,omitempty"`
	StaffRequiredMax      *int            `json:"staff_required_max,omitempty"`
	StaffBreakdown        json.RawMessage `json:"staff_breakdown,omitempty"` // NEW
	OperatingHours        *string         `json:"operating_hours,omitempty"`
	TrainingProvided      bool            `json:"training_provided"`
	TrainingDetails       *string         `json:"training_details,omitempty"`
	ComputerRequirements  *string         `json:"computer_requirements,omitempty"`  // NEW
	MarketingSupport      *string         `json:"marketing_support,omitempty"`      // NEW
	PreferredLocations    *string         `json:"preferred_locations,omitempty"`    // NEW
	QualificationRequired *string         `json:"qualification_required,omitempty"` // NEW
	SupplyChainSupport    bool            `json:"supply_chain_support"`
	QualityControl        bool            `json:"quality_control"`
}

type StaffBreakdownItem struct {
	Role  string `json:"role"`
	Count int    `json:"count"`
}

type CreateOperationsInput struct {
	OperationType         string               `json:"operation_type"` // CREATE_OPERATIONS
	FranchiseID           string               `json:"franchise_id"`
	CreatedBy             string               `json:"created_by"`
	SpaceMinSqft          *int                 `json:"space_min_sqft,omitempty"`
	SpaceMaxSqft          *int                 `json:"space_max_sqft,omitempty"`
	RequiredPropertyType  *string              `json:"required_property_type,omitempty"` // NEW
	StaffRequiredMin      *int                 `json:"staff_required_min,omitempty"`
	StaffRequiredMax      *int                 `json:"staff_required_max,omitempty"`
	StaffBreakdown        []StaffBreakdownItem `json:"staff_breakdown,omitempty"` // NEW
	OperatingHours        *string              `json:"operating_hours,omitempty"`
	TrainingProvided      bool                 `json:"training_provided"`
	TrainingDetails       *string              `json:"training_details,omitempty"`
	ComputerRequirements  *string              `json:"computer_requirements,omitempty"`  // NEW
	MarketingSupport      *string              `json:"marketing_support,omitempty"`      // NEW
	PreferredLocations    *string              `json:"preferred_locations,omitempty"`    // NEW
	QualificationRequired *string              `json:"qualification_required,omitempty"` // NEW
	SupplyChainSupport    bool                 `json:"supply_chain_support"`
	QualityControl        bool                 `json:"quality_control"`
}

type UpdateOperationsInput struct {
	OperationType         string                `json:"operation_type"` // UPDATE_OPERATIONS
	FranchiseID           string                `json:"franchise_id"`
	UpdatedBy             string                `json:"updated_by"` // System Admin UUID
	SpaceMinSqft          *int                  `json:"space_min_sqft,omitempty"`
	SpaceMaxSqft          *int                  `json:"space_max_sqft,omitempty"`
	RequiredPropertyType  *string               `json:"required_property_type,omitempty"` // NEW
	StaffRequiredMin      *int                  `json:"staff_required_min,omitempty"`     // ✅ ADD
	StaffRequiredMax      *int                  `json:"staff_required_max,omitempty"`     // ✅ ADD
	OperatingHours        *string               `json:"operating_hours,omitempty"`        // ✅ ADD
	StaffBreakdown        *[]StaffBreakdownItem `json:"staff_breakdown,omitempty"`        // NEW
	TrainingProvided      *bool                 `json:"training_provided,omitempty"`
	TrainingDetails       *string               `json:"training_details,omitempty"`
	ComputerRequirements  *string               `json:"computer_requirements,omitempty"`  // NEW
	MarketingSupport      *string               `json:"marketing_support,omitempty"`      // NEW
	PreferredLocations    *string               `json:"preferred_locations,omitempty"`    // NEW
	QualificationRequired *string               `json:"qualification_required,omitempty"` // NEW
	SupplyChainSupport    *bool                 `json:"supply_chain_support,omitempty"`
	QualityControl        *bool                 `json:"quality_control,omitempty"`
}

// ============================================================================
// CATEGORY QUESTIONS MODELS (NEW)
// ============================================================================

type CategoryQuestion struct {
	ID           string    `json:"id"`
	CategoryName string    `json:"category_name"`
	Question     string    `json:"question"`
	Answer       string    `json:"answer"`
	DisplayOrder int       `json:"display_order"`
	CreatedAt    time.Time `json:"created_at"`
}

type CreateCategoryQuestionInput struct {
	OperationType string `json:"operation_type"` // CREATE_CATEGORY_QUESTION
	CategoryName  string `json:"category_name"`
	Question      string `json:"question"`
	Answer        string `json:"answer"`
	DisplayOrder  int    `json:"display_order"`
}

func (i *CreateCategoryQuestionInput) Validate() error {
	if i.CategoryName == "" {
		return errors.New("category_name is required")
	}
	if i.Question == "" {
		return errors.New("question is required")
	}
	if i.Answer == "" {
		return errors.New("answer is required")
	}
	return nil
}

type GetCategoryQuestionsInput struct {
	OperationType string `json:"operation_type"` // GET_CATEGORY_QUESTIONS
	CategoryName  string `json:"category_name"`
}

type UpdateCategoryQuestionInput struct {
	OperationType string  `json:"operation_type"` // UPDATE_CATEGORY_QUESTION
	QuestionID    string  `json:"question_id"`
	Question      *string `json:"question,omitempty"`
	Answer        *string `json:"answer,omitempty"`
	DisplayOrder  *int    `json:"display_order,omitempty"`
}

type DeleteCategoryQuestionInput struct {
	OperationType string `json:"operation_type"` // DELETE_CATEGORY_QUESTION
	QuestionID    string `json:"question_id"`
}

type CategoryQuestionsOutput struct {
	Questions []CategoryQuestion `json:"questions"`
	Success   bool               `json:"success"`
	Message   string             `json:"message"`
}

// ============================================================================
// FRANCHISE CITIES MODELS (NEW)
// ============================================================================

type FranchiseCity struct {
	ID          string    `json:"id"`
	FranchiseID string    `json:"franchise_id"`
	City        string    `json:"city"`
	State       string    `json:"state"`
	Country     string    `json:"country"`
	CreatedAt   time.Time `json:"created_at"`
}

type CreateFranchiseCityInput struct {
	OperationType string `json:"operation_type"` // CREATE_FRANCHISE_CITY
	FranchiseID   string `json:"franchise_id"`
	City          string `json:"city"`
	State         string `json:"state"`
	Country       string `json:"country"`
}

type GetFranchiseCitiesInput struct {
	OperationType string `json:"operation_type"` // GET_FRANCHISE_CITIES
	FranchiseID   string `json:"franchise_id"`
}

type DeleteFranchiseCityInput struct {
	OperationType string `json:"operation_type"` // DELETE_FRANCHISE_CITY
	CityID        string `json:"city_id"`
}

type FranchiseCitiesOutput struct {
	Cities  []FranchiseCity `json:"cities"`
	Success bool            `json:"success"`
	Message string          `json:"message"`
}

// ============================================================================
// FULL FRANCHISE MODEL (UPDATED)
// ============================================================================

type FullFranchise struct {
	Franchise        Franchise         `json:"franchise"`
	BusinessOverview *BusinessOverview `json:"business_overview,omitempty"`
	Investment       *Investment       `json:"investment,omitempty"`
	Operations       *Operations       `json:"operations,omitempty"`
	SocialLinks      *SocialLinks      `json:"social_links,omitempty"` // NEW
	Stats            *FranchiseStats   `json:"stats,omitempty"`        // Already in Franchise struct but can be duplicated here
	Cities           []FranchiseCity   `json:"cities,omitempty"`       // NEW: Add cities array
}

// ============================================================================
// VALIDATION HELPERS
// ============================================================================

func isValidSlug(slug string) bool {
	// Only lowercase letters, numbers, and hyphens
	matched, _ := regexp.MatchString(`^[a-z0-9-]+$`, slug)
	return matched
}

func isValidEmail(email string) bool {
	// Basic email validation
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`, email)
	return matched
}

func isValidURL(url string) bool {
	// URL must start with http:// or https://
	matched, _ := regexp.MatchString(`^https?://`, url)
	return matched
}

// ============================================================
// BOOKMARK INPUT/OUTPUT
// ============================================================

type BookmarkInput struct {
	OperationType string `json:"operationType"`
	UserID        string `json:"userId"`
	FranchiseID   string `json:"franchiseId"`
}

type BookmarkOutput struct {
	ID           string `json:"id,omitempty"`
	UserID       string `json:"userId"`
	FranchiseID  string `json:"franchiseId"`
	IsBookmarked bool   `json:"isBookmarked"`
	Success      bool   `json:"success"`
	Message      string `json:"message"`
}

type GetBookmarksInput struct {
	OperationType string `json:"operationType"`
	UserID        string `json:"userId"`
	Page          int    `json:"page"`
	Limit         int    `json:"limit"`
}

type BookmarkedFranchise struct {
	BookmarkID   string    `json:"bookmarkId"`
	FranchiseID  string    `json:"franchiseId"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	LogoURL      string    `json:"logoUrl,omitempty"`
	Industry     string    `json:"industry,omitempty"`
	BookmarkedAt time.Time `json:"bookmarkedAt"`
}

type GetBookmarksOutput struct {
	Bookmarks  []BookmarkedFranchise `json:"bookmarks"`
	TotalCount int                   `json:"totalCount"`
	Page       int                   `json:"page"`
	Limit      int                   `json:"limit"`
	Success    bool                  `json:"success"`
	Message    string                `json:"message"`
}

type CheckBookmarkInput struct {
	OperationType string `json:"operationType"`
	UserID        string `json:"userId"`
	FranchiseID   string `json:"franchiseId"`
}

type CheckBookmarkOutput struct {
	IsBookmarked bool   `json:"isBookmarked"`
	BookmarkID   string `json:"bookmarkId,omitempty"`
	Success      bool   `json:"success"`
}

// ============================================================
// RATING INPUT/OUTPUT
// ============================================================

type SubmitRatingInput struct {
	OperationType string  `json:"operationType"`
	UserID        string  `json:"userId"`
	FranchiseID   string  `json:"franchiseId"`
	Rating        float64 `json:"rating"` // 1.0 to 5.0
	Review        string  `json:"review,omitempty"`
}

func (s SubmitRatingInput) Validate() error {
	if s.UserID == "" {
		return fmt.Errorf("userId is required")
	}
	if s.FranchiseID == "" {
		return fmt.Errorf("franchiseId is required")
	}
	if s.Rating < 1.0 || s.Rating > 5.0 {
		return fmt.Errorf("rating must be between 1.0 and 5.0")
	}
	if len(s.Review) > 2000 {
		return fmt.Errorf("review must not exceed 2000 characters")
	}
	return nil
}

type UpdateRatingInput struct {
	OperationType string   `json:"operationType"`
	UserID        string   `json:"userId"`
	FranchiseID   string   `json:"franchiseId"`
	Rating        *float64 `json:"rating,omitempty"`
	Review        *string  `json:"review,omitempty"`
}

type RatingOutput struct {
	ID          string    `json:"id,omitempty"`
	UserID      string    `json:"userId"`
	FranchiseID string    `json:"franchiseId"`
	Rating      float64   `json:"rating"`
	Review      string    `json:"review,omitempty"`
	IsNew       bool      `json:"isNew"` // true = created, false = updated
	Success     bool      `json:"success"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"createdAt,omitempty"`
}

type GetUserRatingInput struct {
	OperationType string `json:"operationType"`
	UserID        string `json:"userId"`
	FranchiseID   string `json:"franchiseId"`
}

type GetUserRatingOutput struct {
	HasRated  bool      `json:"hasRated"`
	Rating    float64   `json:"rating,omitempty"`
	Review    string    `json:"review,omitempty"`
	RatingID  string    `json:"ratingId,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
}

type GetFranchiseRatingsInput struct {
	OperationType string `json:"operationType"`
	FranchiseID   string `json:"franchiseId"`
	Page          int    `json:"page"`
	Limit         int    `json:"limit"`
}

type RatingItem struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	Rating    float64   `json:"rating"`
	Review    string    `json:"review,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type GetFranchiseRatingsOutput struct {
	Ratings    []RatingItem `json:"ratings"`
	TotalCount int          `json:"totalCount"`
	AvgRating  float64      `json:"avgRating"`
	Page       int          `json:"page"`
	Limit      int          `json:"limit"`
	Success    bool         `json:"success"`
	Message    string       `json:"message"`
}

// ============================================================
// SHARE INPUT/OUTPUT
// ============================================================

type ShareFranchiseInput struct {
	OperationType string `json:"operationType"`
	UserID        string `json:"userId,omitempty"` // optional — anon share
	FranchiseID   string `json:"franchiseId"`
	SharePlatform string `json:"sharePlatform"` // whatsapp, twitter, linkedin, email, copy_link
	IPAddress     string `json:"ipAddress,omitempty"`
}

func (s ShareFranchiseInput) Validate() error {
	if s.FranchiseID == "" {
		return fmt.Errorf("franchiseId is required")
	}
	validPlatforms := map[string]bool{
		"whatsapp":  true,
		"twitter":   true,
		"linkedin":  true,
		"email":     true,
		"copy_link": true,
		"facebook":  true,
		"other":     true,
	}
	if s.SharePlatform != "" && !validPlatforms[s.SharePlatform] {
		return fmt.Errorf("invalid sharePlatform: must be whatsapp/twitter/linkedin/email/copy_link/facebook/other")
	}
	return nil
}

type ShareOutput struct {
	ShareID       string `json:"shareId"`
	FranchiseID   string `json:"franchiseId"`
	SharePlatform string `json:"sharePlatform"`
	ShareURL      string `json:"shareUrl,omitempty"`
	Success       bool   `json:"success"`
	Message       string `json:"message"`
}

type GetUserSharesInput struct {
	OperationType string `json:"operationType"`
	UserID        string `json:"userId"`
	Page          int    `json:"page"`
	Limit         int    `json:"limit"`
}

type ShareItem struct {
	ShareID       string    `json:"shareId"`
	FranchiseID   string    `json:"franchiseId"`
	FranchiseName string    `json:"franchiseName,omitempty"`
	SharePlatform string    `json:"sharePlatform"`
	SharedAt      time.Time `json:"sharedAt"`
}

type GetUserSharesOutput struct {
	Shares     []ShareItem `json:"shares"`
	TotalCount int         `json:"totalCount"`
	Success    bool        `json:"success"`
	Message    string      `json:"message"`
}
