// internal/workers/infrastructure/select-template/handler_test.go
package selecttemplate

import (
	"context"
	"encoding/json"
	"testing"

	"camunda-workers/internal/common/logger"

	"github.com/stretchr/testify/assert"
)

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{
		TemplateRules: map[string]map[string]string{
			"route": {
				"/franchise/search:premium":    "franchise-search-premium",
				"/franchise/search:free":       "franchise-search-free",
				"/franchise/search:fallback":   "franchise-search-fallback",
				"/franchise/detail:premium":    "franchise-detail-premium",
				"/franchise/detail:free":       "franchise-detail-free",
				"/franchise/detail:fallback":   "franchise-detail-fallback",
				"/application/submit:premium":  "application-submit-premium",
				"/application/submit:free":     "application-submit-free",
				"/application/submit:fallback": "application-submit-fallback",
			},
		},
	}
}

func createTestHandler(t *testing.T, config *Config) *Handler {
	if config == nil {
		config = createTestConfig()
	}
	// Use test logger from your logger package
	testLogger := logger.NewTestLogger(t)
	return NewHandler(config, testLogger)
}

func createInput(subscriptionTier, routePath, templateType string, confidence float64) *Input {
	return &Input{
		SubscriptionTier: subscriptionTier,
		RoutePath:        routePath,
		TemplateType:     templateType,
		Confidence:       confidence,
	}
}

// ==========================
// Core Functionality Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	tests := []struct {
		name           string
		input          *Input
		expectedOutput *Output
		validateOutput func(t *testing.T, output *Output)
	}{
		{
			name:  "AI response with high confidence",
			input: createInput("premium", "", "ai-response", 0.9),
			expectedOutput: &Output{
				SelectedTemplateId: "ai-detailed",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "ai-detailed", output.SelectedTemplateId)
			},
		},
		{
			name:  "AI response with low confidence",
			input: createInput("premium", "", "ai-response", 0.7),
			expectedOutput: &Output{
				SelectedTemplateId: "ai-tentative",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "ai-tentative", output.SelectedTemplateId)
			},
		},
		{
			name:  "premium franchise search route",
			input: createInput("premium", "/franchise/search", "", 0),
			expectedOutput: &Output{
				SelectedTemplateId: "franchise-search-premium",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "franchise-search-premium", output.SelectedTemplateId)
			},
		},
		{
			name:  "free franchise search route",
			input: createInput("free", "/franchise/search", "", 0),
			expectedOutput: &Output{
				SelectedTemplateId: "franchise-search-free",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "franchise-search-free", output.SelectedTemplateId)
			},
		},
		{
			name:  "premium franchise detail route",
			input: createInput("premium", "/franchise/detail", "", 0),
			expectedOutput: &Output{
				SelectedTemplateId: "franchise-detail-premium",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "franchise-detail-premium", output.SelectedTemplateId)
			},
		},
		{
			name:  "free franchise detail route",
			input: createInput("free", "/franchise/detail", "", 0),
			expectedOutput: &Output{
				SelectedTemplateId: "franchise-detail-free",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "franchise-detail-free", output.SelectedTemplateId)
			},
		},
		{
			name:  "premium application submit route",
			input: createInput("premium", "/application/submit", "", 0),
			expectedOutput: &Output{
				SelectedTemplateId: "application-submit-premium",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "application-submit-premium", output.SelectedTemplateId)
			},
		},
		{
			name:  "free application submit route",
			input: createInput("free", "/application/submit", "", 0),
			expectedOutput: &Output{
				SelectedTemplateId: "application-submit-free",
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "application-submit-free", output.SelectedTemplateId)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := createTestHandler(t, nil)
			output, err := handler.Execute(context.Background(), tt.input) // Changed to Execute

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedOutput.SelectedTemplateId, output.SelectedTemplateId)

			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}
		})
	}
}

func TestHandler_Execute_FallbackScenarios(t *testing.T) {
	tests := []struct {
		name           string
		config         *Config
		input          *Input
		expectedOutput *Output
	}{
		{
			name: "route fallback when specific tier not found",
			config: &Config{
				TemplateRules: map[string]map[string]string{
					"route": {
						"/franchise/search:fallback": "franchise-search-fallback",
					},
				},
			},
			input: createInput("premium", "/franchise/search", "", 0),
			expectedOutput: &Output{
				SelectedTemplateId: "franchise-search-fallback",
			},
		},
		{
			name: "default fallback when no rules match",
			config: &Config{
				TemplateRules: map[string]map[string]string{
					"route": {
						"other-route:premium": "other-template",
					},
				},
			},
			input: createInput("premium", "/unknown/route", "", 0),
			expectedOutput: &Output{
				SelectedTemplateId: "default-template",
			},
		},
		{
			name: "default fallback when no route provided",
			config: &Config{
				TemplateRules: map[string]map[string]string{
					"route": {
						"/some/route:premium": "some-template",
					},
				},
			},
			input: createInput("premium", "", "", 0),
			expectedOutput: &Output{
				SelectedTemplateId: "default-template",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := createTestHandler(t, tt.config)
			output, err := handler.Execute(context.Background(), tt.input) // Changed to Execute

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedOutput.SelectedTemplateId, output.SelectedTemplateId)
		})
	}
}

func TestHandler_Execute_AIResponseEdgeCases(t *testing.T) {
	tests := []struct {
		name           string
		input          *Input
		expectedOutput *Output
	}{
		{
			name:  "AI response with exact threshold confidence",
			input: createInput("premium", "", "ai-response", 0.8),
			expectedOutput: &Output{
				SelectedTemplateId: "ai-detailed",
			},
		},
		{
			name:  "AI response with very low confidence",
			input: createInput("premium", "", "ai-response", 0.1),
			expectedOutput: &Output{
				SelectedTemplateId: "ai-tentative",
			},
		},
		{
			name:  "AI response with zero confidence",
			input: createInput("premium", "", "ai-response", 0.0),
			expectedOutput: &Output{
				SelectedTemplateId: "ai-tentative",
			},
		},
		{
			name:  "AI response with negative confidence",
			input: createInput("premium", "", "ai-response", -0.5),
			expectedOutput: &Output{
				SelectedTemplateId: "ai-tentative",
			},
		},
		{
			name:  "AI response with confidence above 1",
			input: createInput("premium", "", "ai-response", 1.5),
			expectedOutput: &Output{
				SelectedTemplateId: "ai-detailed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := createTestHandler(t, nil)
			output, err := handler.Execute(context.Background(), tt.input) // Changed to Execute

			assert.NoError(t, err)
			assert.NotNil(t, output)
			assert.Equal(t, tt.expectedOutput.SelectedTemplateId, output.SelectedTemplateId)
		})
	}
}

func TestHandler_Execute_ErrorCases(t *testing.T) {
	t.Run("missing route template rules in config", func(t *testing.T) {
		config := &Config{
			TemplateRules: map[string]map[string]string{
				// No "route" key
				"other": {
					"key": "value",
				},
			},
		}
		handler := createTestHandler(t, config)
		input := createInput("premium", "/franchise/search", "", 0)

		output, err := handler.Execute(context.Background(), input) // Changed to Execute

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing route template rules in config")
		assert.Nil(t, output)
	})
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_AIResponseSelection(t *testing.T) {
	handler := createTestHandler(t, nil)

	tests := []struct {
		name       string
		confidence float64
		expected   string
	}{
		{
			name:       "high confidence selects detailed",
			confidence: 0.81,
			expected:   "ai-detailed",
		},
		{
			name:       "threshold confidence selects detailed",
			confidence: 0.8,
			expected:   "ai-detailed",
		},
		{
			name:       "medium confidence selects tentative",
			confidence: 0.79,
			expected:   "ai-tentative",
		},
		{
			name:       "low confidence selects tentative",
			confidence: 0.1,
			expected:   "ai-tentative",
		},
		{
			name:       "zero confidence selects tentative",
			confidence: 0.0,
			expected:   "ai-tentative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := createInput("premium", "", "ai-response", tt.confidence)
			output, err := handler.Execute(context.Background(), input) // Changed to Execute

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, output.SelectedTemplateId)
		})
	}
}

func TestHandler_RouteBasedSelection(t *testing.T) {
	handler := createTestHandler(t, nil)

	tests := []struct {
		name             string
		routePath        string
		subscriptionTier string
		expected         string
	}{
		{
			name:             "premium franchise search",
			routePath:        "/franchise/search",
			subscriptionTier: "premium",
			expected:         "franchise-search-premium",
		},
		{
			name:             "free franchise search",
			routePath:        "/franchise/search",
			subscriptionTier: "free",
			expected:         "franchise-search-free",
		},
		{
			name:             "premium franchise detail",
			routePath:        "/franchise/detail",
			subscriptionTier: "premium",
			expected:         "franchise-detail-premium",
		},
		{
			name:             "free franchise detail",
			routePath:        "/franchise/detail",
			subscriptionTier: "free",
			expected:         "franchise-detail-free",
		},
		{
			name:             "premium application submit",
			routePath:        "/application/submit",
			subscriptionTier: "premium",
			expected:         "application-submit-premium",
		},
		{
			name:             "free application submit",
			routePath:        "/application/submit",
			subscriptionTier: "free",
			expected:         "application-submit-free",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := createInput(tt.subscriptionTier, tt.routePath, "", 0)
			output, err := handler.Execute(context.Background(), input) // Changed to Execute

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, output.SelectedTemplateId)
		})
	}
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	handler := createTestHandler(t, nil)

	t.Run("empty subscription tier", func(t *testing.T) {
		input := &Input{
			SubscriptionTier: "",
			RoutePath:        "/franchise/search",
		}

		output, err := handler.Execute(context.Background(), input) // Changed to Execute
		assert.NoError(t, err)
		// Should use fallback logic since empty tier won't match premium/free
		assert.Equal(t, "franchise-search-fallback", output.SelectedTemplateId)
	})

	t.Run("unknown subscription tier", func(t *testing.T) {
		input := createInput("enterprise", "/franchise/search", "", 0)

		output, err := handler.Execute(context.Background(), input) // Changed to Execute
		assert.NoError(t, err)
		assert.Equal(t, "franchise-search-fallback", output.SelectedTemplateId)
	})

	t.Run("unknown route path", func(t *testing.T) {
		input := createInput("premium", "/unknown/path", "", 0)

		output, err := handler.Execute(context.Background(), input) // Changed to Execute
		assert.NoError(t, err)
		assert.Equal(t, "default-template", output.SelectedTemplateId)
	})

	t.Run("AI response with empty template type", func(t *testing.T) {
		input := createInput("premium", "", "", 0.9)

		output, err := handler.Execute(context.Background(), input) // Changed to Execute
		assert.NoError(t, err)
		// Should use route-based selection (which falls back to default)
		assert.Equal(t, "default-template", output.SelectedTemplateId)
	})

	t.Run("route path takes precedence over AI response", func(t *testing.T) {
		input := createInput("premium", "/franchise/search", "ai-response", 0.9)

		output, err := handler.Execute(context.Background(), input) // Changed to Execute
		assert.NoError(t, err)
		// Route-based selection should take precedence
		assert.Equal(t, "franchise-search-premium", output.SelectedTemplateId)
	})
}

func TestHandler_ConfigEdgeCases(t *testing.T) {
	t.Run("empty config rules", func(t *testing.T) {
		config := &Config{
			TemplateRules: map[string]map[string]string{},
		}
		handler := createTestHandler(t, config)
		input := createInput("premium", "/franchise/search", "", 0)

		output, err := handler.Execute(context.Background(), input) // Changed to Execute

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing route template rules in config")
		assert.Nil(t, output)
	})

	t.Run("nil config rules", func(t *testing.T) {
		config := &Config{
			TemplateRules: nil,
		}
		handler := createTestHandler(t, config)
		input := createInput("premium", "/franchise/search", "", 0)

		output, err := handler.Execute(context.Background(), input) // Changed to Execute

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing route template rules in config")
		assert.Nil(t, output)
	})

	t.Run("partial route rules", func(t *testing.T) {
		config := &Config{
			TemplateRules: map[string]map[string]string{
				"route": {
					"/franchise/search:premium": "search-premium",
					// Missing free and fallback
				},
			},
		}
		handler := createTestHandler(t, config)

		t.Run("premium tier works", func(t *testing.T) {
			input := createInput("premium", "/franchise/search", "", 0)
			output, err := handler.Execute(context.Background(), input) // Changed to Execute
			assert.NoError(t, err)
			assert.Equal(t, "search-premium", output.SelectedTemplateId)
		})

		t.Run("free tier falls back to default", func(t *testing.T) {
			input := createInput("free", "/franchise/search", "", 0)
			output, err := handler.Execute(context.Background(), input) // Changed to Execute
			assert.NoError(t, err)
			assert.Equal(t, "default-template", output.SelectedTemplateId)
		})
	})
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	config := &Config{
		TemplateRules: map[string]map[string]string{
			"route": {
				"/franchise/search:premium":  "search-premium-template",
				"/franchise/search:free":     "search-free-template",
				"/franchise/search:fallback": "search-fallback-template",
				"/franchise/detail:premium":  "detail-premium-template",
				"/franchise/detail:free":     "detail-free-template",
				"/franchise/detail:fallback": "detail-fallback-template",
			},
		},
	}
	handler := createTestHandler(t, config)

	tests := []struct {
		name        string
		input       *Input
		description string
	}{
		{
			name:        "AI workflow with high confidence",
			input:       createInput("premium", "", "ai-response", 0.95),
			description: "Should select AI detailed template for high confidence",
		},
		{
			name:        "AI workflow with medium confidence",
			input:       createInput("free", "", "ai-response", 0.6),
			description: "Should select AI tentative template for medium confidence",
		},
		{
			name:        "Premium franchise search workflow",
			input:       createInput("premium", "/franchise/search", "", 0),
			description: "Should select premium search template",
		},
		{
			name:        "Free franchise search workflow",
			input:       createInput("free", "/franchise/search", "", 0),
			description: "Should select free search template",
		},
		{
			name:        "Premium franchise detail workflow",
			input:       createInput("premium", "/franchise/detail", "", 0),
			description: "Should select premium detail template",
		},
		{
			name:        "Free franchise detail workflow",
			input:       createInput("free", "/franchise/detail", "", 0),
			description: "Should select free detail template",
		},
		{
			name:        "Fallback workflow for unknown route",
			input:       createInput("premium", "/unknown/route", "", 0),
			description: "Should select default template for unknown route",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := handler.Execute(context.Background(), tt.input) // Changed to Execute

			assert.NoError(t, err, tt.description)
			assert.NotNil(t, output, tt.description)
			assert.NotEmpty(t, output.SelectedTemplateId, tt.description)

			// Verify the selection logic worked correctly
			switch {
			case tt.input.TemplateType == "ai-response" && tt.input.Confidence > 0:
				if tt.input.Confidence > 0.8 {
					assert.Equal(t, "ai-detailed", output.SelectedTemplateId, tt.description)
				} else {
					assert.Equal(t, "ai-tentative", output.SelectedTemplateId, tt.description)
				}
			case tt.input.RoutePath == "/franchise/search":
				switch tt.input.SubscriptionTier {
				case "premium":
					assert.Equal(t, "search-premium-template", output.SelectedTemplateId, tt.description)
				case "free":
					assert.Equal(t, "search-free-template", output.SelectedTemplateId, tt.description)
				}
			case tt.input.RoutePath == "/franchise/detail":
				switch tt.input.SubscriptionTier {
				case "premium":
					assert.Equal(t, "detail-premium-template", output.SelectedTemplateId, tt.description)
				case "free":
					assert.Equal(t, "detail-free-template", output.SelectedTemplateId, tt.description)
				}
			default:
				assert.Equal(t, "default-template", output.SelectedTemplateId, tt.description)
			}
		})
	}
}

// ==========================
// JSON Serialization Tests
// ==========================

func TestHandler_JSONSerialization(t *testing.T) {
	output := &Output{
		SelectedTemplateId: "test-template-123",
	}

	jsonData, err := json.Marshal(output)
	assert.NoError(t, err)

	var decoded Output
	err = json.Unmarshal(jsonData, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, output.SelectedTemplateId, decoded.SelectedTemplateId)
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	config := createTestConfig()
	handler := NewHandler(config, logger.NewTestLogger(b))

	inputs := []*Input{
		createInput("premium", "/franchise/search", "", 0),
		createInput("free", "/franchise/detail", "", 0),
		createInput("premium", "", "ai-response", 0.9),
		createInput("free", "", "ai-response", 0.7),
		createInput("premium", "/application/submit", "", 0),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), inputs[i%len(inputs)]) // Changed to Execute
	}
}

func BenchmarkHandler_AIResponseSelection(b *testing.B) {
	handler := NewHandler(createTestConfig(), logger.NewTestLogger(b))

	inputs := []*Input{
		createInput("premium", "", "ai-response", 0.95),
		createInput("premium", "", "ai-response", 0.75),
		createInput("premium", "", "ai-response", 0.5),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), inputs[i%len(inputs)]) // Changed to Execute
	}
}

func BenchmarkHandler_RouteBasedSelection(b *testing.B) {
	handler := NewHandler(createTestConfig(), logger.NewTestLogger(b))

	inputs := []*Input{
		createInput("premium", "/franchise/search", "", 0),
		createInput("free", "/franchise/search", "", 0),
		createInput("premium", "/franchise/detail", "", 0),
		createInput("free", "/franchise/detail", "", 0),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Execute(context.Background(), inputs[i%len(inputs)]) // Changed to Execute
	}
}

// // internal/workers/infrastructure/select-template/handler_test.go
// package selecttemplate

// import (
// 	"context"
// 	"encoding/json"
// 	"testing"

// 	"github.com/stretchr/testify/assert"
// 	"go.uber.org/zap/zaptest"
// )

// // ==========================
// // Test Helper Functions
// // ==========================

// func createTestConfig() *Config {
// 	return &Config{
// 		TemplateRules: map[string]map[string]string{
// 			"route": {
// 				"/franchise/search:premium":    "franchise-search-premium",
// 				"/franchise/search:free":       "franchise-search-free",
// 				"/franchise/search:fallback":   "franchise-search-fallback",
// 				"/franchise/detail:premium":    "franchise-detail-premium",
// 				"/franchise/detail:free":       "franchise-detail-free",
// 				"/franchise/detail:fallback":   "franchise-detail-fallback",
// 				"/application/submit:premium":  "application-submit-premium",
// 				"/application/submit:free":     "application-submit-free",
// 				"/application/submit:fallback": "application-submit-fallback",
// 			},
// 		},
// 	}
// }

// func createTestHandler(t *testing.T, config *Config) *Handler {
// 	if config == nil {
// 		config = createTestConfig()
// 	}
// 	return NewHandler(config, zaptest.NewLogger(t))
// }

// func createInput(subscriptionTier, routePath, templateType string, confidence float64) *Input {
// 	return &Input{
// 		SubscriptionTier: subscriptionTier,
// 		RoutePath:        routePath,
// 		TemplateType:     templateType,
// 		Confidence:       confidence,
// 	}
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	tests := []struct {
// 		name           string
// 		input          *Input
// 		expectedOutput *Output
// 		validateOutput func(t *testing.T, output *Output)
// 	}{
// 		{
// 			name:  "AI response with high confidence",
// 			input: createInput("premium", "", "ai-response", 0.9),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "ai-detailed",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, "ai-detailed", output.SelectedTemplateId)
// 			},
// 		},
// 		{
// 			name:  "AI response with low confidence",
// 			input: createInput("premium", "", "ai-response", 0.7),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "ai-tentative",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, "ai-tentative", output.SelectedTemplateId)
// 			},
// 		},
// 		{
// 			name:  "premium franchise search route",
// 			input: createInput("premium", "/franchise/search", "", 0),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "franchise-search-premium",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, "franchise-search-premium", output.SelectedTemplateId)
// 			},
// 		},
// 		{
// 			name:  "free franchise search route",
// 			input: createInput("free", "/franchise/search", "", 0),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "franchise-search-free",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, "franchise-search-free", output.SelectedTemplateId)
// 			},
// 		},
// 		{
// 			name:  "premium franchise detail route",
// 			input: createInput("premium", "/franchise/detail", "", 0),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "franchise-detail-premium",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, "franchise-detail-premium", output.SelectedTemplateId)
// 			},
// 		},
// 		{
// 			name:  "free franchise detail route",
// 			input: createInput("free", "/franchise/detail", "", 0),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "franchise-detail-free",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, "franchise-detail-free", output.SelectedTemplateId)
// 			},
// 		},
// 		{
// 			name:  "premium application submit route",
// 			input: createInput("premium", "/application/submit", "", 0),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "application-submit-premium",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, "application-submit-premium", output.SelectedTemplateId)
// 			},
// 		},
// 		{
// 			name:  "free application submit route",
// 			input: createInput("free", "/application/submit", "", 0),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "application-submit-free",
// 			},
// 			validateOutput: func(t *testing.T, output *Output) {
// 				assert.Equal(t, "application-submit-free", output.SelectedTemplateId)
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			handler := createTestHandler(t, nil)
// 			output, err := handler.execute(context.Background(), tt.input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.Equal(t, tt.expectedOutput.SelectedTemplateId, output.SelectedTemplateId)

// 			if tt.validateOutput != nil {
// 				tt.validateOutput(t, output)
// 			}
// 		})
// 	}
// }

// func TestHandler_Execute_FallbackScenarios(t *testing.T) {
// 	tests := []struct {
// 		name           string
// 		config         *Config
// 		input          *Input
// 		expectedOutput *Output
// 	}{
// 		{
// 			name: "route fallback when specific tier not found",
// 			config: &Config{
// 				TemplateRules: map[string]map[string]string{
// 					"route": {
// 						"/franchise/search:fallback": "franchise-search-fallback",
// 					},
// 				},
// 			},
// 			input: createInput("premium", "/franchise/search", "", 0),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "franchise-search-fallback",
// 			},
// 		},
// 		{
// 			name: "default fallback when no rules match",
// 			config: &Config{
// 				TemplateRules: map[string]map[string]string{
// 					"route": {
// 						"other-route:premium": "other-template",
// 					},
// 				},
// 			},
// 			input: createInput("premium", "/unknown/route", "", 0),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "default-template",
// 			},
// 		},
// 		{
// 			name: "default fallback when no route provided",
// 			config: &Config{
// 				TemplateRules: map[string]map[string]string{
// 					"route": {
// 						"/some/route:premium": "some-template",
// 					},
// 				},
// 			},
// 			input: createInput("premium", "", "", 0),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "default-template",
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			handler := createTestHandler(t, tt.config)
// 			output, err := handler.execute(context.Background(), tt.input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.Equal(t, tt.expectedOutput.SelectedTemplateId, output.SelectedTemplateId)
// 		})
// 	}
// }

// func TestHandler_Execute_AIResponseEdgeCases(t *testing.T) {
// 	tests := []struct {
// 		name           string
// 		input          *Input
// 		expectedOutput *Output
// 	}{
// 		{
// 			name:  "AI response with exact threshold confidence",
// 			input: createInput("premium", "", "ai-response", 0.8),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "ai-detailed",
// 			},
// 		},
// 		{
// 			name:  "AI response with very low confidence",
// 			input: createInput("premium", "", "ai-response", 0.1),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "ai-tentative",
// 			},
// 		},
// 		{
// 			name:  "AI response with zero confidence",
// 			input: createInput("premium", "", "ai-response", 0.0),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "ai-tentative",
// 			},
// 		},
// 		{
// 			name:  "AI response with negative confidence",
// 			input: createInput("premium", "", "ai-response", -0.5),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "ai-tentative",
// 			},
// 		},
// 		{
// 			name:  "AI response with confidence above 1",
// 			input: createInput("premium", "", "ai-response", 1.5),
// 			expectedOutput: &Output{
// 				SelectedTemplateId: "ai-detailed",
// 			},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			handler := createTestHandler(t, nil)
// 			output, err := handler.execute(context.Background(), tt.input)

// 			assert.NoError(t, err)
// 			assert.NotNil(t, output)
// 			assert.Equal(t, tt.expectedOutput.SelectedTemplateId, output.SelectedTemplateId)
// 		})
// 	}
// }

// func TestHandler_Execute_ErrorCases(t *testing.T) {
// 	t.Run("missing route template rules in config", func(t *testing.T) {
// 		config := &Config{
// 			TemplateRules: map[string]map[string]string{
// 				// No "route" key
// 				"other": {
// 					"key": "value",
// 				},
// 			},
// 		}
// 		handler := createTestHandler(t, config)
// 		input := createInput("premium", "/franchise/search", "", 0)

// 		output, err := handler.execute(context.Background(), input)

// 		assert.Error(t, err)
// 		assert.Contains(t, err.Error(), "missing route template rules in config")
// 		assert.Nil(t, output)
// 	})
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_AIResponseSelection(t *testing.T) {
// 	handler := createTestHandler(t, nil)

// 	tests := []struct {
// 		name       string
// 		confidence float64
// 		expected   string
// 	}{
// 		{
// 			name:       "high confidence selects detailed",
// 			confidence: 0.81,
// 			expected:   "ai-detailed",
// 		},
// 		{
// 			name:       "threshold confidence selects detailed",
// 			confidence: 0.8,
// 			expected:   "ai-detailed",
// 		},
// 		{
// 			name:       "medium confidence selects tentative",
// 			confidence: 0.79,
// 			expected:   "ai-tentative",
// 		},
// 		{
// 			name:       "low confidence selects tentative",
// 			confidence: 0.1,
// 			expected:   "ai-tentative",
// 		},
// 		{
// 			name:       "zero confidence selects tentative",
// 			confidence: 0.0,
// 			expected:   "ai-tentative",
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			input := createInput("premium", "", "ai-response", tt.confidence)
// 			output, err := handler.execute(context.Background(), input)

// 			assert.NoError(t, err)
// 			assert.Equal(t, tt.expected, output.SelectedTemplateId)
// 		})
// 	}
// }

// func TestHandler_RouteBasedSelection(t *testing.T) {
// 	handler := createTestHandler(t, nil)

// 	tests := []struct {
// 		name             string
// 		routePath        string
// 		subscriptionTier string
// 		expected         string
// 	}{
// 		{
// 			name:             "premium franchise search",
// 			routePath:        "/franchise/search",
// 			subscriptionTier: "premium",
// 			expected:         "franchise-search-premium",
// 		},
// 		{
// 			name:             "free franchise search",
// 			routePath:        "/franchise/search",
// 			subscriptionTier: "free",
// 			expected:         "franchise-search-free",
// 		},
// 		{
// 			name:             "premium franchise detail",
// 			routePath:        "/franchise/detail",
// 			subscriptionTier: "premium",
// 			expected:         "franchise-detail-premium",
// 		},
// 		{
// 			name:             "free franchise detail",
// 			routePath:        "/franchise/detail",
// 			subscriptionTier: "free",
// 			expected:         "franchise-detail-free",
// 		},
// 		{
// 			name:             "premium application submit",
// 			routePath:        "/application/submit",
// 			subscriptionTier: "premium",
// 			expected:         "application-submit-premium",
// 		},
// 		{
// 			name:             "free application submit",
// 			routePath:        "/application/submit",
// 			subscriptionTier: "free",
// 			expected:         "application-submit-free",
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			input := createInput(tt.subscriptionTier, tt.routePath, "", 0)
// 			output, err := handler.execute(context.Background(), input)

// 			assert.NoError(t, err)
// 			assert.Equal(t, tt.expected, output.SelectedTemplateId)
// 		})
// 	}
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_EdgeCases(t *testing.T) {
// 	handler := createTestHandler(t, nil)

// 	t.Run("empty subscription tier", func(t *testing.T) {
// 		input := &Input{
// 			SubscriptionTier: "",
// 			RoutePath:        "/franchise/search",
// 		}

// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		// Should use fallback logic since empty tier won't match premium/free
// 		assert.Equal(t, "franchise-search-fallback", output.SelectedTemplateId)
// 	})

// 	t.Run("unknown subscription tier", func(t *testing.T) {
// 		input := createInput("enterprise", "/franchise/search", "", 0)

// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		assert.Equal(t, "franchise-search-fallback", output.SelectedTemplateId)
// 	})

// 	t.Run("unknown route path", func(t *testing.T) {
// 		input := createInput("premium", "/unknown/path", "", 0)

// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		assert.Equal(t, "default-template", output.SelectedTemplateId)
// 	})

// 	t.Run("AI response with empty template type", func(t *testing.T) {
// 		input := createInput("premium", "", "", 0.9)

// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		// Should use route-based selection (which falls back to default)
// 		assert.Equal(t, "default-template", output.SelectedTemplateId)
// 	})

// 	t.Run("route path takes precedence over AI response", func(t *testing.T) {
// 		input := createInput("premium", "/franchise/search", "ai-response", 0.9)

// 		output, err := handler.execute(context.Background(), input)
// 		assert.NoError(t, err)
// 		// Route-based selection should take precedence
// 		assert.Equal(t, "franchise-search-premium", output.SelectedTemplateId)
// 	})
// }

// func TestHandler_ConfigEdgeCases(t *testing.T) {
// 	t.Run("empty config rules", func(t *testing.T) {
// 		config := &Config{
// 			TemplateRules: map[string]map[string]string{},
// 		}
// 		handler := createTestHandler(t, config)
// 		input := createInput("premium", "/franchise/search", "", 0)

// 		output, err := handler.execute(context.Background(), input)

// 		assert.Error(t, err)
// 		assert.Contains(t, err.Error(), "missing route template rules in config")
// 		assert.Nil(t, output)
// 	})

// 	t.Run("nil config rules", func(t *testing.T) {
// 		config := &Config{
// 			TemplateRules: nil,
// 		}
// 		handler := createTestHandler(t, config)
// 		input := createInput("premium", "/franchise/search", "", 0)

// 		output, err := handler.execute(context.Background(), input)

// 		assert.Error(t, err)
// 		assert.Contains(t, err.Error(), "missing route template rules in config")
// 		assert.Nil(t, output)
// 	})

// 	t.Run("partial route rules", func(t *testing.T) {
// 		config := &Config{
// 			TemplateRules: map[string]map[string]string{
// 				"route": {
// 					"/franchise/search:premium": "search-premium",
// 					// Missing free and fallback
// 				},
// 			},
// 		}
// 		handler := createTestHandler(t, config)

// 		t.Run("premium tier works", func(t *testing.T) {
// 			input := createInput("premium", "/franchise/search", "", 0)
// 			output, err := handler.execute(context.Background(), input)
// 			assert.NoError(t, err)
// 			assert.Equal(t, "search-premium", output.SelectedTemplateId)
// 		})

// 		t.Run("free tier falls back to default", func(t *testing.T) {
// 			input := createInput("free", "/franchise/search", "", 0)
// 			output, err := handler.execute(context.Background(), input)
// 			assert.NoError(t, err)
// 			assert.Equal(t, "default-template", output.SelectedTemplateId)
// 		})
// 	})
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	config := &Config{
// 		TemplateRules: map[string]map[string]string{
// 			"route": {
// 				"/franchise/search:premium":  "search-premium-template",
// 				"/franchise/search:free":     "search-free-template",
// 				"/franchise/search:fallback": "search-fallback-template",
// 				"/franchise/detail:premium":  "detail-premium-template",
// 				"/franchise/detail:free":     "detail-free-template",
// 				"/franchise/detail:fallback": "detail-fallback-template",
// 			},
// 		},
// 	}
// 	handler := createTestHandler(t, config)

// 	tests := []struct {
// 		name        string
// 		input       *Input
// 		description string
// 	}{
// 		{
// 			name:        "AI workflow with high confidence",
// 			input:       createInput("premium", "", "ai-response", 0.95),
// 			description: "Should select AI detailed template for high confidence",
// 		},
// 		{
// 			name:        "AI workflow with medium confidence",
// 			input:       createInput("free", "", "ai-response", 0.6),
// 			description: "Should select AI tentative template for medium confidence",
// 		},
// 		{
// 			name:        "Premium franchise search workflow",
// 			input:       createInput("premium", "/franchise/search", "", 0),
// 			description: "Should select premium search template",
// 		},
// 		{
// 			name:        "Free franchise search workflow",
// 			input:       createInput("free", "/franchise/search", "", 0),
// 			description: "Should select free search template",
// 		},
// 		{
// 			name:        "Premium franchise detail workflow",
// 			input:       createInput("premium", "/franchise/detail", "", 0),
// 			description: "Should select premium detail template",
// 		},
// 		{
// 			name:        "Free franchise detail workflow",
// 			input:       createInput("free", "/franchise/detail", "", 0),
// 			description: "Should select free detail template",
// 		},
// 		{
// 			name:        "Fallback workflow for unknown route",
// 			input:       createInput("premium", "/unknown/route", "", 0),
// 			description: "Should select default template for unknown route",
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			output, err := handler.execute(context.Background(), tt.input)

// 			assert.NoError(t, err, tt.description)
// 			assert.NotNil(t, output, tt.description)
// 			assert.NotEmpty(t, output.SelectedTemplateId, tt.description)

// 			// Verify the selection logic worked correctly
// 			switch {
// 			case tt.input.TemplateType == "ai-response" && tt.input.Confidence > 0:
// 				if tt.input.Confidence > 0.8 {
// 					assert.Equal(t, "ai-detailed", output.SelectedTemplateId, tt.description)
// 				} else {
// 					assert.Equal(t, "ai-tentative", output.SelectedTemplateId, tt.description)
// 				}
// 			case tt.input.RoutePath == "/franchise/search":
// 				switch tt.input.SubscriptionTier {
// 				case "premium":
// 					assert.Equal(t, "search-premium-template", output.SelectedTemplateId, tt.description)
// 				case "free":
// 					assert.Equal(t, "search-free-template", output.SelectedTemplateId, tt.description)
// 				}
// 			case tt.input.RoutePath == "/franchise/detail":
// 				switch tt.input.SubscriptionTier {
// 				case "premium":
// 					assert.Equal(t, "detail-premium-template", output.SelectedTemplateId, tt.description)
// 				case "free":
// 					assert.Equal(t, "detail-free-template", output.SelectedTemplateId, tt.description)
// 				}
// 			default:
// 				assert.Equal(t, "default-template", output.SelectedTemplateId, tt.description)
// 			}
// 		})
// 	}
// }

// // ==========================
// // JSON Serialization Tests
// // ==========================

// func TestHandler_JSONSerialization(t *testing.T) {
// 	output := &Output{
// 		SelectedTemplateId: "test-template-123",
// 	}

// 	jsonData, err := json.Marshal(output)
// 	assert.NoError(t, err)

// 	var decoded Output
// 	err = json.Unmarshal(jsonData, &decoded)
// 	assert.NoError(t, err)
// 	assert.Equal(t, output.SelectedTemplateId, decoded.SelectedTemplateId)
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	config := createTestConfig()
// 	handler := NewHandler(config, zaptest.NewLogger(b))

// 	inputs := []*Input{
// 		createInput("premium", "/franchise/search", "", 0),
// 		createInput("free", "/franchise/detail", "", 0),
// 		createInput("premium", "", "ai-response", 0.9),
// 		createInput("free", "", "ai-response", 0.7),
// 		createInput("premium", "/application/submit", "", 0),
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), inputs[i%len(inputs)])
// 	}
// }

// func BenchmarkHandler_AIResponseSelection(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	inputs := []*Input{
// 		createInput("premium", "", "ai-response", 0.95),
// 		createInput("premium", "", "ai-response", 0.75),
// 		createInput("premium", "", "ai-response", 0.5),
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), inputs[i%len(inputs)])
// 	}
// }

// func BenchmarkHandler_RouteBasedSelection(b *testing.B) {
// 	handler := NewHandler(createTestConfig(), zaptest.NewLogger(b))

// 	inputs := []*Input{
// 		createInput("premium", "/franchise/search", "", 0),
// 		createInput("free", "/franchise/search", "", 0),
// 		createInput("premium", "/franchise/detail", "", 0),
// 		createInput("free", "/franchise/detail", "", 0),
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		handler.execute(context.Background(), inputs[i%len(inputs)])
// 	}
// }

// // internal/workers/infrastructure/select-template/handler_test.go
// package selecttemplate

// import (
// 	"context"
// 	"testing"

// 	"github.com/stretchr/testify/assert"
// 	"go.uber.org/zap"
// )

// func TestHandler_execute_RouteDetailPremium(t *testing.T) {
// 	config := &Config{
// 		TemplateRules: map[string]map[string]string{
// 			"route": {
// 				"/franchises/detail:premium": "franchise-detail-premium",
// 				"/franchises/detail:free":    "franchise-detail-basic",
// 			},
// 		},
// 	}
// 	handler := NewHandler(config, zap.NewNop())

// 	input := &Input{
// 		SubscriptionTier: "premium",
// 		RoutePath:        "/franchises/detail",
// 	}

// 	output, err := handler.execute(context.Background(), input)
// 	assert.NoError(t, err)
// 	assert.Equal(t, "franchise-detail-premium", output.SelectedTemplateId)
// }

// func TestHandler_execute_RouteDetailFree(t *testing.T) {
// 	config := &Config{
// 		TemplateRules: map[string]map[string]string{
// 			"route": {
// 				"/franchises/detail:premium": "franchise-detail-premium",
// 				"/franchises/detail:free":    "franchise-detail-basic",
// 			},
// 		},
// 	}
// 	handler := NewHandler(config, zap.NewNop())

// 	input := &Input{
// 		SubscriptionTier: "free",
// 		RoutePath:        "/franchises/detail",
// 	}

// 	output, err := handler.execute(context.Background(), input)
// 	assert.NoError(t, err)
// 	assert.Equal(t, "franchise-detail-basic", output.SelectedTemplateId)
// }

// func TestHandler_execute_AIHighConfidence(t *testing.T) {
// 	config := &Config{}
// 	handler := NewHandler(config, zap.NewNop())

// 	input := &Input{
// 		TemplateType: "ai-response",
// 		Confidence:   0.9,
// 	}

// 	output, err := handler.execute(context.Background(), input)
// 	assert.NoError(t, err)
// 	assert.Equal(t, "ai-detailed", output.SelectedTemplateId)
// }

// func TestHandler_execute_AILowConfidence(t *testing.T) {
// 	config := &Config{}
// 	handler := NewHandler(config, zap.NewNop())

// 	input := &Input{
// 		TemplateType: "ai-response",
// 		Confidence:   0.5,
// 	}

// 	output, err := handler.execute(context.Background(), input)
// 	assert.NoError(t, err)
// 	assert.Equal(t, "ai-tentative", output.SelectedTemplateId)
// }

// func TestHandler_execute_UnknownRoute(t *testing.T) {
// 	config := &Config{
// 		TemplateRules: map[string]map[string]string{
// 			"route": {
// 				"/unknown:premium": "some-template",
// 			},
// 		},
// 	}
// 	handler := NewHandler(config, zap.NewNop())

// 	input := &Input{
// 		SubscriptionTier: "premium",
// 		RoutePath:        "/franchises/unknown",
// 	}

// 	output, err := handler.execute(context.Background(), input)
// 	assert.NoError(t, err)
// 	assert.Equal(t, "default-template", output.SelectedTemplateId)
// }
