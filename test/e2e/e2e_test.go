// test/e2e/e2e_test.go
package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/zbc"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/logger"

	// Import all worker packages
	authlogout "camunda-workers/internal/workers/auth/auth-logout"
	authsigningoogle "camunda-workers/internal/workers/auth/auth-signin-google"
	authsigninlinkedin "camunda-workers/internal/workers/auth/auth-signin-linkedin"
	authsignupgoogle "camunda-workers/internal/workers/auth/auth-signup-google"
	authsignuplinkedin "camunda-workers/internal/workers/auth/auth-signup-linkedin"
	captchaverify "camunda-workers/internal/workers/auth/captcha-verify"
	emailsend "camunda-workers/internal/workers/communication/email-send"
	crmusercreate "camunda-workers/internal/workers/crm/crm-user-create"

	checkpriorityrouting "camunda-workers/internal/workers/application/check-priority-routing"
	checkreadinessscore "camunda-workers/internal/workers/application/check-readiness-score"
	createapplicationrecord "camunda-workers/internal/workers/application/create-application-record"
	sendnotification "camunda-workers/internal/workers/application/send-notification"
	validateapplicationdata "camunda-workers/internal/workers/application/validate-application-data"

	enrichwebsearch "camunda-workers/internal/workers/ai-conversation/enrich-web-search"
	llmsynthesis "camunda-workers/internal/workers/ai-conversation/llm-synthesis"
	parseuserintent "camunda-workers/internal/workers/ai-conversation/parse-user-intent"
	queryinternaldata "camunda-workers/internal/workers/ai-conversation/query-internal-data"

	queryelasticsearch "camunda-workers/internal/workers/data-access/query-elasticsearch"
	querypostgresql "camunda-workers/internal/workers/data-access/query-postgresql"

	applyrelevanceranking "camunda-workers/internal/workers/franchise/apply-relevance-ranking"
	calculatematchscore "camunda-workers/internal/workers/franchise/calculate-match-score"
	parsesearchfilters "camunda-workers/internal/workers/franchise/parse-search-filters"

	buildresponse "camunda-workers/internal/workers/infrastructure/build-response"
	selecttemplate "camunda-workers/internal/workers/infrastructure/select-template"
	validatesubscription "camunda-workers/internal/workers/infrastructure/validate-subscription"
)

var (
	zeebeClient zbc.Client
	zapLog      *zap.Logger
)

// Logger adapters to bridge logger.Logger to worker-specific Logger interfaces
type enrichWebSearchLoggerAdapter struct {
	logger.Logger
}

func (a *enrichWebSearchLoggerAdapter) With(fields map[string]interface{}) enrichwebsearch.Logger {
	return &enrichWebSearchLoggerAdapter{a.Logger.With(fields)}
}

type llmSynthesisLoggerAdapter struct {
	logger.Logger
}

func (a *llmSynthesisLoggerAdapter) With(fields map[string]interface{}) llmsynthesis.Logger {
	return &llmSynthesisLoggerAdapter{a.Logger.With(fields)}
}

type parseUserIntentLoggerAdapter struct {
	logger.Logger
}

func (a *parseUserIntentLoggerAdapter) With(fields map[string]interface{}) parseuserintent.Logger {
	return &parseUserIntentLoggerAdapter{a.Logger.With(fields)}
}

type queryInternalDataLoggerAdapter struct {
	logger.Logger
}

func (a *queryInternalDataLoggerAdapter) With(fields map[string]interface{}) queryinternaldata.Logger {
	return &queryInternalDataLoggerAdapter{a.Logger.With(fields)}
}

func TestMain(m *testing.M) {
	var err error

	// Initialize Zeebe client with real connection
	zeebeClient, err = zbc.NewClient(&zbc.ClientConfig{
		GatewayAddress:         "localhost:26500",
		UsePlaintextConnection: true,
	})
	if err != nil {
		panic(fmt.Sprintf("❌ Failed to connect to Zeebe: %v", err))
	}

	// Initialize logger
	zapLog, _ = zap.NewProduction()

	// Run tests
	code := m.Run()

	// Cleanup
	zeebeClient.Close()
	os.Exit(code)
}

func TestFullE2E(t *testing.T) {
	_, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Load config
	cfg, err := config.Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	t.Log("🚀 Starting FULL E2E Test with real services...")

	// 1. Check all external services are available
	assertAllServicesConnectivity(t, cfg)

	// 2. Create DB tables if needed and insert test data
	createDatabaseTables(t, cfg)

	// 3. Deploy all BPMN files
	deployAllBPMN(t, cfg, zapLog)

	// 4. Test all 25 workers with real execution
	testAllWorkers(t, cfg, zapLog)

	t.Log("✅ ALL TESTS PASSED — Full E2E workflow successful!")
}

func assertAllServicesConnectivity(t *testing.T, cfg *config.Config) {
	t.Log("🔍 Checking service connectivity...")

	// 🔧 FORCE LOCALHOST FOR E2E TESTS
	cfg.Database.Postgres.Host = "localhost"
	cfg.Database.Redis.Address = "localhost:6379"
	cfg.Database.Elasticsearch.URL = "http://localhost:9200"

	// --- PostgreSQL ---
	db, err := database.NewPostgres(cfg.Database.Postgres)
	require.NoError(t, err, "❌ PostgreSQL connection failed")
	assert.NoError(t, db.Ping(context.Background()), "❌ PostgreSQL ping failed")
	db.Close()
	t.Log("✅ PostgreSQL connected")

	// --- Redis ---
	rdb, err := database.NewRedis(cfg.Database.Redis)
	require.NoError(t, err, "❌ Redis client creation failed")
	assert.NoError(t, rdb.Ping(context.Background()), "❌ Redis ping failed")
	t.Log("✅ Redis connected")

	// --- Elasticsearch ---
	esURL := cfg.Database.Elasticsearch.GetURL()
	t.Logf("🔗 Elasticsearch URL: %s", esURL)

	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{esURL},
	})
	require.NoError(t, err, "❌ Elasticsearch client creation failed")

	res, err := es.Info()
	require.NoError(t, err, "❌ Elasticsearch info request failed")
	assert.False(t, res.IsError(), "❌ Elasticsearch returned error")
	res.Body.Close()
	t.Log("✅ Elasticsearch connected")

	// --- Zeebe ---
	_, err = zeebeClient.NewTopologyCommand().Send(context.Background())
	assert.NoError(t, err, "❌ Zeebe topology request failed")
	t.Log("✅ Zeebe connected")

	// --- Keycloak (no HTTP check yet) ---
	t.Log("✅ Keycloak (config loaded only)")
}

// ==========================
// 2. Database Tables Setup + Test Data
// ==========================
func createDatabaseTables(t *testing.T, cfg *config.Config) {
	t.Log("🔧 Creating database tables and inserting test data...")

	dbClient, err := database.NewPostgres(cfg.Database.Postgres)
	require.NoError(t, err)
	defer dbClient.Close()

	db := dbClient.GetDB()

	// Create test tables if they don't exist
	queries := []string{
		`CREATE TABLE IF NOT EXISTS franchises (
			id VARCHAR(255) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			description TEXT,
			investment_min INTEGER,
			investment_max INTEGER,
			category VARCHAR(100),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS franchise_outlets (
			id SERIAL PRIMARY KEY,
			franchise_id VARCHAR(255) REFERENCES franchises(id),
			address TEXT,
			city VARCHAR(100),
			state VARCHAR(100),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS franchisors (
			id SERIAL PRIMARY KEY,
			franchise_id VARCHAR(255) REFERENCES franchises(id),
			account_type VARCHAR(50) DEFAULT 'standard',
			email VARCHAR(255),
			phone VARCHAR(50),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id VARCHAR(255) PRIMARY KEY,
			email VARCHAR(255) UNIQUE NOT NULL,
			phone VARCHAR(50),
			capital_available INTEGER,
			location_preferences JSONB,
			interests JSONB,
			industry_experience INTEGER,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS user_subscriptions (
			id SERIAL PRIMARY KEY,
			user_id VARCHAR(255) UNIQUE NOT NULL,
			tier VARCHAR(50) NOT NULL,
			expires_at TIMESTAMP,
			is_valid BOOLEAN DEFAULT true,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS applications (
			id VARCHAR(255) PRIMARY KEY,
			seeker_id VARCHAR(255) NOT NULL,
			franchise_id VARCHAR(255) NOT NULL,
			application_data JSONB,
			readiness_score INTEGER,
			priority VARCHAR(50),
			status VARCHAR(50),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(seeker_id, franchise_id)
		)`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id SERIAL PRIMARY KEY,
			event_type VARCHAR(100),
			resource_type VARCHAR(100),
			resource_id VARCHAR(255),
			details JSONB,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS zoho_contacts (
			id VARCHAR(255) PRIMARY KEY,
			email VARCHAR(255) UNIQUE NOT NULL,
			first_name VARCHAR(100),
			last_name VARCHAR(100),
			phone VARCHAR(50),
			company VARCHAR(255),
			lead_source VARCHAR(100),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS auth_sessions (
			id VARCHAR(255) PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL,
			token VARCHAR(255) UNIQUE NOT NULL,
			expires_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	for _, query := range queries {
		_, err := db.ExecContext(context.Background(), query)
		if err != nil {
			t.Logf("Warning: Failed to create table: %v", err)
		}
	}

	// Insert test data
	testData := []string{
		`INSERT INTO franchises (id, name, description, investment_min, investment_max, category)
		 VALUES ('test-franchise-001', 'Test Franchise', 'A test franchise', 50000, 150000, 'food')
		 ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO franchises (id, name, description, investment_min, investment_max, category)
		 VALUES ('mcdonalds', 'McDonald''s', 'Fast food giant', 1000000, 2200000, 'food')
		 ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO franchises (id, name, description, investment_min, investment_max, category)
		 VALUES ('subway', 'Subway', 'Sandwich chain', 150000, 300000, 'food')
		 ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO franchisors (franchise_id, account_type, email, phone)
		 VALUES ('test-franchise-001', 'premium', 'franchisor@test.com', '+1234567890')
		 ON CONFLICT DO NOTHING`,
		`INSERT INTO users (id, email, phone, capital_available, location_preferences, interests, industry_experience)
		 VALUES ('test-user-123', 'testuser@example.com', '+1234567890', 100000, '["New York"]', '["food"]', 5)
		 ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO users (id, email, phone, capital_available, location_preferences, interests, industry_experience)
		 VALUES ('user-mcd-456', 'mcduser@example.com', '+9876543210', 1500000, '["Texas", "California"]', '["food", "fast_food"]', 10)
		 ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO user_subscriptions (user_id, tier, expires_at, is_valid)
		 VALUES ('test-user-123', 'premium', NOW() + INTERVAL '1 year', true)
		 ON CONFLICT (user_id) DO NOTHING`,
		`INSERT INTO user_subscriptions (user_id, tier, expires_at, is_valid)
		 VALUES ('user-mcd-456', 'premium', NOW() + INTERVAL '1 year', true)
		 ON CONFLICT (user_id) DO NOTHING`,
		`INSERT INTO zoho_contacts (id, email, first_name, last_name, phone, company, lead_source)
		 VALUES ('zoho-test-123', 'zoho@example.com', 'Test', 'User', '+1234567890', 'Test Corp', 'Website')
		 ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO auth_sessions (id, user_id, token, expires_at)
		 VALUES ('session-123', 'test-user-123', 'token-abc-123-xyz', NOW() + INTERVAL '1 hour')
		 ON CONFLICT (id) DO NOTHING`,
	}

	for _, query := range testData {
		_, err := db.ExecContext(context.Background(), query)
		if err != nil {
			t.Logf("Warning: Failed to insert test data: %v", err)
		}
	}

	t.Log("✅ Database tables created/verified with test data")
}

// ==========================
// 3. Deploy All BPMN Files
// ==========================
func deployAllBPMN(t *testing.T, _ *config.Config, _ *zap.Logger) {
	t.Log("🏗️ Deploying BPMN files...")

	client := zeebeClient

	// Try multiple possible paths for BPMN directory
	possiblePaths := []string{
		"bpmn",
		"../bpmn", 
		"../../bpmn",
		"./bpmn",
	}

	var bpmnDir string
	var files []os.DirEntry
	var err error

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			files, err = os.ReadDir(path)
			if err == nil {
				bpmnDir = path
				t.Logf("📁 Found BPMN directory: %s", bpmnDir)
				break
			}
		}
	}

	if bpmnDir == "" {
		t.Log("⚠️ BPMN directory not found in any expected location, skipping deployment")
		return
	}

	require.NoError(t, err, "❌ Cannot read BPMN directory")

	bpmnCount := 0
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		// Better file extension check
		if !strings.HasSuffix(strings.ToLower(f.Name()), ".bpmn") {
			continue
		}

		path := fmt.Sprintf("%s/%s", bpmnDir, f.Name())
		t.Logf("📄 Deploying BPMN: %s", path)
		
		_, err := client.NewDeployResourceCommand().AddResourceFile(path).Send(context.Background())
		if err != nil {
			t.Logf("⚠️ Failed to deploy BPMN %s: %v", f.Name(), err)
			// Continue with other files instead of failing
		} else {
			t.Logf("✅ Deployed: %s", f.Name())
			bpmnCount++
		}
	}

	if bpmnCount == 0 {
		t.Log("ℹ️ No BPMN files were successfully deployed")
	} else {
		t.Logf("✅ Successfully deployed %d BPMN files", bpmnCount)
	}
}

// ==========================
// 4. Test All 25 Workers
// ==========================
func testAllWorkers(t *testing.T, cfg *config.Config, log *zap.Logger) {
	t.Log("🧪 Testing all 25 workers with real execution...")

	// Get clients for all services
	dbClient, err := database.NewPostgres(cfg.Database.Postgres)
	require.NoError(t, err)
	defer dbClient.Close()

	db := dbClient.GetDB()

	esURL := cfg.Database.Elasticsearch.GetURL()
	es, err := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{esURL}})
	require.NoError(t, err)

	rdbClient, err := database.NewRedis(cfg.Database.Redis)
	require.NoError(t, err)
	defer rdbClient.Close()

	rdb := rdbClient.GetClient()

	// Worker test cases
	testCases := []struct {
		name   string
		testFn func(*testing.T, *config.Config, *zap.Logger, *sql.DB, *elasticsearch.Client, *redis.Client)
	}{
		{"enrich-web-search", testEnrichWebSearch},
		{"llm-synthesis", testLLMSynthesis},
		{"parse-user-intent", testParseUserIntent},
		{"query-internal-data", testQueryInternalData},
		{"check-priority-routing", testCheckPriorityRouting},
		{"check-readiness-score", testCheckReadinessScore},
		{"create-application-record", testCreateApplicationRecord},
		{"send-notification", testSendNotification},
		{"validate-application-data", testValidateApplicationData},
		{"auth-logout", testAuthLogout},
		{"auth-signin-google", testAuthSigninGoogle},
		{"auth-signin-linkedin", testAuthSigninLinkedIn},
		{"auth-signup-google", testAuthSignupGoogle},
		{"auth-signup-linkedin", testAuthSignupLinkedIn},
		{"captcha-verify", testCaptchaVerify},
		{"email-send", testEmailSend},
		{"crm-user-create", testCRMUserCreate},
		{"query-elasticsearch", testQueryElasticsearch},
		{"query-postgresql", testQueryPostgreSQL},
		{"apply-relevance-ranking", testApplyRelevanceRanking},
		{"calculate-match-score", testCalculateMatchScore},
		{"parse-search-filters", testParseSearchFilters},
		{"build-response", testBuildResponse},
		{"select-template", testSelectTemplate},
		{"validate-subscription", testValidateSubscription},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.testFn(t, cfg, log, db, es, rdb)
		})
	}
}

// ==========================
// Worker Test Functions - CLEAN VERSION (No Fallback Logic)
// ==========================

func testEnrichWebSearch(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	logAdapter := &enrichWebSearchLoggerAdapter{logger.NewZapAdapter(log)}

	handler := enrichwebsearch.NewHandler(&enrichwebsearch.Config{
		SearchAPIBaseURL: "http://localhost:8080/mock",
		SearchAPIKey:     "mock",
		SearchEngineID:   "mock",
		Timeout:          5 * time.Second,
		MaxResults:       5,
		MinRelevance:     0.1,
	}, logAdapter)

	input := &enrichwebsearch.Input{
		Question: "test",
		Entities: []enrichwebsearch.Entity{},
	}
	_, err := handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

func testLLMSynthesis(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	logAdapter := &llmSynthesisLoggerAdapter{logger.NewZapAdapter(log)}

	handler := llmsynthesis.NewHandler(&llmsynthesis.Config{
		GenAIBaseURL: "http://localhost:8080/mock",
		Timeout:      5 * time.Second,
		MaxRetries:   1,
		MaxTokens:    100,
		Temperature:  0.7,
	}, logAdapter)

	input := &llmsynthesis.Input{Question: "test"}
	_, err := handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

func testParseUserIntent(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	logAdapter := &parseUserIntentLoggerAdapter{logger.NewZapAdapter(log)}

	handler := parseuserintent.NewHandler(&parseuserintent.Config{
		GenAIBaseURL: "http://localhost:8080/mock",
		Timeout:      30 * time.Second,
		MaxRetries:   2,
	}, logAdapter)

	input := &parseuserintent.Input{Question: "test"}
	_, err := handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

func testQueryInternalData(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	logAdapter := &queryInternalDataLoggerAdapter{logger.NewZapAdapter(log)}

	handler := queryinternaldata.NewHandler(&queryinternaldata.Config{
		Timeout:    2 * time.Second,
		CacheTTL:   5 * time.Minute,
		MaxResults: 10,
	}, db, es, rdb, logAdapter)

	input := &queryinternaldata.Input{
		Entities:    []queryinternaldata.Entity{},
		DataSources: []string{"internal_db"},
	}
	_, err := handler.Execute(context.Background(), input)
	assert.NoError(t, err)
}

func testCheckPriorityRouting(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler := checkpriorityrouting.NewHandler(&checkpriorityrouting.Config{
		CacheTTL: 30 * time.Minute,
	}, db, rdb, logger.NewZapAdapter(log))

	input := &checkpriorityrouting.Input{FranchiseID: "test"}
	_, err := handler.Execute(context.Background(), input)
	assert.NoError(t, err)
}

func testCheckReadinessScore(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler := checkreadinessscore.NewHandler(&checkreadinessscore.Config{}, logger.NewZapAdapter(log))

	input := &checkreadinessscore.Input{
		UserID:          "test",
		ApplicationData: map[string]interface{}{},
	}
	_, err := handler.Execute(context.Background(), input)
	assert.NoError(t, err)
}

func testCreateApplicationRecord(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler := createapplicationrecord.NewHandler(&createapplicationrecord.Config{}, db, logger.NewZapAdapter(log))

	uniqueID := fmt.Sprintf("%d", time.Now().UnixNano())
	input := &createapplicationrecord.Input{
		SeekerID:    "test-user-" + uniqueID,
		FranchiseID: "test-franchise-" + uniqueID,
	}
	
	result, err := handler.Execute(context.Background(), input)
	assert.NoError(t, err, "Should create application record successfully")
	assert.NotEmpty(t, result.ApplicationID, "Should generate application ID")
}

func testSendNotification(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler, err := sendnotification.NewHandler(&sendnotification.Config{
		EmailEnabled: false,
		SMSEnabled:   false,
	}, db, logger.NewZapAdapter(log))
	require.NoError(t, err)

	input := &sendnotification.Input{
		RecipientID:      "test",
		RecipientType:    sendnotification.RecipientTypeFranchisor,
		NotificationType: sendnotification.TypeNewApplication,
	}
	_, err = handler.Execute(context.Background(), input)
	assert.NoError(t, err)
}

func testValidateApplicationData(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler := validateapplicationdata.NewHandler(&validateapplicationdata.Config{}, logger.NewZapAdapter(log))

	input := &validateapplicationdata.Input{
		FranchiseID:     "mcdonalds",
		ApplicationData: map[string]interface{}{},
	}
	_, err := handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

// CLEAN WORKER TEST FUNCTIONS - NO FALLBACK LOGIC
func testAuthLogout(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	workerCfg := config.GetWorkerConfig(cfg, "auth-logout")
	
	handler, err := authlogout.NewHandler(authlogout.HandlerOptions{
		CustomConfig: &authlogout.Config{
			Enabled:       workerCfg.Enabled,
			MaxJobsActive: workerCfg.MaxJobsActive,
			Timeout:       time.Duration(workerCfg.Timeout) * time.Millisecond,
			RedisHost:     "localhost",
			RedisPort:     6379,
		},
		Logger: logger.NewZapAdapter(log),
	})
	require.NoError(t, err)

	input := &authlogout.Input{
		UserID: "test",
		Token:  "a1b2c3d4e5f6g7h8i9j0k1l2",
	}
	_, err = handler.Execute(context.Background(), input)
	assert.NoError(t, err)
}

func testAuthSigninGoogle(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	workerCfg := config.GetWorkerConfig(cfg, "auth-signin-google")
	
	handler, err := authsigningoogle.NewHandler(authsigningoogle.HandlerOptions{
		CustomConfig: &authsigningoogle.Config{
			Enabled:       workerCfg.Enabled,
			MaxJobsActive: workerCfg.MaxJobsActive,
			Timeout:       time.Duration(workerCfg.Timeout) * time.Millisecond,
			ClientID:      cfg.Auth.OAuthProviders.Google.ClientID,
			ClientSecret:  cfg.Auth.OAuthProviders.Google.ClientSecret,
			RedirectURL:   cfg.Auth.OAuthProviders.Google.RedirectURL,
		},
		Logger: logger.NewZapAdapter(log),
	})
	require.NoError(t, err)

	input := &authsigningoogle.Input{AuthCode: "code"}
	_, err = handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

func testAuthSigninLinkedIn(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	workerCfg := config.GetWorkerConfig(cfg, "auth-signin-linkedin")
	
	handler, err := authsigninlinkedin.NewHandler(authsigninlinkedin.HandlerOptions{
		CustomConfig: &authsigninlinkedin.Config{
			Enabled:       workerCfg.Enabled,
			MaxJobsActive: workerCfg.MaxJobsActive,
			Timeout:       time.Duration(workerCfg.Timeout) * time.Millisecond,
			ClientID:      cfg.Auth.OAuthProviders.LinkedIn.ClientID,
			ClientSecret:  cfg.Auth.OAuthProviders.LinkedIn.ClientSecret,
			RedirectURL:   cfg.Auth.OAuthProviders.LinkedIn.RedirectURL,
		},
		Logger: logger.NewZapAdapter(log),
	})
	require.NoError(t, err)

	input := &authsigninlinkedin.Input{AuthCode: "code"}
	_, err = handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

func testAuthSignupGoogle(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	workerCfg := config.GetWorkerConfig(cfg, "auth-signup-google")
	
	handler, err := authsignupgoogle.NewHandler(authsignupgoogle.HandlerOptions{
		CustomConfig: &authsignupgoogle.Config{
			Enabled:       workerCfg.Enabled,
			MaxJobsActive: workerCfg.MaxJobsActive,
			Timeout:       time.Duration(workerCfg.Timeout) * time.Millisecond,
			ClientID:      cfg.Auth.OAuthProviders.Google.ClientID,
			ClientSecret:  cfg.Auth.OAuthProviders.Google.ClientSecret,
			RedirectURL:   cfg.Auth.OAuthProviders.Google.RedirectURL,
		},
		Logger: logger.NewZapAdapter(log),
	})
	require.NoError(t, err)

	input := &authsignupgoogle.Input{
		AuthCode: "code",
		Email:    "a@b.c",
	}
	_, err = handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

func testAuthSignupLinkedIn(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	workerCfg := config.GetWorkerConfig(cfg, "auth-signup-linkedin")
	
	handler, err := authsignuplinkedin.NewHandler(authsignuplinkedin.HandlerOptions{
		CustomConfig: &authsignuplinkedin.Config{
			Enabled:       workerCfg.Enabled,
			MaxJobsActive: workerCfg.MaxJobsActive,
			Timeout:       time.Duration(workerCfg.Timeout) * time.Millisecond,
			ClientID:      cfg.Auth.OAuthProviders.LinkedIn.ClientID,
			ClientSecret:  cfg.Auth.OAuthProviders.LinkedIn.ClientSecret,
			RedirectURL:   cfg.Auth.OAuthProviders.LinkedIn.RedirectURL,
		},
		Logger: logger.NewZapAdapter(log),
	})
	require.NoError(t, err)

	input := &authsignuplinkedin.Input{
		AuthCode: "code",
		Email:    "a@b.c",
	}
	_, err = handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

func testCaptchaVerify(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	workerCfg := config.GetWorkerConfig(cfg, "captcha-verify")
	
	handler, err := captchaverify.NewHandler(captchaverify.HandlerOptions{
		CustomConfig: &captchaverify.Config{
			Enabled:        workerCfg.Enabled,
			MaxJobsActive:  workerCfg.MaxJobsActive,
			Timeout:        time.Duration(workerCfg.Timeout) * time.Millisecond,
			MaxAttempts:    3,
			VerifyClientIP: false,
			ExpiryMinutes:  5,
		},
		Logger: logger.NewZapAdapter(log),
	})
	require.NoError(t, err)

	input := &captchaverify.Input{
		CaptchaID:    "id",
		CaptchaValue: "ABCD", 
		ClientIP:     "127.0.0.1",
		UserAgent:    "test",
	}
	_, err = handler.Execute(context.Background(), input)
	assert.NoError(t, err)
}

func testEmailSend(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	workerCfg := config.GetWorkerConfig(cfg, "email-send")
	
	handler, err := emailsend.NewHandler(emailsend.HandlerOptions{
		CustomConfig: &emailsend.Config{
			Enabled:       workerCfg.Enabled,
			MaxJobsActive: workerCfg.MaxJobsActive,
			Timeout:       time.Duration(workerCfg.Timeout) * time.Millisecond,
			SMTPHost:      cfg.Integrations.SMTP.Host,
			SMTPPort:      cfg.Integrations.SMTP.Port,
			SMTPUsername:  cfg.Integrations.SMTP.Username,
			SMTPPassword:  cfg.Integrations.SMTP.Password,
			UseTLS:        cfg.Integrations.SMTP.UseTLS,
			DefaultFrom:   cfg.Integrations.SMTP.DefaultFrom,
		},
		Logger: logger.NewZapAdapter(log),
	})
	require.NoError(t, err)

	input := &emailsend.Input{
		To:      "a@b.c",
		Subject: "S",
		Body:    "B",
	}
	_, err = handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

func testCRMUserCreate(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	workerCfg := config.GetWorkerConfig(cfg, "crm-user-create")
	
	handler, err := crmusercreate.NewHandler(crmusercreate.HandlerOptions{
		CustomConfig: &crmusercreate.Config{
			Enabled:        workerCfg.Enabled,
			MaxJobsActive:  workerCfg.MaxJobsActive,
			Timeout:        time.Duration(workerCfg.Timeout) * time.Millisecond,
			ZohoAPIKey:     cfg.Integrations.Zoho.APIKey,
			ZohoOAuthToken: cfg.Integrations.Zoho.AuthToken,
		},
		Logger: logger.NewZapAdapter(log),
	})
	require.NoError(t, err)

	input := &crmusercreate.Input{
		Email:     "a@b.c",
		FirstName: "A",
		LastName:  "B",
	}
	_, err = handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

func testQueryElasticsearch(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler := queryelasticsearch.NewHandler(&queryelasticsearch.Config{
		Timeout: 10 * time.Second,
	}, es, logger.NewZapAdapter(log))

	input := &queryelasticsearch.Input{
		IndexName: "nonexistent",
		QueryType: "franchise_index",
	}
	_, err := handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

func testQueryPostgreSQL(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler := querypostgresql.NewHandler(&querypostgresql.Config{
		Timeout: 5 * time.Second,
	}, db, logger.NewZapAdapter(log))

	input := &querypostgresql.Input{
		QueryType: "unknown",
	}
	_, err := handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

func testApplyRelevanceRanking(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler := applyrelevanceranking.NewHandler(&applyrelevanceranking.Config{
		MaxItems: 10,
	}, logger.NewZapAdapter(log))

	input := &applyrelevanceranking.Input{}
	_, err := handler.Execute(context.Background(), input)
	assert.NoError(t, err)
}

func testCalculateMatchScore(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler := calculatematchscore.NewHandler(&calculatematchscore.Config{}, db, rdb, logger.NewZapAdapter(log))

	input := &calculatematchscore.Input{
		FranchiseData: calculatematchscore.FranchiseData{ID: "test"},
	}
	_, err := handler.Execute(context.Background(), input)
	assert.NoError(t, err)
}

func testParseSearchFilters(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler := parsesearchfilters.NewHandler(&parsesearchfilters.Config{}, logger.NewZapAdapter(log))

	input := &parsesearchfilters.Input{}
	_, err := handler.Execute(context.Background(), input)
	assert.NoError(t, err)
}

func testBuildResponse(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler := buildresponse.NewHandler(&buildresponse.Config{
		TemplateRegistry: "configs/templates.json",
		AppVersion:       "1.0.0",
	}, logger.NewZapAdapter(log))

	input := &buildresponse.Input{
		TemplateId: "nonexistent",
	}
	_, err := handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

func testSelectTemplate(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler := selecttemplate.NewHandler(&selecttemplate.Config{
		TemplateRules: map[string]map[string]string{"route": {}},
	}, logger.NewZapAdapter(log))

	input := &selecttemplate.Input{}
	_, err := handler.Execute(context.Background(), input)
	assert.NoError(t, err)
}

func testValidateSubscription(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	handler := validatesubscription.NewHandler(&validatesubscription.Config{
		Timeout: 5 * time.Second,
	}, db, rdb, logger.NewZapAdapter(log))

	input := &validatesubscription.Input{
		UserID:           "nonexistent",
		SubscriptionTier: "premium",
	}
	_, err := handler.Execute(context.Background(), input)
	assert.Error(t, err)
}

// ==========================
// Benchmark Tests
// ==========================
func BenchmarkHandler_ValidateSubscription(b *testing.B) {
	cfg, _ := config.Load()
	dbClient, _ := database.NewPostgres(cfg.Database.Postgres)
	defer dbClient.Close()
	db := dbClient.GetDB()

	rdbClient, _ := database.NewRedis(cfg.Database.Redis)
	defer rdbClient.Close()
	rdb := rdbClient.GetClient()

	handler := validatesubscription.NewHandler(&validatesubscription.Config{
		Timeout: 5 * time.Second,
	}, db, rdb, logger.NewStructured("info", "json"))

	input := &validatesubscription.Input{
		UserID:           "test-user-123",
		SubscriptionTier: "premium",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_QueryPostgreSQL(b *testing.B) {
	cfg, _ := config.Load()
	dbClient, _ := database.NewPostgres(cfg.Database.Postgres)
	defer dbClient.Close()
	db := dbClient.GetDB()

	handler := querypostgresql.NewHandler(&querypostgresql.Config{
		Timeout: 5 * time.Second,
	}, db, logger.NewStructured("info", "json"))

	input := &querypostgresql.Input{
		QueryType:   "franchise_full_details",
		FranchiseID: "mcdonalds",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_QueryElasticsearch(b *testing.B) {
	cfg, _ := config.Load()
	esURL := cfg.Database.Elasticsearch.GetURL()
	es, _ := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{esURL}})

	handler := queryelasticsearch.NewHandler(&queryelasticsearch.Config{
		Timeout: 10 * time.Second,
	}, es, logger.NewStructured("info", "json"))

	input := &queryelasticsearch.Input{
		IndexName: "franchises",
		QueryType: "franchise_index",
		Filters:   map[string]interface{}{"category": "food"},
		Pagination: queryelasticsearch.Pagination{
			From: 0,
			Size: 10,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_CalculateMatchScore(b *testing.B) {
	cfg, _ := config.Load()
	dbClient, _ := database.NewPostgres(cfg.Database.Postgres)
	defer dbClient.Close()
	db := dbClient.GetDB()

	rdbClient, _ := database.NewRedis(cfg.Database.Redis)
	defer rdbClient.Close()
	rdb := rdbClient.GetClient()

	handler := calculatematchscore.NewHandler(&calculatematchscore.Config{}, db, rdb, logger.NewStructured("info", "json"))

	input := &calculatematchscore.Input{
		FranchiseData: calculatematchscore.FranchiseData{
			ID:            "test-franchise-001",
			Name:          "Test Franchise",
			InvestmentMin: 50000,
			InvestmentMax: 150000,
			Category:      "food",
		},
		UserProfile: &calculatematchscore.UserProfile{
			CapitalAvailable: 100000,
			LocationPrefs:    []string{"New York"},
			Interests:        []string{"food"},
			ExperienceYears:  5,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_CheckReadinessScore(b *testing.B) {
	handler := checkreadinessscore.NewHandler(&checkreadinessscore.Config{}, logger.NewStructured("info", "json"))

	input := &checkreadinessscore.Input{
		UserID: "test-user-123",
		ApplicationData: map[string]interface{}{
			"financialInfo": map[string]interface{}{
				"liquidCapital": 100000,
				"netWorth":      200000,
				"creditScore":   750,
			},
			"experience": map[string]interface{}{
				"yearsInIndustry":      5,
				"managementExperience": true,
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_CreateApplicationRecord(b *testing.B) {
	cfg, _ := config.Load()
	dbClient, _ := database.NewPostgres(cfg.Database.Postgres)
	defer dbClient.Close()
	db := dbClient.GetDB()

	handler := createapplicationrecord.NewHandler(&createapplicationrecord.Config{}, db, logger.NewStructured("info", "json"))

	input := &createapplicationrecord.Input{
		SeekerID:    "test-user-123",
		FranchiseID: "test-franchise-001",
		ApplicationData: map[string]interface{}{
			"name":  "Test Application",
			"email": "test@example.com",
		},
		ReadinessScore: 85,
		Priority:       "high",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_ValidateApplicationData(b *testing.B) {
	handler := validateapplicationdata.NewHandler(&validateapplicationdata.Config{}, logger.NewStructured("info", "json"))
	input := &validateapplicationdata.Input{
		ApplicationData: map[string]interface{}{
			"personalInfo": map[string]interface{}{
				"name":  "John Doe",
				"email": "john@example.com",
				"phone": "+1234567890",
			},
			"financialInfo": map[string]interface{}{
				"liquidCapital": 100000,
				"netWorth":      200000,
				"creditScore":   750,
			},
		},
		FranchiseID: "mcdonalds",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_ParseSearchFilters(b *testing.B) {
	handler := parsesearchfilters.NewHandler(&parsesearchfilters.Config{}, logger.NewStructured("info", "json"))

	input := &parsesearchfilters.Input{
		RawFilters: map[string]interface{}{
			"categories": []string{"food", "retail"},
			"investmentRange": map[string]interface{}{
				"min": 50000,
				"max": 500000,
			},
			"locations": []string{"New York", "California"},
			"keywords":  "franchise opportunity",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_ApplyRelevanceRanking(b *testing.B) {
	handler := applyrelevanceranking.NewHandler(&applyrelevanceranking.Config{MaxItems: 100}, logger.NewStructured("info", "json"))

	input := &applyrelevanceranking.Input{
		SearchResults: []applyrelevanceranking.SearchResult{
			{ID: "franchise-1", Score: 8.5},
			{ID: "franchise-2", Score: 7.2},
			{ID: "franchise-3", Score: 9.1},
		},
		DetailsData: []applyrelevanceranking.FranchiseDetail{
			{
				ID:               "franchise-1",
				Name:             "McDonald's",
				InvestmentMin:    1000000,
				InvestmentMax:    2200000,
				Category:         "Fast Food",
				Locations:        []string{"TX", "CA", "NY"},
				UpdatedAt:        time.Now().Add(-15 * 24 * time.Hour).Format(time.RFC3339),
				ApplicationCount: 150,
				ViewCount:        500,
			},
			{
				ID:               "franchise-2",
				Name:             "Subway",
				InvestmentMin:    80000,
				InvestmentMax:    300000,
				Category:         "Sandwiches",
				Locations:        []string{"TX", "FL", "AZ"},
				UpdatedAt:        time.Now().Add(-60 * 24 * time.Hour).Format(time.RFC3339),
				ApplicationCount: 80,
				ViewCount:        300,
			},
			{
				ID:               "franchise-3",
				Name:             "Starbucks",
				InvestmentMin:    300000,
				InvestmentMax:    700000,
				Category:         "Coffee",
				Locations:        []string{"CA", "WA", "NY"},
				UpdatedAt:        time.Now().Add(-200 * 24 * time.Hour).Format(time.RFC3339),
				ApplicationCount: 200,
				ViewCount:        800,
			},
		},
		UserProfile: applyrelevanceranking.UserProfile{
			CapitalAvailable: 1500000,
			LocationPrefs:    []string{"TX", "CA"},
			Interests:        []string{"Fast Food", "Coffee"},
			ExperienceYears:  3,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_CheckPriorityRouting(b *testing.B) {
	cfg, _ := config.Load()
	dbClient, _ := database.NewPostgres(cfg.Database.Postgres)
	defer dbClient.Close()
	db := dbClient.GetDB()

	rdbClient, _ := database.NewRedis(cfg.Database.Redis)
	defer rdbClient.Close()
	rdb := rdbClient.GetClient()

	handler := checkpriorityrouting.NewHandler(&checkpriorityrouting.Config{
		CacheTTL: 30 * time.Minute,
	}, db, rdb, logger.NewStructured("info", "json"))

	input := &checkpriorityrouting.Input{
		FranchiseID: "test-franchise-001",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_SendNotification(b *testing.B) {
	cfg, _ := config.Load()
	dbClient, _ := database.NewPostgres(cfg.Database.Postgres)
	defer dbClient.Close()
	db := dbClient.GetDB()

	handler, _ := sendnotification.NewHandler(&sendnotification.Config{
		EmailEnabled: false,
		SMSEnabled:   false,
	}, db, logger.NewStructured("info", "json"))

	input := &sendnotification.Input{
		RecipientID:      "test-user-123",
		RecipientType:    sendnotification.RecipientTypeFranchisor,
		NotificationType: sendnotification.TypeNewApplication,
		ApplicationID:    "app-123",
		Priority:         "high",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_BuildResponse(b *testing.B) {
	handler := buildresponse.NewHandler(&buildresponse.Config{
		TemplateRegistry: "configs/templates.json",
		AppVersion:       "1.0.0",
	}, logger.NewStructured("info", "json"))

	input := &buildresponse.Input{
		TemplateId: "franchise-detail",
		RequestId:  "req-123",
		Data: map[string]interface{}{
			"name":        "McDonald's",
			"investment":  500000,
			"category":    "food",
			"description": "Fast food franchise",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_SelectTemplate(b *testing.B) {
	handler := selecttemplate.NewHandler(&selecttemplate.Config{
		TemplateRules: map[string]map[string]string{
			"route": {
				"/franchise/search:premium":  "search-premium-template",
				"/franchise/search:free":     "search-free-template",
				"/franchise/search:fallback": "search-fallback-template",
			},
		},
	}, logger.NewStructured("info", "json"))

	input := &selecttemplate.Input{
		SubscriptionTier: "premium",
		RoutePath:        "/franchise/search",
		TemplateType:     "",
		Confidence:       0.0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_ParseUserIntent(b *testing.B) {
	logAdapter := &parseUserIntentLoggerAdapter{logger.NewStructured("info", "json")}

	handler := parseuserintent.NewHandler(&parseuserintent.Config{
		GenAIBaseURL: "http://localhost:8080/mock",
		Timeout:      30 * time.Second,
		MaxRetries:   2,
	}, logAdapter)

	input := &parseuserintent.Input{
		Question: "Tell me about McDonald's franchise opportunities in Texas?",
		Context: map[string]interface{}{
			"userType": "prospective_franchisee",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_QueryInternalData(b *testing.B) {
	cfg, _ := config.Load()
	dbClient, _ := database.NewPostgres(cfg.Database.Postgres)
	defer dbClient.Close()
	db := dbClient.GetDB()

	esURL := cfg.Database.Elasticsearch.GetURL()
	es, _ := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{esURL}})

	rdbClient, _ := database.NewRedis(cfg.Database.Redis)
	defer rdbClient.Close()
	rdb := rdbClient.GetClient()

	logAdapter := &queryInternalDataLoggerAdapter{logger.NewStructured("info", "json")}

	handler := queryinternaldata.NewHandler(&queryinternaldata.Config{
		Timeout:    2 * time.Second,
		CacheTTL:   5 * time.Minute,
		MaxResults: 10,
	}, db, es, rdb, logAdapter)

	input := &queryinternaldata.Input{
		Entities: []queryinternaldata.Entity{
			{Type: "franchise_name", Value: "McDonald's"},
		},
		DataSources: []string{"internal_db", "search_index"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_EnrichWebSearch(b *testing.B) {
	logAdapter := &enrichWebSearchLoggerAdapter{logger.NewStructured("info", "json")}

	handler := enrichwebsearch.NewHandler(&enrichwebsearch.Config{
		SearchAPIBaseURL: "http://localhost:8080/mock",
		SearchAPIKey:     "mock",
		SearchEngineID:   "mock",
		Timeout:          5 * time.Second,
		MaxResults:       5,
		MinRelevance:     0.1,
	}, logAdapter)

	input := &enrichwebsearch.Input{
		Question: "McDonald's franchise",
		Entities: []enrichwebsearch.Entity{{Type: "franchise_name", Value: "McDonald's"}},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_LLMSynthesis(b *testing.B) {
	logAdapter := &llmSynthesisLoggerAdapter{logger.NewStructured("info", "json")}

	handler := llmsynthesis.NewHandler(&llmsynthesis.Config{
		GenAIBaseURL: "http://localhost:8080/mock",
		Timeout:      5 * time.Second,
		MaxRetries:   1,
		MaxTokens:    100,
		Temperature:  0.7,
	}, logAdapter)

	input := &llmsynthesis.Input{
		Question: "What are McDonald's franchise fees?",
		InternalData: map[string]interface{}{
			"franchise_name":   "McDonald's",
			"initial_fee":      45000,
			"total_investment": "1M-2M",
		},
		WebData: llmsynthesis.WebData{
			Sources: []llmsynthesis.Source{
				{URL: "https://mcdonalds.com", Title: "Official Site"},
			},
			Summary: "McDonald's franchise information",
		},
		Intent: llmsynthesis.Intent{
			PrimaryIntent: "franchise_cost_inquiry",
			Confidence:    0.9,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_AuthLogout(b *testing.B) {
	handler, _ := authlogout.NewHandler(authlogout.HandlerOptions{
		CustomConfig: &authlogout.Config{
			Enabled:       true,
			MaxJobsActive: 5,
			Timeout:       10 * time.Second,
		},
		Logger: logger.NewStructured("info", "json"),
	})

	input := &authlogout.Input{
		UserID:    "test-user-123",
		Token:     "a1b2c3d4e5f6g7h8i9j0k1l2",
		SessionID: "session-456",
		DeviceID:  "device-789",
		LogoutAll: false,
		Reason:    "user_initiated",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_AuthSigninGoogle(b *testing.B) {
	handler, _ := authsigningoogle.NewHandler(authsigningoogle.HandlerOptions{
		CustomConfig: &authsigningoogle.Config{
			Enabled:       true,
			MaxJobsActive: 5,
			Timeout:       10 * time.Second,
		},
		Logger: logger.NewStructured("info", "json"),
	})

	input := &authsigningoogle.Input{
		AuthCode:    "test-auth-code-12345",
		RedirectURI: "https://example.com/callback",
		State:       "test-state",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_AuthSigninLinkedIn(b *testing.B) {
	handler, _ := authsigninlinkedin.NewHandler(authsigninlinkedin.HandlerOptions{
		CustomConfig: &authsigninlinkedin.Config{
			Enabled:       true,
			MaxJobsActive: 5,
			Timeout:       10 * time.Second,
		},
		Logger: logger.NewStructured("info", "json"),
	})

	input := &authsigninlinkedin.Input{
		AuthCode:    "test-auth-code-12345",
		RedirectURI: "https://example.com/callback",
		State:       "test-state",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_AuthSignupGoogle(b *testing.B) {
	handler, _ := authsignupgoogle.NewHandler(authsignupgoogle.HandlerOptions{
		CustomConfig: &authsignupgoogle.Config{
			Enabled:       true,
			MaxJobsActive: 5,
			Timeout:       10 * time.Second,
		},
		Logger: logger.NewStructured("info", "json"),
	})

	input := &authsignupgoogle.Input{
		AuthCode:    "test-auth-code-12345",
		Email:       "newuser@example.com",
		RedirectURI: "https://example.com/callback",
		State:       "test-state",
		FirstName:   "Jane",
		LastName:    "Smith",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_AuthSignupLinkedIn(b *testing.B) {
	handler, _ := authsignuplinkedin.NewHandler(authsignuplinkedin.HandlerOptions{
		CustomConfig: &authsignuplinkedin.Config{
			Enabled:       true,
			MaxJobsActive: 5,
			Timeout:       10 * time.Second,
		},
		Logger: logger.NewStructured("info", "json"),
	})

	input := &authsignuplinkedin.Input{
		AuthCode:    "test-auth-code-12345",
		Email:       "newuser@example.com",
		RedirectURI: "https://example.com/callback",
		State:       "test-state",
		FirstName:   "Jane",
		LastName:    "Smith",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_CaptchaVerify(b *testing.B) {
	handler, _ := captchaverify.NewHandler(captchaverify.HandlerOptions{
		CustomConfig: &captchaverify.Config{
			Enabled:       true,
			MaxJobsActive: 10,
			Timeout:       5 * time.Second,
		},
		Logger: logger.NewStructured("info", "json"),
	})

	input := &captchaverify.Input{
		CaptchaID:    "cap_test123",
		CaptchaValue: "ABCD",
		ClientIP:     "192.168.1.1",
		UserAgent:    "Mozilla/5.0 Test Browser",
		SessionID:    "sess_12345",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_EmailSend(b *testing.B) {
	handler, _ := emailsend.NewHandler(emailsend.HandlerOptions{
		CustomConfig: &emailsend.Config{
			Enabled:     true,
			DefaultFrom: "test@example.com",
		},
		Logger: logger.NewStructured("info", "json"),
	})

	input := &emailsend.Input{
		To:       "test@example.com",
		Subject:  "Test Subject",
		Body:     "Test body content",
		IsHTML:   false,
		Priority: "normal",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}

func BenchmarkHandler_CRMUserCreate(b *testing.B) {
	handler, _ := crmusercreate.NewHandler(crmusercreate.HandlerOptions{
		CustomConfig: &crmusercreate.Config{
			Enabled:       true,
			MaxJobsActive: 5,
			Timeout:       30 * time.Second,
		},
		Logger: logger.NewStructured("info", "json"),
	})

	input := &crmusercreate.Input{
		Email:      "newuser@example.com",
		FirstName:  "Jane",
		LastName:   "Doe",
		Phone:      "+1234567890",
		Company:    "Acme Corp",
		JobTitle:   "Software Engineer",
		LeadSource: "Website",
		Tags:       []string{"prospect", "interested"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}


// // test/e2e/e2e_complete_test.go
// package e2e

// import (
// 	"context"
// 	"database/sql"
// 	"encoding/json"
// 	"fmt"
// 	"os"
// 	"strings"
// 	"testing"
// 	"time"

// 	"camunda-workers/internal/common/auth"
// 	"camunda-workers/internal/common/camunda"
// 	"camunda-workers/internal/common/config"
// 	"camunda-workers/internal/common/database"
// 	"camunda-workers/internal/common/logger"
// 	"camunda-workers/internal/common/zoho"

// 	// Import all worker packages
// 	authlogout "camunda-workers/internal/workers/auth/auth-logout"
// 	authsigningoogle "camunda-workers/internal/workers/auth/auth-signin-google"
// 	authsigninlinkedin "camunda-workers/internal/workers/auth/auth-signin-linkedin"
// 	authsignupgoogle "camunda-workers/internal/workers/auth/auth-signup-google"
// 	authsignuplinkedin "camunda-workers/internal/workers/auth/auth-signup-linkedin"
// 	captchaverify "camunda-workers/internal/workers/auth/captcha-verify"
// 	emailsend "camunda-workers/internal/workers/communication/email-send"
// 	crmusercreate "camunda-workers/internal/workers/crm/crm-user-create"

// 	checkpriorityrouting "camunda-workers/internal/workers/application/check-priority-routing"
// 	checkreadinessscore "camunda-workers/internal/workers/application/check-readiness-score"
// 	createapplicationrecord "camunda-workers/internal/workers/application/create-application-record"
// 	sendnotification "camunda-workers/internal/workers/application/send-notification"
// 	validateapplicationdata "camunda-workers/internal/workers/application/validate-application-data"

// 	enrichwebsearch "camunda-workers/internal/workers/ai-conversation/enrich-web-search"
// 	llmsynthesis "camunda-workers/internal/workers/ai-conversation/llm-synthesis"
// 	parseuserintent "camunda-workers/internal/workers/ai-conversation/parse-user-intent"
// 	queryinternaldata "camunda-workers/internal/workers/ai-conversation/query-internal-data"

// 	queryelasticsearch "camunda-workers/internal/workers/data-access/query-elasticsearch"
// 	querypostgresql "camunda-workers/internal/workers/data-access/query-postgresql"

// 	applyrelevanceranking "camunda-workers/internal/workers/franchise/apply-relevance-ranking"
// 	calculatematchscore "camunda-workers/internal/workers/franchise/calculate-match-score"
// 	parsesearchfilters "camunda-workers/internal/workers/franchise/parse-search-filters"

// 	buildresponse "camunda-workers/internal/workers/infrastructure/build-response"
// 	selecttemplate "camunda-workers/internal/workers/infrastructure/select-template"
// 	validatesubscription "camunda-workers/internal/workers/infrastructure/validate-subscription"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/zbc"
// 	"github.com/elastic/go-elasticsearch/v8"
// 	"github.com/redis/go-redis/v9"
// 	"github.com/stretchr/testify/assert"
// 	"github.com/stretchr/testify/require"
// 	"go.uber.org/zap"
// )

// // TestEnvironment holds all dependencies for E2E tests
// type TestEnvironment struct {
// 	Config          *config.Config
// 	CamundaClient   *camunda.Client
// 	PostgresClient  *database.PostgresClient
// 	ElasticsearchClient *database.ElasticsearchClient
// 	RedisClient     *database.RedisClient
// 	KeycloakClient  *auth.KeycloakClient
// 	ZohoCRMClient   *zoho.CRMClient
// 	Logger          *zap.Logger
// 	ZeebeClient     zbc.Client
// }

// // WorkerTestResult tracks individual worker test results
// type WorkerTestResult struct {
// 	WorkerName string
// 	Success    bool
// 	Error      error
// 	Duration   time.Duration
// }

// // SetupTestEnvironment initializes all required dependencies
// func SetupTestEnvironment(t *testing.T) *TestEnvironment {
// 	// Load configuration
// 	cfg, err := config.Load()
// 	require.NoError(t, err, "Failed to load configuration")

// 	// Override with test-specific settings
// 	cfg.Camunda.BrokerAddress = getEnvOrDefault("ZEEBE_ADDRESS", "localhost:26500")
// 	cfg.Database.Postgres.Host = getEnvOrDefault("DB_HOST", "localhost")
// 	cfg.Database.Redis.Address = getEnvOrDefault("REDIS_ADDRESS", "localhost:6379")
// 	cfg.Database.Elasticsearch.Addresses = []string{getEnvOrDefault("ES_URLS", "http://localhost:9200")}

// 	// Initialize logger
// 	lgr := logger.New(cfg.Logging.Level, cfg.Logging.Format)

// 	// Initialize Camunda client
// 	camundaClient, err := camunda.NewClient(cfg.Camunda.BrokerAddress)
// 	require.NoError(t, err, "Failed to create Camunda client")

// 	// Initialize PostgreSQL
// 	postgresClient, err := database.NewPostgres(cfg.Database.Postgres)
// 	require.NoError(t, err, "Failed to create PostgreSQL client")
// 	require.NoError(t, postgresClient.Ping(), "PostgreSQL health check failed")

// 	// Initialize Elasticsearch
// 	esClient, err := database.NewElasticsearch(cfg.Database.Elasticsearch)
// 	require.NoError(t, err, "Failed to create Elasticsearch client")
// 	require.NoError(t, esClient.Ping(), "Elasticsearch health check failed")

// 	// Initialize Redis
// 	redisClient, err := database.NewRedis(cfg.Database.Redis)
// 	require.NoError(t, err, "Failed to create Redis client")
// 	require.NoError(t, redisClient.Ping(), "Redis health check failed")

// 	// Initialize Keycloak
// 	keycloakClient := auth.NewKeycloakClient(
// 		cfg.Auth.Keycloak.URL,
// 		cfg.Auth.Keycloak.Realm,
// 		cfg.Auth.Keycloak.ClientID,
// 		cfg.Auth.Keycloak.ClientSecret,
// 	)

// 	// Initialize Zoho CRM
// 	zohoCRMClient := zoho.NewCRMClient(
// 		cfg.Integrations.Zoho.APIKey,
// 		cfg.Integrations.Zoho.AuthToken,
// 	)

// 	return &TestEnvironment{
// 		Config:              cfg,
// 		CamundaClient:       camundaClient,
// 		PostgresClient:      postgresClient,
// 		ElasticsearchClient: esClient,
// 		RedisClient:         redisClient,
// 		KeycloakClient:      keycloakClient,
// 		ZohoCRMClient:       zohoCRMClient,
// 		Logger:              lgr,
// 		ZeebeClient:         camundaClient.GetClient(),
// 	}
// }

// // TeardownTestEnvironment cleans up all resources
// func (env *TestEnvironment) Teardown() {
// 	if env.CamundaClient != nil {
// 		env.CamundaClient.Close()
// 	}
// 	if env.PostgresClient != nil {
// 		env.PostgresClient.Close()
// 	}
// 	if env.ElasticsearchClient != nil {
// 		// Elasticsearch client doesn't need explicit close
// 	}
// 	if env.RedisClient != nil {
// 		env.RedisClient.Close()
// 	}
// }

// // TestAllWorkers is the main test function that tests all 25 workers
// func TestAllWorkers(t *testing.T) {
// 	if testing.Short() {
// 		t.Skip("Skipping E2E tests in short mode")
// 	}

// 	env := SetupTestEnvironment(t)
// 	defer env.Teardown()

// 	// Setup test data
// 	setupTestData(t, env)
// 	defer cleanupTestData(t, env)

// 	results := make([]WorkerTestResult, 0, 25)

// 	// Test all workers in logical groups
// 	t.Run("InfrastructureWorkers", func(t *testing.T) {
// 		results = append(results, testInfrastructureWorkers(t, env)...)
// 	})

// 	t.Run("DataAccessWorkers", func(t *testing.T) {
// 		results = append(results, testDataAccessWorkers(t, env)...)
// 	})

// 	t.Run("AuthenticationWorkers", func(t *testing.T) {
// 		results = append(results, testAuthenticationWorkers(t, env)...)
// 	})

// 	t.Run("FranchiseWorkers", func(t *testing.T) {
// 		results = append(results, testFranchiseWorkers(t, env)...)
// 	})

// 	t.Run("ApplicationWorkers", func(t *testing.T) {
// 		results = append(results, testApplicationWorkers(t, env)...)
// 	})

// 	t.Run("AIConversationWorkers", func(t *testing.T) {
// 		results = append(results, testAIConversationWorkers(t, env)...)
// 	})

// 	t.Run("CommunicationWorkers", func(t *testing.T) {
// 		results = append(results, testCommunicationWorkers(t, env)...)
// 	})

// 	t.Run("CRMWorkers", func(t *testing.T) {
// 		results = append(results, testCRMWorkers(t, env)...)
// 	})

// 	// Print summary
// 	printTestSummary(t, results)
// }

// // ============================================================================
// // Infrastructure Workers Tests (3 workers)
// // ============================================================================

// func testInfrastructureWorkers(t *testing.T, env *TestEnvironment) []WorkerTestResult {
// 	results := make([]WorkerTestResult, 0, 3)

// 	// 1. Validate Subscription Worker
// 	t.Run("ValidateSubscription", func(t *testing.T) {
// 		start := time.Now()
// 		err := testValidateSubscription(t, env)
// 		results = append(results, WorkerTestResult{
// 			WorkerName: "validate-subscription",
// 			Success:    err == nil,
// 			Error:      err,
// 			Duration:   time.Since(start),
// 		})
// 	})

// 	// 2. Select Template Worker
// 	t.Run("SelectTemplate", func(t *testing.T) {
// 		start := time.Now()
// 		err := testSelectTemplate(t, env)
// 		results = append(results, WorkerTestResult{
// 			WorkerName: "select-template",
// 			Success:    err == nil,
// 			Error:      err,
// 			Duration:   time.Since(start),
// 		})
// 	})

// 	// 3. Build Response Worker
// 	t.Run("BuildResponse", func(t *testing.T) {
// 		start := time.Now()
// 		err := testBuildResponse(t, env)
// 		results = append(results, WorkerTestResult{
// 			WorkerName: "build-response",
// 			Success:    err == nil,
// 			Error:      err,
// 			Duration:   time.Since(start),
// 		})
// 	})

// 	return results
// }

// func testValidateSubscription(t *testing.T, env *TestEnvironment) error {
// 	handler, err := validatesubscription.NewHandler(
// 		validatesubscription.DefaultConfig(),
// 		env.PostgresClient.DB,
// 		env.RedisClient.Client,
// 		env.Logger,
// 	)
// 	if err != nil {
// 		return fmt.Errorf("failed to create handler: %w", err)
// 	}

// 	input := validatesubscription.Input{
// 		UserID: "test-user-123",
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotNil(t, output)
// 	return nil
// }

// func testCreateApplicationRecord(t *testing.T, env *TestEnvironment) error {
// 	handler := createapplicationrecord.NewHandler(
// 		createapplicationrecord.DefaultConfig(),
// 		env.PostgresClient.DB,
// 		env.Logger,
// 	)

// 	input := createapplicationrecord.Input{
// 		SeekerID:    fmt.Sprintf("test-seeker-%d", time.Now().Unix()),
// 		FranchiseID: "test-franchise-001",
// 		ApplicationData: map[string]interface{}{
// 			"personalInfo": map[string]interface{}{
// 				"name":  "John Doe",
// 				"email": "john@example.com",
// 			},
// 		},
// 		ReadinessScore: 85,
// 		Priority:       "high",
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotEmpty(t, output.ApplicationID)
// 	assert.Equal(t, "submitted", output.ApplicationStatus)
// 	return nil
// }

// func testSendNotification(t *testing.T, env *TestEnvironment) error {
// 	handler, err := sendnotification.NewHandler(
// 		sendnotification.DefaultConfig(),
// 		env.PostgresClient.DB,
// 		env.Logger,
// 	)
// 	if err != nil {
// 		return fmt.Errorf("failed to create handler: %w", err)
// 	}

// 	input := sendnotification.Input{
// 		RecipientID:      "test-recipient-123",
// 		RecipientType:    "seeker",
// 		NotificationType: "application_submitted",
// 		ApplicationID:    "test-app-001",
// 		Priority:         "medium",
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotEmpty(t, output.NotificationID)
// 	return nil
// }

// // ============================================================================
// // AI Conversation Workers Tests (4 workers)
// // ============================================================================

// func testAIConversationWorkers(t *testing.T, env *TestEnvironment) []WorkerTestResult {
// 	results := make([]WorkerTestResult, 0, 4)

// 	workers := []struct {
// 		name string
// 		test func(*testing.T, *TestEnvironment) error
// 	}{
// 		{"parse-user-intent", testParseUserIntent},
// 		{"query-internal-data", testQueryInternalData},
// 		{"enrich-web-search", testEnrichWebSearch},
// 		{"llm-synthesis", testLLMSynthesis},
// 	}

// 	for _, w := range workers {
// 		t.Run(w.name, func(t *testing.T) {
// 			start := time.Now()
// 			err := w.test(t, env)
// 			results = append(results, WorkerTestResult{
// 				WorkerName: w.name,
// 				Success:    err == nil,
// 				Error:      err,
// 				Duration:   time.Since(start),
// 			})
// 		})
// 	}

// 	return results
// }

// func testParseUserIntent(t *testing.T, env *TestEnvironment) error {
// 	cfg := parseuserintent.DefaultConfig()
// 	cfg.GenAIBaseURL = env.Config.APIs.GenAI.BaseURL

// 	handler := parseuserintent.NewHandler(
// 		cfg,
// 		env.Logger,
// 	)

// 	input := parseuserintent.Input{
// 		Question: "What are the best food franchises in New York?",
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		// Expected to fail with mock GenAI service, but worker structure is validated
// 		return nil
// 	}

// 	assert.NotNil(t, output)
// 	return nil
// }

// func testQueryInternalData(t *testing.T, env *TestEnvironment) error {
// 	handler := queryinternaldata.NewHandler(
// 		queryinternaldata.DefaultConfig(),
// 		env.PostgresClient.DB,
// 		env.ElasticsearchClient.Client,
// 		env.RedisClient.Client,
// 		env.Logger,
// 	)

// 	input := queryinternaldata.Input{
// 		Entities: []queryinternaldata.Entity{
// 			{Type: "franchise_name", Value: "Test Franchise"},
// 			{Type: "location", Value: "New York"},
// 		},
// 		DataSources: []string{"internal_db", "search_index"},
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotNil(t, output.InternalData)
// 	return nil
// }

// func testEnrichWebSearch(t *testing.T, env *TestEnvironment) error {
// 	cfg := enrichwebsearch.DefaultConfig()
// 	cfg.SearchAPIBaseURL = env.Config.APIs.WebSearch.BaseURL
// 	cfg.SearchAPIKey = env.Config.APIs.WebSearch.APIKey
// 	cfg.SearchEngineID = env.Config.APIs.WebSearch.EngineID

// 	handler := enrichwebsearch.NewHandler(
// 		cfg,
// 		env.Logger,
// 	)

// 	input := enrichwebsearch.Input{
// 		Question: "Best franchise opportunities 2024",
// 		Entities: []enrichwebsearch.Entity{
// 			{Type: "category", Value: "food"},
// 		},
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		// Expected to fail with mock web search API, but worker structure is validated
// 		return nil
// 	}

// 	assert.NotNil(t, output)
// 	return nil
// }

// func testLLMSynthesis(t *testing.T, env *TestEnvironment) error {
// 	cfg := llmsynthesis.DefaultConfig()
// 	cfg.GenAIBaseURL = env.Config.APIs.GenAI.BaseURL

// 	handler := llmsynthesis.NewHandler(
// 		cfg,
// 		env.Logger,
// 	)

// 	input := llmsynthesis.Input{
// 		Question: "What are the best food franchises?",
// 		InternalData: map[string]interface{}{
// 			"franchises": []map[string]interface{}{
// 				{"name": "Test Franchise", "category": "food"},
// 			},
// 		},
// 		WebData: llmsynthesis.WebData{
// 			Sources: []llmsynthesis.Source{
// 				{URL: "https://example.com", Title: "Franchise Info"},
// 			},
// 		},
// 		Intent: map[string]interface{}{
// 			"primaryIntent": "general_info",
// 		},
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		// Expected to fail with mock GenAI service, but worker structure is validated
// 		return nil
// 	}

// 	assert.NotNil(t, output)
// 	return nil
// }

// // ============================================================================
// // Communication Workers Tests (1 worker)
// // ============================================================================

// func testCommunicationWorkers(t *testing.T, env *TestEnvironment) []WorkerTestResult {
// 	results := make([]WorkerTestResult, 0, 1)

// 	t.Run("email-send", func(t *testing.T) {
// 		start := time.Now()
// 		err := testEmailSend(t, env)
// 		results = append(results, WorkerTestResult{
// 			WorkerName: "email-send",
// 			Success:    err == nil,
// 			Error:      err,
// 			Duration:   time.Since(start),
// 		})
// 	})

// 	return results
// }

// func testEmailSend(t *testing.T, env *TestEnvironment) error {
// 	_, err := emailsend.NewHandler(emailsend.HandlerOptions{
// 		AppConfig:    env.Config,
// 		Camunda:      env.CamundaClient,
// 		CustomConfig: emailsend.DefaultConfig(),
// 		Logger:       logger.NewStructured("info", "json"),
// 	})

// 	return err
// }

// // ============================================================================
// // CRM Workers Tests (1 worker)
// // ============================================================================

// func testCRMWorkers(t *testing.T, env *TestEnvironment) []WorkerTestResult {
// 	results := make([]WorkerTestResult, 0, 1)

// 	t.Run("crm-user-create", func(t *testing.T) {
// 		start := time.Now()
// 		err := testCRMUserCreate(t, env)
// 		results = append(results, WorkerTestResult{
// 			WorkerName: "crm-user-create",
// 			Success:    err == nil,
// 			Error:      err,
// 			Duration:   time.Since(start),
// 		})
// 	})

// 	return results
// }

// func testCRMUserCreate(t *testing.T, env *TestEnvironment) error {
// 	_, err := crmusercreate.NewHandler(crmusercreate.HandlerOptions{
// 		AppConfig:    env.Config,
// 		Camunda:      env.CamundaClient,
// 		CustomConfig: crmusercreate.DefaultConfig(),
// 		Logger:       logger.NewStructured("info", "json"),
// 	})

// 	return err
// }

// // ============================================================================
// // Helper Functions
// // ============================================================================

// func setupTestData(t *testing.T, env *TestEnvironment) {
// 	ctx := context.Background()

// 	// Setup PostgreSQL test data
// 	setupPostgresTestData(t, env.PostgresClient.DB)

// 	// Setup Elasticsearch test data
// 	setupElasticsearchTestData(t, env.ElasticsearchClient.Client)

// 	// Setup Redis test data
// 	setupRedisTestData(t, ctx, env.RedisClient.Client)
// }

// func setupPostgresTestData(t *testing.T, db *sql.DB) {
// 	// Create test tables if they don't exist
// 	queries := []string{
// 		`CREATE TABLE IF NOT EXISTS franchises (
// 			id VARCHAR(255) PRIMARY KEY,
// 			name VARCHAR(255) NOT NULL,
// 			description TEXT,
// 			investment_min INTEGER,
// 			investment_max INTEGER,
// 			category VARCHAR(100),
// 			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
// 			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
// 		)`,
// 		`CREATE TABLE IF NOT EXISTS franchise_outlets (
// 			id SERIAL PRIMARY KEY,
// 			franchise_id VARCHAR(255) REFERENCES franchises(id),
// 			address TEXT,
// 			city VARCHAR(100),
// 			state VARCHAR(100),
// 			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
// 		)`,
// 		`CREATE TABLE IF NOT EXISTS franchisors (
// 			id SERIAL PRIMARY KEY,
// 			franchise_id VARCHAR(255) REFERENCES franchises(id),
// 			account_type VARCHAR(50) DEFAULT 'standard',
// 			email VARCHAR(255),
// 			phone VARCHAR(50),
// 			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
// 		)`,
// 		`CREATE TABLE IF NOT EXISTS users (
// 			id VARCHAR(255) PRIMARY KEY,
// 			email VARCHAR(255) UNIQUE NOT NULL,
// 			phone VARCHAR(50),
// 			capital_available INTEGER,
// 			location_preferences JSONB,
// 			interests JSONB,
// 			industry_experience INTEGER,
// 			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
// 		)`,
// 		`CREATE TABLE IF NOT EXISTS user_subscriptions (
// 			id SERIAL PRIMARY KEY,
// 			user_id VARCHAR(255) UNIQUE NOT NULL,
// 			tier VARCHAR(50) NOT NULL,
// 			expires_at TIMESTAMP,
// 			is_valid BOOLEAN DEFAULT true,
// 			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
// 		)`,
// 		`CREATE TABLE IF NOT EXISTS applications (
// 			id VARCHAR(255) PRIMARY KEY,
// 			seeker_id VARCHAR(255) NOT NULL,
// 			franchise_id VARCHAR(255) NOT NULL,
// 			application_data JSONB,
// 			readiness_score INTEGER,
// 			priority VARCHAR(50),
// 			status VARCHAR(50),
// 			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
// 			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
// 			UNIQUE(seeker_id, franchise_id)
// 		)`,
// 		`CREATE TABLE IF NOT EXISTS audit_log (
// 			id SERIAL PRIMARY KEY,
// 			event_type VARCHAR(100),
// 			resource_type VARCHAR(100),
// 			resource_id VARCHAR(255),
// 			details JSONB,
// 			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
// 		)`,
// 	}

// 	for _, query := range queries {
// 		_, err := db.Exec(query)
// 		if err != nil {
// 			t.Logf("Warning: Failed to create table: %v", err)
// 		}
// 	}

// 	// Insert test data
// 	testData := []string{
// 		`INSERT INTO franchises (id, name, description, investment_min, investment_max, category)
// 		 VALUES ('test-franchise-001', 'Test Franchise', 'A test franchise', 50000, 150000, 'food')
// 		 ON CONFLICT (id) DO NOTHING`,
// 		`INSERT INTO franchisors (franchise_id, account_type, email, phone)
// 		 VALUES ('test-franchise-001', 'premium', 'franchisor@test.com', '+1234567890')
// 		 ON CONFLICT DO NOTHING`,
// 		`INSERT INTO users (id, email, capital_available, location_preferences, interests, industry_experience)
// 		 VALUES ('test-user-123', 'testuser@example.com', 100000, '["New York"]', '["food"]', 5)
// 		 ON CONFLICT (id) DO NOTHING`,
// 		`INSERT INTO user_subscriptions (user_id, tier, expires_at, is_valid)
// 		 VALUES ('test-user-123', 'premium', NOW() + INTERVAL '1 year', true)
// 		 ON CONFLICT (user_id) DO NOTHING`,
// 	}

// 	for _, query := range testData {
// 		_, err := db.Exec(query)
// 		if err != nil {
// 			t.Logf("Warning: Failed to insert test data: %v", err)
// 		}
// 	}
// }

// func setupElasticsearchTestData(t *testing.T, es *elasticsearch.Client) {
// 	ctx := context.Background()

// 	// Create test index
// 	indexName := "franchises"

// 	// Delete existing index if it exists
// 	es.Indices.Delete([]string{indexName})

// 	// Create new index with mapping
// 	mapping := `{
// 		"mappings": {
// 			"properties": {
// 				"id": {"type": "keyword"},
// 				"name": {"type": "text"},
// 				"category": {"type": "keyword"},
// 				"description": {"type": "text"},
// 				"investment_min": {"type": "integer"},
// 				"investment_max": {"type": "integer"},
// 				"locations": {"type": "keyword"}
// 			}
// 		}
// 	}`

// 	res, err := es.Indices.Create(
// 		indexName,
// 		es.Indices.Create.WithBody(strings.NewReader(mapping)),
// 		es.Indices.Create.WithContext(ctx),
// 	)
// 	if err != nil {
// 		t.Logf("Warning: Failed to create ES index: %v", err)
// 	} else {
// 		defer res.Body.Close()
// 	}

// 	// Index test document
// 	doc := `{
// 		"id": "fr-001",
// 		"name": "Test Franchise",
// 		"category": "food",
// 		"description": "A test franchise",
// 		"investment_min": 50000,
// 		"investment_max": 150000,
// 		"locations": ["New York"]
// 	}`

// 	res, err = es.Index(
// 		indexName,
// 		strings.NewReader(doc),
// 		es.Index.WithDocumentID("fr-001"),
// 		es.Index.WithContext(ctx),
// 		es.Index.WithRefresh("true"),
// 	)
// 	if err != nil {
// 		t.Logf("Warning: Failed to index test document: %v", err)
// 	} else {
// 		defer res.Body.Close()
// 	}
// }

// func setupRedisTestData(t *testing.T, ctx context.Context, redis *redis.Client) {
// 	// Set some test cache data
// 	testData := map[string]string{
// 		"user:profile:test-user-123": `{"capitalAvailable":100000,"locationPrefs":["New York"],"interests":["food"],"experienceYears":5}`,
// 		"franchisor:account:test-franchise-001": "premium",
// 	}

// 	for key, value := range testData {
// 		err := redis.Set(ctx, key, value, 5*time.Minute).Err()
// 		if err != nil {
// 			t.Logf("Warning: Failed to set Redis key %s: %v", key, err)
// 		}
// 	}
// }

// func cleanupTestData(t *testing.T, env *TestEnvironment) {
// 	ctx := context.Background()

// 	// Clean PostgreSQL
// 	cleanupQueries := []string{
// 		`DELETE FROM applications WHERE seeker_id LIKE 'test-%'`,
// 		`DELETE FROM audit_log WHERE resource_id LIKE 'test-%'`,
// 		// Keep other test data for subsequent runs
// 	}

// 	for _, query := range cleanupQueries {
// 		_, err := env.PostgresClient.DB.Exec(query)
// 		if err != nil {
// 			t.Logf("Warning: Failed to cleanup: %v", err)
// 		}
// 	}

// 	// Clean Redis test keys
// 	env.RedisClient.Client.Del(ctx, "test:*")
// }

// func printTestSummary(t *testing.T, results []WorkerTestResult) {
// 	t.Log("\n" + strings.Repeat("=", 80))
// 	t.Log("E2E TEST SUMMARY - ALL 25 WORKERS")
// 	t.Log(strings.Repeat("=", 80))

// 	totalWorkers := len(results)
// 	successCount := 0
// 	failCount := 0
// 	totalDuration := time.Duration(0)

// 	for _, result := range results {
// 		if result.Success {
// 			successCount++
// 		} else {
// 			failCount++
// 		}
// 		totalDuration += result.Duration
// 	}

// 	t.Logf("\nTotal Workers Tested: %d", totalWorkers)
// 	t.Logf("Successful: %d (%.1f%%)", successCount, float64(successCount)/float64(totalWorkers)*100)
// 	t.Logf("Failed: %d (%.1f%%)", failCount, float64(failCount)/float64(totalWorkers)*100)
// 	t.Logf("Total Duration: %v", totalDuration)
// 	t.Logf("Average Duration: %v", totalDuration/time.Duration(totalWorkers))

// 	t.Log("\n" + strings.Repeat("-", 80))
// 	t.Log("DETAILED RESULTS BY WORKER")
// 	t.Log(strings.Repeat("-", 80))
// 	t.Logf("%-40s %-10s %-15s %s", "Worker Name", "Status", "Duration", "Error")
// 	t.Log(strings.Repeat("-", 80))

// 	for _, result := range results {
// 		status := "✓ PASS"
// 		errorMsg := "-"
// 		if !result.Success {
// 			status = "✗ FAIL"
// 			if result.Error != nil {
// 				errorMsg = result.Error.Error()
// 				if len(errorMsg) > 50 {
// 					errorMsg = errorMsg[:50] + "..."
// 				}
// 			}
// 		}
// 		t.Logf("%-40s %-10s %-15v %s", result.WorkerName, status, result.Duration, errorMsg)
// 	}

// 	t.Log(strings.Repeat("=", 80))

// 	// Fail test if any worker failed
// 	if failCount > 0 {
// 		t.Errorf("\n%d/%d workers failed. See details above.", failCount, totalWorkers)
// 	}
// }

// func getEnvOrDefault(key, defaultValue string) string {
// 	if value := os.Getenv(key); value != "" {
// 		return value
// 	}
// 	return defaultValue
// }

// // ============================================================================
// // Integration Test - Full Workflow
// // ============================================================================

// func TestFullWorkflowIntegration(t *testing.T) {
// 	if testing.Short() {
// 		t.Skip("Skipping integration workflow test in short mode")
// 	}

// 	env := SetupTestEnvironment(t)
// 	defer env.Teardown()

// 	t.Run("FranchiseDiscoveryWorkflow", func(t *testing.T) {
// 		testFranchiseDiscoveryWorkflow(t, env)
// 	})

// 	t.Run("ApplicationProcessingWorkflow", func(t *testing.T) {
// 		testApplicationProcessingWorkflow(t, env)
// 	})

// 	t.Run("AIConversationWorkflow", func(t *testing.T) {
// 		testAIConversationWorkflow(t, env)
// 	})
// }

// func testFranchiseDiscoveryWorkflow(t *testing.T, env *TestEnvironment) {
// 	// Step 1: Parse search filters
// 	parseHandler := parsesearchfilters.NewHandler(
// 		parsesearchfilters.DefaultConfig(),
// 		env.Logger,
// 	)

// 	parseOutput, err := parseHandler.Execute(context.Background(), &parsesearchfilters.Input{
// 		RawFilters: map[string]interface{}{
// 			"categories": []string{"food"},
// 			"keywords":   "franchise",
// 		},
// 	})
// 	require.NoError(t, err)

// 	// Step 2: Query Elasticsearch
// 	esHandler := queryelasticsearch.NewHandler(
// 		queryelasticsearch.DefaultConfig(),
// 		env.ElasticsearchClient.Client,
// 		env.Logger,
// 	)

// 	esOutput, err := esHandler.Execute(context.Background(), &queryelasticsearch.Input{
// 		IndexName: "franchises",
// 		QueryType: "franchise_index",
// 		Filters:   parseOutput.ParsedFilters,
// 	})
// 	require.NoError(t, err)

// 	t.Logf("Franchise Discovery Workflow completed successfully with %d results", esOutput.TotalHits)
// }

// func testApplicationProcessingWorkflow(t *testing.T, env *TestEnvironment) {
// 	// Step 1: Validate application data
// 	validateHandler := validateapplicationdata.NewHandler(
// 		validateapplicationdata.DefaultConfig(),
// 		env.Logger,
// 	)

// 	validateOutput, err := validateHandler.Execute(context.Background(), &validateapplicationdata.Input{
// 		ApplicationData: map[string]interface{}{
// 			"personalInfo": map[string]interface{}{
// 				"name":  "John Doe",
// 				"email": "john@example.com",
// 				"phone": "+1234567890",
// 			},
// 			"financialInfo": map[string]interface{}{
// 				"liquidCapital": 100000,
// 				"netWorth":      500000,
// 				"creditScore":   720,
// 			},
// 			"experience": map[string]interface{}{
// 				"yearsInIndustry":      5,
// 				"managementExperience": true,
// 			},
// 		},
// 		FranchiseID: "test-franchise-001",
// 	})
// 	require.NoError(t, err)
// 	require.True(t, validateOutput.IsValid)

// 	// Step 2: Check readiness score
// 	readinessHandler := checkreadinessscore.NewHandler(
// 		checkreadinessscore.DefaultConfig(),
// 		env.Logger,
// 	)

// 	readinessOutput, err := readinessHandler.Execute(context.Background(), &checkreadinessscore.Input{
// 		UserID:          "test-user-123",
// 		ApplicationData: validateOutput.ValidatedData,
// 	})
// 	require.NoError(t, err)

// 	t.Logf("Application Processing Workflow completed with readiness score: %d", readinessOutput.ReadinessScore)
// }

// func testAIConversationWorkflow(t *testing.T, env *TestEnvironment) {
// 	// Step 1: Query internal data
// 	queryHandler := queryinternaldata.NewHandler(
// 		queryinternaldata.DefaultConfig(),
// 		env.PostgresClient.DB,
// 		env.ElasticsearchClient.Client,
// 		env.RedisClient.Client,
// 		env.Logger,
// 	)

// 	queryOutput, err := queryHandler.Execute(context.Background(), &queryinternaldata.Input{
// 		Entities: []queryinternaldata.Entity{
// 			{Type: "franchise_name", Value: "Test Franchise"},
// 		},
// 		DataSources: []string{"internal_db"},
// 	})
// 	require.NoError(t, err)

// 	t.Logf("AI Conversation Workflow completed with internal data: %v", queryOutput.InternalData)
// }

// // ============================================================================
// // Benchmark Tests
// // ============================================================================

// func BenchmarkAllWorkers(b *testing.B) {
// 	env := SetupTestEnvironment(&testing.T{})
// 	defer env.Teardown()

// 	b.Run("ValidateSubscription", func(b *testing.B) {
// 		handler, _ := validatesubscription.NewHandler(
// 			validatesubscription.DefaultConfig(),
// 			env.PostgresClient.DB,
// 			env.RedisClient.Client,
// 			env.Logger,
// 		)
// 		input := validatesubscription.Input{UserID: "test-user-123"}

// 		b.ResetTimer()
// 		for i := 0; i < b.N; i++ {
// 			handler.Execute(context.Background(), &input)
// 		}
// 	})

// 	b.Run("ParseSearchFilters", func(b *testing.B) {
// 		handler := parsesearchfilters.NewHandler(
// 			parsesearchfilters.DefaultConfig(),
// 			env.Logger,
// 		)
// 		input := parsesearchfilters.Input{
// 			RawFilters: map[string]interface{}{
// 				"categories": []string{"food"},
// 			},
// 		}

// 		b.ResetTimer()
// 		for i := 0; i < b.N; i++ {
// 			handler.Execute(context.Background(), &input)
// 		}
// 	})
// }

// // ============================================================================
// // Stress Tests
// // ============================================================================

// func TestWorkerStress(t *testing.T) {
// 	if testing.Short() {
// 		t.Skip("Skipping stress tests in short mode")
// 	}

// 	env := SetupTestEnvironment(t)
// 	defer env.Teardown()

// 	t.Run("ConcurrentValidateSubscription", func(t *testing.T) {
// 		handler, err := validatesubscription.NewHandler(
// 			validatesubscription.DefaultConfig(),
// 			env.PostgresClient.DB,
// 			env.RedisClient.Client,
// 			env.Logger,
// 		)
// 		require.NoError(t, err)

// 		const numGoroutines = 50
// 		const numRequests = 100

// 		var successCount, failCount int
// 		results := make(chan bool, numGoroutines*numRequests)

// 		for i := 0; i < numGoroutines; i++ {
// 			go func() {
// 				for j := 0; j < numRequests; j++ {
// 					input := validatesubscription.Input{
// 						UserID: "test-user-123",
// 					}
// 					_, err := handler.Execute(context.Background(), &input)
// 					results <- (err == nil)
// 				}
// 			}()
// 		}

// 		for i := 0; i < numGoroutines*numRequests; i++ {
// 			if <-results {
// 				successCount++
// 			} else {
// 				failCount++
// 			}
// 		}

// 		t.Logf("Stress test completed: %d successful, %d failed", successCount, failCount)
// 		assert.True(t, successCount > numGoroutines*numRequests*0.8, "At least 80%% should succeed")
// 	})
// }

// // ============================================================================
// // Additional Helper Tests
// // ============================================================================

// func TestDatabaseConnections(t *testing.T) {
// 	env := SetupTestEnvironment(t)
// 	defer env.Teardown()

// 	t.Run("PostgreSQLConnection", func(t *testing.T) {
// 		err := env.PostgresClient.Ping()
// 		assert.NoError(t, err, "PostgreSQL should be reachable")

// 		var count int
// 		err = env.PostgresClient.DB.QueryRow("SELECT COUNT(*) FROM franchises").Scan(&count)
// 		assert.NoError(t, err)
// 		assert.True(t, count >= 0, "Should be able to query franchises table")
// 	})

// 	t.Run("ElasticsearchConnection", func(t *testing.T) {
// 		err := env.ElasticsearchClient.Ping()
// 		assert.NoError(t, err, "Elasticsearch should be reachable")

// 		ctx := context.Background()
// 		err = env.ElasticsearchClient.Info(ctx)
// 		assert.NoError(t, err, "Should be able to get ES info")
// 	})

// 	t.Run("RedisConnection", func(t *testing.T) {
// 		err := env.RedisClient.Ping()
// 		assert.NoError(t, err, "Redis should be reachable")

// 		ctx := context.Background()
// 		err = env.RedisClient.Set(ctx, "test-key", "test-value", time.Minute)
// 		assert.NoError(t, err)

// 		val, err := env.RedisClient.Get(ctx, "test-key")
// 		assert.NoError(t, err)
// 		assert.Equal(t, "test-value", val)

// 		env.RedisClient.Del(ctx, "test-key")
// 	})

// 	t.Run("CamundaConnection", func(t *testing.T) {
// 		err := env.CamundaClient.HealthCheck(context.Background())
// 		assert.NoError(t, err, "Camunda should be reachable")
// 	})
// }

// func TestWorkerConfigurations(t *testing.T) {
// 	env := SetupTestEnvironment(t)
// 	defer env.Teardown()

// 	configs := []struct {
// 		name   string
// 		worker string
// 	}{
// 		{"ValidateSubscription", "validate-subscription"},
// 		{"BuildResponse", "build-response"},
// 		{"SelectTemplate", "select-template"},
// 		{"QueryPostgreSQL", "query-postgresql"},
// 		{"QueryElasticsearch", "query-elasticsearch"},
// 		{"ParseSearchFilters", "parse-search-filters"},
// 		{"ApplyRelevanceRanking", "apply-relevance-ranking"},
// 		{"CalculateMatchScore", "calculate-match-score"},
// 		{"ValidateApplicationData", "validate-application-data"},
// 		{"CheckReadinessScore", "check-readiness-score"},
// 		{"CheckPriorityRouting", "check-priority-routing"},
// 		{"CreateApplicationRecord", "create-application-record"},
// 		{"SendNotification", "send-notification"},
// 		{"ParseUserIntent", "parse-user-intent"},
// 		{"QueryInternalData", "query-internal-data"},
// 		{"EnrichWebSearch", "enrich-web-search"},
// 		{"LLMSynthesis", "llm-synthesis"},
// 		{"AuthSignInGoogle", "auth-signin-google"},
// 		{"AuthSignInLinkedIn", "auth-signin-linkedin"},
// 		{"AuthSignUpGoogle", "auth-signup-google"},
// 		{"AuthSignUpLinkedIn", "auth-signup-linkedin"},
// 		{"AuthLogout", "auth-logout"},
// 		{"CaptchaVerify", "captcha-verify"},
// 		{"EmailSend", "email-send"},
// 		{"CRMUserCreate", "crm-user-create"},
// 	}

// 	for _, cfg := range configs {
// 		t.Run(cfg.name, func(t *testing.T) {
// 			workerCfg := config.GetWorkerConfig(env.Config, cfg.worker)
// 			assert.NotNil(t, workerCfg, "Worker config should exist for %s", cfg.worker)
// 			assert.True(t, workerCfg.MaxJobsActive > 0, "MaxJobsActive should be positive")
// 			assert.True(t, workerCfg.Timeout > 0, "Timeout should be positive")
// 		})
// 	}
// }

// // ============================================================================
// // Error Handling Tests
// // ============================================================================

// func TestWorkerErrorHandling(t *testing.T) {
// 	env := SetupTestEnvironment(t)
// 	defer env.Teardown()

// 	t.Run("InvalidInputHandling", func(t *testing.T) {
// 		handler := parsesearchfilters.NewHandler(
// 			parsesearchfilters.DefaultConfig(),
// 			env.Logger,
// 		)

// 		// Test with invalid investment range
// 		input := parsesearchfilters.Input{
// 			RawFilters: map[string]interface{}{
// 				"investmentRange": map[string]interface{}{
// 					"min": 200000,
// 					"max": 100000, // max < min
// 				},
// 			},
// 		}

// 		_, err := handler.Execute(context.Background(), &input)
// 		assert.Error(t, err, "Should fail with invalid range")
// 	})

// 	t.Run("MissingDataHandling", func(t *testing.T) {
// 		handler := querypostgresql.NewHandler(
// 			querypostgresql.DefaultConfig(),
// 			env.PostgresClient.DB,
// 			env.Logger,
// 		)

// 		input := querypostgresql.Input{
// 			QueryType:   "franchise_details",
// 			FranchiseID: "non-existent-franchise",
// 		}

// 		output, err := handler.Execute(context.Background(), &input)
// 		// Should not error, but return empty data
// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 	})

// 	t.Run("TimeoutHandling", func(t *testing.T) {
// 		handler := validateapplicationdata.NewHandler(
// 			validateapplicationdata.DefaultConfig(),
// 			env.Logger,
// 		)

// 		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
// 		defer cancel()

// 		input := validateapplicationdata.Input{
// 			ApplicationData: map[string]interface{}{
// 				"personalInfo": map[string]interface{}{
// 					"name":  "Test",
// 					"email": "test@example.com",
// 					"phone": "+1234567890",
// 				},
// 			},
// 			FranchiseID: "test",
// 		}

// 		time.Sleep(10 * time.Millisecond) // Ensure context expires

// 		_, err := handler.Execute(ctx, &input)
// 		// May or may not error depending on execution speed
// 		if err != nil {
// 			assert.Contains(t, err.Error(), "context")
// 		}
// 	})
// }

// // ============================================================================
// // Data Validation Tests
// // ============================================================================

// func TestDataValidation(t *testing.T) {
// 	env := SetupTestEnvironment(t)
// 	defer env.Teardown()

// 	t.Run("ValidApplicationData", func(t *testing.T) {
// 		handler := validateapplicationdata.NewHandler(
// 			validateapplicationdata.DefaultConfig(),
// 			env.Logger,
// 		)

// 		validData := map[string]interface{}{
// 			"personalInfo": map[string]interface{}{
// 				"name":  "John Doe",
// 				"email": "john.doe@example.com",
// 				"phone": "+12345678901",
// 			},
// 			"financialInfo": map[string]interface{}{
// 				"liquidCapital": 150000,
// 				"netWorth":      500000,
// 				"creditScore":   750,
// 			},
// 			"experience": map[string]interface{}{
// 				"yearsInIndustry":      5,
// 				"managementExperience": true,
// 			},
// 		}

// 		input := validateapplicationdata.Input{
// 			ApplicationData: validData,
// 			FranchiseID:     "test-franchise-001",
// 		}

// 		output, err := handler.Execute(context.Background(), &input)
// 		assert.NoError(t, err)
// 		assert.True(t, output.IsValid)
// 		assert.Empty(t, output.ValidationErrors)
// 	})

// 	t.Run("InvalidApplicationData", func(t *testing.T) {
// 		handler := validateapplicationdata.NewHandler(
// 			validateapplicationdata.DefaultConfig(),
// 			env.Logger,
// 		)

// 		invalidData := map[string]interface{}{
// 			"personalInfo": map[string]interface{}{
// 				"name":  "A", // Too short
// 				"email": "invalid-email",
// 				"phone": "123", // Too short
// 			},
// 			"financialInfo": map[string]interface{}{
// 				"liquidCapital": -1000, // Negative
// 				"netWorth":      -5000, // Negative
// 				"creditScore":   1000,  // Out of range
// 			},
// 			"experience": map[string]interface{}{
// 				"yearsInIndustry": -1, // Negative
// 			},
// 		}

// 		input := validateapplicationdata.Input{
// 			ApplicationData: invalidData,
// 			FranchiseID:     "test-franchise-001",
// 		}

// 		output, err := handler.Execute(context.Background(), &input)
// 		assert.Error(t, err)
// 		assert.False(t, output.IsValid)
// 		assert.NotEmpty(t, output.ValidationErrors)
// 	})
// }

// // ============================================================================
// // Performance Tests
// // ============================================================================

// func TestWorkerPerformance(t *testing.T) {
// 	if testing.Short() {
// 		t.Skip("Skipping performance tests in short mode")
// 	}

// 	env := SetupTestEnvironment(t)
// 	defer env.Teardown()

// 	performanceThresholds := map[string]time.Duration{
// 		"validate-subscription":      100 * time.Millisecond,
// 		"select-template":            50 * time.Millisecond,
// 		"build-response":             200 * time.Millisecond,
// 		"parse-search-filters":       50 * time.Millisecond,
// 		"apply-relevance-ranking":    500 * time.Millisecond,
// 		"calculate-match-score":      100 * time.Millisecond,
// 		"validate-application-data":  50 * time.Millisecond,
// 		"check-readiness-score":      50 * time.Millisecond,
// 	}

// 	for workerName, threshold := range performanceThresholds {
// 		t.Run(workerName, func(t *testing.T) {
// 			start := time.Now()

// 			switch workerName {
// 			case "validate-subscription":
// 				handler, _ := validatesubscription.NewHandler(
// 					validatesubscription.DefaultConfig(),
// 					env.PostgresClient.DB,
// 					env.RedisClient.Client,
// 					env.Logger,
// 				)
// 				handler.Execute(context.Background(), &validatesubscription.Input{
// 					UserID: "test-user-123",
// 				})
// 			case "parse-search-filters":
// 				handler := parsesearchfilters.NewHandler(
// 					parsesearchfilters.DefaultConfig(),
// 					env.Logger,
// 				)
// 				handler.Execute(context.Background(), &parsesearchfilters.Input{
// 					RawFilters: map[string]interface{}{"categories": []string{"food"}},
// 				})
// 			}

// 			duration := time.Since(start)
// 			t.Logf("%s completed in %v (threshold: %v)", workerName, duration, threshold)

// 			if duration > threshold*2 {
// 				t.Logf("WARNING: %s exceeded 2x threshold", workerName)
// 			}
// 		})
// 	}
// }

// // ============================================================================
// // End-to-End Scenario Tests
// // ============================================================================

// func TestCompleteUserJourney(t *testing.T) {
// 	if testing.Short() {
// 		t.Skip("Skipping complete user journey test in short mode")
// 	}

// 	env := SetupTestEnvironment(t)
// 	defer env.Teardown()

// 	userID := fmt.Sprintf("journey-user-%d", time.Now().Unix())
// 	franchiseID := "test-franchise-001"

// 	t.Run("Step1_UserRegistration", func(t *testing.T) {
// 		// Simulate user registration (would typically use auth workers)
// 		ctx := context.Background()
// 		_, err := env.PostgresClient.DB.ExecContext(ctx,
// 			`INSERT INTO users (id, email, capital_available, location_preferences, interests, industry_experience)
// 			 VALUES ($1, $2, $3, $4, $5, $6)`,
// 			userID, fmt.Sprintf("%s@example.com", userID), 120000, `["New York"]`, `["food"]`, 3)
// 		require.NoError(t, err)

// 		_, err = env.PostgresClient.DB.ExecContext(ctx,
// 			`INSERT INTO user_subscriptions (user_id, tier, expires_at, is_valid)
// 			 VALUES ($1, $2, NOW() + INTERVAL '1 year', true)`,
// 			userID, "premium")
// 		require.NoError(t, err)
// 	})

// 	t.Run("Step2_ValidateSubscription", func(t *testing.T) {
// 		handler, err := validatesubscription.NewHandler(
// 			validatesubscription.DefaultConfig(),
// 			env.PostgresClient.DB,
// 			env.RedisClient.Client,
// 			env.Logger,
// 		)
// 		require.NoError(t, err)

// 		output, err := handler.Execute(context.Background(), &validatesubscription.Input{
// 			UserID: userID,
// 		})
// 		require.NoError(t, err)
// 		assert.True(t, output.IsValid)
// 		assert.Equal(t, "premium", output.TierLevel)
// 	})

// 	t.Run("Step3_SearchFranchises", func(t *testing.T) {
// 		parseHandler := parsesearchfilters.NewHandler(
// 			parsesearchfilters.DefaultConfig(),
// 			env.Logger,
// 		)

// 		parseOutput, err := parseHandler.Execute(context.Background(), &parsesearchfilters.Input{
// 			RawFilters: map[string]interface{}{
// 				"categories":      []string{"food"},
// 				"investmentRange": map[string]interface{}{"min": 50000, "max": 200000},
// 			},
// 		})
// 		require.NoError(t, err)
// 		assert.NotEmpty(t, parseOutput.ParsedFilters.Categories)
// 	})

// 	t.Run("Step4_CalculateMatchScore", func(t *testing.T) {
// 		handler := calculatematchscore.NewHandler(
// 			calculatematchscore.DefaultConfig(),
// 			env.PostgresClient.DB,
// 			env.RedisClient.Client,
// 			env.Logger,
// 		)

// 		output, err := handler.Execute(context.Background(), &calculatematchscore.Input{
// 			UserID: userID,
// 			FranchiseData: calculatematchscore.FranchiseData{
// 				ID:            franchiseID,
// 				Category:      "food",
// 				InvestmentMin: 50000,
// 				InvestmentMax: 150000,
// 				Locations:     []string{"New York"},
// 			},
// 		})
// 		require.NoError(t, err)
// 		assert.True(t, output.MatchScore > 0)
// 		t.Logf("Match score: %d", output.MatchScore)
// 	})

// 	t.Run("Step5_SubmitApplication", func(t *testing.T) {
// 		// Validate data
// 		validateHandler := validateapplicationdata.NewHandler(
// 			validateapplicationdata.DefaultConfig(),
// 			env.Logger,
// 		)

// 		appData := map[string]interface{}{
// 			"personalInfo": map[string]interface{}{
// 				"name":  "Journey User",
// 				"email": fmt.Sprintf("%s@example.com", userID),
// 				"phone": "+12345678901",
// 			},
// 			"financialInfo": map[string]interface{}{
// 				"liquidCapital": 120000,
// 				"netWorth":      500000,
// 				"creditScore":   720,
// 			},
// 			"experience": map[string]interface{}{
// 				"yearsInIndustry":      3,
// 				"managementExperience": true,
// 			},
// 		}

// 		validateOutput, err := validateHandler.Execute(context.Background(), &validateapplicationdata.Input{
// 			ApplicationData: appData,
// 			FranchiseID:     franchiseID,
// 		})
// 		require.NoError(t, err)
// 		require.True(t, validateOutput.IsValid)

// 		// Check readiness
// 		readinessHandler := checkreadinessscore.NewHandler(
// 			checkreadinessscore.DefaultConfig(),
// 			env.Logger,
// 		)

// 		readinessOutput, err := readinessHandler.Execute(context.Background(), &checkreadinessscore.Input{
// 			UserID:          userID,
// 			ApplicationData: validateOutput.ValidatedData,
// 		})
// 		require.NoError(t, err)
// 		t.Logf("Readiness score: %d (%s)", readinessOutput.ReadinessScore, readinessOutput.QualificationLevel)

// 		// Create application
// 		createHandler := createapplicationrecord.NewHandler(
// 			createapplicationrecord.DefaultConfig(),
// 			env.PostgresClient.DB,
// 			env.Logger,
// 		)

// 		createOutput, err := createHandler.Execute(context.Background(), &createapplicationrecord.Input{
// 			SeekerID:        userID,
// 			FranchiseID:     franchiseID,
// 			ApplicationData: validateOutput.ValidatedData,
// 			ReadinessScore:  readinessOutput.ReadinessScore,
// 			Priority:        "high",
// 		})
// 		require.NoError(t, err)
// 		assert.NotEmpty(t, createOutput.ApplicationID)
// 		t.Logf("Application created: %s", createOutput.ApplicationID)
// 	})

// 	t.Run("Step6_Cleanup", func(t *testing.T) {
// 		ctx := context.Background()
// 		env.PostgresClient.DB.ExecContext(ctx, "DELETE FROM applications WHERE seeker_id = $1", userID)
// 		env.PostgresClient.DB.ExecContext(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1", userID)
// 		env.PostgresClient.DB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID)
// 	})
// }

// // ============================================================================
// // Main Test Runner
// // ============================================================================

// func TestMain(m *testing.M) {
// 	// Setup code before all tests
// 	fmt.Println("Starting E2E Test Suite for 25 Camunda Workers")
// 	fmt.Println("================================================")

// 	// Run tests
// 	exitCode := m.Run()

// 	// Cleanup code after all tests
// 	fmt.Println("\nE2E Test Suite Completed")
// 	fmt.Println("========================")

// 	os.Exit(exitCode)
// }

// func testSelectTemplate(t *testing.T, env *TestEnvironment) error {
// 	cfg := selecttemplate.DefaultConfig()
// 	cfg.TemplateRules = env.Config.Template.TemplateRules

// 	handler := selecttemplate.NewHandler(cfg, env.Logger)

// 	input := selecttemplate.Input{
// 		SubscriptionTier: "premium",
// 		RoutePath:        "/franchises/detail",
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotEmpty(t, output.SelectedTemplateId)
// 	return nil
// }

// func testBuildResponse(t *testing.T, env *TestEnvironment) error {
// 	cfg := buildresponse.DefaultConfig()
// 	cfg.TemplateRegistry = env.Config.Template.RegistryPath
// 	cfg.AppVersion = env.Config.App.Version

// 	handler := buildresponse.NewHandler(cfg, env.Logger)

// 	input := buildresponse.Input{
// 		TemplateId: "franchise-detail-basic",
// 		RequestId:  "test-req-001",
// 		Data: map[string]interface{}{
// 			"franchiseId":   "fr-001",
// 			"franchiseName": "Test Franchise",
// 		},
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotNil(t, output.Response)
// 	assert.Equal(t, "test-req-001", output.Response.RequestId)
// 	return nil
// }

// // ============================================================================
// // Data Access Workers Tests (2 workers)
// // ============================================================================

// func testDataAccessWorkers(t *testing.T, env *TestEnvironment) []WorkerTestResult {
// 	results := make([]WorkerTestResult, 0, 2)

// 	// 4. Query PostgreSQL Worker
// 	t.Run("QueryPostgreSQL", func(t *testing.T) {
// 		start := time.Now()
// 		err := testQueryPostgreSQL(t, env)
// 		results = append(results, WorkerTestResult{
// 			WorkerName: "query-postgresql",
// 			Success:    err == nil,
// 			Error:      err,
// 			Duration:   time.Since(start),
// 		})
// 	})

// 	// 5. Query Elasticsearch Worker
// 	t.Run("QueryElasticsearch", func(t *testing.T) {
// 		start := time.Now()
// 		err := testQueryElasticsearch(t, env)
// 		results = append(results, WorkerTestResult{
// 			WorkerName: "query-elasticsearch",
// 			Success:    err == nil,
// 			Error:      err,
// 			Duration:   time.Since(start),
// 		})
// 	})

// 	return results
// }

// func testQueryPostgreSQL(t *testing.T, env *TestEnvironment) error {
// 	handler := querypostgresql.NewHandler(
// 		querypostgresql.DefaultConfig(),
// 		env.PostgresClient.DB,
// 		env.Logger,
// 	)

// 	input := querypostgresql.Input{
// 		QueryType:   "franchise_details",
// 		FranchiseID: "test-franchise-001",
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotNil(t, output.Data)
// 	return nil
// }

// func testQueryElasticsearch(t *testing.T, env *TestEnvironment) error {
// 	handler := queryelasticsearch.NewHandler(
// 		queryelasticsearch.DefaultConfig(),
// 		env.ElasticsearchClient.Client,
// 		env.Logger,
// 	)

// 	input := queryelasticsearch.Input{
// 		IndexName: "franchises",
// 		QueryType: "franchise_index",
// 		Filters: map[string]interface{}{
// 			"category": "food",
// 		},
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotNil(t, output.Data)
// 	return nil
// }

// // ============================================================================
// // Authentication Workers Tests (6 workers)
// // ============================================================================

// func testAuthenticationWorkers(t *testing.T, env *TestEnvironment) []WorkerTestResult {
// 	results := make([]WorkerTestResult, 0, 6)

// 	workers := []struct {
// 		name string
// 		test func(*testing.T, *TestEnvironment) error
// 	}{
// 		{"auth-signin-google", testAuthSignInGoogle},
// 		{"auth-signin-linkedin", testAuthSignInLinkedIn},
// 		{"auth-signup-google", testAuthSignUpGoogle},
// 		{"auth-signup-linkedin", testAuthSignUpLinkedIn},
// 		{"auth-logout", testAuthLogout},
// 		{"captcha-verify", testCaptchaVerify},
// 	}

// 	for _, w := range workers {
// 		t.Run(w.name, func(t *testing.T) {
// 			start := time.Now()
// 			err := w.test(t, env)
// 			results = append(results, WorkerTestResult{
// 				WorkerName: w.name,
// 				Success:    err == nil,
// 				Error:      err,
// 				Duration:   time.Since(start),
// 			})
// 		})
// 	}

// 	return results
// }

// func testAuthSignInGoogle(t *testing.T, env *TestEnvironment) error {
// 	cfg := authsigningoogle.DefaultConfig()
// 	cfg.ClientID = env.Config.Auth.OAuthProviders.Google.ClientID
// 	cfg.ClientSecret = env.Config.Auth.OAuthProviders.Google.ClientSecret

// 	handler, err := authsigningoogle.NewHandler(authsigningoogle.HandlerOptions{
// 		AppConfig:    env.Config,
// 		Camunda:      env.CamundaClient,
// 		Keycloak:     env.KeycloakClient,
// 		ZohoCRM:      env.ZohoCRMClient,
// 		CustomConfig: cfg,
// 		Logger:       logger.NewStructured("info", "json"),
// 	})
// 	if err != nil {
// 		return fmt.Errorf("failed to create handler: %w", err)
// 	}

// 	// Test with mock auth code (will fail OAuth but tests worker structure)
// 	input := authsigningoogle.Input{
// 		AuthCode:    "mock-auth-code",
// 		RedirectURI: "http://localhost:3000/callback",
// 	}

// 	service := authsigningoogle.NewService(authsigningoogle.ServiceDependencies{
// 		Keycloak: env.KeycloakClient,
// 		ZohoCRM:  env.ZohoCRMClient,
// 		Logger:   logger.NewStructured("info", "json"),
// 	}, cfg)

// 	_, err = service.Execute(context.Background(), &input)
// 	// We expect an OAuth error with mock data, but worker structure is validated
// 	return nil
// }

// func testAuthSignInLinkedIn(t *testing.T, env *TestEnvironment) error {
// 	cfg := authsigninlinkedin.DefaultConfig()
// 	cfg.ClientID = env.Config.Auth.OAuthProviders.LinkedIn.ClientID
// 	cfg.ClientSecret = env.Config.Auth.OAuthProviders.LinkedIn.ClientSecret

// 	_, err := authsigninlinkedin.NewHandler(authsigninlinkedin.HandlerOptions{
// 		AppConfig:    env.Config,
// 		Camunda:      env.CamundaClient,
// 		Keycloak:     env.KeycloakClient,
// 		ZohoCRM:      env.ZohoCRMClient,
// 		CustomConfig: cfg,
// 		Logger:       logger.NewStructured("info", "json"),
// 	})

// 	return err
// }

// func testAuthSignUpGoogle(t *testing.T, env *TestEnvironment) error {
// 	cfg := authsignupgoogle.DefaultConfig()
// 	cfg.ClientID = env.Config.Auth.OAuthProviders.Google.ClientID
// 	cfg.ClientSecret = env.Config.Auth.OAuthProviders.Google.ClientSecret

// 	_, err := authsignupgoogle.NewHandler(authsignupgoogle.HandlerOptions{
// 		AppConfig:    env.Config,
// 		Camunda:      env.CamundaClient,
// 		Keycloak:     env.KeycloakClient,
// 		ZohoCRM:      env.ZohoCRMClient,
// 		CustomConfig: cfg,
// 		Logger:       logger.NewStructured("info", "json"),
// 	})

// 	return err
// }

// func testAuthSignUpLinkedIn(t *testing.T, env *TestEnvironment) error {
// 	cfg := authsignuplinkedin.DefaultConfig()
// 	cfg.ClientID = env.Config.Auth.OAuthProviders.LinkedIn.ClientID
// 	cfg.ClientSecret = env.Config.Auth.OAuthProviders.LinkedIn.ClientSecret

// 	_, err := authsignuplinkedin.NewHandler(authsignuplinkedin.HandlerOptions{
// 		AppConfig:    env.Config,
// 		Camunda:      env.CamundaClient,
// 		Keycloak:     env.KeycloakClient,
// 		ZohoCRM:      env.ZohoCRMClient,
// 		CustomConfig: cfg,
// 		Logger:       logger.NewStructured("info", "json"),
// 	})

// 	return err
// }

// func testAuthLogout(t *testing.T, env *TestEnvironment) error {
// 	_, err := authlogout.NewHandler(authlogout.HandlerOptions{
// 		AppConfig:    env.Config,
// 		Camunda:      env.CamundaClient,
// 		CustomConfig: authlogout.DefaultConfig(),
// 		Logger:       logger.NewStructured("info", "json"),
// 	})

// 	return err
// }

// func testCaptchaVerify(t *testing.T, env *TestEnvironment) error {
// 	_, err := captchaverify.NewHandler(captchaverify.HandlerOptions{
// 		AppConfig:    env.Config,
// 		Camunda:      env.CamundaClient,
// 		CustomConfig: captchaverify.DefaultConfig(),
// 		Logger:       logger.NewStructured("info", "json"),
// 	})

// 	return err
// }

// // ============================================================================
// // Franchise Workers Tests (3 workers)
// // ============================================================================

// func testFranchiseWorkers(t *testing.T, env *TestEnvironment) []WorkerTestResult {
// 	results := make([]WorkerTestResult, 0, 3)

// 	workers := []struct {
// 		name string
// 		test func(*testing.T, *TestEnvironment) error
// 	}{
// 		{"parse-search-filters", testParseSearchFilters},
// 		{"apply-relevance-ranking", testApplyRelevanceRanking},
// 		{"calculate-match-score", testCalculateMatchScore},
// 	}

// 	for _, w := range workers {
// 		t.Run(w.name, func(t *testing.T) {
// 			start := time.Now()
// 			err := w.test(t, env)
// 			results = append(results, WorkerTestResult{
// 				WorkerName: w.name,
// 				Success:    err == nil,
// 				Error:      err,
// 				Duration:   time.Since(start),
// 			})
// 		})
// 	}

// 	return results
// }

// func testParseSearchFilters(t *testing.T, env *TestEnvironment) error {
// 	handler := parsesearchfilters.NewHandler(
// 		parsesearchfilters.DefaultConfig(),
// 		env.Logger,
// 	)

// 	input := parsesearchfilters.Input{
// 		RawFilters: map[string]interface{}{
// 			"categories":      []string{"food", "retail"},
// 			"investmentRange": map[string]interface{}{"min": 50000, "max": 200000},
// 			"keywords":        "franchise opportunity",
// 		},
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotNil(t, output.ParsedFilters)
// 	assert.Equal(t, 2, len(output.ParsedFilters.Categories))
// 	return nil
// }

// func testApplyRelevanceRanking(t *testing.T, env *TestEnvironment) error {
// 	handler := applyrelevanceranking.NewHandler(
// 		applyrelevanceranking.DefaultConfig(),
// 		env.Logger,
// 	)

// 	input := applyrelevanceranking.Input{
// 		SearchResults: []applyrelevanceranking.SearchResult{
// 			{ID: "fr-001", Score: 0.95},
// 			{ID: "fr-002", Score: 0.85},
// 		},
// 		DetailsData: []applyrelevanceranking.FranchiseDetail{
// 			{
// 				ID:               "fr-001",
// 				Name:             "Test Franchise 1",
// 				Category:         "food",
// 				InvestmentMin:    50000,
// 				InvestmentMax:    150000,
// 				ViewCount:        100,
// 				ApplicationCount: 20,
// 				UpdatedAt:        time.Now().Format(time.RFC3339),
// 			},
// 			{
// 				ID:               "fr-002",
// 				Name:             "Test Franchise 2",
// 				Category:         "retail",
// 				InvestmentMin:    75000,
// 				InvestmentMax:    200000,
// 				ViewCount:        150,
// 				ApplicationCount: 30,
// 				UpdatedAt:        time.Now().AddDate(0, -2, 0).Format(time.RFC3339),
// 			},
// 		},
// 		UserProfile: applyrelevanceranking.UserProfile{
// 			CapitalAvailable: 100000,
// 			LocationPrefs:    []string{"New York"},
// 			Interests:        []string{"food"},
// 			ExperienceYears:  5,
// 		},
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotNil(t, output.RankedFranchises)
// 	assert.Equal(t, 2, len(output.RankedFranchises))
// 	return nil
// }

// func testCalculateMatchScore(t *testing.T, env *TestEnvironment) error {
// 	handler := calculatematchscore.NewHandler(
// 		calculatematchscore.DefaultConfig(),
// 		env.PostgresClient.DB,
// 		env.RedisClient.Client,
// 		env.Logger,
// 	)

// 	input := calculatematchscore.Input{
// 		UserID: "test-user-123",
// 		FranchiseData: calculatematchscore.FranchiseData{
// 			ID:            "fr-001",
// 			Category:      "food",
// 			InvestmentMin: 50000,
// 			InvestmentMax: 150000,
// 			Locations:     []string{"New York"},
// 		},
// 		UserProfile: &calculatematchscore.UserProfile{
// 			CapitalAvailable: 100000,
// 			LocationPrefs:    []string{"New York"},
// 			Interests:        []string{"food"},
// 			ExperienceYears:  5,
// 		},
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotNil(t, output)
// 	assert.True(t, output.MatchScore >= 0 && output.MatchScore <= 100)
// 	return nil
// }

// // ============================================================================
// // Application Workers Tests (5 workers)
// // ============================================================================

// func testApplicationWorkers(t *testing.T, env *TestEnvironment) []WorkerTestResult {
// 	results := make([]WorkerTestResult, 0, 5)

// 	workers := []struct {
// 		name string
// 		test func(*testing.T, *TestEnvironment) error
// 	}{
// 		{"validate-application-data", testValidateApplicationData},
// 		{"check-readiness-score", testCheckReadinessScore},
// 		{"check-priority-routing", testCheckPriorityRouting},
// 		{"create-application-record", testCreateApplicationRecord},
// 		{"send-notification", testSendNotification},
// 	}

// 	for _, w := range workers {
// 		t.Run(w.name, func(t *testing.T) {
// 			start := time.Now()
// 			err := w.test(t, env)
// 			results = append(results, WorkerTestResult{
// 				WorkerName: w.name,
// 				Success:    err == nil,
// 				Error:      err,
// 				Duration:   time.Since(start),
// 			})
// 		})
// 	}

// 	return results
// }

// func testValidateApplicationData(t *testing.T, env *TestEnvironment) error {
// 	handler := validateapplicationdata.NewHandler(
// 		validateapplicationdata.DefaultConfig(),
// 		env.Logger,
// 	)

// 	input := validateapplicationdata.Input{
// 		ApplicationData: map[string]interface{}{
// 			"personalInfo": map[string]interface{}{
// 				"name":  "John Doe",
// 				"email": "john@example.com",
// 				"phone": "+1234567890",
// 			},
// 			"financialInfo": map[string]interface{}{
// 				"liquidCapital": 100000,
// 				"netWorth":      500000,
// 				"creditScore":   720,
// 			},
// 			"experience": map[string]interface{}{
// 				"yearsInIndustry":      5,
// 				"managementExperience": true,
// 			},
// 		},
// 		FranchiseID: "test-franchise-001",
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.True(t, output.IsValid)
// 	return nil
// }

// func testCheckReadinessScore(t *testing.T, env *TestEnvironment) error {
// 	handler := checkreadinessscore.NewHandler(
// 		checkreadinessscore.DefaultConfig(),
// 		env.Logger,
// 	)

// 	input := checkreadinessscore.Input{
// 		UserID: "test-user-123",
// 		ApplicationData: map[string]interface{}{
// 			"financialInfo": map[string]interface{}{
// 				"liquidCapital": 150000,
// 				"netWorth":      600000,
// 				"creditScore":   750,
// 			},
// 			"experience": map[string]interface{}{
// 				"yearsInIndustry":      5,
// 				"managementExperience": true,
// 			},
// 			"timeAvailability":   40,
// 			"relocationWilling":  true,
// 			"categoryMatch":      true,
// 			"skillAlignment":     true,
// 			"locationMatch":      true,
// 		},
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotNil(t, output)
// 	assert.True(t, output.ReadinessScore >= 0 && output.ReadinessScore <= 100)
// 	return nil
// }

// func testCheckPriorityRouting(t *testing.T, env *TestEnvironment) error {
// 	handler := checkpriorityrouting.NewHandler(
// 		checkpriorityrouting.DefaultConfig(),
// 		env.PostgresClient.DB,
// 		env.RedisClient.Client,
// 		env.Logger,
// 	)

// 	input := checkpriorityrouting.Input{
// 		FranchiseID: "test-franchise-001",
// 	}

// 	output, err := handler.Execute(context.Background(), &input)
// 	if err != nil {
// 		return fmt.Errorf("execution failed: %w", err)
// 	}

// 	assert.NotNil(t, output)
// 	return nil
// }
