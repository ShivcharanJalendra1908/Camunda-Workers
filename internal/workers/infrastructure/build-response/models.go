// internal/workers/infrastructure/build-response/models.go
package buildresponse

// Input matches REQ-INFRA-007
type Input struct {
	TemplateId string                 `json:"templateId"`
	RequestId  string                 `json:"requestId"`
	Data       map[string]interface{} `json:"data"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// Output matches REQ-INFRA-007
type Output struct {
	Response ResponsePayload `json:"response"`
}

type ResponsePayload struct {
	RequestId string                 `json:"requestId"`
	Status    string                 `json:"status"` // "success" or "error"
	Data      map[string]interface{} `json:"data"`
	Metadata  ResponseMetadata       `json:"metadata"`
}

type ResponseMetadata struct {
	Timestamp string `json:"timestamp"` // ISO 8601
	Version   string `json:"version"`
}

// TemplateDefinition matches REQ-INFRA-008
type TemplateDefinition struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`     // ai-response, franchise-detail, etc.
	Schema   map[string]interface{} `json:"schema"`   // JSON Schema for validation
	Template map[string]interface{} `json:"template"` // Base structure with placeholders
	Version  string                 `json:"version"`
}
