package blog

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

// MetricsExporter handles metrics export
type MetricsExporter struct {
	localTem *LocalTelemetryStorage
}

func (e *MetricsExporter) Temporality(_ metric.InstrumentKind) metricdata.Temporality {
	return metric.DefaultTemporalitySelector(metric.InstrumentKindCounter)
}

func (e *MetricsExporter) Aggregation(_ metric.InstrumentKind) metric.Aggregation {
	return metric.DefaultAggregationSelector(metric.InstrumentKindCounter)
}

func (e *MetricsExporter) ForceFlush(context.Context) error {
	return nil
}

// Export implements the metrics exporter interface
func (e *MetricsExporter) Export(_ context.Context, metrics *metricdata.ResourceMetrics) error {
	for _, scopeMetrics := range metrics.ScopeMetrics {
		for _, m := range scopeMetrics.Metrics {
			if m.Name == "articles.served" {
				if data, ok := m.Data.(metricdata.Sum[int64]); ok {
					for _, point := range data.DataPoints {
						e.localTem.articlesServed.Store(point.Value)
					}
				}
			}
		}
	}
	return nil
}

func (e *MetricsExporter) Shutdown(context.Context) error {
	return nil
}

// TracesExporter handles trace export
type TracesExporter struct {
	localTem *LocalTelemetryStorage
}

func (e *TracesExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) > 0 {
		stub := tracetest.SpanStubFromReadOnlySpan(spans[len(spans)-1])
		e.localTem.Write(stub)
	}
	return nil
}

func (e *TracesExporter) Shutdown(context.Context) error {
	return nil
}

func (bs *BlogServer) InstallExportPipeline(ctx context.Context) error {
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String("jake-blog"),
	)

	// Create separate exporters for traces and metrics
	metricsExporter := &MetricsExporter{localTem: bs.telem}
	tracesExporter := &TracesExporter{localTem: bs.telem}

	// Set up trace provider
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(
			sdktrace.NewSimpleSpanProcessor(tracesExporter),
		),
		sdktrace.WithResource(res),
	)

	// Set up meter provider
	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(
			metricsExporter,
			metric.WithInterval(time.Second*1),
		)),
	)

	// Set global providers
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)

	return nil
}
