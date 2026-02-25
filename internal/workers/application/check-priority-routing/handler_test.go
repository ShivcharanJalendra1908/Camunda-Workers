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

// BUG FIX: validateInput uses ozzo.Required + UUID v4 regex validation.
// "franchise-001" etc. are NOT valid UUID v4 format, so all tests would fail
// validation before reaching business logic. All IDs must be valid UUID v4.
// Valid UUID v4 format: xxxxxxxx-xxxx-4xxx-[89ab]xxx-xxxxxxxxxxxx

// Pre-defined test UUIDs (valid v4 format)
const (
	testFranchiseUUID001  = "a1b2c3d4-e5f6-4789-89ab-000000000001"
	testFranchiseUUID002  = "a1b2c3d4-e5f6-4789-89ab-000000000002"
	testFranchiseUUID003  = "a1b2c3d4-e5f6-4789-89ab-000000000003"
	testFranchiseUUID004  = "a1b2c3d4-e5f6-4789-89ab-000000000004"
	testFranchiseUUIDUnk  = "a1b2c3d4-e5f6-4789-89ab-000000000005"
	testFranchiseUUIDErr  = "a1b2c3d4-e5f6-4789-89ab-000000000006"
	testFranchiseUUIDNF   = "a1b2c3d4-e5f6-4789-89ab-000000000007"
	testFranchiseUUIDCac  = "a1b2c3d4-e5f6-4789-89ab-00000000000c"
	testFranchiseUUIDDB   = "a1b2c3d4-e5f6-4789-89ab-00000000000d"
	testFranchiseUUIDInv  = "a1b2c3d4-e5f6-4789-89ab-00000000000e"
	testFranchiseBench    = "a1b2c3d4-e5f6-4789-89ab-000000000009"
	testFranchiseFull     = "a1b2c3d4-e5f6-4789-89ab-00000000000f"
	testFranchiseCacheTst = "a1b2c3d4-e5f6-4789-89ab-000000000010"
)

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
	return tl
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
			// BUG FIX: was "franchise-001" (invalid UUID) → use valid UUID v4
			name:             "premium account from cache",
			franchiseID:      testFranchiseUUID001,
			setupCache:       true,
			cacheValue:       AccountTypePremium,
			setupDB:          false,
			expectedPremium:  true,
			expectedPriority: PriorityHigh,
		},
		{
			// BUG FIX: was "franchise-002" (invalid UUID) → use valid UUID v4
			name:             "premium account from database",
			franchiseID:      testFranchiseUUID002,
			accountType:      AccountTypePremium,
			setupCache:       false,
			setupDB:          true,
			expectedPremium:  true,
			expectedPriority: PriorityHigh,
		},
		{
			// BUG FIX: was "franchise-003" (invalid UUID) → use valid UUID v4
			name:             "verified account from database",
			franchiseID:      testFranchiseUUID003,
			accountType:      AccountTypeVerified,
			setupCache:       false,
			setupDB:          true,
			expectedPremium:  false,
			expectedPriority: PriorityMedium,
		},
		{
			// BUG FIX: was "franchise-004" (invalid UUID) → use valid UUID v4
			name:             "standard account from database",
			franchiseID:      testFranchiseUUID004,
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

			if tt.setupCache {
				err := rdb.Set(context.Background(), "franchisor:account:"+tt.franchiseID, tt.cacheValue, 30*time.Minute).Err()
				assert.NoError(t, err)
			}

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

			if tt.setupDB {
				assert.NoError(t, mock.ExpectationsWereMet())
			}
		})
	}
}

func TestHandler_Execute_UnknownAccountType(t *testing.T) {
	rdb := setupRedis(t)
	db, mock := setupMockDB(t)

	// BUG FIX: was "franchise-unknown" (invalid UUID) → use valid UUID v4
	franchiseID := testFranchiseUUIDUnk
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

	// BUG FIX: was "franchise-error" (invalid UUID) → use valid UUID v4
	franchiseID := testFranchiseUUIDErr
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

	// BUG FIX: was "non-existent" (invalid UUID) → use valid UUID v4
	franchiseID := testFranchiseUUIDNF
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

	// BUG FIX: was "franchise-cached" (invalid UUID) → use valid UUID v4
	franchiseID := testFranchiseUUIDCac
	cacheKey := "franchisor:account:" + franchiseID

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

	// BUG FIX: was "franchise-db" (invalid UUID) → use valid UUID v4
	franchiseID := testFranchiseUUIDDB
	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypeVerified))

	handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

	accountType, err := handler.getFranchisorAccountType(context.Background(), franchiseID)

	assert.NoError(t, err)
	assert.Equal(t, AccountTypeVerified, accountType)

	cacheKey := "franchisor:account:" + franchiseID
	cachedValue, err := rdb.Get(context.Background(), cacheKey).Result()
	assert.NoError(t, err)
	assert.Equal(t, AccountTypeVerified, cachedValue)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_GetFranchisorAccountType_NotFound(t *testing.T) {
	rdb := setupRedis(t)
	db, mock := setupMockDB(t)

	// BUG FIX: was "non-existent" (invalid UUID) → use valid UUID v4
	franchiseID := testFranchiseUUIDNF
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

	// BUG FIX: was "franchise-invalid" (invalid UUID) → use valid UUID v4
	franchiseID := testFranchiseUUIDInv
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
		{"premium returns high", AccountTypePremium, PriorityHigh},
		{"verified returns medium", AccountTypeVerified, PriorityMedium},
		{"standard returns low", AccountTypeStandard, PriorityLow},
		{"unknown returns low", "unknown", PriorityLow},
		{"empty returns low", "", PriorityLow},
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
	t.Run("empty franchise ID - fails UUID validation", func(t *testing.T) {
		// BUG FIX: Original test expected NoError with empty franchiseID, but
		// validateInput uses ozzo.Required which rejects empty strings → returns error.
		// Corrected to assert.Error.
		rdb := setupRedis(t)
		db, _ := setupMockDB(t)

		handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

		input := createTestInput("")
		output, err := handler.Execute(context.Background(), input)

		assert.Error(t, err)
		assert.Nil(t, output)
	})

	t.Run("special characters in franchise ID - fails UUID validation", func(t *testing.T) {
		// BUG FIX: "franchise-@#$%" fails UUID regex validation.
		// validateInput strictly enforces UUID v4 format. Test corrected to assert.Error.
		rdb := setupRedis(t)
		db, _ := setupMockDB(t)

		handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

		input := createTestInput("franchise-@#$%")
		output, err := handler.Execute(context.Background(), input)

		assert.Error(t, err)
		assert.Nil(t, output)
	})

	t.Run("cache populated from database", func(t *testing.T) {
		rdb := setupRedis(t)
		db, mock := setupMockDB(t)

		// BUG FIX: was "franchise-cache-test" (invalid UUID) → use valid UUID v4
		franchiseID := testFranchiseCacheTst

		// First call - cache miss, DB hit
		mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
			WithArgs(franchiseID).
			WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypePremium))

		handler := NewHandler(createTestConfig(), db, rdb, newTestLogger(t))

		output1, err := handler.Execute(context.Background(), createTestInput(franchiseID))
		assert.NoError(t, err)
		assert.True(t, output1.IsPremiumFranchisor)

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

	// BUG FIX: was "premium-franchise" (invalid UUID) → use valid UUID v4
	franchiseID := testFranchiseFull

	mock.ExpectQuery(`SELECT account_type FROM franchisors WHERE franchise_id = \$1`).
		WithArgs(franchiseID).
		WillReturnRows(sqlmock.NewRows([]string{"account_type"}).AddRow(AccountTypePremium))

	config := createTestConfig()
	handler := NewHandler(config, db, rdb, newTestLogger(t))

	input := createTestInput(franchiseID)
	output1, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output1)
	assert.True(t, output1.IsPremiumFranchisor)
	assert.Equal(t, PriorityHigh, output1.RoutingPriority)

	// Second call - should hit cache
	output2, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output2)
	assert.True(t, output2.IsPremiumFranchisor)
	assert.Equal(t, PriorityHigh, output2.RoutingPriority)

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

	// BUG FIX: was "benchmark" (invalid UUID) → use valid UUID v4
	rdb.Set(context.Background(), "franchisor:account:"+testFranchiseBench, AccountTypePremium, 30*time.Minute)

	config := createTestConfig()
	handler := NewHandler(config, db, rdb, newTestLogger(&testing.T{}))

	// BUG FIX: was "benchmark" (invalid UUID) → use valid UUID v4
	input := createTestInput(testFranchiseBench)

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
