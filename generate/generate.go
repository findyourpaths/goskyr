package generate

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/scrape"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/gosimple/slug"
)

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
	RequireString   string
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
	if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_ConfigurationsForPage_log.txt"), slog.LevelDebug)
		if err != nil {
			return nil, nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Info("ConfigurationsForPage()", "opts", opts)
	defer slog.Info("ConfigurationsForPage() returning")

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
	if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_ConfigurationsForGQDocument_log.txt"), slog.LevelDebug)
		if err != nil {
			return nil, nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Info("ConfigurationsForGQDocument()")
	defer slog.Info("ConfigurationsForGQDocument() returning")

	htmlStr, err := goquery.OuterHtml(gqdoc.Children())
	if err != nil {
		return nil, nil, fmt.Errorf("error when generating configurations for GQDocument: %v", err)
	}

	locPropsSel, pagProps, err := analyzePage(opts, htmlStr, minOcc)
	if err != nil {
		return nil, nil, fmt.Errorf("error when generating configurations for GQDocument: %v", err)
	}
	if len(locPropsSel) == 0 {
		// No fields were found, so just return.
		return nil, gqdocsByURL, nil
	}

	// slog.Debug("in ConfigurationsForGQDocument, before expanding", "len(a.LocMan)", len(a.LocMan))
	slog.Debug("in ConfigurationsForGQDocument, before expanding", "len(locPropsSel)", len(locPropsSel))
	slog.Debug("in ConfigurationsForGQDocument, before expanding", "len(pagProps)", len(pagProps))

	minOccStr := fmt.Sprintf("%02da", minOcc)
	if opts.configID.Field != "" {
		opts.configID.SubID = minOccStr
	} else {
		opts.configID.ID = minOccStr
	}
	rs := map[string]*scrape.Config{}

	// FIXME
	// if !opts.DoSubpages {
	pagProps = []*locationProps{}
	// }

	if err := expandAllPossibleConfigs(gqdoc, opts, locPropsSel, nil, "", pagProps, rs); err != nil {
		return nil, nil, err
	}

	slog.Debug("in ConfigurationsForGQDocument()", "len(rs)", len(rs))
	return rs, gqdocsByURL, nil
}

func expandAllPossibleConfigs(gqdoc *goquery.Document, opts ConfigOptions, locPropsSel []*locationProps, parentRootSelector path, parentItemsStr string, pagProps []*locationProps, results map[string]*scrape.Config) error {
	// if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
	// 	prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_expandAllPossibleConfigs_log.txt"), slog.LevelDebug)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	defer output.RestoreDefaultLogger(prevLogger)
	// }
	// slog.Debug("expandAllPossibleConfigs()")
	// defer slog.Debug("expandAllPossibleConfigs() returning")

	slog.Info("in expandAllPossibleConfigs()", "opts.configID", opts.configID.String())

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

	slog.Info("in expandAllPossibleConfigs()", "pags", pags)

	s := scrape.Scraper{
		Name:       opts.configID.String(),
		RenderJs:   opts.RenderJS,
		URL:        opts.URL,
		Paginators: pags,
	}

	rootSelector := findSharedRootSelector(locPropsSel)
	// s.Item = shortenRootSelector(rootSelector).string()
	s.Item = rootSelector.string()
	s.Fields = processFields(locPropsSel, rootSelector)
	if opts.DoSubpages && len(s.GetSubpageURLFields()) == 0 {
		slog.Info("candidate configuration failed to find a subpage URL field, excluding", "opts.configID", opts.configID)
		return nil
	}

	c := &scrape.Config{
		ID:       opts.configID,
		Scrapers: []scrape.Scraper{s},
	}

	items, err := scrape.GQDocument(c, &s, gqdoc, true)
	if err != nil {
		return err
	}
	c.ItemMaps = items

	if slog.Default().Enabled(nil, slog.LevelDebug) {
		slog.Debug("in expandAllPossibleConfigs()", "len(items)", len(items), "items.TotalFields()", items.TotalFields())
	}

	itemsStr := items.String()
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

	if scrape.DoPruning && itemsStr == parentItemsStr {
		slog.Info("candidate configuration failed to produce different items from parent, excluding", "opts.configID", opts.configID)
		return nil
	}

	// fmt.Printf("strings.Index(itemsStr, opts.RequireString): %d\n", strings.Index(itemsStr, opts.RequireString))
	if opts.RequireString != "" && strings.Index(itemsStr, opts.RequireString) == -1 {
		slog.Info("candidate configuration failed to extract the required string, excluding", "opts.configID", opts.configID, "opts.RequireString", opts.RequireString, "itemsStr", itemsStr)
		return nil
	}

	results[opts.configID.String()] = c
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

		items, err := scrape.GQDocument(pageC, &pageS, nextGQDoc, true)
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
	if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_ConfigurationsForAllSubpages_log.txt"), slog.LevelDebug)
		if err != nil {
			return nil, nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Info("ConfigurationsForAllSubpages()")
	defer slog.Info("ConfigurationsForAllSubpages() returning")

	slog.Info("in ConfigurationsForAllSubpages()", "opts.URL", opts.URL)
	slog.Info("in ConfigurationsForAllSubPages()", "opts.ConfigOutputDir", opts.ConfigOutputDir)
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
	if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
		prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_ConfigurationsForSubpages_log.txt"), slog.LevelDebug)
		if err != nil {
			return nil, nil, err
		}
		defer output.RestoreDefaultLogger(prevLogger)
	}
	slog.Info("ConfigurationsForSubpages()", "opts", opts)
	defer slog.Info("ConfigurationsForSubpages() returning")

	gqdoc, err := joinPageJoinsGQDocuments(opts, pjs, gqdocsByURL)
	if err != nil {
		return nil, nil, err
	}
	// Prepare for calling general page generator.
	opts.DoSubpages = false
	opts.RequireString = ""
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
				slog.Info("skipping generating configuration for subpages for merged config", "mergedC.ID", mergedC.ID.String(), "err", err)
				continue
			}
			rs[mergedC.ID.String()] = mergedC
		}
	}

	slog.Debug("in ConfigurationsForAllSubpages()", "len(rs)", len(rs))
	return rs, gqdocsByURL, nil
}

func fetchGQDocument(opts ConfigOptions, u string, gqdocsByURL map[string]*goquery.Document) (*goquery.Document, map[string]*goquery.Document, error) {
	// if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
	// 	prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_fetchGQDocument_log.txt"), slog.LevelDebug)
	// 	if err != nil {
	// 		return nil, nil, err
	// 	}
	// 	defer output.RestoreDefaultLogger(prevLogger)
	// }
	slog.Info("fetchGQDocument()", "u", u)
	slog.Info("fetchGQDocument()", "len(gqdocsByURL)", len(gqdocsByURL))
	defer slog.Info("fetchGQDocument() returning")

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
		slog.Debug("fetchGQDocument(), looking on disk at", "cacheInPath", cacheInPath)
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
