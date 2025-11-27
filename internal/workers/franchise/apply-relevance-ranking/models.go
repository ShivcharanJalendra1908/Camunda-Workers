// internal/workers/franchise/apply-relevance-ranking/models.go
package applyrelevanceranking

type Input struct {
	SearchResults []SearchResult    `json:"searchResults"`
	DetailsData   []FranchiseDetail `json:"detailsData"`
	UserProfile   UserProfile       `json:"userProfile"`
}

type SearchResult struct {
	ID     string                 `json:"id"`
	Score  float64                `json:"score"` // Elasticsearch _score
	Source map[string]interface{} `json:"_source"`
}

type FranchiseDetail struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	InvestmentMin    int      `json:"investmentMin"`
	InvestmentMax    int      `json:"investmentMax"`
	Category         string   `json:"category"`
	Locations        []string `json:"locations"`
	UpdatedAt        string   `json:"updatedAt"` // ISO 8601
	ApplicationCount int      `json:"applicationCount"`
	ViewCount        int      `json:"viewCount"`
}

type UserProfile struct {
	CapitalAvailable int      `json:"capitalAvailable"`
	LocationPrefs    []string `json:"locationPreferences"`
	Interests        []string `json:"interests"`
	ExperienceYears  int      `json:"industryExperience"`
}

type Output struct {
	RankedFranchises []RankedFranchise `json:"rankedFranchises"`
}

type RankedFranchise struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	FinalScore      float64 `json:"finalScore"`
	ESScore         float64 `json:"esScore"`
	MatchScore      float64 `json:"matchScore"`
	PopularityScore float64 `json:"popularityScore"`
	FreshnessScore  float64 `json:"freshnessScore"`
}
