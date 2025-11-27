// cmd/tools/worker-generator/main.go
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"camunda-workers/pkg/registry"
)

// WorkerData holds data for templates
type WorkerData struct {
	Name                 string                 `json:"name"`
	PackageName          string                 `json:"packageName"`
	TaskType             string                 `json:"taskType"`
	InputSchema          map[string]interface{} `json:"inputSchema"`
	OutputSchema         map[string]interface{} `json:"outputSchema"`
	ErrorCodes           []string               `json:"errorCodes"`
	Description          string                 `json:"description"`
	Category             string                 `json:"category"`
	Timeout              string                 `json:"timeout"`
	Retries              int                    `json:"retries"`
	ImplementationStatus string                 `json:"implementationStatus"`
}

// parseSchema extracts properties from a JSON schema object
func parseSchema(schemaObj interface{}) map[string]interface{} {
	if schemaMap, ok := schemaObj.(map[string]interface{}); ok {
		if props, exists := schemaMap["properties"]; exists {
			if properties, ok := props.(map[string]interface{}); ok {
				return properties
			}
		}
	}
	return map[string]interface{}{}
}

// goTypeFromJSONType maps JSON schema types to Go types
func goTypeFromJSONType(jsonType interface{}, jsonFormat interface{}) string {
	if jt, ok := jsonType.(string); ok {
		switch jt {
		case "string":
			if jf, ok := jsonFormat.(string); ok && jf == "date-time" {
				return "string"
			}
			return "string"
		case "number", "integer":
			return "float64"
		case "boolean":
			return "bool"
		case "object":
			return "map[string]interface{}"
		case "array":
			return "[]interface{}"
		default:
			return "interface{}"
		}
	}
	return "interface{}"
}

// jsonTagFromProperty creates a JSON tag for a property
func jsonTagFromProperty(propName string) string {
	return fmt.Sprintf("`json:\"%s\"`", propName)
}

// generateStructFields generates Go struct field definitions from schema properties
func generateStructFields(properties map[string]interface{}) string {
	var fields []string
	for prop, details := range properties {
		propDetails, ok := details.(map[string]interface{})
		if !ok {
			continue
		}
		goType := goTypeFromJSONType(propDetails["type"], propDetails["format"])
		jsonTag := jsonTagFromProperty(prop)

		comment := ""
		if desc, exists := propDetails["description"]; exists {
			if d, ok := desc.(string); ok && d != "" {
				comment = fmt.Sprintf(" // %s", d)
			}
		}

		fieldDef := fmt.Sprintf("\t%s %s %s%s", upperFirst(prop), goType, jsonTag, comment)
		fields = append(fields, fieldDef)
	}
	return strings.Join(fields, "\n")
}

// upperFirst makes the first character uppercase
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

const handlerTemplate = `package {{ .PackageName }}

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"camunda-workers/internal/common/errors"
	"camunda-workers/internal/common/logger"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const TaskType = "{{ .TaskType }}"

// Handler implements the job handling logic for the {{ .Name }} worker.
type Handler struct {
	service *Service
	config  *Config
	logger  logger.Logger
}

// NewHandler creates a new instance of the handler.
func NewHandler(config *Config, logger logger.Logger) *Handler {
	service := NewService(config)
	return &Handler{
		service: service,
		config:  config,
		logger:  logger,
	}
}

// Handle processes a single Zeebe job for the {{ .Name }} task.
func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	ctx := context.Background()
	startTime := time.Now()

	h.logger.Info("Processing job",
		"taskType", TaskType,
		"jobKey", job.Key,
		"processInstanceKey", job.ProcessInstanceKey,
	)

	// Parse input variables
	var input Input
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		h.failJob(client, job, "INVALID_INPUT", "Failed to parse input variables: " + err.Error())
		return
	}

	// Validate input
	if err := input.Validate(); err != nil {
		h.failJob(client, job, "VALIDATION_FAILED", "Input validation failed: " + err.Error())
		return
	}

	// Execute business logic
	output, err := h.service.Execute(ctx, &input)
	if err != nil {
		h.failJob(client, job, "EXECUTION_FAILED", "Service execution failed: " + err.Error())
		return
	}

	// Complete job with output
	h.completeJob(client, job, output)

	duration := time.Since(startTime)
	h.logger.Info("Job completed successfully",
		"taskType", TaskType,
		"jobKey", job.Key,
		"duration", duration.Milliseconds(),
	)
}

// completeJob sends a completion command to Zeebe with the output variables.
func (h *Handler) completeJob(client worker.JobClient, job entities.Job, output *Output) {
	variables, err := json.Marshal(output)
	if err != nil {
		h.logger.Error("Failed to marshal output", "error", err)
		h.failJob(client, job, "MARSHAL_ERROR", "Failed to marshal output: " + err.Error())
		return
	}

	request := client.NewCompleteJobCommand().
		JobKey(job.Key).
		VariablesFromString(string(variables))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = request.Send(ctx)
	if err != nil {
		h.logger.Error("Failed to complete job",
			"jobKey", job.Key,
			"error", err,
		)
		return
	}

	h.logger.Info("Job completed successfully",
		"jobKey", job.Key,
		"processInstanceKey", job.ProcessInstanceKey,
	)
}

// failJob sends a failure command to Zeebe with error information.
func (h *Handler) failJob(client worker.JobClient, job entities.Job, errorCode string, errorMessage string) {
	retries := int32(0)
	
	request := client.NewFailJobCommand().
		JobKey(job.Key).
		Retries(retries).
		ErrorMessage(errorMessage)

	if errorCode != "" {
		request = request.ErrorCode(errorCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, sendErr := request.Send(ctx)
	if sendErr != nil {
		h.logger.Error("Failed to send fail command",
			"jobKey", job.Key,
			"error", sendErr,
		)
	}

	h.logger.Error("Job failed",
		"jobKey", job.Key,
		"errorCode", errorCode,
		"errorMessage", errorMessage,
		"retries", retries,
	)
}
`

const serviceTemplate = `package {{ .PackageName }}

import (
	"context"
)

// Service contains the business logic for the {{ .Name }} worker.
type Service struct {
	config *Config
	// Add other dependencies like db clients, API clients here
}

// NewService creates a new instance of the service.
func NewService(config *Config) *Service {
	return &Service{
		config: config,
		// Initialize other dependencies here
	}
}

// Execute performs the core business logic of the worker.
func (s *Service) Execute(ctx context.Context, input *Input) (*Output, error) {
	// TODO: Implement the business logic for '{{ .Name }}'.
	
	// Example validation:
	// if input.SomeField == "" {
	// 	return nil, fmt.Errorf("SomeField is required")
	// }

	// Perform actual work here...

	// Example output
	output := &Output{
		// Set output fields based on business logic
	}

	return output, nil
}
`

const configTemplate = `package {{ .PackageName }}

import "time"

// Config holds configuration specific to the {{ .Name }} worker.
type Config struct {
	Timeout time.Duration
	// Add other worker-specific config fields here
}
`

const modelsTemplate = `package {{ .PackageName }}

// Input represents the input variables for the '{{ .Name }}' worker.
type Input struct {
{{- $inputProps := parseSchema .InputSchema }}
{{- if $inputProps }}
{{ generateStructFields $inputProps }}
{{- else }}
	// TODO: Add input fields based on the BPMN task requirements
{{- end }}
}

// Output represents the output variables for the '{{ .Name }}' worker.
type Output struct {
{{- $outputProps := parseSchema .OutputSchema }}
{{- if $outputProps }}
{{ generateStructFields $outputProps }}
{{- else }}
	// TODO: Add output fields based on the BPMN task requirements
{{- end }}
}
`

const testTemplate = `package {{ .PackageName }}

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestService_Execute(t *testing.T) {
	tests := []struct {
		name    string
		input   *Input
		wantErr bool
	}{
		{
			name: "valid input",
			input: &Input{
				// Set test input fields
			},
			wantErr: false,
		},
		// Add more test cases
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				Timeout: 10 * time.Second,
			}
			service := NewService(config)

			got, err := service.Execute(context.Background(), tt.input)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, got)
				// Add more assertions based on expected output
			}
		})
	}
}

func TestInput_Validate(t *testing.T) {
	tests := []struct {
		name    string
		input   *Input
		wantErr bool
	}{
		{
			name: "valid input",
			input: &Input{
				// Set valid input fields
			},
			wantErr: false,
		},
		// Add more test cases
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
`

const readmeTemplate = `# {{ .Name }} Worker

## Description
{{ .Description }}

## Category
{{ .Category }}

## Task Type
{{ .TaskType }}

## Implementation Status
{{ .ImplementationStatus }}

## Configuration
- **Timeout**: {{ .Timeout }}
- **Retries**: {{ .Retries }}

## Input Schema
{{- $inputProps := parseSchema .InputSchema }}
{{- if $inputProps }}
The worker expects the following input variables:

{{ range $prop, $details := $inputProps }}
- **{{ $prop }}** ({{ goTypeFromJSONType (index $details "type") (index $details "format") }}){{ if index $details "description" }}: {{ index $details "description" }}{{ end }}
{{ end }}
{{- else }}
No input schema defined in registry.
{{- end }}

## Output Schema
{{- $outputProps := parseSchema .OutputSchema }}
{{- if $outputProps }}
The worker produces the following output variables:

{{ range $prop, $details := $outputProps }}
- **{{ $prop }}** ({{ goTypeFromJSONType (index $details "type") (index $details "format") }}){{ if index $details "description" }}: {{ index $details "description" }}{{ end }}
{{ end }}
{{- else }}
No output schema defined in registry.
{{- end }}

## Error Codes
{{- if .ErrorCodes }}
{{ range .ErrorCodes }}
- {{ . }}
{{ end }}
{{- else }}
No specific error codes defined.
{{- end }}

## Usage

### Register in Worker Manager

` + "```go" + `
import "camunda-workers/internal/workers/{{ .Category }}/{{ .PackageName }}"

// In registerWorkers function:
handler := {{ .PackageName }}.NewHandler(
    &config.Workers.{{ upperFirst .PackageName }},
    logger,
)

client.NewJobWorker().
    JobType({{ .PackageName }}.TaskType).
    Handler(handler.Handle).
    MaxJobsActive(config.Workers.{{ upperFirst .PackageName }}.MaxJobsActive).
    Timeout(config.Workers.{{ upperFirst .PackageName }}.Timeout).
    Name("{{ .Name }}-worker").
    Open()
` + "```" + `

### Configuration in config.yaml

` + "```yaml" + `
workers:
  {{ .PackageName }}:
    enabled: true
    max_jobs_active: 5
    timeout: {{ .Timeout }}
    # Add worker-specific configuration here
` + "```" + `

## Development

### Run Tests
` + "```bash" + `
go test ./internal/workers/{{ .Category }}/{{ .PackageName }}/...
` + "```" + `
`

const validationTemplate = `package {{ .PackageName }}

// Validate validates the input data.
func (i *Input) Validate() error {
	// TODO: Add validation logic for input fields
	
	// Example:
	// if i.SomeField == "" {
	// 	return fmt.Errorf("SomeField is required")
	// }
	
	return nil
}
`

func main() {
	activity := flag.String("activity", "", "Activity ID from registry (e.g., validate-subscription)")
	outputDir := flag.String("output", "./internal/workers/", "Output directory for the generated worker")
	registryPath := flag.String("registry", "configs/activity-registry.json", "Path to the activity registry JSON file")
	flag.Parse()

	if *activity == "" {
		fmt.Println("Usage: worker-generator --activity <id> --output <dir> [--registry <path>]")
		fmt.Println("\nExample:")
		fmt.Println("  go run cmd/tools/worker-generator/main.go --activity validate-subscription")
		os.Exit(1)
	}

	// Load the registry
	reg, err := registry.LoadRegistry(*registryPath)
	if err != nil {
		fmt.Printf("Error loading registry from %s: %v\n", *registryPath, err)
		os.Exit(1)
	}

	// Find the activity in the registry
	var foundActivity *registry.Activity
	for _, act := range reg.Activities {
		if act.ID == *activity {
			foundActivity = &act
			break
		}
	}

	if foundActivity == nil {
		fmt.Printf("Activity '%s' not found in registry %s\n", *activity, *registryPath)
		os.Exit(1)
	}

	// Prepare data for templates
	data := WorkerData{
		Name:                 foundActivity.DisplayName,
		PackageName:          strings.ReplaceAll(foundActivity.ID, "-", ""),
		TaskType:             foundActivity.TaskType,
		InputSchema:          foundActivity.InputSchema,
		OutputSchema:         foundActivity.OutputSchema,
		ErrorCodes:           foundActivity.ErrorCodes,
		Description:          foundActivity.Description,
		Category:             foundActivity.Category,
		Timeout:              foundActivity.Timeout,
		Retries:              foundActivity.Retries,
		ImplementationStatus: foundActivity.ImplementationStatus,
	}

	// Map category to directory structure
	categoryDir := mapCategoryToDirectory(data.Category)
	workerDir := filepath.Join(*outputDir, categoryDir, foundActivity.ID)

	if err := os.MkdirAll(workerDir, 0755); err != nil {
		fmt.Printf("Error creating directory: %v\n", err)
		os.Exit(1)
	}

	// Create templates with functions
	funcMap := template.FuncMap{
		"parseSchema":          parseSchema,
		"goTypeFromJSONType":   goTypeFromJSONType,
		"generateStructFields": generateStructFields,
		"upperFirst":           upperFirst,
		"index": func(m map[string]interface{}, key string) interface{} {
			if val, exists := m[key]; exists {
				return val
			}
			return nil
		},
	}

	// Generate files
	templates := map[string]string{
		"handler.go":      handlerTemplate,
		"service.go":      serviceTemplate,
		"config.go":       configTemplate,
		"models.go":       modelsTemplate,
		"handler_test.go": testTemplate,
		"validation.go":   validationTemplate,
		"README.md":       readmeTemplate,
	}

	for filename, tmplStr := range templates {
		tmpl, err := template.New(filename).Funcs(funcMap).Parse(tmplStr)
		if err != nil {
			fmt.Printf("Error parsing template %s: %v\n", filename, err)
			continue
		}

		filePath := filepath.Join(workerDir, filename)
		file, err := os.Create(filePath)
		if err != nil {
			fmt.Printf("Error creating file %s: %v\n", filePath, err)
			continue
		}

		if err := tmpl.Execute(file, data); err != nil {
			fmt.Printf("Error executing template for %s: %v\n", filename, err)
		}
		file.Close()

		fmt.Printf("✓ Generated %s\n", filePath)
	}

	fmt.Printf("\n✅ Worker scaffold generated successfully at: %s\n", workerDir)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Implement business logic in service.go\n")
	fmt.Printf("  2. Add validation in validation.go\n")
	fmt.Printf("  3. Write tests in handler_test.go\n")
	fmt.Printf("  4. Register worker in cmd/worker-manager/main.go\n")
	fmt.Printf("  5. Add configuration to configs/config.yaml\n")
}

// mapCategoryToDirectory maps registry categories to directory names
func mapCategoryToDirectory(category string) string {
	switch category {
	case "authentication":
		return "auth"
	case "crm-integration":
		return "crm"
	case "communication":
		return "communication"
	case "infrastructure":
		return "infrastructure"
	case "data-access":
		return "data-access"
	case "business-logic":
		return "franchise"
	case "ai-ml":
		return "ai-conversation"
	case "application":
		return "application"
	default:
		return strings.ToLower(category)
	}
}
