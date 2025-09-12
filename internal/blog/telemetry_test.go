package blog

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

func TestPercentileCalcCorrectness(t *testing.T) {
	tests := []struct {
		name           string
		totalCount     int64
		bucketCounts   map[int]int64
		percentile     int64
		expectedResult int64
	}{
		{
			name:       "P50 with even distribution",
			totalCount: 100,
			bucketCounts: map[int]int64{
				5:  25, // 0-5ms
				10: 25, // 5-10ms
				25: 25, // 10-25ms
				50: 25, // 25-50ms
			},
			percentile:     50,
			expectedResult: 7,
		},
		{
			name:       "P95 skewed distribution",
			totalCount: 1000,
			bucketCounts: map[int]int64{
				5:  900,
				10: 50,
				25: 30,
				50: 20,
			},
			percentile:     95,
			expectedResult: 7,
		},
		{
			name:       "P99 with outliers",
			totalCount: 100,
			bucketCounts: map[int]int64{
				5:   90,
				10:  5,
				25:  3,
				50:  1,
				100: 1,
			},
			percentile:     99,
			expectedResult: 37,
		},
		{
			name:           "empty histogram",
			totalCount:     0,
			bucketCounts:   map[int]int64{},
			percentile:     50,
			expectedResult: 0,
		},
		{
			name:       "P50 production data",
			totalCount: 33,
			bucketCounts: map[int]int64{
				5:  32,
				10: 1,
			},
			percentile:     50,
			expectedResult: 2,
		},
		{
			name:       "P90 production data",
			totalCount: 33,
			bucketCounts: map[int]int64{
				5:  32,
				10: 1,
			},
			percentile:     90,
			expectedResult: 4,
		},
		{
			name:       "P95 production data",
			totalCount: 33,
			bucketCounts: map[int]int64{
				5:  32,
				10: 1,
			},
			percentile:     95,
			expectedResult: 4,
		},
		{
			name:       "P99 production data",
			totalCount: 33,
			bucketCounts: map[int]int64{
				5:  32,
				10: 1,
			},
			percentile:     99,
			expectedResult: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := NewLocalTelemetryStorage()

			storage.reqDurTotalCount.Store(tt.totalCount)
			for bucketMs, count := range tt.bucketCounts {
				if bucket := storage.reqDurBuckets[bucketMs]; bucket != nil {
					bucket.Store(count)
				}
			}

			result, err := storage.calcPercentile(tt.percentile)
			require.NoError(t, err)
			require.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestMetricsExporter(t *testing.T) {
	tests := []struct {
		name               string
		dataPoints         []metricdata.HistogramDataPoint[float64]
		expectedTotalCount int64
		expectedBuckets    map[int]int64
	}{
		{
			name: "single data point cumulative",
			dataPoints: []metricdata.HistogramDataPoint[float64]{
				{
					Count:        10,
					Sum:          0.050,
					Bounds:       []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1},
					BucketCounts: []uint64{5, 3, 2, 0, 0, 0, 0},
				},
			},
			expectedTotalCount: 10,
			expectedBuckets: map[int]int64{
				5:   5,
				10:  3,
				25:  2,
				50:  0,
				75:  0,
				100: 0,
			},
		},
		{
			name: "multiple data points last one wins",
			dataPoints: []metricdata.HistogramDataPoint[float64]{
				{
					Count:        5,
					Bounds:       []float64{0.005, 0.01},
					BucketCounts: []uint64{3, 2, 0},
				},
				{
					Count:        15,
					Bounds:       []float64{0.005, 0.01},
					BucketCounts: []uint64{10, 5, 0},
				},
			},
			expectedTotalCount: 15,
			expectedBuckets: map[int]int64{
				5:  10,
				10: 5,
			},
		},
		{
			name: "bounds conversion to milliseconds",
			dataPoints: []metricdata.HistogramDataPoint[float64]{
				{
					Count:        5,
					Bounds:       []float64{0.001, 0.0025, 0.005},
					BucketCounts: []uint64{1, 2, 2, 0},
				},
			},
			expectedTotalCount: 5,
			expectedBuckets: map[int]int64{
				5: 2,
			},
		},
		{
			name: "invalid buckets that dont map to predefined boundaries",
			dataPoints: []metricdata.HistogramDataPoint[float64]{
				{
					Count:        5,
					Bounds:       []float64{0.001, 0.003, 0.007},
					BucketCounts: []uint64{1, 2, 2, 0},
				},
			},
			expectedTotalCount: 5,
			expectedBuckets:    map[int]int64{},
		},
		{
			name: "bounds index out of range",
			dataPoints: []metricdata.HistogramDataPoint[float64]{
				{
					Count:        5,
					Bounds:       []float64{0.005, 0.01, 0.025, 0.05},
					BucketCounts: []uint64{3, 2},
				},
			},
			expectedTotalCount: 5,
			expectedBuckets: map[int]int64{
				5:  3,
				10: 2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := NewLocalTelemetryStorage()
			exporter := &MetricsExporter{localTem: storage}

			metrics := &metricdata.ResourceMetrics{
				Resource: resource.Empty(),
				ScopeMetrics: []metricdata.ScopeMetrics{
					{
						Metrics: []metricdata.Metrics{
							{
								Name: "http.server.request.duration",
								Data: metricdata.Histogram[float64]{
									DataPoints:  tt.dataPoints,
									Temporality: metricdata.CumulativeTemporality,
								},
							},
						},
					},
				},
			}

			err := exporter.Export(context.Background(), metrics)
			if err != nil {
				t.Fatalf("Export failed: %v", err)
			}

			actualTotal := storage.reqDurTotalCount.Load()
			if actualTotal != tt.expectedTotalCount {
				t.Errorf("Expected total count %d, got %d", tt.expectedTotalCount, actualTotal)
			}

			for bucketMs, expectedCount := range tt.expectedBuckets {
				bucket := storage.reqDurBuckets[bucketMs]
				if bucket == nil {
					t.Errorf("Bucket %dms not found in storage", bucketMs)
					continue
				}
				actualCount := bucket.Load()
				if actualCount != expectedCount {
					t.Errorf("Bucket %dms: expected %d, got %d", bucketMs, expectedCount, actualCount)
				}
			}
		})
	}
}

func BenchmarkPercentileCalculation(b *testing.B) {
	storage := NewLocalTelemetryStorage()

	storage.reqDurTotalCount.Store(10000)
	storage.reqDurBuckets[5].Store(5000)
	storage.reqDurBuckets[10].Store(2000)
	storage.reqDurBuckets[25].Store(1500)
	storage.reqDurBuckets[50].Store(800)
	storage.reqDurBuckets[100].Store(400)
	storage.reqDurBuckets[250].Store(200)
	storage.reqDurBuckets[500].Store(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = storage.calcPercentile(95)
	}
}

/*
*
* NOTE: creating a new goroutine in the exporter to calc percentiles is more than 2x slower
* than calculating in the same goroutine
 */
func BenchmarkMetricsExporter(b *testing.B) {
	storage := NewLocalTelemetryStorage()
	exporter := &MetricsExporter{localTem: storage}

	metrics := &metricdata.ResourceMetrics{
		Resource: resource.Empty(),
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Metrics: []metricdata.Metrics{
					{
						Name: "http.server.request.duration",
						Data: metricdata.Histogram[float64]{
							DataPoints: []metricdata.HistogramDataPoint[float64]{
								{
									Count:        1000,
									Bounds:       []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 7.5, 10.0},
									BucketCounts: []uint64{100, 200, 150, 100, 50, 25, 25, 25, 25, 25, 25, 25, 25, 25, 0},
								},
							},
							Temporality: metricdata.CumulativeTemporality,
						},
					},
				},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		storage.reqDur99.Store(0)
		_ = exporter.Export(context.Background(), metrics)
		for {
			if storage.reqDur99.Load() != 0 {
				break
			}
		}
	}
}
