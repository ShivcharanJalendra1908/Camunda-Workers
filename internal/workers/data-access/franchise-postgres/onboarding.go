package franchisepostgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// handleCreatePendingEntity creates a new franchise record with status 'pending'
func (h *Handler) handleCreatePendingEntity(ctx context.Context, variables string) (map[string]interface{}, error) {
	var input CreatePendingEntityInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	entityType := strings.ToLower(input.EntityType)
	switch entityType {
	case "franchises":
		entityType = "franchise"
	case "associations":
		entityType = "association"
	case "master-franchise", "master_franchises", "master franchises", "masterfranchise":
		entityType = "master_franchise"
	case "":
		entityType = "franchise"
	default:
		entityType = strings.TrimSuffix(entityType, "s")
		if entityType == "master-franchise" || entityType == "master franchise" || entityType == "masterfranchise" {
			entityType = "master_franchise"
		}
	}

	var name, email string

	if entityType == "association" {
		if val, ok := input.FormData["associationName"].(string); ok {
			name = val
		}
		if val, ok := input.FormData["contactEmail"].(string); ok {
			email = val
		}
	} else {
		if val, ok := input.FormData["companyName"].(string); ok {
			name = val
		} else if val, ok := input.FormData["brandName"].(string); ok {
			name = val
		}
		if val, ok := input.FormData["email"].(string); ok {
			email = val
		}
	}

	slug := strings.ToLower(strings.ReplaceAll(name, " ", "-"))

	// Create idempotency key based on email and name to avoid duplicate pending records
	idempotencyKey := fmt.Sprintf("create_pending_%s_%s", email, slug)

	// Check idempotency
	result, err := h.idempotencyChecker.Check(ctx, idempotencyKey)
	if err != nil && err.Error() != "duplicate request detected" && err.Error() != "request already processing" {
		h.logger.Warn("Idempotency check failed, proceeding anyway", map[string]interface{}{"error": err.Error()})
	} else if result != nil && result.IsDuplicate {
		h.logger.Info("Duplicate request detected by idempotency checker", map[string]interface{}{"key": idempotencyKey})
		// If processed, we would ideally fetch the existing ID, but for simplicity we'll just query it
		var existingID uuid.UUID
		err := h.db.QueryRowContext(ctx, "SELECT id FROM franchises WHERE contact_email = $1 AND slug = $2 LIMIT 1", email, slug).Scan(&existingID)
		if err == nil {
			return map[string]interface{}{
				"franchiseId": existingID.String(),
				"success":     true,
			}, nil
		}
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	var memberCount *int
	var membershipFeeMin, membershipFeeMax *float64

	if entityType == "association" {
		if val, ok := input.FormData["memberCount"].(float64); ok {
			mc := int(val)
			memberCount = &mc
		}
		if val, ok := input.FormData["membershipFeeMin"].(float64); ok {
			membershipFeeMin = &val
		}
		if val, ok := input.FormData["membershipFeeMax"].(float64); ok {
			membershipFeeMax = &val
		}
	}

	var encryptedEmail string
	if email != "" {
		if h.encryptor == nil {
			return nil, fmt.Errorf("encryptor not initialized")
		}
		enc, err := h.encryptor.Encrypt([]byte(email))
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt email: %v", err)
		}
		encryptedEmail = enc
	}

	query := `
		INSERT INTO listings (
			name, slug, contact_email, entity_type, status, created_at, updated_at, created_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		) RETURNING id`

	var listingID uuid.UUID
	now := time.Now()
	systemUserID := "00000000-0000-0000-0000-000000000001" // System user for pending guest entities

	err = tx.QueryRowContext(ctx, query,
		h.sanitizer.SanitizeString(name),
		h.sanitizer.SanitizeString(slug),
		encryptedEmail,
		entityType,
		"pending",
		now,
		now,
		systemUserID,
	).Scan(&listingID)

	if err != nil {
		return nil, fmt.Errorf("%w: insert pending listing: %v", ErrDatabaseError, err)
	}

	// Insert into child table
	switch entityType {
	case "association":
		var assocType, sectorRep, memberType, memberSizeClass string
		if val, ok := input.FormData["association_type"].(string); ok { assocType = val }
		if val, ok := input.FormData["sector_represented"].(string); ok { sectorRep = val }
		if val, ok := input.FormData["member_type"].(string); ok { memberType = val }
		if val, ok := input.FormData["member_size_classification"].(string); ok { memberSizeClass = val }

		assocQuery := `INSERT INTO associations (id, member_count, membership_fee_min, membership_fee_max, association_type, sector_represented, member_type, member_size_classification) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
		_, err = tx.ExecContext(ctx, assocQuery, listingID, memberCount, membershipFeeMin, membershipFeeMax, assocType, sectorRep, memberType, memberSizeClass)
		if err != nil {
			return nil, fmt.Errorf("%w: insert association child: %v", ErrDatabaseError, err)
		}
	case "master_franchise":
		mfQuery := `INSERT INTO master_franchises (id) VALUES ($1)`
		_, err = tx.ExecContext(ctx, mfQuery, listingID)
		if err != nil {
			return nil, fmt.Errorf("%w: insert master_franchise child: %v", ErrDatabaseError, err)
		}
	default:
		// Default to franchise
		franQuery := `INSERT INTO franchises (id, total_outlets, units_count) VALUES ($1, 0, 0)`
		_, err = tx.ExecContext(ctx, franQuery, listingID)
		if err != nil {
			return nil, fmt.Errorf("%w: insert franchise child: %v", ErrDatabaseError, err)
		}
	}

	// Store association_metadata if it's an association
	if entityType == "association" {
		metadataJSON, _ := json.Marshal(input.FormData)
		updateQuery := `UPDATE associations SET association_metadata = $1 WHERE id = $2`
		_, err = tx.ExecContext(ctx, updateQuery, metadataJSON, listingID)
		if err != nil {
			h.logger.Warn("Failed to insert association_metadata", map[string]interface{}{"error": err.Error()})
		}
	}

	// Insert dedicated documents if present
	if docsRaw, ok := input.FormData["documents"].([]interface{}); ok {
		for _, docRaw := range docsRaw {
			if doc, ok := docRaw.(map[string]interface{}); ok {
				docType, _ := doc["type"].(string)
				s3Url, _ := doc["url"].(string)
				if docType != "" && s3Url != "" {
					docQuery := `INSERT INTO listing_documents (listing_id, document_type, s3_url) VALUES ($1, $2, $3)`
					_, err = tx.ExecContext(ctx, docQuery, listingID, h.sanitizer.SanitizeString(docType), h.sanitizer.SanitizeString(s3Url))
					if err != nil {
						h.logger.Warn("Failed to insert franchise document", map[string]interface{}{"error": err.Error(), "type": docType})
					}
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit transaction: %v", ErrDatabaseError, err)
	}

	// Mark idempotent
	err = h.idempotencyChecker.MarkCompleted(ctx, idempotencyKey, nil)
	if err != nil {
		h.logger.Warn("Failed to mark request as processed", map[string]interface{}{"error": err.Error()})
	}

	return map[string]interface{}{
		"franchiseId": listingID.String(),
		"success":     true,
	}, nil
}

// handleUpdateStatus updates the status and rejection reason of an entity
func (h *Handler) handleUpdateStatus(ctx context.Context, variables string) (map[string]interface{}, error) {
	var input UpdateStatusInput
	if err := json.Unmarshal([]byte(variables), &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	franchiseID, err := uuid.Parse(input.FranchiseID)
	if err != nil {
		return nil, fmt.Errorf("%w: franchiseId: %v", ErrInvalidUUID, err)
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: begin transaction: %v", ErrDatabaseError, err)
	}
	defer tx.Rollback()

	query := `
		UPDATE franchises 
		SET status = $1, rejection_reason = $2, updated_at = $3, reviewed_by = NULL, reviewed_at = $3,
		    approved_at = CASE WHEN $1 = 'live' THEN $3 ELSE approved_at END
		WHERE id = $4
	`

	docStatus := "pending"
	switch input.Status {
	case "live":
		docStatus = "verified"
	case "rejected":
		docStatus = "rejected"
	}

	docQuery := `
		UPDATE listing_documents
		SET status = $1, rejection_reason = $2, updated_at = $3
		WHERE listing_id = $4
	`

	_, err = tx.ExecContext(ctx, query,
		h.sanitizer.SanitizeString(input.Status),
		h.sanitizer.SanitizeString(input.RejectionReason),
		time.Now(),
		franchiseID,
	)

	if err != nil {
		return nil, fmt.Errorf("%w: update status: %v", ErrDatabaseError, err)
	}

	_, err = tx.ExecContext(ctx, docQuery,
		docStatus,
		h.sanitizer.SanitizeString(input.RejectionReason),
		time.Now(),
		franchiseID,
	)

	if err != nil {
		h.logger.Warn("Failed to update franchise documents status", map[string]interface{}{"error": err.Error(), "franchiseId": franchiseID})
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: commit transaction: %v", ErrDatabaseError, err)
	}

	return map[string]interface{}{
		"success": true,
		"status":  input.Status,
	}, nil
}
