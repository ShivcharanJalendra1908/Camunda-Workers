// internal/workers/application/check-priority-routing/models.go
package checkpriorityrouting

type Input struct {
	FranchiseID string `json:"franchiseId"`
}

type Output struct {
	IsPremiumFranchisor bool   `json:"isPremiumFranchisor"`
	RoutingPriority     string `json:"routingPriority"`
}

// Franchisor account types from REQ-BIZ-022
const (
	AccountTypePremium  = "premium"
	AccountTypeVerified = "verified"
	AccountTypeStandard = "standard"
)

// Priority levels
const (
	PriorityHigh   = "high"
	PriorityMedium = "medium"
	PriorityLow    = "low"
)
