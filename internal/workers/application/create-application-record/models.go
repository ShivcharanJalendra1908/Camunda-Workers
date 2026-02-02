// internal/workers/application/create-application-record/models.go
package createapplicationrecord

type Input struct {
	SeekerID        string                 `json:"seekerId"`
	FranchiseID     string                 `json:"franchiseId"`
	ApplicationData map[string]interface{} `json:"applicationData"`
	ReadinessScore  float64                `json:"readinessScore"` // Changed from int to float64
	Priority        string                 `json:"priority"`
}

type Output struct {
	ApplicationID     string `json:"applicationId"`
	ApplicationStatus string `json:"applicationStatus"`
	IsDuplicate       bool   `json:"isDuplicate,omitempty"`
	Message           string `json:"message,omitempty"`
	CreatedAt         string `json:"createdAt"` // ISO 8601
}
