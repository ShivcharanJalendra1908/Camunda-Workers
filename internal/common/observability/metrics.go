package observability

import (
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

	return &Observability{
		meterProvider: provider,
		meter:         meter,
		jobCounter:    jobCounter,
		jobDuration:   jobDuration,
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
