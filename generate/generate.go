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
	"github.com/gosimple/slug"
	"github.com/jpillora/go-tld"
)

type ConfigOptions struct {
	Batch                     bool
	CacheInputDir             string
	CacheOutputDir            string
	ConfigOutputDir           string
	DoDetailPages             bool
	MinOccs                   []int
	ModelName                 string
	Offline                   bool
	OnlySameDomainDetailPages bool
	OnlyVaryingFields         bool
	RequireString             string
	RenderJS                  bool
	URL                       string
	WordsDir                  string
	configID                  scrape.ConfigID
	configPrefix              string
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
		opts.CacheInputDir = filepath.Join(opts.CacheInputDir, prefix)
	}

	if opts.CacheOutputDir != "" {
		opts.CacheOutputDir = filepath.Join(opts.CacheOutputDir, prefix)
	}

	if opts.ConfigOutputDir != "" {
		opts.ConfigOutputDir = filepath.Join(opts.ConfigOutputDir, prefix+"_configs")
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

	gqdoc, found, err := fetch.GetGQDocument(cache, opts.URL) //fetchGQDocument(opts, fetch.TrimURLScheme(opts.URL), map[string]*goquery.Document{})
	if !found || err != nil {
		return nil, fmt.Errorf("failed to get page %s (found: %t): %v", opts.URL, found, err)
	}
	return ConfigurationsForGQDocument(cache, opts, gqdoc)
}

func ConfigurationsForGQDocument(cache fetch.Cache, opts ConfigOptions, gqdoc *goquery.Document) (map[string]*scrape.Config, error) {
	var cims map[string]*scrape.Config
	var err error
	rs := map[string]*scrape.Config{}
	// Generate configs for each of the minimum occs.
	for _, minOcc := range opts.MinOccs {
		slog.Debug("calling ConfigurationsForGQDocument()", "minOcc", minOcc)
		cims, err = ConfigurationsForGQDocumentWithMinOccurrence(cache, opts, gqdoc, minOcc)
		if err != nil {
			return nil, err
		}
		for k, v := range cims {
			rs[k] = v
		}
	}

	slog.Debug("in ConfigurationsForPage()", "len(rs)", len(rs))
	return rs, nil
}

// func ConfigurationsForGQDocuments(opts ConfigOptions, gqdocs []*goquery.Document, minOcc int, gqdocsByURL map[string]*goquery.Document) (map[string]*scrape.Config, map[string]*goquery.Document, error) {
// 	_, gqdoc, err := joinGQDocuments(gqdocs)
// 	if err != nil {
// 		return nil, nil, err
// 	}
// 	return ConfigurationsForGQDocumentWithMinOccurrence(opts, gqdoc, minOcc, gqdocsByURL)
// }

func ConfigurationsForGQDocumentWithMinOccurrence(cache fetch.Cache, opts ConfigOptions, gqdoc *goquery.Document, minOcc int) (map[string]*scrape.Config, error) {
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
	slog.Info("ConfigurationsForGQDocument()")
	defer slog.Info("ConfigurationsForGQDocument() returning")

	htmlStr, err := goquery.OuterHtml(gqdoc.Children())
	if err != nil {
		return nil, fmt.Errorf("error when generating configurations for GQDocument: %v", err)
	}

	lps, pagProps, err := analyzePage(opts, htmlStr, minOcc)
	if err != nil {
		return nil, fmt.Errorf("error when generating configurations for GQDocument: %v", err)
	}
	if len(lps) == 0 {
		// No fields were found, so just return.
		return nil, nil
	}

	// slog.Debug("in ConfigurationsForGQDocument, before expanding", "len(a.LocMan)", len(a.LocMan))
	slog.Debug("in ConfigurationsForGQDocument, before expanding", "len(lps)", len(lps))
	slog.Debug("in ConfigurationsForGQDocument, before expanding", "len(pagProps)", len(pagProps))
	rs := map[string]*scrape.Config{}

	// FIXME
	// if !opts.DoDetailPages {
	pagProps = []*locationProps{}
	// }

	if err := expandAllPossibleConfigs(gqdoc, opts, lps, findSharedRootSelector(lps), "", pagProps, rs); err != nil {
		return nil, err
	}

	slog.Debug("in ConfigurationsForGQDocument()", "len(rs)", len(rs))
	return rs, nil
}

func expandAllPossibleConfigs(gqdoc *goquery.Document, opts ConfigOptions, lps []*locationProps, rootSelector path, parentRecsStr string, pagProps []*locationProps, results map[string]*scrape.Config) error {
	if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_expandAllPossibleConfigs_log.txt"), slog.LevelDebug)
		if err != nil {
			return err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Info("expandAllPossibleConfigs()")
	defer slog.Info("expandAllPossibleConfigs() returning")

	slog.Info("in expandAllPossibleConfigs()", "opts.configID", opts.configID.String(), "len(lps)", len(lps))
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
		RenderJs:   opts.RenderJS,
		URL:        opts.URL,
		Paginators: pags,
	}

	// s.Record = shortenRootSelector(rootSelector).string()
	s.Selector = rootSelector.string()
	s.Fields = processFields(lps, rootSelector)
	if opts.DoDetailPages && len(s.GetDetailPageURLFields()) == 0 {
		slog.Info("candidate configuration failed to find a detail page URL field, excluding", "opts.configID", opts.configID)
		return nil
	}

	c := &scrape.Config{
		ID:       opts.configID,
		Scrapers: []scrape.Scraper{s},
	}

	recs, err := scrape.GQDocument(c, &s, gqdoc, true)
	if err != nil {
		return err
	}
	c.Records = recs

	if slog.Default().Enabled(nil, slog.LevelDebug) {
		slog.Debug("in expandAllPossibleConfigs()", "len(recs)", len(recs), "recs.TotalFields()", recs.TotalFields())
	}

	recsStr := recs.String()
	clusters := findClusters(lps, rootSelector)
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
		nextLPs := clusters[clusterID]
		nextRootSel := clusters[clusterID][0].path[0 : len(rootSelector)+1]
		if err := expandAllPossibleConfigs(gqdoc, nextOpts, nextLPs, nextRootSel, recsStr, pagProps, results); err != nil {
			return err
		}
		lastID++
	}

	if scrape.DoPruning && recsStr == parentRecsStr {
		slog.Info("candidate configuration failed to produce different records from parent, excluding", "opts.configID", opts.configID)
		return nil
	}

	// fmt.Printf("strings.Index(itemsStr, opts.RequireString): %d\n", strings.Index(itemsStr, opts.RequireString))
	if opts.RequireString != "" && strings.Index(recsStr, opts.RequireString) == -1 {
		slog.Info("candidate configuration failed to extract the required string, excluding", "opts.configID", opts.configID) //, "opts.RequireString", opts.RequireString, "recsStr", recsStr)
		return nil
	}

	results[opts.configID.String()] = c
	return nil
}

func ExtendPageConfigsWithNexts(cache fetch.Cache, opts ConfigOptions, pageConfigs map[string]*scrape.Config) error {
	pageCIDs := []string{}
	for _, pageC := range pageConfigs {
		pageCIDs = append(pageCIDs, pageC.ID.String())
	}

	gqdoc, found, err := fetch.GetGQDocument(cache, opts.URL)
	if !found || err != nil {
		return fmt.Errorf("failed to get next page %s: %v", opts.URL, err)
	}

	// path := filepath.Join(opts.InputDir, fetch.MakeURLStringSlug(opts.URL)+".html")
	// f := &fetch.FileFetcher{}
	// gqdoc, err := fetch.GQDocument(f, "file://"+path, nil)
	// // fmt.Printf("adding subURL: %q\n", subURL)
	// if err != nil {
	// 	return nil, fmt.Errorf("error fetching page at: %v", err)
	// }

	for _, id := range pageCIDs {
		if err := ExtendPageConfigRecordsWithNext(opts, pageConfigs[id], gqdoc.Selection); err != nil {
			return fmt.Errorf("error extending page config records with next page records: %v", err)
		}
	}
	return nil
}

func ExtendPageConfigRecordsWithNext(opts ConfigOptions, pageC *scrape.Config, sel *goquery.Selection) error {
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

	// Scrape records for the proposed next pages.
	// f := &fetch.FileFetcher{}
	newPags := []scrape.Paginator{}
	for uStr, pag := range uStrsMap {
		nextGQDoc := gqdocsByURL[uStr]
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

		recs, err := scrape.GQDocument(pageC, &pageS, nextGQDoc, true)
		if err != nil {
			return err
		}
		// fmt.Printf("found %d records\n", len(records))

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
		rs = append(rs, u)
	}
	sort.Strings(rs)
	return rs
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
	slog.Info("in ConfigurationsForAllDetailPages()", "opts.ConfigOutputDir", opts.ConfigOutputDir)
	slog.Debug("in ConfigurationsForAllDetailPages()", "opts", opts)

	pageCIDs := []string{}
	for _, pageC := range pageConfigs {
		pageCIDs = append(pageCIDs, pageC.ID.String())
	}

	uBase, err := tld.Parse(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("error parsing input url %q: %v", opts.URL, err)
	}

	pageJoinsByFieldName := map[string][]*pageJoin{}
	for _, pageC := range pageConfigs {
		pageCIDs = append(pageCIDs, pageC.ID.String())
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
				if absURL.Scheme != "http" && absURL.Scheme != "https" {
					slog.Debug("skipping sub URL with non-http(s) scheme", "fj.value", fj.value)
					continue
				}

				if opts.OnlySameDomainDetailPages {
					if uBase.Domain != absURL.Domain {
						slog.Debug("skipping sub URL with different domain", "uBase", uBase, "fj.value", fj.value)
						continue
					}
				}

				fj.url = fetch.TrimURLScheme(absURL.String())
				// fmt.Printf("adding fj: %#v\n", fj)
				pj.fieldJoins = append(pj.fieldJoins, fj)
			}
		}
	}
	sort.Strings(pageCIDs)

	subURLs := pageJoinsURLs(pageJoinsByFieldName)
	if opts.ConfigOutputDir != "" {
		urlsPath := filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_urls.txt")
		if err := utils.WriteStringFile(urlsPath, strings.Join(subURLs, "\n")); err != nil {
			return nil, fmt.Errorf("failed to write detail page URLs list: %v", err)
		}
	}
	slog.Debug("in ConfigurationsForAllDetailPages()", "opts.CacheInputDir", opts.CacheInputDir)

	var cs map[string]*scrape.Config
	rs := map[string]*scrape.Config{}
	for fname, pjs := range pageJoinsByFieldName {
		opts.configID.Field = fname
		cs, err = ConfigurationsForDetailPages(cache, opts, pjs)
		if err != nil {
			return nil, fmt.Errorf("error generating configuration for detail pages for field %q: %v", fname, err)
		}
		for id, c := range cs {
			rs[id] = c
		}
	}

	slog.Debug("in ConfigurationsForAllDetailPages()", "len(rs)", len(rs))
	return rs, nil
}

// ConfigurationsForDetailPages collects the URL values for a candidate detail
// page field, retrieves the pages at those URLs, concatenates them, trains a
// scraper to extract from those detail pages, and merges the resulting records
// into the parent page, outputting the result.
func ConfigurationsForDetailPages(cache fetch.Cache, opts ConfigOptions, pjs []*pageJoin) (map[string]*scrape.Config, error) {
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
	rs := map[string]*scrape.Config{}
	// if fetchFn == nil {
	// 	fetchFn = func(u string) (*goquery.Document, error) {
	// 		u = fetch.TrimURLScheme(u)
	// 		r := gqdocsByURL[u]
	// 		if r == nil {
	// 			return nil, fmt.Errorf("didn't find %q", u)
	// 		}
	// 		return r, nil
	// 	}
	// }

	// slog.Debug("in ConfigurationsForDetailPages()", "mergedCConfigBase", mergedCConfigBase)
	for _, c := range cs {
		slog.Debug("looking at", "c.ID", c.ID)
		rs[c.ID.String()] = c
		subScraper := c.Scrapers[0]
		subScraper.Selector = strings.TrimPrefix(subScraper.Selector, "body > htmls > ")

		for _, pj := range pjs {
			slog.Debug("looking at", "pj.config.ID", pj.config.ID.String())

			mergedC := pj.config.Copy()
			mergedC.ID.Field = opts.configID.Field
			mergedC.ID.SubID = c.ID.SubID
			mergedC.Scrapers = append(mergedC.Scrapers, subScraper)

			if err := scrape.DetailPages(cache, mergedC, &subScraper, mergedC.Records); err != nil {
				// fmt.Printf("skipping generating configuration for detail pages for merged config %q: %v\n", mergedC.ID.String(), err)
				slog.Info("skipping generating configuration for detail pages for merged config", "mergedC.ID", mergedC.ID.String(), "err", err)
				continue
			}
			rs[mergedC.ID.String()] = mergedC
		}
	}

	slog.Debug("in ConfigurationsForAllDetailPages()", "len(rs)", len(rs))
	return rs, nil
}

func joinPageJoinsGQDocuments(cache fetch.Cache, opts ConfigOptions, pjs []*pageJoin) (*goquery.Document, error) {
	// Get all URLs appearing in the values of the fields with this name in the parent pages.
	us := pageJoinsURLs(map[string][]*pageJoin{"": pjs})
	if opts.ConfigOutputDir != "" {
		usPath := filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_urls.txt")
		if err := utils.WriteStringFile(usPath, strings.Join(us, "\n")); err != nil {
			return nil, fmt.Errorf("error writing detail page URLs page to %q: %v", usPath, err)
		}
	}

	key := "http://" + opts.configID.String() + ".html"
	r, found, err := fetch.GetGQDocument(cache, key)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch joined page %q: %v", key, err)
	}
	str := ""
	if !found {
		// Concatenate all of the detail pages pointed to by the field with this name in the parent pages.
		gqdocs := []*goquery.Document{}
		fmt.Printf("us: %#v\n", us)
		for _, u := range us {
			gqdoc, found, err := fetch.GetGQDocument(cache, "http://"+u)
			if !found || err != nil {
				return nil, fmt.Errorf("failed to fetch page to join %q (found: %t): %v", u, found, err)
			}
			gqdocs = append(gqdocs, gqdoc)
		}

		str, r, err = joinGQDocuments(gqdocs) // ./opts, us, gqdocsByURL)
		if err != nil {
			return nil, err
		}
	}

	if opts.CacheOutputDir != "" {
		fetch.SetGQDocument(cache, key, str)
	}
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
	return r, nil
}

// func joinGQDocuments(opts ConfigOptions, us []string, gqdocsByURL map[string]*goquery.Document) (string, *goquery.Document, error) {

func joinGQDocuments(gqdocs []*goquery.Document) (string, *goquery.Document, error) {
	rs := strings.Builder{}
	rs.WriteString("<htmls>\n")

	var gqdoc *goquery.Document
	var err error
	for _, gqdoc := range gqdocs {
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
