package hack

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/products/hack/app/teams"
)

// NewSiteSweep pins deadlines and removes orphan sites before the platform
// reconciles markers and runs the certificate/entry-settling callback.
func NewSiteSweep(db *sql.DB, site *handler.SiteHandler, after func(context.Context)) handler.GroupSweep {
	svc := teams.New(db)
	return handler.GroupSweep{
		Name: "sites.hack_sweep",
		Sweep: func(ctx context.Context) {
			svc.SweepSites(ctx, site.TrashTeamSite, func() { sites.ReportWorkerProgress(ctx) }, func(op string, err error) { sites.ReportWorkerFailure(ctx, op, err) })
		},
		After: after,
	}
}
