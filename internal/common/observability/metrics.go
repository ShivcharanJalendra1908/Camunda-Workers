package observability

import (
	"camunda-workers/internal/common/errors"
	"context"
	"log"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
)

type Observability struct {
	meterProvider *metric.MeterProvider
	meter         otelmetric.Meter
	jobCounter    otelmetric.Int64Counter
	jobDuration   otelmetric.Float64Histogram

	// ✅ NEW: Error tracking metrics
	errorCounter    otelmetric.Int64Counter
	errorByCategory otelmetric.Int64Counter
	errorBySeverity otelmetric.Int64Counter
	retryCounter    otelmetric.Int64Counter
}

func (o *Observability) StartSpan(s string) any {
	panic("unimplemented")
}

func New(serviceName string) *Observability {
	exporter, err := prometheus.New()
	if err != nil {
		log.Printf("Failed to create Prometheus exporter: %v", err)
		return &Observability{}
	}

	provider := metric.NewMeterProvider(metric.WithReader(exporter))
	otel.SetMeterProvider(provider)

	meter := provider.Meter(serviceName)

	jobCounter, _ := meter.Int64Counter(
		"jobs.processed",
		otelmetric.WithDescription("Number of jobs processed"),
	)

	jobDuration, _ := meter.Float64Histogram(
		"jobs.duration",
		otelmetric.WithDescription("Job processing duration"),
		otelmetric.WithUnit("ms"),
	)

	// ✅ NEW: Error metrics
	errorCounter, _ := meter.Int64Counter(
		"errors.total",
		otelmetric.WithDescription("Total number of errors"),
	)

	errorByCategory, _ := meter.Int64Counter(
		"errors.by_category",
		otelmetric.WithDescription("Errors grouped by category"),
	)

	errorBySeverity, _ := meter.Int64Counter(
		"errors.by_severity",
		otelmetric.WithDescription("Errors grouped by severity"),
	)

	retryCounter, _ := meter.Int64Counter(
		"errors.retries",
		otelmetric.WithDescription("Number of job retries"),
	)

	return &Observability{
		meterProvider:   provider,
		meter:           meter,
		jobCounter:      jobCounter,
		jobDuration:     jobDuration,
		errorCounter:    errorCounter,
		errorByCategory: errorByCategory,
		errorBySeverity: errorBySeverity,
		retryCounter:    retryCounter,
	}
}

func (o *Observability) RecordJobProcessed(ctx context.Context, status string) {
	if o.jobCounter != nil {
		o.jobCounter.Add(ctx, 1, otelmetric.WithAttributes(
			attribute.String("status", status),
		))
	}
}

func (o *Observability) RecordJobDuration(ctx context.Context, duration time.Duration, status string) {
	if o.jobDuration != nil {
		o.jobDuration.Record(ctx, float64(duration.Milliseconds()), otelmetric.WithAttributes(
			attribute.String("status", status),
		))
	}
}

func (o *Observability) Shutdown() {
	if o.meterProvider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		o.meterProvider.Shutdown(ctx)
	}
}

// RecordError records an error occurrence with category and severity
func (o *Observability) RecordError(ctx context.Context, errorCode errors.ErrorCode, workerName string) {
	category := errors.GetErrorCategory(errorCode)

	if o.errorCounter != nil {
		o.errorCounter.Add(ctx, 1, otelmetric.WithAttributes(
			attribute.String("error_code", string(errorCode)),
			attribute.String("worker", workerName),
		))
	}

	if o.errorByCategory != nil {
		o.errorByCategory.Add(ctx, 1, otelmetric.WithAttributes(
			attribute.String("category", string(category)),
			attribute.String("worker", workerName),
		))
	}

	severity := getSeverityForMetrics(category)
	if o.errorBySeverity != nil {
		o.errorBySeverity.Add(ctx, 1, otelmetric.WithAttributes(
			attribute.String("severity", severity),
			attribute.String("worker", workerName),
		))
	}
}

// RecordRetry records a job retry attempt
func (o *Observability) RecordRetry(ctx context.Context, errorCode errors.ErrorCode, retryCount int, workerName string) {
	if o.retryCounter != nil {
		o.retryCounter.Add(ctx, 1, otelmetric.WithAttributes(
			attribute.String("error_code", string(errorCode)),
			attribute.Int("retry_count", retryCount),
			attribute.String("worker", workerName),
		))
	}
}

// Helper function for metrics severity
func getSeverityForMetrics(cat errors.ErrorCategory) string {
	switch cat {
	case errors.CategoryValidation, errors.CategoryClient:
		return "warn"
	case errors.CategoryTransient:
		return "error"
	case errors.CategoryDependency:
		return "high"
	case errors.CategoryPermanent:
		return "critical"
	default:
		return "error"
	}
}
