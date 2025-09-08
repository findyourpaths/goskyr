package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"time"

	"github.com/findyourpaths/goskyr/utils"
	clientprom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/samber/lo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Define package-level variables for your metric instruments.
var EmailsProcessedCounter metric.Int64Counter

var registry *clientprom.Registry
var meterProvider *sdkmetric.MeterProvider

// Meter is the global meter for the application, accessible by other packages.
var Meter metric.Meter

// InitMetrics sets up OTel metrics, creating the meter provider and the global
// Meter.
func InitMetrics(ctx context.Context) error {
	slog.Info("Configuring OpenTelemetry metrics...")

	// Exporter for pushing metrics to an OTLP collector.
	otlpExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint("localhost:4327"),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("failed to create OTLP metric exporter: %w", err)
	}

	// 1. Create a new Prometheus registry.
	registry = clientprom.NewRegistry()

	// 2. Create the Prometheus exporter, passing the registry to it.
	prometheusExporter, err := prometheus.New(
		prometheus.WithRegisterer(registry),
	)
	if err != nil {
		return fmt.Errorf("failed to create Prometheus exporter: %w", err)
	}

	meterProvider = sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(Resource),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(otlpExp, sdkmetric.WithInterval(10*time.Second))),
		sdkmetric.WithReader(prometheusExporter),
	)
	otel.SetMeterProvider(meterProvider)

	Meter = otel.Meter("goskyr/application")

	slog.Info("Configured OpenTelemetry metrics")
	return nil
}

func GetMetrics(ctx context.Context) string {
	// The registry is already configured, so we can create the handler directly.
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return strings.Join(lo.Filter(strings.Split(string(recorder.Body.Bytes()), "\n"), func(str string, index int) bool {
		return !strings.Contains(str, "duration")
	}), "\n")
}

// ShutdownMetrics ensures the tracer provider is shut down gracefully.
// Returns the collected spans if an in-memory exporter was used.
func ShutdownMetrics(ctx context.Context) error {
	return meterProvider.Shutdown(ctx)
}

func WriteMetrics(ctx context.Context, p string) error {
	metricsStr := GetMetrics(ctx)
	pmetricsStr := ProjectMetrics(metricsStr)

	// Write metrics and paths metrics.
	if err := utils.WriteStringFile(p, metricsStr); err != nil {
		return fmt.Errorf("Failed to write metrics file: %v", err)
	}
	fmt.Printf("wrote golden metrics file: %q\n", p)

	projectP := strings.TrimSuffix(p, ".prom") + ".project.prom"
	if err := utils.WriteStringFile(projectP, pmetricsStr); err != nil {
		return fmt.Errorf("Failed to write paths metrics file: %v", err)
	}
	fmt.Printf("wrote golden paths metrics file: %q\n", projectP)

	return nil
}

// MetricsFilePath generates a standardized path for metrics files.
func MetricsFilePath(dir string, path string) string {
	dir = filepath.Join(dir, ObservabilityDir)
	utils.MustEnsureDir(dir) // Assuming you have this helper
	return filepath.Join(dir, path+"_metrics.prom")
}

func ProjectMetrics(metricsStr string) string {
	extraneous := `otel_scope_name="paths/application",otel_scope_schema_url="",otel_scope_version=""`
	metricsStr = strings.ReplaceAll(metricsStr, extraneous+",", "")
	metricsStr = strings.ReplaceAll(metricsStr, extraneous, "")
	metricsStr = strings.Join(lo.Filter(strings.Split(metricsStr, "\n"), func(line string, index int) bool {
		return strings.HasPrefix(line, "goskyr_")
	}), "\n")
	return metricsStr
}
