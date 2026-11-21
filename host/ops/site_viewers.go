package ops

import (
	"context"
	"database/sql"
	"log"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/data/notify"
)

// RemoveSiteViewer removes a named viewer from a site and, in the same call,
// that person's push subscriptions for it: every caller gets the cleanup. A
// push failure is logged, never returned, since the viewer is already
// removed; delivery-time membership checking (ProcessDelivery) remains the
// backstop. push may be nil (no notifications on this server).
func RemoveSiteViewer(ctx context.Context, database *sql.DB, push *notify.Engine, siteID, email string) (bool, error) {
	removed, err := accountsrepo.RemoveSiteViewer(ctx, database, siteID, email)
	if err != nil || !removed {
		return removed, err
	}
	if push != nil {
		if pushErr := push.DeleteViewerPush(ctx, siteID, email); pushErr != nil {
			log.Printf("remove_site_viewer: push cleanup for site %s, %s: %v", siteID, email, pushErr)
		}
	}
	return removed, nil
}
