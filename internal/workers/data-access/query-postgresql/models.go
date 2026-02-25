// internal/workers/data-access/query-postgresql/models.go
package querypostgresql

import (
	"strings"

	"camunda-workers/internal/common/validation"
	"camunda-workers/internal/models"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
)

type Input struct {
	QueryType    string                 `json:"queryType"`
	FranchiseID  string                 `json:"franchiseId,omitempty"`
	FranchiseIDs []string               `json:"franchiseIds,omitempty"`
	UserID       string                 `json:"userId,omitempty"`
	Slug         string                 `json:"slug,omitempty"`
	IndustrySlug string                 `json:"industrySlug,omitempty"`
	IndustryID   string                 `json:"industryId,omitempty"`
	Filters      map[string]interface{} `json:"filters,omitempty"`
	Params       map[string]interface{} `json:"params,omitempty"` // Add this line
}

type Output struct {
	QueryType          string                 `json:"queryType"`
	FranchiseID        string                 `json:"franchiseId,omitempty"`
	FranchiseIDs       []string               `json:"franchiseIds,omitempty"`
	UserID             string                 `json:"userId,omitempty"`
	Slug               string                 `json:"slug,omitempty"`
	IndustrySlug       string                 `json:"industrySlug,omitempty"`
	IndustryID         string                 `json:"industryId,omitempty"`
	Filters            map[string]interface{} `json:"filters,omitempty"`
	Params             map[string]interface{} `json:"params,omitempty"` // Add this line for consistency
	Data               interface{}            `json:"data"`
	RowCount           int                    `json:"rowCount"`
	QueryExecutionTime int64                  `json:"queryExecutionTime"`
}

func (i *Input) Sanitize() {
	i.QueryType = strings.TrimSpace(i.QueryType)
	i.FranchiseID = validation.SanitizeString(i.FranchiseID)
	i.UserID = validation.SanitizeString(i.UserID)
	i.Slug = strings.TrimSpace(i.Slug)
	i.IndustrySlug = strings.TrimSpace(i.IndustrySlug)
	i.IndustryID = validation.SanitizeString(i.IndustryID)

	for idx := range i.FranchiseIDs {
		i.FranchiseIDs[idx] = strings.TrimSpace(i.FranchiseIDs[idx])
	}

	// Sanitize Params if it exists
	if i.Params != nil {
		// You might want to add specific sanitization for Params fields
		// For now, we'll just ensure it's properly initialized
	}
}

func (i *Input) Validate() error {
	validQueryTypes := []interface{}{
		string(models.QueryTypeFranchiseFullDetails),
		string(models.QueryTypeFranchiseDetails),
		string(models.QueryTypeFranchiseOutlets),
		string(models.QueryTypeFranchiseVerification),
		string(models.QueryTypeUserProfile),
		string(models.QueryTypeIndustriesTop9),
		string(models.QueryTypeCategoriesTop30),
		string(models.QueryTypeCategoriesFeatured8),
		string(models.QueryTypeIndustryBySlug),
		string(models.QueryTypeFranchiseOverview),
		string(models.QueryTypeFranchiseBusiness),
		string(models.QueryTypeFranchiseInvestment),
		string(models.QueryTypeFranchiseOperations),
		string(models.QueryTypeFranchiseSocial),
		string(models.QueryTypeIndustryBySlugWithQuestions),
		string(models.QueryTypeCategoryQuestionsByIndustry),
		string(models.QueryTypeFeaturedCategoriesByIndustry),
	}

	return ozzo.ValidateStruct(i,
		// QueryType validation
		ozzo.Field(&i.QueryType,
			ozzo.Required.Error("queryType is required"),
			validation.ValidateStringLength(3, 100),
			validation.SafeSQLString,
			ozzo.In(validQueryTypes...).Error("invalid query type"),
		),

		// UUID validations
		ozzo.Field(&i.FranchiseID,
			ozzo.When(i.FranchiseID != "", validation.IsUUID),
		),
		ozzo.Field(&i.FranchiseIDs,
			ozzo.Each(validation.IsUUID),
		),
		ozzo.Field(&i.UserID,
			ozzo.When(i.UserID != "", validation.IsUUID),
		),
		ozzo.Field(&i.IndustryID,
			ozzo.When(i.IndustryID != "", validation.IsUUID),
		),

		// Slug validations
		ozzo.Field(&i.Slug,
			ozzo.When(i.Slug != "",
				validation.ValidateStringLength(1, 100),
				validation.SafeSQLString,
			),
		),
		ozzo.Field(&i.IndustrySlug,
			ozzo.When(i.IndustrySlug != "",
				validation.ValidateStringLength(1, 100),
				validation.SafeSQLString,
			),
		),

		// Filters validation
		ozzo.Field(&i.Filters,
			ozzo.Length(0, 20).Error("filters cannot exceed 20 items"),
		),

		// Params validation (add this)
		ozzo.Field(&i.Params,
			ozzo.Length(0, 20).Error("params cannot exceed 20 items"),
		),
	)
}

type QueryType = models.QueryType

var (
	QueryTypeFranchiseFullDetails  = models.QueryTypeFranchiseFullDetails
	QueryTypeFranchiseOutlets      = models.QueryTypeFranchiseOutlets
	QueryTypeFranchiseVerification = models.QueryTypeFranchiseVerification
	QueryTypeFranchiseDetails      = models.QueryTypeFranchiseDetails
	QueryTypeUserProfile           = models.QueryTypeUserProfile
	QueryTypeIndustriesTop9        = models.QueryTypeIndustriesTop9
	QueryTypeCategoriesTop30       = models.QueryTypeCategoriesTop30
	QueryTypeCategoriesFeatured8   = models.QueryTypeCategoriesFeatured8
	QueryTypeIndustryBySlug        = models.QueryTypeIndustryBySlug
	QueryTypeFranchiseOverview     = models.QueryTypeFranchiseOverview
	QueryTypeFranchiseBusiness     = models.QueryTypeFranchiseBusiness
	QueryTypeFranchiseInvestment   = models.QueryTypeFranchiseInvestment
	QueryTypeFranchiseOperations   = models.QueryTypeFranchiseOperations
	QueryTypeFranchiseSocial       = models.QueryTypeFranchiseSocial
)

