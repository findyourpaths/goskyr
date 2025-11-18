package generate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/observability"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/jpillora/go-tld"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

var DoPruning = true

// MaxRecursionDepth limits how deep expandAllPossibleConfigs can recurse to prevent
// exponential explosion on pages with very deep/complex DOM trees
const MaxRecursionDepth = 10

// addStrategyPrefix adds the strategy prefix ('n' or 's') to the appropriate ID field.
// For detail pages (Field != ""), it modifies SubID. Otherwise it modifies ID.
func addStrategyPrefix(configID *scrape.ConfigID, prefix string) {
	if configID.Field != "" {
		if !strings.HasPrefix(configID.SubID, "n") && !strings.HasPrefix(configID.SubID, "s") {
			configID.SubID = prefix + configID.SubID
		}
	} else {
		if !strings.HasPrefix(configID.ID, "n") && !strings.HasPrefix(configID.ID, "s") {
			configID.ID = prefix + configID.ID
		}
	}
}

// replaceStrategyPrefix replaces 'n' prefix with 's' (or vice versa) in the appropriate ID field.
func replaceStrategyPrefix(configID scrape.ConfigID, newPrefix string) scrape.ConfigID {
	if configID.Field != "" {
		baseSubID := strings.TrimPrefix(configID.SubID, "n")
		baseSubID = strings.TrimPrefix(baseSubID, "s")
		configID.SubID = newPrefix + baseSubID
	} else {
		baseID := strings.TrimPrefix(configID.ID, "n")
		baseID = strings.TrimPrefix(baseID, "s")
		configID.ID = newPrefix + baseID
	}
	return configID
}

// createSequentialConfig creates a sequential strategy config from the nested config parameters.
// Returns the config and scraped records, or an error.
func createSequentialConfig(ctx context.Context, opts ConfigOptions, gqdoc *fetch.Document, pags []scrape.Paginator, rootSelector path, exsCache map[string]string, lps []*locationProps) (*scrape.Config, output.Records, error) {
	seqOpts := opts
	seqOpts.configID = replaceStrategyPrefix(opts.configID, "s")

	// Create sequential scraper
	seqScraper := scrape.Scraper{
		Name:       seqOpts.configID.String(),
		Paginators: pags,
		RenderJs:   opts.RenderJS,
		URL:        opts.URL,
		Strategy:   "sequential",
	}

	// For sequential mode, determine if we should use parent or root selector
	// Check the direct children of the rootSelector to determine if they represent
	// different sibling groups (like event-info vs event-desc) or if all fields
	// come from deeper nested elements
	if len(rootSelector) > 1 {
		// Get the element types at the rootSelector level for all fields
		childPaths := make(map[string]bool)
		allFieldsGoDeeper := true

		for _, lp := range lps {
			if len(lp.path) == len(rootSelector) {
				// Some field paths end exactly at rootSelector
				allFieldsGoDeeper = false
			} else if len(lp.path) > len(rootSelector) {
				// Get the path one level beyond rootSelector
				childPath := lp.path[0 : len(rootSelector)+1].string()
				childPaths[childPath] = true
			}
		}

		// Heuristic to distinguish between:
		// 1. Split sections (event-info/event-desc) - exactly 2 child paths representing structural divisions
		// 2. Flat fields (span.date, span.name, span.link) - 3+ child paths representing individual fields
		//
		// If we have exactly 2 different child paths (like event-info vs event-desc),
		// they likely represent structural sections that should be kept as siblings.
		// If we have 3+ child paths, they likely represent individual fields within a repeating structure.
		if len(childPaths) == 2 && allFieldsGoDeeper {
			// Exactly 2 child paths - likely structural sections, use rootSelector as parent
			seqScraper.Selector = rootSelector.string()
		} else {
			// 1 child path, 3+ child paths, or fields end at rootSelector - trim to get parent
			parentSelector := rootSelector[:len(rootSelector)-1]
			seqScraper.Selector = parentSelector.string()
		}
	} else if len(rootSelector) == 1 {
		// Only one level - use as is
		seqScraper.Selector = rootSelector.string()
	}

	// Special handling for email HTML with section divs
	// Email HTML often has structure: body > ... > div[data-dynamic-sections] > div[data-section-id]
	// where each div[data-section-id] represents one event/record.
	// Check if such a structure exists and use it as the selector.
	sectionDivSelector := `div[data-dynamic-sections="index"] > div[data-section-id]`
	sectionDivCount := gqdoc.Document.Selection.Find(sectionDivSelector).Length()
	if sectionDivCount > 0 {
		// Found section divs - check if this looks like a better selector than what we have
		currentSel := seqScraper.Selector
		currentCount := gqdoc.Document.Selection.Find(currentSel).Filter(currentSel).Length()

		// Use section div selector if:
		// 1. We have a reasonable number of sections (4-100)
		// 2. The current selector matches many more elements (suggesting it's too granular)
		if sectionDivCount >= 4 && sectionDivCount <= 100 && currentCount > sectionDivCount*2 {
			slog.Info("createSequentialConfig() using section div selector for email HTML",
				"originalSelector", currentSel,
				"originalCount", currentCount,
				"sectionDivSelector", sectionDivSelector,
				"sectionDivCount", sectionDivCount)
			seqScraper.Selector = sectionDivSelector
		}
	}

	seqScraper.Fields = processFields(ctx, exsCache, lps, rootSelector)

	// Add validation requiring CTA (link) for sequential mode
	for _, f := range seqScraper.Fields {
		if f.Type == "url" && len(f.ElementLocations) > 0 {
			seqScraper.Validation = &scrape.ValidationConfig{
				RequiresCTASelector: f.ElementLocations[0].Selector,
			}
			break
		}
	}

	seqConfig := &scrape.Config{
		ID:       seqOpts.configID,
		Scrapers: []scrape.Scraper{seqScraper},
	}

	slog.Info("in expandAllPossibleConfigs(), scraping sequential")
	seqRecs, err := scrape.GQDocument(ctx, seqConfig, &seqScraper, gqdoc)
	if err != nil {
		return nil, nil, err
	}
	slog.Info("in expandAllPossibleConfigs(), scraped sequential", "len(seqRecs)", len(seqRecs))
	seqConfig.Records = seqRecs
	return seqConfig, seqRecs, nil
}

// shouldUseSequentialStrategy determines whether the sequential extraction strategy should be used
// based on the presence of date fields and container-ending selectors.
func shouldUseSequentialStrategy(gqdoc *fetch.Document, rootSel string, fields []scrape.Field) bool {
	slog.Info("shouldUseSequentialStrategy() called", "rootSel", rootSel, "len(fields)", len(fields))
	// Check if we have date fields - a key indicator of sequential records
	hasDateField := false
	for _, f := range fields {
		if f.Type == "date_time_tz_ranges" {
			hasDateField = true
			break
		}
	}

	if !hasDateField {
		slog.Info("shouldUseSequentialStrategy() no date field", "rootSel", rootSel)
		return false
	}

	// Check if the selector targets container elements
	// Sequential mode is appropriate when we're selecting sibling elements
	// that should be grouped into records
	// Match selectors ending with container elements (with or without classes)
	endsWithContainer := false
	checkLen := 20
	if len(rootSel) < checkLen {
		checkLen = len(rootSel)
	}
	suffixPart := rootSel[len(rootSel)-checkLen:]
	for _, suffix := range []string{" > div", " > span", " > tr", " > td", " > table"} {
		if strings.HasSuffix(rootSel, suffix) || strings.Contains(suffixPart, suffix+".") || strings.Contains(suffixPart, suffix+"#") {
			endsWithContainer = true
			break
		}
	}
	if !endsWithContainer {
		slog.Info("shouldUseSequentialStrategy() selector doesn't end with container element", "rootSel", rootSel)
		return false
	}

	// Simple heuristic: if we have date fields, try sequential strategy
	// The duplicate record filtering will handle cases where it doesn't make sense
	slog.Info("shouldUseSequentialStrategy() yes - has date field", "rootSel", rootSel)
	return true
}

// ConfigOptions contains configuration parameters for scraper generation.
type ConfigOptions struct {
	Batch bool
	// CacheInputDir             string
	// CacheOutputDir            string
	ConfigOutputParentDir      string
	ConfigOutputDir            string
	DoDetailPages              bool
	MinOccs                    []int
	MinRecords                 int
	ModelName                  string
	Offline                    bool
	OnlyKnownDomainDetailPages bool
	OnlyVaryingFields          bool
	RenderJS                   bool
	RequireDates               bool
	RequireDetailURL           string
	RequireString              string
	URL                        string
	WordsDir                   string
	configID                   scrape.ConfigID
	configPrefix               string
}

// InitOpts initializes configuration options by parsing the URL and setting up directory paths.
func InitOpts(opts ConfigOptions) (ConfigOptions, error) {
	if len(opts.URL) == 0 {
		return opts, errors.New("URL cannot be empty")
	}

	u, err := url.Parse(opts.URL)
	if err != nil {
		return opts, fmt.Errorf("error parsing input URL %q: %v", opts.URL, err)
	}
	opts.configID.Slug = fetch.MakeURLStringSlug(opts.URL)
	// prefix := fetch.MakeURLStringSlug(u.Host)

	// if opts.CacheInputDir != "" {
	// 	opts.CacheInputDir = filepath.Join(opts.CacheInputDir, prefix)
	// }

	// if opts.CacheOutputDir != "" {
	// 	opts.CacheOutputDir = filepath.Join(opts.CacheOutputDir, prefix)
	// }

	if opts.ConfigOutputParentDir != "" {
		opts.ConfigOutputDir = filepath.Join(opts.ConfigOutputParentDir, fetch.MakeURLStringSlug(u.Host)+"_configs")
	}

	return opts, nil
}

// logNonDefaultConfigOptions logs all ConfigOptions fields that have non-default values.
func logNonDefaultConfigOptions(opts ConfigOptions) {
	v := reflect.ValueOf(opts)
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		// Skip unexported fields
		if !fieldType.IsExported() {
			continue
		}

		// Log field if it has a non-default value
		if !field.IsZero() {
			// slog.Info("ConfigOption", fieldType.Name, field.Interface())
			fmt.Println("ConfigOption", fieldType.Name, field.Interface())
		}
	}
}

// ConfigurationsForPage generates scraper configurations for a web page by fetching the page
// from the cache and analyzing its HTML structure.
func ConfigurationsForPage(ctx context.Context, cache fetch.Cache, opts ConfigOptions) (map[string]*scrape.Config, error) {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/generate").Start(ctx, "generate.ConfigurationsForPage")

	// Metering
	// source := "error"
	defer func() {
		// entity.observability.Add(ctx, observability.Instruments.Generate, 1,
		// 	// attribute.String("source", source),
		// 	attribute.Int("arg.i", i),
		// 	attribute.Int64("arg.gmail_id", ret.Email.GmailId),
		// 	attribute.String("ret.title", ret.Title),
		// 	attribute.Int("ret.images.len", len(ret.Images)),
		// 	attribute.Int("ret.links.len", len(ret.Links)),
		// 	attribute.Int("ret.datetime_ranges.len", len(ret.DatetimeRanges)),
		// )
		span.End()
	}()

	// Logging
	if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_ConfigurationsForPage_log.txt"), slog.LevelDebug)
		if err != nil {
			return nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Info("ConfigurationsForPage()", "opts", opts)
	defer slog.Info("ConfigurationsForPage() returning")

	scrape.DebugGQFind = false

	gqdoc, found, err := fetch.GetGQDocument(cache, opts.URL) //fetchGQDocument(opts, fetch.TrimURLScheme(opts.URL), map[string]*fetch.Document{})
	if err != nil {
		return nil, fmt.Errorf("failed to get page %s (found: %t): %v", opts.URL, found, err)
	}
	if !found || gqdoc == nil {
		return nil, fmt.Errorf("page not found in cache: %s (this usually means the page failed to fetch - check repository logs for HTTP fetch errors)", opts.URL)
	}
	return ConfigurationsForGQDocument(ctx, cache, opts, gqdoc)
}

// ConfigurationsForGQDocument generates scraper configurations for a parsed HTML document by trying
// multiple minimum occurrence thresholds to find repeating patterns.
func ConfigurationsForGQDocument(ctx context.Context, cache fetch.Cache, opts ConfigOptions, gqdoc *fetch.Document) (map[string]*scrape.Config, error) {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/generate").Start(ctx, "generate.ConfigurationsForGQDocument")

	// Metering
	// source := "error"
	defer func() {
		// entity.observability.Add(ctx, observability.Instruments.Generate, 1,
		// 	// attribute.String("source", source),
		// 	attribute.Int("arg.i", i),
		// 	attribute.Int64("arg.gmail_id", ret.Email.GmailId),
		// 	attribute.String("ret.title", ret.Title),
		// 	attribute.Int("ret.images.len", len(ret.Images)),
		// 	attribute.Int("ret.links.len", len(ret.Links)),
		// 	attribute.Int("ret.datetime_ranges.len", len(ret.DatetimeRanges)),
		// )
		span.End()
	}()

	// Logging
	logNonDefaultConfigOptions(opts)

	// cims := map[string]*scrape.Config{}
	// fmt.Println("in ConfigurationsForGQDocument()", "cims == nil", cims == nil)
	var err error
	rs := map[string]*scrape.Config{}
	// Generate configs for each of the minimum occs.
	minOccs := opts.MinOccs
	sort.Sort(sort.Reverse(sort.IntSlice(minOccs)))
	for _, minOcc := range minOccs {
		slog.Info("calling ConfigurationsForGQDocument()", "minOcc", minOcc, "rs == nil", rs == nil)
		// fmt.Println("calling ConfigurationsForGQDocument()", "minOcc", minOcc, "rs == nil", rs == nil)
		rs, err = ConfigurationsForGQDocumentWithMinOccurrence(ctx, cache, opts, gqdoc, minOcc, rs)
		if err != nil {
			return nil, err
		}
		// for k, v := range rs {
		// 	rs[k] = v
		// }
	}

	slog.Info("in ConfigurationsForPage()", "len(rs)", len(rs))

	// Warn if MinRecords filter resulted in no configs
	if opts.MinRecords > 0 && len(rs) == 0 {
		slog.Warn("No configurations produced at least the required number of records",
			"min_required", opts.MinRecords,
			"suggestion", fmt.Sprintf("Try lowering --min-records or check if the page has fewer records than %d", opts.MinRecords))
	}

	return rs, nil
}

// func ConfigurationsForGQDocuments(opts ConfigOptions, gqdocs []*fetch.Document, minOcc int, gqdocsByURL map[string]*fetch.Document) (map[string]*scrape.Config, map[string]*fetch.Document, error) {
// 	_, gqdoc, err := joinGQDocuments(gqdocs)
// 	if err != nil {
// 		return nil, nil, err
// 	}
// 	return ConfigurationsForGQDocumentWithMinOccurrence(opts, gqdoc, minOcc, gqdocsByURL)
// }

// ConfigurationsForGQDocumentWithMinOccurrence generates scraper configurations using a specific
// minimum occurrence threshold to identify repeating patterns in the HTML.
func ConfigurationsForGQDocumentWithMinOccurrence(ctx context.Context, cache fetch.Cache, opts ConfigOptions, gqdoc *fetch.Document, minOcc int, rs map[string]*scrape.Config) (map[string]*scrape.Config, error) {
	minOccStr := fmt.Sprintf("%02da", minOcc)
	if opts.configID.Field != "" {
		opts.configID.SubID = minOccStr
	} else {
		opts.configID.ID = minOccStr
	}

	// NOTE: Don't add strategy prefix here - this function generates both nested and sequential configs
	// The prefix is added later in expandAllPossibleConfigs() after the strategy is determined

	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/generate").Start(ctx, fmt.Sprintf("generate.ConfigurationsForGQDocumentWithMinOccurrence(%d, %q, len(rs): %d)", minOcc, opts.configID.String(), len(rs)))

	// Metering
	status := "unknown"
	var lps []*locationProps
	var pagProps []*locationProps
	defer func() {
		observability.Add(ctx, observability.Instruments.Generate, 1,
			attribute.String("status", status),
			attribute.Int("arg.minocc", minOcc),
			attribute.Int("int.lps.len", len(lps)),
			attribute.Int("int.pag_props.len", len(pagProps)),
			attribute.Int("ret.len", len(rs)),
		)
		span.End()
	}()

	// Logging
	if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_ConfigurationsForGQDocument_log.txt"), slog.LevelDebug)
		if err != nil {
			return nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Info("ConfigurationsForGQDocumentWithMinOccurrence()")
	defer slog.Info("ConfigurationsForGQDocumentWithMinOccurrence() returning")
	// fmt.Println("in ConfigurationsForGQDocumentWithMinOccurrence()", "results == nil", rs == nil)

	// fmt.Println("in ConfigurationsForGQDocumentWithMinOccurrence()", "gqdoc == nil", gqdoc == nil)
	// fmt.Println("in ConfigurationsForGQDocumentWithMinOccurrence()", "gqdoc.Document == nil", gqdoc.Document == nil)
	htmlStr, err := goquery.OuterHtml(gqdoc.Document.Children())
	if err != nil {
		return nil, fmt.Errorf("error when generating configurations for GQDocument: %v", err)
	}

	lps, pagProps, err = analyzePage(ctx, opts, htmlStr, minOcc)
	if err != nil {
		return nil, fmt.Errorf("error when generating configurations for GQDocument: %v", err)
	}
	if len(lps) == 0 {
		status = "failed_to_find_fields"
		// No fields were found, so just return.
		return rs, nil
	}

	// slog.Debug("in ConfigurationsForGQDocument, before expanding", "len(a.LocMan)", len(a.LocMan))
	slog.Info("in ConfigurationsForGQDocumentWithMinOccurrence(), before expanding", "len(lps)", len(lps))
	slog.Info("in ConfigurationsForGQDocumentWithMinOccurrence(), before expanding", "len(pagProps)", len(pagProps))
	// rs := results

	// FIXME
	// if !opts.DoDetailPages {
	pagProps = []*locationProps{}
	// }

	exsCache := map[string]string{}
	rootSel := findSharedRootSelector(ctx, gqdoc, lps)
	rs, err = expandAllPossibleConfigs(ctx, cache, exsCache, gqdoc, opts, "", lps, rootSel, pagProps, rs)
	if err != nil {
		status = "failed_to_expand"
		return nil, err
	}

	slog.Info("in ConfigurationsForGQDocumentWithMinOccurrence()", "len(results)", len(rs))
	// fmt.Println("in ConfigurationsForGQDocumentWithMinOccurrence()", "len(results)", len(rs))
	status = "success"
	return rs, nil
}

// expandAllPossibleConfigs generates nested and sequential scraper configurations from discovered
// field locations and recursively explores field clusters to create variant configurations.
func expandAllPossibleConfigs(ctx context.Context, cache fetch.Cache, exsCache map[string]string, gqdoc *fetch.Document, opts ConfigOptions, clusterID string, lps []*locationProps, rootSelector path, pagProps []*locationProps, rs map[string]*scrape.Config) (map[string]*scrape.Config, error) {
	return expandAllPossibleConfigsWithDepth(ctx, cache, exsCache, gqdoc, opts, clusterID, lps, rootSelector, pagProps, rs, 0)
}

func expandAllPossibleConfigsWithDepth(ctx context.Context, cache fetch.Cache, exsCache map[string]string, gqdoc *fetch.Document, opts ConfigOptions, clusterID string, lps []*locationProps, rootSelector path, pagProps []*locationProps, rs map[string]*scrape.Config, depth int) (map[string]*scrape.Config, error) {
	// Check recursion depth limit
	if depth >= MaxRecursionDepth {
		slog.Warn("expandAllPossibleConfigs hit max recursion depth, stopping",
			"depth", depth,
			"max_depth", MaxRecursionDepth,
			"config_id", opts.configID.String(),
			"clusters_found_so_far", len(rs))
		return rs, nil
	}

	rootSel := rootSelector.string()

	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/generate").Start(ctx, fmt.Sprintf("generate.expandAllPossibleConfigs(%q, %q, %d, %q, %d, depth:%d)", opts.configID.String(), clusterID, len(lps), rootSel, len(pagProps), depth))

	// Metering
	status := "unknown"
	var recs output.Records
	var clusterIDs []string
	var clusters map[string][]*locationProps
	var s scrape.Scraper
	var include bool
	defer func() {
		observability.Add(ctx, observability.Instruments.Generate, 1,
			attribute.String("status", status),
			// 	// attribute.String("source", source),
			attribute.String("arg.opts", opts.configID.String()),
			attribute.Int("arg.lps.len", len(lps)),
			attribute.String("arg.root_selector", rootSel),
			attribute.Int("arg.pag_props.len", len(pagProps)),
			attribute.Int("int.recs.len", len(recs)),
			attribute.Int("int.recs.total_fields", recs.TotalFields()),
			attribute.Int("int.clusters.len", len(clusters)),
			attribute.String("int.cluster_ids", fmt.Sprintf("%#v", clusterIDs)),
			attribute.String("int.scraper", fmt.Sprintf("%#v", s)),
			attribute.Bool("int.include", include),
			attribute.Int("ret.len", len(rs)),
		)
		span.End()
	}()

	// Add 'n' prefix for nested strategy (the default) to distinguish from sequential
	addStrategyPrefix(&opts.configID, "n")

	// Logging
	fmt.Printf("in expandAllPossibleConfigs(), opts.configID: %q\n", opts.configID)
	if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_expandAllPossibleConfigs_log.txt"), slog.LevelDebug)
		if err != nil {
			return nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Info("expandAllPossibleConfigs()")
	defer slog.Info("expandAllPossibleConfigs() returning")
	// fmt.Println("in expandAllPossibleConfigs()", "results == nil", rs == nil)

	slog.Info("in expandAllPossibleConfigs()", "opts.configID", opts.configID, "len(lps)", len(lps))
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		for i, lp := range lps {
			slog.Debug("in expandAllPossibleConfigs()", "i", i, "lp", lp.DebugString())
		}
	}

	pags := []scrape.Paginator{}
	for _, lp := range pagProps {
		pags = append(pags, scrape.Paginator{
			Location: scrape.ElementLocation{
				Selector: lp.path.string(),
				// AllNodes:      true,
				// EntireSubtree: true,
				// Separator:     "\n",
			}})
	}
	sort.Slice(pags, func(i, j int) bool {
		return pags[i].Location.Selector < pags[j].Location.Selector
	})

	slog.Info("in expandAllPossibleConfigs()", "pags", pags)

	s = scrape.Scraper{
		Name:       opts.configID.String(),
		Paginators: pags,
		RenderJs:   opts.RenderJS,
		URL:        opts.URL,
	}

	// s.Record = shortenRootSelector(rootSelector).string()
	s.Selector = rootSel
	s.Fields = processFields(ctx, exsCache, lps, rootSelector)

	// Detect if we should generate both nested and sequential strategies
	generateSequential := shouldUseSequentialStrategy(gqdoc, rootSel, s.Fields)

	// Generate nested config (always)
	// Note: opts.configID already has 'n' prefix added at start of function
	nestedOpts := opts
	nestedOpts.configID = opts.configID

	if opts.DoDetailPages && len(s.GetDetailPageURLFields()) == 0 {
		slog.Info("candidate configuration failed to find a detail page URL field, excluding", "opts.configID", nestedOpts.configID)
		status = "failed_to_find_detail_page_url"
		return rs, nil
	}

	nestedConfig := &scrape.Config{
		ID:       nestedOpts.configID,
		Scrapers: []scrape.Scraper{s},
	}

	var err error
	slog.Info("in expandAllPossibleConfigs(), scraping nested")
	recs, err = scrape.GQDocument(ctx, nestedConfig, &s, gqdoc)
	if err != nil {
		slog.Info("candidate configuration got error scraping GQDocument, excluding", "opts.configID", nestedOpts.configID)
		status = "failed_to_scrape_gqdoc"
		return nil, err
	}
	slog.Info("in expandAllPossibleConfigs(), scraped nested", "len(recs)", len(recs))
	nestedConfig.Records = recs

	// Store nested config
	c := nestedConfig

	// Generate sequential config if applicable
	if generateSequential {
		seqConfig, seqRecs, err := createSequentialConfig(ctx, opts, gqdoc, pags, rootSelector, exsCache, lps)
		if err != nil {
			slog.Warn("failed to scrape sequential config, skipping", "opts.configID", opts.configID, "err", err)
		} else {
			// Check MinRecords before adding sequential config
			if opts.MinRecords > 0 && len(seqRecs) < opts.MinRecords {
				slog.Info("sequential config produced too few records, skipping",
					"id", seqConfig.ID.String(),
					"records_produced", len(seqRecs),
					"min_required", opts.MinRecords)
			} else {
				// Add sequential config to results if it produces unique records
				seqRecsStr := seqRecs.String()
				if _, found := rs[seqRecsStr]; !found {
					rs[seqRecsStr] = seqConfig
					slog.Info("added sequential config", "id", seqConfig.ID.String())
				} else {
					slog.Info("sequential config produces duplicate records, skipping", "id", seqConfig.ID.String())
				}
			}
		}
	}

	clusters = findClusters(ctx, lps, rootSelector)
	for clusterID := range clusters {
		clusterIDs = append(clusterIDs, clusterID)
	}
	sort.Strings(clusterIDs)

	if slog.Default().Enabled(ctx, slog.LevelInfo) {
		slog.Info("in expandAllPossibleConfigs()", "len(recs)", len(recs), "recs.TotalFields()", recs.TotalFields(), "len(clusters)", len(clusters))
	}

	include = true
	recsStr := recs.String()
	// fmt.Printf("strings.Index(itemsStr, opts.RequireString): %d\n", strings.Index(itemsStr, opts.RequireString))
	if opts.RequireString != "" && strings.Index(recsStr, opts.RequireString) == -1 {
		slog.Info("candidate configuration failed to extract the required string, excluding", "opts.configID", opts.configID) //, "opts.RequireString", opts.RequireString, "recsStr", recsStr)
		include = false
		// return rs, nil
	}
	if opts.RequireDates {
		count := 0
		for _, rec := range recs {
			for k := range rec {
				// slog.Info("found", "key", k, "value", v)
				if strings.HasSuffix(k, scrape.DateTimeFieldSuffix) {
					count++
					break
				}
			}
		}
		slog.Info("found", "count", count, "len(recs)", len(recs))
		if float32(count)/float32(len(recs)) < 0.5 {
			slog.Info("candidate configuration failed to find dates, excluding", "opts.configID", opts.configID)
			include = false
		}
	}

	// Check MinRecords threshold
	if opts.MinRecords > 0 && len(recs) < opts.MinRecords {
		slog.Info("candidate configuration produced too few records, excluding",
			"opts.configID", opts.configID,
			"records_produced", len(recs),
			"min_required", opts.MinRecords)
		include = false
		status = "failed_min_records"
	}

	if include {
		prevConfig, found := rs[recsStr]
		// fmt.Println("in generate.expandAllPossibleConfigs()", "found", found, "len(recsStr)", len(recsStr), "c.ID", c.ID)
		if DoPruning && found {
			slog.Info("candidate configuration failed to produce different records, pruning", "prevID", prevConfig.ID, "opts.configID", opts.configID)
			include = false
			// return rs, nil
		}
		if include {
			rs[recsStr] = c
		}
	}

	lastID := 'a'
	for _, clusterID := range clusterIDs {
		slog.Debug("in expandAllPossibleConfigs()", "clusterID", clusterID)
		nextOpts := opts
		if opts.configID.Field != "" {
			nextOpts.configID.SubID += string(lastID)
		} else {
			nextOpts.configID.ID += string(lastID)
		}
		nextLPs := clusters[clusterID]
		nextRootSel := clusters[clusterID][0].path[0 : len(rootSelector)+1]
		rs, err = expandAllPossibleConfigsWithDepth(ctx, cache, exsCache, gqdoc, nextOpts, clusterID, nextLPs, nextRootSel, pagProps, rs, depth+1)
		if err != nil {
			status = "failed_to_expand"
			return nil, err
		}
		lastID++
	}

	status = "success"
	return rs, nil
}

// ExtendPageConfigsWithNexts extends page configurations by adding records from next pages
// identified by pagination links.
func ExtendPageConfigsWithNexts(ctx context.Context, cache fetch.Cache, opts ConfigOptions, pageConfigs map[string]*scrape.Config) error {
	gqdoc, _, err := fetch.GetGQDocument(cache, opts.URL)
	if err != nil {
		return fmt.Errorf("failed to get next page %s: %v", opts.URL, err)
	}

	pageCIDs := []string{}
	for _, pageC := range pageConfigs {
		pageCIDs = append(pageCIDs, pageC.ID.String())
	}
	sort.Strings(pageCIDs)

	for _, id := range pageCIDs {
		if err := ExtendPageConfigRecordsWithNext(ctx, cache, opts, pageConfigs[id], fetch.NewSelection(gqdoc.Document.Selection)); err != nil {
			return fmt.Errorf("error extending page config records with next page records: %v", err)
		}
	}
	return nil
}

// ExtendPageConfigRecordsWithNext extends a single page configuration with records from the next
// page by following pagination links and scraping additional records.
func ExtendPageConfigRecordsWithNext(ctx context.Context, cache fetch.Cache, opts ConfigOptions, pageC *scrape.Config, sel *fetch.Selection) error {
	// fmt.Printf("looking at %q\n", pageC.ID.String())
	// fmt.Printf("looking at opts url %q\n", fetch.TrimURLScheme(opts.URL))

	// Collect all of the proposed next urls from all the scraper's paginators.
	pageS := pageC.Scrapers[0]
	usMap := map[string]scrape.Paginator{}
	for _, pag := range pageS.Paginators {
		// hash := crc32.ChecksumIEEE([]byte(pag.Location.Selector))
		// fmt.Printf("using pag with hash: %#v\n", hash)
		_, uu, err := scrape.GetTextStringAndURL(&pag.Location, sel, opts.URL)
		if err != nil {
			fmt.Printf("ERROR: failed to get next page url: %v\n", err)
			continue
		}
		// fmt.Printf("found next page url: %q\n", u)

		u := uu.String()
		if strings.HasPrefix(u, "javascript:") {
			continue
		}
		u = fetch.TrimURLScheme(u)
		shortURL := fetch.TrimURLScheme(opts.URL)
		// fmt.Printf("looking at next url %q\n", uStr)
		if u == shortURL ||
			"www."+u == shortURL ||
			u == "www."+shortURL {
			continue
		}
		usMap[u] = pag
	}

	// Download all of the proposed next pages at the urls.
	us := []string{}
	for u := range usMap {
		us = append(us, fetch.TrimURLScheme(u))
	}

	// FIXME
	gqdocsByURL := map[string]*fetch.Document{}
	// gqdocsByURL, err := fetchGQDocumentsByURL(uStrs, opts.CacheInputDir, opts.ConfigOutputFullDir)
	// if err != nil {
	// 	return fmt.Errorf("failed to fetch next pages: %v", err)
	// }

	// Scrape records for the proposed next pages.
	// f := &fetch.FileFetcher{}
	newPags := []scrape.Paginator{}
	for u, pag := range usMap {
		nextGQDoc := gqdocsByURL[u]
		// , err := goquery.NewDocumentFromReader(strings.NewReader(nextStr))
		// if err != nil {
		// 	return err
		// }

		// // fmt.Printf("extended %q with records from page %q\n", pageC.ID.String(), uStr)
		// path := filepath.Join(opts.CacheInputDir, fetch.MakeURLStringSlug(uStr)+".html")
		// nextGQDoc, err := fetch.GQDocument(f, "file://"+path, nil)
		// // fmt.Printf("adding subURL: %q\n", subURL)
		// if err != nil {
		// 	fmt.Printf("ERROR: error fetching detail page at %q: %v\n", path, err)
		// 	continue
		// }

		// fmt.Printf("read next page: %q\n", u)

		recs, err := scrape.GQDocument(ctx, pageC, &pageS, nextGQDoc)
		if err != nil {
			return err
		}
		// fmt.Println("found", "len(recs)", len(recs))

		if len(recs) == 0 {
			continue
		}

		pageC.Records = append(pageC.Records, recs...)
		newPags = append(newPags, pag)

		// fmt.Printf("extended %q to %d records\n", pageC.ID.String(), len(pageC.RecordMaps))

		// rel, err := url.Parse(fj.value)
		// if err != nil {
		// 	slog.Error("error parsing detail page url", "err", err)
		// 	continue
		// }
		// fj.url = uBase.ResolveReference(rel).String()

	}

	pageC.Scrapers[0].Paginators = newPags
	return nil
}

type pageJoin struct {
	config     *scrape.Config
	fieldJoins []*fieldJoin
}

type fieldJoin struct {
	name  string
	value string
	url   string
}

// pageJoinsURLs extracts all unique URLs from a map of page joins.
func pageJoinsURLs(pageJoinsMap map[string][]*pageJoin) []string {
	us := map[string]bool{}
	for _, pjs := range pageJoinsMap {
		for _, pj := range pjs {
			for _, fj := range pj.fieldJoins {
				// fmt.Printf("fj: %#v\n", fj)
				us[fj.url] = true
			}
		}
	}
	rs := []string{}
	for u := range us {
		rs = append(rs, "http://"+u)
	}
	sort.Strings(rs)
	return rs
}

var BlockedDomains = map[string]bool{
	"wikipedia": true,
	"google":    true, // Block Google Calendar "Add to Calendar" links
}

var KnownDomains = map[string]bool{
	"ticketweb": true,
	"dice":      true,
}

// ConfigurationsForAllDetailPages generates scraper configurations for all detail pages linked
// from list page configurations by collecting URL fields and generating detail page scrapers.
func ConfigurationsForAllDetailPages(ctx context.Context, cache fetch.Cache, opts ConfigOptions, pageConfigs map[string]*scrape.Config) (map[string]*scrape.Config, error) {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/generate").Start(ctx, "generate.ConfigurationsForAllDetailPages")

	// Metering
	// source := "error"
	var subURLs []string
	var fnames []string
	defer func() {
		observability.Add(ctx, observability.Instruments.Generate, 1,
			// 	// attribute.String("source", source),
			// 	attribute.Int("arg.i", i),
			// 	attribute.Int64("arg.gmail_id", ret.Email.GmailId),
			attribute.String("int.sub_urls", strings.Join(subURLs, "\n")),
			attribute.String("int.fnames", strings.Join(fnames, "\n")),
		// 	attribute.Int("ret.images.len", len(ret.Images)),
		// 	attribute.Int("ret.links.len", len(ret.Links)),
		// 	attribute.Int("ret.datetime_ranges.len", len(ret.DatetimeRanges)),
		)
		span.End()
	}()

	// Logging
	if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_ConfigurationsForAllDetailPages_log.txt"), slog.LevelDebug)
		if err != nil {
			return nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Info("ConfigurationsForAllDetailPages()")
	defer slog.Info("ConfigurationsForAllDetailPages() returning")

	slog.Info("in ConfigurationsForAllDetailPages()", "opts.URL", opts.URL)
	slog.Info("in ConfigurationsForAllDetailPages()", "opts.ConfigOutputFullDir", opts.ConfigOutputDir)
	slog.Debug("in ConfigurationsForAllDetailPages()", "opts", opts)

	// pageCIDs := []string{}
	// for _, pageC := range pageConfigs {
	// 	pageCIDs = append(pageCIDs, pageC.ID.String())
	// }

	uBase, err := tld.Parse(opts.URL)
	if err != nil {
		uBase = nil
		// return nil, fmt.Errorf("error parsing input url %q: %v", opts.URL, err)
	}

	pageJoinsByFieldName := map[string][]*pageJoin{}
	fieldURLsByFieldName := map[string][]string{}
	slog.Info("[DEBUG] ConfigurationsForAllDetailPages() processing page configs", "page_config_count", len(pageConfigs))
	for _, pageC := range pageConfigs {
		// pageCIDs = append(pageCIDs, pageC.ID.String())
		pageS := pageC.Scrapers[0]
		detailURLFields := pageS.GetDetailPageURLFields()
		slog.Info("[DEBUG] Processing page config", "config_id", pageC.ID.String(), "detail_url_field_count", len(detailURLFields), "record_count", len(pageC.Records))
		// fmt.Printf("found %d detail page URL fields\n", len(s.GetDetailPageURLFields()))
		for _, pageF := range detailURLFields {
			pj := &pageJoin{config: pageC}
			pageJoinsByFieldName[pageF.Name] = append(pageJoinsByFieldName[pageF.Name], pj)
			slog.Info("[DEBUG] Processing field", "field_name", pageF.Name, "field_type", pageF.Type)
			recordsProcessed := 0
			recordsSkipped := 0
			for i, pageIM := range pageC.Records {
				fieldValue := pageIM[pageF.Name]
				// Not sure why we need this...
				if fieldValue == "" {
					recordsSkipped++
					if i < 3 { // Log first 3 skipped records
						slog.Info("[DEBUG] Skipping record (empty field)", "record_index", i, "field_name", pageF.Name)
					}
					continue
				}
				recordsProcessed++
				if recordsProcessed <= 3 { // Log first 3 processed records
					slog.Info("[DEBUG] Processing record", "record_index", i, "field_name", pageF.Name, "field_value", fieldValue)
				}
				fj := &fieldJoin{
					// pageConfig: pageC
					// pageItemMap: pageIM
					name:  pageF.Name,
					value: fmt.Sprintf("%v", pageIM[pageF.Name]),
				}
				// fmt.Printf("created fj: %#v with %#v\n", fj, pageIM[pageF.Name])

				if scrape.SkipSubURLExt[filepath.Ext(fj.value)] {
					slog.Debug("skipping sub URL due to extension", "fj.value", fj.value)
					continue
				}

				relURL, err := url.Parse(fj.value)
				if err != nil {
					slog.Error("error parsing detail page relative url", "fj.value", fj.value, "err", err)
					continue
				}

				absStr := relURL.String()
				if uBase != nil {
					absStr = uBase.ResolveReference(relURL).String()
				}

				// Check domain before resolving redirects to avoid wasting time on blocked domains
				preCheckURL, err := tld.Parse(absStr)
				if err == nil && BlockedDomains[preCheckURL.Domain] {
					slog.Debug("skipping URL with blocked domain (pre-check)", "absStr", absStr, "domain", preCheckURL.Domain)
					continue
				}

				// Resolve redirects to get final URL
				resolvedStr, err := cache.GetResolvedURL(absStr)
				if err != nil {
					slog.Error("error resolving URL redirects", "absStr", absStr, "err", err)
					// Fall back to original URL if resolution fails
					resolvedStr = absStr
				}

				absURL, err := tld.Parse(resolvedStr)
				if err != nil {
					slog.Error("error parsing detail page absolute url", "resolvedStr", resolvedStr, "err", err)
					continue
				}

				// fmt.Printf("rel: %q, %#v\n", rerel)
				if !scrape.KeepSubURLScheme[absURL.Scheme] {
					slog.Debug("skipping sub URL with non-http(s) scheme", "fj.value", fj.value)
					continue
				}

				// fmt.Println("checking skipping sub URL with different domain", "uBase", uBase, "fj.value", fj.value, "opts.OnlySameDomainDetailPages", opts.OnlySameDomainDetailPages, "uBase.Domain != absURL.Domain", uBase.Domain != absURL.Domain)
				slog.Debug("checking", "absURL", absURL)
				slog.Debug("checking", "absURL.Domain", absURL.Domain)
				// Skip domain checking for email sources (gmail://) - emails can link to any domain
				isEmailSource := strings.HasPrefix(opts.URL, "gmail://")
				if !isEmailSource && opts.OnlyKnownDomainDetailPages && !(uBase.Domain == absURL.Domain || KnownDomains[absURL.Domain]) {
					slog.Debug("skipping sub URL with different domain", "uBase", uBase, "fj.value", fj.value)
					continue
				}
				if BlockedDomains[absURL.Domain] {
					slog.Debug("skipping sub URL with blocked domain", "uBase", uBase, "fj.value", fj.value)
					continue
				}

				fj.url = fetch.TrimURLScheme(absURL.String())
				fieldURLsByFieldName[fj.name] = append(fieldURLsByFieldName[fj.name], fj.url)
				// fmt.Printf("adding to pj.config.ID: %q fj: %#v\n", pj.config.ID, fj)
				pj.fieldJoins = append(pj.fieldJoins, fj)
			}
		}
	}

	subURLs = pageJoinsURLs(pageJoinsByFieldName)
	if opts.ConfigOutputDir != "" {
		urlsPath := filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_urls.txt")
		if err := utils.WriteStringFile(urlsPath, strings.Join(subURLs, "\n")); err != nil {
			return nil, fmt.Errorf("failed to write detail page URLs list: %v", err)
		}
	}
	// slog.Debug("in ConfigurationsForAllDetailPages()", "opts.CacheInputDir", opts.CacheInputDir)

	for _, fURLs := range fieldURLsByFieldName {
		sort.Strings(fURLs)
	}

	for fname := range pageJoinsByFieldName {
		fnames = append(fnames, fname)
	}
	sort.Strings(fnames)
	// var cs map[string]*scrape.Config
	rs := map[string]*scrape.Config{}
	fieldURLsSeen := map[string]string{}
	for _, fname := range fnames {
		fURLs := strings.Join(fieldURLsByFieldName[fname], "\n")
		if prevFName, found := fieldURLsSeen[fURLs]; found {
			// Avoid creating redundant configurations when each entry in a list page
			// has multiple links to its detail page, e.g. from the event title, the
			// profile picture, the "register now" link, etc. Instead, just creates a
			// config for the first link path and skips the rest.
			slog.Debug("skipping making configurations for new field with the same URL values as old field", "fname", fname, "prevFName", prevFName)
			continue
		}
		fieldURLsSeen[fURLs] = fname

		pjs := pageJoinsByFieldName[fname]
		// fmt.Println("in ConfigurationsForAllDetailPages()", "fname", fname)
		opts.configID.Field = fname
		sort.Slice(pjs, func(i, j int) bool {
			return pjs[i].config.ID.String() < pjs[j].config.ID.String()
		})
		rs, err = ConfigurationsForDetailPages(ctx, cache, opts, pjs, rs)
		if err != nil {
			return nil, fmt.Errorf("error generating configuration for detail pages for field %q: %v", fname, err)
		}

		// for recsStr, c := range cs {
		// 	prevConfig, found := rs[recsStr]
		// 	fmt.Println("in generate.ConfigurationsForAllDetailPages()", "found", found, "len(recsStr)", len(recsStr), "c.ID", c.ID)
		// 	if found {
		// 		slog.Info("candidate all detail page configuration failed to produce different records, excluding", "prevID", prevConfig.ID, "opts.configID", opts.configID)
		// 		continue
		// 	}
		// 	rs[recsStr] = c
		// }
	}

	slog.Info("in ConfigurationsForAllDetailPages()", "len(rs)", len(rs))
	return rs, nil
}

// ConfigurationsForDetailPages collects the URL values for a candidate detail
// page field, retrieves the pages at those URLs, concatenates them, trains a
// scraper to extract from those detail pages, and merges the resulting records
// into the parent page, outputting the result.
func ConfigurationsForDetailPages(ctx context.Context, cache fetch.Cache, opts ConfigOptions, pjs []*pageJoin, rs map[string]*scrape.Config) (map[string]*scrape.Config, error) {
	if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_ConfigurationsForDetailPages_log.txt"), slog.LevelDebug)
		if err != nil {
			return nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Info("ConfigurationsForDetailPages()", "opts", opts)
	defer slog.Info("ConfigurationsForDetailPages() returning")

	gqdoc, err := joinPageJoinsGQDocuments(cache, opts, pjs)
	if err != nil {
		return nil, err
	}
	// Prepare for calling general page generator.
	opts.DoDetailPages = false
	opts.RequireString = ""
	cs, err := ConfigurationsForGQDocument(ctx, cache, opts, gqdoc)
	if err != nil {
		return nil, err
	}

	// Traverse the fieldJoins for all of the page configs that have a field with this name.
	// rs := map[string]*scrape.Config{}
	// if fetchFn == nil {
	// 	fetchFn = func(u string) (*fetch.Document, error) {
	// 		u = fetch.TrimURLScheme(u)
	// 		r := gqdocsByURL[u]
	// 		if r == nil {
	// 			return nil, fmt.Errorf("didn't find %q", u)
	// 		}
	// 		return r, nil
	// 	}
	// }

	// slog.Debug("in ConfigurationsForDetailPages()", "mergedCConfigBase", mergedCConfigBase)

	domain := ""
	if opts.OnlyKnownDomainDetailPages {
		uBase, err := tld.Parse(opts.URL)
		if err != nil {
			return nil, fmt.Errorf("error parsing input url %q: %v", opts.URL, err)
		}
		domain = uBase.Domain
	}

	configsByID := map[string]*scrape.Config{}
	configIDs := []string{}
	for _, c := range cs {
		configsByID[c.ID.String()] = c
		configIDs = append(configIDs, c.ID.String())
	}
	// We look at each config produced and discard it if another config already
	// produced the same output. So the order we see the configs matters. We want
	// to do a breadth-first search, and prefer looking at shorter config paths,
	// so we sort them now.
	sort.Slice(configIDs, func(i, j int) bool {
		return len(configIDs[i]) < len(configIDs[j]) ||
			(len(configIDs[i]) == len(configIDs[j]) &&
				configIDs[i] < configIDs[j])
	})

	for _, id := range configIDs {
		c := configsByID[id]
		slog.Debug("looking at", "c.ID", c.ID)
		// fmt.Println("config", c.ID)

		// fmt.Println("skipping storing updated", "c.ID", c.ID)
		// recsStr := c.Records.String()
		// // prevConfig, found := rs[recsStr]
		// // fmt.Println("in generate.ConfigurationsForDetailPages()", "found", found, "len(recsStr)", len(recsStr), "c.ID", c.ID)
		// // if found {
		// // 	slog.Info("candidate detail page configuration failed to produce different records, excluding", "prevID", prevConfig.ID, "opts.configID", opts.configID)
		// // 	continue
		// // }
		// rs[recsStr] = c
		// rs[c.ID] = c

		subScraper := c.Scrapers[0]
		// Separate these out because this may be the whole selector, not just a
		// prefix (in which case it doesn't have the trailing arrow).
		subScraper.Selector = strings.TrimPrefix(subScraper.Selector, "body > htmls")
		subScraper.Selector = strings.TrimPrefix(subScraper.Selector, " > ")

		for _, pj := range pjs {
			slog.Debug("looking at", "pj.config.ID", pj.config.ID)
			// fmt.Println("    merging with", pj.config.ID)

			mergedC := pj.config.Copy()
			mergedC.ID.Field = opts.configID.Field
			mergedC.ID.SubID = c.ID.SubID
			mergedC.Scrapers = append(mergedC.Scrapers, subScraper)
			// fmt.Println("    merged as", mergedC.ID)
			if err := scrape.DetailPages(ctx, cache, mergedC, &subScraper, mergedC.Records, domain); err != nil {
				// fmt.Printf("skipping generating configuration for detail pages for merged config %q: %v\n", mergedC.ID, err)
				slog.Info("skipping generating configuration for detail pages for merged config with error", "mergedC.ID", mergedC.ID, "err", err)
				continue
			}

			// Use MinRecords if specified, otherwise default to 2 for detail pages
			minRecords := 2
			if opts.MinRecords > 0 {
				minRecords = opts.MinRecords
			}
			if DoPruning && len(mergedC.Records) < minRecords {
				slog.Info("candidate detail page configuration produced too few records, pruning",
					"opts.configID", opts.configID,
					"records_produced", len(mergedC.Records),
					"min_required", minRecords)
				continue
			}

			recsStr := mergedC.Records.String()
			if !DoPruning {
				recsStr = mergedC.ID.String() + "\n" + recsStr
			}

			prevConfig, found := rs[recsStr]
			if DoPruning && found {
				slog.Info("candidate detail page configuration failed to produce different records, pruning", "prevID", prevConfig.ID, "opts.configID", opts.configID)
				continue
			}
			rs[recsStr] = mergedC
		}
	}

	slog.Info("in ConfigurationsForAllDetailPages()", "len(rs)", len(rs))
	return rs, nil
}

// joinPageJoinsGQDocuments fetches and concatenates multiple detail pages into a single HTML
// document for pattern analysis.
func joinPageJoinsGQDocuments(cache fetch.Cache, opts ConfigOptions, pjs []*pageJoin) (*fetch.Document, error) {
	// Get all URLs appearing in the values of the fields with this name in the parent pages.
	us := pageJoinsURLs(map[string][]*pageJoin{"": pjs})
	if opts.ConfigOutputDir != "" {
		usPath := filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_urls.txt")
		if err := utils.WriteStringFile(usPath, strings.Join(us, "\n")); err != nil {
			return nil, fmt.Errorf("error writing detail page URLs page to %q: %v", usPath, err)
		}
	}

	// key := "http://" + opts.configID.String() + ".html"
	// r, found, err := fetch.GetGQDocument(cache, key)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to fetch joined page %q: %v", key, err)
	// }
	// str := ""
	// if !found {

	// for _, u := range us {
	// 	fmt.Println("in generate.joinPageJoinsGQDocuments()", "u", u)
	// }

	// Concatenate all of the detail pages pointed to by the field with this name in the parent pages.
	gqdocs, errs := fetch.GetGQDocuments(cache, us)
	if errs != nil {
		for _, err := range errs {
			slog.Error("in generate.joinPageJoinsGQDocuments()", "err", err)
		}
		return nil, fmt.Errorf("failed to join pages")
	}
	// gqdocs := []*fetch.Document{}
	// for _, u := range us {
	// 	gqdoc, found, err := fetch.GetGQDocument(cache, "http://"+u)
	// 	if err != nil {
	// 		err = fmt.Errorf("failed to fetch page to join %q (found: %t): %v", u, found, err)
	// 		slog.Warn("in generate.joinPageJoinsGQDocuments()", "err", err)
	// 		continue
	// 	}
	// 	gqdocs = append(gqdocs, gqdoc)
	// }

	_, r, err := joinGQDocuments(gqdocs) // ./opts, us, gqdocsByURL)
	return r, err
}

// if err != nil {
// 	return nil, err
// }
// }

// if opts.CacheOutputDir != "" {
// 	fetch.SetGQDocument(cache, key, str)
// }

// if opts.CacheOutputDir != "" {
// 	outPath := filepath.Join(opts.CacheOutputDir, opts.configID.String()+".html")
// 	slog.Debug("in joinPageJoinsGQDocuments(), writing to disk cache", "len(str)", len(str), "outPath", outPath)
// 	if str == "" {
// 		if _, err := utils.CopyStringFile(inPath, outPath); err != nil {
// 			return nil, fmt.Errorf("error copying joined detail pages to %q: %v", inPath, err)
// 		}
// 	} else {
// 		if err := utils.WriteStringFile(outPath, str); err != nil {
// 			return nil, fmt.Errorf("error writing joined detail pages to %q: %v", inPath, err)
// 		}
// 	}
// }
// 	return r, nil
// }

// func joinGQDocuments(opts ConfigOptions, us []string, gqdocsByURL map[string]*fetch.Document) (string, *fetch.Document, error) {

// joinGQDocuments concatenates multiple HTML documents into a single document wrapped in an
// <htmls> container element.
func joinGQDocuments(gqdocs []*fetch.Document) (string, *fetch.Document, error) {
	rs := strings.Builder{}
	rs.WriteString("<htmls>\n")

	var gqdoc *fetch.Document
	var err error
	for _, gqdoc := range gqdocs {
		if gqdoc == nil {
			slog.Warn("in generate.joinGQDocuments(), skipping empty gqdoc")
			continue
		}
		str, err := goquery.OuterHtml(gqdoc.Document.Children())
		if err != nil {
			return "", nil, err
		}

		rs.WriteString("\n")
		rs.WriteString(str)
		rs.WriteString("\n")
	}
	rs.WriteString("\n</htmls>\n")

	r := rs.String()
	gqdoc, err = fetch.NewDocumentFromString(r)
	if err != nil {
		return "", nil, err
	}

	return r, gqdoc, nil
}
