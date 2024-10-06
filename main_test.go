// Demonstrates file-driven tests in Go.
//
// Eli Bendersky [https://eli.thegreenplace.net]
// This code is in the public domain.
//
// See: https://eli.thegreenplace.net/2022/file-driven-testing-in-go/
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scraper"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/nsf/jsondiff"
	"github.com/sergi/go-diff/diffmatchpatch"
)

var htmlSuffix = ".html"
var configSuffix = "_config.yml"
var jsonSuffix = ".json"

var writeActualTestOutputs = true
var testOutputDir = "/tmp/goskyr/testdata/"

var printDiffs = false

func TestAutoconfig(t *testing.T) {
	allPaths := []string{}
	for _, glob := range []string{
		filepath.Join("testdata", "*"+htmlSuffix),
		filepath.Join("testdata/chicago", "*"+htmlSuffix),
	} {
		paths, err := filepath.Glob(glob)
		if err != nil {
			t.Fatal(err)
		}
		allPaths = append(allPaths, paths...)
	}

	for _, path := range allPaths {
		dir, filename := filepath.Split(path)
		testname := filename[:len(filename)-len(htmlSuffix)]

		// Each path turns into a test: the test name is the filename without the
		// extension.
		t.Run(testname, func(t *testing.T) {

			glob := filepath.Join(dir, testname+"_*"+configSuffix)
			expPathGlob, err := filepath.Glob(glob)
			if err != nil {
				t.Fatal(err)
			}
			if len(expPathGlob) == 0 {
				t.Fatalf("expected to find config file with glob: %q", glob)
			}

			expPath := expPathGlob[0]
			starIdx := strings.Index(glob, "*")
			id := expPath[starIdx : starIdx+(len(expPath)-(starIdx+len(configSuffix)))]
			// fmt.Printf("looking at id: %q\n", id)
			// fmt.Printf("looking at expPath: %q\n", expPath)
			// fmt.Printf("looking at cs[id]: %v\n", cs[id])

			exp, err := utils.ReadStringFile(expPath)
			if err != nil {
				t.Fatal(err)
			}

			opts := mainOpts{
				GenerateForURL: "file://" + path,
				ConfigFile:     filepath.Join(testOutputDir, testname+configSuffix),
				Batch:          true,
				Min:            5,
				FieldsVary:     true,
			}
			cs, err := GenerateConfigs(opts)
			if err != nil {
				t.Fatalf("error generating config file: %v", err)
			}
			if cs[id] == nil {
				keys := []string{}
				for key := range cs {
					keys = append(keys, key)
				}
				t.Fatalf("can't find config with id: %q in keys: %#v", id, keys)
			}
			act := cs[id].String()
			// if writeActualTestOutputs {
			// 	actPath := filepath.Join(testOutputDir, fmt.Sprintf("%s_%s%s", testname, id, configSuffix))
			// 	if err := utils.WriteStringFile(actPath, act); err != nil {
			// 		t.Fatalf("failed to write actual test output to %q: %v", actPath, err)
			// 	}
			// }

			dmp := diffmatchpatch.New()
			diffs := dmp.DiffMain(string(exp), act, false)
			if len(diffs) != 0 && diffs[0].Type != diffmatchpatch.DiffEqual {
				if !printDiffs {
					diffs = nil
				}
				t.Fatalf("actual output (%d) does not match expected output (%d):\n%v", len(act), len(exp), diffs)
			}
		})
	}
}

func TestScraper(t *testing.T) {
	// Find the paths of all input files in the data directories.
	allPaths := []string{}
	for _, glob := range []string{
		filepath.Join("testdata", "*"+configSuffix),
		filepath.Join("testdata/chicago", "*"+configSuffix),
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
			conf, err := scraper.NewConfig(path)
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
				if !printDiffs {
					diffStr = ""
				}
				t.Fatalf("JSON output does not match expected output:\n%s", diffStr)
			}
		})
	}
}
