package fetch

import (
	"fmt"
	"sync"
)

// Cache is an implementation of Cache that stores responses in an in-memory map.
type MemoryCache struct {
	fallback Cache
	mu       sync.RWMutex
	items    map[string][]byte
}

// New returns a new Cache that will store items in an in-memory map
func NewMemoryCache(fallback Cache) *MemoryCache {
	return &MemoryCache{
		fallback: fallback,
		items:    map[string][]byte{},
	}
}

// Get returns the []byte representation of the response and true if present, false if not
func (c *MemoryCache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	resp, ok := c.items[key]
	c.mu.RUnlock()

	if ok {
		return resp, true
	}

	if ShowCaching {
		fmt.Println("in fetch.MemoryCache.Get, cache miss", "key", key)
	}
	if c.fallback == nil {
		fmt.Println("in fetch.MemoryCache.Get, no fallback")
		// if PanicOnCacheMiss {
		panic("memorycache fail for key: " + key)
	}
	resp, ok = c.fallback.Get(key)
	if !ok {
		panic("memorycache fail for key: " + key)
		// return nil, false
	}
	c.Set(key, resp)
	return resp, true
}

// Set saves response resp to the cache with key
func (c *MemoryCache) Set(key string, resp []byte) {
	c.mu.Lock()
	c.items[key] = resp
	c.mu.Unlock()
}

// Delete removes key from the cache
func (c *MemoryCache) Delete(key string) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}
