package template_driven

import (
	"container/list"
	"fmt"
	"strings"
	"sync"
	"time"
)

// InputVariables - Worker input with validation
type InputVariables struct {
	TemplatePath string                 `json:"templatePath"`
	InputData    map[string]interface{} `json:"inputData"`
	Format       string                 `json:"format"`
	Context      map[string]interface{} `json:"context"`
}

func (i *InputVariables) Validate() error {
	if i.TemplatePath == "" {
		return fmt.Errorf("templatePath is required")
	}
	if i.InputData == nil {
		i.InputData = make(map[string]interface{})
	}
	if i.Format == "" {
		i.Format = "json"
	}
	if i.Format != "json" && i.Format != "xml" {
		return fmt.Errorf("format must be 'json' or 'xml'")
	}
	return nil
}

// OutputVariables - Worker output
type OutputVariables struct {
	Success       bool                   `json:"success"`
	Result        map[string]interface{} `json:"result"`
	TemplateName  string                 `json:"templateName"`
	Version       string                 `json:"version"`
	ExecutedAt    string                 `json:"executedAt"`
	CorrelationID string                 `json:"correlationId"`
	ErrorMessage  string                 `json:"errorMessage,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
}

// Template - Complete template structure
type Template struct {
	Metadata       TemplateMetadata       `json:"metadata" xml:"metadata"`
	InputSchema    Schema                 `json:"inputSchema" xml:"inputSchema"`
	OutputSchema   Schema                 `json:"outputSchema" xml:"outputSchema"`
	Preprocessing  *PreProcessing         `json:"preprocessing,omitempty" xml:"preprocessing,omitempty"`
	Processing     Processing             `json:"processing" xml:"processing"`
	Postprocessing *PostProcessing        `json:"postprocessing,omitempty" xml:"postprocessing,omitempty"`
	Functions      []Function             `json:"functions,omitempty" xml:"functions,omitempty"`
	Variables      map[string]interface{} `json:"variables,omitempty" xml:"variables,omitempty"`
}

func (t *Template) Validate() error {
	if t.Metadata.Name == "" {
		return fmt.Errorf("template name is required")
	}
	if t.Metadata.Version == "" {
		return fmt.Errorf("template version is required")
	}
	if len(t.Processing.Mappings) == 0 {
		return fmt.Errorf("at least one mapping is required")
	}
	return nil
}

// TemplateMetadata - Template information
type TemplateMetadata struct {
	Name        string   `json:"name" xml:"name"`
	Version     string   `json:"version" xml:"version"`
	Description string   `json:"description,omitempty" xml:"description,omitempty"`
	Author      string   `json:"author,omitempty" xml:"author,omitempty"`
	Tags        []string `json:"tags,omitempty" xml:"tags,omitempty"`
	CreatedAt   string   `json:"createdAt,omitempty" xml:"createdAt,omitempty"`
	UpdatedAt   string   `json:"updatedAt,omitempty" xml:"updatedAt,omitempty"`
	Category    string   `json:"category,omitempty" xml:"category,omitempty"`
}

// Schema - Data schema definition
type Schema struct {
	Fields map[string]Field `json:"fields" xml:"fields"`
}

// Field - Field definition
type Field struct {
	Type        string           `json:"type" xml:"type"`
	Required    bool             `json:"required,omitempty" xml:"required,omitempty"`
	Default     interface{}      `json:"default,omitempty" xml:"default,omitempty"`
	Description string           `json:"description,omitempty" xml:"description,omitempty"`
	Format      string           `json:"format,omitempty" xml:"format,omitempty"`
	Pattern     string           `json:"pattern,omitempty" xml:"pattern,omitempty"`
	MinLength   int              `json:"minLength,omitempty" xml:"minLength,omitempty"`
	MaxLength   int              `json:"maxLength,omitempty" xml:"maxLength,omitempty"`
	Minimum     float64          `json:"minimum,omitempty" xml:"minimum,omitempty"`
	Maximum     float64          `json:"maximum,omitempty" xml:"maximum,omitempty"`
	Enum        []string         `json:"enum,omitempty" xml:"enum,omitempty"`
	Items       *Field           `json:"items,omitempty" xml:"items,omitempty"`
	Properties  map[string]Field `json:"properties,omitempty" xml:"properties,omitempty"`
}

// PreProcessing - Steps before main processing
type PreProcessing struct {
	Steps []ProcessingStep `json:"steps" xml:"steps"`
}

// Processing - Main processing logic
type Processing struct {
	Mappings []Mapping `json:"mappings" xml:"mappings"`
}

// PostProcessing - Steps after main processing
type PostProcessing struct {
	Steps []ProcessingStep `json:"steps" xml:"steps"`
}

// ProcessingStep - Generic processing step
type ProcessingStep struct {
	Name        string                 `json:"name" xml:"name"`
	Type        string                 `json:"type" xml:"type"`
	Description string                 `json:"description,omitempty" xml:"description,omitempty"`
	Config      map[string]interface{} `json:"config" xml:"config"`
	Condition   map[string]interface{} `json:"condition,omitempty" xml:"condition,omitempty"`
}

// Mapping - Field mapping rule
type Mapping struct {
	Target       string                 `json:"target" xml:"target"`
	Type         string                 `json:"type" xml:"type"`
	Source       string                 `json:"source,omitempty" xml:"source,omitempty"`
	Config       map[string]interface{} `json:"config,omitempty" xml:"config,omitempty"`
	Required     bool                   `json:"required,omitempty" xml:"required,omitempty"`
	DefaultValue interface{}            `json:"defaultValue,omitempty" xml:"defaultValue,omitempty"`
	Description  string                 `json:"description,omitempty" xml:"description,omitempty"`
	Condition    map[string]interface{} `json:"condition,omitempty" xml:"condition,omitempty"`
}

// Function - Custom function definition
type Function struct {
	Name        string                 `json:"name" xml:"name"`
	Description string                 `json:"description,omitempty" xml:"description,omitempty"`
	Type        string                 `json:"type" xml:"type"`
	Config      map[string]interface{} `json:"config" xml:"config"`
	Params      []FunctionParam        `json:"params,omitempty" xml:"params,omitempty"`
	Returns     string                 `json:"returns,omitempty" xml:"returns,omitempty"`
}

// FunctionParam - Function parameter definition
type FunctionParam struct {
	Name     string      `json:"name" xml:"name"`
	Type     string      `json:"type" xml:"type"`
	Required bool        `json:"required,omitempty" xml:"required,omitempty"`
	Default  interface{} `json:"default,omitempty" xml:"default,omitempty"`
}

// ExecutionContext - Runtime execution context (thread-safe)
type ExecutionContext struct {
	Data          map[string]interface{}
	Variables     map[string]interface{}
	StartTime     time.Time
	CorrelationID string
	mu            sync.RWMutex
}

func NewExecutionContext(data map[string]interface{}) *ExecutionContext {
	return &ExecutionContext{
		Data:      data,
		Variables: make(map[string]interface{}),
		StartTime: time.Now(),
	}
}

func (ec *ExecutionContext) Get(key string) interface{} {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	// Support nested keys with dot notation
	if strings.Contains(key, ".") {
		parts := strings.Split(key, ".")
		var current interface{} = ec.Data

		for _, part := range parts {
			if m, ok := current.(map[string]interface{}); ok {
				current = m[part]
			} else {
				return nil
			}
		}
		return current
	}

	// Try data first
	if val, ok := ec.Data[key]; ok {
		return val
	}
	// Try variables
	if val, ok := ec.Variables[key]; ok {
		return val
	}
	return nil
}

func (ec *ExecutionContext) Set(key string, value interface{}) {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	// Support nested keys
	if strings.Contains(key, ".") {
		parts := strings.Split(key, ".")
		current := ec.Data

		for i := 0; i < len(parts)-1; i++ {
			if _, ok := current[parts[i]]; !ok {
				current[parts[i]] = make(map[string]interface{})
			}
			if next, ok := current[parts[i]].(map[string]interface{}); ok {
				current = next
			} else {
				return
			}
		}
		current[parts[len(parts)-1]] = value
	} else {
		ec.Data[key] = value
	}
}

func (ec *ExecutionContext) SetVariable(key string, value interface{}) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.Variables[key] = value
}

// GAP FIX #7: Thread-safe template registry with LRU cache
type TemplateRegistry struct {
	templates map[string]*cacheEntry
	functions map[string]TemplateFunction
	lruList   *list.List
	maxSize   int
	mu        sync.RWMutex
	stats     CacheStats
}

type cacheEntry struct {
	key      string
	template *Template
	element  *list.Element
	accessed time.Time
}

type CacheStats struct {
	Hits      int64
	Misses    int64
	Evictions int64
	Size      int
	mu        sync.RWMutex
}

type TemplateFunction func(params interface{}, context *ExecutionContext) (interface{}, error)

func NewTemplateRegistry() *TemplateRegistry {
	registry := &TemplateRegistry{
		templates: make(map[string]*cacheEntry),
		functions: make(map[string]TemplateFunction),
		lruList:   list.New(),
		maxSize:   100, // Default
		stats:     CacheStats{},
	}

	registry.registerBuiltinFunctions()

	return registry
}

// GAP FIX #7: Thread-safe Get
func (tr *TemplateRegistry) Get(path string) *Template {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	entry, exists := tr.templates[path]
	if !exists {
		tr.stats.recordMiss()
		return nil
	}

	// Update LRU
	tr.lruList.MoveToFront(entry.element)
	entry.accessed = time.Now()
	tr.stats.recordHit()

	return entry.template
}

// GAP FIX #7: Thread-safe Set with LRU eviction
func (tr *TemplateRegistry) Set(path string, template *Template) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	// Check if already exists
	if entry, exists := tr.templates[path]; exists {
		entry.template = template
		entry.accessed = time.Now()
		tr.lruList.MoveToFront(entry.element)
		return
	}

	// Check size limit and evict if necessary
	if tr.lruList.Len() >= tr.maxSize {
		tr.evictLRU()
	}

	// Add new entry
	entry := &cacheEntry{
		key:      path,
		template: template,
		accessed: time.Now(),
	}
	entry.element = tr.lruList.PushFront(entry)
	tr.templates[path] = entry

	tr.stats.updateSize(tr.lruList.Len())
}

// GAP FIX #7: LRU eviction
func (tr *TemplateRegistry) evictLRU() {
	if element := tr.lruList.Back(); element != nil {
		entry := element.Value.(*cacheEntry)
		delete(tr.templates, entry.key)
		tr.lruList.Remove(element)
		tr.stats.recordEviction()
	}
}

func (tr *TemplateRegistry) GetFunction(name string) TemplateFunction {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return tr.functions[name]
}

func (tr *TemplateRegistry) RegisterFunction(name string, fn TemplateFunction) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.functions[name] = fn
}

func (tr *TemplateRegistry) GetStats() *CacheStats {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	// Return a snapshot copy to avoid copying the mutex
	return &CacheStats{
		Hits:      tr.stats.Hits,
		Misses:    tr.stats.Misses,
		Evictions: tr.stats.Evictions,
		Size:      tr.stats.Size,
		// Fresh mutex for the copy
		mu: sync.RWMutex{},
	}
}

// Cache statistics methods
func (cs *CacheStats) recordHit() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.Hits++
}

func (cs *CacheStats) recordMiss() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.Misses++
}

func (cs *CacheStats) recordEviction() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.Evictions++
}

func (cs *CacheStats) updateSize(size int) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.Size = size
}

func (cs *CacheStats) HitRate() float64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	total := cs.Hits + cs.Misses
	if total == 0 {
		return 0
	}
	return float64(cs.Hits) / float64(total)
}

// Built-in functions registration
func (tr *TemplateRegistry) registerBuiltinFunctions() {
	// String functions
	tr.RegisterFunction("uppercase", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if str, ok := params.(string); ok {
			return strings.ToUpper(str), nil
		}
		return nil, fmt.Errorf("parameter must be string")
	})

	tr.RegisterFunction("lowercase", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if str, ok := params.(string); ok {
			return strings.ToLower(str), nil
		}
		return nil, fmt.Errorf("parameter must be string")
	})

	tr.RegisterFunction("trim", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if str, ok := params.(string); ok {
			return strings.TrimSpace(str), nil
		}
		return nil, fmt.Errorf("parameter must be string")
	})

	tr.RegisterFunction("concat", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if arr, ok := params.([]interface{}); ok {
			var result string
			for _, item := range arr {
				result += fmt.Sprintf("%v", item)
			}
			return result, nil
		}
		return nil, fmt.Errorf("parameter must be array")
	})

	tr.RegisterFunction("replace", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if paramMap, ok := params.(map[string]interface{}); ok {
			str, _ := paramMap["string"].(string)
			old, _ := paramMap["old"].(string)
			new, _ := paramMap["new"].(string)
			return strings.ReplaceAll(str, old, new), nil
		}
		return nil, fmt.Errorf("invalid parameters")
	})

	// Number functions
	tr.RegisterFunction("add", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if arr, ok := params.([]interface{}); ok {
			sum := 0.0
			for _, item := range arr {
				if num, ok := item.(float64); ok {
					sum += num
				}
			}
			return sum, nil
		}
		return nil, fmt.Errorf("parameter must be array")
	})

	tr.RegisterFunction("multiply", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if paramMap, ok := params.(map[string]interface{}); ok {
			value, _ := paramMap["value"].(float64)
			factor, _ := paramMap["factor"].(float64)
			return value * factor, nil
		}
		return nil, fmt.Errorf("invalid parameters")
	})

	tr.RegisterFunction("divide", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if paramMap, ok := params.(map[string]interface{}); ok {
			dividend, _ := paramMap["dividend"].(float64)
			divisor, _ := paramMap["divisor"].(float64)
			if divisor == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return dividend / divisor, nil
		}
		return nil, fmt.Errorf("invalid parameters")
	})

	tr.RegisterFunction("round", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if paramMap, ok := params.(map[string]interface{}); ok {
			value, _ := paramMap["value"].(float64)
			precision, _ := paramMap["precision"].(float64)
			multiplier := 1.0
			for i := 0; i < int(precision); i++ {
				multiplier *= 10
			}
			return float64(int(value*multiplier+0.5)) / multiplier, nil
		}
		return nil, fmt.Errorf("invalid parameters")
	})

	// Array functions
	tr.RegisterFunction("count", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if arr, ok := params.([]interface{}); ok {
			return len(arr), nil
		}
		return 0, nil
	})

	tr.RegisterFunction("join", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if paramMap, ok := params.(map[string]interface{}); ok {
			arr, _ := paramMap["array"].([]interface{})
			separator, _ := paramMap["separator"].(string)
			var result []string
			for _, item := range arr {
				result = append(result, fmt.Sprintf("%v", item))
			}
			return strings.Join(result, separator), nil
		}
		return nil, fmt.Errorf("invalid parameters")
	})

	tr.RegisterFunction("first", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if arr, ok := params.([]interface{}); ok && len(arr) > 0 {
			return arr[0], nil
		}
		return nil, fmt.Errorf("array is empty")
	})

	tr.RegisterFunction("last", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if arr, ok := params.([]interface{}); ok && len(arr) > 0 {
			return arr[len(arr)-1], nil
		}
		return nil, fmt.Errorf("array is empty")
	})

	// Date/Time functions
	tr.RegisterFunction("now", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		return time.Now().Format(time.RFC3339), nil
	})

	tr.RegisterFunction("formatDate", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if paramMap, ok := params.(map[string]interface{}); ok {
			dateStr, _ := paramMap["date"].(string)
			format, _ := paramMap["format"].(string)

			t, err := time.Parse(time.RFC3339, dateStr)
			if err != nil {
				return nil, err
			}

			return t.Format(format), nil
		}
		return nil, fmt.Errorf("invalid parameters")
	})

	// Logic functions
	tr.RegisterFunction("if", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if paramMap, ok := params.(map[string]interface{}); ok {
			condition, _ := paramMap["condition"].(bool)
			thenVal := paramMap["then"]
			elseVal := paramMap["else"]
			if condition {
				return thenVal, nil
			}
			return elseVal, nil
		}
		return nil, fmt.Errorf("invalid parameters")
	})

	tr.RegisterFunction("coalesce", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if arr, ok := params.([]interface{}); ok {
			for _, item := range arr {
				if item != nil {
					return item, nil
				}
			}
		}
		return nil, nil
	})

	// Utility functions
	tr.RegisterFunction("generateUUID", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		return fmt.Sprintf("%d-%d", time.Now().UnixNano(), ctx.StartTime.UnixNano()), nil
	})

	tr.RegisterFunction("hash", func(params interface{}, ctx *ExecutionContext) (interface{}, error) {
		if str, ok := params.(string); ok {
			hash := 0
			for _, c := range str {
				hash = hash*31 + int(c)
			}
			return fmt.Sprintf("%x", hash), nil
		}
		return nil, fmt.Errorf("parameter must be string")
	})
}
