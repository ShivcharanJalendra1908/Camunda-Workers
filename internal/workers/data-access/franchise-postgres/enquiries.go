package franchisepostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// handleCreateEnquiry handles inserting a new enquiry and logging it in the audit trail.
func (h *Handler) handleCreateEnquiry(ctx context.Context, variables string) (*BaseOutput, error) {
	var input CreateEnquiryInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidationError, err)
	}

	userID, err := uuid.Parse(input.UserID)
	if err != nil {
		return nil, fmt.Errorf("%w: user_id: %v", ErrInvalidUUID, err)
	}

	entityID, err := uuid.Parse(input.EntityID)
	if err != nil {
		return nil, fmt.Errorf("%w: entity_id: %v", ErrInvalidUUID, err)
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	// 1. Insert enquiry
	insertEnquiryQuery := `
		INSERT INTO enquiries (
			user_id, entity_id, status, message, preferred_contact, last_activity_at, created_at, updated_at
		) VALUES (
			$1, $2, 'PENDING', $3, $4, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		) RETURNING id`

	var enquiryID uuid.UUID
	var msgVal *string
	if input.Message != "" {
		msgSanitized := h.sanitizer.SanitizeString(input.Message)
		msgVal = &msgSanitized
	}

	err = tx.QueryRowContext(ctx, insertEnquiryQuery,
		userID,
		entityID,
		msgVal,
		input.PreferredContact,
	).Scan(&enquiryID)

	if err != nil {
		if strings.Contains(err.Error(), "uq_enquiry_pending_active") || strings.Contains(err.Error(), "23505") {
			return nil, fmt.Errorf("%w: an active pending enquiry already exists for this user and entity", ErrValidationError)
		}
		return nil, fmt.Errorf("%w: insert enquiry: %v", ErrDatabaseError, err)
	}

	// 2. Insert into enquiry_audit_log
	insertAuditQuery := `
		INSERT INTO enquiry_audit_log (
			enquiry_id, from_status, to_status, actor_id, actor_role, event_type, metadata, created_at
		) VALUES (
			$1, NULL, 'PENDING', $2, 'ROLE_USER', 'ENQUIRY_CREATED', '{}'::jsonb, CURRENT_TIMESTAMP)`

	_, err = tx.ExecContext(ctx, insertAuditQuery, enquiryID, userID)
	if err != nil {
		return nil, fmt.Errorf("%w: insert enquiry audit log: %v", ErrDatabaseError, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit transaction: %v", ErrDatabaseError, err)
	}

	return &BaseOutput{
		ID:      enquiryID.String(),
		Success: true,
		Message: "Enquiry created successfully",
	}, nil
}

// handleUpdateEnquiryStatus updates an enquiry's status and records the transition in the audit log.
func (h *Handler) handleUpdateEnquiryStatus(ctx context.Context, variables string) (*UpdateEnquiryStatusOutput, error) {
	var input UpdateEnquiryStatusInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidationError, err)
	}

	enquiryID, err := uuid.Parse(input.EnquiryID)
	if err != nil {
		return nil, fmt.Errorf("%w: enquiry_id: %v", ErrInvalidUUID, err)
	}

	actorID, err := uuid.Parse(input.ActorID)
	if err != nil {
		return nil, fmt.Errorf("%w: actor_id: %v", ErrInvalidUUID, err)
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	// 1. Get current status to verify existence and check transition
	var fromStatus string
	checkQuery := "SELECT status FROM enquiries WHERE id = $1 FOR UPDATE"
	err = tx.QueryRowContext(ctx, checkQuery, enquiryID).Scan(&fromStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: enquiry not found: %s", ErrValidationError, input.EnquiryID)
		}
		return nil, fmt.Errorf("%w: select enquiry: %v", ErrDatabaseError, err)
	}

	// If status is already what we want, return success without doing anything
	if fromStatus == input.Status {
		return &UpdateEnquiryStatusOutput{
			ID:           input.EnquiryID,
			Success:      true,
			Message:      fmt.Sprintf("Enquiry is already in status %s", input.Status),
			ReplyMessage: input.ReplyMessage,
		}, nil
	}

	// 2. Perform state update
	var closedByVal *string
	if input.Status == "CLOSED" {
		closedByVal = &input.ClosedBy
	}

	updateQuery := `
		UPDATE enquiries SET 
			status = $1, 
			responded_at = CASE WHEN $1 = 'RESPONDED' AND responded_at IS NULL THEN CURRENT_TIMESTAMP ELSE responded_at END,
			closed_at = CASE WHEN $1 = 'CLOSED' AND closed_at IS NULL THEN CURRENT_TIMESTAMP ELSE closed_at END,
			closed_by = CASE WHEN $1 = 'CLOSED' THEN $2 ELSE closed_by END,
			last_activity_at = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $3`

	_, err = tx.ExecContext(ctx, updateQuery, input.Status, closedByVal, enquiryID)
	if err != nil {
		return nil, fmt.Errorf("%w: update enquiry status: %v", ErrDatabaseError, err)
	}

	// 3. Insert audit log
	metadataObj := map[string]interface{}{}
	if closedByVal != nil {
		metadataObj["closed_by"] = *closedByVal
	}
	if input.ReplyMessage != "" {
		metadataObj["reply_message"] = input.ReplyMessage
	}
	metadataBytes, _ := json.Marshal(metadataObj)

	insertAuditQuery := `
		INSERT INTO enquiry_audit_log (
			enquiry_id, from_status, to_status, actor_id, actor_role, event_type, metadata, created_at
		) VALUES (
			$1, $2, $3, $4, $5, 'STATUS_UPDATED', $6, CURRENT_TIMESTAMP)`

	_, err = tx.ExecContext(ctx, insertAuditQuery,
		enquiryID,
		fromStatus,
		input.Status,
		actorID,
		input.ActorRole,
		metadataBytes,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: insert enquiry status audit log: %v", ErrDatabaseError, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit transaction: %v", ErrDatabaseError, err)
	}

	return &UpdateEnquiryStatusOutput{
		ID:           input.EnquiryID,
		Success:      true,
		Message:      "Enquiry status updated successfully",
		ReplyMessage: input.ReplyMessage,
	}, nil
}

// handleGetEnquiries retrieves filtered, paginated lists of enquiries.
func (h *Handler) handleGetEnquiries(ctx context.Context, variables string) (*EnquiriesOutput, error) {
	var input GetEnquiriesInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	if input.Page <= 0 {
		input.Page = 1
	}
	if input.Limit <= 0 {
		input.Limit = 10
	} else if input.Limit > 100 {
		input.Limit = 100
	}

	// Build dynamic query
	var filters []string
	var args []interface{}
	argCounter := 1

	if input.UserID != "" {
		userUUID, err := uuid.Parse(input.UserID)
		if err != nil {
			return nil, fmt.Errorf("%w: user_id: %v", ErrInvalidUUID, err)
		}
		filters = append(filters, fmt.Sprintf("user_id = $%d", argCounter))
		args = append(args, userUUID)
		argCounter++
	}

	if input.EntityID != "" {
		entityUUID, err := uuid.Parse(input.EntityID)
		if err != nil {
			return nil, fmt.Errorf("%w: entity_id: %v", ErrInvalidUUID, err)
		}
		filters = append(filters, fmt.Sprintf("entity_id = $%d", argCounter))
		args = append(args, entityUUID)
		argCounter++
	}

	if input.Status != "" {
		statusSanitized := h.sanitizer.SanitizeString(input.Status)
		filters = append(filters, fmt.Sprintf("status = $%d", argCounter))
		args = append(args, statusSanitized)
		argCounter++
	}

	filterClause := ""
	if len(filters) > 0 {
		filterClause = "WHERE " + strings.Join(filters, " AND ")
	}

	// 1. Get total count
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM enquiries %s", filterClause)
	var totalCount int
	err := h.db.QueryRowContext(ctx, countQuery, args...).Scan(&totalCount)
	if err != nil {
		return nil, fmt.Errorf("%w: count enquiries: %v", ErrDatabaseError, err)
	}

	// 2. Fetch records
	offset := (input.Page - 1) * input.Limit
	selectQuery := fmt.Sprintf(`
		SELECT 
			id, user_id, entity_id, status, message, preferred_contact, 
			responded_at, closed_at, closed_by, last_activity_at, created_at, updated_at
		FROM enquiries 
		%s 
		ORDER BY last_activity_at DESC 
		LIMIT $%d OFFSET $%d`, filterClause, argCounter, argCounter+1)

	selectArgs := append(args, input.Limit, offset)
	rows, err := h.db.QueryContext(ctx, selectQuery, selectArgs...)
	if err != nil {
		return nil, fmt.Errorf("%w: select enquiries: %v", ErrDatabaseError, err)
	}
	defer rows.Close()

	enquiries := []Enquiry{}
	for rows.Next() {
		var eq Enquiry
		var respondedAt, closedAt sql.NullTime
		var message, closedBy sql.NullString

		err := rows.Scan(
			&eq.ID,
			&eq.UserID,
			&eq.EntityID,
			&eq.Status,
			&message,
			&eq.PreferredContact,
			&respondedAt,
			&closedAt,
			&closedBy,
			&eq.LastActivityAt,
			&eq.CreatedAt,
			&eq.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: scan enquiry row: %v", ErrDatabaseError, err)
		}

		if message.Valid {
			eq.Message = &message.String
		}
		if respondedAt.Valid {
			eq.RespondedAt = &respondedAt.Time
		}
		if closedAt.Valid {
			eq.ClosedAt = &closedAt.Time
		}
		if closedBy.Valid {
			eq.ClosedBy = &closedBy.String
		}

		enquiries = append(enquiries, eq)
	}

	return &EnquiriesOutput{
		Enquiries:  enquiries,
		TotalCount: totalCount,
		Page:       input.Page,
		Limit:      input.Limit,
		Success:    true,
		Message:    "Enquiries retrieved successfully",
	}, nil
}
