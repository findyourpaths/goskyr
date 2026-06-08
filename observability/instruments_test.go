package observability

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

func TestKeepMetricAttributeKeepsOnlyAllowlistedLabels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		attr attribute.KeyValue
		want bool
	}{
		{
			name: "source",
			attr: attribute.String("source", "web_example_com"),
			want: true,
		},
		{
			name: "status",
			attr: attribute.String("status", "success"),
			want: true,
		},
		{
			name: "arg prefix",
			attr: attribute.String("arg.url", "https://example.com/path"),
			want: false,
		},
		{
			name: "int prefix",
			attr: attribute.Int("int.position", 12),
			want: false,
		},
		{
			name: "ret prefix",
			attr: attribute.Int("ret.count", 3),
			want: false,
		},
		{
			name: "unprefixed record dump",
			attr: attribute.String("rets", "huge per-record dump"),
			want: false,
		},
		{
			name: "unprefixed list length",
			attr: attribute.Int("lps.len", 1200),
			want: false,
		},
		{
			name: "duration",
			attr: attribute.Int64("wait_ms", 1200),
			want: false,
		},
		{
			name: "bytes",
			attr: attribute.Int("payload_bytes", 999),
			want: false,
		},
		{
			name: "domain",
			attr: attribute.String("domain", "example.com"),
			want: false,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := keepMetricAttribute(tt.attr, 0); got != tt.want {
				t.Fatalf("keepMetricAttribute(%q) = %t, want %t", tt.attr.Key, got, tt.want)
			}
		})
	}
}
