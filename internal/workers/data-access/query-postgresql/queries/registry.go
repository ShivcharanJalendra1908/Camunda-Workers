// internal/workers/data-access/query-postgresql/queries/registry.go
package queries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"camunda-workers/internal/crypto"
	"camunda-workers/internal/models"
)

var (
	ErrMissingParam     = errors.New("missing required parameter")
	ErrUnknownQueryType = errors.New("unknown query type")
)

// QueryFunc returns: data, rowCount, executionTime (ms), error
type QueryFunc func(ctx context.Context, db *sql.DB, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error)

var Registry = map[models.QueryType]QueryFunc{
	models.QueryTypeFranchiseFullDetails:  FranchiseFullDetails,
	models.QueryTypeFranchiseOutlets:      FranchiseOutlets,
	models.QueryTypeFranchiseVerification: FranchiseVerification,
	models.QueryTypeFranchiseDetails:      FranchiseDetails,
	models.QueryTypeUserProfile:           UserProfile,
	// ===== NEW HOME =====
	models.QueryTypeIndustriesTop9:  IndustriesTop9,
	models.QueryTypeCategoriesTop30: CategoriesTop30,

	// ===== NEW LISTING =====
	models.QueryTypeCategoriesFeatured8: CategoriesFeatured8,
	models.QueryTypeIndustryBySlug:      IndustryBySlug,

	// ===== NEW DETAIL =====
	models.QueryTypeFranchiseOverview:   FranchiseOverview,
	models.QueryTypeFranchiseBusiness:   FranchiseBusiness,
	models.QueryTypeFranchiseInvestment: FranchiseInvestment,
	models.QueryTypeFranchiseOperations: FranchiseOperations,
	models.QueryTypeFranchiseSocial:     FranchiseSocial,

	models.QueryTypeIndustryBySlugWithQuestions:  IndustryBySlugWithQuestions,
	models.QueryTypeCategoryQuestionsByIndustry:  CategoryQuestionsByIndustry,
	models.QueryTypeFeaturedCategoriesByIndustry: FeaturedCategoriesByIndustry,

	// // ===== ALL INDUSTRIES =====
	// models.QueryTypeAllIndustries:           AllIndustries,
	// models.QueryTypeCategoriesByIndustry:    CategoriesByIndustry,
	// models.QueryTypeSubCategoriesByCategory: SubCategoriesByCategory,

	// ===== ENQUERY =====
	models.QueryTypeFranchiseContactInfo: FranchiseContactInfo,

	// =====USER ACTIONS =====
	models.QueryTypeGetUserBookmarks:    UserBookmarks,
	models.QueryTypeCheckBookmark:       UserBookmarkCheck,
	models.QueryTypeGetUserRating:       UserRatingForFranchise,
	models.QueryTypeGetFranchiseRatings: FranchiseRatings,
	models.QueryTypeGetUserShares:       UserShareHistory,
}

func Execute(ctx context.Context, db *sql.DB, queryType models.QueryType, params map[string]interface{}, encryptor *crypto.Encryptor) (interface{}, int, int64, error) {
	fn, exists := Registry[queryType]
	if !exists {
		return nil, 0, 0, fmt.Errorf("%w: %s", ErrUnknownQueryType, queryType)
	}
	return fn(ctx, db, params, encryptor)
}
