package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
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

const maxSuspendReason = 300

// uuidShape guards the id path values before they reach a uuid column.
var uuidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// takedownPage is what every address of a suspended site answers. Plain on
// purpose: it is served on the site's own origins (custom domains included),
// so it loads nothing and names no one.
var takedownPage = themed(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><!--sh:theme--><title>Site taken down</title>
<style>body{font:17px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:#1a2233;background:#fff;margin:0;padding:15vh 20px;text-align:center}h1{font-size:26px;margin:0 0 8px}p{color:#5b6576;margin:0}html[data-theme=dark] body{color:#e6ebf3;background:#0b1222}html[data-theme=dark] p{color:#a3afc1}</style>
</head><body><h1>This site has been taken down</h1><p>It is no longer available.</p></body></html>
`)

// serveTakedown writes the take-down page. 410 so it is not cached as the
// site's content; no-store so a restore takes effect on the next load.
func serveTakedown(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(http.StatusGone)
	if r.Method != http.MethodHead {
		_, _ = w.Write(hostedStatusPage(takedownPage))
	}
}

// suspendedPage answers /internal/suspended, where nginx and Caddy send a
// request for a site whose folder carries the marker.
func (h *SiteHandler) suspendedPage(w http.ResponseWriter, r *http.Request) {
	serveTakedown(w, r)
}

func writeSiteSuspended(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error":  "this site has been taken down by the operator; changes are refused until it is restored",
		"code":   "site_suspended",
		"reason": reason,
	})
}

// writeAccountSuspended answers a request made as a suspended person. The
// reason is not echoed here (it is shown to them on the dashboard's sign-in
// refusal); the code is what clients act on.
func writeAccountSuspended(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error": "this account has been suspended by the operator",
		"code":  "account_suspended",
	})
}

// refuseSuspendedSite writes the refusal and returns true when the site (or
// its owner's account) is suspended.
func refuseSuspendedSite(w http.ResponseWriter, site db.Site) bool {
	if !site.Suspended() {
		return false
	}
	writeSiteSuspended(w, site.SuspendedReason())
	return true
}

// refuseSuspendedSiteID is refuseSuspendedSite for handlers that hold only
// the site id. A lookup error refuses too (fail closed).
func (h *SiteHandler) refuseSuspendedSiteID(w http.ResponseWriter, r *http.Request, siteID string) bool {
	susp, reason, err := db.SiteSuspension(r.Context(), h.database, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return true
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return true
	}
	if susp {
		writeSiteSuspended(w, reason)
		return true
	}
	return false
}

// suspendRequest is the body of the admin suspend calls.
type suspendRequest struct {
	Reason string `json:"reason"`
}

func readSuspendReason(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req suspendRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `invalid JSON body (expected {"reason":"..."})`, "code": "invalid_request"})
		return "", false
	}
	reason := strings.Join(strings.Fields(req.Reason), " ")
	if reason == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a one-line reason is required", "code": "reason_required"})
		return "", false
	}
	if utf8.RuneCountInString(reason) > maxSuspendReason {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reason is too long (300 characters at most)", "code": "reason_too_long"})
		return "", false
	}
	return reason, true
}

// syncSiteMarker makes the disk marker match the site's effective state.
func (h *SiteHandler) syncSiteMarker(site db.Site) error {
	return h.disk.SetSuspended(site.UserID, site.Name, site.Suspended())
}

// SyncSuspendMarkers makes every site's disk marker match the database. Run
// once at boot so a marker lost to a failed write, a restore from backup or a
// hand-applied migration cannot leave a taken-down site serving (or a
// restored one dark).
func (h *SiteHandler) SyncSuspendMarkers(ctx context.Context) {
	sites, err := db.ListAllSites(ctx, h.database)
	if err != nil {
		log.Printf("suspend: marker sync skipped: %v", err)
		return
	}
	n := 0
	for _, s := range sites {
		if s.Suspended() != h.disk.IsSuspended(s.UserID, s.Name) {
			if err := h.syncSiteMarker(s); err != nil {
				log.Printf("suspend: marker for %s/%s: %v", s.UserID, s.Name, err)
				continue
			}
			n++
		}
		// The owner's offline switch (offline.go) has a marker too.
		if s.Offline != h.disk.IsOffline(s.UserID, s.Name) {
			if err := h.syncOfflineMarker(s); err != nil {
				log.Printf("offline: marker for %s/%s: %v", s.UserID, s.Name, err)
				continue
			}
			n++
		}
		// And a passcode (passcode.go).
		if s.Passcode != h.disk.HasPasscodeMarker(s.UserID, s.Name) {
			if err := h.syncPasscodeMarker(s); err != nil {
				log.Printf("passcode: marker for %s/%s: %v", s.UserID, s.Name, err)
				continue
			}
			n++
		}
	}
	if n > 0 {
		log.Printf("suspend: corrected %d site marker(s)", n)
	}
}

// adminSiteJSON is how the admin calls report a site after a change.
func (h *SiteHandler) adminSiteJSON(s db.Site) map[string]any {
	return map[string]any{
		"id":               s.ID,
		"name":             s.Name,
		"owner":            s.OwnerUsername,
		"handle":           s.OwnerHandle,
		"site_url":         h.siteURLFor(s),
		"suspended":        s.Suspended(),
		"suspended_reason": s.SuspendedReason(),
		"suspended_by":     suspendedBy(s),
	}
}

// suspendedBy says which switch holds a site down: "site", "account", or "".
func suspendedBy(s db.Site) string {
	switch {
	case s.SiteSuspended:
		return "site"
	case s.OwnerSuspended:
		return "account"
	}
	return ""
}

// setSiteSuspension is POST /v1/admin/sites/{id}/suspend (on) and
// /v1/admin/sites/{id}/restore (off).
func (h *SiteHandler) setSiteSuspension(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !accountAdmin(w, r) {
			return
		}
		reason := ""
		if on {
			var ok bool
			if reason, ok = readSuspendReason(w, r); !ok {
				return
			}
		}
		id := r.PathValue("id")
		site, err := db.GetSiteByID(r.Context(), h.database, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		// Lock, then re-read: a rename or delete that finished while this
		// waited would otherwise be undone by the stale copy (a marker, and
		// folders, written under the old name).
		for {
			unlock := h.lockSite(site.UserID, site.Name)
			fresh, err := db.GetSiteByID(r.Context(), h.database, id)
			if err != nil {
				unlock()
				if errors.Is(err, sql.ErrNoRows) {
					writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
					return
				}
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
			if fresh.UserID == site.UserID && fresh.Name == site.Name {
				site = fresh
				defer unlock()
				break
			}
			unlock()
			site = fresh
		}
		if err := db.SetSiteSuspended(r.Context(), h.database, site.ID, reason); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		site.SiteSuspended, site.SiteSuspendedReason = on, reason
		// A site in Recently deleted has no served folder; Restore writes its
		// marker from the database when it comes back.
		if site.Deleted {
			admin := auth.GetUser(r.Context())
			log.Printf("admin_site_suspend on=%t site_id=%s name=%s owner=%s by=%s reason=%q deleted=true", on, site.ID, site.Name, site.UserID, admin.ID, reason)
			writeJSON(w, http.StatusOK, h.adminSiteJSON(site))
			return
		}
		if err := h.syncSiteMarker(site); err != nil {
			log.Printf("suspend: marker for site %s: %v", site.ID, err)
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "saved, but the site's files could not be marked; retry"})
			return
		}
		admin := auth.GetUser(r.Context())
		log.Printf("admin_site_suspend on=%t site_id=%s name=%s owner=%s by=%s reason=%q", on, site.ID, site.Name, site.UserID, admin.ID, reason)
		writeJSON(w, http.StatusOK, h.adminSiteJSON(site))
	}
}

// setUserSuspension is POST /v1/admin/users/{id}/suspend (on) and
// /v1/admin/users/{id}/enable (off). Suspending keeps the account's keys,
// connected apps and sites; every check that authenticates refuses them while
// the flag is set, and every site of theirs is taken down.
func (h *SiteHandler) setUserSuspension(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !accountAdmin(w, r) {
			return
		}
		reason := ""
		if on {
			var ok bool
			if reason, ok = readSuspendReason(w, r); !ok {
				return
			}
		}
		id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
		if !uuidShape.MatchString(id) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
			return
		}
		if held, herr := hackHoldingAccount(r.Context(), h.database, id); herr != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		} else if held {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "this account holds an event; take the event down or restore it from the admin Events tab", Code: "event_account"})
			return
		}
		target, err := db.GetUserByID(r.Context(), h.database, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if on && (target.IsAdmin || target.ID == h.adminUserID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot suspend an admin account", "code": "admin_account"})
			return
		}
		if err := db.SetUserSuspended(r.Context(), h.database, target.ID, reason); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		sites, err := db.ListSitesByUser(r.Context(), h.database, target.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "saved, but the account's sites could not be listed; retry"})
			return
		}
		failed := 0
		for _, s := range sites {
			if err := h.syncSiteMarker(s); err != nil {
				log.Printf("suspend: marker for %s/%s: %v", s.UserID, s.Name, err)
				failed++
			}
		}
		if failed > 0 {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "saved, but some of the account's sites could not be marked; retry"})
			return
		}
		admin := auth.GetUser(r.Context())
		log.Printf("admin_user_suspend on=%t user_id=%s sites=%d by=%s reason=%q", on, target.ID, len(sites), admin.ID, reason)
		writeJSON(w, http.StatusOK, map[string]any{
			"id":               target.ID,
			"username":         target.Username,
			"suspended":        on,
			"suspended_reason": reason,
			"site_count":       len(sites),
		})
	}
}
