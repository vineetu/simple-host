package handler

import (
	"context"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/workers"
)

// HTTPCaches owns the transport's periodic in-memory eviction loops.
// Registration finishes before Start, alongside route wiring.
type HTTPCaches struct {
	once   sync.Once
	runner *workers.Runner
	jobs   []cacheCleanup
}
type cacheCleanup struct {
	tracker *workers.Tracker
	every   time.Duration
	evict   func() int
}

func (c *HTTPCaches) add(name string, every time.Duration, evict func() int) {
	c.jobs = append(c.jobs, cacheCleanup{workers.NewTracker(name), every, evict})
}
func (c *HTTPCaches) rate(name string, rl *rateLimiter, every, idle time.Duration) {
	c.add(name, every, func() int { return rl.EvictIdle(idle) })
}
func (c *HTTPCaches) init() {
	c.once.Do(func() {
		var ts []*workers.Tracker
		for _, j := range c.jobs {
			ts = append(ts, j.tracker)
		}
		c.runner = workers.NewRunner(ts...)
	})
}
func (c *HTTPCaches) Start(ctx context.Context) {
	c.init()
	for _, j := range c.jobs {
		c.runner.Go(ctx, func(ctx context.Context) {
			workers.Every(ctx, j.every, j.every, func(context.Context) {
				j.tracker.Attempt()
				if j.evict() > 0 {
					j.tracker.Progress()
				}
			})
		})
	}
}
func (c *HTTPCaches) Stop()                           { c.init(); c.runner.Stop() }
func (c *HTTPCaches) Wait()                           { c.init(); c.runner.Wait() }
func (c *HTTPCaches) Status() []workers.Status        { c.init(); return c.runner.Status() }
func (h *SiteHandler) HTTPCaches() workers.Owner      { return &h.httpCaches }
func (h *UserHandler) HTTPCaches() workers.Owner      { return &h.httpCaches }
func (h *OAuthHandler) HTTPCaches() workers.Owner     { return &h.httpCaches }
func (h *ConnectorHandler) HTTPCaches() workers.Owner { return &h.httpCaches }
