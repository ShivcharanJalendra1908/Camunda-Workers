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

	"camunda-workers/internal/api/middleware"
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
// ðŸ”¥ SINGLE WORKFLOW EXECUTION METHOD - WORKS FOR ALL WORKFLOWS
// ========================================================================

func (h *FranchiseHandler) executeWorkflow(
	ctx context.Context,
	processID string,
	variables map[string]interface{},
) (map[string]interface{}, error) {

	correlationKey := variables["correlationKey"].(string)
	channel := fmt.Sprintf("workflow:response:%s", correlationKey)

	// âœ… STEP 1: Subscribe to Redis FIRST (before workflow starts)
	pubsub := h.redisClient.Subscribe(ctx, channel)
	defer pubsub.Close()

	// âœ… STEP 2: Wait for subscription confirmation WITHOUT consuming messages.
	// pubsub.Receive() is safe for the subscription event but we must NOT use it
	// if a message might arrive at the same time (race on warm connections).
	// Use ReceiveMessage only for the subscribe-confirmation event type.
	subCtx, subCancel := context.WithTimeout(ctx, 5*time.Second)
	defer subCancel()
	if _, err := pubsub.Receive(subCtx); err != nil {
		// Non-fatal: connection may already be ready on warm pool; continue anyway.
		h.logger.Warn("subscription confirm timeout, proceeding anyway", map[string]interface{}{
			"channel": channel, "error": err.Error(),
		})
	}

	h.logger.Info("âœ… Subscribed to Redis channel BEFORE workflow", map[string]interface{}{
		"channel":        channel,
		"correlationKey": correlationKey,
		"processID":      processID,
	})

	// âœ… STEP 3: NOW start the workflow (subscription is ready)
	instance, err := h.camundaClient.StartProcessInstance(ctx, processID, variables)
	if err != nil {
		return nil, fmt.Errorf("failed to start workflow: %w", err)
	}

	h.logger.Info("Workflow started successfully", map[string]interface{}{
		"processId":      processID,
		"instanceKey":    instance.ProcessInstanceKey,
		"correlationKey": correlationKey,
	})

	// âœ… STEP 4: Wait for response with dual fallback
	responseChan := pubsub.Channel()
	timeoutDuration := 30 * time.Second

	// envelope is the format sent by send-api-response worker
	type envelope struct {
		Response     map[string]interface{} `json:"response"`
		CookieHeader string                 `json:"cookieHeader,omitempty"`
	}

	parsePayload := func(raw string) (map[string]interface{}, error) {
		// Try envelope format first (send-api-response publishes this)
		var env envelope
		if err := json.Unmarshal([]byte(raw), &env); err == nil && env.Response != nil {
			// Ensure success: true is present for frontend compatibility
			if env.Response["success"] == nil {
				env.Response["success"] = true
			}
			return env.Response, nil
		}
		// Fallback: direct map (legacy)
		var direct map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &direct); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
		if direct["success"] == nil {
			direct["success"] = true
		}
		return direct, nil
	}

	select {
	case msg := <-responseChan:
		response, err := parsePayload(msg.Payload)
		if err != nil {
			return nil, err
		}
		h.logger.Info("âœ… Received response from Redis", map[string]interface{}{
			"correlationKey": correlationKey,
			"channel":        channel,
			"success":        response["success"],
		})
		return response, nil

	case <-time.After(timeoutDuration):
		// âœ… Fallback: Try cache
		cacheKey := fmt.Sprintf("workflow:response:cache:%s", correlationKey)
		cached, err := h.redisClient.Get(ctx, cacheKey).Result()
		if err == nil {
			if response, parseErr := parsePayload(cached); parseErr == nil {
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
// ðŸ“± HOME PAGE
// ========================================================================

func (h *FranchiseHandler) GetHomePageData(c *gin.Context) {
	ctx := c.Request.Context()

	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise" // fallback for backward compatibility
	}

	correlationKey := fmt.Sprintf("home_%s_%d",
		uuid.New().String()[:8],
		time.Now().UnixNano())

	variables := map[string]interface{}{
		"correlationKey": correlationKey,
		"operation":      "home_page",
		"pageType":       "home",
		"entityType":     entityType,
		"lang":           c.GetHeader("X-Lang"),
		"userId":         c.GetString("userId"),
		"deviceType":     c.GetHeader("X-Device-Type"),
		"countryCode":    c.GetHeader("X-Country-Code"),
		"traceId":        c.GetString("traceId"),
		"spanId":         c.GetString("spanId"),
		"requestId":      c.GetString("requestId"),
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
// ðŸ“‹ LISTING PAGE
// ========================================================================

func (h *FranchiseHandler) GetListingPageData(c *gin.Context) {
	ctx := c.Request.Context()
	
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	}
	
	searchQuery := c.Query("q")
	if searchQuery == "" {
		searchQuery = c.Query("query")
	}
	industrySlug := strings.ToLower(strings.TrimSpace(c.Query("industry")))
	categorySlug := strings.ToLower(strings.TrimSpace(c.Query("category")))
	subCategorySlug := strings.ToLower(strings.TrimSpace(c.Query("subcategory"))) // â† NEW
	locationParam := strings.TrimSpace(c.Query("location"))

	page := 1
	pageSize := h.paginationCfg.DefaultPageSize

	if p := c.Query("page"); p != "" {
		if pageNum, err := strconv.Atoi(p); err == nil && pageNum > 0 {
			page = pageNum
		}
	}

	if ps := c.Query("page_size"); ps != "" {
		if psNum, err := strconv.Atoi(ps); err == nil && psNum > 0 {
			if psNum > h.paginationCfg.MaxPageSize {
				psNum = h.paginationCfg.MaxPageSize
			}
			pageSize = psNum
		}
	}

	offset := (page - 1) * pageSize

	if searchQuery != "" {
		h.SearchFranchises(c)
		return
	}

	// Resolve industrySlug from categorySlug if missing
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

	// Resolve industrySlug + categorySlug from subCategorySlug if both missing  â† NEW
	if subCategorySlug != "" {
		if industrySlug == "" {
			var resolvedIndustry string
			err := h.db.QueryRowContext(ctx,
				`SELECT i.slug FROM industries i
                 JOIN categories c ON c.industry_id = i.id
                 JOIN sub_categories sc ON sc.category_id = c.id
                 WHERE sc.slug = $1 LIMIT 1`, subCategorySlug,
			).Scan(&resolvedIndustry)
			if err == nil && resolvedIndustry != "" {
				industrySlug = resolvedIndustry
			}
		}
		if categorySlug == "" {
			var resolvedCategory string
			err := h.db.QueryRowContext(ctx,
				`SELECT c.slug FROM categories c
                 JOIN sub_categories sc ON sc.category_id = c.id
                 WHERE sc.slug = $1 LIMIT 1`, subCategorySlug,
			).Scan(&resolvedCategory)
			if err == nil && resolvedCategory != "" {
				categorySlug = resolvedCategory
			}
		}
	}

	correlationKey := fmt.Sprintf("listing_%s_%d",
		uuid.New().String()[:8],
		time.Now().UnixNano())

	variables := map[string]interface{}{
		"correlationKey":  correlationKey,
		"operation":       "listing_page",
		"pageType":        "listing",
		"entityType":      entityType,
		"industrySlug":    industrySlug,
		"categorySlug":    categorySlug,
		"subCategorySlug": subCategorySlug,
		"location":        locationParam,
		"page":            page,
		"pageSize":        pageSize, // â† "limit" -> "pageSize"     //"limit":          limit,
		"offset":          offset,
		"userId":          c.GetString("userId"),
		"lang":            c.GetHeader("X-Lang"),
		"traceId":         c.GetString("traceId"),
		"spanId":          c.GetString("spanId"),
		"requestId":       c.GetString("requestId"),
		"userAgent":       c.Request.UserAgent(),
		"ipAddress":       c.ClientIP(),
	}

	response, err := h.executeWorkflow(ctx, "franchise-listing-page", variables)
	if err != nil {
		h.internalError(c, "Failed to fetch listing data", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// ðŸ“„ DETAIL PAGE
// ========================================================================

func findStringField(val interface{}, targetKey string) string {
	if val == nil {
		return ""
	}
	if m, ok := val.(map[string]interface{}); ok {
		for k, v := range m {
			if strings.EqualFold(k, targetKey) {
				if s, ok := v.(string); ok {
					return s
				}
			}
		}
		for _, v := range m {
			if found := findStringField(v, targetKey); found != "" {
				return found
			}
		}
	} else if arr, ok := val.([]interface{}); ok {
		for _, item := range arr {
			if found := findStringField(item, targetKey); found != "" {
				return found
			}
		}
	}
	return ""
}

func stripEmailFields(val interface{}) {
	if val == nil {
		return
	}
	if m, ok := val.(map[string]interface{}); ok {
		for k := range m {
			lowerK := strings.ToLower(k)
			if strings.Contains(lowerK, "email") || strings.Contains(lowerK, "contact_email") || strings.Contains(lowerK, "contactemail") {
				delete(m, k)
			}
		}
		for _, v := range m {
			stripEmailFields(v)
		}
	} else if arr, ok := val.([]interface{}); ok {
		for _, item := range arr {
			stripEmailFields(item)
		}
	}
}

func (h *FranchiseHandler) GetFranchiseDetailPage(c *gin.Context) {
	ctx := c.Request.Context()
	slug := c.Param("slug")
	
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	}

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
		"entityType":     entityType,
		"slug":           slug,
		"includeStats":   true,
		"userId":         c.GetString("userId"),
		"lang":           c.GetHeader("X-Lang"),
		"traceId":        c.GetString("traceId"),
		"spanId":         c.GetString("spanId"),
		"requestId":      c.GetString("requestId"),
		"userAgent":      c.Request.UserAgent(),
		"ipAddress":      c.ClientIP(),
	}

	response, err := h.executeWorkflow(ctx, "franchise-detail-page", variables)
	if err != nil {
		h.internalError(c, "Failed to fetch franchise details", err)
		return
	}

	// Extract auth details
	userID := c.GetString("userId")
	claims := middleware.ExtractClaims(c)
	var isAdmin bool
	if claims != nil {
		if userID == "" {
			userID = claims.UserID
		}
		for _, r := range claims.Roles {
			if r == "SYSTEM_ADMIN" || r == "ADMIN" || r == "ROLE_PLATFORM_ADMIN" {
				isAdmin = true
				break
			}
		}
	}

	status := findStringField(response, "status")
	createdBy := findStringField(response, "created_by")
	if createdBy == "" {
		createdBy = findStringField(response, "createdBy")
	}

	isOwner := userID != "" && createdBy != "" && strings.EqualFold(createdBy, userID)

	// Block access if not live and not owner/admin
	if status != "" && !strings.EqualFold(status, "live") && !isOwner && !isAdmin {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Not Found",
			"message": "Entity details not found or not published",
		})
		return
	}

	// Strip emails if not owner/admin
	if !isOwner && !isAdmin {
		stripEmailFields(response)
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// ðŸ” SEARCH OPERATIONS
// ========================================================================

func (h *FranchiseHandler) SearchFranchises(c *gin.Context) {
	ctx := c.Request.Context()
	searchQuery := c.Query("query")
	if searchQuery == "" {
		searchQuery = c.Query("q")
	}

	var filters models.FranchiseSearchFilters
	if err := c.ShouldBindQuery(&filters); err != nil {
		h.validationError(c, "Invalid search parameters: "+err.Error())
		return
	}

	if searchQuery != "" {
		filters.Query = searchQuery
	}

	entityType := c.Param("entityType")
	if entityType != "" {
		filters.EntityType = entityType
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
		"entityType":     entityType,
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
			"entityType":    entityType,
			"minFee":        filters.MinFee,
			"maxFee":        filters.MaxFee,
			"minMembers":    filters.MinMembers,
			"maxMembers":    filters.MaxMembers,
		},
		"page":       filters.Page,
		"limit":      filters.Limit,
		"offset":     (filters.Page - 1) * filters.Limit,
		"sortBy":     filters.SortBy,
		"sortOrder":  filters.SortOrder,
		"userId":     c.GetString("userId"),
		"searchType": "advanced",
		"traceId":    c.GetString("traceId"),
		"spanId":     c.GetString("spanId"),
		"requestId":  c.GetString("requestId"),
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
// ðŸ¢ MVP WORKFLOW HELPER (for other operations)
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
	variables["requestId"] = c.GetString("requestId")
	variables["userAgent"] = c.Request.UserAgent()
	variables["ipAddress"] = c.ClientIP()

	return h.executeWorkflow(ctx, "franchise-mvp-workflow", variables)
}

// ========================================================================
// ðŸ¢ FRANCHISE OPERATIONS
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
// ðŸ“Š STATISTICS & ANALYTICS
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
// ðŸ—‚ï¸ CATEGORIES & INDUSTRIES
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

func (h *FranchiseHandler) GetAllIndustries(c *gin.Context) {
	ctx := c.Request.Context()

	correlationKey := fmt.Sprintf("industries_%s_%d",
		uuid.New().String()[:8],
		time.Now().UnixNano())

	variables := map[string]interface{}{
		"correlationKey": correlationKey,
		"operation":      "get_industries",
		"search":         c.Query("search"), // ?search=food â€” optional
		"lang":           c.GetHeader("X-Lang"),
		"userId":         c.GetString("userId"),
		"traceId":        c.GetString("traceId"),
		"spanId":         c.GetString("spanId"),
		"requestId":      c.GetString("requestId"),
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
// â­ FEATURED & RECOMMENDED
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

	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	}

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
		"entityType":     entityType,
	})

	if err != nil {
		h.internalError(c, "Failed to get suggestions", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// ðŸ‘¤ USER OPERATIONS
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
// â¤ï¸ USER FAVORITES
// ========================================================================

func (h *FranchiseHandler) AddToFavorites(c *gin.Context) {
	entityID := c.Param("id")
	userID := c.GetString("userId")
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	} else if strings.HasSuffix(entityType, "s") {
		entityType = entityType[:len(entityType)-1]
	}

	if entityID == "" || userID == "" {
		h.validationError(c, "Entity ID and User ID are required")
		return
	}

	response, err := h.executeUserActionWorkflow(c, "add_bookmark", map[string]interface{}{
		"operationType": "ADD_BOOKMARK",
		"userId":        userID,
		"entityId":      entityID,
		"entityType":    entityType,
		"franchiseId":   entityID,
	})

	if err != nil {
		h.internalError(c, "Failed to add to favorites", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (h *FranchiseHandler) RemoveFromFavorites(c *gin.Context) {
	entityID := c.Param("id")
	userID := c.GetString("userId")
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	} else if strings.HasSuffix(entityType, "s") {
		entityType = entityType[:len(entityType)-1]
	}

	if entityID == "" || userID == "" {
		h.validationError(c, "Entity ID and User ID are required")
		return
	}

	response, err := h.executeUserActionWorkflow(c, "remove_bookmark", map[string]interface{}{
		"operationType": "REMOVE_BOOKMARK",
		"userId":        userID,
		"entityId":      entityID,
		"entityType":    entityType,
		"franchiseId":   entityID,
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

	response, err := h.executeUserActionWorkflow(c, "get_bookmarks", map[string]interface{}{
		"operationType": "GET_USER_BOOKMARKS",
		"userId":        userID,
		"page":          page,
		"limit":         limit,
	})

	if err != nil {
		h.internalError(c, "Failed to get favorites", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// ========================================================================
// ðŸ” SAVED SEARCHES
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
// ðŸ†• BATCH OPERATIONS
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
// ðŸ·ï¸ TAGS OPERATIONS
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
// ðŸ·ï¸ ENQUIRY PAGE
// ========================================================================

// SubmitFranchiseEnquiry handles franchise enquiry submissions.
// Works for BOTH logged-in and anonymous users.
//
// Anonymous users: No rate limiting currently applied.
// userId is empty string for anonymous users.
//
// Logged-in users: Unlimited enquiries. userId set by SessionOrJWTAuth
// middleware (if route is also in protectedAPI group) or optional.
func (h *FranchiseHandler) SubmitFranchiseEnquiry(c *gin.Context) {
	ctx := c.Request.Context()

	// Franchise ID from URL param
	franchiseID := c.Param("id")
	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}

	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	}

	// userId is OPTIONAL â€” empty string for anonymous users.
	userID := c.GetString("userId")
	isAnonymous := userID == ""

	// Parse enquiry body
	var enquiryFormData map[string]interface{}
	if err := c.ShouldBindJSON(&enquiryFormData); err != nil {
		h.validationError(c, "Invalid request body: "+err.Error())
		return
	}

	correlationKey := fmt.Sprintf("enquiry_%s_%d",
		uuid.New().String()[:8],
		time.Now().UnixNano())

	variables := map[string]interface{}{
		"correlationKey":  correlationKey,
		"operation":       "franchise_enquiry",
		"entityType":      entityType,
		"entityId":        franchiseID,
		"franchiseId":     franchiseID,
		"userId":          userID,      // empty string for anonymous
		"isAnonymous":     isAnonymous, // downstream workers can use this
		"enquiryFormData": enquiryFormData,
		"traceId":         c.GetString("traceId"),
		"spanId":          c.GetString("spanId"),
		"requestId":       c.GetString("requestId"),
		"userAgent":       c.Request.UserAgent(),
		"ipAddress":       c.ClientIP(),
		"operationsEmail": h.internalAlertEmail,
	}

	if entityType == "association" {
		variables["associationId"] = franchiseID
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
	entityID := c.Param("id")
	userID := c.GetString("userId")
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	} else if strings.HasSuffix(entityType, "s") {
		entityType = entityType[:len(entityType)-1]
	}

	if entityID == "" {
		h.validationError(c, "Entity ID is required")
		return
	}
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "Authentication required",
			"message": "Please login to bookmark",
		})
		return
	}

	response, err := h.executeUserActionWorkflow(c, "add_bookmark", map[string]interface{}{
		"operationType": "ADD_BOOKMARK",
		"userId":        userID,
		"entityId":      entityID,
		"entityType":    entityType,
		"franchiseId":   entityID,
	})
	if err != nil {
		h.internalError(c, "Failed to add bookmark", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// UnbookmarkFranchise DELETE /api/franchises/:id/bookmark
func (h *FranchiseHandler) UnbookmarkFranchise(c *gin.Context) {
	entityID := c.Param("id")
	userID := c.GetString("userId")
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	} else if strings.HasSuffix(entityType, "s") {
		entityType = entityType[:len(entityType)-1]
	}

	if entityID == "" {
		h.validationError(c, "Entity ID is required")
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
		"entityId":      entityID,
		"entityType":    entityType,
		"franchiseId":   entityID,
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
	entityID := c.Param("id")
	userID := c.GetString("userId")
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	} else if strings.HasSuffix(entityType, "s") {
		entityType = entityType[:len(entityType)-1]
	}

	if entityID == "" {
		h.validationError(c, "Entity ID is required")
		return
	}
	if userID == "" {
		// Not logged in â€” return false without error
		c.JSON(http.StatusOK, gin.H{
			"success":      true,
			"isBookmarked": false,
		})
		return
	}

	response, err := h.executeUserActionWorkflow(c, "check_bookmark", map[string]interface{}{
		"operationType": "CHECK_BOOKMARK",
		"userId":        userID,
		"entityId":      entityID,
		"entityType":    entityType,
		"franchiseId":   entityID,
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
	entityID := c.Param("id")
	userID := c.GetString("userId")
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	} else if strings.HasSuffix(entityType, "s") {
		entityType = entityType[:len(entityType)-1]
	}

	if entityID == "" {
		h.validationError(c, "Entity ID is required")
		return
	}
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "Authentication required",
			"message": "Please login to submit a rating",
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
		"entityId":      entityID,
		"entityType":    entityType,
		"franchiseId":   entityID,
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
	entityID := c.Param("id")
	userID := c.GetString("userId")
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	} else if strings.HasSuffix(entityType, "s") {
		entityType = entityType[:len(entityType)-1]
	}

	if entityID == "" || userID == "" {
		h.validationError(c, "Entity ID and authentication required")
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
		"entityId":      entityID,
		"entityType":    entityType,
		"franchiseId":   entityID,
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
	entityID := c.Param("id")
	userID := c.GetString("userId")
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	} else if strings.HasSuffix(entityType, "s") {
		entityType = entityType[:len(entityType)-1]
	}

	if entityID == "" || userID == "" {
		h.validationError(c, "Entity ID and authentication required")
		return
	}

	response, err := h.executeUserActionWorkflow(c, "delete_rating", map[string]interface{}{
		"operationType": "DELETE_USER_RATING",
		"userId":        userID,
		"entityId":      entityID,
		"entityType":    entityType,
		"franchiseId":   entityID,
	})
	if err != nil {
		h.internalError(c, "Failed to delete rating", err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetFranchiseRatings GET /api/franchises/:id/ratings (public)
func (h *FranchiseHandler) GetFranchiseRatings(c *gin.Context) {
	entityID := c.Param("id")
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	} else if strings.HasSuffix(entityType, "s") {
		entityType = entityType[:len(entityType)-1]
	}

	if entityID == "" {
		h.validationError(c, "Entity ID is required")
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
		"entityId":      entityID,
		"entityType":    entityType,
		"franchiseId":   entityID,
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
	entityID := c.Param("id")
	userID := c.GetString("userId")
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	} else if strings.HasSuffix(entityType, "s") {
		entityType = entityType[:len(entityType)-1]
	}

	if entityID == "" {
		h.validationError(c, "Entity ID is required")
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
		"entityId":      entityID,
		"entityType":    entityType,
		"franchiseId":   entityID,
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

// ShareFranchise POST /api/franchises/:id/share (public â€” no auth needed)
func (h *FranchiseHandler) ShareFranchise(c *gin.Context) {
	entityID := c.Param("id")
	entityType := c.Param("entityType")
	if entityType == "" {
		entityType = "franchise"
	} else if strings.HasSuffix(entityType, "s") {
		entityType = entityType[:len(entityType)-1]
	}

	if entityID == "" {
		h.validationError(c, "Entity ID is required")
		return
	}

	var body struct {
		Platform string `json:"platform"` // whatsapp, twitter, linkedin, email, copy_link
	}
	// body optional â€” default to copy_link
	_ = c.ShouldBindJSON(&body)
	if body.Platform == "" {
		body.Platform = "copy_link"
	}

	// userID optional â€” anonymous share allowed
	userID := c.GetString("userId")

	response, err := h.executeUserActionWorkflow(c, "share_franchise", map[string]interface{}{
		"operationType": "SHARE_FRANCHISE",
		"entityId":      entityID,
		"entityType":    entityType,
		"franchiseId":   entityID,
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
// HELPER â€” executeUserActionWorkflow
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
	variables["requestId"] = c.GetString("requestId")
	variables["userAgent"] = c.Request.UserAgent()
	variables["ipAddress"] = c.ClientIP()

	return h.executeWorkflow(ctx, "franchise-user-actions", variables)
}

// ========================================================================
// ðŸ”„ HEALTH CHECK
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
// ðŸ› ï¸ HELPER METHODS
// ========================================================================

func (h *FranchiseHandler) validationError(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{
		"success":   false,
		"error":     "Validation error",
		"message":   message,
		"requestId": c.GetString("requestId"),
	})
}

func (h *FranchiseHandler) internalError(c *gin.Context, message string, err error) {
	h.logger.Error(message, map[string]interface{}{
		"error":     err.Error(),
		"path":      c.Request.URL.Path,
		"requestId": c.GetString("requestId"),
	})
	c.JSON(http.StatusInternalServerError, gin.H{
		"success":   false,
		"error":     "Internal server error",
		"message":   message,
		"requestId": c.GetString("requestId"),
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

	// Validate Fee Range
	if filters.MinFee < 0 || filters.MinFee > 1000000000 {
		return fmt.Errorf("invalid min_fee: must be >= 0")
	}
	if filters.MaxFee < 0 || filters.MaxFee > 1000000000 {
		return fmt.Errorf("invalid max_fee: must be >= 0")
	}

	// Validate Member Range
	if filters.MinMembers < 0 {
		return fmt.Errorf("invalid min_members: must be >= 0")
	}
	if filters.MaxMembers < 0 {
		return fmt.Errorf("invalid max_members: must be >= 0")
	}

	return nil
}

// ========================================================================
// ðŸ”Œ COMPATIBILITY METHODS (Not Used - For Registry Interface)
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

// ========================================================================
// ðŸŽ OFFERINGS MANAGEMENT
// ========================================================================

func (h *FranchiseHandler) GetOfferings(c *gin.Context) {
	entityID := c.Param("id")
	entityType := c.Param("entityType")
	userID := c.GetString("userId")

	if entityID == "" {
		h.validationError(c, "Entity ID is required")
		return
	}

	claims := middleware.ExtractClaims(c)
	var isAdmin bool
	if claims != nil {
		if userID == "" {
			userID = claims.UserID
		}
		for _, r := range claims.Roles {
			if r == "SYSTEM_ADMIN" || r == "ADMIN" || r == "ROLE_PLATFORM_ADMIN" {
				isAdmin = true
				break
			}
		}
	}

	var isMember bool
	if userID != "" {
		var status string
		err := h.db.QueryRowContext(c.Request.Context(), 
			"SELECT status FROM memberships WHERE user_id = $1 AND entity_id = $2", 
			userID, entityID).Scan(&status)
		if err == nil && status == "ACTIVE" {
			isMember = true
		}
	}

	rows, err := h.db.QueryContext(c.Request.Context(), 
		"SELECT id, title, description, type, details, status FROM offerings WHERE entity_id = $1 AND status != 'DELETED'", 
		entityID)
	if err != nil {
		h.internalError(c, "Failed to query offerings", err)
		return
	}
	defer rows.Close()

	var offeringsList []map[string]interface{}
	for rows.Next() {
		var id, title, description, offeringType, detailsStr, status string
		if err := rows.Scan(&id, &title, &description, &offeringType, &detailsStr, &status); err != nil {
			continue
		}

		var details map[string]interface{}
		_ = json.Unmarshal([]byte(detailsStr), &details)

		offering := map[string]interface{}{
			"id":          id,
			"title":       title,
			"description": description,
			"type":        offeringType,
			"status":      status,
		}

		if isMember || isAdmin {
			offering["details"] = details
		} else {
			offering["details"] = map[string]interface{}{
				"teaser": "Unlock full details by becoming a member of this " + entityType,
			}
		}
		offeringsList = append(offeringsList, offering)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"offerings": offeringsList,
	})
}

func (h *FranchiseHandler) CreateOffering(c *gin.Context) {
	entityID := c.Param("id")
	userID := c.GetString("userId")

	var input struct {
		Title       string                 `json:"title" binding:"required"`
		Description string                 `json:"description"`
		Type        string                 `json:"type" binding:"required"`
		Details     map[string]interface{} `json:"details"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.validationError(c, err.Error())
		return
	}

	claims := middleware.ExtractClaims(c)
	isAdmin := false
	if claims != nil {
		if userID == "" {
			userID = claims.UserID
		}
		for _, r := range claims.Roles {
			if r == "SYSTEM_ADMIN" || r == "ADMIN" || r == "ROLE_PLATFORM_ADMIN" {
				isAdmin = true
				break
			}
		}
	}

	if !isAdmin {
		var createdBy string
		err := h.db.QueryRowContext(c.Request.Context(), 
			"SELECT created_by FROM franchises WHERE id = $1", entityID).Scan(&createdBy)
		if err != nil || createdBy != userID {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "Forbidden", "message": "Only admin or listing owner can create offerings"})
			return
		}
	}

	detailsJSON, _ := json.Marshal(input.Details)
	if detailsJSON == nil {
		detailsJSON = []byte("{}")
	}

	var offeringID string
	err := h.db.QueryRowContext(c.Request.Context(), 
		"INSERT INTO offerings (entity_id, title, description, type, details, status) VALUES ($1, $2, $3, $4, $5, 'DRAFT') RETURNING id",
		entityID, input.Title, input.Description, input.Type, detailsJSON).Scan(&offeringID)
	if err != nil {
		h.internalError(c, "Failed to create offering", err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success":    true,
		"offeringId": offeringID,
		"message":    "Offering created in DRAFT status",
	})
}

func (h *FranchiseHandler) UpdateOffering(c *gin.Context) {
	offeringID := c.Param("offeringId")
	userID := c.GetString("userId")

	var input struct {
		Title       *string                 `json:"title"`
		Description *string                 `json:"description"`
		Type        *string                 `json:"type"`
		Details     map[string]interface{} `json:"details"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		h.validationError(c, err.Error())
		return
	}

	var entityID string
	err := h.db.QueryRowContext(c.Request.Context(), 
		"SELECT entity_id FROM offerings WHERE id = $1", offeringID).Scan(&entityID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Offering not found"})
		return
	}

	claims := middleware.ExtractClaims(c)
	isAdmin := false
	if claims != nil {
		if userID == "" {
			userID = claims.UserID
		}
		for _, r := range claims.Roles {
			if r == "SYSTEM_ADMIN" || r == "ADMIN" || r == "ROLE_PLATFORM_ADMIN" {
				isAdmin = true
				break
			}
		}
	}

	if !isAdmin {
		var createdBy string
		err := h.db.QueryRowContext(c.Request.Context(), 
			"SELECT created_by FROM franchises WHERE id = $1", entityID).Scan(&createdBy)
		if err != nil || createdBy != userID {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "Forbidden", "message": "Only admin or listing owner can update offerings"})
			return
		}
	}

	query := "UPDATE offerings SET updated_at = NOW()"
	args := []interface{}{}
	argPos := 1

	if input.Title != nil {
		query += fmt.Sprintf(", title = $%d", argPos)
		args = append(args, *input.Title)
		argPos++
	}
	if input.Description != nil {
		query += fmt.Sprintf(", description = $%d", argPos)
		args = append(args, *input.Description)
		argPos++
	}
	if input.Type != nil {
		query += fmt.Sprintf(", type = $%d", argPos)
		args = append(args, *input.Type)
		argPos++
	}
	if input.Details != nil {
		detailsJSON, _ := json.Marshal(input.Details)
		query += fmt.Sprintf(", details = $%d", argPos)
		args = append(args, detailsJSON)
		argPos++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argPos)
	args = append(args, offeringID)

	_, err = h.db.ExecContext(c.Request.Context(), query, args...)
	if err != nil {
		h.internalError(c, "Failed to update offering", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Offering updated successfully",
	})
}

func (h *FranchiseHandler) DeleteOffering(c *gin.Context) {
	offeringID := c.Param("offeringId")
	userID := c.GetString("userId")

	var entityID string
	err := h.db.QueryRowContext(c.Request.Context(), 
		"SELECT entity_id FROM offerings WHERE id = $1", offeringID).Scan(&entityID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Offering not found"})
		return
	}

	claims := middleware.ExtractClaims(c)
	isAdmin := false
	if claims != nil {
		if userID == "" {
			userID = claims.UserID
		}
		for _, r := range claims.Roles {
			if r == "SYSTEM_ADMIN" || r == "ADMIN" || r == "ROLE_PLATFORM_ADMIN" {
				isAdmin = true
				break
			}
		}
	}

	if !isAdmin {
		var createdBy string
		err := h.db.QueryRowContext(c.Request.Context(), 
			"SELECT created_by FROM franchises WHERE id = $1", entityID).Scan(&createdBy)
		if err != nil || createdBy != userID {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "Forbidden", "message": "Only admin or listing owner can delete offerings"})
			return
		}
	}

	_, err = h.db.ExecContext(c.Request.Context(), 
		"UPDATE offerings SET status = 'DELETED', updated_at = NOW() WHERE id = $1", offeringID)
	if err != nil {
		h.internalError(c, "Failed to delete offering", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Offering soft-deleted successfully",
	})
}

func (h *FranchiseHandler) PublishOffering(c *gin.Context) {
	h.setOfferingStatus(c, "PUBLISHED")
}

func (h *FranchiseHandler) UnpublishOffering(c *gin.Context) {
	h.setOfferingStatus(c, "DRAFT")
}

func (h *FranchiseHandler) setOfferingStatus(c *gin.Context, status string) {
	offeringID := c.Param("offeringId")
	userID := c.GetString("userId")

	var entityID string
	err := h.db.QueryRowContext(c.Request.Context(), 
		"SELECT entity_id FROM offerings WHERE id = $1", offeringID).Scan(&entityID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Offering not found"})
		return
	}

	claims := middleware.ExtractClaims(c)
	isAdmin := false
	if claims != nil {
		if userID == "" {
			userID = claims.UserID
		}
		for _, r := range claims.Roles {
			if r == "SYSTEM_ADMIN" || r == "ADMIN" || r == "ROLE_PLATFORM_ADMIN" {
				isAdmin = true
				break
			}
		}
	}

	if !isAdmin {
		var createdBy string
		err := h.db.QueryRowContext(c.Request.Context(), 
			"SELECT created_by FROM franchises WHERE id = $1", entityID).Scan(&createdBy)
		if err != nil || createdBy != userID {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "Forbidden", "message": "Only admin or listing owner can change offering status"})
			return
		}
	}

	_, err = h.db.ExecContext(c.Request.Context(), 
		"UPDATE offerings SET status = $1, updated_at = NOW() WHERE id = $2", status, offeringID)
	if err != nil {
		h.internalError(c, "Failed to update offering status", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Offering status updated to " + status,
	})
}

func (h *FranchiseHandler) RedeemOffering(c *gin.Context) {
	offeringID := c.Param("offeringId")
	userID := c.GetString("userId")

	claims := middleware.ExtractClaims(c)
	if claims != nil && userID == "" {
		userID = claims.UserID
	}

	if userID == "" {
		h.validationError(c, "User ID is required")
		return
	}

	var entityID string
	err := h.db.QueryRowContext(c.Request.Context(), 
		"SELECT entity_id FROM offerings WHERE id = $1 AND status = 'PUBLISHED'", offeringID).Scan(&entityID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Published offering not found"})
		return
	}

	var isMember bool
	var membershipStatus string
	err = h.db.QueryRowContext(c.Request.Context(), 
		"SELECT status FROM memberships WHERE user_id = $1 AND entity_id = $2", userID, entityID).Scan(&membershipStatus)
	if err == nil && membershipStatus == "ACTIVE" {
		isMember = true
	}

	if !isMember {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "Forbidden", "message": "Only active members can redeem offerings"})
		return
	}

	rateLimitKey := fmt.Sprintf("rate_limit:redeem:%s:%s", userID, time.Now().Format("2006-01-02-15"))
	count, err := h.redisClient.Incr(c.Request.Context(), rateLimitKey).Result()
	if err == nil && count == 1 {
		h.redisClient.Expire(c.Request.Context(), rateLimitKey, time.Hour)
	}
	if count > 10 {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"success": false,
			"error":   "Too Many Requests",
			"message": "Redemption rate limit exceeded (max 10 per hour)",
		})
		return
	}

	h.logger.Info("Offering redeemed", map[string]interface{}{
		"userId":     userID,
		"offeringId": offeringID,
		"entityId":   entityID,
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Offering redeemed successfully",
		"code":    uuid.New().String()[:8],
	})
}

// ========================================================================
// ðŸ‘¥ MEMBERSHIPS MANAGEMENT
// ========================================================================

func (h *FranchiseHandler) GetMemberships(c *gin.Context) {
	entityID := c.Param("id")
	_ = c.GetString("userId")

	if entityID == "" {
		h.validationError(c, "Entity ID is required")
		return
	}

	rows, err := h.db.QueryContext(c.Request.Context(), 
		`SELECT m.id, m.user_id, u.name, u.email, m.status, m.metadata, m.created_at 
		 FROM memberships m
		 JOIN users u ON m.user_id = u.id
		 WHERE m.entity_id = $1`, entityID)
	if err != nil {
		h.internalError(c, "Failed to query memberships", err)
		return
	}
	defer rows.Close()

	var list []map[string]interface{}
	for rows.Next() {
		var id, memberUserID, name, email, status, metadataStr, createdAt string
		if err := rows.Scan(&id, &memberUserID, &name, &email, &status, &metadataStr, &createdAt); err != nil {
			continue
		}

		var metadata map[string]interface{}
		_ = json.Unmarshal([]byte(metadataStr), &metadata)

		list = append(list, map[string]interface{}{
			"id":        id,
			"userId":    memberUserID,
			"name":      name,
			"email":     email,
			"status":    status,
			"metadata":  metadata,
			"createdAt": createdAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"memberships": list,
	})
}

func (h *FranchiseHandler) RequestMembership(c *gin.Context) {
	entityID := c.Param("id")
	userID := c.GetString("userId")

	claims := middleware.ExtractClaims(c)
	if claims != nil && userID == "" {
		userID = claims.UserID
	}

	if userID == "" {
		h.validationError(c, "User not authenticated")
		return
	}

	var input struct {
		Metadata map[string]interface{} `json:"metadata"`
	}
	_ = c.ShouldBindJSON(&input)

	metadataJSON, _ := json.Marshal(input.Metadata)
	if metadataJSON == nil {
		metadataJSON = []byte("{}")
	}

	var membershipID string
	err := h.db.QueryRowContext(c.Request.Context(), 
		`INSERT INTO memberships (user_id, entity_id, status, metadata) 
		 VALUES ($1, $2, 'PENDING', $3) 
		 ON CONFLICT (user_id, entity_id) DO UPDATE SET status = 'PENDING', metadata = $3, updated_at = NOW() 
		 RETURNING id`, userID, entityID, metadataJSON).Scan(&membershipID)

	if err != nil {
		h.internalError(c, "Failed to request membership", err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success":      true,
		"membershipId": membershipID,
		"status":       "PENDING",
		"message":      "Membership request submitted successfully",
	})
}

func (h *FranchiseHandler) UpdateMembershipStatus(c *gin.Context) {
	membershipID := c.Param("membershipId")
	userID := c.GetString("userId")

	var input struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.validationError(c, err.Error())
		return
	}

	if input.Status != "ACTIVE" && input.Status != "SUSPENDED" && input.Status != "CANCELLED" && input.Status != "PENDING" {
		h.validationError(c, "Invalid membership status")
		return
	}

	var entityID string
	err := h.db.QueryRowContext(c.Request.Context(), 
		"SELECT entity_id FROM memberships WHERE id = $1", membershipID).Scan(&entityID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Membership not found"})
		return
	}

	claims := middleware.ExtractClaims(c)
	isAdmin := false
	if claims != nil {
		if userID == "" {
			userID = claims.UserID
		}
		for _, r := range claims.Roles {
			if r == "SYSTEM_ADMIN" || r == "ADMIN" || r == "ROLE_PLATFORM_ADMIN" {
				isAdmin = true
				break
			}
		}
	}

	if !isAdmin {
		var createdBy string
		err := h.db.QueryRowContext(c.Request.Context(), 
			"SELECT created_by FROM franchises WHERE id = $1", entityID).Scan(&createdBy)
		if err != nil || createdBy != userID {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "Forbidden", "message": "Only admin or listing owner can manage memberships"})
			return
		}
	}

	_, err = h.db.ExecContext(c.Request.Context(), 
		"UPDATE memberships SET status = $1, updated_at = NOW() WHERE id = $2", input.Status, membershipID)
	if err != nil {
		h.internalError(c, "Failed to update membership status", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Membership status updated to " + input.Status,
	})
}

// ========================================================================
// ðŸŒ WEBSITE VERIFICATION (VC-02)
// ========================================================================

func (h *FranchiseHandler) VerifyWebsite(c *gin.Context) {
	entityID := c.Param("id")
	userID := c.GetString("userId")

	claims := middleware.ExtractClaims(c)
	isAdmin := false
	if claims != nil {
		if userID == "" {
			userID = claims.UserID
		}
		for _, r := range claims.Roles {
			if r == "SYSTEM_ADMIN" || r == "ADMIN" || r == "ROLE_PLATFORM_ADMIN" {
				isAdmin = true
				break
			}
		}
	}

	if !isAdmin {
		var createdBy string
		err := h.db.QueryRowContext(c.Request.Context(), 
			"SELECT created_by FROM franchises WHERE id = $1", entityID).Scan(&createdBy)
		if err != nil || createdBy != userID {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "Forbidden", "message": "Only admin or listing owner can initiate website verification"})
			return
		}
	}

	token := fmt.Sprintf("lemici-verification-%s", uuid.New().String()[:12])

	_, err := h.db.ExecContext(c.Request.Context(), 
		`INSERT INTO verification_criteria (entity_id, vc_type, status, details) 
		 VALUES ($1, 'VC-02', 'PENDING', jsonb_build_object('token', $2, 'initiated_at', NOW()))
		 ON CONFLICT (entity_id, vc_type) DO UPDATE 
		 SET status = 'PENDING', details = jsonb_build_object('token', $2, 'initiated_at', NOW()), updated_at = NOW()`,
		 entityID, token)

	if err != nil {
		h.internalError(c, "Failed to generate verification token", err)
		return
	}

	h.logger.Info("Website verification initiated", map[string]interface{}{
		"entityId": entityID,
		"token":    token,
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Website verification initiated. Please configure DNS TXT record or HTML meta tag.",
		"verification": gin.H{
			"type":      "VC-02",
			"status":    "PENDING",
			"txtRecord": token,
			"metaTagName": "lemici-verification",
			"metaTagValue": token,
		},
	})
}

// ========================================================================
// ðŸ›¡ï¸ PLATFORM ADMIN QUEUES & TRANSITIONS
// ========================================================================

func (h *FranchiseHandler) ListReviewQueue(c *gin.Context) {
	entityType := c.Param("entityType")
	et := strings.ToLower(strings.TrimSpace(entityType))
	switch et {
	case "franchises":
		et = "franchise"
	case "associations":
		et = "association"
	case "master-franchise", "master_franchises", "master franchises", "master franchise", "masterfranchise":
		et = "master_franchise"
	default:
		et = strings.TrimSuffix(et, "s")
		if et == "master-franchise" || et == "master franchise" || et == "masterfranchise" {
			et = "master_franchise"
		}
	}

	rows, err := h.db.QueryContext(c.Request.Context(), 
		`SELECT id, name, slug, status, created_at, created_by 
		 FROM franchises 
		 WHERE entity_type = $1 AND status != 'live' AND status != 'DELETED' 
		 ORDER BY created_at DESC`, et)
	if err != nil {
		h.internalError(c, "Failed to query review queue", err)
		return
	}
	defer rows.Close()

	var list []map[string]interface{}
	for rows.Next() {
		var id, name, slug, status, createdAt, createdBy string
		if err := rows.Scan(&id, &name, &slug, &status, &createdAt, &createdBy); err != nil {
			continue
		}
		list = append(list, map[string]interface{}{
			"id":        id,
			"name":      name,
			"slug":      slug,
			"status":    status,
			"createdAt": createdAt,
			"createdBy": createdBy,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"entities": list,
	})
}

func (h *FranchiseHandler) transitionEntityStatus(c *gin.Context, targetStatus string, reasonField string) {
	entityID := c.Param("id")
	actorID := c.GetString("userId")

	claims := middleware.ExtractClaims(c)
	if claims != nil && actorID == "" {
		actorID = claims.UserID
	}

	var input struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&input)

	if reasonField != "" && input.Reason == "" {
		h.validationError(c, reasonField+" is required")
		return
	}

	tx, err := h.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		h.internalError(c, "Failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	var oldStatus string
	var oldName string
	err = tx.QueryRowContext(c.Request.Context(), 
		"SELECT status, name FROM franchises WHERE id = $1 FOR UPDATE", entityID).Scan(&oldStatus, &oldName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Entity not found"})
		return
	}

	updateQuery := "UPDATE franchises SET status = $1, updated_at = NOW()"
	args := []interface{}{targetStatus}
	argPos := 2

	switch reasonField {
	case "rejection_reason":
		updateQuery += fmt.Sprintf(", rejection_reason = $%d", argPos)
		args = append(args, input.Reason)
		argPos++
	case "suspension_reason":
		updateQuery += fmt.Sprintf(", association_metadata = association_metadata || jsonb_build_object('suspension_reason', $%d)", argPos)
		args = append(args, input.Reason)
		argPos++
	}

	if targetStatus == "live" {
		updateQuery += ", approved_at = NOW()"
	}

	updateQuery += fmt.Sprintf(" WHERE id = $%d", argPos)
	args = append(args, entityID)

	_, err = tx.ExecContext(c.Request.Context(), updateQuery, args...)
	if err != nil {
		h.internalError(c, "Failed to update status", err)
		return
	}

	oldValuesJSON, _ := json.Marshal(map[string]string{"status": oldStatus})
	newValuesJSON, _ := json.Marshal(map[string]string{"status": targetStatus})
	var notes = targetStatus + " transition"
	if input.Reason != "" {
		notes += ": " + input.Reason
	}

	var actorUUID interface{}
	if actorID != "" {
		actorUUID = actorID
	} else {
		actorUUID = nil
	}

	_, err = tx.ExecContext(c.Request.Context(), 
		`INSERT INTO entity_audit_log (entity_id, action, actor_id, old_values, new_values, notes) 
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		 entityID, "STATUS_CHANGE", actorUUID, oldValuesJSON, newValuesJSON, notes)

	if err != nil {
		h.internalError(c, "Failed to write audit log", err)
		return
	}

	if err := tx.Commit(); err != nil {
		h.internalError(c, "Failed to commit status change", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Entity %s transition complete", targetStatus),
	})
}

func (h *FranchiseHandler) ApproveEntity(c *gin.Context) {
	h.transitionEntityStatus(c, "approved", "")
}

func (h *FranchiseHandler) RejectEntity(c *gin.Context) {
	h.transitionEntityStatus(c, "rejected", "rejection_reason")
}

func (h *FranchiseHandler) PublishEntity(c *gin.Context) {
	h.transitionEntityStatus(c, "live", "")
}

func (h *FranchiseHandler) SuspendEntity(c *gin.Context) {
	h.transitionEntityStatus(c, "suspended", "suspension_reason")
}

func (h *FranchiseHandler) ReinstateEntity(c *gin.Context) {
	h.transitionEntityStatus(c, "live", "")
}

func (h *FranchiseHandler) ArchiveEntity(c *gin.Context) {
	h.transitionEntityStatus(c, "archived", "")
}

func (h *FranchiseHandler) ApprovePendingEdit(c *gin.Context) {
	editID := c.Param("editId")
	actorID := c.GetString("userId")

	claims := middleware.ExtractClaims(c)
	if claims != nil && actorID == "" {
		actorID = claims.UserID
	}

	tx, err := h.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		h.internalError(c, "Failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	var entityID, fieldName string
	var newValueStr []byte
	err = tx.QueryRowContext(c.Request.Context(), 
		"SELECT entity_id, field_name, new_value FROM pending_edits WHERE id = $1 AND status = 'PENDING' FOR UPDATE", 
		editID).Scan(&entityID, &fieldName, &newValueStr)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Pending edit not found or already processed"})
		return
	}

	var newValue interface{}
	_ = json.Unmarshal(newValueStr, &newValue)

	var query string
	if s, ok := newValue.(string); ok {
		query = fmt.Sprintf("UPDATE franchises SET %s = $1, updated_at = NOW() WHERE id = $2", fieldName)
		_, err = tx.ExecContext(c.Request.Context(), query, s, entityID)
	} else {
		query = fmt.Sprintf("UPDATE franchises SET %s = $1, updated_at = NOW() WHERE id = $2", fieldName)
		_, err = tx.ExecContext(c.Request.Context(), query, newValueStr, entityID)
	}

	if err != nil {
		h.internalError(c, "Failed to apply pending edit", err)
		return
	}

	var actorUUID interface{}
	if actorID != "" {
		actorUUID = actorID
	} else {
		actorUUID = nil
	}

	_, err = tx.ExecContext(c.Request.Context(), 
		"UPDATE pending_edits SET status = 'APPROVED', reviewed_by = $1, reviewed_at = NOW(), updated_at = NOW() WHERE id = $2", 
		actorUUID, editID)
	if err != nil {
		h.internalError(c, "Failed to update pending edit status", err)
		return
	}

	if err := tx.Commit(); err != nil {
		h.internalError(c, "Failed to commit transaction", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Pending edit approved and applied successfully",
	})
}

func (h *FranchiseHandler) RejectPendingEdit(c *gin.Context) {
	editID := c.Param("editId")
	actorID := c.GetString("userId")

	claims := middleware.ExtractClaims(c)
	if claims != nil && actorID == "" {
		actorID = claims.UserID
	}

	var input struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.validationError(c, "rejection reason is required")
		return
	}

	var actorUUID interface{}
	if actorID != "" {
		actorUUID = actorID
	} else {
		actorUUID = nil
	}

	_, err := h.db.ExecContext(c.Request.Context(), 
		`UPDATE pending_edits 
		 SET status = 'REJECTED', reviewed_by = $1, reviewed_at = NOW(), rejection_reason = $2, updated_at = NOW() 
		 WHERE id = $3 AND status = 'PENDING'`, 
		actorUUID, input.Reason, editID)
	if err != nil {
		h.internalError(c, "Failed to reject pending edit", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Pending edit rejected successfully",
	})
}

func (h *FranchiseHandler) ResolveDuplicateFlag(c *gin.Context) {
	flagID := c.Param("flagId")
	actorID := c.GetString("userId")

	claims := middleware.ExtractClaims(c)
	if claims != nil && actorID == "" {
		actorID = claims.UserID
	}

	var input struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		h.validationError(c, err.Error())
		return
	}

	if input.Status != "RESOLVED" && input.Status != "IGNORED" {
		h.validationError(c, "Invalid resolution status")
		return
	}

	var actorUUID interface{}
	if actorID != "" {
		actorUUID = actorID
	} else {
		actorUUID = nil
	}

	_, err := h.db.ExecContext(c.Request.Context(), 
		`UPDATE duplicate_flags 
		 SET status = $1, resolved_by = $2, resolved_at = NOW() 
		 WHERE id = $3 AND status = 'PENDING'`, 
		input.Status, actorUUID, flagID)
	if err != nil {
		h.internalError(c, "Failed to resolve duplicate flag", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Duplicate flag resolved as " + input.Status,
	})
}
