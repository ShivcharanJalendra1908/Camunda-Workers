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

//	func createSubscription(userID, tier string, isValid bool, expiresAt string) *Subscription {
//		return &Subscription{
//			UserID:    userID,
//			Tier:      tier,
//			ExpiresAt: expiresAt,
//			IsValid:   isValid,
//		}
//	}
//
// createSubscription returns a Subscription with proper sql.NullString for ExpiresAt
func createSubscription(userID, tier string, isValid bool, expiresAt string) *Subscription {
	expiresAtNull := sql.NullString{}
	if expiresAt != "" {
		expiresAtNull = sql.NullString{
			String: expiresAt,
			Valid:  true,
		}
	}

	return &Subscription{
		UserID:    userID,
		Tier:      tier,
		ExpiresAt: expiresAtNull,
		IsValid:   isValid,
	}
}

// cacheKey returns the exact key format used by the handler:
// "sub:" + sanitizedUserID + ":" + sanitizedTier
func cacheKey(userID, tier string) string {
	return "sub:" + userID + ":" + tier
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
			input: createInput("550e8400-e29b-41d4-a716-446655440000", "premium"),
			mockDBResult: createSubscription("550e8400-e29b-41d4-a716-446655440000", "premium", true,
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
			input: createInput("550e8400-e29b-41d4-a716-446655440001", "free"),
			mockDBResult: createSubscription("550e8400-e29b-41d4-a716-446655440001", "free", true,
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
			input: createInput("550e8400-e29b-41d4-a716-446655440002", "basic"),
			mockDBResult: createSubscription("550e8400-e29b-41d4-a716-446655440002", "basic", true,
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
			input: createInput("550e8400-e29b-41d4-a716-446655440003", "enterprise"),
			mockDBResult: createSubscription("550e8400-e29b-41d4-a716-446655440003", "enterprise", true,
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
			input:        createInput("550e8400-e29b-41d4-a716-446655440004", "premium"),
			mockDBResult: createSubscription("550e8400-e29b-41d4-a716-446655440004", "premium", true, ""),
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
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			redisClient, redisMock := redismock.NewClientMock()

			ctx := context.Background()

			// FIX: cache key is "sub:userID:tier" not "sub:userID"
			key := cacheKey(tt.input.UserID, tt.input.SubscriptionTier)
			redisMock.ExpectGet(key).RedisNil()

			// Handler queries with both userID AND tier first
			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
				AddRow(tt.mockDBResult.UserID, tt.mockDBResult.Tier,
					tt.mockDBResult.ExpiresAt, tt.mockDBResult.IsValid)
			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
				WithArgs(tt.input.UserID, tt.input.SubscriptionTier).
				WillReturnRows(rows)

			// Mock Redis SET
			cachedData, _ := json.Marshal(tt.mockDBResult)
			redisMock.ExpectSet(key, cachedData, 5*time.Minute).SetVal("OK")

			handler := createTestHandler(t, db, redisClient, nil)
			output, err := handler.Execute(ctx, tt.input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedOutput.IsValid, output.IsValid)
			assert.Equal(t, tt.expectedOutput.TierLevel, output.TierLevel)

			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}

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

		userID := "550e8400-e29b-41d4-a716-446655440010"
		tier := "premium"

		cachedSub := createSubscription(userID, tier, true,
			time.Now().Add(24*time.Hour).Format(time.RFC3339))
		cachedData, _ := json.Marshal(cachedSub)

		// FIX: cache key is "sub:userID:tier"
		key := cacheKey(userID, tier)
		redisMock.ExpectGet(key).SetVal(string(cachedData))

		handler := createTestHandler(t, db, redisClient, nil)
		input := createInput(userID, tier)

		output, err := handler.Execute(ctx, input)

		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.True(t, output.IsValid)
		assert.Equal(t, tier, output.TierLevel)

		// DB should NOT be queried on cache hit
		assert.NoError(t, mock.ExpectationsWereMet())
		assert.NoError(t, redisMock.ExpectationsWereMet())
	})
}

func TestHandler_Execute_ValidationErrors(t *testing.T) {
	// These inputs fail validateInput (invalid UUID format), so they never reach DB/Redis.
	// Use valid UUIDs for the DB-level error cases.
	tests := []struct {
		name          string
		input         *Input
		setupMocks    func(mock sqlmock.Sqlmock, redisMock redismock.ClientMock)
		expectedError error
	}{
		{
			name:  "subscription not found",
			input: createInput("550e8400-e29b-41d4-a716-446655440020", "premium"),
			setupMocks: func(mock sqlmock.Sqlmock, redisMock redismock.ClientMock) {
				key := cacheKey("550e8400-e29b-41d4-a716-446655440020", "premium")
				redisMock.ExpectGet(key).RedisNil()
				// Primary query returns no rows
				mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
					WithArgs("550e8400-e29b-41d4-a716-446655440020", "premium").
					WillReturnError(sql.ErrNoRows)
				// Fallback query also returns no rows
				mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1`).
					WithArgs("550e8400-e29b-41d4-a716-446655440020").
					WillReturnError(sql.ErrNoRows)
			},
			expectedError: ErrSubscriptionInvalid,
		},
		{
			name:  "subscription marked invalid",
			input: createInput("550e8400-e29b-41d4-a716-446655440021", "premium"),
			setupMocks: func(mock sqlmock.Sqlmock, redisMock redismock.ClientMock) {
				key := cacheKey("550e8400-e29b-41d4-a716-446655440021", "premium")
				redisMock.ExpectGet(key).RedisNil()
				rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
					AddRow("550e8400-e29b-41d4-a716-446655440021", "premium", "", false)
				mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
					WithArgs("550e8400-e29b-41d4-a716-446655440021", "premium").
					WillReturnRows(rows)
			},
			expectedError: ErrSubscriptionInvalid,
		},
		{
			name:  "expired subscription",
			input: createInput("550e8400-e29b-41d4-a716-446655440022", "premium"),
			setupMocks: func(mock sqlmock.Sqlmock, redisMock redismock.ClientMock) {
				key := cacheKey("550e8400-e29b-41d4-a716-446655440022", "premium")
				redisMock.ExpectGet(key).RedisNil()
				rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
					AddRow("550e8400-e29b-41d4-a716-446655440022", "premium",
						time.Now().Add(-24*time.Hour).Format(time.RFC3339), true)
				mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
					WithArgs("550e8400-e29b-41d4-a716-446655440022", "premium").
					WillReturnRows(rows)
			},
			expectedError: ErrSubscriptionExpired,
		},
		{
			name:  "database connection error",
			input: createInput("550e8400-e29b-41d4-a716-446655440023", "premium"),
			setupMocks: func(mock sqlmock.Sqlmock, redisMock redismock.ClientMock) {
				key := cacheKey("550e8400-e29b-41d4-a716-446655440023", "premium")
				redisMock.ExpectGet(key).RedisNil()
				mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
					WithArgs("550e8400-e29b-41d4-a716-446655440023", "premium").
					WillReturnError(errors.New("connection failed"))
			},
			expectedError: ErrSubscriptionCheckFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			redisClient, redisMock := redismock.NewClientMock()
			tt.setupMocks(mock, redisMock)

			handler := createTestHandler(t, db, redisClient, nil)
			output, err := handler.Execute(context.Background(), tt.input)

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
	// Valid UUID to pass validateInput
	validUserID := "550e8400-e29b-41d4-a716-446655440030"

	validTiers := []string{"free", "basic", "premium", "enterprise"}
	// Invalid tiers fail validateInput before DB/Redis — no mocks needed
	invalidTiers := []string{"invalid", "trial", "pro"}

	for _, tier := range validTiers {
		t.Run(fmt.Sprintf("valid tier: %s", tier), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			redisClient, redisMock := redismock.NewClientMock()
			ctx := context.Background()

			key := cacheKey(validUserID, tier)
			redisMock.ExpectGet(key).RedisNil()

			expiresAt := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
				AddRow(validUserID, tier, expiresAt, true)
			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
				WithArgs(validUserID, tier).
				WillReturnRows(rows)

			sub := createSubscription(validUserID, tier, true, expiresAt)
			cachedData, _ := json.Marshal(sub)
			redisMock.ExpectSet(key, cachedData, 5*time.Minute).SetVal("OK")

			handler := createTestHandler(t, db, redisClient, nil)
			input := createInput(validUserID, tier)
			output, err := handler.Execute(ctx, input)

			assert.NoError(t, err)
			assert.True(t, output.IsValid)
			assert.Equal(t, tier, output.TierLevel)
		})
	}

	for _, tier := range invalidTiers {
		t.Run(fmt.Sprintf("invalid tier: %s", tier), func(t *testing.T) {
			db, _, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			redisClient, _ := redismock.NewClientMock()

			handler := createTestHandler(t, db, redisClient, nil)
			// Invalid tier fails validateInput — no DB/Redis interaction expected
			input := createInput(validUserID, tier)
			output, err := handler.Execute(context.Background(), input)

			assert.Error(t, err)
			assert.Nil(t, output)
		})
	}

	// Empty tier also fails validateInput
	t.Run("invalid tier: empty", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()
		redisClient, _ := redismock.NewClientMock()
		handler := createTestHandler(t, db, redisClient, nil)
		output, err := handler.Execute(context.Background(), createInput(validUserID, ""))
		assert.Error(t, err)
		assert.Nil(t, output)
	})
}

func TestHandler_ExpirationLogic(t *testing.T) {
	validUserID := "550e8400-e29b-41d4-a716-446655440040"

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
			name:        "invalid date format - treated as no expiration",
			expiresAt:   "invalid-date",
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			redisClient, redisMock := redismock.NewClientMock()
			ctx := context.Background()

			key := cacheKey(validUserID, "premium")
			redisMock.ExpectGet(key).RedisNil()

			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
				AddRow(validUserID, "premium", tt.expiresAt, true)
			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
				WithArgs(validUserID, "premium").
				WillReturnRows(rows)

			if !tt.shouldError {
				sub := createSubscription(validUserID, "premium", true, tt.expiresAt)
				cachedData, _ := json.Marshal(sub)
				redisMock.ExpectSet(key, cachedData, 5*time.Minute).SetVal("OK")
			}

			handler := createTestHandler(t, db, redisClient, nil)
			input := createInput(validUserID, "premium")
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
// Validation Tests
// ==========================

func TestHandler_ValidateInput(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	redisClient, _ := redismock.NewClientMock()
	handler := createTestHandler(t, db, redisClient, nil)

	tests := []struct {
		name          string
		input         *Input
		expectedError string
	}{
		{
			name:          "empty user ID",
			input:         createInput("", "premium"),
			expectedError: "userId",
		},
		{
			name:          "invalid UUID format",
			input:         createInput("not-a-uuid", "premium"),
			expectedError: "userId",
		},
		{
			name:          "empty subscription tier",
			input:         createInput("550e8400-e29b-41d4-a716-446655440000", ""),
			expectedError: "subscriptionTier",
		},
		{
			name:  "valid input",
			input: createInput("550e8400-e29b-41d4-a716-446655440000", "premium"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateInput(tt.input)
			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	t.Run("context timeout", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		redisClient, redisMock := redismock.NewClientMock()

		validUserID := "550e8400-e29b-41d4-a716-446655440050"
		config := &Config{Timeout: 1 * time.Millisecond}
		handler := createTestHandler(t, db, redisClient, config)

		key := cacheKey(validUserID, "premium")
		redisMock.ExpectGet(key).RedisNil()

		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
			WithArgs(validUserID, "premium").
			WillDelayFor(10 * time.Millisecond).
			WillReturnRows(sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
				AddRow(validUserID, "premium", time.Now().Add(24*time.Hour).Format(time.RFC3339), true))

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()

		input := createInput(validUserID, "premium")
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

		userID := "550e8400-e29b-41d4-a716-446655440060"
		tier := "premium"
		handler := createTestHandler(t, db, redisClient, nil)

		expiresAt := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
		sub := createSubscription(userID, tier, true, expiresAt)
		cachedData, _ := json.Marshal(sub)
		key := cacheKey(userID, tier)

		// First call — cache miss, DB hit
		redisMock.ExpectGet(key).RedisNil()
		rows1 := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
			AddRow(userID, tier, expiresAt, true)
		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
			WithArgs(userID, tier).
			WillReturnRows(rows1)
		redisMock.ExpectSet(key, cachedData, 5*time.Minute).SetVal("OK")

		input := createInput(userID, tier)
		output1, err := handler.Execute(ctx, input)
		assert.NoError(t, err)
		assert.True(t, output1.IsValid)

		// Second call — cache hit, no DB
		redisMock.ExpectGet(key).SetVal(string(cachedData))

		output2, err := handler.Execute(ctx, input)
		assert.NoError(t, err)
		assert.True(t, output2.IsValid)
		assert.Equal(t, output1.TierLevel, output2.TierLevel)

		assert.NoError(t, mock.ExpectationsWereMet())
		assert.NoError(t, redisMock.ExpectationsWereMet())
	})

	t.Run("different users have separate cache keys", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		redisClient, redisMock := redismock.NewClientMock()
		ctx := context.Background()

		user1 := "550e8400-e29b-41d4-a716-446655440061"
		user2 := "550e8400-e29b-41d4-a716-446655440062"
		handler := createTestHandler(t, db, redisClient, nil)

		// User 1 - premium
		key1 := cacheKey(user1, "premium")
		exp1 := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
		redisMock.ExpectGet(key1).RedisNil()
		rows1 := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
			AddRow(user1, "premium", exp1, true)
		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
			WithArgs(user1, "premium").
			WillReturnRows(rows1)
		sub1 := createSubscription(user1, "premium", true, exp1)
		data1, _ := json.Marshal(sub1)
		redisMock.ExpectSet(key1, data1, 5*time.Minute).SetVal("OK")

		// User 2 - free
		key2 := cacheKey(user2, "free")
		exp2 := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
		redisMock.ExpectGet(key2).RedisNil()
		rows2 := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
			AddRow(user2, "free", exp2, true)
		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
			WithArgs(user2, "free").
			WillReturnRows(rows2)
		sub2 := createSubscription(user2, "free", true, exp2)
		data2, _ := json.Marshal(sub2)
		redisMock.ExpectSet(key2, data2, 5*time.Minute).SetVal("OK")

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
			userID:      "550e8400-e29b-41d4-a716-446655440070",
			tier:        "premium",
			isValid:     true,
			expiresAt:   time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
			description: "Should validate premium subscription successfully",
		},
		{
			name:        "Free user with valid subscription",
			userID:      "550e8400-e29b-41d4-a716-446655440071",
			tier:        "free",
			isValid:     true,
			expiresAt:   "",
			description: "Should validate free subscription without expiration",
		},
		{
			name:        "Enterprise user with valid subscription",
			userID:      "550e8400-e29b-41d4-a716-446655440072",
			tier:        "enterprise",
			isValid:     true,
			expiresAt:   time.Now().Add(365 * 24 * time.Hour).Format(time.RFC3339),
			description: "Should validate enterprise subscription successfully",
		},
		{
			name:        "User with expired subscription",
			userID:      "550e8400-e29b-41d4-a716-446655440073",
			tier:        "premium",
			isValid:     true,
			expiresAt:   time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
			description: "Should reject expired subscription",
		},
		{
			name:        "User with invalid subscription",
			userID:      "550e8400-e29b-41d4-a716-446655440074",
			tier:        "premium",
			isValid:     false,
			expiresAt:   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
			description: "Should reject invalid subscription",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := cacheKey(tt.userID, tt.tier)
			redisMock.ExpectGet(key).RedisNil()

			rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
				AddRow(tt.userID, tt.tier, tt.expiresAt, tt.isValid)
			mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
				WithArgs(tt.userID, tt.tier).
				WillReturnRows(rows)

			if tt.isValid && (tt.expiresAt == "" || isFutureDate(tt.expiresAt)) {
				sub := createSubscription(tt.userID, tt.tier, tt.isValid, tt.expiresAt)
				cachedData, _ := json.Marshal(sub)
				redisMock.ExpectSet(key, cachedData, 5*time.Minute).SetVal("OK")
			}

			input := createInput(tt.userID, tt.tier)
			output, err := handler.Execute(ctx, input)

			if tt.isValid && (tt.expiresAt == "" || isFutureDate(tt.expiresAt)) {
				assert.NoError(t, err, tt.description)
				assert.NotNil(t, output, tt.description)
				assert.True(t, output.IsValid, tt.description)
				assert.Equal(t, tt.tier, output.TierLevel, tt.description)
			} else {
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

	userID := "550e8400-e29b-41d4-a716-446655440080"
	tier := "premium"
	key := cacheKey(userID, tier)
	expiresAt := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
	sub := createSubscription(userID, tier, true, expiresAt)
	cachedData, _ := json.Marshal(sub)

	for i := 0; i < b.N; i++ {
		redisMock.ExpectGet(key).RedisNil()
		rows := sqlmock.NewRows([]string{"user_id", "tier", "expires_at", "is_valid"}).
			AddRow(userID, tier, expiresAt, true)
		mock.ExpectQuery(`SELECT user_id, tier, expires_at, is_valid FROM user_subscriptions WHERE user_id = \$1 AND tier = \$2`).
			WithArgs(userID, tier).
			WillReturnRows(rows)
		redisMock.ExpectSet(key, cachedData, 5*time.Minute).SetVal("OK")
	}

	input := createInput(userID, tier)
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

	userID := "550e8400-e29b-41d4-a716-446655440081"
	tier := "premium"
	key := cacheKey(userID, tier)

	cachedSub := createSubscription(userID, tier, true,
		time.Now().Add(24*time.Hour).Format(time.RFC3339))
	cachedData, _ := json.Marshal(cachedSub)

	for i := 0; i < b.N; i++ {
		redisMock.ExpectGet(key).SetVal(string(cachedData))
	}

	input := createInput(userID, tier)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(ctx, input)
	}

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
