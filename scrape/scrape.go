package scrape

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/jsonquery"
	"github.com/findyourpaths/goskyr/date"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/findyourpaths/phil/datetime"
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/jpillora/go-tld"
	"golang.org/x/net/html"
	"gopkg.in/yaml.v3"
)

var DoDebug = true

// var DoDebug = false

var DebugGQFind = true

// var DebugGQFind = false

func init() {
	utils.WriteStringFile("/tmp/goskyr/main/scrape_Page_log.txt", "")
	utils.WriteStringFile("/tmp/goskyr/main/scrape_GQDocument_log.txt", "")
	utils.WriteStringFile("/tmp/goskyr/main/scrape_GQSelection_log.txt", "")
}

// GlobalConfig is used for storing global configuration parameters that
// are needed across all scrapers
type GlobalConfig struct {
	UserAgent string `yaml:"user-agent"`
}

// Config defines the overall structure of the scraper configuration.
// Values will be taken from a config yml file or environment variables
// or both.
type Config struct {
	ID       ConfigID
	Writer   output.WriterConfig `yaml:"writer,omitempty"`
	Scrapers []Scraper           `yaml:"scrapers,omitempty"`
	Global   GlobalConfig        `yaml:"global,omitempty"`
	Records  output.Records
}

type ConfigID struct {
	Slug  string
	ID    string
	Field string
	SubID string
}

func (cid ConfigID) String() string {
	// slog.Debug(fmt.Sprintf("ConfigID.String(): cid %#v\n", cid))
	// fmt.Printf("ConfigID.String(): cid %#v\n", cid)
	rb := strings.Builder{}

	// if cid.Base != "" {
	// 	r.WriteString(cid.Base)
	// }
	if cid.Slug != "" {
		rb.WriteString(cid.Slug)
	}

	sep := "__"
	if cid.ID != "" {
		rb.WriteString(sep + cid.ID)
		sep = "_"
	}
	if cid.Field != "" {
		rb.WriteString(sep + cid.Field)
		sep = "_"
	}
	if cid.SubID != "" {
		rb.WriteString(sep + cid.SubID)
		sep = "_"
	}

	r := rb.String()
	// slog.Debug(fmt.Sprintf("ConfigID.String() returning %q\n", r))
	// fmt.Printf("ConfigID.String() returning %q\n", r)
	return r
}

func (c Config) Copy() *Config {
	r := c
	r.Records = output.Records{}
	for _, im := range c.Records {
		rim := output.Record{}
		for k, v := range im {
			rim[k] = v
		}
		r.Records = append(r.Records, rim)
	}
	return &r
}

func (c Config) String() string {
	cCopy := c
	cCopy.Records = nil
	yamlData, err := yaml.Marshal(&cCopy)
	if err != nil {
		log.Fatalf("error while marshaling config. %v", err)
	}
	return string(yamlData)
}

func (c Config) WriteToFile(dir string) error {
	// fmt.Printf("WriteToFile(dir: %q) %q\n", dir, c.ID.String())
	if err := utils.WriteStringFile(filepath.Join(dir, c.ID.String()+".yml"), c.String()); err != nil {
		return err
	}
	if len(c.Records) > 0 {
		if err := utils.WriteStringFile(filepath.Join(dir, c.ID.String()+".json"), c.Records.String()); err != nil {
			return err
		}
	}
	return nil
}

func ReadConfig(configPath string) (*Config, error) {
	var config Config
	fileInfo, err := os.Stat(configPath)
	if err != nil {
		return nil, err
	}
	if fileInfo.IsDir() {
		err := filepath.WalkDir(configPath, func(path string, d fs.DirEntry, err error) error {
			if !d.IsDir() {
				var configTmp Config
				if err := cleanenv.ReadConfig(path, &configTmp); err != nil {
					return err
				}
				config.Scrapers = append(config.Scrapers, configTmp.Scrapers...)
				if configTmp.Writer.Type != "" {
					if config.Writer.Type == "" {
						config.Writer = configTmp.Writer
					} else {
						return fmt.Errorf("config files must only contain max. one writer")
					}
				}
			}
			return nil // skipping everything that is not a file
		})
		if err != nil {
			return nil, err
		}
	} else {
		if err := cleanenv.ReadConfig(configPath, &config); err != nil {
			return nil, err
		}
	}
	if config.Writer.Type == "" {
		config.Writer.Type = output.STDOUT_WRITER_TYPE
	}
	if config.Global.UserAgent == "" {
		config.Global.UserAgent = "goskyr web scraper (github.com/findyourpaths/goskyr)"
	}

	// for _, s := range config.Scrapers {
	// 	u, err := url.Parse(s.URL)
	// 	if err != nil {
	// 		return nil, fmt.Errorf("failed to parse scraper URL: %q", s.URL)
	// 	}
	// 	s.HostSlug = fetch.MakeURLStringSlug(u.Host)
	// }
	return &config, nil
}

// RegexConfig is used for extracting a substring from a string based on the
// given RegexPattern and Index
type RegexConfig struct {
	RegexPattern string `yaml:"exp"`
	Index        int    `yaml:"index"`
}

// ElementLocation is used to find a specific string in a html document
type ElementLocation struct {
	Selector      string      `yaml:"selector,omitempty"`
	JsonSelector  string      `yaml:"json_selector,omitempty"`
	ChildIndex    int         `yaml:"child_index,omitempty"`
	RegexExtract  RegexConfig `yaml:"regex_extract,omitempty"`
	Attr          string      `yaml:"attr,omitempty"`
	MaxLength     int         `yaml:"max_length,omitempty"`
	EntireSubtree bool        `yaml:"entire_subtree,omitempty"`
	AllNodes      bool        `yaml:"all_nodes,omitempty"`
	Separator     string      `yaml:"separator,omitempty"`
}

// TransformConfig is used to replace an existing substring with some other
// kind of string. Processing needs to happen before extracting dates.
type TransformConfig struct {
	TransformType string `yaml:"type,omitempty"`    // only regex-replace for now
	RegexPattern  string `yaml:"regex,omitempty"`   // a container for the pattern
	Replacement   string `yaml:"replace,omitempty"` // a plain string for replacement
}

// A DateComponent is used to find a specific part of a date within
// a html document
type DateComponent struct {
	Covers          date.CoveredDateParts `yaml:"covers"`
	ElementLocation ElementLocation       `yaml:"location"`
	Layout          []string              `yaml:"layout"`
	Transform       []TransformConfig     `yaml:"transform,omitempty"`
}

// A Field contains all the information necessary to scrape
// a dynamic field from a website, ie a field who's value changes
// for each record.
type Field struct {
	Name             string           `yaml:"name"`
	Value            string           `yaml:"value,omitempty"`
	Type             string           `yaml:"type,omitempty"`     // can currently be text, url or date
	ElementLocations ElementLocations `yaml:"location,omitempty"` // elements are extracted strings joined using the given Separator
	Default          string           `yaml:"default,omitempty"`  // the default for a dynamic field (text or url) if no value is found
	Separator        string           `yaml:"separator,omitempty"`
	// If a field can be found on a detail page the following variable has to
	// contain a field name of a field of type 'url' that is located on the main
	// page.
	OnDetailPage string            `yaml:"on_detail_page,omitempty"` // applies to text, url, date
	CanBeEmpty   bool              `yaml:"can_be_empty,omitempty"`   // applies to text, url
	Components   []DateComponent   `yaml:"components,omitempty"`     // applies to date
	DateLocation string            `yaml:"date_location,omitempty"`  // applies to date
	DateLanguage string            `yaml:"date_language,omitempty"`  // applies to date
	Hide         bool              `yaml:"hide,omitempty"`           // applies to text, url, date
	GuessYear    bool              `yaml:"guess_year,omitempty"`     // applies to date
	Transform    []TransformConfig `yaml:"transform,omitempty"`      // applies to text
}

type ElementLocations []ElementLocation

func (e *ElementLocations) UnmarshalYAML(value *yaml.Node) error {
	var multi []ElementLocation
	err := value.Decode(&multi)
	if err != nil {
		var single ElementLocation
		err := value.Decode(&single)
		if err != nil {
			return err
		}
		*e = []ElementLocation{single}
	} else {
		*e = multi
	}
	return nil
}

// also have a marshal func for the config generation? so that if the ElementLocations list
// is of length one we output the value in the yaml as ElementLocation and not list of ElementLocations

// A Filter is used to filter certain recs from the result list
type Filter struct {
	Field      string `yaml:"field"`
	Type       string
	Expression string `yaml:"exp"` // changed from 'regex' to 'exp' in version 0.5.7
	RegexComp  *regexp.Regexp
	DateComp   time.Time
	DateOp     string
	Match      bool `yaml:"match"`
}

func (f *Filter) FilterMatch(value interface{}) bool {
	switch f.Type {
	case "regex":
		return f.RegexComp.MatchString(fmt.Sprint(value))
	case "date":
		d, _ := value.(time.Time)
		if f.DateOp == ">" {
			return d.After(f.DateComp)
		} else {
			return d.Before(f.DateComp)
		}
	default:
		return false
	}
}

func (f *Filter) Initialize(fieldType string) error {
	if fieldType == "date" {
		f.Type = "date"
	} else {
		f.Type = "regex" // default for everything except date fields
	}
	switch f.Type {
	case "regex":
		regex, err := regexp.Compile(f.Expression)
		if err != nil {
			return err
		}
		f.RegexComp = regex
		return nil
	case "date":
		initErr := fmt.Errorf("the expression for filtering by date should be of the following format: '<|> now|YYYY-MM-ddTHH:mm'")
		tokens := strings.Split(f.Expression, " ")
		if len(tokens) != 2 {
			return initErr
		}
		if tokens[0] != ">" && tokens[0] != "<" {
			return initErr
		}
		f.DateOp = tokens[0]
		// parse date, return error
		if tokens[1] != "now" {
			t, err := time.Parse("2006-01-02T15:04", tokens[1])
			if err != nil {
				return initErr
			}
			f.DateComp = t
		} else {
			f.DateComp = time.Now().UTC()
		}
		return nil
	default:
		return fmt.Errorf("type '%s' does not exist for filters", f.Type)
	}
}

// A Paginator is used to paginate through a website
type Paginator struct {
	Location ElementLocation `yaml:"location,omitempty"`
	MaxPages int             `yaml:"max_pages,omitempty"`
}

// A Scraper contains all the necessary config parameters and structs needed
// to extract the desired information from a website
type Scraper struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	// HostSlug     string               `yaml:"hostslug"`
	Selector     string               `yaml:"selector"`
	Fields       []Field              `yaml:"fields,omitempty"`
	Filters      []*Filter            `yaml:"filters,omitempty"`
	Paginators   []Paginator          `yaml:"paginators,omitempty"`
	RenderJs     bool                 `yaml:"render_js,omitempty"`
	PageLoadWait int                  `yaml:"page_load_wait,omitempty"` // milliseconds. Only taken into account when render_js = true
	Interaction  []*fetch.Interaction `yaml:"interaction,omitempty"`
}

func (s Scraper) HostSlug() string {
	host := s.URL[strings.Index(s.URL, "//")+2:]
	end := strings.Index(host, "/")
	if end == -1 {
		end = len(host)
	}
	host = host[:end]
	return fetch.MakeURLStringSlug(host)
}

// Page fetches and returns all records from a webpage according to the
// Scraper's paramaters. When rawDyn is set to true the records returned are
// not processed according to their type but instead the raw values based
// only on the location are returned (ignore regex_extract??). And only those
// of dynamic fields, ie fields that don't have a predefined value and that are
// present on the main page (not detail pages). This is used by the ML feature generation.
func Page(cache fetch.Cache, c *Config, s *Scraper, globalConfig *GlobalConfig, rawDyn bool, path string) (output.Records, error) {
	if DoDebug {
		if output.WriteSeparateLogFiles {
			prevLogger, err := output.SetDefaultLogger("/tmp/goskyr/main/"+s.HostSlug()+"_configs/"+c.ID.String()+"_scrape_GQPage_log.txt", slog.LevelDebug)
			if err != nil {
				return nil, err
			}
			defer output.RestoreDefaultLogger(prevLogger)
		}
		// slog = slog.With(slog.String("s.Name", s.Name))
		slog.Debug("scrape.Page()")
		defer slog.Debug("scrape.Page() returning")
	}

	// // slog.Debug("Scraper.Page(globalConfig: %#v, rawDyn: %t)", globalConfig, rawDyn)
	// scrLogger := slog.With(slog.String("name", s.Name))
	// // initialize fetcher

	// fmt.Println("in scrape.Page()", "s.URL", s.URL)
	u := s.URL
	if path != "" {
		// 	s.fetcher = &fetch.FileFetcher{}
		u = path
	}
	// } else if s.RenderJs {
	// 	dynFetcher := fetch.NewDynamicFetcher(globalConfig.UserAgent, s.PageLoadWait)
	// 	defer dynFetcher.Cancel()
	// 	s.fetcher = dynFetcher
	// } else {
	// 	s.fetcher = &fetch.StaticFetcher{
	// 		UserAgent: globalConfig.UserAgent,
	// 		// UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
	// 	}
	// }
	// fmt.Println("in scrape.Page()", "u", u)

	rs := output.Records{}

	slog.Debug("initializing filters")
	if err := s.initializeFilters(); err != nil {
		return nil, err
	}

	hasNextPage := true
	currentPage := 0
	var gqdoc *fetch.Document

	// fmt.Println("fetching", "u", u)
	hasNextPage, pageURL, gqdoc, err := s.fetchPage(cache, nil, currentPage, u, globalConfig.UserAgent, s.Interaction)
	if err != nil {
		// slog.Debug("pageURL: %q", pageURL)
		return nil, fmt.Errorf("failed to fetch next page: %w", err)
	}

	for hasNextPage {
		if gqdoc == nil {
			// slog.Debug("pageURL: %q", pageURL)
			return nil, fmt.Errorf("failed to fetch next page (gqdoc == nil: %t)", gqdoc == nil)
		}

		recs, err := GQDocument(c, s, gqdoc)
		if err != nil {
			return nil, err
		}
		rs = append(rs, recs...)

		currentPage++
		hasNextPage, pageURL, gqdoc, err = s.fetchPage(cache, gqdoc, currentPage, pageURL, globalConfig.UserAgent, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch next page: %w", err)
		}
	}

	s.guessYear(rs, time.Now())

	slog.Debug("in scrape.Page()", "len(rs)", len(rs), "rs.TotalFields()", rs.TotalFields())
	return rs, nil
}

// GQDocument fetches and returns all records from a website according to the
// Scraper's paramaters. When rawDyn is set to true the records returned are
// not processed according to their type but instead the raw values based
// only on the location are returned (ignore regex_extract??). And only those
// of dynamic fields, ie fields that don't have a predefined value and that are
// present on the main page (not detail pages). This is used by the ML feature generation.
func GQDocument(c *Config, s *Scraper, gqdoc *fetch.Document) (output.Records, error) {
	if DoDebug {
		if output.WriteSeparateLogFiles {
			prevLogger, err := output.SetDefaultLogger("/tmp/goskyr/main/"+s.HostSlug()+"_configs/"+c.ID.String()+"_scrape_GQDocument_log.txt", slog.LevelDebug)
			if err != nil {
				return nil, err
			}
			defer output.RestoreDefaultLogger(prevLogger)
		}
		// slog = slog.With(slog.String("s.Name", s.Name))
		slog.Debug("scrape.GQDocument()")
		defer slog.Debug("scrape.GQDocument() returning")
	}

	rs := output.Records{}
	baseURL := getBaseURL(s.URL, gqdoc)

	// recElts := strings.Split(s.Item, " > ")
	// gqdoc.Document.Find(strings.Join(itemElts[0:len(itemElts)-1], " > ")).Each(func(i int, sel *cache.Selection) {
	slog.Debug("in scrape.GQDocument()", "s.Selector", s.Selector)
	slog.Debug("in scrape.GQDocument()", "len(doc.Find(s.Selector).Nodes)", len(gqdoc.Find(s.Selector).Nodes))
	// fmt.Println("in scrape.GQDocument()", "s.Selector", s.Selector)
	// fmt.Println("in scrape.GQDocument()", "len(gqdoc.Find(s.Selector).Nodes)", len(gqdoc.Find(s.Selector).Nodes))
	// fmt.Println("in scrape.GQDocument()", "len(gqdoc.Selection.Find(s.Selector).Nodes)", len(gqdoc.Selection.Find(s.Selector).Nodes))

	found := gqdoc.Document.Selection
	if s.Selector != "" {
		found = found.Find(s.Selector).Filter(s.Selector)
		if DebugGQFind && len(found.Nodes) == 0 {
			fmt.Printf("Trying to scrape from %q\n", s.URL)
			fmt.Printf("Found no nodes for original selector: %q\n", s.Selector)
			printGQFindDebug(gqdoc, s.Selector)
			return nil, nil
		}
	}
	found.Each(func(i int, sel *goquery.Selection) {
		// fmt.Println("in scrape.GQDocument()", "i", i) //, "sel.Nodes", printHTMLNodes(sel.Nodes))
		slog.Debug("in scrape.GQDocument()", "i", i) //, "sel.Nodes", printHTMLNodes(sel.Nodes))
		r, err := GQSelection(c, s, fetch.NewSelection(sel), baseURL)
		if err != nil {
			slog.Warn("while scraping document got error", "baseUrl", baseURL, "err", err.Error())
			return
		}
		if len(r) == 0 {
			return
		}
		r[URLFieldName] = baseURL
		r[TitleFieldName] = gqdoc.Find("title").Text()
		// fmt.Println("in scrape.GQDocument()", "r[URLFieldName]", r[URLFieldName])
		// fmt.Println("in scrape.GQDocument()", "rs", rs)
		rs = append(rs, r)
	})

	s.guessYear(rs, time.Now())

	slog.Debug("in scrape.GQDocument()", "len(rs)", len(rs), "rs.TotalFields()", rs.TotalFields())
	// fmt.Println("in scrape.GQDocument()", "len(rs)", len(rs), "rs.TotalFields()", rs.TotalFields())
	return rs, nil
}

func printGQFindDebug(gqdoc *fetch.Document, sel string) {
	selParts := strings.Split(sel, " > ")
	fmt.Printf("    starting with selector: %q\n", selParts[0])
	foundNone := false
	for i := 1; i < len(selParts); i++ {
		sel := strings.Join(selParts[0:i], " > ")
		found := len(gqdoc.Find(sel).Nodes)
		if !foundNone && found == 0 {
			foundNone = true
			prevNs := gqdoc.Find(strings.Join(selParts[0:i-1], " > ")).Nodes
			for _, n := range prevNs {
				fmt.Printf("    found child node: %q\n", printHTMLNodeAsStartTag(n))
			}
		}
		fmt.Printf("    found %d nodes with selector: %q\n", found, selParts[i])
	}
}

// GQSelection fetches and returns an records from a website according to the
// Scraper's paramaters. When rawDyn is set to true the record returned is not
// processed according to its type but instead the raw value based only on the
// location is returned (ignore regex_extract??). And only those of dynamic
// fields, ie fields that don't have a predefined value and that are present on
// the main page (not detail pages). This is used by the ML feature generation.
func GQSelection(c *Config, s *Scraper, sel *fetch.Selection, baseURL string) (output.Record, error) {
	// fmt.Println("scrape.GQSelection()", "c.ID", c.ID)
	if DoDebug {
		if output.WriteSeparateLogFiles {
			prevLogger, err := output.SetDefaultLogger("/tmp/goskyr/main/"+s.HostSlug()+"_configs/"+c.ID.String()+"_scrape_GQSelection_log.txt", slog.LevelDebug)
			if err != nil {
				return nil, err
			}
			defer output.RestoreDefaultLogger(prevLogger)
		}
		// slog = slog.With(slog.String("s.Name", s.Name))
		slog.Debug("scrape.GQSelection()")
		defer slog.Debug("scrape.GQSelection() returning")
	}

	// slog.Debug("Scraper.GQSelection()", "s", sel, "baseUrl", baseUrl, "rawDyn", rawDyn)
	// for i, node := range s.Nodes {
	// 	slog.Debug("in Scraper.GQSelection()", "i", i, "node", node)
	// 	// slog.Debug("in Scraper.GetRecords(), c.Record match", "i", i)
	// 	// slog.Debug("in Scraper.GetRecords(), c.Record matched", "c.Fields", c.Fields)
	// }

	rs := output.Record{}
	fs := s.Fields
	sort.Slice(fs, func(i, j int) bool { return fs[i].Type == "url" })
	for _, f := range fs {
		slog.Debug("in scrape.GQSelection(), looking at field", "f.Name", f.Name)
		// if f.Value != "" {
		// 	if !rawDyn {
		// 		// add static fields
		// 		rs[f.Name] = f.Value
		// 	}
		// 	continue
		// }
		slog.Debug("in scrape.GQSelection(), before extract", "f", f)

		// handle all dynamic fields on the main page
		if f.OnDetailPage == "" {
			var err error
			// if rawDyn {
			// err = extractRawField(&f, rs, sel)
			// } else {
			err = extractField(&f, rs, sel, baseURL, 0)
			// }
			if err != nil {
				return nil, fmt.Errorf("error while parsing field %s: %v. Skipping rs %v.", f.Name, err, rs)
			}
		}
		slog.Debug("in scrape.GQSelection(), after extract", "f", f)

		// To speed things up we check the filter after each field.  Like that we
		// safe time if we already know for sure that we want to filter out a
		// certain record. Especially, if certain elements would need to be fetched
		// from detail pages.
		//
		// Filter fast!
		if !s.keepRecord(rs) {
			return nil, nil
		}
	}
	// slog.Debug("in Scraper.GQSelection(), after field check", "currentItem", rs)

	// Handle all fields on detail pages.
	// if !rawDyn {
	// 	dpDocs := make(map[string]*fetch.Document)
	// 	for _, f := range s.Fields {
	// 		if f.OnDetailPage == "" || f.Value != "" {
	// 			continue
	// 		}

	// 		// check whether we fetched the page already
	// 		dpURL := fmt.Sprint(rs[f.OnDetailPage])
	// 		_, found := dpDocs[dpURL]
	// 		if !found {
	// 			panic("in scrape.GQSelection(), we can't fetch pages here")
	// 			// dpDoc, _, err := fetch.GetGQDocument(cache, dpURL)
	// 			// dpDocs[dpURL] = dpDoc

	// 			// // dpRes, err := s.fetcher.Fetch(dpURL, nil)
	// 			// dpDoc, _, err := fetch.GetGQDocument(cache, dpURL)
	// 			// if err != nil {
	// 			// 	return nil, fmt.Errorf("error while fetching detail page: %v. Skipping record %v.", err, rs)
	// 			// }
	// 			// // dpDoc, err := goquery.NewDocumentFromReader(strings.NewReader(dpRes))
	// 			// // if err != nil {
	// 			// // 	return nil, fmt.Errorf("error while reading detail page document: %v. Skipping record %v", err, rs)
	// 			// // }
	// 			// dpDocs[dpURL] = dpDoc
	// 		}

	// 		baseURLDetailPage := getBaseURL(dpURL, dpDocs[dpURL])
	// 		err := extractField(&f, rs, dpDocs[dpURL].Selection, baseURLDetailPage)
	// 		if err != nil {
	// 			return nil, fmt.Errorf("error while parsing detail page field %s: %v. Skipping record %v.", f.Name, err, rs)
	// 		}
	// 		// filter fast!
	// 		if !s.keepRecord(rs) {
	// 			return nil, nil
	// 		}
	// 	}
	// }
	// slog.Debug("in Scraper.GQSelection(), after rawDyn", "currentItem", rs)

	// check if item should be filtered
	if !s.keepRecord(rs) {
		return nil, nil
	}

	rs = s.removeHiddenFields(rs)
	// fmt.Println("s.numNonEmptyFields(rs)", s.numNonEmptyFields(rs))
	// if s.numNonEmptyFields(rs) == 0 {
	// 	return nil, nil
	// }

	slog.Debug("in scrape.GQSelection()", "rs", rs)
	// fmt.Println("in scrape.GQSelection()", "rs", rs)
	return rs, nil
}

func (c *Scraper) guessYear(recs output.Records, ref time.Time) {
	// get date field names where we need to adapt the year
	dateFieldsGuessYear := map[string]bool{}
	for _, f := range c.Fields {
		if f.Type == "date" {
			if f.GuessYear {
				dateFieldsGuessYear[f.Name] = true
			}
		}
	}

	// main use case:
	// event websites mostly contain a list of events ordered by date. Sometimes the date does
	// not contain the year. In that case we could simply set the year to the current year but
	// it might happen that the list of events spans across more than one year into the next
	// year. In that case we still want to set the correct year which would be current year + n.
	// Moreover, the list might not be ordered at all. In that case we also want to try to set
	// the correct year.
	if len(dateFieldsGuessYear) > 0 {
		for i, rec := range recs {
			for name, val := range rec {
				if dateFieldsGuessYear[name] {
					if t, ok := val.(time.Time); ok {

						// for the first record we compare this record's date with 'now' and try
						// to find the most suitable year, ie the year that brings this record's
						// date closest to now.
						// for the remaining records we do the same as with the first record except
						// that we compare this record's date to the previous record's date instead
						// of 'now'.
						if i > 0 {
							ref, _ = recs[i-1][name].(time.Time)
						}
						diff := time.Since(time.Unix(0, 0))
						newDate := t
						for y := ref.Year() - 1; y <= ref.Year()+1; y++ {
							tmpT := time.Date(y, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
							if newDiff := tmpT.Sub(ref).Abs(); newDiff < diff {
								diff = newDiff
								newDate = time.Date(y, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
							}
						}
						rec[name] = newDate
					}
				}
			}
		}
	}
}

func (c *Scraper) initializeFilters() error {
	// build temporary map field name -> field type
	fieldTypes := map[string]string{}
	for _, field := range c.Fields {
		fieldTypes[field.Name] = field.Type
	}
	for _, f := range c.Filters {
		if fieldType, ok := fieldTypes[f.Field]; ok {
			if err := f.Initialize(fieldType); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("filter error. There is no field with the name '%s'", f.Field)
		}
	}
	return nil
}

func (c *Scraper) keepRecord(rec output.Record) bool {
	nrMatchTrue := 0
	filterMatchTrue := false
	filterMatchFalse := true
	for _, f := range c.Filters {
		if fieldValue, found := rec[f.Field]; found {
			if f.Match {
				nrMatchTrue++
				if f.FilterMatch(fieldValue) {
					filterMatchTrue = true
				}
			} else {
				if f.FilterMatch(fieldValue) {
					filterMatchFalse = false
				}
			}
		}
	}
	if nrMatchTrue == 0 {
		filterMatchTrue = true
	}
	return filterMatchTrue && filterMatchFalse
}

func (c *Scraper) removeHiddenFields(rec output.Record) output.Record {
	for _, f := range c.Fields {
		if f.Hide {
			delete(rec, f.Name)
		}
	}
	return rec
}

// func (c *Scraper) numNonEmptyFields(rec output.Record) int {
// 	r := 0
// 	for _, f := range c.Fields {
// 		if rec[f.Name] != "" {
// 			fmt.Println("f.Name", f.Name, "rec[f.Name]", rec[f.Name])
// 			r += 1
// 		}
// 	}
// 	return r
// }

func (c *Scraper) GetDetailPageURLFields() []Field {
	rs := []Field{}
	for _, f := range c.Fields {
		if f.Type != "url" {
			continue
		}
		if SkipSubURLExt[filepath.Ext(f.Value)] {
			continue
		}
		rs = append(rs, f)
	}
	return rs
}

func (c *Scraper) fetchPage(cache fetch.Cache, gqdoc *fetch.Document, nextPageI int, currentPageURL, userAgent string, i []*fetch.Interaction) (bool, string, *fetch.Document, error) {
	// fmt.Println("scrape.Scraper.fetchPage()", "nextPageI", nextPageI, "currentPageURL", currentPageURL)
	if nextPageI == 0 {
		newDoc, _, err := fetch.GetGQDocument(cache, currentPageURL) //, &fetch.FetchOpts{Interaction: i})
		if err != nil {
			return false, "", nil, err
		}
		return true, currentPageURL, newDoc, nil
	}

	if len(c.Paginators) == 0 {
		return false, "", nil, nil
	}

	if c.RenderJs {
		// check if node c.Paginator.Location.Selector is present in doc
		pag := c.Paginators[0]
		pagSelector := gqdoc.Find(pag.Location.Selector)
		fmt.Println("pagSelector", pagSelector)
		if len(pagSelector.Nodes) > 0 {
			if nextPageI < pag.MaxPages || pag.MaxPages == 0 {
				fmt.Println("pag.Location.Selector", pag.Location.Selector)
				// ia := []*fetch.Interaction{
				// 	{
				// 		Selector: pag.Location.Selector,
				// 		Type:     fetch.InteractionTypeClick,
				// 		Count:    nextPageI, // we always need to 'restart' the clicks because we always re-fetch the page
				// 	},
				// }
				nextPageDoc, _, err := fetch.GetGQDocument(cache, currentPageURL) //, &fetch.FetchOpts{Interaction: ia})
				if err != nil {
					return false, "", nil, err
				}
				return true, currentPageURL, nextPageDoc, nil
			}
		}
		return false, "", nil, nil
	}

	baseURL := getBaseURL(currentPageURL, gqdoc)
	_, nextPageUU, err := GetTextStringAndURL(&c.Paginators[0].Location, fetch.NewSelection(gqdoc.Document.Selection), baseURL)
	nextPageURL := nextPageUU.String()
	fmt.Println("in scrape.fetchPage()", "baseURL", baseURL)
	fmt.Println("in scrape.fetchPage()", "nextPageURL", nextPageURL)

	if err != nil {
		return false, "", nil, err
	}
	if nextPageURL != "" {
		nextPageDoc, _, err := fetch.GetGQDocument(cache, nextPageURL) //, nil)
		if err != nil {
			return false, "", nil, err
		}
		if nextPageI < c.Paginators[0].MaxPages || c.Paginators[0].MaxPages == 0 {
			return true, nextPageURL, nextPageDoc, nil
		}
	}

	return false, "", nil, nil
}

var URLFieldName = "Aurl"
var URLFieldSuffix = "__" + URLFieldName
var TitleFieldName = "Atitle"
var TitleFieldSuffix = "__" + TitleFieldName
var DateTimeFieldSuffix = "__" + DateTimeFieldName
var DateTimeFieldName = "Pdate_time_tz_ranges"

var DateRE = regexp.MustCompile(`(?i)\b(2024|2025|January|February|March|April|` + /* May */ `|June|July|August|September|October|November|December|Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec|Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday|Mon|Tue|Wed|Thu|Fri|Sat|Sun)\b`)
var yearRE = regexp.MustCompile(`(?i)\b(20[0-9][0-9])\b`)

func DebugDateTime(args ...any) { slog.Debug(args[0].(string), args[1:]...) }

// func DebugDateTime(args ...any) { fmt.Println(args...) }

func extractField(f *Field, rec output.Record, sel *fetch.Selection, baseURL string, baseYear int) error {
	slog.Debug("scrape.extractField()", "field", f, "event", rec, "sel", sel, "baseURL", baseURL)
	switch f.Type {
	case "text", "": // the default, ie when type is not configured, is 'text'
		parts := []string{}
		if len(f.ElementLocations) == 0 {
			slog.Debug("in scrape.extractField(), empty field.ElementLocations")
		} else {
			for _, p := range f.ElementLocations {
				str, err := getTextString(&p, sel)
				if err != nil {
					return err
				}
				if str != "" {
					parts = append(parts, str)
				}
			}
		}
		t := strings.Join(parts, f.Separator)
		if t == "" {
			// if the extracted value is an empty string assign the default value
			t = f.Default
			if !f.CanBeEmpty && t == "" {
				// if it's still empty and must not be empty return an error
				return fmt.Errorf("field %s cannot be empty", f.Name)
			}
		}
		// transform the string if required
		for _, tr := range f.Transform {
			var err error
			t, err = transformString(&tr, t)
			if err != nil {
				return err
			}
		}
		rec[f.Name] = t

	case "url":
		if len(f.ElementLocations) != 1 {
			return fmt.Errorf("a field of type 'url' must exactly have one location, found %d", len(f.ElementLocations))
		}
		relU, uu, err := GetTextStringAndURL(&f.ElementLocations[0], sel, baseURL)
		// fmt.Println("in extractFieldURL()", "f.Name", f.Name, "relU", relU, "uu", uu)
		rec[f.Name] = relU
		if err != nil {
			return err
		}
		u := uu.String()
		if u == "" {
			// if the extracted value is an empty string assign the default value
			u = f.Default
			if !f.CanBeEmpty && u == "" {
				// if it's still empty and must not be empty return an error
				return fmt.Errorf("field %s cannot be empty", f.Name)
			}
		}
		// fmt.Println("in extractFieldURL()", "f.Name+URLFieldSuffix", f.Name+URLFieldSuffix, "uu", uu, "u", u)
		rec[f.Name+URLFieldSuffix] = u

	case "date_time_tz_ranges":
		if len(f.ElementLocations) != 1 {
			return fmt.Errorf("a field of type 'date_time_tz_ranges' must exactly have one location, found %d", len(f.ElementLocations))
		}
		str, err := getTextString(&f.ElementLocations[0], sel)
		rec[f.Name] = str
		if err != nil {
			return err
		}

		// First check if the url encodes a parseable datetime with year, and use the year if so.
		for k, v := range rec {
			str := v.(string)
			DebugDateTime("looking at non-date-time field", "baseYear", baseYear, "k", k, "v", v, "strings.HasSuffix(k, URLFieldSuffix)", strings.HasSuffix(k, URLFieldSuffix))
			if strings.HasSuffix(k, URLFieldSuffix) {
				// if match := DateRE.FindString(str); match == "" {
				// 	continue
				// }
				// debugDateTime("matched dateRE")
				rngs, err := datetime.Parse(0, "", datetime.NewTimeZone(f.DateLocation, "", ""), str)
				if err != nil {
					continue
				}
				if rngs != nil {
					for _, rng := range rngs.Items {
						if rng.Start.Date.Year != 0 {
							baseYear = rng.Start.Date.Year
							slog.Warn("found", "baseYear", baseYear)
							break
						}
						if rng.End != nil && rng.End.Date.Year != 0 {
							baseYear = rng.End.Date.Year
							slog.Warn("found", "baseYear", baseYear)
							break
						}
					}
				}
			}
			DebugDateTime("after looking", "baseYear", baseYear)
		}
		// Then use the current year if none is provided.
		if baseYear == 0 {
			// baseYear = time.Now().Year()
			baseYear = 2024
			DebugDateTime("after setting to now", "baseYear", baseYear)
		}
		DebugDateTime("parsing datetime with", "baseYear", baseYear, "str", str)
		rngs, err := datetime.Parse(baseYear, "", datetime.NewTimeZone(f.DateLocation, "", ""), str)
		// fmt.Printf("rngs.Items[0]: %#v\n", rngs.Items[0])
		// fmt.Printf("rngs.Items[0].Start: %#v\n", rngs.Items[0].Start)
		if err != nil {
			DebugDateTime("parse error", "err", err)
			break
		}
		if datetime.HasStartMonthAndDay(rngs) {
			DebugDateTime("parsed", "rngs", rngs)
			// fmt.Printf("rngs.Items[0].Start: %#v\n", rngs.Items[0].Start)
			// start := rngs.Items[0].Start
			// event[field.Name] = start.Date.String() + " " + start.Time.String()
			rec[f.Name+DateTimeFieldSuffix] = rngs.String()
			break
		}
		if match := DateRE.FindString(str); match != "" {
			DebugDateTime("found date term in field but failed to parse datetime ranges", "match", match, "str", str)
			break
		}

		// d, err := getDate(field, sel, dateDefaults{})
		// if err != nil {
		// 	return err
		// }
		// event[field.Name] = d
	default:
		return fmt.Errorf("field type '%s' does not exist", f.Type)
	}
	return nil
}

func GetTextStringAndURL(e *ElementLocation, sel *fetch.Selection, baseURL string) (string, *url.URL, error) {
	// var urlVal, urlRes string
	baseUU, err := url.Parse(baseURL)
	if err != nil {
		return "", nil, err
	}

	if e.Attr == "" {
		// set attr to the default if not set
		e.Attr = "href"
	}
	relU, err := getTextString(e, sel)
	if err != nil {
		return "", nil, err
	}
	uu, err := baseUU.Parse(relU)
	// fmt.Println("in GetTextStringAndURL()", "baseUU", baseUU, "relU", relU, "uu", uu)
	return relU, uu, err
}

var SkipTag = map[string]bool{
	"noscript": true,
	"script":   true,
	"style":    true,
}

func getTextString(e *ElementLocation, sel *fetch.Selection) (string, error) {
	slog.Debug("getTextString()", "e", e, "s", sel)
	var fieldStrings []string
	var fieldSelection *fetch.Selection
	if e.Selector == "" {
		fieldSelection = sel
	} else {
		fieldSelection = sel.Find(e.Selector)
	}
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		slog.Debug("in getTextString()", "e.Selector", e.Selector, "e.Attr", e.Attr, "e.EntireSubtree", e.EntireSubtree, "len(fieldSelection.Nodes)", len(fieldSelection.Nodes))
		slog.Debug("in getTextString()", "printHTMLNodes(fieldSelection.Nodes)", printHTMLNodes(fieldSelection.Nodes))
		for i, n := range fieldSelection.Nodes {
			slog.Debug("in getTextString()", "i", i, "fieldSelectionNode", n, "printHTMLNodeAsStartTag(n)", printHTMLNodeAsStartTag(n))
		}
	}
	if len(fieldSelection.Nodes) > 0 {
		if e.Attr == "" {
			if e.EntireSubtree {
				// copied from https://github.com/PuerkitoBio/goquery/blob/v1.8.0/property.go#L62
				var buf bytes.Buffer
				var f func(*html.Node)
				f = func(n *html.Node) {
					// Skip the text in-between <style></style> tags.
					if n.Type == html.ElementNode && SkipTag[n.Data] {
						return
					}
					if n.Type == html.TextNode {
						// Keep newlines and spaces, like jQuery
						buf.WriteString(n.Data)
					}
					if n.FirstChild != nil {
						for c := n.FirstChild; c != nil; c = c.NextSibling {
							f(c)
						}
					}
				}
				if e.AllNodes {
					for _, node := range fieldSelection.Nodes {
						f(node)
						fieldStrings = append(fieldStrings, buf.String())
						buf.Reset()
					}
				} else {
					f(fieldSelection.Get(0))
					fieldStrings = append(fieldStrings, buf.String())
				}
			} else {

				var fieldNodes []*html.Node
				if e.AllNodes {
					for _, node := range fieldSelection.Nodes {
						fieldNode := node.FirstChild
						if fieldNode != nil {
							fieldNodes = append(fieldNodes, fieldNode)
						}
					}
				} else {
					fieldNode := fieldSelection.Get(0).FirstChild
					for _, n := range fieldSelection.Nodes {
						if n.Attr == nil {
							// FIXME: Test this case
							slog.Debug("in getTextString(), setting field node to node with no attributes, as we expect from selector")
							fieldNode = n.FirstChild
							break
						}
					}

					if fieldNode != nil {
						fieldNodes = append(fieldNodes, fieldNode)
					}
				}
				for _, fieldNode := range fieldNodes {
					currentChildIndex := 0
					for fieldNode != nil {
						if currentChildIndex == e.ChildIndex {
							if fieldNode.Type == html.TextNode {
								fieldStrings = append(fieldStrings, fieldNode.Data)
								break
							}
						}
						fieldNode = fieldNode.NextSibling
						currentChildIndex++
					}
				}
			}
		} else {
			// WRONG
			// It could be the case that there are multiple nodes that match the selector
			// and we don't want the attr of the first node...
			fieldStrings = append(fieldStrings, fieldSelection.AttrOr(e.Attr, ""))
		}
	}
	slog.Debug("getTextString() 1", "fieldStrings", fieldStrings)
	// do json lookup if we have a json_selector
	for i, f := range fieldStrings {
		fieldString, err := extractJsonField(e.JsonSelector, f)
		if err != nil {
			return "", err
		}
		fieldStrings[i] = fieldString
	}
	// regex extract
	for i, f := range fieldStrings {
		fieldString, err := extractStringRegex(&e.RegexExtract, f)
		if err != nil {
			return "", err
		}
		fieldStrings[i] = fieldString
	}
	// automatically trimming whitespaces might be confusing in some cases...
	// TODO make this configurable
	for i, f := range fieldStrings {
		fieldStrings[i] = strings.TrimSpace(f)
	}
	// shortening
	for i, f := range fieldStrings {
		fieldStrings[i] = utils.ShortenString(f, e.MaxLength)
	}
	r := strings.Join(fieldStrings, e.Separator)
	slog.Debug("getTextString(), returning", "r", r)
	return r, nil
}

func extractStringRegex(rc *RegexConfig, s string) (string, error) {
	extractedString := s
	if rc.RegexPattern != "" {
		regex, err := regexp.Compile(rc.RegexPattern)
		if err != nil {
			return "", err
		}
		matchingStrings := regex.FindAllString(s, -1)
		if len(matchingStrings) == 0 {
			msg := fmt.Sprintf("no matching strings found for regex: %s", rc.RegexPattern)
			return "", errors.New(msg)
		}
		if rc.Index == -1 {
			extractedString = matchingStrings[len(matchingStrings)-1]
		} else {
			if rc.Index >= len(matchingStrings) {
				msg := fmt.Sprintf("regex index out of bounds. regex '%s' gave only %d matches", rc.RegexPattern, len(matchingStrings))
				return "", errors.New(msg)
			}
			extractedString = matchingStrings[rc.Index]
		}
	}
	return extractedString, nil
}

func transformString(t *TransformConfig, s string) (string, error) {
	extractedString := s
	switch t.TransformType {
	case "regex-replace":
		if t.RegexPattern != "" {
			regex, err := regexp.Compile(t.RegexPattern)
			if err != nil {
				return "", err
			}
			extractedString = regex.ReplaceAllString(s, t.Replacement)
		}
	case "":
		// do nothing
	default:
		return "", fmt.Errorf("transform type '%s' does not exist", t.TransformType)
	}
	return extractedString, nil
}

func getBaseURL(pageUrl string, gqdoc *fetch.Document) string {
	// relevant info: https://www.w3.org/TR/WD-html40-970917/htmlweb.html#relative-urls
	// currently this function does not fully implement the standard
	baseURL := gqdoc.Find("base").AttrOr("href", "")
	if baseURL == "" {
		baseURL = pageUrl
	}
	return baseURL
}

func extractJsonField(p string, s string) (string, error) {
	extractedString := s
	if p != "" {
		// HACK: json has some instances of meaningfull whitespace
		// (\n in values). Let's get rid of them
		spacecleaner := regexp.MustCompile(`\s+`)
		s = spacecleaner.ReplaceAllString(s, " ")
		// HACK: a dangling comma is another common json mistake
		regex := regexp.MustCompile(`,\s*}`)
		s = regex.ReplaceAllString(s, " }")
		doc, err := jsonquery.Parse(strings.NewReader(s))
		if err != nil {
			return "", fmt.Errorf("JSON: %+v : %s", err, s)
		}
		node := jsonquery.FindOne(doc, p)
		extractedString = fmt.Sprintf("%v", node.Value())
	}
	return extractedString, nil
}

var SkipSubURLExt = map[string]bool{
	".gif":  true,
	".jfif": true,
	".jpeg": true,
	".jpg":  true,
	".mp4":  true,
	".pdf":  true,
	".png":  true,
	".webp": true,
	".zip":  true,
}

var KeepSubURLScheme = map[string]bool{
	"http":  true,
	"https": true,
}

func DetailPages(cache fetch.Cache, c *Config, s *Scraper, recs output.Records, domain string) error {
	if DoDebug {
		slog.Debug("scrape.DetailPages()")
		defer slog.Debug("scrape.DetailPages() returning")
	}

	uBase, err := url.Parse(s.URL)
	if err != nil {
		return fmt.Errorf("error parsing input url %q: %v", s.URL, err)
	}

	for i, rec := range recs {
		relStr := rec[c.ID.Field].(string)
		if SkipSubURLExt[filepath.Ext(relStr)] {
			slog.Debug("in scrape.DetailPages(), skipping sub URL due to extension", "relStr", relStr)
			continue
		}

		rel, err := url.Parse(relStr)
		if err != nil {
			return fmt.Errorf("error parsing detail page url %q: %v", c.ID.Field, err)
		}
		subURL, err := tld.Parse(uBase.ResolveReference(rel).String())
		if err != nil {
			return fmt.Errorf("error reparsing detail page url %q: %v", c.ID.Field, err)
		}

		if !KeepSubURLScheme[subURL.Scheme] {
			slog.Debug("in scrape.DetailPages(), skipping sub URL due to scheme", "subURL", subURL)
			continue
		}
		if domain != "" && domain != subURL.Domain {
			slog.Debug("in scrape.DetailPages(), skipping sub URL with different domain", "domain", domain, "subURL", subURL)
			continue
		}

		slog.Debug("in scrape.DetailPages()", "i", i, "subURL", subURL)
		// fmt.Println("in scrape.DetailPages()", "i", i, "subURL", subURL)
		subGQDoc, found, err := fetch.GetGQDocument(cache, subURL.String())
		if err != nil {
			return fmt.Errorf("error fetching detail page GQDocument at %q (found: %t): %v", subURL, found, err)
		}
		if subGQDoc == nil {
			slog.Warn("no subGQDoc found for %q", "subURL", subURL)
			// return fmt.Errorf("error fetching detail page GQDocument at %q (found: %t): %v", subURL, found, err)
			continue
		}
		if err := SubGQDocument(c, s, rec, c.ID.Field, subGQDoc); err != nil {
			return fmt.Errorf("error extending records: %v", err)
		}
	}
	return nil
}

func SubGQDocument(c *Config, s *Scraper, rec output.Record, fname string, gqdoc *fetch.Document) error {
	if DoDebug {
		if output.WriteSeparateLogFiles {
			prevLogger, err := output.SetDefaultLogger("/tmp/goskyr/main/"+s.HostSlug()+"_configs/"+c.ID.String()+"_scrape_SubGQDocument_log.txt", slog.LevelDebug)
			if err != nil {
				return err
			}
			defer output.RestoreDefaultLogger(prevLogger)
		}
		// slog = slog.With(slog.String("s.Name", s.Name))
		slog.Debug("scrape.SubGQDocument()", "fname", fname)
		defer slog.Debug("scrape.SubGQDocument() returning")
	}

	subRecs, err := GQDocument(c, s, gqdoc)
	if err != nil {
		return fmt.Errorf("error scraping detail page for field %q: %v", fname, err)
	}
	// The detail page may not have had valid records.
	if len(subRecs) != 1 {
		slog.Debug("error scraping detail page: expected exactly one item map", "c.ID", c.ID, "fname", fname, "len(subRecs)", len(subRecs))
		// fmt.Printf("error scraping detail page: expected exactly one item map for configID: %q, fname %q, got %d instead\n", c.ID.String(), fname, len(subRecs))
		// return fmt.Errorf("error scraping detail page: expected exactly one item map for configID: %q, fname %q, got %d instead", c.ID.String(), fname, len(subRecs))
		return nil
	}
	for k, v := range subRecs[0] {
		if k == URLFieldName {
			continue
		}
		rec[fname+"__"+k] = v
		// fmt.Println("in SubGQDocument()", "fname+\"__\"+k", fname+"__"+k, "k", k, "v", v)
	}
	return nil
}

func printHTMLNodes(ns []*html.Node) string {
	var r strings.Builder
	for _, n := range ns {
		r.WriteString(n.Data)
		if len(n.Attr) > 0 {
			r.WriteString(fmt.Sprintf("%#v", n.Attr))
		}
	}
	return r.String()
}

func printHTMLNodeAsStartTag(n *html.Node) string {
	if n.Type != html.ElementNode {
		return ""
	}
	var attrs []string
	for _, attr := range n.Attr {
		attrs = append(attrs, fmt.Sprintf("%s=\"%s\"", attr.Key, attr.Val))
	}
	return "<" + n.Data + " " + strings.Join(attrs, " ") + ">"
}
