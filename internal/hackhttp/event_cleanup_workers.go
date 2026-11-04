package hackhttp

import (
	"context"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/workers"
)

type eventCleanupOwner struct {
	*workers.Runner
	tracker *workers.Tracker
	once    sync.Once
	h       *HackHandler
}

func (h *HackHandler) EventCleanupOwner() workers.Owner {
	h.eventCleanupOnce.Do(func() {
		t := workers.NewTracker("hack.event_cleanup")
		h.eventCleanupWorker = &eventCleanupOwner{Runner: workers.NewRunner(t), tracker: t, h: h}
	})
	return h.eventCleanupWorker
}
func (o *eventCleanupOwner) Start(ctx context.Context) {
	o.once.Do(func() {
		o.Go(ctx, func(ctx context.Context) {
			timer := time.NewTimer(eventCleanupDelay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			run := func() { o.tracker.Attempt(); o.h.runEventCleanupTracked(ctx, time.Now(), o.tracker) }
			run()
			ticker := time.NewTicker(eventCleanupEvery)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					run()
				}
			}
		})
	})
}

type siteSweepReport struct{ ctx context.Context }

func (r siteSweepReport) Progress()                 { sites.ReportWorkerProgress(r.ctx) }
func (r siteSweepReport) Fail(op string, err error) { sites.ReportWorkerFailure(r.ctx, op, err) }
