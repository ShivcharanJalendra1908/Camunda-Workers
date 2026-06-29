-- ============================================================
-- CSV DATA LOADING SCRIPT (Updated for new_extracted CSVs)
-- This script loads CSV data into the franchises database
-- Place CSVs in a Docker-mounted path (e.g., /csv-data/)
-- ============================================================

-- Disable triggers temporarily for faster loading
SET session_replication_role = 'replica';

-- ========================================
-- TRUNCATE TABLES (Clean start)
-- ========================================
\echo 'Cleaning existing data...'
TRUNCATE
    franchise_social_links,
    franchise_operations,
    franchise_investment_requirement,
    franchise_business_overview,
    franchise_cities,
    franchise_stats,
    franchise_categories,
    category_questions,
    industry_market_insights,
    franchises,
    sub_categories,
    categories,
    industries
CASCADE;

\echo '✅ Tables truncated'

-- ========================================
-- LOAD INDUSTRIES (21 rows)
-- CSV: id,name,slug,icon_name,icon_url,image_url,color_hex,color_name,
--      listing_title,listing_description,display_order,is_active,is_featured,
--      meta_title,meta_description,created_at,updated_at
-- ========================================
\echo 'Loading industries data...'
COPY industries(
    id, name, slug, icon_name, icon_url, image_url, color_hex, color_name,
    listing_title, listing_description, association_listing_description, master_franchise_listing_description,
    display_order, is_active, is_featured, meta_title, meta_description, created_at, updated_at
)
FROM '/csv-data/industries.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL ''
);

\echo '✅ Industries loaded'

-- ========================================
-- LOAD CATEGORIES (112 rows)
-- CSV: id,industry_id,name,slug,icon_name,icon_url,image_url,description,
--      display_order,is_active,meta_title,meta_description,created_at,updated_at
-- ========================================
\echo 'Loading categories data...'
COPY categories(
    id, industry_id, name, slug, icon_name, icon_url, image_url, description,
    display_order, is_active, meta_title, meta_description, created_at, updated_at
)
FROM '/csv-data/categories.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL ''
);

\echo '✅ Categories loaded'

-- ========================================
-- LOAD SUB-CATEGORIES (282 rows)
-- CSV: id,category_id,name,slug,description,display_order,is_active,
--      created_at,updated_at
-- ========================================
\echo 'Loading sub-categories data...'
COPY sub_categories(
    id, category_id, name, slug, description, display_order, is_active,
    created_at, updated_at
)
FROM '/csv-data/sub_categories.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL 'NULL'
);

\echo '✅ Sub-categories loaded'

-- ========================================
-- LOAD INDUSTRY MARKET INSIGHTS (45 rows)
-- CSV: id,industry_id,industry_slug,entity_type,intent_tag,
--      growth_rate_title,growth_rate_description,
--      market_trend_title,market_trend_description,
--      created_at,updated_at
-- ========================================
\echo 'Loading industry market insights...'
COPY industry_market_insights(
    id, industry_id, industry_slug, entity_type, intent_tag,
    growth_rate_title, growth_rate_description,
    market_trend_title, market_trend_description,
    created_at, updated_at
)
FROM '/csv-data/industry_market_insights.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL ''
);

\echo '✅ Industry market insights loaded'

-- ========================================
-- LOAD FRANCHISES (1433 rows)
-- CSV: id,name,slug,short_description,description,founded_year,
--      trusted_seller,verified,total_outlets,outlet_range,parent_company,
--      business_type,established_year,units_count,leader_name,leader_role,
--      contact_email,logo_url_circle,logo_url_square,created_by,updated_by,
--      created_at,updated_at,entity_type,status,association_metadata,
--      member_count,membership_fee_min,membership_fee_max,approved_at
-- ========================================
\echo 'Loading franchises data...'
COPY franchises(
    id, name, slug, short_description, description, founded_year,
    trusted_seller, verified, total_outlets, outlet_range, parent_company,
    business_type, established_year, units_count, leader_name, leader_role,
    contact_email, logo_url_circle, logo_url_square, created_by, updated_by,
    created_at, updated_at, entity_type, status, association_metadata,
    member_count, membership_fee_min, membership_fee_max, approved_at
)
FROM '/csv-data/franchises.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL ''
);

\echo '✅ Franchises loaded'

-- ========================================
-- LOAD FRANCHISE CATEGORIES (1433 rows)
-- CSV: id,franchise_id,category_id,sub_category_id,is_primary,created_at
-- ========================================
\echo 'Loading franchise categories mapping...'
COPY franchise_categories(
    id, franchise_id, category_id, sub_category_id, is_primary, created_at
)
FROM '/csv-data/franchise_categories.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL ''
);

\echo '✅ Franchise categories mapping loaded'

-- ========================================
-- LOAD FRANCHISE STATS (1432 rows)
-- CSV: id,franchise_id,rating,rating_count,follow_count,likes_count,
--      view_count,save_count,share_count,enquiry_count,news_count,
--      created_at,updated_at
-- ========================================
\echo 'Loading franchise stats...'
COPY franchise_stats(
    id, franchise_id, rating, rating_count, follow_count, likes_count,
    view_count, save_count, share_count, enquiry_count, news_count,
    created_at, updated_at
)
FROM '/csv-data/franchise_stats.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL ''
);

\echo '✅ Franchise stats loaded'

-- ========================================
-- LOAD FRANCHISE CITIES (5041 rows)
-- CSV: id,franchise_id,city,state,country,created_at
-- ========================================
\echo 'Loading franchise cities...'
COPY franchise_cities(
    id, franchise_id, city, state, country, created_at
)
FROM '/csv-data/franchise_cities.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL ''
);

\echo '✅ Franchise cities loaded'

-- ========================================
-- LOAD FRANCHISE BUSINESS OVERVIEW (1432 rows)
-- CSV: id,franchise_id,products,services,created_by,updated_by,
--      created_at,updated_at
-- ========================================
\echo 'Loading franchise business overview...'
COPY franchise_business_overview(
    id, franchise_id, products, services, created_by, updated_by,
    created_at, updated_at
)
FROM '/csv-data/franchise_business_overview.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL ''
);

\echo '✅ Franchise business overview loaded'

-- ========================================
-- LOAD FRANCHISE INVESTMENT REQUIREMENTS (1433 rows)
-- CSV: id,franchise_id,initial_investment_min,initial_investment_max,
--      franchise_fee,royalty_percentage,marketing_fee_percentage,
--      payback_min_months,payback_max_months,roi_min_percentage,
--      roi_max_percentage,monthly_turnover_min,monthly_turnover_max,
--      single_unit_cost_min,single_unit_cost_max,investment_includes,
--      created_by,updated_by,created_at,updated_at,revenue_model
-- ========================================
\echo 'Loading franchise investment requirements...'
COPY franchise_investment_requirement(
    id, franchise_id, initial_investment_min, initial_investment_max,
    franchise_fee, royalty_percentage, marketing_fee_percentage,
    payback_min_months, payback_max_months, roi_min_percentage,
    roi_max_percentage, monthly_turnover_min, monthly_turnover_max,
    single_unit_cost_min, single_unit_cost_max, investment_includes,
    created_by, updated_by, created_at, updated_at, revenue_model
)
FROM '/csv-data/franchise_investment_requirement.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL ''
);

\echo '✅ Franchise investment requirements loaded'

-- ========================================
-- LOAD FRANCHISE OPERATIONS (1433 rows)
-- CSV: id,franchise_id,space_min_sqft,space_max_sqft,required_property_type,
--      staff_required_min,staff_required_max,staff_breakdown,operating_hours,
--      training_provided,training_details,computer_requirements,marketing_support,
--      preferred_locations,qualification_required,supply_chain_support,
--      quality_control,created_by,updated_by,created_at,updated_at,
--      territory_details,development_schedule,support_training,legal_compliance
-- ========================================
\echo 'Loading franchise operations...'
COPY franchise_operations(
    id, franchise_id, space_min_sqft, space_max_sqft, required_property_type,
    staff_required_min, staff_required_max, staff_breakdown, operating_hours,
    training_provided, training_details, computer_requirements, marketing_support,
    preferred_locations, qualification_required, supply_chain_support,
    quality_control, created_by, updated_by, created_at, updated_at,
    territory_details, development_schedule, support_training, legal_compliance
)
FROM '/csv-data/franchise_operations.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL 'NULL'
);

\echo '✅ Franchise operations loaded'

-- ========================================
-- LOAD FRANCHISE SOCIAL LINKS (1143 rows)
-- CSV: id,franchise_id,instagram_url,facebook_url,twitter_url,linkedin_url,
--      created_at,updated_at
-- ========================================
\echo 'Loading franchise social links...'
COPY franchise_social_links(
    id, franchise_id, instagram_url, facebook_url, twitter_url, linkedin_url,
    created_at, updated_at
)
FROM '/csv-data/franchise_social_links.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL ''
);

\echo '✅ Franchise social links loaded'

-- ========================================
-- LOAD CATEGORY QUESTIONS (359 rows)
-- CSV: id,reference_id,entity_type,question,intent_tag,created_at
-- ========================================
\echo 'Loading category questions...'
COPY category_questions(
    id, reference_id, entity_type, question, intent_tag, created_at
)
FROM '/csv-data/category_questions.csv'
WITH (
    FORMAT csv,
    HEADER true,
    DELIMITER ',',
    QUOTE '"',
    ESCAPE '"',
    NULL ''
);

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
SELECT 'Category Questions', COUNT(*) FROM category_questions
UNION ALL
SELECT 'Industry Market Insights', COUNT(*) FROM industry_market_insights;


\echo ''
\echo '✅ CSV data loading completed successfully!'
\echo ''
