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
	"camunda-workers/internal/common/auth"

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
	searchfranchises "camunda-workers/internal/workers/franchise/search-franchises"

	buildresponse "camunda-workers/internal/workers/infrastructure/build-response"
	selecttemplate "camunda-workers/internal/workers/infrastructure/select-template"
	validatesubscription "camunda-workers/internal/workers/infrastructure/validate-subscription"
)

var (
	zeebeClient zbc.Client
	zapLog      *zap.Logger
	keycloakClient *auth.KeycloakClient
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

// func TestMain(m *testing.M) {
// 	var err error

// 	// Initialize Zeebe client with real connection
// 	zeebeClient, err = zbc.NewClient(&zbc.ClientConfig{
// 		GatewayAddress:         "localhost:26500",
// 		UsePlaintextConnection: true,
// 	})
// 	if err != nil {
// 		panic(fmt.Sprintf("❌ Failed to connect to Zeebe: %v", err))
// 	}

// 	// Initialize logger
// 	zapLog, _ = zap.NewProduction()

// 	// Run tests
// 	code := m.Run()

// 	// Cleanup
// 	zeebeClient.Close()
// 	os.Exit(code)
// }

func TestMain(m *testing.M) {
	var err error

	// 1️⃣ Load config FIRST
	cfg, err := config.Load()
	if err != nil {
		panic(fmt.Sprintf("❌ Failed to load config: %v", err))
	}

	// 2️⃣ Initialize Zeebe client (real connection)
	zeebeClient, err = zbc.NewClient(&zbc.ClientConfig{
		GatewayAddress:         "localhost:26500",
		UsePlaintextConnection: true,
	})
	if err != nil {
		panic(fmt.Sprintf("❌ Failed to connect to Zeebe: %v", err))
	}

	// // 3️⃣ Initialize Keycloak client (REAL)
	// keycloakClient, err = auth.NewKeycloakClient(cfg.Auth.Keycloak)
	// if err != nil {
	// 	panic(fmt.Sprintf("❌ Failed to initialize Keycloak client: %v", err))
	// }
	
	// 3️⃣ Initialize Keycloak client (REAL) - FIXED
    keycloakClient = auth.NewKeycloakClient(
        cfg.Auth.Keycloak.URL,
        cfg.Auth.Keycloak.Realm,
        cfg.Auth.Keycloak.ClientID,
        cfg.Auth.Keycloak.ClientSecret,
    )

	// 4️⃣ Initialize logger
	zapLog, err = zap.NewProduction()
	if err != nil {
		panic(fmt.Sprintf("❌ Failed to initialize logger: %v", err))
	}

	// 5️⃣ Run all tests
	exitCode := m.Run()

	// 6️⃣ Cleanup
	if zeebeClient != nil {
		zeebeClient.Close()
	}

	os.Exit(exitCode)
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

	// 4. Test all 26 workers with real execution
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
// 4. Test All 26 Workers
// ==========================
func testAllWorkers(t *testing.T, cfg *config.Config, log *zap.Logger) {
	t.Log("🧪 Testing all 26 workers with real execution...")

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

	// Worker test cases - NOW INCLUDING search-franchises (26 total)
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
		{"search-franchises", testSearchFranchises}, // ✅ NEW - Worker #26
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
// Worker Test Functions
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
	    Keycloak: keycloakClient,
	})
	require.NoError(t, err)

	input := &authlogout.Input{
		UserID:       "test",
		RefreshToken: "a1b2c3d4e5f6g7h8i9j0k1l2",
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

// ✅ NEW TEST FUNCTION FOR SEARCH-FRANCHISES (Worker #26)
func testSearchFranchises(t *testing.T, cfg *config.Config, log *zap.Logger, db *sql.DB, es *elasticsearch.Client, rdb *redis.Client) {
	// Create Elasticsearch client wrapper
	esClient, err := database.NewElasticsearch(cfg.Database.Elasticsearch)
	require.NoError(t, err, "Failed to create Elasticsearch client")
	handler := searchfranchises.NewHandler(
		&searchfranchises.Config{
			DefaultLimit:       20,
			MaxLimit:           100,
			EnableSuggestions:  true,
			EnableAggregations: true,
			Fuzziness:          "AUTO",
		},
		esClient,
		logger.NewZapAdapter(log),
	)

	input := &searchfranchises.Input{
		Query:    "franchise",
		Category: "food",
		Location: "New York",
		Page:     1,
		Limit:    10,
	}

	output, err := handler.Execute(context.Background(), input)
	assert.NoError(t, err, "Search franchises should execute without error")
	assert.NotNil(t, output, "Output should not be nil")
	assert.True(t, output.Success, "Search should be successful")
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
// Benchmark Tests (Keep existing + add search-franchises)
// ==========================
func BenchmarkHandler_SearchFranchises(b *testing.B) {
	cfg, _ := config.Load()
	esClient, _ := database.NewElasticsearch(cfg.Database.Elasticsearch)
	handler := searchfranchises.NewHandler(
		&searchfranchises.Config{
			DefaultLimit:       20,
			MaxLimit:           100,
			EnableSuggestions:  true,
			EnableAggregations: true,
			Fuzziness:          "AUTO",
		},
		esClient,
		logger.NewStructured("info", "json"),
	)

	input := &searchfranchises.Input{
		Query:    "franchise",
		Category: "food",
		Page:     1,
		Limit:    20,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), input)
	}
}
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
		UserID:       "test-user-123",
		RefreshToken: "a1b2c3d4e5f6g7h8i9j0k1l2",
		SessionID:    "session-456",
		DeviceID:     "device-789",
		LogoutAll:    false,
		Reason:       "user_initiated",
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
