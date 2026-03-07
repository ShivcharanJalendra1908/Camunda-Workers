package franchiseesindexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TaskType = "franchise-es-indexer"
	ESIndex  = "franchises_index"
)

type Handler struct {
	config       *Config
	db           *sql.DB
	esClient     *database.ElasticsearchClient
	logger       logger.Logger
	errorHandler *errors.ErrorHandler
}

func NewHandler(cfg *Config, db *sql.DB, es *database.ElasticsearchClient, log logger.Logger) *Handler {
	return &Handler{
		config:       cfg,
		db:           db,
		esClient:     es,
		logger:       log,
		errorHandler: errors.NewErrorHandler(log),
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	ctx := context.Background()
	tracer := otel.Tracer("worker-manager")
	ctx, span := tracer.Start(ctx, "worker:"+TaskType,
		trace.WithAttributes(
			attribute.String("worker.name", TaskType),
			attribute.Int64("job.key", job.GetKey()),
		),
	)
	defer span.End()

	h.logger.Info("Processing ES indexer job", map[string]interface{}{
		"job_id": job.GetKey(),
	})

	// Parse input
	input, err := h.parseInput(job)
	if err != nil {
		span.RecordError(err)
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// Fetch franchise data from Postgres
	franchiseDoc, err := h.buildESDocument(ctx, input.FranchiseID)
	if err != nil {
		span.RecordError(err)
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// Index to Elasticsearch
	err = h.indexToES(ctx, input.FranchiseID, franchiseDoc)
	if err != nil {
		span.RecordError(err)
		h.errorHandler.HandleJobError(ctx, client, job, err)
		return
	}

	// Complete job
	output := &Output{
		FranchiseID: input.FranchiseID,
		Indexed:     true,
		IndexName:   ESIndex,
		Success:     true,
	}

	h.completeJob(ctx, client, job, output)

	h.logger.Info("ES indexer job completed", map[string]interface{}{
		"job_id":       job.GetKey(),
		"franchise_id": input.FranchiseID,
	})
}

// func (h *Handler) parseInput(job entities.Job) (*Input, error) {
// 	var vars map[string]interface{}
// 	if err := json.Unmarshal([]byte(job.Variables), &vars); err != nil {
// 		return nil, fmt.Errorf("failed to unmarshal variables: %w", err)
// 	}

// 	franchiseID, ok := vars["franchise_id"].(string)
// 	if !ok || franchiseID == "" {
// 		return nil, errors.NewValidationError("franchise_id", "required")
// 	}

// 	operation := "INDEX"
// 	if op, ok := vars["operation"].(string); ok {
// 		operation = op
// 	}

// 	return &Input{
// 		FranchiseID: franchiseID,
// 		Operation:   operation,
// 	}, nil
// }

// ADD this new method to handler.go in franchise-es-indexer package

func (h *Handler) parseInputFromVariables(variables string) (*Input, error) {
	var vars map[string]interface{}
	if err := json.Unmarshal([]byte(variables), &vars); err != nil {
		return nil, fmt.Errorf("failed to unmarshal variables: %w", err)
	}

	franchiseID, ok := vars["franchise_id"].(string)
	if !ok || franchiseID == "" {
		return nil, errors.NewValidationError("franchise_id", "required")
	}

	operation := "INDEX"
	if op, ok := vars["operation"].(string); ok {
		operation = op
	}

	return &Input{
		FranchiseID: franchiseID,
		Operation:   operation,
	}, nil
}

// REPLACE existing parseInput to delegate to the new method:
func (h *Handler) parseInput(job entities.Job) (*Input, error) {
	return h.parseInputFromVariables(job.Variables)
}

func (h *Handler) buildESDocument(ctx context.Context, franchiseID string) (map[string]interface{}, error) {
	doc := make(map[string]interface{})

	// Main franchise data
	query := `
		SELECT 
			f.id, f.name, f.slug, f.short_description, f.description,
			f.founded_year, f.trusted_seller, f.verified, f.total_outlets,
			f.parent_company, f.business_type, f.logo_url,
			f.created_at, f.updated_at
		FROM franchises f
		WHERE f.id = $1
	`

	var (
		id, name, slug                                      string
		shortDesc, description, parentCompany, businessType sql.NullString
		foundedYear, totalOutlets                           sql.NullInt32
		trustedSeller, verified                             bool
		logoURL                                             sql.NullString
		createdAt, updatedAt                                time.Time
	)

	err := h.db.QueryRowContext(ctx, query, franchiseID).Scan(
		&id, &name, &slug, &shortDesc, &description,
		&foundedYear, &trustedSeller, &verified, &totalOutlets,
		&parentCompany, &businessType, &logoURL,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch franchise: %w", err)
	}

	doc["franchise_id"] = id
	doc["name"] = name
	doc["slug"] = slug
	doc["short_description"] = shortDesc.String
	doc["description"] = description.String
	doc["trusted_seller"] = trustedSeller
	doc["verified"] = verified
	doc["created_at"] = createdAt
	doc["updated_at"] = updatedAt

	if foundedYear.Valid {
		doc["founded_year"] = foundedYear.Int32
	}
	if totalOutlets.Valid {
		doc["total_outlets"] = totalOutlets.Int32
	}
	if parentCompany.Valid {
		doc["parent_company"] = parentCompany.String
	}
	if businessType.Valid {
		doc["business_type"] = businessType.String
	}
	if logoURL.Valid {
		doc["logo_url"] = logoURL.String
	}

	// Industry
	industry, err := h.getIndustry(ctx, franchiseID)
	if err == nil && industry != nil {
		doc["industry"] = industry
	}

	// Categories
	categories, err := h.getCategories(ctx, franchiseID)
	if err == nil && len(categories) > 0 {
		doc["categories"] = categories
	}

	// Cities
	cities, err := h.getCities(ctx, franchiseID)
	if err == nil && len(cities) > 0 {
		doc["cities"] = cities
	}

	// Investment
	investment, err := h.getInvestment(ctx, franchiseID)
	if err == nil && investment != nil {
		doc["investment"] = investment
	}

	// Operations
	operations, err := h.getOperations(ctx, franchiseID)
	if err == nil && operations != nil {
		doc["operations"] = operations
	}

	// Stats
	stats, err := h.getStats(ctx, franchiseID)
	if err == nil && stats != nil {
		doc["stats"] = stats
	}

	// Business Overview
	overview, err := h.getBusinessOverview(ctx, franchiseID)
	if err == nil && overview != nil {
		doc["business_overview"] = overview
	}

	return doc, nil
}

func (h *Handler) getIndustry(ctx context.Context, franchiseID string) (map[string]interface{}, error) {
	query := `
		SELECT DISTINCT i.id, i.name, i.slug, i.color_hex
		FROM industries i
		INNER JOIN categories c ON c.industry_id = i.id
		INNER JOIN franchise_categories fc ON fc.category_id = c.id
		WHERE fc.franchise_id = $1 AND fc.is_primary = true
		LIMIT 1
	`

	var id, name, slug, colorHex string
	err := h.db.QueryRowContext(ctx, query, franchiseID).Scan(&id, &name, &slug, &colorHex)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"id":        id,
		"name":      name,
		"slug":      slug,
		"color_hex": colorHex,
	}, nil
}

func (h *Handler) getCategories(ctx context.Context, franchiseID string) ([]map[string]interface{}, error) {
	query := `
		SELECT 
			c.id, c.name, c.slug, fc.is_primary,
			sc.id, sc.name, sc.slug
		FROM franchise_categories fc
		INNER JOIN categories c ON fc.category_id = c.id
		LEFT JOIN sub_categories sc ON fc.sub_category_id = sc.id
		WHERE fc.franchise_id = $1
		ORDER BY fc.is_primary DESC
	`

	rows, err := h.db.QueryContext(ctx, query, franchiseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []map[string]interface{}
	for rows.Next() {
		var (
			catID, catName, catSlug string
			isPrimary               bool
			subID, subName, subSlug sql.NullString
		)

		err := rows.Scan(&catID, &catName, &catSlug, &isPrimary, &subID, &subName, &subSlug)
		if err != nil {
			continue
		}

		cat := map[string]interface{}{
			"id":         catID,
			"name":       catName,
			"slug":       catSlug,
			"is_primary": isPrimary,
		}

		if subID.Valid {
			cat["sub_category"] = map[string]interface{}{
				"id":   subID.String,
				"name": subName.String,
				"slug": subSlug.String,
			}
		}

		categories = append(categories, cat)
	}

	return categories, nil
}

func (h *Handler) getCities(ctx context.Context, franchiseID string) ([]string, error) {
	query := `SELECT DISTINCT city FROM franchise_cities WHERE franchise_id = $1`
	rows, err := h.db.QueryContext(ctx, query, franchiseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cities []string
	for rows.Next() {
		var city string
		if err := rows.Scan(&city); err == nil {
			cities = append(cities, city)
		}
	}
	return cities, nil
}

func (h *Handler) getInvestment(ctx context.Context, franchiseID string) (map[string]interface{}, error) {
	query := `
		SELECT initial_investment_min, initial_investment_max, 
		       franchise_fee, royalty_percentage
		FROM franchise_investment_requirement
		WHERE franchise_id = $1
	`

	var min, max, fee, royalty sql.NullFloat64
	err := h.db.QueryRowContext(ctx, query, franchiseID).Scan(&min, &max, &fee, &royalty)
	if err != nil {
		return nil, err
	}

	inv := make(map[string]interface{})
	if min.Valid {
		inv["min"] = min.Float64
	}
	if max.Valid {
		inv["max"] = max.Float64
	}
	if fee.Valid {
		inv["franchise_fee"] = fee.Float64
	}
	if royalty.Valid {
		inv["royalty_percentage"] = royalty.Float64
	}

	return inv, nil
}

func (h *Handler) getOperations(ctx context.Context, franchiseID string) (map[string]interface{}, error) {
	query := `
		SELECT space_min_sqft, space_max_sqft, 
		       staff_required_min, staff_required_max, training_provided
		FROM franchise_operations
		WHERE franchise_id = $1
	`

	var spaceMin, spaceMax, staffMin, staffMax sql.NullInt32
	var training sql.NullBool
	err := h.db.QueryRowContext(ctx, query, franchiseID).Scan(&spaceMin, &spaceMax, &staffMin, &staffMax, &training)
	if err != nil {
		return nil, err
	}

	ops := make(map[string]interface{})
	if spaceMin.Valid {
		ops["space_min"] = spaceMin.Int32
	}
	if spaceMax.Valid {
		ops["space_max"] = spaceMax.Int32
	}
	if staffMin.Valid {
		ops["staff_required_min"] = staffMin.Int32
	}
	if staffMax.Valid {
		ops["staff_required_max"] = staffMax.Int32
	}
	if training.Valid {
		ops["training_provided"] = training.Bool
	}

	return ops, nil
}

func (h *Handler) getStats(ctx context.Context, franchiseID string) (map[string]interface{}, error) {
	query := `
		SELECT rating, rating_count, follow_count, view_count, enquiry_count
		FROM franchise_stats
		WHERE franchise_id = $1
	`

	var rating sql.NullFloat64
	var ratingCount, followCount, viewCount, enquiryCount sql.NullInt32
	err := h.db.QueryRowContext(ctx, query, franchiseID).Scan(&rating, &ratingCount, &followCount, &viewCount, &enquiryCount)
	if err != nil {
		return nil, err
	}

	stats := make(map[string]interface{})
	if rating.Valid {
		stats["rating"] = rating.Float64
	}
	if ratingCount.Valid {
		stats["rating_count"] = ratingCount.Int32
	}
	if followCount.Valid {
		stats["follow_count"] = followCount.Int32
	}
	if viewCount.Valid {
		stats["view_count"] = viewCount.Int32
	}
	if enquiryCount.Valid {
		stats["enquiry_count"] = enquiryCount.Int32
	}

	return stats, nil
}

func (h *Handler) getBusinessOverview(ctx context.Context, franchiseID string) (map[string]interface{}, error) {
	query := `SELECT products, services FROM franchise_business_overview WHERE franchise_id = $1`

	var productsJSON, servicesJSON []byte
	err := h.db.QueryRowContext(ctx, query, franchiseID).Scan(&productsJSON, &servicesJSON)
	if err != nil {
		return nil, err
	}

	overview := make(map[string]interface{})

	var products, services []string
	if len(productsJSON) > 0 {
		json.Unmarshal(productsJSON, &products)
		overview["products"] = products
	}
	if len(servicesJSON) > 0 {
		json.Unmarshal(servicesJSON, &services)
		overview["services"] = services
	}

	return overview, nil
}

func (h *Handler) indexToES(ctx context.Context, franchiseID string, doc map[string]interface{}) error {
	return h.esClient.Index(ctx, ESIndex, franchiseID, doc)
}

func (h *Handler) completeJob(ctx context.Context, client worker.JobClient, job entities.Job, output *Output) {
	variables := map[string]interface{}{
		"franchise_id": output.FranchiseID,
		"indexed":      output.Indexed,
		"index_name":   output.IndexName,
		"success":      output.Success,
	}

	request, err := client.NewCompleteJobCommand().JobKey(job.GetKey()).VariablesFromMap(variables)
	if err != nil {
		h.logger.Error("Failed to create complete job command", map[string]interface{}{"error": err.Error()})
		return
	}

	_, err = request.Send(ctx)
	if err != nil {
		h.logger.Error("Failed to complete job", map[string]interface{}{"error": err.Error()})
	}
}
