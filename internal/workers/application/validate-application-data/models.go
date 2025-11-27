// internal/workers/application/validate-application-data/models.go
package validateapplicationdata

import "regexp"

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

// Predefined patterns
var (
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	// E.164 format: optional +, must start with 1-9, then 6-14 more digits (total 7-15 digits)
	// This prevents short numbers like "123" from passing
	phoneRegex = regexp.MustCompile(`^[\+]?[1-9][\d]{6,14}$`)
	nameRegex  = regexp.MustCompile(`^[a-zA-Z\s\-\']{2,100}$`)
)

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
