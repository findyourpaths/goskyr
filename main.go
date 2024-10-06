package main

import (
	"fmt"
	"log/slog"
	"math"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/findyourpaths/goskyr/autoconfig"
	"github.com/findyourpaths/goskyr/config"
	"github.com/findyourpaths/goskyr/ml"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scraper"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/jessevdk/go-flags"
)

type mainOpts struct {
	Batch               bool   `short:"b" long:"batch" description:"Run batch (not interactively) to generate the config file."`
	ConfigFile          string `short:"c" long:"config" default:"./config.yml" description:"The location of the configuration. Can be a directory containing config files or a single config file."`
	DebugFlag           bool   `short:"d" long:"debug" description:"Prints debug logs and writes scraped html's to files."`
	ExtractFeatures     string `short:"e" long:"extract" description:"Extract ML features based on the given configuration file (-c) and write them to the given file in csv format."`
	FieldsVary          bool   `short:"f" long:"fieldsvary" description:"Only show fields that have varying values across the list of items. Works in combination with the -g flag."`
	GenerateForURL      string `short:"g" long:"generate" description:"Automatically generate a config file for the given url."`
	Min                 int    `short:"m" long:"min" default:"20" description:"The minimum number of items on a page. This is needed to filter out noise. Works in combination with the -g flag."`
	PretrainedModelPath string `short:"p" long:"pretrained" description:"Use a pre-trained ML model to infer names of extracted fields. Works in combination with the -g flag."`
	RenderJs            bool   `short:"r" long:"renderjs" description:"Render JS before generating a configuration file. Works in combination with the -g flag."`
	SingleScraper       string `short:"s" description:"The name of the scraper to be run."`
	TrainModel          string `short:"t" long:"train" description:"Train a ML model based on the given csv features file. This will generate 2 files, goskyr.model and goskyr.class"`
	PrintVersion        bool   `short:"v" description:"The version of goskyr."`
	WordsDir            string `short:"w" default:"word-lists" description:"The directory that contains a number of files containing words of different languages. This is needed for the ML part (use with -e or -b)."`
	ToJSON              bool   `long:"json" description:"If --stdout is true and this is set to true, the scraped data will be written as JSON to stdout."`
	ToStdout            bool   `long:"stdout" description:"If set to true the scraped data will be written to stdout despite any other existing writer configurations. In combination with the -generate flag the newly generated config will be written to stdout instead of to a file."`
	// writeTest := flag.Bool("writetest", false, "Runs on test inputs and rewrites test outputs.")
}

var opts mainOpts

var version = "dev"

func worker(sc chan scraper.Scraper, ic chan output.ItemMap, gc *scraper.GlobalConfig, threadNr int) {
	workerLogger := slog.With(slog.Int("thread", threadNr))
	for s := range sc {
		scraperLogger := workerLogger.With(slog.String("name", s.Name))
		scraperLogger.Info("starting scraping task")
		items, err := s.GetItems(gc, false)
		if err != nil {
			scraperLogger.Error(fmt.Sprintf("%s: %s", s.Name, err))
			continue
		}
		scraperLogger.Info(fmt.Sprintf("fetched %d items", len(items)))
		for _, item := range items {
			ic <- item
		}
	}
	workerLogger.Info("done working")
}

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

	if opts.GenerateForURL != "" {
		if _, err := GenerateConfigs(opts); err != nil {
			slog.Error(err.Error())
			os.Exit(1)
		}
		return
	}

	if opts.TrainModel != "" {
		if err := ml.TrainModel(opts.TrainModel); err != nil {
			slog.Error(fmt.Sprintf("%v", err))
			os.Exit(1)
		}
		return
	}

	conf, err := scraper.NewConfig(opts.ConfigFile)
	if err != nil {
		slog.Error(fmt.Sprintf("%v", err))
		os.Exit(1)
	}

	if opts.ExtractFeatures != "" {
		if err := ml.ExtractFeatures(conf, opts.ExtractFeatures, opts.WordsDir); err != nil {
			slog.Error(fmt.Sprintf("%v", err))
			os.Exit(1)
		}
		return
	}

	var workerWg sync.WaitGroup
	var writerWg sync.WaitGroup
	ic := make(chan output.ItemMap)

	var writer output.Writer
	if opts.ToStdout {
		writer = &output.StdoutWriter{}
	} else if opts.ToJSON {
		writer = &output.JSONWriter{}
	} else {
		switch conf.Writer.Type {
		case output.STDOUT_WRITER_TYPE:
			writer = &output.StdoutWriter{}
		case output.API_WRITER_TYPE:
			writer = output.NewAPIWriter(&conf.Writer)
		case output.FILE_WRITER_TYPE:
			writer = output.NewFileWriter(&conf.Writer)
		default:
			slog.Error(fmt.Sprintf("writer of type %s not implemented", conf.Writer.Type))
			os.Exit(1)
		}
	}

	if conf.Global.UserAgent == "" {
		conf.Global.UserAgent = "goskyr web scraper (github.com/findyourpaths/goskyr)"
	}

	sc := make(chan scraper.Scraper)

	// fill worker queue
	go func() {
		for _, s := range conf.Scrapers {
			if opts.SingleScraper == "" || opts.SingleScraper == s.Name {
				// s.Debug = opts.DebugFlag
				sc <- s
			}
		}
		close(sc)
	}()

	// start workers
	nrWorkers := 1
	if opts.SingleScraper == "" {
		nrWorkers = int(math.Min(20, float64(len(conf.Scrapers))))
	}
	slog.Info(fmt.Sprintf("running with %d threads", nrWorkers))
	workerWg.Add(nrWorkers)
	slog.Debug("starting workers")
	for i := 0; i < nrWorkers; i++ {
		go func(j int) {
			defer workerWg.Done()
			worker(sc, ic, &conf.Global, j)
		}(i)
	}

	// start writer
	writerWg.Add(1)
	slog.Debug("starting writer")
	go func() {
		defer writerWg.Done()
		writer.Write(ic)
	}()
	workerWg.Wait()
	close(ic)
	writerWg.Wait()
}

func GenerateConfigs(opts mainOpts) (map[string]*scraper.Config, error) {
	slog.Debug("starting to generate config")
	slog.Debug("analyzing", "url", opts.GenerateForURL)
	cims, err := autoconfig.NewDynamicFieldsConfigs(opts.GenerateForURL, opts.RenderJs, opts.Min, opts.FieldsVary, opts.PretrainedModelPath, opts.WordsDir, opts.Batch)
	if err != nil {
		return nil, err
	}

	cs := map[string]*scraper.Config{}
	for id, cim := range cims {
		cs[id] = cim.Config
		if opts.ToStdout {
			fmt.Println(cim.Config.String())
		}
		if opts.ConfigFile != "" {
			base := strings.TrimSuffix(opts.ConfigFile, "_config.yml")
			if err := utils.WriteStringFile(fmt.Sprintf("%s_%s_config.yml", base, id), cim.Config.String()); err != nil {
				return nil, err
			}
			if err := utils.WriteStringFile(fmt.Sprintf("%s_%s.json", base, id), cim.ItemMaps.String()); err != nil {
				return nil, err
			}
		}
	}

	return cs, nil
}
