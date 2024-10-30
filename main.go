package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/pprof"

	"github.com/alecthomas/kong"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/generate"
	"github.com/findyourpaths/goskyr/ml"
	"github.com/findyourpaths/goskyr/output"
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
	if cli.Globals.Debug {
		logLevel = slog.LevelDebug
	} else {
		logLevel = slog.LevelInfo
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
	Debug bool `short:"d" help:"Enable debug mode"`
}

type ExtractFeaturesCmd struct {
	File            string `default:"./config.yml" description:"The location of the configuration. Can be a directory containing config files or a single config file."`
	ExtractFeatures string `short:"e" long:"extract" description:"Write extracted features to the given file in csv format."`
	WordsDir        string `short:"w" default:"word-lists" description:"The directory that contains a number of files containing words of different languages. This is needed for the ML part (use with -e or -b)."`
}

func (a *ExtractFeaturesCmd) Run(globals *Globals) error {
	conf, err := scrape.ReadConfig(a.File)
	if err != nil {
		return fmt.Errorf("error reading config: %v", err)
	}

	if err := ml.ExtractFeatures(conf, a.ExtractFeatures, a.WordsDir); err != nil {
		return fmt.Errorf("error extracting features: %v", err)
	}
	return nil
}

type GenerateCmd struct {
	URL string `arg:"" help:"Automatically generate a config file for the given input url."`

	Batch               bool   `short:"b" long:"batch" help:"Run batch (not interactively) to generate the config file."`
	DoSubpages          bool   `short:"s" long:"subpages" help:"Whether to generate configurations for subpages as well."`
	FieldsVary          bool   `long:"fieldsvary" help:"Only show fields that have varying values across the list of items. Works in combination with the -g flag."`
	File                string `help:"skip retrieving from the URL and use this saved copy of the page instead"`
	MinOcc              int    `short:"m" long:"min" help:"The minimum number of items on a page. This is needed to filter out noise. Works in combination with the -g flag."`
	OutputDir           string `help:"The output directory."`
	PretrainedModelPath string `short:"p" long:"pretrained" description:"Use a pre-trained ML model to infer names of extracted fields. Works in combination with the -g flag."`
	RenderJs            bool   `short:"r" long:"renderjs" help:"Render JS before generating a configuration file. Works in combination with the -g flag."`
	WordsDir            string `short:"w" default:"word-lists" description:"The directory that contains a number of files containing words of different languages. This is needed for the ML part (use with -e or -b)."`
}

var mainDir = "/tmp/goskyr/main/"

func (a *GenerateCmd) Run(globals *Globals) error {
	f, err := os.Create("generate.prof")
	if err != nil {
		return err
	}
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	// fmt.Printf("Config: %s\n", globals.Config)
	// fmt.Printf("Attaching to: %v\n", a.Container)
	// fmt.Printf("Batch: %v\n", a.Batch)
	// return nil

	// if opts.URL == "" {
	// 	if strings.HasPrefix(opts.InputURL, "file://") {
	// 		slog.Error("URL flag required if InputURL is file")
	// 		os.Exit(1)
	// 	}
	// 	opts.URL = opts.InputURL
	// }

	minOccs := []int{5, 10, 20}
	if a.MinOcc != 0 {
		minOccs = []int{a.MinOcc}
	}

	if a.OutputDir != "" {
		mainDir = a.OutputDir
	}

	pageOpts, err := generate.InitOpts(generate.ConfigOptions{
		Batch:       a.Batch,
		InputDir:    mainDir,
		File:        a.File,
		URL:         a.URL,
		ModelName:   a.PretrainedModelPath,
		OnlyVarying: a.FieldsVary,
		RenderJS:    a.RenderJs,
		DoSubpages:  a.DoSubpages,
		WordsDir:    a.WordsDir,
		MinOccs:     minOccs,
		OutputDir:   mainDir,
	})
	if err != nil {
		return fmt.Errorf("error initializing page options: %v", err)
	}
	// fmt.Printf("pageOpts before generate.ConfigurationsForPage: %#v\n", pageOpts)
	cs, err := generate.ConfigurationsForPage(pageOpts)
	if err != nil {
		return fmt.Errorf("error generating configs: %v", err)
	}
	// fmt.Printf("pageOpts before generate.ConfigurationsForAllSubpages: %#v\n", pageOpts)
	var subCs map[string]*scrape.Config
	if pageOpts.DoSubpages {
		if subCs, err = generate.ConfigurationsForAllSubpages(pageOpts, cs); err != nil {

			return fmt.Errorf("error generating configuration for all subpages: %v", err)
		}
	}

	outDir := filepath.Join(mainDir, fetch.MakeURLStringSlug(a.URL)+"_configs")
	for _, c := range cs {
		if err := c.WriteToFile(outDir); err != nil {
			return err
		}
	}
	for _, c := range subCs {
		if err := c.WriteToFile(outDir); err != nil {
			return err
		}
	}

	return nil
}

type RegenerateCmd struct{}

func (a *RegenerateCmd) Run(globals *Globals) error {
	for dir, urlsForTestnames := range urlsForTestnamesByDir {
		for testname, url := range urlsForTestnames {
			fmt.Printf("Regenerating test %q\n", testname)

			_, err := os.Stat(filepath.Join("testdata", dir, testname+"_subpages"))
			doSubpages := err == nil
			cmd := GenerateCmd{
				Batch:      true,
				DoSubpages: doSubpages,
				FieldsVary: true,
				File:       filepath.Join("testdata", dir, testname+".html"),
				OutputDir:  "/tmp/goskyr/main/",
				RenderJs:   true,
				URL:        url,
			}
			if err := cmd.Run(globals); err != nil {
				fmt.Printf("ERROR: error running generate with dir: %q, testname: %q, url: %q\n", dir, testname, url)
			}
		}
	}
	return nil
}

type ScrapeCmd struct {
	ConfigFile string `arg:"" description:"The location of the configuration. Can be a directory containing config files or a single config file."` // . In case of generation, it should be a directory."`
	File       string `help:"skip retrieving from the URL and use this saved copy of the page instead"`
	ToStdout   bool   `short:"o" long:"stdout" default:"true" help:"If set to true the scraped data will be written to stdout despite any other existing writer configurations. In combination with the -generate flag the newly generated config will be written to stdout instead of to a file."`
	JSONFile   string `short:"j" long:"json" description:"Writes scraped data as JSON to the given file path."`
}

func (a *ScrapeCmd) Run(globals *Globals) error {
	conf, err := scrape.ReadConfig(a.ConfigFile)
	if err != nil {
		return fmt.Errorf("error reading config: %v", err)
	}

	allItems := output.ItemMaps{}
	for _, s := range conf.Scrapers {
		items, err := scrape.Page(&s, &conf.Global, true, a.File)
		if err != nil {
			slog.Error("error scraping", "err", err)
			continue
		}
		allItems = append(allItems, items...)
	}

	if a.ToStdout {
		fmt.Println(allItems) //conf.String())
	}

	if a.JSONFile != "" {
		if err := utils.WriteJSONFile(a.JSONFile, allItems); err != nil {
			return fmt.Errorf("error writing json file to path: %q: %v", a.JSONFile, err)
		}
	}
	return nil
}

type TrainCmd struct {
	TrainModel string `short:"t" long:"train" description:"Train a ML model based on the given csv features file. This will generate 2 files, goskyr.model and goskyr.class"`
}

func (a *TrainCmd) Run(globals *Globals) error {
	if err := ml.TrainModel(a.TrainModel); err != nil {
		return fmt.Errorf("error training model: %v", err)
	}
	return nil
}

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
var urlsForTestnamesByDir = map[string]map[string]string{
	"chicago": {
		"hideoutchicago-com-events": "https://hideoutchicago.com/events",
	},
	"scraping": {
		"books-toscrape-com":             "https://books.toscrape.com",
		"quotes-toscrape-com":            "https://quotes.toscrape.com",
		"realpython-github-io-fake-jobs": "https://realpython.github.io/fake-jobs/",
		"webscraper-io-test-sites-e-commerce-allinone-computers-tablets": "https://webscraper.io/test-sites/e-commerce/allinone/computers/tablets",
		"www-scrapethissite-com-pages-forms":                             "https://www.scrapethissite.com/pages/forms",
		"www-scrapethissite-com-pages-simple":                            "https://www.scrapethissite.com/pages/simple",
	},
}
