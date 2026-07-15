package generate

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/observability"
)

// TestCheckAndUpdateLocPropsVaryingPostClasses verifies that checkAndUpdateLocProps
// merges nodes that differ only in CMS-generated per-page classes like WordPress
// post-9762 vs post-8954. These classes are stripped before comparison.
func TestCheckAndUpdateLocPropsVaryingPostClasses(t *testing.T) {
	// Page 1: div.content-area.post-1001.product.type-product.status-publish > h1.product-title
	// Page 2: div.content-area.post-2002.product.type-product.status-publish > h1.product-title
	old := &locationProps{
		textIndex: 0,
		attr:      "",
		path: path{
			{tagName: "div", classes: []string{"content-area", "post-1001", "product", "type-product", "status-publish"}},
			{tagName: "h1", classes: []string{"product-title", "entry-title"}},
		},
		count:    1,
		examples: []string{"Workshop A"},
	}

	new := &locationProps{
		textIndex: 0,
		attr:      "",
		path: path{
			{tagName: "div", classes: []string{"content-area", "post-2002", "product", "type-product", "status-publish"}},
			{tagName: "h1", classes: []string{"product-title", "entry-title"}},
		},
		count:    1,
		examples: []string{"Workshop B"},
	}

	merged := checkAndUpdateLocProps(old, new)
	if !merged {
		t.Fatal("expected paths to merge (differ only in post-NNNNN) but they did not")
	}

	if old.count != 2 {
		t.Errorf("expected count 2, got %d", old.count)
	}

	// The merged path should have post-NNNNN stripped
	wrapperClasses := old.path[0].classes
	if !slices.Contains(wrapperClasses, "content-area") {
		t.Error("expected 'content-area' in merged classes")
	}
	if !slices.Contains(wrapperClasses, "product") {
		t.Error("expected 'product' in merged classes")
	}
	if slices.Contains(wrapperClasses, "post-1001") {
		t.Error("'post-1001' should be stripped from merged classes")
	}
	if slices.Contains(wrapperClasses, "post-2002") {
		t.Error("'post-2002' should be stripped from merged classes")
	}

	// h1 classes should be fully preserved (no auto-generated classes)
	h1Classes := old.path[1].classes
	if len(h1Classes) != 2 || !slices.Contains(h1Classes, "product-title") || !slices.Contains(h1Classes, "entry-title") {
		t.Errorf("expected h1 classes [product-title, entry-title], got %v", h1Classes)
	}
}

// TestCheckAndUpdateLocPropsBeaverBuilderContentIDs verifies that fl-builder-content-NNNN
// classes are stripped, allowing pages with different Beaver Builder content IDs to merge.
func TestCheckAndUpdateLocPropsBeaverBuilderContentIDs(t *testing.T) {
	old := &locationProps{
		textIndex: 0,
		attr:      "",
		path: path{
			{tagName: "div", classes: []string{"fl-builder-content", "fl-builder-content-6725", "fl-builder-global-templates-locked", "product"}},
		},
		count:    1,
		examples: []string{"val1"},
	}

	new := &locationProps{
		textIndex: 0,
		attr:      "",
		path: path{
			{tagName: "div", classes: []string{"fl-builder-content", "fl-builder-content-8090", "fl-builder-global-templates-locked", "product"}},
		},
		count:    1,
		examples: []string{"val2"},
	}

	merged := checkAndUpdateLocProps(old, new)
	if !merged {
		t.Fatal("expected paths to merge (differ only in fl-builder-content-NNNN) but they did not")
	}

	classes := old.path[0].classes
	if slices.Contains(classes, "fl-builder-content-6725") {
		t.Error("fl-builder-content-6725 should be stripped")
	}
	if slices.Contains(classes, "fl-builder-content-8090") {
		t.Error("fl-builder-content-8090 should be stripped")
	}
	if !slices.Contains(classes, "fl-builder-content") {
		t.Error("fl-builder-content (no number) should be preserved")
	}
	if !slices.Contains(classes, "product") {
		t.Error("product should be preserved")
	}
}

// TestCheckAndUpdateLocPropsDrupalViewIDs verifies that Drupal's per-render
// js-view-dom-id hashes do not split one structural field into count-1 columns.
func TestCheckAndUpdateLocPropsDrupalViewIDs(t *testing.T) {
	old := &locationProps{
		attr: "href",
		path: path{
			{tagName: "div", classes: []string{"views-element-container"}},
			{tagName: "div", classes: []string{"js-view-dom-id-c4784e6de400096e"}},
			{tagName: "a", classes: []string{"website"}},
		},
		count:    1,
		examples: []string{"https://alice.example"},
	}
	new := &locationProps{
		attr: "href",
		path: path{
			{tagName: "div", classes: []string{"views-element-container"}},
			{tagName: "div", classes: []string{"js-view-dom-id-58c08a4c74af54dc"}},
			{tagName: "a", classes: []string{"website"}},
		},
		count:    1,
		examples: []string{"https://bob.example"},
	}

	if !checkAndUpdateLocProps(old, new) {
		t.Fatal("expected paths with distinct Drupal view instance IDs to merge")
	}
	if old.count != 2 {
		t.Fatalf("merged count = %d, want 2", old.count)
	}
	if got := old.path[1].classes; len(got) != 0 {
		t.Fatalf("generated Drupal view classes survived merge: %v", got)
	}
}

func TestCheckAndUpdateLocPropsVaryingRecordStateClasses(t *testing.T) {
	locations := locationManager{}
	for _, availability := range []string{"yes", "limited", "no"} {
		locations = mergeLocationProp(locations, &locationProps{
			attr: "href",
			path: path{
				{tagName: "article", classes: []string{"practitioner-profile", "practice-availability-" + availability}},
				{tagName: "div", classes: []string{"main"}},
				{tagName: "a", classes: []string{"website"}},
			},
			count:    1,
			examples: []string{"https://" + availability + ".example"},
		})
	}

	if len(locations) != 1 {
		t.Fatalf("record-state variants produced %d locations, want 1", len(locations))
	}
	if got, want := locations[0].path.string(), "article.practitioner-profile > div.main > a.website"; got != want {
		t.Fatalf("merged selector = %q, want %q", got, want)
	}
	if got := locations[0].count; got != 3 {
		t.Fatalf("merged count = %d, want 3", got)
	}
}

func TestCheckAndUpdateLocPropsDoesNotMergeRoleClasses(t *testing.T) {
	old := &locationProps{
		path: path{
			{tagName: "div", classes: []string{"block", "header"}},
			{tagName: "a", classes: []string{"link"}},
		},
	}
	new := &locationProps{
		path: path{
			{tagName: "div", classes: []string{"block", "footer"}},
			{tagName: "a", classes: []string{"link"}},
		},
	}

	if checkAndUpdateLocProps(old, new) {
		t.Fatal("distinct header and footer roles merged")
	}
}

func TestCheckAndUpdateLocPropsMergesOptionalPictureWrapper(t *testing.T) {
	wrapped := &locationProps{
		attr: "src",
		path: path{
			{tagName: "article", classes: []string{"card"}},
			{tagName: "div", classes: []string{"media"}},
			{tagName: "picture"},
			{tagName: "img", classes: []string{"el-image"}},
		},
		count:    1,
		examples: []string{"wrapped.jpg"},
	}
	direct := &locationProps{
		attr: "src",
		path: path{
			{tagName: "article", classes: []string{"card"}},
			{tagName: "div", classes: []string{"media"}},
			{tagName: "img", classes: []string{"el-image"}},
		},
		count:    1,
		examples: []string{"direct.jpg"},
	}

	if !checkAndUpdateLocProps(wrapped, direct) {
		t.Fatal("direct and picture-wrapped images did not merge")
	}
	if got, want := wrapped.path.string(), "article.card > div.media > img.el-image"; got != want {
		t.Fatalf("canonical path = %q, want %q", got, want)
	}
	if got, want := wrapped.count, 2; got != want {
		t.Fatalf("merged count = %d, want %d", got, want)
	}
	if got, want := len(wrapped.alternativePaths), 1; got != want {
		t.Fatalf("alternative path count = %d, want %d", got, want)
	}
	if got, want := wrapped.alternativePaths[0].string(), "article.card > div.media > picture > img.el-image"; got != want {
		t.Fatalf("alternative path = %q, want %q", got, want)
	}

	rootSelector := path{
		{tagName: "article", classes: []string{"card"}},
	}
	if got, want := relativeLocationSelector(wrapped, rootSelector), "div.media > img.el-image, div.media > picture > img.el-image"; got != want {
		t.Fatalf("field selector = %q, want %q", got, want)
	}
}

func TestCheckAndUpdateLocPropsDoesNotMergeArbitraryWrapper(t *testing.T) {
	direct := &locationProps{
		attr: "src",
		path: path{
			{tagName: "article", classes: []string{"card"}},
			{tagName: "div", classes: []string{"media"}},
			{tagName: "img", classes: []string{"el-image"}},
		},
	}
	linked := &locationProps{
		attr: "src",
		path: path{
			{tagName: "article", classes: []string{"card"}},
			{tagName: "div", classes: []string{"media"}},
			{tagName: "a", classes: []string{"profile"}},
			{tagName: "img", classes: []string{"el-image"}},
		},
	}

	if checkAndUpdateLocProps(direct, linked) {
		t.Fatal("meaningful link wrapper merged as an optional structural wrapper")
	}
}

func TestCheckAndUpdateLocPropsRebasesPictureAlternativeAfterCanonicalWidening(t *testing.T) {
	wrappedStateA := &locationProps{
		attr: "src",
		path: path{
			{tagName: "article", classes: []string{"card", "card-state-featured"}},
			{tagName: "div", classes: []string{"media"}},
			{tagName: "picture"},
			{tagName: "img", classes: []string{"el-image"}},
		},
		count: 1,
	}
	directStateA := &locationProps{
		attr: "src",
		path: path{
			{tagName: "article", classes: []string{"card", "card-state-featured"}},
			{tagName: "div", classes: []string{"media"}},
			{tagName: "img", classes: []string{"el-image"}},
		},
		count: 1,
	}
	directStateB := &locationProps{
		attr: "src",
		path: path{
			{tagName: "article", classes: []string{"card", "card-state-standard"}},
			{tagName: "div", classes: []string{"media"}},
			{tagName: "img", classes: []string{"el-image"}},
		},
		count: 1,
	}

	if !checkAndUpdateLocProps(wrappedStateA, directStateA) {
		t.Fatal("state A direct and picture-wrapped images did not merge")
	}
	if !checkAndUpdateLocProps(wrappedStateA, directStateB) {
		t.Fatal("state B direct image did not merge into the existing location")
	}
	if got, want := wrappedStateA.path.string(), "article.card > div.media > img.el-image"; got != want {
		t.Fatalf("canonical path = %q, want %q", got, want)
	}
	if got, want := len(wrappedStateA.alternativePaths), 1; got != want {
		t.Fatalf("alternative path count = %d, want %d", got, want)
	}
	if got, want := wrappedStateA.alternativePaths[0].string(), "article.card > div.media > picture > img.el-image"; got != want {
		t.Fatalf("rebased alternative path = %q, want %q", got, want)
	}
	rootSelector := path{{tagName: "article", classes: []string{"card"}}}
	if got, want := relativeLocationSelector(wrappedStateA, rootSelector), "div.media > img.el-image, div.media > picture > img.el-image"; got != want {
		t.Fatalf("field selector = %q, want %q", got, want)
	}
}

// TestCheckAndUpdateLocPropsStructurallyDifferent verifies that nodes with different
// structural classes (not just auto-generated) are NOT merged.
func TestCheckAndUpdateLocPropsStructurallyDifferent(t *testing.T) {
	old := &locationProps{
		textIndex: 0,
		attr:      "",
		path: path{
			{tagName: "div", classes: []string{"header", "nav-bar", "main-menu"}},
		},
		count:    1,
		examples: []string{"Home"},
	}

	new := &locationProps{
		textIndex: 0,
		attr:      "",
		path: path{
			{tagName: "div", classes: []string{"footer", "nav-bar", "copyright"}},
		},
		count:    1,
		examples: []string{"(c) 2026"},
	}

	merged := checkAndUpdateLocProps(old, new)
	if merged {
		t.Fatal("expected paths NOT to merge (structurally different) but they did")
	}
}

// TestFilterAutoGeneratedClasses verifies the regex patterns.
func TestFilterAutoGeneratedClasses(t *testing.T) {
	tests := []struct {
		input    []string
		expected []string
	}{
		{
			input:    []string{"content-area", "post-1001", "product", "type-product"},
			expected: []string{"content-area", "product", "type-product"},
		},
		{
			input:    []string{"fl-builder-content", "fl-builder-content-6725", "fl-builder-global-templates-locked"},
			expected: []string{"fl-builder-content", "fl-builder-global-templates-locked"},
		},
		{
			input:    []string{"views-element-container", "js-view-dom-id-c4784e6de400096ea344a44cb2fc7c03"},
			expected: []string{"views-element-container"},
		},
		{
			input:    []string{"postid-456", "page-id-789", "wp-theme-bb"},
			expected: []string{"wp-theme-bb"},
		},
		{
			input:    []string{"product-title", "entry-title"},
			expected: []string{"product-title", "entry-title"},
		},
	}

	for _, tt := range tests {
		result := filterAutoGeneratedClasses(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("filterAutoGeneratedClasses(%v) = %v, want %v", tt.input, result, tt.expected)
			continue
		}
		for i, r := range result {
			if r != tt.expected[i] {
				t.Errorf("filterAutoGeneratedClasses(%v)[%d] = %q, want %q", tt.input, i, r, tt.expected[i])
			}
		}
	}
}

func TestConfigurationsForGQDocumentStaticFieldEvidenceBoundsExpansionAndReportsMatches(t *testing.T) {
	ctx := context.Background()
	endFn, err := observability.InitAll(ctx, t.TempDir(), true)
	if err != nil {
		t.Fatalf("InitAll: %v", err)
	}
	defer func() {
		if err := endFn(); err != nil {
			t.Fatalf("end observability: %v", err)
		}
	}()

	doc, err := fetch.NewDocumentFromString(`<html><body><main class="directory">
<article class="person"><h2>Aga</h2><div class="role">Assessor</div><div class="chrome">Directory</div></article>
<article class="person"><h2>Ada</h2><div class="chrome">Directory</div></article>
<article class="person"><h2>Alex</h2><div class="chrome">Directory</div></article>
<article class="person"><h2>Ari</h2><div class="chrome">Directory</div></article>
</main></body></html>`)
	if err != nil {
		t.Fatalf("NewDocumentFromString: %v", err)
	}
	opts, err := InitOpts(ConfigOptions{
		Batch:             true,
		URL:               "https://example.com/trainers",
		MinOccs:           []int{1},
		MinRecords:        2,
		OnlyVaryingFields: true,
		StaticFieldEvidence: []StaticFieldEvidence{
			{Values: []string{"  Assessor\n"}, OccurrenceCount: 1},
			{Values: []string{"Directory", "Directory", "Directory"}, OccurrenceCount: 3},
			{Values: []string{"Mentor"}, OccurrenceCount: 1},
		},
	})
	if err != nil {
		t.Fatalf("InitOpts: %v", err)
	}

	configs, report, err := ConfigurationsForGQDocumentWithEvidenceReport(ctx, nil, opts, doc)
	if err != nil {
		t.Fatalf("ConfigurationsForGQDocumentWithEvidenceReport: %v", err)
	}
	if len(configs) == 0 {
		t.Fatal("ConfigurationsForGQDocumentWithEvidenceReport returned no configs")
	}
	if !slices.Equal(report.MatchedEvidenceIndexes, []int{0}) {
		t.Fatalf("matched evidence indexes = %v, want [0]", report.MatchedEvidenceIndexes)
	}
	if !slices.Equal(report.UnmatchedEvidenceIndexes, []int{1, 2}) {
		t.Fatalf("unmatched evidence indexes = %v, want [1 2]", report.UnmatchedEvidenceIndexes)
	}
	for _, config := range configs {
		for recordIndex, record := range config.Records {
			for fieldName, value := range record {
				if value == "Directory" {
					t.Fatalf("config %q record %d field %q retained unrelated static chrome", config.ID, recordIndex, fieldName)
				}
			}
		}
	}
}

func TestConfigurationsForGQDocumentStaticFieldEvidenceRequiresVaryingMode(t *testing.T) {
	t.Parallel()
	_, _, err := ConfigurationsForGQDocumentWithEvidenceReport(context.Background(), nil, ConfigOptions{
		StaticFieldEvidence: []StaticFieldEvidence{{Values: []string{"Assessor"}, OccurrenceCount: 1}},
	}, nil)
	if err == nil {
		t.Fatal("ConfigurationsForGQDocumentWithEvidenceReport error = nil, want OnlyVaryingFields contract error")
	}
}

func TestFindClustersDoesNotMutateRootBackedFieldPaths(t *testing.T) {
	cardPath := path{
		{tagName: "body"},
		{tagName: "div", classes: []string{"container"}},
		{tagName: "div", classes: []string{"main", "current"}},
		{tagName: "article", classes: []string{"card", "article"}},
		{tagName: "div", classes: []string{"card-body"}},
		{tagName: "div", classes: []string{"name"}},
	}
	rootSelector := cardPath[:2]
	cardLP := &locationProps{path: cardPath, count: 10, examples: []string{"Abby"}}
	formLP := &locationProps{
		path: path{
			{tagName: "body"},
			{tagName: "div", classes: []string{"container"}},
			{tagName: "form"},
			{tagName: "select"},
		},
		count:    10,
		examples: []string{"filter"},
	}

	ctx := context.Background()
	endFn, err := observability.InitAll(ctx, t.TempDir(), true)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer func() {
		if err := endFn(); err != nil {
			t.Fatalf("%v", err)
		}
	}()

	clusters := findClusters(ctx, []*locationProps{cardLP, formLP}, rootSelector)
	cardKey := "body > div.container > div.main.current"
	if _, ok := clusters[cardKey]; !ok {
		t.Fatalf("missing card cluster %q; got keys %v", cardKey, clusters)
	}
	if got := cardLP.path[2].string(); got != "div.main.current" {
		t.Fatalf("card path mutated at cluster node: got %q", got)
	}
	if got := clonePath(clusters[cardKey][0].path[:len(rootSelector)+1]).string(); got != cardKey {
		t.Fatalf("recursive root = %q, want %q", got, cardKey)
	}
}

func TestSquashLocationManagerPreservesPositionalSiblingAlternatives(t *testing.T) {
	lps := locationManager{}
	for record := 1; record <= 2; record++ {
		for paragraph := 1; paragraph <= 3; paragraph++ {
			lps = append(lps, &locationProps{
				textIndex: 0,
				path: path{
					{tagName: "body"},
					{tagName: "div", classes: []string{"card"}, pseudoClasses: []string{fmt.Sprintf("nth-child(%d)", record)}},
					{tagName: "p", pseudoClasses: []string{fmt.Sprintf("nth-child(%d)", paragraph)}},
				},
				count:    1,
				examples: []string{fmt.Sprintf("record %d paragraph %d", record, paragraph)},
			})
		}
	}

	got := squashLocationManager(lps, 2)
	countByPath := map[string]int{}
	for _, lp := range got {
		countByPath[lp.path.string()] = lp.count
	}

	if countByPath["body > div.card > p"] != 6 {
		t.Fatalf("broad p count = %d, want 6; got %#v", countByPath["body > div.card > p"], countByPath)
	}
	for paragraph := 1; paragraph <= 3; paragraph++ {
		selector := fmt.Sprintf("body > div.card > p:nth-child(%d)", paragraph)
		if countByPath[selector] != 2 {
			t.Fatalf("%s count = %d, want 2; got %#v", selector, countByPath[selector], countByPath)
		}
	}
	if countByPath["body > div.card:nth-child(1) > p"] != 0 {
		t.Fatalf("record-position variant survived; got %#v", countByPath)
	}
}

func TestDateDominatedText(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		// Schedule rows: mostly date text.
		{text: "Saturday, June 27, 2026 | 6 pm - 9 pm", want: true},
		{text: "17:00 PM – 21:00 PM (SAST)", want: true},
		{text: "Begins: Friday, 03-Jul-2026", want: true},
		{text: "2026-08-29 @10:00 AM - 2026-08-30@05:00 PM", want: true},
		{text: "Monday, June 15, 2026 6:00 - 7:30 pm CST", want: true},
		// Titles and prose that merely embed a date: mostly words.
		{text: "Development by Design | Singapore | July 7 - 10, 2026", want: false},
		{text: "Type, Teach, Transform through the 27 Enneagram Subtypes | Virtual | July 17-24, 2026", want: false},
		{text: "The Art of Enneagram Typing and Training | VIRTUAL | August 17 - 28, 2026", want: false},
		{text: "Join us on Saturday, June 27 for a wonderful workshop about the nine personality types and their wings", want: false},
		{text: "", want: false},
	}
	for _, tc := range cases {
		if got := dateDominatedText(tc.text); got != tc.want {
			t.Errorf("dateDominatedText(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
