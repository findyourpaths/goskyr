// Uses file-driven tests in Go.
// See: https://eli.thegreenplace.net/2022/file-driven-testing-in-go/
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"

	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/generate"
	"github.com/findyourpaths/goskyr/observability"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/nsf/jsondiff"
	"github.com/sergi/go-diff/diffmatchpatch"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

var writeActualTestOutputs = true

func TestGenerate(t *testing.T) {
	f, err := os.Create("test-generate.prof")
	if err != nil {
		t.Fatalf("error initializing pprof: %v", err)
	}
	defer f.Close()
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	// logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// slog.SetDefault(logger)
	// output.SetDefaultLogger(filepath.Join(testOutputDir, "test-generate_log.txt"), slog.LevelDebug)

	fetch.ErrorIfPageNotInCache = true

	for _, cat := range sortedTestCategories() {
		t.Run(cat, func(t *testing.T) {
			testGenerateCategory(t, cat)
		})
	}
}

func testGenerateCategory(t *testing.T, cat string) {
	for _, hostSlug := range sortedTestHostSlugs(cat) {
		t.Run(hostSlug, func(t *testing.T) {
			testGenerateCategoryHost(t, cat, hostSlug)
		})
	}
}

func testGenerateCategoryHost(t *testing.T, cat string, hostSlug string) {
	for _, test := range testsByHostSlugByCategory[cat][hostSlug] {
		pageSlug := fetch.MakeURLStringSlug(test.url)
		t.Run(pageSlug, func(t *testing.T) {
			testGenerateCategoryHostPage(t, cat, hostSlug, test)
		})
	}
}

func testGenerateCategoryHostPage(t *testing.T, cat string, hostSlug string, test maintest) {
	testCatInputDir := filepath.Join(testInputDir, cat)
	testCatOutputDir := filepath.Join(testOutputDir, cat)

	ctx := context.Background()
	endFn, err := observability.InitAll(ctx, testCatOutputDir)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/paths/internal/event").Start(ctx, "test."+hostSlug)

	// Metering
	defer func() {
		observability.Add(ctx, observability.Instruments.Test, 1,
			attribute.String("int.test_cat_input_dir", testCatInputDir),
			attribute.String("int.test_cat_output_dir", testCatOutputDir),
		)
		span.End()

		endFn()
	}()

	pageSlug := fetch.MakeURLStringSlug(test.url)
	ps, err := testDirPathsWithPattern(testCatInputDir, hostSlug+"_configs", pageSlug+"*"+"href"+"*"+configSuffix)
	if err != nil {
		t.Fatalf("error getting cache directory paths: %v", err)
	}
	doDetailPages := len(ps) > 0
	// fmt.Println("doDetailPages", doDetailPages)

	opts, err := generate.InitOpts(generate.ConfigOptions{
		Batch: true,
		// CacheInputParentDir: testInputDir,
		DoDetailPages: doDetailPages,
		MinOccs:       []int{5, 10, 20},
		// MinOccs:           []int{5},
		OnlyVaryingFields: true,
		RenderJS:          true,
		RequireString:     test.required,
		URL:               test.url,
	})
	if err != nil {
		t.Fatalf("error initializing page options: %v", err)
	}

	output.SetDefaultLogger(filepath.Join(testCatOutputDir, hostSlug+"_configs", "test-generate_log.txt"), slog.LevelDebug)
	var cache fetch.Cache
	cache = fetch.NewURLFileCache(nil, testCatInputDir, false)
	cache = fetch.NewURLFileCache(cache, testCatOutputDir, true)
	cache = fetch.NewMemoryCache(cache)
	cs, err := generate.ConfigurationsForPage(ctx, cache, opts)
	if err != nil {
		t.Fatalf("error generating page configs: %v", err)
	}

	csByID := map[string]*scrape.Config{}
	for _, c := range cs {
		// fmt.Println("found config with ID", c.ID.String())
		csByID[c.ID.String()] = c
	}

	if doDetailPages {
		subCs, err := generate.ConfigurationsForAllDetailPages(ctx, cache, opts, cs)
		if err != nil {
			t.Fatalf("error generating detail page configs: %v", err)
		}
		// fmt.Println("len(subCs)", len(subCs))
		for _, c := range subCs {
			// fmt.Println("found subconfig with ID", c.ID.String())
			csByID[c.ID.String()] = c
		}
	}

	testGenerateCategoryHostPageConfigs(t, cat, hostSlug, test, csByID)
}

func testGenerateCategoryHostPageConfigs(t *testing.T, cat string, hostSlug string, test maintest, csByID map[string]*scrape.Config) {
	testCatInputDir := filepath.Join(testInputDir, cat)

	pageSlug := fetch.MakeURLStringSlug(test.url)
	wantPs, err := testDirPathsWithPattern(testCatInputDir, hostSlug+"_configs", pageSlug+"*"+configSuffix)
	if err != nil {
		t.Fatal(err)
	}

	for _, wantP := range wantPs {
		// fmt.Println("in testGenerateCategoryHostPageConfigs()", "expP", expP)
		if _, err := os.Stat(wantP); err != nil {
			t.Fatal(err)
		}
		want, err := utils.ReadStringFile(wantP)
		if err != nil {
			t.Fatal(err)
		}

		id := filepath.Base(wantP)
		id = strings.TrimSuffix(id, filepath.Ext(id))
		// fmt.Println("in testGenerateCategoryHostPageConfigs()", "id", id)
		c := csByID[id]
		if c == nil {
			t.Fatal(fmt.Errorf("no config found with ID: %q", id))
		}

		t.Run(id, func(t *testing.T) {
			testGenerateCategoryHostPageConfig(t, cat, hostSlug, c, want)
		})
	}
}

func testGenerateCategoryHostPageConfig(t *testing.T, cat string, hostSlug string, config *scrape.Config, want string) {
	testCatOutputDir := filepath.Join(testOutputDir, cat)

	gotC := config
	// Strip the event list scraper paginators, which are generated but don't appear in the expected data.
	if config.ID.ID != "" && config.ID.Field == "" && config.ID.SubID == "" {
		gotC.Scrapers[0] = config.Scrapers[0]
		gotC.Scrapers[0].Paginators = nil
	}
	got := gotC.String()

	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(string(want), got, false)
	if len(diffs) == 1 && diffs[0].Type == diffmatchpatch.DiffEqual {
		return
	}
	diffStr := ""
	for _, d := range diffs {
		diffStr += d.Text + "\n"
	}

	id := config.ID.String()
	diffP := filepath.Join(testCatOutputDir, hostSlug+"_configs", id+configSuffix+".diff")
	if err := utils.WriteStringFile(diffP, diffStr); err != nil {
		t.Fatalf("failed to write diff to %q: %v", diffP, err)
	}
	t.Errorf("actual output (%d) does not match expected output (%d) and wrote diff to %q", len(got), len(want), diffP)

	if writeActualTestOutputs {
		actP := filepath.Join(testCatOutputDir, hostSlug+"_configs", id+".actual"+configSuffix)
		if err := utils.WriteStringFile(actP, got); err != nil {
			t.Fatalf("failed to write actual test output to %q: %v", actP, err)
		}
		fmt.Printf("wrote to actPath: %q\n", actP)
	}
}

func TestScrape(t *testing.T) {
	f, err := os.Create("test-scrape.prof")
	if err != nil {
		t.Fatalf("error initializing pprof: %v", err)
	}
	defer f.Close()
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	output.SetDefaultLogger(filepath.Join(testOutputDir, "test-generate_log.txt"), slog.LevelWarn)

	for _, cat := range sortedTestCategories() {
		t.Run(cat, func(t *testing.T) {
			testScrapeCategory(t, cat)
		})
	}
}

func testScrapeCategory(t *testing.T, cat string) {
	for _, hostSlug := range sortedTestHostSlugs(cat) {
		t.Run(hostSlug, func(t *testing.T) {
			testScrapeCategoryHost(t, cat, hostSlug)
		})
	}
}

func testScrapeCategoryHost(t *testing.T, cat string, hostSlug string) {
	for _, test := range testsByHostSlugByCategory[cat][hostSlug] {
		pageSlug := fetch.MakeURLStringSlug(test.url)
		t.Run(pageSlug, func(t *testing.T) {
			testScrapeCategoryHostPage(t, cat, hostSlug, test)
		})
	}
}

func testScrapeCategoryHostPage(t *testing.T, cat string, hostSlug string, test maintest) {
	testCatInputDir := filepath.Join(testInputDir, cat)
	pageSlug := fetch.MakeURLStringSlug(test.url)

	ps, err := testDirPathsWithPattern(testCatInputDir, hostSlug+"_configs", pageSlug+"*"+configSuffix)
	if err != nil {
		t.Fatalf("error getting config directory paths: %v", err)
	}
	// fmt.Println("in testScrapeCategoryHost()", "len(ps)", len(ps))
	for _, p := range ps {
		c, err := scrape.ReadConfig(p)
		if err != nil {
			t.Fatalf("cannot open config file path at %q: %v", p, err)
		}
		t.Run(c.ID.String(), func(t *testing.T) {
			testScrapeCategoryHostPageConfig(t, cat, hostSlug, c)
		})
	}
}

func testScrapeCategoryHostPageConfig(t *testing.T, cat string, hostSlug string, c *scrape.Config) {
	testCatInputDir := filepath.Join(testInputDir, cat)
	testCatOutputDir := filepath.Join(testOutputDir, cat)

	recs, err := getRecords(cat, hostSlug, c)
	if err != nil {
		t.Fatalf("failed to get items for scraper config %q: %v", c.ID.String(), err)
	}

	got, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal json: %v", err)
	}

	// Each input file is expected to have a "golden output" file, with the
	// same path except the .input extension is replaced by the golden suffix.
	// jsonfile := path[:len(path)-len(configSuffix)] + jsonSuffix
	jsonP := filepath.Join(testCatInputDir, hostSlug+"_configs", c.ID.String()+jsonSuffix)
	want, err := os.ReadFile(jsonP)
	if err != nil {
		t.Fatalf("error reading golden file at %q: %v", jsonP, err)
	}

	// Compare the JSON outputs
	opts := jsondiff.DefaultConsoleOptions()
	diff, diffStr := jsondiff.Compare(got, want, &opts)

	// Check if there are any differences
	if diff == jsondiff.FullMatch {
		return
	}

	id := c.ID.String()
	diffP := filepath.Join(testCatOutputDir, hostSlug+"_configs", id+jsonSuffix+".diff")
	if err := utils.WriteStringFile(diffP, diffStr); err != nil {
		t.Fatalf("failed to write diff to %q: %v", diffStr, err)
	}
	t.Errorf("actual output (%d) does not match expected output (%d) and wrote diff to %q", len(got), len(want), diffP)

	if writeActualTestOutputs {
		gotP := filepath.Join(testCatOutputDir, hostSlug+"_configs", id+".actual"+jsonSuffix)
		if err := utils.WriteBytesFile(gotP, got); err != nil {
			t.Fatalf("failed to write actual test output to %q: %v", gotP, err)
		}
		fmt.Printf("wrote to actPath: %q\n", gotP)
	}
}

func getRecords(cat string, hostSlug string, c *scrape.Config) (output.Records, error) {
	testCatInputDir := filepath.Join(testInputDir, cat)
	testCatOutputDir := filepath.Join(testOutputDir, cat)

	var cache fetch.Cache
	cache = fetch.NewURLFileCache(nil, testCatInputDir, false)
	cache = fetch.NewURLFileCache(cache, testCatOutputDir, true)
	cache = fetch.NewMemoryCache(cache)

	ctx := context.Background()
	if c.ID.ID != "" && c.ID.Field == "" && c.ID.SubID == "" {
		// We're looking at an event list page scraper. Scrape the page in the outer directory.
		return scrape.Page(ctx, cache, c, &c.Scrapers[0], &c.Global, true, "")
	} else if c.ID.ID == "" && c.ID.Field != "" && c.ID.SubID != "" {
		// We're looking at an event page scraper. Scrape the page in this directory.
		return scrape.Page(ctx, cache, c, &c.Scrapers[0], &c.Global, true, "")
	} else {
		// We're looking at a combined event list and page scraper. Scrape both pages.
		recs, err := scrape.Page(ctx, cache, c, &c.Scrapers[0], &c.Global, true, "")
		if err != nil {
			return nil, err
		}
		err = scrape.DetailPages(ctx, cache, c, &c.Scrapers[1], recs, "")
		return recs, err
	}
}
