package generate

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/observability"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/findyourpaths/phil/datetime"
	"github.com/kr/pretty"
	"github.com/samber/lo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/net/html"
)

// var DoDebug = false

var DoDebug = true

// analyzePage parses HTML and discovers repeating patterns by tokenizing the HTML, tracking element
// paths, and identifying fields that appear at least minOcc times.
func analyzePage(ctx context.Context, opts ConfigOptions, htmlStr string, minOcc int) ([]*locationProps, []*locationProps, error) {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/generate").Start(ctx, fmt.Sprintf("generate.analyzePage(%d)", minOcc))

	// Metering
	a := &Analyzer{
		Tokenizer:   html.NewTokenizer(strings.NewReader(htmlStr)),
		NumChildren: map[string]int{},
		ChildNodes:  map[string][]node{},
		FindNext:    opts.configID.Field == "" && opts.configID.SubID == "",
	}
	var locManRaws []string
	var locManSquasheds []string
	var locManFilteredMins []string
	var retLocMans []string
	var pagManRaws []string
	var pagManSquasheds []string
	var pagManFilteredMins []string
	var retPagMans []string
	defer func() {
		observability.Add(ctx, observability.Instruments.Generate, 1,
			attribute.Int("arg.minocc", minOcc),
			attribute.Int("int.locman_1.len", len(a.LocMan)),
			attribute.String("int.locman_1.raws", strings.Join(locManRaws, "\n")),
			attribute.String("int.locman_2.squasheds", strings.Join(locManSquasheds, "\n")),
			attribute.String("int.locman_3.filtered_mins", strings.Join(locManFilteredMins, "\n")),
			attribute.String("ret.locman_4", strings.Join(retLocMans, "\n")),
			attribute.Int("int.pagman_1.len", len(a.PagMan)),
			attribute.String("int.pagman_1.raws", strings.Join(pagManRaws, "\n")),
			attribute.String("int.pagman_2.squasheds", strings.Join(pagManSquasheds, "\n")),
			attribute.String("int.pagman_3.filtered_mins", strings.Join(pagManFilteredMins, "\n")),
			attribute.String("ret.pagman_4", strings.Join(retPagMans, "\n")),
		)
		span.End()
	}()

	// Logging
	if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_analyzePage_log.txt"), slog.LevelDebug)
		if err != nil {
			return nil, nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Info("analyzePage()", "opts", opts)
	// defer slog.Info("analyzePage() returning")

	a.Parse()
	for i, lp := range a.LocMan {
		locManRaws = append(locManRaws, lp.DebugString())
		if DoDebug && slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("raw", "i", i, "lp", lp.DebugString())
		}
	}
	for i, lp := range a.PagMan {
		pagManRaws = append(pagManRaws, lp.DebugString())
		if DoDebug && slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("raw pags", "i", i, "lp", lp.DebugString())
		}
	}

	a.LocMan = squashLocationManager(a.LocMan, minOcc)
	a.PagMan = squashLocationManager(a.PagMan, 3)
	for i, lp := range a.LocMan {
		locManSquasheds = append(locManSquasheds, lp.DebugString())
		if DoDebug && slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("after squashing", "i", i, "lp", lp.DebugString())
		}
	}
	for i, lp := range a.PagMan {
		pagManSquasheds = append(pagManSquasheds, lp.DebugString())
		if DoDebug && slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("after squashing pags", "i", i, "lp", lp.DebugString())
		}
	}

	// Set the field names now and log what gets filtered out.
	if err := a.LocMan.setFieldNames(opts.ModelName, opts.WordsDir); err != nil {
		return nil, nil, err
	}

	a.LocMan = filterBelowMinCount(a.LocMan, minOcc)
	a.PagMan = filterBelowMinCount(a.PagMan, 3)
	for i, lp := range a.LocMan {
		locManFilteredMins = append(locManFilteredMins, lp.DebugString())
		if DoDebug && slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("after filtering min count", "i", i, "lp", lp.DebugString())
		}
	}
	for i, lp := range a.PagMan {
		pagManFilteredMins = append(pagManFilteredMins, lp.DebugString())
		if DoDebug && slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("after filtering min count pags", "i", i, "lp", lp.DebugString())
		}
	}

	slog.Debug("in analyzePage()", "opts.OnlyVaryingFields", opts.OnlyVaryingFields)
	if opts.OnlyVaryingFields {
		a.LocMan = filterStaticFields(a.LocMan)
		a.PagMan = filterStaticFields(a.PagMan)
		for i, lp := range a.LocMan {
			retLocMans = append(retLocMans, lp.DebugString())
			if DoDebug && slog.Default().Enabled(ctx, slog.LevelDebug) {
				slog.Debug("after filtering static", "i", i, "lp", lp.DebugString())
			}
		}
		for i, lp := range a.PagMan {
			retPagMans = append(retPagMans, lp.DebugString())
			if DoDebug && slog.Default().Enabled(ctx, slog.LevelDebug) {
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
	if opts.Batch {
		lps = a.LocMan
	} else {
		a.LocMan.setColors()
		a.LocMan.selectFieldsTable()
		for _, lm := range a.LocMan {
			if lm.selected {
				lps = append(lps, lm)
			}
		}
	}
	if len(lps) == 0 {
		return nil, nil, fmt.Errorf("no fields selected")
	}
	return lps, append(a.NextPaths, a.PagMan...), nil
}

// findSharedRootSelector finds the common parent selector that contains all discovered field locations
// by walking up the DOM tree and finding where paths diverge.
func findSharedRootSelector(ctx context.Context, gqdoc *fetch.Document, lps []*locationProps) path {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/generate").Start(ctx, fmt.Sprintf("generate.findSharedRootSelector(%d)", len(lps)))

	// Metering
	status := "unknown"
	var i int
	var j int
	var retPath path
	defer func() {
		observability.Add(ctx, observability.Instruments.Generate, 1,
			attribute.String("status", status),
			attribute.Int("arg.lps.len", len(lps)),
			attribute.Int("int.i", i),
			attribute.Int("int.j", j),
			attribute.String("ret", retPath.string()),
		)
		span.End()
	}()

	// Logging
	if DoDebug {
		slog.Debug("findSharedRootSelector()", "len(lps)", len(lps))
	}
	if len(lps) == 1 {
		status = "singleton"
		if DoDebug {
			slog.Debug("in findSharedRootSelector(), found singleton, returning", "lps[0].path.string()", lps[0].path.string())
		}
		retPath = pullBackRootSelector(ctx, lps[0].path, gqdoc, lps[0].count)
		return retPath
	}
	if DoDebug {
		for j, lp := range lps {
			slog.Debug("in findSharedRootSelector(), all", "j", j, "lp", lp.DebugString())
		}
	}
	for i = 0; ; i++ {
		if DoDebug {
			slog.Debug("in findSharedRootSelector()", "i", i)
		}
		var n node
		var lp *locationProps
		for j, lp = range lps {
			if DoDebug {
				slog.Debug("in findSharedRootSelector()", "  j", j, "lp", lp.DebugString())
			}
			if i+1 == len(lp.path) {
				status = "end"
				if DoDebug {
					slog.Debug("in findSharedRootSelector(), returning end", "lp.path[:i].string()", lp.path[:i].string())
				}
				retPath = pullBackRootSelector(ctx, lp.path[:i], gqdoc, lp.count)
				return retPath
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
					status = "divergence"
					if DoDebug {
						slog.Debug("in findSharedRootSelector(), found divergence, returning", "lp.path[:i].string()", lp.path[:i].string())
					}
					retPath = pullBackRootSelector(ctx, lp.path[:i], gqdoc, lp.count)
					return retPath
				}
			}
		}
	}
	status = "nil"
	if DoDebug {
		slog.Debug("in findSharedRootSelector(), returning nil")
	}
	retPath = []node{}
	return retPath
}

// pullBackRootSelector adjusts the root selector by pulling back to a parent element if the
// selector matches more elements than expected, ensuring it targets the repeating container.
func pullBackRootSelector(ctx context.Context, rootSel path, gqdoc *fetch.Document, count int) path {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/generate").Start(ctx, fmt.Sprintf("generate.pullBackRootSelector(%d)", len(rootSel)))

	// Metering
	// status := "unknown"
	// var i int
	var ret path
	var selLen int
	defer func() {
		observability.Add(ctx, observability.Instruments.Generate, 1,
			// attribute.String("status", status),
			attribute.String("arg.root_sel", rootSel.string()),
			attribute.Int("arg.count", count),
			attribute.Int("int.sel.len", selLen),
			attribute.String("ret", ret.string()),
		)
		span.End()
	}()

	// Logging
	if DoDebug {
		slog.Debug("pullBackRootSelector()", "len(rootSel)", len(rootSel))
	}

	ret = rootSel
	prev := ret
	if len(ret) == 0 {
		return ret
	}

	// For sequential scraping with email HTML, try to find the shallowest div-ending selector
	// Email HTML often has structure like: body > ... > div > div (section containers)
	// followed by deeper nesting for individual elements.
	// We want to group at the section level, not the deep element level.

	// Collect all potential selectors, looking for div-ending selectors that are divisors of count
	var candidates []struct {
		path  path
		count int
	}
	testRet := ret
	for len(testRet) > 3 {
		testStr := testRet.string()
		testLen := gqdoc.Document.Selection.Find(testStr).Filter(testStr).Length()

		// Accept selectors where count is a multiple of testLen (more sections than fields is OK)
		// or where testLen equals count (exact match)
		if testLen > 0 && (count%testLen == 0 || testLen == count) {
			candidates = append(candidates, struct {
				path  path
				count int
			}{testRet, testLen})
		}

		testRet = testRet[:len(testRet)-1]
	}

	// Prefer the shallowest div-ending selector with a reasonable count ratio
	// A "reasonable" ratio means we don't have too many sections per field (e.g., < 20)
	for _, candidate := range candidates {
		if len(candidate.path) > 0 && candidate.path[len(candidate.path)-1].tagName == "div" {
			ratio := candidate.count / count
			if ratio == 1 || (ratio > 1 && ratio < 20) {
				slog.Info("pullBackRootSelector() found div-ending selector",
					"selector", candidate.path.string(),
					"selectorCount", candidate.count,
					"fieldCount", count,
					"ratio", ratio)
				return candidate.path
			}
		}
	}

	// If no div-ending selector found, use standard pullback logic
	for {
		retStr := ret.string()
		selLen = gqdoc.Document.Selection.Find(retStr).Filter(retStr).Length()
		if selLen == count {
			return ret
		}
		if selLen%count != 0 {
			// something went wrong
			return prev
		}
		if len(ret) == 0 {
			break
		}
		prev = ret
		ret = ret[:len(ret)-1]
	}

	return ret
}

// shortenRootSelector shortens a path by removing elements from the beginning until a threshold
// number of classes is found, reducing selector specificity.
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

var datetimeFieldThreshold = 0.25

var datetimeWeekdays = "sun|sunday|mon|monday|tue|tues|tuesday|wed|weds|wednesday|thu|thus|thursday|fri|friday|saturday|sat"
var datetimeMonths = "jan|january|feb|february|mar|march|apr|april|may|jun|june|jul|july|aug|august|sep|sept|september|oct|october|nov|november|dec|december"
var datetimeFieldRE = regexp.MustCompile(`(?i)\b(?:(?:19|20)\d{2}|` + datetimeMonths + `|` + datetimeWeekdays + `)\b`)

// processFields converts discovered location properties into scrape field definitions by determining
// field types (text, url, date) and constructing relative selectors from the root.
func processFields(ctx context.Context, exsCache map[string]string, lps []*locationProps, rootSelector path) []scrape.Field {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/generate").Start(ctx, fmt.Sprintf("generate.processFields(%d, %q)", len(lps), rootSelector.string()))

	// Metering
	var rs []scrape.Field
	// var i int
	// var j int
	// var ret path
	defer func() {
		observability.Add(ctx, observability.Instruments.Generate, 1,
			attribute.String("arg.lps", fmt.Sprintf("%# v\n", pretty.Formatter(lps))),
			// attribute.Int("arg.lps.len", len(lps)),
			// 	attribute.Int("int.i", i),
			// 	attribute.Int("int.j", j),
			attribute.String("ret", fmt.Sprintf("%# v\n", pretty.Formatter(rs))),
		)
		span.End()
	}()

	// Logging
	slog.Debug("processFields()", "len(lps)", len(lps))

	// zone, _ := time.Now().Zone()
	// zone = strings.Replace(zone, "CEST", "CET", 1) // quick hack for issue #209
	// dateField := scrape.Field{
	// 	Name:         "date",
	// 	Type:         "date",
	// 	DateLocation: zone,
	// }

	slog.Debug("in processFields()", "len(rootSelector)", len(rootSelector), "rootSelector", rootSelector.string())
	for _, lp := range lps {
		// slog.Debug("in processFields()", "e.path", e.path.string())
		// slog.Debug("in processFields()", "e.path[len(rootSelector):]", e.path[len(rootSelector):].string())
		fLoc := scrape.ElementLocation{
			Selector:      lp.path[len(rootSelector):].string(),
			// Don't set ChildIndex when using EntireSubtree - they're incompatible
			// ChildIndex:    lp.textIndex,
			Attr:          lp.attr,
			AllNodes:      true,
			EntireSubtree: true,
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
					slog.Debug("in processFields(), no datetimes match in field", "ex", ex)
					continue
				}

				slog.Debug("in processFields(), parsing field value with datetime", "ex", ex)
				// fmt.Printf("ex: %#v\n", ex)
				rngs, err := datetime.Parse(datetime.NewDateTimeForNow(), "", ex)
				if err != nil {
					exsCache[ex] = ""
					slog.Warn("parse error", "err", err)
				}
				// fmt.Printf("rngs: %#v\n", rngs)
				if datetime.HasStartMonthAndDay(rngs) {
					// fmt.Printf("rngs.Items[0].Start: %#v\n", rngs.Items[0].Start)
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
			// Required defaults to false - fields are optional by default
			// Validation of required fields happens downstream in the consumer
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

// checkAndUpdateLocProps checks if two location properties represent the same field pattern and
// merges them if they match, returning true if merged.
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

		// We require an identical set of classes to merge.
		if len(on.classes) != len(new.path[i].classes) {
			return false // Different number of classes, so they can't be identical.
		}

		if len(on.classes) > 0 {
			// Verify that the set of classes is the same.
			ovClasses := utils.IntersectionSlices(on.classes, new.path[i].classes)
			if len(ovClasses) != len(on.classes) {
				return false // The classes are not an identical set.
			}
		}

		// If we reach here, the classes are identical (or both empty).
		newNode.classes = on.classes // Use the original classes since they match.
		newPath = append(newPath, newNode)

		// ovClasses := utils.IntersectionSlices(on.classes, new.path[i].classes)
		// // If nodes have more than 0 classes, there has to be at least 1 overlapping class.
		// if len(ovClasses) > 0 {
		// 	newNode.classes = ovClasses
		// 	newPath = append(newPath, newNode)
		// } else {
		// 	return false // No overlapping classes, no overlap
		// }
	}

	// slog.Debug("in checkAndUpdateLocProps, incrementing")
	// If we get until here, there is an overlapping path.
	old.path = newPath
	old.count++
	old.examples = append(old.examples, new.examples...)
	return true
}

// filterBelowMinCount removes location properties with count below the minimum threshold.
func filterBelowMinCount(lps []*locationProps, minCount int) []*locationProps {
	var kept []*locationProps
	for _, lp := range lps {
		if lp.count < minCount {
			if DoDebug {
				slog.Debug("in filterBelowMinCount dropping", "minCount", minCount, "lp.count", lp.count, "lp.path", lp.path.string())
			}
			continue
		}
		kept = append(kept, lp)
	}
	return kept
}

// filterStaticFields removes location properties where all examples have the same value,
// keeping only fields that vary across records.
func filterStaticFields(lps []*locationProps) locationManager {
	var kept []*locationProps
	for _, lp := range lps {
		varied := false
		if DoDebug {
			slog.Debug("in filterStaticFields", "lp", lp.DebugString())
			slog.Debug("in filterStaticFields", "len(lp.examples)", len(lp.examples), "lp.examples[0]", lp.examples[0])
		}
		for i, ex := range lp.examples {
			if DoDebug {
				slog.Debug("in filterStaticFields, looking for varying", "i", i, "ex", ex)
			}
			if ex != lp.examples[0] {
				varied = true
				// break
			}
		}
		if DoDebug {
			slog.Debug("in filterStaticFields", "varied", varied)
		}
		if varied {
			kept = append(kept, lp)
		}
	}
	return kept
}

// findClusters groups field locations into clusters by extending the root selector by one level
// and grouping fields that share the same extended path.
func findClusters(ctx context.Context, lps []*locationProps, rootSelector path) map[string][]*locationProps {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/generate").Start(ctx, fmt.Sprintf("generate.findClusters(%q)", rootSelector.string()))

	// Metering
	rets := map[string][]*locationProps{}

	// status := "unknown"
	// var recs output.Records
	// var clusters map[string][]*locationProps
	// var s scrape.Scraper
	// var include bool
	defer func() {
		observability.Add(ctx, observability.Instruments.Generate, 1,

			attribute.String("lps", fmt.Sprintf("%# v\n", pretty.Formatter(lps))),
			attribute.Int("lps.len", len(lps)),
			// attribute.String("status", status),
			// // 	// attribute.String("source", source),
			// attribute.String("arg.opts", opts.configID.String()),
			// attribute.Int("arg.lps.len", len(lps)),
			// attribute.String("arg.root_selector", rootSel),
			// attribute.Int("arg.pag_props.len", len(pagProps)),
			// attribute.Int("int.recs.len", len(recs)),
			// attribute.Int("int.recs.total_fields", recs.TotalFields()),
			// attribute.Int("int.clusters.len", len(clusters)),
			// attribute.String("int.scraper", fmt.Sprintf("%#v", s)),
			// attribute.Bool("int.include", include),
			// attribute.String("rets.summary", fmt.Sprintf("%# v\n", pretty.Formatter(rets))),
			attribute.String("rets", fmt.Sprintf("%# v\n", pretty.Formatter(rets))),
			attribute.Int("rets.len", len(rets)),
			attribute.String("rets.summary", strings.Join(lo.MapToSlice(rets, func(k string, v []*locationProps) string {
				return fmt.Sprintf("%q:\n%s", k, strings.Join(lo.Map(v, func(lp *locationProps, i int) string {
					return fmt.Sprintf("--> %q: %d", lp.path.string(), len(lp.examples))
				}), "\n"))
			}), "\n")),
			// attribute.String("rets.lens", lo.MapToSlice(rets, func(k string], v []*locationProps{}) string {
			// 	return fmt.Sprintf("%q: %d", k, len(v))

			// 	len(rets)),
		)

		span.End()
	}()

	// Logging
	slog.Debug("findClusters()", "len(lps)", len(lps), "len(rootSelector)", len(rootSelector), "rootSelector", rootSelector.string())

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
		rets[lpStr] = append(rets[lpStr], lp)
		i := newLen - 1
		slog.Debug("in findClusters(), lp path node", "i", i, "lp.path.node", lp.path[i])
		slog.Debug("in findClusters(), added lp", "lpStr", lpStr)
	}
	for pStr, pByP := range rets {
		slog.Debug("in findClusters()", "pStr", pStr, "len(pByP)", len(pByP))
	}
	return rets
}
