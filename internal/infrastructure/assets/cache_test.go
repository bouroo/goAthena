//go:build unit

package assets

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCachePutGet(t *testing.T) {
	c := NewCache(1024)
	c.Put("a", []byte("hello"))

	got, ok := c.Get("a")
	assert.True(t, ok)
	assert.Equal(t, []byte("hello"), got)

	_, ok = c.Get("missing")
	assert.False(t, ok)
}

func TestCacheUpdate(t *testing.T) {
	c := NewCache(1024)
	c.Put("k", []byte("short"))
	c.Put("k", []byte("a longer value here"))

	got, ok := c.Get("k")
	assert.True(t, ok)
	assert.Equal(t, []byte("a longer value here"), got)
	assert.Equal(t, 1, c.Len())
}

func TestCacheEviction(t *testing.T) {
	c := NewCache(10)
	c.Put("a", []byte("12345"))
	c.Put("b", []byte("67890"))
	c.Put("c", []byte("abcde"))

	_, ok := c.Get("a")
	assert.False(t, ok, "oldest entry should be evicted")
	assert.Equal(t, 2, c.Len())

	_, ok = c.Get("b")
	assert.True(t, ok)
	_, ok = c.Get("c")
	assert.True(t, ok)
}

func TestCacheGetMovesToFront(t *testing.T) {
	c := NewCache(10)
	c.Put("a", []byte("12345"))
	c.Put("b", []byte("67890"))

	_, _ = c.Get("a")
	c.Put("c", []byte("vwxyz"))

	_, ok := c.Get("a")
	assert.True(t, ok, "recently-touched entry must survive eviction")
	_, ok = c.Get("b")
	assert.False(t, ok)
}

func TestCacheClear(t *testing.T) {
	c := NewCache(100)
	c.Put("a", []byte("x"))
	c.Put("b", []byte("y"))
	c.Clear()
	assert.Equal(t, 0, c.Len())
	_, ok := c.Get("a")
	assert.False(t, ok)
}

func TestCacheZeroBudget(t *testing.T) {
	c := NewCache(0)
	c.Put("a", []byte("anything"))
	assert.Equal(t, 0, c.Len(), "zero budget should reject all puts")
}

func TestCacheConcurrent(t *testing.T) {
	c := NewCache(1 << 20)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := []byte{byte(i)}
			c.Put(string(key), key)
			_, _ = c.Get(string(key))
		}(i)
	}
	wg.Wait()
	assert.Greater(t, c.Len(), 0)
}
