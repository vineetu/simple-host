package accesshttp

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
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
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

const maxSuspendReason = 300

func (h *AccessHandler) ServeTakedown(w http.ResponseWriter, r *http.Request) {
	web.ServeTakedownPage(w, r, h.pipeline())
}

// refuseSuspendedSiteID is RefuseSuspendedSite for handlers that hold only
// the site id. A lookup error refuses too (fail closed).
func (h *AccessHandler) refuseSuspendedSiteID(w http.ResponseWriter, r *http.Request, siteID string) bool {
	susp, reason, err := h.sitesSvc().SiteSuspension(r.Context(), siteID)
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

// suspendRequest is the body of the admin suspend calls.
type suspendRequest struct {
	Reason string `json:"reason"`
}

func readSuspendReason(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req suspendRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": `invalid JSON body (expected {"reason":"..."})`, "code": "invalid_request"})
		return "", false
	}
	reason := strings.Join(strings.Fields(req.Reason), " ")
	if reason == "" {
		httpx.WriteError(w, http.StatusBadRequest, "reason_required", "a one-line reason is required")
		return "", false
	}
	if utf8.RuneCountInString(reason) > maxSuspendReason {
		httpx.WriteError(w, http.StatusBadRequest, "reason_too_long", "reason is too long (300 characters at most)")
		return "", false
	}
	return reason, true
}

// SyncSuspendMarkers makes every site's disk marker match the database. Run
// once at boot so a marker lost to a failed write, a restore from backup or a
// hand-applied migration cannot leave a taken-down site serving (or a
// restored one dark).
func (h *AccessHandler) SyncSuspendMarkers(ctx context.Context) {
	h.sitesSvc().SyncSuspendMarkers(ctx)
}

// adminSiteJSON is how the admin calls report a site after a change.
func (h *AccessHandler) adminSiteJSON(s model.Site) map[string]any {
	return map[string]any{
		"id":               s.ID,
		"name":             s.Name,
		"owner":            s.OwnerUsername,
		"handle":           s.OwnerHandle,
		"site_url":         h.Hosts.Addressing().SiteURLFor(s),
		"suspended":        s.Suspended(),
		"suspended_reason": s.SuspendedReason(),
		"suspended_by":     web.SuspendedBy(s),
	}
}

// setSiteSuspension is POST /v1/admin/sites/{id}/suspend (on) and
// /v1/admin/sites/{id}/restore (off).
func (h *AccessHandler) setSiteSuspension(on bool) http.HandlerFunc {
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
		httpx.WriteJSON(w, http.StatusOK, h.adminSiteJSON(site))
	}
}

// setUserSuspension is POST /v1/admin/users/{id}/suspend (on) and
// /v1/admin/users/{id}/enable (off). Suspending keeps the account's keys,
// connected apps and sites; every check that authenticates refuses them while
// the flag is set, and every site of theirs is taken down.
func (h *AccessHandler) setUserSuspension(on bool) http.HandlerFunc {
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
				httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "not found"})
			} else if e, ok := err.(*appsites.Error); ok && e.Code == "event_account" {
				httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: e.Error(), Code: e.Code})
			} else {
				httpx.WriteInternal(w)
			}
			return
		}

		out, err := h.sitesSvc().SuspendAccount(r.Context(), appsites.SuspensionInput{ID: id, ActorID: auth.GetUser(r.Context()).ID, Reason: reason, On: on, Marker: h.sitesSvc().SyncSiteSuspensionMarker})
		if err != nil {
			var failure *accounts.LifecycleError
			if errors.As(err, &failure) && failure.Code == "admin_account" {
				httpx.WriteError(w, 400, failure.Code, failure.Message)
				return
			}
			writeLifecycleFailure(w, err)
			return
		}
		target := out.User

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"id":               target.ID,
			"username":         target.Username,
			"suspended":        on,
			"suspended_reason": reason,
			"site_count":       out.SiteCount,
		})
	}
}
