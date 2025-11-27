// internal/workers/application/create-application-record/models.go
package createapplicationrecord

type Input struct {
	SeekerID        string                 `json:"seekerId"`
	FranchiseID     string                 `json:"franchiseId"`
	ApplicationData map[string]interface{} `json:"applicationData"`
	ReadinessScore  int                    `json:"readinessScore"`
	Priority        string                 `json:"priority"`
}

type Output struct {
	ApplicationID     string `json:"applicationId"`
	ApplicationStatus string `json:"applicationStatus"`
	CreatedAt         string `json:"createdAt"` // ISO 8601
}
