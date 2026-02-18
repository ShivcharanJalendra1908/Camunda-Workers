package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/camunda/zeebe/clients/go/v8/pkg/zbc"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"camunda-workers/internal/api/handlers"
	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/circuitbreaker"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/idempotency"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/observability"
	"camunda-workers/pkg/registry"

	// Infrastructure Workers (5 with template-driven)
	br "camunda-workers/internal/workers/infrastructure/build-response"
	st "camunda-workers/internal/workers/infrastructure/select-template"
	sar "camunda-workers/internal/workers/infrastructure/send-api-response"
	td "camunda-workers/internal/workers/infrastructure/template-driven"
	vs "camunda-workers/internal/workers/infrastructure/validate-subscription"

	// Data Access Workers (4)
	esindexer "camunda-workers/internal/workers/data-access/franchise-es-indexer"
	franchisepostgres "camunda-workers/internal/workers/data-access/franchise-postgres"
	qe "camunda-workers/internal/workers/data-access/query-elasticsearch"
	qp "camunda-workers/internal/workers/data-access/query-postgresql"

	// Business Logic Workers (4 from franchise + 5 from application = 9)
	arr "camunda-workers/internal/workers/franchise/apply-relevance-ranking"
	cms "camunda-workers/internal/workers/franchise/calculate-match-score"
	psf "camunda-workers/internal/workers/franchise/parse-search-filters"
	sf "camunda-workers/internal/workers/franchise/search-franchises"

	cpr "camunda-workers/internal/workers/application/check-priority-routing"
	crs "camunda-workers/internal/workers/application/check-readiness-score"
	car "camunda-workers/internal/workers/application/create-application-record"
	sn "camunda-workers/internal/workers/application/send-notification"
	vad "camunda-workers/internal/workers/application/validate-application-data"

	// AI/ML Workers (5)
	ais "camunda-workers/internal/workers/ai-conversation/ai-search"
	ews "camunda-workers/internal/workers/ai-conversation/enrich-web-search"
	llm "camunda-workers/internal/workers/ai-conversation/llm-synthesis"
	pui "camunda-workers/internal/workers/ai-conversation/parse-user-intent"
	qid "camunda-workers/internal/workers/ai-conversation/query-internal-data"

	// Authentication & Utility Workers (10)
	alo "camunda-workers/internal/workers/auth/auth-logout"
	asig "camunda-workers/internal/workers/auth/auth-signin-google"
	asil "camunda-workers/internal/workers/auth/auth-signin-linkedin"
	asug "camunda-workers/internal/workers/auth/auth-signup-google"
	asul "camunda-workers/internal/workers/auth/auth-signup-linkedin"
	cv "camunda-workers/internal/workers/auth/captcha-verify"
	keycloaksignin "camunda-workers/internal/workers/auth/keycloak-signin"
	sessionmanager "camunda-workers/internal/workers/auth/session-manager"
	es "camunda-workers/internal/workers/communication/email-send"
	cuc "camunda-workers/internal/workers/crm/crm-user-create"
)

// retryWithBackoff attempts to execute a function with exponential backoff
func retryWithBackoff(operation func() error, maxRetries int, initialDelay time.Duration, log *zap.Logger, operationName string) error {
	var err error
	delay := initialDelay

	for i := 0; i < maxRetries; i++ {
		err = operation()
		if err == nil {
			return nil
		}

		if i < maxRetries-1 {
			log.Warn(fmt.Sprintf("%s failed, retrying...", operationName),
				zap.Error(err),
				zap.Int("attempt", i+1),
				zap.Int("maxRetries", maxRetries),
				zap.Duration("nextRetryIn", delay),
			)
			time.Sleep(delay)
			delay *= 2 // Exponential backoff
		}
	}

	return fmt.Errorf("%s failed after %d attempts: %w", operationName, maxRetries, err)
}

func main() {
	zapLog := logger.New("info", "console")
	defer zapLog.Sync()

	// Wrap zap logger with our logger interface
	log := logger.NewZapAdapter(zapLog)

	zapLog.Info("Starting worker manager...")

	cfg, err := config.Load()
	if err != nil {
		zapLog.Fatal("config load failed", zap.Error(err))
	}

	// ✅ Initialize Tracing
	tracingConfig := observability.TracingConfig{
		Enabled:       cfg.Monitoring.Tracing.Enabled,
		ServiceName:   "lemici-worker-manager",
		Endpoint:      cfg.Monitoring.Tracing.Endpoint,
		Sampler:       cfg.Monitoring.Tracing.Sampler,
		Probability:   cfg.Monitoring.Tracing.Probability,
		Environment:   cfg.App.Environment,
		Version:       cfg.App.Version,
		ExportTimeout: time.Duration(cfg.Monitoring.Tracing.ExportTimeout) * time.Second,
	}

	tracerProvider, cleanupTracer, err := observability.InitTracer(tracingConfig)
	if err != nil {
		zapLog.Warn("Failed to initialize tracer", zap.Error(err))
	} else {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := cleanupTracer(ctx); err != nil {
				zapLog.Error("Failed to shutdown tracer", zap.Error(err))
			}
		}()
		zapLog.Info("Distributed tracing initialized",
			zap.String("service", tracingConfig.ServiceName),
			zap.String("endpoint", tracingConfig.Endpoint),
		)
	}

	_ = tracerProvider

	// ============================================================================
	// INITIALIZE CIRCUIT BREAKER MANAGER
	// ============================================================================
	zapLog.Info("Initializing Circuit Breaker Manager")
	cbManager := circuitbreaker.NewManager()
	zapLog.Info("Circuit Breaker Manager initialized",
		zap.Int("initial_count", len(cbManager.GetAll())))

	obs := observability.New("worker-manager")
	defer obs.Shutdown()

	jaegerEndpoint := getEnvOrDefault("JAEGER_ENDPOINT", cfg.Monitoring.Tracing.Endpoint)
	if jaegerEndpoint == "" {
		jaegerEndpoint = "http://jaeger:14268/api/traces"
	}

	zapLog.Info("Initializing secondary tracer", zap.String("endpoint", jaegerEndpoint))

	tracer, tracerCleanup, terr := observability.NewTracer("worker-manager", jaegerEndpoint)
	// tracer, tracerCleanup, terr := observability.NewTracer("worker-manager", "http://localhost:14268/api/traces")
	if terr != nil {
		zapLog.Warn("tracer init failed", zap.Error(terr))
	} else {
		defer tracerCleanup()
	}

	_ = tracer
	ctx := context.Background()

	// --- Init Zeebe Client with retry ---
	var zeebeClient zbc.Client
	err = retryWithBackoff(func() error {
		var err error
		zeebeClient, err = zbc.NewClient(&zbc.ClientConfig{
			GatewayAddress:         cfg.Camunda.BrokerAddress,
			UsePlaintextConnection: true,
			KeepAlive:              30 * time.Second,
		})
		return err
	}, 10, 2*time.Second, zapLog, "Zeebe client initialization")

	if err != nil {
		zapLog.Fatal("zeebe client failed after retries", zap.Error(err))
	}
	zapLog.Info("Zeebe client connected successfully")

	// --- Init PostgreSQL with retry ---
	var pg *database.PostgresClient
	err = retryWithBackoff(func() error {
		var err error
		pg, err = database.NewPostgres(cfg.Database.Postgres)
		if err != nil {
			return err
		}
		// Test the connection with context
		return pg.Ping(ctx)
	}, 15, 2*time.Second, zapLog, "PostgreSQL connection")

	if err != nil {
		zapLog.Fatal("postgres failed after retries", zap.Error(err))
	}
	defer pg.Close()
	zapLog.Info("PostgreSQL connected successfully")

	// --- Init Elasticsearch with retry ---
	var esClient *database.ElasticsearchClient
	err = retryWithBackoff(func() error {
		var err error
		esClient, err = database.NewElasticsearch(cfg.Database.Elasticsearch)
		if err != nil {
			return err
		}
		// Test the connection
		return esClient.Ping()
	}, 15, 2*time.Second, zapLog, "Elasticsearch connection")

	if err != nil {
		zapLog.Fatal("elasticsearch failed after retries", zap.Error(err))
	}
	zapLog.Info("Elasticsearch connected successfully")

	// --- Init Redis with retry ---
	var redis *database.RedisClient
	err = retryWithBackoff(func() error {
		var err error
		redis, err = database.NewRedis(cfg.Database.Redis)
		if err != nil {
			return err
		}
		// Test the connection with context
		return redis.Ping(ctx)
	}, 10, 2*time.Second, zapLog, "Redis connection")

	if err != nil {
		zapLog.Fatal("redis failed after retries", zap.Error(err))
	}
	defer redis.Close()
	zapLog.Info("Redis connected successfully")

	// ============================================================================
	// INITIALIZE EXTERNAL SERVICE CLIENTS WITH CIRCUIT BREAKERS
	// ============================================================================
	zapLog.Info("Initializing external service clients with circuit breakers")

	// Initialize Keycloak Client with circuit breaker
	keycloakClient := auth.NewKeycloakClient(
		cfg.Auth.Keycloak.URL,
		cfg.Auth.Keycloak.Realm,
		cfg.Auth.Keycloak.ClientID,
		cfg.Auth.Keycloak.ClientSecret,
	)

	// ===== IDEMPOTENCY CHECKER SETUP =====
	var idempotencyChecker idempotency.Checker
	if pg != nil {
		idempotencyChecker = idempotency.NewDBChecker(pg.DB)
		log.Info("Idempotency checker initialized with PostgreSQL", map[string]interface{}{})
	} else {
		log.Warn("PostgreSQL not available, idempotency checking disabled", map[string]interface{}{})
	}

	zapLog.Info("All external service clients initialized with circuit breakers",
		zap.Int("circuit_breaker_count", len(cbManager.GetAll())))

	// ============================================================================
	// ✅ STEP 1: INITIALIZE CAMUNDA CLIENT (BEFORE EVERYTHING)
	// ============================================================================
	camundaClient, err := camunda.NewClientWithRegistry(&camunda.ClientConfig{
		GatewayAddress:         cfg.Camunda.BrokerAddress,
		UsePlaintextConnection: true,
		ConnectionTimeout:      10 * time.Second,
		RequestTimeout:         30 * time.Second,
		RetryConfig:            camunda.DefaultRetryConfig,
	}, nil) // ⚠️ Pass nil first, we'll update deps later
	if err != nil {
		zapLog.Fatal("Failed to create Camunda client", zap.Error(err))
	}

	// ============================================================================
	// ✅ STEP 2: CREATE FRANCHISE HANDLER (BEFORE REGISTRY)
	// ============================================================================
	franchiseHandler := handlers.NewFranchiseHandler(camundaClient, log, redis.GetClient())

	zapLog.Info("✅ Franchise handler created",
		zap.Int("pendingResponses", franchiseHandler.PendingResponsesCount()))

	// ============================================================================
	// ✅ STEP 3: CREATE DEPENDENCIES WITH RESPONSE HANDLER
	// ============================================================================
	deps := &registry.Dependencies{
		Logger:          log,
		Config:          cfg,
		ESClient:        esClient,
		PGClient:        pg,
		RedisClient:     redis,
		CircuitBreaker:  cbManager,
		Idempotency:     idempotencyChecker,
		ResponseHandler: franchiseHandler, // ✅ CRITICAL: Set BEFORE workers register
	}

	zapLog.Info("✅ Dependencies created with response handler",
		zap.Bool("hasResponseHandler", deps.ResponseHandler != nil))

	// ============================================================================
	// ✅ STEP 4: UPDATE CAMUNDA CLIENT WITH DEPS
	// ============================================================================
	camundaClient.SetDependencies(deps)

	zapLog.Info("✅ Camunda client updated with dependencies")

	// ============================================================================
	// ✅ REGISTER ALL WORKERS (using registry pattern)
	// ============================================================================

	// --- 1. Infrastructure Workers (5) ---
	if cfg.Workers[sar.TaskType].Enabled {
		handler := sar.NewHandler(
			&sar.Config{
				Timeout: time.Duration(cfg.Workers[sar.TaskType].Timeout) * time.Millisecond,
			},
			log,
			deps,
			//franchiseHandler, // ✅ Pass response handler directly
		)

		// ✅ START WORKER WITH FAST POLLING
		zeebeClient.NewJobWorker().
			JobType(sar.TaskType).
			Handler(handler.Handle).
			MaxJobsActive(15).                   // ✅ More jobs
			Concurrency(5).                      // ✅ More concurrency
			PollInterval(50 * time.Millisecond). // ✅ FAST 50ms polling
			RequestTimeout(5 * time.Second).     // ✅ Quick timeout
			Timeout(10 * time.Second).           // ✅ Job timeout
			Name("send-api-response-worker").
			Open()

		zapLog.Info("send-api-response worker started",
			zap.String("taskType", sar.TaskType),
			zap.Int("maxJobsActive", 15),
			zap.Duration("pollInterval", 50*time.Millisecond),
		)
	}

	if cfg.Workers[vs.TaskType].Enabled {
		handler := vs.NewHandler(
			&vs.Config{
				Timeout: time.Duration(cfg.Workers[vs.TaskType].Timeout) * time.Millisecond,
			},
			pg.DB, redis.Client, log,
		)
		startWorker(zeebeClient, vs.TaskType, cfg.Workers[vs.TaskType], handler.Handle, zapLog)
	}

	if cfg.Workers[br.TaskType].Enabled {
		handler := br.NewHandler(
			&br.Config{
				AppVersion: cfg.App.Version,
				Timeout:    time.Duration(cfg.Workers[vs.TaskType].Timeout) * time.Millisecond,
			},
			log,
		)
		startWorker(zeebeClient, br.TaskType, cfg.Workers[br.TaskType], handler.Handle, zapLog)
	}

	if cfg.Workers[st.TaskType].Enabled {
		handler := st.NewHandler(
			&st.Config{
				TemplateRules: map[string]map[string]string{
					"route": cfg.Template.TemplateRules.Route,
					"flow":  cfg.Template.TemplateRules.Flow,
				},
			},
			log,
		)
		startWorker(zeebeClient, st.TaskType, cfg.Workers[st.TaskType], handler.Handle, zapLog)
	}

	// Template-Driven Worker
	if cfg.Workers[td.TaskType].Enabled {
		tdConfig := td.ProductionConfig()

		if cfg.Template.RegistryPath != "" {
			tdConfig.TemplatesBaseDir = cfg.Template.RegistryPath
		}

		tdConfig.LogLevel = "info" // Default log level
		tdConfig.MaxJobsActive = cfg.Workers[td.TaskType].MaxJobsActive
		tdConfig.Timeout = int(time.Duration(cfg.Workers[td.TaskType].Timeout) * time.Millisecond / time.Second)
		tdLogger := logger.NewZapAdapter(zapLog)

		handler, err := td.NewHandler(tdConfig, tdLogger)
		if err != nil {
			zapLog.Fatal("Failed to create template-driven handler", zap.Error(err))
		}

		worker := zeebeClient.NewJobWorker().
			JobType(td.TaskType).
			Handler(handler.Handle).
			MaxJobsActive(cfg.Workers[td.TaskType].MaxJobsActive).
			Timeout(time.Duration(cfg.Workers[td.TaskType].Timeout) * time.Millisecond).
			Name(tdConfig.WorkerID).
			Open()

		zapLog.Info("Template-driven worker started",
			zap.String("taskType", td.TaskType),
			zap.String("workerId", tdConfig.WorkerID),
		)

		_ = worker
	}

	// --- 2. Data Access Workers (4) ---
	if cfg.Workers[qp.TaskType].Enabled {
		handler := qp.NewHandler(
			&qp.Config{
				Timeout: time.Duration(cfg.Workers[qp.TaskType].Timeout) * time.Millisecond,
			},
			pg.DB, log,
		)
		startWorker(zeebeClient, qp.TaskType, cfg.Workers[qp.TaskType], handler.Handle, zapLog)
	}

	if cfg.Workers[qe.TaskType].Enabled {
		handler := qe.NewHandler(
			&qe.Config{
				Timeout: time.Duration(cfg.Workers[qe.TaskType].Timeout) * time.Millisecond,
			},
			esClient.Client, log,
		)
		startWorker(zeebeClient, qe.TaskType, cfg.Workers[qe.TaskType], handler.Handle, zapLog)
	}

	// Franchise PostgreSQL Worker
	if taskType := "franchise-postgres"; cfg.Workers[taskType].Enabled {
		fpConfig := &franchisepostgres.Config{
			RequestTimeout: time.Duration(cfg.Workers[taskType].Timeout) * time.Millisecond,
			MaxJobsActive:  cfg.Workers[taskType].MaxJobsActive,
		}
		handler := franchisepostgres.NewHandler(pg.DB, log, fpConfig)
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)

		zapLog.Info("Franchise PostgreSQL worker registered successfully",
			zap.String("taskType", taskType),
			zap.Int("supportedOperations", 22),
			zap.Int("tables", 8),
			zap.Int("maxJobsActive", fpConfig.MaxJobsActive),
			zap.Duration("requestTimeout", fpConfig.RequestTimeout),
		)
	}

	// Franchise ES Indexer Worker
	if taskType := "franchise-es-indexer"; cfg.Workers[taskType].Enabled {
		esConfig := &esindexer.Config{
			RequestTimeout: time.Duration(cfg.Workers[taskType].Timeout) * time.Millisecond,
			MaxRetries:     3,
			BatchSize:      100,
		}
		handler := esindexer.NewHandler(esConfig, pg.DB, esClient, log)
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)

		zapLog.Info("Franchise ES Indexer worker registered successfully",
			zap.String("taskType", taskType),
			zap.String("esIndex", esindexer.ESIndex),
			zap.Int("maxJobsActive", cfg.Workers[taskType].MaxJobsActive),
			zap.Duration("requestTimeout", esConfig.RequestTimeout),
		)
	}

	// --- 3. Business Logic Workers (9) ---

	// First check for search-franchises worker
	if taskType := "search-franchises"; cfg.Workers[taskType].Enabled {
		handler := sf.NewHandler(
			&sf.Config{
				DefaultLimit:       20,
				MaxLimit:           100,
				Fuzziness:          "AUTO",
				EnableSuggestions:  true,
				EnableAggregations: true,
			},
			esClient, // database.ElasticsearchClient
			log,
		)

		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.HandleJob, zapLog)
	}

	if cfg.Workers[psf.TaskType].Enabled {
		handler := psf.NewHandler(&psf.Config{}, log)
		startWorker(zeebeClient, psf.TaskType, cfg.Workers[psf.TaskType], handler.Handle, zapLog)
	}

	if cfg.Workers[arr.TaskType].Enabled {
		handler := arr.NewHandler(
			&arr.Config{
				MaxItems: 100,
				Timeout:  time.Duration(cfg.Workers[arr.TaskType].Timeout) * time.Millisecond,
			},
			log,
		)
		startWorker(zeebeClient, arr.TaskType, cfg.Workers[arr.TaskType], handler.Handle, zapLog)
	}

	if cfg.Workers[cms.TaskType].Enabled {
		handler := cms.NewHandler(
			&cms.Config{
				CacheTTL: 10 * time.Minute,
			},
			pg.DB, redis.Client, log,
		)
		startWorker(zeebeClient, cms.TaskType, cfg.Workers[cms.TaskType], handler.Handle, zapLog)
	}

	if cfg.Workers[vad.TaskType].Enabled {
		handler := vad.NewHandler(&vad.Config{}, log)
		startWorker(zeebeClient, vad.TaskType, cfg.Workers[vad.TaskType], handler.Handle, zapLog)
	}

	if cfg.Workers[crs.TaskType].Enabled {
		handler := crs.NewHandler(&crs.Config{}, log)
		startWorker(zeebeClient, crs.TaskType, cfg.Workers[crs.TaskType], handler.Handle, zapLog)
	}

	if cfg.Workers[cpr.TaskType].Enabled {
		handler := cpr.NewHandler(
			&cpr.Config{
				CacheTTL: 30 * time.Minute,
			},
			pg.DB, redis.Client, log,
		)
		startWorker(zeebeClient, cpr.TaskType, cfg.Workers[cpr.TaskType], handler.Handle, zapLog)
	}

	if cfg.Workers[car.TaskType].Enabled {
		handler := car.NewHandler(&car.Config{}, pg.DB, log)
		startWorker(zeebeClient, car.TaskType, cfg.Workers[car.TaskType], handler.Handle, zapLog)
	}

	if cfg.Workers[sn.TaskType].Enabled {
		handler, err := sn.NewHandler(
			&sn.Config{
				Timeout: time.Duration(cfg.Workers[sn.TaskType].Timeout) * time.Millisecond,
			},
			pg.DB, log,
		)
		if err != nil {
			zapLog.Fatal("failed to create send-notification handler", zap.Error(err))
		}
		startWorker(zeebeClient, sn.TaskType, cfg.Workers[sn.TaskType], handler.Handle, zapLog)
	}

	// --- 4. AI/ML Workers (5) ---
	// Create adapters for AI workers
	puiLogAdapter := &parseUserIntentLoggerAdapter{log}
	qidLogAdapter := &queryInternalDataLoggerAdapter{log}
	ewsLogAdapter := &enrichWebSearchLoggerAdapter{log}
	llmLogAdapter := &llmSynthesisLoggerAdapter{log}

	// Parse User Intent Worker
	if cfg.Workers[pui.TaskType].Enabled {
		handler := pui.NewHandler(pui.HandlerOptions{
			Config: &pui.Config{
				GenAIBaseURL: cfg.APIs.GenAI.BaseURL,
				Timeout:      30 * time.Second,
				MaxRetries:   2,
			},
			Logger:    puiLogAdapter,
			CBManager: cbManager, // Pass circuit breaker manager
		})
		startWorker(zeebeClient, pui.TaskType, cfg.Workers[pui.TaskType], handler.Handle, zapLog)
	}

	// Query Internal Data Worker
	if cfg.Workers[qid.TaskType].Enabled {
		handler := qid.NewHandler(
			&qid.Config{
				Timeout:    2 * time.Second,
				CacheTTL:   5 * time.Minute,
				MaxResults: 10,
			},
			pg.DB, esClient.Client, redis.Client, qidLogAdapter,
		)
		startWorker(zeebeClient, qid.TaskType, cfg.Workers[qid.TaskType], handler.Handle, zapLog)
	}

	// Enrich Web Search Worker
	if cfg.Workers[ews.TaskType].Enabled {
		handler := ews.NewHandler(ews.HandlerOptions{
			Config: &ews.Config{
				SearchAPIBaseURL: cfg.APIs.WebSearch.BaseURL,
				SearchAPIKey:     cfg.APIs.WebSearch.APIKey,
				SearchEngineID:   cfg.APIs.WebSearch.EngineID,
				Timeout:          3 * time.Second,
				MaxResults:       5,
				MinRelevance:     0.5,
			},
			Logger:    ewsLogAdapter,
			CBManager: cbManager,
		})
		startWorker(zeebeClient, ews.TaskType, cfg.Workers[ews.TaskType], handler.Handle, zapLog)
	}

	// LLM Synthesis Worker
	if cfg.Workers[llm.TaskType].Enabled {
		handler := llm.NewHandler(llm.HandlerOptions{
			Config: &llm.Config{
				GenAIBaseURL: cfg.APIs.GenAI.BaseURL,
				Timeout:      5 * time.Second,
				MaxRetries:   1,
				MaxTokens:    500,
				Temperature:  0.7,
			},
			Logger:    llmLogAdapter,
			CBManager: cbManager, // Pass circuit breaker manager
		})
		startWorker(zeebeClient, llm.TaskType, cfg.Workers[llm.TaskType], handler.Handle, zapLog)
	}

	// AI Search Worker - Using Qwen 2.5 for 36x faster responses
	if taskType := "ai-search"; cfg.Workers[taskType].Enabled {
		aiConfig := ais.NewDefaultConfig()

		// ONLY env-based overrides
		aiConfig.LLMEndpoint = getEnvOrDefault(
			"OLLAMA_URL",
			getEnvOrDefault("LLM_ENDPOINT", aiConfig.LLMEndpoint),
		)

		handler := ais.NewHandler(aiConfig, esClient, log)

		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)

		zapLog.Info("AI Search worker registered successfully",
			zap.String("taskType", taskType),
			zap.String("llmModel", aiConfig.LLMModel),
			zap.String("llmEndpoint", aiConfig.LLMEndpoint),
			zap.String("esIndex", aiConfig.IndexName),
			zap.Duration("llmTimeout", aiConfig.LLMTimeout),
		)
	}

	// --- 5. Authentication & Utility Workers (10) ---

	if taskType := "keycloak-signin"; cfg.Workers[taskType].Enabled {
		handler, err := keycloaksignin.NewHandler(keycloaksignin.HandlerOptions{
			AppConfig: cfg,
			Camunda:   camundaClient,
			Redis:     redis.GetClient(),
			Logger:    log,
		})
		if err != nil {
			zapLog.Fatal("failed to create keycloak-signin handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	if taskType := "session-manager"; cfg.Workers[taskType].Enabled {
		handler, err := sessionmanager.NewHandler(sessionmanager.HandlerOptions{
			AppConfig: cfg,
			Camunda:   camundaClient,
			Redis:     redis.GetClient(),
			Logger:    log,
		})
		if err != nil {
			zapLog.Fatal("failed to create session-manager handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}
	// Auth Signin Google
	if taskType := "auth-signin-google"; cfg.Workers[taskType].Enabled {
		handler, err := asig.NewHandler(asig.HandlerOptions{
			AppConfig:          cfg,
			Camunda:            nil,
			Logger:             log,
			CBManager:          cbManager,      // NEW: Pass circuit breaker manager
			Keycloak:           keycloakClient, // NEW: Pass Keycloak client
			IdempotencyChecker: idempotencyChecker,
		})
		if err != nil {
			zapLog.Fatal("failed to create auth-signin-google handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	// Auth Signin LinkedIn
	if taskType := "auth-signin-linkedin"; cfg.Workers[taskType].Enabled {
		handler, err := asil.NewHandler(asil.HandlerOptions{
			AppConfig:          cfg,
			Camunda:            nil,
			Logger:             log,
			CBManager:          cbManager,
			Keycloak:           keycloakClient,
			IdempotencyChecker: idempotencyChecker,
		})
		if err != nil {
			zapLog.Fatal("failed to create auth-signin-linkedin handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	// Auth Signup Google
	if taskType := "auth-signup-google"; cfg.Workers[taskType].Enabled {
		handler, err := asug.NewHandler(asug.HandlerOptions{
			AppConfig:          cfg,
			Camunda:            nil,
			Logger:             log,
			CBManager:          cbManager,
			Keycloak:           keycloakClient,
			IdempotencyChecker: idempotencyChecker,
		})
		if err != nil {
			zapLog.Fatal("failed to create auth-signup-google handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	// Auth Signup LinkedIn
	if taskType := "auth-signup-linkedin"; cfg.Workers[taskType].Enabled {
		handler, err := asul.NewHandler(asul.HandlerOptions{
			AppConfig:          cfg,
			Camunda:            nil,
			Logger:             log,
			CBManager:          cbManager,
			Keycloak:           keycloakClient,
			IdempotencyChecker: idempotencyChecker,
		})
		if err != nil {
			zapLog.Fatal("failed to create auth-signup-linkedin handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	// Auth Logout
	if taskType := "auth.logout"; cfg.Workers[taskType].Enabled {
		handler, err := alo.NewHandler(alo.HandlerOptions{
			AppConfig:   cfg,
			Camunda:     nil,
			Logger:      log,
			CBManager:   cbManager,
			Keycloak:    keycloakClient,
			RedisClient: redis.GetClient(),
		})
		if err != nil {
			zapLog.Fatal("failed to create auth-logout handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	// Captcha Verify
	if taskType := "captcha-verify"; cfg.Workers[taskType].Enabled {
		handler, err := cv.NewHandler(cv.HandlerOptions{
			AppConfig: cfg,
			Camunda:   nil,
			Logger:    log,
		})
		if err != nil {
			zapLog.Fatal("failed to create captcha-verify handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	// CRM User Create
	if taskType := "crm-user-create"; cfg.Workers[taskType].Enabled {
		handler, err := cuc.NewHandler(cuc.HandlerOptions{
			AppConfig: cfg,
			Camunda:   nil,
			Logger:    log,
			CBManager: cbManager,
		})
		if err != nil {
			zapLog.Fatal("failed to create crm-user-create handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	// Email Send
	if taskType := "email-send"; cfg.Workers[taskType].Enabled {
		handler, err := es.NewHandler(es.HandlerOptions{
			AppConfig:          cfg,
			Camunda:            nil,
			Logger:             log,
			CBManager:          cbManager,
			IdempotencyChecker: idempotencyChecker,
		})
		if err != nil {
			zapLog.Fatal("failed to create email-send handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	zapLog.Info("All workers registered successfully",
		zap.Int("totalWorkers", 33))

	// ============================================================================
	// START IDEMPOTENCY CLEANUP JOB
	// ============================================================================
	if idempotencyChecker != nil {
		go func() {
			cleanupTicker := time.NewTicker(1 * time.Hour)
			defer cleanupTicker.Stop()

			zapLog.Info("Idempotency cleanup job started")

			for range cleanupTicker.C {
				ctx := context.Background()

				// Type assertion to access CleanupExpired method
				if dbChecker, ok := idempotencyChecker.(*idempotency.DBChecker); ok {
					count, err := dbChecker.CleanupExpired(ctx)
					if err != nil {
						zapLog.Error("Idempotency cleanup failed",
							zap.Error(err))
					} else if count > 0 {
						zapLog.Info("Idempotency cleanup completed",
							zap.Int64("keysDeleted", count))
					}
				}
			}
		}()
	}

	// ============================================================================
	// HEALTH & METRICS SERVER
	// ============================================================================
	go func() {
		// Enhanced health endpoint
		http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":    "healthy",
				"timestamp": time.Now().Format(time.RFC3339),
				"service":   "worker-manager",
				"version":   cfg.App.Version,
				"workers":   30, // Updated count
			})
		})

		http.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":    "ready",
				"timestamp": time.Now().Format(time.RFC3339),
				"workers":   30, // Updated count
			})
		})

		// Add circuit breakers endpoint
		http.HandleFunc("/circuit-breakers", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"circuit_breakers": cbManager.GetMetrics(),
				"timestamp":        time.Now().Format(time.RFC3339),
			})
		})

		// Add idempotency stats endpoint
		http.HandleFunc("/idempotency-stats", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			if idempotencyChecker != nil {
				if dbChecker, ok := idempotencyChecker.(*idempotency.DBChecker); ok {
					stats, err := dbChecker.GetStats(context.Background())
					if err != nil {
						w.WriteHeader(http.StatusInternalServerError)
						json.NewEncoder(w).Encode(map[string]interface{}{
							"error": err.Error(),
						})
						return
					}

					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"stats":     stats,
						"timestamp": time.Now().Format(time.RFC3339),
					})
					return
				}
			}

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"message":   "Idempotency not enabled",
				"timestamp": time.Now().Format(time.RFC3339),
			})
		})

		// Add workers status endpoint
		http.HandleFunc("/workers", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"workers": []string{
					"send-api-response",
					"query-elasticsearch",
					"query-postgresql",
					"search-franchises",
					// ... other workers
				},
				"timestamp": time.Now().Format(time.RFC3339),
			})
		})

		http.HandleFunc("/debug/response-handler", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			status := map[string]interface{}{
				"connected": franchiseHandler != nil,
				"timestamp": time.Now().Format(time.RFC3339),
			}

			if franchiseHandler != nil {
				status["pending_responses"] = franchiseHandler.PendingResponsesCount()
			}

			_ = json.NewEncoder(w).Encode(status)
		})

		http.Handle("/metrics", promhttp.Handler())

		zapLog.Info("Health/Metrics/Circuit-Breakers server listening on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil {
			zapLog.Error("Health/Metrics server failed", zap.Error(err))
		}
	}()

	// ============================================================================
	// GRACEFUL SHUTDOWN
	// ============================================================================
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	zapLog.Info("Worker manager running. Press Ctrl+C to stop.")
	<-sigCh

	zapLog.Info("Shutdown signal received, stopping workers...")
	_, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := zeebeClient.Close(); err != nil {
		zapLog.Error("Error closing Zeebe client", zap.Error(err))
	}

	zapLog.Info("Worker manager stopped gracefully")
}

// getEnvOrDefault gets environment variable or returns default
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// Logger adapters for AI workers
type parseUserIntentLoggerAdapter struct {
	logger.Logger
}

func (a *parseUserIntentLoggerAdapter) With(fields map[string]interface{}) pui.Logger {
	return &parseUserIntentLoggerAdapter{a.Logger.With(fields)}
}

type queryInternalDataLoggerAdapter struct {
	logger.Logger
}

func (a *queryInternalDataLoggerAdapter) With(fields map[string]interface{}) qid.Logger {
	return &queryInternalDataLoggerAdapter{a.Logger.With(fields)}
}

type enrichWebSearchLoggerAdapter struct {
	logger.Logger
}

func (a *enrichWebSearchLoggerAdapter) With(fields map[string]interface{}) ews.Logger {
	return &enrichWebSearchLoggerAdapter{a.Logger.With(fields)}
}

type llmSynthesisLoggerAdapter struct {
	logger.Logger
}

func (a *llmSynthesisLoggerAdapter) With(fields map[string]interface{}) llm.Logger {
	return &llmSynthesisLoggerAdapter{a.Logger.With(fields)}
}

// Line 1150-1180: REPLACE startWorker function completely

func startWorker(client zbc.Client, taskType string, wcfg config.WorkerConfig, handlerFunc func(worker.JobClient, entities.Job), log *zap.Logger) {
	if !wcfg.Enabled {
		log.Info("worker disabled", zap.String("taskType", taskType))
		return
	}

	// ✅ CRITICAL CHANGES for faster polling
	pollInterval := 25 * time.Millisecond // ✅ CHANGED from 100ms to 50ms
	if wcfg.PollInterval > 0 {
		pollInterval = time.Duration(wcfg.PollInterval) * time.Millisecond
	}

	maxJobsActive := wcfg.MaxJobsActive
	if maxJobsActive == 0 {
		maxJobsActive = 50 // ✅ Default increased from 5 to 10
	}

	concurrency := 10 // ✅ Default concurrency
	if wcfg.Concurrency > 0 {
		concurrency = wcfg.Concurrency
	}

	requestTimeout := 5 * time.Second // ✅ CHANGED from 10s to 5s
	jobTimeout := time.Duration(wcfg.Timeout) * time.Millisecond

	wrapped := func(c worker.JobClient, j entities.Job) {
		ctx := context.Background()
		ctx, span := otel.Tracer("worker-manager").Start(ctx, "worker:"+taskType)
		start := time.Now()
		span.SetAttributes(
			attribute.String("worker.task_type", taskType),
			attribute.Int64("job.key", j.GetKey()),
			attribute.Int64("workflow.instance_key", j.GetProcessInstanceKey()),
			attribute.String("bpmn.process_id", j.GetBpmnProcessId()),
			attribute.String("element.id", j.GetElementId()),
		)
		defer func() {
			span.SetAttributes(
				attribute.Int64("job.retries", int64(j.GetRetries())),
				attribute.Float64("duration_ms", float64(time.Since(start).Milliseconds())),
			)
			span.End()
		}()
		handlerFunc(c, j)
	}

	// ✅ KEY CHANGE: Add PollInterval and RequestTimeout
	client.NewJobWorker().
		JobType(taskType).
		Handler(wrapped).
		MaxJobsActive(maxJobsActive).
		Concurrency(concurrency).
		PollInterval(pollInterval).
		RequestTimeout(requestTimeout).
		Timeout(jobTimeout).
		Name(fmt.Sprintf("%s-worker", taskType)).
		Open()

	log.Info("worker started",
		zap.String("taskType", taskType),
		zap.Int("maxJobsActive", maxJobsActive),
		zap.Int("concurrency", concurrency),
		zap.Duration("pollInterval", pollInterval), // ✅ ADD THIS
		zap.Int("timeout_ms", wcfg.Timeout),
	)
}
