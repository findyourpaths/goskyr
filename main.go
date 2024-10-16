package main

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/findyourpaths/goskyr/config"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/generate"
	"github.com/findyourpaths/goskyr/ml"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/gosimple/slug"
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
	SubpageRequired     bool   `short:"s" long:"subpage" description:"Whether a URL for a subpage is required in the generated config. If true, configs will not be produced if they don't have a subpage URL field. URLs for images (e.g. ending in .jpg, .gif, or .png) are not considered subpage URLs."`
	TrainModel          string `short:"t" long:"train" description:"Train a ML model based on the given csv features file. This will generate 2 files, goskyr.model and goskyr.class"`
	URL                 string `short:"u" long:"url" description:"URL source of the input URL."`
	PrintVersion        bool
	WordsDir            string `short:"w" default:"word-lists" description:"The directory that contains a number of files containing words of different languages. This is needed for the ML part (use with -e or -b)."`
	ToStdout            bool   `long:"stdout" description:"If set to true the scraped data will be written to stdout despite any other existing writer configurations. In combination with the -generate flag the newly generated config will be written to stdout instead of to a file."`
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
		if _, err := GenerateConfigs(opts); err != nil {
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

var doWriteSubpages = true

func GenerateConfigs(opts mainOpts) (map[string]*scrape.Config, error) {
	slog.Debug("starting to generate config")
	slog.Debug("analyzing", "opts.InputURL", opts.InputURL)

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

	autoOpts := generate.ConfigOptions{
		Batch:           opts.Batch,
		InputURL:        opts.InputURL,
		ModelName:       opts.PretrainedModelPath,
		OnlyVarying:     opts.FieldsVary,
		RenderJS:        opts.RenderJs,
		SubpageRequired: opts.SubpageRequired,
		URL:             opts.URL,
		WordsDir:        opts.WordsDir,
	}

	cims, err := generate.ConfigurationsForURI(autoOpts, minOccs)
	if err != nil {
		return nil, err
	}

	baseu, err := url.Parse(opts.URL)
	if err != nil {
		slog.Error("error parsing input url", "err", err)
		os.Exit(1)
	}
	subpageURLsBySlug := map[string]string{}
	subpageURLSetsByFieldName := map[string]map[string]bool{}

	base := strings.TrimSuffix(opts.ConfigFile, "_config.yml")
	cs := map[string]*scrape.Config{}
	for id, cim := range cims {
		cs[id] = cim.Config
		if opts.ToStdout {
			fmt.Println(cim.Config.String())
		}
		if opts.ConfigFile != "" {
			if err := utils.WriteStringFile(fmt.Sprintf("%s_%s_config.yml", base, id), cim.Config.String()); err != nil {
				return nil, err
			}
			if err := utils.WriteStringFile(fmt.Sprintf("%s_%s.json", base, id), cim.ItemMaps.String()); err != nil {
				return nil, err
			}
		}

		for _, s := range cim.Config.Scrapers {
			// fmt.Printf("found %d subpage URL fields\n", len(s.GetSubpageURLFields()))
			for _, f := range s.GetSubpageURLFields() {
				for _, im := range cim.ItemMaps {
					rel, err := url.Parse(fmt.Sprintf("%v", im[f.Name]))
					slog.Error("error parsing subpage url", "err", err)
					if err != nil {
						continue
					}
					u := baseu.ResolveReference(rel)
					if subpageURLSetsByFieldName[f.Name] == nil {
						subpageURLSetsByFieldName[f.Name] = map[string]bool{}
					}
					subpageURLSetsByFieldName[f.Name][u.String()] = true
					subpageURLsBySlug[slug.Make(u.String())+".html"] = u.String()
				}
			}
		}
	}

	if doWriteSubpages {
		// if err := writeSubpages(subpageURLsBySlug, base); err != nil {
		// 	return nil, err
		// }
		for fname, uset := range subpageURLSetsByFieldName {
			us := []string{}
			for u, _ := range uset {
				us = append(us, u)
			}
			sort.Strings(us)
			subpageURLsListPath := fmt.Sprintf("%s_subpages_%s-urls.txt", base, fname)
			if err := utils.WriteStringFile(subpageURLsListPath, strings.Join(us, "\n")); err != nil {
				return nil, fmt.Errorf("error writing subpage URLs page: %v", err)
			}

			subpagesMerged := strings.Builder{}
			for _, u := range us {
				subpagePath := filepath.Join(fmt.Sprintf("%s_subpages", base), slug.Make(u)+".html")
				subpage, err := utils.ReadStringFile(subpagePath)
				if err != nil {
					return nil, fmt.Errorf("error reading subpage at : %v", err)
				}
				subpagesMerged.WriteString("\n" + subpage + "\n")
			}
			subpagesMergedPath := fmt.Sprintf("%s_subpages_%s.html", base, fname)
			if err := utils.WriteStringFile(subpagesMergedPath, "<htmls>\n"+subpagesMerged.String()+"\n</htmls>\n"); err != nil {
				return nil, fmt.Errorf("error writing merged subpages: %v", err)
			}
		}
	}

	// for fname, uset := range subpageURLSetsByFieldName {
	// subpageURLsListPath := fmt.Sprintf("%s_subpage-urls_%s.txt", base, fname)

	// for _, cim := range cims {
	// 	for _, s := range cim.Config.Scrapers {
	// 		// fmt.Printf("found %d subpage URL fields\n", len(s.GetSubpageURLFields()))
	// 		for _, f := range s.GetSubpageURLFields() {
	// 			for _, im := range cim.ItemMaps {
	// 				rel, err := url.Parse(fmt.Sprintf("%v", im[f.Name]))
	// 				slog.Error("error parsing subpage url", "err", err)
	// 				if err != nil {
	// 					continue
	// 				}
	// 				u := base.ResolveReference(rel)
	// 				subpageURLsBySlug[slug.Make(u.String())+".html"] = u
	// 			}
	// 		}
	// 	}
	// }

	return cs, nil
}

func writeSubpages(subpageURLsBySlug map[string]string, base string) error {
	subpageURLs := []string{}
	for _, u := range subpageURLsBySlug {
		subpageURLs = append(subpageURLs, u)
	}
	sort.Strings(subpageURLs)
	subpageURLsPath := fmt.Sprintf("%s_subpage-urls.txt", base)
	if err := utils.WriteStringFile(subpageURLsPath, strings.Join(subpageURLs, "\n")); err != nil {
		return err
	}

	fetcher := fetch.NewDynamicFetcher("", 0)
	for _, u := range subpageURLs {
		body, err := fetcher.Fetch(u, fetch.FetchOpts{})
		if err != nil {
			slog.Debug("failed to fetch", "url", u, "err", err)
		}
		subpagePath := filepath.Join(fmt.Sprintf("%s_subpages", base), slug.Make(u)+".html")
		if err := utils.WriteStringFile(subpagePath, body); err != nil {
			return err
		}
	}
	return nil
}
