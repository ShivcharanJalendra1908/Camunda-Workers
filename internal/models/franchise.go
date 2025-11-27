// internal/models/franchise.go
package models

type Franchise struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	InvestmentMin    int      `json:"investmentMin"`
	InvestmentMax    int      `json:"investmentMax"`
	Category         string   `json:"category"`
	Locations        []string `json:"locations"`
	IsVerified       bool     `json:"isVerified"`
	CreatedAt        string   `json:"createdAt"`
	UpdatedAt        string   `json:"updatedAt"`
	ApplicationCount int      `json:"applicationCount"`
	ViewCount        int      `json:"viewCount"`
}

type FranchiseOutlet struct {
	ID          string `json:"id"`
	FranchiseID string `json:"franchiseId"`
	Address     string `json:"address"`
	City        string `json:"city"`
	State       string `json:"state"`
	Country     string `json:"country"`
	Phone       string `json:"phone"`
}

type FranchiseVerification struct {
	FranchiseID        string `json:"franchiseId"`
	VerificationStatus string `json:"verificationStatus"`
	VerifiedAt         string `json:"verifiedAt"`
	ComplianceScore    int    `json:"complianceScore"`
}
