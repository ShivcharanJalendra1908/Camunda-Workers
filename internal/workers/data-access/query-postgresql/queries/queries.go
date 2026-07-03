// internal/workers/data-access/query-postgresql/queries/queries.go
package queries

import (
	"camunda-workers/internal/crypto"

	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
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
func IndustriesTop9(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	entityType, ok := params["entityType"].(string)
	if !ok || entityType == "" {
		entityType = "franchise"
	}

	query := `
    SELECT i.id, i.name, i.slug, i.icon_url
    FROM industries i
    INNER JOIN (
        SELECT c.industry_id, COUNT(*) as franchise_count
        FROM listing_categories lc
        INNER JOIN categories c ON lc.category_id = c.id
        INNER JOIN listings l ON lc.listing_id = l.id
        WHERE l.entity_type = $1 AND l.status = 'live'
        GROUP BY c.industry_id
    ) f ON f.industry_id = i.id
    WHERE i.is_active = true
    ORDER BY f.franchise_count DESC
    LIMIT 10
    `

	rows, err := db.QueryContext(ctx, query, entityType)
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
func CategoriesTop30(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	entityType, ok := params["entityType"].(string)
	if !ok || entityType == "" {
		entityType = "franchise"
	}

	query := `
		SELECT DISTINCT c.id, c.name, c.slug, c.icon_url, i.slug as industry_slug, c.display_order
		FROM categories c
		INNER JOIN industries i ON c.industry_id = i.id
		INNER JOIN listing_categories lc ON lc.category_id = c.id
		INNER JOIN listings l ON lc.listing_id = l.id
		WHERE c.is_active = true AND l.entity_type = $1 AND l.status = 'live'
		ORDER BY c.display_order
		LIMIT 30
	`

	rows, err := db.QueryContext(ctx, query, entityType)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var categories []map[string]interface{}
	for rows.Next() {
		var id, name, slug, industrySlug string
		var iconURL sql.NullString
		var displayOrder int

		if err := rows.Scan(&id, &name, &slug, &iconURL, &industrySlug, &displayOrder); err != nil {
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
func CategoriesFeatured8(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	entityType, ok := params["entityType"].(string)
	if !ok || entityType == "" {
		entityType = "franchise"
	}

	industryID, hasIndustry := params["industryId"].(string)
	industrySlug, hasSlug := params["industrySlug"].(string)

	var rows *sql.Rows
	var err error

	if hasIndustry && industryID != "" {
		rows, err = db.QueryContext(ctx, `
			SELECT c.id, c.name, c.slug, c.icon_url, c.image_url,
			       COUNT(DISTINCT l.id) as franchise_count
			FROM categories c
			LEFT JOIN listing_categories lc ON lc.category_id = c.id
			LEFT JOIN listings l ON lc.listing_id = l.id AND l.entity_type = $2 AND l.status = 'live'
			WHERE c.industry_id = $1 AND c.is_active = true
			GROUP BY c.id, c.name, c.slug, c.icon_url, c.image_url
			ORDER BY franchise_count DESC, c.display_order ASC
			LIMIT 8
		`, industryID, entityType)
	} else if hasSlug && industrySlug != "" {
		rows, err = db.QueryContext(ctx, `
			SELECT c.id, c.name, c.slug, c.icon_url, c.image_url,
			       COUNT(DISTINCT l.id) as franchise_count
			FROM categories c
			INNER JOIN industries i ON c.industry_id = i.id
			LEFT JOIN listing_categories lc ON lc.category_id = c.id
			LEFT JOIN listings l ON lc.listing_id = l.id AND l.entity_type = $2 AND l.status = 'live'
			WHERE i.slug = ANY(string_to_array($1, ',')) AND c.is_active = true
			GROUP BY c.id, c.name, c.slug, c.icon_url, c.image_url
			ORDER BY franchise_count DESC, c.display_order ASC
			LIMIT 8
		`, industrySlug, entityType)
	} else {
		rows, err = db.QueryContext(ctx, `
			SELECT c.id, c.name, c.slug, c.icon_url, c.image_url,
			       COUNT(DISTINCT l.id) as franchise_count
			FROM categories c
			LEFT JOIN listing_categories lc ON lc.category_id = c.id
			LEFT JOIN listings l ON lc.listing_id = l.id AND l.entity_type = $1 AND l.status = 'live'
			WHERE c.is_active = true
			GROUP BY c.id, c.name, c.slug, c.icon_url, c.image_url
			ORDER BY franchise_count DESC, c.display_order ASC
			LIMIT 8
		`, entityType)
	}

	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var categories []map[string]interface{}
	for rows.Next() {
		var id, name, slug string
		var iconURL, imageURL sql.NullString
		var franchiseCount int

		if err := rows.Scan(&id, &name, &slug, &iconURL, &imageURL, &franchiseCount); err != nil {
			continue
		}

		category := map[string]interface{}{
			"id":              id,
			"name":            name,
			"slug":            slug,
			"franchise_count": franchiseCount, // Count nhi chaheye toh comment this line only
		}

		if iconURL.Valid {
			category["icon_url"] = iconURL.String
		}

		if imageURL.Valid {
			category["image_url"] = imageURL.String
		}

		categories = append(categories, category)
	}

	return categories, len(categories), time.Since(start).Milliseconds(), nil
}

// IndustryBySlug - Get industry info by slug
func IndustryBySlug(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	slug, ok := params["slug"].(string)
	if !ok || slug == "" {
		return map[string]interface{}{}, 0, 0, nil
	}

	query := `
		SELECT id, name, slug, listing_title, listing_description
		FROM industries
		WHERE (
			slug = $1
			OR slug LIKE $1 || '%'
			OR slug LIKE '%' || $1 || '%'
			OR $1 LIKE '%' || slug || '%'
			OR name ILIKE '%' || $1 || '%'
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
	var listingTitle, description sql.NullString

	err := db.QueryRowContext(ctx, query, slug).Scan(&id, &name, &industrySlug, &listingTitle, &description)
	if err != nil {
		if err == sql.ErrNoRows {
			return map[string]interface{}{}, 0, time.Since(start).Milliseconds(), nil
		}
		return nil, 0, 0, err
	}

	industry := map[string]interface{}{
		"id":   id,
		"name": name,
		"slug": industrySlug,
	}

	if listingTitle.Valid && listingTitle.String != "" {
		industry["listing_title"] = listingTitle.String
	}
	if description.Valid && description.String != "" {
		industry["listing_description"] = description.String
	}

	return industry, 1, time.Since(start).Milliseconds(), nil
}

// FranchiseOverview - Get franchising overview for detail page

func FranchiseOverview(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	franchiseID, err := extractFranchiseID(params)
	if err != nil {
		return nil, 0, 0, err
	}

	query := `
SELECT
	l.contact_email,
	f.parent_company,
	f.business_type,
	f.leader_name,
	f.leader_role,
	l.founded_year as established_year,
	f.units_count,
	fc.city as city,
	fir.franchise_fee,
	fir.royalty_percentage,
	fir.monthly_turnover_min,
	fir.monthly_turnover_max,
	fo.space_min_sqft,
	fo.space_max_sqft,
	a.association_type,
	a.sector_represented,
	a.legal_status,
	a.headquarters_address,
	a.regional_presence,
	a.contact_phone
FROM listings l
LEFT JOIN franchises f ON l.id = f.id
LEFT JOIN associations a ON l.id = a.id
LEFT JOIN franchise_investment_requirement fir ON f.id = fir.franchise_id
LEFT JOIN franchise_operations fo ON f.id = fo.franchise_id
LEFT JOIN LATERAL (
	SELECT city
	FROM listing_cities
	WHERE listing_id = l.id
	ORDER BY created_at DESC
	LIMIT 1
) fc ON true
WHERE l.id = $1
	`

	var (
		email, parentCompany, businessType sql.NullString
		leaderName, leaderRole, city       sql.NullString
		establishedYear                    sql.NullInt32
		unitsCount                         sql.NullInt32
		franchiseFee, royaltyPercent       sql.NullFloat64
		turnoverMin, turnoverMax           sql.NullFloat64
		spaceMin, spaceMax                 sql.NullInt32

		associationType, sectorRepresented  sql.NullString
		legalStatus, headquartersAddress    sql.NullString
		regionalPresence, contactPhone      sql.NullString
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

		&associationType,
		&sectorRepresented,
		&legalStatus,
		&headquartersAddress,
		&regionalPresence,
		&contactPhone,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, 0, ErrNotFound
		}
		return nil, 0, 0, err
	}

	overview := map[string]interface{}{}

	if email.Valid {
		decryptedEmail := email.String
		if encryptor != nil && decryptedEmail != "" {
			dec, err := encryptor.Decrypt(decryptedEmail)
			if err == nil {
				decryptedEmail = string(dec)
			}
		}
		overview["email"] = decryptedEmail
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

	if associationType.Valid {
		overview["association_type"] = associationType.String
	}
	if sectorRepresented.Valid {
		overview["sector_represented"] = sectorRepresented.String
	}
	if legalStatus.Valid {
		overview["legal_status"] = legalStatus.String
	}
	if headquartersAddress.Valid {
		overview["headquarters_address"] = headquartersAddress.String
	}
	if regionalPresence.Valid {
		overview["regional_presence"] = regionalPresence.String
	}
	if contactPhone.Valid {
		overview["contact_phone"] = contactPhone.String
	}

	return overview, 1, time.Since(start).Milliseconds(), nil
}

// FranchiseBusiness - Get business overview (products/services)
func FranchiseBusiness(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
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
func FranchiseInvestment(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
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
			single_unit_cost_max,
			revenue_model
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
		revenueModel                 sql.NullString
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
		&revenueModel,
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
	if revenueModel.Valid && revenueModel.String != "" {
		var revModel map[string]interface{}
		if err := json.Unmarshal([]byte(revenueModel.String), &revModel); err == nil {
			investment["revenue_model"] = revModel
		}
	}

	return investment, 1, time.Since(start).Milliseconds(), nil
}

// FranchiseOperations - Get operations details
func FranchiseOperations(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

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
			marketing_support,
			territory_details,
			development_schedule,
			support_training,
			legal_compliance
		FROM franchise_operations
		WHERE franchise_id = $1
	`

	var spaceMin, spaceMax, staffMin, staffMax sql.NullInt32
	var trainingProvided sql.NullBool
	var trainingDetails, marketingSupport sql.NullString
	var territoryDetails, developmentSchedule sql.NullString
	var supportTraining, legalCompliance sql.NullString

	err = db.QueryRowContext(ctx, query, franchiseID).Scan(
		&spaceMin, &spaceMax, &staffMin, &staffMax,
		&trainingProvided, &trainingDetails, &marketingSupport,
		&territoryDetails, &developmentSchedule,
		&supportTraining, &legalCompliance,
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
	if territoryDetails.Valid && territoryDetails.String != "" {
		var td map[string]interface{}
		if err := json.Unmarshal([]byte(territoryDetails.String), &td); err == nil {
			operations["territory_details"] = td
		}
	}
	if developmentSchedule.Valid && developmentSchedule.String != "" {
		var ds map[string]interface{}
		if err := json.Unmarshal([]byte(developmentSchedule.String), &ds); err == nil {
			operations["development_schedule"] = ds
		}
	}
	if supportTraining.Valid && supportTraining.String != "" {
		var st map[string]interface{}
		if err := json.Unmarshal([]byte(supportTraining.String), &st); err == nil {
			operations["support_training"] = st
		}
	}
	if legalCompliance.Valid && legalCompliance.String != "" {
		var lc map[string]interface{}
		if err := json.Unmarshal([]byte(legalCompliance.String), &lc); err == nil {
			operations["legal_compliance"] = lc
		}
	}

	return operations, 1, time.Since(start).Milliseconds(), nil
}

// FranchiseSocial - Get social links
func FranchiseSocial(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
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
		FROM listing_social_links
		WHERE listing_id = $1
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
	encryptor *crypto.Encryptor,
) (interface{}, int, int64, error) {

	start := time.Now()

	slug, ok := params["slug"].(string)
	if !ok {
		return nil, 0, 0, ErrInvalidParams
	}

	entityType, ok := params["entityType"].(string)
	if !ok || entityType == "" {
		entityType = "franchise"
	}

	var industryID, name string
	var description sql.NullString

	err := db.QueryRowContext(ctx, `
		SELECT id, name, listing_description
		FROM industries
		WHERE slug = $1 AND is_active = true
	`, slug).Scan(&industryID, &name, &description)
	if err != nil {
		if err == sql.ErrNoRows {
			return map[string]interface{}{}, 0, time.Since(start).Milliseconds(), nil
		}
		return nil, 0, 0, err
	}

	rows, _ := db.QueryContext(ctx, `
		SELECT question
		FROM category_questions
		WHERE reference_id = $1 AND entity_type = $2
		ORDER BY created_at
		LIMIT 8
	`, industryID, entityType)
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

func detectIntentTag(searchQuery string) string {
	q := strings.ToLower(searchQuery)
	for _, kw := range []string{"cheap", "low investment", "budget", "affordable", "under 5", "under 10", "5 lakh", "10 lakh", "less investment", "minimum investment", "small investment"} {
		if strings.Contains(q, kw) {
			return "low-investment"
		}
	}
	for _, kw := range []string{"delhi", "mumbai", "bangalore", "bengaluru", "hyderabad", "chennai", "pune", "kolkata", "jaipur", "lucknow", "indore", "city", "location", "near me", "tier 2", "tier 3", "local"} {
		if strings.Contains(q, kw) {
			return "location-based"
		}
	}
	for _, kw := range []string{"roi", "profit", "return", "earning", "income", "revenue", "margin", "payback", "profitable"} {
		if strings.Contains(q, kw) {
			return "roi-focused"
		}
	}
	return "general"
}

func CategoryQuestionsByIndustry(
	ctx context.Context,
	db *sql.DB,
	params map[string]interface{},
	encryptor *crypto.Encryptor,
) (interface{}, int, int64, error) {
	start := time.Now()
	var referenceIDs []string
	searchQuery, _ := params["searchQuery"].(string)
	intentTag := "general"

	entityType, ok := params["entityType"].(string)
	if !ok || entityType == "" {
		entityType = "franchise"
	}

	if v, ok := params["industryId"].(string); ok && v != "" {
		referenceIDs = []string{v}
	} else if v, ok := params["industrySlug"].(string); ok && v != "" {
		rows, err := db.QueryContext(ctx, `
			SELECT id FROM industries
			WHERE slug = ANY(string_to_array($1, ','))
			  AND is_active = true
		`, v)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err == nil && id != "" {
					referenceIDs = append(referenceIDs, id)
				}
			}
		}
		if len(referenceIDs) == 0 {
			referenceIDs = []string{"00000000-0000-0000-0000-000000000000"}
			intentTag = detectIntentTag(searchQuery)
		}
	} else if v, ok := params["categoryId"].(string); ok && v != "" {
		referenceIDs = []string{v}
	} else if v, ok := params["categorySlug"].(string); ok && v != "" {
		var id string
		err := db.QueryRowContext(ctx,
			"SELECT id FROM categories WHERE slug = $1 AND is_active = true", v,
		).Scan(&id)
		if err == nil {
			referenceIDs = []string{id}
		} else {
			return []string{}, 0, time.Since(start).Milliseconds(), nil
		}
	} else {
		referenceIDs = []string{"00000000-0000-0000-0000-000000000000"}
		intentTag = detectIntentTag(searchQuery)
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var questions []string
	if len(referenceIDs) > 1 {
		var lists [][]string
		for _, refID := range referenceIDs {
			subRows, err := db.QueryContext(queryCtx, `
				SELECT question
				FROM category_questions
				WHERE reference_id = $1::uuid
				  AND (intent_tag = $2 OR intent_tag = 'general')
				  AND entity_type = $3
				ORDER BY
					CASE WHEN intent_tag = $2 THEN 0 ELSE 1 END,
					created_at
				LIMIT 8
			`, refID, intentTag, entityType)
			if err == nil {
				var subList []string
				for subRows.Next() {
					var q string
					if err := subRows.Scan(&q); err == nil && q != "" {
						subList = append(subList, q)
					}
				}
				subRows.Close()
				if len(subList) > 0 {
					lists = append(lists, subList)
				}
			}
		}

		maxLen := 0
		for _, list := range lists {
			if len(list) > maxLen {
				maxLen = len(list)
			}
		}

		for i := 0; i < maxLen; i++ {
			for _, list := range lists {
				if i < len(list) {
					questions = append(questions, list[i])
				}
			}
		}

		if len(questions) > 8 {
			questions = questions[:8]
		}
	} else {
		refIDsStr := strings.Join(referenceIDs, ",")
		rows, err := db.QueryContext(queryCtx, `
			SELECT question
			FROM category_questions
			WHERE reference_id = ANY(string_to_array($1, ',')::uuid[])
			  AND (intent_tag = $2 OR intent_tag = 'general')
			  AND entity_type = $3
			ORDER BY
				CASE WHEN intent_tag = $2 THEN 0 ELSE 1 END,
				created_at
			LIMIT 8
		`, refIDsStr, intentTag, entityType)
		if err != nil {
			return []string{}, 0, time.Since(start).Milliseconds(), nil
		}
		defer rows.Close()
		for rows.Next() {
			var q string
			if err := rows.Scan(&q); err == nil && q != "" {
				questions = append(questions, q)
			}
		}
	}
	return questions, len(questions), time.Since(start).Milliseconds(), nil
}

// FeaturedCategoriesByIndustry - Get featured categories for an industry (detail page)
func FeaturedCategoriesByIndustry(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
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
