package handler

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/internal/app/sites"
)

// GroupSweep contributes product work around the platform's marker reconciliation.
type GroupSweep struct {
	Name  string
	Sweep func(context.Context)
	After func(context.Context)
}

func (h *SiteHandler) startGroupSweep(ctx context.Context, every time.Duration, sweep GroupSweep) {
	if sweep.Sweep == nil {
		return
	}
	o := h.siteLoop(sweep.Name)
	o.Go(ctx, func(ctx context.Context) {
		ctx = sites.WithWorkerReport(ctx, o.tracker)
		t := time.NewTicker(every)
		defer t.Stop()
		for n := 0; ; n++ {
			o.tracker.Attempt()
			groupSweepPass(ctx, n, sweep, h.SyncSuspendMarkers)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})
}

func groupSweepPass(ctx context.Context, n int, sweep GroupSweep, syncMarkers func(context.Context)) {
	sweep.Sweep(ctx)
	// Retry marker writes that failed after a take-down, every ten rounds.
	if n%10 == 9 {
		syncMarkers(ctx)
	}
	if sweep.After != nil {
		sweep.After(ctx)
	}
}
