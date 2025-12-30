package template_driven

import (
	"fmt"
	"os"
	"strconv"
)

// GAP FIX #5: Comprehensive configuration with resource limits
type Config struct {
	// Worker settings
	WorkerID      string
	TaskType      string
	MaxJobsActive int
	Timeout       int // seconds

	// Template settings
	TemplatesBaseDir string
	EnableCache      bool
	MaxCacheSize     int

	// GAP FIX #1: Path security
	MaxPathLength int // Maximum allowed path length

	// GAP FIX #5: File size limits
	MaxTemplateSize int // bytes (default: 10MB)
	MaxInputSize    int // bytes (default: 10MB)

	// GAP FIX #5: Structural complexity limits
	MaxNestingDepth     int // Maximum nesting depth
	MaxMappings         int // Maximum number of mappings
	MaxProcessingSteps  int // Maximum preprocessing/postprocessing steps
	MaxArraySize        int // Maximum array size

	// GAP FIX #3: Expression security
	MaxExpressionLength    int // Maximum expression length
	MaxExpressionOperators int // Maximum operators in expression

	// GAP FIX #4: Regex security
	MaxRegexPatternLength int // Maximum regex pattern length
	MaxRegexInputSize     int // Maximum input size for regex

	// Logging
	LogLevel string // debug, info, warn, error

	// Performance
	MaxConcurrency int
	EnableMetrics  bool

	// Validation
	StrictValidation   bool
	AllowUnknownFields bool

	// GAP FIX #18: Version support
	SupportedVersions []string
}

// NewConfig creates a new configuration with secure defaults
func NewConfig() *Config {
	return &Config{
		// Worker settings
		WorkerID:      getEnv("WORKER_ID", "template-driven-worker"),
		TaskType:      getEnv("TASK_TYPE", "template-processor"),
		MaxJobsActive: getEnvAsInt("MAX_JOBS_ACTIVE", 5),
		Timeout:       getEnvAsInt("TIMEOUT", 30), // 30 seconds default

		// Template settings
		TemplatesBaseDir: getEnv("TEMPLATES_BASE_DIR", "./templates"),
		EnableCache:      getEnvAsBool("ENABLE_CACHE", true),
		MaxCacheSize:     getEnvAsInt("MAX_CACHE_SIZE", 100),

		// GAP FIX #1: Path security
		MaxPathLength: getEnvAsInt("MAX_PATH_LENGTH", 500),

		// GAP FIX #5: File size limits (10MB default)
		MaxTemplateSize: getEnvAsInt("MAX_TEMPLATE_SIZE", 10*1024*1024),
		MaxInputSize:    getEnvAsInt("MAX_INPUT_SIZE", 10*1024*1024),

		// GAP FIX #5: Structural limits
		MaxNestingDepth:    getEnvAsInt("MAX_NESTING_DEPTH", 20),
		MaxMappings:        getEnvAsInt("MAX_MAPPINGS", 1000),
		MaxProcessingSteps: getEnvAsInt("MAX_PROCESSING_STEPS", 100),
		MaxArraySize:       getEnvAsInt("MAX_ARRAY_SIZE", 10000),

		// GAP FIX #3: Expression limits
		MaxExpressionLength:    getEnvAsInt("MAX_EXPRESSION_LENGTH", 1000),
		MaxExpressionOperators: getEnvAsInt("MAX_EXPRESSION_OPERATORS", 50),

		// GAP FIX #4: Regex limits
		MaxRegexPatternLength: getEnvAsInt("MAX_REGEX_PATTERN_LENGTH", 500),
		MaxRegexInputSize:     getEnvAsInt("MAX_REGEX_INPUT_SIZE", 10000),

		// Logging
		LogLevel: getEnv("LOG_LEVEL", "info"),

		// Performance
		MaxConcurrency: getEnvAsInt("MAX_CONCURRENCY", 10),
		EnableMetrics:  getEnvAsBool("ENABLE_METRICS", true),

		// Validation
		StrictValidation:   getEnvAsBool("STRICT_VALIDATION", false),
		AllowUnknownFields: getEnvAsBool("ALLOW_UNKNOWN_FIELDS", true),

		// GAP FIX #18: Version support
		SupportedVersions: []string{"1", "2"},
	}
}

// NewConfigWithOptions creates config with custom options
func NewConfigWithOptions(opts ...ConfigOption) *Config {
	cfg := NewConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// ConfigOption is a function that modifies config
type ConfigOption func(*Config)

// Builder pattern options
func WithWorkerID(id string) ConfigOption {
	return func(c *Config) { c.WorkerID = id }
}

func WithTaskType(taskType string) ConfigOption {
	return func(c *Config) { c.TaskType = taskType }
}

func WithMaxJobsActive(max int) ConfigOption {
	return func(c *Config) { c.MaxJobsActive = max }
}

func WithTimeout(timeout int) ConfigOption {
	return func(c *Config) { c.Timeout = timeout }
}

func WithTemplatesBaseDir(dir string) ConfigOption {
	return func(c *Config) { c.TemplatesBaseDir = dir }
}

func WithCache(enable bool) ConfigOption {
	return func(c *Config) { c.EnableCache = enable }
}

func WithMaxCacheSize(size int) ConfigOption {
	return func(c *Config) { c.MaxCacheSize = size }
}

func WithLogLevel(level string) ConfigOption {
	return func(c *Config) { c.LogLevel = level }
}

func WithMaxConcurrency(max int) ConfigOption {
	return func(c *Config) { c.MaxConcurrency = max }
}

func WithMetrics(enable bool) ConfigOption {
	return func(c *Config) { c.EnableMetrics = enable }
}

func WithStrictValidation(strict bool) ConfigOption {
	return func(c *Config) { c.StrictValidation = strict }
}

// GAP FIX #5: Security limit options
func WithMaxTemplateSize(size int) ConfigOption {
	return func(c *Config) { c.MaxTemplateSize = size }
}

func WithMaxInputSize(size int) ConfigOption {
	return func(c *Config) { c.MaxInputSize = size }
}

func WithMaxNestingDepth(depth int) ConfigOption {
	return func(c *Config) { c.MaxNestingDepth = depth }
}

func WithMaxMappings(max int) ConfigOption {
	return func(c *Config) { c.MaxMappings = max }
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.WorkerID == "" {
		return fmt.Errorf("worker ID is required")
	}
	if c.TaskType == "" {
		return fmt.Errorf("task type is required")
	}
	if c.MaxJobsActive <= 0 {
		return fmt.Errorf("max jobs active must be > 0")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be > 0")
	}
	if c.TemplatesBaseDir == "" {
		return fmt.Errorf("templates base directory required")
	}

	// GAP FIX #5: Validate limits
	if c.MaxTemplateSize <= 0 {
		return fmt.Errorf("max template size must be > 0")
	}
	if c.MaxInputSize <= 0 {
		return fmt.Errorf("max input size must be > 0")
	}
	if c.MaxNestingDepth <= 0 {
		return fmt.Errorf("max nesting depth must be > 0")
	}
	if c.MaxMappings <= 0 {
		return fmt.Errorf("max mappings must be > 0")
	}

	// Validate log level
	validLogLevels := map[string]bool{
		"debug": true, "info": true, "warn": true, "error": true,
	}
	if !validLogLevels[c.LogLevel] {
		return fmt.Errorf("invalid log level: %s", c.LogLevel)
	}

	return nil
}

// String returns string representation
func (c *Config) String() string {
	return fmt.Sprintf(
		"Config{WorkerID: %s, Timeout: %ds, MaxTemplateSize: %dB, MaxInputSize: %dB, EnableCache: %t, LogLevel: %s}",
		c.WorkerID, c.Timeout, c.MaxTemplateSize, c.MaxInputSize, c.EnableCache, c.LogLevel,
	)
}

// Helper functions
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvAsBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1" || value == "yes"
	}
	return defaultValue
}

// Pre-defined secure configurations

// DevelopmentConfig - For development with strict validation
func DevelopmentConfig() *Config {
	return &Config{
		WorkerID:               "template-dev-worker",
		TaskType:               "template-processor",
		MaxJobsActive:          3,
		Timeout:                30,
		TemplatesBaseDir:       "./templates",
		EnableCache:            false, // Fresh load for dev
		MaxCacheSize:           10,
		MaxPathLength:          500,
		MaxTemplateSize:        5 * 1024 * 1024,  // 5MB
		MaxInputSize:           5 * 1024 * 1024,  // 5MB
		MaxNestingDepth:        15,               // Lower for dev
		MaxMappings:            500,              // Lower for dev
		MaxProcessingSteps:     50,               // Lower for dev
		MaxArraySize:           5000,             // Lower for dev
		MaxExpressionLength:    500,              // Lower for dev
		MaxExpressionOperators: 25,               // Lower for dev
		MaxRegexPatternLength:  250,              // Lower for dev
		MaxRegexInputSize:      5000,             // Lower for dev
		LogLevel:               "debug",
		MaxConcurrency:         5,
		EnableMetrics:          true,
		StrictValidation:       true, // Strict in dev
		AllowUnknownFields:     false, // Strict in dev
		SupportedVersions:      []string{"1", "2"},
	}
}

// ProductionConfig - For production with optimized performance
func ProductionConfig() *Config {
	return &Config{
		WorkerID:               "template-prod-worker",
		TaskType:               "template-processor",
		MaxJobsActive:          10,
		Timeout:                30,
		TemplatesBaseDir:       "/app/templates",
		EnableCache:            true,
		MaxCacheSize:           100,
		MaxPathLength:          500,
		MaxTemplateSize:        10 * 1024 * 1024, // 10MB
		MaxInputSize:           10 * 1024 * 1024, // 10MB
		MaxNestingDepth:        20,
		MaxMappings:            1000,
		MaxProcessingSteps:     100,
		MaxArraySize:           10000,
		MaxExpressionLength:    1000,
		MaxExpressionOperators: 50,
		MaxRegexPatternLength:  500,
		MaxRegexInputSize:      10000,
		LogLevel:               "info",
		MaxConcurrency:         20,
		EnableMetrics:          true,
		StrictValidation:       false,
		AllowUnknownFields:     true,
		SupportedVersions:      []string{"1", "2"},
	}
}

// TestConfig - For testing with minimal limits
func TestConfig() *Config {
	return &Config{
		WorkerID:               "template-test-worker",
		TaskType:               "template-processor",
		MaxJobsActive:          1,
		Timeout:                10,
		TemplatesBaseDir:       "./test/templates",
		EnableCache:            false,
		MaxCacheSize:           10,
		MaxPathLength:          200,
		MaxTemplateSize:        1 * 1024 * 1024,  // 1MB
		MaxInputSize:           1 * 1024 * 1024,  // 1MB
		MaxNestingDepth:        10,
		MaxMappings:            100,
		MaxProcessingSteps:     20,
		MaxArraySize:           1000,
		MaxExpressionLength:    200,
		MaxExpressionOperators: 10,
		MaxRegexPatternLength:  100,
		MaxRegexInputSize:      1000,
		LogLevel:               "debug",
		MaxConcurrency:         1,
		EnableMetrics:          false,
		StrictValidation:       true,
		AllowUnknownFields:     false,
		SupportedVersions:      []string{"1", "2"},
	}
}

// HighSecurityConfig - Maximum security, minimal limits
func HighSecurityConfig() *Config {
	return &Config{
		WorkerID:               "template-secure-worker",
		TaskType:               "template-processor",
		MaxJobsActive:          5,
		Timeout:                15, // Shorter timeout
		TemplatesBaseDir:       "/app/templates",
		EnableCache:            true,
		MaxCacheSize:           50,
		MaxPathLength:          300,              // Shorter paths
		MaxTemplateSize:        2 * 1024 * 1024,  // 2MB - smaller
		MaxInputSize:           2 * 1024 * 1024,  // 2MB - smaller
		MaxNestingDepth:        10,               // Shallow nesting
		MaxMappings:            200,              // Fewer mappings
		MaxProcessingSteps:     30,               // Fewer steps
		MaxArraySize:           1000,             // Smaller arrays
		MaxExpressionLength:    200,              // Shorter expressions
		MaxExpressionOperators: 10,               // Fewer operators
		MaxRegexPatternLength:  100,              // Shorter patterns
		MaxRegexInputSize:      1000,             // Smaller inputs
		LogLevel:               "info",
		MaxConcurrency:         5,
		EnableMetrics:          true,
		StrictValidation:       true,  // Always strict
		AllowUnknownFields:     false, // No unknown fields
		SupportedVersions:      []string{"1"},    // Only version 1
	}
}