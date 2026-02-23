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
	articlesServed      atomic.Int64
	reqBlocked          atomic.Int64
	roboticVisitors     atomic.Int64
	numGoRo             atomic.Int64
	heapAlloc           atomic.Int64
	stackAlloc          atomic.Int64
	costUpdateSuccess   atomic.Int64
	costUpdateFailure   atomic.Int64

	spanMu       sync.RWMutex
	costMu       sync.RWMutex
	freqUpdateMu sync.Mutex

	spanChan              chan tracetest.SpanStub
	reqFreqBound          []int
	reqDurBucketValues    []*atomic.Int64
	boundaryToIndex       map[int]int
	servedCountPerArticle map[string]*atomic.Int64
	reqBlockedByReason    map[string]*atomic.Int64
	costHTML              []byte
	cfg                   *Config
}

func NewLocalTelemetryStorage() *LocalTelemetryStorage {
	boundaries := []int{5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000}
	bucketValues := make([]*atomic.Int64, len(boundaries))
	bIndex := make(map[int]int, len(boundaries))
	for i, bounds := range boundaries {
		bucketValues[i] = &atomic.Int64{}
		bIndex[bounds] = i
	}
	return &LocalTelemetryStorage{
		servedCountPerArticle: make(map[string]*atomic.Int64, 0),
		reqBlockedByReason:    make(map[string]*atomic.Int64, 0),
		spanChan:              make(chan tracetest.SpanStub, 10),
		reqDurBucketValues:    bucketValues,
		boundaryToIndex:       bIndex,
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

	p50, p90, p95, p99 := lts.calcPercentiles()
	lts.reqDur50.Store(p50)
	lts.reqDur90.Store(p90)
	lts.reqDur95.Store(p95)
	lts.reqDur99.Store(p99)
	telemLogger.Debug().
		Int64("p50", p50).
		Int64("p90", p90).
		Int64("p95", p95).
		Int64("p99", p99).
		Msg("percentiles updated")
}

func (lts *LocalTelemetryStorage) calcPercentiles() (int64, int64, int64, int64) {
	totalCount := lts.reqDurTotalCount.Load()
	if totalCount == 0 {
		return 0, 0, 0, 0
	}

	targets := [4]float64{
		float64(totalCount*50) / 100,
		float64(totalCount*90) / 100,
		float64(totalCount*95) / 100,
		float64(totalCount*99) / 100,
	}
	var results [4]int64
	found := 0

	var runningCount float64 = 0
	previousBound := 0
	lastBound := int64(lts.reqFreqBound[len(lts.reqFreqBound)-1])

	for i, boundary := range lts.reqFreqBound {
		count := float64(lts.reqDurBucketValues[i].Load())
		runningCount += count

		for found < 4 && runningCount >= targets[found] {
			if count > 0 {
				positionInBucket := targets[found] - (runningCount - count)
				fraction := positionInBucket / count
				lowerBound := float64(previousBound)
				upperBound := float64(boundary)
				results[found] = int64(math.Round(lowerBound + fraction*(upperBound-lowerBound)))
			} else {
				results[found] = int64(boundary)
			}
			found++
		}
		if found == 4 {
			break
		}
		previousBound = boundary
	}

	for found < 4 {
		results[found] = lastBound
		found++
	}

	return results[0], results[1], results[2], results[3]
}

// calcPercentile computes a single percentile (used by tests)
func (lts *LocalTelemetryStorage) calcPercentile(percentile int64) (int64, error) {
	totalCount := lts.reqDurTotalCount.Load()
	if totalCount == 0 {
		return 0, nil
	}

	targetCount := float64(totalCount*percentile) / 100
	var runningCount float64 = 0
	previousBound := 0

	for i, boundary := range lts.reqFreqBound {
		count := float64(lts.reqDurBucketValues[i].Load())
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

func generateCostHTML(serviceCosts map[string]map[string]string, lastUpdate string) []byte {
	var costBuilder strings.Builder
	costBuilder.Grow(1536)

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

	costBuilder.WriteString("<tfoot>")
	costBuilder.WriteString("<tr>")
	costBuilder.WriteString("<td colspan=\"4\" class=\"cost-updated\">Last updated: ")
	costBuilder.WriteString(lastUpdate)
	costBuilder.WriteString("</td>")
	costBuilder.WriteString("</tr>")
	costBuilder.WriteString("</tfoot>")

	return []byte(costBuilder.String())
}

func (lts *LocalTelemetryStorage) costUpdateLoop(ctx context.Context) {
	meter := otel.GetMeterProvider().Meter("jake-blog")

	costSuccess, err := meter.Int64Counter("blog.cost.update.success",
		metric.WithDescription("number of successful cost updates"),
		metric.WithUnit("updates"))
	if err != nil {
		telemLogger.Error().Msgf("failed to init cost update metrics: %v", err)
		return
	}

	costFailure, err := meter.Int64Counter("blog.cost.update.failure",
		metric.WithDescription("number of failed cost updates"),
		metric.WithUnit("updates"))
	if err != nil {
		telemLogger.Error().Msgf("failed to init cost update metrics: %v", err)
		return
	}

	if lts.cfg == nil || !lts.cfg.CostTrackingEnabled {
		lts.costMu.Lock()
		lts.costHTML = []byte(`<thead><tr><th>Service</th><th>7d</th><th>30d</th><th>90d</th></tr></thead><tbody><tr><td colspan="4" style="text-align: center; padding: 20px; color: #76ff03;">Cost tracking disabled</td></tr></tbody><tfoot><tr><td colspan="4" class="cost-updated">Last updated: N/A</td></tr></tfoot>`)
		lts.costMu.Unlock()
		telemLogger.Info().Msg("cost tracking is disabled")
		return
	}

	serviceCosts, err := lts.fetchAWSCosts(ctx)
	lts.costMu.Lock()
	if err != nil {
		telemLogger.Error().Msgf("failed to fetch initial AWS costs: %v", err)
		timestamp := time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
		lts.costHTML = []byte(`<thead><tr><th>Service</th><th>7d</th><th>30d</th><th>90d</th></tr></thead><tbody><tr><td colspan="4" style="text-align: center; padding: 20px; color: #ff0000;">Failed to fetch cost data. Check logs for details.</td></tr></tbody><tfoot><tr><td colspan="4" class="cost-updated">Last updated: ` + timestamp + `</td></tr></tfoot>`)
		costFailure.Add(ctx, 1)
	} else {
		timestamp := time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
		lts.costHTML = generateCostHTML(serviceCosts, timestamp)
		costSuccess.Add(ctx, 1)
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
				costFailure.Add(ctx, 1)
				// Keep old data - don't overwrite costHTML on failure
				continue
			}

			timestamp := time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
			lts.costMu.Lock()
			lts.costHTML = generateCostHTML(serviceCosts, timestamp)
			lts.costMu.Unlock()

			costSuccess.Add(ctx, 1)
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
				stackAllocInt64 = int64(stackAlloc) // #nosec G115 -- bounds checked above
			}

			goStackAlloc.Record(ctx, stackAllocInt64)

			if heapAlloc >= uint64(math.MaxInt64) {
				telemLogger.Error().Msg("failed to export heap size")
				heapAllocInt64 = math.MaxInt64
			} else {
				heapAllocInt64 = int64(heapAlloc) // #nosec G115 -- bounds checked above
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
