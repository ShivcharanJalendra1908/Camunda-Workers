// internal/workers/data-access/query-postgresql/queries/user.go
package queries

import (
	"context"
	"database/sql"
	"time"
)

func UserProfile(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	userID, ok := params["userId"].(string)
	if !ok {
		return nil, 0, 0, ErrMissingParam
	}

	start := time.Now()

	var id, name, email, subscriptionTier string
	var capitalAvailable, industryExperience int
	var locationPreferences, interests string

	err := db.QueryRowContext(ctx, `
		SELECT id, name, email, subscription_tier, capital_available, 
		       industry_experience, location_preferences, interests
		FROM users 
		WHERE id = $1`, userID).Scan(
		&id, &name, &email,
		&subscriptionTier, &capitalAvailable,
		&industryExperience, &locationPreferences,
		&interests,
	)
	if err != nil {
		return nil, 0, 0, err
	}

	result := map[string]interface{}{
		"id":                  id,
		"name":                name,
		"email":               email,
		"subscriptionTier":    subscriptionTier,
		"capitalAvailable":    capitalAvailable,
		"industryExperience":  industryExperience,
		"locationPreferences": locationPreferences,
		"interests":           interests,
	}

	execTime := time.Since(start).Milliseconds()
	return result, 1, execTime, nil
}
