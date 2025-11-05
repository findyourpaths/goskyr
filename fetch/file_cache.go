package fetch

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	"github.com/findyourpaths/goskyr/utils"
)

// var cacheResponseSuffix = ".html"

var DefaultMaxBody int64 = 1024 * 1024 * 1024 // 1GB

// Cache is an implementation of Geziyor cache.Cache that stores html pages on disk.
type FileCache struct {
	filenameFn func(string, string) string
	suffix     string
	fallback   Cache
	parentDir  string
	writeable  bool
}

// New returns a new Cache that will store files in dir.
func NewURLFileCache(fallback Cache, parentDir string, writeable bool) *FileCache {
	return &FileCache{
		filenameFn: CacheURLFilename,
		fallback:   fallback,
		parentDir:  parentDir,
		writeable:  writeable,
	}
}

// New returns a new Cache that will store files in dir.
func NewFileCache(fallback Cache, parentDir string, writeable bool, filenameFn func(string, string) string) *FileCache {
	return &FileCache{
		filenameFn: filenameFn,
		fallback:   fallback,
		parentDir:  parentDir,
		writeable:  writeable,
	}
}

// Get returns the response corresponding to key, and true, if
// present in InputDir or OutputDir. Otherwise it returns nil and false.
func (c *FileCache) Get(key string) ([]byte, bool) {
	// if strings.Index(key, "facebook") != -1 {
	// 	panic("trying to get a facebook page")
	// }

	if DoDebug {
		fmt.Println("fetch.FileCache.Get()", "key", key)
	}
	p := c.filenameFn(c.parentDir, key)
	if DoDebug {
		fmt.Println("in fetch.FileCache.Get()", "p", p)
	}
	resp, err := utils.ReadBytesFile(p)
	if err == nil {
		if ShowCaching {
			fmt.Println("in fetch.FileCache.Get(), cache hit", "key", key, "c.parentDir", c.parentDir)
		}
		if c.writeable {
			c.Set(key, resp)
		}
		return resp, true
	}

	if ShowCaching {
		fmt.Println("in fetch.FileCache.Get(), cache miss", "key", key, "c.parentDir", c.parentDir)
	}
	if c.fallback == nil {
		if ShowCaching {
			fmt.Println("in fetch.FileCache.Get(), no fallback")
		}
		return nil, false
	}

	var ok bool
	resp, ok = c.fallback.Get(key)
	if !ok {
		if ShowCaching {
			fmt.Println("in fetch.FileCache.Get(), fallback failed")
		}
		return nil, false
	}

	if c.writeable {
		c.Set(key, resp)
	}
	return resp, true
}

// Set saves a response to the cache as key
func (c *FileCache) Set(key string, resp []byte) {
	if DoDebug {
		fmt.Println("fetch.FileCache.Set()", "key", key, "len(resp)", len(resp), "c.writeable", c.writeable)
	}
	if !c.writeable {
		return
	}
	p := c.filenameFn(c.parentDir, key)
	if err := utils.WriteBytesFile(p, resp); err != nil {
		slog.Warn("fetch.FileCache.Set(), failed to write to cache at", "path", p, "error", err.Error())
	}
}

// Delete removes the response with key from the cache
func (c *FileCache) Delete(key string) {
	if DoDebug {
		fmt.Println("fetch.FileCache.Delete()", "key", key)
	}
	p := c.filenameFn(c.parentDir, key)
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		// p = keyToFilename(InputDir, key)
		// if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		slog.Warn("fetch.FileCache.Delete(), failed to find cache entry at", "path", p, "error", err.Error())
		// }
	}
	if err := os.Remove(p); err != nil {
		slog.Warn("fetch.FileCache.Delete(), failed to remove cache entry at", "path", p, "error", err.Error())
	}
}

// func ResponseFilename(dir string, urlStr string) string {
// 	return c.filename(dir, urlStr) + c.suffix
// }

// func (c *FileCache) ResponseFilename(dir string, urlStr string) string {
// 	return c.filename(dir, urlStr) + c.suffix
// }

func CacheURLFilename(dir string, urlStr string) string {
	// return CacheURLFilebase(dir, urlStr) + ".html"
	return CacheURLFilebase(dir, urlStr)
}

func CacheURLFilebase(dir string, urlStr string) string {
	if dir == "" {
		panic("need to set Filename dir")
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
	return filepath.Join(dir, uHostSlug, uSlug) + ".html"
}

// GetResolvedURL returns the final URL after following redirects.
// For FileCache, we delegate to the fallback cache if available.
func (c *FileCache) GetResolvedURL(rawURL string) (string, error) {
	if c.fallback != nil {
		return c.fallback.GetResolvedURL(rawURL)
	}
	// If no fallback, just return the URL as-is (no redirect resolution)
	return rawURL, nil
}
