// internal/workers/data-access/query-postgresql/queries/franchise.go
package queries

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"camunda-workers/internal/crypto"
)

func FranchiseFullDetails(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	franchiseID, ok := params["franchiseId"].(string)
	if !ok {
		return nil, 0, 0, ErrMissingParam
	}

	start := time.Now()

	var id, name, description, category string
	var investmentMin, investmentMax float64
	var locations string
	var isVerified bool
	var createdAt, updatedAt string

	err := db.QueryRowContext(ctx, `
		SELECT l.id, l.name, l.description, 
		       COALESCE(fir.initial_investment_min, 0) as investment_min, 
		       COALESCE(fir.initial_investment_max, 0) as investment_max, 
		       COALESCE(lc.category_slug, '') as category, 
		       '' as locations, 
		       l.verified as is_verified, 
		       l.created_at, l.updated_at
		FROM listings l
		LEFT JOIN franchise_investment_requirement fir ON l.id = fir.franchise_id
		LEFT JOIN listing_categories lc ON l.id = lc.listing_id
		WHERE l.id = $1`, franchiseID).Scan(
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
		"investmentMin": int(investmentMin),
		"investmentMax": int(investmentMax),
		"category":      category,
		"locations":     locations,
		"isVerified":    isVerified,
		"createdAt":     createdAt,
		"updatedAt":     updatedAt,
	}

	execTime := time.Since(start).Milliseconds()
	return result, 1, execTime, nil
}

func FranchiseOutlets(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	franchiseID, ok := params["franchiseId"].(string)
	if !ok {
		return nil, 0, 0, ErrMissingParam
	}

	start := time.Now()

	rows, err := db.QueryContext(ctx, `
		SELECT id, listing_id as franchise_id, 'Not Available' as address, city_name as city, state_name as state, country_name as country, '' as phone
		FROM listing_cities 
		WHERE listing_id = $1`, franchiseID)
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

func FranchiseVerification(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	franchiseID, ok := params["franchiseId"].(string)
	if !ok {
		return nil, 0, 0, ErrMissingParam
	}

	start := time.Now()

	var franchiseId, verificationStatus, verifiedAt string
	var complianceScore float64

	err := db.QueryRowContext(ctx, `
		SELECT id as franchise_id, 
		       CASE WHEN verified THEN 'VERIFIED' ELSE 'PENDING' END as verification_status, 
		       created_at as verified_at, 
		       100 as compliance_score
		FROM listings 
		WHERE id = $1`, franchiseID).Scan(
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

func FranchiseDetails(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	franchiseIDs, ok := params["franchiseIds"].([]string)
	if !ok || len(franchiseIDs) == 0 {
		return nil, 0, 0, ErrMissingParam
	}

	start := time.Now()

	placeholders := make([]string, len(franchiseIDs))
	args := make([]interface{}, len(franchiseIDs))
	for i, id := range franchiseIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	query := `SELECT l.id, l.name, 
	                 COALESCE(fir.initial_investment_min, 0) as investment_min,
	                 COALESCE(fir.initial_investment_max, 0) as investment_max, 
	                 COALESCE(lc.category_slug, '') as category 
	          FROM listings l
	          LEFT JOIN franchise_investment_requirement fir ON l.id = fir.franchise_id
	          LEFT JOIN listing_categories lc ON l.id = lc.listing_id
	          WHERE l.id IN (` + join(placeholders, ",") + `)`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var id, name, category string
		var investmentMin, investmentMax float64
		err := rows.Scan(&id, &name, &investmentMin, &investmentMax, &category)
		if err != nil {
			return nil, 0, 0, err
		}
		results = append(results, map[string]interface{}{
			"id":            id,
			"name":          name,
			"investmentMin": int(investmentMin),
			"investmentMax": int(investmentMax),
			"category":      category,
		})
	}

	execTime := time.Since(start).Milliseconds()
	return results, len(results), execTime, nil
}

func FranchiseContactInfo(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	franchiseID, ok := params["franchiseId"].(string)
	if !ok || franchiseID == "" {
		return nil, 0, 0, ErrMissingParam
	}

	var name string
	var contactEmail sql.NullString

	err := db.QueryRowContext(ctx,
		`SELECT name, contact_email FROM listings WHERE id = $1`,
		franchiseID,
	).Scan(&name, &contactEmail)

	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, 0, 0, ErrNotFound
		}
		return nil, 0, 0, err
	}

	result := map[string]interface{}{
		"franchiseName": name,
		"entityName":    name,
	}
	if contactEmail.Valid && contactEmail.String != "" {
		decryptedEmail := contactEmail.String
		if encryptor != nil {
			dec, err := encryptor.Decrypt(contactEmail.String)
			if err == nil {
				decryptedEmail = string(dec)
			}
		}
		result["franchiseContactEmail"] = decryptedEmail
	} else {
		// Fallback - agar contact_email NULL hai toh internal team ko bhejo
		result["franchiseContactEmail"] = ""
		result["useInternalFallback"] = true
	}

	return result, 1, time.Since(start).Milliseconds(), nil
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
