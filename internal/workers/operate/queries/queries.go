package queries

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/elastic/go-elasticsearch/v8"

	"camunda-workers/internal/models"
)

const (
	IndexProcessInstances = "zeebe-process-instances"
	IndexJobs             = "zeebe-jobs"
	IndexIncidents        = "zeebe-incidents"
	IndexVariables        = "zeebe-variables"
	IndexDeployments      = "zeebe-deployments"
)

type OperateQueryService struct {
	es *elasticsearch.Client
}

func NewOperateQueryService(es *elasticsearch.Client) *OperateQueryService {
	return &OperateQueryService{es: es}
}

// ── Process Instances ─────────────────────────────────────────────────────────

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
	from := (page - 1) * size

	must := []map[string]interface{}{}

	if filter.BpmnProcessID != "" {
		must = append(must, map[string]interface{}{
			"term": map[string]interface{}{"bpmnProcessId": filter.BpmnProcessID},
		})
	}
	if filter.State != "" {
		must = append(must, map[string]interface{}{
			"term": map[string]interface{}{"state": string(filter.State)},
		})
	}
	if filter.StartTimeFrom != nil || filter.StartTimeTo != nil {
		rangeQ := map[string]interface{}{}
		if filter.StartTimeFrom != nil {
			rangeQ["gte"] = filter.StartTimeFrom
		}
		if filter.StartTimeTo != nil {
			rangeQ["lte"] = filter.StartTimeTo
		}
		must = append(must, map[string]interface{}{
			"range": map[string]interface{}{"startTime": rangeQ},
		})
	}

	query := map[string]interface{}{
		"from": from,
		"size": size,
		"sort": []map[string]interface{}{
			{"startTime": map[string]interface{}{"order": "desc"}},
		},
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"must": must},
		},
	}

	return s.searchProcessInstances(ctx, query, page, size)
}

func (s *OperateQueryService) GetProcessInstance(
	ctx context.Context,
	instanceKey int64,
) (*models.ProcessInstance, error) {
	query := map[string]interface{}{
		"size": 1,
		"query": map[string]interface{}{
			"term": map[string]interface{}{"processInstanceKey": instanceKey},
		},
	}
	resp, err := s.searchProcessInstances(ctx, query, 1, 1)
	if err != nil {
		return nil, err
	}
	if len(resp.Items) == 0 {
		return nil, fmt.Errorf("process instance %d not found", instanceKey)
	}
	return &resp.Items[0], nil
}

// CountInstancesByProcess returns active/completed/incident counts per bpmnProcessId.
// Used to enrich the deployed process list.
func (s *OperateQueryService) CountInstancesByProcess(
	ctx context.Context,
	bpmnProcessID string,
) (active, completed, withIncident int64, err error) {
	query := map[string]interface{}{
		"size": 0,
		"query": map[string]interface{}{
			"term": map[string]interface{}{"bpmnProcessId": bpmnProcessID},
		},
		"aggs": map[string]interface{}{
			"by_state": map[string]interface{}{
				"terms": map[string]interface{}{"field": "state"},
			},
			"has_incident": map[string]interface{}{
				"filter": map[string]interface{}{
					"term": map[string]interface{}{"hasIncident": true},
				},
			},
		},
	}

	body, _ := json.Marshal(query)
	res, esErr := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(IndexProcessInstances),
		s.es.Search.WithBody(bytes.NewReader(body)),
	)
	if esErr != nil {
		return 0, 0, 0, fmt.Errorf("count instances: %w", esErr)
	}
	defer res.Body.Close()

	var result struct {
		Aggregations struct {
			ByState struct {
				Buckets []struct {
					Key      string `json:"key"`
					DocCount int64  `json:"doc_count"`
				} `json:"buckets"`
			} `json:"by_state"`
			HasIncident struct {
				DocCount int64 `json:"doc_count"`
			} `json:"has_incident"`
		} `json:"aggregations"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return 0, 0, 0, fmt.Errorf("decode count response: %w", err)
	}

	for _, b := range result.Aggregations.ByState.Buckets {
		switch b.Key {
		case "ACTIVE":
			active = b.DocCount
		case "COMPLETED":
			completed = b.DocCount
		}
	}
	withIncident = result.Aggregations.HasIncident.DocCount
	return
}

// ── Jobs ──────────────────────────────────────────────────────────────────────

func (s *OperateQueryService) ListJobsByInstance(
	ctx context.Context,
	instanceKey int64,
) ([]models.Job, error) {
	query := map[string]interface{}{
		"size": 100,
		"sort": []map[string]interface{}{
			{"_score": map[string]interface{}{"order": "desc"}},
		},
		"query": map[string]interface{}{
			"term": map[string]interface{}{"processInstanceKey": instanceKey},
		},
	}
	return s.searchJobs(ctx, query)
}

func (s *OperateQueryService) ListFailedJobs(
	ctx context.Context,
) ([]models.Job, error) {
	query := map[string]interface{}{
		"size": 50,
		"sort": []map[string]interface{}{
			{"_score": map[string]interface{}{"order": "desc"}},
		},
		"query": map[string]interface{}{
			"term": map[string]interface{}{"state": "FAILED"},
		},
	}
	return s.searchJobs(ctx, query)
}

// ── Incidents ─────────────────────────────────────────────────────────────────

func (s *OperateQueryService) ListActiveIncidents(
	ctx context.Context,
	instanceKey *int64,
) (*models.IncidentListResponse, error) {
	must := []map[string]interface{}{
		{"term": map[string]interface{}{"state": "ACTIVE"}},
	}
	if instanceKey != nil {
		must = append(must, map[string]interface{}{
			"term": map[string]interface{}{"processInstanceKey": *instanceKey},
		})
	}
	query := map[string]interface{}{
		"size": 100,
		"sort": []map[string]interface{}{
			{"createdAt": map[string]interface{}{"order": "desc"}},
		},
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"must": must},
		},
	}
	return s.searchIncidents(ctx, query)
}

func (s *OperateQueryService) GetIncident(
	ctx context.Context,
	incidentKey int64,
) (*models.Incident, error) {
	query := map[string]interface{}{
		"size": 1,
		"query": map[string]interface{}{
			"term": map[string]interface{}{"incidentKey": incidentKey},
		},
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

// ── Variables ─────────────────────────────────────────────────────────────────

func (s *OperateQueryService) ListVariablesByInstance(
	ctx context.Context,
	instanceKey int64,
) ([]models.Variable, error) {
	query := map[string]interface{}{
		"size": 200,
		"query": map[string]interface{}{
			"term": map[string]interface{}{"processInstanceKey": instanceKey},
		},
	}

	body, _ := json.Marshal(query)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(IndexVariables),
		s.es.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("es search variables: %w", err)
	}
	defer res.Body.Close()

	var esResp esHitsResponse
	if err := json.NewDecoder(res.Body).Decode(&esResp); err != nil {
		return nil, fmt.Errorf("decode variables: %w", err)
	}

	vars := make([]models.Variable, 0, len(esResp.Hits.Hits))
	for _, hit := range esResp.Hits.Hits {
		var v models.Variable
		if err := json.Unmarshal(hit.Source, &v); err == nil {
			vars = append(vars, v)
		}
	}
	return vars, nil
}

// ── Deployed Processes ────────────────────────────────────────────────────────

func (s *OperateQueryService) ListDeployedProcesses(
	ctx context.Context,
) ([]models.DeployedProcess, error) {
	// Get latest version per bpmnProcessId using terms agg + top_hits
	query := map[string]interface{}{
		"size": 0,
		"aggs": map[string]interface{}{
			"by_process": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "bpmnProcessId",
					"size":  100,
				},
				"aggs": map[string]interface{}{
					"latest": map[string]interface{}{
						"top_hits": map[string]interface{}{
							"size": 1,
							"sort": []map[string]interface{}{
								{"version": map[string]interface{}{"order": "desc"}},
							},
						},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(query)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(IndexDeployments),
		s.es.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("es list deployed processes: %w", err)
	}
	defer res.Body.Close()

	var result struct {
		Aggregations struct {
			ByProcess struct {
				Buckets []struct {
					Key    string `json:"key"`
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
		return nil, fmt.Errorf("decode deployed processes: %w", err)
	}

	processes := make([]models.DeployedProcess, 0)
	for _, bucket := range result.Aggregations.ByProcess.Buckets {
		if len(bucket.Latest.Hits.Hits) == 0 {
			continue
		}
		var dp models.DeployedProcess
		if err := json.Unmarshal(bucket.Latest.Hits.Hits[0].Source, &dp); err == nil {
			processes = append(processes, dp)
		}
	}
	return processes, nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func (s *OperateQueryService) searchProcessInstances(
	ctx context.Context,
	query map[string]interface{},
	page, size int,
) (*models.ProcessInstanceListResponse, error) {
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
		var pi models.ProcessInstance
		if err := json.Unmarshal(hit.Source, &pi); err == nil {
			items = append(items, pi)
		}
	}
	return &models.ProcessInstanceListResponse{
		Items:      items,
		TotalCount: esResp.Hits.Total.Value,
		Page:       page,
		PageSize:   size,
	}, nil
}

func (s *OperateQueryService) searchJobs(
	ctx context.Context,
	query map[string]interface{},
) ([]models.Job, error) {
	body, _ := json.Marshal(query)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(IndexJobs),
		s.es.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("es search jobs: %w", err)
	}
	defer res.Body.Close()

	var esResp esHitsResponse
	if err := json.NewDecoder(res.Body).Decode(&esResp); err != nil {
		return nil, fmt.Errorf("decode jobs: %w", err)
	}

	jobs := make([]models.Job, 0, len(esResp.Hits.Hits))
	for _, hit := range esResp.Hits.Hits {
		var j models.Job
		if err := json.Unmarshal(hit.Source, &j); err == nil {
			jobs = append(jobs, j)
		}
	}
	return jobs, nil
}

func (s *OperateQueryService) searchIncidents(
	ctx context.Context,
	query map[string]interface{},
) (*models.IncidentListResponse, error) {
	body, _ := json.Marshal(query)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(IndexIncidents),
		s.es.Search.WithBody(bytes.NewReader(body)),
		s.es.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		return nil, fmt.Errorf("es search incidents: %w", err)
	}
	defer res.Body.Close()

	var esResp esHitsResponse
	if err := json.NewDecoder(res.Body).Decode(&esResp); err != nil {
		return nil, fmt.Errorf("decode incidents: %w", err)
	}

	incidents := make([]models.Incident, 0, len(esResp.Hits.Hits))
	for _, hit := range esResp.Hits.Hits {
		var inc models.Incident
		if err := json.Unmarshal(hit.Source, &inc); err == nil {
			incidents = append(incidents, inc)
		}
	}
	return &models.IncidentListResponse{
		Items:      incidents,
		TotalCount: esResp.Hits.Total.Value,
	}, nil
}

type esHitsResponse struct {
	Hits struct {
		Total struct {
			Value int64 `json:"value"`
		} `json:"total"`
		Hits []struct {
			Source json.RawMessage `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}
