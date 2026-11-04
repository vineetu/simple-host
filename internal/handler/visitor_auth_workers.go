package handler

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/workers"
)

type visitorAuthWorkers struct {
	h       *OAuthHandler
	runner  *workers.Runner
	tracker *workers.Tracker
}

// VisitorAuthWorkers owns expiry of visitor sign-in codes and sessions.
func (h *OAuthHandler) VisitorAuthWorkers() workers.Owner {
	t := workers.NewTracker("accounts.visitor_auth")
	return &visitorAuthWorkers{h, workers.NewRunner(t), t}
}
func (o *visitorAuthWorkers) Start(ctx context.Context) {
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
			err := db.SweepVisitorAuth(runCtx, o.h.database)
			cancel()
			if err != nil {
				o.tracker.Fail("sweep expired visitor authentication", err)
			}
			timer.Reset(time.Hour)
		}
	})
}
func (o *visitorAuthWorkers) Stop()                    { o.runner.Stop() }
func (o *visitorAuthWorkers) Wait()                    { o.runner.Wait() }
func (o *visitorAuthWorkers) Status() []workers.Status { return o.runner.Status() }
