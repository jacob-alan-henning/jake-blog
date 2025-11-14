package blog

import (
	"context"
	"math"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

type MetricsExporter struct {
	localTem *LocalTelemetryStorage
}

func (e *MetricsExporter) Temporality(kind metric.InstrumentKind) metricdata.Temporality {
	return metric.DefaultTemporalitySelector(kind)
}

func (e *MetricsExporter) Aggregation(kind metric.InstrumentKind) metric.Aggregation {
	return metric.DefaultAggregationSelector(kind)
}

func (e *MetricsExporter) ForceFlush(ctx context.Context) error {
	return nil
}

func (e *MetricsExporter) Export(_ context.Context, metrics *metricdata.ResourceMetrics) error {
	for _, scopeMetrics := range metrics.ScopeMetrics {
		for _, m := range scopeMetrics.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, point := range data.DataPoints {
					switch m.Name {
					case "goroutine.count":
						e.localTem.numGoRo.Store(point.Value)
					case "blog.heap.alloc.bytes":
						e.localTem.heapAlloc.Store(point.Value)
					case "blog.stack.alloc.bytes":
						e.localTem.stackAlloc.Store(point.Value)
					}
				}
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					switch m.Name {
					case "articles.served":
						attr, found := point.Attributes.Value(attribute.Key("article"))
						if found {
							arty := attr.AsString()
							e.localTem.validateArticleAttr(arty)
							e.localTem.servedCountPerArticle[arty].Store(point.Value)
						} else {
							e.localTem.articlesServed.Store(point.Value)
						}
					case "request.blocked":
						attr, found := point.Attributes.Value(attribute.Key("blocked"))
						if found {
							reason := attr.AsString()
							e.localTem.validateReqBlockedReason(reason)
							e.localTem.reqBlockedByReason[reason].Store(point.Value)
						} else {
							e.localTem.reqBlocked.Store(point.Value)
						}
					case "robotic.visitors":
						e.localTem.roboticVisitors.Store(point.Value)
					}
				}
			case metricdata.Histogram[float64]:
				switch m.Name {
				case "http.server.request.duration":
					point := data.DataPoints[len(data.DataPoints)-1]
					e.localTem.reqDurTotalCount.Store(safeUint64ToInt64(point.Count))
					for i, bval := range point.Bounds {
						if i < len(point.BucketCounts) {
							bvalMs := int(bval * 1000)
							bucket := e.localTem.reqDurBuckets[bvalMs]
							if bucket != nil {
								bucket.Store(safeUint64ToInt64(point.BucketCounts[i]))
								telemLogger.Debug().Msgf("bucket: %d count: %d", bvalMs, point.BucketCounts[i])
							} else {
								telemLogger.Warn().Msg("received point in req freq histogram with invalid bucket")
							}
						} else {
							telemLogger.Warn().Msgf("invalid bucket index received from datapoint: %f", bval)
						}
					}
					e.localTem.UpdateServerFreqHistogram()
				}
			}
		}
	}
	return nil
}

func (e *MetricsExporter) Shutdown(context.Context) error {
	return nil
}

func safeUint64ToInt64(val uint64) int64 {
	if val > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(val)
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
		attribute.String("env", bs.bm.Config.Env),
	)

	tracesExporter := &TracesExporter{localTem: bs.telem}
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(
			sdktrace.NewSimpleSpanProcessor(tracesExporter),
		),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tracerProvider)

	metricsExporter := &MetricsExporter{localTem: bs.telem}
	reader := metric.NewPeriodicReader(
		metricsExporter,
		metric.WithInterval(time.Second*5),
	)

	if bs.bm.Config.ExportMetrics {
		vectorExporter, err := otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpoint(bs.bm.Config.MetricOTLP),
			otlpmetricgrpc.WithInsecure(),
		)
		if err != nil {
			telemLogger.Fatal().Msgf("failed to start vector metric exporter: %v", err)
		}
		vectorReader := metric.NewPeriodicReader(
			vectorExporter,
			metric.WithInterval(time.Second*10),
		)
		meterProvider := metric.NewMeterProvider(
			metric.WithResource(res),
			metric.WithReader(reader),
			metric.WithReader(vectorReader),
		)

		otel.SetMeterProvider(meterProvider)
	} else {
		meterProvider := metric.NewMeterProvider(
			metric.WithResource(res),
			metric.WithReader(reader),
		)

		otel.SetMeterProvider(meterProvider)
	}

	return nil
}
