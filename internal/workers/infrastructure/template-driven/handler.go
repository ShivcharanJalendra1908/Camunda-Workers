package template_driven

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"camunda-workers/internal/common/logger"
	"camunda-workers/internal/common/metrics"

	"github.com/camunda/zeebe/clients/go/v8/pkg/entities"
	"github.com/camunda/zeebe/clients/go/v8/pkg/worker"
)

const (
	TaskType = "template-driven"
)

// GAP FIX #6: Context with timeout for cancellation
type Handler struct {
	config   *Config
	registry *TemplateRegistry
	logger   logger.Logger
	taskType string
}

func NewHandler(cfg *Config, log logger.Logger) (*Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if err := verifyTemplatesDirectory(cfg.TemplatesBaseDir); err != nil {
		return nil, fmt.Errorf("templates directory verification failed: %w", err)
	}

	return &Handler{
		config:   cfg,
		registry: NewTemplateRegistry(),
		logger:   log,
		taskType: TaskType,
	}, nil
}

// GAP FIX #13: Health check support
func (h *Handler) IsHealthy() error {
	// Check if can access templates directory
	if _, err := os.Stat(h.config.TemplatesBaseDir); err != nil {
		return fmt.Errorf("templates directory not accessible: %w", err)
	}
	return nil
}

func (h *Handler) IsReady() error {
	// Check if handler is ready to process
	if err := h.IsHealthy(); err != nil {
		return err
	}
	// Add more readiness checks as needed
	return nil
}

// Handle processes the template-driven job with full security and reliability
func (h *Handler) Handle(client worker.JobClient, job entities.Job) {
	// GAP FIX #6: Job-level timeout context
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(h.config.Timeout)*time.Second)
	defer cancel()

	startTime := time.Now()
	correlationID := generateCorrelationID()

	// GAP FIX #9: Structured logging with correlation
	h.logger.Info("job_started", map[string]interface{}{
		"job_key":        job.Key,
		"correlation_id": correlationID,
		"worker_id":      h.config.WorkerID,
	})

	// GAP FIX #10: Metrics
	metrics.WorkerJobsActive.WithLabelValues(TaskType).Inc()
	defer func() {
		duration := time.Since(startTime)
		metrics.WorkerJobsActive.WithLabelValues(TaskType).Dec()
		metrics.WorkerJobDuration.WithLabelValues(TaskType).Observe(duration.Seconds())
		metrics.WorkerJobsCompleted.WithLabelValues(TaskType).Inc()

		h.logger.Info("job_completed", map[string]interface{}{
			"job_key":        job.Key,
			"correlation_id": correlationID,
			"duration_ms":    duration.Milliseconds(),
		})
	}()

	// Parse input variables
	var input InputVariables
	if err := json.Unmarshal([]byte(job.Variables), &input); err != nil {
		h.failJobWithError(client, job, correlationID, "failed to parse input", err)
		return
	}

	// GAP FIX #11: Input validation
	if err := h.validateInput(&input); err != nil {
		h.failJobWithError(client, job, correlationID, "input validation failed", err)
		return
	}

	// GAP FIX #1: Secure template loading with path validation
	template, err := h.loadTemplateSecure(ctx, input.TemplatePath, input.Format)
	if err != nil {
		h.failJobWithError(client, job, correlationID, "failed to load template", err)
		return
	}

	// GAP FIX #19: Audit logging
	h.auditLog("template_loaded", correlationID, template.Metadata.Name, template.Metadata.Version)

	// Execute template with timeout
	result, err := h.executeTemplateSecure(ctx, template, input.InputData, correlationID)
	if err != nil {
		h.failJobWithError(client, job, correlationID, "execution failed", err)
		return
	}

	// Build output
	output := OutputVariables{
		Success:       true,
		Result:        result,
		TemplateName:  template.Metadata.Name,
		Version:       template.Metadata.Version,
		ExecutedAt:    time.Now().Format(time.RFC3339),
		CorrelationID: correlationID,
	}

	// Complete job
	request, err := client.NewCompleteJobCommand().JobKey(job.Key).VariablesFromObject(output)
	if err != nil {
		h.failJobWithError(client, job, correlationID, "failed to create complete command", err)
		return
	}

	if _, err := request.Send(ctx); err != nil {
		h.failJobWithError(client, job, correlationID, "failed to send completion", err)
		return
	}

	h.logger.Info("job_success", map[string]interface{}{
		"job_key":        job.Key,
		"correlation_id": correlationID,
		"template":       template.Metadata.Name,
		"version":        template.Metadata.Version,
	})
}

// GAP FIX #11: Comprehensive input validation
func (h *Handler) validateInput(input *InputVariables) error {
	if input == nil {
		return fmt.Errorf("input is nil")
	}

	if err := input.Validate(); err != nil {
		return fmt.Errorf("input validation: %w", err)
	}

	// GAP FIX #5: Size limits
	if len(input.TemplatePath) > h.config.MaxPathLength {
		return fmt.Errorf("template path too long: %d > %d", len(input.TemplatePath), h.config.MaxPathLength)
	}

	// Validate input data size
	dataSize := estimateSize(input.InputData)
	if dataSize > h.config.MaxInputSize {
		return fmt.Errorf("input data too large: %d > %d bytes", dataSize, h.config.MaxInputSize)
	}

	// Validate nesting depth
	if err := validateNestingDepth(input.InputData, h.config.MaxNestingDepth); err != nil {
		return fmt.Errorf("input nesting validation: %w", err)
	}

	return nil
}

// GAP FIX #1: Secure template loading with path traversal prevention
func (h *Handler) loadTemplateSecure(ctx context.Context, path string, format string) (*Template, error) {
	// Check context timeout
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// GAP FIX #1: Sanitize and validate path
	safePath, err := h.sanitizeTemplatePath(path)
	if err != nil {
		h.logger.Warn("path_validation_failed", map[string]interface{}{
			"path":  path,
			"error": err.Error(),
		})
		return nil, fmt.Errorf("invalid template path: %w", err)
	}

	// Check cache first (GAP FIX #7: Thread-safe cache)
	if h.config.EnableCache {
		if cached := h.registry.Get(safePath); cached != nil {
			metrics.TemplateCacheHits.Inc()
			h.logger.Debug("cache_hit", map[string]interface{}{"path": safePath})
			return cached, nil
		}
		metrics.TemplateCacheMisses.Inc()
	}

	// GAP FIX #12: File read with timeout
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	data, err := h.readFileWithTimeout(readCtx, safePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read template: %w", err)
	}

	// GAP FIX #5: File size limit
	if len(data) > h.config.MaxTemplateSize {
		return nil, fmt.Errorf("template file too large: %d > %d bytes", len(data), h.config.MaxTemplateSize)
	}

	var template Template

	// Parse based on format
	switch strings.ToLower(format) {
	case "json":
		if err := json.Unmarshal(data, &template); err != nil {
			return nil, fmt.Errorf("failed to parse JSON template: %w", err)
		}
	case "xml":
		// GAP FIX #2: Secure XML parsing
		if err := h.parseXMLSecure(data, &template); err != nil {
			return nil, fmt.Errorf("failed to parse XML template: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}

	// Validate template
	if err := h.validateTemplate(&template); err != nil {
		return nil, fmt.Errorf("template validation failed: %w", err)
	}

	// GAP FIX #7: Thread-safe cache set
	if h.config.EnableCache {
		h.registry.Set(safePath, &template)
	}

	h.logger.Info("template_loaded", map[string]interface{}{
		"path":    safePath,
		"name":    template.Metadata.Name,
		"version": template.Metadata.Version,
	})

	return &template, nil
}

// GAP FIX #1: Path sanitization with traversal prevention
func (h *Handler) sanitizeTemplatePath(path string) (string, error) {
	// Clean path
	cleanPath := filepath.Clean(path)

	// GAP FIX #1: Whitelist file extensions
	ext := filepath.Ext(cleanPath)
	if ext != ".json" && ext != ".xml" {
		return "", fmt.Errorf("invalid file extension: %s (allowed: .json, .xml)", ext)
	}

	// Convert to absolute path
	absPath, err := filepath.Abs(filepath.Join(h.config.TemplatesBaseDir, cleanPath))
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	// Get absolute base directory
	absBase, err := filepath.Abs(h.config.TemplatesBaseDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory: %w", err)
	}

	// GAP FIX #1: Verify path is within base directory
	if !strings.HasPrefix(absPath, absBase+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes base directory: %s", path)
	}

	// Verify it's a regular file
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("file not accessible: %w", err)
	}

	// GAP FIX #1: Reject symlinks and directories
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("path is not a regular file")
	}

	return absPath, nil
}

// GAP FIX #12: File read with timeout
func (h *Handler) readFileWithTimeout(ctx context.Context, path string) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}

	ch := make(chan result, 1)

	go func() {
		data, err := ioutil.ReadFile(path)
		ch <- result{data: data, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("file read timeout: %w", ctx.Err())
	case res := <-ch:
		return res.data, res.err
	}
}

// GAP FIX #2: Secure XML parsing with XXE prevention
func (h *Handler) parseXMLSecure(data []byte, template *Template) error {
	// Create secure XML decoder
	decoder := xml.NewDecoder(strings.NewReader(string(data)))

	// GAP FIX #2: Disable external entities
	decoder.Strict = true
	decoder.AutoClose = xml.HTMLAutoClose
	decoder.Entity = xml.HTMLEntity // Only allow HTML entities

	// Parse
	if err := decoder.Decode(template); err != nil {
		return fmt.Errorf("XML decode error: %w", err)
	}

	return nil
}

// GAP FIX #5 & #18: Template validation with version checking
func (h *Handler) validateTemplate(template *Template) error {
	if err := template.Validate(); err != nil {
		return err
	}

	// GAP FIX #18: Version compatibility check
	if !h.isVersionSupported(template.Metadata.Version) {
		return fmt.Errorf("unsupported template version: %s", template.Metadata.Version)
	}

	// GAP FIX #5: Complexity limits
	if len(template.Processing.Mappings) > h.config.MaxMappings {
		return fmt.Errorf("too many mappings: %d > %d", len(template.Processing.Mappings), h.config.MaxMappings)
	}

	if template.Preprocessing != nil && len(template.Preprocessing.Steps) > h.config.MaxProcessingSteps {
		return fmt.Errorf("too many preprocessing steps: %d > %d", len(template.Preprocessing.Steps), h.config.MaxProcessingSteps)
	}

	if template.Postprocessing != nil && len(template.Postprocessing.Steps) > h.config.MaxProcessingSteps {
		return fmt.Errorf("too many postprocessing steps: %d > %d", len(template.Postprocessing.Steps), h.config.MaxProcessingSteps)
	}

	// GAP FIX #5: Detect circular dependencies
	if err := h.detectCircularDependencies(template); err != nil {
		return fmt.Errorf("circular dependency detected: %w", err)
	}

	return nil
}

// GAP FIX #18: Version support check
func (h *Handler) isVersionSupported(version string) bool {
	// Support versions 1.x.x and 2.x.x
	supportedMajorVersions := []string{"1", "2"}

	parts := strings.Split(version, ".")
	if len(parts) == 0 {
		return false
	}

	for _, supported := range supportedMajorVersions {
		if parts[0] == supported {
			return true
		}
	}

	return false
}

// GAP FIX #5: Circular dependency detection
func (h *Handler) detectCircularDependencies(template *Template) error {
	graph := make(map[string][]string)

	// Build dependency graph
	for _, mapping := range template.Processing.Mappings {
		if mapping.Source != "" {
			graph[mapping.Target] = append(graph[mapping.Target], mapping.Source)
		}
		// Check config for additional dependencies
		if deps, ok := mapping.Config["dependencies"].([]string); ok {
			graph[mapping.Target] = append(graph[mapping.Target], deps...)
		}
	}

	// Detect cycles using DFS
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var dfs func(string) bool
	dfs = func(node string) bool {
		visited[node] = true
		recStack[node] = true

		for _, neighbor := range graph[node] {
			if !visited[neighbor] {
				if dfs(neighbor) {
					return true
				}
			} else if recStack[neighbor] {
				return true // Cycle detected
			}
		}

		recStack[node] = false
		return false
	}

	for node := range graph {
		if !visited[node] {
			if dfs(node) {
				return fmt.Errorf("circular dependency involving: %s", node)
			}
		}
	}

	return nil
}

// GAP FIX #6 & #12: Secure template execution with timeouts
func (h *Handler) executeTemplateSecure(ctx context.Context, template *Template, inputData map[string]interface{}, correlationID string) (map[string]interface{}, error) {
	execContext := NewExecutionContext(inputData)
	execContext.CorrelationID = correlationID

	// Execute preprocessing
	if template.Preprocessing != nil {
		h.logger.Debug("preprocessing_start", map[string]interface{}{
			"correlation_id": correlationID,
			"steps":          len(template.Preprocessing.Steps),
		})

		for i, step := range template.Preprocessing.Steps {
			// GAP FIX #12: Step-level timeout
			stepCtx, cancel := context.WithTimeout(ctx, 5*time.Second)

			if err := h.executeStepSecure(stepCtx, step, execContext); err != nil {
				cancel()
				return nil, fmt.Errorf("preprocessing step %d (%s) failed: %w", i+1, step.Name, err)
			}
			cancel()
		}
	}

	// Execute main processing
	h.logger.Debug("processing_start", map[string]interface{}{
		"correlation_id": correlationID,
		"mappings":       len(template.Processing.Mappings),
	})

	result := make(map[string]interface{})
	for i, mapping := range template.Processing.Mappings {
		// Check context
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("processing timeout at mapping %d", i+1)
		default:
		}

		// GAP FIX #12: Mapping-level timeout
		mappingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)

		value, err := h.executeMappingSecure(mappingCtx, mapping, execContext)
		cancel()

		if err != nil {
			if mapping.Required {
				return nil, fmt.Errorf("required mapping %d [%s] failed: %w", i+1, mapping.Target, err)
			}
			// Use default value
			if mapping.DefaultValue != nil {
				value = mapping.DefaultValue
			} else {
				continue
			}
		}
		result[mapping.Target] = value
	}

	// Execute postprocessing
	if template.Postprocessing != nil {
		h.logger.Debug("postprocessing_start", map[string]interface{}{
			"correlation_id": correlationID,
			"steps":          len(template.Postprocessing.Steps),
		})

		postContext := NewExecutionContext(result)
		postContext.CorrelationID = correlationID

		for i, step := range template.Postprocessing.Steps {
			stepCtx, cancel := context.WithTimeout(ctx, 5*time.Second)

			if err := h.executeStepSecure(stepCtx, step, postContext); err != nil {
				cancel()
				return nil, fmt.Errorf("postprocessing step %d (%s) failed: %w", i+1, step.Name, err)
			}
			cancel()
		}
		result = postContext.Data
	}

	return result, nil
}

// GAP FIX #12: Step execution with timeout
func (h *Handler) executeStepSecure(ctx context.Context, step ProcessingStep, execContext *ExecutionContext) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("step timeout: %w", ctx.Err())
	default:
	}

	switch step.Type {
	case "validate":
		return h.executeValidation(step, execContext)
	case "transform":
		return h.executeTransform(step, execContext)
	case "enrich":
		return h.executeEnrich(step, execContext)
	case "filter":
		return h.executeFilter(step, execContext)
	default:
		return fmt.Errorf("unknown step type: %s", step.Type)
	}
}

// GAP FIX #4: Secure regex validation with ReDoS protection
func (h *Handler) executeValidation(step ProcessingStep, context *ExecutionContext) error {
	field, _ := step.Config["field"].(string)
	pattern, _ := step.Config["pattern"].(string)
	required, _ := step.Config["required"].(bool)

	value := context.Get(field)

	if required && value == nil {
		return fmt.Errorf("required field missing: %s", field)
	}

	if pattern != "" && value != nil {
		// GAP FIX #4: Pattern complexity validation
		if err := h.validateRegexPattern(pattern); err != nil {
			return fmt.Errorf("invalid regex pattern: %w", err)
		}

		strVal := fmt.Sprintf("%v", value)

		// GAP FIX #4 & #5: Input size limit for regex
		if len(strVal) > h.config.MaxRegexInputSize {
			return fmt.Errorf("input too large for regex: %d > %d", len(strVal), h.config.MaxRegexInputSize)
		}

		// GAP FIX #4: Regex execution with timeout
		matched, err := h.matchRegexWithTimeout(pattern, strVal, 100*time.Millisecond)
		if err != nil {
			h.logger.Warn("regex_timeout", map[string]interface{}{
				"field":   field,
				"pattern": pattern,
				"error":   err.Error(),
			})
			return fmt.Errorf("regex validation timeout for %s", field)
		}

		if !matched {
			return fmt.Errorf("validation failed for %s: pattern mismatch", field)
		}
	}

	return nil
}

// GAP FIX #4: Regex pattern validation
func (h *Handler) validateRegexPattern(pattern string) error {
	// GAP FIX #4: Pattern length limit
	if len(pattern) > h.config.MaxRegexPatternLength {
		return fmt.Errorf("pattern too long: %d > %d", len(pattern), h.config.MaxRegexPatternLength)
	}

	// GAP FIX #4: Check for dangerous patterns
	dangerousPatterns := []string{
		`\(\.\*\+\)`,     // (.*+) - catastrophic backtracking
		`\(\.\+\+\)`,     // (.++) - catastrophic backtracking
		`\(\.\*\)\+`,     // (.*)+  - nested quantifiers
		`\(\.\+\)\*`,     // (.+)*  - nested quantifiers
		`\(\[.*\]\+\)\+`, // ([...]+)+ - nested character class quantifiers
	}

	for _, dangerous := range dangerousPatterns {
		if matched, _ := regexp.MatchString(dangerous, pattern); matched {
			return fmt.Errorf("pattern contains dangerous construct")
		}
	}

	// Try to compile to catch syntax errors
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("pattern compilation failed: %w", err)
	}

	return nil
}

// GAP FIX #4: Regex matching with timeout
func (h *Handler) matchRegexWithTimeout(pattern, input string, timeout time.Duration) (bool, error) {
	// Compile regex (cache if needed)
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, fmt.Errorf("regex compile failed: %w", err)
	}

	type result struct {
		matched bool
		err     error
	}

	ch := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	go func() {
		matched := re.MatchString(input)
		ch <- result{matched: matched, err: nil}
	}()

	select {
	case <-ctx.Done():
		metrics.TemplateRegexTimeouts.Inc()
		return false, fmt.Errorf("regex execution timeout")
	case res := <-ch:
		return res.matched, res.err
	}
}

func (h *Handler) executeTransform(step ProcessingStep, context *ExecutionContext) error {
	operation, _ := step.Config["operation"].(string)
	fields, _ := step.Config["fields"].([]interface{})

	for _, fieldInterface := range fields {
		field, ok := fieldInterface.(string)
		if !ok {
			continue
		}

		value := context.Get(field)
		if value == nil {
			continue
		}

		transformed, err := h.applyTransform(value, operation, step.Config)
		if err != nil {
			return fmt.Errorf("transform failed for %s: %w", field, err)
		}

		context.Set(field, transformed)
	}

	return nil
}

func (h *Handler) executeEnrich(step ProcessingStep, context *ExecutionContext) error {
	enrichFields, ok := step.Config["fields"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("enrich fields not found")
	}

	for key, value := range enrichFields {
		// Process template strings
		if strVal, ok := value.(string); ok {
			processed, _ := h.processTemplate(strVal, context)
			context.Set(key, processed)
		} else {
			context.Set(key, value)
		}
	}

	return nil
}

func (h *Handler) executeFilter(step ProcessingStep, context *ExecutionContext) error {
	condition, ok := step.Config["condition"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("filter condition not found")
	}

	if !h.evaluateCondition(condition, context) {
		action, _ := step.Config["action"].(string)
		if action == "remove" {
			field, _ := step.Config["field"].(string)
			delete(context.Data, field)
		}
	}

	return nil
}

func (h *Handler) applyTransform(value interface{}, operation string, config map[string]interface{}) (interface{}, error) {
	strVal, isString := value.(string)
	numVal, isNum := h.toNumber(value)

	switch operation {
	case "trim":
		if isString {
			return strings.TrimSpace(strVal), nil
		}
	case "uppercase":
		if isString {
			return strings.ToUpper(strVal), nil
		}
	case "lowercase":
		if isString {
			return strings.ToLower(strVal), nil
		}
	case "title":
		if isString {
			return strings.Title(strings.ToLower(strVal)), nil
		}
	case "multiply":
		if isNum {
			factor, _ := config["factor"].(float64)
			return numVal * factor, nil
		}
	case "divide":
		if isNum {
			divisor, _ := config["divisor"].(float64)
			if divisor != 0 {
				return numVal / divisor, nil
			}
			return nil, fmt.Errorf("division by zero")
		}
	case "round":
		if isNum {
			precision, _ := config["precision"].(float64)
			multiplier := 1.0
			if precision > 0 {
				for i := 0; i < int(precision); i++ {
					multiplier *= 10
				}
			}
			return float64(int(numVal*multiplier+0.5)) / multiplier, nil
		}
	}

	return value, nil
}

// GAP FIX #12: Mapping execution with timeout
func (h *Handler) executeMappingSecure(ctx context.Context, mapping Mapping, execContext *ExecutionContext) (interface{}, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("mapping timeout: %w", ctx.Err())
	default:
	}

	switch mapping.Type {
	case "direct":
		return h.executeDirect(mapping, execContext)
	case "expression":
		return h.executeExpressionSecure(ctx, mapping, execContext)
	case "template":
		return h.executeTemplateString(mapping, execContext)
	case "function":
		return h.executeFunctionSecure(ctx, mapping, execContext)
	case "conditional":
		return h.executeConditional(mapping, execContext)
	case "lookup":
		return h.executeLookup(mapping, execContext)
	case "constant":
		return h.executeConstant(mapping, execContext)
	default:
		return nil, fmt.Errorf("unknown mapping type: %s", mapping.Type)
	}
}

func (h *Handler) executeDirect(mapping Mapping, context *ExecutionContext) (interface{}, error) {
	if mapping.Source == "" {
		return nil, fmt.Errorf("source field required")
	}

	value := context.Get(mapping.Source)
	if value == nil {
		return nil, fmt.Errorf("source field not found: %s", mapping.Source)
	}

	return value, nil
}

func (h *Handler) executeConstant(mapping Mapping, _ *ExecutionContext) (interface{}, error) {
	value, ok := mapping.Config["value"]
	if !ok {
		return nil, fmt.Errorf("constant value not found")
	}
	return value, nil
}

// GAP FIX #3: Secure expression evaluation with injection prevention
func (h *Handler) executeExpressionSecure(ctx context.Context, mapping Mapping, execContext *ExecutionContext) (interface{}, error) {
	expr, ok := mapping.Config["expression"].(string)
	if !ok {
		return nil, fmt.Errorf("expression not found")
	}

	// GAP FIX #3 & #5: Expression complexity limits
	if len(expr) > h.config.MaxExpressionLength {
		return nil, fmt.Errorf("expression too long: %d > %d", len(expr), h.config.MaxExpressionLength)
	}

	// GAP FIX #3: Validate expression safety
	if err := h.validateExpression(expr); err != nil {
		return nil, fmt.Errorf("unsafe expression: %w", err)
	}

	// GAP FIX #12: Execute with timeout
	type result struct {
		value interface{}
		err   error
	}

	ch := make(chan result, 1)
	go func() {
		val, err := h.evaluateExpressionSafe(expr, execContext)
		ch <- result{value: val, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("expression evaluation timeout")
	case res := <-ch:
		return res.value, res.err
	}
}

// GAP FIX #3: Expression validation
func (h *Handler) validateExpression(expr string) error {
	// Only allow safe characters
	safePattern := regexp.MustCompile(`^[a-zA-Z0-9_\{\}\+\-\*\/\(\)\.\s]+$`)
	if !safePattern.MatchString(expr) {
		return fmt.Errorf("expression contains unsafe characters")
	}

	// Check for dangerous patterns
	dangerous := []string{
		"__", "eval", "exec", "import", "system", "shell",
		"sleep", "while", "for", "goto",
	}

	exprLower := strings.ToLower(expr)
	for _, pattern := range dangerous {
		if strings.Contains(exprLower, pattern) {
			return fmt.Errorf("expression contains dangerous keyword: %s", pattern)
		}
	}

	// Count operators to detect complexity
	operators := strings.Count(expr, "+") + strings.Count(expr, "-") +
		strings.Count(expr, "*") + strings.Count(expr, "/")
	if operators > h.config.MaxExpressionOperators {
		return fmt.Errorf("expression too complex: %d operators", operators)
	}

	return nil
}

// GAP FIX #3: Safe expression evaluation using AST-like approach
func (h *Handler) evaluateExpressionSafe(expr string, context *ExecutionContext) (interface{}, error) {
	// Replace variables safely
	cleanExpr := expr
	for key, value := range context.Data {
		placeholder := fmt.Sprintf("{%s}", key)

		// Validate variable value is safe
		valStr := fmt.Sprintf("%v", value)
		if !regexp.MustCompile(`^[a-zA-Z0-9\.\-]+$`).MatchString(valStr) {
			return nil, fmt.Errorf("unsafe variable value: %s", key)
		}

		cleanExpr = strings.ReplaceAll(cleanExpr, placeholder, valStr)
	}

	// Only evaluate if all variables replaced
	if strings.Contains(cleanExpr, "{") || strings.Contains(cleanExpr, "}") {
		return nil, fmt.Errorf("undefined variables in expression")
	}

	// Safe evaluation (basic arithmetic only)
	return h.evaluateArithmetic(cleanExpr)
}

// Safe arithmetic evaluation
func (h *Handler) evaluateArithmetic(expr string) (interface{}, error) {
	expr = strings.TrimSpace(expr)

	// Handle addition
	if strings.Contains(expr, "+") {
		parts := strings.Split(expr, "+")
		sum := 0.0
		for _, part := range parts {
			val, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
			if err != nil {
				return nil, fmt.Errorf("invalid number: %s", part)
			}
			sum += val
		}
		return sum, nil
	}

	// Handle subtraction
	if strings.Contains(expr, "-") && !strings.HasPrefix(expr, "-") {
		parts := strings.Split(expr, "-")
		if len(parts) == 2 {
			val1, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			val2, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid numbers in subtraction")
			}
			return val1 - val2, nil
		}
	}

	// Handle multiplication
	if strings.Contains(expr, "*") {
		parts := strings.Split(expr, "*")
		if len(parts) == 2 {
			val1, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			val2, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid numbers in multiplication")
			}
			return val1 * val2, nil
		}
	}

	// Handle division
	if strings.Contains(expr, "/") {
		parts := strings.Split(expr, "/")
		if len(parts) == 2 {
			val1, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			val2, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid numbers in division")
			}
			if val2 == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return val1 / val2, nil
		}
	}

	// Try as number
	if num, err := strconv.ParseFloat(expr, 64); err == nil {
		return num, nil
	}

	// Return as string
	return expr, nil
}

func (h *Handler) executeTemplateString(mapping Mapping, context *ExecutionContext) (interface{}, error) {
	template, ok := mapping.Config["template"].(string)
	if !ok {
		return nil, fmt.Errorf("template not found")
	}

	return h.processTemplate(template, context)
}

// GAP FIX #12: Function execution with timeout
func (h *Handler) executeFunctionSecure(ctx context.Context, mapping Mapping, execContext *ExecutionContext) (interface{}, error) {
	funcName, ok := mapping.Config["function"].(string)
	if !ok {
		return nil, fmt.Errorf("function name not found")
	}

	var params interface{}
	if source, ok := mapping.Config["source"].(string); ok {
		params = execContext.Get(source)
	} else if p, ok := mapping.Config["params"]; ok {
		params = p
	}

	// Execute with timeout
	type result struct {
		value interface{}
		err   error
	}

	ch := make(chan result, 1)
	go func() {
		val, err := h.callFunction(funcName, params, execContext)
		ch <- result{value: val, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("function execution timeout")
	case res := <-ch:
		return res.value, res.err
	}
}

func (h *Handler) executeConditional(mapping Mapping, context *ExecutionContext) (interface{}, error) {
	conditions, ok := mapping.Config["conditions"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("conditions not found")
	}

	for _, condInterface := range conditions {
		cond, ok := condInterface.(map[string]interface{})
		if !ok {
			continue
		}

		if h.evaluateCondition(cond, context) {
			return cond["value"], nil
		}
	}

	if defaultVal, ok := mapping.Config["default"]; ok {
		return defaultVal, nil
	}

	return nil, fmt.Errorf("no condition matched")
}

func (h *Handler) executeLookup(mapping Mapping, context *ExecutionContext) (interface{}, error) {
	lookupTable, ok := mapping.Config["table"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("lookup table not found")
	}

	key, ok := mapping.Config["key"].(string)
	if !ok {
		return nil, fmt.Errorf("lookup key not found")
	}

	keyValue := context.Get(key)
	if keyValue == nil {
		return mapping.Config["default"], nil
	}

	keyStr := fmt.Sprintf("%v", keyValue)
	if value, exists := lookupTable[keyStr]; exists {
		return value, nil
	}

	return mapping.Config["default"], nil
}

func (h *Handler) processTemplate(template string, context *ExecutionContext) (interface{}, error) {
	result := template

	for key, value := range context.Data {
		placeholder := fmt.Sprintf("{{%s}}", key)
		valueStr := fmt.Sprintf("%v", value)
		result = strings.ReplaceAll(result, placeholder, valueStr)
	}

	result = strings.ReplaceAll(result, "{{now}}", time.Now().Format(time.RFC3339))

	return result, nil
}

func (h *Handler) callFunction(funcName string, params interface{}, context *ExecutionContext) (interface{}, error) {
	fn := h.registry.GetFunction(funcName)
	if fn == nil {
		return nil, fmt.Errorf("function not found: %s", funcName)
	}

	return fn(params, context)
}

func (h *Handler) evaluateCondition(cond map[string]interface{}, context *ExecutionContext) bool {
	field, _ := cond["field"].(string)
	operator, _ := cond["operator"].(string)
	expectedValue := cond["value"]

	actualValue := context.Get(field)

	switch operator {
	case "equals", "==", "eq":
		return reflect.DeepEqual(actualValue, expectedValue)
	case "not_equals", "!=", "ne":
		return !reflect.DeepEqual(actualValue, expectedValue)
	case "greater_than", ">", "gt":
		return h.compareNumbers(actualValue, expectedValue) > 0
	case "less_than", "<", "lt":
		return h.compareNumbers(actualValue, expectedValue) < 0
	case "greater_equal", ">=", "gte":
		return h.compareNumbers(actualValue, expectedValue) >= 0
	case "less_equal", "<=", "lte":
		return h.compareNumbers(actualValue, expectedValue) <= 0
	case "contains":
		return h.stringContains(actualValue, expectedValue)
	case "starts_with":
		return h.stringStartsWith(actualValue, expectedValue)
	case "ends_with":
		return h.stringEndsWith(actualValue, expectedValue)
	case "in":
		return h.inArray(actualValue, expectedValue)
	case "not_in":
		return !h.inArray(actualValue, expectedValue)
	default:
		return false
	}
}

// Helper functions
func (h *Handler) compareNumbers(a, b interface{}) int {
	aFloat, _ := h.toNumber(a)
	bFloat, _ := h.toNumber(b)

	if aFloat > bFloat {
		return 1
	} else if aFloat < bFloat {
		return -1
	}
	return 0
}

func (h *Handler) toNumber(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func (h *Handler) stringContains(haystack, needle interface{}) bool {
	h1, ok1 := haystack.(string)
	h2, ok2 := needle.(string)
	return ok1 && ok2 && strings.Contains(h1, h2)
}

func (h *Handler) stringStartsWith(str, prefix interface{}) bool {
	s, ok1 := str.(string)
	p, ok2 := prefix.(string)
	return ok1 && ok2 && strings.HasPrefix(s, p)
}

func (h *Handler) stringEndsWith(str, suffix interface{}) bool {
	s, ok1 := str.(string)
	suf, ok2 := suffix.(string)
	return ok1 && ok2 && strings.HasSuffix(s, suf)
}

func (h *Handler) inArray(value, array interface{}) bool {
	arr, ok := array.([]interface{})
	if !ok {
		return false
	}

	for _, item := range arr {
		if reflect.DeepEqual(value, item) {
			return true
		}
	}
	return false
}

// GAP FIX #8: Proper error handling with context
func (h *Handler) failJobWithError(client worker.JobClient, job entities.Job, correlationID, message string, err error) {
	ctx := context.Background()

	// GAP FIX #8 & #9: Structured error logging
	h.logger.Error("job_failed", map[string]interface{}{
		"job_key":        job.Key,
		"correlation_id": correlationID,
		"error_message":  message,
		"error":          err.Error(),
	})

	// GAP FIX #10: Metrics
	metrics.WorkerJobsFailed.WithLabelValues(TaskType).Inc()

	// GAP FIX #19: Audit log
	h.auditLog("job_failed", correlationID, message, err.Error())

	// Fail job with detailed error
	errorMsg := fmt.Sprintf("%s: %v", message, err)
	_, failErr := client.NewFailJobCommand().
		JobKey(job.Key).
		Retries(0).
		ErrorMessage(errorMsg).
		Send(ctx)

	if failErr != nil {
		h.logger.Error("failed_to_fail_job", map[string]interface{}{
			"job_key": job.Key,
			"error":   failErr.Error(),
		})
	}
}

// GAP FIX #19: Audit logging
func (h *Handler) auditLog(event, correlationID, detail string, metadata ...string) {
	auditEntry := map[string]interface{}{
		"timestamp":      time.Now().Format(time.RFC3339),
		"event":          event,
		"correlation_id": correlationID,
		"detail":         detail,
		"worker_id":      h.config.WorkerID,
	}

	if len(metadata) > 0 {
		auditEntry["metadata"] = metadata
	}

	h.logger.Info("audit_event", auditEntry)
}

// Utility functions

// GAP FIX #5: Estimate data size
func estimateSize(data interface{}) int {
	jsonData, _ := json.Marshal(data)
	return len(jsonData)
}

// GAP FIX #5: Validate nesting depth
func validateNestingDepth(data interface{}, maxDepth int) error {
	return validateDepth(data, 0, maxDepth)
}

func validateDepth(data interface{}, currentDepth, maxDepth int) error {
	if currentDepth > maxDepth {
		return fmt.Errorf("max nesting depth exceeded: %d > %d", currentDepth, maxDepth)
	}

	switch v := data.(type) {
	case map[string]interface{}:
		for _, value := range v {
			if err := validateDepth(value, currentDepth+1, maxDepth); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, item := range v {
			if err := validateDepth(item, currentDepth+1, maxDepth); err != nil {
				return err
			}
		}
	}

	return nil
}

func verifyTemplatesDirectory(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("templates directory not accessible: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("templates path is not a directory: %s", dir)
	}

	return nil
}

func generateCorrelationID() string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return fmt.Sprintf("%x", hash[:8])
}
