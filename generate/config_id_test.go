package generate

import (
	"testing"

	"github.com/findyourpaths/goskyr/scrape"
)

func TestReplaceStrategyPrefixPreservesCompactConfigID(t *testing.T) {
	cid := scrape.ConfigID{Slug: "example-com", ID: "n10a"}.WithCompact(true)
	got := replaceStrategyPrefix(cid, "s")
	if got.String() != "s10a" {
		t.Fatalf("replaceStrategyPrefix lost compact ConfigID mode: got %q", got.String())
	}
}

func TestInitOptsAppliesCompactConfigID(t *testing.T) {
	compactOpts, err := InitOpts(ConfigOptions{URL: "https://example.com/events", CompactConfigID: true})
	if err != nil {
		t.Fatalf("InitOpts compact: %v", err)
	}
	compactOpts.configID.ID = "n5"
	if got := compactOpts.configID.String(); got != "n5" {
		t.Fatalf("compact InitOpts ConfigID.String() = %q, want %q", got, "n5")
	}

	defaultOpts, err := InitOpts(ConfigOptions{URL: "https://example.com/events"})
	if err != nil {
		t.Fatalf("InitOpts default: %v", err)
	}
	defaultOpts.configID.ID = "n5"
	want := defaultOpts.configID.Slug + "__n5"
	if got := defaultOpts.configID.String(); got != want {
		t.Fatalf("default InitOpts ConfigID.String() = %q, want %q", got, want)
	}
}
