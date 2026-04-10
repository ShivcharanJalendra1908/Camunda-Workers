package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	_ "github.com/lib/pq"
)

const (
	HomeIndex             = "franchise_home"
	ListingsIndex         = "franchise_listings"
	IndustriesIndex       = "franchise_industries"
	IndustryInsightsIndex = "industry_insights"
	BrowseIndex           = "franchise_browse"
)

type SyncManager struct {
	db *sql.DB
	es *elasticsearch.Client
}

func main() {
	log.Println("🚀 Starting Postgres → Elasticsearch sync...")

	// Connect to Postgres
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/franchises?sslmode=disable"
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("❌ Failed to connect to Postgres: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("❌ Postgres ping failed: %v", err)
	}
	log.Println("✅ Connected to Postgres")

	// Connect to Elasticsearch
	esURL := os.Getenv("ELASTICSEARCH_URL")
	if esURL == "" {
		esURL = "http://localhost:9200"
	}

	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{esURL},
	})
	if err != nil {
		log.Fatalf("❌ Failed to connect to Elasticsearch: %v", err)
	}

	info, err := es.Info()
	if err != nil {
		log.Fatalf("❌ Elasticsearch info failed: %v", err)
	}
	defer info.Body.Close()
	log.Println("✅ Connected to Elasticsearch")

	manager := &SyncManager{db: db, es: es}

	// Sync all indexes
	ctx := context.Background()

	log.Println("\n📦 Syncing Index 1: franchise_home...")
	if err := manager.syncHomeIndex(ctx); err != nil {
		log.Printf("❌ Home index sync failed: %v", err)
	} else {
		log.Println("✅ Home index synced")
	}

	log.Println("\n📦 Syncing Index 2: franchise_listings...")
	if err := manager.syncListingsIndex(ctx); err != nil {
		log.Printf("❌ Listings index sync failed: %v", err)
	} else {
		log.Println("✅ Listings index synced")
	}

	log.Println("\n📦 Syncing Index 3: franchise_industries...")
	if err := manager.syncIndustriesIndex(ctx); err != nil {
		log.Printf("❌ Industries index sync failed: %v", err)
	} else {
		log.Println("✅ Industries index synced")
	}

	log.Println("\n📦 Syncing Index 4: industry_insights...")
	if err := manager.syncIndustryInsightsIndex(ctx); err != nil {
		log.Printf("❌ Industry insights index sync failed: %v", err)
	} else {
		log.Println("✅ Industry insights index synced")
	}

	log.Println("\n📦 Syncing Index 5: franchise_browse...")
	if err := manager.syncBrowseIndex(ctx); err != nil {
		log.Printf("❌ Browse index sync failed: %v", err)
	} else {
		log.Println("✅ Browse index synced")
	}

	log.Println("\n🎉 Sync completed successfully!")
}

// ============================================================
// INDEX 1: FRANCHISE HOME
// ============================================================
func (m *SyncManager) syncHomeIndex(ctx context.Context) error {
	// Get industries
	query := `
		SELECT id, name, slug, color_hex
		FROM industries
		WHERE is_active = true
		ORDER BY display_order
	`

	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query industries: %w", err)
	}
	defer rows.Close()

	var industries []map[string]interface{}
	for rows.Next() {
		var id, name, slug, colorHex string
		if err := rows.Scan(&id, &name, &slug, &colorHex); err != nil {
			continue
		}
		industries = append(industries, map[string]interface{}{
			"id":        id,
			"name":      name,
			"slug":      slug,
			"color_hex": colorHex,
		})
	}

	// Get statistics
	stats := map[string]interface{}{}

	var totalIndustries, totalFranchises, totalOutlets, newFranchisorsThisYear int64

	if err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM industries WHERE is_active = true").Scan(&totalIndustries); err != nil {
		log.Printf("Warning: failed to get total industries: %v", err)
	}
	stats["total_industries"] = totalIndustries

	if err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM franchises").Scan(&totalFranchises); err != nil {
		log.Printf("Warning: failed to get total franchises: %v", err)
	}
	stats["total_franchises"] = totalFranchises

	if err := m.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(total_outlets), 0) FROM franchises").Scan(&totalOutlets); err != nil {
		log.Printf("Warning: failed to get total outlets: %v", err)
	}
	stats["total_outlets"] = totalOutlets

	currentYear := time.Now().Year()
	if err := m.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM franchises WHERE EXTRACT(YEAR FROM created_at) = $1",
		currentYear,
	).Scan(&newFranchisorsThisYear); err != nil {
		log.Printf("Warning: failed to get new franchisors this year: %v", err)
	}
	stats["new_franchisors_this_year"] = newFranchisorsThisYear

	// Build document
	doc := map[string]interface{}{
		"page_type":  "home",
		"industries": industries,
		"statistics": stats,
		"updated_at": time.Now().Format(time.RFC3339),
	}

	return m.indexDocument(ctx, HomeIndex, "homepage", doc)
}

// ============================================================
// INDEX 2: FRANCHISE LISTINGS
// ============================================================
func (m *SyncManager) syncListingsIndex(ctx context.Context) error {
	query := `
        SELECT DISTINCT ON (f.id)
            f.id,
            f.name,
            f.slug,
            f.founded_year,
            f.total_outlets,
            f.short_description,
            f.logo_url_circle,
            f.logo_url_square,
            COALESCE(fs.rating, 0) as rating,
            i.id as industry_id,
            i.name as industry_name,
            i.slug as industry_slug,
            i.color_hex as industry_color,
			i.image_url as industry_image_url
        FROM franchises f
        LEFT JOIN franchise_stats fs ON f.id = fs.franchise_id
        LEFT JOIN franchise_categories fc ON f.id = fc.franchise_id
        LEFT JOIN categories c ON fc.category_id = c.id
        LEFT JOIN industries i ON c.industry_id = i.id
        WHERE i.id IS NOT NULL
        ORDER BY f.id, fc.is_primary DESC NULLS LAST, fc.created_at ASC
    `

	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query franchises: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var (
			id, name, slug                         string
			shortDescription                       sql.NullString
			logoURLCircle, logoURLSquare           sql.NullString
			foundedYear                            sql.NullInt32
			totalOutlets                           sql.NullInt32
			rating                                 float64
			industryID, industryName, industrySlug string
			industryColor                          sql.NullString
			industryImageURL                       sql.NullString // ✅ NEW
		)

		if err := rows.Scan(
			&id, &name, &slug, &foundedYear, &totalOutlets, &shortDescription,
			&logoURLCircle, &logoURLSquare,
			&rating, &industryID, &industryName, &industrySlug, &industryColor,
			&industryImageURL,
		); err != nil {
			log.Printf("⚠️ Failed to scan franchise row: %v", err)
			continue
		}

		// Location
		var location string
		m.db.QueryRowContext(ctx,
			"SELECT city FROM franchise_cities WHERE franchise_id = $1 LIMIT 1",
			id,
		).Scan(&location)

		// Tags
		tags := m.generateSearchTags(ctx, id, name, industryName)

		// Space
		var minSpace, maxSpace sql.NullInt32
		m.db.QueryRowContext(ctx,
			"SELECT space_min_sqft, space_max_sqft FROM franchise_operations WHERE franchise_id = $1",
			id,
		).Scan(&minSpace, &maxSpace)

		if !minSpace.Valid || minSpace.Int32 == 0 {
			minSpace.Int32 = 200
			minSpace.Valid = true
		}
		if !maxSpace.Valid || maxSpace.Int32 == 0 {
			maxSpace.Int32 = 1000
			maxSpace.Valid = true
		}

		// ✅ Investment + ROI — ek hi query mein
		var minInv, maxInv, roiMin, roiMax sql.NullFloat64
		var paybackMin sql.NullInt32
		m.db.QueryRowContext(ctx,
			`SELECT initial_investment_min, initial_investment_max,
			        roi_min_percentage, roi_max_percentage, payback_min_months
			 FROM franchise_investment_requirement WHERE franchise_id = $1`,
			id,
		).Scan(&minInv, &maxInv, &roiMin, &roiMax, &paybackMin)

		// Categories
		categories := m.getCategories(ctx, id)

		subCategories := m.getSubCategories(ctx, id)

		// Clean description
		cleanDesc := cleanDescription(shortDescription.String)

		doc := map[string]interface{}{
			"franchise_id": id,
			"name":         name,
			"slug":         slug,
			"description":  cleanDesc,
			"logo": map[string]interface{}{
				"circle": logoURLCircle.String,
				"square": logoURLSquare.String,
				"alt":    name,
			},
			"location":      location,
			"tags":          tags,
			"rating":        rating,
			"total_outlets": totalOutlets.Int32,
			"updated_at":    time.Now().Format(time.RFC3339),
		}

		if foundedYear.Valid {
			doc["year_of_establishment"] = foundedYear.Int32
		}

		// ✅ Space — float (range query ke liye)
		if minSpace.Valid || maxSpace.Valid {
			doc["space"] = map[string]interface{}{
				"minSpace":  float64(minSpace.Int32),
				"maxSpace":  float64(maxSpace.Int32),
				"spaceUnit": "sq ft",
			}
		}

		// ✅ Investment — lakhs mein (display + filter dono)
		if minInv.Valid || maxInv.Valid {
			minLakhs := minInv.Float64 / 100000
			maxLakhs := maxInv.Float64 / 100000

			doc["investmentRange"] = map[string]interface{}{
				"minInvestment":  minLakhs, // ✅ float lakhs — range query chalegi
				"maxInvestment":  maxLakhs,
				"investmentUnit": "Lakhs",
			}
			doc["investment"] = map[string]interface{}{
				"min_investment": minLakhs,
				"max_investment": maxLakhs,
			}
		}

		// ✅ ROI — naya field
		if roiMin.Valid || roiMax.Valid {
			doc["roi"] = map[string]interface{}{
				"min":           roiMin.Float64,
				"max":           roiMax.Float64,
				"period_months": paybackMin.Int32,
			}
		}

		// Industry
		color := industryColor.String
		if color == "" {
			color = "#FF6B6B"
		}
		doc["industry"] = map[string]interface{}{
			"id":        industryID,
			"name":      industryName,
			"slug":      industrySlug,
			"color":     color,
			"image_url": industryImageURL.String,
		}

		doc["categories"] = categories

		doc["sub_categories"] = subCategories

		if err := m.indexDocument(ctx, ListingsIndex, id, doc); err != nil {
			log.Printf("⚠️ Failed to index franchise %s: %v", id, err)
			continue
		}

		count++
		if count%10 == 0 {
			log.Printf("   Indexed %d franchises...", count)
		}
	}

	log.Printf("   ✅ Total franchises indexed: %d", count)
	return nil
}

// ============================================================
// GENERATE TAGS
// ============================================================
func (m *SyncManager) generateSearchTags(ctx context.Context, franchiseID, franchiseName, industryName string) []string {
	tags := []string{}
	seen := make(map[string]bool)

	addTag := func(t string) {
		t = strings.TrimSpace(t)
		if t != "" && !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			tags = append(tags, t)
		}
	}

	// Categories + subcategories from DB
	rows, err := m.db.QueryContext(ctx, `
        SELECT c.name, sc.name
        FROM franchise_categories fc
        INNER JOIN categories c ON fc.category_id = c.id
        LEFT JOIN sub_categories sc ON fc.sub_category_id = sc.id
        WHERE fc.franchise_id = $1
        ORDER BY fc.is_primary DESC
    `, franchiseID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var catName string
			var subCatName sql.NullString
			if err := rows.Scan(&catName, &subCatName); err == nil {
				addTag(catName)
				if subCatName.Valid {
					addTag(subCatName.String)
				}
			}
		}
	}

	// Brand first word + full name
	nameParts := strings.Fields(franchiseName)
	if len(nameParts) > 0 {
		addTag(nameParts[0])
	}
	addTag(franchiseName)

	return tags
}

// ============================================================
// INDEX 3: FRANCHISE INDUSTRIES
// ============================================================
func (m *SyncManager) syncIndustriesIndex(ctx context.Context) error {
	query := `
		SELECT id, name, slug, listing_description
		FROM industries
		WHERE is_active = true
		ORDER BY display_order
	`

	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query industries: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, name, slug string
		var description sql.NullString

		if err := rows.Scan(&id, &name, &slug, &description); err != nil {
			continue
		}

		// Get categories for this industry
		categoriesQuery := `
			SELECT id, name, slug, icon_url, image_url
			FROM categories
			WHERE industry_id = $1 AND is_active = true
			ORDER BY display_order
		`

		catRows, err := m.db.QueryContext(ctx, categoriesQuery, id)
		if err != nil {
			continue
		}

		var categories []map[string]interface{}
		for catRows.Next() {
			var catID, catName, catSlug string
			var catIconURL, catImageURL sql.NullString
			if err := catRows.Scan(&catID, &catName, &catSlug, &catIconURL, &catImageURL); err != nil {
				continue
			}
			categories = append(categories, map[string]interface{}{
				"category_id":   catID,
				"category_name": catName,
				"category_slug": catSlug,
				"icon_url":      catIconURL.String,
				"image_url":     catImageURL.String,
			})
		}
		catRows.Close()

		// Get questions
		questionsQuery := `
			SELECT question
			FROM category_questions
			WHERE reference_id = $1
			LIMIT 10
		`

		qRows, err := m.db.QueryContext(ctx, questionsQuery, id)
		var questions []string
		if err == nil {
			for qRows.Next() {
				var q string
				if err := qRows.Scan(&q); err == nil {
					questions = append(questions, q)
				}
			}
			qRows.Close()
		}

		// Get recommended franchises (top 6 by rating)
		recQuery := `
			SELECT f.id, f.name, i.name as industry_name, i.image_url as industry_image_url
			FROM franchises f
			INNER JOIN franchise_categories fc ON f.id = fc.franchise_id
			INNER JOIN categories c ON fc.category_id = c.id
			INNER JOIN industries i ON c.industry_id = i.id
			LEFT JOIN franchise_stats fs ON f.id = fs.franchise_id
			WHERE i.id = $1
			ORDER BY fs.rating DESC NULLS LAST
			LIMIT 6
		`

		recRows, err := m.db.QueryContext(ctx, recQuery, id)
		var recommended []map[string]interface{}
		if err == nil {
			for recRows.Next() {
				var fID, fName, indName string
				var indImageURL sql.NullString
				if err := recRows.Scan(&fID, &fName, &indName, &indImageURL); err == nil {
					recommended = append(recommended, map[string]interface{}{
						"franchise_id":       fID,
						"franchise_name":     fName,
						"industry":           indName,
						"industry_image_url": indImageURL.String, // ✅ NEW
					})
				}
			}
			recRows.Close()
		}

		// ✅ Get market insights from industry_market_insights table
		var growthRateTitle, growthRateDesc, marketTrendTitle, marketTrendDesc string

		insightQuery := `
			SELECT 
				growth_rate_title,
				growth_rate_description,
				market_trend_title,
				market_trend_description
			FROM industry_market_insights
			WHERE industry_id = $1
		`

		err = m.db.QueryRowContext(ctx, insightQuery, id).Scan(
			&growthRateTitle,
			&growthRateDesc,
			&marketTrendTitle,
			&marketTrendDesc,
		)

		// Build market insights object
		var marketInsights map[string]interface{}

		if err != nil {
			// If no insights found, use defaults
			marketInsights = map[string]interface{}{
				"growth_rate_title":        "Growth Rate",
				"growth_rate_description":  "Data not available",
				"market_trend_title":       "Market Trend",
				"market_trend_description": "Data not available",
			}
		} else {
			// Use real data from table
			marketInsights = map[string]interface{}{
				"growth_rate_title":        growthRateTitle,
				"growth_rate_description":  growthRateDesc,
				"market_trend_title":       marketTrendTitle,
				"market_trend_description": marketTrendDesc,
			}
		}

		// Build document
		doc := map[string]interface{}{
			"industry_id":            id,
			"industry_name":          name,
			"industry_slug":          slug,
			"description":            description.String,
			"categories":             categories,
			"questions":              questions,
			"market_insights":        marketInsights,
			"recommended_franchises": recommended,
			"updated_at":             time.Now().Format(time.RFC3339),
		}

		// Index to ES
		if err := m.indexDocument(ctx, IndustriesIndex, id, doc); err != nil {
			log.Printf("⚠️ Failed to index industry %s: %v", id, err)
			continue
		}

		count++
	}

	log.Printf("   ✅ Total industries indexed: %d", count)
	return nil
}

// ============================================================
// INDEX 4: INDUSTRY INSIGHTS
// ============================================================
func (m *SyncManager) syncIndustryInsightsIndex(ctx context.Context) error {
	// ✅ Check if industry_market_insights table exists
	var tableExists bool
	err := m.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT FROM information_schema.tables 
			WHERE table_schema = 'public' 
			AND table_name = 'industry_market_insights'
		)
	`).Scan(&tableExists)

	if err != nil {
		return fmt.Errorf("failed to check table existence: %w", err)
	}

	if !tableExists {
		log.Println("   ⚠️ Table 'industry_market_insights' does not exist. Skipping...")
		return nil
	}

	// ✅ Join with industries to only sync active industries
	query := `
		SELECT 
			imi.industry_id,
			imi.industry_slug,
			imi.intent_tag,
			imi.growth_rate_title,
			imi.growth_rate_description,
			imi.market_trend_title,
			imi.market_trend_description,
			imi.updated_at
		FROM industry_market_insights imi
		INNER JOIN industries i ON imi.industry_id = i.id
		WHERE i.is_active = true
		ORDER BY imi.updated_at DESC
	`

	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query industry insights: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var (
			industryID             string
			industrySlug           string
			intentTag              string
			growthRateTitle        string
			growthRateDescription  string
			marketTrendTitle       string
			marketTrendDescription string
			updatedAt              time.Time
		)

		if err := rows.Scan(
			&industryID,
			&industrySlug,
			&intentTag,
			&growthRateTitle,
			&growthRateDescription,
			&marketTrendTitle,
			&marketTrendDescription,
			&updatedAt,
		); err != nil {
			log.Printf("⚠️ Failed to scan industry insight row: %v", err)
			continue
		}

		// Build document
		doc := map[string]interface{}{
			"industry_id":              industryID,
			"industry_slug":            industrySlug,
			"intent_tag":               intentTag,
			"growth_rate_title":        growthRateTitle,
			"growth_rate_description":  growthRateDescription,
			"market_trend_title":       marketTrendTitle,
			"market_trend_description": marketTrendDescription,
			"updated_at":               updatedAt.Format(time.RFC3339),
		}

		// Use industry_id as document ID for easy updates
		if err := m.indexDocument(ctx, IndustryInsightsIndex, industryID, doc); err != nil {
			log.Printf("⚠️ Failed to index industry insight %s: %v", industryID, err)
			continue
		}

		count++
		if count%5 == 0 {
			log.Printf("   Indexed %d industry insights...", count)
		}
	}

	log.Printf("   ✅ Total industry insights indexed: %d", count)
	return nil
}

func (m *SyncManager) syncBrowseIndex(ctx context.Context) error {
	// Sab active industries fetch karo
	indRows, err := m.db.QueryContext(ctx, `
		SELECT id, name, slug, color_hex, icon_url, image_url, display_order
		FROM industries
		WHERE is_active = true
		ORDER BY display_order
	`)
	if err != nil {
		return fmt.Errorf("failed to query industries: %w", err)
	}
	defer indRows.Close()

	count := 0
	for indRows.Next() {
		var id, name, slug, colorHex string
		var iconURL, imageURL sql.NullString
		var displayOrder int

		if err := indRows.Scan(&id, &name, &slug, &colorHex, &iconURL, &imageURL, &displayOrder); err != nil {
			log.Printf("⚠️ Failed to scan industry: %v", err)
			continue
		}

		// Categories fetch karo
		catRows, err := m.db.QueryContext(ctx, `
			SELECT id, name, slug, icon_url, image_url, display_order
            FROM categories
			WHERE industry_id = $1 AND is_active = true
			ORDER BY display_order
		`, id)
		if err != nil {
			log.Printf("⚠️ Failed to get categories for industry %s: %v", id, err)
			continue
		}

		var categories []map[string]interface{}
		for catRows.Next() {
			var catID, catName, catSlug string
			var catIconURL, catImageURL sql.NullString
			var catOrder int

			if err := catRows.Scan(&catID, &catName, &catSlug, &catIconURL, &catImageURL, &catOrder); err != nil {
				continue
			}

			// Sub-categories fetch karo
			subRows, err := m.db.QueryContext(ctx, `
				SELECT id, name, slug, display_order
				FROM sub_categories
				WHERE category_id = $1 AND is_active = true
				ORDER BY display_order
			`, catID)

			var subCategories []map[string]interface{}
			if err == nil {
				for subRows.Next() {
					var subID, subName, subSlug string
					var subOrder int
					if err := subRows.Scan(&subID, &subName, &subSlug, &subOrder); err != nil {
						continue
					}
					subCategories = append(subCategories, map[string]interface{}{
						"sub_category_id":   subID,
						"sub_category_name": subName,
						"sub_category_slug": subSlug,
						"display_order":     subOrder,
					})
				}
				subRows.Close()
			}

			if subCategories == nil {
				subCategories = []map[string]interface{}{}
			}

			categories = append(categories, map[string]interface{}{
				"category_id":    catID,
				"category_name":  catName,
				"category_slug":  catSlug,
				"display_order":  catOrder,
				"icon_url":       catIconURL.String,
				"image_url":      catImageURL.String, // ✅ NEW
				"sub_categories": subCategories,
			})
		}
		catRows.Close()

		if categories == nil {
			categories = []map[string]interface{}{}
		}

		// Document build karo
		doc := map[string]interface{}{
			"industry_id":   id,
			"industry_name": name,
			"industry_slug": slug,
			"color_hex":     colorHex,
			"display_order": displayOrder,
			"categories":    categories,
			"updated_at":    time.Now().Format(time.RFC3339),
		}
		if iconURL.Valid {
			doc["icon_url"] = iconURL.String
		}

		if imageURL.Valid {
			doc["image_url"] = imageURL.String
		}

		if err := m.indexDocument(ctx, BrowseIndex, id, doc); err != nil {
			log.Printf("⚠️ Failed to index browse industry %s: %v", id, err)
			continue
		}

		count++
		log.Printf("   Indexed: %s (%d categories)", name, len(categories))
	}

	log.Printf("   ✅ Total industries indexed in browse: %d", count)
	return nil
}

// ============================================================
// HELPER FUNCTIONS
// ============================================================

func (m *SyncManager) getCleanedTags(ctx context.Context, franchiseID string) []string {
	query := `
		SELECT products, services
		FROM franchise_business_overview
		WHERE franchise_id = $1
	`

	var productsJSON, servicesJSON []byte
	if err := m.db.QueryRowContext(ctx, query, franchiseID).Scan(&productsJSON, &servicesJSON); err != nil {
		return []string{}
	}

	var products, services []string
	json.Unmarshal(productsJSON, &products)
	json.Unmarshal(servicesJSON, &services)

	// Combine and clean tags
	rawTags := append(products, services...)
	cleanedTags := cleanTags(rawTags)

	// Return max 5 tags
	if len(cleanedTags) > 5 {
		return cleanedTags[:5]
	}

	return cleanedTags
}

func cleanTags(tags []string) []string {
	cleaned := []string{}
	seen := make(map[string]bool)

	reg := regexp.MustCompile(`[^a-zA-Z]+`)

	for _, tag := range tags {
		words := regexp.MustCompile(`[\s/&,()]+`).Split(tag, -1)

		for _, word := range words {
			clean := reg.ReplaceAllString(word, "")
			clean = strings.TrimSpace(clean)

			if len(clean) < 3 || seen[clean] {
				continue
			}

			clean = strings.Title(strings.ToLower(clean))

			cleaned = append(cleaned, clean)
			seen[clean] = true

			if len(cleaned) >= 10 {
				break
			}
		}

		if len(cleaned) >= 10 {
			break
		}
	}

	return cleaned
}

func cleanDescription(desc string) string {
	desc = strings.TrimSuffix(strings.TrimSpace(desc), "...")
	desc = strings.TrimSpace(desc)

	if len(desc) > 200 {
		truncated := desc[:197]
		lastSpace := strings.LastIndex(truncated, " ")
		if lastSpace > 150 {
			desc = desc[:lastSpace]
		} else {
			desc = truncated
		}
	}

	desc = strings.TrimRight(desc, ".,;:")

	return desc
}

func (m *SyncManager) getCategories(ctx context.Context, franchiseID string) []map[string]interface{} {
	query := `
        SELECT c.id, c.name, c.slug
        FROM franchise_categories fc
        INNER JOIN categories c ON fc.category_id = c.id
        WHERE fc.franchise_id = $1 AND c.is_active = true
        ORDER BY fc.is_primary DESC, c.display_order
    `

	rows, err := m.db.QueryContext(ctx, query, franchiseID)
	if err != nil {
		return []map[string]interface{}{}
	}
	defer rows.Close()

	var categories []map[string]interface{}
	for rows.Next() {
		var id, name, slug string
		if err := rows.Scan(&id, &name, &slug); err != nil {
			continue
		}
		categories = append(categories, map[string]interface{}{
			"id":   id,
			"name": name,
			"slug": slug,
		})
	}

	if categories == nil {
		return []map[string]interface{}{}
	}

	return categories
}

func (m *SyncManager) getSubCategories(ctx context.Context, franchiseID string) []map[string]interface{} {
	query := `
        SELECT sc.id, sc.name, sc.slug, sc.category_id
        FROM franchise_categories fc
        INNER JOIN sub_categories sc ON fc.sub_category_id = sc.id
        WHERE fc.franchise_id = $1
          AND fc.sub_category_id IS NOT NULL
          AND sc.is_active = true
        ORDER BY fc.is_primary DESC, sc.display_order
    `

	rows, err := m.db.QueryContext(ctx, query, franchiseID)
	if err != nil {
		return []map[string]interface{}{}
	}
	defer rows.Close()

	var subCategories []map[string]interface{}
	for rows.Next() {
		var id, name, slug, categoryID string
		if err := rows.Scan(&id, &name, &slug, &categoryID); err != nil {
			continue
		}
		subCategories = append(subCategories, map[string]interface{}{
			"id":          id,
			"name":        name,
			"slug":        slug,
			"category_id": categoryID,
		})
	}

	if subCategories == nil {
		return []map[string]interface{}{}
	}

	return subCategories
}

func (m *SyncManager) indexDocument(ctx context.Context, index, docID string, doc map[string]interface{}) error {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("failed to marshal document: %w", err)
	}

	req := esapi.IndexRequest{
		Index:      index,
		DocumentID: docID,
		Body:       &buf,
		Refresh:    "true",
	}

	res, err := req.Do(ctx, m.es)
	if err != nil {
		return fmt.Errorf("index request failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("index response error: %s", res.String())
	}

	return nil
}
