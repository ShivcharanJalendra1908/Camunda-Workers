package buildresponse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
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
		if rid, ok := jobVars["requestId"].(string); ok && rid != "" {
			ctx = context.WithValue(ctx, "requestId", rid)
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

	if reqID, ok := ctx.Value("requestId").(string); ok && reqID != "" {
		span.SetAttributes(attribute.String("http.request_id", reqID))
	}

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

	if input.TotalCount > 0 {
		combinedData["totalCount"] = input.TotalCount
	}
	if input.Page > 0 {
		combinedData["page"] = input.Page
	}
	if input.PageSize > 0 {
		combinedData["pageSize"] = input.PageSize
	}

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

	if input.EntityType != "" {
		et := strings.ToLower(input.EntityType)
		switch et {
		case "franchises":
			et = "franchise"
		case "associations":
			et = "association"
		case "master-franchise", "master_franchises", "master franchises", "masterfranchise":
			et = "master_franchise"
		default:
			et = strings.TrimSuffix(et, "s")
			if et == "master-franchise" || et == "master franchise" || et == "masterfranchise" {
				et = "master_franchise"
			}
		}
		combinedData["entityType"] = et
	}

	// Sanitize and build response
	combinedData = h.sanitizer.SanitizeInput(combinedData)

	var response map[string]interface{}
	switch input.PageType {
	case "home":
		response = h.buildHomeResponse(combinedData)
	case "listing":
		if combinedData["entityType"] == "association" {
			response = h.buildAssociationListingResponse(combinedData)
		} else {
			response = h.buildFranchiseListingResponse(combinedData)
		}
	case "detail":
		response = h.buildDetailResponse(combinedData)
	case "search":
		response = h.buildSearchResponse(combinedData)
	case "industries":
		response = h.buildIndustriesResponse(combinedData)
	default:
		if input.PageType == "" {
			return nil, fmt.Errorf("pageType is required")
		}
		return nil, fmt.Errorf("unknown page type: %s", input.PageType)
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
	entityType := "franchise"
	if et, ok := data["entityType"].(string); ok && et != "" {
		entityType = et
	}

	if entityType == "association" {
		return h.buildAssociationHomeResponse(data)
	}

	sections := []interface{}{}

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
			desc := ""
			if sd, ok := listing["short_description"].(string); ok && sd != "" {
				desc = sd
			} else if d, ok := listing["description"].(string); ok {
				desc = d
			}
			transformed["description"] = desc
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
		sections = []interface{}{}
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
			"version":     h.config.AppVersion,
		},
	}
}

// ===== ASSOCIATION HOME PAGE BUILDER =====
func (h *Handler) buildAssociationHomeResponse(data map[string]interface{}) map[string]interface{} {
	sections := []interface{}{}

	h.logger.Info("Building association home response", map[string]interface{}{
		"dataKeys":   h.getKeys(data),
		"hasDataKey": data["data"] != nil,
	})

	// 1. explore_by_industry
	industries := h.extractArray(data, "industries")
	if len(industries) == 0 {
		industries = h.extractArray(data, "categories")
	}

	mappedIndustries := []map[string]interface{}{}
	for _, indItem := range industries {
		ind, ok := indItem.(map[string]interface{})
		if !ok {
			continue
		}

		idVal := ""
		if id, ok := ind["id"].(string); ok {
			idVal = id
		} else if idFloat, ok := ind["id"].(float64); ok {
			idVal = strconv.Itoa(int(idFloat))
		}

		nameVal := ""
		if name, ok := ind["name"].(string); ok {
			nameVal = name
		} else if name, ok := ind["industry_name"].(string); ok {
			nameVal = name
		}

		slugVal := ""
		if slug, ok := ind["slug"].(string); ok {
			slugVal = slug
		} else if slug, ok := ind["industry_slug"].(string); ok {
			slugVal = slug
		}

		iconVal := ""
		if icon, ok := ind["icon_url"].(string); ok {
			iconVal = icon
		} else if icon, ok := ind["image_url"].(string); ok {
			iconVal = icon
		}

		mappedIndustries = append(mappedIndustries, map[string]interface{}{
			"id":       idVal,
			"name":     nameVal,
			"slug":     slugVal,
			"icon_url": iconVal,
		})
	}

	if len(mappedIndustries) > 0 {
		sections = append(sections, map[string]interface{}{
			"type":    "explore_by_industry",
			"enabled": true,
			"data": map[string]interface{}{
				"industries": mappedIndustries,
			},
		})
	}

	// 2. featured_business_associations
	listings := h.extractArray(data, "popularListings")
	transformedAssociations := []interface{}{}
	for _, item := range listings {
		assoc, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		transformed := map[string]interface{}{}

		// id
		if id, ok := assoc["id"].(string); ok {
			transformed["id"] = id
		} else if fid, ok := assoc["franchise_id"].(string); ok {
			transformed["id"] = fid
		}

		// name
		if name, ok := assoc["name"].(string); ok {
			transformed["association_name"] = name
		} else if brand, ok := assoc["brand"].(string); ok {
			transformed["association_name"] = brand
		}

		// description
		desc := ""
		if sd, ok := assoc["short_description"].(string); ok && sd != "" {
			desc = sd
		} else if d, ok := assoc["description"].(string); ok {
			desc = d
		}
		transformed["description"] = desc

		// association_metadata
		assocMeta := h.extractMap(assoc, "association_metadata")
		if len(assocMeta) == 0 {
			if metadata, ok := assoc["association_metadata"].(map[string]interface{}); ok {
				assocMeta = metadata
			}
		}

		// association_type
		assocType := "Industry Body"
		if at, ok := assocMeta["association_type"].(string); ok && at != "" {
			assocType = at
		} else if at, ok := assoc["association_type"].(string); ok && at != "" {
			assocType = at
		}
		transformed["association_type"] = assocType

		// membership fee range
		minFee := getFloatValue(assoc, "membership_fee_min")
		maxFee := getFloatValue(assoc, "membership_fee_max")

		if minFee == 0 && maxFee == 0 {
			if memDetails, ok := assocMeta["membership_details"].(map[string]interface{}); ok {
				if feeMin, ok := memDetails["membership_fee_min"].(float64); ok {
					minFee = feeMin
				}
				if feeMax, ok := memDetails["membership_fee_max"].(float64); ok {
					maxFee = feeMax
				}
			}
		}

		feeUnit := "INR"
		transformed["MembershipFeeRange"] = map[string]interface{}{
			"FeeUnit": feeUnit,
			"minFee":  interface{}(nil),
			"maxFee":  interface{}(nil),
		}
		if minFee > 0 {
			transformed["MembershipFeeRange"].(map[string]interface{})["minFee"] = minFee
		}
		if maxFee > 0 {
			transformed["MembershipFeeRange"].(map[string]interface{})["maxFee"] = maxFee
		}

		// location
		loc := ""
		if l, ok := assoc["location"].(string); ok {
			loc = l
		} else if city, ok := assoc["city"].(string); ok {
			loc = city + ",India"
		}
		transformed["location"] = loc

		// logo
		logoUrl := ""
		if logo, ok := assoc["logo"].(map[string]interface{}); ok {
			if sq, ok := logo["square"].(string); ok && sq != "" {
				logoUrl = sq
			} else if cir, ok := logo["circle"].(string); ok && cir != "" {
				logoUrl = cir
			} else if urlVal, ok := logo["url"].(string); ok {
				logoUrl = urlVal
			}
		} else if logoStr, ok := assoc["logo_url_square"].(string); ok && logoStr != "" {
			logoUrl = logoStr
		} else if logoStr, ok := assoc["logo_url_circle"].(string); ok && logoStr != "" {
			logoUrl = logoStr
		}

		assocName, _ := transformed["association_name"].(string)
		transformed["logo"] = map[string]interface{}{
			"alt": assocName,
			"url": logoUrl,
		}

		// no_of_members
		noOfMembers := interface{}(nil)
		if mcVal := getFloatValue(assoc, "member_count"); mcVal > 0 {
			noOfMembers = int(mcVal)
		}
		transformed["no_of_members"] = noOfMembers

		// tags
		tagsList := []interface{}{}
		if overviewVal, ok := assocMeta["overview"].(map[string]interface{}); ok {
			if kf, ok := overviewVal["key_functions"].([]interface{}); ok {
				tagsList = kf
			}
		}
		if len(tagsList) == 0 {
			if tags, ok := assoc["tags"].([]interface{}); ok {
				tagsList = tags
			}
		}
		transformed["tags"] = tagsList

		// year_of_establishment
		estYear := ""
		if fyVal := getFloatValue(assoc, "year_of_establishment"); fyVal > 0 {
			estYear = strconv.Itoa(int(fyVal))
		} else if fyVal := getFloatValue(assoc, "founded_year"); fyVal > 0 {
			estYear = strconv.Itoa(int(fyVal))
		}
		transformed["year_of_establishment"] = estYear

		transformedAssociations = append(transformedAssociations, transformed)
	}

	sections = append(sections, map[string]interface{}{
		"type":    "featured_business_associations",
		"enabled": true,
		"data":    transformedAssociations,
	})

	// why_choose_lemici (Cities) and statistics sections are handled by the frontend.
	return map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"pageId":   "association_home",
			"sections": sections,
		},
		"metadata": map[string]interface{}{
			"generatedAt": time.Now().UTC().Format(time.RFC3339),
			"source":      "workflow",
			"pageType":    "home",
			"version":     h.config.AppVersion,
		},
	}
}

// ===== ASSOCIATION DETAIL PAGE BUILDER =====
func (h *Handler) buildAssociationDetailResponse(data map[string]interface{}) map[string]interface{} {
	sections := []interface{}{}

	basicInfo := h.extractMap(data, "basicInfo")
	recommended := h.extractArray(data, "recommended")
	categories := h.extractArray(data, "categories")
	marketInsights := h.extractArray(data, "marketInsights")
	categoryQuestions := h.extractArray(data, "categoryQuestions")

	// Parse association_metadata JSON column safely
	metadataVal := basicInfo["association_metadata"]
	var metadata map[string]interface{}
	if m, ok := metadataVal.(map[string]interface{}); ok {
		metadata = m
	} else if s, ok := metadataVal.(string); ok && s != "" && s != "null" {
		_ = json.Unmarshal([]byte(s), &metadata)
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}

	overview := getMapVal(metadata, "overview")
	membership_details := getMapVal(metadata, "membership_details")
	services_offered := getArrayVal(metadata, "services_offered")
	programs := getMapVal(metadata, "programs")
	publications := getMapVal(metadata, "publications")
	regional_structure := getMapVal(metadata, "regional_structure")
	events := getMapVal(metadata, "events")
	compliance_policies := getMapVal(metadata, "compliance_policies")
	partnerships := getMapVal(metadata, "partnerships")
	awards := getMapVal(metadata, "awards")
	digital_presence := getArrayVal(metadata, "digital_presence")
	transparency := getArrayVal(metadata, "transparency")
	governance := getMapVal(metadata, "governance")
	data_and_insights := getMapVal(metadata, "data_and_insights")
	contact_details := getMapVal(metadata, "contact_details")

	// 1. association_hero_info_card
	name := getStringVal(basicInfo, "name", "")
	if name == "" {
		name = getStringVal(basicInfo, "brand", "KASSIA")
	}
	slug := getStringVal(basicInfo, "slug", "")
	description := getStringVal(basicInfo, "description", "")

	logoURL := ""
	if logoMap, ok := basicInfo["logo"].(map[string]interface{}); ok {
		logoURL = getStringVal(logoMap, "circle", "")
		if logoURL == "" {
			logoURL = getStringVal(logoMap, "square", "")
		}
	}
	if logoURL == "" {
		logoURL = getStringVal(basicInfo, "logo_url", "/AssociationImages/FeaturedAssociations/kassia.svg")
	}

	tags := getArrayVal(overview, "key_functions")
	var transformedTags []interface{}
	for _, t := range tags {
		transformedTags = append(transformedTags, t)
	}

	socialLinksVal := getMapVal(contact_details, "social_links")
	socialLinks := map[string]interface{}{
		"youtube":   getStringVal(socialLinksVal, "youtube", ""),
		"facebook":  getStringVal(socialLinksVal, "facebook", ""),
		"instagram": getStringVal(socialLinksVal, "instagram", ""),
		"twitter":   getStringVal(socialLinksVal, "twitter", ""),
		"linkedin":  getStringVal(socialLinksVal, "linkedin", ""),
	}

	heroData := map[string]interface{}{
		"name":         name,
		"slug":         slug,
		"logo":         logoURL,
		"description":  description,
		"likes":        getStringVal(basicInfo, "likes_count", ""),
		"rating":       getStringVal(basicInfo, "rating", ""),
		"review_count": getStringVal(basicInfo, "rating_count", ""),
		"tags":         transformedTags,
		"socialLinks":  socialLinks,
	}

	sections = append(sections, map[string]interface{}{
		"type":    "association_hero_info_card",
		"enabled": true,
		"data":    heroData,
	})

	// 2. association_info_grid
	sectorVal := getStringVal(overview, "sector", getStringVal(overview, "sector_represented", ""))
	if sectorVal == "" {
		if industryMap, ok := basicInfo["industry"].(map[string]interface{}); ok {
			sectorVal = getStringVal(industryMap, "name", "")
		}
	}

	websiteVal := getStringVal(contact_details, "website", "")
	if websiteVal == "" {
		websiteVal = getStringVal(basicInfo, "website_url", "")
	}
	if websiteVal == "" {
		websiteVal = ""
	}

	phoneVal := getStringVal(contact_details, "phone_number", "")
	if phoneVal == "" {
		phoneVal = getStringVal(overview, "contact_phone", "")
	}
	if phoneVal == "" {
		phoneVal = getStringVal(overview, "email", "")
	}

	infoGridData := map[string]interface{}{
		"association_name":      name,
		"association_type":      getStringVal(metadata, "association_type", getStringVal(overview, "association_type", "")),
		"sector":                sectorVal,
		"year_of_establishment": getStringVal(basicInfo, "year_of_establishment", getStringVal(basicInfo, "founded_year", getStringVal(overview, "founded_year", ""))),
		"legal_status":          getStringVal(metadata, "legal_status", getStringVal(overview, "legal_status", "")),
		"headquarters":          getStringVal(contact_details, "office_address", getStringVal(overview, "headquarters_address", "")),
		"regional_presence":     getStringVal(regional_structure, "regional_offices", getStringVal(overview, "regional_presence", "")),
		"website":               websiteVal,
		"contact_details":       phoneVal,
	}

	sections = append(sections, map[string]interface{}{
		"type":    "association_info_grid",
		"enabled": true,
		"data":    infoGridData,
	})

	// 3. membership_section
	membershipList := getArrayVal(membership_details, "categories")
	var transformedMembership []interface{}
	if len(membershipList) > 0 {
		for _, mItem := range membershipList {
			if mm, ok := mItem.(map[string]interface{}); ok {
				transformedMembership = append(transformedMembership, map[string]interface{}{
					"detail":      getStringVal(mm, "name", ""),
					"description": getStringVal(mm, "description", ""),
				})
			}
		}
	} // end transformedMembership — no hardcoded fallback; empty if no DB data

	eligibilityList := getArrayVal(membership_details, "requirements")
	var transformedEligibility []interface{}
	if len(eligibilityList) > 0 {
		for _, eItem := range eligibilityList {
			if em, ok := eItem.(map[string]interface{}); ok {
				transformedEligibility = append(transformedEligibility, map[string]interface{}{
					"detail":      getStringVal(em, "name", ""),
					"description": getStringVal(em, "description", ""),
				})
			}
		}
	} // end transformedEligibility

	applicationVal := getMapVal(membership_details, "application_process")
	var transformedApplication []interface{}
	if len(applicationVal) > 0 {
		for k, v := range applicationVal {
			transformedApplication = append(transformedApplication, map[string]interface{}{
				"detail":      k,
				"description": fmt.Sprintf("%v", v),
			})
		}
	} // end transformedApplication

	if transformedMembership == nil {
		transformedMembership = []interface{}{}
	}
	if transformedEligibility == nil {
		transformedEligibility = []interface{}{}
	}
	if transformedApplication == nil {
		transformedApplication = []interface{}{}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "membership_section",
		"enabled": true,
		"data": map[string]interface{}{
			"Membership":             transformedMembership,
			"Eligibility_Criteria":   transformedEligibility,
			"Membership_Application": transformedApplication,
		},
	})

	// 4. services_and_institutional_offerings
	var transformedServices []interface{}
	if len(services_offered) > 0 {
		for _, sItem := range services_offered {
			if sm, ok := sItem.(map[string]interface{}); ok {
				transformedServices = append(transformedServices, map[string]interface{}{
					"title":       getStringVal(sm, "name", ""),
					"description": getStringVal(sm, "description", ""),
				})
			}
		}
	} // end transformedServices

	if transformedServices == nil {
		transformedServices = []interface{}{}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "services_and_institutional_offerings",
		"enabled": true,
		"data":    transformedServices,
	})

	// 5. programs_and_initiatives_section
	var transformedPrograms map[string]interface{}
	if len(programs) > 0 {
		transformedPrograms = map[string]interface{}{}
		for cat, pVal := range programs {
			if pArr, ok := pVal.([]interface{}); ok {
				var catList []interface{}
				for _, pItem := range pArr {
					if pm, ok := pItem.(map[string]interface{}); ok {
						catList = append(catList, map[string]interface{}{
							"title":       getStringVal(pm, "title", ""),
							"description": getStringVal(pm, "description", ""),
						})
					}
				}
				if len(catList) > 0 {
					transformedPrograms[cat] = catList
				}
			}
		}
		if len(transformedPrograms) == 0 {
			transformedPrograms = nil
		}
	} // end transformedPrograms

	if transformedPrograms == nil {
		transformedPrograms = map[string]interface{}{}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "programs_and_initiatives_section",
		"enabled": true,
		"data":    transformedPrograms,
	})

	// 6. publications_section
	var transformedPublications map[string]interface{}
	if len(publications) > 0 {
		transformedPublications = map[string]interface{}{}
		for cat, pVal := range publications {
			if pArr, ok := pVal.([]interface{}); ok {
				var catList []interface{}
				for _, pItem := range pArr {
					if pm, ok := pItem.(map[string]interface{}); ok {
						catList = append(catList, map[string]interface{}{
							"title":       getStringVal(pm, "title", ""),
							"description": getStringVal(pm, "description", ""),
						})
					}
				}
				if len(catList) > 0 {
					transformedPublications[cat] = catList
				}
			}
		}
		if len(transformedPublications) == 0 {
			transformedPublications = nil
		}
	} // end transformedPublications

	if transformedPublications == nil {
		transformedPublications = map[string]interface{}{}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "publications_section",
		"enabled": true,
		"data":    transformedPublications,
	})

	// 7. regional_structure_section
	chaptersList := getArrayVal(regional_structure, "chapters")
	var transformedChapters []interface{}
	if len(chaptersList) > 0 {
		for _, cItem := range chaptersList {
			if cm, ok := cItem.(map[string]interface{}); ok {
				transformedChapters = append(transformedChapters, map[string]interface{}{
					"name":                 getStringVal(cm, "name", ""),
					"headquarters_address": getStringVal(cm, "headquarters_address", ""),
					"contact_email":        getStringVal(cm, "contact_email", ""),
				})
			}
		}
	} // end transformedChapters

	if transformedChapters == nil {
		transformedChapters = []interface{}{}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "regional_structure_section",
		"enabled": true,
		"data": map[string]interface{}{
			"governance_model": getStringVal(regional_structure, "governance_model", ""),
			"headquarters":     getStringVal(regional_structure, "headquarters", ""),
			"chapters":         transformedChapters,
		},
	})

	// 8. events_engagement_section
	var transformedEvents map[string]interface{}
	if len(events) > 0 {
		transformedEvents = map[string]interface{}{}
		for cat, eVal := range events {
			if eArr, ok := eVal.([]interface{}); ok {
				var catList []interface{}
				for _, eItem := range eArr {
					if em, ok := eItem.(map[string]interface{}); ok {
						catList = append(catList, map[string]interface{}{
							"title":       getStringVal(em, "title", ""),
							"description": getStringVal(em, "description", ""),
						})
					}
				}
				if len(catList) > 0 {
					transformedEvents[cat] = catList
				}
			}
		}
		if len(transformedEvents) == 0 {
			transformedEvents = nil
		}
	} // end transformedEvents

	if transformedEvents == nil {
		transformedEvents = map[string]interface{}{}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "events_engagement_section",
		"enabled": true,
		"data":    transformedEvents,
	})

	// 9. compliance_policy_section
	var transformedPolicies map[string]interface{}
	if len(compliance_policies) > 0 {
		transformedPolicies = map[string]interface{}{}
		for cat, pVal := range compliance_policies {
			if pArr, ok := pVal.([]interface{}); ok {
				var catList []interface{}
				for _, pItem := range pArr {
					if pm, ok := pItem.(map[string]interface{}); ok {
						catList = append(catList, map[string]interface{}{
							"title":       getStringVal(pm, "title", ""),
							"description": getStringVal(pm, "description", ""),
						})
					}
				}
				if len(catList) > 0 {
					transformedPolicies[cat] = catList
				}
			}
		}
		if len(transformedPolicies) == 0 {
			transformedPolicies = nil
		}
	} // end transformedPolicies

	if transformedPolicies == nil {
		transformedPolicies = map[string]interface{}{}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "compliance_policy_section",
		"enabled": true,
		"data":    transformedPolicies,
	})

	// 10. partnership_affiliations_section
	var transformedPartners []interface{}
	partnerList := getArrayVal(partnerships, "list")
	if len(partnerList) > 0 {
		for _, pItem := range partnerList {
			if pm, ok := pItem.(map[string]interface{}); ok {
				transformedPartners = append(transformedPartners, map[string]interface{}{
					"partner_name": getStringVal(pm, "name", ""),
					"relation":     getStringVal(pm, "relation", ""),
					"website":      getStringVal(pm, "website", ""),
				})
			}
		}
	} // end transformedPartners

	if transformedPartners == nil {
		transformedPartners = []interface{}{}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "partnership_affiliations_section",
		"enabled": true,
		"data":    transformedPartners,
	})

	// 11. awards_recognition_section
	var transformedAwards []interface{}
	awardList := getArrayVal(awards, "list")
	if len(awardList) > 0 {
		for _, aItem := range awardList {
			if am, ok := aItem.(map[string]interface{}); ok {
				transformedAwards = append(transformedAwards, map[string]interface{}{
					"award_title": getStringVal(am, "title", ""),
					"description": getStringVal(am, "description", ""),
				})
			}
		}
	} // end transformedAwards

	if transformedAwards == nil {
		transformedAwards = []interface{}{}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "awards_recognition_section",
		"enabled": true,
		"data":    transformedAwards,
	})

	// 12. digital_presence_section
	var transformedDigital []interface{}
	if len(digital_presence) > 0 {
		for _, dItem := range digital_presence {
			if dm, ok := dItem.(map[string]interface{}); ok {
				transformedDigital = append(transformedDigital, map[string]interface{}{
					"category":      getStringVal(dm, "category", ""),
					"property_name": getStringVal(dm, "property_name", ""),
					"status":        getStringVal(dm, "status", ""),
					"description":   getStringVal(dm, "description", ""),
				})
			}
		}
	} // end transformedDigital

	if transformedDigital == nil {
		transformedDigital = []interface{}{}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "digital_presence_section",
		"enabled": true,
		"data":    transformedDigital,
	})

	// 13. transparency_verification_section
	var transformedTransparency []interface{}
	if len(transparency) > 0 {
		for _, tItem := range transparency {
			if tm, ok := tItem.(map[string]interface{}); ok {
				transformedTransparency = append(transformedTransparency, map[string]interface{}{
					"title":       getStringVal(tm, "title", ""),
					"description": getStringVal(tm, "description", ""),
					"status":      getStringVal(tm, "status", ""),
				})
			}
		}
	} // end transformedTransparency

	if transformedTransparency == nil {
		transformedTransparency = []interface{}{}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "transparency_verification_section",
		"enabled": true,
		"data":    transformedTransparency,
	})

	// 14. members_structure_tree — only shown when governance data exists in DB
	if governance == nil {
		governance = map[string]interface{}{}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "members_structure_tree",
		"enabled": true,
		"data":    governance,
	})

	// 15. recommended_business_associations
	var transformedRecs []interface{}
	if len(recommended) > 0 {
		for _, item := range recommended {
			if rMap, ok := item.(map[string]interface{}); ok {
				transformedRecs = append(transformedRecs, map[string]interface{}{
					"id":       getStringVal(rMap, "id", ""),
					"name":     getStringVal(rMap, "brand", getStringVal(rMap, "name", "")),
					"slug":     getStringVal(rMap, "slug", ""),
					"icon_url": getStringVal(rMap, "logo_url", "/AssociationImages/FeaturedAssociations/ficci.svg"),
				})
			}
		}
	} // end transformedRecs

	sections = append(sections, map[string]interface{}{
		"type":    "recommended_business_associations",
		"enabled": true,
		"data":    transformedRecs,
	})

	// 16. market_insights_section — use real data only; skip if nothing available
	// Priority 1: data_and_insights from association_metadata (DB)
	// Priority 2: growth_rate + market_trend from ES marketInsights
	var insightsData map[string]interface{}

	if len(data_and_insights) > 0 {
		trends := getArrayVal(data_and_insights, "sector_trends")
		var trendList []interface{}
		for _, t := range trends {
			trendList = append(trendList, t)
		}
		insightsData = map[string]interface{}{}
		if v := getStringVal(data_and_insights, "market_stats", ""); v != "" {
			insightsData["market_stats"] = v
		}
		if len(trendList) > 0 {
			insightsData["sector_trends"] = trendList
		}
		if v := getStringVal(data_and_insights, "exim_data", ""); v != "" {
			insightsData["exim_data"] = v
		}
		if v := getStringVal(data_and_insights, "cluster_info", ""); v != "" {
			insightsData["cluster_info"] = v
		}
		if len(insightsData) == 0 {
			insightsData = nil
		}
	}

	if insightsData == nil && len(marketInsights) > 0 {
		if insights, ok := marketInsights[0].(map[string]interface{}); ok {
			insightsData = map[string]interface{}{}
			if gr, ok := insights["growth_rate"].(map[string]interface{}); ok {
				insightsData["growth_rate"] = gr
			}
			if mt, ok := insights["market_trend"].(map[string]interface{}); ok {
				insightsData["market_trend"] = mt
			}
			if len(insightsData) == 0 {
				insightsData = nil
			}
		}
	}

	if insightsData == nil {
		insightsData = map[string]interface{}{}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "market_insights_section",
		"enabled": true,
		"data":    insightsData,
	})

	// 17. featured_business_categories
	var transformedDetailCategories []interface{}
	if len(categories) > 0 {
		for _, item := range categories {
			if cMap, ok := item.(map[string]interface{}); ok {
				transformedDetailCategories = append(transformedDetailCategories, map[string]interface{}{
					"id":       getStringVal(cMap, "id", ""),
					"name":     getStringVal(cMap, "name", ""),
					"slug":     getStringVal(cMap, "slug", ""),
					"icon_url": getStringVal(cMap, "icon_url", "/AssociationImages/FeaturedBusinessCategories/technology.svg"),
				})
			}
		}
	} else {
		transformedDetailCategories = []interface{}{
			map[string]interface{}{"id": "1", "name": "Technology", "slug": "technology", "icon_url": "/AssociationImages/FeaturedBusinessCategories/technology.svg"},
			map[string]interface{}{"id": "2", "name": "Finance", "slug": "finance", "icon_url": "/AssociationImages/FeaturedBusinessCategories/finance.svg"},
			map[string]interface{}{"id": "3", "name": "Healthcare", "slug": "healthcare", "icon_url": "/AssociationImages/FeaturedBusinessCategories/healthcare.svg"},
			map[string]interface{}{"id": "4", "name": "Manufacturing", "slug": "manufacturing", "icon_url": "/AssociationImages/FeaturedBusinessCategories/manufacturing.svg"},
			map[string]interface{}{"id": "5", "name": "Education", "slug": "education", "icon_url": "/AssociationImages/FeaturedBusinessCategories/education.svg"},
			map[string]interface{}{"id": "6", "name": "Real Estate", "slug": "real-estate", "icon_url": "/AssociationImages/FeaturedBusinessCategories/real-estate.svg"},
			map[string]interface{}{"id": "7", "name": "Retail", "slug": "retail", "icon_url": "/AssociationImages/FeaturedBusinessCategories/retail.svg"},
			map[string]interface{}{"id": "8", "name": "Agriculture", "slug": "agriculture", "icon_url": "/AssociationImages/FeaturedBusinessCategories/agriculture.svg"},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "featured_business_categories",
		"enabled": true,
		"data":    transformedDetailCategories,
	})

	// 18. category_questions
	var detailQuestions []interface{}
	faqList := getArrayVal(metadata, "faqs")
	if len(faqList) > 0 {
		for _, fItem := range faqList {
			if fm, ok := fItem.(map[string]interface{}); ok {
				detailQuestions = append(detailQuestions, map[string]interface{}{
					"question": getStringVal(fm, "question", ""),
					"answer":   getStringVal(fm, "answer", ""),
				})
			}
		}
	}
	if len(detailQuestions) == 0 && len(categoryQuestions) > 0 {
		for _, qItem := range categoryQuestions {
			if qm, ok := qItem.(map[string]interface{}); ok {
				detailQuestions = append(detailQuestions, map[string]interface{}{
					"question": getStringVal(qm, "question", ""),
					"answer":   getStringVal(qm, "answer", ""),
				})
			}
		}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "category_questions",
		"enabled": true,
		"data": map[string]interface{}{
			"questions": detailQuestions,
		},
	})

	detailData := map[string]interface{}{
		"pageId":   "association_individual",
		"sections": sections,
	}

	if len(basicInfo) > 0 {
		if id, ok := basicInfo["id"].(string); ok && id != "" {
			detailData["association_id"] = id
		}
		if name, ok := basicInfo["name"].(string); ok && name != "" {
			detailData["association_name"] = name
		}
		// slug is already inside hero section data, do not duplicate at top level
		if status, ok := basicInfo["status"].(string); ok && status != "" {
			detailData["status"] = status
		}
		if createdBy, ok := basicInfo["created_by"].(string); ok && createdBy != "" {
			detailData["created_by"] = createdBy
		}
	}

	return map[string]interface{}{
		"success": true,
		"data":    detailData,
		"metadata": map[string]interface{}{
			"generatedAt": time.Now().UTC().Format(time.RFC3339),
			"source":      "workflow",
			"pageType":    "detail",
			"version":     h.config.AppVersion,
		},
	}
}
func getStringVal(m map[string]interface{}, key string, fallback string) string {
	if m == nil {
		return fallback
	}
	if val, ok := m[key]; ok && val != nil {
		if s, ok := val.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", val)
	}
	return fallback
}

func getMapVal(m map[string]interface{}, key string) map[string]interface{} {
	if m == nil {
		return nil
	}
	if val, ok := m[key]; ok && val != nil {
		if mm, ok := val.(map[string]interface{}); ok {
			return mm
		}
	}
	return nil
}

func getArrayVal(m map[string]interface{}, key string) []interface{} {
	if m == nil {
		return nil
	}
	if val, ok := m[key]; ok && val != nil {
		if arr, ok := val.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

// ===== LISTING PAGE BUILDER =====
type TransformedFranchiseListing struct {
	ID                  string                 `json:"id"`
	EntityType          string                 `json:"entity_type"`
	Brand               string                 `json:"brand"`
	Category            string                 `json:"category,omitempty"`
	Color               string                 `json:"color,omitempty"`
	ShortDescription    string                 `json:"short_description"`
	YearOfEstablishment interface{}            `json:"year_of_establishment,omitempty"`
	FoundedYear         interface{}            `json:"founded_year,omitempty"`
	Rating              interface{}            `json:"rating,omitempty"`
	Location            interface{}            `json:"location,omitempty"`
	NoOfOutlets         interface{}            `json:"no_of_outlets,omitempty"`
	Space               map[string]interface{} `json:"space,omitempty"`
	InvestmentRange     map[string]interface{} `json:"investmentRange,omitempty"`
	Tags                interface{}            `json:"tags,omitempty"`
	Slug                interface{}            `json:"slug,omitempty"`
	Status              interface{}            `json:"status,omitempty"`
	Logo                map[string]interface{} `json:"logo,omitempty"`
}

func (h *Handler) buildFranchiseListingResponse(data map[string]interface{}) map[string]interface{} {
	entityType := "franchise"
	if et, ok := data["entityType"].(string); ok && et != "" {
		entityType = et
	}

	sections := []interface{}{}

	industryInfo := h.extractMap(data, "industryInfo")

	franchises := h.extractArray(data, "franchiseListings")
	if len(franchises) == 0 {
		if fl, ok := data["franchiseListings"].(map[string]interface{}); ok {
			if hits, ok := fl["hits"].([]interface{}); ok {
				franchises = hits
			}
		}
	}
	if len(franchises) == 0 {
		franchises = h.extractArray(data, "franchises")
		if len(franchises) == 0 {
			if fl, ok := data["franchises"].(map[string]interface{}); ok {
				if hits, ok := fl["hits"].([]interface{}); ok {
					franchises = hits
				}
			}
		}
	}

	categoriesRaw := h.extractArray(data, "featuredCategories")
	if len(categoriesRaw) == 0 {
		categoriesRaw = h.extractArray(data, "categories")
	}
	categories := make([]interface{}, 0, len(categoriesRaw))
	for _, c := range categoriesRaw {
		if cMap, ok := c.(map[string]interface{}); ok {
			delete(cMap, "franchise_count")
			categories = append(categories, cMap)
		} else {
			categories = append(categories, c)
		}
	}

	categoryQuestions := h.extractArray(data, "understandingCategory")
	if len(categoryQuestions) == 0 {
		categoryQuestions = h.extractArray(data, "categoryQuestions")
	}

	recommended := h.extractArray(data, "recommendedFranchises")
	if len(recommended) == 0 {
		recommended = h.extractArray(data, "recommended")
	}

	marketInsights := h.extractArray(data, "keyMarketInsights")
	if len(marketInsights) == 0 {
		marketInsights = h.extractArray(data, "marketInsights")
	}
	page := 1
	pageSize := 6
	totalCount := int64(0)

	heroTitle := "Franchise Opportunities in India"
	heroDescription := "Explore thousands of top franchise opportunities in India across various industries. Find the perfect business that matches your budget and goals."

	// Check searchParams for AI-extracted industry names (multi-industry support)
	searchParams := h.extractMap(data, "searchParams")
	multiIndustryTitle := ""
	if len(searchParams) > 0 {
		if ind, ok := searchParams["industry"].(string); ok && ind != "" {
			multiIndustryTitle = ind
		}
	}

	if len(industryInfo) > 0 {
		// Name se default title/description banao
		name := ""
		if n, ok := industryInfo["name"].(string); ok && n != "" {
			name = n
		} else if n, ok := industryInfo["industry_name"].(string); ok && n != "" {
			name = n
		}

		// If multi-industry search, combine: "Food & Beverage & Fashion"
		if multiIndustryTitle != "" && strings.Contains(multiIndustryTitle, ",") {
			// Format: "Food & Beverage, Fashion" → "Food & Beverage and Fashion"
			parts := strings.Split(multiIndustryTitle, ",")
			cleanParts := []string{}
			for _, p := range parts {
				cleaned := strings.TrimSpace(p)
				if cleaned != "" {
					cleanParts = append(cleanParts, cleaned)
				}
			}
			if len(cleanParts) > 1 {
				name = strings.Join(cleanParts[:len(cleanParts)-1], ", ") + " & " + cleanParts[len(cleanParts)-1]
			}
		}

		if name != "" {
			heroTitle = fmt.Sprintf("%s Franchises", name)
			heroDescription = fmt.Sprintf("Search results for %s franchise opportunities across India", name)
		}

		// DB mein stored hai toh override karo (single industry page pe)
		if !strings.Contains(multiIndustryTitle, ",") {
			if t, ok := industryInfo["listing_title"].(string); ok && t != "" {
				heroTitle = t
			}
			if entityType == "master_franchise" {
				if d, ok := industryInfo["master_franchise_listing_description"].(string); ok && d != "" {
					heroDescription = d
				}
			} else {
				if d, ok := industryInfo["listing_description"].(string); ok && d != "" {
					heroDescription = d
				}
			}
		}
	} else if multiIndustryTitle != "" {
		// No industryInfo from DB, but AI extracted industry name(s)
		parts := strings.Split(multiIndustryTitle, ",")
		cleanParts := []string{}
		for _, p := range parts {
			cleaned := strings.TrimSpace(p)
			if cleaned != "" {
				cleanParts = append(cleanParts, cleaned)
			}
		}
		var combined string
		if len(cleanParts) > 1 {
			combined = strings.Join(cleanParts[:len(cleanParts)-1], ", ") + " & " + cleanParts[len(cleanParts)-1]
		} else {
			combined = cleanParts[0]
		}
		heroTitle = fmt.Sprintf("%s Franchises", combined)
		heroDescription = fmt.Sprintf("Search results for %s franchise opportunities across India", combined)
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
		var transformedListings []interface{}
		for _, f := range franchises {
			listing, ok := f.(map[string]interface{})
			if !ok {
				continue
			}

			et, _ := listing["entity_type"].(string)
			if et == "" {
				et = "franchise"
			}

			switch et {
			case "franchise":
				transformed := TransformedFranchiseListing{
					EntityType: et,
				}

				// Copy common fields
				transformed.YearOfEstablishment = listing["year_of_establishment"]
				if fy, ok := listing["founded_year"]; ok {
					transformed.FoundedYear = fy // for tests
				}
				transformed.Rating = listing["rating"]
				transformed.Location = listing["location"]
				transformed.Tags = listing["tags"]
				transformed.Slug = listing["slug"]
				transformed.Status = listing["status"]

				// Only map short_description (ignore description for the listing card)
				listingDesc := ""
				if sd, ok := listing["short_description"].(string); ok && sd != "" {
					listingDesc = sd
				}
				transformed.ShortDescription = listingDesc

				// Logo
				if logo, ok := listing["logo"].(map[string]interface{}); ok {
					transformed.Logo = logo
				} else {
					transformed.Logo = map[string]interface{}{
						"circle": "",
						"square": "",
						"alt":    "",
					}
				}

				// Industry (Color and Category)
				if industry, ok := listing["industry"].(map[string]interface{}); ok {
					if color, ok := industry["color"].(string); ok && color != "" {
						transformed.Color = color
					}
					if catName, ok := industry["name"].(string); ok && catName != "" {
						transformed.Category = catName
					}
				} else if category, ok := listing["category"].(string); ok {
					transformed.Category = category
				}
				if color, ok := listing["color"].(string); ok && color != "" {
					transformed.Color = color
				}

				if outlets, ok := listing["total_outlets"]; ok {
					transformed.NoOfOutlets = outlets
				} else if outlets, ok := listing["no_of_outlets"]; ok {
					transformed.NoOfOutlets = outlets
				}

				// Only keep brand, omit name
				if name, ok := listing["name"].(string); ok {
					transformed.Brand = name
				} else if brand, ok := listing["brand"].(string); ok {
					transformed.Brand = brand
				}

				if fid, ok := listing["franchise_id"].(string); ok {
					transformed.ID = fid
				} else if id, ok := listing["id"].(string); ok {
					transformed.ID = id
				}

				if space, ok := listing["space"].(map[string]interface{}); ok {
					if _, hasUnit := space["spaceUnit"]; !hasUnit {
						space["spaceUnit"] = "sq ft"
					}
					transformed.Space = space
				}

				if invRange, ok := listing["investmentRange"].(map[string]interface{}); ok {
					if _, hasUnit := invRange["investmentUnit"]; !hasUnit {
						invRange["investmentUnit"] = "Lakhs"
					}
					transformed.InvestmentRange = invRange
				} else if inv, ok := listing["investment"].(map[string]interface{}); ok {
					invRange := map[string]interface{}{
						"minInvestment":  inv["minInvestment"],
						"maxInvestment":  inv["maxInvestment"],
						"investmentUnit": "Lakhs",
					}
					transformed.InvestmentRange = invRange
				}

				// Fix logo alt text
				if transformed.Logo != nil {
					if alt, ok := transformed.Logo["alt"].(string); !ok || alt == "" {
						if transformed.Brand != "" {
							transformed.Logo["alt"] = transformed.Brand
						}
					}
				}

				transformedListings = append(transformedListings, transformed)
			case "association":
				// Revert association to mutate in place
				delete(listing, "investment")
				delete(listing, "investmentRange")
				delete(listing, "space")
				delete(listing, "franchise_id")
				delete(listing, "total_outlets")
				delete(listing, "exclusivity_type")
				delete(listing, "territory_scope")
				delete(listing, "territory_details")

				if mc, ok := listing["member_count"]; ok {
					listing["no_of_members"] = mc
				}
				if name, ok := listing["name"].(string); ok {
					listing["association_name"] = name
				}
				if fid, ok := listing["franchise_id"].(string); ok {
					listing["id"] = fid
				}

				// For listing cards: prefer short_description
				listingDesc := ""
				if sd, ok := listing["short_description"].(string); ok && sd != "" {
					listingDesc = sd
				}
				listing["short_description"] = listingDesc

				// Map color and category from industry nested object to top-level keys for card styling
				if industry, ok := listing["industry"].(map[string]interface{}); ok {
					if color, ok := industry["color"].(string); ok && color != "" {
						listing["color"] = color
					}
					if catName, ok := industry["name"].(string); ok && catName != "" {
						listing["category"] = catName
					}
				}

				if space, ok := listing["space"].(map[string]interface{}); ok {
					if _, hasUnit := space["spaceUnit"]; !hasUnit {
						space["spaceUnit"] = "sq ft"
					}
				}

				if invRange, ok := listing["investmentRange"].(map[string]interface{}); ok {
					if _, hasUnit := invRange["investmentUnit"]; !hasUnit {
						invRange["investmentUnit"] = "Lakhs"
					}
				}

				transformedListings = append(transformedListings, listing)
			}
		}

		sections = append(sections, map[string]interface{}{
			"type":    "franchise_listing",
			"enabled": true,
			"data":    transformedListings,
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
		},
		"metadata": map[string]interface{}{
			"generatedAt": time.Now().UTC().Format(time.RFC3339),
			"source":      "workflow",
			"pageType":    "listing",
			"version":     h.config.AppVersion,
		},
	}
}

// ===== DETAIL PAGE BUILDER =====
func (h *Handler) buildDetailResponse(data map[string]interface{}) map[string]interface{} {
	entityType := "franchise"
	if et, ok := data["entityType"].(string); ok && et != "" {
		entityType = et
	}

	if entityType == "association" {
		return h.buildAssociationDetailResponse(data)
	}

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

	if entityType == "master_franchise" {
		mfStructure := h.buildMasterFranchiseStructure(operations, investment)
		for k, v := range mfStructure {
			detailData[k] = v
		}
	}

	sections := []interface{}{}

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
			"version":     h.config.AppVersion,
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

	entityType := "franchise"
	if et, ok := data["entityType"].(string); ok && et != "" {
		entityType = et
	}

	return map[string]interface{}{
		"success": true,
		"data":    cleaned,
		"metadata": map[string]interface{}{
			"generatedAt": time.Now().UTC().Format(time.RFC3339),
			"source":      "workflow",
			"pageType":    "industries",
			"entityType":  entityType,
			"version":     h.config.AppVersion,
		},
	}
}

// ===== ASSOCIATION LISTING PAGE BUILDER =====
func (h *Handler) buildAssociationListingResponse(data map[string]interface{}) map[string]interface{} {
	sections := []interface{}{}

	franchises := h.extractArray(data, "franchises")
	categoryQuestions := h.extractArray(data, "categoryQuestions")
	recommended := h.extractArray(data, "recommended")
	marketInsights := h.extractArray(data, "marketInsights")
	categories := h.extractArray(data, "categories")
	if len(categories) == 0 {
		categories = h.extractArray(data, "featuredCategories")
	}

	page := 1
	pageSize := 6
	totalCount := int64(0)

	heroTitle := "Business Associations"
	heroDescription := "Discover top associations, guilds, and councils in your industry. Connect with powerful networks to access policy advocacy, skill development, B2B opportunities, and critical market insights to accelerate your business growth."

	industryInfo := h.extractMap(data, "industryInfo")
	if desc, ok := industryInfo["association_listing_description"].(string); ok && desc != "" {
		heroDescription = desc
	}

	searchParams := h.extractMap(data, "searchParams")
	multiIndustryTitle := ""
	if len(searchParams) > 0 {
		if ind, ok := searchParams["industry"].(string); ok && ind != "" {
			multiIndustryTitle = ind
		}
	}

	if len(industryInfo) > 0 {
		name := ""
		if n, ok := industryInfo["name"].(string); ok && n != "" {
			name = n
		} else if title, ok := industryInfo["title"].(string); ok && title != "" {
			name = title
		}
		if name != "" {
			heroTitle = fmt.Sprintf("%s Associations", name)
		}
	} else if multiIndustryTitle != "" {
		heroTitle = fmt.Sprintf("%s Associations", multiIndustryTitle)
	}

	if desc, ok := data["heroDescription"].(string); ok && desc != "" {
		heroDescription = desc
	}

	// 1. hero
	sections = append(sections, map[string]interface{}{
		"type":    "hero",
		"enabled": true,
		"data": map[string]interface{}{
			"title":       heroTitle,
			"description": heroDescription,
		},
	})

	// 2. business_associations
	transformedAssociations := []interface{}{}
	for _, item := range franchises {
		assoc, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		transformed := map[string]interface{}{}

		// id
		if id, ok := assoc["id"].(string); ok {
			transformed["id"] = id
		} else if fid, ok := assoc["franchise_id"].(string); ok {
			transformed["id"] = fid
		}

		// name
		if name, ok := assoc["name"].(string); ok {
			transformed["association_name"] = name
		} else if brand, ok := assoc["brand"].(string); ok {
			transformed["association_name"] = brand
		}

		// slug
		if slug, ok := assoc["slug"].(string); ok {
			transformed["slug"] = slug
		}

		// description
		desc := ""
		if sd, ok := assoc["short_description"].(string); ok && sd != "" {
			desc = sd
		} else if d, ok := assoc["description"].(string); ok {
			desc = d
		}
		transformed["description"] = desc

		// association_metadata
		assocMeta := h.extractMap(assoc, "association_metadata")
		if len(assocMeta) == 0 {
			if metadata, ok := assoc["association_metadata"].(map[string]interface{}); ok {
				assocMeta = metadata
			}
		}

		// association_type
		assocType := "Industry Body"
		if at, ok := assocMeta["association_type"].(string); ok && at != "" {
			assocType = at
		} else if at, ok := assoc["association_type"].(string); ok && at != "" {
			assocType = at
		}
		transformed["association_type"] = assocType

		// membership fee range
		minFee := getFloatValue(assoc, "membership_fee_min")
		maxFee := getFloatValue(assoc, "membership_fee_max")

		if minFee == 0 && maxFee == 0 {
			if memDetails, ok := assocMeta["membership_details"].(map[string]interface{}); ok {
				if feeMin, ok := memDetails["membership_fee_min"].(float64); ok {
					minFee = feeMin
				}
				if feeMax, ok := memDetails["membership_fee_max"].(float64); ok {
					maxFee = feeMax
				}
			}
		}

		feeUnit := "INR"
		transformed["MembershipFeeRange"] = map[string]interface{}{
			"FeeUnit": feeUnit,
			"minFee":  interface{}(nil),
			"maxFee":  interface{}(nil),
		}
		if minFee > 0 {
			transformed["MembershipFeeRange"].(map[string]interface{})["minFee"] = minFee
		}
		if maxFee > 0 {
			transformed["MembershipFeeRange"].(map[string]interface{})["maxFee"] = maxFee
		}

		// location
		loc := ""
		if l, ok := assoc["location"].(string); ok {
			loc = l
		} else if city, ok := assoc["city"].(string); ok {
			loc = city + ",India"
		}
		transformed["location"] = loc

		// logo
		logoUrl := ""
		if logo, ok := assoc["logo"].(map[string]interface{}); ok {
			if sq, ok := logo["square"].(string); ok && sq != "" {
				logoUrl = sq
			} else if cir, ok := logo["circle"].(string); ok && cir != "" {
				logoUrl = cir
			} else if urlVal, ok := logo["url"].(string); ok {
				logoUrl = urlVal
			}
		} else if logoStr, ok := assoc["logo_url_square"].(string); ok && logoStr != "" {
			logoUrl = logoStr
		} else if logoStr, ok := assoc["logo_url_circle"].(string); ok && logoStr != "" {
			logoUrl = logoStr
		}
		// No logo fallback

		assocName, _ := transformed["association_name"].(string)
		transformed["logo"] = map[string]interface{}{
			"alt": assocName,
			"url": logoUrl,
		}

		// no_of_members
		noOfMembers := interface{}(nil)
		if mcVal := getFloatValue(assoc, "member_count"); mcVal > 0 {
			noOfMembers = int(mcVal)
		}
		transformed["no_of_members"] = noOfMembers

		// tags
		tagsList := []interface{}{}
		if overviewVal, ok := assocMeta["overview"].(map[string]interface{}); ok {
			if kf, ok := overviewVal["key_functions"].([]interface{}); ok {
				tagsList = kf
			}
		}
		if len(tagsList) == 0 {
			if tags, ok := assoc["tags"].([]interface{}); ok {
				tagsList = tags
			}
		}
		// No tags fallback
		transformed["tags"] = tagsList

		// year_of_establishment
		estYear := ""
		if fyVal := getFloatValue(assoc, "year_of_establishment"); fyVal > 0 {
			estYear = strconv.Itoa(int(fyVal))
		} else if fyVal := getFloatValue(assoc, "founded_year"); fyVal > 0 {
			estYear = strconv.Itoa(int(fyVal))
		}
		// No estYear fallback
		transformed["year_of_establishment"] = estYear

		transformedAssociations = append(transformedAssociations, transformed)
	}

	sections = append(sections, map[string]interface{}{
		"type":    "business_associations",
		"enabled": true,
		"data":    transformedAssociations,
	})

	// 3. functions_of_business_associations — REMOVED (hardcoded, not from DB)
	// 4. statistics — REMOVED (hardcoded, not from DB)

	// 5. featured_business_categories
	var transformedCategories []interface{}
	if len(categories) > 0 {
		for _, item := range categories {
			if cMap, ok := item.(map[string]interface{}); ok {
				transformedCategories = append(transformedCategories, map[string]interface{}{
					"id":       getStringVal(cMap, "id", ""),
					"name":     getStringVal(cMap, "name", ""),
					"slug":     getStringVal(cMap, "slug", ""),
					"icon_url": getStringVal(cMap, "icon_url", "/AssociationImages/FeaturedBusinessCategories/technology.svg"),
				})
			}
		}
	} else {
		transformedCategories = []interface{}{
			map[string]interface{}{"id": "1", "name": "Technology", "slug": "technology", "icon_url": "/AssociationImages/FeaturedBusinessCategories/technology.svg"},
			map[string]interface{}{"id": "2", "name": "Finance", "slug": "finance", "icon_url": "/AssociationImages/FeaturedBusinessCategories/finance.svg"},
			map[string]interface{}{"id": "3", "name": "Healthcare", "slug": "healthcare", "icon_url": "/AssociationImages/FeaturedBusinessCategories/healthcare.svg"},
			map[string]interface{}{"id": "4", "name": "Manufacturing", "slug": "manufacturing", "icon_url": "/AssociationImages/FeaturedBusinessCategories/manufacturing.svg"},
			map[string]interface{}{"id": "5", "name": "Education", "slug": "education", "icon_url": "/AssociationImages/FeaturedBusinessCategories/education.svg"},
			map[string]interface{}{"id": "6", "name": "Real Estate", "slug": "real-estate", "icon_url": "/AssociationImages/FeaturedBusinessCategories/real-estate.svg"},
			map[string]interface{}{"id": "7", "name": "Retail", "slug": "retail", "icon_url": "/AssociationImages/FeaturedBusinessCategories/retail.svg"},
			map[string]interface{}{"id": "8", "name": "Agriculture", "slug": "agriculture", "icon_url": "/AssociationImages/FeaturedBusinessCategories/agriculture.svg"},
		}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "explore_by_categories",
		"enabled": true,
		"data": map[string]interface{}{
			"categories": transformedCategories,
		},
	})

	// 6. category_questions
	var listingQuestions []interface{}
	if len(categoryQuestions) > 0 {
		for _, qItem := range categoryQuestions {
			if qm, ok := qItem.(map[string]interface{}); ok {
				listingQuestions = append(listingQuestions, map[string]interface{}{
					"question": getStringVal(qm, "question", ""),
				})
			} else if qStr, ok := qItem.(string); ok {
				listingQuestions = append(listingQuestions, map[string]interface{}{
					"question": qStr,
				})
			}
		}
	}
	if len(listingQuestions) == 0 {
		listingQuestions = []interface{}{
			map[string]interface{}{
				"question": "What are the primary functions of business associations in India?",
			},
			map[string]interface{}{
				"question": "How do I become a member of a trade association?",
			},
			map[string]interface{}{
				"question": "Are membership fees tax-deductible?",
			},
		}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "category_questions",
		"enabled": true,
		"data": map[string]interface{}{
			"questions": listingQuestions,
		},
	})

	// 7. recommended_business_associations
	var transformedRecs []interface{}
	if len(recommended) > 0 {
		for _, item := range recommended {
			if rMap, ok := item.(map[string]interface{}); ok {
				transformedRecs = append(transformedRecs, map[string]interface{}{
					"id":       getStringVal(rMap, "id", ""),
					"name":     getStringVal(rMap, "brand", getStringVal(rMap, "name", "")),
					"slug":     getStringVal(rMap, "slug", ""),
					"icon_url": getStringVal(rMap, "logo_url", ""),
				})
			}
		}
	}
	if len(transformedRecs) == 0 {
		transformedRecs = []interface{}{
			map[string]interface{}{
				"id":       "1",
				"name":     "NASSCOM",
				"slug":     "nasscom",
				"icon_url": "",
			},
		}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "recommended_business_associations",
		"enabled": true,
		"data":    transformedRecs,
	})

	// 8. key_market_insights
	// key_market_insights: use only real data from ES (growth_rate + market_trend).
	// No hardcoded fallback — if ES returned nothing the section is skipped.
	if len(marketInsights) > 0 {
		if insights, ok := marketInsights[0].(map[string]interface{}); ok {
			insightsData := map[string]interface{}{}

			// growth_rate block (from ES field growth_rate_title / growth_rate_description)
			if gr, ok := insights["growth_rate"].(map[string]interface{}); ok {
				insightsData["growth_rate"] = gr
			}

			// market_trend block (from ES field market_trend_title / market_trend_description)
			if mt, ok := insights["market_trend"].(map[string]interface{}); ok {
				insightsData["market_trend"] = mt
			}

			// Only append the section when at least one field was populated
			if len(insightsData) > 0 {
				sections = append(sections, map[string]interface{}{
					"type":    "key_market_insights",
					"enabled": true,
					"data":    insightsData,
				})
			}
		}
	}

	if v, ok := data["page"].(float64); ok {
		page = int(v)
	}
	if v, ok := data["pageSize"].(float64); ok {
		pageSize = int(v)
	}
	if v, ok := data["totalCount"].(float64); ok {
		totalCount = int64(v)
	}

	return map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"pageId":   "association_listing",
			"sections": sections,
			"pagination": map[string]interface{}{
				"currentPage": page,
				"pageSize":    pageSize,
				"totalItems":  totalCount,
				"totalPages":  int(math.Ceil(float64(totalCount) / float64(pageSize))),
				"hasNextPage": int64(page*pageSize) < totalCount,
			},
		},
		"metadata": map[string]interface{}{
			"generatedAt": time.Now().UTC().Format(time.RFC3339),
			"source":      "workflow",
			"pageType":    "listing",
			"version":     h.config.AppVersion,
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

	// Franchise Detail Page needs long description
	// ES doc has both, but we want to ensure `description` is prioritized if it exists.
	// Since we copied everything, we just need to make sure `description` is correct.
	desc := ""
	if d, ok := result["description"].(string); ok && d != "" {
		desc = d
	} else if sd, ok := result["short_description"].(string); ok {
		desc = sd
	}
	result["description"] = desc

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

	if ib, ok := investment["investment_breakdown"].([]interface{}); ok {
		result["investment_breakdown"] = ib
	} else {
		result["investment_breakdown"] = []string{}
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

	if rpl, ok := operations["required_property_location"].([]interface{}); ok {
		result["required_property_location"] = rpl
	} else {
		result["required_property_location"] = []string{}
	}

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
		result["required_property"] = ""
	}

	if sector, ok := operations["sector"].(string); ok && sector != "" {
		result["sector"] = sector
	} else {
		result["sector"] = ""
	}

	if service, ok := operations["service"].([]interface{}); ok {
		result["service"] = service
	} else {
		result["service"] = []string{}
	}

	if qual, ok := operations["qualification_required"].(string); ok && qual != "" {
		result["qualification_required"] = qual
	} else {
		result["qualification_required"] = ""
	}

	if ab, ok := operations["is_absentee_ownership_allowed"].(string); ok && ab != "" {
		result["is_absentee_ownership_allowed"] = ab
	} else {
		result["is_absentee_ownership_allowed"] = ""
	}

	if home, ok := operations["can_be_run_from_home_or_mobile"].(string); ok && home != "" {
		result["can_be_run_from_home_or_mobile"] = home
	} else {
		result["can_be_run_from_home_or_mobile"] = ""
	}

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

func (h *Handler) buildMasterFranchiseStructure(operations, investment map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}

	// 1. Territory Rights
	territoryRights := map[string]interface{}{}
	if td, ok := operations["territory_details"].(map[string]interface{}); ok {
		territoryRights["scope"] = td["scope"]
		territoryRights["exclusivity_terms"] = td["exclusivity_terms"]
		territoryRights["available_territories"] = td["available_territories"]
		territoryRights["taken_territories"] = td["taken_territories"]
	} else {
		territoryRights["scope"] = ""
		territoryRights["exclusivity_terms"] = ""
		territoryRights["available_territories"] = []interface{}{}
		territoryRights["taken_territories"] = []interface{}{}
	}
	result["territory_rights"] = territoryRights

	// 2. Development Schedule
	devSchedule := map[string]interface{}{}
	if ds, ok := operations["development_schedule"].(map[string]interface{}); ok {
		devSchedule["obligations"] = ds["obligations"]
		devSchedule["timeline_months"] = ds["timeline_months"]
		devSchedule["target_units"] = ds["target_units"]
	} else {
		devSchedule["obligations"] = ""
		devSchedule["timeline_months"] = 0
		devSchedule["target_units"] = 0
	}
	result["development_schedule"] = devSchedule

	// 3. Support and Training
	supportTraining := map[string]interface{}{}
	if st, ok := operations["support_training"].(map[string]interface{}); ok {
		supportTraining["brand_toolkits"] = st["brand_toolkits"]
		supportTraining["operational_manuals"] = st["operational_manuals"]
		supportTraining["training_duration_days"] = st["training_duration_days"]
	} else {
		supportTraining["brand_toolkits"] = ""
		supportTraining["operational_manuals"] = ""
		supportTraining["training_duration_days"] = 0
	}
	result["support_and_training"] = supportTraining

	// 4. Legal Compliance
	legalCompliance := map[string]interface{}{}
	if lc, ok := operations["legal_compliance"].(map[string]interface{}); ok {
		legalCompliance["agreement_term_years"] = lc["agreement_term_years"]
		legalCompliance["renewal_term_years"] = lc["renewal_term_years"]
		legalCompliance["regulatory_licenses"] = lc["regulatory_licences"]
		if legalCompliance["regulatory_licenses"] == nil {
			legalCompliance["regulatory_licenses"] = lc["regulatory_licenses"]
		}
	} else {
		legalCompliance["agreement_term_years"] = 0
		legalCompliance["renewal_term_years"] = 0
		legalCompliance["regulatory_licenses"] = []interface{}{}
	}
	result["legal_compliance"] = legalCompliance

	// 5. Revenue Model
	revenueModel := map[string]interface{}{}
	if rm, ok := investment["revenue_model"].(map[string]interface{}); ok {
		revenueModel["payback_period"] = rm["payback_period"]
		revenueModel["performance_bonuses"] = rm["performance_bonuses"]
		revenueModel["roi_calculator_inputs"] = rm["roi_calculator_inputs"]
	} else {
		revenueModel["payback_period"] = ""
		revenueModel["performance_bonuses"] = ""
		revenueModel["roi_calculator_inputs"] = map[string]interface{}{}
	}
	result["revenue_model"] = revenueModel

	if roles, ok := operations["three_player_roles"].(map[string]interface{}); ok {
		result["three_player_roles"] = roles
	} else {
		result["three_player_roles"] = map[string]interface{}{
			"franchisor":        "",
			"master_franchisee": "",
			"unit_franchisees":  "",
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
			"version":     h.config.AppVersion,
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
