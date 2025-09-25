package observability

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"

	"go.opentelemetry.io/contrib/detectors/gcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

var Resource *sdkresource.Resource
var ObservabilityDir = "Observability"

func init() {
	var err error
	Resource, err = sdkresource.New(
		context.Background(),
		resource.WithDetectors(gcp.NewDetector()),
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(
			semconv.ServiceName("goskyr"),
			semconv.ServiceVersion("v0.1.0"),
			attribute.String("environment", "development"),
		))
	if err != nil {
		log.Fatal(err)
	}
}

func InitAll(ctx context.Context, dir string, inTest bool) (func() error, error) {
	if err := InitLogging(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize logging: %v", err)
	}

	if err := InitMetrics(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %v", err)
	}

	if err := InitTracing(ctx); err != nil {
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
		if !inTest {
			HandleShutdown(ctx, func() {})
		} else {
			ShutdownAll(ctx)
		}

		return nil
	}

	return endFn, nil
}

// HandleShutdown handles shutting down observability and calls the given close function.
func HandleShutdown(ctx context.Context, closeFn func()) func() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	fmt.Println("Program running...")
	fmt.Println("- Press Ctrl-C to gracefully shut down.")
	fmt.Println("- Press Ctrl-\\ to dump goroutine stacks without shutting down.")

	closeAllFn := func() {
		closeFn()
		ShutdownAll(ctx)
	}

	go func() {
		// This loop waits for signals indefinitely.
		for {
			sig := <-sigChan
			switch sig {

			case syscall.SIGINT, syscall.SIGTERM:
				fmt.Println("")
				slog.Warn("Shutdown signal received.")
				fmt.Println("Closing connections...")
				closeAllFn()
				slog.Warn("Closed connections.")
				slog.Warn("Exiting.")
				os.Exit(0)

			case syscall.SIGQUIT:
				fmt.Println("")
				slog.Warn("SIGQUIT received.")
				slog.Warn("Dumping goroutine stacks.")
				pprof.Lookup("goroutine").WriteTo(os.Stderr, 1)
				// The program continues to run after the dump.
			}
		}
	}()

	return func() {
		slog.Info("Exiting application...")
		closeAllFn()
		slog.Info("Exited application")
	}
}

func ShutdownAll(ctx context.Context) {
	if err := ShutdownMetrics(ctx); err != nil {
		slog.Error("failed to shutdown metrics", "error", err)
	}
	if err := ShutdownTracing(ctx); err != nil {
		slog.Error("failed to shutdown tracing", "error", err)
	}
}
