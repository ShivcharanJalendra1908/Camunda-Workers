package buildresponse

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"camunda-workers/internal/common/logger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{
		AppVersion: "1.0.0",
		Timeout:    30 * time.Second,
	}
}

func createTestHandler(t *testing.T, config *Config) *Handler {
	if config == nil {
		config = createTestConfig()
	}
	return NewHandler(config, logger.NewTestLogger(t))
}

func createTestInput(pageType string, data map[string]interface{}) *Input {
	return &Input{
		PageType: pageType,
		Data:     data,
	}
}

// ==========================
// Core Page Builder Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	tests := []struct {
		name           string
		input          *Input
		validateOutput func(t *testing.T, output *Output)
	}{
		{
			name: "home page response",
			input: createTestInput("home", map[string]interface{}{
				"heroBrands": []interface{}{"brand1", "brand2", "brand3"},
				"industries": []interface{}{
					map[string]interface{}{"id": "food", "name": "Food"},
					map[string]interface{}{"id": "retail", "name": "Retail"},
				},
				"popularListings": []interface{}{
					map[string]interface{}{
						"name":       "McDonald's",
						"investment": 500000,
					},
				},
				"categories": []interface{}{
					map[string]interface{}{"id": "fast-food", "name": "Fast Food"},
				},
			}),
			validateOutput: func(t *testing.T, output *Output) {
				assert.True(t, output.Success)
				assert.NotNil(t, output.Response)

				// Check metadata
				metadata, ok := output.Response["metadata"].(map[string]interface{})
				assert.True(t, ok, "metadata should be a map")
				assert.Equal(t, "home", metadata["pageType"])
				assert.Equal(t, "workflow", metadata["source"])
				assert.Equal(t, "1.0.0", metadata["version"])
				assert.NotEmpty(t, metadata["generatedAt"])

				// Check data structure
				data, ok := output.Response["data"].(map[string]interface{})
				assert.True(t, ok, "data should be a map")
				assert.Equal(t, "franchise_home", data["pageId"])

				sections, ok := data["sections"].([]interface{})
				assert.True(t, ok, "sections should be a slice")
				assert.Len(t, sections, 4)

				// Check hero section
				heroSection, ok := sections[0].(map[string]interface{})
				assert.True(t, ok, "hero section should be a map")
				assert.Equal(t, "hero", heroSection["type"])
			},
		},
		{
			name: "association home page response",
			input: &Input{
				PageType:   "home",
				EntityType: "association",
				Data: map[string]interface{}{
					"industries": []interface{}{
						map[string]interface{}{"id": "tech", "name": "Technology", "slug": "technology", "icon_url": "tech.svg"},
					},
					"popularListings": []interface{}{
						map[string]interface{}{
							"id":          "assoc-1",
							"brand":       "KASSIA",
							"description": "Kassia Description",
							"association_metadata": map[string]interface{}{
								"association_type": "Industry Body",
								"overview": map[string]interface{}{
									"key_functions": []interface{}{"Policy Support", "ISO 9001"},
								},
							},
							"founded_year":        1949.0,
							"member_count":        12000.0,
							"membership_fee_min": 10000.0,
							"membership_fee_max": 25000.0,
							"city":                "Bengaluru",
							"logo_url_square":     "kassia.svg",
						},
					},
				},
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.True(t, output.Success)
				assert.NotNil(t, output.Response)

				metadata, ok := output.Response["metadata"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "home", metadata["pageType"])
				assert.Equal(t, "1.0.0", metadata["version"])

				data, ok := output.Response["data"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "association_home", data["pageId"])

				sections, ok := data["sections"].([]interface{})
				assert.True(t, ok)
				assert.Len(t, sections, 2)

				// Check featured associations mapping
				assocSection, ok := sections[1].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "featured_business_associations", assocSection["type"])

				assocList, ok := assocSection["data"].([]interface{})
				assert.True(t, ok)
				assert.Len(t, assocList, 1)

				assoc, ok := assocList[0].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "KASSIA", assoc["association_name"])
				assert.Equal(t, "Industry Body", assoc["association_type"])
				assert.Equal(t, "Bengaluru,India", assoc["location"])
				assert.Equal(t, "1949", assoc["year_of_establishment"])
				
				feeRange, ok := assoc["MembershipFeeRange"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, 10000.0, feeRange["minFee"])
				assert.Equal(t, 25000.0, feeRange["maxFee"])

				logo, ok := assoc["logo"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "kassia.svg", logo["url"])
			},
		},
		{
			name: "listing page response",
			input: createTestInput("listing", map[string]interface{}{
				"industryInfo": map[string]interface{}{
					"description": "Food franchises",
				},
				"franchises": []interface{}{
					map[string]interface{}{
						"name":       "McDonald's",
						"investment": 500000,
						"space": map[string]interface{}{
							"min": 1000,
							"max": 2000,
						},
					},
				},
				"page":  1.0,
				"limit": 20.0,
				"total": 50.0,
			}),
			validateOutput: func(t *testing.T, output *Output) {
				assert.True(t, output.Success)

				// buildListingResponse does NOT set pageId, page, limit, total on data.
				// It only returns: success, data.sections, metadata.
				data, ok := output.Response["data"].(map[string]interface{})
				assert.True(t, ok, "data should be a map")

				sections, ok := data["sections"].([]interface{})
				assert.True(t, ok, "sections should be a slice")
				// hero section always added + franchise_listing section = 2
				assert.Len(t, sections, 2)

				// Check hero section
				heroSection, ok := sections[0].(map[string]interface{})
				assert.True(t, ok, "hero section should be a map")
				assert.Equal(t, "hero", heroSection["type"])

				// Check listing section
				listingSection, ok := sections[1].(map[string]interface{})
				assert.True(t, ok, "listing section should be a map")
				assert.Equal(t, "franchise_listing", listingSection["type"])

				// Check spaceUnit injection
				franchises, ok := listingSection["data"].([]interface{})
				assert.True(t, ok, "franchises should be a slice")
				franchise, ok := franchises[0].(map[string]interface{})
				assert.True(t, ok, "franchise should be a map")
				space, ok := franchise["space"].(map[string]interface{})
				assert.True(t, ok, "space should be a map")
				assert.Equal(t, "sq ft", space["spaceUnit"])
			},
		},
		{
			name: "detail page response",
			input: createTestInput("detail", map[string]interface{}{
				"slug":      "mcdonalds-franchise",
				"basicInfo": map[string]interface{}{"name": "McDonald's"},
				"investment": map[string]interface{}{
					"total": 500000,
					"fee":   45000,
				},
			}),
			validateOutput: func(t *testing.T, output *Output) {
				assert.True(t, output.Success)

				data, ok := output.Response["data"].(map[string]interface{})
				assert.True(t, ok, "data should be a map")
				// buildDetailResponse extracts slug from basicInfo, not from top-level data directly
				// slug comes from basicInfo.slug field, not data["slug"]
				assert.NotNil(t, data["basicInfo"])
				assert.NotNil(t, data["investment_details"])
			},
		},
		{
			name: "search page response",
			input: createTestInput("search", map[string]interface{}{
				"franchises": []interface{}{
					map[string]interface{}{
						"name":       "Subway",
						"investment": 150000,
						"space": map[string]interface{}{
							"min": 800,
							"max": 1200,
						},
					},
				},
				"total": 1.0,
				"page":  1.0,
				"limit": 10.0,
				"appliedFilters": map[string]interface{}{
					"category": "food",
				},
			}),
			validateOutput: func(t *testing.T, output *Output) {
				assert.True(t, output.Success)

				data, ok := output.Response["data"].(map[string]interface{})
				assert.True(t, ok, "data should be a map")
				assert.Equal(t, 1, data["total"])
				assert.Equal(t, 1, data["page"])
				assert.Equal(t, 10, data["limit"])

				filters, ok := data["filters_applied"].(map[string]interface{})
				assert.True(t, ok, "filters should be a map")
				assert.Equal(t, "food", filters["category"])

				franchises, ok := data["franchises"].([]interface{})
				assert.True(t, ok, "franchises should be a slice")
				franchise, ok := franchises[0].(map[string]interface{})
				assert.True(t, ok, "franchise should be a map")
				space, ok := franchise["space"].(map[string]interface{})
				assert.True(t, ok, "space should be a map")
				assert.Equal(t, "sq ft", space["spaceUnit"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := createTestHandler(t, nil)
			output, err := handler.Execute(context.Background(), tt.input)

			assert.NoError(t, err)
			assert.NotNil(t, output)
			tt.validateOutput(t, output)
		})
	}
}

func TestHandler_Execute_ErrorCases(t *testing.T) {
	tests := []struct {
		name          string
		input         *Input
		expectedError string
	}{
		{
			name: "unknown page type",
			input: createTestInput("unknown", map[string]interface{}{
				"data": "test",
			}),
			expectedError: "unknown page type: unknown",
		},
		{
			name:          "empty page type",
			input:         createTestInput("", nil),
			expectedError: "pageType is required",
		},
		{
			name:  "empty data for home page",
			input: createTestInput("home", nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := createTestHandler(t, nil)
			output, err := handler.Execute(context.Background(), tt.input)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.Nil(t, output)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, output)
				assert.True(t, output.Success)
			}
		})
	}
}

// ==========================
// Page Builder Unit Tests
// ==========================

func TestHandler_BuildHomeResponse(t *testing.T) {
	handler := createTestHandler(t, nil)

	tests := []struct {
		name     string
		data     map[string]interface{}
		validate func(t *testing.T, response map[string]interface{})
	}{
		{
			name: "complete home response",
			data: map[string]interface{}{
				"heroBrands": []interface{}{"logo1.png", "logo2.png"},
				"industries": []interface{}{
					map[string]interface{}{"id": "food", "name": "Food & Beverage"},
				},
				"popularListings": []interface{}{
					map[string]interface{}{"name": "Test Franchise"},
				},
				"categories": []interface{}{
					map[string]interface{}{"id": "category1", "name": "Category 1"},
				},
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				assert.True(t, response["success"].(bool))
				assert.NotNil(t, response["metadata"])

				data := response["data"].(map[string]interface{})
				assert.Equal(t, "franchise_home", data["pageId"])

				sections := data["sections"].([]interface{})
				assert.Len(t, sections, 4)

				heroSection := sections[0].(map[string]interface{})
				assert.Equal(t, "hero", heroSection["type"])
			},
		},
		{
			name: "empty sections safety",
			data: map[string]interface{}{},
			validate: func(t *testing.T, response map[string]interface{}) {
				data := response["data"].(map[string]interface{})
				sections := data["sections"].([]interface{})
				assert.Empty(t, sections)
			},
		},
		{
			name: "partial data",
			data: map[string]interface{}{
				"heroBrands": []interface{}{"brand1"},
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				data := response["data"].(map[string]interface{})
				sections := data["sections"].([]interface{})
				assert.Len(t, sections, 1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := handler.buildHomeResponse(tt.data)
			tt.validate(t, response)
		})
	}
}

func TestHandler_BuildListingResponse(t *testing.T) {
	handler := createTestHandler(t, nil)

	tests := []struct {
		name     string
		data     map[string]interface{}
		validate func(t *testing.T, response map[string]interface{})
	}{
		{
			name: "complete listing response",
			data: map[string]interface{}{
				"industryInfo": map[string]interface{}{
					"description": "Best food franchises",
				},
				"franchises": []interface{}{
					map[string]interface{}{
						"name":       "Franchise 1",
						"investment": 100000,
						"space":      map[string]interface{}{"min": 500},
					},
				},
				"categories": []interface{}{"cat1"},
				"recommended": []interface{}{
					map[string]interface{}{"name": "Recommended 1"},
				},
				"page":  2.0,
				"limit": 20.0,
				"total": 100.0,
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				// buildListingResponse does NOT set pageId, page, limit, total on data.
				// It returns: success, data.sections, metadata only.
				assert.True(t, response["success"].(bool))

				data := response["data"].(map[string]interface{})

				// Sections: hero + franchise_listing + featured_categories + recommended_franchises = 4
				sections := data["sections"].([]interface{})
				assert.Len(t, sections, 4)

				// Check hero section (always index 0)
				heroSection := sections[0].(map[string]interface{})
				assert.Equal(t, "hero", heroSection["type"])

				// Check franchise_listing section (index 1)
				listingSection := sections[1].(map[string]interface{})
				assert.Equal(t, "franchise_listing", listingSection["type"])

				// Check spaceUnit injection
				franchises := listingSection["data"].([]interface{})
				franchise := franchises[0].(map[string]interface{})
				space := franchise["space"].(map[string]interface{})
				assert.Equal(t, "sq ft", space["spaceUnit"])
			},
		},
		{
			name: "without space field",
			data: map[string]interface{}{
				"franchises": []interface{}{
					map[string]interface{}{"name": "Franchise 1"},
				},
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				data := response["data"].(map[string]interface{})
				sections := data["sections"].([]interface{})
				// hero + franchise_listing = 2
				assert.Len(t, sections, 2)
			},
		},
		{
			name: "complete listing response with questions and insights",
			data: map[string]interface{}{
				"industryInfo": map[string]interface{}{
					"description": "Best food franchises",
				},
				"franchises": []interface{}{
					map[string]interface{}{
						"name":       "Franchise 1",
						"investment": 100000,
						"space":      map[string]interface{}{"min": 500},
					},
				},
				"categories": []interface{}{"cat1"},
				"recommended": []interface{}{
					map[string]interface{}{"name": "Recommended 1"},
				},
				"categoryQuestions": []interface{}{
					map[string]interface{}{"question": "What is the fee?", "answer": "It is 45000."},
				},
				"marketInsights": []interface{}{
					map[string]interface{}{
						"market_size": "5 Billion",
						"growth_rate": "15%",
					},
				},
				"page":  2.0,
				"limit": 20.0,
				"total": 100.0,
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				assert.True(t, response["success"].(bool))
				data := response["data"].(map[string]interface{})

				sections := data["sections"].([]interface{})
				// Sections:
				// 0: hero
				// 1: franchise_listing
				// 2: featured_categories
				// 3: category_questions
				// 4: recommended_franchises
				// 5: key_market_insights
				assert.Len(t, sections, 6)

				assert.Equal(t, "hero", sections[0].(map[string]interface{})["type"])
				assert.Equal(t, "franchise_listing", sections[1].(map[string]interface{})["type"])
				assert.Equal(t, "featured_categories", sections[2].(map[string]interface{})["type"])
				assert.Equal(t, "category_questions", sections[3].(map[string]interface{})["type"])
				assert.Equal(t, "recommended_franchises", sections[4].(map[string]interface{})["type"])
				assert.Equal(t, "key_market_insights", sections[5].(map[string]interface{})["type"])

				// Validate questions data
				qSection := sections[3].(map[string]interface{})
				qData := qSection["data"].(map[string]interface{})
				questions := qData["questions"].([]interface{})
				assert.Len(t, questions, 1)
				assert.Equal(t, "What is the fee?", questions[0].(map[string]interface{})["question"])

				// Validate market insights data
				insightsSection := sections[5].(map[string]interface{})
				insightsData := insightsSection["data"].(map[string]interface{})
				assert.Equal(t, "5 Billion", insightsData["market_size"])
			},
		},
		{
			name: "association listing page response",
			data: map[string]interface{}{
				"entityType": "association",
				"franchises": []interface{}{
					map[string]interface{}{
						"name": "KASSIA",
						"slug": "kassia",
						"founded_year": 1949.0,
						"association_metadata": map[string]interface{}{
							"association_type": "Industry Body",
							"overview": map[string]interface{}{
								"key_functions": []interface{}{"Policy Support", "ISO 9001"},
							},
						},
					},
				},
				"categories": []interface{}{
					map[string]interface{}{"id": "tech", "name": "Technology", "slug": "technology"},
				},
				"categoryQuestions": []interface{}{
					map[string]interface{}{
						"question": "What are primary functions?",
						"answer":   "Primary functions are...",
					},
				},
				"recommended": []interface{}{
					map[string]interface{}{"id": "1", "name": "NASSCOM", "slug": "nasscom"},
				},
				"marketInsights": []interface{}{
					map[string]interface{}{
						"market_stats": "MSME Stats...",
					},
				},
				"page": 1.0,
				"pageSize": 10.0,
				"totalCount": 50.0,
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				assert.True(t, response["success"].(bool))
				data, ok := response["data"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "association_listing", data["pageId"])

				sections, ok := data["sections"].([]interface{})
				assert.True(t, ok)
				assert.Len(t, sections, 9) // 9 sections total

				// Verify section order
				assert.Equal(t, "hero", sections[0].(map[string]interface{})["type"])
				assert.Equal(t, "business_associations", sections[1].(map[string]interface{})["type"])
				assert.Equal(t, "functions_of_business_associations", sections[2].(map[string]interface{})["type"])
				assert.Equal(t, "statistics", sections[3].(map[string]interface{})["type"])
				assert.Equal(t, "business_associations_across_india", sections[4].(map[string]interface{})["type"])
				assert.Equal(t, "featured_business_categories", sections[5].(map[string]interface{})["type"])
				assert.Equal(t, "category_questions", sections[6].(map[string]interface{})["type"])
				assert.Equal(t, "recommended_business_associations", sections[7].(map[string]interface{})["type"])
				assert.Equal(t, "key_market_insights", sections[8].(map[string]interface{})["type"])

				// Check pagination
				pagination, ok := data["pagination"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, 1, pagination["currentPage"])
				assert.Equal(t, 10, pagination["pageSize"])
				assert.Equal(t, int64(50), pagination["totalItems"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := handler.buildListingResponse(tt.data)
			tt.validate(t, response)
		})
	}
}

func TestHandler_BuildDetailResponse(t *testing.T) {
	handler := createTestHandler(t, nil)

	tests := []struct {
		name     string
		data     map[string]interface{}
		validate func(t *testing.T, response map[string]interface{})
	}{
		{
			name: "complete detail response",
			data: map[string]interface{}{
				// slug must be inside basicInfo — handler extracts it from basicInfo["slug"]
				"basicInfo": map[string]interface{}{"name": "Test", "slug": "test-franchise"},
				"overview":  map[string]interface{}{"desc": "Description"},
				"investment": map[string]interface{}{
					"total": 500000,
					"fee":   45000,
				},
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				data := response["data"].(map[string]interface{})
				assert.Equal(t, "test-franchise", data["slug"])
				assert.NotNil(t, data["basicInfo"])
				assert.NotNil(t, data["franchising_overview"])
				assert.NotNil(t, data["investment_details"])
			},
		},
		{
			name: "without slug in basicInfo",
			data: map[string]interface{}{
				"basicInfo": map[string]interface{}{"name": "Test"},
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				data := response["data"].(map[string]interface{})
				assert.NotNil(t, data["basicInfo"])
				// slug not set when basicInfo has no slug field
				assert.Nil(t, data["slug"])
			},
		},
		{
			name: "master franchise detail response",
			data: map[string]interface{}{
				"entityType": "master_franchise",
				"basicInfo":  map[string]interface{}{"name": "Brew & Blend", "slug": "brew-blend-master"},
				"operations": map[string]interface{}{
					"territory_details": map[string]interface{}{
						"scope":                 "State-wide exclusivity",
						"exclusivity_terms":     "Exclusive regional rights to open up to 10 sub-units",
						"available_territories": []interface{}{"North India"},
						"taken_territories":     []interface{}{"South India"},
					},
					"development_schedule": map[string]interface{}{
						"obligations":     "Must open minimum 5 units within first 3 years",
						"timeline_months": 36,
						"target_units":    5,
					},
					"support_training": map[string]interface{}{
						"brand_toolkits":         "Full advertising and marketing assets package",
						"operational_manuals":    "Operations manuals",
						"training_duration_days": 14,
					},
					"legal_compliance": map[string]interface{}{
						"agreement_term_years": 10,
						"renewal_term_years":   5,
						"regulatory_licences":  []interface{}{"FSSAI"},
					},
				},
				"investment": map[string]interface{}{
					"revenue_model": map[string]interface{}{
						"payback_period":      "18-24 months",
						"performance_bonuses": "10% bonus",
						"roi_calculator_inputs": map[string]interface{}{
							"avg_unit_revenue": 500000,
						},
					},
				},
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				assert.True(t, response["success"].(bool))
				data, ok := response["data"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "brew-blend-master", data["slug"])

				// Check master franchise details mapped
				tr, ok := data["territory_rights"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "State-wide exclusivity", tr["scope"])
				assert.Equal(t, "Exclusive regional rights to open up to 10 sub-units", tr["exclusivity_terms"])

				ds, ok := data["development_schedule"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "Must open minimum 5 units within first 3 years", ds["obligations"])

				st, ok := data["support_and_training"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "Full advertising and marketing assets package", st["brand_toolkits"])

				lc, ok := data["legal_compliance"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, 10, lc["agreement_term_years"])

				rm, ok := data["revenue_model"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "18-24 months", rm["payback_period"])

				roles, ok := data["three_player_roles"].(map[string]interface{})
				assert.True(t, ok)
				assert.NotNil(t, roles["franchisor"])
				assert.NotNil(t, roles["master_franchisee"])
				assert.NotNil(t, roles["unit_franchisees"])
			},
		},
		{
			name: "association detail page response",
			data: map[string]interface{}{
				"entityType": "association",
				"basicInfo": map[string]interface{}{
					"name": "KASSIA",
					"slug": "kassia",
					"description": "Kassia Description",
					"association_metadata": map[string]interface{}{
						"association_type": "Industry Body",
						"overview": map[string]interface{}{
							"key_functions": []interface{}{"Policy Support", "ISO 9001"},
						},
						"faqs": []interface{}{
							map[string]interface{}{
								"question": "What is the membership process?",
								"answer":   "Membership process details...",
							},
						},
					},
				},
				"recommended": []interface{}{
					map[string]interface{}{"id": "1", "name": "NASSCOM", "slug": "nasscom"},
				},
				"categories": []interface{}{
					map[string]interface{}{"id": "tech", "name": "Technology", "slug": "technology"},
				},
				"marketInsights": []interface{}{
					map[string]interface{}{
						"market_stats": "MSME Stats...",
					},
				},
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				assert.True(t, response["success"].(bool))
				data, ok := response["data"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "association_individual", data["pageId"])
				assert.Equal(t, "kassia", data["slug"])
				assert.Equal(t, "KASSIA", data["association_name"])

				sections, ok := data["sections"].([]interface{})
				assert.True(t, ok)
				assert.Len(t, sections, 18) // 18 sections total

				// Verify first section
				heroSection, ok := sections[0].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "association_hero_info_card", heroSection["type"])

				// Verify recommended business associations
				recSection, ok := sections[14].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "recommended_business_associations", recSection["type"])

				// Verify market insights section
				insightsSection, ok := sections[15].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "market_insights_section", insightsSection["type"])

				// Verify category questions section (18th section)
				faqSection, ok := sections[17].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, "category_questions", faqSection["type"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := handler.buildDetailResponse(tt.data)
			tt.validate(t, response)
		})
	}
}

func TestHandler_BuildSearchResponse(t *testing.T) {
	handler := createTestHandler(t, nil)

	tests := []struct {
		name     string
		data     map[string]interface{}
		validate func(t *testing.T, response map[string]interface{})
	}{
		{
			name: "complete search response",
			data: map[string]interface{}{
				"franchises": []interface{}{
					map[string]interface{}{
						"name":  "Franchise 1",
						"space": map[string]interface{}{"min": 500},
					},
				},
				"total": 25.0,
				"page":  1.0,
				"limit": 10.0,
				"appliedFilters": map[string]interface{}{
					"category": "food",
					"price":    "100000-500000",
				},
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				data := response["data"].(map[string]interface{})
				assert.Equal(t, 25, data["total"])
				assert.Equal(t, 1, data["page"])
				assert.Equal(t, 10, data["limit"])

				filters := data["filters_applied"].(map[string]interface{})
				assert.Equal(t, "food", filters["category"])

				franchises := data["franchises"].([]interface{})
				franchise := franchises[0].(map[string]interface{})
				space := franchise["space"].(map[string]interface{})
				assert.Equal(t, "sq ft", space["spaceUnit"])
			},
		},
		{
			name: "without filters",
			data: map[string]interface{}{
				"total":      10.0,
				"franchises": []interface{}{},
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				data := response["data"].(map[string]interface{})
				assert.Equal(t, 10, data["total"])
				// franchises is empty slice so handler won't set it on response["data"]
				assert.Nil(t, data["filters_applied"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := handler.buildSearchResponse(tt.data)
			tt.validate(t, response)
		})
	}
}

// ==========================
// Validation Tests
// ==========================

func TestHandler_ValidateInput(t *testing.T) {
	handler := createTestHandler(t, nil)

	tests := []struct {
		name          string
		input         *Input
		expectedError string
	}{
		{
			name: "valid home page input",
			input: &Input{
				PageType: "home",
				Data:     map[string]interface{}{"test": "data"},
			},
			expectedError: "",
		},
		{
			name: "valid listing page input",
			input: &Input{
				PageType: "listing",
				Data:     map[string]interface{}{},
			},
			expectedError: "",
		},
		{
			name: "empty page type",
			input: &Input{
				PageType: "",
			},
			expectedError: "Validation failed for field 'pageType'",
		},
		{
			name: "invalid page type",
			input: &Input{
				PageType: "invalid",
			},
			expectedError: "Validation failed for field 'pageType'",
		},
		{
			name: "data too deep nesting",
			input: &Input{
				PageType: "home",
				Data: map[string]interface{}{
					"level1": map[string]interface{}{
						"level2": map[string]interface{}{
							"level3": map[string]interface{}{
								"level4": map[string]interface{}{
									"level5": map[string]interface{}{
										"level6": map[string]interface{}{
											"level7": map[string]interface{}{
												"level8": map[string]interface{}{
													"level9": map[string]interface{}{
														"level10": map[string]interface{}{
															"level11": map[string]interface{}{
																"level12": "too deep",
															},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedError: "Validation failed for field 'data'",
		},
		{
			name: "data too large",
			input: &Input{
				PageType: "home",
				Data: func() map[string]interface{} {
					data := make(map[string]interface{})
					largeString := ""
					for i := 0; i < 200000; i++ {
						largeString += "a"
					}
					data["largeField"] = largeString
					return data
				}(),
			},
			expectedError: "Validation failed for field 'data'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateInput(tt.input)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHandler_ValidateDataDepth(t *testing.T) {
	handler := createTestHandler(t, nil)

	tests := []struct {
		name          string
		data          map[string]interface{}
		depth         int
		expectedError string
	}{
		{
			name: "shallow data",
			data: map[string]interface{}{
				"field1": "value1",
				"field2": 123,
			},
			depth:         0,
			expectedError: "",
		},
		{
			name: "nested within limit",
			data: map[string]interface{}{
				"level1": map[string]interface{}{
					"level2": map[string]interface{}{
						"level3": "value",
					},
				},
			},
			depth:         0,
			expectedError: "",
		},
		{
			name: "too deep",
			data: map[string]interface{}{
				"level1": map[string]interface{}{
					"level2": map[string]interface{}{
						"level3": map[string]interface{}{
							"level4": map[string]interface{}{
								"level5": map[string]interface{}{
									"level6": map[string]interface{}{
										"level7": map[string]interface{}{
											"level8": map[string]interface{}{
												"level9": map[string]interface{}{
													"level10": map[string]interface{}{
														"level11": map[string]interface{}{
															"level12": "too deep",
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			depth:         0,
			expectedError: "Validation failed for field 'data'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateDataDepth(tt.data, tt.depth)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHandler_ValidateDataSize(t *testing.T) {
	handler := createTestHandler(t, nil)

	tests := []struct {
		name          string
		data          map[string]interface{}
		expectedError string
	}{
		{
			name: "small data",
			data: map[string]interface{}{
				"field1": "value1",
				"field2": 123,
			},
			expectedError: "",
		},
		{
			name: "large data",
			data: func() map[string]interface{} {
				data := make(map[string]interface{})
				for i := 0; i < 10000; i++ {
					data[fmt.Sprintf("field%d", i)] = fmt.Sprintf("value%d", i)
				}
				return data
			}(),
			expectedError: "Validation failed for field 'data'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateDataSize(tt.data)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EmptyDataHandling(t *testing.T) {
	handler := createTestHandler(t, nil)

	t.Run("nil data for all page types", func(t *testing.T) {
		pageTypes := []string{"home", "listing", "detail", "search"}

		for _, pageType := range pageTypes {
			t.Run(pageType, func(t *testing.T) {
				input := &Input{PageType: pageType, Data: nil}
				err := handler.validateInput(input)
				assert.NoError(t, err)

				output, err := handler.Execute(context.Background(), input)
				assert.NoError(t, err)
				assert.NotNil(t, output)
				assert.True(t, output.Success)
			})
		}
	})

	t.Run("empty data map", func(t *testing.T) {
		input := &Input{PageType: "home", Data: map[string]interface{}{}}
		output, err := handler.Execute(context.Background(), input)
		assert.NoError(t, err)
		assert.NotNil(t, output)
		assert.True(t, output.Success)
	})
}

func TestHandler_TypeConversions(t *testing.T) {
	handler := createTestHandler(t, nil)

	t.Run("float64 to int conversion for search page", func(t *testing.T) {
		// Only buildSearchResponse converts page/limit/total to int.
		// buildListingResponse does NOT put these on the data map.
		data := map[string]interface{}{
			"page":  1.0,
			"limit": 20.0,
			"total": 100.0,
		}

		input := &Input{PageType: "search", Data: data}
		output, err := handler.Execute(context.Background(), input)

		assert.NoError(t, err)
		assert.NotNil(t, output)

		responseData := output.Response["data"].(map[string]interface{})
		assert.IsType(t, 1, responseData["page"])
		assert.IsType(t, 1, responseData["limit"])
		assert.IsType(t, 1, responseData["total"])
	})
}

func TestHandler_MetadataInclusion(t *testing.T) {
	handler := createTestHandler(t, nil)

	config := &Config{
		AppVersion: "2.0.0-test",
	}
	handlerWithVersion := NewHandler(config, logger.NewTestLogger(t))

	tests := []struct {
		name     string
		handler  *Handler
		pageType string
	}{
		{"home page", handler, "home"},
		{"listing page", handlerWithVersion, "listing"},
		{"detail page", handler, "detail"},
		{"search page", handlerWithVersion, "search"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &Input{PageType: tt.pageType}
			output, err := tt.handler.Execute(context.Background(), input)

			assert.NoError(t, err)
			assert.NotNil(t, output.Response["metadata"])

			metadata := output.Response["metadata"].(map[string]interface{})
			assert.Equal(t, tt.pageType, metadata["pageType"])
			assert.Equal(t, "workflow", metadata["source"])
			assert.NotEmpty(t, metadata["generatedAt"])

			if tt.handler.config.AppVersion != "" {
				assert.Equal(t, tt.handler.config.AppVersion, metadata["version"])
			}
		})
	}
}

// ==========================
// JSON Serialization Tests
// ==========================

func TestHandler_JSONSerialization(t *testing.T) {
	handler := createTestHandler(t, nil)

	input := &Input{
		PageType: "home",
		Data: map[string]interface{}{
			"heroBrands": []interface{}{"brand1", "brand2"},
		},
	}

	output, err := handler.Execute(context.Background(), input)
	require.NoError(t, err)

	// Marshal to JSON
	jsonData, err := json.Marshal(output)
	assert.NoError(t, err)

	// Unmarshal back
	var decoded Output
	err = json.Unmarshal(jsonData, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, output.Success, decoded.Success)
	assert.NotNil(t, decoded.Response)

	// Check metadata survived JSON round-trip
	metadata, ok := decoded.Response["metadata"].(map[string]interface{})
	assert.True(t, ok, "metadata should be a map")
	assert.Equal(t, "home", metadata["pageType"])
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	handler := NewHandler(&Config{AppVersion: "1.0.0"}, logger.NewTestLogger(b))

	benchmarks := []struct {
		name     string
		pageType string
		data     map[string]interface{}
	}{
		{
			name:     "home page",
			pageType: "home",
			data: map[string]interface{}{
				"heroBrands": []interface{}{"b1", "b2", "b3"},
				"industries": []interface{}{map[string]interface{}{"id": "test"}},
			},
		},
		{
			name:     "listing page",
			pageType: "listing",
			data: map[string]interface{}{
				"franchises": []interface{}{
					map[string]interface{}{
						"name":  "Test",
						"space": map[string]interface{}{"min": 100},
					},
				},
				"page":  1.0,
				"limit": 20.0,
			},
		},
		{
			name:     "detail page",
			pageType: "detail",
			data: map[string]interface{}{
				"basicInfo": map[string]interface{}{"name": "Test", "slug": "test"},
			},
		},
		{
			name:     "search page",
			pageType: "search",
			data: map[string]interface{}{
				"franchises": []interface{}{map[string]interface{}{"name": "Test"}},
				"total":      1.0,
			},
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			input := &Input{PageType: bm.pageType, Data: bm.data}
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_, _ = handler.Execute(context.Background(), input)
			}
		})
	}
}
