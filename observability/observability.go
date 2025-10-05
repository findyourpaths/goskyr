package observability

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/contrib/detectors/gcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

var ObservabilityDir = "Observability"

func InitAll(ctx context.Context, dir string, inTest bool) (func() error, error) {
	rsc, err := sdkresource.New(
		context.Background(),
		resource.WithDetectors(gcp.NewDetector()),
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(
			semconv.ServiceName("goskyr"),
			semconv.ServiceVersion("v0.1.0"),
			attribute.String("environment", "development"),
		))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize resource: %v", err)
	}

	if err := InitLogging(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize logging: %v", err)
	}

	if err := InitMetrics(ctx, rsc); err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %v", err)
	}

	if err := InitTracing(ctx, rsc); err != nil {
		return nil, fmt.Errorf("failed to initialize logging: %v", err)
	}

	if err := InitInstruments(Meter); err != nil {
		slog.Error("failed to init instruments", "error", err)
		return nil, fmt.Errorf("failed to init instruments: %v", err)
	}

	endFn := func() error {
		wantsTracesP := TracesFilePath(dir, "traces")
		if err := WriteTraces(ctx, wantsTracesP); err != nil {
			slog.Error("in InitTestMain.endFn()", "err", err)
			return err
		}
		ShutdownAll(ctx)
		return nil
	}

	return endFn, nil
}

func ShutdownAll(ctx context.Context) {
	if err := ShutdownMetrics(ctx); err != nil {
		slog.Error("failed to shutdown metrics", "error", err)
	}
	if err := ShutdownTracing(ctx); err != nil {
		slog.Error("failed to shutdown tracing", "error", err)
	}
}
