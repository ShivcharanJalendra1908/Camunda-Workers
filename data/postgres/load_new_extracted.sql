-- ============================================================
-- SQL Script to Load Fixed CSV Data from /tmp/ into franchises DB
-- ============================================================

-- Disable triggers temporarily for faster loading
SET session_replication_role = 'replica';

-- Truncate existing data to start clean
\echo 'Cleaning existing data...'
TRUNCATE 
    industries, categories, sub_categories, 
    franchises, franchise_categories, franchise_stats, 
    franchise_cities, franchise_business_overview, 
    franchise_investment_requirement, franchise_operations, 
    franchise_social_links, category_questions
CASCADE;

-- Load data matching CSV columns exactly to schema
\echo 'Loading industries...'
COPY industries(id, name, slug, icon_name, icon_url, image_url, color_hex, color_name, listing_title, listing_description, display_order, is_active, is_featured, meta_title, meta_description, created_at, updated_at) 
FROM '/tmp/industries.csv' DELIMITER ',' CSV HEADER;

\echo 'Loading categories...'
COPY categories(id, industry_id, name, slug, icon_name, icon_url, image_url, description, display_order, is_active, meta_title, meta_description, created_at, updated_at) 
FROM '/tmp/categories.csv' DELIMITER ',' CSV HEADER;

\echo 'Loading sub-categories...'
COPY sub_categories(id, category_id, name, slug, description, display_order, is_active, created_at, updated_at) 
FROM '/tmp/sub_categories.csv' DELIMITER ',' CSV HEADER;

\echo 'Loading franchises...'
COPY franchises(id, name, slug, short_description, description, founded_year, trusted_seller, verified, total_outlets, outlet_range, parent_company, business_type, established_year, units_count, leader_name, leader_role, contact_email, logo_url_circle, logo_url_square, created_by, updated_by, created_at, updated_at) 
FROM '/tmp/franchises.csv' DELIMITER ',' CSV HEADER;

\echo 'Loading franchise_categories...'
COPY franchise_categories(id, franchise_id, category_id, sub_category_id, is_primary, created_at) 
FROM '/tmp/franchise_categories.csv' DELIMITER ',' CSV HEADER;

\echo 'Loading franchise_stats...'
COPY franchise_stats(id, franchise_id, rating, rating_count, follow_count, likes_count, view_count, save_count, share_count, enquiry_count, news_count, created_at, updated_at) 
FROM '/tmp/franchise_stats.csv' DELIMITER ',' CSV HEADER;

\echo 'Loading franchise_cities...'
COPY franchise_cities(id, franchise_id, city, state, country, created_at) 
FROM '/tmp/franchise_cities.csv' DELIMITER ',' CSV HEADER;

\echo 'Loading franchise_business_overview...'
COPY franchise_business_overview(id, franchise_id, products, services, created_by, updated_by, created_at, updated_at) 
FROM '/tmp/franchise_business_overview.csv' DELIMITER ',' CSV HEADER;

\echo 'Loading franchise_investment_requirement...'
COPY franchise_investment_requirement(id, franchise_id, initial_investment_min, initial_investment_max, franchise_fee, royalty_percentage, marketing_fee_percentage, payback_min_months, payback_max_months, roi_min_percentage, roi_max_percentage, monthly_turnover_min, monthly_turnover_max, single_unit_cost_min, single_unit_cost_max, investment_includes, created_by, updated_by, created_at, updated_at) 
FROM '/tmp/franchise_investment_requirement.csv' DELIMITER ',' CSV HEADER;

\echo 'Loading franchise_operations...'
COPY franchise_operations(id, franchise_id, space_min_sqft, space_max_sqft, required_property_type, staff_required_min, staff_required_max, staff_breakdown, operating_hours, training_provided, training_details, computer_requirements, marketing_support, preferred_locations, qualification_required, supply_chain_support, quality_control, created_by, updated_by, created_at, updated_at) 
FROM '/tmp/franchise_operations.csv' DELIMITER ',' CSV HEADER;

\echo 'Loading franchise_social_links...'
COPY franchise_social_links(id, franchise_id, instagram_url, facebook_url, twitter_url, linkedin_url, created_at, updated_at) 
FROM '/tmp/franchise_social_links.csv' DELIMITER ',' CSV HEADER;

\echo 'Loading category_questions...'
COPY category_questions(id, reference_id, question, intent_tag, created_at) 
FROM '/tmp/category_questions.csv' DELIMITER ',' CSV HEADER;

-- Re-enable triggers
SET session_replication_role = 'origin';

\echo '=========================================='
\echo '📊 DATA LOADING VERIFICATION'
\echo '=========================================='
SELECT 'Industries' as table_name, COUNT(*) as record_count FROM industries
UNION ALL SELECT 'Categories', COUNT(*) FROM categories
UNION ALL SELECT 'Sub-Categories', COUNT(*) FROM sub_categories
UNION ALL SELECT 'Franchises', COUNT(*) FROM franchises
UNION ALL SELECT 'Franchise Categories', COUNT(*) FROM franchise_categories
UNION ALL SELECT 'Franchise Stats', COUNT(*) FROM franchise_stats
UNION ALL SELECT 'Franchise Cities', COUNT(*) FROM franchise_cities
UNION ALL SELECT 'Business Overview', COUNT(*) FROM franchise_business_overview
UNION ALL SELECT 'Investment Requirements', COUNT(*) FROM franchise_investment_requirement
UNION ALL SELECT 'Operations', COUNT(*) FROM franchise_operations
UNION ALL SELECT 'Social Links', COUNT(*) FROM franchise_social_links
UNION ALL SELECT 'Category Questions', COUNT(*) FROM category_questions;

\echo '🎉 Data loaded successfully!'
