// internal/workers/data-access/query-postgresql/queries/franchise.go
package queries

import (
	"context"
	"database/sql"
	"time"
)

func FranchiseFullDetails(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	franchiseID, ok := params["franchiseId"].(string)
	if !ok {
		return nil, 0, 0, ErrMissingParam
	}

	start := time.Now()

	var id, name, description, category string
	var investmentMin, investmentMax int
	var locations string
	var isVerified bool
	var createdAt, updatedAt string

	err := db.QueryRowContext(ctx, `
		SELECT id, name, description, investment_min, investment_max, 
		       category, locations, is_verified, created_at, updated_at
		FROM franchises 
		WHERE id = $1`, franchiseID).Scan(
		&id, &name, &description,
		&investmentMin, &investmentMax,
		&category, &locations,
		&isVerified, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, 0, 0, err
	}

	result := map[string]interface{}{
		"id":            id,
		"name":          name,
		"description":   description,
		"investmentMin": investmentMin,
		"investmentMax": investmentMax,
		"category":      category,
		"locations":     locations,
		"isVerified":    isVerified,
		"createdAt":     createdAt,
		"updatedAt":     updatedAt,
	}

	execTime := time.Since(start).Milliseconds()
	return result, 1, execTime, nil
}

func FranchiseOutlets(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	franchiseID, ok := params["franchiseId"].(string)
	if !ok {
		return nil, 0, 0, ErrMissingParam
	}

	start := time.Now()

	rows, err := db.QueryContext(ctx, `
		SELECT id, franchise_id, address, city, state, country, phone
		FROM franchise_outlets 
		WHERE franchise_id = $1`, franchiseID)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var id, franchiseId, address, city, state, country, phone string
		err := rows.Scan(&id, &franchiseId, &address, &city, &state, &country, &phone)
		if err != nil {
			return nil, 0, 0, err
		}
		results = append(results, map[string]interface{}{
			"id":          id,
			"franchiseId": franchiseId,
			"address":     address,
			"city":        city,
			"state":       state,
			"country":     country,
			"phone":       phone,
		})
	}

	execTime := time.Since(start).Milliseconds()
	return results, len(results), execTime, nil
}

func FranchiseVerification(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	franchiseID, ok := params["franchiseId"].(string)
	if !ok {
		return nil, 0, 0, ErrMissingParam
	}

	start := time.Now()

	var franchiseId, verificationStatus, verifiedAt string
	var complianceScore float64

	err := db.QueryRowContext(ctx, `
		SELECT franchise_id, verification_status, verified_at, compliance_score
		FROM franchise_verification 
		WHERE franchise_id = $1`, franchiseID).Scan(
		&franchiseId, &verificationStatus, &verifiedAt, &complianceScore,
	)
	if err != nil {
		return nil, 0, 0, err
	}

	result := map[string]interface{}{
		"franchiseId":        franchiseId,
		"verificationStatus": verificationStatus,
		"verifiedAt":         verifiedAt,
		"complianceScore":    complianceScore,
	}

	execTime := time.Since(start).Milliseconds()
	return result, 1, execTime, nil
}

func FranchiseDetails(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	franchiseIDs, ok := params["franchiseIds"].([]string)
	if !ok || len(franchiseIDs) == 0 {
		return nil, 0, 0, ErrMissingParam
	}

	start := time.Now()

	placeholders := make([]string, len(franchiseIDs))
	args := make([]interface{}, len(franchiseIDs))
	for i, id := range franchiseIDs {
		placeholders[i] = "$" + string(rune('1'+i))
		args[i] = id
	}

	query := `SELECT id, name, investment_min, investment_max, category 
	          FROM franchises WHERE id IN (` + join(placeholders, ",") + `)`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var id, name, category string
		var investmentMin, investmentMax int
		err := rows.Scan(&id, &name, &investmentMin, &investmentMax, &category)
		if err != nil {
			return nil, 0, 0, err
		}
		results = append(results, map[string]interface{}{
			"id":            id,
			"name":          name,
			"investmentMin": investmentMin,
			"investmentMax": investmentMax,
			"category":      category,
		})
	}

	execTime := time.Since(start).Milliseconds()
	return results, len(results), execTime, nil
}

func join(a []string, sep string) string {
	if len(a) == 0 {
		return ""
	}
	if len(a) == 1 {
		return a[0]
	}
	n := len(sep) * (len(a) - 1)
	for i := 0; i < len(a); i++ {
		n += len(a[i])
	}
	b := make([]byte, n)
	bp := copy(b, a[0])
	for _, s := range a[1:] {
		bp += copy(b[bp:], sep)
		bp += copy(b[bp:], s)
	}
	return string(b)
}
