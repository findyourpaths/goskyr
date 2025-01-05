package scrape

import (
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/findyourpaths/goskyr/date"
	"github.com/findyourpaths/goskyr/output"
)

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
)

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
	err = extractField(f, event, doc.Selection, "")
	if err != nil {
		t.Fatalf("unexpected error while extracting the text field: %v", err)
	}
	if v, ok := event["title"]; !ok {
		t.Fatal("event doesn't contain the expected title field")
	} else {
		expected := "Final Story"
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
	err = extractField(f, event, doc.Selection, "")
	if err != nil {
		t.Fatalf("unexpected error while extracting the text field: %v", err)
	}
	if v, ok := event["title"]; !ok {
		t.Fatal("event doesn't contain the expected title field")
	} else {
		expected := `Final Story
                                                    Aargau`
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
	err = extractField(f, event, doc.Selection, "")
	if err != nil {
		t.Fatalf("unexpected error while extracting the text field: %v", err)
	}
	if v, ok := event["title"]; !ok {
		t.Fatal("event doesn't contain the expected title field")
	} else {
		expected := "Final Story, Moment Of Madness, Irony of Fate"
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
	err = extractField(f, event, doc.Selection, "")
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
	err = extractField(f, event, doc.Selection, "https://www.dachstock.ch/events")
	if err != nil {
		t.Fatalf("unexpected error while extracting the time field: %v", err)
	}
	if v, ok := event["url__Purl"]; !ok {
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
	err = extractField(f, event, doc.Selection, "https://www.eventfabrik-muenchen.de/events?s=&tribe_events_cat=konzert&tribe_events_venue=&tribe_events_month=")
	if err != nil {
		t.Fatalf("unexpected error while extracting the time field: %v", err)
	}
	if v, ok := event["url__Purl"]; !ok {
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
	err = extractField(f, event, doc.Selection, "https://www.eventfabrik-muenchen.de/events?s=&tribe_events_cat=konzert&tribe_events_venue=&tribe_events_month=")
	if err != nil {
		t.Fatalf("unexpected error while extracting the time field: %v", err)
	}
	if v, ok := event["url__Purl"]; !ok {
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
	err = extractField(f, event, doc.Selection, "https://www.roxy.ulm.de/programm/programm.php")
	if err != nil {
		t.Fatalf("unexpected error while extracting the time field: %v", err)
	}
	if v, ok := event["url__Purl"]; !ok {
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
	err = extractField(f, event, doc.Selection, "http://point11.ch/site/home")
	if err != nil {
		t.Fatalf("unexpected error while extracting the time field: %v", err)
	}
	if v, ok := event["url__Purl"]; !ok {
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
		Name: "date",
		Type: "date",
		Components: []DateComponent{
			{
				Covers: date.CoveredDateParts{
					Day:   true,
					Month: true,
					Year:  true,
					Time:  true,
				},
				ElementLocation: ElementLocation{
					Selector: "a.event-date",
				},
				Layout: []string{
					"Mon, 02.01.2006 - 15:04",
				},
			},
		},
		DateLocation: "Europe/Berlin",
	}
	event := output.Record{}
	err = extractField(f, event, doc.Selection, "")
	if err != nil {
		t.Fatalf("unexpected error while extracting the date field: %v", err)
	}
	if v, ok := event["date"]; !ok {
		t.Fatal("event doesn't contain the expected date field")
	} else {
		loc, _ := time.LoadLocation(f.DateLocation)
		expected := time.Date(2023, 3, 10, 20, 0, 0, 0, loc)
		vTime, ok := v.(time.Time)
		if !ok {
			t.Fatalf("%v is not of type time.Time", err)
		}
		if !vTime.Equal(expected) {
			t.Fatalf("expected '%s' for date but got '%s'", expected, vTime)
		}
	}
}

func TestExtractFieldDateTransform(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name: "date",
		Type: "date",
		Components: []DateComponent{
			{
				Covers: date.CoveredDateParts{
					Day:   true,
					Month: true,
					Year:  true,
					Time:  true,
				},
				ElementLocation: ElementLocation{
					Selector: "a.event-date",
				},
				Transform: []TransformConfig{
					{
						TransformType: "regex-replace",
						RegexPattern:  "\\.",
						Replacement:   "/",
					},
				},
				Layout: []string{
					"Mon, 02/01/2006 - 15:04",
				},
			},
		},
		DateLocation: "Europe/Berlin",
	}
	event := output.Record{}
	err = extractField(f, event, doc.Selection, "")
	if err != nil {
		t.Fatalf("unexpected error while extracting the date field: %v", err)
	}
	if v, ok := event["date"]; !ok {
		t.Fatal("event doesn't contain the expected date field")
	} else {
		loc, _ := time.LoadLocation(f.DateLocation)
		expected := time.Date(2023, 3, 10, 20, 0, 0, 0, loc)
		vTime, ok := v.(time.Time)
		if !ok {
			t.Fatalf("%v is not of type time.Time", err)
		}
		if !vTime.Equal(expected) {
			t.Fatalf("expected '%s' for date but got '%s'", expected, vTime)
		}
	}
}

func TestExtractFieldDate29Feb(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString5))
	if err != nil {
		t.Fatalf("unexpected error while reading html string: %v", err)
	}
	f := &Field{
		Name: "date",
		Type: "date",
		Components: []DateComponent{
			{
				Covers: date.CoveredDateParts{
					Day:   true,
					Month: true,
				},
				ElementLocation: ElementLocation{
					Selector: "h2 > a > span",
				},
				Layout: []string{
					"02.01.",
				},
			},
		},
		DateLocation: "Europe/Berlin",
		GuessYear:    true,
	}
	dt, err := getDate(f, doc.Selection, dateDefaults{year: 2023})
	if err != nil {
		t.Fatalf("unexpected error while extracting the date field: %v", err)
	}
	if dt.Year() != 2024 {
		t.Fatalf("expected '2024' as year of date but got '%d'", dt.Year())
	}
}

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

func TestScrapeIMDB(t *testing.T) {

}
