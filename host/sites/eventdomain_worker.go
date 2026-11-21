package sites

import (
	"context"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/workers"
)

type eventDNSState struct {
	once  sync.Once
	owner *eventDNSSweep
}

// eventDNSSweep runs the sweep with the delay counted after each pass.
type eventDNSSweep struct {
	*workers.Runner
	tracker *workers.Tracker
	once    sync.Once
	service *Service
}

func (s *Service) eventDNSWorker() *eventDNSSweep {
	s.eventSweep.once.Do(func() {
		t := workers.NewTracker("sites.event_dns_sweep")
		s.eventSweep.owner = &eventDNSSweep{Runner: workers.NewRunner(t), tracker: t, service: s}
	})
	return s.eventSweep.owner
}
func (s *Service) EventDNSOwner() workers.Owner { return s.eventDNSWorker() }

// Start sweeps hourly until ctx ends.
func (o *eventDNSSweep) Start(ctx context.Context) {
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
				o.service.SweepEventDomains(WithWorkerReport(ctx, o.tracker))
				timer.Reset(every)
			}
		})
	})
}
