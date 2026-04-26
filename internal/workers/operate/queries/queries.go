package queries

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/elastic/go-elasticsearch/v8"

	"camunda-workers/internal/models"
)

const (
	IndexProcessInstances = "zeebe-record_process-instance_*"
	IndexJobs             = "zeebe-record_job_*"
	IndexIncidents        = "zeebe-record_incident_*"
	IndexVariables        = "zeebe-record_variable_*"
	IndexDeployments      = "zeebe-record_process_*"
)

type OperateQueryService struct {
	es *elasticsearch.Client
}

func NewOperateQueryService(es *elasticsearch.Client) *OperateQueryService {
	return &OperateQueryService{es: es}
}

func (s *OperateQueryService) ListProcessInstances(
	ctx context.Context,
	filter models.ProcessInstanceFilter,
) (*models.ProcessInstanceListResponse, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	size := filter.PageSize
	if size < 1 || size > 100 {
		size = 20
	}

	must := []map[string]interface{}{
		{"term": map[string]interface{}{"value.bpmnElementType": "PROCESS"}},
	}
	if filter.BpmnProcessID != "" {
		must = append(must, map[string]interface{}{
			"term": map[string]interface{}{"value.bpmnProcessId": filter.BpmnProcessID},
		})
	}

	query := map[string]interface{}{
		"size": size,
		"from": (page - 1) * size,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"must": must},
		},
		"sort": []map[string]interface{}{
			{"value.processInstanceKey": map[string]interface{}{"order": "desc"}},
			{"timestamp": map[string]interface{}{"order": "desc"}},
		},
		"collapse": map[string]interface{}{
			"field": "value.processInstanceKey",
			"inner_hits": map[string]interface{}{
				"name": "latest",
				"size": 1,
				"sort": []map[string]interface{}{
					{"timestamp": map[string]interface{}{"order": "desc"}},
				},
			},
		},
	}

	body, _ := json.Marshal(query)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(IndexProcessInstances),
		s.es.Search.WithBody(bytes.NewReader(body)),
		s.es.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		return nil, fmt.Errorf("es search process instances: %w", err)
	}
	defer res.Body.Close()

	var esResp esHitsResponse
	if err := json.NewDecoder(res.Body).Decode(&esResp); err != nil {
		return nil, fmt.Errorf("decode process instances: %w", err)
	}

	items := make([]models.ProcessInstance, 0, len(esResp.Hits.Hits))
	for _, hit := range esResp.Hits.Hits {
		// Use inner_hits latest record for correct state
		var rec zeebeProcessInstanceRecord
		if hit.InnerHits != nil {
			if latest, ok := hit.InnerHits["latest"]; ok && len(latest.Hits.Hits) > 0 {
				if err := json.Unmarshal(latest.Hits.Hits[0].Source, &rec); err == nil {
					items = append(items, rec.toModel())
					continue
				}
			}
		}
		if err := json.Unmarshal(hit.Source, &rec); err == nil {
			items = append(items, rec.toModel())
		}
	}

	if filter.State != "" {
		filtered := items[:0]
		for _, item := range items {
			if item.State == filter.State {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	return &models.ProcessInstanceListResponse{
		Items:      items,
		TotalCount: esResp.Hits.Total.Value,
		Page:       page,
		PageSize:   size,
	}, nil
}

func (s *OperateQueryService) GetProcessInstance(
	ctx context.Context,
	instanceKey int64,
) (*models.ProcessInstance, error) {
	query := map[string]interface{}{
		"size": 1,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"term": map[string]interface{}{"value.processInstanceKey": instanceKey}},
					{"term": map[string]interface{}{"value.bpmnElementType": "PROCESS"}},
				},
			},
		},
		"sort": []map[string]interface{}{
			{"timestamp": map[string]interface{}{"order": "desc"}},
		},
	}
	body, _ := json.Marshal(query)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(IndexProcessInstances),
		s.es.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("get process instance: %w", err)
	}
	defer res.Body.Close()

	var esResp esHitsResponse
	if err := json.NewDecoder(res.Body).Decode(&esResp); err != nil {
		return nil, err
	}
	if len(esResp.Hits.Hits) == 0 {
		return nil, fmt.Errorf("process instance %d not found", instanceKey)
	}
	var rec zeebeProcessInstanceRecord
	if err := json.Unmarshal(esResp.Hits.Hits[0].Source, &rec); err != nil {
		return nil, err
	}
	m := rec.toModel()
	return &m, nil
}

func (s *OperateQueryService) CountInstancesByProcess(
	ctx context.Context,
	bpmnProcessID string,
) (active, completed, withIncident int64, err error) {
	for _, intent := range []string{"ELEMENT_ACTIVATING", "ELEMENT_ACTIVATED"} {
		q := map[string]interface{}{
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"must": []map[string]interface{}{
						{"term": map[string]interface{}{"value.bpmnProcessId": bpmnProcessID}},
						{"term": map[string]interface{}{"value.bpmnElementType": "PROCESS"}},
						{"term": map[string]interface{}{"intent": intent}},
					},
				},
			},
		}
		body, _ := json.Marshal(q)
		res, e := s.es.Count(s.es.Count.WithContext(ctx), s.es.Count.WithIndex(IndexProcessInstances), s.es.Count.WithBody(bytes.NewReader(body)))
		if e == nil {
			var cr struct {
				Count int64 `json:"count"`
			}
			json.NewDecoder(res.Body).Decode(&cr)
			res.Body.Close()
			active += cr.Count
		}
	}

	cq := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"term": map[string]interface{}{"value.bpmnProcessId": bpmnProcessID}},
					{"term": map[string]interface{}{"value.bpmnElementType": "PROCESS"}},
					{"term": map[string]interface{}{"intent": "ELEMENT_COMPLETED"}},
				},
			},
		},
	}
	body, _ := json.Marshal(cq)
	res, e := s.es.Count(s.es.Count.WithContext(ctx), s.es.Count.WithIndex(IndexProcessInstances), s.es.Count.WithBody(bytes.NewReader(body)))
	if e == nil {
		var cr struct {
			Count int64 `json:"count"`
		}
		json.NewDecoder(res.Body).Decode(&cr)
		res.Body.Close()
		completed = cr.Count
	}

	iq := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"term": map[string]interface{}{"value.bpmnProcessId": bpmnProcessID}},
					{"term": map[string]interface{}{"intent": "CREATED"}},
				},
			},
		},
	}
	body, _ = json.Marshal(iq)
	res, e = s.es.Count(s.es.Count.WithContext(ctx), s.es.Count.WithIndex(IndexIncidents), s.es.Count.WithBody(bytes.NewReader(body)))
	if e == nil {
		var cr struct {
			Count int64 `json:"count"`
		}
		json.NewDecoder(res.Body).Decode(&cr)
		res.Body.Close()
		withIncident = cr.Count
	}
	return
}

func (s *OperateQueryService) ListDeployedProcesses(ctx context.Context) ([]models.DeployedProcess, error) {
	query := map[string]interface{}{
		"size": 0,
		"query": map[string]interface{}{
			"term": map[string]interface{}{"valueType": "PROCESS"},
		},
		"aggs": map[string]interface{}{
			"by_process": map[string]interface{}{
				"terms": map[string]interface{}{"field": "value.bpmnProcessId", "size": 100},
				"aggs": map[string]interface{}{
					"latest": map[string]interface{}{
						"top_hits": map[string]interface{}{
							"size": 1,
							"sort": []map[string]interface{}{
								{"value.version": map[string]interface{}{"order": "desc"}},
							},
						},
					},
				},
			},
		},
	}
	body, _ := json.Marshal(query)
	res, err := s.es.Search(s.es.Search.WithContext(ctx), s.es.Search.WithIndex(IndexDeployments), s.es.Search.WithBody(bytes.NewReader(body)))
	if err != nil {
		return nil, fmt.Errorf("list deployed processes: %w", err)
	}
	defer res.Body.Close()

	var result struct {
		Aggregations struct {
			ByProcess struct {
				Buckets []struct {
					Latest struct {
						Hits struct {
							Hits []struct {
								Source json.RawMessage `json:"_source"`
							} `json:"hits"`
						} `json:"hits"`
					} `json:"latest"`
				} `json:"buckets"`
			} `json:"by_process"`
		} `json:"aggregations"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, err
	}

	processes := make([]models.DeployedProcess, 0)
	for _, bucket := range result.Aggregations.ByProcess.Buckets {
		if len(bucket.Latest.Hits.Hits) == 0 {
			continue
		}
		var rec struct {
			Value struct {
				BpmnProcessID        string `json:"bpmnProcessId"`
				ProcessDefinitionKey int64  `json:"processDefinitionKey"`
				Version              int32  `json:"version"`
				ResourceName         string `json:"resourceName"`
			} `json:"value"`
		}
		if err := json.Unmarshal(bucket.Latest.Hits.Hits[0].Source, &rec); err == nil {
			processes = append(processes, models.DeployedProcess{
				ProcessDefinitionKey: rec.Value.ProcessDefinitionKey,
				BpmnProcessID:        rec.Value.BpmnProcessID,
				Version:              rec.Value.Version,
				ResourceName:         rec.Value.ResourceName,
			})
		}
	}
	return processes, nil
}

func (s *OperateQueryService) ListJobsByInstance(ctx context.Context, instanceKey int64) ([]models.Job, error) {
	query := map[string]interface{}{
		"size": 100,
		"query": map[string]interface{}{
			"term": map[string]interface{}{"value.processInstanceKey": instanceKey},
		},
		"sort":     []map[string]interface{}{{"timestamp": map[string]interface{}{"order": "desc"}}},
		"collapse": map[string]interface{}{"field": "key"},
	}
	return s.searchJobs(ctx, query)
}

func (s *OperateQueryService) ListFailedJobs(ctx context.Context) ([]models.Job, error) {
	query := map[string]interface{}{
		"size":  50,
		"query": map[string]interface{}{"term": map[string]interface{}{"intent": "FAILED"}},
		"sort":  []map[string]interface{}{{"timestamp": map[string]interface{}{"order": "desc"}}},
	}
	return s.searchJobs(ctx, query)
}

func (s *OperateQueryService) ListActiveIncidents(ctx context.Context, instanceKey *int64) (*models.IncidentListResponse, error) {
	must := []map[string]interface{}{
		{"term": map[string]interface{}{"intent": "CREATED"}},
	}
	if instanceKey != nil {
		must = append(must, map[string]interface{}{"term": map[string]interface{}{"value.processInstanceKey": *instanceKey}})
	}
	query := map[string]interface{}{
		"size":  100,
		"sort":  []map[string]interface{}{{"timestamp": map[string]interface{}{"order": "desc"}}},
		"query": map[string]interface{}{"bool": map[string]interface{}{"must": must}},
	}
	return s.searchIncidents(ctx, query)
}

func (s *OperateQueryService) GetIncident(ctx context.Context, incidentKey int64) (*models.Incident, error) {
	query := map[string]interface{}{
		"size":  1,
		"query": map[string]interface{}{"term": map[string]interface{}{"key": incidentKey}},
		"sort":  []map[string]interface{}{{"timestamp": map[string]interface{}{"order": "desc"}}},
	}
	resp, err := s.searchIncidents(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(resp.Items) == 0 {
		return nil, fmt.Errorf("incident %d not found", incidentKey)
	}
	return &resp.Items[0], nil
}

func (s *OperateQueryService) ListVariablesByInstance(ctx context.Context, instanceKey int64) ([]models.Variable, error) {
	query := map[string]interface{}{
		"size":     200,
		"query":    map[string]interface{}{"term": map[string]interface{}{"value.processInstanceKey": instanceKey}},
		"collapse": map[string]interface{}{"field": "value.name"},
		"sort":     []map[string]interface{}{{"timestamp": map[string]interface{}{"order": "desc"}}},
	}
	body, _ := json.Marshal(query)
	res, err := s.es.Search(s.es.Search.WithContext(ctx), s.es.Search.WithIndex(IndexVariables), s.es.Search.WithBody(bytes.NewReader(body)))
	if err != nil {
		return nil, fmt.Errorf("es search variables: %w", err)
	}
	defer res.Body.Close()

	var esResp esHitsResponse
	if err := json.NewDecoder(res.Body).Decode(&esResp); err != nil {
		return nil, err
	}
	vars := make([]models.Variable, 0)
	for _, hit := range esResp.Hits.Hits {
		var rec struct {
			Key   int64 `json:"key"`
			Value struct {
				ProcessInstanceKey int64  `json:"processInstanceKey"`
				ScopeKey           int64  `json:"scopeKey"`
				Name               string `json:"name"`
				Value              string `json:"value"`
				Truncated          bool   `json:"truncated"`
			} `json:"value"`
		}
		if err := json.Unmarshal(hit.Source, &rec); err == nil {
			vars = append(vars, models.Variable{
				VariableKey:        rec.Key,
				ProcessInstanceKey: rec.Value.ProcessInstanceKey,
				ScopeKey:           rec.Value.ScopeKey,
				Name:               rec.Value.Name,
				Value:              rec.Value.Value,
				Truncated:          rec.Value.Truncated,
			})
		}
	}
	return vars, nil
}

func (s *OperateQueryService) searchJobs(ctx context.Context, query map[string]interface{}) ([]models.Job, error) {
	body, _ := json.Marshal(query)
	res, err := s.es.Search(s.es.Search.WithContext(ctx), s.es.Search.WithIndex(IndexJobs), s.es.Search.WithBody(bytes.NewReader(body)))
	if err != nil {
		return nil, fmt.Errorf("es search jobs: %w", err)
	}
	defer res.Body.Close()

	var esResp esHitsResponse
	if err := json.NewDecoder(res.Body).Decode(&esResp); err != nil {
		return nil, err
	}
	jobs := make([]models.Job, 0)
	for _, hit := range esResp.Hits.Hits {
		var rec struct {
			Key    int64  `json:"key"`
			Intent string `json:"intent"`
			Value  struct {
				ProcessInstanceKey   int64  `json:"processInstanceKey"`
				ProcessDefinitionKey int64  `json:"processDefinitionKey"`
				BpmnProcessID        string `json:"bpmnProcessId"`
				ElementID            string `json:"elementId"`
				Type                 string `json:"type"`
				Retries              int32  `json:"retries"`
				Worker               string `json:"worker"`
				ErrorMessage         string `json:"errorMessage"`
				ErrorCode            string `json:"errorCode"`
			} `json:"value"`
		}
		if err := json.Unmarshal(hit.Source, &rec); err == nil {
			jobs = append(jobs, models.Job{
				JobKey:               rec.Key,
				ProcessInstanceKey:   rec.Value.ProcessInstanceKey,
				ProcessDefinitionKey: rec.Value.ProcessDefinitionKey,
				BpmnProcessID:        rec.Value.BpmnProcessID,
				ElementID:            rec.Value.ElementID,
				JobType:              rec.Value.Type,
				State:                intentToJobState(rec.Intent),
				Retries:              rec.Value.Retries,
				Worker:               rec.Value.Worker,
				ErrorMessage:         rec.Value.ErrorMessage,
				ErrorCode:            rec.Value.ErrorCode,
			})
		}
	}
	return jobs, nil
}

func (s *OperateQueryService) searchIncidents(ctx context.Context, query map[string]interface{}) (*models.IncidentListResponse, error) {
	body, _ := json.Marshal(query)
	res, err := s.es.Search(s.es.Search.WithContext(ctx), s.es.Search.WithIndex(IndexIncidents), s.es.Search.WithBody(bytes.NewReader(body)), s.es.Search.WithTrackTotalHits(true))
	if err != nil {
		return nil, fmt.Errorf("es search incidents: %w", err)
	}
	defer res.Body.Close()

	var esResp esHitsResponse
	if err := json.NewDecoder(res.Body).Decode(&esResp); err != nil {
		return nil, err
	}
	incidents := make([]models.Incident, 0)
	for _, hit := range esResp.Hits.Hits {
		var rec struct {
			Key       int64  `json:"key"`
			Intent    string `json:"intent"`
			Timestamp int64  `json:"timestamp"`
			Value     struct {
				ProcessInstanceKey   int64  `json:"processInstanceKey"`
				ProcessDefinitionKey int64  `json:"processDefinitionKey"`
				BpmnProcessID        string `json:"bpmnProcessId"`
				ElementID            string `json:"elementId"`
				ElementInstanceKey   int64  `json:"elementInstanceKey"`
				JobKey               int64  `json:"jobKey"`
				ErrorType            string `json:"errorType"`
				ErrorMessage         string `json:"errorMessage"`
			} `json:"value"`
		}
		if err := json.Unmarshal(hit.Source, &rec); err == nil {
			ts := time.UnixMilli(rec.Timestamp).UTC()
			incidents = append(incidents, models.Incident{
				IncidentKey:          rec.Key,
				ProcessInstanceKey:   rec.Value.ProcessInstanceKey,
				ProcessDefinitionKey: rec.Value.ProcessDefinitionKey,
				BpmnProcessID:        rec.Value.BpmnProcessID,
				ElementID:            rec.Value.ElementID,
				ElementInstanceKey:   rec.Value.ElementInstanceKey,
				JobKey:               rec.Value.JobKey,
				ErrorType:            rec.Value.ErrorType,
				ErrorMessage:         rec.Value.ErrorMessage,
				State:                intentToIncidentState(rec.Intent),
				CreatedAt:            ts,
			})
		}
	}
	return &models.IncidentListResponse{Items: incidents, TotalCount: esResp.Hits.Total.Value}, nil
}

type zeebeProcessInstanceRecord struct {
	Key       int64  `json:"key"`
	Intent    string `json:"intent"`
	Timestamp int64  `json:"timestamp"`
	Value     struct {
		ProcessInstanceKey   int64  `json:"processInstanceKey"`
		ProcessDefinitionKey int64  `json:"processDefinitionKey"`
		BpmnProcessID        string `json:"bpmnProcessId"`
		Version              int32  `json:"version"`
		TenantID             string `json:"tenantId"`
		ParentInstanceKey    int64  `json:"parentProcessInstanceKey"`
	} `json:"value"`
}

func (r *zeebeProcessInstanceRecord) toModel() models.ProcessInstance {
	ts := time.UnixMilli(r.Timestamp).UTC()
	pi := models.ProcessInstance{
		ProcessInstanceKey:   r.Value.ProcessInstanceKey,
		ProcessDefinitionKey: r.Value.ProcessDefinitionKey,
		BpmnProcessID:        r.Value.BpmnProcessID,
		Version:              r.Value.Version,
		State:                intentToInstanceState(r.Intent),
		StartTime:            ts,
		TenantID:             r.Value.TenantID,
	}
	if r.Value.ParentInstanceKey > 0 {
		pi.ParentInstanceKey = &r.Value.ParentInstanceKey
	}
	if r.Intent == "ELEMENT_COMPLETED" || r.Intent == "ELEMENT_TERMINATED" {
		pi.EndTime = &ts
	}
	return pi
}

func intentToInstanceState(intent string) models.ProcessInstanceState {
	switch intent {
	case "ELEMENT_COMPLETED":
		return models.ProcessInstanceCompleted
	case "ELEMENT_TERMINATED":
		return models.ProcessInstanceCanceled
	default:
		return models.ProcessInstanceActive
	}
}

func intentToJobState(intent string) models.JobState {
	switch intent {
	case "COMPLETED":
		return models.JobCompleted
	case "FAILED":
		return models.JobFailed
	case "ACTIVATED":
		return models.JobActivated
	default:
		return models.JobActivatable
	}
}

func intentToIncidentState(intent string) models.IncidentState {
	if intent == "RESOLVED" {
		return models.IncidentResolved
	}
	return models.IncidentActive
}

type esHitsResponse struct {
	Hits struct {
		Total struct {
			Value int64 `json:"value"`
		} `json:"total"`
		Hits []struct {
			Source    json.RawMessage        `json:"_source"`
			InnerHits map[string]esInnerHits `json:"inner_hits"`
		} `json:"hits"`
	} `json:"hits"`
}

type esInnerHits struct {
	Hits struct {
		Hits []struct {
			Source json.RawMessage `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

// ADD THESE METHODS to internal/workers/operate/queries/queries.go

// ElementInstance represents a BPMN element's execution state
// Used for token overlay on BPMN diagram
type ElementInstance struct {
	ElementID   string `json:"elementId"`
	ElementType string `json:"elementType"`
	State       string `json:"state"` // ACTIVE, COMPLETED, TERMINATED, INCIDENT
	InstanceKey int64  `json:"instanceKey"`
}

// GetProcessXML fetches the raw BPMN XML from Zeebe exporter records.
// Zeebe stores it in zeebe-record_process_* as value.resource (base64 encoded).
func (s *OperateQueryService) GetProcessXML(
	ctx context.Context,
	processDefinitionKey int64,
) (string, error) {
	query := map[string]interface{}{
		"size": 1,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"term": map[string]interface{}{"value.processDefinitionKey": processDefinitionKey}},
					{"term": map[string]interface{}{"valueType": "PROCESS"}},
				},
			},
		},
		"sort": []map[string]interface{}{
			{"timestamp": map[string]interface{}{"order": "desc"}},
		},
	}

	body, _ := json.Marshal(query)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(IndexDeployments),
		s.es.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return "", fmt.Errorf("get process xml: %w", err)
	}
	defer res.Body.Close()

	var esResp esHitsResponse
	if err := json.NewDecoder(res.Body).Decode(&esResp); err != nil {
		return "", err
	}
	if len(esResp.Hits.Hits) == 0 {
		return "", fmt.Errorf("process definition %d not found", processDefinitionKey)
	}

	// Zeebe stores resource as base64 in value.resource
	var rec struct {
		Value struct {
			Resource     string `json:"resource"` // base64 encoded BPMN XML
			ResourceName string `json:"resourceName"`
		} `json:"value"`
	}
	if err := json.Unmarshal(esResp.Hits.Hits[0].Source, &rec); err != nil {
		return "", err
	}

	// Decode base64 BPMN XML
	xmlBytes, err := base64.StdEncoding.DecodeString(rec.Value.Resource)
	if err != nil {
		// Maybe it's already plain XML
		return rec.Value.Resource, nil
	}
	return string(xmlBytes), nil
}

// GetElementInstances returns all element execution records for a process instance.
// Used to draw token overlays on BPMN diagram.
func (s *OperateQueryService) GetElementInstances(
	ctx context.Context,
	instanceKey int64,
) ([]ElementInstance, error) {
	query := map[string]interface{}{
		"size": 500,
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"value.processInstanceKey": instanceKey,
			},
		},
		"sort": []map[string]interface{}{
			{"timestamp": map[string]interface{}{"order": "asc"}},
		},
	}

	body, _ := json.Marshal(query)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(IndexProcessInstances),
		s.es.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("get element instances: %w", err)
	}
	defer res.Body.Close()

	var esResp esHitsResponse
	if err := json.NewDecoder(res.Body).Decode(&esResp); err != nil {
		return nil, err
	}

	// Track latest state per elementId
	elementStates := map[string]*ElementInstance{}

	for _, hit := range esResp.Hits.Hits {
		var rec struct {
			Key    int64  `json:"key"`
			Intent string `json:"intent"`
			Value  struct {
				ElementID       string `json:"elementId"`
				BpmnElementType string `json:"bpmnElementType"`
			} `json:"value"`
		}
		if err := json.Unmarshal(hit.Source, &rec); err != nil {
			continue
		}

		state := intentToElementState(rec.Intent)
		elementStates[rec.Value.ElementID] = &ElementInstance{
			ElementID:   rec.Value.ElementID,
			ElementType: rec.Value.BpmnElementType,
			State:       state,
			InstanceKey: rec.Key,
		}
	}

	// Also check incidents for this instance
	incidentElements := s.getIncidentElementIDs(ctx, instanceKey)
	for _, elemID := range incidentElements {
		if el, ok := elementStates[elemID]; ok {
			el.State = "INCIDENT"
		}
	}

	result := make([]ElementInstance, 0, len(elementStates))
	for _, el := range elementStates {
		result = append(result, *el)
	}
	return result, nil
}

func (s *OperateQueryService) getIncidentElementIDs(ctx context.Context, instanceKey int64) []string {
	query := map[string]interface{}{
		"size": 50,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"term": map[string]interface{}{"value.processInstanceKey": instanceKey}},
					{"term": map[string]interface{}{"intent": "CREATED"}},
				},
			},
		},
	}
	body, _ := json.Marshal(query)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(IndexIncidents),
		s.es.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil
	}
	defer res.Body.Close()

	var esResp esHitsResponse
	json.NewDecoder(res.Body).Decode(&esResp)

	ids := make([]string, 0)
	for _, hit := range esResp.Hits.Hits {
		var rec struct {
			Value struct {
				ElementID string `json:"elementId"`
			} `json:"value"`
		}
		if err := json.Unmarshal(hit.Source, &rec); err == nil {
			ids = append(ids, rec.Value.ElementID)
		}
	}
	return ids
}

func intentToElementState(intent string) string {
	switch intent {
	case "ELEMENT_COMPLETED":
		return "COMPLETED"
	case "ELEMENT_TERMINATED":
		return "TERMINATED"
	case "ELEMENT_ACTIVATING", "ELEMENT_ACTIVATED":
		return "ACTIVE"
	default:
		return "COMPLETED"
	}
}
