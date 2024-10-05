package autoconfig

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"github.com/agnivade/levenshtein"
	"github.com/findyourpaths/goskyr/date"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scraper"
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
func processFields(locPropsSel []*locationProps, rootSelector path) []scraper.Field {
	zone, _ := time.Now().Zone()
	zone = strings.Replace(zone, "CEST", "CET", 1) // quick hack for issue #209
	dateField := scraper.Field{
		Name:         "date",
		Type:         "date",
		DateLocation: zone,
	}
	fields := []scraper.Field{}

	for _, e := range locPropsSel {
		loc := scraper.ElementLocation{
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
			dateField.Components = append(dateField.Components, scraper.DateComponent{
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
		d := scraper.Field{
			Name:             e.name,
			Type:             fieldType,
			ElementLocations: []scraper.ElementLocation{loc},
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
	// returns true if the paths overlap and the rest of the
	// element location is identical. If true is returned
	// the Selector of old will be updated if necessary.
	if old.textIndex == new.textIndex && old.attr == new.attr {
		if len(old.path) != len(new.path) {
			return false
		}
		newPath := path{}
		for i, on := range old.path {
			if on.tagName == new.path[i].tagName {
				pseudoClassesTmp := []string{}
				if i > old.iStrip {
					pseudoClassesTmp = new.path[i].pseudoClasses
				}
				// the following checks are not complete yet but suffice for now
				// with nth-child being our only pseudo class
				if len(on.pseudoClasses) == len(pseudoClassesTmp) {
					if len(on.pseudoClasses) == 1 {
						if on.pseudoClasses[0] != pseudoClassesTmp[0] {
							return false
						}
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
					// if nodes have more than 0 classes there has to be at least 1 overlapping class
					// does this make sense?
					if len(ovClasses) > 0 {
						newNode.classes = ovClasses
						newPath = append(newPath, newNode)
						continue
					}
				}
			}
			return false

		}
		// if we get until here there is an overlapping path
		old.path = newPath
		old.count++
		old.examples = append(old.examples, new.examples...)
		return true

	}
	return false
}

// remove if count is smaller than minCount
func filterBelowMinCount(lps []*locationProps, minCount int) []*locationProps {
	var filtered []*locationProps
	for _, lp := range lps {
		if lp.count < minCount {
			// if p.count != minCount {
			continue
		}
		filtered = append(filtered, lp)
	}
	return filtered
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
func filterAllButLargestCluster(lps []*locationProps, rootSelector path) ([]*locationProps, path) {
	// slog.Debug("filterAllButLargestCluster(lps (%d), rootSelector.string(): %q)", len(lps), rootSelector.string())
	clusterCounts := map[string]int{}
	newLen := len(rootSelector) + 1
	maxCount := 0
	var maxPath path
	for _, lp := range lps {
		// slog.Debug("looking at lp with count: %d and path: %q", lp.count, lp.path.string())
		// check whether we reached the end.
		if newLen > len(lp.path) {
			return lps, rootSelector
		}
		p := lp.path[0:newLen]
		pStr := p.string()
		clusterCounts[pStr] += lp.count
		if clusterCounts[pStr] > maxCount {
			maxCount = clusterCounts[pStr]
			maxPath = p
		}
	}

	maxPathStr := maxPath.string()
	// slog.Debug("maxCount: %d", maxCount)
	// slog.Debug("maxPathStr: %q", maxPathStr)
	var filtered []*locationProps
	for _, lp := range lps {
		if lp.path[0:newLen].string() != maxPathStr {
			continue
		}
		filtered = append(filtered, lp)
	}
	// slog.Debug("filterAllButLargestCluster() returning filtered (%d), maxPath.string(): %q)", len(filtered), maxPath.string())
	return filtered, maxPath
}

func NewDynamicFieldsConfigs(u string, renderJs bool, minOcc int, onlyVarying bool, modelName, wordsDir string, batch bool) ([]*scraper.Config, []output.ItemMaps, error) {
	slog.Debug("NewDynamicFieldsConfigs()")
	if len(u) == 0 {
		return nil, nil, errors.New("URL field cannot be empty")
	}
	s := scraper.Scraper{
		URL:      u,
		Name:     u,
		RenderJs: renderJs,
	}

	// slog.Debug("strings.HasPrefix(s.URL, \"file://\": %t", strings.HasPrefix(s.URL, "file://"))
	var fetcher fetch.Fetcher
	if s.RenderJs {
		fetcher = fetch.NewDynamicFetcher("", 0)
	} else if strings.HasPrefix(s.URL, "file://") {
		fetcher = &fetch.FileFetcher{}
	} else {
		fetcher = &fetch.StaticFetcher{}
	}
	res, err := fetcher.Fetch(s.URL, fetch.FetchOpts{})
	if err != nil {
		return nil, nil, err
	}

	// A bit hacky. But goquery seems to manipulate the html (I only know of goquery adding tbody tags if missing)
	// so we rely on goquery to read the html for both scraping AND figuring out the scraping config.
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(res))
	if err != nil {
		return nil, nil, err
	}

	// Now we have to translate the goquery doc back into a string
	htmlStr, err := goquery.OuterHtml(doc.Children())
	if err != nil {
		return nil, nil, err
	}

	slog.Debug("writing html to file", "u", u)
	fpath, err := utils.WriteTempStringFile("/tmp/goskyr/autoconfig/NewDynamicFieldsConfigsDoc/"+slug.Make(u)+".html", htmlStr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to write html file: %v", err)
	}
	slog.Debug("wrote html to file", "fpath", fpath)

	a := &Analyzer{
		Tokenizer:   html.NewTokenizer(strings.NewReader(htmlStr)),
		NumChildren: map[string]int{},
		ChildNodes:  map[string][]node{},
	}

	slog.Debug("in NewDynamicFieldsConfigs(): parsing")
	a.Parse()
	for i, lp := range a.LocMan {
		slog.Debug("raw", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
	}
	a.LocMan = squashLocationManager(a.LocMan, minOcc)
	for i, lp := range a.LocMan {
		slog.Debug("squashed", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
	}
	a.LocMan = filterBelowMinCount(a.LocMan, minOcc)
	for i, lp := range a.LocMan {
		slog.Debug("filtered min count", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
	}
	if onlyVarying {
		a.LocMan = filterStaticFields(a.LocMan)
	}
	for i, lp := range a.LocMan {
		slog.Debug("filtered static", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
	}

	if err := a.LocMan.findFieldNames(modelName, wordsDir); err != nil {
		return nil, nil, err
	}
	if len(a.LocMan) == 0 {
		return nil, nil, fmt.Errorf("no fields found")
	}

	var locPropsSel []*locationProps
	if !batch {
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

	rootSelector := findSharedRootSelector(locPropsSel)
	var newRootSelector path
	var cs []*scraper.Config
	var ims []output.ItemMaps

	for {
		slog.Debug("in locationManager.GetDynamicFieldsConfig()", "root selector", rootSelector)
		s.Item = shortenRootSelector(rootSelector).string()
		s.Item = rootSelector.string()
		slog.Debug("in locationManager.GetDynamicFieldsConfig()", "s.Item", s.Item)
		s.Fields = processFields(locPropsSel, rootSelector)

		c := &scraper.Config{Scrapers: []scraper.Scraper{s}}
		items, err := s.GetItems(&c.Global, true)
		if err != nil {
			return nil, nil, err
		}
		slog.Debug("autoconfig produced scraper returning", "len(items)", len(items), "items.TotalFields()", items.TotalFields())
		if slog.Default().Enabled(nil, slog.LevelDebug) {
			fmt.Printf(items.String())
		}
		cs = append(cs, c)
		ims = append(ims, items)
		locPropsSel, newRootSelector = filterAllButLargestCluster(locPropsSel, rootSelector)
		if newRootSelector.string() == rootSelector.string() {
			break
		}
		rootSelector = newRootSelector
	}

	for i, lp := range a.LocMan {
		slog.Debug("final", "i", i, "lp.count", lp.count, "lp.path.string()", lp.path.string())
	}
	return cs, ims, nil
}

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
				cls = strings.Split(vString, " ")
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
