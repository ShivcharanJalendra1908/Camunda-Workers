package calculatematchscore

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"camunda-workers/internal/common/logger"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// ==========================
// Valid UUIDs for testing
// FIX: handler.validateInput checks UUID format (36 chars) for non-empty IDs.
// All franchise/user IDs must be valid UUIDs.
// ==========================
const (
	// Franchise UUIDs
	franchiseID1 = "61986cba-415d-44d9-ad09-803db5de6012"
	franchiseID2 = "0c6aeff2-15dc-465e-82bd-9c3105df76f7"
	franchiseID3 = "22e868f6-51bb-4710-834e-d2a49b1328d7"
	franchiseID4 = "fbdd4c71-fa20-48ba-8424-c1d21e90bedc"
	franchiseID5 = "e49d4fa2-2a44-4ccb-88be-37e288b57fb7"

	// User UUIDs
	userID1 = "36efc855-a9c6-4485-811c-c791235e3717"
	userID2 = "d46b80c2-beaf-40b1-bf9c-3a14da527f15"
	userID3 = "69db6030-cdc0-4a81-8669-0d4c8a1ebd33"
	userID4 = "dc6ba026-be30-4cac-9ea6-33457315801c"
	userID5 = "aecd89f7-5436-4490-863d-f21e92be2838"
)

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{
		CacheTTL: 10 * time.Minute,
	}
}

func setupMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	return db, mock
}

func setupMockRedis() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
}

// FIX: franchise ID must be a valid UUID (36 chars)
func createTestFranchiseData() FranchiseData {
	return FranchiseData{
		ID:            franchiseID1,
		Name:          "Test Franchise",
		InvestmentMin: 100000,
		InvestmentMax: 300000,
		Category:      "Food & Beverage",
		Locations:     []string{"Texas", "California"},
	}
}

func createTestUserProfile() *UserProfile {
	return &UserProfile{
		CapitalAvailable: 200000,
		LocationPrefs:    []string{"Texas", "New York"},
		Interests:        []string{"Food & Beverage", "Retail"},
		ExperienceYears:  5,
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

func TestHandler_Execute_WithProvidedProfile(t *testing.T) {
	tests := []struct {
		name           string
		profile        *UserProfile
		franchise      FranchiseData
		expectedScore  int
		validateOutput func(t *testing.T, output *Output)
	}{
		{
			name: "perfect match",
			profile: &UserProfile{
				CapitalAvailable: 150000,
				LocationPrefs:    []string{"Texas"},
				Interests:        []string{"Food & Beverage"},
				ExperienceYears:  5,
			},
			franchise: FranchiseData{
				ID:            franchiseID1,
				InvestmentMin: 100000,
				InvestmentMax: 200000,
				Category:      "Food & Beverage",
				Locations:     []string{"Texas"},
			},
			expectedScore: 100,
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, 100, output.MatchFactors.FinancialFit)
				assert.Equal(t, 100, output.MatchFactors.ExperienceFit)
				assert.Equal(t, 100, output.MatchFactors.LocationFit)
				assert.Equal(t, 100, output.MatchFactors.InterestFit)
			},
		},
		{
			name: "good financial fit, moderate experience",
			profile: &UserProfile{
				CapitalAvailable: 250000,
				LocationPrefs:    []string{"California"},
				Interests:        []string{"Food & Beverage"},
				ExperienceYears:  3,
			},
			franchise: FranchiseData{
				// FIX: use valid UUID
				ID:            franchiseID2,
				InvestmentMin: 100000,
				InvestmentMax: 200000,
				Category:      "Food & Beverage",
				Locations:     []string{"California"},
			},
			expectedScore: 89, // 80*0.30 + 80*0.25 + 100*0.20 + 100*0.25 = 24+20+20+25 = 89
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, 80, output.MatchFactors.FinancialFit)
				assert.Equal(t, 80, output.MatchFactors.ExperienceFit)
				assert.Equal(t, 100, output.MatchFactors.LocationFit)
				assert.Equal(t, 100, output.MatchFactors.InterestFit)
			},
		},
		{
			name: "poor financial fit",
			profile: &UserProfile{
				CapitalAvailable: 50000,
				LocationPrefs:    []string{"Texas"},
				Interests:        []string{"Food & Beverage"},
				ExperienceYears:  5,
			},
			franchise: FranchiseData{
				ID:            franchiseID3,
				InvestmentMin: 200000,
				InvestmentMax: 400000,
				Category:      "Food & Beverage",
				Locations:     []string{"Texas"},
			},
			expectedScore: 76, // 20*0.30 + 100*0.25 + 100*0.20 + 100*0.25 = 6+25+20+25 = 76
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, 20, output.MatchFactors.FinancialFit)
				assert.Equal(t, 100, output.MatchFactors.ExperienceFit)
			},
		},
		{
			name: "no experience match",
			profile: &UserProfile{
				CapitalAvailable: 150000,
				LocationPrefs:    []string{"Texas"},
				Interests:        []string{"Food & Beverage"},
				ExperienceYears:  0,
			},
			franchise: FranchiseData{
				ID:            franchiseID4,
				InvestmentMin: 100000,
				InvestmentMax: 200000,
				Category:      "Food & Beverage",
				Locations:     []string{"Texas"},
			},
			expectedScore: 82, // 100*0.30 + 30*0.25 + 100*0.20 + 100*0.25 = 30+7.5+20+25 = 82.5 → 82
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, 30, output.MatchFactors.ExperienceFit)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, _ := setupMockDB(t)
			defer db.Close()

			handler := NewHandler(createTestConfig(), db, setupMockRedis(), newTestLogger(t))

			input := &Input{
				// FIX: use valid UUID for userID
				UserID:        userID1,
				FranchiseData: tt.franchise,
				UserProfile:   tt.profile,
			}

			output, err := handler.Execute(context.Background(), input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedScore, output.MatchScore)
			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}
		})
	}
}

func TestHandler_Execute_FetchUserProfile(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	handler := NewHandler(createTestConfig(), db, setupMockRedis(), newTestLogger(t))

	locPrefs, _ := json.Marshal([]string{"Texas"})
	interests, _ := json.Marshal([]string{"Food & Beverage"})

	mock.ExpectQuery("SELECT capital_available").
		WithArgs(userID2).
		WillReturnRows(sqlmock.NewRows([]string{"capital_available", "location_preferences", "interests", "industry_experience"}).
			AddRow(150000, locPrefs, interests, 5))

	input := &Input{
		// FIX: use valid UUID
		UserID:        userID2,
		FranchiseData: createTestFranchiseData(),
		UserProfile:   nil,
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Greater(t, output.MatchScore, 0)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_NoUserProfile(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()

	handler := NewHandler(createTestConfig(), db, setupMockRedis(), newTestLogger(t))

	input := &Input{
		UserID: "", // empty userID skips UUID validation
		// FIX: franchise ID must be valid UUID
		FranchiseData: createTestFranchiseData(),
		UserProfile:   nil,
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	// No profile → default score of 50
	assert.Equal(t, 50, output.MatchScore)
	assert.Equal(t, 50, output.MatchFactors.FinancialFit)
	assert.Equal(t, 50, output.MatchFactors.ExperienceFit)
	assert.Equal(t, 50, output.MatchFactors.LocationFit)
	assert.Equal(t, 50, output.MatchFactors.InterestFit)
}

// ==========================
// Matching Algorithm Tests
// ==========================

func TestHandler_CalculateFinancialFit(t *testing.T) {
	tests := []struct {
		name          string
		capital       int
		minInvest     int
		maxInvest     int
		expectedScore int
	}{
		{"within range", 150000, 100000, 200000, 100},
		{"above max", 250000, 100000, 200000, 80},
		{"80% of min", 80000, 100000, 200000, 60},
		{"50% of min", 50000, 100000, 200000, 40},
		{"below 50%", 30000, 100000, 200000, 20},
		{"zero capital", 0, 100000, 200000, 50},
	}

	handler := NewHandler(createTestConfig(), nil, nil, newTestLogger(t))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := handler.calculateFinancialFit(tt.capital, tt.minInvest, tt.maxInvest)
			assert.Equal(t, tt.expectedScore, score)
		})
	}
}

func TestHandler_CalculateExperienceFit(t *testing.T) {
	tests := []struct {
		years         int
		expectedScore int
	}{
		{5, 100},
		{10, 100},
		{3, 80},
		{4, 80},
		{1, 60},
		{2, 60},
		{0, 30},
	}

	handler := NewHandler(createTestConfig(), nil, nil, newTestLogger(t))

	for _, tt := range tests {
		t.Run(string(rune('0'+tt.years))+" years", func(t *testing.T) {
			score := handler.calculateExperienceFit(tt.years)
			assert.Equal(t, tt.expectedScore, score)
		})
	}
}

func TestHandler_CalculateLocationFit(t *testing.T) {
	tests := []struct {
		name          string
		userLocs      []string
		franchiseLocs []string
		expectedScore int
	}{
		{"exact match", []string{"Texas"}, []string{"Texas"}, 100},
		{"match in list", []string{"Texas", "California"}, []string{"California"}, 100},
		{"no match", []string{"Texas"}, []string{"New York"}, 30},
		{"empty user prefs", []string{}, []string{"Texas"}, 50},
		{"empty franchise locs", []string{"Texas"}, []string{}, 50},
		{"both empty", []string{}, []string{}, 50},
	}

	handler := NewHandler(createTestConfig(), nil, nil, newTestLogger(t))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := handler.calculateLocationFit(tt.userLocs, tt.franchiseLocs)
			assert.Equal(t, tt.expectedScore, score)
		})
	}
}

func TestHandler_CalculateInterestFit(t *testing.T) {
	tests := []struct {
		name          string
		interests     []string
		category      string
		expectedScore int
	}{
		{"exact match", []string{"Food & Beverage"}, "Food & Beverage", 100},
		{"match in list", []string{"Retail", "Food & Beverage"}, "Food & Beverage", 100},
		{"no match", []string{"Retail"}, "Food & Beverage", 40},
		{"empty interests", []string{}, "Food & Beverage", 50},
	}

	handler := NewHandler(createTestConfig(), nil, nil, newTestLogger(t))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := handler.calculateInterestFit(tt.interests, tt.category)
			assert.Equal(t, tt.expectedScore, score)
		})
	}
}

// ==========================
// Database & Cache Tests
// ==========================

func TestHandler_GetUserProfile_FromCache(t *testing.T) {
	locPrefs, _ := json.Marshal([]string{"Texas"})
	interests, _ := json.Marshal([]string{"Food & Beverage"})

	rows := sqlmock.NewRows([]string{"capital_available", "location_preferences", "interests", "industry_experience"}).
		AddRow(150000, locPrefs, interests, 5)

	ctx := context.Background()
	db, mock := setupMockDB(t)
	defer db.Close()

	mock.ExpectQuery("SELECT capital_available").
		WithArgs(userID3).
		WillReturnRows(rows)

	handler := NewHandler(createTestConfig(), db, setupMockRedis(), newTestLogger(t))
	// FIX: use valid UUID
	profile, err := handler.getUserProfile(ctx, userID3)

	assert.NoError(t, err)
	assert.NotNil(t, profile)
	assert.Equal(t, 150000, profile.CapitalAvailable)
	assert.Equal(t, []string{"Texas"}, profile.LocationPrefs)
	assert.Equal(t, []string{"Food & Beverage"}, profile.Interests)
	assert.Equal(t, 5, profile.ExperienceYears)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_GetUserProfile_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	// FIX: use valid UUID
	mock.ExpectQuery("SELECT capital_available").
		WithArgs(userID4).
		WillReturnError(sql.ErrNoRows)

	handler := NewHandler(createTestConfig(), db, setupMockRedis(), newTestLogger(t))
	profile, err := handler.getUserProfile(context.Background(), userID4)

	assert.Error(t, err)
	assert.Nil(t, profile)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()
	handler := NewHandler(createTestConfig(), db, setupMockRedis(), newTestLogger(t))

	t.Run("zero investment range", func(t *testing.T) {
		profile := &UserProfile{CapitalAvailable: 100000}
		input := &Input{
			UserProfile: profile,
			FranchiseData: FranchiseData{
				ID:            franchiseID1,
				InvestmentMin: 0,
				InvestmentMax: 0,
			},
		}
		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.NotNil(t, output)
	})

	// FIX: handler.validateUserProfile rejects ExperienceYears < 0 → returns error.
	// The original test expected NoError + ExperienceFit=30, but validateInput is called
	// first and returns an error for negative ExperienceYears. Test updated accordingly.
	t.Run("negative experience years returns validation error", func(t *testing.T) {
		profile := &UserProfile{ExperienceYears: -1}
		input := &Input{
			UserProfile:   profile,
			FranchiseData: createTestFranchiseData(),
		}
		output, err := handler.Execute(context.Background(), input)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "experienceYears")
		assert.Nil(t, output)
	})

	t.Run("very large capital", func(t *testing.T) {
		profile := &UserProfile{CapitalAvailable: 10000000}
		input := &Input{
			UserProfile: profile,
			FranchiseData: FranchiseData{
				ID:            franchiseID2,
				InvestmentMin: 100000,
				InvestmentMax: 200000,
			},
		}
		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.Equal(t, 80, output.MatchFactors.FinancialFit) // Above max = 80
	})

	t.Run("empty category", func(t *testing.T) {
		profile := &UserProfile{Interests: []string{"Food"}}
		input := &Input{
			UserProfile: profile,
			FranchiseData: FranchiseData{
				ID:       franchiseID3,
				Category: "",
			},
		}
		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.Equal(t, 40, output.MatchFactors.InterestFit) // No match
	})

	t.Run("special characters in locations", func(t *testing.T) {
		profile := &UserProfile{LocationPrefs: []string{"São Paulo", "Zürich"}}
		input := &Input{
			UserProfile: profile,
			FranchiseData: FranchiseData{
				ID:        franchiseID4,
				Locations: []string{"São Paulo"},
			},
		}
		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.Equal(t, 100, output.MatchFactors.LocationFit)
	})
}

// ==========================
// Weighted Score Calculation Test
// ==========================

func TestHandler_WeightedScoreCalculation(t *testing.T) {
	db, _ := setupMockDB(t)
	defer db.Close()
	handler := NewHandler(createTestConfig(), db, setupMockRedis(), newTestLogger(t))

	// Known factor scores
	profile := &UserProfile{
		CapitalAvailable: 150000, // 100 fit (within range)
		ExperienceYears:  3,      // 80 fit
		LocationPrefs:    []string{"Texas"},
		Interests:        []string{"Food & Beverage"},
	}

	franchise := FranchiseData{
		// FIX: valid UUID
		ID:            franchiseID1,
		InvestmentMin: 100000,
		InvestmentMax: 200000,
		Category:      "Food & Beverage",
		Locations:     []string{"Texas"},
	}

	input := &Input{
		UserProfile:   profile,
		FranchiseData: franchise,
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)

	// Weights: Financial(30%) + Experience(25%) + Location(20%) + Interest(25%)
	// 100*0.30 + 80*0.25 + 100*0.20 + 100*0.25 = 30 + 20 + 20 + 25 = 95
	expectedScore := 95
	assert.Equal(t, expectedScore, output.MatchScore)
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	locPrefs, _ := json.Marshal([]string{"California", "Texas"})
	interests, _ := json.Marshal([]string{"Food & Beverage", "Fitness"})

	// FIX: use valid UUID
	mock.ExpectQuery("SELECT capital_available").
		WithArgs(userID5).
		WillReturnRows(sqlmock.NewRows([]string{"capital_available", "location_preferences", "interests", "industry_experience"}).
			AddRow(250000, locPrefs, interests, 7))

	handler := NewHandler(createTestConfig(), db, setupMockRedis(), newTestLogger(t))

	input := &Input{
		// FIX: valid UUIDs
		UserID: userID5,
		FranchiseData: FranchiseData{
			ID:            franchiseID5,
			Name:          "Fitness Franchise",
			InvestmentMin: 150000,
			InvestmentMax: 300000,
			Category:      "Fitness",
			Locations:     []string{"California", "Nevada"},
		},
		UserProfile: nil, // Will fetch from DB
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Greater(t, output.MatchScore, 0)
	assert.Greater(t, output.MatchFactors.FinancialFit, 0)
	assert.Greater(t, output.MatchFactors.ExperienceFit, 0)
	assert.Greater(t, output.MatchFactors.LocationFit, 0)
	assert.Greater(t, output.MatchFactors.InterestFit, 0)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	db, _, err := sqlmock.New()
	if err != nil {
		b.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	handler := NewHandler(createTestConfig(), db, setupMockRedis(), newTestLogger(&testing.T{}))
	input := &Input{
		UserProfile:   createTestUserProfile(),
		FranchiseData: createTestFranchiseData(), // FIX: now uses valid UUID
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_CalculateAllFactors(b *testing.B) {
	handler := NewHandler(createTestConfig(), nil, nil, newTestLogger(&testing.T{}))
	profile := createTestUserProfile()
	franchise := createTestFranchiseData()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.calculateFinancialFit(profile.CapitalAvailable, franchise.InvestmentMin, franchise.InvestmentMax)
		handler.calculateExperienceFit(profile.ExperienceYears)
		handler.calculateLocationFit(profile.LocationPrefs, franchise.Locations)
		handler.calculateInterestFit(profile.Interests, franchise.Category)
	}
}
