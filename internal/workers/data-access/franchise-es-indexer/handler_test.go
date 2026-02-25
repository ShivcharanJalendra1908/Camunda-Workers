package franchiseesindexer

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
)

// ============================================================================
// TEST SUITE
// ============================================================================

type HandlerTestSuite struct {
	suite.Suite
	db      *sql.DB
	mock    sqlmock.Sqlmock
	handler *Handler
	ctx     context.Context
}

func (suite *HandlerTestSuite) SetupTest() {
	var err error

	// Use QueryMatcherRegexp so partial query strings match correctly
	suite.db, suite.mock, err = sqlmock.New()
	require.NoError(suite.T(), err)

	log := logger.NewNoOpLogger()
	cfg := &Config{
		RequestTimeout: 10 * time.Second,
	}

	suite.ctx = context.Background()

	suite.handler = &Handler{
		config:       cfg,
		db:           suite.db,
		esClient:     nil, // ES is a concrete struct, tested via integration only
		logger:       log,
		errorHandler: errors.NewErrorHandler(log),
	}
}

func (suite *HandlerTestSuite) TearDownTest() {
	suite.db.Close()
}

func TestFranchiseESIndexerHandler(t *testing.T) {
	suite.Run(t, new(HandlerTestSuite))
}

// ============================================================================
// buildESDocument — happy path
// ============================================================================

func (suite *HandlerTestSuite) TestBuildESDocument_Success() {
	franchiseID := uuid.New().String()
	now := time.Now()

	// --- Main franchise row ---
	suite.mock.ExpectQuery(`SELECT`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "slug", "short_description", "description",
			"founded_year", "trusted_seller", "verified", "total_outlets",
			"parent_company", "business_type", "logo_url",
			"created_at", "updated_at",
		}).AddRow(
			franchiseID,
			"Test Franchise",
			"test-franchise",
			"Short desc",
			"Long desc",
			int32(2018),
			true,
			true,
			int32(25),
			"Parent Co",
			"FOOD",
			"https://logo.png",
			now,
			now,
		))

	// --- Industry (optional — returning no rows is fine) ---
	suite.mock.ExpectQuery(`SELECT DISTINCT i\.id`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	// --- Categories (empty result set) ---
	suite.mock.ExpectQuery(`SELECT`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "slug", "is_primary", "sub_id", "sub_name", "sub_slug",
		}))

	// --- Cities (empty result set) ---
	suite.mock.ExpectQuery(`SELECT DISTINCT city`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{"city"}))

	// --- Investment (no row) ---
	suite.mock.ExpectQuery(`SELECT initial_investment_min`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	// --- Operations (no row) ---
	suite.mock.ExpectQuery(`SELECT space_min_sqft`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	// --- Stats (no row) ---
	suite.mock.ExpectQuery(`SELECT rating`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	// --- Business overview (no row) ---
	suite.mock.ExpectQuery(`SELECT products`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	doc, err := suite.handler.buildESDocument(suite.ctx, franchiseID)

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), doc)
	assert.Equal(suite.T(), franchiseID, doc["franchise_id"])
	assert.Equal(suite.T(), "Test Franchise", doc["name"])
	assert.Equal(suite.T(), "test-franchise", doc["slug"])
	assert.Equal(suite.T(), true, doc["verified"])
	assert.Equal(suite.T(), true, doc["trusted_seller"])

	assert.NoError(suite.T(), suite.mock.ExpectationsWereMet())
}

// ============================================================================
// buildESDocument — with full optional data
// ============================================================================

func (suite *HandlerTestSuite) TestBuildESDocument_WithAllOptionalData() {
	franchiseID := uuid.New().String()
	industryID := uuid.New().String()
	catID := uuid.New().String()
	now := time.Now()

	// Main franchise
	suite.mock.ExpectQuery(`SELECT`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "slug", "short_description", "description",
			"founded_year", "trusted_seller", "verified", "total_outlets",
			"parent_company", "business_type", "logo_url",
			"created_at", "updated_at",
		}).AddRow(
			franchiseID, "Full Franchise", "full-franchise",
			"Short", "Long", int32(2015),
			true, true, int32(100),
			"BigCorp", "RETAIL", "https://logo.png",
			now, now,
		))

	// Industry — returns a row
	suite.mock.ExpectQuery(`SELECT DISTINCT i\.id`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "slug", "color_hex"}).
			AddRow(industryID, "Food & Bev", "food-bev", "#FF0000"))

	// Categories — one row with sub-category
	suite.mock.ExpectQuery(`SELECT`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "slug", "is_primary", "sub_id", "sub_name", "sub_slug",
		}).AddRow(catID, "Fast Food", "fast-food", true, nil, nil, nil))

	// Cities
	suite.mock.ExpectQuery(`SELECT DISTINCT city`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{"city"}).
			AddRow("Mumbai").AddRow("Delhi"))

	// Investment
	suite.mock.ExpectQuery(`SELECT initial_investment_min`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{
			"initial_investment_min", "initial_investment_max", "franchise_fee", "royalty_percentage",
		}).AddRow(100000.0, 500000.0, 30000.0, 5.0))

	// Operations
	suite.mock.ExpectQuery(`SELECT space_min_sqft`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{
			"space_min_sqft", "space_max_sqft", "staff_required_min", "staff_required_max", "training_provided",
		}).AddRow(int32(500), int32(1000), int32(3), int32(10), true))

	// Stats
	suite.mock.ExpectQuery(`SELECT rating`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{
			"rating", "rating_count", "follow_count", "view_count", "enquiry_count",
		}).AddRow(4.5, int32(100), int32(500), int32(5000), int32(25)))

	// Business overview
	suite.mock.ExpectQuery(`SELECT products`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{"products", "services"}).
			AddRow([]byte(`["Product A"]`), []byte(`["Service A"]`)))

	doc, err := suite.handler.buildESDocument(suite.ctx, franchiseID)

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), doc)
	assert.Equal(suite.T(), franchiseID, doc["franchise_id"])

	// Optional fields should be present
	assert.NotNil(suite.T(), doc["industry"])
	assert.NotNil(suite.T(), doc["categories"])
	assert.NotNil(suite.T(), doc["cities"])
	assert.NotNil(suite.T(), doc["investment"])
	assert.NotNil(suite.T(), doc["operations"])
	assert.NotNil(suite.T(), doc["stats"])
	assert.NotNil(suite.T(), doc["business_overview"])

	cities, ok := doc["cities"].([]string)
	assert.True(suite.T(), ok)
	assert.Len(suite.T(), cities, 2)

	assert.NoError(suite.T(), suite.mock.ExpectationsWereMet())
}

// ============================================================================
// buildESDocument — main query fails
// ============================================================================

func (suite *HandlerTestSuite) TestBuildESDocument_MainQueryFails() {
	franchiseID := uuid.New().String()

	suite.mock.ExpectQuery(`SELECT`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	doc, err := suite.handler.buildESDocument(suite.ctx, franchiseID)

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), doc)
	assert.NoError(suite.T(), suite.mock.ExpectationsWereMet())
}

// ============================================================================
// parseInput
// entities.Job in zeebe v8 is a concrete struct, not an interface.
// We cannot pass a *mockJob to parseInput directly.
// Instead we test the parsing logic by calling the internal helper
// parseVariables (extracted from parseInput logic) or by constructing
// a real entities.Job via its exported fields.
//
// The cleanest unit-test approach: test parseInput indirectly through
// the exported Variables field of entities.Job.
// ============================================================================

func (suite *HandlerTestSuite) TestParseInput_Success() {
	franchiseID := uuid.New().String()
	variables := `{"franchise_id":"` + franchiseID + `","operation":"INDEX"}`

	input, err := suite.handler.parseInputFromVariables(variables)

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), input)
	assert.Equal(suite.T(), franchiseID, input.FranchiseID)
	assert.Equal(suite.T(), "INDEX", input.Operation)
}

func (suite *HandlerTestSuite) TestParseInput_DefaultOperation() {
	franchiseID := uuid.New().String()
	variables := `{"franchise_id":"` + franchiseID + `"}`

	input, err := suite.handler.parseInputFromVariables(variables)

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), input)
	assert.Equal(suite.T(), "INDEX", input.Operation)
}

func (suite *HandlerTestSuite) TestParseInput_MissingFranchiseID() {
	variables := `{"operation":"INDEX"}`

	input, err := suite.handler.parseInputFromVariables(variables)

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), input)
}

func (suite *HandlerTestSuite) TestParseInput_InvalidJSON() {
	variables := `{invalid json`

	input, err := suite.handler.parseInputFromVariables(variables)

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), input)
}

// ============================================================================
// indexToES — skipped (integration concern)
// ============================================================================

func (suite *HandlerTestSuite) TestIndexToES_Skipped() {
	suite.T().Skip(`
ElasticsearchClient is a concrete struct.
Indexing must be tested via integration tests (Docker ES).
Unit test scope ends at document creation.
`)
}
