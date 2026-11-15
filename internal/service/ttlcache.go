package service

import (
	"container/list"
	"sync"
	"time"
)

// ttlCache is a tiny thread-safe LRU with per-entry expiry. We use it instead of
// hashicorp/golang-lru to keep dependencies minimal (≈50 lines, fully tested).
type ttlCache[K comparable, V any] struct {
	mu    sync.Mutex
	max   int
	ttl   time.Duration
	now   func() time.Time
	ll    *list.List // front = most recently used
	items map[K]*list.Element
}

type ttlEntry[K comparable, V any] struct {
	key     K
	val     V
	expires time.Time
}

func newTTLCache[K comparable, V any](maxEntries int, ttl time.Duration) *ttlCache[K, V] {
	return &ttlCache[K, V]{max: max(1, maxEntries), ttl: ttl, now: time.Now, ll: list.New(), items: map[K]*list.Element{}}
}

// Get returns the value if present and not expired.
func (c *ttlCache[K, V]) Get(k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.items[k]
	if !ok {
		return zero, false
	}
	e := el.Value.(*ttlEntry[K, V])
	if c.now().After(e.expires) {
		c.ll.Remove(el)
		delete(c.items, k)
		return zero, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

// Put inserts or replaces a value and evicts the least recently used entry when full.
func (c *ttlCache[K, V]) Put(k K, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp := c.now().Add(c.ttl)
	if el, ok := c.items[k]; ok {
		e := el.Value.(*ttlEntry[K, V])
		e.val, e.expires = v, exp
		c.ll.MoveToFront(el)
		return
	}
	c.items[k] = c.ll.PushFront(&ttlEntry[K, V]{key: k, val: v, expires: exp})
	for c.ll.Len() > c.max {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.items, last.Value.(*ttlEntry[K, V]).key)
	}
}

// Len returns the number of entries (expired ones included until touched).
func (c *ttlCache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
