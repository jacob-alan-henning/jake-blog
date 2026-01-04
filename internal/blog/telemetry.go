package blog

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type LocalTelemetryStorage struct {
	latestSpan tracetest.SpanStub

	reqDurTotalCount atomic.Int64
	reqDur99         atomic.Int64
	reqDur95         atomic.Int64
	reqDur90         atomic.Int64
	reqDur50         atomic.Int64
	articlesServed   atomic.Int64
	reqBlocked       atomic.Int64
	roboticVisitors  atomic.Int64
	numGoRo          atomic.Int64
	heapAlloc        atomic.Int64
	stackAlloc       atomic.Int64

	spanMu       sync.RWMutex
	costMu       sync.RWMutex
	freqUpdateMu sync.Mutex

	spanChan              chan tracetest.SpanStub
	reqFreqBound          []int
	reqDurBuckets         map[int]*atomic.Int64
	servedCountPerArticle map[string]*atomic.Int64
	reqBlockedByReason    map[string]*atomic.Int64
	costHTML              []byte
	cfg                   *Config
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
		costHTML:              []byte{},
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

func (lts *LocalTelemetryStorage) Start(ctx context.Context, cfg *Config) error {
	lts.cfg = cfg

	go func() {
		go runtimeMetricLoop(ctx)
		go lts.costUpdateLoop(ctx)
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

func generateDisabledCostHTML() []byte {
	var costBuilder strings.Builder
	costBuilder.Grow(256)

	costBuilder.WriteString("<thead>")
	costBuilder.WriteString("<tr>")
	costBuilder.WriteString("<th>Service</th>")
	costBuilder.WriteString("<th>7d</th>")
	costBuilder.WriteString("<th>30d</th>")
	costBuilder.WriteString("<th>90d</th>")
	costBuilder.WriteString("</tr>")
	costBuilder.WriteString("</thead>")

	costBuilder.WriteString("<tbody>")
	costBuilder.WriteString("<tr>")
	costBuilder.WriteString("<td colspan=\"4\" style=\"text-align: center; padding: 20px; color: #76ff03;\">")
	costBuilder.WriteString("Cost tracking disabled")
	costBuilder.WriteString("</td>")
	costBuilder.WriteString("</tr>")
	costBuilder.WriteString("</tbody>")

	return []byte(costBuilder.String())
}

func (lts *LocalTelemetryStorage) fetchAWSCosts(ctx context.Context) (map[string]map[string]string, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := costexplorer.NewFromConfig(cfg)
	now := time.Now()

	// Filter to only track specific services
	allowedServices := []string{
		"Amazon Lightsail",
		"AmazonCloudWatch",
		"Amazon EC2 Container Registry (ECR)",
		"Amazon Simple Storage Service",
	}
	allowedServicesMap := make(map[string]bool)
	for _, service := range allowedServices {
		allowedServicesMap[service] = true
	}

	// Periods: 7d, 30d, 90d
	periods := map[string]int{
		"7d":  7,
		"30d": 30,
		"90d": 90,
	}

	serviceCosts := make(map[string]map[string]string)

	for period, days := range periods {
		start := now.AddDate(0, 0, -days).Format("2006-01-02")
		end := now.Format("2006-01-02")

		input := &costexplorer.GetCostAndUsageInput{
			TimePeriod: &types.DateInterval{
				Start: aws.String(start),
				End:   aws.String(end),
			},
			Granularity: types.GranularityMonthly,
			Metrics:     []string{"UnblendedCost"},
			GroupBy: []types.GroupDefinition{
				{
					Type: types.GroupDefinitionTypeDimension,
					Key:  aws.String("SERVICE"),
				},
			},
		}

		result, err := client.GetCostAndUsage(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to get cost data for %s: %w", period, err)
		}

		// Aggregate costs across all time buckets for this period
		periodCosts := make(map[string]float64)

		for _, resultByTime := range result.ResultsByTime {
			for _, group := range resultByTime.Groups {
				if len(group.Keys) > 0 {
					serviceName := group.Keys[0]
					// Only track allowed services
					if !allowedServicesMap[serviceName] {
						continue
					}
					if group.Metrics != nil {
						if metric, ok := group.Metrics["UnblendedCost"]; ok && metric.Amount != nil {
							amount := parseFloat(*metric.Amount)
							periodCosts[serviceName] += amount
						}
					}
				}
			}
		}

		// Store aggregated costs for this period
		for serviceName, totalCost := range periodCosts {
			if serviceCosts[serviceName] == nil {
				serviceCosts[serviceName] = make(map[string]string)
			}
			serviceCosts[serviceName][period] = fmt.Sprintf("$%.2f", totalCost)
		}
	}

	return serviceCosts, nil
}

func parseFloat(s string) float64 {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	if err != nil {
		telemLogger.Warn().Msgf("failed to parse float from string '%s': %v", s, err)
		return 0.0
	}
	return f
}

func generateCostHTML(serviceCosts map[string]map[string]string) []byte {
	var costBuilder strings.Builder
	costBuilder.Grow(1024)

	costBuilder.WriteString("<thead>")
	costBuilder.WriteString("<tr>")
	costBuilder.WriteString("<th>Service</th>")
	costBuilder.WriteString("<th>7d</th>")
	costBuilder.WriteString("<th>30d</th>")
	costBuilder.WriteString("<th>90d</th>")
	costBuilder.WriteString("</tr>")
	costBuilder.WriteString("</thead>")

	costBuilder.WriteString("<tbody>")
	if len(serviceCosts) == 0 {
		costBuilder.WriteString("<tr>")
		costBuilder.WriteString("<td colspan=\"4\" style=\"text-align: center; padding: 20px;\">")
		costBuilder.WriteString("No cost data available")
		costBuilder.WriteString("</td>")
		costBuilder.WriteString("</tr>")
	} else {
		for service, costs := range serviceCosts {
			costBuilder.WriteString("<tr>")
			costBuilder.WriteString("<td>")
			costBuilder.WriteString(service)
			costBuilder.WriteString("</td>")
			costBuilder.WriteString("<td>")
			costBuilder.WriteString(costs["7d"])
			costBuilder.WriteString("</td>")
			costBuilder.WriteString("<td>")
			costBuilder.WriteString(costs["30d"])
			costBuilder.WriteString("</td>")
			costBuilder.WriteString("<td>")
			costBuilder.WriteString(costs["90d"])
			costBuilder.WriteString("</td>")
			costBuilder.WriteString("</tr>")
		}
	}
	costBuilder.WriteString("</tbody>")

	return []byte(costBuilder.String())
}

func (lts *LocalTelemetryStorage) costUpdateLoop(ctx context.Context) {
	if lts.cfg == nil || !lts.cfg.CostTrackingEnabled {
		lts.costMu.Lock()
		lts.costHTML = generateDisabledCostHTML()
		lts.costMu.Unlock()
		telemLogger.Info().Msg("cost tracking is disabled")
		return
	}

	serviceCosts, err := lts.fetchAWSCosts(ctx)
	lts.costMu.Lock()
	if err != nil {
		telemLogger.Error().Msgf("failed to fetch initial AWS costs: %v", err)
		lts.costHTML = []byte(`<thead><tr><th>Service</th><th>7d</th><th>30d</th><th>90d</th></tr></thead><tbody><tr><td colspan="4" style="text-align: center; padding: 20px; color: #ff0000;">Failed to fetch cost data. Check logs for details.</td></tr></tbody>`)
	} else {
		lts.costHTML = generateCostHTML(serviceCosts)
		telemLogger.Debug().Msg("cost data updated successfully")
	}
	lts.costMu.Unlock()

	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			serviceCosts, err := lts.fetchAWSCosts(ctx)
			if err != nil {
				telemLogger.Error().Msgf("failed to fetch AWS costs: %v", err)
				// Keep old data - don't overwrite costHTML on failure
				continue
			}

			lts.costMu.Lock()
			lts.costHTML = generateCostHTML(serviceCosts)
			lts.costMu.Unlock()

			telemLogger.Debug().Msg("cost data updated successfully")
		}
	}
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
