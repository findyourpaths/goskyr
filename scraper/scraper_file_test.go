// Demonstrates file-driven tests in Go.
//
// Eli Bendersky [https://eli.thegreenplace.net]
// This code is in the public domain.
//
// See: https://eli.thegreenplace.net/2022/file-driven-testing-in-go/
package scraper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nsf/jsondiff"
)

var configSuffix = "_config.yml"
var goldenSuffix = ".json"

var writeActualTestOutputs = false

func TestScraperFiles(t *testing.T) {
	// Find the paths of all input files in the data directory.
	paths, err := filepath.Glob(filepath.Join("testdata", "*"+configSuffix))
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range paths {
		_, filename := filepath.Split(path)
		testname := filename[:len(filename)-len(configSuffix)]

		// Each path turns into a test: the test name is the filename without the
		// extension.
		t.Run(testname, func(t *testing.T) {
			// source, err := os.ReadFile(path)
			// if err != nil {
			// 	t.Fatal("error reading source file:", err)
			// }

			// >>> This is the actual code under test.
			// configPath := filepath.Join("testdata", path+configSuffix)
			conf, err := NewConfig(path)
			if err != nil {
				t.Fatalf("cannot open config file path at %q: %v", path, err)
			}
			if len(conf.Scrapers) != 1 {
				t.Fatalf("looking for a single scraper in config at %q, instead found %d", path, len(conf.Scrapers))
			}

			s := conf.Scrapers[0]
			items, err := s.GetItems(&conf.Global, false)
			if err != nil {
				t.Fatalf("failed to get items for config at %q: %v", path, err)
			}

			actual, err := json.MarshalIndent(items, "", "  ")
			if err != nil {
				t.Fatalf("failed to marshal json: %v", err)
			}

			if writeActualTestOutputs {
				actualPath := "/tmp/" + testname + goldenSuffix
				if err := WriteStringFile(actualPath, string(actual)); err != nil {
					t.Fatalf("failed to write actual test output to %q: %v", actualPath, err)
				}
			}
			// <<<

			// Each input file is expected to have a "golden output" file, with the
			// same path except the .input extension is replaced by the golden suffix.
			goldenfile := filepath.Join("testdata", testname+goldenSuffix)
			expected, err := os.ReadFile(goldenfile)
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
