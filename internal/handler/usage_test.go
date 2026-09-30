package handler

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUsageCacheMeasuresOnceAndServesStaleRatherThanBlocking(t *testing.T) {
	dir := t.TempDir()
	siteDir := filepath.Join(dir, "by-id", "user", "site", "current")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(siteDir, "index.html"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}

	var cache usageCache
	first, takenFirst, err := cache.get(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.SiteBytes != 4096 || first.Sites != 1 {
		t.Fatalf("first reading: %d bytes across %d sites", first.SiteBytes, first.Sites)
	}

	// A second site lands, but within the cache window the answer must not
	// change — this is the whole point of caching a filesystem walk.
	second := filepath.Join(dir, "by-id", "user", "other", "current")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "index.html"), make([]byte, 8192), 0o644); err != nil {
		t.Fatal(err)
	}
	cached, takenCached, err := cache.get(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cached.SiteBytes != 4096 {
		t.Errorf("cached reading walked again: %d bytes", cached.SiteBytes)
	}
	if !takenCached.Equal(takenFirst) {
		t.Error("a cached reading reported a new measurement time")
	}

	// Once stale, the caller is served the old value immediately and a refresh
	// runs behind it. The admin page must never wait on a walk.
	cache.mu.Lock()
	cache.taken = time.Now().Add(-2 * usageMaxAge)
	cache.mu.Unlock()

	stale, _, err := cache.get(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if stale.SiteBytes != 4096 {
		t.Errorf("stale read blocked for a fresh walk: %d bytes", stale.SiteBytes)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fresh, _, _ := cache.get(dir, 0); fresh.SiteBytes == 12288 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("the background refresh never picked up the second site")
}

// A reading taken before the site files changed (a deploy, a pruned version)
// or older than usageStaleAfter is never served: the caller gets a new walk.
func TestUsageCacheNeverServesAReadingFromBeforeAChange(t *testing.T) {
	dir := t.TempDir()
	write := func(site string, size int) {
		t.Helper()
		d := filepath.Join(dir, "by-id", "user", site, "current")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "index.html"), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a", 4096)
	var cache usageCache
	if u, _, _ := cache.get(dir, 7); u.SiteBytes != 4096 {
		t.Fatalf("first reading: %d", u.SiteBytes)
	}
	write("b", 8192)
	if u, _, _ := cache.get(dir, 7); u.SiteBytes != 4096 {
		t.Fatalf("no change counted, fresh reading: %d", u.SiteBytes)
	}
	if u, _, _ := cache.get(dir, 8); u.SiteBytes != 12288 {
		t.Fatalf("after a change: %d, want a new walk (12288)", u.SiteBytes)
	}
	write("c", 4096)
	cache.mu.Lock()
	cache.taken = time.Now().Add(-2 * usageStaleAfter)
	cache.mu.Unlock()
	if u, _, _ := cache.get(dir, 8); u.SiteBytes != 16384 {
		t.Fatalf("a reading past usageStaleAfter was served: %d", u.SiteBytes)
	}
}

func TestUsageCacheUnderConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "by-id"), 0o755); err != nil {
		t.Fatal(err)
	}
	var cache usageCache
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := cache.get(dir, 0); err != nil {
				t.Errorf("concurrent get: %v", err)
			}
		}()
	}
	wg.Wait()
}

// Callers that must wait for a new walk share one walk.
func TestUsageCacheBlockingWalkIsShared(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "by-id"), 0o755); err != nil {
		t.Fatal(err)
	}
	var cache usageCache
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := cache.get(dir, 3); err != nil {
				t.Errorf("get: %v", err)
			}
		}()
	}
	wg.Wait()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.walk != nil || !cache.hasValue || cache.gen != 3 {
		t.Fatalf("after the shared walk: walk=%v hasValue=%v gen=%d", cache.walk, cache.hasValue, cache.gen)
	}
}
