package generate

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/findyourpaths/goskyr/date"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/gosimple/slug"
	"golang.org/x/net/html"
)

func findSharedRootSelector(locPropsSel []*locationProps) path {
	for i := 0; ; i++ {
		// slog.Debug("i: %d", i)
		var n node
		for j, e := range locPropsSel {
			// slog.Debug("  j: %d", j)
			// slog.Debug("  len(e.path): %d", len(e.path))
			// slog.Debug("  e.path[:i].string(): %#v", e.path[:i].string())
			// slog.Debug("  n: %#v", n)
			if i >= len(e.path) {
				return e.path[:i]
			}
			// slog.Debug("  e.path[i]: %#v", e.path[i])
			if j == 0 {
				n = e.path[i]
			} else {
				// Look for divergence and if found, return what we have so far.
				if !n.equals(e.path[i]) {
					return e.path[:i]
				}
			}
		}
	}
	return []node{}
}

func shortenRootSelector(p path) path {
	// the following algorithm is a bit arbitrary. Let's
	// see if it works.
	nrTotalClasses := 0
	thresholdTotalClasses := 3
	for i := len(p) - 1; i >= 0; i-- {
		nrTotalClasses += len(p[i].classes)
		if nrTotalClasses >= thresholdTotalClasses {
			return p[i:]
		}
	}
	return p
}

// for now we assume that there will only be one date field
func processFields(locPropsSel []*locationProps, rootSelector path) []scrape.Field {
	zone, _ := time.Now().Zone()
	zone = strings.Replace(zone, "CEST", "CET", 1) // quick hack for issue #209
	dateField := scrape.Field{
		Name:         "date",
		Type:         "date",
		DateLocation: zone,
	}
	fields := []scrape.Field{}

	for _, e := range locPropsSel {
		loc := scrape.ElementLocation{
			Selector:   e.path[len(rootSelector):].string(),
			ChildIndex: e.textIndex,
			Attr:       e.attr,
		}
		fieldType := "text"

		if strings.HasPrefix(e.name, "date-component") {
			cd := date.CoveredDateParts{
				Day:   strings.Contains(e.name, "day"),
				Month: strings.Contains(e.name, "month"),
				Year:  strings.Contains(e.name, "year"),
				Time:  strings.Contains(e.name, "time"),
			}
			format, lang := date.GetDateFormatMulti(e.examples, cd)
			dateField.Components = append(dateField.Components, scrape.DateComponent{
				ElementLocation: loc,
				Covers:          cd,
				Layout:          []string{format},
			})
			if dateField.DateLanguage == "" {
				// first lang wins
				dateField.DateLanguage = lang
			}
			continue
		}

		if loc.Attr == "href" || loc.Attr == "src" {
			fieldType = "url"
		}
		d := scrape.Field{
			Name:             e.name,
			Type:             fieldType,
			ElementLocations: []scrape.ElementLocation{loc},
			CanBeEmpty:       true,
		}
		fields = append(fields, d)
	}

	if len(dateField.Components) > 0 {
		fields = append(fields, dateField)
	}
	return fields
}

// squashLocationManager merges different locationProps into one
// based on their similarity. The tricky question is 'when are two
// locationProps close enough to be merged into one?'
func squashLocationManager(l locationManager, minOcc int) locationManager {
	squashed := locationManager{}
	for i := len(l) - 1; i >= 0; i-- {
		lp := l[i]
		updated := false
		for _, sp := range squashed {
			updated = checkAndUpdateLocProps(sp, lp)
			if updated {
				break
			}
		}
		if !updated {
			stripNthChild(lp, minOcc)
			squashed = append(squashed, lp)
		}
	}
	return squashed
}

// stripNthChild tries to find the index in a locationProps path under which
// we need to strip the nth-child pseudo class. We need to strip that pseudo
// class because at a later point we want to find a common base path between
// different paths but if all paths' base paths look differently (because their
// nodes have different nth-child pseudo classes) there won't be a common
// base path.
func stripNthChild(lp *locationProps, minOcc int) {
	iStrip := 0
	// every node in lp.path with index < than iStrip needs no be stripped
	// of its pseudo classes. iStrip changes during the execution of
	// this function.
	// A bit arbitrary (and probably not always correct) but
	// for now we assume that iStrip cannot be len(lp.path)-1
	// not correct for https://huxleysneuewelt.com/shows
	// but needed for http://www.bar-laparenthese.ch/
	// Therefore by default we substract 1 but in a certain case
	// we substract 2
	sub := 1
	// when minOcc is too small we'd risk stripping the wrong nth-child pseudo classes
	if minOcc < 6 {
		sub = 2
	}
	for i := len(lp.path) - sub; i >= 0; i-- {
		if i < iStrip {
			lp.path[i].pseudoClasses = []string{}
		} else if len(lp.path[i].pseudoClasses) > 0 {
			// nth-child(x)
			ncIndex, _ := strconv.Atoi(strings.Replace(strings.Split(lp.path[i].pseudoClasses[0], "(")[1], ")", "", 1))
			if ncIndex >= minOcc {
				lp.path[i].pseudoClasses = []string{}
				iStrip = i
				// we need to pass iStrip to the locationProps too to be used by checkAndUpdateLocProps
				lp.iStrip = iStrip
			}
		}
	}
}

func checkAndUpdateLocProps(old, new *locationProps) bool {
	// slog.Debug("checkAndUpdateLocProps()", "old.path", old.path, "new.path", new.path)
	// Returns true if the paths overlap and the rest of the
	// element location is identical. If true is returned,
	// the Selector of old will be updated if necessary.

	if old.textIndex != new.textIndex {
		// slog.Debug("in checkAndUpdateLocProps, old.textIndex != new.textIndex, returning false", "old.textIndex", old.textIndex, "new.textIndex", new.textIndex)
		return false
	}
	if old.attr != new.attr {
		// slog.Debug("in checkAndUpdateLocProps, old.attr != new.attr, returning false")
		return false
	}
	if len(old.path) != len(new.path) {
		// slog.Debug("in checkAndUpdateLocProps, len(old.path) != len(new.path), returning false", "len(old.path)", len(old.path), "len(new.path)", len(new.path))
		return false
	}

	newPath := make(path, 0, len(old.path)) // Pre-allocate with capacity
	for i, on := range old.path {
		if on.tagName != new.path[i].tagName {
			// slog.Debug("in checkAndUpdateLocProps, on.tagName != new.path[i].tagName, returning false")
			return false
		}

		pseudoClassesTmp := []string{}
		if i > old.iStrip {
			pseudoClassesTmp = new.path[i].pseudoClasses
		}

		// The following checks are not complete yet but suffice for now
		// with nth-child being our only pseudo class.
		if len(on.pseudoClasses) != len(pseudoClassesTmp) {
			return false // Mismatched pseudo-classes, no overlap
		}
		if len(on.pseudoClasses) == 1 && on.pseudoClasses[0] != pseudoClassesTmp[0] {
			return false // Mismatched pseudo-class values, no overlap
		}

		newNode := node{
			tagName:       on.tagName,
			pseudoClasses: on.pseudoClasses,
		}

		if len(on.classes) == 0 && len(new.path[i].classes) == 0 {
			newPath = append(newPath, newNode)
			continue
		}

		ovClasses := utils.IntersectionSlices(on.classes, new.path[i].classes)
		// If nodes have more than 0 classes, there has to be at least 1 overlapping class.
		if len(ovClasses) > 0 {
			newNode.classes = ovClasses
			newPath = append(newPath, newNode)
		} else {
			return false // No overlapping classes, no overlap
		}
	}

	// slog.Debug("in checkAndUpdateLocProps, incrementing")
	// If we get until here, there is an overlapping path.
	old.path = newPath
	old.count++
	old.examples = append(old.examples, new.examples...)
	return true
}

// remove if count is smaller than minCount
func filterBelowMinCount(lps []*locationProps, minCount int) []*locationProps {
	var kept []*locationProps
	for _, lp := range lps {
		if lp.count < minCount {
			slog.Debug("in filterBelowMinCount dropping", "minCount", minCount, "lp.count", lp.count, "lp.path.string()", lp.path.string())
			continue
		}
		kept = append(kept, lp)
	}
	return kept
}

// remove if the examples are all the same (if onlyVarying is true)
func filterStaticFields(lps []*locationProps) locationManager {
	var kept []*locationProps
	for _, lp := range lps {
		varied := false
		for _, ex := range lp.examples {
			if ex != lp.examples[0] {
				varied = true
				break
			}
		}
		if varied {
			kept = append(kept, lp)
		}
	}
	return kept
}

// Go one element beyond the root selector length and find the cluster with the largest number of fields.
// Filter out all of the other fields.
func findClusters(lps []*locationProps, rootSelector path) map[string][]*locationProps {
	// slog.Debug("filterAllButLargestCluster(lps (%d), rootSelector.string(): %q)", len(lps), rootSelector.string())
	locationPropsByPath := map[string][]*locationProps{}
	// clusterCounts := map[path]int{}
	newLen := len(rootSelector) + 1
	// maxCount := 0
	// var maxPath path
	for _, lp := range lps {
		slog.Debug("in filterAllButLargestCluster(), looking at lp", "lp.count", lp.count, "lp.path.string()", lp.path.string())
		// check whether we reached the end.
		if newLen > len(lp.path) {
			return locationPropsByPath
		}
		p := lp.path[0:newLen]
		pStr := p.string()
		locationPropsByPath[pStr] = append(locationPropsByPath[pStr], lp)
		//
		// clusterCounts[p] += lp.count
		// if clusterCounts[pStr] > maxCount {
		// 	maxCount = clusterCounts[pStr]
		// 	maxPath = p
		// }
	}
	return locationPropsByPath
}

type ConfigOptions struct {
	Batch           bool
	CacheInputDir   string
	CacheOutputDir  string
	ConfigOutputDir string
	DoSubpages      bool
	MinOccs         []int
	ModelName       string
	Offline         bool
	OnlyVarying     bool
	RenderJS        bool
	URL             string
	WordsDir        string
	configID        scrape.ConfigID
	configPrefix    string
}

func InitOpts(opts ConfigOptions) (ConfigOptions, error) {
	if len(opts.URL) == 0 {
		return opts, errors.New("URL cannot be empty")
	}

	u, err := url.Parse(opts.URL)
	if err != nil {
		return opts, fmt.Errorf("error parsing input URL %q: %v", opts.URL, err)
	}
	opts.configID.Slug = slug.Make(u.Host)
	prefix := fetch.MakeURLStringSlug(u.String())

	if opts.CacheInputDir != "" {
		opts.CacheInputDir = filepath.Join(opts.CacheInputDir, prefix+"_cache")
	}

	if opts.CacheOutputDir != "" {
		opts.CacheOutputDir = filepath.Join(opts.CacheOutputDir, prefix+"_cache")
	}

	if opts.ConfigOutputDir != "" {
		opts.ConfigOutputDir = filepath.Join(opts.ConfigOutputDir, prefix+"_configs")
	}

	return opts, nil
}

func ConfigurationsForPage(opts ConfigOptions, gqdocsByURL map[string]*goquery.Document) (map[string]*scrape.Config, map[string]*goquery.Document, error) {
	if output.WriteSeparateLogFiles {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_ConfigurationsForPage_log.txt"))
		if err != nil {
			return nil, nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Debug("ConfigurationsForPage()", "opts", opts)
	defer slog.Debug("ConfigurationsForPage() returning")

	var gqdoc *goquery.Document
	var err error
	gqdoc, gqdocsByURL, err = fetchGQDocument(opts, fetch.TrimURLScheme(opts.URL), gqdocsByURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch page: %v", err)
	}

	return ConfigurationsForPageWithMinOccurrences(opts, gqdoc, gqdocsByURL)
}

func ConfigurationsForPageWithMinOccurrences(opts ConfigOptions, gqdoc *goquery.Document, gqdocsByURL map[string]*goquery.Document) (map[string]*scrape.Config, map[string]*goquery.Document, error) {
	var cims map[string]*scrape.Config
	var err error
	rs := map[string]*scrape.Config{}
	// Generate configs for each of the minimum occs.
	for _, minOcc := range opts.MinOccs {
		slog.Debug("calling ConfigurationsForGQDocument()", "minOcc", minOcc)
		cims, gqdocsByURL, err = ConfigurationsForGQDocument(opts, gqdoc, minOcc, gqdocsByURL)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range cims {
			rs[k] = v
		}
	}

	slog.Debug("in ConfigurationsForPage()", "len(rs)", len(rs))
	return rs, gqdocsByURL, nil
}

func ConfigurationsForGQDocument(opts ConfigOptions, gqdoc *goquery.Document, minOcc int, gqdocsByURL map[string]*goquery.Document) (map[string]*scrape.Config, map[string]*goquery.Document, error) {
	if output.WriteSeparateLogFiles {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_ConfigurationsForGQDocument_log.txt"))
		if err != nil {
			return nil, nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Debug("ConfigurationsForGQDocument()")
	defer slog.Debug("ConfigurationsForGQDocument() returning")

	htmlStr, err := goquery.OuterHtml(gqdoc.Children())
	if err != nil {
		return nil, nil, err
	}

	a := &Analyzer{
		Tokenizer:   html.NewTokenizer(strings.NewReader(htmlStr)),
		NumChildren: map[string]int{},
		ChildNodes:  map[string][]node{},
		FindNext:    opts.configID.Field == "" && opts.configID.SubID == "",
	}

	slog.Debug("in ConfigurationsForGQDocument(): parsing")
	a.Parse()

	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range a.LocMan {
			slog.Debug("raw", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
		}
		for i, lp := range a.PagMan {
			slog.Debug("raw pags", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
		}
	}

	a.LocMan = squashLocationManager(a.LocMan, minOcc)
	a.PagMan = squashLocationManager(a.PagMan, 3)
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range a.LocMan {
			slog.Debug("squashed", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
		}
		for i, lp := range a.PagMan {
			slog.Debug("squashed pags", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
		}
	}

	a.LocMan = filterBelowMinCount(a.LocMan, minOcc)
	a.PagMan = filterBelowMinCount(a.PagMan, 3)
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range a.LocMan {
			slog.Debug("filtered min count", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
		}
		for i, lp := range a.PagMan {
			slog.Debug("filtered min count pags", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
		}
	}

	if opts.OnlyVarying {
		a.LocMan = filterStaticFields(a.LocMan)
		a.PagMan = filterStaticFields(a.PagMan)
	}
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range a.LocMan {
			slog.Debug("filtered static", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
		}
		for i, lp := range a.PagMan {
			slog.Debug("filtered static pags", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
		}
	}

	slog.Debug("in ConfigurationsForGQDocument, final", "len(a.LocMan)", len(a.LocMan))
	slog.Debug("in ConfigurationsForGQDocument, final", "len(a.PagMan)", len(a.PagMan))

	if len(a.LocMan) == 0 {
		slog.Warn("no fields found", "opts", opts, "minOcc", minOcc)
		return nil, gqdocsByURL, nil
	}
	if err := a.LocMan.setFieldNames(opts.ModelName, opts.WordsDir); err != nil {
		return nil, nil, err
	}

	slog.Debug("in ConfigurationsForGQDocument", "opts", opts)
	var locPropsSel []*locationProps
	if !opts.Batch {
		a.LocMan.setColors()
		a.LocMan.selectFieldsTable()
		for _, lm := range a.LocMan {
			if lm.selected {
				locPropsSel = append(locPropsSel, lm)
			}
		}
	} else {
		locPropsSel = a.LocMan
	}
	if len(locPropsSel) == 0 {
		return nil, nil, fmt.Errorf("no fields selected")
	}

	slog.Debug("in ConfigurationsForGQDocument, before expanding", "len(a.LocMan)", len(a.LocMan))
	slog.Debug("in ConfigurationsForGQDocument, before expanding", "len(locPropsSel)", len(locPropsSel))

	minOccStr := fmt.Sprintf("%02da", minOcc)
	if opts.configID.Field != "" {
		opts.configID.SubID = minOccStr
	} else {
		opts.configID.ID = minOccStr
	}
	rs := map[string]*scrape.Config{}
	var pagProps []*locationProps

	// FIXME
	// if opts.DoSubpages {
	// 	pagProps = append(a.NextPaths, a.PagMan...)
	// }

	if err := expandAllPossibleConfigs(gqdoc, opts, locPropsSel, nil, "", pagProps, rs); err != nil {
		return nil, nil, err
	}

	slog.Debug("in ConfigurationsForGQDocument()", "len(rs)", len(rs))
	return rs, gqdocsByURL, nil
}

func expandAllPossibleConfigs(gqdoc *goquery.Document, opts ConfigOptions, locPropsSel []*locationProps, parentRootSelector path, parentItemsStr string, pagProps []*locationProps, results map[string]*scrape.Config) error {
	// if output.WriteSeparateLogFiles {
	// 	prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_expandAllPossibleConfigs_log.txt"))
	// 	if err != nil {
	// 		return err
	// 	}
	// 	defer output.RestoreDefaultLogger(prevLogger)
	// }
	// slog.Debug("expandAllPossibleConfigs()")
	// defer slog.Debug("expandAllPossibleConfigs() returning")

	slog.Debug("in expandAllPossibleConfigs()", "opts.configID", opts.configID.String())

	// fmt.Printf("generating Config %#v", opts.configID)
	// slog.Debug("in expandAllPossibleConfigs()", "opts.configID", opts.configID)
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range locPropsSel {
			slog.Debug("in expandAllPossibleConfigs()", "i", i, "lp.count", lp.count)
		}
	}

	pags := []scrape.Paginator{}
	for _, lp := range pagProps {
		pags = append(pags, scrape.Paginator{
			Location: scrape.ElementLocation{
				Selector: lp.path.string(),
			}})
	}
	sort.Slice(pags, func(i, j int) bool {
		return pags[i].Location.Selector < pags[j].Location.Selector
	})

	slog.Debug("in expandAllPossibleConfigs()", "pags", pags)

	s := scrape.Scraper{
		Name:       opts.configID.String(),
		RenderJs:   opts.RenderJS,
		URL:        opts.URL,
		Paginators: pags,
	}

	rootSelector := findSharedRootSelector(locPropsSel)
	s.Item = rootSelector.string()
	s.Fields = processFields(locPropsSel, rootSelector)
	if opts.DoSubpages && len(s.GetSubpageURLFields()) == 0 {
		slog.Warn("a subpage URL field is required but none were found, ending early", "opts.configID", opts.configID, "opts", opts)
		return nil
	}

	items, err := scrape.GQDocument(&s, gqdoc, true)
	if err != nil {
		return err
	}
	itemsStr := items.String()
	if scrape.DoPruning && itemsStr == parentItemsStr {
		slog.Debug("generate produced same items as its parent, ending early", "opts.configID", opts.configID)
		return nil
	}

	if slog.Default().Enabled(nil, slog.LevelDebug) {
		slog.Debug("in expandAllPossibleConfigs()", "len(items)", len(items), "items.TotalFields()", items.TotalFields())
	}

	results[opts.configID.String()] = &scrape.Config{
		ID:       opts.configID,
		Scrapers: []scrape.Scraper{s},
		ItemMaps: items,
	}

	clusters := findClusters(locPropsSel, rootSelector)
	clusterIDs := []string{}
	for clusterID := range clusters {
		clusterIDs = append(clusterIDs, clusterID)
	}
	sort.Strings(clusterIDs)

	lastID := 'a'
	for _, clusterID := range clusterIDs {
		nextOpts := opts
		if opts.configID.Field != "" {
			nextOpts.configID.SubID += string(lastID)
		} else {
			nextOpts.configID.ID += string(lastID)
		}
		if err := expandAllPossibleConfigs(gqdoc, nextOpts, clusters[clusterID], rootSelector, itemsStr, pagProps, results); err != nil {
			return err
		}
		lastID++
	}

	return nil
}

func ExtendPageConfigsWithNexts(opts ConfigOptions, pageConfigs map[string]*scrape.Config, gqdocsByURL map[string]*goquery.Document) error {
	pageCIDs := []string{}
	for _, pageC := range pageConfigs {
		pageCIDs = append(pageCIDs, pageC.ID.String())
	}

	var gqdoc *goquery.Document
	var err error
	gqdoc, gqdocsByURL, err = fetchGQDocument(opts, fetch.TrimURLScheme(opts.URL), gqdocsByURL)
	if err != nil {
		return fmt.Errorf("failed to fetch page: %v", err)
	}

	// path := filepath.Join(opts.InputDir, fetch.MakeURLStringSlug(opts.URL)+".html")
	// f := &fetch.FileFetcher{}
	// gqdoc, err := fetch.GQDocument(f, "file://"+path, nil)
	// // fmt.Printf("adding subURL: %q\n", subURL)
	// if err != nil {
	// 	return nil, fmt.Errorf("error fetching page at: %v", err)
	// }

	for _, id := range pageCIDs {
		if err := extendPageConfigItemsWithNext(opts, pageConfigs[id], gqdoc.Selection); err != nil {
			return fmt.Errorf("error extending page config items with next page items: %v", err)
		}
	}
	return nil
}

func extendPageConfigItemsWithNext(opts ConfigOptions, pageC *scrape.Config, sel *goquery.Selection) error {
	// fmt.Printf("looking at %q\n", pageC.ID.String())
	// fmt.Printf("looking at opts url %q\n", fetch.TrimURLScheme(opts.URL))

	// Collect all of the proposed next urls from all the scraper's paginators.
	pageS := pageC.Scrapers[0]
	uStrsMap := map[string]scrape.Paginator{}
	for _, pag := range pageS.Paginators {
		// hash := crc32.ChecksumIEEE([]byte(pag.Location.Selector))
		// fmt.Printf("using pag with hash: %#v\n", hash)
		u, err := scrape.GetURL(&pag.Location, sel, opts.URL)
		if err != nil {
			fmt.Printf("ERROR: failed to get next page url: %v\n", err)
			continue
		}
		// fmt.Printf("found next page url: %q\n", u)

		uStr := u.String()
		if strings.HasPrefix(uStr, "javascript:") {
			continue
		}
		uStr = fetch.TrimURLScheme(uStr)
		shortURL := fetch.TrimURLScheme(opts.URL)
		// fmt.Printf("looking at next url %q\n", uStr)
		if uStr == shortURL ||
			"www."+uStr == shortURL ||
			uStr == "www."+shortURL {
			continue
		}
		uStrsMap[uStr] = pag
	}

	// Download all of the proposed next pages at the urls.
	uStrs := []string{}
	for uStr := range uStrsMap {
		uStrs = append(uStrs, fetch.TrimURLScheme(uStr))
	}

	// FIXME
	gqdocsByURL := map[string]*goquery.Document{}
	// gqdocsByURL, err := fetchGQDocumentsByURL(uStrs, opts.CacheInputDir, opts.ConfigOutputDir)
	// if err != nil {
	// 	return fmt.Errorf("failed to fetch next pages: %v", err)
	// }

	// Scrape items for the proposed next pages.
	// f := &fetch.FileFetcher{}
	newPags := []scrape.Paginator{}
	for uStr, pag := range uStrsMap {
		nextGQDoc := gqdocsByURL[uStr]
		// , err := goquery.NewDocumentFromReader(strings.NewReader(nextStr))
		// if err != nil {
		// 	return err
		// }

		// // fmt.Printf("extended %q with items from page %q\n", pageC.ID.String(), uStr)
		// path := filepath.Join(opts.CacheInputDir, fetch.MakeURLStringSlug(uStr)+".html")
		// nextGQDoc, err := fetch.GQDocument(f, "file://"+path, nil)
		// // fmt.Printf("adding subURL: %q\n", subURL)
		// if err != nil {
		// 	fmt.Printf("ERROR: error fetching subpage at %q: %v\n", path, err)
		// 	continue
		// }

		// fmt.Printf("read next page: %q\n", u)

		items, err := scrape.GQDocument(&pageS, nextGQDoc, true)
		if err != nil {
			return err
		}
		// fmt.Printf("found %d items\n", len(items))

		if len(items) == 0 {
			continue
		}

		pageC.ItemMaps = append(pageC.ItemMaps, items...)
		newPags = append(newPags, pag)
		// fmt.Printf("extended %q to %d items\n", pageC.ID.String(), len(pageC.ItemMaps))

		// rel, err := url.Parse(fj.value)
		// if err != nil {
		// 	slog.Error("error parsing subpage url", "err", err)
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

func pageJoinsURLs(pageJoinsMap map[string][]*pageJoin) []string {
	us := map[string]bool{}
	for _, pjs := range pageJoinsMap {
		for _, pj := range pjs {
			for _, fj := range pj.fieldJoins {
				us[fj.url] = true
			}
		}
	}
	rs := []string{}
	for u := range us {
		rs = append(rs, u)
	}
	sort.Strings(rs)
	return rs
}

func ConfigurationsForAllSubpages(opts ConfigOptions, pageConfigs map[string]*scrape.Config, gqdocsByURL map[string]*goquery.Document) (map[string]*scrape.Config, map[string]*goquery.Document, error) {
	if output.WriteSeparateLogFiles {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_ConfigurationsForAllSubpages_log.txt"))
		if err != nil {
			return nil, nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Debug("ConfigurationsForAllSubpages()")
	defer slog.Debug("ConfigurationsForAllSubpages() returning")

	slog.Debug("in ConfigurationsForAllSubpages()", "opts.URL", opts.URL)
	slog.Debug("in ConfigurationsForAllSubPages()", "opts.ConfigOutputDir", opts.ConfigOutputDir)
	slog.Debug("in ConfigurationsForAllSubpages()", "opts", opts)

	pageCIDs := []string{}
	for _, pageC := range pageConfigs {
		pageCIDs = append(pageCIDs, pageC.ID.String())
	}

	uBase, err := url.Parse(opts.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("error parsing input url %q: %v", opts.URL, err)
	}

	pageJoinsByFieldName := map[string][]*pageJoin{}
	for _, pageC := range pageConfigs {
		pageCIDs = append(pageCIDs, pageC.ID.String())
		pageS := pageC.Scrapers[0]
		// fmt.Printf("found %d subpage URL fields\n", len(s.GetSubpageURLFields()))
		for _, pageF := range pageS.GetSubpageURLFields() {
			pj := &pageJoin{config: pageC}
			pageJoinsByFieldName[pageF.Name] = append(pageJoinsByFieldName[pageF.Name], pj)
			for _, pageIM := range pageC.ItemMaps {
				fj := &fieldJoin{
					// pageConfig: pageC
					// pageItemMap: pageIM
					name:  pageF.Name,
					value: fmt.Sprintf("%v", pageIM[pageF.Name]),
				}

				if scrape.SkipSubURLExt[filepath.Ext(fj.value)] {
					slog.Debug("skipping sub URL due to extension", "fj.value", fj.value)
					continue
				}

				rel, err := url.Parse(fj.value)
				if err != nil {
					slog.Error("error parsing subpage url", "err", err)
					continue
				}

				u := uBase.ResolveReference(rel)
				if u.Scheme == "mailto" {
					slog.Debug("skipping sub URL due to scheme", "u", u)
					continue
				}

				fj.url = fetch.TrimURLScheme(u.String())
				pj.fieldJoins = append(pj.fieldJoins, fj)
			}
		}
	}
	sort.Strings(pageCIDs)

	subURLs := pageJoinsURLs(pageJoinsByFieldName)
	if opts.ConfigOutputDir != "" {
		urlsPath := filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_urls.txt")
		if err := utils.WriteStringFile(urlsPath, strings.Join(subURLs, "\n")); err != nil {
			return nil, nil, fmt.Errorf("failed to write subpage URLs list: %v", err)
		}
	}
	slog.Debug("in ConfigurationsForAllSubpages()", "opts.CacheInputDir", opts.CacheInputDir)

	var cs map[string]*scrape.Config
	rs := map[string]*scrape.Config{}
	for fname, pjs := range pageJoinsByFieldName {
		opts.configID.Field = fname
		cs, gqdocsByURL, err = ConfigurationsForSubpages(opts, pjs, gqdocsByURL)
		if err != nil {
			return nil, nil, fmt.Errorf("error generating configuration for subpages for field %q: %v", fname, err)
		}
		for id, c := range cs {
			rs[id] = c
		}
	}

	slog.Debug("in ConfigurationsForAllSubpages()", "len(rs)", len(rs))
	return rs, gqdocsByURL, nil
}

// ConfigurationsForSubpages collects the URL values for a candidate subpage field, retrieves the pages at those URLs, concatenates them, trains a scraper to extract from those subpages, and merges the resulting ItemMap into the parent page, outputting the result.
func ConfigurationsForSubpages(opts ConfigOptions, pjs []*pageJoin, gqdocsByURL map[string]*goquery.Document) (map[string]*scrape.Config, map[string]*goquery.Document, error) {
	if output.WriteSeparateLogFiles {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_ConfigurationsForSubpages_log.txt"))
		if err != nil {
			return nil, nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Debug("ConfigurationsForSubpages()", "opts", opts)
	defer slog.Debug("ConfigurationsForSubpages() returning")

	gqdoc, err := joinPageJoinsGQDocuments(opts, pjs, gqdocsByURL)
	if err != nil {
		return nil, nil, err
	}
	opts.DoSubpages = false
	cs, gqdocsByURL, err := ConfigurationsForPageWithMinOccurrences(opts, gqdoc, gqdocsByURL)
	if err != nil {
		return nil, nil, err
	}

	// Traverse the fieldJoins for all of the page configs that have a field with this name.
	rs := map[string]*scrape.Config{}
	fetchFn := func(u string) (*goquery.Document, error) {
		u = fetch.TrimURLScheme(u)
		r := gqdocsByURL[u]
		if r == nil {
			return nil, fmt.Errorf("didn't find %q", u)
		}
		return r, nil
	}

	// slog.Debug("in ConfigurationsForSubpages()", "mergedCConfigBase", mergedCConfigBase)
	for _, c := range cs {
		slog.Debug("looking at", "c.ID", c.ID)
		rs[c.ID.String()] = c
		subScraper := c.Scrapers[0]
		subScraper.Item = strings.TrimPrefix(subScraper.Item, "body > htmls > ")

		for _, pj := range pjs {
			slog.Debug("looking at", "pj.config.ID.String()", pj.config.ID.String())

			mergedC := pj.config.Copy()
			mergedC.ID.Field = opts.configID.Field
			mergedC.ID.SubID = c.ID.SubID
			mergedC.Scrapers = append(mergedC.Scrapers, subScraper)

			if err := scrape.Subpages(mergedC, &subScraper, mergedC.ItemMaps, fetchFn); err != nil {
				// fmt.Printf("skipping generating configuration for subpages for merged config %q: %v\n", mergedC.ID.String(), err)
				slog.Warn("skipping generating configuration for subpages for merged config", "mergedC.ID", mergedC.ID.String(), "err", err)
				continue
			}
			rs[mergedC.ID.String()] = mergedC
		}
	}

	slog.Debug("in ConfigurationsForAllSubpages()", "len(rs)", len(rs))
	return rs, gqdocsByURL, nil
}

func fetchGQDocument(opts ConfigOptions, u string, gqdocsByURL map[string]*goquery.Document) (*goquery.Document, map[string]*goquery.Document, error) {
	// if output.WriteSeparateLogFiles {
	// 	prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_fetchGQDocument_log.txt"))
	// 	if err != nil {
	// 		return nil, nil, err
	// 	}
	// 	defer output.RestoreDefaultLogger(prevLogger)
	// }
	slog.Debug("fetchGQDocument()", "u", u)
	slog.Debug("fetchGQDocument()", "len(gqdocsByURL)", len(gqdocsByURL))
	defer slog.Debug("fetchGQDocument() returning")

	if gqdocsByURL == nil {
		gqdocsByURL = map[string]*goquery.Document{}
	}

	// Check if we have it in memory.
	gqdoc, found := gqdocsByURL[u]
	str := ""
	var err error

	if found {
		slog.Debug("fetchGQDocument(), memory cache hit")
	} else {
		// Not in memory, so check if it's in our cache on disk.
		cacheInPath := filepath.Join(opts.CacheInputDir, fetch.MakeURLStringSlug(u)+".html")
		str, err = utils.ReadStringFile(cacheInPath)
		if err == nil {
			slog.Debug("fetchGQDocument(), disk cache hit", "len(str)", len(str))
		} else {
			if opts.Offline {
				return nil, nil, fmt.Errorf("running offline and unable to retrieve %q", u)
			}
			var fetcher fetch.Fetcher
			if opts.RenderJS {
				fetcher = fetch.NewDynamicFetcher("", 0)
			} else {
				fetcher = &fetch.StaticFetcher{}
			}

			str, err = fetcher.Fetch("http://"+u, nil)
			if err != nil {
				return nil, nil, fmt.Errorf("error fetching GQDocument: %v", err)
			}
			slog.Debug("fetchGQDocument(), retrieved html", "len(str)", len(str))
		}

		// If on disk, use the cached html string. Otherwise, use the retrieved html.
		//
		// Original goskyr comment:
		// A bit hacky. But goquery seems to manipulate the html (I only know of goquery adding tbody tags if missing)
		// so we rely on goquery to read the html for both scraping AND figuring out the scraping config.
		gqdoc, err = goquery.NewDocumentFromReader(strings.NewReader(str))
		if err != nil {
			return nil, nil, err
		}

		gqdocsByURL[u] = gqdoc
	}

	slog.Debug("fetchGQDocument()", "len(str)", len(str))

	// Now write to the new cache if there is one and the page isn't already there.
	if opts.CacheOutputDir != "" {
		cacheOutPath := filepath.Join(opts.CacheOutputDir, fetch.MakeURLStringSlug(u)+".html")
		_, err = os.Stat(cacheOutPath)
		if err == nil {
			slog.Debug("fetchGQDocument(), already written to disk cache", "cacheOutPath", cacheOutPath)
		} else {
			if str == "" {
				// Now we have to translate the goquery doc back into a string
				str, err = goquery.OuterHtml(gqdoc.Children())
				if err != nil {
					return nil, nil, err
				}
			}

			slog.Debug("in fetchGQDocument(), writing to disk cache", "cacheOutPath", cacheOutPath)
			if err := utils.WriteStringFile(cacheOutPath, str); err != nil {
				return nil, nil, fmt.Errorf("failed to write html file: %v", err)
			}
		}
	}

	return gqdoc, gqdocsByURL, nil
}

func joinPageJoinsGQDocuments(opts ConfigOptions, pjs []*pageJoin, gqdocsByURL map[string]*goquery.Document) (*goquery.Document, error) {
	// Get all URLs appearing in the values of the fields with this name in the parent pages.
	us := pageJoinsURLs(map[string][]*pageJoin{"": pjs})
	if opts.ConfigOutputDir != "" {
		usPath := filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_urls.txt")
		if err := utils.WriteStringFile(usPath, strings.Join(us, "\n")); err != nil {
			return nil, fmt.Errorf("error writing subpage URLs page to %q: %v", usPath, err)
		}
	}

	inPath := filepath.Join(opts.CacheInputDir, opts.configID.String()+".html")
	r, found := gqdocsByURL[inPath]
	str := ""
	var err error
	if !found {
		// Concatenate all of the subpages pointed to by the field with this name in the parent pages.
		str, r, err = joinGQDocuments(opts, us, gqdocsByURL)
		if err != nil {
			return nil, err
		}
	}

	if opts.CacheOutputDir != "" {
		outPath := filepath.Join(opts.CacheOutputDir, opts.configID.String()+".html")
		slog.Debug("in joinPageJoinsGQDocuments(), writing to disk cache", "len(str)", len(str), "outPath", outPath)
		if str == "" {
			if _, err := utils.CopyStringFile(inPath, outPath); err != nil {
				return nil, fmt.Errorf("error copying joined subpages to %q: %v", inPath, err)
			}
		} else {
			if err := utils.WriteStringFile(outPath, str); err != nil {
				return nil, fmt.Errorf("error writing joined subpages to %q: %v", inPath, err)
			}
		}
	}
	return r, nil
}

func joinGQDocuments(opts ConfigOptions, us []string, gqdocsByURL map[string]*goquery.Document) (string, *goquery.Document, error) {
	rs := strings.Builder{}
	rs.WriteString("<htmls>\n")

	var gqdoc *goquery.Document
	var err error
	for _, u := range us {
		gqdoc, gqdocsByURL, err = fetchGQDocument(opts, u, gqdocsByURL)
		if err != nil {
			return "", nil, fmt.Errorf("failed to fetch page: %v", err)
		}

		str, err := goquery.OuterHtml(gqdoc.Children())
		if err != nil {
			return "", nil, err
		}

		rs.WriteString("\n")
		rs.WriteString(str)
		rs.WriteString("\n")
	}
	rs.WriteString("\n</htmls>\n")

	r := rs.String()
	gqdoc, err = goquery.NewDocumentFromReader(strings.NewReader(r))
	if err != nil {
		return "", nil, err
	}

	return r, gqdoc, nil
}
