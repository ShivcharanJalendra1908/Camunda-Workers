package models

type FranchiseSearchFilters struct {
	Query         string   `form:"q" json:"query"`
	EntityType    string   `form:"entity_type"`
	Category      string   `form:"category"`
	Location      string   `form:"location"`
	MinInvestment int      `form:"min_investment"`
	MaxInvestment int      `form:"max_investment"`
	MinSpace      int      `form:"min_space"`
	MaxSpace      int      `form:"max_space"`
	Tags          []string `form:"tags"`
	MinRating     float64  `form:"min_rating"`
	MinFee        float64  `form:"min_fee" json:"min_fee"`
	MaxFee        float64  `form:"max_fee" json:"max_fee"`
	MinMembers    int      `form:"min_members" json:"min_members"`
	MaxMembers    int      `form:"max_members" json:"max_members"`
	SortBy        string   `form:"sort_by"`    // rating, investment, relevance, alphabetical, newest, popularity
	SortOrder     string   `form:"sort_order"` // asc, desc
	Page          int      `form:"page"`
	Limit         int      `form:"limit"`
	IncludeStats  bool     `form:"include_stats"`
}

type FranchiseSearchResult struct {
	Franchises []Franchise            `json:"franchises"`
	Pagination Pagination             `json:"pagination"`
	Filters    FranchiseSearchFilters `json:"filters_applied"`
	Stats      map[string]interface{} `json:"stats,omitempty"`
}

type Pagination struct {
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// FranchiseSearchWorkerRequest represents the request for franchise search worker
type FranchiseSearchWorkerRequest struct {
	Query         string   `json:"query"`
	Category      string   `json:"category"`
	Location      string   `json:"location"`
	MinInvestment int      `json:"min_investment"`
	MaxInvestment int      `json:"max_investment"`
	MinSpace      int      `json:"min_space"`
	MaxSpace      int      `json:"max_space"`
	Tags          []string `json:"tags"`
	MinRating     float64  `json:"min_rating"`
	UserId        string   `json:"user_id"`
	SessionId     string   `json:"session_id"`
}

// FranchiseSearchWorkerResponse represents the response from franchise search worker
type FranchiseSearchWorkerResponse struct {
	Franchises     []map[string]interface{} `json:"franchises"`
	TotalCount     int                      `json:"total_count"`
	QueryTime      int64                    `json:"query_time_ms"`
	Suggestions    []string                 `json:"suggestions,omitempty"`
	FiltersApplied FranchiseSearchFilters   `json:"filters_applied"`
}
