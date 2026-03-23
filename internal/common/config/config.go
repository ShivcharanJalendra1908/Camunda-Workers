// internal/common/config/config.go
package config

import (
	"fmt"
	"time"
)

// ============================================================================
// MAIN CONFIG STRUCTURE
// ============================================================================

// Config is the main application configuration struct.
type Config struct {
	App             AppConfig               `mapstructure:"app"`
	API             APIConfig               `mapstructure:"api"`
	Auth            AuthConfig              `mapstructure:"auth"`
	Camunda         CamundaConfig           `mapstructure:"camunda"`
	Database        DatabaseConfig          `mapstructure:"database"`
	Template        TemplateConfig          `mapstructure:"template"`
	Workers         map[string]WorkerConfig `mapstructure:"workers"`
	Integrations    IntegrationConfig       `mapstructure:"integrations"`
	APIs            APIsConfig              `mapstructure:"apis"`
	Logging         LoggingConfig           `mapstructure:"logging"`
	Notifications   NotificationConfig      `mapstructure:"notifications"`
	Monitoring      MonitoringConfig        `mapstructure:"monitoring"`
	Security        SecurityConfig          `mapstructure:"security"`
	Workflows       WorkflowConfig          `mapstructure:"workflows"`
	FranchiseSearch FranchiseSearchConfig   `yaml:"franchise_search"`
	Idempotency     IdempotencyConfig       `yaml:"idempotency"`
	Pagination      PaginationConfig        `mapstructure:"pagination"`
}

// ============================================================================
// PAGINATION CONFIG
// ============================================================================
type PaginationConfig struct {
	DefaultPageSize int `mapstructure:"default_page_size"`
	MaxPageSize     int `mapstructure:"max_page_size"`
}

// ============================================================================
// FRANCHISE SEARCH CONFIG
// ============================================================================

type FranchiseSearchConfig struct {
	DefaultLimit       int     `yaml:"default_limit"`
	MaxLimit           int     `yaml:"max_limit"`
	Fuzziness          string  `yaml:"fuzziness"`
	MinScore           float64 `yaml:"min_score"`
	EnableSuggestions  bool    `yaml:"enable_suggestions"`
	EnableAggregations bool    `yaml:"enable_aggregations"`
	EnableSpellCheck   bool    `yaml:"enable_spell_check"`
	DefaultSort        string  `yaml:"default_sort"`
	CacheTTL           int     `yaml:"cache_ttl"`
}

// ============================================================================
// CORE APP/INFRASTRUCTURE CONFIG
// ============================================================================

type AppConfig struct {
	Name        string `mapstructure:"name"`
	Version     string `mapstructure:"version"`
	Environment string `mapstructure:"environment"`
	Debug       bool   `mapstructure:"debug"`
	Port        int    `mapstructure:"port"`
	BaseURL     string `mapstructure:"baseUrl"`
}

type APIConfig struct {
	Port         int             `mapstructure:"port"`
	ReadTimeout  int             `mapstructure:"readTimeout"`
	WriteTimeout int             `mapstructure:"writeTimeout"`
	IdleTimeout  int             `mapstructure:"idleTimeout"`
	RateLimit    RateLimitConfig `mapstructure:"rateLimit"`
	CORS         CORSConfig      `mapstructure:"cors"`
	HTTPS        HTTPSConfig     `mapstructure:"https"`
}

type CORSConfig struct {
	AllowOrigins     []string `mapstructure:"allowOrigins"`
	AllowMethods     []string `mapstructure:"allowMethods"`
	AllowHeaders     []string `mapstructure:"allowHeaders"`
	ExposeHeaders    []string `mapstructure:"exposeHeaders"`
	AllowCredentials bool     `mapstructure:"allowCredentials"`
	MaxAge           int      `mapstructure:"maxAge"`
}

type HTTPSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	CertFile string `mapstructure:"certFile"`
	KeyFile  string `mapstructure:"keyFile"`
}

type RateLimitConfig struct {
	Enabled           bool     `mapstructure:"enabled"`
	RequestsPerSecond int      `mapstructure:"requestsPerSecond"`
	Burst             int      `mapstructure:"burst"`
	ExcludedIPs       []string `mapstructure:"excludedIPs"`
	ExcludedPaths     []string `mapstructure:"excludedPaths"`
}

type CamundaConfig struct {
	BrokerAddress      string `mapstructure:"broker_address"`
	GatewayAddress     string `mapstructure:"gateway_address"`
	MaxJobsActive      int    `mapstructure:"max_jobs_active"`
	Timeout            int    `mapstructure:"timeout"`
	RequestTimeout     int    `mapstructure:"request_timeout"`
	UsePlainTextAuth   bool   `mapstructure:"use_plaintext_auth"`
	KeepAlive          int    `mapstructure:"keep_alive"`
	InsecureSkipVerify bool   `mapstructure:"insecure_skip_verify"`
	CACertPath         string `mapstructure:"ca_cert_path"`
}

// ============================================================================
// DATABASE CONFIGURATION
// ============================================================================

type DatabaseConfig struct {
	Postgres      PostgresConfig      `mapstructure:"postgres"`
	Elasticsearch ElasticsearchConfig `mapstructure:"elasticsearch"`
	Redis         RedisConfig         `mapstructure:"redis"`
}

type PostgresConfig struct {
	Host             string `mapstructure:"host"`
	Port             int    `mapstructure:"port"`
	Database         string `mapstructure:"database"`
	User             string `mapstructure:"user"`
	Username         string `mapstructure:"username"`
	Password         string `mapstructure:"password"`
	MaxConnections   int    `mapstructure:"max_connections"`
	MaxOpenConns     int    `mapstructure:"maxOpenConns"`
	MaxIdle          int    `mapstructure:"max_idle"`
	MaxIdleConns     int    `mapstructure:"maxIdleConns"`
	ConnMaxLifetime  int    `mapstructure:"connMaxLifetime"`
	SSLMode          string `mapstructure:"sslmode"`
	ConnectionString string `mapstructure:"connectionString"`
}

func (p PostgresConfig) GetDSN() string {
	if p.ConnectionString != "" {
		return p.ConnectionString
	}
	user := p.User
	if user == "" {
		user = p.Username
	}
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		p.Host, p.Port, user, p.Password, p.Database, p.SSLMode,
	)
}

type ElasticsearchConfig struct {
	Addresses      []string             `mapstructure:"addresses"`
	Username       string               `mapstructure:"username"`
	Password       string               `mapstructure:"password"`
	SSLEnabled     bool                 `mapstructure:"ssl_enabled"`
	URL            string               `mapstructure:"url"`
	MaxRetries     int                  `mapstructure:"maxRetries"`
	EnableMetrics  bool                 `mapstructure:"enableMetrics"`
	IndexPrefix    string               `mapstructure:"indexPrefix"`
	SniffEnabled   bool                 `mapstructure:"sniffEnabled"`
	Healthcheck    bool                 `mapstructure:"healthcheck"`
	RetryOnStatus  []int                `mapstructure:"retryOnStatus"`
	RequestTimeout int                  `mapstructure:"requestTimeout"`
	Indices        ElasticsearchIndices `yaml:"indices"` // NEW
	Timeout        string               `yaml:"timeout"`
	Sniff          bool                 `yaml:"sniff"`
}

// NEW: Elasticsearch Indices
type ElasticsearchIndices struct {
	Food      string `yaml:"food"`
	Education string `yaml:"education"`
	Fashion   string `yaml:"fashion"`
	All       string `yaml:"all"`
}

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
	Address      string `mapstructure:"address"`
	Password     string `mapstructure:"password"`
	DB           int    `mapstructure:"db"`
	PoolSize     int    `mapstructure:"poolSize"`
	MaxRetries   int    `mapstructure:"maxRetries"`
	DialTimeout  int    `mapstructure:"dialTimeout"`
	ReadTimeout  int    `mapstructure:"readTimeout"`
	WriteTimeout int    `mapstructure:"writeTimeout"`
	MinIdleConns int    `mapstructure:"minIdleConns"`
	MaxIdleConns int    `mapstructure:"maxIdleConns"`
}

// ============================================================================
// AUTHENTICATION CONFIGURATION
// ============================================================================

type AuthConfig struct {
	JWT            JWTConfig            `mapstructure:"jwt"`
	Session        SessionConfig        `mapstructure:"session"`
	PasswordPolicy PasswordPolicyConfig `mapstructure:"passwordPolicy"`
	OIDC           OIDCConfig           `mapstructure:"oidc"`

	Keycloak struct {
		URL               string `mapstructure:"url"`
		Realm             string `mapstructure:"realm"`
		ClientID          string `mapstructure:"client_id"`
		ClientSecret      string `mapstructure:"client_secret"`
		AdminClientID     string `mapstructure:"admin_client_id"`     // ← ADD
		AdminClientSecret string `mapstructure:"admin_client_secret"` // ← ADD
		Issuer            string `mapstructure:"issuer"`              // ✅ ADD
		RedirectURL       string `mapstructure:"redirectUrl"`         // ✅ ADD
		PublicBaseURL     string `mapstructure:"publicBaseUrl"`       // ✅ ADD
		PostLogoutRedirectURI string `mapstructure:"post_logout_redirect_uri"`
	} `mapstructure:"keycloak"`

	OAuthProviders struct {
		Google struct {
			ClientID     string `mapstructure:"client_id"`
			ClientSecret string `mapstructure:"client_secret"`
			RedirectURL  string `mapstructure:"redirect_uri"`
			Scopes       string `mapstructure:"scopes"`
		} `mapstructure:"google"`
		LinkedIn struct {
			ClientID     string `mapstructure:"client_id"`
			ClientSecret string `mapstructure:"client_secret"`
			RedirectURL  string `mapstructure:"redirect_uri"`
			Scopes       string `mapstructure:"scopes"`
		} `mapstructure:"linkedin"`
		Microsoft struct {
			ClientID     string `mapstructure:"client_id"`
			ClientSecret string `mapstructure:"client_secret"`
			RedirectURL  string `mapstructure:"redirect_uri"`
			Scopes       string `mapstructure:"scopes"`
		} `mapstructure:"microsoft"`
	} `mapstructure:"oauth_providers"`
}

type JWTConfig struct {
	Secret             string `mapstructure:"secret"`
	Issuer             string `mapstructure:"issuer"`
	Audience           string `mapstructure:"audience"`
	ExpiryHours        int    `mapstructure:"expiryHours"`
	RefreshExpiryHours int    `mapstructure:"refreshExpiryHours"`
	Algorithm          string `mapstructure:"algorithm"`
	PublicKeyPath      string `mapstructure:"publicKeyPath"`
	PrivateKeyPath     string `mapstructure:"privateKeyPath"`
	AccessTokenTTL     int    `mapstructure:"accessTokenTTL"`
	RefreshTokenTTL    int    `mapstructure:"refreshTokenTTL"`
}

type SessionConfig struct {
	CookieName      string `mapstructure:"cookieName"`
	CookieDomain    string `mapstructure:"cookieDomain"`
	CookieSecure    bool   `mapstructure:"cookieSecure"`
	CookieHTTPOnly  bool   `mapstructure:"cookieHttpOnly"`
	CookieSameSite  string `mapstructure:"cookieSameSite"`
	SessionTTL      int    `mapstructure:"sessionTTL"`
	RefreshTokenTTL int    `mapstructure:"refreshTokenTTL"`
	MaxSessions     int    `mapstructure:"maxSessions"`
}

type PasswordPolicyConfig struct {
	MinLength           int  `mapstructure:"minLength"`
	RequireUppercase    bool `mapstructure:"requireUppercase"`
	RequireLowercase    bool `mapstructure:"requireLowercase"`
	RequireNumbers      bool `mapstructure:"requireNumbers"`
	RequireSpecialChars bool `mapstructure:"requireSpecialChars"`
	MaxAgeDays          int  `mapstructure:"maxAgeDays"`
	HistorySize         int  `mapstructure:"historySize"`
}

type OIDCConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	Issuer       string `mapstructure:"issuer"`
	ClientID     string `mapstructure:"clientId"`
	ClientSecret string `mapstructure:"clientSecret"`
	RedirectURL  string `mapstructure:"redirectUrl"`
	Scopes       string `mapstructure:"scopes"`
}

// ============================================================================
// SECURITY CONFIGURATION
// ============================================================================

type SecurityConfig struct {
	Recaptcha         RecaptchaConfig         `mapstructure:"recaptcha"`
	TLS               TLSConfig               `mapstructure:"tls"`
	RequestValidation RequestValidationConfig `mapstructure:"requestValidation"`
	IPWhitelist       IPWhitelistConfig       `mapstructure:"ipWhitelist"`
	RateLimiting      RateLimitingConfig      `mapstructure:"rateLimiting"`
	Encryption        EncryptionConfig        `mapstructure:"encryption"`
}

type RecaptchaConfig struct {
	Enabled    bool     `mapstructure:"enabled"`
	SecretKey  string   `mapstructure:"secret_key"`
	VerifyURL  string   `mapstructure:"verify_url"`
	SiteKey    string   `mapstructure:"siteKey"`
	MinScore   float64  `mapstructure:"minScore"`
	SkipRoutes []string `mapstructure:"skipRoutes"`
}

type TLSConfig struct {
	Enabled      bool     `mapstructure:"enabled"`
	CertFile     string   `mapstructure:"certFile"`
	KeyFile      string   `mapstructure:"keyFile"`
	ClientAuth   string   `mapstructure:"clientAuth"`
	MinVersion   string   `mapstructure:"minVersion"`
	MaxVersion   string   `mapstructure:"maxVersion"`
	CipherSuites []string `mapstructure:"cipherSuites"`
}

type RequestValidationConfig struct {
	MaxBodySize    int      `mapstructure:"maxBodySize"`
	MaxHeaderSize  int      `mapstructure:"maxHeaderSize"`
	AllowedMethods []string `mapstructure:"allowedMethods"`
	AllowedHeaders []string `mapstructure:"allowedHeaders"`
}

type IPWhitelistConfig struct {
	Enabled    bool     `mapstructure:"enabled"`
	AllowedIPs []string `mapstructure:"allowedIPs"`
	BlockedIPs []string `mapstructure:"blockedIPs"`
}

type RateLimitingConfig struct {
	Enabled           bool              `mapstructure:"enabled"`
	Default           RateLimitSettings `mapstructure:"default"`
	ByIP              RateLimitSettings `mapstructure:"byIp"`
	ByUser            RateLimitSettings `mapstructure:"byUser"`
	WhitelistedRoutes []string          `mapstructure:"whitelistedRoutes"`
}

type RateLimitSettings struct {
	RequestsPerMinute int `mapstructure:"requestsPerMinute"`
	Burst             int `mapstructure:"burst"`
	WindowMinutes     int `mapstructure:"windowMinutes"`
}

type EncryptionConfig struct {
	Key          string `mapstructure:"key"`
	Algorithm    string `mapstructure:"algorithm"`
	IV           string `mapstructure:"iv"`
	KeyRotation  bool   `mapstructure:"keyRotation"`
	RotationDays int    `mapstructure:"rotationDays"`
}

// ============================================================================
// INTEGRATIONS CONFIGURATION
// ============================================================================

type IntegrationConfig struct {
	Zoho struct {
		APIKey       string `mapstructure:"api_key"`
		AuthToken    string `mapstructure:"oauth_token"`
		BaseURL      string `mapstructure:"baseUrl"`
		Timeout      int    `mapstructure:"timeout"`
		RetryCount   int    `mapstructure:"retryCount"`
		RetryDelay   int    `mapstructure:"retryDelay"`
		ClientID     string `mapstructure:"clientId"`
		ClientSecret string `mapstructure:"clientSecret"`
		RefreshToken string `mapstructure:"refreshToken"`
		AccountURL   string `mapstructure:"accountUrl"`
		TokenURL     string `mapstructure:"tokenUrl"`
		Scopes       string `mapstructure:"scopes"`
	} `mapstructure:"zoho"`

	AWS struct {
		Region          string `mapstructure:"region"`
		AccessKeyID     string `mapstructure:"accessKeyId"`
		SecretAccessKey string `mapstructure:"secretAccessKey"`
		Endpoint        string `mapstructure:"endpoint"`
		SES             struct {
			Enabled   bool   `mapstructure:"enabled"`
			FromEmail string `mapstructure:"from_email"`
			Charset   string `mapstructure:"charset"`
			ConfigSet string `mapstructure:"configSet"`
		} `mapstructure:"ses"`
		SNS struct {
			Enabled            bool   `mapstructure:"enabled"`
			DefaultSMSSenderID string `mapstructure:"default_sms_sender_id"`
			SMSType            string `mapstructure:"smsType"`
		} `mapstructure:"sns"`
		S3 struct {
			Enabled    bool   `mapstructure:"enabled"`
			Bucket     string `mapstructure:"bucket"`
			Region     string `mapstructure:"region"`
			PresignTTL int    `mapstructure:"presignTtl"`
		} `mapstructure:"s3"`
	} `mapstructure:"aws"`

	SMTP struct {
		Host           string `mapstructure:"host"`
		Port           int    `mapstructure:"port"`
		Username       string `mapstructure:"username"`
		Password       string `mapstructure:"password"`
		UseTLS         bool   `mapstructure:"use_tls"`
		UseSSL         bool   `mapstructure:"use_ssl"`
		DefaultFrom    string `mapstructure:"default_from"`
		FromName       string `mapstructure:"fromName"`
		AuthType       string `mapstructure:"authType"`
		Timeout        int    `mapstructure:"timeout"`
		MaxConnections int    `mapstructure:"maxConnections"`
	} `mapstructure:"smtp"`

	SendGrid struct {
		Enabled   bool   `mapstructure:"enabled"`
		APIKey    string `mapstructure:"apiKey"`
		FromEmail string `mapstructure:"fromEmail"`
		FromName  string `mapstructure:"fromName"`
	} `mapstructure:"sendgrid"`

	Twilio struct {
		Enabled      bool   `mapstructure:"enabled"`
		AccountSID   string `mapstructure:"accountSid"`
		AuthToken    string `mapstructure:"authToken"`
		FromNumber   string `mapstructure:"fromNumber"`
		MessagingSID string `mapstructure:"messagingSid"`
	} `mapstructure:"twilio"`

	Internal struct {
		EnquiryAlertEmail string `yaml:"enquiry_alert_email"`
		EnquiryAlertName  string `yaml:"enquiry_alert_name"`
	} `yaml:"internal"`
}

// ============================================================================
// API CONFIGURATION
// ============================================================================

type APIsConfig struct {
	GenAI struct {
		BaseURL      string  `mapstructure:"base_url"`
		APIKey       string  `mapstructure:"api_key"`
		Timeout      int     `mapstructure:"timeout"`
		Model        string  `mapstructure:"model"`
		MaxTokens    int     `mapstructure:"maxTokens"`
		Temperature  float64 `mapstructure:"temperature"`
		Organization string  `mapstructure:"organization"`
		Project      string  `mapstructure:"project"`
	} `mapstructure:"genai"`

	WebSearch struct {
		BaseURL    string `mapstructure:"base_url"`
		APIKey     string `mapstructure:"api_key"`
		EngineID   string `mapstructure:"engine_id"`
		Timeout    int    `mapstructure:"timeout"`
		SafeSearch string `mapstructure:"safeSearch"`
		Country    string `mapstructure:"country"`
		Language   string `mapstructure:"language"`
	} `mapstructure:"web_search"`

	Payment struct {
		Stripe struct {
			SecretKey      string `mapstructure:"secretKey"`
			PublishableKey string `mapstructure:"publishableKey"`
			WebhookSecret  string `mapstructure:"webhookSecret"`
		} `mapstructure:"stripe"`
		PayPal struct {
			ClientID     string `mapstructure:"clientId"`
			ClientSecret string `mapstructure:"clientSecret"`
			Environment  string `mapstructure:"environment"`
		} `mapstructure:"paypal"`
	} `mapstructure:"payment"`
}

// ============================================================================
// NOTIFICATION CONFIGURATION
// ============================================================================

type NotificationConfig struct {
	Email struct {
		Enabled     bool     `mapstructure:"enabled"`
		FromEmail   string   `mapstructure:"from_email"`
		FromName    string   `mapstructure:"fromName"`
		ReplyTo     string   `mapstructure:"replyTo"`
		Bcc         []string `mapstructure:"bcc"`
		TemplateDir string   `mapstructure:"templateDir"`
	} `mapstructure:"email"`
	SMS struct {
		Enabled           bool   `mapstructure:"enabled"`
		PriorityThreshold string `mapstructure:"priority_threshold"`
		Provider          string `mapstructure:"provider"`
		MaxLength         int    `mapstructure:"maxLength"`
	} `mapstructure:"sms"`
	Push struct {
		Enabled  bool   `mapstructure:"enabled"`
		Provider string `mapstructure:"provider"`
		APIKey   string `mapstructure:"apiKey"`
	} `mapstructure:"push"`
	AWS struct {
		Region string `mapstructure:"region"`
	} `mapstructure:"aws"`
}

// ============================================================================
// LOGGING CONFIGURATION
// ============================================================================

type LoggingConfig struct {
	Level      string        `mapstructure:"level"`
	Format     string        `mapstructure:"format"`
	Output     string        `mapstructure:"output"`
	File       LogFileConfig `mapstructure:"file"`
	JSON       bool          `mapstructure:"json"`
	StackTrace bool          `mapstructure:"stackTrace"`
	Caller     bool          `mapstructure:"caller"`
}

type LogFileConfig struct {
	Path       string `mapstructure:"path"`
	MaxSize    int    `mapstructure:"maxSize"`
	MaxBackups int    `mapstructure:"maxBackups"`
	MaxAge     int    `mapstructure:"maxAge"`
	Compress   bool   `mapstructure:"compress"`
}

// ============================================================================
// MONITORING CONFIGURATION
// ============================================================================

type MonitoringConfig struct {
	Metrics     MetricsConfig     `mapstructure:"metrics"`
	Tracing     TracingConfig     `mapstructure:"tracing"`
	HealthCheck HealthCheckConfig `mapstructure:"healthCheck"`
	Profiling   ProfilingConfig   `mapstructure:"profiling"`
}

type MetricsConfig struct {
	Enabled      bool      `mapstructure:"enabled"`
	Path         string    `mapstructure:"path"`
	Port         int       `mapstructure:"port"`
	Namespace    string    `mapstructure:"namespace"`
	Buckets      []float64 `mapstructure:"buckets"`
	Collectors   []string  `mapstructure:"collectors"`
	PushGateway  string    `mapstructure:"pushGateway"`
	PushInterval int       `mapstructure:"pushInterval"`
}

type TracingConfig struct {
	Enabled       bool    `mapstructure:"enabled"`
	ServiceName   string  `mapstructure:"serviceName"`
	Endpoint      string  `mapstructure:"endpoint"`
	Sampler       string  `mapstructure:"sampler"`
	Probability   float64 `mapstructure:"probability"`
	Environment   string  `mapstructure:"environment"`
	Version       string  `mapstructure:"version"`
	ExportTimeout int     `mapstructure:"exportTimeout"` // in seconds
}

type HealthCheckConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Path     string `mapstructure:"path"`
	Interval int    `mapstructure:"interval"`
	Timeout  int    `mapstructure:"timeout"`
}

type ProfilingConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Endpoint string `mapstructure:"endpoint"`
}

// ============================================================================
// WORKFLOW CONFIGURATION
// ============================================================================

type WorkflowConfig struct {
	DefaultTimeout int                  `mapstructure:"defaultTimeout"`
	MaxRetries     int                  `mapstructure:"maxRetries"`
	RetryBackoff   int                  `mapstructure:"retryBackoff"`
	ContextStorage ContextStorageConfig `mapstructure:"contextStorage"`
	MaxConcurrent  int                  `mapstructure:"maxConcurrent"`
	WorkerPoolSize int                  `mapstructure:"workerPoolSize"`
	PollInterval   int                  `mapstructure:"pollInterval"`
	LockDuration   int                  `mapstructure:"lockDuration"`
}

type ContextStorageConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Backend string `mapstructure:"backend"`
	TTL     int    `mapstructure:"ttl"`
}

// ============================================================================
// TEMPLATE CONFIGURATION
// ============================================================================

type TemplateConfig struct {
	TemplateRules TemplateRules `mapstructure:"template_rules"`
	RegistryPath  string        `mapstructure:"registry_path"`
	CacheEnabled  bool          `mapstructure:"cacheEnabled"`
	CacheTTL      int           `mapstructure:"cacheTtl"`
}

type TemplateRules struct {
	Route    map[string]string `mapstructure:"route"`
	Flow     map[string]string `mapstructure:"flow"`
	Fallback string            `mapstructure:"fallback"`
}

// ============================================================================
// WORKER CONFIGURATION
// ============================================================================

type WorkerConfig struct {
	Enabled        bool     `mapstructure:"enabled"`
	MaxJobsActive  int      `mapstructure:"max_jobs_active"`
	Timeout        int      `mapstructure:"timeout"`
	MaxRetries     int      `mapstructure:"max_retries"`
	Concurrency    int      `mapstructure:"concurrency"`
	PollInterval   int      `mapstructure:"pollInterval"`
	FetchVariables []string `mapstructure:"fetchVariables"`
	WorkerType     string   `mapstructure:"workerType"`
	Priority       int      `mapstructure:"priority"`
}

// ============================================================================
// IDEMPOTENCY CONFIGURATION
// ============================================================================
type IdempotencyConfig struct {
	Enabled         bool                     `yaml:"enabled"`
	DefaultTTL      time.Duration            `yaml:"default_ttl"`
	CleanupInterval time.Duration            `yaml:"cleanup_interval"`
	TTLs            map[string]time.Duration `yaml:"ttls"`
}
