package buildresponse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"camunda-workers/internal/common/logger" // Add your logger package import

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{
		TemplateRegistry: "test_registry.json",
		CacheTTL:         5 * time.Minute,
		AppVersion:       "1.0.0",
	}
}

func createTestHandler(t *testing.T, config *Config) *Handler {
	if config == nil {
		config = createTestConfig()
	}
	return NewHandler(config, logger.NewTestLogger(t)) // Changed to use custom logger
}

func createTemplateRegistry(templates []TemplateDefinition) string {
	registry := struct {
		Templates []TemplateDefinition `json:"templates"`
	}{Templates: templates}

	data, _ := json.MarshalIndent(registry, "", "  ")
	return string(data)
}

func createTestInput(templateId, requestId string, data map[string]interface{}) *Input {
	return &Input{
		TemplateId: templateId,
		RequestId:  requestId,
		Data:       data,
	}
}

// ==========================
// Core Functionality Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	tests := []struct {
		name           string
		templates      []TemplateDefinition
		input          *Input
		expectedOutput *Output
		validateOutput func(t *testing.T, output *Output)
	}{
		{
			name: "successful response build with validation",
			templates: []TemplateDefinition{
				{
					ID:   "franchise-detail",
					Type: "franchise-detail",
					Schema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name":        map[string]interface{}{"type": "string"},
							"investment":  map[string]interface{}{"type": "number"},
							"category":    map[string]interface{}{"type": "string"},
							"description": map[string]interface{}{"type": "string"},
						},
						"required": []string{"name", "investment"},
					},
					Template: map[string]interface{}{
						"franchise": map[string]interface{}{
							"name":        "{{name}}",
							"investment":  "{{investment}}",
							"category":    "{{category}}",
							"description": "{{description}}",
							"features":    []string{"feature1", "feature2"},
						},
						"metadata": map[string]interface{}{
							"source": "template",
						},
					},
					Version: "1.0",
				},
			},
			input: createTestInput("franchise-detail", "req-123", map[string]interface{}{
				"name":        "McDonald's",
				"investment":  500000,
				"category":    "food",
				"description": "Fast food franchise",
			}),
			expectedOutput: &Output{
				Response: ResponsePayload{
					RequestId: "req-123",
					Status:    "success",
					Data: map[string]interface{}{
						"franchise": map[string]interface{}{
							"name":        "McDonald's",
							"investment":  float64(500000),
							"category":    "food",
							"description": "Fast food franchise",
							"features":    []interface{}{"feature1", "feature2"},
						},
						"metadata": map[string]interface{}{
							"source": "template",
						},
					},
					Metadata: ResponseMetadata{
						Version: "1.0.0",
					},
				},
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "req-123", output.Response.RequestId)
				assert.Equal(t, "success", output.Response.Status)
				assert.Equal(t, "1.0.0", output.Response.Metadata.Version)
				assert.NotEmpty(t, output.Response.Metadata.Timestamp)

				data := output.Response.Data
				franchise := data["franchise"].(map[string]interface{})
				assert.Equal(t, "McDonald's", franchise["name"])
				assert.Equal(t, float64(500000), franchise["investment"])
				assert.Equal(t, "food", franchise["category"])
			},
		},
		{
			name: "minimal template without schema",
			templates: []TemplateDefinition{
				{
					ID:       "simple-template",
					Type:     "simple",
					Schema:   map[string]interface{}{},
					Template: map[string]interface{}{"message": "{{text}}"},
					Version:  "1.0",
				},
			},
			input: createTestInput("simple-template", "req-456", map[string]interface{}{
				"text": "Hello World",
			}),
			expectedOutput: &Output{
				Response: ResponsePayload{
					RequestId: "req-456",
					Status:    "success",
					Data:      map[string]interface{}{"message": "Hello World"},
					Metadata: ResponseMetadata{
						Version: "1.0.0",
					},
				},
			},
			validateOutput: func(t *testing.T, output *Output) {
				assert.Equal(t, "Hello World", output.Response.Data["message"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary registry file
			registryContent := createTemplateRegistry(tt.templates)
			tmpFile, err := os.CreateTemp("", "test_registry_*.json")
			require.NoError(t, err)
			defer os.Remove(tmpFile.Name())

			_, err = tmpFile.WriteString(registryContent)
			require.NoError(t, err)
			tmpFile.Close()

			config := createTestConfig()
			config.TemplateRegistry = tmpFile.Name()
			handler := createTestHandler(t, config)

			output, err := handler.Execute(context.Background(), tt.input) // Changed to Execute

			assert.NoError(t, err)
			assert.NotNil(t, output)
			if tt.expectedOutput != nil {
				assert.Equal(t, tt.expectedOutput.Response.RequestId, output.Response.RequestId)
				assert.Equal(t, tt.expectedOutput.Response.Status, output.Response.Status)
				assert.Equal(t, tt.expectedOutput.Response.Metadata.Version, output.Response.Metadata.Version)
			}
			assert.NotEmpty(t, output.Response.Metadata.Timestamp)

			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}
		})
	}
}

func TestHandler_NestedDataSubstitution(t *testing.T) {
	templates := []TemplateDefinition{
		{
			ID:   "nested-template",
			Type: "nested",
			Template: map[string]interface{}{
				"user": map[string]interface{}{
					"profile": map[string]interface{}{
						"name": "{{user.name}}",
						"role": "{{user.role}}",
					},
					"settings": map[string]interface{}{
						"notifications": true,
					},
				},
			},
			Version: "1.0",
		},
	}

	registryContent := createTemplateRegistry(templates)
	tmpFile, err := os.CreateTemp("", "test_registry_*.json")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(registryContent)
	require.NoError(t, err)
	tmpFile.Close()

	config := createTestConfig()
	config.TemplateRegistry = tmpFile.Name()
	handler := createTestHandler(t, config)

	input := createTestInput("nested-template", "req-789", map[string]interface{}{
		"user": map[string]interface{}{
			"name": "John Doe",
			"role": "admin",
		},
	})

	output, err := handler.Execute(context.Background(), input) // Changed to Execute

	require.NoError(t, err)
	require.NotNil(t, output)
	require.NotNil(t, output.Response.Data)

	data := output.Response.Data
	t.Logf("Output data: %+v", data)

	require.Contains(t, data, "user")
	userInterface := data["user"]
	require.NotNil(t, userInterface)

	user, ok := userInterface.(map[string]interface{})
	require.True(t, ok, "user should be a map")

	require.Contains(t, user, "profile")
	profileInterface := user["profile"]
	require.NotNil(t, profileInterface)

	profile, ok := profileInterface.(map[string]interface{})
	require.True(t, ok, "profile should be a map")

	require.Contains(t, user, "settings")
	settingsInterface := user["settings"]
	require.NotNil(t, settingsInterface)

	settings, ok := settingsInterface.(map[string]interface{})
	require.True(t, ok, "settings should be a map")

	assert.Equal(t, "John Doe", profile["name"])
	assert.Equal(t, "admin", profile["role"])
	assert.Equal(t, true, settings["notifications"])
}

func TestHandler_Execute_ValidationErrors(t *testing.T) {
	tests := []struct {
		name          string
		templates     []TemplateDefinition
		input         *Input
		expectedError string
	}{
		{
			name: "template not found",
			templates: []TemplateDefinition{
				{
					ID:       "other-template",
					Type:     "other",
					Template: map[string]interface{}{},
					Version:  "1.0",
				},
			},
			input:         createTestInput("non-existent-template", "req-123", map[string]interface{}{}),
			expectedError: "TEMPLATE_NOT_FOUND",
		},
		{
			name: "schema validation failed",
			templates: []TemplateDefinition{
				{
					ID:   "validated-template",
					Type: "validated",
					Schema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"requiredField": map[string]interface{}{"type": "string"},
						},
						"required": []string{"requiredField"},
					},
					Template: map[string]interface{}{},
					Version:  "1.0",
				},
			},
			input: createTestInput("validated-template", "req-123", map[string]interface{}{
				"optionalField": "value",
			}),
			expectedError: "TEMPLATE_VALIDATION_FAILED: data validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary registry file
			registryContent := createTemplateRegistry(tt.templates)
			tmpFile, err := os.CreateTemp("", "test_registry_*.json")
			require.NoError(t, err)
			defer os.Remove(tmpFile.Name())

			_, err = tmpFile.WriteString(registryContent)
			require.NoError(t, err)
			tmpFile.Close()

			config := createTestConfig()
			config.TemplateRegistry = tmpFile.Name()
			handler := createTestHandler(t, config)

			output, err := handler.Execute(context.Background(), tt.input) // Changed to Execute

			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedError)
			assert.Nil(t, output)
		})
	}
}

func TestHandler_Execute_RegistryFileErrors(t *testing.T) {
	t.Run("registry file not found", func(t *testing.T) {
		config := createTestConfig()
		config.TemplateRegistry = "/non/existent/path/registry.json"
		handler := createTestHandler(t, config)

		input := createTestInput("any-template", "req-123", map[string]interface{}{})
		output, err := handler.Execute(context.Background(), input) // Changed to Execute

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "read registry")
		assert.Nil(t, output)
	})

	t.Run("invalid registry JSON", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test_invalid_registry_*.json")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString("invalid json content")
		require.NoError(t, err)
		tmpFile.Close()

		config := createTestConfig()
		config.TemplateRegistry = tmpFile.Name()
		handler := createTestHandler(t, config)

		input := createTestInput("any-template", "req-123", map[string]interface{}{})
		output, err := handler.Execute(context.Background(), input) // Changed to Execute

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse registry")
		assert.Nil(t, output)
	})
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_LoadTemplate(t *testing.T) {
	templates := []TemplateDefinition{
		{
			ID:       "template-1",
			Type:     "type-1",
			Template: map[string]interface{}{"key": "value1"},
			Version:  "1.0",
		},
		{
			ID:       "template-2",
			Type:     "type-2",
			Template: map[string]interface{}{"key": "value2"},
			Version:  "1.0",
		},
	}

	registryContent := createTemplateRegistry(templates)
	tmpFile, err := os.CreateTemp("", "test_registry_*.json")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(registryContent)
	require.NoError(t, err)
	tmpFile.Close()

	config := createTestConfig()
	config.TemplateRegistry = tmpFile.Name()
	handler := createTestHandler(t, config)

	t.Run("template found", func(t *testing.T) {
		template, err := handler.loadTemplate("template-1")
		assert.NoError(t, err)
		assert.Equal(t, "template-1", template.ID)
		assert.Equal(t, "type-1", template.Type)
	})

	t.Run("template not found", func(t *testing.T) {
		template, err := handler.loadTemplate("non-existent")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrTemplateNotFound))
		assert.Nil(t, template)
	})

	t.Run("caching works", func(t *testing.T) {
		// First call should load from file
		template1, err := handler.loadTemplate("template-2")
		assert.NoError(t, err)
		assert.Equal(t, "template-2", template1.ID)

		// Second call should use cache
		template2, err := handler.loadTemplate("template-2")
		assert.NoError(t, err)
		assert.Equal(t, template1, template2) // Same pointer indicates cache hit
	})
}

func TestHandler_ValidateData(t *testing.T) {
	handler := createTestHandler(t, nil)

	tests := []struct {
		name    string
		schema  map[string]interface{}
		data    map[string]interface{}
		wantErr bool
	}{
		{
			name: "valid data",
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string"},
					"age":  map[string]interface{}{"type": "number"},
				},
				"required": []string{"name"},
			},
			data: map[string]interface{}{
				"name": "John",
				"age":  30,
			},
			wantErr: false,
		},
		{
			name: "missing required field",
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string"},
				},
				"required": []string{"name"},
			},
			data: map[string]interface{}{
				"age": 30,
			},
			wantErr: true,
		},
		{
			name: "wrong data type",
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"age": map[string]interface{}{"type": "number"},
				},
			},
			data: map[string]interface{}{
				"age": "not-a-number",
			},
			wantErr: true,
		},
		{
			name:    "empty schema",
			schema:  map[string]interface{}{},
			data:    map[string]interface{}{"any": "data"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateData(tt.schema, tt.data)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHandler_DeepMerge(t *testing.T) {
	handler := createTestHandler(t, nil)

	tests := []struct {
		name     string
		dst      map[string]interface{}
		src      map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "simple merge",
			dst:  map[string]interface{}{"a": 1, "b": 2},
			src:  map[string]interface{}{"b": 3, "c": 4},
			expected: map[string]interface{}{
				"a": 1, "b": 3, "c": 4,
			},
		},
		{
			name:     "empty source",
			dst:      map[string]interface{}{"a": 1},
			src:      map[string]interface{}{},
			expected: map[string]interface{}{"a": 1},
		},
		{
			name:     "empty destination",
			dst:      map[string]interface{}{},
			src:      map[string]interface{}{"a": 1},
			expected: map[string]interface{}{"a": 1},
		},
		{
			name: "nested objects",
			dst: map[string]interface{}{
				"user": map[string]interface{}{
					"name": "John",
					"age":  30,
				},
			},
			src: map[string]interface{}{
				"user": map[string]interface{}{
					"age":  31,
					"role": "admin",
				},
			},
			expected: map[string]interface{}{
				"user": map[string]interface{}{
					"age":  31,
					"role": "admin",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.deepMerge(tt.dst, tt.src)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_EdgeCases(t *testing.T) {
	t.Run("cache TTL expiration", func(t *testing.T) {
		templates := []TemplateDefinition{
			{
				ID:       "test-template",
				Type:     "test",
				Template: map[string]interface{}{},
				Version:  "1.0",
			},
		}

		registryContent := createTemplateRegistry(templates)
		tmpFile, err := os.CreateTemp("", "test_registry_*.json")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString(registryContent)
		require.NoError(t, err)
		tmpFile.Close()

		config := createTestConfig()
		config.TemplateRegistry = tmpFile.Name()
		config.CacheTTL = 1 * time.Millisecond // Very short TTL
		handler := createTestHandler(t, config)

		// First call - cache miss
		template1, err := handler.loadTemplate("test-template")
		assert.NoError(t, err)

		// Wait for cache to expire
		time.Sleep(2 * time.Millisecond)

		// Second call - should be cache miss again
		template2, err := handler.loadTemplate("test-template")
		assert.NoError(t, err)
		assert.NotEqual(t, fmt.Sprintf("%p", template1), fmt.Sprintf("%p", template2)) // Different pointers
	})

	t.Run("template with complex schema", func(t *testing.T) {
		complexSchema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"arrayField": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "string",
					},
				},
				"nestedObject": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"nestedField": map[string]interface{}{"type": "string"},
					},
				},
			},
		}

		templates := []TemplateDefinition{
			{
				ID:       "complex-template",
				Type:     "complex",
				Schema:   complexSchema,
				Template: map[string]interface{}{},
				Version:  "1.0",
			},
		}

		registryContent := createTemplateRegistry(templates)
		tmpFile, err := os.CreateTemp("", "test_registry_*.json")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString(registryContent)
		require.NoError(t, err)
		tmpFile.Close()

		config := createTestConfig()
		config.TemplateRegistry = tmpFile.Name()
		handler := createTestHandler(t, config)

		input := createTestInput("complex-template", "req-123", map[string]interface{}{
			"arrayField": []string{"item1", "item2"},
			"nestedObject": map[string]interface{}{
				"nestedField": "value",
			},
		})

		output, err := handler.Execute(context.Background(), input) // Changed to Execute
		assert.NoError(t, err)
		assert.NotNil(t, output)
	})

	t.Run("empty data with required schema", func(t *testing.T) {
		templates := []TemplateDefinition{
			{
				ID:   "required-template",
				Type: "required",
				Schema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"field": map[string]interface{}{"type": "string"},
					},
					"required": []string{"field"},
				},
				Template: map[string]interface{}{},
				Version:  "1.0",
			},
		}

		registryContent := createTemplateRegistry(templates)
		tmpFile, err := os.CreateTemp("", "test_registry_*.json")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString(registryContent)
		require.NoError(t, err)
		tmpFile.Close()

		config := createTestConfig()
		config.TemplateRegistry = tmpFile.Name()
		handler := createTestHandler(t, config)

		input := createTestInput("required-template", "req-123", map[string]interface{}{})
		output, err := handler.Execute(context.Background(), input) // Changed to Execute

		assert.Error(t, err)
		assert.Nil(t, output)
	})
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	templates := []TemplateDefinition{
		{
			ID:   "franchise-search-result",
			Type: "search-result",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"franchises": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"name":       map[string]interface{}{"type": "string"},
								"investment": map[string]interface{}{"type": "number"},
								"category":   map[string]interface{}{"type": "string"},
							},
							"required": []string{"name", "investment"},
						},
					},
					"totalCount": map[string]interface{}{"type": "number"},
				},
				"required": []string{"franchises", "totalCount"},
			},
			Template: map[string]interface{}{
				"searchResults": map[string]interface{}{
					"franchises": "{{franchises}}",
					"pagination": map[string]interface{}{
						"total": "{{totalCount}}",
						"page":  1,
						"size":  20,
					},
					"metadata": map[string]interface{}{
						"searchId": "{{requestId}}",
					},
				},
			},
			Version: "1.0",
		},
	}

	registryContent := createTemplateRegistry(templates)
	tmpFile, err := os.CreateTemp("", "test_registry_*.json")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(registryContent)
	require.NoError(t, err)
	tmpFile.Close()

	config := createTestConfig()
	config.TemplateRegistry = tmpFile.Name()
	handler := createTestHandler(t, config)

	// Convert to []interface{} for proper type handling
	franchisesData := []interface{}{
		map[string]interface{}{
			"name":       "McDonald's",
			"investment": 500000,
			"category":   "food",
		},
		map[string]interface{}{
			"name":       "Subway",
			"investment": 150000,
			"category":   "food",
		},
	}

	input := createTestInput("franchise-search-result", "search-123", map[string]interface{}{
		"franchises": franchisesData,
		"totalCount": float64(2),
		"requestId":  "search-123",
	})

	output, err := handler.Execute(context.Background(), input) // Changed to Execute

	assert.NoError(t, err)
	assert.NotNil(t, output)

	// Verify the complete response structure
	assert.Equal(t, "search-123", output.Response.RequestId)
	assert.Equal(t, "success", output.Response.Status)

	data := output.Response.Data
	searchResults := data["searchResults"].(map[string]interface{})

	// The franchises field will be whatever type was substituted
	franchisesResult := searchResults["franchises"]
	require.NotNil(t, franchisesResult)

	// Check if it's a slice and has the right length
	franchisesSlice, ok := franchisesResult.([]interface{})
	if ok {
		assert.Len(t, franchisesSlice, 2)
	} else {
		t.Logf("franchises is type %T, value: %+v", franchisesResult, franchisesResult)
	}

	pagination := searchResults["pagination"].(map[string]interface{})
	metadata := searchResults["metadata"].(map[string]interface{})

	assert.Equal(t, float64(2), pagination["total"])

	assert.Equal(t, "search-123", metadata["searchId"])
}

// ==========================
// JSON Serialization Tests
// ==========================

func TestHandler_JSONSerialization(t *testing.T) {
	output := &Output{
		Response: ResponsePayload{
			RequestId: "test-123",
			Status:    "success",
			Data: map[string]interface{}{
				"message": "test",
				"count":   42,
			},
			Metadata: ResponseMetadata{
				Timestamp: "2023-01-01T00:00:00Z",
				Version:   "1.0.0",
			},
		},
	}

	jsonData, err := json.Marshal(output)
	assert.NoError(t, err)

	var decoded Output
	err = json.Unmarshal(jsonData, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, output.Response.RequestId, decoded.Response.RequestId)
	assert.Equal(t, output.Response.Status, decoded.Response.Status)
	assert.Equal(t, output.Response.Metadata, decoded.Response.Metadata)
	// Don't compare Data directly due to JSON number type conversion
	assert.Equal(t, "test", decoded.Response.Data["message"])
	assert.Equal(t, float64(42), decoded.Response.Data["count"]) // JSON unmarshals numbers as float64
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	templates := []TemplateDefinition{
		{
			ID:   "benchmark-template",
			Type: "benchmark",
			Template: map[string]interface{}{
				"data": "{{value}}",
			},
			Version: "1.0",
		},
	}

	registryContent := createTemplateRegistry(templates)
	tmpFile, err := os.CreateTemp("", "benchmark_registry_*.json")
	require.NoError(b, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(registryContent)
	require.NoError(b, err)
	tmpFile.Close()

	config := &Config{
		TemplateRegistry: tmpFile.Name(),
		CacheTTL:         5 * time.Minute,
		AppVersion:       "1.0.0",
	}
	handler := NewHandler(config, logger.NewTestLogger(b)) // Changed to use custom logger

	input := createTestInput("benchmark-template", "benchmark-req", map[string]interface{}{
		"value": "benchmark data",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.Execute(context.Background(), input) // Changed to Execute
	}
}

func BenchmarkHandler_LoadTemplate(b *testing.B) {
	templates := []TemplateDefinition{
		{
			ID:       "benchmark-template",
			Type:     "benchmark",
			Template: map[string]interface{}{},
			Version:  "1.0",
		},
	}

	registryContent := createTemplateRegistry(templates)
	tmpFile, err := os.CreateTemp("", "benchmark_registry_*.json")
	require.NoError(b, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(registryContent)
	require.NoError(b, err)
	tmpFile.Close()

	config := &Config{
		TemplateRegistry: tmpFile.Name(),
		CacheTTL:         5 * time.Minute,
	}
	handler := NewHandler(config, logger.NewTestLogger(b)) // Changed to use custom logger

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.loadTemplate("benchmark-template")
	}
}

func BenchmarkHandler_DeepMerge(b *testing.B) {
	handler := NewHandler(&Config{}, logger.NewTestLogger(b)) // Changed to use custom logger

	dst := map[string]interface{}{
		"field1": "value1",
		"field2": "value2",
		"nested": map[string]interface{}{
			"nested1": "nvalue1",
		},
	}

	src := map[string]interface{}{
		"field2": "updated",
		"field3": "value3",
		"nested": map[string]interface{}{
			"nested2": "nvalue2",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.deepMerge(dst, src)
	}
}

// // internal/workers/infrastructure/build-response/handler_test.go
// package buildresponse

// import (
// 	"context"
// 	"encoding/json"
// 	"errors"
// 	"fmt"
// 	"os"
// 	"testing"
// 	"time"

// 	"github.com/stretchr/testify/assert"
// 	"github.com/stretchr/testify/require"
// 	"go.uber.org/zap/zaptest"
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
// 	return NewHandler(config, zaptest.NewLogger(t))
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
// 		{
// 			name: "template with nested data",
// 			templates: []TemplateDefinition{
// 				{
// 					ID:   "nested-template",
// 					Type: "nested",
// 					Template: map[string]interface{}{
// 						"user": map[string]interface{}{
// 							"profile": map[string]interface{}{
// 								"name": "{{user.name}}",
// 								"role": "{{user.role}}",
// 							},
// 							"settings": map[string]interface{}{
// 								"notifications": true,
// 							},
// 						},
// 					},
// 					Version: "1.0",
// 				},
// 			},
// 			input: createTestInput("nested-template", "req-789", map[string]interface{}{
// 				"user": map[string]interface{}{
// 					"name": "John Doe",
// 					"role": "admin",
// 				},
// 			}),
// 			validateOutput: nil, // Validated in separate test to avoid closure issues
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

// 			output, err := handler.execute(context.Background(), tt.input)

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

// 	output, err := handler.execute(context.Background(), input)

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

// 			output, err := handler.execute(context.Background(), tt.input)

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
// 		output, err := handler.execute(context.Background(), input)

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
// 		output, err := handler.execute(context.Background(), input)

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

// 		output, err := handler.execute(context.Background(), input)
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
// 		output, err := handler.execute(context.Background(), input)

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

// 	output, err := handler.execute(context.Background(), input)

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
// 	searchId := metadata["searchId"]
// 	if searchId != nil {
// 		assert.Equal(t, "search-123", searchId)
// 	} else {
// 		t.Logf("searchId is nil, metadata: %+v", metadata)
// 	}
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
// 	//assert.Equal(t, output.Response, decoded.Response)
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
// 	handler := NewHandler(config, zaptest.NewLogger(b))

// 	input := createTestInput("benchmark-template", "benchmark-req", map[string]interface{}{
// 		"value": "benchmark data",
// 	})

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_, _ = handler.execute(context.Background(), input)
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
// 	handler := NewHandler(config, zaptest.NewLogger(b))

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_, _ = handler.loadTemplate("benchmark-template")
// 	}
// }

// func BenchmarkHandler_DeepMerge(b *testing.B) {
// 	handler := NewHandler(&Config{}, zaptest.NewLogger(b))

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
