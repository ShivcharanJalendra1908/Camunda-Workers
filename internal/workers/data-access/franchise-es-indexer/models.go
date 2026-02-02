package franchiseesindexer

// Input represents the job input
type Input struct {
	FranchiseID string `json:"franchise_id"`
	Operation   string `json:"operation"` // INDEX, UPDATE, DELETE
}

// Output represents the job output
type Output struct {
	FranchiseID string `json:"franchise_id"`
	Indexed     bool   `json:"indexed"`
	IndexName   string `json:"index_name"`
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
}
