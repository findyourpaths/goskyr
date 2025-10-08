package fetch

import (
	"fmt"
	"sync"
)

// Cache is an implementation of Cache that stores responses in an in-memory map.
type MemoryCache struct {
	fallback        Cache
	respsByKey      map[string][]byte
	respsByKeyMutex sync.RWMutex
}

// New returns a new Cache that will store items in an in-memory map
func NewMemoryCache(fallback Cache) *MemoryCache {
	return &MemoryCache{
		fallback:   fallback,
		respsByKey: map[string][]byte{},
	}
}

// Get returns the []byte representation of the response and true if present, false if not
func (c *MemoryCache) Get(key string) ([]byte, bool) {
	c.respsByKeyMutex.RLock()
	resp, ok := c.respsByKey[key]
	c.respsByKeyMutex.RUnlock()
	if ok {
		return resp, true
	}

	if ShowCaching {
		fmt.Println("in fetch.MemoryCache.Get(), cache miss", "key", key)
	}
	if c.fallback == nil {
		fmt.Println("in fetch.MemoryCache.Get(), no fallback")
		return nil, false
	}

	resp, ok = c.fallback.Get(key)
	if !ok {
		return nil, false
	}
	c.Set(key, resp)
	return resp, true
}

// Set saves response resp to the cache with key
func (c *MemoryCache) Set(key string, resp []byte) {
	c.respsByKeyMutex.Lock()
	c.respsByKey[key] = resp
	c.respsByKeyMutex.Unlock()
}

// Delete removes key from the cache
func (c *MemoryCache) Delete(key string) {
	c.respsByKeyMutex.Lock()
	delete(c.respsByKey, key)
	c.respsByKeyMutex.Unlock()
}

// GetResolvedURL returns the final URL after following redirects
func (c *MemoryCache) GetResolvedURL(rawURL string) (string, error) {
	// MemoryCache delegates to fallback if available
	if c.fallback != nil {
		return c.fallback.GetResolvedURL(rawURL)
	}
	// No fallback - just return the raw URL
	return rawURL, nil
}
