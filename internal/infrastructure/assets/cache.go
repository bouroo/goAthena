package assets

import (
	"container/list"
	"sync"
)

// Cache is a thread-safe, byte-budgeted LRU cache for decoded asset payloads.
type Cache struct {
	mu        sync.Mutex
	maxBytes  int64
	usedBytes int64
	ll        *list.List
	items     map[string]*list.Element
}

type cacheEntry struct {
	key   string
	value []byte
}

// NewCache constructs an LRU cache bounded by maxBytes total payload size.
// A non-positive maxBytes disables the budget (no eviction on size).
func NewCache(maxBytes int64) *Cache {
	return &Cache{
		maxBytes: maxBytes,
		ll:       list.New(),
		items:    make(map[string]*list.Element),
	}
}

// Get returns the cached value for key and reports presence. Hits move the
// entry to the front of the LRU.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	entry, _ := el.Value.(*cacheEntry)
	return entry.value, true
}

// Put inserts or updates key with value. Evicts oldest entries until the
// total payload size fits within maxBytes.
func (c *Cache) Put(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		entry, _ := el.Value.(*cacheEntry)
		c.usedBytes += int64(len(value)) - int64(len(entry.value))
		entry.value = value
		c.ll.MoveToFront(el)
	} else {
		entry := &cacheEntry{key: key, value: value}
		c.items[key] = c.ll.PushFront(entry)
		c.usedBytes += int64(len(value))
	}

	if c.maxBytes > 0 {
		// If the single item exceeds the budget, remove it and return.
		// This prevents evicting the entire cache for one oversized entry.
		if int64(len(value)) > c.maxBytes {
			if el, ok := c.items[key]; ok {
				c.ll.Remove(el)
				delete(c.items, key)
				c.usedBytes -= int64(len(value))
			}
			return
		}
		for c.usedBytes > c.maxBytes {
			c.evictOldest()
			if c.ll.Len() == 0 {
				break
			}
		}
	}
}

// Len returns the number of cached entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// Clear removes all entries.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.items = make(map[string]*list.Element)
	c.usedBytes = 0
}

func (c *Cache) evictOldest() {
	el := c.ll.Back()
	if el == nil {
		return
	}
	entry, _ := el.Value.(*cacheEntry)
	delete(c.items, entry.key)
	c.ll.Remove(el)
	c.usedBytes -= int64(len(entry.value))
}
