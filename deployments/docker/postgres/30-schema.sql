-- ============================================================
-- COMPLETE PostgreSQL Schema - Franchise Marketplace Platform
-- Date: 2026-06-11
-- Version: 4.0 
-- ============================================================

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
-- EXTENSIONS
-- ========================================
CREATE EXTENSION IF NOT EXISTS citext;

-- ========================================
-- USERS TABLE
-- ========================================
CREATE TABLE IF NOT EXISTS users (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    email           citext      NOT NULL,
    email_verified  boolean     NOT NULL DEFAULT false,
    status          text        NOT NULL DEFAULT 'active',
    name            varchar(255),
    phone           varchar(20),
    created_at      timestamptz NOT NULL DEFAULT NOW(),
    updated_at      timestamptz NOT NULL DEFAULT NOW(),

    CONSTRAINT users_email_unique UNIQUE (email),
    CONSTRAINT chk_email_format
        CHECK (email::text ~* '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$')
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE users IS 'Platform users - franchise seekers, franchisors, and admins';

-- ========================================
-- IDENTITIES TABLE
-- ========================================
CREATE TABLE IF NOT EXISTS identities (
    id               uuid  PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider         text  NOT NULL,
    provider_user_id text  NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT NOW(),
    updated_at       timestamptz NOT NULL DEFAULT NOW(),

    CONSTRAINT identities_provider_unique
        UNIQUE (provider, provider_user_id)
);

CREATE INDEX IF NOT EXISTS identities_user_id_idx ON identities(user_id);

-- ========================================
-- USERS SUBSCRIPTIONS TABLE
-- ========================================
CREATE TABLE IF NOT EXISTS user_subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tier VARCHAR(50) NOT NULL DEFAULT 'free',
    is_valid BOOLEAN NOT NULL DEFAULT true,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT uq_user_subscription UNIQUE (user_id, tier)
);
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
    image_url VARCHAR(255),
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
    CHECK (icon_url IS NULL OR icon_url = '' OR icon_url ~* '^https?://' OR icon_url ~* '^/'),
    CHECK (image_url IS NULL OR image_url = '' OR image_url ~* '^https?://' OR image_url ~* '^/'),
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
    image_url VARCHAR(255),
    description TEXT,
    display_order INT DEFAULT 0,
    is_active BOOLEAN DEFAULT TRUE,
    
    -- SEO
    meta_title VARCHAR(200),
    meta_description TEXT,
    
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT chk_icon_url 
        CHECK (icon_url IS NULL OR icon_url ~* '^https?://' OR icon_url ~* '^/'),
    CONSTRAINT chk_image_url 
        CHECK (image_url IS NULL OR image_url ~* '^https?://' OR image_url ~* '^/'),
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
    logo_url_circle VARCHAR(255),
    logo_url_square VARCHAR(255),
    
    -- V2 Unified Entities & Approval Audit
    entity_type VARCHAR(50) NOT NULL DEFAULT 'franchise',
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    association_metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    reviewed_by UUID REFERENCES users(id),
    reviewed_at TIMESTAMP,
    rejection_reason TEXT,
    
    -- Association Search & Sort
    member_count INT DEFAULT 0,
    membership_fee_min NUMERIC(12, 2) DEFAULT 0.00,
    membership_fee_max NUMERIC(12, 2) DEFAULT 0.00,
    approved_at TIMESTAMP,

    -- Feature 3 & 4 Curation & Onboarding Details
    website_url VARCHAR(255),
    is_featured BOOLEAN DEFAULT FALSE,
    featured_start_at TIMESTAMP,
    featured_expires_at TIMESTAMP,
    featured_order INT DEFAULT 0,
    is_sponsored BOOLEAN DEFAULT FALSE,

    created_by UUID NOT NULL REFERENCES users(id),
    updated_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    -- Constraints
    CONSTRAINT chk_email_format
        CHECK (contact_email IS NULL OR contact_email ~* '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$'),
    CONSTRAINT chk_logo_url_circle
        CHECK (logo_url_circle IS NULL OR logo_url_circle ~* '^https?://' OR logo_url_circle ~* '^/'),
    CONSTRAINT chk_logo_url_square
        CHECK (logo_url_square IS NULL OR logo_url_square ~* '^https?://' OR logo_url_square ~* '^/'),
    CONSTRAINT chk_total_outlets_positive
        CHECK (total_outlets >= 0),
    CONSTRAINT chk_units_count_positive
        CHECK (units_count >= 0),
    CONSTRAINT chk_founded_year_valid
        CHECK (founded_year IS NULL OR (founded_year >= 1800 AND founded_year <= EXTRACT(YEAR FROM CURRENT_DATE))),
    CONSTRAINT chk_established_year_valid
        CHECK (established_year IS NULL OR (established_year >= 1800 AND established_year <= EXTRACT(YEAR FROM CURRENT_DATE))),
    CONSTRAINT chk_website_url
        CHECK (website_url IS NULL OR website_url = '' OR website_url ~* '^https?://'),
    CONSTRAINT chk_featured_order_positive
        CHECK (featured_order >= 0)
);

CREATE TRIGGER update_franchises_updated_at
    BEFORE UPDATE ON franchises
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_franchises_slug ON franchises(slug);
CREATE INDEX idx_franchises_created_by ON franchises(created_by);
CREATE INDEX idx_franchises_entity_type_status ON franchises(entity_type, status);
CREATE INDEX idx_franchises_featured ON franchises(is_featured) WHERE is_featured = TRUE;
CREATE INDEX idx_franchises_sponsored ON franchises(is_sponsored) WHERE is_sponsored = TRUE;
CREATE INDEX idx_franchises_featured_order ON franchises(featured_order);

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
    revenue_model JSONB,
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
    territory_details JSONB,
    development_schedule JSONB,
    support_training JSONB,
    legal_compliance JSONB,
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

    entity_type VARCHAR(50) NOT NULL DEFAULT 'franchise',

    question TEXT NOT NULL,
    intent_tag VARCHAR(50),           -- NOT NULL DEFAULT 'general',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT chk_category_questions_entity_type 
        CHECK (entity_type IN ('franchise', 'association', 'master_franchise'))
);

CREATE INDEX idx_category_questions_reference
ON category_questions(reference_id);
CREATE INDEX idx_category_questions_entity_type ON category_questions(entity_type);

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
    entity_id UUID NOT NULL,
    entity_type VARCHAR(50) NOT NULL DEFAULT 'franchise',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- IDEMPOTENCY: Prevent duplicate favorites
    CONSTRAINT uq_user_entity_favorite UNIQUE (user_id, entity_id, entity_type),
    CONSTRAINT chk_user_favorites_entity_type CHECK (entity_type IN ('franchise', 'association', 'master_franchise'))
);

CREATE INDEX idx_user_favorites_user ON user_favorites(user_id);
CREATE INDEX idx_user_favorites_entity ON user_favorites(entity_id, entity_type);

COMMENT ON TABLE user_favorites IS 
    'User saved/favorited entities (franchise/association). Unique constraint prevents duplicates.';

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

-- ========================================
-- FRANCHISE DOCUMENTS TABLE
-- ========================================
CREATE TABLE franchise_documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    franchise_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    document_type VARCHAR(100) NOT NULL,
    s3_url VARCHAR(512) NOT NULL,
    status VARCHAR(50) DEFAULT 'pending',
    uploaded_by VARCHAR(255),
    rejection_reason TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT chk_doc_status_valid 
        CHECK (status IN ('pending', 'verified', 'rejected'))
);

CREATE TRIGGER update_franchise_documents_updated_at
    BEFORE UPDATE ON franchise_documents
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_franchise_documents_franchise ON franchise_documents(franchise_id);
CREATE INDEX idx_franchise_documents_status ON franchise_documents(status);

COMMENT ON TABLE franchise_documents IS 
    'Stores documents associated with a franchise (e.g. GST, PAN, Pitch Deck). Status tracks verification state.';

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
    entity_type VARCHAR(50) NOT NULL DEFAULT 'franchise',
    intent_tag VARCHAR(50) NOT NULL DEFAULT 'general',
    growth_rate_title VARCHAR(200) NOT NULL,
    growth_rate_description TEXT NOT NULL,
    market_trend_title VARCHAR(200) NOT NULL,
    market_trend_description TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- Ensure one insight per industry
    CONSTRAINT uq_industry_intent UNIQUE (industry_id, intent_tag, entity_type),
    CONSTRAINT chk_industry_market_insights_entity_type 
        CHECK (entity_type IN ('franchise', 'association', 'master_franchise'))
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
-- USER ACTIONS SCHEMA - Bookmark, Rating, Share
-- ============================================================

-- ========================================
-- USER RATINGS TABLE
-- ========================================
CREATE TABLE user_ratings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entity_id UUID NOT NULL,
    entity_type VARCHAR(50) NOT NULL DEFAULT 'franchise',
    rating DECIMAL(2,1) NOT NULL,
    review TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    -- One user can rate one entity only once
    CONSTRAINT uq_user_entity_rating UNIQUE (user_id, entity_id, entity_type),
    CONSTRAINT chk_rating_range CHECK (rating >= 1.0 AND rating <= 5.0),
    CONSTRAINT chk_user_ratings_entity_type CHECK (entity_type IN ('franchise', 'association', 'master_franchise'))
);

CREATE TRIGGER update_user_ratings_updated_at
    BEFORE UPDATE ON user_ratings
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_user_ratings_user       ON user_ratings(user_id);
CREATE INDEX idx_user_ratings_entity     ON user_ratings(entity_id, entity_type);
CREATE INDEX idx_user_ratings_rating     ON user_ratings(rating);

COMMENT ON TABLE user_ratings IS
    'User-submitted ratings (1-5) and optional review text for entities (franchise/association). One rating per user per entity.';

-- ========================================
-- FRANCHISE SHARES TABLE
-- ========================================
CREATE TABLE franchise_shares (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    -- user_id nullable — anonymous share bhi ho sakta hai
    entity_id UUID NOT NULL,
    entity_type VARCHAR(50) NOT NULL DEFAULT 'franchise',
    share_platform VARCHAR(50) DEFAULT 'copy_link',
    -- e.g. 'whatsapp', 'twitter', 'linkedin', 'email', 'copy_link'
    ip_address VARCHAR(45),
    shared_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT chk_franchise_shares_entity_type CHECK (entity_type IN ('franchise', 'association', 'master_franchise'))
);

CREATE INDEX idx_franchise_shares_entity    ON franchise_shares(entity_id, entity_type);
CREATE INDEX idx_franchise_shares_user      ON franchise_shares(user_id);
CREATE INDEX idx_franchise_shares_platform  ON franchise_shares(share_platform);
CREATE INDEX idx_franchise_shares_shared_at ON franchise_shares(shared_at);

COMMENT ON TABLE franchise_shares IS
    'Tracks when users share an entity. user_id nullable for anonymous shares. Increments entity stats share_count.';

-- ============================================================
-- CONTACT US
-- ============================================================
 
CREATE TABLE IF NOT EXISTS contact_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255)  NOT NULL,
    email       VARCHAR(255)  NOT NULL,
    company     VARCHAR(255),
    phone       VARCHAR(20),
    message     TEXT          NOT NULL,
    ip_address  VARCHAR(45),                        -- IPv4 / IPv6
    created_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
 
-- Index for admin queries (list messages newest-first)
CREATE INDEX IF NOT EXISTS idx_contact_messages_created_at
    ON contact_messages (created_at DESC);
 
-- Index for rate-limit lookups per IP
CREATE INDEX IF NOT EXISTS idx_contact_messages_ip_created
    ON contact_messages (ip_address, created_at DESC);

-- ============================================================
-- PUBLIC FORM SUBMISSIONS
-- ============================================================
CREATE TABLE IF NOT EXISTS public_form_submissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    form_type VARCHAR(100) NOT NULL,
    email VARCHAR(255) NOT NULL,
    phone VARCHAR(50),
    form_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(50) DEFAULT 'new',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_public_form_submissions_type ON public_form_submissions(form_type);
CREATE INDEX idx_public_form_submissions_email ON public_form_submissions(email);
CREATE INDEX idx_public_form_submissions_created_at ON public_form_submissions(created_at DESC);

-- ============================================================

-- FEATURED ENGINE AUDIT LOG
-- ============================================================
CREATE TABLE entity_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    action VARCHAR(50) NOT NULL,
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    old_values JSONB NOT NULL DEFAULT '{}'::jsonb,
    new_values JSONB NOT NULL DEFAULT '{}'::jsonb,
    notes TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_entity_audit_entity ON entity_audit_log(entity_id);
CREATE INDEX idx_entity_audit_created_at ON entity_audit_log(created_at DESC);

COMMENT ON TABLE entity_audit_log IS
    'Immutably tracks status changes and direct updates on entities (franchises/associations/master_franchises).';

-- ============================================================
-- MEMBERSHIP ENQUIRIES
-- ============================================================
CREATE TABLE enquiries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entity_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    message TEXT,
    preferred_contact VARCHAR(50) NOT NULL DEFAULT 'EMAIL',
    responded_at TIMESTAMP,
    closed_at TIMESTAMP,
    closed_by VARCHAR(50),
    last_activity_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT chk_enquiry_status
        CHECK (status IN ('PENDING', 'RESPONDED', 'CLOSED')),
    CONSTRAINT chk_preferred_contact
        CHECK (preferred_contact IN ('EMAIL', 'PHONE', 'EITHER')),
    CONSTRAINT chk_closed_by
        CHECK (closed_by IS NULL OR closed_by IN ('USER', 'ASSOCIATION', 'SYSTEM'))
);

CREATE TRIGGER update_enquiries_updated_at
    BEFORE UPDATE ON enquiries
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Create partial unique index to block duplicate PENDING enquiries per user-entity pair
CREATE UNIQUE INDEX uq_enquiry_pending_active
    ON enquiries(user_id, entity_id)
    WHERE status = 'PENDING';

CREATE INDEX idx_enquiries_user ON enquiries(user_id);
CREATE INDEX idx_enquiries_entity ON enquiries(entity_id);
CREATE INDEX idx_enquiries_status ON enquiries(status);
CREATE INDEX idx_enquiries_last_activity ON enquiries(last_activity_at DESC);

COMMENT ON TABLE enquiries IS
    'Tracks user membership/general enquiries for franchises, associations, and other entities.';

-- ============================================================
-- ENQUIRY AUDIT LOG
-- ============================================================
CREATE TABLE enquiry_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    enquiry_id UUID NOT NULL REFERENCES enquiries(id) ON DELETE CASCADE,
    from_status VARCHAR(50),
    to_status VARCHAR(50) NOT NULL,
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_role VARCHAR(50) NOT NULL,
    event_type VARCHAR(50) NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT chk_audit_from_status
        CHECK (from_status IS NULL OR from_status IN ('PENDING', 'RESPONDED', 'CLOSED')),
    CONSTRAINT chk_audit_to_status
        CHECK (to_status IN ('PENDING', 'RESPONDED', 'CLOSED')),
    CONSTRAINT chk_actor_role
        CHECK (actor_role IN ('ROLE_USER', 'ROLE_ASSOC_ADMIN', 'ROLE_PLATFORM_ADMIN', 'SYSTEM'))
);

CREATE INDEX idx_enquiry_audit_enquiry ON enquiry_audit_log(enquiry_id);
CREATE INDEX idx_enquiry_audit_created_at ON enquiry_audit_log(created_at DESC);

COMMENT ON TABLE enquiry_audit_log IS
    'Immutably logs all status changes and activity for enquiries.';

-- ============================================================
-- PENDING EDITS (Sensitive listing fields changes staging)
-- ============================================================
CREATE TABLE pending_edits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    field_name VARCHAR(100) NOT NULL,
    old_value JSONB,
    new_value JSONB,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    requested_by UUID REFERENCES users(id) ON DELETE SET NULL,
    requested_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at TIMESTAMP,
    rejection_reason TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_pending_edit_status CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED'))
);

CREATE TRIGGER update_pending_edits_updated_at
    BEFORE UPDATE ON pending_edits
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_pending_edits_entity ON pending_edits(entity_id);
CREATE INDEX idx_pending_edits_status ON pending_edits(status);

COMMENT ON TABLE pending_edits IS
    'Staged sensitive field changes for review on any entity type.';

-- ============================================================
-- DUPLICATE FLAGS (Fuzzy duplicate warnings)
-- ============================================================
CREATE TABLE duplicate_flags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    matched_entity_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    similarity_score NUMERIC(5, 2) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    resolved_by UUID REFERENCES users(id) ON DELETE SET NULL,
    resolved_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_duplicate_flag_status CHECK (status IN ('PENDING', 'RESOLVED', 'IGNORED'))
);

CREATE INDEX idx_duplicate_flags_entity ON duplicate_flags(entity_id);
CREATE INDEX idx_duplicate_flags_matched_entity ON duplicate_flags(matched_entity_id);

COMMENT ON TABLE duplicate_flags IS
    'Fuzzy duplicate warnings for newly submitted entities.';

-- ============================================================
-- VERIFICATION CRITERIA (Pass/Fail state of verification steps)
-- ============================================================
CREATE TABLE verification_criteria (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    vc_type VARCHAR(50) NOT NULL, -- 'VC-01', 'VC-02', 'VC-03'
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING', -- 'PENDING', 'PASSED', 'FAILED'
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_entity_vc UNIQUE (entity_id, vc_type),
    CONSTRAINT chk_vc_status CHECK (status IN ('PENDING', 'PASSED', 'FAILED'))
);

CREATE TRIGGER update_verification_criteria_updated_at
    BEFORE UPDATE ON verification_criteria
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_verification_criteria_entity ON verification_criteria(entity_id);

COMMENT ON TABLE verification_criteria IS
    'Pass/Fail state of VC-01, VC-02, VC-03 verification steps linked to an entity.';

-- ============================================================
-- OFFERINGS (Structured perks, discounts, directories, webinars)
-- ============================================================
CREATE TABLE offerings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    type VARCHAR(50) NOT NULL, -- 'benefit', 'discount', 'webinar', 'directory'
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(50) NOT NULL DEFAULT 'DRAFT', -- 'DRAFT', 'PUBLISHED', 'DELETED'
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_offering_status CHECK (status IN ('DRAFT', 'PUBLISHED', 'DELETED'))
);

CREATE TRIGGER update_offerings_updated_at
    BEFORE UPDATE ON offerings
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_offerings_entity ON offerings(entity_id);
CREATE INDEX idx_offerings_status ON offerings(status);

COMMENT ON TABLE offerings IS
    'Structured perks, discounts, directories, and webinars linked to an entity.';

-- ============================================================
-- MEMBERSHIPS (Approved entity-user relationship mapping)
-- ============================================================
CREATE TABLE memberships (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entity_id UUID NOT NULL REFERENCES franchises(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING', -- 'PENDING', 'ACTIVE', 'SUSPENDED', 'CANCELLED'
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_user_entity_membership UNIQUE (user_id, entity_id),
    CONSTRAINT chk_membership_status CHECK (status IN ('PENDING', 'ACTIVE', 'SUSPENDED', 'CANCELLED'))
);

CREATE TRIGGER update_memberships_updated_at
    BEFORE UPDATE ON memberships
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_memberships_user ON memberships(user_id);
CREATE INDEX idx_memberships_entity ON memberships(entity_id);

COMMENT ON TABLE memberships IS
    'Approved entity-user relationship mapping.';

-- ============================================================
-- ASSOCIATIONS TABLE
-- ============================================================
CREATE TABLE IF NOT EXISTS associations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug VARCHAR(255) UNIQUE NOT NULL,
    brand_name VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'DRAFT', -- 'DRAFT', 'PENDING_REVIEW', 'LIVE', 'SUSPENDED', 'ARCHIVED'
    featured_start_at TIMESTAMP,
    featured_end_at TIMESTAMP,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_association_status CHECK (status IN ('DRAFT', 'PENDING_REVIEW', 'LIVE', 'SUSPENDED', 'ARCHIVED'))
);

CREATE TRIGGER update_associations_updated_at
    BEFORE UPDATE ON associations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_associations_slug ON associations(slug);
CREATE INDEX idx_associations_status ON associations(status);

COMMENT ON TABLE associations IS 'Stores core data and extended JSON metadata for associations.';

-- GUEST AUDIT LOG
-- ============================================================
CREATE TABLE IF NOT EXISTS guest_audit_log (
    id BIGSERIAL PRIMARY KEY,
    session_id VARCHAR(64) NOT NULL,
    composite_key TEXT NOT NULL,
    action VARCHAR(32) NOT NULL,
    route_group VARCHAR(32) NOT NULL DEFAULT '',
    queries_used INT DEFAULT 0,
    credits_used INT DEFAULT 0,
    anomaly_flags JSONB DEFAULT '[]',
    blocked BOOLEAN DEFAULT FALSE,
    block_reason TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    retain_until TIMESTAMP WITH TIME ZONE DEFAULT (NOW() + INTERVAL '30 days')
);

CREATE INDEX IF NOT EXISTS idx_guest_audit_session_id ON guest_audit_log(session_id);
CREATE INDEX IF NOT EXISTS idx_guest_audit_created_at ON guest_audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_guest_audit_retain_until ON guest_audit_log(retain_until);
CREATE INDEX IF NOT EXISTS idx_guest_audit_route_group ON guest_audit_log(route_group);

-- ============================================================
-- CLEANUP FUNCTION FOR EXPIRED GUEST AUDIT EVENTS
-- ============================================================
CREATE OR REPLACE FUNCTION cleanup_expired_guest_audit_events()
RETURNS void AS $$
BEGIN
    DELETE FROM guest_audit_log
    WHERE retain_until < NOW();
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION cleanup_expired_guest_audit_events() IS
    'Deletes guest audit events past their retention period. Run via cron job daily.';

-- ============================================================
-- END OF COMPLETE SCHEMA
-- ============================================================
