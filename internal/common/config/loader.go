package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Load loads configuration from standard locations
func Load() (*Config, error) {
	loadEnvFile()

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./configs")
	viper.AddConfigPath("../../configs")
	viper.AddConfigPath(".")

	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()

	env := os.Getenv("APP_ENVIRONMENT")
	if env == "" {
		env = "development"
	}

	// 1️⃣ LOAD BASE CONFIG
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading base config: %w", err)
		}
	}

	// 2️⃣ LOAD ENV CONFIG
	envConfigFile := fmt.Sprintf("config.%s", env)
	viper.SetConfigName(envConfigFile)
	_ = viper.MergeInConfig()

	// 3️⃣ EXPAND ENV PLACEHOLDERS
	expandEnvVarsWithDefault(viper.GetViper())

	// 4️⃣ Unmarshal final config
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	applyDefaults(&cfg)

	// 5️⃣ DIRECT OVERRIDE FROM ENV VARS (Critical for Docker)
	overrideEmptyConfig(&cfg)

	// ✅ FIX: Set GatewayAddress from BrokerAddress if not set
	if cfg.Camunda.GatewayAddress == "" && cfg.Camunda.BrokerAddress != "" {
		cfg.Camunda.GatewayAddress = cfg.Camunda.BrokerAddress
	}

	if cfg.Database.Elasticsearch.URL == "" && len(cfg.Database.Elasticsearch.Addresses) > 0 {
		cfg.Database.Elasticsearch.URL = cfg.Database.Elasticsearch.Addresses[0]
	}

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// LoadConfig is an alias for Load for backward compatibility
func LoadConfig(path string) (*Config, error) {
	if path != "" && path != "configs/config.yaml" {
		return LoadFromFile(path)
	}
	return Load()
}

// loadEnvFile loads .env from multiple possible locations
func loadEnvFile() {
	// Get current working directory
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}

	possiblePaths := []string{
		".env",
		"../.env",
		"../../.env",
		filepath.Join(wd, ".env"),
		filepath.Join(wd, "..", ".env"),
		filepath.Join(wd, "..", "..", ".env"),
		filepath.Join(wd, "configs", ".env"),
	}

	// Try to find project root
	if rootDir := findProjectRoot(); rootDir != "" {
		possiblePaths = append(possiblePaths, filepath.Join(rootDir, ".env"))
	}

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			if err := godotenv.Load(path); err == nil {
				fmt.Printf("✅ Loaded .env from: %s\n", path)
				return
			}
		}
	}

	fmt.Printf("⚠️  .env file not found, using system environment variables\n")
}

// findProjectRoot finds project root by looking for go.mod
func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return ""
}

// expandEnvVarsWithDefault expands ${VAR:default} syntax and handles integer conversion
func expandEnvVarsWithDefault(v *viper.Viper) {
	for _, key := range v.AllKeys() {
		val := v.Get(key)

		if strVal, ok := val.(string); ok {
			if strings.Contains(strVal, "${") || (strings.HasPrefix(strVal, "$") && len(strVal) > 1) {
				expanded := expandEnvVarWithDefault(strVal)
				if expanded != strVal && expanded != "" {
					// Check if this is an integer field
					if isIntegerField(key) {
						if intVal, err := strconv.Atoi(expanded); err == nil {
							v.Set(key, intVal)
						} else {
							v.Set(key, expanded)
						}
					} else {
						v.Set(key, expanded)
					}
				}
			}
		}
	}
}

// expandEnvVarWithDefault handles ${VAR:default} syntax
func expandEnvVarWithDefault(s string) string {
	// Handle ${VAR:default} syntax
	if strings.HasPrefix(s, "${") && strings.Contains(s, "}") {
		end := strings.Index(s, "}")
		if end == -1 {
			return s
		}

		content := s[2:end]
		varName := content
		defaultValue := ""

		// Check for default value syntax
		if colonIdx := strings.Index(content, ":"); colonIdx != -1 {
			varName = content[:colonIdx]
			defaultValue = content[colonIdx+1:]
		}

		// Get environment variable
		envValue := os.Getenv(varName)
		if envValue != "" {
			return envValue
		}

		// Return default if no env var
		return defaultValue
	}

	// Handle simple $VAR syntax
	return os.ExpandEnv(s)
}

// isIntegerField checks if a config key should have an integer value
func isIntegerField(key string) bool {
	integerFields := []string{
		"port", "db", "timeout", "max_", "default_limit", "max_limit",
		"cache_ttl", "expiry", "ttl", "interval", "maxretries", "retry",
		"maxconnections", "maxidle", "poolsize", "dialtimeout", "readtimeout",
		"writetimeout", "minutes", "seconds", "hours", "days",
	}

	keyLower := strings.ToLower(key)
	for _, field := range integerFields {
		if strings.Contains(keyLower, field) {
			return true
		}
	}
	return false
}

// overrideEmptyConfig directly overrides empty config values from env vars
func overrideEmptyConfig(cfg *Config) {
	// ============================================================================
	// DATABASE CONFIGURATION (CRITICAL FOR DOCKER)
	// ============================================================================

	// PostgreSQL
	if cfg.Database.Postgres.Host == "" {
		if val := os.Getenv("POSTGRES_HOST"); val != "" {
			cfg.Database.Postgres.Host = val
		}
	}
	if cfg.Database.Postgres.Port == 0 {
		if val := os.Getenv("POSTGRES_PORT"); val != "" {
			if port, err := strconv.Atoi(val); err == nil {
				cfg.Database.Postgres.Port = port
			}
		}
	}
	if cfg.Database.Postgres.Database == "" {
		if val := os.Getenv("POSTGRES_DB"); val != "" {
			cfg.Database.Postgres.Database = val
		}
	}
	if cfg.Database.Postgres.User == "" {
		if val := os.Getenv("POSTGRES_USER"); val != "" {
			cfg.Database.Postgres.User = val
		}
	}
	if cfg.Database.Postgres.Username == "" {
		if val := os.Getenv("POSTGRES_USER"); val != "" {
			cfg.Database.Postgres.Username = val
		}
	}
	if cfg.Database.Postgres.Password == "" {
		if val := os.Getenv("POSTGRES_PASSWORD"); val != "" {
			cfg.Database.Postgres.Password = val
		}
	}

	// Elasticsearch - THIS IS THE KEY FIX
	if cfg.Database.Elasticsearch.URL == "" {
		if val := os.Getenv("ELASTICSEARCH_URL"); val != "" {
			cfg.Database.Elasticsearch.URL = val
			// Also update Addresses array
			cfg.Database.Elasticsearch.Addresses = []string{val}
		}
	}
	// If URL is set but Addresses is empty, populate it
	if cfg.Database.Elasticsearch.URL != "" && len(cfg.Database.Elasticsearch.Addresses) == 0 {
		cfg.Database.Elasticsearch.Addresses = []string{cfg.Database.Elasticsearch.URL}
	}
	// If Addresses is set but URL is empty, use first address
	if cfg.Database.Elasticsearch.URL == "" && len(cfg.Database.Elasticsearch.Addresses) > 0 {
		cfg.Database.Elasticsearch.URL = cfg.Database.Elasticsearch.Addresses[0]
	}

	// Redis
	if cfg.Database.Redis.Address == "" {
		if val := os.Getenv("REDIS_ADDRESS"); val != "" {
			cfg.Database.Redis.Address = val
		}
	}
	if cfg.Database.Redis.DB == 0 {
		if val := os.Getenv("REDIS_DB"); val != "" {
			if db, err := strconv.Atoi(val); err == nil {
				cfg.Database.Redis.DB = db
			}
		}
	}

	// ============================================================================
	// CAMUNDA CONFIGURATION
	// ============================================================================

	if cfg.Camunda.BrokerAddress == "" {
		if val := os.Getenv("ZEEBE_ADDRESS"); val != "" {
			cfg.Camunda.BrokerAddress = val
		}
	}
	if cfg.Camunda.GatewayAddress == "" {
		if val := os.Getenv("ZEEBE_ADDRESS"); val != "" {
			cfg.Camunda.GatewayAddress = val
		}
	}

	// ============================================================================
	// JWT CONFIGURATION
	// ============================================================================

	if cfg.Auth.JWT.Secret == "" {
		if val := os.Getenv("JWT_SECRET"); val != "" {
			cfg.Auth.JWT.Secret = val
		}
	}

	// ============================================================================
	// OAUTH PROVIDERS
	// ============================================================================

	// Google OAuth
	if cfg.Auth.OAuthProviders.Google.ClientID == "" {
		if val := os.Getenv("GOOGLE_CLIENT_ID"); val != "" {
			cfg.Auth.OAuthProviders.Google.ClientID = val
		}
	}
	if cfg.Auth.OAuthProviders.Google.ClientSecret == "" {
		if val := os.Getenv("GOOGLE_CLIENT_SECRET"); val != "" {
			cfg.Auth.OAuthProviders.Google.ClientSecret = val
		}
	}
	if cfg.Auth.OAuthProviders.Google.RedirectURL == "" {
		if val := os.Getenv("GOOGLE_REDIRECT_URI"); val != "" {
			cfg.Auth.OAuthProviders.Google.RedirectURL = val
		}
	}

	// LinkedIn OAuth
	if cfg.Auth.OAuthProviders.LinkedIn.ClientID == "" {
		if val := os.Getenv("LINKEDIN_CLIENT_ID"); val != "" {
			cfg.Auth.OAuthProviders.LinkedIn.ClientID = val
		}
	}
	if cfg.Auth.OAuthProviders.LinkedIn.ClientSecret == "" {
		if val := os.Getenv("LINKEDIN_CLIENT_SECRET"); val != "" {
			cfg.Auth.OAuthProviders.LinkedIn.ClientSecret = val
		}
	}
	if cfg.Auth.OAuthProviders.LinkedIn.RedirectURL == "" {
		if val := os.Getenv("LINKEDIN_REDIRECT_URI"); val != "" {
			cfg.Auth.OAuthProviders.LinkedIn.RedirectURL = val
		}
	}

	// ============================================================================
	// KEYCLOAK CONFIGURATION
	// ============================================================================

	if cfg.Auth.Keycloak.URL == "" {
		if val := os.Getenv("KEYCLOAK_URL"); val != "" {
			cfg.Auth.Keycloak.URL = val
		}
	}
	if cfg.Auth.Keycloak.Realm == "" {
		if val := os.Getenv("KEYCLOAK_REALM"); val != "" {
			cfg.Auth.Keycloak.Realm = val
		}
	}
	if cfg.Auth.Keycloak.ClientID == "" {
		if val := os.Getenv("KEYCLOAK_CLIENT_ID"); val != "" {
			cfg.Auth.Keycloak.ClientID = val
		}
	}
	if cfg.Auth.Keycloak.ClientSecret == "" {
		if val := os.Getenv("KEYCLOAK_CLIENT_SECRET"); val != "" {
			cfg.Auth.Keycloak.ClientSecret = val
		}
	}
	if cfg.Auth.Keycloak.AdminClientID == "" {
		if val := os.Getenv("KEYCLOAK_ADMIN_CLIENT_ID"); val != "" {
			cfg.Auth.Keycloak.AdminClientID = val
		}
		// Fallback to regular client if admin not set
		if cfg.Auth.Keycloak.AdminClientID == "" {
			cfg.Auth.Keycloak.AdminClientID = cfg.Auth.Keycloak.ClientID
		}
	}
	if cfg.Auth.Keycloak.AdminClientSecret == "" {
		if val := os.Getenv("KEYCLOAK_ADMIN_CLIENT_SECRET"); val != "" {
			cfg.Auth.Keycloak.AdminClientSecret = val
		}
		// Fallback to regular secret if admin not set
		if cfg.Auth.Keycloak.AdminClientSecret == "" {
			cfg.Auth.Keycloak.AdminClientSecret = cfg.Auth.Keycloak.ClientSecret
		}
	}

	// PublicBaseURL for browser-facing Keycloak URL
	if cfg.Auth.Keycloak.PublicBaseURL == "" {
		if val := os.Getenv("KEYCLOAK_PUBLIC_URL"); val != "" {
			cfg.Auth.Keycloak.PublicBaseURL = val
		}
	}
	// Always override PublicBaseURL from env if set (not just when empty)
	if val := os.Getenv("KEYCLOAK_PUBLIC_URL"); val != "" {
		cfg.Auth.Keycloak.PublicBaseURL = val
	}

	// ============================================================================
	// EXTERNAL INTEGRATIONS
	// ============================================================================

	// Zoho CRM
	if cfg.Integrations.Zoho.APIKey == "" {
		if val := os.Getenv("ZOHO_CRM_API_KEY"); val != "" {
			cfg.Integrations.Zoho.APIKey = val
		}
	}
	if cfg.Integrations.Zoho.AuthToken == "" {
		if val := os.Getenv("ZOHO_CRM_OAUTH_TOKEN"); val != "" {
			cfg.Integrations.Zoho.AuthToken = val
		}
	}

	// SMTP
	if cfg.Integrations.SMTP.Host == "" {
		if val := os.Getenv("SMTP_HOST"); val != "" {
			cfg.Integrations.SMTP.Host = val
		}
	}
	if cfg.Integrations.SMTP.Port == 0 {
		if val := os.Getenv("SMTP_PORT"); val != "" {
			if port, err := strconv.Atoi(val); err == nil {
				cfg.Integrations.SMTP.Port = port
			}
		}
	}

	// ============================================================================
	// EXTERNAL APIs
	// ============================================================================

	// GenAI API
	if cfg.APIs.GenAI.APIKey == "" {
		if val := os.Getenv("GENAI_API_KEY"); val != "" {
			cfg.APIs.GenAI.APIKey = val
		}
	}
	if cfg.APIs.GenAI.BaseURL == "" {
		if val := os.Getenv("GENAI_SERVICE_URL"); val != "" {
			cfg.APIs.GenAI.BaseURL = val
		}
	}

	// Web Search API
	if cfg.APIs.WebSearch.APIKey == "" {
		if val := os.Getenv("WEB_SEARCH_API_KEY"); val != "" {
			cfg.APIs.WebSearch.APIKey = val
		}
	}
	if cfg.APIs.WebSearch.EngineID == "" {
		if val := os.Getenv("WEB_SEARCH_ENGINE_ID"); val != "" {
			cfg.APIs.WebSearch.EngineID = val
		}
	}

	// ============================================================================
	// API CONFIGURATION
	// ============================================================================

	if cfg.API.Port == 0 {
		if val := os.Getenv("API_PORT"); val != "" {
			if port, err := strconv.Atoi(val); err == nil {
				cfg.API.Port = port
			}
		}
	}

	// ============================================================================
	// CORS CONFIGURATION
	// ============================================================================
	if len(cfg.API.CORS.AllowOrigins) == 0 {
		if val := os.Getenv("API_CORS_ALLOWED_ORIGINS"); val != "" {
			cfg.API.CORS.AllowOrigins = strings.Split(val, ",")
			// Trim whitespace from each origin
			for i, origin := range cfg.API.CORS.AllowOrigins {
				cfg.API.CORS.AllowOrigins[i] = strings.TrimSpace(origin)
			}
		}
	}
	if len(cfg.API.CORS.AllowMethods) == 0 {
		if val := os.Getenv("API_CORS_ALLOWED_METHODS"); val != "" {
			cfg.API.CORS.AllowMethods = strings.Split(val, ",")
			for i, method := range cfg.API.CORS.AllowMethods {
				cfg.API.CORS.AllowMethods[i] = strings.TrimSpace(method)
			}
		}
	}
	if len(cfg.API.CORS.AllowHeaders) == 0 {
		if val := os.Getenv("API_CORS_ALLOWED_HEADERS"); val != "" {
			cfg.API.CORS.AllowHeaders = strings.Split(val, ",")
			for i, header := range cfg.API.CORS.AllowHeaders {
				cfg.API.CORS.AllowHeaders[i] = strings.TrimSpace(header)
			}
		}
	}
}

// LoadFromFile loads configuration from a specific file path
func LoadFromFile(path string) (*Config, error) {
	loadEnvFile()

	viper.SetConfigFile(path)
	viper.SetConfigType("yaml")

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	expandEnvVarsWithDefault(viper.GetViper())

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	applyDefaults(&cfg)
	overrideEmptyConfig(&cfg)

	// ✅ FIX: Set GatewayAddress from BrokerAddress if not set
	if cfg.Camunda.GatewayAddress == "" && cfg.Camunda.BrokerAddress != "" {
		cfg.Camunda.GatewayAddress = cfg.Camunda.BrokerAddress
	}

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// applyDefaults sets default values for optional configuration fields
func applyDefaults(cfg *Config) {
	// API defaults
	if cfg.API.Port == 0 {
		cfg.API.Port = 8080
	}
	if cfg.API.ReadTimeout == 0 {
		cfg.API.ReadTimeout = 30
	}
	if cfg.API.WriteTimeout == 0 {
		cfg.API.WriteTimeout = 30
	}
	if cfg.API.IdleTimeout == 0 {
		cfg.API.IdleTimeout = 120
	}

	// Rate limit defaults
	if cfg.API.RateLimit.RequestsPerSecond == 0 {
		cfg.API.RateLimit.RequestsPerSecond = 100
	}
	if cfg.API.RateLimit.Burst == 0 {
		cfg.API.RateLimit.Burst = 200
	}

	// JWT defaults
	if cfg.Auth.JWT.Issuer == "" {
		cfg.Auth.JWT.Issuer = "lemici-platform"
	}
	if cfg.Auth.JWT.ExpiryHours == 0 {
		cfg.Auth.JWT.ExpiryHours = 24
	}
	if cfg.Auth.JWT.RefreshExpiryHours == 0 {
		cfg.Auth.JWT.RefreshExpiryHours = 168
	}
	if cfg.Auth.JWT.Algorithm == "" {
		cfg.Auth.JWT.Algorithm = "HS256"
	}

	// Camunda defaults
	if cfg.Camunda.MaxJobsActive == 0 {
		cfg.Camunda.MaxJobsActive = 10
	}
	if cfg.Camunda.Timeout == 0 {
		cfg.Camunda.Timeout = 30000
	}
	if cfg.Camunda.RequestTimeout == 0 {
		cfg.Camunda.RequestTimeout = 30000
	}
	if cfg.Camunda.KeepAlive == 0 {
		cfg.Camunda.KeepAlive = 45
	}

	// Database defaults
	if cfg.Database.Postgres.Port == 0 {
		cfg.Database.Postgres.Port = 5432
	}
	if cfg.Database.Postgres.MaxConnections == 0 {
		cfg.Database.Postgres.MaxConnections = 25
	}
	if cfg.Database.Postgres.MaxIdle == 0 {
		cfg.Database.Postgres.MaxIdle = 5
	}
	if cfg.Database.Postgres.SSLMode == "" {
		cfg.Database.Postgres.SSLMode = "disable"
	}

	// Redis defaults
	if cfg.Database.Redis.DB == 0 {
		cfg.Database.Redis.DB = 0
	}
	if cfg.Database.Redis.PoolSize == 0 {
		cfg.Database.Redis.PoolSize = 10
	}
	if cfg.Database.Redis.MaxRetries == 0 {
		cfg.Database.Redis.MaxRetries = 3
	}

	// SMTP defaults
	if cfg.Integrations.SMTP.Port == 0 {
		cfg.Integrations.SMTP.Port = 1025
	}
	if cfg.Integrations.SMTP.Host == "" {
		cfg.Integrations.SMTP.Host = "localhost"
	}

	// Set Username from User if empty
	if cfg.Database.Postgres.Username == "" {
		cfg.Database.Postgres.Username = cfg.Database.Postgres.User
	}
	// Set MaxOpenConns from MaxConnections
	if cfg.Database.Postgres.MaxOpenConns == 0 {
		cfg.Database.Postgres.MaxOpenConns = cfg.Database.Postgres.MaxConnections
	}
	// Set MaxIdleConns from MaxIdle
	if cfg.Database.Postgres.MaxIdleConns == 0 {
		cfg.Database.Postgres.MaxIdleConns = cfg.Database.Postgres.MaxIdle
	}

	// Elasticsearch URL fallback
	if cfg.Database.Elasticsearch.URL == "" && len(cfg.Database.Elasticsearch.Addresses) > 0 {
		cfg.Database.Elasticsearch.URL = cfg.Database.Elasticsearch.Addresses[0]
	}

	// Logging defaults
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}
	if cfg.Logging.Output == "" {
		cfg.Logging.Output = "stdout"
	}

	// Worker defaults
	for key, worker := range cfg.Workers {
		if worker.MaxJobsActive == 0 {
			worker.MaxJobsActive = 5
		}
		if worker.Timeout == 0 {
			worker.Timeout = 30000
		}
		if worker.MaxRetries == 0 {
			worker.MaxRetries = 3
		}
		cfg.Workers[key] = worker
	}

	// API timeout defaults
	if cfg.APIs.GenAI.Timeout == 0 {
		cfg.APIs.GenAI.Timeout = 60000
	}
	if cfg.APIs.WebSearch.Timeout == 0 {
		cfg.APIs.WebSearch.Timeout = 10000
	}

	// Monitoring defaults
	if cfg.Monitoring.Metrics.Port == 0 {
		cfg.Monitoring.Metrics.Port = 9090
	}
	if cfg.Monitoring.Metrics.Path == "" {
		cfg.Monitoring.Metrics.Path = "/metrics"
	}
	if cfg.Monitoring.HealthCheck.Path == "" {
		cfg.Monitoring.HealthCheck.Path = "/health"
	}

	// Workflow defaults
	if cfg.Workflows.DefaultTimeout == 0 {
		cfg.Workflows.DefaultTimeout = 300
	}
	if cfg.Workflows.MaxRetries == 0 {
		cfg.Workflows.MaxRetries = 3
	}
	if cfg.Workflows.ContextStorage.TTL == 0 {
		cfg.Workflows.ContextStorage.TTL = 3600
	}
}

// validateConfig validates critical configuration fields
func validateConfig(cfg *Config) error {
	// Camunda validation
	if cfg.Camunda.BrokerAddress == "" && cfg.Camunda.GatewayAddress == "" {
		return fmt.Errorf("camunda.broker_address or camunda.gateway_address is required")
	}

	// Database validation
	if cfg.Database.Postgres.Host == "" {
		return fmt.Errorf("database.postgres.host is required")
	}
	if cfg.Database.Postgres.Database == "" {
		return fmt.Errorf("database.postgres.database is required")
	}
	user := cfg.Database.Postgres.User
	if user == "" {
		user = cfg.Database.Postgres.Username
	}
	if user == "" {
		return fmt.Errorf("database.postgres.user or username is required")
	}

	if len(cfg.Database.Elasticsearch.Addresses) == 0 && cfg.Database.Elasticsearch.URL == "" {
		return fmt.Errorf("database.elasticsearch.addresses or url is required")
	}

	if cfg.Database.Redis.Address == "" {
		return fmt.Errorf("database.redis.address is required")
	}

	// JWT validation (only for API Gateway)
	if cfg.API.Port > 0 {
		if cfg.Auth.JWT.Secret == "" {
			return fmt.Errorf("auth.jwt.secret is required for API Gateway")
		}
		if len(cfg.Auth.JWT.Secret) < 32 {
			return fmt.Errorf("auth.jwt.secret must be at least 32 characters long")
		}
	}

	return nil
}

// GetDuration converts milliseconds from config to time.Duration
func GetDuration(milliseconds int) time.Duration {
	return time.Duration(milliseconds) * time.Millisecond
}

// GetWorkerConfig retrieves worker-specific configuration with fallback to defaults
func GetWorkerConfig(cfg *Config, workerName string) WorkerConfig {
	if worker, exists := cfg.Workers[workerName]; exists {
		return worker
	}

	return WorkerConfig{
		Enabled:       true,
		MaxJobsActive: 5,
		Timeout:       30000,
		MaxRetries:    3,
	}
}

// IsWorkerEnabled checks if a specific worker is enabled
func IsWorkerEnabled(cfg *Config, workerName string) bool {
	if worker, exists := cfg.Workers[workerName]; exists {
		return worker.Enabled
	}
	return true
}
