package main

import (
	"fmt"
	"github.com/wonbyte/gust"
	"time"
)

// ExampleFetcher is a simple fetcher function that simulates retrieving a value.
func ExampleFetcher(key string) (string, bool) {
	// Simulate a delay in fetching data.
	time.Sleep(50 * time.Millisecond)
	return "Value for " + key, true
}

func main() {
	// Create a new cache instance with a TTL of 1 minute.
	cache := gust.New(ExampleFetcher, time.Minute)

	// Set a value explicitly.
	cache.Set("greeting", "Hello, gust!")

	// Get a value from the cache.
	val, ok := cache.Get("greeting")
	if ok {
		fmt.Println("Cached greeting:", val)
	} else {
		fmt.Println("Greeting not found in cache.")
	}

	// Demonstrate fetch on missing key.
	missingKey := "farewell"
	fmt.Println("Fetching missing key:", missingKey)
	val, ok = cache.Get(missingKey)
	if ok {
		fmt.Println("Fetched value for", missingKey, ":", val)
	} else {
		fmt.Println("Could not fetch value for", missingKey)
	}

	// Replace a value.
	cache.Replace("greeting", "Hi, gust!")
	val, ok = cache.Get("greeting")
	if ok {
		fmt.Println("Replaced greeting:", val)
	}

	// Delete a key.
	cache.Delete("farewell")
	_, ok = cache.Get("farewell")
	if !ok {
		fmt.Println("Value for 'farewell' was deleted from cache.")
	}

	// Clear the entire cache.
	cache.Clear()
	_, ok = cache.Get("greeting")
	if !ok {
		fmt.Println("Cache is cleared, no greeting found.")
	}
}
