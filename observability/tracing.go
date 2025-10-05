package observability

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	// "github.com/findyourpaths/paths/internal/util"
	"github.com/findyourpaths/goskyr/utils"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

var tracerProvider *sdktrace.TracerProvider
var inMemoryExporter *tracetest.InMemoryExporter

// InitTracing sets up OpenTelemetry for testing. It sends traces to a running
// Jaeger instance and also collects them in memory for snapshot comparison.
// It returns the in-memory exporter and a shutdown function to be deferred.
func InitTracing(ctx context.Context, rsc *sdkresource.Resource) error {
	slog.Info("Configuring OpenTelemetry tracing...")

	otlpExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint("localhost:4327"),
		otlptracegrpc.WithInsecure(),
		// otlptracegrpc.WithDialOption(grpc.WithBlock()),
	)
	if err != nil {
		return err
	}

	// Use the standard in-memory exporter for collecting spans for tests.
	inMemoryExporter = tracetest.NewInMemoryExporter()

	tracerProvider = sdktrace.NewTracerProvider(
		sdktrace.WithResource(rsc),
		sdktrace.WithBatcher(otlpExp),
		// Use SimpleSpanProcessor for in-memory exporter to ensure immediate export
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(inMemoryExporter)),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tracerProvider)

	otel.SetTextMapPropagator(propagation.TraceContext{})

	slog.Info("Configured OpenTelemetry tracing")
	return nil
}

func TraceSpans(ctx context.Context) tracetest.SpanStubs {
	// Force flush to ensure all spans are exported
	if err := tracerProvider.ForceFlush(ctx); err != nil {
		slog.Error("failed to flush traces", "err", err)
	}
	rets := inMemoryExporter.GetSpans()
	SortSpanStubs(rets)
	return rets
}

// SortSpanStubs sorts a slice of SpanStubs in a canonical, stable order.
// The sorting is done in-place.
func SortSpanStubs(stubs []tracetest.SpanStub) {
	sort.Slice(stubs, func(i, j int) bool {
		s1 := stubs[i]
		s2 := stubs[j]

		// 1. Sort by TraceID.
		tid1 := s1.SpanContext.TraceID()
		tid2 := s2.SpanContext.TraceID()
		if tid1 != tid2 {
			return tid1.String() < tid2.String()
		}

		// 2. Sort by StartTime.
		if !s1.StartTime.Equal(s2.StartTime) {
			return s1.StartTime.Before(s2.StartTime)
		}

		// 3. Sort by EndTime.
		if !s1.EndTime.Equal(s2.EndTime) {
			return s1.EndTime.Before(s2.EndTime)
		}

		// 4. Sort by Name.
		if s1.Name != s2.Name {
			return s1.Name < s2.Name
		}

		// 5. Sort by SpanID as the final, unique tie-breaker.
		sid1 := s1.SpanContext.SpanID()
		sid2 := s2.SpanContext.SpanID()
		return sid1.String() < sid2.String()
	})
}

func TraceSpansToSanitizedString(spans tracetest.SpanStubs) string {
	spans = SanitizeSpanStubs(spans)
	retBs, err := utils.WriteJSONBytes(spans)
	if err != nil {
		slog.Error("Failed to marshall spans", "err", err)
	}
	return string(SanitizeIPAddressValues(retBs))
}

// SanitizeSpanStubs zeros out non-deterministic fields for testing
func SanitizeSpanStubs(stubs []tracetest.SpanStub) []tracetest.SpanStub {
	rets := []tracetest.SpanStub{}
	for _, stub := range stubs {
		ret := stub
		ret.Resource = nil
		ret.InstrumentationScope = instrumentation.Scope{}
		ret.InstrumentationLibrary = instrumentation.Library{}

		// Zero out IDs
		ret.SpanContext = trace.SpanContext{}
		ret.Parent = trace.SpanContext{}

		// Zero out timestamps
		ret.StartTime = time.Time{}
		ret.EndTime = time.Time{}

		// Zero out event times
		for j := range ret.Events {
			ret.Events[j].Time = time.Time{}
		}

		// Zero out link IDs
		for j := range ret.Links {
			ret.Links[j].SpanContext = trace.SpanContext{}
		}
		rets = append(rets, ret)
	}
	return rets
}

var ipRegex = regexp.MustCompile(`"Value": "\d+\.\d+\.\d+\.\d+"`)

func SanitizeIPAddressValues(bs []byte) []byte {
	return ipRegex.ReplaceAll(bs, []byte(`"Value": "0.0.0.0"`))
}

// PrintTraceSpanTree takes a slice of spans and prints them as a tree.
func TraceSpanTree(spans []tracetest.SpanStub) string {
	children := make(map[trace.SpanID][]tracetest.SpanStub)
	var roots []tracetest.SpanStub

	// Group children by their parent's SpanID.
	for _, span := range spans {
		if span.Parent.IsValid() {
			parentID := span.Parent.SpanID()
			children[parentID] = append(children[parentID], span)
		} else {
			roots = append(roots, span)
		}
	}

	// Print a tree for each root span.
	ret := &bytes.Buffer{}
	for _, root := range roots {
		// fmt.Printf("%s (%s)\n", root.Name, root.EndTime.Sub(root.StartTime))
		fmt.Fprintf(ret, "%s\n", root.Name)
		printTree(ret, root.SpanContext.SpanID(), children, "")
	}
	return ret.String()
}

// printTree recursively prints the spans and their events.
func printTree(w io.Writer, parentID trace.SpanID, children map[trace.SpanID][]tracetest.SpanStub, prefix string) {
	if spans, ok := children[parentID]; ok {
		for i, span := range spans {
			var connector string
			var newPrefix string

			if i == len(spans)-1 {
				connector = "└── "
				newPrefix = prefix + "    "
			} else {
				connector = "├── "
				newPrefix = prefix + "│   "
			}

			// Print the span itself
			// fmt.Printf("%s%s%s (%s)\n", prefix, connector, span.Name, span.EndTime.Sub(span.StartTime))
			// fmt.Fprintf(w, "%s%s%s - %s (%0.3fs)\n", prefix, connector, span.Name, span.StartTime, float64((span.EndTime.Sub(span.StartTime)/time.Millisecond))/1000.0)
			fmt.Fprintf(w, "%s%s%s\n", prefix, connector, span.Name)

			// Print the attributes for this span
			for _, attr := range span.Attributes {
				fmt.Fprintf(w, "%s╞ attr %s: %s\n", newPrefix, attr.Key, formatAttrValue(attr.Value))
			}

			// Print the events for this span
			for _, event := range span.Events {
				// Format attributes for printing
				var attrs []string
				for _, attr := range event.Attributes {
					attrs = append(attrs, fmt.Sprintf("%s: %s", attr.Key, formatAttrValue(attr.Value)))
				}
				fmt.Fprintf(w, "%s    event: %s, %s\n", newPrefix, event.Name, strings.Join(attrs, ", "))
			}

			// Recurse to print children
			printTree(w, span.SpanContext.SpanID(), children, newPrefix)
		}
	}
}

// formatAttrValue correctly formats an attribute's value based on its type.
func formatAttrValue(v attribute.Value) string {
	// fmt.Printf("formatAttrValue(%#v)\n", v)
	switch v.Type() {
	case attribute.STRING:
		return fmt.Sprintf("%q", v.AsString())
	// case attribute.INT64:
	// 	// fmt.Printf("got int64: %#v\n", v)
	// 	return strconv.FormatInt(v.AsInt64(), 10)
	// case attribute.BOOL:
	// 	return strconv.FormatBool(v.AsBool())
	// case attribute.FLOAT64:
	// 	return strconv.FormatFloat(v.AsFloat64(), 'f', -1, 64)
	default:
		// fmt.Printf("can't handle this: %#v\n", v)
		return v.Emit() // Fallback for other types
	}
}

func WriteTraces(ctx context.Context, p string) error {
	spans := TraceSpans(ctx)
	tracesStr := TraceSpansToSanitizedString(spans)
	treeStr := TraceSpanTree(spans)

	// Write traces and trace tree.
	if err := utils.WriteStringFile(p, tracesStr); err != nil {
		return fmt.Errorf("Failed to write traces file: %v", err)
	}
	fmt.Printf("wrote traces file: %q\n", p)

	treeP := strings.TrimSuffix(p, ".json") + ".txt"
	if err := utils.WriteStringFile(treeP, treeStr); err != nil {
		return fmt.Errorf("Failed to write traces tree file: %v", err)
	}
	fmt.Printf("wrote traces tree file: %q\n", treeP)

	return nil
}

// TracesFilePath generates a standardized path for trace files.
func TracesFilePath(dir string, path string) string {
	dir = filepath.Join(dir, ObservabilityDir)
	utils.MustEnsureDir(dir)
	return filepath.Join(dir, path+"_traces.json")
}

// ShutdownTracing ensures the tracer provider is shut down gracefully.
// Returns the collected spans if an in-memory exporter was used.
func ShutdownTracing(ctx context.Context) error {
	return tracerProvider.Shutdown(ctx)
}
