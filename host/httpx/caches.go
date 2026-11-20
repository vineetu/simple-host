package httpx

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/workers"
)

// Caches is the one owner of a transport's periodic in-memory eviction loops
// (rate limiter buckets among them). Registration finishes before Start,
// alongside route wiring.
type Caches struct {
	startOnce sync.Once
	once      sync.Once
	runner    *workers.Runner
	jobs      []cacheCleanup
	tracker   *workers.Tracker
	name      string
}
type cacheCleanup struct {
	every time.Duration
	evict func() int
}

func (c *Caches) Add(name string, every time.Duration, evict func() int) {
	c.name = name[:strings.LastIndex(name, ".")] + ".caches"
	c.jobs = append(c.jobs, cacheCleanup{every, evict})
}

// Rate evicts rl's idle buckets every so often.
func (c *Caches) Rate(name string, rl *RateLimiter, every, idle time.Duration) {
	c.Add(name, every, func() int { return rl.EvictIdle(idle) })
}
func (c *Caches) init() {
	c.once.Do(func() {
		c.tracker = workers.NewTracker(c.name)
		c.runner = workers.NewRunner(c.tracker)
	})
}
func (c *Caches) Start(ctx context.Context) {
	c.startOnce.Do(func() {
		c.init()
		for _, j := range c.jobs {
			c.runner.Go(ctx, func(ctx context.Context) {
				workers.Every(ctx, j.every, j.every, func(context.Context) {
					c.tracker.Attempt()
					if j.evict() > 0 {
						c.tracker.Progress()
					}
				})
			})
		}
	})
}
func (c *Caches) Stop()                    { c.init(); c.runner.Stop() }
func (c *Caches) Wait()                    { c.init(); c.runner.Wait() }
func (c *Caches) Status() []workers.Status { c.init(); return c.runner.Status() }
