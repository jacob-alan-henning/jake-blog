package blog

import (
	"context"
	"encoding/json"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type LocalTelemetryStorage struct {
	latestSpan     tracetest.SpanStub
	articlesServed atomic.Int64
	numGoRo        atomic.Int64
	spanMu         sync.RWMutex
	spanChan       chan tracetest.SpanStub
}

func NewLocalTelemetryStorage() *LocalTelemetryStorage {
	storage := &LocalTelemetryStorage{
		spanChan: make(chan tracetest.SpanStub, 10),
	}
	return storage
}

func (lts *LocalTelemetryStorage) Start(ctx context.Context) error {
	go func() {
		go runtimeMetricLoop(ctx)
		for {
			select {
			case span := <-lts.spanChan:
				lts.spanMu.Lock()
				lts.latestSpan = span
				lts.spanMu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

func runtimeMetricLoop(ctx context.Context) {
	meter := otel.GetMeterProvider().Meter("jake-blog")

	goRunNum, err := meter.Int64Gauge("goroutine.count",
		metric.WithDescription("number of goroutines"),
		metric.WithUnit("goroutines"))
	if err != nil {
		log.Printf("failed to initialize runtime metrics %v", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
			goRunNum.Record(ctx, int64(runtime.NumGoroutine()))
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (lts *LocalTelemetryStorage) Write(span tracetest.SpanStub) {
	select {
	case lts.spanChan <- span:
	default:
		log.Printf("dropping span %s due to full bufer", span.SpanContext.SpanID().String())
	}
}

func (lts *LocalTelemetryStorage) GetLastSpanJSON() (string, error) {
	lts.spanMu.RLock()
	data, err := json.Marshal(lts.latestSpan)
	lts.spanMu.RUnlock()
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (lts *LocalTelemetryStorage) GetArticlesServed() int64 {
	return lts.articlesServed.Load()
}

func (lts *LocalTelemetryStorage) GetGoRoutineCount() int64 {
	return lts.numGoRo.Load()
}
