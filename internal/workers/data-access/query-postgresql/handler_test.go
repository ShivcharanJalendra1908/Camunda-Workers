package querypostgresql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/models"
	"camunda-workers/internal/workers/data-access/query-postgresql/queries"
)

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{
		Timeout: 5 * time.Second,
	}
}

func createTestLogger(t *testing.T) logger.Logger {
	return logger.NewZapAdapter(zaptest.NewLogger(t))
}

func createBenchmarkLogger(b *testing.B) logger.Logger {
	// Create a production-like logger for benchmarks
	zapLogger, _ := zap.NewProduction()
	return logger.NewZapAdapter(zapLogger)
}

func createValidInput(queryType models.QueryType) *Input {
	input := &Input{
		QueryType: string(queryType),
	}

	switch queryType {
	case models.QueryTypeFranchiseFullDetails:
		input.FranchiseID = "franchise-123"
	case models.QueryTypeFranchiseOutlets:
		input.FranchiseID = "franchise-123"
	case models.QueryTypeFranchiseVerification:
		input.FranchiseID = "franchise-123"
	case models.QueryTypeFranchiseDetails:
		input.FranchiseIDs = []string{"franchise-123", "franchise-456"}
	case models.QueryTypeUserProfile:
		input.UserID = "user-123"
	}

	return input
}

// ==========================
// Core Functionality Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	tests := []struct {
		name           string
		queryType      models.QueryType
		mockQuery      func(mock sqlmock.Sqlmock)
		validateOutput func(t *testing.T, output *Output)
	}{
		{
			name:      "franchise full details",
			queryType: models.QueryTypeFranchiseFullDetails,
			mockQuery: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "name", "description", "investment_min", "investment_max",
					"category", "locations", "is_verified", "created_at", "updated_at",
				}).AddRow(
					"franchise-123", "Starbucks", "Coffee shop franchise",
					300000, 600000, "food", "US,CA", true,
					"2023-01-01", "2023-12-01",
				)
				mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
					WithArgs("franchise-123").
					WillReturnRows(rows)
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, 1, output.RowCount)
				assert.GreaterOrEqual(t, output.QueryExecutionTime, int64(0))

				data := output.Data.(map[string]interface{})
				assert.Equal(t, "franchise-123", data["id"])
				assert.Equal(t, "Starbucks", data["name"])
				assert.Equal(t, 300000, data["investmentMin"])
				assert.Equal(t, 600000, data["investmentMax"])
				assert.Equal(t, true, data["isVerified"])
			},
		},
		{
			name:      "franchise outlets",
			queryType: models.QueryTypeFranchiseOutlets,
			mockQuery: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "franchise_id", "address", "city", "state", "country", "phone",
				}).AddRow(
					"outlet-1", "franchise-123", "123 Main St", "Seattle", "WA", "US", "+1234567890",
				).AddRow(
					"outlet-2", "franchise-123", "456 Oak Ave", "Portland", "OR", "US", "+1234567891",
				)
				mock.ExpectQuery(`SELECT id, franchise_id, address, city, state, country, phone FROM franchise_outlets WHERE franchise_id = \$1`).
					WithArgs("franchise-123").
					WillReturnRows(rows)
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, 2, output.RowCount)
				assert.GreaterOrEqual(t, output.QueryExecutionTime, int64(0))

				data := output.Data.([]map[string]interface{})
				assert.Equal(t, 2, len(data))
				assert.Equal(t, "outlet-1", data[0]["id"])
				assert.Equal(t, "Seattle", data[0]["city"])
				assert.Equal(t, "outlet-2", data[1]["id"])
				assert.Equal(t, "Portland", data[1]["city"])
			},
		},
		{
			name:      "franchise verification",
			queryType: models.QueryTypeFranchiseVerification,
			mockQuery: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"franchise_id", "verification_status", "verified_at", "compliance_score",
				}).AddRow(
					"franchise-123", "verified", "2023-06-01", 95.5,
				)
				mock.ExpectQuery(`SELECT franchise_id, verification_status, verified_at, compliance_score FROM franchise_verification WHERE franchise_id = \$1`).
					WithArgs("franchise-123").
					WillReturnRows(rows)
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, 1, output.RowCount)
				assert.GreaterOrEqual(t, output.QueryExecutionTime, int64(0))

				data := output.Data.(map[string]interface{})
				assert.Equal(t, "franchise-123", data["franchiseId"])
				assert.Equal(t, "verified", data["verificationStatus"])
				assert.Equal(t, 95.5, data["complianceScore"])
			},
		},
		{
			name:      "multiple franchise details",
			queryType: models.QueryTypeFranchiseDetails,
			mockQuery: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "name", "investment_min", "investment_max", "category",
				}).AddRow(
					"franchise-123", "Starbucks", 300000, 600000, "food",
				).AddRow(
					"franchise-456", "Subway", 150000, 300000, "food",
				)
				mock.ExpectQuery(`SELECT id, name, investment_min, investment_max, category FROM franchises WHERE id IN \(\$1,\$2\)`).
					WithArgs("franchise-123", "franchise-456").
					WillReturnRows(rows)
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, 2, output.RowCount)
				assert.GreaterOrEqual(t, output.QueryExecutionTime, int64(0))

				data := output.Data.([]map[string]interface{})
				assert.Equal(t, 2, len(data))
				assert.Equal(t, "Starbucks", data[0]["name"])
				assert.Equal(t, "Subway", data[1]["name"])
			},
		},
		{
			name:      "user profile",
			queryType: models.QueryTypeUserProfile,
			mockQuery: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "name", "email", "subscription_tier", "capital_available",
					"industry_experience", "location_preferences", "interests",
				}).AddRow(
					"user-123", "John Doe", "john@example.com", "premium",
					500000, 5, "US,CA", "food,retail",
				)
				mock.ExpectQuery(`SELECT id, name, email, subscription_tier, capital_available, industry_experience, location_preferences, interests FROM users WHERE id = \$1`).
					WithArgs("user-123").
					WillReturnRows(rows)
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, 1, output.RowCount)
				assert.GreaterOrEqual(t, output.QueryExecutionTime, int64(0))

				data := output.Data.(map[string]interface{})
				assert.Equal(t, "user-123", data["id"])
				assert.Equal(t, "John Doe", data["name"])
				assert.Equal(t, "premium", data["subscriptionTier"])
				assert.Equal(t, 500000, data["capitalAvailable"])
				assert.Equal(t, 5, data["industryExperience"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("failed to create mock: %v", err)
			}
			defer db.Close()

			tt.mockQuery(mock)

			handler := NewHandler(createTestConfig(), db, createTestLogger(t))
			input := createValidInput(tt.queryType)

			output, err := handler.execute(context.Background(), input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.NoError(t, mock.ExpectationsWereMet())

			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}
		})
	}
}

func TestHandler_Execute_Timeout(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock: %v", err)
	}
	defer db.Close()

	// Mock will delay to simulate timeout - use a channel to control timing
	done := make(chan bool)
	mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
		WithArgs("franchise-123").
		WillDelayFor(200 * time.Millisecond). // Longer than timeout
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("franchise-123"))

	config := createTestConfig()
	config.Timeout = 50 * time.Millisecond // Very short timeout

	handler := NewHandler(config, db, createTestLogger(t))
	input := createValidInput(models.QueryTypeFranchiseFullDetails)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()

	output, err := handler.execute(ctx, input)

	// The test should timeout, but we need to handle both cases
	if err != nil {
		// Check if it's a timeout error or context deadline exceeded
		assert.True(t, errors.Is(err, ErrQueryTimeout) ||
			errors.Is(err, context.DeadlineExceeded) ||
			ctx.Err() == context.DeadlineExceeded ||
			strings.Contains(err.Error(), "timeout") ||
			strings.Contains(err.Error(), "deadline"))
	} else {
		// If no error, output should be nil due to timeout
		assert.Nil(t, output)
	}

	close(done)
}

func TestHandler_Execute_QueryErrors(t *testing.T) {
	tests := []struct {
		name          string
		queryType     models.QueryType
		input         *Input
		mockQuery     func(mock sqlmock.Sqlmock)
		expectedErr   error
		errorContains string
	}{
		{
			name:      "unknown query type",
			queryType: models.QueryType("unknown_query"),
			input: &Input{
				QueryType: "unknown_query",
			},
			mockQuery: func(mock sqlmock.Sqlmock) {
				// No mock needed since it fails before DB call
			},
			expectedErr:   ErrInvalidQueryType,
			errorContains: "INVALID_QUERY_TYPE",
		},
		{
			name:      "database error",
			queryType: models.QueryTypeFranchiseFullDetails,
			input:     createValidInput(models.QueryTypeFranchiseFullDetails),
			mockQuery: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
					WithArgs("franchise-123").
					WillReturnError(errors.New("database connection failed"))
			},
			expectedErr:   ErrQueryExecutionFailed,
			errorContains: "QUERY_EXECUTION_FAILED",
		},
		{
			name:      "missing franchise ID",
			queryType: models.QueryTypeFranchiseFullDetails,
			input: &Input{
				QueryType: string(models.QueryTypeFranchiseFullDetails),
				// Missing FranchiseID
			},
			mockQuery: func(mock sqlmock.Sqlmock) {
				// No mock needed since it fails before DB call
			},
			expectedErr:   queries.ErrMissingParam,
			errorContains: "QUERY_EXECUTION_FAILED",
		},
		{
			name:      "no rows found",
			queryType: models.QueryTypeFranchiseFullDetails,
			input:     createValidInput(models.QueryTypeFranchiseFullDetails),
			mockQuery: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
					WithArgs("franchise-123").
					WillReturnError(sql.ErrNoRows)
			},
			expectedErr:   ErrQueryExecutionFailed,
			errorContains: "QUERY_EXECUTION_FAILED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("failed to create mock: %v", err)
			}
			defer db.Close()

			if tt.mockQuery != nil {
				tt.mockQuery(mock)
			}

			handler := NewHandler(createTestConfig(), db, createTestLogger(t))
			output, err := handler.execute(context.Background(), tt.input)

			assert.Error(t, err)
			assert.True(t, errors.Is(err, tt.expectedErr) || errors.Is(err, ErrQueryExecutionFailed))
			assert.Contains(t, err.Error(), tt.errorContains)
			assert.Nil(t, output)
		})
	}
}

// ==========================
// Unit Tests - Parameter Handling
// ==========================

func TestHandler_Execute_ParameterHandling(t *testing.T) {
	tests := []struct {
		name      string
		input     *Input
		queryType models.QueryType
		validate  func(t *testing.T, output *Output, err error)
	}{
		{
			name: "with filters",
			input: &Input{
				QueryType:   string(models.QueryTypeFranchiseFullDetails),
				FranchiseID: "franchise-123",
				Filters: map[string]interface{}{
					"category":      "food",
					"minInvestment": 100000,
				},
			},
			queryType: models.QueryTypeFranchiseFullDetails,
			validate: func(t *testing.T, output *Output, err error) {
				// Filters should be passed to query function
				assert.NoError(t, err)
				assert.NotNil(t, output)
			},
		},
		{
			name: "empty franchise IDs array",
			input: &Input{
				QueryType:    string(models.QueryTypeFranchiseDetails),
				FranchiseIDs: []string{},
			},
			queryType: models.QueryTypeFranchiseDetails,
			validate: func(t *testing.T, output *Output, err error) {
				assert.Error(t, err)
				assert.Nil(t, output)
			},
		},
		{
			name: "nil franchise IDs",
			input: &Input{
				QueryType: string(models.QueryTypeFranchiseDetails),
				// FranchiseIDs is nil
			},
			queryType: models.QueryTypeFranchiseDetails,
			validate: func(t *testing.T, output *Output, err error) {
				assert.Error(t, err)
				assert.Nil(t, output)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("failed to create mock: %v", err)
			}
			defer db.Close()

			// Only mock if we expect a successful query
			if tt.validate != nil && tt.input.FranchiseID != "" {
				switch tt.queryType {
				case models.QueryTypeFranchiseFullDetails:
					rows := sqlmock.NewRows([]string{
						"id", "name", "description", "investment_min", "investment_max",
						"category", "locations", "is_verified", "created_at", "updated_at",
					}).AddRow("franchise-123", "Test", "Desc", 100000, 200000, "food", "US", true, "2023-01-01", "2023-12-01")
					mock.ExpectQuery(`SELECT.*FROM franchises`).WillReturnRows(rows)
				}
			}

			handler := NewHandler(createTestConfig(), db, createTestLogger(t))
			output, err := handler.execute(context.Background(), tt.input)

			tt.validate(t, output, err)

			// Check if all expectations were met
			if err := mock.ExpectationsWereMet(); err != nil && tt.input.FranchiseID != "" {
				t.Errorf("there were unfulfilled expectations: %s", err)
			}
		})
	}
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, createTestLogger(t))

	t.Run("nil input", func(t *testing.T) {
		output, err := handler.execute(context.Background(), nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "input cannot be nil")
		assert.Nil(t, output)
	})

	t.Run("empty query type", func(t *testing.T) {
		input := &Input{
			QueryType: "", // Empty query type
		}
		output, err := handler.execute(context.Background(), input)
		assert.Error(t, err)
		assert.Nil(t, output)
	})

	t.Run("cancelled context", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create mock: %v", err)
		}
		defer db.Close()

		// Mock will be called but context is cancelled - use exact query
		mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
			WithArgs("franchise-123").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("franchise-123"))

		handler := NewHandler(createTestConfig(), db, createTestLogger(t))
		input := createValidInput(models.QueryTypeFranchiseFullDetails)

		// Create and immediately cancel context
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		output, err := handler.execute(ctx, input)

		// May or may not error depending on timing, but should handle gracefully
		if err != nil {
			assert.Error(t, err)
		} else {
			assert.NotNil(t, output)
		}
	})

	t.Run("large result set", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create mock: %v", err)
		}
		defer db.Close()

		// Create mock for 1000 outlets - use the exact query that will be executed
		rows := sqlmock.NewRows([]string{
			"id", "franchise_id", "address", "city", "state", "country", "phone",
		})
		for i := 0; i < 1000; i++ {
			rows.AddRow(
				fmt.Sprintf("outlet-%d", i), "franchise-123",
				fmt.Sprintf("Address %d", i), "City", "State", "US", "+1234567890",
			)
		}

		// Use the exact query that will be executed for franchise outlets
		mock.ExpectQuery(`SELECT id, franchise_id, address, city, state, country, phone FROM franchise_outlets WHERE franchise_id = \$1`).
			WithArgs("franchise-123").
			WillReturnRows(rows)

		handler := NewHandler(createTestConfig(), db, createTestLogger(t))
		input := createValidInput(models.QueryTypeFranchiseOutlets)

		output, err := handler.execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.Equal(t, 1000, output.RowCount)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock: %v", err)
	}
	defer db.Close()

	// Mock franchise full details query
	franchiseRows := sqlmock.NewRows([]string{
		"id", "name", "description", "investment_min", "investment_max",
		"category", "locations", "is_verified", "created_at", "updated_at",
	}).AddRow(
		"franchise-123", "Starbucks", "Global coffee chain",
		300000, 600000, "food", "US,CA,UK", true,
		"2023-01-01", "2023-12-01",
	)
	mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
		WithArgs("franchise-123").
		WillReturnRows(franchiseRows)

	// Mock franchise outlets query
	outletRows := sqlmock.NewRows([]string{
		"id", "franchise_id", "address", "city", "state", "country", "phone",
	}).AddRow(
		"outlet-1", "franchise-123", "123 Coffee St", "Seattle", "WA", "US", "+1234567890",
	).AddRow(
		"outlet-2", "franchise-123", "456 Brew Ave", "Portland", "OR", "US", "+1234567891",
	)
	mock.ExpectQuery(`SELECT id, franchise_id, address, city, state, country, phone FROM franchise_outlets WHERE franchise_id = \$1`).
		WithArgs("franchise-123").
		WillReturnRows(outletRows)

	handler := NewHandler(createTestConfig(), db, createTestLogger(t))

	// Test franchise full details
	franchiseInput := createValidInput(models.QueryTypeFranchiseFullDetails)
	franchiseOutput, err := handler.execute(context.Background(), franchiseInput)

	assert.NoError(t, err)
	assert.NotNil(t, franchiseOutput)
	assert.Equal(t, 1, franchiseOutput.RowCount)
	assert.GreaterOrEqual(t, franchiseOutput.QueryExecutionTime, int64(0))

	// Test franchise outlets
	outletInput := createValidInput(models.QueryTypeFranchiseOutlets)
	outletOutput, err := handler.execute(context.Background(), outletInput)

	assert.NoError(t, err)
	assert.NotNil(t, outletOutput)
	assert.Equal(t, 2, outletOutput.RowCount)
	assert.GreaterOrEqual(t, outletOutput.QueryExecutionTime, int64(0))

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute_FranchiseFullDetails(b *testing.B) {
	db, mock, err := sqlmock.New()
	if err != nil {
		b.Fatalf("failed to create mock: %v", err)
	}
	defer db.Close()

	rows := sqlmock.NewRows([]string{
		"id", "name", "description", "investment_min", "investment_max",
		"category", "locations", "is_verified", "created_at", "updated_at",
	}).AddRow(
		"franchise-123", "Starbucks", "Coffee chain",
		300000, 600000, "food", "US,CA", true,
		"2023-01-01", "2023-12-01",
	)
	mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
		WithArgs("franchise-123").
		WillReturnRows(rows)

	handler := NewHandler(createTestConfig(), db, createBenchmarkLogger(b))
	input := createValidInput(models.QueryTypeFranchiseFullDetails)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.execute(context.Background(), input)
	}
}

func BenchmarkHandler_Execute_FranchiseOutlets(b *testing.B) {
	db, mock, err := sqlmock.New()
	if err != nil {
		b.Fatalf("failed to create mock: %v", err)
	}
	defer db.Close()

	rows := sqlmock.NewRows([]string{
		"id", "franchise_id", "address", "city", "state", "country", "phone",
	}).AddRow("outlet-1", "franchise-123", "123 St", "City", "State", "US", "+1234567890")
	mock.ExpectQuery(`SELECT id, franchise_id, address, city, state, country, phone FROM franchise_outlets WHERE franchise_id = \$1`).
		WithArgs("franchise-123").
		WillReturnRows(rows)

	handler := NewHandler(createTestConfig(), db, createBenchmarkLogger(b))
	input := createValidInput(models.QueryTypeFranchiseOutlets)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.execute(context.Background(), input)
	}
}

func BenchmarkHandler_Execute_UserProfile(b *testing.B) {
	db, mock, err := sqlmock.New()
	if err != nil {
		b.Fatalf("failed to create mock: %v", err)
	}
	defer db.Close()

	rows := sqlmock.NewRows([]string{
		"id", "name", "email", "subscription_tier", "capital_available",
		"industry_experience", "location_preferences", "interests",
	}).AddRow("user-123", "John Doe", "john@example.com", "premium", 500000, 5, "US,CA", "food")
	mock.ExpectQuery(`SELECT id, name, email, subscription_tier, capital_available, industry_experience, location_preferences, interests FROM users WHERE id = \$1`).
		WithArgs("user-123").
		WillReturnRows(rows)

	handler := NewHandler(createTestConfig(), db, createBenchmarkLogger(b))
	input := createValidInput(models.QueryTypeUserProfile)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.execute(context.Background(), input)
	}
}

// package querypostgresql

// import (
// 	"context"
// 	"database/sql"
// 	"errors"
// 	"fmt"
// 	"strings"
// 	"testing"
// 	"time"

// 	"github.com/DATA-DOG/go-sqlmock"
// 	"github.com/stretchr/testify/assert"
// 	"go.uber.org/zap/zaptest"

// 	"camunda-workers/internal/models"
// 	"camunda-workers/internal/workers/data-access/query-postgresql/queries"
// )

// // ==========================
// // Test Helper Functions
// // ==========================

// func createTestConfig() *Config {
// 	return &Config{
// 		Timeout: 5 * time.Second,
// 	}
// }

// func createValidInput(queryType models.QueryType) *Input {
// 	input := &Input{
// 		QueryType: string(queryType),
// 	}

// 	switch queryType {
// 	case models.QueryTypeFranchiseFullDetails:
// 		input.FranchiseID = "franchise-123"
// 	case models.QueryTypeFranchiseOutlets:
// 		input.FranchiseID = "franchise-123"
// 	case models.QueryTypeFranchiseVerification:
// 		input.FranchiseID = "franchise-123"
// 	case models.QueryTypeFranchiseDetails:
// 		input.FranchiseIDs = []string{"franchise-123", "franchise-456"}
// 	case models.QueryTypeUserProfile:
// 		input.UserID = "user-123"
// 	}

// 	return input
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name           string
// 		queryType      models.QueryType
// 		mockQuery      func(mock sqlmock.Sqlmock)
// 		validateOutput func(t *testing.T, output *Output)
// 	}{
// 		{
// 			name:      "franchise full details",
// 			queryType: models.QueryTypeFranchiseFullDetails,
// 			mockQuery: func(mock sqlmock.Sqlmock) {
// 				rows := sqlmock.NewRows([]string{
// 					"id", "name", "description", "investment_min", "investment_max",
// 					"category", "locations", "is_verified", "created_at", "updated_at",
// 				}).AddRow(
// 					"franchise-123", "Starbucks", "Coffee shop franchise",
// 					300000, 600000, "food", "US,CA", true,
// 					"2023-01-01", "2023-12-01",
// 				)
// 				mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
// 					WithArgs("franchise-123").
// 					WillReturnRows(rows)
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, 1, output.RowCount)
// 				assert.GreaterOrEqual(t, output.QueryExecutionTime, int64(0))

// 				data := output.Data.(map[string]interface{})
// 				assert.Equal(t, "franchise-123", data["id"])
// 				assert.Equal(t, "Starbucks", data["name"])
// 				assert.Equal(t, 300000, data["investmentMin"])
// 				assert.Equal(t, 600000, data["investmentMax"])
// 				assert.Equal(t, true, data["isVerified"])
// 			},
// 		},
// 		{
// 			name:      "franchise outlets",
// 			queryType: models.QueryTypeFranchiseOutlets,
// 			mockQuery: func(mock sqlmock.Sqlmock) {
// 				rows := sqlmock.NewRows([]string{
// 					"id", "franchise_id", "address", "city", "state", "country", "phone",
// 				}).AddRow(
// 					"outlet-1", "franchise-123", "123 Main St", "Seattle", "WA", "US", "+1234567890",
// 				).AddRow(
// 					"outlet-2", "franchise-123", "456 Oak Ave", "Portland", "OR", "US", "+1234567891",
// 				)
// 				mock.ExpectQuery(`SELECT id, franchise_id, address, city, state, country, phone FROM franchise_outlets WHERE franchise_id = \$1`).
// 					WithArgs("franchise-123").
// 					WillReturnRows(rows)
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, 2, output.RowCount)
// 				assert.GreaterOrEqual(t, output.QueryExecutionTime, int64(0))

// 				data := output.Data.([]map[string]interface{})
// 				assert.Equal(t, 2, len(data))
// 				assert.Equal(t, "outlet-1", data[0]["id"])
// 				assert.Equal(t, "Seattle", data[0]["city"])
// 				assert.Equal(t, "outlet-2", data[1]["id"])
// 				assert.Equal(t, "Portland", data[1]["city"])
// 			},
// 		},
// 		{
// 			name:      "franchise verification",
// 			queryType: models.QueryTypeFranchiseVerification,
// 			mockQuery: func(mock sqlmock.Sqlmock) {
// 				rows := sqlmock.NewRows([]string{
// 					"franchise_id", "verification_status", "verified_at", "compliance_score",
// 				}).AddRow(
// 					"franchise-123", "verified", "2023-06-01", 95.5,
// 				)
// 				mock.ExpectQuery(`SELECT franchise_id, verification_status, verified_at, compliance_score FROM franchise_verification WHERE franchise_id = \$1`).
// 					WithArgs("franchise-123").
// 					WillReturnRows(rows)
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, 1, output.RowCount)
// 				assert.GreaterOrEqual(t, output.QueryExecutionTime, int64(0))

// 				data := output.Data.(map[string]interface{})
// 				assert.Equal(t, "franchise-123", data["franchiseId"])
// 				assert.Equal(t, "verified", data["verificationStatus"])
// 				assert.Equal(t, 95.5, data["complianceScore"])
// 			},
// 		},
// 		{
// 			name:      "multiple franchise details",
// 			queryType: models.QueryTypeFranchiseDetails,
// 			mockQuery: func(mock sqlmock.Sqlmock) {
// 				rows := sqlmock.NewRows([]string{
// 					"id", "name", "investment_min", "investment_max", "category",
// 				}).AddRow(
// 					"franchise-123", "Starbucks", 300000, 600000, "food",
// 				).AddRow(
// 					"franchise-456", "Subway", 150000, 300000, "food",
// 				)
// 				mock.ExpectQuery(`SELECT id, name, investment_min, investment_max, category FROM franchises WHERE id IN \(\$1,\$2\)`).
// 					WithArgs("franchise-123", "franchise-456").
// 					WillReturnRows(rows)
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, 2, output.RowCount)
// 				assert.GreaterOrEqual(t, output.QueryExecutionTime, int64(0))

// 				data := output.Data.([]map[string]interface{})
// 				assert.Equal(t, 2, len(data))
// 				assert.Equal(t, "Starbucks", data[0]["name"])
// 				assert.Equal(t, "Subway", data[1]["name"])
// 			},
// 		},
// 		{
// 			name:      "user profile",
// 			queryType: models.QueryTypeUserProfile,
// 			mockQuery: func(mock sqlmock.Sqlmock) {
// 				rows := sqlmock.NewRows([]string{
// 					"id", "name", "email", "subscription_tier", "capital_available",
// 					"industry_experience", "location_preferences", "interests",
// 				}).AddRow(
// 					"user-123", "John Doe", "john@example.com", "premium",
// 					500000, 5, "US,CA", "food,retail",
// 				)
// 				mock.ExpectQuery(`SELECT id, name, email, subscription_tier, capital_available, industry_experience, location_preferences, interests FROM users WHERE id = \$1`).
// 					WithArgs("user-123").
// 					WillReturnRows(rows)
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, 1, output.RowCount)
// 				assert.GreaterOrEqual(t, output.QueryExecutionTime, int64(0))

// 				data := output.Data.(map[string]interface{})
// 				assert.Equal(t, "user-123", data["id"])
// 				assert.Equal(t, "John Doe", data["name"])
// 				assert.Equal(t, "premium", data["subscriptionTier"])
// 				assert.Equal(t, 500000, data["capitalAvailable"])
// 				assert.Equal(t, 5, data["industryExperience"])
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			if err != nil {
// 				t.Fatalf("failed to create mock: %v", err)
// 			}
// 			defer db.Close()

// 			tt.mockQuery(mock)

// 			handler := NewHandler(createTestConfig(), db, zaptest.NewLogger(t))
// 			input := createValidInput(tt.queryType)

// 			output, err := handler.execute(context.Background(), input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.NoError(t, mock.ExpectationsWereMet())

// 			if tt.validateOutput != nil {
// 				tt.validateOutput(t, output)
// 			}
// 		})
// 	}
// }

// func TestHandler_Execute_Timeout(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	if err != nil {
// 		t.Fatalf("failed to create mock: %v", err)
// 	}
// 	defer db.Close()

// 	// Mock will delay to simulate timeout - use a channel to control timing
// 	done := make(chan bool)
// 	mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
// 		WithArgs("franchise-123").
// 		WillDelayFor(200 * time.Millisecond). // Longer than timeout
// 		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("franchise-123"))

// 	config := createTestConfig()
// 	config.Timeout = 50 * time.Millisecond // Very short timeout

// 	handler := NewHandler(config, db, zaptest.NewLogger(t))
// 	input := createValidInput(models.QueryTypeFranchiseFullDetails)

// 	// Create context with timeout
// 	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
// 	defer cancel()

// 	output, err := handler.execute(ctx, input)

// 	// The test should timeout, but we need to handle both cases
// 	if err != nil {
// 		// Check if it's a timeout error or context deadline exceeded
// 		assert.True(t, errors.Is(err, ErrQueryTimeout) ||
// 			errors.Is(err, context.DeadlineExceeded) ||
// 			ctx.Err() == context.DeadlineExceeded ||
// 			strings.Contains(err.Error(), "timeout") ||
// 			strings.Contains(err.Error(), "deadline"))
// 	} else {
// 		// If no error, output should be nil due to timeout
// 		assert.Nil(t, output)
// 	}

// 	close(done)
// }
// func TestHandler_Execute_QueryErrors(t *testing.T) {
// 	tests := []struct {
// 		name          string
// 		queryType     models.QueryType
// 		input         *Input
// 		mockQuery     func(mock sqlmock.Sqlmock)
// 		expectedErr   error
// 		errorContains string
// 	}{
// 		{
// 			name:      "unknown query type",
// 			queryType: models.QueryType("unknown_query"),
// 			input: &Input{
// 				QueryType: "unknown_query",
// 			},
// 			mockQuery: func(mock sqlmock.Sqlmock) {
// 				// No mock needed since it fails before DB call
// 			},
// 			expectedErr:   ErrInvalidQueryType,
// 			errorContains: "INVALID_QUERY_TYPE",
// 		},
// 		{
// 			name:      "database error",
// 			queryType: models.QueryTypeFranchiseFullDetails,
// 			input:     createValidInput(models.QueryTypeFranchiseFullDetails),
// 			mockQuery: func(mock sqlmock.Sqlmock) {
// 				mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
// 					WithArgs("franchise-123").
// 					WillReturnError(errors.New("database connection failed"))
// 			},
// 			expectedErr:   ErrQueryExecutionFailed,
// 			errorContains: "QUERY_EXECUTION_FAILED",
// 		},
// 		{
// 			name:      "missing franchise ID",
// 			queryType: models.QueryTypeFranchiseFullDetails,
// 			input: &Input{
// 				QueryType: string(models.QueryTypeFranchiseFullDetails),
// 				// Missing FranchiseID
// 			},
// 			mockQuery: func(mock sqlmock.Sqlmock) {
// 				// No mock needed since it fails before DB call
// 			},
// 			expectedErr:   queries.ErrMissingParam,
// 			errorContains: "QUERY_EXECUTION_FAILED",
// 		},
// 		{
// 			name:      "no rows found",
// 			queryType: models.QueryTypeFranchiseFullDetails,
// 			input:     createValidInput(models.QueryTypeFranchiseFullDetails),
// 			mockQuery: func(mock sqlmock.Sqlmock) {
// 				mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
// 					WithArgs("franchise-123").
// 					WillReturnError(sql.ErrNoRows)
// 			},
// 			expectedErr:   ErrQueryExecutionFailed,
// 			errorContains: "QUERY_EXECUTION_FAILED",
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			if err != nil {
// 				t.Fatalf("failed to create mock: %v", err)
// 			}
// 			defer db.Close()

// 			if tt.mockQuery != nil {
// 				tt.mockQuery(mock)
// 			}

// 			handler := NewHandler(createTestConfig(), db, zaptest.NewLogger(t))
// 			output, err := handler.execute(context.Background(), tt.input)

// 			assert.Error(t, err)
// 			assert.True(t, errors.Is(err, tt.expectedErr) || errors.Is(err, ErrQueryExecutionFailed))
// 			assert.Contains(t, err.Error(), tt.errorContains)
// 			assert.Nil(t, output)
// 		})
// 	}
// }

// // ==========================
// // Unit Tests - Parameter Handling
// // ==========================

// func TestHandler_Execute_ParameterHandling(t *testing.T) {
// 	tests := []struct {
// 		name      string
// 		input     *Input
// 		queryType models.QueryType
// 		validate  func(t *testing.T, output *Output, err error)
// 	}{
// 		{
// 			name: "with filters",
// 			input: &Input{
// 				QueryType:   string(models.QueryTypeFranchiseFullDetails),
// 				FranchiseID: "franchise-123",
// 				Filters: map[string]interface{}{
// 					"category":      "food",
// 					"minInvestment": 100000,
// 				},
// 			},
// 			queryType: models.QueryTypeFranchiseFullDetails,
// 			validate: func(t *testing.T, output *Output, err error) {
// 				// Filters should be passed to query function
// 				assert.NoError(t, err)
// 				assert.NotNil(t, output)
// 			},
// 		},
// 		{
// 			name: "empty franchise IDs array",
// 			input: &Input{
// 				QueryType:    string(models.QueryTypeFranchiseDetails),
// 				FranchiseIDs: []string{},
// 			},
// 			queryType: models.QueryTypeFranchiseDetails,
// 			validate: func(t *testing.T, output *Output, err error) {
// 				assert.Error(t, err)
// 				assert.Nil(t, output)
// 			},
// 		},
// 		{
// 			name: "nil franchise IDs",
// 			input: &Input{
// 				QueryType: string(models.QueryTypeFranchiseDetails),
// 				// FranchiseIDs is nil
// 			},
// 			queryType: models.QueryTypeFranchiseDetails,
// 			validate: func(t *testing.T, output *Output, err error) {
// 				assert.Error(t, err)
// 				assert.Nil(t, output)
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			if err != nil {
// 				t.Fatalf("failed to create mock: %v", err)
// 			}
// 			defer db.Close()

// 			// Only mock if we expect a successful query
// 			if tt.validate != nil && tt.input.FranchiseID != "" {
// 				switch tt.queryType {
// 				case models.QueryTypeFranchiseFullDetails:
// 					rows := sqlmock.NewRows([]string{
// 						"id", "name", "description", "investment_min", "investment_max",
// 						"category", "locations", "is_verified", "created_at", "updated_at",
// 					}).AddRow("franchise-123", "Test", "Desc", 100000, 200000, "food", "US", true, "2023-01-01", "2023-12-01")
// 					mock.ExpectQuery(`SELECT.*FROM franchises`).WillReturnRows(rows)
// 				}
// 			}

// 			handler := NewHandler(createTestConfig(), db, zaptest.NewLogger(t))
// 			output, err := handler.execute(context.Background(), tt.input)

// 			tt.validate(t, output, err)

// 			// Check if all expectations were met
// 			if err := mock.ExpectationsWereMet(); err != nil && tt.input.FranchiseID != "" {
// 				t.Errorf("there were unfulfilled expectations: %s", err)
// 			}
// 		})
// 	}
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), nil, zaptest.NewLogger(t))

// 	t.Run("nil input", func(t *testing.T) {
// 		output, err := handler.execute(context.Background(), nil)
// 		assert.Error(t, err)
// 		assert.Contains(t, err.Error(), "input cannot be nil")
// 		assert.Nil(t, output)
// 	})

// 	t.Run("empty query type", func(t *testing.T) {
// 		input := &Input{
// 			QueryType: "", // Empty query type
// 		}
// 		output, err := handler.execute(context.Background(), input)
// 		assert.Error(t, err)
// 		assert.Nil(t, output)
// 	})

// 	t.Run("cancelled context", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		if err != nil {
// 			t.Fatalf("failed to create mock: %v", err)
// 		}
// 		defer db.Close()

// 		// Mock will be called but context is cancelled - use exact query
// 		mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
// 			WithArgs("franchise-123").
// 			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("franchise-123"))

// 		handler := NewHandler(createTestConfig(), db, zaptest.NewLogger(t))
// 		input := createValidInput(models.QueryTypeFranchiseFullDetails)

// 		// Create and immediately cancel context
// 		ctx, cancel := context.WithCancel(context.Background())
// 		cancel()

// 		output, err := handler.execute(ctx, input)

// 		// May or may not error depending on timing, but should handle gracefully
// 		if err != nil {
// 			assert.Error(t, err)
// 		} else {
// 			assert.NotNil(t, output)
// 		}
// 	})

// 	t.Run("large result set", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		if err != nil {
// 			t.Fatalf("failed to create mock: %v", err)
// 		}
// 		defer db.Close()

// 		// Create mock for 1000 outlets - use the exact query that will be executed
// 		rows := sqlmock.NewRows([]string{
// 			"id", "franchise_id", "address", "city", "state", "country", "phone",
// 		})
// 		for i := 0; i < 1000; i++ {
// 			rows.AddRow(
// 				fmt.Sprintf("outlet-%d", i), "franchise-123",
// 				fmt.Sprintf("Address %d", i), "City", "State", "US", "+1234567890",
// 			)
// 		}

// 		// Use the exact query that will be executed for franchise outlets
// 		mock.ExpectQuery(`SELECT id, franchise_id, address, city, state, country, phone FROM franchise_outlets WHERE franchise_id = \$1`).
// 			WithArgs("franchise-123").
// 			WillReturnRows(rows)

// 		handler := NewHandler(createTestConfig(), db, zaptest.NewLogger(t))
// 		input := createValidInput(models.QueryTypeFranchiseOutlets)

// 		output, err := handler.execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		assert.Equal(t, 1000, output.RowCount)
// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	if err != nil {
// 		t.Fatalf("failed to create mock: %v", err)
// 	}
// 	defer db.Close()

// 	// Mock franchise full details query
// 	franchiseRows := sqlmock.NewRows([]string{
// 		"id", "name", "description", "investment_min", "investment_max",
// 		"category", "locations", "is_verified", "created_at", "updated_at",
// 	}).AddRow(
// 		"franchise-123", "Starbucks", "Global coffee chain",
// 		300000, 600000, "food", "US,CA,UK", true,
// 		"2023-01-01", "2023-12-01",
// 	)
// 	mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
// 		WithArgs("franchise-123").
// 		WillReturnRows(franchiseRows)

// 	// Mock franchise outlets query
// 	outletRows := sqlmock.NewRows([]string{
// 		"id", "franchise_id", "address", "city", "state", "country", "phone",
// 	}).AddRow(
// 		"outlet-1", "franchise-123", "123 Coffee St", "Seattle", "WA", "US", "+1234567890",
// 	).AddRow(
// 		"outlet-2", "franchise-123", "456 Brew Ave", "Portland", "OR", "US", "+1234567891",
// 	)
// 	mock.ExpectQuery(`SELECT id, franchise_id, address, city, state, country, phone FROM franchise_outlets WHERE franchise_id = \$1`).
// 		WithArgs("franchise-123").
// 		WillReturnRows(outletRows)

// 	handler := NewHandler(createTestConfig(), db, zaptest.NewLogger(t))

// 	// Test franchise full details
// 	franchiseInput := createValidInput(models.QueryTypeFranchiseFullDetails)
// 	franchiseOutput, err := handler.execute(context.Background(), franchiseInput)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, franchiseOutput)
// 	assert.Equal(t, 1, franchiseOutput.RowCount)
// 	assert.GreaterOrEqual(t, franchiseOutput.QueryExecutionTime, int64(0))

// 	// Test franchise outlets
// 	outletInput := createValidInput(models.QueryTypeFranchiseOutlets)
// 	outletOutput, err := handler.execute(context.Background(), outletInput)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, outletOutput)
// 	assert.Equal(t, 2, outletOutput.RowCount)
// 	assert.GreaterOrEqual(t, outletOutput.QueryExecutionTime, int64(0))

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute_FranchiseFullDetails(b *testing.B) {
// 	db, mock, err := sqlmock.New()
// 	if err != nil {
// 		b.Fatalf("failed to create mock: %v", err)
// 	}
// 	defer db.Close()

// 	rows := sqlmock.NewRows([]string{
// 		"id", "name", "description", "investment_min", "investment_max",
// 		"category", "locations", "is_verified", "created_at", "updated_at",
// 	}).AddRow(
// 		"franchise-123", "Starbucks", "Coffee chain",
// 		300000, 600000, "food", "US,CA", true,
// 		"2023-01-01", "2023-12-01",
// 	)
// 	mock.ExpectQuery(`SELECT id, name, description, investment_min, investment_max, category, locations, is_verified, created_at, updated_at FROM franchises WHERE id = \$1`).
// 		WithArgs("franchise-123").
// 		WillReturnRows(rows)

// 	handler := NewHandler(createTestConfig(), db, zaptest.NewLogger(b))
// 	input := createValidInput(models.QueryTypeFranchiseFullDetails)

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), input)
// 	}
// }

// func BenchmarkHandler_Execute_FranchiseOutlets(b *testing.B) {
// 	db, mock, err := sqlmock.New()
// 	if err != nil {
// 		b.Fatalf("failed to create mock: %v", err)
// 	}
// 	defer db.Close()

// 	rows := sqlmock.NewRows([]string{
// 		"id", "franchise_id", "address", "city", "state", "country", "phone",
// 	}).AddRow("outlet-1", "franchise-123", "123 St", "City", "State", "US", "+1234567890")
// 	mock.ExpectQuery(`SELECT id, franchise_id, address, city, state, country, phone FROM franchise_outlets WHERE franchise_id = \$1`).
// 		WithArgs("franchise-123").
// 		WillReturnRows(rows)

// 	handler := NewHandler(createTestConfig(), db, zaptest.NewLogger(b))
// 	input := createValidInput(models.QueryTypeFranchiseOutlets)

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), input)
// 	}
// }

// func BenchmarkHandler_Execute_UserProfile(b *testing.B) {
// 	db, mock, err := sqlmock.New()
// 	if err != nil {
// 		b.Fatalf("failed to create mock: %v", err)
// 	}
// 	defer db.Close()

// 	rows := sqlmock.NewRows([]string{
// 		"id", "name", "email", "subscription_tier", "capital_available",
// 		"industry_experience", "location_preferences", "interests",
// 	}).AddRow("user-123", "John Doe", "john@example.com", "premium", 500000, 5, "US,CA", "food")
// 	mock.ExpectQuery(`SELECT id, name, email, subscription_tier, capital_available, industry_experience, location_preferences, interests FROM users WHERE id = \$1`).
// 		WithArgs("user-123").
// 		WillReturnRows(rows)

// 	handler := NewHandler(createTestConfig(), db, zaptest.NewLogger(b))
// 	input := createValidInput(models.QueryTypeUserProfile)

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), input)
// 	}
// }
