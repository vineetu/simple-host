package hackhttp

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/workers"
)

type limiterJob struct {
	limiter *httpx.RateLimiter
	tracker *workers.Tracker
}
type limiterWorkers struct {
	runner *workers.Runner
	jobs   []limiterJob
}

func (h *HackHandler) addLimiter(name string, l *httpx.RateLimiter) {
	if h.limiterOwner == nil {
		h.limiterOwner = &limiterWorkers{}
	}
	h.limiterOwner.jobs = append(h.limiterOwner.jobs, limiterJob{l, workers.NewTracker(name)})
}
func (h *HackHandler) LimiterWorkers() workers.Owner {
	if h.limiterOwner == nil {
		h.limiterOwner = &limiterWorkers{}
	}
	o := h.limiterOwner
	if o.runner == nil {
		for i, l := range []*httpx.RateLimiter{h.codesIP, h.codesUser, h.namesUser, h.entryWrites, h.shotWrites, h.applyIP, h.applyUser} {
			h.addLimiter([]string{"http.hack.codes_ip", "http.hack.codes_user", "http.hack.names_user", "http.hack.entry_writes", "http.hack.shot_writes", "http.hack.apply_ip", "http.hack.apply_user"}[i], l)
		}
		var ts []*workers.Tracker
		for _, j := range o.jobs {
			ts = append(ts, j.tracker)
		}
		o.runner = workers.NewRunner(ts...)
	}
	return o
}

// StartCleanup is the compatibility entry point for the limiter owner.
func (h *HackHandler) StartCleanup(ctx context.Context) { h.LimiterWorkers().Start(ctx) }
func (o *limiterWorkers) Start(ctx context.Context) {
	for _, j := range o.jobs {
		o.runner.Go(ctx, func(ctx context.Context) {
			workers.Every(ctx, 10*time.Minute, 10*time.Minute, func(context.Context) {
				j.tracker.Attempt()
				if j.limiter.EvictIdle(30*time.Minute) > 0 {
					j.tracker.Progress()
				}
			})
		})
	}
}
func (o *limiterWorkers) Stop()                    { o.runner.Stop() }
func (o *limiterWorkers) Wait()                    { o.runner.Wait() }
func (o *limiterWorkers) Status() []workers.Status { return o.runner.Status() }
