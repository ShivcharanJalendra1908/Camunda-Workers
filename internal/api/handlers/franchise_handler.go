package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"
	"camunda-workers/internal/models"

	"github.com/gin-gonic/gin"
	ozzo "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type FranchiseHandler struct {
	camundaClient      *camunda.Client
	logger             logger.Logger
	redisClient        *redis.Client
	validator          *validation.Validator
	sanitizer          *validation.Sanitizer
	internalAlertEmail string
	paginationCfg      config.PaginationConfig
}

func NewFranchiseHandler(
	camundaClient *camunda.Client,
	log logger.Logger,
	redisClient *redis.Client,
	internalEmail string,
	paginationCfg config.PaginationConfig,
) *FranchiseHandler {
	return &FranchiseHandler{
		camundaClient:      camundaClient,
		logger:             log,
		redisClient:        redisClient,
		validator:          validation.NewValidator(),
		sanitizer:          validation.NewSanitizer(),
		internalAlertEmail: internalEmail,
		paginationCfg:      paginationCfg,
	}
}

// ========================================================================
// 🔥 SINGLE WORKFLOW EXECUTION METHOD - WORKS FOR ALL WORKFLOWS
// ========================================================================

func (h *FranchiseHandler) executeWorkflow(
	ctx context.Context,
	processID string,
	variables map[string]interface{},
) (map[string]interface{}, error) {

	correlationKey := variables["correlationKey"].(string)
	channel := fmt.Sprintf("workflow:response:%s", correlationKey)

	// ✅ STEP 1: Subscribe to Redis FIRST (before workflow starts)
	pubsub := h.redisClient.Subscribe(ctx, channel)
	defer pubsub.Close()

	// ✅ STEP 2: Wait for subscription confirmation
	if _, err := pubsub.Receive(ctx); err != nil {
		return nil, fmt.Errorf("failed to confirm subscription: %w", err)
	}

	h.logger.Info("✅ Subscribed to Redis channel BEFORE workflow", map[string]interface{}{
		"channel":        channel,
		"correlationKey": correlationKey,
		"processID":      processID,
	})

	// ✅ STEP 3: NOW start the workflow (subscription is ready)
	instance, err := h.camundaClient.StartProcessInstance(ctx, processID, variables)
	if err != nil {
		return nil, fmt.Errorf("failed to start workflow: %w", err)
	}

	h.logger.Info("Workflow started successfully", map[string]interface{}{
		"processId":      processID,
		"instanceKey":    instance.ProcessInstanceKey,
		"correlationKey": correlationKey,
	})

	// ✅ STEP 4: Wait for response with dual fallback
	responseChan := pubsub.Channel()
	timeoutDuration := 30 * time.Second

	select {
	case msg := <-responseChan:
		var response map[string]interface{}
		if err := json.Unmarshal([]byte(msg.Payload), &response); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}

		h.logger.Info("✅ Received response from Redis", map[string]interface{}{
			"correlationKey": correlationKey,
			"channel":        channel,
			"success":        response["success"],
		})
		return response, nil

	case <-time.After(timeoutDuration):
		// ✅ Fallback: Try cache
		cacheKey := fmt.Sprintf("workflow:response:cache:%s", correlationKey)
		cached, err := h.redisClient.Get(ctx, cacheKey).Result()
		if err == nil {
			var response map[string]interface{}
			if err := json.Unmarshal([]byte(cached), &response); err == nil {
				h.logger.Info("Retrieved from cache after timeout", map[string]interface{}{
					"correlationKey": correlationKey,
				})
				return response, nil
			}
		}

		h.logger.Error("Timeout waiting for workflow response", map[string]interface{}{
			"correlationKey": correlationKey,
			"timeout":        timeoutDuration.String(),
			"processID":      processID,
		})
		return nil, fmt.Errorf("timeout after %v", timeoutDuration)

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ========================================================================
// 📱 HOME PAGE
// ========================================================================

func (h *FranchiseHandler) GetHomePageData(c *gin.Context) {
	ctx := c.Request.Context()

	correlationKey := fmt.Sprintf("home_%s_%d",
		uuid.New().String()[:8],
		time.Now().UnixNano())

	variables := map[string]interface{}{
		"correlationKey": correlationKey,
		"operation":      "home_page",
		"pageType":       "home",
		"lang":           c.GetHeader("X-Lang"),
		"userId":         c.GetString("userId"),
		"deviceType":     c.GetHeader("X-Device-Type"),
		"countryCode":    c.GetHeader("X-Country-Code"),
		"traceId":        c.GetString("traceId"),
		"spanId":         c.GetString("spanId"),
		"requestId":      c.GetString("X-Request-ID"),
		"userAgent":      c.Request.UserAgent(),
		"ipAddress":      c.ClientIP(),
	}

	response, err := h.executeWorkflow(ctx, "franchise-home-page", variables)
	if err != nil {
		h.internalError(c, "Failed to fetch home page data", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// 📋 LISTING PAGE
// ========================================================================

func (h *FranchiseHandler) GetListingPageData(c *gin.Context) {
	ctx := c.Request.Context()
	searchQuery := c.Query("q")
	industrySlug := strings.ToLower(strings.TrimSpace(c.Query("industry")))
	// industrySlug := c.Query("industry")
	// page := 1
	// limit := 12
	page := 1
	pageSize := h.paginationCfg.DefaultPageSize

	if p := c.Query("page"); p != "" {
		if pageNum, err := strconv.Atoi(p); err == nil && pageNum > 0 {
			page = pageNum
		}
	}
	// if l := c.Query("limit"); l != "" {
	// 	if limitNum, err := strconv.Atoi(l); err == nil && limitNum > 0 && limitNum <= 50 {
	// 		limit = limitNum
	// 	}
	// }
	if ps := c.Query("page_size"); ps != "" {
		if psNum, err := strconv.Atoi(ps); err == nil && psNum > 0 {
			if psNum > h.paginationCfg.MaxPageSize {
				psNum = h.paginationCfg.MaxPageSize
			}
			pageSize = psNum
		}
	}

	offset := (page - 1) * pageSize

	// If search query exists, use search workflow
	if searchQuery != "" {
		_ = models.FranchiseSearchFilters{
			Query: searchQuery,
			Page:  page,
			Limit: pageSize,
			// Limit:    limit,
			Category: industrySlug,
		}
		h.SearchFranchises(c)
		return
	}

	// Industry slug required for listing page
	if industrySlug == "" {
		h.validationError(c, "Either search query (q) or industry slug (industry) is required")
		return
	}

	correlationKey := fmt.Sprintf("listing_%s_%d",
		uuid.New().String()[:8],
		time.Now().UnixNano())

	variables := map[string]interface{}{
		"correlationKey": correlationKey,
		"operation":      "listing_page",
		"pageType":       "listing",
		"industrySlug":   industrySlug,
		"page":           page,
		//"limit":          limit,
		"pageSize":  pageSize, // ← "limit" -> "pageSize"
		"offset":    offset,
		"userId":    c.GetString("userId"),
		"lang":      c.GetHeader("X-Lang"),
		"traceId":   c.GetString("traceId"),
		"spanId":    c.GetString("spanId"),
		"requestId": c.GetString("X-Request-ID"),
		"userAgent": c.Request.UserAgent(),
		"ipAddress": c.ClientIP(),
	}

	response, err := h.executeWorkflow(ctx, "franchise-listing-page", variables)
	if err != nil {
		h.internalError(c, "Failed to fetch listing data", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// 📄 DETAIL PAGE
// ========================================================================

func (h *FranchiseHandler) GetFranchiseDetailPage(c *gin.Context) {
	ctx := c.Request.Context()
	slug := c.Param("slug")

	if slug == "" {
		h.validationError(c, "Franchise slug is required")
		return
	}

	// Validate slug
	if err := ozzo.Validate(slug,
		validation.ValidateStringLength(1, 100),
		validation.SafeSQLString,
	); err != nil {
		h.validationError(c, "Invalid franchise slug: "+err.Error())
		return
	}

	correlationKey := fmt.Sprintf("detail_%s_%d",
		uuid.New().String()[:8],
		time.Now().UnixNano())

	variables := map[string]interface{}{
		"correlationKey": correlationKey,
		"operation":      "detail_page",
		"pageType":       "detail",
		"slug":           slug,
		"includeStats":   true,
		"userId":         c.GetString("userId"),
		"lang":           c.GetHeader("X-Lang"),
		"traceId":        c.GetString("traceId"),
		"spanId":         c.GetString("spanId"),
		"requestId":      c.GetString("X-Request-ID"),
		"userAgent":      c.Request.UserAgent(),
		"ipAddress":      c.ClientIP(),
	}

	response, err := h.executeWorkflow(ctx, "franchise-detail-page", variables)
	if err != nil {
		h.internalError(c, "Failed to fetch franchise details", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// 🔍 SEARCH OPERATIONS
// ========================================================================

func (h *FranchiseHandler) SearchFranchises(c *gin.Context) {
	ctx := c.Request.Context()
	searchQuery := c.Query("query")

	var filters models.FranchiseSearchFilters
	if err := c.ShouldBindQuery(&filters); err != nil {
		h.validationError(c, "Invalid search parameters: "+err.Error())
		return
	}

	if searchQuery != "" {
		filters.Query = searchQuery
	}

	h.logger.Info("Search request received", map[string]interface{}{
		"query":    filters.Query,
		"category": filters.Category,
		"rawURL":   c.Request.URL.String(),
	})

	// Validate inputs
	if err := h.validateSearchFilters(&filters); err != nil {
		h.validationError(c, err.Error())
		return
	}

	// Set defaults
	if filters.Page <= 0 {
		filters.Page = 1
	}
	if filters.Limit <= 0 {
		filters.Limit = h.paginationCfg.DefaultPageSize
	}
	if filters.Limit > h.paginationCfg.MaxPageSize {
		filters.Limit = h.paginationCfg.MaxPageSize
	}

	correlationKey := fmt.Sprintf("search_%s_%d",
		uuid.New().String()[:8],
		time.Now().UnixNano())

	variables := map[string]interface{}{
		"correlationKey": correlationKey,
		"operation":      "search_franchises",
		"searchQuery":    filters.Query,
		"filters": map[string]interface{}{
			"query":         filters.Query,
			"category":      filters.Category,
			"location":      filters.Location,
			"minInvestment": filters.MinInvestment,
			"maxInvestment": filters.MaxInvestment,
			"minSpace":      filters.MinSpace,
			"maxSpace":      filters.MaxSpace,
			"minRating":     filters.MinRating,
			"tags":          filters.Tags,
		},
		"page":       filters.Page,
		"limit":      filters.Limit,
		"offset":     (filters.Page - 1) * filters.Limit,
		"userId":     c.GetString("userId"),
		"searchType": "advanced",
		"traceId":    c.GetString("traceId"),
		"spanId":     c.GetString("spanId"),
		"requestId":  c.GetString("X-Request-ID"),
		"userAgent":  c.Request.UserAgent(),
		"ipAddress":  c.ClientIP(),
	}

	response, err := h.executeWorkflow(ctx, "franchise-listing-ai-search", variables)
	if err != nil {
		h.internalError(c, "Failed to search franchises", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (h *FranchiseHandler) SimpleSearch(c *gin.Context) {
	query := c.Query("q")
	page := 1
	limit := 20

	if p := c.Query("page"); p != "" {
		if pageNum, err := strconv.Atoi(p); err == nil && pageNum > 0 {
			page = pageNum
		}
	}
	if l := c.Query("limit"); l != "" {
		if limitNum, err := strconv.Atoi(l); err == nil && limitNum > 0 && limitNum <= 50 {
			limit = limitNum
		}
	}

	if query == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"franchises": []map[string]interface{}{},
				"pagination": gin.H{
					"page":       page,
					"limit":      limit,
					"total":      0,
					"totalPages": 0,
				},
			},
		})
		return
	}

	_ = models.FranchiseSearchFilters{
		Query: query,
		Page:  page,
		Limit: limit,
	}

	// Reuse SearchFranchises logic
	c.Request.URL.RawQuery = fmt.Sprintf("query=%s&page=%d&limit=%d", query, page, limit)
	h.SearchFranchises(c)
}

// ========================================================================
// 🏢 MVP WORKFLOW HELPER (for other operations)
// ========================================================================

func (h *FranchiseHandler) executeMVPWorkflow(
	c *gin.Context,
	operation string,
	variables map[string]interface{},
) (map[string]interface{}, error) {
	ctx := c.Request.Context()

	correlationKey := fmt.Sprintf("mvp_%s_%s_%d",
		operation,
		uuid.New().String()[:8],
		time.Now().UnixNano())

	variables["correlationKey"] = correlationKey
	variables["operation"] = operation
	variables["traceId"] = c.GetString("traceId")
	variables["spanId"] = c.GetString("spanId")
	variables["requestId"] = c.GetString("X-Request-ID")
	variables["userAgent"] = c.Request.UserAgent()
	variables["ipAddress"] = c.ClientIP()

	return h.executeWorkflow(ctx, "franchise-mvp-workflow", variables)
}

// ========================================================================
// 🏢 FRANCHISE OPERATIONS
// ========================================================================

func (h *FranchiseHandler) GetByID(c *gin.Context) {
	franchiseID := c.Param("id")
	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}

	response, err := h.executeMVPWorkflow(c, "get_franchise", map[string]interface{}{
		"franchiseId": franchiseID,
		"userId":      c.GetString("userId"),
		"includeAll":  true,
	})

	if err != nil {
		h.internalError(c, "Failed to fetch franchise", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (h *FranchiseHandler) GetFranchiseOutlets(c *gin.Context) {
	franchiseID := c.Param("id")
	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}

	response, err := h.executeMVPWorkflow(c, "get_franchise_outlets", map[string]interface{}{
		"franchiseId": franchiseID,
		"userId":      c.GetString("userId"),
	})

	if err != nil {
		h.internalError(c, "Failed to fetch franchise outlets", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (h *FranchiseHandler) GetFranchiseVerification(c *gin.Context) {
	franchiseID := c.Param("id")
	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}

	response, err := h.executeMVPWorkflow(c, "get_franchise_verification", map[string]interface{}{
		"franchiseId": franchiseID,
		"userId":      c.GetString("userId"),
	})

	if err != nil {
		h.internalError(c, "Failed to fetch franchise verification", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// 📊 STATISTICS & ANALYTICS
// ========================================================================

func (h *FranchiseHandler) GetStats(c *gin.Context) {
	response, err := h.executeMVPWorkflow(c, "get_stats", map[string]interface{}{
		"userId":    c.GetString("userId"),
		"statsType": "overview",
		"timeRange": c.Query("timeRange"),
		"groupBy":   c.Query("groupBy"),
	})

	if err != nil {
		h.internalError(c, "Failed to fetch statistics", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// 🗂️ CATEGORIES & INDUSTRIES
// ========================================================================

func (h *FranchiseHandler) GetCategories(c *gin.Context) {
	response, err := h.executeMVPWorkflow(c, "get_categories", map[string]interface{}{
		"lang":   c.GetHeader("X-Lang"),
		"userId": c.GetString("userId"),
		"limit":  c.Query("limit"),
		"offset": c.Query("offset"),
	})

	if err != nil {
		h.internalError(c, "Failed to fetch categories", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (h *FranchiseHandler) GetIndustryBySlug(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		h.validationError(c, "Industry slug is required")
		return
	}

	response, err := h.executeMVPWorkflow(c, "get_industry", map[string]interface{}{
		"industrySlug":      slug,
		"lang":              c.GetHeader("X-Lang"),
		"userId":            c.GetString("userId"),
		"includeCategories": c.Query("includeCategories") == "true",
	})

	if err != nil {
		h.internalError(c, "Failed to fetch industry details", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// Use this with postgres :
// func (h *FranchiseHandler) GetAllIndustries(c *gin.Context) {
// 	ctx := c.Request.Context()

// 	correlationKey := fmt.Sprintf("industries_%s_%d",
// 		uuid.New().String()[:8],
// 		time.Now().UnixNano())

// 	variables := map[string]interface{}{
// 		"correlationKey": correlationKey,
// 		"operation":      "get_industries",
// 		// Query level: "industries" / "categories" / "sub-categories"
// 		"level":      c.Query("level"),
// 		"industryId": c.Query("industry_id"),
// 		"categoryId": c.Query("category_id"),
// 		"search":     c.Query("search"),
// 		"withCounts": c.Query("withCounts") == "true",
// 		"activeOnly": c.Query("activeOnly") != "false",
// 		"lang":       c.GetHeader("X-Lang"),
// 		"userId":     c.GetString("userId"),
// 		"traceId":    c.GetString("traceId"),
// 		"spanId":     c.GetString("spanId"),
// 		"requestId":  c.GetString("X-Request-ID"),
// 		"userAgent":  c.Request.UserAgent(),
// 		"ipAddress":  c.ClientIP(),
// 	}

// 	response, err := h.executeWorkflow(ctx, "franchise-industry-browse", variables)
// 	if err != nil {
// 		h.internalError(c, "Failed to fetch industries", err)
// 		return
// 	}

// 	c.JSON(http.StatusOK, response)
// }

func (h *FranchiseHandler) GetAllIndustries(c *gin.Context) {
	ctx := c.Request.Context()

	correlationKey := fmt.Sprintf("industries_%s_%d",
		uuid.New().String()[:8],
		time.Now().UnixNano())

	variables := map[string]interface{}{
		"correlationKey": correlationKey,
		"operation":      "get_industries",
		"search":         c.Query("search"), // ?search=food — optional
		"lang":           c.GetHeader("X-Lang"),
		"userId":         c.GetString("userId"),
		"traceId":        c.GetString("traceId"),
		"spanId":         c.GetString("spanId"),
		"requestId":      c.GetString("X-Request-ID"),
		"userAgent":      c.Request.UserAgent(),
		"ipAddress":      c.ClientIP(),
	}

	response, err := h.executeWorkflow(ctx, "franchise-industry-browse", variables)
	if err != nil {
		h.internalError(c, "Failed to fetch industries", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// ⭐ FEATURED & RECOMMENDED
// ========================================================================

func (h *FranchiseHandler) GetFeatured(c *gin.Context) {
	limit := 10
	if l := c.Query("limit"); l != "" {
		if limitNum, err := strconv.Atoi(l); err == nil && limitNum > 0 && limitNum <= 50 {
			limit = limitNum
		}
	}

	response, err := h.executeMVPWorkflow(c, "get_featured", map[string]interface{}{
		"limit":        limit,
		"userId":       c.GetString("userId"),
		"lang":         c.GetHeader("X-Lang"),
		"featuredType": c.Query("type"),
		"countryCode":  c.GetHeader("X-Country-Code"),
	})

	if err != nil {
		h.internalError(c, "Failed to fetch featured franchises", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (h *FranchiseHandler) GetSuggestions(c *gin.Context) {
	query := c.Query("q")

	if query == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    []string{},
		})
		return
	}

	// Validate query
	if err := ozzo.Validate(query,
		validation.ValidateStringLength(1, 100),
		validation.SafeSQLString,
	); err != nil {
		h.validationError(c, "Invalid query: "+err.Error())
		return
	}

	response, err := h.executeMVPWorkflow(c, "get_suggestions", map[string]interface{}{
		"prefix":         query,
		"userId":         c.GetString("userId"),
		"limit":          10,
		"suggestionType": c.Query("type"),
	})

	if err != nil {
		h.internalError(c, "Failed to get suggestions", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// 👤 USER OPERATIONS
// ========================================================================

func (h *FranchiseHandler) GetUserProfile(c *gin.Context) {
	userID := c.GetString("userId")
	if userID == "" {
		h.validationError(c, "User ID is required")
		return
	}

	response, err := h.executeMVPWorkflow(c, "get_user_profile", map[string]interface{}{
		"userId": userID,
	})

	if err != nil {
		h.internalError(c, "Failed to get user profile", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (h *FranchiseHandler) UpdateUserProfile(c *gin.Context) {
	userID := c.GetString("userId")
	if userID == "" {
		h.validationError(c, "User ID is required")
		return
	}

	var input map[string]interface{}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.validationError(c, "Invalid input: "+err.Error())
		return
	}

	response, err := h.executeMVPWorkflow(c, "update_user_profile", map[string]interface{}{
		"userId":      userID,
		"profileData": input,
	})

	if err != nil {
		h.internalError(c, "Failed to update user profile", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// ❤️ USER FAVORITES
// ========================================================================

func (h *FranchiseHandler) AddToFavorites(c *gin.Context) {
	franchiseID := c.Param("id")
	userID := c.GetString("userId")

	if franchiseID == "" || userID == "" {
		h.validationError(c, "Franchise ID and User ID are required")
		return
	}

	response, err := h.executeMVPWorkflow(c, "add_to_favorites", map[string]interface{}{
		"userId":      userID,
		"franchiseId": franchiseID,
	})

	if err != nil {
		h.internalError(c, "Failed to add to favorites", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (h *FranchiseHandler) RemoveFromFavorites(c *gin.Context) {
	franchiseID := c.Param("id")
	userID := c.GetString("userId")

	if franchiseID == "" || userID == "" {
		h.validationError(c, "Franchise ID and User ID are required")
		return
	}

	response, err := h.executeMVPWorkflow(c, "remove_from_favorites", map[string]interface{}{
		"userId":      userID,
		"franchiseId": franchiseID,
	})

	if err != nil {
		h.internalError(c, "Failed to remove from favorites", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (h *FranchiseHandler) GetFavorites(c *gin.Context) {
	userID := c.GetString("userId")
	if userID == "" {
		h.validationError(c, "User ID is required")
		return
	}

	page := 1
	limit := 20
	if p := c.Query("page"); p != "" {
		if pageNum, err := strconv.Atoi(p); err == nil && pageNum > 0 {
			page = pageNum
		}
	}
	if l := c.Query("limit"); l != "" {
		if limitNum, err := strconv.Atoi(l); err == nil && limitNum > 0 && limitNum <= 50 {
			limit = limitNum
		}
	}

	response, err := h.executeMVPWorkflow(c, "get_favorites", map[string]interface{}{
		"userId": userID,
		"page":   page,
		"limit":  limit,
	})

	if err != nil {
		h.internalError(c, "Failed to get favorites", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// 🔍 SAVED SEARCHES
// ========================================================================

func (h *FranchiseHandler) SaveSearch(c *gin.Context) {
	userID := c.GetString("userId")

	var input struct {
		Name    string                        `json:"name" binding:"required"`
		Filters models.FranchiseSearchFilters `json:"filters" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.validationError(c, "Invalid input: "+err.Error())
		return
	}

	filtersJSON, _ := json.Marshal(input.Filters)

	response, err := h.executeMVPWorkflow(c, "save_search", map[string]interface{}{
		"userId":     userID,
		"searchName": input.Name,
		"filters":    string(filtersJSON),
	})

	if err != nil {
		h.internalError(c, "Failed to save search", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (h *FranchiseHandler) GetSavedSearches(c *gin.Context) {
	userID := c.GetString("userId")

	response, err := h.executeMVPWorkflow(c, "get_saved_searches", map[string]interface{}{
		"userId": userID,
	})

	if err != nil {
		h.internalError(c, "Failed to get saved searches", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (h *FranchiseHandler) DeleteSavedSearch(c *gin.Context) {
	searchID := c.Param("id")
	userID := c.GetString("userId")

	response, err := h.executeMVPWorkflow(c, "delete_saved_search", map[string]interface{}{
		"userId":   userID,
		"searchId": searchID,
	})

	if err != nil {
		h.internalError(c, "Failed to delete saved search", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// 🆕 BATCH OPERATIONS
// ========================================================================

func (h *FranchiseHandler) BatchGetFranchises(c *gin.Context) {
	var request struct {
		FranchiseIDs []string `json:"franchiseIds" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		h.validationError(c, "Invalid request: "+err.Error())
		return
	}

	if len(request.FranchiseIDs) == 0 {
		h.validationError(c, "At least one franchise ID is required")
		return
	}

	if len(request.FranchiseIDs) > 100 {
		h.validationError(c, "Maximum 100 franchise IDs allowed")
		return
	}

	response, err := h.executeMVPWorkflow(c, "batch_get_franchises", map[string]interface{}{
		"franchiseIds": request.FranchiseIDs,
		"userId":       c.GetString("userId"),
	})

	if err != nil {
		h.internalError(c, "Failed to fetch franchises in batch", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// 🏷️ TAGS OPERATIONS
// ========================================================================

func (h *FranchiseHandler) GetPopularTags(c *gin.Context) {
	limit := 20
	if l := c.Query("limit"); l != "" {
		if limitNum, err := strconv.Atoi(l); err == nil && limitNum > 0 && limitNum <= 100 {
			limit = limitNum
		}
	}

	response, err := h.executeMVPWorkflow(c, "get_popular_tags", map[string]interface{}{
		"limit":  limit,
		"userId": c.GetString("userId"),
	})

	if err != nil {
		h.internalError(c, "Failed to fetch popular tags", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// 🏷️ ENQUERY PAGE
// ========================================================================
func (h *FranchiseHandler) SubmitFranchiseEnquiry(c *gin.Context) {
	ctx := c.Request.Context()

	// Franchise ID from URL param
	franchiseID := c.Param("id")
	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}

	// User must be logged in — middleware ne userId set kiya hoga
	userID := c.GetString("userId")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "Authentication required",
			"message": "Please login to submit an enquiry",
		})
		return
	}

	// Parse form body
	var enquiryFormData map[string]interface{}
	if err := c.ShouldBindJSON(&enquiryFormData); err != nil {
		h.validationError(c, "Invalid request body: "+err.Error())
		return
	}

	correlationKey := fmt.Sprintf("enquiry_%s_%d",
		uuid.New().String()[:8],
		time.Now().UnixNano())

	variables := map[string]interface{}{
		"correlationKey":     correlationKey,
		"operation":          "franchise_enquiry",
		"franchiseId":        franchiseID,
		"userId":             userID,
		"enquiryFormData":    enquiryFormData,
		"traceId":            c.GetString("traceId"),
		"spanId":             c.GetString("spanId"),
		"requestId":          c.GetString("X-Request-ID"),
		"userAgent":          c.Request.UserAgent(),
		"ipAddress":          c.ClientIP(),
		"internalAlertEmail": h.internalAlertEmail,
	}

	response, err := h.executeWorkflow(ctx, "franchise-enquiry-submission", variables)
	if err != nil {
		h.internalError(c, "Failed to submit enquiry", err)
		return
	}

	c.JSON(http.StatusCreated, response)
}

// ============================================================
// BOOKMARK ENDPOINTS
// ============================================================

// BookmarkFranchise POST /api/franchises/:id/bookmark
func (h *FranchiseHandler) BookmarkFranchise(c *gin.Context) {
	franchiseID := c.Param("id")
	userID := c.GetString("userId")

	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "Authentication required",
			"message": "Please login to bookmark a franchise",
		})
		return
	}

	response, err := h.executeUserActionWorkflow(c, "add_bookmark", map[string]interface{}{
		"operationType": "ADD_BOOKMARK",
		"userId":        userID,
		"franchiseId":   franchiseID,
	})
	if err != nil {
		h.internalError(c, "Failed to bookmark franchise", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// UnbookmarkFranchise DELETE /api/franchises/:id/bookmark
func (h *FranchiseHandler) UnbookmarkFranchise(c *gin.Context) {
	franchiseID := c.Param("id")
	userID := c.GetString("userId")

	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "Authentication required",
		})
		return
	}

	response, err := h.executeUserActionWorkflow(c, "remove_bookmark", map[string]interface{}{
		"operationType": "REMOVE_BOOKMARK",
		"userId":        userID,
		"franchiseId":   franchiseID,
	})
	if err != nil {
		h.internalError(c, "Failed to remove bookmark", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetUserBookmarks GET /api/user/bookmarks
func (h *FranchiseHandler) GetUserBookmarks(c *gin.Context) {
	userID := c.GetString("userId")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "Authentication required",
		})
		return
	}

	page := 1
	limit := 20
	if p := c.Query("page"); p != "" {
		if pageNum, err := strconv.Atoi(p); err == nil && pageNum > 0 {
			page = pageNum
		}
	}
	if l := c.Query("limit"); l != "" {
		if limitNum, err := strconv.Atoi(l); err == nil && limitNum > 0 && limitNum <= 50 {
			limit = limitNum
		}
	}

	response, err := h.executeUserActionWorkflow(c, "get_bookmarks", map[string]interface{}{
		"operationType": "GET_USER_BOOKMARKS",
		"userId":        userID,
		"page":          page,
		"limit":         limit,
	})
	if err != nil {
		h.internalError(c, "Failed to get bookmarks", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// CheckBookmark GET /api/franchises/:id/bookmark/check
func (h *FranchiseHandler) CheckBookmark(c *gin.Context) {
	franchiseID := c.Param("id")
	userID := c.GetString("userId")

	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}
	if userID == "" {
		// Not logged in — return false without error
		c.JSON(http.StatusOK, gin.H{
			"success":      true,
			"isBookmarked": false,
		})
		return
	}

	response, err := h.executeUserActionWorkflow(c, "check_bookmark", map[string]interface{}{
		"operationType": "CHECK_BOOKMARK",
		"userId":        userID,
		"franchiseId":   franchiseID,
	})
	if err != nil {
		h.internalError(c, "Failed to check bookmark", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ============================================================
// RATING ENDPOINTS
// ============================================================

// RateFranchise POST /api/franchises/:id/rate
func (h *FranchiseHandler) RateFranchise(c *gin.Context) {
	franchiseID := c.Param("id")
	userID := c.GetString("userId")

	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "Authentication required",
			"message": "Please login to rate a franchise",
		})
		return
	}

	var body struct {
		Rating float64 `json:"rating" binding:"required"`
		Review string  `json:"review"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		h.validationError(c, "Invalid request: rating (1.0-5.0) is required")
		return
	}
	if body.Rating < 1.0 || body.Rating > 5.0 {
		h.validationError(c, "Rating must be between 1.0 and 5.0")
		return
	}

	response, err := h.executeUserActionWorkflow(c, "submit_rating", map[string]interface{}{
		"operationType": "SUBMIT_USER_RATING",
		"userId":        userID,
		"franchiseId":   franchiseID,
		"rating":        body.Rating,
		"review":        body.Review,
	})
	if err != nil {
		h.internalError(c, "Failed to submit rating", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// UpdateRating PUT /api/franchises/:id/rate
func (h *FranchiseHandler) UpdateRating(c *gin.Context) {
	franchiseID := c.Param("id")
	userID := c.GetString("userId")

	if franchiseID == "" || userID == "" {
		h.validationError(c, "Franchise ID and authentication required")
		return
	}

	var body struct {
		Rating *float64 `json:"rating"`
		Review *string  `json:"review"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		h.validationError(c, "Invalid request body")
		return
	}
	if body.Rating != nil && (*body.Rating < 1.0 || *body.Rating > 5.0) {
		h.validationError(c, "Rating must be between 1.0 and 5.0")
		return
	}

	vars := map[string]interface{}{
		"operationType": "UPDATE_USER_RATING",
		"userId":        userID,
		"franchiseId":   franchiseID,
	}
	if body.Rating != nil {
		vars["rating"] = *body.Rating
	}
	if body.Review != nil {
		vars["review"] = *body.Review
	}

	response, err := h.executeUserActionWorkflow(c, "update_rating", vars)
	if err != nil {
		h.internalError(c, "Failed to update rating", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// DeleteRating DELETE /api/franchises/:id/rate
func (h *FranchiseHandler) DeleteRating(c *gin.Context) {
	franchiseID := c.Param("id")
	userID := c.GetString("userId")

	if franchiseID == "" || userID == "" {
		h.validationError(c, "Franchise ID and authentication required")
		return
	}

	response, err := h.executeUserActionWorkflow(c, "delete_rating", map[string]interface{}{
		"operationType": "DELETE_USER_RATING",
		"userId":        userID,
		"franchiseId":   franchiseID,
	})
	if err != nil {
		h.internalError(c, "Failed to delete rating", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetFranchiseRatings GET /api/franchises/:id/ratings (public)
func (h *FranchiseHandler) GetFranchiseRatings(c *gin.Context) {
	franchiseID := c.Param("id")
	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}

	page := 1
	limit := 10
	if p := c.Query("page"); p != "" {
		if pageNum, err := strconv.Atoi(p); err == nil && pageNum > 0 {
			page = pageNum
		}
	}
	if l := c.Query("limit"); l != "" {
		if limitNum, err := strconv.Atoi(l); err == nil && limitNum > 0 && limitNum <= 50 {
			limit = limitNum
		}
	}

	response, err := h.executeUserActionWorkflow(c, "get_franchise_ratings", map[string]interface{}{
		"operationType": "GET_FRANCHISE_RATINGS",
		"franchiseId":   franchiseID,
		"page":          page,
		"limit":         limit,
	})
	if err != nil {
		h.internalError(c, "Failed to get ratings", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetUserRating GET /api/franchises/:id/my-rating
func (h *FranchiseHandler) GetUserRating(c *gin.Context) {
	franchiseID := c.Param("id")
	userID := c.GetString("userId")

	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}
	if userID == "" {
		c.JSON(http.StatusOK, gin.H{
			"success":  true,
			"hasRated": false,
		})
		return
	}

	response, err := h.executeUserActionWorkflow(c, "get_user_rating", map[string]interface{}{
		"operationType": "GET_USER_RATING",
		"userId":        userID,
		"franchiseId":   franchiseID,
	})
	if err != nil {
		h.internalError(c, "Failed to get user rating", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ============================================================
// SHARE ENDPOINTS
// ============================================================

// ShareFranchise POST /api/franchises/:id/share (public — no auth needed)
func (h *FranchiseHandler) ShareFranchise(c *gin.Context) {
	franchiseID := c.Param("id")
	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}

	var body struct {
		Platform string `json:"platform"` // whatsapp, twitter, linkedin, email, copy_link
	}
	// body optional — default to copy_link
	_ = c.ShouldBindJSON(&body)
	if body.Platform == "" {
		body.Platform = "copy_link"
	}

	// userID optional — anonymous share allowed
	userID := c.GetString("userId")

	response, err := h.executeUserActionWorkflow(c, "share_franchise", map[string]interface{}{
		"operationType": "SHARE_FRANCHISE",
		"franchiseId":   franchiseID,
		"userId":        userID, // empty string if not logged in
		"sharePlatform": body.Platform,
		"ipAddress":     c.ClientIP(),
	})
	if err != nil {
		h.internalError(c, "Failed to record share", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetUserShares GET /api/user/shares
func (h *FranchiseHandler) GetUserShares(c *gin.Context) {
	userID := c.GetString("userId")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "Authentication required",
		})
		return
	}

	page := 1
	limit := 20
	if p := c.Query("page"); p != "" {
		if pageNum, err := strconv.Atoi(p); err == nil && pageNum > 0 {
			page = pageNum
		}
	}
	if l := c.Query("limit"); l != "" {
		if limitNum, err := strconv.Atoi(l); err == nil && limitNum > 0 && limitNum <= 50 {
			limit = limitNum
		}
	}

	response, err := h.executeUserActionWorkflow(c, "get_user_shares", map[string]interface{}{
		"operationType": "GET_USER_SHARES",
		"userId":        userID,
		"page":          page,
		"limit":         limit,
	})
	if err != nil {
		h.internalError(c, "Failed to get shares", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ============================================================
// HELPER — executeUserActionWorkflow
// Dedicated workflow executor for user actions
// Uses franchise-user-actions BPMN process
// ============================================================
func (h *FranchiseHandler) executeUserActionWorkflow(
	c *gin.Context,
	action string,
	variables map[string]interface{},
) (map[string]interface{}, error) {
	ctx := c.Request.Context()

	correlationKey := fmt.Sprintf("ua_%s_%s_%d",
		action,
		uuid.New().String()[:8],
		time.Now().UnixNano(),
	)

	variables["correlationKey"] = correlationKey
	variables["action"] = action
	variables["traceId"] = c.GetString("traceId")
	variables["spanId"] = c.GetString("spanId")
	variables["requestId"] = c.GetString("X-Request-ID")
	variables["userAgent"] = c.Request.UserAgent()
	variables["ipAddress"] = c.ClientIP()

	return h.executeWorkflow(ctx, "franchise-user-actions", variables)
}

// ========================================================================
// 🔄 HEALTH CHECK
// ========================================================================

func (h *FranchiseHandler) HealthCheck(c *gin.Context) {
	response, err := h.executeMVPWorkflow(c, "health_check", map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})

	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":  "unhealthy",
			"message": "Workflow service unavailable",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// 🛠️ HELPER METHODS
// ========================================================================

func (h *FranchiseHandler) validationError(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{
		"success": false,
		"error":   "Validation error",
		"message": message,
	})
}

func (h *FranchiseHandler) internalError(c *gin.Context, message string, err error) {
	h.logger.Error(message, map[string]interface{}{
		"error": err.Error(),
		"path":  c.Request.URL.Path,
	})
	c.JSON(http.StatusInternalServerError, gin.H{
		"success": false,
		"error":   "Internal server error",
		"message": message,
	})
}

func (h *FranchiseHandler) validateSearchFilters(filters *models.FranchiseSearchFilters) error {
	// Validate Query string
	if filters.Query != "" {
		if err := ozzo.Validate(filters.Query,
			validation.ValidateStringLength(0, 500),
			validation.SafeSQLString,
			validation.SafeNoSQLString,
		); err != nil {
			return fmt.Errorf("invalid query: %s", err.Error())
		}
	}

	// Validate Category
	if filters.Category != "" {
		if err := ozzo.Validate(filters.Category,
			validation.ValidateStringLength(0, 100),
			validation.SafeSQLString,
			validation.SafeNoSQLString,
		); err != nil {
			return fmt.Errorf("invalid category: %s", err.Error())
		}
	}

	// Validate Investment Range
	if filters.MinInvestment < 0 || filters.MinInvestment > 100000000 {
		return fmt.Errorf("invalid minInvestment: must be 0-100000000")
	}
	if filters.MaxInvestment < 0 || filters.MaxInvestment > 100000000 {
		return fmt.Errorf("invalid maxInvestment: must be 0-100000000")
	}

	return nil
}

// ========================================================================
// 🔌 COMPATIBILITY METHODS (Not Used - For Registry Interface)
// ========================================================================

// These methods exist only for backward compatibility with registry
// The new design doesn't use them, but they're kept to avoid breaking changes

func (h *FranchiseHandler) ReceiveWorkflowResponse(correlationKey string, response map[string]interface{}) error {
	// Not used in new Redis pub/sub design
	return nil
}

func (h *FranchiseHandler) PendingResponsesCount() int {
	// Not used in new Redis pub/sub design
	return 0
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////

// // internal/api/handlers/franchise_handler.go
// package handlers

// import (
// 	"bytes"
// 	"context"
// 	"database/sql"
// 	"encoding/json"
// 	"fmt"
// 	"net/http"
// 	"strconv"
// 	"strings"
// 	"time"

// 	"camunda-workers/internal/common/database"
// 	"camunda-workers/internal/common/logger"
// 	"camunda-workers/internal/common/validation"
// 	"camunda-workers/internal/models"

// 	"github.com/gin-gonic/gin"
// 	ozzo "github.com/go-ozzo/ozzo-validation/v4"
// )

// type FranchiseHandler struct {
// 	esClient  *database.ElasticsearchClient
// 	pgClient  *database.PostgresClient
// 	logger    logger.Logger
// 	validator *validation.Validator
// 	sanitizer *validation.Sanitizer
// }

// func NewFranchiseHandler(esClient *database.ElasticsearchClient, pgClient *database.PostgresClient, log logger.Logger) *FranchiseHandler {
// 	return &FranchiseHandler{
// 		esClient:  esClient,
// 		pgClient:  pgClient,
// 		logger:    log,
// 		validator: validation.NewValidator(),
// 		sanitizer: validation.NewSanitizer(),
// 	}
// }

// // Helper function for validation errors
// func (h *FranchiseHandler) validationError(c *gin.Context, message string) {
// 	c.JSON(http.StatusBadRequest, gin.H{
// 		"success": false,
// 		"error":   "Validation error",
// 		"message": message,
// 	})
// }

// // Helper function for not found errors
// func (h *FranchiseHandler) notFoundError(c *gin.Context, message string) {
// 	c.JSON(http.StatusNotFound, gin.H{
// 		"success": false,
// 		"error":   "Not found",
// 		"message": message,
// 	})
// }

// // Helper function for internal errors
// func (h *FranchiseHandler) internalError(c *gin.Context, message string, err error) {
// 	h.logger.Error(message, map[string]interface{}{
// 		"error": err.Error(),
// 	})
// 	c.JSON(http.StatusInternalServerError, gin.H{
// 		"success": false,
// 		"error":   "Internal server error",
// 		"message": message,
// 	})
// }

// // ========================================================================
// // FRANCHISE HOME PAGE
// // ========================================================================

// // GetHomePageData returns all data needed for homepage (MVP Structure)
// func (h *FranchiseHandler) GetHomePageData(c *gin.Context) {
// 	ctx := c.Request.Context()

// 	// Get language from header or query param (default: en)
// 	lang := c.GetHeader("X-Lang")
// 	if lang == "" {
// 		lang = c.DefaultQuery("lang", "en")
// 	}

// 	response := gin.H{
// 		"success":   true,
// 		"timestamp": time.Now().UTC().Format(time.RFC3339),
// 		"data": gin.H{
// 			"pageId":   "franchise_home",
// 			"sections": []gin.H{},
// 		},
// 	}

// 	sections := []gin.H{}

// 	// ========================================================================
// 	// SECTION 1: HERO - Top 9 Brand Logos
// 	// ========================================================================
// 	heroSection := h.getHeroSection(ctx)
// 	sections = append(sections, heroSection)

// 	// ========================================================================
// 	// SECTION 2: TOP FRANCHISE OPPORTUNITIES - 9 Industries
// 	// ========================================================================
// 	industriesSection := h.getTopFranchiseOpportunitiesSection(ctx)
// 	sections = append(sections, industriesSection)

// 	// ========================================================================
// 	// SECTION 3: POPULAR LISTINGS - 12 Franchise Cards
// 	// ========================================================================
// 	popularListingsSection := h.getPopularListingsSection(ctx)
// 	sections = append(sections, popularListingsSection)

// 	// ========================================================================
// 	// SECTION 4: EXPLORE BY CATEGORIES - 30 Categories
// 	// ========================================================================
// 	categoriesSection := h.getExploreByCategoriesSection(ctx)
// 	sections = append(sections, categoriesSection)

// 	response["data"].(gin.H)["sections"] = sections

// 	//c.JSON(http.StatusOK, response)
// 	h.jsonResponse(c, http.StatusOK, response)
// }

// // ============================================================================
// // HELPER METHODS FOR HOME PAGE SECTIONS
// // ============================================================================

// // getHeroSection returns top 9 brand logos
// func (h *FranchiseHandler) getHeroSection(ctx context.Context) gin.H {
// 	// Get top 9 franchises by rating
// 	query := map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"match_all": map[string]interface{}{},
// 		},
// 		"size": 9,
// 		"sort": []map[string]interface{}{
// 			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
// 		},
// 		"_source": []string{"franchise_id", "name", "slug"},
// 	}

// 	result, err := h.esClient.Search(ctx, []string{"franchise_listings"}, query)

// 	topBrandLogos := []gin.H{}

// 	if err == nil {
// 		if hits, ok := result["hits"].(map[string]interface{}); ok {
// 			if hitsList, ok := hits["hits"].([]interface{}); ok {
// 				for _, hit := range hitsList {
// 					if hitMap, ok := hit.(map[string]interface{}); ok {
// 						if source, ok := hitMap["_source"].(map[string]interface{}); ok {
// 							brandID := ""
// 							if id, ok := source["franchise_id"].(string); ok {
// 								brandID = id
// 							} else if id, ok := hitMap["_id"].(string); ok {
// 								brandID = id
// 							}

// 							name, _ := source["name"].(string)
// 							slug, _ := source["slug"].(string)
// 							if slug == "" {
// 								slug = strings.ToLower(strings.ReplaceAll(name, " ", "-"))
// 							}

// 							topBrandLogos = append(topBrandLogos, gin.H{
// 								"brandId": brandID,
// 								"name":    name,
// 								"logo": gin.H{
// 									"url": "/brands/" + slug + "-logo.png",
// 									"alt": name,
// 								},
// 								"slug": slug,
// 							})
// 						}
// 					}
// 				}
// 			}
// 		}
// 	}

// 	return gin.H{
// 		"type":    "hero",
// 		"enabled": true,
// 		"data": gin.H{
// 			"topBrandLogos": topBrandLogos,
// 		},
// 	}
// }

// // getTopFranchiseOpportunitiesSection returns top 9 industries
// func (h *FranchiseHandler) getTopFranchiseOpportunitiesSection(ctx context.Context) gin.H {
// 	query := `
// 		SELECT id, name, slug, icon_name
// 		FROM industries
// 		WHERE is_active = true
// 		ORDER BY display_order
// 		LIMIT 9
// 	`

// 	rows, err := h.pgClient.DB.QueryContext(ctx, query)
// 	industries := []gin.H{}

// 	if err == nil {
// 		defer rows.Close()

// 		for rows.Next() {
// 			var id, name, slug string
// 			var iconName interface{}

// 			if err := rows.Scan(&id, &name, &slug, &iconName); err == nil {
// 				// iconURL := ""
// 				// if iconName != nil {
// 				// 	iconURL = "/icons/industries/" + slug + ".svg"
// 				// }

// 				iconURL := "/icons/industries/" + slug + ".svg"

// 				industries = append(industries, gin.H{
// 					"id":       id,
// 					"name":     name,
// 					"icon_url": iconURL,
// 					"slug":     slug,
// 				})
// 			}
// 		}
// 	}

// 	return gin.H{
// 		"type":    "top_franchise_opportunities",
// 		"enabled": true,
// 		"data": gin.H{
// 			"industries": industries,
// 		},
// 	}
// }

// // getPopularListingsSection returns 12 popular franchise cards
// func (h *FranchiseHandler) getPopularListingsSection(ctx context.Context) gin.H {
// 	query := map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"match_all": map[string]interface{}{},
// 		},
// 		"size": 12,
// 		"sort": []map[string]interface{}{
// 			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
// 			{"_score": map[string]interface{}{"order": "desc"}},
// 		},
// 	}

// 	result, err := h.esClient.Search(ctx, []string{"franchise_listings"}, query)
// 	listings := []gin.H{}

// 	if err == nil {
// 		if hits, ok := result["hits"].(map[string]interface{}); ok {
// 			if hitsList, ok := hits["hits"].([]interface{}); ok {
// 				for _, hit := range hitsList {
// 					if hitMap, ok := hit.(map[string]interface{}); ok {
// 						if source, ok := hitMap["_source"].(map[string]interface{}); ok {
// 							listing := h.formatFranchiseListing(hitMap["_id"], source)
// 							listings = append(listings, listing)
// 						}
// 					}
// 				}
// 			}
// 		}
// 	}

// 	return gin.H{
// 		"type":    "popular_listings",
// 		"enabled": true,
// 		"data":    listings,
// 	}
// }

// // getExploreByCategoriesSection returns 30 categories
// func (h *FranchiseHandler) getExploreByCategoriesSection(ctx context.Context) gin.H {
// 	query := `
// 		SELECT c.id, c.name, c.slug, c.icon_name, i.slug as industry_slug
// 		FROM categories c
// 		INNER JOIN industries i ON c.industry_id = i.id
// 		WHERE c.is_active = true
// 		ORDER BY c.display_order
// 		LIMIT 30
// 	`

// 	rows, err := h.pgClient.DB.QueryContext(ctx, query)
// 	categories := []gin.H{}

// 	if err == nil {
// 		defer rows.Close()

// 		for rows.Next() {
// 			var id, name, slug, industrySlug string
// 			var iconName interface{}

// 			if err := rows.Scan(&id, &name, &slug, &iconName, &industrySlug); err == nil {
// 				// iconURL := ""
// 				// if iconName != nil {
// 				// 	iconURL = "/icons/categories/" + slug + ".svg"
// 				// }
// 				iconURL := "/icons/categories/" + slug + ".svg"

// 				categories = append(categories, gin.H{
// 					"id":       id,
// 					"name":     name,
// 					"icon_url": iconURL,
// 					"slug":     slug,
// 				})
// 			}
// 		}
// 	}

// 	return gin.H{
// 		"type":    "explore_by_categories",
// 		"enabled": true,
// 		"data": gin.H{
// 			"categories": categories,
// 		},
// 	}
// }

// // ============================================================================
// // FIX 1: formatFranchiseListing - Handle nil in space fields
// // ============================================================================
// func (h *FranchiseHandler) formatFranchiseListing(id interface{}, source map[string]interface{}) gin.H {
// 	listing := gin.H{
// 		"id":    id,
// 		"brand": source["name"],
// 	}

// 	if desc, ok := source["description"].(string); ok {
// 		if len(desc) > 200 {
// 			desc = desc[:197] + "..."
// 		}
// 		listing["description"] = desc
// 	}

// 	if industry, ok := source["industry"].(map[string]interface{}); ok {
// 		if industryName, ok := industry["name"].(string); ok {
// 			listing["category"] = industryName
// 		}
// 	}

// 	if year, ok := source["year_of_establishment"].(float64); ok {
// 		listing["year_of_establishment"] = int(year)
// 	}

// 	if rating, ok := source["rating"].(float64); ok {
// 		listing["rating"] = rating
// 	}

// 	if location, ok := source["location"].(string); ok {
// 		listing["location"] = location
// 	}

// 	if tags, ok := source["tags"].([]interface{}); ok {
// 		tagStrings := []string{}
// 		for i, tag := range tags {
// 			if i >= 5 {
// 				break
// 			}
// 			if tagStr, ok := tag.(string); ok {
// 				tagStrings = append(tagStrings, tagStr)
// 			}
// 		}
// 		listing["tags"] = tagStrings
// 	}

// 	// ✅ FIX: Space - Return null if no valid data
// 	if space, ok := source["space"].(map[string]interface{}); ok {
// 		var minSpace, maxSpace interface{} = nil, nil

// 		if ms, exists := space["minSpace"]; exists && ms != nil {
// 			switch v := ms.(type) {
// 			case float64:
// 				minSpace = fmt.Sprintf("%.0f", v)
// 			case int:
// 				minSpace = fmt.Sprintf("%d", v)
// 			case string:
// 				if v != "" {
// 					minSpace = v
// 				}
// 			}
// 		}

// 		if ms, exists := space["maxSpace"]; exists && ms != nil {
// 			switch v := ms.(type) {
// 			case float64:
// 				maxSpace = fmt.Sprintf("%.0f", v)
// 			case int:
// 				maxSpace = fmt.Sprintf("%d", v)
// 			case string:
// 				if v != "" {
// 					maxSpace = v
// 				}
// 			}
// 		}

// 		listing["space"] = gin.H{
// 			"minSpace":  minSpace,
// 			"maxSpace":  maxSpace,
// 			"spaceUnit": "sq ft",
// 		}
// 	}

// 	if outlets, ok := source["total_outlets"].(float64); ok {
// 		listing["no_of_outlets"] = int(outlets)
// 	}

// 	// Investment Range
// 	if investment, ok := source["investmentRange"].(map[string]interface{}); ok {
// 		listing["investmentRange"] = gin.H{
// 			"minInvestment":  investment["minInvestment"],
// 			"maxInvestment":  investment["maxInvestment"],
// 			"investmentUnit": investment["investmentUnit"],
// 		}
// 	}

// 	name, _ := source["name"].(string)
// 	slug, _ := source["slug"].(string)
// 	if slug == "" {
// 		slug = strings.ToLower(strings.ReplaceAll(name, " ", "-"))
// 	}

// 	listing["logo"] = gin.H{
// 		"url": "/brands/" + slug + "-logo.png",
// 		"alt": name,
// 	}
// 	listing["slug"] = slug

// 	if industry, ok := source["industry"].(map[string]interface{}); ok {
// 		if color, ok := industry["color"].(string); ok {
// 			listing["color"] = color
// 		}
// 	}
// 	if _, exists := listing["color"]; !exists {
// 		listing["color"] = "#FF6B6B"
// 	}

// 	return listing
// }

// // ========================================================================
// // FRANCHISE LISTING PAGE
// // ========================================================================

// // GetListingPageData returns all data for the listing page (MVP Structure)
// func (h *FranchiseHandler) GetListingPageData(c *gin.Context) {
// 	ctx := c.Request.Context()

// 	// Get language from header or query param (default: en)
// 	lang := c.GetHeader("X-Lang")
// 	if lang == "" {
// 		lang = c.DefaultQuery("lang", "en")
// 	}

// 	// Get pagination parameters
// 	page := 1
// 	limit := 12
// 	if p := c.Query("page"); p != "" {
// 		if pageNum, err := strconv.Atoi(p); err == nil && pageNum > 0 {
// 			page = pageNum
// 		}
// 	}
// 	if l := c.Query("limit"); l != "" {
// 		if limitNum, err := strconv.Atoi(l); err == nil && limitNum > 0 && limitNum <= 50 {
// 			limit = limitNum
// 		}
// 	}

// 	// Get optional industry filter from query
// 	industrySlug := c.Query("industry")

// 	response := gin.H{
// 		"success":   true,
// 		"timestamp": time.Now().UTC().Format(time.RFC3339),
// 		"data": gin.H{
// 			"pageId":   "franchise_listing",
// 			"sections": []gin.H{},
// 		},
// 	}

// 	sections := []gin.H{}

// 	// ========================================================================
// 	// SECTION 1: HERO - Description based on industry or generic
// 	// ========================================================================
// 	heroSection := h.getListingHeroSection(ctx, industrySlug)
// 	sections = append(sections, heroSection)

// 	// ========================================================================
// 	// SECTION 2: FRANCHISE LISTING - Paginated franchise cards
// 	// ========================================================================
// 	franchiseListingSection := h.getFranchiseListingSection(ctx, industrySlug, page, limit)
// 	sections = append(sections, franchiseListingSection)

// 	// ========================================================================
// 	// SECTION 3: FEATURED CATEGORIES - 8 Categories
// 	// ========================================================================
// 	featuredCategoriesSection := h.getFeaturedCategoriesSection(ctx)
// 	sections = append(sections, featuredCategoriesSection)

// 	// ========================================================================
// 	// SECTION 4: RECOMMENDED FRANCHISES - 4 Recommended items
// 	// ========================================================================
// 	recommendedSection := h.getRecommendedFranchisesSection(ctx, industrySlug)
// 	sections = append(sections, recommendedSection)

// 	// ✅ ADD KEY MARKET INSIGHTS AT ROOT LEVEL (not inside sections)
// 	response["data"].(gin.H)["sections"] = sections
// 	response["data"].(gin.H)["key_market_insights"] = gin.H{
// 		"growth_rate": gin.H{
// 			"title":       "Growth Rate",
// 			"description": "The indoor golf simulator market in India is growing at a CAGR of 17-20%, driven by rising disposable income in premium-experience consumers, urbanization, and tech adoption",
// 		},
// 		"market_trend": gin.H{
// 			"title":       "Market Trend",
// 			"description": "Golf is evolving from an elite outdoor sport to an accessible indoor entertainment and training experience through simulators and golf lounges",
// 		},
// 	}
// 	//c.JSON(http.StatusOK, response)
// 	h.jsonResponse(c, http.StatusOK, response)
// }

// // ============================================================================
// // HELPER METHODS FOR LISTING PAGE SECTIONS
// // ============================================================================

// // getListingHeroSection returns hero with description and category questions
// func (h *FranchiseHandler) getListingHeroSection(ctx context.Context, industrySlug string) gin.H {
// 	description := "Explore top franchise opportunities across various industries"
// 	var categoryQuestions []string

// 	// If industry is specified, get industry-specific description and questions
// 	if industrySlug != "" {
// 		// Get industry ID and description
// 		query := `
// 			SELECT id, listing_description
// 			FROM industries
// 			WHERE slug = $1 AND is_active = true
// 		`
// 		var industryID string
// 		var desc sql.NullString
// 		if err := h.pgClient.DB.QueryRowContext(ctx, query, industrySlug).Scan(&industryID, &desc); err == nil {
// 			if desc.Valid {
// 				description = desc.String
// 			}

// 			// Get category questions for this industry
// 			questionsQuery := `
// 				SELECT question
// 				FROM category_questions
// 				WHERE reference_id = $1
// 				ORDER BY created_at
// 				LIMIT 8
// 			`
// 			rows, err := h.pgClient.DB.QueryContext(ctx, questionsQuery, industryID)
// 			if err == nil {
// 				defer rows.Close()
// 				for rows.Next() {
// 					var question string
// 					if err := rows.Scan(&question); err == nil {
// 						categoryQuestions = append(categoryQuestions, question)
// 					}
// 				}
// 			}
// 		}
// 	}

// 	heroData := gin.H{
// 		"description": description,
// 	}

// 	// Add questions if available
// 	if len(categoryQuestions) > 0 {
// 		heroData["category_questions"] = categoryQuestions
// 	}

// 	return gin.H{
// 		"type":    "hero",
// 		"enabled": true,
// 		"data":    heroData,
// 	}
// }

// // getFranchiseListingSection returns paginated franchise listings
// func (h *FranchiseHandler) getFranchiseListingSection(ctx context.Context, industrySlug string, page, limit int) gin.H {
// 	// Build ES query
// 	query := map[string]interface{}{
// 		"from": (page - 1) * limit,
// 		"size": limit,
// 		"sort": []map[string]interface{}{
// 			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
// 			{"_score": map[string]interface{}{"order": "desc"}},
// 		},
// 	}

// 	// Add industry filter if specified
// 	if industrySlug != "" {
// 		query["query"] = map[string]interface{}{
// 			"term": map[string]interface{}{
// 				"industry.slug": industrySlug,
// 			},
// 		}
// 	} else {
// 		query["query"] = map[string]interface{}{
// 			"match_all": map[string]interface{}{},
// 		}
// 	}

// 	result, err := h.esClient.Search(ctx, []string{"franchise_listings"}, query)
// 	listings := []gin.H{}

// 	if err == nil {
// 		if hits, ok := result["hits"].(map[string]interface{}); ok {
// 			if hitsList, ok := hits["hits"].([]interface{}); ok {
// 				for _, hit := range hitsList {
// 					if hitMap, ok := hit.(map[string]interface{}); ok {
// 						if source, ok := hitMap["_source"].(map[string]interface{}); ok {
// 							listing := h.formatFranchiseListing(hitMap["_id"], source)
// 							listings = append(listings, listing)
// 						}
// 					}
// 				}
// 			}
// 		}
// 	}

// 	return gin.H{
// 		"type":    "franchise_listing",
// 		"enabled": true,
// 		"data":    listings,
// 	}
// }

// // getFeaturedCategoriesSection returns 8 featured categories
// func (h *FranchiseHandler) getFeaturedCategoriesSection(ctx context.Context) gin.H {
// 	query := `
// 		SELECT c.id, c.name, c.slug, c.icon_name
// 		FROM categories c
// 		WHERE c.is_active = true
// 		ORDER BY c.display_order
// 		LIMIT 8
// 	`

// 	rows, err := h.pgClient.DB.QueryContext(ctx, query)
// 	categories := []gin.H{}

// 	if err == nil {
// 		defer rows.Close()

// 		for rows.Next() {
// 			var id, name, slug string
// 			var iconName sql.NullString

// 			if err := rows.Scan(&id, &name, &slug, &iconName); err == nil {
// 				// iconURL := ""
// 				// if iconName.Valid {
// 				// 	iconURL = "/icons/categories/" + slug + ".svg"
// 				// }
// 				iconURL := "/icons/categories/" + slug + ".svg"

// 				categories = append(categories, gin.H{
// 					"id":       id,
// 					"name":     name,
// 					"icon_url": iconURL,
// 					"slug":     slug,
// 				})
// 			}
// 		}
// 	}

// 	return gin.H{
// 		"type":    "featured_categories",
// 		"enabled": true,
// 		"data":    categories,
// 	}
// }

// // getRecommendedFranchisesSection returns 4 recommended franchises with market insights
// func (h *FranchiseHandler) getRecommendedFranchisesSection(ctx context.Context, industrySlug string) gin.H {
// 	// Build query - get top 4 by rating
// 	query := map[string]interface{}{
// 		"size": 4,
// 		"sort": []map[string]interface{}{
// 			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
// 		},
// 		"_source": []string{"franchise_id", "name", "slug", "industry"},
// 	}

// 	// If industry specified, get from same industry, else random
// 	if industrySlug != "" {
// 		query["query"] = map[string]interface{}{
// 			"term": map[string]interface{}{
// 				"industry.slug": industrySlug,
// 			},
// 		}
// 	} else {
// 		query["query"] = map[string]interface{}{
// 			"match_all": map[string]interface{}{},
// 		}
// 	}

// 	result, err := h.esClient.Search(ctx, []string{"franchise_listings"}, query)
// 	items := []gin.H{}

// 	if err == nil {
// 		if hits, ok := result["hits"].(map[string]interface{}); ok {
// 			if hitsList, ok := hits["hits"].([]interface{}); ok {
// 				for _, hit := range hitsList {
// 					if hitMap, ok := hit.(map[string]interface{}); ok {
// 						if source, ok := hitMap["_source"].(map[string]interface{}); ok {
// 							id := ""
// 							if fID, ok := source["franchise_id"].(string); ok {
// 								id = fID
// 							} else if docID, ok := hitMap["_id"].(string); ok {
// 								id = docID
// 							}

// 							name, _ := source["name"].(string)
// 							slug, _ := source["slug"].(string)

// 							industryName := ""
// 							if industry, ok := source["industry"].(map[string]interface{}); ok {
// 								if iName, ok := industry["name"].(string); ok {
// 									industryName = iName
// 								}
// 							}

// 							items = append(items, gin.H{
// 								"id":       id,
// 								"brand":    name,
// 								"industry": industryName,
// 								"image": gin.H{
// 									"url": "/brands/" + slug + "-banner.jpg",
// 									"alt": name,
// 								},
// 								"slug": slug,
// 							})
// 						}
// 					}
// 				}
// 			}
// 		}
// 	}

// 	return gin.H{
// 		"type":    "recommended_franchises",
// 		"enabled": true,
// 		"data": gin.H{
// 			"items": items,
// 		},
// 	}
// }

// /////////////////////////////////////////////////////////////////////////////////////////////

// // ========================================================================
// // FRANCHISE DETAIL PAGE
// // ========================================================================
// // GetFranchiseDetailPage returns complete franchise detail page data
// func (h *FranchiseHandler) GetFranchiseDetailPage(c *gin.Context) {
// 	ctx := c.Request.Context()
// 	slug := c.Param("slug")

// 	if slug == "" {
// 		h.validationError(c, "Franchise slug is required")
// 		return
// 	}

// 	// ========================================================================
// 	// Get Franchise Basic Info from Elasticsearch
// 	// ========================================================================
// 	franchiseData, err := h.getFranchiseBySlug(ctx, slug)
// 	if err != nil {
// 		h.notFoundError(c, "Franchise not found")
// 		return
// 	}

// 	franchiseID := franchiseData["franchise_id"].(string)

// 	// ========================================================================
// 	// SECTION 1: HERO/HEADER - Brand Info & Contact
// 	// ========================================================================
// 	hero := h.getFranchiseHeroSection(ctx, franchiseID, franchiseData)["data"]

// 	// ========================================================================
// 	// SECTION 2: FRANCHISING OVERVIEW - Key Details
// 	// ========================================================================
// 	overview := h.getFranchisingOverviewSection(ctx, franchiseID, franchiseData)["data"]

// 	// ========================================================================
// 	// SECTION 3: BUSINESS OVERVIEW - Products & Services
// 	// ========================================================================
// 	business := h.getBusinessOverviewSection(ctx, franchiseID)["data"]

// 	// ========================================================================
// 	// SECTION 4: INVESTMENT REQUIREMENT - Financials
// 	// ========================================================================
// 	investment := h.getInvestmentRequirementSection(ctx, franchiseID, franchiseData)["data"]

// 	// ========================================================================
// 	// SECTION 5: OPERATIONS - Space, Training, Support
// 	// ========================================================================
// 	operations := h.getOperationsSection(ctx, franchiseID, franchiseData)["data"]

// 	// ========================================================================
// 	// SECTION 6: FEATURED CATEGORIES - Related Categories
// 	// ========================================================================
// 	categories := h.getFeaturedCategoriesForFranchise(ctx, franchiseData)["data"]

// 	// ========================================================================
// 	// SECTION 7: RECOMMENDED FRANCHISES - Similar Franchises
// 	// ========================================================================
// 	recommended := h.getRecommendedForFranchise(ctx, franchiseData)["data"]

// 	// ========================================================================
// 	// SECTION 8: KEY MARKET INSIGHTS - From industry data
// 	// ========================================================================
// 	insights := h.getMarketInsightsSection(ctx, franchiseData)["data"]

// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		"success": true,
// 		"data": gin.H{
// 			"franchiseId":          franchiseID,
// 			"slug":                 slug,
// 			"basicInfo":            hero,
// 			"franchising_overview": overview,
// 			"business_overview":    business,
// 			"investment_details":   investment,
// 			"operation":            operations,
// 			"featured_categories":  categories,
// 			"recommended":          recommended,
// 			"key_market_insights":  insights,
// 		},
// 	})
// }

// // ============================================================================
// // HELPER METHODS FOR FRANCHISE DETAIL PAGE
// // ============================================================================

// // getFranchiseBySlug gets franchise from Elasticsearch by slug
// func (h *FranchiseHandler) getFranchiseBySlug(ctx context.Context, slug string) (map[string]interface{}, error) {
// 	query := map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"term": map[string]interface{}{
// 				"slug": slug,
// 			},
// 		},
// 		"size": 1,
// 	}

// 	result, err := h.esClient.Search(ctx, []string{"franchise_listings"}, query)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if hits, ok := result["hits"].(map[string]interface{}); ok {
// 		if hitsList, ok := hits["hits"].([]interface{}); ok && len(hitsList) > 0 {
// 			if hitMap, ok := hitsList[0].(map[string]interface{}); ok {
// 				if source, ok := hitMap["_source"].(map[string]interface{}); ok {
// 					source["_id"] = hitMap["_id"]
// 					return source, nil
// 				}
// 			}
// 		}
// 	}

// 	return nil, fmt.Errorf("franchise not found")
// }

// // getFranchiseHeroSection returns hero/header section from DB
// func (h *FranchiseHandler) getFranchiseHeroSection(ctx context.Context, franchiseID string, franchiseData map[string]interface{}) gin.H {
// 	name, _ := franchiseData["name"].(string)
// 	slug, _ := franchiseData["slug"].(string)
// 	description, _ := franchiseData["description"].(string)

// 	// Get rating from ES
// 	rating := 0.0
// 	if r, ok := franchiseData["rating"].(float64); ok {
// 		rating = r
// 	}

// 	// ✅ Get social links from database (CORRECT schema)
// 	var socialLinks []gin.H
// 	socialQuery := `
//     SELECT
//         instagram_url,
//         facebook_url,
//         twitter_url,
//         linkedin_url
//     FROM franchise_social_links
//     WHERE franchise_id = $1
// `

// 	var instagram, facebook, twitter, linkedin sql.NullString
// 	err := h.pgClient.DB.QueryRowContext(ctx, socialQuery, franchiseID).Scan(
// 		&instagram, &facebook, &twitter, &linkedin,
// 	)

// 	if err == nil {
// 		if instagram.Valid && instagram.String != "" {
// 			socialLinks = append(socialLinks, gin.H{
// 				"platform": "instagram",
// 				"url":      instagram.String,
// 			})
// 		}
// 		if facebook.Valid && facebook.String != "" {
// 			socialLinks = append(socialLinks, gin.H{
// 				"platform": "facebook",
// 				"url":      facebook.String,
// 			})
// 		}
// 		if twitter.Valid && twitter.String != "" {
// 			socialLinks = append(socialLinks, gin.H{
// 				"platform": "twitter",
// 				"url":      twitter.String,
// 			})
// 		}
// 		if linkedin.Valid && linkedin.String != "" {
// 			socialLinks = append(socialLinks, gin.H{
// 				"platform": "linkedin",
// 				"url":      linkedin.String,
// 			})
// 		}
// 	}

// 	// ✅ If no social links, return empty array instead of null
// 	if socialLinks == nil {
// 		socialLinks = []gin.H{}
// 	}

// 	// ✅ Get gallery images from database (if you have a table for this)
// 	// For now, using brand images as gallery
// 	gallery := []gin.H{
// 		{"url": "/brands/" + slug + "-gallery-1.jpg", "alt": name + " interior"},
// 		{"url": "/brands/" + slug + "-gallery-2.jpg", "alt": name + " exterior"},
// 		{"url": "/brands/" + slug + "-gallery-3.jpg", "alt": name + " products"},
// 	}

// 	return gin.H{
// 		"type":    "hero",
// 		"enabled": true,
// 		"data": gin.H{
// 			"name":          name,
// 			"slug":          slug,
// 			"logo":          "/brands/" + slug + "-logo.png",
// 			"description":   description,
// 			"rating":        rating,
// 			"gallery":       gallery,
// 			"socialLinks":   socialLinks,
// 			"verifiedBadge": true,
// 		},
// 	}
// }

// // ============================================================================
// // FIX 2: getFranchisingOverviewSection - Add missing avgTurnoverPerMonth
// // ============================================================================
// func (h *FranchiseHandler) getFranchisingOverviewSection(ctx context.Context, franchiseID string, franchiseData map[string]interface{}) gin.H {
// 	industry := franchiseData["industry"].(map[string]interface{})
// 	industryName, _ := industry["name"].(string)

// 	yearEstablished := 0
// 	if year, ok := franchiseData["year_of_establishment"].(float64); ok {
// 		yearEstablished = int(year)
// 	}

// 	totalOutlets := 0
// 	if outlets, ok := franchiseData["total_outlets"].(float64); ok {
// 		totalOutlets = int(outlets)
// 	}

// 	investmentMin := ""
// 	investmentMax := ""
// 	if invRange, ok := franchiseData["investmentRange"].(map[string]interface{}); ok {
// 		if min, ok := invRange["minInvestment"].(string); ok {
// 			investmentMin = min
// 		}
// 		if max, ok := invRange["maxInvestment"].(string); ok {
// 			investmentMax = max
// 		}
// 	}

// 	// ✅ Get space with proper nil handling
// 	var spaceMin, spaceMax interface{} = nil, nil
// 	if space, ok := franchiseData["space"].(map[string]interface{}); ok {
// 		if ms, exists := space["minSpace"]; exists && ms != nil {
// 			switch v := ms.(type) {
// 			case float64:
// 				spaceMin = fmt.Sprintf("%.0f sq ft", v)
// 			case int:
// 				spaceMin = fmt.Sprintf("%d sq ft", v)
// 			case string:
// 				if v != "" {
// 					spaceMin = v + " sq ft"
// 				}
// 			}
// 		}

// 		if ms, exists := space["maxSpace"]; exists && ms != nil {
// 			switch v := ms.(type) {
// 			case float64:
// 				spaceMax = fmt.Sprintf("%.0f sq ft", v)
// 			case int:
// 				spaceMax = fmt.Sprintf("%d sq ft", v)
// 			case string:
// 				if v != "" {
// 					spaceMax = v + " sq ft"
// 				}
// 			}
// 		}
// 	}

// 	var contactEmail, parentCompany, leaderName, leaderRole, city, businessType sql.NullString
// 	var franchiseFee, royaltyPercentage, monthlyTurnoverMin, monthlyTurnoverMax sql.NullFloat64
// 	var spaceMinSqft, spaceMaxSqft sql.NullInt32

// 	detailQuery := `
// 		SELECT
// 			f.contact_email,
// 			f.parent_company,
// 			f.business_type,
// 			f.leader_name,
// 			f.leader_role,
// 			fc.city,
// 			fir.franchise_fee,
// 			fir.royalty_percentage,
// 			fir.monthly_turnover_min,
// 			fir.monthly_turnover_max,
// 			fo.space_min_sqft,
// 			fo.space_max_sqft
// 		FROM franchises f
// 		LEFT JOIN franchise_investment_requirement fir ON f.id = fir.franchise_id
// 		LEFT JOIN franchise_operations fo ON f.id = fo.franchise_id
// 		LEFT JOIN franchise_cities fc ON f.id = fc.franchise_id
// 		WHERE f.id = $1
// 		LIMIT 1
// 	`

// 	err := h.pgClient.DB.QueryRowContext(ctx, detailQuery, franchiseID).Scan(
// 		&contactEmail, &parentCompany, &businessType, &leaderName, &leaderRole,
// 		&city,
// 		&franchiseFee, &royaltyPercentage, &monthlyTurnoverMin, &monthlyTurnoverMax,
// 		&spaceMinSqft, &spaceMaxSqft,
// 	)

// 	if err != nil {
// 		h.logger.Warn("Failed to fetch franchise details", map[string]interface{}{
// 			"franchise_id": franchiseID,
// 			"error":        err.Error(),
// 		})
// 	}

// 	overview := gin.H{
// 		"initialInvestment": gin.H{
// 			"min":  investmentMin,
// 			"max":  investmentMax,
// 			"unit": "Lakhs",
// 		},
// 		"industry":     industryName,
// 		"totalOutlets": totalOutlets,
// 		"unitEstd":     yearEstablished,
// 	}

// 	// ✅ Space requirement - only if we have values
// 	if spaceMin != nil || spaceMax != nil {
// 		overview["spaceRequirement"] = gin.H{
// 			"min": spaceMin,
// 			"max": spaceMax,
// 		}
// 	}

// 	if parentCompany.Valid && parentCompany.String != "" {
// 		overview["parentCompany"] = parentCompany.String
// 	}

// 	if businessType.Valid && businessType.String != "" {
// 		overview["businessType"] = businessType.String
// 	} else {
// 		overview["businessType"] = "Franchise"
// 	}

// 	if leaderName.Valid && leaderName.String != "" {
// 		leadership := leaderName.String
// 		if leaderRole.Valid && leaderRole.String != "" {
// 			leadership = leaderName.String + " (" + leaderRole.String + ")"
// 		}
// 		overview["leadership"] = leadership
// 	}

// 	if contactEmail.Valid && contactEmail.String != "" {
// 		overview["email"] = contactEmail.String
// 	}

// 	if franchiseFee.Valid {
// 		overview["franchiseFees"] = fmt.Sprintf("₹%.1f Lakhs", franchiseFee.Float64/100000)
// 	}

// 	var contractDuration sql.NullString
// 	h.pgClient.DB.QueryRowContext(ctx,
// 		`SELECT contract_duration FROM franchise_stats WHERE franchise_id = $1`,
// 		franchiseID,
// 	).Scan(&contractDuration)

// 	if contractDuration.Valid && contractDuration.String != "" {
// 		overview["termDurationYear"] = contractDuration.String
// 	} else {
// 		overview["termDurationYear"] = "5 years"
// 	}

// 	if royaltyPercentage.Valid {
// 		overview["royalties"] = fmt.Sprintf("%.1f%%", royaltyPercentage.Float64)
// 	}

// 	// ✅ FIX: Add avgTurnoverPerMonth
// 	if monthlyTurnoverMin.Valid && monthlyTurnoverMax.Valid {
// 		minLakhs := monthlyTurnoverMin.Float64 / 100000
// 		maxLakhs := monthlyTurnoverMax.Float64 / 100000
// 		overview["avgTurnoverPerMonth"] = fmt.Sprintf("₹%.0f-%.0f Lakhs", minLakhs, maxLakhs)
// 	}

// 	if city.Valid && city.String != "" {
// 		overview["city"] = city.String
// 	}

// 	return gin.H{
// 		"type":    "franchising_overview",
// 		"enabled": true,
// 		"data":    overview,
// 	}
// }

// // getBusinessOverviewSection returns business overview with products/services from DB
// func (h *FranchiseHandler) getBusinessOverviewSection(ctx context.Context, franchiseID string) gin.H {
// 	query := `
// 		SELECT products, services
// 		FROM franchise_business_overview
// 		WHERE franchise_id = $1
// 	`

// 	var productsJSON, servicesJSON []byte
// 	var products, services []string

// 	if err := h.pgClient.DB.QueryRowContext(ctx, query, franchiseID).Scan(&productsJSON, &servicesJSON); err == nil {
// 		json.Unmarshal(productsJSON, &products)
// 		json.Unmarshal(servicesJSON, &services)
// 	}

// 	return gin.H{
// 		"type":    "business_overview",
// 		"enabled": true,
// 		"data": gin.H{
// 			"products": products,
// 			"services": services,
// 		},
// 	}
// }

// // getInvestmentRequirementSection returns investment details from DB or ES fallback
// func (h *FranchiseHandler) getInvestmentRequirementSection(ctx context.Context, franchiseID string, franchiseData map[string]interface{}) gin.H {
// 	query := `
// 		SELECT
// 			initial_investment_min,
// 			initial_investment_max,
// 			franchise_fee,
// 			royalty_fee,
// 			marketing_fee,
// 			working_capital,
// 			equipment_cost,
// 			inventory_cost
// 		FROM franchise_investment_requirement
// 		WHERE franchise_id = $1
// 	`

// 	var minInv, maxInv, franchiseFee, royaltyFee, marketingFee, workingCapital, equipmentCost, inventoryCost sql.NullFloat64

// 	err := h.pgClient.DB.QueryRowContext(ctx, query, franchiseID).Scan(
// 		&minInv, &maxInv, &franchiseFee, &royaltyFee, &marketingFee, &workingCapital, &equipmentCost, &inventoryCost,
// 	)

// 	investment := gin.H{}

// 	// ✅ If PostgreSQL data available, use it
// 	if err == nil && (minInv.Valid || maxInv.Valid) {
// 		if minInv.Valid || maxInv.Valid {

// 			// Convert ₹ → Lakhs
// 			minInv := minInv.Float64 / 100000
// 			maxInv := maxInv.Float64 / 100000

// 			investment["totalInvestment"] = gin.H{
// 				"min":  fmt.Sprintf("%.1f", minInv),
// 				"max":  fmt.Sprintf("%.1f", maxInv),
// 				"unit": "Lakhs",
// 			}
// 		}
// 		if franchiseFee.Valid {
// 			investment["franchiseFee"] = fmt.Sprintf("₹%.1f Lakhs", franchiseFee.Float64/100000)
// 		}
// 		if royaltyFee.Valid {
// 			investment["royaltyFee"] = fmt.Sprintf("%.0f%%", royaltyFee.Float64)
// 		}
// 		if marketingFee.Valid {
// 			investment["marketingFee"] = fmt.Sprintf("%.0f%%", marketingFee.Float64)
// 		}
// 		if workingCapital.Valid {
// 			investment["workingCapital"] = fmt.Sprintf("₹%.1f Lakhs", workingCapital.Float64/100000)
// 		}
// 		if equipmentCost.Valid {
// 			investment["equipmentCost"] = fmt.Sprintf("₹%.1f Lakhs", equipmentCost.Float64/100000)
// 		}
// 		if inventoryCost.Valid {
// 			investment["inventoryCost"] = fmt.Sprintf("₹%.1f Lakhs", inventoryCost.Float64/100000)
// 		}
// 	} else {
// 		// ✅ FALLBACK: Get from Elasticsearch if PostgreSQL doesn't have data
// 		if invRange, ok := franchiseData["investmentRange"].(map[string]interface{}); ok {
// 			investment["totalInvestment"] = gin.H{
// 				"min":  invRange["minInvestment"],
// 				"max":  invRange["maxInvestment"],
// 				"unit": invRange["investmentUnit"],
// 			}
// 		}
// 	}

// 	return gin.H{
// 		"type":    "investment_requirement",
// 		"enabled": true,
// 		"data":    investment,
// 	}
// }

// // ============================================================================
// // FIX 3: getOperationsSection - Add fallback for empty training
// // ============================================================================
// func (h *FranchiseHandler) getOperationsSection(ctx context.Context, franchiseID string, franchiseData map[string]interface{}) gin.H {
// 	query := `
// 		SELECT
// 			space_min_sqft,
// 			space_max_sqft,
// 			staff_required_min,
// 			staff_required_max,
// 			training_provided,
// 			training_details,
// 			marketing_support
// 		FROM franchise_operations
// 		WHERE franchise_id = $1
// 	`

// 	var minSpace, maxSpace, minStaff, maxStaff sql.NullInt32
// 	var trainingProvided sql.NullBool
// 	var trainingDetails, marketingSupport sql.NullString

// 	err := h.pgClient.DB.QueryRowContext(ctx, query, franchiseID).Scan(
// 		&minSpace, &maxSpace, &minStaff, &maxStaff,
// 		&trainingProvided, &trainingDetails, &marketingSupport,
// 	)

// 	operations := gin.H{}

// 	if err == nil && (minSpace.Valid || maxSpace.Valid) {
// 		if minSpace.Valid || maxSpace.Valid {
// 			operations["spaceRequired"] = gin.H{
// 				"min":  minSpace.Int32,
// 				"max":  maxSpace.Int32,
// 				"unit": "sq ft",
// 			}
// 		}

// 		// ✅ FIX: Always provide training object with fallback
// 		trainingData := gin.H{}

// 		if trainingDetails.Valid && trainingDetails.String != "" {
// 			trainingData["description"] = trainingDetails.String
// 		} else {
// 			// ✅ Fallback description
// 			trainingData["description"] = "Training provided as per franchise requirements"
// 		}

// 		if marketingSupport.Valid && marketingSupport.String != "" {
// 			trainingData["support"] = marketingSupport.String
// 		} else {
// 			// ✅ Fallback support info
// 			trainingData["support"] = "Marketing and operational support available"
// 		}

// 		operations["training"] = trainingData

// 		if minStaff.Valid || maxStaff.Valid {
// 			operations["staffRequired"] = gin.H{
// 				"min": minStaff.Int32,
// 				"max": maxStaff.Int32,
// 			}
// 		}
// 	} else {
// 		// ✅ FALLBACK: Get from ES if PostgreSQL doesn't have data
// 		if space, ok := franchiseData["space"].(map[string]interface{}); ok {
// 			if minS, ok := space["minSpace"].(float64); ok {
// 				operations["spaceRequired"] = gin.H{
// 					"min":  int(minS),
// 					"max":  int(space["maxSpace"].(float64)),
// 					"unit": "sq ft",
// 				}
// 			}
// 		}

// 		// ✅ Always provide training fallback
// 		operations["training"] = gin.H{
// 			"description": "Comprehensive training and ongoing support provided to franchisees",
// 			"support":     "Marketing and operational support available",
// 		}
// 	}

// 	return gin.H{
// 		"type":    "operations",
// 		"enabled": true,
// 		"data":    operations,
// 	}
// }

// // ============================================================================
// // FIX 4: getFeaturedCategoriesForFranchise - Handle empty questions gracefully
// // ============================================================================
// func (h *FranchiseHandler) getFeaturedCategoriesForFranchise(ctx context.Context, franchiseData map[string]interface{}) gin.H {
// 	industry := franchiseData["industry"].(map[string]interface{})
// 	industryID, _ := industry["id"].(string)
// 	industryName, _ := industry["name"].(string)

// 	categoriesQuery := `
// 		SELECT c.id, c.name, c.slug
// 		FROM categories c
// 		WHERE c.industry_id = $1 AND c.is_active = true
// 		ORDER BY c.display_order
// 		LIMIT 8
// 	`

// 	rows, err := h.pgClient.DB.QueryContext(ctx, categoriesQuery, industryID)
// 	var categories []gin.H

// 	if err == nil {
// 		defer rows.Close()
// 		for rows.Next() {
// 			var id, name, slug string
// 			if err := rows.Scan(&id, &name, &slug); err == nil {
// 				categories = append(categories, gin.H{
// 					"id":       id,
// 					"name":     name,
// 					"image":    "/categories/" + slug + ".jpg",
// 					"icon_url": "/icons/categories/" + slug + ".svg",
// 					"slug":     slug,
// 				})
// 			}
// 		}
// 	}

// 	questionsQuery := `
// 		SELECT question
// 		FROM category_questions
// 		WHERE reference_id = $1
// 		ORDER BY created_at
// 		LIMIT 8
// 	`

// 	qRows, err := h.pgClient.DB.QueryContext(ctx, questionsQuery, industryID)
// 	var questions []string

// 	if err == nil {
// 		defer qRows.Close()
// 		for qRows.Next() {
// 			var question string
// 			if err := qRows.Scan(&question); err == nil {
// 				questions = append(questions, question)
// 			}
// 		}
// 	}

// 	// ✅ FIX: If no questions, provide empty array (not null)
// 	if questions == nil {
// 		questions = []string{}
// 	}

// 	return gin.H{
// 		"type":    "featured_categories",
// 		"enabled": true,
// 		"data": gin.H{
// 			"title":      "Featured " + industryName + " franchise categories",
// 			"categories": categories,
// 			"questions":  questions, // ✅ Will be [] if empty, not null
// 		},
// 	}
// }

// // getRecommendedForFranchise returns similar franchises from ES
// func (h *FranchiseHandler) getRecommendedForFranchise(ctx context.Context, franchiseData map[string]interface{}) gin.H {
// 	industry := franchiseData["industry"].(map[string]interface{})
// 	industrySlug, _ := industry["slug"].(string)
// 	currentFranchiseID, _ := franchiseData["franchise_id"].(string)

// 	// ✅ Get 4 similar franchises from same industry, excluding current one
// 	query := map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"bool": map[string]interface{}{
// 				"must": []map[string]interface{}{
// 					{
// 						"term": map[string]interface{}{
// 							"industry.slug": industrySlug,
// 						},
// 					},
// 				},
// 				"must_not": []map[string]interface{}{
// 					{
// 						"term": map[string]interface{}{
// 							"franchise_id": currentFranchiseID,
// 						},
// 					},
// 				},
// 			},
// 		},
// 		"size": 4,
// 		"sort": []map[string]interface{}{
// 			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
// 		},
// 		"_source": []string{"franchise_id", "name", "slug", "industry"},
// 	}

// 	result, _ := h.esClient.Search(ctx, []string{"franchise_listings"}, query)
// 	items := []gin.H{}

// 	if hits, ok := result["hits"].(map[string]interface{}); ok {
// 		if hitsList, ok := hits["hits"].([]interface{}); ok {
// 			for _, hit := range hitsList {
// 				if hitMap, ok := hit.(map[string]interface{}); ok {
// 					if source, ok := hitMap["_source"].(map[string]interface{}); ok {
// 						name, _ := source["name"].(string)
// 						slug, _ := source["slug"].(string)

// 						items = append(items, gin.H{
// 							"name":  name,
// 							"image": "/brands/" + slug + "-card.jpg",
// 							"slug":  slug,
// 						})
// 					}
// 				}
// 			}
// 		}
// 	}

// 	return gin.H{
// 		"type":    "recommended_franchises",
// 		"enabled": true,
// 		"data": gin.H{
// 			"title": "Recommended Franchises Just for You",
// 			"items": items,
// 		},
// 	}
// }

// // getMarketInsightsSection returns market insights from Elasticsearch
// func (h *FranchiseHandler) getMarketInsightsSection(ctx context.Context, franchiseData map[string]interface{}) gin.H {
// 	// ✅ Get industry info
// 	industry := franchiseData["industry"].(map[string]interface{})
// 	industryID, _ := industry["id"].(string)

// 	// ✅ Get market insights from franchise_industries index in ES
// 	query := map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"term": map[string]interface{}{
// 				"industry_id": industryID,
// 			},
// 		},
// 		"size": 1,
// 	}

// 	result, err := h.esClient.Search(ctx, []string{"franchise_industries"}, query)

// 	marketTrend := gin.H{
// 		"title":       "Market Trend",
// 		"description": "This industry is experiencing steady growth with increasing consumer demand.",
// 	}

// 	growthRate := gin.H{
// 		"title":       "Growth Rate",
// 		"description": "The market is growing at a healthy pace driven by urbanization and changing consumer preferences.",
// 	}

// 	// ✅ If data found in ES, use it
// 	if err == nil {
// 		if hits, ok := result["hits"].(map[string]interface{}); ok {
// 			if hitsList, ok := hits["hits"].([]interface{}); ok && len(hitsList) > 0 {
// 				if hitMap, ok := hitsList[0].(map[string]interface{}); ok {
// 					if source, ok := hitMap["_source"].(map[string]interface{}); ok {
// 						if insights, ok := source["market_insights"].(map[string]interface{}); ok {
// 							if trend, ok := insights["market_trend"].(string); ok {
// 								marketTrend["description"] = trend
// 							}
// 							if rate, ok := insights["growth_rate"].(float64); ok {
// 								growthRate["description"] = fmt.Sprintf("Growing at %.1f%% annually", rate)
// 							}
// 						}
// 					}
// 				}
// 			}
// 		}
// 	}

// 	return gin.H{
// 		"type":    "market_insights",
// 		"enabled": true,
// 		"data": gin.H{
// 			"marketTrend": marketTrend,
// 			"growthRate":  growthRate,
// 		},
// 	}
// }

// //////////////////////////////////////////////////////////////////////////////////////////////////////

// // ========================================================================
// // SEARCH FRANCHISES WITH FILTERS
// // ========================================================================
// // SearchFranchises handles franchise search with filters
// func (h *FranchiseHandler) SearchFranchises(c *gin.Context) {
// 	var filters models.FranchiseSearchFilters

// 	if err := c.ShouldBindQuery(&filters); err != nil {
// 		h.validationError(c, "Invalid search parameters: "+err.Error())
// 		return
// 	}

// 	// ===== VALIDATION =====
// 	// Validate Query string
// 	if filters.Query != "" {
// 		if err := ozzo.Validate(filters.Query,
// 			validation.ValidateStringLength(0, 500),
// 			validation.SafeSQLString,
// 			validation.SafeNoSQLString,
// 		); err != nil {
// 			h.validationError(c, "Invalid query: "+err.Error())
// 			return
// 		}
// 	}

// 	// ✅ REMOVED CATEGORY VALIDATION - Allow any category/industry
// 	// Validate only for SQL injection, not enum values
// 	if filters.Category != "" {
// 		if err := ozzo.Validate(filters.Category,
// 			validation.ValidateStringLength(0, 100),
// 			validation.SafeSQLString,
// 			validation.SafeNoSQLString,
// 		); err != nil {
// 			h.validationError(c, "Invalid category: "+err.Error())
// 			return
// 		}
// 	}

// 	// Validate Investment Range
// 	if filters.MinInvestment < 0 || filters.MinInvestment > 100000000 {
// 		h.validationError(c, "Invalid minInvestment: must be 0-100000000")
// 		return
// 	}
// 	if filters.MaxInvestment < 0 || filters.MaxInvestment > 100000000 {
// 		h.validationError(c, "Invalid maxInvestment: must be 0-100000000")
// 		return
// 	}
// 	// ===== END VALIDATION =====

// 	// Validate and set default values
// 	if filters.Page <= 0 {
// 		filters.Page = 1
// 	}
// 	if filters.Limit <= 0 {
// 		filters.Limit = 10
// 	}
// 	if filters.Limit > 100 {
// 		filters.Limit = 100
// 	}

// 	// ✅ ALWAYS use franchise_listings index
// 	indices := []string{"franchise_listings"}

// 	// Build Elasticsearch query
// 	query := h.buildSearchQuery(filters)

// 	result, err := h.esClient.Search(c.Request.Context(), indices, query)
// 	if err != nil {
// 		h.logger.Error("🔥 Elasticsearch Search Failed", map[string]interface{}{
// 			"error":   err.Error(),
// 			"indices": indices,
// 			"query":   query,
// 		})

// 		c.JSON(http.StatusInternalServerError, gin.H{
// 			"success": false,
// 			"error":   "Search failed",
// 			"message": err.Error(),
// 		})
// 		return
// 	}

// 	// Process and format results
// 	franchises, total, err := h.processSearchResults(result, filters)
// 	if err != nil {
// 		h.internalError(c, "Failed to process results", err)
// 		return
// 	}

// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		"success": true,
// 		"data": gin.H{
// 			"franchises": franchises,
// 			"pagination": gin.H{
// 				"page":       filters.Page,
// 				"limit":      filters.Limit,
// 				"total":      total,
// 				"totalPages": (total + filters.Limit - 1) / filters.Limit,
// 			},
// 			"filters_applied": filters,
// 		},
// 	})
// }

// // ////////////////////////////////////////////////////////////////////////////////////////////
// // ========================================================================
// // FRANCHISE GET BY ID
// // ========================================================================
// // GetByID retrieves a specific franchise
// func (h *FranchiseHandler) GetByID(c *gin.Context) {
// 	franchiseID := c.Param("id")
// 	if franchiseID == "" {
// 		h.validationError(c, "Franchise ID is required")
// 		return
// 	}

// 	// ✅ Search in franchise_listings using search query (not direct get)
// 	query := map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"term": map[string]interface{}{
// 				"_id": franchiseID,
// 			},
// 		},
// 		"size": 1,
// 	}

// 	result, err := h.esClient.Search(c.Request.Context(), []string{"franchise_listings"}, query)
// 	if err != nil {
// 		h.internalError(c, "Search failed", err)
// 		return
// 	}

// 	// Extract first hit
// 	var franchise map[string]interface{}
// 	if hits, ok := result["hits"].(map[string]interface{}); ok {
// 		if hitsList, ok := hits["hits"].([]interface{}); ok && len(hitsList) > 0 {
// 			if hitMap, ok := hitsList[0].(map[string]interface{}); ok {
// 				if source, ok := hitMap["_source"].(map[string]interface{}); ok {
// 					franchise = source
// 					franchise["_id"] = hitMap["_id"]
// 					franchise["_index"] = "franchise_listings"
// 				}
// 			}
// 		}
// 	}

// 	if franchise == nil {
// 		h.notFoundError(c, "Franchise not found")
// 		return
// 	}

// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		//c.JSON(http.StatusOK, gin.H{
// 		"success": true,
// 		"data":    franchise,
// 	})
// }

// // ========================================================================
// // FRANCHISE GET STATS
// // ========================================================================
// // GetStats returns statistics and aggregations
// func (h *FranchiseHandler) GetStats(c *gin.Context) {
// 	stats := map[string]interface{}{
// 		"by_category": map[string]int{
// 			// "education": 0,
// 			// "fashion":   0,
// 		},
// 		"investment_ranges": map[string]string{
// 			"low":       "₹2-10 Lakhs",
// 			"medium":    "₹10-30 Lakhs",
// 			"high":      "₹30+ Lakhs",
// 			"very_high": "₹50+ Lakhs",
// 		},
// 		"space_requirements": map[string]string{
// 			"small":  "50-200 sq ft",
// 			"medium": "200-500 sq ft",
// 			"large":  "500-1000 sq ft",
// 			"xlarge": "1000+ sq ft",
// 		},
// 	}

// 	// Get actual counts from Elasticsearch
// 	count, err := h.esClient.Count(c.Request.Context(), "franchise_listings", map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"match_all": map[string]interface{}{},
// 		},
// 	})

// 	if err == nil {
// 		// Get counts by industry aggregation
// 		aggQuery := map[string]interface{}{
// 			"size": 0,
// 			"aggs": map[string]interface{}{
// 				"by_industry": map[string]interface{}{
// 					"terms": map[string]interface{}{
// 						"field": "industry.slug",
// 						"size":  20,
// 					},
// 				},
// 			},
// 		}

// 		result, err := h.esClient.Search(c.Request.Context(), []string{"franchise_listings"}, aggQuery)
// 		if err == nil {
// 			if aggs, ok := result["aggregations"].(map[string]interface{}); ok {
// 				if byIndustry, ok := aggs["by_industry"].(map[string]interface{}); ok {
// 					if buckets, ok := byIndustry["buckets"].([]interface{}); ok {
// 						for _, bucket := range buckets {
// 							if b, ok := bucket.(map[string]interface{}); ok {
// 								key := b["key"].(string)
// 								docCount := int(b["doc_count"].(float64))
// 								stats["by_category"].(map[string]int)[key] = docCount

// 							}
// 						}
// 					}
// 				}
// 			}
// 		}
// 	} else {
// 		h.logger.Warn("Failed to count documents", map[string]interface{}{
// 			"index": "franchise_listings",
// 			"error": err.Error(),
// 			"count": count,
// 		})
// 	}

// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		//c.JSON(http.StatusOK, gin.H{
// 		"success": true,
// 		"data":    stats,
// 	})
// }

// // ========================================================================
// // FRANCHISE GET SUGGESTIONS
// // ========================================================================
// // GetSuggestions provides autocomplete suggestions
// func (h *FranchiseHandler) GetSuggestions(c *gin.Context) {
// 	query := c.Query("q")
// 	if query == "" {
// 		h.jsonResponse(c, http.StatusOK, gin.H{
// 			//c.JSON(http.StatusOK, gin.H{
// 			"success": true,
// 			"data":    []string{},
// 		})
// 		return
// 	}

// 	// Build completion/suggest query
// 	suggestQuery := map[string]interface{}{
// 		"suggest": map[string]interface{}{
// 			"brand_suggest": map[string]interface{}{
// 				"prefix": query,
// 				"completion": map[string]interface{}{
// 					"field":           "name.suggest",
// 					"skip_duplicates": true,
// 					"size":            10,
// 				},
// 			},
// 		},
// 	}

// 	// Search in franchise_listings
// 	indices := []string{"franchise_listings"}

// 	var suggestions []string

// 	result, err := h.esClient.Search(c.Request.Context(), indices, suggestQuery)
// 	if err == nil {
// 		if suggests, ok := result["suggest"].(map[string]interface{}); ok {
// 			if brandSuggests, ok := suggests["brand_suggest"].([]interface{}); ok {
// 				for _, suggestion := range brandSuggests {
// 					if sMap, ok := suggestion.(map[string]interface{}); ok {
// 						if options, ok := sMap["options"].([]interface{}); ok {
// 							for _, option := range options {
// 								if optMap, ok := option.(map[string]interface{}); ok {
// 									if text, ok := optMap["text"].(string); ok && text != "" {
// 										suggestions = append(suggestions, text)
// 									}
// 								}
// 							}
// 						}
// 					}
// 				}
// 			}
// 		}
// 	}

// 	// Remove duplicates and limit to 10
// 	suggestions = h.removeDuplicates(suggestions)
// 	if len(suggestions) > 10 {
// 		suggestions = suggestions[:10]
// 	}

// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		//c.JSON(http.StatusOK, gin.H{
// 		"success": true,
// 		"data":    suggestions,
// 	})
// }

// // ========================================================================
// // FRANCHISE GET CATEGORIES
// // ========================================================================
// // GetCategories returns all available franchise categories from database
// func (h *FranchiseHandler) GetCategories(c *gin.Context) {
// 	// Query to get all active industries with their categories
// 	query := `
// 		SELECT
// 			i.id,
// 			i.name,
// 			i.slug,
// 			i.icon_name,
// 			i.color_hex,
// 			i.listing_description,
// 			COUNT(DISTINCT c.id) as category_count
// 		FROM industries i
// 		LEFT JOIN categories c ON i.id = c.industry_id AND c.is_active = true
// 		WHERE i.is_active = true
// 		GROUP BY i.id, i.name, i.slug, i.icon_name, i.color_hex, i.listing_description
// 		ORDER BY i.display_order
// 	`

// 	rows, err := h.pgClient.DB.QueryContext(c.Request.Context(), query)
// 	if err != nil {
// 		h.internalError(c, "Failed to fetch categories", err)
// 		return
// 	}
// 	defer rows.Close()

// 	var categories []map[string]interface{}
// 	for rows.Next() {
// 		var (
// 			id, name, slug                  string
// 			iconName, colorHex, description interface{}
// 			categoryCount                   int
// 		)

// 		if err := rows.Scan(&id, &name, &slug, &iconName, &colorHex, &description, &categoryCount); err != nil {
// 			continue
// 		}

// 		category := map[string]interface{}{
// 			"id":             id,
// 			"name":           name,
// 			"slug":           slug,
// 			"category_count": categoryCount,
// 		}

// 		if iconName != nil {
// 			category["icon"] = iconName
// 		}
// 		if colorHex != nil {
// 			category["color"] = colorHex
// 		}
// 		if description != nil {
// 			category["description"] = description
// 		}

// 		categories = append(categories, category)
// 	}

// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		//c.JSON(http.StatusOK, gin.H{
// 		"success": true,
// 		"data":    categories,
// 	})
// }

// // ========================================================================
// // FRANCHISE GET INDUSTRY BY SLUG
// // ========================================================================
// // GetIndustryBySlug returns detailed information about a specific industry
// func (h *FranchiseHandler) GetIndustryBySlug(c *gin.Context) {
// 	slug := c.Param("slug")
// 	if slug == "" {
// 		h.validationError(c, "Industry slug is required")
// 		return
// 	}

// 	// Get industry details
// 	industryQuery := `
// 		SELECT
// 			id,
// 			name,
// 			slug,
// 			icon_name,
// 			color_hex,
// 			listing_title,
// 			listing_description,
// 			meta_title,
// 			meta_description
// 		FROM industries
// 		WHERE slug = $1 AND is_active = true
// 	`

// 	var (
// 		id, name, industrySlug                               string
// 		iconName, colorHex, title, desc, metaTitle, metaDesc interface{}
// 	)

// 	err := h.pgClient.DB.QueryRowContext(c.Request.Context(), industryQuery, slug).Scan(
// 		&id, &name, &industrySlug, &iconName, &colorHex, &title, &desc, &metaTitle, &metaDesc,
// 	)

// 	if err != nil {
// 		h.notFoundError(c, "Industry not found")
// 		return
// 	}

// 	industry := map[string]interface{}{
// 		"id":   id,
// 		"name": name,
// 		"slug": industrySlug,
// 	}

// 	if iconName != nil {
// 		industry["icon"] = iconName
// 	}
// 	if colorHex != nil {
// 		industry["color"] = colorHex
// 	}
// 	if title != nil {
// 		industry["title"] = title
// 	}
// 	if desc != nil {
// 		industry["description"] = desc
// 	}
// 	if metaTitle != nil {
// 		industry["meta_title"] = metaTitle
// 	}
// 	if metaDesc != nil {
// 		industry["meta_description"] = metaDesc
// 	}

// 	// Get categories for this industry
// 	categoriesQuery := `
// 		SELECT
// 			id,
// 			name,
// 			slug,
// 			description,
// 			icon_name
// 		FROM categories
// 		WHERE industry_id = $1 AND is_active = true
// 		ORDER BY display_order
// 	`

// 	rows, err := h.pgClient.DB.QueryContext(c.Request.Context(), categoriesQuery, id)
// 	if err == nil {
// 		defer rows.Close()

// 		var categories []map[string]interface{}
// 		for rows.Next() {
// 			var (
// 				catID, catName, catSlug string
// 				catDesc, catIcon        interface{}
// 			)

// 			if err := rows.Scan(&catID, &catName, &catSlug, &catDesc, &catIcon); err == nil {
// 				category := map[string]interface{}{
// 					"id":   catID,
// 					"name": catName,
// 					"slug": catSlug,
// 				}
// 				if catDesc != nil {
// 					category["description"] = catDesc
// 				}
// 				if catIcon != nil {
// 					category["icon"] = catIcon
// 				}
// 				categories = append(categories, category)
// 			}
// 		}
// 		industry["categories"] = categories
// 	}

// 	// Get franchise count for this industry (from Elasticsearch)
// 	countQuery := map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"term": map[string]interface{}{
// 				"industry.slug": slug,
// 			},
// 		},
// 	}

// 	count, err := h.esClient.Count(c.Request.Context(), "franchise_listings", countQuery)
// 	if err == nil {
// 		industry["franchise_count"] = int(count)
// 	}

// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		//c.JSON(http.StatusOK, gin.H{
// 		"success": true,
// 		"data":    industry,
// 	})
// }

// // ========================================================================
// // FRANCHISE GET ALL INDUSTRIES
// // ========================================================================
// // GetAllIndustries returns all industries with basic info
// func (h *FranchiseHandler) GetAllIndustries(c *gin.Context) {
// 	query := `
// 		SELECT
// 			i.id,
// 			i.name,
// 			i.slug,
// 			i.icon_name,
// 			i.color_hex,
// 			i.listing_description,
// 			i.display_order
// 		FROM industries i
// 		WHERE i.is_active = true
// 		ORDER BY i.display_order
// 	`

// 	rows, err := h.pgClient.DB.QueryContext(c.Request.Context(), query)
// 	if err != nil {
// 		h.internalError(c, "Failed to fetch industries", err)
// 		return
// 	}
// 	defer rows.Close()

// 	var industries []map[string]interface{}
// 	for rows.Next() {
// 		var (
// 			id, name, slug                  string
// 			iconName, colorHex, description interface{}
// 			displayOrder                    int
// 		)

// 		if err := rows.Scan(&id, &name, &slug, &iconName, &colorHex, &description, &displayOrder); err != nil {
// 			continue
// 		}

// 		industry := map[string]interface{}{
// 			"id":            id,
// 			"name":          name,
// 			"slug":          slug,
// 			"display_order": displayOrder,
// 		}

// 		if iconName != nil {
// 			industry["icon"] = iconName
// 		}
// 		if colorHex != nil {
// 			industry["color"] = colorHex
// 		}
// 		if description != nil {
// 			industry["description"] = description
// 		}

// 		industries = append(industries, industry)
// 	}

// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		//c.JSON(http.StatusOK, gin.H{
// 		"success": true,
// 		"data":    industries,
// 	})
// }

// // ========================================================================
// // FRANCHISE GET FEATURED
// // ========================================================================
// func (h *FranchiseHandler) GetFeatured(c *gin.Context) {
// 	// ✅ Just get top rated franchises (no featured field needed)
// 	query := map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"match_all": map[string]interface{}{},
// 		},
// 		"size": 10,
// 		"sort": []map[string]interface{}{
// 			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
// 			{"_score": map[string]interface{}{"order": "desc"}},
// 		},
// 	}

// 	indices := []string{"franchise_listings"}

// 	result, err := h.esClient.Search(c.Request.Context(), indices, query)
// 	if err != nil {
// 		h.logger.Error("Failed to fetch featured franchises", map[string]interface{}{
// 			"error": err.Error(),
// 		})
// 		//c.JSON(http.StatusOK, gin.H{
// 		h.jsonResponse(c, http.StatusOK, gin.H{
// 			"success": true,
// 			"data":    []map[string]interface{}{},
// 		})
// 		return
// 	}

// 	franchises, _, _ := h.processSearchResults(result, models.FranchiseSearchFilters{})

// 	//c.JSON(http.StatusOK, gin.H{
// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		"success": true,
// 		"data":    franchises,
// 	})
// }

// // ============================================================================
// // USER-SPECIFIC OPERATIONS (Require PostgreSQL)
// // ============================================================================

// // AddToFavorites adds a franchise to user's favorites
// func (h *FranchiseHandler) AddToFavorites(c *gin.Context) {
// 	franchiseID := c.Param("id")
// 	userID := c.GetString("userId") // From JWT middleware

// 	if franchiseID == "" || userID == "" {
// 		h.validationError(c, "Franchise ID and User ID are required")
// 		return
// 	}

// 	// Insert into favorites table
// 	query := `
// 		INSERT INTO user_favorites (user_id, franchise_id, created_at)
// 		VALUES ($1, $2, NOW())
// 		ON CONFLICT (user_id, franchise_id) DO NOTHING
// 		RETURNING id
// 	`

// 	var favoriteID int64
// 	err := h.pgClient.DB.QueryRowContext(c.Request.Context(), query, userID, franchiseID).Scan(&favoriteID)
// 	if err != nil {
// 		h.internalError(c, "Failed to add favorite", err)
// 		return
// 	}

// 	//c.JSON(http.StatusOK, gin.H{
// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		"success": true,
// 		"message": "Franchise added to favorites",
// 		"data": gin.H{
// 			"favoriteId":  favoriteID,
// 			"franchiseId": franchiseID,
// 		},
// 	})
// }

// // RemoveFromFavorites removes a franchise from user's favorites
// func (h *FranchiseHandler) RemoveFromFavorites(c *gin.Context) {
// 	franchiseID := c.Param("id")
// 	userID := c.GetString("userId")

// 	if franchiseID == "" || userID == "" {
// 		h.validationError(c, "Franchise ID and User ID are required")
// 		return
// 	}

// 	query := `DELETE FROM user_favorites WHERE user_id = $1 AND franchise_id = $2`

// 	_, err := h.pgClient.DB.ExecContext(c.Request.Context(), query, userID, franchiseID)
// 	if err != nil {
// 		h.internalError(c, "Failed to remove favorite", err)
// 		return
// 	}

// 	//c.JSON(http.StatusOK, gin.H{
// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		"success": true,
// 		"message": "Franchise removed from favorites",
// 	})
// }

// // GetFavorites retrieves user's favorite franchises
// func (h *FranchiseHandler) GetFavorites(c *gin.Context) {
// 	userID := c.GetString("userId")

// 	if userID == "" {
// 		h.validationError(c, "User ID is required")
// 		return
// 	}

// 	query := `
// 		SELECT franchise_id, created_at
// 		FROM user_favorites
// 		WHERE user_id = $1
// 		ORDER BY created_at DESC
// 	`

// 	rows, err := h.pgClient.DB.QueryContext(c.Request.Context(), query, userID)
// 	if err != nil {
// 		h.internalError(c, "Failed to fetch favorites", err)
// 		return
// 	}
// 	defer rows.Close()

// 	var franchiseIDs []string
// 	for rows.Next() {
// 		var franchiseID string
// 		var createdAt interface{}
// 		if err := rows.Scan(&franchiseID, &createdAt); err == nil {
// 			franchiseIDs = append(franchiseIDs, franchiseID)
// 		}
// 	}

// 	// Fetch franchise details from Elasticsearch
// 	var favorites []map[string]interface{}
// 	for _, id := range franchiseIDs {
// 		result, err := h.esClient.Get(c.Request.Context(), "franchise_listings", id)
// 		if err == nil && result != nil {
// 			result["_id"] = id
// 			favorites = append(favorites, result)
// 		}
// 	}

// 	//c.JSON(http.StatusOK, gin.H{
// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		"success": true,
// 		"data":    favorites,
// 	})
// }

// // SaveSearch saves a user's search query for future reference
// func (h *FranchiseHandler) SaveSearch(c *gin.Context) {
// 	userID := c.GetString("userId")

// 	var input struct {
// 		Name    string                        `json:"name" binding:"required"`
// 		Filters models.FranchiseSearchFilters `json:"filters" binding:"required"`
// 	}

// 	if err := c.ShouldBindJSON(&input); err != nil {
// 		h.validationError(c, "Invalid input: "+err.Error())
// 		return
// 	}

// 	query := `
// 		INSERT INTO saved_searches (user_id, name, filters, created_at)
// 		VALUES ($1, $2, $3, NOW())
// 		RETURNING id
// 	`

// 	var searchID int64
// 	err := h.pgClient.DB.QueryRowContext(c.Request.Context(), query, userID, input.Name, input.Filters).Scan(&searchID)
// 	if err != nil {
// 		h.internalError(c, "Failed to save search", err)
// 		return
// 	}

// 	//c.JSON(http.StatusOK, gin.H{
// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		"success": true,
// 		"message": "Search saved successfully",
// 		"data": gin.H{
// 			"searchId": searchID,
// 			"name":     input.Name,
// 		},
// 	})
// }

// // GetSavedSearches retrieves user's saved searches
// func (h *FranchiseHandler) GetSavedSearches(c *gin.Context) {
// 	userID := c.GetString("userId")

// 	query := `
// 		SELECT id, name, filters, created_at
// 		FROM saved_searches
// 		WHERE user_id = $1
// 		ORDER BY created_at DESC
// 	`

// 	rows, err := h.pgClient.DB.QueryContext(c.Request.Context(), query, userID)
// 	if err != nil {
// 		h.internalError(c, "Failed to fetch saved searches", err)
// 		return
// 	}
// 	defer rows.Close()

// 	var searches []map[string]interface{}
// 	for rows.Next() {
// 		var id int64
// 		var name string
// 		var filters interface{}
// 		var createdAt interface{}

// 		if err := rows.Scan(&id, &name, &filters, &createdAt); err == nil {
// 			searches = append(searches, map[string]interface{}{
// 				"id":        id,
// 				"name":      name,
// 				"filters":   filters,
// 				"createdAt": createdAt,
// 			})
// 		}
// 	}

// 	//c.JSON(http.StatusOK, gin.H{
// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		"success": true,
// 		"data":    searches,
// 	})
// }

// // DeleteSavedSearch deletes a saved search
// func (h *FranchiseHandler) DeleteSavedSearch(c *gin.Context) {
// 	searchID := c.Param("id")
// 	userID := c.GetString("userId")

// 	query := `DELETE FROM saved_searches WHERE id = $1 AND user_id = $2`

// 	_, err := h.pgClient.DB.ExecContext(c.Request.Context(), query, searchID, userID)
// 	if err != nil {
// 		h.internalError(c, "Failed to delete saved search", err)
// 		return
// 	}

// 	//c.JSON(http.StatusOK, gin.H{
// 	h.jsonResponse(c, http.StatusOK, gin.H{
// 		"success": true,
// 		"message": "Saved search deleted successfully",
// 	})
// }

// // ============================================================================
// // HELPER METHODS
// // ============================================================================
// // ✅ FIXED buildSearchQuery - Properly handle all filters
// func (h *FranchiseHandler) buildSearchQuery(filters models.FranchiseSearchFilters) map[string]interface{} {
// 	mustQueries := []map[string]interface{}{}
// 	filterQueries := []map[string]interface{}{}

// 	// ===== TEXT SEARCH =====
// 	if filters.Query != "" {
// 		mustQueries = append(mustQueries, map[string]interface{}{
// 			"multi_match": map[string]interface{}{
// 				"query":     filters.Query,
// 				"fields":    []string{"name^3", "description^2", "tags", "industry.name", "location"},
// 				"type":      "best_fields",
// 				"fuzziness": "AUTO",
// 			},
// 		})
// 	}

// 	// ===== INDUSTRY/CATEGORY FILTER =====
// 	// Support both slug and name matching
// 	if filters.Category != "" {
// 		// Try to match against industry slug or name (case-insensitive)
// 		filterQueries = append(filterQueries, map[string]interface{}{
// 			"bool": map[string]interface{}{
// 				"should": []map[string]interface{}{
// 					{
// 						"term": map[string]interface{}{
// 							"industry.slug.keyword": strings.ToLower(filters.Category),
// 						},
// 					},
// 					{
// 						"match": map[string]interface{}{
// 							"industry.name": map[string]interface{}{
// 								"query":    filters.Category,
// 								"operator": "and",
// 							},
// 						},
// 					},
// 					{
// 						"wildcard": map[string]interface{}{
// 							"industry.slug": map[string]interface{}{
// 								"value":            "*" + strings.ToLower(filters.Category) + "*",
// 								"case_insensitive": true,
// 							},
// 						},
// 					},
// 				},
// 				"minimum_should_match": 1,
// 			},
// 		})
// 	}

// 	// ===== LOCATION FILTER =====
// 	if filters.Location != "" {
// 		// Support partial matching for location
// 		filterQueries = append(filterQueries, map[string]interface{}{
// 			"bool": map[string]interface{}{
// 				"should": []map[string]interface{}{
// 					{
// 						"match": map[string]interface{}{
// 							"location": map[string]interface{}{
// 								"query":    filters.Location,
// 								"operator": "and",
// 							},
// 						},
// 					},
// 					{
// 						"wildcard": map[string]interface{}{
// 							"location": map[string]interface{}{
// 								"value":            "*" + strings.ToLower(filters.Location) + "*",
// 								"case_insensitive": true,
// 							},
// 						},
// 					},
// 					{
// 						"match_phrase": map[string]interface{}{
// 							"location": filters.Location,
// 						},
// 					},
// 				},
// 				"minimum_should_match": 1,
// 			},
// 		})
// 	}

// 	// ===== INVESTMENT RANGE FILTER =====

// 	// ===== INVESTMENT RANGE FILTER =====
// 	if filters.MinInvestment > 0 || filters.MaxInvestment > 0 {

// 		userMin := filters.MinInvestment * 100000
// 		userMax := filters.MaxInvestment * 100000

// 		investmentFilter := map[string]interface{}{
// 			"bool": map[string]interface{}{
// 				"must": []map[string]interface{}{},
// 			},
// 		}

// 		mustConditions := []map[string]interface{}{}

// 		if filters.MinInvestment > 0 {
// 			mustConditions = append(mustConditions, map[string]interface{}{
// 				"range": map[string]interface{}{
// 					"investment.max_investment": map[string]interface{}{
// 						"gte": userMin,
// 					},
// 				},
// 			})
// 		}

// 		if filters.MaxInvestment > 0 {
// 			mustConditions = append(mustConditions, map[string]interface{}{
// 				"range": map[string]interface{}{
// 					"investment.min_investment": map[string]interface{}{
// 						"lte": userMax,
// 					},
// 				},
// 			})
// 		}

// 		if len(mustConditions) > 0 {
// 			investmentFilter["bool"].(map[string]interface{})["must"] = mustConditions
// 			filterQueries = append(filterQueries, investmentFilter)
// 		}
// 	}

// 	// ===== SPACE RANGE FILTER =====
// 	if filters.MinSpace > 0 || filters.MaxSpace > 0 {
// 		rangeFilter := map[string]interface{}{}
// 		if filters.MinSpace > 0 {
// 			rangeFilter["gte"] = filters.MinSpace
// 		}
// 		if filters.MaxSpace > 0 {
// 			rangeFilter["lte"] = filters.MaxSpace
// 		}

// 		filterQueries = append(filterQueries, map[string]interface{}{
// 			"bool": map[string]interface{}{
// 				"should": []map[string]interface{}{
// 					{
// 						"range": map[string]interface{}{
// 							"space.min_space": rangeFilter,
// 						},
// 					},
// 					{
// 						"range": map[string]interface{}{
// 							"space.max_space": rangeFilter,
// 						},
// 					},
// 				},
// 				"minimum_should_match": 1,
// 			},
// 		})
// 	}

// 	// ===== TAGS FILTER =====
// 	if len(filters.Tags) > 0 {
// 		tagFilters := []map[string]interface{}{}
// 		for _, tag := range filters.Tags {
// 			tagFilters = append(tagFilters, map[string]interface{}{
// 				"term": map[string]interface{}{
// 					"tags.keyword": tag,
// 				},
// 			})
// 		}
// 		filterQueries = append(filterQueries, map[string]interface{}{
// 			"bool": map[string]interface{}{
// 				"should":               tagFilters,
// 				"minimum_should_match": 1,
// 			},
// 		})
// 	}

// 	// ===== MINIMUM RATING FILTER =====
// 	if filters.MinRating > 0 {
// 		filterQueries = append(filterQueries, map[string]interface{}{
// 			"range": map[string]interface{}{
// 				"rating": map[string]interface{}{
// 					"gte": filters.MinRating,
// 				},
// 			},
// 		})
// 	}

// 	// ===== BUILD FINAL QUERY =====
// 	boolQuery := map[string]interface{}{}

// 	if len(mustQueries) > 0 {
// 		boolQuery["must"] = mustQueries
// 	}

// 	if len(filterQueries) > 0 {
// 		boolQuery["filter"] = filterQueries
// 	}

// 	// If no filters applied, match all
// 	if len(mustQueries) == 0 && len(filterQueries) == 0 {
// 		boolQuery["must"] = []map[string]interface{}{
// 			{"match_all": map[string]interface{}{}},
// 		}
// 	}

// 	query := map[string]interface{}{
// 		"query": map[string]interface{}{
// 			"bool": boolQuery,
// 		},
// 		"from": (filters.Page - 1) * filters.Limit,
// 		"size": filters.Limit,
// 		"sort": []map[string]interface{}{
// 			{"_score": map[string]interface{}{"order": "desc"}},
// 			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
// 		},
// 		"aggs": map[string]interface{}{
// 			"by_industry": map[string]interface{}{
// 				"terms": map[string]interface{}{
// 					"field": "industry.slug.keyword",
// 					"size":  50,
// 				},
// 			},
// 			"by_location": map[string]interface{}{
// 				"terms": map[string]interface{}{
// 					"field": "location.keyword",
// 					"size":  20,
// 				},
// 			},
// 			"investment_stats": map[string]interface{}{
// 				"stats": map[string]interface{}{
// 					"field": "investment.min_investment",
// 				},
// 			},
// 			"space_stats": map[string]interface{}{
// 				"stats": map[string]interface{}{
// 					"field": "space.min_space",
// 				},
// 			},
// 		},
// 	}

// 	return query
// }

// // Helper method to process search results
// func (h *FranchiseHandler) processSearchResults(result map[string]interface{}, _ models.FranchiseSearchFilters) ([]map[string]interface{}, int, error) {
// 	var franchises []map[string]interface{}
// 	var total int

// 	// Extract hits
// 	if hits, ok := result["hits"].(map[string]interface{}); ok {
// 		// Get total count
// 		if totalVal, ok := hits["total"].(map[string]interface{}); ok {
// 			if value, ok := totalVal["value"].(float64); ok {
// 				total = int(value)
// 			}
// 		}

// 		// Extract franchise documents
// 		if hitsList, ok := hits["hits"].([]interface{}); ok {
// 			for _, hit := range hitsList {
// 				if hitMap, ok := hit.(map[string]interface{}); ok {
// 					if source, ok := hitMap["_source"].(map[string]interface{}); ok {
// 						// Add Elasticsearch metadata
// 						source["_id"] = hitMap["_id"]
// 						source["_score"] = hitMap["_score"]
// 						source["_index"] = hitMap["_index"]

// 						franchises = append(franchises, source)
// 					}
// 				}
// 			}
// 		}
// 	}

// 	return franchises, total, nil
// }

// // Helper to remove duplicates from slice
// func (h *FranchiseHandler) removeDuplicates(slice []string) []string {
// 	keys := make(map[string]bool)
// 	list := []string{}
// 	for _, entry := range slice {
// 		if _, value := keys[entry]; !value {
// 			keys[entry] = true
// 			list = append(list, entry)
// 		}
// 	}
// 	return list
// }

// // ✅ ADD THIS HELPER FUNCTION at the bottom of franchise_handler.go:

// // Helper to send JSON without HTML escaping
// func (h *FranchiseHandler) jsonResponse(c *gin.Context, code int, obj interface{}) {
// 	var buf bytes.Buffer
// 	encoder := json.NewEncoder(&buf)
// 	encoder.SetEscapeHTML(false) // Prevent & → \u0026

// 	if err := encoder.Encode(obj); err != nil {
// 		c.JSON(http.StatusInternalServerError, gin.H{
// 			"success": false,
// 			"error":   "Failed to encode response",
// 		})
// 		return
// 	}

// 	c.Data(code, "application/json; charset=utf-8", buf.Bytes())
// }
