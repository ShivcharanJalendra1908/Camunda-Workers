// internal/common/metrics/metrics.go
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	WorkerJobsCompleted = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "worker_jobs_completed_total",
			Help: "Total number of jobs completed by worker",
		},
		[]string{"task_type"},
	)

	WorkerJobsFailed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "worker_jobs_failed_total",
			Help: "Total number of jobs failed by worker",
		},
		[]string{"task_type", "error_code"},
	)

	WorkerJobDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "worker_job_duration_seconds",
			Help: "Duration of job processing in seconds",
		},
		[]string{"task_type"},
	)

	WorkerJobsActive = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "worker_jobs_active",
			Help: "Number of active jobs per worker",
		},
		[]string{"task_type"},
	)
)
