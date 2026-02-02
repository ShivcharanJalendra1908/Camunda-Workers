// internal/common/observability/tracing.go
package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
)

// TracingConfig holds configuration for distributed tracing
type TracingConfig struct {
	Enabled       bool
	ServiceName   string
	Endpoint      string
	Sampler       string
	Probability   float64
	Environment   string
	Version       string
	ExportTimeout time.Duration
}

// InitTracer initializes OpenTelemetry tracer with Jaeger exporter
func InitTracer(config TracingConfig) (trace.TracerProvider, func(context.Context) error, error) {
	if !config.Enabled {
		// Return noop tracer
		return trace.NewNoopTracerProvider(), func(context.Context) error { return nil }, nil
	}

	// Create Jaeger exporter
	exp, err := jaeger.New(jaeger.WithCollectorEndpoint(
		jaeger.WithEndpoint(config.Endpoint),
	))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Jaeger exporter: %w", err)
	}

	// Create resource with service information
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(config.ServiceName),
			semconv.ServiceVersion(config.Version),
			semconv.DeploymentEnvironment(config.Environment),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Configure sampler
	var sampler tracesdk.Sampler
	switch config.Sampler {
	case "always":
		sampler = tracesdk.AlwaysSample()
	case "never":
		sampler = tracesdk.NeverSample()
	case "probabilistic":
		sampler = tracesdk.TraceIDRatioBased(config.Probability)
	default:
		sampler = tracesdk.AlwaysSample()
	}

	// Create tracer provider
	tp := tracesdk.NewTracerProvider(
		tracesdk.WithBatcher(exp,
			tracesdk.WithMaxExportBatchSize(512),
			tracesdk.WithBatchTimeout(config.ExportTimeout),
		),
		tracesdk.WithResource(res),
		tracesdk.WithSampler(sampler),
	)

	// Set global tracer provider
	otel.SetTracerProvider(tp)

	// Set global propagator (for context propagation across services)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Cleanup function
	cleanup := func(ctx context.Context) error {
		return tp.Shutdown(ctx)
	}

	return tp, cleanup, nil
}

// ============================================================================
// HELPER FUNCTIONS FOR SPAN ATTRIBUTES
// ============================================================================

// AddHTTPAttributes adds HTTP-specific attributes to span
func AddHTTPAttributes(span trace.Span, method, path string, statusCode int) {
	span.SetAttributes(
		semconv.HTTPMethod(method),
		semconv.HTTPRoute(path),
		semconv.HTTPStatusCode(statusCode),
	)
}

// AddDatabaseAttributes adds database-specific attributes to span
func AddDatabaseAttributes(span trace.Span, dbSystem, table, operation string) {
	span.SetAttributes(
		attribute.String("db.system", dbSystem),
		attribute.String("db.table", table),
		attribute.String("db.operation", operation),
	)
}

// AddWorkerAttributes adds worker-specific attributes to span
func AddWorkerAttributes(span trace.Span, workerName, jobType string, jobKey int64) {
	span.SetAttributes(
		attribute.String("worker.name", workerName),
		attribute.String("worker.job_type", jobType),
		attribute.Int64("worker.job_key", jobKey),
	)
}

// AddWorkflowAttributes adds workflow-specific attributes to span
func AddWorkflowAttributes(span trace.Span, processID string, instanceKey int64) {
	span.SetAttributes(
		attribute.String("workflow.process_id", processID),
		attribute.Int64("workflow.instance_key", instanceKey),
	)
}

// AddSearchAttributes adds search-specific attributes to span
func AddSearchAttributes(span trace.Span, index, queryType string, resultCount int) {
	span.SetAttributes(
		attribute.String("search.index", index),
		attribute.String("search.query_type", queryType),
		attribute.Int("search.result_count", resultCount),
	)
}

// AddAPIRequestAttributes adds external API request attributes to span
func AddAPIRequestAttributes(span trace.Span, service, endpoint, method string, statusCode int) {
	span.SetAttributes(
		attribute.String("api.service", service),
		attribute.String("api.endpoint", endpoint),
		attribute.String("api.method", method),
		attribute.Int("api.status_code", statusCode),
	)
}

// RecordError records error in span
func RecordError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetAttributes(attribute.Bool("error", true))
}

// StartSpan creates a new span with common attributes
func StartSpan(ctx context.Context, tracer trace.Tracer, spanName string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracer.Start(ctx, spanName, trace.WithAttributes(attrs...))
}

// NewTracer creates a new tracer - यह function आपके main.go में missing था
func NewTracer(serviceName, endpoint string) (trace.TracerProvider, func(), error) {
	config := TracingConfig{
		Enabled:       true,
		ServiceName:   serviceName,
		Endpoint:      endpoint,
		Sampler:       "always",
		Probability:   1.0,
		Environment:   "development",
		Version:       "1.0.0",
		ExportTimeout: 30 * time.Second,
	}

	tracerProvider, cleanup, err := InitTracer(config)
	if err != nil {
		return nil, nil, err
	}

	cleanupFunc := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = cleanup(ctx)
	}

	return tracerProvider, cleanupFunc, nil
}
