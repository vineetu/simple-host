package siteshttp

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

// Taking a site offline (the owner's switch).
//
// An offline site keeps everything (files, versions, saved data, lists,
// addresses). Every address answers a plain "This site is offline" page and
// visitor saves are refused; the owner's key and connector still deploy, read
// and write, so the site can be prepared and switched back on at will. The
// database is the record (sites.offline_at); an `offline` marker file in the
// site folder is what the servers that read files straight from disk test,
// after the operator's take-down marker, which wins.

// ServeOfflineForTest is ServeOffline, exported as a test hook (cleanup 3.2
// slice 3): the moved hack_ink_browser_test.go (an opt-in Playwright
// rehearsal fixture) mounts it at a debug path.
func (h *SiteHandler) ServeOfflineForTest(w http.ResponseWriter, r *http.Request) {
	h.ServeOffline(w, r)
}

// ServeOffline writes the offline page: 503 (a pause, not a removal) and
// no-store, so switching the site back on takes effect on the next load.
func (h *SiteHandler) ServeOffline(w http.ResponseWriter, r *http.Request) {
	web.ServeOfflinePage(w, r, h.Pipeline())
}

// offlinePageHandler answers /internal/offline, where nginx and Caddy send a
// request for a site whose folder carries the offline marker.
func (h *SiteHandler) offlinePageHandler(w http.ResponseWriter, r *http.Request) {
	h.ServeOffline(w, r)
}

// syncOfflineMarker makes the disk marker match the site's offline flag.
func (h *SiteHandler) syncOfflineMarker(site model.Site) error {
	return h.Disk.SetOffline(site.UserID, site.Name, site.Offline)
}

// setSiteOffline is PATCH /v1/sites/{sitename} with {"offline": true|false}.
func (h *SiteHandler) setSiteOffline(w http.ResponseWriter, r *http.Request, name string, on bool) {
	user := auth.GetUser(r.Context())
	if _, err := h.SitesSvc().GetSiteByUser(r.Context(), user.ID, name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "site not found"})
			return
		}
		httpx.WriteInternal(w)
		return
	}
	// Same lock as a deploy, rename or delete: the marker is never written
	// into a folder that moved meanwhile.
	unlock := h.LockSite(user.ID, name)
	defer unlock()
	site, err := h.SitesSvc().GetSiteByUser(r.Context(), user.ID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "site not found"})
			return
		}
		httpx.WriteInternal(w)
		return
	}
	if web.RefuseSuspendedSite(w, site) {
		return
	}
	if err := h.SitesSvc().SetSiteOffline(r.Context(), site.ID, on); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "site not found"})
			return
		}
		httpx.WriteInternal(w)
		return
	}
	site.Offline = on
	if err := h.syncOfflineMarker(site); err != nil {
		log.Printf("offline: marker for site %s: %v", site.ID, err)
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.ErrorResponse{Error: "saved, but the site's files could not be marked; try again"})
		return
	}
	log.Printf("site_offline on=%t site_id=%s name=%s by=%s", on, site.ID, site.Name, user.ID)
	note := "The site is back online at every address."
	if on {
		note = "The site is offline: every address shows “This site is offline” and visitor saves are refused. Nothing is deleted; switch it back on at any time."
	}
	httpx.WriteJSON(w, http.StatusOK, h.ToSiteResponse(site, note))
}

// patchSite is PATCH /v1/sites/{sitename}: {"name": "..."} renames the site,
// {"offline": true|false} takes it offline or back online. One change per
// request.
func (h *SiteHandler) patchSite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    *string `json:"name"`
		Offline *bool   `json:"offline"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || (req.Name == nil) == (req.Offline == nil) {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: `invalid JSON body (expected {"name":"new-name"} or {"offline":true|false})`, Code: "invalid_request"})
		return
	}
	name := strings.TrimSpace(r.PathValue("sitename"))
	if req.Offline != nil {
		h.setSiteOffline(w, r, name, *req.Offline)
		return
	}
	h.renameSite(w, r, name, strings.TrimSpace(*req.Name))
}
