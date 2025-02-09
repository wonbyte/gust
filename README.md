# gust

[![CI](https://github.com/wonbyte/gust/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/wonbyte/gust/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/wonbyte/gust?cache=v1)](https://goreportcard.com/report/github.com/wonbyte/gust)
[![GoDoc](https://godoc.org/github.com/wonbyte/gust?status.svg)](https://godoc.org/github.com/wonbyte/gust)
![Coverage](https://raw.githubusercontent.com/wonbyte/gust/badges/.badges/main/coverage.svg)

**gust** is a simple, thread-safe, generic caching library designed for caching
a small number of items. It periodically removes stale items and helps mitigate
the thundering herd problem. Note that gust's growth is unbounded, so it is best
used for scenarios with a limited number of cache entries.

## Usage

Below is an example demonstrating how to use gust:

```go
// Application represents a sample application.
type Application struct {
	Name string
	// Additional fields can be added here.
}

// fetchApplication simulates fetching an application from a data source.
func fetchApplication(key string) *Application {
	log.Printf("Fetching application for key: %s", key)
	// Simulate data retrieval, e.g., from a database.
	return &Application{Name: "Application " + key}
}

// Initialize gust with a fetcher function and a TTL of 2 minutes.
// Here, we use string as the key type and *Application as the value type.
cache := gust.New(fetcher, time.Minute*1)

// Retrieve an item from the cache. If it does not exist or is expired,
// the fetcher function will be invoked.
item := cache.Get("item")
```

### TTL and Grace Period

- Items in the cache have a TTL of **1 minutes** (60 seconds).
- There is an additional **20-second grace period** during which an expired item may still be returned.
  - This means the effective TTL is between **60 and 80 seconds**.
  - Even if multiple goroutines concurrently call `Get` on an item within this grace period, only one
  call to the fetcher function will occur.

## API Methods

- **Get(key K) V**
  Retrieves the value associated with the given key. If the item is not present
  or has expired (beyond the grace window), it is fetched using the fetcher function.

- **Set(key K, value V)**
  Manually sets a value in the cache.

- **Replace(key K, value V)**
  Replaces an existing value and extends its TTL. If the key does not exist, the
  operation is a no-op.

- **Delete(key K)**
  Removes an item from the cache.

- **Clear()**
  Removes all items from the cache.

## Summary

gust is ideal for scenarios where a small number of items need to be cached with
built-in expiration and concurrency support. Its design minimizes the impact of
cache misses by ensuring that multiple concurrent requests for the same key during
the grace period result in only a single fetch operation.
