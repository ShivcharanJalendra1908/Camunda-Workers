// internal/common/idempotency/checker.go
package idempotency

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrDuplicateRequest = errors.New("duplicate request detected")
	ErrProcessing       = errors.New("request already processing")
	ErrKeyExpired       = errors.New("idempotency key expired")
)

// CheckResult represents the result of idempotency check
type CheckResult struct {
	IsDuplicate  bool
	IsProcessing bool
	Response     map[string]interface{}
	CreatedAt    time.Time
}

// Checker checks for duplicate operations
type Checker interface {
	Check(ctx context.Context, key string) (*CheckResult, error)
	MarkProcessing(ctx context.Context, key, workerType string, ttl time.Duration) error
	MarkCompleted(ctx context.Context, key string, response map[string]interface{}) error
	MarkFailed(ctx context.Context, key string) error
	GetResult(ctx context.Context, key string) (*CheckResult, error)
}

// DBChecker implements database-based idempotency checking
type DBChecker struct {
	db *sql.DB
}

// NewDBChecker creates a new database checker
func NewDBChecker(db *sql.DB) *DBChecker {
	return &DBChecker{db: db}
}

// ============================================================================
// CHECK OPERATIONS
// ============================================================================

// Check checks if operation was already performed
func (c *DBChecker) Check(ctx context.Context, key string) (*CheckResult, error) {
	query := `
		SELECT status, response_data, created_at, expires_at
		FROM idempotency_keys
		WHERE idempotency_key = $1
	`

	var status string
	var responseJSON []byte
	var createdAt, expiresAt time.Time

	err := c.db.QueryRowContext(ctx, query, key).Scan(&status, &responseJSON, &createdAt, &expiresAt)

	if err == sql.ErrNoRows {
		// Not found - first time operation
		return &CheckResult{
			IsDuplicate:  false,
			IsProcessing: false,
		}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to check idempotency: %w", err)
	}

	// Check if expired
	if time.Now().After(expiresAt) {
		return nil, ErrKeyExpired
	}

	result := &CheckResult{
		IsDuplicate: true,
		CreatedAt:   createdAt,
	}

	switch status {
	case "processing":
		result.IsProcessing = true
		return result, ErrProcessing

	case "completed":
		// Parse response
		if len(responseJSON) > 0 {
			var response map[string]interface{}
			if err := json.Unmarshal(responseJSON, &response); err == nil {
				result.Response = response
			}
		}
		return result, ErrDuplicateRequest

	case "failed":
		// Allow retry on failed operations
		return &CheckResult{
			IsDuplicate:  false,
			IsProcessing: false,
		}, nil

	default:
		return result, fmt.Errorf("unknown status: %s", status)
	}
}

// ============================================================================
// MARK OPERATIONS
// ============================================================================

// MarkProcessing marks operation as processing
func (c *DBChecker) MarkProcessing(ctx context.Context, key, workerType string, ttl time.Duration) error {
	query := `
		INSERT INTO idempotency_keys (
			idempotency_key, 
			request_hash, 
			status, 
			worker_type,
			created_at, 
			expires_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (idempotency_key) DO NOTHING
	`

	expiresAt := time.Now().Add(ttl)
	_, err := c.db.ExecContext(ctx, query,
		key,
		"", // request_hash can be empty for worker operations
		"processing",
		workerType,
		time.Now(),
		expiresAt,
	)

	if err != nil {
		return fmt.Errorf("failed to mark processing: %w", err)
	}

	return nil
}

// MarkCompleted marks operation as completed with response
func (c *DBChecker) MarkCompleted(ctx context.Context, key string, response map[string]interface{}) error {
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	query := `
		UPDATE idempotency_keys
		SET status = $1, response_data = $2, completed_at = $3
		WHERE idempotency_key = $4
	`

	_, err = c.db.ExecContext(ctx, query, "completed", responseJSON, time.Now(), key)
	if err != nil {
		return fmt.Errorf("failed to mark completed: %w", err)
	}

	return nil
}

// MarkFailed marks operation as failed
func (c *DBChecker) MarkFailed(ctx context.Context, key string) error {
	query := `
		UPDATE idempotency_keys
		SET status = $1, completed_at = $2
		WHERE idempotency_key = $3
	`

	_, err := c.db.ExecContext(ctx, query, "failed", time.Now(), key)
	if err != nil {
		return fmt.Errorf("failed to mark failed: %w", err)
	}

	return nil
}

// ============================================================================
// GET OPERATIONS
// ============================================================================

// GetResult retrieves the result of a completed operation
func (c *DBChecker) GetResult(ctx context.Context, key string) (*CheckResult, error) {
	return c.Check(ctx, key)
}

// ============================================================================
// DATABASE-SPECIFIC CHECKS
// ============================================================================

// CheckApplicationExists checks if application already exists
func (c *DBChecker) CheckApplicationExists(ctx context.Context, seekerID, franchiseID string) (bool, string, error) {
	query := `
		SELECT id FROM franchise_applications
		WHERE seeker_id = $1 AND franchise_id = $2 
		AND status IN ('submitted', 'under_review')
		LIMIT 1
	`

	var applicationID string
	err := c.db.QueryRowContext(ctx, query, seekerID, franchiseID).Scan(&applicationID)

	if err == sql.ErrNoRows {
		return false, "", nil
	}

	if err != nil {
		return false, "", fmt.Errorf("failed to check application: %w", err)
	}

	return true, applicationID, nil
}

// CheckNotificationSent checks if notification was sent today
func (c *DBChecker) CheckNotificationSent(ctx context.Context, notifType, recipientID, applicationID string) (bool, string, error) {
	today := time.Now().UTC().Format("2006-01-02")

	query := `
		SELECT id FROM notifications
		WHERE notification_type = $1 
		AND recipient_id = $2 
		AND application_id = $3
		AND DATE(sent_at) = $4
		LIMIT 1
	`

	var notificationID string
	err := c.db.QueryRowContext(ctx, query, notifType, recipientID, applicationID, today).Scan(&notificationID)

	if err == sql.ErrNoRows {
		return false, "", nil
	}

	if err != nil {
		return false, "", fmt.Errorf("failed to check notification: %w", err)
	}

	return true, notificationID, nil
}

// CheckFavoriteExists checks if favorite already exists
func (c *DBChecker) CheckFavoriteExists(ctx context.Context, userID, franchiseID string) (bool, string, error) {
	query := `
		SELECT id FROM user_favorites
		WHERE user_id = $1 AND franchise_id = $2
		LIMIT 1
	`

	var favoriteID string
	err := c.db.QueryRowContext(ctx, query, userID, franchiseID).Scan(&favoriteID)

	if err == sql.ErrNoRows {
		return false, "", nil
	}

	if err != nil {
		return false, "", fmt.Errorf("failed to check favorite: %w", err)
	}

	return true, favoriteID, nil
}

// CheckCRMContactExists checks if CRM contact exists by email
func (c *DBChecker) CheckCRMContactExists(ctx context.Context, email string) (bool, string, error) {
	// This would query your CRM tracking table or call Zoho API
	// For now, returning false to allow operation
	return false, "", nil
}

// ============================================================================
// CLEANUP OPERATIONS
// ============================================================================

// CleanupExpired removes expired idempotency keys
func (c *DBChecker) CleanupExpired(ctx context.Context) (int64, error) {
	query := `
		DELETE FROM idempotency_keys
		WHERE expires_at < $1
	`

	result, err := c.db.ExecContext(ctx, query, time.Now())
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired keys: %w", err)
	}

	count, _ := result.RowsAffected()
	return count, nil
}

// GetStats returns statistics about idempotency keys
func (c *DBChecker) GetStats(ctx context.Context) (map[string]int64, error) {
	query := `
		SELECT 
			status,
			COUNT(*) as count
		FROM idempotency_keys
		WHERE expires_at > $1
		GROUP BY status
	`

	rows, err := c.db.QueryContext(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[string]int64)
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		stats[status] = count
	}

	return stats, nil
}
