package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/alecthomas/kong"
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
	if pageOpts.DoSubpages {
		if _, err := generate.ConfigurationsForAllSubpages(pageOpts, cs); err != nil {

			return fmt.Errorf("error generating configuration for all subpages: %v", err)
		}
	}
	return nil
}

type ScrapeCmd struct {
	File string `arg:"" description:"The location of the configuration. Can be a directory containing config files or a single config file."` // . In case of generation, it should be a directory."`

	ToStdout bool   `short:"o" long:"stdout" default:"true" help:"If set to true the scraped data will be written to stdout despite any other existing writer configurations. In combination with the -generate flag the newly generated config will be written to stdout instead of to a file."`
	JSONFile string `short:"j" long:"json" description:"Writes scraped data as JSON to the given file path."`
}

func (a *ScrapeCmd) Run(globals *Globals) error {
	conf, err := scrape.ReadConfig(a.File)
	if err != nil {
		return fmt.Errorf("error reading config: %v", err)
	}

	allItems := output.ItemMaps{}
	for _, s := range conf.Scrapers {
		items, err := scrape.Page(&s, &conf.Global, false, "")
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
