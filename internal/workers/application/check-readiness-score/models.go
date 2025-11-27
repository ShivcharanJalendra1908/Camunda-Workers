// internal/workers/application/check-readiness-score/models.go
package checkreadinessscore

type Input struct {
	UserID          string                 `json:"userId"`
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
