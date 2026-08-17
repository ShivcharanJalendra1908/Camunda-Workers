package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"camunda-workers/internal/common/camunda"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/validation"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type BlogHandler struct {
	camundaClient *camunda.Client
	logger        logger.Logger
	redisClient   *redis.Client
	validator     *validation.Validator
	sanitizer     *validation.Sanitizer
	paginationCfg config.PaginationConfig
	db            *sql.DB
	operationsEmail string
}

func NewBlogHandler(
	camundaClient *camunda.Client,
	log logger.Logger,
	redisClient *redis.Client,
	paginationCfg config.PaginationConfig,
	db *sql.DB,
	operationsEmail string,
) *BlogHandler {
	return &BlogHandler{
		camundaClient: camundaClient,
		logger:        log,
		redisClient:   redisClient,
		validator:     validation.NewValidator(),
		sanitizer:     validation.NewSanitizer(),
		paginationCfg: paginationCfg,
		db:            db,
		operationsEmail: operationsEmail,
	}
}

func (h *BlogHandler) executeWorkflow(
	ctx context.Context,
	processID string,
	variables map[string]interface{},
) (map[string]interface{}, error) {

	correlationKey := variables["correlationKey"].(string)
	channel := fmt.Sprintf("workflow:response:%s", correlationKey)

	pubsub := h.redisClient.Subscribe(ctx, channel)
	defer pubsub.Close()

	subCtx, subCancel := context.WithTimeout(ctx, 5*time.Second)
	defer subCancel()
	if _, err := pubsub.Receive(subCtx); err != nil {
		h.logger.Warn("subscription confirm timeout", map[string]interface{}{
			"channel": channel, "error": err.Error(),
		})
	}

	_, err := h.camundaClient.StartProcessInstance(ctx, processID, variables)
	if err != nil {
		return nil, fmt.Errorf("failed to start workflow: %w", err)
	}

	msg, err := pubsub.ReceiveMessage(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to receive workflow response: %w", err)
	}

	var response map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Payload), &response); err != nil {
		return nil, fmt.Errorf("invalid JSON from workflow: %w", err)
	}

	return response, nil
}

// CreateBlog handles POST /api/v1/blog
func (h *BlogHandler) CreateBlog(c *gin.Context) {
	var reqBody map[string]interface{}
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON format"})
		return
	}

	// Retrieve authenticated user
	userID := c.GetString("userId")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	reqBody["created_by"] = userID

	correlationKey := uuid.New().String()
	reqBody["operation_type"] = "CREATE_BLOG"

	payloadBytes, _ := json.Marshal(reqBody)

	variables := map[string]interface{}{
		"payload":         string(payloadBytes),
		"correlationKey":  correlationKey,
		"entityType":      "blog",
		"operationsEmail": h.operationsEmail,
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	response, err := h.executeWorkflow(ctx, "blog-submission-workflow", variables)
	if err != nil {
		h.logger.Error("Workflow execution failed", map[string]interface{}{"error": err.Error()})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process request"})
		return
	}

	if success, _ := response["success"].(bool); !success {
		errMsg := "Unknown error"
		if msg, ok := response["error_message"].(string); ok {
			errMsg = msg
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": errMsg})
		return
	}

	if dbResultStr, ok := response["db_result"].(string); ok && dbResultStr != "" {
		c.Data(http.StatusCreated, "application/json", []byte(dbResultStr))
		return
	}
	c.JSON(http.StatusCreated, response["db_result"])
}

func (h *BlogHandler) genericWorkflowSubmit(c *gin.Context, operation string) {
	var reqBody map[string]interface{}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&reqBody); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON format"})
			return
		}
	} else {
		reqBody = make(map[string]interface{})
	}

	userID := c.GetString("userId")
	if userID != "" {
		reqBody["requested_by"] = userID
	}

	// Capture path params (e.g. blog_id, author_id)
	for _, param := range c.Params {
		reqBody[param.Key] = param.Value
	}

	correlationKey := uuid.New().String()
	reqBody["operation_type"] = operation
	payloadBytes, _ := json.Marshal(reqBody)

	variables := map[string]interface{}{
		"payload":        string(payloadBytes),
		"operation_type": operation,
		"correlationKey": correlationKey,
		"entityType":     "blog",
		"operationsEmail": h.operationsEmail,
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	response, err := h.executeWorkflow(ctx, "blog-user-actions", variables)
	if err != nil {
		h.logger.Error("Workflow execution failed", map[string]interface{}{"error": err.Error()})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process request"})
		return
	}

	if success, _ := response["success"].(bool); !success {
		errMsg := "Unknown error"
		if msg, ok := response["error_message"].(string); ok {
			errMsg = msg
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": errMsg})
		return
	}

	if dbResultStr, ok := response["db_result"].(string); ok && dbResultStr != "" {
		c.Data(http.StatusOK, "application/json", []byte(dbResultStr))
		return
	}
	c.JSON(http.StatusOK, response["db_result"])
}

func (h *BlogHandler) UpdateBlog(c *gin.Context) {
	h.genericWorkflowSubmit(c, "UPDATE_BLOG")
}

func (h *BlogHandler) SubscribeNewsletter(c *gin.Context) {
	h.genericWorkflowSubmit(c, "CREATE_SUBSCRIBER")
}

func (h *BlogHandler) FollowAuthor(c *gin.Context) {
	h.genericWorkflowSubmit(c, "CREATE_FOLLOWER")
}

func (h *BlogHandler) UnfollowAuthor(c *gin.Context) {
	h.genericWorkflowSubmit(c, "DELETE_FOLLOWER")
}

// GET endpoints — ALL go through Zeebe workflows (same pattern as FranchiseHandler)

// GetHomeSections handles GET /api/v1/blog/home
// BPMN: blog-home-page (fetches featured + popular in parallel)
func (h *BlogHandler) GetHomeSections(c *gin.Context) {
	ctx := c.Request.Context()
	correlationKey := fmt.Sprintf("blog_home_%s", uuid.New().String()[:8])

	variables := map[string]interface{}{
		"correlationKey": correlationKey,
		"entityType":     "blog",
	}

	response, err := h.executeWorkflow(ctx, "blog-home-page", variables)
	if err != nil {
		h.logger.Error("blog-home-page workflow failed", map[string]interface{}{"error": err.Error()})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch blog home data"})
		return
	}
	c.JSON(http.StatusOK, response)
}

// GetListing handles GET /api/v1/blog/listing
// BPMN: blog-listing-page (fetches blog list + popular sidebar in parallel)
// Query params: ?search=, ?categorySlug=, ?page=, ?page_size=
func (h *BlogHandler) GetListing(c *gin.Context) {
	ctx := c.Request.Context()
	correlationKey := fmt.Sprintf("blog_listing_%s", uuid.New().String()[:8])

	page := 1
	pageSize := 6
	if p := c.Query("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}
	if ps := c.Query("page_size"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n > 0 && n <= 50 {
			pageSize = n
		}
	}

	variables := map[string]interface{}{
		"correlationKey": correlationKey,
		"entityType":     "blog",
		"search":         c.Query("search"),
		"categoryId":     c.Query("categorySlug"),
		"page":           page,
		"pageSize":       pageSize,
		"offset":         (page - 1) * pageSize,
	}

	response, err := h.executeWorkflow(ctx, "blog-listing-page", variables)
	if err != nil {
		h.logger.Error("blog-listing-page workflow failed", map[string]interface{}{"error": err.Error()})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch blog listing"})
		return
	}
	c.JSON(http.StatusOK, response)
}

// GetSingleBlog handles GET /api/v1/blog/:id
// BPMN: blog-detail-page (fetches full blog + related articles)
func (h *BlogHandler) GetSingleBlog(c *gin.Context) {
	ctx := c.Request.Context()
	blogID := c.Param("id")
	slug := c.Param("slug")
	identifier := blogID
	if identifier == "" {
		identifier = slug
	}

	correlationKey := fmt.Sprintf("blog_detail_%s", uuid.New().String()[:8])

	variables := map[string]interface{}{
		"correlationKey": correlationKey,
		"entityType":     "blog",
		"blogId":         identifier,
		"slug":           identifier,
	}

	response, err := h.executeWorkflow(ctx, "blog-detail-page", variables)
	if err != nil {
		h.logger.Error("blog-detail-page workflow failed", map[string]interface{}{"error": err.Error()})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch blog detail"})
		return
	}
	c.JSON(http.StatusOK, response)
}

// GetFeatured handles GET /api/v1/blog/featured
// Used for: Independent UI widgets (e.g., sidebar) requesting only featured blogs
func (h *BlogHandler) GetFeatured(c *gin.Context) {
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT l.id, l.name, l.slug, l.short_description,
		       b.featured_image_url, b.reading_time_mins, b.author_display_name
		FROM listings l
		JOIN blogs b ON b.id = l.id
		WHERE l.entity_type = 'blog' AND l.status = 'LIVE' AND l.is_featured = TRUE
		ORDER BY l.featured_order ASC NULLS LAST, l.created_at DESC
		LIMIT 6
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch featured blogs"})
		return
	}
	defer rows.Close()
	var blogs []map[string]interface{}
	for rows.Next() {
		var id, title, slug string
		var shortDesc, image, author sql.NullString
		var readingTime int
		rows.Scan(&id, &title, &slug, &shortDesc, &image, &readingTime, &author)
		blogs = append(blogs, map[string]interface{}{
			"id": id, "title": title, "slug": slug,
			"short_description":   shortDesc.String,
			"featured_image_url":  image.String,
			"reading_time_mins":   readingTime,
			"author_display_name": author.String,
		})
	}
	if blogs == nil {
		blogs = []map[string]interface{}{}
	}
	c.JSON(http.StatusOK, gin.H{"data": blogs, "success": true})
}

// GetPopular handles GET /api/v1/blog/popular
// Used for: Independent UI widgets requesting only popular blogs
func (h *BlogHandler) GetPopular(c *gin.Context) {
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT l.id, l.name, l.slug, l.short_description,
		       b.featured_image_url, b.reading_time_mins, b.author_display_name,
		       COALESCE(ls.view_count, 0) AS view_count
		FROM listings l
		JOIN blogs b ON b.id = l.id
		LEFT JOIN listing_stats ls ON ls.listing_id = l.id
		WHERE l.entity_type = 'blog' AND l.status = 'LIVE'
		ORDER BY ls.view_count DESC NULLS LAST, l.created_at DESC
		LIMIT 6
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch popular blogs"})
		return
	}
	defer rows.Close()
	var blogs []map[string]interface{}
	for rows.Next() {
		var id, title, slug string
		var shortDesc, image, author sql.NullString
		var readingTime int
		var viewCount int64
		rows.Scan(&id, &title, &slug, &shortDesc, &image, &readingTime, &author, &viewCount)
		blogs = append(blogs, map[string]interface{}{
			"id": id, "title": title, "slug": slug,
			"short_description":   shortDesc.String,
			"featured_image_url":  image.String,
			"reading_time_mins":   readingTime,
			"author_display_name": author.String,
			"view_count":          viewCount,
		})
	}
	if blogs == nil {
		blogs = []map[string]interface{}{}
	}
	c.JSON(http.StatusOK, gin.H{"data": blogs, "success": true})
}
