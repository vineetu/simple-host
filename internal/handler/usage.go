package handler

import (
	"net/http"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/buildinfo"
	"github.com/vsriram/simple-host/internal/capacity"
	"github.com/vsriram/simple-host/internal/config"
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
// admin page. So a recent reading is served while a new one is taken behind
// the request, and a second walk is never started while one is running.
//
// A reading taken before the site files last changed (a deploy, a pruned
// version, a delete: DiskStorage.Changes) or older than usageStaleAfter is not
// served at all: the caller waits for a new walk. Serving any stale reading
// once showed a site at 1.1 GB for hours after its old versions were pruned.
type usageCache struct {
	mu       sync.Mutex
	value    capacity.Usage
	taken    time.Time
	gen      int64 // the DiskStorage change count the reading was taken at
	running  bool
	hasValue bool
	// walk is closed when the blocking walk in progress finishes (nil when
	// none is): callers that must wait share that one walk.
	walk    chan struct{}
	walkGen int64
	walkErr error
}

const (
	usageMaxAge     = 2 * time.Minute
	usageStaleAfter = 10 * time.Minute
)

// usageLargestSites is how many of the biggest sites to name. Enough to find the
// cause of a full disk, not so many that the admin screen becomes a file browser.
const usageLargestSites = 10

// get returns a reading of dataDir no older than the last change to it (gen,
// the DiskStorage change count now).
func (c *usageCache) get(dataDir string, gen int64) (capacity.Usage, time.Time, error) {
	c.mu.Lock()
	current := c.hasValue && c.gen == gen
	age := time.Since(c.taken)
	if current && age < usageMaxAge {
		value, taken := c.value, c.taken
		c.mu.Unlock()
		return value, taken, nil
	}
	if current && age < usageStaleAfter {
		// A few minutes old and nothing changed since: serve it now and
		// refresh behind the request, for what changed outside this process.
		value, taken := c.value, c.taken
		if !c.running {
			c.running = true
			go c.refresh(dataDir, gen)
		}
		c.mu.Unlock()
		return value, taken, nil
	}
	// Nothing measured yet, or the files changed since: this caller waits,
	// on the walk already running when it started at this change or later.
	if c.walk != nil && c.walkGen >= gen {
		done := c.walk
		c.mu.Unlock()
		<-done
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.hasValue {
			return capacity.Usage{}, time.Time{}, c.walkErr
		}
		return c.value, c.taken, nil
	}
	done := make(chan struct{})
	c.walk, c.walkGen = done, gen
	c.mu.Unlock()

	usage, err := capacity.Measure(dataDir, usageLargestSites)
	taken := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.walkErr = err
	if c.walk == done {
		c.walk = nil
	}
	close(done)
	if err != nil {
		return capacity.Usage{}, time.Time{}, err
	}
	if !c.hasValue || gen >= c.gen {
		c.value, c.taken, c.gen, c.hasValue = usage, taken, gen, true
	}
	return usage, taken, nil
}

func (c *usageCache) refresh(dataDir string, gen int64) {
	usage, err := capacity.Measure(dataDir, usageLargestSites)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	if err == nil && gen >= c.gen {
		c.value, c.taken, c.gen, c.hasValue = usage, time.Now(), gen, true
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
	usage, measuredAt, err := h.usage.get(h.disk.DataDir(), h.disk.Changes())
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "could not read this server's disk"})
		return
	}
	var accounts int
	if err := h.database.QueryRowContext(r.Context(),
		`SELECT count(*) FROM users WHERE NOT is_admin`).Scan(&accounts); err != nil {
		accounts = -1 // reported as unknown rather than as zero
	}
	out := map[string]any{
		"disk_bytes":      usage.DiskBytes,
		"disk_free_bytes": usage.DiskFreeBytes,
		"disk_used_bytes": usage.DiskUsedBytes,
		"disk_used_pct":   usage.DiskUsedPct,
		"site_bytes":      usage.SiteBytes,
		"sites":           usage.Sites,
		"accounts":        accounts,
		"largest":         usage.Largest,
		"site_limit_mb":   SiteLimit() >> 20,
		// MAX_ARCHIVE_MB_OVERRIDES: handle -> MB, for the accounts that
		// have their own; null when none do.
		"site_limit_overrides": config.Active().ArchiveOverrides(),
		// Settings and build in force, so "which release is this box on" and
		// "how many deploys does it keep" are answered without a shell.
		"keep_versions": KeepVersions(),
		"version":       buildinfo.Version,
		"commit":        buildinfo.Commit,
		"status":        usage.Status,
		"message":       usage.Message,
		// When the walk behind these figures ran. A served-stale reading is
		// better than a page that hangs, but only if the page can say so.
		"measured_at": measuredAt.UTC().Format(time.RFC3339),
	}
	// ?sizes=1 adds every site's footprint (the admin page's Sites table),
	// from the same cached walk.
	if r.URL.Query().Get("sizes") == "1" {
		sizes := usage.All
		if sizes == nil {
			sizes = []capacity.SiteUsage{}
		}
		out["site_sizes"] = sizes
	}
	writeJSON(w, 200, out)
}
