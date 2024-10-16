package generate

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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
	// Returns true if the paths overlap and the rest of the
	// element location is identical. If true is returned,
	// the Selector of old will be updated if necessary.

	if old.textIndex != new.textIndex {
		return false
	}
	if old.attr != new.attr {
		return false
	}
	if len(old.path) != len(new.path) {
		return false
	}

	newPath := make(path, 0, len(old.path)) // Pre-allocate with capacity
	for i, on := range old.path {
		if on.tagName != new.path[i].tagName {
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
	Batch       bool
	InputURL    string
	MinOccs     []int
	ModelName   string
	OnlyVarying bool
	RenderJS    bool
	DoSubpages  bool
	URL         string
	WordsDir    string
}

func ConfigurationsForPage(opts ConfigOptions, base string, toStdout bool, doSubpages bool) (map[string]*scrape.Config, error) {
	slog.Debug("starting to generate config")
	slog.Debug("analyzing", "opts.InputURL", opts.InputURL)

	cs, err := ConfigurationsForURI(opts)
	if err != nil {
		return nil, err
	}

	for _, c := range cs {
		if toStdout {
			fmt.Println(c.String())
		}
		if err := c.WriteToFile(base); err != nil {
			return nil, err
		}
	}

	if doSubpages {
		if _, err := ConfigurationsForAllSubpages(opts, cs, base); err != nil {
			return nil, fmt.Errorf("error generating configuration for all subpages: %v", err)
		}
	}
	return cs, nil
}

func ConfigurationsForAllSubpages(pageOpts ConfigOptions, cs map[string]*scrape.Config, base string) (map[string]*scrape.Config, error) {
	slog.Debug("ConfigurationsForSubpages()", "pageOpts", pageOpts)
	uBase, err := url.Parse(pageOpts.URL)
	if err != nil {
		return nil, fmt.Errorf("error parsing input url %q: %v", pageOpts.URL, err)
	}

	subpageURLsBySlug := map[string]string{}
	subpageURLSetsByFieldName := map[string]map[string]bool{}

	for _, c := range cs {
		for _, s := range c.Scrapers {
			// fmt.Printf("found %d subpage URL fields\n", len(s.GetSubpageURLFields()))
			for _, f := range s.GetSubpageURLFields() {
				for _, im := range c.ItemMaps {
					rel, err := url.Parse(fmt.Sprintf("%v", im[f.Name]))
					if err != nil {
						slog.Error("error parsing subpage url", "err", err)
						continue
					}
					u := uBase.ResolveReference(rel)
					if subpageURLSetsByFieldName[f.Name] == nil {
						subpageURLSetsByFieldName[f.Name] = map[string]bool{}
					}
					subpageURLSetsByFieldName[f.Name][u.String()] = true
					subpageURLsBySlug[slug.Make(u.String())+".html"] = u.String()
				}
			}
		}
	}

	allSubsBase := fmt.Sprintf("%s_subpages", base)

	subURLs := []string{}
	for _, u := range subpageURLsBySlug {
		subURLs = append(subURLs, u)
	}
	sort.Strings(subURLs)
	subURLsPath := fmt.Sprintf("%s_urls.txt", allSubsBase)
	if err := utils.WriteStringFile(subURLsPath, strings.Join(subURLs, "\n")); err != nil {
		return nil, err
	}

	if err := fetchSubpages(subURLs, allSubsBase); err != nil {
		return nil, err
	}

	for fname, uset := range subpageURLSetsByFieldName {
		us := []string{}
		for u := range uset {
			us = append(us, u)
		}
		sort.Strings(us)
		if _, err := ConfigurationsForSubpages(pageOpts, allSubsBase, fname, us); err != nil {
			return nil, fmt.Errorf("error generating configuration for subpages: %v", err)
		}
	}
	return nil, err
}

func ConfigurationsForSubpages(pageOpts ConfigOptions, allSubsBase string, fname string, us []string) (map[string]*scrape.Config, error) {
	base := fmt.Sprintf("%s_%s", allSubsBase, fname)
	slog.Debug("ConfigurationsForSubpages()", "pageOpts", pageOpts)
	usPath := fmt.Sprintf("%s-urls.txt", base)
	if err := utils.WriteStringFile(usPath, strings.Join(us, "\n")); err != nil {
		return nil, fmt.Errorf("error writing subpage URLs page: %v", err)
	}

	merged := strings.Builder{}
	for _, u := range us {
		subPath := filepath.Join(allSubsBase, slug.Make(u)+".html")
		sub, err := utils.ReadStringFile(subPath)
		if err != nil {
			return nil, fmt.Errorf("error reading subpage at : %v", err)
		}
		merged.WriteString("\n" + sub + "\n")
	}
	mergedPath := fmt.Sprintf("%s.html", base)
	if err := utils.WriteStringFile(mergedPath, "<htmls>\n"+merged.String()+"\n</htmls>\n"); err != nil {
		return nil, fmt.Errorf("error writing merged subpages: %v", err)
	}

	opts := pageOpts
	opts.InputURL = "file://" + mergedPath
	opts.DoSubpages = false
	cs, err := ConfigurationsForURI(opts)
	if err != nil {
		return nil, err
	}

	for _, c := range cs {
		if err := c.WriteToFile(base); err != nil {
			return nil, err
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

func fetchSubpages(us []string, base string) error {
	return nil

	fetcher := fetch.NewDynamicFetcher("", 0)
	for _, u := range us {
		body, err := fetcher.Fetch(u, fetch.FetchOpts{})
		if err != nil {
			slog.Debug("failed to fetch", "url", u, "err", err)
		}
		subPath := filepath.Join(base, slug.Make(u)+".html")
		if err := utils.WriteStringFile(subPath, body); err != nil {
			return err
		}
	}
	return nil
}

func ConfigurationsForURI(opts ConfigOptions) (map[string]*scrape.Config, error) {
	slog.Debug("ConfigurationsForURL()", "opts", opts)
	if len(opts.InputURL) == 0 {
		return nil, errors.New("URL field cannot be empty")
	}

	// slog.Debug("strings.HasPrefix(s.URL, \"file://\": %t", strings.HasPrefix(s.URL, "file://"))
	var fetcher fetch.Fetcher
	if opts.RenderJS {
		fetcher = fetch.NewDynamicFetcher("", 0)
	} else if strings.HasPrefix(opts.InputURL, "file://") {
		fetcher = &fetch.FileFetcher{}
	} else {
		fetcher = &fetch.StaticFetcher{}
	}
	res, err := fetcher.Fetch(opts.InputURL, fetch.FetchOpts{})
	if err != nil {
		return nil, err
	}
	opts.InputURL = "file://" + slug.Make(opts.InputURL) + ".html"

	// A bit hacky. But goquery seems to manipulate the html (I only know of goquery adding tbody tags if missing)
	// so we rely on goquery to read the html for both scraping AND figuring out the scraping config.
	gqdoc, err := goquery.NewDocumentFromReader(strings.NewReader(res))
	if err != nil {
		return nil, err
	}

	rs := map[string]*scrape.Config{}
	// Generate configs for each of the minimum occs.
	for _, minOcc := range opts.MinOccs {
		slog.Debug("calling ConfigurationsForGQDocument()", "minOcc", minOcc, "opts.InputURL", opts.InputURL)
		cims, err := ConfigurationsForGQDocument(gqdoc, opts, minOcc)
		if err != nil {
			return nil, err
		}
		for k, v := range cims {
			rs[k] = v
		}
	}
	return rs, nil
}

var htmlOutputDir = "/tmp/goskyr/generate/ConfigurationsForGQDocument/"

func ConfigurationsForGQDocument(gqdoc *goquery.Document, opts ConfigOptions, minOcc int) (map[string]*scrape.Config, error) {
	// Now we have to translate the goquery doc back into a string
	htmlStr, err := goquery.OuterHtml(gqdoc.Children())
	if err != nil {
		return nil, err
	}

	slog.Debug("writing html to file", "u", opts.InputURL)
	fpath, err := utils.WriteTempStringFile(filepath.Join(htmlOutputDir, slug.Make(opts.InputURL)+".html"), htmlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to write html file: %v", err)
	}
	slog.Debug("wrote html to file", "fpath", fpath)

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

	if len(a.LocMan) == 0 {
		slog.Warn("no fields found", "opts", opts, "minOcc", minOcc)
		return nil, nil
	}
	if err := a.LocMan.setFieldNames(opts.ModelName, opts.WordsDir); err != nil {
		return nil, err
	}

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

	rs := map[string]*scrape.Config{}
	if err := expandAllPossibleConfigs(gqdoc, fmt.Sprintf("%02d-a", minOcc), opts, locPropsSel, nil, rs); err != nil {
		return nil, err
	}
	return rs, nil
}

func expandAllPossibleConfigs(gqdoc *goquery.Document, id string, opts ConfigOptions, locPropsSel []*locationProps, parentRootSelector path, results map[string]*scrape.Config) error {
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		slog.Debug("generating Config and itemMaps", "id", id)
		for i, lp := range locPropsSel {
			slog.Debug("expecting counts", "i", i, "lp.count", lp.count)
		}
	}

	s := scrape.Scraper{
		URL:      opts.URL,
		InputURL: opts.InputURL,
		Name:     opts.InputURL,
		RenderJs: opts.RenderJS,
	}

	rootSelector := findSharedRootSelector(locPropsSel)
	slog.Debug("in expandAllPossibleConfigs()", "root selector", rootSelector)
	// s.Item = shortenRootSelector(rootSelector).string()
	s.Item = rootSelector.string()
	slog.Debug("in expandAllPossibleConfigs()", "s.Item", s.Item)
	s.Fields = processFields(locPropsSel, rootSelector)

	items, err := s.GQDocumentItems(gqdoc, true)
	if err != nil {
		return err
	}
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		slog.Debug("generate produced scraper returning", "len(items)", len(items), "items.TotalFields()", items.TotalFields())
		fmt.Println(items.String())
	}

	c := &scrape.Config{
		ID:       id,
		Scrapers: []scrape.Scraper{s},
		ItemMaps: items,
	}
	if !opts.DoSubpages || len(s.GetSubpageURLFields()) > 0 {
		results[id] = c
	} else {
		slog.Warn("a subpage URL field is required but none were found", "id", id, "opts", opts)
		// We don't add this result, but we may add an expanded config.
	}
	// slog.Info("created scraper", "id", id, "rootSelector diff", strings.TrimPrefix(rootSelector.string(), parentRootSelector.string()))

	clusters := findClusters(locPropsSel, rootSelector)
	clusterIDs := []string{}
	for clusterID := range clusters {
		clusterIDs = append(clusterIDs, clusterID)
	}
	sort.Strings(clusterIDs)

	lastID := 'a'
	for _, clusterID := range clusterIDs {
		if err := expandAllPossibleConfigs(gqdoc, id+string(lastID), opts, clusters[clusterID], rootSelector, results); err != nil {
			return err
		}
		lastID++
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
