package blog

import (
	"context"
	"encoding/json"
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
	latestSpan            tracetest.SpanStub
	reqDurTotalCount      atomic.Int64
	reqDurBuckets         map[int]*atomic.Int64
	reqDur99              atomic.Int64
	reqDur95              atomic.Int64
	reqDur90              atomic.Int64
	reqDur50              atomic.Int64
	reqFreqBound          []int
	articlesServed        atomic.Int64
	servedCountPerArticle map[string]*atomic.Int64 // ["articlename"].Load()
	reqBlocked            atomic.Int64
	reqBlockedByReason    map[string]*atomic.Int64 // ["reason"].Load()
	roboticVisitors       atomic.Int64
	numGoRo               atomic.Int64
	heapAlloc             atomic.Int64
	stackAlloc            atomic.Int64
	spanMu                sync.RWMutex
	freqUpdateMu          sync.Mutex
	spanChan              chan tracetest.SpanStub
}

func NewLocalTelemetryStorage() *LocalTelemetryStorage {
	boundaries := []int{5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000}
	bcounts := make(map[int]*atomic.Int64, 15)
	for _, bounds := range boundaries {
		bcounts[bounds] = &atomic.Int64{}
		bcounts[bounds].Store(0)
	}
	return &LocalTelemetryStorage{
		servedCountPerArticle: make(map[string]*atomic.Int64, 0),
		reqBlockedByReason:    make(map[string]*atomic.Int64, 0),
		spanChan:              make(chan tracetest.SpanStub, 10),
		reqDurBuckets:         bcounts,
		reqFreqBound:          boundaries,
	}
}

// have to do this because article names can change during process runtime
func (lts *LocalTelemetryStorage) validateArticleAttr(artName string) {
	_, found := lts.servedCountPerArticle[artName]
	if !found {
		lts.servedCountPerArticle[artName] = &atomic.Int64{}
		lts.servedCountPerArticle[artName].Store(0)
	}
}

// will want to make this more generic at some point
func (lts *LocalTelemetryStorage) validateReqBlockedReason(reason string) {
	_, found := lts.reqBlockedByReason[reason]
	if !found {
		lts.reqBlockedByReason[reason] = &atomic.Int64{}
		lts.reqBlockedByReason[reason].Store(0)
	}
}

func (lts *LocalTelemetryStorage) UpdateServerFreqHistogram() {
	lts.freqUpdateMu.Lock()
	defer lts.freqUpdateMu.Unlock()

	p50, err := lts.calcPercentile(50)
	if err != nil {
		telemLogger.Warn().Msgf("unable to calculate p50 for req freq: %v", err)
	} else {
		lts.reqDur50.Store(p50)
		telemLogger.Debug().Msgf("p50: %d", p50)
	}

	p90, err := lts.calcPercentile(90)
	if err != nil {
		telemLogger.Warn().Msgf("unable to calculate p90 for req freq: %v", err)
	} else {
		lts.reqDur90.Store(p90)
		telemLogger.Debug().Msgf("p90: %d", p90)

	}
	p95, err := lts.calcPercentile(95)
	if err != nil {
		telemLogger.Warn().Msgf("unable to calculate p95 for req freq: %v", err)
	} else {
		lts.reqDur95.Store(p95)
		telemLogger.Debug().Msgf("p95: %d", p95)

	}
	p99, err := lts.calcPercentile(99)
	if err != nil {
		telemLogger.Warn().Msgf("unable to calculate p99 for req freq: %v", err)
	} else {
		lts.reqDur99.Store(p99)
		telemLogger.Debug().Msgf("p99: %d", p99)

	}
}

func (lts *LocalTelemetryStorage) calcPercentile(percentile int64) (int64, error) {
	totalCount := lts.reqDurTotalCount.Load()
	if totalCount == 0 {
		return 0, nil
	}

	targetCount := float64(totalCount*percentile) / 100
	var runningCount float64 = 0
	previousBound := 0

	for _, boundary := range lts.reqFreqBound {
		bucket := lts.reqDurBuckets[boundary]
		if bucket == nil {
			continue
		}

		count := float64(bucket.Load())
		runningCount += count

		if runningCount >= targetCount {
			positionInBucket := targetCount - (runningCount - count)
			if count > 0 {
				fraction := positionInBucket / count
				lowerBound := float64(previousBound)
				upperBound := float64(boundary)
				result := lowerBound + fraction*(upperBound-lowerBound)
				return int64(math.Round(result)), nil
			}
			return int64(boundary), nil
		}
		previousBound = boundary
	}

	return int64(lts.reqFreqBound[len(lts.reqFreqBound)-1]), nil
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
		telemLogger.Error().Msgf("failed to init runtime metrics: %v", err)
		return
	}

	goHeapAlloc, err := meter.Int64Gauge("blog.heap.alloc.bytes",
		metric.WithDescription("bytes allocated to the heap by the blog"),
		metric.WithUnit("bytes"))
	if err != nil {
		telemLogger.Error().Msgf("failed to init runtime metrics: %v", err)
		return
	}

	goStackAlloc, err := meter.Int64Gauge("blog.stack.alloc.bytes",
		metric.WithDescription("bytes allocated to the stack by the blog"),
		metric.WithUnit("bytes"))
	if err != nil {
		telemLogger.Error().Msgf("failed to init runtime metrics: %v", err)
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
				telemLogger.Error().Msg("failed to export stack size")
				stackAllocInt64 = math.MaxInt64
			} else {
				stackAllocInt64 = int64(stackAlloc)
			}

			goStackAlloc.Record(ctx, stackAllocInt64)

			if heapAlloc >= uint64(math.MaxInt64) {
				telemLogger.Error().Msg("failed to export heap size")
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
		telemLogger.Warn().Msgf("dropping span %s: full buffer", span.SpanContext.SpanID().String())
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
