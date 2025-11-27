// internal/workers/data-access/query-postgresql/queries/registry.go
package queries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"camunda-workers/internal/models"
)

var (
	ErrMissingParam     = errors.New("missing required parameter")
	ErrUnknownQueryType = errors.New("unknown query type")
)

// QueryFunc returns: data, rowCount, executionTime (ms), error
type QueryFunc func(ctx context.Context, db *sql.DB, params map[string]interface{}) (interface{}, int, int64, error)

var Registry = map[models.QueryType]QueryFunc{
	models.QueryTypeFranchiseFullDetails:  FranchiseFullDetails,
	models.QueryTypeFranchiseOutlets:      FranchiseOutlets,
	models.QueryTypeFranchiseVerification: FranchiseVerification,
	models.QueryTypeFranchiseDetails:      FranchiseDetails,
	models.QueryTypeUserProfile:           UserProfile,
}

func Execute(ctx context.Context, db *sql.DB, queryType models.QueryType, params map[string]interface{}) (interface{}, int, int64, error) {
	fn, exists := Registry[queryType]
	if !exists {
		return nil, 0, 0, fmt.Errorf("%w: %s", ErrUnknownQueryType, queryType)
	}
	return fn(ctx, db, params)
}
