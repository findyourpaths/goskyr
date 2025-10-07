package observability

import (
	"context"
	"fmt"
	"strings"

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
		ic.Add(ctx, 1, metric.WithAttributes(lo.Filter(kvs, KeepNonVarAttributes)...))
	}
	span := trace.SpanFromContext(ctx)
	if span != nil {
		span.SetAttributes(kvs...)
		// if span.IsRecording() {
		// 	span.AddEvent("From", trace.WithAttributes(kvs...))
		// }
	}
}

func KeepNonVarAttributes(kv attribute.KeyValue, i int) bool {
	key := string(kv.Key)
	return !strings.HasPrefix(key, "arg.") && !strings.HasPrefix(key, "int.") && !strings.HasPrefix(key, "ret.")
}
