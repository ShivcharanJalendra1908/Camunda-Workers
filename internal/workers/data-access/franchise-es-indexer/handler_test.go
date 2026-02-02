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

	suite.db, suite.mock, err = sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(suite.T(), err)

	log := logger.NewNoOpLogger()
	cfg := &Config{
		RequestTimeout: 10 * time.Second,
	}

	suite.ctx = context.Background()

	// IMPORTANT:
	// ElasticsearchClient is a concrete struct → cannot be mocked
	// So we keep it nil for unit tests
	suite.handler = &Handler{
		config:       cfg,
		db:           suite.db,
		esClient:     nil,
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
// UNIT TEST: buildESDocument (MAIN BUSINESS LOGIC)
// ============================================================================

func (suite *HandlerTestSuite) TestBuildESDocument_Success() {
	franchiseID := uuid.New().String()
	now := time.Now()

	// MAIN FRANCHISE QUERY
	mainQuery := `
		SELECT 
			f.id, f.name, f.slug, f.short_description, f.description,
			f.founded_year, f.trusted_seller, f.verified, f.total_outlets,
			f.parent_company, f.business_type, f.logo_url,
			f.created_at, f.updated_at
		FROM franchises f
		WHERE f.id = $1
	`

	suite.mock.ExpectQuery(mainQuery).
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
			2018,
			true,
			true,
			25,
			"Parent Co",
			"FOOD",
			"https://logo.png",
			now,
			now,
		))

	// INDUSTRY (optional)
	suite.mock.ExpectQuery(`
		SELECT DISTINCT i.id, i.name, i.slug, i.color_hex
		FROM industries i
	`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	// CATEGORIES
	suite.mock.ExpectQuery(`
		SELECT 
			c.id, c.name, c.slug, fc.is_primary,
			sc.id, sc.name, sc.slug
		FROM franchise_categories fc
	`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "slug", "is_primary", "sub_id", "sub_name", "sub_slug",
		}))

	// CITIES
	suite.mock.ExpectQuery(`SELECT DISTINCT city FROM franchise_cities WHERE franchise_id = $1`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{"city"}))

	// INVESTMENT
	suite.mock.ExpectQuery(`
		SELECT initial_investment_min, initial_investment_max
	`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	// OPERATIONS
	suite.mock.ExpectQuery(`
		SELECT space_min_sqft, space_max_sqft
	`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	// STATS
	suite.mock.ExpectQuery(`
		SELECT rating, rating_count
	`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	// BUSINESS OVERVIEW
	suite.mock.ExpectQuery(`
		SELECT products, services FROM franchise_business_overview
	`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	doc, err := suite.handler.buildESDocument(suite.ctx, franchiseID)

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), doc)

	assert.Equal(suite.T(), franchiseID, doc["franchise_id"])
	assert.Equal(suite.T(), "Test Franchise", doc["name"])
	assert.Equal(suite.T(), true, doc["verified"])

	assert.NoError(suite.T(), suite.mock.ExpectationsWereMet())
}

// ============================================================================
// ES INDEX TEST (INTENTIONALLY SKIPPED – INTEGRATION CONCERN)
// ============================================================================

func (suite *HandlerTestSuite) TestIndexToES_Skipped() {
	suite.T().Skip(`
ElasticsearchClient is a concrete struct.
Indexing must be tested using integration tests (Docker ES).
Unit test scope ends at document creation.
`)

	doc := map[string]interface{}{
		"franchise_id": "f1",
		"name":         "Demo",
	}

	err := suite.handler.indexToES(suite.ctx, "f1", doc)
	assert.NoError(suite.T(), err)
}
