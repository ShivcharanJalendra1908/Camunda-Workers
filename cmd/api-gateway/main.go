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

	"camunda-workers/internal/api/handlers"
	"camunda-workers/internal/api/middleware"
	"camunda-workers/internal/common/auth"
	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/logger"

	"github.com/gin-gonic/gin"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		panic(fmt.Sprintf("Failed to load config: %v", err))
	}

	// Initialize logger
	log := logger.NewStructured(cfg.Logging.Level, cfg.Logging.Format)
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
	)

	log.Info("Connected to Keycloak", map[string]interface{}{
		"url":   cfg.Auth.Keycloak.URL,
		"realm": cfg.Auth.Keycloak.Realm,
	})

	// Initialize Camunda client
    fmt.Printf("🔧 Connecting to Camunda at: %s\n", cfg.Camunda.BrokerAddress)
    camundaClient, err := camunda.NewClientFromEnv()
	//camundaClient, err := camunda.NewClient(cfg.Camunda.BrokerAddress)
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

	// Redis address - use the Address field from config
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

	// Global middleware
	router.Use(gin.Recovery())
	router.Use(middleware.RequestID())
	router.Use(middleware.Logger(log))
	router.Use(middleware.CORS(cfg.API.CORS))
	router.Use(middleware.SecurityHeaders())

	// Public routes (no authentication required)
	router.GET("/health", healthCheckHandler(cfg, postgresDB, redisClient, esClient))
    router.HEAD("/health", healthCheckHandler(cfg, postgresDB, redisClient, esClient))

	router.GET("/metrics", metricsHandler())

	// Initialize handlers
	workflowHandler := handlers.NewWorkflowHandler(camundaClient, log)
	franchiseHandler := handlers.NewFranchiseHandler(esClient, postgresDB, log)

	// ============================================================================
	// PUBLIC API ROUTES (No JWT Required)
	// ============================================================================
	publicAPI := router.Group("/api/v1/public")
	{
		// ========================================================================
		// AUTHENTICATION ROUTES - Workflow-based (Workers handle Keycloak/DB)
		// ========================================================================
		authGroup := publicAPI.Group("/auth")
		{
			// Google OAuth workflows
			authGroup.POST("/google/signup", workflowHandler.StartGoogleSignup)
			authGroup.POST("/google/signin", workflowHandler.StartGoogleSignin)

			// LinkedIn OAuth workflows
			authGroup.POST("/linkedin/signup", workflowHandler.StartLinkedInSignup)
			authGroup.POST("/linkedin/signin", workflowHandler.StartLinkedInSignin)

			// Email/Password Auth workflows
			authGroup.POST("/login", workflowHandler.StartUserSignin)
			authGroup.POST("/signup", workflowHandler.StartUserSignup)

			// Password Reset workflow
			authGroup.POST("/password/reset", workflowHandler.StartPasswordReset)

			// Logout workflow
			authGroup.POST("/logout", workflowHandler.StartUserLogout)

			// NOTE: Token refresh should ideally be a direct operation (not workflow)
			// Create a direct handler for this if needed:
			// authGroup.POST("/refresh", authHandler.HandleTokenRefresh)
		}

		// ========================================================================
		// PUBLIC FRANCHISE ROUTES - Direct handlers (no auth required)
		// ========================================================================
		franchiseGroup := publicAPI.Group("/franchises")
		{
			// Direct handlers for public browsing (existing handlers)
			franchiseGroup.GET("/search", franchiseHandler.SearchFranchises)
			franchiseGroup.GET("/suggest", franchiseHandler.GetSuggestions)
			franchiseGroup.GET("/stats", franchiseHandler.GetStats)
			franchiseGroup.GET("/:id", franchiseHandler.GetByID)
			franchiseGroup.GET("/categories", franchiseHandler.GetCategories)
			franchiseGroup.GET("/featured", franchiseHandler.GetFeatured)
		}
	}

	// ============================================================================
	// PROTECTED API ROUTES (JWT Required)
	// ============================================================================
	protectedAPI := router.Group("/api/v1")
	// Rate limiter middleware - check if config has enabled flag
	if cfg.API.RateLimit.Enabled {
		// ✅ FIXED: Pass the entire RateLimitConfig struct instead of individual fields
		protectedAPI.Use(middleware.RateLimiter(cfg.API.RateLimit))
	}
	protectedAPI.Use(middleware.JWTAuth(cfg.Auth.JWT))
	{
		// ========================================================================
		// AI CONVERSATION WORKFLOWS
		// ========================================================================
		aiGroup := protectedAPI.Group("/ai")
		{
			aiGroup.POST("/query", workflowHandler.StartAIQuery)
			aiGroup.POST("/discovery", workflowHandler.StartDiscovery)
		}

		// ========================================================================
		// USER MANAGEMENT
		// ========================================================================
		userGroup := protectedAPI.Group("/user")
		{
			// Simple CRUD - Direct handlers (create user_handler.go later)
			// userGroup.GET("/profile", userHandler.GetProfile)
			// userGroup.GET("/preferences", userHandler.GetPreferences)

			// Complex operations - Workflows
			userGroup.PUT("/profile", workflowHandler.StartProfileUpdate)
			userGroup.DELETE("/account", workflowHandler.StartAccountDeletion)

			// Temporary placeholders
			userGroup.GET("/profile", placeholderHandler("GET /user/profile"))
			userGroup.GET("/preferences", placeholderHandler("GET /user/preferences"))
		}

		// ========================================================================
		// FRANCHISE WORKFLOWS (Authenticated operations)
		// ========================================================================
		franchiseGroup := protectedAPI.Group("/franchises")
		{
			// Complex search with personalization - Workflow
			franchiseGroup.POST("/search", workflowHandler.StartFranchiseSearch)
			franchiseGroup.GET("/details/:id", workflowHandler.GetFranchiseDetails)

			// Simple user-specific operations - Direct handlers
			franchiseGroup.POST("/favorite/:id", franchiseHandler.AddToFavorites)
			franchiseGroup.DELETE("/favorite/:id", franchiseHandler.RemoveFromFavorites)
			franchiseGroup.GET("/favorites", franchiseHandler.GetFavorites)
			franchiseGroup.POST("/save-search", franchiseHandler.SaveSearch)
			franchiseGroup.GET("/saved-searches", franchiseHandler.GetSavedSearches)
			franchiseGroup.DELETE("/saved-searches/:id", franchiseHandler.DeleteSavedSearch)
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
		// CRM WORKFLOWS
		// ========================================================================
		crmGroup := protectedAPI.Group("/crm")
		{
			crmGroup.POST("/sync", workflowHandler.StartCRMSync)
		}

		// ========================================================================
		// EMAIL WORKFLOWS
		// ========================================================================
		emailGroup := protectedAPI.Group("/email")
		{
			emailGroup.POST("/campaign", workflowHandler.StartEmailCampaign)
			emailGroup.POST("/welcome-series", workflowHandler.StartWelcomeSeries)
		}

		// ========================================================================
		// SOCIAL AUTH ORCHESTRATION WORKFLOW
		// ========================================================================
		socialGroup := protectedAPI.Group("/social")
		{
			socialGroup.POST("/auth", workflowHandler.StartSocialAuthOrchestration)
		}

		// ========================================================================
		// ERROR HANDLING WORKFLOW
		// ========================================================================
		protectedAPI.POST("/error/handle", workflowHandler.StartErrorHandling)
	}

	// ============================================================================
	// ADMIN ROUTES (JWT + Admin Role Required)
	// ============================================================================
	adminAPI := router.Group("/api/admin")
	// Rate limiter for admin - always enabled but stricter
	// ✅ FIXED: Create a RateLimitConfig struct for admin
	adminRateLimit := config.RateLimitConfig{
		Enabled:           true,
		RequestsPerSecond: 30,
		Burst:             50,
	}
	adminAPI.Use(middleware.RateLimiter(adminRateLimit))
	adminAPI.Use(middleware.JWTAuth(cfg.Auth.JWT))
	adminAPI.Use(middleware.RequireRole("admin"))
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

	// Start HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.API.Port),
		Handler:      router,
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
		c.JSON(http.StatusNotImplemented, gin.H{
			"message": fmt.Sprintf("%s - Handler not implemented yet", endpoint),
			"status":  "not_implemented",
			"note":    "Create the corresponding handler to implement this endpoint",
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
		fmt.Sprintf("    POST http://localhost:%d/api/v1/public/auth/google/signup", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/public/auth/google/signin", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/public/auth/linkedin/signup", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/public/auth/linkedin/signin", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/public/auth/login", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/public/auth/signup", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/public/auth/logout", port),
		fmt.Sprintf("    POST http://localhost:%d/api/v1/public/auth/password/reset", port),
		"",
		"  🏢 Franchise Discovery (Direct Handlers - No Workflow):",
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/public/franchises/search", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/public/franchises/suggest", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/public/franchises/stats", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/public/franchises/:id", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/public/franchises/categories", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/v1/public/franchises/featured", port),
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
		fmt.Sprintf("    POST http://localhost:%d/api/v1/franchises/save-search (direct)", port),
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
		"🛡️  ADMIN ROUTES (JWT + admin role):",
		fmt.Sprintf("    GET  http://localhost:%d/api/admin/workflows", port),
		fmt.Sprintf("    GET  http://localhost:%d/api/admin/workflows/:id/status", port),
		fmt.Sprintf("    POST http://localhost:%d/api/admin/workflows/:id/cancel", port),
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
