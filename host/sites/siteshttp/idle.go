package siteshttp

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/host/sites/idle"
)

// Idle-site cleanup (no emails are sent).
//
// A site with no visits by people, no new version and no saved-data or list
// write for idleAfter + idleGrace (90 + 30 days by default) moves to Recently
// deleted (the usual 7-day restore window), unless its owner marked it Keep
// (owner app, PUT /v1/sites/{site}/keep, MCP keep_site). Nobody is emailed.
// Exempt: sites with a custom domain or a claimed name, admin, reviewer and
// event accounts' sites, handles in IDLE_CLEANUP_EXEMPT_HANDLES, taken-down
// sites and suspended accounts, sites their owner took offline, preview sites
// (repository/sites/ops_idle.go has the rule). Activity is a person's visit, a
// new version, a saved-data or list write, or a restore.
//
// Off unless IDLE_CLEANUP=on. The admin page's dry run (GET
// /v1/admin/idle-sites) lists what a run would do either way. A removal
// re-checks every condition in the transaction that moves the site, so a Keep,
// deploy or domain that lands mid-run wins. Nothing runs while visit data
// cannot be trusted: fewer than idleAfter + idleGrace days of analytics on
// record, or an ingester that has not run for six hours.

const idleRunEvery = 6 * time.Hour

func (h *SiteHandler) startIdleCleanup(ctx context.Context) {
	o := h.siteLoop("sites.idle_cleanup")
	o.periodicTicker(ctx, 10*time.Minute, idleRunEvery, func(ctx context.Context) { idle.Run(ctx, h.Handler, time.Now()) })
}
