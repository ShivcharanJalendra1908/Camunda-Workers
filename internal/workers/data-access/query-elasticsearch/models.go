// FILE: internal/workers/data-access/query-elasticsearch/models.go
package queryelasticsearch

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"camunda-workers/internal/models"

	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
)

// Input - Elasticsearch query input structure
type Input struct {
	// ✅ New format - registry based (preferred)
	QueryType models.QueryType       `json:"queryType"`
	Params    map[string]interface{} `json:"params"` // Query parameters for registry queries

	// ✅ Old format - backward compatibility
	IndexName string                 `json:"indexName"` // e.g., "franchise_listings"
	Query     map[string]interface{} `json:"query"`     // Raw ES query (direct query format)

	// ✅ Common fields (for backward compatibility and flexibility)
	Filters      map[string]interface{} `json:"filters"`      // Filter criteria
	FranchiseID  string                 `json:"franchiseId"`  // Can be UUID or slug
	Category     string                 `json:"category"`     // Category filter
	CategorySlug    string                 `json:"categorySlug"`    // ✅ ADDED: For category-specific queries
	SubCategorySlug string                 `json:"subCategorySlug"` // ✅ ADDED: For sub-category-specific queries
	IndustrySlug    string                 `json:"industrySlug"`    // ✅ ADDED: For industry-specific queries
	Pagination   Pagination             `json:"pagination"`

	Page     int `json:"page,omitempty"`
	PageSize int `json:"pageSize,omitempty"`
	Offset   int `json:"offset,omitempty"`

	// ✅ NEW: Timeout configuration per request
	TimeoutMs int64 `json:"timeoutMs,omitempty"` // Request-specific timeout
}

// Pagination - For controlling result sets
type Pagination struct {
	From int `json:"from"`
	Size int `json:"size"`
	Page int `json:"page,omitempty"` // ✅ ADDED: Alternative page-based pagination
}

// Output - Elasticsearch query response
type Output struct {
	Success   bool                     `json:"success"`
	Data      []map[string]interface{} `json:"data"`
	TotalHits int64                    `json:"total"`
	MaxScore  float64                  `json:"maxScore,omitempty"`
	Took      int64                    `json:"tookMs"`
	QueryType string                   `json:"queryType,omitempty"`
	IndexName string                   `json:"indexName,omitempty"`
	Message   string                   `json:"message,omitempty"`

	// ✅ NEW: Error details for partial failures
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`

	// ✅ NEW: Pagination metadata
	Page       int `json:"page,omitempty"`
	PageSize   int `json:"pageSize,omitempty"`
	TotalPages int `json:"totalPages,omitempty"`
}

// Validate - Comprehensive validation for Input
func (i *Input) Validate() error {
	// ✅ Phase 1: Basic structural validation
	if err := ozzo.ValidateStruct(i,
		// QueryType validation (when provided)
		ozzo.Field(&i.QueryType,
			ozzo.When(i.QueryType != "",
				ozzo.Required.Error("queryType is required when using registry queries"),
				ozzo.Length(1, 100).Error("queryType must be 1-100 characters"),
			),
		),

		// IndexName validation (when provided)
		ozzo.Field(&i.IndexName,
			ozzo.When(i.IndexName != "",
				ozzo.Required.Error("indexName is required for raw queries"),
				ozzo.Length(1, 100).Error("indexName must be 1-100 characters"),
			),
		),

		// FranchiseID validation (supports UUID or slug)
		ozzo.Field(&i.FranchiseID,
			ozzo.When(i.FranchiseID != "",
				ozzo.Length(1, 100).Error("franchiseId must be 1-100 characters"),
				ozzo.By(validateFranchiseID),
			),
		),

		// Category validation
		ozzo.Field(&i.Category,
			ozzo.Length(0, 100).Error("category must be ≤100 characters"),
		),

		// CategorySlug validation
		ozzo.Field(&i.CategorySlug,
			ozzo.Length(0, 100).Error("categorySlug must be ≤100 characters"),
		),

		// SubCategorySlug validation
		ozzo.Field(&i.SubCategorySlug,
			ozzo.Length(0, 100).Error("subCategorySlug must be ≤100 characters"),
		),

		// IndustrySlug validation
		ozzo.Field(&i.IndustrySlug,
			ozzo.Length(0, 100).Error("industrySlug must be ≤100 characters"),
		),

		// Pagination validation
		ozzo.Field(&i.Pagination),

		// Timeout validation
		ozzo.Field(&i.TimeoutMs,
			ozzo.Min(100).Error("timeout must be ≥100ms"),
			ozzo.Max(120000).Error("timeout must be ≤120000ms (2 minutes)"),
		),

		// Filters validation
		ozzo.Field(&i.Filters, ozzo.By(validateFiltersComplex)),

		// Query validation (for raw queries)
		ozzo.Field(&i.Query, ozzo.By(validateQuery)),

		// Params validation (for registry queries)
		ozzo.Field(&i.Params, ozzo.By(validateParams)),
	); err != nil {
		return err
	}

	// ✅ Phase 2: Cross-field validation
	if i.QueryType == "" && i.IndexName == "" {
		return errors.New("either queryType or indexName must be provided")
	}

	if i.QueryType != "" && i.IndexName != "" {
		return errors.New("cannot provide both queryType and indexName, choose one format")
	}

	if i.QueryType != "" && i.Query != nil {
		return errors.New("cannot use query field with queryType, use params instead")
	}

	if i.IndexName != "" && i.Params != nil {
		return errors.New("cannot use params field with indexName, use query instead")
	}

	if i.QueryType != "" && i.IndexName == "" && i.Query == nil && i.Params == nil {
		return errors.New("queryType requires params field")
	}

	if i.IndexName != "" && i.Query == nil {
		return errors.New("indexName requires query field")
	}

	// ✅ Phase 3: Validate based on query type
	if i.QueryType != "" {
		if err := validateByQueryType(i); err != nil {
			return err
		}
	}

	return nil
}

// validateFranchiseID - Supports both UUID and slug formats
func validateFranchiseID(value interface{}) error {
	str, ok := value.(string)
	if !ok {
		return errors.New("must be a string")
	}

	// Try to parse as UUID first
	if _, err := uuid.Parse(str); err == nil {
		return nil // Valid UUID
	}

	// If not UUID, validate as slug (alphanumeric, hyphens, underscores)
	if len(str) < 1 || len(str) > 100 {
		return errors.New("slug must be 1-100 characters")
	}

	// Simple slug validation: alphanumeric, hyphens, underscores
	for _, ch := range str {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			return errors.New("slug can only contain letters, numbers, hyphens and underscores")
		}
	}

	return nil
}

// validateFiltersComplex - Comprehensive filter validation
func validateFiltersComplex(value interface{}) error {
	if value == nil {
		return nil
	}

	filters, ok := value.(map[string]interface{})
	if !ok {
		return errors.New("filters must be a map")
	}

	// Check size limit
	if len(filters) > 50 {
		return errors.New("too many filter criteria (max 50)")
	}

	// Validate each filter recursively
	return validateFilterRecursive(filters, "", 0)
}

func validateFilterRecursive(obj interface{}, path string, depth int) error {
	// Prevent deep nesting attacks
	if depth > 5 {
		return errors.New("filter nesting too deep (max 5 levels)")
	}

	switch v := obj.(type) {
	case map[string]interface{}:
		// Check for dangerous keys
		for key := range v {
			lowerKey := strings.ToLower(key)

			// Block dangerous patterns
			dangerousPatterns := []string{"script", "inline", "source", "function", "eval", "exec", "$where"}
			for _, pattern := range dangerousPatterns {
				if strings.Contains(lowerKey, pattern) {
					return errors.New("dangerous filter pattern detected: " + pattern)
				}
			}

			// Validate key format (safe field name)
			if len(key) < 1 || len(key) > 50 {
				return errors.New("field name must be 1-50 characters")
			}

			// Recursively validate value
			nextPath := key
			if path != "" {
				nextPath = path + "." + key
			}
			if err := validateFilterRecursive(v[key], nextPath, depth+1); err != nil {
				return err
			}
		}

	case []interface{}:
		// Check array size
		if len(v) > 100 {
			return errors.New("array too large (max 100 items)")
		}

		// Validate each item
		for i, item := range v {
			itemPath := fmt.Sprintf("%s[%d]", path, i)
			if err := validateFilterRecursive(item, itemPath, depth+1); err != nil {
				return err
			}
		}

	case string:
		// Check string length
		if len(v) > 500 {
			return errors.New("string value too long (max 500 chars)")
		}

	case float64:
		// Validate numeric range
		if v < -1e15 || v > 1e15 {
			return errors.New("number out of valid range")
		}

	case bool, int, int64, nil:
		// These types are safe
		return nil

	default:
		return errors.New("unsupported filter value type")
	}

	return nil
}

// validateQuery - Validate raw Elasticsearch query
func validateQuery(value interface{}) error {
	if value == nil {
		return nil
	}

	query, ok := value.(map[string]interface{})
	if !ok {
		return errors.New("query must be a map")
	}

	// Convert to JSON for pattern checking
	queryJSON, err := json.Marshal(query)
	if err != nil {
		return errors.New("invalid query format")
	}

	queryStr := strings.ToLower(string(queryJSON))

	// Check for dangerous patterns
	dangerousPatterns := []string{
		"\"script\":",
		"\"inline\":",
		"\"source\":",
		"\"function\":",
		"\"eval(\"",
		"\"exec(\"",
		"\"$where\":",
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(queryStr, pattern) {
			return errors.New("dangerous query pattern detected: " + pattern)
		}
	}

	// Check query depth/complexity
	if err := validateQueryDepth(query, 0); err != nil {
		return err
	}

	return nil
}

func validateQueryDepth(obj map[string]interface{}, depth int) error {
	if depth > 10 {
		return errors.New("query nesting too deep (max 10 levels)")
	}

	for _, value := range obj {
		switch v := value.(type) {
		case map[string]interface{}:
			if err := validateQueryDepth(v, depth+1); err != nil {
				return err
			}
		case []interface{}:
			for _, item := range v {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if err := validateQueryDepth(itemMap, depth+1); err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

// validateParams - Validate registry query parameters
func validateParams(value interface{}) error {
	if value == nil {
		return nil
	}

	params, ok := value.(map[string]interface{})
	if !ok {
		return errors.New("params must be a map")
	}

	// Size limit
	if len(params) > 100 {
		return errors.New("too many parameters (max 100)")
	}

	// Validate each parameter
	for key, value := range params {
		// Validate key
		if len(key) < 1 || len(key) > 50 {
			return errors.New("parameter name must be 1-50 characters")
		}

		// Validate value based on type
		switch v := value.(type) {
		case string:
			if len(v) > 500 {
				return errors.New("parameter value too long: " + key)
			}
		case float64:
			if v < -1e15 || v > 1e15 {
				return errors.New("parameter value out of range: " + key)
			}
		case bool, int, int64, nil:
			// Valid types
		case []interface{}:
			if len(v) > 100 {
				return errors.New("array too large: " + key)
			}
		case map[string]interface{}:
			return errors.New("nested objects not allowed in params: " + key)
		default:
			return errors.New("unsupported parameter type: " + key)
		}
	}

	return nil
}

// validateByQueryType - Query type specific validation
func validateByQueryType(input *Input) error {
	switch input.QueryType {
	case models.ESQueryTypeFranchiseBySlug:
		// Must have slug in franchiseId, params, or filters
		hasSlug := input.FranchiseID != "" ||
			(input.Params != nil && input.Params["slug"] != nil) ||
			(input.Filters != nil && input.Filters["slug"] != nil)

		if !hasSlug {
			return errors.New("FRANCHISE_BY_SLUG query requires slug parameter")
		}

	case models.ESQueryTypeRecommendedByIndustry:
		// // Must have industrySlug or franchiseId (to derive industry)
		// hasIndustry := input.IndustrySlug != "" ||
		// 	input.FranchiseID != "" ||
		// 	(input.Params != nil && input.Params["industrySlug"] != nil)

		// if !hasIndustry {
		// 	return errors.New("RECOMMENDED_BY_INDUSTRY query requires industrySlug or franchiseId")
		// }

	case models.ESQueryTypeMarketInsights:
		// // Must have industrySlug
		// hasIndustry := input.IndustrySlug != "" ||
		// 	(input.Params != nil && input.Params["industrySlug"] != nil)

		// if !hasIndustry {
		// 	return errors.New("MARKET_INSIGHTS query requires industrySlug")
		// }

	case models.ESQueryTypeSearchWithFilters:
		fallthrough
	case models.ESQueryTypeSearchWithAggregations:
		// Must have filters
		if len(input.Filters) == 0 && len(input.Params) == 0 {
			return errors.New("search queries require filters or params")
		}
	}

	return nil
}

// Sanitize - Clean input data
func (i *Input) Sanitize() {
	i.IndexName = strings.TrimSpace(i.IndexName)
	i.QueryType = models.QueryType(strings.TrimSpace(string(i.QueryType)))
	i.FranchiseID = strings.TrimSpace(i.FranchiseID)
	i.Category = strings.TrimSpace(i.Category)
	i.CategorySlug = strings.TrimSpace(i.CategorySlug)
	i.SubCategorySlug = strings.TrimSpace(i.SubCategorySlug)
	i.IndustrySlug = strings.TrimSpace(i.IndustrySlug)

	// Apply default timeout if not set
	if i.TimeoutMs <= 0 {
		i.TimeoutMs = 30000 // 30 seconds default
	}

	// Apply default pagination
	if i.Pagination.Size <= 0 {
		i.Pagination.Size = 10
	}
	if i.Pagination.Size > 100 {
		i.Pagination.Size = 100
	}
	if i.Pagination.From < 0 {
		i.Pagination.From = 0
	}

	// Convert page to from if provided
	if i.Pagination.Page > 0 && i.Pagination.From == 0 {
		i.Pagination.From = (i.Pagination.Page - 1) * i.Pagination.Size
	}
}

// Validate - Pagination validation
func (p *Pagination) Validate() error {
	return ozzo.ValidateStruct(p,
		ozzo.Field(&p.From, ozzo.Min(0), ozzo.Max(10000)),
		ozzo.Field(&p.Size, ozzo.Min(1), ozzo.Max(100)),
		ozzo.Field(&p.Page, ozzo.Min(1), ozzo.Max(1000)),
	)
}

// CalculatePage - Helper to calculate current page
func (p *Pagination) CalculatePage() int {
	if p.Size <= 0 {
		return 0
	}
	return (p.From / p.Size) + 1
}

// Output helpers
func (o *Output) WithPaginationInfo(page, pageSize, total int64) *Output {
	o.Page = int(page)
	o.PageSize = int(pageSize)
	o.TotalHits = total

	if pageSize > 0 {
		o.TotalPages = int(total / pageSize)
		if total%pageSize > 0 {
			o.TotalPages++
		}
	}

	return o
}

func (o *Output) WithError(code, message string) *Output {
	o.Success = false
	o.ErrorCode = code
	o.ErrorMessage = message
	return o
}

func (o *Output) WithMessage(message string) *Output {
	o.Message = message
	return o
}
