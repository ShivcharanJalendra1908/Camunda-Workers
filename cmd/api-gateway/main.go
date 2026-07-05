// cmd/api-gateway/main.go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"strings"

	"camunda-workers/internal/api/handlers"
	"camunda-workers/internal/api/middleware"
	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/encryption"
	"camunda-workers/internal/common/flagsmith"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/observability"

	awsutil "camunda-workers/internal/common/aws"

	operateactions "camunda-workers/internal/workers/operate/actions"
	operatequeries "camunda-workers/internal/workers/operate/queries"
	operatews "camunda-workers/internal/workers/operate/ws"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func main() {

	// Load configuration
	cfg, err := config.Load()

	if err != nil {
		panic(fmt.Sprintf("Failed to load config: %v", err))
	}

	// Initialize logger
	log := logger.NewStructured(cfg.Logging.Level, cfg.Logging.Format)

	// Initialize Flagsmith Client
	flagsmith.Init(cfg, log)

	tracingConfig := observability.TracingConfig{
		Enabled:       cfg.Monitoring.Tracing.Enabled,
		ServiceName:   "lemici-api-gateway",
		Endpoint:      cfg.Monitoring.Tracing.Endpoint,
		Sampler:       cfg.Monitoring.Tracing.Sampler,
		Probability:   cfg.Monitoring.Tracing.Probability,
		Environment:   cfg.App.Environment,
		Version:       cfg.App.Version,
		ExportTimeout: time.Duration(cfg.Monitoring.Tracing.ExportTimeout) * time.Second,
	}

	tracerProvider, cleanupTracer, err := observability.InitTracer(tracingConfig)
	if err != nil {
		log.Error("Failed to initialize tracer", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := cleanupTracer(ctx); err != nil {
				log.Error("Failed to shutdown tracer", map[string]interface{}{"error": err.Error()})
			}
		}()
		log.Info("Distributed tracing initialized", map[string]interface{}{
			"service":  tracingConfig.ServiceName,
			"endpoint": tracingConfig.Endpoint,
			"sampler":  tracingConfig.Sampler,
		})
	}

	_ = tracerProvider

	log.Info("Starting Lemici API Gateway", map[string]interface{}{
		"version": cfg.App.Version,
		"env":     cfg.App.Environment,
		"port":    cfg.API.Port,
	})

	// Initialize Keycloak client
	_ = auth.NewKeycloakClient(
		cfg.Auth.Keycloak.URL,
		cfg.Auth.Keycloak.Realm,
		cfg.Auth.Keycloak.ClientID,
		cfg.Auth.Keycloak.ClientSecret,
		cfg.Auth.Keycloak.AdminClientID,     // ← ADD
		cfg.Auth.Keycloak.AdminClientSecret, // ← ADD
	)

	log.Info("Connected to Keycloak", map[string]interface{}{
		"url":   cfg.Auth.Keycloak.URL,
		"realm": cfg.Auth.Keycloak.Realm,
	})

	// Initialize Camunda client
	fmt.Printf("🔧 Connecting to Camunda at: %s\n", cfg.Camunda.BrokerAddress)
	camundaClient, err := camunda.NewClientFromEnv()
	if err != nil {
		log.Error("Failed to initialize Camunda client", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}
	defer camundaClient.Close()

	log.Info("Connected to Camunda/Zeebe", map[string]interface{}{
		"gateway": cfg.Camunda.BrokerAddress,
	})

	// Initialize PostgreSQL
	postgresDB, err := database.NewPostgres(cfg.Database.Postgres)
	if err != nil {
		log.Error("Failed to connect to PostgreSQL", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}
	defer postgresDB.Close()

	// Initialize Guest Audit Repo (async PG writer for guest audit logs)
	var guestAuditRepo *database.GuestAuditRepo
	if cfg.Guest.Audit.Enabled {
		guestAuditRepo = database.NewGuestAuditRepo(postgresDB.DB, cfg.Guest.Audit.BufferSize)
		defer guestAuditRepo.Close()
	}

	log.Info("Connected to PostgreSQL", map[string]interface{}{
		"host": cfg.Database.Postgres.Host,
		"db":   cfg.Database.Postgres.Database,
	})

	// Initialize Elasticsearch
	esClient, err := database.NewElasticsearch(cfg.Database.Elasticsearch)
	if err != nil {
		log.Error("Failed to connect to Elasticsearch", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	log.Info("Connected to Elasticsearch", map[string]interface{}{
		"addresses": cfg.Database.Elasticsearch.Addresses,
	})

	// Initialize Redis
	redisClient, err := database.NewRedis(cfg.Database.Redis)
	if err != nil {
		log.Error("Failed to connect to Redis", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}
	defer redisClient.Close()

	redisInfo := cfg.Database.Redis.Address
	if redisInfo == "" {
		redisInfo = "localhost:6379"
	}

	log.Info("Connected to Redis", map[string]interface{}{
		"address": redisInfo,
	})

	// Setup Gin router
	if cfg.App.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()

	// ============================================================================
	// ✅ CORRECT MIDDLEWARE ORDER (CRITICAL - DON'T CHANGE!)
	// ============================================================================
	// 1. Recovery MUST BE FIRST (catches all panics)
	router.Use(middleware.Recovery(log))

	// 2. Request ID (generate unique ID for each request)
	router.Use(middleware.RequestID())

	// 3. Tracing (distributed tracing context)
	router.Use(middleware.TracingMiddleware())

	// 4. CORS (handle cross-origin requests)

	// 4.1 CSRF Token Issuer (issue CSRF token for each request)
	// router.Use(middleware.CSRFTokenIssuer(redisClient.GetClient()))

	// 5. Security Headers

	// 6. Input Validation (basic validation)
	router.Use(middleware.InputValidation())

	// 7. Timeout (set request timeout)
	//router.Use(middleware.Timeout(30 * time.Second))
	router.Use(middleware.Timeout(60 * time.Second)) // Increase for LLM Testing

	// 8. Idempotency (prevent duplicate requests)
	router.Use(middleware.IdempotencyMiddleware(redisClient.GetClient()))

	// 9. Logger (log all requests/responses)
	router.Use(middleware.Logger(log))

	// 10. Error Handler MUST BE LAST! (catches all errors)
	router.Use(middleware.ErrorHandler(log, cfg.Auth.Keycloak.LoginRedirectURI))

	// ============================================================================
	// Public routes (no authentication required)
	// ============================================================================
	router.GET("/health", healthCheckHandler(cfg, postgresDB, redisClient, esClient))
	router.HEAD("/health", healthCheckHandler(cfg, postgresDB, redisClient, esClient))
	router.GET("/metrics", metricsHandler())

	// Root + catch-all redirect → configured home page (Keycloak "Return to Login" fix)
	// Only registered when PostLoginRedirectURI is set in config
	// Root redirect to Home (Handles Keycloak "Return to Login")
	router.GET("/", func(c *gin.Context) {
		homePage := cfg.Auth.Keycloak.PostLoginRedirectURI
		if homePage == "" {
			homePage = "/"
		}
		c.Redirect(http.StatusFound, homePage)
	})

	// Global 404 handler - Redirect to Home instead of showing 404
	router.NoRoute(func(c *gin.Context) {
		homePage := cfg.Auth.Keycloak.PostLoginRedirectURI
		if homePage == "" {
			homePage = "/"
		}
		c.Redirect(http.StatusFound, homePage)
	})

	// ============================================================================
	// ============================================================================
	// Initialize FLE Service (Field-Level Encryption for PII)
	// ============================================================================
	var fleService *encryption.FLEService
	if cfg.FLE != nil {
		fleFields := make(map[string]encryption.FieldConfig)
		for name, fieldCfg := range cfg.FLE.Fields {
			fleFields[name] = encryption.FieldConfig{Key: fieldCfg.Key}
		}
		fleCfg := encryption.FLEConfig{
			Enabled: cfg.FLE.Enabled,
			Fields:  fleFields,
		}
		fleService, err = encryption.NewFLEService(fleCfg)
		if err != nil {
			log.Warn("FLE service initialization failed, PII encryption disabled", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			log.Info("FLE service initialized", map[string]interface{}{
				"enabled": fleService.Enabled(),
			})
		}
	}

	// Initialize S3 Client (for profile photo uploads)
	// ============================================================================
	var s3Client *awsutil.S3Client
	if cfg.Integrations.AWS.S3.Enabled {
		s3Region := cfg.Integrations.AWS.S3.Region
		if s3Region == "" {
			s3Region = cfg.Integrations.AWS.Region
		}
		s3Client, err = awsutil.NewS3Client(context.Background(), s3Region, cfg.Integrations.AWS.S3.Bucket)
		if err != nil {
			log.Warn("S3 client initialization failed, photo uploads disabled", map[string]interface{}{
				"error":  err.Error(),
				"bucket": cfg.Integrations.AWS.S3.Bucket,
			})
		} else {
			log.Info("S3 client initialized", map[string]interface{}{
				"bucket": cfg.Integrations.AWS.S3.Bucket,
				"region": s3Region,
			})
		}
	}

	// Initialize handlers
	// ============================================================================
	workflowHandler := handlers.NewWorkflowHandler(
		camundaClient,
		log,
		redisClient.GetClient(),
		cfg,
		fleService,
		s3Client)

	franchiseHandler := handlers.NewFranchiseHandler(camundaClient, log, redisClient.GetClient(),
		cfg.Integrations.Internal.OperationsAlertEmail, cfg.Pagination, postgresDB.DB)

	userHandler := handlers.NewUserHandler(redisClient.GetClient(), postgresDB.DB, log, cfg)

	oauthHandler := handlers.NewOAuthHandler(redisClient.GetClient(), log, postgresDB.DB, camundaClient, cfg.Auth.Session.CookieDomain, cfg)

	documentHandler := handlers.NewDocumentHandler(cfg)

	// ============================================================================
	// Operate Live-Monitoring (WebSocket + Queries + Actions)
	// ============================================================================
	// Context for Operate background tasks (Poller)
	operateCtx, operateCancel := context.WithCancel(context.Background())
	defer operateCancel()

	// 1. WebSocket hub (must start before handler registration)
	wsHub := operatews.NewHub()

	// 2. ES poller — polls every 3 seconds, broadcasts to WS clients
	poller := operatews.NewPoller(esClient.Client, wsHub, 3*time.Second)
	go poller.Run(operateCtx)

	// 3. Services
	operateQuerySvc := operatequeries.NewOperateQueryService(esClient.Client)
	operateActionSvc := operateactions.NewOperateActionService(camundaClient.GetClient())

	// 4. Handler
	operateHandler := handlers.NewOperateHandler(operateQuerySvc, operateActionSvc, wsHub)

	router.GET("/debug/response-handler", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"connected":         franchiseHandler != nil,
			"pending_responses": franchiseHandler.PendingResponsesCount(),
			"timestamp":         time.Now().Format(time.RFC3339),
		})
	})
	// ============================================================================
	// PUBLIC API ROUTES (No JWT Required)
	// ============================================================================
	publicAPI := router.Group("/api/v1/")
	{
		// ========================================================================
		// GUEST QUOTA ROUTES (per-route-group middleware)
		// ========================================================================
		if cfg.Guest.Enabled {
			// ── AI routes ────────────────────────────────────────────────────
			aiGroup := publicAPI.Group("/ai")
			aiGroup.Use(
				middleware.GuestSignalMiddleware(cfg),
				middleware.GuestAnomalyMiddleware(redisClient.GetClient(), cfg, guestAuditRepo),
				middleware.GuestQuotaMiddleware(redisClient.GetClient(), cfg, "ai"),
			)
			{
				aiGroup.POST("/query", workflowHandler.StartAIQuery)
				aiGroup.POST("/discovery", workflowHandler.StartDiscovery)
			}
		} else {
			// Guest quota disabled — AI routes have no quota middleware
			publicAPI.POST("/ai/query", workflowHandler.StartAIQuery)
			publicAPI.POST("/ai/discovery", workflowHandler.StartDiscovery)
		}

		// ========================================================================
		// OAUTH/OIDC ROUTES - Direct OAuth flow (for future migration from Camunda)
		// ========================================================================
		oauthGroup := publicAPI.Group("/oauth")
		{
			oauthGroup.POST("/logout", oauthHandler.OAuthLogout)
			oauthGroup.POST("/logout-all", oauthHandler.LogoutAll)
		}

		// ========================================================================
		// AUTHENTICATION ROUTES - Workflow-based (Workers handle Keycloak/DB)
		// ========================================================================
		authGroup := publicAPI.Group("/auth")
		{
			// ✅ KEYCLOAK UNIFIED LOGIN (Email/Password + Google + LinkedIn)
			authGroup.POST("/login", workflowHandler.StartKeycloakLogin)

			// ✅ NEW: Keycloak callback (backend-handled)
			authGroup.GET("/callback", workflowHandler.HandleKeycloakCallback)

			// Password Reset workflow (keep if needed)
			authGroup.POST("/password/reset", workflowHandler.StartPasswordReset)

			// ✅ CHECK EMAIL EXISTS (For forgot password template validation)
			authGroup.GET("/check-email", userHandler.CheckEmailExists)
		}

		// ========================================================================
		// PUBLIC ENTITY ROUTES (Dynamic :entityType for Franchises/Associations)
		// ========================================================================
		entityGroup := publicAPI.Group("/entities/:entityType")
		{
			entityGroup.GET("/home", franchiseHandler.GetHomePageData)
			entityGroup.GET("/listing", franchiseHandler.GetListingPageData)
			entityGroup.GET("/detail/:slug", franchiseHandler.GetFranchiseDetailPage)

			// Search & Browse
			entityGroup.GET("/search", franchiseHandler.SearchFranchises)
			entityGroup.GET("/suggest", franchiseHandler.GetSuggestions)

			// Categories & Industries
			entityGroup.GET("/industries", franchiseHandler.GetAllIndustries)
			entityGroup.GET("/industries/:slug", franchiseHandler.GetIndustryBySlug)
			entityGroup.GET("/categories", franchiseHandler.GetCategories)
			entityGroup.GET("/stats", franchiseHandler.GetStats)
			entityGroup.GET("/featured", franchiseHandler.GetFeatured)

			// ✅ Generic :id route MUST be LAST
			entityGroup.GET("/:id", franchiseHandler.GetByID)

			// Rating
			entityGroup.GET("/:id/ratings", franchiseHandler.GetFranchiseRatings)
		}

		// ========================================================================
		// CONTACT US ROUTE
		// ========================================================================
		publicAPI.POST("/contact", workflowHandler.StartContactUs)

		// ========================================================================
		// UNIFIED ONBOARDING SUBMISSION (V2)
		// ========================================================================
		publicAPI.POST("/onboarding/submit/:entityType",
			workflowHandler.StartUnifiedOnboarding)

		// ========================================================================
		// PUBLIC GENERIC FORMS (e.g., Buyer/Franchisor Registrations)
		// ========================================================================
		publicAPI.POST("/forms/:formType/submit", workflowHandler.StartFormSubmission)
	}

	// ============================================================================
	// PROTECTED API ROUTES (JWT Required)
	// ============================================================================
	protectedAPI := router.Group("/api/v1")
	// Rate limiter middleware - check if config has enabled flag
	//protectedAPI.Use(middleware.JWTAuth(cfg.Auth.JWT))
	protectedAPI.Use(middleware.SessionOrJWTAuth(cfg.Auth.JWT, redisClient.GetClient()))
	// protectedAPI.Use(middleware.CSRFProtection(redisClient.GetClient()))
	{
		// ========================================================================
		// USER MANAGEMENT
		// ========================================================================
		userGroup := protectedAPI.Group("/user")
		{
			// Complex operations - Workflows
			userGroup.PUT("/profile", workflowHandler.StartProfileUpdate)
			userGroup.DELETE("/account", workflowHandler.StartAccountDeletion)

			// Workflow completion status
			userGroup.GET("/workflow/:workflowId", workflowHandler.GetWorkflowCompletionStatus)

			// Profile
			userGroup.GET("/profile", userHandler.GetProfile)
			userGroup.GET("/session/validate", userHandler.ValidateSession)

			// Preferences
			userGroup.GET("/preferences", userHandler.GetPreferences)
			userGroup.GET("/preferences/options", workflowHandler.GetPreferenceOptions)

			// Audit Log
			userGroup.GET("/audit-log", userHandler.GetAuditLog)

			// Feedback
			userGroup.POST("/feedback", userHandler.SubmitFeedback)

			// Dashboard
			userGroup.GET("/dashboard", userHandler.GetDashboard)
		}

		// ========================================================================
		// UNIFIED ENTITY ROUTES (Authenticated) - Works for BOTH franchises & associations
		// ========================================================================
		protectedEntityGroup := protectedAPI.Group("/entities/:entityType")
		{
			// Search with personalization - Workflow
			protectedEntityGroup.POST("/search", workflowHandler.StartFranchiseSearch)
			protectedEntityGroup.GET("/details/:id", workflowHandler.GetFranchiseDetails)

			// User-specific operations - Direct handlers
			protectedEntityGroup.POST("/favorite/:id", franchiseHandler.AddToFavorites)
			protectedEntityGroup.DELETE("/favorite/:id", franchiseHandler.RemoveFromFavorites)
			protectedEntityGroup.GET("/favorites", franchiseHandler.GetFavorites)
			protectedEntityGroup.POST("/:id/bookmark", franchiseHandler.BookmarkFranchise)
			protectedEntityGroup.DELETE("/:id/bookmark", franchiseHandler.UnbookmarkFranchise)
			protectedEntityGroup.GET("/:id/bookmark/check", franchiseHandler.CheckBookmark)
			protectedEntityGroup.GET("/user/bookmarks", franchiseHandler.GetUserBookmarks)
			protectedEntityGroup.POST("/:id/rate", franchiseHandler.RateFranchise)
			protectedEntityGroup.PUT("/:id/rate", franchiseHandler.UpdateRating)
			protectedEntityGroup.DELETE("/:id/rate", franchiseHandler.DeleteRating)
			protectedEntityGroup.GET("/:id/my-rating", franchiseHandler.GetUserRating)
			protectedEntityGroup.POST("/:id/share", franchiseHandler.ShareFranchise)
			protectedEntityGroup.GET("/user/shares", franchiseHandler.GetUserShares)
			protectedEntityGroup.POST("/save-search", franchiseHandler.SaveSearch)
			protectedEntityGroup.GET("/saved-searches", franchiseHandler.GetSavedSearches)
			protectedEntityGroup.DELETE("/saved-searches/:id", franchiseHandler.DeleteSavedSearch)

			// CRUD Operations (via franchise-postgres worker)
			protectedEntityGroup.POST("/create", workflowHandler.CreateFranchise)
			protectedEntityGroup.PUT("/:id", workflowHandler.UpdateFranchise)
			protectedEntityGroup.DELETE("/:id", workflowHandler.DeleteFranchise)
			protectedEntityGroup.GET("/full/:slug", workflowHandler.GetFullFranchise)

			// Enquiry
			protectedEntityGroup.POST("/:id/enquiry", franchiseHandler.SubmitFranchiseEnquiry)

			// Website Verification
			protectedEntityGroup.POST("/:id/verify/website", franchiseHandler.VerifyWebsite)

			// Offerings
			protectedEntityGroup.GET("/:id/offerings", franchiseHandler.GetOfferings)
			protectedEntityGroup.POST("/:id/offerings", franchiseHandler.CreateOffering)
			protectedEntityGroup.PATCH("/offerings/:offeringId", franchiseHandler.UpdateOffering)
			protectedEntityGroup.DELETE("/offerings/:offeringId", franchiseHandler.DeleteOffering)
			protectedEntityGroup.POST("/offerings/:offeringId/publish", franchiseHandler.PublishOffering)
			protectedEntityGroup.POST("/offerings/:offeringId/unpublish", franchiseHandler.UnpublishOffering)
			protectedEntityGroup.POST("/offerings/:offeringId/redeem", franchiseHandler.RedeemOffering)

			// Memberships
			protectedEntityGroup.GET("/:id/memberships", franchiseHandler.GetMemberships)
			protectedEntityGroup.POST("/:id/memberships", franchiseHandler.RequestMembership)
			protectedEntityGroup.PATCH("/memberships/:membershipId", franchiseHandler.UpdateMembershipStatus)
		}

		// ========================================================================
		// PLATFORM ADMIN ENTITY ROUTES
		// ========================================================================
		adminEntityGroup := protectedAPI.Group("/admin")
		adminEntityGroup.Use(middleware.RequireRole("admin"))
		{
			adminEntityGroup.GET("/entities/:entityType", franchiseHandler.ListReviewQueue)
			adminEntityGroup.PATCH("/entities/:entityType/:id/approve", franchiseHandler.ApproveEntity)
			adminEntityGroup.PATCH("/entities/:entityType/:id/reject", franchiseHandler.RejectEntity)
			adminEntityGroup.PATCH("/entities/:entityType/:id/publish", franchiseHandler.PublishEntity)
			adminEntityGroup.PATCH("/entities/:entityType/:id/suspend", franchiseHandler.SuspendEntity)
			adminEntityGroup.PATCH("/entities/:entityType/:id/reinstate", franchiseHandler.ReinstateEntity)
			adminEntityGroup.PATCH("/entities/:entityType/:id/archive", franchiseHandler.ArchiveEntity)

			adminEntityGroup.PATCH("/pending-edits/:editId/approve", franchiseHandler.ApprovePendingEdit)
			adminEntityGroup.PATCH("/pending-edits/:editId/reject", franchiseHandler.RejectPendingEdit)

			adminEntityGroup.PATCH("/duplicate-flags/:flagId/resolve", franchiseHandler.ResolveDuplicateFlag)
		}


		// ========================================================================
		// DOCUMENTS WORKFLOWS
		// ========================================================================
		documentGroup := protectedAPI.Group("/documents")
		{
			documentGroup.GET("/presigned-url", documentHandler.GeneratePresignedURL)
		}

		// ========================================================================
		// APPLICATION WORKFLOWS
		// ========================================================================
		applicationGroup := protectedAPI.Group("/applications")
		{
			// All application operations use workflows
			applicationGroup.POST("/submit", workflowHandler.StartApplicationProcessing)
			applicationGroup.POST("/approve", workflowHandler.StartActivityApproval)

			// Status/list endpoints (create direct handlers later)
			applicationGroup.GET("/list", placeholderHandler("GET /applications/list"))
			applicationGroup.GET("/:id", placeholderHandler("GET /applications/:id"))
			applicationGroup.GET("/status/:id", placeholderHandler("GET /applications/status/:id"))
		}

		// ========================================================================

		// ========================================================================
		// OAUTH ME ENDPOINT (Get current user info)
		// ========================================================================
		oauthMeGroup := protectedAPI.Group("/oauth")
		{
			oauthMeGroup.GET("/me", oauthHandler.GetCurrentUser)
		}
	}

	// ============================================================================
	// OPERATE LIVE-MONITORING ROUTES
	// ============================================================================
	operateGroup := router.Group("/operate")
	{
		operateHandler.RegisterRoutes(operateGroup)
	}

	// ============================================================================
	// ADMIN ROUTES (JWT + Admin Role Required)
	// ============================================================================
	adminAPI := router.Group("/internal")
	adminAPI.Use(middleware.SessionOrJWTAuth(cfg.Auth.JWT, redisClient.GetClient()))
	adminAPI.Use(middleware.RequireRole("admin"))
	// adminAPI.Use(middleware.CSRFProtection(redisClient.GetClient()))
	{
		// Workflow management
		workflowGroup := adminAPI.Group("/workflows")
		{
			workflowGroup.GET("", workflowHandler.ListWorkflows)
			workflowGroup.GET("/:id/status", workflowHandler.GetWorkflowStatus)
			workflowGroup.POST("/:id/cancel", workflowHandler.CancelWorkflow)
		}

		// Analytics & monitoring
		adminAPI.GET("/analytics", placeholderHandler("GET /admin/analytics"))
		adminAPI.GET("/users", placeholderHandler("GET /admin/users"))
		adminAPI.GET("/system/health", placeholderHandler("GET /admin/system/health"))
	}

	// ============================================================================
	// NO ROUTE / NO METHOD HANDLERS (catch-all for unmatched routes)
	// ============================================================================
	router.NoRoute(func(c *gin.Context) {
		requestID := c.GetString("requestId")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.JSON(http.StatusNotFound, gin.H{
			"success":   false,
			"error":     http.StatusText(http.StatusNotFound),
			"message":   "Route not found",
			"requestId": requestID,
		})
	})

	router.NoMethod(func(c *gin.Context) {
		requestID := c.GetString("requestId")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"success":   false,
			"error":     http.StatusText(http.StatusMethodNotAllowed),
			"message":   "Method not allowed",
			"requestId": requestID,
		})
	})

	// URL Rewrite Middleware for Dynamic Entity Routes (to support /api/v1/:entityType/ routes externally)
	rewriteHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/api/v1/") {
			subPath := strings.TrimPrefix(path, "/api/v1/")
			segments := strings.Split(subPath, "/")
			if len(segments) > 0 && segments[0] != "" {
				firstSeg := segments[0]
				reserved := map[string]bool{
					"auth":         true,
					"oauth":        true,
					"contact":      true,
					"onboarding":   true,
					"forms":        true,
					"ai":           true,
					"user":         true,
					"documents":    true,
					"applications": true,
					"entities":     true,
					"admin":        true,
				}
				if !reserved[firstSeg] {
					r.URL.Path = "/api/v1/entities/" + subPath
				}
			}
		}
		router.ServeHTTP(w, r)
	})

	// Start HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.API.Port),
		Handler:      rewriteHandler,
		ReadTimeout:  time.Duration(cfg.API.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.API.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.API.IdleTimeout) * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Info("🚀 Lemici API Gateway started", map[string]interface{}{
			"port":    cfg.API.Port,
			"address": srv.Addr,
		})

		printRoutesSummary(log, cfg.API.Port)

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("Server failed", map[string]interface{}{
				"error": err.Error(),
			})
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down gracefully...", map[string]interface{}{})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error("Server forced shutdown", map[string]interface{}{
			"error": err.Error(),
		})
	}

	log.Info("✅ Server stopped successfully", map[string]interface{}{})
}

func healthCheckHandler(cfg *config.Config, pg *database.PostgresClient, redis *database.RedisClient, es *database.ElasticsearchClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		health := gin.H{
			"status":      "healthy",
			"service":     "lemici-api-gateway",
			"version":     cfg.App.Version,
			"environment": cfg.App.Environment,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		}

		// Check database connections
		dependencies := gin.H{}

		// Check PostgreSQL
		if err := pg.Ping(c.Request.Context()); err != nil {
			dependencies["postgres"] = gin.H{"status": "unhealthy", "error": err.Error()}
			health["status"] = "degraded"
		} else {
			dependencies["postgres"] = gin.H{"status": "healthy"}
		}

		// Check Redis
		if err := redis.Ping(c.Request.Context()); err != nil {
			dependencies["redis"] = gin.H{"status": "unhealthy", "error": err.Error()}
			health["status"] = "degraded"
		} else {
			dependencies["redis"] = gin.H{"status": "healthy"}
		}

		// Check Elasticsearch
		if err := es.Ping(); err != nil {
			dependencies["elasticsearch"] = gin.H{"status": "unhealthy", "error": err.Error()}
			health["status"] = "degraded"
		} else {
			dependencies["elasticsearch"] = gin.H{"status": "healthy"}
		}

		health["dependencies"] = dependencies

		statusCode := http.StatusOK
		if health["status"] == "degraded" {
			statusCode = http.StatusServiceUnavailable
		}

		c.JSON(statusCode, health)
	}
}

func metricsHandler() gin.HandlerFunc {
	startTime := time.Now()
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"uptime":    time.Since(startTime).String(),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}
}

func placeholderHandler(endpoint string) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetString("requestId")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.JSON(http.StatusNotImplemented, gin.H{
			"message":   fmt.Sprintf("%s - Handler not implemented yet", endpoint),
			"status":    "not_implemented",
			"note":      "Create the corresponding handler to implement this endpoint",
			"requestId": requestID,
		})
	}
}

func printRoutesSummary(_ logger.Logger, port int) {
	routes := []string{
		"\n📋 ═══════════════════════════════════════════════════════════════",
		"   LEMICI API GATEWAY - ROUTES ARCHITECTURE",
		"═══════════════════════════════════════════════════════════════\n",
		"🔓 PUBLIC ROUTES (No Authentication):",
		"",
		"  🔐 Auth Workflows (API → Camunda → Workers → Keycloak/DB):",
		fmt.Sprintf("    POST http://localhost:%d/api/v1/auth/login", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/auth/logout", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/auth/password/reset", port),
		"",
		"  🏢 Franchise Discovery & 🏠 Homepage (Direct Handlers):",
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/home", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/listing", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/detail/:slug", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/search", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/industries", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/industries/:slug", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/categories", port),

		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/suggest", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/stats", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/featured", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/:id", port),
		"",
		"  📧 Enquiries:",
		fmt.Sprintf("    POST http://localhost:%d/api/v1/enquiries", port),
		"",
		"🔒 PROTECTED ROUTES (Requires JWT Token):",
		"",
		"  🤖 AI Workflows (API → Camunda → Workers):",
		fmt.Sprintf("    POST http://localhost:%d/api/v1/ai/query", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/ai/discovery", port),
		"",
		"  👤 User Management:",
		fmt.Sprintf("    PUT  http://localhost:%d/api/v1/user/profile (workflow)", port),
		fmt.Sprintf("    DEL  http://localhost:%d/api/v1/user/account (workflow)", port),
		"",
		"  🏢 Franchise Operations:",
		fmt.Sprintf("    POST http://localhost:%d/api/v1/franchises/search (workflow)", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/franchises/favorite/:id (direct)", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/favorites (direct)", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/franchises/:id/bookmark", port),
		fmt.Sprintf("    DEL  http://localhost:%d/api/v1/franchises/:id/bookmark", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/:id/bookmark/check", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/user/bookmarks", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/franchises/:id/rate", port),
		fmt.Sprintf("    PUT  http://localhost:%d/api/v1/franchises/:id/rate", port),
		fmt.Sprintf("    DEL  http://localhost:%d/api/v1/franchises/:id/rate", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/:id/my-rating", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/:id/ratings (public)", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/franchises/:id/share", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/user/shares", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/franchises/save-search (direct)", port),
		"",
		"  🏗️  Franchise CRUD (Workflow-based):",
		fmt.Sprintf("    POST http://localhost:%d/api/v1/franchises/create", port),
		fmt.Sprintf("    PUT  http://localhost:%d/api/v1/franchises/:id", port),
		fmt.Sprintf("    DEL  http://localhost:%d/api/v1/franchises/:id", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/franchises/full/:slug", port),
		"",
		"  📝 Application Workflows:",
		fmt.Sprintf("    POST http://localhost:%d/api/v1/applications/submit", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/applications/approve", port),
		"",
		"  📧 Email & CRM Workflows:",
		fmt.Sprintf("    POST http://localhost:%d/api/v1/crm/sync", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/email/campaign", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/email/welcome-series", port),
		"",
		"🛡️  ADMIN ROUTES (JWT + admin role, IP-restricted via Kong):",
		fmt.Sprintf("    GET  http://localhost:%d/internal/workflows", port),
		fmt.Sprintf("    GET  http://localhost:%d/internal/workflows/:id/status", port),
		fmt.Sprintf("    POST http://localhost:%d/internal/workflows/:id/cancel", port),
		"",
		"❤️  HEALTH & METRICS:",
		fmt.Sprintf("    GET  http://localhost:%d/health", port),
		fmt.Sprintf("    GET  http://localhost:%d/metrics", port),
		"\n═══════════════════════════════════════════════════════════════",
		"",
		"📊 ARCHITECTURE:",
		"  • Auth operations: API → Camunda Workflow → Workers → Keycloak/DB",
		"  • Business processes: API → Camunda Workflow → Workers",
		"  • Simple queries: API → Direct Handler → Database",
		"  • Workers handle all Keycloak/PostgreSQL/Redis interactions",
		"\n═══════════════════════════════════════════════════════════════\n",
	}

	for _, route := range routes {
		fmt.Println(route)
	}
}
