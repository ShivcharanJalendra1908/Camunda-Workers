// ============================================================
// NEW FILE: internal/workers/data-access/query-postgresql/queries/user_actions.go
// Register these in registry.go as well (instructions at bottom)
// ============================================================

package queries

import (
	"context"
	"database/sql"
	"time"
)

// ============================================================
// BOOKMARK QUERIES
// ============================================================

// UserBookmarks — get all bookmarks for a user (for query-postgresql worker)
func UserBookmarks(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	userID, ok := params["userId"].(string)
	if !ok || userID == "" {
		return nil, 0, 0, ErrMissingParam
	}

	entityType, ok := params["entityType"].(string)
	if !ok || entityType == "" {
		entityType = "all" // If not specified, get all or fallback
	}

	page := 1
	limit := 20
	if p, ok := params["page"].(float64); ok && p > 0 {
		page = int(p)
	}
	if l, ok := params["limit"].(float64); ok && l > 0 && l <= 50 {
		limit = int(l)
	}
	offset := (page - 1) * limit

	rows, err := db.QueryContext(ctx, `
		SELECT
			uf.id as bookmark_id,
			uf.entity_id,
			uf.entity_type,
			COALESCE(f.name, a.name, '') as name,
			COALESCE(f.slug, a.slug, '') as slug,
			COALESCE(f.logo_url, a.logo_url, '') as logo_url,
			COALESCE(f.industry, a.industry, '') as industry,
			uf.created_at as bookmarked_at
		FROM user_favorites uf
		LEFT JOIN franchises f ON uf.entity_id = f.id AND uf.entity_type = 'franchise'
		LEFT JOIN associations a ON uf.entity_id = a.id AND uf.entity_type = 'association'
		WHERE uf.user_id = $1 AND ($2 = 'all' OR uf.entity_type = $2)
		ORDER BY uf.created_at DESC
		LIMIT $3 OFFSET $4`,
		userID, entityType, limit, offset,
	)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var bookmarks []map[string]interface{}
	for rows.Next() {
		var bookmarkID, entityID, dbEntityType, name, slug, logoURL, industry string
		var bookmarkedAt time.Time
		if err := rows.Scan(&bookmarkID, &entityID, &dbEntityType, &name, &slug, &logoURL, &industry, &bookmarkedAt); err != nil {
			continue
		}
		bookmarks = append(bookmarks, map[string]interface{}{
			"bookmarkId":   bookmarkID,
			"franchiseId":  entityID, // backwards compatibility
			"entityId":     entityID,
			"entityType":   dbEntityType,
			"name":         name,
			"slug":         slug,
			"logoUrl":      logoURL,
			"industry":     industry,
			"bookmarkedAt": bookmarkedAt,
		})
	}

	if bookmarks == nil {
		bookmarks = []map[string]interface{}{}
	}

	return map[string]interface{}{
		"bookmarks": bookmarks,
		"page":      page,
		"limit":     limit,
	}, len(bookmarks), time.Since(start).Milliseconds(), nil
}

// UserBookmarkCheck — check if a user has bookmarked a specific franchise
func UserBookmarkCheck(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	userID, ok := params["userId"].(string)
	if !ok || userID == "" {
		return nil, 0, 0, ErrMissingParam
	}
	
	entityID, ok := params["entityId"].(string)
	if !ok || entityID == "" {
		entityID, ok = params["franchiseId"].(string)
		if !ok || entityID == "" {
			return nil, 0, 0, ErrMissingParam
		}
	}

	var bookmarkID string
	err := db.QueryRowContext(ctx, `
		SELECT id FROM user_favorites WHERE user_id = $1 AND entity_id = $2`,
		userID, entityID,
	).Scan(&bookmarkID)

	if err == sql.ErrNoRows {
		return map[string]interface{}{
			"isBookmarked": false,
			"bookmarkId":   "",
		}, 0, time.Since(start).Milliseconds(), nil
	}
	if err != nil {
		return nil, 0, 0, err
	}

	return map[string]interface{}{
		"isBookmarked": true,
		"bookmarkId":   bookmarkID,
	}, 1, time.Since(start).Milliseconds(), nil
}

// ============================================================
// RATING QUERIES
// ============================================================

// UserRatingForFranchise — get a specific user's rating for a franchise
func UserRatingForFranchise(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	userID, ok := params["userId"].(string)
	if !ok || userID == "" {
		return nil, 0, 0, ErrMissingParam
	}
	entityID, ok := params["entityId"].(string)
	if !ok || entityID == "" {
		entityID, ok = params["franchiseId"].(string)
		if !ok || entityID == "" {
			return nil, 0, 0, ErrMissingParam
		}
	}

	var ratingID string
	var rating float64
	var review sql.NullString
	var updatedAt time.Time

	err := db.QueryRowContext(ctx, `
		SELECT id, rating, review, updated_at
		FROM user_ratings
		WHERE user_id = $1 AND entity_id = $2`,
		userID, entityID,
	).Scan(&ratingID, &rating, &review, &updatedAt)

	if err == sql.ErrNoRows {
		return map[string]interface{}{
			"hasRated": false,
		}, 0, time.Since(start).Milliseconds(), nil
	}
	if err != nil {
		return nil, 0, 0, err
	}

	result := map[string]interface{}{
		"hasRated":  true,
		"ratingId":  ratingID,
		"rating":    rating,
		"updatedAt": updatedAt,
	}
	if review.Valid {
		result["review"] = review.String
	}

	return result, 1, time.Since(start).Milliseconds(), nil
}

// FranchiseRatings — get all ratings for an entity with avg
func FranchiseRatings(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	entityID, ok := params["entityId"].(string)
	if !ok || entityID == "" {
		entityID, ok = params["franchiseId"].(string)
		if !ok || entityID == "" {
			return nil, 0, 0, ErrMissingParam
		}
	}

	page := 1
	limit := 10
	if p, ok := params["page"].(float64); ok && p > 0 {
		page = int(p)
	}
	if l, ok := params["limit"].(float64); ok && l > 0 && l <= 50 {
		limit = int(l)
	}
	offset := (page - 1) * limit

	var totalCount int
	var avgRating sql.NullFloat64
	_ = db.QueryRowContext(ctx, `
		SELECT COUNT(*), AVG(rating) FROM user_ratings WHERE entity_id = $1`,
		entityID,
	).Scan(&totalCount, &avgRating)

	rows, err := db.QueryContext(ctx, `
		SELECT id, user_id, rating, COALESCE(review, ''), created_at
		FROM user_ratings
		WHERE entity_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`,
		entityID, limit, offset,
	)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var ratings []map[string]interface{}
	for rows.Next() {
		var id, userID, review string
		var rating float64
		var createdAt time.Time
		if err := rows.Scan(&id, &userID, &rating, &review, &createdAt); err != nil {
			continue
		}
		ratings = append(ratings, map[string]interface{}{
			"id":        id,
			"userId":    userID,
			"rating":    rating,
			"review":    review,
			"createdAt": createdAt,
		})
	}

	if ratings == nil {
		ratings = []map[string]interface{}{}
	}

	avg := 0.0
	if avgRating.Valid {
		avg = avgRating.Float64
	}

	return map[string]interface{}{
		"ratings":    ratings,
		"totalCount": totalCount,
		"avgRating":  avg,
		"page":       page,
		"limit":      limit,
	}, totalCount, time.Since(start).Milliseconds(), nil
}

// ============================================================
// SHARE QUERIES
// ============================================================

// UserShareHistory — get share history for a user
func UserShareHistory(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	userID, ok := params["userId"].(string)
	if !ok || userID == "" {
		return nil, 0, 0, ErrMissingParam
	}

	page := 1
	limit := 20
	if p, ok := params["page"].(float64); ok && p > 0 {
		page = int(p)
	}
	if l, ok := params["limit"].(float64); ok && l > 0 && l <= 50 {
		limit = int(l)
	}
	offset := (page - 1) * limit

	var totalCount int
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM franchise_shares WHERE user_id = $1`, userID,
	).Scan(&totalCount)

	rows, err := db.QueryContext(ctx, `
		SELECT
			fs.id,
			fs.entity_id,
			fs.entity_type,
			COALESCE(f.name, a.name, '') as name,
			fs.share_platform,
			fs.shared_at
		FROM franchise_shares fs
		LEFT JOIN franchises f ON fs.entity_id = f.id AND fs.entity_type = 'franchise'
		LEFT JOIN associations a ON fs.entity_id = a.id AND fs.entity_type = 'association'
		WHERE fs.user_id = $1
		ORDER BY fs.shared_at DESC
		LIMIT $2 OFFSET $3`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var shares []map[string]interface{}
	for rows.Next() {
		var shareID, entityID, entityType, name, platform string
		var sharedAt time.Time
		if err := rows.Scan(&shareID, &entityID, &entityType, &name, &platform, &sharedAt); err != nil {
			continue
		}
		shares = append(shares, map[string]interface{}{
			"shareId":       shareID,
			"franchiseId":   entityID, // backwards compatibility
			"entityId":      entityID,
			"entityType":    entityType,
			"franchiseName": name, // backwards compatibility
			"entityName":    name,
			"sharePlatform": platform,
			"sharedAt":      sharedAt,
		})
	}

	if shares == nil {
		shares = []map[string]interface{}{}
	}

	return map[string]interface{}{
		"shares":     shares,
		"totalCount": totalCount,
		"page":       page,
		"limit":      limit,
	}, len(shares), time.Since(start).Milliseconds(), nil
}
