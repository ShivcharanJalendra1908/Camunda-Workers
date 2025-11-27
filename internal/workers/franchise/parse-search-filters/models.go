// internal/workers/franchise/parse-search-filters/models.go
package parsesearchfilters

type Input struct {
	RawFilters map[string]interface{} `json:"rawFilters"`
}

type Output struct {
	ParsedFilters ParsedFilters `json:"parsedFilters"`
}

type ParsedFilters struct {
	Categories      []string        `json:"categories"`
	InvestmentRange InvestmentRange `json:"investmentRange"`
	Locations       []string        `json:"locations"`
	Keywords        string          `json:"keywords"`
	SortBy          string          `json:"sortBy"`
	Pagination      Pagination      `json:"pagination"`
}

type InvestmentRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type Pagination struct {
	Page int `json:"page"`
	Size int `json:"size"`
}
