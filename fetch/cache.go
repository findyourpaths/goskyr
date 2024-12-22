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

	"github.com/PuerkitoBio/goquery"
	"github.com/findyourpaths/goskyr/utils"
)

// var DebugCache = true
var DebugCache = false

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

var fetcher = NewDynamicFetcher("", 0) //s.PageLoadWait)

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
	SetGQDocument(cache, u, gqdoc)
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
	inputDir  string
	outputDir string
}

// New returns a new Cache that will store files in dir.
func New(inputDir string, outputDir string) *FetchCache {
	return &FetchCache{
		inputDir:  inputDir,
		outputDir: outputDir,
	}
}

var cacheResponseSuffix = ".html"

var ShowHits = false

var PanicOnCacheMiss = false

var DefaultMaxBody int64 = 1024 * 1024 * 1024 // 1GB

// Get returns the response corresponding to key, and true, if
// present in InputDir or OutputDir. Otherwise it returns nil and false.
func (c *FetchCache) Get(key string) ([]byte, bool) {
	if DebugCache {
		fmt.Println("fetch.FetchCache.Get()", "key", key)
	}
	p := ResponseFilename(c.inputDir, key)
	if DebugCache {
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
	if c.outputDir != c.inputDir {
		c.Set(key, resp)
	}
	return resp, true
}

// Set saves a response to the cache as key
func (c *FetchCache) Set(key string, resp []byte) {
	if DebugCache {
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
	if DebugCache {
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
	if DebugCache {
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

func (c *FetchCache) GetGQDocument(key string) (*goquery.Document, error) {
	u, err := url.Parse(key)
	if err != nil {
		return nil, fmt.Errorf("error parsing URL in cache key %q: %v", key, err)
	}
	req := &http.Request{Method: http.MethodGet, URL: u}
	cachedVal, ok := c.Get(req.URL.String())
	if !ok {
		return nil, fmt.Errorf("error getting cache response for key %q: %v", key, err)
	}

	b := bytes.NewBuffer(cachedVal)
	hresp, err := http.ReadResponse(bufio.NewReader(b), nil)
	if err != nil {
		return nil, fmt.Errorf("error reading cache response for key %q: %v", key, err)
	}
	if hresp == nil {
		p := ResponseFilename("", key)
		fmt.Printf("ResponseFilename: %q\n", p)
		return nil, fmt.Errorf("error reading cache response for key %q: *http.Response is nil", key)
	}
	body, err := doCachedRequestClient(hresp)
	if err != nil {
		return nil, fmt.Errorf("error parsing cache response body for key %q: %v", key, err)
	}

	return goquery.NewDocumentFromReader(bytes.NewReader(body))
}

// doCachedRequestClient is a simple wrapper to read response according to options.
// Keep this in sync with geziyor.client.doCachedRequestClient
func doCachedRequestClient(resp *http.Response) ([]byte, error) {
	// Do request
	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()

	var body []byte
	if resp.Body != nil {
		// Limit response body reading
		// bodyReader := io.LimitReader(resp.Body, c.opt.MaxBodySize)
		bodyReader := io.LimitReader(resp.Body, DefaultMaxBody)

		// // Decode response
		// if resp.Request.Method != "HEAD" && resp.ContentLength > 0 {
		// 	// if req.Encoding != "" {
		// 	// 	if enc, _ := charset.Lookup(req.Encoding); enc != nil {
		// 	// 		bodyReader = transform.NewReader(bodyReader, enc.NewDecoder())
		// 	// 	}
		// 	// } else {
		// 	// 	if !c.opt.CharsetDetectDisabled {
		// 	contentType := req.Header.Get("Content-Type")
		// 	bodyReader, err = charset.NewReader(bodyReader, contentType)
		// 	if err != nil {
		// 		return nil, fmt.Errorf("charset detection error on content-type %s: %w", contentType, err)
		// 	}
		// 	// 	}
		// 	// }
		// }

		var err error
		body, err = io.ReadAll(bodyReader)
		if err != nil {
			return nil, fmt.Errorf("reading body: %w", err)
		}
	}
	return body, nil
}

// // cacheKey returns the cache key for req.
// func cacheKey(req *http.Request) string {
// 	if req.Method == http.MethodGet {
// 		return req.URL.String()
// 	} else {
// 		return req.Method + " " + req.URL.String()
// 	}
// }

// type Policy int

// const (
// 	// This policy has no awareness of any HTTP Cache-Control directives.
// 	// Every request and its corresponding response are cached.
// 	// When the same request is seen again, the response is returned without transferring anything from the Internet.

// 	// The Dummy policy is useful for testing spiders faster (without having to wait for downloads every time)
// 	// and for trying your spider offline, when an Internet connection is not available.
// 	// The goal is to be able to “replay” a spider run exactly as it ran before.
// 	Dummy Policy = iota

// 	// This policy provides a RFC2616 compliant HTTP cache, i.e. with HTTP Cache-Control awareness,
// 	// aimed at production and used in continuous runs to avoid downloading unmodified data
// 	// (to save bandwidth and speed up crawls).
// 	RFC2616
// )

// const (
// 	stale = iota
// 	fresh
// 	transparent
// 	// XFromCache is the header added to responses that are returned from the cache
// 	XFromCache = "X-From-Cache"
// )

// // CachedResponse returns the cached http.Response for req if present, and nil
// // otherwise.
// func CachedResponse(c Cache, req *http.Request) (resp *http.Response, err error) {
// 	// log.Printf("cache.CachedResponse(c, req.URL: %q)", req.URL)
// 	// defer log.Printf("cache.CachedResponse(c, req.URL: %q) returning", req.URL)

// 	cachedVal, ok := c.Get(cacheKey(req))
// 	if !ok {
// 		return
// 	}

// 	b := bytes.NewBuffer(cachedVal)
// 	return http.ReadResponse(bufio.NewReader(b), nil)
// }

// // Transport is an implementation of http.RoundTripper that will return values from a cache
// // where possible (avoiding a network request) and will additionally add validators (etag/if-modified-since)
// // to repeated requests allowing servers to return 304 / Not Modified
// type Transport struct {
// 	Policy Policy
// 	// The RoundTripper interface actually used to make requests
// 	// If nil, http.DefaultTransport is used
// 	Transport http.RoundTripper
// 	Cache     Cache
// 	// If true, responses returned from the cache will be given an extra header, X-From-Cache
// 	MarkCachedResponses bool
// }

// // NewTransport returns a new Transport with the
// // provided Cache implementation and MarkCachedResponses set to true
// func NewTransport(c Cache) *Transport {
// 	return &Transport{
// 		Policy:              RFC2616,
// 		Cache:               c,
// 		MarkCachedResponses: true,
// 	}
// }

// // Client returns an *http.Client that caches responses.
// func (t *Transport) Client() *http.Client {
// 	return &http.Client{Transport: t}
// }

// // varyMatches will return false unless all of the cached values for the headers listed in Vary
// // match the new request
// func varyMatches(cachedResp *http.Response, req *http.Request) bool {
// 	for _, header := range headerAllCommaSepValues(cachedResp.Header, "vary") {
// 		header = http.CanonicalHeaderKey(header)
// 		if header != "" && req.Header.Get(header) != cachedResp.Header.Get("X-Varied-"+header) {
// 			return false
// 		}
// 	}
// 	return true
// }

// // RoundTrip is a wrapper for caching requests.
// // If there is a fresh Response already in cache, then it will be returned without connecting to
// // the server.
// func (t *Transport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
// 	if t.Policy == Dummy {
// 		return t.RoundTripDummy(req)
// 	}
// 	return t.RoundTripRFC2616(req)
// }

// // RoundTripDummy has no awareness of any HTTP Cache-Control directives.
// // Every request and its corresponding response are cached.
// // When the same request is seen again, the response is returned without transferring anything from the Internet.
// func (t *Transport) RoundTripDummy(req *http.Request) (resp *http.Response, err error) {
// 	// log.Printf("cache.Transport.RoundTripDummy(req: %#v)\n", req)
// 	cacheKey := cacheKey(req)
// 	cacheable := (req.Method == "GET" || req.Method == "HEAD") && req.Header.Get("range") == ""
// 	// log.Printf("in cache.Transport.RoundTripDummy(req.URL: %q), cacheable: %t\n", req.URL, cacheable)
// 	var cachedResp *http.Response
// 	if cacheable {
// 		cachedResp, err = CachedResponse(t.Cache, req)
// 	} else {
// 		// Need to invalidate an existing value
// 		t.Cache.Delete(cacheKey)
// 	}

// 	transport := t.Transport
// 	if transport == nil {
// 		transport = http.DefaultTransport
// 	}

// 	if cacheable && cachedResp != nil && err == nil {
// 		if t.MarkCachedResponses {
// 			cachedResp.Header.Set(XFromCache, "1")
// 		}
// 		return cachedResp, nil
// 	} else {
// 		resp, err = transport.RoundTrip(req)
// 		if err != nil {
// 			return nil, err
// 		}
// 	}

// 	if cacheable {
// 		respBytes, err := httputil.DumpResponse(resp, true)
// 		// log.Printf("in cache.Transport.RoundTripDummy(req.URL: %q), setting cache, err: %v", req.URL, err)
// 		if err == nil {
// 			t.Cache.Set(cacheKey, respBytes)
// 		}
// 	} else {
// 		t.Cache.Delete(cacheKey)
// 	}
// 	return resp, nil
// }

// // RoundTripRFC2616 provides a RFC2616 compliant HTTP cache, i.e. with HTTP Cache-Control awareness,
// // aimed at production and used in continuous runs to avoid downloading unmodified data
// // (to save bandwidth and speed up crawls).
// //
// // If there is a stale Response, then any validators it contains will be set on the new request
// // to give the server a chance to respond with NotModified. If this happens, then the cached Response
// // will be returned.
// func (t *Transport) RoundTripRFC2616(req *http.Request) (resp *http.Response, err error) {
// 	log.Printf("cache.Transport.RoundTripRFC2616(req: %#v)\n", req)
// 	cacheKey := cacheKey(req)
// 	cacheable := (req.Method == "GET" || req.Method == "HEAD") && req.Header.Get("range") == ""
// 	log.Printf("in cache.Transport.RoundTripRFC2616(req), cacheable: %t\n", cacheable)
// 	var cachedResp *http.Response
// 	if cacheable {
// 		cachedResp, err = CachedResponse(t.Cache, req)
// 	} else {
// 		// Need to invalidate an existing value
// 		t.Cache.Delete(cacheKey)
// 	}

// 	transport := t.Transport
// 	if transport == nil {
// 		transport = http.DefaultTransport
// 	}

// 	if cacheable && cachedResp != nil && err == nil {
// 		if t.MarkCachedResponses {
// 			cachedResp.Header.Set(XFromCache, "1")
// 		}

// 		if varyMatches(cachedResp, req) {
// 			// Can only use cached value if the new request doesn't Vary significantly
// 			freshness := getFreshness(cachedResp.Header, req.Header)
// 			if freshness == fresh {
// 				return cachedResp, nil
// 			}

// 			if freshness == stale {
// 				var req2 *http.Request
// 				// Add validators if caller hasn't already done so
// 				etag := cachedResp.Header.Get("etag")
// 				if etag != "" && req.Header.Get("etag") == "" {
// 					req2 = cloneRequest(req)
// 					req2.Header.Set("if-none-match", etag)
// 				}
// 				lastModified := cachedResp.Header.Get("last-modified")
// 				if lastModified != "" && req.Header.Get("last-modified") == "" {
// 					if req2 == nil {
// 						req2 = cloneRequest(req)
// 					}
// 					req2.Header.Set("if-modified-since", lastModified)
// 				}
// 				if req2 != nil {
// 					req = req2
// 				}
// 			}
// 		}

// 		resp, err = transport.RoundTrip(req)
// 		if err == nil && req.Method == "GET" && resp.StatusCode == http.StatusNotModified {
// 			// Replace the 304 response with the one from cache, but update with some new headers
// 			endToEndHeaders := getEndToEndHeaders(resp.Header)
// 			for _, header := range endToEndHeaders {
// 				cachedResp.Header[header] = resp.Header[header]
// 			}
// 			resp.Body.Close()
// 			resp = cachedResp
// 		} else if (err != nil || resp.StatusCode >= 500) &&
// 			req.Method == "GET" && canStaleOnError(cachedResp.Header, req.Header) {
// 			// In case of transport failure and stale-if-error activated, returns cached content
// 			// when available
// 			if resp != nil && resp.Body != nil {
// 				resp.Body.Close()
// 			}
// 			return cachedResp, nil
// 		} else {
// 			if err != nil || resp.StatusCode != http.StatusOK {
// 				t.Cache.Delete(cacheKey)
// 			}
// 			if err != nil {
// 				return nil, err
// 			}
// 		}
// 	} else {
// 		reqCacheControl := parseCacheControl(req.Header)
// 		if _, ok := reqCacheControl["only-if-cached"]; ok {
// 			resp = newGatewayTimeoutResponse(req)
// 		} else {
// 			resp, err = transport.RoundTrip(req)
// 			if err != nil {
// 				return nil, err
// 			}
// 		}
// 	}

// 	// log.Printf("in cache.Transport.RoundTripRFC2616(req), cacheable: %#v\n", cacheable)
// 	// log.Printf("in cache.Transport.RoundTripRFC2616(req), canStore...: %#v\n", canStore(parseCacheControl(req.Header), parseCacheControl(resp.Header)))
// 	if cacheable && canStore(parseCacheControl(req.Header), parseCacheControl(resp.Header)) {
// 		for _, varyKey := range headerAllCommaSepValues(resp.Header, "vary") {
// 			varyKey = http.CanonicalHeaderKey(varyKey)
// 			fakeHeader := "X-Varied-" + varyKey
// 			reqValue := req.Header.Get(varyKey)
// 			if reqValue != "" {
// 				resp.Header.Set(fakeHeader, reqValue)
// 			}
// 		}
// 		log.Printf("in cache.Transport.RoundTripRFC2616(req), req.Method: %#v\n", req.Method)
// 		switch req.Method {
// 		case "GET":
// 			// Delay caching until EOF is reached.
// 			resp.Body = &cachingReadCloser{
// 				R: resp.Body,
// 				OnEOF: func(r io.Reader) {
// 					resp := *resp
// 					resp.Body = ioutil.NopCloser(r)
// 					respBytes, err := httputil.DumpResponse(&resp, true)
// 					log.Printf("in cache.Transport.RoundTripRFC2616(req), err: %#v\n", err)
// 					if err == nil {
// 						t.Cache.Set(cacheKey, respBytes)
// 					}
// 				},
// 			}
// 		default:
// 			respBytes, err := httputil.DumpResponse(resp, true)
// 			if err == nil {
// 				t.Cache.Set(cacheKey, respBytes)
// 			}
// 		}
// 	} else {
// 		t.Cache.Delete(cacheKey)
// 	}
// 	return resp, nil
// }

// // ErrNoDateHeader indicates that the HTTP headers contained no Date header.
// var ErrNoDateHeader = errors.New("no Date header")

// // Date parses and returns the value of the Date header.
// func Date(respHeaders http.Header) (date time.Time, err error) {
// 	dateHeader := respHeaders.Get("date")
// 	if dateHeader == "" {
// 		err = ErrNoDateHeader
// 		return
// 	}

// 	return time.Parse(time.RFC1123, dateHeader)
// }

// type realClock struct{}

// func (c *realClock) since(d time.Time) time.Duration {
// 	return time.Since(d)
// }

// type timer interface {
// 	since(d time.Time) time.Duration
// }

// var clock timer = &realClock{}

// // getFreshness will return one of fresh/stale/transparent based on the cache-control
// // values of the request and the response
// //
// // fresh indicates the response can be returned
// // stale indicates that the response needs validating before it is returned
// // transparent indicates the response should not be used to fulfil the request
// //
// // Because this is only a private cache, 'public' and 'private' in cache-control aren't
// // signficant. Similarly, smax-age isn't used.
// func getFreshness(respHeaders, reqHeaders http.Header) (freshness int) {
// 	respCacheControl := parseCacheControl(respHeaders)
// 	reqCacheControl := parseCacheControl(reqHeaders)
// 	if _, ok := reqCacheControl["no-cache"]; ok {
// 		return transparent
// 	}
// 	if _, ok := respCacheControl["no-cache"]; ok {
// 		return stale
// 	}
// 	if _, ok := reqCacheControl["only-if-cached"]; ok {
// 		return fresh
// 	}

// 	date, err := Date(respHeaders)
// 	if err != nil {
// 		return stale
// 	}
// 	currentAge := clock.since(date)

// 	var lifetime time.Duration
// 	var zeroDuration time.Duration

// 	// If a response includes both an Expires header and a max-age directive,
// 	// the max-age directive overrides the Expires header, even if the Expires header is more restrictive.
// 	if maxAge, ok := respCacheControl["max-age"]; ok {
// 		lifetime, err = time.ParseDuration(maxAge + "s")
// 		if err != nil {
// 			lifetime = zeroDuration
// 		}
// 	} else {
// 		expiresHeader := respHeaders.Get("Expires")
// 		if expiresHeader != "" {
// 			expires, err := time.Parse(time.RFC1123, expiresHeader)
// 			if err != nil {
// 				lifetime = zeroDuration
// 			} else {
// 				lifetime = expires.Sub(date)
// 			}
// 		}
// 	}

// 	if maxAge, ok := reqCacheControl["max-age"]; ok {
// 		// the client is willing to accept a response whose age is no greater than the specified time in seconds
// 		lifetime, err = time.ParseDuration(maxAge + "s")
// 		if err != nil {
// 			lifetime = zeroDuration
// 		}
// 	}
// 	if minfresh, ok := reqCacheControl["min-fresh"]; ok {
// 		//  the client wants a response that will still be fresh for at least the specified number of seconds.
// 		minfreshDuration, err := time.ParseDuration(minfresh + "s")
// 		if err == nil {
// 			currentAge = time.Duration(currentAge + minfreshDuration)
// 		}
// 	}

// 	if maxstale, ok := reqCacheControl["max-stale"]; ok {
// 		// Indicates that the client is willing to accept a response that has exceeded its expiration time.
// 		// If max-stale is assigned a value, then the client is willing to accept a response that has exceeded
// 		// its expiration time by no more than the specified number of seconds.
// 		// If no value is assigned to max-stale, then the client is willing to accept a stale response of any age.
// 		//
// 		// Responses served only because of a max-stale value are supposed to have a Warning header added to them,
// 		// but that seems like a  hassle, and is it actually useful? If so, then there needs to be a different
// 		// return-value available here.
// 		if maxstale == "" {
// 			return fresh
// 		}
// 		maxstaleDuration, err := time.ParseDuration(maxstale + "s")
// 		if err == nil {
// 			currentAge = time.Duration(currentAge - maxstaleDuration)
// 		}
// 	}

// 	if lifetime > currentAge {
// 		return fresh
// 	}

// 	return stale
// }

// // Returns true if either the request or the response includes the stale-if-error
// // cache control extension: https://tools.ietf.org/html/rfc5861
// func canStaleOnError(respHeaders, reqHeaders http.Header) bool {
// 	respCacheControl := parseCacheControl(respHeaders)
// 	reqCacheControl := parseCacheControl(reqHeaders)

// 	var err error
// 	lifetime := time.Duration(-1)

// 	if staleMaxAge, ok := respCacheControl["stale-if-error"]; ok {
// 		if staleMaxAge != "" {
// 			lifetime, err = time.ParseDuration(staleMaxAge + "s")
// 			if err != nil {
// 				return false
// 			}
// 		} else {
// 			return true
// 		}
// 	}
// 	if staleMaxAge, ok := reqCacheControl["stale-if-error"]; ok {
// 		if staleMaxAge != "" {
// 			lifetime, err = time.ParseDuration(staleMaxAge + "s")
// 			if err != nil {
// 				return false
// 			}
// 		} else {
// 			return true
// 		}
// 	}

// 	if lifetime >= 0 {
// 		date, err := Date(respHeaders)
// 		if err != nil {
// 			return false
// 		}
// 		currentAge := clock.since(date)
// 		if lifetime > currentAge {
// 			return true
// 		}
// 	}

// 	return false
// }

// func getEndToEndHeaders(respHeaders http.Header) []string {
// 	// These headers are always hop-by-hop
// 	hopByHopHeaders := map[string]struct{}{
// 		"Connection":          {},
// 		"Keep-Alive":          {},
// 		"Proxy-Authenticate":  {},
// 		"Proxy-Authorization": {},
// 		"Te":                  {},
// 		"Trailers":            {},
// 		"Transfer-Encoding":   {},
// 		"Upgrade":             {},
// 	}

// 	for _, extra := range strings.Split(respHeaders.Get("connection"), ",") {
// 		// any header listed in connection, if present, is also considered hop-by-hop
// 		if strings.Trim(extra, " ") != "" {
// 			hopByHopHeaders[http.CanonicalHeaderKey(extra)] = struct{}{}
// 		}
// 	}
// 	var endToEndHeaders []string
// 	for respHeader := range respHeaders {
// 		if _, ok := hopByHopHeaders[respHeader]; !ok {
// 			endToEndHeaders = append(endToEndHeaders, respHeader)
// 		}
// 	}
// 	return endToEndHeaders
// }

// func canStore(reqCacheControl, respCacheControl cacheControl) (canStore bool) {
// 	if _, ok := respCacheControl["no-store"]; ok {
// 		return false
// 	}
// 	if _, ok := reqCacheControl["no-store"]; ok {
// 		return false
// 	}
// 	return true
// }

// func newGatewayTimeoutResponse(req *http.Request) *http.Response {
// 	var braw bytes.Buffer
// 	braw.WriteString("HTTP/1.1 504 Gateway Timeout\r\n\r\n")
// 	resp, err := http.ReadResponse(bufio.NewReader(&braw), req)
// 	if err != nil {
// 		panic(err)
// 	}
// 	return resp
// }

// // cloneRequest returns a clone of the provided *http.Request.
// // The clone is a shallow copy of the struct and its Header map.
// // (This function copyright goauth2 authors: https://code.google.com/p/goauth2)
// func cloneRequest(r *http.Request) *http.Request {
// 	// shallow copy of the struct
// 	r2 := new(http.Request)
// 	*r2 = *r
// 	// deep copy of the Header
// 	r2.Header = make(http.Header)
// 	for k, s := range r.Header {
// 		r2.Header[k] = s
// 	}
// 	return r2
// }

// type cacheControl map[string]string

// func parseCacheControl(headers http.Header) cacheControl {
// 	cc := cacheControl{}
// 	ccHeader := headers.Get("Cache-Control")
// 	for _, part := range strings.Split(ccHeader, ",") {
// 		part = strings.Trim(part, " ")
// 		if part == "" {
// 			continue
// 		}
// 		if strings.ContainsRune(part, '=') {
// 			keyval := strings.Split(part, "=")
// 			cc[strings.Trim(keyval[0], " ")] = strings.Trim(keyval[1], ",")
// 		} else {
// 			cc[part] = ""
// 		}
// 	}
// 	return cc
// }

// // headerAllCommaSepValues returns all comma-separated values (each
// // with whitespace trimmed) for header name in headers. According to
// // Section 4.2 of the HTTP/1.1 spec
// // (http://www.w3.org/Protocols/rfc2616/rfc2616-sec4.html#sec4.2),
// // values from multiple occurrences of a header should be concatenated, if
// // the header's value is a comma-separated list.
// func headerAllCommaSepValues(headers http.Header, name string) []string {
// 	var vals []string
// 	for _, val := range headers[http.CanonicalHeaderKey(name)] {
// 		fields := strings.Split(val, ",")
// 		for i, f := range fields {
// 			fields[i] = strings.TrimSpace(f)
// 		}
// 		vals = append(vals, fields...)
// 	}
// 	return vals
// }

// // cachingReadCloser is a wrapper around ReadCloser R that calls OnEOF
// // handler with a full copy of the content read from R when EOF is
// // reached.
// type cachingReadCloser struct {
// 	// Underlying ReadCloser.
// 	R io.ReadCloser
// 	// OnEOF is called with a copy of the content of R when EOF is reached.
// 	OnEOF func(io.Reader)

// 	buf bytes.Buffer // buf stores a copy of the content of R.
// }

// // Read reads the next len(p) bytes from R or until R is drained. The
// // return value n is the number of bytes read. If R has no data to
// // return, err is io.EOF and OnEOF is called with a full copy of what
// // has been read so far.
// func (r *cachingReadCloser) Read(p []byte) (n int, err error) {
// 	n, err = r.R.Read(p)
// 	r.buf.Write(p[:n])
// 	if err == io.EOF {
// 		r.OnEOF(bytes.NewReader(r.buf.Bytes()))
// 	}
// 	return n, err
// }

// func (r *cachingReadCloser) Close() error {
// 	return r.R.Close()
// }

// // PleaseCache excercises a Cache implementation.
// func PleaseCache(t *testing.T, cache Cache) {
// 	key := "testKey"
// 	_, ok := cache.Get(key)
// 	if ok {
// 		t.Fatal("retrieved key before adding it")
// 	}

// 	val := []byte("some bytes")
// 	cache.Set(key, val)

// 	retVal, ok := cache.Get(key)
// 	if !ok {
// 		t.Fatal("could not retrieve an element we just added")
// 	}
// 	if !bytes.Equal(retVal, val) {
// 		t.Fatal("retrieved a different value than what we put in")
// 	}

// 	cache.Delete(key)

// 	_, ok = cache.Get(key)
// 	if ok {
// 		t.Fatal("deleted key still present")
// 	}
// }

// // NewMemoryCacheTransport returns a new Transport using the in-memory cache implementation
// func NewMemoryCacheTransport() *Transport {
// 	c := memorycache.New()
// 	t := NewTransport(c)
// 	return t
// }
