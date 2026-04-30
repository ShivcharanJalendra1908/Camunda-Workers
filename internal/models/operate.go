package models

import "time"

// ── Process Instance ──────────────────────────────────────────────────────────

type ProcessInstanceState string

const (
	ProcessInstanceActive    ProcessInstanceState = "ACTIVE"
	ProcessInstanceCompleted ProcessInstanceState = "COMPLETED"
	ProcessInstanceCanceled  ProcessInstanceState = "CANCELED"
)

type ProcessInstance struct {
	ProcessInstanceKey   int64                `json:"processInstanceKey"`
	ProcessDefinitionKey int64                `json:"processDefinitionKey"`
	BpmnProcessID        string               `json:"bpmnProcessId"`
	Version              int32                `json:"version"`
	State                ProcessInstanceState `json:"state"`
	StartTime            time.Time            `json:"startTime"`
	EndTime              *time.Time           `json:"endTime,omitempty"`
	ParentInstanceKey    *int64               `json:"parentInstanceKey,omitempty"`
	TenantID             string               `json:"tenantId,omitempty"`
	// Enriched at query time
	IncidentCount int `json:"incidentCount,omitempty"`
}

type ProcessInstanceListResponse struct {
	Items      []ProcessInstance `json:"items"`
	TotalCount int64             `json:"totalCount"`
	Page       int               `json:"page"`
	PageSize   int               `json:"pageSize"`
}

type ProcessInstanceFilter struct {
	BpmnProcessID string               `json:"bpmnProcessId,omitempty" form:"bpmnProcessId"`
	State         ProcessInstanceState `json:"state,omitempty"         form:"state"`
	StartTimeFrom *time.Time           `json:"startTimeFrom,omitempty" form:"startTimeFrom"`
	StartTimeTo   *time.Time           `json:"startTimeTo,omitempty"   form:"startTimeTo"`
	Page          int                  `json:"page,omitempty"          form:"page"`
	PageSize      int                  `json:"pageSize,omitempty"      form:"pageSize"`
}

// ── Job ───────────────────────────────────────────────────────────────────────

type JobState string

const (
	JobActivatable JobState = "ACTIVATABLE"
	JobActivated   JobState = "ACTIVATED"
	JobCompleted   JobState = "COMPLETED"
	JobFailed      JobState = "FAILED"
)

type Job struct {
	JobKey               int64    `json:"jobKey"`
	ProcessInstanceKey   int64    `json:"processInstanceKey"`
	ProcessDefinitionKey int64    `json:"processDefinitionKey"`
	BpmnProcessID        string   `json:"bpmnProcessId"`
	ElementID            string   `json:"elementId"`
	JobType              string   `json:"jobType"`
	State                JobState `json:"state"`
	Retries              int32    `json:"retries"`
	Worker               string   `json:"worker,omitempty"`
	ErrorMessage         string   `json:"errorMessage,omitempty"`
	ErrorCode            string   `json:"errorCode,omitempty"`
}

// ── Incident ──────────────────────────────────────────────────────────────────

type IncidentState string

const (
	IncidentActive   IncidentState = "ACTIVE"
	IncidentResolved IncidentState = "RESOLVED"
)

type Incident struct {
	IncidentKey          int64         `json:"incidentKey"`
	ProcessInstanceKey   int64         `json:"processInstanceKey"`
	ProcessDefinitionKey int64         `json:"processDefinitionKey"`
	BpmnProcessID        string        `json:"bpmnProcessId"`
	ElementID            string        `json:"elementId"`
	ElementInstanceKey   int64         `json:"elementInstanceKey"`
	JobKey               int64         `json:"jobKey,omitempty"`
	ErrorType            string        `json:"errorType"`
	ErrorMessage         string        `json:"errorMessage"`
	State                IncidentState `json:"state"`
	CreatedAt            time.Time     `json:"createdAt"`
	ResolvedAt           *time.Time    `json:"resolvedAt,omitempty"`
}

type IncidentListResponse struct {
	Items      []Incident `json:"items"`
	TotalCount int64      `json:"totalCount"`
}

// ── Variable ──────────────────────────────────────────────────────────────────

type Variable struct {
	VariableKey        int64  `json:"variableKey"`
	ProcessInstanceKey int64  `json:"processInstanceKey"`
	ScopeKey           int64  `json:"scopeKey"`
	Name               string `json:"name"`
	Value              string `json:"value"`
	Truncated          bool   `json:"truncated"`
}

// ── Deployed Process ──────────────────────────────────────────────────────────

type DeployedProcess struct {
	ProcessDefinitionKey int64  `json:"processDefinitionKey"`
	BpmnProcessID        string `json:"bpmnProcessId"`
	Version              int32  `json:"version"`
	ResourceName         string `json:"resourceName"`
	// Counts enriched from instance index
	ActiveCount    int64 `json:"activeCount"`
	IncidentCount  int64 `json:"incidentCount"`
	CompletedCount int64 `json:"completedCount"`
}

// ── Action Requests ───────────────────────────────────────────────────────────

type ResolveIncidentRequest struct {
	IncidentKey int64 `json:"incidentKey" binding:"required"`
}

type UpdateRetriesRequest struct {
	Retries int32 `json:"retries" binding:"required,min=1"`
}

type CancelInstanceRequest struct {
	ProcessInstanceKey int64 `json:"processInstanceKey" binding:"required"`
}

type SetVariablesRequest struct {
	Variables map[string]interface{} `json:"variables" binding:"required"`
	Local     bool                   `json:"local"`
}

type ModifyInstanceRequest struct {
	ActivateElementID           string `json:"activateElementId"`
	TerminateElementInstanceKey int64  `json:"terminateElementInstanceKey"`
}

// ── WebSocket Events ──────────────────────────────────────────────────────────

type WSEventType string

const (
	WSEventInstanceUpdated  WSEventType = "INSTANCE_UPDATED"
	WSEventIncidentCreated  WSEventType = "INCIDENT_CREATED"
	WSEventIncidentResolved WSEventType = "INCIDENT_RESOLVED"
	WSEventJobFailed        WSEventType = "JOB_FAILED"
	WSEventJobCompleted     WSEventType = "JOB_COMPLETED"
)

type WSEvent struct {
	Type      WSEventType `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Payload   interface{} `json:"payload"`
}
