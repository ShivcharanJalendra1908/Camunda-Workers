// internal/common/config/loader.go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

func Load() (*Config, error) {
	// 🔧 FIX: Load .env from multiple possible locations
	loadEnvFile()

	// Base config
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./configs")
	viper.AddConfigPath("../../configs")
	viper.AddConfigPath(".")

	// Enable ENV override like GOOGLE_CLIENT_ID
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
	_ = viper.MergeInConfig() // ignore error if not found

	// 3️⃣ EXPAND ENV PLACEHOLDERS
	expandEnvVars(viper.GetViper())

	// 4️⃣ Unmarshal final config
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	applyDefaults(&cfg)

	// 5️⃣ DIRECT OVERRIDE IF STILL EMPTY
	overrideEmptyConfig(&cfg)

	if cfg.Database.Elasticsearch.URL == "" && len(cfg.Database.Elasticsearch.Addresses) > 0 {
		cfg.Database.Elasticsearch.URL = cfg.Database.Elasticsearch.Addresses[0]
	}

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// 🔥 FIX: Load .env from multiple possible locations
func loadEnvFile() {
	// Try multiple paths (for running from different directories)
	possiblePaths := []string{
		".env",                    // Current directory
		"../.env",                 // Parent directory
		"../../.env",              // Two levels up (for tests in test/e2e/)
		"../../../.env",           // Three levels up
	}

	// Also try to find project root by looking for go.mod
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

	fmt.Printf("⚠️  .env file not found in any location, using system environment variables\n")
}

// Find project root by looking for go.mod
func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Walk up directories looking for go.mod
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root
			break
		}
		dir = parent
	}

	return ""
}

// Improved environment variable expansion
func expandEnvVars(v *viper.Viper) {
	for _, key := range v.AllKeys() {
		val := v.Get(key)
		
		// Only process string values
		if strVal, ok := val.(string); ok {
			// Check if it contains environment variable pattern
			if strings.Contains(strVal, "${") || (strings.HasPrefix(strVal, "$") && len(strVal) > 1) {
				expanded := os.ExpandEnv(strVal)
				if expanded != strVal && expanded != "" {
					v.Set(key, expanded)
				}
			}
		}
	}
}

// Direct override if config values are still empty after expansion
func overrideEmptyConfig(cfg *Config) {
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

	// GenAI API
	if cfg.APIs.GenAI.APIKey == "" {
		if val := os.Getenv("GENAI_API_KEY"); val != "" {
			cfg.APIs.GenAI.APIKey = val
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

	// Database overrides
	if cfg.Database.Postgres.User == "" {
		if val := os.Getenv("DB_USER"); val != "" {
			cfg.Database.Postgres.User = val
		}
	}
	if cfg.Database.Postgres.Password == "" {
		if val := os.Getenv("DB_PASSWORD"); val != "" {
			cfg.Database.Postgres.Password = val
		}
	}
}

// LoadFromFile loads configuration from a specific file path
func LoadFromFile(path string) (*Config, error) {
	loadEnvFile() // Load env file first

	viper.SetConfigFile(path)
	viper.SetConfigType("yaml")

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	// Expand environment variables before unmarshal
	expandEnvVars(viper.GetViper())

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	applyDefaults(&cfg)
	overrideEmptyConfig(&cfg)

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// applyDefaults sets default values for optional configuration fields
func applyDefaults(cfg *Config) {
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

	// Database defaults
	if cfg.Database.Postgres.MaxConnections == 0 {
		cfg.Database.Postgres.MaxConnections = 25
	}
	if cfg.Database.Postgres.MaxIdle == 0 {
		cfg.Database.Postgres.MaxIdle = 5
	}
	if cfg.Database.Postgres.SSLMode == "" {
		cfg.Database.Postgres.SSLMode = "disable"
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

	// Worker defaults - CRITICAL FIX!
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
}

// validateConfig validates critical configuration fields
func validateConfig(cfg *Config) error {
	if cfg.Camunda.BrokerAddress == "" {
		return fmt.Errorf("camunda.broker_address is required")
	}

	if cfg.Database.Postgres.Host == "" {
		return fmt.Errorf("database.postgres.host is required")
	}
	if cfg.Database.Postgres.Database == "" {
		return fmt.Errorf("database.postgres.database is required")
	}
	if cfg.Database.Postgres.User == "" {
		return fmt.Errorf("database.postgres.user is required")
	}

	if len(cfg.Database.Elasticsearch.Addresses) == 0 && cfg.Database.Elasticsearch.URL == "" {
		return fmt.Errorf("database.elasticsearch.addresses or url is required")
	}

	if cfg.Database.Redis.Address == "" {
		return fmt.Errorf("database.redis.address is required")
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

	// Return default worker config if not found
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
