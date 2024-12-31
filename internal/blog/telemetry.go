package blog

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type LocalTelemetryStorage struct {
	latestSpan     tracetest.SpanStub
	articlesServed atomic.Int64
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
