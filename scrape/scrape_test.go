package scrape

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/output"
	"gopkg.in/yaml.v3"
)

func TestPagePaginatorMaxPagesDoesNotFetchBeyondLimit(t *testing.T) {
	t.Parallel()
	cache := fetch.NewMemoryCache(nil)
	cache.Set("https://example.com/page-1", testHTTPResponse(`<html><body>
<article><h2>First</h2></article>
<a class="next" href="/page-2">Next</a>
</body></html>`))
	cfg := &Config{
		Scrapers: []Scraper{
			{
				Name:     "list",
				URL:      "https://example.com/page-1",
				Selector: "article",
				Paginators: []Paginator{
					{
						Location: ElementLocation{Selector: "a.next", Attr: "href"},
						MaxPages: 1,
					},
				},
				Fields: []Field{
					{
						Name: "title",
						Type: "text",
						ElementLocations: ElementLocations{
							{Selector: "h2"},
						},
					},
				},
			},
		},
	}

	recs, err := Page(context.Background(), cache, cfg, &cfg.Scrapers[0], &cfg.Global, true, "")
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("records len = %d, want 1", len(recs))
	}
	if recs[0]["title"] != "First" {
		t.Fatalf("record title = %q, want First", recs[0]["title"])
	}
}

func testHTTPResponse(body string) []byte {
	return []byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\n\r\n%s", len(body), body))
}

const (
	htmlString = `
                            <div class="teaser event-teaser teaser-border teaser-hover">
                                <div class="event-teaser-image event-teaser-image--full"><a
                                        href="/events/10-03-2023-krachstock-final-story" class=""><!--[--><img
                                            src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
                                            class="image image--event_teaser v-lazy-image"><!--]--><!----></a>
                                    <div class="event-tix"><a class="button"
                                            href="https://www.petzi.ch/events/51480/tickets" target="_blank"
                                            rel="nofollow">Tickets</a></div>
                                </div>
                                <div class="event-teaser-info">
                                    <div class="event-teaser-top"><a href="/events/10-03-2023-krachstock-final-story"
                                            class="event-date size-m bold">Fr, 10.03.2023 - 20:00</a></div><a
                                        href="/events/10-03-2023-krachstock-final-story" class="event-teaser-bottom">
                                        <div class="size-xl event-title">Krachstock</div>
                                        <div class="artist-list"><!--[-->
                                            <h3 class="size-xxl"><!--[-->
                                                <div class="artist-teaser">
                                                    <div class="artist-name">Final Story</div>
                                                    <div class="artist-info">Aargau</div>
                                                </div><!----><!--]-->
                                            </h3>
                                            <h3 class="size-xxl"><!--[-->
                                                <div class="artist-teaser">
                                                    <div class="artist-name">Moment Of Madness</div>
                                                    <div class="artist-info">Basel</div>
                                                </div><!----><!--]-->
                                            </h3>
                                            <h3 class="size-xxl"><!--[-->
                                                <div class="artist-teaser">
                                                    <div class="artist-name">Irony of Fate</div>
                                                    <div class="artist-info">Bern</div>
                                                </div><!----><!--]-->
                                            </h3><!--]--><!---->
                                        </div><!---->
                                        <div class="event-teaser-tags"><!--[-->
                                            <div class="tag">Konzert</div><!--]--><!--[-->
                                            <div class="tag">Metal</div>
                                            <div class="tag">Metalcore</div><!--]-->
                                        </div>
                                    </a>
                                </div>
                            </div>`
	htmlString2 = `
	<h2>
		<a href="https://www.eventfabrik-muenchen.de/event/heinz-rudolf-kunze-verstaerkung-2/"
			title="Heinz Rudolf Kunze &amp; Verstärkung &#8211; ABGESAGT">
			<span>Di. | 03.05.2022</span><span>Heinz Rudolf Kunze &amp; Verstärkung
				&#8211; ABGESAGT</span> </a>
	</h2>`
	htmlString3 = `
	<h2>
		<a href="?bli=bla"
			title="Heinz Rudolf Kunze &amp; Verstärkung &#8211; ABGESAGT">
			<span>Di. | 03.05.2022</span><span>Heinz Rudolf Kunze &amp; Verstärkung
				&#8211; ABGESAGT</span> </a>
	</h2>`
	htmlString4 = `
	<div class="text">
		<a href="programm.php?m=4&j=2023&vid=4378">
			<div class="reihe">Treffpunkt</div>
			<div class="titel">Kreativ-Workshop: "My message to the world"
				<span class="supportband">— Творча майстерня: "Моє послання до світу"</span>
			</div>
			<div class="beschreibung"><em>Osterferienprogramm Ukrainehilfe / ПРОГРАМА ПАСХАЛЬНИХ КАНІКУЛ ПІДТРИМКА УКРАЇНЦІВ</em></div>
		</a>
	</div>`
	htmlString5 = `
	<h2>
		<a href="?bli=bla"
			title="Heinz Rudolf Kunze &amp; Verstärkung &#8211; ABGESAGT">
			<span>29.02.</span><span>Heinz Rudolf Kunze &amp; Verstärkung
				&#8211; ABGESAGT</span> </a>
	</h2>`
	htmlString6 = `
	<h2>
		<a href="../site/event/id/165"
			title="Heinz Rudolf Kunze &amp; Verstärkung &#8211; ABGESAGT">
			<span>29.02.</span><span>Heinz Rudolf Kunze &amp; Verstärkung
				&#8211; ABGESAGT</span> </a>
	</h2>`
	htmlString7 = `
	<h2>
		<a href="http://musicvenue.de/site/event/id/2024/02/29"
			title="Heinz Rudolf Kunze &amp; Verstärkung &#8211; ABGESAGT">
			<span>Feb 29</span><span>Heinz Rudolf Kunze &amp; Verstärkung
				&#8211; ABGESAGT</span> </a>
	</h2>`
)

func TestConfigIDStringDefaultPreservesExistingFormat(t *testing.T) {
	tests := []struct {
		name string
		cid  ConfigID
		want string
	}{
		{name: "empty", cid: ConfigID{}, want: ""},
		{name: "slug only", cid: ConfigID{Slug: "example-com"}, want: "example-com"},
		{name: "id only", cid: ConfigID{ID: "n5"}, want: "__n5"},
		{name: "field only", cid: ConfigID{Field: "Fabc-href-0"}, want: "__Fabc-href-0"},
		{name: "subid only", cid: ConfigID{SubID: "s1"}, want: "__s1"},
		{name: "slug id", cid: ConfigID{Slug: "example-com", ID: "n5"}, want: "example-com__n5"},
		{name: "full", cid: ConfigID{Slug: "example-com", ID: "n5", Field: "Fabc-href-0", SubID: "s1"}, want: "example-com__n5_Fabc-href-0_s1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cid.String(); got != tt.want {
				t.Fatalf("ConfigID.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompactConfigIDString(t *testing.T) {
	tests := []struct {
		name string
		cid  ConfigID
		want string
	}{
		{name: "id", cid: ConfigID{Slug: "long-url-slug", ID: "n10a"}.WithCompact(true), want: "n10a"},
		{name: "id subid", cid: ConfigID{Slug: "long-url-slug", ID: "n10a", SubID: "n10aa"}.WithCompact(true), want: "n10a-n10aa"},
		{name: "field lowercased", cid: ConfigID{Slug: "long-url-slug", ID: "n10a", Field: "F2a60128b-href-0", SubID: "n10aaa"}.WithCompact(true), want: "n10a-f2a60128b-href-0-n10aaa"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cid.String(); got != tt.want {
				t.Fatalf("compact ConfigID.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompactConfigIDStringIsValidScraperName(t *testing.T) {
	re := regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	tests := []ConfigID{
		{Slug: "long-url-slug", ID: "n5"},
		{Slug: "long-url-slug", ID: "n10a", SubID: "n10aa"},
		{Slug: "long-url-slug", ID: "n10a", Field: "F2a60128b-href-0", SubID: "n10aaa"},
	}

	for _, cid := range tests {
		got := cid.WithCompact(true).String()
		if !re.MatchString(got) {
			t.Fatalf("compact ConfigID.String() = %q, want ValidSourceScraperName-compatible shape", got)
		}
	}
}

func TestConfigIDCompactDoesNotSerializeToYAML(t *testing.T) {
	cid := ConfigID{Slug: "example-com", ID: "n5", Field: "Fabc-href-0", SubID: "s1"}
	want := "slug: example-com\nid: n5\nfield: Fabc-href-0\nsubid: s1\n"
	for _, cid := range []ConfigID{cid, cid.WithCompact(true), cid.WithCompact(false)} {
		got, err := yaml.Marshal(cid)
		if err != nil {
			t.Fatalf("yaml.Marshal(ConfigID) failed: %v", err)
		}
		if strings.Contains(string(got), "compact") {
			t.Fatalf("ConfigID YAML leaked compact field: %q", string(got))
		}
		if string(got) != want {
			t.Fatalf("ConfigID YAML = %q, want %q", string(got), want)
		}
	}
}

func TestFilterRecordMatchTrue(t *testing.T) {
	rec := output.Record{"title": "Jacob Collier - Concert"}
	s := &Scraper{
		Fields: []Field{
			{
				Name: "title",
			},
		},
		Filters: []*Filter{
			{
				Field:      "title",
				Expression: ".*Concert",
				Match:      true,
			},
		},
	}
	err := s.initializeFilters()
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	f := s.keepRecord(rec)
	if !f {
		t.Fatalf("expected 'true' but got 'false'")
	}
}

func TestFilterRecordMatchFalse(t *testing.T) {
	rec := output.Record{"title": "Jacob Collier - Cancelled"}
	s := &Scraper{
		Fields: []Field{
			{
				Name: "title",
			},
		},
		Filters: []*Filter{
			{
				Field:      "title",
				Expression: ".*Cancelled",
				Match:      false,
			},
		},
	}
	err := s.initializeFilters()
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	f := s.keepRecord(rec)
	if f {
		t.Fatalf("expected 'false' but got 'true'")
	}
}

func TestFilterRecordByDateMatchTrue(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	rec := output.Record{"date": time.Date(2023, 10, 20, 19, 1, 0, 0, loc)}
	s := &Scraper{
		Fields: []Field{
			{
				Name: "date",
				Type: "date",
			},
		},
		Filters: []*Filter{
			{
				Field:      "date",
				Expression: "> 2023-10-20T19:00",
				Match:      true,
			},
		},
	}
	err := s.initializeFilters()
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	f := s.keepRecord(rec)
	if !f {
		t.Fatalf("expected 'true' but got 'false'")
	}
}

func TestFilterRecordByDateMatchTrue2(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	rec := output.Record{"date": time.Date(2023, 10, 20, 19, 0, 0, 0, loc)}
	s := &Scraper{
		Fields: []Field{
			{
				Name: "date",
				Type: "date",
			},
		},
		Filters: []*Filter{
			{
				Field:      "date",
				Expression: "> 2023-10-20T19:00",
				Match:      true,
			},
		},
	}
	err := s.initializeFilters()
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	f := s.keepRecord(rec)
	if f {
		t.Fatalf("expected 'false' but got 'true'")
	}
}

func TestFilterRecordByDateMatchFalse(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	rec := output.Record{"date": time.Date(2023, 10, 20, 19, 1, 0, 0, loc)}
	s := &Scraper{
		Fields: []Field{
			{
				Name: "date",
				Type: "date",
			},
		},
		Filters: []*Filter{
			{
				Field:      "date",
				Expression: "> 2023-10-20T19:00",
				Match:      false,
			},
		},
	}
	err := s.initializeFilters()
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}
	f := s.keepRecord(rec)
	if f {
		t.Fatalf("expected 'false' but got 'true'")
	}
}

func TestRemoveHiddenFields(t *testing.T) {
	s := &Scraper{
		Fields: []Field{
			{
				Name: "hidden",
				Hide: true,
			},
			{
				Name: "visible",
				Hide: false,
			},
		},
	}
	rec := output.Record{"hidden": "bli", "visible": "bla"}
	r := s.removeHiddenFields(rec)
	if _, ok := r["hidden"]; ok {
		t.Fatal("the field 'hidden' should have been removed from the record")
	}
	if _, ok := r["visible"]; !ok {
		t.Fatal("the field 'visible' should not have been removed from the record")
	}
}

func TestExtractFieldText(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name: "title",
		ElementLocations: []ElementLocation{
			{
				Selector: ".artist-name",
			},
		},
	}
	event := output.Record{}
	ctx := context.Background()
	err = extractField(ctx, f, event, fetch.NewSelection(doc.Selection), "", 0)
	if err != nil {
		t.Fatalf("unexpected error while extracting the text field: %v", err)
	}
	if v, ok := event["title"]; !ok {
		t.Fatal("event doesn't contain the expected title field")
	} else {
		// ASCII Record Separator (\x1e) between multiple matched nodes
		expected := "Final Story\x1eMoment Of Madness\x1eIrony of Fate"
		if v != expected {
			t.Fatalf("expected '%s' for title but got '%s'", expected, v)
		}
	}
}

func TestExtractFieldTextEntireSubtree(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name: "title",
		ElementLocations: []ElementLocation{
			{
				Selector:      ".artist-teaser",
				EntireSubtree: true,
			},
		},
	}
	event := output.Record{}
	ctx := context.Background()
	err = extractField(ctx, f, event, fetch.NewSelection(doc.Selection), "", 0)
	if err != nil {
		t.Fatalf("unexpected error while extracting the text field: %v", err)
	}
	if v, ok := event["title"]; !ok {
		t.Fatal("event doesn't contain the expected title field")
	} else {
		// ASCII Unit Separator (\x1f) between sibling elements in entire_subtree mode
		// ASCII Record Separator (\x1e) between multiple matched nodes
		// Note: \x1f appears after each element sibling (including the last one before \x1e)
		// extractStringField always collapses runs of 2+ spaces to one, so the HTML's
		// inter-element indentation reduces to a single space after the newline.
		expected := "Final Story\x1f\n Aargau\x1f\x1eMoment Of Madness\x1f\n Basel\x1f\x1eIrony of Fate\x1f\n Bern\x1f"
		if v != expected {
			t.Fatalf("expected '%s' for title but got '%s'", expected, v)
		}
	}
}

func TestExtractFieldTextAllNodes(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name: "title",
		ElementLocations: []ElementLocation{
			{
				Selector:  ".artist-name",
				AllNodes:  true,
				Separator: ", ",
			},
		},
	}
	event := output.Record{}
	ctx := context.Background()
	err = extractField(ctx, f, event, fetch.NewSelection(doc.Selection), "", 0)
	if err != nil {
		t.Fatalf("unexpected error while extracting the text field: %v", err)
	}
	if v, ok := event["title"]; !ok {
		t.Fatal("event doesn't contain the expected title field")
	} else {
		// ASCII Record Separator (\x1e) between multiple matched nodes (all_nodes mode)
		expected := "Final Story\x1eMoment Of Madness\x1eIrony of Fate"
		if v != expected {
			t.Fatalf("expected '%s' for title but got '%s'", expected, v)
		}
	}
}

func TestExtractFieldTextRegex(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name: "time",
		ElementLocations: []ElementLocation{
			{
				Selector: "a.event-date",
				RegexExtract: RegexConfig{
					RegexPattern: "[0-9]{2}:[0-9]{2}",
				},
			},
		},
	}
	event := output.Record{}
	ctx := context.Background()
	err = extractField(ctx, f, event, fetch.NewSelection(doc.Selection), "", 0)
	if err != nil {
		t.Fatalf("unexpected error while extracting the time field: %v", err)
	}
	if v, ok := event["time"]; !ok {
		t.Fatal("event doesn't contain the expected time field")
	} else {
		expected := "20:00"
		if v != expected {
			t.Fatalf("expected '%s' for title but got '%s'", expected, v)
		}
	}
}

func TestExtractFieldUrl(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name: "url",
		Type: "url",
		ElementLocations: []ElementLocation{
			{
				Selector: "a.event-date",
			},
		},
	}
	event := output.Record{}
	ctx := context.Background()
	err = extractField(ctx, f, event, fetch.NewSelection(doc.Selection), "https://www.dachstock.ch/events", 0)
	if err != nil {
		t.Fatalf("unexpected error while extracting the time field: %v", err)
	}
	if v, ok := event["url"+URLFieldSuffix]; !ok {
		t.Fatal("event doesn't contain the expected url field")
	} else {
		expected := "https://www.dachstock.ch/events/10-03-2023-krachstock-final-story"
		if v != expected {
			t.Fatalf("expected '%s' for url but got '%s'", expected, v)
		}
	}
}

func TestExtractFieldUrlFull(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString2))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name: "url",
		Type: "url",
		ElementLocations: []ElementLocation{
			{
				Selector: "h2 > a",
			},
		},
	}
	event := output.Record{}
	ctx := context.Background()
	err = extractField(ctx, f, event, fetch.NewSelection(doc.Selection), "https://www.eventfabrik-muenchen.de/events?s=&tribe_events_cat=konzert&tribe_events_venue=&tribe_events_month=", 0)
	if err != nil {
		t.Fatalf("unexpected error while extracting the time field: %v", err)
	}
	if v, ok := event["url"+URLFieldSuffix]; !ok {
		t.Fatal("event doesn't contain the expected url field")
	} else {
		expected := "https://www.eventfabrik-muenchen.de/event/heinz-rudolf-kunze-verstaerkung-2/"
		if v != expected {
			t.Fatalf("expected '%s' for url but got '%s'", expected, v)
		}
	}
}

func TestExtractFieldUrlQuery(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString3))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name: "url",
		Type: "url",
		ElementLocations: []ElementLocation{
			{
				Selector: "h2 > a",
			},
		},
	}
	event := output.Record{}
	ctx := context.Background()
	err = extractField(ctx, f, event, fetch.NewSelection(doc.Selection), "https://www.eventfabrik-muenchen.de/events?s=&tribe_events_cat=konzert&tribe_events_venue=&tribe_events_month=", 0)
	if err != nil {
		t.Fatalf("unexpected error while extracting the time field: %v", err)
	}
	if v, ok := event["url"+URLFieldSuffix]; !ok {
		t.Fatal("event doesn't contain the expected url field")
	} else {
		expected := "https://www.eventfabrik-muenchen.de/events?bli=bla"
		if v != expected {
			t.Fatalf("expected '%s' for url but got '%s'", expected, v)
		}
	}
}

func TestExtractFieldUrlFile(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString4))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name: "url",
		Type: "url",
		ElementLocations: []ElementLocation{
			{
				Selector: "div > a",
			},
		},
	}
	event := output.Record{}
	ctx := context.Background()
	err = extractField(ctx, f, event, fetch.NewSelection(doc.Selection), "https://www.roxy.ulm.de/programm/programm.php", 0)
	if err != nil {
		t.Fatalf("unexpected error while extracting the time field: %v", err)
	}
	if v, ok := event["url"+URLFieldSuffix]; !ok {
		t.Fatal("event doesn't contain the expected url field")
	} else {
		expected := "https://www.roxy.ulm.de/programm/programm.php?m=4&j=2023&vid=4378"
		if v != expected {
			t.Fatalf("expected '%s' for url but got '%s'", expected, v)
		}
	}
}

func TestExtractFieldUrlParentDir(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString6))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name: "url",
		Type: "url",
		ElementLocations: []ElementLocation{
			{
				Selector: "h2 > a",
			},
		},
	}
	event := output.Record{}
	ctx := context.Background()
	err = extractField(ctx, f, event, fetch.NewSelection(doc.Selection), "http://point11.ch/site/home", 0)
	if err != nil {
		t.Fatalf("unexpected error while extracting the time field: %v", err)
	}
	if v, ok := event["url"+URLFieldSuffix]; !ok {
		t.Fatal("event doesn't contain the expected url field")
	} else {
		expected := "http://point11.ch/site/event/id/165"
		if v != expected {
			t.Fatalf("expected '%s' for url but got '%s'", expected, v)
		}
	}
}

func TestExtractFieldDate(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name:             "date",
		Type:             "date_time_tz_ranges",
		ElementLocations: []ElementLocation{{Selector: "a.event-date"}},
		DateLocation:     "Europe/Berlin",
	}
	event := output.Record{}
	ctx := context.Background()
	err = extractField(ctx, f, event, fetch.NewSelection(doc.Selection), "", 0)
	if err != nil {
		t.Fatalf("unexpected error while extracting the date field: %v", err)
	}
	// t.Logf("event: %#v\n", event)
	if actAny, ok := event["date"+DateTimeFieldSuffix]; !ok {
		t.Fatal("event doesn't contain the expected date field")
	} else {
		// t.Logf("actAny: %#v\n", actAny)
		actStr, ok := actAny.(string)
		if !ok {
			t.Fatal("event date field is not a string")
		}
		loc, _ := time.LoadLocation(f.DateLocation)
		expected := time.Date(2023, 3, 10, 20, 0, 0, 0, loc)
		// t.Logf("expected: %#v\n", expected)
		act, err := time.Parse(time.RFC3339, actStr)
		// t.Logf("act: %#v\n", act)
		if err != nil {
			t.Fatalf("%v is not of type time.Time, actStr: %q", err, actStr)
		}
		if !act.Equal(expected) {
			t.Fatalf("expected '%s' for date but got '%s'", expected, act)
		}
	}
}

func TestExtractFieldDate_RefTimeDrivesYearlessYear(t *testing.T) {
	// A year-less date resolves its year from the injected reference time, not
	// wall-clock or the legacy 2024 fallback — so committed extraction is
	// deterministic and replay-stable (paths-tri1).
	const html = `<a class="event-date">10 March 20:00</a>`
	f := &Field{
		Name:             "date",
		Type:             "date_time_tz_ranges",
		ElementLocations: []ElementLocation{{Selector: "a.event-date"}},
		DateLocation:     "Europe/Berlin",
	}
	parsedYear := func(refYear int) int {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
		if err != nil {
			t.Fatalf("read html: %v", err)
		}
		rec := output.Record{}
		ctx := WithRefTime(context.Background(), time.Date(refYear, 1, 1, 0, 0, 0, 0, time.UTC))
		if err := extractField(ctx, f, rec, fetch.NewSelection(doc.Selection), "", 0); err != nil {
			t.Fatalf("extractField: %v", err)
		}
		v, ok := rec["date"+DateTimeFieldSuffix].(string)
		if !ok {
			t.Fatalf("missing date field, rec=%#v", rec)
		}
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t.Fatalf("parse %q: %v", v, err)
		}
		return parsed.Year()
	}
	if y := parsedYear(2030); y != 2030 {
		t.Errorf("refTime year 2030 → parsed year %d, want 2030", y)
	}
	if y := parsedYear(2020); y != 2020 {
		t.Errorf("refTime year 2020 → parsed year %d, want 2020", y)
	}
}

// func TestExtractFieldDateWithoutYear1(t *testing.T) {
// 	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString5))
// 	if err != nil {
// 		t.Fatalf("unexpected error while reading html string: %v", err)
// 	}
// 	f := &Field{
// 		Name:             "date",
// 		Type:             "date_time_tz_ranges",
// 		ElementLocations: []ElementLocation{{Selector: "h2 > a > span"}},
// 		DateLocation:     "Europe/Berlin",
// 		GuessYear:        true,
// 	}
// 	event := output.Record{}
// 	ctx := context.Background()
// 	err = extractField(ctx, f, event, fetch.NewSelection(doc.Selection), "", 2024)
// 	if err != nil {
// 		t.Fatalf("unexpected error while extracting the date field: %v", err)
// 	}
// 	t.Logf("event: %#v\n", event)
// 	if actAny, ok := event["date"+DateTimeFieldSuffix]; !ok {
// 		t.Fatal("event doesn't contain the expected date field")
// 	} else {
// 		// t.Logf("actAny: %#v\n", actAny)
// 		actStr, ok := actAny.(string)
// 		if !ok {
// 			t.Fatal("event date field is not a string")
// 		}
// 		loc, _ := time.LoadLocation(f.DateLocation)
// 		expected := time.Date(2024, 2, 29, 0, 0, 0, 0, loc)
// 		// t.Logf("expected: %#v\n", expected)
// 		act, err := time.Parse(time.RFC3339, actStr)
// 		// t.Logf("act: %#v\n", act)
// 		if err != nil {
// 			t.Fatalf("%v is not of type time.Time", err)
// 		}
// 		if !act.Equal(expected) {
// 			t.Fatalf("expected '%s' for date but got '%s'", expected, act)
// 		}
// 	}
// }

// func TestExtractFieldDate29Feb(t *testing.T) {
// 	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString5))
// 	if err != nil {
// 		t.Fatalf("unexpected error while reading html string: %v", err)
// 	}
// 	f := &Field{
// 		Name:            "date",
// 		Type:            "date_time_tz_ranges",
// 		ElementLocation: ElementLocation{Selector: "h2 > a > span"},
// 		DateLocation:    "Europe/Berlin",
// 		GuessYear:       true,
// 	}
// 	dt, err := getDate(f, fetch.NewSelection(doc.Selection), dateDefaults{year: 2023})
// 	if err != nil {
// 		t.Fatalf("unexpected error while extracting the date field: %v", err)
// 	}
// 	if dt.Year() != 2024 {
// 		t.Fatalf("expected '2024' as year of date but got '%d'", dt.Year())
// 	}
// }

// func TestExtractFieldDateWithoutYear2(t *testing.T) {
// 	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString7))
// 	if err != nil {
// 		t.Fatalf("unexpected error while reading html string: %v", err)
// 	}
// 	dateLoc := "Europe/Berlin"
// 	fs := []*Field{{
// 		Name:             "href-0",
// 		Type:             "url",
// 		ElementLocations: []ElementLocation{{Selector: "h2 > a"}},
// 	}, {
// 		Name:             "date",
// 		Type:             "date_time_tz_ranges",
// 		ElementLocations: []ElementLocation{{Selector: "h2 > a > span"}},
// 		DateLocation:     dateLoc,
// 		GuessYear:        true,
// 	}}
// 	ctx := context.Background()
// 	event := output.Record{}
// 	for _, f := range fs {
// 		err = extractField(ctx, f, event, fetch.NewSelection(doc.Selection), "", 2025)
// 		if err != nil {
// 			t.Fatalf("unexpected error while extracting the date field: %v", err)
// 		}
// 	}
// 	// t.Logf("event: %#v\n", event)
// 	if actAny, ok := event["date"+DateTimeFieldSuffix]; !ok {
// 		t.Fatal("event doesn't contain the expected date field")
// 	} else {
// 		// t.Logf("actAny: %#v\n", actAny)
// 		actStr, ok := actAny.(string)
// 		if !ok {
// 			t.Fatal("event date field is not a string")
// 		}
// 		loc, _ := time.LoadLocation(dateLoc)
// 		expected := time.Date(2024, 2, 29, 0, 0, 0, 0, loc)
// 		// t.Logf("expected: %#v\n", expected)
// 		act, err := time.Parse(time.RFC3339, actStr)
// 		// t.Logf("act: %#v\n", act)
// 		if err != nil {
// 			t.Fatalf("%v is not of type time.Time", err)
// 		}
// 		if !act.Equal(expected) {
// 			t.Fatalf("expected '%s' for date but got '%s'", expected, act)
// 		}
// 	}
// }

func TestGuessYearSimple(t *testing.T) {
	// records dates span period around change of year
	s := &Scraper{
		Fields: []Field{
			{
				Type:      "date",
				GuessYear: true,
				Name:      "date",
			},
		},
	}
	loc, _ := time.LoadLocation("CET")
	recs := output.Records{
		{
			"date": time.Date(2023, 12, 2, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 12, 24, 21, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 1, 2, 20, 0, 0, 0, loc),
		},
	}
	expectedRecs := output.Records{
		{
			"date": time.Date(2023, 12, 2, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 12, 24, 21, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2024, 1, 2, 20, 0, 0, 0, loc),
		},
	}
	s.guessYear(recs, time.Date(2023, 11, 30, 20, 30, 0, 0, loc))
	for i, d := range recs {
		if d["date"] != expectedRecs[i]["date"] {
			t.Fatalf("expected '%v' as year of date but got '%v'", expectedRecs[i]["date"], d["date"])
		}
	}
}

func TestGuessYearUnordered(t *testing.T) {
	// records dates are not perfectly ordered and span
	// period around change of year
	s := &Scraper{
		Fields: []Field{
			{
				Type:      "date",
				GuessYear: true,
				Name:      "date",
			},
		},
	}
	loc, _ := time.LoadLocation("CET")
	recs := output.Records{
		{
			"date": time.Date(2023, 11, 2, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 12, 14, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 12, 2, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 12, 24, 21, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 1, 2, 20, 0, 0, 0, loc),
		},
	}
	expectedRecs := output.Records{
		{
			"date": time.Date(2023, 11, 2, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 12, 14, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 12, 2, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 12, 24, 21, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2024, 1, 2, 20, 0, 0, 0, loc),
		},
	}
	s.guessYear(recs, time.Date(2023, 11, 1, 20, 30, 0, 0, loc))
	for i, d := range recs {
		if d["date"] != expectedRecs[i]["date"] {
			t.Fatalf("expected '%v' as year of date but got '%v'", expectedRecs[i]["date"], d["date"])
		}
	}
}

func TestGuessYear2Years(t *testing.T) {
	// records dates span more than 2 years
	s := &Scraper{
		Fields: []Field{
			{
				Type:      "date",
				GuessYear: true,
				Name:      "date",
			},
		},
	}
	loc, _ := time.LoadLocation("CET")
	recs := output.Records{
		{
			"date": time.Date(2023, 12, 2, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 1, 14, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 5, 2, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 9, 24, 21, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 2, 2, 20, 0, 0, 0, loc),
		},
	}
	expectedRecs := output.Records{
		{
			"date": time.Date(2023, 12, 2, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2024, 1, 14, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2024, 5, 2, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2024, 9, 24, 21, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2025, 2, 2, 20, 0, 0, 0, loc),
		},
	}
	s.guessYear(recs, time.Date(2023, 11, 1, 20, 30, 0, 0, loc))
	for i, d := range recs {
		if d["date"] != expectedRecs[i]["date"] {
			t.Fatalf("expected '%v' as year of date but got '%v'", expectedRecs[i]["date"], d["date"])
		}
	}
}

func TestGuessYearStartBeforeReference(t *testing.T) {
	// records date start before given reference
	s := &Scraper{
		Fields: []Field{
			{
				Type:      "date",
				GuessYear: true,
				Name:      "date",
			},
		},
	}
	loc, _ := time.LoadLocation("CET")
	recs := output.Records{
		{
			"date": time.Date(2023, 12, 2, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 12, 24, 21, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 1, 2, 20, 0, 0, 0, loc),
		},
	}
	expectedRecs := output.Records{
		{
			"date": time.Date(2023, 12, 2, 20, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2023, 12, 24, 21, 30, 0, 0, loc),
		},
		{
			"date": time.Date(2024, 1, 2, 20, 0, 0, 0, loc),
		},
	}
	s.guessYear(recs, time.Date(2024, 1, 30, 20, 30, 0, 0, loc))
	for i, d := range recs {
		if d["date"] != expectedRecs[i]["date"] {
			t.Fatalf("expected '%v' as year of date but got '%v'", expectedRecs[i]["date"], d["date"])
		}
	}
}

const htmlStringRichDescription = `
<div class="event-page">
	<h1 class="event-title">Weekend Retreat</h1>
	<div class="event-description">
		<p>Join us for a <strong>transformative weekend</strong> exploring the Enneagram.</p>
		<p>What to bring:</p>
		<ul>
			<li>Journal and pen</li>
			<li>Comfortable clothing</li>
		</ul>
		<p>Visit <a href="https://example.com/venue">our venue</a> for directions.</p>
		<p><img src="retreat.jpg" alt="Retreat photo">Beautiful setting.</p>
	</div>
	<div class="event-summary">A weekend retreat for exploring the Enneagram.</div>
</div>`

func TestGetHTMLString(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStringRichDescription))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	e := &ElementLocation{
		Selector: ".event-description",
	}
	htmlStr, err := getHTMLString(e, fetch.NewSelection(doc.Selection))
	if err != nil {
		t.Fatalf("unexpected error in getHTMLString: %v", err)
	}
	// Should contain HTML tags, not stripped text
	if !strings.Contains(htmlStr, "<strong>") {
		t.Fatalf("expected inner HTML to contain <strong> tag but got: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "<ul>") {
		t.Fatalf("expected inner HTML to contain <ul> tag but got: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `href="https://example.com/venue"`) {
		t.Fatalf("expected inner HTML to contain link href but got: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "<img") {
		t.Fatalf("expected inner HTML to contain <img> tag but got: %s", htmlStr)
	}
}

func TestGetHTMLStringEmpty(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStringRichDescription))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	e := &ElementLocation{
		Selector: ".nonexistent",
	}
	htmlStr, err := getHTMLString(e, fetch.NewSelection(doc.Selection))
	if err != nil {
		t.Fatalf("unexpected error in getHTMLString: %v", err)
	}
	if htmlStr != "" {
		t.Fatalf("expected empty string for nonexistent selector but got: %s", htmlStr)
	}
}

func TestGetHTMLStringPlainText(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStringRichDescription))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	e := &ElementLocation{
		Selector: ".event-summary",
	}
	htmlStr, err := getHTMLString(e, fetch.NewSelection(doc.Selection))
	if err != nil {
		t.Fatalf("unexpected error in getHTMLString: %v", err)
	}
	expected := "A weekend retreat for exploring the Enneagram."
	if htmlStr != expected {
		t.Fatalf("expected %q but got %q", expected, htmlStr)
	}
}

// TestGetHTMLStringMultipleNodes verifies that getHTMLString returns inner HTML
// from ALL matched elements, not just the first. This is a regression test for a
// bug where goquery's .Html() returned only the first element, causing pages with
// an empty leading <p></p> (e.g., WordPress block editor artifacts) to lose all
// body content from subsequent <p> tags.
func TestGetHTMLStringMultipleNodes(t *testing.T) {
	html := `<div class="wrapper">
		<div class="content">
			<p></p>
			<p>First paragraph with <strong>bold</strong> text.</p>
			<p>Second paragraph.</p>
		</div>
	</div>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := &ElementLocation{
		Selector: "div.content p",
	}
	result, err := getHTMLString(e, fetch.NewSelection(doc.Selection))
	if err != nil {
		t.Fatalf("unexpected error in getHTMLString: %v", err)
	}
	// Empty <p> should be skipped; both content paragraphs should be present.
	if !strings.Contains(result, "First paragraph") {
		t.Fatalf("expected result to contain 'First paragraph' but got: %s", result)
	}
	if !strings.Contains(result, "Second paragraph") {
		t.Fatalf("expected result to contain 'Second paragraph' but got: %s", result)
	}
	if !strings.Contains(result, "<strong>bold</strong>") {
		t.Fatalf("expected result to preserve HTML tags but got: %s", result)
	}
	// Parts should be joined with HTMLNodeSeparator for downstream HTML-to-markdown conversion
	if !strings.Contains(result, HTMLNodeSeparator) {
		t.Fatalf("expected %q between parts but got: %q", HTMLNodeSeparator, result)
	}
}

func TestExtractFieldHTML(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStringRichDescription))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name: "description",
		Type: "html",
		ElementLocations: []ElementLocation{
			{
				Selector: ".event-description",
			},
		},
	}
	rec := output.Record{}
	ctx := context.Background()
	err = extractField(ctx, f, rec, fetch.NewSelection(doc.Selection), "", 0)
	if err != nil {
		t.Fatalf("unexpected error while extracting html field: %v", err)
	}
	v, ok := rec["description"]
	if !ok {
		t.Fatal("record doesn't contain the expected description field")
	}
	vStr := v.(string)
	// HTML field should return raw HTML, not plain text
	if !strings.Contains(vStr, "<strong>") {
		t.Fatalf("expected HTML with <strong> tag but got: %s", vStr)
	}
	if !strings.Contains(vStr, "<li>") {
		t.Fatalf("expected HTML with <li> tag but got: %s", vStr)
	}
}

func TestExtractFieldHTMLvsText(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStringRichDescription))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	ctx := context.Background()

	// Extract as "html"
	htmlField := &Field{
		Name: "desc_html",
		Type: "html",
		ElementLocations: []ElementLocation{
			{Selector: ".event-description"},
		},
	}
	htmlRec := output.Record{}
	err = extractField(ctx, htmlField, htmlRec, fetch.NewSelection(doc.Selection), "", 0)
	if err != nil {
		t.Fatalf("unexpected error extracting html field: %v", err)
	}

	// Extract as "text"
	textField := &Field{
		Name: "desc_text",
		Type: "text",
		ElementLocations: []ElementLocation{
			{Selector: ".event-description"},
		},
	}
	textRec := output.Record{}
	err = extractField(ctx, textField, textRec, fetch.NewSelection(doc.Selection), "", 0)
	if err != nil {
		t.Fatalf("unexpected error extracting text field: %v", err)
	}

	htmlVal := htmlRec["desc_html"].(string)
	textVal := textRec["desc_text"].(string)

	// HTML should contain tags
	if !strings.Contains(htmlVal, "<strong>") {
		t.Fatalf("html extraction should contain <strong> tag but got: %s", htmlVal)
	}

	// Text should NOT contain tags
	if strings.Contains(textVal, "<strong>") {
		t.Fatalf("text extraction should not contain <strong> tag but got: %s", textVal)
	}

	// Both should contain the actual text content
	if !strings.Contains(htmlVal, "transformative weekend") {
		t.Fatalf("html extraction should contain text content but got: %s", htmlVal)
	}
	if !strings.Contains(textVal, "transformative weekend") {
		t.Fatalf("text extraction should contain text content but got: %s", textVal)
	}
}

const htmlNestedFields = `
<div class="event-card">
	<h3 class="title">Weekend Workshop</h3>
	<span class="date">2026-04-17</span>
	<span class="cost">$295</span>
	<a class="detail-link" href="/event/workshop-1">Details</a>
	<a class="register-link" href="https://eventbrite.com/e/123">Register</a>
	<div class="contact">
		<span class="contact-name">Alice Smith</span>
		<a class="contact-email" href="mailto:alice@example.com">alice@example.com</a>
		<span class="contact-phone">555-1234</span>
	</div>
</div>`

func TestExtractSubfields_SingleMap(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlNestedFields))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fields := []Field{
		{Name: "raw_url", ElementLocations: ElementLocations{{Selector: "a.detail-link", Attr: "href"}}},
		{Name: "role", Value: "detail"},
	}
	ctx := context.Background()
	result := extractSubfields(ctx, fields, fetch.NewSelection(doc.Selection), "https://example.com")
	if result["raw_url"] != "/event/workshop-1" {
		t.Errorf("raw_url = %q, want /event/workshop-1", result["raw_url"])
	}
	if result["role"] != "detail" {
		t.Errorf("role = %q, want detail", result["role"])
	}
}

func TestExtractSubfields_NestedMap(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlNestedFields))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 3-level nesting: names → items → raw_text
	fields := []Field{
		{Name: "items", Fields: []Field{
			{Name: "raw_text", ElementLocations: ElementLocations{{Selector: "h3.title"}}},
		}},
	}
	ctx := context.Background()
	result := extractSubfields(ctx, fields, fetch.NewSelection(doc.Selection), "")
	items, ok := result["items"].(map[string]any)
	if !ok {
		t.Fatalf("items should be map[string]any, got %T", result["items"])
	}
	if items["raw_text"] != "Weekend Workshop" {
		t.Errorf("items.raw_text = %q, want Weekend Workshop", items["raw_text"])
	}
}

func TestMergeNestedField_SingleToSlice(t *testing.T) {
	rec := output.Record{}

	// First link
	mergeNestedField(rec, "links", map[string]any{"raw_url": "url1", "role": "detail"})
	if _, ok := rec["links"].(map[string]any); !ok {
		t.Fatalf("after first merge, links should be map, got %T", rec["links"])
	}

	// Second link — should convert to slice
	mergeNestedField(rec, "links", map[string]any{"raw_url": "url2", "role": "registration"})
	slice, ok := rec["links"].([]any)
	if !ok {
		t.Fatalf("after second merge, links should be []any, got %T", rec["links"])
	}
	if len(slice) != 2 {
		t.Fatalf("expected 2 items, got %d", len(slice))
	}
	m1 := slice[0].(map[string]any)
	m2 := slice[1].(map[string]any)
	if m1["role"] != "detail" || m2["role"] != "registration" {
		t.Errorf("roles: %q, %q — want detail, registration", m1["role"], m2["role"])
	}
}

func TestExtractSubfields_ConstantOnly(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlNestedFields))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fields := []Field{
		{Name: "role", Value: "detail"},
	}
	ctx := context.Background()
	result := extractSubfields(ctx, fields, fetch.NewSelection(doc.Selection), "")
	if result["role"] != "detail" {
		t.Errorf("role = %q, want detail", result["role"])
	}
}

// TestExtractSubfields_MultiSubfield verifies that a parent field with multiple
// dynamic subfields (text + email from different selectors) produces a map
// containing all subfield values.
func TestExtractSubfields_MultiSubfield(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlNestedFields))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fields := []Field{
		{Name: "contact", Fields: []Field{
			{Name: "name", ElementLocations: ElementLocations{{Selector: "span.contact-name"}}},
			{Name: "email", ElementLocations: ElementLocations{{Selector: "a.contact-email"}}},
			{Name: "phone", ElementLocations: ElementLocations{{Selector: "span.contact-phone"}}},
		}},
	}
	ctx := context.Background()
	result := extractSubfields(ctx, fields, fetch.NewSelection(doc.Selection), "")
	contact, ok := result["contact"].(map[string]any)
	if !ok {
		t.Fatalf("contact should be map[string]any, got %T", result["contact"])
	}
	if contact["name"] != "Alice Smith" {
		t.Errorf("contact.name = %q, want Alice Smith", contact["name"])
	}
	if contact["email"] != "alice@example.com" {
		t.Errorf("contact.email = %q, want alice@example.com", contact["email"])
	}
	if contact["phone"] != "555-1234" {
		t.Errorf("contact.phone = %q, want 555-1234", contact["phone"])
	}
}

// TestExtractSubfields_ConstantValue verifies that a Field with Value set
// (no CSS selector) produces a constant string in the output map.
func TestExtractSubfields_ConstantValue(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlNestedFields))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fields := []Field{
		{Name: "link", Fields: []Field{
			{Name: "raw_url", ElementLocations: ElementLocations{{Selector: "a.detail-link", Attr: "href"}}},
			{Name: "role", Value: "detail"},
			{Name: "source", Value: "scraper"},
		}},
	}
	ctx := context.Background()
	result := extractSubfields(ctx, fields, fetch.NewSelection(doc.Selection), "")
	link, ok := result["link"].(map[string]any)
	if !ok {
		t.Fatalf("link should be map[string]any, got %T", result["link"])
	}
	if link["raw_url"] != "/event/workshop-1" {
		t.Errorf("link.raw_url = %q, want /event/workshop-1", link["raw_url"])
	}
	if link["role"] != "detail" {
		t.Errorf("link.role = %q, want detail", link["role"])
	}
	if link["source"] != "scraper" {
		t.Errorf("link.source = %q, want scraper", link["source"])
	}
}

// TestExtractSubfields_MultipleSameNameEntries verifies that two Fields with
// the same name produce a slice of maps (via mergeNestedField).
func TestExtractSubfields_MultipleSameNameEntries(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlNestedFields))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fields := []Field{
		{Name: "links", Fields: []Field{
			{Name: "raw_url", ElementLocations: ElementLocations{{Selector: "a.detail-link", Attr: "href"}}},
			{Name: "role", Value: "detail"},
		}},
		{Name: "links", Fields: []Field{
			{Name: "raw_url", ElementLocations: ElementLocations{{Selector: "a.register-link", Attr: "href"}}},
			{Name: "role", Value: "registration"},
		}},
	}
	ctx := context.Background()
	result := extractSubfields(ctx, fields, fetch.NewSelection(doc.Selection), "")
	slice, ok := result["links"].([]any)
	if !ok {
		t.Fatalf("links should be []any after two same-name entries, got %T", result["links"])
	}
	if len(slice) != 2 {
		t.Fatalf("expected 2 link entries, got %d", len(slice))
	}
	link1 := slice[0].(map[string]any)
	link2 := slice[1].(map[string]any)
	if link1["raw_url"] != "/event/workshop-1" {
		t.Errorf("link1.raw_url = %q, want /event/workshop-1", link1["raw_url"])
	}
	if link1["role"] != "detail" {
		t.Errorf("link1.role = %q, want detail", link1["role"])
	}
	if link2["raw_url"] != "https://eventbrite.com/e/123" {
		t.Errorf("link2.raw_url = %q, want https://eventbrite.com/e/123", link2["raw_url"])
	}
	if link2["role"] != "registration" {
		t.Errorf("link2.role = %q, want registration", link2["role"])
	}
}

// TestExtractSubfields_ThreeLevelNesting verifies 3-level deep nesting:
// names → items → raw_text, where a Field has Fields that themselves have Fields.
func TestExtractSubfields_ThreeLevelNesting(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlNestedFields))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fields := []Field{
		{Name: "names", Fields: []Field{
			{Name: "items", Fields: []Field{
				{Name: "raw_text", ElementLocations: ElementLocations{{Selector: "h3.title"}}},
			}},
		}},
	}
	ctx := context.Background()
	result := extractSubfields(ctx, fields, fetch.NewSelection(doc.Selection), "")

	names, ok := result["names"].(map[string]any)
	if !ok {
		t.Fatalf("names should be map[string]any, got %T", result["names"])
	}
	items, ok := names["items"].(map[string]any)
	if !ok {
		t.Fatalf("names.items should be map[string]any, got %T", names["items"])
	}
	if items["raw_text"] != "Weekend Workshop" {
		t.Errorf("names.items.raw_text = %q, want Weekend Workshop", items["raw_text"])
	}
}

// TestMergeNestedField_ThirdAppend verifies that mergeNestedField correctly
// appends a third map to an existing slice (not just the single→slice conversion).
func TestMergeNestedField_ThirdAppend(t *testing.T) {
	rec := output.Record{}

	mergeNestedField(rec, "links", map[string]any{"url": "url1"})
	mergeNestedField(rec, "links", map[string]any{"url": "url2"})
	mergeNestedField(rec, "links", map[string]any{"url": "url3"})

	slice, ok := rec["links"].([]any)
	if !ok {
		t.Fatalf("after three merges, links should be []any, got %T", rec["links"])
	}
	if len(slice) != 3 {
		t.Fatalf("expected 3 items, got %d", len(slice))
	}
	for i, want := range []string{"url1", "url2", "url3"} {
		m := slice[i].(map[string]any)
		if m["url"] != want {
			t.Errorf("slice[%d].url = %q, want %q", i, m["url"], want)
		}
	}
}

func TestScrapeIMDB(t *testing.T) {

}
