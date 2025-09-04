package fetch

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// var cacheResponseSuffix = ".html"

// var ShowHits = false

// var PanicOnCacheMiss = false

// var DefaultMaxBody int64 = 1024 * 1024 * 1024 // 1GB

// FetchCache is an implementation of Cache that fetches webpages.
// web.
type FetchCache struct {
	fetcher Fetcher
}

// New returns a new FetchCache that will fetch webpages
func NewFetchCache(fetcher Fetcher) *FetchCache {
	return &FetchCache{
		fetcher: fetcher,
	}
}

// Get returns the response corresponding to key, and true, if found on the
// web. Otherwise it returns nil and false.
func (c *FetchCache) Get(key string) ([]byte, bool) {
	slog.Info("FetchCache.Get()", "key", key)
	if c.fetcher == nil {
		panic(fmt.Sprintf("in FetchCache.Get(), fetcher is nil, may be running offline, failed to fetch %q", key))
	}
	urlResp, err := c.fetcher.Fetch(key, nil)
	// slog.Info("in FetchCache.Get(), fetched", "key", key, "err", err)
	if err != nil {
		fmt.Println("in fetch.FetchCache.Get(), got error fetching data from URL", "key", key, "err", err)
		return nil, false
	}

	gqdoc, err := GQDocumentFromURLResponse(urlResp)
	if err != nil {
		fmt.Println("in fetch.FetchCache.Get(), got error getting GQDocument from response", "key", key, "err", err)
		return nil, false
	}

	str, err := goquery.OuterHtml(gqdoc.Children())
	if err != nil {
		fmt.Println("in fetch.FetchCache.Get(), got error setting cache", "key", key, "err", err)
		return nil, false
	}

	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(str)),
	}
	r, err := httputil.DumpResponse(resp, true)
	if err != nil {
		fmt.Println("in fetch.FetchCache.Get(), got error dumping response", "key", key, "err", err)
		return nil, false
	}

	if ShowCaching {
		fmt.Println("in fetch.FetchCache.Get(), cache hit", "key", key)
	}
	return r, true
}

func (c *FetchCache) Set(key string, resp []byte) {
	return
}

func (c *FetchCache) Delete(key string) {
	return
}
