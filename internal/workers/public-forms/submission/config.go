package submission

import "time"

const (
	TaskTypeValidate = "validate-public-form"
	TaskTypeSave     = "save-public-form"
)

type Config struct {
	// Worker configuration
	WorkerName     string        `yaml:"worker_name" default:"public-form-submission-worker"`
	MaxJobsActive  int           `yaml:"max_jobs_active" default:"10"`
	PollInterval   time.Duration `yaml:"poll_interval" default:"100ms"`
	RequestTimeout time.Duration `yaml:"request_timeout" default:"30s"`
}

func DefaultConfig() *Config {
	return &Config{
		WorkerName:     "public-form-submission-worker",
		MaxJobsActive:  10,
		PollInterval:   100 * time.Millisecond,
		RequestTimeout: 30 * time.Second,
	}
}
