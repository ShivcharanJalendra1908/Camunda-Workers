// internal/workers/infrastructure/validate-subscription/models.go
package validatesubscription

type Input struct {
	UserID           string `json:"userId"`
	SubscriptionTier string `json:"subscriptionTier"`
}

// Output represents the output data after subscription validation
type Output struct {
	IsValid     bool     `json:"isValid"`
	TierLevel   string   `json:"tierLevel"`
	Permissions []string `json:"permissions,omitempty"`
}

// Subscription represents a user subscription record
type Subscription struct {
	UserID    string `json:"userId"`
	Tier      string `json:"tier"`
	ExpiresAt string `json:"expiresAt"`
	IsValid   bool   `json:"isValid"`
}
