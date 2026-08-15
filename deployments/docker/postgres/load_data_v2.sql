-- ============================================================
-- Clean import script for v2 database schema
-- Run this script using:
-- docker exec -i postgres psql -U postgres -d franchises < deployments/docker/postgres/load_data_v2.sql
-- ============================================================

-- Disable triggers temporarily for faster loading and bypassing constraint orders
SET session_replication_role = 'replica';

-- Truncate all tables first to avoid duplicates
TRUNCATE TABLE
    industry_market_insights,
    category_questions,
    franchise_operations,
    franchise_investment_requirement,
    franchise_business_overview,
    listing_cities,
    listing_social_links,
    listing_categories,
    listing_stats,
    master_franchises,
    associations,
    franchises,
    listings,
    sub_categories,
    categories,
    industries,
    author_profiles,
    users
CASCADE;

INSERT INTO users (id, email, name, phone, email_verified, status) 
VALUES ('00000000-0000-0000-0000-000000000001', 'admin@franchise.com', 'Admin User', '+1234567890', true, 'active')
ON CONFLICT (email) DO NOTHING;

INSERT INTO users (id, email, name, phone, email_verified, status)
VALUES ('6dc94a96-7c87-4882-a50d-0d78c7a07233', 'editorial@lemici.com', 'LeMiCi Editorial', '', true, 'active')
ON CONFLICT (email) DO NOTHING;

\echo 'Loading industries.csv...'
\COPY industries(id, name, slug, icon_name, icon_url, image_url, color_hex, color_name, listing_title, listing_description, association_listing_description, master_franchise_listing_description, display_order, is_active, is_featured, meta_title, meta_description, created_at, updated_at) FROM '/csv-data/v2-data/industries.csv' WITH (FORMAT csv, HEADER true, NULL '');

\echo 'Loading categories.csv...'
\COPY categories(id, industry_id, name, slug, icon_name, icon_url, image_url, description, display_order, is_active, meta_title, meta_description, created_at, updated_at) FROM '/csv-data/v2-data/categories.csv' WITH (FORMAT csv, HEADER true, NULL '');

\echo 'Loading sub_categories.csv...'
\COPY sub_categories(id, category_id, name, slug, description, display_order, is_active, created_at, updated_at) FROM '/csv-data/v2-data/sub_categories.csv' WITH (FORMAT csv, HEADER true, NULL 'NULL');

\echo 'Loading listings.csv...'
\COPY listings(id, name, slug, short_description, description, founded_year, contact_email, website_url, logo_url_circle, logo_url_square, created_by, updated_by, created_at, updated_at, entity_type, status, verified, trusted_seller) FROM '/csv-data/v2-data/listings.csv' WITH (FORMAT csv, HEADER true, NULL '', FORCE_NULL(short_description, description, founded_year, contact_email, website_url, logo_url_circle, logo_url_square, updated_by));

\echo 'Loading franchises.csv...'
\COPY franchises(id, total_outlets, parent_company, business_type, established_year, units_count, leader_name, leader_role) FROM '/csv-data/v2-data/franchises.csv' WITH (FORMAT csv, HEADER true, NULL '', FORCE_NULL(parent_company, business_type, established_year, total_outlets, units_count, leader_name, leader_role));

\echo 'Loading blogs.csv...'
\COPY blogs(id, reading_time_mins, seo_title, seo_description, featured_image_url, author_display_name, tags, additional_media_urls) FROM '/csv-data/v2-data/blogs.csv' WITH (FORMAT csv, HEADER true, NULL '', FORCE_NULL(seo_title, seo_description, author_display_name));

\echo 'Loading author_profiles.csv...'
\COPY author_profiles(user_id, full_name, author_name, bio, profile_picture_url, categories, created_at, updated_at) FROM '/csv-data/v2-data/author_profiles.csv' WITH (FORMAT csv, HEADER true, NULL '', FORCE_NULL(bio, profile_picture_url));

\echo 'Loading associations.csv...'
\COPY associations(id, association_type, sector_represented, member_count, membership_fee_min, membership_fee_max, industry_id, association_metadata) FROM '/csv-data/v2-data/associations.csv' WITH (FORMAT csv, HEADER true, NULL '', FORCE_NULL(sector_represented, member_count, membership_fee_min, membership_fee_max, industry_id, association_metadata));

\echo 'Loading master_franchises.csv...'
\COPY master_franchises(id, territory_rights, sub_franchise_fee_split, master_fee, min_sub_franchises_required) FROM '/csv-data/v2-data/master_franchises.csv' WITH (FORMAT csv, HEADER true, NULL '');

\echo 'Loading listing_stats.csv...'
\COPY listing_stats(id, listing_id, rating, rating_count, follow_count, likes_count, view_count, save_count, share_count, enquiry_count, news_count, created_at, updated_at) FROM '/csv-data/v2-data/listing_stats.csv' WITH (FORMAT csv, HEADER true, NULL '', FORCE_NULL(rating));

\echo 'Loading listing_categories.csv...'
\COPY listing_categories(id, listing_id, category_id, sub_category_id, is_primary, created_at) FROM '/csv-data/v2-data/listing_categories.csv' WITH (FORMAT csv, HEADER true, NULL '');

\echo 'Loading listing_social_links.csv...'
\COPY listing_social_links(id, listing_id, instagram_url, facebook_url, twitter_url, linkedin_url, youtube_url, created_at, updated_at) FROM '/csv-data/v2-data/listing_social_links.csv' WITH (FORMAT csv, HEADER true, NULL '');

\echo 'Loading franchise_cities.csv as listing_cities...'
\COPY listing_cities(id, listing_id, city, state, country, created_at) FROM '/csv-data/v2-data/franchise_cities.csv' WITH (FORMAT csv, HEADER true, NULL '');

\echo 'Loading franchise_business_overview.csv...'
\COPY franchise_business_overview(id, franchise_id, products, services, created_by, updated_by, created_at, updated_at) FROM '/csv-data/v2-data/franchise_business_overview.csv' WITH (FORMAT csv, HEADER true, NULL '', FORCE_NULL(updated_by));

\echo 'Loading franchise_investment_requirement.csv...'
\COPY franchise_investment_requirement(id, franchise_id, initial_investment_min, initial_investment_max, franchise_fee, royalty_percentage, marketing_fee_percentage, payback_min_months, payback_max_months, roi_min_percentage, roi_max_percentage, monthly_turnover_min, monthly_turnover_max, single_unit_cost_min, single_unit_cost_max, investment_includes, created_by, updated_by, created_at, updated_at, revenue_model) FROM '/csv-data/v2-data/franchise_investment_requirement.csv' WITH (FORMAT csv, HEADER true, NULL '', FORCE_NULL(initial_investment_min, initial_investment_max, franchise_fee, royalty_percentage, marketing_fee_percentage, payback_min_months, payback_max_months, roi_min_percentage, roi_max_percentage, monthly_turnover_min, monthly_turnover_max, single_unit_cost_min, single_unit_cost_max, investment_includes, revenue_model, updated_by));

\echo 'Loading franchise_operations.csv...'
\COPY franchise_operations(id, franchise_id, space_min_sqft, space_max_sqft, required_property_type, staff_required_min, staff_required_max, staff_breakdown, operating_hours, training_provided, training_details, computer_requirements, marketing_support, preferred_locations, qualification_required, supply_chain_support, quality_control, created_by, updated_by, created_at, updated_at, territory_details, development_schedule, support_training, legal_compliance) FROM '/csv-data/v2-data/franchise_operations.csv' WITH (FORMAT csv, HEADER true, NULL '', FORCE_NULL(required_property_type, operating_hours, training_details, computer_requirements, marketing_support, preferred_locations, qualification_required, updated_by));

\echo 'Loading category_questions.csv...'
\COPY category_questions(id, reference_id, entity_type, question, intent_tag, created_at) FROM '/csv-data/v2-data/category_questions.csv' WITH (FORMAT csv, HEADER true, NULL '');

\echo 'Loading industry_market_insights.csv...'
\COPY industry_market_insights(id, industry_id, industry_slug, entity_type, intent_tag, growth_rate_title, growth_rate_description, market_trend_title, market_trend_description, created_at, updated_at) FROM '/csv-data/v2-data/industry_market_insights.csv' WITH (FORMAT csv, HEADER true, NULL '');

-- Re-enable triggers
SET session_replication_role = 'origin';

\echo '=========================================='
\echo '📊 DATA LOADING VERIFICATION'
\echo '=========================================='

SELECT 'Industries' as table_name, COUNT(*) as record_count FROM industries
UNION ALL SELECT 'Categories', COUNT(*) FROM categories
UNION ALL SELECT 'Sub-Categories', COUNT(*) FROM sub_categories
UNION ALL SELECT 'Listings', COUNT(*) FROM listings
UNION ALL SELECT 'Franchises', COUNT(*) FROM franchises
UNION ALL SELECT 'Blogs', COUNT(*) FROM blogs
UNION ALL SELECT 'Author Profiles', COUNT(*) FROM author_profiles
UNION ALL SELECT 'Associations', COUNT(*) FROM associations
UNION ALL SELECT 'Master Franchises', COUNT(*) FROM master_franchises
UNION ALL SELECT 'Listing Categories', COUNT(*) FROM listing_categories
UNION ALL SELECT 'Listing Stats', COUNT(*) FROM listing_stats
UNION ALL SELECT 'Listing Cities', COUNT(*) FROM listing_cities
UNION ALL SELECT 'Listing Social Links', COUNT(*) FROM listing_social_links
UNION ALL SELECT 'Business Overview', COUNT(*) FROM franchise_business_overview
UNION ALL SELECT 'Investment Requirements', COUNT(*) FROM franchise_investment_requirement
UNION ALL SELECT 'Operations', COUNT(*) FROM franchise_operations
UNION ALL SELECT 'Category Questions', COUNT(*) FROM category_questions
UNION ALL SELECT 'Industry Market Insights', COUNT(*) FROM industry_market_insights;

\echo ''
\echo 'Setting blogs as featured per requirement...'
UPDATE listings SET is_featured = TRUE WHERE entity_type = 'blog' AND status = 'live';

\echo ''
\echo '✅ CSV data loading completed successfully!'
\echo ''
