package sample

import (
	"fmt"
	"sync"
)

// DefaultTimeout is the default timeout in milliseconds.
const DefaultTimeout = 5000

// ErrNotFound is returned when a key is not found.
var ErrNotFound error

// CacheEntry represents a single cache entry.
type CacheEntry struct {
	Key   string
	Value interface{}
}

// Searcher defines the interface for searching symbols.
type Searcher interface {
	Search(query string) ([]CacheEntry, error)
	Count() int
}

// Handler is a function type for handling requests.
type Handler func(key string) (interface{}, error)

// Cache provides a thread-safe in-memory key-value store.
type Cache struct {
	data map[string]interface{}
	mu   sync.RWMutex
}

// NewCache creates and initializes a new Cache.
func NewCache() *Cache {
	return &Cache{
		data: make(map[string]interface{}),
	}
}

// Get retrieves a value by key.
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.data[key]
	return val, ok
}

// Set stores a key-value pair.
func (c *Cache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = value
}

// Delete removes a key from the cache.
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
}

// PrintAll prints all keys in the cache.
func PrintAll(c *Cache) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for k := range c.data {
		fmt.Println(k)
	}
}
