package scrape

import (
	"testing"
)

func TestParseTemplatePattern(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		wantSyms  []string
		wantDelim string
		wantErr   bool
	}{
		{
			name:      "single symbol",
			template:  "{name}",
			wantSyms:  []string{"name"},
			wantDelim: "",
		},
		{
			name:      "pipe delimiter",
			template:  "{title} | {date}",
			wantSyms:  []string{"title", "date"},
			wantDelim: " | ",
		},
		{
			name:      "three symbols pipe",
			template:  "{name} | {location} | {date}",
			wantSyms:  []string{"name", "location", "date"},
			wantDelim: " | ",
		},
		{
			name:      "dash delimiter",
			template:  "{title} - {subtitle}",
			wantSyms:  []string{"title", "subtitle"},
			wantDelim: " - ",
		},
		{
			name:      "colon delimiter",
			template:  "{label}: {value}",
			wantSyms:  []string{"label", "value"},
			wantDelim: ": ",
		},
		{
			name:     "mixed delimiters error",
			template: "{a} | {b} - {c}",
			wantErr:  true,
		},
		{
			name:     "no symbols error",
			template: "just text",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syms, delim, err := parseTemplatePattern(tt.template)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if delim != tt.wantDelim {
				t.Errorf("delimiter = %q, want %q", delim, tt.wantDelim)
			}
			if len(syms) != len(tt.wantSyms) {
				t.Errorf("symbols = %v, want %v", syms, tt.wantSyms)
				return
			}
			for i, s := range syms {
				if s != tt.wantSyms[i] {
					t.Errorf("symbol[%d] = %q, want %q", i, s, tt.wantSyms[i])
				}
			}
		})
	}
}

func TestParseTemplateInput(t *testing.T) {
	tests := []struct {
		name     string
		symbols  []string
		delim    string
		input    string
		want     map[string]string
	}{
		{
			name:    "single symbol",
			symbols: []string{"name"},
			delim:   "",
			input:   "John Doe",
			want:    map[string]string{"name": "John Doe"},
		},
		{
			name:    "two symbols pipe",
			symbols: []string{"title", "date"},
			delim:   " | ",
			input:   "Workshop Title | March 15, 2025",
			want:    map[string]string{"title": "Workshop Title", "date": "March 15, 2025"},
		},
		{
			name:    "three symbols",
			symbols: []string{"name", "location", "date"},
			delim:   " | ",
			input:   "Event Name | VIRTUAL | February 2, 2026",
			want:    map[string]string{"name": "Event Name", "location": "VIRTUAL", "date": "February 2, 2026"},
		},
		{
			name:    "extra parts join into last",
			symbols: []string{"name", "rest"},
			delim:   " | ",
			input:   "Part1 | Part2 | Part3 | Part4",
			want:    map[string]string{"name": "Part1", "rest": "Part2 | Part3 | Part4"},
		},
		{
			name:    "fewer parts than symbols",
			symbols: []string{"a", "b", "c"},
			delim:   " | ",
			input:   "Only One",
			want:    map[string]string{"a": "Only One", "b": "", "c": ""},
		},
		{
			name:    "whitespace trimmed",
			symbols: []string{"title", "date"},
			delim:   "|",
			input:   "  Title  |  Date  ",
			want:    map[string]string{"title": "Title", "date": "Date"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTemplateInput(tt.symbols, tt.delim, tt.input)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("result[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestParseRegexInput(t *testing.T) {
	tests := []struct {
		name    string
		regex   string
		input   string
		want    map[string]string
	}{
		{
			name:  "named groups",
			regex: `(?P<sponsor>.*?) Based In: (?P<location>.*)`,
			input: "John Smith Based In: New York",
			want:  map[string]string{"sponsor": "John Smith", "location": "New York"},
		},
		{
			name:  "sponsor by regex",
			regex: `(?i)Sponsored\s+By[:\s]+(?P<sponsor>.+?)(?:\s*Based\s+In|$)`,
			input: "Sponsored By: Acme Corp Based In: Chicago",
			want:  map[string]string{"sponsor": "Acme Corp"},
		},
		{
			name:  "no match returns empty",
			regex: `(?P<title>\d+)`,
			input: "no numbers here",
			want:  map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			df := &DerivedField{Regex: tt.regex}
			if err := df.Initialize(); err != nil {
				t.Fatalf("initialize error: %v", err)
			}
			got, err := parseRegexInput(df.compiledRegex, tt.input)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("result[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestOutputConditionEvaluate(t *testing.T) {
	tests := []struct {
		name      string
		condition *OutputCondition
		value     string
		want      bool
	}{
		{
			name:      "nil condition always true",
			condition: nil,
			value:     "anything",
			want:      true,
		},
		{
			name:      "equals match",
			condition: &OutputCondition{Equals: "VIRTUAL"},
			value:     "VIRTUAL",
			want:      true,
		},
		{
			name:      "equals no match",
			condition: &OutputCondition{Equals: "VIRTUAL"},
			value:     "IN-PERSON",
			want:      false,
		},
		{
			name:      "equals case insensitive",
			condition: &OutputCondition{Equals: "virtual", CaseInsensitive: true},
			value:     "VIRTUAL",
			want:      true,
		},
		{
			name:      "not_equals match",
			condition: &OutputCondition{NotEquals: "VIRTUAL"},
			value:     "IN-PERSON",
			want:      true,
		},
		{
			name:      "not_equals no match",
			condition: &OutputCondition{NotEquals: "VIRTUAL"},
			value:     "VIRTUAL",
			want:      false,
		},
		{
			name:      "matches regex",
			condition: &OutputCondition{Matches: `^\d{4}$`},
			value:     "2025",
			want:      true,
		},
		{
			name:      "matches regex no match",
			condition: &OutputCondition{Matches: `^\d{4}$`},
			value:     "not a year",
			want:      false,
		},
		{
			name:      "not_matches regex",
			condition: &OutputCondition{NotMatches: `^VIRTUAL$`},
			value:     "IN-PERSON",
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.condition != nil {
				if err := tt.condition.Initialize(); err != nil {
					t.Fatalf("initialize error: %v", err)
				}
			}
			got := tt.condition.Evaluate(tt.value)
			if got != tt.want {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestApplyDerivedFields(t *testing.T) {
	tests := []struct {
		name          string
		derivedFields []DerivedField
		rec           map[string]interface{}
		wantRec       map[string]interface{}
	}{
		{
			name: "simple template extraction",
			derivedFields: []DerivedField{
				{
					Source:   "raw_title",
					Template: "{name} | {date}",
					Outputs: []DerivedOutput{
						{Symbol: "name", Target: "name"},
						{Symbol: "date", Target: "datetime_ranges.text"},
					},
				},
			},
			rec: map[string]interface{}{
				"raw_title": "Workshop | March 15, 2025",
			},
			wantRec: map[string]interface{}{
				"raw_title":            "Workshop | March 15, 2025",
				"name":                 "Workshop",
				"datetime_ranges.text": "March 15, 2025",
			},
		},
		{
			name: "conditional output with value override",
			derivedFields: []DerivedField{
				{
					Source:   "raw_title",
					Template: "{name} | {location}",
					Outputs: []DerivedOutput{
						{Symbol: "name", Target: "name"},
						{
							Symbol:    "location",
							Target:    "virtual_locations.text",
							Condition: &OutputCondition{Equals: "VIRTUAL"},
							Value:     "Online",
						},
					},
				},
			},
			rec: map[string]interface{}{
				"raw_title": "Event | VIRTUAL",
			},
			wantRec: map[string]interface{}{
				"raw_title":              "Event | VIRTUAL",
				"name":                   "Event",
				"virtual_locations.text": "Online",
			},
		},
		{
			name: "conditional output not matching",
			derivedFields: []DerivedField{
				{
					Source:   "raw_title",
					Template: "{name} | {location}",
					Outputs: []DerivedOutput{
						{Symbol: "name", Target: "name"},
						{
							Symbol:    "location",
							Target:    "virtual_locations.text",
							Condition: &OutputCondition{Equals: "VIRTUAL"},
							Value:     "Online",
						},
						{
							Symbol:    "location",
							Target:    "locations.text",
							Condition: &OutputCondition{NotEquals: "VIRTUAL"},
						},
					},
				},
			},
			rec: map[string]interface{}{
				"raw_title": "Event | New York",
			},
			wantRec: map[string]interface{}{
				"raw_title":      "Event | New York",
				"name":           "Event",
				"locations.text": "New York",
			},
		},
		{
			name: "regex extraction",
			derivedFields: []DerivedField{
				{
					Source: "content",
					Regex:  `Sponsored By: (?P<sponsor>.+?) Based In: (?P<location>.+)$`,
					Outputs: []DerivedOutput{
						{Symbol: "sponsor", Target: "sponsor_name"},
						{Symbol: "location", Target: "locations.text"},
					},
				},
			},
			rec: map[string]interface{}{
				"content": "Sponsored By: Acme Corp Based In: Chicago",
			},
			wantRec: map[string]interface{}{
				"content":        "Sponsored By: Acme Corp Based In: Chicago",
				"sponsor_name":   "Acme Corp",
				"locations.text": "Chicago",
			},
		},
		{
			name: "source field missing - no error",
			derivedFields: []DerivedField{
				{
					Source:   "missing_field",
					Template: "{a} | {b}",
					Outputs: []DerivedOutput{
						{Symbol: "a", Target: "a"},
					},
				},
			},
			rec: map[string]interface{}{
				"other_field": "value",
			},
			wantRec: map[string]interface{}{
				"other_field": "value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Initialize derived fields
			for i := range tt.derivedFields {
				if err := tt.derivedFields[i].Initialize(); err != nil {
					t.Fatalf("initialize error: %v", err)
				}
			}

			err := ApplyDerivedFields(tt.derivedFields, tt.rec)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			for k, want := range tt.wantRec {
				got, ok := tt.rec[k]
				if !ok {
					t.Errorf("missing key %q in result", k)
					continue
				}
				if got != want {
					t.Errorf("rec[%q] = %v, want %v", k, got, want)
				}
			}
		})
	}
}
