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

				// Check each section
				heroSection, ok := sections[0].(map[string]interface{})
				assert.True(t, ok, "hero section should be a map")
				assert.Equal(t, "hero", heroSection["type"])
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

				data, ok := output.Response["data"].(map[string]interface{})
				assert.True(t, ok, "data should be a map")
				assert.Equal(t, "franchise_listing", data["pageId"])
				assert.Equal(t, 1, data["page"])
				assert.Equal(t, 20, data["limit"])
				assert.Equal(t, 50, data["total"])

				// Check spaceUnit injection
				sections, ok := data["sections"].([]interface{})
				assert.True(t, ok, "sections should be a slice")
				assert.Len(t, sections, 2)

				listingSection, ok := sections[1].(map[string]interface{})
				assert.True(t, ok, "listing section should be a map")
				assert.Equal(t, "franchise_listing", listingSection["type"])

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
				assert.Equal(t, "mcdonalds-franchise", data["slug"])
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
				data := response["data"].(map[string]interface{})
				assert.Equal(t, "franchise_listing", data["pageId"])
				assert.Equal(t, 2, data["page"])
				assert.Equal(t, 20, data["limit"])
				assert.Equal(t, 100, data["total"])

				sections := data["sections"].([]interface{})
				assert.Len(t, sections, 4)

				// Check spaceUnit injection
				listingSection := sections[1].(map[string]interface{})
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
				assert.Len(t, sections, 1)
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
				"slug":      "test-franchise",
				"basicInfo": map[string]interface{}{"name": "Test"},
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
			name: "without slug",
			data: map[string]interface{}{
				"basicInfo": map[string]interface{}{"name": "Test"},
			},
			validate: func(t *testing.T, response map[string]interface{}) {
				data := response["data"].(map[string]interface{})
				assert.NotNil(t, data["basicInfo"])
				assert.Nil(t, data["slug"])
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
				assert.NotNil(t, data["franchises"])
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
			expectedError: "pageType is required",
		},
		{
			name: "invalid page type",
			input: &Input{
				PageType: "invalid",
			},
			expectedError: "must be one of: home, listing, detail, search",
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
															"level11": "too deep",
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
			expectedError: "object nesting too deep",
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
			expectedError: "data too large",
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
														"level11": "too deep",
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
			expectedError: "object nesting too deep",
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
			expectedError: "data too large",
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

	t.Run("float64 to int conversion", func(t *testing.T) {
		data := map[string]interface{}{
			"page":  1.0,
			"limit": 20.0,
			"total": 100.0,
			"float": 3.14,
		}

		input := &Input{PageType: "listing", Data: data}
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
				"slug":      "test",
				"basicInfo": map[string]interface{}{"name": "Test"},
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

// package buildresponse

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"os"
// 	"testing"
// 	"time"

// 	"camunda-workers/internal/common/logger" // Add your logger package import

// 	"github.com/stretchr/testify/assert"
// 	"github.com/stretchr/testify/require"
// )

// // ==========================
// // Test Helper Functions
// // ==========================

// func createTestConfig() *Config {
// 	return &Config{
// 		TemplateRegistry: "test_registry.json",
// 		CacheTTL:         5 * time.Minute,
// 		AppVersion:       "1.0.0",
// 	}
// }

// func createTestHandler(t *testing.T, config *Config) *Handler {
// 	if config == nil {
// 		config = createTestConfig()
// 	}
// 	return NewHandler(config, logger.NewTestLogger(t)) // Changed to use custom logger
// }

// func createTemplateRegistry(templates []TemplateDefinition) string {
// 	registry := struct {
// 		Templates []TemplateDefinition `json:"templates"`
// 	}{Templates: templates}

// 	data, _ := json.MarshalIndent(registry, "", "  ")
// 	return string(data)
// }

// func createTestInput(templateId, requestId string, data map[string]interface{}) *Input {
// 	return &Input{
// 		TemplateId: templateId,
// 		RequestId:  requestId,
// 		Data:       data,
// 	}
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name           string
// 		templates      []TemplateDefinition
// 		input          *Input
// 		expectedOutput *Output
// 		validateOutput func(t *testing.T, output *Output)
// 	}{
// 		{
// 			name: "successful response build with validation",
// 			templates: []TemplateDefinition{
// 				{
// 					ID:   "franchise-detail",
// 					Type: "franchise-detail",
// 					Schema: map[string]interface{}{
// 						"type": "object",
// 						"properties": map[string]interface{}{
// 							"name":        map[string]interface{}{"type": "string"},
// 							"investment":  map[string]interface{}{"type": "number"},
// 							"category":    map[string]interface{}{"type": "string"},
// 							"description": map[string]interface{}{"type": "string"},
// 						},
// 						"required": []string{"name", "investment"},
// 					},
// 					Template: map[string]interface{}{
// 						"franchise": map[string]interface{}{
// 							"name":        "{{name}}",
// 							"investment":  "{{investment}}",
// 							"category":    "{{category}}",
// 							"description": "{{description}}",
// 							"features":    []string{"feature1", "feature2"},
// 						},
// 						"metadata": map[string]interface{}{
// 							"source": "template",
// 						},
// 					},
// 					Version: "1.0",
// 				},
// 			},
// 			input: createTestInput("franchise-detail", "req-123", map[string]interface{}{
// 				"name":        "McDonald's",
// 				"investment":  500000,
// 				"category":    "food",
// 				"description": "Fast food franchise",
// 			}),
// 			expectedOutput: &Output{
// 				Response: ResponsePayload{
// 					RequestId: "req-123",
// 					Status:    "success",
// 					Data: map[string]interface{}{
// 						"franchise": map[string]interface{}{
// 							"name":        "McDonald's",
// 							"investment":  float64(500000),
// 							"category":    "food",
// 							"description": "Fast food franchise",
// 							"features":    []interface{}{"feature1", "feature2"},
// 						},
// 						"metadata": map[string]interface{}{
// 							"source": "template",
// 						},
// 					},
// 					Metadata: ResponseMetadata{
// 						Version: "1.0.0",
// 					},
// 				},
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, "req-123", output.Response.RequestId)
// 				assert.Equal(t, "success", output.Response.Status)
// 				assert.Equal(t, "1.0.0", output.Response.Metadata.Version)
// 				assert.NotEmpty(t, output.Response.Metadata.Timestamp)

// 				data := output.Response.Data
// 				franchise := data["franchise"].(map[string]interface{})
// 				assert.Equal(t, "McDonald's", franchise["name"])
// 				assert.Equal(t, float64(500000), franchise["investment"])
// 				assert.Equal(t, "food", franchise["category"])
// 			},
// 		},
// 		{
// 			name: "minimal template without schema",
// 			templates: []TemplateDefinition{
// 				{
// 					ID:       "simple-template",
// 					Type:     "simple",
// 					Schema:   map[string]interface{}{},
// 					Template: map[string]interface{}{"message": "{{text}}"},
// 					Version:  "1.0",
// 				},
// 			},
// 			input: createTestInput("simple-template", "req-456", map[string]interface{}{
// 				"text": "Hello World",
// 			}),
// 			expectedOutput: &Output{
// 				Response: ResponsePayload{
// 					RequestId: "req-456",
// 					Status:    "success",
// 					Data:      map[string]interface{}{"message": "Hello World"},
// 					Metadata: ResponseMetadata{
// 						Version: "1.0.0",
// 					},
// 				},
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, "Hello World", output.Response.Data["message"])
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			// Create temporary registry file
// 			registryContent := createTemplateRegistry(tt.templates)
// 			tmpFile, err := os.CreateTemp("", "test_registry_*.json")
// 			require.NoError(t, err)
// 			defer os.Remove(tmpFile.Name())

// 			_, err = tmpFile.WriteString(registryContent)
// 			require.NoError(t, err)
// 			tmpFile.Close()

// 			config := createTestConfig()
// 			config.TemplateRegistry = tmpFile.Name()
// 			handler := createTestHandler(t, config)

// 			output, err := handler.Execute(context.Background(), tt.input) // Changed to Execute

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			if tt.expectedOutput != nil {
// 				assert.Equal(t, tt.expectedOutput.Response.RequestId, output.Response.RequestId)
// 				assert.Equal(t, tt.expectedOutput.Response.Status, output.Response.Status)
// 				assert.Equal(t, tt.expectedOutput.Response.Metadata.Version, output.Response.Metadata.Version)
// 			}
// 			assert.NotEmpty(t, output.Response.Metadata.Timestamp)

// 			if tt.validateOutput != nil {
// 				tt.validateOutput(t, output)
// 			}
// 		})
// 	}
// }

// func TestHandler_NestedDataSubstitution(t *testing.T) {
// 	templates := []TemplateDefinition{
// 		{
// 			ID:   "nested-template",
// 			Type: "nested",
// 			Template: map[string]interface{}{
// 				"user": map[string]interface{}{
// 					"profile": map[string]interface{}{
// 						"name": "{{user.name}}",
// 						"role": "{{user.role}}",
// 					},
// 					"settings": map[string]interface{}{
// 						"notifications": true,
// 					},
// 				},
// 			},
// 			Version: "1.0",
// 		},
// 	}

// 	registryContent := createTemplateRegistry(templates)
// 	tmpFile, err := os.CreateTemp("", "test_registry_*.json")
// 	require.NoError(t, err)
// 	defer os.Remove(tmpFile.Name())

// 	_, err = tmpFile.WriteString(registryContent)
// 	require.NoError(t, err)
// 	tmpFile.Close()

// 	config := createTestConfig()
// 	config.TemplateRegistry = tmpFile.Name()
// 	handler := createTestHandler(t, config)

// 	input := createTestInput("nested-template", "req-789", map[string]interface{}{
// 		"user": map[string]interface{}{
// 			"name": "John Doe",
// 			"role": "admin",
// 		},
// 	})

// 	output, err := handler.Execute(context.Background(), input) // Changed to Execute

// 	require.NoError(t, err)
// 	require.NotNil(t, output)
// 	require.NotNil(t, output.Response.Data)

// 	data := output.Response.Data
// 	t.Logf("Output data: %+v", data)

// 	require.Contains(t, data, "user")
// 	userInterface := data["user"]
// 	require.NotNil(t, userInterface)

// 	user, ok := userInterface.(map[string]interface{})
// 	require.True(t, ok, "user should be a map")

// 	require.Contains(t, user, "profile")
// 	profileInterface := user["profile"]
// 	require.NotNil(t, profileInterface)

// 	profile, ok := profileInterface.(map[string]interface{})
// 	require.True(t, ok, "profile should be a map")

// 	require.Contains(t, user, "settings")
// 	settingsInterface := user["settings"]
// 	require.NotNil(t, settingsInterface)

// 	settings, ok := settingsInterface.(map[string]interface{})
// 	require.True(t, ok, "settings should be a map")

// 	assert.Equal(t, "John Doe", profile["name"])
// 	assert.Equal(t, "admin", profile["role"])
// 	assert.Equal(t, true, settings["notifications"])
// }

// func TestHandler_Execute_ValidationErrors(t *testing.T) {
// 	tests := []struct {
// 		name          string
// 		templates     []TemplateDefinition
// 		input         *Input
// 		expectedError string
// 	}{
// 		{
// 			name: "template not found",
// 			templates: []TemplateDefinition{
// 				{
// 					ID:       "other-template",
// 					Type:     "other",
// 					Template: map[string]interface{}{},
// 					Version:  "1.0",
// 				},
// 			},
// 			input:         createTestInput("non-existent-template", "req-123", map[string]interface{}{}),
// 			expectedError: "TEMPLATE_NOT_FOUND",
// 		},
// 		{
// 			name: "schema validation failed",
// 			templates: []TemplateDefinition{
// 				{
// 					ID:   "validated-template",
// 					Type: "validated",
// 					Schema: map[string]interface{}{
// 						"type": "object",
// 						"properties": map[string]interface{}{
// 							"requiredField": map[string]interface{}{"type": "string"},
// 						},
// 						"required": []string{"requiredField"},
// 					},
// 					Template: map[string]interface{}{},
// 					Version:  "1.0",
// 				},
// 			},
// 			input: createTestInput("validated-template", "req-123", map[string]interface{}{
// 				"optionalField": "value",
// 			}),
// 			expectedError: "TEMPLATE_VALIDATION_FAILED: data validation failed",
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			// Create temporary registry file
// 			registryContent := createTemplateRegistry(tt.templates)
// 			tmpFile, err := os.CreateTemp("", "test_registry_*.json")
// 			require.NoError(t, err)
// 			defer os.Remove(tmpFile.Name())

// 			_, err = tmpFile.WriteString(registryContent)
// 			require.NoError(t, err)
// 			tmpFile.Close()

// 			config := createTestConfig()
// 			config.TemplateRegistry = tmpFile.Name()
// 			handler := createTestHandler(t, config)

// 			output, err := handler.Execute(context.Background(), tt.input) // Changed to Execute

// 			assert.Error(t, err)
// 			assert.Contains(t, err.Error(), tt.expectedError)
// 			assert.Nil(t, output)
// 		})
// 	}
// }

// func TestHandler_Execute_RegistryFileErrors(t *testing.T) {
// 	t.Run("registry file not found", func(t *testing.T) {
// 		config := createTestConfig()
// 		config.TemplateRegistry = "/non/existent/path/registry.json"
// 		handler := createTestHandler(t, config)

// 		input := createTestInput("any-template", "req-123", map[string]interface{}{})
// 		output, err := handler.Execute(context.Background(), input) // Changed to Execute

// 		assert.Error(t, err)
// 		assert.Contains(t, err.Error(), "read registry")
// 		assert.Nil(t, output)
// 	})

// 	t.Run("invalid registry JSON", func(t *testing.T) {
// 		tmpFile, err := os.CreateTemp("", "test_invalid_registry_*.json")
// 		require.NoError(t, err)
// 		defer os.Remove(tmpFile.Name())

// 		_, err = tmpFile.WriteString("invalid json content")
// 		require.NoError(t, err)
// 		tmpFile.Close()

// 		config := createTestConfig()
// 		config.TemplateRegistry = tmpFile.Name()
// 		handler := createTestHandler(t, config)

// 		input := createTestInput("any-template", "req-123", map[string]interface{}{})
// 		output, err := handler.Execute(context.Background(), input) // Changed to Execute

// 		assert.Error(t, err)
// 		assert.Contains(t, err.Error(), "parse registry")
// 		assert.Nil(t, output)
// 	})
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_LoadTemplate(t *testing.T) {
// 	templates := []TemplateDefinition{
// 		{
// 			ID:       "template-1",
// 			Type:     "type-1",
// 			Template: map[string]interface{}{"key": "value1"},
// 			Version:  "1.0",
// 		},
// 		{
// 			ID:       "template-2",
// 			Type:     "type-2",
// 			Template: map[string]interface{}{"key": "value2"},
// 			Version:  "1.0",
// 		},
// 	}

// 	registryContent := createTemplateRegistry(templates)
// 	tmpFile, err := os.CreateTemp("", "test_registry_*.json")
// 	require.NoError(t, err)
// 	defer os.Remove(tmpFile.Name())

// 	_, err = tmpFile.WriteString(registryContent)
// 	require.NoError(t, err)
// 	tmpFile.Close()

// 	config := createTestConfig()
// 	config.TemplateRegistry = tmpFile.Name()
// 	handler := createTestHandler(t, config)

// 	t.Run("template found", func(t *testing.T) {
// 		template, err := handler.loadTemplate("template-1")
// 		assert.NoError(t, err)
// 		assert.Equal(t, "template-1", template.ID)
// 		assert.Equal(t, "type-1", template.Type)
// 	})

// 	t.Run("template not found", func(t *testing.T) {
// 		template, err := handler.loadTemplate("non-existent")
// 		assert.Error(t, err)
// 		assert.True(t, errors.Is(err, ErrTemplateNotFound))
// 		assert.Nil(t, template)
// 	})

// 	t.Run("caching works", func(t *testing.T) {
// 		// First call should load from file
// 		template1, err := handler.loadTemplate("template-2")
// 		assert.NoError(t, err)
// 		assert.Equal(t, "template-2", template1.ID)

// 		// Second call should use cache
// 		template2, err := handler.loadTemplate("template-2")
// 		assert.NoError(t, err)
// 		assert.Equal(t, template1, template2) // Same pointer indicates cache hit
// 	})
// }

// func TestHandler_ValidateData(t *testing.T) {
// 	handler := createTestHandler(t, nil)

// 	tests := []struct {
// 		name    string
// 		schema  map[string]interface{}
// 		data    map[string]interface{}
// 		wantErr bool
// 	}{
// 		{
// 			name: "valid data",
// 			schema: map[string]interface{}{
// 				"type": "object",
// 				"properties": map[string]interface{}{
// 					"name": map[string]interface{}{"type": "string"},
// 					"age":  map[string]interface{}{"type": "number"},
// 				},
// 				"required": []string{"name"},
// 			},
// 			data: map[string]interface{}{
// 				"name": "John",
// 				"age":  30,
// 			},
// 			wantErr: false,
// 		},
// 		{
// 			name: "missing required field",
// 			schema: map[string]interface{}{
// 				"type": "object",
// 				"properties": map[string]interface{}{
// 					"name": map[string]interface{}{"type": "string"},
// 				},
// 				"required": []string{"name"},
// 			},
// 			data: map[string]interface{}{
// 				"age": 30,
// 			},
// 			wantErr: true,
// 		},
// 		{
// 			name: "wrong data type",
// 			schema: map[string]interface{}{
// 				"type": "object",
// 				"properties": map[string]interface{}{
// 					"age": map[string]interface{}{"type": "number"},
// 				},
// 			},
// 			data: map[string]interface{}{
// 				"age": "not-a-number",
// 			},
// 			wantErr: true,
// 		},
// 		{
// 			name:    "empty schema",
// 			schema:  map[string]interface{}{},
// 			data:    map[string]interface{}{"any": "data"},
// 			wantErr: false,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			err := handler.validateData(tt.schema, tt.data)
// 			if tt.wantErr {
// 				assert.Error(t, err)
// 			} else {
// 				assert.NoError(t, err)
// 			}
// 		})
// 	}
// }

// func TestHandler_DeepMerge(t *testing.T) {
// 	handler := createTestHandler(t, nil)

// 	tests := []struct {
// 		name     string
// 		dst      map[string]interface{}
// 		src      map[string]interface{}
// 		expected map[string]interface{}
// 	}{
// 		{
// 			name: "simple merge",
// 			dst:  map[string]interface{}{"a": 1, "b": 2},
// 			src:  map[string]interface{}{"b": 3, "c": 4},
// 			expected: map[string]interface{}{
// 				"a": 1, "b": 3, "c": 4,
// 			},
// 		},
// 		{
// 			name:     "empty source",
// 			dst:      map[string]interface{}{"a": 1},
// 			src:      map[string]interface{}{},
// 			expected: map[string]interface{}{"a": 1},
// 		},
// 		{
// 			name:     "empty destination",
// 			dst:      map[string]interface{}{},
// 			src:      map[string]interface{}{"a": 1},
// 			expected: map[string]interface{}{"a": 1},
// 		},
// 		{
// 			name: "nested objects",
// 			dst: map[string]interface{}{
// 				"user": map[string]interface{}{
// 					"name": "John",
// 					"age":  30,
// 				},
// 			},
// 			src: map[string]interface{}{
// 				"user": map[string]interface{}{
// 					"age":  31,
// 					"role": "admin",
// 				},
// 			},
// 			expected: map[string]interface{}{
// 				"user": map[string]interface{}{
// 					"age":  31,
// 					"role": "admin",
// 				},
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result := handler.deepMerge(tt.dst, tt.src)
// 			assert.Equal(t, tt.expected, result)
// 		})
// 	}
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	t.Run("cache TTL expiration", func(t *testing.T) {
// 		templates := []TemplateDefinition{
// 			{
// 				ID:       "test-template",
// 				Type:     "test",
// 				Template: map[string]interface{}{},
// 				Version:  "1.0",
// 			},
// 		}

// 		registryContent := createTemplateRegistry(templates)
// 		tmpFile, err := os.CreateTemp("", "test_registry_*.json")
// 		require.NoError(t, err)
// 		defer os.Remove(tmpFile.Name())

// 		_, err = tmpFile.WriteString(registryContent)
// 		require.NoError(t, err)
// 		tmpFile.Close()

// 		config := createTestConfig()
// 		config.TemplateRegistry = tmpFile.Name()
// 		config.CacheTTL = 1 * time.Millisecond // Very short TTL
// 		handler := createTestHandler(t, config)

// 		// First call - cache miss
// 		template1, err := handler.loadTemplate("test-template")
// 		assert.NoError(t, err)

// 		// Wait for cache to expire
// 		time.Sleep(2 * time.Millisecond)

// 		// Second call - should be cache miss again
// 		template2, err := handler.loadTemplate("test-template")
// 		assert.NoError(t, err)
// 		assert.NotEqual(t, fmt.Sprintf("%p", template1), fmt.Sprintf("%p", template2)) // Different pointers
// 	})

// 	t.Run("template with complex schema", func(t *testing.T) {
// 		complexSchema := map[string]interface{}{
// 			"type": "object",
// 			"properties": map[string]interface{}{
// 				"arrayField": map[string]interface{}{
// 					"type": "array",
// 					"items": map[string]interface{}{
// 						"type": "string",
// 					},
// 				},
// 				"nestedObject": map[string]interface{}{
// 					"type": "object",
// 					"properties": map[string]interface{}{
// 						"nestedField": map[string]interface{}{"type": "string"},
// 					},
// 				},
// 			},
// 		}

// 		templates := []TemplateDefinition{
// 			{
// 				ID:       "complex-template",
// 				Type:     "complex",
// 				Schema:   complexSchema,
// 				Template: map[string]interface{}{},
// 				Version:  "1.0",
// 			},
// 		}

// 		registryContent := createTemplateRegistry(templates)
// 		tmpFile, err := os.CreateTemp("", "test_registry_*.json")
// 		require.NoError(t, err)
// 		defer os.Remove(tmpFile.Name())

// 		_, err = tmpFile.WriteString(registryContent)
// 		require.NoError(t, err)
// 		tmpFile.Close()

// 		config := createTestConfig()
// 		config.TemplateRegistry = tmpFile.Name()
// 		handler := createTestHandler(t, config)

// 		input := createTestInput("complex-template", "req-123", map[string]interface{}{
// 			"arrayField": []string{"item1", "item2"},
// 			"nestedObject": map[string]interface{}{
// 				"nestedField": "value",
// 			},
// 		})

// 		output, err := handler.Execute(context.Background(), input) // Changed to Execute
// 		assert.NoError(t, err)
// 		assert.NotNil(t, output)
// 	})

// 	t.Run("empty data with required schema", func(t *testing.T) {
// 		templates := []TemplateDefinition{
// 			{
// 				ID:   "required-template",
// 				Type: "required",
// 				Schema: map[string]interface{}{
// 					"type": "object",
// 					"properties": map[string]interface{}{
// 						"field": map[string]interface{}{"type": "string"},
// 					},
// 					"required": []string{"field"},
// 				},
// 				Template: map[string]interface{}{},
// 				Version:  "1.0",
// 			},
// 		}

// 		registryContent := createTemplateRegistry(templates)
// 		tmpFile, err := os.CreateTemp("", "test_registry_*.json")
// 		require.NoError(t, err)
// 		defer os.Remove(tmpFile.Name())

// 		_, err = tmpFile.WriteString(registryContent)
// 		require.NoError(t, err)
// 		tmpFile.Close()

// 		config := createTestConfig()
// 		config.TemplateRegistry = tmpFile.Name()
// 		handler := createTestHandler(t, config)

// 		input := createTestInput("required-template", "req-123", map[string]interface{}{})
// 		output, err := handler.Execute(context.Background(), input) // Changed to Execute

// 		assert.Error(t, err)
// 		assert.Nil(t, output)
// 	})
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	templates := []TemplateDefinition{
// 		{
// 			ID:   "franchise-search-result",
// 			Type: "search-result",
// 			Schema: map[string]interface{}{
// 				"type": "object",
// 				"properties": map[string]interface{}{
// 					"franchises": map[string]interface{}{
// 						"type": "array",
// 						"items": map[string]interface{}{
// 							"type": "object",
// 							"properties": map[string]interface{}{
// 								"name":       map[string]interface{}{"type": "string"},
// 								"investment": map[string]interface{}{"type": "number"},
// 								"category":   map[string]interface{}{"type": "string"},
// 							},
// 							"required": []string{"name", "investment"},
// 						},
// 					},
// 					"totalCount": map[string]interface{}{"type": "number"},
// 				},
// 				"required": []string{"franchises", "totalCount"},
// 			},
// 			Template: map[string]interface{}{
// 				"searchResults": map[string]interface{}{
// 					"franchises": "{{franchises}}",
// 					"pagination": map[string]interface{}{
// 						"total": "{{totalCount}}",
// 						"page":  1,
// 						"size":  20,
// 					},
// 					"metadata": map[string]interface{}{
// 						"searchId": "{{requestId}}",
// 					},
// 				},
// 			},
// 			Version: "1.0",
// 		},
// 	}

// 	registryContent := createTemplateRegistry(templates)
// 	tmpFile, err := os.CreateTemp("", "test_registry_*.json")
// 	require.NoError(t, err)
// 	defer os.Remove(tmpFile.Name())

// 	_, err = tmpFile.WriteString(registryContent)
// 	require.NoError(t, err)
// 	tmpFile.Close()

// 	config := createTestConfig()
// 	config.TemplateRegistry = tmpFile.Name()
// 	handler := createTestHandler(t, config)

// 	// Convert to []interface{} for proper type handling
// 	franchisesData := []interface{}{
// 		map[string]interface{}{
// 			"name":       "McDonald's",
// 			"investment": 500000,
// 			"category":   "food",
// 		},
// 		map[string]interface{}{
// 			"name":       "Subway",
// 			"investment": 150000,
// 			"category":   "food",
// 		},
// 	}

// 	input := createTestInput("franchise-search-result", "search-123", map[string]interface{}{
// 		"franchises": franchisesData,
// 		"totalCount": float64(2),
// 		"requestId":  "search-123",
// 	})

// 	output, err := handler.Execute(context.Background(), input) // Changed to Execute

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)

// 	// Verify the complete response structure
// 	assert.Equal(t, "search-123", output.Response.RequestId)
// 	assert.Equal(t, "success", output.Response.Status)

// 	data := output.Response.Data
// 	searchResults := data["searchResults"].(map[string]interface{})

// 	// The franchises field will be whatever type was substituted
// 	franchisesResult := searchResults["franchises"]
// 	require.NotNil(t, franchisesResult)

// 	// Check if it's a slice and has the right length
// 	franchisesSlice, ok := franchisesResult.([]interface{})
// 	if ok {
// 		assert.Len(t, franchisesSlice, 2)
// 	} else {
// 		t.Logf("franchises is type %T, value: %+v", franchisesResult, franchisesResult)
// 	}

// 	pagination := searchResults["pagination"].(map[string]interface{})
// 	metadata := searchResults["metadata"].(map[string]interface{})

// 	assert.Equal(t, float64(2), pagination["total"])

// 	assert.Equal(t, "search-123", metadata["searchId"])
// }

// // ==========================
// // JSON Serialization Tests
// // ==========================

// func TestHandler_JSONSerialization(t *testing.T) {
// 	output := &Output{
// 		Response: ResponsePayload{
// 			RequestId: "test-123",
// 			Status:    "success",
// 			Data: map[string]interface{}{
// 				"message": "test",
// 				"count":   42,
// 			},
// 			Metadata: ResponseMetadata{
// 				Timestamp: "2023-01-01T00:00:00Z",
// 				Version:   "1.0.0",
// 			},
// 		},
// 	}

// 	jsonData, err := json.Marshal(output)
// 	assert.NoError(t, err)

// 	var decoded Output
// 	err = json.Unmarshal(jsonData, &decoded)
// 	assert.NoError(t, err)
// 	assert.Equal(t, output.Response.RequestId, decoded.Response.RequestId)
// 	assert.Equal(t, output.Response.Status, decoded.Response.Status)
// 	assert.Equal(t, output.Response.Metadata, decoded.Response.Metadata)
// 	// Don't compare Data directly due to JSON number type conversion
// 	assert.Equal(t, "test", decoded.Response.Data["message"])
// 	assert.Equal(t, float64(42), decoded.Response.Data["count"]) // JSON unmarshals numbers as float64
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	templates := []TemplateDefinition{
// 		{
// 			ID:   "benchmark-template",
// 			Type: "benchmark",
// 			Template: map[string]interface{}{
// 				"data": "{{value}}",
// 			},
// 			Version: "1.0",
// 		},
// 	}

// 	registryContent := createTemplateRegistry(templates)
// 	tmpFile, err := os.CreateTemp("", "benchmark_registry_*.json")
// 	require.NoError(b, err)
// 	defer os.Remove(tmpFile.Name())

// 	_, err = tmpFile.WriteString(registryContent)
// 	require.NoError(b, err)
// 	tmpFile.Close()

// 	config := &Config{
// 		TemplateRegistry: tmpFile.Name(),
// 		CacheTTL:         5 * time.Minute,
// 		AppVersion:       "1.0.0",
// 	}
// 	handler := NewHandler(config, logger.NewTestLogger(b)) // Changed to use custom logger

// 	input := createTestInput("benchmark-template", "benchmark-req", map[string]interface{}{
// 		"value": "benchmark data",
// 	})

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_, _ = handler.Execute(context.Background(), input) // Changed to Execute
// 	}
// }

// func BenchmarkHandler_LoadTemplate(b *testing.B) {
// 	templates := []TemplateDefinition{
// 		{
// 			ID:       "benchmark-template",
// 			Type:     "benchmark",
// 			Template: map[string]interface{}{},
// 			Version:  "1.0",
// 		},
// 	}

// 	registryContent := createTemplateRegistry(templates)
// 	tmpFile, err := os.CreateTemp("", "benchmark_registry_*.json")
// 	require.NoError(b, err)
// 	defer os.Remove(tmpFile.Name())

// 	_, err = tmpFile.WriteString(registryContent)
// 	require.NoError(b, err)
// 	tmpFile.Close()

// 	config := &Config{
// 		TemplateRegistry: tmpFile.Name(),
// 		CacheTTL:         5 * time.Minute,
// 	}
// 	handler := NewHandler(config, logger.NewTestLogger(b)) // Changed to use custom logger

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_, _ = handler.loadTemplate("benchmark-template")
// 	}
// }

// func BenchmarkHandler_DeepMerge(b *testing.B) {
// 	handler := NewHandler(&Config{}, logger.NewTestLogger(b)) // Changed to use custom logger

// 	dst := map[string]interface{}{
// 		"field1": "value1",
// 		"field2": "value2",
// 		"nested": map[string]interface{}{
// 			"nested1": "nvalue1",
// 		},
// 	}

// 	src := map[string]interface{}{
// 		"field2": "updated",
// 		"field3": "value3",
// 		"nested": map[string]interface{}{
// 			"nested2": "nvalue2",
// 		},
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.deepMerge(dst, src)
// 	}
// }
