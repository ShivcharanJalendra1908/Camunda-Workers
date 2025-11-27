// internal/workers/infrastructure/select-template/models.go
package selecttemplate

// Input matches REQ-INFRA-011
type Input struct {
	BibId            string  `json:"bibId,omitempty"`
	SubscriptionTier string  `json:"subscriptionTier"`
	RoutePath        string  `json:"routePath,omitempty"`
	TemplateType     string  `json:"templateType,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
}

// Output matches REQ-INFRA-011
type Output struct {
	SelectedTemplateId string `json:"selectedTemplateId"`
}
