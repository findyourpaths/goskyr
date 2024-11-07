package generate

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/findyourpaths/goskyr/date"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"golang.org/x/net/html"
)

func analyzePage(opts ConfigOptions, htmlStr string, minOcc int) ([]*locationProps, []*locationProps, error) {
	a := &Analyzer{
		Tokenizer:   html.NewTokenizer(strings.NewReader(htmlStr)),
		NumChildren: map[string]int{},
		ChildNodes:  map[string][]node{},
		FindNext:    opts.configID.Field == "" && opts.configID.SubID == "",
	}
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
		return nil, nil, nil
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

	return locPropsSel, append(a.NextPaths, a.PagMan...), nil
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
