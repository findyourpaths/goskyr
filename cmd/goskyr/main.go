package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/alecthomas/kong"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/generate"
	"github.com/findyourpaths/goskyr/ml"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
)

var version = "dev"

func main() {
	cli := CLI{
		Globals: Globals{
			// Version: VersionFlag("0.1.1"),
		},
	}

	ctx := kong.Parse(&cli,
		kong.Name("goskyr"),
		kong.Description("A configurable command-line web scraper."),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
		}),
		kong.Vars{
			"version": "0.0.1",
		})

	var logLevel slog.Level
	switch strings.ToLower(cli.Globals.LogLevel) {
	case "debug":
		logLevel = slog.LevelDebug
		scrape.DoDebug = true
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	err := ctx.Run(&cli.Globals)
	ctx.FatalIfErrorf(err)
}

type CLI struct {
	Globals

	ExtractFeatures ExtractFeaturesCmd `cmd:"" help:"Extract ML features based on the given configuration file"`
	Generate        GenerateCmd        `cmd:"" help:"Automatically generate a configuration file for the given URL"`
	Regenerate      RegenerateCmd      `cmd:"" help:"Automatically regenerate test data"`
	Scrape          ScrapeCmd          `cmd:"" help:"Scrape"`
}

type Globals struct {
	LogLevel string `short:"l" default:"info" help:"Control log level: debug, info, or warn"`
}

type ExtractFeaturesCmd struct {
	File string `arg:"" help:"The location of the configuration. Can be a directory containing config files or a single config file."`

	ExtractFeatures string `short:"e" long:"extract" description:"Write extracted features to the given file in csv format."`
	WordsDir        string `short:"w" default:"word-lists" description:"The directory that contains a number of files containing words of different languages. This is needed for the ML part (use with -e or -b)."`
}

func (cmd *ExtractFeaturesCmd) Run(globals *Globals) error {
	conf, err := scrape.ReadConfig(cmd.File)
	if err != nil {
		return fmt.Errorf("error reading config: %v", err)
	}

	if err := ml.ExtractFeatures(conf, cmd.ExtractFeatures, cmd.WordsDir); err != nil {
		return fmt.Errorf("error extracting features: %v", err)
	}
	return nil
}

type GenerateCmd struct {
	URL string `arg:"" help:"Automatically generate a config file for the given input url."`

	Batch               bool   `short:"b" long:"batch" default:true help:"Run batch (not interactively) to generate the config file."`
	DoSubpages          bool   `short:"s" long:"subpages" default:true help:"Whether to generate configurations for subpages as well."`
	FieldsVary          bool   `long:"fieldsvary" default:true help:"Only show fields that have varying values across the list of records."`
	MinOcc              int    `short:"m" long:"min" help:"The minimum number of records on a page. This is needed to filter out noise. Works in combination with the -g flag."`
	CacheInputDir       string `default:"/tmp/goskyr/main/" help:"Parent directory for the directory containing cached copies of the html page and linked pages."`
	CacheOutputDir      string `default:"/tmp/goskyr/main/" help:"Parent directory for the directory that will receive cached copies of the html page and linked pages."`
	ConfigOutputDir     string `default:"/tmp/goskyr/main/" help:"Parent directory for the directory that will recieve configuration files."`
	Offline             bool   `default:false help:"Run offline and don't fetch pages."`
	PretrainedModelPath string `short:"p" long:"pretrained" description:"Use a pre-trained ML model to infer names of extracted fields. Works in combination with the -g flag."`
	RenderJs            bool   `short:"r" long:"renderjs" default:true help:"Render JS before generating a configuration file. Works in combination with the -g flag."`
	RequireString       string `long:"require-string" help:"Require a candidate configuration to extract the given text in order for it to be generated."`
	WordsDir            string `short:"w" default:"word-lists" description:"The directory that contains a number of files containing words of different languages. This is needed for the ML part (use with -e or -b)."`
}

func (cmd *GenerateCmd) Run(globals *Globals) error {
	f, err := os.Create("generate.prof")
	if err != nil {
		return err
	}
	defer f.Close()
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	minOccs := []int{5, 10, 20}
	if cmd.MinOcc != 0 {
		minOccs = []int{cmd.MinOcc}
	}

	opts, err := generate.InitOpts(generate.ConfigOptions{
		Batch:           cmd.Batch,
		CacheInputDir:   cmd.CacheInputDir,
		CacheOutputDir:  cmd.CacheOutputDir,
		ConfigOutputDir: cmd.ConfigOutputDir,
		DoSubpages:      cmd.DoSubpages,
		URL:             cmd.URL,
		MinOccs:         minOccs,
		ModelName:       cmd.PretrainedModelPath,
		Offline:         cmd.Offline,
		OnlyVarying:     cmd.FieldsVary,
		RenderJS:        cmd.RenderJs,
		RequireString:   cmd.RequireString,
		WordsDir:        cmd.WordsDir,
	})
	if err != nil {
		return fmt.Errorf("error initializing page options: %v", err)
	}

	cs, gqdocsByURL, err := generate.ConfigurationsForPage(opts)
	if err != nil {
		return fmt.Errorf("error generating page configs: %v", err)
	}
	fmt.Printf("Generated %d page configurations\n", len(cs))

	var subCs map[string]*scrape.Config
	if opts.DoSubpages {
		if subCs, gqdocsByURL, err = generate.ConfigurationsForAllSubpages(opts, cs, gqdocsByURL, nil); err != nil {
			return fmt.Errorf("error generating subpage configs: %v", err)
		}
	}
	fmt.Printf("Generated %d subpage configurations\n", len(subCs))

	if cmd.ConfigOutputDir != "" {
		for _, c := range cs {
			if err := c.WriteToFile(opts.ConfigOutputDir); err != nil {
				return err
			}
		}
		for _, c := range subCs {
			if err := c.WriteToFile(opts.ConfigOutputDir); err != nil {
				return err
			}
		}
	}

	return nil
}

type RegenerateCmd struct {
	CacheOutputDir  string `default:"/tmp/goskyr/main/" help:"Parent directory for the directory that will receive cached copies of the html page and linked pages."`
	ConfigOutputDir string `default:"/tmp/goskyr/main/" help:"Parent directory for the directory that will recieve configuration files."`
}

func (cmd *RegenerateCmd) Run(globals *Globals) error {
	f, err := os.Create("regenerate.prof")
	if err != nil {
		return err
	}
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	dirs := []string{}
	for dir := range urlsForTestnamesByDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		testnames := []string{}
		for testname := range urlsForTestnamesByDir[dir] {
			testnames = append(testnames, testname)
		}
		sort.Strings(testnames)

		for _, testname := range testnames {
			urlAndReq := urlsForTestnamesByDir[dir][testname]
			fmt.Printf("Regenerating test %q\n", testname)

			cacheInDir := filepath.Join("testdata", dir)
			glob := filepath.Join(cacheInDir, testname+"_cache", "*")
			paths, err := filepath.Glob(glob)
			if err != nil {
				return fmt.Errorf("error getting cache input paths with glob %q: %v", glob, err)
			}
			doSubpages := len(paths) > 1

			url := urlAndReq[0]
			cmd := GenerateCmd{
				Batch:           true,
				DoSubpages:      doSubpages,
				FieldsVary:      true,
				CacheInputDir:   cacheInDir,
				CacheOutputDir:  cmd.CacheOutputDir,
				ConfigOutputDir: cmd.ConfigOutputDir,
				Offline:         true,
				RenderJs:        true,
				RequireString:   urlAndReq[1],
				URL:             url,
			}
			if err := cmd.Run(globals); err != nil {
				return fmt.Errorf("error running generate with dir: %q, testname: %q, url: %q: %v\n", dir, testname, url, err)
			}

			// Copy updated config files to testdata config dir.
			cGlob := filepath.Join(strings.TrimPrefix(testInputDir, "../../"), dir, testname+"_configs", "*")
			cPaths, err := filepath.Glob(cGlob)
			if err != nil {
				return fmt.Errorf("error getting config input paths with glob %q: %v", cGlob, err)
			}
			for _, cPath := range cPaths {
				outPath := filepath.Join(cmd.ConfigOutputDir, testname+"_configs", filepath.Base(cPath))
				if _, err := utils.CopyStringFile(outPath, cPath); err != nil {
					fmt.Printf("error copying %q to %q: %v\n", outPath, cPath, err)
				}
			}
			fmt.Printf("Copied %d config files for test %q\n", len(cPaths), testname)

			// Clear old cache files in testdata cache dir.
			cGlob = filepath.Join(cacheInDir, testname+"_cache", "*")
			// fmt.Printf("cGlob: %q\n", cGlob)
			cPaths, err = filepath.Glob(cGlob)
			if err != nil {
				return fmt.Errorf("error getting cache output paths with glob %q: %v", cGlob, err)
			}
			for _, cPath := range cPaths {
				// fmt.Printf("removing cPath: %q\n", cPath)
				if err := os.Remove(cPath); err != nil {
					return fmt.Errorf("error removing old cache input path %q: %v", cPath, err)
				}
			}
			fmt.Printf("Removed %d old cache files for test %q\n", len(cPaths), testname)

			// Copy updated cache files to testdata cache dir.
			cGlob = filepath.Join(cmd.CacheOutputDir, testname+"_cache", "*")
			// fmt.Printf("cGlob: %q\n", cGlob)
			cPaths, err = filepath.Glob(cGlob)
			if err != nil {
				return fmt.Errorf("error getting cache output paths with glob %q: %v", cGlob, err)
			}
			for _, cPath := range cPaths {
				inPath := filepath.Join(cmd.CacheInputDir, testname+"_cache", filepath.Base(cPath))
				if _, err := utils.CopyStringFile(cPath, inPath); err != nil {
					return fmt.Errorf("error copying %q to %q: %v", cPath, inPath, err)
				}
			}
			fmt.Printf("Copied %d cache files for test %q\n", len(cPaths), testname)
		}
	}
	return nil
}

type ScrapeCmd struct {
	ConfigFile string `arg:"" description:"The location of the configuration file."` // . In case of generation, it should be a directory."`
	File       string `help:"skip retrieving from the URL and use this saved copy of the page instead"`
	OutputDir  string `default:"/tmp/goskyr/main/" help:"The output directory."`
	ToStdout   bool   `short:"o" long:"stdout" default:"true" help:"If set to true the scraped data will be written to stdout despite any other existing writer configurations. In combination with the -generate flag the newly generated config will be written to stdout instead of to a file."`
	JSONFile   string `short:"j" long:"json" description:"Writes scraped data as JSON to the given file path."`
}

func (cmd *ScrapeCmd) Run(globals *Globals) error {
	conf, err := scrape.ReadConfig(cmd.ConfigFile)
	if err != nil {
		return fmt.Errorf("error reading config: %v", err)
	}

	recs, err := scrape.Page(conf, &conf.Scrapers[0], &conf.Global, true, cmd.File)
	if err != nil {
		return err
	}
	fmt.Printf("found %d itemMaps\n", len(recs))
	f := &fetch.FileFetcher{}
	slugID := fetch.MakeURLStringSlug(conf.Scrapers[0].URL)
	fetchFn := func(u string) (*goquery.Document, error) {
		u = "file://" + cmd.OutputDir + "/" + slugID + "_subpages" + "/" + fetch.MakeURLStringSlug(u) + ".html"
		slog.Debug("in ScrapeCmd.Run()", "u", u)
		return fetch.GQDocument(f, u, nil)
	}
	if len(conf.Scrapers) > 1 {
		if err = scrape.Subpages(conf, &conf.Scrapers[1], recs, fetchFn); err != nil {
			return err
		}
	}

	if cmd.ToStdout {
		fmt.Println(recs) //conf.String())
	}

	if cmd.JSONFile != "" {
		if err := utils.WriteJSONFile(cmd.JSONFile, recs); err != nil {
			return fmt.Errorf("error writing json file to path: %q: %v", cmd.JSONFile, err)
		}
	}
	return nil
}

type TrainCmd struct {
	TrainModel string `short:"t" long:"train" description:"Train a ML model based on the given csv features file. This will generate 2 files, goskyr.model and goskyr.class"`
}

func (cmd *TrainCmd) Run(globals *Globals) error {
	if err := ml.TrainModel(cmd.TrainModel); err != nil {
		return fmt.Errorf("error training model: %v", err)
	}
	return nil
}

// urlsForTestnames stores the live URLs used to create tests. They are needed to resolve relative paths for event pages that appear in event-list pages. To add new tests, run:
//
//		go run ./cmd/goskyr --debug generate 'https://basic-field.com' --cache-input-dir testdata/regression --cache-output-dir testdata/regression --config-output-dir testdata/regression --do-subpages=false
//
//	 or
//
//	 rm -r /tmp/goskyr/main/basic-fields-w-link-com_configs/; \
//	 time go run ./cmd/goskyr --log-level=debug generate 'https://basic-fields-w-link.com' --cache-input-dir testdata/regression --do-subpages=false
//
// regenerate with
//
//	go run main.go --debug regenerate
var urlsForTestnamesByDir = map[string]map[string][]string{
	"regression": {
		"basic-field-com":                   []string{"https://basic-field.com", ""},
		"basic-field-w-div-com":             []string{"https://basic-field-w-div.com", ""},
		"basic-fields-w-div-com":            []string{"https://basic-fields-w-div.com", ""},
		"basic-fields-w-div-w-div-com":      []string{"https://basic-fields-w-div-w-div.com", ""},
		"basic-fields-w-div-w-divc-com":     []string{"https://basic-fields-w-div-w-div.com", ""},
		"basic-fields-w-div-w-link-div-com": []string{"https://basic-fields-w-div-w-link-div.com", ""},
		"basic-fields-w-link-com":           []string{"https://basic-fields-w-link.com", ""},
		"basic-fields-w-link-div-com":       []string{"https://basic-fields-w-link-div.com", ""},
		"basic-fields-w-style-com":          []string{"https://basic-fields-w-style.com", ""},
		"basic-subpages-com":                []string{"https://basic-subpages.com", ""},
		"css-class-with-special-chars-com":  []string{"https://css-class-with-special-chars.com", ""},
		"fields-w-a-com_cache":              []string{"https://fields-w-a.com", ""},
	},
	"scraping": {
		"books-toscrape-com":             []string{"https://books.toscrape.com", "Soumission"},
		"quotes-toscrape-com":            []string{"https://quotes.toscrape.com", "Imperfection"},
		"realpython-github-io-fake-jobs": []string{"https://realpython.github.io/fake-jobs", ""},
		"webscraper-io-test-sites-e-commerce-allinone-computers-tablets": []string{"https://webscraper.io/test-sites/e-commerce/allinone/computers/tablets", "Android"},
		"www-scrapethissite-com-pages-forms":                             []string{"https://www.scrapethissite.com/pages/forms", ""},
		"www-scrapethissite-com-pages-simple":                            []string{"https://www.scrapethissite.com/pages/simple", ""},
	},
}

var testOutputDir = "/tmp/goskyr/main/"
var testInputDir = "../../testdata/"
