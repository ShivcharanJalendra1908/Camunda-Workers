// internal/common/database/postgres.go
package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"camunda-workers/internal/common/config"

	_ "github.com/lib/pq"
)

// PostgresClient wraps the SQL database connection
type PostgresClient struct {
	DB *sql.DB
}

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
	return c.DB.QueryContext(ctx, query, args...)
}

// QueryRow executes a query that returns at most one row
func (c *PostgresClient) QueryRow(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return c.DB.QueryRowContext(ctx, query, args...)
}

// Exec executes a query that doesn't return rows
func (c *PostgresClient) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return c.DB.ExecContext(ctx, query, args...)
}

// GetDB returns the underlying *sql.DB for compatibility
func (c *PostgresClient) GetDB() *sql.DB {
	return c.DB
}

// // internal/common/database/postgres.go
// package database

// import (
// 	"context"
// 	"database/sql"
// 	"fmt"
// 	"time"

// 	"camunda-workers/internal/common/config"

// 	_ "github.com/lib/pq"
// )

// // PostgresClient wraps the SQL database connection
// type PostgresClient struct {
// 	DB *sql.DB
// }

// // NewPostgres creates a new PostgreSQL client
// func NewPostgres(cfg config.PostgresConfig) (*PostgresClient, error) {
// 	dsn := fmt.Sprintf(
// 		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
// 		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database,
// 	)

// 	db, err := sql.Open("postgres", dsn)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to open postgres: %w", err)
// 	}

// 	db.SetMaxOpenConns(cfg.MaxConnections)
// 	db.SetMaxIdleConns(cfg.MaxIdle)
// 	db.SetConnMaxLifetime(5 * time.Minute)
// 	db.SetConnMaxIdleTime(5 * time.Minute)

// 	return &PostgresClient{DB: db}, nil
// }

// // Ping tests the database connection
// func (c *PostgresClient) Ping() error {
// 	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
// 	defer cancel()
// 	return c.DB.PingContext(ctx)
// }

// // Close closes the database connection
// func (c *PostgresClient) Close() error {
// 	if c.DB != nil {
// 		return c.DB.Close()
// 	}
// 	return nil
// }

// // Query executes a query that returns rows
// func (c *PostgresClient) Query(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
// 	return c.DB.QueryContext(ctx, query, args...)
// }

// // QueryRow executes a query that returns at most one row
// func (c *PostgresClient) QueryRow(ctx context.Context, query string, args ...interface{}) *sql.Row {
// 	return c.DB.QueryRowContext(ctx, query, args...)
// }

// // Exec executes a query that doesn't return rows
// func (c *PostgresClient) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
// 	return c.DB.ExecContext(ctx, query, args...)
// }
