package blog

import (
	"context"
	"encoding/json"
	"log"
	"math"
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
	heapAlloc      atomic.Int64
	stackAlloc     atomic.Int64
	spanMu         sync.RWMutex
	spanChan       chan tracetest.SpanStub
}

func NewLocalTelemetryStorage() *LocalTelemetryStorage {
	storage := &LocalTelemetryStorage{
		spanChan: make(chan tracetest.SpanStub, 10),
	}
	return storage
}

func (lts *LocalTelemetryStorage) UpdateMetricFromName(name string, val int64) {
	switch name {
	case "goroutine.count":
		lts.numGoRo.Store(val)
	case "articles.served":
		lts.articlesServed.Store(val)
	case "blog.heap.alloc.bytes":
		lts.heapAlloc.Store(val)
	}
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

	goHeapAlloc, err := meter.Int64Gauge("blog.heap.alloc.bytes",
		metric.WithDescription("bytes allocated to the heap by the blog"),
		metric.WithUnit("bytes"))
	if err != nil {
		log.Printf("failed to initialize runtime metrics %v", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)

			goRunNum.Record(ctx, int64(runtime.NumGoroutine()))

			heapAlloc := m.HeapAlloc
			var heapAllocInt64 int64

			if heapAlloc >= uint64(math.MaxInt64) {
				log.Print("gosec was right and i am an idiot")
				heapAllocInt64 = math.MaxInt64
			} else {
				heapAllocInt64 = int64(heapAlloc)
			}

			goHeapAlloc.Record(ctx, int64(heapAllocInt64))
			time.Sleep(5 * time.Second)
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
