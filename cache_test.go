package gust

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestGetForMissingKey ensures that when a key is not present, the fetcher is called
// and its value is returned.
func TestGetForMissingKey(t *testing.T) {
	var counter atomic.Int32
	fetcher := func(key string) (string, bool) {
		newCount := counter.Add(1)
		return fmt.Sprintf("value %d", newCount), true
	}

	cache := New(fetcher, time.Minute)

	val, ok := cache.Get("foo")
	if !ok {
		t.Fatalf("Expected value, got none")
	}
	if val != "value 1" {
		t.Errorf("Expected 'value 1', got '%s'", val)
	}
}

// TestSet verifies that values stored with Set are returned by Get.
func TestSet(t *testing.T) {
	fetcher := func(key string) (string, bool) {
		return "fetched", true
	}
	cache := New(fetcher, time.Minute)

	cache.Set("bar", "set")

	val, ok := cache.Get("bar")
	if !ok {
		t.Fatalf("Expected value for key 'bar'")
	}
	if val != "set" {
		t.Errorf("Expected 'set', got '%s'", val)
	}
}

// TestStaleItemRefresh simulates a stale item and verifies that an async refresh occurs.
func TestStaleItemRefresh(t *testing.T) {
	var counter atomic.Int32
	fetcher := func(key string) (string, bool) {
		return fmt.Sprintf("fetched-%d", counter.Add(1)), true
	}

	cache := New(fetcher, time.Minute)
	cache.Set("key", "initial")

	// Force the item to become stale.
	cache.Lock()
	if item, exists := cache.items["key"]; exists {
		item.expires = time.Now().Add(-1 * time.Second)
	}
	cache.Unlock()

	// First Get returns "initial" and triggers an async refresh.
	val, ok := cache.Get("key")
	if !ok {
		t.Fatalf("Expected value for key 'key'")
	}
	if val != "initial" {
		t.Fatalf("Expected 'initial', got '%s'", val)
	}

	// Poll for the async refresh to update the value.
	deadline := time.Now().Add(500 * time.Millisecond)
	var newVal string
	for {
		newVal, _ = cache.Get("key")
		if newVal != "initial" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Async refresh did not complete in time; value still 'initial'")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !strings.HasPrefix(newVal, "fetched-") {
		t.Errorf("Expected new value to have prefix 'fetched-', but got '%s'", newVal)
	}
}

// TestExpiredItemFetch simulates an expired item so that a fetch occurs.
func TestExpiredItemFetch(t *testing.T) {
	var counter atomic.Int32
	fetcher := func(key string) (string, bool) {
		return fmt.Sprintf("fetched-%d", counter.Add(1)), true
	}

	cache := New(fetcher, time.Minute)
	cache.Set("key", "initial")

	// Force the item into an expired state.
	cache.Lock()
	if item, exists := cache.items["key"]; exists {
		item.expires = time.Now().Add(-graceLimit).Add(-1 * time.Millisecond)
	}
	cache.Unlock()

	val, ok := cache.Get("key")
	if !ok {
		t.Fatalf("Expected value after fetch")
	}
	if val == "initial" {
		t.Errorf("Expected updated value, got 'initial'")
	}
}

// TestDelete verifies that Delete removes keys as expected.
func TestDelete(t *testing.T) {
	var counter atomic.Int32
	fetcher := func(key string) (string, bool) {
		newCount := counter.Add(1)
		return fmt.Sprintf("value %d", newCount), true
	}

	cache := New(fetcher, time.Minute)
	cache.Set("a", "A")
	cache.Set("b", "B")
	cache.Delete("a")

	cache.RLock()
	if _, exists := cache.items["a"]; exists {
		t.Errorf("Expected key 'a' to be deleted")
	}
	cache.RUnlock()

	val, ok := cache.Get("b")
	if !ok || val != "B" {
		t.Errorf("Expected key 'b' to have value 'B', got '%s'", val)
	}
}

// TestClear verifies that Clear removes all keys as expected.
func TestClear(t *testing.T) {
	var counter atomic.Int32
	fetcher := func(key string) (string, bool) {
		newCount := counter.Add(1)
		return fmt.Sprintf("value %d", newCount), true
	}

	cache := New(fetcher, time.Minute)
	cache.Set("a", "A")
	cache.Set("b", "B")
	cache.Set("c", "C")

	cache.Clear()

	cache.RLock()
	if len(cache.items) != 0 {
		t.Errorf("Expected cache to be empty after Clear but found %d items", len(cache.items))
	}
	cache.RUnlock()
}

// TestReaper manually triggers the reaper to ensure expired keys are removed.
func TestReaper(t *testing.T) {
	fetcher := func(key string) (string, bool) {
		return "fetched", true
	}

	cache := New(fetcher, time.Minute)
	cache.Set("live", "Live")
	cache.Set("expired", "Expired")

	// Force item to expire.
	cache.Lock()
	if item, exists := cache.items["expired"]; exists {
		item.expires = time.Now().Add(-graceLimit).Add(-1 * time.Millisecond)
	}
	cache.Unlock()

	cache.reap()

	cache.RLock()
	if _, exists := cache.items["expired"]; exists {
		t.Errorf("Expected 'expired' key to be removed by the reaper")
	}
	if _, exists := cache.items["live"]; !exists {
		t.Errorf("Expected 'live' key to remain after reaping")
	}
	cache.RUnlock()
}

func TestStartReaperTickerBranch(t *testing.T) {
	// A simple fetcher that returns a dummy value.
	fetcher := func(key string) (string, bool) { return "dummy", true }
	// Create the cache.
	cache := &Cache[string, string]{
		fetcher:   fetcher,
		ttl:       time.Minute,
		items:     make(map[string]*Item[string]),
		fetchings: make(map[string]time.Time),
		done:      make(chan struct{}),
	}
	// Prepopulate the cache if desired. (Not required to test the ticker branch.)

	// Create a ticker that fires very frequently.
	fastTicker := time.NewTicker(10 * time.Millisecond)
	// Run the reaper with the fast ticker in a separate goroutine.
	doneCh := make(chan struct{})
	go func() {
		cache.startReaper(fastTicker)
		close(doneCh)
	}()

	// Wait a short moment to ensure at least one tick occurs.
	time.Sleep(50 * time.Millisecond)

	// Now signal the reaper to stop.
	cache.stop()
	// Wait for the reaper goroutine to exit.
	select {
	case <-doneCh:
		// Success.
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Reaper did not stop in time")
	}
}

func TestReapRemovesExpiredItems(t *testing.T) {
	// A simple fetcher that always returns a dummy value.
	fetcher := func(key string) (string, bool) { return "dummy", true }
	// Create the cache with a TTL of 1 minute.
	cache := New(fetcher, time.Minute)
	// Ensure that any background goroutines are stopped at the end of the test.
	defer cache.stop()

	// Insert two keys: one that should remain live and one that will be expired.
	cache.Set("live", "liveValue")
	cache.Set("expired", "expiredValue")

	// Force the "expired" key to be in an expired state.
	cache.Lock()
	if item, exists := cache.items["expired"]; exists {
		// Set its expiration so far in the past that its state becomes 'expired'.
		item.expires = time.Now().Add(-graceLimit).Add(-1 * time.Millisecond)
		// Note: the live key remains unmodified.
	}
	cache.Unlock()

	// Call reap() manually (the same function invoked by the ticker) to remove expired items.
	cache.reap()

	// Verify that the expired key was removed and the live key remains.
	cache.RLock()
	if _, exists := cache.items["expired"]; exists {
		t.Errorf("expected expired key 'expired' to be removed, but it still exists")
	}
	if _, exists := cache.items["live"]; !exists {
		t.Errorf("expected live key 'live' to remain, but it was removed")
	}
	cache.RUnlock()
}

func TestGetForMissingKeyInt(t *testing.T) {
	var counter atomic.Int32
	fetcher := func(key int) (string, bool) {
		newCount := counter.Add(1)
		return fmt.Sprintf("value %d", newCount), true
	}

	cache := New(fetcher, time.Minute)

	val, ok := cache.Get(42)
	if !ok {
		t.Fatalf("Expected value, got none")
	}
	if val != "value 1" {
		t.Errorf("Expected 'value 1', got '%s'", val)
	}
}

func TestSetInt(t *testing.T) {
	fetcher := func(key int) (string, bool) {
		return "fetched", true
	}
	cache := New(fetcher, time.Minute)

	cache.Set(100, "set")

	val, ok := cache.Get(100)
	if !ok {
		t.Fatalf("Expected value for key 100")
	}
	if val != "set" {
		t.Errorf("Expected 'set', got '%s'", val)
	}

	cache.Replace(100, "replaced")
	val, ok = cache.Get(100)
	if !ok {
		t.Fatalf("Expected value for key 100 after Replace")
	}
	if val != "replaced" {
		t.Errorf("Expected 'replaced', got '%s'", val)
	}
}

func TestStaleItemRefreshInt(t *testing.T) {
	var counter atomic.Int32
	fetcher := func(key int) (string, bool) {
		return fmt.Sprintf("fetched-%d", counter.Add(1)), true
	}

	cache := New(fetcher, time.Minute)
	cache.Set(7, "initial")

	// Force the item to become stale.
	cache.Lock()
	if item, exists := cache.items[7]; exists {
		item.expires = time.Now().Add(-1 * time.Second)
	}
	cache.Unlock()

	// First Get returns "initial" and triggers an async refresh.
	val, ok := cache.Get(7)
	if !ok {
		t.Fatalf("Expected value for key 7")
	}
	if val != "initial" {
		t.Fatalf("Expected 'initial', got '%s'", val)
	}

	// Poll for the async refresh to update the value.
	deadline := time.Now().Add(500 * time.Millisecond)
	var newVal string
	for {
		newVal, _ = cache.Get(7)
		if newVal != "initial" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Async refresh did not complete in time; value still 'initial'")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !strings.HasPrefix(newVal, "fetched-") {
		t.Errorf("Expected new value to have prefix 'fetched-', got '%s'", newVal)
	}
}

func TestExpiredItemFetchInt(t *testing.T) {
	var counter atomic.Int32
	fetcher := func(key int) (string, bool) {
		return fmt.Sprintf("fetched-%d", counter.Add(1)), true
	}

	cache := New(fetcher, time.Minute)
	cache.Set(55, "initial")

	cache.Lock()
	if item, exists := cache.items[55]; exists {
		item.expires = time.Now().Add(-graceLimit).Add(-1 * time.Millisecond)
	}
	cache.Unlock()

	val, ok := cache.Get(55)
	if !ok {
		t.Fatalf("Expected value after synchronous fetch")
	}
	if val == "initial" {
		t.Errorf("Expected updated value on expired item fetch, got 'initial'")
	}
}

func TestDeleteInt(t *testing.T) {
	var counter atomic.Int32
	fetcher := func(key int) (string, bool) {
		newCount := counter.Add(1)
		return fmt.Sprintf("value %d", newCount), true
	}

	cache := New(fetcher, time.Minute)
	cache.Set(1, "A")
	cache.Set(2, "B")

	cache.Delete(1)

	cache.RLock()
	if _, exists := cache.items[1]; exists {
		t.Errorf("Expected key 1 to be deleted")
	}
	cache.RUnlock()

	val, ok := cache.Get(2)
	if !ok || val != "B" {
		t.Errorf("Expected key 2 to have value 'B', got '%s'", val)
	}
}

func TestClearInt(t *testing.T) {
	var counter atomic.Int32
	fetcher := func(key int) (string, bool) {
		newCount := counter.Add(1)
		return fmt.Sprintf("value %d", newCount), true
	}

	cache := New(fetcher, time.Minute)
	cache.Set(1, "A")
	cache.Set(2, "B")
	cache.Set(3, "C")

	cache.Clear()

	cache.RLock()
	if len(cache.items) != 0 {
		t.Errorf("Expected cache to be empty after Clear, found %d items", len(cache.items))
	}
	cache.RUnlock()
}

func TestReaperInt(t *testing.T) {
	fetcher := func(key int) (string, bool) {
		return "fetched", true
	}

	cache := New(fetcher, time.Minute)
	cache.Set(10, "live")
	cache.Set(20, "expired")

	cache.Lock()
	if item, exists := cache.items[20]; exists {
		item.expires = time.Now().Add(-graceLimit).Add(-1 * time.Millisecond)
	}
	cache.Unlock()

	cache.reap()

	cache.RLock()
	if _, exists := cache.items[20]; exists {
		t.Errorf("Expected key 20 to be removed by the reaper")
	}
	if _, exists := cache.items[10]; !exists {
		t.Errorf("Expected key 10 to remain after reaping")
	}
	cache.RUnlock()
}

// TestStopChannel checks that calling Stop() closes the internal done channel.
func TestStopChannel(t *testing.T) {
	fetcher := func(key string) (string, bool) {
		return "value", true
	}

	cache := New(fetcher, time.Minute)
	// Call Stop to close the channel.
	cache.stop()

	// A non-blocking read on a closed channel returns immediately with ok==false.
	select {
	case _, ok := <-cache.done:
		if ok {
			t.Error("Expected done channel to be closed, but it is still open")
		}
	default:
		t.Error("Expected done channel to be closed, but no value was available")
	}
}

// TestFetcherFailureFull exercises the branch in fetch when the fetcher returns false.
func TestFetcherFailureFull(t *testing.T) {
	// A fetcher that always fails.
	fetcher := func(key string) (string, bool) {
		return "", false
	}
	cache := New(fetcher, time.Minute)

	// Calling Get on a missing key will call fetch.
	val, ok := cache.Get("nonexistent")
	if ok {
		t.Errorf("Expected fetch failure (ok==false), but got value: %q", val)
	}
	// For a string type, the zero value is "".
	if val != "" {
		t.Errorf("Expected zero value (empty string), got %q", val)
	}
}

// TestAsyncFetchNormal forces the normal path of asyncFetch.
func TestAsyncFetchNormal(t *testing.T) {
	var callCount atomic.Int32
	fetcher := func(key string) (string, bool) {
		callCount.Add(1)
		return "fetched", true
	}

	cache := New(fetcher, time.Minute)
	cache.Delete("testkey")
	cache.asyncFetch("testkey")

	// Poll until the fetcher is called (callCount becomes nonzero).
	deadline := time.Now().Add(100 * time.Millisecond)
	for callCount.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("Expected fetcher to be called by asyncFetch but it was not")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Also poll until the key appears in the cache with the expected value.
	deadline = time.Now().Add(100 * time.Millisecond)
	var val string
	var ok bool
	for {
		val, ok = cache.Get("testkey")
		if ok && val == "fetched" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Expected key 'testkey' to have value 'fetched' but it did not appear in time")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if callCount.Load() != 1 {
		t.Errorf("Expected fetcher to be called once, but was called %d times", callCount.Load())
	}
}

// TestAsyncFetchDuplicateCall ensures that if a fetch is in progress, a duplicate
// call to asyncFetch for the same key returns immediately.
func TestAsyncFetchDuplicateCall(t *testing.T) {
	var callCount atomic.Int32
	fetcher := func(key string) (string, bool) {
		callCount.Add(1)
		// Simulate long fetch.
		time.Sleep(200 * time.Millisecond)
		return "fetched", true
	}

	cache := New(fetcher, time.Minute)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		cache.asyncFetch("dup")
	}()

	// Poll until the first fetcher call is registered.
	deadline := time.Now().Add(50 * time.Millisecond)
	for callCount.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("Expected fetcher to be called by the first asyncFetch")
		}
		time.Sleep(1 * time.Millisecond)
	}

	// Now call asyncFetch again and measure how long it takes.
	start := time.Now()
	cache.asyncFetch("dup")
	duration := time.Since(start)
	if duration > 10*time.Millisecond {
		t.Errorf("Expected asyncFetch to return immediately when fetch is in progress, but took %s", duration)
	}

	wg.Wait()
	if callCount.Load() != 1 {
		t.Errorf("Expected fetcher to be called once, got %d calls", callCount.Load())
	}
}

// TestReapNoExpired tests that reap does not remove keys when none are expired.
func TestReapNoExpired(t *testing.T) {
	fetcher := func(key string) (string, bool) { return "value", true }
	cache := New(fetcher, time.Minute)

	for i := 0; i < 10; i++ {
		cache.Set(fmt.Sprintf("key-%d", i), "value")
	}

	cache.reap()

	cache.RLock()
	if len(cache.items) != 10 {
		t.Errorf("Expected 10 keys to remain, got %d", len(cache.items))
	}
	cache.RUnlock()
}

// TestReapPartialExpired tests that reap removes only expired keys.
func TestReapPartialExpired(t *testing.T) {
	fetcher := func(key string) (string, bool) { return "value", true }
	cache := New(fetcher, time.Minute)

	for i := 0; i < 10; i++ {
		cache.Set(fmt.Sprintf("key-%d", i), "value")
	}

	cache.Lock()
	for i := 0; i < 5; i++ {
		if item, exists := cache.items[fmt.Sprintf("key-%d", i)]; exists {
			item.expires = time.Now().Add(-2 * time.Minute)
		}
	}
	cache.Unlock()

	cache.reap()

	cache.RLock()
	if len(cache.items) != 5 {
		t.Errorf("Expected 5 keys to remain after reaping expired keys, got %d", len(cache.items))
	}
	cache.RUnlock()
}

// TestReapLimit verifies that the reap method stops processing after reaperLimit items.
func TestReapLimit(t *testing.T) {
	const totalExtra = 10
	totalKeys := reaperLimit + totalExtra
	fetcher := func(key string) (string, bool) { return "value", true }

	cache := New(fetcher, time.Minute)

	for i := 0; i < totalKeys; i++ {
		cache.Set(fmt.Sprintf("key-%d", i), "value")
	}

	cache.Lock()
	for k, item := range cache.items {
		item.expires = time.Now().Add(-time.Minute)
		cache.items[k] = item
	}
	cache.Unlock()

	cache.reap()

	cache.RLock()
	remaining := len(cache.items)
	cache.RUnlock()

	expectedRemaining := totalKeys - reaperLimit
	if remaining != expectedRemaining {
		t.Errorf("Expected %d keys remaining after reap, got %d", expectedRemaining, remaining)
	}
}

func BenchmarkCacheGet(b *testing.B) {
	b.StopTimer()
	// Benchmark only the Get method.
	fetcher := func(key string) (string, bool) { return "value", true }
	cache := New(fetcher, time.Minute)
	// Prepopulate the cache with the key to be read.
	cache.Set("benchmark", "value")

	b.StartTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cache.Get("benchmark")
	}
	b.StopTimer()
}

func BenchmarkCacheSet(b *testing.B) {
	b.StopTimer()
	// Benchmark only the Set method.
	fetcher := func(key string) (string, bool) { return "value", true }
	cache := New(fetcher, time.Minute)
	// Precompute keys outside the timed section.
	keys := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = fmt.Sprintf("key-%d", i)
	}

	b.StartTimer()
	for i := 0; i < b.N; i++ {
		cache.Set(keys[i], "value")
	}
	b.StopTimer()
}

func BenchmarkCacheReplace(b *testing.B) {
	b.StopTimer()
	// Benchmark only the Replace method.
	fetcher := func(key string) (string, bool) { return "value", true }
	cache := New(fetcher, time.Minute)
	// Prepopulate the cache with a key.
	cache.Set("replaceKey", "initial")
	// Precompute replacement values outside the timed section.
	newValues := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		newValues[i] = fmt.Sprintf("value-%d", i)
	}

	b.StartTimer()
	for i := 0; i < b.N; i++ {
		cache.Replace("replaceKey", newValues[i])
	}
	b.StopTimer()
}

func BenchmarkCacheDelete(b *testing.B) {
	b.StopTimer() // Stop timer for all setup work.
	fetcher := func(key string) (string, bool) { return "value", true }
	const numKeys = 1000
	// Precompute the keys to be deleted.
	keys := make([]string, numKeys)
	for j := 0; j < numKeys; j++ {
		keys[j] = fmt.Sprintf("key-%d", j)
	}

	for i := 0; i < b.N; i++ {
		cache := New(fetcher, time.Minute)
		// Prepopulate the cache.
		for _, key := range keys {
			cache.Set(key, "value")
		}
		b.StartTimer() // Start timer for the delete operation.
		for _, key := range keys {
			cache.Delete(key)
		}
		b.StopTimer()
	}
}

// FuzzCache sets and then retrieves a key to verify that the stored value is returned.
func FuzzCache(f *testing.F) {
	// Seed the fuzzer with initial values.
	f.Add("key1", "value1")
	f.Add("key2", "value2")

	fetcher := func(key string) (string, bool) { return key, true }
	cache := New(fetcher, time.Minute)

	f.Fuzz(func(t *testing.T, key, value string) {
		cache.Set(key, value)
		got, ok := cache.Get(key)
		if !ok {
			t.Errorf("Expected key %q to exist", key)
		}
		if got != value {
			t.Errorf("For key %q, expected %q, got %q", key, value, got)
		}
	})
}

func TestProfile(t *testing.T) {
	// --- Setup phase (not profiled) ---
	fetcher := func(key string) (string, bool) { return "fetched", true }
	cache := New(fetcher, time.Minute)

	// Initially, set a benchmark key.
	cache.Set("benchmark", "value")

	const iterations = 100000

	// --- Begin profiling just before the workload ---
	cpuFile, err := os.Create("cpu.out")
	if err != nil {
		t.Fatalf("Could not create CPU profile: %v", err)
	}
	// Start CPU profiling right before the loop.
	if err = pprof.StartCPUProfile(cpuFile); err != nil {
		t.Fatalf("Could not start CPU profile: %v", err)
	}

	// --- Workload: exercise only gust code ---
	for i := 0; i < iterations; i++ {
		// Exercise Get: repeatedly retrieve a known key.
		_, _ = cache.Get("benchmark")

		// Exercise Set: add a new key.
		key := fmt.Sprintf("key-%d", i)
		cache.Set(key, fmt.Sprintf("value-%d", i))

		// Exercise Replace: update the key we just set.
		cache.Replace(key, fmt.Sprintf("newvalue-%d", i))

		// Exercise Delete: every 10 iterations, remove the key.
		if i%10 == 0 {
			cache.Delete(key)
		}

		// Exercise Clear: every 10000 iterations, clear the entire cache.
		if i%10000 == 0 {
			cache.Clear()
		}
	}

	// --- Stop profiling immediately after the workload ---
	pprof.StopCPUProfile()
	cpuFile.Close()

	// Write a memory profile.
	memFile, err := os.Create("mem.out")
	if err != nil {
		t.Fatalf("Could not create memory profile: %v", err)
	}
	defer memFile.Close()
	// Run GC to update heap statistics.
	runtime.GC()
	if err := pprof.WriteHeapProfile(memFile); err != nil {
		t.Fatalf("Could not write memory profile: %v", err)
	}
}
