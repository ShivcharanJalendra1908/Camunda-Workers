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
		response = h.buildListingResponse(combinedData)
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
		transformed["description"] = assoc["description"]

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

		transformed["logo"] = map[string]interface{}{
			"alt": "",
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
		if fyVal := getFloatValue(assoc, "founded_year"); fyVal > 0 {
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

	// 3. why_choose_lemici (Cities)
	cities := h.extractArray(data, "cities")
	if len(cities) == 0 {
		cities = []interface{}{
			map[string]interface{}{"icon_url": "/AssociationImages/cities/delhi.jpg", "id": "1", "name": "Delhi", "slug": "delhi"},
			map[string]interface{}{"icon_url": "/AssociationImages/cities/bengaluru.jpg", "id": "2", "name": "Bengaluru", "slug": "bengaluru"},
			map[string]interface{}{"icon_url": "/AssociationImages/cities/hyderabad.jpg", "id": "3", "name": "Hyderabad", "slug": "hyderabad"},
			map[string]interface{}{"icon_url": "/AssociationImages/cities/ahmedabad.png", "id": "4", "name": "Ahmedabad", "slug": "ahmedabad"},
			map[string]interface{}{"icon_url": "/AssociationImages/cities/pune.jpg", "id": "5", "name": "Pune", "slug": "pune"},
			map[string]interface{}{"icon_url": "/AssociationImages/cities/kolkata.jpg", "id": "6", "name": "Kolkata", "slug": "kolkata"},
			map[string]interface{}{"icon_url": "/AssociationImages/cities/chennai.png", "id": "7", "name": "Chennai", "slug": "chennai"},
			map[string]interface{}{"icon_url": "/AssociationImages/cities/mumbai.jpg", "id": "8", "name": "Mumbai", "slug": "mumbai"},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "why_choose_lemici",
		"enabled": true,
		"data": map[string]interface{}{
			"cities": cities,
		},
	})

	// 4. statistics
	stats := h.extractArray(data, "statistics")
	if len(stats) == 0 {
		stats = []interface{}{
			map[string]interface{}{
				"products":           10000000,
				"businesses":         10000,
				"product_categories": 5900,
				"countries_regions":  200,
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "statistics",
		"enabled": true,
		"data":    stats,
	})

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
	if len(tags) > 0 {
		for _, t := range tags {
			transformedTags = append(transformedTags, t)
		}
	} else {
		transformedTags = []interface{}{
			"12,000+ Members",
			"ISO 9001:2015 Certified",
			"Policy & Grievance Support",
			"Entrepreneur Support",
		}
	}

	socialLinksVal := getMapVal(contact_details, "social_links")
	socialLinks := map[string]interface{}{
		"youtube":   getStringVal(socialLinksVal, "youtube", "https://youtube.com"),
		"facebook":  getStringVal(socialLinksVal, "facebook", "https://facebook.com"),
		"instagram": getStringVal(socialLinksVal, "instagram", "https://instagram.com"),
		"twitter":   getStringVal(socialLinksVal, "twitter", "https://x.com"),
		"linkedin":  getStringVal(socialLinksVal, "linkedin", "https://linkedin.com"),
	}

	heroData := map[string]interface{}{
		"name":         name,
		"slug":         slug,
		"logo":         logoURL,
		"description":  description,
		"likes":        getStringVal(basicInfo, "likes_count", "107"),
		"rating":       getStringVal(basicInfo, "rating", "4.5"),
		"review_count": getStringVal(basicInfo, "rating_count", "99"),
		"tags":         transformedTags,
		"socialLinks":  socialLinks,
	}

	sections = append(sections, map[string]interface{}{
		"type":    "association_hero_info_card",
		"enabled": true,
		"data":    heroData,
	})

	// 2. association_info_grid
	infoGridData := map[string]interface{}{
		"association_name":      name,
		"association_type":      getStringVal(metadata, "association_type", "Industry body / State-level trade association"),
		"sector":                getStringVal(overview, "sector", "Small scale industries / MSMEs"),
		"year_of_establishment": getStringVal(basicInfo, "established_year", "1949"),
		"legal_status":          getStringVal(metadata, "legal_status", "Non-government industry association (trade body)"),
		"headquarters":          getStringVal(contact_details, "office_address", "2/106, 17th Cross, Magadi Chord Road, Vijayanagar, Bangalore-560040, Karnataka, India"),
		"regional_presence":     getStringVal(regional_structure, "regional_offices", "Primarily Karnataka with state & national representation"),
		"website":               getStringVal(contact_details, "website", "https://kassia.org.in/"),
		"contact_details":       getStringVal(contact_details, "phone_number", "(080) 2335 3250 / 2335 8698"),
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
	} else {
		transformedMembership = []interface{}{
			map[string]interface{}{"detail": "Total Number of Members", "description": "12,000+ members (individual MSME units)"},
			map[string]interface{}{"detail": "Affiliated Associations", "description": "127 affiliated industrial associations across Karnataka"},
			map[string]interface{}{"detail": "Eligibility for Membership", "description": "Small-scale / MSME industrial units operating in Karnataka"},
		}
	}

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
	} else {
		transformedEligibility = []interface{}{
			map[string]interface{}{"detail": "Business Type", "description": "MSMEs, manufacturers, startups, and industrial enterprises"},
			map[string]interface{}{"detail": "Location Requirement", "description": "Business operations should be based in Karnataka"},
			map[string]interface{}{"detail": "Documents Required", "description": "GST certificate, business registration, PAN card"},
		}
	}

	applicationVal := getMapVal(membership_details, "application_process")
	var transformedApplication []interface{}
	if len(applicationVal) > 0 {
		for k, v := range applicationVal {
			transformedApplication = append(transformedApplication, map[string]interface{}{
				"detail":      k,
				"description": fmt.Sprintf("%v", v),
			})
		}
	} else {
		transformedApplication = []interface{}{
			map[string]interface{}{"detail": "Application Process", "description": "Submit online membership application form"},
			map[string]interface{}{"detail": "Approval Timeline", "description": "Usually approved within 7–15 business days"},
		}
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
	} else {
		transformedServices = []interface{}{
			map[string]interface{}{"title": "Policy Advocacy", "description": "Actively raises MSME sector policy issues with the Government and participates in policy forums."},
			map[string]interface{}{"title": "Government Representation", "description": "Formal representation on central and state government committees."},
			map[string]interface{}{"title": "Market Access Programs", "description": "Provides buyer–seller information, marketing leads, national & international tender notifications."},
		}
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
				transformedPrograms[cat] = catList
			}
		}
	} else {
		transformedPrograms = map[string]interface{}{
			"Startup_Programs": []interface{}{
				map[string]interface{}{"title": "MSME Entrepreneurship Promotion", "description": "KASSIA promotes entrepreneurship and supports startup MSMEs through guidance, advocacy, and scheme awareness."},
			},
			"Skill_Development_Initiatives": []interface{}{
				map[string]interface{}{"title": "Industrial Skill Development", "description": "Skill enhancement programs for workforce readiness and MSME industrial growth."},
			},
		}
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
				transformedPublications[cat] = catList
			}
		}
	} else {
		transformedPublications = map[string]interface{}{
			"Industry_Reports": []interface{}{
				map[string]interface{}{"title": "MSME Industry Overview Publications", "description": "General industry insights and MSME sector updates issued through KASSIA platforms."},
			},
		}
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
	} else {
		transformedChapters = []interface{}{
			map[string]interface{}{
				"name":                 "KASSIA Bengaluru Chapter",
				"headquarters_address": "Magadi Chord Road, Vijayanagar, Bangalore-560040",
				"contact_email":        "info@kassia.org.in",
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "regional_structure_section",
		"enabled": true,
		"data": map[string]interface{}{
			"governance_model": getStringVal(regional_structure, "governance_model", "Governing Council consisting of elected representatives from districts and affiliated associations."),
			"headquarters":     getStringVal(regional_structure, "headquarters", "Bengaluru, Karnataka, India"),
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
				transformedEvents[cat] = catList
			}
		}
	} else {
		transformedEvents = map[string]interface{}{
			"Conferences_&_Seminars": []interface{}{
				map[string]interface{}{"title": "India MSME Conclave", "description": "National platform bringing together policy makers, industry leaders, and MSME stakeholders to discuss challenges and growth strategies."},
			},
		}
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
				transformedPolicies[cat] = catList
			}
		}
	} else {
		transformedPolicies = map[string]interface{}{
			"Standard_Operating_Guidelines": []interface{}{
				map[string]interface{}{"title": "Udyam Registration Advisory", "description": "Guideline helping micro enterprises register under the updated MSME definition."},
			},
		}
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
	} else {
		transformedPartners = []interface{}{
			map[string]interface{}{
				"partner_name": "Government of Karnataka – Dept. of MSME",
				"relation":     "Policy advocacy, MSME representation, industrial policy inputs",
				"website":      "https://kassia.org.in/about-us/",
			},
		}
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
	} else {
		transformedAwards = []interface{}{
			map[string]interface{}{
				"award_title": "ISO 9001:2015 Certification",
				"description": "Certified for maintaining quality standards in providing support services to small-scale industries.",
			},
		}
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
	} else {
		transformedDigital = []interface{}{
			map[string]interface{}{
				"category":      "Official Website",
				"property_name": "https://kassia.org.in",
				"status":        "Active",
				"description":   "Primary online hub for member services, Udyam support, news, and notifications.",
			},
		}
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
	} else {
		transformedTransparency = []interface{}{
			map[string]interface{}{"title": "Financial Audit Status", "description": "Audited annually by certified chartered accountants.", "status": "Audited"},
			map[string]interface{}{"title": "Governing Council Disclosures", "description": "Names and designations of all council members publicly disclosed.", "status": "Available"},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "transparency_verification_section",
		"enabled": true,
		"data":    transformedTransparency,
	})

	// 14. members_structure_tree
	var structure map[string]interface{}
	if len(governance) > 0 {
		structure = governance
	} else {
		structure = map[string]interface{}{
			"name":        "Sri B.R Ganesh Rao",
			"designation": "President",
			"children": []interface{}{
				map[string]interface{}{
					"name":        "Sri Ninganna S. Biradar",
					"designation": "Vice-President",
				},
				map[string]interface{}{
					"name":        "Sri S.M Hussain",
					"designation": "Hon. General Secretary",
				},
				map[string]interface{}{
					"name":        "Sri Durai R.",
					"designation": "Treasurer",
				},
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "members_structure_tree",
		"enabled": true,
		"data":    structure,
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
	} else {
		transformedRecs = []interface{}{
			map[string]interface{}{"id": "1", "name": "NASSCOM", "slug": "nascomm", "icon_url": "/AssociationImages/FeaturedAssociations/nasscom.svg"},
			map[string]interface{}{"id": "2", "name": "Kassia", "slug": "kassia", "icon_url": "/AssociationImages/FeaturedAssociations/kassia.svg"},
			map[string]interface{}{"id": "3", "name": "FICCI", "slug": "ficci", "icon_url": "/AssociationImages/FeaturedAssociations/ficci.svg"},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "recommended_business_associations",
		"enabled": true,
		"data":    transformedRecs,
	})

	// 16. market_insights_section
	var transformedInsights map[string]interface{}
	if len(data_and_insights) > 0 {
		trends := getArrayVal(data_and_insights, "sector_trends")
		var trendList []interface{}
		for _, t := range trends {
			trendList = append(trendList, t)
		}
		transformedInsights = map[string]interface{}{
			"market_stats":  getStringVal(data_and_insights, "market_stats", ""),
			"sector_trends": trendList,
			"exim_data":     getStringVal(data_and_insights, "exim_data", ""),
			"cluster_info":  getStringVal(data_and_insights, "cluster_info", ""),
		}
	} else if len(marketInsights) > 0 {
		if insights, ok := marketInsights[0].(map[string]interface{}); ok {
			trends := getArrayVal(insights, "sector_trends")
			var trendList []interface{}
			for _, t := range trends {
				trendList = append(trendList, t)
			}
			if len(trendList) == 0 {
				trendList = []interface{}{
					"Rising adoption of AI and automation in manufacturing.",
					"Increased focus on sustainable and green manufacturing practices.",
					"Growing integration of MSMEs into global supply chains.",
				}
			}
			transformedInsights = map[string]interface{}{
				"market_stats":  getStringVal(insights, "market_stats", "Karnataka MSME sector contributes 20% to state GDP with over 8 lakh registered units."),
				"sector_trends": trendList,
				"exim_data":     getStringVal(insights, "exim_data", "MSME exports from Karnataka account for approximately $10 billion annually."),
				"cluster_info":  getStringVal(insights, "cluster_info", "Major clusters include Peenya (manufacturing), Belagavi (foundry), and Hubli (valves/machine tools)."),
			}
		}
	}
	if len(transformedInsights) == 0 {
		transformedInsights = map[string]interface{}{
			"market_stats": "Karnataka MSME sector contributes 20% to state GDP with over 8 lakh registered units.",
			"sector_trends": []interface{}{
				"Rising adoption of AI and automation in manufacturing.",
				"Increased focus on sustainable and green manufacturing practices.",
				"Growing integration of MSMEs into global supply chains.",
			},
			"exim_data":    "MSME exports from Karnataka account for approximately $10 billion annually.",
			"cluster_info": "Major clusters include Peenya (manufacturing), Belagavi (foundry), and Hubli (valves/machine tools).",
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "market_insights_section",
		"enabled": true,
		"data":    transformedInsights,
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
	if len(detailQuestions) == 0 {
		detailQuestions = []interface{}{
			map[string]interface{}{
				"question": "What is the membership process?",
				"answer":   "The membership process involves submitting an online application along with business proof like GST/PAN certificate, followed by approval within 7-15 working days.",
			},
			map[string]interface{}{
				"question": "Does this association support export-import guidelines?",
				"answer":   "Yes, the association regularizes and organizes EXIM workshops, consultancies, and representation on international trade fairs for its premium members.",
			},
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
		if slug, ok := basicInfo["slug"].(string); ok && slug != "" {
			detailData["slug"] = slug
		}
	}

	return map[string]interface{}{
		"success": true,
		"data":    detailData,
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
func (h *Handler) buildListingResponse(data map[string]interface{}) map[string]interface{} {
	entityType := "franchise"
	if et, ok := data["entityType"].(string); ok && et != "" {
		entityType = et
	}

	if entityType == "association" {
		return h.buildAssociationListingResponse(data)
	}

	sections := []interface{}{}

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
			if d, ok := industryInfo["listing_description"].(string); ok && d != "" {
				heroDescription = d
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
			"type":    "franchise_listing",
			"enabled": true,
			"data":    franchises,
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
	categories := h.extractArray(data, "categories")
	categoryQuestions := h.extractArray(data, "categoryQuestions")
	recommended := h.extractArray(data, "recommended")
	marketInsights := h.extractArray(data, "marketInsights")

	page := 1
	pageSize := 6
	totalCount := int64(0)

	heroTitle := "Business Associations"
	heroDescription := "Discover associations and networking opportunities in your industry"

	searchParams := h.extractMap(data, "searchParams")
	multiIndustryTitle := ""
	if len(searchParams) > 0 {
		if ind, ok := searchParams["industry"].(string); ok && ind != "" {
			multiIndustryTitle = ind
		}
	}

	industryInfo := h.extractMap(data, "industryInfo")
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

		// description
		transformed["description"] = assoc["description"]

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
		if logoUrl == "" {
			logoUrl = "/AssociationImages/FeaturedAssociations/kassia.svg"
		}

		transformed["logo"] = map[string]interface{}{
			"alt": "",
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
		if len(tagsList) == 0 {
			tagsList = []interface{}{
				"ISO 9001:2015 Certified",
				"Policy & Grievance Support",
				"Entrepreneur Support",
			}
		}
		transformed["tags"] = tagsList

		// year_of_establishment
		estYear := ""
		if fyVal := getFloatValue(assoc, "founded_year"); fyVal > 0 {
			estYear = strconv.Itoa(int(fyVal))
		}
		if estYear == "" {
			estYear = "1949"
		}
		transformed["year_of_establishment"] = estYear

		transformedAssociations = append(transformedAssociations, transformed)
	}

	sections = append(sections, map[string]interface{}{
		"type":    "business_associations",
		"enabled": true,
		"data":    transformedAssociations,
	})

	// 3. functions_of_business_associations
	sections = append(sections, map[string]interface{}{
		"type":    "functions_of_business_associations",
		"enabled": true,
		"data": []interface{}{
			map[string]interface{}{
				"title":       "Policy Advocacy",
				"description": "Representing MSME interests to state and central governments to influence industrial policies and resolve regulatory grievances.",
				"icon":        "AdvocacyIcon",
			},
			map[string]interface{}{
				"title":       "Business Networking",
				"description": "Facilitating B2B meetings, industrial exhibitions, and international trade delegations to open new market opportunities.",
				"icon":        "NetworkingIcon",
			},
			map[string]interface{}{
				"title":       "Industrial Development",
				"description": "Providing technical training, workshops, and seminars on quality standards, automation, and emerging technologies.",
				"icon":        "DevelopmentIcon",
			},
			map[string]interface{}{
				"title":       "Collaboration & Support",
				"description": "Fostering strategic partnerships between industries, academia, and government bodies to promote cluster development.",
				"icon":        "CollaborationIcon",
			},
		},
	})

	// 4. statistics
	stats := h.extractArray(data, "statistics")
	if len(stats) == 0 {
		stats = []interface{}{
			map[string]interface{}{
				"businesses_engaged_annually": 3000000,
				"msme_india":                  6000000,
				"enablers_partnered":          1500,
				"live_events_annually":        1200,
			},
		}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "statistics",
		"enabled": true,
		"data":    stats,
	})

	// 5. business_associations_across_india
	cities := h.extractArray(data, "cities")
	if len(cities) == 0 {
		cities = []interface{}{
			map[string]interface{}{
				"state":       "Andhra Pradesh",
				"slug":        "andhra-pradesh",
				"map":         "/AssociationImages/BusinessAcrossIndia/states/andhra-pradesh.png",
				"projects":    15,
				"consultants": 20,
				"overview":    "Andhra Pradesh is the second largest producer of cotton and raw silk in India. The state has a strong textile industry base consisting of handlooms, handicrafts, spinning and processing units. The state has integrated apparel city in Vizag with an innovative concept of \"Fibre to Store\".",
				"industries":  "Textiles, IT, Pharmaceuticals, Agriculture",
				"associations": "FAPCCI, APITC, Textile Alliance",
				"growth":      "18% YoY in manufacturing sector",
				"highlights": []interface{}{
					map[string]interface{}{
						"title":       "Innovation Hubs",
						"description": "Multiple innovation centers and incubators supporting startups and SMEs with mentorship, funding, and infrastructure.",
						"icon":        "InnovationIcon",
					},
					map[string]interface{}{
						"title":       "Startup Ecosystem",
						"description": "Growing startup community with government support, angel investors, and venture capital presence.",
						"icon":        "StartupIcon",
					},
					map[string]interface{}{
						"title":       "Skill Development",
						"description": "Strong focus on vocational training and skill development programs aligned with industry needs.",
						"icon":        "SkillIcon",
					},
					map[string]interface{}{
						"title":       "Infrastructure",
						"description": "Well-developed industrial parks, SEZs, ports, and connectivity through road, rail, and air networks.",
						"icon":        "InfrastructureIcon",
					},
				},
			},
			map[string]interface{}{
				"state":       "Maharashtra",
				"slug":        "maharashtra",
				"map":         "/AssociationImages/BusinessAcrossIndia/states/maharashtraMap.svg",
				"projects":    30,
				"consultants": 40,
				"overview":    "Maharashtra is India's financial powerhouse with strong industrial and startup ecosystems.",
				"industries":  "Finance, IT, Automobile",
				"associations": "MCCIA, IMC",
				"growth":      "22% startup growth",
				"highlights": []interface{}{
					map[string]interface{}{
						"title":       "Financial Capital",
						"description": "Mumbai serves as India's financial center.",
						"icon":        "InnovationIcon",
					},
					map[string]interface{}{
						"title":       "Startup Ecosystem",
						"description": "Thriving startup communities and incubators.",
						"icon":        "StartupIcon",
					},
				},
			},
		}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "business_associations_across_india",
		"enabled": true,
		"data":    cities,
	})

	// 6. featured_business_categories
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
		}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "featured_business_categories",
		"enabled": true,
		"data":    transformedCategories,
	})

	// 7. category_questions
	var listingQuestions []interface{}
	if len(categoryQuestions) > 0 {
		for _, qItem := range categoryQuestions {
			if qm, ok := qItem.(map[string]interface{}); ok {
				listingQuestions = append(listingQuestions, map[string]interface{}{
					"question": getStringVal(qm, "question", ""),
					"answer":   getStringVal(qm, "answer", ""),
				})
			}
		}
	}
	if len(listingQuestions) == 0 {
		listingQuestions = []interface{}{
			map[string]interface{}{
				"question": "What are the primary functions of business associations in India?",
				"answer":   "Business associations provide policy advocacy, industry networking, skill development, market linkage, and standards certification to help MSMEs grow.",
			},
			map[string]interface{}{
				"question": "How do I become a member of a trade association?",
				"answer":   "To join, select the association relevant to your industry and region, check the eligibility criteria, and submit an application form along with business proof like GST/PAN.",
			},
			map[string]interface{}{
				"question": "Are membership fees tax-deductible?",
				"answer":   "Yes, membership subscriptions paid to professional or business associations are generally deductible as business expenses under Section 37(1) of the Income Tax Act.",
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

	// 8. recommended_business_associations
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
	} else {
		transformedRecs = []interface{}{
			map[string]interface{}{"id": "1", "name": "NASSCOM", "slug": "nascomm", "icon_url": "/AssociationImages/FeaturedAssociations/nasscom.svg"},
			map[string]interface{}{"id": "2", "name": "Kassia", "slug": "kassia", "icon_url": "/AssociationImages/FeaturedAssociations/kassia.svg"},
			map[string]interface{}{"id": "3", "name": "FICCI", "slug": "ficci", "icon_url": "/AssociationImages/FeaturedAssociations/ficci.svg"},
		}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "recommended_business_associations",
		"enabled": true,
		"data":    transformedRecs,
	})

	// 9. key_market_insights
	var insightsData interface{}
	if len(marketInsights) > 0 {
		if insights, ok := marketInsights[0].(map[string]interface{}); ok {
			trends := getArrayVal(insights, "sector_trends")
			var trendList []interface{}
			for _, t := range trends {
				trendList = append(trendList, t)
			}
			if len(trendList) == 0 {
				trendList = []interface{}{
					"Accelerated digitization of supply chains and business processes.",
					"Increased integration into global value chains through trade associations.",
					"Government schemes like PLI and Udyam boosting MSME manufacturing.",
				}
			}
			insightsData = map[string]interface{}{
				"market_stats":  getStringVal(insights, "market_stats", "India has over 6.3 crore MSMEs, contributing 30% to GDP and employing 11 crore people."),
				"sector_trends": trendList,
				"exim_data":     getStringVal(insights, "exim_data", "MSMEs contribute approximately 45% of India's total exports."),
				"cluster_info":  getStringVal(insights, "cluster_info", "Industrial clusters supported by associations are growing in Pune, Bengaluru, Surat, and Coimbatore."),
			}
		}
	}
	if insightsData == nil {
		insightsData = map[string]interface{}{
			"market_stats": "India has over 6.3 crore MSMEs, contributing 30% to GDP and employing 11 crore people.",
			"sector_trends": []interface{}{
				"Accelerated digitization of supply chains and business processes.",
				"Increased integration into global value chains through trade associations.",
				"Government schemes like PLI and Udyam boosting MSME manufacturing.",
			},
			"exim_data":    "MSMEs contribute approximately 45% of India's total exports.",
			"cluster_info": "Industrial clusters supported by associations are growing in Pune, Bengaluru, Surat, and Coimbatore.",
		}
	}
	sections = append(sections, map[string]interface{}{
		"type":    "key_market_insights",
		"enabled": true,
		"data":    insightsData,
	})

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
