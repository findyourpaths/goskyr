package main

import (
	"path/filepath"
	"sort"
)

type maintest struct {
	url      string
	required string
}

// urlsForTestNames stores the live URLs used to create tests. They are needed to resolve relative paths for event pages that appear in event-list pages. To add new tests, run:
//
//		go run ./cmd/goskyr --debug generate 'https://basic-field.com' --cache-input-dir testdata/regression --cache-output-dir testdata/regression --config-output-dir testdata/regression --do-detail-pages=false
//
//	 or
//
//	 rm -r /tmp/goskyr/main/basic-fields-w-link-com_configs/; \
//	 time go run ./cmd/goskyr --log-level=debug generate 'https://basic-fields-w-link.com' --cache-input-dir testdata/regression --do-detail-pages=false
//
// regenerate with
//
//	go run main.go --debug regenerate
var testsByHostSlugByCategory = map[string]map[string][]maintest{
	"regression": {
		// "basic-detail-pages-com":            []maintest{{url: "https://basic-detail-pages.com"}},
		// "basic-detail-pages-w-links-com":    []maintest{{url: "https://basic-detail-pages-w-links.com"}},
		// "basic-detail-pages-w-static-com":   []maintest{{url: "https://basic-detail-pages-w-static.com"}},
		// "basic-field-com":                   []maintest{{url: "https://basic-field.com"}},
		// "basic-field-w-div-com":             []maintest{{url: "https://basic-field-w-div.com"}},
		// "basic-fields-w-div-com":            []maintest{{url: "https://basic-fields-w-div.com"}},
		// "basic-fields-w-div-w-div-com":      []maintest{{url: "https://basic-fields-w-div-w-div.com"}},
		// "basic-fields-w-div-w-link-div-com": []maintest{{url: "https://basic-fields-w-div-w-link-div.com"}},
		// "basic-fields-w-link-com":           []maintest{{url: "https://basic-fields-w-link.com"}},
		// "basic-fields-w-link-div-com":       []maintest{{url: "https://basic-fields-w-link-div.com"}},
		// "basic-fields-w-style-com":          []maintest{{url: "https://basic-fields-w-style.com"}},
		// "basic-fields-w-table-com":          []maintest{{url: "https://basic-fields-w-table.com"}},
		// "basic-fields-w-table-in-table-com": []maintest{{url: "https://basic-fields-w-table-in-table.com"}},
		// "css-class-with-special-chars-com":  []maintest{{url: "https://css-class-with-special-chars.com"}},
		// // "dates-com": []maintest{
		// // 	{url: "https://dates.com/all"},
		// // 	{url: "https://dates.com/most"}},
		// "fields-w-a-com": []maintest{{url: "https://fields-w-a.com"}},

		"real-table-w-extra-rows-com": []maintest{{url: "https://real-table-w-extra-rows.com"}},
	},
	// "scraping": {
	// 	"books-toscrape-com":   []maintest{{url: "https://books.toscrape.com", required: "Soumission"}},
	// 	"quotes-toscrape-com":  []maintest{{url: "https://quotes.toscrape.com", required: "Imperfection"}},
	// 	"realpython-github-io": []maintest{{url: "https://realpython.github.io/fake-jobs"}},
	// 	"webscraper-io":        []maintest{{url: "https://webscraper.io/test-sites/e-commerce/allinone/computers/tablets", required: "Android"}},
	// 	// "scrapethissite-com": []maintest{
	// 	// 	{url: "https://www.scrapethissite.com/pages/forms"},
	// 	// 	{url: "https://www.scrapethissite.com/pages/simple"}},
	// },
}

func sortedTestCategories() []string {
	rs := []string{}
	for r := range testsByHostSlugByCategory {
		rs = append(rs, r)
	}
	sort.Strings(rs)
	return rs
}

func sortedTestHostSlugs(cat string) []string {
	rs := []string{}
	for r := range testsByHostSlugByCategory[cat] {
		rs = append(rs, r)
	}
	sort.Strings(rs)
	return rs
}

var testOutputDir = "/tmp/goskyr/main/"

var testInputDir = "../../testdata/"

// var testInputDir = "testdata/"

var htmlSuffix = ".html"
var configSuffix = ".yml"
var jsonSuffix = ".json"

func testDirPathsWithPattern(testCatDir string, name string, pattern string) ([]string, error) {
	glob := filepath.Join(testCatDir, name, pattern)
	// fmt.Printf("glob: %s\n", glob)
	rs, err := filepath.Glob(glob)
	if err != nil {
		return nil, err
	}
	sort.Strings(rs)
	return rs, nil
}
