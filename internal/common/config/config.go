// internal/common/config/config.go
package config

import "fmt"

// Config is the main application configuration struct.
type Config struct {
	App           AppConfig               `mapstructure:"app"`
	Camunda       CamundaConfig           `mapstructure:"camunda"`
	Database      DatabaseConfig          `mapstructure:"database"`
	Template      TemplateConfig          `mapstructure:"template"`
	Workers       map[string]WorkerConfig `mapstructure:"workers"`
	Auth          AuthConfig              `mapstructure:"auth"`
	Integrations  IntegrationConfig       `mapstructure:"integrations"`
	APIs          APIsConfig              `mapstructure:"apis"`
	Logging       LoggingConfig           `mapstructure:"logging"`
	Notifications NotificationConfig      `mapstructure:"notifications"`
}

// --- Core App/Infrastructure Config ---
type AppConfig struct {
	Name        string `mapstructure:"name"`
	Version     string `mapstructure:"version"`
	Environment string `mapstructure:"environment"`
}

type CamundaConfig struct {
	BrokerAddress  string `mapstructure:"broker_address"`
	MaxJobsActive  int    `mapstructure:"max_jobs_active"`
	Timeout        int    `mapstructure:"timeout"`         // milliseconds
	RequestTimeout int    `mapstructure:"request_timeout"` // milliseconds
}

type DatabaseConfig struct {
	Postgres      PostgresConfig      `mapstructure:"postgres"`
	Elasticsearch ElasticsearchConfig `mapstructure:"elasticsearch"`
	Redis         RedisConfig         `mapstructure:"redis"`
}

type PostgresConfig struct {
	Host           string `mapstructure:"host"`
	Port           int    `mapstructure:"port"`
	Database       string `mapstructure:"database"`
	User           string `mapstructure:"user"`
	Password       string `mapstructure:"password"`
	MaxConnections int    `mapstructure:"max_connections"`
	MaxIdle        int    `mapstructure:"max_idle"`
	SSLMode        string `mapstructure:"sslmode"`
}

// GetDSN returns the PostgreSQL connection string
func (p PostgresConfig) GetDSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		p.Host, p.Port, p.User, p.Password, p.Database, p.SSLMode,
	)
}

type ElasticsearchConfig struct {
	Addresses  []string `mapstructure:"addresses"`
	Username   string   `mapstructure:"username"`
	Password   string   `mapstructure:"password"`
	SSLEnabled bool     `mapstructure:"ssl_enabled"`
	URL        string   `mapstructure:"url"` // Single URL for backwards compatibility
}

// GetURL returns the first address or the URL field
func (e ElasticsearchConfig) GetURL() string {
	if e.URL != "" {
		return e.URL
	}
	if len(e.Addresses) > 0 {
		return e.Addresses[0]
	}
	return ""
}

type RedisConfig struct {
	Address  string `mapstructure:"address"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// WorkerConfig holds the core settings applicable to every worker.
type WorkerConfig struct {
	Enabled       bool `mapstructure:"enabled"`
	MaxJobsActive int  `mapstructure:"max_jobs_active"`
	Timeout       int  `mapstructure:"timeout"`     // milliseconds
	MaxRetries    int  `mapstructure:"max_retries"` // For error handling
}

// --- Specific Configuration Sections ---

// AuthConfig holds settings for all authentication workers.
type AuthConfig struct {
	Keycloak struct {
		URL          string `mapstructure:"url"`
		Realm        string `mapstructure:"realm"`
		ClientID     string `mapstructure:"client_id"`
		ClientSecret string `mapstructure:"client_secret"`
	} `mapstructure:"keycloak"`

	OAuthProviders struct {
		Google struct {
			ClientID     string `mapstructure:"client_id"`
			ClientSecret string `mapstructure:"client_secret"`
			RedirectURL  string `mapstructure:"redirect_uri"`
		} `mapstructure:"google"`
		LinkedIn struct {
			ClientID     string `mapstructure:"client_id"`
			ClientSecret string `mapstructure:"client_secret"`
			RedirectURL  string `mapstructure:"redirect_uri"`
		} `mapstructure:"linkedin"`
	} `mapstructure:"oauth_providers"`
}

// --- Security Configuration ---
type SecurityConfig struct {
	Recaptcha RecaptchaConfig `mapstructure:"recaptcha"`
}

type RecaptchaConfig struct {
	SecretKey string `mapstructure:"secret_key"`
	VerifyURL string `mapstructure:"verify_url"`
}

// IntegrationConfig holds settings for CRM, Email, and other external services.
type IntegrationConfig struct {
	Zoho struct {
		APIKey    string `mapstructure:"api_key"`
		AuthToken string `mapstructure:"oauth_token"`
	} `mapstructure:"zoho"`

	AWS struct {
		Region string `mapstructure:"region"`
		SES    struct {
			Enabled   bool   `mapstructure:"enabled"`
			FromEmail string `mapstructure:"from_email"`
		} `mapstructure:"ses"`
		SNS struct {
			Enabled            bool   `mapstructure:"enabled"`
			DefaultSMSSenderID string `mapstructure:"default_sms_sender_id"`
		} `mapstructure:"sns"`
	} `mapstructure:"aws"`

	// Add SMTP configuration
	SMTP struct {
		Host        string `mapstructure:"host"`
		Port        int    `mapstructure:"port"`
		Username    string `mapstructure:"username"`
		Password    string `mapstructure:"password"`
		UseTLS      bool   `mapstructure:"use_tls"`
		DefaultFrom string `mapstructure:"default_from"`
	} `mapstructure:"smtp"`
}

// APIsConfig holds settings for external API integrations.
type APIsConfig struct {
	GenAI struct {
		BaseURL string `mapstructure:"base_url"`
		APIKey  string `mapstructure:"api_key"`
		Timeout int    `mapstructure:"timeout"` // milliseconds
	} `mapstructure:"genai"`

	WebSearch struct {
		BaseURL  string `mapstructure:"base_url"`
		APIKey   string `mapstructure:"api_key"`
		EngineID string `mapstructure:"engine_id"`
		Timeout  int    `mapstructure:"timeout"` // milliseconds
	} `mapstructure:"web_search"`
}

// NotificationConfig holds settings for the send-notification worker.
type NotificationConfig struct {
	Email struct {
		Enabled   bool   `mapstructure:"enabled"`
		FromEmail string `mapstructure:"from_email"`
	} `mapstructure:"email"`
	SMS struct {
		Enabled           bool   `mapstructure:"enabled"`
		PriorityThreshold string `mapstructure:"priority_threshold"`
	} `mapstructure:"sms"`
	AWS struct {
		Region string `mapstructure:"region"`
	} `mapstructure:"aws"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
}

// TemplateConfig holds settings for the build-response and select-template workers.
type TemplateConfig struct {
	TemplateRules TemplateRules `mapstructure:"template_rules"`
	RegistryPath  string        `mapstructure:"registry_path"`
}

// TemplateRules holds template routing rules
type TemplateRules struct {
	Route map[string]string `mapstructure:"route"`
	Flow  map[string]string `mapstructure:"flow"`
}
