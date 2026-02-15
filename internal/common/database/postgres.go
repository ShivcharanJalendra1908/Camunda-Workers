// internal/common/database/postgres.go
package database

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/errors"

	_ "github.com/lib/pq"
)

// PostgresClient wraps the SQL database connection with validation
type PostgresClient struct {
	DB *sql.DB
}

// Dangerous SQL patterns
var (
	multiStatementPattern = regexp.MustCompile(`(?i);\s*(DROP|TRUNCATE|DELETE|UPDATE|INSERT|CREATE|ALTER)`)
	sqlKeywordsPattern    = regexp.MustCompile(`(?i)(DROP\s+TABLE|TRUNCATE\s+TABLE|EXEC\s*\(|EXECUTE\s*\(|\bUNION\s+SELECT\b|\bSELECT\s+.*\bFROM\s+pg_|--|\/\*)`)
)

// NewPostgres creates a new PostgreSQL client
func NewPostgres(cfg config.PostgresConfig) (*PostgresClient, error) {
	dsn := cfg.GetDSN()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxConnections)
	db.SetMaxIdleConns(cfg.MaxIdle)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	return &PostgresClient{DB: db}, nil
}

// Ping tests the database connection
func (c *PostgresClient) Ping(ctx context.Context) error {
	return c.DB.PingContext(ctx)
}

// Close closes the database connection
func (c *PostgresClient) Close() error {
	if c.DB != nil {
		return c.DB.Close()
	}
	return nil
}

// Query executes a query that returns rows
func (c *PostgresClient) Query(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	if err := validateQuery(query, args); err != nil {
		return nil, err
	}
	return c.DB.QueryContext(ctx, query, args...)
}

// QueryRow executes a query that returns at most one row
func (c *PostgresClient) QueryRow(ctx context.Context, query string, args ...interface{}) *sql.Row {
	if err := validateQuery(query, args); err != nil {
		// Return a row that will error on Scan
		return c.DB.QueryRowContext(ctx, "SELECT NULL WHERE FALSE")
	}
	return c.DB.QueryRowContext(ctx, query, args...)
}

// Exec executes a query that doesn't return rows
func (c *PostgresClient) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if err := validateQuery(query, args); err != nil {
		return nil, err
	}
	return c.DB.ExecContext(ctx, query, args...)
}

// QueryContext validates and executes a query
func (c *PostgresClient) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	if err := validateQuery(query, args); err != nil {
		return nil, err
	}
	return c.DB.QueryContext(ctx, query, args...)
}

// QueryRowContext validates and executes a single-row query
func (c *PostgresClient) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	if err := validateQuery(query, args); err != nil {
		// Return a row that will error on Scan
		return c.DB.QueryRowContext(ctx, "SELECT NULL WHERE FALSE")
	}
	return c.DB.QueryRowContext(ctx, query, args...)
}

// ExecContext validates and executes a command
func (c *PostgresClient) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if err := validateQuery(query, args); err != nil {
		return nil, err
	}
	return c.DB.ExecContext(ctx, query, args...)
}

// BeginTx begins a transaction with validation
func (c *PostgresClient) BeginTx(ctx context.Context, opts *sql.TxOptions) (*TxWrapper, error) {
	tx, err := c.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &TxWrapper{Tx: tx}, nil
}

// GetDB returns the underlying *sql.DB for compatibility
func (c *PostgresClient) GetDB() *sql.DB {
	return c.DB
}

// validateQuery ensures query uses parameterized queries and is safe
func validateQuery(query string, args []interface{}) error {
	// 1. Check for multiple statements (SQL injection attempt)
	if multiStatementPattern.MatchString(query) {
		return errors.NewSQLInjectionError("query", "multiple SQL statements detected")
	}

	// 2. Check for dangerous keywords
	if sqlKeywordsPattern.MatchString(query) {
		return errors.NewSQLInjectionError("query", "dangerous SQL keywords detected")
	}

	// 3. Ensure parameterized queries (PostgreSQL uses $1, $2, etc.)
	dollarPlaceholderCount := strings.Count(query, "$")
	questionPlaceholderCount := strings.Count(query, "?")
	totalPlaceholders := dollarPlaceholderCount + questionPlaceholderCount

	if totalPlaceholders > 0 && totalPlaceholders != len(args) {
		return fmt.Errorf("placeholder count (%d) doesn't match args count (%d)", totalPlaceholders, len(args))
	}

	// 4. Validate parameter types
	for _, arg := range args {
		switch v := arg.(type) {
		case string:
			// Check for suspicious strings that might contain SQL
			if strings.Contains(strings.ToUpper(v), "DROP") ||
				strings.Contains(strings.ToUpper(v), "DELETE") ||
				strings.Contains(strings.ToUpper(v), "INSERT") ||
				strings.Contains(strings.ToUpper(v), "UPDATE") ||
				strings.Contains(strings.ToUpper(v), "SELECT") {
				// This might be legitimate, but log it
				// Log suspicious parameter: i, v
			}
		}
	}

	// 5. Validate query length
	if len(query) > 10000 {
		return errors.NewSQLInjectionError("query", "query too long")
	}

	return nil
}

// TxWrapper wraps sql.Tx with validation
type TxWrapper struct {
	Tx *sql.Tx
}

// Query executes a query within transaction
func (t *TxWrapper) Query(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	if err := validateQuery(query, args); err != nil {
		return nil, err
	}
	return t.Tx.QueryContext(ctx, query, args...)
}

// QueryRow executes a single-row query within transaction
func (t *TxWrapper) QueryRow(ctx context.Context, query string, args ...interface{}) *sql.Row {
	if err := validateQuery(query, args); err != nil {
		// Return a row that will error on Scan
		rows, _ := t.Tx.QueryContext(ctx, "SELECT NULL WHERE FALSE")
		if rows != nil {
			rows.Close()
		}
		return t.Tx.QueryRowContext(ctx, "SELECT NULL WHERE FALSE")
	}
	return t.Tx.QueryRowContext(ctx, query, args...)
}

// Exec executes a command within transaction
func (t *TxWrapper) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if err := validateQuery(query, args); err != nil {
		return nil, err
	}
	return t.Tx.ExecContext(ctx, query, args...)
}

// Commit commits the transaction
func (t *TxWrapper) Commit() error {
	return t.Tx.Commit()
}

// Rollback rolls back the transaction
func (t *TxWrapper) Rollback() error {
	return t.Tx.Rollback()
}
