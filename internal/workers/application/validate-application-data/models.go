// internal/workers/application/validate-application-data/models.go
package validateapplicationdata

type Input struct {
	ApplicationData map[string]interface{} `json:"applicationData"`
	FranchiseID     string                 `json:"franchiseId"`
}

type Output struct {
	IsValid          bool                   `json:"isValid"`
	ValidatedData    map[string]interface{} `json:"validatedData"`
	ValidationErrors []ValidationError      `json:"validationErrors"`
}

type ValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Franchise-specific rules (in real system, fetch from DB)
var franchiseRules = map[string]FranchiseRule{
	"mcdonalds": {
		MinLiquidCapital:    500000,
		MinNetWorth:         1000000,
		RequiresCreditScore: true,
	},
	"starbucks": {
		MinLiquidCapital:    300000,
		MinNetWorth:         600000,
		RequiresCreditScore: false,
	},
}

type FranchiseRule struct {
	MinLiquidCapital    int
	MinNetWorth         int
	RequiresCreditScore bool
}
