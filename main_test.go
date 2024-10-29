// Uses file-driven tests in Go.
// See: https://eli.thegreenplace.net/2022/file-driven-testing-in-go/
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/generate"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/gosimple/slug"
	"github.com/nsf/jsondiff"
	"github.com/sergi/go-diff/diffmatchpatch"
)

var htmlSuffix = ".html"
var configSuffix = ".yml"
var jsonSuffix = ".json"

var writeActualTestOutputs = true
var testOutputDir = "/tmp/goskyr/main_test/"
var testInputDir = "testdata/"

// urlsForTestnames stores the live URLs used to create tests. They are needed to resolve relative paths for event pages that appear in event-list pages. To add new tests, run:
//
//	go run main.go --debug generate https://books.toscrape.com --fields-vary --batch --do-subpages --output-dir /tmp/goskyr/main/
//
// and copy the new directory within /tmp/goskyr/main/ to testdata.
var urlsForTestnames = map[string]string{
	"books-toscrape-com":             "https://books.toscrape.com",
	"quotes-toscrape-com":            "https://quotes.toscrape.com",
	"realpython-github-io-fake-jobs": "https://realpython.github.io/fake-jobs/",
	"webscraper-io-test-sites-e-commerce-allinone-computers-tablets": "https://webscraper.io/test-sites/e-commerce/allinone/computers/tablets",
	"www-scrapethissite-com-pages-simple":                            "https://www.scrapethissite.com/pages/simple",
}

func TestGenerate(t *testing.T) {
	testnames := []string{}
	for testname := range urlsForTestnames {
		testnames = append(testnames, testname)
	}
	sort.Strings(testnames)

	for _, testname := range testnames {
		t.Run(testname, func(t *testing.T) {
			TGenerateAllConfigs(t, testname)
		})
	}
}

func TGenerateAllConfigs(t *testing.T, testname string) {
	inputDir := testInputDir + "scraping"
	outputDir := testOutputDir + "scraping"
	_, err := os.Stat(filepath.Join(inputDir, testname+"_subpages"))
	doSubpages := err != nil

	opts, err := generate.InitOpts(generate.ConfigOptions{
		Batch:       true,
		InputDir:    inputDir,
		URL:         urlsForTestnames[testname],
		DoSubpages:  doSubpages,
		MinOccs:     []int{5, 10, 20},
		OnlyVarying: true,
		OutputDir:   outputDir,
		File:        filepath.Join(inputDir, testname+".html"),
	})
	if err != nil {
		t.Fatalf("error initializing page options: %v", err)
	}

	pageConfigs, err := generate.ConfigurationsForPage(opts)
	if err != nil {
		t.Fatalf("error generating config: %v", err)
	}
	TGenerateConfigs(t, testname, pageConfigs, inputDir, outputDir)

	if doSubpages {
		subPageConfigs, err := generate.ConfigurationsForAllSubpages(opts, pageConfigs)
		if err != nil {
			t.Fatalf("error generating config: %v", err)
		}
		TGenerateConfigs(t, testname, subPageConfigs, inputDir, outputDir)
	}
}

func TGenerateConfigs(t *testing.T, testname string, configs map[string]*scrape.Config, inputDir string, outputDir string) { // , pageSuffix string) {
	ids := []string{}
	for id := range configs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		expPath := filepath.Join(inputDir, testname+"_configs", id+configSuffix)
		if _, err := os.Stat(expPath); err != nil {
			continue
		}
		exp, err := utils.ReadStringFile(expPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(id, func(t *testing.T) {
			TGenerateConfig(t, testname, configs[id], outputDir, exp)
		})
	}
}

func readExpectedOutput(expPath string) (string, error) {
	if _, err := os.Stat(expPath); err != nil {
		return "", nil
	}
	exp, err := utils.ReadStringFile(expPath)
	if err != nil {
		return "", err
	}
	return exp, nil
}

func TGenerateConfig(t *testing.T, testname string, config *scrape.Config, outputDir string, exp string) {
	act := config.String()
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(string(exp), act, false)
	if len(diffs) == 1 && diffs[0].Type == diffmatchpatch.DiffEqual {
		return
	}
	diffStr := ""
	for _, d := range diffs {
		diffStr += d.Text + "\n"
	}

	id := config.ID.String()
	diffPath := filepath.Join(outputDir, testname+"_configs", id+configSuffix+".diff")
	if err := utils.WriteStringFile(diffPath, diffStr); err != nil {
		t.Fatalf("failed to write diff to %q: %v", diffPath, err)
	}
	t.Errorf("actual output (%d) does not match expected output (%d) and wrote diff to %q", len(act), len(exp), diffPath)

	if writeActualTestOutputs {
		actPath := filepath.Join(outputDir, testname+"_configs", id+".actual"+configSuffix)
		if err := utils.WriteStringFile(actPath, act); err != nil {
			t.Fatalf("failed to write actual test output to %q: %v", actPath, err)
		}
		fmt.Printf("wrote to actPath: %q\n", actPath)
	}
}

func TestScrape(t *testing.T) {
	testnames := []string{}
	for testname := range urlsForTestnames {
		testnames = append(testnames, testname)
	}
	sort.Strings(testnames)

	for _, testname := range testnames {
		t.Run(testname, func(t *testing.T) {
			TScrapeWithAllConfigs(t, testname)
		})
	}
}

func TScrapeWithAllConfigs(t *testing.T, testname string) {
	glob := filepath.Join(testInputDir, "scraping", testname+"_configs", "*"+configSuffix)
	// fmt.Printf("glob: %s\n", glob)
	allPaths, err := filepath.Glob(glob)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for _, path := range allPaths {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	// startLen := len(filepath.Join(testInputDir, "scraping", cid.Slug+"__"))
	// endLen := len(".p") + len(configSuffix)
	for _, path := range paths {
		// fmt.Printf("path: %q\n", path)
		// id := path[startLen : len(path)-endLen]
		// fmt.Printf("id: %q\n", id)
		config, err := scrape.ReadConfig(path)
		if err != nil {
			t.Fatalf("cannot open config file path at %q: %v", path, err)
		}
		t.Run(config.ID.String(), func(t *testing.T) {
			TScrapeWithConfig(t, testname, config)
		})
	}
}

func getItems(testname string, config *scrape.Config) (output.ItemMaps, error) {
	if config.ID.ID != "" && config.ID.Field == "" && config.ID.SubID == "" {
		// We're looking at an event list page scraper. Scrape the page in the outer directory.
		htmlPath := filepath.Join(testInputDir, "scraping", testname+htmlSuffix)
		return scrape.Page(&config.Scrapers[0], &config.Global, true, htmlPath)
	} else if config.ID.ID == "" && config.ID.Field != "" && config.ID.SubID != "" {
		// We're looking at an event page scraper. Scrape the page in this directory.
		htmlPath := filepath.Join(testInputDir, "scraping", testname+"_configs", config.ID.Slug+"__"+config.ID.Field+htmlSuffix)
		return scrape.Page(&config.Scrapers[0], &config.Global, true, htmlPath)
	} else {
		// We're looking at a combined event list and page scraper. Scrape both pages.
		htmlPath := filepath.Join(testInputDir, "scraping", testname+htmlSuffix)
		itemMaps, err := scrape.Page(&config.Scrapers[0], &config.Global, true, htmlPath)
		if err != nil {
			return nil, err
		}
		f := &fetch.FileFetcher{}
		fetchFn := func(u string) (*goquery.Document, error) {
			u = strings.TrimPrefix(u, "http://")
			u = strings.TrimPrefix(u, "https://")
			u = "file://" + filepath.Join(testInputDir, "scraping", testname+"_subpages", slug.Make(u)+".html")
			return fetch.GQDocument(f, u, nil)
		}
		err = scrape.Subpages(config, &config.Scrapers[1], itemMaps, fetchFn)
		return itemMaps, err
	}
}

func TScrapeWithConfig(t *testing.T, testname string, config *scrape.Config) {
	itemMaps, err := getItems(testname, config)
	if err != nil {
		t.Fatalf("failed to get items for scraper config %q: %v", config.ID.String(), err)
	}

	act, err := json.MarshalIndent(itemMaps, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal json: %v", err)
	}

	// Each input file is expected to have a "golden output" file, with the
	// same path except the .input extension is replaced by the golden suffix.
	// jsonfile := path[:len(path)-len(configSuffix)] + jsonSuffix
	jsonPath := filepath.Join(testInputDir, "scraping", testname+"_configs", config.ID.String()+jsonSuffix)
	exp, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("error reading golden file at %q: %v", jsonPath, err)
	}

	// Compare the JSON outputs
	opts := jsondiff.DefaultConsoleOptions()
	diff, diffStr := jsondiff.Compare(act, exp, &opts)

	// Check if there are any differences
	if diff == jsondiff.FullMatch {
		return
	}

	id := config.ID.String()
	diffPath := filepath.Join(testOutputDir, "scraping", testname+"_configs", id+jsonSuffix+".diff")
	if err := utils.WriteStringFile(diffPath, diffStr); err != nil {
		t.Fatalf("failed to write diff to %q: %v", diffStr, err)
	}
	t.Errorf("actual output (%d) does not match expected output (%d) and wrote diff to %q", len(act), len(exp), diffPath)

	if writeActualTestOutputs {
		actPath := filepath.Join(testOutputDir, "scraping", testname+"_configs", id+".actual"+jsonSuffix)
		if err := utils.WriteBytesFile(actPath, act); err != nil {
			t.Fatalf("failed to write actual test output to %q: %v", actPath, err)
		}
		fmt.Printf("wrote to actPath: %q\n", actPath)
	}
}
