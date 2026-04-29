package publicforms

import "time"

// PublicFormSubmission represents a record in the public_form_submissions table
type PublicFormSubmission struct {
	ID        string                 `json:"id"`
	FormType  string                 `json:"formType"`
	Email     string                 `json:"email"`
	Phone     string                 `json:"phone"`
	FormData  map[string]interface{} `json:"formData"`
	Status    string                 `json:"status"`
	CreatedAt time.Time              `json:"createdAt"`
}

// ValidationVariables represents the variables used in the validation job
type ValidationVariables struct {
	FormType string                 `json:"formType"`
	FormData map[string]interface{} `json:"formData"`
}

// ValidationResponse represents the output of the validation job
type ValidationResponse struct {
	IsValid          bool              `json:"isValid"`
	ValidationErrors map[string]string `json:"validationErrors"`
}

// SaveResponse represents the output of the save job
type SaveResponse struct {
	SubmissionID string `json:"submissionId"`
}
