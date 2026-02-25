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
	MinRating     float64  `json:"min_rating"`
	Tags          []string `json:"tags"`
	Page          int      `json:"page"`
	Limit         int      `json:"limit"`
	SortBy        string   `json:"sort_by"`
	SortOrder     string   `json:"sort_order"`
	UserId        string   `json:"user_id"`
	SessionId     string   `json:"session_id"`
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
	Franchises     []map[string]interface{} `json:"franchises"`
	TotalCount     int                      `json:"total_count"`
	QueryTimeMs    int64                    `json:"query_time_ms"`
	Suggestions    []string                 `json:"suggestions,omitempty"`
	AppliedFilters map[string]interface{}   `json:"applied_filters"`
	Success        bool                     `json:"success"`
}
