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
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type LocalTelemetryStorage struct {
	latestSpan     tracetest.SpanStub
	reqDur99       atomic.Int64
	reqDur95       atomic.Int64
	reqDur90       atomic.Int64
	reqDur50       atomic.Int64
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

// synchronous gauge/sum instruments
func (lts *LocalTelemetryStorage) UpdateMetricFromName(name string, val int64) {
	switch name {
	case "goroutine.count":
		lts.numGoRo.Store(val)
	case "articles.served":
		lts.articlesServed.Store(val)
	case "blog.heap.alloc.bytes":
		lts.heapAlloc.Store(val)
	case "blog.stack.alloc.bytes":
		lts.stackAlloc.Store(val)
	}
}

// store histogram metrics
func (lts *LocalTelemetryStorage) UpdateHistogramMetricFromName(name string, vals []metricdata.HistogramDataPoint[float64]) {
	switch name {
	case "http.server.duration":
		for _, val := range vals {
			p50, valid := lts.calculatePercentile(val, 0.5)
			if valid {
				lts.reqDur50.Store(int64(p50))
			}
			p90, valid := lts.calculatePercentile(val, 0.9)
			if valid {
				lts.reqDur90.Store(int64(p90))
			}
			p95, valid := lts.calculatePercentile(val, 0.95)
			if valid {
				lts.reqDur95.Store(int64(p95))
			}
			p99, valid := lts.calculatePercentile(val, 0.99)
			if valid {
				lts.reqDur99.Store(int64(p99))
			}
		}
	}
}

func (lts *LocalTelemetryStorage) calculatePercentile(histogramData metricdata.HistogramDataPoint[float64], percentile float64) (float64, bool) {
	if histogramData.Count == 0 {
		return 0, false
	}
	// figure out the number of points needed to "reach" a percentile
	targetCount := uint64(float64(histogramData.Count) * percentile)
	var runningCount uint64 = 0

	// iterate through the datapoints until we find the bucket that has the percentile
	// provide best guess on exact num
	for index, count := range histogramData.BucketCounts {
		runningCount += count

		if runningCount >= targetCount {
			if index == 0 {
				return histogramData.Bounds[0] / 2, true
			}

			if index < len(histogramData.Bounds) {
				lowerBound := histogramData.Bounds[index-1]
				upperBound := histogramData.Bounds[index]

				// Calculate how far into this bucket our percentile falls
				bucketCount := count
				positionInBucket := targetCount - (runningCount - bucketCount)
				fraction := float64(positionInBucket) / float64(bucketCount)

				return lowerBound + fraction*(upperBound-lowerBound), true
			}

			// This is not super accurate
			lastBound := histogramData.Bounds[len(histogramData.Bounds)-1]
			return lastBound * 1.5, true
		}
	}
	return histogramData.Max.Value()
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

	goStackAlloc, err := meter.Int64Gauge("blog.stack.alloc.bytes",
		metric.WithDescription("bytes allocated to the stack by the blog"),
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

			stackAlloc := m.StackSys
			var stackAllocInt64 int64

			if stackAlloc >= uint64(math.MaxInt64) {
				log.Print("gosec was right and i am an idiot")
				stackAllocInt64 = math.MaxInt64
			} else {
				stackAllocInt64 = int64(stackAlloc)
			}

			goStackAlloc.Record(ctx, stackAllocInt64)

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
