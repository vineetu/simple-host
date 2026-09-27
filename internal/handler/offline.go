package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
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

// offlinePage is served on the site's own origins (custom domains included),
// so it loads nothing and names no one.
const offlinePage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>Site offline</title>
<style>body{font:17px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:#1a2233;background:#fff;margin:0;padding:15vh 20px;text-align:center}h1{font-size:26px;margin:0 0 8px}p{color:#5b6576;margin:0}</style>
</head><body><h1>This site is offline</h1><p>Its owner has taken it offline for now.</p></body></html>
`

// serveOffline writes the offline page: 503 (a pause, not a removal) and
// no-store, so switching the site back on takes effect on the next load.
func serveOffline(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(http.StatusServiceUnavailable)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(offlinePage))
	}
}

// offlinePageHandler answers /internal/offline, where nginx and Caddy send a
// request for a site whose folder carries the offline marker.
func (h *SiteHandler) offlinePageHandler(w http.ResponseWriter, r *http.Request) {
	serveOffline(w, r)
}

func writeSiteOffline(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error": "this site is offline; its owner has paused it, and saves are refused until it is back online",
		"code":  "site_offline",
	})
}

// syncOfflineMarker makes the disk marker match the site's offline flag.
func (h *SiteHandler) syncOfflineMarker(site db.Site) error {
	return h.disk.SetOffline(site.UserID, site.Name, site.Offline)
}

// ownerKeyWrite: the request carries the site owner's (or the admin's) key.
func (h *SiteHandler) ownerKeyWrite(r *http.Request, ownerID string) bool {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		return false
	}
	u, ok, err := h.resolveWriterKey(r.Context(), key)
	return err == nil && ok && (u.IsAdmin || u.ID == ownerID)
}

// setSiteOffline is PATCH /v1/sites/{sitename} with {"offline": true|false}.
func (h *SiteHandler) setSiteOffline(w http.ResponseWriter, r *http.Request, name string, on bool) {
	user := auth.GetUser(r.Context())
	if _, err := db.GetSiteByUser(r.Context(), h.database, user.ID, name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// Same lock as a deploy, rename or delete: the marker is never written
	// into a folder that moved meanwhile.
	unlock := h.lockSite(user.ID, name)
	defer unlock()
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if refuseSuspendedSite(w, site) {
		return
	}
	if err := db.SetSiteOffline(r.Context(), h.database, site.ID, on); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	site.Offline = on
	if err := h.syncOfflineMarker(site); err != nil {
		log.Printf("offline: marker for site %s: %v", site.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "saved, but the site's files could not be marked; try again"})
		return
	}
	log.Printf("site_offline on=%t site_id=%s name=%s by=%s", on, site.ID, site.Name, user.ID)
	note := "The site is back online at every address."
	if on {
		note = "The site is offline: every address shows “This site is offline” and visitor saves are refused. Nothing is deleted; switch it back on at any time."
	}
	writeJSON(w, http.StatusOK, h.toSiteResponse(site, note))
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
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `invalid JSON body (expected {"name":"new-name"} or {"offline":true|false})`, Code: "invalid_request"})
		return
	}
	name := strings.TrimSpace(r.PathValue("sitename"))
	if req.Offline != nil {
		h.setSiteOffline(w, r, name, *req.Offline)
		return
	}
	h.renameSite(w, r, name, strings.TrimSpace(*req.Name))
}
