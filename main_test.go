// Uses file-driven tests in Go.
// See: https://eli.thegreenplace.net/2022/file-driven-testing-in-go/
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/generate"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/nsf/jsondiff"
	"github.com/sergi/go-diff/diffmatchpatch"
)

var htmlSuffix = ".html"
var configSuffix = ".yml"
var jsonSuffix = ".json"

var writeActualTestOutputs = true

func TestGenerate(t *testing.T) {
	f, err := os.Create("test-generate.prof")
	if err != nil {
		t.Fatalf("error initializing pprof: %v", err)
	}
	defer f.Close()
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	dirs := []string{}
	for dir := range urlsForTestnamesByDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		t.Run(dir, func(t *testing.T) {
			TGenerateAllDirConfigs(t, dir)
		})
	}
}

func TGenerateAllDirConfigs(t *testing.T, dir string) {
	testnames := []string{}
	for testname := range urlsForTestnamesByDir[dir] {
		testnames = append(testnames, testname)
	}
	sort.Strings(testnames)

	for _, testname := range testnames {
		t.Run(testname, func(t *testing.T) {
			TGenerateAllConfigs(t, dir, testname)
		})
	}
}

func TGenerateAllConfigs(t *testing.T, dir string, testname string) {
	inputDir := testInputDir + dir
	outputDir := testOutputDir + dir

	glob := filepath.Join(inputDir, testname+"_cache", "*")
	// fmt.Printf("glob: %q\n", glob)
	paths, err := filepath.Glob(glob)
	if err != nil {
		t.Fatalf("error getting cache input paths with glob %q: %v", glob, err)
	}
	doSubpages := len(paths) > 1
	fmt.Printf("in test %q, doing subpages: %t\n", testname, doSubpages)

	opts, err := generate.InitOpts(generate.ConfigOptions{
		Batch:         true,
		CacheInputDir: inputDir,
		DoSubpages:    doSubpages,
		MinOccs:       []int{5, 10, 20},
		OnlyVarying:   true,
		RenderJS:      true,
		URL:           urlsForTestnamesByDir[dir][testname],
	})
	if err != nil {
		t.Fatalf("error initializing page options: %v", err)
	}

	cs, gqdocsByURL, err := generate.ConfigurationsForPage(opts, nil)
	if err != nil {
		t.Fatalf("error generating page configs: %v", err)
	}
	TGenerateConfigs(t, testname, cs, inputDir, outputDir)

	if doSubpages {
		subCs, _, err := generate.ConfigurationsForAllSubpages(opts, cs, gqdocsByURL)
		if err != nil {
			t.Fatalf("error generating subpage configs: %v", err)
		}
		TGenerateConfigs(t, testname, subCs, inputDir, outputDir)
	}
}

func TGenerateConfigs(t *testing.T, testname string, cs map[string]*scrape.Config, inputDir string, outputDir string) {
	ids := []string{}
	for id := range cs {
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
			TGenerateConfig(t, testname, cs[id], outputDir, exp)
		})
	}
}

func TGenerateConfig(t *testing.T, testname string, config *scrape.Config, outputDir string, exp string) {
	actC := config
	// Strip the event list scraper paginators, which are generated but don't appear in the expected data.
	if config.ID.ID != "" && config.ID.Field == "" && config.ID.SubID == "" {
		actC.Scrapers[0] = config.Scrapers[0]
		actC.Scrapers[0].Paginators = nil
	}
	act := actC.String()

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

func TScrape(t *testing.T) {
	dirs := []string{}
	for dir := range urlsForTestnamesByDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		t.Run(dir, func(t *testing.T) {
			TScrapeWithAllDirConfigs(t, dir)
		})
	}
}

func TScrapeWithAllDirConfigs(t *testing.T, dir string) {
	testnames := []string{}
	for testname := range urlsForTestnamesByDir[dir] {
		testnames = append(testnames, testname)
	}
	sort.Strings(testnames)

	for _, testname := range testnames {
		t.Run(testname, func(t *testing.T) {
			TScrapeWithAllConfigs(t, dir, testname)
		})
	}
}

func TScrapeWithAllConfigs(t *testing.T, dir string, testname string) {
	glob := filepath.Join(testInputDir, dir, testname+"_configs", "*"+configSuffix)
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

	// startLen := len(filepath.Join(testInputDir, dir, cid.Slug+"__"))
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
			TScrapeWithConfig(t, dir, testname, config)
		})
	}
}

func getItems(dir string, testname string, config *scrape.Config) (output.ItemMaps, error) {
	if config.ID.ID != "" && config.ID.Field == "" && config.ID.SubID == "" {
		// We're looking at an event list page scraper. Scrape the page in the outer directory.
		htmlPath := filepath.Join(testInputDir, dir, testname+htmlSuffix)
		return scrape.Page(&config.Scrapers[0], &config.Global, true, htmlPath)
	} else if config.ID.ID == "" && config.ID.Field != "" && config.ID.SubID != "" {
		// We're looking at an event page scraper. Scrape the page in this directory.
		htmlPath := filepath.Join(testInputDir, dir, testname+"_configs", config.ID.Slug+"__"+config.ID.Field+htmlSuffix)
		return scrape.Page(&config.Scrapers[0], &config.Global, true, htmlPath)
	} else {
		// We're looking at a combined event list and page scraper. Scrape both pages.
		htmlPath := filepath.Join(testInputDir, dir, testname+htmlSuffix)
		itemMaps, err := scrape.Page(&config.Scrapers[0], &config.Global, true, htmlPath)
		if err != nil {
			return nil, err
		}
		f := &fetch.FileFetcher{}
		fetchFn := func(u string) (*goquery.Document, error) {
			u = "file://" + filepath.Join(testInputDir, dir, testname+"_subpages", fetch.MakeURLStringSlug(u)+".html")
			return fetch.GQDocument(f, u, nil)
		}
		err = scrape.Subpages(config, &config.Scrapers[1], itemMaps, fetchFn)
		return itemMaps, err
	}
}

func TScrapeWithConfig(t *testing.T, dir string, testname string, config *scrape.Config) {
	itemMaps, err := getItems(dir, testname, config)
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
	jsonPath := filepath.Join(testInputDir, dir, testname+"_configs", config.ID.String()+jsonSuffix)
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
	diffPath := filepath.Join(testOutputDir, dir, testname+"_configs", id+jsonSuffix+".diff")
	if err := utils.WriteStringFile(diffPath, diffStr); err != nil {
		t.Fatalf("failed to write diff to %q: %v", diffStr, err)
	}
	t.Errorf("actual output (%d) does not match expected output (%d) and wrote diff to %q", len(act), len(exp), diffPath)

	if writeActualTestOutputs {
		actPath := filepath.Join(testOutputDir, dir, testname+"_configs", id+".actual"+jsonSuffix)
		if err := utils.WriteBytesFile(actPath, act); err != nil {
			t.Fatalf("failed to write actual test output to %q: %v", actPath, err)
		}
		fmt.Printf("wrote to actPath: %q\n", actPath)
	}
}
