// internal/workers/application/check-readiness-score/models.go
package checkreadinessscore

type Input struct {
	UserID          string                 `json:"userId"`
	FranchiseID     string                 `json:"franchiseId"`
	ApplicationData map[string]interface{} `json:"applicationData"`
}

type Output struct {
	ReadinessScore     int            `json:"readinessScore"`
	QualificationLevel string         `json:"qualificationLevel"`
	ScoreBreakdown     ScoreBreakdown `json:"scoreBreakdown"`
}

type ScoreBreakdown struct {
	Financial     int `json:"financial"`
	Experience    int `json:"experience"`
	Commitment    int `json:"commitment"`
	Compatibility int `json:"compatibility"`
}

type FinancialInfo struct {
	LiquidCapital float64 `json:"liquidCapital"`
	NetWorth      float64 `json:"netWorth"`
	CreditScore   float64 `json:"creditScore"`
}

type ExperienceInfo struct {
	YearsInIndustry      int  `json:"yearsInIndustry"`
	ManagementExperience bool `json:"managementExperience"`
	BusinessOwnership    bool `json:"businessOwnership"`
}
