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
// ✅ Helper to extract franchiseId from multiple sources
func extractFranchiseID(params map[string]interface{}) (string, error) {
	// Priority 1: Direct franchiseId parameter
	if id, ok := params["franchiseId"].(string); ok && id != "" {
		return id, nil
	}

	// Priority 2: Extract from basicInfo.id (BPMN passes this)
	if basicInfo, ok := params["basicInfo"].(map[string]interface{}); ok {
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

	// Priority 3: Direct id parameter
	if id, ok := params["id"].(string); ok && id != "" {
		return id, nil
	}

	// Priority 4: Extract from data array (ES response format)
	if data, ok := params["data"].([]interface{}); ok && len(data) > 0 {
		if firstItem, ok := data[0].(map[string]interface{}); ok {
			if id, ok := firstItem["id"].(string); ok && id != "" {
				return id, nil
			}
			if id, ok := firstItem["franchise_id"].(string); ok && id != "" {
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

	// query := `
	// 	SELECT id, name, slug, icon_url
	// 	FROM industries
	// 	WHERE is_active = true
	// 	ORDER BY display_order
	// 	LIMIT 9
	// `
	query := `
    SELECT i.id, i.name, i.slug, i.icon_url
    FROM industries i
    INNER JOIN (
        SELECT industry_id, COUNT(*) as franchise_count
        FROM franchises
        WHERE is_active = true
        GROUP BY industry_id
    ) f ON f.industry_id = i.id
    WHERE i.is_active = true
    ORDER BY f.franchise_count DESC
    LIMIT 10
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

	industryID, hasIndustry := params["industryId"].(string)
	industrySlug, hasSlug := params["industrySlug"].(string)

	var rows *sql.Rows
	var err error

	if hasIndustry && industryID != "" {
		rows, err = db.QueryContext(ctx, `
			SELECT c.id, c.name, c.slug, c.icon_url
			FROM categories c
			WHERE c.industry_id = $1 AND c.is_active = true
			ORDER BY c.display_order
			LIMIT 8
		`, industryID)
	} else if hasSlug && industrySlug != "" {
		rows, err = db.QueryContext(ctx, `
			SELECT c.id, c.name, c.slug, c.icon_url
			FROM categories c
			INNER JOIN industries i ON c.industry_id = i.id
			WHERE i.slug = $1 AND c.is_active = true
			ORDER BY c.display_order
			LIMIT 8
		`, industrySlug)
	} else {
		rows, err = db.QueryContext(ctx, `
			SELECT c.id, c.name, c.slug, c.icon_url
			FROM categories c
			WHERE c.is_active = true
			ORDER BY c.display_order
			LIMIT 8
		`)
	}
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
	// query := `
	// 	SELECT id, name, slug, listing_description
	// 	FROM industries
	// 	WHERE (slug = $1 OR slug LIKE $1 || '%' OR $1 LIKE slug || '%')
	// 	  AND is_active = true
	// 	ORDER BY
	// 	  CASE WHEN slug = $1 THEN 1 ELSE 2 END,
	// 	  LENGTH(slug)
	// 	LIMIT 1
	// `
	query := `
    SELECT id, name, slug, listing_description
    FROM industries
    WHERE (
        slug = $1                          -- exact: "food-beverage"
        OR slug LIKE $1 || '%'             -- prefix: "food" → "food-beverage"
        OR slug LIKE '%' || $1 || '%'      -- contains: "travel" → "hotel-travel-tourism"
        OR $1 LIKE '%' || slug || '%'      -- reverse: slug inside input
        OR name ILIKE '%' || $1 || '%'     -- name fuzzy: "hotels" → "Hotel, Travel & Tourism"
    )
    AND is_active = true
    ORDER BY 
        CASE WHEN slug = $1 THEN 1
             WHEN slug LIKE $1 || '%' THEN 2
             WHEN slug LIKE '%' || $1 || '%' THEN 3
             ELSE 4
        END,
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
	f.established_year,
	f.units_count,
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
		establishedYear                    sql.NullInt32
		unitsCount                         sql.NullInt32
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
		&establishedYear,
		&unitsCount,
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
	if establishedYear.Valid {
		overview["established_year"] = establishedYear.Int32
	}
	if unitsCount.Valid {
		overview["units_count"] = unitsCount.Int32
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

// func FranchiseOverview(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
// 	start := time.Now()

// 	franchiseID, err := extractFranchiseID(params)
// 	if err != nil {
// 		return nil, 0, 0, err
// 	}

// 	query := `
// SELECT
// 	f.contact_email,
// 	f.parent_company,
// 	f.business_type,
// 	f.leader_name,
// 	f.leader_role,
// 	fc.city,
// 	fir.franchise_fee,
// 	fir.royalty_percentage,
// 	fir.monthly_turnover_min,
// 	fir.monthly_turnover_max,
// 	fo.space_min_sqft,
// 	fo.space_max_sqft
// FROM franchises f
// LEFT JOIN franchise_investment_requirement fir ON f.id = fir.franchise_id
// LEFT JOIN franchise_operations fo ON f.id = fo.franchise_id
// LEFT JOIN LATERAL (
// 	SELECT city
// 	FROM franchise_cities
// 	WHERE franchise_id = f.id
// 	ORDER BY created_at DESC
// 	LIMIT 1
// ) fc ON true
// WHERE f.id = $1
// 	`

// 	var (
// 		email, parentCompany, businessType sql.NullString
// 		leaderName, leaderRole, city       sql.NullString
// 		franchiseFee, royaltyPercent       sql.NullFloat64
// 		turnoverMin, turnoverMax           sql.NullFloat64
// 		spaceMin, spaceMax                 sql.NullInt32
// 	)

// 	err = db.QueryRowContext(ctx, query, franchiseID).Scan(
// 		&email,
// 		&parentCompany,
// 		&businessType,
// 		&leaderName,
// 		&leaderRole,
// 		&city,
// 		&franchiseFee,
// 		&royaltyPercent,
// 		&turnoverMin,
// 		&turnoverMax,
// 		&spaceMin,
// 		&spaceMax,
// 	)

// 	if err != nil {
// 		if err == sql.ErrNoRows {
// 			return nil, 0, 0, ErrNotFound
// 		}
// 		return nil, 0, 0, err
// 	}

// 	overview := map[string]interface{}{}

// 	if email.Valid {
// 		overview["email"] = email.String
// 	}
// 	if parentCompany.Valid {
// 		overview["parent_company"] = parentCompany.String
// 	}
// 	if businessType.Valid {
// 		overview["business_type"] = businessType.String
// 	}
// 	if leaderName.Valid {
// 		overview["leader_name"] = leaderName.String
// 	}
// 	if leaderRole.Valid {
// 		overview["leader_role"] = leaderRole.String
// 	}
// 	if city.Valid {
// 		overview["city"] = city.String
// 	}
// 	if franchiseFee.Valid {
// 		overview["franchise_fee"] = franchiseFee.Float64
// 	}
// 	if royaltyPercent.Valid {
// 		overview["royalty_percentage"] = royaltyPercent.Float64
// 	}
// 	if turnoverMin.Valid {
// 		overview["monthly_turnover_min"] = turnoverMin.Float64
// 	}
// 	if turnoverMax.Valid {
// 		overview["monthly_turnover_max"] = turnoverMax.Float64
// 	}
// 	if spaceMin.Valid {
// 		overview["space_min_sqft"] = spaceMin.Int32
// 	}
// 	if spaceMax.Valid {
// 		overview["space_max_sqft"] = spaceMax.Int32
// 	}

// 	return overview, 1, time.Since(start).Milliseconds(), nil
// }

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
			payback_min_months,
			payback_max_months,
			roi_min_percentage,
			roi_max_percentage,
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
		paybackMin, paybackMax       sql.NullInt32
		roiMin, roiMax               sql.NullFloat64
		turnoverMin, turnoverMax     sql.NullFloat64
		unitCostMin, unitCostMax     sql.NullFloat64
	)

	err = db.QueryRowContext(ctx, query, franchiseID).Scan(
		&minInv,
		&maxInv,
		&franchiseFee,
		&royaltyPct,
		&marketingPct,
		&paybackMin,
		&paybackMax,
		&roiMin,
		&roiMax,
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
	if paybackMin.Valid {
		investment["payback_min_months"] = paybackMin.Int32
	}
	if paybackMax.Valid {
		investment["payback_max_months"] = paybackMax.Int32
	}
	if roiMin.Valid {
		investment["roi_min_percentage"] = roiMin.Float64
	}
	if roiMax.Valid {
		investment["roi_max_percentage"] = roiMax.Float64
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

// func FranchiseInvestment(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
// 	start := time.Now()

// 	franchiseID, err := extractFranchiseID(params)
// 	if err != nil {
// 		return nil, 0, 0, err
// 	}

// 	query := `
// 		SELECT
// 			initial_investment_min,
// 			initial_investment_max,
// 			franchise_fee,
// 			royalty_percentage,
// 			marketing_fee_percentage,
// 			monthly_turnover_min,
// 			monthly_turnover_max,
// 			single_unit_cost_min,
// 			single_unit_cost_max
// 		FROM franchise_investment_requirement
// 		WHERE franchise_id = $1
// 	`

// 	var (
// 		minInv, maxInv, franchiseFee sql.NullFloat64
// 		royaltyPct, marketingPct     sql.NullFloat64
// 		turnoverMin, turnoverMax     sql.NullFloat64
// 		unitCostMin, unitCostMax     sql.NullFloat64
// 	)

// 	err = db.QueryRowContext(ctx, query, franchiseID).Scan(
// 		&minInv,
// 		&maxInv,
// 		&franchiseFee,
// 		&royaltyPct,
// 		&marketingPct,
// 		&turnoverMin,
// 		&turnoverMax,
// 		&unitCostMin,
// 		&unitCostMax,
// 	)

// 	if err != nil {
// 		if err == sql.ErrNoRows {
// 			return map[string]interface{}{}, 0, time.Since(start).Milliseconds(), nil
// 		}
// 		return nil, 0, 0, err
// 	}

// 	investment := map[string]interface{}{}

// 	if minInv.Valid {
// 		investment["initial_investment_min"] = minInv.Float64
// 	}
// 	if maxInv.Valid {
// 		investment["initial_investment_max"] = maxInv.Float64
// 	}
// 	if franchiseFee.Valid {
// 		investment["franchise_fee"] = franchiseFee.Float64
// 	}
// 	if royaltyPct.Valid {
// 		investment["royalty_percentage"] = royaltyPct.Float64
// 	}
// 	if marketingPct.Valid {
// 		investment["marketing_fee_percentage"] = marketingPct.Float64
// 	}
// 	if turnoverMin.Valid {
// 		investment["monthly_turnover_min"] = turnoverMin.Float64
// 	}
// 	if turnoverMax.Valid {
// 		investment["monthly_turnover_max"] = turnoverMax.Float64
// 	}
// 	if unitCostMin.Valid {
// 		investment["single_unit_cost_min"] = unitCostMin.Float64
// 	}
// 	if unitCostMax.Valid {
// 		investment["single_unit_cost_max"] = unitCostMax.Float64
// 	}

// 	return investment, 1, time.Since(start).Milliseconds(), nil
// }

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
		// err := db.QueryRowContext(ctx,
		// 	"SELECT id FROM industries WHERE slug = $1 AND is_active = true", v,
		// ).Scan(&referenceID)
		err := db.QueryRowContext(ctx, `
    SELECT id FROM industries 
    WHERE (
        slug = $1 
        OR slug LIKE $1 || '%'
        OR slug LIKE '%' || $1 || '%'
        OR name ILIKE '%' || $1 || '%'
    )
    AND is_active = true
    ORDER BY
        CASE WHEN slug = $1 THEN 1
             WHEN slug LIKE $1 || '%' THEN 2
             ELSE 3
        END
    LIMIT 1`, v,
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

// func AllIndustries(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
// 	start := time.Now()
// 	activeOnly := true
// 	if v, ok := params["activeOnly"].(bool); ok {
// 		activeOnly = v
// 	}

// 	query := `SELECT id, name, slug, icon_url, color_hex, listing_description, display_order
// 	          FROM industries`
// 	if activeOnly {
// 		query += ` WHERE is_active = true`
// 	}
// 	query += ` ORDER BY display_order`

// 	rows, err := db.QueryContext(ctx, query)
// 	if err != nil {
// 		return nil, 0, 0, err
// 	}
// 	defer rows.Close()

// 	var industries []map[string]interface{}
// 	for rows.Next() {
// 		var id, name, slug, colorHex string
// 		var iconURL, description sql.NullString
// 		var displayOrder int

// 		if err := rows.Scan(&id, &name, &slug, &iconURL, &colorHex, &description, &displayOrder); err != nil {
// 			continue
// 		}

// 		ind := map[string]interface{}{
// 			"id":           id,
// 			"name":         name,
// 			"slug":         slug,
// 			"color_hex":    colorHex,
// 			"displayOrder": displayOrder,
// 		}
// 		if iconURL.Valid {
// 			ind["icon_url"] = iconURL.String
// 		}
// 		if description.Valid {
// 			ind["description"] = description.String
// 		}
// 		industries = append(industries, ind)
// 	}

// 	return industries, len(industries), time.Since(start).Milliseconds(), nil
// }

// // CategoriesByIndustry - Ek industry ke sab categories (BPMN 1 ke liye)
// func CategoriesByIndustry(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
// 	start := time.Now()

// 	industryID, ok := params["industryId"].(string)
// 	if !ok || industryID == "" {
// 		return nil, 0, 0, ErrMissingParam
// 	}

// 	rows, err := db.QueryContext(ctx, `
// 		SELECT id, name, slug, icon_url, description, display_order
// 		FROM categories
// 		WHERE industry_id = $1 AND is_active = true
// 		ORDER BY display_order`, industryID)
// 	if err != nil {
// 		return nil, 0, 0, err
// 	}
// 	defer rows.Close()

// 	var cats []map[string]interface{}
// 	for rows.Next() {
// 		var id, name, slug string
// 		var iconURL, desc sql.NullString
// 		var order int

// 		if err := rows.Scan(&id, &name, &slug, &iconURL, &desc, &order); err != nil {
// 			continue
// 		}

// 		cat := map[string]interface{}{
// 			"id":           id,
// 			"name":         name,
// 			"slug":         slug,
// 			"displayOrder": order,
// 		}
// 		if iconURL.Valid {
// 			cat["icon_url"] = iconURL.String
// 		}
// 		if desc.Valid {
// 			cat["description"] = desc.String
// 		}
// 		cats = append(cats, cat)
// 	}

// 	return cats, len(cats), time.Since(start).Milliseconds(), nil
// }

// // SubCategoriesByCategory - Ek category ke sab sub-categories (BPMN 1 ke liye)
// func SubCategoriesByCategory(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
// 	start := time.Now()

// 	categoryID, ok := params["categoryId"].(string)
// 	if !ok || categoryID == "" {
// 		return nil, 0, 0, ErrMissingParam
// 	}

// 	rows, err := db.QueryContext(ctx, `
// 		SELECT id, name, slug, description, display_order
// 		FROM sub_categories
// 		WHERE category_id = $1 AND is_active = true
// 		ORDER BY display_order`, categoryID)
// 	if err != nil {
// 		return nil, 0, 0, err
// 	}
// 	defer rows.Close()

// 	var subs []map[string]interface{}
// 	for rows.Next() {
// 		var id, name, slug string
// 		var desc sql.NullString
// 		var order int

// 		if err := rows.Scan(&id, &name, &slug, &desc, &order); err != nil {
// 			continue
// 		}

// 		sub := map[string]interface{}{
// 			"id":           id,
// 			"name":         name,
// 			"slug":         slug,
// 			"displayOrder": order,
// 		}
// 		if desc.Valid {
// 			sub["description"] = desc.String
// 		}
// 		subs = append(subs, sub)
// 	}

// 	return subs, len(subs), time.Since(start).Milliseconds(), nil
// }

func FranchiseContactInfo(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error) {
	start := time.Now()

	franchiseID, ok := params["franchiseId"].(string)
	if !ok || franchiseID == "" {
		return nil, 0, 0, ErrMissingParam
	}

	var name string
	var contactEmail sql.NullString

	err := db.QueryRowContext(ctx,
		`SELECT name, contact_email FROM franchises WHERE id = $1`,
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
	}
	if contactEmail.Valid && contactEmail.String != "" {
		result["franchiseContactEmail"] = contactEmail.String
	} else {
		// Fallback - agar contact_email NULL hai toh internal team ko bhejo
		result["franchiseContactEmail"] = ""
		result["useInternalFallback"] = true
	}

	return result, 1, time.Since(start).Milliseconds(), nil
}
