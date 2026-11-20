package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
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

// takedownPage is what every address of a suspended site answers. Plain on
// purpose: it is served on the site's own origins (custom domains included),
// so it loads nothing and names no one.
var takedownPage = themed(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><!--sh:theme--><title>Site taken down</title>
<style>body{font:17px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:#1a2233;background:#fff;margin:0;padding:15vh 20px;text-align:center}h1{font-size:26px;margin:0 0 8px}p{color:#5b6576;margin:0}html[data-theme=dark] body{color:#e6ebf3;background:#0b1222}html[data-theme=dark] p{color:#a3afc1}</style>
</head><body><h1>This site has been taken down</h1><p>It is no longer available.</p></body></html>
`)

// ServeTakedownForTest is serveTakedown, exported as a test hook (cleanup
// 3.2 slice 3): the moved hack_ink_browser_test.go (an opt-in Playwright
// rehearsal fixture) mounts it at a debug path.
func (h *SiteHandler) ServeTakedownForTest(w http.ResponseWriter, r *http.Request) {
	h.serveTakedown(w, r)
}

// serveTakedown writes the take-down page. 410 so it is not cached as the
// site's content; no-store so a restore takes effect on the next load.
func (h *SiteHandler) serveTakedown(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(http.StatusGone)
	if r.Method != http.MethodHead {
		_, _ = w.Write(h.pipeline().hostedStatusPage(takedownPage))
	}
}

// suspendedPage answers /internal/suspended, where nginx and Caddy send a
// request for a site whose folder carries the marker.
func (h *SiteHandler) suspendedPage(w http.ResponseWriter, r *http.Request) {
	h.serveTakedown(w, r)
}

func writeSiteSuspended(w http.ResponseWriter, reason string) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error":  "this site has been taken down by the operator; changes are refused until it is restored",
		"code":   "site_suspended",
		"reason": reason,
	})
}

// refuseSuspendedSite writes the refusal and returns true when the site (or
// its owner's account) is suspended.
func refuseSuspendedSite(w http.ResponseWriter, site model.Site) bool {
	if !site.Suspended() {
		return false
	}
	writeSiteSuspended(w, site.SuspendedReason())
	return true
}

// refuseSuspendedSiteID is refuseSuspendedSite for handlers that hold only
// the site id. A lookup error refuses too (fail closed).
func (h *SiteHandler) refuseSuspendedSiteID(w http.ResponseWriter, r *http.Request, siteID string) bool {
	susp, reason, err := h.sitesSvc().SiteSuspension(r.Context(), siteID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return true
	}
	if err != nil {
		writeInternal(w)
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
		writeProductErr(w, http.StatusBadRequest, "reason_required", "a one-line reason is required")
		return "", false
	}
	if utf8.RuneCountInString(reason) > maxSuspendReason {
		writeProductErr(w, http.StatusBadRequest, "reason_too_long", "reason is too long (300 characters at most)")
		return "", false
	}
	return reason, true
}

// syncSiteMarker makes the disk marker match the site's effective state.
func (h *SiteHandler) syncSiteMarker(site model.Site) error {
	return h.sitesSvc().SyncSiteSuspensionMarker(site)
}

// SyncSuspendMarkers makes every site's disk marker match the database. Run
// once at boot so a marker lost to a failed write, a restore from backup or a
// hand-applied migration cannot leave a taken-down site serving (or a
// restored one dark).
func (h *SiteHandler) SyncSuspendMarkers(ctx context.Context) { h.sitesSvc().SyncSuspendMarkers(ctx) }

// adminSiteJSON is how the admin calls report a site after a change.
func (h *SiteHandler) adminSiteJSON(s model.Site) map[string]any {
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
func suspendedBy(s model.Site) string {
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
		if !auth.Admin(w, r) {
			return
		}
		reason := ""
		if on {
			var ok bool
			if reason, ok = readSuspendReason(w, r); !ok {
				return
			}
		}
		site, err := h.sitesSvc().SuspendSite(r.Context(), r.PathValue("id"), reason, auth.GetUser(r.Context()).ID, on)
		if writeLifecycleFailure(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, h.adminSiteJSON(site))
	}
}

// setUserSuspension is POST /v1/admin/users/{id}/suspend (on) and
// /v1/admin/users/{id}/enable (off). Suspending keeps the account's keys,
// connected apps and sites; every check that authenticates refuses them while
// the flag is set, and every site of theirs is taken down.
func (h *SiteHandler) setUserSuspension(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth.Admin(w, r) {
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
		if err := h.sitesSvc().SuspensionGate(r.Context(), id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
			} else if e, ok := err.(*appsites.Error); ok && e.Code == "event_account" {
				writeJSON(w, http.StatusConflict, errorResponse{Error: e.Error(), Code: e.Code})
			} else {
				writeInternal(w)
			}
			return
		}

		out, err := h.sitesSvc().SuspendAccount(r.Context(), appsites.SuspensionInput{ID: id, ActorID: auth.GetUser(r.Context()).ID, Reason: reason, On: on, Marker: h.syncSiteMarker})
		if err != nil {
			var failure *accounts.LifecycleError
			if errors.As(err, &failure) && failure.Code == "admin_account" {
				writeProductErr(w, 400, failure.Code, failure.Message)
				return
			}
			writeLifecycleFailure(w, err)
			return
		}
		target := out.User

		writeJSON(w, http.StatusOK, map[string]any{
			"id":               target.ID,
			"username":         target.Username,
			"suspended":        on,
			"suspended_reason": reason,
			"site_count":       out.SiteCount,
		})
	}
}
