-- ============================================================
-- COMPLETE PostgreSQL Schema - Franchise Marketplace Platform
-- Date: 2025-01-07
-- Version: 2.0 (With 3-Level Taxonomy: Industry → Category → Sub-Category)
-- ============================================================

-- Drop existing tables (reverse order due to foreign key constraints)
DROP TABLE IF EXISTS notifications CASCADE;
DROP TABLE IF EXISTS application_history CASCADE;
DROP TABLE IF EXISTS franchise_applications CASCADE;
DROP TABLE IF EXISTS idempotency_keys CASCADE;
DROP TABLE IF EXISTS franchise_operations CASCADE;
DROP TABLE IF EXISTS franchise_investment_requirement CASCADE;
DROP TABLE IF EXISTS franchise_business_overview CASCADE;
DROP TABLE IF EXISTS franchise_social_links CASCADE;
DROP TABLE IF EXISTS franchise_stats CASCADE;
DROP TABLE IF EXISTS franchise_cities CASCADE;
DROP TABLE IF EXISTS category_questions CASCADE;
DROP TABLE IF EXISTS franchise_outlets CASCADE;
DROP TABLE IF EXISTS saved_searches CASCADE;
DROP TABLE IF EXISTS user_favorites CASCADE;
DROP TABLE IF EXISTS franchise_categories CASCADE;
DROP TABLE IF EXISTS sub_categories CASCADE;
DROP TABLE IF EXISTS categories CASCADE;
DROP TABLE IF EXISTS industries CASCADE;
DROP TABLE IF EXISTS franchises CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS industry_market_insights CASCADE;


-- Drop views if exist
DROP VIEW IF EXISTS v_franchise_taxonomy CASCADE;
DROP VIEW IF EXISTS v_industry_stats CASCADE;

-- Drop functions if exist
DROP FUNCTION IF EXISTS get_franchise_hierarchy(UUID) CASCADE;
DROP FUNCTION IF EXISTS get_franchises_by_category(UUID) CASCADE;
DROP FUNCTION IF EXISTS update_category_franchise_count() CASCADE;

-- Enable UUID generation
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ========================================
-- AUTO-UPDATE TRIGGER FUNCTION
-- ========================================
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

-- ========================================
-- KEYCLOAK DATABASE
-- ========================================
SELECT 'CREATE DATABASE keycloak'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'keycloak')\gexec

SELECT 'CREATE DATABASE camunda'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'camunda')\gexec

-- ========================================
-- USERS TABLE
-- ========================================
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) UNIQUE NOT NULL,
    name VARCHAR(255),
    phone VARCHAR(20),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT chk_email_format 
        CHECK (email ~* '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$')
);

CREATE TRIGGER update_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_users_email ON users(email);

COMMENT ON TABLE users IS 'Platform users - franchise seekers, franchisors, and admins';

CREATE TABLE IF NOT EXISTS identities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider text NOT NULL,
    provider_user_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT identities_provider_unique
        UNIQUE (provider, provider_user_id)
);

CREATE INDEX IF NOT EXISTS identities_user_id_idx
ON identities (user_id);

-- ========================================
-- IDEMPOTENCY KEYS TABLE
-- ========================================
CREATE TABLE idempotency_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key VARCHAR(255) UNIQUE NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    response_data JSONB,
    status VARCHAR(50) NOT NULL DEFAULT 'processing',
    worker_type VARCHAR(100),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    
    CONSTRAINT chk_status_valid 
        CHECK (status IN ('processing', 'completed', 'failed'))
);

CREATE INDEX idx_idempotency_key ON idempotency_keys(idempotency_key);
CREATE INDEX idx_idempotency_expires ON idempotency_keys(expires_at);
CREATE INDEX idx_idempotency_status ON idempotency_keys(status);

COMMENT ON TABLE idempotency_keys IS 
    'Stores idempotency keys for API requests and worker operations. Prevents duplicate processing. TTL: 24 hours.';

-- ========================================
-- INDUSTRIES TABLE (Top Level - Level 1)
-- ========================================
CREATE TABLE industries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) UNIQUE NOT NULL,
    slug VARCHAR(100) UNIQUE NOT NULL,
    icon_name VARCHAR(100),
    icon_url VARCHAR(255),
    color_hex VARCHAR(7) NOT NULL,
    color_name VARCHAR(50),
    listing_title VARCHAR(200),
    listing_description TEXT,
    display_order INT DEFAULT 0,
    is_active BOOLEAN DEFAULT TRUE,
    is_featured BOOLEAN DEFAULT FALSE,
    
    -- SEO
    meta_title VARCHAR(200),
    meta_description TEXT,
    
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT chk_color_hex_format 
        CHECK (color_hex ~* '^#[0-9A-F]{6}$'),
    CHECK (icon_url IS NULL OR icon_url = '' OR icon_url ~* '^https?://'),
    CONSTRAINT chk_display_order_positive 
        CHECK (display_order >= 0)
);

CREATE TRIGGER update_industries_updated_at
    BEFORE UPDATE ON industries
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_industries_slug ON industries(slug);
CREATE INDEX idx_industries_active ON industries(is_active);
CREATE INDEX idx_industries_featured ON industries(is_featured);

COMMENT ON TABLE industries IS 
    'Top-level industry classification (e.g., Food & Beverage, Automotive, Beauty). Contains visual styling and listing info.';

-- ========================================
-- CATEGORIES TABLE (Middle Level - Level 2)
-- ========================================
CREATE TABLE categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    industry_id UUID NOT NULL REFERENCES industries(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    slug VARCHAR(100) UNIQUE NOT NULL,
    icon_name VARCHAR(100),
    icon_url VARCHAR(255),
    description TEXT,
    display_order INT DEFAULT 0,
    is_active BOOLEAN DEFAULT TRUE,
    
    -- SEO
    meta_title VARCHAR(200),
    meta_description TEXT,
    
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT chk_icon_url 
        CHECK (icon_url IS NULL OR icon_url ~* '^https?://'),
    CONSTRAINT chk_display_order_positive 
        CHECK (display_order >= 0),
    
    -- Unique category name within same industry
    CONSTRAINT uq_category_industry UNIQUE (industry_id, name)
);

CREATE TRIGGER update_categories_updated_at
    BEFORE UPDATE ON categories
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_categories_industry ON categories(industry_id);
CREATE INDEX idx_categories_slug ON categories(slug);
CREATE INDEX idx_categories_active ON categories(is_active);

COMMENT ON TABLE categories IS 
    'Mid-level categories within industries (e.g., Fast Food, Coffee Shops under Food & Beverage).';

-- ========================================
-- SUB-CATEGORIES TABLE (Bottom Level - Level 3)
-- ========================================
CREATE TABLE sub_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category_id UUID NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    slug VARCHAR(100) UNIQUE NOT NULL,
    description TEXT,
    display_order INT DEFAULT 0,
    is_active BOOLEAN DEFAULT TRUE,
    
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT chk_display_order_positive 
        CHECK (display_order >= 0),
    
    -- Unique sub-category name within same category
    CONSTRAINT uq_subcategory_category UNIQUE (category_id, name)
);

CREATE TRIGGER update_sub_categories_updated_at
    BEFORE UPDATE ON sub_categories
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_sub_categories_category ON sub_categories(category_id);
CREATE INDEX idx_sub_categories_slug ON sub_categories(slug);
CREATE INDEX idx_sub_categories_active ON sub_categories(is_active);

COMMENT ON TABLE sub_categories IS 
    'Bottom-level sub-categories within categories (e.g., Burger Joints, Pizza Chains under Fast Food).';

-- ========================================
-- MAIN FRANCHISES TABLE
-- ========================================
CREATE TABLE franchises (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(150) NOT NULL,
    slug VARCHAR(150) UNIQUE NOT NULL,
    short_description TEXT,
    description TEXT,
    founded_year SMALLINT,
    trusted_seller BOOLEAN DEFAULT FALSE,
    verified BOOLEAN DEFAULT FALSE,
    total_outlets INT DEFAULT 0,
    outlet_range VARCHAR(50),
    parent_company VARCHAR(200),
    business_type VARCHAR(100),
    established_year SMALLINT,
    units_count INT DEFAULT 0,
    leader_name VARCHAR(150),
    leader_role VARCHAR(100),
    contact_email VARCHAR(150),
    logo_url VARCHAR(255),
    created_by UUID NOT NULL REFERENCES users(id),
    updated_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- Constraints
    CONSTRAINT chk_email_format 
        CHECK (contact_email IS NULL OR contact_email ~* '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$'),
    CONSTRAINT chk_logo_url 
        CHECK (logo_url IS NULL OR logo_url ~* '^https?://'),
    CONSTRAINT chk_total_outlets_positive 
        CHECK (total_outlets >= 0),
    CONSTRAINT chk_units_count_positive 
        CHECK (units_count >= 0),
    CONSTRAINT chk_founded_year_valid 
        CHECK (founded_year IS NULL OR (founded_year >= 1800 AND founded_year <= EXTRACT(YEAR FROM CURRENT_DATE))),
    CONSTRAINT chk_established_year_valid 
        CHECK (established_year IS NULL OR (established_year >= 1800 AND established_year <= EXTRACT(YEAR FROM CURRENT_DATE)))
);

CREATE TRIGGER update_franchises_updated_at
    BEFORE UPDATE ON franchises
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_franchises_slug ON franchises(slug);
CREATE INDEX idx_franchises_created_by ON franchises(created_by);

COMMENT ON TABLE franchises IS 
    'Core franchise master data. Single source of truth for franchise information. Synced to Elasticsearch for search.';

-- ========================================
-- FRANCHISE CATEGORIES (Many-to-Many Junction)
-- ========================================
CREATE TABLE franchise_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    franchise_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    category_id UUID NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    sub_category_id UUID REFERENCES sub_categories(id) ON DELETE SET NULL,
    is_primary BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- Prevent duplicate category assignments
    CONSTRAINT uq_franchise_category_subcategory 
        UNIQUE (franchise_id, category_id, sub_category_id)
);

CREATE INDEX idx_franchise_categories_franchise ON franchise_categories(franchise_id);
CREATE INDEX idx_franchise_categories_category ON franchise_categories(category_id);
CREATE INDEX idx_franchise_categories_sub_category ON franchise_categories(sub_category_id);
CREATE INDEX idx_franchise_categories_primary ON franchise_categories(is_primary);

COMMENT ON TABLE franchise_categories IS 
    'Many-to-many relationship between franchises and categories/sub-categories. One franchise can have multiple categories.';

-- ========================================
-- FRANCHISE STATS TABLE
-- ========================================
CREATE TABLE franchise_stats (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    franchise_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    rating DECIMAL(3,2),
    rating_count INT DEFAULT 0,
    follow_count INT DEFAULT 0,
    likes_count INT DEFAULT 0,
    view_count INT DEFAULT 0,
    save_count INT DEFAULT 0,
    share_count INT DEFAULT 0,
    enquiry_count INT DEFAULT 0,
    news_count INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT uq_franchise_stats UNIQUE (franchise_id),
    CONSTRAINT chk_rating_range 
        CHECK (rating IS NULL OR (rating >= 0 AND rating <= 5)),
    CONSTRAINT chk_rating_count_positive CHECK (rating_count >= 0),
    CONSTRAINT chk_follow_count_positive CHECK (follow_count >= 0),
    CONSTRAINT chk_likes_count_positive CHECK (likes_count >= 0),
    CONSTRAINT chk_view_count_positive CHECK (view_count >= 0),
    CONSTRAINT chk_save_count_positive CHECK (save_count >= 0),
    CONSTRAINT chk_share_count_positive CHECK (share_count >= 0),
    CONSTRAINT chk_enquiry_count_positive CHECK (enquiry_count >= 0),
    CONSTRAINT chk_news_count_positive CHECK (news_count >= 0)
);

CREATE TRIGGER update_franchise_stats_updated_at
    BEFORE UPDATE ON franchise_stats
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_franchise_stats_franchise ON franchise_stats(franchise_id);

COMMENT ON TABLE franchise_stats IS 
    'Engagement and performance metrics. Updated frequently by user interactions (views, likes, follows, etc.).';

-- ========================================
-- FRANCHISE CITIES TABLE
-- ========================================
CREATE TABLE franchise_cities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    franchise_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    city VARCHAR(100) NOT NULL,
    state VARCHAR(100) NOT NULL,
    country VARCHAR(100) DEFAULT 'India',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_franchise_cities_franchise ON franchise_cities(franchise_id);
CREATE INDEX idx_franchise_cities_city ON franchise_cities(city);

COMMENT ON TABLE franchise_cities IS 
    'Cities where franchise operates or is available. Used for location-based browsing.';

-- ========================================
-- FRANCHISE BUSINESS OVERVIEW
-- ========================================
CREATE TABLE franchise_business_overview (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    franchise_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    products JSONB NOT NULL DEFAULT '[]'::jsonb,
    services JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by UUID NOT NULL REFERENCES users(id),
    updated_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT uq_business_overview UNIQUE (franchise_id)
);

CREATE TRIGGER update_franchise_business_overview_updated_at
    BEFORE UPDATE ON franchise_business_overview
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_business_overview_franchise_id ON franchise_business_overview(franchise_id);

COMMENT ON TABLE franchise_business_overview IS 
    'Products and services offered by franchise. Stored as JSONB for flexibility.';

-- ========================================
-- FRANCHISE INVESTMENT REQUIREMENT
-- ========================================
CREATE TABLE franchise_investment_requirement (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    franchise_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    initial_investment_min DECIMAL(15,2),
    initial_investment_max DECIMAL(15,2),
    franchise_fee DECIMAL(15,2),
    royalty_percentage DECIMAL(5,2),
    marketing_fee_percentage DECIMAL(5,2),
    payback_min_months INT,
    payback_max_months INT,
    roi_min_percentage DECIMAL(5,2),
    roi_max_percentage DECIMAL(5,2),
    monthly_turnover_min DECIMAL(15,2),
    monthly_turnover_max DECIMAL(15,2),
    single_unit_cost_min DECIMAL(15,2),
    single_unit_cost_max DECIMAL(15,2),
    investment_includes TEXT,
    created_by UUID NOT NULL REFERENCES users(id),
    updated_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT uq_investment UNIQUE (franchise_id),
    CONSTRAINT chk_royalty_percentage 
        CHECK (royalty_percentage IS NULL OR (royalty_percentage >= 0 AND royalty_percentage <= 100)),
    CONSTRAINT chk_marketing_fee_percentage 
        CHECK (marketing_fee_percentage IS NULL OR (marketing_fee_percentage >= 0 AND marketing_fee_percentage <= 100)),
    CONSTRAINT chk_roi_min_percentage 
        CHECK (roi_min_percentage IS NULL OR (roi_min_percentage >= 0 AND roi_min_percentage <= 100)),
    CONSTRAINT chk_roi_max_percentage 
        CHECK (roi_max_percentage IS NULL OR (roi_max_percentage >= 0 AND roi_max_percentage <= 100)),
    CONSTRAINT chk_investment_range 
        CHECK (initial_investment_min IS NULL OR initial_investment_max IS NULL OR initial_investment_min <= initial_investment_max),
    CONSTRAINT chk_payback_range 
        CHECK (payback_min_months IS NULL OR payback_max_months IS NULL OR payback_min_months <= payback_max_months),
    CONSTRAINT chk_roi_range 
        CHECK (roi_min_percentage IS NULL OR roi_max_percentage IS NULL OR roi_min_percentage <= roi_max_percentage),
    CONSTRAINT chk_monthly_turnover_range 
        CHECK (monthly_turnover_min IS NULL OR monthly_turnover_max IS NULL OR monthly_turnover_min <= monthly_turnover_max),
    CONSTRAINT chk_single_unit_cost_range 
        CHECK (single_unit_cost_min IS NULL OR single_unit_cost_max IS NULL OR single_unit_cost_min <= single_unit_cost_max),
    CONSTRAINT chk_initial_investment_min_positive 
        CHECK (initial_investment_min IS NULL OR initial_investment_min >= 0),
    CONSTRAINT chk_initial_investment_max_positive 
        CHECK (initial_investment_max IS NULL OR initial_investment_max >= 0),
    CONSTRAINT chk_franchise_fee_positive 
        CHECK (franchise_fee IS NULL OR franchise_fee >= 0),
    CONSTRAINT chk_monthly_turnover_min_positive 
        CHECK (monthly_turnover_min IS NULL OR monthly_turnover_min >= 0),
    CONSTRAINT chk_monthly_turnover_max_positive 
        CHECK (monthly_turnover_max IS NULL OR monthly_turnover_max >= 0)
);

CREATE TRIGGER update_franchise_investment_requirement_updated_at
    BEFORE UPDATE ON franchise_investment_requirement
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_investment_franchise_id ON franchise_investment_requirement(franchise_id);

COMMENT ON TABLE franchise_investment_requirement IS 
    'Financial requirements and projections for franchisees. All amounts in INR.';

-- ========================================
-- FRANCHISE OPERATIONS
-- ========================================
CREATE TABLE franchise_operations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    franchise_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    space_min_sqft INT,
    space_max_sqft INT,
    required_property_type VARCHAR(100),
    staff_required_min INT,
    staff_required_max INT,
    staff_breakdown JSONB DEFAULT '[]'::jsonb,
    operating_hours VARCHAR(100),
    training_provided BOOLEAN DEFAULT TRUE,
    training_details TEXT,
    computer_requirements TEXT,
    marketing_support TEXT,
    preferred_locations TEXT,
    qualification_required TEXT,
    supply_chain_support BOOLEAN DEFAULT FALSE,
    quality_control BOOLEAN DEFAULT FALSE,
    created_by UUID NOT NULL REFERENCES users(id),
    updated_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT uq_operations UNIQUE (franchise_id),
    CONSTRAINT chk_space_range 
        CHECK (space_min_sqft IS NULL OR space_max_sqft IS NULL OR space_min_sqft <= space_max_sqft),
    CONSTRAINT chk_staff_range 
        CHECK (staff_required_min IS NULL OR staff_required_max IS NULL OR staff_required_min <= staff_required_max),
    CONSTRAINT chk_space_min_positive 
        CHECK (space_min_sqft IS NULL OR space_min_sqft > 0),
    CONSTRAINT chk_space_max_positive 
        CHECK (space_max_sqft IS NULL OR space_max_sqft > 0),
    CONSTRAINT chk_staff_min_positive 
        CHECK (staff_required_min IS NULL OR staff_required_min >= 0),
    CONSTRAINT chk_staff_max_positive 
        CHECK (staff_required_max IS NULL OR staff_required_max >= 0)
);

CREATE TRIGGER update_franchise_operations_updated_at
    BEFORE UPDATE ON franchise_operations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_operations_franchise_id ON franchise_operations(franchise_id);

COMMENT ON TABLE franchise_operations IS 
    'Operational requirements and support details for running the franchise.';

-- ========================================
-- CATEGORY QUESTIONS (AI-Driven FAQ)
-- ========================================
CREATE TABLE category_questions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    reference_id UUID NOT NULL,
    -- Refers to either industries.id OR categories.id (decided by AI / backend)

    question TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_category_questions_reference
ON category_questions(reference_id);

COMMENT ON TABLE category_questions IS
    'AI-driven questions linked to either industry or category using reference_id. Answer, type, and display handled by AI.';

-- ========================================
-- FRANCHISE SOCIAL LINKS
-- ========================================
CREATE TABLE franchise_social_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    franchise_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    instagram_url VARCHAR(255),
    facebook_url VARCHAR(255),
    twitter_url VARCHAR(255),
    linkedin_url VARCHAR(255),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT uq_social_links UNIQUE (franchise_id),
    CONSTRAINT chk_instagram_url 
        CHECK (instagram_url IS NULL OR instagram_url ~* '^https?://'),
    CONSTRAINT chk_facebook_url 
        CHECK (facebook_url IS NULL OR facebook_url ~* '^https?://'),
    CONSTRAINT chk_twitter_url 
        CHECK (twitter_url IS NULL OR twitter_url ~* '^https?://'),
    CONSTRAINT chk_linkedin_url 
        CHECK (linkedin_url IS NULL OR linkedin_url ~* '^https?://')
);

CREATE TRIGGER update_franchise_social_links_updated_at
    BEFORE UPDATE ON franchise_social_links
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_social_links_franchise_id ON franchise_social_links(franchise_id);

COMMENT ON TABLE franchise_social_links IS 
    'Social media profile URLs for franchise. Optional.';

-- ========================================
-- USER FAVORITES TABLE
-- ========================================
CREATE TABLE user_favorites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    franchise_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- IDEMPOTENCY: Prevent duplicate favorites
    CONSTRAINT uq_user_franchise_favorite UNIQUE (user_id, franchise_id)
);

CREATE INDEX idx_user_favorites_user ON user_favorites(user_id);
CREATE INDEX idx_user_favorites_franchise ON user_favorites(franchise_id);

COMMENT ON TABLE user_favorites IS 
    'User saved/favorited franchises. Unique constraint prevents duplicates.';

-- ========================================
-- SAVED SEARCHES TABLE
-- ========================================
CREATE TABLE saved_searches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    filters JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- IDEMPOTENCY: Prevent duplicate search names per user
    CONSTRAINT uq_user_search_name UNIQUE (user_id, name)
);

CREATE TRIGGER update_saved_searches_updated_at
    BEFORE UPDATE ON saved_searches
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_saved_searches_user ON saved_searches(user_id);

COMMENT ON TABLE saved_searches IS 
    'User saved search filters. Unique constraint on (user_id, name) prevents duplicate search names.';

-- ========================================
-- FRANCHISE APPLICATIONS TABLE
-- ========================================
CREATE TABLE franchise_applications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seeker_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    franchise_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL DEFAULT 'submitted',
    application_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    idempotency_key VARCHAR(255) UNIQUE,
    submitted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT chk_status_valid 
        CHECK (status IN ('submitted', 'under_review', 'approved', 'rejected', 'withdrawn'))
);

-- Create partial unique index separately (correct syntax)
CREATE UNIQUE INDEX uq_application_active 
    ON franchise_applications(seeker_id, franchise_id) 
    WHERE status IN ('submitted', 'under_review');

CREATE TRIGGER update_franchise_applications_updated_at
    BEFORE UPDATE ON franchise_applications
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_applications_seeker ON franchise_applications(seeker_id);
CREATE INDEX idx_applications_franchise ON franchise_applications(franchise_id);
CREATE INDEX idx_applications_status ON franchise_applications(status);
CREATE INDEX idx_applications_idempotency ON franchise_applications(idempotency_key);

COMMENT ON TABLE franchise_applications IS 
    'Franchise applications submitted by seekers. Partial unique index prevents duplicate active applications per user-franchise pair.';

-- ========================================
-- APPLICATION HISTORY TABLE (AUDIT LOG)
-- ========================================
CREATE TABLE application_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES franchise_applications(id) ON DELETE CASCADE,
    old_status VARCHAR(50),
    new_status VARCHAR(50) NOT NULL,
    changed_by UUID REFERENCES users(id),
    notes TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_application_history_application ON application_history(application_id);
CREATE INDEX idx_application_history_created_at ON application_history(created_at);

COMMENT ON TABLE application_history IS 
    'Audit log for franchise application status changes. Used for tracking and compliance.';

-- ========================================
-- NOTIFICATIONS TABLE
-- ========================================
CREATE TABLE notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    notification_type VARCHAR(100) NOT NULL,
    recipient_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    application_id UUID REFERENCES franchise_applications(id) ON DELETE SET NULL,
    franchise_id UUID REFERENCES franchises(id) ON DELETE SET NULL,
    subject VARCHAR(255),
    message TEXT,
    channel VARCHAR(50) NOT NULL DEFAULT 'email',
    sent_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    idempotency_key VARCHAR(255) UNIQUE,
    metadata JSONB DEFAULT '{}'::jsonb,
    sent_date DATE GENERATED ALWAYS AS (sent_at::DATE) STORED,
    
    CONSTRAINT chk_channel_valid 
        CHECK (channel IN ('email', 'sms', 'push', 'in_app'))
);

-- Create partial unique index separately using generated column
CREATE UNIQUE INDEX uq_notification_daily
    ON notifications(notification_type, recipient_id, COALESCE(application_id::TEXT, 'NULL'), sent_date);

CREATE INDEX idx_notifications_recipient ON notifications(recipient_id);
CREATE INDEX idx_notifications_application ON notifications(application_id);
CREATE INDEX idx_notifications_type ON notifications(notification_type);
CREATE INDEX idx_notifications_sent_at ON notifications(sent_at);
CREATE INDEX idx_notifications_idempotency ON notifications(idempotency_key);

COMMENT ON TABLE notifications IS 
    'All platform notifications sent to users. Unique constraint prevents duplicate notifications on same day.';

-- ============================================================
-- HELPER FUNCTIONS
-- ============================================================


-- Get complete hierarchy for a franchise
CREATE OR REPLACE FUNCTION get_franchise_hierarchy(p_franchise_id UUID)
RETURNS TABLE (
    industry_name VARCHAR,
    industry_color VARCHAR,
    category_name VARCHAR,
    sub_category_name VARCHAR,
    is_primary BOOLEAN
) AS $$
BEGIN
    RETURN QUERY
    SELECT 
        i.name as industry_name,
        i.color_hex as industry_color,
        c.name as category_name,
        sc.name as sub_category_name,
        fc.is_primary
    FROM franchise_categories fc
    INNER JOIN categories c ON fc.category_id = c.id
    INNER JOIN industries i ON c.industry_id = i.id
    LEFT JOIN sub_categories sc ON fc.sub_category_id = sc.id
    WHERE fc.franchise_id = p_franchise_id
    ORDER BY fc.is_primary DESC, i.display_order, c.display_order, sc.display_order;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION get_franchise_hierarchy(UUID) IS 
    'Returns complete industry → category → sub-category hierarchy for a franchise.';

-- Get all franchises in a category (including sub-categories)
CREATE OR REPLACE FUNCTION get_franchises_by_category(p_category_id UUID)
RETURNS TABLE (
    franchise_id UUID,
    franchise_name VARCHAR,
    franchise_slug VARCHAR,
    sub_category_name VARCHAR,
    is_primary BOOLEAN
) AS $$
BEGIN
    RETURN QUERY
    SELECT 
        f.id as franchise_id,
        f.name as franchise_name,
        f.slug as franchise_slug,
        sc.name as sub_category_name,
        fc.is_primary
    FROM franchises f
    INNER JOIN franchise_categories fc ON f.id = fc.franchise_id
    LEFT JOIN sub_categories sc ON fc.sub_category_id = sc.id
    WHERE fc.category_id = p_category_id
    ORDER BY fc.is_primary DESC, f.name;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION get_franchises_by_category(UUID) IS 
    'Returns all franchises belonging to a specific category, with their sub-categories.';

-- ============================================================
-- USEFUL VIEWS
-- ============================================================

-- Complete franchise taxonomy view
CREATE OR REPLACE VIEW v_franchise_taxonomy AS
SELECT 
    f.id as franchise_id,
    f.name as franchise_name,
    f.slug as franchise_slug,
    i.id as industry_id,
    i.name as industry_name,
    i.color_hex as industry_color,
    c.id as category_id,
    c.name as category_name,
    sc.id as sub_category_id,
    sc.name as sub_category_name,
    fc.is_primary
FROM franchises f
INNER JOIN franchise_categories fc ON f.id = fc.franchise_id
INNER JOIN categories c ON fc.category_id = c.id
INNER JOIN industries i ON c.industry_id = i.id
LEFT JOIN sub_categories sc ON fc.sub_category_id = sc.id;

COMMENT ON VIEW v_franchise_taxonomy IS 
    'Denormalized view showing complete franchise → category → industry relationships.';

-- Industry statistics view
CREATE OR REPLACE VIEW v_industry_stats AS
SELECT 
    i.id,
    i.name,
    i.color_hex,
    i.slug,
    COUNT(DISTINCT c.id) as category_count,
    COUNT(DISTINCT sc.id) as subcategory_count,
    COUNT(DISTINCT fc.franchise_id) as franchise_count
FROM industries i
LEFT JOIN categories c ON i.id = c.industry_id
LEFT JOIN sub_categories sc ON c.id = sc.category_id
LEFT JOIN franchise_categories fc ON c.id = fc.category_id
WHERE i.is_active = TRUE
GROUP BY i.id, i.name, i.color_hex, i.slug
ORDER BY i.display_order;

COMMENT ON VIEW v_industry_stats IS 
    'Statistics showing count of categories, sub-categories, and franchises per industry.';

-- ============================================================
-- CLEANUP JOB FOR EXPIRED IDEMPOTENCY KEYS
-- ============================================================
CREATE OR REPLACE FUNCTION cleanup_expired_idempotency_keys()
RETURNS void AS $$
BEGIN
    DELETE FROM idempotency_keys 
    WHERE expires_at < CURRENT_TIMESTAMP;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION cleanup_expired_idempotency_keys() IS 
    'Cleanup function for expired idempotency keys. Run via cron job daily.';

-- ============================================================
-- INDUSTRY MARKET INSIGHTS TABLE
-- ============================================================
-- Create the table
CREATE TABLE industry_market_insights (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    industry_id UUID NOT NULL REFERENCES industries(id) ON DELETE CASCADE,
    industry_slug VARCHAR(100) NOT NULL,
    growth_rate_title VARCHAR(200) NOT NULL,
    growth_rate_description TEXT NOT NULL,
    market_trend_title VARCHAR(200) NOT NULL,
    market_trend_description TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- Ensure one insight per industry
    CONSTRAINT uq_industry_insights UNIQUE (industry_id)
);

-- Add trigger for auto-updating updated_at
CREATE TRIGGER update_industry_market_insights_updated_at
    BEFORE UPDATE ON industry_market_insights
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Create indexes
CREATE INDEX idx_industry_market_insights_industry_id ON industry_market_insights(industry_id);
CREATE INDEX idx_industry_market_insights_industry_slug ON industry_market_insights(industry_slug);

COMMENT ON TABLE industry_market_insights IS 
    'Market insights, growth rates, and trends for each industry. Used for industry detail pages and research.';

-- ============================================================
-- END OF COMPLETE SCHEMA
-- ============================================================
