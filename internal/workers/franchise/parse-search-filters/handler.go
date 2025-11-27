// internal/workers/franchise/parse-search-filters/handler.go
package parsesearchfilters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"camunda-workers/internal/common/logger"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const TaskType = "parse-search-filters"

var (
	ErrInvalidFilterFormat = errors.New("INVALID_FILTER_FORMAT")
)

var validCategories = map[string]bool{
	"food": true, "retail": true, "health": true, "education": true,
	"automotive": true, "fitness": true, "beauty": true, "home": true,
}

var validSortOptions = map[string]bool{
	"relevance": true, "investment_min": true, "name": true,
}

type Handler struct {
	config *Config
	logger logger.Logger
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	return &Handler{
		config: config,
		logger: log.WithFields(map[string]interface{}{"taskType": TaskType}),
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	h.logger.Info("processing job", map[string]interface{}{
		"jobKey":      job.Key,
		"workflowKey": job.ProcessInstanceKey,
	})

	var input Input
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := h.execute(ctx, &input)
	if err != nil {
		h.failJob(client, job, "INVALID_FILTER_FORMAT", err.Error(), 0)
		return
	}

	h.completeJob(client, job, output)
}

func (h *Handler) execute(_ context.Context, input *Input) (*Output, error) {
	if input.RawFilters == nil {
		input.RawFilters = make(map[string]interface{})
	}

	// Initialize with defaults as per REQ-BIZ-002
	parsed := ParsedFilters{
		Categories:      []string{},
		Locations:       []string{},
		Keywords:        "",
		SortBy:          "relevance",
		Pagination:      Pagination{Page: 1, Size: 20},
		InvestmentRange: InvestmentRange{Min: 0, Max: 10000000},
	}

	// Parse categories
	if categoriesRaw, ok := input.RawFilters["categories"]; ok {
		parsed.Categories = h.parseStringArray(categoriesRaw)
		// Validate categories as per REQ-BIZ-003
		for _, cat := range parsed.Categories {
			if !validCategories[cat] {
				return nil, fmt.Errorf("%w: invalid category '%s'", ErrInvalidFilterFormat, cat)
			}
		}
	}

	// Parse investment range as per REQ-BIZ-002
	if invRangeRaw, ok := input.RawFilters["investmentRange"]; ok {
		if invMap, ok := invRangeRaw.(map[string]interface{}); ok {
			// Parse min
			if minRaw, exists := invMap["min"]; exists {
				if min, err := h.parseInt(minRaw); err == nil {
					// Validate min >= 0 as per REQ-BIZ-003
					if min >= 0 {
						parsed.InvestmentRange.Min = min
					}
				}
			}

			// Parse max
			if maxRaw, exists := invMap["max"]; exists {
				if max, err := h.parseInt(maxRaw); err == nil {
					// Validate max <= 10000000 as per REQ-BIZ-003
					if max > 0 && max <= 10000000 {
						parsed.InvestmentRange.Max = max
					}
				}
			}

			// Validate min <= max as per REQ-BIZ-003
			if parsed.InvestmentRange.Min > parsed.InvestmentRange.Max {
				return nil, fmt.Errorf("%w: investment min (%d) > max (%d)",
					ErrInvalidFilterFormat, parsed.InvestmentRange.Min, parsed.InvestmentRange.Max)
			}
		}
	}

	// Parse locations
	if locationsRaw, ok := input.RawFilters["locations"]; ok {
		parsed.Locations = h.parseStringArray(locationsRaw)
	}

	// Parse keywords
	if keywordsRaw, ok := input.RawFilters["keywords"]; ok {
		if s, ok := keywordsRaw.(string); ok {
			parsed.Keywords = strings.TrimSpace(s)
		}
	}

	// Parse sortBy with validation as per REQ-BIZ-003
	if sortByRaw, ok := input.RawFilters["sortBy"]; ok {
		if s, ok := sortByRaw.(string); ok {
			s = strings.TrimSpace(s)
			if validSortOptions[s] {
				parsed.SortBy = s
			} else {
				return nil, fmt.Errorf("%w: invalid sortBy '%s'", ErrInvalidFilterFormat, s)
			}
		}
	}

	// Parse pagination as per REQ-BIZ-002
	if paginationRaw, ok := input.RawFilters["pagination"]; ok {
		if pgMap, ok := paginationRaw.(map[string]interface{}); ok {
			// Parse page
			if pageRaw, exists := pgMap["page"]; exists {
				if page, err := h.parseInt(pageRaw); err == nil {
					// Validate page >= 1 as per REQ-BIZ-003
					if page >= 1 {
						parsed.Pagination.Page = page
					}
				}
			}

			// Parse size
			if sizeRaw, exists := pgMap["size"]; exists {
				if size, err := h.parseInt(sizeRaw); err == nil {
					// Validate size between 1 and 100 as per REQ-BIZ-003
					// Values > 100 are capped at 100
					if size >= 1 {
						if size <= 100 {
							parsed.Pagination.Size = size
						} else {
							parsed.Pagination.Size = 100
						}
					}
				}
			}
		}
	}

	h.logger.Info("filters parsed successfully", map[string]interface{}{
		"categories":     parsed.Categories,
		"investmentRange": parsed.InvestmentRange,
		"locations":      parsed.Locations,
		"keywords":       parsed.Keywords,
		"sortBy":         parsed.SortBy,
		"pagination":     parsed.Pagination,
	})

	return &Output{ParsedFilters: parsed}, nil
}

func (h *Handler) parseStringArray(raw interface{}) []string {
	// Always return non-nil slice
	result := []string{}

	if raw == nil {
		return result
	}

	seen := make(map[string]bool) // For deduplication

	switch v := raw.(type) {
	case string:
		if v != "" {
			parts := strings.Split(v, ",")
			for _, s := range parts {
				trimmed := strings.TrimSpace(s)
				if trimmed != "" && !seen[trimmed] {
					result = append(result, trimmed)
					seen[trimmed] = true
				}
			}
		}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				trimmed := strings.TrimSpace(s)
				if trimmed != "" && !seen[trimmed] {
					result = append(result, trimmed)
					seen[trimmed] = true
				}
			}
		}
	case []string:
		for _, s := range v {
			trimmed := strings.TrimSpace(s)
			if trimmed != "" && !seen[trimmed] {
				result = append(result, trimmed)
				seen[trimmed] = true
			}
		}
	}

	return result
}

func (h *Handler) parseInt(raw interface{}) (int, error) {
	if raw == nil {
		return 0, errors.New("cannot parse nil as integer")
	}

	switch v := raw.(type) {
	case float64:
		// Check if it's a valid positive integer
		if v < 0 || v != float64(int(v)) {
			return 0, errors.New("not a valid positive integer")
		}
		return int(v), nil

	case int:
		if v < 0 {
			return 0, errors.New("negative integer not allowed")
		}
		return v, nil

	case int64:
		if v < 0 {
			return 0, errors.New("negative integer not allowed")
		}
		return int(v), nil

	case string:
		// Handle special case: extract numbers and check for decimal point
		// "USD 50,000.00" should become "50000" not "5000000"

		// First remove currency symbols and spaces
		cleaned := strings.ReplaceAll(v, " ", "")
		cleaned = strings.ReplaceAll(cleaned, "$", "")
		cleaned = strings.ReplaceAll(cleaned, "USD", "")
		cleaned = strings.ReplaceAll(cleaned, ",", "")

		// If there's a decimal point, truncate at it (for monetary values)
		if strings.Contains(cleaned, ".") {
			parts := strings.Split(cleaned, ".")
			cleaned = parts[0]
		}

		// Now remove any remaining non-digit characters
		re := regexp.MustCompile(`[^\d]+`)
		cleaned = re.ReplaceAllString(cleaned, "")

		if cleaned == "" {
			return 0, errors.New("not a number")
		}

		num, err := strconv.Atoi(cleaned)
		if err != nil {
			return 0, fmt.Errorf("strconv.Atoi failed: %w", err)
		}
		if num < 0 {
			return 0, errors.New("negative integer not allowed")
		}
		return num, nil

	default:
		return 0, errors.New("not a number")
	}
}

func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		h.logger.Error("failed to create complete job command", map[string]interface{}{
			"error": err,
		})
		return
	}
	_, err = cmd.Send(context.Background())
	if err != nil {
		h.logger.Error("failed to send complete job command", map[string]interface{}{
			"error": err,
		})
	}
}

func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, _ int32) {
	h.logger.Error("job failed", map[string]interface{}{
		"jobKey":       job.Key,
		"errorCode":    errorCode,
		"errorMessage": errorMessage,
	})

	_, err := client.NewThrowErrorCommand().
		JobKey(job.Key).
		ErrorCode(errorCode).
		ErrorMessage(errorMessage).
		Send(context.Background())
	if err != nil {
		h.logger.Error("failed to throw error", map[string]interface{}{
			"error": err,
		})
	}
}

func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	return h.execute(ctx, input)
}

// // internal/workers/franchise/parse-search-filters/handler.go
// package parsesearchfilters

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"regexp"
// 	"strconv"
// 	"strings"
// 	"time"

// 	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
// 	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
// 	"go.uber.org/zap"
// )

// const TaskType = "parse-search-filters"

// var (
// 	ErrInvalidFilterFormat = errors.New("INVALID_FILTER_FORMAT")
// )

// var validCategories = map[string]bool{
// 	"food": true, "retail": true, "health": true, "education": true,
// 	"automotive": true, "fitness": true, "beauty": true, "home": true,
// }

// var validSortOptions = map[string]bool{
// 	"relevance": true, "investment_min": true, "name": true,
// }

// type Handler struct {
// 	config *Config
// 	logger *zap.Logger
// }

// func NewHandler(config *Config, logger *zap.Logger) *Handler {
// 	return &Handler{
// 		config: config,
// 		logger: logger.With(zap.String("taskType", TaskType)),
// 	}
// }

// func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
// 	h.logger.Info("processing job",
// 		zap.Int64("jobKey", job.Key),
// 		zap.Int64("workflowKey", job.ProcessInstanceKey),
// 	)

// 	var input Input
// 	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
// 		h.failJob(client, job, "PARSE_ERROR", fmt.Sprintf("parse input: %v", err), 0)
// 		return
// 	}

// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	output, err := h.execute(ctx, &input)
// 	if err != nil {
// 		h.failJob(client, job, "INVALID_FILTER_FORMAT", err.Error(), 0)
// 		return
// 	}

// 	h.completeJob(client, job, output)
// }

// func (h *Handler) execute(_ context.Context, input *Input) (*Output, error) {
// 	if input.RawFilters == nil {
// 		input.RawFilters = make(map[string]interface{})
// 	}

// 	// Initialize with defaults as per REQ-BIZ-002
// 	parsed := ParsedFilters{
// 		Categories:      []string{},
// 		Locations:       []string{},
// 		Keywords:        "",
// 		SortBy:          "relevance",
// 		Pagination:      Pagination{Page: 1, Size: 20},
// 		InvestmentRange: InvestmentRange{Min: 0, Max: 10000000},
// 	}

// 	// Parse categories
// 	if categoriesRaw, ok := input.RawFilters["categories"]; ok {
// 		parsed.Categories = h.parseStringArray(categoriesRaw)
// 		// Validate categories as per REQ-BIZ-003
// 		for _, cat := range parsed.Categories {
// 			if !validCategories[cat] {
// 				return nil, fmt.Errorf("%w: invalid category '%s'", ErrInvalidFilterFormat, cat)
// 			}
// 		}
// 	}

// 	// Parse investment range as per REQ-BIZ-002
// 	if invRangeRaw, ok := input.RawFilters["investmentRange"]; ok {
// 		if invMap, ok := invRangeRaw.(map[string]interface{}); ok {
// 			// Parse min
// 			if minRaw, exists := invMap["min"]; exists {
// 				if min, err := h.parseInt(minRaw); err == nil {
// 					// Validate min >= 0 as per REQ-BIZ-003
// 					if min >= 0 {
// 						parsed.InvestmentRange.Min = min
// 					}
// 				}
// 			}

// 			// Parse max
// 			if maxRaw, exists := invMap["max"]; exists {
// 				if max, err := h.parseInt(maxRaw); err == nil {
// 					// Validate max <= 10000000 as per REQ-BIZ-003
// 					if max > 0 && max <= 10000000 {
// 						parsed.InvestmentRange.Max = max
// 					}
// 				}
// 			}

// 			// Validate min <= max as per REQ-BIZ-003
// 			if parsed.InvestmentRange.Min > parsed.InvestmentRange.Max {
// 				return nil, fmt.Errorf("%w: investment min (%d) > max (%d)",
// 					ErrInvalidFilterFormat, parsed.InvestmentRange.Min, parsed.InvestmentRange.Max)
// 			}
// 		}
// 	}

// 	// Parse locations
// 	if locationsRaw, ok := input.RawFilters["locations"]; ok {
// 		parsed.Locations = h.parseStringArray(locationsRaw)
// 	}

// 	// Parse keywords
// 	if keywordsRaw, ok := input.RawFilters["keywords"]; ok {
// 		if s, ok := keywordsRaw.(string); ok {
// 			parsed.Keywords = strings.TrimSpace(s)
// 		}
// 	}

// 	// Parse sortBy with validation as per REQ-BIZ-003
// 	if sortByRaw, ok := input.RawFilters["sortBy"]; ok {
// 		if s, ok := sortByRaw.(string); ok {
// 			s = strings.TrimSpace(s)
// 			if validSortOptions[s] {
// 				parsed.SortBy = s
// 			} else {
// 				return nil, fmt.Errorf("%w: invalid sortBy '%s'", ErrInvalidFilterFormat, s)
// 			}
// 		}
// 	}

// 	// Parse pagination as per REQ-BIZ-002
// 	if paginationRaw, ok := input.RawFilters["pagination"]; ok {
// 		if pgMap, ok := paginationRaw.(map[string]interface{}); ok {
// 			// Parse page
// 			if pageRaw, exists := pgMap["page"]; exists {
// 				if page, err := h.parseInt(pageRaw); err == nil {
// 					// Validate page >= 1 as per REQ-BIZ-003
// 					if page >= 1 {
// 						parsed.Pagination.Page = page
// 					}
// 				}
// 			}

// 			// Parse size
// 			if sizeRaw, exists := pgMap["size"]; exists {
// 				if size, err := h.parseInt(sizeRaw); err == nil {
// 					// Validate size between 1 and 100 as per REQ-BIZ-003
// 					// Values > 100 are capped at 100
// 					if size >= 1 {
// 						if size <= 100 {
// 							parsed.Pagination.Size = size
// 						} else {
// 							parsed.Pagination.Size = 100
// 						}
// 					}
// 				}
// 			}
// 		}
// 	}

// 	h.logger.Info("filters parsed successfully",
// 		zap.Strings("categories", parsed.Categories),
// 		zap.Any("investmentRange", parsed.InvestmentRange),
// 		zap.Strings("locations", parsed.Locations),
// 		zap.String("keywords", parsed.Keywords),
// 		zap.String("sortBy", parsed.SortBy),
// 		zap.Any("pagination", parsed.Pagination),
// 	)

// 	return &Output{ParsedFilters: parsed}, nil
// }

// func (h *Handler) parseStringArray(raw interface{}) []string {
// 	// Always return non-nil slice
// 	result := []string{}

// 	if raw == nil {
// 		return result
// 	}

// 	seen := make(map[string]bool) // For deduplication

// 	switch v := raw.(type) {
// 	case string:
// 		if v != "" {
// 			parts := strings.Split(v, ",")
// 			for _, s := range parts {
// 				trimmed := strings.TrimSpace(s)
// 				if trimmed != "" && !seen[trimmed] {
// 					result = append(result, trimmed)
// 					seen[trimmed] = true
// 				}
// 			}
// 		}
// 	case []interface{}:
// 		for _, item := range v {
// 			if s, ok := item.(string); ok {
// 				trimmed := strings.TrimSpace(s)
// 				if trimmed != "" && !seen[trimmed] {
// 					result = append(result, trimmed)
// 					seen[trimmed] = true
// 				}
// 			}
// 		}
// 	case []string:
// 		for _, s := range v {
// 			trimmed := strings.TrimSpace(s)
// 			if trimmed != "" && !seen[trimmed] {
// 				result = append(result, trimmed)
// 				seen[trimmed] = true
// 			}
// 		}
// 	}

// 	return result
// }

// func (h *Handler) parseInt(raw interface{}) (int, error) {
// 	if raw == nil {
// 		return 0, errors.New("cannot parse nil as integer")
// 	}

// 	switch v := raw.(type) {
// 	case float64:
// 		// Check if it's a valid positive integer
// 		if v < 0 || v != float64(int(v)) {
// 			return 0, errors.New("not a valid positive integer")
// 		}
// 		return int(v), nil

// 	case int:
// 		if v < 0 {
// 			return 0, errors.New("negative integer not allowed")
// 		}
// 		return v, nil

// 	case int64:
// 		if v < 0 {
// 			return 0, errors.New("negative integer not allowed")
// 		}
// 		return int(v), nil

// 	case string:
// 		// Handle special case: extract numbers and check for decimal point
// 		// "USD 50,000.00" should become "50000" not "5000000"

// 		// First remove currency symbols and spaces
// 		cleaned := strings.ReplaceAll(v, " ", "")
// 		cleaned = strings.ReplaceAll(cleaned, "$", "")
// 		cleaned = strings.ReplaceAll(cleaned, "USD", "")
// 		cleaned = strings.ReplaceAll(cleaned, ",", "")

// 		// If there's a decimal point, truncate at it (for monetary values)
// 		if strings.Contains(cleaned, ".") {
// 			parts := strings.Split(cleaned, ".")
// 			cleaned = parts[0]
// 		}

// 		// Now remove any remaining non-digit characters
// 		re := regexp.MustCompile(`[^\d]+`)
// 		cleaned = re.ReplaceAllString(cleaned, "")

// 		if cleaned == "" {
// 			return 0, errors.New("not a number")
// 		}

// 		num, err := strconv.Atoi(cleaned)
// 		if err != nil {
// 			return 0, fmt.Errorf("strconv.Atoi failed: %w", err)
// 		}
// 		if num < 0 {
// 			return 0, errors.New("negative integer not allowed")
// 		}
// 		return num, nil

// 	default:
// 		return 0, errors.New("not a number")
// 	}
// }

// func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
// 	cmd, err := client.NewCompleteJobCommand().
// 		JobKey(job.Key).
// 		VariablesFromObject(output)
// 	if err != nil {
// 		h.logger.Error("failed to create complete job command", zap.Error(err))
// 		return
// 	}
// 	_, err = cmd.Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to send complete job command", zap.Error(err))
// 	}
// }

// func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode, errorMessage string, _ int32) {
// 	h.logger.Error("job failed",
// 		zap.Int64("jobKey", job.Key),
// 		zap.String("errorCode", errorCode),
// 		zap.String("errorMessage", errorMessage),
// 	)

// 	_, err := client.NewThrowErrorCommand().
// 		JobKey(job.Key).
// 		ErrorCode(errorCode).
// 		ErrorMessage(errorMessage).
// 		Send(context.Background())
// 	if err != nil {
// 		h.logger.Error("failed to throw error", zap.Error(err))
// 	}
// }

// func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
// 	return h.execute(ctx, input)
// }
