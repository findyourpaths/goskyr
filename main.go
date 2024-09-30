package main

import (
	"fmt"
	"math"
	"os"
	"runtime/debug"
	"sync"

	"github.com/findyourpaths/goskyr/autoconfig"
	"github.com/findyourpaths/goskyr/config"
	"github.com/findyourpaths/goskyr/ml"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scraper"
	"github.com/jessevdk/go-flags"
	"golang.org/x/exp/slog"
	"gopkg.in/yaml.v3"
)

type mainOpts struct {
	F               bool   `short:"f" description:"Only show fields that have varying values across the list of items. Works in combination with the -g flag."`
	M               int    `short:"m" default:"20" description:"The minimum number of items on a page. This is needed to filter out noise. Works in combination with the -g flag."`
	SingleScraper   string `short:"s" description:"The name of the scraper to be run."`
	ToStdout        bool   `long:"stdout" description:"If set to true the scraped data will be written to stdout despite any other existing writer configurations. In combination with the -generate flag the newly generated config will be written to stdout instead of to a file."`
	ToJSON          bool   `long:"json" description:"If --stdout is true and this is set to true, the scraped data will be written as JSON to stdout."`
	ConfigLoc       string `short:"c" default:"./config.yml" description:"The location of the configuration. Can be a directory containing config files or a single config file."`
	PrintVersion    bool   `short:"v" description:"The version of goskyr."`
	GenerateConfig  string `short:"g" description:"Automatically generate a config file for the given url."`
	ExtractFeatures string `short:"e" description:"Extract ML features based on the given configuration file (-c) and write them to the given file in csv format."`
	WordsDir        string `short:"w" default:"word-lists" description:"The directory that contains a number of files containing words of different languages. This is needed for the ML part (use with -e or -b)."`
	BuildModel      string `short:"t" description:"Train a ML model based on the given csv features file. This will generate 2 files, goskyr.model and goskyr.class"`
	DebugFlag       bool   `long:"debug" description:"Prints debug logs and writes scraped html's to files."`
	ModelPath       string `long:"model" description:"Use a pre-trained ML model to infer names of extracted fields. Works in combination with the -g flag."`
	RenderJs        bool   `short:"r" description:"Render JS before generating a configuration file. Works in combination with the -g flag."`
	// writeTest := flag.Bool("writetest", false, "Runs on test inputs and rewrites test outputs.")
}

// configLoc := flag.String("c", "./config.yml", "The location of the configuration. Can be a directory containing config files or a single config file.")
// generateConfig := flag.String("g", "", "Automatically generate a config file for the given url.")
// toStdout := flag.Bool("stdout", false, "If set to true the scraped data will be written to stdout despite any other existing writer configurations. In combination with the -generate flag the newly generated config will be written to stdout instead of to a file.")
// wordsDir := flag.String("w", "word-lists", "The directory that contains a number of files containing words of different languages. This is needed for the ML part (use with -e or -b).")

var opts mainOpts

// 	// Slice of bool will append 'true' each time the option
// 	// is encountered (can be set multiple times, like -vvv)
// 	Verbose []bool `short:"v" long:"verbose" description:"Show verbose debug information"`

// 	// Example of automatic marshalling to desired type (uint)
// 	Offset uint `long:"offset" description:"Offset"`

// 	// Example of a callback, called each time the option is found.
// 	Call func(string) `short:"c" description:"Call phone number"`

// 	// Example of a required flag
// 	Name string `short:"n" long:"name" description:"A name" required:"true"`

// 	// Example of a flag restricted to a pre-defined set of strings
// 	Animal string `long:"animal" choice:"cat" choice:"dog"`

// 	// Example of a value name
// 	File string `short:"f" long:"file" description:"A file" value-name:"FILE"`

// 	// Example of a pointer
// 	Ptr *int `short:"p" description:"A pointer to an integer"`

// 	// Example of a slice of strings
// 	StringSlice []string `short:"s" description:"A slice of strings"`

// 	// Example of a slice of pointers
// 	PtrSlice []*string `long:"ptrslice" description:"A slice of pointers to string"`

// 	// Example of a map
// 	IntMap map[string]int `long:"intmap" description:"A map from string to int"`

// 	// Example of env variable
// 	Thresholds []int `long:"thresholds" default:"1" default:"2" env:"THRESHOLD_VALUES"  env-delim:","`
// }

// func parse() {
// 	// Parse flags from `args'. Note that here we use flags.ParseArgs for
// 	// the sake of making a working example. Normally, you would simply use
// 	// flags.Parse(&opts) which uses os.Args

// 	fmt.Printf("Verbosity: %v\n", opts.Verbose)
// 	fmt.Printf("Offset: %d\n", opts.Offset)
// 	fmt.Printf("Name: %s\n", opts.Name)
// 	fmt.Printf("Animal: %s\n", opts.Animal)
// 	fmt.Printf("Ptr: %d\n", *opts.Ptr)
// 	fmt.Printf("StringSlice: %v\n", opts.StringSlice)
// 	fmt.Printf("PtrSlice: [%v %v]\n", *opts.PtrSlice[0], *opts.PtrSlice[1])
// 	fmt.Printf("IntMap: [a:%v b:%v]\n", opts.IntMap["a"], opts.IntMap["b"])
// 	fmt.Printf("Remaining args: %s\n", strings.Join(args, " "))
// }

var version = "dev"

func worker(sc chan scraper.Scraper, ic chan map[string]interface{}, gc *scraper.GlobalConfig, threadNr int) {
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

	if opts.GenerateConfig != "" {
		if err := doGenerateConfig(opts); err != nil {
			slog.Error(err.Error())
			os.Exit(1)
		}
		return
	}

	if opts.BuildModel != "" {
		if err := ml.TrainModel(opts.BuildModel); err != nil {
			slog.Error(fmt.Sprintf("%v", err))
			os.Exit(1)
		}
		return
	}

	conf, err := scraper.NewConfig(opts.ConfigLoc)
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
	ic := make(chan map[string]interface{})

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

func doGenerateConfig(opts mainOpts) error {

	slog.Debug("starting to generate config")
	s := &scraper.Scraper{URL: opts.GenerateConfig}
	if opts.RenderJs {
		s.RenderJs = true
	}
	slog.Debug(fmt.Sprintf("analyzing url %s", s.URL))
	err := autoconfig.GetDynamicFieldsConfig(s, opts.M, opts.F, opts.ModelPath, opts.WordsDir)
	if err != nil {
		return err
	}
	c := scraper.Config{
		Scrapers: []scraper.Scraper{
			*s,
		},
	}
	yamlData, err := yaml.Marshal(&c)
	if err != nil {
		return fmt.Errorf("error while marshaling. %v", err)
	}

	if opts.ToStdout {
		fmt.Println(string(yamlData))
	} else {
		f, err := os.Create(opts.ConfigLoc)
		if err != nil {
			return fmt.Errorf("error opening file: %v", err)
		}
		defer f.Close()
		_, err = f.Write(yamlData)
		if err != nil {
			return fmt.Errorf("error writing to file: %v", err)
		}
		slog.Info(fmt.Sprintf("successfully wrote config to file %s", opts.ConfigLoc))
	}

	return nil
}
