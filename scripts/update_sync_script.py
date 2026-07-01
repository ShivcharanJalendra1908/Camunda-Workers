import os

file_path = r'c:\Users\lenovo\Desktop\LeMiCi\Camunda-Workers\scripts\sync\sync-postgres-to-es.go'

with open(file_path, 'r', encoding='utf-8') as f:
    content = f.read()

# 1. Update stats block
target_stats = """	if err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM franchises").Scan(&totalFranchises); err != nil {
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
	}"""

replace_stats = """	if err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM listings WHERE entity_type = 'franchise'").Scan(&totalFranchises); err != nil {
		log.Printf("Warning: failed to get total franchises: %v", err)
	}
	stats["total_franchises"] = totalFranchises

	if err := m.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(total_outlets), 0) FROM franchises").Scan(&totalOutlets); err != nil {
		log.Printf("Warning: failed to get total outlets: %v", err)
	}
	stats["total_outlets"] = totalOutlets

	currentYear := time.Now().Year()
	if err := m.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM listings WHERE entity_type = 'franchise' AND EXTRACT(YEAR FROM created_at) = $1",
		currentYear,
	).Scan(&newFranchisorsThisYear); err != nil {
		log.Printf("Warning: failed to get new franchisors this year: %v", err)
	}"""

content = content.replace(target_stats, replace_stats)

# 2. Update syncListingsIndex query
target_listings = """        SELECT DISTINCT ON (f.id)
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
			i.image_url as industry_image_url,
			f.entity_type,
			f.member_count,
			f.membership_fee_min,
			f.membership_fee_max,
			f.association_metadata,
			f.is_sponsored,
			f.is_featured,
			f.featured_order,
			f.featured_start_at,
			f.featured_expires_at,
			f.status,
			f.verified,
			f.trusted_seller,
			f.website_url
        FROM franchises f
        LEFT JOIN franchise_stats fs ON f.id = fs.franchise_id
        LEFT JOIN franchise_categories fc ON f.id = fc.franchise_id
        LEFT JOIN categories c ON fc.category_id = c.id
        LEFT JOIN industries i ON c.industry_id = i.id
        WHERE i.id IS NOT NULL
        ORDER BY f.id, fc.is_primary DESC NULLS LAST, fc.created_at ASC"""

replace_listings = """        SELECT DISTINCT ON (l.id)
            l.id,
            l.name,
            l.slug,
            f.established_year as founded_year,
            f.total_outlets,
            l.short_description,
            l.logo_url_circle,
            l.logo_url_square,
            COALESCE(ls.rating, 0) as rating,
            i.id as industry_id,
            i.name as industry_name,
            i.slug as industry_slug,
            i.color_hex as industry_color,
			i.image_url as industry_image_url,
			l.entity_type,
			a.member_count,
			a.membership_fee_min,
			a.membership_fee_max,
			json_build_object('association_type', a.association_type, 'sector_represented', a.sector_represented, 'member_type', a.member_type, 'member_size_classification', a.member_size_classification) as association_metadata,
			l.is_sponsored,
			l.is_featured,
			l.featured_order,
			l.featured_start_at,
			l.featured_expires_at,
			l.status,
			l.verified,
			l.trusted_seller,
			l.website_url
        FROM listings l
        LEFT JOIN franchises f ON l.id = f.id
        LEFT JOIN associations a ON l.id = a.id
        LEFT JOIN listing_stats ls ON l.id = ls.listing_id
        LEFT JOIN listing_categories lc ON l.id = lc.listing_id
        LEFT JOIN categories c ON lc.category_id = c.id
        LEFT JOIN industries i ON c.industry_id = i.id
        WHERE i.id IS NOT NULL
        ORDER BY l.id, lc.is_primary DESC NULLS LAST, lc.created_at ASC"""

content = content.replace(target_listings, replace_listings)

# 3. Update tags query
target_tags = """        SELECT c.name, sc.name
        FROM franchise_categories fc
        INNER JOIN categories c ON fc.category_id = c.id
        LEFT JOIN sub_categories sc ON fc.sub_category_id = sc.id
        WHERE fc.franchise_id = $1
        ORDER BY fc.is_primary DESC"""

replace_tags = """        SELECT c.name, sc.name
        FROM listing_categories lc
        INNER JOIN categories c ON lc.category_id = c.id
        LEFT JOIN sub_categories sc ON lc.sub_category_id = sc.id
        WHERE lc.listing_id = $1
        ORDER BY lc.is_primary DESC"""

content = content.replace(target_tags, replace_tags)

# 4. Update recQuery
target_rec = """			SELECT f.id, f.name, i.name as industry_name, i.image_url as industry_image_url
			FROM franchises f
			INNER JOIN franchise_categories fc ON f.id = fc.franchise_id
			INNER JOIN categories c ON fc.category_id = c.id
			INNER JOIN industries i ON c.industry_id = i.id
			LEFT JOIN franchise_stats fs ON f.id = fs.franchise_id
			WHERE i.id = $1
			ORDER BY fs.rating DESC NULLS LAST
			LIMIT 6"""

replace_rec = """			SELECT l.id, l.name, i.name as industry_name, i.image_url as industry_image_url
			FROM listings l
			INNER JOIN listing_categories lc ON l.id = lc.listing_id
			INNER JOIN categories c ON lc.category_id = c.id
			INNER JOIN industries i ON c.industry_id = i.id
			LEFT JOIN listing_stats ls ON l.id = ls.listing_id
			WHERE i.id = $1 AND l.entity_type = 'franchise'
			ORDER BY ls.rating DESC NULLS LAST
			LIMIT 6"""

content = content.replace(target_rec, replace_rec)

# 5. Other franchise_categories replacements
content = content.replace("FROM franchise_categories fc", "FROM listing_categories lc")
content = content.replace("INNER JOIN franchise_categories fc ON f.id = fc.franchise_id", "INNER JOIN listing_categories lc ON l.id = lc.listing_id")
content = content.replace("WHERE fc.category_id = $1", "WHERE lc.category_id = $1")
content = content.replace("fc.franchise_id", "lc.listing_id")

with open(file_path, 'w', encoding='utf-8') as f:
    f.write(content)

print("Done updating script!")
