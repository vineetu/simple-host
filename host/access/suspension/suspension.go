package suspension

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

const maxSuspendReason = 300

func (h *Handler) ServeTakedown(w http.ResponseWriter, r *http.Request) {
	web.ServeTakedownPage(w, r, h.Pages)
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

// adminSiteJSON is how the admin calls report a site after a change.
func (h *Handler) adminSiteJSON(s model.Site) map[string]any {
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
func (h *Handler) setSiteSuspension(on bool) http.HandlerFunc {
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
		site, err := h.Sites.SuspendSite(r.Context(), r.PathValue("id"), reason, auth.GetUser(r.Context()).ID, on)
		if auth.WriteLifecycleFailure(w, err) {
			return
		}
		httpx.WriteJSON(w, http.StatusOK, h.adminSiteJSON(site))
	}
}

// setUserSuspension is POST /v1/admin/users/{id}/suspend (on) and
// /v1/admin/users/{id}/enable (off). Suspending keeps the account's keys,
// connected apps and sites; every check that authenticates refuses them while
// the flag is set, and every site of theirs is taken down.
func (h *Handler) setUserSuspension(on bool) http.HandlerFunc {
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
		target, siteCount, err := h.Cutoff.SuspendPerson(r.Context(), id, auth.GetUser(r.Context()).ID, reason, on)
		if auth.WriteLifecycleFailure(w, err) {
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"id":               target.ID,
			"username":         target.Username,
			"suspended":        on,
			"suspended_reason": reason,
			"site_count":       siteCount,
		})
	}
}

// Handler serves the operator's take-down of a site and suspension of a
// person, and the page a taken-down site shows.
type Handler struct {
	Hosts  *sites.Hosts
	Sites  *sites.Service
	Cutoff ops.Cutoff
	Pages  *web.AssetPipeline
}

// Register mounts the routes: restore / enable reverses a take-down;
// GET /internal/suspended is where nginx and Caddy send a request for a site
// whose folder carries the marker.
func (h *Handler) Register(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/admin/sites/{id}/suspend", authMiddleware(h.setSiteSuspension(true)))
	mux.Handle("POST /v1/admin/sites/{id}/restore", authMiddleware(h.setSiteSuspension(false)))
	mux.Handle("POST /v1/admin/users/{id}/suspend", authMiddleware(h.setUserSuspension(true)))
	mux.Handle("POST /v1/admin/users/{id}/enable", authMiddleware(h.setUserSuspension(false)))
	mux.HandleFunc("GET /internal/suspended", h.ServeTakedown)
}
