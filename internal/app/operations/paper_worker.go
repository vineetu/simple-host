package operations

import (
	"context"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"github.com/vsriram/simple-host/internal/workers"
	"sync"
	"time"
)

// PaperMetrics owns process samples, aggregate flushes and paper snapshots.
type PaperMetrics struct {
	collector *papermetrics.Collector
	startOnce sync.Once
	runner    *workers.Runner
	tracker   *workers.Tracker
}

func NewPaperMetrics(c *papermetrics.Collector) *PaperMetrics {
	t := c.ProgressTracker()
	return &PaperMetrics{collector: c, tracker: t, runner: workers.NewRunner(t)}
}
func (c *PaperMetrics) Start(ctx context.Context) {
	c.startOnce.Do(func() {
		if !papermetrics.Enabled() {
			return
		}
		c.runner.Go(ctx, func(ctx context.Context) {
			c.collector.InitializeRun(ctx)
			if c.collector.HackEnabled() {
				c.runner.Go(ctx, c.hackLoop)
			}
			c.loop(ctx)
		})
	})
}
func (c *PaperMetrics) Stop()                    { c.runner.Stop() }
func (c *PaperMetrics) Wait()                    { c.runner.Wait() }
func (c *PaperMetrics) Status() []workers.Status { return c.runner.Status() }
func (c *PaperMetrics) loop(ctx context.Context) {
	c.tracker.Attempt()
	flush := time.NewTicker(time.Minute)
	sampleT := time.NewTicker(5 * time.Minute)
	snap := time.NewTicker(time.Hour)
	defer flush.Stop()
	defer sampleT.Stop()
	defer snap.Stop()
	c.collector.TakeSample(ctx)
	c.snapshots(ctx)
	for {
		select {
		case <-ctx.Done():
			c.collector.Flush(context.Background())
			return
		case <-flush.C:
			c.tracker.Attempt()
			c.collector.Flush(ctx)
		case <-sampleT.C:
			c.tracker.Attempt()
			c.collector.TakeSample(ctx)
		case <-snap.C:
			c.tracker.Attempt()
			c.snapshots(ctx)
		}
	}
}
func (c *PaperMetrics) snapshots(ctx context.Context) {
	c.tracker.Attempt()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := c.collector.DailySnapshot(ctx, time.Now().UTC()); err != nil {
		c.tracker.Fail("daily snapshot", err)
	}
	if c.collector.HackEnabled() {
		if err := c.collector.HackSnapshot(ctx, time.Now().UTC()); err != nil {
			c.tracker.Fail("Hack snapshot", err)
		}
	}
	if err := c.collector.WeeklyFootprint(ctx, time.Now().UTC()); err != nil {
		c.tracker.Fail("footprint", err)
	}
}

func (c *PaperMetrics) hackLoop(ctx context.Context) {
	tick := time.NewTimer(time.Until(time.Now().UTC().Truncate(time.Minute).Add(time.Minute)))
	defer tick.Stop()
	c.collector.RefreshHackBackground(ctx)
	for {
		select {
		case <-ctx.Done():
			finalCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			c.collector.FlushHack(finalCtx, time.Now().UTC())
			cancel()
			return
		case <-c.collector.HackRefresh():
			c.collector.RefreshHackBackground(ctx)
		case now := <-tick.C:
			c.collector.FlushHack(ctx, now)
			c.collector.RefreshHackBackground(ctx)
			tick.Reset(time.Until(time.Now().UTC().Truncate(time.Minute).Add(time.Minute)))
		}
	}
}
