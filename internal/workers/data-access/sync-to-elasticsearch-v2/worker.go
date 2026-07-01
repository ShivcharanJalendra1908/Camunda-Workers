package synctoelasticsearchv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/crypto"
	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
	"github.com/elastic/go-elasticsearch/v8"
)

const TaskType = "sync-to-elasticsearch-v2"

type Config struct {
	Timeout       time.Duration `mapstructure:"timeout"`
	EncryptionKey string        `mapstructure:"encryption_key"`
}

type Handler struct {
	config *Config
	db     *sql.DB
	es     *elasticsearch.Client
	logger logger.Logger
	encryptor *crypto.Encryptor
}

func NewHandler(cfg *Config, db *sql.DB, es *elasticsearch.Client, log logger.Logger) *Handler {
	var encryptor *crypto.Encryptor
	if cfg.EncryptionKey != "" {
		enc, err := crypto.NewEncryptor(cfg.EncryptionKey)
		if err == nil {
			encryptor = enc
		} else {
			log.Warn("Failed to initialize encryptor", map[string]interface{}{"error": err.Error()})
		}
	}

	return &Handler{
		config:    cfg,
		db:        db,
		es:        es,
		logger:    log.WithFields(map[string]interface{}{"worker": TaskType}),
		encryptor: encryptor,
	}
}

func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	jobKey := job.GetKey()
	
	h.logger.Info("Starting sync to elasticsearch v2", map[string]interface{}{"jobKey": jobKey})

	variables, err := job.GetVariablesAsMap()
	if err != nil {
		h.logger.Error("Failed to get variables", map[string]interface{}{"error": err.Error()})
		client.NewFailJobCommand().JobKey(jobKey).Retries(0).ErrorMessage(err.Error()).Send(context.Background())
		return
	}

	franchiseId, ok := variables["franchiseId"].(string)
	if !ok || franchiseId == "" {
		h.logger.Error("Missing franchiseId", map[string]interface{}{"jobKey": jobKey})
		client.NewCompleteJobCommand().JobKey(jobKey).Send(context.Background())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Fetch full entity from DB (V2 CTI schema)
	query := `
		SELECT 
			l.id, l.name, l.slug, l.short_description, l.description, 
			l.contact_email, l.entity_type, l.status, l.trusted_seller, l.verified, 
			COALESCE(fr.total_outlets, 0), fr.outlet_range, fr.industry, fr.business_type, 
			fr.established_year, COALESCE(fr.units_count, 0), l.logo_url_circle, l.logo_url_square, a.association_metadata,
			a.member_count, a.membership_fee_min, a.membership_fee_max, l.approved_at,
			l.website_url, l.is_featured, l.featured_start_at, l.featured_expires_at, l.featured_order, l.is_sponsored,
			COALESCE(lc.country, 'India') as country,
			fo.territory_details,
			fo.space_min_sqft, fo.space_max_sqft,
			fi.initial_investment_min, fi.initial_investment_max
		FROM listings l
		LEFT JOIN franchises fr ON l.id = fr.id
		LEFT JOIN associations a ON l.id = a.id
		LEFT JOIN (
			SELECT DISTINCT ON (listing_id) listing_id, country 
			FROM listing_cities
		) lc ON l.id = lc.listing_id
		LEFT JOIN franchise_operations fo ON l.id = fo.franchise_id
		LEFT JOIN franchise_investment_requirement fi ON l.id = fi.franchise_id
		WHERE l.id = $1
		LIMIT 1
	`
	
	var f struct {
		ID                   string          `json:"id"`
		Name                 string          `json:"name"`
		Slug                 string          `json:"slug"`
		ShortDescription     *string         `json:"short_description,omitempty"`
		Description          *string         `json:"description,omitempty"`
		ContactEmail         *string         `json:"contact_email,omitempty"`
		EntityType           *string         `json:"entity_type,omitempty"`
		Status               *string         `json:"status,omitempty"`
		TrustedSeller        bool            `json:"trusted_seller"`
		Verified             bool            `json:"verified"`
		TotalOutlets         int             `json:"total_outlets"`
		OutletRange          *string         `json:"outlet_range,omitempty"`
		Industry             *string         `json:"industry,omitempty"`
		BusinessType         *string         `json:"business_type,omitempty"`
		EstablishedYear      *int16          `json:"established_year,omitempty"`
		UnitsCount           int             `json:"units_count"`
		LogoURLCircle        *string         `json:"logo_url_circle,omitempty"`
		LogoURLSquare        *string         `json:"logo_url_square,omitempty"`
		AssociationMetadata  json.RawMessage `json:"association_metadata,omitempty"`
		MemberCount          *int            `json:"member_count,omitempty"`
		MembershipFeeMin     *float64        `json:"membership_fee_min,omitempty"`
		MembershipFeeMax     *float64        `json:"membership_fee_max,omitempty"`
		ApprovedAt           *time.Time      `json:"approved_at,omitempty"`
		WebsiteURL           *string         `json:"website_url,omitempty"`
		IsFeatured           bool            `json:"is_featured"`
		FeaturedStartAt      *time.Time      `json:"featured_start_at,omitempty"`
		FeaturedExpiresAt    *time.Time      `json:"featured_expires_at,omitempty"`
		FeaturedOrder        int             `json:"featured_order"`
		IsSponsored          bool            `json:"is_sponsored"`
		Country              string          `json:"country"`
		TerritoryDetails     json.RawMessage `json:"territory_details,omitempty"`
		SpaceMinSqft         *int            `json:"space_min_sqft,omitempty"`
		SpaceMaxSqft         *int            `json:"space_max_sqft,omitempty"`
		InitialInvestmentMin *float64        `json:"initial_investment_min,omitempty"`
		InitialInvestmentMax *float64        `json:"initial_investment_max,omitempty"`
	}

	var websiteUrlStr sql.NullString
	var featuredStartAt, featuredExpiresAt sql.NullTime

	err = h.db.QueryRowContext(ctx, query, franchiseId).Scan(
		&f.ID, &f.Name, &f.Slug, &f.ShortDescription, &f.Description,
		&f.ContactEmail, &f.EntityType, &f.Status, &f.TrustedSeller, &f.Verified,
		&f.TotalOutlets, &f.OutletRange, &f.Industry, &f.BusinessType,
		&f.EstablishedYear, &f.UnitsCount, &f.LogoURLCircle, &f.LogoURLSquare, &f.AssociationMetadata,
		&f.MemberCount, &f.MembershipFeeMin, &f.MembershipFeeMax, &f.ApprovedAt,
		&websiteUrlStr, &f.IsFeatured, &featuredStartAt, &featuredExpiresAt, &f.FeaturedOrder, &f.IsSponsored,
		&f.Country, &f.TerritoryDetails,
		&f.SpaceMinSqft, &f.SpaceMaxSqft,
		&f.InitialInvestmentMin, &f.InitialInvestmentMax,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			h.logger.Warn("Entity not found in DB for sync", map[string]interface{}{"franchiseId": franchiseId})
			// Just remove from ES if it exists
			_, _ = h.es.Delete("franchise_listings", franchiseId, h.es.Delete.WithContext(ctx))
			client.NewCompleteJobCommand().JobKey(jobKey).Send(context.Background())
			return
		}
		h.logger.Error("DB error", map[string]interface{}{"error": err.Error()})
		client.NewFailJobCommand().JobKey(jobKey).Retries(3).ErrorMessage(err.Error()).Send(context.Background())
		return
	}

	if websiteUrlStr.Valid {
		f.WebsiteURL = &websiteUrlStr.String
	}
	if featuredStartAt.Valid {
		f.FeaturedStartAt = &featuredStartAt.Time
	}
	if featuredExpiresAt.Valid {
		f.FeaturedExpiresAt = &featuredExpiresAt.Time
	}

	statusStr := ""
	if f.Status != nil {
		statusStr = *f.Status
	}

	// 2. If status is NOT live, remove from ES (or do not index)
	if statusStr != "live" {
		h.logger.Info("Entity is not live, ensuring it is removed from ES", map[string]interface{}{"franchiseId": franchiseId})
		_, err = h.es.Delete("franchise_listings", franchiseId, h.es.Delete.WithContext(ctx))
		if err != nil {
			h.logger.Warn("Failed to delete from ES, may not exist", map[string]interface{}{"error": err.Error()})
		}
		client.NewCompleteJobCommand().JobKey(jobKey).Send(context.Background())
		return
	}

	// 3. Entity is live, index into ES
	
	// Parse association_metadata if present
	var assocMeta map[string]interface{}
	if len(f.AssociationMetadata) > 0 {
		_ = json.Unmarshal(f.AssociationMetadata, &assocMeta)
	}

	// Parse territory_details
	var territoryDetails map[string]interface{}
	var exclusivityType, territoryScope string
	if len(f.TerritoryDetails) > 0 {
		if err := json.Unmarshal(f.TerritoryDetails, &territoryDetails); err == nil {
			if exclusivity, ok := territoryDetails["exclusivity_type"].(string); ok {
				exclusivityType = exclusivity
			} else if exclusivity, ok := territoryDetails["scope"].(string); ok {
				exclusivityType = exclusivity
			}
			if scope, ok := territoryDetails["territory_scope"].(string); ok {
				territoryScope = scope
			} else if scope, ok := territoryDetails["scope"].(string); ok {
				territoryScope = scope
			}
		}
	}

	// Space object
	var spaceDoc map[string]interface{}
	if f.SpaceMinSqft != nil || f.SpaceMaxSqft != nil {
		var minSpaceVal, maxSpaceVal float64
		if f.SpaceMinSqft != nil {
			minSpaceVal = float64(*f.SpaceMinSqft)
		}
		if f.SpaceMaxSqft != nil {
			maxSpaceVal = float64(*f.SpaceMaxSqft)
		}
		spaceDoc = map[string]interface{}{
			"minSpace":  minSpaceVal,
			"maxSpace":  maxSpaceVal,
			"min_space":  minSpaceVal,
			"max_space":  maxSpaceVal,
			"spaceUnit": "sq ft",
		}
	}

	// Investment objects
	var investmentDoc map[string]interface{}
	var investmentRangeDoc map[string]interface{}
	if f.InitialInvestmentMin != nil || f.InitialInvestmentMax != nil {
		var minLakhs, maxLakhs float64
		if f.InitialInvestmentMin != nil {
			minLakhs = *f.InitialInvestmentMin / 100000
		}
		if f.InitialInvestmentMax != nil {
			maxLakhs = *f.InitialInvestmentMax / 100000
		}
		investmentRangeDoc = map[string]interface{}{
			"minInvestment":  minLakhs,
			"maxInvestment":  maxLakhs,
			"investmentUnit": "Lakhs",
		}
		investmentDoc = map[string]interface{}{
			"min_investment": minLakhs,
			"max_investment": maxLakhs,
			"minInvestment":  minLakhs,
			"maxInvestment":  maxLakhs,
		}
	}

	getStr := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}

	doc := map[string]interface{}{
		"franchise_id":         f.ID,
		"name":                 f.Name,
		"slug":                 f.Slug,
		"entity_type":          f.EntityType,
		"status":               f.Status,
		"short_description":    f.ShortDescription,
		"description":          f.Description,
		"trusted_seller":       f.TrustedSeller,
		"verified":             f.Verified,
		"total_outlets":        f.TotalOutlets,
		"industry":             f.Industry,
		"business_type":        f.BusinessType,
		"established_year":     f.EstablishedYear,
		"logo": map[string]interface{}{
			"circle": getStr(f.LogoURLCircle),
			"square": getStr(f.LogoURLSquare),
			"alt":    f.Name,
		},
		"association_metadata": assocMeta,
		"member_count":         f.MemberCount,
		"membership_fee_min":   f.MembershipFeeMin,
		"membership_fee_max":   f.MembershipFeeMax,
		"approved_at":          f.ApprovedAt,
		"website_url":          f.WebsiteURL,
		"is_featured":          f.IsFeatured,
		"featured_start_at":    f.FeaturedStartAt,
		"featured_expires_at":  f.FeaturedExpiresAt,
		"featured_order":       f.FeaturedOrder,
		"is_sponsored":         f.IsSponsored,
		"country":              f.Country,
		"exclusivity_type":     exclusivityType,
		"territory_scope":      territoryScope,
		"territory_details":    territoryDetails,
		"updated_at":           time.Now().Format(time.RFC3339),
	}

	if spaceDoc != nil {
		doc["space"] = spaceDoc
	}
	if investmentDoc != nil {
		doc["investment"] = investmentDoc
		doc["investmentRange"] = investmentRangeDoc
	}

	docJSON, err := json.Marshal(doc)
	if err != nil {
		h.logger.Error("Failed to marshal ES doc", map[string]interface{}{"error": err.Error()})
		client.NewFailJobCommand().JobKey(jobKey).Retries(0).ErrorMessage(err.Error()).Send(context.Background())
		return
	}

	res, err := h.es.Index("franchise_listings", strings.NewReader(string(docJSON)), h.es.Index.WithDocumentID(f.ID), h.es.Index.WithContext(ctx))
	if err != nil {
		h.logger.Error("Failed to index into ES", map[string]interface{}{"error": err.Error()})
		client.NewFailJobCommand().JobKey(jobKey).Retries(3).ErrorMessage(err.Error()).Send(context.Background())
		return
	}
	defer res.Body.Close()

	if res.IsError() {
		h.logger.Error("ES index error response", map[string]interface{}{"status": res.Status()})
		client.NewFailJobCommand().JobKey(jobKey).Retries(3).ErrorMessage("ES index error").Send(context.Background())
		return
	}

	h.logger.Info("Successfully synced to ES", map[string]interface{}{"franchiseId": franchiseId})
	client.NewCompleteJobCommand().JobKey(jobKey).Send(context.Background())
}
