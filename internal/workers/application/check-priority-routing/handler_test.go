// internal/workers/application/check-priority-routing/handler_test.go
package checkpriorityrouting

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"camunda-workers/internal/common/logger"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{
		CacheTTL: 30 * time.Minute,
	}
}

func setupRedis(t *testing.T) *redis.Client {
	mr, err := miniredis.Run()
	assert.NoError(t, err)
	t.Cleanup(mr.Close)

	return redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
}

func setupMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db, mock
}

func createTestInput(franchiseID string) *Input {
	return &Input{
		FranchiseID: franchiseID,
	}
}

// Create a test logger that implements your logger.Logger interface
type testLogger struct {
	t *testing.T
}

func (tl *testLogger) Debug(msg string, fields map[string]interface{}) {
	tl.t.Logf("DEBUG: %s %v", msg, fields)
}

func (tl *testLogger) Info(msg string, fields map[string]interface{}) {
	tl.t.Logf("INFO: %s %v", msg, fields)
}

func (tl *testLogger) Warn(msg string, fields map[string]interface{}) {
	tl.t.Logf("WARN: %s %v", msg, fields)
}

func (tl *testLogger) Error(msg string, fields map[string]interface{}) {
	tl.t.Logf("ERROR: %s %v", msg, fields)
}

func (tl *testLogger) WithFields(fields map[string]interface{}) logger.Logger {
	return tl // Simple implementation for testing
}

func (tl *testLogger) WithError(err error) logger.Logger {
	return tl.WithFields(map[string]interface{}{"error": err})
}

func (t *testLogger) With(fields map[string]interface{}) logger.Logger {
	return t
}

func newTestLogger(t *testing.T) logger.Logger {
	return &testLogger{t: t}
}

// ==========================
// Core Functionality Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	tests := []struct {
		name             string
		franchiseID      string
		accountType      string
		setupCache       bool
		cacheValue       string
		setupDB          bool
		expectedPremium  bool
		expectedPriority string
	}{
		{
			name:             "premium account from cache",
			franchiseID:      "franchise-001",
			setupCache:       true,
			cacheValue:       AccountTypePremium,
			setupDB:          false,
			expectedPremium:  true,
			expectedPriority: PriorityHigh,
		},
		{
			name:             "premium account from database",
			franchiseID:      "franchise-002",
			accountType:      AccountTypePremium,
			setupCache:       false,
			setupDB:          true,
			expectedPremium:  true,
			expectedPriority: PriorityHigh,
		},
		{
			name:             "verified account from database",
			franchiseID:      "franchise-003",
			accountType:      AccountTypeVerified,
			setupCache:       false,
			setupDB:          true,
			expectedPremium:  false,
			expectedPriority: PriorityMedium,
		},
		{
			name:             "standard account from database",
			franchiseID:      "franchise-004",
			accountType:      AccountTypeStandard,
			setupCache:       false,
			setupDB:          true,
			expectedPremium:  false,
			expectedPriority: PriorityLow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rdb := setupRedis(t)
			db, mock := setupMockDB(t)

			// Setup cache if needed
			if tt.setupCache {
				err := rdb.Set(context.Background(), "franchisor:account:"+tt.franchiseID, tt.cacheValue, 30*time.Minute).Err()
				assert.NoError(t, err)
			}

			// Setup database expectations if needed
			if tt.setupDB {
				mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
					WithArgs(tt.franchiseID).
					WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(tt.accountType))
			}

			config := createTestConfig()
			handler := NewHandler(config, db, rdb, newTestLogger(t))

			input := createTestInput(tt.franchiseID)
			output, err := handler.Execute(context.Background(), input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedPremium, output.IsPremiumFranchisor)
			assert.Equal(t, tt.expectedPriority, output.RoutingPriority)

			// Verify all expectations were met
			if tt.setupDB {
				assert.NoError(t, mock.ExpectationsWereMet())
			}
		})
	}
}

func TestHandler_Execute_UnknownAccountType(t *testing.T) {
	rdb := setupRedis(t)
	db, mock := setupMockDB(t)

	franchiseID := "franchise-unknown"
	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow("unknown-type"))

	config := createTestConfig()
	handler := NewHandler(config, db, rdb, newTestLogger(t))

	input := createTestInput(franchiseID)
	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.False(t, output.IsPremiumFranchisor)
	assert.Equal(t, PriorityLow, output.RoutingPriority)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_DatabaseError(t *testing.T) {
	rdb := setupRedis(t)
	db, mock := setupMockDB(t)

	franchiseID := "franchise-error"
	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrConnDone)

	config := createTestConfig()
	handler := NewHandler(config, db, rdb, newTestLogger(t))

	input := createTestInput(franchiseID)
	output, err := handler.Execute(context.Background(), input)

	// Per REQ-BIZ-021: Default to low priority if franchisor not found
	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.False(t, output.IsPremiumFranchisor)
	assert.Equal(t, PriorityLow, output.RoutingPriority)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_FranchisorNotFound(t *testing.T) {
	rdb := setupRedis(t)
	db, mock := setupMockDB(t)

	franchiseID := "non-existent"
	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	config := createTestConfig()
	handler := NewHandler(config, db, rdb, newTestLogger(t))

	input := createTestInput(franchiseID)
	output, err := handler.Execute(context.Background(), input)

	// Per REQ-BIZ-021: Default to low priority if franchisor not found
	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.False(t, output.IsPremiumFranchisor)
	assert.Equal(t, PriorityLow, output.RoutingPriority)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_GetFranchisorAccountType_CacheHit(t *testing.T) {
	rdb := setupRedis(t)
	db, _ := setupMockDB(t)

	franchiseID := "franchise-cached"
	cacheKey := "franchisor:account:" + franchiseID

	// Pre-populate cache
	err := rdb.Set(context.Background(), cacheKey, AccountTypePremium, 30*time.Minute).Err()
	assert.NoError(t, err)

	handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

	assert.NoError(t, err)
	assert.Equal(t, AccountTypePremium, accountType)
}

func TestHandler_GetFranchisorAccountType_CacheMiss_DBHit(t *testing.T) {
	rdb := setupRedis(t)
	db, mock := setupMockDB(t)

	franchiseID := "franchise-db"
	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypeVerified))

	handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

	assert.NoError(t, err)
	assert.Equal(t, AccountTypeVerified, accountType)

	// Verify cache was set
	cacheKey := "franchisor:account:" + franchiseID
	cachedValue, err := rdb.Get(context.Background(), cacheKey).Result()
	assert.NoError(t, err)
	assert.Equal(t, AccountTypeVerified, cachedValue)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_GetFranchisorAccountType_NotFound(t *testing.T) {
	rdb := setupRedis(t)
	db, mock := setupMockDB(t)

	franchiseID := "non-existent"
	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
		WithArgs(franchiseID).
		WillReturnError(sql.ErrNoRows)

	handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "franchisor not found")
	assert.Empty(t, accountType)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_GetFranchisorAccountType_InvalidType(t *testing.T) {
	rdb := setupRedis(t)
	db, mock := setupMockDB(t)

	franchiseID := "franchise-invalid"
	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow("invalid-type"))

	handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

	// Per REQ-BIZ-022: Unknown types default to standard
	assert.NoError(t, err)
	assert.Equal(t, AccountTypeStandard, accountType)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_DeterminePriority(t *testing.T) {
	handler := NewHandler(createTestConfig(), nil, nil, newTestLogger(t))

	tests := []struct {
		name             string
		accountType      string
		expectedPriority string
	}{
		{
			name:             "premium returns high",
			accountType:      AccountTypePremium,
			expectedPriority: PriorityHigh,
		},
		{
			name:             "verified returns medium",
			accountType:      AccountTypeVerified,
			expectedPriority: PriorityMedium,
		},
		{
			name:             "standard returns low",
			accountType:      AccountTypeStandard,
			expectedPriority: PriorityLow,
		},
		{
			name:             "unknown returns low",
			accountType:      "unknown",
			expectedPriority: PriorityLow,
		},
		{
			name:             "empty returns low",
			accountType:      "",
			expectedPriority: PriorityLow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			priority := handler.determinePriority(tt.accountType)
			assert.Equal(t, tt.expectedPriority, priority)
		})
	}
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	t.Run("empty franchise ID", func(t *testing.T) {
		rdb := setupRedis(t)
		db, mock := setupMockDB(t)

		mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
			WithArgs("").
			WillReturnError(sql.ErrNoRows)

		handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

		input := createTestInput("")
		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.False(t, output.IsPremiumFranchisor)
		assert.Equal(t, PriorityLow, output.RoutingPriority)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("special characters in franchise ID", func(t *testing.T) {
		rdb := setupRedis(t)
		db, mock := setupMockDB(t)

		specialID := "franchise-@#$%"
		mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
			WithArgs(specialID).
			WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypePremium))

		handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

		input := createTestInput(specialID)
		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.True(t, output.IsPremiumFranchisor)
		assert.Equal(t, PriorityHigh, output.RoutingPriority)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("cache populated from database", func(t *testing.T) {
		rdb := setupRedis(t)
		db, mock := setupMockDB(t)

		franchiseID := "franchise-cache-test"

		// First call - cache miss, DB hit
		mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
			WithArgs(franchiseID).
			WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypePremium))

		handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

		// First execution
		output1, err := handler.Execute(context.Background(), createTestInput(franchiseID))
		assert.NoError(t, err)
		assert.True(t, output1.IsPremiumFranchisor)

		// Verify cache was populated
		cacheKey := "franchisor:account:" + franchiseID
		cachedValue, err := rdb.Get(context.Background(), cacheKey).Result()
		assert.NoError(t, err)
		assert.Equal(t, AccountTypePremium, cachedValue)

		// Second execution - should use cache (no DB query expected)
		output2, err := handler.Execute(context.Background(), createTestInput(franchiseID))
		assert.NoError(t, err)
		assert.True(t, output2.IsPremiumFranchisor)

		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	rdb := setupRedis(t)
	db, mock := setupMockDB(t)

	franchiseID := "premium-franchise"

	// Mock database query for first call
	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypePremium))

	config := createTestConfig()
	handler := NewHandler(config, db, rdb, newTestLogger(t))

	// First call - should query database and populate cache
	input := createTestInput(franchiseID)
	output1, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output1)
	assert.True(t, output1.IsPremiumFranchisor)
	assert.Equal(t, PriorityHigh, output1.RoutingPriority)

	// Second call - should hit cache (no additional DB query expected)
	output2, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output2)
	assert.True(t, output2.IsPremiumFranchisor)
	assert.Equal(t, PriorityHigh, output2.RoutingPriority)

	// Verify all database expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute_CacheHit(b *testing.B) {
	mr, _ := miniredis.Run()
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	db, _ := setupMockDB(&testing.T{})

	// Pre-populate cache
	rdb.Set(context.Background(), "franchisor:account:benchmark", AccountTypePremium, 30*time.Minute)

	config := createTestConfig()
	handler := NewHandler(config, db, rdb, newTestLogger(&testing.T{}))

	input := createTestInput("benchmark")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_DeterminePriority(b *testing.B) {
	handler := NewHandler(createTestConfig(), nil, nil, newTestLogger(&testing.T{}))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.determinePriority(AccountTypePremium)
	}
}

// // internal/workers/application/check-priority-routing/handler_test.go
// package checkpriorityrouting

// import (
// 	"context"
// 	"database/sql"
// 	"testing"
// 	"time"

// 	"camunda-workers/internal/common/logger"

// 	"github.com/DATA-DOG/go-sqlmock"
// 	"github.com/alicebob/miniredis/v2"
// 	"github.com/redis/go-redis/v9"
// 	"github.com/stretchr/testify/assert"
// )

// // ==========================
// // Test Helper Functions
// // ==========================

// func createTestConfig() *Config {
// 	return &Config{
// 		CacheTTL: 30 * time.Minute,
// 	}
// }

// func setupRedis(t *testing.T) *redis.Client {
// 	mr, err := miniredis.Run()
// 	assert.NoError(t, err)
// 	t.Cleanup(mr.Close)

// 	return redis.NewClient(&redis.Options{
// 		Addr: mr.Addr(),
// 	})
// }

// func setupMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	t.Cleanup(func() { db.Close() })
// 	return db, mock
// }

// func createTestInput(franchiseID string) *Input {
// 	return &Input{
// 		FranchiseID: franchiseID,
// 	}
// }

// // Create a test logger that implements your logger.Logger interface
// type testLogger struct {
// 	t *testing.T
// }

// // WithError implements logger.Logger.
// func (tl *testLogger) WithError(err error) logger.Logger {
// 	panic("unimplemented")
// }

// func (tl *testLogger) Debug(msg string, fields map[string]interface{}) {
// 	tl.t.Logf("DEBUG: %s %v", msg, fields)
// }

// func (tl *testLogger) Info(msg string, fields map[string]interface{}) {
// 	tl.t.Logf("INFO: %s %v", msg, fields)
// }

// func (tl *testLogger) Warn(msg string, fields map[string]interface{}) {
// 	tl.t.Logf("WARN: %s %v", msg, fields)
// }

// func (tl *testLogger) Error(msg string, fields map[string]interface{}) {
// 	tl.t.Logf("ERROR: %s %v", msg, fields)
// }

// func (tl *testLogger) WithFields(fields map[string]interface{}) logger.Logger {
// 	return tl // Simple implementation for testing
// }

// func newTestLogger(t *testing.T) logger.Logger {
// 	return &testLogger{t: t}
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name             string
// 		franchiseID      string
// 		accountType      string
// 		setupCache       bool
// 		cacheValue       string
// 		setupDB          bool
// 		expectedPremium  bool
// 		expectedPriority string
// 	}{
// 		{
// 			name:             "premium account from cache",
// 			franchiseID:      "franchise-001",
// 			setupCache:       true,
// 			cacheValue:       AccountTypePremium,
// 			setupDB:          false,
// 			expectedPremium:  true,
// 			expectedPriority: PriorityHigh,
// 		},
// 		{
// 			name:             "premium account from database",
// 			franchiseID:      "franchise-002",
// 			accountType:      AccountTypePremium,
// 			setupCache:       false,
// 			setupDB:          true,
// 			expectedPremium:  true,
// 			expectedPriority: PriorityHigh,
// 		},
// 		{
// 			name:             "verified account from database",
// 			franchiseID:      "franchise-003",
// 			accountType:      AccountTypeVerified,
// 			setupCache:       false,
// 			setupDB:          true,
// 			expectedPremium:  false,
// 			expectedPriority: PriorityMedium,
// 		},
// 		{
// 			name:             "standard account from database",
// 			franchiseID:      "franchise-004",
// 			accountType:      AccountTypeStandard,
// 			setupCache:       false,
// 			setupDB:          true,
// 			expectedPremium:  false,
// 			expectedPriority: PriorityLow,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			rdb := setupRedis(t)
// 			db, mock := setupMockDB(t)

// 			// Setup cache if needed
// 			if tt.setupCache {
// 				err := rdb.Set(context.Background(), "franchisor:account:"+tt.franchiseID, tt.cacheValue, 30*time.Minute).Err()
// 				assert.NoError(t, err)
// 			}

// 			// Setup database expectations if needed
// 			if tt.setupDB {
// 				mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 					WithArgs(tt.franchiseID).
// 					WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(tt.accountType))
// 			}

// 			config := createTestConfig()
// 			handler := NewHandler(config, db, rdb, newTestLogger(t))

// 			input := createTestInput(tt.franchiseID)
// 			output, err := handler.Execute(context.Background(), input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.Equal(t, tt.expectedPremium, output.IsPremiumFranchisor)
// 			assert.Equal(t, tt.expectedPriority, output.RoutingPriority)

// 			// Verify all expectations were met
// 			if tt.setupDB {
// 				assert.NoError(t, mock.ExpectationsWereMet())
// 			}
// 		})
// 	}
// }

// func TestHandler_Execute_UnknownAccountType(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "franchise-unknown"
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow("unknown-type"))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, rdb, newTestLogger(t))

// 	input := createTestInput(franchiseID)
// 	output, err := handler.Execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.False(t, output.IsPremiumFranchisor)
// 	assert.Equal(t, PriorityLow, output.RoutingPriority)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_DatabaseError(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "franchise-error"
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnError(sql.ErrConnDone)

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, rdb, newTestLogger(t))

// 	input := createTestInput(franchiseID)
// 	output, err := handler.Execute(context.Background(), input)

// 	// Per REQ-BIZ-021: Default to low priority if franchisor not found
// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.False(t, output.IsPremiumFranchisor)
// 	assert.Equal(t, PriorityLow, output.RoutingPriority)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_FranchisorNotFound(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "non-existent"
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnError(sql.ErrNoRows)

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, rdb, newTestLogger(t))

// 	input := createTestInput(franchiseID)
// 	output, err := handler.Execute(context.Background(), input)

// 	// Per REQ-BIZ-021: Default to low priority if franchisor not found
// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.False(t, output.IsPremiumFranchisor)
// 	assert.Equal(t, PriorityLow, output.RoutingPriority)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_GetFranchisorAccountType_CacheHit(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, _ := setupMockDB(t)

// 	franchiseID := "franchise-cached"
// 	cacheKey := "franchisor:account:" + franchiseID

// 	// Pre-populate cache
// 	err := rdb.Set(context.Background(), cacheKey, AccountTypePremium, 30*time.Minute).Err()
// 	assert.NoError(t, err)

// 	handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

// 	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

// 	assert.NoError(t, err)
// 	assert.Equal(t, AccountTypePremium, accountType)
// }

// func TestHandler_GetFranchisorAccountType_CacheMiss_DBHit(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "franchise-db"
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypeVerified))

// 	handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

// 	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

// 	assert.NoError(t, err)
// 	assert.Equal(t, AccountTypeVerified, accountType)

// 	// Verify cache was set
// 	cacheKey := "franchisor:account:" + franchiseID
// 	cachedValue, err := rdb.Get(context.Background(), cacheKey).Result()
// 	assert.NoError(t, err)
// 	assert.Equal(t, AccountTypeVerified, cachedValue)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_GetFranchisorAccountType_NotFound(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "non-existent"
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnError(sql.ErrNoRows)

// 	handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

// 	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

// 	assert.Error(t, err)
// 	assert.Contains(t, err.Error(), "franchisor not found")
// 	assert.Empty(t, accountType)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_GetFranchisorAccountType_InvalidType(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "franchise-invalid"
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow("invalid-type"))

// 	handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

// 	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

// 	// Per REQ-BIZ-022: Unknown types default to standard
// 	assert.NoError(t, err)
// 	assert.Equal(t, AccountTypeStandard, accountType)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_DeterminePriority(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), nil, nil, newTestLogger(t))

// 	tests := []struct {
// 		name             string
// 		accountType      string
// 		expectedPriority string
// 	}{
// 		{
// 			name:             "premium returns high",
// 			accountType:      AccountTypePremium,
// 			expectedPriority: PriorityHigh,
// 		},
// 		{
// 			name:             "verified returns medium",
// 			accountType:      AccountTypeVerified,
// 			expectedPriority: PriorityMedium,
// 		},
// 		{
// 			name:             "standard returns low",
// 			accountType:      AccountTypeStandard,
// 			expectedPriority: PriorityLow,
// 		},
// 		{
// 			name:             "unknown returns low",
// 			accountType:      "unknown",
// 			expectedPriority: PriorityLow,
// 		},
// 		{
// 			name:             "empty returns low",
// 			accountType:      "",
// 			expectedPriority: PriorityLow,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			priority := handler.determinePriority(tt.accountType)
// 			assert.Equal(t, tt.expectedPriority, priority)
// 		})
// 	}
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	t.Run("empty franchise ID", func(t *testing.T) {
// 		rdb := setupRedis(t)
// 		db, mock := setupMockDB(t)

// 		mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 			WithArgs("").
// 			WillReturnError(sql.ErrNoRows)

// 		handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

// 		input := createTestInput("")
// 		output, err := handler.Execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		assert.False(t, output.IsPremiumFranchisor)
// 		assert.Equal(t, PriorityLow, output.RoutingPriority)

// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})

// 	t.Run("special characters in franchise ID", func(t *testing.T) {
// 		rdb := setupRedis(t)
// 		db, mock := setupMockDB(t)

// 		specialID := "franchise-@#$%"
// 		mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 			WithArgs(specialID).
// 			WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypePremium))

// 		handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

// 		input := createTestInput(specialID)
// 		output, err := handler.Execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		assert.True(t, output.IsPremiumFranchisor)
// 		assert.Equal(t, PriorityHigh, output.RoutingPriority)

// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})

// 	t.Run("cache populated from database", func(t *testing.T) {
// 		rdb := setupRedis(t)
// 		db, mock := setupMockDB(t)

// 		franchiseID := "franchise-cache-test"

// 		// First call - cache miss, DB hit
// 		mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 			WithArgs(franchiseID).
// 			WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypePremium))

// 		handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

// 		// First execution
// 		output1, err := handler.Execute(context.Background(), createTestInput(franchiseID))
// 		assert.NoError(t, err)
// 		assert.True(t, output1.IsPremiumFranchisor)

// 		// Verify cache was populated
// 		cacheKey := "franchisor:account:" + franchiseID
// 		cachedValue, err := rdb.Get(context.Background(), cacheKey).Result()
// 		assert.NoError(t, err)
// 		assert.Equal(t, AccountTypePremium, cachedValue)

// 		// Second execution - should use cache (no DB query expected)
// 		output2, err := handler.Execute(context.Background(), createTestInput(franchiseID))
// 		assert.NoError(t, err)
// 		assert.True(t, output2.IsPremiumFranchisor)

// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "premium-franchise"

// 	// Mock database query for first call
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypePremium))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, rdb, newTestLogger(t))

// 	// First call - should query database and populate cache
// 	input := createTestInput(franchiseID)
// 	output1, err := handler.Execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output1)
// 	assert.True(t, output1.IsPremiumFranchisor)
// 	assert.Equal(t, PriorityHigh, output1.RoutingPriority)

// 	// Second call - should hit cache (no additional DB query expected)
// 	output2, err := handler.Execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output2)
// 	assert.True(t, output2.IsPremiumFranchisor)
// 	assert.Equal(t, PriorityHigh, output2.RoutingPriority)

// 	// Verify all database expectations were met
// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute_CacheHit(b *testing.B) {
// 	mr, _ := miniredis.Run()
// 	defer mr.Close()

// 	rdb := redis.NewClient(&redis.Options{
// 		Addr: mr.Addr(),
// 	})

// 	db, _ := setupMockDB(&testing.T{})

// 	// Pre-populate cache
// 	rdb.Set(context.Background(), "franchisor:account:benchmark", AccountTypePremium, 30*time.Minute)

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, rdb, newTestLogger(&testing.T{}))

// 	input := createTestInput("benchmark")

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.Execute(context.Background(), input)
// 	}
// }

// func BenchmarkHandler_DeterminePriority(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), nil, nil, newTestLogger(&testing.T{}))

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.determinePriority(AccountTypePremium)
// 	}
// }

// // internal/workers/application/check-priority-routing/handler_test.go
// package checkpriorityrouting

// import (
// 	"context"
// 	"database/sql"
// 	"testing"
// 	"time"

// 	"github.com/DATA-DOG/go-sqlmock"
// 	"github.com/alicebob/miniredis/v2"
// 	"github.com/redis/go-redis/v9"
// 	"github.com/stretchr/testify/assert"
// 	"go.uber.org/zap/zaptest"
// )

// // ==========================
// // Test Helper Functions
// // ==========================

// func createTestConfig() *Config {
// 	return &Config{
// 		CacheTTL: 30 * time.Minute,
// 	}
// }

// func setupRedis(t *testing.T) *redis.Client {
// 	mr, err := miniredis.Run()
// 	assert.NoError(t, err)
// 	t.Cleanup(mr.Close)

// 	return redis.NewClient(&redis.Options{
// 		Addr: mr.Addr(),
// 	})
// }

// func setupMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	t.Cleanup(func() { db.Close() })
// 	return db, mock
// }

// func createTestInput(franchiseID string) *Input {
// 	return &Input{
// 		FranchiseID: franchiseID,
// 	}
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name             string
// 		franchiseID      string
// 		accountType      string
// 		setupCache       bool
// 		cacheValue       string
// 		setupDB          bool
// 		expectedPremium  bool
// 		expectedPriority string
// 	}{
// 		{
// 			name:             "premium account from cache",
// 			franchiseID:      "franchise-001",
// 			setupCache:       true,
// 			cacheValue:       AccountTypePremium,
// 			setupDB:          false,
// 			expectedPremium:  true,
// 			expectedPriority: PriorityHigh,
// 		},
// 		{
// 			name:             "premium account from database",
// 			franchiseID:      "franchise-002",
// 			accountType:      AccountTypePremium,
// 			setupCache:       false,
// 			setupDB:          true,
// 			expectedPremium:  true,
// 			expectedPriority: PriorityHigh,
// 		},
// 		{
// 			name:             "verified account from database",
// 			franchiseID:      "franchise-003",
// 			accountType:      AccountTypeVerified,
// 			setupCache:       false,
// 			setupDB:          true,
// 			expectedPremium:  false,
// 			expectedPriority: PriorityMedium,
// 		},
// 		{
// 			name:             "standard account from database",
// 			franchiseID:      "franchise-004",
// 			accountType:      AccountTypeStandard,
// 			setupCache:       false,
// 			setupDB:          true,
// 			expectedPremium:  false,
// 			expectedPriority: PriorityLow,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			rdb := setupRedis(t)
// 			db, mock := setupMockDB(t)

// 			// Setup cache if needed
// 			if tt.setupCache {
// 				err := rdb.Set(context.Background(), "franchisor:account:"+tt.franchiseID, tt.cacheValue, 30*time.Minute).Err()
// 				assert.NoError(t, err)
// 			}

// 			// Setup database expectations if needed
// 			if tt.setupDB {
// 				mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 					WithArgs(tt.franchiseID).
// 					WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(tt.accountType))
// 			}

// 			config := createTestConfig()
// 			handler := NewHandler(config, db, rdb, zaptest.NewLogger(t))

// 			input := createTestInput(tt.franchiseID)
// 			output, err := handler.execute(context.Background(), input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.Equal(t, tt.expectedPremium, output.IsPremiumFranchisor)
// 			assert.Equal(t, tt.expectedPriority, output.RoutingPriority)

// 			// Verify all expectations were met
// 			if tt.setupDB {
// 				assert.NoError(t, mock.ExpectationsWereMet())
// 			}
// 		})
// 	}
// }

// func TestHandler_Execute_UnknownAccountType(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "franchise-unknown"
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow("unknown-type"))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, rdb, zaptest.NewLogger(t))

// 	input := createTestInput(franchiseID)
// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.False(t, output.IsPremiumFranchisor)
// 	assert.Equal(t, PriorityLow, output.RoutingPriority)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_DatabaseError(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "franchise-error"
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnError(sql.ErrConnDone)

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, rdb, zaptest.NewLogger(t))

// 	input := createTestInput(franchiseID)
// 	output, err := handler.execute(context.Background(), input)

// 	// Per REQ-BIZ-021: Default to low priority if franchisor not found
// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.False(t, output.IsPremiumFranchisor)
// 	assert.Equal(t, PriorityLow, output.RoutingPriority)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_FranchisorNotFound(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "non-existent"
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnError(sql.ErrNoRows)

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, rdb, zaptest.NewLogger(t))

// 	input := createTestInput(franchiseID)
// 	output, err := handler.execute(context.Background(), input)

// 	// Per REQ-BIZ-021: Default to low priority if franchisor not found
// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.False(t, output.IsPremiumFranchisor)
// 	assert.Equal(t, PriorityLow, output.RoutingPriority)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_GetFranchisorAccountType_CacheHit(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, _ := setupMockDB(t)

// 	franchiseID := "franchise-cached"
// 	cacheKey := "franchisor:account:" + franchiseID

// 	// Pre-populate cache
// 	err := rdb.Set(context.Background(), cacheKey, AccountTypePremium, 30*time.Minute).Err()
// 	assert.NoError(t, err)

// 	handler := NewHandler(createTestConfig(), db, rdb, zaptest.NewLogger(t))

// 	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

// 	assert.NoError(t, err)
// 	assert.Equal(t, AccountTypePremium, accountType)
// }

// func TestHandler_GetFranchisorAccountType_CacheMiss_DBHit(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "franchise-db"
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypeVerified))

// 	handler := NewHandler(createTestConfig(), db, rdb, zaptest.NewLogger(t))

// 	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

// 	assert.NoError(t, err)
// 	assert.Equal(t, AccountTypeVerified, accountType)

// 	// Verify cache was set
// 	cacheKey := "franchisor:account:" + franchiseID
// 	cachedValue, err := rdb.Get(context.Background(), cacheKey).Result()
// 	assert.NoError(t, err)
// 	assert.Equal(t, AccountTypeVerified, cachedValue)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_GetFranchisorAccountType_NotFound(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "non-existent"
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnError(sql.ErrNoRows)

// 	handler := NewHandler(createTestConfig(), db, rdb, zaptest.NewLogger(t))

// 	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

// 	assert.Error(t, err)
// 	assert.Contains(t, err.Error(), "franchisor not found")
// 	assert.Empty(t, accountType)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_GetFranchisorAccountType_InvalidType(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "franchise-invalid"
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow("invalid-type"))

// 	handler := NewHandler(createTestConfig(), db, rdb, zaptest.NewLogger(t))

// 	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

// 	// Per REQ-BIZ-022: Unknown types default to standard
// 	assert.NoError(t, err)
// 	assert.Equal(t, AccountTypeStandard, accountType)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_DeterminePriority(t *testing.T) {
// 	handler := NewHandler(createTestConfig(), nil, nil, zaptest.NewLogger(t))

// 	tests := []struct {
// 		name             string
// 		accountType      string
// 		expectedPriority string
// 	}{
// 		{
// 			name:             "premium returns high",
// 			accountType:      AccountTypePremium,
// 			expectedPriority: PriorityHigh,
// 		},
// 		{
// 			name:             "verified returns medium",
// 			accountType:      AccountTypeVerified,
// 			expectedPriority: PriorityMedium,
// 		},
// 		{
// 			name:             "standard returns low",
// 			accountType:      AccountTypeStandard,
// 			expectedPriority: PriorityLow,
// 		},
// 		{
// 			name:             "unknown returns low",
// 			accountType:      "unknown",
// 			expectedPriority: PriorityLow,
// 		},
// 		{
// 			name:             "empty returns low",
// 			accountType:      "",
// 			expectedPriority: PriorityLow,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			priority := handler.determinePriority(tt.accountType)
// 			assert.Equal(t, tt.expectedPriority, priority)
// 		})
// 	}
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	t.Run("empty franchise ID", func(t *testing.T) {
// 		rdb := setupRedis(t)
// 		db, mock := setupMockDB(t)

// 		mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 			WithArgs("").
// 			WillReturnError(sql.ErrNoRows)

// 		handler := NewHandler(createTestConfig(), db, rdb, zaptest.NewLogger(t))

// 		input := createTestInput("")
// 		output, err := handler.execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		assert.False(t, output.IsPremiumFranchisor)
// 		assert.Equal(t, PriorityLow, output.RoutingPriority)

// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})

// 	t.Run("special characters in franchise ID", func(t *testing.T) {
// 		rdb := setupRedis(t)
// 		db, mock := setupMockDB(t)

// 		specialID := "franchise-@#$%"
// 		mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 			WithArgs(specialID).
// 			WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypePremium))

// 		handler := NewHandler(createTestConfig(), db, rdb, zaptest.NewLogger(t))

// 		input := createTestInput(specialID)
// 		output, err := handler.execute(context.Background(), input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		assert.True(t, output.IsPremiumFranchisor)
// 		assert.Equal(t, PriorityHigh, output.RoutingPriority)

// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})

// 	t.Run("cache populated from database", func(t *testing.T) {
// 		rdb := setupRedis(t)
// 		db, mock := setupMockDB(t)

// 		franchiseID := "franchise-cache-test"

// 		// First call - cache miss, DB hit
// 		mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 			WithArgs(franchiseID).
// 			WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypePremium))

// 		handler := NewHandler(createTestConfig(), db, rdb, zaptest.NewLogger(t))

// 		// First execution
// 		output1, err := handler.execute(context.Background(), createTestInput(franchiseID))
// 		assert.NoError(t, err)
// 		assert.True(t, output1.IsPremiumFranchisor)

// 		// Verify cache was populated
// 		cacheKey := "franchisor:account:" + franchiseID
// 		cachedValue, err := rdb.Get(context.Background(), cacheKey).Result()
// 		assert.NoError(t, err)
// 		assert.Equal(t, AccountTypePremium, cachedValue)

// 		// Second execution - should use cache (no DB query expected)
// 		output2, err := handler.execute(context.Background(), createTestInput(franchiseID))
// 		assert.NoError(t, err)
// 		assert.True(t, output2.IsPremiumFranchisor)

// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	rdb := setupRedis(t)
// 	db, mock := setupMockDB(t)

// 	franchiseID := "premium-franchise"

// 	// Mock database query for first call
// 	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
// 		WithArgs(franchiseID).
// 		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypePremium))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, rdb, zaptest.NewLogger(t))

// 	// First call - should query database and populate cache
// 	input := createTestInput(franchiseID)
// 	output1, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output1)
// 	assert.True(t, output1.IsPremiumFranchisor)
// 	assert.Equal(t, PriorityHigh, output1.RoutingPriority)

// 	// Second call - should hit cache (no additional DB query expected)
// 	output2, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output2)
// 	assert.True(t, output2.IsPremiumFranchisor)
// 	assert.Equal(t, PriorityHigh, output2.RoutingPriority)

// 	// Verify all database expectations were met
// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute_CacheHit(b *testing.B) {
// 	mr, _ := miniredis.Run()
// 	defer mr.Close()

// 	rdb := redis.NewClient(&redis.Options{
// 		Addr: mr.Addr(),
// 	})

// 	db, _ := setupMockDB(&testing.T{})

// 	// Pre-populate cache
// 	rdb.Set(context.Background(), "franchisor:account:benchmark", AccountTypePremium, 30*time.Minute)

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, rdb, zaptest.NewLogger(b))

// 	input := createTestInput("benchmark")

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), input)
// 	}
// }

// func BenchmarkHandler_DeterminePriority(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), nil, nil, zaptest.NewLogger(b))

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.determinePriority(AccountTypePremium)
// 	}
// }
