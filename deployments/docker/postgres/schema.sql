-- ============================================================
-- FIXED PostgreSQL Schema - Franchise Marketplace Platform
-- Date: 2025-12-24
-- All Critical Fixes Applied from Review Document
-- ============================================================

-- Drop existing tables (reverse order due to foreign key constraints)
DROP TABLE IF EXISTS franchise_operations CASCADE;
DROP TABLE IF EXISTS franchise_investment_requirement CASCADE;
DROP TABLE IF EXISTS franchise_business_overview CASCADE;
DROP TABLE IF EXISTS franchise_social_links CASCADE;
DROP TABLE IF EXISTS franchise_stats CASCADE;
DROP TABLE IF EXISTS franchise_cities CASCADE;
DROP TABLE IF EXISTS category_questions CASCADE;
DROP TABLE IF EXISTS franchise_outlets CASCADE;
DROP TABLE IF EXISTS franchisors CASCADE;
DROP TABLE IF EXISTS franchises CASCADE;

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
    industry VARCHAR(100),
    parent_company VARCHAR(200),
    business_type VARCHAR(100),
    established_year SMALLINT,
    units_count INT DEFAULT 0,
    leader_name VARCHAR(150),
    leader_role VARCHAR(100),
    contact_email VARCHAR(150),
    logo_url VARCHAR(255),
    created_by UUID NOT NULL,
    updated_by UUID,
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

-- ========================================
-- FRANCHISE BUSINESS OVERVIEW
-- ========================================
CREATE TABLE franchise_business_overview (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    franchise_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    products JSONB NOT NULL DEFAULT '[]'::jsonb,
    services JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by UUID NOT NULL,
    updated_by UUID,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT uq_business_overview UNIQUE (franchise_id)
);

CREATE TRIGGER update_franchise_business_overview_updated_at
    BEFORE UPDATE ON franchise_business_overview
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_business_overview_franchise_id ON franchise_business_overview(franchise_id);

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
    created_by UUID NOT NULL,
    updated_by UUID,
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
    created_by UUID NOT NULL,
    updated_by UUID,
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

-- ========================================
-- CATEGORY QUESTIONS TABLE
-- ========================================
CREATE TABLE category_questions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category_name VARCHAR(100) NOT NULL,
    question TEXT NOT NULL,
    answer TEXT,
    display_order INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_category_questions_category ON category_questions(category_name);

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

-- ========================================
-- DOCUMENTATION COMMENTS
-- ========================================
COMMENT ON TABLE franchises IS 
    'Core franchise master data. Single source of truth for franchise information. Synced to Elasticsearch for search.';

COMMENT ON TABLE franchise_stats IS 
    'Engagement and performance metrics. Updated frequently by user interactions (views, likes, follows, etc.).';

COMMENT ON TABLE franchise_cities IS 
    'Cities where franchise operates or is available. Used for location-based browsing.';

COMMENT ON TABLE franchise_business_overview IS 
    'Products and services offered by franchise. Stored as JSONB for flexibility.';

COMMENT ON TABLE franchise_investment_requirement IS 
    'Financial requirements and projections for franchisees. All amounts in INR.';

COMMENT ON TABLE franchise_operations IS 
    'Operational requirements and support details for running the franchise.';

COMMENT ON TABLE category_questions IS 
    'Industry/category-specific frequently asked questions. Displayed on category pages.';

COMMENT ON TABLE franchise_social_links IS 
    'Social media profile URLs for franchise. Optional.';

COMMENT ON COLUMN franchises.trusted_seller IS 
    'Premium verified franchise with enhanced visibility, trust badge, and priority listing. Requires manual admin approval.';

COMMENT ON COLUMN franchises.verified IS 
    'Basic verification - email and business details confirmed. Lower trust level than trusted_seller.';

COMMENT ON COLUMN franchises.slug IS 
    'URL-friendly unique identifier. Used in franchise detail page URLs: /franchise/{slug}';

COMMENT ON COLUMN franchise_stats.rating IS 
    'Average customer rating (0.00-5.00). Calculated from user review submissions.';

COMMENT ON COLUMN franchise_investment_requirement.roi_min_percentage IS 
    'Minimum ROI percentage. ROI is always a range (min-max), never a single value. Display as range on all UIs.';

COMMENT ON COLUMN franchise_investment_requirement.payback_min_months IS 
    'Minimum time to recover initial investment. Based on franchisee-reported performance data.';

COMMENT ON COLUMN franchise_operations.training_provided IS 
    'Whether franchisor provides initial training to franchisee and staff members.';

-- ============================================================
-- END OF SCHEMA
-- ============================================================

-- ========================================
-- USERS TABLE (if not exists)
-- ========================================
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) UNIQUE NOT NULL,
    name VARCHAR(255),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ========================================
-- USER FAVORITES TABLE
-- ========================================
CREATE TABLE IF NOT EXISTS user_favorites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    franchise_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT uq_user_franchise_favorite UNIQUE (user_id, franchise_id)
);

-- ========================================
-- SAVED SEARCHES TABLE
-- ========================================
CREATE TABLE IF NOT EXISTS saved_searches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    name VARCHAR(255) NOT NULL,
    filters JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ========================================
-- INDEXES
-- ========================================
CREATE INDEX idx_user_favorites_user ON user_favorites(user_id);
CREATE INDEX idx_user_favorites_franchise ON user_favorites(franchise_id);
CREATE INDEX idx_saved_searches_user ON saved_searches(user_id);