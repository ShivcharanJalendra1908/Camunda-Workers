// internal/workers/franchise/calculate-match-score/models.go
package calculatematchscore

type Input struct {
	UserID        string        `json:"userId"`
	FranchiseData FranchiseData `json:"franchiseData"`
	UserProfile   *UserProfile  `json:"userProfile,omitempty"`
}

type FranchiseData struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	InvestmentMin int      `json:"investmentMin"`
	InvestmentMax int      `json:"investmentMax"`
	Category      string   `json:"category"`
	Locations     []string `json:"locations"`
}

type UserProfile struct {
	CapitalAvailable int      `json:"capitalAvailable"`
	LocationPrefs    []string `json:"locationPreferences"`
	Interests        []string `json:"interests"`
	ExperienceYears  int      `json:"industryExperience"`
}

type Output struct {
	MatchScore   int          `json:"matchScore"`
	MatchFactors MatchFactors `json:"matchFactors"`
}

type MatchFactors struct {
	FinancialFit  int `json:"financialFit"`
	ExperienceFit int `json:"experienceFit"`
	LocationFit   int `json:"locationFit"`
	InterestFit   int `json:"interestFit"`
}
