// internal/workers/data-access/query-postgresql/queries/user.go
package queries

import (
	"camunda-workers/internal/crypto"

	"context"
	"database/sql"
	"time"
)

func UserProfile(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	userID, ok := params["userId"].(string)
	if !ok {
		return nil, 0, 0, ErrMissingParam
	}

	start := time.Now()

	var id, email, status string
	var name, phone sql.NullString
	var emailVerified bool

	err := db.QueryRowContext(ctx, `
		SELECT id, email, email_verified, status, name, phone
		FROM users
		WHERE id = $1`, userID).Scan(
		&id, &email, &emailVerified, &status, &name, &phone,
	)
	if err != nil {
		return nil, 0, 0, err
	}

	// Fetch subscription tier from user_subscriptions
	var subscriptionTier string
	subErr := db.QueryRowContext(ctx, `
		SELECT tier FROM user_subscriptions
		WHERE user_id = $1 AND is_valid = true
		ORDER BY created_at DESC LIMIT 1`, userID).Scan(&subscriptionTier)
	if subErr != nil {
		subscriptionTier = "free" // default agar subscription nahi mili
	}

	result := map[string]interface{}{
		"id":               id,
		"email":            email,
		"emailVerified":    emailVerified,
		"status":           status,
		"name":             name.String,
		"phone":            phone.String,
		"subscriptionTier": subscriptionTier,
	}

	execTime := time.Since(start).Milliseconds()
	return result, 1, execTime, nil
}

// // internal/workers/data-access/query-postgresql/queries/user.go
// package queries

// import (
// 	"context"
// 	"database/sql"
// 	"time"
// )

// func UserProfile(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
// 	userID, ok := params["userId"].(string)
// 	if !ok {
// 		return nil, 0, 0, ErrMissingParam
// 	}

// 	start := time.Now()

// 	var id, name, email, subscriptionTier string
// 	var capitalAvailable, industryExperience int
// 	var locationPreferences, interests string

// 	err := db.QueryRowContext(ctx, `
// 		SELECT id, name, email, subscription_tier, capital_available,
// 		       industry_experience, location_preferences, interests
// 		FROM users
// 		WHERE id = $1`, userID).Scan(
// 		&id, &name, &email,
// 		&subscriptionTier, &capitalAvailable,
// 		&industryExperience, &locationPreferences,
// 		&interests,
// 	)
// 	if err != nil {
// 		return nil, 0, 0, err
// 	}

// 	result := map[string]interface{}{
// 		"id":                  id,
// 		"name":                name,
// 		"email":               email,
// 		"subscriptionTier":    subscriptionTier,
// 		"capitalAvailable":    capitalAvailable,
// 		"industryExperience":  industryExperience,
// 		"locationPreferences": locationPreferences,
// 		"interests":           interests,
// 	}

// 	execTime := time.Since(start).Milliseconds()
// 	return result, 1, execTime, nil
// }
