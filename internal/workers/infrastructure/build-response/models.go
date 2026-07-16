package buildresponse

// Input - Now using pageType instead of templateId
type Input struct {
	PageType   string                 `json:"pageType"` // "home", "listing", "detail", "search"
	EntityType string                 `json:"entityType,omitempty"`
	Data       map[string]interface{} `json:"data"`     // All data from workflow (can be nested)
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	ExtractedParams map[string]interface{} `json:"extractedParams,omitempty"`

	// HOME PAGE FIELDS
	HeroBrands      []interface{} `json:"heroBrands,omitempty"`
	Industries      []interface{} `json:"industries,omitempty"`
	PopularListings []interface{} `json:"popularListings,omitempty"`
	Categories      []interface{} `json:"categories,omitempty"`

	// LISTING PAGE FIELDS
	FranchiseListings     []interface{} `json:"franchiseListings,omitempty"`
	FeaturedCategories    []interface{} `json:"featuredCategories,omitempty"`
	UnderstandingCategory []interface{} `json:"understandingCategory,omitempty"`
	RecommendedFranchises []interface{} `json:"recommendedFranchises,omitempty"`
	KeyMarketInsights     []interface{} `json:"keyMarketInsights,omitempty"`
	HeroDescription       string        `json:"heroDescription,omitempty"`

	TotalCount float64 `json:"totalCount,omitempty"`
	Page       float64 `json:"page,omitempty"`
	PageSize   float64 `json:"pageSize,omitempty"`

	// DETAIL PAGE FIELDS
	BasicInfo         map[string]interface{} `json:"basicInfo,omitempty"`
	Business          map[string]interface{} `json:"business,omitempty"`
	Investment        map[string]interface{} `json:"investment,omitempty"`
	Operations        map[string]interface{} `json:"operations,omitempty"`
	Overview          map[string]interface{} `json:"overview,omitempty"`
	Social            []interface{}          `json:"social,omitempty"`
	Recommended       []interface{}          `json:"recommended,omitempty"`
	MarketInsights    interface{}            `json:"marketInsights,omitempty"`
	CategoryQuestions []interface{}          `json:"categoryQuestions,omitempty"`
	MatchScore        interface{}            `json:"matchScore,omitempty"`
	MatchFactors      map[string]interface{} `json:"matchFactors,omitempty"`
}

// Output - Simplified structure (matches what build-response returns)
type Output struct {
	Success  bool                   `json:"success"`
	Response map[string]interface{} `json:"response"`
}

// HomePageSection - For better type safety in handlers
type HomePageSection struct {
	Type    string                 `json:"type"`
	Enabled bool                   `json:"enabled"`
	Data    map[string]interface{} `json:"data"`
}

// HomePageResponse - Structured response for home page
type HomePageResponse struct {
	PageID   string            `json:"pageId"`
	Sections []HomePageSection `json:"sections"`
}

// ListingPageResponse - Structured response for listing page
type ListingPageResponse struct {
	PageID       string                 `json:"pageId"`
	Sections     []HomePageSection      `json:"sections"`
	SearchParams map[string]interface{} `json:"searchParams,omitempty"`
	Page         int                    `json:"page,omitempty"`
	Limit        int                    `json:"limit,omitempty"`
	Total        int                    `json:"total,omitempty"`
}

// DetailPageResponse - Structured response for detail page
type DetailPageResponse struct {
	Slug                string                 `json:"slug,omitempty"`
	BasicInfo           map[string]interface{} `json:"basicInfo,omitempty"`
	FranchisingOverview map[string]interface{} `json:"franchising_overview,omitempty"`
	BusinessOverview    map[string]interface{} `json:"business_overview,omitempty"`
	InvestmentDetails   map[string]interface{} `json:"investment_details,omitempty"`
	Operation           map[string]interface{} `json:"operation,omitempty"`
	SocialLinks         map[string]interface{} `json:"social_links,omitempty"`
	FeaturedCategories  []interface{}          `json:"featured_categories,omitempty"`
	Recommended         []interface{}          `json:"recommended,omitempty"`
}

// SearchPageResponse - Structured response for search page
type SearchPageResponse struct {
	Franchises     []interface{}          `json:"franchises,omitempty"`
	Total          int                    `json:"total,omitempty"`
	Page           int                    `json:"page,omitempty"`
	Limit          int                    `json:"limit,omitempty"`
	FiltersApplied map[string]interface{} `json:"filters_applied,omitempty"`
}

// ResponseMetadata - Standard metadata for all responses
type ResponseMetadata struct {
	GeneratedAt string `json:"generatedAt"`
	Source      string `json:"source"`
	PageType    string `json:"pageType"`
}

// FullResponse - Complete response structure
type FullResponse struct {
	Success  bool             `json:"success"`
	Data     interface{}      `json:"data"` // Can be any of the *PageResponse structs
	Metadata ResponseMetadata `json:"metadata"`
}
