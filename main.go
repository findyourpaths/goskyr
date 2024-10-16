package main

import (
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"

	"github.com/findyourpaths/goskyr/config"
	"github.com/findyourpaths/goskyr/generate"
	"github.com/findyourpaths/goskyr/ml"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/jessevdk/go-flags"
)

type mainOpts struct {
	Batch               bool   `short:"b" long:"batch" description:"Run batch (not interactively) to generate the config file."`
	ConfigFile          string `short:"c" long:"config" default:"./config.yml" description:"The location of the configuration. Can be a directory containing config files or a single config file."`
	DebugFlag           bool   `short:"d" long:"debug" description:"Prints debug logs and writes scraped html's to files."`
	ExtractFeatures     string `short:"e" long:"extract" description:"Extract ML features based on the given configuration file (-c) and write them to the given file in csv format."`
	FieldsVary          bool   `short:"f" long:"fieldsvary" description:"Only show fields that have varying values across the list of items. Works in combination with the -g flag."`
	InputURL            string `short:"i" long:"inputurl" description:"Automatically generate a config file for the given input url."`
	JSONFile            string `short:"j" long:"json" description:"Writes scraped data as JSON to the given file path."`
	MinOcc              int    `short:"m" long:"min" description:"The minimum number of items on a page. This is needed to filter out noise. Works in combination with the -g flag."`
	PretrainedModelPath string `short:"p" long:"pretrained" description:"Use a pre-trained ML model to infer names of extracted fields. Works in combination with the -g flag."`
	RenderJs            bool   `short:"r" long:"renderjs" description:"Render JS before generating a configuration file. Works in combination with the -g flag."`
	DoSubpages          bool   `short:"s" long:"subpages" description:"Whether to generate configurations for subpages as well."`
	// SubpagesRequired     bool   `short:"s" long:"subpage" description:"Whether a URL for a subpage is required in the generated config. If true, configs will not be produced if they don't have a subpage URL field. URLs for images (e.g. ending in .jpg, .gif, or .png) are not considered subpage URLs."`
	TrainModel   string `short:"t" long:"train" description:"Train a ML model based on the given csv features file. This will generate 2 files, goskyr.model and goskyr.class"`
	URL          string `short:"u" long:"url" description:"Canonical URL source of the input URL, useful for resolving relative paths when the input URL is for a file."`
	PrintVersion bool
	WordsDir     string `short:"w" default:"word-lists" description:"The directory that contains a number of files containing words of different languages. This is needed for the ML part (use with -e or -b)."`
	ToStdout     bool   `long:"stdout" description:"If set to true the scraped data will be written to stdout despite any other existing writer configurations. In combination with the -generate flag the newly generated config will be written to stdout instead of to a file."`
	// writeTest := flag.Bool("writetest", false, "Runs on test inputs and rewrites test outputs.")
}

var opts mainOpts

var version = "dev"

func main() {
	_, err := flags.Parse(&opts)
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

	if opts.PrintVersion {
		buildInfo, ok := debug.ReadBuildInfo()
		if ok {
			if buildInfo.Main.Version != "" && buildInfo.Main.Version != "(devel)" {
				fmt.Println(buildInfo.Main.Version)
				return
			}
		}
		fmt.Println(version)
		return
	}

	config.Debug = opts.DebugFlag
	var logLevel slog.Level
	if opts.DebugFlag {
		logLevel = slog.LevelDebug
	} else {
		logLevel = slog.LevelInfo
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	if opts.InputURL != "" {

		if opts.URL == "" {
			if strings.HasPrefix(opts.InputURL, "file://") {
				slog.Error("URL flag required if InputURL is file")
				os.Exit(1)
			}
			opts.URL = opts.InputURL
		}

		minOccs := []int{5, 10, 20}
		if opts.MinOcc != 0 {
			minOccs = []int{opts.MinOcc}
		}

		pageOpts := generate.ConfigOptions{
			Batch:       opts.Batch,
			InputURL:    opts.InputURL,
			ModelName:   opts.PretrainedModelPath,
			OnlyVarying: opts.FieldsVary,
			RenderJS:    opts.RenderJs,
			DoSubpages:  opts.DoSubpages,
			URL:         opts.URL,
			WordsDir:    opts.WordsDir,
			MinOccs:     minOccs,
		}
		base := strings.TrimSuffix(opts.ConfigFile, "_config.yml")
		if _, err := generate.ConfigurationsForPage(pageOpts, base, opts.ToStdout, opts.DoSubpages); err != nil {
			slog.Error("error generating configs", "err", err)
			os.Exit(1)
		}
		return
	}

	if opts.TrainModel != "" {
		if err := ml.TrainModel(opts.TrainModel); err != nil {
			slog.Error("error training model", "err", err)
			os.Exit(1)
		}
		return
	}

	conf, err := scrape.NewConfig(opts.ConfigFile)
	if err != nil {
		slog.Error("error making new config", "err", err)
		os.Exit(1)
	}

	if opts.ExtractFeatures != "" {
		if err := ml.ExtractFeatures(conf, opts.ExtractFeatures, opts.WordsDir); err != nil {
			slog.Error("error extracting features", "err", err)
			os.Exit(1)
		}
		return
	}

	if conf.Global.UserAgent == "" {
		conf.Global.UserAgent = "goskyr web scraper (github.com/findyourpaths/goskyr)"
	}

	allItems := output.ItemMaps{}
	for _, s := range conf.Scrapers {
		items, err := s.GetItems(&conf.Global, false)
		if err != nil {
			slog.Error("error scraping", "err", err)
			continue
		}
		allItems = append(allItems, items...)
	}

	if opts.JSONFile != "" {
		if err := utils.WriteJSONFile(opts.JSONFile, allItems); err != nil {
			slog.Error("error writing json file", "path", opts.JSONFile)
		}
	}
}
