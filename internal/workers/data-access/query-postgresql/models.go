// internal/workers/data-access/query-postgresql/models.go
package querypostgresql

import "camunda-workers/internal/models"

type Input struct {
	QueryType    string                 `json:"queryType"`
	FranchiseID  string                 `json:"franchiseId,omitempty"`
	FranchiseIDs []string               `json:"franchiseIds,omitempty"`
	UserID       string                 `json:"userId,omitempty"`
	Filters      map[string]interface{} `json:"filters,omitempty"`
}

type Output struct {
	Data               interface{} `json:"data"`
	RowCount           int         `json:"rowCount"`
	QueryExecutionTime int64       `json:"queryExecutionTime"` // milliseconds
}

// Query types from REQ-DATA-002
type QueryType = models.QueryType

// Export constants for external use
var (
	QueryTypeFranchiseFullDetails  = models.QueryTypeFranchiseFullDetails
	QueryTypeFranchiseOutlets      = models.QueryTypeFranchiseOutlets
	QueryTypeFranchiseVerification = models.QueryTypeFranchiseVerification
	QueryTypeFranchiseDetails      = models.QueryTypeFranchiseDetails
	QueryTypeUserProfile           = models.QueryTypeUserProfile
)
