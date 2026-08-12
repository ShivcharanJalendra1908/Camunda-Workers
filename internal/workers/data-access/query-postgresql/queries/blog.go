// internal/workers/data-access/query-postgresql/queries/blog.go
package queries

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"camunda-workers/internal/crypto"
)

// BlogListing — Blog Listing Page / Category Page
// queryType: BLOG_LISTING
// params: page, pageSize, search, categoryId, entityType
func BlogListing(ctx context.Context, db *sql.DB, params map[string]interface{}, _ *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	page := 1
	pageSize := 10
	if p, ok := params["page"].(float64); ok && p > 0 {
		page = int(p)
	}
	if ps, ok := params["pageSize"].(float64); ok && ps > 0 {
		pageSize = int(ps)
	}
	offset := (page - 1) * pageSize

	baseQuery := `
		SELECT
			l.id, l.name AS title, l.slug, l.short_description,
			b.featured_image_url, b.reading_time_mins, b.author_display_name,
			COALESCE(array_to_json(b.tags), '[]'::json) AS tags,
			COALESCE(ls.view_count, 0) AS view_count,
			l.created_at,
			COALESCE(json_agg(DISTINCT jsonb_build_object('id', c.id, 'name', c.name)) FILTER (WHERE c.id IS NOT NULL), '[]'::json) AS categories
		FROM listings l
		JOIN blogs b ON b.id = l.id
		LEFT JOIN listing_stats ls ON ls.listing_id = l.id
		LEFT JOIN listing_categories lc ON lc.listing_id = l.id
		LEFT JOIN categories c ON c.id = lc.category_id
		WHERE l.entity_type = 'blog' AND l.status = 'LIVE'
	`

	args := []interface{}{}
	argIdx := 1

	if search, ok := params["search"].(string); ok && search != "" {
		baseQuery += fmt.Sprintf(" AND (l.name ILIKE $%d OR l.short_description ILIKE $%d)", argIdx, argIdx+1)
		term := "%" + search + "%"
		args = append(args, term, term)
		argIdx += 2
	}
	if catID, ok := params["categoryId"].(string); ok && catID != "" {
		baseQuery += fmt.Sprintf(" AND lc.category_id = $%d", argIdx)
		args = append(args, catID)
		argIdx++
	}

	baseQuery += fmt.Sprintf(
		" GROUP BY l.id, b.id, ls.view_count ORDER BY l.created_at DESC LIMIT $%d OFFSET $%d",
		argIdx, argIdx+1,
	)
	args = append(args, pageSize, offset)

	rows, err := db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("blog listing query failed: %w", err)
	}
	defer rows.Close()

	var blogs []map[string]interface{}
	for rows.Next() {
		var (
			id, title, slug        string
			shortDesc, imageURL    sql.NullString
			authorName             sql.NullString
			readingTime            int
			viewCount              int64
			createdAt              time.Time
			tagsJSON, catsJSON     []byte
		)
		if err := rows.Scan(&id, &title, &slug, &shortDesc, &imageURL, &readingTime, &authorName, &tagsJSON, &viewCount, &createdAt, &catsJSON); err != nil {
			continue
		}
		var tags []string
		var cats []interface{}
		json.Unmarshal(tagsJSON, &tags)
		json.Unmarshal(catsJSON, &cats)
		blogs = append(blogs, map[string]interface{}{
			"id":                  id,
			"title":               title,
			"slug":                slug,
			"short_description":   shortDesc.String,
			"featured_image_url":  imageURL.String,
			"reading_time_mins":   readingTime,
			"author_display_name": authorName.String,
			"tags":                tags,
			"categories":          cats,
			"view_count":          viewCount,
			"published_at":        createdAt.Format(time.RFC3339),
		})
	}
	if blogs == nil {
		blogs = []map[string]interface{}{}
	}

	elapsed := time.Since(start).Milliseconds()
	return map[string]interface{}{
		"blogs":    blogs,
		"page":     page,
		"pageSize": pageSize,
	}, len(blogs), elapsed, nil
}

// BlogFeatured — Home Page "Featured Posts" section
// queryType: BLOG_FEATURED
func BlogFeatured(ctx context.Context, db *sql.DB, params map[string]interface{}, _ *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	limit := 6
	if l, ok := params["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT l.id, l.name AS title, l.slug, l.short_description,
		       b.featured_image_url, b.reading_time_mins, b.author_display_name, l.created_at
		FROM listings l
		JOIN blogs b ON b.id = l.id
		WHERE l.entity_type = 'blog' AND l.status = 'LIVE' AND l.is_featured = TRUE
		ORDER BY l.featured_order ASC NULLS LAST, l.created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("blog featured query failed: %w", err)
	}
	defer rows.Close()

	var blogs []map[string]interface{}
	for rows.Next() {
		var id, title, slug string
		var shortDesc, image, author sql.NullString
		var readingTime int
		var createdAt time.Time
		rows.Scan(&id, &title, &slug, &shortDesc, &image, &readingTime, &author, &createdAt)
		blogs = append(blogs, map[string]interface{}{
			"id":                  id,
			"title":               title,
			"slug":                slug,
			"short_description":   shortDesc.String,
			"featured_image_url":  image.String,
			"reading_time_mins":   readingTime,
			"author_display_name": author.String,
			"published_at":        createdAt.Format(time.RFC3339),
		})
	}
	if blogs == nil {
		blogs = []map[string]interface{}{}
	}

	elapsed := time.Since(start).Milliseconds()
	return map[string]interface{}{"featured_blogs": blogs}, len(blogs), elapsed, nil
}

// BlogPopular — Home Page + Category Page "Popular Right Now" section
// queryType: BLOG_POPULAR
func BlogPopular(ctx context.Context, db *sql.DB, params map[string]interface{}, _ *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	limit := 6
	if l, ok := params["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT l.id, l.name AS title, l.slug, l.short_description,
		       b.featured_image_url, b.reading_time_mins, b.author_display_name,
		       COALESCE(ls.view_count, 0) AS view_count, l.created_at
		FROM listings l
		JOIN blogs b ON b.id = l.id
		LEFT JOIN listing_stats ls ON ls.listing_id = l.id
		WHERE l.entity_type = 'blog' AND l.status = 'LIVE'
		ORDER BY ls.view_count DESC NULLS LAST, l.created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("blog popular query failed: %w", err)
	}
	defer rows.Close()

	var blogs []map[string]interface{}
	for rows.Next() {
		var id, title, slug string
		var shortDesc, image, author sql.NullString
		var readingTime int
		var viewCount int64
		var createdAt time.Time
		rows.Scan(&id, &title, &slug, &shortDesc, &image, &readingTime, &author, &viewCount, &createdAt)
		blogs = append(blogs, map[string]interface{}{
			"id":                  id,
			"title":               title,
			"slug":                slug,
			"short_description":   shortDesc.String,
			"featured_image_url":  image.String,
			"reading_time_mins":   readingTime,
			"author_display_name": author.String,
			"view_count":          viewCount,
			"published_at":        createdAt.Format(time.RFC3339),
		})
	}
	if blogs == nil {
		blogs = []map[string]interface{}{}
	}

	elapsed := time.Since(start).Milliseconds()
	return map[string]interface{}{"popular_blogs": blogs}, len(blogs), elapsed, nil
}

// BlogDetail — Single Blog Page full data
// queryType: BLOG_DETAIL
// params: blogId
func BlogDetail(ctx context.Context, db *sql.DB, params map[string]interface{}, _ *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	blogID, ok := params["blogId"].(string)
	if !ok || blogID == "" {
		return nil, 0, 0, fmt.Errorf("blogId is required")
	}

	row := db.QueryRowContext(ctx, `
		SELECT
			l.id, l.name AS title, l.slug, l.short_description, l.description AS content,
			b.reading_time_mins, b.seo_title, b.seo_description,
			b.featured_image_url, b.author_display_name,
			COALESCE(array_to_json(b.tags), '[]'::json) AS tags,
			COALESCE(array_to_json(b.additional_media_urls), '[]'::json) AS additional_media_urls,
			COALESCE(ls.view_count, 0) AS view_count,
			l.created_at, l.created_by
		FROM listings l
		JOIN blogs b ON b.id = l.id
		LEFT JOIN listing_stats ls ON ls.listing_id = l.id
		WHERE l.entity_type = 'blog' AND l.status = 'LIVE' AND l.id = $1
	`, blogID)

	var (
		id, title, slug, content  string
		shortDesc                  sql.NullString
		readingTime                int
		seoTitle, seoDesc          sql.NullString
		imageURL, authorName       sql.NullString
		tagsJSON                   []byte
		mediaJSON                  []byte
		viewCount                  int64
		createdAt                  time.Time
		createdBy                  string
	)
	if err := row.Scan(&id, &title, &slug, &shortDesc, &content, &readingTime, &seoTitle, &seoDesc, &imageURL, &authorName, &tagsJSON, &mediaJSON, &viewCount, &createdAt, &createdBy); err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, 0, fmt.Errorf("blog not found")
		}
		return nil, 0, 0, fmt.Errorf("blog detail query failed: %w", err)
	}

	// Async view count increment
	go db.ExecContext(context.Background(), "UPDATE listing_stats SET view_count = view_count + 1 WHERE listing_id = $1", blogID)

	// Fetch categories
	catRows, _ := db.QueryContext(ctx, `
		SELECT c.id, c.name FROM categories c
		JOIN listing_categories lc ON lc.category_id = c.id
		WHERE lc.listing_id = $1
	`, blogID)
	defer catRows.Close()
	var cats []map[string]interface{}
	var catIDs []string
	for catRows.Next() {
		var cid, cname string
		catRows.Scan(&cid, &cname)
		cats = append(cats, map[string]interface{}{"id": cid, "name": cname})
		catIDs = append(catIDs, "'"+cid+"'")
	}

	var tags []string
	json.Unmarshal(tagsJSON, &tags)

	var additionalMedia []string
	json.Unmarshal(mediaJSON, &additionalMedia)

	// Related articles
	var related []map[string]interface{}
	if len(catIDs) > 0 {
		relQuery := fmt.Sprintf(`
			SELECT DISTINCT l.id, l.name, l.slug, l.short_description, b.featured_image_url, b.reading_time_mins
			FROM listings l
			JOIN blogs b ON b.id = l.id
			JOIN listing_categories lc ON lc.listing_id = l.id
			WHERE l.entity_type = 'blog' AND l.status = 'LIVE'
			  AND l.id != $1
			  AND lc.category_id IN (%s)
			LIMIT 4
		`, strings.Join(catIDs, ","))
		relRows, err := db.QueryContext(ctx, relQuery, blogID)
		if err == nil {
			defer relRows.Close()
			for relRows.Next() {
				var rid, rtitle, rslug string
				var rdesc, rimage sql.NullString
				var rtime int
				relRows.Scan(&rid, &rtitle, &rslug, &rdesc, &rimage, &rtime)
				related = append(related, map[string]interface{}{
					"id": rid, "title": rtitle, "slug": rslug,
					"short_description": rdesc.String, "featured_image_url": rimage.String,
					"reading_time_mins": rtime,
				})
			}
		}
	}
	if related == nil {
		related = []map[string]interface{}{}
	}

	elapsed := time.Since(start).Milliseconds()
	return map[string]interface{}{
		"id":                  id,
		"title":               title,
		"slug":                slug,
		"short_description":   shortDesc.String,
		"content":             content,
		"reading_time_mins":   readingTime,
		"seo_title":           seoTitle.String,
		"seo_description":     seoDesc.String,
		"featured_image_url":  imageURL.String,
		"author_display_name": authorName.String,
		"tags":                tags,
		"additional_media_urls": additionalMedia,
		"categories":          cats,
		"view_count":          viewCount,
		"published_at":        createdAt.Format(time.RFC3339),
		"related_articles":    related,
	}, 1, elapsed, nil
}
