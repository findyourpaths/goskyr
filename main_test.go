// Uses file-driven tests in Go.
// See: https://eli.thegreenplace.net/2022/file-driven-testing-in-go/
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
var testOutputDir = "/tmp/goskyr/main_test/"
var testInputDir = "testdata/"

var urlsForTestnames = map[string]string{
	"https-realpython-github-io-fake-jobs":                                 "https://realpython.github.io/fake-jobs/",
	"https-webscraper-io-test-sites-e-commerce-allinone-computers-tablets": "https://webscraper.io/test-sites/e-commerce/allinone/computers/tablets",
}

func TestGenerate(t *testing.T) {
	// slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))
	for testname := range urlsForTestnames {
		// Each path turns into a test: the test name is the filename without the
		// extension.
		t.Run(testname, func(t *testing.T) {
			TGenerateAllConfigs(t, scrape.ConfigID{Base: testname})
		})
	}
}

func TGenerateAllConfigs(t *testing.T, cid scrape.ConfigID) {
	inputDir := testInputDir + "scraping"
	outputDir := testOutputDir + "scraping"
	opts := &generate.ConfigOptions{
		Batch:       true,
		InputDir:    inputDir,
		InputURL:    "file://" + filepath.Join(inputDir, cid.Base+htmlSuffix),
		DoSubpages:  true,
		MinOccs:     []int{5, 10, 20},
		OnlyVarying: true,
		OutputDir:   outputDir,
		InputFile:   urlsForTestnames[cid.Base],
	}

	pageConfigs, err := generate.ConfigurationsForPage(opts)
	if err != nil {
		t.Fatalf("error generating config: %v", err)
	}
	TGenerateConfigs(t, cid, pageConfigs, inputDir, outputDir, "p")

	subPageConfigs, err := generate.ConfigurationsForAllSubpages(opts, pageConfigs)
	if err != nil {
		t.Fatalf("error generating config: %v", err)
	}
	TGenerateConfigs(t, cid, subPageConfigs, inputDir, outputDir, "sp")
}

func TGenerateConfigs(t *testing.T, cid scrape.ConfigID, configs map[string]*scrape.Config, inputDir string, outputDir string, pageSuffix string) {
	for id, config := range configs {
		expPath := filepath.Join(inputDir, cid.Base+"__"+id+"."+pageSuffix+".yml")
		// fmt.Printf("expPath: %s\n", expPath)
		if _, err := os.Stat(expPath); err != nil {
			continue
		}
		exp, err := utils.ReadStringFile(expPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(id, func(t *testing.T) {
			TGenerateConfig(t, cid, config, id, outputDir, exp)
		})
	}
}

func readExpectedOutput(expPath string) (string, error) {
	// fmt.Printf("looking at pageConfig with id: %q\n", id)
	// fmt.Printf("looking at expPath: %q\n", expPath)
	if _, err := os.Stat(expPath); err != nil {
		return "", nil
	}
	exp, err := utils.ReadStringFile(expPath)
	if err != nil {
		return "", err
	}
	return exp, nil
}

func TGenerateConfig(t *testing.T, cid scrape.ConfigID, config *scrape.Config, id string, outputDir string, exp string) {
	act := config.String()
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(string(exp), act, false)
	// fmt.Printf("len(diffs): %d\n", len(diffs))
	// fmt.Printf("diffs:\n%v\n", diffs)
	if len(diffs) == 1 && diffs[0].Type == diffmatchpatch.DiffEqual {
		return
	}
	diffPath := filepath.Join(outputDir, cid.Base+"_config.diff")
	if err := utils.WriteStringFile(diffPath, fmt.Sprintf("%#v", diffs)); err != nil {
		t.Fatalf("failed to write diff to %q: %v", diffPath, err)
	}

	if writeActualTestOutputs {
		actPath := filepath.Join(outputDir, fmt.Sprintf("%s__%s_actual%s", cid.Base, id, configSuffix))
		if err := utils.WriteStringFile(actPath, act); err != nil {
			t.Fatalf("failed to write actual test output to %q: %v", actPath, err)
		}
		fmt.Printf("wrote to actPath: %q\n", actPath)
	}
	t.Fatalf("actual output (%d) does not match expected output (%d) and wrote diff to %q", len(act), len(exp), diffPath)
}

func TestScrape(t *testing.T) {
	// slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))
	for testname := range urlsForTestnames {
		// Each path turns into a test: the test name is the filename without the
		// extension.
		t.Run(testname, func(t *testing.T) {
			TScrapeWithAllConfigs(t, scrape.ConfigID{Base: testname})
		})
	}
}

func TScrapeWithAllConfigs(t *testing.T, cid scrape.ConfigID) {
	glob := filepath.Join(testInputDir, "scraping", cid.Base+"__*.p"+configSuffix)
	fmt.Printf("glob: %s\n", glob)
	pageConfigPaths, err := filepath.Glob(glob)
	if err != nil {
		t.Fatal(err)
	}
	startLen := len(filepath.Join(testInputDir, "scraping", cid.Base+"__"))
	endLen := len(".p") + len(configSuffix)
	for _, path := range pageConfigPaths {
		// fmt.Printf("path: %q\n", path)
		id := path[startLen : len(path)-endLen]
		// fmt.Printf("id: %q\n", id)
		config, err := scrape.ReadConfig(path)
		if err != nil {
			t.Fatalf("cannot open config file path at %q: %v", path, err)
		}
		t.Run(id, func(t *testing.T) {
			TScrapeWithConfig(t, cid, config, path, id, false)
		})
	}

	endLen = len(".sp") + len(configSuffix)
	var subPageConfigPaths []string
	subPageConfigPaths, err = filepath.Glob(filepath.Join(testInputDir, "scraping", cid.Base+"__*.sp"+configSuffix))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range subPageConfigPaths {
		// fmt.Printf("path: %q\n", path)
		// fmt.Printf("id: %q\n", id)
		config, err := scrape.ReadConfig(path)
		if err != nil {
			t.Fatalf("cannot open config file path at %q: %v", path, err)
		}
		id := path[startLen : len(path)-endLen]
		t.Run(id, func(t *testing.T) {
			TScrapeWithConfig(t, cid, config, path, id, true)
		})
	}
}

func TScrapeWithConfig(t *testing.T, cid scrape.ConfigID, config *scrape.Config, path string, id string, isSubpage bool) {
	allItems := output.ItemMaps{}
	for i, s := range config.Scrapers {
		if isSubpage {
			fmt.Printf("id: %s\n", id)
			fieldIDStart := strings.Index(id, "_") + 1
			fieldID := id[fieldIDStart : fieldIDStart+strings.Index(id[fieldIDStart:], "_")]
			fmt.Printf("fieldID: %s\n", fieldID)
			fmt.Printf("s.InputURL before: %s\n", s.InputURL)
			s.InputURL = "file://" + filepath.Join(testInputDir, "scraping", cid.Base+"__"+fieldID+htmlSuffix) // strings.TrimSuffix(s.InputURL, htmlSuffix) + "__" + fieldID + htmlSuffix
			fmt.Printf("s.InputURL after: %s\n", s.InputURL)
		}
		items, err := s.GetItems(&config.Global, true)
		if err != nil {
			t.Fatalf("failed to get items for scraper config %d at %q: %v", i, path, err)
		}
		fmt.Printf("len(items): %d\n", len(items))
		allItems = append(allItems, items...)
	}

	act, err := json.MarshalIndent(allItems, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal json: %v", err)
	}

	// Each input file is expected to have a "golden output" file, with the
	// same path except the .input extension is replaced by the golden suffix.
	jsonfile := path[:len(path)-len(configSuffix)] + jsonSuffix
	exp, err := os.ReadFile(jsonfile)
	if err != nil {
		t.Fatalf("error reading golden file: %v", err)
	}

	// Compare the JSON outputs
	opts := jsondiff.DefaultConsoleOptions()
	diff, diffStr := jsondiff.Compare(act, exp, &opts)

	// Check if there are any differences
	if diff == jsondiff.FullMatch {
		return
	}

	diffPath := filepath.Join(testOutputDir, cid.Base+".diff")
	if err := utils.WriteStringFile(diffPath, diffStr); err != nil {
		t.Fatalf("failed to write diff to %q: %v", diffPath, err)
	}
	if writeActualTestOutputs {
		actPath := filepath.Join(testOutputDir, fmt.Sprintf("%s__%s_actual%s", cid.Base, id, jsonSuffix))
		if err := utils.WriteStringFile(actPath, string(act)); err != nil {
			t.Fatalf("failed to write actual test output to %q: %v", actPath, err)
		}
		fmt.Printf("wrote to actPath: %q\n", actPath)
	}
	t.Fatalf("JSON output does not match expected output and wrote diff to: %q", diffPath)
}
