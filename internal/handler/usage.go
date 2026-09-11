package handler

import (
	"net/http"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/capacity"
	"github.com/vsriram/simple-host/internal/tarball"
)

// SetSiteLimit applies the per-site cap, in bytes, to every place that enforces
// one: the upload body cap and the extractor's uncompressed caps.
//
// Both, not either. The upload cap alone bounds the compressed bytes, and a
// well-compressed archive is an order of magnitude smaller than what it expands
// to — the extractor is where a 2 MB upload becoming 200 MB on disk is stopped.
// Call once at startup, before serving.
func SetSiteLimit(bytes int64) {
	if bytes <= 0 {
		return
	}
	maxSiteArchiveSize = bytes
	tarball.SetSiteLimit(bytes)
}

// SiteLimit reports the per-site cap currently in force, in bytes.
func SiteLimit() int64 { return maxSiteArchiveSize }

// usageCache holds the last measurement. Measuring means walking every file
// under the data directory, which is cheap on a small instance and not cheap on
// a full one — exactly the instance most likely to have someone reloading the
// admin page. So a request never waits for a walk it did not have to start, and
// never triggers a second one while the first is running.
type usageCache struct {
	mu       sync.Mutex
	value    capacity.Usage
	taken    time.Time
	running  bool
	hasValue bool
}

const usageMaxAge = 2 * time.Minute

// usageLargestSites is how many of the biggest sites to name. Enough to find the
// cause of a full disk, not so many that the admin screen becomes a file browser.
const usageLargestSites = 5

func (c *usageCache) get(dataDir string) (capacity.Usage, time.Time, error) {
	c.mu.Lock()
	fresh := c.hasValue && time.Since(c.taken) < usageMaxAge
	if fresh {
		value, taken := c.value, c.taken
		c.mu.Unlock()
		return value, taken, nil
	}
	if c.hasValue {
		// Stale but present: serve it now and refresh behind the request. A
		// two-minute-old disk figure is a good answer; a page that hangs while
		// the disk is walked is not.
		value, taken := c.value, c.taken
		if !c.running {
			c.running = true
			go c.refresh(dataDir)
		}
		c.mu.Unlock()
		return value, taken, nil
	}
	c.mu.Unlock()

	// Nothing measured yet, so this caller does the work.
	usage, err := capacity.Measure(dataDir, usageLargestSites)
	if err != nil {
		return capacity.Usage{}, time.Time{}, err
	}
	taken := time.Now()
	c.mu.Lock()
	c.value, c.taken, c.hasValue = usage, taken, true
	c.mu.Unlock()
	return usage, taken, nil
}

func (c *usageCache) refresh(dataDir string) {
	usage, err := capacity.Measure(dataDir, usageLargestSites)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	if err == nil {
		c.value, c.taken, c.hasValue = usage, time.Now(), true
	}
}

// adminUsage reports what this instance is using and what it has left.
//
// There is deliberately no projection of how many people or sites will fit. The
// sites on the instance this was built for have a median size of 25 KB against a
// 100 MB cap, so any figure derived from the cap is wrong by three orders of
// magnitude. What is true is what is on the disk right now.
//
// GET /v1/admin/usage
func (h *SiteHandler) adminUsage(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	usage, measuredAt, err := h.usage.get(h.disk.DataDir())
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "could not read this server's disk"})
		return
	}
	var accounts int
	if err := h.database.QueryRowContext(r.Context(),
		`SELECT count(*) FROM users WHERE NOT is_admin`).Scan(&accounts); err != nil {
		accounts = -1 // reported as unknown rather than as zero
	}
	writeJSON(w, 200, map[string]any{
		"disk_bytes":      usage.DiskBytes,
		"disk_free_bytes": usage.DiskFreeBytes,
		"disk_used_bytes": usage.DiskUsedBytes,
		"disk_used_pct":   usage.DiskUsedPct,
		"site_bytes":      usage.SiteBytes,
		"sites":           usage.Sites,
		"accounts":        accounts,
		"largest":         usage.Largest,
		"site_limit_mb":   SiteLimit() >> 20,
		"status":          usage.Status,
		"message":         usage.Message,
		// When the walk behind these figures ran. A served-stale reading is
		// better than a page that hangs, but only if the page can say so.
		"measured_at": measuredAt.UTC().Format(time.RFC3339),
	})
}
