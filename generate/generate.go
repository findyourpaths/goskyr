package generate

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"github.com/agnivade/levenshtein"
	"github.com/findyourpaths/goskyr/date"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/gosimple/slug"
	"golang.org/x/net/html"
)

// A node is our representation of a node in an html tree
type node struct {
	tagName       string
	classes       []string
	pseudoClasses []string
}

func (n node) string() string {
	nodeString := n.tagName
	for _, cl := range n.classes {
		// https://www.itsupportguides.com/knowledge-base/website-tips/css-colon-in-id/
		cl = strings.ReplaceAll(cl, ":", "\\:")
		cl = strings.ReplaceAll(cl, ">", "\\>")
		// https://stackoverflow.com/questions/45293534/css-class-starting-with-number-is-not-getting-applied
		if unicode.IsDigit(rune(cl[0])) {
			cl = fmt.Sprintf(`\3%s `, string(cl[1:]))
		}
		nodeString += fmt.Sprintf(".%s", cl)
	}
	if len(n.pseudoClasses) > 0 {
		nodeString += fmt.Sprintf(":%s", strings.Join(n.pseudoClasses, ":"))
	}
	return nodeString
}

func (n node) equals(n2 node) bool {
	if n.tagName == n2.tagName {
		if utils.SliceEquals(n.classes, n2.classes) {
			if utils.SliceEquals(n.pseudoClasses, n2.pseudoClasses) {
				return true
			}
		}
	}
	return false
}

// A path is a list of nodes starting from the root node and going down
// the html tree to a specific node
type path []node

func (p path) string() string {
	nodeStrings := []string{}
	for _, n := range p {
		nodeStrings = append(nodeStrings, n.string())
	}
	return strings.Join(nodeStrings, " > ")
}

// distance calculates the levenshtein distance between the string represention
// of two paths
func (p path) distance(p2 path) float64 {
	return float64(levenshtein.ComputeDistance(p.string(), p2.string()))
}

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
	Batch         bool
	DoSubpages    bool
	InputURL      string
	MinOccs       []int
	ModelName     string
	OnlyVarying   bool
	OutputDir     string
	InputDir      string
	RenderJS      bool
	InputFile     string
	WordsDir      string
	configID      scrape.ConfigID
	configPrefix  string
	inputDirBase  string
	outputDirBase string
}

func InitOpts(opts ConfigOptions) (ConfigOptions, error) {
	host := ""
	if strings.HasPrefix(opts.InputURL, "http") {
		iu, err := url.Parse(opts.InputURL)
		if err != nil {
			return opts, fmt.Errorf("error parsing input URL %q: %v", opts.InputURL, err)
		}
		host = iu.Host
		opts.outputDirBase = filepath.Join(opts.OutputDir, slug.Make(opts.InputURL))
		opts.inputDirBase = filepath.Join(opts.InputDir, slug.Make(opts.InputURL))
	} else if strings.HasPrefix(opts.InputURL, "file") {
		_, host = filepath.Split(opts.InputURL)
		ext := filepath.Ext(host)
		host = strings.TrimSuffix(host, ext)
		opts.outputDirBase = filepath.Join(opts.OutputDir, slug.Make(host))
		opts.inputDirBase = filepath.Join(opts.InputDir, slug.Make(host))
	}

	opts.configID.Slug = slug.Make(host)

	return opts, nil
}

func setDefaultLogger(logPath string) (*slog.Logger, error) {
	prevLogger := slog.Default()
	if err := os.MkdirAll(filepath.Dir(logPath), 0770); err != nil {
		return nil, fmt.Errorf("error creating parent directories for log output file %q: %v", logPath, err)
	}
	logF, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("error opening log output file %q: %v", logPath, err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(logF, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return prevLogger, nil
}

func ConfigurationsForPage(opts ConfigOptions) (map[string]*scrape.Config, error) {
	slog.Debug("starting to generate config")
	slog.Debug("analyzing", "opts.InputURL", opts.InputURL)

	// opts.configID.Base = filepath.Join(opts.outputDirBase, opts.configID.Slug+"__")
	// opts.configID.OutputDirBase = opts.outputDirBase

	slog.Debug("in ConfigurationsForPage()", "opts.configID", opts.configID)

	cs, htmlStr, err := ConfigurationsForURI(opts)
	if err != nil {
		return nil, err
	}

	if err := utils.WriteStringFile(filepath.Join(opts.outputDirBase, opts.configID.String()+".html"), htmlStr); err != nil {
		return nil, fmt.Errorf("failed to write html file: %v", err)
	}

	for _, c := range cs {
		if err := c.WriteToFile(opts.outputDirBase); err != nil {
			return nil, err
		}
	}
	return cs, nil
}

func ConfigurationsForURI(opts ConfigOptions) (map[string]*scrape.Config, string, error) {
	prevLogger, err := setDefaultLogger(filepath.Join(opts.outputDirBase, opts.configID.String()+"_ConfigurationsForURI_log.txt"))
	if err != nil {
		return nil, "", err
	}
	defer slog.SetDefault(prevLogger)

	slog.Debug("ConfigurationsForURI()", "opts", opts)
	if len(opts.InputURL) == 0 {
		return nil, "", errors.New("InputURL cannot be empty")
	}

	// slog.Debug("strings.HasPrefix(s.URL, \"file://\": %t", strings.HasPrefix(s.URL, "file://"))
	var fetcher fetch.Fetcher
	inputURL := opts.InputURL
	if strings.HasPrefix(inputURL, "file://") {
		inputURL = "file://" + filepath.Join(opts.inputDirBase, strings.TrimPrefix(inputURL, "file://"))
		fetcher = &fetch.FileFetcher{}
	} else if opts.RenderJS {
		fetcher = fetch.NewDynamicFetcher("", 0)
	} else {
		fetcher = &fetch.StaticFetcher{}
	}
	res, err := fetcher.Fetch(inputURL, nil)
	if err != nil {
		return nil, "", err
	}
	if !strings.HasPrefix(opts.InputURL, "file") {
		opts.InputURL = "file://" + slug.Make(opts.InputURL) + ".html"
	}

	// A bit hacky. But goquery seems to manipulate the html (I only know of goquery adding tbody tags if missing)
	// so we rely on goquery to read the html for both scraping AND figuring out the scraping config.
	gqdoc, err := goquery.NewDocumentFromReader(strings.NewReader(res))
	if err != nil {
		return nil, "", err
	}

	// Now we have to translate the goquery doc back into a string
	htmlStr, err := goquery.OuterHtml(gqdoc.Children())
	if err != nil {
		return nil, "", err
	}

	// slog.Debug("writing html to file", "u", opts.InputURL)
	// path = filepath.Join(htmlOutputDir, slug.Make(opts.InputURL)+".html")
	// // path := ""
	// // if strings.HasPrefix(opts.InputURL, "http") {
	// // 	path = filepath.Join(htmlOutputDir, slug.Make(opts.InputURL)+".html")
	// // } else if strings.HasPrefix(opts.InputURL, "file") {
	// // 	_, path = filepath.Split(opts.InputURL)
	// // 	ext := filepath.Ext(path)
	// // 	path = strings.TrimSuffix(path, ext)
	// // }

	// fpath, err := utils.WriteTempStringFile(path, htmlStr)
	// if err != nil {
	// 	return nil, "", fmt.Errorf("failed to write html file: %v", err)
	// }
	// slog.Debug("wrote html to file", "fpath", fpath)

	rs := map[string]*scrape.Config{}
	// Generate configs for each of the minimum occs.
	for _, minOcc := range opts.MinOccs {
		slog.Debug("calling ConfigurationsForGQDocument()", "minOcc", minOcc, "opts.InputURL", opts.InputURL)
		cims, err := ConfigurationsForGQDocument(gqdoc, htmlStr, opts, minOcc)
		if err != nil {
			return nil, "", err
		}
		for k, v := range cims {
			rs[k] = v
		}
	}
	return rs, htmlStr, nil
}

var htmlOutputDir = "/tmp/goskyr/generate/ConfigurationsForGQDocument/"

func ConfigurationsForGQDocument(gqdoc *goquery.Document, htmlStr string, opts ConfigOptions, minOcc int) (map[string]*scrape.Config, error) {
	prevLogger, err := setDefaultLogger(filepath.Join(opts.outputDirBase, opts.configID.String()+"_ConfigurationsForGQDocument_log.txt"))
	if err != nil {
		return nil, err
	}
	defer slog.SetDefault(prevLogger)

	a := &Analyzer{
		Tokenizer:   html.NewTokenizer(strings.NewReader(htmlStr)),
		NumChildren: map[string]int{},
		ChildNodes:  map[string][]node{},
	}

	slog.Debug("in ConfigurationsForGQDocument(): parsing")
	a.Parse()
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range a.LocMan {
			slog.Debug("raw", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
		}
	}
	a.LocMan = squashLocationManager(a.LocMan, minOcc)
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range a.LocMan {
			slog.Debug("squashed", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
		}
	}
	a.LocMan = filterBelowMinCount(a.LocMan, minOcc)
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range a.LocMan {
			slog.Debug("filtered min count", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
		}
	}
	if opts.OnlyVarying {
		a.LocMan = filterStaticFields(a.LocMan)
	}
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range a.LocMan {
			slog.Debug("filtered static", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
		}
	}
	slog.Debug("in ConfigurationsForGQDocument, final", "len(a.LocMan)", len(a.LocMan))

	if len(a.LocMan) == 0 {
		slog.Warn("no fields found", "opts", opts, "minOcc", minOcc)
		return nil, nil
	}
	if err := a.LocMan.setFieldNames(opts.ModelName, opts.WordsDir); err != nil {
		return nil, err
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
		return nil, fmt.Errorf("no fields selected")
	}

	slog.Debug("in ConfigurationsForGQDocument, before expanding", "len(a.LocMan)", len(a.LocMan))
	slog.Debug("in ConfigurationsForGQDocument, before expanding", "len(locPropsSel)", len(locPropsSel))
	// fmt.Printf("checking opts.configID.ID: %v\n", opts.configID.ID)
	// fmt.Printf("checking opts.configID: %v\n", opts.configID)
	minOccStr := fmt.Sprintf("%02da", minOcc)
	if opts.configID.Field != "" {
		opts.configID.SubID = minOccStr
	} else {
		opts.configID.ID = minOccStr
	}
	rs := map[string]*scrape.Config{}
	// fmt.Printf("before generating Config %#v", opts.configID)
	if err := expandAllPossibleConfigs(gqdoc, opts, locPropsSel, nil, "", rs); err != nil {
		return nil, err
	}
	return rs, nil
}

func expandAllPossibleConfigs(gqdoc *goquery.Document, opts ConfigOptions, locPropsSel []*locationProps, parentRootSelector path, parentItemsStr string, results map[string]*scrape.Config) error {
	prevLogger, err := setDefaultLogger(filepath.Join(opts.outputDirBase, opts.configID.String()+"_expandAllPossibleConfigs_log.txt"))
	if err != nil {
		return err
	}
	defer slog.SetDefault(prevLogger)

	// fmt.Printf("generating Config %#v", opts.configID)
	slog.Debug("generating Config and itemMaps", "opts.configID", opts.configID)
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range locPropsSel {
			slog.Debug("expecting counts", "i", i, "lp.count", lp.count)
		}
	}

	s := scrape.Scraper{
		InputURL: opts.InputURL,
		Name:     opts.configID.String(),
		RenderJs: opts.RenderJS,
		URL:      opts.InputFile,
	}

	rootSelector := findSharedRootSelector(locPropsSel)
	s.Item = rootSelector.string()
	s.Fields = processFields(locPropsSel, rootSelector)
	if opts.DoSubpages && len(s.GetSubpageURLFields()) == 0 {
		slog.Warn("a subpage URL field is required but none were found, ending early", "opts.configID", opts.configID, "opts", opts)
		return nil
	}

	items, err := s.GQDocumentItems(gqdoc, true)
	if err != nil {
		return err
	}
	itemsStr := items.String()
	if scrape.DoPruning && itemsStr == parentItemsStr {
		slog.Debug("generate produced same items as its parent, ending early", "opts.configID", opts.configID)
		return nil
	}

	if slog.Default().Enabled(nil, slog.LevelDebug) {
		slog.Debug("generate produced scraper returning", "len(items)", len(items), "items.TotalFields()", items.TotalFields())
	}

	results[opts.configID.IDString()] = &scrape.Config{
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
		if err := expandAllPossibleConfigs(gqdoc, nextOpts, clusters[clusterID], rootSelector, itemsStr, results); err != nil {
			return err
		}
		lastID++
	}

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

func ConfigurationsForAllSubpages(opts ConfigOptions, pageConfigs map[string]*scrape.Config) (map[string]*scrape.Config, error) {
	prevLogger, err := setDefaultLogger(filepath.Join(opts.outputDirBase, opts.configID.String()+"_ConfigurationsForAllSubpages_log.txt"))
	if err != nil {
		return nil, err
	}
	defer slog.SetDefault(prevLogger)

	slog.Debug("in ConfigurationsForAllSubpages()", "opts.InputURL", opts.InputURL)
	slog.Debug("in ConfigurationsForAllSubPages()", "opts.outputDirBase", opts.outputDirBase)
	slog.Debug("in ConfigurationsForAllSubpages()", "opts", opts)

	uBase, err := url.Parse(opts.InputFile)
	if err != nil {
		return nil, fmt.Errorf("error parsing input url %q: %v", opts.InputFile, err)
	}

	pageJoinsByFieldName := map[string][]*pageJoin{}
	for _, pageC := range pageConfigs {
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
				rel, err := url.Parse(fj.value)
				if err != nil {
					slog.Error("error parsing subpage url", "err", err)
					continue
				}
				fj.url = uBase.ResolveReference(rel).String()
				pj.fieldJoins = append(pj.fieldJoins, fj)
			}
		}
	}

	subURLsSet := map[string]bool{}
	for _, pjs := range pageJoinsByFieldName {
		for _, pj := range pjs {
			for _, fj := range pj.fieldJoins {
				subURLsSet[fj.url] = true
			}
		}
	}
	subURLs := []string{}
	for u := range subURLsSet {
		subURLs = append(subURLs, u)
	}
	sort.Strings(subURLs)
	// subURLs = subURLs[0:5]

	subDir := opts.inputDirBase + "_subpages"
	slog.Debug("in ConfigurationsForAllSubpages()", "subDir", subDir)
	// return nil, nil

	// subURLsPath := fmt.Sprintf("%s_urls.txt", opts.outputDirBase)
	if err := utils.WriteStringFile(filepath.Join(opts.outputDirBase, opts.configID.String()+"_urls.txt"), strings.Join(subURLs, "\n")); err != nil {
		return nil, fmt.Errorf("failed to write subpage URLs list: %v", err)
	}
	if err := fetchSubpages(subURLs, subDir); err != nil {
		return nil, fmt.Errorf("failed to fetch subpages: %v", err)
	}

	subCs := map[string]*scrape.Config{}
	for fname, pjs := range pageJoinsByFieldName {
		opts.configID.Field = fname
		cs, err := ConfigurationsForSubpages(opts, subDir, pjs)
		if err != nil {
			return nil, fmt.Errorf("error generating configuration for subpages for field %q: %v", fname, err)
		}
		for id, c := range cs {
			subCs[id] = c
		}
	}
	return subCs, nil
}

// ConfigurationsForSubpages collects the URL values for a candidate subpage field, retrieves the pages at those URLs, concatenates them, trains a scraper to extract from those subpages, and merges the resulting ItemMap into the parent page, outputing the result.
func ConfigurationsForSubpages(opts ConfigOptions, subDir string, pjs []*pageJoin) (map[string]*scrape.Config, error) {
	prevLogger, err := setDefaultLogger(filepath.Join(opts.outputDirBase, opts.configID.String()+"_ConfigurationsForSubpages_log.txt"))
	if err != nil {
		return nil, err
	}
	defer slog.SetDefault(prevLogger)

	// base := fmt.Sprintf("%s_%s", allSubsBase, fname)
	slog.Debug("in ConfigurationsForSubpages()", "opts", opts)

	// Get all URLs appearing in the values of the fields with this name in the parent pages.
	subURLsSet := map[string]bool{}
	for _, pj := range pjs {
		for _, fj := range pj.fieldJoins {
			subURLsSet[fj.url] = true
		}
	}
	subURLs := []string{}
	for u := range subURLsSet {
		subURLs = append(subURLs, u)
	}
	sort.Strings(subURLs)
	// subURLs = subURLs[0:5]

	// subURLsID := pageOpts.configID
	// subURLsID.Field = fname
	// subURLsPrefix := pageOpts.configID.Slug + "__" + fname
	// subURLsPrefix := pageOpts.configPrefix
	// subURLsPath := subURLsID.WithSuffix("_urls.txt").String()
	// fmt.Printf("subURLsPath: %q\n", subURLsPath)
	if err := utils.WriteStringFile(filepath.Join(opts.outputDirBase, opts.configID.String()+"_urls.txt"), strings.Join(subURLs, "\n")); err != nil {
		return nil, fmt.Errorf("error writing subpage URLs page: %v", err)
	}

	// Concatenate all of the subpages pointed to by the field with this name in the parent pages.
	joinedStr, err := joinPageSubpages(subDir, subURLs)
	if err != nil {
		return nil, err
	}
	joinedPath := filepath.Join(opts.outputDirBase, opts.configID.String()+".html")

	if err := utils.WriteStringFile(joinedPath, joinedStr); err != nil {
		return nil, fmt.Errorf("error writing joined subpages: %v", err)
	}

	// Generate scrapers for the concatenated subpages.
	// opts := &ConfigOptions{}
	// *opts = *pageOpts
	opts.InputURL = "file://" + opts.configID.String() + ".html" // joinedPath
	opts.DoSubpages = false
	// opts.configID.Slug = pageOpts.configID.Slug + "__" + fname
	// opts.configID.Field = fname
	cs, _, err := ConfigurationsForURI(opts)
	if err != nil {
		return nil, err
	}

	// subCConfigBase := filepath.Join(opts.outputDirBase, opts.configID.Slug+"__")
	// slog.Debug("in ConfigurationsForSubpages()", "subCConfigBase", subCConfigBase)
	for id, c := range cs {
		slog.Debug("in ConfigurationsForSubpages()", "id", id)
		slog.Debug("before", "c.Scrapers[0].Item", c.Scrapers[0].Item)
		c.Scrapers[0].Item = strings.TrimPrefix(c.Scrapers[0].Item, "body > htmls > ")
		slog.Debug("after", "c.Scrapers[0].Item", c.Scrapers[0].Item)
		// if err := c.WriteToFile(subCConfigBase); err != nil {
		if err := c.WriteToFile(opts.outputDirBase); err != nil {
			return nil, err
		}
	}

	// When concatenating the subpage HTMLs, we lose their identities.
	// Here we revisit the individual subpages and collect their goquery Documents.
	f := &fetch.FileFetcher{}
	gqdocsByURL := map[string]*goquery.Document{}
	for _, subURL := range subURLs {
		subPath := filepath.Join(subDir, slug.Make(subURL)+".html")
		gqdoc, err := fetch.GQDocument(f, "file://"+subPath, nil)
		// fmt.Printf("adding subURL: %q\n", subURL)
		if err != nil {
			return nil, fmt.Errorf("error fetching subpage at : %v", err)
		}
		gqdocsByURL[subURL] = gqdoc
	}

	// Traverse the fieldJoins for all of the page configs that have a field with this name.
	mergedCs := map[string]*scrape.Config{}
	// mergedCConfigBase := filepath.Join(opts.outputDirBase, pageOpts.configID.Slug+"__")
	// slog.Debug("in ConfigurationsForSubpages()", "mergedCConfigBase", mergedCConfigBase)
	for _, pj := range pjs {
		slog.Debug("looking at", "pj.config.ID.IDString()", pj.config.ID.IDString())
		gqdocs := []*goquery.Document{}
		for _, fj := range pj.fieldJoins {
			// slog.Debug("looking at", "i", i, "fj.name", fj.name)
			// if gqdocsByURL[fj.url] == nil {
			// 	continue
			// 	// fmt.Printf("error: gqdocsByURL[%q] is nil\n", fj.url)
			// }
			gqdocs = append(gqdocs, gqdocsByURL[fj.url])
		}
		for _, c := range cs {
			slog.Debug("looking at", "c.ID", c.ID)
			mergedC := pj.config.Copy()
			// mergedC.ID.ID = pj.config.ID.ID
			mergedC.ID.Field = opts.configID.Field
			mergedC.ID.SubID = c.ID.SubID
			subScraper := c.Scrapers[0]
			mergedC.Scrapers = append(mergedC.Scrapers, subScraper)
			// mergedC.ID.Field = fname
			// mergedID := opts.configID
			// mergedID.ID = id
			// m
			// // mergedC.ID.ID = fmt.Sprintf("%s_%s_%s", pj.config.ID, fname, c.ID.ID)

			if err := subScraper.ExtendGQDocumentItems(opts.configID.Field, mergedC.ItemMaps, gqdocs); err != nil {
				return nil, err
			}

			// slog.Debug("writing", "mergedC.ID", mergedC.ID)
			// if err := mergedC.WriteToFile(mergedCConfigBase); err != nil {
			if err := mergedC.WriteToFile(opts.outputDirBase); err != nil {
				return nil, err
			}
			mergedCs[mergedC.ID.IDString()] = mergedC
		}
	}

	return mergedCs, nil
	// return nil, nil
}

func fetchSubpages(us []string, base string) error {
	fetcher := fetch.NewDynamicFetcher("", 0)
	for _, u := range us {
		// slog.Debug("checking whether to fetch", "u", u)
		if strings.HasPrefix(u, "file://") {
			continue
		}
		subPath := filepath.Join(base, slug.Make(u)+".html")
		// slog.Debug("checking whether page to fetch already exists at", "subPath", subPath)
		if _, err := os.Stat(subPath); err == nil {
			continue
		}

		body, err := fetcher.Fetch(u, nil)
		if err != nil {
			slog.Debug("failed to fetch", "url", u, "err", err)
		}
		if err := utils.WriteStringFile(subPath, body); err != nil {
			slog.Debug("failed to write", "url", u, "err", err)
		}
	}
	return nil
}

var spacesRE = regexp.MustCompile(`\s+`)

// getTagMetadata, for a given node returns a map of key value pairs (only for the attriutes we're interested in) and
// a list of this node's classes and a list of this node's pseudo classes (currently only nth-child).
func getTagMetadata(tagName string, z *html.Tokenizer, siblingNodes []node) (map[string]string, []string, []string) {
	allowedAttrs := map[string]map[string]bool{
		"a":   {"href": true},
		"img": {"src": true},
	}
	moreAttr := true
	attrs := make(map[string]string)
	var cls []string       // classes
	if tagName != "body" { // we don't care about classes for the body tag
		for moreAttr {
			k, v, m := z.TagAttr()
			vString := strings.TrimSpace(string(v))
			kString := string(k)
			if kString == "class" && vString != "" {
				cls = spacesRE.Split(vString, -1)
				j := 0
				for _, cl := range cls {
					// for now we ignore classes that contain dots
					if cl != "" && !strings.Contains(cl, ".") {
						cls[j] = cl
						j++
					}
				}
				cls = cls[:j]
			}
			if _, found := allowedAttrs[tagName]; found {
				if _, found := allowedAttrs[tagName][kString]; found {
					attrs[kString] = vString
				}
			}
			moreAttr = m
		}
	}
	var pCls []string // pseudo classes
	// only add nth-child if there has been another node before at the same
	// level (sibling node) with same tag and the same classes
	for i := 0; i < len(siblingNodes); i++ {
		childNode := siblingNodes[i]
		if childNode.tagName == tagName {
			if utils.SliceEquals(childNode.classes, cls) {
				pCls = []string{fmt.Sprintf("nth-child(%d)", len(siblingNodes)+1)}
				break
			}
		}

	}
	return attrs, cls, pCls
}

func joinPageSubpages(subDir string, subURLs []string) (string, error) {
	r := strings.Builder{}
	r.WriteString("<htmls>\n")
	for _, subURL := range subURLs {
		subPath := filepath.Join(subDir, slug.Make(subURL)+".html")
		sub, err := utils.ReadStringFile(subPath)
		if err != nil {
			return "", fmt.Errorf("error reading subpage at %q: %v", subPath, err)
		}
		r.WriteString("\n")
		r.WriteString(sub)
		r.WriteString("\n")
	}
	r.WriteString("\n</htmls>\n")
	return r.String(), nil
}
