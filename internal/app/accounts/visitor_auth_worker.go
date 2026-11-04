package accounts

import (
	"context"
	"sync"
	"time"

	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/workers"
)

type VisitorAuthWorker struct {
	startOnce sync.Once
	service   *Service
	runner    *workers.Runner
	tracker   *workers.Tracker
}

// NewVisitorAuthWorker owns expiry of visitor sign-in codes and sessions.
func NewVisitorAuthWorker(service *Service) *VisitorAuthWorker {
	t := workers.NewTracker("accounts.visitor_auth")
	return &VisitorAuthWorker{service: service, runner: workers.NewRunner(t), tracker: t}
}
func (o *VisitorAuthWorker) Start(ctx context.Context) {
	o.startOnce.Do(func() {
		if o.service.database == nil {
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
				n, err := repo.SweepVisitorAuthCount(runCtx, o.service.database)
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
func (o *VisitorAuthWorker) Stop()                    { o.runner.Stop() }
func (o *VisitorAuthWorker) Wait()                    { o.runner.Wait() }
func (o *VisitorAuthWorker) Status() []workers.Status { return o.runner.Status() }
