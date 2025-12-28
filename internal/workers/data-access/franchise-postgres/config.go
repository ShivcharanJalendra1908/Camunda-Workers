// internal/workers/data-access/franchise-postgres/config.go
package franchisepostgres

import "time"

type Config struct {
	// Worker configuration
	WorkerName     string        `yaml:"worker_name" default:"franchise-postgres-worker"`
	JobType        string        `yaml:"job_type" default:"franchise-postgres"`
	MaxJobsActive  int           `yaml:"max_jobs_active" default:"10"`
	PollInterval   time.Duration `yaml:"poll_interval" default:"100ms"`
	RequestTimeout time.Duration `yaml:"request_timeout" default:"30s"`

	// Database configuration
	DBMaxRetries int           `yaml:"db_max_retries" default:"3"`
	DBRetryDelay time.Duration `yaml:"db_retry_delay" default:"1s"`

	// Transaction configuration
	TxTimeout time.Duration `yaml:"tx_timeout" default:"10s"`

	// Feature flags
	EnableSoftDelete bool `yaml:"enable_soft_delete" default:"false"`
	EnableAuditLog   bool `yaml:"enable_audit_log" default:"true"`
}

func DefaultConfig() *Config {
	return &Config{
		WorkerName:       "franchise-postgres-worker",
		JobType:          "franchise-postgres",
		MaxJobsActive:    10,
		PollInterval:     100 * time.Millisecond,
		RequestTimeout:   30 * time.Second,
		DBMaxRetries:     3,
		DBRetryDelay:     1 * time.Second,
		TxTimeout:        10 * time.Second,
		EnableSoftDelete: false,
		EnableAuditLog:   true,
	}
}