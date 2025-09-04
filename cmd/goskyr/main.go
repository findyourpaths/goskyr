package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/generate"
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

	switch strings.ToLower(cli.Globals.LogLevel) {
	case "debug":
		scrape.DoDebug = true
		output.MinLogLevel = slog.LevelDebug
	case "info":
		output.MinLogLevel = slog.LevelInfo
	case "warn":
		output.MinLogLevel = slog.LevelWarn
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: output.MinLogLevel}))
	slog.SetDefault(logger)

	err := ctx.Run(&cli.Globals)
	ctx.FatalIfErrorf(err)
}

type CLI struct {
	Globals

	// ExtractFeatures ExtractFeaturesCmd `cmd:"" help:"Extract ML features based on the given configuration file"`
	Generate   GenerateCmd   `cmd:"" help:"Automatically generate a configuration file for the given URL"`
	Regenerate RegenerateCmd `cmd:"" help:"Automatically regenerate test data"`
	Scrape     ScrapeCmd     `cmd:"" help:"Scrape"`
}

type Globals struct {
	LogLevel string `short:"l" default:"info" help:"Control log level: debug, info, or warn"`
}

// type ExtractFeaturesCmd struct {
// 	File string `arg:"" help:"The location of the configuration. Can be a directory containing config files or a single config file."`

// 	ExtractFeatures string `short:"e" long:"extract" description:"Write extracted features to the given file in csv format."`
// 	WordsDir        string `short:"w" default:"word-lists" description:"The directory that contains a number of files containing words of different languages. This is needed for the ML part (use with -e or -b)."`
// }

// func (cmd *ExtractFeaturesCmd) Run(globals *Globals) error {
// 	conf, err := scrape.ReadConfig(cmd.File)
// 	if err != nil {
// 		return fmt.Errorf("error reading config: %v", err)
// 	}

// 	if err := ml.ExtractFeatures(conf, cmd.ExtractFeatures, cmd.WordsDir); err != nil {
// 		return fmt.Errorf("error extracting features: %v", err)
// 	}
// 	return nil
// }

type GenerateCmd struct {
	URL string `arg:"" help:"Automatically generate a config file for the given input url."`

	Batch                      bool   `short:"b" long:"batch" default:true help:"Run batch (not interactively) to generate the config file."`
	DoDetailPages              bool   `default:true help:"Whether to generate configurations for detail page as well."`
	OnlyKnownDomainDetailPages bool   `default:true help:"Only go to detail pages on the same domain (e.g. follow z.com to y.z.com but not x.com) or a known ticketing domain (e.g. eventbrite.com)."`
	OnlyVaryingFields          bool   `default:true help:"Only show fields that have varying values across the list of records."`
	MinOcc                     int    `short:"m" long:"min" help:"The minimum number of records on a page. This is needed to filter out noise. Works in combination with the -g flag."`
	CacheInputParentDir        string `default:"/tmp/goskyr/main/" help:"Parent directory for the directory containing cached copies of the html page and linked pages."`
	CacheOutputParentDir       string `default:"/tmp/goskyr/main/" help:"Parent directory for the directory that will receive cached copies of the html page and linked pages."`
	ConfigOutputParentDir      string `default:"/tmp/goskyr/main/" help:"Parent directory for the directory that will recieve configuration files."`
	Offline                    bool   `default:false help:"Run offline and don't fetch pages."`
	PretrainedModelPath        string `short:"p" long:"pretrained" description:"Use a pre-trained ML model to infer names of extracted fields. Works in combination with the -g flag."`
	RenderJs                   bool   `short:"r" long:"renderjs" default:true help:"Render JS before generating a configuration file. Works in combination with the -g flag."`
	RequireDates               bool   `help:"Require a candidate configuration to extract a date for most items in order for it to be generated."`
	RequireString              string `help:"Require a candidate configuration to extract the given text in order for it to be generated."`
	WordsDir                   string `short:"w" default:"word-lists" description:"The directory that contains a number of files containing words of different languages. This is needed for the ML part (use with -e or -b)."`
}

func (cmd *GenerateCmd) Run(globals *Globals) error {
	f, err := os.Create("generate.prof")
	if err != nil {
		return err
	}
	defer f.Close()
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	minOccs := []int{3, 5, 10, 20}
	// minOccs = []int{5}
	if cmd.MinOcc != 0 {
		minOccs = []int{cmd.MinOcc}
	}

	opts, err := generate.InitOpts(generate.ConfigOptions{
		Batch: cmd.Batch,
		// CacheInputDir:             cmd.CacheInputDir,
		// CacheOutputDir:            cmd.CacheOutputDir,
		ConfigOutputParentDir:      cmd.ConfigOutputParentDir,
		DoDetailPages:              cmd.DoDetailPages,
		URL:                        cmd.URL,
		MinOccs:                    minOccs,
		ModelName:                  cmd.PretrainedModelPath,
		Offline:                    cmd.Offline,
		OnlyKnownDomainDetailPages: cmd.OnlyKnownDomainDetailPages,
		OnlyVaryingFields:          cmd.OnlyVaryingFields,
		RenderJS:                   cmd.RenderJs,
		RequireDates:               cmd.RequireDates,
		RequireString:              cmd.RequireString,
		WordsDir:                   cmd.WordsDir,
	})
	if err != nil {
		return fmt.Errorf("error initializing page options: %v", err)
	}

	var fetcher fetch.Fetcher
	if !cmd.Offline {
		fetcher = fetch.NewDynamicFetcher("", 0) //s.PageLoadWait)
	}
	var cache fetch.Cache
	cache = fetch.NewFetchCache(fetcher)
	cache = fetch.NewURLFileCache(cache, cmd.CacheInputParentDir, false)
	cache = fetch.NewURLFileCache(cache, cmd.CacheOutputParentDir, true)
	cache = fetch.NewMemoryCache(cache)

	cs, err := generate.ConfigurationsForPage(cache, opts)
	if err != nil {
		return fmt.Errorf("error generating page configs: %v", err)
	}
	fmt.Printf("Generated %d page configurations in %q\n", len(cs), opts.ConfigOutputDir)

	var subCs map[string]*scrape.Config
	if opts.DoDetailPages {
		if subCs, err = generate.ConfigurationsForAllDetailPages(cache, opts, cs); err != nil {
			return fmt.Errorf("error generating detail page configs: %v", err)
		}
	}
	fmt.Printf("Generated %d detail page configurations in %q\n", len(subCs), opts.ConfigOutputDir)

	if cmd.ConfigOutputParentDir != "" {
		for _, c := range cs {
			// fmt.Println("writing config", "len(key)", len(key), "c.ID.String()", c.ID.String())
			if err := c.WriteToFile(opts.ConfigOutputDir); err != nil {
				return err
			}
		}
		for _, c := range subCs {
			// fmt.Println("writing subconfig", "len(key)", len(key), "c.ID.String()", c.ID.String())
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

	for _, cat := range sortedTestCategories() {
		for _, hostSlug := range sortedTestHostSlugs(cat) {
			if err := cmd.regenerateTest(globals, cat, hostSlug); err != nil {
				return err
			}
		}
	}
	return nil
}

func (cmd *RegenerateCmd) regenerateTest(globals *Globals, cat string, hostSlug string) error {
	fmt.Printf("Regenerating test %q\n", hostSlug)
	testCatInputDir := filepath.Join("testdata", cat)

	for _, t := range testsByHostSlugByCategory[cat][hostSlug] {
		pageSlug := fetch.MakeURLStringSlug(t.url)
		ps, err := testDirPathsWithPattern(testCatInputDir, hostSlug+"_configs", pageSlug+"*"+"href"+"*"+configSuffix)
		if err != nil {
			return fmt.Errorf("error getting cache directory paths: %v", err)
		}
		doDetailPages := len(ps) > 0

		// ps, err := testDirPathsWithPattern(testCatInputDir, slug, htmlSuffix)
		// if err != nil {
		// 	return fmt.Errorf("error getting cache directory paths: %v", err)
		// }
		// doDetailPages := len(ps) > 1

		// glob := filepath.Join(testCatInputDir, name, "*")
		// paths, err := filepath.Glob(glob)
		// if err != nil {
		// 	return fmt.Errorf("error getting cache input paths with glob %q: %v", glob, err)
		// }
		// doDetailPages := len(paths) > 1

		subCmd := GenerateCmd{
			Batch:                      true,
			DoDetailPages:              doDetailPages,
			OnlyKnownDomainDetailPages: true,
			OnlyVaryingFields:          true,
			CacheInputParentDir:        testCatInputDir,
			CacheOutputParentDir:       cmd.CacheOutputDir,
			ConfigOutputParentDir:      cmd.ConfigOutputDir,
			Offline:                    true,
			RenderJs:                   true,
			RequireString:              t.required,
			URL:                        t.url,
		}
		if err := subCmd.Run(globals); err != nil {
			return fmt.Errorf("error running generate with dir: %q, name: %q, url: %q: %v\n", cat, hostSlug, t.url, err)
		}

		// Copy updated config files to testdata config dir.
		// fmt.Println("testCatInputDir", testCatInputDir, "name+_configs", hostSlug+"_configs")
		ps, err = testDirPathsWithPattern(testCatInputDir, hostSlug+"_configs", pageSlug+"*")
		if err != nil {
			return fmt.Errorf("error getting config input paths in %q: %v", testCatInputDir, err)
		}
		count := 0
		for _, p := range ps {
			outPath := filepath.Join(subCmd.ConfigOutputParentDir, hostSlug+"_configs", filepath.Base(p))
			if _, err := utils.CopyStringFile(outPath, p); err != nil {
				fmt.Printf("error copying %q to %q: %v\n", outPath, p, err)
				continue
			}
			count++
		}
		fmt.Printf("  Copied %d/%d config files for test %q\n", count, len(ps), hostSlug)
	}

	// Clear old cache files in testdata cache dir.
	ps, err := testDirPathsWithPattern(testCatInputDir, hostSlug, "*")
	if err != nil {
		return fmt.Errorf("error getting cache output paths in %q: %v", testCatInputDir, err)
	}
	for _, p := range ps {
		// fmt.Printf("removing path: %q\n", path)
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("error removing old cache input path %q: %v", p, err)
		}
	}
	fmt.Printf("  Removed %d old cache files for test %q\n", len(ps), hostSlug)

	// Copy updated cache files to testdata cache dir.
	ps, err = testDirPathsWithPattern(cmd.CacheOutputDir, hostSlug, "*")
	if err != nil {
		return fmt.Errorf("error getting cache output paths in %q: %v", cmd.CacheOutputDir, err)
	}
	for _, p := range ps {
		inPath := filepath.Join(testCatInputDir, hostSlug, filepath.Base(p))
		if _, err := utils.CopyStringFile(p, inPath); err != nil {
			return fmt.Errorf("error copying %q to %q: %v", p, inPath, err)
		}
	}
	fmt.Printf("  Copied %d cache files for test %q\n", len(ps), hostSlug)

	return nil
}

type ScrapeCmd struct {
	CacheInputParentDir  string `default:"/tmp/goskyr/main/" help:"Parent directory for the directory containing cached copies of the html page and linked pages."`
	CacheOutputParentDir string `default:"/tmp/goskyr/main/" help:"Parent directory for the directory that will receive cached copies of the html page and linked pages."`
	ConfigFile           string `arg:"" description:"The location of the configuration file."` // . In case of generation, it should be a directory."`
	File                 string `help:"skip retrieving from the URL and use this saved copy of the page instead"`
	// OutputDir            string `default:"/tmp/goskyr/main/" help:"The output directory."`
	ToStdout bool   `short:"o" long:"stdout" default:"true" help:"If set to true the scraped data will be written to stdout despite any other existing writer configurations. In combination with the -generate flag the newly generated config will be written to stdout instead of to a file."`
	JSONFile string `short:"j" long:"json" description:"Writes scraped data as JSON to the given file path."`
}

func (cmd *ScrapeCmd) Run(globals *Globals) error {
	var cache fetch.Cache
	cache = fetch.NewFetchCache(nil)
	cache = fetch.NewURLFileCache(cache, cmd.CacheInputParentDir, false)
	cache = fetch.NewURLFileCache(cache, cmd.CacheOutputParentDir, true)
	cache = fetch.NewMemoryCache(cache)

	conf, err := scrape.ReadConfig(cmd.ConfigFile)
	if err != nil {
		return fmt.Errorf("error reading config: %v", err)
	}

	recs, err := scrape.Page(cache, conf, &conf.Scrapers[0], &conf.Global, true, cmd.File)
	if err != nil {
		return err
	}
	fmt.Printf("found %d itemMaps\n", len(recs))
	// f := &fetch.FileFetcher{}
	// slugID := fetch.MakeURLStringSlug(conf.Scrapers[0].URL)
	// fetchFn := func(u string) (*goquery.Document, error) {
	// 	u = "file://" + cmd.OutputDir + "/" + slugID + "_cache" + "/" + fetch.MakeURLStringSlug(u) + ".html"
	// 	slog.Debug("in ScrapeCmd.Run()", "u", u)
	// 	return fetch.GQDocument(f, u, nil)
	// }

	if len(conf.Scrapers) > 1 {
		if err = scrape.DetailPages(cache, conf, &conf.Scrapers[1], recs, ""); err != nil {
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

// type TrainCmd struct {
// 	TrainModel string `short:"t" long:"train" description:"Train a ML model based on the given csv features file. This will generate 2 files, goskyr.model and goskyr.class"`
// }

// func (cmd *TrainCmd) Run(globals *Globals) error {
// 	if err := ml.TrainModel(cmd.TrainModel); err != nil {
// 		return fmt.Errorf("error training model: %v", err)
// 	}
// 	return nil
// }
