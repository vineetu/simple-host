package siteshttp

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
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

// ServeOffline writes the offline page: 503 (a pause, not a removal) and
// no-store, so switching the site back on takes effect on the next load.
func (h *SiteHandler) ServeOffline(w http.ResponseWriter, r *http.Request) {
	web.ServeOfflinePage(w, r, h.Pages())
}

func (h *SiteHandler) setSiteOffline(w http.ResponseWriter, r *http.Request, name string, on bool) {
	user := auth.GetUser(r.Context())
	site, err := h.SitesSvc().SetSiteOffline(r.Context(), user.ID, name, on)
	if err != nil {
		writeSitesError(w, err, name)
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
