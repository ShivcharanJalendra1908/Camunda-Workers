// internal/models/franchise.go
package models

import (
	"regexp"
	"time"

	"camunda-workers/internal/common/validation"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
)

type Franchise struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	Description      string       `json:"description"`
	InvestmentMin    int          `json:"investmentMin"`
	InvestmentMax    int          `json:"investmentMax"`
	Category         string       `json:"category"`
	Locations        []string     `json:"locations"`
	IsVerified       bool         `json:"isVerified"`
	CreatedAt        string       `json:"createdAt"`
	UpdatedAt        string       `json:"updatedAt"`
	ApplicationCount int          `json:"applicationCount"`
	ViewCount        int          `json:"viewCount"`
	ROI              FranchiseROI `json:"roi" db:"roi"`
}

// UserRating represents a user's rating for a franchise
type UserRating struct {
	ID          string    `json:"id" db:"id"`
	UserID      string    `json:"userId" db:"user_id"`
	FranchiseID string    `json:"franchiseId" db:"franchise_id"`
	Rating      float64   `json:"rating" db:"rating"` // 1.0 to 5.0
	Review      string    `json:"review,omitempty" db:"review"`
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
}

// FranchiseShare represents a share event
type FranchiseShare struct {
	ID            string    `json:"id" db:"id"`
	UserID        string    `json:"userId,omitempty" db:"user_id"`
	FranchiseID   string    `json:"franchiseId" db:"franchise_id"`
	SharePlatform string    `json:"sharePlatform" db:"share_platform"`
	IPAddress     string    `json:"ipAddress,omitempty" db:"ip_address"`
	SharedAt      time.Time `json:"sharedAt" db:"shared_at"`
}

// UserBookmark — alias for user_favorites, used in user-actions context
type UserBookmark struct {
	ID          string    `json:"id" db:"id"`
	UserID      string    `json:"userId" db:"user_id"`
	FranchiseID string    `json:"franchiseId" db:"franchise_id"`
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
}

type FranchiseROI struct {
	Min          float64 `json:"min" db:"roi_min"`
	Max          float64 `json:"max" db:"roi_max"`
	PeriodMonths int     `json:"period_months" db:"roi_period_months"`
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
