// ============================================================
// FILE: internal/workers/infrastructure/send-api-response/models.go
// ============================================================

package send_api_response

// Input - Data received from workflow
type Input struct {
	CorrelationKey string                 `json:"correlationKey"` // Unique ID to match with waiting API request
	Response       map[string]interface{} `json:"response"`       // Final response to send to client
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// Output - Result of sending response
type Output struct {
	Success   bool   `json:"success"`            // Overall operation success
	SentToAPI bool   `json:"sentToApi"`          // Whether response was sent to API
	Error     string `json:"error,omitempty"`    // Worker error if any
	ApiError  string `json:"apiError,omitempty"` // API gateway error if any
}

// APIResponse - Standard API response format
type APIResponse struct {
	Success   bool                   `json:"success"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Timestamp string                 `json:"timestamp"`
	RequestId string                 `json:"requestId,omitempty"`
}
