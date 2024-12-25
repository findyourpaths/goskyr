package fetch

import (
	"fmt"
	"io"
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
	gqdoc, err := GQDocument(c.fetcher, key, nil)
	if err != nil {
		fmt.Println("in fetch.FetchCache.Get(), got error getting GQDocument", "err", err)
		return nil, false
	}

	str, err := goquery.OuterHtml(gqdoc.Children())
	if err != nil {
		fmt.Println("in fetch.FetchCache.Get(), got error setting cache", "err", err)
		return nil, false
	}

	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(str)),
	}
	r, err := httputil.DumpResponse(resp, true)
	if err != nil {
		fmt.Println("in fetch.FetchCache.Get(), got error dumping response", "err", err)
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
