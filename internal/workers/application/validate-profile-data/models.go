package validateprofiledata

type Config struct {
	Timeout int `yaml:"timeout" default:"10"`
}

type Input struct {
	Action      string                 `json:"action"`
	UserId      string                 `json:"userId"`
	ProfileData map[string]interface{} `json:"profileData"`
	RequestId   string                 `json:"requestId,omitempty"`
}

type Output struct {
	IsValid      bool              `json:"isValid"`
	ErrorCode    string            `json:"errorCode,omitempty"`
	ErrorMessage string            `json:"errorMessage,omitempty"`
	ValidatedData map[string]interface{} `json:"validatedData,omitempty"`
}

type ValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
