// pkg/registry/schema.go
package registry

type ActivityRegistry struct {
	Version     string     `json:"version"`
	LastUpdated string     `json:"lastUpdated"`
	Activities  []Activity `json:"activities"`
}

type Activity struct {
	ID                   string                 `json:"id"`
	DisplayName          string                 `json:"displayName"`
	Description          string                 `json:"description"`
	Category             string                 `json:"category"`
	Version              string                 `json:"version"`
	TaskType             string                 `json:"taskType"`
	ImplementationStatus string                 `json:"implementationStatus"`
	InputSchema          map[string]interface{} `json:"inputSchema"`
	OutputSchema         map[string]interface{} `json:"outputSchema"`
	ErrorCodes           []string               `json:"errorCodes"`
	Timeout              string                 `json:"timeout"`
	Retries              int                    `json:"retries"`
	Workflows            []string               `json:"workflows"`
	Tags                 []string               `json:"tags"`
}
