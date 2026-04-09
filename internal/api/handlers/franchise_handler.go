package handlers

import (
	"context"
	"database/sql"
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
	db                 *sql.DB
}

func NewFranchiseHandler(
	camundaClient *camunda.Client,
	log logger.Logger,
	redisClient *redis.Client,
	internalEmail string,
	paginationCfg config.PaginationConfig,
	db *sql.DB,
) *FranchiseHandler {
	return &FranchiseHandler{
		camundaClient:      camundaClient,
		logger:             log,
		redisClient:        redisClient,
		validator:          validation.NewValidator(),
		sanitizer:          validation.NewSanitizer(),
		internalAlertEmail: internalEmail,
		paginationCfg:      paginationCfg,
		db:                 db,
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

	categorySlug := strings.ToLower(strings.TrimSpace(c.Query("category")))
	if industrySlug == "" && categorySlug != "" {
		var resolved string
		err := h.db.QueryRowContext(ctx,
			`SELECT i.slug FROM industries i
             JOIN categories c ON c.industry_id = i.id
             WHERE c.slug = $1 LIMIT 1`, categorySlug,
		).Scan(&resolved)
		if err == nil && resolved != "" {
			industrySlug = resolved
		}
	}

	// // Industry slug required for listing page
	// if industrySlug == "" {
	// 	h.validationError(c, "Either search query (q) or industry slug (industry) is required")
	// 	return
	// }

	correlationKey := fmt.Sprintf("listing_%s_%d",
		uuid.New().String()[:8],
		time.Now().UnixNano())

	variables := map[string]interface{}{
		"correlationKey": correlationKey,
		"operation":      "listing_page",
		"pageType":       "listing",
		"industrySlug":   industrySlug,
		"categorySlug":   categorySlug,
		"page":           page,
		"pageSize":       pageSize, // ← "limit" -> "pageSize"     //"limit":          limit,
		"offset":         offset,
		"userId":         c.GetString("userId"),
		"lang":           c.GetHeader("X-Lang"),
		"traceId":        c.GetString("traceId"),
		"spanId":         c.GetString("spanId"),
		"requestId":      c.GetString("X-Request-ID"),
		"userAgent":      c.Request.UserAgent(),
		"ipAddress":      c.ClientIP(),
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

	response, err := h.executeWorkflow(ctx, "franchise-listing-page", variables)
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
