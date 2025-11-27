// internal/models/application.go
package models

type Application struct {
	ID              string                 `json:"id"`
	SeekerID        string                 `json:"seekerId"`
	FranchiseID     string                 `json:"franchiseId"`
	ApplicationData map[string]interface{} `json:"applicationData"`
	ReadinessScore  int                    `json:"readinessScore"`
	Priority        string                 `json:"priority"`
	Status          string                 `json:"status"`
	CreatedAt       string                 `json:"createdAt"`
	UpdatedAt       string                 `json:"updatedAt"`
}

type ApplicationData struct {
	PersonalInfo  PersonalInfo  `json:"personalInfo"`
	FinancialInfo FinancialInfo `json:"financialInfo"`
	Experience    Experience    `json:"experience"`
	BusinessPlan  *BusinessPlan `json:"businessPlan,omitempty"`
}

type PersonalInfo struct {
	Name                string   `json:"name"`
	Email               string   `json:"email"`
	Phone               string   `json:"phone"`
	Address             string   `json:"address,omitempty"`
	LocationPreferences []string `json:"locationPreferences,omitempty"`
}

type FinancialInfo struct {
	LiquidCapital     int     `json:"liquidCapital"`
	NetWorth          int     `json:"netWorth"`
	CreditScore       int     `json:"creditScore,omitempty"`
	DebtToIncomeRatio float64 `json:"debtToIncomeRatio,omitempty"`
}

type Experience struct {
	YearsInIndustry      int  `json:"yearsInIndustry"`
	ManagementExperience bool `json:"managementExperience"`
	BusinessOwnership    bool `json:"businessOwnership,omitempty"`
}

type BusinessPlan struct {
	ExecutiveSummary     string `json:"executiveSummary,omitempty"`
	MarketAnalysis       string `json:"marketAnalysis,omitempty"`
	FinancialProjections string `json:"financialProjections,omitempty"`
}
