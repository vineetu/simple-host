package operations

import (
	"context"
	"fmt"
	"github.com/vsriram/simple-host/internal/analytics"
	"github.com/vsriram/simple-host/internal/workers"
	"sync"
	"time"
)

// AnalyticsIngest owns nginx-log ingestion timing and lifecycle.
type AnalyticsIngest struct {
	*analytics.Ingester
	startOnce sync.Once
	runner    *workers.Runner
	tracker   *workers.Tracker
	interval  time.Duration
}

func NewAnalyticsIngest(i *analytics.Ingester, interval time.Duration) *AnalyticsIngest {
	t := i.ProgressTracker()
	return &AnalyticsIngest{Ingester: i, tracker: t, runner: workers.NewRunner(t), interval: interval}
}
func (w *AnalyticsIngest) Stop()                    { w.runner.Stop() }
func (w *AnalyticsIngest) Wait()                    { w.runner.Wait() }
func (w *AnalyticsIngest) Status() []workers.Status { return w.runner.Status() }
func (w *AnalyticsIngest) Start(ctx context.Context) {
	w.startOnce.Do(func() {
		w.runner.Go(ctx, func(ctx context.Context) {
			timer := time.NewTimer(0)
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
				func() {
					w.tracker.Attempt()
					defer func() {
						if rec := recover(); rec != nil {
							w.tracker.Fail("ingest panic", fmt.Errorf("%v", rec))
						}
					}()
					ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
					defer cancel()
					if err := w.Ingester.RunOnce(ctx); err != nil {
						w.tracker.Fail("ingest", err)
					}
				}()
				timer.Reset(w.interval)
			}
		})
	})
}
