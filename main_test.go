// Demonstrates file-driven tests in Go.
//
// Eli Bendersky [https://eli.thegreenplace.net]
// This code is in the public domain.
//
// See: https://eli.thegreenplace.net/2022/file-driven-testing-in-go/
package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/findyourpaths/goskyr/scraper"
	"github.com/nsf/jsondiff"
	"github.com/sergi/go-diff/diffmatchpatch"
)

var htmlSuffix = ".html"
var configSuffix = "_config.yml"
var jsonSuffix = ".json"

var writeActualTestOutputs = true

func TestAutoconfig(t *testing.T) {
	// Find the paths of all input files in the data directory.
	paths, err := filepath.Glob(filepath.Join("testdata", "*"+htmlSuffix))
	if err != nil {
		t.Fatal(err)
	}

	return
	for _, path := range paths {
		_, filename := filepath.Split(path)
		testname := filename[:len(filename)-len(htmlSuffix)]

		// Each path turns into a test: the test name is the filename without the
		// extension.
		t.Run(testname, func(t *testing.T) {
			// >>> This is the actual code under test.
			opts := mainOpts{
				GenerateConfig: "file://" + path,
				ConfigLoc:      "",
			}
			log.Printf("opts.GenerateConfig: %q", opts.GenerateConfig)
			actual, err := GenerateConfig(opts)

			if writeActualTestOutputs {
				actualPath := "/tmp/" + testname + configSuffix
				if err := WriteStringFile(actualPath, string(actual)); err != nil {
					t.Fatalf("failed to write actual test output to %q: %v", actualPath, err)
				}
			}
			// <<<

			// Each input file is expected to have a "golden output" file, with the
			// same path except the .input extension is replaced by the golden suffix.
			configFile := filepath.Join("testdata", testname+configSuffix)
			expected, err := os.ReadFile(configFile)
			if err != nil {
				t.Fatal("error reading golden file:", err)
			}

			dmp := diffmatchpatch.New()
			diffs := dmp.DiffMain(string(expected), actual, false)
			if len(diffs) != 0 {
				t.Errorf("actual output (%d) does not match expected output (%d)", len(actual), len(expected))
				// t.Errorf("JSON output does not match expected output:\n%s", dmp.DiffPrettyText(diffs))
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

			allItems := []map[string]interface{}{}
			for i, s := range conf.Scrapers {
				// TODO: handle scrapers that require javascript.
				if s.RenderJs {
					continue
				}
				items, err := s.GetItems(&conf.Global, true)
				if err != nil {
					t.Fatalf("failed to get items for scraper config %d at %q: %v", i, path, err)
				}
				allItems = append(allItems, items...)
			}

			actual, err := json.MarshalIndent(allItems, "", "  ")
			if err != nil {
				t.Fatalf("failed to marshal json: %v", err)
			}

			if writeActualTestOutputs {
				actualPath := "/tmp/" + testname + jsonSuffix
				if err := WriteStringFile(actualPath, string(actual)); err != nil {
					t.Fatalf("failed to write actual test output to %q: %v", actualPath, err)
				}
			}

			// Each input file is expected to have a "golden output" file, with the
			// same path except the .input extension is replaced by the golden suffix.
			jsonfile := path[:len(path)-len(configSuffix)] + jsonSuffix
			expected, err := os.ReadFile(jsonfile)
			if err != nil {
				t.Fatal("error reading golden file:", err)
			}

			// Compare the JSON outputs
			opts := jsondiff.DefaultConsoleOptions()
			diff, diffStr := jsondiff.Compare(actual, expected, &opts)

			// Check if there are any differences
			if diff != jsondiff.FullMatch {
				t.Errorf("JSON output does not match expected output:\n%s", diffStr)
			}
		})
	}
}

// WriteStringFile writes the given file contents to the given path.
func WriteStringFile(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0770); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(content)
	return err
}
