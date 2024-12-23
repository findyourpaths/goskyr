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
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/jsonquery"
	"github.com/findyourpaths/goskyr/date"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/goodsign/monday"
	"github.com/ilyakaznacheev/cleanenv"
	"golang.org/x/net/html"
	"gopkg.in/yaml.v3"
)

// var DoDebug = true

var DoDebug = false

var DebugGQFind = true

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
	Name         string               `yaml:"name"`
	URL          string               `yaml:"url"`
	Selector     string               `yaml:"selector"`
	Fields       []Field              `yaml:"fields,omitempty"`
	Filters      []*Filter            `yaml:"filters,omitempty"`
	Paginators   []Paginator          `yaml:"paginators,omitempty"`
	RenderJs     bool                 `yaml:"render_js,omitempty"`
	PageLoadWait int                  `yaml:"page_load_wait,omitempty"` // milliseconds. Only taken into account when render_js = true
	Interaction  []*fetch.Interaction `yaml:"interaction,omitempty"`
	fetcher      fetch.Fetcher
}

// Page fetches and returns all records from a webpage according to the
// Scraper's paramaters. When rawDyn is set to true the records returned are
// not processed according to their type but instead the raw values based
// only on the location are returned (ignore regex_extract??). And only those
// of dynamic fields, ie fields that don't have a predefined value and that are
// present on the main page (not detail pages). This is used by the ML feature generation.
func Page(c *Config, s *Scraper, globalConfig *GlobalConfig, rawDyn bool, path string) (output.Records, error) {
	if DoDebug {
		if output.WriteSeparateLogFiles {
			prevLogger, err := output.SetDefaultLogger("/tmp/goskyr/main/"+c.ID.Slug+"_configs/"+c.ID.String()+"_scrape_GQPage_log.txt", slog.LevelDebug)
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

	u := s.URL
	if path != "" {
		s.fetcher = &fetch.FileFetcher{}
		u = path
	} else if s.RenderJs {
		dynFetcher := fetch.NewDynamicFetcher(globalConfig.UserAgent, s.PageLoadWait)
		defer dynFetcher.Cancel()
		s.fetcher = dynFetcher
	} else {
		s.fetcher = &fetch.StaticFetcher{
			UserAgent: globalConfig.UserAgent,
			// UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
		}
	}

	rs := output.Records{}

	slog.Debug("initializing filters")
	if err := s.initializeFilters(); err != nil {
		return nil, err
	}

	hasNextPage := true
	currentPage := 0
	var doc *goquery.Document

	hasNextPage, pageURL, doc, err := s.fetchPage(nil, currentPage, u, globalConfig.UserAgent, s.Interaction)
	if err != nil {
		// slog.Debug("pageURL: %q", pageURL)
		return nil, fmt.Errorf("failed to fetch next page: %w", err)
	}

	for hasNextPage {
		baseUrl := getBaseURL(pageURL, doc)

		// if len(doc.Find(c.Record).Nodes) == 0 {
		// 	slog.Debug("in Scraper.Page(), no records found, shortening selector to find the longest prefix that selects records")
		// 	itemPath := c.Record
		// 	for {
		// 		slog.Debug("in Scraper.Page(), itemPath: %#v", itemPath)
		// 		if len(doc.Find(itemPath).Nodes) != 0 {
		// 			slog.Debug("in Scraper.Page(), len(doc.Find(itemPath).Nodes): %d", len(doc.Find(itemPath).Nodes))
		// 			for _, node := range doc.Find(itemPath).Nodes {
		// 				slog.Debug("%#v", node)
		// 			}
		// 			break
		// 		}
		// 		itemPathParts := strings.Split(itemPath, " > ")
		// 		itemPath = strings.Join(itemPathParts[0:len(itemPathParts)-1], " > ")
		// 	}
		// }

		slog.Debug("in scrape.Page()", "s.Selector", s.Selector)
		slog.Debug("in scrape.Page()", "len(doc.Find(s.Record).Nodes)", len(doc.Find(s.Selector).Nodes))

		found := doc.Find(s.Selector)
		if DebugGQFind && len(found.Nodes) == 0 {
			fmt.Printf("Found no nodes for original selector: %q\n", s.Selector)
			selParts := strings.Split(s.Selector, " > ")
			fmt.Printf("     starting with selector: %q\n", selParts[0])
			foundNone := false
			for i := 1; i < len(selParts); i++ {
				sel := strings.Join(selParts[0:i], " > ")
				found := len(doc.Find(sel).Nodes)
				if !foundNone && found == 0 {
					foundNone = true
					prevNs := doc.Find(strings.Join(selParts[0:i-1], " > ")).Nodes
					for _, n := range prevNs {
						fmt.Printf("found child node: %q\n", printHTMLNodeAsStartTag(n))
					}
				}
				fmt.Printf("found %d nodes with selector: %q\n", found, selParts[i])
			}
			return nil, nil
		}
		found.Each(func(i int, sel *goquery.Selection) {
			rec, err := GQSelection(c, s, sel, baseUrl, rawDyn)
			if err != nil {
				slog.Error(err.Error())
				return
			}
			slog.Debug("in scrape.Page(), looking at sel record", "rec", rec)
			if rec != nil {
				rs = append(rs, rec)
			}
		})

		currentPage++
		hasNextPage, pageURL, doc, err = s.fetchPage(doc, currentPage, pageURL, globalConfig.UserAgent, nil)
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
func GQDocument(c *Config, s *Scraper, gqdoc *goquery.Document, rawDyn bool) (output.Records, error) {
	if DoDebug {
		if output.WriteSeparateLogFiles {
			prevLogger, err := output.SetDefaultLogger("/tmp/goskyr/main/"+c.ID.Slug+"_configs/"+c.ID.String()+"_scrape_GQDocument_log.txt", slog.LevelDebug)
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
	baseUrl := getBaseURL(s.URL, gqdoc)

	// recElts := strings.Split(s.Item, " > ")
	// gqdoc.Find(strings.Join(itemElts[0:len(itemElts)-1], " > ")).Each(func(i int, sel *goquery.Selection) {
	slog.Debug("in scrape.GQDocument()", "s.Item", s.Selector)
	slog.Debug("in scrape.GQDocument()", "len(doc.Find(s.Record).Nodes)", len(gqdoc.Find(s.Selector).Nodes))
	gqdoc.Find(s.Selector).Filter(s.Selector).Each(func(i int, sel *goquery.Selection) {
		// slog.Debug("in scrape.GQDocument()", "i", i, "sel.Nodes", printHTMLNodes(sel.Nodes))
		record, err := GQSelection(c, s, sel, baseUrl, rawDyn)
		if err != nil {
			slog.Warn("while scraping document got error", "baseUrl", baseUrl, "err", err.Error())
			return
		}
		if record != nil {
			rs = append(rs, record)
		}
	})

	s.guessYear(rs, time.Now())

	slog.Debug("in scrape.GQDocument()", "len(rs)", len(rs), "rs.TotalFields()", rs.TotalFields())
	return rs, nil
}

// GQSelection fetches and returns an records from a website according to the
// Scraper's paramaters. When rawDyn is set to true the record returned is not
// processed according to its type but instead the raw value based only on the
// location is returned (ignore regex_extract??). And only those of dynamic
// fields, ie fields that don't have a predefined value and that are present on
// the main page (not detail pages). This is used by the ML feature generation.
func GQSelection(c *Config, s *Scraper, sel *goquery.Selection, baseUrl string, rawDyn bool) (output.Record, error) {
	if DoDebug {
		if output.WriteSeparateLogFiles {
			prevLogger, err := output.SetDefaultLogger("/tmp/goskyr/main/"+c.ID.Slug+"_configs/"+c.ID.String()+"_scrape_GQSelection_log.txt", slog.LevelDebug)
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
	for _, f := range s.Fields {
		slog.Debug("in scrape.GQSelection(), looking at field", "f.Name", f.Name)
		if f.Value != "" {
			if !rawDyn {
				// add static fields
				rs[f.Name] = f.Value
			}
			continue
		}
		slog.Debug("in scrape.GQSelection(), before extract", "f", f)

		// handle all dynamic fields on the main page
		if f.OnDetailPage == "" {
			var err error
			if rawDyn {
				err = extractRawField(&f, rs, sel)
			} else {
				err = extractField(&f, rs, sel, baseUrl)
			}
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
	if !rawDyn {
		dpDocs := make(map[string]*goquery.Document)
		for _, f := range s.Fields {
			if f.OnDetailPage == "" || f.Value != "" {
				continue
			}

			// check whether we fetched the page already
			dpURL := fmt.Sprint(rs[f.OnDetailPage])
			_, found := dpDocs[dpURL]
			if !found {
				dpRes, err := s.fetcher.Fetch(dpURL, nil)
				if err != nil {
					return nil, fmt.Errorf("error while fetching detail page: %v. Skipping record %v.", err, rs)
				}
				dpDoc, err := goquery.NewDocumentFromReader(strings.NewReader(dpRes))
				if err != nil {
					return nil, fmt.Errorf("error while reading detail page document: %v. Skipping record %v", err, rs)
				}
				dpDocs[dpURL] = dpDoc
			}

			baseURLDetailPage := getBaseURL(dpURL, dpDocs[dpURL])
			err := extractField(&f, rs, dpDocs[dpURL].Selection, baseURLDetailPage)
			if err != nil {
				return nil, fmt.Errorf("error while parsing detail page field %s: %v. Skipping record %v.", f.Name, err, rs)
			}
			// filter fast!
			if !s.keepRecord(rs) {
				return nil, nil
			}
		}
	}
	// slog.Debug("in Scraper.GQSelection(), after rawDyn", "currentItem", rs)

	// check if item should be filtered
	if !s.keepRecord(rs) {
		return nil, nil
	}

	rs = s.removeHiddenFields(rs)
	slog.Debug("in scrape.GQSelection()", "rs", rs)
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

func (c *Scraper) GetDetailPageURLFields() []Field {
	rs := []Field{}
	for _, f := range c.Fields {
		if f.Type != "url" {
			continue
		}
		if strings.HasSuffix(f.Value, ".gif") ||
			strings.HasSuffix(f.Value, ".jfif") ||
			strings.HasSuffix(f.Value, ".jpg") ||
			strings.HasSuffix(f.Value, ".png") {
			continue
		}
		rs = append(rs, f)
	}
	return rs
}

func (c *Scraper) fetchPage(doc *goquery.Document, nextPageI int, currentPageUrl, userAgent string, i []*fetch.Interaction) (bool, string, *goquery.Document, error) {

	if nextPageI == 0 {
		newDoc, err := fetch.GQDocument(c.fetcher, currentPageUrl, &fetch.FetchOpts{Interaction: i})
		if err != nil {
			return false, "", nil, err
		}
		return true, currentPageUrl, newDoc, nil
	}

	if len(c.Paginators) == 0 {
		return false, "", nil, nil
	}

	if c.RenderJs {
		// check if node c.Paginator.Location.Selector is present in doc
		pag := c.Paginators[0]
		pagSelector := doc.Find(pag.Location.Selector)
		if len(pagSelector.Nodes) > 0 {
			if nextPageI < pag.MaxPages || pag.MaxPages == 0 {
				ia := []*fetch.Interaction{
					{
						Selector: pag.Location.Selector,
						Type:     fetch.InteractionTypeClick,
						Count:    nextPageI, // we always need to 'restart' the clicks because we always re-fetch the page
					},
				}
				nextPageDoc, err := fetch.GQDocument(c.fetcher, currentPageUrl, &fetch.FetchOpts{Interaction: ia})
				if err != nil {
					return false, "", nil, err
				}
				return true, currentPageUrl, nextPageDoc, nil
			}
		}
		return false, "", nil, nil
	}

	baseUrl := getBaseURL(currentPageUrl, doc)
	nextPageU, err := GetURL(&c.Paginators[0].Location, doc.Selection, baseUrl)
	nextPageUrl := nextPageU.String()
	if err != nil {
		return false, "", nil, err
	}
	if nextPageUrl != "" {
		nextPageDoc, err := fetch.GQDocument(c.fetcher, nextPageUrl, nil)
		if err != nil {
			return false, "", nil, err
		}
		if nextPageI < c.Paginators[0].MaxPages || c.Paginators[0].MaxPages == 0 {
			return true, nextPageUrl, nextPageDoc, nil
		}
	}

	return false, "", nil, nil
}

func extractField(field *Field, event output.Record, sel *goquery.Selection, baseURL string) error {
	slog.Debug("scrape.extractField()", "field", field, "event", event, "sel", sel, "baseURL", baseURL)
	switch field.Type {
	case "text", "": // the default, ie when type is not configured, is 'text'
		parts := []string{}
		if len(field.ElementLocations) == 0 {
			slog.Debug("in scrape.extractField(), empty field.ElementLocations")
		} else {
			for _, p := range field.ElementLocations {
				ts, err := getTextString(&p, sel)
				if err != nil {
					return err
				}
				if ts != "" {
					parts = append(parts, ts)
				}
			}
		}
		t := strings.Join(parts, field.Separator)
		if t == "" {
			// if the extracted value is an empty string assign the default value
			t = field.Default
			if !field.CanBeEmpty && t == "" {
				// if it's still empty and must not be empty return an error
				return fmt.Errorf("field %s cannot be empty", field.Name)
			}
		}
		// transform the string if required
		for _, tr := range field.Transform {
			var err error
			t, err = transformString(&tr, t)
			if err != nil {
				return err
			}
		}
		event[field.Name] = t
	case "url":
		if len(field.ElementLocations) != 1 {
			return fmt.Errorf("a field of type 'url' must exactly have one location")
		}
		u, err := GetURL(&field.ElementLocations[0], sel, baseURL)
		if err != nil {
			return err
		}
		uStr := u.String()
		if uStr == "" {
			// if the extracted value is an empty string assign the default value
			uStr = field.Default
			if !field.CanBeEmpty && uStr == "" {
				// if it's still empty and must not be empty return an error
				return fmt.Errorf("field %s cannot be empty", field.Name)
			}
		}
		event[field.Name] = uStr
	case "date":
		d, err := getDate(field, sel, dateDefaults{})
		if err != nil {
			return err
		}
		event[field.Name] = d
	default:
		return fmt.Errorf("field type '%s' does not exist", field.Type)
	}
	return nil
}

func extractRawField(field *Field, event output.Record, sel *goquery.Selection) error {
	// slog.Debug("Scraper.extractRawField()", "field", field, "event", event, "s", sel)
	switch field.Type {
	case "text", "":
		parts := []string{}
		if len(field.ElementLocations) == 0 {
			slog.Debug("in scrape.extractField(), empty field.ElementLocations")
			ts, err := getTextString(&ElementLocation{}, sel)
			if err != nil {
				return err
			}
			if ts != "" {
				parts = append(parts, ts)
			}
		} else {
			for _, p := range field.ElementLocations {
				ts, err := getTextString(&p, sel)
				if err != nil {
					return err
				}
				if ts != "" {
					parts = append(parts, ts)
				}
			}
		}
		t := strings.Join(parts, field.Separator)
		if !field.CanBeEmpty && t == "" {
			return fmt.Errorf("field %s cannot be empty", field.Name)
		}
		event[field.Name] = t
	case "url":
		if len(field.ElementLocations) != 1 {
			return fmt.Errorf("a field of type 'url' must exactly have one location")
		}
		if field.ElementLocations[0].Attr == "" {
			// normally we'd set the default in getUrlString
			// but we're not using this function for the raw extraction
			// because we don't want the url to be auto expanded
			field.ElementLocations[0].Attr = "href"
		}
		ts, err := getTextString(&field.ElementLocations[0], sel)
		if err != nil {
			return err
		}
		if !field.CanBeEmpty && ts == "" {
			return fmt.Errorf("field %s cannot be empty", field.Name)
		}
		event[field.Name] = ts
	case "date":
		cs, err := getRawDateComponents(field, sel)
		if err != nil {
			return err
		}
		for k, v := range cs {
			event[k] = v
		}
	}
	return nil
}

type datePart struct {
	stringPart  string
	layoutParts []string
}

type dateDefaults struct {
	year int
	time string // should be format 15:04
}

func getDate(f *Field, sel *goquery.Selection, dd dateDefaults) (time.Time, error) {
	// time zone
	var t time.Time
	loc, err := time.LoadLocation(f.DateLocation)
	if err != nil {
		return t, err
	}

	// locale (language)
	mLocale := "de_DE"
	if f.DateLanguage != "" {
		mLocale = f.DateLanguage
	}

	// collect all the date parts
	dateParts := []datePart{}
	combinedParts := date.CoveredDateParts{}
	for _, c := range f.Components {
		if !date.HasAllDateParts(combinedParts) {
			if err := date.CheckForDoubleDateParts(c.Covers, combinedParts); err != nil {
				return t, err
			}
			sp, err := getTextString(&c.ElementLocation, sel)
			if err != nil {
				return t, err
			}
			for _, tr := range c.Transform {
				sp, err = transformString(&tr, sp)
				// we have to return the error here instead of after the loop
				// otherwise errors might be overwritten and hence ignored.
				if err != nil {
					return t, err
				}
			}
			if sp != "" {
				dateParts = append(dateParts, datePart{
					stringPart:  sp,
					layoutParts: c.Layout,
				})
				combinedParts = date.MergeDateParts(combinedParts, c.Covers)
			}
		}
	}

	// currently not all date parts have default values
	if !combinedParts.Day || !combinedParts.Month {
		return t, errors.New("date parsing error: to generate a date at least a day and a month is needed")
	}

	// adding default values where necessary
	if !combinedParts.Year {
		if dd.year == 0 {
			dd.year = time.Now().Year()
		}
		dateParts = append(dateParts, datePart{
			stringPart:  strconv.Itoa(dd.year),
			layoutParts: []string{"2006"},
		})
	}
	if !combinedParts.Time {
		if dd.time == "" {
			dd.time = "20:00"
		}
		dateParts = append(dateParts, datePart{
			stringPart:  dd.time,
			layoutParts: []string{"15:04"},
		})
	}

	var dateTimeString string
	dateTimeLayouts := []string{""}
	for _, dp := range dateParts {
		tmpDateTimeLayouts := dateTimeLayouts
		dateTimeLayouts = []string{}
		for _, tlp := range tmpDateTimeLayouts {
			for _, lp := range dp.layoutParts {
				dateTimeLayouts = append(dateTimeLayouts, tlp+lp+" ")
			}
		}
		dateTimeString += dp.stringPart + " "
	}

	for _, dateTimeLayout := range dateTimeLayouts {
		t, err = monday.ParseInLocation(dateTimeLayout, dateTimeString, loc, monday.Locale(mLocale))
		if err == nil {
			return t, nil
		} else if !combinedParts.Year && f.GuessYear {
			// edge case, parsing time "29.02. 20:00 2023 ": day out of range
			// We set the year to the current year but it should actually be 2024
			// We only update the year string in case guess_year is set to true
			// to not confuse the user too much
			if strings.HasSuffix(err.Error(), "day out of range") && strings.Contains(err.Error(), "29") {
				for i := 1; i < 4; i++ {
					dateTimeString = strings.Replace(dateTimeString, strconv.Itoa(dd.year), strconv.Itoa(dd.year+i), 1)
					t, err = monday.ParseInLocation(dateTimeLayout, dateTimeString, loc, monday.Locale(mLocale))
					if err == nil {
						return t, nil
					}
				}
			}
		}
	}
	return t, err
}

func getRawDateComponents(f *Field, sel *goquery.Selection) (map[string]string, error) {
	rawComponents := map[string]string{}
	for _, c := range f.Components {
		ts, err := getTextString(&c.ElementLocation, sel)
		if err != nil {
			return rawComponents, err
		}
		fName := "date-component"
		if c.Covers.Day {
			fName += "-day"
		}
		if c.Covers.Month {
			fName += "-month"
		}
		if c.Covers.Year {
			fName += "-year"
		}
		if c.Covers.Time {
			fName += "-time"
		}
		rawComponents[fName] = ts
	}
	return rawComponents, nil
}

func GetURL(e *ElementLocation, sel *goquery.Selection, baseURL string) (*url.URL, error) {
	// var urlVal, urlRes string
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	if e.Attr == "" {
		// set attr to the default if not set
		e.Attr = "href"
	}
	relStr, err := getTextString(e, sel)
	if err != nil {
		return nil, err
	}

	return u.Parse(relStr)
}

// 	urlVal = strings.TrimSpace(urlVal)
// 	if urlVal == "" {
// 		return "", nil
// 	} else if strings.HasPrefix(urlVal, "http") {
// 		urlRes = urlVal
// 	} else if strings.HasPrefix(urlVal, "?") || strings.HasPrefix(urlVal, ".?") {
// 		urlVal = strings.TrimLeft(urlVal, ".")
// 		urlRes = fmt.Sprintf("%s://%s%s%s", u.Scheme, u.Host, u.Path, urlVal)
// 	} else if strings.HasPrefix(urlVal, "/") {
// 		baseURL := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
// 		urlRes = fmt.Sprintf("%s%s", baseURL, urlVal)
// 	} else if strings.HasPrefix(urlVal, "..") {
// 		partsUrlVal := strings.Split(urlVal, "/")
// 		partsPath := strings.Split(u.Path, "/")
// 		i := 0
// 		for ; i < len(partsUrlVal); i++ {
// 			if partsUrlVal[i] != ".." {
// 				break
// 			}
// 		}
// 		urlRes = fmt.Sprintf("%s://%s%s/%s", u.Scheme, u.Host, strings.Join(partsPath[:len(partsPath)-i-1], "/"), strings.Join(partsUrlVal[i:], "/"))
// 	} else {
// 		idx := strings.LastIndex(u.Path, "/")
// 		if idx > 0 {
// 			path := u.Path[:idx]
// 			urlRes = fmt.Sprintf("%s://%s%s/%s", u.Scheme, u.Host, path, urlVal)
// 		} else {
// 			urlRes = fmt.Sprintf("%s://%s/%s", u.Scheme, u.Host, urlVal)
// 		}
// 	}

// 	urlRes = strings.TrimSpace(urlRes)
// 	return urlRes, nil
// }

var DoPruning = true

var SkipTag = map[string]bool{
	"noscript": true,
	"script":   true,
	"style":    true,
}

func getTextString(e *ElementLocation, sel *goquery.Selection) (string, error) {
	// slog.Debug("getTextString()", "e", e, "s", sel)
	var fieldStrings []string
	var fieldSelection *goquery.Selection
	if e.Selector == "" {
		fieldSelection = sel
	} else {
		fieldSelection = sel.Find(e.Selector)
	}
	// slog.Debug("in getTextString()", "e.Selector", e.Selector)
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
	return strings.Join(fieldStrings, e.Separator), nil
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

func getBaseURL(pageUrl string, gqdoc *goquery.Document) string {
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
	".png":  true,
}

func DetailPages(cache fetch.Cache, c *Config, s *Scraper, recs output.Records) error {
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
		if SkipSubURLExt[filepath.Ext(relStr)] || strings.HasPrefix(relStr, "mailto:") {
			continue
		}

		rel, err := url.Parse(relStr)
		if err != nil {
			return fmt.Errorf("error parsing detail page url %q: %v", c.ID.Field, err)
		}
		subURL := uBase.ResolveReference(rel).String()
		slog.Debug("in scrape.DetailPages()", "i", i, "subURL", subURL)
		subGQDoc, found, err := fetch.GetGQDocument(cache, subURL)
		if err != nil {
			return fmt.Errorf("error fetching detail page GQDocument at %q (found: %t): %v", subURL, found, err)
		}
		if err := SubGQDocument(c, s, rec, c.ID.Field, subGQDoc); err != nil {
			return fmt.Errorf("error extending records: %v", err)
		}
	}
	return nil
}

func SubGQDocument(c *Config, s *Scraper, rec output.Record, fname string, gqdoc *goquery.Document) error {
	if DoDebug {
		if output.WriteSeparateLogFiles {
			prevLogger, err := output.SetDefaultLogger("/tmp/goskyr/main/"+c.ID.Slug+"_configs/"+c.ID.String()+"_scrape_SubGQDocument_log.txt", slog.LevelDebug)
			if err != nil {
				return err
			}
			defer output.RestoreDefaultLogger(prevLogger)
		}
		// slog = slog.With(slog.String("s.Name", s.Name))
		slog.Debug("scrape.SubGQDocument()", "fname", fname)
		defer slog.Debug("scrape.SubGQDocument() returning")
	}

	subRecs, err := GQDocument(c, s, gqdoc, true)
	if err != nil {
		return fmt.Errorf("error scraping detail page for field %q: %v", fname, err)
	}
	// The detail page may not have had valid records.
	if len(subRecs) != 1 {
		// return fmt.Errorf("error scraping detail page: expected exactly one item map for configID: %q, fname %q, got %d instead", c.ID.String(), fname, len(subRecs))
		// fmt.Printf("error scraping detail page: expected exactly one item map for configID: %q, fname %q, got %d instead", c.ID.String(), fname, len(subRecs))
		return nil
	}
	for k, v := range subRecs[0] {
		rec[fname+"__"+k] = v
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
