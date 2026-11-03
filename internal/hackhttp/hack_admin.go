package hackhttp

import (
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/httpx"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

// hackAccountAdmin is handler's accountAdmin (accounts.go), duplicated here
// (cleanup 3.2 slice 1c) so the platform-admin endpoints below need no
// cross-package reference once HackHandler moves to internal/hackhttp.
// Same 401/404 shape (a signed-in non-admin should not learn the admin
// endpoint exists).
func hackAccountAdmin(w http.ResponseWriter, r *http.Request) bool {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return false
	}
	if !user.IsAdmin {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "not found"})
		return false
	}
	return true
}

func (h *HackHandler) adminListEvents(w http.ResponseWriter, r *http.Request) {
	if !hackAccountAdmin(w, r) {
		return
	}
	rows, err := h.events.AdminListEvents(r.Context())
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		exp := any(nil)
		if a.ExpectedParticipants.Valid {
			exp = a.ExpectedParticipants.Int64
		}
		out = append(out, map[string]any{
			"slug":                  a.Slug,
			"title":                 a.Title,
			"stage":                 a.Stage,
			"time_zone":             a.TimeZone,
			"organiser_name":        a.OrganiserName,
			"organisation":          a.Organisation,
			"contact_email":         a.ContactEmail,
			"purpose":               a.Purpose,
			"expected_participants": exp,
			"created_by_email":      a.CreatorEmail,
			"url":                   h.EventURL(a.Slug),
			"starts_at":             rfc3339UTC(a.StartsAt),
			"ends_at":               rfc3339UTC(a.EndsAt),
			"created_at":            rfc3339Time(a.CreatedAt),
			"taken_down":            a.TakenDown(),
			"taken_down_reason":     a.TakenDownReason,
			"taken_down_at":         rfc3339UTC(a.TakenDownAt),
			"participants":          a.Participants,
			"teams":                 a.Teams,
			"judges":                a.Judges,
			"counts": map[string]int{
				"participants": a.Participants,
				"teams":        a.Teams,
				"judges":       a.Judges,
			},
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": out})
}

func (h *HackHandler) adminTakeDown(w http.ResponseWriter, r *http.Request) {
	if !hackAccountAdmin(w, r) {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	reason := strings.Join(strings.Fields(req.Reason), " ")
	if reason == "" {
		httpx.WriteError(w, http.StatusBadRequest, "reason_required", "a reason is required")
		return
	}
	if utf8.RuneCountInString(reason) > 1000 {
		httpx.WriteError(w, http.StatusBadRequest, "reason_too_long", "reason is too long")
		return
	}
	ev, ok := h.adminLoadEvent(w, r)
	if !ok {
		return
	}
	updated, err := h.events.AdminModerate(r.Context(), ev, reason, false, h.syncEventMarkers)
	if err != nil {
		if e, ok := err.(*events.Error); ok && e.Code == "markers_failed" {
			httpx.WriteError(w, http.StatusInternalServerError, e.Code, e.Message)
		} else {
			httpx.WriteInternal(w)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.adminEventJSON(updated))
}

func (h *HackHandler) adminKeepSites(w http.ResponseWriter, r *http.Request) {
	if !hackAccountAdmin(w, r) {
		return
	}
	var req struct {
		Keep *bool `json:"keep"`
	}
	if !decodeHackJSON(w, r, &req) || req.Keep == nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", `body must be {"keep": true} or {"keep": false}`)
		return
	}
	ev, ok := h.adminLoadEvent(w, r)
	if !ok {
		return
	}
	updated, err := h.events.AdminKeepSites(r.Context(), ev.ID, *req.Keep)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"keep_sites": updated.KeepSites})
}

func (h *HackHandler) adminRestore(w http.ResponseWriter, r *http.Request) {
	if !hackAccountAdmin(w, r) {
		return
	}
	ev, ok := h.adminLoadEvent(w, r)
	if !ok {
		return
	}
	updated, err := h.events.AdminModerate(r.Context(), ev, "", true, h.syncEventMarkers)
	if err != nil {
		if e, ok := err.(*events.Error); ok && e.Code == "markers_failed" {
			httpx.WriteError(w, http.StatusInternalServerError, e.Code, e.Message)
		} else {
			httpx.WriteInternal(w)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.adminEventJSON(updated))
}

func (h *HackHandler) adminDelete(w http.ResponseWriter, r *http.Request) {
	if !hackAccountAdmin(w, r) {
		return
	}
	ev, ok := h.adminLoadEvent(w, r)
	if !ok {
		return
	}
	if err := h.events.AdminDelete(r.Context(), ev, h.removeEventFiles); err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HackHandler) adminLoadEvent(w http.ResponseWriter, r *http.Request) (db.Event, bool) {
	ev, err := h.events.AdminEvent(r.Context(), r.PathValue("slug"))
	if err == nil {
		paperHackEvent(r, ev)
	}
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "not found"})
		return db.Event{}, false
	}
	if err != nil {
		httpx.WriteInternal(w)
		return db.Event{}, false
	}
	return ev, true
}

func (h *HackHandler) adminEventJSON(ev db.Event) map[string]any {
	return map[string]any{
		"slug":              ev.Slug,
		"title":             ev.Title,
		"stage":             ev.Stage,
		"taken_down":        ev.TakenDown(),
		"taken_down_reason": ev.TakenDownReason,
		"taken_down_at":     rfc3339UTC(ev.TakenDownAt),
		"url":               h.EventURL(ev.Slug),
	}
}
