// internal/workers/infrastructure/validate-subscription/models.go
package validatesubscription

import "database/sql"

type Input struct {
	UserID           string `json:"userId"`
	SubscriptionTier string `json:"subscriptionTier"`
}

type Output struct {
	IsValid     bool     `json:"isValid"`
	TierLevel   string   `json:"tierLevel"`
	Permissions []string `json:"permissions,omitempty"`
}

type Subscription struct {
	UserID    string         `json:"userId"`
	Tier      string         `json:"tier"`
	ExpiresAt sql.NullString `json:"expiresAt"`
	IsValid   bool           `json:"isValid"`
}
