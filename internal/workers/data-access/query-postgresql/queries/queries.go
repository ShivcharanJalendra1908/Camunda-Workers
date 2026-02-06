// internal/workers/data-access/query-postgresql/queries/queries.go
package queries

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidParams = errors.New("invalid query parameters")
	ErrNotFound      = errors.New("record not found")
)

// ✅ Helper to extract franchiseId from multiple sources
func extractFranchiseID(params map[string]interface{}) (string, error) {
	// Priority 1: Direct franchiseId parameter
	if id, ok := params["franchiseId"].(string); ok && id != "" {
		return id, nil
	}

	// Priority 2: Extract from basicInfo object (Detail Page)
	if basicInfo, ok := params["basicInfo"].(map[string]interface{}); ok {
		// Try all possible field names in basicInfo
		if id, ok := basicInfo["id"].(string); ok && id != "" {
			return id, nil
		}
		if id, ok := basicInfo["franchise_id"].(string); ok && id != "" {
			return id, nil
		}
		if id, ok := basicInfo["franchiseId"].(string); ok && id != "" {
			return id, nil
		}
	}

	// Priority 3: Direct id parameter (ES data)
	if id, ok := params["id"].(string); ok && id != "" {
		return id, nil
	}

	// Priority 4: Extract from params map
	if paramsMap, ok := params["params"].(map[string]interface{}); ok {
		if basicInfo, ok := paramsMap["basicInfo"].(map[string]interface{}); ok {
			if id, ok := basicInfo["id"].(string); ok && id != "" {
				return id, nil
			}
		}
	}

	return "", ErrInvalidParams
}

// ============================================================
// QUERY IMPLEMENTATIONS
// ============================================================

// IndustriesTop9 - Get top 9 industries for home page
func IndustriesTop9(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	query := `
		SELECT id, name, slug, icon_url
		FROM industries
		WHERE is_active = true
		ORDER BY display_order
		LIMIT 9
	`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var industries []map[string]interface{}
	for rows.Next() {
		var id, name, slug string
		var iconURL sql.NullString

		if err := rows.Scan(&id, &name, &slug, &iconURL); err != nil {
			continue
		}

		industry := map[string]interface{}{
			"id":   id,
			"name": name,
			"slug": slug,
		}

		// ✅ CHANGED: Use icon_url instead of icon_name
		if iconURL.Valid {
			industry["icon_url"] = iconURL.String
		}

		industries = append(industries, industry)
	}

	return industries, len(industries), time.Since(start).Milliseconds(), nil
}

// CategoriesTop30 - Get top 30 categories for home page
func CategoriesTop30(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	query := `
		SELECT c.id, c.name, c.slug, c.icon_url, i.slug as industry_slug
		FROM categories c
		INNER JOIN industries i ON c.industry_id = i.id
		WHERE c.is_active = true
		ORDER BY c.display_order
		LIMIT 30
	`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var categories []map[string]interface{}
	for rows.Next() {
		var id, name, slug, industrySlug string
		var iconURL sql.NullString

		if err := rows.Scan(&id, &name, &slug, &iconURL, &industrySlug); err != nil {
			continue
		}

		category := map[string]interface{}{
			"id":            id,
			"name":          name,
			"slug":          slug,
			"industry_slug": industrySlug,
		}

		// ✅ CHANGED: Use icon_url instead of icon_name
		if iconURL.Valid {
			category["icon_url"] = iconURL.String
		}

		categories = append(categories, category)
	}

	return categories, len(categories), time.Since(start).Milliseconds(), nil
}

// CategoriesFeatured8 - Get 8 featured categories for listing page
func CategoriesFeatured8(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	query := `
		SELECT c.id, c.name, c.slug, c.icon_url
		FROM categories c
		WHERE c.is_active = true
		ORDER BY c.display_order
		LIMIT 8
	`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var categories []map[string]interface{}
	for rows.Next() {
		var id, name, slug string
		var iconURL sql.NullString

		if err := rows.Scan(&id, &name, &slug, &iconURL); err != nil {
			continue
		}

		category := map[string]interface{}{
			"id":   id,
			"name": name,
			"slug": slug,
		}

		// ✅ CHANGED: Use icon_url instead of icon_name
		if iconURL.Valid {
			category["icon_url"] = iconURL.String
		}

		categories = append(categories, category)
	}

	return categories, len(categories), time.Since(start).Milliseconds(), nil
}

// IndustryBySlug - Get industry info by slug
func IndustryBySlug(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	slug, ok := params["slug"].(string)
	if !ok {
		return nil, 0, 0, ErrInvalidParams
	}

	// ✅ FIXED: Try exact match first, then partial match
	query := `
		SELECT id, name, slug, listing_description
		FROM industries
		WHERE (slug = $1 OR slug LIKE $1 || '%' OR $1 LIKE slug || '%')
		  AND is_active = true
		ORDER BY 
		  CASE WHEN slug = $1 THEN 1 ELSE 2 END,
		  LENGTH(slug)
		LIMIT 1
	`

	var id, name, industrySlug string
	var description sql.NullString

	err := db.QueryRowContext(ctx, query, slug).Scan(&id, &name, &industrySlug, &description)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, 0, ErrNotFound
		}
		return nil, 0, 0, err
	}

	industry := map[string]interface{}{
		"id":   id,
		"name": name,
		"slug": industrySlug,
	}

	if description.Valid {
		industry["description"] = description.String
	}

	return industry, 1, time.Since(start).Milliseconds(), nil
}

// FranchiseOverview - Get franchising overview for detail page
func FranchiseOverview(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	// franchiseID, ok := params["franchiseId"].(string)
	// if !ok {
	// 	return nil, 0, 0, ErrInvalidParams
	// }
	franchiseID, err := extractFranchiseID(params)
	if err != nil {
		return nil, 0, 0, err
	}

	query := `
SELECT 
	f.contact_email,
	f.parent_company,
	f.business_type,
	f.leader_name,
	f.leader_role,
	fc.city,
	fir.franchise_fee,
	fir.royalty_percentage,
	fir.monthly_turnover_min,
	fir.monthly_turnover_max,
	fo.space_min_sqft,
	fo.space_max_sqft
FROM franchises f
LEFT JOIN franchise_investment_requirement fir ON f.id = fir.franchise_id
LEFT JOIN franchise_operations fo ON f.id = fo.franchise_id
LEFT JOIN LATERAL (
	SELECT city
	FROM franchise_cities
	WHERE franchise_id = f.id
	ORDER BY created_at DESC
	LIMIT 1
) fc ON true
WHERE f.id = $1
	`

	var (
		email, parentCompany, businessType sql.NullString
		leaderName, leaderRole, city       sql.NullString
		franchiseFee, royaltyPercent       sql.NullFloat64
		turnoverMin, turnoverMax           sql.NullFloat64
		spaceMin, spaceMax                 sql.NullInt32
	)

	err = db.QueryRowContext(ctx, query, franchiseID).Scan(
		&email,
		&parentCompany,
		&businessType,
		&leaderName,
		&leaderRole,
		&city,
		&franchiseFee,
		&royaltyPercent,
		&turnoverMin,
		&turnoverMax,
		&spaceMin,
		&spaceMax,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, 0, ErrNotFound
		}
		return nil, 0, 0, err
	}

	overview := map[string]interface{}{}

	if email.Valid {
		overview["email"] = email.String
	}
	if parentCompany.Valid {
		overview["parent_company"] = parentCompany.String
	}
	if businessType.Valid {
		overview["business_type"] = businessType.String
	}
	if leaderName.Valid {
		overview["leader_name"] = leaderName.String
	}
	if leaderRole.Valid {
		overview["leader_role"] = leaderRole.String
	}
	if city.Valid {
		overview["city"] = city.String
	}
	if franchiseFee.Valid {
		overview["franchise_fee"] = franchiseFee.Float64
	}
	if royaltyPercent.Valid {
		overview["royalty_percentage"] = royaltyPercent.Float64
	}
	if turnoverMin.Valid {
		overview["monthly_turnover_min"] = turnoverMin.Float64
	}
	if turnoverMax.Valid {
		overview["monthly_turnover_max"] = turnoverMax.Float64
	}
	if spaceMin.Valid {
		overview["space_min_sqft"] = spaceMin.Int32
	}
	if spaceMax.Valid {
		overview["space_max_sqft"] = spaceMax.Int32
	}

	return overview, 1, time.Since(start).Milliseconds(), nil
}

// FranchiseBusiness - Get business overview (products/services)
func FranchiseBusiness(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	// franchiseID, ok := params["franchiseId"].(string)
	// if !ok {
	// 	return nil, 0, 0, ErrInvalidParams
	// }
	franchiseID, err := extractFranchiseID(params)
	if err != nil {
		return nil, 0, 0, err
	}

	query := `
		SELECT products, services
		FROM franchise_business_overview
		WHERE franchise_id = $1
	`

	var productsJSON, servicesJSON []byte

	err = db.QueryRowContext(ctx, query, franchiseID).Scan(&productsJSON, &servicesJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return map[string]interface{}{
				"products": []string{},
				"services": []string{},
			}, 0, time.Since(start).Milliseconds(), nil
		}
		return nil, 0, 0, err
	}

	var products, services []string
	json.Unmarshal(productsJSON, &products)
	json.Unmarshal(servicesJSON, &services)

	return map[string]interface{}{
		"products": products,
		"services": services,
	}, 1, time.Since(start).Milliseconds(), nil
}

// FranchiseInvestment - Get investment details
func FranchiseInvestment(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	// franchiseID, ok := params["franchiseId"].(string)
	// if !ok {
	// 	return nil, 0, 0, ErrInvalidParams
	// }

	franchiseID, err := extractFranchiseID(params)
	if err != nil {
		return nil, 0, 0, err
	}

	query := `
		SELECT
			initial_investment_min,
			initial_investment_max,
			franchise_fee,
			royalty_percentage,
			marketing_fee_percentage,
			monthly_turnover_min,
			monthly_turnover_max,
			single_unit_cost_min,
			single_unit_cost_max
		FROM franchise_investment_requirement
		WHERE franchise_id = $1
	`

	var (
		minInv, maxInv, franchiseFee sql.NullFloat64
		royaltyPct, marketingPct     sql.NullFloat64
		turnoverMin, turnoverMax     sql.NullFloat64
		unitCostMin, unitCostMax     sql.NullFloat64
	)

	err = db.QueryRowContext(ctx, query, franchiseID).Scan(
		&minInv,
		&maxInv,
		&franchiseFee,
		&royaltyPct,
		&marketingPct,
		&turnoverMin,
		&turnoverMax,
		&unitCostMin,
		&unitCostMax,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return map[string]interface{}{}, 0, time.Since(start).Milliseconds(), nil
		}
		return nil, 0, 0, err
	}

	investment := map[string]interface{}{}

	if minInv.Valid {
		investment["initial_investment_min"] = minInv.Float64
	}
	if maxInv.Valid {
		investment["initial_investment_max"] = maxInv.Float64
	}
	if franchiseFee.Valid {
		investment["franchise_fee"] = franchiseFee.Float64
	}
	if royaltyPct.Valid {
		investment["royalty_percentage"] = royaltyPct.Float64
	}
	if marketingPct.Valid {
		investment["marketing_fee_percentage"] = marketingPct.Float64
	}
	if turnoverMin.Valid {
		investment["monthly_turnover_min"] = turnoverMin.Float64
	}
	if turnoverMax.Valid {
		investment["monthly_turnover_max"] = turnoverMax.Float64
	}
	if unitCostMin.Valid {
		investment["single_unit_cost_min"] = unitCostMin.Float64
	}
	if unitCostMax.Valid {
		investment["single_unit_cost_max"] = unitCostMax.Float64
	}

	return investment, 1, time.Since(start).Milliseconds(), nil
}

// FranchiseOperations - Get operations details
func FranchiseOperations(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	// franchiseID, ok := params["franchiseId"].(string)
	// if !ok {
	// 	return nil, 0, 0, ErrInvalidParams
	// }

	franchiseID, err := extractFranchiseID(params)
	if err != nil {
		return nil, 0, 0, err
	}

	query := `
		SELECT 
			space_min_sqft,
			space_max_sqft,
			staff_required_min,
			staff_required_max,
			training_provided,
			training_details,
			marketing_support
		FROM franchise_operations
		WHERE franchise_id = $1
	`

	var spaceMin, spaceMax, staffMin, staffMax sql.NullInt32
	var trainingProvided sql.NullBool
	var trainingDetails, marketingSupport sql.NullString

	err = db.QueryRowContext(ctx, query, franchiseID).Scan(
		&spaceMin, &spaceMax, &staffMin, &staffMax,
		&trainingProvided, &trainingDetails, &marketingSupport,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return map[string]interface{}{}, 0, time.Since(start).Milliseconds(), nil
		}
		return nil, 0, 0, err
	}

	operations := map[string]interface{}{}

	if spaceMin.Valid {
		operations["space_min_sqft"] = spaceMin.Int32
	}
	if spaceMax.Valid {
		operations["space_max_sqft"] = spaceMax.Int32
	}
	if staffMin.Valid {
		operations["staff_required_min"] = staffMin.Int32
	}
	if staffMax.Valid {
		operations["staff_required_max"] = staffMax.Int32
	}
	if trainingProvided.Valid {
		operations["training_provided"] = trainingProvided.Bool
	}
	if trainingDetails.Valid {
		operations["training_details"] = trainingDetails.String
	}
	if marketingSupport.Valid {
		operations["marketing_support"] = marketingSupport.String
	}

	return operations, 1, time.Since(start).Milliseconds(), nil
}

// FranchiseSocial - Get social links
func FranchiseSocial(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	// franchiseID, ok := params["franchiseId"].(string)
	// if !ok {
	// 	return nil, 0, 0, ErrInvalidParams
	// }

	franchiseID, err := extractFranchiseID(params)
	if err != nil {
		return nil, 0, 0, err
	}

	query := `
		SELECT 
			instagram_url,
			facebook_url,
			twitter_url,
			linkedin_url
		FROM franchise_social_links
		WHERE franchise_id = $1
	`

	var instagram, facebook, twitter, linkedin sql.NullString

	err = db.QueryRowContext(ctx, query, franchiseID).Scan(&instagram, &facebook, &twitter, &linkedin)

	if err != nil {
		if err == sql.ErrNoRows {
			return []map[string]interface{}{}, 0, time.Since(start).Milliseconds(), nil
		}
		return nil, 0, 0, err
	}

	var socialLinks []map[string]interface{}

	if instagram.Valid && instagram.String != "" {
		socialLinks = append(socialLinks, map[string]interface{}{
			"platform": "instagram",
			"url":      instagram.String,
		})
	}
	if facebook.Valid && facebook.String != "" {
		socialLinks = append(socialLinks, map[string]interface{}{
			"platform": "facebook",
			"url":      facebook.String,
		})
	}
	if twitter.Valid && twitter.String != "" {
		socialLinks = append(socialLinks, map[string]interface{}{
			"platform": "twitter",
			"url":      twitter.String,
		})
	}
	if linkedin.Valid && linkedin.String != "" {
		socialLinks = append(socialLinks, map[string]interface{}{
			"platform": "linkedin",
			"url":      linkedin.String,
		})
	}

	return socialLinks, len(socialLinks), time.Since(start).Milliseconds(), nil
}

// IndustryBySlugWithQuestions - Get industry info with category questions
func IndustryBySlugWithQuestions(
	ctx context.Context,
	db *sql.DB,
	params map[string]interface{},
) (interface{}, int, int64, error) {

	start := time.Now()

	slug, ok := params["slug"].(string)
	if !ok {
		return nil, 0, 0, ErrInvalidParams
	}

	var industryID, name string
	var description sql.NullString

	err := db.QueryRowContext(ctx, `
		SELECT id, name, listing_description
		FROM industries
		WHERE slug = $1 AND is_active = true
	`, slug).Scan(&industryID, &name, &description)
	if err != nil {
		return nil, 0, 0, err
	}

	rows, _ := db.QueryContext(ctx, `
		SELECT question
		FROM category_questions
		WHERE reference_id = $1
		ORDER BY created_at
		LIMIT 8
	`, industryID)
	defer rows.Close()

	var questions []string
	for rows.Next() {
		var q string
		if rows.Scan(&q) == nil {
			questions = append(questions, q)
		}
	}

	return map[string]interface{}{
		"industry": map[string]interface{}{
			"id":          industryID,
			"name":        name,
			"slug":        slug,
			"description": description.String,
		},
		"questions": questions,
	}, 1, time.Since(start).Milliseconds(), nil
}

// CategoryQuestionsByIndustry - Get 8 questions for an industry
func CategoryQuestionsByIndustry(
	ctx context.Context,
	db *sql.DB,
	params map[string]interface{},
) (interface{}, int, int64, error) {
	start := time.Now()

	var referenceID string

	// ✅ IMPROVED: Extract reference ID from multiple sources
	if v, ok := params["industryId"].(string); ok && v != "" {
		referenceID = v
	} else if v, ok := params["industrySlug"].(string); ok && v != "" {
		// Lookup industry ID by slug
		err := db.QueryRowContext(ctx,
			"SELECT id FROM industries WHERE slug = $1 AND is_active = true", v,
		).Scan(&referenceID)
		if err != nil {
			if err == sql.ErrNoRows {
				// Industry not found - return empty array
				return []string{}, 0, time.Since(start).Milliseconds(), nil
			}
			return nil, 0, 0, fmt.Errorf("industry lookup failed: %w", err)
		}
	} else if v, ok := params["categoryId"].(string); ok && v != "" {
		referenceID = v
	} else if v, ok := params["categorySlug"].(string); ok && v != "" {
		// Lookup category ID by slug
		err := db.QueryRowContext(ctx,
			"SELECT id FROM categories WHERE slug = $1 AND is_active = true", v,
		).Scan(&referenceID)
		if err != nil {
			if err == sql.ErrNoRows {
				return []string{}, 0, time.Since(start).Milliseconds(), nil
			}
			return nil, 0, 0, fmt.Errorf("category lookup failed: %w", err)
		}
	} else {
		return nil, 0, 0, ErrInvalidParams
	}

	// ✅ Query with proper timeout
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := db.QueryContext(queryCtx, `
        SELECT question
        FROM category_questions
        WHERE reference_id = $1
        ORDER BY created_at
        LIMIT 8
    `, referenceID)

	if err != nil {
		// Return empty array on error (graceful degradation)
		return []string{}, 0, time.Since(start).Milliseconds(), nil
	}
	defer rows.Close()

	var questions []string
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err == nil && q != "" {
			questions = append(questions, q)
		}
	}

	return questions, len(questions), time.Since(start).Milliseconds(), nil
}

// FeaturedCategoriesByIndustry - Get featured categories for an industry (detail page)
func FeaturedCategoriesByIndustry(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	industryID, ok := params["industryId"].(string)
	if !ok {
		return nil, 0, 0, ErrInvalidParams
	}

	query := `
		SELECT c.id, c.name, c.slug
		FROM categories c
		WHERE c.industry_id = $1 AND c.is_active = true
		ORDER BY c.display_order
		LIMIT 8
	`

	rows, err := db.QueryContext(ctx, query, industryID)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var categories []map[string]interface{}
	for rows.Next() {
		var id, name, slug string
		if err := rows.Scan(&id, &name, &slug); err == nil {
			categories = append(categories, map[string]interface{}{
				"id":   id,
				"name": name,
				"slug": slug,
			})
		}
	}

	return categories, len(categories), time.Since(start).Milliseconds(), nil
}
