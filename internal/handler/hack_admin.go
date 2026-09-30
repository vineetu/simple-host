package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/db"
)

func (h *HackHandler) adminListEvents(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	rows, err := db.ListAdminEvents(r.Context(), h.database)
	if err != nil {
		writeInternal(w)
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
			"counts": map[string]int{
				"participants": a.Participants,
				"teams":        a.Teams,
				"judges":       a.Judges,
			},
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *HackHandler) adminTakeDown(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
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
		writeHackErr(w, http.StatusBadRequest, "reason_required", "a reason is required")
		return
	}
	if utf8.RuneCountInString(reason) > 1000 {
		writeHackErr(w, http.StatusBadRequest, "reason_too_long", "reason is too long")
		return
	}
	ev, ok := h.adminLoadEvent(w, r)
	if !ok {
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	updated, err := db.TakeDownEvent(r.Context(), tx, ev.ID, reason)
	if err != nil {
		writeInternal(w)
		return
	}
	if err := db.SetUserSuspended(r.Context(), tx, ev.AccountID, reason); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, h.adminEventJSON(updated))
}

func (h *HackHandler) adminRestore(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	ev, ok := h.adminLoadEvent(w, r)
	if !ok {
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	updated, err := db.RestoreEvent(r.Context(), tx, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if err := db.SetUserSuspended(r.Context(), tx, ev.AccountID, ""); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, h.adminEventJSON(updated))
}

func (h *HackHandler) adminDelete(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	ev, ok := h.adminLoadEvent(w, r)
	if !ok {
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	if err := db.DeleteEventAndAccount(r.Context(), tx, ev); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HackHandler) adminLoadEvent(w http.ResponseWriter, r *http.Request) (db.Event, bool) {
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	ev, err := db.GetEventBySlug(r.Context(), h.database, slug)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return db.Event{}, false
	}
	if err != nil {
		writeInternal(w)
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
