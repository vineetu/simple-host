package gate

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
)

// Operator take-down of a site, and suspension of a person.
//
// A suspended site keeps everything (files, versions, saved data, lists,
// addresses); it just stops being served and refuses changes, so the evidence
// the Terms promise to keep is kept and a restore puts it back exactly as it
// was. The database is the record (sites.suspended_at, users.suspended_at);
// a `suspended` marker file in the site folder is what every server that reads
// files straight from disk tests (Go here, nginx and Caddy for the content host
// and custom domains).

// refuseSuspendedSiteID is RefuseSuspendedSite for handlers that hold only
// the site id. A lookup error refuses too (fail closed).
func (h *Handler) refuseSuspendedSiteID(w http.ResponseWriter, r *http.Request, siteID string) bool {
	susp, reason, err := h.SitesSvc().SiteSuspension(r.Context(), siteID)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "site not found"})
		return true
	}
	if err != nil {
		httpx.WriteInternal(w)
		return true
	}
	if susp {
		web.WriteSiteSuspended(w, reason)
		return true
	}
	return false
}

// SyncSuspendMarkers makes every site's disk marker match the database. Run
// once at boot so a marker lost to a failed write, a restore from backup or a
// hand-applied migration cannot leave a taken-down site serving (or a
// restored one dark).
func (h *Handler) SyncSuspendMarkers(ctx context.Context) {
	h.SitesSvc().SyncSuspendMarkers(ctx)
}
