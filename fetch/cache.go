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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"
	"github.com/findyourpaths/goskyr/utils"
)

// var DoDebug = true

var DoDebug = false

// A Cache interface is used by the Transport to store and retrieve responses.
type Cache interface {
	// Get returns the []byte representation of a cached response and a bool
	// set to true if the value isn't empty
	Get(key string) (responseBytes []byte, ok bool)
	// Set stores the []byte representation of a response against a key
	Set(key string, responseBytes []byte)
	// Delete removes the value associated with the key
	Delete(key string)
}

var fetcher = NewDynamicFetcher("", 1) //s.PageLoadWait)

var ErrorIfPageNotInCache = false

func GetGQDocument(cache Cache, u string) (*goquery.Document, bool, error) {
	respBytes, ok := cache.Get(u) //fetchGQDocument(opts, fetch.TrimURLScheme(opts.URL), map[string]*goquery.Document{})
	if ok {
		r, err := ResponseBytesToGQDocument(respBytes)
		return r, true, err
	}

	if ErrorIfPageNotInCache {
		return nil, false, fmt.Errorf("didn't find page in cache: %q", u)
	}
	// return nil, false, nil
	gqdoc, err := GQDocument(fetcher, u, nil)
	if gqdoc != nil {
		SetGQDocument(cache, u, gqdoc)
	}
	return gqdoc, false, err
}

func SetGQDocument(cache Cache, u string, gqdoc *goquery.Document) {
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

func ResponseBytesToGQDocument(respBytes []byte) (*goquery.Document, error) {
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

	return goquery.NewDocumentFromReader(bytes.NewReader(body))
}

// Cache is an implementation of Geziyor cache.Cache that stores html pages on disk.
type FetchCache struct {
	inputDir            string
	outputDir           string
	responsesByKey      map[string][]byte
	responsesByKeyMutex *sync.Mutex
}

// New returns a new Cache that will store files in dir.
func New(inputDir string, outputDir string) *FetchCache {
	return &FetchCache{
		inputDir:            inputDir,
		outputDir:           outputDir,
		responsesByKey:      map[string][]byte{},
		responsesByKeyMutex: &sync.Mutex{},
	}
}

var cacheResponseSuffix = ".html"

var ShowHits = false

var PanicOnCacheMiss = false

var DefaultMaxBody int64 = 1024 * 1024 * 1024 // 1GB

// Get returns the response corresponding to key, and true, if
// present in InputDir or OutputDir. Otherwise it returns nil and false.
func (c *FetchCache) Get(key string) ([]byte, bool) {
	// if strings.Index(key, "facebook") != -1 {
	// 	panic("trying to get a facebook page")
	// }

	c.responsesByKeyMutex.Lock()
	resp, ok := c.responsesByKey[key]
	c.responsesByKeyMutex.Unlock()
	if ok {
		return resp, ok
	}

	if DoDebug {
		fmt.Println("fetch.FetchCache.Get()", "key", key)
	}
	p := ResponseFilename(c.inputDir, key)
	if DoDebug {
		fmt.Println("in fetch.FetchCache.Get()", "p", p)
	}
	resp, err := utils.ReadBytesFile(p)
	if err != nil {
		p := ResponseFilename(c.outputDir, key)
		resp, err = utils.ReadBytesFile(p)
	}

	// if ShowHits {
	// 	slog.Info("in filecache.Cache.Get(), looking for", "c.dir", c.dir, "p", p, "err", err)
	// }
	if err != nil {
		if ShowHits {
			fmt.Println("in fetch.Cache.Get(), cache miss", "key", key)
		}
		if PanicOnCacheMiss {
			panic("cache miss for key: " + key)
		}
		return nil, false
	}

	// if ShowHits {
	// 	slog.Info("in filecache.Cache.Get(), cache hit", "key", key)
	// }
	c.responsesByKeyMutex.Lock()
	c.responsesByKey[key] = resp
	c.responsesByKeyMutex.Unlock()

	if c.outputDir != c.inputDir {
		c.Set(key, resp)
	}
	return resp, true
}

// Set saves a response to the cache as key
func (c *FetchCache) Set(key string, resp []byte) {
	if DoDebug {
		fmt.Println("fetch.FetchCache.Set()", "key", key, "len(resp)", len(resp))
	}
	p := ResponseFilename(c.outputDir, key)
	// if ShowHits {
	// 	slog.Info("in filecache.Cache.Set(), writing response to", "p", p)
	// }
	if err := utils.WriteBytesFile(p, resp); err != nil {
		slog.Warn("failed to write to cache at", "path", p, "error", err.Error())
	}
}

// Delete removes the response with key from the cache
func (c *FetchCache) Delete(key string) {
	if DoDebug {
		fmt.Println("fetch.FetchCache.Delete()", "key", key)
	}
	p := ResponseFilename(c.outputDir, key)
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		// p = keyToFilename(InputDir, key)
		// if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		slog.Warn("failed to find cache entry at", "path", p, "error", err.Error())
		// }
	}
	if err := os.Remove(p); err != nil {
		slog.Warn("failed to remove cache entry at", "path", p, "error", err.Error())
	}
}

func ResponseFilename(dir string, urlStr string) string {
	return Filename(dir, urlStr) + cacheResponseSuffix
}

func Filename(dir string, urlStr string) string {
	if dir == "" {
		panic("need to set Filename dir")
		// dir = InputDir
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		panic(err)
	}
	// if ShowHits {
	// 	slog.Info("in filecache.Filename()", "u.Host", u.Host, "urlStr", urlStr)
	// }
	uHostSlug := utils.MakeURLStringSlug(u.Host)
	uSlug := utils.MakeURLStringSlug(urlStr)
	if DoDebug {
		fmt.Println("in fetch.Filename()", "dir", dir, "uHostSlug", uHostSlug, "uSlug", uSlug)
	}
	return filepath.Join(dir, uHostSlug, uSlug)
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
