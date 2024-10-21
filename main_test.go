// Demonstrates file-driven tests in Go.
//
// Eli Bendersky [https://eli.thegreenplace.net]
// This code is in the public domain.
//
// See: https://eli.thegreenplace.net/2022/file-driven-testing-in-go/
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	for testname := range urlsForTestnames {
		// Each path turns into a test: the test name is the filename without the
		// extension.
		t.Run(testname, func(t *testing.T) {
			GenerateTest(t, testname)
		})
	}
}

func GenerateTest(t *testing.T, testname string) {
	inputDir := testInputDir + "scraping"
	outputDir := testOutputDir + "scraping"
	opts := &generate.ConfigOptions{
		Batch:       true,
		InputDir:    inputDir,
		InputURL:    "file://" + filepath.Join(inputDir, testname+htmlSuffix),
		DoSubpages:  true,
		MinOccs:     []int{5, 10, 20},
		OnlyVarying: true,
		OutputDir:   outputDir,
		URL:         urlsForTestnames[testname],
	}
	// slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))

	pageConfigs, err := generate.ConfigurationsForPage(opts, false)
	if err != nil {
		t.Fatalf("error generating config: %v", err)
	}

	for id, pageConfig := range pageConfigs {
		// fmt.Printf("looking at pageConfig with id: %q\n", id)
		CheckGenerate(t, testname, pageConfig, id, outputDir, inputDir)
	}

	subPageConfigs, err := generate.ConfigurationsForAllSubpages(opts, pageConfigs)
	if err != nil {
		t.Fatalf("error generating config: %v", err)
	}

	for id, subPageConfig := range subPageConfigs {
		// fmt.Printf("looking at subPageConfig with id: %q\n", id)
		CheckGenerate(t, testname, subPageConfig, id, outputDir, inputDir)
	}
}

func CheckGenerate(t *testing.T, testname string, pageConfig *scrape.Config, id string, outputDir string, inputDir string) {
	expPath := filepath.Join(inputDir, testname+"__"+id+".yml")
	// fmt.Printf("looking at expPath: %q\n", expPath)
	if _, err := os.Stat(expPath); err != nil {
		return
	}
	exp, err := utils.ReadStringFile(expPath)
	if err != nil {
		t.Fatal(err)
	}

	act := pageConfig.String()
	if writeActualTestOutputs {
		actPath := filepath.Join(outputDir, fmt.Sprintf("%s_%s_actual%s", testname, id, configSuffix))
		if err := utils.WriteStringFile(actPath, act); err != nil {
			t.Fatalf("failed to write actual test output to %q: %v", actPath, err)
		}
	}

	fmt.Printf("checking id: %q\n", id)
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(string(exp), act, false)
	// fmt.Printf("len(diffs): %d\n", len(diffs))
	// fmt.Printf("diffs:\n%v\n", diffs)
	if len(diffs) == 1 && diffs[0].Type == diffmatchpatch.DiffEqual {
		return
	}
	diffPath := filepath.Join(outputDir, testname+"_config.diff")
	if err := utils.WriteStringFile(diffPath, fmt.Sprintf("%#v", diffs)); err != nil {
		t.Fatalf("failed to write diff to %q: %v", diffPath, err)
	}
	t.Fatalf("actual output (%d) does not match expected output (%d) and wrote diff to %q", len(act), len(exp), diffPath)
	fmt.Printf("wrote to actPath: %q\n", actPath)
}

func TestScrape(t *testing.T) {
	// Find the paths of all input files in the data directories.
	allPaths := []string{}
	for _, glob := range []string{
		filepath.Join("testdata/scraping", "*"+configSuffix),
		// filepath.Join("testdata/chicago", "*"+configSuffix),
	} {
		paths, err := filepath.Glob(glob)
		if err != nil {
			t.Fatal(err)
		}
		allPaths = append(allPaths, paths...)
	}

	for _, path := range allPaths {
		_, filename := filepath.Split(path)
		testname := filename[:len(filename)-len(configSuffix)]

		// Each path turns into a test: the test name is the filename without the
		// extension.
		t.Run(testname, func(t *testing.T) {
			conf, err := scrape.ReadConfig(path)
			if err != nil {
				t.Fatalf("cannot open config file path at %q: %v", path, err)
			}

			allItems := output.ItemMaps{}
			for i, s := range conf.Scrapers {
				items, err := s.GetItems(&conf.Global, true)
				if err != nil {
					t.Fatalf("failed to get items for scraper config %d at %q: %v", i, path, err)
				}
				allItems = append(allItems, items...)
			}

			act, err := json.MarshalIndent(allItems, "", "  ")
			if err != nil {
				t.Fatalf("failed to marshal json: %v", err)
			}

			if writeActualTestOutputs {
				actPath := filepath.Join(testOutputDir, testname+jsonSuffix)
				if err := utils.WriteStringFile(actPath, string(act)); err != nil {
					t.Fatalf("failed to write actual test output to %q: %v", actPath, err)
				}
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
			if diff != jsondiff.FullMatch {
				diffPath := filepath.Join(testOutputDir, testname+".diff")
				if err := utils.WriteStringFile(diffPath, diffStr); err != nil {
					t.Fatalf("failed to write diff to %q: %v", diffPath, err)
				}
				t.Fatalf("JSON output does not match expected output and wrote diff to: %q", diffPath)
			}
		})
	}
}
