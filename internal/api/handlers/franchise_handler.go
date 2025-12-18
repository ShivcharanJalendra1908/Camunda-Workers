// internal/api/handlers/franchise_handler.go
package handlers

import (
	"net/http"

	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/models"

	"github.com/gin-gonic/gin"
)

type FranchiseHandler struct {
	esClient *database.ElasticsearchClient
	pgClient *database.PostgresClient
	logger   logger.Logger
}

// ✅ FIXED: Constructor now accepts all required dependencies
func NewFranchiseHandler(esClient *database.ElasticsearchClient, pgClient *database.PostgresClient, log logger.Logger) *FranchiseHandler {
	return &FranchiseHandler{
		esClient: esClient,
		pgClient: pgClient,
		logger:   log,
	}
}

// Helper function to handle errors consistently
func (h *FranchiseHandler) handleError(c *gin.Context, status int, message string, err error) {
	h.logger.Error(message, map[string]interface{}{
		"error": err.Error(),
	})
	c.JSON(status, gin.H{
		"success": false,
		"error":   message,
		"details": err.Error(),
	})
}

// Helper function for validation errors
func (h *FranchiseHandler) validationError(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{
		"success": false,
		"error":   "Validation error",
		"message": message,
	})
}

// Helper function for not found errors
func (h *FranchiseHandler) notFoundError(c *gin.Context, message string) {
	c.JSON(http.StatusNotFound, gin.H{
		"success": false,
		"error":   "Not found",
		"message": message,
	})
}

// Helper function for internal errors
func (h *FranchiseHandler) internalError(c *gin.Context, message string, err error) {
	h.logger.Error(message, map[string]interface{}{
		"error": err.Error(),
	})
	c.JSON(http.StatusInternalServerError, gin.H{
		"success": false,
		"error":   "Internal server error",
		"message": message,
	})
}

// SearchFranchises handles franchise search with filters
func (h *FranchiseHandler) SearchFranchises(c *gin.Context) {
	var filters models.FranchiseSearchFilters

	if err := c.ShouldBindQuery(&filters); err != nil {
		h.validationError(c, "Invalid search parameters: "+err.Error())
		return
	}

	// Validate and set default values
	if filters.Page <= 0 {
		filters.Page = 1
	}
	if filters.Limit <= 0 {
		filters.Limit = 10
	}
	if filters.Limit > 100 {
		filters.Limit = 100
	}

	// Determine which index to search based on category
	indexName := "franchises_all" // default composite index
	if filters.Category != "" {
		switch filters.Category {
		case "food", "food & beverage":
			indexName = "food_beverage_franchises"
		case "education":
			indexName = "education_franchises"
		case "fashion":
			indexName = "fashion_franchises"
		}
	}

	// Build Elasticsearch query
	query := h.buildSearchQuery(filters)

	// Execute search
	result, err := h.esClient.Search(c.Request.Context(), indexName, query)
	if err != nil {
		h.internalError(c, "Search failed", err)
		return
	}

	// Process and format results
	franchises, total, err := h.processSearchResults(result, filters)
	if err != nil {
		h.internalError(c, "Failed to process results", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"franchises": franchises,
			"pagination": gin.H{
				"page":       filters.Page,
				"limit":      filters.Limit,
				"total":      total,
				"totalPages": (total + filters.Limit - 1) / filters.Limit,
			},
			"filters_applied": filters,
		},
	})
}

// GetByID retrieves a specific franchise (renamed from GetFranchiseByID for consistency)
func (h *FranchiseHandler) GetByID(c *gin.Context) {
	franchiseID := c.Param("id")
	if franchiseID == "" {
		h.validationError(c, "Franchise ID is required")
		return
	}

	// Try to find franchise in all indices
	indices := []string{
		"food_beverage_franchises",
		"education_franchises",
		"fashion_franchises",
	}

	var franchise map[string]interface{}
	var foundIndex string

	for _, index := range indices {
		result, err := h.esClient.Get(c.Request.Context(), index, franchiseID)
		if err == nil && result != nil {
			franchise = result
			foundIndex = index
			break
		}
	}

	if franchise == nil {
		h.notFoundError(c, "Franchise not found")
		return
	}

	// Add metadata about which index it was found in
	franchise["_index"] = foundIndex

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    franchise,
	})
}

// GetStats returns statistics and aggregations (renamed from GetFranchiseStats)
func (h *FranchiseHandler) GetStats(c *gin.Context) {
	stats := map[string]interface{}{
		"by_category": map[string]int{
			"food":      0,
			"education": 0,
			"fashion":   0,
		},
		"investment_ranges": map[string]string{
			"low":       "₹2-10 Lakhs",
			"medium":    "₹10-30 Lakhs",
			"high":      "₹30+ Lakhs",
			"very_high": "₹50+ Lakhs",
		},
		"space_requirements": map[string]string{
			"small":  "50-200 sq ft",
			"medium": "200-500 sq ft",
			"large":  "500-1000 sq ft",
			"xlarge": "1000+ sq ft",
		},
	}

	// Get actual counts from Elasticsearch
	indices := []string{"food_beverage_franchises", "education_franchises", "fashion_franchises"}

	for _, index := range indices {
		count, err := h.esClient.Count(c.Request.Context(), index, map[string]interface{}{
			"query": map[string]interface{}{
				"match_all": map[string]interface{}{},
			},
		})

		if err == nil {
			switch index {
			case "food_beverage_franchises":
				stats["by_category"].(map[string]int)["food"] = int(count)
			case "education_franchises":
				stats["by_category"].(map[string]int)["education"] = int(count)
			case "fashion_franchises":
				stats["by_category"].(map[string]int)["fashion"] = int(count)
			}
		} else {
			h.logger.Warn("Failed to count documents", map[string]interface{}{
				"index": index,
				"error": err.Error(),
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}

// GetSuggestions provides autocomplete suggestions (renamed from GetFranchiseSuggestions)
func (h *FranchiseHandler) GetSuggestions(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    []string{},
		})
		return
	}

	// Build completion/suggest query
	suggestQuery := map[string]interface{}{
		"suggest": map[string]interface{}{
			"brand_suggest": map[string]interface{}{
				"prefix": query,
				"completion": map[string]interface{}{
					"field":           "brand.suggest",
					"skip_duplicates": true,
					"size":            10,
				},
			},
		},
	}

	// Search across all indices
	indices := []string{
		"food_beverage_franchises",
		"education_franchises",
		"fashion_franchises",
	}

	var suggestions []string

	for _, index := range indices {
		result, err := h.esClient.Search(c.Request.Context(), index, suggestQuery)
		if err == nil {
			if suggests, ok := result["suggest"].(map[string]interface{}); ok {
				if brandSuggests, ok := suggests["brand_suggest"].([]interface{}); ok {
					for _, suggestion := range brandSuggests {
						if sMap, ok := suggestion.(map[string]interface{}); ok {
							if options, ok := sMap["options"].([]interface{}); ok {
								for _, option := range options {
									if optMap, ok := option.(map[string]interface{}); ok {
										if text, ok := optMap["text"].(string); ok && text != "" {
											suggestions = append(suggestions, text)
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Remove duplicates and limit to 10
	suggestions = h.removeDuplicates(suggestions)
	if len(suggestions) > 10 {
		suggestions = suggestions[:10]
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    suggestions,
	})
}

// ✅ NEW: GetCategories returns all available franchise categories
func (h *FranchiseHandler) GetCategories(c *gin.Context) {
	categories := []map[string]interface{}{
		{
			"id":          "food",
			"name":        "Food & Beverage",
			"description": "Restaurants, cafes, and food services",
			"icon":        "🍔",
		},
		{
			"id":          "education",
			"name":        "Education",
			"description": "Educational services and training centers",
			"icon":        "📚",
		},
		{
			"id":          "fashion",
			"name":        "Fashion",
			"description": "Clothing, accessories, and fashion retail",
			"icon":        "👗",
		},
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    categories,
	})
}

// ✅ NEW: GetFeatured returns featured franchises
func (h *FranchiseHandler) GetFeatured(c *gin.Context) {
	// Query for featured franchises across all indices
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"term": map[string]interface{}{
							"featured": true,
						},
					},
				},
			},
		},
		"size": 10,
		"sort": []map[string]interface{}{
			{"rating": map[string]interface{}{"order": "desc"}},
			{"popularity": map[string]interface{}{"order": "desc", "missing": "_last"}},
		},
	}

	indices := []string{
		"food_beverage_franchises",
		"education_franchises",
		"fashion_franchises",
	}

	var allFeatured []map[string]interface{}

	for _, index := range indices {
		result, err := h.esClient.Search(c.Request.Context(), index, query)
		if err != nil {
			h.logger.Error("Failed to fetch featured franchises", map[string]interface{}{
				"error": err.Error(),
				"index": index,
			})
			continue
		}

		franchises, _, _ := h.processSearchResults(result, models.FranchiseSearchFilters{})
		allFeatured = append(allFeatured, franchises...)
	}

	// Limit to 10 total
	if len(allFeatured) > 10 {
		allFeatured = allFeatured[:10]
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    allFeatured,
	})
}

// ============================================================================
// USER-SPECIFIC OPERATIONS (Require PostgreSQL)
// ============================================================================

// ✅ NEW: AddToFavorites adds a franchise to user's favorites
func (h *FranchiseHandler) AddToFavorites(c *gin.Context) {
	franchiseID := c.Param("id")
	userID := c.GetString("userId") // From JWT middleware

	if franchiseID == "" || userID == "" {
		h.validationError(c, "Franchise ID and User ID are required")
		return
	}

	// Insert into favorites table
	query := `
		INSERT INTO user_favorites (user_id, franchise_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id, franchise_id) DO NOTHING
		RETURNING id
	`

	var favoriteID int64
	err := h.pgClient.QueryRow(c.Request.Context(), query, userID, franchiseID).Scan(&favoriteID)
	if err != nil {
		h.internalError(c, "Failed to add favorite", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Franchise added to favorites",
		"data": gin.H{
			"favoriteId":  favoriteID,
			"franchiseId": franchiseID,
		},
	})
}

// ✅ NEW: RemoveFromFavorites removes a franchise from user's favorites
func (h *FranchiseHandler) RemoveFromFavorites(c *gin.Context) {
	franchiseID := c.Param("id")
	userID := c.GetString("userId")

	if franchiseID == "" || userID == "" {
		h.validationError(c, "Franchise ID and User ID are required")
		return
	}

	query := `DELETE FROM user_favorites WHERE user_id = $1 AND franchise_id = $2`

	_, err := h.pgClient.Exec(c.Request.Context(), query, userID, franchiseID)
	if err != nil {
		h.internalError(c, "Failed to remove favorite", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Franchise removed from favorites",
	})
}

// ✅ NEW: GetFavorites retrieves user's favorite franchises
func (h *FranchiseHandler) GetFavorites(c *gin.Context) {
	userID := c.GetString("userId")

	if userID == "" {
		h.validationError(c, "User ID is required")
		return
	}

	query := `
		SELECT franchise_id, created_at
		FROM user_favorites
		WHERE user_id = $1
		ORDER BY created_at DESC
	`

	rows, err := h.pgClient.Query(c.Request.Context(), query, userID)
	if err != nil {
		h.internalError(c, "Failed to fetch favorites", err)
		return
	}
	defer rows.Close()

	var franchiseIDs []string
	for rows.Next() {
		var franchiseID string
		var createdAt interface{}
		if err := rows.Scan(&franchiseID, &createdAt); err == nil {
			franchiseIDs = append(franchiseIDs, franchiseID)
		}
	}

	// Fetch franchise details from Elasticsearch
	var favorites []map[string]interface{}
	for _, id := range franchiseIDs {
		// Try each index
		indices := []string{
			"food_beverage_franchises",
			"education_franchises",
			"fashion_franchises",
		}

		for _, index := range indices {
			result, err := h.esClient.Get(c.Request.Context(), index, id)
			if err == nil && result != nil {
				result["_id"] = id
				favorites = append(favorites, result)
				break
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    favorites,
	})
}

// ✅ NEW: SaveSearch saves a user's search query for future reference
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

	query := `
		INSERT INTO saved_searches (user_id, name, filters, created_at)
		VALUES ($1, $2, $3, NOW())
		RETURNING id
	`

	var searchID int64
	err := h.pgClient.QueryRow(c.Request.Context(), query, userID, input.Name, input.Filters).Scan(&searchID)
	if err != nil {
		h.internalError(c, "Failed to save search", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Search saved successfully",
		"data": gin.H{
			"searchId": searchID,
			"name":     input.Name,
		},
	})
}

// ✅ NEW: GetSavedSearches retrieves user's saved searches
func (h *FranchiseHandler) GetSavedSearches(c *gin.Context) {
	userID := c.GetString("userId")

	query := `
		SELECT id, name, filters, created_at
		FROM saved_searches
		WHERE user_id = $1
		ORDER BY created_at DESC
	`

	rows, err := h.pgClient.Query(c.Request.Context(), query, userID)
	if err != nil {
		h.internalError(c, "Failed to fetch saved searches", err)
		return
	}
	defer rows.Close()

	var searches []map[string]interface{}
	for rows.Next() {
		var id int64
		var name string
		var filters interface{}
		var createdAt interface{}

		if err := rows.Scan(&id, &name, &filters, &createdAt); err == nil {
			searches = append(searches, map[string]interface{}{
				"id":        id,
				"name":      name,
				"filters":   filters,
				"createdAt": createdAt,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    searches,
	})
}

// ✅ NEW: DeleteSavedSearch deletes a saved search
func (h *FranchiseHandler) DeleteSavedSearch(c *gin.Context) {
	searchID := c.Param("id")
	userID := c.GetString("userId")

	query := `DELETE FROM saved_searches WHERE id = $1 AND user_id = $2`

	_, err := h.pgClient.Exec(c.Request.Context(), query, searchID, userID)
	if err != nil {
		h.internalError(c, "Failed to delete saved search", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Saved search deleted successfully",
	})
}

// ============================================================================
// HELPER METHODS
// ============================================================================

// Helper method to build Elasticsearch query
func (h *FranchiseHandler) buildSearchQuery(filters models.FranchiseSearchFilters) map[string]interface{} {
	mustQueries := []map[string]interface{}{}

	// Text search on brand and description
	if filters.Query != "" {
		mustQueries = append(mustQueries, map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":     filters.Query,
				"fields":    []string{"brand^3", "description^2", "tags", "highlights"},
				"type":      "best_fields",
				"fuzziness": "AUTO",
			},
		})
	}

	// Category filter
	if filters.Category != "" {
		mustQueries = append(mustQueries, map[string]interface{}{
			"term": map[string]interface{}{
				"category.keyword": filters.Category,
			},
		})
	}

	// Location filter
	if filters.Location != "" {
		mustQueries = append(mustQueries, map[string]interface{}{
			"term": map[string]interface{}{
				"location.keyword": filters.Location,
			},
		})
	}

	// Investment range filter
	if filters.MinInvestment > 0 || filters.MaxInvestment > 0 {
		rangeFilter := map[string]interface{}{}
		if filters.MinInvestment > 0 {
			rangeFilter["gte"] = filters.MinInvestment
		}
		if filters.MaxInvestment > 0 {
			rangeFilter["lte"] = filters.MaxInvestment
		}
		mustQueries = append(mustQueries, map[string]interface{}{
			"range": map[string]interface{}{
				"investment_min": rangeFilter,
			},
		})
	}

	// Space range filter
	if filters.MinSpace > 0 || filters.MaxSpace > 0 {
		rangeFilter := map[string]interface{}{}
		if filters.MinSpace > 0 {
			rangeFilter["gte"] = filters.MinSpace
		}
		if filters.MaxSpace > 0 {
			rangeFilter["lte"] = filters.MaxSpace
		}
		mustQueries = append(mustQueries, map[string]interface{}{
			"range": map[string]interface{}{
				"space_min": rangeFilter,
			},
		})
	}

	// Tags filter
	if len(filters.Tags) > 0 {
		for _, tag := range filters.Tags {
			mustQueries = append(mustQueries, map[string]interface{}{
				"term": map[string]interface{}{
					"tags.keyword": tag,
				},
			})
		}
	}

	// Minimum rating filter
	if filters.MinRating > 0 {
		mustQueries = append(mustQueries, map[string]interface{}{
			"range": map[string]interface{}{
				"rating": map[string]interface{}{
					"gte": filters.MinRating,
				},
			},
		})
	}

	// Build final query
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": mustQueries,
			},
		},
		"from": (filters.Page - 1) * filters.Limit,
		"size": filters.Limit,
		"sort": []map[string]interface{}{
			{"_score": map[string]interface{}{"order": "desc"}},
			{"rating": map[string]interface{}{"order": "desc", "missing": "_last"}},
		},
		"aggs": map[string]interface{}{
			"by_category": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "category.keyword",
					"size":  10,
				},
			},
			"by_location": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "location.keyword",
					"size":  10,
				},
			},
			"investment_stats": map[string]interface{}{
				"stats": map[string]interface{}{
					"field": "investment_min",
				},
			},
		},
	}

	return query
}

// Helper method to process search results
func (h *FranchiseHandler) processSearchResults(result map[string]interface{}, filters models.FranchiseSearchFilters) ([]map[string]interface{}, int, error) {
	var franchises []map[string]interface{}
	var total int

	// Extract hits
	if hits, ok := result["hits"].(map[string]interface{}); ok {
		// Get total count
		if totalVal, ok := hits["total"].(map[string]interface{}); ok {
			if value, ok := totalVal["value"].(float64); ok {
				total = int(value)
			}
		}

		// Extract franchise documents
		if hitsList, ok := hits["hits"].([]interface{}); ok {
			for _, hit := range hitsList {
				if hitMap, ok := hit.(map[string]interface{}); ok {
					if source, ok := hitMap["_source"].(map[string]interface{}); ok {
						// Add Elasticsearch metadata
						source["_id"] = hitMap["_id"]
						source["_score"] = hitMap["_score"]
						source["_index"] = hitMap["_index"]

						franchises = append(franchises, source)
					}
				}
			}
		}
	}

	return franchises, total, nil
}

// Helper to remove duplicates from slice
func (h *FranchiseHandler) removeDuplicates(slice []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range slice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

