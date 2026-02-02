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

// package searchfranchises

// // Input variables for the worker
// type Input struct {
// 	Query             string                 `json:"query"`
// 	Category          string                 `json:"category"`
// 	Industry          string                 `json:"industry"`
// 	Location          string                 `json:"location"`
// 	MinInvestment     float64                `json:"min_investment"`
// 	MaxInvestment     float64                `json:"max_investment"`
// 	MinSpace          float64                `json:"min_space"`
// 	MaxSpace          float64                `json:"max_space"`
// 	Tags              []string               `json:"tags"`
// 	MinRating         float64                `json:"min_rating"`
// 	Page              int                    `json:"page"`
// 	Limit             int                    `json:"limit"`
// 	SortBy            string                 `json:"sort_by"`
// 	SortOrder         string                 `json:"sort_order"`
// 	UserId            string                 `json:"user_id"`
// 	SessionId         string                 `json:"session_id"`
// 	AdditionalFilters map[string]interface{} `json:"additional_filters"`
// }

// // Output variables from the worker
// type Output struct {
// 	Franchises     []map[string]interface{} `json:"franchises"`
// 	TotalCount     int                      `json:"total_count"`
// 	QueryTimeMs    int64                    `json:"query_time_ms"`
// 	Suggestions    []string                 `json:"suggestions,omitempty"`
// 	Aggregations   map[string]interface{}   `json:"aggregations,omitempty"`
// 	AppliedFilters map[string]interface{}   `json:"applied_filters"`
// 	ErrorMessage   string                   `json:"error_message,omitempty"`
// 	Success        bool                     `json:"success"`
// }

// // Franchise document structure
// type FranchiseDocument struct {
// 	ID            string   `json:"_id"`
// 	Brand         string   `json:"brand"`
// 	Location      string   `json:"location"`
// 	Since         int      `json:"since"`
// 	Description   string   `json:"description"`
// 	Category      string   `json:"category"`
// 	Rating        float64  `json:"rating"`
// 	Space         string   `json:"space"`
// 	SpaceMin      int      `json:"space_min"`
// 	SpaceMax      int      `json:"space_max"`
// 	Tags          []string `json:"tags"`
// 	NoOfOutlets   string   `json:"no_of_outlets"`
// 	Investment    string   `json:"investment"`
// 	InvestmentMin int      `json:"investment_min"`
// 	InvestmentMax int      `json:"investment_max"`
// 	Verified      bool     `json:"verified"`
// 	Highlights    string   `json:"highlights"`
// 	SubCategory   string   `json:"sub_category,omitempty"`
// 	EducationType string   `json:"education_type,omitempty"`
// 	FashionType   string   `json:"fashion_type,omitempty"`
// 	LogoLink      string   `json:"logo_link"`
// }

// // Search request for Elasticsearch
// type SearchRequest struct {
// 	Index        string                   `json:"index"`
// 	Query        map[string]interface{}   `json:"query"`
// 	From         int                      `json:"from"`
// 	Size         int                      `json:"size"`
// 	Sort         []map[string]interface{} `json:"sort"`
// 	Aggregations map[string]interface{}   `json:"aggs,omitempty"`
// 	Suggest      map[string]interface{}   `json:"suggest,omitempty"`
// 	Highlight    map[string]interface{}   `json:"highlight,omitempty"`
// }
