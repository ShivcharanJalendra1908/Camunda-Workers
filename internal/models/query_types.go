// internal/models/query_types.go
package models

type QueryType string

const (
	QueryTypeFranchiseFullDetails  QueryType = "franchise_full_details"
	QueryTypeFranchiseOutlets      QueryType = "franchise_outlets"
	QueryTypeFranchiseVerification QueryType = "franchise_verification"
	QueryTypeFranchiseDetails      QueryType = "franchise_details"
	QueryTypeUserProfile           QueryType = "user_profile"
)
