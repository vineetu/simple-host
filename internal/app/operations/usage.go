package operations

import (
	"github.com/vsriram/simple-host/internal/capacity"
	"sync"
	"time"
)

// SiteUsage is a measured site footprint returned to operator transports.
type SiteUsage = capacity.SiteUsage

// UsageCache holds the last measurement. Measuring means walking every file
// under the data directory, which is cheap on a small instance and not cheap on
// a full one — exactly the instance most likely to have someone reloading the
// admin page. So a recent reading is served while a new one is taken behind
// the request, and a second walk is never started while one is running.
//
// A reading taken before the site files last changed (a deploy, a pruned
// version, a delete: DiskStorage.Changes) or older than usageStaleAfter is not
// served at all: the caller waits for a new walk. Serving any stale reading
// once showed a site at 1.1 GB for hours after its old versions were pruned.
type UsageCache struct {
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
func (c *UsageCache) Get(dataDir string, gen int64) (capacity.Usage, time.Time, error) {
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

func (c *UsageCache) refresh(dataDir string, gen int64) {
	usage, err := capacity.Measure(dataDir, usageLargestSites)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	if err == nil && gen >= c.gen {
		c.value, c.taken, c.gen, c.hasValue = usage, time.Now(), gen, true
	}
}
