package middlewares

import (
	"sync"
	"time"
)

type fileCache[T any] struct {
	mu    sync.Mutex
	items map[string]fileCacheEntry[T]
}

type fileCacheEntry[T any] struct {
	mtime time.Time
	value T
}

func (c *fileCache[T]) get(path string, mtime time.Time, load func() (T, bool)) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[string]fileCacheEntry[T])
	}
	if e, ok := c.items[path]; ok && e.mtime.Equal(mtime) {
		return e.value, true
	}
	v, ok := load()
	if !ok {
		var zero T
		return zero, false
	}
	c.items[path] = fileCacheEntry[T]{mtime: mtime, value: v}
	return v, true
}
