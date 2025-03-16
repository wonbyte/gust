// Package gust implements a generic in-memory cache with expiration, asynchronous
// refreshing, and background reaping of expired items. The cache stores key-value
// pairs along with an expiration time determined by a configurable TTL (time-to-live).
// When a key is not present or its corresponding item is expired, a user-provided fetcher
// function is invoked to obtain the value. The package also supports asynchronous refresh
// of stale items and rate limiting of fetch operations to prevent excessive load.
// Expired items are periodically removed from the cache using a background reaper goroutine.
package gust

import (
	"sync"
	"time"
)

const (
	// graceLimit is the duration an item remains accessible before it is considered expired.
	graceLimit time.Duration = 20 * time.Second

	// fetchTimeLimit is the minimum interval between fetches for the same key.
	fetchTimeLimit time.Duration = 5 * time.Second

	// reaperFrequency is how often the cache will scan for expired items.
	reaperFrequency time.Duration = 5 * time.Minute

	// reaperLimit is the maximum number of items to examine per reaper cycle.
	reaperLimit int = 1000
)

// state represents the state of a cached item.
type state byte

const (
	// ok indicates that the cached item is still valid.
	ok state = iota
	// stale indicates that the cached item is expired but still within the grace period.
	stale
	// expired indicates that the cached item has exceeded its grace period.
	expired
)

// Item represents a single cache entry.
type Item[T any] struct {
	expires time.Time
	value   T
}

// State returns the current state of the item by comparing the current time with its expiration.
func (i *Item[T]) State() state {
	now := time.Now()
	if now.Before(i.expires) {
		return ok
	}
	if now.Sub(i.expires) > graceLimit {
		return expired
	}
	return stale
}

// Fetcher defines a function type that fetches data based on a key.
// If the fetch is unsuccessful, the value will not be cached.
type Fetcher[K comparable, T any] func(key K) (T, bool)

// Cache represents an in-memory key-value store with expiration and background reaping.
type Cache[K comparable, T any] struct {
	fetcher   Fetcher[K, T]
	items     map[K]*Item[T]
	fetchings map[K]time.Time
	done      chan struct{}
	ttl       time.Duration
	sync.RWMutex
	fetchingLock sync.Mutex
}

// New creates a new cache instance with the given fetcher function and TTL.
// It also starts a background reaper process that cleans up expired items.
func New[K comparable, T any](fetcher Fetcher[K, T], ttl time.Duration) *Cache[K, T] {
	c := &Cache[K, T]{
		fetcher:   fetcher,
		items:     make(map[K]*Item[T]),
		fetchings: make(map[K]time.Time),
		done:      make(chan struct{}),
		ttl:       ttl,
	}
	go c.startReaper(time.NewTicker(reaperFrequency))
	return c
}

// Get retrieves a cached item by key, fetching it if necessary.
// It returns the value and a boolean indicating whether the value is valid.
func (c *Cache[K, T]) Get(key K) (T, bool) {
	c.RLock()
	item, exists := c.items[key]
	c.RUnlock()

	if !exists {
		return c.fetch(key)
	}

	//nolint:exhaustive
	switch item.State() {
	case stale:
		go c.asyncFetch(key)
		return item.value, true
	case expired:
		return c.fetch(key)
	case ok:
		return item.value, true
	}

	// This point should never be reached if all states are covered.
	panic("unreachable: invalid cache state")
}

// Set stores a value in the cache with an expiration time based on the cache's TTL.
func (c *Cache[K, T]) Set(key K, value T) {
	item := &Item[T]{
		value:   value,
		expires: time.Now().Add(c.ttl),
	}
	c.Lock()
	c.items[key] = item
	c.Unlock()
}

// Replace updates an existing cached item, but does nothing if the key does not exist.
func (c *Cache[K, T]) Replace(key K, value T) {
	c.Lock()
	defer c.Unlock()
	if _, exists := c.items[key]; exists {
		c.items[key] = &Item[T]{
			value:   value,
			expires: time.Now().Add(c.ttl),
		}
	}
}

// Delete removes a cached item by key.
func (c *Cache[K, T]) Delete(key K) {
	c.Lock()
	delete(c.items, key)
	c.Unlock()
}

// Clear removes all cached items.
func (c *Cache[K, T]) Clear() {
	c.Lock()
	c.items = make(map[K]*Item[T])
	c.Unlock()
}

// fetch retrieves the value using the fetcher function and stores it in the cache.
// If the fetch is unsuccessful (bool == false), the cache is not updated.
func (c *Cache[K, T]) fetch(key K) (T, bool) {
	value, ok := c.fetcher(key)
	if !ok {
		var zero T
		return zero, false
	}
	c.Set(key, value)
	return value, true
}

// asyncFetch performs a refresh of a stale item. It ensures that if a refresh
// for a given key is already in progress, it will not trigger another until
// fetchTimeLimit has elapsed.
func (c *Cache[K, T]) asyncFetch(key K) {
	now := time.Now()

	c.fetchingLock.Lock()
	if expiry, inProgress := c.fetchings[key]; inProgress && now.Before(expiry) {
		c.fetchingLock.Unlock()
		return
	}
	c.fetchings[key] = now.Add(fetchTimeLimit)
	c.fetchingLock.Unlock()

	defer func() {
		c.fetchingLock.Lock()
		delete(c.fetchings, key)
		c.fetchingLock.Unlock()
	}()

	c.fetch(key)
}

// startReaper launches a background goroutine that periodically removes expired items.
func (c *Cache[K, T]) startReaper(ticker *time.Ticker) {
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.reap()
		case <-c.done:
			return
		}
	}
}

// stop signals the background reaper to terminate.
func (c *Cache[K, T]) stop() {
	close(c.done)
}

// reap removes expired items from the cache, up to reaperLimit keys at a time.
func (c *Cache[K, T]) reap() {
	c.RLock()
	expiredKeys := make([]K, 0, reaperLimit)
	count := 0

	for key, item := range c.items {
		if item.State() == expired {
			expiredKeys = append(expiredKeys, key)
		}
		count++
		if count >= reaperLimit {
			break
		}
	}
	c.RUnlock()

	if len(expiredKeys) == 0 {
		return
	}

	c.Lock()
	for _, key := range expiredKeys {
		delete(c.items, key)
	}
	c.Unlock()
}
