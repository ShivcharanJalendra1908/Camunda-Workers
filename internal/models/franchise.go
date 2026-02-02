// internal/models/franchise.go
package models

import (
	"regexp"

	"camunda-workers/internal/common/validation"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
)

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

// Validate validates the Franchise struct
func (f Franchise) Validate() error {
	return ozzo.ValidateStruct(&f,
		// Validate ID (UUID)
		ozzo.Field(&f.ID,
			ozzo.When(f.ID != "", validation.IsUUID),
		),

		// Validate Name
		ozzo.Field(&f.Name,
			ozzo.Required.Error("name is required"),
			ozzo.Length(2, 100).Error("name must be 2-100 characters"),
			validation.SafeSQLString,
		),

		// Validate Description
		ozzo.Field(&f.Description,
			ozzo.When(f.Description != "",
				ozzo.Length(10, 5000).Error("description must be 10-5000 characters"),
			),
			validation.SafeSQLString,
		),

		// Validate Investment Range
		ozzo.Field(&f.InvestmentMin,
			ozzo.Min(0).Error("investmentMin must be 0-100000000"),
			ozzo.Max(100000000).Error("investmentMin must be 0-100000000"),
		),

		ozzo.Field(&f.InvestmentMax,
			ozzo.Min(0).Error("investmentMax must be 0-100000000"),
			ozzo.Max(100000000).Error("investmentMax must be 0-100000000"),
			ozzo.By(func(value interface{}) error {
				if f.InvestmentMax < f.InvestmentMin {
					return ozzo.NewError(
						"validation_failed",
						"investmentMax must be >= investmentMin",
					)
				}
				return nil
			}),
		),

		// Validate Category
		ozzo.Field(&f.Category,
			ozzo.When(f.Category != "",
				ozzo.Length(2, 50),
				validation.SafeSQLString,
			),
		),

		// Validate Locations array
		ozzo.Field(&f.Locations,
			ozzo.Each(
				ozzo.Length(2, 100).Error("each location must be 2-100 characters"),
				ozzo.Match(regexp.MustCompile(`^[a-zA-Z\s\-,\.']+$`)).Error("location contains invalid characters"),
			),
			validation.ValidateArraySize(0, 100),
		),

		// Validate Counts
		ozzo.Field(&f.ApplicationCount,
			ozzo.Min(0).Error("applicationCount cannot be negative"),
		),

		ozzo.Field(&f.ViewCount,
			ozzo.Min(0).Error("viewCount cannot be negative"),
		),
	)
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

// Validate validates the FranchiseOutlet struct
func (fo FranchiseOutlet) Validate() error {
	return ozzo.ValidateStruct(&fo,
		// Validate ID (UUID)
		ozzo.Field(&fo.ID,
			ozzo.When(fo.ID != "", validation.IsUUID),
		),

		// Validate FranchiseID (UUID)
		ozzo.Field(&fo.FranchiseID,
			ozzo.Required.Error("franchiseId is required"),
			validation.IsUUID,
		),

		// Validate Address
		ozzo.Field(&fo.Address,
			ozzo.Required.Error("address is required"),
			ozzo.Length(10, 500).Error("address must be 10-500 characters"),
			validation.SafeSQLString,
		),

		// Validate City
		ozzo.Field(&fo.City,
			ozzo.Required.Error("city is required"),
			ozzo.Length(2, 100).Error("city must be 2-100 characters"),
			ozzo.Match(regexp.MustCompile(`^[a-zA-Z\s\-\.']+$`)).Error("city contains invalid characters"),
		),

		// Validate State
		ozzo.Field(&fo.State,
			ozzo.Required.Error("state is required"),
			ozzo.Length(2, 100).Error("state must be 2-100 characters"),
			ozzo.Match(regexp.MustCompile(`^[a-zA-Z\s\-\.']+$`)).Error("state contains invalid characters"),
		),

		// Validate Country
		ozzo.Field(&fo.Country,
			ozzo.Required.Error("country is required"),
			ozzo.Length(2, 100).Error("country must be 2-100 characters"),
			ozzo.Match(regexp.MustCompile(`^[a-zA-Z\s\-\.']+$`)).Error("country contains invalid characters"),
		),

		// Validate Phone
		ozzo.Field(&fo.Phone,
			ozzo.When(fo.Phone != "", validation.ValidatePhone()),
		),
	)
}

type FranchiseVerification struct {
	FranchiseID        string `json:"franchiseId"`
	VerificationStatus string `json:"verificationStatus"`
	VerifiedAt         string `json:"verifiedAt"`
	ComplianceScore    int    `json:"complianceScore"`
}

// Validate validates the FranchiseVerification struct
func (fv FranchiseVerification) Validate() error {
	return ozzo.ValidateStruct(&fv,
		// Validate FranchiseID (UUID)
		ozzo.Field(&fv.FranchiseID,
			ozzo.Required.Error("franchiseId is required"),
			validation.IsUUID,
		),

		// Validate VerificationStatus (enum)
		ozzo.Field(&fv.VerificationStatus,
			ozzo.Required.Error("verificationStatus is required"),
			ozzo.In("pending", "verified", "rejected", "suspended").
				Error("verificationStatus must be: pending, verified, rejected, or suspended"),
		),

		// Validate ComplianceScore (0-100)
		ozzo.Field(&fv.ComplianceScore,
			ozzo.Min(0).Error("complianceScore cannot be negative"),
			ozzo.Max(100).Error("complianceScore cannot exceed 100"),
		),
	)
}

// // internal/models/franchise.go
// package models

// type Franchise struct {
// 	ID               string   `json:"id"`
// 	Name             string   `json:"name"`
// 	Description      string   `json:"description"`
// 	InvestmentMin    int      `json:"investmentMin"`
// 	InvestmentMax    int      `json:"investmentMax"`
// 	Category         string   `json:"category"`
// 	Locations        []string `json:"locations"`
// 	IsVerified       bool     `json:"isVerified"`
// 	CreatedAt        string   `json:"createdAt"`
// 	UpdatedAt        string   `json:"updatedAt"`
// 	ApplicationCount int      `json:"applicationCount"`
// 	ViewCount        int      `json:"viewCount"`
// }

// type FranchiseOutlet struct {
// 	ID          string `json:"id"`
// 	FranchiseID string `json:"franchiseId"`
// 	Address     string `json:"address"`
// 	City        string `json:"city"`
// 	State       string `json:"state"`
// 	Country     string `json:"country"`
// 	Phone       string `json:"phone"`
// }

// type FranchiseVerification struct {
// 	FranchiseID        string `json:"franchiseId"`
// 	VerificationStatus string `json:"verificationStatus"`
// 	VerifiedAt         string `json:"verifiedAt"`
// 	ComplianceScore    int    `json:"complianceScore"`
// }
