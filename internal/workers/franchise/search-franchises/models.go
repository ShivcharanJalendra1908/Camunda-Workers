package searchfranchises

// Input represents the search request parameters
type Input struct {
	Query         string   `json:"query"`
	Category      string   `json:"category"`
	Industry      string   `json:"industry"` // ✅ NEW FIELD
	Location      string   `json:"location"`
	MinInvestment float64  `json:"min_investment"`
	MaxInvestment float64  `json:"max_investment"`
	MinSpace      float64  `json:"min_space"`
	MaxSpace      float64  `json:"max_space"`
	MinSize       float64  `json:"min_size"`
	MaxSize       float64  `json:"max_size"`
	MinFee        float64  `json:"min_fee"`
	MaxFee        float64  `json:"max_fee"`
	MinRating     float64  `json:"min_rating"`
	Tags          []string `json:"tags"`
	Page          int      `json:"page"`
	Limit         int      `json:"limit"`
	SortBy        string   `json:"sort_by"`
	SortOrder     string   `json:"sort_order"`
	UserId          string   `json:"user_id"`
	SessionId       string   `json:"session_id"`
	EntityType      string   `json:"entityType"`
	ExclusivityType string   `json:"exclusivityType"`
	TerritoryScope  string   `json:"territoryScope"`
	MinUnits        int      `json:"minUnits"`
	LocalBrandsOnly bool     `json:"localBrandsOnly"`
	IsFeaturedOnly  bool     `json:"isFeaturedOnly"`
	IsSponsoredOnly bool     `json:"isSponsoredOnly"`
}

// SearchRequest represents the Elasticsearch query structure
type SearchRequest struct {
	Query        map[string]interface{}   `json:"query"`
	From         int                      `json:"from"`
	Size         int                      `json:"size"`
	Sort         []map[string]interface{} `json:"sort"`
	Aggregations map[string]interface{}   `json:"aggs,omitempty"`
}

// Output represents the search response
type Output struct {
	SearchResults  []map[string]interface{} `json:"search_results"`
	TotalCount     int                      `json:"total_count"`
	QueryTimeMs    int64                    `json:"query_time_ms"`
	Suggestions    []string                 `json:"suggestions,omitempty"`
	AppliedFilters map[string]interface{}   `json:"applied_filters"`
	Success        bool                     `json:"success"`
}
