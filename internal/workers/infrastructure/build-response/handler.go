package buildresponse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	ozzo "github.com/go-ozzo/ozzo-validation/v4"

	appErrs "camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const TaskType = "build-response"

type Handler struct {
	config       *Config
	logger       logger.Logger
	errorHandler *appErrs.ErrorHandler
	validator    *validation.Validator
	sanitizer    *validation.Sanitizer
}

func NewHandler(config *Config, log logger.Logger) *Handler {
	return &Handler{
		config:       config,
		logger:       log.WithFields(map[string]interface{}{"taskType": TaskType}),
		errorHandler: appErrs.NewErrorHandler(log),
		validator:    validation.NewValidator(),
		sanitizer:    validation.NewSanitizer(),
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	ctx := context.Background()

	var traceID, parentSpanID string
	var jobVars map[string]interface{}

	if err := json.Unmarshal([]byte(job.Variables), &jobVars); err == nil {
		if tid, ok := jobVars["traceId"].(string); ok {
			traceID = tid
		}
		if psid, ok := jobVars["spanId"].(string); ok {
			parentSpanID = psid
		}
	}

	tracer := otel.Tracer("worker-manager")
	ctx, span := tracer.Start(ctx, "worker:"+TaskType,
		trace.WithAttributes(
			attribute.String("worker.name", TaskType),
			attribute.Int64("job.key", job.GetKey()),
			attribute.Int64("workflow.instance_key", job.GetProcessInstanceKey()),
			attribute.String("workflow.process_id", job.GetBpmnProcessId()),
			attribute.String("workflow.element_id", job.GetElementId()),
			attribute.String("trace.parent_id", parentSpanID),
		),
	)
	defer span.End()

	h.logger.Info("processing job",
		map[string]interface{}{
			"jobKey":      job.Key,
			"workflowKey": job.ProcessInstanceKey,
			"traceId":     traceID,
			"spanId":      span.SpanContext().SpanID().String(),
		})

	var input Input
	_, spanParse := otel.Tracer("worker-manager").Start(ctx, "build-response.parseInput")

	h.logger.Debug("Job variables received", map[string]interface{}{
		"rawVarsLength": len(job.Variables),
		"elementId":     job.GetElementId(),
		"first500Chars": safeSubstring(job.Variables, 0, 500),
	})

	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job,
			appErrs.NewValidationError("input", fmt.Sprintf("parse input: %v", err)))
		spanParse.End()
		return
	}

	h.logger.Info("Parsed input structure", map[string]interface{}{
		"pageType":   input.PageType,
		"dataKeys":   h.getKeys(input.Data),
		"hasDataKey": input.Data != nil && input.Data["data"] != nil,
		"metadata":   input.Metadata != nil,
	})

	spanParse.End()

	_, spanValidate := otel.Tracer("worker-manager").Start(ctx, "build-response.validateInput")
	if err := h.validateInput(&input); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
		h.errorHandler.HandleJobError(ctx, client, job, err)
		spanValidate.End()
		return
	}
	spanValidate.End()

	ctxExec, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ctxExec, spanExec := otel.Tracer("worker-manager").Start(ctxExec, "build-response.Execute")
	output, err := h.Execute(ctxExec, &input)
	spanExec.End()
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))

		stdErr := appErrs.NewExternalServiceError("build-response", err)
		h.errorHandler.HandleJobError(ctx, client, job, stdErr)
		return
	}

	_, spanComp := otel.Tracer("worker-manager").Start(ctx, "build-response.completeJob")
	h.completeJob(ctx, client, job, output)
	spanComp.End()
}

// ===== HELPER FUNCTIONS =====

func (h *Handler) extractArray(data map[string]interface{}, key string) []interface{} {
	if data == nil {
		return []interface{}{}
	}

	if arr, ok := data[key].([]interface{}); ok {
		h.logger.Debug("Direct array access successful", map[string]interface{}{
			"key":   key,
			"count": len(arr),
		})
		return arr
	}

	if nestedData, ok := data["data"].(map[string]interface{}); ok {
		if arr, ok := nestedData[key].([]interface{}); ok {
			h.logger.Debug("Nested array access successful", map[string]interface{}{
				"key":    key,
				"count":  len(arr),
				"nested": true,
			})
			return arr
		}
	}

	if str, ok := data[key].(string); ok {
		var arr []interface{}
		if err := json.Unmarshal([]byte(str), &arr); err == nil {
			h.logger.Debug("JSON string unmarshaled", map[string]interface{}{
				"key":   key,
				"count": len(arr),
				"from":  "string",
			})
			return arr
		}
	}

	h.logger.Debug("Array extraction failed", map[string]interface{}{
		"key":        key,
		"value":      data[key],
		"valueType":  fmt.Sprintf("%T", data[key]),
		"hasDataKey": data["data"] != nil,
	})

	return []interface{}{}
}

func (h *Handler) extractMap(data map[string]interface{}, key string) map[string]interface{} {
	if data == nil {
		return map[string]interface{}{}
	}

	if m, ok := data[key].(map[string]interface{}); ok {
		return m
	}

	if nestedData, ok := data["data"].(map[string]interface{}); ok {
		if m, ok := nestedData[key].(map[string]interface{}); ok {
			return m
		}
	}

	if str, ok := data[key].(string); ok {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(str), &m); err == nil {
			return m
		}
	}

	return map[string]interface{}{}
}

func (h *Handler) extractValue(data map[string]interface{}, key string) interface{} {
	if data == nil {
		return nil
	}

	if value, exists := data[key]; exists {
		return value
	}

	if nestedData, ok := data["data"].(map[string]interface{}); ok {
		if value, exists := nestedData[key]; exists {
			return value
		}
	}

	return nil
}

func (h *Handler) getKeys(data map[string]interface{}) []string {
	if data == nil {
		return []string{}
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return keys
}

func safeSubstring(s string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(s) {
		end = len(s)
	}
	if start >= end {
		return ""
	}
	return s[start:end]
}

// ===== VALIDATION FUNCTION =====
func (h *Handler) validateInput(input *Input) error {
	// pageType empty skip (enquiry/application workflows)
	if input.PageType == "" {
		return nil
	}

	if err := ozzo.Validate(input.PageType,
		ozzo.Required.Error("pageType is required"),
		ozzo.In("home", "listing", "detail", "search", "industries").Error("must be one of: home, listing, detail, search, industries"),
		validation.SafeSQLString,
	); err != nil {
		return appErrs.NewValidationError("pageType", err.Error())
	}

	if input.Data != nil {
		if err := h.validateDataDepth(input.Data, 0); err != nil {
			return err
		}
		if err := h.validateDataSize(input.Data); err != nil {
			return err
		}
	}

	return nil
}

func (h *Handler) validateDataDepth(data map[string]interface{}, depth int) error {
	if depth > 10 {
		return appErrs.NewValidationError("data", "object nesting too deep (max 10 levels)")
	}

	for key, value := range data {
		if err := ozzo.Validate(key,
			ozzo.Length(1, 100).Error("key must be 1-100 characters"),
			validation.SafeNoSQLString,
			// validation.AlphanumericOnly,
		); err != nil {
			return appErrs.NewValidationError(fmt.Sprintf("data.%s", key), err.Error())
		}

		if nestedMap, ok := value.(map[string]interface{}); ok {
			if err := h.validateDataDepth(nestedMap, depth+1); err != nil {
				return err
			}
		}

		if arr, ok := value.([]interface{}); ok {
			if len(arr) > 1000 {
				return appErrs.NewArrayTooLargeError(fmt.Sprintf("data.%s", key), 1000, len(arr))
			}

			for i, item := range arr {
				if nestedMap, ok := item.(map[string]interface{}); ok {
					if err := h.validateDataDepth(nestedMap, depth+1); err != nil {
						return appErrs.NewValidationError(fmt.Sprintf("data.%s[%d]", key, i), err.Error())
					}
				}
			}
		}
	}

	return nil
}

func (h *Handler) validateDataSize(data map[string]interface{}) error {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return appErrs.NewValidationError("data", fmt.Sprintf("cannot serialize data: %v", err))
	}

	sizeKB := len(jsonBytes) / 1024
	if sizeKB > 100 {
		return appErrs.NewValidationError("data",
			fmt.Sprintf("data too large (%d KB), maximum is 100 KB", sizeKB))
	}

	return nil
}

// ===== EXECUTE METHOD =====
func (h *Handler) Execute(ctx context.Context, input *Input) (*Output, error) {
	combinedData := make(map[string]interface{})

	// ✅ DETAIL PAGE - Map individual fields
	if len(input.BasicInfo) > 0 {
		combinedData["basicInfo"] = input.BasicInfo
	}
	if len(input.Business) > 0 {
		combinedData["business"] = input.Business
	}
	if len(input.Investment) > 0 {
		combinedData["investment"] = input.Investment
	}
	if len(input.Operations) > 0 {
		combinedData["operations"] = input.Operations
	}
	if len(input.Overview) > 0 {
		combinedData["overview"] = input.Overview
	}
	if len(input.Social) > 0 {
		combinedData["social"] = input.Social
	}
	if len(input.Categories) > 0 {
		combinedData["categories"] = input.Categories
	}
	if len(input.Recommended) > 0 {
		combinedData["recommended"] = input.Recommended
	}
	if input.MarketInsights != nil {
		combinedData["marketInsights"] = input.MarketInsights
	}
	if len(input.CategoryQuestions) > 0 {
		combinedData["categoryQuestions"] = input.CategoryQuestions
	}

	// ✅ LISTING PAGE - Map listing-specific fields
	if len(input.FranchiseListings) > 0 {
		combinedData["franchises"] = input.FranchiseListings
	}
	if len(input.FeaturedCategories) > 0 {
		combinedData["categories"] = input.FeaturedCategories
	}
	if len(input.UnderstandingCategory) > 0 {
		combinedData["categoryQuestions"] = input.UnderstandingCategory
	}
	if len(input.RecommendedFranchises) > 0 {
		combinedData["recommended"] = input.RecommendedFranchises
	}
	if len(input.KeyMarketInsights) > 0 {
		combinedData["marketInsights"] = input.KeyMarketInsights
	}
	if input.HeroDescription != "" {
		combinedData["heroDescription"] = input.HeroDescription
	}

	combinedData["totalCount"] = input.TotalCount
	combinedData["page"] = input.Page
	combinedData["pageSize"] = input.PageSize

	// ✅ HOME PAGE - Map home-specific fields
	if len(input.HeroBrands) > 0 {
		combinedData["heroBrands"] = input.HeroBrands
	}
	if len(input.Industries) > 0 {
		combinedData["industries"] = input.Industries
	}
	if len(input.PopularListings) > 0 {
		combinedData["popularListings"] = input.PopularListings
	}
	if len(input.Categories) > 0 {
		combinedData["categories"] = input.Categories
	}

	// ✅ Also merge anything in input.Data (backup/generic data)
	if input.Data != nil {
		for k, v := range input.Data {
			if _, exists := combinedData[k]; !exists {
				combinedData[k] = v
			}
		}
	}

	// Sanitize and build response
	combinedData = h.sanitizer.SanitizeInput(combinedData)

	var response map[string]interface{}
	switch input.PageType {
	case "home":
		response = h.buildHomeResponse(combinedData)
	case "listing":
		response = h.buildListingResponse(combinedData)
	case "detail":
		response = h.buildDetailResponse(combinedData)
	case "search":
		response = h.buildSearchResponse(combinedData)
	case "industries":
		response = h.buildIndustriesResponse(combinedData)
	default:
		// Generic response - enquiry/application workflows
		response = h.buildGenericResponse(input)
		// return nil, fmt.Errorf("unknown page type: %s", input.PageType)
	}

	success := true
	if response != nil {
		if s, ok := response["success"].(bool); ok {
			success = s
		} else {
			response["success"] = true
		}
	}

	return &Output{Success: success, Response: response}, nil
}

// ===== HOME PAGE BUILDER =====
func (h *Handler) buildHomeResponse(data map[string]interface{}) map[string]interface{} {
	sections := []map[string]interface{}{}

	h.logger.Info("Building home response", map[string]interface{}{
		"dataKeys":   h.getKeys(data),
		"hasDataKey": data["data"] != nil,
	})

	heroBrands := h.extractArray(data, "heroBrands")
	if len(heroBrands) > 0 {
		h.logger.Info("Hero brands found", map[string]interface{}{
			"count": len(heroBrands),
		})

		sections = append(sections, map[string]interface{}{
			"type":    "hero",
			"enabled": true,
			"data": map[string]interface{}{
				"topBrandLogos": heroBrands,
			},
		})
	} else {
		h.logger.Warn("No hero brands extracted", map[string]interface{}{
			"dataKeys": h.getKeys(data),
		})
	}

	industries := h.extractArray(data, "industries")
	if len(industries) > 0 {
		sections = append(sections, map[string]interface{}{
			"type":    "top_franchise_opportunities",
			"enabled": true,
			"data": map[string]interface{}{
				"industries": industries,
			},
		})
	}

	// ✅ DEFENSIVE TRANSFORMATION - Handles both transformed and untransformed data
	listings := h.extractArray(data, "popularListings")
	if len(listings) > 0 {
		transformedListings := make([]map[string]interface{}, 0, len(listings))

		for _, item := range listings {
			listing, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			transformed := map[string]interface{}{}

			// ✅ Handle franchise_id OR id (defensive)
			if franchiseID, ok := listing["franchise_id"].(string); ok {
				transformed["id"] = franchiseID
			} else if id, ok := listing["id"].(string); ok {
				transformed["id"] = id
			}

			// ✅ Handle name OR brand (defensive)
			if name, ok := listing["name"].(string); ok {
				transformed["brand"] = name
			} else if brand, ok := listing["brand"].(string); ok {
				transformed["brand"] = brand
			}

			// ✅ Extract category from industry.name OR use existing category
			if industry, ok := listing["industry"].(map[string]interface{}); ok {
				if categoryName, ok := industry["name"].(string); ok {
					transformed["category"] = categoryName
				}
				if color, ok := industry["color"].(string); ok {
					transformed["color"] = color
				}
			} else if category, ok := listing["category"].(string); ok {
				transformed["category"] = category
			}

			// Copy remaining fields
			transformed["description"] = listing["description"]
			transformed["year_of_establishment"] = listing["year_of_establishment"]
			transformed["rating"] = listing["rating"]
			transformed["location"] = listing["location"]
			transformed["tags"] = listing["tags"]
			transformed["space"] = listing["space"]
			transformed["slug"] = listing["slug"]

			// ✅ Handle total_outlets OR no_of_outlets (defensive)
			if outlets, ok := listing["total_outlets"]; ok {
				transformed["no_of_outlets"] = outlets
			} else if outlets, ok := listing["no_of_outlets"]; ok {
				transformed["no_of_outlets"] = outlets
			}

			// Keep investmentRange
			transformed["investmentRange"] = listing["investmentRange"]

			// ✅ Handle logo_url OR logo object (defensive)
			if logo, ok := listing["logo"].(map[string]interface{}); ok {
				transformed["logo"] = logo
			} else {
				brandName := ""
				if b, ok := transformed["brand"].(string); ok {
					brandName = b
				}
				transformed["logo"] = map[string]interface{}{
					"circle": "",
					"square": "",
					"alt":    brandName,
				}
			}

			transformedListings = append(transformedListings, transformed)
		}

		sections = append(sections, map[string]interface{}{
			"type":    "popular_listings",
			"enabled": true,
			"data":    transformedListings,
		})
	}

	categories := h.extractArray(data, "categories")
	if len(categories) > 0 {
		sections = append(sections, map[string]interface{}{
			"type":    "explore_by_categories",
			"enabled": true,
			"data": map[string]interface{}{
				"categories": categories,
			},
		})
	}

	if sections == nil {
		sections = []map[string]interface{}{}
	}

	h.logger.Info("Built home response sections", map[string]interface{}{
		"totalSections": len(sections),
		"hasHero":       len(heroBrands) > 0,
		"hasIndustries": len(industries) > 0,
		"hasListings":   len(listings) > 0,
		"hasCategories": len(categories) > 0,
	})

	return map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"pageId":   "franchise_home",
			"sections": sections,
		},
		"metadata": map[string]interface{}{
			"generatedAt": time.Now().UTC().Format(time.RFC3339),
			"source":      "workflow",
			"pageType":    "home",
		},
	}
}

// ===== LISTING PAGE BUILDER =====
func (h *Handler) buildListingResponse(data map[string]interface{}) map[string]interface{} {
	sections := []map[string]interface{}{}

	industryInfo := h.extractMap(data, "industryInfo")
	franchises := h.extractArray(data, "franchises")
	categories := h.extractArray(data, "categories")
	categoryQuestions := h.extractArray(data, "categoryQuestions")
	recommended := h.extractArray(data, "recommended")
	marketInsights := h.extractArray(data, "marketInsights")
	page := 1
	pageSize := 6
	totalCount := int64(0)

	heroTitle := "Franchise Opportunities in India"
	heroDescription := "Explore top franchise opportunities in India"

	if len(industryInfo) > 0 {
		// Name se default title/description banao
		name := ""
		if n, ok := industryInfo["name"].(string); ok && n != "" {
			name = n
		} else if n, ok := industryInfo["industry_name"].(string); ok && n != "" {
			name = n
		}

		if name != "" {
			heroTitle = fmt.Sprintf("%s Franchise Opportunities in India", name)
			heroDescription = fmt.Sprintf("Explore top %s franchise opportunities in India", name)
		}

		// DB mein stored hai toh override karo
		if t, ok := industryInfo["listing_title"].(string); ok && t != "" {
			heroTitle = t
		}
		if d, ok := industryInfo["listing_description"].(string); ok && d != "" {
			heroDescription = d
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "hero",
		"enabled": true,
		"data": map[string]interface{}{
			"title":       heroTitle,
			"description": heroDescription,
		},
	})

	if len(franchises) > 0 {
		for _, f := range franchises {
			franchise, ok := f.(map[string]interface{})
			if !ok {
				continue
			}

			if space, ok := franchise["space"].(map[string]interface{}); ok {
				if _, hasUnit := space["spaceUnit"]; !hasUnit {
					space["spaceUnit"] = "sq ft"
				}
			}

			if invRange, ok := franchise["investmentRange"].(map[string]interface{}); ok {
				if _, hasUnit := invRange["investmentUnit"]; !hasUnit {
					invRange["investmentUnit"] = "Lakhs"
				}
			}
		}

		sections = append(sections, map[string]interface{}{
			"type":    "listing",
			"enabled": true,
			"data": map[string]interface{}{
				"items": franchises,
			},
		})
	}

	if len(categories) > 0 {
		sections = append(sections, map[string]interface{}{
			"type":    "featured_categories",
			"enabled": true,
			"data":    categories,
		})
	}

	if len(categoryQuestions) > 0 {
		sections = append(sections, map[string]interface{}{
			"type":    "category_questions",
			"enabled": true,
			"data": map[string]interface{}{
				"questions": categoryQuestions,
			},
		})
	}

	if len(recommended) > 0 {
		transformedRecommended := make([]map[string]interface{}, 0, len(recommended))

		for _, item := range recommended {
			rec, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			// Extract industry as STRING
			industryName := ""
			if industry, ok := rec["industry"].(map[string]interface{}); ok {
				if name, ok := industry["name"].(string); ok {
					industryName = name
				}
			} else if industry, ok := rec["industry"].(string); ok {
				industryName = industry
			}

			// ✅ NEW — industry image_url extract karo
			industryImageURL := ""
			if industry, ok := rec["industry"].(map[string]interface{}); ok {
				if imgURL, ok := industry["image_url"].(string); ok {
					industryImageURL = imgURL
				}
			}

			circle := ""
			square := ""
			if logo, ok := rec["logo"].(map[string]interface{}); ok {
				if c, ok := logo["circle"].(string); ok {
					circle = c
				}
				if s, ok := logo["square"].(string); ok {
					square = s
				}
			}

			transformed := map[string]interface{}{
				"id":                 rec["id"],
				"brand":              rec["brand"],
				"industry":           industryName,     // ✅ String, not object
				"industry_image_url": industryImageURL, // ✅ NEW
				"slug":               rec["slug"],
				"image": map[string]interface{}{
					"circle": circle,
					"square": square,
					"alt":    rec["brand"],
				},
			}

			transformedRecommended = append(transformedRecommended, transformed)
		}

		sections = append(sections, map[string]interface{}{
			"type":    "recommended_franchises",
			"enabled": true,
			"data": map[string]interface{}{
				"items": transformedRecommended, // ✅ Use transformed
			},
		})
	}

	if len(marketInsights) > 0 {
		if insights, ok := marketInsights[0].(map[string]interface{}); ok {
			sections = append(sections, map[string]interface{}{
				"type":    "key_market_insights",
				"enabled": true,
				"data":    insights,
			})
		}
	}

	switch v := data["page"].(type) {
	case float64:
		if v > 0 {
			page = int(v)
		}
	case int:
		if v > 0 {
			page = v
		}
	}

	switch v := data["pageSize"].(type) {
	case float64:
		if v > 0 {
			pageSize = int(v)
		}
	case int:
		if v > 0 {
			pageSize = v
		}
	}

	switch v := data["totalCount"].(type) {
	case float64:
		totalCount = int64(v)
	case int:
		totalCount = int64(v)
	case int64:
		totalCount = v
	}

	totalPages := 0
	if pageSize > 0 {
		totalPages = int(math.Ceil(float64(totalCount) / float64(pageSize)))
	}

	if totalPages > 0 && page > totalPages {
		page = totalPages
	}

	pagination := map[string]interface{}{
		"page":        page,
		"page_size":   pageSize,
		"total_items": totalCount,
		"total_pages": totalPages,
		"has_next":    page < totalPages,
		"has_prev":    page > 1,
		"is_empty":    totalCount == 0,
	}

	return map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"sections":   sections,
			"pagination": pagination,
			"franchises": franchises, // ✅ Added for compatibility
		},
		"metadata": map[string]interface{}{
			"generatedAt": time.Now().UTC().Format(time.RFC3339),
			"source":      "workflow",
			"pageType":    "listing",
		},
	}
}

// ===== DETAIL PAGE BUILDER =====
func (h *Handler) buildDetailResponse(data map[string]interface{}) map[string]interface{} {
	basicInfo := h.extractMap(data, "basicInfo")
	overview := h.extractMap(data, "overview")
	business := h.extractMap(data, "business")
	investment := h.extractMap(data, "investment")
	operations := h.extractMap(data, "operations")
	socialLinks := h.extractArray(data, "social")
	categories := h.extractArray(data, "categories")
	recommended := h.extractArray(data, "recommended")
	marketInsights := h.extractArray(data, "marketInsights")
	categoryQuestions := h.extractArray(data, "categoryQuestions")

	detailData := map[string]interface{}{}

	// ✅ Extract franchiseId (defensive)
	if len(basicInfo) > 0 {
		if fid, ok := basicInfo["franchise_id"].(string); ok && fid != "" {
			detailData["franchiseId"] = fid
		} else if id, ok := basicInfo["id"].(string); ok && id != "" {
			detailData["franchiseId"] = id
		}

		if slug, ok := basicInfo["slug"].(string); ok && slug != "" {
			detailData["slug"] = slug
		}
	}

	detailData["basicInfo"] = h.buildBasicInfoStructure(basicInfo)

	if len(socialLinks) > 0 {
		detailData["social_media"] = h.buildSocialMediaStructure(socialLinks)
	} else {
		detailData["social_media"] = map[string]interface{}{
			"instagram": "",
			"facebook":  "",
			"linkedin":  "",
			"twitter":   "",
			"youtube":   "",
		}
	}

	detailData["franchising_overview"] = h.buildFranchisingOverviewStructure(overview, investment, basicInfo)
	detailData["business_overview"] = h.buildBusinessOverviewStructure(business, operations)
	detailData["investment_details"] = h.buildInvestmentDetailsStructure(investment, operations)
	detailData["operation"] = h.buildOperationStructure(operations)

	sections := []map[string]interface{}{}

	if len(categories) > 0 {
		sections = append(sections, map[string]interface{}{
			"type":    "featured_categories",
			"enabled": true,
			"data":    categories,
		})
	}

	// Add this transformation for recommended items

	// LISTING PAGE - Line 669
	if len(recommended) > 0 {
		transformedRecommended := make([]map[string]interface{}, 0, len(recommended))

		for _, item := range recommended {
			rec, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			// Extract industry as STRING
			industryName := ""
			if industry, ok := rec["industry"].(map[string]interface{}); ok {
				if name, ok := industry["name"].(string); ok {
					industryName = name
				}
			} else if industry, ok := rec["industry"].(string); ok {
				industryName = industry
			}

			// ✅ NEW — industry image_url extract karo
			industryImageURL := ""
			if industry, ok := rec["industry"].(map[string]interface{}); ok {
				if imgURL, ok := industry["image_url"].(string); ok {
					industryImageURL = imgURL
				}
			}

			circle := ""
			square := ""
			if logo, ok := rec["logo"].(map[string]interface{}); ok {
				if c, ok := logo["circle"].(string); ok {
					circle = c
				}
				if s, ok := logo["square"].(string); ok {
					square = s
				}
			}

			transformed := map[string]interface{}{
				"id":                 rec["id"],
				"brand":              rec["brand"],
				"industry":           industryName,     // ✅ String, not object
				"industry_image_url": industryImageURL, // ✅ NEW
				"slug":               rec["slug"],
				"image": map[string]interface{}{
					"circle": circle,
					"square": square,
					"alt":    rec["brand"],
				},
			}

			transformedRecommended = append(transformedRecommended, transformed)
		}

		sections = append(sections, map[string]interface{}{
			"type":    "recommended_franchises",
			"enabled": true,
			"data": map[string]interface{}{
				"items": transformedRecommended, // ✅ Use transformed
			},
		})
	}

	if len(categoryQuestions) > 0 {
		sections = append(sections, map[string]interface{}{
			"type":    "category_questions",
			"enabled": true,
			"data": map[string]interface{}{
				"questions": categoryQuestions,
			},
		})
	}

	detailData["sections"] = sections

	if len(marketInsights) > 0 {
		if insights, ok := marketInsights[0].(map[string]interface{}); ok {
			detailData["key_market_insights"] = insights
		}
	} else {
		detailData["key_market_insights"] = nil
	}

	return map[string]interface{}{
		"success": true,
		"data":    detailData,
		"metadata": map[string]interface{}{
			"generatedAt": time.Now().UTC().Format(time.RFC3339),
			"source":      "workflow",
			"pageType":    "detail",
		},
	}
}

// ===== INDUSTRIES PAGE BUILDER =====
func (h *Handler) buildIndustriesResponse(data map[string]interface{}) map[string]interface{} {
	industries := h.extractArray(data, "industries")

	cleaned := make([]map[string]interface{}, 0, len(industries))

	for _, item := range industries {
		industry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		cleanedCategories := []map[string]interface{}{}
		if cats, ok := industry["categories"].([]interface{}); ok {
			for _, catItem := range cats {
				cat, ok := catItem.(map[string]interface{})
				if !ok {
					continue
				}

				cleanedSubs := []map[string]interface{}{}
				if subs, ok := cat["sub_categories"].([]interface{}); ok {
					for _, subItem := range subs {
						sub, ok := subItem.(map[string]interface{})
						if !ok {
							continue
						}
						cleanedSubs = append(cleanedSubs, map[string]interface{}{
							"sub_category_name": sub["sub_category_name"],
						})
					}
				}

				cleanedCategories = append(cleanedCategories, map[string]interface{}{
					"category_name":  cat["category_name"],
					"category_slug":  cat["category_slug"],
					"icon_url":       cat["icon_url"],  // ✅ ADD
					"image_url":      cat["image_url"], // ✅ NEW
					"sub_categories": cleanedSubs,
				})
			}
		}

		cleaned = append(cleaned, map[string]interface{}{
			"industry_name": industry["industry_name"],
			"categories":    cleanedCategories,
		})
	}

	return map[string]interface{}{
		"success": true,
		"data":    cleaned,
		"metadata": map[string]interface{}{
			"generatedAt": time.Now().UTC().Format(time.RFC3339),
			"source":      "workflow",
			"pageType":    "industries",
		},
	}
}

// ===== GENERIC RESPONSE BUILDER =====
func (h *Handler) buildGenericResponse(input *Input) map[string]interface{} {
	data := map[string]interface{}{}
	success := true

	if input.Data != nil {
		for k, v := range input.Data {
			data[k] = v
		}
		// If success is explicitly passed in data, use it
		if s, ok := input.Data["success"].(bool); ok {
			success = s
		}
	}

	return map[string]interface{}{
		"success": success,
		"data":    data,
		"metadata": map[string]interface{}{
			"generatedAt": time.Now().UTC().Format(time.RFC3339),
			"source":      "workflow",
		},
	}
}

// ===== HELPER BUILDERS =====
func (h *Handler) buildBasicInfoStructure(basicInfo map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}

	// Copy all fields except metadata
	for key, value := range basicInfo {
		if key == "_id" || key == "_score" || key == "updated_at" {
			continue
		}
		result[key] = value
	}

	// ✅ DEFENSIVE: Handle logo structure (logo object OR logo_url)

	if logo, ok := result["logo"].(map[string]interface{}); ok {
		// Already circle/square format — bas alt ensure karo
		name := ""
		if n, ok := basicInfo["name"].(string); ok {
			name = n
		} else if b, ok := basicInfo["brand"].(string); ok {
			name = b
		}
		if _, hasAlt := logo["alt"]; !hasAlt {
			logo["alt"] = name
		}
	} else {
		// Logo object nahi hai — empty banao
		name := ""
		if n, ok := basicInfo["name"].(string); ok {
			name = n
		} else if b, ok := basicInfo["brand"].(string); ok {
			name = b
		}
		result["logo"] = map[string]interface{}{
			"circle": "",
			"square": "",
			"alt":    name,
		}
	}
	delete(result, "logo_url")

	return result
}

func (h *Handler) buildSocialMediaStructure(socialLinks []interface{}) map[string]interface{} {
	result := map[string]interface{}{
		"instagram": "",
		"facebook":  "",
		"linkedin":  "",
		"twitter":   "",
		"youtube":   "",
	}

	for _, link := range socialLinks {
		if linkMap, ok := link.(map[string]interface{}); ok {
			platform, _ := linkMap["platform"].(string)
			url, _ := linkMap["url"].(string)

			switch platform {
			case "instagram":
				result["instagram"] = url
			case "facebook":
				result["facebook"] = url
			case "twitter":
				result["twitter"] = url
			case "linkedin":
				result["linkedin"] = url
			case "youtube":
				result["youtube"] = url
			}
		}
	}

	return result
}

func (h *Handler) buildFranchisingOverviewStructure(overview, investment, basicInfo map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}

	// Initial investment
	minInv := getFloatValue(investment, "initial_investment_min")
	maxInv := getFloatValue(investment, "initial_investment_max")
	if minInv > 0 || maxInv > 0 {
		result["initial_investment"] = map[string]interface{}{
			"min":  minInv,
			"max":  maxInv,
			"unit": "INR",
		}
	}

	// Units count — with "as of year" context
	unitsCount := 0
	if u, ok := overview["units_count"].(float64); ok {
		unitsCount = int(u)
	} else if u, ok := overview["units_count"].(int32); ok {
		unitsCount = int(u)
	} else if u, ok := basicInfo["total_outlets"].(float64); ok {
		unitsCount = int(u)
	} else if u, ok := basicInfo["no_of_outlets"].(float64); ok {
		unitsCount = int(u)
	}

	if unitsCount > 0 {
		unitsData := map[string]interface{}{
			"count": unitsCount,
		}
		// FIX: attach established_year so frontend can render "X units as of 20XX"
		if year, ok := overview["established_year"].(int32); ok && year > 0 {
			unitsData["as_of_year"] = year
		} else if year, ok := overview["established_year"].(float64); ok && year > 0 {
			unitsData["as_of_year"] = int(year)
		}
		result["number_of_units"] = unitsData
	}

	if unitsCount == 0 {
		if u, ok := basicInfo["no_of_outlets"].(float64); ok && u > 0 {
			unitsCount = int(u)
		}
	}

	// Space requirement
	minSpace := getFloatValue(overview, "space_min_sqft")
	maxSpace := getFloatValue(overview, "space_max_sqft")
	if minSpace > 0 || maxSpace > 0 {
		result["space_requirement"] = map[string]interface{}{
			"min":  minSpace,
			"max":  maxSpace,
			"unit": "sq. ft.",
		}
	}

	if minSpace == 0 && maxSpace == 0 {
		if space, ok := basicInfo["space"].(map[string]interface{}); ok {
			if v, err := strconv.ParseFloat(fmt.Sprintf("%v", space["minSpace"]), 64); err == nil {
				minSpace = v
			}
			if v, err := strconv.ParseFloat(fmt.Sprintf("%v", space["maxSpace"]), 64); err == nil {
				maxSpace = v
			}
		}
	}

	// Parent company
	if parentCompany, ok := overview["parent_company"].(string); ok && parentCompany != "" {
		result["parent_company"] = parentCompany
	}

	// Business type
	if businessType, ok := overview["business_type"].(string); ok && businessType != "" {
		result["business_type"] = businessType
	}

	// FIX: leader_name + leader_role together
	if leaderName, ok := overview["leader_name"].(string); ok && leaderName != "" {
		leaderData := map[string]interface{}{
			"name": leaderName,
		}
		if leaderRole, ok := overview["leader_role"].(string); ok && leaderRole != "" {
			leaderData["role"] = leaderRole
		}
		result["leadership"] = leaderData
	}

	// Email
	if email, ok := overview["email"].(string); ok && email != "" {
		result["email"] = email
	}

	// Franchise fee
	fee := getFloatValue(overview, "franchise_fee")
	if fee == 0 {
		fee = getFloatValue(investment, "franchise_fee")
	}
	if fee > 0 {
		result["franchise_fees"] = map[string]interface{}{
			"min":   fee,
			"max":   fee,
			"unit":  "INR",
			"notes": "",
		}
	}

	// FIX: royalty percentage
	royalty := getFloatValue(overview, "royalty_percentage")
	if royalty == 0 {
		royalty = getFloatValue(investment, "royalty_percentage")
	}
	if royalty > 0 {
		result["royalty_percentage"] = royalty
	}

	// FIX: marketing fee percentage
	marketingFee := getFloatValue(investment, "marketing_fee_percentage")
	if marketingFee > 0 {
		result["marketing_fee_percentage"] = marketingFee
	}

	// FIX: monthly turnover range
	turnoverMin := getFloatValue(overview, "monthly_turnover_min")
	turnoverMax := getFloatValue(overview, "monthly_turnover_max")
	if turnoverMin == 0 {
		turnoverMin = getFloatValue(investment, "monthly_turnover_min")
	}
	if turnoverMax == 0 {
		turnoverMax = getFloatValue(investment, "monthly_turnover_max")
	}
	if turnoverMin > 0 || turnoverMax > 0 {
		result["monthly_turnover"] = map[string]interface{}{
			"min":  turnoverMin,
			"max":  turnoverMax,
			"unit": "INR",
		}
	}

	// Headquarters
	if hq, ok := overview["headquarters"].(string); ok && hq != "" {
		result["headquarters"] = hq
	} else if city, ok := overview["city"].(string); ok && city != "" {
		result["headquarters"] = city
	}

	return result
}

func (h *Handler) buildBusinessOverviewStructure(business, operations map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}

	if products, ok := business["products"].([]interface{}); ok && len(products) > 0 {
		result["products"] = products
	} else {
		result["products"] = []string{}
	}

	if services, ok := business["services"].([]interface{}); ok && len(services) > 0 {
		result["services"] = services
	} else {
		result["services"] = []string{}
	}

	trainingProvided := false
	if tp, ok := operations["training_provided"].(bool); ok {
		trainingProvided = tp
	}

	result["training_and_support"] = map[string]interface{}{
		"ongoing_support":             trainingProvided,
		"ongoing_support_notes":       "",
		"marketing_support":           trainingProvided,
		"marketing_support_notes":     "",
		"on_the_job_training":         trainingProvided,
		"on_the_job_training_notes":   "",
		"classroom_training":          false,
		"classroom_training_notes":    "",
		"field_assistance":            trainingProvided,
		"field_assistance_notes":      "",
		"franchisee_training_program": trainingProvided,
		"franchisee_training_notes":   "",
	}

	return result
}

func (h *Handler) buildInvestmentDetailsStructure(investment, operations map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}

	// Initial investment (in Lakhs)
	minInv := getFloatValue(investment, "initial_investment_min")
	maxInv := getFloatValue(investment, "initial_investment_max")
	if minInv > 0 || maxInv > 0 {
		result["initial_investment"] = map[string]interface{}{
			"min":   minInv / 100000,
			"max":   maxInv / 100000,
			"unit":  "Lakhs",
			"notes": "",
		}
	}

	result["investment_breakdown"] = []string{
		"Franchise Fee",
		"Equipment & Fixtures",
		"Initial Inventory",
		"Marketing & Promotion",
		"Working Capital",
	}

	// Franchise fee
	fee := getFloatValue(investment, "franchise_fee")
	if fee > 0 {
		result["franchise_fee"] = map[string]interface{}{
			"min":  fee / 100000,
			"max":  fee / 100000,
			"unit": "Lakhs",
		}
	}

	// FIX: royalty percentage
	royalty := getFloatValue(investment, "royalty_percentage")
	if royalty > 0 {
		result["royalty_percentage"] = royalty
	}

	// FIX: marketing fee percentage
	marketingFee := getFloatValue(investment, "marketing_fee_percentage")
	if marketingFee > 0 {
		result["marketing_fee_percentage"] = marketingFee
	}

	// FIX: payback period
	paybackMin := getFloatValue(investment, "payback_min_months")
	paybackMax := getFloatValue(investment, "payback_max_months")
	if paybackMin > 0 || paybackMax > 0 {
		result["payback_period"] = map[string]interface{}{
			"min":  int(paybackMin),
			"max":  int(paybackMax),
			"unit": "months",
		}
	}

	// FIX: ROI range
	roiMin := getFloatValue(investment, "roi_min_percentage")
	roiMax := getFloatValue(investment, "roi_max_percentage")
	if roiMin > 0 || roiMax > 0 {
		result["roi"] = map[string]interface{}{
			"min":  roiMin,
			"max":  roiMax,
			"unit": "%",
		}
	}

	// FIX: monthly turnover range
	turnoverMin := getFloatValue(investment, "monthly_turnover_min")
	turnoverMax := getFloatValue(investment, "monthly_turnover_max")
	if turnoverMin > 0 || turnoverMax > 0 {
		result["monthly_turnover"] = map[string]interface{}{
			"min":  turnoverMin,
			"max":  turnoverMax,
			"unit": "INR",
		}
	}

	result["required_property_location"] = []string{"Commercial", "High Street", "Mall"}

	// Floor area from operations
	minSpace := getFloatValue(operations, "space_min_sqft")
	maxSpace := getFloatValue(operations, "space_max_sqft")
	if minSpace > 0 || maxSpace > 0 {
		result["floor_area"] = map[string]interface{}{
			"min":  minSpace,
			"max":  maxSpace,
			"unit": "sq. ft.",
		}
	}

	return result
}

func (h *Handler) buildOperationStructure(operations map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}

	if propType, ok := operations["required_property_type"].(string); ok && propType != "" {
		result["required_property"] = propType
	} else {
		result["required_property"] = "Commercial"
	}

	result["sector"] = "Retail"
	result["service"] = []string{}

	if qual, ok := operations["qualification_required"].(string); ok && qual != "" {
		result["qualification_required"] = qual
	} else {
		result["qualification_required"] = "None specified"
	}

	result["is_absentee_ownership_allowed"] = "Negotiable"
	result["can_be_run_from_home_or_mobile"] = "No"

	minStaff := getFloatValue(operations, "staff_required_min")
	maxStaff := getFloatValue(operations, "staff_required_max")
	if minStaff > 0 || maxStaff > 0 {
		result["staff_required"] = map[string]interface{}{
			"min": int(minStaff),
			"max": int(maxStaff),
		}
	}

	return result
}

func getFloatValue(data map[string]interface{}, key string) float64 {
	if val, ok := data[key].(float64); ok {
		return val
	}
	if val, ok := data[key].(int); ok {
		return float64(val)
	}
	return 0
}

// ===== SEARCH PAGE BUILDER =====
func (h *Handler) buildSearchResponse(data map[string]interface{}) map[string]interface{} {
	response := map[string]interface{}{
		"success": true,
		"data":    map[string]interface{}{},
		"metadata": map[string]interface{}{
			"generatedAt": time.Now().UTC().Format(time.RFC3339),
			"source":      "workflow",
			"pageType":    "search",
		},
	}

	h.logger.Info("Building search response", map[string]interface{}{
		"dataKeys":   h.getKeys(data),
		"hasDataKey": data["data"] != nil,
	})

	franchises := h.extractArray(data, "franchises")
	if len(franchises) > 0 {
		for _, f := range franchises {
			franchise, ok := f.(map[string]interface{})
			if !ok {
				continue
			}

			space, ok := franchise["space"].(map[string]interface{})
			if !ok {
				continue
			}

			space["spaceUnit"] = "sq ft"
		}

		response["data"].(map[string]interface{})["franchises"] = franchises
	}

	if totalVal := h.extractValue(data, "total"); totalVal != nil {
		if total, ok := totalVal.(float64); ok {
			response["data"].(map[string]interface{})["total"] = int(total)
		}
	}

	if pageVal := h.extractValue(data, "page"); pageVal != nil {
		if page, ok := pageVal.(float64); ok {
			response["data"].(map[string]interface{})["page"] = int(page)
		}
	}
	if limitVal := h.extractValue(data, "limit"); limitVal != nil {
		if limit, ok := limitVal.(float64); ok {
			response["data"].(map[string]interface{})["limit"] = int(limit)
		}
	}

	filters := h.extractMap(data, "appliedFilters")
	if len(filters) > 0 {
		response["data"].(map[string]interface{})["filters_applied"] = filters
	}

	h.logger.Info("Built search response", map[string]interface{}{
		"hasFranchises": len(franchises) > 0,
		"total":         response["data"].(map[string]interface{})["total"],
	})

	return response
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	cmd, err := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromObject(output)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to create complete job command",
			map[string]interface{}{
				"error":   err,
				"traceId": span.SpanContext().TraceID().String(),
			})
		return
	}
	_, err = cmd.Send(ctx)
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		h.logger.Error("failed to send complete job command",
			map[string]interface{}{
				"error":   err,
				"traceId": span.SpanContext().TraceID().String(),
			})
	}
}
