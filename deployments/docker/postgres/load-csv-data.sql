-- ============================================================
-- CSV DATA LOADING SCRIPT
-- This script loads CSV data into the franchises database
-- Place this file in: deployments/docker/postgres/load-csv-data.sql
-- ============================================================

\c franchises

-- Disable triggers temporarily for faster loading
SET session_replication_role = 'replica';

-- ========================================
-- LOAD INDUSTRIES
-- ========================================
\echo 'Loading industries data...'
COPY industries(
    id, name, slug, icon_name, icon_url, color_hex, color_name,
    listing_title, listing_description, display_order, is_active, is_featured,
    meta_title, meta_description
)
FROM '/csv-data/industries.csv'
DELIMITER ',' CSV HEADER;

\echo '✅ Industries loaded'

-- ========================================
-- LOAD CATEGORIES
-- ========================================
\echo 'Loading categories data...'
COPY categories(
    id, industry_id, name, slug, icon_name, icon_url, description,
    display_order, is_active, meta_title, meta_description
)
FROM '/csv-data/categories.csv'
DELIMITER ',' CSV HEADER;

\echo '✅ Categories loaded'

-- ========================================
-- LOAD SUB-CATEGORIES
-- ========================================
\echo 'Loading sub-categories data...'
COPY sub_categories(
    id, category_id, name, slug, description, display_order, is_active
)
FROM '/csv-data/sub_categories.csv'
DELIMITER ',' CSV HEADER;

\echo '✅ Sub-categories loaded'

-- ========================================
-- LOAD FRANCHISES
-- ========================================
\echo 'Loading franchises data...'
COPY franchises(
    id, name, slug, short_description, description, founded_year,
    trusted_seller, verified, total_outlets, outlet_range, parent_company,
    business_type, established_year, units_count, leader_name, leader_role,
    contact_email, logo_url, created_by, updated_by
)
FROM '/csv-data/franchises.csv'
DELIMITER ',' CSV HEADER;

\echo '✅ Franchises loaded'

-- ========================================
-- LOAD FRANCHISE CATEGORIES (JUNCTION)
-- ========================================
\echo 'Loading franchise categories mapping...'
COPY franchise_categories(
    id, franchise_id, category_id, sub_category_id, is_primary
)
FROM '/csv-data/franchise_categories.csv'
DELIMITER ',' CSV HEADER;

\echo '✅ Franchise categories mapping loaded'

-- ========================================
-- LOAD FRANCHISE STATS
-- ========================================
\echo 'Loading franchise stats...'
COPY franchise_stats(
    id, franchise_id, rating, rating_count, follow_count, likes_count,
    view_count, save_count, share_count, enquiry_count, news_count
)
FROM '/csv-data/franchise_stats.csv'
DELIMITER ',' CSV HEADER;

\echo '✅ Franchise stats loaded'

-- ========================================
-- LOAD FRANCHISE CITIES
-- ========================================
\echo 'Loading franchise cities...'
COPY franchise_cities(
    id, franchise_id, city, state, country
)
FROM '/csv-data/franchise_cities.csv'
DELIMITER ',' CSV HEADER;

\echo '✅ Franchise cities loaded'

-- ========================================
-- LOAD FRANCHISE BUSINESS OVERVIEW
-- ========================================
\echo 'Loading franchise business overview...'
COPY franchise_business_overview(
    id, franchise_id, products, services, created_by, updated_by
)
FROM '/csv-data/franchise_business_overview.csv'
DELIMITER ',' CSV HEADER;

\echo '✅ Franchise business overview loaded'

-- ========================================
-- LOAD FRANCHISE INVESTMENT REQUIREMENTS
-- ========================================
\echo 'Loading franchise investment requirements...'
COPY franchise_investment_requirement(
    id, franchise_id, initial_investment_min, initial_investment_max,
    franchise_fee, royalty_percentage, marketing_fee_percentage,
    payback_min_months, payback_max_months, roi_min_percentage, roi_max_percentage,
    monthly_turnover_min, monthly_turnover_max, single_unit_cost_min,
    single_unit_cost_max, investment_includes, created_by, updated_by
)
FROM '/csv-data/franchise_investment_requirement.csv'
DELIMITER ',' CSV HEADER;

\echo '✅ Franchise investment requirements loaded'

-- ========================================
-- LOAD FRANCHISE OPERATIONS
-- ========================================
\echo 'Loading franchise operations...'
COPY franchise_operations(
    id, franchise_id, space_min_sqft, space_max_sqft, required_property_type,
    staff_required_min, staff_required_max, staff_breakdown, operating_hours,
    training_provided, training_details, computer_requirements, marketing_support,
    preferred_locations, qualification_required, supply_chain_support,
    quality_control, created_by, updated_by
)
FROM '/csv-data/franchise_operations.csv'
DELIMITER ',' CSV HEADER;

\echo '✅ Franchise operations loaded'

-- ========================================
-- LOAD FRANCHISE SOCIAL LINKS
-- ========================================
\echo 'Loading franchise social links...'
COPY franchise_social_links(
    id, franchise_id, instagram_url, facebook_url, twitter_url, linkedin_url
)
FROM '/csv-data/franchise_social_links.csv'
DELIMITER ',' CSV HEADER;

\echo '✅ Franchise social links loaded'

-- ========================================
-- LOAD CATEGORY QUESTIONS
-- ========================================
\echo 'Loading category questions...'
COPY category_questions(
    id, reference_id, question
)
FROM '/csv-data/category_questions.csv'
DELIMITER ',' CSV HEADER;

\echo '✅ Category questions loaded'

-- Re-enable triggers
SET session_replication_role = 'origin';

-- ========================================
-- VERIFY DATA LOADED
-- ========================================
\echo ''
\echo '=========================================='
\echo '📊 DATA LOADING VERIFICATION'
\echo '=========================================='

SELECT 
    'Industries' as table_name, 
    COUNT(*) as record_count 
FROM industries
UNION ALL
SELECT 'Categories', COUNT(*) FROM categories
UNION ALL
SELECT 'Sub-Categories', COUNT(*) FROM sub_categories
UNION ALL
SELECT 'Franchises', COUNT(*) FROM franchises
UNION ALL
SELECT 'Franchise Categories', COUNT(*) FROM franchise_categories
UNION ALL
SELECT 'Franchise Stats', COUNT(*) FROM franchise_stats
UNION ALL
SELECT 'Franchise Cities', COUNT(*) FROM franchise_cities
UNION ALL
SELECT 'Business Overview', COUNT(*) FROM franchise_business_overview
UNION ALL
SELECT 'Investment Requirements', COUNT(*) FROM franchise_investment_requirement
UNION ALL
SELECT 'Operations', COUNT(*) FROM franchise_operations
UNION ALL
SELECT 'Social Links', COUNT(*) FROM franchise_social_links
UNION ALL
SELECT 'Category Questions', COUNT(*) FROM category_questions;

\echo ''
\echo '✅ CSV data loading completed successfully!'
\echo ''