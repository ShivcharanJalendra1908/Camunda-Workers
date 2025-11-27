// internal/workers/infrastructure/validate-subscription/handler_test.go
package validatesubscription

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"camunda-workers/internal/common/logger"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{
		Timeout: 10 * time.Second,
	}
}

func createTestHandler(t *testing.T, db *sql.DB, redisClient *redis.Client, config *Config) *Handler {
	if config == nil {
		config = createTestConfig()
	}
	testLog := logger.NewTestLogger(t)
	return NewHandler(config, db, redisClient, testLog)
}

func createInput(userID, subscriptionTier string) *Input {
	return &Input{
		UserID:           userID,
		SubscriptionTier: subscriptionTier,
	}
}

func createSubscription(userID, tier string, isValid bool, expiresAt string) *Subscription {
	return &Subscription{
		UserID:    userID,
		Tier:      tier,
		ExpiresAt: expiresAt,
		IsValid:   isValid,
	}
}

// ==========================
// Core Functionality Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	tests := []struct {
		name           string
		input          *Input
		mockDBResult   *Subscription
		expectedOutput *Output
		validateOutput func(t *testing.T, output *Output)
	}{
		{
			name:  "valid premium subscription",
			input: createInput("user-123", "premium"),
			mockDBResult: createSubscription("user-123", "premium", true,
				time.Now().Add(24*time.Hour).Format(time.RFC3339)),
			expectedOutput: &Output{
				IsValid:   true,
				TierLevel: "premium",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.True(t, output.IsValid)
				assert.Equal(t, "premium", output.TierLevel)
			},
		},
		{
			name:  "valid free subscription",
			input: createInput("user-456", "free"),
			mockDBResult: createSubscription("user-456", "free", true,
				time.Now().Add(24*time.Hour).Format(time.RFC3339)),
			expectedOutput: &Output{
				IsValid:   true,
				TierLevel: "free",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.True(t, output.IsValid)
				assert.Equal(t, "free", output.TierLevel)
			},
		},
		{
			name:  "valid basic subscription",
			input: createInput("user-789", "basic"),
			mockDBResult: createSubscription("user-789", "basic", true,
				time.Now().Add(24*time.Hour).Format(time.RFC3339)),
			expectedOutput: &Output{
				IsValid:   true,
				TierLevel: "basic",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.True(t, output.IsValid)
				assert.Equal(t, "basic", output.TierLevel)
			},
		},
		{
			name:  "valid enterprise subscription",
			input: createInput("user-999", "enterprise"),
			mockDBResult: createSubscription("user-999", "enterprise", true,
				time.Now().Add(24*time.Hour).Format(time.RFC3339)),
			expectedOutput: &Output{
				IsValid:   true,
				TierLevel: "enterprise",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.True(t, output.IsValid)
				assert.Equal(t, "enterprise", output.TierLevel)
			},
		},
		{
			name:         "subscription without expiration",
			input:        createInput("user-nil-expiry", "premium"),
			mockDBResult: createSubscription("user-nil-expiry", "premium", true, ""),
			expectedOutput: &Output{
				IsValid:   true,
				TierLevel: "premium",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.True(t, output.IsValid)
				assert.Equal(t, "premium", output.TierLevel)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			redisClient, redisMock := redismock.NewClientMock()

			ctx := context.Background()

			// Mock Redis GET (cache miss)
			cacheKey := "sub:" + tt.input.UserID
			redisMock.ExpectGet(cacheKey).RedisNil()

			// Mock database query
			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
				AddRow(tt.mockDBResult.UserID, tt.mockDBResult.Tier,
					tt.mockDBResult.ExpiresAt, tt.mockDBResult.IsValid)
			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
				WithArgs(tt.input.UserID).
				WillReturnRows(rows)

			// Mock Redis SET (cache write)
			cachedData, _ := json.Marshal(tt.mockDBResult)
			redisMock.ExpectSet(cacheKey, cachedData, 5*time.Minute).SetVal("OK")

			handler := createTestHandler(t, db, redisClient, nil)
			output, err := handler.Execute(ctx, tt.input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedOutput.IsValid, output.IsValid)
			assert.Equal(t, tt.expectedOutput.TierLevel, output.TierLevel)

			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}

			// Verify all expectations were met
			assert.NoError(t, mock.ExpectationsWereMet())
			assert.NoError(t, redisMock.ExpectationsWereMet())
		})
	}
}

func TestHandler_Execute_CacheHit(t *testing.T) {
	t.Run("cache hit returns cached subscription", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		redisClient, redisMock := redismock.NewClientMock()

		ctx := context.Background()

		// Pre-populate cache
		cachedSub := createSubscription("cached-user", "premium", true,
			time.Now().Add(24*time.Hour).Format(time.RFC3339))
		cachedData, _ := json.Marshal(cachedSub)

		cacheKey := "sub:cached-user"
		redisMock.ExpectGet(cacheKey).SetVal(string(cachedData))

		handler := createTestHandler(t, db, redisClient, nil)
		input := createInput("cached-user", "premium")

		output, err := handler.Execute(ctx, input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.True(t, output.IsValid)
		assert.Equal(t, "premium", output.TierLevel)

		// Verify database was not queried (cache hit)
		assert.NoError(t, mock.ExpectationsWereMet())
		assert.NoError(t, redisMock.ExpectationsWereMet())
	})
}

func TestHandler_Execute_ValidationErrors(t *testing.T) {
	tests := []struct {
		name          string
		input         *Input
		mockDBError   error
		mockDBResult  *Subscription
		expectedError error
	}{
		{
			name:          "subscription not found",
			input:         createInput("non-existent-user", "premium"),
			mockDBError:   sql.ErrNoRows,
			expectedError: ErrSubscriptionInvalid,
		},
		{
			name:          "subscription marked invalid",
			input:         createInput("invalid-user", "premium"),
			mockDBResult:  createSubscription("invalid-user", "premium", false, ""),
			expectedError: ErrSubscriptionInvalid,
		},
		{
			name:  "expired subscription",
			input: createInput("expired-user", "premium"),
			mockDBResult: createSubscription("expired-user", "premium", true,
				time.Now().Add(-24*time.Hour).Format(time.RFC3339)),
			expectedError: ErrSubscriptionExpired,
		},
		{
			name:          "invalid tier level",
			input:         createInput("invalid-tier-user", "invalid-tier"),
			mockDBResult:  createSubscription("invalid-tier-user", "invalid-tier", true, ""),
			expectedError: ErrSubscriptionInvalid,
		},
		{
			name:          "database error",
			input:         createInput("db-error-user", "premium"),
			mockDBError:   errors.New("connection failed"),
			expectedError: ErrSubscriptionCheckFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			redisClient, redisMock := redismock.NewClientMock()

			ctx := context.Background()
			cacheKey := "sub:" + tt.input.UserID

			// Mock Redis GET (cache miss)
			redisMock.ExpectGet(cacheKey).RedisNil()

			query := mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
				WithArgs(tt.input.UserID)

			if tt.mockDBError != nil {
				query.WillReturnError(tt.mockDBError)
			} else {
				rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
					AddRow(tt.mockDBResult.UserID, tt.mockDBResult.Tier,
						tt.mockDBResult.ExpiresAt, tt.mockDBResult.IsValid)
				query.WillReturnRows(rows)
			}

			handler := createTestHandler(t, db, redisClient, nil)
			output, err := handler.Execute(ctx, tt.input)

			assert.Error(t, err)
			assert.True(t, errors.Is(err, tt.expectedError))
			assert.Nil(t, output)

			assert.NoError(t, mock.ExpectationsWereMet())
			assert.NoError(t, redisMock.ExpectationsWereMet())
		})
	}
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_ValidTiers(t *testing.T) {
	validTiers := []string{"free", "basic", "premium", "enterprise"}
	invalidTiers := []string{"", "invalid", "trial", "pro"}

	for _, tier := range validTiers {
		t.Run(fmt.Sprintf("valid tier: %s", tier), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			redisClient, redisMock := redismock.NewClientMock()

			ctx := context.Background()
			cacheKey := "sub:test-user"

			redisMock.ExpectGet(cacheKey).RedisNil()

			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
				AddRow("test-user", tier, time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
				WithArgs("test-user").
				WillReturnRows(rows)

			sub := createSubscription("test-user", tier, true, time.Now().Add(24*time.Hour).Format(time.RFC3339))
			cachedData, _ := json.Marshal(sub)
			redisMock.ExpectSet(cacheKey, cachedData, 5*time.Minute).SetVal("OK")

			handler := createTestHandler(t, db, redisClient, nil)
			input := createInput("test-user", tier)
			output, err := handler.Execute(ctx, input)

			assert.NoError(t, err)
			assert.True(t, output.IsValid)
			assert.Equal(t, tier, output.TierLevel)
		})
	}

	for _, tier := range invalidTiers {
		t.Run(fmt.Sprintf("invalid tier: %s", tier), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			redisClient, redisMock := redismock.NewClientMock()

			ctx := context.Background()
			cacheKey := "sub:test-user"

			redisMock.ExpectGet(cacheKey).RedisNil()

			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
				AddRow("test-user", tier, time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
				WithArgs("test-user").
				WillReturnRows(rows)

			handler := createTestHandler(t, db, redisClient, nil)
			input := createInput("test-user", tier)
			output, err := handler.Execute(ctx, input)

			assert.Error(t, err)
			assert.True(t, errors.Is(err, ErrSubscriptionInvalid))
			assert.Nil(t, output)
		})
	}
}

func TestHandler_ExpirationLogic(t *testing.T) {
	tests := []struct {
		name        string
		expiresAt   string
		shouldError bool
	}{
		{
			name:        "future expiration",
			expiresAt:   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
			shouldError: false,
		},
		{
			name:        "past expiration",
			expiresAt:   time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
			shouldError: true,
		},
		{
			name:        "empty expiration",
			expiresAt:   "",
			shouldError: false,
		},
		{
			name:        "invalid date format",
			expiresAt:   "invalid-date",
			shouldError: false, // Should not error on parse failure, treat as no expiration
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			redisClient, redisMock := redismock.NewClientMock()

			ctx := context.Background()
			cacheKey := "sub:test-user"

			redisMock.ExpectGet(cacheKey).RedisNil()

			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
				AddRow("test-user", "premium", tt.expiresAt, true)
			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
				WithArgs("test-user").
				WillReturnRows(rows)

			if !tt.shouldError {
				sub := createSubscription("test-user", "premium", true, tt.expiresAt)
				cachedData, _ := json.Marshal(sub)
				redisMock.ExpectSet(cacheKey, cachedData, 5*time.Minute).SetVal("OK")
			}

			handler := createTestHandler(t, db, redisClient, nil)
			input := createInput("test-user", "premium")
			output, err := handler.Execute(ctx, input)

			if tt.shouldError {
				assert.Error(t, err)
				assert.True(t, errors.Is(err, ErrSubscriptionExpired))
				assert.Nil(t, output)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, output)
				assert.True(t, output.IsValid)
				assert.Equal(t, "premium", output.TierLevel)
			}
		})
	}
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	t.Run("empty user ID", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		redisClient, redisMock := redismock.NewClientMock()

		ctx := context.Background()
		cacheKey := "sub:"

		redisMock.ExpectGet(cacheKey).RedisNil()

		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
			WithArgs("").
			WillReturnError(sql.ErrNoRows)

		handler := createTestHandler(t, db, redisClient, nil)
		input := createInput("", "premium")
		output, err := handler.Execute(ctx, input)

		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrSubscriptionInvalid))
		assert.Nil(t, output)
	})

	t.Run("context timeout", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		redisClient, redisMock := redismock.NewClientMock()

		config := &Config{Timeout: 1 * time.Millisecond}
		handler := createTestHandler(t, db, redisClient, config)

		cacheKey := "sub:test-user"
		redisMock.ExpectGet(cacheKey).RedisNil()

		// Mock a slow database query
		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
			WithArgs("test-user").
			WillDelayFor(10 * time.Millisecond).
			WillReturnRows(sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
				AddRow("test-user", "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true))

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()

		input := createInput("test-user", "premium")
		output, err := handler.Execute(ctx, input)

		assert.Error(t, err)
		assert.Nil(t, output)
	})
}

func TestHandler_CacheConsistency(t *testing.T) {
	t.Run("cache reflects database state", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		redisClient, redisMock := redismock.NewClientMock()

		ctx := context.Background()
		userID := "cache-test-user"
		cacheKey := "sub:" + userID

		handler := createTestHandler(t, db, redisClient, nil)

		// First call - cache miss, database hit
		sub := createSubscription(userID, "premium", true, time.Now().Add(24*time.Hour).Format(time.RFC3339))
		cachedData, _ := json.Marshal(sub)

		redisMock.ExpectGet(cacheKey).RedisNil()

		rows1 := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
			AddRow(userID, "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
			WithArgs(userID).
			WillReturnRows(rows1)

		redisMock.ExpectSet(cacheKey, cachedData, 5*time.Minute).SetVal("OK")

		input := createInput(userID, "premium")
		output1, err := handler.Execute(ctx, input)
		assert.NoError(t, err)
		assert.True(t, output1.IsValid)

		// Second call - cache hit
		redisMock.ExpectGet(cacheKey).SetVal(string(cachedData))

		output2, err := handler.Execute(ctx, input)
		assert.NoError(t, err)
		assert.True(t, output2.IsValid)
		assert.Equal(t, output1.TierLevel, output2.TierLevel)

		// Verify expectations
		assert.NoError(t, mock.ExpectationsWereMet())
		assert.NoError(t, redisMock.ExpectationsWereMet())
	})

	t.Run("cache invalidated on different users", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		redisClient, redisMock := redismock.NewClientMock()

		ctx := context.Background()
		user1 := "user-1"
		user2 := "user-2"

		handler := createTestHandler(t, db, redisClient, nil)

		// First user
		cacheKey1 := "sub:" + user1
		redisMock.ExpectGet(cacheKey1).RedisNil()

		rows1 := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
			AddRow(user1, "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
			WithArgs(user1).
			WillReturnRows(rows1)

		sub1 := createSubscription(user1, "premium", true, time.Now().Add(24*time.Hour).Format(time.RFC3339))
		cachedData1, _ := json.Marshal(sub1)
		redisMock.ExpectSet(cacheKey1, cachedData1, 5*time.Minute).SetVal("OK")

		// Second user
		cacheKey2 := "sub:" + user2
		redisMock.ExpectGet(cacheKey2).RedisNil()

		rows2 := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
			AddRow(user2, "free", time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
			WithArgs(user2).
			WillReturnRows(rows2)

		sub2 := createSubscription(user2, "free", true, time.Now().Add(24*time.Hour).Format(time.RFC3339))
		cachedData2, _ := json.Marshal(sub2)
		redisMock.ExpectSet(cacheKey2, cachedData2, 5*time.Minute).SetVal("OK")

		output1, err := handler.Execute(ctx, createInput(user1, "premium"))
		assert.NoError(t, err)
		assert.True(t, output1.IsValid)

		output2, err := handler.Execute(ctx, createInput(user2, "free"))
		assert.NoError(t, err)
		assert.True(t, output2.IsValid)

		assert.NoError(t, mock.ExpectationsWereMet())
		assert.NoError(t, redisMock.ExpectationsWereMet())
	})
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	redisClient, redisMock := redismock.NewClientMock()

	ctx := context.Background()
	handler := createTestHandler(t, db, redisClient, nil)

	tests := []struct {
		name        string
		userID      string
		tier        string
		isValid     bool
		expiresAt   string
		description string
	}{
		{
			name:        "Premium user with valid subscription",
			userID:      "premium-user-1",
			tier:        "premium",
			isValid:     true,
			expiresAt:   time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
			description: "Should validate premium subscription successfully",
		},
		{
			name:        "Free user with valid subscription",
			userID:      "free-user-1",
			tier:        "free",
			isValid:     true,
			expiresAt:   "",
			description: "Should validate free subscription without expiration",
		},
		{
			name:        "Enterprise user with valid subscription",
			userID:      "enterprise-user-1",
			tier:        "enterprise",
			isValid:     true,
			expiresAt:   time.Now().Add(365 * 24 * time.Hour).Format(time.RFC3339),
			description: "Should validate enterprise subscription successfully",
		},
		{
			name:        "User with expired subscription",
			userID:      "expired-user-1",
			tier:        "premium",
			isValid:     true,
			expiresAt:   time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
			description: "Should reject expired subscription",
		},
		{
			name:        "User with invalid subscription",
			userID:      "invalid-user-1",
			tier:        "premium",
			isValid:     false,
			expiresAt:   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
			description: "Should reject invalid subscription",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cacheKey := "sub:" + tt.userID

			// Mock Redis GET (cache miss)
			redisMock.ExpectGet(cacheKey).RedisNil()

			// Mock database query
			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
				AddRow(tt.userID, tt.tier, tt.expiresAt, tt.isValid)
			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
				WithArgs(tt.userID).
				WillReturnRows(rows)

			if tt.isValid && (tt.expiresAt == "" || isFutureDate(tt.expiresAt)) {
				// Mock Redis SET for successful validation
				sub := createSubscription(tt.userID, tt.tier, tt.isValid, tt.expiresAt)
				cachedData, _ := json.Marshal(sub)
				redisMock.ExpectSet(cacheKey, cachedData, 5*time.Minute).SetVal("OK")
			}

			input := createInput(tt.userID, tt.tier)
			output, err := handler.Execute(ctx, input)

			if tt.isValid && (tt.expiresAt == "" || isFutureDate(tt.expiresAt)) {
				// Should be valid
				assert.NoError(t, err, tt.description)
				assert.NotNil(t, output, tt.description)
				assert.True(t, output.IsValid, tt.description)
				assert.Equal(t, tt.tier, output.TierLevel, tt.description)
			} else {
				// Should be invalid
				assert.Error(t, err, tt.description)
				assert.Nil(t, output, tt.description)
			}
		})
	}

	assert.NoError(t, mock.ExpectationsWereMet())
	assert.NoError(t, redisMock.ExpectationsWereMet())
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	db, mock, err := sqlmock.New()
	require.NoError(b, err)
	defer db.Close()

	redisClient, redisMock := redismock.NewClientMock()

	ctx := context.Background()

	noOpLogger := logger.NewNoOpLogger()
	handler := NewHandler(createTestConfig(), db, redisClient, noOpLogger)

	cacheKey := "sub:benchmark-user"
	sub := createSubscription("benchmark-user", "premium", true, time.Now().Add(24*time.Hour).Format(time.RFC3339))
	cachedData, _ := json.Marshal(sub)

	for i := 0; i < b.N; i++ {
		redisMock.ExpectGet(cacheKey).RedisNil()

		rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
			AddRow("benchmark-user", "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
			WithArgs("benchmark-user").
			WillReturnRows(rows)

		redisMock.ExpectSet(cacheKey, cachedData, 5*time.Minute).SetVal("OK")
	}

	input := createInput("benchmark-user", "premium")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(ctx, input)
	}
}

func BenchmarkHandler_Execute_CacheHit(b *testing.B) {
	db, mock, err := sqlmock.New()
	require.NoError(b, err)
	defer db.Close()

	redisClient, redisMock := redismock.NewClientMock()

	ctx := context.Background()

	noOpLogger := logger.NewNoOpLogger()
	handler := NewHandler(createTestConfig(), db, redisClient, noOpLogger)

	// Pre-populate cache
	cachedSub := createSubscription("cached-user", "premium", true,
		time.Now().Add(24*time.Hour).Format(time.RFC3339))
	cachedData, _ := json.Marshal(cachedSub)

	cacheKey := "sub:cached-user"

	for i := 0; i < b.N; i++ {
		redisMock.ExpectGet(cacheKey).SetVal(string(cachedData))
	}

	input := createInput("cached-user", "premium")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(ctx, input)
	}

	// No database queries should occur
	assert.NoError(b, mock.ExpectationsWereMet())
}

// ==========================
// Helper Functions
// ==========================

func isFutureDate(dateStr string) bool {
	if dateStr == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		return false
	}
	return time.Now().Before(t)
}

// // internal/workers/infrastructure/validate-subscription/handler_test.go
// Using Real Redis
// package validatesubscription

// import (
// 	"context"
// 	"database/sql"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"testing"
// 	"time"

// 	"camunda-workers/internal/common/logger"

// 	"github.com/DATA-DOG/go-sqlmock"
// 	"github.com/redis/go-redis/v9"
// 	"github.com/stretchr/testify/assert"
// 	"github.com/stretchr/testify/require"
// )

// // ==========================
// // Test Helper Functions
// // ==========================

// func createTestConfig() *Config {
// 	return &Config{
// 		Timeout: 10 * time.Second,
// 	}
// }

// func createTestHandler(t *testing.T, db *sql.DB, redisClient *redis.Client, config *Config) *Handler {
// 	if config == nil {
// 		config = createTestConfig()
// 	}
// 	// Use the test logger from your logger package
// 	testLog := logger.NewTestLogger(t)
// 	return NewHandler(config, db, redisClient, testLog)
// }

// func createInput(userID, subscriptionTier string) *Input {
// 	return &Input{
// 		UserID:           userID,
// 		SubscriptionTier: subscriptionTier,
// 	}
// }

// func createSubscription(userID, tier string, isValid bool, expiresAt string) *Subscription {
// 	return &Subscription{
// 		UserID:    userID,
// 		Tier:      tier,
// 		ExpiresAt: expiresAt,
// 		IsValid:   isValid,
// 	}
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name           string
// 		input          *Input
// 		mockDBResult   *Subscription
// 		expectedOutput *Output
// 		validateOutput func(t *testing.T, output *Output)
// 	}{
// 		{
// 			name:  "valid premium subscription",
// 			input: createInput("user-123", "premium"),
// 			mockDBResult: createSubscription("user-123", "premium", true,
// 				time.Now().Add(24*time.Hour).Format(time.RFC3339)),
// 			expectedOutput: &Output{
// 				IsValid:   true,
// 				TierLevel: "premium",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Equal(t, "premium", output.TierLevel)
// 			},
// 		},
// 		{
// 			name:  "valid free subscription",
// 			input: createInput("user-456", "free"),
// 			mockDBResult: createSubscription("user-456", "free", true,
// 				time.Now().Add(24*time.Hour).Format(time.RFC3339)),
// 			expectedOutput: &Output{
// 				IsValid:   true,
// 				TierLevel: "free",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Equal(t, "free", output.TierLevel)
// 			},
// 		},
// 		{
// 			name:  "valid basic subscription",
// 			input: createInput("user-789", "basic"),
// 			mockDBResult: createSubscription("user-789", "basic", true,
// 				time.Now().Add(24*time.Hour).Format(time.RFC3339)),
// 			expectedOutput: &Output{
// 				IsValid:   true,
// 				TierLevel: "basic",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Equal(t, "basic", output.TierLevel)
// 			},
// 		},
// 		{
// 			name:  "valid enterprise subscription",
// 			input: createInput("user-999", "enterprise"),
// 			mockDBResult: createSubscription("user-999", "enterprise", true,
// 				time.Now().Add(24*time.Hour).Format(time.RFC3339)),
// 			expectedOutput: &Output{
// 				IsValid:   true,
// 				TierLevel: "enterprise",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Equal(t, "enterprise", output.TierLevel)
// 			},
// 		},
// 		{
// 			name:         "subscription without expiration",
// 			input:        createInput("user-nil-expiry", "premium"),
// 			mockDBResult: createSubscription("user-nil-expiry", "premium", true, ""),
// 			expectedOutput: &Output{
// 				IsValid:   true,
// 				TierLevel: "premium",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Equal(t, "premium", output.TierLevel)
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			// Setup mocks
// 			db, mock, err := sqlmock.New()
// 			require.NoError(t, err)
// 			defer db.Close()

// 			redisClient := redis.NewClient(&redis.Options{
// 				Addr: "localhost:6379", // We'll use a real client but test in isolation
// 			})
// 			defer redisClient.Close()

// 			// Clear any existing cache
// 			ctx := context.Background()
// 			redisClient.Del(ctx, "sub:"+tt.input.UserID)

// 			// Mock database query
// 			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 				AddRow(tt.mockDBResult.UserID, tt.mockDBResult.Tier,
// 					tt.mockDBResult.ExpiresAt, tt.mockDBResult.IsValid)
// 			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 				WithArgs(tt.input.UserID).
// 				WillReturnRows(rows)

// 			handler := createTestHandler(t, db, redisClient, nil)
// 			output, err := handler.Execute(ctx, tt.input) // Use public Execute method

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.Equal(t, tt.expectedOutput.IsValid, output.IsValid)
// 			assert.Equal(t, tt.expectedOutput.TierLevel, output.TierLevel)

// 			if tt.validateOutput != nil {
// 				tt.validateOutput(t, output)
// 			}

// 			// Verify cache was set
// 			cacheKey := "sub:" + tt.input.UserID
// 			cachedData, err := redisClient.Get(ctx, cacheKey).Result()
// 			assert.NoError(t, err)

// 			var cachedSub Subscription
// 			err = json.Unmarshal([]byte(cachedData), &cachedSub)
// 			assert.NoError(t, err)
// 			assert.Equal(t, tt.mockDBResult.UserID, cachedSub.UserID)
// 			assert.Equal(t, tt.mockDBResult.Tier, cachedSub.Tier)
// 			assert.Equal(t, tt.mockDBResult.IsValid, cachedSub.IsValid)

// 			// Verify all expectations were met
// 			assert.NoError(t, mock.ExpectationsWereMet())
// 		})
// 	}
// }

// func TestHandler_Execute_CacheHit(t *testing.T) {
// 	t.Run("cache hit returns cached subscription", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		require.NoError(t, err)
// 		defer db.Close()

// 		redisClient := redis.NewClient(&redis.Options{
// 			Addr: "localhost:6379",
// 		})
// 		defer redisClient.Close()

// 		ctx := context.Background()

// 		// Pre-populate cache
// 		cachedSub := createSubscription("cached-user", "premium", true,
// 			time.Now().Add(24*time.Hour).Format(time.RFC3339))
// 		cachedData, _ := json.Marshal(cachedSub)
// 		redisClient.Set(ctx, "sub:cached-user", cachedData, 5*time.Minute)

// 		handler := createTestHandler(t, db, redisClient, nil)
// 		input := createInput("cached-user", "premium")

// 		output, err := handler.Execute(ctx, input) // Use public Execute method

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		assert.True(t, output.IsValid)
// 		assert.Equal(t, "premium", output.TierLevel)

// 		// Verify database was not queried (cache hit)
// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})
// }

// func TestHandler_Execute_ValidationErrors(t *testing.T) {
// 	tests := []struct {
// 		name          string
// 		input         *Input
// 		mockDBError   error
// 		mockDBResult  *Subscription
// 		expectedError error
// 	}{
// 		{
// 			name:          "subscription not found",
// 			input:         createInput("non-existent-user", "premium"),
// 			mockDBError:   sql.ErrNoRows,
// 			expectedError: ErrSubscriptionInvalid,
// 		},
// 		{
// 			name:          "subscription marked invalid",
// 			input:         createInput("invalid-user", "premium"),
// 			mockDBResult:  createSubscription("invalid-user", "premium", false, ""),
// 			expectedError: ErrSubscriptionInvalid,
// 		},
// 		{
// 			name:  "expired subscription",
// 			input: createInput("expired-user", "premium"),
// 			mockDBResult: createSubscription("expired-user", "premium", true,
// 				time.Now().Add(-24*time.Hour).Format(time.RFC3339)),
// 			expectedError: ErrSubscriptionExpired,
// 		},
// 		{
// 			name:          "invalid tier level",
// 			input:         createInput("invalid-tier-user", "invalid-tier"),
// 			mockDBResult:  createSubscription("invalid-tier-user", "invalid-tier", true, ""),
// 			expectedError: ErrSubscriptionInvalid,
// 		},
// 		{
// 			name:          "database error",
// 			input:         createInput("db-error-user", "premium"),
// 			mockDBError:   errors.New("connection failed"),
// 			expectedError: ErrSubscriptionCheckFailed,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			require.NoError(t, err)
// 			defer db.Close()

// 			redisClient := redis.NewClient(&redis.Options{
// 				Addr: "localhost:6379",
// 			})
// 			defer redisClient.Close()

// 			ctx := context.Background()
// 			redisClient.Del(ctx, "sub:"+tt.input.UserID)

// 			query := mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 				WithArgs(tt.input.UserID)

// 			if tt.mockDBError != nil {
// 				query.WillReturnError(tt.mockDBError)
// 			} else {
// 				rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 					AddRow(tt.mockDBResult.UserID, tt.mockDBResult.Tier,
// 						tt.mockDBResult.ExpiresAt, tt.mockDBResult.IsValid)
// 				query.WillReturnRows(rows)
// 			}

// 			handler := createTestHandler(t, db, redisClient, nil)
// 			output, err := handler.Execute(ctx, tt.input) // Use public Execute method

// 			assert.Error(t, err)
// 			assert.True(t, errors.Is(err, tt.expectedError))
// 			assert.Nil(t, output)

// 			// Verify cache was not set for errors
// 			cacheKey := "sub:" + tt.input.UserID
// 			_, err = redisClient.Get(ctx, cacheKey).Result()
// 			assert.Error(t, err) // Should not be in cache

// 			assert.NoError(t, mock.ExpectationsWereMet())
// 		})
// 	}
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_ValidTiers(t *testing.T) {
// 	validTiers := []string{"free", "basic", "premium", "enterprise"}
// 	invalidTiers := []string{"", "invalid", "trial", "pro"}

// 	for _, tier := range validTiers {
// 		t.Run(fmt.Sprintf("valid tier: %s", tier), func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			require.NoError(t, err)
// 			defer db.Close()

// 			redisClient := redis.NewClient(&redis.Options{
// 				Addr: "localhost:6379",
// 			})
// 			defer redisClient.Close()

// 			ctx := context.Background()
// 			redisClient.Del(ctx, "sub:test-user")

// 			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 				AddRow("test-user", tier, time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
// 			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 				WithArgs("test-user").
// 				WillReturnRows(rows)

// 			handler := createTestHandler(t, db, redisClient, nil)
// 			input := createInput("test-user", tier)
// 			output, err := handler.Execute(ctx, input) // Use public Execute method

// 			assert.NoError(t, err)
// 			assert.True(t, output.IsValid)
// 			assert.Equal(t, tier, output.TierLevel)
// 		})
// 	}

// 	for _, tier := range invalidTiers {
// 		t.Run(fmt.Sprintf("invalid tier: %s", tier), func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			require.NoError(t, err)
// 			defer db.Close()

// 			redisClient := redis.NewClient(&redis.Options{
// 				Addr: "localhost:6379",
// 			})
// 			defer redisClient.Close()

// 			ctx := context.Background()
// 			redisClient.Del(ctx, "sub:test-user")

// 			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 				AddRow("test-user", tier, time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
// 			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 				WithArgs("test-user").
// 				WillReturnRows(rows)

// 			handler := createTestHandler(t, db, redisClient, nil)
// 			input := createInput("test-user", tier)
// 			output, err := handler.Execute(ctx, input) // Use public Execute method

// 			assert.Error(t, err)
// 			assert.True(t, errors.Is(err, ErrSubscriptionInvalid))
// 			assert.Nil(t, output)
// 		})
// 	}
// }

// func TestHandler_ExpirationLogic(t *testing.T) {
// 	tests := []struct {
// 		name        string
// 		expiresAt   string
// 		shouldError bool
// 	}{
// 		{
// 			name:        "future expiration",
// 			expiresAt:   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
// 			shouldError: false,
// 		},
// 		{
// 			name:        "past expiration",
// 			expiresAt:   time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
// 			shouldError: true,
// 		},
// 		{
// 			name:        "empty expiration",
// 			expiresAt:   "",
// 			shouldError: false,
// 		},
// 		{
// 			name:        "invalid date format",
// 			expiresAt:   "invalid-date",
// 			shouldError: false, // Should not error on parse failure, treat as no expiration
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			require.NoError(t, err)
// 			defer db.Close()

// 			redisClient := redis.NewClient(&redis.Options{
// 				Addr: "localhost:6379",
// 			})
// 			defer redisClient.Close()

// 			ctx := context.Background()
// 			redisClient.Del(ctx, "sub:test-user")

// 			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 				AddRow("test-user", "premium", tt.expiresAt, true)
// 			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 				WithArgs("test-user").
// 				WillReturnRows(rows)

// 			handler := createTestHandler(t, db, redisClient, nil)
// 			input := createInput("test-user", "premium")
// 			output, err := handler.Execute(ctx, input) // Use public Execute method

// 			if tt.shouldError {
// 				assert.Error(t, err)
// 				assert.True(t, errors.Is(err, ErrSubscriptionExpired))
// 				assert.Nil(t, output)
// 			} else {
// 				assert.NoError(t, err)
// 				assert.NotNil(t, output)
// 				assert.True(t, output.IsValid)
// 				assert.Equal(t, "premium", output.TierLevel)
// 			}
// 		})
// 	}
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	t.Run("empty user ID", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		require.NoError(t, err)
// 		defer db.Close()

// 		redisClient := redis.NewClient(&redis.Options{
// 			Addr: "localhost:6379",
// 		})
// 		defer redisClient.Close()

// 		ctx := context.Background()
// 		redisClient.Del(ctx, "sub:")

// 		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 			WithArgs("").
// 			WillReturnError(sql.ErrNoRows)

// 		handler := createTestHandler(t, db, redisClient, nil)
// 		input := createInput("", "premium")
// 		output, err := handler.Execute(ctx, input) // Use public Execute method

// 		assert.Error(t, err)
// 		assert.True(t, errors.Is(err, ErrSubscriptionInvalid))
// 		assert.Nil(t, output)
// 	})

// 	t.Run("context timeout", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		require.NoError(t, err)
// 		defer db.Close()

// 		redisClient := redis.NewClient(&redis.Options{
// 			Addr: "localhost:6379",
// 		})
// 		defer redisClient.Close()

// 		config := &Config{Timeout: 1 * time.Millisecond}
// 		handler := createTestHandler(t, db, redisClient, config)

// 		// Mock a slow database query
// 		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 			WithArgs("test-user").
// 			WillDelayFor(10 * time.Millisecond). // Longer than timeout
// 			WillReturnRows(sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 				AddRow("test-user", "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true))

// 		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
// 		defer cancel()

// 		input := createInput("test-user", "premium")
// 		output, err := handler.Execute(ctx, input) // Use public Execute method

// 		assert.Error(t, err)
// 		assert.Contains(t, err.Error(), "context") // Either deadline exceeded or canceled
// 		assert.Nil(t, output)
// 	})
// }

// func TestHandler_CacheConsistency(t *testing.T) {
// 	t.Run("cache reflects database state", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		require.NoError(t, err)
// 		defer db.Close()

// 		redisClient := redis.NewClient(&redis.Options{
// 			Addr: "localhost:6379",
// 		})
// 		defer redisClient.Close()

// 		ctx := context.Background()
// 		userID := "cache-test-user"
// 		redisClient.Del(ctx, "sub:"+userID)

// 		handler := createTestHandler(t, db, redisClient, nil)

// 		// First call - database hit
// 		rows1 := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 			AddRow(userID, "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
// 		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 			WithArgs(userID).
// 			WillReturnRows(rows1)

// 		input := createInput(userID, "premium")
// 		output1, err := handler.Execute(ctx, input) // Use public Execute method
// 		assert.NoError(t, err)
// 		assert.True(t, output1.IsValid)

// 		// Second call - cache hit
// 		output2, err := handler.Execute(ctx, input) // Use public Execute method
// 		assert.NoError(t, err)
// 		assert.True(t, output2.IsValid)
// 		assert.Equal(t, output1.TierLevel, output2.TierLevel)

// 		// Verify cache was used (only one DB query)
// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})

// 	t.Run("cache invalidated on different users", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		require.NoError(t, err)
// 		defer db.Close()

// 		redisClient := redis.NewClient(&redis.Options{
// 			Addr: "localhost:6379",
// 		})
// 		defer redisClient.Close()

// 		ctx := context.Background()
// 		user1 := "user-1"
// 		user2 := "user-2"
// 		redisClient.Del(ctx, "sub:"+user1, "sub:"+user2)

// 		handler := createTestHandler(t, db, redisClient, nil)

// 		// First user
// 		rows1 := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 			AddRow(user1, "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
// 		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 			WithArgs(user1).
// 			WillReturnRows(rows1)

// 		// Second user
// 		rows2 := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 			AddRow(user2, "free", time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
// 		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 			WithArgs(user2).
// 			WillReturnRows(rows2)

// 		output1, err := handler.Execute(ctx, createInput(user1, "premium")) // Use public Execute method
// 		assert.NoError(t, err)
// 		assert.True(t, output1.IsValid)

// 		output2, err := handler.Execute(ctx, createInput(user2, "free")) // Use public Execute method
// 		assert.NoError(t, err)
// 		assert.True(t, output2.IsValid)

// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	require.NoError(t, err)
// 	defer db.Close()

// 	redisClient := redis.NewClient(&redis.Options{
// 		Addr: "localhost:6379",
// 	})
// 	defer redisClient.Close()

// 	ctx := context.Background()
// 	handler := createTestHandler(t, db, redisClient, nil)

// 	tests := []struct {
// 		name        string
// 		userID      string
// 		tier        string
// 		isValid     bool
// 		expiresAt   string
// 		description string
// 	}{
// 		{
// 			name:        "Premium user with valid subscription",
// 			userID:      "premium-user-1",
// 			tier:        "premium",
// 			isValid:     true,
// 			expiresAt:   time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
// 			description: "Should validate premium subscription successfully",
// 		},
// 		{
// 			name:        "Free user with valid subscription",
// 			userID:      "free-user-1",
// 			tier:        "free",
// 			isValid:     true,
// 			expiresAt:   "",
// 			description: "Should validate free subscription without expiration",
// 		},
// 		{
// 			name:        "Enterprise user with valid subscription",
// 			userID:      "enterprise-user-1",
// 			tier:        "enterprise",
// 			isValid:     true,
// 			expiresAt:   time.Now().Add(365 * 24 * time.Hour).Format(time.RFC3339),
// 			description: "Should validate enterprise subscription successfully",
// 		},
// 		{
// 			name:        "User with expired subscription",
// 			userID:      "expired-user-1",
// 			tier:        "premium",
// 			isValid:     true,
// 			expiresAt:   time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
// 			description: "Should reject expired subscription",
// 		},
// 		{
// 			name:        "User with invalid subscription",
// 			userID:      "invalid-user-1",
// 			tier:        "premium",
// 			isValid:     false,
// 			expiresAt:   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
// 			description: "Should reject invalid subscription",
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			// Clear cache for this user
// 			redisClient.Del(ctx, "sub:"+tt.userID)

// 			// Mock database query
// 			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 				AddRow(tt.userID, tt.tier, tt.expiresAt, tt.isValid)
// 			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 				WithArgs(tt.userID).
// 				WillReturnRows(rows)

// 			input := createInput(tt.userID, tt.tier)
// 			output, err := handler.Execute(ctx, input) // Use public Execute method

// 			if tt.isValid && (tt.expiresAt == "" || isFutureDate(tt.expiresAt)) {
// 				// Should be valid
// 				assert.NoError(t, err, tt.description)
// 				assert.NotNil(t, output, tt.description)
// 				assert.True(t, output.IsValid, tt.description)
// 				assert.Equal(t, tt.tier, output.TierLevel, tt.description)

// 				// Verify cache was set
// 				cacheKey := "sub:" + tt.userID
// 				cachedData, err := redisClient.Get(ctx, cacheKey).Result()
// 				assert.NoError(t, err, tt.description)

// 				var cachedSub Subscription
// 				err = json.Unmarshal([]byte(cachedData), &cachedSub)
// 				assert.NoError(t, err, tt.description)
// 				assert.Equal(t, tt.userID, cachedSub.UserID, tt.description)
// 				assert.Equal(t, tt.tier, cachedSub.Tier, tt.description)
// 			} else {
// 				// Should be invalid
// 				assert.Error(t, err, tt.description)
// 				assert.Nil(t, output, tt.description)
// 			}
// 		})
// 	}

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	db, mock, err := sqlmock.New()
// 	require.NoError(b, err)
// 	defer db.Close()

// 	redisClient := redis.NewClient(&redis.Options{
// 		Addr: "localhost:6379",
// 	})
// 	defer redisClient.Close()

// 	ctx := context.Background()
// 	redisClient.Del(ctx, "sub:benchmark-user")

// 	// Use no-op logger for benchmarks to avoid I/O overhead
// 	noOpLogger := logger.NewNoOpLogger()
// 	handler := NewHandler(createTestConfig(), db, redisClient, noOpLogger)

// 	// Mock database response
// 	rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 		AddRow("benchmark-user", "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
// 	mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 		WithArgs("benchmark-user").
// 		WillReturnRows(rows)

// 	input := createInput("benchmark-user", "premium")

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.Execute(ctx, input) // Use public Execute method
// 		// Clear cache for each iteration to force DB query
// 		redisClient.Del(ctx, "sub:benchmark-user")
// 	}
// }

// func BenchmarkHandler_Execute_CacheHit(b *testing.B) {
// 	db, mock, err := sqlmock.New()
// 	require.NoError(b, err)
// 	defer db.Close()

// 	redisClient := redis.NewClient(&redis.Options{
// 		Addr: "localhost:6379",
// 	})
// 	defer redisClient.Close()

// 	ctx := context.Background()

// 	// Use no-op logger for benchmarks to avoid I/O overhead
// 	noOpLogger := logger.NewNoOpLogger()
// 	handler := NewHandler(createTestConfig(), db, redisClient, noOpLogger)

// 	// Pre-populate cache once
// 	cachedSub := createSubscription("cached-user", "premium", true,
// 		time.Now().Add(24*time.Hour).Format(time.RFC3339))
// 	cachedData, _ := json.Marshal(cachedSub)
// 	redisClient.Set(ctx, "sub:cached-user", cachedData, 5*time.Minute)

// 	input := createInput("cached-user", "premium")

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.Execute(ctx, input) // Use public Execute method
// 	}

// 	// No database queries should occur
// 	assert.NoError(b, mock.ExpectationsWereMet())
// }

// // ==========================
// // Helper Functions
// // ==========================

// func isFutureDate(dateStr string) bool {
// 	if dateStr == "" {
// 		return true
// 	}
// 	t, err := time.Parse(time.RFC3339, dateStr)
// 	if err != nil {
// 		return false
// 	}
// 	return time.Now().Before(t)
// }

// // internal/workers/infrastructure/validate-subscription/handler_test.go
// package validatesubscription

// import (
// 	"context"
// 	"database/sql"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"testing"
// 	"time"

// 	"github.com/DATA-DOG/go-sqlmock"
// 	"github.com/redis/go-redis/v9"
// 	"github.com/stretchr/testify/assert"
// 	"github.com/stretchr/testify/require"
// 	"go.uber.org/zap/zaptest"
// )

// // ==========================
// // Test Helper Functions
// // ==========================

// func createTestConfig() *Config {
// 	return &Config{
// 		Timeout: 10 * time.Second,
// 	}
// }

// func createTestHandler(t *testing.T, db *sql.DB, redisClient *redis.Client, config *Config) *Handler {
// 	if config == nil {
// 		config = createTestConfig()
// 	}
// 	return NewHandler(config, db, redisClient, zaptest.NewLogger(t))
// }

// func createInput(userID, subscriptionTier string) *Input {
// 	return &Input{
// 		UserID:           userID,
// 		SubscriptionTier: subscriptionTier,
// 	}
// }

// func createSubscription(userID, tier string, isValid bool, expiresAt string) *Subscription {
// 	return &Subscription{
// 		UserID:    userID,
// 		Tier:      tier,
// 		ExpiresAt: expiresAt,
// 		IsValid:   isValid,
// 	}
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name           string
// 		input          *Input
// 		mockDBResult   *Subscription
// 		expectedOutput *Output
// 		validateOutput func(t *testing.T, output *Output)
// 	}{
// 		{
// 			name:  "valid premium subscription",
// 			input: createInput("user-123", "premium"),
// 			mockDBResult: createSubscription("user-123", "premium", true,
// 				time.Now().Add(24*time.Hour).Format(time.RFC3339)),
// 			expectedOutput: &Output{
// 				IsValid:   true,
// 				TierLevel: "premium",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Equal(t, "premium", output.TierLevel)
// 			},
// 		},
// 		{
// 			name:  "valid free subscription",
// 			input: createInput("user-456", "free"),
// 			mockDBResult: createSubscription("user-456", "free", true,
// 				time.Now().Add(24*time.Hour).Format(time.RFC3339)),
// 			expectedOutput: &Output{
// 				IsValid:   true,
// 				TierLevel: "free",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Equal(t, "free", output.TierLevel)
// 			},
// 		},
// 		{
// 			name:  "valid basic subscription",
// 			input: createInput("user-789", "basic"),
// 			mockDBResult: createSubscription("user-789", "basic", true,
// 				time.Now().Add(24*time.Hour).Format(time.RFC3339)),
// 			expectedOutput: &Output{
// 				IsValid:   true,
// 				TierLevel: "basic",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Equal(t, "basic", output.TierLevel)
// 			},
// 		},
// 		{
// 			name:  "valid enterprise subscription",
// 			input: createInput("user-999", "enterprise"),
// 			mockDBResult: createSubscription("user-999", "enterprise", true,
// 				time.Now().Add(24*time.Hour).Format(time.RFC3339)),
// 			expectedOutput: &Output{
// 				IsValid:   true,
// 				TierLevel: "enterprise",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Equal(t, "enterprise", output.TierLevel)
// 			},
// 		},
// 		{
// 			name:         "subscription without expiration",
// 			input:        createInput("user-nil-expiry", "premium"),
// 			mockDBResult: createSubscription("user-nil-expiry", "premium", true, ""),
// 			expectedOutput: &Output{
// 				IsValid:   true,
// 				TierLevel: "premium",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.True(t, output.IsValid)
// 				assert.Equal(t, "premium", output.TierLevel)
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			// Setup mocks
// 			db, mock, err := sqlmock.New()
// 			require.NoError(t, err)
// 			defer db.Close()

// 			redisClient := redis.NewClient(&redis.Options{
// 				Addr: "localhost:6379", // We'll use a real client but test in isolation
// 			})
// 			defer redisClient.Close()

// 			// Clear any existing cache
// 			ctx := context.Background()
// 			redisClient.Del(ctx, "sub:"+tt.input.UserID)

// 			// Mock database query
// 			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 				AddRow(tt.mockDBResult.UserID, tt.mockDBResult.Tier,
// 					tt.mockDBResult.ExpiresAt, tt.mockDBResult.IsValid)
// 			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 				WithArgs(tt.input.UserID).
// 				WillReturnRows(rows)

// 			handler := createTestHandler(t, db, redisClient, nil)
// 			output, err := handler.execute(ctx, tt.input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.Equal(t, tt.expectedOutput.IsValid, output.IsValid)
// 			assert.Equal(t, tt.expectedOutput.TierLevel, output.TierLevel)

// 			if tt.validateOutput != nil {
// 				tt.validateOutput(t, output)
// 			}

// 			// Verify cache was set
// 			cacheKey := "sub:" + tt.input.UserID
// 			cachedData, err := redisClient.Get(ctx, cacheKey).Result()
// 			assert.NoError(t, err)

// 			var cachedSub Subscription
// 			err = json.Unmarshal([]byte(cachedData), &cachedSub)
// 			assert.NoError(t, err)
// 			assert.Equal(t, tt.mockDBResult.UserID, cachedSub.UserID)
// 			assert.Equal(t, tt.mockDBResult.Tier, cachedSub.Tier)
// 			assert.Equal(t, tt.mockDBResult.IsValid, cachedSub.IsValid)

// 			// Verify all expectations were met
// 			assert.NoError(t, mock.ExpectationsWereMet())
// 		})
// 	}
// }

// func TestHandler_Execute_CacheHit(t *testing.T) {
// 	t.Run("cache hit returns cached subscription", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		require.NoError(t, err)
// 		defer db.Close()

// 		redisClient := redis.NewClient(&redis.Options{
// 			Addr: "localhost:6379",
// 		})
// 		defer redisClient.Close()

// 		ctx := context.Background()

// 		// Pre-populate cache
// 		cachedSub := createSubscription("cached-user", "premium", true,
// 			time.Now().Add(24*time.Hour).Format(time.RFC3339))
// 		cachedData, _ := json.Marshal(cachedSub)
// 		redisClient.Set(ctx, "sub:cached-user", cachedData, 5*time.Minute)

// 		handler := createTestHandler(t, db, redisClient, nil)
// 		input := createInput("cached-user", "premium")

// 		output, err := handler.execute(ctx, input)

// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 		assert.True(t, output.IsValid)
// 		assert.Equal(t, "premium", output.TierLevel)

// 		// Verify database was not queried (cache hit)
// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})
// }

// func TestHandler_Execute_ValidationErrors(t *testing.T) {
// 	tests := []struct {
// 		name          string
// 		input         *Input
// 		mockDBError   error
// 		mockDBResult  *Subscription
// 		expectedError error
// 	}{
// 		{
// 			name:          "subscription not found",
// 			input:         createInput("non-existent-user", "premium"),
// 			mockDBError:   sql.ErrNoRows,
// 			expectedError: ErrSubscriptionInvalid,
// 		},
// 		{
// 			name:          "subscription marked invalid",
// 			input:         createInput("invalid-user", "premium"),
// 			mockDBResult:  createSubscription("invalid-user", "premium", false, ""),
// 			expectedError: ErrSubscriptionInvalid,
// 		},
// 		{
// 			name:  "expired subscription",
// 			input: createInput("expired-user", "premium"),
// 			mockDBResult: createSubscription("expired-user", "premium", true,
// 				time.Now().Add(-24*time.Hour).Format(time.RFC3339)),
// 			expectedError: ErrSubscriptionExpired,
// 		},
// 		{
// 			name:          "invalid tier level",
// 			input:         createInput("invalid-tier-user", "invalid-tier"),
// 			mockDBResult:  createSubscription("invalid-tier-user", "invalid-tier", true, ""),
// 			expectedError: ErrSubscriptionInvalid,
// 		},
// 		{
// 			name:          "database error",
// 			input:         createInput("db-error-user", "premium"),
// 			mockDBError:   errors.New("connection failed"),
// 			expectedError: ErrSubscriptionCheckFailed,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			require.NoError(t, err)
// 			defer db.Close()

// 			redisClient := redis.NewClient(&redis.Options{
// 				Addr: "localhost:6379",
// 			})
// 			defer redisClient.Close()

// 			ctx := context.Background()
// 			redisClient.Del(ctx, "sub:"+tt.input.UserID)

// 			query := mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 				WithArgs(tt.input.UserID)

// 			if tt.mockDBError != nil {
// 				query.WillReturnError(tt.mockDBError)
// 			} else {
// 				rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 					AddRow(tt.mockDBResult.UserID, tt.mockDBResult.Tier,
// 						tt.mockDBResult.ExpiresAt, tt.mockDBResult.IsValid)
// 				query.WillReturnRows(rows)
// 			}

// 			handler := createTestHandler(t, db, redisClient, nil)
// 			output, err := handler.execute(ctx, tt.input)

// 			assert.Error(t, err)
// 			assert.True(t, errors.Is(err, tt.expectedError))
// 			assert.Nil(t, output)

// 			// Verify cache was not set for errors
// 			cacheKey := "sub:" + tt.input.UserID
// 			_, err = redisClient.Get(ctx, cacheKey).Result()
// 			assert.Error(t, err) // Should not be in cache

// 			assert.NoError(t, mock.ExpectationsWereMet())
// 		})
// 	}
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_ValidTiers(t *testing.T) {
// 	validTiers := []string{"free", "basic", "premium", "enterprise"}
// 	invalidTiers := []string{"", "invalid", "trial", "pro"}

// 	for _, tier := range validTiers {
// 		t.Run(fmt.Sprintf("valid tier: %s", tier), func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			require.NoError(t, err)
// 			defer db.Close()

// 			redisClient := redis.NewClient(&redis.Options{
// 				Addr: "localhost:6379",
// 			})
// 			defer redisClient.Close()

// 			ctx := context.Background()
// 			redisClient.Del(ctx, "sub:test-user")

// 			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 				AddRow("test-user", tier, time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
// 			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 				WithArgs("test-user").
// 				WillReturnRows(rows)

// 			handler := createTestHandler(t, db, redisClient, nil)
// 			input := createInput("test-user", tier)
// 			output, err := handler.execute(ctx, input)

// 			assert.NoError(t, err)
// 			assert.True(t, output.IsValid)
// 			assert.Equal(t, tier, output.TierLevel)
// 		})
// 	}

// 	for _, tier := range invalidTiers {
// 		t.Run(fmt.Sprintf("invalid tier: %s", tier), func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			require.NoError(t, err)
// 			defer db.Close()

// 			redisClient := redis.NewClient(&redis.Options{
// 				Addr: "localhost:6379",
// 			})
// 			defer redisClient.Close()

// 			ctx := context.Background()
// 			redisClient.Del(ctx, "sub:test-user")

// 			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 				AddRow("test-user", tier, time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
// 			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 				WithArgs("test-user").
// 				WillReturnRows(rows)

// 			handler := createTestHandler(t, db, redisClient, nil)
// 			input := createInput("test-user", tier)
// 			output, err := handler.execute(ctx, input)

// 			assert.Error(t, err)
// 			assert.True(t, errors.Is(err, ErrSubscriptionInvalid))
// 			assert.Nil(t, output)
// 		})
// 	}
// }

// func TestHandler_ExpirationLogic(t *testing.T) {
// 	tests := []struct {
// 		name        string
// 		expiresAt   string
// 		shouldError bool
// 	}{
// 		{
// 			name:        "future expiration",
// 			expiresAt:   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
// 			shouldError: false,
// 		},
// 		{
// 			name:        "past expiration",
// 			expiresAt:   time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
// 			shouldError: true,
// 		},
// 		{
// 			name:        "empty expiration",
// 			expiresAt:   "",
// 			shouldError: false,
// 		},
// 		{
// 			name:        "invalid date format",
// 			expiresAt:   "invalid-date",
// 			shouldError: false, // Should not error on parse failure, treat as no expiration
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			db, mock, err := sqlmock.New()
// 			require.NoError(t, err)
// 			defer db.Close()

// 			redisClient := redis.NewClient(&redis.Options{
// 				Addr: "localhost:6379",
// 			})
// 			defer redisClient.Close()

// 			ctx := context.Background()
// 			redisClient.Del(ctx, "sub:test-user")

// 			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 				AddRow("test-user", "premium", tt.expiresAt, true)
// 			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 				WithArgs("test-user").
// 				WillReturnRows(rows)

// 			handler := createTestHandler(t, db, redisClient, nil)
// 			input := createInput("test-user", "premium")
// 			output, err := handler.execute(ctx, input)

// 			if tt.shouldError {
// 				assert.Error(t, err)
// 				assert.True(t, errors.Is(err, ErrSubscriptionExpired))
// 				assert.Nil(t, output)
// 			} else {
// 				assert.NoError(t, err)
// 				assert.NotNil(t, output)
// 				assert.True(t, output.IsValid)
// 				assert.Equal(t, "premium", output.TierLevel)
// 			}
// 		})
// 	}
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	t.Run("empty user ID", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		require.NoError(t, err)
// 		defer db.Close()

// 		redisClient := redis.NewClient(&redis.Options{
// 			Addr: "localhost:6379",
// 		})
// 		defer redisClient.Close()

// 		ctx := context.Background()
// 		redisClient.Del(ctx, "sub:")

// 		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 			WithArgs("").
// 			WillReturnError(sql.ErrNoRows)

// 		handler := createTestHandler(t, db, redisClient, nil)
// 		input := createInput("", "premium")
// 		output, err := handler.execute(ctx, input)

// 		assert.Error(t, err)
// 		assert.True(t, errors.Is(err, ErrSubscriptionInvalid))
// 		assert.Nil(t, output)
// 	})

// 	t.Run("context timeout", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		require.NoError(t, err)
// 		defer db.Close()

// 		redisClient := redis.NewClient(&redis.Options{
// 			Addr: "localhost:6379",
// 		})
// 		defer redisClient.Close()

// 		config := &Config{Timeout: 1 * time.Millisecond}
// 		handler := createTestHandler(t, db, redisClient, config)

// 		// Mock a slow database query
// 		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 			WithArgs("test-user").
// 			WillDelayFor(10 * time.Millisecond). // Longer than timeout
// 			WillReturnRows(sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 				AddRow("test-user", "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true))

// 		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
// 		defer cancel()

// 		input := createInput("test-user", "premium")
// 		output, err := handler.execute(ctx, input)

// 		assert.Error(t, err)
// 		assert.Contains(t, err.Error(), "context") // Either deadline exceeded or canceled
// 		assert.Nil(t, output)
// 	})
// }

// func TestHandler_CacheConsistency(t *testing.T) {
// 	t.Run("cache reflects database state", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		require.NoError(t, err)
// 		defer db.Close()

// 		redisClient := redis.NewClient(&redis.Options{
// 			Addr: "localhost:6379",
// 		})
// 		defer redisClient.Close()

// 		ctx := context.Background()
// 		userID := "cache-test-user"
// 		redisClient.Del(ctx, "sub:"+userID)

// 		handler := createTestHandler(t, db, redisClient, nil)

// 		// First call - database hit
// 		rows1 := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 			AddRow(userID, "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
// 		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 			WithArgs(userID).
// 			WillReturnRows(rows1)

// 		input := createInput(userID, "premium")
// 		output1, err := handler.execute(ctx, input)
// 		assert.NoError(t, err)
// 		assert.True(t, output1.IsValid)

// 		// Second call - cache hit
// 		output2, err := handler.execute(ctx, input)
// 		assert.NoError(t, err)
// 		assert.True(t, output2.IsValid)
// 		assert.Equal(t, output1.TierLevel, output2.TierLevel)

// 		// Verify cache was used (only one DB query)
// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})

// 	t.Run("cache invalidated on different users", func(t *testing.T) {
// 		db, mock, err := sqlmock.New()
// 		require.NoError(t, err)
// 		defer db.Close()

// 		redisClient := redis.NewClient(&redis.Options{
// 			Addr: "localhost:6379",
// 		})
// 		defer redisClient.Close()

// 		ctx := context.Background()
// 		user1 := "user-1"
// 		user2 := "user-2"
// 		redisClient.Del(ctx, "sub:"+user1, "sub:"+user2)

// 		handler := createTestHandler(t, db, redisClient, nil)

// 		// First user
// 		rows1 := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 			AddRow(user1, "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
// 		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 			WithArgs(user1).
// 			WillReturnRows(rows1)

// 		// Second user
// 		rows2 := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 			AddRow(user2, "free", time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
// 		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 			WithArgs(user2).
// 			WillReturnRows(rows2)

// 		output1, err := handler.execute(ctx, createInput(user1, "premium"))
// 		assert.NoError(t, err)
// 		assert.True(t, output1.IsValid)

// 		output2, err := handler.execute(ctx, createInput(user2, "free"))
// 		assert.NoError(t, err)
// 		assert.True(t, output2.IsValid)

// 		assert.NoError(t, mock.ExpectationsWereMet())
// 	})
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	require.NoError(t, err)
// 	defer db.Close()

// 	redisClient := redis.NewClient(&redis.Options{
// 		Addr: "localhost:6379",
// 	})
// 	defer redisClient.Close()

// 	ctx := context.Background()
// 	handler := createTestHandler(t, db, redisClient, nil)

// 	tests := []struct {
// 		name        string
// 		userID      string
// 		tier        string
// 		isValid     bool
// 		expiresAt   string
// 		description string
// 	}{
// 		{
// 			name:        "Premium user with valid subscription",
// 			userID:      "premium-user-1",
// 			tier:        "premium",
// 			isValid:     true,
// 			expiresAt:   time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
// 			description: "Should validate premium subscription successfully",
// 		},
// 		{
// 			name:        "Free user with valid subscription",
// 			userID:      "free-user-1",
// 			tier:        "free",
// 			isValid:     true,
// 			expiresAt:   "",
// 			description: "Should validate free subscription without expiration",
// 		},
// 		{
// 			name:        "Enterprise user with valid subscription",
// 			userID:      "enterprise-user-1",
// 			tier:        "enterprise",
// 			isValid:     true,
// 			expiresAt:   time.Now().Add(365 * 24 * time.Hour).Format(time.RFC3339),
// 			description: "Should validate enterprise subscription successfully",
// 		},
// 		{
// 			name:        "User with expired subscription",
// 			userID:      "expired-user-1",
// 			tier:        "premium",
// 			isValid:     true,
// 			expiresAt:   time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
// 			description: "Should reject expired subscription",
// 		},
// 		{
// 			name:        "User with invalid subscription",
// 			userID:      "invalid-user-1",
// 			tier:        "premium",
// 			isValid:     false,
// 			expiresAt:   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
// 			description: "Should reject invalid subscription",
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			// Clear cache for this user
// 			redisClient.Del(ctx, "sub:"+tt.userID)

// 			// Mock database query
// 			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 				AddRow(tt.userID, tt.tier, tt.expiresAt, tt.isValid)
// 			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 				WithArgs(tt.userID).
// 				WillReturnRows(rows)

// 			input := createInput(tt.userID, tt.tier)
// 			output, err := handler.execute(ctx, input)

// 			if tt.isValid && (tt.expiresAt == "" || isFutureDate(tt.expiresAt)) {
// 				// Should be valid
// 				assert.NoError(t, err, tt.description)
// 				assert.NotNil(t, output, tt.description)
// 				assert.True(t, output.IsValid, tt.description)
// 				assert.Equal(t, tt.tier, output.TierLevel, tt.description)

// 				// Verify cache was set
// 				cacheKey := "sub:" + tt.userID
// 				cachedData, err := redisClient.Get(ctx, cacheKey).Result()
// 				assert.NoError(t, err, tt.description)

// 				var cachedSub Subscription
// 				err = json.Unmarshal([]byte(cachedData), &cachedSub)
// 				assert.NoError(t, err, tt.description)
// 				assert.Equal(t, tt.userID, cachedSub.UserID, tt.description)
// 				assert.Equal(t, tt.tier, cachedSub.Tier, tt.description)
// 			} else {
// 				// Should be invalid
// 				assert.Error(t, err, tt.description)
// 				assert.Nil(t, output, tt.description)
// 			}
// 		})
// 	}

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	db, mock, err := sqlmock.New()
// 	require.NoError(b, err)
// 	defer db.Close()

// 	redisClient := redis.NewClient(&redis.Options{
// 		Addr: "localhost:6379",
// 	})
// 	defer redisClient.Close()

// 	ctx := context.Background()
// 	redisClient.Del(ctx, "sub:benchmark-user")

// 	handler := NewHandler(createTestConfig(), db, redisClient, zaptest.NewLogger(b))

// 	// Mock database response
// 	rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
// 		AddRow("benchmark-user", "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true)
// 	mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
// 		WithArgs("benchmark-user").
// 		WillReturnRows(rows)

// 	input := createInput("benchmark-user", "premium")

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(ctx, input)
// 		// Clear cache for each iteration to force DB query
// 		redisClient.Del(ctx, "sub:benchmark-user")
// 	}
// }

// func BenchmarkHandler_Execute_CacheHit(b *testing.B) {
// 	db, mock, err := sqlmock.New()
// 	require.NoError(b, err)
// 	defer db.Close()

// 	redisClient := redis.NewClient(&redis.Options{
// 		Addr: "localhost:6379",
// 	})
// 	defer redisClient.Close()

// 	ctx := context.Background()

// 	handler := NewHandler(createTestConfig(), db, redisClient, zaptest.NewLogger(b))

// 	// Pre-populate cache once
// 	cachedSub := createSubscription("cached-user", "premium", true,
// 		time.Now().Add(24*time.Hour).Format(time.RFC3339))
// 	cachedData, _ := json.Marshal(cachedSub)
// 	redisClient.Set(ctx, "sub:cached-user", cachedData, 5*time.Minute)

// 	input := createInput("cached-user", "premium")

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(ctx, input)
// 	}

// 	// No database queries should occur
// 	assert.NoError(b, mock.ExpectationsWereMet())
// }

// // ==========================
// // Helper Functions
// // ==========================

// func isFutureDate(dateStr string) bool {
// 	if dateStr == "" {
// 		return true
// 	}
// 	t, err := time.Parse(time.RFC3339, dateStr)
// 	if err != nil {
// 		return false
// 	}
// 	return time.Now().Before(t)
// }
