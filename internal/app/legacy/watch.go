package legacy

import (
	"context"
	"github.com/vsriram/simple-host/internal/repository/legacy"
	"log"
)

// Watch records a positive metric count without failing the caller.
func (a *Adapter) Watch(ctx context.Context, siteID, metric string, n int64) {
	if n <= 0 {
		return
	}
	if err := legacy.BumpDataWatch(ctx, a.database, siteID, metric, n); err != nil {
		log.Printf("data_watch site_id=%s metric=%s: %v", siteID, metric, err)
	}
}

// WatchItemSize records an item exceeding the configured size threshold.
func (a *Adapter) WatchItemSize(ctx context.Context, siteID string, size, thresholdKB int, metric string) {
	if size > thresholdKB<<10 {
		a.Watch(ctx, siteID, metric, 1)
	}
}
