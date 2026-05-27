package generate

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/observability"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scrape"
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
	// Walk DOM paths in lockstep, merging nodes via class intersection.
	// mergedPath accumulates the shared structural path with intersected classes.
	var mergedPath path
	for i = 0; ; i++ {
		if DoDebug {
			slog.Debug("in findSharedRootSelector()", "i", i)
		}
		var merged node
		var lp *locationProps
		for j, lp = range lps {
			if DoDebug {
				slog.Debug("in findSharedRootSelector()", "  j", j, "lp", lp.DebugString())
			}
			if i+1 == len(lp.path) {
				status = "end"
				if DoDebug {
					slog.Debug("in findSharedRootSelector(), returning end", "mergedPath", mergedPath.string())
				}
				retPath = pullBackRootSelector(ctx, mergedPath, gqdoc, lp.count)
				return retPath
			}

			if j == 0 {
				merged = lp.path[i]
			} else {
				// Structural match: same tag + overlapping classes → merge via intersection.
				matched, m := merged.structuralMatch(lp.path[i])
				if !matched {
					status = "divergence"
					if DoDebug {
						slog.Debug("in findSharedRootSelector(), found divergence, returning", "mergedPath", mergedPath.string())
					}
					retPath = pullBackRootSelector(ctx, mergedPath, gqdoc, lp.count)
					return retPath
				}
				merged = m
			}
		}
		mergedPath = append(mergedPath, merged)
	}
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
			Selector: lp.path[len(rootSelector):].string(),
			// Don't set ChildIndex - it's incompatible with the default EntireSubtree behavior
			// ChildIndex: lp.textIndex,
			Attr: lp.attr,
			// EntireSubtree and AllNodes default to true at runtime (see getTextString)
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
				now := datetime.NewDateTimeForNow()
				rngs, err := datetime.Parse(ex, datetime.ParseOptions{
					MinDateTime:     now,
					DefaultLocation: time.Local,
					DefaultYear:     now.Date.Year,
				})
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
	// Pre-compute: for each path (ignoring nth-child), count total occurrences.
	// This handles patterns that repeat across parallel subtrees (e.g., event cards
	// inside multiple profile pages in a concatenated document). The per-parent
	// nth-child index may be small (1-5) but the total count across the document
	// is large (80+). We strip nth-child when the total count >= minOcc.
	pathCounts := countPathsIgnoringNthChild(l)

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
			stripNthChild(lp, minOcc, pathCounts)
			squashed = append(squashed, lp)
		}
	}
	return squashed
}

// countPathsIgnoringNthChild counts how many locationProps share the same path
// when all nth-child pseudo classes are removed. This gives the total document-wide
// occurrence count for each structural pattern.
func countPathsIgnoringNthChild(l locationManager) map[string]int {
	counts := map[string]int{}
	for _, lp := range l {
		key := pathStringWithoutNthChild(lp.path)
		counts[key]++
	}
	return counts
}

// pathStringWithoutNthChild returns the path string with all nth-child removed.
func pathStringWithoutNthChild(p path) string {
	var parts []string
	for _, n := range p {
		s := n.tagName
		for _, c := range n.classes {
			s += "." + c
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " > ")
}

// stripNthChild removes nth-child pseudo classes from path segments that represent
// repeating patterns. Uses two criteria (either sufficient):
//  1. The nth-child INDEX is >= minOcc (original heuristic: high index = many siblings)
//  2. The total document-wide count of this path (ignoring nth-child) is >= minOcc
//     (handles patterns in parallel subtrees, e.g., event cards across concatenated pages)
func stripNthChild(lps *locationProps, minOcc int, pathCounts map[string]int) {
	// Check if this entire path pattern repeats >= minOcc times across the document.
	// If so, strip ALL nth-child pseudo classes — the pattern is clearly repeating.
	totalCount := pathCounts[pathStringWithoutNthChild(lps.path)]
	if totalCount >= minOcc {
		for i := range lps.path {
			if len(lps.path[i].pseudoClasses) > 0 {
				lps.path[i].pseudoClasses = []string{}
				if lps.iStrip == 0 {
					lps.iStrip = i
				}
			}
		}
		return
	}

	// Original heuristic: strip nth-child where the index >= minOcc.
	iStrip := 0
	sub := 1
	if minOcc < 6 {
		sub = 2
	}
	for i := len(lps.path) - sub; i >= 0; i-- {
		if i < iStrip {
			lps.path[i].pseudoClasses = []string{}
		} else if len(lps.path[i].pseudoClasses) > 0 {
			ncIndex, _ := strconv.Atoi(strings.Replace(strings.Split(lps.path[i].pseudoClasses[0], "(")[1], ")", "", 1))
			if ncIndex >= minOcc {
				lps.path[i].pseudoClasses = []string{}
				iStrip = i
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

		// Strip known auto-generated CMS classes (post-NNNNN, fl-builder-content-NNNN),
		// then merge via class intersection. This handles both WordPress per-page IDs
		// and arbitrary varying classes (e.g., event_listing_category-*) by keeping
		// only the classes shared across all pages at this structural position.
		oldFiltered := filterAutoGeneratedClasses(on.classes)
		newFiltered := filterAutoGeneratedClasses(new.path[i].classes)

		if len(oldFiltered) == 0 && len(newFiltered) == 0 {
			newPath = append(newPath, newNode)
			continue
		}

		// Intersect class lists: keep only classes shared between old and new.
		// Require that the shared classes cover a majority of at least one
		// input list — this prevents merging genuinely different elements
		// (e.g., header vs footer) that happen to share a single utility class.
		sharedClasses := intersectStrings(oldFiltered, newFiltered)
		if len(sharedClasses) == 0 {
			return false
		}
		if 2*len(sharedClasses) <= len(oldFiltered) && 2*len(sharedClasses) <= len(newFiltered) {
			return false // Overlap is minority of both lists — structurally different
		}
		newNode.classes = sharedClasses
		newPath = append(newPath, newNode)
	}

	// slog.Debug("in checkAndUpdateLocProps, incrementing")
	// If we get until here, there is an overlapping path.
	old.path = newPath
	old.count++
	old.examples = append(old.examples, new.examples...)
	return true
}

// autoGeneratedClassRE matches CMS-generated per-page CSS classes that vary across
// pages of the same template. Stripping these before comparison allows goskyr to
// merge location props from different pages that share the same structural template.
//
// Patterns matched:
//   - WordPress: post-123, postid-456, page-id-789, attachment-789
//   - WooCommerce: product_cat-*, product_tag-*, product-type-*
//   - Beaver Builder: fl-builder-content-1234 (template content IDs)
//   - Generic: *-id-123, *-post-123, any class that is ONLY digits
var autoGeneratedClassRE = regexp.MustCompile(
	`^(?:` +
		`post-\d+` + `|` + // WordPress post-NNNNN
		`postid-\d+` + `|` + // WordPress postid-NNNNN
		`page-id-\d+` + `|` + // WordPress page-id-NNNNN
		`attachment-\d+` + `|` + // WordPress attachment-NNNNN
		`fl-builder-content-\d+` + `|` + // Beaver Builder content IDs
		`\d+` + // Pure numeric classes
		`)$`)

// filterAutoGeneratedClasses returns classes with CMS-generated per-page classes removed.
func filterAutoGeneratedClasses(classes []string) []string {
	var filtered []string
	for _, c := range classes {
		if !autoGeneratedClassRE.MatchString(c) {
			filtered = append(filtered, c)
		}
	}
	return filtered
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

	// Group fields by structural match at the cluster position (rootSelector+1).
	// Fields whose nodes at this depth have the same tag and overlapping classes
	// are grouped together. This prevents category-specific classes from splitting
	// one logical cluster into many per-category clusters.
	type clusterGroup struct {
		merged node
		lps    []*locationProps
	}
	var groups []clusterGroup

	for _, lp := range lps {
		slog.Debug("in findClusters()", "lp", lp.DebugString())
		if newLen > len(lp.path) {
			continue
		}
		clusterNode := lp.path[newLen-1]

		// Find an existing group this node structurally matches
		matched := false
		for gi := range groups {
			if ok, m := groups[gi].merged.structuralMatch(clusterNode); ok {
				groups[gi].merged = m
				groups[gi].lps = append(groups[gi].lps, lp)
				matched = true
				break
			}
		}
		if !matched {
			groups = append(groups, clusterGroup{merged: clusterNode, lps: []*locationProps{lp}})
		}
	}

	// Build the return map keyed by the final merged selector string
	for _, g := range groups {
		key := appendPath(rootSelector, g.merged).string()
		rets[key] = g.lps
		slog.Debug("in findClusters()", "key", key, "len(lps)", len(g.lps))
	}
	return rets
}
