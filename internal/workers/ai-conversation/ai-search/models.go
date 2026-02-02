package ai_search

type SearchInput struct {
	Query    string                 `json:"query"`
	UserID   string                 `json:"user_id,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type ExtractedParameters struct {
	Category      string            `json:"category,omitempty"`
	Location      *LocationFilter   `json:"location,omitempty"`
	ROI           *RangeFilter      `json:"roi,omitempty"`
	Investment    *InvestmentFilter `json:"investment,omitempty"`
	Space         *RangeFilter      `json:"space,omitempty"`
	Staff         *RangeFilter      `json:"staff,omitempty"`
	Outlets       *int              `json:"outlets,omitempty"`
	Rating        *float64          `json:"rating,omitempty"`
	Verified      *bool             `json:"verified,omitempty"`
	TrustedSeller *bool             `json:"trusted_seller,omitempty"`
}

type LocationFilter struct {
	City    string `json:"city,omitempty"`
	State   string `json:"state,omitempty"`
	Country string `json:"country,omitempty"`
}

type RangeFilter struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

type InvestmentFilter struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

type SearchResults struct {
	Total    int64                    `json:"total"`
	MaxScore float64                  `json:"max_score"`
	Hits     []map[string]interface{} `json:"hits"`
	TookMs   int64                    `json:"took_ms"`
}
