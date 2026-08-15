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

// BlogBySlug — Resolves a blog slug to its UUID
// queryType: BLOG_BY_SLUG
// params: slug
// Returns: blogId (UUID string)
func BlogBySlug(ctx context.Context, db *sql.DB, params map[string]interface{}, _ *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	slug, ok := params["slug"].(string)
	if !ok || slug == "" {
		return nil, 0, 0, fmt.Errorf("slug is required")
	}

	var id string
	err := db.QueryRowContext(ctx, `
		SELECT l.id
		FROM listings l
		WHERE l.entity_type = 'blog' AND l.status = 'live' AND l.slug = $1
		LIMIT 1
	`, slug).Scan(&id)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, 0, fmt.Errorf("blog not found for slug: %s", slug)
		}
		return nil, 0, 0, fmt.Errorf("slug resolution failed: %w", err)
	}

	elapsed := time.Since(start).Milliseconds()
	return map[string]interface{}{
		"blogId": id,
	}, 1, elapsed, nil
}

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
		WHERE l.entity_type = 'blog' AND l.status = 'live'
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
		       b.featured_image_url, b.reading_time_mins, b.author_display_name,
		       ap.profile_picture_url AS author_profile_pic,
		       COALESCE(json_agg(DISTINCT jsonb_build_object('id', c.id, 'name', c.name)) FILTER (WHERE c.id IS NOT NULL), '[]'::json) AS categories,
		       l.created_at
		FROM listings l
		JOIN blogs b ON b.id = l.id
		LEFT JOIN listing_categories lc ON lc.listing_id = l.id
		LEFT JOIN categories c ON c.id = lc.category_id
		LEFT JOIN author_profiles ap ON ap.user_id = l.created_by
		WHERE l.entity_type = 'blog' AND l.status = 'live' AND l.is_featured = TRUE
		GROUP BY l.id, b.id, ap.profile_picture_url
		ORDER BY MAX(l.featured_order) ASC NULLS LAST, MAX(l.created_at) DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("blog featured query failed: %w", err)
	}
	defer rows.Close()

	var blogs []map[string]interface{}
	for rows.Next() {
		var id, title, slug string
		var shortDesc, image, author, authorPic sql.NullString
		var readingTime int
		var createdAt time.Time
		var catsJSON []byte
		rows.Scan(&id, &title, &slug, &shortDesc, &image, &readingTime, &author, &authorPic, &catsJSON, &createdAt)
		var cats []interface{}
		json.Unmarshal(catsJSON, &cats)
		blogs = append(blogs, map[string]interface{}{
			"id":                  id,
			"title":               title,
			"slug":                slug,
			"short_description":   shortDesc.String,
			"featured_image_url":  image.String,
			"reading_time_mins":   readingTime,
			"author_display_name": author.String,
			"author_profile_pic":  authorPic.String,
			"categories":          cats,
			"published_at":        createdAt.Format(time.RFC3339),
		})
	}
	if blogs == nil {
		blogs = []map[string]interface{}{}
	}

	elapsed := time.Since(start).Milliseconds()
	return blogs, len(blogs), elapsed, nil
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
		       ap.profile_picture_url AS author_profile_pic,
		       COALESCE(ls.view_count, 0) AS view_count,
		       l.created_at
		FROM listings l
		JOIN blogs b ON b.id = l.id
		LEFT JOIN listing_stats ls ON ls.listing_id = l.id
		LEFT JOIN author_profiles ap ON ap.user_id = l.created_by
		WHERE l.entity_type = 'blog' AND l.status = 'live'
		GROUP BY l.id, b.id, ls.view_count, ap.profile_picture_url
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
		var shortDesc, image, author, authorPic sql.NullString
		var readingTime int
		var viewCount int64
		var createdAt time.Time
		rows.Scan(&id, &title, &slug, &shortDesc, &image, &readingTime, &author, &authorPic, &viewCount, &createdAt)
		blogs = append(blogs, map[string]interface{}{
			"id":                  id,
			"title":               title,
			"slug":                slug,
			"short_description":   shortDesc.String,
			"featured_image_url":  image.String,
			"reading_time_mins":   readingTime,
			"author_display_name": author.String,
			"author_profile_pic":  authorPic.String,
			"published_at":        createdAt.Format(time.RFC3339),
		})
	}
	if blogs == nil {
		blogs = []map[string]interface{}{}
	}

	elapsed := time.Since(start).Milliseconds()
	return blogs, len(blogs), elapsed, nil
}

// BlogHero — Single Blog Page: Hero Section (Title, Author, Date, Tags, Categories, etc)
// queryType: BLOG_HERO
// params: blogId (UUID)
func BlogHero(ctx context.Context, db *sql.DB, params map[string]interface{}, _ *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	blogID, ok := params["blogId"].(string)
	if !ok || blogID == "" {
		return nil, 0, 0, fmt.Errorf("blogId is required")
	}

	row := db.QueryRowContext(ctx, `
		SELECT
			l.id, l.name AS title, l.slug, l.short_description,
			b.reading_time_mins, b.seo_title, b.seo_description,
			b.featured_image_url, b.author_display_name,
			COALESCE(array_to_json(b.tags), '[]'::json) AS tags,
			COALESCE(array_to_json(b.additional_media_urls), '[]'::json) AS additional_media_urls,
			COALESCE(ls.view_count, 0) AS view_count,
			l.created_at, l.created_by,
			COALESCE(
				json_agg(DISTINCT jsonb_build_object('id', c.id, 'name', c.name)) FILTER (WHERE c.id IS NOT NULL),
				'[]'::json
			) AS categories
		FROM listings l
		JOIN blogs b ON b.id = l.id
		LEFT JOIN listing_stats ls ON ls.listing_id = l.id
		LEFT JOIN listing_categories lc ON lc.listing_id = l.id
		LEFT JOIN categories c ON c.id = lc.category_id
		WHERE l.entity_type = 'blog' AND l.status = 'live' AND l.id = $1
		GROUP BY l.id, b.id, ls.view_count
	`, blogID)

	var (
		id, title, blogSlug       string
		shortDesc                 sql.NullString
		readingTime               int
		seoTitle, seoDesc         sql.NullString
		imageURL, authorName      sql.NullString
		tagsJSON                  []byte
		mediaJSON                 []byte
		viewCount                 int64
		createdAt                 time.Time
		createdBy                 string
		catsJSON                  []byte
	)
	if err := row.Scan(&id, &title, &blogSlug, &shortDesc, &readingTime, &seoTitle, &seoDesc, &imageURL, &authorName, &tagsJSON, &mediaJSON, &viewCount, &createdAt, &createdBy, &catsJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, 0, fmt.Errorf("blog not found")
		}
		return nil, 0, 0, fmt.Errorf("blog hero query failed: %w", err)
	}

	// Async view count increment
	go db.ExecContext(context.Background(), "UPDATE listing_stats SET view_count = view_count + 1 WHERE listing_id = $1", blogID)

	var tags []string
	json.Unmarshal(tagsJSON, &tags)
	var additionalMedia []string
	json.Unmarshal(mediaJSON, &additionalMedia)
	var cats []interface{}
	json.Unmarshal(catsJSON, &cats)

	elapsed := time.Since(start).Milliseconds()
	return map[string]interface{}{
		"id":                    id,
		"title":                 title,
		"slug":                  blogSlug,
		"short_description":     shortDesc.String,
		"reading_time_mins":     readingTime,
		"seo_title":             seoTitle.String,
		"seo_description":       seoDesc.String,
		"featured_image_url":    imageURL.String,
		"author_display_name":   authorName.String,
		"tags":                  tags,
		"additional_media_urls": additionalMedia,
		"categories":            cats,
		"view_count":            viewCount,
		"published_at":          createdAt.Format(time.RFC3339),
	}, 1, elapsed, nil
}

// BlogContent — Single Blog Page: Main HTML Content
// queryType: BLOG_CONTENT
// params: blogId (UUID)
func BlogContent(ctx context.Context, db *sql.DB, params map[string]interface{}, _ *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	blogID, ok := params["blogId"].(string)
	if !ok || blogID == "" {
		return nil, 0, 0, fmt.Errorf("blogId is required")
	}

	row := db.QueryRowContext(ctx, `
		SELECT COALESCE(b.content, '') AS content
		FROM listings l
		JOIN blogs b ON b.id = l.id
		WHERE l.entity_type = 'blog' AND l.status = 'live' AND l.id = $1
	`, blogID)

	var content string
	if err := row.Scan(&content); err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, 0, fmt.Errorf("blog content not found")
		}
		return nil, 0, 0, fmt.Errorf("blog content query failed: %w", err)
	}

	elapsed := time.Since(start).Milliseconds()
	return map[string]interface{}{
		"content": content,
	}, 1, elapsed, nil
}

// BlogRelatedArticles — Sidebar "Related Articles" section on Blog Detail Page
// queryType: BLOG_RELATED_ARTICLES
// params: blogId (UUID), limit (default 4)
// Fetches blogs in the same categories as the given blog, excluding itself
func BlogRelatedArticles(ctx context.Context, db *sql.DB, params map[string]interface{}, _ *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	blogID, ok := params["blogId"].(string)
	if !ok || blogID == "" {
		return nil, 0, 0, fmt.Errorf("blogId is required")
	}
	limit := 4
	if l, ok := params["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	// First get category IDs for this blog
	catRows, err := db.QueryContext(ctx, `
		SELECT category_id FROM listing_categories WHERE listing_id = $1
	`, blogID)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("related articles: failed to get categories: %w", err)
	}
	defer catRows.Close()

	var catIDs []string
	for catRows.Next() {
		var cid string
		catRows.Scan(&cid)
		catIDs = append(catIDs, "'"+cid+"'")
	}

	var related []map[string]interface{}
	if len(catIDs) > 0 {
		relQuery := fmt.Sprintf(`
			SELECT DISTINCT l.id, l.name, l.slug, l.short_description,
			       b.featured_image_url, b.reading_time_mins, b.author_display_name, l.created_at
			FROM listings l
			JOIN blogs b ON b.id = l.id
			JOIN listing_categories lc ON lc.listing_id = l.id
			WHERE l.entity_type = 'blog' AND l.status = 'live'
			  AND l.id != $1
			  AND lc.category_id IN (%s)
			ORDER BY l.created_at DESC
			LIMIT $2
		`, strings.Join(catIDs, ","))

		relRows, err := db.QueryContext(ctx, relQuery, blogID, limit)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("related articles query failed: %w", err)
		}
		defer relRows.Close()
		for relRows.Next() {
			var rid, rtitle, rslug string
			var rdesc, rimage, rauthor sql.NullString
			var rtime int
			var rcreatedAt time.Time
			relRows.Scan(&rid, &rtitle, &rslug, &rdesc, &rimage, &rtime, &rauthor, &rcreatedAt)
			related = append(related, map[string]interface{}{
				"id":                  rid,
				"title":               rtitle,
				"slug":                rslug,
				"short_description":   rdesc.String,
				"featured_image_url":  rimage.String,
				"reading_time_mins":   rtime,
				"author_display_name": rauthor.String,
				"published_at":        rcreatedAt.Format(time.RFC3339),
			})
		}
	}
	if related == nil {
		related = []map[string]interface{}{}
	}

	elapsed := time.Since(start).Milliseconds()
	return related, len(related), elapsed, nil
}

// BlogAuthorProfile — "About the Author" section on Blog Detail Page
// queryType: BLOG_AUTHOR_PROFILE
// params: blogId (UUID)
func BlogAuthorProfile(ctx context.Context, db *sql.DB, params map[string]interface{}, _ *crypto.Encryptor) (interface{}, int, int64, error) {
	start := time.Now()

	blogID, ok := params["blogId"].(string)
	if !ok || blogID == "" {
		return nil, 0, 0, fmt.Errorf("blogId is required")
	}

	row := db.QueryRowContext(ctx, `
		SELECT
			ap.user_id,
			ap.full_name,
			ap.author_name,
			ap.bio,
			ap.profile_picture_url,
			COALESCE(array_to_json(ap.categories), '[]'::json) AS categories,
			COALESCE(
				(SELECT COUNT(*) FROM blog_author_followers WHERE author_id = ap.user_id),
				0
			) AS follower_count
		FROM listings l
		JOIN author_profiles ap ON ap.user_id = l.created_by
		WHERE l.id = $1 AND l.entity_type = 'blog'
	`, blogID)

	var (
		userID, fullName, authorName string
		bio, picURL                  sql.NullString
		catsJSON                     []byte
		followerCount                int64
	)
	if err := row.Scan(&userID, &fullName, &authorName, &bio, &picURL, &catsJSON, &followerCount); err != nil {
		if err == sql.ErrNoRows {
			// No author profile found — return minimal data
			elapsed := time.Since(start).Milliseconds()
			return map[string]interface{}{}, 0, elapsed, nil
		}
		return nil, 0, 0, fmt.Errorf("author profile query failed: %w", err)
	}

	var cats []string
	json.Unmarshal(catsJSON, &cats)

	elapsed := time.Since(start).Milliseconds()
	return map[string]interface{}{
		"user_id":             userID,
		"full_name":           fullName,
		"author_name":         authorName,
		"bio":                 bio.String,
		"profile_picture_url": picURL.String,
		"categories":          cats,
		"follower_count":      followerCount,
	}, 1, elapsed, nil
}
