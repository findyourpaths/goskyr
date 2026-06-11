package scrape

import (
	"bytes"
	"context"
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

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"github.com/antchfx/jsonquery"
	"github.com/findyourpaths/goskyr/date"
	"github.com/findyourpaths/goskyr/fetch"
	"github.com/findyourpaths/goskyr/observability"
	"github.com/findyourpaths/goskyr/output"
	"github.com/findyourpaths/goskyr/utils"
	"github.com/findyourpaths/phil/datetime"
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/jpillora/go-tld"
	"github.com/kr/pretty"
	"github.com/samber/lo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/net/html"
	"gopkg.in/yaml.v3"
)

var DoDebug = true

// var DoDebug = false

var DebugGQFind = true

// var DebugGQFind = false

// ASCII separator characters for unambiguous field/record delimiting.
// These never appear in HTML content, unlike \n and \t.
const (
	// UnitSeparator separates siblings within a single matched element (entire_subtree).
	UnitSeparator = "\x1f"
	// RecordSeparator separates values from multiple matched elements (all_nodes).
	RecordSeparator = "\x1e"
	// GroupSeparator reserved for future use (groups of records).
	GroupSeparator = "\x1d"
)

// FieldPartSeparator joins text extracted from multiple ElementLocations for text fields.
// Triple-newline marks an element boundary: HTMLToMarkdown guarantees max \n\n
// within a single element, so \n\n\n unambiguously signals "different elements."
// Downstream consumers split on \n\n\n to recover per-element items.
var FieldPartSeparator = "\n\n\n"

// HTMLPartSeparator joins text extracted from multiple ElementLocations for html/markdown fields.
// Uses <br> so the separator survives HTML-to-markdown conversion (plain \n\n\n would be
// collapsed as whitespace by the HTML parser).
const HTMLPartSeparator = "<br>"

// HTMLNodeSeparator joins inner HTML from multiple matched nodes within one ElementLocation.
// Uses <br> so paragraph breaks survive HTML-to-markdown conversion.
const HTMLNodeSeparator = "<br>"

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

	compact bool
}

// WithCompact returns a copy of cid whose String form omits URL-derived slug provenance.
func (cid ConfigID) WithCompact(v bool) ConfigID {
	cid.compact = v
	return cid
}

// String converts a ConfigID to its string representation by joining its components
// with underscores, creating a hierarchical identifier.
func (cid ConfigID) String() string {
	if cid.compact {
		return compactConfigIDString(cid)
	}

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

func compactConfigIDString(cid ConfigID) string {
	parts := []string{}
	if cid.ID != "" {
		parts = append(parts, cid.ID)
	}
	if cid.Field != "" {
		parts = append(parts, cid.Field)
	}
	if cid.SubID != "" {
		parts = append(parts, cid.SubID)
	}
	return strings.ToLower(strings.Join(parts, "-"))
}

// Copy creates a deep copy of the Config including all records.
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

// String converts a Config to its YAML representation, excluding records.
func (c Config) String() string {
	cCopy := c
	cCopy.Records = nil
	yamlData, err := yaml.Marshal(&cCopy)
	if err != nil {
		log.Fatalf("error while marshaling config. %v", err)
	}
	return string(yamlData)
}

// WriteToFile writes the Config to a YAML file and optionally writes records to a JSON file
// in the specified directory.
func (c Config) WriteToFile(dir string) error {
	if err := utils.WriteStringFile(filepath.Join(dir, c.ID.String()+".yml"), c.String()); err != nil {
		return err
	}
	if len(c.Records) > 0 {
		jsonPath := filepath.Join(dir, fmt.Sprintf("%s_%d.json", c.ID.String(), len(c.Records)))
		if err := utils.WriteStringFile(jsonPath, c.Records.String()); err != nil {
			return err
		}
		fmt.Printf("Extracted records to: %s\n", jsonPath)
	}
	return nil
}

// ReadConfig reads a scraper configuration from a YAML file or directory of YAML files.
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

	// Initialize derived fields for all scrapers
	for i := range config.Scrapers {
		s := &config.Scrapers[i]
		for j := range s.DerivedFields {
			if err := s.DerivedFields[j].Initialize(); err != nil {
				return nil, fmt.Errorf("initializing derived field %d in scraper %q: %w", j, s.Name, err)
			}
		}
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
	Selector       string      `yaml:"selector,omitempty"`
	JsonSelector   string      `yaml:"json_selector,omitempty"`
	ChildIndex     int         `yaml:"child_index,omitempty"`
	RegexExtract   RegexConfig `yaml:"regex_extract,omitempty"`
	Attr           string      `yaml:"attr,omitempty"`
	MaxLength      int         `yaml:"max_length,omitempty"`
	EntireSubtree  bool        `yaml:"entire_subtree,omitempty"`
	AllNodes       bool        `yaml:"all_nodes,omitempty"`
	Separator      string      `yaml:"separator,omitempty"`       // Intra-node sibling separator (default: \x1F)
	NodeSeparator  string      `yaml:"node_separator,omitempty"`  // Inter-node separator (default: \x1E)
	StripTags      bool        `yaml:"strip_tags,omitempty"`      // Only insert separators between block-level elements
	CollapseSpaces bool        `yaml:"collapse_spaces,omitempty"` // Collapse runs of 2+ spaces to single space
	UntilSelector  string      `yaml:"until_selector,omitempty"`  // Stop extracting text when hitting a child matching this CSS selector
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
//
// A Field with Fields (subfields) produces nested output:
//   - name: links
//     fields:
//   - name: raw_url
//     location: [...]
//   - name: role
//     value: detail
//
// Output: {"links": {"raw_url": "https://...", "role": "detail"}}
// Multiple same-name Fields produce a slice: {"links": [map1, map2]}
type Field struct {
	Name             string           `yaml:"name"`
	Value            string           `yaml:"value,omitempty"`
	Type             string           `yaml:"type,omitempty"`     // can be text (default), html, url, or date_time_tz_ranges
	Fields           []Field          `yaml:"fields,omitempty"`   // subfields — produces nested map output
	ElementLocations ElementLocations `yaml:"location,omitempty"` // elements are extracted strings joined with newlines
	Default          string           `yaml:"default,omitempty"`  // the default for a dynamic field (text or url) if no value is found
	// If a field can be found on a detail page the following variable has to
	// contain a field name of a field of type 'url' that is located on the main
	// page.
	OnDetailPage   string            `yaml:"on_detail_page,omitempty"`  // applies to text, url, date
	Required       bool              `yaml:"required,omitempty"`        // applies to text, url - if true, skip record when field is empty
	Components     []DateComponent   `yaml:"components,omitempty"`      // applies to date
	DateLocation   string            `yaml:"date_location,omitempty"`   // applies to date
	DateLanguage   string            `yaml:"date_language,omitempty"`   // applies to date
	Hide           bool              `yaml:"hide,omitempty"`            // applies to text, url, date
	GuessYear      bool              `yaml:"guess_year,omitempty"`      // applies to date
	Transform      []TransformConfig `yaml:"transform,omitempty"`       // applies to text
	StripTags      bool              `yaml:"strip_tags,omitempty"`      // Only insert separators between block-level elements
	CollapseSpaces bool              `yaml:"collapse_spaces,omitempty"` // Collapse runs of 2+ spaces to single space
}

type ElementLocations []ElementLocation

// UnmarshalYAML handles YAML unmarshalling for ElementLocations, accepting either a single
// ElementLocation or a list of ElementLocations.
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
	Field           string `yaml:"field"`
	Type            string
	Expression      string `yaml:"exp"` // changed from 'regex' to 'exp' in version 0.5.7
	RegexComp       *regexp.Regexp
	DateComp        time.Time
	DateOp          string
	Match           bool   `yaml:"match"`
	Condition       string `yaml:"condition,omitempty"`        // matches, not_matches, missing, missing_or_matches, exists
	CaseInsensitive bool   `yaml:"case_insensitive,omitempty"` // for regex matching
}

// FilterMatch checks if a value matches the filter's criteria based on regex or date comparison.
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

// FilterMatchWithCondition checks the filter against record with extended conditions.
// Returns true if the record should be KEPT.
func (f *Filter) FilterMatchWithCondition(rec map[string]interface{}) bool {
	value, exists := rec[f.Field]

	switch f.Condition {
	case "missing":
		return !exists
	case "exists":
		return exists
	case "missing_or_matches":
		if !exists {
			return true // missing = keep
		}
		// Field exists, check if it matches
		return f.RegexComp.MatchString(fmt.Sprint(value))
	case "not_matches":
		if !exists {
			return true // missing fields don't match, so keep
		}
		return !f.RegexComp.MatchString(fmt.Sprint(value))
	case "matches", "":
		if !exists {
			return false // can't match if missing
		}
		return f.RegexComp.MatchString(fmt.Sprint(value))
	default:
		// Fall back to original behavior
		if !exists {
			return false
		}
		return f.FilterMatch(value)
	}
}

// Initialize compiles the filter's regex pattern or parses date comparison expressions.
func (f *Filter) Initialize(fieldType string) error {
	if fieldType == "date" {
		f.Type = "date"
	} else {
		f.Type = "regex" // default for everything except date fields
	}
	switch f.Type {
	case "regex":
		pattern := f.Expression
		if f.CaseInsensitive {
			pattern = "(?i)" + pattern
		}
		regex, err := regexp.Compile(pattern)
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

// FetchConfig controls how pages are fetched (JS rendering, waits, etc.)
type FetchConfig struct {
	UseJavascript          bool   `yaml:"use_javascript,omitempty"`           // Enable headless browser
	WaitSelector           string `yaml:"wait_selector,omitempty"`            // CSS selector to wait for
	WaitTimeoutMs          int    `yaml:"wait_timeout_ms,omitempty"`          // Timeout for wait (default 30000)
	FerretQL               string `yaml:"ferret_ql,omitempty"`                // Custom FerretQL script (advanced)
	Script                 string `yaml:"script,omitempty"`                   // JavaScript to run after page load (Rod only, runs before scraping)
	InfiniteScrollSelector string `yaml:"infinite_scroll_selector,omitempty"` // CSS selector for "Load More" button (Rod clicks it repeatedly)
}

// Pagination configures multi-page extraction
type Pagination struct {
	Type           string `yaml:"type"`            // query_param, scroll, next_button
	ParamName      string `yaml:"param_name"`      // for query_param: e.g. "start", "page"
	StartValue     int    `yaml:"start_value"`     // starting value (usually 0)
	Increment      int    `yaml:"increment"`       // increment per page
	MaxPages       int    `yaml:"max_pages"`       // safety limit
	ButtonSelector string `yaml:"button_selector"` // for scroll/next_button types
	WaitMs         int    `yaml:"wait_ms"`         // delay between pages (milliseconds)
}

// A Scraper contains all the necessary config parameters and structs needed
// to extract the desired information from a website
type Scraper struct {
	Interaction  []*fetch.Interaction `yaml:"interaction,omitempty"`
	Name         string               `yaml:"name"`
	PageLoadWait int                  `yaml:"page_load_wait,omitempty"` // milliseconds. Only taken into account when render_js = true
	RenderJs     bool                 `yaml:"render_js,omitempty"`
	Selector     string               `yaml:"selector"`
	Strategy     string               `yaml:"strategy,omitempty"` // "nested" (default) or "sequential"
	URL          string               `yaml:"url"`
	Validation   *ValidationConfig    `yaml:"validation,omitempty"`
	Fields       []Field              `yaml:"fields,omitempty"`
	Filters      []*Filter            `yaml:"filters,omitempty"`
	Paginators   []Paginator          `yaml:"paginators,omitempty"`

	// New declarative config fields
	Fetch         *FetchConfig   `yaml:"fetch,omitempty"`          // Fetch configuration
	Pagination    *Pagination    `yaml:"pagination,omitempty"`     // Declarative pagination
	DerivedFields []DerivedField `yaml:"derived_fields,omitempty"` // Template-based field derivation

	// MergeKey makes this an independent scraper (not a detail-page follower).
	// When set, this scraper is run separately via Page() and its records are
	// merged into the primary scraper's records by matching on this field name.
	// The field must exist in both scrapers' output.
	MergeKey string `yaml:"merge_key,omitempty"`
}

type ValidationConfig struct {
	RequiresCTASelector string `yaml:"requires_cta_selector,omitempty"`
}

// HostSlug extracts and returns a URL slug from the scraper's URL host.
func (s Scraper) HostSlug() string {
	host := s.URL[strings.Index(s.URL, "//")+2:]
	end := strings.Index(host, "/")
	if end == -1 {
		end = len(host)
	}
	host = host[:end]
	return fetch.MakeURLStringSlug(host)
}

// FindByName returns a pointer to the named scraper within config.
// Returns nil if config is nil, has no scrapers, or no scraper matches the name.
// Exact name match only — no fallback.
func FindByName(config *Config, scraperName string) *Scraper {
	if config == nil || len(config.Scrapers) == 0 {
		return nil
	}
	for i := range config.Scrapers {
		if config.Scrapers[i].Name == scraperName {
			return &config.Scrapers[i]
		}
	}
	return nil
}

// Page fetches and returns all records from a webpage according to the
// Scraper's paramaters. When rawDyn is set to true the records returned are
// not processed according to their type but instead the raw values based
// only on the location are returned (ignore regex_extract??). And only those
// of dynamic fields, ie fields that don't have a predefined value and that are
// present on the main page (not detail pages). This is used by the ML feature generation.
func Page(ctx context.Context, cache fetch.Cache, c *Config, s *Scraper, globalConfig *GlobalConfig, rawDyn bool, path string) (output.Records, error) {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/scrape").Start(ctx, "scrape.Page")

	// Metering
	// source := "error"
	defer func() {
		// entity.observability.Add(ctx, insts.Generate, 1,
		// 	// attribute.String("source", source),
		// 	attribute.Int("arg.i", i),
		// 	attribute.Int64("arg.gmail_id", ret.Email.GmailId),
		// 	attribute.String("ret.title", ret.Title),
		// 	attribute.Int("ret.images.len", len(ret.Images)),
		// 	attribute.Int("ret.links.len", len(ret.Links)),
		// 	attribute.Int("ret.datetime_ranges.len", len(ret.DatetimeRanges)),
		// )
		span.End()
	}()

	// Logging
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
	// Track visited URLs to prevent cycles (e.g., "Previous" link back to page 1).
	// Normalize via url.Parse to canonicalize path (trailing slash) and query.
	normURL := func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		return parsed.String()
	}
	visited := map[string]bool{normURL(u): true}

	hasNextPage, pageURL, gqdoc, err := s.fetchPage(cache, nil, currentPage, u, globalConfig.UserAgent, s.Interaction)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch next page %q: %w", u, err)
	}

	for hasNextPage {
		if gqdoc == nil {
			return nil, fmt.Errorf("fetch returned nil document without error for URL %q (page %d)", pageURL, currentPage)
		}

		recs, err := GQDocument(ctx, c, s, gqdoc)
		if err != nil {
			return nil, err
		}
		// Set Aurl to the actual page URL from the pagination loop.
		// GQDocument uses s.URL (the scraper's base URL), which is wrong
		// for paginated pages.
		for _, r := range recs {
			r[URLFieldName] = pageURL
		}
		rs = append(rs, recs...)

		currentPage++
		hasNextPage, pageURL, gqdoc, err = s.fetchPage(cache, gqdoc, currentPage, pageURL, globalConfig.UserAgent, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch next page: %w", err)
		}
		if hasNextPage && visited[normURL(pageURL)] {
			slog.Debug("pagination loop: already visited URL, stopping", "url", pageURL, "page", currentPage)
			break
		}
		visited[normURL(pageURL)] = true
	}

	s.guessYear(rs, guessYearRef(ctx))

	slog.Debug("in scrape.Page()", "len(rs)", len(rs), "rs.TotalFields()", rs.TotalFields())
	return rs, nil
}

// GQDocument fetches and returns all records from a website according to the
// Scraper's paramaters. When rawDyn is set to true the records returned are
// not processed according to their type but instead the raw values based
// only on the location are returned (ignore regex_extract??). And only those
// of dynamic fields, ie fields that don't have a predefined value and that are
// present on the main page (not detail pages). This is used by the ML feature generation.
func GQDocument(ctx context.Context, c *Config, s *Scraper, gqdoc *fetch.Document) (output.Records, error) {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/scrape").Start(ctx, fmt.Sprintf("scrape.GQDocument(%q, %q)", c.ID.String(), s.Selector))

	// Metering
	// source := "error"
	var count int
	var rets output.Records
	// var rsStr string
	defer func() {
		var counter metric.Int64Counter
		if observability.Instruments != nil {
			counter = observability.Instruments.Scrape
		}
		observability.Add(ctx, counter, 1,
			// 	// attribute.String("source", source),
			attribute.String("arg.config.id", c.ID.String()),
			attribute.String("arg.scraper.selector", s.Selector),
			attribute.Int("arg.scraper.found_nodes.len", len(gqdoc.Find(s.Selector).Nodes)),
			attribute.Int("int.count", count),
			// 	attribute.Int64("arg.gmail_id", ret.Email.GmailId),
			// 	attribute.String("ret.title", ret.Title),
			attribute.Int("rets.len", len(rets)),
			attribute.String("rets", fmt.Sprintf("%# v\n", pretty.Formatter(rets))),

			// attribute.Int("ret.total_fields", recs.TotalFields()),
		// 	attribute.Int("ret.links.len", len(ret.Links)),
		// 	attribute.Int("ret.datetime_ranges.len", len(ret.DatetimeRanges)),
		)
		span.End()
	}()

	// Logging
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

	baseURL := getBaseURL(s.URL, gqdoc)

	// recElts := strings.Split(s.Item, " > ")
	// gqdoc.Document.Find(strings.Join(itemElts[0:len(itemElts)-1], " > ")).Each(func(i int, sel *cache.Selection) {
	slog.Debug("in scrape.GQDocument()", "s.Selector", s.Selector)

	found := gqdoc.Document.Selection

	// If the document URL has a fragment, scope to the element with that ID.
	// This ensures cross-type traversal URLs targeting a specific element
	// (e.g., a speaker popup) extract from that element, not the first match.
	if gqdoc.Url != nil && gqdoc.Url.Fragment != "" {
		fragSel := gqdoc.Document.Find("#" + gqdoc.Url.Fragment)
		if fragSel.Length() > 0 {
			found = fragSel
			slog.Debug("in scrape.GQDocument(), scoped to URL fragment", "fragment", gqdoc.Url.Fragment)
		}
	}

	if s.Selector != "" {
		selfMatch := found.Filter(s.Selector)
		descMatch := found.Find(s.Selector)
		found = selfMatch.AddSelection(descMatch)
		if DebugGQFind && len(found.Nodes) == 0 {
			fmt.Printf("Trying to scrape from %q\n", s.URL)
			fmt.Printf("Found no nodes for original selector: %q\n", s.Selector)
			printGQFindDebug(gqdoc, s.Selector)
			return nil, nil
		}
	}

	if s.Strategy == "sequential" {
		rets, err := scrapeSequential(ctx, c, s, found, baseURL, gqdoc)
		if err != nil {
			return nil, err
		}
		s.guessYear(rets, guessYearRef(ctx))
		slog.Debug("in scrape.GQDocument() sequential", "len(rets)", len(rets), "rets.TotalFields()", rets.TotalFields())
		return rets, nil
	}

	found.Each(func(i int, sel *goquery.Selection) {
		count = i + 1
		slog.Debug("in scrape.GQDocument()", "i", i, "sel.Nodes", printHTMLNodes(sel.Nodes))
		r, err := GQSelection(ctx, c, s, fetch.NewSelection(sel), baseURL)
		if err != nil {
			slog.Warn("while scraping document got error", "baseUrl", baseURL, "err", err.Error())
			// Include record with error for downstream processing (even if empty)
			if r == nil {
				r = make(output.Record)
			}
			r[URLFieldName] = baseURL
			r[TitleFieldName] = gqdoc.Find("title").Text()
			r["_error"] = err.Error()
			rets = append(rets, r)
			return
		}
		if len(r) == 0 {
			return
		}
		r[URLFieldName] = baseURL
		r[TitleFieldName] = gqdoc.Find("title").Text()
		// fmt.Println("in scrape.GQDocument()", "r[URLFieldName]", r[URLFieldName])
		// fmt.Println("in scrape.GQDocument()", "rs", rs)
		rets = append(rets, r)
		// rsStr = rsStr + fmt.Sprintf("%#v\n", r)
	})

	s.guessYear(rets, time.Now())

	slog.Debug("in scrape.GQDocument()", "len(rs)", len(rets), "rs.TotalFields()", rets.TotalFields())
	// fmt.Println("in scrape.GQDocument()", "len(rs)", len(rs), "rs.TotalFields()", rs.TotalFields())
	return rets, nil
}

// cloneHTMLNode creates a deep copy of an HTML node and all its descendants.
func cloneHTMLNode(n *html.Node) *html.Node {
	clone := &html.Node{
		Type:      n.Type,
		DataAtom:  n.DataAtom,
		Data:      n.Data,
		Namespace: n.Namespace,
		Attr:      make([]html.Attribute, len(n.Attr)),
	}
	copy(clone.Attr, n.Attr)

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		clonedChild := cloneHTMLNode(child)
		clone.AppendChild(clonedChild)
	}

	return clone
}

// isDateElement checks if a goquery selection contains date-related text by examining direct
// text content and immediate children for date patterns.
func isDateElement(sel *goquery.Selection, s *Scraper) bool {
	// Get direct text content (not from descendants)
	// We need to check if THIS element directly contains a date, not if any descendant does
	var directText string
	sel.Contents().Each(func(i int, content *goquery.Selection) {
		// Only get text nodes (type 3), not element nodes
		if len(content.Nodes) > 0 && content.Nodes[0].Type == html.TextNode {
			directText += content.Text()
		}
	})

	// Also check immediate children for date text
	// This handles cases like <div><span>Feb 3, 2023</span></div>
	childText := ""
	sel.Children().Each(func(i int, child *goquery.Selection) {
		childText += " " + child.Text()
	})

	combined := strings.TrimSpace(directText + " " + childText)
	if combined == "" {
		return false
	}

	// Check if text matches date patterns
	if DateRE.MatchString(combined) {
		return true
	}

	return false
}

// isDescendantOfAny checks if an HTML node is a descendant of any node in the provided ancestor map.
func isDescendantOfAny(n *html.Node, ancestors map[*html.Node]bool) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if ancestors[p] {
			return true
		}
	}
	return false
}

// scrapeSequential implements the sequential extraction strategy by chunking elements based on
// date boundaries and extracting fields from each chunk to build records.
func scrapeSequential(ctx context.Context, c *Config, s *Scraper, parentSel *goquery.Selection, baseURL string, gqdoc *fetch.Document) (output.Records, error) {
	slog.Debug("scrapeSequential()")
	defer slog.Debug("scrapeSequential() returning")

	// First pass: initial chunking by date boundaries
	var initialChunks [][]*goquery.Selection
	var currentChunk []*goquery.Selection
	var foundFirstDate bool

	parentSel.Children().Each(func(i int, childSel *goquery.Selection) {
		isDate := isDateElement(childSel, s)

		if isDate {
			if foundFirstDate && len(currentChunk) > 0 {
				initialChunks = append(initialChunks, currentChunk)
			}
			currentChunk = []*goquery.Selection{childSel}
			foundFirstDate = true
		} else if foundFirstDate {
			currentChunk = append(currentChunk, childSel)
		}
	})

	if foundFirstDate && len(currentChunk) > 0 {
		initialChunks = append(initialChunks, currentChunk)
		slog.Debug("scrapeSequential() saved final initial chunk", "len", len(currentChunk))
	}

	slog.Debug("scrapeSequential() found initial chunks", "count", len(initialChunks))

	// Second pass: split chunks that contain multiple date-bearing sections
	// This handles the case where dates are in section A and descriptions in section B
	var chunks [][]*goquery.Selection
	for _, chunk := range initialChunks {
		// Find all elements with dates in this chunk
		var dateIndices []int
		for i, sel := range chunk {
			if isDateElement(sel, s) {
				dateIndices = append(dateIndices, i)
			}
		}

		if len(dateIndices) <= 1 {
			// Single date or no dates - keep chunk as is
			chunks = append(chunks, chunk)
			continue
		}

		// Multiple dates found - split at each date boundary
		slog.Debug("scrapeSequential() splitting chunk with multiple dates", "dateCount", len(dateIndices))
		for di, dateIdx := range dateIndices {
			var subChunk []*goquery.Selection

			// Include the date element
			subChunk = append(subChunk, chunk[dateIdx])

			// Determine the end boundary for this subchunk
			endIdx := len(chunk)
			if di+1 < len(dateIndices) {
				endIdx = dateIndices[di+1]
			}

			// Include elements between this date and the next date (or end)
			for i := dateIdx + 1; i < endIdx; i++ {
				subChunk = append(subChunk, chunk[i])
			}

			chunks = append(chunks, subChunk)
			slog.Debug("scrapeSequential() created subchunk", "dateIdx", dateIdx, "subChunkLen", len(subChunk))
		}
	}

	slog.Debug("scrapeSequential() found chunks after split", "count", len(chunks))

	var ctaSelector string
	if s.Validation != nil {
		ctaSelector = s.Validation.RequiresCTASelector
	}

	var rets output.Records
	for chunkIdx, chunk := range chunks {
		slog.Debug("scrapeSequential() processing chunk", "chunkIdx", chunkIdx, "len", len(chunk))

		var hasDate bool
		var hasCTA bool

		// Validate chunk
		for _, sel := range chunk {
			if isDateElement(sel, s) {
				hasDate = true
			}
			if ctaSelector != "" && len(sel.Find(ctaSelector).Nodes) > 0 {
				hasCTA = true
			}
		}

		if !hasDate {
			slog.Debug("scrapeSequential() skipping chunk without date", "chunkIdx", chunkIdx)
			continue
		}
		if ctaSelector != "" && !hasCTA {
			slog.Debug("scrapeSequential() skipping chunk without CTA", "chunkIdx", chunkIdx, "ctaSelector", ctaSelector)
			continue
		}

		slog.Debug("scrapeSequential() chunk validated", "chunkIdx", chunkIdx, "hasDate", hasDate, "hasCTA", hasCTA)

		// Extract fields from the chunk
		// Try each element in the chunk and extract fields from it
		r := output.Record{}
		for _, field := range s.Fields {
			// Try to extract this field from each element in the chunk
			for _, chunkElem := range chunk {
				err := extractField(ctx, &field, r, fetch.NewSelection(chunkElem), baseURL, 0)
				if err != nil {
					slog.Debug("scrapeSequential() error extracting field from chunk element", "chunkIdx", chunkIdx, "field", field.Name, "err", err.Error())
				}
				// If we successfully extracted this field, stop trying other elements
				if r[field.Name] != nil && r[field.Name] != "" {
					slog.Debug("scrapeSequential() successfully extracted field", "chunkIdx", chunkIdx, "field", field.Name, "value", r[field.Name])
					break
				}
			}
		}

		if len(r) == 0 {
			slog.Debug("scrapeSequential() chunk produced empty record", "chunkIdx", chunkIdx)
			continue
		}

		r[URLFieldName] = baseURL
		r[TitleFieldName] = gqdoc.Find("title").Text()
		rets = append(rets, r)
		slog.Debug("scrapeSequential() added record", "chunkIdx", chunkIdx, "totalRecords", len(rets))
	}

	slog.Debug("scrapeSequential() completed", "totalRecords", len(rets))
	return rets, nil
}

// printGQFindDebug prints debug information about CSS selector matching by progressively testing
// each part of the selector to identify where matching fails.
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
func GQSelection(ctx context.Context, c *Config, s *Scraper, sel *fetch.Selection, baseURL string) (output.Record, error) {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/scrape").Start(ctx, fmt.Sprintf("scrape.GQSelection()"))

	// Metering
	// // source := "error"
	rets := output.Record{}
	defer func() {
		var counter metric.Int64Counter
		if observability.Instruments != nil {
			counter = observability.Instruments.Scrape
		}
		observability.Add(ctx, counter, 1, // 	// attribute.String("source", source),
			// attribute.String("arg.scraper.selector", s.Selector),
			// attribute.Int("arg.scraper.found_nodes.len", len(gqdoc.Find(s.Selector).Nodes)),
			// attribute.Int("int.count", count),
			// 	attribute.Int64("arg.gmail_id", ret.Email.GmailId),
			// 	attribute.String("ret.title", ret.Title),
			// attribute.Int("ret.len", len(rs)),
			// attribute.Int("ret.total_fields", rs.TotalFields()),
			// 	attribute.Int("ret.links.len", len(ret.Links)),
			// 	attribute.Int("ret.datetime_ranges.len", len(ret.DatetimeRanges)),
			attribute.String("fields", strings.Join(lo.Map(s.Fields, func(f Field, i int) string {
				return f.Name
			}), "\n")),
			attribute.String("rets", fmt.Sprintf("%# v\n", pretty.Formatter(rets))),
		)
		span.End()
	}()

	// Logging
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

	fs := s.Fields
	sort.Slice(fs, func(i, j int) bool { return fs[i].Type == "url" })
	for _, f := range fs {
		slog.Debug("in scrape.GQSelection(), looking at field", "f.Name", f.Name)

		// Constant value — no DOM extraction needed.
		if f.Value != "" {
			rets[f.Name] = f.Value
			continue
		}

		// Nested field — extract subfields into a map, merge into record.
		// When a subfield's selector matches multiple DOM elements, goskyr joins
		// values with \x1e. Split these into separate sub-entities so each gets
		// its own map (e.g., two instructor <a> tags → two Link maps).
		if len(f.Fields) > 0 {
			subMap := extractSubfields(ctx, f.Fields, sel, baseURL)
			if len(subMap) > 0 {
				for _, m := range splitSubMapBySeparator(subMap) {
					mergeNestedField(rets, f.Name, m)
				}
			}
			continue
		}

		slog.Debug("in scrape.GQSelection(), before extract", "f", f)

		// handle all dynamic fields on the main page
		if f.OnDetailPage == "" {
			var err error
			err = extractField(ctx, &f, rets, sel, baseURL, 0)
			if err != nil {
				return nil, fmt.Errorf("error while parsing field %s: %v. Skipping rs %v.", f.Name, err, rets)
			}
		}
		slog.Debug("in scrape.GQSelection(), after extract", "f", f)

		// To speed things up we check the filter after each field.  Like that we
		// safe time if we already know for sure that we want to filter out a
		// certain record. Especially, if certain elements would need to be fetched
		// from detail pages.
		//
		// Filter fast!
		if !s.keepRecord(rets) {
			return nil, nil
		}
	}

	// check if item should be filtered
	if !s.keepRecord(rets) {
		return nil, nil
	}

	// Apply derived fields (template-based field parsing)
	if len(s.DerivedFields) > 0 {
		if err := ApplyDerivedFields(s.DerivedFields, rets); err != nil {
			return nil, fmt.Errorf("applying derived fields: %w", err)
		}
	}

	rets = s.removeHiddenFields(rets)
	// fmt.Println("s.numNonEmptyFields(rs)", s.numNonEmptyFields(rs))
	// if s.numNonEmptyFields(rs) == 0 {
	// 	return nil, nil
	// }

	slog.Debug("in scrape.GQSelection()", "rets", rets)
	// fmt.Println("in scrape.GQSelection()", "rs", rs)
	return rets, nil
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

// guessYear attempts to assign years to dates without year information by comparing them to
// reference dates and choosing the year that minimizes the time difference.
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

// initializeFilters compiles regex patterns and parses date expressions for all filters
// based on field types.
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

// keepRecord checks if a record passes all filter criteria, returning true if the record
// should be kept.
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

// removeHiddenFields removes fields marked as hidden from a record.
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

// GetDetailPageURLFields returns all URL-type fields that can be used to navigate to detail pages.
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

// fetchPage fetches the initial page or follows pagination links to retrieve subsequent pages.
func (c *Scraper) fetchPage(cache fetch.Cache, gqdoc *fetch.Document, nextPageI int, currentPageURL, userAgent string, i []*fetch.Interaction) (bool, string, *fetch.Document, error) {
	// fmt.Println("scrape.Scraper.fetchPage()", "nextPageI", nextPageI, "currentPageURL", currentPageURL)
	if nextPageI == 0 {
		newDoc, _, err := fetch.GetGQDocument(cache, currentPageURL) //, &fetch.FetchOpts{Interaction: i})
		if err != nil {
			return false, "", nil, fmt.Errorf("fetching page %q: %w", currentPageURL, err)
		}
		if newDoc == nil {
			return false, "", nil, fmt.Errorf("fetch returned nil document for URL %q (no error returned)", currentPageURL)
		}
		return true, currentPageURL, newDoc, nil
	}

	if len(c.Paginators) == 0 {
		return false, "", nil, nil
	}

	if c.RenderJs {
		// check if node c.Paginator.Location.Selector is present in doc
		pag := c.Paginators[0]
		if strings.EqualFold(strings.TrimSpace(pag.Location.Attr), "href") {
			baseURL := getBaseURL(currentPageURL, gqdoc)
			_, nextPageUU, err := GetTextStringAndURL(&pag.Location, fetch.NewSelection(gqdoc.Document.Selection), baseURL)
			nextPageURL := nextPageUU.String()
			if err != nil {
				return false, "", nil, err
			}
			if nextPageURL != "" {
				if pag.MaxPages > 0 && nextPageI >= pag.MaxPages {
					return false, "", nil, nil
				}
				nextPageDoc, _, err := fetch.GetGQDocument(cache, nextPageURL)
				if err != nil {
					return false, "", nil, fmt.Errorf("fetching paginated page %q (page %d): %w", nextPageURL, nextPageI, err)
				}
				if nextPageDoc == nil {
					return false, "", nil, fmt.Errorf("fetch returned nil document for paginated URL %q (page %d, no error returned)", nextPageURL, nextPageI)
				}
				return true, nextPageURL, nextPageDoc, nil
			}
			return false, "", nil, nil
		}
		pagSelector := gqdoc.Find(pag.Location.Selector)
		if len(pagSelector.Nodes) > 0 {
			if nextPageI < pag.MaxPages || pag.MaxPages == 0 {
				// ia := []*fetch.Interaction{
				// 	{
				// 		Selector: pag.Location.Selector,
				// 		Type:     fetch.InteractionTypeClick,
				// 		Count:    nextPageI, // we always need to 'restart' the clicks because we always re-fetch the page
				// 	},
				// }
				nextPageDoc, _, err := fetch.GetGQDocument(cache, currentPageURL) //, &fetch.FetchOpts{Interaction: ia})
				if err != nil {
					return false, "", nil, fmt.Errorf("fetching paginated page %q (page %d): %w", currentPageURL, nextPageI, err)
				}
				if nextPageDoc == nil {
					return false, "", nil, fmt.Errorf("fetch returned nil document for paginated URL %q (page %d, no error returned)", currentPageURL, nextPageI)
				}
				return true, currentPageURL, nextPageDoc, nil
			}
		}
		return false, "", nil, nil
	}

	baseURL := getBaseURL(currentPageURL, gqdoc)
	pag := c.Paginators[0]
	_, nextPageUU, err := GetTextStringAndURL(&pag.Location, fetch.NewSelection(gqdoc.Document.Selection), baseURL)
	nextPageURL := nextPageUU.String()

	if err != nil {
		return false, "", nil, err
	}
	if nextPageURL != "" {
		if pag.MaxPages > 0 && nextPageI >= pag.MaxPages {
			return false, "", nil, nil
		}
		nextPageDoc, _, err := fetch.GetGQDocument(cache, nextPageURL) //, nil)
		if err != nil {
			return false, "", nil, fmt.Errorf("fetching next page %q (page %d): %w", nextPageURL, nextPageI, err)
		}
		if nextPageDoc == nil {
			return false, "", nil, fmt.Errorf("fetch returned nil document for next page URL %q (page %d, no error returned)", nextPageURL, nextPageI)
		}
		return true, nextPageURL, nextPageDoc, nil
	}

	return false, "", nil, nil
}

func addOrReplaceQueryParam(rawURL string, paramName string, paramValue string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set(paramName, paramValue)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

var URLFieldName = "Aurl"
var URLFieldSuffix = "__" + URLFieldName
var TitleFieldName = "Atitle"
var TitleFieldSuffix = "__" + TitleFieldName
var DateTimeFieldSuffix = "__" + DateTimeFieldName
var DateTimeFieldName = "Pdate_time_tz_ranges"

var DateRE = regexp.MustCompile(`(?i)\b(20\d{2}|January|February|March|April|May|June|July|August|September|October|November|December|Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec|Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday|Mon|Tue|Wed|Thu|Fri|Sat|Sun)\b`)
var yearRE = regexp.MustCompile(`(?i)\b(20[0-9][0-9])\b`)

// extractSubfields extracts each subfield into a flat map.
// Subfields with Value set produce constants. Subfields with Fields recurse.
// Subfields with Location extract from the DOM.
func extractSubfields(ctx context.Context, fields []Field, sel *fetch.Selection, baseURL string) map[string]any {
	result := output.Record{}
	for _, sf := range fields {
		if sf.Value != "" {
			result[sf.Name] = sf.Value
			continue
		}
		if len(sf.Fields) > 0 {
			sub := extractSubfields(ctx, sf.Fields, sel, baseURL)
			if len(sub) > 0 {
				mergeNestedField(result, sf.Name, sub)
			}
			continue
		}
		if sf.OnDetailPage == "" {
			if err := extractField(ctx, &sf, result, sel, baseURL, 0); err != nil {
				slog.Debug("extractSubfields: field extraction failed", "field", sf.Name, "err", err)
			}
		}
	}
	return result
}

// splitSubMapBySeparator splits a nested map into multiple maps when a URL-type
// value contains \x1e (record separator). This handles the case where a subfield's
// CSS selector matches multiple DOM elements (e.g., two instructor <a> tags).
// Only splits when the map contains URL keys — text-only maps (schedule, descriptions)
// keep their \x1e as content separators (converted to \n by cleanMapSeparators).
// Returns the original map in a single-element slice when no splitting is needed.
func splitSubMapBySeparator(m map[string]any) []map[string]any {
	// Only split when a URL-type key has \x1e — indicates separate entities.
	// Text-only maps with \x1e represent multi-line content, not separate entities.
	hasURLKey := false
	maxParts := 1
	for k, v := range m {
		isURL := strings.HasSuffix(k, "url") || strings.HasSuffix(k, "href")
		if s, ok := v.(string); ok && isURL {
			if n := strings.Count(s, "\x1e") + 1; n > maxParts {
				maxParts = n
				hasURLKey = true
			}
		}
	}
	if maxParts == 1 || !hasURLKey {
		return []map[string]any{m}
	}
	result := make([]map[string]any, maxParts)
	for i := range result {
		result[i] = make(map[string]any, len(m))
	}
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			for i := range result {
				result[i][k] = v
			}
			continue
		}
		parts := strings.Split(s, "\x1e")
		for i := range result {
			if i < len(parts) {
				result[i][k] = parts[i]
			} else {
				// Repeat last value for constants (e.g., role="profile" applies to all)
				result[i][k] = parts[len(parts)-1]
			}
		}
	}
	return result
}

// mergeNestedField adds a nested map to the record under the given key.
// If the key already exists (multiple same-name entries), converts to a slice.
func mergeNestedField(rec output.Record, key string, subMap map[string]any) {
	existing, exists := rec[key]
	if !exists {
		rec[key] = subMap
		return
	}
	// Convert to slice: existing single map + new map, or append to existing slice.
	switch v := existing.(type) {
	case map[string]any:
		rec[key] = []any{v, subMap}
	case []any:
		rec[key] = append(v, subMap)
	default:
		rec[key] = subMap // overwrite unexpected type
	}
}

func DebugDateTime(args ...any) { slog.Debug(args[0].(string), args[1:]...) }

// func DebugDateTime(args ...any) { fmt.Println(args...) }

// extractField extracts a single field's value from an HTML selection and stores it in the record,
// handling different field types (text, url, date) and applying transformations.
// refTimeKey carries a fixed reference time for date extraction through the
// context so year-less dates resolve deterministically (e.g. to the page's
// fetch time) instead of wall-clock. paths injects it via WithRefTime;
// standalone/CLI callers do not and fall back to wall-clock plus a fixed legacy
// year, keeping goskyr's own golden tests deterministic.
type refTimeKey struct{}

// WithRefTime returns ctx carrying t as the date-extraction reference time.
func WithRefTime(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, refTimeKey{}, t)
}

func refTimeFromContext(ctx context.Context) (time.Time, bool) {
	t, ok := ctx.Value(refTimeKey{}).(time.Time)
	return t, ok && !t.IsZero()
}

// newReferenceDateTime builds the phil reference DateTime fed to Parse as
// MinDateTime: the injected reference time when present (deterministic),
// otherwise wall-clock (legacy standalone behavior).
func newReferenceDateTime(ctx context.Context) *datetime.DateTime {
	if t, ok := refTimeFromContext(ctx); ok {
		return datetime.NewDateTimeForTime(t)
	}
	return datetime.NewDateTimeForNow()
}

// referenceYear is the fallback year for year-less dates: the injected
// reference time's year, or wall-clock when none is set (standalone/CLI use).
// Tests that need a deterministic year-less result inject WithRefTime rather
// than relying on this wall-clock fallback.
func referenceYear(ctx context.Context) int {
	if t, ok := refTimeFromContext(ctx); ok {
		return t.Year()
	}
	return time.Now().Year()
}

// guessYearRef is the reference time guessYear compares record dates against:
// the injected reference time, or wall-clock when none is set.
func guessYearRef(ctx context.Context) time.Time {
	if t, ok := refTimeFromContext(ctx); ok {
		return t
	}
	return time.Now()
}

func extractField(ctx context.Context, f *Field, rec output.Record, sel *fetch.Selection, baseURL string, baseYear int) error {
	// // Tracing
	// _, span := otel.Tracer("github.com/findyourpaths/goskyr/scrape").Start(ctx, fmt.Sprintf("scrape.ExtractField()"))

	// // Metering
	// // source := "error"
	// // var count int
	// // rs := output.Records{}
	// defer func() {
	// 	// insts.observability.Add(ctx, observability.Instruments.Scrape, 1,
	// 	// 	// 	// attribute.String("source", source),
	// 	// 	attribute.String("arg.scraper.selector", s.Selector),
	// 	// 	attribute.Int("arg.scraper.found_nodes.len", len(gqdoc.Find(s.Selector).Nodes)),
	// 	// 	attribute.Int("int.count", count),
	// 	// 	// 	attribute.Int64("arg.gmail_id", ret.Email.GmailId),
	// 	// 	// 	attribute.String("ret.title", ret.Title),
	// 	// 	attribute.Int("ret.len", len(rs)),
	// 	// 	attribute.Int("ret.total_fields", recs.TotalFields()),
	// 	// // 	attribute.Int("ret.links.len", len(ret.Links)),
	// 	// // 	attribute.Int("ret.datetime_ranges.len", len(ret.DatetimeRanges)),
	// 	// )
	// 	span.End()
	// }()

	// Logging
	slog.Debug("scrape.extractField()", "field", f, "event", rec, "sel", sel, "baseURL", baseURL)
	switch f.Type {
	case "text", "": // the default, ie when type is not configured, is 'text'
		if err := extractStringField(getTextString, f, rec, sel, FieldPartSeparator); err != nil {
			return err
		}

	case "html":
		if err := extractStringField(getHTMLString, f, rec, sel, HTMLPartSeparator); err != nil {
			return err
		}

	case "markdown":
		if err := extractStringField(getMarkdownString, f, rec, sel, HTMLPartSeparator); err != nil {
			return err
		}

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
			if f.Required && u == "" {
				// if it's still empty and is required, return an error
				return fmt.Errorf("field %s is required but empty", f.Name)
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
		// fmt.Println("in case date_time_tz_ranges", "str", str)
		if err != nil {
			return err
		}

		// First check if the url encodes a parseable datetime with year, and use the year if so.
		// Iterate sorted keys, never map order: the first parseable URL field wins
		// baseYear, and the chosen year changes the rendered datetime range, which
		// downstream sourcegen commits and re-verifies byte-identically.
		recKeys := make([]string, 0, len(rec))
		for k := range rec {
			recKeys = append(recKeys, k)
		}
		sort.Strings(recKeys)
		for _, k := range recKeys {
			v := rec[k]
			str := v.(string)
			DebugDateTime("looking at non-date-time field", "baseYear", baseYear, "k", k, "v", v, "strings.HasSuffix(k, URLFieldSuffix)", strings.HasSuffix(k, URLFieldSuffix))
			if strings.HasSuffix(k, URLFieldSuffix) {
				if match := DateRE.FindString(str); match == "" {
					continue
				}
				dt := newReferenceDateTime(ctx)
				dt.TimeZone = datetime.NewTimeZone(f.DateLocation, "", "")
				rngs, err := datetime.Parse(str, datetime.ParseOptions{
					MinDateTime:     dt,
					DateMode:        philDateMode(f.DateLocation),
					DefaultLocation: philDefaultLocation(f.DateLocation),
					DefaultYear:     dt.Date.Year,
				})
				if err != nil {
					continue
				}
				if rngs != nil {
					for _, rng := range rngs.Items {
						if rng.Start.Date.Year != 0 {
							baseYear = rng.Start.Date.Year
							DebugDateTime("found", "baseYear", baseYear)
							break
						}
						if rng.End != nil && rng.End.Date.Year != 0 {
							baseYear = rng.End.Date.Year
							DebugDateTime("found", "baseYear", baseYear)
							break
						}
					}
				}
			}
			DebugDateTime("after looking", "baseYear", baseYear)
		}
		// Then use the current year if none is provided.
		if baseYear == 0 {
			baseYear = referenceYear(ctx)
			DebugDateTime("after setting reference year", "baseYear", baseYear)
		}
		// Limit input to datetime.Parse — long concatenated text (e.g., from
		// multi-page detail discovery) causes the parser to hang. The first
		// occurrence of a date is always near the start.
		parseStr := str
		if len(parseStr) > 500 {
			parseStr = parseStr[:500]
		}
		DebugDateTime("parsing datetime with", "baseYear", baseYear, "str", parseStr)
		dt := newReferenceDateTime(ctx)
		dt.Date.Year = baseYear
		dt.TimeZone = datetime.NewTimeZone(f.DateLocation, "", "")
		rngs, err := datetime.Parse(parseStr, datetime.ParseOptions{
			MinDateTime:     dt,
			DateMode:        philDateMode(f.DateLocation),
			DefaultLocation: philDefaultLocation(f.DateLocation),
			DefaultYear:     baseYear,
		})
		// fmt.Printf("rngs.Items[0]: %#v\n", rngs.Items[0])
		// fmt.Printf("rngs.Items[0].Start: %#v\n", rngs.Items[0].Start)
		if err != nil {
			DebugDateTime("parse error", "err", err)
			break
		}
		if rngs != nil && len(rngs.Items) > 0 && rngs.Items[0] != nil && datetime.HasStartMonthAndDay(rngs) {
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

func philDefaultLocation(name string) *time.Location {
	if name == "" {
		return nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	return loc
}

func philDateMode(locationName string) string {
	if locationName == "" {
		return ""
	}
	if datetime.DateMode(datetime.NewTimeZone(locationName, "", "")) == datetime.DateModeNorthAmerican {
		return datetime.DateModeNorthAmerican
	}
	return datetime.DateModeRest
}

// GetTextStringAndURL extracts text or attribute value from an element and resolves it as a URL
// relative to the base URL.
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
	// If multiple URLs were matched (concatenated with RecordSeparator), use only the first one for parsing
	relUForParsing := relU
	if idx := strings.Index(relU, RecordSeparator); idx != -1 {
		relUForParsing = relU[:idx]
	}
	uu, err := baseUU.Parse(relUForParsing)
	// fmt.Println("in GetTextStringAndURL()", "baseUU", baseUU, "relU", relU, "uu", uu)
	return relU, uu, err
}

var SkipTag = map[string]bool{
	"noscript": true,
	"script":   true,
	"style":    true,
}

// blockElements contains HTML block-level elements. When strip_tags is enabled,
// separators are only inserted between block-level siblings, not inline elements
// like <span>, <a>, <strong>, etc. This prevents Word-pasted HTML from fragmenting
// numbers across spans (e.g., <span>2</span><span>5</span> → "25" not "2 5").
var blockElements = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"br":      true,
	"details": true, "dialog": true, "dd": true, "div": true, "dl": true,
	"dt": true, "fieldset": true, "figcaption": true, "figure": true,
	"footer": true, "form": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "header": true, "hgroup": true,
	"hr": true, "li": true, "main": true, "nav": true, "ol": true,
	"p": true, "pre": true, "section": true, "table": true, "tbody": true,
	"td": true, "tfoot": true, "th": true, "thead": true, "tr": true,
	"ul": true,
}

// extractStringField extracts string parts from element locations using extractFn,
// joins them with partSep, applies defaults/required checks, and runs transforms.
func extractStringField(extractFn func(*ElementLocation, *fetch.Selection) (string, error), f *Field, rec output.Record, sel *fetch.Selection, partSep string) error {
	var parts []string
	for i := range f.ElementLocations {
		p := &f.ElementLocations[i]
		// Propagate field-level flags to each element location
		if f.StripTags {
			p.StripTags = true
		}
		if f.CollapseSpaces {
			p.CollapseSpaces = true
		}
		str, err := extractFn(p, sel)
		if err != nil {
			return err
		}
		if str != "" {
			parts = append(parts, str)
		}
	}
	t := strings.Join(parts, partSep)
	if t == "" {
		t = f.Default
		if f.Required && t == "" {
			return fmt.Errorf("field %s is required but empty", f.Name)
		}
	}
	for _, tr := range f.Transform {
		var err error
		t, err = transformString(&tr, t)
		if err != nil {
			return err
		}
	}
	// Always collapse spaces: normalize NBSP to space, then collapse runs of 2+ spaces.
	// No valid use case for preserving runs of whitespace in extracted text.
	t = strings.ReplaceAll(t, "\u00a0", " ")
	t = collapseSpacesRE.ReplaceAllString(t, " ")
	t = strings.TrimSpace(t)
	rec[f.Name] = t
	return nil
}

var collapseSpacesRE = regexp.MustCompile(`[ ]{2,}`)

// getTextString extracts text content or attribute values from elements matching the element
// location, applying regex extraction and length limits.
func getTextString(e *ElementLocation, sel *fetch.Selection) (string, error) {
	slog.Debug("getTextString()", "e", e, "s", sel)

	// Set sensible defaults for EntireSubtree and AllNodes if not explicitly configured
	entireSubtree := e.EntireSubtree
	allNodes := e.AllNodes
	// Default both to true unless ChildIndex is set (which requires EntireSubtree=false)
	if e.ChildIndex == 0 {
		if !e.EntireSubtree && !e.AllNodes {
			// Neither explicitly set - default both to true
			entireSubtree = true
			allNodes = true
		} else if e.EntireSubtree && !e.AllNodes {
			// EntireSubtree explicitly set to true but AllNodes not set - default AllNodes to true
			allNodes = true
		}
	}

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
			if entireSubtree {
				// Separator between text from different element children.
				// strip_tags mode uses newline (matches pageMarkdown block boundaries).
				// Normal mode uses ASCII Unit Separator (never appears in HTML content).
				subtreeSeparator := e.Separator
				if subtreeSeparator == "" {
					if e.StripTags {
						subtreeSeparator = "\n"
					} else {
						subtreeSeparator = UnitSeparator
					}
				}

				// copied from https://github.com/PuerkitoBio/goquery/blob/v1.8.0/property.go#L62
				stripTags := e.StripTags

				// Compile until_selector once if set.
				var untilMatcher cascadia.Sel
				if e.UntilSelector != "" {
					var err error
					untilMatcher, err = cascadia.Parse(e.UntilSelector)
					if err != nil {
						return "", fmt.Errorf("invalid until_selector %q: %w", e.UntilSelector, err)
					}
				}

				var buf bytes.Buffer
				stopped := false
				var f func(*html.Node)
				f = func(n *html.Node) {
					if stopped {
						return
					}
					// Skip the text in-between <style></style> tags.
					if n.Type == html.ElementNode && SkipTag[n.Data] {
						return
					}
					// Stop at until_selector match.
					if untilMatcher != nil && n.Type == html.ElementNode && untilMatcher.Match(n) {
						stopped = true
						return
					}
					if n.Type == html.TextNode {
						// Keep newlines and spaces, like jQuery
						buf.WriteString(n.Data)
					}
					if n.FirstChild != nil {
						for c := n.FirstChild; c != nil; c = c.NextSibling {
							if stopped {
								break
							}
							f(c)
							// Add separator between element siblings to preserve structure.
							// When strip_tags is enabled, only separate block-level elements
							// (not inline: span, a, strong, em). Use \n to match pageMarkdown.
							// In normal mode, use \x1f (unit separator for multi-value fields).
							if c.Type == html.ElementNode && c.NextSibling != nil {
								if !stripTags || blockElements[c.Data] {
									buf.WriteString(subtreeSeparator)
								}
							}
						}
					}
				}
				if allNodes {
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
				if allNodes {
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
			// Extract attribute values from nodes
			// When allNodes is true, extract attribute from ALL matching nodes
			// When allNodes is false, extract attribute from only the first matching node
			if allNodes {
				for _, node := range fieldSelection.Nodes {
					attrValue := ""
					for _, attr := range node.Attr {
						if attr.Key == e.Attr {
							attrValue = attr.Val
							break
						}
					}
					fieldStrings = append(fieldStrings, attrValue)
				}
			} else {
				fieldStrings = append(fieldStrings, fieldSelection.AttrOr(e.Attr, ""))
			}
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
	// Separator between values from multiple matched nodes.
	// Default to ASCII Record Separator (never appears in HTML content).
	nodeSeparator := e.NodeSeparator
	if nodeSeparator == "" {
		nodeSeparator = RecordSeparator
	}
	r := strings.Join(fieldStrings, nodeSeparator)
	slog.Debug("getTextString(), returning", "r", r)
	return r, nil
}

// getHTMLString extracts the inner HTML of elements matching the element location.
// Unlike getTextString which extracts only text content, this returns the raw HTML
// including tags. The caller (paths) is responsible for converting HTML to markdown.
func getHTMLString(e *ElementLocation, sel *fetch.Selection) (string, error) {
	slog.Debug("getHTMLString()", "e", e, "s", sel)

	var fieldSelection *fetch.Selection
	if e.Selector == "" {
		fieldSelection = sel
	} else {
		fieldSelection = sel.Find(e.Selector)
	}

	if len(fieldSelection.Nodes) == 0 {
		return "", nil
	}

	// Get inner HTML of ALL matched elements, concatenated with record separator.
	// goquery's .Html() only returns the first element; we need all of them
	// (e.g., multiple <p> tags matching "div.col-7 p").
	// Get inner HTML of ALL matched elements, joined with <br><br> so that
	// downstream HTML-to-markdown conversion produces paragraph breaks.
	// goquery's .Html() only returns the first element; we need all of them
	// (e.g., multiple <p> tags matching "div.col-7 p").
	var parts []string
	fieldSelection.Selection.Each(func(_ int, s *goquery.Selection) {
		h, err := s.Html()
		if err != nil {
			return
		}
		h = strings.TrimSpace(h)
		if h != "" {
			parts = append(parts, h)
		}
	})
	htmlStr := strings.Join(parts, HTMLNodeSeparator)

	// regex extract
	var err error
	htmlStr, err = extractStringRegex(&e.RegexExtract, htmlStr)
	if err != nil {
		return "", err
	}

	// shortening
	htmlStr = utils.ShortenString(htmlStr, e.MaxLength)

	return htmlStr, nil
}

// getMarkdownString extracts inner HTML and converts to markdown.
// Uses the same html-to-markdown library as pageMarkdown (paths/internal/entity/markdown)
// to ensure spec offsets and goskyr extraction produce identical text.
func getMarkdownString(e *ElementLocation, sel *fetch.Selection) (string, error) {
	htmlStr, err := getHTMLString(e, sel)
	if err != nil || htmlStr == "" {
		return htmlStr, err
	}
	return HTMLToMarkdown(htmlStr)
}

// HTMLToMarkdown converts an HTML string to cleaned markdown text.
// Post-processing matches paths/internal/entity/markdown.TextFromHTML exactly:
//   - Strips backslash line-break markers (\\\n → \n)
//   - Doubles single newlines to paragraph breaks
//   - Collapses 3+ newlines to 2
//   - Strips blockquotes and horizontal rules
//   - Normalizes whitespace
func HTMLToMarkdown(htmlStr string) (string, error) {
	r, err := htmltomarkdown.ConvertString(htmlStr)
	if err != nil {
		return "", fmt.Errorf("html-to-markdown conversion failed: %w", err)
	}
	r = strings.ToValidUTF8(r, " ")
	r = strings.ReplaceAll(r, "\u00A0", " ")
	r = strings.ReplaceAll(r, "\u2007", " ")
	r = strings.ReplaceAll(r, "\u202F", " ")
	r = spaceBeforeNewlineRE.ReplaceAllString(r, "\n")
	r = strings.ReplaceAll(r, "* * *\n", "\n")
	r = blockquoteRE.ReplaceAllString(r, "")
	r = strings.ReplaceAll(r, "\\\n", "\n")
	// Double all newlines. The html-to-markdown library uses:
	//   \n   = within a list (li items)
	//   \n\n = between block elements (div, p, h1, etc.)
	// After doubling:
	//   \n   → \n\n  (intra-block paragraph break)
	//   \n\n → \n\n\n\n (inter-block boundary)
	r = strings.ReplaceAll(r, "\n", "\n\n")
	// Cap at \n\n\n: preserves the block boundary signal (\n\n\n)
	// while collapsing any longer runs.
	r = excessiveNewlinesRE.ReplaceAllString(r, "\n\n\n")
	r = strings.TrimSpace(r)
	return r, nil
}

var spaceBeforeNewlineRE = regexp.MustCompile(`  \n`)
var blockquoteRE = regexp.MustCompile(`(?m)^> ?`)
var excessiveNewlinesRE = regexp.MustCompile(`\n{4,}`)

// extractStringRegex applies regex pattern matching to extract a substring from a string based
// on the regex configuration.
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

// transformString applies a transformation to a string based on the transform configuration,
// currently supporting regex-replace transformations.
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

// getBaseURL returns the base URL for a page, checking for <base> tags in the HTML or using
// the page URL as the default.
func getBaseURL(pageUrl string, gqdoc *fetch.Document) string {
	// relevant info: https://www.w3.org/TR/WD-html40-970917/htmlweb.html#relative-urls
	// currently this function does not fully implement the standard
	baseURL := gqdoc.Find("base").AttrOr("href", "")
	if baseURL == "" {
		baseURL = pageUrl
	}
	return baseURL
}

// extractJsonField extracts a value from JSON text using a JSONPath query string.
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

// DetailPages follows URL fields in records to scrape detail pages and merge the extracted
// data back into the original records.
func DetailPages(ctx context.Context, cache fetch.Cache, c *Config, s *Scraper, recs output.Records, domain string) error {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/scrape").Start(ctx, fmt.Sprintf("scrape.DetailPages(%d)", len(recs)))

	// Metering
	// source := "error"
	// var count int
	// rs := output.Records{}
	defer func() {
		// insts.observability.Add(ctx, observability.Instruments.Scrape, 1,
		// 	// 	// attribute.String("source", source),
		// 	attribute.String("arg.scraper.selector", s.Selector),
		// 	attribute.Int("arg.scraper.found_nodes.len", len(gqdoc.Find(s.Selector).Nodes)),
		// 	attribute.Int("int.count", count),
		// 	// 	attribute.Int64("arg.gmail_id", ret.Email.GmailId),
		// 	// 	attribute.String("ret.title", ret.Title),
		// 	attribute.Int("ret.len", len(rs)),
		// 	attribute.Int("ret.total_fields", recs.TotalFields()),
		// // 	attribute.Int("ret.links.len", len(ret.Links)),
		// // 	attribute.Int("ret.datetime_ranges.len", len(ret.DatetimeRanges)),
		// )
		span.End()
	}()

	// Logging
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
		if err := SubGQDocument(ctx, c, s, rec, c.ID.Field, subGQDoc); err != nil {
			return fmt.Errorf("error extending records: %v", err)
		}
	}
	return nil
}

// SubGQDocument scrapes a detail page document and merges the extracted fields into the
// parent record with field names prefixed by the source field name.
func SubGQDocument(ctx context.Context, c *Config, s *Scraper, rec output.Record, fname string, gqdoc *fetch.Document) error {
	// Tracing
	ctx, span := otel.Tracer("github.com/findyourpaths/goskyr/scrape").Start(ctx, fmt.Sprintf("scrape.SubGQDocument(%q)", s.Selector))

	// Metering
	// source := "error"
	// var count int
	// rs := output.Records{}
	defer func() {
		// insts.observability.Add(ctx, observability.Instruments.Scrape, 1,
		// 	// 	// attribute.String("source", source),
		// 	attribute.String("arg.scraper.selector", s.Selector),
		// 	attribute.Int("arg.scraper.found_nodes.len", len(gqdoc.Find(s.Selector).Nodes)),
		// 	attribute.Int("int.count", count),
		// 	// 	attribute.Int64("arg.gmail_id", ret.Email.GmailId),
		// 	// 	attribute.String("ret.title", ret.Title),
		// 	attribute.Int("ret.len", len(rs)),
		// 	attribute.Int("ret.total_fields", recs.TotalFields()),
		// // 	attribute.Int("ret.links.len", len(ret.Links)),
		// // 	attribute.Int("ret.datetime_ranges.len", len(ret.DatetimeRanges)),
		// )
		span.End()
	}()

	// Logging
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

	subRecs, err := GQDocument(ctx, c, s, gqdoc)
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

// printHTMLNodes returns a debug string representation of HTML nodes showing their data and attributes.
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

// printHTMLNodeAsStartTag returns a string representation of an HTML element node as an
// opening tag with all attributes.
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
