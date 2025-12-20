// cmd/worker-manager/main.go
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
	"go.uber.org/zap"

	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/observability"
	"camunda-workers/internal/common/zoho"

	// Infrastructure Workers (3)
	br "camunda-workers/internal/workers/infrastructure/build-response"
	st "camunda-workers/internal/workers/infrastructure/select-template"
	vs "camunda-workers/internal/workers/infrastructure/validate-subscription"

	// Data Access Workers (2)
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

	// AI/ML Workers (4)
	ews "camunda-workers/internal/workers/ai-conversation/enrich-web-search"
	llm "camunda-workers/internal/workers/ai-conversation/llm-synthesis"
	pui "camunda-workers/internal/workers/ai-conversation/parse-user-intent"
	qid "camunda-workers/internal/workers/ai-conversation/query-internal-data"

	// Authentication & Utility Workers (8)
	alo "camunda-workers/internal/workers/auth/auth-logout"
	asig "camunda-workers/internal/workers/auth/auth-signin-google"
	asil "camunda-workers/internal/workers/auth/auth-signin-linkedin"
	asug "camunda-workers/internal/workers/auth/auth-signup-google"
	asul "camunda-workers/internal/workers/auth/auth-signup-linkedin"
	cv "camunda-workers/internal/workers/auth/captcha-verify"
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

	obs := observability.New("worker-manager")
	defer obs.Shutdown()

	ctx := context.Background()

	// --- Init Zeebe Client with retry ---
	var zeebeClient zbc.Client
	err = retryWithBackoff(func() error {
		var err error
		zeebeClient, err = zbc.NewClient(&zbc.ClientConfig{
			GatewayAddress:         cfg.Camunda.BrokerAddress,
			UsePlaintextConnection: true,
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

	// --- Init External Service Clients ---
	_ = auth.NewKeycloakClient(
		cfg.Auth.Keycloak.URL,
		cfg.Auth.Keycloak.Realm,
		cfg.Auth.Keycloak.ClientID,
		cfg.Auth.Keycloak.ClientSecret,
	)

	_ = zoho.NewCRMClient(cfg.Integrations.Zoho.APIKey, cfg.Integrations.Zoho.AuthToken)

	zapLog.Info("All external service clients initialized")

	// --- START: Register ALL 26 Workers ---

	// --- 1. Infrastructure Workers (3) ---
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
				TemplateRegistry: cfg.Template.RegistryPath,
				AppVersion:       cfg.App.Version,
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

	// --- 2. Data Access Workers (2) ---
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

	// --- 3. Business Logic Workers (5 + 5 = 10) ---

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

	// --- 4. AI/ML Workers (4) ---
	// Create adapters for AI workers
	puiLogAdapter := &parseUserIntentLoggerAdapter{log}
	qidLogAdapter := &queryInternalDataLoggerAdapter{log}
	ewsLogAdapter := &enrichWebSearchLoggerAdapter{log}
	llmLogAdapter := &llmSynthesisLoggerAdapter{log}

	if cfg.Workers[pui.TaskType].Enabled {
		handler := pui.NewHandler(
			&pui.Config{
				GenAIBaseURL: cfg.APIs.GenAI.BaseURL,
				Timeout:      30 * time.Second,
				MaxRetries:   2,
			},
			puiLogAdapter,
		)
		startWorker(zeebeClient, pui.TaskType, cfg.Workers[pui.TaskType], handler.Handle, zapLog)
	}

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

	if cfg.Workers[ews.TaskType].Enabled {
		handler := ews.NewHandler(
			&ews.Config{
				SearchAPIBaseURL: cfg.APIs.WebSearch.BaseURL,
				SearchAPIKey:     cfg.APIs.WebSearch.APIKey,
				SearchEngineID:   cfg.APIs.WebSearch.EngineID,
				Timeout:          3 * time.Second,
				MaxResults:       5,
				MinRelevance:     0.5,
			},
			ewsLogAdapter,
		)
		startWorker(zeebeClient, ews.TaskType, cfg.Workers[ews.TaskType], handler.Handle, zapLog)
	}

	if cfg.Workers[llm.TaskType].Enabled {
		handler := llm.NewHandler(
			&llm.Config{
				GenAIBaseURL: cfg.APIs.GenAI.BaseURL,
				Timeout:      5 * time.Second,
				MaxRetries:   1,
				MaxTokens:    500,
				Temperature:  0.7,
			},
			llmLogAdapter,
		)
		startWorker(zeebeClient, llm.TaskType, cfg.Workers[llm.TaskType], handler.Handle, zapLog)
	}

	// --- 5. Authentication & Utility Workers (8) ---

	// Auth Signin Google
	if taskType := "auth-signin-google"; cfg.Workers[taskType].Enabled {
		handler, err := asig.NewHandler(asig.HandlerOptions{
			AppConfig: cfg,
			Camunda:   nil,
			Logger:    log,
		})
		if err != nil {
			zapLog.Fatal("failed to create auth-signin-google handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	// Auth Signin LinkedIn
	if taskType := "auth-signin-linkedin"; cfg.Workers[taskType].Enabled {
		handler, err := asil.NewHandler(asil.HandlerOptions{
			AppConfig: cfg,
			Camunda:   nil,
			Logger:    log,
		})
		if err != nil {
			zapLog.Fatal("failed to create auth-signin-linkedin handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	// Auth Signup Google
	if taskType := "auth-signup-google"; cfg.Workers[taskType].Enabled {
		handler, err := asug.NewHandler(asug.HandlerOptions{
			AppConfig: cfg,
			Camunda:   nil,
			Logger:    log,
		})
		if err != nil {
			zapLog.Fatal("failed to create auth-signup-google handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	// Auth Signup LinkedIn
	if taskType := "auth-signup-linkedin"; cfg.Workers[taskType].Enabled {
		handler, err := asul.NewHandler(asul.HandlerOptions{
			AppConfig: cfg,
			Camunda:   nil,
			Logger:    log,
		})
		if err != nil {
			zapLog.Fatal("failed to create auth-signup-linkedin handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	// Auth Logout
	if taskType := "auth-logout"; cfg.Workers[taskType].Enabled {
		handler, err := alo.NewHandler(alo.HandlerOptions{
			AppConfig: cfg,
			Camunda:   nil,
			Logger:    log,
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
		})
		if err != nil {
			zapLog.Fatal("failed to create crm-user-create handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}

	// Email Send
	if taskType := "email-send"; cfg.Workers[taskType].Enabled {
		handler, err := es.NewHandler(es.HandlerOptions{
			AppConfig: cfg,
			Camunda:   nil,
			Logger:    log,
		})
		if err != nil {
			zapLog.Fatal("failed to create email-send handler", zap.Error(err))
		}
		startWorker(zeebeClient, taskType, cfg.Workers[taskType], handler.Handle, zapLog)
	}
	zapLog.Info("All 25 workers registered successfully")

	// --- Health & Metrics Server ---
	go func() {
		http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"status": "healthy",
				"time":   time.Now().Format(time.RFC3339),
			})
		})
		http.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"status": "ready",
				"time":   time.Now().Format(time.RFC3339),
			})
		})
		http.Handle("/metrics", promhttp.Handler())
		zapLog.Info("Health/Metrics server listening on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil {
			zapLog.Error("Health/Metrics server failed", zap.Error(err))
		}
	}()

	// --- Graceful Shutdown ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	zapLog.Info("Shutdown signal received, stopping workers...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_ = shutdownCtx

	if err := zeebeClient.Close(); err != nil {
		zapLog.Error("Error closing Zeebe client", zap.Error(err))
	}

	zapLog.Info("Worker manager stopped gracefully")
}

// Logger adapters for AI workers that have their own Logger interfaces
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

func startWorker(client zbc.Client, taskType string, wcfg config.WorkerConfig, handlerFunc func(worker.JobClient, entities.Job), log *zap.Logger) {
	if !wcfg.Enabled {
		log.Info("worker disabled", zap.String("taskType", taskType))
		return
	}

	client.NewJobWorker().
		JobType(taskType).
		Handler(handlerFunc).
		MaxJobsActive(wcfg.MaxJobsActive).
		Timeout(time.Duration(wcfg.Timeout) * time.Millisecond).
		Open()

	log.Info("worker started",
		zap.String("taskType", taskType),
		zap.Int("maxJobsActive", wcfg.MaxJobsActive),
		zap.Int("timeout_ms", wcfg.Timeout),
	)
}
