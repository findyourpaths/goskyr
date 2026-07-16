package generate

import (
	"testing"

	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scrape"
)

func TestGeneratedFieldsWithRecordValuesDropsUnalignedDynamicField(t *testing.T) {
	t.Parallel()

	fields := []scrape.Field{
		{Name: "Ftitle--0", Type: "text"},
		{Name: "Fbuttons-href-0", Type: "url"},
		{Name: "source", Value: "directory"},
	}
	records := output.Records{
		{
			"Ftitle--0":             "Alice",
			"Fbuttons-href-0":       "",
			"Fbuttons-href-0__Aurl": "",
		},
	}

	got := generatedFieldsWithRecordValues(fields, records)
	if len(got) != 2 {
		t.Fatalf("generatedFieldsWithRecordValues returned %d fields, want 2: %#v", len(got), got)
	}
	if got[0].Name != "Ftitle--0" || got[1].Name != "source" {
		t.Fatalf("generatedFieldsWithRecordValues names = [%q %q], want [Ftitle--0 source]", got[0].Name, got[1].Name)
	}
}

func TestSequentialCTAValidationUsesRetainedURLField(t *testing.T) {
	t.Parallel()

	fields := []scrape.Field{
		{Name: "Ftitle--0", Type: "text"},
		{
			Name: "Fdetail-href-0",
			Type: "url",
			ElementLocations: []scrape.ElementLocation{
				{Selector: "a.detail", Attr: "href"},
			},
		},
	}

	validation := sequentialCTAValidation(fields)
	if validation == nil || validation.RequiresCTASelector != "a.detail" {
		t.Fatalf("sequentialCTAValidation = %#v, want selector a.detail", validation)
	}
	if got := sequentialCTAValidation(fields[:1]); got != nil {
		t.Fatalf("sequentialCTAValidation without URL field = %#v, want nil", got)
	}
}
