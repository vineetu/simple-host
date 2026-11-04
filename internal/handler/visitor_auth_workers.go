package handler

import (
	"context"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/workers"
)

type visitorAuthWorkers struct {
	startOnce sync.Once
	h         *OAuthHandler
	runner    *workers.Runner
	tracker   *workers.Tracker
}

// VisitorAuthWorkers owns expiry of visitor sign-in codes and sessions.
func (h *OAuthHandler) VisitorAuthWorkers() workers.Owner {
	h.visitorWorkerOnce.Do(func() {
		t := workers.NewTracker("accounts.visitor_auth")
		h.visitorWorker = &visitorAuthWorkers{h: h, runner: workers.NewRunner(t), tracker: t}
	})
	return h.visitorWorker
}
func (o *visitorAuthWorkers) Start(ctx context.Context) {
	o.startOnce.Do(func() {
		if o.h.database == nil {
			return
		}
		o.runner.Go(ctx, func(ctx context.Context) {
			timer := time.NewTimer(30 * time.Second)
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
				runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				n, err := db.SweepVisitorAuthCount(runCtx, o.h.database)
				cancel()
				if n > 0 {
					o.tracker.Progress()
				}
				if err != nil {
					o.tracker.Fail("sweep expired visitor authentication", err)
				}
				timer.Reset(time.Hour)
			}
		})
	})
}
func (o *visitorAuthWorkers) Stop()                    { o.runner.Stop() }
func (o *visitorAuthWorkers) Wait()                    { o.runner.Wait() }
func (o *visitorAuthWorkers) Status() []workers.Status { return o.runner.Status() }
