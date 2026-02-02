package franchiseesindexer

import "time"

// Config holds configuration for ES indexer worker
type Config struct {
	RequestTimeout time.Duration
	MaxRetries     int
	BatchSize      int
}
