-- ============================================================
-- PostgreSQL Schema v2 - Class Table Inheritance (Listings)
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
    email           TEXT        NOT NULL,
    email_verified  boolean     NOT NULL DEFAULT false,
    phone_verified  boolean     DEFAULT false,
    status          text        NOT NULL DEFAULT 'active',
    name            VARCHAR(500),
    phone           VARCHAR(100),
    location        varchar(255),
    profile_image   varchar(500),
    archived_at     TIMESTAMPTZ DEFAULT NULL,
    created_at      timestamptz NOT NULL DEFAULT NOW(),
    updated_at      timestamptz NOT NULL DEFAULT NOW(),

    CONSTRAINT users_email_unique UNIQUE (email),
    CONSTRAINT chk_users_status
        CHECK (status IN ('active', 'inactive', 'suspended', 'pending', 'archived'))
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
COMMENT ON TABLE users IS 'Platform users - franchise seekers, franchisors, and admins';
COMMENT ON COLUMN users.profile_image IS 'S3 object key (not full URL). CDN URL constructed at read time via BuildPhotoURL().';

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
    CONSTRAINT identities_provider_unique UNIQUE (provider, provider_user_id)
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
    CONSTRAINT chk_status_valid CHECK (status IN ('processing', 'completed', 'failed'))
);
CREATE INDEX idx_idempotency_key ON idempotency_keys(idempotency_key);
CREATE INDEX idx_idempotency_expires ON idempotency_keys(expires_at);
CREATE INDEX idx_idempotency_status ON idempotency_keys(status);
COMMENT ON TABLE idempotency_keys IS 'Stores idempotency keys for API requests and worker operations. Prevents duplicate processing. TTL: 24 hours.';

-- ========================================
-- INDUSTRIES TABLE
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
    association_listing_description TEXT,
    master_franchise_listing_description TEXT,
    display_order INT DEFAULT 0,
    is_active BOOLEAN DEFAULT TRUE,
    is_featured BOOLEAN DEFAULT FALSE,
    meta_title VARCHAR(200),
    meta_description TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_color_hex_format CHECK (color_hex ~* '^#[0-9A-F]{6}$'),
    CHECK (icon_url IS NULL OR icon_url = '' OR icon_url ~* '^https?://' OR icon_url ~* '^/'),
    CHECK (image_url IS NULL OR image_url = '' OR image_url ~* '^https?://' OR image_url ~* '^/'),
    CONSTRAINT chk_display_order_positive CHECK (display_order >= 0)
);
CREATE TRIGGER update_industries_updated_at BEFORE UPDATE ON industries FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_industries_slug ON industries(slug);
CREATE INDEX idx_industries_active ON industries(is_active);
CREATE INDEX idx_industries_featured ON industries(is_featured);
COMMENT ON TABLE industries IS 'Top-level industry classification. Contains visual styling and listing info.';

-- ========================================
-- CATEGORIES TABLE
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
    meta_title VARCHAR(200),
    meta_description TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_icon_url CHECK (icon_url IS NULL OR icon_url ~* '^https?://' OR icon_url ~* '^/'),
    CONSTRAINT chk_image_url CHECK (image_url IS NULL OR image_url ~* '^https?://' OR image_url ~* '^/'),
    CONSTRAINT chk_display_order_positive CHECK (display_order >= 0),
    CONSTRAINT uq_category_industry UNIQUE (industry_id, name)
);
CREATE TRIGGER update_categories_updated_at BEFORE UPDATE ON categories FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_categories_industry ON categories(industry_id);
CREATE INDEX idx_categories_slug ON categories(slug);
CREATE INDEX idx_categories_active ON categories(is_active);
COMMENT ON TABLE categories IS 'Mid-level categories within industries.';

-- ========================================
-- SUB-CATEGORIES TABLE
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
    CONSTRAINT chk_display_order_positive CHECK (display_order >= 0),
    CONSTRAINT uq_subcategory_category UNIQUE (category_id, name)
);
CREATE TRIGGER update_sub_categories_updated_at BEFORE UPDATE ON sub_categories FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_sub_categories_category ON sub_categories(category_id);
CREATE INDEX idx_sub_categories_slug ON sub_categories(slug);
CREATE INDEX idx_sub_categories_active ON sub_categories(is_active);
COMMENT ON TABLE sub_categories IS 'Bottom-level sub-categories within categories.';

-- ============================================================
-- CLASS TABLE INHERITANCE: BASE LISTINGS TABLE
-- ============================================================
CREATE TABLE listings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(150) NOT NULL,
    slug VARCHAR(150) UNIQUE NOT NULL,
    short_description TEXT,
    description TEXT,
    entity_type VARCHAR(50) NOT NULL, -- 'franchise', 'association', 'master_franchise'
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    
    founded_year SMALLINT,
    contact_email TEXT,
    website_url VARCHAR(255),
    logo_url_circle VARCHAR(255),
    logo_url_square VARCHAR(255),
    
    trusted_seller BOOLEAN DEFAULT FALSE,
    verified BOOLEAN DEFAULT FALSE,
    reviewed_by UUID REFERENCES users(id),
    reviewed_at TIMESTAMP,
    approved_at TIMESTAMP,
    rejection_reason TEXT,
    
    is_featured BOOLEAN DEFAULT FALSE,
    featured_start_at TIMESTAMP,
    featured_expires_at TIMESTAMP,
    featured_order INT DEFAULT 0,
    is_sponsored BOOLEAN DEFAULT FALSE,
    
    created_by UUID NOT NULL REFERENCES users(id),
    updated_by UUID REFERENCES users(id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    

CONSTRAINT chk_entity_type CHECK (entity_type IN ('franchise', 'association', 'master_franchise', 'blog')),
    CONSTRAINT chk_listing_status CHECK (status IN ('DRAFT', 'PENDING_REVIEW', 'LIVE', 'SUSPENDED', 'ARCHIVED', 'pending', 'under_review', 'approved', 'rejected', 'withdrawn', 'live')),
    CONSTRAINT chk_logo_url_circle CHECK (logo_url_circle IS NULL OR logo_url_circle ~* '^https?://' OR logo_url_circle ~* '^/'),
    CONSTRAINT chk_logo_url_square CHECK (logo_url_square IS NULL OR logo_url_square ~* '^https?://' OR logo_url_square ~* '^/'),
    CONSTRAINT chk_founded_year_valid CHECK (founded_year IS NULL OR (founded_year >= 1800 AND founded_year <= EXTRACT(YEAR FROM CURRENT_DATE))),
    CONSTRAINT chk_website_url CHECK (website_url IS NULL OR website_url = '' OR website_url ~* '^https?://'),
    CONSTRAINT chk_featured_order_positive CHECK (featured_order >= 0)
);
CREATE TRIGGER update_listings_updated_at BEFORE UPDATE ON listings FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_listings_slug ON listings(slug);
CREATE INDEX idx_listings_created_by ON listings(created_by);
CREATE INDEX idx_listings_entity_type_status ON listings(entity_type, status);
CREATE INDEX idx_listings_featured ON listings(is_featured) WHERE is_featured = TRUE;
CREATE INDEX idx_listings_sponsored ON listings(is_sponsored) WHERE is_sponsored = TRUE;
CREATE INDEX idx_listings_featured_order ON listings(featured_order);
COMMENT ON TABLE listings IS 'Base table for all entities (Franchises, Associations, Master Franchises). Centralized fields for fast queries.';

-- ============================================================
-- CHILD TABLE: FRANCHISES
-- ============================================================
CREATE TABLE franchises (
    id UUID PRIMARY KEY REFERENCES listings(id) ON DELETE CASCADE,
    total_outlets INT DEFAULT 0,
    outlet_range VARCHAR(50),
    parent_company VARCHAR(200),
    business_type VARCHAR(100),
    established_year SMALLINT,
    units_count INT DEFAULT 0,
    leader_name VARCHAR(150),
    leader_role VARCHAR(100),
    CONSTRAINT chk_total_outlets_positive CHECK (total_outlets >= 0),
    CONSTRAINT chk_units_count_positive CHECK (units_count >= 0),
    CONSTRAINT chk_established_year_valid CHECK (established_year IS NULL OR (established_year >= 1800 AND established_year <= EXTRACT(YEAR FROM CURRENT_DATE)))
);
COMMENT ON TABLE franchises IS 'Extension table specifically for Franchises. ID matches listings(id).';

-- ============================================================
-- CHILD TABLE: BLOGS
-- ============================================================
CREATE TABLE blogs (
    id UUID PRIMARY KEY REFERENCES listings(id) ON DELETE CASCADE,
    reading_time_mins INT NOT NULL DEFAULT 5,
    seo_title VARCHAR(200),
    seo_description TEXT,
    featured_image_url VARCHAR(500) NOT NULL,
    author_display_name VARCHAR(150),
    tags TEXT[],
    additional_media_urls TEXT[],
    content TEXT
);
COMMENT ON TABLE blogs IS 'Blog-specific extension of the listings table. ID matches listings(id).';

-- ============================================================
-- BLOG SUBSCRIBERS
-- ============================================================
CREATE TABLE blog_subscribers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
    source VARCHAR(50),
    subscribed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    unsubscribed_at TIMESTAMPTZ,
    CONSTRAINT uq_blog_subscriber_email UNIQUE (email),
    CONSTRAINT chk_subscriber_status CHECK (status IN ('ACTIVE', 'UNSUBSCRIBED'))
);
CREATE INDEX idx_blog_subscribers_email ON blog_subscribers(email);
CREATE INDEX idx_blog_subscribers_status ON blog_subscribers(status);

-- ============================================================
-- BLOG AUTHOR FOLLOWERS
-- ============================================================
CREATE TABLE blog_author_followers (
    follower_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    author_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (follower_id, author_id)
);
CREATE INDEX idx_blog_followers_author ON blog_author_followers(author_id);

-- ============================================================
-- CHILD TABLE: ASSOCIATIONS (Fully normalized, NO JSONB bloat)
-- ============================================================
CREATE TABLE associations (
    id UUID PRIMARY KEY REFERENCES listings(id) ON DELETE CASCADE,
    association_type VARCHAR(100),
    sector_represented VARCHAR(150),
    legal_status VARCHAR(150),
    headquarters_address TEXT,
    regional_presence VARCHAR(255),
    contact_phone VARCHAR(50),
    member_count INT DEFAULT 0,
    membership_fee_min NUMERIC(12, 2) DEFAULT 0.00,
    membership_fee_max NUMERIC(12, 2) DEFAULT 0.00,
    member_type VARCHAR(150),
    member_size_classification VARCHAR(150),
    industry_id UUID REFERENCES industries(id) ON DELETE SET NULL,
    association_metadata JSONB DEFAULT '{}'::jsonb,
    CONSTRAINT chk_member_count_positive CHECK (member_count >= 0),
    CONSTRAINT chk_membership_fee_min_positive CHECK (membership_fee_min >= 0),
    CONSTRAINT chk_membership_fee_max_positive CHECK (membership_fee_max >= 0),
    CONSTRAINT chk_membership_fee_range CHECK (membership_fee_min <= membership_fee_max)
);
COMMENT ON TABLE associations IS 'Extension table specifically for Associations. ID matches listings(id).';

CREATE TABLE association_membership_details (
    association_id UUID PRIMARY KEY REFERENCES associations(id) ON DELETE CASCADE,
    affiliated_associations_count INT DEFAULT 0,
    domestic_members_available BOOLEAN DEFAULT FALSE,
    sectoral_diversity VARCHAR(255),
    member_services_access TEXT,
    member_directory_availability VARCHAR(100),
    eligible_legal_entity_type VARCHAR(200),
    minimum_revenue_requirement VARCHAR(200),
    operational_presence_requirement VARCHAR(200),
    application_mode VARCHAR(50)
);

CREATE TABLE association_transparency (
    association_id UUID PRIMARY KEY REFERENCES associations(id) ON DELETE CASCADE,
    governance_model TEXT,
    jurisdiction TEXT,
    publicly_available_documents_status VARCHAR(50),
    member_verification_mechanism VARCHAR(50),
    grievance_redressal_mechanism VARCHAR(50),
    ethics_compliance_framework VARCHAR(50)
);

-- Generic structured table to handle lists without JSON arrays
CREATE TABLE association_attributes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    association_id UUID NOT NULL REFERENCES associations(id) ON DELETE CASCADE,
    attribute_type VARCHAR(50) NOT NULL, -- e.g., 'SERVICE', 'PROGRAM', 'PUBLICATION', 'EVENT', 'PARTNERSHIP', 'AWARD', 'POLICY'
    category VARCHAR(150), 
    title VARCHAR(255) NOT NULL,
    description TEXT,
    metadata JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_association_attr ON association_attributes(association_id, attribute_type);

CREATE TABLE association_council_members (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    association_id UUID NOT NULL REFERENCES associations(id) ON DELETE CASCADE,
    name VARCHAR(150) NOT NULL,
    role VARCHAR(150) NOT NULL,
    parent_id UUID REFERENCES association_council_members(id) ON DELETE SET NULL, 
    display_order INT DEFAULT 0
);

-- ============================================================
-- CHILD TABLE: MASTER FRANCHISES
-- ============================================================
CREATE TABLE master_franchises (
    id UUID PRIMARY KEY REFERENCES listings(id) ON DELETE CASCADE,
    territory_rights VARCHAR(255),
    sub_franchise_fee_split NUMERIC(5,2),
    master_fee DECIMAL(15,2),
    min_sub_franchises_required INT
);
COMMENT ON TABLE master_franchises IS 'Extension table specifically for Master Franchises. ID matches listings(id).';

-- ============================================================
-- UNIVERSAL LISTING RELATIONS (Categories, Stats, Social, Docs, etc.)
-- ============================================================
CREATE TABLE listing_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    category_id UUID NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    sub_category_id UUID REFERENCES sub_categories(id) ON DELETE SET NULL,
    is_primary BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_listing_category_subcategory UNIQUE (listing_id, category_id, sub_category_id)
);
CREATE INDEX idx_listing_categories_listing ON listing_categories(listing_id);
CREATE INDEX idx_listing_categories_category ON listing_categories(category_id);
CREATE INDEX idx_listing_categories_sub_category ON listing_categories(sub_category_id);
CREATE INDEX idx_listing_categories_primary ON listing_categories(is_primary);
COMMENT ON TABLE listing_categories IS 'Many-to-many relationship between listings and categories/sub-categories.';

CREATE TABLE listing_stats (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
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
    CONSTRAINT uq_listing_stats UNIQUE (listing_id),
    CONSTRAINT chk_rating_range CHECK (rating IS NULL OR (rating >= 0 AND rating <= 5)),
    CONSTRAINT chk_rating_count_positive CHECK (rating_count >= 0),
    CONSTRAINT chk_follow_count_positive CHECK (follow_count >= 0),
    CONSTRAINT chk_likes_count_positive CHECK (likes_count >= 0),
    CONSTRAINT chk_view_count_positive CHECK (view_count >= 0),
    CONSTRAINT chk_save_count_positive CHECK (save_count >= 0),
    CONSTRAINT chk_share_count_positive CHECK (share_count >= 0),
    CONSTRAINT chk_enquiry_count_positive CHECK (enquiry_count >= 0),
    CONSTRAINT chk_news_count_positive CHECK (news_count >= 0)
);
CREATE TRIGGER update_listing_stats_updated_at BEFORE UPDATE ON listing_stats FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_listing_stats_listing ON listing_stats(listing_id);
COMMENT ON TABLE listing_stats IS 'Engagement and performance metrics for listings.';

CREATE TABLE listing_cities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    city VARCHAR(100) NOT NULL,
    state VARCHAR(100),
    country VARCHAR(100) DEFAULT 'India',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_listing_cities_listing ON listing_cities(listing_id);
CREATE INDEX idx_listing_cities_city ON listing_cities(city);
COMMENT ON TABLE listing_cities IS 'Cities where listing operates or is available.';

CREATE TABLE listing_social_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    instagram_url VARCHAR(255),
    facebook_url VARCHAR(255),
    twitter_url VARCHAR(255),
    linkedin_url VARCHAR(255),
    youtube_url VARCHAR(255),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_social_links UNIQUE (listing_id),
    CONSTRAINT chk_instagram_url CHECK (instagram_url IS NULL OR instagram_url ~* '^https?://'),
    CONSTRAINT chk_facebook_url CHECK (facebook_url IS NULL OR facebook_url ~* '^https?://'),
    CONSTRAINT chk_twitter_url CHECK (twitter_url IS NULL OR twitter_url ~* '^https?://'),
    CONSTRAINT chk_linkedin_url CHECK (linkedin_url IS NULL OR linkedin_url ~* '^https?://'),
    CONSTRAINT chk_youtube_url CHECK (youtube_url IS NULL OR youtube_url ~* '^https?://')
);
CREATE TRIGGER update_listing_social_links_updated_at BEFORE UPDATE ON listing_social_links FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_listing_social_links_listing ON listing_social_links(listing_id);
COMMENT ON TABLE listing_social_links IS 'Social media profile URLs for listings.';

CREATE TABLE listing_documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    document_type VARCHAR(100) NOT NULL,
    s3_url VARCHAR(512) NOT NULL,
    status VARCHAR(50) DEFAULT 'pending',
    uploaded_by VARCHAR(255),
    rejection_reason TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_doc_status_valid CHECK (status IN ('pending', 'verified', 'rejected'))
);
CREATE TRIGGER update_listing_documents_updated_at BEFORE UPDATE ON listing_documents FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_listing_documents_listing ON listing_documents(listing_id);
CREATE INDEX idx_listing_documents_status ON listing_documents(status);
COMMENT ON TABLE listing_documents IS 'Stores documents associated with a listing (e.g. GST, PAN, pitch decks).';

-- ============================================================
-- FRANCHISE SPECIFIC RELATIONS
-- ============================================================
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
CREATE TRIGGER update_franchise_business_overview_updated_at BEFORE UPDATE ON franchise_business_overview FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_business_overview_franchise_id ON franchise_business_overview(franchise_id);
COMMENT ON TABLE franchise_business_overview IS 'Products and services offered by franchise.';

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
    CONSTRAINT chk_royalty_percentage CHECK (royalty_percentage IS NULL OR (royalty_percentage >= 0 AND royalty_percentage <= 100)),
    CONSTRAINT chk_marketing_fee_percentage CHECK (marketing_fee_percentage IS NULL OR (marketing_fee_percentage >= 0 AND marketing_fee_percentage <= 100)),
    CONSTRAINT chk_roi_min_percentage CHECK (roi_min_percentage IS NULL OR (roi_min_percentage >= 0 AND roi_min_percentage <= 100)),
    CONSTRAINT chk_roi_max_percentage CHECK (roi_max_percentage IS NULL OR (roi_max_percentage >= 0 AND roi_max_percentage <= 100)),
    CONSTRAINT chk_investment_range CHECK (initial_investment_min IS NULL OR initial_investment_max IS NULL OR initial_investment_min <= initial_investment_max),
    CONSTRAINT chk_payback_range CHECK (payback_min_months IS NULL OR payback_max_months IS NULL OR payback_min_months <= payback_max_months),
    CONSTRAINT chk_roi_range CHECK (roi_min_percentage IS NULL OR roi_max_percentage IS NULL OR roi_min_percentage <= roi_max_percentage),
    CONSTRAINT chk_monthly_turnover_range CHECK (monthly_turnover_min IS NULL OR monthly_turnover_max IS NULL OR monthly_turnover_min <= monthly_turnover_max),
    CONSTRAINT chk_single_unit_cost_range CHECK (single_unit_cost_min IS NULL OR single_unit_cost_max IS NULL OR single_unit_cost_min <= single_unit_cost_max),
    CONSTRAINT chk_initial_investment_min_positive CHECK (initial_investment_min IS NULL OR initial_investment_min >= 0),
    CONSTRAINT chk_initial_investment_max_positive CHECK (initial_investment_max IS NULL OR initial_investment_max >= 0),
    CONSTRAINT chk_franchise_fee_positive CHECK (franchise_fee IS NULL OR franchise_fee >= 0),
    CONSTRAINT chk_monthly_turnover_min_positive CHECK (monthly_turnover_min IS NULL OR monthly_turnover_min >= 0),
    CONSTRAINT chk_monthly_turnover_max_positive CHECK (monthly_turnover_max IS NULL OR monthly_turnover_max >= 0)
);
CREATE TRIGGER update_franchise_investment_requirement_updated_at BEFORE UPDATE ON franchise_investment_requirement FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_investment_franchise_id ON franchise_investment_requirement(franchise_id);
COMMENT ON TABLE franchise_investment_requirement IS 'Financial requirements and projections for franchisees. All amounts in INR.';

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
    CONSTRAINT chk_space_range CHECK (space_min_sqft IS NULL OR space_max_sqft IS NULL OR space_min_sqft <= space_max_sqft),
    CONSTRAINT chk_staff_range CHECK (staff_required_min IS NULL OR staff_required_max IS NULL OR staff_required_min <= staff_required_max),
    CONSTRAINT chk_space_min_positive CHECK (space_min_sqft IS NULL OR space_min_sqft > 0),
    CONSTRAINT chk_space_max_positive CHECK (space_max_sqft IS NULL OR space_max_sqft > 0),
    CONSTRAINT chk_staff_min_positive CHECK (staff_required_min IS NULL OR staff_required_min >= 0),
    CONSTRAINT chk_staff_max_positive CHECK (staff_required_max IS NULL OR staff_required_max >= 0)
);
CREATE TRIGGER update_franchise_operations_updated_at BEFORE UPDATE ON franchise_operations FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_operations_franchise_id ON franchise_operations(franchise_id);
COMMENT ON TABLE franchise_operations IS 'Operational requirements and support details for running the franchise.';

-- Applications are Franchise Specific
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
    CONSTRAINT chk_status_valid CHECK (status IN ('submitted', 'under_review', 'approved', 'rejected', 'withdrawn'))
);
CREATE UNIQUE INDEX uq_application_active ON franchise_applications(seeker_id, franchise_id) WHERE status IN ('submitted', 'under_review');
CREATE TRIGGER update_franchise_applications_updated_at BEFORE UPDATE ON franchise_applications FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_applications_seeker ON franchise_applications(seeker_id);
CREATE INDEX idx_applications_franchise ON franchise_applications(franchise_id);
CREATE INDEX idx_applications_status ON franchise_applications(status);
CREATE INDEX idx_applications_idempotency ON franchise_applications(idempotency_key);
COMMENT ON TABLE franchise_applications IS 'Franchise applications submitted by seekers.';

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
COMMENT ON TABLE application_history IS 'Audit log for franchise application status changes.';

-- ============================================================
-- UNIVERSAL SYSTEM TABLES (Linked to `listings`)
-- ============================================================
CREATE TABLE user_favorites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_user_listing_favorite UNIQUE (user_id, listing_id)
);
CREATE INDEX idx_user_favorites_user ON user_favorites(user_id);
CREATE INDEX idx_user_favorites_listing ON user_favorites(listing_id);
COMMENT ON TABLE user_favorites IS 'User saved/favorited entities.';

CREATE TABLE saved_searches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    filters JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_user_search_name UNIQUE (user_id, name)
);
CREATE TRIGGER update_saved_searches_updated_at BEFORE UPDATE ON saved_searches FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_saved_searches_user ON saved_searches(user_id);
COMMENT ON TABLE saved_searches IS 'User saved search filters.';

CREATE TABLE notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    notification_type VARCHAR(100) NOT NULL,
    recipient_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    application_id UUID REFERENCES franchise_applications(id) ON DELETE SET NULL,
    listing_id UUID REFERENCES listings(id) ON DELETE SET NULL,
    subject VARCHAR(255),
    message TEXT,
    channel VARCHAR(50) NOT NULL DEFAULT 'email',
    sent_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    idempotency_key VARCHAR(255) UNIQUE,
    metadata JSONB DEFAULT '{}'::jsonb,
    sent_date DATE GENERATED ALWAYS AS (sent_at::DATE) STORED,
    retain_until TIMESTAMP DEFAULT (NOW() + INTERVAL '180 days'),
    CONSTRAINT chk_channel_valid CHECK (channel IN ('email', 'sms', 'push', 'in_app'))
);
CREATE UNIQUE INDEX uq_notification_daily ON notifications(notification_type, recipient_id, COALESCE(application_id::TEXT, 'NULL'), sent_date);
CREATE INDEX idx_notifications_recipient ON notifications(recipient_id);
CREATE INDEX idx_notifications_application ON notifications(application_id);
CREATE INDEX idx_notifications_type ON notifications(notification_type);
CREATE INDEX idx_notifications_sent_at ON notifications(sent_at);
CREATE INDEX idx_notifications_idempotency ON notifications(idempotency_key);
COMMENT ON TABLE notifications IS 'All platform notifications sent to users.';

CREATE TABLE user_ratings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    rating DECIMAL(2,1) NOT NULL,
    review TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_user_listing_rating UNIQUE (user_id, listing_id),
    CONSTRAINT chk_rating_range CHECK (rating >= 1.0 AND rating <= 5.0)
);
CREATE TRIGGER update_user_ratings_updated_at BEFORE UPDATE ON user_ratings FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_user_ratings_user ON user_ratings(user_id);
CREATE INDEX idx_user_ratings_listing ON user_ratings(listing_id);
CREATE INDEX idx_user_ratings_rating ON user_ratings(rating);
COMMENT ON TABLE user_ratings IS 'User-submitted ratings (1-5) and reviews for listings.';

CREATE TABLE listing_shares (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    share_platform VARCHAR(50) DEFAULT 'copy_link',
    ip_address VARCHAR(45),
    shared_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    retain_until TIMESTAMP DEFAULT (NOW() + INTERVAL '365 days')
);
CREATE INDEX idx_listing_shares_listing ON listing_shares(listing_id);
CREATE INDEX idx_listing_shares_user ON listing_shares(user_id);
CREATE INDEX idx_listing_shares_platform ON listing_shares(share_platform);
CREATE INDEX idx_listing_shares_shared_at ON listing_shares(shared_at);
COMMENT ON TABLE listing_shares IS 'Tracks when users share an entity.';

CREATE TABLE enquiries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    message TEXT,
    preferred_contact VARCHAR(50) NOT NULL DEFAULT 'EMAIL',
    responded_at TIMESTAMP,
    closed_at TIMESTAMP,
    closed_by VARCHAR(50),
    last_activity_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_enquiry_status CHECK (status IN ('PENDING', 'RESPONDED', 'CLOSED')),
    CONSTRAINT chk_preferred_contact CHECK (preferred_contact IN ('EMAIL', 'PHONE', 'EITHER')),
    CONSTRAINT chk_closed_by CHECK (closed_by IS NULL OR closed_by IN ('USER', 'ASSOCIATION', 'SYSTEM'))
);
CREATE UNIQUE INDEX uq_enquiry_pending_active ON enquiries(user_id, listing_id) WHERE status = 'PENDING';
CREATE TRIGGER update_enquiries_updated_at BEFORE UPDATE ON enquiries FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_enquiries_user ON enquiries(user_id);
CREATE INDEX idx_enquiries_listing ON enquiries(listing_id);
CREATE INDEX idx_enquiries_status ON enquiries(status);
CREATE INDEX idx_enquiries_last_activity ON enquiries(last_activity_at DESC);
COMMENT ON TABLE enquiries IS 'Tracks user membership/general enquiries for listings.';

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
    CONSTRAINT chk_audit_from_status CHECK (from_status IS NULL OR from_status IN ('PENDING', 'RESPONDED', 'CLOSED')),
    CONSTRAINT chk_audit_to_status CHECK (to_status IN ('PENDING', 'RESPONDED', 'CLOSED')),
    CONSTRAINT chk_actor_role CHECK (actor_role IN ('ROLE_USER', 'ROLE_ASSOC_ADMIN', 'ROLE_PLATFORM_ADMIN', 'SYSTEM'))
);
CREATE INDEX idx_enquiry_audit_enquiry ON enquiry_audit_log(enquiry_id);
CREATE INDEX idx_enquiry_audit_created_at ON enquiry_audit_log(created_at DESC);
COMMENT ON TABLE enquiry_audit_log IS 'Immutably logs all status changes and activity for enquiries.';

CREATE TABLE pending_edits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
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
CREATE TRIGGER update_pending_edits_updated_at BEFORE UPDATE ON pending_edits FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_pending_edits_listing ON pending_edits(listing_id);
CREATE INDEX idx_pending_edits_status ON pending_edits(status);
COMMENT ON TABLE pending_edits IS 'Staged sensitive field changes for review on any listing.';

CREATE TABLE duplicate_flags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    matched_listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    similarity_score NUMERIC(5, 2) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    resolved_by UUID REFERENCES users(id) ON DELETE SET NULL,
    resolved_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_duplicate_flag_status CHECK (status IN ('PENDING', 'RESOLVED', 'IGNORED'))
);
CREATE INDEX idx_duplicate_flags_listing ON duplicate_flags(listing_id);
CREATE INDEX idx_duplicate_flags_matched_listing ON duplicate_flags(matched_listing_id);
COMMENT ON TABLE duplicate_flags IS 'Fuzzy duplicate warnings for newly submitted listings.';

CREATE TABLE verification_criteria (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    vc_type VARCHAR(50) NOT NULL, 
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING', 
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_listing_vc UNIQUE (listing_id, vc_type),
    CONSTRAINT chk_vc_status CHECK (status IN ('PENDING', 'PASSED', 'FAILED'))
);
CREATE TRIGGER update_verification_criteria_updated_at BEFORE UPDATE ON verification_criteria FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_verification_criteria_listing ON verification_criteria(listing_id);
COMMENT ON TABLE verification_criteria IS 'Pass/Fail state of verification steps linked to listings.';

CREATE TABLE offerings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    type VARCHAR(50) NOT NULL, 
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(50) NOT NULL DEFAULT 'DRAFT', 
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_offering_status CHECK (status IN ('DRAFT', 'PUBLISHED', 'DELETED'))
);
CREATE TRIGGER update_offerings_updated_at BEFORE UPDATE ON offerings FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_offerings_listing ON offerings(listing_id);
CREATE INDEX idx_offerings_status ON offerings(status);
COMMENT ON TABLE offerings IS 'Structured perks, discounts, directories, and webinars linked to a listing.';

CREATE TABLE memberships (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING', 
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_user_listing_membership UNIQUE (user_id, listing_id),
    CONSTRAINT chk_membership_status CHECK (status IN ('PENDING', 'ACTIVE', 'SUSPENDED', 'CANCELLED'))
);
CREATE TRIGGER update_memberships_updated_at BEFORE UPDATE ON memberships FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_memberships_user ON memberships(user_id);
CREATE INDEX idx_memberships_listing ON memberships(listing_id);
COMMENT ON TABLE memberships IS 'Approved listing-user relationship mapping.';

CREATE TABLE entity_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id UUID NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    action VARCHAR(50) NOT NULL,
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    old_values JSONB NOT NULL DEFAULT '{}'::jsonb,
    new_values JSONB NOT NULL DEFAULT '{}'::jsonb,
    notes TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_entity_audit_listing ON entity_audit_log(listing_id);
CREATE INDEX idx_entity_audit_created_at ON entity_audit_log(created_at DESC);
COMMENT ON TABLE entity_audit_log IS 'Immutably tracks status changes and updates on listings.';

-- ============================================================
-- MISCELLANEOUS TABLES
-- ============================================================
CREATE TABLE category_questions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reference_id UUID NOT NULL,
    entity_type VARCHAR(50) NOT NULL,
    question TEXT NOT NULL,
    intent_tag VARCHAR(50),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_category_questions_entity_type CHECK (entity_type IN ('franchise', 'association', 'master_franchise'))
);
CREATE INDEX idx_category_questions_reference ON category_questions(reference_id);
CREATE INDEX idx_category_questions_entity_type ON category_questions(entity_type);
COMMENT ON TABLE category_questions IS 'AI-driven FAQ questions linked to either industry or category.';

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
    CONSTRAINT uq_industry_intent UNIQUE (industry_id, intent_tag, entity_type),
    CONSTRAINT chk_industry_market_insights_entity_type CHECK (entity_type IN ('franchise', 'association', 'master_franchise'))
);
CREATE TRIGGER update_industry_market_insights_updated_at BEFORE UPDATE ON industry_market_insights FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE INDEX idx_industry_market_insights_industry_id ON industry_market_insights(industry_id);
CREATE INDEX idx_industry_market_insights_industry_slug ON industry_market_insights(industry_slug);
COMMENT ON TABLE industry_market_insights IS 'Market insights, growth rates, and trends for each industry.';

CREATE TABLE IF NOT EXISTS contact_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255)  NOT NULL,
    email       TEXT          NOT NULL,
    company     VARCHAR(255),
    phone       VARCHAR(100),
    message     TEXT          NOT NULL,
    ip_address  VARCHAR(45),                        
    created_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    retain_until TIMESTAMP WITH TIME ZONE DEFAULT (NOW() + INTERVAL '90 days')
);
CREATE INDEX IF NOT EXISTS idx_contact_messages_created_at ON contact_messages (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_contact_messages_ip_created ON contact_messages (ip_address, created_at DESC);

CREATE TABLE IF NOT EXISTS public_form_submissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    form_type VARCHAR(100) NOT NULL,
    email TEXT NOT NULL,
    phone VARCHAR(50),
    form_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(50) DEFAULT 'new',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    retain_until TIMESTAMP DEFAULT (NOW() + INTERVAL '90 days')
);
CREATE INDEX idx_public_form_submissions_type ON public_form_submissions(form_type);
CREATE INDEX idx_public_form_submissions_email ON public_form_submissions(email);
CREATE INDEX idx_public_form_submissions_created_at ON public_form_submissions(created_at DESC);

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
-- HELPER FUNCTIONS & VIEWS
-- ============================================================
CREATE OR REPLACE FUNCTION get_listing_hierarchy(p_listing_id UUID)
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
        lc.is_primary
    FROM listing_categories lc
    INNER JOIN categories c ON lc.category_id = c.id
    LEFT JOIN associations a ON lc.listing_id = a.id
    INNER JOIN industries i ON COALESCE(a.industry_id, c.industry_id) = i.id
    LEFT JOIN sub_categories sc ON lc.sub_category_id = sc.id
    WHERE lc.listing_id = p_listing_id
    ORDER BY lc.is_primary DESC, i.display_order, c.display_order, sc.display_order;
END;
$$ LANGUAGE plpgsql;
COMMENT ON FUNCTION get_listing_hierarchy(UUID) IS 'Returns complete industry -> category -> sub-category hierarchy for a listing.';

CREATE OR REPLACE FUNCTION get_listings_by_category(p_category_id UUID)
RETURNS TABLE (
    listing_id UUID,
    listing_name VARCHAR,
    listing_slug VARCHAR,
    sub_category_name VARCHAR,
    is_primary BOOLEAN
) AS $$
BEGIN
    RETURN QUERY
    SELECT 
        l.id as listing_id,
        l.name as listing_name,
        l.slug as listing_slug,
        sc.name as sub_category_name,
        lc.is_primary
    FROM listings l
    INNER JOIN listing_categories lc ON l.id = lc.listing_id
    LEFT JOIN sub_categories sc ON lc.sub_category_id = sc.id
    WHERE lc.category_id = p_category_id
    ORDER BY lc.is_primary DESC, l.name;
END;
$$ LANGUAGE plpgsql;
COMMENT ON FUNCTION get_listings_by_category(UUID) IS 'Returns all listings belonging to a specific category, with their sub-categories.';

CREATE OR REPLACE VIEW v_listing_taxonomy AS
SELECT 
    l.id as listing_id,
    l.name as listing_name,
    l.slug as listing_slug,
    l.entity_type,
    i.id as industry_id,
    i.name as industry_name,
    i.color_hex as industry_color,
    c.id as category_id,
    c.name as category_name,
    sc.id as sub_category_id,
    sc.name as sub_category_name,
    lc.is_primary
FROM listings l
LEFT JOIN associations a ON l.id = a.id
INNER JOIN listing_categories lc ON l.id = lc.listing_id
INNER JOIN categories c ON lc.category_id = c.id
INNER JOIN industries i ON COALESCE(a.industry_id, c.industry_id) = i.id
LEFT JOIN sub_categories sc ON lc.sub_category_id = sc.id;
COMMENT ON VIEW v_listing_taxonomy IS 'Denormalized view showing complete listing -> category -> industry relationships.';

CREATE OR REPLACE VIEW v_industry_stats AS
SELECT 
    i.id,
    i.name,
    i.color_hex,
    i.slug,
    COUNT(DISTINCT c.id) as category_count,
    COUNT(DISTINCT sc.id) as subcategory_count,
    COUNT(DISTINCT lc.listing_id) as listing_count
FROM industries i
LEFT JOIN categories c ON i.id = c.industry_id
LEFT JOIN sub_categories sc ON c.id = sc.category_id
LEFT JOIN listing_categories lc ON c.id = lc.category_id
WHERE i.is_active = TRUE
GROUP BY i.id, i.name, i.color_hex, i.slug
ORDER BY i.display_order;
COMMENT ON VIEW v_industry_stats IS 'Statistics showing count of categories, sub-categories, and listings per industry.';

CREATE OR REPLACE FUNCTION cleanup_expired_idempotency_keys()
RETURNS void AS $$
BEGIN
    DELETE FROM idempotency_keys WHERE expires_at < CURRENT_TIMESTAMP;
END;
$$ LANGUAGE plpgsql;
COMMENT ON FUNCTION cleanup_expired_idempotency_keys() IS 'Cleanup function for expired idempotency keys. Run via cron job daily.';

CREATE OR REPLACE FUNCTION cleanup_expired_guest_audit_events()
RETURNS void AS $$
BEGIN
    DELETE FROM guest_audit_log WHERE retain_until < NOW();
END;
$$ LANGUAGE plpgsql;
COMMENT ON FUNCTION cleanup_expired_guest_audit_events() IS 'Deletes guest audit events past their retention period. Run via cron job daily.';

-- ============================================================
-- USER PROFILE EXTENDED TABLES
-- ============================================================

-- ========================================
-- USER PROFESSIONAL DETAILS
-- ========================================
CREATE TABLE IF NOT EXISTS user_professional_details (
    user_id           UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    occupation        VARCHAR(255),
    designation       VARCHAR(255),
    experience        VARCHAR(50),
    prior_experience  BOOLEAN DEFAULT false,
    industry_id       UUID REFERENCES industries(id) ON DELETE SET NULL,
    industry          VARCHAR(255),
    created_at        TIMESTAMPTZ DEFAULT NOW(),
    updated_at        TIMESTAMPTZ DEFAULT NOW()
);

CREATE TRIGGER update_user_professional_details_updated_at
    BEFORE UPDATE ON user_professional_details
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE user_professional_details IS
    'Extended professional information for users. Occupation, designation, experience, and industry classification.';

-- ========================================
-- USER COMPANY DETAILS
-- ========================================
CREATE TABLE IF NOT EXISTS user_company_details (
    user_id              UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    business_name        VARCHAR(500),
    business_type        VARCHAR(100),
    industry_sector      VARCHAR(255),
    year_established     INTEGER,
    cin_registration     VARCHAR(100),
    gst_number           VARCHAR(100),
    annual_turnover      VARCHAR(50),
    company_website      VARCHAR(500),
    company_phone        VARCHAR(100),
    registered_address   TEXT,
    company_description  TEXT,
    created_at           TIMESTAMPTZ DEFAULT NOW(),
    updated_at           TIMESTAMPTZ DEFAULT NOW()
);

CREATE TRIGGER update_user_company_details_updated_at
    BEFORE UPDATE ON user_company_details
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE user_company_details IS
    'Extended company/business information for users. Business name, type, industry, registration details, and contact information.';

-- ========================================
-- USER INVESTMENT DETAILS
-- ========================================
CREATE TABLE IF NOT EXISTS user_investment_details (
    user_id                    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    min_investment             VARCHAR(100),
    max_investment             VARCHAR(100),
    liquid_capital_available   VARCHAR(100),
    funding_source             VARCHAR(100),
    roi_timeline               VARCHAR(50),
    expected_annual_roi        VARCHAR(50),
    preferred_sectors          TEXT[],
    preferred_categories       TEXT[],
    created_at                 TIMESTAMPTZ DEFAULT NOW(),
    updated_at                 TIMESTAMPTZ DEFAULT NOW()
);

CREATE TRIGGER update_user_investment_details_updated_at
    BEFORE UPDATE ON user_investment_details
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE user_investment_details IS
    'Investment preferences and financial information for users. Investment ranges, funding sources, ROI expectations, and preferred sectors.';

-- ========================================
-- USER PREFERENCES
-- ========================================
CREATE TABLE IF NOT EXISTS user_preferences (
    user_id                 UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    theme                   VARCHAR(20) DEFAULT 'system',
    email_notifications     BOOLEAN DEFAULT true,
    push_notifications      BOOLEAN DEFAULT true,
    sms_notifications       BOOLEAN DEFAULT false,
    language                VARCHAR(10) DEFAULT 'en',
    timezone                VARCHAR(50),
    notification_settings   JSONB DEFAULT '{}'::jsonb,
    created_at              TIMESTAMPTZ DEFAULT NOW(),
    updated_at              TIMESTAMPTZ DEFAULT NOW()
);

CREATE TRIGGER update_user_preferences_updated_at
    BEFORE UPDATE ON user_preferences
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE user_preferences IS
    'User preferences for theme, language, timezone, and notification settings.';

-- ========================================
-- PROFILE AUDIT LOG
-- ========================================
CREATE TABLE IF NOT EXISTS profile_audit_log (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action      VARCHAR(50) NOT NULL,
    field       VARCHAR(100),
    old_value   TEXT,
    new_value   TEXT,
    changes     JSONB,
    source      VARCHAR(50),
    request_id  VARCHAR(100),
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_profile_audit_log_user ON profile_audit_log(user_id);
CREATE INDEX idx_profile_audit_log_action ON profile_audit_log(action);
CREATE INDEX idx_profile_audit_log_created_at ON profile_audit_log(created_at DESC);
CREATE INDEX idx_profile_audit_log_request ON profile_audit_log(request_id);

COMMENT ON TABLE profile_audit_log IS
    'Immutable audit log for all user profile changes. Tracks old/new values for compliance and debugging.';

-- ========================================
-- USER CONSENTS (GDPR Article 7 / DPDPA Section 6)
-- ========================================
CREATE TABLE IF NOT EXISTS user_consents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    consent_type    VARCHAR(100) NOT NULL,
    version         VARCHAR(50) NOT NULL,
    granted         BOOLEAN NOT NULL DEFAULT true,
    granted_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    withdrawn_at    TIMESTAMPTZ,
    ip_address      VARCHAR(45),
    source          VARCHAR(50) NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_consents_user ON user_consents(user_id);
CREATE INDEX idx_user_consents_type ON user_consents(consent_type);
CREATE INDEX idx_user_consents_granted ON user_consents(granted, granted_at DESC);

COMMENT ON TABLE user_consents IS
    'GDPR Article 7 / DPDPA Section 6 compliance. Records what users consented to, when, which version, and withdrawal events.';

-- ========================================
-- FEEDBACK
-- ========================================
CREATE TABLE IF NOT EXISTS feedback (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    type        VARCHAR(50) NOT NULL,
    message     TEXT NOT NULL,
    status      VARCHAR(20) DEFAULT 'open',
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_feedback_user ON feedback(user_id);
CREATE INDEX idx_feedback_status ON feedback(status);

COMMENT ON TABLE feedback IS
    'User feedback submissions. Types and statuses validated at app layer via config/dropdowns.yaml.';

-- ============================================================
-- DPDPA CLEANUP FUNCTIONS FOR ANONYMOUS PII TABLES
-- ============================================================

-- Cleanup expired contact messages (90-day retention)
CREATE OR REPLACE FUNCTION cleanup_expired_contact_messages()
RETURNS void AS $$
BEGIN
    DELETE FROM contact_messages WHERE retain_until < NOW();
END;
$$ LANGUAGE plpgsql;
COMMENT ON FUNCTION cleanup_expired_contact_messages() IS
    'Deletes contact messages past their 90-day retention period. Run via cron job daily. DPDPA compliance.';

-- Cleanup expired public form submissions (90-day retention)
CREATE OR REPLACE FUNCTION cleanup_expired_form_submissions()
RETURNS void AS $$
BEGIN
    DELETE FROM public_form_submissions WHERE retain_until < NOW();
END;
$$ LANGUAGE plpgsql;
COMMENT ON FUNCTION cleanup_expired_form_submissions() IS
    'Deletes public form submissions past their 90-day retention period. Run via cron job daily. DPDPA compliance.';

-- Anonymize old share IPs (365-day retention)
CREATE OR REPLACE FUNCTION cleanup_expired_share_ips()
RETURNS void AS $$
BEGIN
    UPDATE listing_shares
    SET ip_address = NULL
    WHERE ip_address IS NOT NULL
      AND shared_at < NOW() - INTERVAL '365 days';
END;
$$ LANGUAGE plpgsql;
COMMENT ON FUNCTION cleanup_expired_share_ips() IS
    'Anonymizes IP addresses in listing_shares past their 365-day retention period. Run via cron job weekly. DPDPA compliance.';

-- Cleanup expired notifications (180-day retention)
CREATE OR REPLACE FUNCTION cleanup_expired_notifications()
RETURNS void AS $$
BEGIN
    DELETE FROM notifications WHERE retain_until < NOW();
END;
$$ LANGUAGE plpgsql;
COMMENT ON FUNCTION cleanup_expired_notifications() IS
    'Deletes notifications past their 180-day retention period. Run via cron job daily. DPDPA compliance.';

-- ==========================================
-- AUTHOR PROFILES (For Blog Creators)
-- ==========================================
CREATE TABLE author_profiles (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    full_name VARCHAR(255) NOT NULL,
    author_name VARCHAR(255) NOT NULL,
    bio TEXT,
    profile_picture_url VARCHAR(2048),
    categories TEXT[] DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TRIGGER update_author_profiles_updated_at
    BEFORE UPDATE ON author_profiles
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
