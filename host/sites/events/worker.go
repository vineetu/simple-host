package events

import (
	"context"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/workers"
)

// sweep runs the expiry sweep with the delay counted after each pass.
type sweep struct {
	*workers.Runner
	tracker *workers.Tracker
	once    sync.Once
	service Service
}

func newSweep(s Service) *sweep {
	t := workers.NewTracker("sites.event_dns_sweep")
	return &sweep{Runner: workers.NewRunner(t), tracker: t, service: s}
}

// Start sweeps hourly until ctx ends.
func (o *sweep) Start(ctx context.Context) {
	const every = time.Hour
	o.once.Do(func() {
		o.Go(ctx, func(ctx context.Context) {
			timer := time.NewTimer(every)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
				if ctx.Err() != nil {
					return
				}
				o.tracker.Attempt()
				o.service.SweepEventDomains(sites.WithWorkerReport(ctx, o.tracker))
				timer.Reset(every)
			}
		})
	})
}
