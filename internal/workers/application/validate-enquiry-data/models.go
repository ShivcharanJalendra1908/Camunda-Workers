package validateenquirydata

const TaskType = "validate-enquiry-data"

// Input - BPMN se aane wala data
type Input struct {
	EntityID        string                 `json:"entityId,omitempty"`
	EntityType      string                 `json:"entityType,omitempty"`
	FranchiseID     string                 `json:"franchiseId,omitempty"`
	AssociationID   string                 `json:"associationId,omitempty"`
	UserID          string                 `json:"userId"`
	UserProfile     map[string]interface{} `json:"userProfile"` // DB se fetch hoga
	Data            map[string]interface{} `json:"data"`
	EnquiryFormData map[string]interface{} `json:"enquiryFormData"` // Frontend se aaya form data
}

// Output - merge + validate ke baad
type Output struct {
	IsValid          bool                   `json:"isValid"`
	MergedData       map[string]interface{} `json:"mergedData"`
	ValidationErrors []string               `json:"validationErrors"`
}
