package generate

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/findyourpaths/phil/parse"
	"golang.org/x/net/html"
)

func analyzePage(opts ConfigOptions, htmlStr string, minOcc int) ([]*locationProps, []*locationProps, error) {
	// if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
	// 	prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_analyzePage_log.txt"), slog.LevelDebug)
	// 	if err != nil {
	// 		return nil, nil, err
	// 	}
	// 	defer output.RestoreDefaultLogger(prevLogger)
	// }
	// slog.Info("analyzePage()", "opts", opts)
	// defer slog.Info("analyzePage() returning")

	a := &Analyzer{
		Tokenizer:   html.NewTokenizer(strings.NewReader(htmlStr)),
		NumChildren: map[string]int{},
		ChildNodes:  map[string][]node{},
		FindNext:    opts.configID.Field == "" && opts.configID.SubID == "",
	}
	a.Parse()

	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range a.LocMan {
			slog.Debug("raw", "i", i, "lp", lp.DebugString())
		}
		for i, lp := range a.PagMan {
			slog.Debug("raw pags", "i", i, "lp", lp.DebugString())
		}
	}

	a.LocMan = squashLocationManager(a.LocMan, minOcc)
	a.PagMan = squashLocationManager(a.PagMan, 3)
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range a.LocMan {
			slog.Debug("after squashing", "i", i, "lp", lp.DebugString())
		}
		for i, lp := range a.PagMan {
			slog.Debug("after squashing pags", "i", i, "lp", lp.DebugString())
		}
	}

	// Set the field names now and log what gets filtered out.
	if err := a.LocMan.setFieldNames(opts.ModelName, opts.WordsDir); err != nil {
		return nil, nil, err
	}

	a.LocMan = filterBelowMinCount(a.LocMan, minOcc)
	a.PagMan = filterBelowMinCount(a.PagMan, 3)
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range a.LocMan {
			slog.Debug("after filtering min count", "i", i, "lp", lp.DebugString())
		}
		for i, lp := range a.PagMan {
			slog.Debug("after filtering min count pags", "i", i, "lp", lp.DebugString())
		}
	}

	slog.Debug("in analyzePage()", "opts.OnlyVaryingFields", opts.OnlyVaryingFields)
	if opts.OnlyVaryingFields {
		a.LocMan = filterStaticFields(a.LocMan)
		a.PagMan = filterStaticFields(a.PagMan)
		if slog.Default().Enabled(nil, slog.LevelDebug) {
			for i, lp := range a.LocMan {
				slog.Debug("after filtering static", "i", i, "lp", lp.DebugString())
			}
			for i, lp := range a.PagMan {
				slog.Debug("after filtering static pags", "i", i, "lp", lp.DebugString())
			}
		}
	}

	slog.Debug("in analyzePage(), final", "len(a.LocMan)", len(a.LocMan))
	slog.Debug("in analyzePage(), final", "len(a.PagMan)", len(a.PagMan))

	if len(a.LocMan) == 0 {
		slog.Info("no fields found", "opts", opts, "minOcc", minOcc)
		return nil, nil, nil
	}

	var lps []*locationProps
	if !opts.Batch {
		a.LocMan.setColors()
		a.LocMan.selectFieldsTable()
		for _, lm := range a.LocMan {
			if lm.selected {
				lps = append(lps, lm)
			}
		}
	} else {
		lps = a.LocMan
	}
	if len(lps) == 0 {
		return nil, nil, fmt.Errorf("no fields selected")
	}
	return lps, append(a.NextPaths, a.PagMan...), nil
}

func findSharedRootSelector(lps []*locationProps) path {
	slog.Debug("findSharedRootSelector()", "len(lps)", len(lps))
	if len(lps) == 1 {
		slog.Debug("in findSharedRootSelector(), found singleton, returning", "lps[0].path.string()", lps[0].path.string())
		return lps[0].path
	}
	for j, lp := range lps {
		slog.Debug("in findSharedRootSelector(), all", "j", j, "lp", lp.DebugString())
	}
	for i := 0; ; i++ {
		slog.Debug("in findSharedRootSelector()", "i", i)
		var n node
		for j, lp := range lps {
			slog.Debug("in findSharedRootSelector()", "  j", j, "lp", lp.DebugString())
			if i+1 == len(lp.path) {
				slog.Debug("in findSharedRootSelector(), returning end", "lp.path[:i].string()", lp.path[:i].string())
				return lp.path[:i]
			}

			// if lp.isText && i == len(lp.path) {
			// 	slog.Debug("in findSharedRootSelector(), returning 2", "lp.path[:i].string()", lp.path[:i].string())
			// 	return lp.path[:i]
			// }
			// if !lp.isText && i == len(lp.path)-1 {
			// 	slog.Debug("in findSharedRootSelector(), returning 2", "lp.path[:i].string()", lp.path[:i].string())
			// 	return lp.path[:i]
			// }

			if j == 0 {
				n = lp.path[i]
			} else {
				// Look for divergence and if found, return what we have so far.
				if !n.equals(lp.path[i]) {
					slog.Debug("in findSharedRootSelector(), found divergence, returning", "lp.path[:i].string()", lp.path[:i].string())
					return lp.path[:i]
				}
			}
		}
	}
	slog.Debug("in findSharedRootSelector(), returning nil")
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

var datetimeFieldThreshold = 0.5

var datetimeWeekdays = "sun|sunday|mon|monday|tue|tues|tuesday|wed|weds|wednesday|thu|thus|thursday|fri|friday|saturday|sat"
var datetimeMonths = "jan|january|feb|february|mar|march|apr|april|may|jun|june|jul|july|aug|august|sep|sept|september|oct|october|nov|november|dec|december"
var datetimeFieldRE = regexp.MustCompile(`(?i)\b(?:(?:19|20)\d{2}|` + datetimeMonths + `|` + datetimeWeekdays + `)\b`)

// for now we assume that there will only be one date field
func processFields(exsCache map[string]string, lps []*locationProps, rootSelector path) []scrape.Field {
	slog.Debug("processFields()", "len(lps)", len(lps))

	// zone, _ := time.Now().Zone()
	// zone = strings.Replace(zone, "CEST", "CET", 1) // quick hack for issue #209
	// dateField := scrape.Field{
	// 	Name:         "date",
	// 	Type:         "date",
	// 	DateLocation: zone,
	// }

	slog.Debug("in processFields()", "len(rootSelector)", len(rootSelector), "rootSelector", rootSelector.string())
	rs := []scrape.Field{}
	for _, lp := range lps {
		// slog.Debug("in processFields()", "e.path", e.path.string())
		// slog.Debug("in processFields()", "e.path[len(rootSelector):]", e.path[len(rootSelector):].string())
		fLoc := scrape.ElementLocation{
			Selector:   lp.path[len(rootSelector):].string(),
			ChildIndex: lp.textIndex,
			Attr:       lp.attr,
		}
		fName := lp.name
		fType := "text"
		if fLoc.Attr == "href" || fLoc.Attr == "src" {
			fType = "url"
		} else {
			num := 0
			for _, ex := range lp.examples {
				if _, found := exsCache[ex]; found {
					num += 1
					continue
				}

				if !datetimeFieldRE.MatchString(ex) {
					slog.Debug("in processFields(), ignoring field value with no datetimes", "ex", ex)
					continue
				}

				slog.Debug("in processFields(), parsing field value with datetime", "ex", ex)
				rngs, err := parse.ExtractDateTimeTZRanges(0, "", "", ex)
				if err != nil {
					exsCache[ex] = ""
					slog.Warn("parse error", "err", err)
				}
				if rngs != nil && len(rngs.Items) > 0 {
					exsCache[ex] = rngs.String()
					num += 1
				}
			}
			if float64(num)/float64(len(lp.examples)) > datetimeFieldThreshold {
				// fName = fmt.Sprintf("date"
				fType = "date_time_tz_ranges"
			}
		}
		// if strings.HasPrefix(lp.name, "date-component") {
		// 	cd := date.CoveredDateParts{
		// 		Day:   strings.Contains(lp.name, "day"),
		// 		Month: strings.Contains(lp.name, "month"),
		// 		Year:  strings.Contains(lp.name, "year"),
		// 		Time:  strings.Contains(lp.name, "time"),
		// 	}
		// 	format, lang := date.GetDateFormatMulti(lp.examples, cd)
		// 	dateField.Components = append(dateField.Components, scrape.DateComponent{
		// 		ElementLocation: loc,
		// 		Covers:          cd,
		// 		Layout:          []string{format},
		// 	})
		// 	if dateField.DateLanguage == "" {
		// 		// first lang wins
		// 		dateField.DateLanguage = lang
		// 	}
		// 	continue
		// }

		f := scrape.Field{
			Name:             fName,
			Type:             fType,
			ElementLocations: []scrape.ElementLocation{fLoc},
			CanBeEmpty:       true,
		}
		rs = append(rs, f)
	}

	// if len(dateField.Components) > 0 {
	// 	fields = append(fields, dateField)
	// }
	slog.Debug("processFields() returning", "len(rs)", len(rs))
	return rs
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
func stripNthChild(lps *locationProps, minOcc int) {
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
	for i := len(lps.path) - sub; i >= 0; i-- {
		if i < iStrip {
			lps.path[i].pseudoClasses = []string{}
		} else if len(lps.path[i].pseudoClasses) > 0 {
			// nth-child(x)
			ncIndex, _ := strconv.Atoi(strings.Replace(strings.Split(lps.path[i].pseudoClasses[0], "(")[1], ")", "", 1))
			if ncIndex >= minOcc {
				lps.path[i].pseudoClasses = []string{}
				iStrip = i
				// we need to pass iStrip to the locationProps too to be used by checkAndUpdateLocProps
				lps.iStrip = iStrip
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
			slog.Debug("in filterBelowMinCount dropping", "minCount", minCount, "lp.count", lp.count, "lp.path", lp.path.string())
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
		slog.Debug("in filterStaticFields", "lp", lp.DebugString())
		slog.Debug("in filterStaticFields", "len(lp.examples)", len(lp.examples), "lp.examples[0]", lp.examples[0])
		for i, ex := range lp.examples {
			slog.Debug("in filterStaticFields, looking for varying", "i", i, "ex", ex)
			if ex != lp.examples[0] {
				varied = true
				// break
			}
		}
		slog.Debug("in filterStaticFields", "varied", varied)
		if varied {
			kept = append(kept, lp)
		}
	}
	return kept
}

// Go one element beyond the root selector length and find the cluster with the largest number of fields.
// Filter out all of the other fields.
func findClusters(lps []*locationProps, rootSelector path) map[string][]*locationProps {
	slog.Debug("findClusters()", "len(lps)", len(lps), "len(rootSelector)", len(rootSelector), "rootSelector", rootSelector.string())

	locationPropsByPath := map[string][]*locationProps{}
	newLen := len(rootSelector) + 1
	slog.Debug("in findClusters()", "newLen", newLen)
	for _, lp := range lps {
		slog.Debug("in findClusters()", "lp", lp.DebugString())
		// Check whether we reached the end.
		// If our new root selector is longer or equal to the length of this path, return.
		if newLen > len(lp.path) {
			continue
			// return locationPropsByPath
		}
		lpStr := lp.path[0:newLen].string()
		locationPropsByPath[lpStr] = append(locationPropsByPath[lpStr], lp)
		slog.Debug("in findClusters(), added lp", "lpStr", lpStr)
	}
	for pStr, pByP := range locationPropsByPath {
		slog.Debug("in findClusters()", "pStr", pStr, "len(pByP)", len(pByP))
	}
	return locationPropsByPath
}
