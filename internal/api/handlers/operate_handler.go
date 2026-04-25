package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"camunda-workers/internal/models"
	"camunda-workers/internal/workers/operate/actions"
	"camunda-workers/internal/workers/operate/queries"
	"camunda-workers/internal/workers/operate/ws"
)

type OperateHandler struct {
	q   *queries.OperateQueryService
	a   *actions.OperateActionService
	hub *ws.Hub
}

func NewOperateHandler(
	q *queries.OperateQueryService,
	a *actions.OperateActionService,
	hub *ws.Hub,
) *OperateHandler {
	return &OperateHandler{q: q, a: a, hub: hub}
}

// RegisterRoutes wires all /operate/* endpoints.
//
//	rg := router.Group("/operate", authMiddleware)
//	h.RegisterRoutes(rg)
func (h *OperateHandler) RegisterRoutes(rg *gin.RouterGroup) {
	// Deployed processes
	rg.GET("/processes", h.ListProcesses)

	// Process instances
	rg.GET("/instances", h.ListInstances)
	rg.GET("/instances/:key", h.GetInstance)
	rg.POST("/instances/:key/cancel", h.CancelInstance)
	rg.POST("/instances/:key/modify", h.ModifyInstance)

	// Variables
	rg.GET("/instances/:key/variables", h.ListVariables)
	rg.POST("/instances/:key/variables/:scopeKey", h.SetVariables)

	// Jobs
	rg.GET("/instances/:key/jobs", h.ListJobs)
	rg.GET("/jobs/failed", h.ListFailedJobs)
	rg.POST("/jobs/:jobKey/retries", h.UpdateJobRetries)
	rg.POST("/jobs/:jobKey/timeout", h.UpdateJobTimeout)

	// Incidents
	rg.GET("/incidents", h.ListAllIncidents)
	rg.GET("/instances/:key/incidents", h.ListInstanceIncidents)
	rg.POST("/incidents/:key/resolve", h.ResolveIncident)

	// WebSocket live updates
	rg.GET("/ws", h.hub.ServeWS)
}

// ── Deployed Processes ────────────────────────────────────────────────────────

// ListProcesses GET /operate/processes
// Returns all deployed BPMN processes with instance counts.
func (h *OperateHandler) ListProcesses(c *gin.Context) {
	processes, err := h.q.ListDeployedProcesses(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Enrich each process with live instance counts
	for i, p := range processes {
		active, completed, withIncident, err := h.q.CountInstancesByProcess(
			c.Request.Context(), p.BpmnProcessID,
		)
		if err == nil {
			processes[i].ActiveCount = active
			processes[i].CompletedCount = completed
			processes[i].IncidentCount = withIncident
		}
	}

	c.JSON(http.StatusOK, gin.H{"items": processes, "totalCount": len(processes)})
}

// ── Process Instances ─────────────────────────────────────────────────────────

// ListInstances GET /operate/instances?bpmnProcessId=&state=&page=1&pageSize=20
func (h *OperateHandler) ListInstances(c *gin.Context) {
	var filter models.ProcessInstanceFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := h.q.ListProcessInstances(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// GetInstance GET /operate/instances/:key
func (h *OperateHandler) GetInstance(c *gin.Context) {
	key, err := parseKey(c, "key")
	if err != nil {
		return
	}
	instance, err := h.q.GetProcessInstance(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, instance)
}

// CancelInstance POST /operate/instances/:key/cancel
func (h *OperateHandler) CancelInstance(c *gin.Context) {
	key, err := parseKey(c, "key")
	if err != nil {
		return
	}
	if err := h.a.CancelProcessInstance(c.Request.Context(), key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.hub.Publish(models.WSEventInstanceUpdated, gin.H{
		"processInstanceKey": key,
		"state":              "CANCELED",
	})
	c.JSON(http.StatusOK, gin.H{"message": "instance cancelled"})
}

// ModifyInstance POST /operate/instances/:key/modify
// Body: { "activateElementId": "Task_B", "terminateElementInstanceKey": 9007 }
func (h *OperateHandler) ModifyInstance(c *gin.Context) {
	key, err := parseKey(c, "key")
	if err != nil {
		return
	}
	var req models.ModifyInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.a.ModifyProcessInstance(c.Request.Context(), req, key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "instance modified"})
}

// ── Variables ─────────────────────────────────────────────────────────────────

// ListVariables GET /operate/instances/:key/variables
func (h *OperateHandler) ListVariables(c *gin.Context) {
	key, err := parseKey(c, "key")
	if err != nil {
		return
	}
	vars, err := h.q.ListVariablesByInstance(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": vars})
}

// SetVariables POST /operate/instances/:key/variables/:scopeKey
// Body: { "variables": {"key": "value"}, "local": false }
func (h *OperateHandler) SetVariables(c *gin.Context) {
	scopeKey, err := parseKey(c, "scopeKey")
	if err != nil {
		return
	}
	var req models.SetVariablesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.a.SetVariables(c.Request.Context(), scopeKey, req.Variables, req.Local); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "variables updated"})
}

// ── Jobs ──────────────────────────────────────────────────────────────────────

// ListJobs GET /operate/instances/:key/jobs
func (h *OperateHandler) ListJobs(c *gin.Context) {
	key, err := parseKey(c, "key")
	if err != nil {
		return
	}
	jobs, err := h.q.ListJobsByInstance(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": jobs})
}

// ListFailedJobs GET /operate/jobs/failed
func (h *OperateHandler) ListFailedJobs(c *gin.Context) {
	jobs, err := h.q.ListFailedJobs(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": jobs})
}

// UpdateJobRetries POST /operate/jobs/:jobKey/retries
// Body: { "retries": 3 }
func (h *OperateHandler) UpdateJobRetries(c *gin.Context) {
	jobKey, err := parseKey(c, "jobKey")
	if err != nil {
		return
	}
	var req models.UpdateRetriesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.a.UpdateJobRetries(c.Request.Context(), jobKey, req.Retries); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "retries updated"})
}

// UpdateJobTimeout POST /operate/jobs/:jobKey/timeout
// Body: { "timeoutMs": 30000 }
func (h *OperateHandler) UpdateJobTimeout(c *gin.Context) {
	jobKey, err := parseKey(c, "jobKey")
	if err != nil {
		return
	}
	var body struct {
		TimeoutMs int64 `json:"timeoutMs" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.a.UpdateJobTimeout(c.Request.Context(), jobKey, body.TimeoutMs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "timeout updated"})
}

// ── Incidents ─────────────────────────────────────────────────────────────────

// ListAllIncidents GET /operate/incidents  — all active incidents
func (h *OperateHandler) ListAllIncidents(c *gin.Context) {
	resp, err := h.q.ListActiveIncidents(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ListInstanceIncidents GET /operate/instances/:key/incidents
func (h *OperateHandler) ListInstanceIncidents(c *gin.Context) {
	key, err := parseKey(c, "key")
	if err != nil {
		return
	}
	resp, err := h.q.ListActiveIncidents(c.Request.Context(), &key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ResolveIncident POST /operate/incidents/:key/resolve
// Typical flow:
//  1. POST /operate/jobs/:jobKey/retries  { "retries": 3 }
//  2. POST /operate/incidents/:key/resolve
func (h *OperateHandler) ResolveIncident(c *gin.Context) {
	key, err := parseKey(c, "key")
	if err != nil {
		return
	}

	// Fetch incident to get its job key (needed for retries update)
	incident, err := h.q.GetIncident(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// If it has a job, reset retries to 1 automatically before resolving
	if incident.JobKey != 0 {
		_ = h.a.UpdateJobRetries(c.Request.Context(), incident.JobKey, 1)
	}

	if err := h.a.ResolveIncident(c.Request.Context(), key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.hub.Publish(models.WSEventIncidentResolved, gin.H{"incidentKey": key})
	c.JSON(http.StatusOK, gin.H{"message": "incident resolved"})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func parseKey(c *gin.Context, param string) (int64, error) {
	v, err := strconv.ParseInt(c.Param(param), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + param})
	}
	return v, err
}
