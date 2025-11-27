package models

type UserSubscription struct {
	UserID          string `json:"userId"`
	SubscriptionTier string `json:"subscriptionTier"`
	IsValid         bool   `json:"isValid"`
	TierLevel       string `json:"tierLevel"`
}
