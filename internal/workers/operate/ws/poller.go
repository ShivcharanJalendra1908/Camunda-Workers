package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/elastic/go-elasticsearch/v8"

	"camunda-workers/internal/models"
)

// Poller polls Elasticsearch for new Zeebe exporter records
// and broadcasts changes to all connected WebSocket clients.
// Run this as a goroutine alongside your server.
type Poller struct {
	es       *elasticsearch.Client
	hub      *Hub
	interval time.Duration
	// tracks last seen incident/instance timestamps to avoid re-broadcasting
	lastIncidentPoll time.Time
	lastInstancePoll time.Time
}

func NewPoller(es *elasticsearch.Client, hub *Hub, interval time.Duration) *Poller {
	now := time.Now().UTC()
	return &Poller{
		es:               es,
		hub:              hub,
		interval:         interval,
		lastIncidentPoll: now,
		lastInstancePoll: now,
	}
}

// Run starts the polling loop. Cancel ctx to stop.
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollNewIncidents(ctx)
			p.pollInstanceChanges(ctx)
		}
	}
}

func (p *Poller) pollNewIncidents(ctx context.Context) {
	query := map[string]interface{}{
		"size": 20,
		"sort": []map[string]interface{}{
			{"createdAt": map[string]interface{}{"order": "asc"}},
		},
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"term": map[string]interface{}{"state": "ACTIVE"}},
					{"range": map[string]interface{}{
						"createdAt": map[string]interface{}{
							"gt": p.lastIncidentPoll.Format(time.RFC3339Nano),
						},
					}},
				},
			},
		},
	}

	body, _ := json.Marshal(query)
	res, err := p.es.Search(
		p.es.Search.WithContext(ctx),
		p.es.Search.WithIndex("zeebe-incidents"),
		p.es.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil || res.IsError() {
		return
	}
	defer res.Body.Close()

	var esResp struct {
		Hits struct {
			Hits []struct {
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&esResp); err != nil {
		return
	}

	for _, hit := range esResp.Hits.Hits {
		var inc models.Incident
		if err := json.Unmarshal(hit.Source, &inc); err == nil {
			p.hub.Publish(models.WSEventIncidentCreated, inc)
			if inc.CreatedAt.After(p.lastIncidentPoll) {
				p.lastIncidentPoll = inc.CreatedAt
			}
		}
	}
}

func (p *Poller) pollInstanceChanges(ctx context.Context) {
	query := map[string]interface{}{
		"size": 50,
		"sort": []map[string]interface{}{
			{"startTime": map[string]interface{}{"order": "asc"}},
		},
		"query": map[string]interface{}{
			"range": map[string]interface{}{
				"startTime": map[string]interface{}{
					"gt": p.lastInstancePoll.Format(time.RFC3339Nano),
				},
			},
		},
	}

	body, _ := json.Marshal(query)
	res, err := p.es.Search(
		p.es.Search.WithContext(ctx),
		p.es.Search.WithIndex("zeebe-process-instances"),
		p.es.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil || res.IsError() {
		return
	}
	defer res.Body.Close()

	var esResp struct {
		Hits struct {
			Hits []struct {
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&esResp); err != nil {
		return
	}

	for _, hit := range esResp.Hits.Hits {
		var pi models.ProcessInstance
		if err := json.Unmarshal(hit.Source, &pi); err == nil {
			p.hub.Publish(models.WSEventInstanceUpdated, pi)
			if pi.StartTime.After(p.lastInstancePoll) {
				p.lastInstancePoll = pi.StartTime
			}
		}
	}
}
