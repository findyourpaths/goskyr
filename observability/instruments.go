package observability

import (
	"context"
	"fmt"

	"github.com/samber/lo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var Instruments *instruments

type instruments struct {
	Generate metric.Int64Counter
	Scrape   metric.Int64Counter
	Test     metric.Int64Counter
}

func InitInstruments(meter metric.Meter) error {
	var err error
	Instruments, err = NewInstruments(meter, "goskyr")
	return err
}

func NewInstruments(meter metric.Meter, prefix string) (*instruments, error) {
	ret := &instruments{}
	var err error

	ret.Generate, err = meter.Int64Counter(prefix) //  + "_generate")
	if err != nil {
		return nil, fmt.Errorf("failed to create counter: %w", err)
	}

	ret.Scrape, err = meter.Int64Counter(prefix) //  + "_generate")
	if err != nil {
		return nil, fmt.Errorf("failed to create counter: %w", err)
	}

	ret.Test, err = meter.Int64Counter(prefix) //  + "_generate")
	if err != nil {
		return nil, fmt.Errorf("failed to create counter: %w", err)
	}

	return ret, nil
}

func Add(ctx context.Context, ic metric.Int64Counter, incr int64, kvs ...attribute.KeyValue) {
	if ic != nil {
		ic.Add(ctx, incr, metric.WithAttributes(lo.Filter(kvs, keepMetricAttribute)...))
	}
	span := trace.SpanFromContext(ctx)
	if span != nil {
		span.SetAttributes(kvs...)
	}
}

// metricAttrKeys allowlists the bounded-cardinality attribute keys goskyr emits
// as metric labels. A metric label is one time series per distinct value, so an
// unbounded value (a per-record dump, a formatted payload, a per-call id) pins
// the SDK's in-memory series store and OOMs long runs. Only these keys reach the
// counters; every attribute still populates the active span, so tracing detail
// is unchanged. New attributes are span-only by default — add a key here only
// once it is known low-cardinality.
var metricAttrKeys = map[string]bool{
	"source": true,
	"status": true,
}

func keepMetricAttribute(kv attribute.KeyValue, _ int) bool {
	return metricAttrKeys[string(kv.Key)]
}
