package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"camunda-workers/internal/models"
)

// GuestAuditRepo handles async PG writes for guest audit logs.
type GuestAuditRepo struct {
	db    *sql.DB
	queue chan *models.GuestAuditEvent
	done  chan struct{}
}

// NewGuestAuditRepo creates a new audit repo with a background writer goroutine.
func NewGuestAuditRepo(db *sql.DB, queueSize int) *GuestAuditRepo {
	if queueSize <= 0 {
		queueSize = 1000
	}

	repo := &GuestAuditRepo{
		db:    db,
		queue: make(chan *models.GuestAuditEvent, queueSize),
		done:  make(chan struct{}),
	}

	go repo.processQueue()

	return repo
}

// LogEvent enqueues an audit event for async writing. Never blocks.
func (r *GuestAuditRepo) LogEvent(event *models.GuestAuditEvent) {
	select {
	case r.queue <- event:
	default:
		// Queue full â€” drop event rather than blocking the request
	}
}

// Close waits for the queue to drain and closes the repo.
func (r *GuestAuditRepo) Close() {
	close(r.queue)
	<-r.done
}

// processQueue is the background goroutine that writes audit events to PG.
func (r *GuestAuditRepo) processQueue() {
	defer close(r.done)

	for event := range r.queue {
		if err := r.writeEvent(context.Background(), event); err != nil {
			fmt.Printf("[guest-audit] failed to write event: %v\n", err)
		}
	}
}

// writeEvent inserts a single audit event into PostgreSQL.
func (r *GuestAuditRepo) writeEvent(ctx context.Context, event *models.GuestAuditEvent) error {
	anomalyJSON, err := json.Marshal(event.AnomalyFlags)
	if err != nil {
		anomalyJSON = []byte("[]")
	}

	query := `
		INSERT INTO guest_audit_log (
			session_id, composite_key, action, route_group,
			queries_used, credits_used,
			anomaly_flags, blocked, block_reason,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err = r.db.ExecContext(ctx, query,
		event.SessionID,
		event.CompositeKey,
		event.Action,
		event.RouteGroup,
		event.QueriesUsed,
		event.CreditsUsed,
		string(anomalyJSON),
		event.Blocked,
		event.BlockReason,
		event.CreatedAt,
	)

	return err
}

// QueryAuditLog queries recent audit events (admin/debugging).
func (r *GuestAuditRepo) QueryAuditLog(ctx context.Context, limit int) ([]*models.GuestAuditEvent, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, session_id, composite_key, action, route_group,
		       queries_used, credits_used,
		       anomaly_flags, blocked, block_reason, created_at
		FROM guest_audit_log
		ORDER BY created_at DESC
		LIMIT $1
	`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit log: %w", err)
	}
	defer rows.Close()

	var events []*models.GuestAuditEvent
	for rows.Next() {
		event := &models.GuestAuditEvent{}
		var anomalyJSON string

		err := rows.Scan(
			&event.ID,
			&event.SessionID,
			&event.CompositeKey,
			&event.Action,
			&event.RouteGroup,
			&event.QueriesUsed,
			&event.CreditsUsed,
			&anomalyJSON,
			&event.Blocked,
			&event.BlockReason,
			&event.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}

		_ = json.Unmarshal([]byte(anomalyJSON), &event.AnomalyFlags)
		events = append(events, event)
	}

	return events, rows.Err()
}

// Stats returns basic stats for the audit log (for health checks).
func (r *GuestAuditRepo) Stats(ctx context.Context) (totalEvents int64, blockedEvents int64, err error) {
	query := `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE blocked = TRUE)
		FROM guest_audit_log
		WHERE created_at > NOW() - INTERVAL '24 hours'
	`

	err = r.db.QueryRowContext(ctx, query).Scan(&totalEvents, &blockedEvents)
	return
}

// PruneOldEvents deletes audit events older than the given duration.
func (r *GuestAuditRepo) PruneOldEvents(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	query := `DELETE FROM guest_audit_log WHERE created_at < $1`

	result, err := r.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune audit events: %w", err)
	}

	return result.RowsAffected()
}
