package generate

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/jpillora/go-tld"
)

var DoPruning = true

type ConfigOptions struct {
	Batch bool
	// CacheInputDir             string
	// CacheOutputDir            string
	ConfigOutputParentDir      string
	ConfigOutputDir            string
	DoDetailPages              bool
	MinOccs                    []int
	ModelName                  string
	Offline                    bool
	OnlyKnownDomainDetailPages bool
	OnlyVaryingFields          bool
	RenderJS                   bool
	RequireDates               bool
	RequireString              string
	URL                        string
	WordsDir                   string
	configID                   scrape.ConfigID
	configPrefix               string
}

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

func ConfigurationsForPage(cache fetch.Cache, opts ConfigOptions) (map[string]*scrape.Config, error) {
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

	// fmt.Println("cache", cache)
	// fmt.Println("opts.URL", opts.URL)
	gqdoc, found, err := fetch.GetGQDocument(cache, opts.URL) //fetchGQDocument(opts, fetch.TrimURLScheme(opts.URL), map[string]*fetch.Document{})
	if err != nil {
		return nil, fmt.Errorf("failed to get page %s (found: %t): %v", opts.URL, found, err)
	}
	return ConfigurationsForGQDocument(cache, opts, gqdoc)
}

func ConfigurationsForGQDocument(cache fetch.Cache, opts ConfigOptions, gqdoc *fetch.Document) (map[string]*scrape.Config, error) {
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
		rs, err = ConfigurationsForGQDocumentWithMinOccurrence(cache, opts, gqdoc, minOcc, rs)
		if err != nil {
			return nil, err
		}
		// for k, v := range rs {
		// 	rs[k] = v
		// }
	}

	slog.Info("in ConfigurationsForPage()", "len(rs)", len(rs))
	return rs, nil
}

// func ConfigurationsForGQDocuments(opts ConfigOptions, gqdocs []*fetch.Document, minOcc int, gqdocsByURL map[string]*fetch.Document) (map[string]*scrape.Config, map[string]*fetch.Document, error) {
// 	_, gqdoc, err := joinGQDocuments(gqdocs)
// 	if err != nil {
// 		return nil, nil, err
// 	}
// 	return ConfigurationsForGQDocumentWithMinOccurrence(opts, gqdoc, minOcc, gqdocsByURL)
// }

func ConfigurationsForGQDocumentWithMinOccurrence(cache fetch.Cache, opts ConfigOptions, gqdoc *fetch.Document, minOcc int, rs map[string]*scrape.Config) (map[string]*scrape.Config, error) {
	minOccStr := fmt.Sprintf("%02da", minOcc)
	if opts.configID.Field != "" {
		opts.configID.SubID = minOccStr
	} else {
		opts.configID.ID = minOccStr
	}

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

	lps, pagProps, err := analyzePage(opts, htmlStr, minOcc)
	if err != nil {
		return nil, fmt.Errorf("error when generating configurations for GQDocument: %v", err)
	}
	if len(lps) == 0 {
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
	rs, err = expandAllPossibleConfigs(cache, exsCache, gqdoc, opts, lps, findSharedRootSelector(lps), pagProps, rs)
	if err != nil {
		return nil, err
	}

	slog.Info("in ConfigurationsForGQDocumentWithMinOccurrence()", "len(results)", len(rs))
	// fmt.Println("in ConfigurationsForGQDocumentWithMinOccurrence()", "len(results)", len(rs))
	return rs, nil
}

func expandAllPossibleConfigs(cache fetch.Cache, exsCache map[string]string, gqdoc *fetch.Document, opts ConfigOptions, lps []*locationProps, rootSelector path, pagProps []*locationProps, rs map[string]*scrape.Config) (map[string]*scrape.Config, error) {
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
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		for i, lp := range lps {
			slog.Debug("in expandAllPossibleConfigs()", "i", i, "lp", lp.DebugString())
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

	slog.Info("in expandAllPossibleConfigs()", "pags", pags)

	s := scrape.Scraper{
		Name:       opts.configID.String(),
		Paginators: pags,
		RenderJs:   opts.RenderJS,
		URL:        opts.URL,
	}

	// s.Record = shortenRootSelector(rootSelector).string()
	s.Selector = rootSelector.string()
	s.Fields = processFields(exsCache, lps, rootSelector)
	if opts.DoDetailPages && len(s.GetDetailPageURLFields()) == 0 {
		slog.Info("candidate configuration failed to find a detail page URL field, excluding", "opts.configID", opts.configID)
		return rs, nil
	}

	c := &scrape.Config{
		ID:       opts.configID,
		Scrapers: []scrape.Scraper{s},
	}

	recs, err := scrape.GQDocument(c, &s, gqdoc)
	if err != nil {
		slog.Info("candidate configuration got error scraping GQDocument, excluding", "opts.configID", opts.configID)
		return nil, err
	}
	c.Records = recs

	clusters := findClusters(lps, rootSelector)
	clusterIDs := []string{}
	for clusterID := range clusters {
		clusterIDs = append(clusterIDs, clusterID)
	}
	sort.Strings(clusterIDs)

	if slog.Default().Enabled(nil, slog.LevelInfo) {
		slog.Info("in expandAllPossibleConfigs()", "len(recs)", len(recs), "recs.TotalFields()", recs.TotalFields(), "len(clusters)", len(clusters))
	}

	include := true
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
		rs, err = expandAllPossibleConfigs(cache, exsCache, gqdoc, nextOpts, nextLPs, nextRootSel, pagProps, rs)
		if err != nil {
			return nil, err
		}
		lastID++
	}

	return rs, nil
}

func ExtendPageConfigsWithNexts(cache fetch.Cache, opts ConfigOptions, pageConfigs map[string]*scrape.Config) error {
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
		if err := ExtendPageConfigRecordsWithNext(cache, opts, pageConfigs[id], fetch.NewSelection(gqdoc.Document.Selection)); err != nil {
			return fmt.Errorf("error extending page config records with next page records: %v", err)
		}
	}
	return nil
}

func ExtendPageConfigRecordsWithNext(cache fetch.Cache, opts ConfigOptions, pageC *scrape.Config, sel *fetch.Selection) error {
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

		recs, err := scrape.GQDocument(pageC, &pageS, nextGQDoc)
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
}

var KnownDomains = map[string]bool{
	"ticketweb": true,
	"dice":      true,
}

func ConfigurationsForAllDetailPages(cache fetch.Cache, opts ConfigOptions, pageConfigs map[string]*scrape.Config) (map[string]*scrape.Config, error) {
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
		return nil, fmt.Errorf("error parsing input url %q: %v", opts.URL, err)
	}

	pageJoinsByFieldName := map[string][]*pageJoin{}
	fieldURLsByFieldName := map[string][]string{}
	for _, pageC := range pageConfigs {
		// pageCIDs = append(pageCIDs, pageC.ID.String())
		pageS := pageC.Scrapers[0]
		// fmt.Printf("found %d detail page URL fields\n", len(s.GetDetailPageURLFields()))
		for _, pageF := range pageS.GetDetailPageURLFields() {
			pj := &pageJoin{config: pageC}
			pageJoinsByFieldName[pageF.Name] = append(pageJoinsByFieldName[pageF.Name], pj)
			for _, pageIM := range pageC.Records {
				// Not sure why we need this...
				if pageIM[pageF.Name] == "" {
					continue
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

				absURL, err := tld.Parse(uBase.ResolveReference(relURL).String())
				if err != nil {
					slog.Error("error parsing detail page absolute url", "fj.value", fj.value, "err", err)
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
				if opts.OnlyKnownDomainDetailPages && !(uBase.Domain == absURL.Domain || KnownDomains[absURL.Domain]) {
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

	subURLs := pageJoinsURLs(pageJoinsByFieldName)
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

	fnames := []string{}
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
		rs, err = ConfigurationsForDetailPages(cache, opts, pjs, rs)
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
func ConfigurationsForDetailPages(cache fetch.Cache, opts ConfigOptions, pjs []*pageJoin, rs map[string]*scrape.Config) (map[string]*scrape.Config, error) {
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
	cs, err := ConfigurationsForGQDocument(cache, opts, gqdoc)
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
			if err := scrape.DetailPages(cache, mergedC, &subScraper, mergedC.Records, domain); err != nil {
				// fmt.Printf("skipping generating configuration for detail pages for merged config %q: %v\n", mergedC.ID, err)
				slog.Info("skipping generating configuration for detail pages for merged config with error", "mergedC.ID", mergedC.ID, "err", err)
				continue
			}

			if DoPruning && len(mergedC.Records) < 2 {
				slog.Info("candidate detail page configuration failed to produce more than one record, pruning", "opts.configID", opts.configID)
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
