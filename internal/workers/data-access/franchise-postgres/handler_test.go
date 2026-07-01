// internal/workers/data-access/franchise-postgres/handler_test.go
package franchisepostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"camunda-workers/internal/common/logger"
	
)

func init() {
	loadEnvFile()
}

func loadEnvFile() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Println("Warning: Could not get current directory:", err)
		return
	}

	dir := cwd
	for {
		envPath := filepath.Join(dir, ".env")
		if _, err := os.Stat(envPath); err == nil {
			if err := godotenv.Load(envPath); err == nil {
				fmt.Printf("✓ Loaded .env from: %s\n", envPath)
				return
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	fmt.Println("ℹ No .env file found, using system environment variables")
}

// ============================================================================
// UNIT TESTS (with sqlmock)
// ============================================================================

type HandlerUnitTestSuite struct {
	suite.Suite
	db      *sql.DB
	mock    sqlmock.Sqlmock
	handler *Handler
	ctx     context.Context
}

func (suite *HandlerUnitTestSuite) SetupTest() {
	var err error
	suite.db, suite.mock, err = sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(suite.T(), err)

	testLogger := logger.NewNoOpLogger()
	config := &Config{
		RequestTimeout: 30 * time.Second,
		EncryptionKey:  "MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMA==",
	}
	suite.ctx = context.Background()
	suite.handler = NewHandler(suite.db, testLogger, config, "test-worker")
}

func (suite *HandlerUnitTestSuite) TearDownTest() {
	suite.db.Close()
}

func TestHandlerUnitTestSuite(t *testing.T) {
	suite.Run(t, new(HandlerUnitTestSuite))
}

// ============================================================================
// TEST ALL 22 OPERATIONS
// ============================================================================

// 1. CREATE_FRANCHISE
func (suite *HandlerUnitTestSuite) TestCreateFranchise_Success() {
	userID := uuid.New()
	franchiseID := uuid.New()
	now := time.Now()

	input := CreateFranchiseInput{
		OperationType:     "CREATE_FRANCHISE",
		Name:              "Test Franchise",
		Slug:              "test-franchise",
		ShortDescription:  "Short description",
		Description:       "Test franchise description",
		FoundedYear:       int16Ptr(2020),
		TrustedSeller:     true,
		Verified:          true,
		TotalOutlets:      100,
		OutletRange:       "51-100",
		Industry:          "Food & Beverage",
		ParentCompany:     "Test Corp",
		BusinessType:      "Quick Service Restaurant",
		EstablishedYear:   int16Ptr(2020),
		UnitsCount:        50,
		LeaderName:        "John Doe",
		LeaderRole:        "CEO",
		ContactEmail:      "john@test.com",
		LogoURL:           "https://logo.com/test",
		InstagramURL:      "https://instagram.com/test",
		FacebookURL:       "https://facebook.com/test",
		TwitterURL:        "https://twitter.com/test",
		LinkedinURL:       "https://linkedin.com/test",
		CreatedBy:         userID.String(),
	}

	inputJSON, _ := json.Marshal(input)

	expectedFranchiseQuery := `
		INSERT INTO franchises (
			name, slug, short_description, description, founded_year, 
			trusted_seller, verified, total_outlets, outlet_range,
			industry, parent_company, business_type, established_year,
			units_count, leader_name, leader_role, contact_email, 
			logo_url, website_url, is_sponsored, created_by, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 
			$14, $15, $16, $17, $18, $19, $20, $21, $22, $23
		) RETURNING id, created_at, updated_at`

	suite.mock.ExpectBegin()
	suite.mock.ExpectQuery(expectedFranchiseQuery).
		WithArgs(
			"Test Franchise", "test-franchise", "Short description",
			"Test franchise description", int16(2020), true, true, 100,
			"51-100", "Food & Beverage", "Test Corp", "Quick Service Restaurant",
			int16(2020), 50, "John Doe", "CEO", "john@test.com",
			"https://logo.com/test", "", false, userID, sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(franchiseID, now, now))

	expectedSocialQuery := `
		INSERT INTO franchise_social_links (
			franchise_id, instagram_url, facebook_url, 
			twitter_url, linkedin_url, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	suite.mock.ExpectExec(expectedSocialQuery).
		WithArgs(
			franchiseID,
			"https://instagram.com/test",
			"https://facebook.com/test",
			"https://twitter.com/test",
			"https://linkedin.com/test",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	suite.mock.ExpectCommit()

	result, err := suite.handler.handleCreateFranchise(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)
	assert.Equal(suite.T(), franchiseID.String(), result.FranchiseID)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 2. UPDATE_FRANCHISE
func (suite *HandlerUnitTestSuite) TestUpdateFranchise_Success() {
	franchiseID := uuid.New()
	adminID := uuid.New()
	now := time.Now()

	input := UpdateFranchiseInput{
		OperationType: "UPDATE_FRANCHISE",
		FranchiseID:   franchiseID.String(),
		Name:          stringPtr("Modified Name"),
		Description:   stringPtr("Modified description"),
		ContactEmail:  stringPtr("modified@test.com"),
	}

	inputJSON, _ := json.Marshal(input)

	suite.mock.ExpectBegin()
	selectQuery := `
		SELECT is_featured, is_sponsored, featured_order, featured_start_at, featured_expires_at 
		FROM franchises WHERE id = $1 FOR UPDATE`
	suite.mock.ExpectQuery(selectQuery).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{
			"is_featured", "is_sponsored", "featured_order", "featured_start_at", "featured_expires_at",
		}).AddRow(false, false, 0, nil, nil))

	expectedQuery := "UPDATE franchises SET updated_by = $1, updated_at = $2, name = $3, description = $4, contact_email = $5 WHERE id = $6 RETURNING updated_at"
	suite.mock.ExpectQuery(expectedQuery).
		WithArgs(
			adminID, sqlmock.AnyArg(),
			"Modified Name", "Modified description", "modified@test.com",
			franchiseID,
		).
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(now))

	suite.mock.ExpectCommit()

	result, err := suite.handler.handleUpdateFranchise(suite.ctx, string(inputJSON), adminID.String())

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)
	assert.Equal(suite.T(), franchiseID.String(), result.FranchiseID)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 3. GET_FRANCHISE
func (suite *HandlerUnitTestSuite) TestGetFranchise_Success() {
	franchiseID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	input := GetFranchiseInput{
		OperationType: "GET_FRANCHISE",
		FranchiseID:   franchiseID.String(),
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `
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
		WHERE f.id = $1`

	expectedRow := sqlmock.NewRows([]string{
		"id", "name", "slug", "short_description", "description",
		"founded_year", "trusted_seller", "verified", "total_outlets",
		"outlet_range", "industry", "parent_company", "business_type",
		"established_year", "units_count", "leader_name", "leader_role",
		"contact_email", "logo_url",
		"created_by", "updated_by", "created_at", "updated_at",
		"entity_type", "association_metadata", "member_count", "membership_fee_min", "membership_fee_max", "approved_at",
		"website_url", "is_featured", "featured_start_at", "featured_expires_at", "featured_order", "is_sponsored",
		"rating", "rating_count", "follow_count", "likes_count",
		"view_count", "save_count", "share_count", "enquiry_count", "news_count",
	}).AddRow(
		franchiseID, "Test Franchise", "test-franchise",
		"Short description", "Full description",
		int16(2020), true, true, 100,
		"51-100", "Food & Beverage", "Test Corp", "Quick Service Restaurant",
		int16(2020), 50, "John Doe", "CEO",
		"john@test.com", "https://logo.com/test",
		userID, userID, now, now,
		"franchise", []byte("{}"), 0, 0.00, 0.00, nil,
		nil, false, nil, nil, 0, false,
		4.5, 100, 500, 1000, 5000, 200, 50, 25, 10,
	)

	suite.mock.ExpectQuery(expectedQuery).
		WithArgs(franchiseID).
		WillReturnRows(expectedRow)

	result, err := suite.handler.handleGetFranchise(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.Equal(suite.T(), franchiseID.String(), result.ID)
	assert.Equal(suite.T(), "Test Franchise", result.Name)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 4. DELETE_FRANCHISE
func (suite *HandlerUnitTestSuite) TestDeleteFranchise_Success() {
	franchiseID := uuid.New()
	adminID := uuid.New()

	input := DeleteFranchiseInput{
		OperationType: "DELETE_FRANCHISE",
		FranchiseID:   franchiseID.String(),
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `DELETE FROM franchises WHERE id = $1`
	suite.mock.ExpectExec(expectedQuery).
		WithArgs(franchiseID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := suite.handler.handleDeleteFranchise(suite.ctx, string(inputJSON), adminID.String())

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 5. CREATE_BUSINESS_OVERVIEW
func (suite *HandlerUnitTestSuite) TestCreateBusinessOverview_Success() {
	franchiseID := uuid.New()
	userID := uuid.New()
	overviewID := uuid.New()

	products := []ProductItem{
		{Name: "Product 1", Description: "Description 1", Category: "Category A"},
	}

	services := []ServiceItem{
		{Name: "Service 1", Description: "Service Description 1", Type: "Type A"},
	}

	input := CreateBusinessOverviewInput{
		OperationType: "CREATE_BUSINESS_OVERVIEW",
		FranchiseID:   franchiseID.String(),
		CreatedBy:     userID.String(),
		Products:      products,
		Services:      services,
	}

	inputJSON, _ := json.Marshal(input)

	productsJSON, _ := json.Marshal(products)
	servicesJSON, _ := json.Marshal(services)

	expectedQuery := `
		INSERT INTO franchise_business_overview 
			(franchise_id, products, services, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`

	suite.mock.ExpectQuery(expectedQuery).
		WithArgs(franchiseID, productsJSON, servicesJSON, userID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(overviewID))

	result, err := suite.handler.handleCreateBusinessOverview(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)
	assert.Equal(suite.T(), overviewID.String(), result.ID)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 6. UPDATE_BUSINESS_OVERVIEW
func (suite *HandlerUnitTestSuite) TestUpdateBusinessOverview_Success() {
	franchiseID := uuid.New()
	adminID := uuid.New()

	products := []ProductItem{
		{Name: "Updated Product", Category: "Updated Category"},
	}

	input := UpdateBusinessOverviewInput{
		OperationType: "UPDATE_BUSINESS_OVERVIEW",
		FranchiseID:   franchiseID.String(),
		Products:      &products,
	}

	inputJSON, _ := json.Marshal(input)

	productsJSON, _ := json.Marshal(products)

	expectedQuery := "UPDATE franchise_business_overview SET updated_by = $1, updated_at = $2, products = $3 WHERE franchise_id = $4"
	suite.mock.ExpectExec(expectedQuery).
		WithArgs(adminID, sqlmock.AnyArg(), productsJSON, franchiseID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := suite.handler.handleUpdateBusinessOverview(suite.ctx, string(inputJSON), adminID.String())

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 7. CREATE_INVESTMENT
func (suite *HandlerUnitTestSuite) TestCreateInvestment_Success() {
	franchiseID := uuid.New()
	userID := uuid.New()
	investmentID := uuid.New()

	input := CreateInvestmentInput{
		OperationType:          "CREATE_INVESTMENT",
		FranchiseID:            franchiseID.String(),
		CreatedBy:              userID.String(),
		InitialInvestmentMin:   float64Ptr(100000.0),
		InitialInvestmentMax:   float64Ptr(500000.0),
		FranchiseFee:           float64Ptr(30000.0),
		RoyaltyPercentage:      float64Ptr(5.0),
		MarketingFeePercentage: float64Ptr(2.0),
		PaybackMinMonths:       intPtr(12),
		PaybackMaxMonths:       intPtr(24),
		ROIMinPercentage:       float64Ptr(15.0),
		ROIMaxPercentage:       float64Ptr(30.0),
		MonthlyTurnoverMin:     float64Ptr(50000.0),
		MonthlyTurnoverMax:     float64Ptr(200000.0),
		SingleUnitCostMin:      float64Ptr(1000.0),
		SingleUnitCostMax:      float64Ptr(5000.0),
		InvestmentIncludes:     stringPtr("Equipment, Training, Marketing"),
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `
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

	suite.mock.ExpectQuery(expectedQuery).
		WithArgs(
			franchiseID, 100000.0, 500000.0, 30000.0, 5.0, 2.0,
			12, 24, 15.0, 30.0, 50000.0, 200000.0,
			1000.0, 5000.0, "Equipment, Training, Marketing",
			userID, sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(investmentID))

	result, err := suite.handler.handleCreateInvestment(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)
	assert.Equal(suite.T(), investmentID.String(), result.ID)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 8. UPDATE_INVESTMENT
func (suite *HandlerUnitTestSuite) TestUpdateInvestment_Success() {
	franchiseID := uuid.New()
	adminID := uuid.New()

	input := UpdateInvestmentInput{
		OperationType:          "UPDATE_INVESTMENT",
		FranchiseID:            franchiseID.String(),
		FranchiseFee:           float64Ptr(35000.0),
		RoyaltyPercentage:      float64Ptr(6.0),
		ROIMinPercentage:       float64Ptr(20.0),
		ROIMaxPercentage:       float64Ptr(35.0),
		SingleUnitCostMin:      float64Ptr(1500.0),
		SingleUnitCostMax:      float64Ptr(6000.0),
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := "UPDATE franchise_investment_requirement SET updated_by = $1, updated_at = $2, roi_min_percentage = $3, roi_max_percentage = $4, single_unit_cost_min = $5, single_unit_cost_max = $6, franchise_fee = $7, royalty_percentage = $8 WHERE franchise_id = $9"
	suite.mock.ExpectExec(expectedQuery).
		WithArgs(
			adminID, sqlmock.AnyArg(),
			20.0, 35.0, 1500.0, 6000.0, 35000.0, 6.0,
			franchiseID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := suite.handler.handleUpdateInvestment(suite.ctx, string(inputJSON), adminID.String())

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 9. CREATE_OPERATIONS
func (suite *HandlerUnitTestSuite) TestCreateOperations_Success() {
	franchiseID := uuid.New()
	userID := uuid.New()
	operationsID := uuid.New()

	staffBreakdown := []StaffBreakdownItem{
		{Role: "Manager", Count: 1},
		{Role: "Staff", Count: 5},
	}

	input := CreateOperationsInput{
		OperationType:         "CREATE_OPERATIONS",
		FranchiseID:           franchiseID.String(),
		CreatedBy:             userID.String(),
		SpaceMinSqft:          intPtr(500),
		SpaceMaxSqft:          intPtr(1000),
		RequiredPropertyType:  stringPtr("Commercial"),
		StaffRequiredMin:      intPtr(3),
		StaffRequiredMax:      intPtr(10),
		StaffBreakdown:        staffBreakdown,
		OperatingHours:        stringPtr("9 AM - 9 PM"),
		TrainingProvided:      true,
		TrainingDetails:       stringPtr("2 weeks comprehensive training"),
		ComputerRequirements:  stringPtr("POS System, Computer"),
		MarketingSupport:      stringPtr("Local Marketing Materials"),
		PreferredLocations:    stringPtr("Malls, High Streets"),
		QualificationRequired: stringPtr("High School Diploma"),
		SupplyChainSupport:    true,
		QualityControl:        true,
	}

	inputJSON, _ := json.Marshal(input)

	staffBreakdownJSON, _ := json.Marshal(staffBreakdown)

	expectedQuery := `
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

	suite.mock.ExpectQuery(expectedQuery).
		WithArgs(
			franchiseID, 500, 1000, "Commercial", 3, 10,
			staffBreakdownJSON, "9 AM - 9 PM", true,
			"2 weeks comprehensive training", "POS System, Computer",
			"Local Marketing Materials", "Malls, High Streets", "High School Diploma",
			true, true, userID, sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(operationsID))

	result, err := suite.handler.handleCreateOperations(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)
	assert.Equal(suite.T(), operationsID.String(), result.ID)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 10. UPDATE_OPERATIONS
func (suite *HandlerUnitTestSuite) TestUpdateOperations_Success() {
	franchiseID := uuid.New()
	adminID := uuid.New()

	input := UpdateOperationsInput{
		OperationType:        "UPDATE_OPERATIONS",
		FranchiseID:          franchiseID.String(),
		SpaceMinSqft:         intPtr(600),
		SpaceMaxSqft:         intPtr(1200),
		TrainingProvided:     boolPtr(true),
		TrainingDetails:      stringPtr("3 weeks modified training"),
		ComputerRequirements: stringPtr("Modified computer requirements"),
		MarketingSupport:     stringPtr("Modified marketing support"),
		SupplyChainSupport:   boolPtr(true),
		QualityControl:       boolPtr(true),
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := "UPDATE franchise_operations SET updated_by = $1, updated_at = $2, space_min_sqft = $3, space_max_sqft = $4, training_provided = $5, training_details = $6, computer_requirements = $7, marketing_support = $8, supply_chain_support = $9, quality_control = $10 WHERE franchise_id = $11"
	suite.mock.ExpectExec(expectedQuery).
		WithArgs(
			adminID, sqlmock.AnyArg(),
			600, 1200, true, "3 weeks modified training",
			"Modified computer requirements", "Modified marketing support",
			true, true, franchiseID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := suite.handler.handleUpdateOperations(suite.ctx, string(inputJSON), adminID.String())

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 11. GET_FULL_FRANCHISE
func (suite *HandlerUnitTestSuite) TestGetFullFranchise_Success() {
	franchiseID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	input := GetFranchiseInput{
		OperationType: "GET_FULL_FRANCHISE",
		FranchiseID:   franchiseID.String(),
	}

	inputJSON, _ := json.Marshal(input)

	// Get main franchise with stats
	franchiseQuery := `
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
		WHERE f.id = $1`

	franchiseRow := sqlmock.NewRows([]string{
		"id", "name", "slug", "short_description", "description",
		"founded_year", "trusted_seller", "verified", "total_outlets",
		"outlet_range", "industry", "parent_company", "business_type",
		"established_year", "units_count", "leader_name", "leader_role",
		"contact_email", "logo_url",
		"created_by", "updated_by", "created_at", "updated_at",
		"entity_type", "association_metadata", "member_count", "membership_fee_min", "membership_fee_max", "approved_at",
		"website_url", "is_featured", "featured_start_at", "featured_expires_at", "featured_order", "is_sponsored",
		"rating", "rating_count", "follow_count", "likes_count",
		"view_count", "save_count", "share_count", "enquiry_count", "news_count",
	}).AddRow(
		franchiseID, "Test Franchise", "test-franchise",
		"Short description", "Full description",
		int16(2020), true, true, 100,
		"51-100", "Food & Beverage", "Test Corp", "Quick Service Restaurant",
		int16(2020), 50, "John Doe", "CEO",
		"john@test.com", "https://logo.com/test",
		userID, userID, now, now,
		"franchise", []byte("{}"), 0, 0.00, 0.00, nil,
		nil, false, nil, nil, 0, false,
		4.5, 100, 500, 1000, 5000, 200, 50, 25, 10,
	)

	suite.mock.ExpectQuery(franchiseQuery).
		WithArgs(franchiseID).
		WillReturnRows(franchiseRow)

	// Business overview
	overviewID := uuid.New()
	productsJSON := `[{"name":"Product 1"}]`
	servicesJSON := `[{"name":"Service 1"}]`
	suite.mock.ExpectQuery(`SELECT id, products, services FROM franchise_business_overview WHERE franchise_id = $1`).
        WithArgs(franchiseID).
        WillReturnRows(sqlmock.NewRows([]string{"id", "products", "services"}).
            AddRow(overviewID, productsJSON, servicesJSON))

	// Investment
	investmentID := uuid.New()
	investmentQuery := `
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
    
    suite.mock.ExpectQuery(investmentQuery).
        WithArgs(franchiseID).
        WillReturnRows(sqlmock.NewRows([]string{
            "id", "initial_investment_min", "initial_investment_max",
            "franchise_fee", "royalty_percentage", "marketing_fee_percentage",
            "payback_min_months", "payback_max_months",
            "roi_min_percentage", "roi_max_percentage",
            "monthly_turnover_min", "monthly_turnover_max",
            "single_unit_cost_min", "single_unit_cost_max",
            "investment_includes", "revenue_model",
        }).AddRow(
            investmentID, 100000.0, 500000.0,
            30000.0, 5.0, 2.0,
            12, 24,
            15.0, 30.0,
            50000.0, 200000.0,
            1000.0, 5000.0,
            "Equipment, Training, Marketing", nil,
        ))

	// Operations
	operationsID := uuid.New()
    staffBreakdownJSON := `[{"role":"Manager","count":1}]`
    operationsQuery := `
        SELECT 
            id, space_min_sqft, space_max_sqft, required_property_type,
            staff_required_min, staff_required_max, staff_breakdown,
            operating_hours, training_provided, training_details,
            computer_requirements, marketing_support, preferred_locations,
            qualification_required, supply_chain_support, quality_control,
            territory_details, development_schedule, support_training, legal_compliance
        FROM franchise_operations 
        WHERE franchise_id = $1`
    
    suite.mock.ExpectQuery(operationsQuery).
        WithArgs(franchiseID).
        WillReturnRows(sqlmock.NewRows([]string{
            "id", "space_min_sqft", "space_max_sqft", "required_property_type",
            "staff_required_min", "staff_required_max", "staff_breakdown",
            "operating_hours", "training_provided", "training_details",
            "computer_requirements", "marketing_support", "preferred_locations",
            "qualification_required", "supply_chain_support", "quality_control",
            "territory_details", "development_schedule", "support_training", "legal_compliance",
        }).AddRow(
            operationsID, 500, 1000, "Commercial",
            3, 10, staffBreakdownJSON,
            "9 AM - 9 PM", true, "2 weeks training",
            "POS System", "Local marketing", "Malls",
            "High School", true, true,
            nil, nil, nil, nil,
        ))

	// Social links
	socialLinksID := uuid.New()
    socialLinksQuery := `
        SELECT id, instagram_url, facebook_url, twitter_url, linkedin_url
        FROM franchise_social_links 
        WHERE franchise_id = $1`
    
    suite.mock.ExpectQuery(socialLinksQuery).
        WithArgs(franchiseID).
        WillReturnRows(sqlmock.NewRows([]string{
            "id", "instagram_url", "facebook_url", "twitter_url", "linkedin_url",
        }).AddRow(
            socialLinksID,
            "https://instagram.com/test",
            "https://facebook.com/test",
            "https://twitter.com/test",
            "https://linkedin.com/test",
        ))

	// Cities
	cityID := uuid.New()
    citiesQuery := `
        SELECT id, franchise_id, city, state, country, created_at
        FROM listing_cities 
        WHERE franchise_id = $1
        ORDER BY city ASC`
    
    suite.mock.ExpectQuery(citiesQuery).
        WithArgs(franchiseID).
        WillReturnRows(sqlmock.NewRows([]string{
            "id", "franchise_id", "city", "state", "country", "created_at",
        }).AddRow(
            cityID, franchiseID, "Mumbai", "Maharashtra", "India", now,
        ))

	result, err := suite.handler.handleGetFullFranchise(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.Equal(suite.T(), franchiseID.String(), result.Franchise.ID)
	assert.NotNil(suite.T(), result.BusinessOverview)
	assert.NotNil(suite.T(), result.Investment)
	assert.NotNil(suite.T(), result.Operations)
	assert.NotNil(suite.T(), result.SocialLinks)
	assert.NotNil(suite.T(), result.Stats)
	assert.Len(suite.T(), result.Cities, 1)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 12. CREATE_SOCIAL_LINKS
func (suite *HandlerUnitTestSuite) TestCreateSocialLinks_Success() {
	franchiseID := uuid.New()
	socialLinksID := uuid.New()

	input := CreateSocialLinksInput{
		OperationType: "CREATE_SOCIAL_LINKS",
		FranchiseID:   franchiseID.String(),
		InstagramURL:  "https://instagram.com/test",
		FacebookURL:   "https://facebook.com/test",
		TwitterURL:    "https://twitter.com/test",
		LinkedinURL:   "https://linkedin.com/test",
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `
		INSERT INTO franchise_social_links (
			franchise_id, instagram_url, facebook_url, 
			twitter_url, linkedin_url, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`

	suite.mock.ExpectQuery(expectedQuery).
		WithArgs(
			franchiseID,
			"https://instagram.com/test",
			"https://facebook.com/test",
			"https://twitter.com/test",
			"https://linkedin.com/test",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(socialLinksID))

	result, err := suite.handler.handleCreateSocialLinks(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)
	assert.Equal(suite.T(), socialLinksID.String(), result.ID)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 13. CREATE_LISTING_STATS
func (suite *HandlerUnitTestSuite) TestCreateFranchiseStats_Success() {
	franchiseID := uuid.New()
	statsID := uuid.New()

	input := CreateFranchiseStatsInput{
		OperationType: "CREATE_LISTING_STATS",
		FranchiseID:   franchiseID.String(),
		Rating:        float64Ptr(4.5),
		RatingCount:   100,
		FollowCount:   500,
		LikesCount:    1000,
		ViewCount:     5000,
		SaveCount:     200,
		ShareCount:    50,
		EnquiryCount:  25,
		NewsCount:     10,
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `
		INSERT INTO listing_stats (
			franchise_id, rating, rating_count, follow_count, 
			likes_count, view_count, save_count, share_count,
			enquiry_count, news_count, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id`

	suite.mock.ExpectQuery(expectedQuery).
		WithArgs(
			franchiseID, 4.5, 100, 500, 1000, 5000, 200, 50, 25, 10,
			sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(statsID))

	result, err := suite.handler.handleCreateFranchiseStats(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)
	assert.Equal(suite.T(), statsID.String(), result.ID)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 14. UPDATE_LISTING_STATS
func (suite *HandlerUnitTestSuite) TestUpdateFranchiseStats_Success() {
	franchiseID := uuid.New()

	input := UpdateFranchiseStatsInput{
		OperationType: "UPDATE_LISTING_STATS",
		FranchiseID:   franchiseID.String(),
		ViewCount:     intPtr(10),
		LikesCount:    intPtr(5),
		SaveCount:     intPtr(3),
		ShareCount:    intPtr(2),
		FollowCount:   intPtr(7),
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `UPDATE listing_stats SET updated_at = $1, view_count = view_count + $2, likes_count = likes_count + $3, save_count = save_count + $4, share_count = share_count + $5, follow_count = follow_count + $6 WHERE franchise_id = $7`
	suite.mock.ExpectExec(expectedQuery).
		WithArgs(
			sqlmock.AnyArg(), 10, 5, 3, 2, 7, franchiseID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := suite.handler.handleUpdateFranchiseStats(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 15. CREATE_CATEGORY_QUESTION
func (suite *HandlerUnitTestSuite) TestCreateCategoryQuestion_Success() {
	questionID := uuid.New()

	input := CreateCategoryQuestionInput{
		OperationType: "CREATE_CATEGORY_QUESTION",
		CategoryName:  "Franchise",
		Question:      "What is the minimum investment?",
		Answer:        "Minimum investment is $100,000",
		DisplayOrder:  1,
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `
		INSERT INTO category_questions (
			category_name, question, answer, display_order, created_at
		) VALUES ($1, $2, $3, $4, $5)
		RETURNING id`

	suite.mock.ExpectQuery(expectedQuery).
		WithArgs(
			"Franchise", "What is the minimum investment?", "Minimum investment is $100,000", 1,
			sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(questionID))

	result, err := suite.handler.handleCreateCategoryQuestion(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)
	assert.Equal(suite.T(), questionID.String(), result.ID)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 16. GET_CATEGORY_QUESTIONS
func (suite *HandlerUnitTestSuite) TestGetCategoryQuestions_Success() {
	questionID := uuid.New()
	now := time.Now()

	input := GetCategoryQuestionsInput{
		OperationType: "GET_CATEGORY_QUESTIONS",
		CategoryName:  "Franchise",
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `
		SELECT id, category_name, question, answer, display_order, created_at
		FROM category_questions 
		WHERE category_name = $1
		ORDER BY display_order ASC, created_at ASC`

	expectedRows := sqlmock.NewRows([]string{
		"id", "category_name", "question", "answer", "display_order", "created_at",
	}).AddRow(
		questionID, "Franchise", "What is the minimum investment?", 
		"Minimum investment is $100,000", 1, now,
	)

	suite.mock.ExpectQuery(expectedQuery).
		WithArgs("Franchise").
		WillReturnRows(expectedRows)

	result, err := suite.handler.handleGetCategoryQuestions(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)
	assert.Len(suite.T(), result.Questions, 1)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 17. UPDATE_CATEGORY_QUESTION
func (suite *HandlerUnitTestSuite) TestUpdateCategoryQuestion_Success() {
	questionID := uuid.New()

	input := UpdateCategoryQuestionInput{
		OperationType: "UPDATE_CATEGORY_QUESTION",
		QuestionID:    questionID.String(),
		Question:      stringPtr("Modified question?"),
		Answer:        stringPtr("Modified answer"),
		DisplayOrder:  intPtr(2),
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := "UPDATE category_questions SET question = $1, answer = $2, display_order = $3 WHERE id = $4"
	suite.mock.ExpectExec(expectedQuery).
		WithArgs(
			"Modified question?", "Modified answer", 2, questionID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := suite.handler.handleUpdateCategoryQuestion(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 18. DELETE_CATEGORY_QUESTION
func (suite *HandlerUnitTestSuite) TestDeleteCategoryQuestion_Success() {
	questionID := uuid.New()

	input := DeleteCategoryQuestionInput{
		OperationType: "DELETE_CATEGORY_QUESTION",
		QuestionID:    questionID.String(),
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `DELETE FROM category_questions WHERE id = $1`
	suite.mock.ExpectExec(expectedQuery).
		WithArgs(questionID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := suite.handler.handleDeleteCategoryQuestion(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 19. CREATE_FRANCHISE_CITY
func (suite *HandlerUnitTestSuite) TestCreateFranchiseCity_Success() {
	franchiseID := uuid.New()
	cityID := uuid.New()

	input := CreateFranchiseCityInput{
		OperationType: "CREATE_FRANCHISE_CITY",
		FranchiseID:   franchiseID.String(),
		City:          "Mumbai",
		State:         "Maharashtra",
		Country:       "India",
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `
		INSERT INTO listing_cities (
			franchise_id, city, state, country, created_at
		) VALUES ($1, $2, $3, $4, $5)
		RETURNING id`

	suite.mock.ExpectQuery(expectedQuery).
		WithArgs(
			franchiseID, "Mumbai", "Maharashtra", "India",
			sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(cityID))

	result, err := suite.handler.handleCreateFranchiseCity(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)
	assert.Equal(suite.T(), cityID.String(), result.ID)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 20. GET_LISTING_CITIES
func (suite *HandlerUnitTestSuite) TestGetFranchiseCities_Success() {
	franchiseID := uuid.New()
	cityID := uuid.New()
	now := time.Now()

	input := GetFranchiseCitiesInput{
		OperationType: "GET_LISTING_CITIES",
		FranchiseID:   franchiseID.String(),
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `
		SELECT id, franchise_id, city, state, country, created_at
		FROM listing_cities 
		WHERE franchise_id = $1
		ORDER BY city ASC`

	expectedRows := sqlmock.NewRows([]string{
		"id", "franchise_id", "city", "state", "country", "created_at",
	}).AddRow(
		cityID, franchiseID, "Mumbai", "Maharashtra", "India", now,
	)

	suite.mock.ExpectQuery(expectedQuery).
		WithArgs(franchiseID).
		WillReturnRows(expectedRows)

	result, err := suite.handler.handleGetFranchiseCities(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)
	assert.Len(suite.T(), result.Cities, 1)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 21. DELETE_FRANCHISE_CITY
func (suite *HandlerUnitTestSuite) TestDeleteFranchiseCity_Success() {
	cityID := uuid.New()

	input := DeleteFranchiseCityInput{
		OperationType: "DELETE_FRANCHISE_CITY",
		CityID:        cityID.String(),
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `DELETE FROM listing_cities WHERE id = $1`
	suite.mock.ExpectExec(expectedQuery).
		WithArgs(cityID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := suite.handler.handleDeleteFranchiseCity(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// 22. UPDATE_SOCIAL_LINKS
func (suite *HandlerUnitTestSuite) TestUpdateSocialLinks_Success() {
	franchiseID := uuid.New()

	input := UpdateSocialLinksInput{
		OperationType: "UPDATE_SOCIAL_LINKS",
		FranchiseID:   franchiseID.String(),
		InstagramURL:  stringPtr("https://instagram.com/modified"),
		FacebookURL:   stringPtr("https://facebook.com/modified"),
	}

	inputJSON, _ := json.Marshal(input)

	expectedQuery := `UPDATE franchise_social_links SET updated_at = $1, instagram_url = $2, facebook_url = $3 WHERE franchise_id = $4`
	suite.mock.ExpectExec(expectedQuery).
		WithArgs(
			sqlmock.AnyArg(),
			"https://instagram.com/modified",
			"https://facebook.com/modified",
			franchiseID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := suite.handler.handleUpdateSocialLinks(suite.ctx, string(inputJSON))

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), result)
	assert.True(suite.T(), result.Success)

	err = suite.mock.ExpectationsWereMet()
	assert.NoError(suite.T(), err)
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

func intPtr(i int) *int {
	return &i
}

func int16Ptr(i int16) *int16 {
	return &i
}

func float64Ptr(f float64) *float64 {
	return &f
}

func stringPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}

// ============================================================================
// MOCK IMPLEMENTATIONS FOR TESTING HANDLE FUNCTION
// ============================================================================

type mockJob struct {
	variables string
}

func (m *mockJob) GetKey() int64 {
	return 123
}

func (m *mockJob) GetProcessInstanceKey() int64 {
	return 456
}

func (m *mockJob) GetProcessDefinitionKey() int64 {
	return 789
}

func (m *mockJob) GetVariables() string {
	return m.variables
}

type mockJobClient struct {
	completeJobCount int
	failJobCount     int
}

func (m *mockJobClient) NewCompleteJobCommand() interface{} {
	return &mockCompleteJobCommand{client: m}
}

func (m *mockJobClient) NewThrowErrorCommand() interface{} {
	return &mockThrowErrorCommand{client: m}
}

type mockCompleteJobCommand struct {
	client *mockJobClient
}

func (m *mockCompleteJobCommand) JobKey(key int64) interface{} {
	return m
}

func (m *mockCompleteJobCommand) VariablesFromObject(obj interface{}) (interface{}, error) {
	return m, nil
}

func (m *mockCompleteJobCommand) Send(ctx context.Context) (interface{}, error) {
	m.client.completeJobCount++
	return nil, nil
}

type mockThrowErrorCommand struct {
	client *mockJobClient
}

func (m *mockThrowErrorCommand) JobKey(key int64) interface{} {
	return m
}

func (m *mockThrowErrorCommand) ErrorCode(code string) interface{} {
	return m
}

func (m *mockThrowErrorCommand) ErrorMessage(msg string) interface{} {
	return m
}

func (m *mockThrowErrorCommand) Send(ctx context.Context) (interface{}, error) {
	m.client.failJobCount++
	return nil, nil
}
