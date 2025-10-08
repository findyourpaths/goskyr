// Package cache provides a http.RoundTripper implementation that works as a
// mostly RFC-compliant cache for http responses.
//
// It is only suitable for use as a 'private' cache (i.e. for a web-browser or an API-client
// and not for a shared proxy).
//
// Mostly borrowed from https://github.com/gregjones/httpcache. Customized for different policies.
package fetch

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

var DoDebug = false

// var DoDebug = true

var ShowCaching = false

// var ShowCaching = true

var Synchronized = true

// var Synchronized = false

// A Cache interface is used by the Transport to store and retrieve responses.
type Cache interface {
	// Get returns the []byte representation of a cached response and a bool
	// set to true if the value isn't empty
	Get(key string) (responseBytes []byte, ok bool)
	// Set stores the []byte representation of a response against a key
	Set(key string, responseBytes []byte)
	// Delete removes the value associated with the key
	Delete(key string)
	// GetResolvedURL returns the final URL after following redirects
	GetResolvedURL(rawURL string) (string, error)
}

// var fetcher = NewDynamicFetcher("", 1) //s.PageLoadWait)

var ErrorIfPageNotInCache = false

type Document struct {
	*goquery.Document
	findCache map[string]*Selection
}

func NewDocument(gqdoc *goquery.Document) *Document {
	return &Document{
		Document:  gqdoc,
		findCache: map[string]*Selection{},
	}
}

func NewDocumentFromResponse(str string) (*Document, error) {
	strs := strings.SplitN(str, "\n", 2)
	gqdoc, err := NewDocumentFromString(strs[1])
	if err != nil {
		return nil, err
	}

	gqdoc.Url, err = url.Parse(strs[0])
	if err != nil {
		return nil, err
	}
	return gqdoc, nil
}

// NormalizeHTMLString parses and re-serializes HTML to ensure consistent structure.
// This ensures that HTML auto-corrections (like wrapping <tr> in <tbody>) are
// applied consistently during both pattern generation and scraping phases.
func NormalizeHTMLString(htmlStr string) (string, error) {
	// Parse HTML
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return "", fmt.Errorf("parsing HTML: %w", err)
	}

	// Serialize back to HTML
	var buf bytes.Buffer
	err = html.Render(&buf, doc)
	if err != nil {
		return "", fmt.Errorf("rendering HTML: %w", err)
	}

	return buf.String(), nil
}

func NewDocumentFromString(str string) (*Document, error) {
	// Normalize HTML to ensure consistent structure across generation and scraping
	normalizedHTML, err := NormalizeHTMLString(str)
	if err != nil {
		return nil, fmt.Errorf("normalizing HTML: %w", err)
	}

	gqdoc, err := goquery.NewDocumentFromReader(strings.NewReader(normalizedHTML))
	if err != nil {
		return nil, err
	}
	return NewDocument(gqdoc), nil
}

func (gqdoc *Document) Find(selector string) *Selection {
	if r, found := gqdoc.findCache[selector]; found {
		return r
	}
	r := NewSelection(gqdoc.Document.Find(selector))
	gqdoc.findCache[selector] = r
	return r
}

type Selection struct {
	*goquery.Selection
	findCache map[string]*Selection
	textCache map[any]string
}

func NewSelection(sel *goquery.Selection) *Selection {
	return &Selection{
		Selection: sel,
		findCache: map[string]*Selection{},
		textCache: map[any]string{},
	}
}

func (sel *Selection) Find(selector string) *Selection {
	if r, found := sel.findCache[selector]; found {
		return r
	}
	r := NewSelection(sel.Selection.Find(selector))
	sel.findCache[selector] = r
	return r
}

func GetGQDocument(cache Cache, u string) (*Document, bool, error) {
	if ShowCaching {
		fmt.Println("fetch.GetGQDocument()", "u", u)
	}
	respBytes, ok := cache.Get(u) //fetchGQDocument(opts, fetch.TrimURLScheme(opts.URL), map[string]*goquery.Document{})
	if !ok {
		return nil, false, nil
	}
	r, err := ResponseBytesToGQDocument(respBytes)
	if err != nil {
		return nil, true, err
	}
	uu, err := url.Parse(u)
	if err != nil {
		return nil, true, err
	}
	r.Url = uu
	if ShowCaching {
		fmt.Println("in fetch.GetGQDocument(), returning", "u", u)
	}
	return r, true, nil
}

// func GetGQDocuments(cache Cache, us []string) ([]*goquery.Document, []error) {
// 	rs := []*goquery.Document{}
// 	errs := []error{}
// 	foundErr := false
// 	for _, u := range us {
// 		r, _, err := GetGQDocument(cache, u)
// 		rs = append(rs, r)
// 		errs = append(errs, err)
// 		if err != nil {
// 			foundErr = true
// 		}
// 	}
// 	if !foundErr {
// 		errs = nil
// 	}
// 	return rs, errs
// }

// Helper struct to hold page results
type pageResult struct {
	index int
	gqdoc *Document
	err   error
}

func GetGQDocuments(cache Cache, us []string) ([]*Document, []error) {
	// func Pages(cache fetch.Cache, us []string, reqFn func(req *client.Request)) ([]*goquery.Document, []error) {
	rs := make([]*Document, len(us))
	errs := make([]error, len(us))

	// Use a channel to receive results and errors.
	results := make(chan *pageResult, len(us))

	if ShowCaching {
		fmt.Println("in cache.GetGQDocuments() retrieving", "len(us)", len(us))
	}
	uCount := 0
	for i, u := range us {
		if u == "" {
			continue
		}
		if ShowCaching {
			fmt.Println("in cache.GetGQDocuments(), retrieving", "i", i, "u", u)
		}

		// go func() {
		fn := func() {
			gqdoc, _, err := GetGQDocument(cache, u)
			if err != nil {
				if ShowCaching {
					fmt.Println("in cache.GetGQDocuments(), got error", "i", i, "u", u, "err", err)
				}
				results <- &pageResult{index: i, err: err}
				return
			}
			if ShowCaching {
				fmt.Println("in cache.GetGQDocuments(), got gqdoc", "i", i, "u", u, "gqdoc == nil", gqdoc == nil)
			}
			results <- &pageResult{index: i, gqdoc: gqdoc}
		}

		if Synchronized {
			fn()
		} else {
			go fn()
		}
		uCount++
	}

	// Collect results and errors from the channel.
	foundErr := false
	if ShowCaching {
		fmt.Println("in cache.GetGQDocuments(), waiting for", "len(us)", len(us))
	}
	for i := 0; i < uCount; i++ {
		if ShowCaching {
			fmt.Println("in cache.GetGQDocuments(), waiting to receive", "i", i)
		}
		result := <-results
		if ShowCaching {
			fmt.Println("in cache.GetGQDocuments(), received", "i", i, "result.index", result.index)
		}
		if result.err != nil {
			errs[result.index] = result.err
			foundErr = true
		} else {
			rs[result.index] = result.gqdoc
		}
	}
	if !foundErr {
		errs = nil
	}

	return rs, errs
}

func SetGQDocument(cache Cache, u string, gqdoc *Document) {
	str, err := goquery.OuterHtml(gqdoc.Children())
	if err != nil {
		fmt.Printf("got error setting cache: %v\n", err)
		return
	}
	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(str)),
	}
	respBytes, err := httputil.DumpResponse(resp, true)
	if err != nil {
		fmt.Printf("got error setting cache: %v\n", err)
	}
	cache.Set(u, respBytes)
}

func ResponseBytesToGQDocument(respBytes []byte) (*Document, error) {
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewBuffer(respBytes)), nil)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	// Limit response body reading
	// bodyReader := io.LimitReader(hresp.Body, c.opt.MaxBodySize)
	bodyReader := resp.Body

	// // Decode response
	// if hresp.Request != nil && hresp.Request.Method != "HEAD" && hresp.ContentLength > 0 {
	// 	if creq.Encoding != "" {
	// 		if enc, _ := charset.Lookup(creq.Encoding); enc != nil {
	// 			bodyReader = transform.NewReader(bodyReader, enc.NewDecoder())
	// 		}
	// 	} else {
	// 		if !c.opt.CharsetDetectDisabled {
	// 			contentType := creq.Header.Get("Content-Type")
	// 			var err error
	// 			bodyReader, err = charset.NewReader(bodyReader, contentType)
	// 			if err != nil {
	// 				return nil, fmt.Errorf("charset detection error on content-type %s: %w", contentType, err)
	// 			}
	// 		}
	// 	}
	// }

	body, err := io.ReadAll(bodyReader)
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}

	return NewDocumentFromString(string(body))
}

// func fetchGQDocument(opts ConfigOptions, u string) (*goquery.Document, error) {
// 	// if output.WriteSeparateLogFiles && opts.ConfigOutputDir != "" {
// 	// 	prevLogger, err := output.SetDefaultLogger(filepath.Join(opts.ConfigOutputDir, opts.configID.String()+"_fetchGQDocument_log.txt"), slog.LevelDebug)
// 	// 	if err != nil {
// 	// 		return nil, nil, err
// 	// 	}
// 	// 	defer output.RestoreDefaultLogger(prevLogger)
// 	// }
// 	slog.Info("fetchGQDocument()", "u", u)
// 	slog.Info("fetchGQDocument()", "len(gqdocsByURL)", len(gqdocsByURL))
// 	defer slog.Info("fetchGQDocument() returning")

// 	if gqdocsByURL == nil {
// 		gqdocsByURL = map[string]*goquery.Document{}
// 	}

// 	// Check if we have it in memory.
// 	gqdoc, found := gqdocsByURL[u]
// 	str := ""
// 	var err error

// 	if found {
// 		slog.Debug("fetchGQDocument(), memory cache hit")
// 	} else {
// 		// Not in memory, so check if it's in our cache on disk.
// 		cacheInPath := filepath.Join(opts.CacheInputDir, fetch.MakeURLStringSlug(u)+".html")
// 		slog.Debug("fetchGQDocument(), looking on disk at", "cacheInPath", cacheInPath)
// 		str, err = utils.ReadStringFile(cacheInPath)
// 		if err == nil {
// 			slog.Debug("fetchGQDocument(), disk cache hit", "len(str)", len(str))
// 		} else {
// 			if opts.Offline {
// 				return nil, nil, fmt.Errorf("running offline and unable to retrieve %q", u)
// 			}
// 			var fetcher fetch.Fetcher
// 			if opts.RenderJS {
// 				fetcher = fetch.NewDynamicFetcher("", 0)
// 			} else {
// 				fetcher = &fetch.StaticFetcher{}
// 			}

// 			str, err = fetcher.Fetch("http://"+u, nil)
// 			if err != nil {
// 				return nil, nil, fmt.Errorf("error fetching GQDocument: %v", err)
// 			}
// 			slog.Debug("fetchGQDocument(), retrieved html", "len(str)", len(str))
// 		}

// 		// If on disk, use the cached html string. Otherwise, use the retrieved html.
// 		//
// 		// Original goskyr comment:
// 		// A bit hacky. But goquery seems to manipulate the html (I only know of goquery adding tbody tags if missing)
// 		// so we rely on goquery to read the html for both scraping AND figuring out the scraping config.
// 		gqdoc, err = goquery.NewDocumentFromReader(strings.NewReader(str))
// 		if err != nil {
// 			return nil, nil, err
// 		}

// 		gqdocsByURL[u] = gqdoc
// 	}

// 	slog.Debug("fetchGQDocument()", "len(str)", len(str))

// 	// Now write to the new cache if there is one and the page isn't already there.
// 	if opts.CacheOutputDir != "" {
// 		cacheOutPath := filepath.Join(opts.CacheOutputDir, fetch.MakeURLStringSlug(u)+".html")
// 		_, err = os.Stat(cacheOutPath)
// 		if err == nil {
// 			slog.Debug("fetchGQDocument(), already written to disk cache", "cacheOutPath", cacheOutPath)
// 		} else {
// 			if str == "" {
// 				// Now we have to translate the goquery doc back into a string
// 				str, err = goquery.OuterHtml(gqdoc.Children())
// 				if err != nil {
// 					return nil, nil, err
// 				}
// 			}

// 			slog.Debug("in fetchGQDocument(), writing to disk cache", "cacheOutPath", cacheOutPath)
// 			if err := utils.WriteStringFile(cacheOutPath, str); err != nil {
// 				return nil, nil, fmt.Errorf("failed to write html file: %v", err)
// 			}
// 		}
// 	}

// 	return gqdoc, gqdocsByURL, nil
// }

// var parseHTML = &middleware.ParseHTML{ParseHTMLDisabled: false}

// func (c *FetchCache) GetGQDocument(key string) (*goquery.Document, error) {
// 	u, err := url.Parse(key)
// 	if err != nil {
// 		return nil, fmt.Errorf("error parsing URL in cache key %q: %v", key, err)
// 	}
// 	req := &http.Request{Method: http.MethodGet, URL: u}
// 	cachedVal, ok := c.Get(req.URL.String())
// 	if !ok {
// 		return nil, fmt.Errorf("error getting cache response for key %q: %v", key, err)
// 	}

// 	b := bytes.NewBuffer(cachedVal)
// 	hresp, err := http.ReadResponse(bufio.NewReader(b), nil)
// 	if err != nil {
// 		return nil, fmt.Errorf("error reading cache response for key %q: %v", key, err)
// 	}
// 	if hresp == nil {
// 		p := ResponseFilename("", key)
// 		fmt.Printf("ResponseFilename: %q\n", p)
// 		return nil, fmt.Errorf("error reading cache response for key %q: *http.Response is nil", key)
// 	}
// 	body, err := doCachedRequestClient(hresp)
// 	if err != nil {
// 		return nil, fmt.Errorf("error parsing cache response body for key %q: %v", key, err)
// 	}

// 	return goquery.NewDocumentFromReader(bytes.NewReader(body))
// }

// // doCachedRequestClient is a simple wrapper to read response according to options.
// // Keep this in sync with geziyor.client.doCachedRequestClient
// func doCachedRequestClient(resp *http.Response) ([]byte, error) {
// 	// Do request
// 	defer func() {
// 		if resp != nil && resp.Body != nil {
// 			resp.Body.Close()
// 		}
// 	}()

// 	var body []byte
// 	if resp.Body != nil {
// 		// Limit response body reading
// 		// bodyReader := io.LimitReader(resp.Body, c.opt.MaxBodySize)
// 		bodyReader := io.LimitReader(resp.Body, DefaultMaxBody)

// 		// // Decode response
// 		// if resp.Request.Method != "HEAD" && resp.ContentLength > 0 {
// 		// 	// if req.Encoding != "" {
// 		// 	// 	if enc, _ := charset.Lookup(req.Encoding); enc != nil {
// 		// 	// 		bodyReader = transform.NewReader(bodyReader, enc.NewDecoder())
// 		// 	// 	}
// 		// 	// } else {
// 		// 	// 	if !c.opt.CharsetDetectDisabled {
// 		// 	contentType := req.Header.Get("Content-Type")
// 		// 	bodyReader, err = charset.NewReader(bodyReader, contentType)
// 		// 	if err != nil {
// 		// 		return nil, fmt.Errorf("charset detection error on content-type %s: %w", contentType, err)
// 		// 	}
// 		// 	// 	}
// 		// 	// }
// 		// }

// 		var err error
// 		body, err = io.ReadAll(bodyReader)
// 		if err != nil {
// 			return nil, fmt.Errorf("reading body: %w", err)
// 		}
// 	}
// 	return body, nil
// }
